#!/usr/bin/env bash
# ci.yml's `checks` job: passes only when every needed job succeeded. RESULTS is
# join(needs.*.result, ' '), one word per needed job as GitHub reports it, so no job output can
# pose as a result; COMMIT_LINT is needs.commit-lint.result and EVENT the triggering event.
# commit-lint runs only on pull requests (its own `if:`), so one `skipped`, its own, is accepted
# elsewhere. Any other result fails, including one GitHub adds later (a hosted-runner outage
# reports `abandoned`), and so do empty results.
set -euo pipefail
: "${EVENT:?EVENT is not set}" "${COMMIT_LINT:?COMMIT_LINT is not set}"
read -r -a results <<<"${RESULTS:-}"
[ "${#results[@]}" -gt 0 ] || { echo "checks: no job results" >&2; exit 1; }
skip_allowed=0
if [ "$COMMIT_LINT" = skipped ] && [ "$EVENT" != pull_request ]; then
  skip_allowed=1
fi
bad=0
for result in "${results[@]}"; do
  case $result in
    success) ;;
    skipped)
      if [ "$skip_allowed" -eq 1 ]; then
        skip_allowed=0
      else
        echo "checks: a job was skipped" >&2
        bad=1
      fi
      ;;
    *)
      echo "checks: a job ended $result" >&2
      bad=1
      ;;
  esac
done
exit "$bad"
