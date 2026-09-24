#!/usr/bin/env bash
# Shared setup for the E4 dispatch-safety runs. Sourced by the scripts beside it, never run on its
# own.
#
# It reuses fixtures/lib.sh rather than re-deriving anything: the PostgreSQL password and port, the
# psql wrapper that runs inside the container, the verified talosctl, the node addresses, the
# talosconfig and the leak-scan pattern list all come from there.
#
# Every row starts from an empty schema whose one machine is the fixture's worker at the
# configuration it carries now, runs one or more e4x executors as separate processes, stops them at
# named gates (gate.go) to revoke, take over, inject a fault or kill, and then reads the outcome
# back with readers that share no code with e4x: psql inside the PostgreSQL container for the
# database, and the harness's own talosctl reads of the worker (its configuration digest, its
# machine-configuration resource version and its machined log). The row's verdict compares those
# readings, and the executors' exit statuses, with an expectation written in run/all before the
# run.
#
# Phase-0 evidence for the dispatch-safety experiment (ginsys/bronzeward issue 7). Not v1 tooling.

E4D_RUN_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
E4D_DIR=$(dirname -- "$E4D_RUN_DIR")
REPO_DIR=$(cd -- "$E4D_DIR/../.." && pwd)
FIXTURES_DIR=$REPO_DIR/fixtures

# shellcheck source-path=SCRIPTDIR source=../../../fixtures/lib.sh
. "$FIXTURES_DIR/lib.sh"

# Not defaulted: it holds a copy of the fixture's leak-scan patterns, which are this run's
# secrets, so it is the operator's to place and to delete, outside the checkout.
[ -n "${E4D_OUT:-}" ] || die "set E4D_OUT to a directory on disk, outside this checkout"
case $E4D_OUT in
  /*) ;;
  *) die "E4D_OUT must be an absolute path" ;;
esac
[ "${E4D_OUT#"$REPO_DIR"}" = "$E4D_OUT" ] || die "E4D_OUT is inside the checkout"

E4D_MARK=$E4D_OUT/.e4-dispatch-safety
E4D_BIN=$E4D_OUT/bin/e4x
E4D_CELLS=$E4D_OUT/cells.tsv
E4D_T=$E4D_OUT/transcripts
E4D_PARTS=$E4D_OUT/parts
# shellcheck disable=SC2034  # read by run/all and run/collect-evidence, which source this file
E4D_PATTERNS=$E4D_OUT/scan-patterns.txt
# Every executor is bounded, so a hung one ends the row instead of the run.
E4D_CLIENT_LIMIT=180
# How long a row waits, after a fault is healed, for a request the executor gave up on to land.
# The probes saw one land 11.6 s after the client gave up and 6 s after the partition healed.
E4D_SETTLE=30

e4d_init() {
  if [ -e "$E4D_OUT" ] && [ -n "$(ls -A -- "$E4D_OUT")" ]; then
    die "E4D_OUT ($E4D_OUT) is not empty; give each run a new directory"
  fi
  mkdir -p -- "$E4D_OUT/bin" "$E4D_T" "$E4D_PARTS"
  printf 'e4 dispatch-safety run output; delete after the evidence has landed\n' >"$E4D_MARK"
  printf 'n\tgroup\trow\texpected\tobserved\tverdict\ttranscript\n' >"$E4D_CELLS"
}

e4d_need_fixture() {
  [ -d "$STATE" ] || die "no fixture state: run fixtures/bin/up first"
  [ -n "${BW_POSTGRES_PASSWORD:-}" ] || die "the fixture published no PostgreSQL password"
  [ -s "$STATE/scan-patterns.txt" ] || die "$STATE/scan-patterns.txt is missing; the fixture is not up"
  [ -s "$TALOSCONFIG" ] || die "no talosconfig at $TALOSCONFIG"
  containers_own
  need go timeout pgrep sha256sum
  tool_verify talosctl "$TALOSCTL_SHA256"
}

e4d_build() {
  go -C "$E4D_DIR" build -o "$E4D_BIN" . || die "could not build the prototype"
}

# e4d_dsn: lib/pq's connection string. It carries the password, so it only ever travels in the
# environment of an executor (E4X_PG_DSN), never on a command line.
e4d_dsn() {
  printf 'host=127.0.0.1 port=%s user=bronzeward dbname=bronzeward sslmode=disable connect_timeout=5 password=%s' \
    "$POSTGRES_PORT" "$BW_POSTGRES_PASSWORD"
}

e4d_psql() {
  pg_stdin psql --username=bronzeward --dbname=bronzeward -X -q -A -t -F '|' -v ON_ERROR_STOP=1 "$@"
}

# ---- the worker, read by the harness ----

# Through the control plane, as e4x's default endpoint, bounded like the fixture's own reads.
w_talos() { talosctl_timed 10 -e "$TALOS_CONTROLPLANE_IP" -n "$TALOS_WORKER_IP" "$@"; }

# The fixture's normalization (fixtures/bin/selftest): command substitution strips the trailing
# newlines and printf adds one back, which gives the sent file's own digest.
w_digest() {
  local c
  c=$(w_talos get machineconfig v1alpha1 -o jsonpath='{.spec}') || return 1
  printf '%s\n' "$c" | sha256sum | cut -d' ' -f1
}

# The machine-configuration resource's version: it moves on every apply that lands, identical
# content included, and not on a rejected one (probe, report §3).
# w_resource prints "<version> <updated>": the resource's metadata version, and the worker's own
# time (whole seconds) of the last change, which dates a landing against .state/injections.log.
w_resource() {
  local yaml v u
  yaml=$(w_talos get machineconfig v1alpha1 -o yaml) || return 1
  v=$(awk '$1 == "version:" { print $2; exit }' <<<"$yaml")
  u=$(awk '$1 == "updated:" { print $2; exit }' <<<"$yaml")
  [[ $v =~ ^[0-9]+$ ]] || return 1
  printf '%s %s\n' "$v" "${u:-unknown}"
}
w_version() {
  local v u
  read -r v u < <(w_resource) || return 1
  printf '%s\n' "$v"
}

# Apply requests the worker's machined answered, by outcome, over its whole log.
w_log_counts() {
  local log
  log=$(w_talos logs machined) || return 1
  printf '%s %s\n' \
    "$(grep -c 'OK \[/machine.MachineService/ApplyConfiguration\]' <<<"$log")" \
    "$(grep -c 'InvalidArgument \[/machine.MachineService/ApplyConfiguration\]' <<<"$log")"
}

# ---- rows ----

declare -g ROW_N ROW_ID ROW_GROUP ROW_NAME ROW_PARTS PRE_DIGEST PRE_VERSION PRE_OK PRE_INVALID ART_FILE
declare -gA RC=() PID=() R=() ART=()
declare -ga ROW_LABELS=()

e4d_next() { printf '%03d' "$(($(wc -l <"$E4D_CELLS")))"; }

e4d_part() { printf '%s/%s.txt' "$ROW_PARTS" "$1"; }

# row_begin <group> <row> [setup flags]: an empty schema whose machine is the worker as it is now.
row_begin() {
  ROW_GROUP=$1 ROW_NAME=$2
  shift 2
  ROW_N=$(e4d_next)
  ROW_ID=$ROW_N-$ROW_GROUP-$ROW_NAME
  ROW_PARTS=$E4D_PARTS/$ROW_ID
  mkdir -p -- "$ROW_PARTS"
  RC=() PID=() R=() ART=() ROW_LABELS=()
  E4X_PG_DSN=$(e4d_dsn)
  export E4X_PG_DSN E4X_TALOSCTL=$CACHE/talosctl E4X_NODE=$TALOS_WORKER_IP
  export E4X_ENDPOINT=$TALOS_CONTROLPLANE_IP
  say "row $ROW_ID"
  PRE_DIGEST=$(w_digest) || die "row $ROW_ID: could not read the worker's configuration"
  PRE_VERSION=$(w_version) || die "row $ROW_ID: could not read the worker's resource version"
  read -r PRE_OK PRE_INVALID < <(w_log_counts) || die "row $ROW_ID: could not read the worker's machined log"
  printf 'pre digest=%s version=%s machined_ok=%s machined_invalid=%s\n' "$PRE_DIGEST" "$PRE_VERSION" \
    "$PRE_OK" "$PRE_INVALID" >"$(e4d_part pre)"
  ex setup harness setup "$@"
  # e4x's own read of the worker becomes the machine's Applied digest; it must be the harness's.
  e4d_read setup
  if [ "${R[setup.applied]-}" = "$PRE_DIGEST" ]; then R[baseline]=match; else R[baseline]=differs; fi
}

# artifact <name>: the worker's configuration as it is now with a row-unique node label, made with
# the pinned talosctl, which does not contact any node for this. Sets ART_FILE and ART[name] (not
# for use in a command substitution, whose subshell would lose ART).
artifact() {
  local name=$1 base=$ROW_PARTS/$1-base.yaml
  ART_FILE=$ROW_PARTS/$1.yaml
  w_talos get machineconfig v1alpha1 -o jsonpath='{.spec}' >"$base" ||
    die "row $ROW_ID: could not read the worker's configuration"
  talosctl_timed 20 machineconfig patch "$base" --output "$ART_FILE" \
    --patch "{\"machine\":{\"nodeLabels\":{\"bronzeward.test/e4x\":\"$ROW_N-$name\"}}}" >/dev/null ||
    die "row $ROW_ID: could not make artifact $name"
  ART[$name]=$(sha256sum <"$ART_FILE" | cut -d' ' -f1)
}

# rejected_artifact <name>: a configuration the worker's validation refuses (unknown machine type).
rejected_artifact() {
  local out=$ROW_PARTS/$1.yaml
  artifact "$1-source"
  # machine.type: the first `type: worker` at four spaces, the indent of talosctl's patch output.
  sed '0,/^    type: worker$/s//    type: bogus/' "$ART_FILE" >"$out"
  [ "$(grep -c '^    type: bogus$' "$out")" = 1 ] || die "row $ROW_ID: could not make the rejected artifact"
  unset 'ART['"$1-source"']'
  ART_FILE=$out
  ART[$1]=$(sha256sum <"$out" | cut -d' ' -f1)
}

# plan <id> <artifact file> [plan flags]: plan and approve.
plan() {
  local id=$1 file=$2
  shift 2
  ex "plan-$id" harness plan -id "$id" -artifact "$file" "$@"
  ex "approve-$id" harness approve -id "$id"
}

# ex <label> <owner> <e4x args...>: one e4x in the foreground; RC[label] is its exit status. Each
# label has its own gate directory, so a gate armed for one executor never stops another.
ex() {
  local label=$1 owner=$2 rc=0
  shift 2
  ROW_LABELS+=("$label")
  mkdir -p -- "$ROW_PARTS/gates-$label"
  printf '$ E4X_OWNER=%s e4x %s\n' "$owner" "$*" >"$(e4d_part "$label")"
  E4X_OWNER=$owner E4X_GATES=$ROW_PARTS/gates-$label timeout --signal=KILL "$E4D_CLIENT_LIMIT" "$E4D_BIN" "$@" \
    >>"$(e4d_part "$label")" 2>&1 || rc=$?
  RC[$label]=$rc
}

# ex_bg <label> <owner> <e4x args...>: the same in the background; ex_reap collects it.
ex_bg() {
  local label=$1 owner=$2
  shift 2
  ROW_LABELS+=("$label")
  mkdir -p -- "$ROW_PARTS/gates-$label"
  printf '$ E4X_OWNER=%s e4x %s\n' "$owner" "$*" >"$(e4d_part "$label")"
  E4X_OWNER=$owner E4X_GATES=$ROW_PARTS/gates-$label timeout --signal=KILL "$E4D_CLIENT_LIMIT" "$E4D_BIN" "$@" \
    >>"$(e4d_part "$label")" 2>&1 &
  PID[$label]=$!
}

ex_reap() {
  local label=$1 rc=0
  wait "${PID[$label]}" || rc=$?
  RC[$label]=$rc
}

ex_running() { kill -0 "${PID[$1]}" 2>/dev/null; }

# ex_kill <label>: SIGKILL the e4x process itself (the timeout wrapper's child), so that no
# deferred rollback or cleanup runs, then reap it.
ex_kill() {
  local label=$1 child
  child=$(pgrep -P "${PID[$label]}" -x e4x) || child=
  if [ -n "$child" ]; then kill -KILL "$child"; else kill -KILL "${PID[$label]}"; fi
  printf '# SIGKILL sent to %s\n' "${child:-${PID[$label]}}" >>"$(e4d_part "$label")"
  ex_reap "$label"
}

# Gates of one executor: arm before it starts, wait until it is held there, release it.
gate_arm() { : >"$ROW_PARTS/gates-$1/$2.hold"; }
gate_release() { : >"$ROW_PARTS/gates-$1/$2.go"; }
gate_wait() {
  local label=$1 gate=$2 limit=${3:-60} i
  for ((i = 0; i < limit * 20; i++)); do
    [ -e "$ROW_PARTS/gates-$label/$gate.reached" ] && return 0
    ex_running "$label" || break
    sleep 0.05
  done
  printf '# gate_wait: %s never reached gate %s\n' "$label" "$gate" >>"$(e4d_part "$label")"
  return 1
}

# ex_prepare <label>: the gate directory, so gates can be armed before the executor starts.
ex_prepare() { mkdir -p -- "$ROW_PARTS/gates-$1"; }

# e4d_inject <action> <service>: the fixture's own injection, logged in .state/injections.log.
e4d_inject() {
  local label="inject-$1-$2" rc=0
  [ -n "${RC[$label]+set}" ] && label="$label-${#ROW_LABELS[@]}"
  ROW_LABELS+=("$label")
  printf '$ fixtures/bin/inject %s\n' "$*" >"$(e4d_part "$label")"
  "$FIXTURES_DIR/bin/inject" "$@" >>"$(e4d_part "$label")" 2>&1 || rc=$?
  RC[$label]=$rc
}

# settle <seconds>: wait for anything still in flight, and say so in the transcript.
settle() {
  ROW_LABELS+=("settle")
  printf '# settled %ss at %s\n' "${1:-$E4D_SETTLE}" "$(date -u +%FT%TZ)" >>"$(e4d_part settle)"
  sleep "${1:-$E4D_SETTLE}"
}

# worker_readable [seconds]: wait until the harness can read the worker's configuration again
# (after a partition heals), for at most the given seconds (30 by default). A worker still
# unreadable at the end is recorded, not fatal: the row's own reads then show it.
worker_readable() {
  local limit=${1:-30} start=$SECONDS result=unreadable
  ROW_LABELS+=("readable")
  while [ $((SECONDS - start)) -lt "$limit" ]; do
    if w_digest >/dev/null 2>&1; then
      result=readable
      break
    fi
    sleep 1
  done
  printf '# worker %s after %ss at %s\n' "$result" "$((SECONDS - start))" "$(date -u +%FT%TZ)" \
    >>"$(e4d_part readable)"
}

# e4d_read <reader> [op] [label]: readers/<reader>.sql with psql, the operation id bound as
# :'op'; its name|value rows go into R as <label, or op, or reader>.<name>.
e4d_read() {
  local reader=$1 op=${2:-} file out k v rc=0 prefix name
  file=$E4D_DIR/readers/$reader.sql
  [ -f "$file" ] || die "no reader $file"
  prefix=${3:-${op:-$reader}}
  name=read-$reader${op:+-$op}${3:+-$3}
  out=$ROW_PARTS/$name.txt
  ROW_LABELS+=("$name")
  e4d_psql -v "op=$op" -f - <"$file" >"$out" 2>&1 || rc=$?
  RC[$name]=$rc
  while IFS='|' read -r k v; do
    [ -n "$k" ] && R[$prefix.$k]=$v
  done <"$out"
}

# read_worker [prefix]: which configuration the worker carries, and how many applies landed and
# were answered since row_begin, by the harness's own reads, as <prefix>.is, .landed, .ok and
# .invalid (prefix worker by default; a row that reads twice names the stage).
read_worker() {
  local p=${1:-worker} digest version updated ok invalid name
  ROW_LABELS+=("$p")
  digest=$(w_digest) || digest=unreadable
  read -r version updated < <(w_resource) || { version='' updated=''; }
  read -r ok invalid < <(w_log_counts) || { ok='' invalid=''; }
  R[$p.is]=other
  [ "$digest" = "$PRE_DIGEST" ] && R[$p.is]=pre
  for name in "${!ART[@]}"; do
    [ "$digest" = "${ART[$name]}" ] && R[$p.is]=$name
  done
  [ "$digest" = unreadable ] && R[$p.is]=unreadable
  [ -n "$version" ] && R[$p.landed]=$((version - PRE_VERSION))
  [ -n "$ok" ] && R[$p.ok]=$((ok - PRE_OK)) R[$p.invalid]=$((invalid - PRE_INVALID))
  R[$p.updated]=${updated:-unknown}
  printf '%s digest=%s version=%s updated=%s machined_ok=%s machined_invalid=%s\n' "$(date -u +%FT%T.%3NZ)" \
    "$digest" "$version" "${updated:-unknown}" "$ok" "$invalid" >"$(e4d_part "$p")"
}

row_note() { R[$1]=$2; }

# row_end <expectation...>: each expectation is
#   key=value     the reading R[key] equals value
#   key>value     the reading is an integer greater than value
#   rc:label=N    executor label exited N
#   out:label~re  executor label's transcript matches the extended regex
#   seen:key      the reading R[key] exists; its value is recorded, not judged
# Every row also expects baseline=match: e4x's read of the worker agreed with the harness's.
row_end() {
  local e key val got ok=1 observed=() t label
  for e in baseline=match "$@"; do
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
        if grep -qE -- "$val" "$(e4d_part "$label")" 2>/dev/null; then got=yes; else got=no ok=0; fi
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
    for label in pre "${ROW_LABELS[@]}"; do
      [ -f "$(e4d_part "$label")" ] || continue
      printf '==> %s (rc %s) <==\n' "$label" "${RC[$label]-}"
      cat -- "$(e4d_part "$label")"
    done
  } >"$E4D_OUT/$t"
  local verdict=match
  [ "$ok" = 1 ] || verdict=mismatch
  local IFS=' '
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$ROW_N" "$ROW_GROUP" "$ROW_NAME" "$*" "${observed[*]}" "$verdict" "$t" \
    >>"$E4D_CELLS"
  say "row $ROW_ID: $verdict (${observed[*]})"
}

e4d_mismatches() { awk -F'\t' 'NR > 1 && $6 == "mismatch"' "$E4D_CELLS" | wc -l; }
