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

# call <method> <path> <token> [body] [idempotency key] [if-match]: the status of a request to the
# instance on port $call_port (A's unless the scenario sets it), 000 when nothing answers; the
# response headers and body are left in $EV/last.headers and $EV/last.body, both emptied first.
# The token goes over stdin and the body through a file only the user reads: a command line is
# readable by any local user.
call_port=$SERVER_A_PORT
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
    command curl --disable --noproxy '*' --silent --max-time 10 --output "$EV/last.body" \
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
# and moved into place, so that C never reads half of it; - removes it.
interrupt() {
  local control=$STATE/server/c/interrupt tmp
  if [ "$1" = - ]; then
    rm -f -- "$control"
    return
  fi
  tmp=$(mktemp -- "$STATE/server/c/interrupt.XXXXXX") || return 1
  if ! printf '%s %s\n' "$1" "$2" >"$tmp" || ! mv -f -- "$tmp" "$control"; then
    rm -f -- "$tmp"
    return 1
  fi
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
# abandoned at generation 1 without a payload, and not before its lease lapsed (C §3.5). The state
# and the lapse are read in one statement by the database's clock (an abandonment leaves
# lease_until as it was), so an abandonment seen while the lease is still live fails at once.
swept_after_lapse() {
  local n row
  for ((n = 1; n <= $1; n++)); do
    row=$(sql "SELECT state || ' ' || owner_gen || ' ' || CASE WHEN payload IS NULL THEN 'none' ELSE 'payload' END
      || ' ' || (lease_until <= clock_timestamp())::text FROM staging_claim WHERE id = '$tk_ing'") || return 1
    case $row in
      'abandoned 1 none true') return 0 ;;
      'abandoned '*' false')
        say "$scenario: the claim was abandoned while its lease was still live"
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
