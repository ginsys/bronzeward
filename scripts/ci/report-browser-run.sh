#!/usr/bin/env bash
# The `report` job of safari.yml and previous-majors.yml, the browser runs outside the required
# checks: a failed run on main or on the schedule opens one issue, or comments on the one that is
# open. RESULT is the needed job's result and REF is github.ref. Only a `failure` on
# refs/heads/main reports: a run cancelled or abandoned for want of a runner, a skipped run, and a
# manual run of another branch open nothing. SIMULATED=true (the workflow_dispatch input) reports
# as if the job failed, into a separate "[test]" issue, so a test of this job never joins a real
# report.
#
# The open issue is found by a stable marker in its body, not by a label (labels are managed
# elsewhere and an unregistered one is deleted) and not by search (its index lags, so two reports
# a minute apart could both create). `gh issue list` reads the tracker directly. Each workflow has
# its own marker: KEY names it, SUBJECT is what the issue calls the run, and the defaults are
# Safari's.
#
# Every value comes from the environment, none from the workflow text: REPO, RESULT, REF, RUN_URL,
# EVENT and SHA are required, SIMULATED, SUBJECT and KEY are optional, and gh reads GH_TOKEN.
set -euo pipefail
: "${REPO:?REPO is not set}" "${RESULT:?RESULT is not set}" "${REF:?REF is not set}"
: "${RUN_URL:?RUN_URL is not set}" "${EVENT:?EVENT is not set}" "${SHA:?SHA is not set}"
subject=${SUBJECT:-Safari}
prefix=${KEY:-safari}

if [ "${SIMULATED:-}" = true ]; then
  key=$prefix-simulated-failure
  title="[test] $subject run failed (simulated)"
  note=$'This is a simulated failure, made to test the report job. Close this issue.\n\n'
elif [ "$RESULT" = failure ] && [ "$REF" = refs/heads/main ]; then
  key=$prefix-main-failure
  title="$subject run failed on main"
  note=
else
  echo "report-browser-run: $subject result is $RESULT on $REF: nothing to report"
  exit 0
fi

marker="<!-- bronzeward-key: $key -->"
facts=$(printf 'Run: %s\nEvent: %s\nCommit: %s' "$RUN_URL" "$EVENT" "$SHA")

# A failure of gh or jq stops the script here, so a broken lookup never creates a second issue.
# --paginate reads every page of open issues, so the marker is found however many are open. The
# issues endpoint lists pull requests too: they have a pull_request key and are skipped.
number=$(gh api --paginate --slurp "repos/$REPO/issues?state=open&per_page=100" |
  jq -r --arg m "$marker" '[.[][] | select(.pull_request == null) | select((.body // "") | contains($m)) | .number] | min // empty')

if [ -n "$number" ]; then
  gh issue comment "$number" --repo "$REPO" --body "${note}The $subject run failed again.

$facts"
  echo "report-browser-run: commented on #$number"
else
  gh issue create --repo "$REPO" --title "$title" --label type/implementation --label area/ci --body "${note}The $subject browser run failed.

$facts

$subject does not block pull requests (ginsys/bronzeward#612). A failed run on main or on the daily
schedule is reported here. A later failed run adds a comment. Close this issue when $subject passes.

$marker"
  echo "report-browser-run: opened a new issue"
fi
