import io
from contextlib import redirect_stdout
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch
import zipfile

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import release_control as rc

COMMIT = "a" * 40
OTHER = "b" * 40
DIGEST = "sha256:" + "c" * 64


def run_and_record(platform="ios"):
    run = {"id": 123, "run_attempt": 1, "conclusion": "success", "event": "push",
           "path": f".github/workflows/{rc.WORKFLOWS[platform]}",
           "head_sha": COMMIT, "head_branch": "release/1.0.0"}
    record = {"schema": 1, "platform": platform, "run_id": 123, "run_attempt": 1,
              "sha": COMMIT, "ref": "refs/heads/release/1.0.0", "channel": "candidate",
              "version": "1.0.0", "build_number": "42", "digest": DIGEST, "repository": "owner/repo"}
    return run, record


class RoutingTests(unittest.TestCase):
    def test_public_beta_has_exactly_one_owner_and_resumes_after_release(self):
        branches = {"main": COMMIT}
        self.assertEqual(rc.channel_for("refs/heads/main", COMMIT, branches), "beta")
        branches["release/1.0.0"] = OTHER
        self.assertEqual(rc.channel_for("refs/heads/main", COMMIT, branches), "paused")
        self.assertEqual(rc.channel_for("refs/heads/release/1.0.0", OTHER, branches), "candidate")
        del branches["release/1.0.0"]
        self.assertEqual(rc.channel_for("refs/heads/main", COMMIT, branches), "beta")

    def test_older_queued_runs_cannot_publish(self):
        self.assertEqual(rc.channel_for("refs/heads/main", COMMIT, {"main": OTHER}), "superseded")

    def test_unrecognized_refs_and_ambiguous_releases_fail_closed(self):
        for ref, branches in [
            ("refs/heads/feat/test", {"feat/test": COMMIT}),
            ("refs/tags/v1.0.0", {"main": COMMIT}),
            ("refs/heads/main", {"main": COMMIT, "release/wip": COMMIT}),
            ("refs/heads/main", {"main": COMMIT, "release/1.0.0": COMMIT, "release/1.1.0": OTHER}),
        ]:
            with self.subTest(ref=ref, branches=branches), self.assertRaises(rc.ReleaseError):
                rc.channel_for(ref, COMMIT, branches)

    def test_freeze_stops_an_already_built_public_app_but_allows_edge_server(self):
        source = {"sha": COMMIT, "ref": "refs/heads/main", "channel": "beta"}
        with patch.object(rc, "branch_state", return_value={"main": COMMIT, "release/1.0.0": OTHER}):
            with self.assertRaises(rc.ReleaseError):
                rc.current(source)
            rc.current(source, server=True)

    def test_unavailable_github_is_not_treated_as_no_release(self):
        with patch.dict(os.environ, {"GITHUB_REPOSITORY": "owner/repo"}), \
                patch.object(rc, "pages", side_effect=rc.ReleaseError("GitHub unavailable")):
            with self.assertRaises(rc.ReleaseError):
                rc.branch_state()


class PreviewTests(unittest.TestCase):
    def setUp(self):
        self.pr = {"number": 7, "state": "open", "draft": False, "head": {"sha": COMMIT},
                   "merge_commit_sha": OTHER, "base": {"ref": "main"}}

    def test_builds_tested_merge_and_retains_reviewed_head(self):
        source = rc.preview_source(self.pr, COMMIT)
        self.assertEqual(source["sha"], OTHER)
        self.assertEqual(source["ci_head"], COMMIT)
        self.assertEqual(source["channel"], "preview")

    def test_head_change_invalidates_approval(self):
        with self.assertRaises(rc.ReleaseError):
            rc.preview_source(self.pr, OTHER)

    def test_closed_draft_or_conflicted_pr_cannot_be_uploaded(self):
        for changed in ({"state": "closed"}, {"draft": True}, {"merge_commit_sha": None}):
            with self.subTest(changed=changed), self.assertRaises(rc.ReleaseError):
                rc.preview_source({**self.pr, **changed}, COMMIT)

    def test_base_update_invalidates_old_merge_checkout(self):
        source = rc.preview_source(self.pr, COMMIT)
        with patch.dict(os.environ, {"GITHUB_REPOSITORY": "owner/repo"}), patch.object(
                rc, "api", return_value={**self.pr, "merge_commit_sha": "d" * 40}):
            with self.assertRaises(rc.ReleaseError):
                rc.current(source)


class PublishingStatusTests(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.output = Path(directory.name) / "output"
        self.summary = Path(directory.name) / "summary"
        self.source = {"sha": COMMIT, "ref": "refs/heads/main", "channel": "beta"}
        environment = patch.dict(os.environ, {
            "SOURCE_JSON": json.dumps(self.source),
            "GITHUB_OUTPUT": str(self.output),
            "GITHUB_STEP_SUMMARY": str(self.summary),
        })
        environment.start()
        self.addCleanup(environment.stop)

    def run_current(self, branches, *flags):
        output = io.StringIO()
        with patch.object(sys, "argv", ["release_control.py", "current", *flags]), \
                patch.object(rc, "branch_state", return_value=branches), redirect_stdout(output):
            rc.main()
        return output.getvalue()

    def test_late_freeze_emits_disabled_and_reports_a_successful_pause(self):
        log = self.run_current({"main": COMMIT, "release/1.0.0": OTHER}, "--skip-paused")
        self.assertEqual(self.output.read_text(), "enabled=false\n")
        self.assertIn("::notice::", log)
        self.assertIn("skipped further publishing", self.summary.read_text())

    def test_current_main_and_edge_server_remain_publishable(self):
        self.run_current({"main": COMMIT}, "--skip-paused")
        self.assertEqual(self.output.read_text(), "enabled=true\n")
        self.assertFalse(self.summary.exists())
        self.output.unlink()
        self.run_current({"main": COMMIT, "release/1.0.0": OTHER}, "--platform", "server")
        self.assertEqual(self.output.read_text(), "enabled=true\n")

    def test_strict_callers_still_fail_during_freeze(self):
        with self.assertRaises(SystemExit):
            self.run_current({"main": COMMIT, "release/1.0.0": OTHER})
        self.assertFalse(self.output.exists())

    def test_skip_paused_does_not_hide_changed_or_ambiguous_sources(self):
        for branches in (
            {"main": OTHER},
            {"main": OTHER, "release/1.0.0": OTHER},
            {"main": COMMIT, "release/wip": OTHER},
            {"main": COMMIT, "release/1.0.0": COMMIT, "release/1.1.0": OTHER},
        ):
            with self.subTest(branches=branches), self.assertRaises(SystemExit):
                self.run_current(branches, "--skip-paused")
            self.assertFalse(self.output.exists())

    def test_skip_paused_does_not_hide_unreadable_ownership(self):
        with patch.object(sys, "argv", ["release_control.py", "current", "--skip-paused"]), \
                patch.object(rc, "branch_state", side_effect=rc.ReleaseError("GitHub unavailable")), \
                self.assertRaises(SystemExit):
            rc.main()
        self.assertFalse(self.output.exists())


class ReceiptTests(unittest.TestCase):
    def setUp(self):
        env = patch.dict(os.environ, {"GITHUB_REPOSITORY": "owner/repo"})
        env.start()
        self.addCleanup(env.stop)

    def test_candidate_manifest_rejects_mixed_commits(self):
        _, ios = run_and_record("ios")
        _, android = run_and_record("android")
        result = rc.combine_records({"ios": ios, "android": android}, "Device upgrade verified")
        self.assertEqual(result["sha"], COMMIT)
        with self.assertRaises(rc.ReleaseError):
            rc.combine_records({"ios": ios, "android": {**android, "sha": OTHER}}, "Tested")
        with self.assertRaises(rc.ReleaseError):
            rc.combine_records({"ios": ios}, "")

    def test_exact_successful_candidates_are_accepted(self):
        for platform in rc.WORKFLOWS:
            run, record = run_and_record(platform)
            self.assertEqual(rc.validate_record(record, run, platform), record)

    def test_wrong_workflow_failed_run_and_previews_cannot_reach_production(self):
        for changed in ({"conclusion": "failure"}, {"event": "pull_request"},
                        {"path": ".github/workflows/mobile-preview.yml"}, {"head_sha": OTHER},
                        {"head_branch": "main"}):
            run, record = run_and_record()
            with self.subTest(changed=changed), self.assertRaises(rc.ReleaseError):
                rc.validate_record(record, {**run, **changed}, "ios")

    def test_corrupt_or_substituted_build_records_are_rejected(self):
        for changed in ({"channel": "preview"}, {"build_number": ""}, {"build_number": "0"},
                        {"sha": OTHER}, {"run_id": 999}, {"platform": "android"},
                        {"ref": "refs/heads/main"}, {"version": "1.0.0\nattack"},
                        {"repository": "other/repository"}):
            run, record = run_and_record()
            with self.subTest(changed=changed), self.assertRaises(rc.ReleaseError):
                rc.validate_record({**record, **changed}, run, "ios")

    def test_server_record_requires_matching_version_and_real_digest(self):
        run, record = run_and_record("server")
        for changed in ({"version": "1.1.0"}, {"digest": "latest"}):
            with self.assertRaises(rc.ReleaseError):
                rc.validate_record({**record, **changed}, run, "server")

    def test_retrying_failed_distribution_keeps_original_uploaded_build(self):
        run, record = run_and_record()
        run["run_attempt"] = 2
        self.assertEqual(rc.validate_record(record, run, "ios")["build_number"], "42")
        with self.assertRaises(rc.ReleaseError):
            rc.validate_record({**record, "run_attempt": 3}, run, "ios")

    def test_artifact_selection_uses_latest_actual_attempt_without_extracting_files(self):
        run, record = run_and_record()
        run["run_attempt"] = 3
        buffer = io.BytesIO()
        with zipfile.ZipFile(buffer, "w") as archive:
            archive.writestr("release.json", json.dumps(record))
            archive.writestr("../never-extract", "untrusted")
        artifacts = [{"id": 8, "name": "ios-release-record-1", "expired": False},
                     {"id": 9, "name": "ios-release-record-2", "expired": False}]
        with patch.dict(os.environ, {"GITHUB_REPOSITORY": "owner/repo"}), \
                patch.object(rc, "pages", return_value=[{"artifacts": artifacts}]), \
                patch.object(rc, "command", return_value=buffer.getvalue()) as command:
            self.assertEqual(rc.artifact_json(run, "ios-release-record-3", "release.json"), record)
            self.assertIn("artifacts/9/zip", command.call_args.args[-1])

    def test_green_pr_head_is_insufficient_when_ci_checked_a_different_tree(self):
        run, _ = run_and_record()
        run["html_url"] = "https://example.invalid/run"
        with patch.dict(os.environ, {"GITHUB_REPOSITORY": "owner/repo"}), \
                patch.object(rc, "api", return_value={"workflow_runs": [run]}), \
                patch.object(rc, "artifact_json", return_value={"sha": OTHER}):
            with self.assertRaises(rc.ReleaseError):
                rc.wait_ci(COMMIT, COMMIT, 0)


class PromotionTests(unittest.TestCase):
    def setUp(self):
        env = patch.dict(os.environ, {"GITHUB_REPOSITORY": "owner/repo"})
        env.start()
        self.addCleanup(env.stop)
        self.manifest = {"manifests": [{"platform": {"architecture": arch}} for arch in ["amd64", "arm64"]]}

    def test_server_uses_recorded_run_even_if_the_same_sha_was_rebuilt_later(self):
        _, record = run_and_record("server")
        selected = rc.combine_records({"server": record}, "Candidate digest installed and tested")
        run = {"id": 456, "run_attempt": 1, "conclusion": "success", "head_branch": "main",
               "event": "workflow_dispatch", "path": ".github/workflows/release-candidate.yml"}
        with patch.object(rc, "pages", return_value=[{"workflow_runs": [run]}]), \
                patch.object(rc, "artifact_json", return_value=selected), \
                patch.object(rc, "select_record", return_value=record) as select:
            self.assertEqual(rc.find_server(COMMIT, "1.0.0"), selected)
            select.assert_called_once_with("server", 123)

    def test_server_requires_a_recorded_selection_before_tagging(self):
        with patch.object(rc, "pages", return_value=[{"workflow_runs": []}]):
            with self.assertRaises(rc.ReleaseError):
                rc.find_server(COMMIT, "1.0.0")

    def test_newer_stable_release_blocks_an_old_retry(self):
        _, record = run_and_record("server")
        manifest = {"manifests": [{"platform": {"architecture": arch}} for arch in ["amd64", "arm64"]]}
        releases = [{"draft": False, "prerelease": False, "tag_name": "v1.1.0"}]
        with patch.dict(os.environ, {"GITHUB_REPOSITORY": "owner/repo"}), \
                patch.object(rc, "command", return_value=json.dumps(manifest).encode()) as command, \
                patch.object(rc, "pages", return_value=[releases]):
            with self.assertRaises(rc.ReleaseError):
                rc.promote_server(record)
            self.assertEqual(command.call_count, 1)

    def test_partial_newer_publish_blocks_old_retry_without_a_github_release(self):
        _, record = run_and_record("server")
        configs = {"linux/amd64": {"config": {"Labels": {"org.opencontainers.image.version": "1.1.0-rc.99"}}}}
        with patch.object(rc, "command", return_value=json.dumps(self.manifest).encode()) as command, \
                patch.object(rc, "pages", return_value=[[]]), \
                patch.object(rc, "inspect_image", return_value=configs):
            with self.assertRaises(rc.ReleaseError):
                rc.promote_server(record)
            self.assertEqual(command.call_count, 1)

    def test_retry_promotes_same_digest_and_verifies_every_tag(self):
        _, record = run_and_record("server")
        with patch.object(rc, "command", return_value=json.dumps(self.manifest).encode()) as command, \
                patch.object(rc, "pages", return_value=[[]]), \
                patch.object(rc, "inspect_image", side_effect=[None, DIGEST, DIGEST, DIGEST, DIGEST]) as inspect:
            rc.promote_server(record)
            self.assertEqual(command.call_count, 4)
            self.assertTrue(all(call.args[-1].endswith('@' + DIGEST) for call in command.call_args_list[1:]))
            self.assertEqual(inspect.call_count, 5)

    def test_numbered_image_cannot_be_replaced(self):
        _, record = run_and_record("server")
        with patch.object(rc, "command", return_value=json.dumps(self.manifest).encode()) as command, \
                patch.object(rc, "pages", return_value=[[]]), \
                patch.object(rc, "inspect_image", side_effect=[None, "sha256:" + "d" * 64]):
            with self.assertRaises(rc.ReleaseError):
                rc.promote_server(record)
            self.assertEqual(command.call_count, 1)


if __name__ == "__main__":
    unittest.main()
