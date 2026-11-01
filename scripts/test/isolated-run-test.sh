#!/usr/bin/env bash
# isolated_run (fixtures/lib.sh) against the installed mise, without a fixture: mise accepts the
# configuration files it names, and a global config.toml and conf.d planted where mise looks by
# default reach `mise env` without isolated_run, not with it, and isolated_run names that same
# file for the system configuration, which no test can plant under /etc (#446). Only git_isolated and
# isolated_run are extracted; the rest of lib.sh is never run. Synthetic values only.
set -euo pipefail

root=$(CDPATH='' cd -P -- "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
awk '/^git_isolated=\(/ { copy=1 } copy { print } copy && /\)$/ { exit }' "$root/fixtures/lib.sh" >"$work/lib.sh"
awk '/^isolated_run\(\) \{/ { copy=1 } copy { print } copy && /^}$/ { exit }' "$root/fixtures/lib.sh" >>"$work/lib.sh"
grep -q '^isolated_run() {' "$work/lib.sh" && grep -q '^git_isolated=(' "$work/lib.sh"
bash -n "$work/lib.sh"
# shellcheck source=/dev/null
. "$work/lib.sh"

home=$work/home
clone=$work/clone
mkdir -p -- "$home/.config/mise/conf.d" "$clone"
printf '[env]\nBW_ISOLATED_GLOBAL = "global"\n' >"$home/.config/mise/config.toml"
printf '[env]\nBW_ISOLATED_CONF_D = "conf.d"\n' >"$home/.config/mise/conf.d/isolated.toml"
printf '[env]\nBW_ISOLATED_CLONE = "clone"\n' >"$clone/mise.toml"
# mise's data stays the caller's, where its tools are installed; its state and cache go under $work.
data=${MISE_DATA_DIR:-${XDG_DATA_HOME:-$HOME/.local/share}/mise}
dirs=(MISE_DATA_DIR="$data" MISE_STATE_DIR="$work/state/mise" MISE_CACHE_DIR="$work/cache/mise"
  XDG_STATE_HOME="$work/state" XDG_CACHE_HOME="$work/cache")

# The names of the three variables `mise env` sets, in order, comma-separated.
seen() {
  local out=$1 n
  for n in BW_ISOLATED_GLOBAL BW_ISOLATED_CONF_D BW_ISOLATED_CLONE; do
    if grep -q -- "^export $n=" <<<"$out"; then printf '%s,' "$n"; else printf ','; fi
  done
}

fail=0
# The control starts from an empty environment, as isolated_run does, so no MISE_* setting of the
# caller (MISE_CONFIG_FILE, MISE_ENV, ...) decides what it reads.
control=$(cd -- "$clone" && env -i PATH="$PATH" HOME="$home" "${dirs[@]}" \
  MISE_TRUSTED_CONFIG_PATHS="$clone" MISE_CEILING_PATHS="$work" \
  mise env -s bash 2>&1) || { echo "FAIL control: mise env without isolated_run failed: $control"; exit 1; }
if [ "$(seen "$control")" = BW_ISOLATED_GLOBAL,BW_ISOLATED_CONF_D,BW_ISOLATED_CLONE, ]; then
  echo "ok   control: without isolated_run, the global config.toml and conf.d reach mise env"
else
  echo "FAIL control: the planted global configuration did not reach mise env ($(seen "$control"))"
  fail=1
fi

rc=0
got=$(export HOME="$home" "${dirs[@]}" && isolated_run "$clone" mise env -s bash 2>&1) || rc=$?
if [ "$rc" -ne 0 ]; then
  echo "FAIL under isolated_run mise refused the configuration it was given (status $rc): $got"
  fail=1
elif [ "$(seen "$got")" = ,,BW_ISOLATED_CLONE, ]; then
  echo "ok   under isolated_run mise reads the clone's mise.toml, not the global config.toml or conf.d"
else
  echo "FAIL under isolated_run mise env shows $(seen "$got"), not ,,BW_ISOLATED_CLONE,"
  fail=1
fi

# The system configuration lives under /etc, where this test cannot plant one. The run above shows
# that mise accepts the name; this pins isolated_run to give that name for both files.
names=$(isolated_run "$clone" printenv MISE_GLOBAL_CONFIG_FILE MISE_SYSTEM_CONFIG_FILE 2>&1) || true
if [ "$names" = $'/dev/null/config.toml\n/dev/null/config.toml' ]; then
  echo "ok   under isolated_run both the global and the system configuration file are /dev/null/config.toml"
else
  echo "FAIL under isolated_run the configuration files are named: $names"
  fail=1
fi

[ "$fail" -eq 0 ] && echo "isolated-run-test: 3 passed, 0 failed"
exit "$fail"
