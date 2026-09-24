#!/usr/bin/env bash
# Shared setup for the E5 provider-capability runs. Sourced by the scripts beside it, never run on
# its own.
#
# It reuses fixtures/lib.sh rather than re-deriving anything: the credentials, container names,
# ports, the pinned age and sops binaries and the OpenBao CLI wrapper all come from there.
#
# Phase-0 evidence for the provider-capability experiment (ginsys/bronzeward issue 8). Not v1 tooling.

E5_RUN_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
E5_DIR=$(dirname -- "$E5_RUN_DIR")
REPO_DIR=$(cd -- "$E5_DIR/../.." && pwd)
FIXTURES_DIR=$REPO_DIR/fixtures

# source-path=SCRIPTDIR: shellcheck resolves the path against the script, not its own working
# directory, so the lint task finds fixtures/lib.sh from the repository root as well.
# shellcheck source-path=SCRIPTDIR source=../../../fixtures/lib.sh
. "$FIXTURES_DIR/lib.sh"

# Not defaulted, as E1_OUT is not: a bundle carries a PostgreSQL data directory (tens of MiB), a
# default under /tmp would put it in RAM on most systems, and one inside the checkout would be
# removed by fixtures/bin/down with the state it is evidence about.
[ -n "${E5_OUT:-}" ] ||
  die "set E5_OUT to a directory on disk, outside this checkout, with room for the evidence bundles"
case $E5_OUT in
  /*) ;;
  *) die "E5_OUT must be an absolute path" ;;
esac
[ "${E5_OUT#"$REPO_DIR"}" = "$E5_OUT" ] ||
  die "E5_OUT is inside the checkout; a bundle there would not survive fixtures/bin/down"

E5_CELLS=$E5_OUT/cells.tsv
E5_TRANSCRIPTS=$E5_OUT/transcripts

e5_init() {
  mkdir -p -- "$E5_OUT" "$E5_TRANSCRIPTS"
  [ -f "$E5_CELLS" ] ||
    printf 'n\tcandidate\tcolumn\tidentity\tkind\texpected\tobserved\trc\tverdict\tcommand\ttranscript\n' >"$E5_CELLS"
}

# e5_need_fixture: refuse to run against a fixture that is not up; every cell below would otherwise
# record a failure that says nothing about the provider.
e5_need_fixture() {
  [ -d "$STATE" ] || die "no fixture state: run fixtures/bin/up first"
  [ -n "${BW_BAO_ROOT_TOKEN:-}" ] || die "the fixture published no OpenBao root token"
  [ -n "${BW_BAO_METADATA_TOKEN:-}" ] || die "the fixture published no metadata-only token"
  containers_own
  tools_verify
}

e5_next() { # the next cell number
  local n
  n=$(($(wc -l <"$E5_CELLS")))
  printf '%03d' "$n"
}

e5_row() { # e5_row <fields...>: one tab-separated row; a tab or newline inside a field is flattened
  local out='' f
  for f in "$@"; do
    f=${f//$'\t'/ }
    f=${f//$'\n'/ }
    out=${out:+$out$'\t'}$f
  done
  printf '%s\n' "$out" >>"$E5_CELLS"
}

# cell <candidate> <column> <identity> <expected> <kind> -- <command...>
#
# Runs one command, keeps its whole output as a transcript, and records one row. The observed
# outcome comes from the exit status and the transcript only, never from the expectation:
#   ok      exit 0
#   denied  non-zero, and the provider said so (HTTP 403 or "permission denied")
#   fail    any other non-zero exit
# expected is ok, denied, fail or any; a row whose observation differs is a mismatch, recorded and
# counted, never fatal, so that one surprise does not hide the cells after it.
# kind is the acceptance-criterion-2 label: primitive, versioned, metadata or unsupported.
# stdin is the command's: a secret value goes in there, never on the command line.
cell() {
  local candidate=$1 column=$2 identity=$3 expected=$4 kind=$5 n transcript rc=0 observed verdict
  shift 5
  [ "${1:-}" = -- ] || die "cell: expected -- before the command"
  shift
  n=$(e5_next)
  transcript=transcripts/$n-$candidate-$column.txt
  "$@" >"$E5_OUT/$transcript" 2>&1 || rc=$?
  if [ "$rc" -eq 0 ]; then
    observed=ok
  elif grep -qiE 'code: 403|permission denied' -- "$E5_OUT/$transcript"; then
    observed=denied
  else
    observed=fail
  fi
  if [ "$expected" = any ] || [ "$expected" = "$observed" ]; then verdict=match; else verdict=mismatch; fi
  e5_row "$n" "$candidate" "$column" "$identity" "$kind" "$expected" "$observed" "$rc" "$verdict" \
    "$(printf '%q ' "$@")" "$transcript"
  say "cell $n $candidate/$column as $identity: $observed ($verdict)"
}

# unsupported <candidate> <column> <reason>: a capability the candidate does not have, named
# rather than left blank (issue 8, acceptance criterion 3).
unsupported() {
  e5_row "$(e5_next)" "$1" "$2" - unsupported - n/a - - "$3" -
}

e5_mismatches() { awk -F'\t' 'NR > 1 && $9 == "mismatch"' "$E5_CELLS" | wc -l; }

# Synthetic values. Each starts with the fixture's BWSYNTH- prefix, so the fixture's own leak scan
# finds any plaintext copy. They live under $E5_OUT/work, outside the checkout, and are never
# committed; commands see them over stdin only.
E5_WORK=$E5_OUT/work
e5_secret() { # e5_secret <name>: create a value once
  mkdir -p -- "$E5_WORK/values"
  [ -f "$E5_WORK/values/$1" ] && return 0
  (umask 077 && printf 'BWSYNTH-e5-%s-%s' "$1" "$(head -c 12 /dev/urandom | od -An -tx1 | tr -d ' \n')" \
    >"$E5_WORK/values/$1")
}
e5_value_file() { printf '%s' "$E5_WORK/values/$1"; }

# e5_sops <key-file|-> <sops-args...>: sops holding exactly one age identity, or none (-). Left to
# itself, sops also reads SOPS_AGE_KEY*, the keys.txt under its config directory, and the caller's
# ~/.ssh/id_ed25519 and ~/.ssh/id_rsa as age identities (its own error lists those locations), so
# HOME and XDG_CONFIG_HOME point at an empty directory and every key variable is cleared.
e5_sops() {
  local nohome=$E5_WORK/no-home
  local -a set=(HOME="$nohome" XDG_CONFIG_HOME="$nohome/.config")
  [ "$1" = - ] || set+=(SOPS_AGE_KEY_FILE="$1")
  shift
  mkdir -p -- "$nohome"
  env -u SOPS_AGE_KEY -u SOPS_AGE_KEY_CMD -u SOPS_AGE_KEY_FILE \
    -u SOPS_AGE_SSH_PRIVATE_KEY_FILE -u SOPS_AGE_SSH_PRIVATE_KEY_CMD "${set[@]}" "$CACHE/sops" "$@"
}
e5_digest() { sha256sum <"$E5_WORK/values/$1" | cut -d' ' -f1; }

# check_digest <want> <cmd...>: run a read, compare the digest of what it printed to <want>, and
# print only the verdict: the value itself must never reach a transcript. The read's stderr passes
# through, so a denial is still seen as one.
check_digest() {
  local want=$1 got
  shift
  got=$("$@" | sha256sum | cut -d' ' -f1) || return 1
  if [ "$got" = "$want" ]; then
    echo "value digest matches"
  else
    echo "value digest differs"
    return 1
  fi
}

# OpenBao identities. The fixture supplies the root and metadata-only tokens; the experiment adds
# one policy per role the specification names, so that the permission matrix is measured under
# least privilege rather than asserted:
#   compiler   reads source secrets and encrypts artifacts (design §7.4 step 2-3)
#   executor   decrypts artifacts only (docs/spec/execution-recovery.md §3.1)
#   publisher  creates new immutable generations, never overwrites
#   metadata   the fixture's metadata-only token (design §7.6)
#   admin      the root token, standing for the operator's administrator
declare -A E5_TOKEN=()
e5_bao_identities() {
  local role
  BAO_TOKEN=$BW_BAO_ROOT_TOKEN bao_stdin policy write bw-e5-compiler - >/dev/null <<'POLICY'
path "secret/data/*"               { capabilities = ["read"] }
path "transit/encrypt/bw-artifact" { capabilities = ["update"] }
POLICY
  BAO_TOKEN=$BW_BAO_ROOT_TOKEN bao_stdin policy write bw-e5-executor - >/dev/null <<'POLICY'
path "transit/decrypt/bw-artifact" { capabilities = ["update"] }
POLICY
  BAO_TOKEN=$BW_BAO_ROOT_TOKEN bao_stdin policy write bw-e5-publisher - >/dev/null <<'POLICY'
path "secret/data/gen/*" { capabilities = ["create"] }
POLICY
  for role in compiler executor publisher; do
    E5_TOKEN[$role]=$(BAO_TOKEN=$BW_BAO_ROOT_TOKEN bao token create -policy="bw-e5-$role" -format=json |
      jq -er .auth.client_token) || die "could not create the $role token"
  done
  E5_TOKEN[metadata]=$BW_BAO_METADATA_TOKEN
  E5_TOKEN[admin]=$BW_BAO_ROOT_TOKEN
}

# as <identity> <cmd...>: run one command under that identity's token. The token goes through the
# environment, never argv, and the identity's name is what the cell's command column shows.
as() {
  local role=$1
  shift
  [ -n "${E5_TOKEN[$role]:-}" ] || die "no token for identity $role"
  BAO_TOKEN=${E5_TOKEN[$role]} "$@"
}
