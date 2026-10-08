"""Prepare human-readable notes from the exact mobile source checkout."""

import argparse
from pathlib import Path
import re


SOURCES = {
    "android": "app/android/fastlane/metadata/android/en-US/changelogs/default.txt",
    "ios": "app/ios/fastlane/what_to_test.txt",
}


def render(platform, root, version, build, channel, pr="", sha=""):
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?", version):
        raise ValueError("Mobile notes require the exact marketing version")
    if not re.fullmatch(r"[1-9][0-9]*", build):
        raise ValueError("Mobile notes require the exact positive build number")
    if channel not in {"beta", "candidate", "preview"}:
        raise ValueError("Mobile notes require a publishing channel")
    body = (Path(root) / SOURCES[platform]).read_text(encoding="utf-8").strip()
    if not body or re.fullmatch(r"(?:candidate|beta|preview)\s*\|\s*PR .*\|\s*commit [0-9a-f]+", body):
        raise ValueError("Write a meaningful changelog before uploading this build")
    if re.match(r"Cantinarr [0-9]", body):
        raise ValueError("Version headings are generated; keep the source notes free of stale version headings")
    heading = f"Cantinarr {version} ({build})" if platform == "ios" else f"Cantinarr {version}"
    notes = f"{heading}\n\n{body}"
    if channel == "preview":
        if not re.fullmatch(r"[1-9][0-9]*", pr) or not re.fullmatch(r"[0-9a-f]{40}", sha):
            raise ValueError("Preview notes require the reviewed PR and source SHA")
        notes += f"\n\nPreview PR #{pr}, commit {sha}."
    limit = 500 if platform == "android" else 4000
    if len(notes) > limit:
        raise ValueError(f"{platform} notes exceed {limit} characters; shorten the source notes")
    return notes + "\n"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--platform", choices=SOURCES, required=True)
    parser.add_argument("--source-root", type=Path, required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--build", required=True)
    parser.add_argument("--channel", required=True)
    parser.add_argument("--pr", default="")
    parser.add_argument("--sha", default="")
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    try:
        notes = render(args.platform, args.source_root, args.version, args.build,
                       args.channel, args.pr, args.sha)
    except (OSError, ValueError) as error:
        parser.error(str(error))
    args.output.write_text(notes, encoding="utf-8")


if __name__ == "__main__":
    main()
