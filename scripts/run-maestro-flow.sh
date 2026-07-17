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

[[ $# -eq 1 ]] || die "usage: scripts/run-maestro-flow.sh FLOW.yaml"
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

relative="${flow#"${ROOT_DIR}/e2e/maestro/flows/"}"
slug="${relative%.yaml}"
slug="${slug//\//-}"
run_id="$(date -u +%Y%m%dT%H%M%SZ)-$$"
artifacts="${ROOT_DIR}/e2e/maestro/.artifacts/${slug}/${run_id}"
mkdir -p "${artifacts}/debug"
chmod 0700 "${ROOT_DIR}/e2e/maestro/.artifacts" "${artifacts}" "${artifacts}/debug"

artifacts_sanitized=0

sanitize_or_remove_artifacts() {
  if python3 "${SCRIPT_DIR}/redact_maestro.py" tree "${artifacts}"; then
    artifacts_sanitized=1
    return 0
  fi
  rm -rf -- "${artifacts}"
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
  exit "${status}"
}

trap cleanup_artifacts EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

printf 'Running %s against the private loopback lab as %s\n' "${relative}" "${MAESTRO_USERNAME}"
set +e
maestro --platform=web --no-ansi test \
  --config="${ROOT_DIR}/e2e/maestro/config.yaml" \
  --headless \
  --screen-size=500x1400 \
  --format=JUNIT \
  --output="${artifacts}/report.xml" \
  --test-output-dir="${artifacts}/debug" \
  "${flow}" \
  2>&1 | python3 "${SCRIPT_DIR}/redact_maestro.py" stream
statuses=("${PIPESTATUS[@]}")
set -e

sanitize_or_remove_artifacts || {
  die "artifacts could not be safely redacted and were deleted"
}

[[ "${statuses[1]}" -eq 0 ]] || die "console redaction failed"
[[ "${statuses[0]}" -eq 0 ]] || die "${relative} failed; redacted artifacts: ${artifacts}"
printf 'Passed %s; redacted JUnit/artifacts: %s\n' "${relative}" "${artifacts}"
