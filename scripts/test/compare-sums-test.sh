#!/usr/bin/env bash
# Fixture tests for scripts/release/compare-sums.sh: the packaging check's two builds pass only when
# every SHA256SUMS line matches, or each differing file is a listed exception. The planted
# differences are ginsys/bronzeward#401's proof that an unlisted difference fails.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
compare="$here/../release/compare-sums.sh"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
pass=0
fail=0

sums() { # <file> <line>...: write a SHA256SUMS file
  local f=$1
  shift
  printf '%s\n' "$@" >"$f"
}
h1=$(printf '1%.0s' {1..64})
h2=$(printf '2%.0s' {1..64})
h3=$(printf '3%.0s' {1..64})
sums "$work/a" "$h1  bronzewardctl_0.1.0_linux_amd64.tar.gz" "$h2  bronzeward-web_0.1.0.tar.gz"
printf '# comment\n' >"$work/none"
printf 'bronzeward-web_0.1.0.tar.gz  an example cause (#0)\n' >"$work/web"

# expect RC NAME A B EXCEPTIONS
expect() {
  local want=$1 name=$2 rc=0
  "$compare" "$3" "$4" "$5" >/dev/null 2>&1 || rc=$?
  if [ "$rc" -eq "$want" ]; then
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    echo "FAIL: $name: expected rc=$want got rc=$rc" >&2
  fi
}

expect 0 "identical builds" "$work/a" "$work/a" "$work/none"
sums "$work/b" "$h1  bronzewardctl_0.1.0_linux_amd64.tar.gz" "$h3  bronzeward-web_0.1.0.tar.gz"
expect 1 "a planted hash difference" "$work/a" "$work/b" "$work/none"
expect 0 "the same difference, listed" "$work/a" "$work/b" "$work/web"
sums "$work/c" "$h3  bronzewardctl_0.1.0_linux_amd64.tar.gz" "$h2  bronzeward-web_0.1.0.tar.gz"
expect 1 "a difference in another file than the listed one" "$work/a" "$work/c" "$work/web"
sums "$work/d" "$h1  bronzewardctl_0.1.0_linux_amd64.tar.gz"
expect 1 "a file one build lacks" "$work/a" "$work/d" "$work/none"
expect 1 "the other order" "$work/d" "$work/a" "$work/none"
: >"$work/empty"
expect 1 "an empty SHA256SUMS" "$work/empty" "$work/empty" "$work/none"
expect 1 "a missing SHA256SUMS" "$work/absent" "$work/a" "$work/none"
printf 'bronzeward-web_0.1.0\n' >"$work/prefix"
expect 1 "an exception naming only a prefix" "$work/a" "$work/b" "$work/prefix"

echo "compare-sums: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
