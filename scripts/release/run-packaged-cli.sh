#!/usr/bin/env bash
# Run the CLI conformance cases of internal/clitest on the release archives' own binaries (ginsys/bronzeward#405, the
# negative cases of ginsys/bronzeward#262 acceptance 3): TLS-01, TLS-02, COMP-01 and COMP-02, and TestPackagedVersion, which
# checks both binaries' version and the client's answer with the server gone. The binaries are unpacked from
# <dir>/bronzewardctl_<version>_linux_amd64.tar.gz and <dir>/bronzeward_<version>_linux_amd64.tar.gz, where <version> is
# BW_EXPECT_VERSION without its leading v (the archive names of .goreleaser.yaml), and the cases reach them through
# BW_BIN_DIR. A disposable PostgreSQL must be up (mise run dev-db). Three things keep a green run honest: a case that
# the -run filter matched nothing of fails the run, the version case compares with the version the archives were built at,
# and a run with a wrong version must fail (the planted defect).
#
#   BW_EXPECT_VERSION=v0.0.0-ci.g<sha> run-packaged-cli.sh <directory holding the package-a archives>
set -euo pipefail

dir=${1:?usage: run-packaged-cli.sh <directory holding the package-a archives>}
: "${BW_EXPECT_VERSION:?BW_EXPECT_VERSION is not set}"
root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"
# shellcheck disable=SC1091 # the dev services' instance, sourced as mise run go-db does
. scripts/dev/instance.sh
: "${BW_TEST_PG_DSN:=$BW_DEV_PG_DSN}"
export BW_TEST_PG_DSN BW_REQUIRE_PG=1 BW_REQUIRE_PACKAGED=1

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
plain=${BW_EXPECT_VERSION#v}
for name in bronzewardctl bronzeward; do
	archive=$dir/${name}_${plain}_linux_amd64.tar.gz
	[ -f "$archive" ] || { echo "run-packaged-cli: no $archive" >&2; exit 1; }
	tar -xzf "$archive" -C "$work" "$name"
done

# gotest <wanted passes> <go test arguments...>: run, print, and count the cases that passed.
gotest() {
	local want=$1 got
	shift
	BW_BIN_DIR=$work go test -count=1 -timeout 15m -v ./internal/clitest "$@" | tee "$work/out"
	got=$(grep -c -e '^ *--- PASS: ' "$work/out" || true)
	if [ "$got" -ne "$want" ]; then
		echo "run-packaged-cli: $got cases passed, want $want ($*)" >&2
		exit 1
	fi
}

gotest 5 -run 'TestSharedCases/(TLS-01|TLS-02|COMP-01|COMP-02)$'
gotest 1 -run 'TestPackagedVersion$'

# The planted defect: the version the archives were not built at must fail the version case.
if BW_EXPECT_VERSION=v0.0.0-planted BW_BIN_DIR=$work go test -count=1 -timeout 15m ./internal/clitest -run 'TestPackagedVersion$' >"$work/planted" 2>&1; then
	echo "run-packaged-cli: a wrong BW_EXPECT_VERSION passed TestPackagedVersion" >&2
	cat "$work/planted" >&2
	exit 1
fi
# It failed for the version, not for another cause: the failure names the version it was given.
grep -q -e 'v0.0.0-planted' "$work/planted" || { echo "run-packaged-cli: the planted run failed without naming its version:" >&2; cat "$work/planted" >&2; exit 1; }
echo "run-packaged-cli: the cases passed on the packaged binaries, and a wrong version failed the version case"
