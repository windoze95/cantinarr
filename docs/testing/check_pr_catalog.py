#!/usr/bin/env python3
"""Require every pull request to make one unambiguous catalog declaration."""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys

from check_catalog import (
    CASE_RE,
    CATALOG_DIR,
    GAP_RE,
    KNOWN_GAPS,
    RETIRED,
    RETIRED_RE,
    ROOT,
    VECTOR_PARENT_RE,
    default_base_ref,
    git_text,
)


DECLARATION_RE = re.compile(r"^Affected case IDs:\s*(?P<value>.+?)\s*$", re.MULTILINE)
CASE_ID_RE = re.compile(r"^[A-Z]+-\d{3}$")
RANGE_RE = re.compile(r"^(?P<start>[A-Z]+-\d{3})\.\.(?P<end>[A-Z]+-\d{3})$")
NO_CHANGE_RE = re.compile(r"^no change\s+[—-]\s+(?P<reason>.+)$", re.IGNORECASE)
OPTIONS = (
    "Updated, added, or retired affected cases, counts, and automation references",
    "Existing catalog cases already describe the affected behavior and remain accurate",
    "No catalog impact — explained in **What**",
)


def catalog_ids() -> tuple[set[str], set[str]]:
    active: set[str] = set()
    for path in CATALOG_DIR.glob("*.md"):
        for line in path.read_text(encoding="utf-8").splitlines():
            match = CASE_RE.fullmatch(line)
            if match:
                active.add(match.group("id"))
    retired = {
        match.group("id")
        for match in map(RETIRED_RE.fullmatch, RETIRED.read_text(encoding="utf-8").splitlines())
        if match
    }
    return active, retired


def case_records(contents: list[str]) -> dict[str, str]:
    definitions: dict[str, str] = {}
    vectors: dict[str, list[str]] = {}
    for content in contents:
        for line in content.splitlines():
            match = CASE_RE.fullmatch(line)
            if match:
                definitions[match.group("id")] = line
            vector = VECTOR_PARENT_RE.match(line)
            if vector:
                vectors.setdefault(vector.group("id"), []).append(line)
    return {
        f"case:{case_id}": definition + "\n" + "\n".join(vectors.get(case_id, []))
        for case_id, definition in definitions.items()
    }


def current_catalog_records() -> dict[str, str]:
    records = case_records([path.read_text(encoding="utf-8") for path in CATALOG_DIR.glob("*.md")])
    for path, pattern, kind in ((RETIRED, RETIRED_RE, "retired"), (KNOWN_GAPS, GAP_RE, "gap")):
        for line in path.read_text(encoding="utf-8").splitlines():
            match = pattern.fullmatch(line)
            if match:
                records[f"{kind}:{match.group('id')}"] = line
    return records


def base_catalog_records(ref: str, errors: list[str]) -> dict[str, str]:
    verify = subprocess.run(
        ["git", "rev-parse", "--verify", f"{ref}^{{commit}}"],
        cwd=ROOT,
        check=False,
        capture_output=True,
        text=True,
    )
    if verify.returncode != 0:
        errors.append(f"cannot resolve PR catalog base ref {ref!r}")
        return {}
    listing = subprocess.run(
        ["git", "ls-tree", "-r", "--name-only", ref, "--", "docs/testing/catalog"],
        cwd=ROOT,
        check=False,
        capture_output=True,
        text=True,
    )
    if listing.returncode != 0:
        errors.append(f"cannot read PR catalog base ref {ref!r}")
        return {}

    catalog_contents: list[str] = []
    paths = listing.stdout.splitlines()
    for path in paths:
        content = git_text(ref, path)
        if content is None:
            errors.append(f"cannot read historical catalog file {path!r} from {ref!r}")
            continue
        catalog_contents.append(content)
    records = case_records(catalog_contents)
    for path, pattern, kind in (
        ("docs/testing/retired-cases.md", RETIRED_RE, "retired"),
        ("docs/testing/known-gaps.md", GAP_RE, "gap"),
    ):
        content = git_text(ref, path)
        if content is None:
            if paths:
                errors.append(f"cannot read historical {kind} ledger from {ref!r}")
            continue
        for line in content.splitlines():
            match = pattern.fullmatch(line)
            if match:
                records[f"{kind}:{match.group('id')}"] = line
    return records


def changed_catalog_ids(base_ref: str, errors: list[str]) -> set[str]:
    base = base_catalog_records(base_ref, errors)
    current = current_catalog_records()
    changed_records = {
        key
        for key in set(base) | set(current)
        if base.get(key) != current.get(key)
    }
    return {key.split(":", 1)[1] for key in changed_records}


def expand_ids(value: str, errors: list[str]) -> set[str]:
    case_ids: set[str] = set()
    for token in (part.strip() for part in value.split(",")):
        if not token:
            errors.append("affected case list contains an empty entry")
            continue
        if CASE_ID_RE.fullmatch(token):
            case_ids.add(token)
            continue
        range_match = RANGE_RE.fullmatch(token)
        if range_match is None:
            errors.append(f"invalid case ID or range {token!r}; use PREFIX-001 or PREFIX-001..PREFIX-009")
            continue
        start_prefix, start_number = range_match.group("start").split("-", 1)
        end_prefix, end_number = range_match.group("end").split("-", 1)
        if start_prefix != end_prefix or int(start_number) > int(end_number):
            errors.append(f"invalid case range {token!r}")
            continue
        case_ids.update(
            f"{start_prefix}-{number:03d}"
            for number in range(int(start_number), int(end_number) + 1)
        )
    return case_ids


def validate(body: str, changed_ids: set[str] | None = None) -> list[str]:
    errors: list[str] = []
    declarations = list(DECLARATION_RE.finditer(body))
    if len(declarations) != 1:
        return ["PR body must contain exactly one nonblank 'Affected case IDs:' declaration"]
    declaration = declarations[0].group("value").strip()

    option_states: list[bool] = []
    for option in OPTIONS:
        matches = list(re.finditer(rf"^- \[(?P<state>[ xX])\] {re.escape(option)}$", body, re.MULTILINE))
        if len(matches) != 1:
            errors.append(f"PR body must contain exactly one catalog option line: {option}")
            option_states.append(False)
        else:
            option_states.append(matches[0].group("state").lower() == "x")
    if sum(option_states) != 1:
        errors.append("select exactly one regression-catalog option")

    no_change = NO_CHANGE_RE.fullmatch(declaration)
    if no_change:
        reason = no_change.group("reason").strip()
        if len(reason) < 10 or "specific reason" in reason.lower() or reason.startswith("<"):
            errors.append("'no change' requires a substantive, specific reason")
        if len(option_states) == 3 and not option_states[2]:
            errors.append("a 'no change' declaration must select the no-catalog-impact option")
        if changed_ids:
            errors.append(
                "catalog definitions changed, so the PR must select the updated option and list every changed ID"
            )
        return errors

    if len(option_states) == 3 and option_states[2]:
        errors.append("the no-catalog-impact option requires 'no change — <specific reason>'")

    affected = expand_ids(declaration, errors)
    if not affected:
        errors.append("list at least one affected case ID")
        return errors

    active, retired = catalog_ids()
    allowed = active | retired if option_states and option_states[0] else active
    unknown = affected - allowed
    if unknown:
        errors.append(f"affected declaration references unknown or inapplicable IDs: {', '.join(sorted(unknown))}")
    if changed_ids is not None:
        if changed_ids and not option_states[0]:
            errors.append("catalog definitions changed, so select the updated/added/retired option")
        if not changed_ids and option_states[0]:
            errors.append("the updated/added/retired option is selected, but no catalog definitions changed")
        if option_states[0] and not changed_ids.issubset(affected):
            missing = sorted(changed_ids - affected)
            if missing:
                errors.append(f"affected declaration omits changed IDs: {', '.join(missing)}")
    return errors


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--body-file", type=Path, help="Read a PR body from this file instead of GITHUB_EVENT_PATH")
    parser.add_argument("--base-ref", help="Git ref used to compare catalog definitions")
    args = parser.parse_args()

    base_ref = args.base_ref
    if args.body_file:
        body = args.body_file.read_text(encoding="utf-8")
    else:
        event_path = os.environ.get("GITHUB_EVENT_PATH")
        if not event_path:
            print("ERROR: GITHUB_EVENT_PATH is unset; pass --body-file for a local check", file=sys.stderr)
            return 1
        event = json.loads(Path(event_path).read_text(encoding="utf-8"))
        body = event.get("pull_request", {}).get("body") or ""
        base_ref = base_ref or event.get("pull_request", {}).get("base", {}).get("sha")

    history_errors: list[str] = []
    base_ref = base_ref or default_base_ref(history_errors)
    changed_ids = changed_catalog_ids(base_ref, history_errors) if base_ref else set()
    if history_errors:
        print("\n".join(f"ERROR: {error}" for error in history_errors), file=sys.stderr)
        return 1

    errors = validate(body, changed_ids)
    if errors:
        print("\n".join(f"ERROR: {error}" for error in errors), file=sys.stderr)
        return 1
    print("Validated pull-request regression catalog declaration.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
