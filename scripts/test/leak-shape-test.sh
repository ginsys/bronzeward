#!/usr/bin/env bash
# The leak scan's key-shape matcher (ginsys/bronzeward#943): fixtures/lib.sh's leak_shape and
# leak_shape_check, extracted and run with no fixture. The fixed list the scan already holds cannot name a
# private key the server generates per use, so a key is recognised by its shape: the PEM line `... PRIVATE
# KEY-----` as text, as base64 at each of its three byte alignments, and as hex. Synthetic keys only, made
# here with openssl; the scan itself never runs openssl. leak_scan_body below is the grep the scans run.
set -uo pipefail

root=$(CDPATH='' cd -P -- "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
work=$(CDPATH='' cd -P -- "$work" && pwd -P)
for fn in leak_shape leak_shape_check; do
  awk -v fn="$fn" '$0 ~ "^" fn "\\(\\) \\{" { copy=1 } copy { print } copy && /^}$/ { exit }' "$root/fixtures/lib.sh" >>"$work/lib.sh"
  grep -q "^$fn() {" "$work/lib.sh" || { echo "FAIL fixtures/lib.sh has no $fn"; exit 1; }
done
bash -n "$work/lib.sh"

command -v openssl >/dev/null || { echo "FAIL openssl is needed to make the synthetic keys"; exit 1; }
STATE=$work/state
mkdir -p "$STATE"
printf '%s\n' 'BWSYNTH-' 'BWSYNTH-fixed-secret-0001' >"$STATE/scan-patterns.txt"
export STATE

# The keys: an ed25519 key in PKCS8 form, the same key under the type name the Talos machinery gives an
# ed25519 key (crypto/x509/key.go: ED25519 PRIVATE KEY), and an EC key in its own form.
openssl genpkey -algorithm ed25519 >"$work/pkcs8.pem" 2>/dev/null
sed 's/PRIVATE KEY/ED25519 PRIVATE KEY/' "$work/pkcs8.pem" >"$work/ed25519.pem"
openssl ecparam -genkey -name prime256v1 -noout >"$work/ec.pem" 2>/dev/null
# Not a key: a certificate's PEM block, ordinary base64 and prose that names a key without the PEM line.
{ printf '%s\n' '-----BEGIN CERTIFICATE-----'; head -c 600 /dev/urandom | base64 -w64; printf '%s\n' '-----END CERTIFICATE-----'; } >"$work/cert.pem"
head -c 6000 /dev/urandom | base64 -w0 >"$work/random.b64"
printf 'the private key stays in OpenBao; a PRIVATE KEY is never logged and the key: field is empty\n' >"$work/prose.txt"

# assertions <lib>: the matcher in <lib> against every shape; prints one line per failed check and the number of failures.
assertions() {
  local lib=$1 bad=0 name key pad
  # shellcheck source=/dev/null
  . "$lib"
  # The call the scans make (bin/evidence, scenarios s1 and s5): the list and the shape, one grep.
  leak_scan_body() { grep --no-messages --text --quiet --fixed-strings --file="$STATE/scan-patterns.txt" --file=<(leak_shape) -- "$1"; }
  check() { # check <want 0|1> <description> <file>
    local rc=0
    leak_scan_body "$3" || rc=$?
    [ "$rc" -eq "$1" ] || { echo "FAIL $2 (leak_scan_body exit $rc, want $1)"; bad=$((bad + 1)); }
  }
  leak_shape_check || { echo "FAIL leak_shape_check failed on the matcher under test"; bad=$((bad + 1)); }
  for name in pkcs8 ed25519 ec; do
    key=$work/$name.pem
    check 0 "$name key as PEM text" "$key"
    jq -Rs '{key: .}' "$key" >"$work/$name.json"
    check 0 "$name key as a JSON string" "$work/$name.json"
    printf 'contexts:\n  c:\n    key: %s\n' "$(base64 -w0 "$key")" >"$work/$name.yaml"
    check 0 "$name key as a talosconfig's base64" "$work/$name.yaml"
    for pad in '' x xx; do
      { printf 'blob '; printf '%s%s' "$pad" "$(cat "$key")" | base64 -w0; printf ' end\n'; } >"$work/$name.pad${#pad}"
      check 0 "$name key inside base64 at alignment ${#pad}" "$work/$name.pad${#pad}"
    done
    printf '\\x%s\n' "$(od -An -tx1 "$key" | tr -d ' \n')" >"$work/$name.hex"
    check 0 "$name key as a bytea in hex" "$work/$name.hex"
  done
  # The needle alone, once, at each offset: a whole key carries it twice (BEGIN and END line), at two
  # offsets, so only a lone one tells a dropped alignment.
  for pad in '' x xx; do
    printf 'blob %s end\n' "$(printf '%sPRIVATE KEY-----tail' "$pad" | base64 -w0)" >"$work/lone.${#pad}"
    check 0 "a lone needle in base64 at alignment ${#pad}" "$work/lone.${#pad}"
  done
  # The fixed list still finds what it lists, by its own route, and a key's shape is no part of that.
  printf 'x BWSYNTH-fixed-secret-0001 y\n' >"$work/fixed.txt"
  check 0 "a listed secret (the old control)" "$work/fixed.txt"
  # What is not a key.
  check 1 "a certificate" "$work/cert.pem"
  check 1 "ordinary base64" "$work/random.b64"
  check 1 "prose that names a private key" "$work/prose.txt"
  check 2 "a file that cannot be read" "$work/absent"
  echo "$bad"
}

report=$(assertions "$work/lib.sh")
failures=${report##*$'\n'}
[ "$report" = "$failures" ] || printf '%s\n' "${report%$'\n'*}"
if [ "$failures" = 0 ]; then echo "ok   the matcher finds every key shape and none of the controls"; else echo "FAIL $failures check(s) of the matcher failed"; fi
status=$((failures != 0))

# Mutants of the matcher, each of which must fail at least one check: one alignment dropped, the needle cut
# to the words without the dashes, and the hex form dropped. A mutant that passes is a check that cannot tell.
mutant() { # mutant <name> <sed expression>
  local m=$work/mutant-$1.lib out
  sed -e "$2" "$work/lib.sh" >"$m"
  if cmp --silent -- "$m" "$work/lib.sh"; then echo "FAIL mutant $1 did not change the helper"; status=1; return; fi
  out=$(assertions "$m" | tail -n 1)
  if [ "$out" -gt 0 ] 2>/dev/null; then echo "ok   mutant $1 is caught ($out failing checks)"; else echo "FAIL mutant $1 passes every check"; status=1; fi
}
mutant alignment "s/for pad in '' x xx/for pad in '' x/"
mutant dashes 's/needle="PRIV""ATE KEY-----"/needle="PRIV""ATE KEY"/'
# shellcheck disable=SC2016 # the sed expression is a literal pattern, not an expansion
mutant hex 's/ "\$hex" "\${hex^^}"//'
exit "$status"
