from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import mobile_release_notes as notes


class MobileReleaseNotesTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.write("android", "Browse and request media. Receive ready notifications.")
        self.write("ios", "Test retained sign-in, requests, passkeys, links and push.")

    def write(self, platform, text):
        source = self.root / notes.SOURCES[platform]
        source.parent.mkdir(parents=True, exist_ok=True)
        source.write_text(text, encoding="utf-8")

    def render(self, platform="android", channel="candidate", **kwargs):
        return notes.render(platform, self.root, "1.0.0", "295", channel, **kwargs)

    def test_promotable_android_notes_are_the_source_changelog(self):
        expected = "Cantinarr 1.0.0\n\nBrowse and request media. Receive ready notifications.\n"
        for channel in ("candidate", "beta"):
            self.assertEqual(self.render(channel=channel), expected)
        self.write("android", "This checkout fixes request notifications.")
        self.assertEqual(self.render(), "Cantinarr 1.0.0\n\nThis checkout fixes request notifications.\n")

    def test_stale_handwritten_version_headings_cannot_reach_a_new_build(self):
        for platform in notes.SOURCES:
            self.write(platform, "Cantinarr 0.1\n\nReadable but stale notes.")
            with self.assertRaises(ValueError):
                self.render(platform)

    def test_testflight_notes_include_the_exact_build_and_actual_test_guidance(self):
        text = self.render("ios")
        self.assertEqual(text, "Cantinarr 1.0.0 (295)\n\nTest retained sign-in, requests, passkeys, links and push.\n")

    def test_preview_identity_is_a_footer_after_meaningful_notes(self):
        for platform in notes.SOURCES:
            text = self.render(platform, "preview", pr="702", sha="a" * 40)
            self.assertIn((self.root / notes.SOURCES[platform]).read_text(), text)
            self.assertTrue(text.endswith("Preview PR #702, commit " + "a" * 40 + ".\n"))
        with self.assertRaises(ValueError):
            self.render(channel="preview")

    def test_empty_metadata_only_missing_and_overlong_notes_fail_closed(self):
        for platform, limit in (("android", 500), ("ios", 4000)):
            for body in ("", "  \n", "candidate | PR merged | commit " + "a" * 40, "x" * (limit + 1)):
                self.write(platform, body)
                with self.assertRaises(ValueError):
                    self.render(platform)
            (self.root / notes.SOURCES[platform]).unlink()
            with self.assertRaises(FileNotFoundError):
                self.render(platform)

    def test_cli_writes_the_same_selected_notes_without_executing_their_text(self):
        body = "Requests work. Literal $(touch should-not-exist) and `echo text` stay text."
        self.write("android", body)
        output = self.root / "295.txt"
        process = subprocess.run([
            sys.executable, str(Path(notes.__file__)), "--platform", "android",
            "--source-root", str(self.root), "--version", "1.0.0", "--build", "295",
            "--channel", "candidate", "--output", str(output),
        ], cwd=self.root, capture_output=True, text=True)
        self.assertEqual(process.returncode, 0, process.stderr)
        self.assertEqual(output.read_text(), "Cantinarr 1.0.0\n\n" + body + "\n")
        self.assertFalse((self.root / "should-not-exist").exists())


if __name__ == "__main__":
    unittest.main()
