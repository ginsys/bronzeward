#!/usr/bin/env bash
# isolated_run (fixtures/lib.sh) against the installed mise, without a fixture: mise accepts the
# configuration files it names, and a global config.toml and conf.d planted where mise looks by
# default reach `mise env` without isolated_run, not with it (#446). Only git_isolated and
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
# mise's data stays the caller's, where its tools are installed.
data=${XDG_DATA_HOME:-$HOME/.local/share}

# The names of the three variables `mise env` sets, in order, comma-separated.
seen() {
  local out=$1 n
  for n in BW_ISOLATED_GLOBAL BW_ISOLATED_CONF_D BW_ISOLATED_CLONE; do
    if grep -q -- "^export $n=" <<<"$out"; then printf '%s,' "$n"; else printf ','; fi
  done
}

fail=0
control=$(cd -- "$clone" && env -u XDG_CONFIG_HOME -u MISE_CONFIG_DIR -u MISE_GLOBAL_CONFIG_FILE -u MISE_SYSTEM_CONFIG_FILE \
  HOME="$home" XDG_DATA_HOME="$data" MISE_TRUSTED_CONFIG_PATHS="$clone" MISE_CEILING_PATHS="$work" \
  mise env -s bash 2>&1) || { echo "FAIL control: mise env without isolated_run failed: $control"; exit 1; }
if [ "$(seen "$control")" = BW_ISOLATED_GLOBAL,BW_ISOLATED_CONF_D,BW_ISOLATED_CLONE, ]; then
  echo "ok   control: without isolated_run, the global config.toml and conf.d reach mise env"
else
  echo "FAIL control: the planted global configuration did not reach mise env ($(seen "$control"))"
  fail=1
fi

rc=0
got=$(HOME="$home" XDG_DATA_HOME="$data" isolated_run "$clone" mise env -s bash 2>&1) || rc=$?
if [ "$rc" -ne 0 ]; then
  echo "FAIL under isolated_run mise refused the configuration it was given (status $rc): $got"
  fail=1
elif [ "$(seen "$got")" = ,,BW_ISOLATED_CLONE, ]; then
  echo "ok   under isolated_run mise reads the clone's mise.toml, not the global config.toml or conf.d"
else
  echo "FAIL under isolated_run mise env shows $(seen "$got"), not ,,BW_ISOLATED_CLONE,"
  fail=1
fi

[ "$fail" -eq 0 ] && echo "isolated-run-test: 2 passed, 0 failed"
exit "$fail"
