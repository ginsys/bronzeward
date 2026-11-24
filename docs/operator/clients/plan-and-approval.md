# Journey: plan a release for a machine, approve it, and apply it to several

**You want** to send a published release to one machine, or to several, and to have a person
approve it first. A **plan** is one proposed action on one machine: apply a release to it, or
adopt it as it is. An approver reads the plan and approves it. Only then can the controller send
anything.

**You need** the `publisher` role to create a plan and a person with the `approver` role to
approve it. One operator who holds both can do every step. An automation token can create a plan
but cannot approve one: an approval is for a human sign-in only. The release comes from
[publication](publication.md). What happens after the approval is in
[operation tracking](operation-tracking.md). Words such as plan, approval, evidence and
self-approval are in [the word list](terms.md).

The `text` blocks on this page show the shape of the output, read from the source. This page does
not claim that a run printed them.

**Nothing here sends a configuration to a machine.** Creating a plan sends nothing. Approving a
plan starts no operation. After an approval, the controller checks the plan again when it is time
to send it and applies it only if every check passes.

## Command line, one machine

Create the plan for one release, one machine and one operation. Publishing already printed the
command for each machine (see [publication](publication.md)).

```bash
bronzewardctl plan create --release <release> --machine <machine> --operation apply-config
```

`<machine>` is the machine's name or its ID (see [the inventory](inventory.md)).
`--operation` is `apply-config` or `adopt`. An `apply-config` plan sends the release; its only
mode is `no-reboot`. An `adopt` plan records what the machine runs as its starting point and sends
nothing, and it takes no `--mode`, deadline or attempt flags. Leave a limit out to use the
configured default. The [reference](cli-reference.md#plan-create) lists the limits.

The command prints the plan: its state, its expiry, the machine, the release and the machine's
Desired release at creation, the assignment and any drift record, the limits, who created it, and
the **evidence**. The evidence is the validation result, a dry run (the proof of concept has none:
"Dry run: not available"), and the **diff**, the whole redacted configuration with the changed
lines marked. A new plan reads "It is not approved and nothing runs until an approver approves."
Read it with `plan show <plan>`, list plans with `plan list`, and stop an unwanted one with
`plan cancel <plan> --reason "<why>"`.

```bash
bronzewardctl plan show <plan>
bronzewardctl approve <plan>
```

`approve` prints the plan again, with a prediction of the **self-approval** it will record, and
then asks:

```text
Answering y records your approval of this plan as you. Approve it? [y/N]:
```

Only `y` or `yes` sends the approval. Enter, `n` and the end of input send nothing. There is no
`--yes`, and `--no-input` refuses the command. These are guards against approving by habit
without the evidence on screen. They do not prove that a person typed the answer. The server
enforces the human-only rule: an automation token is refused, and the client says so before it
sends.

After `y` the command prints "Approved (<approval>). Recorded self-approval reasons: <reasons>."
and, for an apply plan, "The controller re-checks the plan at use time; it applies it only if
every check passes." For an adopt plan it says the controller records the adoption without
sending anything.

**Self-approval is allowed and marked.** If you created the plan, published the release, or wrote
a fragment, profile or assignment revision in it, the approval records that reason (`created-plan`,
`published`, `authored-change`, `authored-reused`). A reason for automation you own
(`owned-automation`) or for the import base cannot be predicted before you approve, so the
prediction never shows you as clear of it.

## Command line, several machines

`apply` makes one apply-config plan for each machine you choose, shows every change when you pass
`--diff` (without it, only the number of lines of each change), and asks one question for all of
them.

```bash
bronzewardctl apply <release> --machine <machine>,<machine> --diff
bronzewardctl apply <release> --all --diff
```

Each machine is named by name or ID. `--diff` prints each plan's change. The command prints, for
each machine, its role, its profiles, its plan with its expiry, and whether this command made the
plan or somebody else prepared it. A plan somebody prepared and that is ready is reused, never
made twice. It then asks:

```text
Answering y records your approval of the 2 plans listed as you. Approve them? [y/N]:
```

`y` approves every plan listed, each by its own approval, and prints one line per machine. A
refusal for one machine leaves the others approved and the command exits non-zero. Enter, `n` and
the end of input approve nothing, and the plans this command made are cancelled. `-o json` and
`-o yaml` are not offered, since the command asks a person a question.

The command leaves a machine out, with its reason, and never approves it unseen:

| Reason printed | What to do |
|---|---|
| "applied: this release is what the machine runs" | Nothing to do. |
| "approved already: the controller sends it when its gates allow" | Wait; follow it in [operation tracking](operation-tracking.md). |
| "applying: its operation is in progress" | Wait. |
| "stopped: the machine is frozen; an approver can unfreeze it" | An approver unfreezes the machine first. |
| "stopped: the machine changed since Bronzeward last verified it; decide on the drift first" | Decide on it in [the drift journey](drift.md). |
| "stopped: an operation on this machine is unresolved; open it with operation list" | Read the operation. After a restore, [recovery](recovery-after-restore.md) settles it. |
| "stopped: the plan waiting for this machine is not an apply plan; open it with plan show <plan>" | Decide that plan on its own. |
| "stopped: the change of its plan cannot be shown ... it is left out" | The configuration could not be redacted, or the machine holds a configuration other than the one Bronzeward last applied. A plan somebody else prepared stays: read it with `plan show` and approve it alone with `approve`, or leave the machine out. A plan this command made is cancelled: make a new one with `plan create` and approve that, or leave the machine out. |
| "stopped: the change of its plan could not be read (<reason>), so it is left out" | Try again; read the plan with `plan show`. |
| "stopped: its plan overwrites the drifted configuration of drift record <n>, which no reviewer has seen; it is left out." | The plan is bound to a drift record. A plan somebody else prepared stays: approve it alone with `approve`, or leave the machine out. A plan this command made is cancelled: make a new one with `plan create`, or leave the machine out. The drift record is in [the drift journey](drift.md). |
| "not a machine of release <release>: <names>; nothing was sent" | A name you gave is not covered by this release. |

If you choose a control plane machine, the command says: "A control plane machine is chosen.
Applying to a control plane machine was not covered by this version's acceptance tests. The server
does not refuse it, machines are applied one at a time, and you decide." The controller applies
one machine of a cluster at a time, in an order it does not promise.

## Web client

**One plan.** The release page lists its machines, each with a **Rendered configuration** link.
Under "Next step" on that rendered page, **Plan adopt** or **Plan apply** opens the plan form and
sends nothing.

1. In the form "Create plan", choose the operation: "apply-config (send this release, mode
   no-reboot)" or "adopt (records what the machine runs; sends nothing to it)". Leave a limit
   blank to use the default.
2. Press **Create plan**. It needs `publisher`. The plan page shows the plan, its evidence and the
   diff.
3. As an approver, read the plan page. Above the one **Approve** button it says: "Pressing
   Approve records your approval of this plan." and what the controller does next, and "Approval is
   for a human sign-in only." No field asks you to type anything back. Press **Approve**.
4. **Cancel plan** takes a reason of 1 to 1024 bytes. After the controller commits a plan,
   cancelling ends the operation only while no attempt is recorded.

**Several machines.** On the release page choose **Apply this release to machines**.

1. Tick the machines to change. None is chosen for you. Each line shows the machine's name, its
   role and its profiles, and where it stands for this release: "Not applied yet.", "A plan is
   ready. It is not approved.", "Approved. It waits behind another machine's apply", "Applying:
   the operation is in progress.", "Applied: this release is what the machine runs." or a
   "Stopped:" line with its reason.
2. Press **Show apply**. It needs `publisher` and makes one plan for each machine you ticked. The
   page lists each plan's change. If you are an approver without the publisher role, a publisher
   prepares the plans and you then press Apply.
3. Read the changes, then press **Apply**. It needs `approver`. The sentence above it says that
   pressing Apply records your approval of each plan shown, and what the controller does next.
   The page carries on past a machine the server refuses and says so on that machine's line.
4. **Apply** stays off while any plan's change is unread, bound to a drift record, or withheld by
   the server. The page names that plan and links to it. Open it, read it, and approve it there
   one plan at a time, or untick the machine. A plan the page made and you did not approve is
   cancelled when you untick its machine or leave the page, with one exception: a plan the page
   handed over to you because its change is withheld or bound to a drift record stays either way.
   Cancel it on its own page with **Cancel plan** if you do not want it. A plan you pressed Apply
   for is never cancelled.

**Adopting a draft's machines.** An open draft page that holds a machine's import has a section
"Adopt these machines" with one button of the same name. The sentence above it reads: "Adopting
records what each machine runs as its starting point. This adds an assignment that selects nothing
for each machine that has none, publishes the draft, then creates an adopt plan for each machine
and approves each plan as you, recorded as your own approval. It sends nothing to any machine. A
change you apply later keeps its own plan and approval." The button needs the author, publisher
and approver roles; without them the page says "Needs the <roles> role. The separate steps stay:
publish this draft, then on each machine's release page plan the adopt, then approve each plan."
It acts only on a machine with no Applied configuration; a machine that has one is left, and the
line says "Left as it is: this machine already has an applied configuration, so its changes keep
their own steps." If the draft editor holds text you have not saved, the press sends nothing and
says so. A second press after a closed page continues from the recorded state and repeats no step.
The import journey that leads to it is in [import and adopt](protected-import.md).

## What can go wrong

| What you see | Why, and what to do |
|---|---|
| `Needs one of the roles [publisher].` or `[approver]`, and `You are signed in as ...` | You lack the role for that step. Ask for it. |
| "This needs a person signed in, not an automation token." | An approval needs a human sign-in. Sign in as a person. |
| "Plan the machine's Desired release instead: bronzewardctl plan create ..." | The release is not the machine's Desired release. Plan the one named. |
| "The machine has no Desired release: publish a release that covers it first." | Publish first. |
| "Adopt the machine with an adopt plan, which records what the machine runs as its starting point and sends nothing: ..." | The machine has no Applied configuration; adopt before you apply. |
| "Add an assignment for the machine to a draft, one that selects nothing is enough: bronzewardctl assignment set <machine> --draft <draft> --none. Publish that draft, then plan the release it makes." | The machine has no assignment. See [editing](editing.md). |
| "Publish a release from the machine's current assignment, then plan that release." | The assignment moved after the release was made. |
| "Expired: it can no longer be approved or committed. Create a new plan: ..." | The plan passed its expiry. Create a new one. Likewise after "Cancelled" and "Revoked". |
| "This approval is from before the last recovery entry and authorizes nothing; an approver may approve again" | The installation was restored after the approval. An approver approves again. |
| "stopped: the answer is not y or yes; nothing was sent" | You answered something else. Nothing happened; run the command again. |
| "approve asks for your y, which --no-input forbids; nothing was sent" | Run it without `--no-input`. |
| "the epoch changed while the plans were read, so nothing was approved; run apply again" | The installation was restored while `apply` read. Run it again. |
| "Not approved: the apply page was left or its selection changed." | The web page cancelled a plan it made and you did not approve. |
| "Nothing was approved for this machine. Press Show apply to read where it stands now: a plan that changed or expired is made again." | The server refused that machine. Press Show apply again. |

## What the result does and does not show

- A created plan shows that the server accepted it and validated the release for that machine. It
  shows nothing about the machine.
- An approval records who approved which plan, with any self-approval reasons. It starts no
  operation and does not show that the machine will accept the configuration.
- The controller applies a plan only if its own checks pass when it sends it. An approved plan
  that is not applied expires at the time shown. Whether it was applied is read in
  [operation tracking](operation-tracking.md).
- The revert of a drifted configuration has an extra approval line that says the approver
  approves overwriting a configuration nobody reviewed. That belongs to [the drift journey](drift.md).

Next: [follow the operation the controller makes from the approved plan](operation-tracking.md). A release you still
need to make is in [publication](publication.md).
