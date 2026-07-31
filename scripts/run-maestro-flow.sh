#!/usr/bin/env bash

set -Eeuo pipefail
umask 077

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
readonly SCRIPT_DIR
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd -P)"
readonly ROOT_DIR
readonly TESTED_MAESTRO_VERSION="2.6.1"

die() {
  printf 'maestro flow failed: %s\n' "$*" >&2
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

artifacts_root=""
if [[ "${1:-}" == "--artifacts-root" ]]; then
  [[ $# -ge 3 ]] || die "--artifacts-root requires a directory and flow"
  artifacts_root="$2"
  shift 2
fi
[[ $# -eq 1 ]] || die "usage: scripts/run-maestro-flow.sh [--artifacts-root DIR] FLOW.yaml"
flow_dir="$(cd "$(dirname "$1")" 2>/dev/null && pwd -P)" || die "flow directory does not exist"
flow="${flow_dir}/$(basename "$1")"
[[ -f "${flow}" ]] || die "flow does not exist: ${flow}"
[[ ! -L "${flow}" ]] || die "flow must not be a symlink"
[[ "${flow}" == "${ROOT_DIR}/e2e/maestro/flows/"* ]] || die "flow must live under e2e/maestro/flows"
python3 "${SCRIPT_DIR}/check_test_automation.py" --flow "${flow}" >/dev/null ||
  die "flow failed the local safety validator"

for variable in MAESTRO_SERVER_URL MAESTRO_USERNAME MAESTRO_PASSWORD; do
  [[ -n "${!variable:-}" ]] || die "${variable} was not supplied by the lab wrapper"
done
python3 "${SCRIPT_DIR}/maestro_safety.py" \
  --config "${ROOT_DIR}/e2e/maestro/config.yaml" \
  -- "${MAESTRO_SERVER_URL}" ||
  die "Maestro URL/config safety validation failed"

relative="${flow#"${ROOT_DIR}/e2e/maestro/flows/"}"
slug="${relative%.yaml}"
slug="${slug//\//-}"
run_id="$(date -u +%Y%m%dT%H%M%SZ)-$$"
if [[ -n "${artifacts_root}" ]]; then
  [[ -d "${artifacts_root}" && ! -L "${artifacts_root}" ]] ||
    die "suite artifact root must be an existing real directory"
  assert_no_symlink_components "${artifacts_root}"
  artifacts_root="$(cd "${artifacts_root}" && pwd -P)"
  case "${artifacts_root}" in
    "${ROOT_DIR}/e2e/maestro/.artifacts/suites/"*"/raw") ;;
    *) die "suite artifact root is outside the private Maestro suite tree" ;;
  esac
  artifacts="${artifacts_root}/${slug}"
  [[ ! -e "${artifacts}" && ! -L "${artifacts}" ]] ||
    die "suite artifact directory already exists: ${slug}"
else
  artifacts="${ROOT_DIR}/e2e/maestro/.artifacts/${slug}/${run_id}"
fi
assert_no_symlink_components "${artifacts}"
mkdir -p "${artifacts}/debug"
assert_no_symlink_components "${artifacts}/debug"
chmod 0700 "${ROOT_DIR}/e2e/maestro/.artifacts" "${artifacts}" "${artifacts}/debug"
artifacts_physical="$(cd "${artifacts}" && pwd -P)" ||
  die "artifact directory could not be resolved"
[[ "${artifacts_physical}" == "${artifacts}" ]] ||
  die "artifact directory crossed a symlink boundary"
readonly artifacts_physical

artifacts_sanitized=0

remove_native_debug() {
  local current_physical
  [[ -d "${artifacts}" && ! -L "${artifacts}" ]] || return 1
  current_physical="$(cd "${artifacts}" && pwd -P)" || return 1
  [[ "${current_physical}" == "${artifacts_physical}" ]] || return 1
  rm -rf -- "${artifacts}/debug" || return 1
  rm -f -- "${artifacts}/console.log" || return 1
  [[ ! -e "${artifacts}/debug" && ! -L "${artifacts}/debug" ]] || return 1
  [[ ! -e "${artifacts}/console.log" && ! -L "${artifacts}/console.log" ]] || return 1
}

remove_artifact_tree() {
  local current_physical
  [[ -d "${artifacts}" && ! -L "${artifacts}" ]] || return 1
  current_physical="$(cd "${artifacts}" && pwd -P)" || return 1
  [[ "${current_physical}" == "${artifacts_physical}" ]] || return 1
  rm -rf -- "${artifacts}"
}

validate_junit() {
  python3 - "${artifacts}/report.xml" "$1" <<'PY'
import pathlib
import stat
import sys
import xml.etree.ElementTree as ET

path = pathlib.Path(sys.argv[1])
expected_success = sys.argv[2] == "0"
try:
    metadata = path.lstat()
    if path.is_symlink() or not stat.S_ISREG(metadata.st_mode):
        raise ValueError("result is not a regular file")
    if metadata.st_size <= 0 or metadata.st_size > 10 * 1024 * 1024:
        raise ValueError("result has an invalid size")
    payload = path.read_bytes()
    if b"<!DOCTYPE" in payload.upper() or b"<!ENTITY" in payload.upper():
        raise ValueError("DTD and entity declarations are forbidden")
    root = ET.fromstring(payload)
    local_name = lambda element: element.tag.rsplit("}", 1)[-1]
    if local_name(root) not in {"testsuite", "testsuites"}:
        raise ValueError("root is not testsuite/testsuites")
    testcases = [element for element in root.iter() if local_name(element) == "testcase"]
    if not testcases:
        raise ValueError("result has no test cases")
    if expected_success:
        for suite in (element for element in root.iter() if local_name(element) == "testsuite"):
            for field in ("failures", "errors", "skipped"):
                if int(suite.attrib.get(field, "0")) != 0:
                    raise ValueError(f"passing result declares {field}")
        for testcase in testcases:
            status_name = testcase.attrib.get("status", "").upper()
            if status_name in {"ERROR", "FAIL", "FAILED", "SKIP", "SKIPPED"}:
                raise ValueError("passing result contains a non-passing test case")
            if any(
                local_name(child) in {"error", "failure", "skipped"}
                for child in testcase
            ):
                raise ValueError("passing result contains failure/skip details")
except (OSError, ET.ParseError, ValueError) as exc:
    print(f"invalid Maestro JUnit: {exc}", file=sys.stderr)
    raise SystemExit(1)
PY
}

sanitize_or_remove_artifacts() {
  local current_physical
  [[ -d "${artifacts}" && ! -L "${artifacts}" ]] || return 1
  current_physical="$(cd "${artifacts}" && pwd -P)" || return 1
  [[ "${current_physical}" == "${artifacts_physical}" ]] || return 1
  if python3 "${SCRIPT_DIR}/redact_maestro.py" tree "${artifacts}"; then
    if printf 'sanitized\n' >"${artifacts}/.sanitized"; then
      chmod 0600 "${artifacts}/.sanitized"
      artifacts_sanitized=1
      return 0
    fi
  fi
  if [[ -d "${artifacts}" && ! -L "${artifacts}" ]]; then
    current_physical="$(cd "${artifacts}" && pwd -P)" || return 1
    if [[ "${current_physical}" == "${artifacts_physical}" ]]; then
      rm -rf -- "${artifacts}"
    fi
  fi
  return 1
}

cleanup_artifacts() {
  local status=$?
  trap - EXIT INT TERM
  set +e
  if [[ "${artifacts_sanitized}" -eq 0 && -e "${artifacts}" ]]; then
    if ! sanitize_or_remove_artifacts; then
      printf 'maestro flow warning: interrupted artifacts could not be redacted and were removed\n' >&2
    fi
  fi
  if [[ "${artifacts_sanitized}" -eq 1 && -e "${artifacts}" ]] && ! remove_native_debug; then
    remove_artifact_tree || true
    printf 'maestro flow warning: native debug cleanup failed; artifact-tree removal attempted\n' >&2
  fi
  exit "${status}"
}

trap cleanup_artifacts EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

java_major_version() {
  command -v java >/dev/null 2>&1 || return 1
  java -version 2>&1 \
    | sed -nE 's/.*version "([0-9]+).*/\1/p' \
    | head -n 1
}

java_is_supported() {
  local major
  major="$(java_major_version)" || return 1
  case "${major}" in
    17 | 21) return 0 ;;
    *) return 1 ;;
  esac
}

if ! java_is_supported; then
  if command -v brew >/dev/null 2>&1; then
    for formula in openjdk@21 openjdk@17; do
      if prefix="$(brew --prefix "${formula}" 2>/dev/null)"; then
        export JAVA_HOME="${prefix}/libexec/openjdk.jdk/Contents/Home"
        export PATH="${JAVA_HOME}/bin:${PATH}"
        break
      fi
    done
  fi
fi
java_major="$(java_major_version || true)"
case "${java_major}" in
  17 | 21) ;;
  *) die "Java 17 or 21 is required by Maestro (found ${java_major:-unknown})" ;;
esac
if ! command -v maestro >/dev/null 2>&1 && [[ -x "${HOME}/.maestro/bin/maestro" ]]; then
  export PATH="${HOME}/.maestro/bin:${PATH}"
fi
command -v maestro >/dev/null 2>&1 ||
  die "Maestro ${TESTED_MAESTRO_VERSION} is required; see docs/testing/automation.md"

export MAESTRO_CLI_NO_ANALYTICS=true
export MAESTRO_CLI_ANALYSIS_NOTIFICATION_DISABLED=true
export MAESTRO_DISABLE_UPDATE_CHECK=true

maestro_version="$(maestro --version 2>/dev/null | sed -nE 's/.*([0-9]+\.[0-9]+\.[0-9]+).*/\1/p' | head -n 1)"
[[ -n "${maestro_version}" ]] || die "could not determine the Maestro version"
[[ "${maestro_version}" == "${TESTED_MAESTRO_VERSION}" ]] ||
  die "Maestro ${TESTED_MAESTRO_VERSION} is required (found ${maestro_version})"

printf 'Running %s against the private loopback lab as %s\n' "${relative}" "${MAESTRO_USERNAME}"
set +e
maestro --platform=web --no-ansi test \
  --config="${ROOT_DIR}/e2e/maestro/config.yaml" \
  --headless \
  --screen-size=500x1400 \
  --format=JUNIT \
  --output="${artifacts}/report.xml" \
  --test-output-dir="${artifacts}/debug" \
  --debug-output="${artifacts}/debug" \
  --flatten-debug-output \
  "${flow}" \
  2>&1 \
  | python3 "${SCRIPT_DIR}/redact_maestro.py" stream \
  | tee "${artifacts}/console.log"
statuses=("${PIPESTATUS[@]}")
set -e

sanitize_or_remove_artifacts || {
  die "artifacts could not be safely redacted and were deleted"
}

[[ "${statuses[1]}" -eq 0 ]] || die "console redaction failed"
[[ "${statuses[2]}" -eq 0 ]] || die "redacted console log could not be written"
remove_native_debug || {
  remove_artifact_tree || true
  die "native debug output could not be removed; artifact-tree removal attempted"
}
validate_junit "${statuses[0]}" || {
  remove_artifact_tree || true
  die "Maestro did not produce a valid JUnit result"
}
[[ "${statuses[0]}" -eq 0 ]] || die "${relative} failed; JUnit result: ${artifacts}/report.xml"
printf 'Passed %s; JUnit result: %s\n' "${relative}" "${artifacts}/report.xml"
