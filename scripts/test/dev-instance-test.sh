#!/usr/bin/env bash
# scripts/dev/instance.sh (ginsys/bronzeward#458), run under /bin/sh as the mise tasks run it, with no
# Docker: the default instance gets its names and its ports in the 11000 block (ginsys/bronzeward#464),
# a named instance gets its suffix and
# slot ports, a bad name is refused, no two instances share a container name or a port, and no dev
# task or fixtures/dev-idp hardcodes a default name or port outside the helper. Synthetic names only.
set -euo pipefail

root=$(CDPATH='' cd -P -- "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)
helper=$root/scripts/dev/instance.sh
fail=0

ok() { printf 'ok   %s\n' "$1"; }
bad() {
  printf 'FAIL %s\n' "$1" >&2
  fail=1
}
# expect <label> <wanted> <got>
expect() {
  if [ "$2" = "$3" ]; then ok "$1"; else
    bad "$1"
    printf '  wanted:\n%s\n  got:\n%s\n' "$2" "$3" >&2
  fi
}

# resolve <value of BW_DEV_INSTANCE | -unset>: the helper's BW_DEV_ variables, sorted, as /bin/sh
# prints them after sourcing it; the exit status is the helper's.
resolve() {
  local inst=() stale=(BW_DEV_PG_PORT=1 BW_DEV_IDP_DIR=stale)
  [ "$1" = -unset ] || inst=(BW_DEV_INSTANCE="$1")
  # shellcheck disable=SC2016 # $1 is the inner shell's: the helper's path
  env -i PATH="$PATH" "${stale[@]}" "${inst[@]}" sh -c '. "$1" || exit 1; env' sh "$helper" | sort | grep -e '^BW_DEV_' | grep -v -e '^BW_DEV_INSTANCE='
}

default=$'BW_DEV_BAO_ADDR=http://127.0.0.1:11200
BW_DEV_BAO_CONTAINER=bw-dev-bao
BW_DEV_BAO_PORT=11200
BW_DEV_IDP_CONTAINER=bw-dev-idp
BW_DEV_IDP_DIR=.dev/idp
BW_DEV_IDP_GROUPS_CONTAINER=bw-dev-idp-groups
BW_DEV_IDP_ISSUER=http://localhost:11080
BW_DEV_IDP_NET=bw-dev-idp
BW_DEV_IDP_PG_CONTAINER=bw-dev-idp-pg
BW_DEV_IDP_PORT=11080
BW_DEV_PFX=
BW_DEV_PG_CONTAINER=bw-dev-pg
BW_DEV_PG_DSN=postgres://bronzeward@127.0.0.1:11432/bronzeward?sslmode=disable
BW_DEV_PG_PORT=11432'

expect "unset is the default instance's names and ports (and overwrites a stale environment)" "$default" "$(resolve -unset)"
# ginsys/bronzeward#464: the default ports, and 11081 that ginsys/bronzeward#367 takes as the
# identity provider's port + 1, lie in the 11000 block: below the Linux ephemeral range (32768),
# below every named slot (12000 and up) and below the browser tests' 15000 to 19999.
for p in $(sed -n 's/^BW_DEV_[A-Z]*_PORT=//p' <<<"$default") $(($(sed -n 's/^BW_DEV_IDP_PORT=//p' <<<"$default") + 1)); do
  if [ "$p" -ge 11000 ] && [ "$p" -lt 12000 ]; then ok "default port $p is in the 11000 block"; else bad "default port $p is in the 11000 block"; fi
done
expect "an empty BW_DEV_INSTANCE is the default instance" "$default" "$(resolve '')"

# cksum("bww2") mod 1000 = 459.
named=$'BW_DEV_BAO_ADDR=http://127.0.0.1:13459
BW_DEV_BAO_CONTAINER=bw-dev-bao-bww2
BW_DEV_BAO_PORT=13459
BW_DEV_IDP_CONTAINER=bw-dev-idp-bww2
BW_DEV_IDP_DIR=.dev/idp-bww2
BW_DEV_IDP_GROUPS_CONTAINER=bw-dev-idp-groups-bww2
BW_DEV_IDP_ISSUER=http://localhost:14459
BW_DEV_IDP_NET=bw-dev-idp-bww2
BW_DEV_IDP_PG_CONTAINER=bw-dev-idp-pg-bww2
BW_DEV_IDP_PORT=14459
BW_DEV_PFX=BW_DEV_INSTANCE=bww2<sp>
BW_DEV_PG_CONTAINER=bw-dev-pg-bww2
BW_DEV_PG_DSN=postgres://bronzeward@127.0.0.1:12459/bronzeward?sslmode=disable
BW_DEV_PG_PORT=12459'
named=${named//<sp>/ } # BW_DEV_PFX ends in a space, which an editor may strip from the literal
expect "a named instance gets its suffix and its slot's ports" "$named" "$(resolve bww2)"

for n in UPPER a-b x_y pg groups abcdefghijklmnopq 'a b' '.' '-x'; do
  if out=$(resolve "$n" 2>/dev/null); then bad "refuses BW_DEV_INSTANCE='$n'"; else
    expect "refuses BW_DEV_INSTANCE='$n' without exporting anything" "" "$out"
  fi
done
if ok16=$(resolve abcdefghijklmnop 2>/dev/null) && [ -n "$ok16" ]; then ok "accepts 16 characters"; else bad "accepts 16 characters"; fi
# shellcheck disable=SC2016 # $1 is the inner shell's: the helper's path
msg=$(env -i PATH="$PATH" BW_DEV_INSTANCE=pg sh -c '. "$1"' sh "$helper" 2>&1 || true)
case $msg in *BW_DEV_INSTANCE=pg*) ok "the refusal names the setting and the value" ;; *) bad "the refusal names the setting and the value: $msg" ;; esac

# Many names: ports in their bands and below 32768, one slot per name, no shared container name.
# (Two names on one slot share ports by design: the second start fails on the taken port.)
names=(a b idp bao pgx bww1 bww2 bww3 bww4 x1 x2)
for i in $(seq 0 150); do names+=("n$i"); done
declare -A seen_container=()
out=
get() { sed -n "s/^$1=//p" <<<"$out"; }
for n in "${names[@]}"; do
  out=$(resolve "$n")
  pg=$(get BW_DEV_PG_PORT) bao=$(get BW_DEV_BAO_PORT) idp=$(get BW_DEV_IDP_PORT)
  slot=$((pg - 12000))
  if [ "$slot" -ge 0 ] && [ "$slot" -lt 1000 ] && [ "$bao" -eq $((13000 + slot)) ] && [ "$idp" -eq $((14000 + slot)) ] && [ "$idp" -lt 32768 ]; then
    :
  else
    bad "ports of $n are in their bands: $pg $bao $idp"
  fi
  for c in PG_CONTAINER BAO_CONTAINER IDP_CONTAINER IDP_PG_CONTAINER IDP_GROUPS_CONTAINER; do
    v=$(get BW_DEV_$c)
    if [ -n "${seen_container[$v]:-}" ]; then bad "container $v is shared by $n and ${seen_container[$v]}"; fi
    seen_container[$v]=$n
  done
done
for c in PG_CONTAINER BAO_CONTAINER IDP_CONTAINER IDP_PG_CONTAINER IDP_GROUPS_CONTAINER; do
  v=$(sed -n "s/^BW_DEV_$c=//p" <<<"$default")
  if [ -n "${seen_container[$v]:-}" ]; then bad "the default container $v is also ${seen_container[$v]}'s"; fi
done
ok "${#names[@]} instance names: bands, slots and container names checked against each other and the default"

# No dev task or dev-idp hardcodes what the helper owns: a default container name or port, or the
# state directory, outside a comment or a task description.
code() { grep -v -e '^[[:space:]]*#' -e '^description = ' "$1" || true; }
hard=$(code "$root/mise.toml" | grep -n -e '11432' -e '11200' -e '11080' -e '55433' -e '58201' -e '58280' -e 'bw-dev-pg' -e 'bw-dev-bao' -e 'bw-dev-idp' -e '\.dev/idp' || true)
expect "mise.toml hardcodes no default name, port or idp state directory" "" "$hard"
hard=$(code "$root/fixtures/dev-idp" | grep -n -e '11432' -e '11200' -e '11080' -e '55433' -e '58201' -e '58280' -e 'bw-dev-pg' -e 'bw-dev-bao' -e 'bw-dev-idp' -e '\.dev/idp' || true)
expect "fixtures/dev-idp hardcodes no default name, port or idp state directory" "" "$hard"

exit "$fail"
