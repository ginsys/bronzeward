#!/usr/bin/env bash
# Fixture tests for scripts/ci/report-safari.sh, safari.yml's `report` job, with a stub `gh` that
# serves a fixed issue list and records what the script asks it to do. A failed run opens one issue,
# a second one comments on it, and a run with no runner, or a success, touches nothing.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
script="$here/../ci/report-safari.sh"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir "$work/bin"
cat >"$work/bin/gh" <<'STUB'
#!/usr/bin/env bash
# gh api --paginate --slurp: print $LIST, an array of pages (or fail); gh issue create|comment:
# record the call and its arguments.
echo "$1 $2" >>"$WORK/calls"
case "$1 $2" in
  "api --paginate")
    [ -z "${LIST_FAILS:-}" ] || exit 1
    printf '%s' "$LIST"
    ;;
  *)
    printf '%s\n' "$@" >"$WORK/args-$2"
    ;;
esac
STUB
chmod +x "$work/bin/gh"

pass=0
fail=0
ok() { pass=$((pass + 1)); }
bad() { fail=$((fail + 1)); echo "FAIL: $*" >&2; }

# run RESULT SIMULATED LIST [REF]: the script's exit code goes to $rc, what gh was asked to
# $work/calls. REF is the branch of the run and defaults to main.
run() {
  rm -f "$work/calls" "$work"/args-*
  rc=0
  PATH="$work/bin:$PATH" WORK=$work LIST=$3 REPO=o/r RESULT=$1 SIMULATED=$2 REF=${4:-refs/heads/main} \
    RUN_URL=https://example.test/run/1 EVENT=schedule SHA=abc1234 "$script" >/dev/null 2>&1 || rc=$?
  touch "$work/calls"
}
calls() { grep -c "^$1\$" "$work/calls" || true; }

# Each LIST is what `gh api --paginate --slurp` prints: an array of pages, each an array of issues.
real='[[{"number":7,"body":"x\n<!-- bronzeward-key: safari-main-failure -->"}]]'
other='[[{"number":3,"body":"<!-- bronzeward-key: other -->"},{"number":4,"body":"no marker"},{"number":5,"body":null}]]'
twice='[[{"number":12,"body":"<!-- bronzeward-key: safari-main-failure -->"}],[{"number":7,"body":"<!-- bronzeward-key: safari-main-failure -->"}]]'
second_page='[[{"number":3,"body":"a"}],[{"number":8,"body":"<!-- bronzeward-key: safari-main-failure -->"}]]'
a_pull='[[{"number":6,"pull_request":{},"body":"<!-- bronzeward-key: safari-main-failure -->"}]]'
test_open='[[{"number":9,"body":"<!-- bronzeward-key: safari-simulated-failure -->"}]]'

# No open issue: one is created, with its labels, the marker and the run URL, and nothing else.
run failure '' '[[]]'
if [ "$rc" -eq 0 ] && [ "$(calls 'issue create')" -eq 1 ] && [ "$(calls 'issue comment')" -eq 0 ]; then ok; else bad "a failure with no open issue did not create exactly one"; fi
if grep -q 'bronzeward-key: safari-main-failure' "$work/args-create" && grep -q 'https://example.test/run/1' "$work/args-create" \
  && grep -q '^type/implementation$' "$work/args-create" && grep -q '^area/ci$' "$work/args-create"; then ok; else bad "the new issue lacks its marker, run URL or labels"; fi

# An open issue with the marker: a comment on it, no second issue.
run failure '' "$real"
if [ "$rc" -eq 0 ] && [ "$(calls 'issue create')" -eq 0 ] && [ "$(calls 'issue comment')" -eq 1 ] && grep -qx 7 "$work/args-comment"; then ok; else bad "a failure with an open issue did not comment on #7 only"; fi

# Issues without the marker do not count.
run failure '' "$other"
if [ "$rc" -eq 0 ] && [ "$(calls 'issue create')" -eq 1 ]; then ok; else bad "an issue without the marker was taken for the report"; fi

# The marker on a later page is found: no second issue.
run failure '' "$second_page"
if [ "$rc" -eq 0 ] && [ "$(calls 'issue create')" -eq 0 ] && grep -qx 8 "$work/args-comment"; then ok; else bad "an issue on the second page was missed"; fi

# A pull request carrying the marker is not the report.
run failure '' "$a_pull"
if [ "$rc" -eq 0 ] && [ "$(calls 'issue create')" -eq 1 ] && [ "$(calls 'issue comment')" -eq 0 ]; then ok; else bad "a pull request was taken for the report"; fi

# A failed run of another branch (a manual run) reports nothing, also with a real issue open.
run failure '' "$real" refs/heads/feature
if [ "$rc" -eq 0 ] && [ ! -s "$work/calls" ]; then ok; else bad "a failure on another branch called gh"; fi

# Two open issues with the marker: the older number gets the comment.
run failure '' "$twice"
if [ "$rc" -eq 0 ] && [ "$(calls 'issue comment')" -eq 1 ] && grep -qx 7 "$work/args-comment"; then ok; else bad "two marked issues did not go to the lower number"; fi

# A run that never got a runner, one that was skipped, and a success open nothing.
for result in cancelled abandoned skipped success; do
  run "$result" '' '[[]]'
  if [ "$rc" -eq 0 ] && [ ! -s "$work/calls" ]; then ok; else bad "result $result called gh"; fi
done

# A simulated failure reports into its own issue, also when the job was skipped, and never into a real one.
run skipped true "$real"
if [ "$rc" -eq 0 ] && [ "$(calls 'issue create')" -eq 1 ] && grep -q 'bronzeward-key: safari-simulated-failure' "$work/args-create" \
  && grep -q '^\[test\] ' "$work/args-create"; then ok; else bad "a simulated failure joined the real issue or did not create its own"; fi
run skipped true "$test_open"
if [ "$rc" -eq 0 ] && [ "$(calls 'issue create')" -eq 0 ] && grep -qx 9 "$work/args-comment"; then ok; else bad "a second simulated failure did not comment on #9"; fi
run skipped false '[[]]'
if [ "$rc" -eq 0 ] && [ ! -s "$work/calls" ]; then ok; else bad "SIMULATED=false reported"; fi

# A lookup that fails creates nothing and fails the script.
rc=0
rm -f "$work/calls"
PATH="$work/bin:$PATH" WORK=$work LIST_FAILS=1 LIST='[[]]' REPO=o/r RESULT=failure REF=refs/heads/main RUN_URL=u EVENT=e SHA=s "$script" >/dev/null 2>&1 || rc=$?
touch "$work/calls"
if [ "$rc" -ne 0 ] && [ "$(calls 'issue create')" -eq 0 ]; then ok; else bad "a failed lookup went on to create an issue"; fi

# An unset value never reports.
for unset_var in REPO RESULT REF RUN_URL EVENT SHA; do
  rc=0
  rm -f "$work/calls"
  (
    export PATH="$work/bin:$PATH" WORK=$work LIST='[[]]' REPO=o/r RESULT=failure REF=refs/heads/main RUN_URL=u EVENT=e SHA=s
    unset "$unset_var"
    "$script" >/dev/null 2>&1
  ) || rc=$?
  if [ "$rc" -ne 0 ] && [ ! -e "$work/calls" ]; then ok; else bad "unset $unset_var did not stop the script"; fi
done

echo "report-safari-test: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
