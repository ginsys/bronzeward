#!/usr/bin/env bash
# The darwin/arm64 archive of the release, installed the way the install page does and run on a macOS runner
# (ginsys/bronzeward#405, the darwin row of ginsys/bronzeward#262's executable checks). It proves install,
# the version report with the server gone, and the macOS Keychain store, read and removal, and nothing
# more: the server is Linux only and the runner has no Docker, so no workflow against a live server runs here.
#
#   BW_EXPECT_VERSION=v0.0.0-ci.g<sha> run-packaged-darwin.sh <directory holding the package-a archives>
#
# What it checks, in order:
#   1. the archive matches its line of SHA256SUMS and unpacks to a bronzewardctl built for the target;
#   2. the sign-in: `login --device` against scripts/release/stub-idp.py (a loopback test double, not a provider)
#      keeps the tokens in the Keychain of a temporary keychain made the default one, which `security` itself reads
#      back; `logout` in a new process finds the entry and sends its refresh token to the stub, and the entry is gone;
#   3. with the stub stopped, `version` prints the client's version and "Server unknown", and fails; a wrong
#      expected version must fail that check (the planted defect).
# BW_TARGET=linux_amd64 runs steps 1 and 3 on a Linux host (a rehearsal; it must be set to run off a runner).
# It runs only on GitHub Actions or with BW_TARGET set, since the Keychain half changes the default keychain.
# Written for bash 3.2, which macOS ships.
set -euo pipefail

dir=${1:?usage: run-packaged-darwin.sh <directory holding the package-a archives>}
: "${BW_EXPECT_VERSION:?BW_EXPECT_VERSION is not set}"
target=${BW_TARGET:-darwin_arm64}
root=$(cd "$(dirname "$0")/../.." && pwd)
fail() {
	echo "run-packaged-darwin: $*" >&2
	exit 1
}
if [ "$target" = darwin_arm64 ]; then
	[ "$(uname -s)-$(uname -m)" = Darwin-arm64 ] || fail "not a macOS arm64 host"
	[ -n "${GITHUB_ACTIONS:-}" ] || fail "this changes the default keychain: run it on GitHub Actions"
fi
case $target in
	darwin_arm64) kind='Mach-O 64-bit executable arm64' ;;
	linux_amd64) kind='ELF 64-bit LSB executable, x86-64' ;;
	*) fail "BW_TARGET $target is not darwin_arm64 or linux_amd64" ;;
esac

work=$(mktemp -d)
stub=''
keychain=''
old_default=''
old_list=()
contexts=''
# The exit status is the script's own, unless the runner's keychain settings cannot be put back: that fails the run.
cleanup() {
	status=$?
	[ -z "$stub" ] || kill "$stub" 2>/dev/null || true
	if [ -n "$keychain" ]; then
		restored=1
		if [ "${#old_list[@]}" -gt 0 ]; then security list-keychains -d user -s "${old_list[@]}" >/dev/null || restored=0; fi
		if [ -n "$old_default" ]; then
			security default-keychain -d user -s "$old_default" >/dev/null || restored=0
			now=$(security default-keychain -d user | sed -e 's/^[[:space:]]*"//' -e 's/"$//') || now=unreadable
			[ "$now" = "$old_default" ] || restored=0
		fi
		security delete-keychain "$keychain" >/dev/null || restored=0
		if [ "$restored" -ne 1 ]; then
			echo "run-packaged-darwin: the runner's default keychain was not put back (it was: $old_default)" >&2
			status=1
		fi
	fi
	for c in $contexts; do ctl context remove "$c" >/dev/null 2>&1 || true; done
	rm -rf "$work"
	exit "$status"
}
trap cleanup EXIT
mkdir -p "$work/bin" "$work/unpack"
# The client's config and state go under $work where the system honours XDG (Linux); macOS keeps its config in
# the account's Library, which is why the contexts are removed again on exit. Only the client gets the variables:
# a tool under mise keeps its trust state in XDG_STATE_HOME.
ctl() { XDG_CONFIG_HOME=$work/config XDG_STATE_HOME=$work/state "$work/bin/bronzewardctl" "$@"; }
# run <command...>: the output (both streams) in $out and the exit status in $rc, without ending the script.
run() {
	set +e
	out=$("$@" 2>&1)
	rc=$?
	set -e
}

# 1. Install: the archive against its line of SHA256SUMS, then unpack and install as the install page does.
archive=bronzewardctl_${BW_EXPECT_VERSION#v}_$target.tar.gz
[ -f "$dir/$archive" ] || fail "no $dir/$archive"
grep -E "^[0-9a-f]{64} [ *]$archive\$" "$dir/SHA256SUMS" >"$work/line.sums" || fail "SHA256SUMS has no line for $archive"
[ "$(wc -l <"$work/line.sums" | tr -d ' ')" = 1 ] || fail "SHA256SUMS names $archive more than once"
if command -v sha256sum >/dev/null; then
	(cd "$dir" && sha256sum --check --strict "$work/line.sums") || fail "$archive does not match SHA256SUMS"
else
	(cd "$dir" && shasum -a 256 -c "$work/line.sums") || fail "$archive does not match SHA256SUMS"
fi
tar -xzf "$dir/$archive" -C "$work/unpack" bronzewardctl
install -m 0755 "$work/unpack/bronzewardctl" "$work/bin/bronzewardctl"
file "$work/bin/bronzewardctl" | grep -F -q "$kind" || fail "bronzewardctl is not a $kind: $(file "$work/bin/bronzewardctl")"

# The stub identity provider also stands in as the server while contexts are added: `context add` wants
# GET /livez to answer 204. It is stopped before the version report, so that the server is gone.
python3 -I "$root/scripts/release/stub-idp.py" "$work/port" "$work/idp.log" &
stub=$!
for _ in $(seq 1 50); do
	[ -s "$work/port" ] && break
	sleep 0.1
done
[ -s "$work/port" ] || fail "the stub identity provider did not start"
issuer=http://127.0.0.1:$(cat "$work/port")
printf 'bwt_p.BWSYNTH-darwin\n' >"$work/token"
chmod 600 "$work/token"
run ctl context add bw-version --server "$issuer" --automation-token-file "$work/token"
contexts='bw-version'
[ "$rc" -eq 0 ] || fail "context add failed ($rc): $out"

# 2. The Keychain, with the stub up. The tests' own method (internal/client/store_darwin_keychain_test.go): a
# keychain made for the run, unlocked with a synthetic password, the default and the only one searched; the
# runner's lists are put back.
if [ "$target" = darwin_arm64 ]; then
	old_default=$(security default-keychain -d user | sed -e 's/^[[:space:]]*"//' -e 's/"$//')
	while IFS= read -r line; do
		old_list+=("$line")
	done < <(security list-keychains -d user | sed -e 's/^[[:space:]]*"//' -e 's/"$//')
	keychain=$work/bwtest.keychain-db
	security create-keychain -p BWSYNTH-keychain "$keychain"
	security set-keychain-settings "$keychain"
	security unlock-keychain -p BWSYNTH-keychain "$keychain"
	security default-keychain -d user -s "$keychain"
	security list-keychains -d user -s "$keychain"

	run ctl context add bw-keychain --server "$issuer" --issuer "$issuer" --client-id BWSYNTH-client --audience BWSYNTH-audience
	contexts='bw-version bw-keychain'
	[ "$rc" -eq 0 ] || fail "context add (oidc) failed ($rc): $out"

	# `context add` selects a context only when none is current, so bw-version stays the current one: name the other.
	run ctl --context bw-keychain login --device
	[ "$rc" -eq 0 ] || fail "login --device failed ($rc): $out"
	grep -F -q 'kept in the system credential store' <<<"$out" || fail "login did not keep the sign-in in the credential store: $out"
	if grep -E -q 'BWSYNTH-(access|refresh)' <<<"$out"; then fail "login printed a token: $out"; fi

	# The entry as the system's own tool reads it: go-keyring's format, the service and the context's name.
	secret=$(security find-generic-password -s bronzewardctl -a bw-keychain -w "$keychain") || fail "security finds no entry after login"
	case $secret in go-keyring-base64:*) ;; *) fail "the entry is not in go-keyring's format" ;; esac
	doc=$(python3 -I -c 'import base64, sys; sys.stdout.write(base64.b64decode(sys.argv[1].split(":", 1)[1]).decode())' "$secret")
	grep -F -q '"refreshToken":"BWSYNTH-refresh"' <<<"$doc" || fail "the stored entry does not hold the refresh token the stub issued"
	grep -F -q '"accessToken":"BWSYNTH-access"' <<<"$doc" || fail "the stored entry does not hold the access token the stub issued"

	# Read back in a new process: logout can only send the stub that refresh token by reading the Keychain.
	run ctl --context bw-keychain logout
	[ "$rc" -eq 0 ] || fail "logout failed ($rc): $out"
	grep -F -q 'The refresh token is revoked at the identity provider.' <<<"$out" || fail "logout did not revoke the refresh token: $out"
	grep -F -x -q '/revoke token=BWSYNTH-refresh' "$work/idp.log" || fail "the stub did not receive the stored refresh token: $(cat "$work/idp.log")"
	run security find-generic-password -s bronzewardctl -a bw-keychain -w "$keychain"
	[ "$rc" -ne 0 ] || fail "the entry is still in the keychain after logout"
	grep -F -q 'could not be found' <<<"$out" || fail "security gave another answer for the removed entry: $out"
	echo "run-packaged-darwin: login stored the sign-in in the Keychain, logout read it back, revoked it and removed it"
else
	echo "run-packaged-darwin: BW_TARGET $target: the Keychain half is not run"
fi

# 3. The server is gone: stop the stub, then the version report. The context's server now refuses connections.
kill "$stub"
wait "$stub" 2>/dev/null || true
stub=''
# check_version <version>: version fails, names the client at <version>, and says the server is unknown.
check_version() {
	run ctl --context bw-version version
	[ "$rc" -ne 0 ] || fail "version against a server that is not there exited 0: $out"
	line=$(grep -E '^Client[[:space:]]' <<<"$out" || true)
	case $line in
		Client*"bronzewardctl "*", needs API revision "*" or later") ;;
		*) fail "no client line in the version report, so the binary may not have run: $out" ;;
	esac
	case $line in
		*"bronzewardctl $1, needs API revision "*" or later") ;;
		*) fail "version mismatch: the client line is '$line', and the expected version is $1" ;;
	esac
	grep -E -q '^Server[[:space:]]+unknown$' <<<"$out" || fail "the server line is not 'unknown': $out"
}
check_version "$BW_EXPECT_VERSION"
# The planted defect: the version the archive was not built at must fail the check, naming it.
if planted=$(check_version v0.0.0-planted 2>&1); then
	fail "a wrong expected version passed the version check"
fi
grep -F -q 'version mismatch: ' <<<"$planted" || fail "the planted run failed for another reason than a version mismatch: $planted"
grep -F -q 'expected version is v0.0.0-planted' <<<"$planted" || fail "the planted run did not name the planted version: $planted"
echo "run-packaged-darwin: $archive matches SHA256SUMS, is a $kind, and prints $BW_EXPECT_VERSION with the server gone"
