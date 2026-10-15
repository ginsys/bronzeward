# shellcheck shell=bash
# The sourcing scenario sets the plan and row state these helpers read and leaves the results they
# set for its own checks (SC2034, SC2154).
# shellcheck disable=SC2034,SC2154
# The dispatch helpers S4's DS re-run and S6 share, on the worker ($machine_w). Sourced after
# common.sh by a scenario that has set `ds_ref`, the acceptance-plan reference a row's checks carry
# before the row's name, and `mutation_ref`, the reason its revocations and accountings record. Each
# predicate takes IDs and counts only; plan, rel, op, held, taker, start and end are the scenario's.

machine_at() {
  sql "SELECT id FROM machine WHERE cluster = '$cluster' AND talos_endpoint::text = '$1:50000'
    AND talos_node_id NOT LIKE 's1-no-such-node-%'"
}
one_line() { [ -n "$1" ] && [ "$(wc -l <<<"$1")" = 1 ]; }
worker_joined() { [ ! -f "$STATE/linksplits" ] || counted 0 '^[ABC] worker$' "$STATE/linksplits"; }
machines() {
  cluster=$(sql "SELECT id FROM cluster WHERE name = 'bw-fixture'") || die "$scenario: cannot read the cluster"
  machine_cp=$(machine_at "$TALOS_CONTROLPLANE_IP") || die "$scenario: cannot read the control plane's machine"
  one_line "$machine_cp" || die "$scenario: not one machine at the control plane's endpoint: run scenarios/s1 run first"
  machine_w=$(machine_at "$TALOS_WORKER_IP") || die "$scenario: cannot read the worker's machine"
  one_line "$machine_w" || die "$scenario: not one machine at the worker's endpoint: run scenarios/s1 run first"
}

# completed: the plan committed to an apply-config operation that completed; its id in op.
completed() {
  op=$(sql "SELECT o.id FROM plan_state s JOIN operation o ON o.id = s.operation
    WHERE s.plan = '$plan' AND s.state = 'committed' AND o.kind = 'apply-config' AND o.state = 'completed'")
  [ -n "$op" ]
}
# scope_released: no committed, sending, verifying or unresolved apply-config operation holds the
# worker's scope or the cluster's rollout slot.
scope_released() {
  [ "$(sql "SELECT count(*) FROM operation WHERE kind = 'apply-config' AND state IN ('committed', 'sending', 'verifying', 'unresolved')
    AND (machine = '$machine_w' OR cluster = '$cluster')")" = 0 ]
}
# one_apply <log before> <log now>: exactly one mutating request past the log before, and it is
# ApplyConfiguration.
one_apply() {
  local methods
  [[ "$2" == "$1"* ]] || return 1
  [ "$(run_mutations "$1" "$2")" = 1 ] || return 1
  # Read whole first: grep --quiet stops at its match, and under pipefail a writer still writing
  # into the pipe would fail the check.
  methods=$(log_methods <<<"${2:${#1}}") || return 1
  grep --quiet --fixed-strings --line-regexp -- machine.MachineService/ApplyConfiguration <<<"$methods"
}
# nothing_since <operation seq>: no apply-config operation exists since that sequence number.
nothing_since() { [ "$(sql "SELECT count(*) FROM operation WHERE seq > $1 AND kind = 'apply-config'")" = 0 ]; }
# stalls_at <step>: how many times C's log says the seam stalled it at the step.
stalls_at() { c_log | grep --count --line-regexp --fixed-strings -- "fixture interrupt: stall at $1" || true; }
# committed_held <plan> <stalls before>: the plan committed to an apply-config operation that is
# still committed with no attempt, its id in held, and C stalled after the commitment once more.
committed_held() {
  held=$(sql "SELECT o.id FROM plan_state s JOIN operation o ON o.id = s.operation WHERE s.plan = '$1'
    AND s.state = 'committed' AND o.kind = 'apply-config'") || return 1
  [ -n "$held" ] && still_held && [ "$(stalls_at dispatch-committed)" -gt "$2" ]
}
# still_held: held is committed, with no attempt.
still_held() {
  [ "$(sql "SELECT o.state || ' ' || count(a.id) FROM operation o LEFT JOIN attempt a ON a.operation = o.id
    WHERE o.id = '$held' GROUP BY o.state")" = 'committed 0' ]
}
# must <ref> <what> <command...>: a prerequisite of what follows: the command succeeds, recorded
# PASS, or the run stops (check records a FAIL and goes on).
must() {
  local ref=$1 what=$2
  shift 2
  "$@" || die "$scenario: $ref: $what failed; nothing after it can run"
  result PASS "$ref" "$what"
}
# attempt_admitted <plan> <stalls before>: the plan committed, its operation sending with one
# attempt and no response, its id in held; C stalled once more after the attempt, so nothing is sent.
attempt_admitted() {
  held=$(sql "SELECT operation FROM plan_state WHERE plan = '$1' AND state = 'committed'") && [ -n "$held" ] &&
    [ "$(sql "SELECT o.state || ' ' || count(a.id) || ' ' || count(r.attempt) FROM operation o
      LEFT JOIN attempt a ON a.operation = o.id LEFT JOIN attempt_response r ON r.attempt = a.id
      WHERE o.id = '$held' GROUP BY o.state")" = 'sending 1 0' ] &&
    [ "$(stalls_at dispatch-attempted)" -gt "$2" ]
}

# before: the bundle captured before the row; taker: the owner token that took the row's operation
# over; ds_failed: the failure count at the table's last line.
before='' taker='' ds_failed=0
# ds_row <row> <source> <outcome>: the table's line for the row, PASS when no check failed since the
# last line.
ds_row() {
  local r=PASS
  [ "$failed" = "$ds_failed" ] || r=FAIL
  ds_failed=$failed
  printf '%s\t%s\t%s\t%s\n' "$1" "$r" "$2" "$3" >>"$EV/ds-rows.tsv"
}
# changes_worker: the worker's configuration digest in rel is not its Applied one.
changes_worker() {
  [ "$(sql "SELECT rm.configuration_digest <> s.applied_digest FROM release_machine rm
    JOIN machine_state s ON s.machine = rm.machine WHERE rm.release = '$rel' AND rm.machine = '$machine_w'")" = t ]
}
# ds_publish <row>: h-author drafts a role fragment, a node label of the row's own, and lists it in
# the workers profile; h-publisher publishes it: a release changing the worker's configuration, its
# Desired, in rel. Each step is a prerequisite.
ds_publish() {
  local ref="$ds_ref $1"
  must "$ref" 'h-author opens a draft: 201' draft_by h-author "ds$1"
  must "$ref" "h-author drafts a role fragment, a node label of the row's own: 200" fragment_by h-author "ds$1" "ds$1" role
  must "$ref" 'h-author lists it in the workers profile: 200' profile_by h-author "ds$1"
  must "$ref" "h-publisher publishes it: 202, one release, the worker's Desired" publish_by h-publisher "ds$1"
  rel=$new_release
  must "$ref" "the release changes the worker's configuration digest" changes_worker
}
# ds_plan <row> [extra JSON]: the worker's baseline revision and apid log, the last operation and the
# last bundle, read; then a plan of the worker's Desired as h-publisher, its id in plan.
ds_plan() {
  baseline=$(sql "SELECT baseline_revision FROM machine_state WHERE machine = '$machine_w'") || die "$scenario: cannot read the worker's state"
  ops0=$(sql "SELECT coalesce(max(seq), 0) FROM operation") || die "$scenario: cannot read the operations"
  w_log=$(apid_log "$TALOS_WORKER_IP") || die "cannot read the worker's apid log"
  before=$bundle
  plan_id=''
  must "$ds_ref $1" 'a plan of the release as h-publisher: 201' plan_by h-publisher "ds$1" "${2:-}"
  plan=$plan_id
}
# ds_completed <attempts> <accepted>: the plan's operation completed, its id in op, with that many
# attempts, that many of them accepted; Applied the release from the operation, the baseline
# revision one more.
ds_completed() {
  completed || return 1
  [ "$(sql "SELECT count(*) || ' ' || count(*) FILTER (WHERE r.class = 'accepted') FROM attempt a
    LEFT JOIN attempt_response r ON r.attempt = a.id WHERE a.operation = '$op'")" = "$1 $2" ] &&
    [ "$(sql "SELECT applied_release || ' ' || applied_source || ' ' || baseline_revision FROM machine_state
      WHERE machine = '$machine_w'")" = "$rel operation $((baseline + 1))" ]
}
# ds_landed: in the latest bundle the worker's digest is the release's artifact, and its resource
# version is not the one in the bundle before the row.
ds_landed() {
  local want
  want=$(sql "SELECT encode(configuration_digest, 'hex') FROM release_machine WHERE release = '$rel' AND machine = '$machine_w'")
  [ -n "$want" ] && [ "$(digest_of "$bundle/talos-config-sha256.txt" "$TALOS_WORKER_IP")" = "$want" ] &&
    differ "$(digest_of "$bundle/talos-config-version.txt" "$TALOS_WORKER_IP")" \
      "$(digest_of "$before/talos-config-version.txt" "$TALOS_WORKER_IP")"
}
# none_sent: no mutating request in the worker's apid log since the row began.
none_sent() {
  local w
  w=$(apid_log "$TALOS_WORKER_IP") && [[ "$w" == "$w_log"* ]] && [ "$(run_mutations "$w_log" "$w")" = 0 ]
}
# ds_done <row> <attempts> [accepted, 1]: the row's operation completed with that many attempts and
# accepted responses, one ApplyConfiguration on the worker since the row began, and its bundle:
# clean, the worker's digest the artifact's and its resource version moved.
ds_done() {
  local ref="$ds_ref $1" w_after
  check "$ref" "the plan's operation completed within four minutes with $2 attempt(s), ${3:-1} accepted; Applied the release from the operation, the baseline revision one more" \
    within 240 ds_completed "$2" "${3:-1}"
  w_after=$(apid_log "$TALOS_WORKER_IP") || die "cannot read the worker's apid log"
  check "$ref" "the worker's apid log records exactly one mutating request since the row began, ApplyConfiguration" \
    one_apply "$w_log" "$w_after"
  outcome_bundle "$ref" "ds-$1"
  check "$ref" "the worker's digest is the release's artifact and its resource version moved" ds_landed
}
# revoke_approval <row>: h-approver revokes approval_id: 201.
revoke_approval() {
  fresh h-approver
  answers 201 - POST "/api/v1/approvals/$approval_id/revocations" "$tok" "{\"reason\": \"$mutation_ref\"}" "$(key "revoke-$1")"
}
# revoked_in_flight: the approval's revocation entry lies between the attempt and its response.
revoked_in_flight() {
  [ "$(sql "SELECT count(*) FROM machine_event v, attempt a JOIN attempt_response r ON r.attempt = a.id
    WHERE a.operation = '$op' AND v.machine = '$machine_w' AND v.kind = 'approval-revocation'
      AND v.entry->>'approval' = '$approval_id' AND v.revision > a.revision AND v.revision < r.revision")" = 1 ]
}
# take_over <operation> <row>: h-recovery asks the instance on call_port to take the operation over
# at generation 1: 201, unresolved at generation 2; the new owner token in taker.
take_over() {
  fresh h-recovery
  answers 201 - POST "/api/v1/operations/$1/takeovers" "$tok" "{\"generation\": 1, \"reason\": \"$ds_ref $2\"}" \
    "$(key "takeover-$2")" || return 1
  jq -e '.state == "unresolved" and .generation == 2' "$EV/last.body" >/dev/null || return 1
  taker=$(sql "SELECT owner FROM operation WHERE id = '$1'")
  [ -n "$taker" ]
}
# owner_refused: one refusal of held by comparison 7, the owner token not the refusing controller's,
# by another controller than taker, and no attempt.
owner_refused() {
  [ "$(sql "SELECT count(*) FROM machine_event WHERE machine = '$machine_w' AND kind = 'refusal'
    AND entry->>'operation' = '$held' AND entry->>'comparison' = '7' AND entry->>'controller' <> '$taker'
    AND entry->>'cause' = 'the operation''s owner token is not this controller''s'")" = 1 ] &&
    [ "$(sql "SELECT count(*) FROM attempt WHERE operation = '$held'")" = 0 ]
}
# killed_committed: the plan committed, its operation, in held, committed with no attempt.
killed_committed() {
  held=$(sql "SELECT operation FROM plan_state WHERE plan = '$plan' AND state = 'committed'") && [ -n "$held" ] && still_held
}
# started_takeover: one ownership transition of op, a controller's start, to generation 2; its new
# owner token in taker.
started_takeover() {
  taker=$(sql "SELECT entry->>'to' FROM machine_event WHERE machine = '$machine_w' AND kind = 'ownership-transition'
    AND entry->>'operation' = '$op' AND entry->>'reason' = 'controller-start' AND entry->>'generation' = '2'")
  one_line "$taker" && [ "$(sql "SELECT count(*) FROM machine_event WHERE machine = '$machine_w'
    AND kind = 'ownership-transition' AND entry->>'operation' = '$op'")" = 1 ]
}
# retried_by_taker: op's one attempt, number 1, is taker's at generation 2 and names as its
# classification one safe to retry by the same token, resting on the same observation.
retried_by_taker() {
  [ "$(sql "SELECT count(*) || ' ' || bool_and(a.number = 1 AND a.owner = '$taker' AND a.owner_gen = 2
      AND c.outcome = 'safe-to-retry' AND c.owner = '$taker' AND c.owner_gen = 2 AND a.observation = c.observation)
    FROM attempt a JOIN operation_classification c ON c.machine = a.machine AND c.revision = a.classification
      AND c.operation = a.operation
    WHERE a.operation = '$op'")" = '1 true' ]
}
# The transport and kill rows: attempts no response accounts for, settled by a recovery-admin's
# accounting decision (ER §5.2), and faults at the response and the completion. start and end: the
# decision's observations. The plans bind a 30 s transport deadline.
start='' end='' fast='{"transportDeadlineSeconds": 30}'
# op_unaccounted: the plan's operation, its id in op, unresolved, with one attempt and no accounting.
op_unaccounted() {
  op=$(sql "SELECT operation FROM plan_state WHERE plan = '$plan' AND state = 'committed'") && [ -n "$op" ] &&
    [ "$(sql "SELECT o.state || ' ' || count(a.id) || ' ' || count(c.attempt) FROM operation o JOIN attempt a ON a.operation = o.id
      LEFT JOIN attempt_accounting c ON c.attempt = a.id WHERE o.id = '$op' GROUP BY o.state")" = 'unresolved 1 0' ]
}
# first_read: in start, the first successful read of the worker (one that read a node value) begun
# after the transport deadline of op's attempt: where the decision's settle starts.
first_read() {
  start=$(sql "SELECT o.id FROM observation o JOIN machine_event se ON se.machine = o.machine AND se.revision = o.basis
    JOIN attempt a ON a.operation = '$op' AND a.number = 1 WHERE o.machine = '$machine_w' AND se.at > a.transport_deadline
      AND (o.unread->'identity' IS NULL OR o.unread->'runningVersion' IS NULL OR o.unread->'configuration' IS NULL)
    ORDER BY o.basis LIMIT 1")
  [ -n "$start" ]
}
# recovery_read [SQL instant]: in end, the first recovery observation of op that read the worker's
# resource version, begun after start was recorded and not before the instant.
recovery_read() {
  local from=${1:-"'-infinity'"}
  end=$(sql "SELECT o.id FROM observation o JOIN observation_start s ON s.machine = o.machine AND s.revision = o.basis
    JOIN machine_event se ON se.machine = o.machine AND se.revision = o.basis
    WHERE s.operation = '$op' AND s.purpose = 'recovery' AND o.resource_version IS NOT NULL
      AND se.at > (SELECT at FROM observation WHERE id = '$start') AND se.at >= $from ORDER BY o.basis LIMIT 1")
  [ -n "$end" ]
}
# floor_read: recovery_read from the fixture's settle floor (30 s, server.yaml.in) after the latest
# of the transport deadline, op's last takeover and start.
floor_read() {
  recovery_read "(SELECT greatest(a.transport_deadline, s.at, (SELECT max(at) FROM machine_event WHERE machine = '$machine_w'
    AND kind = 'ownership-transition' AND entry->>'operation' = '$op')) + interval '30 seconds'
    FROM attempt a, observation s WHERE a.operation = '$op' AND a.number = 1 AND s.id = '$start')"
}
# account <name> <status> <problem code or -> <jq filter> [identity, h-recovery]: the identity asks
# the instance on call_port to account for op's first attempt by decision on start and end.
account() {
  local attempt
  attempt=$(sql "SELECT id FROM attempt WHERE operation = '$op' AND number = 1") && [ -n "$attempt" ] || return 1
  fresh "${5:-h-recovery}"
  answers_with "$4" "$2" "$3" POST "/api/v1/operations/$op/attempts/$attempt/accountings" "$tok" \
    "$(jq -nc --arg s "$start" --arg e "$end" --arg x "$mutation_ref: the node was cut off" '{start: $s, end: $e, facts: {explanation: $x}}')" \
    "$(key "accounting-$1")"
}
# accounted_by_decision: op's first attempt accounted by a decision on start and end.
accounted_by_decision() {
  [ "$(sql "SELECT count(*) FROM attempt_accounting c JOIN attempt a ON a.id = c.attempt WHERE a.operation = '$op'
    AND a.number = 1 AND c.basis = 'decision' AND c.start_observation = '$start' AND c.end_observation = '$end'")" = 1 ]
}
# stalled_at <step> <stalls before>: C stalled at the step once more.
stalled_at() { [ "$(stalls_at "$1")" -gt "$2" ]; }
# deadline_ahead <seconds>: held's attempt's transport deadline is at least that far away, so a
# request released now still meets the fault rather than an already expired deadline.
deadline_ahead() {
  [ "$(sql "SELECT count(*) FROM attempt WHERE operation = '$held' AND number = 1
    AND transport_deadline > clock_timestamp() + make_interval(secs => $1)")" = 1 ]
}
# retried_after_accounting: op's second attempt names a classification safe to retry by taker,
# recorded after the first attempt's accounting.
retried_after_accounting() {
  [ "$(sql "SELECT count(*) FROM attempt a JOIN attempt_accounting c ON c.attempt = (SELECT id FROM attempt
      WHERE operation = '$op' AND number = 1) JOIN operation_classification k ON k.machine = a.machine
      AND k.revision = a.classification AND k.operation = a.operation
    WHERE a.operation = '$op' AND a.number = 2 AND k.outcome = 'safe-to-retry' AND k.owner = '$taker'
      AND k.revision > c.revision")" = 1 ]
}
# plan_op: the plan's operation, its id in op.
plan_op() {
  op=$(sql "SELECT operation FROM plan_state WHERE plan = '$plan' AND state = 'committed'")
  [ -n "$op" ]
}
# op_attempts <states>: op's state, attempts and responses, as 'state attempts responses'.
op_attempts() {
  [ "$(sql "SELECT o.state || ' ' || count(a.id) || ' ' || count(r.attempt) FROM operation o JOIN attempt a ON a.operation = o.id
    LEFT JOIN attempt_response r ON r.attempt = a.id WHERE o.id = '$op' GROUP BY o.state")" = "$1" ]
}
# verifying_stalled <stalls before>: the plan's operation verifying, its one attempt answered, and C
# stalled once more after recording the response.
verifying_stalled() { plan_op && op_attempts 'verifying 1 1' && stalled_at dispatch-responded "$1"; }
# completion_observations <unread predicate>: how many completion observations of op satisfy it.
completion_observations() {
  sql "SELECT count(*) FROM observation o JOIN observation_start s ON s.machine = o.machine AND s.revision = o.basis
    WHERE s.operation = '$op' AND s.purpose = 'completion' AND $1"
}
# completion_unread: a completion observation of op that read no configuration, and op verifying.
completion_unread() { [ "$(completion_observations "o.unread->'configuration' IS NOT NULL")" -ge 1 ] && op_attempts 'verifying 1 1'; }
# sending_unanswered: the plan's operation sending with one attempt and no response.
sending_unanswered() { plan_op && op_attempts 'sending 1 0'; }
# verifying_observed: the plan's operation verifying with its one attempt answered, a completion
# observation of it recorded, and no classification of it.
verifying_observed() {
  plan_op && op_attempts 'verifying 1 1' && [ "$(completion_observations true)" -ge 1 ] &&
    [ "$(sql "SELECT count(*) FROM operation_classification WHERE operation = '$op'")" = 0 ]
}
