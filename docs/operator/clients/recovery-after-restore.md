# Journey: recover after a restore

**You want** to bring an installation back into use after the database, the provider store (OpenBao)
or the credential store was restored from a backup. A restore takes the installation back to the
backup's moment. Operations that were in flight look in flight again, and a request sent before the
restore may still land on a machine. Bronzeward therefore holds each machine in a **scope** until a
person has accounted for it, checked it and released it. Words such as epoch, scope and operation
are in [the word list](terms.md).

**You need** the `recovery-admin` role, as a person signed in. An automation token is refused. Any
role can read the state. The server side of a restore (stopping the instances, restoring the
backups, `auth.deniedSubjects`, the restarts and the token rotation) is in the [server
recovery guide](../recovery.md). The clients do none of that.

The `text` blocks on this page show the shape of the output, read from the source. This page does
not claim that a run printed them.

**A release is not an approval.** Nothing on this page approves a plan or sends a configuration to
a machine. Each machine goes through the states `pre-restore-unaccounted`, then `unresolved` or
`blocked`, then `ready`, then `released`. When every scope is released, you exit recovery mode.

## Command line

Read where you stand, at any point:

```bash {.bw-run .bw-db}
bronzewardctl recovery status
```

```text
Epoch            <epoch>
Recovery mode    on
Start mode       <mode> (GET /server)
Entered          <time> by <person>: <reason>
Restore stopped  <time>
Restored         <backups>

MACHINE    SCOPE STATE              NEXT
<machine>  pre-restore-unaccounted  account the scope: recovery scope-account <machine>

Every scope is released: recovery exit leaves recovery mode.
```

The "Entered" lines appear after an entry, and "Exited" after an exit. The last line appears only
when every scope is released. `bronzewardctl status` shows the same in one line, "Recovery mode  on
since <time>. Scopes: <n>: <machine> <state>, ...", beside the server's start mode, any open drift
and any unresolved operation. Each scope state has one next step:

| Scope state | Next step |
|---|---|
| `pre-restore-unaccounted` | Account for the machine (step 2). |
| `unresolved` | A restored operation holds it. The normal restart classifies the operation; then mark the scope again. An operation with no attempt is resolved (step 3). |
| `blocked` | Mark the scope (step 4). The output says what is missing. |
| `ready` | Release it (step 5). |
| `released` | None. |

### 1. Enter recovery

Run this on the server that was started for recovery, once the restores are done. Name each
restored backup (`database`, `provider` or `credential-store`, one to three times) and the time the
restore stopped writes.

```bash {.bw-run .bw-db}
bronzewardctl recovery enter --stopped-at <RFC3339 time> --reason "<why>" \
  --restored family=<family>,backup=<backup id>,taken-at=<RFC3339 time>
```

The command first prints what entering does: a new epoch begins; every machine's scope becomes
`pre-restore-unaccounted`; every apply operation in flight is taken over as `unresolved`; running
import and publish jobs end; recovery mode lasts until `recovery exit`. Then it asks:

```text
Enter recovery in <context>? [y/N]
```

Only `y` or `yes` sends the entry. There is no `--yes`, and `--no-input` sends nothing. The flags
are in the [reference](cli-reference.md#recovery-enter). A server that was not started for recovery
refuses the entry.

### 2. Account for each machine

An attempt is recorded before its request is sent. After a restore the journal can lack an attempt,
and nothing shows whether a request landed. You decide, from two observations of the machine, that
no request sent before the restore can still land.

```bash {.bw-run .bw-db}
bronzewardctl recovery scope-account <machine> --start <observation> --end <observation>
```

List the observations first with `machine observations <machine>` (see [the
inventory](inventory.md)). The start is the first successful read of the machine after the latest of two
times: the time the restore stopped plus the maximum transport deadline, and the transport deadline
of each restored attempt that has no accounting. The end is a recovery observation begun at least
the settle floor after the latest of that time and the start. Both need a resource version. If the version moved between them, add `--explanation
"<why>"` (at most 1024 bytes).

The command shows the window, the settle floor and the maximum transport deadline as the server
serves them, both observations, and the risk, then asks `Record accounting for machine <machine>
(cluster <cluster>)? [y/N]`. The risk reads: "This records your judgment that no request sent
before the restore, from any attempt the restored journal shows or one it lost, can still land on
this machine. It may already have landed; nothing here shows whether it did. The settle floor is a
minimum wait, not a proven bound: a request landing after this can overwrite work admitted once the
scope is released, and would be seen only as drift at a later observation."

The answer is "Recorded. Scope <machine> is now blocked until it is marked." or, where a restored
operation still holds the scope, "unresolved: a restored operation still holds it". Run it again for
each machine. With no recovery entry the command stops: "shows no recovery entry on <where>;
nothing was sent. Enter recovery first: recovery enter".

### 3. Settle an unresolved operation

A scope held by an operation shows `unresolved`. Open the operation with `operation show
<operation>` (see [operation tracking](operation-tracking.md)). What you do depends on the
operation; the three commands are alternatives, not a sequence.

- **The operation has an attempt.** The accounting of step 2 already accounted for every
  unaccounted attempt of the machine, so you do nothing here. After the normal restart (see the
  [server recovery guide](../recovery.md)), the controller classifies the operation from an
  observation taken after the accounting. It is never retried. `recovery account` on an attempt
  that is already accounted for is refused: "the attempt is already accounted for".
- **The operation has no attempt.** End it:

  ```bash
  bronzewardctl recovery resolve <operation> --reason "<why>"
  ```

  This ends an unresolved operation as `cancelled`. It records no remote outcome. With an attempt
  recorded, the command refuses: "operation <operation> has an attempt: it is classified after the
  normal restart, or its attempts are accounted (recovery account); nothing was sent".
- **You want this server to own the operation.** Take it over:

  ```bash
  bronzewardctl recovery takeover <operation> --reason "<why>"
  ```

  This makes the serving controller the owner of a non-terminal operation. It reads the owner
  generation from the operation's events and attempts. It sends nothing to the node and classifies
  nothing. If the answer is lost, repeat it with the `--generation` the command printed.

One more command accounts for a single attempt whose response was lost, and it is not part of this
sequence: `recovery account <operation> <attempt> --start <observation> --end <observation>`. It
needs the attempt's executor to be stopped or taken over, and an attempt not yet accounted for. Its
window differs from step 2: the start is the first successful read after that attempt's transport
deadline, and the end is a recovery observation begun at least the settle floor after the latest of
the deadline, any takeover and the start. It changes no state: the operation stays as it was.

### 4. Mark the machine

```bash {.bw-run .bw-db}
bronzewardctl recovery mark <machine>
```

The server observes the machine and records the state the evidence supports: `ready`, `blocked` or
`unresolved`. The state is the server's, not yours. A `ready` scope shows no missing item. A
`blocked` scope lists what is missing:

| Missing | Meaning |
|---|---|
| `observation` | The restoration observation does not support the scope: it read no identity, does not follow the accounting, an operation changed after it, or it used another access version. |
| `identity` | The identity the node showed is not the one the machine record binds, or a later observation showed another. |
| `access` | The cluster's Talos access failed the dependency check. |
| `artifact` | The Desired release's artifact could not be decrypted for the machine; a lost key version leaves through a newly published release. |

Fix what is missing, then mark again. A mark checks only the machine, its Desired artifact and
Talos access. Whether the provider references are retained, whether the backups were paired, and
whether the credentials work are yours to check first (see the [server recovery guide](../recovery.md)).
A mark can open a drift record; decide on it after the release, in [the drift journey](drift.md).

### 5. Release the machine

```bash {.bw-run .bw-db}
bronzewardctl recovery release <machine>
```

Only a `ready` scope is released. The command says: "A release is not an approval: it approves no
plan and sends nothing." A scope that is not `ready`, or whose Desired release moved after the mark,
is refused. The command names `recovery mark` as the next step, except for a scope still
`pre-restore-unaccounted` (the accounting of step 2 comes first) or already `released` (none).

### 6. Exit recovery mode

```bash {.bw-run .bw-db}
bronzewardctl recovery exit
```

The exit is refused while any scope is not released, and the command names the first one. An
approval recorded before the entry authorizes nothing: plan and approve again (see [plans and
approval](plan-and-approval.md)).

## Web client

1. Open **Recovery** in the navigation. On a server started for recovery with no entry yet, the page
   shows **Enter recovery**: the restored backups (database, provider and credential store, each
   with the time it was taken), the time the restore stopped, a reason, and the list of what
   entering does. One press sends it. Nothing is typed back.
2. Once entered, the page shows the epoch, the mode and a table of scopes: Machine, Scope state,
   Next step and Decision. It reads again every 5 seconds. The banner reads "Recovery
   mode since <time>, epoch <epoch>", gives a summary of the scopes and ends "Drafts can be edited."
3. On a `pre-restore-unaccounted` row, the form **Record accounting** shows the machine, the settle
   floor and the maximum transport deadline, and the machine's observations twice: once to choose
   the start, once to choose the end. An observation that cannot be the start or the end is off,
   with its reason. Where the page can name the deadline, it marks one "the first read after the
   deadline". Add an explanation only if the resource version moved. **Read the observations
   again** keeps your choice while you wait for a later observation. The sentence above the button
   is the judgment that is recorded; one press records it.
4. On a `blocked`, `unresolved` or `ready` row, **Mark** records what the evidence supports. The
   answer stays in the row: the state, each missing item with its meaning, and the next step: "Next:
   release the scope.", "A restored operation still holds the scope. It is classified after the
   normal restart; then mark the scope again." or "Next: fix what is missing, then mark the scope
   again."
5. On a `ready` row, **Release** releases that scope only. No control acts on more than one machine.
6. For an unresolved operation, the operation's page links to **Open in Recovery**. That page has a
   reason field and one press for each of **Take over** ("Move the operation to this server
   instance, from the owner generation read from its events and attempts.") and **Resolve as
   cancelled** ("Cancel an operation that has no attempt. This action sends nothing to the node and
   records no remote outcome."). An **Account for an attempt** section lists each attempt with no
   accounting, with the form of web step 3.
7. **Exit recovery** stays off until every scope is released. The state `ready` is not released.
8. **Revoke identity** takes the identity, or the issuer and subject, and a reason; one press sends
   it. A revocation is permanent. The same act on the command line is `identity revoke` (see the
   [reference](cli-reference.md#identity-revoke)).

A control you may not use is shown off with the reason beside it. The server's answer stays the
authority: a refusal is shown in place with its next step.

![The Recovery page of the web client after the entry, signed in as a recovery administrator. The
banner reads "Recovery mode since 06:50, epoch" and an epoch name, and "1 of 2 scopes not
released: those cannot commit. Drafts can be edited." The navigation shows Recovery. The page
shows the entry that was recorded, with the two restored backups and their digests, and the table
of scopes: machine 10.55.0.3 "released" and machine 10.55.0.2 "pre-restore-unaccounted" with its
next step. The first lines of the form **Record accounting** show under the second row. Taken on
the participant fixture at revision 454b5609, with synthetic names and backup
files.](images/recovery-after-restore-1.png)

This screenshot shows the top of the page in steps 2 and 3. It does not show the rest of the form
**Record accounting**, **Mark**, **Release**, **Exit recovery** or **Revoke identity**, which lie
below it. The fixture's own setup had recorded the entry and brought one scope to "released" before
the capture; the capture only read the page, and nothing on it was filled in or pressed. Step 1, the
press of **Enter recovery**, is not shown.

## What can go wrong

| What you see | Why, and what to do |
|---|---|
| "shows no recovery entry on <where>; nothing was sent. Enter recovery first: recovery enter" | The server has no entry yet. Enter recovery first (step 1). |
| "Recovery mode: the scope of machine <machine> is <state>." | A restriction of one machine's scope. Follow that scope's next step in `recovery status`. |
| "Recovery mode: the server was started for recovery: it refuses plan creation, approval and unfreeze, and before its recovery entry every request but the entry. A restriction of the process, not of one scope: record the recovery entry first, then restart the server without the flag to end it." | The process, not a scope, refuses. If the entry is not recorded yet, record it (step 1). Then restart the server normally (see the [server recovery guide](../recovery.md)). |
| `recovery exit` refused, naming a scope | That scope is not released. Follow its next step in `recovery status`. |
| Mark refused on a scope that is `pre-restore-unaccounted` | Account for the machine first (step 2). |
| Release refused on a scope that is not `ready`, or whose Desired release moved | Mark the scope again (step 4). A scope still `pre-restore-unaccounted` takes the accounting of step 2 first. One already `released` needs nothing: read `recovery status`. |
| The accounting needs an explanation | The resource version moved between the two observations. Say why. |
| An end observation refused | The end must be a recovery observation with a resource version, begun after the start and at least the settle floor later. Read the observations again once a later one exists. |
| A takeover refused for a generation that is not current | Nothing changed. Read the operation with `operation show` and repeat. |
| "An attempt is recorded, and the request may have landed: an attempt is accounted for, not cancelled." (web) | Resolve is only for an operation with no attempt. If the scope accounting of step 2 covered the attempt, do nothing here: the controller classifies the operation after the normal restart. Only an attempt that is not yet accounted for takes `recovery account`. |
| "the epoch changed from <a> to <b> while the next steps were read; nothing is shown. Read it again with: recovery status" | A recovery entry was recorded while the command read. Read again. |

## What the result does and does not show

- An accounting records your judgment. It does not show that no request landed. A request that
  lands after it can overwrite work admitted once the scope is released, and would be seen only as
  drift at a later observation.
- A mark shows what the server observed on the machine at that moment. It does not check the
  provider references, the backup pairing or the credentials.
- A release lets work on that machine start again. It approves no plan and sends nothing to the node.
- A takeover moves an operation to this server and sends nothing to the node. Resolving records no
  remote outcome: after a restore no recorded attempt does not prove that nothing was ever sent.

Next: decide on a mark's drift in [the drift journey](drift.md). A change you still want goes to
[plans and approval](plan-and-approval.md). A client that cannot reach the server is in [client
troubleshooting](client-troubleshooting.md).
