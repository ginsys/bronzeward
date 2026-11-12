#!/usr/bin/env bash
# Fixture tests for scripts/ci/browser-artifact.sh (ginsys/bronzeward#782): a failed browser-auth run
# uploads its artifacts when the scan passed, and only a listing of names and sizes when it did not.
# The control plants a synthetic value, runs the real scan, and reads every file in the upload set,
# and the name of every one, for it.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
tool="$here/../ci/browser-artifact.sh"
root=$(mktemp -d)
trap 'rm -rf "$root"' EXIT
pass=0
fail=0
ok() { pass=$((pass + 1)); }
bad() { fail=$((fail + 1)); echo "FAIL: $*" >&2; }
value="BWSYNTH-web-0123456789abcdef"

# fresh NAME: an artifact directory of this case with a clean console log and a nested file.
fresh() {
  case=$root/$1
  rm -rf "$case"
  mkdir -p "$case/out/sub"
  echo "ran cases" >"$case/out/console.log"
  echo "a page" >"$case/out/sub/page.html"
}

# scan: runs the scan verb on the current case with the planted value; sets rc and the log in $root/err.
scan() {
  rc=0
  "$tool" scan "$case/dev/status" "$case/dev/listing" "$case/out" "$value" 2>"$root/err" >/dev/null || rc=$?
}

# publish: runs the publish verb on the current case and sets up to the directory it names.
publish() {
  local line
  line=$("$tool" publish "$case/out" "$case/dev/status" "$case/dev/listing") || { bad "publish failed in $case"; up=; return; }
  up=${line#path=}
  [ "$up" != "$line" ] || { bad "publish printed '$line', not path=<dir>"; up=; }
}

# holds PATH: whether a file under PATH, or the name of one, holds the value.
holds() {
  grep -rqF -- "$value" "$1" || find "$1" -name "*$value*" | grep -q .
}

# 1. A clean run: the scan passes and the whole artifact directory is the upload.
fresh clean
scan
publish
if [ "$rc" -eq 0 ] && [ "$(cat "$case/dev/status")" = passed ] && [ "$up" = "$case/out" ]; then ok; else bad "clean run: rc=$rc status=$(cat "$case/dev/status" 2>/dev/null) upload=$up"; fi

# 2. A planted value in a file's bytes: the scan fails, only the listing is uploaded, and no uploaded
# file or name holds the value.
for how in console nested; do
  fresh "planted-$how"
  if [ "$how" = console ]; then
    echo "expected $value to equal 1" >"$case/out/console.log"
  else
    echo "before $value after" >"$case/out/sub/page.html"
  fi
  scan
  publish
  if [ "$rc" -eq 0 ]; then bad "planted value in $how: the scan passed"; continue; fi
  if [ "$(cat "$case/dev/status")" != listed ]; then bad "planted value in $how: status is $(cat "$case/dev/status")"; continue; fi
  if [ "$up" != "$case/dev/listing" ]; then bad "planted value in $how: upload is $up"; continue; fi
  files=$(find "$up" -type f | wc -l)
  if [ "$files" -ne 1 ] || [ ! -f "$up/files.txt" ]; then bad "planted value in $how: upload holds $files files"; continue; fi
  if holds "$up"; then bad "planted value in $how: the upload holds the value"; continue; fi
  if grep -q "^f [0-9]* console.log$" "$up/files.txt" && grep -q "^f [0-9]* sub/page.html$" "$up/files.txt"; then ok; else bad "planted value in $how: the listing is $(cat "$up/files.txt")"; fi
done

# 3. The positive control for the check above: the planted value is in the original bytes.
if holds "$root/planted-nested/out"; then ok; else bad "the control's planted value is not in the original bytes"; fi

# 4. A planted value in a file's name: the scan fails, the listing would carry the name, so the
# listing is withheld and the upload is empty. The log never holds the value.
fresh planted-name
echo "clean" >"$case/out/sub/$value.log"
scan
publish
if [ "$rc" -eq 0 ]; then bad "planted value in a name: the scan passed"
elif [ "$(cat "$case/dev/status")" != withheld ]; then bad "planted value in a name: status is $(cat "$case/dev/status")"
elif [ "$up" != "$case/dev/listing" ] || [ "$(find "$up" -type f | wc -l)" -ne 0 ]; then bad "planted value in a name: upload $up holds files"
elif holds "$up" || grep -qF -- "$value" "$root/err"; then bad "planted value in a name: the upload or the log holds the value"
elif holds "$case/out"; then ok
else bad "the control's planted name is not in the original tree"; fi

# 4b. An unreadable file whose name holds the value: a filesystem error would quote the path, so the
# log must not hold it. (Skipped when the user can read it anyway, as root can.)
fresh unreadable-name
echo "clean" >"$case/out/sub/$value.log"
chmod 000 "$case/out/sub/$value.log"
if [ -r "$case/out/sub/$value.log" ]; then
  ok
else
  scan
  if [ "$rc" -ne 0 ] && [ "$(cat "$case/dev/status")" = withheld ] && ! grep -qF -- "$value" "$root/err"; then ok; else bad "unreadable file named with the value: rc=$rc status=$(cat "$case/dev/status" 2>/dev/null) log=$(cat "$root/err")"; fi
fi

# 4b'. An unreadable directory named with the value: the listing cannot be completed, so nothing is
# uploaded, and neither the scan nor find may put the name in the log.
fresh unreadable-dir
mkdir "$case/out/$value"
echo "clean" >"$case/out/$value/page.html"
chmod 000 "$case/out/$value"
if [ -r "$case/out/$value" ]; then
  ok
else
  scan
  publish
  if [ "$rc" -ne 0 ] && [ "$(cat "$case/dev/status")" = withheld ] && [ "$(find "$up" -type f | wc -l)" -eq 0 ] && ! grep -qF -- "$value" "$root/err"; then ok; else bad "unreadable directory named with the value: rc=$rc status=$(cat "$case/dev/status" 2>/dev/null) log=$(cat "$root/err")"; fi
fi
chmod 700 "$case/out/$value"

# 4c. A value that the control files' own names hold ("planted-0.log") must not break the control's count.
fresh control-name
rc=0
"$tool" scan "$case/dev/status" "$case/dev/listing" "$case/out" "planted-0" 2>"$root/err" >/dev/null || rc=$?
if [ "$rc" -eq 0 ] && [ "$(cat "$case/dev/status")" = passed ]; then ok; else bad "a value held by the control's file names: rc=$rc $(cat "$root/err")"; fi

# 4d. The upload of a passed scan carries hidden files, which the scan read.
if sed -n '/^  browser-auth:/,/^  package:/p' "$here/../../.github/workflows/ci.yml" | grep -q 'include-hidden-files: true'; then ok; else bad "ci.yml's browser-auth upload does not set include-hidden-files: true"; fi

# 5. A run that never reached the scan, or left an odd status: nothing is uploaded, never the files.
fresh no-status
publish
if [ "$up" = "$case/dev/listing" ] && [ "$(find "$up" -type f | wc -l)" -eq 0 ]; then ok; else bad "no status file: upload is $up"; fi
fresh odd-status
mkdir -p "$case/dev"
echo "passed but not" >"$case/dev/status"
publish
if [ "$up" = "$case/dev/listing" ] && [ "$(find "$up" -type f | wc -l)" -eq 0 ]; then ok; else bad "a status that is not exactly passed released $up"; fi

# 6. A stale status from an earlier run does not survive a failed scan.
fresh stale
mkdir -p "$case/dev"
echo passed >"$case/dev/status"
echo "$value" >"$case/out/console.log"
scan
if [ "$rc" -ne 0 ] && [ "$(cat "$case/dev/status")" = listed ]; then ok; else bad "stale status: rc=$rc status=$(cat "$case/dev/status")"; fi

# 7. No artifact directory at all: the scan fails and the listing holds its header only.
fresh gone
rm -rf "$case/out"
scan
publish
if [ "$rc" -ne 0 ] && [ "$up" = "$case/dev/listing" ] && [ "$(wc -l <"$up/files.txt")" -eq 1 ]; then ok; else bad "no artifact directory: rc=$rc upload is $up"; fi

# 8. Usage errors exit 2.
rc=0
"$tool" 2>/dev/null || rc=$?
if [ "$rc" -eq 2 ]; then ok; else bad "no verb: rc=$rc, want 2"; fi

echo "browser-artifact-test: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
