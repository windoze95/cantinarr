#!/usr/bin/env python3
"""Validate the master catalog format/counts and Maestro flow safety."""

from __future__ import annotations

from collections import Counter
import json
from pathlib import Path
import re
import sys

from maestro_safety import SafetyError, validate_maestro_config


ROOT = Path(__file__).resolve().parents[1]
CATALOG_DIR = ROOT / "docs" / "testing" / "catalog"
README = ROOT / "docs" / "testing" / "README.md"
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


def validate_suites() -> tuple[int, int]:
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
    for flow in sorted(repository_flows - suite_flows):
        validate_flow(flow, validated_helpers)

    repository_helpers = set(HELPERS_DIR.rglob("*.yaml")) | set(HELPERS_DIR.rglob("*.yml"))
    for helper in sorted(repository_helpers - validated_helpers):
        validate_maestro_commands(helper, helper.read_text(), validated_helpers=validated_helpers)
    return len(suite_flows), len(repository_flows)


def main() -> int:
    try:
        validate_maestro_config(MAESTRO_CONFIG)
    except SafetyError as exc:
        raise ValidationError(f"unsafe e2e/maestro/config.yaml: {exc}") from exc
    cases, per_file = parse_catalog()
    validate_readme_counts(per_file, len(cases))
    suite_flows, repository_flows = validate_suites()
    print(
        "test catalog valid: "
        f"{len(cases)} parent cases; {repository_flows} Maestro flow(s) "
        f"safety-checked, {suite_flows} mapped in suites"
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
