#!/usr/bin/env bash

set -Eeuo pipefail
umask 077

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
readonly SCRIPT_DIR
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd -P)"
readonly ROOT_DIR
DEFAULT_LAB_DIR="$(cd "${ROOT_DIR}/../.." && pwd)/cantinarr-lab"
readonly TESTED_MAESTRO_VERSION="2.6.1"

suite="smoke"
reset=0
deploy=0
list_only=0

usage() {
  cat <<'EOF'
Usage: scripts/run-maestro-lab.sh [--suite smoke] [--reset] [--deploy] [--list]

Runs the selected Maestro web suite against the private disposable lab. The
lab repo defaults to ../../cantinarr-lab and can be overridden with
CANTINARR_LAB_DIR. --deploy first builds/deploys this checkout as the lab
candidate. --reset performs the lab's full volume reset once before the suite.
Every execution writes a private Markdown evidence bundle beneath
e2e/maestro/.artifacts/suites/.
EOF
}

die() {
  printf 'maestro lab suite failed: %s\n' "$*" >&2
  exit 1
}

assert_no_symlink_components() {
  local path="$1"
  local current="${path}"
  while [[ "${current}" != "${ROOT_DIR}" ]]; do
    [[ "${current}" == "${ROOT_DIR}/"* ]] ||
      die "artifact path is outside the source checkout"
    [[ ! -L "${current}" ]] ||
      die "artifact path contains a symlink"
    current="$(dirname "${current}")"
  done
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --suite)
      [[ $# -ge 2 ]] || die "--suite requires a value"
      suite="$2"
      shift 2
      ;;
    --reset)
      reset=1
      shift
      ;;
    --deploy)
      deploy=1
      shift
      ;;
    --list)
      list_only=1
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *) die "unknown option: $1" ;;
  esac
done

[[ "${suite}" =~ ^[a-z0-9]+(-[a-z0-9]+)*$ ]] ||
  die "suite name contains unsupported characters"

lab_dir="${CANTINARR_LAB_DIR:-${DEFAULT_LAB_DIR}}"
suite_file="${ROOT_DIR}/e2e/maestro/suites.json"

mapfile_cmd="$(mktemp "${TMPDIR:-/tmp}/cantinarr-maestro-suite.XXXXXX")" ||
  die "could not create the private suite map"
trap 'rm -f "${mapfile_cmd}"' EXIT
python3 - "${suite_file}" "${suite}" >"${mapfile_cmd}" <<'PY'
import json
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
suite_name = sys.argv[2]
data = json.loads(path.read_text())
suite = data.get("suites", {}).get(suite_name)
if not isinstance(suite, list) or not suite:
    raise SystemExit(f"unknown or empty Maestro suite: {suite_name}")
for item in suite:
    print(f"{item['user']}\t{item['flow']}")
PY

if [[ "${list_only}" -eq 1 ]]; then
  printf 'Suite %s:\n' "${suite}"
  while IFS=$'\t' read -r user flow; do
    printf '  %-16s %s\n' "${user}" "${flow}"
  done <"${mapfile_cmd}"
  exit 0
fi

[[ -x "${lab_dir}/scripts/lab" ]] ||
  die "private lab repo not found at ${lab_dir}; set CANTINARR_LAB_DIR"
python3 "${SCRIPT_DIR}/check_test_automation.py" >/dev/null ||
  die "Maestro suite failed the local safety validator"

artifact_root="${ROOT_DIR}/e2e/maestro/.artifacts"
suite_root="${artifact_root}/suites/${suite}"
assert_no_symlink_components "${suite_root}"
mkdir -p "${suite_root}"
assert_no_symlink_components "${suite_root}"
chmod 0700 "${ROOT_DIR}/e2e/maestro/.artifacts" \
  "${ROOT_DIR}/e2e/maestro/.artifacts/suites" "${suite_root}"
suite_run="$(mktemp -d "${suite_root}/$(date -u +%Y%m%dT%H%M%SZ).XXXXXX")" ||
  die "could not create the private suite artifact directory"
mkdir -p "${suite_run}/raw" "${suite_run}/statuses"
chmod 0700 "${suite_run}" "${suite_run}/raw" "${suite_run}/statuses"

harness_revision="$(git -C "${ROOT_DIR}" rev-parse --verify HEAD 2>/dev/null)" ||
  die "could not determine the harness revision"
harness_status="$(git -C "${ROOT_DIR}" status --porcelain)" ||
  die "could not determine the harness dirty state"
harness_dirty=false
if [[ -n "${harness_status}" ]]; then
  harness_dirty=true
fi
harness_sha256="$(python3 "${SCRIPT_DIR}/render_maestro_report.py" --print-harness-sha256)" ||
  die "could not determine the harness content hash"
deployed_this_run=false
[[ "${deploy}" -eq 1 ]] && deployed_this_run=true
reset_requested=false
[[ "${reset}" -eq 1 ]] && reset_requested=true

# A fresh loopback origin keeps Chromium from reusing a Flutter service worker
# or cached bundle from an earlier candidate deployed on the same Droplet.
e2e_port="$(python3 - <<'PY'
import secrets
import socket

for _ in range(100):
    port = 20000 + secrets.randbelow(40000)
    try:
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
            sock.bind(("127.0.0.1", port))
    except OSError:
        continue
    print(port)
    break
else:
    raise SystemExit("could not find an unused loopback port")
PY
)"

if [[ "${deploy}" -eq 1 ]]; then
  printf 'Deploying this checkout to the scoped private lab before E2E.\n'
  (
    cd "${lab_dir}"
    CANTINARR_SOURCE_DIR="${ROOT_DIR}" scripts/lab deploy
  )
fi

first=1
suite_status=0
while IFS=$'\t' read -r user flow; do
  [[ -f "${ROOT_DIR}/${flow}" ]] || die "suite flow is missing: ${flow}"
  slug="${flow#e2e/maestro/flows/}"
  slug="${slug%.yaml}"
  slug="${slug//\//-}"
  args=(e2e-run --user "${user}" --platform web --port "${e2e_port}")
  if [[ "${reset}" -eq 1 && "${first}" -eq 1 ]]; then
    args+=(--reset)
  fi
  args+=(-- "${SCRIPT_DIR}/run-maestro-flow.sh" \
    --artifacts-root "${suite_run}/raw" "${ROOT_DIR}/${flow}")
  set +e
  (
    cd "${lab_dir}"
    scripts/lab "${args[@]}"
  )
  flow_status=$?
  set -e
  printf '%s\n' "${flow_status}" >"${suite_run}/statuses/${slug}.exit"
  chmod 0600 "${suite_run}/statuses/${slug}.exit"
  first=0
  if [[ "${flow_status}" -ne 0 ]]; then
    suite_status="${flow_status}"
    break
  fi
done <"${mapfile_cmd}"

set +e
report_path="$(
  python3 "${SCRIPT_DIR}/render_maestro_report.py" \
    --suite "${suite}" \
    --run-dir "${suite_run}" \
    --harness-revision "${harness_revision}" \
    --harness-dirty "${harness_dirty}" \
    --harness-sha256 "${harness_sha256}" \
    --deployed-this-run "${deployed_this_run}" \
    --reset-requested "${reset_requested}" \
    --maestro-version "${TESTED_MAESTRO_VERSION}" \
    --platform web
)"
report_status=$?
set -e
if [[ -n "${report_path}" ]]; then
  printf 'Private Markdown report: %s\n' "${report_path}"
fi
if [[ "${report_status}" -ne 0 ]]; then
  printf 'Maestro %s suite report generation failed.\n' "${suite}" >&2
  [[ "${suite_status}" -ne 0 ]] || suite_status="${report_status}"
fi
if [[ "${suite_status}" -ne 0 ]]; then
  printf 'Maestro %s suite failed against the private disposable lab.\n' "${suite}" >&2
  exit "${suite_status}"
fi
printf 'Maestro %s suite passed against the private disposable lab.\n' "${suite}"
