# Words the client pages use

Read this page when a word on another page is new to you. Each entry says what the word means and
which page shows it at work. Nothing here is a command; the command pages are linked.

## You and the installation

- **Installation**: one running Bronzeward server and its records. A client talks to one
  installation at a time.
- **Context**: one installation that `bronzewardctl` knows, with the way you sign in to it. See
  [contexts](contexts.md).
- **Role**: what the installation lets you do. The server reports your roles; you cannot choose
  them. `viewer` reads. `author` writes drafts. `publisher` publishes drafts and creates plans.
  `approver` approves plans. `recovery-admin` decides what happens to a machine after a restore.
  See [sign in](sign-in-and-renewal.md).
- **Principal**: the identity the server holds for you or for an automation token. It is the
  identifier on the line "Principal" of `whoami`.
- **Epoch**: the installation's current generation. It changes when the installation is restored
  from a backup or enters recovery mode. A client that read something under an earlier epoch reads
  it again before it writes, so a message that says the epoch changed means "what you read may no
  longer hold; read it again".

## What a machine runs, and what it should run

- **Fragment, profile, assignment**: the three things a machine's configuration is made of. A
  fragment is a piece of configuration. A profile is a named list of fragments. An assignment says
  what one machine uses. See [editing](editing.md).
- **Draft**: a set of changes to one cluster that stays open until it is published or discarded.
  See [editing](editing.md).
- **ETag**: the version of a draft that a client last read. A write that carries an older ETag is
  refused, so nobody overwrites a change they have not seen.
- **Release**: the finished, rendered configuration of the machines of one cluster, built from one
  draft. It does not change after it is made. See [publication](publication.md).
- **Desired, Applied, Observed**: the release the installation wants a machine to run, the release
  it recorded as applied, and how the last read of the machine's configuration compares with
  them. See [the inventory](inventory.md).
- **Observation**: a recorded read of a machine's configuration.
- **Drift**: a machine whose configuration changed on the machine itself, after Bronzeward last
  verified it. The record of that change is a **drift record**.
- **Frozen**: a machine for which a plan cannot commit until an approver unfreezes the machine.
- **Import base**: the configuration read from a machine and kept as that machine's starting
  point. See [protected import](protected-import.md).
- **Adopt**: record what a machine runs as its starting point. Adopting sends nothing to the
  machine.

## Sending a change, and following it

- **Plan**: one proposed action on one machine: apply a release to it, or adopt it. Creating a plan
  sends nothing. See [plans and approval](plan-and-approval.md).
- **Approval**: a person with the `approver` role records that they approve a plan. An approval
  made by an automation token is refused.
- **Self-approval**: an approval by a person who also created the plan, published its release or
  wrote a change in it. It is allowed, and it is marked with the reason.
- **Evidence**: what a plan shows an approver before the approval: the validation result, the
  change (the **diff**) and whether a dry run exists.
- **Controller**: the part of the installation that, after an approval, checks the plan again and
  sends it to the machine only if every check passes.
- **Operation**: one run of something the installation does: a publication, an import, an apply or
  an adoption. See [operation tracking](operation-tracking.md).
