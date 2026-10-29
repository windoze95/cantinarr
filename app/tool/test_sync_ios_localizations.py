from pathlib import Path
import json
import plistlib
import subprocess
import sys
import tempfile
import unittest

import sync_ios_localizations as sync


class BundleLocalizationTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="native locales ")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.arbs = self.root / "catalogs"
        self.arbs.mkdir()
        self.plist = self.root / "Runner.app" / "Info.plist"
        self.plist.parent.mkdir()
        self.catalog("en")
        self.catalog("fr")

    def catalog(self, locale, **extra):
        path = self.arbs / f"app_{locale}.arb"
        path.write_text(json.dumps({"@@locale": locale, "languageNativeName": locale, **extra}), encoding="utf-8")
        return path

    def write_plist(self, fmt=plistlib.FMT_XML):
        values = {
            "CFBundleIdentifier": "codes.julian.cantinarr",
            "CFBundleDevelopmentRegion": "en",
            "CFBundleLocalizations": ["en", "stale"],
            "CFBundleVersion": "123",
            "NSBonjourServices": ["_dartVmService._tcp"],
            "Nested": {"Boolean": True, "Integer": 17, "Data": b"\x00\xff"},
        }
        self.plist.write_bytes(plistlib.dumps(values, fmt=fmt))
        return values

    def test_adding_one_arb_updates_xml_and_binary_without_touching_other_fields(self):
        for fmt in (plistlib.FMT_XML, plistlib.FMT_BINARY):
            with self.subTest(fmt=fmt):
                original = self.write_plist(fmt)
                self.catalog("es", languageNativeName="Español")
                self.assertEqual(sync.sync_localizations(self.arbs, self.plist), ["en", "es", "fr"])
                encoded = self.plist.read_bytes()
                self.assertEqual(encoded.startswith(b"bplist00"), fmt == plistlib.FMT_BINARY)
                values = plistlib.loads(encoded)
                self.assertEqual(values.pop("CFBundleLocalizations"), ["en", "es", "fr"])
                original.pop("CFBundleLocalizations")
                self.assertEqual(values, original)

    def test_regions_scripts_and_numeric_regions_are_preserved(self):
        for locale in ("fr_CA", "zh", "zh_Hant", "zh_Hant_TW", "es", "es_419"):
            self.catalog(locale)
        self.assertEqual(sync.bundle_localizations(self.arbs), ["en", "es", "es-419", "fr", "fr-CA", "zh", "zh-Hant", "zh-Hant-TW"])

    def test_filename_locale_without_metadata_is_supported(self):
        (self.arbs / "app_de.arb").write_text('{"languageNativeName":"Deutsch"}', encoding="utf-8")
        self.assertEqual(sync.bundle_localizations(self.arbs), ["en", "de", "fr"])

    def test_invalid_catalogs_leave_the_built_plist_untouched(self):
        invalid = [
            ("app_de.arb", "[]", "JSON object"),
            ("app_de.arb", "{", "invalid ARB"),
            ("app_de.arb", '{"@@locale":"es"}', "must match"),
            ("app_de.arb", '{"@@locale":null}', "must match"),
            ("app_de.arb", '{"@@locale":"de","@@locale":"de"}', "duplicate JSON key"),
            ("app_de.arb", '{"@@locale":"de","message":"one","message":"two"}', "duplicate JSON key"),
            ("app_en-us.arb", '{"@@locale":"en-us"}', "invalid locale"),
            ("app_DE.arb", '{"@@locale":"DE"}', "invalid locale"),
            ("app_de_DE.arb", '{"@@locale":"de_DE"}', "base-language"),
            ("app_zh_Hant.arb", '{"@@locale":"zh_Hant"}', "base-language"),
            ("app_en_US_Hant.arb", '{"@@locale":"en_US_Hant"}', "invalid locale"),
        ]
        for name, text, message in invalid:
            with self.subTest(name=name, text=text):
                self.write_plist()
                original = self.plist.read_bytes()
                bad = self.arbs / name
                bad.write_text(text, encoding="utf-8")
                try:
                    with self.assertRaisesRegex(ValueError, message):
                        sync.sync_localizations(self.arbs, self.plist)
                    self.assertEqual(self.plist.read_bytes(), original)
                finally:
                    bad.unlink()

    def test_empty_or_missing_catalog_directory_fails(self):
        for path in self.arbs.glob("*.arb"):
            path.unlink()
        with self.assertRaisesRegex(ValueError, "no app_.*arb catalogs"):
            sync.bundle_localizations(self.arbs)
        with self.assertRaisesRegex(ValueError, "directory does not exist"):
            sync.bundle_localizations(self.root / "missing")

    def test_invalid_plist_and_tracked_source_are_never_overwritten(self):
        for data in (b"not a plist", plistlib.dumps(["not", "a dictionary"])):
            self.plist.write_bytes(data)
            with self.assertRaises((ValueError, plistlib.InvalidFileException)):
                sync.sync_localizations(self.arbs, self.plist)
            self.assertEqual(self.plist.read_bytes(), data)
        source = sync.SOURCE_PLIST.read_bytes()
        with self.assertRaisesRegex(ValueError, "built bundle"):
            sync.sync_localizations(self.arbs, sync.SOURCE_PLIST)
        self.assertEqual(sync.SOURCE_PLIST.read_bytes(), source)

    def test_repeat_build_preserves_format_permissions_and_modification_time(self):
        self.write_plist(plistlib.FMT_BINARY)
        self.plist.chmod(0o640)
        sync.sync_localizations(self.arbs, self.plist)
        first = self.plist.stat()
        sync.sync_localizations(self.arbs, self.plist)
        self.assertEqual(self.plist.stat().st_mtime_ns, first.st_mtime_ns)
        self.assertEqual(self.plist.stat().st_mode & 0o777, 0o640)
        self.assertEqual(list(self.plist.parent.glob(".localizations-*")), [])

    def test_cli_accepts_paths_with_spaces_and_updates_the_built_bundle(self):
        self.write_plist()
        self.catalog("de", languageNativeName="Deutsch")
        process = subprocess.run([
            sys.executable, str(Path(sync.__file__)), "--arb-dir", str(self.arbs), "--plist", str(self.plist),
        ], capture_output=True, text=True)
        self.assertEqual(process.returncode, 0, process.stderr)
        self.assertIn("en, de, fr", process.stdout)
        self.assertEqual(plistlib.loads(self.plist.read_bytes())["CFBundleLocalizations"], ["en", "de", "fr"])

    def test_xcode_hook_runs_after_flutter_and_before_signing_against_built_plist(self):
        project = (Path(__file__).resolve().parents[1] / "ios/Runner.xcodeproj/project.pbxproj").read_text()
        start = project.index("3B06AD1E1E4923F5004D2608 /* Thin Binary */ = {")
        phase = project[start:project.index("\n\t\t};", start)]
        self.assertIn("alwaysOutOfDate = 1", phase)
        self.assertLess(phase.index("embed_and_thin"), phase.index("sync_ios_localizations.py"))
        self.assertIn("${TARGET_BUILD_DIR}/${INFOPLIST_PATH}", phase)
        self.assertNotIn("Runner/Info.plist", phase)


if __name__ == "__main__":
    unittest.main()
