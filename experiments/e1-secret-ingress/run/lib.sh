#!/usr/bin/env bash
# Shared setup for the E1 run matrix. Sourced by the scripts beside it, never run on its own.
#
# It reuses fixtures/lib.sh rather than re-deriving anything: the credentials, container names,
# ports and the leak-scan pattern list all come from there, so a screen here and a bundle from
# fixtures/bin/evidence are looking for the same strings by the same method. A second copy of that
# logic would be the most likely place for the two to disagree without anyone noticing.
#
# Phase-0 evidence for the secret-ingress experiment (ginsys/bronzeward issue 2). Not v1 tooling.

E1_RUN_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
E1_DIR=$(dirname -- "$E1_RUN_DIR")
REPO_DIR=$(cd -- "$E1_DIR/../.." && pwd)
FIXTURES_DIR=$REPO_DIR/fixtures

# source-path=SCRIPTDIR is required, not decoration. Without it shellcheck resolves the source path
# against its own working directory, so `shellcheck -x experiments/e1-secret-ingress/run/*` from the
# repository root looked for fixtures/lib.sh three levels above the checkout and reported SC1091.
# That is how the lint task passed locally, run from this directory, and failed in CI.
# shellcheck source-path=SCRIPTDIR source=../../../fixtures/lib.sh
. "$FIXTURES_DIR/lib.sh"

# The output directory is the operator's to choose and is deliberately not defaulted. Every bundle
# carries a PostgreSQL data directory, so a full matrix runs to hundreds of megabytes; a default
# pointing anywhere under /tmp would put that in RAM on most systems, and one inside the checkout
# would be destroyed by fixtures/bin/down along with the state it is evidence about.
[ -n "${E1_OUT:-}" ] ||
  die "set E1_OUT to a directory on disk with room for the evidence bundles (hundreds of MiB).
It must not be a tmpfs and must not sit inside this checkout: fixtures/bin/down removes .state,
and a bundle captured before a teardown has to outlive it."
case $E1_OUT in
  /*) ;;
  *) die "E1_OUT must be an absolute path" ;;
esac
[ "${E1_OUT#"$REPO_DIR"}" = "$E1_OUT" ] ||
  die "E1_OUT is inside the checkout; a bundle there would not survive fixtures/bin/down"

E1_BIN=$E1_OUT/bin/e1
E1_RUNS=$E1_OUT/runs
E1_BUNDLES=$E1_OUT/bundles
E1_REPORTS=$E1_OUT/reports

# e1_need_fixture: refuse to run against a fixture that is not up. Every assertion below reads
# .state; without it a screen would pass by finding nothing, for the wrong reason.
e1_need_fixture() {
  [ -d "$STATE" ] || die "no fixture state: run fixtures/bin/up first"
  [ -f "$STATE/scan-patterns.txt" ] ||
    die "$STATE/scan-patterns.txt is missing; the fixture is not up or its state is incomplete"
  [ -n "${BW_CANARY:-}" ] || die "the fixture published no canary; results would be unattributable"
  [ -n "${BW_POSTGRES_PASSWORD:-}" ] || die "the fixture published no database password"
  [ -n "${BW_BAO_ROOT_TOKEN:-}" ] || die "the fixture published no OpenBao token"
  # Without this the fixture's container table is empty, so pg() refuses by name and every command
  # that goes through it fails. That is how the screen's dump column came to be vacuous: e1_dump
  # failed for every row, the failure was redirected away, and a dump that was never taken was
  # counted as a dump holding nothing.
  containers_own
}

# e1_build: compile the prototype once per matrix. Its build cache and temporary files go under
# E1_OUT for the same reason the bundles do.
e1_build() {
  mkdir -p -- "$E1_OUT/bin" "$E1_OUT/gocache" "$E1_OUT/gotmp" "$E1_RUNS" "$E1_BUNDLES" "$E1_REPORTS"
  GOCACHE=$E1_OUT/gocache GOTMPDIR=$E1_OUT/gotmp TMPDIR=$E1_OUT/gotmp \
    go -C "$E1_DIR" build -o "$E1_BIN" . ||
    die "could not build the prototype"
}

# e1_env: the environment every run gets. No secret is ever an argument: argv is world-readable
# through /proc and is captured by the bundles these runs produce.
e1_env() {
  BW_E1_CANARY=$BW_CANARY \
    BW_POSTGRES_PASSWORD=$BW_POSTGRES_PASSWORD \
    BW_BAO_ROOT_TOKEN=$BW_BAO_ROOT_TOKEN \
    BW_BAO_ADDR=http://127.0.0.1:$OPENBAO_PORT \
    "$@"
}

# e1_config: the document a flow ingests. import takes the configuration the generator produced;
# adopt takes the effective configuration read back off the node, which is what design §12.4's
# drift adoption actually sees.
e1_config() {
  case $1 in
    import) printf '%s' "$STATE/controlplane.yaml" ;;
    adopt) printf '%s' "$STATE/controlplane.yaml" ;;
    *) die "unknown flow $1" ;;
  esac
}

# e1_hits <path>...: how many occurrences of this run's synthetic secrets the paths hold, counted
# the way fixtures/bin/evidence counts them — occurrences, not matching lines, against the
# fixture's own pattern file. Paths that do not exist contribute nothing.
e1_hits() {
  local found rc=0
  found=$(grep --recursive --no-messages --text --fixed-strings --only-matching \
    --file="$STATE/scan-patterns.txt" -- "$@") || rc=$?
  [ "$rc" -le 1 ] || die "the scan could not read one of its paths; results void"
  sed '/^$/d' <<<"$found" | wc -l
}

# e1_dump <file>: a fresh logical dump, for the screen. It is not a substitute for a bundle: it
# shows the live tables and says nothing about the heap, the write-ahead log or the backups, which
# is exactly where a redact-after control's evidence lives.
e1_dump() {
  pg pg_dump --username=bronzeward --dbname=bronzeward --no-password >"$1"
}

# e1_report <name>: the path a script writes its table to.
e1_report() { printf '%s' "$E1_REPORTS/$1"; }
