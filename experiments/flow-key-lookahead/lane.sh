#!/usr/bin/env bash
# The heavy sets of the investigation, one after the other: the flow-key set (s13, 23247 documents),
# S9 and S8 (245696). It builds the oracles, runs each set through three oracles and the model,
# and keeps one result file per set: <workdir>/lane-<set>.out.
# The wrapper that holds the heavy-run lock makes the sets first (gen-flow-key-set.mjs and the two
# generators in ../scanner-cross-line/sets) and checks their count and sha256. Nothing else starts here.
# Usage: lane.sh <workdir>   (<workdir> holds s13.json, s9.json and s8.json)
set -euo pipefail

work=${1:?usage: lane.sh <workdir>}
here=$(cd "$(dirname "$0")" && pwd)
work=$(cd "$work" && pwd)

"$here/build-oracles.sh" "$work"
# compare.mjs exits 1 when a set fails the pass rule. Every set still runs and keeps its file, and the
# script exits 1 at the end if any set failed.
rc=0
for name in s13 s9 s8; do
  "$here/run-compare.sh" "$work" "$work/$name.json" --show 20 > "$work/lane-$name.out" || { echo "lane: $name FAILED" >&2; rc=1; }
  head -c 2000 "$work/lane-$name.out"
done
exit "$rc"
