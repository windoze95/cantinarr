from __future__ import annotations

import sys
from pathlib import Path
import unittest

SCRIPTS_DIR = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SCRIPTS_DIR))

import testflight_distribute as td  # noqa: E402


GROUP_ID = "c0601f06-0000-0000-0000-000000000000"
BUILD_ID = "01ce6068-0000-0000-0000-000000000000"


def group_body(name="Public Beta", internal=False):
    return {
        "data": {"type": "betaGroups", "id": GROUP_ID, "attributes": {"name": name, "isInternalGroup": internal}},
        "included": [{"type": "apps", "id": "6744379934", "attributes": {"bundleId": "codes.julian.cantinarr"}}],
    }


class FakeClient:
    """Records every request so tests can assert what reached App Store Connect."""

    def __init__(self, *, group=None, builds=None, group_builds=None, beta_state="READY_FOR_BETA_SUBMISSION"):
        self.group = group if group is not None else group_body()
        self.builds = builds if builds is not None else [{"type": "builds", "id": BUILD_ID}]
        self.group_builds = group_builds or []
        self.beta_state = beta_state
        self.calls = []

    def request(self, method, path, body=None, allow_status=()):
        self.calls.append((method, path.split("?")[0], body))
        if method == "POST":
            return 201, {"data": {"attributes": {"betaReviewState": "APPROVED"}}}
        if path.startswith("/v1/betaGroups/") and "/builds" in path:
            return 200, {"data": self.group_builds, "links": {}}
        if path.startswith("/v1/betaGroups/"):
            return 200, self.group
        if path.startswith("/v1/builds?"):
            return 200, {"data": self.builds}
        if "/buildBetaDetail" in path:
            return 200, {"data": {"attributes": {"externalBuildState": self.beta_state}}}
        raise AssertionError(f"unexpected request {method} {path}")

    def get(self, path, allow_status=()):
        return self.request("GET", path, allow_status=allow_status)

    def get_all(self, path):
        return self.request("GET", path)[1]["data"]

    def mutations(self):
        return [(m, p, b) for (m, p, b) in self.calls if m != "GET"]


class GroupGuardTests(unittest.TestCase):
    def test_resolves_external_group_and_its_app(self) -> None:
        name, app_id, bundle_id = td.resolve_group(FakeClient(), GROUP_ID, "Public Beta")
        self.assertEqual((name, app_id, bundle_id), ("Public Beta", "6744379934", "codes.julian.cantinarr"))

    def test_refuses_when_the_group_name_no_longer_matches(self) -> None:
        # A renamed or recreated group must stop the run, not push a build at whoever is in the
        # group that now holds this id.
        client = FakeClient(group=group_body(name="Something Else"))
        with self.assertRaises(td.ApiError) as ctx:
            td.resolve_group(client, GROUP_ID, "Public Beta")
        self.assertIn("Refusing to distribute", str(ctx.exception))
        self.assertEqual(client.mutations(), [])

    def test_refuses_internal_groups(self) -> None:
        client = FakeClient(group=group_body(name="Cantinarr Testers", internal=True))
        with self.assertRaises(td.ApiError):
            td.resolve_group(client, GROUP_ID, "Cantinarr Testers")
        self.assertEqual(client.mutations(), [])


class ProcessingStateTests(unittest.TestCase):
    def test_states_that_block_distribution_fail_without_mutating(self) -> None:
        for state in ("PROCESSING_EXCEPTION", "MISSING_EXPORT_COMPLIANCE", "BETA_REJECTED", "EXPIRED"):
            with self.subTest(state=state):
                client = FakeClient(beta_state=state)
                with self.assertRaises(td.ApiError) as ctx:
                    td.wait_for_processing(client, BUILD_ID, deadline=0)
                self.assertIn(state, str(ctx.exception))
                self.assertEqual(client.mutations(), [])

    def test_ready_states_return_immediately(self) -> None:
        for state in sorted(td.STATES_READY):
            with self.subTest(state=state):
                client = FakeClient(beta_state=state)
                self.assertEqual(td.wait_for_processing(client, BUILD_ID, deadline=0), state)

    def test_still_processing_times_out_rather_than_distributing(self) -> None:
        client = FakeClient(beta_state="PROCESSING")
        with self.assertRaises(td.ApiError) as ctx:
            td.wait_for_processing(client, BUILD_ID, deadline=0)
        self.assertIn("Nothing was distributed", str(ctx.exception))
        self.assertEqual(client.mutations(), [])


class DistributionTests(unittest.TestCase):
    def test_already_distributed_build_is_a_no_op(self) -> None:
        client = FakeClient(group_builds=[{"type": "builds", "id": BUILD_ID}])
        self.assertTrue(td.already_in_group(client, GROUP_ID, BUILD_ID))
        self.assertEqual(client.mutations(), [])

    def test_beta_review_submission_then_group_add(self) -> None:
        # Order mirrors fastlane's pilot: a build still at READY_FOR_BETA_SUBMISSION is submitted
        # for beta review before it is added to the group.
        client = FakeClient()
        td.submit_for_beta_review(client, BUILD_ID)
        td.add_to_group(client, GROUP_ID, BUILD_ID)
        self.assertEqual(
            client.mutations(),
            [
                (
                    "POST",
                    "/v1/betaAppReviewSubmissions",
                    {
                        "data": {
                            "type": "betaAppReviewSubmissions",
                            "relationships": {"build": {"data": {"type": "builds", "id": BUILD_ID}}},
                        }
                    },
                ),
                (
                    "POST",
                    f"/v1/betaGroups/{GROUP_ID}/relationships/builds",
                    {"data": [{"type": "builds", "id": BUILD_ID}]},
                ),
            ],
        )

    def test_ambiguous_build_match_is_an_error(self) -> None:
        client = FakeClient(builds=[{"id": "a"}, {"id": "b"}])
        with self.assertRaises(td.ApiError):
            td.find_build(client, "6744379934", "0.1.0", "337")

    def test_missing_build_returns_none(self) -> None:
        self.assertIsNone(td.find_build(FakeClient(builds=[]), "6744379934", "0.1.0", "337"))


if __name__ == "__main__":
    unittest.main()
