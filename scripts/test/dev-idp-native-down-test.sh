#!/usr/bin/env bash
# fixtures/dev-idp down in the native runtime (ginsys/bronzeward#927): it waits for the groups and Zitadel
# processes to exit before it removes the state, as the Docker path waits for its containers, so that a
# later up does not find a port still held. The processes are stand-ins that take 2 s to leave after TERM.
# Synthetic names only.
set -euo pipefail

root=$(CDPATH='' cd -P -- "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)
# An instance of its own, so that its state directory is no real provider's.
instance=downprobe
state=$root/$(BW_DEV_INSTANCE=$instance sh -c '. "$1" && printf %s "$BW_DEV_IDP_DIR"' sh "$root/scripts/dev/instance.sh")
[ ! -e "$state" ] || { echo "dev-idp-native-down-test: $state exists; remove it first" >&2; exit 1; }
trap 'for f in groups zitadel; do [ ! -s "$state/$f.pid" ] || kill -9 "$(cat "$state/$f.pid")" 2>/dev/null || true; done; rm -rf -- "$state"' EXIT

mkdir -p "$state"
for f in groups zitadel; do
  # In a subshell that ends, so that init owns the process and reaps it, as it does a provider that up started.
  (sh -c 'trap "sleep 2; exit 0" TERM; while :; do sleep 0.2; done' >/dev/null 2>&1 & echo $! >"$state/$f.pid")
done
pids=$(cat "$state/groups.pid" "$state/zitadel.pid")

BW_DEV_INSTANCE=$instance BW_DEV_RUNTIME=native "$root/fixtures/dev-idp" down

fail=0
for p in $pids; do
  if kill -0 "$p" 2>/dev/null; then echo "FAIL: down returned while process $p was still running" >&2; fail=1; fi
done
if [ -e "$state" ]; then echo "FAIL: down left $state" >&2; fail=1; fi
[ "$fail" -ne 0 ] || echo "ok   down waited for both processes and removed the state"
exit "$fail"
