# S8 step 2's answers for one operation, from what h-viewer was served: the operation ($op, GET
# /operations/{id}), its events ($ev) and its machine's timeline ($tl), each a one-element array
# from --slurpfile. Every answer is reached by a link: the operation names its plan and machine,
# the timeline's entries name the operation or the plan, an observation names the start it rests
# on. scenarios/s8 checks the answers; this program only reads.
($op[0]) as $o | ($ev[0]) as $e | ($tl[0]) as $t |
$o.subject.plan as $plan |
[$t[] | select(.entry.operation == $o.id)] as $mine |
($mine | map(.revision)) as $revs |
[$t[] | select(.kind == "observation-started" and .entry.operation == $o.id) | .revision] as $starts |
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
    .sending = (if .response then "response" elif .accounting then "accounting:" + .accounting.basis else "unknown" end)],
  # What was observed for it: the observations resting on a start that names it.
  observed: [$t[] | select(.kind == "observation" and (.entry.basis as $b | $starts | index($b)) != null) |
    {revision, observation: .entry.observation, purpose: .entry.purpose, basis: .entry.basis}],
  # Why the scope was held, and by whom: its ownership and state changes, and the scope states
  # recorded between its first and last entry.
  held: [$mine[] | select(.kind == "ownership-transition" or .kind == "operation-state") |
    {revision, kind, from: .entry.from, to: .entry.to, why: (.entry.cause // .entry.reason),
     by: (.entry.decidedBy // .entry.resolvedBy // .entry.requestedBy), role: .entry.role}],
  scope: (if $revs == [] then [] else [$t[] | select(.kind == "scope-state" and .revision > $revs[0] and .revision < $revs[-1]) |
    {revision, from: .entry.from, to: .entry.to, why: .entry.cause}] end),
  refusals: [$t[] | select(.kind == "refusal" and .entry.plan == $plan) |
    {revision, comparison: .entry.comparison, cause: .entry.cause, controller: .entry.controller}],
  # How it ended.
  classifications: [$mine[] | select(.kind == "classification") | {revision, outcome: .entry.outcome}],
  ended: ([$mine[] | select(.kind == "operation-state") | .entry.to] | last),
  state: $o.state,
  # The pass criteria's structure: commit order, links, and no response text served.
  ordered: (($t | map(.revision)) as $r | $r == ($r | sort) and ($r | unique | length) == ($r | length)),
  eventsAreTimeline: (($e | map(.id | split(":")[1] | tonumber)) ==
    ([$mine[] | select(.kind != "observation-started") | .revision])),
  attemptsLinked: ((($o.attempts // []) | map(.id)) == [$mine[] | select(.kind == "attempt") | .entry.attempt]),
  commitmentApproved: ([$mine[] | select(.kind == "commitment") | .entry.approval] as $c |
    $c != [] and all($c[]; . as $x | [$t[] | select(.kind == "approval" and .entry.plan == $plan) | .entry.approval] | index($x) != null)),
  textServed: ([$o, $e, $t] | [.. | objects | select(has("text"))] | length > 0)
}
