#!/usr/bin/env bash
# Run the web client's real-browser cases on the release's own web bundle (ginsys/bronzeward#405, the negative cases of
# ginsys/bronzeward#262 acceptance 3): TLS-02 and TLS-03's read case (web/e2e/tls.e2e.ts) and the footer case
# (web/e2e/packaged.e2e.ts), with the page served by the pinned Caddy image from the bundle's own Caddyfile and dist/
# (BW_PACKAGED_WEB, web/e2e/wdio.conf.ts), and bin/webhost -redirect-api as the API stand-in behind it. The bundle is
# <dir>/bronzeward-web_<version>.tar.gz, where <version> is BW_EXPECT_VERSION without its leading v (the name
# scripts/release/web-package.sh writes). BW_BROWSERS picks the browsers, as for `mise run browser`. Needs Docker. Two things
# keep a green run honest: the footer case compares the page's version with the one the bundle was built at, so a page built
# from source (which prints (devel)) fails it, and a run with a wrong version must fail that case, for that version.
#
#   BW_EXPECT_VERSION=v0.0.0-ci.g<sha> run-packaged-web.sh <directory holding the package-a archives>
set -euo pipefail

dir=${1:?usage: run-packaged-web.sh <directory holding the package-a archives>}
: "${BW_EXPECT_VERSION:?BW_EXPECT_VERSION is not set}"
root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
name=bronzeward-web_${BW_EXPECT_VERSION#v}
archive=$dir/$name.tar.gz
[ -f "$archive" ] || { echo "run-packaged-web: no $archive" >&2; exit 1; }
tar -xzf "$archive" -C "$work"
[ -f "$work/$name/Caddyfile" ] || { echo "run-packaged-web: $archive holds no $name/Caddyfile" >&2; exit 1; }

export BW_PACKAGED_WEB=$work/$name
BW_SPECS=tls,packaged mise run browser

# The planted defect: a version the bundle was not built at must fail the footer case.
if BW_SPECS=packaged BW_EXPECT_VERSION=v0.0.0-planted mise run browser >"$work/planted" 2>&1; then
	echo "run-packaged-web: a wrong BW_EXPECT_VERSION passed the footer case" >&2
	cat "$work/planted" >&2
	exit 1
fi
# It failed at the footer assertion, not for another cause: the runner prints the version in the case's title on any failure,
# so the evidence is the assertion's own two footers, the one expected and the one the bundle's page showed.
for footer in "Client v0.0.0-planted. Server unknown" "Client $BW_EXPECT_VERSION. Server unknown"; do
	grep -qF -e "$footer" "$work/planted" || { echo "run-packaged-web: the planted run's output lacks the footer '$footer': it did not fail at the assertion:" >&2; cat "$work/planted" >&2; exit 1; }
done
echo "run-packaged-web: the cases passed on the packaged bundle, and a wrong version failed the footer case"
