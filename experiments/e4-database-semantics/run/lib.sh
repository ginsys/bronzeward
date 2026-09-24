#!/usr/bin/env bash
# Shared setup for the E4 database-semantics runs. Sourced by the scripts beside it, never run on
# its own.
#
# It reuses fixtures/lib.sh rather than re-deriving anything: the PostgreSQL password, port and
# container, the psql wrapper that runs inside the container, and the leak-scan pattern list all
# come from there.
#
# Every row starts from an empty database, runs one or more e4db clients as separate processes,
# interrupts them or the server where the row says so, and then reads the outcome back from the
# data with a reader that shares no code with the client: psql inside the PostgreSQL container, or
# the host's sqlite3 CLI. The row's verdict compares that reading, and the clients' exit statuses,
# with an expectation written in run/all before the run.
#
# Phase-0 evidence for the database-semantics experiment (ginsys/bronzeward issue 6). Not v1 tooling.

E4_RUN_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
E4_DIR=$(dirname -- "$E4_RUN_DIR")
REPO_DIR=$(cd -- "$E4_DIR/../.." && pwd)
FIXTURES_DIR=$REPO_DIR/fixtures

# shellcheck source-path=SCRIPTDIR source=../../../fixtures/lib.sh
. "$FIXTURES_DIR/lib.sh"

# Not defaulted: it holds a copy of the fixture's leak-scan patterns, which are this run's
# secrets, so it is the operator's to place and to delete, outside the checkout.
[ -n "${E4_OUT:-}" ] || die "set E4_OUT to a directory on disk, outside this checkout"
case $E4_OUT in
  /*) ;;
  *) die "E4_OUT must be an absolute path" ;;
esac
[ "${E4_OUT#"$REPO_DIR"}" = "$E4_OUT" ] || die "E4_OUT is inside the checkout"

E4_MARK=$E4_OUT/.e4-database-semantics
E4_BIN=$E4_OUT/bin/e4db
E4_CELLS=$E4_OUT/cells.tsv
E4_T=$E4_OUT/transcripts
E4_S=$E4_OUT/state
E4_PARTS=$E4_OUT/parts
# shellcheck disable=SC2034  # read by run/all and run/collect-evidence, which source this file
E4_PATTERNS=$E4_OUT/scan-patterns.txt
# SQLite files live under the fixture's data directory, which store-snapshot and store-restore act
# on (fixtures/README.md).
E4_SQLITE_DIR=$STATE/data/e4
# Every client is bounded, so a hung connection ends the row instead of the run.
E4_CLIENT_LIMIT=120

# e4_init: a fresh output directory only. Rows are numbered from the cells file, so rows from an
# earlier run, or another experiment's files, would be counted as this run's.
e4_init() {
  if [ -e "$E4_OUT" ] && [ -n "$(ls -A -- "$E4_OUT")" ]; then
    die "E4_OUT ($E4_OUT) is not empty; give each run a new directory"
  fi
  mkdir -p -- "$E4_OUT/bin" "$E4_T" "$E4_S" "$E4_PARTS"
  printf 'e4 run output; delete after the evidence has landed\n' >"$E4_MARK"
  printf 'n\tscenario\tbackend\trow\texpected\tobserved\tverdict\ttranscript\n' >"$E4_CELLS"
}

e4_need_fixture() {
  [ -d "$STATE" ] || die "no fixture state: run fixtures/bin/up first"
  [ -n "${BW_POSTGRES_PASSWORD:-}" ] || die "the fixture published no PostgreSQL password"
  [ -s "$STATE/scan-patterns.txt" ] || die "$STATE/scan-patterns.txt is missing; the fixture is not up"
  containers_own
  need sqlite3 go timeout fuser
  mkdir -p -- "$E4_SQLITE_DIR"
}

e4_build() {
  go -C "$E4_DIR" build -o "$E4_BIN" . || die "could not build the prototype"
}

# e4_dsn: lib/pq's connection string. It carries the password, so it only ever travels in the
# environment of a client (E4_PG_DSN), never on a command line.
e4_dsn() {
  printf 'host=127.0.0.1 port=%s user=bronzeward dbname=bronzeward sslmode=disable connect_timeout=5 password=%s' \
    "$POSTGRES_PORT" "$BW_POSTGRES_PASSWORD"
}

# psql inside the PostgreSQL container, over its unix socket: the independent reader.
e4_psql() {
  pg_stdin psql --username=bronzeward --dbname=bronzeward -X -q -A -t -F '|' -v ON_ERROR_STOP=1 "$@"
}

# ---- rows ----

declare -g ROW_N ROW_ID ROW_BACKEND ROW_DB ROW_PARTS ROW_SCENARIO ROW_NAME
declare -gA RC=() PID=() R=()
declare -ga ROW_LABELS=()

e4_next() { printf '%03d' "$(($(wc -l <"$E4_CELLS")))"; }

# row_begin <scenario> <backend> <row>: an empty database, and the client environment for it.
row_begin() {
  ROW_SCENARIO=$1 ROW_BACKEND=$2 ROW_NAME=$3
  ROW_N=$(e4_next)
  ROW_ID=$ROW_N-$ROW_SCENARIO-$ROW_BACKEND-$ROW_NAME
  ROW_PARTS=$E4_PARTS/$ROW_ID
  mkdir -p -- "$ROW_PARTS"
  RC=() PID=() R=() ROW_LABELS=()
  export E4_BACKEND=$ROW_BACKEND
  unset E4_SQLITE_TXLOCK
  case $ROW_BACKEND in
    sqlite)
      ROW_DB=$E4_SQLITE_DIR/$ROW_ID.db
      rm -f -- "$ROW_DB" "$ROW_DB-wal" "$ROW_DB-shm"
      export E4_SQLITE_PATH=$ROW_DB
      unset E4_PG_DSN
      ;;
    postgres)
      ROW_DB=bronzeward
      E4_PG_DSN=$(e4_dsn)
      export E4_PG_DSN
      unset E4_SQLITE_PATH
      e4_psql -c 'DROP SCHEMA public CASCADE' -c 'CREATE SCHEMA public' >"$ROW_PARTS/reset.txt" 2>&1 ||
        die "row $ROW_ID: could not empty the PostgreSQL schema (see $ROW_PARTS/reset.txt)"
      ;;
    *) die "unknown backend $ROW_BACKEND" ;;
  esac
  say "row $ROW_ID"
}

e4_part() { printf '%s/%s.txt' "$ROW_PARTS" "$1"; }

# e4 <label> <e4db args...>: one client in the foreground; RC[label] is its exit status.
e4() {
  local label=$1 rc=0
  shift
  ROW_LABELS+=("$label")
  printf '$ e4db %s\n' "$*" >"$(e4_part "$label")"
  timeout --signal=KILL "$E4_CLIENT_LIMIT" "$E4_BIN" "$@" >>"$(e4_part "$label")" 2>&1 || rc=$?
  RC[$label]=$rc
}

# e4_bg <label> <e4db args...>: one client in the background; e4_reap collects it.
e4_bg() {
  local label=$1
  shift
  ROW_LABELS+=("$label")
  printf '$ e4db %s\n' "$*" >"$(e4_part "$label")"
  timeout --signal=KILL "$E4_CLIENT_LIMIT" "$E4_BIN" "$@" >>"$(e4_part "$label")" 2>&1 &
  PID[$label]=$!
}

e4_reap() {
  local label=$1 rc=0
  wait "${PID[$label]}" || rc=$?
  RC[$label]=$rc
}

# e4_kill <label>: SIGKILL the client (the timeout wrapper's child: the e4db process itself, so
# that no signal handler and no deferred rollback runs), then reap it.
e4_kill() {
  local label=$1 child
  child=$(pgrep -P "${PID[$label]}" -x e4db) || child=
  if [ -n "$child" ]; then kill -KILL "$child"; else kill -KILL "${PID[$label]}"; fi
  printf '# SIGKILL sent to %s\n' "${child:-${PID[$label]}}" >>"$(e4_part "$label")"
  e4_reap "$label"
}

# e4_wait_for <label> <extended regex> [seconds]: until the client's transcript shows the line.
# A client that never gets there leaves the row to its expectation, which then fails visibly.
e4_wait_for() {
  local label=$1 re=$2 limit=${3:-30} i
  for ((i = 0; i < limit * 20; i++)); do
    grep -qE -- "$re" "$(e4_part "$label")" 2>/dev/null && return 0
    sleep 0.05
  done
  printf '# e4_wait_for: no line matching %s after %ss\n' "$re" "$limit" >>"$(e4_part "$label")"
  return 1
}

# e4_inject <action> [args]: the fixture's own injection, logged in .state/injections.log.
e4_inject() {
  local label="inject-$1" rc=0
  [ -n "${RC[$label]+set}" ] && label="$label-$((${#ROW_LABELS[@]}))"
  ROW_LABELS+=("$label")
  printf '$ fixtures/bin/inject %s\n' "$*" >"$(e4_part "$label")"
  "$FIXTURES_DIR/bin/inject" "$@" >>"$(e4_part "$label")" 2>&1 || rc=$?
  RC[$label]=$rc
}

e4_start_at() { printf '%s' "$(($(date +%s%3N) + ${1:-1500}))"; }

# e4_read <reader>: run readers/<reader>.sql with the independent reader and load its name|value
# rows into R. Called once or more per row; a later reading of a key replaces the earlier one, so
# a row that reads twice names the stage in the key (see row_note).
e4_read() {
  local reader=$1 file out k v rc=0
  file=$E4_DIR/readers/$reader.sql
  [ -f "$file" ] || die "no reader $file"
  out=$ROW_PARTS/read-$reader${2:+-$2}.txt
  ROW_LABELS+=("read-$reader${2:+-$2}")
  case $ROW_BACKEND in
    sqlite) sqlite3 -bail -batch -noheader -separator '|' "$ROW_DB" <"$file" >"$out" 2>&1 || rc=$? ;;
    postgres) e4_psql -f - <"$file" >"$out" 2>&1 || rc=$? ;;
  esac
  RC[read-$reader${2:+-$2}]=$rc
  while IFS='|' read -r k v; do
    [ -n "$k" ] && R[${2:+$2.}$k]=$v
  done <"$out"
}

# row_note <key> <value>: a measured fact that is not a database reading (a quiesce check).
row_note() { R[$1]=$2; }

# row_end <expectation...>: each expectation is
#   key=value     the reading R[key] equals value
#   key>value     the reading is an integer greater than value
#   rc:label=N    client label exited N
#   out:label~re  client label's transcript matches the extended regex
#   seen:key      the reading R[key] exists; its value is recorded, not judged (a control's size)
# A row matches only if every expectation holds, and only if no client reports reaching its start
# barrier late (e4db prints late=true): a late client ran after the others, not with them. The
# observed column records the actual value of each, so a mismatch shows what was seen instead.
row_end() {
  local e key val got ok=1 observed=() t label
  for label in "${ROW_LABELS[@]}"; do
    if grep -q '^late=true' -- "$(e4_part "$label")" 2>/dev/null; then
      ok=0
      observed+=("late:$label")
    fi
  done
  for e in "$@"; do
    case $e in
      seen:*)
        key=${e#seen:}
        got=${R[$key]-unset}
        [ "$got" != unset ] || ok=0
        observed+=("$key=$got")
        ;;
      rc:*=*)
        key=${e%%=*} val=${e#*=} label=${key#rc:}
        got=${RC[$label]-unset}
        [ "$got" = "$val" ] || ok=0
        observed+=("$key=$got")
        ;;
      out:*~*)
        key=${e%%~*} val=${e#*~} label=${key#out:}
        if grep -qE -- "$val" "$(e4_part "$label")" 2>/dev/null; then got=yes; else got=no ok=0; fi
        observed+=("$key~$got")
        ;;
      *'>'*)
        key=${e%%>*} val=${e#*>}
        got=${R[$key]-unset}
        { [[ $got =~ ^[0-9]+$ ]] && [ "$got" -gt "$val" ]; } || ok=0
        observed+=("$key=$got")
        ;;
      *=*)
        key=${e%%=*} val=${e#*=}
        got=${R[$key]-unset}
        [ "$got" = "$val" ] || ok=0
        observed+=("$key=$got")
        ;;
      *) die "row $ROW_ID: unreadable expectation '$e'" ;;
    esac
  done
  t=transcripts/$ROW_ID.txt
  {
    for label in reset "${ROW_LABELS[@]}"; do
      [ -f "$(e4_part "$label")" ] || continue
      printf '==> %s (rc %s) <==\n' "$label" "${RC[$label]-}"
      cat -- "$(e4_part "$label")"
    done
  } >"$E4_OUT/$t"
  local verdict=match
  [ "$ok" = 1 ] || verdict=mismatch
  local IFS=' '
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$ROW_N" "$ROW_SCENARIO" "$ROW_BACKEND" "$ROW_NAME" \
    "$*" "${observed[*]}" "$verdict" "$t" >>"$E4_CELLS"
  say "row $ROW_ID: $verdict (${observed[*]})"
}

e4_mismatches() { awk -F'\t' 'NR > 1 && $7 == "mismatch"' "$E4_CELLS" | wc -l; }

# e4_quiesced: every client of the row's SQLite file has exited and none holds it open. A
# store-snapshot copies files, which is a valid backup only then (fixtures/README.md).
e4_quiesced() {
  local label
  for label in "${!PID[@]}"; do
    kill -0 "${PID[$label]}" 2>/dev/null && return 1
  done
  # fuser exits 1 when no process has either file open. Anything else, a missing fuser or an
  # error included, is not evidence of quiescence.
  local rc=0
  fuser -s -- "$ROW_DB" "$ROW_DB-wal" 2>/dev/null || rc=$?
  [ "$rc" = 1 ]
}
