# Words the client pages use

Read this page when a word on another page is new to you. Each entry says what the word means and
which page shows it at work. Nothing here is a command; the command pages are linked.

## You and the installation

- **Installation**: one running Bronzeward server and its records. A client talks to one
  installation at a time.
- **Context**: one installation that `bronzewardctl` knows, with the way you sign in to it. See
  [contexts](contexts.md).
- **Role**: what the installation lets you do. The server reports your roles; you cannot choose
  them. `viewer` reads. `author` records clusters and machines, imports, and edits drafts.
  `publisher` publishes drafts and creates plans. `approver` approves plans and unfreezes machines.
  `recovery-admin` handles recovery after a restore. An automation token holds only `viewer`,
  `author` and `publisher`, and some actions are for a person only. See
  [sign in](sign-in-and-renewal.md).
- **Principal**: the identity record the server holds for a person or an automation token. It is
  the identifier on the line "Principal" of `whoami`.
- **Epoch**: the installation's recovery epoch. Entering recovery after a restore mints a new one.
  An approval made in an earlier epoch authorizes nothing, so it must be made again. A message that
  says the epoch changed means the installation was restored while your command ran: read again
  before you write.

## What a machine runs, and what it should run

- **Fragment, profile, assignment**: the three kinds of entry in a draft. A fragment is a piece of
  configuration. A profile is a named list of fragments. An assignment says what one machine
  uses. See [editing](editing.md).
- **Draft**: a set of changes to one cluster that stays open until it is published or discarded.
  See [editing](editing.md).
- **ETag**: the version of a draft that a client last read. A write that carries an older ETag is
  refused, so nobody overwrites a change they have not seen.
- **Release**: an immutable build of the configuration of every machine a draft covers. Publishing
  it makes it each machine's Desired release. See [publication](publication.md).
- **Desired, Applied, Observed**: Desired is the release the installation wants a machine to run.
  Applied is the configuration Bronzeward last verified or adopted, and the release it came
  from. Observed is the machine's best recorded read of its configuration, and whether that read
  equals Applied or Desired. See [the inventory](inventory.md).
- **Observation**: a recorded read of a machine's configuration.
- **Drift**: a read of the machine that differs from Applied while no operation holds the machine.
  It opens a **drift record**.
- **Frozen**: a machine whose plans cannot commit until an approver unfreezes it.
- **Import base**: the sanitized configuration read from a machine and kept in a draft as that
  machine's starting point. See [protected import](protected-import.md).
- **Adopt**: record what a machine runs as its Applied configuration, by observation. Adopting
  sends nothing to the machine.

## Sending a change, and following it

- **Plan**: one proposed action on one machine: apply a release to it, or adopt it. Creating a plan
  sends nothing. See [plans and approval](plan-and-approval.md).
- **Approval**: a person with the `approver` role records that they approve one plan. An approval
  made by an automation token is refused.
- **Self-approval**: approving a plan when you also created it, published its release or wrote a
  change in it. It is allowed, and the approval records the reason.
- **Evidence**: what a plan shows an approver before the approval: the validation result, the
  change (the **diff**) and whether a dry run exists.
- **Controller**: the part of the installation that commits an approved plan after its own checks,
  sends it to the machine, verifies the result and records Applied.
- **Operation**: one run of something the installation does: a publication, an import, an apply or
  an adoption. See [operation tracking](operation-tracking.md).
