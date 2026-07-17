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
trap 'rm -rf -- "${sandbox}"' EXIT

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
for argument in "$@"; do
  case "${argument}" in
    --output=*) output="${argument#--output=}" ;;
    --test-output-dir=*) debug="${argument#--test-output-dir=}" ;;
  esac
done
[[ -n "${output}" && -n "${debug}" ]]
mkdir -p "$(dirname "${output}")" "${debug}"
printf '<password>%s</password>\n' "${MAESTRO_PASSWORD}" >"${output}"
printf '{"password":"%s"}\n' "${MAESTRO_PASSWORD}" >"${debug}/commands.json"
if [[ "${FAKE_MAESTRO_SECRET_FILENAME:-0}" -eq 1 ]]; then
  printf 'unsafe filename\n' >"${debug}/${MAESTRO_PASSWORD}.txt"
fi
if [[ "${FAKE_MAESTRO_INTERRUPT:-0}" -eq 1 ]]; then
  kill -TERM "${PPID}"
  sleep 1
fi
EOF
chmod 0700 "${fake_bin}/java" "${fake_bin}/maestro"

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
PATH="${fake_bin}:${PATH}" \
  FAKE_MAESTRO_SECRET_FILENAME=1 \
  MAESTRO_SERVER_URL=http://127.0.0.1:18585 \
  MAESTRO_USERNAME=lab-admin-b \
  MAESTRO_PASSWORD="${secret}" \
  /bin/bash "${fixture}/scripts/run-maestro-flow.sh" "${fixture_flow}" >/dev/null 2>&1
filename_status=$?
set -e
[[ "${filename_status}" -ne 0 ]] || fail "secret-bearing artifact filename was retained"
if [[ "$(find "${fixture}/e2e/maestro/.artifacts" -print)" == *"${secret}"* ]]; then
  fail "failed redaction left a secret-bearing artifact path"
fi

printf 'maestro runner tests passed\n'
