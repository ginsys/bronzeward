#!/bin/bash
# Run every measurement of the bw-861 note in order and write the output to one file.
# usage: bash runall.sh <repository checkout> <work dir written by gen.sh> <output file>
# Go build caches follow the caller's GOCACHE and GOTMPDIR. The exit status is 1 if any
# measurement failed, and the failing command is named in the output.
set -u
R=$1
W=$2
OUT=$3
M=docs/design/research/evidence/20261118-import-equivalence/measure.go
rc=0
cd "$R" || exit 1
: >"$OUT" || { echo "cannot write $OUT"; exit 1; }
first=1
run() { # label, then the measurement's arguments; a failure is recorded and the run goes on
  [ "$first" = 1 ] || echo
  first=0
  echo "### $1"
  shift
  "$@" || { echo "FAILED (exit $?): $*"; rc=1; }
}
{
  run "baseline, machines as written by talosctl machineconfig patch (already comment-free)" go run $M baseline "$W/machines"
  run "baseline, the two commented files as written by talosctl gen config" go run $M baseline "$W/cfg"
  run "shape" go run $M shape "$W/machines"
  run "split, rule R2 on, emptied documents dropped from the base" go run $M split "$W/machines"
  run "split, rule R2 off (leaf by leaf)" env BW_R2=off go run $M split "$W/machines"
  run "split, rule R2 on, emptied documents kept as identity-only shells in the base" env BW_SHELLS=1 go run $M split "$W/machines"
  run "pairs" go run $M pairs "$W/machines"
  run "what a reference becomes in the redacted form (w1)" go run $M placeholder "$W/machines"
} >"$OUT" 2>&1
echo "exit=$rc lines=$(wc -l <"$OUT")"
exit $rc
