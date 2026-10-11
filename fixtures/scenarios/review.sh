# shellcheck shell=bash
# The sourcing scenario sets the variables these helpers read (SC2154) and reads the ones they set
# (SC2034); the conditions handed to within are evaluated when it asks, not when they are written
# (SC2016).
# shellcheck disable=SC2016,SC2034,SC2154
# S1's operator review rows (compilation §3.6, persistence and API §16): imports of the worker,
# encrypted, with review: true and no marks, so the worker's file secret is staged unextracted
# inside the envelope. Pause, show, a restart, refused and extracting marks, a mark's run killed at
# each step it re-enters and a continuation's run killed before its draft transaction, each resumed
# by takeover, a mark failing after a provider write, an operator's abandonment and the sweep at the
# absolute expiry, every outcome bundle scanned. Sourced by s1 after common.sh, on instance C.

# The run's events up to its first pause: its start, the Talos access version read, the envelope
# stored and the pause.
paused_events='started talos-access staged paused'
# The mark that extracts the worker's file secret, and a second one on a string beside it, so that
# step 6 makes two generations and a kill can fall between them.
SECRET_MARKS=$(jq -nc --arg p "$WORKER_MARK" '[$p]')
PAIR_MARKS=$(jq -nc --arg p "$WORKER_MARK" '[$p, "doc[0]/machine/files/0/path"]')
# A mark on the file list itself: a sequence, refused mark-kind before any provider write.
KIND_MARKS='["doc[0]/machine/files"]'
# The points a mark's run passes as it re-extracts the staged document (compilation §2.3 steps 2
# to 7), step 6 split after its first generation, and the point after its new envelope is
# encrypted and before it is stored.
mark_points='parse identify substitute guard generation-first generation construct envelope'
# The expiry claim, started early so that its absolute expiry passes during the run.
expiry_ing='' expiry_op=''

# review_import <key name> <A|C>: the worker imported on that instance, encrypted, review: true, no
# marks, into a draft of its own, since an ingestion of a draft revision runs alone and a paused one
# still runs: 202, its claim and operation in tk_ing and tk_op, its draft and the draft's ETag in
# rv_draft and rv_etag.
rv_draft='' rv_etag=''
review_import() {
  local body rc=0
  tk_ing='' tk_op='' rv_draft='' rv_etag=''
  answers 201 - POST /api/v1/drafts "$author" "$(jq -nc --arg c "$cluster" --arg t "S1 review $1" '{cluster: $c, title: $t}')" \
    "$(key "draft-review-$1")" || return 1
  rv_draft=$(jq -er '.id' "$EV/last.body") && rv_etag=$(etag_of_last) && [ -n "$rv_etag" ] || return 1
  body=$(jq -nc --arg m "$machine_w" --arg d "$rv_draft" \
    '{kind: "import", source: "machine", staging: "encrypted", review: true, machine: $m, draft: $d, marks: []}')
  [ "$2" = A ] || call_port=$SERVER_C_PORT
  answers 202 - POST /api/v1/ingestions "$author" "$body" "$(key "import-review-$1")" "$rv_etag" || rc=1
  call_port=$SERVER_A_PORT
  [ "$rc" -eq 0 ] || return 1
  tk_ing=$(jq -er '.ingestion' "$EV/last.body") && tk_op=$(jq -er '.operation' "$EV/last.body")
}
# review_is <review>: tk_ing's review column.
review_is() { [ "$(sql "SELECT coalesce(review, 'none') FROM staging_claim WHERE id = '$tk_ing'")" = "$1" ]; }
# digest_now: tk_ing's stored envelope digest, hex (a digest, not the envelope).
digest_now() { sql "SELECT encode(payload_digest, 'hex') FROM staging_claim WHERE id = '$tk_ing'"; }
# digest_is <hex>: tk_ing's envelope is still that one.
digest_is() { [ "$(digest_now)" = "$1" ]; }
# events_end <list>: tk_op's last events, in order, are the list.
events_end() {
  local n
  n=$(wc -w <<<"$1")
  [ "$(sql "SELECT string_agg(t, ' ' ORDER BY number) FROM (SELECT entry->>'type' AS t, number FROM operation_event
    WHERE operation = '$tk_op' ORDER BY number DESC LIMIT $n) e")" = "$(tr -s ' ' <<<"$1")" ]
}
# op_running: tk_op is still running, read in the database.
op_running() { [ "$(sql "SELECT state FROM operation WHERE id = '$tk_op'")" = running ]; }
# paused_at <generation>: tk_ig paused at that owner generation with its envelope, its review still
# pending, its lease ended and its operation running.
paused_at() {
  claim_is paused "$1" payload && review_is pending && lapsed && op_running
}
# generations_are <n>: the metadata identity lists n generations under tk_ing.
generations_are() {
  local got
  got=$(claim_generations) || return 1
  [ "$got" = "$1" ] || {
    say "s1: $got generation(s) listed under the claim, want $1"
    return 1
  }
}

# shown present|absent: the author's GET /ingestions/<tk_ing>/review on A answers 200 under
# Cache-Control: no-store, its document holding the worker's synthetic file secret or not. This is
# the one route that answers input text (persistence and API §9.1): the answer is held in this
# shell only and piped to jq, never written to a file (a here-string can be), and its transcript
# records the status alone. The secret's prefix is written in two parts here, since the leak scan
# looks for it whole in the bundle's diff of an uncommitted fixtures tree.
shown() {
  local out head body status
  out=$({ printf 'Authorization: Bearer %s\n' "$author"; } |
    command curl --disable --noproxy '*' --silent --max-time 10 --include --header @- \
      "http://127.0.0.1:$SERVER_A_PORT/api/v1/ingestions/$tk_ing/review") || return 1
  head=${out%%$'\r\n\r\n'*}
  body=${out#*$'\r\n\r\n'}
  status=$(awk 'NR == 1 { print $2 }' <<<"$head")
  http_n=$((http_n + 1))
  printf '> GET /api/v1/ingestions/%s/review\n> Authorization: Bearer <withheld>\n< status %s\n(the answer is withheld: the review route returns input text)\n' \
    "$tk_ing" "$status" >"$(printf '%s/http/%04d.txt' "$EV" "$http_n")"
  [ "$status" = 200 ] || { say "s1: the review route answered $status, want 200"; return 1; }
  grep --quiet --ignore-case '^cache-control: no-store'$'\r''\?$' <<<"$head" || {
    say "s1: the review route's answer is not no-store"
    return 1
  }
  case $1 in
    present) printf '%s' "$body" | jq -e '.document | contains("BWSYNTH" + "-")' >/dev/null ;;
    absent) printf '%s' "$body" | jq -e '(.document | type == "string") and (.document | contains("BWSYNTH" + "-") | not)' >/dev/null ;;
  esac
}
# accepted <A|C> <jq filter> <path> <body> <key>: the author's POST to that instance answers 202
# and a body satisfying the filter. C commits its 202 before the run starts, but sends it only
# once the handler returns, so a kill early in the run can end C between the two; the same request
# to A then gets the committed answer replayed under its Idempotency-Key, never a second action.
accepted() {
  local rc=0
  [ "$1" = A ] || call_port=$SERVER_C_PORT
  answers_with "$2" 202 - POST "$3" "$author" "$4" "$5" || rc=1
  call_port=$SERVER_A_PORT
  if [ "$rc" -ne 0 ] && [ "$1" = C ] && [ "$last_status" = 000 ]; then
    rc=0
    answers_with "$2" 202 - POST "$3" "$author" "$4" "$5" || rc=1
    [ "$rc" -ne 0 ] || grep --quiet --ignore-case '^Idempotent-Replayed: true' "$EV/last.headers" || {
      say "s1: the request C did not answer was not a replay on A"
      rc=1
    }
  fi
  return "$rc"
}
# mark <marks> <key name> <generation> [A|C]: the author's mark of tk_ing on that instance (A by
# default): 202, its body naming that owner generation and neither a path nor a synthetic secret.
mark() {
  accepted "${4:-A}" "(.generation == $3) and (tostring | (contains(\"doc[\") or contains(\"BWSYNTH\" + \"-\")) | not)" \
    "/api/v1/ingestions/$tk_ing/marks" "{\"marks\": $1}" "$(key "mark-$2")"
}
# continue_review <key name> [A|C]: the author's continuation of tk_ing on that instance: 202.
continue_review() {
  accepted "${2:-A}" '.ingestion | type == "string"' "/api/v1/ingestions/$tk_ing/continuations" '{}' "$(key "continue-$1")"
}
# refused_kind: tk_op's last mark-refused event names the rule mark-kind and the mark by its
# position 0, and no path.
refused_kind() {
  sql "SELECT entry->'problem' FROM operation_event WHERE operation = '$tk_op' AND entry->>'type' = 'mark-refused'
    ORDER BY number DESC LIMIT 1" |
    jq -e '.rule == "mark-kind" and .position == 0 and (tostring | contains("doc[") | not)' >/dev/null
}
# op_failed_with <problem code>: tk_op failed with that problem, read in the database.
op_failed_with() {
  [ "$(sql "SELECT state || ' ' || (error->>'type') FROM operation WHERE id = '$tk_op'")" = "failed urn:bronzeward:problem:$1" ]
}
# released_succeeded: tk_ing released without a payload, tk_op succeeded.
released_succeeded() {
  [ "$(sql "SELECT c.state || ' ' || CASE WHEN c.payload IS NULL THEN 'none' ELSE 'payload' END || ' ' || o.state
    FROM staging_claim c JOIN operation o ON o.ingestion = c.id WHERE c.id = '$tk_ing'")" = 'released none succeeded' ]
}
# abandoned_failed: tk_ing abandoned without a payload, tk_op failed ingestion-abandoned.
abandoned_failed() {
  [ "$(sql "SELECT c.state || ' ' || CASE WHEN c.payload IS NULL THEN 'none' ELSE 'payload' END || ' ' || o.state || ' ' || (o.error->>'type')
    FROM staging_claim c JOIN operation o ON o.ingestion = c.id WHERE c.id = '$tk_ing'")" = 'abandoned none failed urn:bronzeward:problem:ingestion-abandoned' ]
}

# continued_marked: the import base revision tk_op's success names holds the worker's file content
# as a reference, matched in the database, never printed.
continued_marked() {
  local rev
  rev=$(sql "SELECT result->>'importBaseRevision' FROM operation WHERE id = '$tk_op'") && [ -n "$rev" ] || return 1
  [ "$(sql "SELECT count(*) FROM import_base_revision WHERE id = '$rev' AND document ~ '\\ycontent: !bwref '")" = 1 ]
}

# expiry_start: a reviewed import on A that nothing marks or continues; it pauses, and the sweep
# abandons it at its absolute expiry (expiry_rows, after the rows that run meanwhile). Its
# generations are counted while it is paused, before anything can abandon it.
expiry_gens=''
expiry_start() {
  fresh_author
  check 'S1 review, PA §16' 'expiry claim: a reviewed import of the worker on A: 202' review_import expiry A
  [ -n "$tk_op" ] || die "s1: the reviewed import on A was not accepted; nothing after it can run"
  check 'S1 review, PA §16' "expiry claim: paused at generation 1 with its envelope, its lease ended, the operation running" \
    within 60 paused_at 1
  expiry_ing=$tk_ing expiry_op=$tk_op
  expiry_gens=$(claim_generations) || die "s1: cannot list the expiry claim's generations"
  check 'S1 review, PA §16' "expiry claim: control, it made $expiry_gens generation(s) for the orphan report" differ "$expiry_gens" 0
}
# abandoned_after_expiry: tk_ing abandoned, its abandonment committed at or after its absolute
# expiry: the abandoning transaction's own commit time (compose.yaml: track_commit_timestamp), not
# when a poll saw it, as swept_after_lapse reads it. The abandonment is the last write to the row,
# so its xmin is that transaction; no commit time recorded fails, as an unknown.
abandoned_after_expiry() {
  local row
  row=$(sql "SELECT state || ' ' || coalesce((pg_xact_commit_timestamp(xmin) >= expires_at)::text, 'unknown')
    FROM staging_claim WHERE id = '$tk_ing'") || return 1
  [ "$row" = 'abandoned true' ] || {
    say "s1: the expiry claim is '$row' (state, abandonment committed at or after the absolute expiry)"
    return 1
  }
}
# expiry_rows: the expiry claim, still paused until its absolute expiry, abandoned by the sweep after
# it; its generations kept, for the orphan report (interrupted).
expiry_rows() {
  local gens=$expiry_gens
  tk_ing=$expiry_ing tk_op=$expiry_op
  # The fixture's absolute expiry is 10 minutes and its sweep runs every 15 seconds.
  check 'S1 review, PA §16' 'expiry claim: the sweep abandons it after its absolute expiry, the operation failed ingestion-abandoned' \
    within 720 abandoned_failed
  check 'S1 review, PA §16' 'expiry claim: its abandonment committed at or after its absolute expiry' abandoned_after_expiry
  check 'S1 review, PA §16' 'expiry claim: its generations still listed, none deleted' generations_are "$gens"
  interrupted+=("$tk_ing $gens")
  outcome_bundle 'S1 review, PA §16' review-expired
}

# mark_kill_case <step> <generation> <generations before> <digest>: C told to exit at the step, a
# mark of both values on C takes the claim at the next generation and its run exits there; once
# the lease lapses A's takeover resumes from the stored envelope and pauses again without the
# marks, the envelope unchanged; the generations created before the kill are kept. The bundle is
# captured while C is still stopped; C is started again after. Leaves the claim's generation in
# case_gen and its generation count in case_gens.
case_gen='' case_gens=''
mark_kill_case() {
  local step=$1 gen=$2 before=$3 digest=$4 kills made ref='S1 review kill, PA §16'
  fresh_author
  kills=$(kills_at "$step")
  case $step in
    generation-first) made=1 ;;
    generation | construct | envelope) made=2 ;;
    *) made=0 ;;
  esac
  check "$ref" "C told to exit at once at $step" interrupt "$step" kill
  check "$ref" "a mark of two values on C: 202 at generation $((gen + 1))" mark "$PAIR_MARKS" "kill-$step" "$((gen + 1))" C
  check "$ref" "C exits at $step in the mark's run: the claim held at generation $((gen + 1)) with its envelope, the marked event last" \
    within 60 eval 'c_killed_at "$step" "$((kills + 1))" && claim_is held "$((gen + 1))" payload && events_end marked'
  check "$ref" "C's control file removed" interrupt -
  check "$ref" "killed at $step: the lease lapses" within 40 lapsed
  check "$ref" "killed at $step: A's takeover: 202" takeover A 202 - "mark-$step"
  check "$ref" "killed at $step: A resumes from the stored envelope and pauses again without the marks, at generation $((gen + 2))" \
    within 60 eval 'paused_at "$((gen + 2))" && events_end "marked taken-over paused" && digest_is "$digest"'
  check "$ref" "killed at $step: the $made generation(s) the mark made before the kill kept with the earlier ones, none deleted" \
    generations_are "$((before + made))"
  outcome_bundle "$ref" "review-kill-$step"
  check "$ref" 'inject start C' "$bin/inject" start C
  case_gen=$((gen + 2)) case_gens=$((before + made))
}

# renew_short <seconds> <key name>: when tk_ing's absolute expiry is less than that many seconds
# away, its author abandons it and a new reviewed import of the worker on C, paused at generation 1,
# takes its place for the rows after; returns 1 when the claim had time enough. The fixture's
# absolute expiry (10 minutes) is shorter than the rows on one claim: the sweep would abandon it in
# the middle of a case. The abandoned claim's generations go to the orphan report. Leaves the new
# claim's envelope digest and generation count in rv_digest and rv_gens.
rv_digest='' rv_gens=''
renew_short() {
  local left gens
  left=$(sql "SELECT floor(extract(epoch FROM expires_at - clock_timestamp())) FROM staging_claim WHERE id = '$tk_ing'") ||
    die "s1: cannot read the reviewed claim's absolute expiry"
  [ "$left" -lt "$1" ] || return 1
  say "s1: $left s left before the reviewed claim's absolute expiry; the rows continue on a new one"
  gens=$(claim_generations) || die "s1: cannot list the claim's generations"
  fresh_author
  check 'S1 review, PA §16' 'near its absolute expiry, the author abandons the paused claim: 200' \
    answers 200 - POST "/api/v1/ingestions/$tk_ing/abandonments" "$author" '{}' "$(key "abandon-$2")"
  check 'S1 review, PA §16' 'the claim abandoned without a payload, the operation failed ingestion-abandoned' abandoned_failed
  check 'S1 review, PA §16' "the abandoned claim's $gens generation(s) kept, none deleted" generations_are "$gens"
  interrupted+=("$tk_ing $gens")
  check 'S1 review, PA §16' 'a new reviewed import of the worker on C: 202' review_import "$2" C
  [ -n "$tk_op" ] || die "s1: the reviewed import on C was not accepted; nothing after it can run"
  check 'S1 review, PA §16' 'the new import pauses at generation 1' within 60 eval 'paused_at 1 && events_are "$paused_events"'
  rv_digest=$(digest_now) || die "s1: cannot read the claim's envelope digest"
  rv_gens=$(claim_generations) || die "s1: cannot list the claim's generations"
}

# review_rows: the operator review on one reviewed import of the worker, then its failure cases on
# two more.
review_rows() {
  local robot digest gens gen step revisions t
  robot=$(cat -- "$STATE/automation-token") || die "no automation token in $STATE"
  redact_automation_token "$robot"
  fresh_author
  check 'S1 review, PA §16' 'review: true with transient staging: 422 validation-failed, rule review-needs-encrypted-staging' \
    answers_with '.rule == "review-needs-encrypted-staging"' 422 validation-failed POST /api/v1/ingestions "$author" \
    "$(jq -nc --arg m "$machine_w" --arg d "$draft" \
      '{kind: "import", source: "machine", staging: "transient", review: true, machine: $m, draft: $d, marks: []}')" \
    "$(key review-transient)" "$etag"

  # Pause and show.
  check 'S1 review, PA §16' 'a reviewed import of the worker on C: 202' review_import main C
  [ -n "$tk_op" ] || die "s1: the reviewed import on C was not accepted; nothing after it can run"
  check 'S1 review, PA §16' 'the run pauses: the claim paused at generation 1 with its envelope, its lease ended, the operation running, its events started to paused' \
    within 60 eval 'paused_at 1 && events_are "$paused_events"'
  digest=$(digest_now) || die "s1: cannot read the claim's envelope digest"
  gens=$(claim_generations) || die "s1: cannot list the claim's generations"
  check 'S1 review, PA §16' "control: the reviewed import created $gens generation(s) for the schema-listed secrets" differ "$gens" 0
  check 'S1 review, PA §16' 'the review route, as the author: 200, no-store, the unmarked file secret shown' shown present
  check 'S1 review, PA §16' 'the review route, as the automation identity: 403 forbidden' \
    answers 403 forbidden GET "/api/v1/ingestions/$tk_ing/review" "$robot"
  outcome_bundle 'S1 review, PA §16' review-paused

  # Kept paused across a restart of the instance that ran it.
  check 'S1 review, PA §16' 'inject kill C' "$bin/inject" kill C
  check 'S1 review, PA §16' 'inject start C' "$bin/inject" start C
  t=$(sql "SELECT extract(epoch FROM clock_timestamp()) + 16") || die "s1: cannot read the database's clock"
  check 'S1 review, PA §16' "a sweep interval after C's restart" within 30 past "$t"
  check 'S1 review, PA §16' 'C restarted: the claim still paused at generation 1, its envelope unchanged' \
    eval 'paused_at 1 && digest_is "$digest"'

  # A mark refused before any provider write.
  check 'S1 review, PA §16' 'a mark on the file list (a sequence): 202 at generation 2' mark "$KIND_MARKS" kind 2
  check 'S1 review, PA §16' 'the mark is refused: mark-refused naming mark-kind and position 0, no path; paused again at generation 2' \
    within 60 eval 'paused_at 2 && events_end "marked mark-refused paused" && refused_kind'
  check 'S1 review, PA §16' 'the refused mark: the envelope unchanged, no generation created' \
    eval 'digest_is "$digest" && generations_are "$gens"'
  outcome_bundle 'S1 review, PA §16' review-mark-refused

  # A mark's run killed at each step it re-enters, each resumed by A's takeover.
  gen=2
  for step in $mark_points; do
    # One case takes about 80 seconds, most of it the lapse of C's lease.
    if renew_short 180 "kill-$step"; then gen=1 gens=$rv_gens digest=$rv_digest; fi
    mark_kill_case "$step" "$gen" "$gens" "$digest"
    gen=$case_gen gens=$case_gens
  done
  fresh_author
  check 'S1 review kill, PA §16' 'after the killed marks, the review still shows the unmarked file secret' shown present

  # The mark that extracts the file secret, then the continuation: about two minutes.
  if renew_short 240 secret; then gen=1 gens=$rv_gens digest=$rv_digest; fi
  check 'S1 review, PA §16' "a mark of the file secret: 202 at generation $((gen + 1))" mark "$SECRET_MARKS" secret "$((gen + 1))"
  check 'S1 review, PA §16' "the mark's run pauses again at generation $((gen + 1)) with a new envelope, one more generation" \
    within 60 eval 'paused_at "$((gen + 1))" && events_end "marked paused" && ! digest_is "$digest" && generations_are "$((gens + 1))"'
  check 'S1 review, PA §16' 'the review no longer shows the file secret: it is a reference' shown absent
  outcome_bundle 'S1 review, PA §16' review-marked
  gen=$((gen + 1)) gens=$((gens + 1))

  # The continuation, its run killed before the draft transaction and resumed by A's takeover.
  revisions=$(sql 'SELECT count(*) FROM import_base_revision') || die "s1: cannot count import base revisions"
  local kills
  kills=$(kills_at draft)
  check 'S1 review kill, PA §16' 'C told to exit at once at draft' interrupt draft kill
  check 'S1 review kill, PA §16' 'the continuation on C: 202' continue_review draft C
  check 'S1 review kill, PA §16' "C exits before the draft transaction: the claim held at generation $((gen + 1)), continued, the continued event last" \
    within 60 eval 'c_killed_at draft "$((kills + 1))" && claim_is held "$((gen + 1))" payload && review_is continued && events_end continued'
  check 'S1 review kill, PA §16' "C's control file removed" interrupt -
  check 'S1 review kill, PA §16' 'killed at draft: the lease lapses' within 40 lapsed
  check 'S1 review kill, PA §16' "killed at draft: A's takeover: 202" takeover A 202 - continue-draft
  check 'S1 review kill, PA §16' 'A resumes and commits: the claim released without a payload, the operation succeeded, one new import base revision' \
    within 60 eval 'released_succeeded && events_end "continued taken-over succeeded" && revisions_are "$((revisions + 1))"'
  check 'S1 review, PA §16' 'the continued import: its import base revision holds the marked file secret as a reference' continued_marked
  check 'S1 review, PA §16' 'the continued import: its generations kept' generations_are "$gens"
  outcome_bundle 'S1 review kill, PA §16' review-continued
  check 'S1 review kill, PA §16' 'inject start C' "$bin/inject" start C
  answers 200 - GET "/api/v1/drafts/$rv_draft" "$author" || die "s1: cannot read the reviewed claim's draft"
  check 'S1 review, PA §16' "the continuation's draft transaction moved its draft's ETag" differ "$(etag_of_last)" "$rv_etag"

  # A mark failing after a provider write: its run stalled after the first generation, OpenBao
  # partitioned, then released: the second create fails, and the claim is abandoned.
  fresh_author
  check 'S1 review, PA §16' 'a second reviewed import of the worker on C: 202' review_import provider C
  [ -n "$tk_op" ] || die "s1: the reviewed import on C was not accepted; nothing after it can run"
  check 'S1 review, PA §16' 'the second import pauses at generation 1' within 60 paused_at 1
  gens=$(claim_generations) || die "s1: cannot list the claim's generations"
  check 'S1 review, PA §16' 'C told to stall its next run after its first generation' interrupt generation-first stall
  check 'S1 review, PA §16' 'a mark of two values on C: 202 at generation 2' mark "$PAIR_MARKS" provider 2 C
  check 'S1 review, PA §16' "C's mark run stalls after its first generation" \
    within 60 c_says '^fixture interrupt: stall at generation-first$'
  if ! "$bin/inject" netsplit openbao >/dev/null; then
    "$bin/inject" netjoin openbao >/dev/null || die "s1: OpenBao could not be put back on its network"
    die "s1: OpenBao could not be partitioned"
  fi
  check 'S1 review, PA §16' "C's stall released, OpenBao partitioned" interrupt -
  # The second create fails at once on a fresh dial, or at the provider's 30-second request bound.
  check 'S1 review, PA §16' 'the second create fails after the first was written: the claim abandoned, the operation failed' \
    within 60 eval 'claim_is abandoned 2 none && op_failed_with dependency-unavailable'
  # The failed create's request can still sit in C's socket, retransmitted, and land once OpenBao
  # is back: a generation written after its claim was abandoned, an orphan of it (seen 23 seconds
  # after the join). C is stopped first, its network namespace and the socket with it, so that the
  # count below is exact; it is started again after the bundle.
  check 'S1 review, PA §16' 'inject kill C' "$bin/inject" kill C
  "$bin/inject" netjoin openbao >/dev/null || die "s1: OpenBao could not be put back on its network"
  check 'S1 review, PA §16' "the mark's first generation kept with the earlier ones, none deleted" generations_are "$((gens + 1))"
  interrupted+=("$tk_ing $((gens + 1))")
  outcome_bundle 'S1 review, PA §16' review-mark-provider-failed
  check 'S1 review, PA §16' 'inject start C' "$bin/inject" start C

  # An operator's abandonment of a paused claim.
  check 'S1 review, PA §16' 'a third reviewed import of the worker on A: 202' review_import abandon A
  [ -n "$tk_op" ] || die "s1: the reviewed import on A was not accepted; nothing after it can run"
  check 'S1 review, PA §16' 'the third import pauses at generation 1' within 60 paused_at 1
  gens=$(claim_generations) || die "s1: cannot list the claim's generations"
  check 'S1 review, PA §16' "control: the paused claim made $gens generation(s) for the orphan report" differ "$gens" 0
  check 'S1 review, PA §16' "the author abandons the paused claim: 200" \
    answers 200 - POST "/api/v1/ingestions/$tk_ing/abandonments" "$author" '{}' "$(key abandon-review)"
  check 'S1 review, PA §16' 'the claim abandoned without a payload, the operation failed ingestion-abandoned' abandoned_failed
  check 'S1 review, PA §16' "the abandoned claim's $gens generation(s) kept, none deleted" generations_are "$gens"
  interrupted+=("$tk_ing $gens")
  outcome_bundle 'S1 review, PA §16' review-abandoned
}
