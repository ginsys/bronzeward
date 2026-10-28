#!/usr/bin/env bash
# Check one build's archives in $1 against the version and revision it was built from
# (ginsys/bronzeward#262 P7): SHA256SUMS matches every file; each binary holds BW_VERSION and
# records vcs.revision=EXPECT_REVISION and vcs.modified=false; the linux/amd64 binaries report
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

(cd "$dir" && sha256sum --check --strict SHA256SUMS) || fail "SHA256SUMS does not match the files"
v=${BW_VERSION#v}
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
		info=$(go version -m "$bin") || { fail "${a##*/}: no Go build information"; continue; }
		# -trimpath leaves -ldflags out of the build information, so the -X value is looked for in
		# the binary itself; the linux/amd64 ones are also run below.
		grep -qaF -- "$BW_VERSION" "$bin" || fail "${a##*/}: the binary holds no version $BW_VERSION"
		grep -qxE "[[:space:]]+build[[:space:]]+vcs\.revision=$EXPECT_REVISION" <<<"$info" || fail "${a##*/}: vcs.revision is not $EXPECT_REVISION"
		grep -qxE '[[:space:]]+build[[:space:]]+vcs\.modified=false' <<<"$info" || fail "${a##*/}: vcs.modified is not false"
	done
done

ctl=$work/bronzewardctl_${v}_linux_amd64/bronzewardctl
srv=$work/bronzeward_${v}_linux_amd64/bronzeward
if [ -x "$srv" ]; then
	[ "$("$srv" version)" = "$BW_VERSION" ] || fail "bronzeward version does not print $BW_VERSION"
fi
if [ -x "$ctl" ]; then
	# No context is configured, so the command fails after printing the client's part.
	got=$(HOME=$work/home XDG_CONFIG_HOME=$work/home "$ctl" version -o json 2>/dev/null | python3 -c 'import json, sys; print(json.load(sys.stdin)["client"]["version"])' || true)
	[ "$got" = "$BW_VERSION" ] || fail "bronzewardctl version reports '$got', not $BW_VERSION"
fi

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
