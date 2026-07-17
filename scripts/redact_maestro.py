#!/usr/bin/env python3
"""Redact the selected lab password from Maestro output and artifacts."""

from __future__ import annotations

import argparse
import os
from pathlib import Path
import sys


REDACTION = "[REDACTED LAB PASSWORD]"


def secrets_from_environment() -> list[str]:
    values = [os.environ.get("MAESTRO_PASSWORD", "")]
    return sorted({value for value in values if value}, key=len, reverse=True)


def redact_text(value: str, secrets: list[str]) -> str:
    for secret in secrets:
        value = value.replace(secret, REDACTION)
    return value


def stream(secrets: list[str]) -> int:
    for line in sys.stdin:
        sys.stdout.write(redact_text(line, secrets))
        sys.stdout.flush()
    return 0


def sanitize_tree(root: Path, secrets: list[str]) -> int:
    if not root.exists():
        return 0

    replacements = [(secret.encode(), REDACTION.encode()) for secret in secrets]
    for path in sorted(root.rglob("*")):
        if path.is_symlink():
            raise RuntimeError(f"artifact tree contains a symlink: {path}")
        if not path.is_file():
            continue
        relative = path.relative_to(root).as_posix()
        if any(secret in relative for secret in secrets):
            raise RuntimeError(f"artifact filename contains a secret: {path}")
        data = path.read_bytes()
        sanitized = data
        for secret, replacement in replacements:
            sanitized = sanitized.replace(secret, replacement)
        if sanitized != data:
            path.write_bytes(sanitized)
        for secret, _ in replacements:
            if secret in path.read_bytes():
                raise RuntimeError(f"secret redaction failed for {path}")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest="command", required=True)
    subparsers.add_parser("stream")
    tree_parser = subparsers.add_parser("tree")
    tree_parser.add_argument("root", type=Path)
    args = parser.parse_args()

    secrets = secrets_from_environment()
    if not secrets:
        print("MAESTRO_PASSWORD is required for redaction", file=sys.stderr)
        return 2
    if args.command == "stream":
        return stream(secrets)
    return sanitize_tree(args.root, secrets)


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, RuntimeError) as exc:
        print(f"maestro redaction failed: {exc}", file=sys.stderr)
        raise SystemExit(1)
