#!/usr/bin/env bash
# Check one build's archives in $1 against the version and revision it was built from
# (ginsys/bronzeward#262 P7): SHA256SUMS names exactly the seven artifacts and matches them; each
# binary is executable, built for its archive's GOOS and GOARCH, holds BW_VERSION and records
# vcs.revision=EXPECT_REVISION and vcs.modified=false; the linux/amd64 binaries report
# BW_VERSION when run; each archive holds the licence status and notices; the web bundle's footer
# string is BW_VERSION and its headers.json is web/headers.json. Run from the repository root on
# linux/amd64 with go on PATH.
set -euo pipefail
: "${BW_VERSION:?}" "${EXPECT_REVISION:?}"
dir=$(cd "$1" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
status=0
fail() { echo "check-build: $*" >&2; status=1; }

v=${BW_VERSION#v}
expected=("bronzeward-web_$v.tar.gz")
for os_arch in linux_amd64 linux_arm64 darwin_amd64 darwin_arm64; do
	expected+=("bronzewardctl_${v}_$os_arch.tar.gz")
	case $os_arch in linux_*) expected+=("bronzeward_${v}_$os_arch.tar.gz") ;; esac
done
# The two builds are compared through their SHA256SUMS, so it must name every artifact, once.
if grep -qvE '^[0-9a-f]{64} [ *].+$' "$dir/SHA256SUMS"; then
	fail "SHA256SUMS has a line that is not a SHA-256 checksum and a file name"
fi
# The whole name after the checksum and its text or binary marker, as sha256sum reads it.
listed=$(sed -E 's/^[0-9a-f]{64} [ *]//' "$dir/SHA256SUMS" | LC_ALL=C sort)
[ "$listed" = "$(printf '%s\n' "${expected[@]}" | LC_ALL=C sort)" ] || fail "SHA256SUMS does not name exactly the ${#expected[@]} artifacts: $(tr '\n' ' ' <<<"$listed")"
(cd "$dir" && sha256sum --check --strict SHA256SUMS) || fail "SHA256SUMS does not match the files"
for os_arch in linux_amd64 linux_arm64 darwin_amd64 darwin_arm64; do
	archives=("$dir/bronzewardctl_${v}_$os_arch.tar.gz")
	case $os_arch in linux_*) archives+=("$dir/bronzeward_${v}_$os_arch.tar.gz") ;; esac
	for a in "${archives[@]}"; do
		[ -f "$a" ] || { fail "${a##*/} is missing"; continue; }
		d=$work/$(basename "$a" .tar.gz)
		mkdir -p "$d"
		tar -xzf "$a" -C "$d"
		for f in LICENSE-STATUS.md THIRD_PARTY_NOTICES.txt; do
			[ -s "$d/$f" ] || fail "${a##*/} holds no $f"
		done
		bin=$d/$(basename "$a" | cut -d_ -f1)
		[ -x "$bin" ] || fail "${a##*/}: ${bin##*/} is not executable"
		info=$(go version -m "$bin") || { fail "${a##*/}: no Go build information"; continue; }
		grep -qxE "[[:space:]]+build[[:space:]]+GOOS=${os_arch%_*}" <<<"$info" || fail "${a##*/}: the binary is not built for ${os_arch%_*}"
		grep -qxE "[[:space:]]+build[[:space:]]+GOARCH=${os_arch#*_}" <<<"$info" || fail "${a##*/}: the binary is not built for ${os_arch#*_}"
		# -trimpath leaves -ldflags out of the build information, so the -X value is looked for in
		# the binary itself, whole: the linker ends the string -X sets with a NUL byte, so a longer
		# version such as v0.1.0-alpha.10 does not pass for v0.1.0-alpha.1. The linux/amd64 ones
		# are also run below.
		python3 -c 'import sys; sys.exit(sys.argv[2].encode() + b"\0" not in open(sys.argv[1], "rb").read())' "$bin" "$BW_VERSION" ||
			fail "${a##*/}: the binary holds no version $BW_VERSION"
		grep -qxE "[[:space:]]+build[[:space:]]+vcs\.revision=$EXPECT_REVISION" <<<"$info" || fail "${a##*/}: vcs.revision is not $EXPECT_REVISION"
		grep -qxE '[[:space:]]+build[[:space:]]+vcs\.modified=false' <<<"$info" || fail "${a##*/}: vcs.modified is not false"
	done
done

ctl=$work/bronzewardctl_${v}_linux_amd64/bronzewardctl
srv=$work/bronzeward_${v}_linux_amd64/bronzeward
got=$("$srv" version 2>/dev/null || true)
[ "$got" = "$BW_VERSION" ] || fail "bronzeward version reports '$got', not $BW_VERSION"
# No context is configured, so the command fails after printing the client's part.
got=$(HOME=$work/home XDG_CONFIG_HOME=$work/home "$ctl" version -o json 2>/dev/null | python3 -c 'import json, sys; print(json.load(sys.stdin)["client"]["version"])' 2>/dev/null || true)
[ "$got" = "$BW_VERSION" ] || fail "bronzewardctl version reports '$got', not $BW_VERSION"

web=$dir/bronzeward-web_$v.tar.gz
if [ -f "$web" ]; then
	tar -xzf "$web" -C "$work"
	w=$work/bronzeward-web_$v
	for f in Caddyfile bronzeward-client.example.json LICENSE-STATUS.md THIRD_PARTY_NOTICES.txt dist/index.html; do
		[ -s "$w/$f" ] || fail "${web##*/} holds no $f"
	done
	cmp -s "$w/headers.json" web/headers.json || fail "${web##*/}: headers.json is not web/headers.json"
	# The version as a whole string literal; the minifier may quote it with any of the three.
	re=${BW_VERSION//./\\.}
	re=${re//+/\\+}
	grep -qrE -- "[\"'\`]${re}[\"'\`]" "$w/dist/assets" || fail "${web##*/}: no bundle names $BW_VERSION for the footer"
else
	fail "${web##*/} is missing"
fi
[ "$status" -ne 0 ] || echo "check-build: every archive is $BW_VERSION at $EXPECT_REVISION"
exit "$status"
