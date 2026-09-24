#!/usr/bin/env bash
# Shared setup for the retention-classification runs. Sourced by the scripts beside it, never run
# on its own.
#
# It reuses fixtures/lib.sh rather than re-deriving anything: the credentials, container names,
# ports, the pinned age and sops binaries and the OpenBao CLI wrapper all come from there.
#
# Phase-0 evidence for the retention-classification experiment (ginsys/bronzeward issue 9). Not v1
# tooling.

RC_RUN_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
RC_DIR=$(dirname -- "$RC_RUN_DIR")
REPO_DIR=$(cd -- "$RC_DIR/../.." && pwd)
FIXTURES_DIR=$REPO_DIR/fixtures

# shellcheck source-path=SCRIPTDIR source=../../../fixtures/lib.sh
. "$FIXTURES_DIR/lib.sh"

# Not defaulted, as E1_OUT and E5_OUT are not: every state gets its own evidence bundle, and a
# bundle carries a PostgreSQL data directory (tens of MiB). A default under /tmp would put them in
# RAM on most systems, and one inside the checkout would be removed by fixtures/bin/down with the
# state it is evidence about.
[ -n "${RC_OUT:-}" ] ||
  die "set RC_OUT to a directory on disk, outside this checkout, with room for the evidence bundles"
case $RC_OUT in
  /*) ;;
  *) die "RC_OUT must be an absolute path" ;;
esac
[ "${RC_OUT#"$REPO_DIR"}" = "$RC_OUT" ] ||
  die "RC_OUT is inside the checkout; a bundle there would not survive fixtures/bin/down"

RC_ROWS=$RC_OUT/verdicts.tsv
RC_TRANSCRIPTS=$RC_OUT/transcripts
RC_WORK=$RC_OUT/work

rc_init() {
  mkdir -p -- "$RC_OUT" "$RC_TRANSCRIPTS" "$RC_WORK"
  [ -f "$RC_ROWS" ] ||
    printf 'n\tcandidate\tcase\tdependency\tidentity\tstate\texpected\tobserved\treason\tverdict\tcommand\ttranscript\n' >"$RC_ROWS"
}

# rc_need_fixture: refuse to run against a fixture that is not up; every row below would otherwise
# record an unknown that says nothing about the provider.
rc_need_fixture() {
  [ -d "$STATE" ] || die "no fixture state: run fixtures/bin/up first"
  [ -n "${BW_BAO_ROOT_TOKEN:-}" ] || die "the fixture published no OpenBao root token"
  [ -n "${BW_BAO_METADATA_TOKEN:-}" ] || die "the fixture published no metadata-only token"
  containers_own
  tools_verify
}

rc_next() { printf '%03d' "$(($(wc -l <"$RC_ROWS")))"; }

rc_row() { # rc_row <fields...>: one tab-separated row; a tab or newline inside a field is flattened
  local out='' f
  for f in "$@"; do
    f=${f//$'\t'/ }
    f=${f//$'\n'/ }
    out=${out:+$out$'\t'}$f
  done
  printf '%s\n' "$out" >>"$RC_ROWS"
}

# RC_STATE names the ground-truth bundle the rows that follow are judged against (rc_bundle sets it).
RC_STATE=-

# judge <candidate> <case> <dependency> <identity> <expected> -- <command...>
#
# Runs one command and records one row. Two kinds of command are judged:
#   - a classifier call, whose last output line is `class=<c> reason=<r>`: observed is <c>;
#   - any other command (a read, a decrypt): observed is ok for exit 0, denied when the provider
#     said so (HTTP 403 or "permission denied"), fail otherwise.
# The observation comes from the output and exit status alone, never from the expectation. A row
# whose observation differs from the expectation is a mismatch, recorded and counted, never fatal.
# expected `any` is for an outcome that is itself uncertain (a request that got no answer): the
# row records what was observed, and the state's bundle is what judges it.
judge() {
  local candidate=$1 case=$2 dep=$3 identity=$4 expected=$5 n transcript rc=0 observed reason=- verdict line
  shift 5
  [ "${1:-}" = -- ] || die "judge: expected -- before the command"
  shift
  n=$(rc_next)
  transcript=transcripts/$n-$candidate-$case.txt
  "$@" >"$RC_OUT/$transcript" 2>&1 || rc=$?
  line=$(grep -E '^class=[a-z]+ reason=' -- "$RC_OUT/$transcript" | tail -n 1 || true)
  if [ -n "$line" ]; then
    observed=${line#class=}
    observed=${observed%% *}
    reason=${line#* reason=}
  elif [ "$rc" -eq 0 ]; then
    observed=ok
  elif grep -qiE 'code: 403|permission denied' -- "$RC_OUT/$transcript"; then
    observed=denied
  else
    observed=fail
  fi
  if [ "$expected" = any ] || [ "$expected" = "$observed" ]; then verdict=match; else verdict=mismatch; fi
  rc_row "$n" "$candidate" "$case" "$dep" "$identity" "$RC_STATE" "$expected" "$observed" "$reason" \
    "$verdict" "$(printf '%q ' "$@")" "$transcript"
  say "row $n $candidate/$case $dep as $identity: $observed ($verdict)"
}

# absent <candidate> <state> <reason>: a state the candidate cannot be in, named rather than left
# blank (acceptance criterion 1 asks for every candidate).
absent() { rc_row "$(rc_next)" "$1" "$2" - - - n/a n/a "$3" - - -; }

rc_mismatches() { awk -F'\t' 'NR > 1 && $10 == "mismatch"' "$RC_ROWS" | wc -l; }

# Synthetic values, each with the fixture's BWSYNTH- prefix so that the fixture's leak scan finds
# any plaintext copy. They live under $RC_WORK, outside the checkout, and commands see them over
# stdin only.
rc_secret() { # rc_secret <name>: create a value once
  mkdir -p -- "$RC_WORK/values"
  [ -f "$RC_WORK/values/$1" ] && return 0
  (umask 077 && printf 'BWSYNTH-rc-%s-%s' "$1" "$(head -c 12 /dev/urandom | od -An -tx1 | tr -d ' \n')" \
    >"$RC_WORK/values/$1")
}
rc_value_file() { printf '%s' "$RC_WORK/values/$1"; }

# rc_sops <key-file|-> <sops-args...>: the pinned sops with that one age identity and no other: every
# variable through which sops finds a key is cleared, and HOME points at an empty directory, so a
# key in the caller's own configuration is never picked up (as e5_sops in the provider-capability
# run).
rc_sops() {
  local nohome=$RC_WORK/no-home
  local -a set=(HOME="$nohome" XDG_CONFIG_HOME="$nohome/.config" TZ=UTC)
  [ "$1" = - ] || set+=(SOPS_AGE_KEY_FILE="$1")
  shift
  mkdir -p -- "$nohome"
  env -u SOPS_AGE_KEY -u SOPS_AGE_KEY_CMD -u SOPS_AGE_KEY_FILE \
    -u SOPS_AGE_SSH_PRIVATE_KEY_FILE -u SOPS_AGE_SSH_PRIVATE_KEY_CMD "${set[@]}" "$CACHE/sops" "$@"
}
rc_digest() { sha256sum <"$RC_WORK/values/$1" | cut -d' ' -f1; }

# check_digest <want> <cmd...>: run a read, compare the digest of what it printed to <want>, and
# print only the verdict: the value itself must never reach a transcript.
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

# rc_bundle <label>: one fixtures/bin/evidence bundle for the state just set up, copied out of
# .state (fixtures/bin/down removes it), and named as the ground truth of the rows that follow. A
# bundle with parts unavailable is still a bundle (fixtures/bin/evidence lists them in
# unavailable.txt and exits 0): a paused or sealed provider is exactly such a state. Its exit
# status is recorded all the same.
rc_bundle() {
  local label=$1 bundle rc=0
  bundle=$("$FIXTURES_DIR/bin/evidence" 2>"$RC_OUT/states-$label.stderr") || rc=$?
  case $bundle in
    "$FIXTURES_DIR"/.state/evidence/?*) [ -d "$bundle" ] || die "fixtures/bin/evidence printed $bundle, which is not a directory" ;;
    *) die "fixtures/bin/evidence printed '$bundle' (rc=$rc), not a bundle under .state/evidence" ;;
  esac
  mkdir -p -- "$RC_OUT/states/$label"
  cp -a -- "$bundle/." "$RC_OUT/states/$label/" || die "could not copy the $label bundle out of .state"
  printf 'bundle %s rc=%s\n' "$bundle" "$rc" >"$RC_OUT/states/$label/source.txt"
  RC_STATE=$label
  say "state $label: bundle copied (fixtures/bin/evidence rc=$rc)"
}
