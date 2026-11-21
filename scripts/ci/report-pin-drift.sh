#!/usr/bin/env bash
# The `check` job of previous-major-pins.yml (ginsys/bronzeward#907): keeps one issue that names the
# previous-major browser pins web/e2e/previous-majors.json should hold. STATUS is the exit status of
# `previous-major-pins.py --check` and DIFF its output: 1 (drift) opens the issue, or comments on the
# open one (its body is written once and never edited); 0 (no drift) closes the open one; 2 (a source
# could not be read) touches nothing, so a vendor outage never reads as "no drift" and never closes
# the issue.
#
# The open issue is found by its marker, as scripts/ci/report-browser-run.sh finds its own: a failed
# gh or jq stops the script before anything is created. KEY and TITLE_PREFIX let a test of the job use
# its own marker and a "[test] " title, so it never touches the real issue.
#
# Every value comes from the environment: REPO, STATUS, DIFF, RUN_URL, EVENT and SHA are required,
# KEY and TITLE_PREFIX are optional, and gh reads GH_TOKEN.
set -euo pipefail
: "${REPO:?REPO is not set}" "${STATUS:?STATUS is not set}" "${RUN_URL:?RUN_URL is not set}"
: "${EVENT:?EVENT is not set}" "${SHA:?SHA is not set}"
: "${DIFF=}"
marker="<!-- bronzeward-key: ${KEY:-previous-major-pins} -->"
title="${TITLE_PREFIX:-}The previous-major browser pins are behind"

case "$STATUS" in
  0 | 1) ;;
  2)
    echo "report-pin-drift: a source could not be read: nothing to report"
    exit 0
    ;;
  *)
    echo "report-pin-drift: unknown STATUS '$STATUS'" >&2
    exit 1
    ;;
esac

# Drift always names at least one pin. A STATUS 1 with no DIFF is a crash that exited 1, not drift.
if [ "$STATUS" = 1 ] && [ -z "$DIFF" ]; then
  echo "report-pin-drift: STATUS 1 with an empty DIFF is not drift: nothing reported" >&2
  exit 1
fi

number=$(gh api --paginate --slurp "repos/$REPO/issues?state=open&per_page=100" |
  jq -r --arg m "$marker" '[.[][] | select(.pull_request == null) | select((.body // "") | contains($m)) | .number] | min // empty')

if [ "$STATUS" = 0 ]; then
  if [ -n "$number" ]; then
    gh issue close "$number" --repo "$REPO" --comment "The pins in web/e2e/previous-majors.json match the sources again: $RUN_URL"
    echo "report-pin-drift: closed #$number"
  else
    echo "report-pin-drift: no drift, no open issue"
  fi
  exit 0
fi

note="The pins in web/e2e/previous-majors.json are behind the previous major of Chrome, Edge and Firefox:

\`\`\`
$DIFF
\`\`\`

Apply them in a pull request with

\`\`\`
python3 scripts/ci/previous-major-pins.py --write web/e2e/previous-majors.json
\`\`\`

then update the build numbers in conformance/README.md. The pull request runs the Previous majors
workflow, because the file is among its paths. This issue closes at the next weekly run, or the next
manual run on main of the Previous-major pins workflow, after the file matches the sources.

Run: $RUN_URL
Event: $EVENT
Commit: $SHA"

# The body is written once and never edited, so a person's text in it stays; a later run adds a comment.
if [ -n "$number" ]; then
  gh issue comment "$number" --repo "$REPO" --body "$note"
  echo "report-pin-drift: commented on #$number"
else
  gh issue create --repo "$REPO" --title "$title" --label type/implementation --label area/ci --body "$note

A later run adds a comment with the newer pins, and the newest comment is current.

$marker"
  echo "report-pin-drift: opened a new issue"
fi
