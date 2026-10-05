#!/usr/bin/env bash
# Fixture tests for scripts/ci/needs-check.sh: ci.yml's `checks` job passes only when every needed
# job succeeded, or commit-lint was skipped outside a pull request.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
check="$here/../ci/needs-check.sh"
pass=0
fail=0

# expect RC EVENT COMMIT_LINT RESULTS: RESULTS is join(needs.*.result, ' ').
expect() {
  local want=$1 event=$2 lint=$3 results=$4 rc=0
  RESULTS=$results COMMIT_LINT=$lint EVENT=$event "$check" >/dev/null 2>&1 || rc=$?
  if [ "$rc" -eq "$want" ]; then
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    echo "FAIL: expected rc=$want got rc=$rc for $event, commit-lint $lint: $results" >&2
  fi
}

expect 0 pull_request success 'success success success'
expect 0 merge_group  skipped 'success success skipped'
expect 0 push         skipped 'skipped success success'
expect 1 pull_request success 'success failure success'
expect 1 pull_request success 'success cancelled success'
expect 1 pull_request success 'success abandoned success'   # a hosted-runner outage
expect 1 merge_group  skipped 'success timed_out skipped'   # a result GitHub adds later
expect 1 pull_request skipped 'success success skipped'     # commit-lint runs on every pull request
expect 1 merge_group  success 'success skipped success'     # only commit-lint may be skipped
expect 1 merge_group  skipped 'skipped skipped success'     # and only once
expect 1 pull_request success 'success garbage success'
expect 1 pull_request success ''                            # no results: nothing ran
expect 1 pull_request success '   '

# An unset COMMIT_LINT or EVENT never passes.
rc=0
RESULTS=success EVENT=pull_request "$check" >/dev/null 2>&1 || rc=$?
if [ "$rc" -ne 0 ]; then pass=$((pass + 1)); else fail=$((fail + 1)); echo "FAIL: unset COMMIT_LINT passed" >&2; fi
rc=0
RESULTS=success COMMIT_LINT=success "$check" >/dev/null 2>&1 || rc=$?
if [ "$rc" -ne 0 ]; then pass=$((pass + 1)); else fail=$((fail + 1)); echo "FAIL: unset EVENT passed" >&2; fi

echo "needs-check-test: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
