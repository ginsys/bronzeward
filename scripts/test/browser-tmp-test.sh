#!/usr/bin/env bash
# Fixture tests for scripts/ci/browser-tmp.sh: the browser tasks' TMPDIR length guard and their check
# for a browser profile left behind.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
tool="$here/../ci/browser-tmp.sh"
# Under /tmp, not TMPDIR: the cases need absolute paths of 42 to 62 bytes, and a TMPDIR can be longer.
root=$(mktemp -d /tmp/bwt.XXXXXX)
trap 'rm -rf "$root"' EXIT
[ "${#root}" -le 40 ] || { echo "browser-tmp-test: $root is too long to build the TMPDIRs it needs" >&2; exit 1; }
pass=0
fail=0
ok() { pass=$((pass + 1)); }
bad() { fail=$((fail + 1)); echo "FAIL: $*" >&2; }

# tmpdir LEN: a fresh directory whose path is LEN bytes long.
tmpdir() {
  local d
  d="$root/$(printf '%*s' $(($1 - ${#root} - 1)) '' | tr ' ' a)"
  rm -rf "$d"
  mkdir -p "$d"
  printf '%s' "$d"
}

# start LEN BROWSERS WANT: start's status for a TMPDIR of LEN bytes, and what it leaves there.
start() {
  local t rc=0 out
  t=$(tmpdir "$1")
  if [ "$2" = unset ]; then
    out=$(env -u BW_BROWSERS TMPDIR="$t" "$tool" start browser 2>"$root/err") || rc=$?
  else
    out=$(TMPDIR=$t BW_BROWSERS=$2 "$tool" start browser 2>"$root/err") || rc=$?
  fi
  if [ "$rc" -ne "$3" ]; then bad "start at $1 bytes with '$2': rc=$rc, want $3"; return; fi
  if [ "$rc" -eq 0 ]; then
    if [ -d "$out" ] && [ "${out%/*}" = "$t" ]; then ok; else bad "start at $1 bytes printed '$out', not a directory in $t"; fi
  elif [ -n "$(ls -A "$t")" ]; then
    bad "a refused start at $1 bytes left $(ls -A "$t")"
  elif grep -q "browser: TMPDIR $t is too long: .* would be $(($1 + 56)) bytes, .* at most 51 bytes" "$root/err"; then
    ok
  else
    bad "start at $1 bytes said: $(cat "$root/err")"
  fi
}

start 42 chrome,firefox 0
start 51 chrome,firefox 0
start 52 chrome,firefox 1
start 62 chrome,firefox 1
start 52 unset 1                   # wdio.conf.ts's default, chrome,firefox
start 52 '' 1                      # wdio refuses an empty list; the guard treats it as the default
start 52 edge 1
start 52 firefox-esr,chrome 1
start 52 firefox 0                 # no Chromium session, no socket
start 52 firefox,firefox-esr,safari 0
start 52 chromeless 0              # a name, not a substring

# The length is in bytes whatever the locale: 52 bytes of two-byte characters are refused in C.UTF-8,
# where they are fewer characters (checked, so a missing locale cannot pass this vacuously).
p=$((52 - ${#root} - 1))
mb="$root/$([ $((p % 2)) -eq 0 ] || printf a)$(printf 'é%.0s' $(seq $((p / 2))))"
mkdir -p "$mb"
rc=0
LC_ALL=C.UTF-8 TMPDIR=$mb BW_BROWSERS=chrome "$tool" start browser >/dev/null 2>"$root/err" || rc=$?
if [ "$(printf %s "$mb" | wc -c)" -ne 52 ] || [ "$(LC_ALL=C.UTF-8 bash -c 'echo ${#1}' _ "$mb")" -ge 52 ]; then
  bad "the multibyte TMPDIR is not 52 bytes of fewer characters in C.UTF-8 (missing locale?)"
elif [ "$rc" -eq 1 ] && grep -q 'would be 108 bytes' "$root/err"; then
  ok
else
  bad "a 52-byte multibyte TMPDIR: rc=$rc $(cat "$root/err")"
fi

# finish ENTRY WANT: finish's status after the run left ENTRY (none: nothing) in its directory.
finish() {
  local t dir rc=0
  t=$(tmpdir 30)
  dir=$(mktemp -d "$t/bw.XXXXXX")
  [ "$1" = none ] || mkdir "$dir/$1"
  "$tool" finish browser "$dir" 2>"$root/err" || rc=$?
  if [ "$rc" -ne "$2" ]; then bad "finish with $1 left: rc=$rc, want $2"; return; fi
  if [ -e "$dir" ]; then bad "finish with $1 left kept $dir"; return; fi
  if [ "$rc" -eq 0 ] || grep -q "left browser profiles behind" "$root/err"; then ok; else bad "finish with $1 left said: $(cat "$root/err")"; fi
}

finish none 0
finish .org.chromium.Chromium.Ab12Cd 1
finish bw-browser-profiles-Ab12Cd 1
finish wdio-other 0                # only browser profiles count

# A directory that is gone or cannot be read is a failure, never a pass.
rc=0
"$tool" finish browser "$root/gone" 2>"$root/err" || rc=$?
if [ "$rc" -eq 1 ] && grep -q "cannot read $root/gone" "$root/err"; then ok; else bad "finish on a missing directory: rc=$rc $(cat "$root/err")"; fi
rc=0
"$tool" start 2>/dev/null || rc=$?
if [ "$rc" -eq 2 ]; then ok; else bad "start without a task: rc=$rc, want 2"; fi

echo "browser-tmp-test: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
