#!/usr/bin/env python3
"""Distribute an uploaded TestFlight build to an external tester group.

`upload_to_testflight` hands the IPA to Apple and stops. Internal groups created with
"Enable automatic distribution" pick the build up on their own; external groups never do —
somebody has to add the build to the group, in the console or through the API. This script is
that step, so a merge to `main` reaches external testers without a human opening App Store
Connect.

It runs after the upload, off the macOS runner: fastlane can do this inline, but only with
`skip_waiting_for_build_processing: false`, which parks a 10x-billed macOS runner for the whole
of Apple's processing (and Apple's TestFlight ingestion has stalled for hours before).

Order of operations mirrors fastlane's pilot: submit for beta review first if the build still
needs it, then add it to the group.

Usage:
    testflight_distribute.py --group-id UUID --group-name NAME \
        --app-version 0.1.0 --build-number 337 [--timeout-minutes 45]

Reads APP_STORE_CONNECT_KEY_ID, APP_STORE_CONNECT_ISSUER_ID, APP_STORE_CONNECT_API_KEY_B64.
"""

import argparse
import base64
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

import jwt

API = "https://api.appstoreconnect.apple.com"

# externalBuildState values, grouped by what they mean for us. Apple can add new ones, so
# anything unrecognized is treated as "keep waiting" and reported if we time out.
STATES_WAIT = {"PROCESSING", "IN_EXPORT_COMPLIANCE_REVIEW"}
STATES_READY = {
    "READY_FOR_BETA_SUBMISSION",  # needs a beta review submission before it can be distributed
    "WAITING_FOR_BETA_REVIEW",
    "IN_BETA_REVIEW",
    "BETA_APPROVED",
    "READY_FOR_BETA_TESTING",
    "IN_BETA_TESTING",
}
STATES_FAIL = {
    "PROCESSING_EXCEPTION",
    "MISSING_EXPORT_COMPLIANCE",
    "BETA_REJECTED",
    "EXPIRED",
}

POLL_SECONDS = 30


class ApiError(Exception):
    pass


def log(msg):
    print(msg, flush=True)


class Client:
    def __init__(self, key_id, issuer_id, private_key):
        self._key_id = key_id
        self._issuer_id = issuer_id
        self._private_key = private_key
        self._token = None
        self._token_expiry = 0.0

    def _bearer(self):
        # ASC caps token lifetime at 20 minutes; mint a fresh one well before expiry so a long
        # poll doesn't start 401ing halfway through.
        now = time.time()
        if self._token is None or now > self._token_expiry - 60:
            exp = int(now) + 15 * 60
            self._token = jwt.encode(
                {"iss": self._issuer_id, "iat": int(now), "exp": exp, "aud": "appstoreconnect-v1"},
                self._private_key,
                algorithm="ES256",
                headers={"kid": self._key_id, "typ": "JWT"},
            )
            self._token_expiry = exp
        return self._token

    def request(self, method, path, body=None, allow_status=()):
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(API + path, data=data, method=method)
        req.add_header("Authorization", "Bearer " + self._bearer())
        if data is not None:
            req.add_header("Content-Type", "application/json")
        try:
            with urllib.request.urlopen(req) as resp:
                raw = resp.read()
                return resp.status, (json.loads(raw) if raw else None)
        except urllib.error.HTTPError as e:
            raw = e.read().decode(errors="replace")
            if e.code in allow_status:
                return e.code, _try_json(raw)
            raise ApiError(f"{method} {path} -> HTTP {e.code}: {raw[:1000]}") from None

    def get(self, path, allow_status=()):
        return self.request("GET", path, allow_status=allow_status)

    def get_all(self, path):
        """Follow ASC pagination. Never pass `sort` to /betaGroups/{id}/builds — it silently
        returns an empty page."""
        out = []
        url = API + path
        while url:
            status, body = self.request("GET", url[len(API):])
            out.extend(body.get("data", []))
            url = (body.get("links") or {}).get("next")
        return out


def _try_json(raw):
    try:
        return json.loads(raw)
    except ValueError:
        return {"raw": raw}


def resolve_group(client, group_id, expected_name):
    """Look the group up by id and assert its name and externality.

    The id is the address and the name is the assertion: a rename or a recreated group must fail
    the run loudly rather than push a build at an audience nobody meant to reach.
    """
    _, body = client.get(f"/v1/betaGroups/{urllib.parse.quote(group_id)}?include=app")
    attrs = body["data"]["attributes"]
    name = attrs.get("name")
    if name != expected_name:
        raise ApiError(
            f"beta group {group_id} is named {name!r}, expected {expected_name!r}. "
            "Refusing to distribute — reconcile the workflow with App Store Connect first."
        )
    if attrs.get("isInternalGroup"):
        raise ApiError(
            f"beta group {name!r} is internal. Internal groups reject build adds by design "
            "(422 'Cannot add internal group to a build') and already receive every build when "
            "created with automatic distribution."
        )
    apps = [i for i in body.get("included", []) if i["type"] == "apps"]
    if not apps:
        raise ApiError(f"beta group {name!r} has no app relationship")
    return name, apps[0]["id"], apps[0]["attributes"].get("bundleId")


def find_build(client, app_id, app_version, build_number):
    query = urllib.parse.urlencode(
        {
            "filter[app]": app_id,
            "filter[preReleaseVersion.version]": app_version,
            "filter[version]": build_number,
            "limit": 2,
        }
    )
    _, body = client.get(f"/v1/builds?{query}")
    data = body.get("data", [])
    if len(data) > 1:
        raise ApiError(f"{len(data)} builds match {app_version} ({build_number}); expected one")
    return data[0]["id"] if data else None


def wait_for_build(client, app_id, app_version, build_number, deadline):
    """Wait for the upload to surface as a build record."""
    while True:
        build_id = find_build(client, app_id, app_version, build_number)
        if build_id:
            log(f"build {app_version} ({build_number}) is {build_id}")
            return build_id
        if time.time() > deadline:
            raise ApiError(
                f"build {app_version} ({build_number}) never appeared in App Store Connect. "
                "The upload succeeded, so this is Apple-side ingestion, not a signing or "
                "export failure — check the build list in App Store Connect."
            )
        log(f"build {app_version} ({build_number}) not listed yet; waiting {POLL_SECONDS}s")
        time.sleep(POLL_SECONDS)


def wait_for_processing(client, build_id, deadline):
    """Wait until the build leaves processing, and return its externalBuildState.

    A 404 on buildBetaDetail is the honest signal that Apple's TestFlight handoff has not
    materialized the build yet: the upload is `Complete` and the build can still be distributed
    by nobody, automatically or manually.
    """
    last = None
    while True:
        status, body = client.get(f"/v1/builds/{build_id}/buildBetaDetail", allow_status=(404,))
        if status == 404:
            state = None
            last = "no buildBetaDetail (Apple's TestFlight handoff has not materialized)"
        else:
            state = (body.get("data") or {}).get("attributes", {}).get("externalBuildState")
            last = f"externalBuildState={state}"
            if state in STATES_FAIL:
                raise ApiError(
                    f"build {build_id} is {state}; it cannot be distributed to external testers. "
                    "Resolve it in App Store Connect (export compliance, beta review, or a fresh "
                    "upload) — this run made no changes."
                )
            if state in STATES_READY:
                log(f"build is ready to distribute ({state})")
                return state
            if state not in STATES_WAIT:
                log(f"unrecognized externalBuildState {state!r}; treating as still processing")
        if time.time() > deadline:
            raise ApiError(
                f"timed out waiting for build {build_id} to finish processing ({last}). "
                "Nothing was distributed; re-run this workflow once App Store Connect shows the "
                "build as ready, or add it to the group by hand."
            )
        log(f"{last}; waiting {POLL_SECONDS}s")
        time.sleep(POLL_SECONDS)


def submit_for_beta_review(client, build_id):
    body = {
        "data": {
            "type": "betaAppReviewSubmissions",
            "relationships": {"build": {"data": {"type": "builds", "id": build_id}}},
        }
    }
    status, resp = client.request(
        "POST", "/v1/betaAppReviewSubmissions", body=body, allow_status=(409,)
    )
    if status == 409:
        log("build was already submitted for beta review")
        return
    state = ((resp or {}).get("data") or {}).get("attributes", {}).get("betaReviewState")
    log(f"submitted for beta review (betaReviewState={state})")


def already_in_group(client, group_id, build_id):
    builds = client.get_all(f"/v1/betaGroups/{urllib.parse.quote(group_id)}/builds?limit=200")
    return any(b["id"] == build_id for b in builds)


def add_to_group(client, group_id, build_id):
    client.request(
        "POST",
        f"/v1/betaGroups/{urllib.parse.quote(group_id)}/relationships/builds",
        body={"data": [{"type": "builds", "id": build_id}]},
    )


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--group-id", required=True, help="App Store Connect beta group id")
    parser.add_argument("--group-name", required=True, help="expected name of that group")
    parser.add_argument("--app-version", required=True, help="marketing version, e.g. 0.1.0")
    parser.add_argument("--build-number", required=True, help="build number, e.g. 337")
    parser.add_argument("--timeout-minutes", type=int, default=45)
    args = parser.parse_args()

    # An empty build number would otherwise spend the whole timeout looking for a build that
    # cannot exist, and report it as an Apple-side stall.
    if not args.app_version.strip():
        sys.exit("error: --app-version is empty (did the upload job's outputs get through?)")
    if not args.build_number.strip().isdigit():
        sys.exit(f"error: --build-number must be a number, got {args.build_number!r}")

    try:
        key_id = os.environ["APP_STORE_CONNECT_KEY_ID"]
        issuer_id = os.environ["APP_STORE_CONNECT_ISSUER_ID"]
        private_key = base64.b64decode(os.environ["APP_STORE_CONNECT_API_KEY_B64"]).decode()
    except KeyError as e:
        sys.exit(f"missing required secret: {e.args[0]}")

    client = Client(key_id, issuer_id, private_key)
    deadline = time.time() + args.timeout_minutes * 60

    try:
        name, app_id, bundle_id = resolve_group(client, args.group_id, args.group_name)
        log(f"distributing to external group {name!r} ({bundle_id}, app {app_id})")

        build_id = wait_for_build(client, app_id, args.app_version, args.build_number, deadline)

        if already_in_group(client, args.group_id, build_id):
            log(f"build {args.build_number} is already in {name!r}; nothing to do")
            return

        state = wait_for_processing(client, build_id, deadline)
        if state == "READY_FOR_BETA_SUBMISSION":
            submit_for_beta_review(client, build_id)

        add_to_group(client, args.group_id, build_id)
    except ApiError as e:
        sys.exit(f"error: {e}")

    log(f"added build {args.app_version} ({args.build_number}) to {args.group_name!r}")


if __name__ == "__main__":
    main()
