#!/usr/bin/env python3
"""Render a private Markdown evidence candidate for one Maestro lab suite run."""

from __future__ import annotations

import argparse
import hashlib
import json
import math
import os
import shutil
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
import re
import struct
import sys
import xml.etree.ElementTree as ET
import zlib


ROOT = Path(__file__).resolve().parents[1]
PNG_SIGNATURE = b"\x89PNG\r\n\x1a\n"
EVIDENCE_RE = re.compile(r"^- takeScreenshot: (evidence-[a-z0-9]+(?:-[a-z0-9]+)*)$")
MAX_XML_BYTES = 1_000_000
MAX_PNG_BYTES = 20_000_000
MAX_PIXELS = 16_777_216
HARNESS_SCRIPT_NAMES = (
    "check_test_automation.py",
    "maestro_safety.py",
    "redact_maestro.py",
    "render_maestro_report.py",
    "run-maestro-flow.sh",
    "run-maestro-lab.sh",
)


class ReportError(Exception):
    """Raised when report inputs cross a containment or trust boundary."""


@dataclass
class FlowResult:
    flow: str
    name: str
    role: str
    status: str
    duration: float | None
    tags: list[str]
    evidence_name: str
    evidence_path: str | None
    evidence_sha256: str | None
    evidence_width: int | None
    evidence_height: int | None
    junit_path: str | None
    proofs: list[dict[str, str]]
    report_error: str | None = None


def load_json(path: Path) -> object:
    try:
        return json.loads(path.read_text())
    except (OSError, json.JSONDecodeError) as exc:
        raise ReportError(f"cannot read valid JSON from {path}: {exc}") from exc


def assert_under(path: Path, root: Path, *, must_exist: bool = True) -> Path:
    try:
        resolved_root = root.resolve(strict=True)
        resolved = path.resolve(strict=must_exist)
    except OSError as exc:
        raise ReportError(f"cannot resolve report path {path}: {exc}") from exc
    if resolved == resolved_root or resolved_root not in resolved.parents:
        raise ReportError(f"report path escapes its allowed root: {path}")
    current = path
    while True:
        if current.is_symlink():
            raise ReportError(f"report path contains a symlink: {current}")
        if current == root:
            break
        if current == current.parent:
            raise ReportError(f"report path does not reach its allowed root: {path}")
        current = current.parent
    return resolved


def assert_real_path_chain(path: Path, anchor: Path) -> None:
    try:
        relative = path.relative_to(anchor)
    except ValueError as exc:
        raise ReportError(f"path is outside its trusted anchor: {path}") from exc
    current = anchor
    if current.is_symlink():
        raise ReportError(f"trusted path contains a symlink: {current}")
    for part in relative.parts:
        current /= part
        if current.is_symlink():
            raise ReportError(f"trusted path contains a symlink: {current}")


def harness_content_sha256(root: Path) -> str:
    candidates = {
        root / "Makefile",
        root / "docs" / "testing" / "automation.json",
        root / "e2e" / "maestro" / "config.yaml",
        root / "e2e" / "maestro" / "suites.json",
        *(root / "scripts" / name for name in HARNESS_SCRIPT_NAMES),
    }
    maestro_root = root / "e2e" / "maestro"
    for directory in (maestro_root / "flows", maestro_root / "helpers"):
        if directory.is_dir() and not directory.is_symlink():
            candidates.update(directory.rglob("*.yaml"))
            candidates.update(directory.rglob("*.yml"))
    digest = hashlib.sha256()
    included = 0
    for path in sorted(candidates):
        if not path.exists():
            continue
        if path.is_symlink() or not path.is_file():
            raise ReportError("harness inputs must be regular files")
        try:
            relative = path.relative_to(root).as_posix().encode()
        except ValueError as exc:
            raise ReportError("harness input escaped the source root") from exc
        data = path.read_bytes()
        digest.update(len(relative).to_bytes(8, "big"))
        digest.update(relative)
        digest.update(len(data).to_bytes(8, "big"))
        digest.update(data)
        included += 1
    if included == 0:
        raise ReportError("no harness inputs were found")
    return digest.hexdigest()


def flow_slug(flow: str) -> str:
    prefix = "e2e/maestro/flows/"
    if not flow.startswith(prefix) or not flow.endswith(".yaml"):
        raise ReportError(f"invalid Maestro flow path: {flow}")
    relative = flow.removeprefix(prefix).removesuffix(".yaml")
    if not relative or any(part in {"", ".", ".."} for part in relative.split("/")):
        raise ReportError(f"invalid Maestro flow path: {flow}")
    slug = relative.replace("/", "-")
    if not re.fullmatch(r"[a-z0-9]+(?:-[a-z0-9]+)*", slug):
        raise ReportError(f"invalid Maestro flow slug: {flow}")
    return slug


def flow_metadata(root: Path, flow: str) -> tuple[str, list[str], str]:
    path = assert_under(root / flow, root / "e2e" / "maestro" / "flows")
    if not path.is_file():
        raise ReportError(f"flow is not a regular file: {flow}")
    lines = path.read_text().splitlines()
    try:
        separator = lines.index("---")
    except ValueError as exc:
        raise ReportError(f"flow lacks a header separator: {flow}") from exc
    name_lines = [line.removeprefix("name: ") for line in lines[:separator] if line.startswith("name: ")]
    tags = [line.removeprefix("  - ") for line in lines[:separator] if line.startswith("  - ")]
    evidence = [
        match.group(1)
        for line in lines[separator + 1 :]
        if (match := EVIDENCE_RE.fullmatch(line))
    ]
    if len(name_lines) != 1 or not name_lines[0].strip():
        raise ReportError(f"flow has an invalid name: {flow}")
    if len(evidence) != 1:
        raise ReportError(f"flow must have exactly one reviewed evidence screenshot: {flow}")
    return name_lines[0], tags, evidence[0]


def role_label(username: str) -> str:
    labels = {
        "lab-admin": "Primary administrator",
        "lab-admin-b": "Administrator",
        "lab-requester": "Requester",
        "lab-restricted": "Restricted requester",
        "lab-no-grants": "No-grants requester",
    }
    try:
        return labels[username]
    except KeyError as exc:
        raise ReportError(f"suite uses an unknown lab role: {username}") from exc


def proofs_by_flow(automation_path: Path) -> dict[str, list[dict[str, str]]]:
    data = load_json(automation_path)
    if not isinstance(data, dict) or data.get("schema_version") != 1:
        raise ReportError("automation manifest has an unsupported schema")
    mapped: dict[str, list[dict[str, str]]] = {}
    for proof in data.get("proofs", []):
        if not isinstance(proof, dict) or set(proof) != {
            "case_id",
            "status",
            "runner",
            "specs",
            "scope",
        }:
            raise ReportError("automation manifest contains an invalid proof")
        values = {
            "case_id": str(proof.get("case_id", "")),
            "status": str(proof.get("status", "")),
            "scope": str(proof.get("scope", "")),
        }
        specs = proof.get("specs")
        if (
            not re.fullmatch(r"[A-Z]+-[0-9]{3}", values["case_id"])
            or values["status"] not in {"automated", "partial"}
            or not (20 <= len(values["scope"].strip()) <= 1_000)
            or not isinstance(specs, list)
            or not specs
            or any(not isinstance(spec, str) for spec in specs)
        ):
            raise ReportError("automation manifest contains unsafe proof metadata")
        for spec in specs:
            mapped.setdefault(str(spec), []).append(values)
    for proofs in mapped.values():
        proofs.sort(key=lambda item: item["case_id"])
    return mapped


def parse_junit(path: Path) -> tuple[str, float | None]:
    if path.is_symlink() or not path.is_file():
        raise ReportError(f"JUnit input is not a regular file: {path}")
    data = path.read_bytes()
    if len(data) > MAX_XML_BYTES:
        raise ReportError(f"JUnit input is too large: {path}")
    try:
        text = data.decode("utf-8-sig")
    except UnicodeDecodeError as exc:
        raise ReportError(f"JUnit input is not UTF-8: {path}") from exc
    if "<!DOCTYPE" in text.upper() or "<!ENTITY" in text.upper():
        raise ReportError(f"JUnit input contains a forbidden declaration: {path}")
    try:
        root = ET.fromstring(text)
    except ET.ParseError as exc:
        raise ReportError(f"JUnit input is malformed: {path}") from exc
    suites = root.findall("./testsuite") if root.tag == "testsuites" else []
    if len(suites) != 1:
        raise ReportError(f"JUnit input must contain exactly one testsuite: {path}")
    suite = suites[0]
    cases = suite.findall("./testcase")
    if len(cases) != 1:
        raise ReportError(f"JUnit input must contain exactly one testcase: {path}")
    if "tests" in suite.attrib and suite.attrib["tests"] != "1":
        raise ReportError(f"JUnit input has a contradictory test count: {path}")
    suite_failures = 0
    for attribute in ("failures", "errors", "skipped"):
        raw_count = suite.attrib.get(attribute, "0")
        if not re.fullmatch(r"[0-9]+", raw_count):
            raise ReportError(f"JUnit input has an invalid {attribute} count: {path}")
        suite_failures += int(raw_count)
    case = cases[0]
    raw_status = case.attrib.get("status", "").upper()
    failed = (
        suite_failures > 0
        or case.find(".//failure") is not None
        or case.find(".//error") is not None
        or case.find(".//skipped") is not None
    )
    status = "PASS" if raw_status == "SUCCESS" and not failed else "FAIL"
    try:
        duration = float(case.attrib["time"]) if "time" in case.attrib else None
    except ValueError as exc:
        raise ReportError(f"JUnit input has an invalid duration: {path}") from exc
    if duration is not None and (not math.isfinite(duration) or duration < 0 or duration > 86_400):
        raise ReportError(f"JUnit input duration is out of range: {path}")
    return status, duration


def normalized_junit(name: str, flow: str, status: str, duration: float | None, tags: list[str]) -> bytes:
    testsuites = ET.Element("testsuites")
    failed = status != "PASS"
    suite = ET.SubElement(
        testsuites,
        "testsuite",
        {
            "name": "Cantinarr Maestro lab",
            "tests": "1",
            "failures": "1" if failed else "0",
            "time": f"{duration:.1f}" if duration is not None else "0.0",
        },
    )
    case = ET.SubElement(
        suite,
        "testcase",
        {
            "id": name,
            "name": name,
            "classname": name,
            "file": flow,
            "time": f"{duration:.1f}" if duration is not None else "0.0",
            "status": "SUCCESS" if not failed else "ERROR",
        },
    )
    properties = ET.SubElement(case, "properties")
    ET.SubElement(properties, "property", {"name": "tags", "value": ", ".join(tags)})
    if failed:
        failure = ET.SubElement(case, "failure", {"message": "Maestro flow failed"})
        failure.text = "See the private sibling raw artifacts for diagnostics."
    ET.indent(testsuites, space="  ")
    return ET.tostring(testsuites, encoding="utf-8", xml_declaration=True) + b"\n"


def png_chunk(kind: bytes, data: bytes) -> bytes:
    return (
        struct.pack(">I", len(data))
        + kind
        + data
        + struct.pack(">I", zlib.crc32(kind + data) & 0xFFFFFFFF)
    )


def paeth(left: int, above: int, upper_left: int) -> int:
    estimate = left + above - upper_left
    left_distance = abs(estimate - left)
    above_distance = abs(estimate - above)
    upper_left_distance = abs(estimate - upper_left)
    if left_distance <= above_distance and left_distance <= upper_left_distance:
        return left
    if above_distance <= upper_left_distance:
        return above
    return upper_left


def decode_scanlines(
    compressed: bytes,
    *,
    width: int,
    height: int,
    channels: int,
) -> list[bytes]:
    row_bytes = width * channels
    expected = height * (row_bytes + 1)
    decoder = zlib.decompressobj()
    decoded = bytearray()
    remaining = compressed
    try:
        while remaining:
            produced = decoder.decompress(remaining, expected + 1 - len(decoded))
            decoded.extend(produced)
            if len(decoded) > expected:
                raise ReportError("evidence PNG expands beyond its declared dimensions")
            remaining = decoder.unconsumed_tail
            if not remaining:
                break
    except zlib.error as exc:
        raise ReportError("evidence PNG contains invalid compressed pixels") from exc
    if not decoder.eof or decoder.unused_data or remaining or len(decoded) != expected:
        raise ReportError("evidence PNG pixel data does not match its declared dimensions")

    rows: list[bytes] = []
    previous = bytes(row_bytes)
    offset = 0
    for _ in range(height):
        filter_type = decoded[offset]
        filtered = decoded[offset + 1 : offset + 1 + row_bytes]
        offset += row_bytes + 1
        if filter_type not in {0, 1, 2, 3, 4}:
            raise ReportError("evidence PNG uses an unsupported row filter")
        reconstructed = bytearray(row_bytes)
        for index, value in enumerate(filtered):
            left = reconstructed[index - channels] if index >= channels else 0
            above = previous[index]
            upper_left = previous[index - channels] if index >= channels else 0
            if filter_type == 0:
                predictor = 0
            elif filter_type == 1:
                predictor = left
            elif filter_type == 2:
                predictor = above
            elif filter_type == 3:
                predictor = (left + above) // 2
            else:
                predictor = paeth(left, above, upper_left)
            reconstructed[index] = (value + predictor) & 0xFF
        row = bytes(reconstructed)
        rows.append(row)
        previous = row
    return rows


def canonical_rgba_png(raw: bytes) -> tuple[bytes, int, int]:
    if len(raw) > MAX_PNG_BYTES or not raw.startswith(PNG_SIGNATURE):
        raise ReportError("evidence image is not an allowed PNG")
    offset = len(PNG_SIGNATURE)
    width = height = color_type = None
    channels = None
    palette: list[tuple[int, int, int]] | None = None
    transparency: bytes | None = None
    idat_parts: list[bytes] = []
    saw_ihdr = saw_idat = saw_iend = False
    ended_idat = False
    chunk_index = 0
    while offset < len(raw):
        if offset + 12 > len(raw):
            raise ReportError("evidence PNG has a truncated chunk")
        length = struct.unpack(">I", raw[offset : offset + 4])[0]
        end = offset + 12 + length
        if end > len(raw):
            raise ReportError("evidence PNG has an invalid chunk length")
        kind = raw[offset + 4 : offset + 8]
        data = raw[offset + 8 : offset + 8 + length]
        expected_crc = struct.unpack(">I", raw[offset + 8 + length : end])[0]
        if not re.fullmatch(rb"[A-Za-z]{4}", kind) or kind[2:3].islower():
            raise ReportError("evidence PNG has an invalid chunk type")
        if zlib.crc32(kind + data) & 0xFFFFFFFF != expected_crc:
            raise ReportError("evidence PNG has an invalid checksum")
        if chunk_index == 0 and kind != b"IHDR":
            raise ReportError("evidence PNG header is not first")
        if kind == b"IHDR":
            if saw_ihdr or chunk_index != 0 or length != 13:
                raise ReportError("evidence PNG has an invalid header")
            width, height, bit_depth, color_type, compression, filtering, interlace = struct.unpack(
                ">IIBBBBB", data
            )
            if (
                not (1 <= width <= 4096 and 1 <= height <= 4096)
                or width * height > MAX_PIXELS
                or bit_depth != 8
                or color_type not in {0, 2, 3, 4, 6}
                or compression != 0
                or filtering != 0
                or interlace != 0
            ):
                raise ReportError("evidence PNG header uses an unsupported format")
            channels = {0: 1, 2: 3, 3: 1, 4: 2, 6: 4}[color_type]
            saw_ihdr = True
        elif kind == b"PLTE":
            if not saw_ihdr or saw_idat or palette is not None or color_type in {0, 4}:
                raise ReportError("evidence PNG has an invalid palette")
            if length == 0 or length > 768 or length % 3:
                raise ReportError("evidence PNG has an invalid palette")
            palette = [tuple(data[index : index + 3]) for index in range(0, length, 3)]
        elif kind == b"tRNS":
            if not saw_ihdr or saw_idat or transparency is not None:
                raise ReportError("evidence PNG has invalid transparency data")
            valid_length = (
                (color_type == 0 and length == 2)
                or (color_type == 2 and length == 6)
                or (
                    color_type == 3
                    and palette is not None
                    and 1 <= length <= len(palette)
                )
            )
            if not valid_length:
                raise ReportError("evidence PNG has invalid transparency data")
            transparency = data
        elif kind == b"IDAT":
            if not saw_ihdr or ended_idat or (color_type == 3 and palette is None):
                raise ReportError("evidence PNG has invalid pixel chunk ordering")
            saw_idat = True
            idat_parts.append(data)
        elif kind == b"IEND":
            if not saw_idat or length != 0:
                raise ReportError("evidence PNG has an invalid end marker")
            saw_iend = True
            offset = end
            if offset != len(raw):
                raise ReportError("evidence PNG contains trailing data")
            break
        elif kind[:1].isupper():
            raise ReportError("evidence PNG contains an unsupported critical chunk")
        elif saw_idat:
            ended_idat = True
        offset = end
        chunk_index += 1
    if not saw_ihdr or not saw_idat or not saw_iend:
        raise ReportError("evidence PNG is incomplete")
    assert width is not None and height is not None and color_type is not None and channels is not None
    rows = decode_scanlines(
        b"".join(idat_parts), width=width, height=height, channels=channels
    )
    rgba_rows: list[bytes] = []
    transparent_gray = struct.unpack(">H", transparency)[0] if color_type == 0 and transparency else None
    transparent_rgb = (
        struct.unpack(">HHH", transparency) if color_type == 2 and transparency else None
    )
    palette_alpha = list(transparency or b"") if color_type == 3 else []
    for row in rows:
        rgba = bytearray()
        for index in range(0, len(row), channels):
            pixel = row[index : index + channels]
            if color_type == 0:
                gray = pixel[0]
                if gray == transparent_gray:
                    rgba.extend((0, 0, 0, 0))
                else:
                    rgba.extend((gray, gray, gray, 255))
            elif color_type == 2:
                red, green, blue = pixel
                if (red, green, blue) == transparent_rgb:
                    rgba.extend((0, 0, 0, 0))
                else:
                    rgba.extend((red, green, blue, 255))
            elif color_type == 3:
                palette_index = pixel[0]
                if palette is None or palette_index >= len(palette):
                    raise ReportError("evidence PNG references an invalid palette entry")
                alpha = (
                    palette_alpha[palette_index]
                    if palette_index < len(palette_alpha)
                    else 255
                )
                rgba.extend((*palette[palette_index], alpha) if alpha else (0, 0, 0, 0))
            elif color_type == 4:
                gray, alpha = pixel
                rgba.extend((gray, gray, gray, alpha) if alpha else (0, 0, 0, 0))
            else:
                red, green, blue, alpha = pixel
                rgba.extend((red, green, blue, alpha) if alpha else (0, 0, 0, 0))
        rgba_rows.append(bytes(rgba))
    scanlines = b"".join(b"\x00" + row for row in rgba_rows)
    header = struct.pack(">IIBBBBB", width, height, 8, 6, 0, 0, 0)
    canonical = (
        PNG_SIGNATURE
        + png_chunk(b"IHDR", header)
        + png_chunk(b"IDAT", zlib.compress(scanlines, level=9))
        + png_chunk(b"IEND", b"")
    )
    return canonical, width, height


def sanitize_png(source: Path, destination: Path) -> tuple[str, int, int]:
    if source.is_symlink() or not source.is_file():
        raise ReportError(f"evidence image is not a regular file: {source}")
    raw = source.read_bytes()
    output, width, height = canonical_rgba_png(raw)
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.write_bytes(output)
    destination.chmod(0o600)
    return hashlib.sha256(output).hexdigest(), width, height


def markdown_escape(value: str) -> str:
    collapsed = " ".join(value.split())
    return re.sub(r"([\\`*_\[\]<>|])", r"\\\1", collapsed)


def report_time(value: str | None) -> str:
    if value is None:
        return datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")
    if not re.fullmatch(r"[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z", value):
        raise ReportError("generated-at must be a UTC timestamp ending in Z")
    return value


def _render_report(
    *,
    root: Path,
    suite_name: str,
    run_dir: Path,
    harness_revision: str,
    harness_dirty: bool,
    harness_sha256: str,
    deployed_this_run: bool,
    reset_requested: bool,
    maestro_version: str,
    platform: str,
    staging_created: list[bool],
    generated_at: str | None = None,
) -> tuple[Path, bool]:
    if not re.fullmatch(r"[a-z0-9]+(?:-[a-z0-9]+)*", suite_name):
        raise ReportError("suite name contains unsupported characters")
    allowed_runs = root / "e2e" / "maestro" / ".artifacts" / "suites"
    suite_runs = allowed_runs / suite_name
    assert_real_path_chain(suite_runs, root)
    resolved_run = assert_under(run_dir, suite_runs)
    if not resolved_run.is_dir():
        raise ReportError("suite run path is not a directory")
    if resolved_run.parent != suite_runs.resolve(strict=True):
        raise ReportError("suite run must be an immediate child of its selected suite directory")
    final_report_dir = resolved_run / "report"
    if final_report_dir.exists() or final_report_dir.is_symlink():
        raise ReportError("suite report directory already exists")
    report_dir = resolved_run / f".report-staging-{os.getpid()}"
    if report_dir.exists() or report_dir.is_symlink():
        raise ReportError("suite report staging directory already exists")
    report_dir.mkdir(mode=0o700)
    staging_created[0] = True
    (report_dir / "screenshots").mkdir(mode=0o700)
    (report_dir / "junit").mkdir(mode=0o700)

    current_harness_sha256 = harness_content_sha256(root)
    if not re.fullmatch(r"[a-f0-9]{64}", harness_sha256):
        raise ReportError("harness content hash is invalid")
    if current_harness_sha256 != harness_sha256:
        raise ReportError("harness inputs changed while the suite was running")

    suites_data = load_json(root / "e2e" / "maestro" / "suites.json")
    if not isinstance(suites_data, dict) or suites_data.get("schema_version") != 1:
        raise ReportError("suite map has an unsupported schema")
    suite = suites_data.get("suites", {}).get(suite_name)
    if not isinstance(suite, list) or not suite:
        raise ReportError(f"unknown or empty Maestro suite: {suite_name}")
    mapped_proofs = proofs_by_flow(root / "docs" / "testing" / "automation.json")

    results: list[FlowResult] = []
    report_valid = True
    seen_slugs: set[str] = set()
    seen_evidence: set[str] = set()
    seen_names: set[str] = set()
    for item in suite:
        if not isinstance(item, dict) or set(item) != {"flow", "user"}:
            raise ReportError("suite entry has an invalid schema")
        flow = str(item["flow"])
        slug = flow_slug(flow)
        name, tags, evidence_name = flow_metadata(root, flow)
        if slug in seen_slugs:
            raise ReportError(f"suite contains a colliding flow slug: {slug}")
        if evidence_name in seen_evidence:
            raise ReportError(f"suite contains a duplicate evidence name: {evidence_name}")
        if name in seen_names:
            raise ReportError(f"suite contains a duplicate flow name: {name}")
        seen_slugs.add(slug)
        seen_evidence.add(evidence_name)
        seen_names.add(name)
        status_file = resolved_run / "statuses" / f"{slug}.exit"
        raw_dir = resolved_run / "raw" / slug
        status = "NOT RUN"
        duration = None
        report_error = None
        evidence_path = evidence_sha256 = junit_path = None
        evidence_width = evidence_height = None
        junit_parsed = False

        if status_file.exists() or status_file.is_symlink():
            assert_under(status_file, resolved_run)
            if status_file.is_symlink() or not status_file.is_file():
                raise ReportError(f"flow status is not a regular file: {status_file}")
            if status_file.stat().st_nlink != 1:
                raise ReportError(f"flow status is hardlinked: {status_file}")
            raw_exit = status_file.read_text().strip()
            if not re.fullmatch(r"[0-9]{1,3}", raw_exit):
                raise ReportError(f"flow status is invalid: {status_file}")
            exit_code = int(raw_exit)
            trusted = raw_dir.is_dir() and not raw_dir.is_symlink()
            if trusted:
                assert_under(raw_dir, resolved_run)
                marker = raw_dir / ".sanitized"
                trusted = (
                    marker.is_file()
                    and not marker.is_symlink()
                    and marker.read_bytes() == b"sanitized\n"
                )
            if trusted:
                for artifact in raw_dir.rglob("*"):
                    if artifact.is_symlink():
                        raise ReportError(f"trusted artifact tree contains a symlink: {artifact}")
                    if artifact.is_file() and artifact.stat().st_nlink != 1:
                        raise ReportError(f"trusted artifact tree contains a hardlink: {artifact}")
            if not trusted:
                status = "ERROR"
                report_error = "The flow did not produce a completed, sanitized artifact set."
                report_valid = False
            else:
                junit_source = raw_dir / "report.xml"
                if junit_source.is_file() and not junit_source.is_symlink():
                    junit_status, duration = parse_junit(junit_source)
                    status = "PASS" if exit_code == 0 and junit_status == "PASS" else "FAIL"
                    junit_parsed = True
                else:
                    status = "ERROR"
                    report_error = "The flow did not produce a valid JUnit result."
                    report_valid = False

                if status == "PASS":
                    candidates = [
                        path
                        for path in raw_dir.rglob(f"{evidence_name}.png")
                        if path.is_file() and not path.is_symlink()
                    ]
                    if len(candidates) != 1:
                        status = "ERROR"
                        report_error = "The passing flow did not produce exactly one reviewed evidence screenshot."
                        report_valid = False
                    else:
                        evidence_target = report_dir / "screenshots" / f"{evidence_name}.png"
                        evidence_sha256, evidence_width, evidence_height = sanitize_png(
                            candidates[0], evidence_target
                        )
                        evidence_path = f"screenshots/{evidence_name}.png"

                if junit_parsed:
                    junit_target = report_dir / "junit" / f"{slug}.xml"
                    junit_target.write_bytes(
                        normalized_junit(name, flow, status, duration, tags)
                    )
                    junit_target.chmod(0o600)
                    junit_path = f"junit/{slug}.xml"

        results.append(
            FlowResult(
                flow=flow,
                name=name,
                role=role_label(str(item["user"])),
                status=status,
                duration=duration,
                tags=tags,
                evidence_name=evidence_name,
                evidence_path=evidence_path,
                evidence_sha256=evidence_sha256,
                evidence_width=evidence_width,
                evidence_height=evidence_height,
                junit_path=junit_path,
                proofs=mapped_proofs.get(flow, []),
                report_error=report_error,
            )
        )

    if any(result.status in {"FAIL", "ERROR"} for result in results):
        overall = "FAIL"
    elif any(result.status == "NOT RUN" for result in results):
        overall = "INCOMPLETE"
    else:
        overall = "PASS"
    passed = sum(result.status == "PASS" for result in results)
    failed = sum(result.status in {"FAIL", "ERROR"} for result in results)
    not_run = sum(result.status == "NOT RUN" for result in results)
    total_duration = sum(result.duration or 0 for result in results)
    created = report_time(generated_at)

    manifest = {
        "schema_version": 1,
        "suite": suite_name,
        "result": overall,
        "generated_at": created,
        "harness_revision": harness_revision,
        "harness_dirty": harness_dirty,
        "harness_sha256": harness_sha256,
        "deployed_this_run": deployed_this_run,
        "reset_requested": reset_requested,
        "screenshot_review_status": "UNREVIEWED",
        "platform": platform,
        "maestro_version": maestro_version,
        "summary": {
            "planned": len(results),
            "passed": passed,
            "failed": failed,
            "not_run": not_run,
            "duration_seconds": total_duration,
        },
        "results": [result.__dict__ for result in results],
    }
    manifest_path = report_dir / "manifest.json"
    manifest_path.write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n")
    manifest_path.chmod(0o600)

    icon = {"PASS": "✅", "FAIL": "❌", "ERROR": "⚠️", "NOT RUN": "⏭️"}
    lines = [
        f"# Cantinarr Maestro `{suite_name}` report",
        "",
        f"> **{overall}** · **PRIVATE / SCREENSHOTS UNREVIEWED** · Generated locally from the disposable fixture lab. Review every image before sharing this bundle.",
        "",
        "## Run summary",
        "",
        "| Result | Planned | Passed | Failed | Not run | Duration |",
        "|---|---:|---:|---:|---:|---:|",
        f"| **{overall}** | {len(results)} | {passed} | {failed} | {not_run} | {total_duration:.1f}s |",
        "",
        "## Environment",
        "",
        "| Field | Value |",
        "|---|---|",
        f"| Harness revision | `{markdown_escape(harness_revision)}` ({'dirty' if harness_dirty else 'clean'}) |",
        f"| Harness content SHA-256 | `{harness_sha256}` |",
        "| Candidate provenance | "
        + (
            "Built and deployed from this checkout immediately before the suite |"
            if deployed_this_run
            else "Pre-existing lab deployment; its source revision was not attested by this run |"
        ),
        "| Lab state | "
        + (
            "A reset was requested; completion is not separately attested |"
            if reset_requested
            else "No reset requested; prior lab state is not attested |"
        ),
        f"| Platform | `{markdown_escape(platform)}` |",
        f"| Maestro | `{markdown_escape(maestro_version)}` |",
        "| Headless browser window | `500×1400` |",
        f"| Generated | `{created}` |",
        "| Target | Private workstation-loopback SSH tunnel |",
        "| Content | Deterministic checksum-locked CC0 fixtures only |",
        "| AI/cloud execution | Disabled and forbidden by the repository validator |",
        "",
        "## Results",
        "",
        "| Status | Flow | Catalog coverage | Role | Duration | Evidence |",
        "|---|---|---|---|---:|---|",
    ]
    for result in results:
        coverage = ", ".join(
            f"`{proof['case_id']}` ({proof['status']})" for proof in result.proofs
        ) or "—"
        evidence = f"[PNG]({result.evidence_path})" if result.evidence_path else "—"
        duration = f"{result.duration:.1f}s" if result.duration is not None else "—"
        lines.append(
            f"| {icon[result.status]} {result.status} | `{result.flow}` | {coverage} | {result.role} | {duration} | {evidence} |"
        )

    lines.extend(["", "## Evidence", ""])
    for result in results:
        lines.extend([f"### {icon[result.status]} {markdown_escape(result.name)}", ""])
        if result.proofs:
            lines.append("Catalog proof:")
            lines.append("")
            for proof in result.proofs:
                lines.append(
                    f"- `{proof['case_id']}` — **{proof['status']}**: {markdown_escape(proof['scope'])}"
                )
            lines.append("")
        lines.append(f"Role: **{result.role}**  ")
        lines.append(f"Result: **{result.status}**")
        lines.append("")
        if result.evidence_path:
            lines.append(f"![{markdown_escape(result.name)}]({result.evidence_path})")
            lines.append("")
            lines.append(
                f"PNG: `{result.evidence_width}×{result.evidence_height}` · SHA-256 `{result.evidence_sha256}`"
            )
            lines.append("")
        elif result.status in {"FAIL", "ERROR"}:
            lines.append(
                "No screenshot is embedded: automatic failure captures are private and may show an unreviewed sensitive screen."
            )
            lines.append("")
        elif result.status == "NOT RUN":
            lines.append("This flow was not executed after an earlier suite failure.")
            lines.append("")
        if result.report_error:
            lines.extend([f"Report note: {markdown_escape(result.report_error)}", ""])
        links = []
        if result.junit_path:
            links.append(f"[normalized JUnit]({result.junit_path})")
        if links:
            lines.extend(["Artifacts: " + " · ".join(links), ""])

    lines.extend(
        [
            "## Security attestation",
            "",
            "- The suite targeted only a loopback SSH forward into the disposable lab.",
            "- The lab password was removed from streaming console output and the sibling raw artifact tree before this report was rendered.",
            "- Only explicit, final `evidence-*` checkpoint screenshots were considered; their pixels have not been human-approved.",
            "- Every included PNG was decoded and canonically re-encoded as RGBA, then SHA-256 checksumed.",
            "- Console logs, automatic failure screenshots, command JSON, stack traces, local paths, tunnel ports, and raw environment values are excluded from this bundle.",
            "- Keep this report private until a person reviews every screenshot and confirms the fixture-only content policy.",
            "",
            "Machine-readable metadata: [manifest.json](manifest.json).",
            "",
        ]
    )
    report_path = report_dir / "REPORT.md"
    report_path.write_text("\n".join(lines))
    report_path.chmod(0o600)
    if harness_content_sha256(root) != harness_sha256:
        raise ReportError("harness inputs changed while the report was being rendered")
    report_dir.rename(final_report_dir)
    return final_report_dir / "REPORT.md", report_valid and overall == "PASS"


def render_report(
    *,
    root: Path,
    suite_name: str,
    run_dir: Path,
    harness_revision: str,
    harness_dirty: bool,
    harness_sha256: str,
    deployed_this_run: bool,
    reset_requested: bool,
    maestro_version: str,
    platform: str,
    generated_at: str | None = None,
) -> tuple[Path, bool]:
    staging = run_dir / f".report-staging-{os.getpid()}"
    staging_created = [False]
    try:
        return _render_report(
            root=root,
            suite_name=suite_name,
            run_dir=run_dir,
            harness_revision=harness_revision,
            harness_dirty=harness_dirty,
            harness_sha256=harness_sha256,
            deployed_this_run=deployed_this_run,
            reset_requested=reset_requested,
            maestro_version=maestro_version,
            platform=platform,
            staging_created=staging_created,
            generated_at=generated_at,
        )
    except BaseException:
        if staging_created[0] and staging.is_dir() and not staging.is_symlink():
            shutil.rmtree(staging, ignore_errors=True)
        raise


def main() -> int:
    if sys.argv[1:] == ["--print-harness-sha256"]:
        print(harness_content_sha256(ROOT))
        return 0
    parser = argparse.ArgumentParser()
    parser.add_argument("--suite", required=True)
    parser.add_argument("--run-dir", required=True, type=Path)
    parser.add_argument("--harness-revision", required=True)
    parser.add_argument("--harness-dirty", choices=("true", "false"), required=True)
    parser.add_argument("--harness-sha256", required=True)
    parser.add_argument("--deployed-this-run", choices=("true", "false"), required=True)
    parser.add_argument("--reset-requested", choices=("true", "false"), required=True)
    parser.add_argument("--maestro-version", required=True)
    parser.add_argument("--platform", default="web")
    parser.add_argument("--generated-at")
    args = parser.parse_args()
    report, valid = render_report(
        root=ROOT,
        suite_name=args.suite,
        run_dir=args.run_dir,
        harness_revision=args.harness_revision,
        harness_dirty=args.harness_dirty == "true",
        harness_sha256=args.harness_sha256,
        deployed_this_run=args.deployed_this_run == "true",
        reset_requested=args.reset_requested == "true",
        maestro_version=args.maestro_version,
        platform=args.platform,
        generated_at=args.generated_at,
    )
    print(report)
    return 0 if valid else 1


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, ReportError) as exc:
        print(f"Maestro report generation failed: {exc}", file=sys.stderr)
        raise SystemExit(1)
