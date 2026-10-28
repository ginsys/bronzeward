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

fresh() { rm -rf "$work/d" && cp -a "$src" "$work/d"; }
resum() { (cd "$work/d" && sha256sum ./*.tar.gz | sed 's| \./| |' >SHA256SUMS); }
repack() { # <archive> <command run in its extracted directory>
	local a=$work/d/$1 x=$work/x
	rm -rf "$x" && mkdir "$x" && tar -xzf "$a" -C "$x"
	(cd "$x" && eval "$2")
	tar -czf "$a" -C "$x" .
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
sed -i "/ bronzeward-web_$v.tar.gz\$/d" "$work/d/SHA256SUMS"
expect 1 "SHA256SUMS without the web tarball" "SHA256SUMS does not name exactly"

fresh
repack "bronzeward_${v}_linux_amd64.tar.gz" 'chmod 0644 bronzeward'
resum
expect 1 "a server binary without its execute bit" "bronzeward is not executable"

fresh
repack "bronzewardctl_${v}_darwin_arm64.tar.gz" "tar -xzOf '$src/bronzewardctl_${v}_linux_amd64.tar.gz' bronzewardctl >bronzewardctl"
resum
expect 1 "the linux/amd64 client in the darwin/arm64 archive" "the binary is not built for darwin"

fresh
EXPECT_REVISION=0000000000000000000000000000000000000000 expect 1 "another revision" "vcs.revision is not"

echo "check-build-test: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
