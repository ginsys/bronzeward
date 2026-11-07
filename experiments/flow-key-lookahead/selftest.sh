#!/usr/bin/env bash
# Checks that compare.mjs fails closed. It feeds the comparison five hand-written oracle outputs and
# requires the exit status to match: 0 for a pass, and nonzero for a false refusal, a missed document,
# an empty set and a count mismatch. It needs no go and no oracle.
# Usage: selftest.sh
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/flow-key-selftest.XXXXXX") || exit 2
trap 'rm -rf "$tmp"' EXIT

bad=0
# check <name> <want: zero|nonzero> <docs json> <real json> <patched json>
check() {
  printf '%s' "$3" > "$tmp/docs.json"
  printf '%s' "$4" > "$tmp/real.json"
  printf '%s' "$5" > "$tmp/patched.json"
  node "$here/compare.mjs" "$tmp/docs.json" "$tmp/real.json" "$tmp/patched.json" --show 0 > "$tmp/out" 2> "$tmp/err"
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
exit "$bad"
