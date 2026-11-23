#!/usr/bin/env bash
# Fixture tests for scripts/release/stop.sh, the stop before a release (ginsys/bronzeward#262): the
# VERSION file holds a pre-release of the decided form, and the repository has a LICENSE. A dry run
# without a LICENSE passes with a warning, a real run fails (ginsys/bronzeward#16).
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
stop="$here/../release/stop.sh"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
pass=0
fail=0

repo() { # <dir> <licence: yes|no> <VERSION text>: a repository root for the stop
  local d=$work/$1
  mkdir -p "$d"
  printf '%s\n' "$3" >"$d/VERSION"
  if [ "$2" = yes ]; then printf 'A licence.\n' >"$d/LICENSE"; fi
}
repo good yes v0.1.0-alpha.1
repo nolicence no v0.1.0-alpha.1
repo nolicence-badversion no v1.0.0
repo zero yes v0.2.0-beta.0
repo rc yes v0.12.0-rc.3
repo nofile yes x
rm "$work/nofile/VERSION"
# A VERSION that is not a pre-release of the decided form, each refused on its form alone.
bad=(v0.1.0 v1.0.0-alpha.1 v0.1.1-alpha.1 v0.0.0-alpha.1 v0.1.0-alpha.01 0.1.0-alpha.1 xv0.1.0-alpha.1
  v0.1.0-gamma.1 v0.1.0-alpha.1x "v0.1.0-alpha.1 x")
for i in "${!bad[@]}"; do repo "bad$i" yes "${bad[$i]}"; done

# expect RC NAME DIR [DRY_RUN]; the stop's stderr is left in $work/err
expect() {
  local want=$1 name=$2 rc=0
  (cd "$work/$3" && DRY_RUN=${4:-0} "$stop") >/dev/null 2>"$work/err" || rc=$?
  if [ "$rc" -eq "$want" ]; then
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    echo "FAIL: $name: expected rc=$want got rc=$rc" >&2
  fi
}
check() { # <name> <command>...: one more case, passing when the command does
  local name=$1
  shift
  if "$@"; then
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    echo "FAIL: $name" >&2
  fi
}

expect 0 "a pre-release and a licence, real run" good
expect 0 "a pre-release and a licence, dry run" good 1
expect 0 "a .0 pre-release number" zero
expect 0 "a release candidate on a longer minor" rc
for i in "${!bad[@]}"; do
  expect 1 "the VERSION '${bad[$i]}', real run" "bad$i"
  expect 1 "the VERSION '${bad[$i]}', dry run" "bad$i" 1
done
expect 1 "no VERSION file" nofile
expect 1 "no licence, real run" nolicence
check "the refusal names the licence issue" grep -qF "ginsys/bronzeward#16" "$work/err"
expect 0 "no licence, dry run" nolicence 1
check "the dry run says a real run stops" grep -qF "a real run stops here" "$work/err"
expect 1 "no licence and a bad VERSION, dry run" nolicence-badversion 1
check "a dry run with no licence names the VERSION failure only" test "$(grep -c '::error::' "$work/err")" -eq 1
expect 1 "no licence and a bad VERSION, real run" nolicence-badversion
check "a real run names every failure, not only the first" test "$(grep -c '::error::' "$work/err")" -eq 2

echo "stop-test: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
