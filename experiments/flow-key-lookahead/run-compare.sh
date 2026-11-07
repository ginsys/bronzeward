#!/usr/bin/env bash
# Runs one document set through the three oracles built by build-oracles.sh, then compares the
# model with them. Results stay in <workdir> as <name>.real.json, .patched.json and .instr.json.
# Usage: run-compare.sh <workdir> <set.json> [compare.mjs options]
set -euo pipefail

work=$(cd "${1:?usage: run-compare.sh <workdir> <set.json> [options]}" && pwd)
set_file=$(cd "$(dirname "${2:?}")" && pwd)/$(basename "$2")
shift 2
here=$(cd "$(dirname "$0")" && pwd)
name=$(basename "$set_file" .json)

(cd "$work/oracle-real" && go run . "$set_file" "$work/$name.real.json")
(cd "$work/oracle-patched" && go run . "$set_file" "$work/$name.patched.json")
(cd "$work/oracle-instr" && go run . "$set_file" "$work/$name.instr.json")
node "$here/compare.mjs" "$set_file" "$work/$name.real.json" "$work/$name.patched.json" "$work/$name.instr.json" "$@"
