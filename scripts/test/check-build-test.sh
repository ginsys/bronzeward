#!/usr/bin/env bash
# Negative controls for scripts/release/check-build.sh over a real build in $1 (ginsys/bronzeward#401):
# the build passes as built, and each planted defect, with SHA256SUMS regenerated where the defect
# would otherwise only show as a hash mismatch, fails with its own message. Same environment as
# check-build.sh (BW_VERSION, EXPECT_REVISION).
set -euo pipefail
: "${BW_VERSION:?}" "${EXPECT_REVISION:?}"
here=$(cd "$(dirname "$0")" && pwd)
check="$here/../release/check-build.sh"
src=$(cd "$1" && pwd)
v=${BW_VERSION#v}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
pass=0
fail=0

web=bronzeward-web_$v.tar.gz
fresh() { rm -rf "$work/d" && cp -a "$src" "$work/d"; }
resum() { (cd "$work/d" && sha256sum ./*.tar.gz | sed 's| \./| |' >SHA256SUMS); }
unpack() { # <archive> <directory>: extract a copy's archive, whether its members are name or ./name
	rm -rf "$2" && mkdir "$2" && tar -xzf "$work/d/$1" -C "$2"
}
pack() { tar -czf "$work/d/$1" -C "$work/x" .; }
# drop <file name>: remove that file's one SHA256SUMS line, with either marker
drop() {
	local before
	before=$(wc -l <"$work/d/SHA256SUMS")
	awk -v n="$1" '!(length($0) == 66 + length(n) && substr($0, 67) == n && substr($0, 65, 1) == " ")' "$work/d/SHA256SUMS" >"$work/sums" && mv "$work/sums" "$work/d/SHA256SUMS"
	[ "$(wc -l <"$work/d/SHA256SUMS")" -eq $((before - 1)) ] || { echo "check-build-test: no one line for $1 in SHA256SUMS" >&2; exit 1; }
}

expect() { # <0|1> <name> <message expected on a failure>
	local want=$1 name=$2 out rc=0
	out=$("$check" "$work/d" 2>&1) || rc=$?
	if [ "$want" -eq 0 ] && [ "$rc" -eq 0 ]; then
		pass=$((pass + 1))
	elif [ "$want" -ne 0 ] && [ "$rc" -ne 0 ] && grep -qF -- "$3" <<<"$out"; then
		pass=$((pass + 1))
	else
		fail=$((fail + 1))
		printf 'FAIL: %s: rc=%s\n%s\n' "$name" "$rc" "$out" >&2
	fi
}

fresh
expect 0 "the build as built"

fresh
drop "$web"
expect 1 "SHA256SUMS without the web tarball" "SHA256SUMS does not name exactly"

# sha256sum reads the whole name, so a line naming "<web> x" checks a file of that name, not <web>.
fresh
cp "$work/d/$web" "$work/d/$web x"
drop "$web"
printf '%s  %s\n' "$(sha256sum <"$work/d/$web x" | cut -d' ' -f1)" "$web x" >>"$work/d/SHA256SUMS"
expect 1 "SHA256SUMS naming the web tarball only as a name's first word" "SHA256SUMS does not name exactly"

fresh
printf 'not a checksum line\n' >>"$work/d/SHA256SUMS"
expect 1 "SHA256SUMS with a line that is no checksum" "SHA256SUMS has a line that is not"

fresh
a=bronzeward_${v}_linux_amd64.tar.gz
unpack "$a" "$work/x"
chmod 0644 "$work/x/bronzeward"
pack "$a"
resum
expect 1 "a server binary without its execute bit" "bronzeward is not executable"

fresh
a=bronzewardctl_${v}_darwin_arm64.tar.gz
unpack "$a" "$work/x"
unpack "bronzewardctl_${v}_linux_amd64.tar.gz" "$work/y"
cp "$work/y/bronzewardctl" "$work/x/bronzewardctl"
pack "$a"
resum
expect 1 "the linux/amd64 client in the darwin/arm64 archive" "the binary is not built for darwin"

# A longer version holds the expected one as a prefix: its NUL now follows one byte later.
fresh
a=bronzewardctl_${v}_darwin_arm64.tar.gz
unpack "$a" "$work/x"
python3 -c 'import sys; p, v = sys.argv[1], sys.argv[2].encode(); b = open(p, "rb").read(); assert b.count(v + b"\0") == 1; open(p, "wb").write(b.replace(v + b"\0", v + b"0"))' "$work/x/bronzewardctl" "$BW_VERSION"
pack "$a"
resum
expect 1 "a darwin/arm64 client stamped with a longer version" "darwin_arm64.tar.gz: the binary holds no version"

fresh
EXPECT_REVISION=0000000000000000000000000000000000000000 expect 1 "another revision" "vcs.revision is not"

echo "check-build-test: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
