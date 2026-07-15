#!/usr/bin/env python3
"""Validate the canonical regression catalog's structural invariants."""

from __future__ import annotations

import argparse
from collections import Counter
from pathlib import Path
import re
import subprocess
import sys


HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]
INDEX = HERE / "README.md"
CATALOG_DIR = HERE / "catalog"
RETIRED = HERE / "retired-cases.md"
KNOWN_GAPS = HERE / "known-gaps.md"

CASE_RE = re.compile(
    r"^- \[(?P<state>[ xX])\] `(?P<id>[A-Z]+-\d{3})` · "
    r"(?P<priority>P[0-2]) · (?P<tags>[A-Z]+(?:/[A-Z]+)*) — (?P<description>\S.*)$"
)
GROUP_RE = re.compile(
    r"^\| \[(?P<label>[^]]+)\]\((?P<path>catalog/[^)]+\.md)\) \| "
    r"(?P<prefixes>[A-Z]+(?:, [A-Z]+)*) \| (?P<count>\d+) \|$"
)
RETIRED_RE = re.compile(r"^\| `(?P<id>[A-Z]+-\d{3})` \|(?P<fields>.+)\|$")
GAP_RE = re.compile(r"^\| `(?P<id>[A-Z]+-\d{3})` \|(?P<fields>.+)\|$")
SUMMARY_RE = re.compile(
    r"contains \*\*(?P<total>\d+) active cases\*\*: \*\*(?P<p0>\d+) P0\*\*, "
    r"\*\*(?P<p1>\d+) P1\*\*, and \*\*(?P<p2>\d+) P2\*\*\."
)
TOTAL_RE = re.compile(r"^\| \*\*Total\*\* \| \| \*\*(?P<count>\d+)\*\* \|$")
KNOWN_TAGS = {"AUTO", "API", "UI", "LIVE", "CHAOS", "SEC", "RT", "GAP"}
ALLOWED_PREFIXES = {
    "AI", "AUTH", "BASE", "BOOK", "DISC", "DOWN", "EXP", "INST", "ISS", "MCP", "NAV", "OPS",
    "PERF", "PLEX", "PUSH", "RAD", "REL", "REQ", "RT", "SEC", "SON", "TAUT", "USER", "UX",
}
NON_CASE_TOKENS = {"AES-256"}
RESULT_ANNOTATION_RE = re.compile(r"— (?:PASS|FAIL|BLOCKED|N/A):")
AUTO_REFERENCE_RE = re.compile(r"AUTO:\s+`?(?P<path>[^`\s:]+)::(?P<symbol>[^`\s]+)`?")
MARKDOWN_LINK_RE = re.compile(r"\[[^]]+\]\((?P<target>[^)]+)\)")
ID_REFERENCE_RE = re.compile(r"\b[A-Z]+-\d{3}\b")
EXPLICIT_ID_REFERENCE_RE = re.compile(r"`(?P<id>[A-Z]+-\d{3})`")
VECTOR_PARENT_RE = re.compile(r"^\| (?P<id>[A-Z]+-\d{3}) \|")


def add_error(errors: list[str], message: str) -> None:
    errors.append(message)


def read_retired(text: str, errors: list[str]) -> set[str]:
    retired: set[str] = set()
    for line_number, line in enumerate(text.splitlines(), 1):
        if not line.startswith("| `"):
            continue
        match = RETIRED_RE.fullmatch(line)
        if match is None:
            add_error(errors, f"retired-cases.md:{line_number}: malformed retired-ID row")
            continue
        fields = [field.strip() for field in match.group("fields").split("|")]
        if len(fields) != 3 or any(not field for field in fields):
            add_error(
                errors,
                f"retired-cases.md:{line_number}: reason, replacement (or None), and PR/release are required",
            )
            continue
        case_id = match.group("id")
        if case_id in retired:
            add_error(errors, f"retired-cases.md:{line_number}: duplicate retired ID {case_id}")
        retired.add(case_id)
    return retired


def read_known_gaps(text: str, errors: list[str]) -> dict[str, str]:
    gaps: dict[str, str] = {}
    for line_number, line in enumerate(text.splitlines(), 1):
        if not line.startswith("| `"):
            continue
        match = GAP_RE.fullmatch(line)
        if match is None:
            add_error(errors, f"known-gaps.md:{line_number}: malformed GAP row")
            continue
        fields = [field.strip() for field in match.group("fields").split("|")]
        if len(fields) != 4 or any(not field for field in fields):
            add_error(errors, f"known-gaps.md:{line_number}: status, owner, limitation, and accepted reference are required")
            continue
        status = fields[0]
        if status not in {"Open", "Resolved", "Withdrawn"}:
            add_error(errors, f"known-gaps.md:{line_number}: unknown GAP status {status!r}")
        if not MARKDOWN_LINK_RE.search(fields[3]) and not re.search(r"https?://|#[0-9]+|\bv\d", fields[3]):
            add_error(errors, f"known-gaps.md:{line_number}: GAP needs a linked decision, issue, PR, or release")
        case_id = match.group("id")
        if case_id in gaps:
            add_error(errors, f"known-gaps.md:{line_number}: duplicate GAP ID {case_id}")
        gaps[case_id] = status
    return gaps


def git_text(ref: str, path: str) -> str | None:
    result = subprocess.run(
        ["git", "show", f"{ref}:{path}"],
        cwd=ROOT,
        check=False,
        capture_output=True,
        text=True,
    )
    return result.stdout if result.returncode == 0 else None


def previous_ids(ref: str, errors: list[str]) -> tuple[set[str], set[str], set[str]]:
    verify = subprocess.run(
        ["git", "rev-parse", "--verify", f"{ref}^{{commit}}"],
        cwd=ROOT,
        check=False,
        capture_output=True,
        text=True,
    )
    if verify.returncode != 0:
        add_error(errors, f"cannot resolve catalog history base ref {ref!r}")
        return set(), set(), set()
    result = subprocess.run(
        ["git", "ls-tree", "-r", "--name-only", ref, "--", "docs/testing/catalog"],
        cwd=ROOT,
        check=False,
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        add_error(errors, f"cannot read catalog history from base ref {ref!r}")
        return set(), set(), set()

    active: set[str] = set()
    paths = result.stdout.splitlines()
    for path in paths:
        content = git_text(ref, path)
        if content is None:
            add_error(errors, f"cannot read historical catalog file {path!r} from {ref!r}")
            continue
        active.update(match.group("id") for match in map(CASE_RE.fullmatch, content.splitlines()) if match)

    retired_content = git_text(ref, "docs/testing/retired-cases.md")
    old_retired: set[str] = set()
    if paths and retired_content is None:
        add_error(errors, f"cannot read historical retired-ID ledger from {ref!r}")
    elif retired_content:
        old_retired = {
            match.group("id")
            for match in map(RETIRED_RE.fullmatch, retired_content.splitlines())
            if match
        }
    known_gaps_content = git_text(ref, "docs/testing/known-gaps.md")
    old_gaps: set[str] = set()
    if paths and known_gaps_content is None:
        add_error(errors, f"cannot read historical GAP ledger from {ref!r}")
    elif known_gaps_content:
        old_gaps = {
            match.group("id")
            for match in map(GAP_RE.fullmatch, known_gaps_content.splitlines())
            if match
        }
    return active, old_retired, old_gaps


def default_base_ref(errors: list[str]) -> str:
    result = subprocess.run(
        ["git", "merge-base", "HEAD", "origin/main"],
        cwd=ROOT,
        check=False,
        capture_output=True,
        text=True,
    )
    if result.returncode != 0 or not result.stdout.strip():
        add_error(errors, "cannot determine merge base of HEAD and origin/main; fetch origin or pass --base-ref")
        return ""
    return result.stdout.strip()


def heading_anchor(heading: str) -> str:
    anchor = re.sub(r"[^\w\- ]", "", heading.strip().lower())
    return re.sub(r" +", "-", anchor)


def validate_links(path: Path, text: str, errors: list[str]) -> None:
    for match in MARKDOWN_LINK_RE.finditer(text):
        target = match.group("target")
        if target.startswith(("http://", "https://", "/")):
            continue
        target_path, _, anchor = target.partition("#")
        resolved = (path.parent / target_path).resolve() if target_path else path.resolve()
        if target_path and not resolved.exists():
            add_error(errors, f"{path.relative_to(ROOT)}: broken link target {target}")
            continue
        if anchor and resolved.is_file():
            headings = {
                heading_anchor(line.lstrip("# "))
                for line in resolved.read_text(encoding="utf-8").splitlines()
                if line.startswith("#")
            }
            if anchor not in headings:
                add_error(errors, f"{path.relative_to(ROOT)}: broken Markdown anchor {target}")


def validate_run_template(path: Path, text: str, errors: list[str]) -> None:
    section = ""
    section_counts: Counter[str] = Counter()
    for line_number, line in enumerate(text.splitlines(), 1):
        if line.startswith("## "):
            section = line[3:]
            section_counts[section] += 1
            continue
        if not line.startswith("|") or line.startswith("|---"):
            continue
        cells = [cell.strip() for cell in line.strip("|").split("|")]
        if section == "Run record" and cells != ["Field", "Value"]:
            if len(cells) != 2:
                add_error(errors, f"{path.relative_to(ROOT)}:{line_number}: malformed run-record row")
                continue
            field, value = cells
            expected = "NOT RUN" if field == "Result" else ""
            if value != expected:
                add_error(errors, f"{path.relative_to(ROOT)}:{line_number}: committed run metadata is not blank")
        elif section == "Execution evidence log" and cells != [
            "Case ID",
            "Result",
            "Version / environment",
            "Evidence",
            "Defect / note",
            "Tester / date",
        ]:
            if any(cells):
                add_error(errors, f"{path.relative_to(ROOT)}:{line_number}: committed execution evidence is not blank")
        elif section not in {"Run record", "Execution evidence log"}:
            add_error(errors, f"{path.relative_to(ROOT)}:{line_number}: unexpected table outside a run-neutral section")
    for required_section in ("Run record", "Execution evidence log"):
        if section_counts[required_section] != 1:
            add_error(
                errors,
                f"{path.relative_to(ROOT)}: expected exactly one {required_section!r} heading; "
                f"found {section_counts[required_section]}",
            )


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--base-ref",
        help="Git ref used to verify ID history (default: merge-base of HEAD and origin/main)",
    )
    args = parser.parse_args()
    errors: list[str] = []

    required = [INDEX, CATALOG_DIR, RETIRED, KNOWN_GAPS, HERE / "fixtures.md", HERE / "run-template.md"]
    for path in required:
        if not path.exists():
            add_error(errors, f"missing required catalog path: {path.relative_to(ROOT)}")
    if errors:
        print("\n".join(f"ERROR: {error}" for error in errors), file=sys.stderr)
        return 1

    index_text = INDEX.read_text(encoding="utf-8")
    retired_text = RETIRED.read_text(encoding="utf-8")
    retired = read_retired(retired_text, errors)
    known_gaps_text = KNOWN_GAPS.read_text(encoding="utf-8")
    gap_ledger = read_known_gaps(known_gaps_text, errors)

    cases: dict[str, tuple[Path, int, str, str, str]] = {}
    priorities: Counter[str] = Counter()
    prefixes: Counter[str] = Counter()
    file_prefixes: dict[str, Counter[str]] = {}
    catalog_texts: dict[Path, str] = {}

    catalog_files = sorted(CATALOG_DIR.glob("*.md"))
    if not catalog_files:
        add_error(errors, "catalog directory contains no Markdown files")

    for path in catalog_files:
        text = path.read_text(encoding="utf-8")
        catalog_texts[path] = text
        validate_links(path, text, errors)
        current_prefixes: Counter[str] = Counter()

        for line_number, line in enumerate(text.splitlines(), 1):
            vector_parent = VECTOR_PARENT_RE.match(line)
            if vector_parent and line.split("|")[-2].strip():
                add_error(errors, f"{path.relative_to(ROOT)}:{line_number}: canonical vector result is not blank")

            if not line.startswith("- ["):
                continue
            match = CASE_RE.fullmatch(line)
            if match is None:
                add_error(errors, f"{path.relative_to(ROOT)}:{line_number}: malformed active-case line")
                continue
            case_id = match.group("id")
            if match.group("state") != " ":
                add_error(errors, f"{path.relative_to(ROOT)}:{line_number}: canonical case {case_id} is checked")
            if RESULT_ANNOTATION_RE.search(match.group("description")):
                add_error(errors, f"{path.relative_to(ROOT)}:{line_number}: canonical case {case_id} contains a run result")
            if case_id in cases:
                prior_path, prior_line, *_ = cases[case_id]
                add_error(
                    errors,
                    f"{path.relative_to(ROOT)}:{line_number}: duplicate {case_id}; first defined at "
                    f"{prior_path.relative_to(ROOT)}:{prior_line}",
                )
            tags = match.group("tags").split("/")
            unknown_tags = sorted(set(tags) - KNOWN_TAGS)
            if unknown_tags:
                add_error(errors, f"{path.relative_to(ROOT)}:{line_number}: {case_id} has unknown tags {unknown_tags}")
            if "GAP" in tags and not re.search(r"\bgap\b", match.group("description"), re.IGNORECASE):
                add_error(errors, f"{path.relative_to(ROOT)}:{line_number}: {case_id} has GAP tag without an explicit gap explanation")
            auto_references = list(AUTO_REFERENCE_RE.finditer(match.group("description")))
            if "AUTO:" in match.group("description") and not auto_references:
                add_error(errors, f"{path.relative_to(ROOT)}:{line_number}: {case_id} has malformed AUTO reference")
            for auto_ref in auto_references:
                reference_path = Path(auto_ref.group("path"))
                resolved = (ROOT / reference_path).resolve()
                if reference_path.is_absolute() or ROOT not in resolved.parents or not resolved.is_file():
                    add_error(errors, f"{path.relative_to(ROOT)}:{line_number}: {case_id} has invalid AUTO path {reference_path}")
                    continue
                try:
                    automated_source = resolved.read_text(encoding="utf-8")
                except UnicodeDecodeError:
                    add_error(errors, f"{path.relative_to(ROOT)}:{line_number}: {case_id} AUTO path is not a text file")
                    continue
                if auto_ref.group("symbol") not in automated_source:
                    add_error(
                        errors,
                        f"{path.relative_to(ROOT)}:{line_number}: {case_id} AUTO symbol "
                        f"{auto_ref.group('symbol')} is absent from {reference_path}",
                    )

            prefix = case_id.split("-", 1)[0]
            cases[case_id] = (path, line_number, match.group("priority"), match.group("tags"), match.group("description"))
            priorities[match.group("priority")] += 1
            prefixes[prefix] += 1
            current_prefixes[prefix] += 1
        file_prefixes[path.relative_to(HERE).as_posix()] = current_prefixes

    active_ids = set(cases)
    gap_ids = {case_id for case_id, (_, _, _, tags, _) in cases.items() if "GAP" in tags.split("/")}
    open_gap_ids = {case_id for case_id, status in gap_ledger.items() if status == "Open"}
    if gap_ids != open_gap_ids:
        add_error(
            errors,
            f"open known-gaps.md IDs {sorted(open_gap_ids)} do not match active GAP cases {sorted(gap_ids)}",
        )
    overlap = active_ids & retired
    if overlap:
        add_error(errors, f"active IDs also appear in the retired ledger: {', '.join(sorted(overlap))}")

    summary = SUMMARY_RE.search(index_text)
    actual_summary = {"total": len(cases), "p0": priorities["P0"], "p1": priorities["P1"], "p2": priorities["P2"]}
    if summary is None:
        add_error(errors, "README.md: missing catalog priority summary")
    else:
        declared = {key: int(value) for key, value in summary.groupdict().items()}
        if declared != actual_summary:
            add_error(errors, f"README.md: stale priority summary {declared}; actual is {actual_summary}")

    assigned_prefixes: set[str] = set()
    group_count = 0
    referenced_files: set[str] = set()
    for line_number, line in enumerate(index_text.splitlines(), 1):
        match = GROUP_RE.fullmatch(line)
        if match is None:
            continue
        relative_path = match.group("path")
        referenced_files.add(relative_path)
        declared_prefixes = match.group("prefixes").split(", ")
        duplicates = assigned_prefixes & set(declared_prefixes)
        if duplicates:
            add_error(errors, f"README.md:{line_number}: prefixes appear in multiple groups: {sorted(duplicates)}")
        assigned_prefixes.update(declared_prefixes)
        expected_count = sum(prefixes[prefix] for prefix in declared_prefixes)
        group_count += expected_count
        if int(match.group("count")) != expected_count:
            add_error(errors, f"README.md:{line_number}: group count is {match.group('count')}; actual is {expected_count}")
        actual_file_prefixes = set(file_prefixes.get(relative_path, Counter()))
        if actual_file_prefixes != set(declared_prefixes):
            add_error(
                errors,
                f"README.md:{line_number}: {relative_path} contains prefixes {sorted(actual_file_prefixes)}; "
                f"dashboard declares {sorted(declared_prefixes)}",
            )

    expected_files = {path.relative_to(HERE).as_posix() for path in catalog_files}
    if referenced_files != expected_files:
        add_error(errors, f"README.md: catalog links {sorted(referenced_files)} do not match files {sorted(expected_files)}")
    if assigned_prefixes != set(prefixes):
        add_error(errors, f"README.md: dashboard prefixes {sorted(assigned_prefixes)} do not match active prefixes {sorted(prefixes)}")
    if set(prefixes) != ALLOWED_PREFIXES:
        add_error(errors, f"active prefixes {sorted(prefixes)} do not match the explicit registry {sorted(ALLOWED_PREFIXES)}")

    total_match = next((TOTAL_RE.fullmatch(line) for line in index_text.splitlines() if TOTAL_RE.fullmatch(line)), None)
    if total_match is None or int(total_match.group("count")) != len(cases) or group_count != len(cases):
        add_error(errors, f"README.md: total dashboard count does not equal {len(cases)} active cases")

    all_known_ids = active_ids | retired
    known_prefixes = set(prefixes) | {case_id.split("-", 1)[0] for case_id in retired}
    for path, text in catalog_texts.items():
        references = set(EXPLICIT_ID_REFERENCE_RE.findall(text))
        references.update(VECTOR_PARENT_RE.findall(text))
        references.update({
            reference
            for reference in ID_REFERENCE_RE.findall(text)
            if reference.split("-", 1)[0] in known_prefixes
        })
        unknown_case_tokens = {
            reference
            for reference in ID_REFERENCE_RE.findall(text)
            if reference not in NON_CASE_TOKENS and reference.split("-", 1)[0] not in ALLOWED_PREFIXES
        }
        if unknown_case_tokens:
            add_error(errors, f"{path.relative_to(ROOT)}: case-shaped tokens use unknown prefixes {sorted(unknown_case_tokens)}")
        unknown_refs = sorted(references - all_known_ids)
        if unknown_refs:
            add_error(errors, f"{path.relative_to(ROOT)}: references unknown case IDs {unknown_refs}")

    run_template = HERE / "run-template.md"
    validate_run_template(run_template, run_template.read_text(encoding="utf-8"), errors)
    for path in [INDEX, RETIRED, KNOWN_GAPS, HERE / "fixtures.md", run_template]:
        validate_links(path, path.read_text(encoding="utf-8"), errors)

    base_ref = args.base_ref or default_base_ref(errors)
    old_active, old_retired, old_gaps = previous_ids(base_ref, errors) if base_ref else (set(), set(), set())
    missing_without_retirement = old_active - active_ids - retired
    if missing_without_retirement:
        add_error(errors, f"active IDs removed without retirement: {', '.join(sorted(missing_without_retirement))}")
    removed_retired = old_retired - retired
    if removed_retired:
        add_error(errors, f"retired IDs removed from the permanent ledger: {', '.join(sorted(removed_retired))}")
    reused_retired = old_retired & active_ids
    if reused_retired:
        add_error(errors, f"retired IDs reused as active cases: {', '.join(sorted(reused_retired))}")
    removed_gap_history = old_gaps - set(gap_ledger)
    if removed_gap_history:
        add_error(errors, f"historical GAP rows removed from the permanent ledger: {', '.join(sorted(removed_gap_history))}")
    unknown_gap_cases = set(gap_ledger) - active_ids - retired
    if unknown_gap_cases:
        add_error(errors, f"GAP ledger references neither active nor retired cases: {', '.join(sorted(unknown_gap_cases))}")

    if old_active or old_retired:
        old_known = old_active | old_retired
        new_ids = active_ids - old_known
        old_numbers: dict[str, list[int]] = {}
        for case_id in old_known:
            prefix, number = case_id.split("-", 1)
            old_numbers.setdefault(prefix, []).append(int(number))
        new_numbers: dict[str, list[int]] = {}
        for case_id in new_ids:
            prefix, number = case_id.split("-", 1)
            new_numbers.setdefault(prefix, []).append(int(number))
        for prefix, numbers in new_numbers.items():
            prior = old_numbers.get(prefix, [])
            if not prior:
                expected = list(range(1, 1 + len(numbers)))
                previous = 0
            else:
                previous = max(prior)
                expected = list(range(previous + 1, previous + 1 + len(numbers)))
            if sorted(numbers) != expected:
                add_error(
                    errors,
                    f"new {prefix} IDs must append consecutively after {previous:03d}; "
                    f"expected {expected}, found {sorted(numbers)}",
                )

    if errors:
        print("\n".join(f"ERROR: {error}" for error in errors), file=sys.stderr)
        return 1

    priority_output = ", ".join(f"{priority}={priorities[priority]}" for priority in ("P0", "P1", "P2"))
    print(f"Validated {len(cases)} active cases across {len(prefixes)} prefixes ({priority_output}); {len(retired)} retired IDs reserved.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
