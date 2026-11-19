#!/usr/bin/env bash
# fixtures/dev-idp down in the native runtime (ginsys/bronzeward#927): it waits for the groups and Zitadel
# processes to exit before it removes the state, as the Docker path waits for its containers, so that a
# later up does not find a port still held. The processes are stand-ins that take 2 s to leave after TERM.
# Two cases (ginsys/bronzeward#936): init owns the stand-ins and reaps them, and a live parent that never
# waits owns them, so that an exited stand-in stays a zombie, as where PID 1 does not reap. kill -0
# still succeeds on a zombie, and down must not wait for it. Synthetic names only.
set -euo pipefail

root=$(CDPATH='' cd -P -- "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)
# An instance of its own, so that its state directory is no real provider's.
instance=downprobe
state=$root/$(BW_DEV_INSTANCE=$instance sh -c '. "$1" && printf %s "$BW_DEV_IDP_DIR"' sh "$root/scripts/dev/instance.sh")
[ ! -e "$state" ] || { echo "dev-idp-native-down-test: $state exists; remove it first" >&2; exit 1; }
holders=
trap 'for f in groups zitadel; do [ ! -s "$state/$f.pid" ] || kill -9 "$(cat "$state/$f.pid")" 2>/dev/null || true; done; for h in $holders; do kill -9 "$h" 2>/dev/null || true; done; rm -rf -- "$state"' EXIT

standin='trap "sleep 2; exit 0" TERM; while :; do sleep 0.2; done'

# running PID: the process is there and is not a zombie.
running() {
  local s
  kill -0 "$1" 2>/dev/null || return 1
  s=$(ps -o stat= -p "$1" 2>/dev/null || true)
  case $s in Z*) return 1 ;; esac
}

# case MODE: starts the two stand-ins, runs down, and checks that it waited for them, removed the state
# and, for the zombie case, did not wait the 30 s per process that a zombie costs.
fail=0
run_case() {
  local mode=$1 f pids started elapsed
  mkdir -p "$state"
  for f in groups zitadel; do
    if [ "$mode" = reaped ]; then
      # In a subshell that ends, so that init owns the process and reaps it, as it does a provider that up started.
      (sh -c "$standin" >/dev/null 2>&1 & echo $! >"$state/$f.pid")
    else
      # The parent stays and never waits: the stand-in is a zombie once it has left.
      sh -c 'sh -c "$1" >/dev/null 2>&1 & echo $! >"$2"; exec sleep 300' sh "$standin" "$state/$f.pid" &
      holders="$holders $!"
      disown "$!" # its kill at the end of the case is no news
      for _ in $(seq 50); do [ -s "$state/$f.pid" ] && break; sleep 0.1; done
    fi
  done
  pids=$(cat "$state/groups.pid" "$state/zitadel.pid")

  started=$SECONDS
  BW_DEV_INSTANCE=$instance BW_DEV_RUNTIME=native "$root/fixtures/dev-idp" down
  elapsed=$((SECONDS - started))

  for p in $pids; do
    if running "$p"; then echo "FAIL ($mode): down returned while process $p was still running" >&2; fail=1; fi
  done
  if [ -e "$state" ]; then echo "FAIL ($mode): down left $state" >&2; fail=1; fi
  if [ "$elapsed" -ge 20 ]; then echo "FAIL ($mode): down took ${elapsed}s; it waited for a process that had already left" >&2; fail=1; fi
  for h in $holders; do kill -9 "$h" 2>/dev/null || true; done
  holders=
  echo "case $mode: down took ${elapsed}s"
}

run_case reaped
run_case zombie
[ "$fail" -ne 0 ] || echo "ok   down waited for both processes, also when they stayed zombies, and removed the state"
exit "$fail"
