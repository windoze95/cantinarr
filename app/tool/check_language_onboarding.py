#!/usr/bin/env python3
"""Prove that adding an ARB alone enables a language, without editing the checkout."""

import argparse
import json
from pathlib import Path
import plistlib
import shutil
import subprocess
import sys
import tempfile

APP = Path(__file__).resolve().parents[1]


def run(command, cwd):
    subprocess.run(command, cwd=cwd, check=True)


def fixtures(catalogs):
    # Prefer a new base language. As translations land, select another unused
    # fixture instead of requiring contributors to change this test.
    for code, name in [("ar", "العربية"), ("es", "Español"), ("de", "Deutsch"), ("it", "Italiano"),
                       ("pt", "Português"), ("nl", "Nederlands"), ("sv", "Svenska"),
                       ("fi", "Suomi"), ("pl", "Polski"), ("tr", "Türkçe"),
                       ("uk", "Українська"), ("ja", "日本語"), ("ko", "한국어")]:
        if code not in catalogs:
            yield code, name + " (fixture)", code
            break
    # A second single-file addition proves region persistence and provider
    # mapping. Its base already exists; Flutter requires that for variants.
    for code in ("fr_CA", "fr_CH", "fr_BE", "en_NZ", "en_AU", "en_ZA"):
        if code not in catalogs:
            yield code, code + " (fixture)", code.replace("_", "-")
            return
    # A real translation may occupy every probe slot. That is valid and must
    # not force its contributor to edit this harness. The ordinary catalog
    # suite still validates every shipped locale.


def check(flutter, locale, native_name, metadata_tag):
    with tempfile.TemporaryDirectory(prefix="cantinarr-language-") as directory:
        app = Path(directory) / "app"
        app.mkdir()
        for name in ("pubspec.yaml", "pubspec.lock", "l10n.yaml", "analysis_options.yaml"):
            shutil.copy2(APP / name, app / name)
        for name in ("lib", "assets", "test", "tool"):
            shutil.copytree(APP / name, app / name, ignore=shutil.ignore_patterns(
                "__pycache__", "*.pyc", "failures", "app_localizations*.dart"))

        english = json.loads((app / "lib/l10n/app_en.arb").read_text(encoding="utf-8"))
        english.update({"@@locale": locale, "languageNativeName": native_name,
                        "metadataLanguageTag": metadata_tag})
        added = app / "lib/l10n" / f"app_{locale}.arb"
        added.write_text(json.dumps(english, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        original_lock = (app / "pubspec.lock").read_bytes()
        print(f"Checking single-ARB addition: {added.name}", flush=True)
        run([flutter, "pub", "get", "--offline", "--enforce-lockfile"], app)
        if (app / "pubspec.lock").read_bytes() != original_lock:
            raise ValueError("Onboarding changed the dependency lockfile")
        run([flutter, "test", "--no-pub", "test/settings/settings_screen_test.dart", "test/l10n",
             f"--dart-define=CANTINARR_TEST_ADDED_LOCALE={locale}",
             f"--dart-define=CANTINARR_TEST_ADDED_LANGUAGE_NAME={native_name}",
             f"--dart-define=CANTINARR_TEST_METADATA_TAG={metadata_tag}"], app)

        built_plist = app / "Runner.app/Info.plist"
        built_plist.parent.mkdir()
        built_plist.write_bytes(plistlib.dumps({"CFBundleIdentifier": "test.onboarding",
                                               "CFBundleLocalizations": ["en"]}))
        run([sys.executable, "tool/sync_ios_localizations.py", "--arb-dir", "lib/l10n",
             "--plist", str(built_plist)], app)
        result = plistlib.loads(built_plist.read_bytes())
        if locale.replace("_", "-") not in result["CFBundleLocalizations"]:
            raise ValueError("Added ARB did not reach the native bundle declaration")
        if result["CFBundleIdentifier"] != "test.onboarding":
            raise ValueError("Native language generation changed an unrelated bundle value")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--flutter", default="flutter", help="Pinned Flutter executable")
    args = parser.parse_args()
    flutter = shutil.which(args.flutter)
    if flutter is None:
        parser.error("Flutter executable not found; use --flutter with the pinned SDK")
    catalogs = {path.stem.removeprefix("app_") for path in (APP / "lib/l10n").glob("app_*.arb")}
    probes = list(fixtures(catalogs))
    for locale, name, tag in probes:
        check(flutter, locale, name, tag)
    if probes:
        print("ARB-only onboarding passed; source catalogs were not modified.")
    else:
        print("All probe locales are real translations; the regular Flutter catalog tests cover them.")


if __name__ == "__main__":
    main()
