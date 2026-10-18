# shellcheck shell=bash
# The sourcing scenario sets scenario, EV, bin and image_id (SC2154) and reads last_status,
# issued_token and the other results these helpers leave (SC2034).
# shellcheck disable=SC2034,SC2154
# Helpers the acceptance scenarios share. Sourced by a scenario after lib.sh, once it has set
# `scenario` (its name: the results file is $EV/<scenario>.tsv and messages are prefixed with it),
# `EV` (its evidence directory), `bin` and `image_id`. Each check writes PASS, FAIL or SKIP, its
# acceptance-plan reference and what it checked to the results file and to stdout.

failed=0
# secrets: every token this run mints or issues, and the secret half of each automation token on
# its own (its token id is in the database dump, so the half alone rebuilds it). None is a scan
# pattern of bin/evidence, which knows only the fixture's own; the end of the run looks for each of
# them in the evidence it kept.
secrets=()
last_half=
keep_secret() {
  [ -n "$1" ] || return 0
  secrets+=("$1")
  case $1 in bwt_*.*) last_half=${1##*.} && secrets+=("$last_half") ;; esac
}
# redacted: credentials commands.tsv must not show that bin/evidence already scans for, such as the
# automation token bin/seed issues, with its secret half, which the refused variants still carry.
redacted=()
redact_automation_token() { redacted+=("$1" "${1##*.}"); }
# withheld: fixture secrets a request body carries on purpose, such as the canary in a refused
# input; no transcript or commands.tsv line shows them, each is written <withheld>.
withheld=()

# result PASS|FAIL|SKIP <reference> <what>. No line carries a token or a response value.
result() {
  printf '%s\t%s\t%s\n' "$1" "$2" "$3" | tee -a "$EV/$scenario.tsv"
  [ "$1" != FAIL ] || failed=$((failed + 1))
}
# ran <reference> <exit> <command...>: the command and its exit status as a line of commands.tsv
# (acceptance plan §2's common evidence), every token this run holds written as <token> and every
# withheld value as <withheld>.
ran() {
  local ref=$1 rc=$2 cmd s
  shift 2
  cmd=$(printf '%q ' "$@")
  for s in "${secrets[@]}" "${redacted[@]}"; do cmd=${cmd//"$s"/<token>}; done
  for s in "${withheld[@]}"; do cmd=${cmd//"$s"/<withheld>}; done
  printf '%s\t%s\t%s\n' "$ref" "$rc" "${cmd% }" >>"$EV/commands.tsv"
}
# check <reference> <what> <command...>: PASS when the command succeeds; the command and its exit
# status go to commands.tsv.
check() {
  local ref=$1 what=$2 rc=0
  shift 2
  "$@" || rc=$?
  ran "$ref" "$rc" "$@"
  if [ "$rc" -eq 0 ]; then result PASS "$ref" "$what"; else result FAIL "$ref" "$what"; fi
}

# mint <human> [defect]: an access token for the issuer A trusts, from its key, by the oidc binary
# of the image up built. Minted offline: the issuer is not published.
oidc() {
  timeout "$REQUEST_LIMIT" docker run --rm --pull never --network none --user "$(id -u):$(id -g)" \
    --label "$TOOL_LABEL" -v "$STATE/issuer:/etc/oidc:ro" --entrypoint /oidc "$image_id" "$@"
}
mint() {
  local defect=()
  [ -z "${2:-}" ] || defect=(-defect "$2")
  oidc mint -key /etc/oidc/key.json -issuer http://issuer:5556 -human "$1" "${defect[@]}"
}
# mint_without <human> <group>: a valid token whose groups claim lacks one of the human's groups, as
# if the human had been removed from it at the issuer.
mint_without() { oidc mint -key /etc/oidc/key.json -issuer http://issuer:5556 -human "$1" -without-group "$2"; }

# call <method> <path> <token> [body] [idempotency key] [if-match]: the status of a request to the
# instance on port $call_port (A's unless the scenario sets it), 000 when nothing answers; the
# response headers and body are left in $EV/last.headers and $EV/last.body, both emptied first.
# The token goes over stdin and the body through a file only the user reads: a command line is
# readable by any local user. call_limit bounds the request, in seconds.
call_port=$SERVER_A_PORT call_limit=10
call() {
  local method=$1 path=$2 token=$3 body=${4:-} key=${5:-} match=${6:-} args=() sent=$STATE/$scenario-request-body
  : >"$EV/last.headers"
  : >"$EV/last.body"
  if [ -n "$body" ]; then
    (umask 077 && printf '%s' "$body" >"$sent")
    args+=(--header 'Content-Type: application/json' --data-binary "@$sent")
  fi
  [ -z "$key" ] || args+=(--header "Idempotency-Key: $key")
  [ -z "$match" ] || args+=(--header "If-Match: $match")
  { [ -z "$token" ] || printf 'Authorization: Bearer %s\n' "$token"; } |
    command curl --disable --noproxy '*' --silent --max-time "$call_limit" --output "$EV/last.body" \
      --dump-header "$EV/last.headers" --write-out '%{http_code}' \
      --header @- --request "$method" "${args[@]}" "http://127.0.0.1:$call_port$path" || true
  rm -f -- "$sent"
}
# transcript <status> <call args...>: the request and its response into $EV/http/<n>.txt, in call
# order, the token withheld (acceptance plan §2). The request bodies are the script's own literals,
# each withheld value replaced.
http_n=0
transcript() {
  local status=$1 method=$2 path=$3 token=$4 body=${5:-} key=${6:-} match=${7:-} s
  for s in "${withheld[@]}"; do body=${body//"$s"/<withheld>}; done
  http_n=$((http_n + 1))
  {
    printf '> %s %s\n' "$method" "$path"
    if [ -n "$token" ]; then printf '> Authorization: Bearer <withheld>\n'; else printf '> (no Authorization)\n'; fi
    [ -z "$key" ] || printf '> Idempotency-Key: %s\n' "$key"
    [ -z "$match" ] || printf '> If-Match: %s\n' "$match"
    [ -z "$body" ] || printf '> Content-Type: application/json\n>\n%s\n' "$body"
    printf '< status %s\n' "$status"
    tr -d '\r' <"$EV/last.headers"
    cat -- "$EV/last.body"
    printf '\n'
  } >"$(printf '%s/http/%04d.txt' "$EV" "$http_n")"
}
# answers <status> <problem code or -> <call args...>: the request gets that status and, unless
# -, a problem document of that code. Says what it got otherwise.
last_status=
answers() {
  local want=$1 code=$2 got
  shift 2
  got=$(call "$@")
  last_status=$got
  transcript "$got" "$@"
  if [ "$got" != "$want" ]; then
    say "$scenario: $1 $2: $got, want $want"
    return 1
  fi
  [ "$code" = - ] || jq -e --arg t "urn:bronzeward:problem:$code" '.type == $t' "$EV/last.body" >/dev/null || {
    say "$scenario: $1 $2: $got without the problem $code"
    return 1
  }
}
# answers_with <jq filter> <status> <problem code or -> <call args...>: as answers, and the body
# also satisfies the filter.
answers_with() {
  local filter=$1
  shift
  answers "$@" || return 1
  jq -e "$filter" "$EV/last.body" >/dev/null || {
    say "$scenario: $3 $4: the body does not satisfy $filter"
    return 1
  }
}

# issue <name> [token flags]: a new automation identity; its token in $issued_token, its idn in
# $issued. Globals, not stdout: a command substitution would run it in a subshell, and the identity
# would never reach the caller.
issued='' issued_token='' issued_n=0
issue() {
  local name=$1 err tok
  shift
  err=$(mktemp "$STATE/$scenario-issue.XXXXXX")
  if ! tok=$("$bin/bw" token issue -name "$name" -roles author -responsible h-all -operator h-all "$@" 2>"$err"); then
    say "$scenario: bw token issue $name: $(tail -n 1 "$err")"
    rm -f -- "$err"
    return 1
  fi
  issued=$(sed -n 's/^identity \(idn_[a-z2-7]*\):.*/\1/p' "$err")
  rm -f -- "$err"
  issued_token=$tok
  keep_secret "$tok"
  issued_n=$((issued_n + 1))
  [ -n "$issued" ] && [ -n "$tok" ]
}

# Go suites a scenario runs, it runs in a clone of the image commit (image_clone), not in this
# checkout: nothing here, edited, untracked, ignored or misreported by git's configuration, reaches
# the bytes they test (ginsys/bronzeward#68). Under .state, which down removes; the scenario removes
# it again once its suites have run.
clone_dir=$STATE/$scenario-clone
clone_src=$clone_dir/src
clone_head=
# clone_made <name>: a fresh clone at clone_src, its HEAD in clone_head, recorded in $EV/<name>.log.
clone_made() {
  local name=$1 rc=0
  rm -rf -- "$clone_dir"
  mkdir -- "$clone_dir"
  image_clone "$clone_src" >"$EV/$name.log" 2>&1 || rc=$?
  ran "$name" "$rc" image_clone "$clone_src"
  [ "$rc" -eq 0 ] || return "$rc"
  clone_head=$(sed -n 's/^clone .* HEAD \([0-9a-f]*\)$/\1/p' "$EV/$name.log")
}
# in_clone <command...>: in the clone, isolated_run: only the caller's variables the suites need
# (locations, locale, Go, BW_TEST_*, proxies), git's configuration, attributes, hooks and templates
# off for every git the suites run, and no mise configuration but the clone's.
in_clone() {
  isolated_run "$clone_src" "$@"
}
# logged <name> <command...>: in the clone, output to $EV/<name>.log, which starts with the
# command and the clone's HEAD and ends with its exit status; both go to commands.tsv too.
logged() {
  local name=$1 rc=0
  shift
  printf '$ %s\n# in a clone of the image commit, HEAD %s\n' "$*" "$clone_head" >"$EV/$name.log"
  in_clone timeout 1800 "$@" >>"$EV/$name.log" 2>&1 || rc=$?
  printf 'exit %d\n' "$rc" >>"$EV/$name.log"
  ran "$name" "$rc" "$@"
  return "$rc"
}
# go_env_clean: no Go setting, from the environment or go env -w, selects or skips tests or changes
# what they build: GOFLAGS=-run=^$ alone makes every suite exit 0 having run nothing. And the
# suites' Go is the one up-build records for the image: GOTOOLCHAIN can switch it.
go_env_clean() {
  local out v
  out=$(in_clone mise exec -- go env GOFLAGS GOEXPERIMENT GOWORK GOVERSION) || return 1
  mapfile -t v <<<"$out"
  [ "${#v[@]}" -eq 4 ] && [ -z "${v[0]}${v[1]}${v[2]}" ] && [ -n "${v[3]}" ] &&
    [ "${v[3]}" = "$(sed -n 's/^go //p' "$STATE/up-build")" ]
}

# The helpers below serve the scenarios that run on instance C, the interruption build (s1, s2).
# key <name>: this run's Idempotency-Key for one request ($run_key, set by the scenario).
key() { printf '%s-%s-%s' "$scenario" "$run_key" "$1"; }
# etag_of_last: the ETag header of the last response.
etag_of_last() { sed -n 's/^[Ee][Tt][Aa][Gg]: *//p' "$EV/last.headers" | tr -d '\r'; }
# sql <statement>: one psql answer, unaligned, as Bronzeward's owner role.
sql() { pg psql -U bronzeward -d bronzeward -Atc "$1"; }

# h-author's token. The issuer's tokens live five minutes (its Lifetime) and the run is longer, so
# each phase asks for one with most of its life left: minted again once the last is three minutes
# old, longer than any phase between two calls runs.
author='' author_at=0
fresh_author() {
  [ -z "$author" ] || [ $((SECONDS - author_at)) -ge 180 ] || return 0
  author=$(mint h-author) || die "cannot mint an author token"
  keep_secret "$author"
  author_at=$SECONDS
}

# Predicates for check: their arguments land in commands.tsv, so they take IDs and counts only.
same() { [ "$1" = "$2" ]; }
differ() { [ "$1" != "$2" ]; }
# counted <n> <ERE> <file>: exactly n lines of the file match.
counted() { [ "$(grep --count --extended-regexp -- "$2" "$3")" = "$1" ]; }

# evidence_to <name>: a bundle captured and scanned now, its path in $bundle and in $EV/<name>.
bundle=''
evidence_to() {
  bundle=$("$bin/evidence" "$EV" 2>"$EV/$1-evidence.log") || {
    say "$scenario: bin/evidence ($1) failed: $(tail -n 1 "$EV/$1-evidence.log")"
    return 1
  }
  printf '%s\n' "$bundle" >"$EV/$1-bundle"
}
# scan_clean <name>: the bundle evidence_to <name> captured is complete and holds no fixture
# secret but its control: a source not captured was not scanned either.
scan_clean() {
  local b
  b=$(cat -- "$EV/$1-bundle") && [ -n "$b" ] && [ -d "$b" ] || return 1
  [ ! -e "$b/unavailable.txt" ] || { say "$scenario: the $1 bundle is incomplete: $b/unavailable.txt"; return 1; }
  grep --quiet --fixed-strings -- 'positive control found; 0 other file(s) contain' "$EV/$1-evidence.log"
}
# outcome_bundle <row ref> <name>: the evidence bundle after one success or refusal, and its scan
# clean: the acceptance plan retains one scan bundle per success, refusal and interruption point,
# since a later bundle cannot show what an earlier outcome left behind. The capture takes time the
# rows after it did not budget for, so the author token is minted again if it is old.
outcome_bundle() {
  evidence_to "$2" || die "no bundle after $2: bin/evidence failed"
  check "$1" "after $2, its bundle and the run directory: no fixture secret but the scan's control" \
    scan_clean "$2"
  fresh_author
}

# interrupt <step> kill|stall, or interrupt -: C's control file, written whole under another name
# and moved into place, so that C never reads half of it; - removes it. interrupt_of b ... is B's.
interrupt() { interrupt_of c "$@"; }
interrupt_of() {
  local control=$STATE/server/$1/interrupt tmp
  shift
  if [ "$1" = - ]; then
    rm -f -- "$control"
    return
  fi
  tmp=$(mktemp -- "$control.XXXXXX") || return 1
  if ! printf '%s %s\n' "$1" "$2" >"$tmp" || ! mv -f -- "$tmp" "$control"; then
    rm -f -- "$tmp"
    return 1
  fi
}
# set_denied_of <a|b> <YAML flow sequence>: that instance's deniedSubjects, through a partial name.
# The configuration directory is bind-mounted: the instance reads it at its next start.
set_denied_of() {
  local cfg=$STATE/server/$1/config.yaml line
  while IFS= read -r line; do
    if [[ $line == '  deniedSubjects:'* ]]; then printf '  deniedSubjects: %s\n' "$2"; else printf '%s\n' "$line"; fi
  done <"$cfg" >"$cfg.partial"
  grep --quiet --line-regexp --fixed-strings -- "  deniedSubjects: $2" "$cfg.partial" || die "no deniedSubjects line in $cfg"
  mv --no-target-directory -- "$cfg.partial" "$cfg"
}
# c_log: instance C's log, by the container ID need_state verified.
c_log() { docker logs "${verified_container_ids[$BW_C]}" 2>&1; }
# c_says <ERE>: a line of C's log matches.
c_says() { c_log | grep --quiet --extended-regexp -- "$1"; }
# kills_at <step>: how many times C's log says the seam ended it at the step, across its restarts.
kills_at() { c_log | grep --count --line-regexp --fixed-strings -- "fixture interrupt: kill at $1" || true; }
# c_killed_at <step> <count>: C is not running, and its log names that many kills at the step.
c_killed_at() {
  [ "$(docker inspect --format '{{.State.Running}}' "${verified_container_ids[$BW_C]}")" = false ] &&
    [ "$(kills_at "$1")" = "$2" ]
}
# within <seconds> <command...>: the command succeeds within that many seconds, asked each second.
within() {
  local n
  for ((n = 1; n < $1; n++)); do
    "${@:2}" && return 0
    sleep 1
  done
  "${@:2}"
}
# worker_links linksplit|linkjoin: A's, B's and C's links to the worker cut, or joined again.
worker_links() {
  local i
  for i in A B C; do "$bin/inject" "$1" "$i" worker || return 1; done
}

# The claim under test.
tk_ing=''
# claim_is <state> <owner generation> payload|none: tk_ing's stored row.
claim_is() {
  [ "$(sql "SELECT state || ' ' || owner_gen || ' ' || CASE WHEN payload IS NULL THEN 'none' ELSE 'payload' END
    FROM staging_claim WHERE id = '$tk_ing'")" = "$1 $2 $3" ]
}
# lapsed: tk_ing's lease has lapsed by the database's clock.
lapsed() { [ "$(sql "SELECT lease_until <= clock_timestamp() FROM staging_claim WHERE id = '$tk_ing'")" = t ]; }
# swept_after_lapse <seconds>: within that many seconds, asked each second, the sweep writes tk_ing
# abandoned at generation 1 without a payload, and not before its lease lapsed (C §3.5). Not before
# is the abandoning transaction's own commit time, which the fixture's database records
# (compose.yaml: track_commit_timestamp), against the lease deadline the abandonment leaves as it
# was: when the row was written, not when a poll saw it. The abandonment is the last write to the
# row, so its xmin is that transaction; no commit time recorded fails, as an unknown.
swept_after_lapse() {
  local n row
  for ((n = 1; n <= $1; n++)); do
    row=$(sql "SELECT state || ' ' || owner_gen || ' ' || CASE WHEN payload IS NULL THEN 'none' ELSE 'payload' END
      || ' ' || coalesce((pg_xact_commit_timestamp(xmin) >= lease_until)::text, 'unknown')
      FROM staging_claim WHERE id = '$tk_ing'") || return 1
    case $row in
      'abandoned 1 none true') return 0 ;;
      'abandoned '*' false')
        say "$scenario: the claim's abandonment committed before its lease lapsed"
        return 1
        ;;
      'abandoned '*' unknown')
        say "$scenario: the database recorded no commit time for the claim's abandonment"
        return 1
        ;;
      'held 1 none '*) ;;
      *)
        say "$scenario: the claim is '$row' while its sweep is awaited"
        return 1
        ;;
    esac
    if [ "$n" -lt "$1" ]; then sleep 1; fi
  done
  say "$scenario: the claim was not abandoned within $1 seconds"
  return 1
}
# claim_generations [claim]: how many generations the metadata identity lists under the claim's
# path in the scenario's cluster (tk_ing's by default); 0 when the path holds none (bao's JSON
# answer is then {} with exit 2).
claim_generations() {
  local out rc=0
  out=$(BAO_TOKEN=$BW_BAO_METADATA_TOKEN bao kv list -format=json -mount=secret "gen/$cluster/${1:-$tk_ing}" 2>&1) || rc=$?
  case $rc in
    0) jq -er 'length' <<<"$out" ;;
    2) [ "$out" = '{}' ] && printf '0\n' ;;
    *) return 1 ;;
  esac
}
# The draft under test and its current ETag.
draft='' etag=''
# draft_etag_is <etag>: the draft's current ETag.
draft_etag_is() { answers 200 - GET "/api/v1/drafts/$draft" "$author" && [ "$(etag_of_last)" = "$1" ]; }

# The claims of the imports that succeeded.
ingested=()
# ingest_kind: the kind ingest asks for, import unless the scenario sets drift-adoption (S5).
ingest_kind=import
# ingest <machine id> <marks JSON array> [key suffix]: POST /ingestions with If-Match the draft's
# ETag, then the claim to its end, the operation's state and the draft's new ETag. A machine's
# second import takes a suffix: its Idempotency-Key is its own.
ingest() {
  local m=$1 marks=$2 body ing op claim n next
  body=$(jq -nc --arg k "$ingest_kind" --arg m "$m" --arg d "$draft" --argjson marks "$marks" \
    '{kind: $k, source: "machine", staging: "transient", machine: $m, draft: $d, marks: $marks}')
  answers 202 - POST /api/v1/ingestions "$author" "$body" "$(key "ingestion-$m${3:+-$3}")" "$etag" || return 1
  if ! ing=$(jq -er '.ingestion' "$EV/last.body") || ! op=$(jq -er '.operation' "$EV/last.body"); then
    say "$scenario: POST /ingestions: no ingestion or operation in the 202"
    return 1
  fi
  # The claim ends released or abandoned; the lease is 15 s, so a minute is generous.
  claim=
  for ((n = 0; n < 60; n++)); do
    answers 200 - GET "/api/v1/ingestions/$ing" "$author" || return 1
    claim=$(jq -er '.state' "$EV/last.body") || claim=
    case $claim in released | abandoned) break ;; esac
    sleep 1
  done
  [ "$claim" = released ] || {
    say "$scenario: ingestion of $m: claim $claim, want released"
    return 1
  }
  ingested+=("$ing")
  answers_with '.state == "succeeded"' 200 - GET "/api/v1/operations/$op" "$author" || return 1
  answers 200 - GET "/api/v1/drafts/$draft" "$author" || return 1
  next=$(etag_of_last)
  [ -n "$next" ] && [ "$next" != "$etag" ] || {
    say "$scenario: the draft's ETag did not move across the ingestion of $m"
    return 1
  }
  etag=$next
}

# The Talos mutation matcher (S1, S3): whether a node's apid log records a mutating request.
# Every Talos API method and its class (internal/talos's TestTalosAPIMethods holds the file to the
# pinned modules and to the read-only guard). MUTATIONS: the mutating ones, one per line.
# mutation_ref: the acceptance-plan reference no_mutation's rows carry, set by the scenario.
METHODS=$(dirname -- "${BASH_SOURCE[0]}")/../talos-api-methods.tsv
MUTATIONS=$(awk -F '\t' '$2 == "mutating" { print $1 }' "$METHODS")
[ -n "$MUTATIONS" ] || die "no mutating method in $METHODS"
mutation_ref=''

# node_talos <node ip> <talosctl args...>: talosctl against one node, through that node.
node_talos() { talosctl_timed 20 -e "$1" -n "$1" "${@:2}"; }
# digest_of <bundle file> <node ip>: the first field of that node's line.
digest_of() { awk -v n="$2" '$2 == n {print $1}' "$1"; }
# apid_log <node ip>: that node's apid log, as its buffer holds it now.
apid_log() { node_talos "$1" logs apid; }
# run_mutations <log before> <log now>: how many Talos mutation requests the log now records past
# the log before. That must be its prefix: a buffer that has dropped its oldest lines since may
# have dropped one of the run's as well, so then there is no count (status 1). A failed extraction
# or count is no count either (status 2), never a count of 0.
run_mutations() {
  [[ "$2" == "$1"* ]] || return 1
  local methods n
  methods=$(log_methods <<<"${2:${#1}}") || return 2
  # grep's status 1 is "no line matched", its count 0; anything above is an error.
  n=$(grep --count --fixed-strings --line-regexp -- "$MUTATIONS" <<<"$methods") || [ $? -eq 1 ] || return 2
  printf '%s\n' "$n"
}
# log_methods: the method each request line names, one per line, from apid lines read on stdin
# (`... OK [/machine.MachineService/Version] 1.3ms stream Success (...)`, whatever the status).
# Whole names, matched whole: EtcdRemoveMember would not match EtcdRemoveMemberByID. Fails when
# grep or sed does; no request line is an empty answer.
log_methods() {
  local lines
  lines=$(grep --only-matching --extended-regexp -- '\[/[A-Za-z0-9_.]+/[A-Za-z0-9_]+\]') || [ $? -eq 1 ] || return 2
  [ -z "$lines" ] || sed 's/^\[\///; s/\]$//' <<<"$lines"
}
# matcher_fails_closed: the matcher's control for its own failure. With grep failing at the
# extraction, and then at the count, a log holding a mutation gives status 2, not a count.
matcher_fails_closed() {
  local line='10.0.0.1: 2026/01/01 00:00:00.000000 log.go:98: OK [/machine.MachineService/MetaWrite] 1ms (runtime=Talos)'
  local which rc out wrong=()
  [ "$(run_mutations '' "$line")" = 1 ] || wrong+=('MetaWrite not counted with grep working')
  for which in --only-matching --count; do
    rc=0
    # shellcheck disable=SC2329 # grep is the shim run_mutations calls, in this subshell only
    out=$(
      grep() { if [[ " $* " == *" $which "* ]]; then return 2; fi; command grep "$@"; }
      run_mutations '' "$line"
    ) || rc=$?
    [ "$rc" -eq 2 ] || wrong+=("grep failing at $which: status $rc, output '$out'")
  done
  [ "${#wrong[@]}" -eq 0 ] || {
    say "$scenario: the matcher did not fail closed: ${wrong[*]}"
    return 1
  }
}
# methods_counted: the matcher's control for every kind. One apid line per classified method, in
# the format the log records, is counted once when the method is mutating and not at all when it is
# a read; FAIL names each method counted otherwise.
methods_counted() {
  local m class n wrong=()
  while IFS=$'\t' read -r m class; do
    case $m in '' | '#'*) continue ;; esac
    n=$(run_mutations '' "10.0.0.1: 2026/01/01 00:00:00.000000 log.go:98: OK [/$m] 1ms stream Success (runtime=Talos)") ||
      n="no count, status $?"
    case $class:$n in mutating:1 | read:0) ;; *) wrong+=("$m ($class, counted $n)") ;; esac
  done <"$METHODS"
  [ "${#wrong[@]}" -eq 0 ] || {
    say "$scenario: miscounted: ${wrong[*]}"
    return 1
  }
}
# no_mutation <label> <node ip> <log before> <log after>: the pass row that the node's apid log
# recorded no mutation request past the log read before the run, written only once a dry-run apply
# on that node, made after the read, appears past it too; FAIL otherwise. Entries are compared,
# not totals, so a run's mutation cannot hide behind an old entry the buffer dropped.
no_mutation() {
  local run ctl log rc=0
  # A pass criterion (acceptance plan S1): unshown is not passed.
  run=$(run_mutations "$3" "$4") || rc=$?
  case $rc in
  0) ;;
  1)
    result FAIL "$mutation_ref" "no Talos mutation in $1's apid log: not shown, the log no longer begins with the one read before the run (its buffer dropped lines)"
    return 0
    ;;
  *)
    result FAIL "$mutation_ref" "no Talos mutation in $1's apid log: not shown, the mutation matcher failed on it"
    return 0
    ;;
  esac
  node_talos "$2" apply-config --dry-run \
    --file <(node_talos "$2" get machineconfig v1alpha1 -o jsonpath='{.spec}') >/dev/null 2>&1 ||
    die "the dry-run apply control did not run on $1"
  log=$(apid_log "$2") || die "cannot read $1's apid log"
  rc=0
  ctl=$(run_mutations "$3" "$log") || rc=$?
  if [ "$rc" -eq 0 ] && [ "$ctl" -gt "$run" ]; then
    check "$mutation_ref" "no Talos mutation request in $1's apid log past the entries read before the run (control: the dry-run apply there is found past them)" \
      same "$run" 0
  else
    result FAIL "$mutation_ref" "no Talos mutation in $1's apid log: not shown, the dry-run apply control is not found past the entries read before the run (matcher status $rc)"
  fi
}

# The plan helpers s3's approval part and s4's controls share, on the worker ($machine_w). Every
# identity's token is minted again once the last is three minutes old (the issuer's tokens live
# five); the automation identity's is bin/seed's, which s3 reads into robot.
declare -A tok_val=() tok_at=()
tok='' robot='' robot_id='' plan_id=''
# fresh <human|robot>: the identity's token in tok. Not a command substitution: the cache and the
# secrets list it updates would be lost with its subshell.
fresh() {
  local h=$1
  if [ "$h" = robot ]; then
    tok=$robot
    return 0
  fi
  if [ -z "${tok_val[$h]:-}" ] || [ $((SECONDS - tok_at[$h])) -ge 180 ]; then
    tok_val[$h]=$(mint "$h") || die "cannot mint a $h token"
    keep_secret "${tok_val[$h]}"
    tok_at[$h]=$SECONDS
  fi
  tok=${tok_val[$h]}
}
# who_id <human|robot>: the identity's principal ID.
who_id() {
  if [ "$1" = robot ]; then printf '%s\n' "$robot_id"; else sql "SELECT id FROM principal WHERE kind = 'human' AND sub = '$1'"; fi
}
# both pause|unpause: A and B. servers pause|unpause: A, B and C.
both() { "$bin/inject" "$1" A && "$bin/inject" "$1" B; }
servers() { both "$1" && "$bin/inject" "$1" C; }
# plan_body [extra JSON object]: an apply-config plan of the worker's Desired: no-reboot, a route
# (the machine's own), the deadlines, the attempt limit, the expiry and the age bound. Two hours:
# S3 step 3's plan must outlive s3 run, s4 controls, s4 restores and s4 run.
plan_body() {
  local extra=${1:-}
  [ -n "$extra" ] || extra='{}'
  jq -nc --arg r "$(sql "SELECT desired FROM machine_state WHERE machine = '$machine_w'")" --arg m "$machine_w" --argjson x "$extra" \
    '{releaseId: $r, machine: $m, operation: "apply-config", mode: "no-reboot", expiresInSeconds: 7200,
      maxObservationAgeSeconds: 300, checkValiditySeconds: 600, transportDeadlineSeconds: 120,
      verificationDeadlineSeconds: 300, maxAttempts: 2} + $x'
}
# plan_by <who> <name> [extra JSON]: an apply-config plan of the worker's Desired created as that
# identity: 201, its id in plan_id.
plan_by() {
  local body
  plan_id=''
  fresh "$1"
  body=$(plan_body "${3:-}") || return 1
  answers 201 - POST /api/v1/plans "$tok" "$body" "$(key "plan-$2")" || return 1
  plan_id=$(jq -er '.id' "$EV/last.body")
}
# approve_by <who> <plan> <name>: 201 naming the plan, the approver, its role and the current
# epoch; the approval in approval_id, its self-approval reasons as a JSON array in
# approval_reasons, the values it was checked against in approval_plan, approval_by and
# approval_epoch.
approval_id='' approval_reasons='' approval_plan='' approval_by='' approval_epoch=''
approve_by() {
  approval_id='' approval_reasons='' approval_plan='' approval_by='' approval_epoch=''
  fresh "$1"
  answers 201 - POST "/api/v1/plans/$2/approvals" "$tok" '{}' "$(key "approval-$3")" || return 1
  # After the request: a principal's row is made by its first authenticated one.
  approval_by=$(who_id "$1") && [ -n "$approval_by" ] || return 1
  approval_epoch=$(sql "SELECT epoch FROM installation_state") && [ -n "$approval_epoch" ] || return 1
  approval_plan=$2
  jq -e --arg p "$approval_plan" --arg a "$approval_by" --arg e "$approval_epoch" \
    '.plan == $p and .approver.principal == $a and .approver.role == "approver" and .epoch == $e
      and .selfApproval.marked == ((.selfApproval.reasons | length) > 0)' "$EV/last.body" >/dev/null || {
    echo "$scenario: $2: the approval answered does not name the plan, the approver, its role and the current epoch" >&2
    return 1
  }
  approval_id=$(jq -er '.id' "$EV/last.body") || return 1
  approval_reasons=$(jq -ce '.selfApproval.reasons' "$EV/last.body")
}
# approved_unseen <plan>: the approval answered last, and its timeline entry, mark it as approving an
# unseen overwrite.
approved_unseen() {
  jq -e --arg p "$1" '.plan == $p and .unseenOverwrite == true' "$EV/last.body" >/dev/null &&
    [ "$(sql "SELECT count(*) FROM machine_event WHERE machine = '$machine_w' AND kind = 'approval'
      AND entry->>'plan' = '$1' AND (entry->>'unseenOverwrite')::boolean")" = 1 ]
}
# keep <name> <collection path>: h-viewer's read of every page of the collection, 500 items at a
# time, following each next cursor, kept in the evidence directory as one array of its items.
keep() {
  local next='' n=0 pages=()
  fresh h-viewer
  while :; do
    n=$((n + 1))
    answers 200 - GET "$2?limit=500${next:+&cursor=$next}" "$tok" || return 1
    cp -- "$EV/last.body" "$EV/$1.page$n" && pages+=("$EV/$1.page$n") || return 1
    next=$(jq -r '.next // empty' "$EV/last.body") || return 1
    [ -n "$next" ] || break
    [[ $next =~ ^[A-Za-z0-9_-]+$ ]] || return 1
  done
  jq -s '[.[].items[]]' "${pages[@]}" >"$EV/$1.json" && rm -f -- "${pages[@]}"
}
# keep_body <name> <path>: h-viewer's read of one resource, kept in the evidence directory.
keep_body() {
  fresh h-viewer
  answers 200 - GET "$2" "$tok" && cp -- "$EV/last.body" "$EV/$1.json"
}
# served_epoch: the Bronzeward-Epoch header of the last response, empty without one.
served_epoch() { tr -d '\r' <"$EV/last.headers" | awk -F': *' 'tolower($1) == "bronzeward-epoch" { v = $2 } END { print v }'; }
# kept <name> <jq filter>: the kept array satisfies the filter.
kept() { jq -e "$2" "$EV/$1.json" >/dev/null; }
# plan_is_stored <plan> <state> <approval or none>: the plan's stored state and approval, and no
# operation.
plan_is_stored() {
  [ "$(sql "SELECT s.state || ' ' || coalesce(s.approval, 'none') || ' ' || coalesce(s.operation, 'none')
    FROM plan_state s WHERE s.plan = '$1'")" = "$2 $3 none" ] &&
    [ "$(sql "SELECT count(*) FROM operation WHERE plan = '$1'")" = 0 ]
}
# plan_reads <plan> <state>: a viewer reads the plan in that state.
plan_reads() {
  fresh h-viewer
  answers_with ".id == \"$1\" and .state == \"$2\" and .committedOperation == null" 200 - GET "/api/v1/plans/$1" "$tok"
}
# plan_state_is <plan> <state>
plan_state_is() { [ "$(sql "SELECT state FROM plan_state WHERE plan = '$1'")" = "$2" ]; }
# refusals <plan>: how many refusal entries name the plan.
refusals() { sql "SELECT count(*) FROM machine_event WHERE kind = 'refusal' AND entry->>'plan' = '$1'"; }
# draft_by <who> <name>: POST /drafts as that identity; draft, etag.
draft_by() {
  fresh "$1"
  answers 201 - POST /api/v1/drafts "$tok" "$(jq -nc --arg c "$cluster" --arg t "$scenario $2" '{cluster: $c, title: $t}')" "$(key "draft-$2")" || return 1
  draft=$(jq -er '.id' "$EV/last.body") || return 1
  etag=$(etag_of_last)
  [ -n "$etag" ]
}
# fragment_by <who> <name> <fragment> [layer]: a fragment of one node label, under the draft's ETag:
# 200, the ETag replaced; its revision in frag_rev. A cluster fragment is selected by no machine's
# profile, so the release compiled from its draft names it as a changed head and no machine's
# configuration changes: it is an unrelated change. A role fragment is one a profile lists (s3's
# profile_by).
frag_rev=''
fragment_by() {
  local body
  fresh "$1"
  body=$(jq -nc --arg n "$3" --arg l "${4:-cluster}" --arg s "$scenario" \
    '{layer: $l, document: ("machine:\n  nodeLabels:\n    bw-" + $s + "-" + $n + ": \"yes\"\n"), marks: []}') || return 1
  answers 200 - PUT "/api/v1/drafts/$draft/fragments/$scenario-$3" "$tok" "$body" "$(key "fragment-$2")" "$etag" || return 1
  differ "$(etag_of_last)" "$etag" || return 1
  etag=$(etag_of_last)
  frag_rev=$(jq -er '.entry.revision' "$EV/last.body")
}
# profile_by <who> <name>: the workers profile, under the draft's ETag, listing the fragment
# revisions its head lists and frag_rev: the release compiled from the draft selects that fragment
# through the worker's assignment, and a later release carries it unchanged.
profile_by() {
  local body
  fresh "$1"
  body=$(sql "SELECT coalesce(jsonb_agg(f.fragment_revision ORDER BY f.position), '[]'::jsonb)
    FROM profile p JOIN profile_revision_fragment f ON f.revision = p.head_revision_id
    WHERE p.cluster = '$cluster' AND p.name = 'workers'") || return 1
  body=$(jq -nc --argjson l "$body" --arg f "$frag_rev" '{fragments: ($l + [$f])}') || return 1
  answers 200 - PUT "/api/v1/drafts/$draft/profiles/workers" "$tok" "$body" "$(key "profile-$2")" "$etag" || return 1
  differ "$(etag_of_last)" "$etag" || return 1
  etag=$(etag_of_last)
}
# publish_by <who> <name>: the draft published under its current ETag: 202, the operation
# succeeded with one release, which is then the worker's Desired; its id in new_release.
new_release='' pub_op=''
pub_succeeded() {
  [ "$(sql "SELECT state FROM operation WHERE id = '$pub_op'")" = succeeded ] &&
    [ "$(sql "SELECT count(*) FROM release WHERE operation = '$pub_op'")" = 1 ]
}
publish_by() {
  new_release=''
  fresh "$1"
  answers 202 - POST "/api/v1/drafts/$draft/publications" "$tok" '{}' "$(key "publication-$2")" "$etag" || return 1
  pub_op=$(jq -er '.id' "$EV/last.body") || return 1
  within 120 pub_succeeded || return 1
  new_release=$(sql "SELECT id FROM release WHERE operation = '$pub_op'")
  [ -n "$new_release" ] &&
    [ "$(sql "SELECT desired FROM machine_state WHERE machine = '$machine_w'")" = "$new_release" ]
}

# The age control's pieces, for an adopt or an apply-config plan alike.
# obs_started <plan>: the plan's evidence observation is recorded.
obs_started() {
  [ -n "$(sql "SELECT o.id FROM observation o JOIN observation_start s ON s.machine = o.machine AND s.revision = o.basis
    WHERE s.plan = '$1' AND s.purpose = 'evidence'")" ]
}
# first_observation <plan>: the id of the plan's first recorded evidence observation.
first_observation() {
  sql "SELECT o.id FROM observation o JOIN observation_start s ON s.machine = o.machine AND s.revision = o.basis
    WHERE s.plan = '$1' AND s.purpose = 'evidence' ORDER BY o.basis LIMIT 1"
}
# observation_stale <plan> <seconds>: the plan's first evidence observation began more than that
# long ago, by the database's clock.
observation_stale() {
  [ "$(sql "SELECT clock_timestamp() > e.at + interval '$2 seconds' FROM observation o
    JOIN observation_start s ON s.machine = o.machine AND s.revision = o.basis
    JOIN machine_event e ON e.machine = s.machine AND e.revision = s.revision
    WHERE s.plan = '$1' AND s.purpose = 'evidence' ORDER BY o.basis LIMIT 1")" = t ]
}
