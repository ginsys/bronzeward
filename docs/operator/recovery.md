# Recovery: stops, interrupted operations and restores

What to do when an apply is cut off part-way, and when the database or OpenBao is restored from a
backup. It uses the [walkthrough's setup](walkthrough.md#setup-for-the-commands-below), plus one
more token:

```sh
recovery=$(mint h-recovery)
```

Every route here is `recovery-admin`, human-only, and needs an `Idempotency-Key`. The full contracts
are [execution and recovery](../spec/execution-recovery.md) and
[persistence and API §12](../spec/persistence-api.md).

## Before you need it

[Design §7.7](../design/Talos_Configuration_and_Machine_Management_Design.md) makes these yours, and
nothing in Bronzeward checks them:

- Back up three things: PostgreSQL, the OpenBao node, and the credential store holding provider
  tokens.
- Keep the OpenBao unseal share apart from every backup. Nothing starts without it.
- Take the OpenBao backup no earlier than the database backup it pairs with, and keep the
  credential store's backup matched to them.
- While a database backup that names a Transit key version may still be restored, keep that
  version decryptable. Restrict delete, destroy and trim on keys and secrets.
- After restoring either service, start in recovery mode (below) before any normal start.

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
| After commitment, before an attempt | `unresolved`, no attempt | Taken over and retried by the server |
| After an attempt was sent, no response | `unresolved`, an attempt with no response | You: account for the attempt |
| While verifying | `unresolved`, an attempt with a response | The server, from a new observation |

### Read it first

```sh
op=<operation id>
call GET /operations/$op "$viewer" | jq '{state, attempts}'
call GET "/machines/$machine_w/timeline?limit=500" "$viewer" | jq '.items[] | {revision, at, kind, entry}'
```

The timeline says who committed, which attempts were made, and every ownership transition. See
[reading the timeline](troubleshooting.md#reading-the-timeline).

### Take it over

An instance that starts takes over what the previous owner left, with no call from you. To move an
operation to the instance you are talking to, take it over from its current generation. The
generation is in the operation's last `ownership-transition` timeline entry.

```sh
call POST /operations/$op/takeovers "$recovery" '{"generation": 1, "reason": "the owner was stopped"}' takeover-1 |
  jq '{state, generation}'                       # unresolved, generation 2
```

A stale generation, or an operation already `completed`, `failed`, `rejected` or `cancelled`, gets
`409 conflict`.

### Account for a sent attempt

When an attempt was sent and no response came back, nobody knows whether it landed. Bronzeward does
not guess. You decide, from two observations of the machine:

- `start`: the first observation, begun after the attempt's transport deadline, that read the node
  and has a `resourceVersion`.
- `end`: a `recovery` observation, with a `resourceVersion`, begun after `start` and at least the
  settle floor (`execution.settleFloor`, 30 s by default) later.

```sh
call GET "/machines/$machine_w/observations?limit=500" "$viewer" |
  jq '.items[] | {id, purpose, startedAt, resourceVersion, unread}'
call POST /operations/$op/attempts/$attempt/accountings "$recovery" \
  "$(jq -nc --arg s "$start" --arg e "$end" '{start: $s, end: $e, facts: {explanation: "the node was cut off"}}')" \
  accounting-1
```

If the resource version moved between the two, say why in `facts.explanation`. The server refuses
a pair that does not meet these rules with `409 conflict` and names the rule. Once accounted, the
server retries or settles the operation. The settle floor is a floor, not a bound: a measured
late landing came 26 to 28 s after its send, and no upper bound was adopted
([late-landing report](../design/research/20261016-late-landing.md)). A request that lands later
still is found as drift.

### Cancel it

When the outcome cannot be read and no attempt was made, cancel the operation:

```sh
call POST /operations/$op/resolutions "$recovery" '{"to": "cancelled", "reason": "never sent"}' resolution-1 | jq .state
```

Only `cancelled` is accepted, only from `unresolved`, and only when the operation has no attempt.

## Restoring the database or OpenBao

A restore takes the installation back to the backup's moment. Operations that were in flight then
look in flight again, and identities revoked since then look valid. Recovery mode stops anything
from acting on that until you have checked each machine. `fixtures/scenarios/s7 run` is the
reference run.

### 1. Stop every instance, then restore

Stop every `serve` process and note the time. Restore PostgreSQL and OpenBao from matched backups.
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

Until the entry is recorded it answers everything except the entry with
`409 recovery-mode-active`. It runs no executor, publisher, adopter or drift loop, and it never
accepts a plan, an approval or an unfreeze, even after the entry.

### 3. Record the entry

Name each restored backup, when it was taken, and when you stopped the servers. A backup is your
identifier; the scripts use the file name and its SHA-256.

```sh
A=http://127.0.0.1:$SERVER_B_PORT     # from here on, every call goes to the recovery instance
call POST /recovery/entries "$recovery" "$(jq -nc --arg s "$stopped_at" '{
  restored: [{family: "database", backup: "pg-t.dump sha256:...", takenAt: "2026-10-18T08:00:00Z"},
             {family: "provider", backup: "bao-t.snap sha256:...", takenAt: "2026-10-18T08:00:05Z"}],
  stoppedAt: $s, reason: "database and OpenBao restored"}')" entry-1 |
  jq '{previousEpoch, epoch, machines, operations}'
```

The families are `database`, `provider` and `credential-store`. The entry starts a new epoch: every
process started before it is refused from then on (`503 epoch-superseded`). Every operation that
was in flight is taken over and becomes `unresolved`. Every machine's scope becomes
`pre-restore-unaccounted`. Paused import claims are abandoned.

Then, through the recovery instance:

- Re-record every revocation the restore erased (`POST /identity-revocations`), then empty
  `auth.deniedSubjects` again.
- Reissue every automation token with `bronzeward token rotate`. A token issued before the entry
  is from an earlier epoch and gets `401`.

### 4. Account, mark and release each machine

Each machine goes `pre-restore-unaccounted`, then `unresolved` or `blocked`, then `ready`, then
`released`. `GET /recovery` shows every scope's state.

1. **Account.** With the same two-observation rule as above, the first read after the stop plus the
   maximum transport deadline, and a recovery read a settle floor later. One decision can cover
   several machines.

   ```sh
   call POST /recovery/accountings "$recovery" "$(jq -nc --arg m "$machine_w" --arg s "$start" --arg e "$end" \
     '{scopes: [{machine: $m, start: $s, end: $e}], facts: {}}')" accounting-scopes | jq .scopes
   ```

   A scope a restored operation still holds is `unresolved`; otherwise `blocked` until marked. A
   restored operation with no attempt is then cancelled by the server. One with an attempt is
   settled as in [interrupted operations](#interrupted-operations).
2. **Mark.** The server observes the machine. `ready` with `missing: []` means it can be
   released. `blocked` names what is missing, such as an observation of an unreachable node.

   ```sh
   call POST /recovery/scopes/$machine_w/marks "$recovery" '{"facts": {}}' mark-worker | jq '{state, missing}'
   ```

   If the node holds a configuration the restored Desired does not, the mark opens a drift record.
   Handle it after exit as in [S5](walkthrough.md#s5-drift-adopted-or-reverted).
3. **Release.** Only a `ready` scope; anything else is `409 conflict`.

   ```sh
   call POST /recovery/scopes/$machine_w/releases "$recovery" '{"facts": {}}' release-worker | jq .state
   ```

### 5. Exit, then start normally

```sh
call POST /recovery/exits "$recovery" '{"facts": {}}' exit-1 | jq .scopes
```

The exit is refused with `409 conflict` while any scope is unreleased. After it, stop the recovery
instance and start the others without the flag. Restored operations are never retried. An
approval recorded before the entry is refused at commitment (`the approval is of an earlier
epoch`): plan and approve again.
