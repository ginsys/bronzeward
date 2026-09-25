#!/usr/bin/env bash
# Shared setup for the E3 Talos compatibility runs. Sourced by the scripts beside it, never run on
# its own.
#
# It reuses fixtures/lib.sh rather than re-deriving anything: the verified download, the pinned
# talosctl the harness reads the worker with, the node addresses, the talosconfig, the cluster's
# own secrets bundle and the leak-scan pattern list all come from there.
#
# Every matrix cell is measured twice, through each renderer's talosctl as a subprocess and
# through e3m (machinery/) built against the same tag's pkg/machinery. The generation and
# validation cells are recorded, not judged: what a renderer does with a contract is the finding.
# What is judged are the rows (row_begin .. row_end): the RPC calls against the live worker, read
# back by the harness's own pinned talosctl, and the controls, each written with its expectation
# in run/all before the run.
#
# Phase-0 evidence for the Talos compatibility experiment (ginsys/bronzeward issue 5). Not v1
# tooling.

E3_RUN_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
E3_DIR=$(dirname -- "$E3_RUN_DIR")
REPO_DIR=$(cd -- "$E3_DIR/../.." && pwd)
FIXTURES_DIR=$REPO_DIR/fixtures

# shellcheck source-path=SCRIPTDIR source=../../../fixtures/lib.sh
. "$FIXTURES_DIR/lib.sh"
# shellcheck source-path=SCRIPTDIR source=../versions.env
. "$E3_DIR/versions.env"

# Not defaulted: the generated configurations hold the fixture's Talos secrets, so the directory is
# the operator's to place and to delete, outside the checkout.
[ -n "${E3_OUT:-}" ] || die "set E3_OUT to a directory on disk, outside this checkout"
case $E3_OUT in
  /*) ;;
  *) die "E3_OUT must be an absolute path" ;;
esac
[ "${E3_OUT#"$REPO_DIR"}" = "$E3_OUT" ] || die "E3_OUT is inside the checkout"

E3_MARK=$E3_OUT/.e3-talos-compatibility
E3_BIN=$E3_OUT/bin
E3_GEN=$E3_OUT/gen
E3_RAW=$E3_OUT/raw
E3_T=$E3_OUT/transcripts
E3_PARTS=$E3_OUT/parts
E3_PATHS=$E3_OUT/paths
E3_ROWS=$E3_OUT/rows.tsv
E3_MESSAGES=$E3_OUT/messages.tsv
# Read by run/all and run/collect-evidence, which source this file. The cluster name and endpoint
# are the fixture's, so that a generated configuration differs from the worker's own only in what
# the renderer and the contract change.
# shellcheck disable=SC2034
{
  E3_SECRETS=$E3_OUT/inputs/talos-secrets.yaml
  E3_PATTERNS=$E3_OUT/scan-patterns.txt
  E3_CLUSTER=$FIXTURE_NAME
  E3_ENDPOINT=https://$TALOS_CONTROLPLANE_IP:6443
}
# Every call is bounded, so a hung one ends its cell instead of the run.
E3_LIMIT=60

e3_init() {
  if [ -e "$E3_OUT" ] && [ -n "$(ls -A -- "$E3_OUT")" ]; then
    die "E3_OUT ($E3_OUT) is not empty; give each run a new directory"
  fi
  mkdir -p -- "$E3_BIN" "$E3_GEN" "$E3_RAW" "$E3_T" "$E3_PARTS" "$E3_PATHS" "$E3_OUT/inputs"
  printf 'e3 talos-compatibility run output; delete after the evidence has landed\n' >"$E3_MARK"
  printf 'n\tgroup\trow\texpected\tobserved\tverdict\ttranscript\n' >"$E3_ROWS"
  printf 'id\tmessage\n' >"$E3_MESSAGES"
}

e3_need_fixture() {
  [ -d "$STATE" ] || die "no fixture state: run fixtures/bin/up first"
  [ -s "$STATE/scan-patterns.txt" ] || die "$STATE/scan-patterns.txt is missing; the fixture is not up"
  [ -s "$STATE/talos-secrets.yaml" ] || die "$STATE/talos-secrets.yaml is missing; the fixture is not up"
  [ -s "$TALOSCONFIG" ] || die "no talosconfig at $TALOSCONFIG"
  containers_own
  need go timeout sha256sum awk sed
  tool_verify talosctl "$TALOSCTL_SHA256"
}

# e3_sha <version>: the pinned digest from versions.env (E3_SHA256_v1_13_6 for v1.13.6).
e3_sha() {
  local name=E3_SHA256_${1//[.-]/_}
  [ -n "${!name:-}" ] || die "versions.env pins no sha256 for $1"
  printf '%s\n' "${!name}"
}

declare -gA E3_VERIFIED=()

# e3_tools: each renderer's talosctl, downloaded once into the fixture's cache by its verified
# fetch, and each e3m, built from its machinery module. tools.tsv records what each reports.
e3_tools() {
  local v file
  printf 'renderer\ttalosctl_sha256\ttalosctl_client\te3m\n' >"$E3_OUT/tools.tsv"
  for v in $E3_RENDERERS; do
    file=$(fetch "e3-talosctl-$v" "https://github.com/siderolabs/talos/releases/download/$v/talosctl-linux-amd64" \
      "$(e3_sha "$v")")
    install -m 0755 "$file" "$E3_BIN/talosctl-$v"
    go -C "$E3_DIR/machinery/$v" build -o "$E3_BIN/e3m-$v" . || die "could not build e3m against machinery $v"
    # The installed binary's digest as measured here, not the pin it was fetched against.
    printf '%s\t%s\t%s\t%s\n' "$v" "$(sha256sum <"$E3_BIN/talosctl-$v" | cut -d' ' -f1)" \
      "$(tc "$v" version --client --short 2>&1 | awk 'NF { printf "%s%s", sep, $0; sep = " | " }')" \
      "$(m "$v" info)" >>"$E3_OUT/tools.tsv"
  done
}

# tc <renderer> <args...>: that renderer's talosctl, bounded, checked against its pin on first use
# in this shell.
tc() {
  local v=$1
  shift
  if [ -z "${E3_VERIFIED[$v]:-}" ]; then
    local digest
    digest=$(sha256sum <"$E3_BIN/talosctl-$v") || die "cannot read $E3_BIN/talosctl-$v"
    [ "${digest%% *}" = "$(e3_sha "$v")" ] || die "$E3_BIN/talosctl-$v is not the pinned talosctl $v"
    E3_VERIFIED[$v]=1
  fi
  timeout "$E3_LIMIT" "$E3_BIN/talosctl-$v" "$@"
}

# m <renderer> <args...>: e3m built against that renderer's machinery, bounded. The RPC
# subcommands take the endpoint, the node and the talosconfig's path from the environment.
m() {
  local v=$1
  shift
  E3M_ENDPOINT=$TALOS_CONTROLPLANE_IP E3M_NODE=$TALOS_WORKER_IP E3M_TALOSCONFIG=$TALOSCONFIG \
    timeout "$E3_LIMIT" "$E3_BIN/e3m-$v" "$@"
}

# digest <file>: e3m's normalization (machinery/src/main.go): the SHA-256 of the content with its
# trailing newlines replaced by exactly one. Command substitution strips them, printf adds one.
digest() {
  local c
  [ -f "$1" ] || { printf 'none\n'; return; }
  c=$(<"$1")
  printf '%s\n' "$c" | sha256sum | cut -d' ' -f1
}

# ---- messages: every distinct output, normalized, once ----

declare -gA E3_MSG=()
declare -g MSG_ID

# msg_intern <file>: MSG_ID becomes the id of the file's normalized content (M1, M2, ...), which
# messages.tsv holds once. Normalized: any path under E3_OUT becomes <file>, lines are joined with
# ` | `, tabs become spaces. An empty output has the id `-`.
msg_intern() {
  local text
  text=$(sed -e "s#$E3_OUT/[^[:space:]:\"']*#<file>#g" -e 's/\t/ /g' -e 's/[[:space:]]*$//' -- "$1" |
    awk 'NF { printf "%s%s", sep, $0; sep = " | " }')
  if [ -z "$text" ]; then
    MSG_ID=-
    return
  fi
  if [ -z "${E3_MSG[$text]:-}" ]; then
    E3_MSG[$text]=M$((${#E3_MSG[@]} + 1))
    printf '%s\t%s\n' "${E3_MSG[$text]}" "$text" >>"$E3_MESSAGES"
  fi
  MSG_ID=${E3_MSG[$text]}
}

# run_cell <raw-file> <command...>: runs the command with its output in raw-file, and sets
# CELL to `<exit status>:<message id>` for run/all to record.
declare -g CELL
run_cell() {
  local raw=$1 rc=0
  shift
  mkdir -p -- "$(dirname -- "$raw")"
  printf '$ %s\n' "$*" >"$raw.cmd"
  "$@" >"$raw" 2>&1 || rc=$?
  msg_intern "$raw"
  # shellcheck disable=SC2034  # read by run/all
  CELL=$rc:$MSG_ID
}

# ---- the worker, read by the harness ----

# Through the control plane, with the fixture's pinned talosctl, bounded like the fixture's reads.
w_talos() { talosctl_timed 10 -e "$TALOS_CONTROLPLANE_IP" -n "$TALOS_WORKER_IP" "$@"; }

w_config() { w_talos get machineconfig v1alpha1 -o jsonpath='{.spec}'; }

w_digest() {
  local c
  c=$(w_config) || return 1
  printf '%s\n' "$c" | sha256sum | cut -d' ' -f1
}

# The machine-configuration resource's version: it moves on every apply that lands and not on a
# rejected or dry-run one (the E4 dispatch-safety probes).
w_version() {
  local yaml v
  # Read whole before parsing: awk's early exit would end talosctl with SIGPIPE, which pipefail
  # reports as the read failing.
  yaml=$(w_talos get machineconfig v1alpha1 -o yaml) || return 1
  v=$(awk '$1 == "version:" { print $2; exit }' <<<"$yaml")
  [[ $v =~ ^[0-9]+$ ]] || return 1
  printf '%s\n' "$v"
}

# ---- rows ----

declare -g ROW_N ROW_ID ROW_GROUP ROW_NAME ROW_PARTS PRE_DIGEST PRE_VERSION ART_FILE ART_DIGEST
declare -gA RC=() R=()
declare -ga ROW_LABELS=()

row_next() { printf '%03d' "$(($(wc -l <"$E3_ROWS")))"; }
row_part() { printf '%s/%s.txt' "$ROW_PARTS" "$1"; }

# row_begin <group> <row> [worker]: with `worker`, the worker's configuration digest and resource
# version are read first, for read_worker to compare against.
row_begin() {
  ROW_GROUP=$1 ROW_NAME=$2
  ROW_N=$(row_next)
  ROW_ID=$ROW_N-$ROW_GROUP-$ROW_NAME
  ROW_PARTS=$E3_PARTS/$ROW_ID
  mkdir -p -- "$ROW_PARTS"
  RC=() R=() ROW_LABELS=() ART_FILE='' ART_DIGEST='' PRE_DIGEST='' PRE_VERSION=''
  say "row $ROW_ID"
  if [ "${3:-}" = worker ]; then
    PRE_DIGEST=$(w_digest) || die "row $ROW_ID: could not read the worker's configuration"
    PRE_VERSION=$(w_version) || die "row $ROW_ID: could not read the worker's resource version"
    printf 'pre digest=%s version=%s\n' "$PRE_DIGEST" "$PRE_VERSION" >"$(row_part pre)"
  fi
}

# step <label> <command...>: one command, its exit status in RC[label], its output in the row's
# transcript. Only for commands whose output holds no secret: a configuration, a dry-run diff or
# a read of the worker's configuration goes through step_quiet instead.
step() {
  local label=$1 rc=0
  shift
  ROW_LABELS+=("$label")
  printf '$ %s\n' "$*" >"$(row_part "$label")"
  "$@" >>"$(row_part "$label")" 2>&1 || rc=$?
  RC[$label]=$rc
}

# step_quiet <label> <command...>: the same, for an apply whose output can carry a configuration
# diff (talosctl prints it on a dry run, and in the error when a change cannot be applied in the
# requested mode). The output is kept in E3_RAW; the transcript holds its lines up to the first
# diff line, then that part's line count and the whole output's digest.
step_quiet() {
  local label=$1 rc=0 raw
  shift
  ROW_LABELS+=("$label")
  raw=$E3_RAW/rows/$ROW_ID-$label.txt
  mkdir -p -- "$(dirname -- "$raw")"
  printf '$ %s\n' "$*" >"$(row_part "$label")"
  "$@" >"$raw" 2>&1 || rc=$?
  RC[$label]=$rc
  awk -v digest="$(sha256sum <"$raw" | cut -d' ' -f1)" '
    /^(Config )?diff:/ || /^--- / { withheld = 1 }
    withheld { n++; next }
    { print }
    END { if (withheld) printf "# diff withheld (may hold the configuration): %d lines; output sha256 %s\n", n, digest }
  ' "$raw" >>"$(row_part "$label")"
}

# step_read <label> <command...>: a read of the worker's configuration. Its standard output, the
# configuration, is kept in E3_RAW (row_read_digest gives its digest) and withheld from the
# transcript; its standard error, where talosctl warns about version skew, is kept in full.
step_read() {
  local label=$1 rc=0 raw
  shift
  ROW_LABELS+=("$label")
  raw=$E3_RAW/rows/$ROW_ID-$label.txt
  mkdir -p -- "$(dirname -- "$raw")"
  printf '$ %s\n' "$*" >"$(row_part "$label")"
  "$@" >"$raw" 2>>"$(row_part "$label")" || rc=$?
  RC[$label]=$rc
  printf '# configuration withheld: %s lines, digest %s\n' "$(wc -l <"$raw")" "$(digest "$raw")" >>"$(row_part "$label")"
}
row_read_digest() { digest "$E3_RAW/rows/$ROW_ID-$1.txt"; }

# artifact <name>: the worker's configuration as it is now with a row-unique node label, made with
# the fixture's pinned talosctl, which contacts no node for this. Sets ART_FILE and ART_DIGEST.
artifact() {
  local base=$ROW_PARTS/$1-base.yaml
  ART_FILE=$E3_RAW/rows/$ROW_ID-$1.yaml
  mkdir -p -- "$(dirname -- "$ART_FILE")"
  w_config >"$base" || die "row $ROW_ID: could not read the worker's configuration"
  talosctl_timed 20 machineconfig patch "$base" --output "$ART_FILE" \
    --patch "{\"machine\":{\"nodeLabels\":{\"bronzeward.test/e3\":\"$ROW_N-$1\"}}}" >/dev/null ||
    die "row $ROW_ID: could not make artifact $1"
  rm -f -- "$base"
  ART_DIGEST=$(digest "$ART_FILE")
}

# read_worker [prefix]: whether the worker carries the configuration it had at row_begin (pre),
# the row's artifact (artifact) or something else, and how many applies landed since, as
# <prefix>.is and <prefix>.landed (prefix worker by default).
read_worker() {
  local p=${1:-worker} d v
  ROW_LABELS+=("$p")
  d=$(w_digest) || d=unreadable
  v=$(w_version) || v=
  R[$p.is]=other
  [ "$d" = "$PRE_DIGEST" ] && R[$p.is]=pre
  [ -n "${ART_DIGEST:-}" ] && [ "$d" = "$ART_DIGEST" ] && R[$p.is]=artifact
  [ "$d" = unreadable ] && R[$p.is]=unreadable
  [ -n "$v" ] && R[$p.landed]=$((v - PRE_VERSION))
  printf 'worker digest=%s version=%s\n' "$d" "$v" >"$(row_part "$p")"
}

row_note() { R[$1]=$2; }

# row_end <expectation...>: each expectation is
#   key=value     the reading R[key] equals value
#   rc:label=N    step label exited N
#   rc:label!=0   step label exited non-zero
#   out:label~re  step label's transcript matches the extended regex
#   seen:key      the reading R[key] exists; its value is recorded, not judged
row_end() {
  local e key val got ok=1 observed=() t label
  for e in "$@"; do
    case $e in
      seen:*)
        key=${e#seen:}
        got=${R[$key]-unset}
        [ "$got" != unset ] || ok=0
        observed+=("$key=$got")
        ;;
      rc:*'!=0')
        key=${e%%!=*} label=${key#rc:}
        got=${RC[$label]-unset}
        { [[ $got =~ ^[0-9]+$ ]] && [ "$got" -ne 0 ]; } || ok=0
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
        if grep -qE -- "$val" "$(row_part "$label")" 2>/dev/null; then got=yes; else got=no ok=0; fi
        observed+=("$key~$got")
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
      [ -f "$(row_part "$label")" ] || continue
      printf '==> %s (rc %s) <==\n' "$label" "${RC[$label]-}"
      cat -- "$(row_part "$label")"
    done
  } >"$E3_OUT/$t"
  local verdict=match
  [ "$ok" = 1 ] || verdict=mismatch
  local IFS=' '
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$ROW_N" "$ROW_GROUP" "$ROW_NAME" "$*" "${observed[*]}" "$verdict" "$t" \
    >>"$E3_ROWS"
  say "row $ROW_ID: $verdict (${observed[*]})"
}

e3_mismatches() { awk -F'\t' 'NR > 1 && $6 == "mismatch"' "$E3_ROWS" | wc -l; }
