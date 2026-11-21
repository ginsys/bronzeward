#!/usr/bin/env bash
# Fixture tests for scripts/ci/report-pin-drift.sh, the `check` job of previous-major-pins.yml, with a
# stub `gh` that serves a fixed issue list and records what the script asks it to do. Drift (STATUS 1)
# opens one issue or comments on the open one (never edits its body); no drift (0) closes it; a source that could not be read (2)
# touches nothing, and never closes the issue.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
script="$here/../ci/report-pin-drift.sh"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir "$work/bin"
cat >"$work/bin/gh" <<'STUB'
#!/usr/bin/env bash
# gh api --paginate --slurp: print $LIST, an array of pages (or fail); gh issue create|comment|edit|close:
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

diff_text=$'chrome: 156.0.8078.80 -> 157.0.8103.78\nedge.debSha256: aaaa -> cccc'

# run STATUS LIST: the script's exit code goes to $rc, what gh was asked to to $work/calls.
run() {
  rm -f "$work/calls" "$work"/args-*
  rc=0
  PATH="$work/bin:$PATH" WORK=$work LIST=$2 REPO=o/r STATUS=$1 DIFF=$diff_text \
    RUN_URL=https://example.test/run/1 EVENT=schedule SHA=abc1234 "$script" >/dev/null 2>&1 || rc=$?
  touch "$work/calls"
  # The body of an issue is never edited, whatever the case.
  [ "$(grep -c '^issue edit$' "$work/calls" || true)" -eq 0 ] || bad "gh issue edit was called"
}
calls() { grep -c "^$1\$" "$work/calls" || true; }

# Each LIST is what `gh api --paginate --slurp` prints: an array of pages, each an array of issues.
real='[[{"number":7,"body":"x\n<!-- bronzeward-key: previous-major-pins -->"}]]'
other='[[{"number":3,"body":"<!-- bronzeward-key: other -->"},{"number":4,"body":"no marker"},{"number":5,"body":null}]]'
twice='[[{"number":12,"body":"<!-- bronzeward-key: previous-major-pins -->"}],[{"number":7,"body":"<!-- bronzeward-key: previous-major-pins -->"}]]'
second_page='[[{"number":3,"body":"a"}],[{"number":8,"body":"<!-- bronzeward-key: previous-major-pins -->"}]]'
a_pull='[[{"number":6,"pull_request":{},"body":"<!-- bronzeward-key: previous-major-pins -->"}]]'
test_open='[[{"number":9,"body":"<!-- bronzeward-key: previous-major-pins-test -->"}]]'

# Drift with no open issue: one is created with its labels, the marker, the pins, the command and the run.
run 1 '[[]]'
if [ "$rc" -eq 0 ] && [ "$(calls 'issue create')" -eq 1 ] && [ "$(calls 'issue edit')" -eq 0 ] && [ "$(calls 'issue close')" -eq 0 ]; then ok; else bad "drift with no open issue did not create exactly one"; fi
if grep -q 'bronzeward-key: previous-major-pins -->' "$work/args-create" && grep -q 'https://example.test/run/1' "$work/args-create" \
  && grep -q '^type/implementation$' "$work/args-create" && grep -q '^area/ci$' "$work/args-create" \
  && grep -q 'chrome: 156.0.8078.80 -> 157.0.8103.78' "$work/args-create" && grep -q 'edge.debSha256: aaaa -> cccc' "$work/args-create" \
  && grep -q 'previous-major-pins.py --write web/e2e/previous-majors.json' "$work/args-create"; then ok; else bad "the new issue lacks its marker, labels, pins, command or run URL"; fi

# The body of a new issue says that a later comment carries newer pins, since it is written once.
if grep -q 'A later run adds a comment with the newer pins' "$work/args-create"; then ok; else bad "the new issue does not say that later comments supersede it"; fi

# Drift with the issue open: one comment on it with the pins, the apply command and the run, no second
# issue, and the body is never edited (a person may have written in it).
run 1 "$real"
if [ "$rc" -eq 0 ] && [ "$(calls 'issue create')" -eq 0 ] && [ "$(calls 'issue edit')" -eq 0 ] && [ "$(calls 'issue comment')" -eq 1 ] && grep -qx 7 "$work/args-comment" \
  && grep -q 'chrome: 156.0.8078.80 -> 157.0.8103.78' "$work/args-comment" && grep -q 'edge.debSha256: aaaa -> cccc' "$work/args-comment" \
  && grep -q 'previous-major-pins.py --write web/e2e/previous-majors.json' "$work/args-comment" && grep -q 'https://example.test/run/1' "$work/args-comment"; then ok; else bad "drift with an open issue did not comment on #7 only"; fi

# Issues without the marker do not count; one on a later page does; a pull request never does.
run 1 "$other"
if [ "$rc" -eq 0 ] && [ "$(calls 'issue create')" -eq 1 ]; then ok; else bad "an issue without the marker was taken for the report"; fi
run 1 "$second_page"
if [ "$rc" -eq 0 ] && [ "$(calls 'issue create')" -eq 0 ] && grep -qx 8 "$work/args-comment"; then ok; else bad "an issue on the second page was missed"; fi
run 1 "$a_pull"
if [ "$rc" -eq 0 ] && [ "$(calls 'issue create')" -eq 1 ] && [ "$(calls 'issue edit')" -eq 0 ]; then ok; else bad "a pull request was taken for the report"; fi
run 1 "$twice"
if [ "$rc" -eq 0 ] && [ "$(calls 'issue comment')" -eq 1 ] && grep -qx 7 "$work/args-comment"; then ok; else bad "two marked issues did not go to the lower number"; fi

# No drift closes the open issue, with a comment, and opens nothing; with none open it does nothing.
run 0 "$real"
if [ "$rc" -eq 0 ] && [ "$(calls 'issue close')" -eq 1 ] && grep -qx 7 "$work/args-close" && [ "$(calls 'issue create')" -eq 0 ] && [ "$(calls 'issue edit')" -eq 0 ]; then ok; else bad "no drift did not close #7 only"; fi
run 0 "$other"
if [ "$rc" -eq 0 ] && [ "$(calls 'issue close')" -eq 0 ] && [ "$(calls 'issue create')" -eq 0 ]; then ok; else bad "no drift with no marked issue changed something"; fi

# A source that could not be read (2) touches nothing, with the issue open or not, and never calls gh.
for list in '[[]]' "$real"; do
  run 2 "$list"
  if [ "$rc" -eq 0 ] && [ ! -s "$work/calls" ]; then ok; else bad "STATUS 2 called gh"; fi
done

# Drift with no pin named is a crash that exited 1: an error, and nothing is created, edited or closed.
for list in '[[]]' "$real"; do
  rc=0
  rm -f "$work/calls" "$work"/args-*
  PATH="$work/bin:$PATH" WORK=$work LIST=$list REPO=o/r STATUS=1 DIFF='' RUN_URL=u EVENT=e SHA=s "$script" >/dev/null 2>&1 || rc=$?
  touch "$work/calls"
  if [ "$rc" -ne 0 ] && [ ! -s "$work/calls" ]; then ok; else bad "STATUS 1 with an empty DIFF reported"; fi
done

# Any other status is an error and calls nothing.
run 3 "$real"
if [ "$rc" -ne 0 ] && [ ! -s "$work/calls" ]; then ok; else bad "an unknown STATUS did not stop the script"; fi

# A test of the job uses its own marker and a "[test] " title, and never touches the real issue.
rc=0
rm -f "$work/calls" "$work"/args-*
PATH="$work/bin:$PATH" WORK=$work LIST=$real REPO=o/r STATUS=1 DIFF=$diff_text KEY=previous-major-pins-test TITLE_PREFIX='[test] ' \
  RUN_URL=u EVENT=e SHA=s "$script" >/dev/null 2>&1 || rc=$?
touch "$work/calls"
if [ "$rc" -eq 0 ] && [ "$(calls 'issue create')" -eq 1 ] && [ "$(calls 'issue edit')" -eq 0 ] \
  && grep -q 'bronzeward-key: previous-major-pins-test -->' "$work/args-create" && grep -q '^\[test\] The previous-major browser pins are behind$' "$work/args-create"; then ok; else bad "a test issue joined the real one or lacks its marker or title"; fi
rc=0
rm -f "$work/calls" "$work"/args-*
PATH="$work/bin:$PATH" WORK=$work LIST=$test_open REPO=o/r STATUS=0 DIFF='' KEY=previous-major-pins-test \
  RUN_URL=u EVENT=e SHA=s "$script" >/dev/null 2>&1 || rc=$?
touch "$work/calls"
if [ "$rc" -eq 0 ] && [ "$(calls 'issue close')" -eq 1 ] && grep -qx 9 "$work/args-close"; then ok; else bad "a test run did not close its own issue #9"; fi

# A lookup that fails creates nothing and fails the script.
rc=0
rm -f "$work/calls"
PATH="$work/bin:$PATH" WORK=$work LIST_FAILS=1 LIST='[[]]' REPO=o/r STATUS=1 DIFF=d RUN_URL=u EVENT=e SHA=s "$script" >/dev/null 2>&1 || rc=$?
touch "$work/calls"
if [ "$rc" -ne 0 ] && [ "$(calls 'issue create')" -eq 0 ]; then ok; else bad "a failed lookup went on to create an issue"; fi

# An unset value never reports.
for unset_var in REPO STATUS RUN_URL EVENT SHA; do
  rc=0
  rm -f "$work/calls"
  (
    export PATH="$work/bin:$PATH" WORK=$work LIST='[[]]' REPO=o/r STATUS=1 DIFF=d RUN_URL=u EVENT=e SHA=s
    unset "$unset_var"
    "$script" >/dev/null 2>&1
  ) || rc=$?
  if [ "$rc" -ne 0 ] && [ ! -e "$work/calls" ]; then ok; else bad "unset $unset_var did not stop the script"; fi
done

echo "report-pin-drift-test: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
