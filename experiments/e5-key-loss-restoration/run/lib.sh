#!/usr/bin/env bash
# Shared setup for the key-loss and restoration runs. Sourced by the scripts beside it, never run on
# its own.
#
# It reuses fixtures/lib.sh rather than re-deriving anything: the credentials, container names, the
# OpenBao and PostgreSQL wrappers and the snapshot/restore injections all come from there.
#
# Phase-0 evidence for the key-loss and restoration experiment (ginsys/bronzeward issue 10). Not v1
# tooling.

KL_RUN_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
KL_DIR=$(dirname -- "$KL_RUN_DIR")
REPO_DIR=$(cd -- "$KL_DIR/../.." && pwd)
FIXTURES_DIR=$REPO_DIR/fixtures

# shellcheck source-path=SCRIPTDIR source=../../../fixtures/lib.sh
. "$FIXTURES_DIR/lib.sh"

# Not defaulted: every state gets its own evidence bundle, and a bundle carries a PostgreSQL data
# directory (tens of MiB). A default under /tmp would put them in RAM on most systems, and one
# inside the checkout would be removed by fixtures/bin/down with the state it is evidence about.
[ -n "${KL_OUT:-}" ] ||
  die "set KL_OUT to a directory on disk, outside this checkout, with room for the evidence bundles"
case $KL_OUT in
  /*) ;;
  *) die "KL_OUT must be an absolute path" ;;
esac
# On a path boundary: a sibling such as <checkout>-evidence is outside it.
case $KL_OUT/ in
  "$REPO_DIR"/*) die "KL_OUT is inside the checkout; a bundle there would not survive fixtures/bin/down" ;;
esac

KL_ROWS=$KL_OUT/verdicts.tsv
KL_TRANSCRIPTS=$KL_OUT/transcripts
KL_WORK=$KL_OUT/work
# The management credentials live in the local store directory, so that store-snapshot and
# store-restore carry them as the third backup family.
# shellcheck disable=SC2034 # used by the scripts that source this file
KL_CREDS=$STATE/data/kl-creds

kl_init() {
  mkdir -p -- "$KL_OUT" "$KL_TRANSCRIPTS" "$KL_WORK"
  [ -f "$KL_ROWS" ] ||
    printf 'n\tcase\trelease\taction\tidentity\tstate\texpected\tobserved\treason\tverdict\tcommand\ttranscript\n' >"$KL_ROWS"
}

# kl_sql <statement>: one statement against the management database, tab-separated, unaligned. Only
# ever given identifiers, digests and ciphertexts, never a value.
kl_sql() { pg psql -U bronzeward -d bronzeward -v ON_ERROR_STOP=1 -AtF $'\t' -c "$1"; }

# kl_need_fixture: refuse to run against a fixture that is not up.
kl_need_fixture() {
  [ -d "$STATE" ] || die "no fixture state: run fixtures/bin/up first"
  [ -n "${BW_BAO_ROOT_TOKEN:-}" ] || die "the fixture published no OpenBao root token"
  containers_own
  tools_verify
}

kl_next() { printf '%03d' "$(($(wc -l <"$KL_ROWS")))"; }

kl_row() { # kl_row <fields...>: one tab-separated row; a tab or newline inside a field is flattened
  local out='' f
  for f in "$@"; do
    f=${f//$'\t'/ }
    f=${f//$'\n'/ }
    out=${out:+$out$'\t'}$f
  done
  printf '%s\n' "$out" >>"$KL_ROWS"
}

# KL_STATE names the ground-truth bundle the rows that follow are judged against (kl_bundle sets it).
KL_STATE=-

# judge <case> <release> <action> <identity> <expected> -- <command...>
#
# Runs one command and records one row. A command whose last output line is
# `class=<c> reason=<r>` (the recovery check) is observed as <c>; any other command is observed as
# ok for exit 0, denied when the provider said so (the bao CLI's `Code: 403`, case-sensitive, so
# that a local "Permission denied" on a file is not taken for the provider's), fail otherwise. The observation comes from the output and exit status alone, never from the
# expectation. A mismatch is recorded and counted, never fatal.
judge() {
  local case=$1 release=$2 action=$3 identity=$4 expected=$5 n transcript rc=0 observed reason=- verdict line
  shift 5
  [ "${1:-}" = -- ] || die "judge: expected -- before the command"
  shift
  n=$(kl_next)
  transcript=transcripts/$n-$case-$action.txt
  "$@" >"$KL_OUT/$transcript" 2>&1 || rc=$?
  line=$(grep -E '^class=[a-z-]+ reason=' -- "$KL_OUT/$transcript" | tail -n 1 || true)
  if [ -n "$line" ]; then
    observed=${line#class=}
    observed=${observed%% *}
    reason=${line#* reason=}
  elif [ "$rc" -eq 0 ]; then
    observed=ok
  elif grep -qF 'Code: 403' -- "$KL_OUT/$transcript"; then
    observed=denied
  else
    observed=fail
  fi
  if [ "$expected" = "$observed" ]; then verdict=match; else verdict=mismatch; fi
  kl_row "$n" "$case" "$release" "$action" "$identity" "$KL_STATE" "$expected" "$observed" "$reason" \
    "$verdict" "$(printf '%q ' "$@")" "$transcript"
  say "row $n $case/$action $release as $identity: $observed ($verdict)"
}

kl_mismatches() { awk -F'\t' 'NR > 1 && $10 == "mismatch"' "$KL_ROWS" | wc -l; }

# Synthetic values, each with the fixture's BWSYNTH- prefix so that the fixture's leak scan finds
# any plaintext copy. They live under $KL_WORK, outside the checkout and outside every backup
# family, and commands see them over stdin only.
kl_secret() { # kl_secret <name>: create a value once
  mkdir -p -- "$KL_WORK/values"
  [ -f "$KL_WORK/values/$1" ] && return 0
  (umask 077 && printf 'BWSYNTH-kl-%s-%s' "$1" "$(head -c 12 /dev/urandom | od -An -tx1 | tr -d ' \n')" \
    >"$KL_WORK/values/$1")
}
kl_value_file() { printf '%s' "$KL_WORK/values/$1"; }

# render: the synthetic configuration artifact for one source value, read on stdin. Deterministic,
# so that a regeneration from the same source version reproduces the artifact's digest.
render() { printf 'machine:\n  token: '; cat; printf '\n'; }

# kl_bundle <label>: one fixtures/bin/evidence bundle for the state just set up, copied out of
# .state (fixtures/bin/down removes it), and named as the ground truth of the rows that follow.
kl_bundle() {
  local label=$1 bundle rc=0
  bundle=$("$FIXTURES_DIR/bin/evidence" 2>"$KL_OUT/states-$label.stderr") || rc=$?
  case $bundle in
    "$FIXTURES_DIR"/.state/evidence/?*) [ -d "$bundle" ] || die "fixtures/bin/evidence printed $bundle, which is not a directory" ;;
    *) die "fixtures/bin/evidence printed '$bundle' (rc=$rc), not a bundle under .state/evidence" ;;
  esac
  mkdir -p -- "$KL_OUT/states/$label"
  cp -a -- "$bundle/." "$KL_OUT/states/$label/" || die "could not copy the $label bundle out of .state"
  printf 'bundle %s rc=%s\n' "$bundle" "$rc" >"$KL_OUT/states/$label/source.txt"
  KL_STATE=$label
  say "state $label: bundle copied (fixtures/bin/evidence rc=$rc)"
}
