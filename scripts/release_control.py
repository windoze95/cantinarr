#!/usr/bin/env python3
"""Release routing and provenance shared by the publishing workflows.

GitHub run artifacts are receipts, not user-supplied build selectors. Production
accepts only successful candidate runs from the expected workflow and commit.
All GitHub reads fail closed; an unavailable API is never an empty release list.
"""

import argparse
import io
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import time
import zipfile

VERSION = re.compile(r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\Z")
SHA = re.compile(r"[0-9a-f]{40}\Z")
WORKFLOWS = {"server": "docker.yml", "ios": "testflight.yml", "android": "playstore.yml"}


class ReleaseError(Exception):
    pass


class ReleasePaused(ReleaseError):
    """A release freeze has taken public beta ownership from an unchanged main."""


class ArtifactMissing(ReleaseError):
    pass


def command(*args):
    result = subprocess.run(args, capture_output=True, check=False)
    if result.returncode:
        raise ReleaseError(f"{args[0]} failed: {result.stderr.decode(errors='replace')[:500]}")
    return result.stdout


def api(path):
    return json.loads(command("gh", "api", path))


def pages(path):
    return json.loads(command("gh", "api", "--paginate", "--slurp", path))


def repo_path(suffix):
    return f"repos/{os.environ['GITHUB_REPOSITORY']}/{suffix}"


def branch_state():
    branches = [b for page in pages(repo_path("branches?per_page=100")) for b in page]
    return {b["name"]: b["commit"]["sha"] for b in branches}


def release_branch_version(branch):
    version = branch.removeprefix("release/")
    if not branch.startswith("release/") or not VERSION.fullmatch(version):
        raise ReleaseError("Candidate branch must be release/X.Y.Z")
    return version


def channel_for(ref, sha, branches):
    """A single live release branch owns public betas until it is deleted."""
    active = [name for name in branches if name.startswith("release/")]
    for name in active:
        release_branch_version(name)
    if len(active) > 1:
        raise ReleaseError("Multiple release branches exist; keep only one active candidate")
    branch = ref.removeprefix("refs/heads/")
    if not ref.startswith("refs/heads/") or branch not in {"main", *active}:
        raise ReleaseError("Publishing is allowed only from main or the active release branch")
    if branches.get(branch) != sha:
        return "superseded"
    if branch == "main":
        return "paused" if active else "beta"
    return "candidate"


def emit(values):
    if output := os.environ.get("GITHUB_OUTPUT"):
        with open(output, "a") as stream:
            for key, value in values.items():
                value = str(value)
                if "\n" in value or "\r" in value:
                    raise ReleaseError("Workflow output must be a single line")
                stream.write(f"{key}={value}\n")


def preview_source(pr, reviewed_sha):
    if not SHA.fullmatch(reviewed_sha or "") or pr["head"]["sha"] != reviewed_sha:
        raise ReleaseError("Reviewed head SHA must equal the PR's current full head SHA")
    if pr["state"] != "open" or pr.get("draft") or not SHA.fullmatch(pr.get("merge_commit_sha") or ""):
        raise ReleaseError("Preview requires an open, ready PR with a merge commit")
    if pr["base"]["ref"] != "main" and not pr["base"]["ref"].startswith("release/"):
        raise ReleaseError("Preview PR must target main or a release branch")
    return {"sha": pr["merge_commit_sha"], "ci_head": reviewed_sha,
            "ref": f"refs/pull/{pr['number']}/merge", "channel": "preview",
            "pr": pr["number"], "reviewed_sha": reviewed_sha}


def prepare():
    number = os.environ.get("PREVIEW_PR", "")
    if number:
        if not number.isdigit() or int(number) <= 0:
            raise ReleaseError("PR number must be positive")
        if os.environ["GITHUB_REF"] != "refs/heads/main":
            raise ReleaseError("Dispatch phone previews from main")
        actor = os.environ.get("GITHUB_TRIGGERING_ACTOR") or os.environ["GITHUB_ACTOR"]
        permission = api(repo_path(f"collaborators/{actor}/permission"))["permission"]
        if permission not in {"admin", "maintain", "write"}:
            raise ReleaseError("Only a repository maintainer may authorize a phone preview")
        source = preview_source(api(repo_path(f"pulls/{number}")), os.environ.get("REVIEWED_SHA", ""))
    else:
        ref, sha = os.environ["GITHUB_REF"], os.environ["GITHUB_SHA"]
        source = {"sha": sha, "ci_head": sha, "ref": ref,
                  "channel": channel_for(ref, sha, branch_state())}
    enabled = source["channel"] not in {"paused", "superseded"}
    print(f"Publishing channel: {source['channel']} ({source['sha']})")
    emit({**source, "enabled": str(enabled).lower(), "source": json.dumps(source)})


def current(source, *, server=False):
    if source["channel"] == "preview":
        pr = api(repo_path(f"pulls/{source['pr']}"))
        checked = preview_source(pr, source["reviewed_sha"])
        if checked["sha"] != source["sha"]:
            raise ReleaseError("PR merge changed after testing; dispatch and review the new revision")
    else:
        channel = channel_for(source["ref"], source["sha"], branch_state())
        allowed = {source["channel"]}
        if server and source["ref"] == "refs/heads/main":
            allowed |= {"paused", "beta"}
        if channel not in allowed or channel == "superseded":
            if source["channel"] == "beta" and channel == "paused":
                raise ReleasePaused("Publishing paused: a release branch now owns public betas")
            raise ReleaseError(f"Publishing stopped: source is now {channel}")


def report_pause():
    message = ("A release branch now owns public beta publishing. This main run "
               "skipped further publishing; resume it after the release closes.")
    print(f"::notice::{message}")
    if summary := os.environ.get("GITHUB_STEP_SUMMARY"):
        with open(summary, "a") as stream:
            stream.write(f"### Publishing paused\n\n{message}\n\n")


def artifact_json(run, name, filename):
    # Rerunning failed jobs may reuse a receipt from an earlier successful job
    # in the same run. Prefer the newest attempt that actually produced one.
    stem = name.rsplit("-", 1)[0] + "-"
    artifacts = [a for page in pages(repo_path(f"actions/runs/{run['id']}/artifacts?per_page=100"))
                 for a in page["artifacts"] if a["name"].startswith(stem)
                 and a["name"][len(stem):].isdigit()
                 and int(a["name"][len(stem):]) <= run["run_attempt"] and not a["expired"]]
    if not artifacts:
        raise ArtifactMissing(f"No retained {name} artifact on run {run['id']}")
    artifacts.sort(key=lambda a: int(a["name"][len(stem):]), reverse=True)
    raw = command("gh", "api", repo_path(f"actions/artifacts/{artifacts[0]['id']}/zip"))
    with zipfile.ZipFile(io.BytesIO(raw)) as archive:
        return json.loads(archive.read(filename))


def validate_record(record, run, platform):
    expected_path = f".github/workflows/{WORKFLOWS[platform]}"
    if run["conclusion"] != "success" or run["event"] not in {"push", "workflow_dispatch"}:
        raise ReleaseError("Production requires a successful branch build, never a PR preview")
    if run["path"].split("@")[0] != expected_path:
        raise ReleaseError("Build came from the wrong workflow")
    if (record.get("schema") != 1 or record.get("platform") != platform
            or record.get("repository") != os.environ["GITHUB_REPOSITORY"]
            or record.get("run_id") != run["id"]
            or not 1 <= record.get("run_attempt", 0) <= run["run_attempt"]
            or record.get("sha") != run["head_sha"]
            or record.get("ref") != f"refs/heads/{run['head_branch']}"
            or record.get("channel") != "candidate"):
        raise ReleaseError("Build receipt does not match its successful candidate run")
    release_branch_version(run["head_branch"])
    if not SHA.fullmatch(record["sha"]) or not VERSION.fullmatch(record.get("version", "")):
        raise ReleaseError("Invalid candidate version or source commit")
    if platform == "server":
        if record["version"] != release_branch_version(run["head_branch"]):
            raise ReleaseError("Server version must match its release branch")
        if not re.fullmatch(r"sha256:[0-9a-f]{64}", record.get("digest", "")):
            raise ReleaseError("Invalid server image digest")
    elif not re.fullmatch(r"[1-9]\d*", record.get("build_number", "")):
        raise ReleaseError("Invalid mobile build number")
    return record


def select_record(platform, run_id):
    run = api(repo_path(f"actions/runs/{int(run_id)}"))
    record = artifact_json(run, f"{platform}-release-record-{run['run_attempt']}", "release.json")
    return validate_record(record, run, platform)


def write_record(source, platform):
    version = os.environ["BUILD_VERSION"]
    if not VERSION.fullmatch(version):
        raise ReleaseError("Build version must be X.Y.Z")
    record = {**source, "schema": 1, "platform": platform, "version": version,
              "run_id": int(os.environ["GITHUB_RUN_ID"]),
              "run_attempt": int(os.environ["GITHUB_RUN_ATTEMPT"]),
              "repository": os.environ["GITHUB_REPOSITORY"]}
    if platform == "server":
        record["digest"] = os.environ["IMAGE_DIGEST"]
    else:
        record["build_number"] = os.environ["BUILD_NUMBER"]
    record["min_app_version"] = re.search(r'const MinAppVersion = "([^"]+)"',
        Path("server/internal/version/version.go").read_text())[1]
    record["min_server_version"] = re.search(r"const String minServerVersion = '([^']+)'",
        Path("app/lib/core/utils/version_compat.dart").read_text())[1]
    Path("release.json").write_text(json.dumps(record, indent=2) + "\n")
    print(json.dumps(record, indent=2))


def wait_ci(sha, ci_head, minutes):
    deadline = time.monotonic() + minutes * 60
    while True:
        runs = api(repo_path(f"actions/workflows/ci.yml/runs?head_sha={ci_head}&per_page=100"))["workflow_runs"]
        for run in runs:
            if run["conclusion"] == "success":
                try:
                    receipt = artifact_json(run, f"ci-source-{run['run_attempt']}", "ci-source.json")
                except ArtifactMissing:
                    continue
                if receipt.get("sha") == sha:
                    print(f"CI proved {sha}: {run['html_url']}")
                    return
        # Branch pushes test their head directly. PR runs need the checkout
        # receipt because GitHub records their head, not the tested merge SHA.
        relevant = [r for r in runs if r["event"] == "push" and r["head_sha"] == sha]
        if relevant and all(r.get("status") == "completed" for r in relevant):
            raise ReleaseError(f"CI did not pass for {sha}; fix or rerun CI before publishing")
        if time.monotonic() >= deadline:
            raise ReleaseError(f"No successful CI run proved the exact checkout {sha}")
        print(f"Waiting for CI evidence for {sha}", flush=True)
        time.sleep(20)


def find_server(sha, version):
    # A rebuild of the same commit can have a different digest. The maintainer's
    # recorded selection, rather than the newest Docker run, decides what ships.
    runs = [run for page in pages(repo_path("actions/workflows/release-candidate.yml/runs"
        "?branch=main&event=workflow_dispatch&status=success&per_page=100")) for run in page["workflow_runs"]]
    for run in runs:
        if (run["conclusion"] != "success" or run["head_branch"] != "main"
                or run["event"] != "workflow_dispatch"
                or run["path"].split("@")[0] != ".github/workflows/release-candidate.yml"):
            continue
        manifest = artifact_json(run, f"release-candidate-{run['run_attempt']}", "release-candidate.json")
        chosen = manifest.get("components", {}).get("server", {})
        if chosen.get("sha") == sha and chosen.get("version") == version:
            verified = combine_records(manifest["components"], manifest.get("verification", ""))
            if verified != manifest or select_record("server", chosen["run_id"]) != chosen:
                raise ReleaseError("Recorded server candidate no longer matches its build receipt")
            return manifest
    raise ReleaseError("Record a verified server candidate for this tag's exact commit and version before tagging")


def combine_records(records, verification):
    if not records or not verification.strip():
        raise ReleaseError("Select candidate runs and record the actual verification performed")
    if len({(r["sha"], r["ref"]) for r in records.values()}) != 1:
        raise ReleaseError("Candidate components were built from different commits")
    first = next(iter(records.values()))
    return {"schema": 1, "sha": first["sha"], "ref": first["ref"],
            "components": records, "verification": verification}


def candidate_manifest():
    records = {}
    for platform in WORKFLOWS:
        if run_id := os.environ.get(platform.upper() + "_RUN", ""):
            record = select_record(platform, int(run_id))
            current(record)
            records[platform] = record
    manifest = combine_records(records, os.environ.get("VERIFICATION", ""))
    Path("release-candidate.json").write_text(json.dumps(manifest, indent=2) + "\n")
    if summary := os.environ.get("GITHUB_STEP_SUMMARY"):
        with open(summary, "a") as stream:
            stream.write(f"## Release candidate\n\nCommit: `{manifest['sha']}`\n\n")
            for name, record in records.items():
                stream.write(f"- {name}: {record['version']} ({record.get('build_number', record.get('digest'))})\n")


def inspect_image(image, *, config=False):
    args = ["docker", "buildx", "imagetools", "inspect"]
    if config:
        args += ["--format", "{{json .Image}}"]
    result = subprocess.run([*args, image], capture_output=True, text=True, check=False)
    if result.returncode == 0:
        if config:
            return json.loads(result.stdout)
        match = re.search(r"^Digest:\s+(sha256:[0-9a-f]{64})", result.stdout, re.MULTILINE)
        if match:
            return match[1]
        raise ReleaseError("Registry returned no image digest")
    error = result.stderr.lower()
    if any(word in error for word in ("unauthorized", "denied", "forbidden", "authenticate", "authorize")):
        raise ReleaseError("Could not inspect the published image; registry access failed")
    if "manifest unknown" in error or "not found" in error:
        return None
    raise ReleaseError("Could not inspect the published image; registry access failed")


def promote_server(record):
    image = "ghcr.io/" + os.environ["GITHUB_REPOSITORY"].lower()
    source = f"{image}@{record['digest']}"
    manifest = json.loads(command("docker", "buildx", "imagetools", "inspect", "--raw", source))
    architectures = {m.get("platform", {}).get("architecture") for m in manifest.get("manifests", [])}
    if not {"amd64", "arm64"}.issubset(architectures):
        raise ReleaseError("Candidate is not a multi-architecture image")
    releases = [r for page in pages(repo_path("releases?per_page=100")) for r in page
                if not r["draft"] and not r["prerelease"] and VERSION.fullmatch(r["tag_name"].removeprefix("v"))]
    target = tuple(map(int, record["version"].split(".")))
    if any(tuple(map(int, r["tag_name"].removeprefix("v").split("."))) > target for r in releases):
        raise ReleaseError("A newer stable release already exists; refusing to roll latest backward")
    # A previous run may have moved latest before GitHub Release creation
    # failed. Inspect its actual configs as well as the release list.
    configs = inspect_image(f"{image}:latest", config=True)
    if configs:
        for config in configs.values():
            version = config["config"]["Labels"]["org.opencontainers.image.version"].removeprefix("v").split("-")[0]
            if not VERSION.fullmatch(version):
                raise ReleaseError("Cannot establish the version currently published as latest")
            if tuple(map(int, version.split("."))) > target:
                raise ReleaseError("A newer image is already latest; refusing an older retry")
    # Published numbered images are immutable, including retries after partial publication.
    existing = inspect_image(f"{image}:{record['version']}")
    if existing and existing != record["digest"]:
        raise ReleaseError("Numbered image already exists with another digest")
    for tag in (record["version"], ".".join(record["version"].split(".")[:2]), "latest"):
        command("docker", "buildx", "imagetools", "create", "--tag", f"{image}:{tag}", source)
        if inspect_image(f"{image}:{tag}") != record["digest"]:
            raise ReleaseError(f"Published {tag} does not match the tested candidate digest")
    print(f"Promoted tested image {record['digest']} as {record['version']} and latest")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=["prepare", "current", "record", "select", "ci", "server", "promote-server", "manifest"])
    parser.add_argument("--platform", choices=list(WORKFLOWS))
    parser.add_argument("--run-id", type=int)
    parser.add_argument("--sha")
    parser.add_argument("--ci-head")
    parser.add_argument("--minutes", type=int, default=45)
    parser.add_argument("--skip-paused", action="store_true",
                        help="For current, emit enabled=false instead of failing an expected beta freeze")
    args = parser.parse_args()
    try:
        if args.action == "prepare":
            prepare()
        elif args.action == "manifest":
            candidate_manifest()
        elif args.action == "current":
            try:
                current(json.loads(os.environ["SOURCE_JSON"]), server=args.platform == "server")
            except ReleasePaused:
                if not args.skip_paused:
                    raise
                emit({"enabled": "false"})
                report_pause()
            else:
                emit({"enabled": "true"})
        elif args.action == "record":
            write_record(json.loads(os.environ["SOURCE_JSON"]), args.platform)
        elif args.action == "ci":
            if not SHA.fullmatch(args.sha or "") or not SHA.fullmatch(args.ci_head or args.sha):
                raise ReleaseError("CI requires full commit SHAs")
            wait_ci(args.sha, args.ci_head or args.sha, args.minutes)
        elif args.action == "promote-server":
            promote_server(json.loads(Path("release.json").read_text()))
        else:
            if args.action == "server":
                tag = os.environ["GITHUB_REF_NAME"]
                if not tag.startswith("v") or not VERSION.fullmatch(tag[1:]):
                    raise ReleaseError("Stable server releases require vX.Y.Z tags")
                manifest = find_server(os.environ["GITHUB_SHA"], tag[1:])
                record = manifest["components"]["server"]
                current(record)
                Path("release-candidate.json").write_text(json.dumps(manifest, indent=2) + "\n")
            else:
                record = select_record(args.platform, args.run_id)
            Path("release.json").write_text(json.dumps(record, indent=2) + "\n")
            emit({**{k: record[k] for k in ("sha", "version", "ref")},
                  "build_number": record.get("build_number", ""), "digest": record.get("digest", "")})
    except (ReleaseError, KeyError, ValueError, zipfile.BadZipFile) as error:
        sys.exit(f"error: {error}")


if __name__ == "__main__":
    main()
