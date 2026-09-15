"""Private, short-lived Apple TV worker. JSON over stdio; no listening socket.

All network traffic is explicitly internal: Companion and mDNS use direct LAN
sockets. Never enable pyatv logging or its plaintext FileStorage. Credentials
travel only in the private pipe, and exist only in memory in this process.
"""

import asyncio
import importlib.metadata
import ipaddress
import json
import logging
import re
import sys

import pyatv
from pyatv.const import OperatingSystem, Protocol
from pyatv.exceptions import AuthenticationError, PairingError
from pyatv.protocols.companion.protocol import CompanionProtocol

VERSION = "0.18.0"
INFUSE = "com.firecore.infuse"
logging.disable(logging.CRITICAL)


class Failure(Exception):
    def __init__(self, code):
        self.code = code


def validate_response(response):
    # pyatv 0.18.0 only rejects _em. tvOS can instead return _ec/_eu, causing
    # launch_app to return normally after a rejection (upstream issue #2868).
    # _rT is deliberately NOT interpreted as a boolean: valid replies use 2.
    nested = response.get("_eu")
    if response.get("_ec") or (isinstance(nested, dict) and nested.get("_ec")):
        raise Failure("launch_failed")
    return response


def install_response_check():
    if importlib.metadata.version("pyatv") != VERSION:
        raise Failure("unsupported")
    original = CompanionProtocol._exchange_generic_opack

    async def checked(self, *args, **kwargs):
        return validate_response(await original(self, *args, **kwargs))

    CompanionProtocol._exchange_generic_opack = checked


def emit(**values):
    print(json.dumps(values), flush=True)


async def receive():
    line = await asyncio.to_thread(sys.stdin.buffer.readline, 16385)
    if not line or len(line) > 16384:
        raise Failure("invalid_request")
    value = json.loads(line)
    if not isinstance(value, dict):
        raise Failure("invalid_request")
    return value


def valid_host(value):
    if not isinstance(value, str) or not value or len(value) > 253:
        return False
    try:
        return isinstance(ipaddress.ip_address(value), ipaddress.IPv4Address)
    except ValueError:
        return bool(re.fullmatch(r"[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9.])?", value))


def title_uri(media_type, tmdb_id):
    if media_type not in ("movie", "tv") or type(tmdb_id) is not int or tmdb_id <= 0:
        raise Failure("invalid_request")
    kind = "movie" if media_type == "movie" else "series"
    return f"infuse://{kind}/{tmdb_id}"


def device_view(config):
    return {"name": config.name, "address": str(config.address),
            "identifier": config.identifier or ""}


async def discover(address=""):
    loop = asyncio.get_running_loop()
    hosts = None
    if address:
        if not valid_host(address):
            raise Failure("invalid_request")
        addresses = await loop.getaddrinfo(address, None, family=2)
        hosts = list(dict.fromkeys(result[4][0] for result in addresses))[:4]

    def matching_tvs(configs):
        return [config for config in configs if config.identifier and
                (hosts is None or str(config.address) in hosts) and
                config.device_info.operating_system == OperatingSystem.TvOS and
                config.get_service(Protocol.Companion) is not None and
                config.get_service(Protocol.Companion).enabled][:64]

    found = matching_tvs(await pyatv.scan(
        loop, hosts=hosts, protocol=Protocol.Companion, timeout=6))
    if hosts and not found:
        # Apple TV ignores off-subnet unicast mDNS. A gateway's mDNS relay can
        # make multicast discovery work there, so use it for pairing and every
        # reconnect too. Keep the address restriction and resolve's identity
        # check; another TV visible through the relay is not a substitute.
        found = matching_tvs(await pyatv.scan(
            loop, protocol=Protocol.Companion, timeout=6))
    return found


async def resolve(request):
    if not request.get("address") or not request.get("identifier"):
        raise Failure("invalid_request")
    found = await discover(request["address"])
    matches = [config for config in found if request["identifier"] in config.all_identifiers]
    if not matches:
        # A different TV at an old IP must not receive a stored pairing key.
        raise Failure("identity_changed" if found else "unreachable")
    if len(matches) != 1:
        raise Failure("identity_changed")
    return matches[0]


async def run():
    install_response_check()
    request = await receive()
    action = request.get("action")
    if action == "discover":
        found = await discover(request.get("address", ""))
        emit(state="complete", devices=[device_view(config) for config in found],
             scope="address" if request.get("address") else "local_network")
        return
    if action not in ("pair", "check", "open", "select"):
        raise Failure("invalid_request")
    config = await resolve(request)
    if action == "pair":
        pairing = await pyatv.pair(config, Protocol.Companion,
                                  asyncio.get_running_loop(), name="Cantinarr")
        try:
            await pairing.begin()
            emit(state="pin_required", device=device_view(config))
            finish = await receive()
            pin = finish.get("pin", "")
            if not isinstance(pin, str) or not re.fullmatch(r"[0-9]{4}", pin):
                raise Failure("invalid_pin")
            pairing.pin(int(pin))
            await pairing.finish()
            if not pairing.has_paired:
                raise Failure("pairing_failed")
            emit(state="paired", device=device_view(config),
                 credentials=config.get_service(Protocol.Companion).credentials)
        finally:
            await pairing.close()
        return
    credentials = request.pop("credentials", "")
    if not credentials:
        raise Failure("needs_pairing")
    config.set_credentials(Protocol.Companion, credentials)
    atv = None
    try:
        atv = await pyatv.connect(config, asyncio.get_running_loop())
        apps = await atv.apps.app_list()
        if not any(app.identifier == INFUSE for app in apps):
            raise Failure("infuse_missing")
        if action == "check":
            emit(state="checked")
            return
        uri = title_uri(request.get("media_type"), request.get("tmdb_id")) if action == "open" else None
        # Connecting may take seconds. Let Go reauthorize the current user,
        # title, session, TV grant and device revision before any TV action.
        emit(state="ready")
        if (await receive()).get("commit") is not True:
            return
        if action == "open":
            await atv.apps.launch_app(uri)
        else:
            await atv.remote_control.select()
        emit(state="sent")
    finally:
        if atv:
            await asyncio.gather(*atv.close(), return_exceptions=True)


async def main():
    try:
        await asyncio.wait_for(run(), 300)
    except Failure as error:
        emit(state="error", code=error.code)
    except AuthenticationError:
        emit(state="error", code="needs_pairing")
    except PairingError:
        emit(state="error", code="pairing_failed")
    except (TimeoutError, asyncio.TimeoutError):
        emit(state="error", code="timeout")
    except (ValueError, TypeError, KeyError):
        emit(state="error", code="invalid_request")
    except Exception:
        # pyatv and OS error strings can contain hosts, credentials or PINs.
        emit(state="error", code="unreachable")


if __name__ == "__main__":
    if sys.argv[1:] == ["--self-test"]:
        install_response_check()
        assert title_uri("tv", 1) == "infuse://series/1"
        assert title_uri("movie", 2) == "infuse://movie/2"
        assert validate_response({"_rT": 2}) == {"_rT": 2}
        emit(state="checked", pyatv=VERSION)
    elif sys.argv[1:]:
        sys.exit(2)
    else:
        asyncio.run(main())
