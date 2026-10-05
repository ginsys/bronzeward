#!/usr/bin/env bash
# ci.yml's `checks` job: passes only when every needed job succeeded. RESULTS is toJSON(needs),
# EVENT the triggering event. commit-lint runs only on pull requests (its own `if:`), so it may be
# skipped elsewhere; any other result fails, including one GitHub adds later (a hosted-runner
# outage reports `abandoned`), and so do empty results.
set -euo pipefail
: "${EVENT:?EVENT is not set}"
[ -n "${RESULTS:-}" ] || { echo "checks: no job results" >&2; exit 1; }
printf '%s\n' "$RESULTS"
awk -v event="$EVENT" '
  # A job is a two-space-indented key opening an object; its result follows on its own line.
  match($0, /^  "[^"]+": \{/) { job = substr($0, 4, RLENGTH - 7); next }
  match($0, /"result": "[^"]*"/) {
    result = substr($0, RSTART + 11, RLENGTH - 12)
    jobs++
    if (result == "success") next
    if (result == "skipped" && job == "commit-lint" && event != "pull_request") next
    printf "checks: %s: %s\n", job, result > "/dev/stderr"
    bad++
  }
  END {
    if (jobs == 0) { print "checks: no job results" > "/dev/stderr"; exit 1 }
    exit (bad > 0)
  }
' <<<"$RESULTS"
