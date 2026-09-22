import json
import os
from pathlib import Path
import subprocess
import tempfile
import textwrap
import unittest


ROOT = Path(__file__).resolve().parents[2]
SHA = "a" * 40
IMAGE = "ghcr.io/example/app@sha256:" + "b" * 64
DIGESTS = {"amd64": "sha256:" + "c" * 64, "arm64": "sha256:" + "d" * 64}


class BinaryPlatformTests(unittest.TestCase):
    def run_resolution(self, manifests, *, revision=SHA, platform_override=""):
        # Execute the actual workflow shell through container selection. The
        # fake classic image store rejects pulling the shared index reference.
        workflow = (ROOT / ".github/workflows/release-binaries.yml").read_text()
        start = workflow.index('          docker buildx imagetools inspect "$IMAGE" --raw')
        end = workflow.index('            docker cp "$cid:/usr/local/bin/cantinarr"', start)
        script = "set -euo pipefail\n" + textwrap.dedent(workflow[start:end]) + "done\n"
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            docker = root / "docker"
            docker.write_text(textwrap.dedent('''\
                #!/usr/bin/env python3
                import json, os, sys
                args = sys.argv[1:]
                with open(os.environ["CALLS"], "a") as log:
                    log.write(json.dumps(args) + "\\n")
                if args[:3] == ["buildx", "imagetools", "inspect"]:
                    assert args[3:] == [os.environ["IMAGE"], "--raw"]
                    print(os.environ["MANIFEST"])
                elif args[0] in ["pull", "create"]:
                    arch = args[2].split("/")[1]
                    expected = "ghcr.io/example/app@" + json.loads(os.environ["DIGESTS"])[arch]
                    assert args == [args[0], "--platform", "linux/" + arch, expected], args
                    if args[0] == "create":
                        print("container-" + arch)
                elif args[:2] == ["image", "inspect"]:
                    arch = next(arch for arch, digest in json.loads(os.environ["DIGESTS"]).items()
                                if args[2] == "ghcr.io/example/app@" + digest)
                    if "revision" in args[4]:
                        print(os.environ["REVISION"])
                    else:
                        print(os.environ["PLATFORM_OVERRIDE"] or "linux/" + arch)
                else:
                    raise AssertionError(args)
                '''))
            docker.chmod(0o755)
            env = {**os.environ, "PATH": directory + os.pathsep + os.environ["PATH"],
                   "RUNNER_TEMP": directory, "IMAGE": IMAGE, "SHA": SHA,
                   "REVISION": revision, "PLATFORM_OVERRIDE": platform_override,
                   "DIGESTS": json.dumps(DIGESTS), "MANIFEST": json.dumps({"manifests": manifests}),
                   "CALLS": str(root / "calls.jsonl")}
            result = subprocess.run(["bash", "-c", script], cwd=root, env=env,
                                    capture_output=True, text=True)
            calls = [json.loads(line) for line in (root / "calls.jsonl").read_text().splitlines()]
            return result, calls

    def manifests(self):
        return [{"digest": digest, "platform": {"os": "linux", "architecture": arch}}
                for arch, digest in DIGESTS.items()]

    def test_both_platforms_use_distinct_pinned_children_and_ignore_attestations(self):
        manifests = self.manifests() + [{"digest": "sha256:" + "e" * 64,
                                        "platform": {"os": "unknown", "architecture": "unknown"}}]
        result, calls = self.run_resolution(manifests)
        self.assertEqual(result.returncode, 0, result.stderr)
        for command in ["pull", "create"]:
            self.assertEqual([call[-1] for call in calls if call[0] == command],
                             ["ghcr.io/example/app@" + digest for digest in DIGESTS.values()])

    def test_missing_duplicate_invalid_and_non_linux_members_fail_closed(self):
        valid = self.manifests()
        invalid_sets = [valid[1:], valid + [valid[0]],
                        [{**valid[0], "digest": "latest"}, valid[1]],
                        [{**valid[0], "platform": {"os": "windows", "architecture": "amd64"}}, valid[1]]]
        for manifests in invalid_sets:
            with self.subTest(manifests=manifests):
                result, calls = self.run_resolution(manifests)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(any(call[0] in ["pull", "create"] for call in calls))

    def test_wrong_source_or_platform_cannot_be_extracted(self):
        for overrides in [{"revision": "f" * 40}, {"platform_override": "linux/arm64"}]:
            with self.subTest(overrides=overrides):
                result, calls = self.run_resolution(self.manifests(), **overrides)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(any(call[0] == "create" for call in calls))


if __name__ == "__main__":
    unittest.main()
