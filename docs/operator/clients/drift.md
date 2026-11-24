# Journey: decide on a change made on a machine (drift)

**You want** to decide what to do about a machine that no longer reports what Bronzeward last
applied to it. Bronzeward calls that **drift**: a read of the machine differs from its **Applied**
release (the release Bronzeward last verified on it; see [the inventory](inventory.md)), and it
opens a **drift record**. You can **freeze** the machine while you decide, **adopt** the change into
a release, or **revert** the machine to its **Desired** release. Words such as epoch and approver
are in [the word list](terms.md).

**You need** any one role to read a drift record. Freezing needs `author`, `publisher`, `approver`
or `recovery-admin`. Unfreezing needs `approver`. Adopting needs an `author` who is a person, then a
`publisher`, then an `approver` to approve the plan. Reverting needs a `publisher` to make the plan
and an `approver` to approve it and to unfreeze the machine first if it is frozen.

The `text` blocks on this page show the shape of the output, read from the source. This page does
not claim that a run printed them.

**A drift record does not show what changed.** Bronzeward can show it only by importing the
machine's configuration for adoption. Nothing on the drift page, and no command here, sends
anything to the machine until a plan is approved and applied.

## Command line

Read the open drift of one machine. `<machine>` is its name or its ID; a name that two clusters hold
needs `--cluster`.

```bash
bronzewardctl drift show <machine>
```

```text
Drift opened by observation <observation> at <time>, last seen in observation <observation> at <time>. Late landing: no.
A configuration read differed from what Bronzeward last verified: Applied <release>.
Bronzeward cannot show what changed until it is imported for adoption.
Choose: freeze while you decide; adopt it into a release; or revert to Desired (<release>).
```

The API serves no opening time, so the times are those of the two observations the record names. An
observation that was not read shows "(its time was not read)". "Late landing" says whether the last
read may be the late result of an operation. A frozen machine adds "Frozen: no new commitment or
attempt until an approver unfreezes; an attempt already recorded is not stopped, and an adoption
can still be recorded."

With no open drift the command says "No open drift on machine <machine>." and why: only a read that
differs from Applied opens a record, so a read that matches it, or one that could not be read, opens
none. There is no command to ignore a drift record.

### Freeze while you decide

```bash
bronzewardctl machine freeze <machine> --reason "<why>"
```

```text
Frozen. No new commitment or attempt for this machine until an approver unfreezes it.
An attempt already recorded is not stopped, and an adoption can still be recorded.
Recorded by <person> (<role>): machine <machine>, timeline revision <n>, act <act>.
```

The reason is 1 to 1024 bytes. A freeze stops new commitments and attempts. It does not stop an
attempt that is already recorded, and an adoption can still be recorded. It stays after the drift is
resolved. Only an `approver` lifts it:

```bash
bronzewardctl machine unfreeze <machine> --reason "<why>"
```

Unfreezing approves and commits nothing.

### Adopt the change into a release

Adoption reads the machine's own configuration into a draft, so you can review it, then publishes the
draft and makes an adopt plan. `drift adopt` runs the steps in order for as long as your roles hold,
and prints the rest.

```bash
bronzewardctl drift adopt <machine> --draft <draft> --review
```

```text
Step 1/3  import (drift-adoption) of machine <machine> into draft <draft>
Step 2/3  publish draft <draft>: it publishes the whole draft
Step 3/3  plan an adoption of release <release>
This plan binds drift record <n>. drift adopt never approves: an approver runs bronzewardctl approve <plan>
Approval leads the controller to record the adoption without sending anything, even while the machine is frozen.
```

- Step 1 needs an `author` signed in as a person. With `--review` the import pauses so you can
  review and mark values (see [protected import](protected-import.md)); the later steps are then
  printed and not run.
- Step 2 needs a `publisher` and publishes the whole draft (see [publication](publication.md)).
- Step 3 needs a `publisher`. The command never approves: an `approver` runs `approve` (see [plans
  and approval](plan-and-approval.md)).

A missing role stops the command at that step with "Not run: step 1 needs an author signed in as a
person. The steps:" (or "step 2 needs a publisher") and each step as the command to run, with its
role. With `-o json` or `-o yaml` the command prints one object with the state of each step; its
exit codes are those of the step that stopped it (see the [reference](cli-reference.md#drift-adopt)).
An adopted base holds each marked value as a reference, so a fragment that overrides such a value
refuses publication: stop selecting that fragment.

### Revert the machine to Desired

```bash
bronzewardctl drift revert <machine>
```

```text
Plan <plan>  proposed  expires <time> (in <duration> by this client's clock)
  Operation  apply-config, mode no-reboot
  ... (the plan, as `plan show` prints it, with its evidence)
Revert of drift record <n> to Desired <release>. The plan expects the drifted digest before dispatch: if the machine drifts again first, it fails and must be made again.
Approving it approves an unseen overwrite: it replaces the drifted configuration, which no reviewer has seen.
The machine is frozen: the plan cannot commit until an approver approves it and, separately, unfreezes the machine: bronzewardctl machine unfreeze <machine> --reason <text>
```

The command makes an `apply-config` plan of the machine's Desired release, bound to the open drift
record. It needs a `publisher`. When an approver runs `approve`, the plan view has the line "Overwrites the
drifted configuration of drift record <n>, which no reviewer has seen", and approving records that
you approve overwriting it. The last line appears only while the machine is frozen.

## Web client

1. Open the machine. Under its status the page links to the drift page: "Decide on the change made
   on the machine" (while it is frozen: "Unfreeze the machine or decide on the change").
2. The page "Change on the machine" says what opened the change and when it was last seen, "Late
   landing", the newest read, and "What changed is not shown: Bronzeward can show it only by importing
   the machine's configuration for adoption." Without an open change it says "No change made on the
   machine is open." and offers only **Freeze**.
3. **Freeze**: type a reason (at most 1024 bytes) and press **Freeze**. The answer reads "Frozen.
   Recorded by <person> (<role>), act <act>." A frozen machine shows **Unfreeze** with the same form.
4. **Adopt**: **Start adoption** opens the import page as a drift adoption of this machine. This page
   stops at the review of the import. Publishing and the adopt plan are on their own pages.
5. **Revert**: **Plan revert** opens the create-plan page with the Desired release, this machine and
   `apply-config`. With no Desired release the page says "No release is selected for this machine, so
   there is nothing to revert it to."

A control you may not use is shown off with the reason beside it. The server's answer stays the
authority: a refusal is shown in place.

## What can go wrong

| What you see | Why, and what to do |
|---|---|
| "No open drift on machine <machine>." | Nothing to adopt or revert. A read that matches Applied, or could not be read, opens no record. Read the reads with `machine observations <machine>`. |
| "machine <machine> has no open drift record; nothing was sent. To apply its Desired release without a revert: ..." | The record closed, or never opened. Use the plan command the line names. |
| "machine <machine> has no Applied configuration, so it has no drift to revert; nothing was sent. Adopt it with an adopt plan: ..." | A machine that was never adopted has no drift. Make the adopt plan the line names. |
| "machine <machine> has no Desired release to revert to; nothing was sent. Publish a release that covers it, then: ..." | Publish first (see [publication](publication.md)). |
| "--reason: give a reason of 1 to 1024 bytes; nothing was sent" | The reason is empty or too long. |
| "drift revert reads the machine's Desired release again, and it may have changed, so it cannot repeat a write; nothing was sent." | `drift revert` takes no repeat flags. Repeat with `plan create`, using the release and flags the outcome-unknown block printed. |
| "This plan binds no drift record: the drift closed before it was created, so it is an ordinary apply-config, not a revert." | The machine stopped drifting while the plan was made. Read it again with `drift show`. |
| An unfreeze refused: "Why: a restore can erase the record of a request sent to this machine, and a request sent before the restore may still land. Until a recovery administrator accounts for the scope, unfreezing is refused." | The machine's scope is waiting for accounting after a restore. See [recovery after a restore](recovery-after-restore.md). A freeze is never refused for a scope. |
| An unfreeze refused: "Why: a server started for recovery refuses every request but the recovery entry until the entry is recorded..." | If the entry is not recorded yet, record it with `recovery enter`. Then restart the server without the recovery flag, and repeat. |
| "the epoch changed from <a> to <b> while the drift was read; nothing is shown. Read it again with: drift show <machine>" | The installation was restored while the command read. Read again. |

## What the result does and does not show

- A drift record shows that one configuration read differed from Applied. It does not show what
  changed, who changed it, or whether the machine still differs.
- A freeze shows that no new commitment or attempt starts on this machine. It does not stop an
  attempt that is already recorded, and it does not resolve the drift.
- An approved adopt plan makes Bronzeward record the adoption without sending anything to the
  machine, even while it is frozen. An approved revert is a plan to overwrite what nobody has
  reviewed: the approval records that you approve it, starts no operation, and the controller applies
  the plan only if its own checks pass when it sends it (see [plan and approval](plan-and-approval.md)).
- If the machine drifts again before a revert commits, the plan fails and must be made again.

Next: [plan and approval](plan-and-approval.md) for approving what you made here, and
[operation tracking](operation-tracking.md) to follow it. A machine whose operation is `unresolved`
after a restore is in [recovery after a restore](recovery-after-restore.md).
