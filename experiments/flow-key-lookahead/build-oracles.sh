#!/usr/bin/env bash
# Builds the three go-yaml v3.0.5 oracles of this investigation under <workdir>:
#   oracle-real     the oracle of experiments/scanner-cross-line on unmodified go-yaml
#   oracle-patched  the same oracle on go-yaml with the guarded delete (go-yaml-guard.patch)
#   oracle-instr    instr-oracle on go-yaml with counters (go-yaml-instrument.patch)
# Usage: build-oracles.sh <workdir>
set -euo pipefail

work=${1:?usage: build-oracles.sh <workdir>}
here=$(cd "$(dirname "$0")" && pwd)
src=$here/../scanner-cross-line
mod=$(cd "$src" && go list -m -f '{{.Dir}}' go.yaml.in/yaml/v3)
mkdir -p "$work"

for variant in guard instrument; do
  rm -rf "$work/yaml-$variant"
  cp -r "$mod" "$work/yaml-$variant"
  chmod -R u+w "$work/yaml-$variant"
  patch -p1 -d "$work/yaml-$variant" < "$here/go-yaml-$variant.patch"
done

# mk <name> <yaml dir, or "" for the pinned module> <main.go source>
mk() {
  local d=$work/oracle-$1
  rm -rf "$d"
  mkdir "$d"
  cp "$src/go.mod" "$src/go.sum" "$d/"
  cp "$3" "$d/main.go"
  if [ -n "$2" ]; then
    (cd "$d" && go mod edit -replace "go.yaml.in/yaml/v3=$2")
  fi
}
mk real "" "$src/main.go"
mk patched "$work/yaml-guard" "$src/main.go"
mk instr "$work/yaml-instrument" "$here/instr-oracle/main.go.tmpl"
echo "built oracles under $work"
