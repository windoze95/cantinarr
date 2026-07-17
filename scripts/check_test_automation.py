#!/usr/bin/env python3
"""Validate the master catalog and its machine-readable automation links."""

from __future__ import annotations

import ast
from collections import Counter
import json
from pathlib import Path
import re
import sys

from maestro_safety import SafetyError, validate_maestro_config


ROOT = Path(__file__).resolve().parents[1]
CATALOG_DIR = ROOT / "docs" / "testing" / "catalog"
README = ROOT / "docs" / "testing" / "README.md"
MANIFEST = ROOT / "docs" / "testing" / "automation.json"
COVERAGE_PLAN = ROOT / "docs" / "testing" / "coverage-plan.json"
SUITES = ROOT / "e2e" / "maestro" / "suites.json"
FLOWS_DIR = ROOT / "e2e" / "maestro" / "flows"
HELPERS_DIR = ROOT / "e2e" / "maestro" / "helpers"
MAESTRO_CONFIG = ROOT / "e2e" / "maestro" / "config.yaml"

CASE_RE = re.compile(
    r"^- \[ \] `(?P<id>[A-Z]+-[0-9]{3})` · "
    r"(?P<priority>P[0-2]) · (?P<tags>[A-Z]+(?:/[A-Z]+)*) — .+"
)
TABLE_RE = re.compile(
    r"^\| \[[^]]+\]\(catalog/(?P<file>[^)]+)\) \| [^|]+ \| "
    r"(?P<count>[0-9]+) \|$"
)
ALLOWED_STATUS = {"automated", "partial"}
ALLOWED_EVIDENCE_KINDS = {
    "flutter-test",
    "go-test",
    "maestro-flow",
    "patrol-test",
    "script-check",
    "workflow-step",
}
EVIDENCE_LAYERS = {
    "flutter-test": "flutter-widget",
    "go-test": "go-api",
    "maestro-flow": "maestro-web",
    "patrol-test": "patrol-native",
    "script-check": "repository-ci",
    "workflow-step": "repository-ci",
}
ALLOWED_DOMINANT_LAYERS = {
    "flutter-widget",
    "go-api",
    "maestro-web",
    "manual-external",
    "patrol-native",
}
ALLOWED_RECOMMENDED_LAYERS = ALLOWED_DOMINANT_LAYERS | {"repository-ci"}
ALLOWED_DISPOSITIONS = {"automatable", "blocked", "hybrid", "manual"}
ALLOWED_USERS = {
    "lab-admin",
    "lab-admin-b",
    "lab-requester",
    "lab-restricted",
    "lab-no-grants",
}
FORBIDDEN_FLOW_COMMANDS = {
    "addMedia",
    "assertNoDefectsWithAI",
    "assertWithAI",
    "copyText",
    "copyTextFrom",
    "evalScript",
    "extractTextWithAI",
    "openLink",
    "runScript",
}
ALLOWED_FLOW_COMMANDS = {
    "assertNotVisible",
    "assertVisible",
    "extendedWaitUntil",
    "inputText",
    "launchApp",
    "pressKey",
    "repeat",
    "runFlow",
    "scrollUntilVisible",
    "swipe",
    "tapOn",
}
FLOW_COMMAND_RE = re.compile(
    r"^\s*-\s*(?P<command>[A-Za-z][A-Za-z0-9]*)(?P<suffix>\s*:.*)?$"
)
RUN_FLOW_FILE_RE = re.compile(r"^\s*[\"']?file[\"']?\s*:\s*(?P<path>.+?)\s*$")
INTERPOLATION_RE = re.compile(r"\$\{(?P<expression>[^}\n]*)}")
SIMPLE_VARIABLE_RE = re.compile(r"[A-Z][A-Z0-9_]*")
ALLOWED_BODY_VARIABLES = {
    "MAESTRO_PASSWORD",
    "MAESTRO_USERNAME",
    "TARGET_ID",
    "TARGET_TEXT",
}
FLOW_URL_HEADER = "url: ${MAESTRO_SERVER_URL}"
FLOW_TAG_RE = re.compile(r"  - [a-z0-9-]+")
GO_TEST_SELECTOR_RE = re.compile(r"Test[A-Za-z0-9_]+")
CASE_ID_RE = re.compile(r"[A-Z]+-[0-9]{3}")


class ValidationError(Exception):
    pass


def load_json(path: Path) -> object:
    try:
        return json.loads(path.read_text())
    except (OSError, json.JSONDecodeError) as exc:
        raise ValidationError(f"cannot read valid JSON from {path.relative_to(ROOT)}: {exc}") from exc


def parse_catalog() -> tuple[dict[str, dict[str, object]], Counter[str]]:
    cases: dict[str, dict[str, object]] = {}
    per_file: Counter[str] = Counter()
    for path in sorted(CATALOG_DIR.glob("*.md")):
        for line_number, line in enumerate(path.read_text().splitlines(), 1):
            match = CASE_RE.match(line)
            if not match:
                continue
            case_id = match.group("id")
            if case_id in cases:
                raise ValidationError(f"duplicate catalog case {case_id}")
            tags = set(match.group("tags").split("/"))
            cases[case_id] = {
                "file": path.relative_to(ROOT).as_posix(),
                "line": line_number,
                "priority": match.group("priority"),
                "tags": tags,
            }
            per_file[path.name] += 1
    if not cases:
        raise ValidationError("no catalog cases found")
    return cases, per_file


def validate_readme_counts(per_file: Counter[str], total: int) -> None:
    declared: dict[str, int] = {}
    declared_total: int | None = None
    for line in README.read_text().splitlines():
        match = TABLE_RE.match(line)
        if match:
            declared[match.group("file")] = int(match.group("count"))
        if line.startswith("| **Total**"):
            numbers = re.findall(r"\*\*([0-9]+)\*\*", line)
            if numbers:
                declared_total = int(numbers[-1])
    if declared != dict(per_file):
        raise ValidationError(
            f"README per-file counts {declared} do not match catalog {dict(per_file)}"
        )
    if declared_total != total:
        raise ValidationError(
            f"README total {declared_total} does not match catalog total {total}"
        )


def normalized_repo_path(value: object, field: str) -> Path:
    if (
        not isinstance(value, str)
        or not value
        or "\\" in value
        or Path(value).is_absolute()
        or ".." in Path(value).parts
        or Path(value).as_posix() != value
    ):
        raise ValidationError(f"{field} must be a non-empty repository-relative path")
    root = ROOT.resolve()
    unresolved = ROOT / value
    try:
        path = unresolved.resolve(strict=True)
    except OSError as exc:
        raise ValidationError(f"{field} does not exist: {value}") from exc
    if root not in path.parents or not path.is_file():
        raise ValidationError(f"{field} does not exist: {value}")
    current = ROOT
    for part in Path(value).parts:
        current /= part
        if current.is_symlink():
            raise ValidationError(f"{field} must not traverse a symlink: {value}")
    return path


def unquoted_scalar(value: str) -> str:
    value = value.strip()
    if len(value) >= 2 and value[0] == value[-1] and value[0] in {'"', "'"}:
        return value[1:-1]
    return value


def validate_helper_reference(
    source: Path,
    reference: str,
    validated_helpers: set[Path],
    visiting_helpers: set[Path],
) -> None:
    reference = unquoted_scalar(reference)
    if not reference or "${" in reference or "#" in reference:
        raise ValidationError(
            f"{source.relative_to(ROOT)} has a dynamic or malformed runFlow helper path"
        )
    try:
        helper = (source.parent / reference).resolve(strict=True)
        helpers_root = HELPERS_DIR.resolve(strict=True)
    except OSError as exc:
        raise ValidationError(
            f"{source.relative_to(ROOT)} references a missing runFlow helper: {reference}"
        ) from exc
    if helper.suffix not in {".yaml", ".yml"} or helpers_root not in helper.parents:
        raise ValidationError(
            f"{source.relative_to(ROOT)} runFlow files must live under "
            "e2e/maestro/helpers"
        )
    if helper in visiting_helpers:
        raise ValidationError(f"recursive Maestro helper reference: {helper.relative_to(ROOT)}")
    if helper in validated_helpers:
        return

    visiting_helpers.add(helper)
    validate_maestro_commands(
        helper,
        helper.read_text(),
        validated_helpers=validated_helpers,
        visiting_helpers=visiting_helpers,
    )
    visiting_helpers.remove(helper)
    validated_helpers.add(helper)


def validate_maestro_commands(
    path: Path,
    body: str,
    *,
    validated_helpers: set[Path] | None = None,
    visiting_helpers: set[Path] | None = None,
    first_line_number: int = 1,
) -> None:
    relative = path.relative_to(ROOT).as_posix()
    if re.search(r"https?://", body):
        raise ValidationError(f"{relative} hardcodes a network URL")
    if "maestro cloud" in body.lower():
        raise ValidationError(f"{relative} must not use Maestro Cloud")
    if "${MAESTRO_SERVER_URL}" in body:
        raise ValidationError(f"{relative} may use MAESTRO_SERVER_URL only in its header")
    matches = list(INTERPOLATION_RE.finditer(body))
    if body.count("${") != len(matches):
        raise ValidationError(f"{relative} contains malformed Maestro interpolation")
    for match in matches:
        expression = match.group("expression")
        if not SIMPLE_VARIABLE_RE.fullmatch(expression):
            raise ValidationError(
                f"{relative} contains a dynamic Maestro expression; only fixed variables are allowed"
            )
        if expression not in ALLOWED_BODY_VARIABLES:
            raise ValidationError(f"{relative} uses unapproved Maestro variable {expression}")

    validated_helpers = validated_helpers if validated_helpers is not None else set()
    visiting_helpers = visiting_helpers if visiting_helpers is not None else set()
    active_run_flow_indent: int | None = None
    active_input_text_indent: int | None = None
    for line_number, line in enumerate(body.splitlines(), first_line_number):
        stripped = line.strip()
        if not stripped or stripped.startswith("#"):
            continue
        indent = len(line) - len(line.lstrip())
        if active_run_flow_indent is not None and indent <= active_run_flow_indent:
            active_run_flow_indent = None
        if active_input_text_indent is not None and indent <= active_input_text_indent:
            active_input_text_indent = None

        match = FLOW_COMMAND_RE.match(line)
        if match:
            command = match.group("command")
            suffix = (match.group("suffix") or "").strip()
            if command in FORBIDDEN_FLOW_COMMANDS:
                raise ValidationError(
                    f"{relative}:{line_number} uses forbidden Maestro command {command}"
                )
            if command not in ALLOWED_FLOW_COMMANDS:
                raise ValidationError(
                    f"{relative}:{line_number} uses unapproved Maestro command {command}"
                )
            if command == "launchApp" and suffix:
                raise ValidationError(
                    f"{relative}:{line_number} must launch only the configured lab URL"
                )
            if command == "runFlow":
                reference = suffix[1:].strip() if suffix.startswith(":") else suffix
                if reference:
                    validate_helper_reference(
                        path, reference, validated_helpers, visiting_helpers
                    )
                else:
                    active_run_flow_indent = indent
            if command == "inputText":
                value = suffix[1:].strip() if suffix.startswith(":") else suffix
                if value:
                    if "${MAESTRO_PASSWORD}" in value and unquoted_scalar(value) != "${MAESTRO_PASSWORD}":
                        raise ValidationError(
                            f"{relative}:{line_number} mixes the lab password with other input"
                        )
                else:
                    active_input_text_indent = indent
            elif "${MAESTRO_PASSWORD}" in line:
                raise ValidationError(
                    f"{relative}:{line_number} may use the lab password only as inputText"
                )
            continue

        if stripped.startswith("-"):
            raise ValidationError(
                f"{relative}:{line_number} uses unsupported Maestro command syntax"
            )

        run_flow_file = RUN_FLOW_FILE_RE.match(line)
        if active_run_flow_indent is not None and run_flow_file:
            validate_helper_reference(
                path,
                run_flow_file.group("path"),
                validated_helpers,
                visiting_helpers,
            )
        if "${MAESTRO_PASSWORD}" in line:
            allowed_password_line = (
                active_input_text_indent is not None
                and stripped.startswith("text:")
                and unquoted_scalar(stripped.split(":", 1)[1]) == "${MAESTRO_PASSWORD}"
            )
            if not allowed_password_line:
                raise ValidationError(
                    f"{relative}:{line_number} may use the lab password only as inputText"
                )


def validate_flow(path: Path, validated_helpers: set[Path] | None = None) -> str:
    relative = path.relative_to(ROOT).as_posix()
    if path.is_symlink():
        raise ValidationError(f"{relative} must not be a symlink")
    text = path.read_text()
    lines = text.splitlines()
    if not lines or lines[0] != FLOW_URL_HEADER:
        raise ValidationError(
            f"{relative} must use the exact loopback origin supplied by the lab"
        )
    if "\n---\n" not in text or "\ntags:\n" not in text or "\nname:" not in text:
        raise ValidationError(f"{relative} is missing Maestro name, tags, or header separator")
    if "${MAESTRO_PASSWORD}" not in text or "${MAESTRO_USERNAME}" not in text:
        raise ValidationError(f"{relative} must receive the selected lab identity from the wrapper")

    header, body = text.split("\n---\n", 1)
    header_lines = header.splitlines()
    canonical_header = (
        len(header_lines) >= 4
        and header_lines[0] == FLOW_URL_HEADER
        and header_lines[1].startswith("name: ")
        and bool(header_lines[1].removeprefix("name: ").strip())
        and header_lines[2] == "tags:"
        and all(FLOW_TAG_RE.fullmatch(line) for line in header_lines[3:])
    )
    if not canonical_header:
        raise ValidationError(f"{relative} has fields outside the reviewed Maestro header schema")
    if header.count("${MAESTRO_SERVER_URL}") != 1:
        raise ValidationError(f"{relative} has an invalid Maestro server URL header")
    if "${MAESTRO_PASSWORD}" in header:
        raise ValidationError(f"{relative} may use the lab password only in inputText")
    header_interpolations = [
        match.group("expression") for match in INTERPOLATION_RE.finditer(header)
    ]
    if header_interpolations != ["MAESTRO_SERVER_URL"]:
        raise ValidationError(f"{relative} has an unsafe Maestro header interpolation")
    validate_maestro_commands(
        path,
        body,
        validated_helpers=validated_helpers,
        first_line_number=len(header.splitlines()) + 2,
    )
    return header_lines[1].removeprefix("name: ").strip()


def validate_coverage_plan(
    cases: dict[str, dict[str, object]],
) -> dict[str, dict[str, object]]:
    data = load_json(COVERAGE_PLAN)
    expected_top_level = {"schema_version", "methodology", "counts", "cases"}
    if not isinstance(data, dict) or set(data) != expected_top_level:
        raise ValidationError(
            f"coverage plan fields must be exactly {sorted(expected_top_level)}"
        )
    if data.get("schema_version") != 1:
        raise ValidationError("coverage plan schema_version must be 1")

    methodology = data.get("methodology")
    expected_methodology = {
        "completion_rule",
        "dispositions",
        "dominant_layer",
        "live_policy",
        "purpose",
    }
    if not isinstance(methodology, dict) or set(methodology) != expected_methodology:
        raise ValidationError(
            "coverage plan methodology fields must be exactly "
            f"{sorted(expected_methodology)}"
        )
    dispositions = methodology.get("dispositions")
    if not isinstance(dispositions, dict) or set(dispositions) != ALLOWED_DISPOSITIONS:
        raise ValidationError("coverage plan methodology must define every disposition")
    methodology_text = [
        methodology["completion_rule"],
        methodology["dominant_layer"],
        methodology["live_policy"],
        methodology["purpose"],
        *dispositions.values(),
    ]
    if any(not isinstance(value, str) or not value.strip() for value in methodology_text):
        raise ValidationError("coverage plan methodology descriptions must be non-empty")

    plan_cases = data.get("cases")
    if not isinstance(plan_cases, list):
        raise ValidationError("coverage plan cases must be a list")
    mapped: dict[str, dict[str, object]] = {}
    dominant_counts: Counter[str] = Counter()
    disposition_counts: Counter[str] = Counter()
    for index, entry in enumerate(plan_cases):
        if not isinstance(entry, dict):
            raise ValidationError(f"coverage plan case {index} must be an object")
        required = {"case_id", "dominant_layer", "disposition", "recommended_layers"}
        allowed = required | {"classification_note"}
        if not required.issubset(entry) or not set(entry).issubset(allowed):
            raise ValidationError(
                f"coverage plan case {index} needs exactly {sorted(required)} "
                "plus optional classification_note"
            )
        case_id = entry["case_id"]
        dominant_layer = entry["dominant_layer"]
        disposition = entry["disposition"]
        recommended_layers = entry["recommended_layers"]
        if not isinstance(case_id, str) or not CASE_ID_RE.fullmatch(case_id):
            raise ValidationError(f"coverage plan case {index} has an invalid case_id")
        if case_id in mapped:
            raise ValidationError(f"coverage plan repeats case {case_id}")
        if not isinstance(dominant_layer, str) or dominant_layer not in ALLOWED_DOMINANT_LAYERS:
            raise ValidationError(f"coverage plan case {case_id} has an invalid dominant_layer")
        if not isinstance(disposition, str) or disposition not in ALLOWED_DISPOSITIONS:
            raise ValidationError(f"coverage plan case {case_id} has an invalid disposition")
        if (
            not isinstance(recommended_layers, list)
            or not recommended_layers
            or any(
                not isinstance(layer, str) or layer not in ALLOWED_RECOMMENDED_LAYERS
                for layer in recommended_layers
            )
            or len(set(recommended_layers)) != len(recommended_layers)
        ):
            raise ValidationError(f"coverage plan case {case_id} has invalid recommended_layers")
        if dominant_layer not in recommended_layers:
            raise ValidationError(
                f"coverage plan case {case_id} must include its dominant_layer "
                "in recommended_layers"
            )
        if "classification_note" in entry and (
            not isinstance(entry["classification_note"], str)
            or not entry["classification_note"].strip()
        ):
            raise ValidationError(
                f"coverage plan case {case_id} has an empty classification_note"
            )
        mapped[case_id] = entry
        dominant_counts[dominant_layer] += 1
        disposition_counts[disposition] += 1

    catalog_ids = set(cases)
    plan_ids = set(mapped)
    if plan_ids != catalog_ids:
        missing = sorted(catalog_ids - plan_ids)
        extra = sorted(plan_ids - catalog_ids)
        raise ValidationError(f"coverage plan/catalog mismatch; missing={missing}, extra={extra}")
    for case_id, entry in mapped.items():
        is_gap = "GAP" in cases[case_id]["tags"]
        is_blocked = entry["disposition"] == "blocked"
        if is_gap != is_blocked:
            raise ValidationError(
                f"coverage plan case {case_id} must be blocked exactly when the catalog carries GAP"
            )

    counts = data.get("counts")
    expected_count_fields = {"total", "dominant_layer", "disposition"}
    if not isinstance(counts, dict) or set(counts) != expected_count_fields:
        raise ValidationError(
            f"coverage plan counts fields must be exactly {sorted(expected_count_fields)}"
        )
    if (
        not isinstance(counts["total"], int)
        or isinstance(counts["total"], bool)
        or counts["total"] != len(cases)
        or counts["dominant_layer"] != dict(dominant_counts)
        or counts["disposition"] != dict(disposition_counts)
    ):
        raise ValidationError("coverage plan declared counts do not match its cases")
    for field, allowed_values in (
        ("dominant_layer", ALLOWED_DOMINANT_LAYERS),
        ("disposition", ALLOWED_DISPOSITIONS),
    ):
        value = counts[field]
        if (
            not isinstance(value, dict)
            or not set(value).issubset(allowed_values)
            or any(
                not isinstance(count, int) or isinstance(count, bool) or count < 0
                for count in value.values()
            )
        ):
            raise ValidationError(f"coverage plan {field} counts are invalid")
    return mapped


def _text_file(path: Path, field: str) -> str:
    try:
        return path.read_text()
    except (OSError, UnicodeDecodeError) as exc:
        raise ValidationError(f"{field} must be a UTF-8 text file") from exc


def _contains_catalog_id(body: str, case_id: str) -> bool:
    return re.search(
        rf"(?<![A-Z0-9-]){re.escape(case_id)}(?![A-Z0-9-])", body
    ) is not None


def _nearby_annotation(body: str, position: int, case_id: str) -> bool:
    if _contains_catalog_id(body[position : body.find("\n", position)], case_id):
        return True
    lines = body[:position].splitlines()
    comments: list[str] = []
    while lines:
        stripped = lines.pop().strip()
        if not stripped and not comments:
            continue
        if (
            stripped.startswith("//")
            or stripped.startswith("#")
            or stripped.startswith("/*")
            or stripped.startswith("*")
            or stripped.endswith("*/")
        ):
            comments.append(stripped)
            continue
        break
    return _contains_catalog_id("\n".join(comments), case_id)


def _dart_test_declarations(
    body: str, call_names: tuple[str, ...]
) -> dict[str, list[tuple[int, bool]]]:
    declarations: dict[str, list[tuple[int, bool]]] = {}
    calls = "|".join(re.escape(name) for name in call_names)
    for quote in ("'", '"'):
        pattern = re.compile(
            rf"(?m)^[ \t]*(?:{calls})\s*\(\s*(?P<raw>r)?{re.escape(quote)}"
            rf"(?P<value>(?:\\.|[^{re.escape(quote)}\\\n])*){re.escape(quote)}"
        )
        for match in pattern.finditer(body):
            if body.rfind("/*", 0, match.start()) > body.rfind("*/", 0, match.start()):
                continue
            value = match.group("value")
            if match.group("raw"):
                description = value
            else:
                try:
                    description = ast.literal_eval(f"{quote}{value}{quote}")
                except (SyntaxError, ValueError):
                    description = value
            next_match = re.search(
                rf"(?m)^[ \t]*(?:{calls})\s*\(", body[match.end() :]
            )
            end = match.end() + next_match.start() if next_match else len(body)
            skipped = re.search(r"\bskip\s*:", body[match.end() : end]) is not None
            declarations.setdefault(description, []).append((match.start(), skipped))
    return declarations


def _flow_leading_annotations(body: str) -> str:
    _, commands = body.split("\n---\n", 1)
    comments: list[str] = []
    for line in commands.splitlines():
        stripped = line.strip()
        if not stripped:
            continue
        if stripped.startswith("#"):
            comments.append(stripped)
            continue
        break
    return "\n".join(comments)


def _workflow_step_names(body: str) -> set[str]:
    names: set[str] = set()
    for line in body.splitlines():
        match = re.fullmatch(r"\s*-\s+name:\s+(.+?)\s*", line)
        if match:
            names.add(unquoted_scalar(match.group(1)))
    return names


def validate_evidence(
    case_id: str,
    evidence: object,
    evidence_index: int,
) -> tuple[str, Path]:
    if not isinstance(evidence, dict):
        raise ValidationError(f"proof {case_id} evidence {evidence_index} must be an object")
    expected = {"kind", "path", "selector"}
    if set(evidence) != expected:
        raise ValidationError(
            f"proof {case_id} evidence {evidence_index} fields must be exactly {sorted(expected)}"
        )
    kind = evidence["kind"]
    selector = evidence["selector"]
    if not isinstance(kind, str) or kind not in ALLOWED_EVIDENCE_KINDS:
        raise ValidationError(f"proof {case_id} evidence {evidence_index} has invalid kind")
    if (
        not isinstance(selector, str)
        or not selector.strip()
        or selector != selector.strip()
        or len(selector) > 500
        or "\n" in selector
        or "\r" in selector
    ):
        raise ValidationError(f"proof {case_id} evidence {evidence_index} has invalid selector")
    path = normalized_repo_path(
        evidence["path"], f"proof {case_id} evidence {evidence_index} path"
    )
    relative = path.relative_to(ROOT.resolve()).as_posix()
    body = _text_file(path, f"proof {case_id} evidence {evidence_index} path")

    if kind == "maestro-flow":
        if not relative.startswith("e2e/maestro/flows/") or path.suffix != ".yaml":
            raise ValidationError(f"Maestro evidence must be a .yaml flow: {relative}")
        if validate_flow(path) != selector:
            raise ValidationError(f"Maestro selector does not match the flow name: {relative}")
        if not _contains_catalog_id(selector + "\n" + _flow_leading_annotations(body), case_id):
            raise ValidationError(f"Maestro flow annotation does not name {case_id}: {relative}")
    elif kind == "go-test":
        if not relative.startswith("server/") or not relative.endswith("_test.go"):
            raise ValidationError(f"Go evidence must be a server *_test.go file: {relative}")
        if re.search(r"(?m)^//(?:go:build|\s*\+build)\b", body):
            raise ValidationError(f"Go evidence must not be build-tag excluded: {relative}")
        declaration = re.search(
            rf"(?m)^func[ \t]+{re.escape(selector)}[ \t]*\("
            rf"(?P<param>[A-Za-z_][A-Za-z0-9_]*)[ \t]+\*testing\.T[ \t]*\)[ \t]*\{{",
            body,
        )
        if not GO_TEST_SELECTOR_RE.fullmatch(selector) or declaration is None:
            raise ValidationError(f"Go selector is not an exact test function: {relative}")
        if not _nearby_annotation(body, declaration.start(), case_id):
            raise ValidationError(f"Go test annotation does not name {case_id}: {relative}")
        next_function = re.search(r"(?m)^func[ \t]+", body[declaration.end() :])
        end = (
            declaration.end() + next_function.start()
            if next_function
            else len(body)
        )
        selected_body = body[declaration.end() : end]
        if re.search(
            rf"\b{re.escape(declaration.group('param'))}\."
            r"(?:Skip|Skipf|SkipNow)\s*\(",
            selected_body,
        ):
            raise ValidationError(f"Go evidence test may not skip execution: {relative}")
    elif kind == "flutter-test":
        if not relative.startswith("app/test/") or not relative.endswith("_test.dart"):
            raise ValidationError(
                f"Flutter evidence must be an app/test *_test.dart file: {relative}"
            )
        declarations = _dart_test_declarations(body, ("test", "testWidgets")).get(selector, [])
        if len(declarations) != 1 or declarations[0][1]:
            raise ValidationError(f"Flutter selector is not an exact test description: {relative}")
        if not _nearby_annotation(body, declarations[0][0], case_id):
            raise ValidationError(f"Flutter test annotation does not name {case_id}: {relative}")
    elif kind == "workflow-step":
        if not relative.startswith(".github/workflows/") or path.suffix not in {
            ".yml",
            ".yaml",
        }:
            raise ValidationError(f"workflow evidence must be a GitHub workflow file: {relative}")
        if selector not in _workflow_step_names(body):
            raise ValidationError(f"workflow selector is not an exact step name: {relative}")
        if not _contains_catalog_id(selector, case_id):
            raise ValidationError(f"workflow step name does not name {case_id}: {relative}")
    elif kind == "script-check":
        if not (relative.startswith("scripts/") or relative.startswith("app/tool/")):
            raise ValidationError(
                f"script evidence must live under scripts or app/tool: {relative}"
            )
        if selector not in body:
            raise ValidationError(f"script selector is not an exact literal: {relative}")
        if not _contains_catalog_id(selector, case_id):
            raise ValidationError(f"script selector does not name {case_id}: {relative}")
    elif kind == "patrol-test":
        if not relative.startswith("app/integration_test/") or not relative.endswith("_test.dart"):
            raise ValidationError(
                f"Patrol evidence must be an app/integration_test *_test.dart file: {relative}"
            )
        declarations = _dart_test_declarations(
            body, ("patrolTest", "patrolWidgetTest")
        ).get(selector, [])
        if len(declarations) != 1 or declarations[0][1]:
            raise ValidationError(f"Patrol selector is not an exact test description: {relative}")
        if not _nearby_annotation(body, declarations[0][0], case_id):
            raise ValidationError(f"Patrol test annotation does not name {case_id}: {relative}")
    return kind, path


def validate_manifest(
    cases: dict[str, dict[str, object]],
    coverage_plan: dict[str, dict[str, object]],
) -> tuple[int, int, set[Path]]:
    data = load_json(MANIFEST)
    if not isinstance(data, dict) or set(data) != {"schema_version", "proofs"}:
        raise ValidationError(
            "automation manifest fields must be exactly schema_version and proofs"
        )
    if data.get("schema_version") != 2:
        raise ValidationError("automation manifest schema_version must be 2")
    proofs = data.get("proofs")
    if not isinstance(proofs, list):
        raise ValidationError("automation manifest proofs must be a list")

    seen_cases: set[str] = set()
    automated_cases: set[str] = set()
    mapped_flows: set[Path] = set()
    statuses: Counter[str] = Counter()
    for index, proof in enumerate(proofs):
        if not isinstance(proof, dict):
            raise ValidationError(f"proof {index} must be an object")
        expected = {"case_id", "status", "scope", "evidence"}
        if set(proof) != expected:
            raise ValidationError(f"proof {index} fields must be exactly {sorted(expected)}")
        case_id = proof["case_id"]
        status = proof["status"]
        scope = proof["scope"]
        evidence_entries = proof["evidence"]
        if not isinstance(case_id, str) or not CASE_ID_RE.fullmatch(case_id):
            raise ValidationError(f"proof {index} has an invalid case_id")
        if case_id not in cases:
            raise ValidationError(f"proof {index} references unknown case {case_id}")
        if case_id in seen_cases:
            raise ValidationError(f"duplicate proof mapping for {case_id}")
        seen_cases.add(case_id)
        if not isinstance(status, str) or status not in ALLOWED_STATUS:
            raise ValidationError(f"proof {case_id} has invalid status {status}")
        if (
            not isinstance(scope, str)
            or not scope.strip()
            or scope != scope.strip()
            or len(scope) > 2_000
        ):
            raise ValidationError(f"proof {case_id} needs a non-empty scope statement")
        if not isinstance(evidence_entries, list) or not evidence_entries:
            raise ValidationError(f"proof {case_id} needs at least one evidence entry")

        seen_evidence: set[tuple[str, str, str]] = set()
        recommended_layers = set(coverage_plan[case_id]["recommended_layers"])
        for evidence_index, evidence in enumerate(evidence_entries):
            kind, path = validate_evidence(case_id, evidence, evidence_index)
            identity = (kind, path.relative_to(ROOT.resolve()).as_posix(), evidence["selector"])
            if identity in seen_evidence:
                raise ValidationError(f"proof {case_id} repeats an evidence entry")
            seen_evidence.add(identity)
            evidence_layer = EVIDENCE_LAYERS[kind]
            if evidence_layer not in recommended_layers:
                raise ValidationError(
                    f"proof {case_id} uses {evidence_layer} evidence outside recommended_layers"
                )
            if kind == "maestro-flow":
                mapped_flows.add(path)
        if status == "automated":
            if coverage_plan[case_id]["disposition"] != "automatable":
                raise ValidationError(
                    f"automated case {case_id} must have automatable disposition"
                )
            if "AUTO" not in cases[case_id]["tags"]:
                raise ValidationError(f"automated case {case_id} must carry the AUTO catalog tag")
            automated_cases.add(case_id)
        statuses[status] += 1

    auto_cases = {case_id for case_id, data in cases.items() if "AUTO" in data["tags"]}
    missing_automated = sorted(auto_cases - automated_cases)
    if missing_automated:
        raise ValidationError(
            "AUTO catalog cases need automated proof mappings: " + ", ".join(missing_automated)
        )
    return statuses["automated"], statuses["partial"], mapped_flows


def validate_suites(mapped_flows: set[Path]) -> None:
    data = load_json(SUITES)
    if not isinstance(data, dict) or data.get("schema_version") != 1:
        raise ValidationError("Maestro suites schema_version must be 1")
    suites = data.get("suites")
    if not isinstance(suites, dict) or not suites:
        raise ValidationError("Maestro suites must be a non-empty object")

    suite_flows: set[Path] = set()
    validated_helpers: set[Path] = set()
    for suite_name, entries in suites.items():
        if not re.fullmatch(r"[a-z0-9-]+", suite_name) or not isinstance(entries, list) or not entries:
            raise ValidationError(f"invalid or empty Maestro suite {suite_name}")
        suite_slugs: set[str] = set()
        suite_flow_names: set[str] = set()
        for entry in entries:
            if not isinstance(entry, dict) or set(entry) != {"flow", "user"}:
                raise ValidationError(f"suite {suite_name} entries need exactly flow and user")
            if entry["user"] not in ALLOWED_USERS:
                raise ValidationError(f"suite {suite_name} uses unknown lab user {entry['user']}")
            flow = normalized_repo_path(entry["flow"], f"suite {suite_name} flow")
            if FLOWS_DIR not in flow.parents:
                raise ValidationError(f"suite {suite_name} flow is outside e2e/maestro/flows")
            if flow.suffix != ".yaml":
                raise ValidationError(f"suite {suite_name} flows must use the .yaml extension")
            flow_name = validate_flow(flow, validated_helpers)
            relative_flow = flow.relative_to(FLOWS_DIR).with_suffix("")
            slug = "-".join(relative_flow.parts)
            if slug in suite_slugs:
                raise ValidationError(f"suite {suite_name} has a colliding flow slug: {slug}")
            if flow_name in suite_flow_names:
                raise ValidationError(
                    f"suite {suite_name} has a duplicate flow name: {flow_name}"
                )
            suite_slugs.add(slug)
            suite_flow_names.add(flow_name)
            if flow in suite_flows:
                raise ValidationError(f"flow appears more than once across suites: {flow.relative_to(ROOT)}")
            suite_flows.add(flow)

    legacy_flows = set(FLOWS_DIR.rglob("*.yml"))
    if legacy_flows:
        paths = sorted(path.relative_to(ROOT).as_posix() for path in legacy_flows)
        raise ValidationError(f"Maestro flows must use the .yaml extension: {paths}")
    repository_flows = set(FLOWS_DIR.rglob("*.yaml"))
    if repository_flows != suite_flows:
        missing = sorted(path.relative_to(ROOT).as_posix() for path in repository_flows - suite_flows)
        extra = sorted(path.relative_to(ROOT).as_posix() for path in suite_flows - repository_flows)
        raise ValidationError(f"suite/flow mismatch; missing={missing}, extra={extra}")
    if not suite_flows.issubset(mapped_flows):
        missing = sorted(path.relative_to(ROOT).as_posix() for path in suite_flows - mapped_flows)
        raise ValidationError(f"Maestro flows lack catalog mappings: {missing}")

    repository_helpers = set(HELPERS_DIR.rglob("*.yaml")) | set(HELPERS_DIR.rglob("*.yml"))
    for helper in sorted(repository_helpers - validated_helpers):
        validate_maestro_commands(helper, helper.read_text(), validated_helpers=validated_helpers)


def main() -> int:
    try:
        validate_maestro_config(MAESTRO_CONFIG)
    except SafetyError as exc:
        raise ValidationError(f"unsafe e2e/maestro/config.yaml: {exc}") from exc
    cases, per_file = parse_catalog()
    validate_readme_counts(per_file, len(cases))
    coverage_plan = validate_coverage_plan(cases)
    automated, partial, mapped_flows = validate_manifest(cases, coverage_plan)
    validate_suites(mapped_flows)

    auto_cases = {case_id for case_id, data in cases.items() if "AUTO" in data["tags"]}
    print(
        "test automation catalog valid: "
        f"{len(cases)} parent cases, {automated} automated mapping(s), "
        f"{partial} partial mapping(s), {len(mapped_flows)} Maestro flow(s), "
        f"{len(auto_cases)} AUTO catalog case(s) linked"
    )
    return 0


def cli() -> int:
    arguments = sys.argv[1:]
    if arguments:
        if len(arguments) != 2 or arguments[0] != "--flow":
            raise ValidationError("usage: check_test_automation.py [--flow FLOW.yaml]")
        try:
            validate_maestro_config(MAESTRO_CONFIG)
        except SafetyError as exc:
            raise ValidationError(f"unsafe e2e/maestro/config.yaml: {exc}") from exc
        flow = Path(arguments[1])
        if not flow.is_absolute() or FLOWS_DIR not in flow.parents:
            raise ValidationError("runtime flow must be an absolute path under e2e/maestro/flows")
        validate_flow(flow)
        return 0
    return main()


if __name__ == "__main__":
    try:
        raise SystemExit(cli())
    except ValidationError as exc:
        print(f"test automation validation failed: {exc}", file=sys.stderr)
        raise SystemExit(1)
