#!/usr/bin/env bash
# Fixture tests for scripts/dev/native-services.sh's host guard: it installs software and starts
# trust-auth services, so it must refuse anywhere but a GitHub Actions macOS arm64 runner. The guard
# takes the system, the machine and the GITHUB_ACTIONS value as arguments, so these cases run on any
# host and never reach the install path.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
tool="$here/../dev/native-services.sh"
root=$(mktemp -d "${TMPDIR:-/tmp}/bwt.XXXXXX")
trap 'rm -rf "$root"' EXIT
pass=0
fail=0
ok() { pass=$((pass + 1)); }
bad() { fail=$((fail + 1)); echo "FAIL: $*" >&2; }

# guard SYSTEM MACHINE ACTIONS WANT_RC WANT_TEXT: the guard's status, and a word in what it says.
guard() {
  local rc=0
  bash "$tool" guard "$1" "$2" "$3" >"$root/out" 2>"$root/err" || rc=$?
  if [ "$rc" -ne "$4" ]; then bad "guard '$1' '$2' '$3': rc=$rc, want $4: $(cat "$root/err")"; return; fi
  if [ -n "$5" ] && ! grep -q -- "$5" "$root/err"; then bad "guard '$1' '$2' '$3' did not say '$5': $(cat "$root/err")"; return; fi
  ok
}

guard Darwin arm64 true 0 ''                  # the positive control: the one host it accepts
guard Darwin arm64 '' 1 GITHUB_ACTIONS        # a developer's own Mac
guard Darwin arm64 false 1 GITHUB_ACTIONS
guard Darwin arm64 1 1 GITHUB_ACTIONS         # only the value `true`
guard Linux x86_64 true 1 'macOS arm64'
guard Darwin x86_64 true 1 'macOS arm64'

# `up` on this host, with a brew that records its calls: refused before anything is installed, and
# nothing is written under the working directory.
mkdir -p "$root/bin" "$root/work"
cat >"$root/bin/brew" <<EOF
#!/bin/sh
echo "\$@" >>"$root/brew.calls"
EOF
chmod +x "$root/bin/brew"
rc=0
(cd "$root/work" && env -u GITHUB_ACTIONS PATH="$root/bin:$PATH" bash "$tool" up >"$root/out" 2>"$root/err") || rc=$?
if [ "$rc" -eq 0 ]; then bad "up ran on this host without GITHUB_ACTIONS"; else ok; fi
if [ -e "$root/brew.calls" ]; then bad "a refused up still called brew: $(cat "$root/brew.calls")"; else ok; fi
if [ -n "$(ls -A "$root/work")" ]; then bad "a refused up left $(ls -A "$root/work")"; else ok; fi

# An unknown verb is a usage error, not a no-op.
rc=0
bash "$tool" frobnicate >/dev/null 2>&1 || rc=$?
if [ "$rc" -eq 2 ]; then ok; else bad "an unknown verb exited $rc, want 2"; fi

echo "native-services-test: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
