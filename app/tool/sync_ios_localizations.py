#!/usr/bin/env python3
"""Declare ARB languages in the built iOS bundle before code signing."""

import argparse
import json
import os
from pathlib import Path
import plistlib
import re
import stat
import tempfile


LOCALE = re.compile(r"[a-z]{2,3}(?:_[A-Z][a-z]{3})?(?:_(?:[A-Z]{2}|[0-9]{3}))?")
SOURCE_PLIST = Path(__file__).resolve().parents[1] / "ios/Runner/Info.plist"


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f"duplicate JSON key: {key}")
        result[key] = value
    return result


def bundle_localizations(arb_dir: Path) -> list[str]:
    """Use the same language_script_REGION identity as Flutter's ARB inputs."""
    if not arb_dir.is_dir():
        raise ValueError(f"ARB directory does not exist: {arb_dir}")
    locales = set()
    for path in sorted(arb_dir.glob("app_*.arb")):
        locale = path.stem.removeprefix("app_")
        if not LOCALE.fullmatch(locale):
            raise ValueError(f"invalid locale in ARB filename: {path.name}")
        try:
            catalog = json.loads(path.read_text(encoding="utf-8"), object_pairs_hook=unique_object)
        except ValueError as error:
            raise ValueError(f"invalid ARB {path.name}: {error}") from error
        if not isinstance(catalog, dict):
            raise ValueError(f"ARB must contain a JSON object: {path.name}")
        if "@@locale" in catalog and catalog["@@locale"] != locale:
            raise ValueError(f"@@locale must match {locale!r} in {path.name}")
        if locale in locales:
            raise ValueError(f"duplicate ARB locale: {locale}")
        locales.add(locale)
    if not locales:
        raise ValueError(f"no app_*.arb catalogs found in {arb_dir}")
    for locale in sorted(locales):
        base = locale.split("_")[0]
        if base not in locales:
            raise ValueError(f"{locale} requires a base-language app_{base}.arb catalog")
    # Apple uses BCP 47 separators. Keep the development language first for a
    # deterministic fallback order; regional and script subtags remain intact.
    return [locale.replace("_", "-") for locale in sorted(locales, key=lambda value: (value != "en", value))]


def sync_localizations(arb_dir: Path, built_plist: Path) -> list[str]:
    if built_plist.resolve() == SOURCE_PLIST.resolve():
        raise ValueError("pass the built bundle Info.plist, not ios/Runner/Info.plist")
    locales = bundle_localizations(arb_dir)
    original = built_plist.read_bytes()
    values = plistlib.loads(original)
    if not isinstance(values, dict):
        raise ValueError("the built Info.plist must contain a dictionary")
    if values.get("CFBundleLocalizations") == locales:
        return locales
    values["CFBundleLocalizations"] = locales
    fmt = plistlib.FMT_BINARY if original.startswith(b"bplist00") else plistlib.FMT_XML
    updated = plistlib.dumps(values, fmt=fmt, sort_keys=False)
    mode = stat.S_IMODE(built_plist.stat().st_mode)
    temporary = None
    try:
        with tempfile.NamedTemporaryFile(dir=built_plist.parent, prefix=".localizations-", delete=False) as output:
            temporary = Path(output.name)
            output.write(updated)
        temporary.chmod(mode)
        os.replace(temporary, built_plist)
        temporary = None
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)
    return locales


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--arb-dir", type=Path, required=True)
    parser.add_argument("--plist", type=Path, required=True, help="Processed Info.plist inside the built app bundle")
    args = parser.parse_args()
    try:
        locales = sync_localizations(args.arb_dir, args.plist)
    except (OSError, ValueError, plistlib.InvalidFileException) as error:
        parser.error(str(error))
    print("iOS bundle localizations: " + ", ".join(locales))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
