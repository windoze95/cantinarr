"""Copy the notices shipped by installed distributions into the image."""

import importlib.metadata
from pathlib import Path
import shutil
import sys


def collect(destination):
    destination.mkdir(parents=True, exist_ok=True)
    index = []
    for distribution in sorted(importlib.metadata.distributions(),
                               key=lambda item: item.metadata["Name"].lower()):
        name = distribution.metadata["Name"]
        copied = []
        for entry in distribution.files or []:
            if not any(word in str(entry).lower() for word in ("license", "notice", "copying")):
                continue
            source = Path(distribution.locate_file(entry))
            if not source.is_file():
                continue
            target = destination / name / Path(entry).name
            target.parent.mkdir(parents=True, exist_ok=True)
            if target.exists() and target.read_bytes() != source.read_bytes():
                target = target.with_name(str(len(copied)) + "-" + target.name)
            shutil.copyfile(source, target)
            copied.append(str(target.relative_to(destination)))
        # Metadata also carries license expressions/URLs when a distribution
        # doesn't ship a standalone license. Preserve that verbatim as well.
        metadata = destination / name / "METADATA"
        metadata.parent.mkdir(parents=True, exist_ok=True)
        metadata.write_text(distribution.read_text("METADATA") or "", encoding="utf-8")
        index.append(f"{name} {distribution.version}: " + ", ".join(copied + [str(metadata.relative_to(destination))]))
    (destination / "INDEX.txt").write_text("\n".join(index) + "\n", encoding="utf-8")


if __name__ == "__main__":
    collect(Path(sys.argv[1]))
