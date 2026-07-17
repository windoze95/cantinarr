#!/usr/bin/env python3
"""Redact the selected lab password from Maestro output and artifacts."""

from __future__ import annotations

import argparse
import os
from pathlib import Path
import stat
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
    if root.is_symlink():
        raise RuntimeError("artifact root must be a real directory")
    if not root.exists():
        return 0
    if not root.is_dir():
        raise RuntimeError("artifact root must be a real directory")

    replacements = [(secret.encode(), REDACTION.encode()) for secret in secrets]
    for path in sorted(root.rglob("*")):
        if path.is_symlink():
            raise RuntimeError("artifact tree contains a symlink")
        relative = path.relative_to(root).as_posix()
        if any(secret in relative for secret in secrets):
            raise RuntimeError("artifact filename contains a secret")
        metadata = path.lstat()
        if stat.S_ISDIR(metadata.st_mode):
            continue
        if not stat.S_ISREG(metadata.st_mode):
            raise RuntimeError("artifact tree contains an unsupported filesystem node")
        if metadata.st_nlink != 1:
            raise RuntimeError("artifact tree contains a hardlinked file")
        data = path.read_bytes()
        sanitized = data
        for secret, replacement in replacements:
            sanitized = sanitized.replace(secret, replacement)
        if sanitized != data:
            path.write_bytes(sanitized)
        for secret, _ in replacements:
            if secret in path.read_bytes():
                raise RuntimeError("secret redaction failed")
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
    except OSError:
        print("maestro redaction failed: artifact I/O error", file=sys.stderr)
        raise SystemExit(1)
    except RuntimeError as exc:
        message = redact_text(str(exc), secrets_from_environment())
        print(f"maestro redaction failed: {message}", file=sys.stderr)
        raise SystemExit(1)
