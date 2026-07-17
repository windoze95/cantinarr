#!/usr/bin/env bash

set -Eeuo pipefail
umask 077

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
readonly SCRIPT_DIR
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd -P)"
readonly ROOT_DIR
FLOW="${ROOT_DIR}/e2e/maestro/flows/auth/password-login.yaml"
readonly FLOW

fail() {
  printf 'maestro runner test failed: %s\n' "$*" >&2
  exit 1
}

sandbox="$(mktemp -d "${TMPDIR:-/tmp}/cantinarr-maestro-runner-test.XXXXXX")"
suite_run=''
mismatch_suite_run=''
cleanup() {
  rm -rf -- "${sandbox}"
  case "${suite_run}" in
    "${ROOT_DIR}/e2e/maestro/.artifacts/suites/smoke/"*) rm -rf -- "${suite_run}" ;;
  esac
  case "${mismatch_suite_run}" in
    "${ROOT_DIR}/e2e/maestro/.artifacts/suites/smoke/"*) rm -rf -- "${mismatch_suite_run}" ;;
  esac
}
trap cleanup EXIT

expect_url_rejected() {
  local value="$1"
  local output
  local status
  set +e
  output="$(
    MAESTRO_SERVER_URL="${value}" \
      MAESTRO_USERNAME=lab-admin-b \
      MAESTRO_PASSWORD=test-secret \
      /bin/bash "${ROOT_DIR}/scripts/run-maestro-flow.sh" "${FLOW}" 2>&1
  )"
  status=$?
  set -e
  [[ "${status}" -ne 0 ]] || fail "unsafe URL was accepted: ${value}"
  [[ "${output}" == *"invalid Maestro loopback URL"* ]] ||
    fail "unsafe URL did not fail at the loopback guard: ${value}"
}

expect_url_rejected "http://127.0.0.1:18585@evil.example"
expect_url_rejected "http://example.invalid:18585"
expect_url_rejected "https://127.0.0.1:18585"
expect_url_rejected "http://127.0.0.1:18585/path"
expect_url_rejected "-h"
expect_url_rejected "--help"

suite_tmp="${sandbox}/suite-tmp"
mkdir -p "${suite_tmp}"
list_output="$(
  CANTINARR_LAB_DIR="${sandbox}/missing-lab" TMPDIR="${suite_tmp}" \
    /bin/bash "${ROOT_DIR}/scripts/run-maestro-lab.sh" --list
)"
[[ "${list_output}" == *"Suite smoke:"* ]] || fail "suite list output is missing"
for expected in \
  'lab-admin-b      e2e/maestro/flows/auth/password-login.yaml' \
  'lab-admin-b      e2e/maestro/flows/navigation/admin-modules.yaml' \
  'lab-no-grants    e2e/maestro/flows/authorization/requester-navigation.yaml'; do
  [[ "${list_output}" == *"${expected}"* ]] || fail "suite list is missing ${expected}"
done
[[ -z "$(find "${suite_tmp}" -mindepth 1 -print -quit)" ]] ||
  fail "private suite map was not removed"

unknown_tmp="${sandbox}/unknown-tmp"
mkdir -p "${unknown_tmp}"
set +e
CANTINARR_LAB_DIR="${sandbox}/missing-lab" TMPDIR="${unknown_tmp}" \
  /bin/bash "${ROOT_DIR}/scripts/run-maestro-lab.sh" --suite missing --list \
  >/dev/null 2>&1
unknown_status=$?
set -e
[[ "${unknown_status}" -ne 0 ]] || fail "unknown suite unexpectedly succeeded"
[[ -z "$(find "${unknown_tmp}" -mindepth 1 -print -quit)" ]] ||
  fail "unknown suite left its private suite map behind"

race_tmp="${sandbox}/race-tmp"
victim="${sandbox}/mktemp-victim"
gate="${sandbox}/mktemp-gate"
mkdir -p "${race_tmp}"
printf 'do-not-truncate\n' >"${victim}"
mkfifo "${gate}"
CANTINARR_LAB_DIR="${sandbox}/missing-lab" TMPDIR="${race_tmp}" \
  /bin/bash -c 'read -r _; exec /bin/bash "$1" --list' _ \
  "${ROOT_DIR}/scripts/run-maestro-lab.sh" <"${gate}" >/dev/null &
list_pid=$!
ln -s "${victim}" "${race_tmp}/cantinarr-maestro-suite-${list_pid}"
printf 'continue\n' >"${gate}"
wait "${list_pid}"
[[ "$(cat "${victim}")" == "do-not-truncate" ]] ||
  fail "suite-map creation followed a predictable symlink"
rm -f "${race_tmp}/cantinarr-maestro-suite-${list_pid}"

fixture="${sandbox}/fixture"
fake_bin="${sandbox}/bin"
fixture_flow="${fixture}/e2e/maestro/flows/auth/password-login.yaml"
mkdir -p "${fixture}/scripts" "$(dirname "${fixture_flow}")" "${fake_bin}"
cp "${ROOT_DIR}/scripts/run-maestro-flow.sh" \
  "${ROOT_DIR}/scripts/check_test_automation.py" \
  "${ROOT_DIR}/scripts/redact_maestro.py" \
  "${ROOT_DIR}/scripts/maestro_safety.py" \
  "${fixture}/scripts/"
cp "${FLOW}" "${fixture_flow}"
cp "${ROOT_DIR}/e2e/maestro/config.yaml" "${fixture}/e2e/maestro/config.yaml"

outside_flow="${sandbox}/outside-flow.yaml"
printf '%s\n' '- launchApp' >"${outside_flow}"
symlink_flow="${fixture}/e2e/maestro/flows/auth/symlink.yaml"
ln -s "${outside_flow}" "${symlink_flow}"
set +e
symlink_output="$(
  /bin/bash "${fixture}/scripts/run-maestro-flow.sh" "${symlink_flow}" 2>&1
)"
symlink_status=$?
set -e
[[ "${symlink_status}" -ne 0 ]] || fail "symlinked flow was accepted"
[[ "${symlink_output}" == *"flow must not be a symlink"* ]] ||
  fail "symlinked flow did not fail at containment check"

cat >"${fake_bin}/java" <<'EOF'
#!/usr/bin/env bash
printf 'openjdk version "21.0.1"\n' >&2
EOF
cat >"${fake_bin}/maestro" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
if [[ "${1:-}" == "--version" ]]; then
  printf 'Maestro CLI 2.6.1\n'
  exit 0
fi
output=''
debug=''
flow="${!#}"
for argument in "$@"; do
  case "${argument}" in
    --output=*) output="${argument#--output=}" ;;
    --test-output-dir=*) debug="${argument#--test-output-dir=}" ;;
  esac
done
[[ -n "${output}" && -n "${debug}" ]]
mkdir -p "$(dirname "${output}")" "${debug}"
if [[ "${FAKE_JUNIT_ERROR:-0}" -eq 1 ]]; then
  printf '<testsuites><testsuite tests="1" failures="1" time="1.0"><testcase name="Fake" status="ERROR" time="1.0"><failure>failed</failure></testcase></testsuite></testsuites>\n' >"${output}"
else
  printf '<testsuites><testsuite tests="1" failures="0" time="1.0"><testcase name="Fake" status="SUCCESS" time="1.0"><system-out>%s</system-out></testcase></testsuite></testsuites>\n' \
    "${MAESTRO_PASSWORD}" >"${output}"
fi
printf '{"password":"%s"}\n' "${MAESTRO_PASSWORD}" >"${debug}/commands.json"
printf 'fake Maestro output contains %s\n' "${MAESTRO_PASSWORD}"
if [[ -n "${FAKE_SCREENSHOT:-}" ]]; then
  evidence="$(sed -nE 's/^- takeScreenshot: (evidence-[a-z0-9-]+)$/\1/p' "${flow}")"
  [[ -n "${evidence}" ]]
  cp "${FAKE_SCREENSHOT}" "${debug}/${evidence}.png"
fi
if [[ "${FAKE_MAESTRO_SECRET_FILENAME:-0}" -eq 1 ]]; then
  printf 'unsafe filename\n' >"${debug}/${MAESTRO_PASSWORD}.txt"
fi
if [[ "${FAKE_MAESTRO_SECRET_DIRECTORY:-0}" -eq 1 ]]; then
  mkdir "${debug}/${MAESTRO_PASSWORD}"
fi
if [[ "${FAKE_MAESTRO_INTERRUPT:-0}" -eq 1 ]]; then
  kill -TERM "${PPID}"
  sleep 1
fi
EOF
chmod 0700 "${fake_bin}/java" "${fake_bin}/maestro"

fake_lab="${sandbox}/fake-lab"
mkdir -p "${fake_lab}/scripts"
cat >"${fake_lab}/scripts/lab" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
[[ "${1:-}" == "e2e-run" ]]
shift
user=''
while [[ $# -gt 0 ]]; do
  case "$1" in
    --user) user="$2"; shift 2 ;;
    --platform | --port) shift 2 ;;
    --reset) shift ;;
    --) shift; break ;;
    *) exit 2 ;;
  esac
done
[[ -n "${user}" && $# -gt 0 ]]
MAESTRO_SERVER_URL=http://127.0.0.1:18585 \
MAESTRO_USERNAME="${user}" \
MAESTRO_PASSWORD=fake-suite-secret \
exec "$@"
EOF
chmod 0700 "${fake_lab}/scripts/lab"
fake_screenshot="${sandbox}/evidence.png"
python3 - "${fake_screenshot}" <<'PY'
import base64
import pathlib
import sys

pathlib.Path(sys.argv[1]).write_bytes(base64.b64decode(
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
))
PY

suite_output="$(
  PATH="${fake_bin}:${PATH}" \
    FAKE_SCREENSHOT="${fake_screenshot}" \
    CANTINARR_LAB_DIR="${fake_lab}" \
    /bin/bash "${ROOT_DIR}/scripts/run-maestro-lab.sh"
)"
report_path="$(sed -n 's/^Private Markdown report: //p' <<<"${suite_output}")"
suite_run="${report_path%/report/REPORT.md}"
[[ -f "${report_path}" ]] || fail "suite did not produce a Markdown report"
[[ "$(find "$(dirname "${report_path}")/screenshots" -type f -name '*.png' | wc -l | tr -d ' ')" == 3 ]] ||
  fail "suite report did not contain three reviewed screenshots"
grep -F -q -- '> **PASS**' "${report_path}" || fail "suite report did not pass"
if grep -R -F -q -- 'fake-suite-secret' "$(dirname "${report_path}")"; then
  fail "suite report retained the raw password"
fi
[[ ! -d "$(dirname "${report_path}")/logs" ]] ||
  fail "suite report copied unrestricted console logs"

set +e
mismatch_output="$(
  PATH="${fake_bin}:${PATH}" \
    FAKE_JUNIT_ERROR=1 \
    FAKE_SCREENSHOT="${fake_screenshot}" \
    CANTINARR_LAB_DIR="${fake_lab}" \
    /bin/bash "${ROOT_DIR}/scripts/run-maestro-lab.sh" 2>&1
)"
mismatch_status=$?
set -e
[[ "${mismatch_status}" -ne 0 ]] || fail "JUnit/exit disagreement returned success"
[[ "${mismatch_output}" != *"suite passed against"* ]] ||
  fail "JUnit/exit disagreement announced a passing suite"
mismatch_report="$(sed -n 's/^Private Markdown report: //p' <<<"${mismatch_output}")"
mismatch_suite_run="${mismatch_report%/report/REPORT.md}"
[[ -f "${mismatch_report}" ]] || fail "JUnit/exit disagreement did not write its failure report"
grep -F -q -- '> **FAIL**' "${mismatch_report}" ||
  fail "JUnit/exit disagreement report did not fail"

unsupported_bin="${sandbox}/unsupported-bin"
fallback_jdk="${sandbox}/openjdk@21"
mkdir -p "${unsupported_bin}" \
  "${fallback_jdk}/libexec/openjdk.jdk/Contents/Home/bin"
cat >"${unsupported_bin}/java" <<'EOF'
#!/usr/bin/env bash
printf 'openjdk version "22.0.1"\n' >&2
EOF
cat >"${unsupported_bin}/brew" <<'EOF'
#!/usr/bin/env bash
[[ "${1:-}" == "--prefix" && "${2:-}" == "openjdk@21" ]]
printf '%s\n' "${FAKE_BREW_PREFIX}"
EOF
cat >"${fallback_jdk}/libexec/openjdk.jdk/Contents/Home/bin/java" <<'EOF'
#!/usr/bin/env bash
printf 'openjdk version "21.0.1"\n' >&2
EOF
chmod 0700 "${unsupported_bin}/java" "${unsupported_bin}/brew" \
  "${fallback_jdk}/libexec/openjdk.jdk/Contents/Home/bin/java"

PATH="${unsupported_bin}:${fake_bin}:${PATH}" \
  FAKE_BREW_PREFIX="${fallback_jdk}" \
  MAESTRO_SERVER_URL=http://127.0.0.1:18585 \
  MAESTRO_USERNAME=lab-admin-b \
  MAESTRO_PASSWORD=fallback-test-secret \
  /bin/bash "${fixture}/scripts/run-maestro-flow.sh" "${fixture_flow}" >/dev/null

secret='generated-test-password'
PATH="${fake_bin}:${PATH}" \
  MAESTRO_SERVER_URL=http://127.0.0.1:18585 \
  MAESTRO_USERNAME=lab-admin-b \
  MAESTRO_PASSWORD="${secret}" \
  /bin/bash "${fixture}/scripts/run-maestro-flow.sh" "${fixture_flow}" >/dev/null
if grep -R -F -q -- "${secret}" "${fixture}/e2e/maestro/.artifacts"; then
  fail "normal completion retained the raw password"
fi

set +e
PATH="${fake_bin}:${PATH}" \
  FAKE_MAESTRO_INTERRUPT=1 \
  MAESTRO_SERVER_URL=http://127.0.0.1:18585 \
  MAESTRO_USERNAME=lab-admin-b \
  MAESTRO_PASSWORD="${secret}" \
  /bin/bash "${fixture}/scripts/run-maestro-flow.sh" "${fixture_flow}" >/dev/null 2>&1
interrupt_status=$?
set -e
[[ "${interrupt_status}" -ne 0 ]] || fail "interrupted runner returned success"
if grep -R -F -q -- "${secret}" "${fixture}/e2e/maestro/.artifacts"; then
  fail "interrupted completion retained the raw password"
fi

set +e
filename_output="$(
  PATH="${fake_bin}:${PATH}" \
    FAKE_MAESTRO_SECRET_FILENAME=1 \
    MAESTRO_SERVER_URL=http://127.0.0.1:18585 \
    MAESTRO_USERNAME=lab-admin-b \
    MAESTRO_PASSWORD="${secret}" \
    /bin/bash "${fixture}/scripts/run-maestro-flow.sh" "${fixture_flow}" 2>&1
)"
filename_status=$?
set -e
[[ "${filename_status}" -ne 0 ]] || fail "secret-bearing artifact filename was retained"
[[ "${filename_output}" != *"${secret}"* ]] ||
  fail "secret-bearing artifact filename leaked through stderr"
if [[ "$(find "${fixture}/e2e/maestro/.artifacts" -print)" == *"${secret}"* ]]; then
  fail "failed redaction left a secret-bearing artifact path"
fi

set +e
directory_output="$(
  PATH="${fake_bin}:${PATH}" \
    FAKE_MAESTRO_SECRET_DIRECTORY=1 \
    MAESTRO_SERVER_URL=http://127.0.0.1:18585 \
    MAESTRO_USERNAME=lab-admin-b \
    MAESTRO_PASSWORD="${secret}" \
    /bin/bash "${fixture}/scripts/run-maestro-flow.sh" "${fixture_flow}" 2>&1
)"
directory_status=$?
set -e
[[ "${directory_status}" -ne 0 ]] || fail "secret-bearing artifact directory was retained"
[[ "${directory_output}" != *"${secret}"* ]] ||
  fail "secret-bearing artifact directory leaked through stderr"

printf 'maestro runner tests passed\n'
