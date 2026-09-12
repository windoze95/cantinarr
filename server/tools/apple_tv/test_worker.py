import asyncio
import io
import json
from types import SimpleNamespace
import unittest
from unittest.mock import AsyncMock, Mock, patch

import cantinarr_appletv as worker


def config(identifier="stable", operating_system=worker.OperatingSystem.TvOS):
    service = SimpleNamespace(enabled=True, credentials="private-test-credential")
    return SimpleNamespace(identifier=identifier, all_identifiers=[identifier],
        name="Living room", address="192.0.2.10", get_service=lambda _: service,
        set_credentials=Mock(), device_info=SimpleNamespace(operating_system=operating_system))


class ValidationTests(unittest.TestCase):
    def test_only_title_pages_never_autoplay_or_caller_urls(self):
        self.assertEqual(worker.title_uri("tv", 12), "infuse://series/12")
        self.assertEqual(worker.title_uri("movie", 34), "infuse://movie/34")
        for media, identifier in [("episode", 1), ("movie", True), ("tv", 0),
                                  ("tv", -1), ("tv", "1?play"), ("infuse://", 1)]:
            with self.assertRaises(worker.Failure):
                worker.title_uri(media, identifier)

    def test_companion_rejection_is_not_reported_as_sent(self):
        for response in [{"_ec": 1}, {"_eu": {"_ec": -1}}]:
            with self.assertRaises(worker.Failure) as caught:
                worker.validate_response(response)
            self.assertEqual(caught.exception.code, "launch_failed")
        for response in [{"_rT": 2}, {"_ec": 0}, {"_eu": {"_ec": 0}}, {}]:
            self.assertEqual(worker.validate_response(response), response)

    def test_host_input_has_no_url_port_or_shell_surface(self):
        for address in ["192.0.2.10", "Living-Room.local."]:
            self.assertTrue(worker.valid_host(address))
        for address in ["", "https://tv", "tv:1234", "::1", "tv/path", "tv\n", "$(whoami)"]:
            self.assertFalse(worker.valid_host(address))


class WorkerTests(unittest.IsolatedAsyncioTestCase):
    async def test_discovery_excludes_mac_and_unknown_devices(self):
        tv = config()
        with patch.object(worker.pyatv, "scan", AsyncMock(return_value=[tv,
                config("mac", worker.OperatingSystem.MacOS),
                config("unknown", worker.OperatingSystem.Unknown)])):
            self.assertEqual(await worker.discover(), [tv])

    async def test_an_old_address_never_receives_credentials_for_another_tv(self):
        with patch.object(worker, "discover", AsyncMock(return_value=[config("new")])):
            with self.assertRaises(worker.Failure) as caught:
                await worker.resolve({"address": "192.0.2.10", "identifier": "old"})
            self.assertEqual(caught.exception.code, "identity_changed")

    async def exercise(self, action="open", commit=True, infuse=True):
        atv = SimpleNamespace(apps=SimpleNamespace(
            app_list=AsyncMock(return_value=[SimpleNamespace(identifier=worker.INFUSE)] if infuse else []),
            launch_app=AsyncMock()), remote_control=SimpleNamespace(select=AsyncMock()), close=Mock(return_value=set()))
        replies = []
        requests = [{"action": action, "credentials": "private-test-credential", "media_type": "tv", "tmdb_id": 12},
                    {"commit": commit}]

        async def receive():
            if len(requests) == 1:
                self.assertEqual(replies, [{"state": "ready"}])
                atv.apps.launch_app.assert_not_awaited()
                atv.remote_control.select.assert_not_awaited()
            return requests.pop(0)

        with patch.object(worker, "install_response_check"), patch.object(worker, "receive", receive), \
                patch.object(worker, "resolve", AsyncMock(return_value=config())), \
                patch.object(worker.pyatv, "connect", AsyncMock(return_value=atv)), \
                patch.object(worker, "emit", lambda **values: replies.append(values)):
            await worker.main()
        atv.close.assert_called_once()
        self.assertNotIn("private-test-credential", json.dumps(replies))
        return atv, replies

    async def test_launch_waits_for_commit_and_does_not_select(self):
        atv, replies = await self.exercise()
        atv.apps.launch_app.assert_awaited_once_with("infuse://series/12")
        atv.remote_control.select.assert_not_awaited()
        self.assertEqual(replies[-1], {"state": "sent"})

    async def test_no_commit_means_no_tv_action(self):
        atv, replies = await self.exercise(commit=False)
        atv.apps.launch_app.assert_not_awaited()
        atv.remote_control.select.assert_not_awaited()
        self.assertEqual(replies, [{"state": "ready"}])

    async def test_confirm_is_one_select_without_a_second_launch(self):
        atv, _ = await self.exercise(action="select")
        atv.remote_control.select.assert_awaited_once()
        atv.apps.launch_app.assert_not_awaited()

    async def test_missing_infuse_never_sends(self):
        atv, replies = await self.exercise(infuse=False)
        atv.apps.launch_app.assert_not_awaited()
        self.assertEqual(replies, [{"state": "error", "code": "infuse_missing"}])

    async def test_pairing_closes_when_cancelled_before_pin(self):
        pairing = SimpleNamespace(begin=AsyncMock(), close=AsyncMock(), pin=Mock(), finish=AsyncMock())
        with patch.object(worker, "install_response_check"), \
                patch.object(worker, "receive", AsyncMock(side_effect=[{"action": "pair"}, asyncio.CancelledError()])), \
                patch.object(worker, "resolve", AsyncMock(return_value=config())), \
                patch.object(worker.pyatv, "pair", AsyncMock(return_value=pairing)), patch.object(worker, "emit"):
            with self.assertRaises(asyncio.CancelledError):
                await worker.run()
        pairing.close.assert_awaited_once()
        pairing.finish.assert_not_awaited()

    async def test_exception_text_never_crosses_the_private_protocol(self):
        with patch.object(worker, "run", AsyncMock(side_effect=RuntimeError("private-host private-credential private-pin"))), \
                patch("sys.stdout", new_callable=io.StringIO) as output:
            await worker.main()
            self.assertEqual(json.loads(output.getvalue()), {"state": "error", "code": "unreachable"})

    async def test_rejection_guard_wraps_the_real_exchange_boundary(self):
        with patch.object(worker.CompanionProtocol, "_exchange_generic_opack", AsyncMock(return_value={"_ec": -1})), \
                patch.object(worker.importlib.metadata, "version", return_value=worker.VERSION):
            worker.install_response_check()
            with self.assertRaises(worker.Failure):
                await worker.CompanionProtocol._exchange_generic_opack(None)


if __name__ == "__main__":
    unittest.main()
