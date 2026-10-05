#!/usr/bin/env bash
# Fixture tests for scripts/ci/needs-check.sh: ci.yml's `checks` job passes only when every needed
# job succeeded, or commit-lint was skipped outside a pull request.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
check="$here/../ci/needs-check.sh"
pass=0
fail=0

# results JOB=RESULT... prints them as toJSON(needs) does.
results() {
  local first=1 pair
  printf '{\n'
  for pair in "$@"; do
    [ "$first" -eq 1 ] || printf ',\n'
    first=0
    printf '  "%s": {\n    "result": "%s",\n    "outputs": {}\n  }' "${pair%%=*}" "${pair#*=}"
  done
  printf '\n}\n'
}

expect() {
  local want=$1 event=$2 rc=0
  shift 2
  RESULTS=$(results "$@") EVENT=$event "$check" >/dev/null 2>&1 || rc=$?
  if [ "$rc" -eq "$want" ]; then
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    echo "FAIL: expected rc=$want got rc=$rc for $event: $*" >&2
  fi
}

expect 0 pull_request go=success go-db=success commit-lint=success
expect 0 merge_group  go=success go-db=success commit-lint=skipped
expect 0 push         go=success go-db=success commit-lint=skipped
expect 1 pull_request go=success go-db=failure commit-lint=success
expect 1 pull_request go=success go-db=cancelled commit-lint=success
expect 1 pull_request go=success go-db=abandoned commit-lint=success   # a hosted-runner outage
expect 1 merge_group  go=success go-db=timed_out commit-lint=skipped   # a result GitHub adds later
expect 1 pull_request go=success go-db=success commit-lint=skipped     # commit-lint runs on every pull request
expect 1 merge_group  go=success go-db=skipped commit-lint=skipped     # only commit-lint may be skipped
expect 1 pull_request                                                  # no results: nothing ran

# An unset or empty RESULTS never passes.
rc=0
EVENT=pull_request RESULTS='' "$check" >/dev/null 2>&1 || rc=$?
if [ "$rc" -ne 0 ]; then pass=$((pass + 1)); else fail=$((fail + 1)); echo "FAIL: empty RESULTS passed" >&2; fi

echo "needs-check-test: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
