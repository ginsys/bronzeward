#!/bin/bash
# Run every measurement of the bw-861 note in order and write the output to one file.
# usage: runall.sh <repository checkout> <work dir written by gen.sh> <output file>
# Go build caches follow the caller's GOCACHE and GOTMPDIR.
set -u
R=$1
W=$2
OUT=$3
M=docs/design/research/evidence/20261118-import-equivalence/measure.go
cd "$R" || exit 1
{
  echo "### baseline, machines as written by talosctl machineconfig patch (already comment-free)"
  go run $M baseline "$W/machines"
  echo
  echo "### baseline, the two commented files as written by talosctl gen config"
  go run $M baseline "$W/cfg"
  echo
  echo "### shape"
  go run $M shape "$W/machines"
  echo
  echo "### split, rule R2 on, emptied documents dropped from the base"
  go run $M split "$W/machines"
  echo
  echo "### split, rule R2 off (leaf by leaf)"
  BW_R2=off go run $M split "$W/machines"
  echo
  echo "### split, rule R2 on, emptied documents kept as identity-only shells in the base"
  BW_SHELLS=1 go run $M split "$W/machines"
  echo
  echo "### pairs"
  go run $M pairs "$W/machines"
  echo
  echo "### what a reference becomes in the redacted form (w1)"
  go run $M placeholder "$W/machines"
} >"$OUT" 2>&1
echo "exit=$? lines=$(wc -l <"$OUT")"
