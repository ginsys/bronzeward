#!/usr/bin/env bash
# Fixture tests for scripts/release/guard.sh: only a pre-release tag of the decided form, with its
# own non-empty CHANGELOG.md section and a LICENSE, passes; a dry run without a LICENSE passes with
# a warning, a real run fails (ginsys/bronzeward#401, #16).
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
guard="$here/../release/guard.sh"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
pass=0
fail=0

repo() { # <dir> <licence: yes|no> <changelog line>...: a repository root for the guard
  local d=$work/$1 licence=$2
  shift 2
  mkdir -p "$d"
  printf '%s\n' "# Changelog" "" "$@" >"$d/CHANGELOG.md"
  if [ "$licence" = yes ]; then printf 'A licence.\n' >"$d/LICENSE"; fi
}
repo good yes "## v0.2.0-rc.1" "" "A candidate." "## v0.2.0-beta.2" "" "A beta." \
  "## v0.2.0-alpha.1" "" "The second." "" "## v0.1.0-alpha.1" "" "The first."
repo nolicence no "## v0.1.0-alpha.1" "" "The first."
repo empty yes "## v0.1.0-alpha.1" "" "## v0.0.9" "" "Older."
repo longer yes "## v0.1.0-alpha.10" "" "Another."
# Every refused tag has its own section, so only the tag's form can refuse it.
bad=(v0.1.0 v1.0.0-alpha.1 v0.1.1-alpha.1 v0.1.0-alpha.0 v0.1.0-alpha.01 0.1.0-alpha.1 xv0.1.0-alpha.1
  v0.1.0-gamma.1 v0.1.0-alpha.1x "v0.1.0-alpha.1 ")
sections=()
for v in "${bad[@]}"; do sections+=("## $v" "" "A section."); done
repo badtags yes "${sections[@]}"

# expect RC NAME DIR VERSION [DRY_RUN]; the guard's stderr is left in $work/err
expect() {
  local want=$1 name=$2 rc=0
  (cd "$work/$3" && DRY_RUN=${5:-0} "$guard" "$4" "$work/notes") >/dev/null 2>"$work/err" || rc=$?
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

expect 0 "a tag with its section and a licence" good v0.1.0-alpha.1
if [ "$(cat "$work/notes")" = "$(printf '\nThe first.')" ]; then
  pass=$((pass + 1))
else
  fail=$((fail + 1))
  echo "FAIL: the notes are not the section's body: $(cat "$work/notes")" >&2
fi
expect 0 "a section between two others" good v0.2.0-alpha.1
expect 0 "a beta" good v0.2.0-beta.2
expect 0 "a release candidate" good v0.2.0-rc.1
for v in "${bad[@]}"; do
  expect 1 "the tag '$v'" badtags "$v"
done
expect 1 "a tag with no section" good v0.3.0-alpha.1
expect 1 "a section for a longer tag only" longer v0.1.0-alpha.1
check "a longer tag's section is no section" grep -qF "has no section headed" "$work/err"
expect 1 "an empty section" empty v0.1.0-alpha.1
rm -f "$work/notes"
expect 1 "no licence, real run" nolicence v0.1.0-alpha.1
check "a refused run writes no notes" test ! -e "$work/notes"
expect 0 "no licence, dry run" nolicence v0.1.0-alpha.1 1
expect 1 "no licence and no section, dry run" nolicence v0.2.0-alpha.1 1

echo "release-guard-test: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
