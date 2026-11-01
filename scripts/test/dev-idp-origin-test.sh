#!/usr/bin/env bash
# fixtures/dev-idp up (ginsys/bronzeward#459), with no Docker: on a provider this checkout already
# runs, up keeps it only when it was seeded for this run's web origin, the browser tests' page of
# BW_E2E_PORT_BASE (default 16000) or BW_DEV_IDP_WEB_ORIGIN, and refuses it otherwise. Without
# BW_DEV_IDP_WEB_ORIGIN, up refuses a base that the browser tests refuse. docker and curl are stubs
# that report the provider running and answering, and jq is a stub. Synthetic names and values only.
set -euo pipefail

root=$(CDPATH='' cd -P -- "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)
fail=0
ok() { printf 'ok   %s\n' "$1"; }
bad() {
  printf 'FAIL %s\n' "$1" >&2
  fail=1
}

# An instance of its own, so that its state directory is no real provider's.
instance=originprobe
state=$root/$(BW_DEV_INSTANCE=$instance sh -c '. "$1" && printf %s "$BW_DEV_IDP_DIR"' sh "$root/scripts/dev/instance.sh")
[ ! -e "$state" ] || { echo "dev-idp-origin-test: $state exists; remove it first" >&2; exit 1; }
stubs=$(mktemp -d)
trap 'rm -rf -- "$state" "$stubs"' EXIT
printf '#!/bin/sh\necho synthetic-container-id\n' >"$stubs/docker"
printf '#!/bin/sh\nexit 0\n' >"$stubs/curl"
# dev-idp up checks that jq exists; a provider it keeps is not seeded again, so jq never runs.
printf '#!/bin/sh\nexit 0\n' >"$stubs/jq"
chmod +x "$stubs/docker" "$stubs/curl" "$stubs/jq"

# seed <web origin>: the state of a provider seeded for that origin.
seed() {
  mkdir -p "$state"
  printf 'synthetic-seed-token\n' >"$state/seed.pat"
  cat >"$state/idp.env" <<EOF
BW_TEST_IDP_ISSUER=http://localhost:1
BW_TEST_IDP_PROJECT=synthetic-project
BW_TEST_IDP_CLI_CLIENT=synthetic-cli
BW_TEST_IDP_WEB_CLIENT=synthetic-web
BW_TEST_IDP_WEB_ORIGIN=$1
EOF
}

# up <expected status> <label> <wanted text> [VAR=value...]: runs up with those settings.
up() {
  local want=$1 label=$2 text=$3 out rc=0
  shift 3
  out=$(env -u BW_E2E_PORT_BASE -u BW_DEV_IDP_WEB_ORIGIN PATH="$stubs:$PATH" BW_DEV_INSTANCE=$instance "$@" "$root/fixtures/dev-idp" up 2>&1) || rc=$?
  if [ "$rc" = "$want" ] && [[ $out == *"$text"* ]]; then ok "$label"; else
    bad "$label"
    printf '  status %s, output:\n%s\n' "$rc" "$out" >&2
  fi
}

seed http://localhost:16000
up 0 "unset BW_E2E_PORT_BASE keeps a provider seeded for http://localhost:16000" "(http://localhost:16000)"
up 1 "BW_E2E_PORT_BASE=16016 refuses a provider seeded for 16000 and names dev-idp-down" \
  "the running provider's web origin is http://localhost:16000, not http://localhost:16016; run BW_DEV_INSTANCE=$instance mise run dev-idp-down" BW_E2E_PORT_BASE=16016
seed http://localhost:58443
up 1 "a provider seeded for the old default 58443 is refused" "web origin is http://localhost:58443, not http://localhost:16000"
up 0 "BW_DEV_IDP_WEB_ORIGIN wins over BW_E2E_PORT_BASE" "(http://localhost:58443)" BW_E2E_PORT_BASE=16016 BW_DEV_IDP_WEB_ORIGIN=http://localhost:58443
# 18446744073709567616 is 2^64 + 16000, which bash arithmetic would read as 16000.
for b in 16001 016000 19985 15984 abc 18446744073709567616; do
  seed "http://localhost:$b"
  up 1 "BW_E2E_PORT_BASE=$b is refused, even for a provider seeded for it" "BW_E2E_PORT_BASE must be 16000+16*n" BW_E2E_PORT_BASE=$b
done
seed http://localhost:abc
up 0 "BW_DEV_IDP_WEB_ORIGIN skips the base check" "(http://localhost:abc)" BW_E2E_PORT_BASE=abc BW_DEV_IDP_WEB_ORIGIN=http://localhost:abc
exit "$fail"
