# Recovery: stops, interrupted operations and restores

What to do when an apply is cut off part-way, and when the database, OpenBao or the credential
store is restored from a backup. It uses the [walkthrough's setup](walkthrough.md#setup-for-the-commands-below), plus one
more token:

```sh
recovery=$(mint h-recovery)
```

Every write here is `recovery-admin`, human-only, and needs an `Idempotency-Key` of 16 to 128
characters of `[A-Za-z0-9_-]`; a shorter key gets `400 invalid-request`. The reads accept any role
and need no key. The full contracts
are [execution and recovery](../spec/execution-recovery.md) and
[persistence and API §12](../spec/persistence-api.md).

## Before you need it

[Design §7.7](../design/Talos_Configuration_and_Machine_Management_Design.md) makes these yours, and
nothing in Bronzeward checks them:

- Back up three things: PostgreSQL, the OpenBao node, and the credential store holding provider
  tokens.
- Keep the OpenBao unseal share apart from every backup. Nothing starts without it.
- Before recording any identity revocation, add its subject to `auth.deniedSubjects`, and leave it
  there. A restore can erase the revocation; it does not rewind the list.
- Take the OpenBao backup no earlier than the database backup it pairs with, and keep the
  credential store's backup matched to them.
- While a database backup that names a Transit key version may still be restored, keep that
  version decryptable. Restrict delete, destroy and trim on keys and secrets.
- After restoring any of the three, start in recovery mode (below) before any normal start, and
  name each restored one in the entry.

Write down when each backup was taken. The recovery entry asks for it.

## Stopping a server

`serve` stops on `SIGINT` or `SIGTERM`. It gives in-flight requests 10 seconds, then exits; past
that it exits 1. It does not wait for a send to a machine to finish.

Stopping writes nothing about the operations it was running. Each keeps the state and owner it had.
The next instance to start takes them over and marks each `unresolved`, keeping its machine's scope
held. A stop never clears an operation; the steps below do.

## Interrupted operations

An apply-config operation moves `committed`, `sending`, `verifying`, then `completed`. When its
owner disappears, the instance that takes it over moves it to `unresolved`. `unresolved` is not an
end state. It leaves as `completed`, `failed` or `rejected` when the outcome is read from the node,
as `sending` when a retry is known to be safe, or as `cancelled` by your decision. Until then no
other operation on that machine can start.

On the fixture, `fixtures/scenarios/s6 run` cuts an operation off at each point. Instance C carries
the interruption seam, and B takes over. What each point leaves:

| Cut off | You see | Who settles it |
|---|---|---|
| Before commitment | No operation; the plan is still approved | The next executor commits it |
| After commitment, before an attempt | `unresolved`, no attempt (`attempts` is `null`) | Taken over and retried by the server |
| After an attempt was sent, no response | `unresolved`, an attempt with no response | You: account for the attempt |
| While verifying | `unresolved`, an attempt with a response | The server, from a new observation |

### Read it first

Lists hold at most 500 items per page, and the timeline is oldest first, so what you need is often
past the first page. `items` follows `next` and prints every item, one per line. It fails, rather
than stopping early, on any page that is not a list, such as a refusal:

```sh
items() {   # items <path> <token>
  local page next=
  while :; do
    page=$(call GET "$1?limit=500${next:+&cursor=$next}" "$2") || return
    jq -e '.items | arrays' <<<"$page" >/dev/null || { printf 'items: %s\n' "$page" >&2; return 1; }
    jq -c '.items[]' <<<"$page"
    next=$(jq -r '.next // empty' <<<"$page")
    [ -n "$next" ] || return 0
  done
}

op=<operation id>
call GET /operations/$op "$viewer" | jq '{state, attempts}'     # attempts is null before the first
items "/machines/$machine_w/timeline" "$viewer" | jq -c '{revision, at, kind, entry}'
```

The timeline says who committed, which attempts were made, and every ownership transition. See
[reading the timeline](troubleshooting.md#reading-the-timeline).

### Take it over

An instance that starts takes over what the previous owner left, with no call from you. To move an
operation to the instance you are talking to, take it over from its current generation: the one in
the operation's last `ownership-transition` timeline entry, or in its `commitment` entry (1) if it
was never taken over. An instance start or a recovery entry has usually moved it past 1 already.

```sh
gen=$(set -o pipefail; items "/machines/$machine_w/timeline" "$viewer" | jq -es --arg op "$op" '[.[] |
  select((.kind == "commitment" or .kind == "ownership-transition") and .entry.operation == $op) |
  .entry.generation] | last') &&
  call POST /operations/$op/takeovers "$recovery" "{\"generation\": $gen, \"reason\": \"the owner was stopped\"}" \
    recovery-takeover-1 | jq '{state, generation}'   # unresolved, generation $gen + 1
```

A stale generation, or an operation already `completed`, `failed`, `rejected` or `cancelled`, gets
`409 conflict`.

### Account for a sent attempt

When an attempt was sent and no response came back, nobody knows whether it landed. Bronzeward does
not guess. You decide, from two observations of the machine:

- `start`: the first observation, begun after the attempt's transport deadline, that read anything
  from the node. It must be that one, not a later one, and it must have a `resourceVersion`; if the
  first read lacks one, the server refuses the decision.
- `end`: a `recovery` observation, with a `resourceVersion`, begun after `start` was recorded (its
  `at`) and at least the settle floor (`execution.settleFloor`, 30 s by default) after the latest
  of the transport deadline, the last takeover, and `start`'s `at`. The server takes `recovery`
  observations itself, one per settle floor.

```sh
items "/machines/$machine_w/observations" "$viewer" |    # listed by id: sorted by start (basis) here
  jq -sc 'sort_by(.basis)[] | {id, basis, purpose, startedAt, at, resourceVersion, unread}'
call POST /operations/$op/attempts/$attempt/accountings "$recovery" \
  "$(jq -nc --arg s "$start" --arg e "$end" '{start: $s, end: $e, facts: {explanation: "the node was cut off"}}')" \
  recovery-accounting-1
```

If the resource version moved between the two, say why in `facts.explanation`. The server refuses
a pair that does not meet these rules with `409 conflict` and names the rule. Once accounted, the
server retries or settles the operation. The settle floor is a floor, not a bound: a measured
late landing came 26 to 28 s after its send, and no upper bound was adopted
([late-landing report](../design/research/20261016-late-landing.md)). A request that lands later
still is found as drift only if it leaves the node's configuration different from Applied; one that
reapplies what Applied already records leaves no trace.

### Cancel it

When the outcome cannot be read and no attempt was made, cancel the operation:

```sh
call POST /operations/$op/resolutions "$recovery" '{"to": "cancelled", "reason": "never sent"}' recovery-resolution-1 | jq .state
```

Only `cancelled` is accepted, only from `unresolved`, and only when the operation has no attempt.

## Restoring from a backup

A restore takes the installation back to the backup's moment. Operations that were in flight then
look in flight again, and identities revoked since then look valid. Recovery mode stops anything
from acting on that until you have checked each machine. `fixtures/scenarios/s7 run` is the
reference run.

### 1. Stop every instance, then restore

Stop every `serve` process and note the time. Restore what was lost from matched backups: PostgreSQL,
OpenBao, the credential store, or several of them.
On the fixture, with backups taken earlier by `inject db-snapshot <name>` and
`inject bao-snapshot <name>`:

```sh
fixtures/bin/inject kill B                       # and every other running instance
stopped_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
fixtures/bin/inject db-restore <name>
fixtures/bin/inject bao-restore <name>
```

Before anything starts, put every subject revoked since the backup in `auth.deniedSubjects`. The
restore erased those revocations; the list refuses them whatever the database says.

### 2. Start one instance in recovery mode

```sh
bin/bronzeward serve -config bronzeward.yaml -recovery-start     # fixture: fixtures/bin/inject start B-recovery
```

Until the entry is recorded it answers everything except `/livez` and the entry with
`409 recovery-mode-active`; a `204` from `/livez` does not mean the entry is recorded. It runs no executor, publisher, adopter or drift loop, and it never
accepts a plan, an approval or an unfreeze, even after the entry.

### 3. Record the entry

Name each restored backup, when it was taken, and when you stopped the servers. A backup is your
identifier; the scripts use the file name and its SHA-256.

```sh
A=http://127.0.0.1:$SERVER_B_PORT     # from here on, every call goes to the recovery instance
call POST /recovery/entries "$recovery" "$(jq -nc --arg s "$stopped_at" '{
  restored: [{family: "database", backup: "pg-t.dump sha256:...", takenAt: "2026-10-18T08:00:00Z"},
             {family: "provider", backup: "bao-t.snap sha256:...", takenAt: "2026-10-18T08:00:05Z"}],
  stoppedAt: $s, reason: "database and OpenBao restored"}')" recovery-entry-1 |
  jq '{previousEpoch, epoch, machines, operations}'
```

The families are `database`, `provider` and `credential-store`. The entry starts a new epoch. A
process started before it may issue no more ownership: its commitments, takeovers, job claims,
ingestions, marks and recovery acts get `503 epoch-superseded`, though it may still answer reads.
Restart every such process. The entry also:

- takes over every apply-config operation in flight, which becomes `unresolved`;
- fails every queued or running publication job (`recovery-mode-entered`) and every running
  ingestion (`ingestion-abandoned`), abandoning every live import claim. These do not resume:
  submit them again once the scope allows;
- moves every machine's scope to `pre-restore-unaccounted`.

Then, through the recovery instance:

- Re-record every revocation the restore erased (`POST /identity-revocations`). Keep every subject
  in `auth.deniedSubjects`, including the ones you just added: the list survives a later restore,
  which could erase the revocation again.
- Reissue every automation token with `bronzeward token rotate`. A token issued before the entry
  is from an earlier epoch and gets `401`.

### 4. Restart normally, still in recovery mode

Once the entry is recorded, stop the recovery instance and start the instances without the flag,
as `fixtures/scenarios/s7` does before the last release. The installation stays in recovery mode
until the exit (step 6): no plan commits on a scope that is not yet released. The recovery instance
runs no executor or job worker, so the normal restart is what settles a restored operation that
has an attempt and runs any publication you queued. Without it, such an operation stays
`unresolved`, its scope can never be released, and the exit is refused.

```sh
bin/bronzeward serve -config bronzeward.yaml     # fixture: fixtures/bin/inject kill B-recovery, then inject start B
```

### 5. Account, mark and release each machine

Each machine goes `pre-restore-unaccounted`, then `unresolved` or `blocked`, then `ready`, then
`released`. `GET /recovery` shows every scope's state.

1. **Account.** With the same two-observation rule as above, the first read after the stop plus the
   maximum transport deadline, and a recovery read a settle floor later. One decision can cover
   several machines.

   ```sh
   call POST /recovery/accountings "$recovery" "$(jq -nc --arg m "$machine_w" --arg s "$start" --arg e "$end" \
     '{scopes: [{machine: $m, start: $s, end: $e}], facts: {}}')" recovery-accounting-scopes | jq .scopes
   ```

   A scope a restored operation still holds is `unresolved`; otherwise `blocked` until marked. A
   restored operation with no attempt is then cancelled by the server, sending nothing. One with an
   attempt is classified by the normal instance's executor from an observation after the
   accounting: `completed`, `failed`, or `unresolved` until you resolve it. It is never retried.
2. **Check dependencies.** A mark checks only the machine, its Desired artifact and Talos access;
   the rest is yours ([execution and recovery §7.3](../spec/execution-recovery.md#73-procedure) step 3). Check
   that every provider reference releases need is still retained, that the backups were paired
   (OpenBao no earlier than the database), and that the decryption and credentials each scope needs
   work under the recovery identities, which must not be able to create a Transit key. Stop and
   name what is missing. After the normal restart, list what the monitor does not hold retained:

   ```sh
   items /dependencies "$viewer" | jq -c 'select(.class != "retained" or .stale) | {id, provider, object, class, reason, stale}'
   ```

3. **Mark.** The server observes the machine. `ready` with `missing: []` means it can be
   released. `blocked` names what is missing, such as an observation of an unreachable node.

   ```sh
   call POST /recovery/scopes/$machine_w/marks "$recovery" '{"facts": {}}' recovery-mark-worker | jq '{state, missing}'
   ```

   If the node's configuration digest is not what the restored Applied explains, the mark opens a
   drift record, even when it matches a restored release's artifact. Handle it once the scope is
   released, as in [S5](walkthrough.md#s5-drift-adopted-or-reverted).
4. **Release.** Only a `ready` scope whose dependencies you checked; anything else is `409 conflict`.

   ```sh
   call POST /recovery/scopes/$machine_w/releases "$recovery" '{"facts": {}}' recovery-release-worker | jq .state
   ```

### 6. Exit recovery mode

```sh
call POST /recovery/exits "$recovery" '{"facts": {}}' recovery-exit-first | jq .scopes
```

The exit is refused with `409 conflict` while any scope is unreleased. Restored operations are never
retried. An approval recorded before the entry is refused at commitment (`the approval is of an
earlier epoch`): plan and approve again.
