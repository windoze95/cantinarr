#!/usr/bin/env python3
"""Shared safety checks for the local Maestro web harness."""

from __future__ import annotations

import argparse
from pathlib import Path
import sys
from urllib.parse import urlsplit


EXPECTED_MAESTRO_CONFIG = """flows:
  - "flows/**/*.yaml"
executionOrder:
  continueOnFailure: false
testOutputDir: .artifacts
"""


class SafetyError(ValueError):
    """Raised when a Maestro runtime value crosses a safety boundary."""


def validate_loopback_http_url(value: str) -> None:
    """Require an unambiguous HTTP base URL for the local SSH tunnel."""

    if not value or any(ord(character) < 0x20 for character in value):
        raise SafetyError("URL must be a non-empty single-line value")
    if "?" in value or "#" in value:
        raise SafetyError("URL must not contain a query or fragment delimiter")

    try:
        parsed = urlsplit(value)
        port = parsed.port
    except ValueError as exc:
        raise SafetyError("URL has an invalid host or port") from exc

    if parsed.scheme != "http":
        raise SafetyError("URL scheme must be http")
    if parsed.username is not None or parsed.password is not None:
        raise SafetyError("URL must not contain user information")
    if parsed.hostname not in {"127.0.0.1", "localhost"}:
        raise SafetyError("URL host must be 127.0.0.1 or localhost")
    if port is None or not 1 <= port <= 65535:
        raise SafetyError("URL must contain an explicit valid port")
    if parsed.path or parsed.query or parsed.fragment:
        raise SafetyError("URL must be an origin without a path, query, or fragment")


def validate_maestro_config(path: Path) -> None:
    """Require the fixed local-only Maestro config, without hooks or scripts."""

    if path.is_symlink():
        raise SafetyError("Maestro config must not be a symlink")
    try:
        contents = path.read_text()
    except OSError as exc:
        raise SafetyError("Maestro config could not be read") from exc
    if contents != EXPECTED_MAESTRO_CONFIG:
        raise SafetyError("Maestro config differs from the reviewed local-only schema")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", required=True, type=Path)
    parser.add_argument("url", help="candidate Maestro server origin")
    args = parser.parse_args()
    try:
        validate_loopback_http_url(args.url)
    except SafetyError as exc:
        print(f"invalid Maestro loopback URL: {exc}", file=sys.stderr)
        return 1
    try:
        validate_maestro_config(args.config)
    except SafetyError as exc:
        print(f"invalid Maestro config: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
