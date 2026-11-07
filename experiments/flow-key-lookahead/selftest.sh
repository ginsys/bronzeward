#!/usr/bin/env bash
# Checks that compare.mjs fails closed. It feeds the comparison six hand-written oracle outputs and
# requires the exit status to match: 0 for a pass, and nonzero for two false refusals (a "misplaced"
# prediction and a scanner error), a missed document, an empty set and a count mismatch. It needs no go
# and no oracle. It also pins one model verdict: a document that starts with two byte order marks is "unsupported".
# Usage: selftest.sh
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/flow-key-selftest.XXXXXX") || exit 2
trap 'rm -rf "$tmp"' EXIT

bad=0
extra=()
# check <name> <want: zero|nonzero> <docs json> <real json> <patched json>
check() {
  printf '%s' "$3" > "$tmp/docs.json"
  printf '%s' "$4" > "$tmp/real.json"
  printf '%s' "$5" > "$tmp/patched.json"
  node "$here/compare.mjs" "$tmp/docs.json" "$tmp/real.json" "$tmp/patched.json" --show 0 "${extra[@]}" > "$tmp/out" 2> "$tmp/err"
  local rc=$?
  local ok=1
  if [ "$2" = zero ] && [ "$rc" -ne 0 ]; then ok=0; fi
  if [ "$2" = nonzero ] && [ "$rc" -eq 0 ]; then ok=0; fi
  if [ "$ok" -eq 1 ]; then echo "selftest: $1 rc=$rc (want $2) ok"; else echo "selftest: FAIL $1 rc=$rc (want $2)" >&2; bad=1; fi
}

keyed='["a: b\n[? c]: !bwref x/foo\n"]'
refused='[{"err":"did not find expected key","tags":[]}]'
accepted='[{"err":"","tags":["x/foo"]}]'
check "pass: the bug decides the document and the model predicts it" zero "$keyed" "$refused" "$accepted"
check "planted false refusal: the model predicts a refusal and go-yaml accepts" nonzero "$keyed" "$accepted" "$accepted"
check "planted miss: the bug decides a document and the model predicts nothing" nonzero '["a: 1\n"]' "$refused" "$accepted"
check "empty set" nonzero '[]' '[]' '[]'
check "count mismatch between the set and an oracle" nonzero "$keyed" '[]' "$accepted"
check "planted false refusal: the model reports a scanner error and go-yaml accepts" nonzero '["@"]' "$accepted" "$accepted"

# Pin: a document that starts with two byte order marks (two U+FEFF characters, written as JSON escapes) gets the verdict
# "unsupported" (the model throws Unsupported("two BOMs at the start")), and the comparison counts it as not
# compared and does not fail. The same document under the bug would count as missed, so the edge set carries
# none (see the report).
check "pin: a document that starts with two marks is unsupported and not a failure" zero '["\ufeff\ufeff[? b]: !bwref x/foo\n"]' "$accepted" "$accepted"
if head -n 1 "$tmp/out" | rg -q '"model:unsupported":1'; then
  echo "selftest: pin: the verdict of a two-mark document is unsupported ok"
else
  echo "selftest: FAIL pin: the verdict of a two-mark document is not unsupported: $(head -n 1 "$tmp/out")" >&2; bad=1
fi

# Pin: a scan loop that hits its bound fails the run, and the count has its own bucket, for both model calls (guard off and on).
# --max-steps 1 is the test hook that makes the bound reachable; "a: b" needs more than one step.
extra=(--max-steps 1)
check "pin: a reached loop bound fails the run" nonzero '["a: b\n"]' '[{"err":"","tags":[]}]' '[{"err":"","tags":[]}]'
extra=()
case "$(head -n 1 "$tmp/out")" in
  *'"not compared: model guard-exhausted":1'*'"not compared: patched model guard-exhausted":1'*)
    echo "selftest: pin: a reached loop bound is counted in both buckets ok" ;;
  *) echo "selftest: FAIL pin: a reached loop bound is not counted in both buckets: $(head -n 1 "$tmp/out")" >&2; bad=1 ;;
esac

# The unit cases of the model.
if node "$here/pins.mjs" > "$tmp/pins.out" 2>&1; then
  echo "selftest: pins.mjs $(tail -n 1 "$tmp/pins.out") ok"
else
  echo "selftest: FAIL pins.mjs" >&2; cat "$tmp/pins.out" >&2; bad=1
fi
exit "$bad"
