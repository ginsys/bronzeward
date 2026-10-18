# S8 step 2's answers for one operation, from what h-viewer was served: the operation ($op, GET
# /operations/{id}), its events ($ev), its machine's timeline ($tl) and observations ($obs, GET
# /machines/{id}/observations), each a one-element array from --slurpfile. Every answer is reached
# by a link: the operation names its plan and machine, the timeline's entries name the operation,
# the plan or an observation, an observation names the start it rests on. scenarios/s8 checks the
# answers; this program only reads.
# instant: a served time as Unix seconds and microseconds, whatever its offset and precision; a
# time it cannot parse is an error, so no attempt drops out of a comparison.
def instant:
  if . == null then null
  elif (type != "string") or (test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\\.[0-9]+)?(Z|[+-][0-9]{2}:[0-9]{2})$") | not) then
    error("unparsable time: \(tojson)")
  else
    capture("^(?<d>[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2})(?<f>\\.[0-9]+)?(?<z>Z|[+-][0-9]{2}:[0-9]{2})$") as $c |
    (if $c.z == "Z" then 0 else ($c.z[1:3] | tonumber) * 3600 + ($c.z[4:6] | tonumber) * 60 end) as $off |
    "\(($c.d + "Z" | fromdateiso8601) - (if $c.z[0:1] == "+" then $off else -$off end)).\((($c.f // ".")[1:] + "000000")[0:6])"
  end;
($op[0]) as $o | ($ev[0]) as $e | ($tl[0]) as $t | ($obs[0]) as $os |
$o.subject.plan as $plan |
[$t[] | select(.entry.operation == $o.id)] as $mine |
($mine | map(.revision)) as $revs |
# The starts taken for it, the plan's evidence included, and the observations its entries name.
[$t[] | select(.kind == "observation-started" and (.entry.operation == $o.id or .entry.plan == $plan)) | .revision] as $starts |
(if $revs == [] then [] else [$t[] | select(.kind == "scope-state" and .revision > $revs[0] and .revision < $revs[-1])] end) as $scoped |
([$t[] | select(.entry.plan == $plan and .kind == "commitment")] + $mine + $scoped |
  [.[] | .entry | (.observation, .start, .end) | select(type == "string" and startswith("obs_"))] | unique) as $links |
[$os[] | select(((.basis as $b | $starts | index($b)) != null) or ((.id as $i | $links | index($i)) != null)) |
  {observation: .id, revision, purpose, basis, at, access, talosNodeId, assignmentEvidence, runningVersion,
   resourceVersion, health, unread}] as $observed |
{
  operation: $o.id,
  kind: $o.kind,
  plan: $plan,
  machine: $o.subject.machine,
  # Who approved what: every approval of the plan, and its revocations.
  approved: [$t[] | select(.kind == "approval" and .entry.plan == $plan) |
    {revision, epoch, approval: .entry.approval, by: .entry.principal, role: .entry.role,
     selfApproval: .entry.selfApproval, unseenOverwrite: .entry.unseenOverwrite}],
  revoked: [$t[] | select(.kind == "approval-revocation" and .entry.plan == $plan) |
    {revision, approval: .entry.approval, by: .entry.principal, role: .entry.role, reason: .entry.reason}],
  commitment: [$mine[] | select(.kind == "commitment") |
    {revision, approval: .entry.approval, observation: .entry.observation, controller: .entry.controller}],
  # What each attempt carried, when it was recorded, and whether its sending is known.
  attempts: [$mine[] | select(.kind == "attempt") | . as $a |
    {attempt: .entry.attempt, number: .entry.number, route: .entry.route, observation: .entry.observation,
     controller: .entry.controller, generation: .entry.generation,
     transportDeadline: .entry.transportDeadline, verificationDeadline: .entry.verificationDeadline,
     recordedAt: .at, revision,
     response: ([$mine[] | select(.kind == "response" and .entry.attempt == $a.entry.attempt) |
       {revision, class: .entry.class, code: .entry.code}] | first),
     accounting: ([$mine[] | select(.kind == "accounting" and .entry.attempt == $a.entry.attempt) |
       {revision, basis: .entry.basis, class: .entry.class, decidedBy: .entry.decidedBy, role: .entry.role,
        start: .entry.start, end: .entry.end}] | first)} |
    # Known only once accounted: a response that does not account (a transport failure) leaves it unknown.
    .sending = (if .accounting then "accounting:" + .accounting.basis else "unknown" end)],
  # What was observed for it: the observations resting on a start taken for it or its plan, and those
  # its entries name, each with what the read returned or which values it could not read and why.
  observed: $observed,
  # Every observation its entries name is served; each timeline observation entry is served, and
  # each observed one is its timeline entry (same id, purpose, basis and revision).
  observationsResolve: ($links - [$os[].id] == []),
  observedIsTimeline: ([$os[] | {id, purpose, basis, revision}] as $s |
    [$t[] | select(.kind == "observation") | {id: .entry.observation, purpose: .entry.purpose, basis: .entry.basis, revision}] as $x |
    all($x[]; . as $e | $s | index([$e]) != null) and
    all($observed[]; {id: .observation, purpose, basis, revision} as $e | $x | index([$e]) != null)),
  # Each observed value was read, or is named unread with its cause: identity (the node id),
  # running version, configuration (the resource version), assignment evidence, health.
  observedRead: all($observed[]; . as $v |
    all(["identity", "talosNodeId"], ["runningVersion", "runningVersion"], ["configuration", "resourceVersion"],
        ["assignmentEvidence", "assignmentEvidence"], ["health", "health"];
      $v[.[1]] != null or ((($v.unread // {})[.[0]].cause // "") != ""))),
  # Why the scope was held, and by whom: its ownership and state changes, and the scope states
  # recorded between its first and last entry.
  held: [$mine[] | select(.kind == "ownership-transition" or .kind == "operation-state") |
    {revision, kind, from: .entry.from, to: .entry.to, why: (.entry.cause // .entry.reason),
     by: (.entry.decidedBy // .entry.resolvedBy // .entry.requestedBy), role: .entry.role}],
  scope: (if $revs == [] then [] else [$t[] | select(.kind == "scope-state" and .revision > $revs[0] and .revision < $revs[-1]) |
    {revision, from: .entry.from, to: .entry.to, why: .entry.cause, decidedBy: .entry.decidedBy, role: .entry.role,
     act: .entry.act, start: .entry.start, end: .entry.end, facts: .entry.facts, attempts: .entry.attempts}] end),
  refusals: [$t[] | select(.kind == "refusal" and .entry.plan == $plan) |
    {revision, comparison: .entry.comparison, cause: .entry.cause, controller: .entry.controller}],
  # How it ended.
  classifications: [$mine[] | select(.kind == "classification") | {revision, outcome: .entry.outcome}],
  ended: ([$mine[] | select(.kind == "operation-state") | .entry.to] | last),
  state: $o.state,
  # The pass criteria's structure: commit order, links, and no response text served.
  ordered: (($t | map(.revision)) as $r | $r == ($r | sort) and ($r | unique | length) == ($r | length)),
  # Each event is its timeline entry: the same revision, type and data.
  eventsAreTimeline: (($e | map([(.id | split(":")[1] | tonumber), .type, .data])) ==
    ([$mine[] | select(.kind != "observation-started") | [.revision, .kind, .entry]])),
  # Each of the resource's attempts is its timeline entry, field by field where both carry it.
  attemptsLinked: ((($o.attempts // []) | map({id, number, controller, generation, route, observation, revision,
      transportDeadline: (.transportDeadline | instant), verificationDeadline: (.verificationDeadline | instant), at: (.at | instant)})) ==
    [$mine[] | select(.kind == "attempt") | {id: .entry.attempt, number: .entry.number, controller: .entry.controller,
      generation: .entry.generation, route: .entry.route, observation: .entry.observation, revision,
      transportDeadline: (.entry.transportDeadline | instant), verificationDeadline: (.entry.verificationDeadline | instant),
      at: (.at | instant)}]),
  commitmentApproved: ([$mine[] | select(.kind == "commitment") | .entry.approval] as $c |
    $c != [] and all($c[]; . as $x | [$t[] | select(.kind == "approval" and .entry.plan == $plan) | .entry.approval] | index($x) != null)),
  textServed: ([$o, $e, $t] | [.. | objects | select(has("text"))] | length > 0)
}
