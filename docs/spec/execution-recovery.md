# Execution and recovery contract

This document specifies the first-milestone execution boundary for
configuration control on an existing cluster, as required by
[Specify execution and recovery](https://github.com/ginsys/bronzeward/issues/19).
It refines the [current design](../design/Talos_Configuration_and_Machine_Management_Design.md)
§§7.7, 7.8, 9.1, 12.1–12.7, 13.7, 14.4, 14.6 and 15.2 for the PoC profile
(PostgreSQL with OpenBao KV v2 and Transit, design
[§7.7](../design/Talos_Configuration_and_Machine_Management_Design.md#77-poc-deployment-profile)),
under the owner's retention and recovery policy
([§7.8](../design/Talos_Configuration_and_Machine_Management_Design.md#78-poc-retention-and-recovery-policy))
and identity and approval policy
([§13.7](../design/Talos_Configuration_and_Machine_Management_Design.md#137-poc-identity-and-approval-policy)).
Each section names the design text it refines. It is a PoC contract, not
evidence that any mechanism has passed design experiment E4 or E6.

The evidence it rests on, abbreviated below:

| Short name | Report |
| --- | --- |
| DS | [Approval and dispatch safety](../design/research/20260925-dispatch-safety.md) |
| DB | [Database semantics](../design/research/20260924-database-semantics.md) |
| KL | [Key loss and restoration](../design/research/20260924-key-loss-restoration.md) |
| E3 | [Talos compatibility](../design/research/20260925-talos-compatibility.md) |
| E1 | [Secret ingress: extraction before persistence](../design/research/20260922-secret-ingress-extraction-before-persistence.md) |
| FR | [Feasibility evidence review](../design/research/20260925-feasibility-evidence-review.md) |
| Fx | [Investigation fixtures](../design/research/20260919-investigation-fixtures.md) |

"DS row 005" is row 005 of the dispatch-safety capture (DS §3). Every dispatch
result holds for one `apply-config --mode=no-reboot` on one container worker
running Talos v1.13.6, at one assignment revision, against one PostgreSQL 17.11
server at its default isolation, with a rollout limit of one
([DS §7](../design/research/20260925-dispatch-safety.md#7-limits)). This
contract inherits those limits; §9 lists them with the gaps.

Where neither the design nor the evidence decides a question, this contract
makes a choice consistent with the design, for a stated reason, and marks it in
place as **(choice §10.n)**; §10 lists each with its reason and alternative, and
says where the choice is not the most conservative option. "Inferred" marks a
claim that no row or test measured.

Two sibling contracts share the boundary. The
[secret ingress and compilation contract](https://github.com/ginsys/bronzeward/issues/17)
(`docs/spec/compilation.md`) owns ingestion, including drift adoption's
extraction, the baseline, redaction and the release record. The
[persistence and API contracts](https://github.com/ginsys/bronzeward/issues/18)
(`docs/spec/persistence-api.md`) own entities, revisions, transactions,
publication, the API, authentication and authorization, idempotency, the API
handle of an operation, migrations and the restore epoch as a persistence
mechanism. Where this contract needs something from persistence it states the
requirement and says "see `persistence-api.md`"; it specifies no schema. The
durable records it requires are: plans and their states (§2), approvals,
approval and identity revocations and cancellations, operations and their
owners (§3.4), the machine timeline with its revision order (§4.1),
observations, attempts, responses, accounting decisions (§5.2), drift records,
freezes and adoption records (§6), and recovery entries, epochs, scope states
and scope releases (§7).

## 1. Supported operation and state values

Design: [§12.1](../design/Talos_Configuration_and_Machine_Management_Design.md#121-desired-applied-and-observed-state),
[§12.2](../design/Talos_Configuration_and_Machine_Management_Design.md#122-plan-before-apply),
[§18.2](../design/Talos_Configuration_and_Machine_Management_Design.md#182-phase-1---configuration-control-on-an-existing-cluster).

The PoC supports one operation class, the one E4 exercised: applying a
published, encrypted per-machine artifact to an existing **worker** with Talos
`apply-config` in `no-reboot` mode, over direct Talos access (DS §2.1, §6.3).
The artifact's contract minor is the node's running minor (compilation
contract §10.2); a v1.13 worker running v1.13.6 is the only evidenced target
([E3 §4.3](../design/research/20260925-talos-compatibility.md#43-operationrpc-compatibility-against-v1136)).
Publication, observation, adoption and recovery are separate capabilities that
send nothing to a machine.

Out of scope until their operation-specific safety evidence exists: control
plane targets, bootstrap, reset, wipe, upgrade, reboot, `auto`, `try` and
`staged` modes, remote transports and managed-cluster etcd recovery (design
§18.2). The Upgrade/LifecycleClient transition is deferred and design
experiment E3 has not passed
([E3 §6.3](../design/research/20260925-talos-compatibility.md#63-criterion-3-unsupported-combinations-and-the-deferred-lifecycle-tests));
this contract claims no upgrade support, no lifecycle execution and no full E3.
The design's bounded **Ignore** drift policy is not supported in the PoC
**(choice §10.1)**; §6 specifies freeze, adopt and revert.

The manager stores three distinct values per machine:

| Value | Meaning |
| --- | --- |
| Desired | The release the application database records as selected for the machine. Selection is not authorization; approval belongs to a plan (§2). |
| Applied | The last verified configuration: a release, the configuration digest verified on the machine, and its source, either a completed operation (§4) or an adoption record (§6.3). |
| Observed | The latest machine-reported identity, assignment evidence, running Talos version, configuration digest, machine-configuration resource version and health results, with the observation's revision, time and purpose (§4.1). |

The **configuration digest** is SHA-256 over the machine configuration read
back from the node (the `v1alpha1` machine-config resource's `spec`) with its
trailing newlines replaced by exactly one, and the same function over the
artifact's plaintext. This is the normalization the fixtures and E4 used; the
read path adds one newline
([Fx §4](../design/research/20260919-investigation-fixtures.md#4-expected-and-observed)
check 3; DS §2.3). It is one fixed function for every comparison in this
contract **(choice §10.2)**. Planning cannot decrypt an artifact, and no
runtime identity can decrypt a baseline, so compilation records the digest:
over each artifact's plaintext at publication, and over the exact input it
encrypts as a baseline at import and drift adoption, which ingestion reads
from the node as the same `v1alpha1` resource `spec` (compilation contract
§2.3 step 8, §11). Its guessability, as a persisted digest of a secret-bearing
document, was not assessed (compilation contract §4.1, choice §16.26 there).

`Applied` changes only when an operation enters `completed`, to that
operation's bound release and artifact digest, or by an adoption record, to the
adopted release and the digest verified by observation (§6.3). An adoption
record is the approved, observation-verified baseline that design §12.1 allows
as the second source of the applied release; it also establishes the first
`Applied` of a machine that has none. An RPC response alone never updates
`Applied`.

Two differences are kept apart:

- **Pending convergence**: the `Desired` release's artifact digest for the
  machine differs from the `Applied` digest. A plan and approval resolve it; an
  offline machine in this state is simply not yet converged.
- **Drift**: the observed digest differs from the `Applied` digest while no
  operation holds the machine's scope (§6).

A newer observation does not invalidate a plan unless it contradicts one of the
plan's bound preconditions; observation freshness at dispatch is governed by §3.

## 2. Immutable plan and approval binding

Design: [§12.2](../design/Talos_Configuration_and_Machine_Management_Design.md#122-plan-before-apply),
[§12.7](../design/Talos_Configuration_and_Machine_Management_Design.md#127-application-approval-and-dispatch-boundary),
[§13.7](../design/Talos_Configuration_and_Machine_Management_Design.md#137-poc-identity-and-approval-policy).

A `publisher` creates a plan from a published release (design §13.7 item 2);
an adoption plan, which sends nothing, is §6.3's. Planning resolves a published
artifact. Dispatch never re-renders a draft or
silently substitutes a newer artifact. The immutable plan binds:

- plan, release and exact per-machine artifact identities, with the artifact's
  configuration digest (§1);
- the release's renderer and contract record, its secret and encryption
  dependency records, and the artifact's Transit key by an identity the
  provider cannot reissue, together with its version, not by name and version
  alone (compilation contract §9, §10.2;
  [KL §7](../design/research/20260924-key-loss-restoration.md#7-recommendation)
  item 1, inferred);
- machine identity and assignment revision;
- the machine's **baseline revision**, a per-machine counter advanced by every
  change of `Applied`, whether by a completed operation or by an adoption
  record (§6.3). A machine with no `Applied` has no baseline revision, so no
  plan can be made for it before its baseline is accepted;
- the expected pre-dispatch configuration digest. This is mandatory for every
  `apply-config` plan, because the request replaces the whole configuration;
- the open drift record, if the machine has one; such a plan is a revert
  (§6.4);
- operation `apply-config`, mode `no-reboot`, the transport route (§3.5) and
  every other parameter value that reaches the Talos request;
- expected preconditions: the node's running Talos minor, the maximum age of
  the execution-time observation, the validity window of the use-time
  dependency check (§3.1), and any health or capacity check;
- expected postconditions: the machine identity, the assignment revision and
  the artifact's configuration digest, plus any bound health check;
- the transport deadline and the verification deadline, as durations from the
  attempt, the first no later than the second, and the maximum number of
  attempts;
- rollout scope and limit, expiry, idempotency key and the approval policy
  (design §13.7: one approval by an `approver`);
- the plan revision and the creating identity and role.

The plan also carries **plan-time evidence**: the redacted diff, the upstream
validation result and any available dry-run evidence, all derived and redacted
under the compilation contract (§8.3 there). A diff is of whole configurations,
not only of the intended patch: E3 saw a one-label patch applied as a full
re-encode, the worker's configuration read shrinking from 428 lines to 55, with
what else changed unrecorded
([E3 §7](../design/research/20260925-talos-compatibility.md#7-limits)).
Plan-time evidence informs review and approval. It never substitutes for the
execution-time checks against live state in §3, and the timeline records the
two separately.

The plan is invalid if any bound value changes, including the artifact,
assignment, baseline revision, operation, mode, route, a parameter value, a
precondition, expiry or approval policy. The controller must replan and obtain
approval again rather than repair the plan in place.

**Plan states.** Until dispatch commitment the plan is the handle for its
change, and no operation exists for it. The commitment transaction (§3.2)
creates the plan's one operation and links the plan to it; from then on the
operation's states (§4) carry the outcome **(choice §10.3)**.

| State | Meaning | Leaves by |
| --- | --- | --- |
| `proposed` | The immutable plan exists but is not authorized. | an approval, to `approved`; a cancellation, to `cancelled`; its expiry, to `expired` |
| `approved` | An approval matches the complete binding. | the §3.2 transaction, to `committed`; revocation of the approval or of the approving identity, to `revoked`; a cancellation, to `cancelled`; its expiry, to `expired` |
| `committed` | Terminal for the plan. Its operation exists and holds the outcome. | none |
| `revoked`, `cancelled`, `expired` | Terminal. No operation was created and nothing was sent (DS row 002, where the prototype reports the outcome as `cancelled`). | none |

An approval recorded before the current recovery epoch authorizes nothing
(§7.1); an unexpired `approved` plan whose approval is from an earlier epoch
cannot commit until an `approver` approves it again in the current epoch.
Revocation, cancellation and expiry after commitment are recorded against the
plan and reach its operation through §3.3.

**Approval.** One approval by an identity holding `approver` authorizes a plan;
automation never holds `approver`, and `recovery-admin` carries no approval
(design §13.7 items 2 and 3). The approval names the plan revision, records the
approving identity, the role it acted under and the recovery epoch in which it
was recorded (§7.1), and is marked self-approval where design §13.7 item 3
requires. Of the two recording cases §13.7 leaves to this contract, both are
marked self-approval **(choice §10.4)**:

- the approving identity authored any revision the release contains, whether
  of a fragment, a profile, an assignment or an import base, however long ago
  and whether or not this release changed it;
- the approval is by the human whom the token-issuing record names as
  responsible for an automation token that authored or published content in
  the release, or created the plan. The token record is persistence's, which
  requires a responsible human for every token (see `persistence-api.md`). As a
  guard only, an approval for which no such record can be found is recorded as
  "self-approval undetermined", never as not self-approval.

Approval authorizes exactly this binding. Publication, a `retained` dependency
verdict and access to a Transit key never authorize dispatch (design §7.4 step
5, §7.8). The trusted controller enforces authorization; the provider does not
(design §12.7, §13.2).

**Revocation and cancellation.** Any `approver` or `recovery-admin` may revoke
an approval; `recovery-admin` records an identity revocation, which invalidates
every approval that identity gave and that no attempt has used yet, including
approvals already used by a commitment (design §13.7 item 4). This contract
also lets it refuse every later attempt of an operation that identity
approved, retries included (§3.3, **choice §10.6**). A plan may be cancelled by
its creator, any `approver` or `recovery-admin` (item 6). What each reaches is
§3.3 and §8.

An approval revocation locks the approval it revokes before recording the
revocation (on PostgreSQL, `SELECT … FOR UPDATE`), so it waits for a commitment
or attempt transaction that read the approval `FOR SHARE` (§3.2 comparison 1;
DS row 003). An identity revocation likewise locks the revoked identity's
principal record, which those transactions read `FOR SHARE`. The records are
persistence's; see `persistence-api.md`.

## 3. Dispatch commitment

Design: [§12.7](../design/Talos_Configuration_and_Machine_Management_Design.md#127-application-approval-and-dispatch-boundary),
[§12.5](../design/Talos_Configuration_and_Machine_Management_Design.md#125-durable-operations-and-uncertain-outcomes),
[§12.3](../design/Talos_Configuration_and_Machine_Management_Design.md#123-rollout-policy),
[§7.4](../design/Talos_Configuration_and_Machine_Management_Design.md#74-publication-without-distributed-transactions).

No role executes. The controller dispatches an approved plan through the
transactions below (design §13.7 item 2). The database and the external systems
do not share a transaction, so commitment has two phases with different
guarantees, and a residual window that this contract states rather than hides.

E4 placed the **dispatch commitment boundary** at the `COMMIT` of the
commitment transaction, and showed each comparison that its controls removed
letting through the failure it exists to prevent
([DS §6.1](../design/research/20260925-dispatch-safety.md#61-criterion-1-the-boundary-and-pre-commit-revocation),
[§8](../design/research/20260925-dispatch-safety.md#8-recommendation)).

### 3.1 Execution-time evidence, gathered before the transaction

Before touching any protected resource, the controller checks that the plan is
currently approved, unrevoked and unexpired and that the scope gate is open
(comparisons 1 and 6 of §3.2). Artifact decryption and operation credentials
are gated by that application check; a plan whose approval was revoked while it
was queued has neither read. The check is not atomic with what follows, which
is why §3.2 repeats it.

The controller then gathers and durably records, on the machine's timeline
(§4.1) as dispatch evidence for this plan, naming the plan and the controller
instance (the operation does not exist yet; §2):

1. a fresh machine observation taken for this dispatch (purpose `evidence`):
   identity, assignment, running Talos version, configuration digest,
   machine-configuration resource version, and the health and capacity
   evidence the plan's preconditions name; and
2. a use-time check, with its time, of the dependencies dispatch itself needs,
   under the executor identity: decryption of the bound artifact under the
   bound key identity and version, and the operation credentials for the
   target.

Item 2 is deliberately narrow. The executor holds scoped artifact decryption
and operation credentials, not compiler-level secret access (compilation
contract §1). Applying a retained artifact needs its release record, the key
version it names at or above the decryption floor, an executor credential the
provider recognises and an unsealed provider; losing a source secret version
blocks regeneration, not application (design §7.8, "Guarantees kept apart";
[KL §5.2](../design/research/20260924-key-loss-restoration.md#52-criterion-2-applying-is-not-regenerating-and-ciphertext-is-not-executability)).
A `retained` monitor verdict is not this check (design §7.6). A sealed OpenBao
fails it, and nothing is dispatched until the operator unseals (design §7.8
item 4).

A stored observation older than the plan's maximum age never satisfies item 1,
whatever it says. A machine that cannot be observed cannot be dispatched to. A
node whose running minor differs from the artifact's contract minor fails the
precondition: it is outside the proven path (§1).

### 3.2 The commitment transaction

The transaction that creates the durable dispatch intent compares database
facts only, atomically, each read under a lock that a concurrent change must
wait for or precede. On PostgreSQL a plain read does not stop a concurrent
change before commit
([DB §4.2](../design/research/20260924-database-semantics.md#42-s2-all-or-nothing-publication)
rows 010 and 011); design §7.7's consequences require such locks, each with a
test that can fail. The transaction first locks the machine's row, the lock
under which §4.1 allocates timeline revisions (on PostgreSQL, `FOR UPDATE`),
and reads the other records named below `FOR SHARE`; the lock order is
persistence's (see `persistence-api.md`).

0. no operation exists for this plan yet: the transaction creates it, so a
   second executor for the same plan is refused (DS row 009; §2,
   **choice §10.3**);
1. the plan is `approved` (in an attempt transaction, `committed` to this
   operation), unexpired and has no recorded cancellation; the approval is not
   revoked; the approving identity has no identity revocation; and the
   approval was recorded in the current recovery epoch (§7.1). The approval,
   the approving identity's
   principal record and the installation state that holds the current epoch
   are each read so that a concurrent revocation or recovery-mode entry waits
   for this transaction or precedes it (on PostgreSQL, `FOR SHARE`; DS row 003
   for the approval);
2. the artifact, assignment, operation, mode, route and parameter bindings are
   unchanged, with the machine's assignment head read `FOR SHARE`, and the
   machine's baseline revision equals the bound one, so a plan made before an
   adoption or another operation's completion, or for a machine whose baseline
   was never accepted, cannot commit (DS row 010);
3. the evidence recorded under §3.1 was recorded for this plan by this
   controller instance, satisfies the plan's preconditions, is inside its bound
   maximum age or validity window, and no newer observation of the machine
   contradicts it. The observed configuration digest must equal the bound
   pre-dispatch digest; on a retry, where the evidence is recorded for the
   operation, it may instead equal the bound artifact's digest (DS row 023,
   `TestEvidenceMayShowArtifactOnlyOnRetry`);
4. this operation takes the machine's coordination scope, and no other
   operation on that scope is `committed`, `sending`, `verifying` or
   `unresolved` (on PostgreSQL, a partial unique index; DS row 010, control row
   011;
   [DB §4.3](../design/research/20260924-database-semantics.md#43-s3-unique-operation-intent));
5. this operation takes a slot in the plan's rollout scope, counting every
   operation of that scope in `committed`, `sending`, `verifying` or
   `unresolved` against the bound rollout limit; and
6. the **scope gate** is open: the machine scope is not frozen (§6.2), and
   either recovery mode is not in effect or the scope was explicitly released
   in the current recovery epoch (§7.3 step 7). The freeze and the scope's
   recovery state are read under the machine row's lock, and recovery mode
   with the installation state of comparison 1.

The commitment transaction also creates the operation in `committed`, links the
plan and its §3.1 evidence to it, and makes the committing controller instance
the operation's owner at generation 1 in the current epoch (§3.4).

If any comparison fails, nothing is committed and nothing is sent. Freeze,
recovery mode and scope release are durable database facts precisely so that
they can be compared here; checking them only before the transaction would
leave a race in which dispatch proceeds while mutation is meant to be paused.

The machine scope also protects the binding it was taken for. Any transaction
that changes a machine's assignment checks that machine's coordination scope
and is refused while an operation on it is `committed`, `sending`, `verifying`
or `unresolved`. Detecting a changed assignment revision only during
verification would be too late: the artifact for the old revision would already
have reached the machine.

The PoC rollout limit is one **(choice §10.5)**; at that limit the machine
scope stands for comparison 5, as it did in E4 (DS §2.1). Drain is not
required for a `no-reboot` apply at the limit of one (inferred: design §12.3
names drain with worker concurrency, and no E4 row drained); the cluster
health and capacity gate is a bound precondition checked under comparison 3.

### 3.3 After commitment

Every attempt, the first included, is recorded by an **attempt transaction**
before its request is sent; the timeline therefore shows the commitment and the
attempt before any Talos request. The attempt transaction takes the same locks
as §3.2, repeats its comparisons 1–3 and 6, against newly gathered §3.1
evidence recorded for the operation when it is a retry, and adds, in the
prototype's numbering (DS §2.1):

7. the recording controller is the operation's current owner at the current
   generation and epoch, and the operation is `committed`, or `unresolved` with
   a safe-to-retry classification made by that owner (§5). The check is made
   inside the `UPDATE` that records the attempt (design §7.7, consequences),
   not by a read followed by an insert: a read-then-insert recorded a stale
   attempt on PostgreSQL
   ([DB §4.4](../design/research/20260924-database-semantics.md#44-s4-ownership-transitions)
   row 018; DS rows 007 and 008);
8. on a retry, no attempt response, ownership transition or reclassification
   was recorded on the timeline after the revision at which the retry was
   classified, so a late acceptance of the earlier attempt forces
   reclassification instead of a duplicate request (DS row 023,
   `TestRetryRefusedAfterNewerTimelineFact`); and
9. the operation has attempts left under the bound maximum.

The transaction records the attempt identity, the owner token, the route, and
the absolute transport and verification deadlines derived from the plan. An
owner that lost ownership before its attempt transaction records no attempt
and sends nothing (DS row 007). The first attempt transaction may be the
commitment transaction itself. Reading the approval or the ownership and
recording the attempt later is not sufficient.

The controller sends only after its attempt transaction has committed. E4
established this for its own executor only: its one send follows a successful
attempt transaction on every path
([DS §6.2](../design/research/20260925-dispatch-safety.md#62-criterion-2-uncertain-sends-stale-ownership-a-after-b)).
An implementation must show the same property for itself (§9.2).

Every request carries a transport deadline no later than its recorded
verification deadline. A transport deadline ends the client's wait; it does
not bound when the request can land. A request sent through the control
plane's API proxy landed about 11 s after the partition healed, after its
client had been killed at its deadline (DS §4.4, capture 1 row 012), and late
landings occurred in three of six control-plane partitions across both
captures and the reproduction
([FR §7](../design/research/20260925-feasibility-evidence-review.md#7-reproduction-of-the-disputed-results)).
§5.2 states what accounts for such an attempt.

**Revocation, cancellation and a closed gate after commitment.** Before the
commitment transaction commits, a revocation prevents dispatch (DS row 002),
and so does a cancellation or expiry, which fail the same comparison (no row
cancelled a plan). After it, the revocation is recorded on the timeline and
fails comparison 1 of every attempt transaction that has not yet committed, so
it also permits no retry (DS rows 003 and 004). An operation with no recorded
attempt then becomes `unresolved` with its scope held, and `cancelled` (§4). An
attempt already recorded is sent anyway and runs to its own classification
(DS row 005); after it, revocation undoes nothing, and only a new approved
plan, such as a revert (§6.4), or recovery changes the machine (design §13.7
item 4). Freezing the scope or entering recovery mode closes the scope gate
with the same effect through comparison 6.

An identity revocation has the same effect. Design §13.7 item 4 makes it
invalidate every approval the identity gave that no attempt has used; this
contract also makes comparison 1 refuse every later attempt transaction of an
operation that identity approved, retries included, as an approval revocation
does **(choice §10.6)**. An attempt already recorded is sent anyway; if its
outcome is then unknown, the operation is `unresolved` and ends only through
accounting (§5.2) and a completion observation, as `completed` or `failed`
(§5). Identity revocation was not measured (design §13.7, evidence and its
limit).

**The residual window.** The machine or a dependency can change after §3.1 and
before the request arrives. Some such changes surface: as a failure before any
attempt is recorded, as a definitive rejection, or as a postcondition mismatch,
each classified under §5. Not all do. `apply-config` sends a full
configuration, so an out-of-band change that lands inside the window, such as
an emergency `talosctl` edit, can be overwritten by a request that then
verifies and completes normally, leaving no trace in this operation's
evidence. The bound maximum observation age narrows this window; it does not
close it, and this contract claims no detection of such a change. Closing it
needs a compare-and-apply precondition on the Talos side; whether `apply-config`
offers one was not checked (DS §8), and none is assumed.

### 3.4 Ownership: the takeover fence

Design: [§12.5](../design/Talos_Configuration_and_Machine_Management_Design.md#125-durable-operations-and-uncertain-outcomes).

The ownership mechanism is the **takeover fence** that E4 recommends (DS §8).
An operation's owner is a controller instance with a generation and the epoch
it was issued in. A **takeover**, in one conditional `UPDATE` on the owner's
generation, sets a new owner, increments the generation and moves the
operation from `committed`, `sending` or `verifying` to `unresolved`; an
`unresolved` operation stays `unresolved` under its new owner. The attempt
transaction's comparison 7 checks owner, generation, epoch and state together.
E4's control row 008, without those comparisons, let a stale executor record an
attempt and send; the rows do not show that either half alone suffices
(DS §4.2), so both are required. Concurrent takeovers of one generation elect
exactly one winner (DB §4.4 row 015, PostgreSQL).

The controller takes over, at its start, every non-terminal operation owned by
another instance, and `recovery-admin` may request a takeover explicitly; no
timer or lease takes an operation over **(choice §10.7)**. A takeover sends
nothing and never classifies an outcome: it only stops the old owner from
recording further attempts.

The fence bounds only work not yet recorded. It cannot stop a request already
sent, or one sent by anything that bypasses the database, direct `talosctl`
included. A database fence is not a Talos fence (DS §7; design §12.5). A
restore rewinds generations, so the fence alone does not survive one (§7.1).

Alternatives E4 names and neither built nor measured: an expiring lease, an
advisory lock held for the executor's lifetime, and a Talos-side
compare-and-apply (DS §8).

### 3.5 Talos client and route

The executor's Talos client is selected here (compilation contract §10.1). It
is the Talos Go machinery client inside the controller process, so that the
decrypted artifact stays in the memory of the process that decrypted it
**(choice §10.8)**. E3 saw the same RPC outcomes through the machinery and
through `talosctl` for the version read, configuration read, no-reboot dry run
and label-patch apply (E3 §4.3, §6.2). Every E4 dispatch row used `talosctl`
with `--file <artifact>` (DS §2.1), so the selection holds on a condition: DS
rows 001–005, 012–017 and 022 are re-run through the machinery client and
reach the same outcomes before this contract's dispatch is accepted. The
fallback is the pinned `talosctl` subprocess with the artifact passed through
an inherited descriptor, never a named file or an argument, as for the
compiler's fallback. That is not E4's channel either, so the fallback carries
the same condition: the same rows re-run through it.

The plan binds the **route**: through the control plane's endpoint with the
worker as target node, or to the worker's own endpoint. Both are allowed, as E4
exercised both (DS §2.1, row 015) **(choice §10.9)**. The late landings were all
through the control plane's proxy; the one row sent to the worker's endpoint
did not land late, and whether a dial failure there proves no send was not
tested (DS §4.4). The route therefore changes no accounting rule (§5.2).

A Talos response or error can embed the configuration or its diff (E3 §5.4,
§7). The controller records response text on the timeline only through the
compilation contract's redaction (§8.3 there), which withholds
configuration-bearing output it cannot positively recognise, failing closed.
The response's class (gRPC code, or transport outcome) is always recorded.

## 4. Operation timeline and states

Design: [§12.5](../design/Talos_Configuration_and_Machine_Management_Design.md#125-durable-operations-and-uncertain-outcomes),
[§15.2](../design/Talos_Configuration_and_Machine_Management_Design.md#152-operation-timeline).

An operation exists from its plan's dispatch commitment (§2, §3.2), except an
`adopt` operation: the adoption record of an `adopt` plan creates it directly in
`completed` (§6.3). It holds no scope or rollout slot and has no attempt, and
its `completed` rests on the adoption record's observation, not on a
completion observation of a sent artifact; none of the rows below after the
first two applies to it. An operation's one
durable timeline links, before its own entries, the plan and approval,
plan-time evidence and the execution-time evidence the commitment relied on;
it then holds ownership transitions, dispatch commitment, request attempts,
responses, reconnects, revocations and cancellations, accounting,
postconditions and final classification. State changes are append-only facts
with a current projection. §4.1 specifies the entries.

| State | Meaning |
| --- | --- |
| `committed` | The commitment transaction created the operation; the machine scope and a rollout slot are held. |
| `sending` | An attempt is recorded; the request is in flight or its outcome is unknown. |
| `verifying` | The request was accepted; the manager is collecting identity, digest and health evidence. |
| `completed` | Terminal. Postconditions prove the bound artifact is applied and healthy; `Applied` is updated. For an `adopt` operation: the adoption record is committed (§6.3). |
| `rejected` | Terminal. Every recorded attempt has a recorded, definitive Talos response of a class proven to precede any mutation (§5.1). |
| `failed` | Terminal. At least one attempt was recorded, every attempt is accounted for, and a completion observation contradicts the postconditions, whether a request changed the machine wrongly or no request took effect. `Applied` is not updated. |
| `cancelled` | Terminal. No attempt was ever recorded. This is never an undo of remote work. |
| `unresolved` | Evidence cannot yet establish a terminal state or safe retry. Scope and slot stay held. |

Before commitment there is no operation: a plan that is revoked, cancelled or
expires first ends in the plan state of that name (§2; DS row 002).

| From | To | Condition | Evidence (§9.1) |
| --- | --- | --- | --- |
| (none) | `committed` | The §3.2 transaction succeeds for an `approved` plan. | DS rows 001, 009, 010 |
| (none) | `completed` | The adoption record of an `adopt` plan (§6.3); such an operation takes no other state. | none |
| `committed` | `sending` | The §3.3 attempt transaction succeeds; the controller then sends. | DS rows 001, 007 |
| `committed` | `unresolved` | Takeover (§3.4); or, after commitment, a revocation, cancellation, the plan's expiry or a closed scope gate; or any other refusal of the attempt transaction by comparison 1, 2, 3 or 6. | DS rows 003, 004, 007, 019 |
| `sending` | `verifying` | An acceptance response is recorded. | DS rows 001, 016 |
| `sending` | `rejected` | A definitive pre-mutation rejection is recorded for this attempt, and every earlier attempt has one too. | DS row 022 |
| `sending` | `unresolved` | Lost response, transport failure or timeout, or takeover; a recorded response of any other class; or a definitive rejection while an earlier attempt has none, which accounts for this attempt only. | DS rows 012–015, 020 |
| `verifying` | `completed` | Postconditions established by a completion observation, and every recorded attempt accounted for. | DS rows 001, 017 |
| `verifying` | `failed` | A completion observation contradicts the postconditions, and every recorded attempt is accounted for. | none |
| `verifying` | `unresolved` | Postconditions not established before the recorded verification deadline, an attempt is not accounted for, or takeover. | DS row 021 (takeover) |
| `unresolved` | `completed` | Postconditions later established by a completion observation, and every recorded attempt accounted for. | DS rows 020, 021; capture 1 row 012 |
| `unresolved` | `failed` | A completion observation contradicts the postconditions, every recorded attempt is accounted for, and the operation is not classified safe to retry (§5). | DS row 013, on untrue accounting |
| `unresolved` | `rejected` | A delayed definitive pre-mutation rejection is recorded, and with it every recorded attempt has one. An attempt that was accepted, or whose outcome is unknown, rules `rejected` out. | none |
| `unresolved` | `sending` | Classified safe to retry (§5) and the §3.3 attempt transaction succeeds. | DS rows 012, 014, 015, 019 |
| `unresolved` | `cancelled` | No attempt transaction ever committed for this operation, and either none can (the approval or its identity is revoked, the approval is from an earlier recovery epoch, the plan expired or is cancelled) or `recovery-admin` resolves it so on a recorded reason. | DS rows 003, 004 |

No other transition is valid. A takeover of an `unresolved` operation changes
its owner and leaves its state. Recovery-mode entry takes over every
non-terminal operation, each of which is owned, so it reaches `unresolved`
through the takeover rows (§7.2). The machine scope and rollout slot are
released only on entering a terminal state.

The controller makes each transition that evidence determines. Where evidence
cannot, only `recovery-admin` decides: it records that an attempt is accounted
for (§5.2) and resolves an `unresolved` operation, only into one of the listed
targets, recorded with the deciding identity, its role and the evidence relied
on (design §13.7 item 5 and its derived note). No other role does either.

A **completion observation** (purpose `completion`) is taken after every
recorded attempt of the operation is accounted for, and recorded on its
timeline. It records the machine identity, assignment revision, running Talos
version, configuration digest, machine-configuration resource version and the
bound health results. Values matching the bound postconditions establish them
and yield `completed`; a value that contradicts them, such as another digest or
a failed bound health check, yields `failed`; an observation that could not
read a value does neither. An observation ordered before the last accounting,
including one ordered after the attempt record but before the attempt's
request settled, or one not tied to this operation, never completes or fails
it: a stalled executor may send after it, and the apply may change or degrade
the machine. "Ordered" is by timeline revision under §4.1's ordering rule. A
timeout is not proof of failure, completion or retry permission.

An observation ordered after an attempt does not show that the attempt's
request has executed or can no longer execute. The scope is therefore released
only when every recorded attempt is **accounted for**, meaning one of:

- its response is recorded on the timeline (DS rows 016, 022); or
- `recovery-admin` has recorded an accounting decision under §5.2.

An executor's exit, a killed client or a transport timeout is not accounting
(DS §4.4, §8). A matching digest is not accounting either, even when the digest
differed before the attempt: direct Talos access can apply the same artifact
independently while the attempt's request is merely held. An operation with an
unaccounted attempt stays `unresolved` with its scope held, however well the
machine's state matches, because releasing the scope would let a newer
operation be overwritten by the late request (DS row 011).

### 4.1 Timeline content

Design: [§15.2](../design/Talos_Configuration_and_Machine_Management_Design.md#152-operation-timeline),
[§13.6](../design/Talos_Configuration_and_Machine_Management_Design.md#136-audit).

Every entry records its kind, time (database clock), the acting identity and
role, or the controller instance, owner generation and epoch, and a revision.
Required entries:

| Entry | Content |
| --- | --- |
| Plan | the binding of §2, the plan-time evidence reference, creator and role |
| Approval | plan revision, approver, role, epoch, self-approval mark (§2) |
| Revocation, identity revocation, cancellation | what it names, who, role |
| Observation | purpose (`evidence`, `completion`, `recovery`, `drift`, `restoration`), identity, assignment evidence, running Talos version, configuration digest, machine-configuration resource version, health results, or which values could not be read |
| Use-time check | each dependency checked, its result, under which identity |
| Commitment | the operation created, the §3.1 evidence it links, owner, generation, epoch, comparisons passed |
| Refusal | the transaction and the comparison that failed, by number |
| Attempt | attempt id, owner token, route, transport and verification deadlines; for a retry, the classification revision it is bound to |
| Response | attempt id, class (acceptance, gRPC code, transport outcome), redacted text or the withheld notice (§3.5) |
| Ownership transition | from and to owner, generation, epoch, reason |
| Accounting | attempt id, basis (a response, or a §5.2 decision with its recorded facts), decider and role |
| Classification | the outcome of §5 and the entries relied on, by revision |
| `Applied` change | from, to, digest, source (operation or adoption record) |

Plan entries, from creation to commitment or a terminal plan state (§2), and
machine-scope facts are recorded on the machine's timeline beside its
operations' entries: drift records and their resolution, freezes and unfreezes,
adoption records, recovery-mode entry, scope states and scope release (§6,
§7).

**Ordering.** Every entry's revision, for a plan, an operation or a
machine-scope fact alike, is allocated from one per-machine counter under the
machine row's lock (on PostgreSQL, the row locked `FOR UPDATE`), so that
revision order within a scope is commit order. "After" in this contract means a
higher revision in that order. E4's prototype took an observation's basis as
the highest committed revision read without the lock, which is unsound under
concurrent writers: an accounting could commit after an observation that read
before it ([DS §7](../design/research/20260925-dispatch-safety.md#7-limits)).
Per-operation allocation would not order a `drift` observation against an
adoption approval or a drift record, which §6.3 needs. The counter and its
schema are persistence's; see `persistence-api.md`.

**Linking.** From an operation, a reader reaches its plan, approval, every
observation it relied on, every attempt and response, every accounting and the
outcome; from a machine, every operation, drift record, adoption record and
recovery epoch that affected it. A drift whose observed digest equals the
artifact digest of an operation that was accounted by decision (§5.2) is linked
to that operation as a possible late landing (§6.1).

**Content limits.** The timeline holds no plaintext configuration and no secret
value; response text follows §3.5, and diffs are the compilation contract's
redacted review data. The PoC deletes no timeline entry: application-controlled
deletion of database records needs the reference graph of design §7.4, which
the PoC does not build (design §7.8 item 1, derived). A restore erases every
entry after its snapshot; §7 records the restore itself on a recovery timeline
that the restored database cannot contain.

Nothing measured the timeline as a user surface: no investigation tested the
authenticated API, scoped authorization or an operation timeline
([FR §9](../design/research/20260925-feasibility-evidence-review.md#9-gaps)
item 5). The API that exposes it is persistence's (design §11.2 Operation).

## 5. Interruption and retry classification

Design: [§12.5](../design/Talos_Configuration_and_Machine_Management_Design.md#125-durable-operations-and-uncertain-outcomes),
[§12.6](../design/Talos_Configuration_and_Machine_Management_Design.md#126-offline-semantics).

Recovery examines the target and cluster rather than replaying the journal.
For each attempt, operation, mode, assignment revision and intervening state,
the controller classifies the outcome:

| Classification | Required action |
| --- | --- |
| Completed | Record completion, persist the bound release and digest as `Applied`, and release the scope and slot. |
| Safe to retry | Create a bounded retry within the original plan, expiry and unrevoked approval, admitted only by the §3.3 attempt transaction. |
| Unresolved | Preserve the assignment and stop dependent or conflicting mutations; observe further or request a specific `recovery-admin` decision. |
| Rejected | Record the response as proof of non-mutation, release the scope and slot, and require a corrected plan; the unchanged plan is not retried. |
| Failed | Record the contradicting completion observation, leave `Applied` unchanged, release the scope and slot, and require a corrective plan; any difference between the machine's state and `Applied` is then handled as drift (§6). |

Completed, safe to retry and unresolved are the design's classifications for
an outcome made uncertain by restart, ownership loss or transport failure.
`Rejected` and `Failed` cover an outcome that is known. `Rejected` needs a
definitive response received and recorded for every recorded attempt, never
when an earlier attempt was accepted or is unknown, and only of a class proven
to precede mutation (§5.1). `Failed` needs a completion observation that
contradicts the postconditions, not evidence that is merely missing, and every
attempt accounted for (§4). Every other error or gap is `unresolved`.

One case fits two rows: every attempt accounted for, none accepted, and an
observation at the pre-dispatch digest, which both contradicts the
postconditions and meets the safe-to-retry evidence. The controller classifies
it safe to retry only while the operation has attempts left under the bound,
its approval passes comparison 1 and its scope gate is open; otherwise it
classifies it `failed` **(choice §10.10)**. E4 left this to a harness flag: row
013 ended `failed`, and rows 012, 014 and 015 retried (DS §2.1). A restored
operation therefore never retries: its approval is from an earlier epoch
(§7.1).

The controller observes after a lost response, restart, ownership loss or
reconnect. Before a retry or dependent mutation it rechecks identity,
assignment, current digest, running version and relevant health. An old
operation that may have been accepted remotely blocks conflicting work until
its outcome is resolved. Idempotency keys and journals prevent duplicate
application intent in the manager, but do not provide exactly-once remote
execution (design §12.5).

### 5.1 Evidence per outcome

For `apply-config`/`no-reboot` on one worker at one assignment revision, E4
found this evidence sufficient
([DS §6.3](../design/research/20260925-dispatch-safety.md#63-criterion-3-per-operation-per-mode-per-assignment-evidence));
this contract adopts it and adds nothing it did not show:

| Outcome | Evidence required |
| --- | --- |
| Completed | every attempt accounted for; then a completion observation of the artifact's digest and the other postconditions |
| Rejected | a recorded `InvalidArgument` response to every attempt. E4 showed it before any mutation for a validation error only, with the resource version unchanged ([DS §4.6](../design/research/20260925-dispatch-safety.md#46-a-definitive-rejection-row-022)); no other code or error class is proven pre-mutation |
| Failed | every attempt accounted for, and a completion observation contradicting a postcondition. The accounting must be true, not merely recorded (DS row 013) |
| Safe to retry | every attempt accounted for, or none recorded; no accepted response; a completion or recovery observation at the pre-dispatch digest; attempts left, an approval passing comparison 1 and an open scope gate (§5); a new attempt transaction bound to the classification's revision |
| Unresolved | any attempt with neither a recorded response nor an accounting decision; or no successful completion observation by the verification deadline |
| Cancelled | after the commitment, with no attempt recorded: a revocation of the approval or of its identity, a cancellation, the plan's expiry, an approval from an earlier recovery epoch, or a `recovery-admin` resolution. Before the commitment there is no operation; the plan ends `revoked`, `cancelled` or `expired` (§2) |

Two response classes are deliberately not rejections. A dial failure
(`Unavailable`) is an unknown outcome: whether it proves no send was not tested
(DS row 015). A node refusing a configuration as not applicable in immediate
mode was seen only on dry runs (E3 §4.3); a recorded response of that kind
accounts for its attempt, but the outcome is decided by a completion
observation, not by the response.

A retry re-applies the same artifact. If an earlier, abandoned request landed
on top of it, it would apply the same configuration again, which should change
no configuration; an identical re-apply was not captured (DS §4.4, §5). A retry
is safe only as long as the accounting behind it is true (§5.2).

### 5.2 Accounting a lost response

No evidence establishes when an abandoned request can no longer land. The
executor's exit is not enough (DS §4.4, capture 1 row 012), and E4's 30 s settle
is a choice, not a bound: nothing shows how long the control plane's proxy can
hold a request (DS §6.3;
[FR §9](../design/research/20260925-feasibility-evidence-review.md#9-gaps)
item 3). Until there is such evidence, an attempt whose response was lost is
accounted only by a `recovery-admin` decision (DS §8), which may be recorded
only when all of these hold and are recorded with it:

1. the attempt's executor is stopped or taken over, so it can record no further
   attempt (§3.4);
2. at least the **settle floor** has elapsed since the latest of the attempt's
   transport deadline, the executor's stop or takeover, and the first
   successful read of the machine after that deadline. The settle floor is a
   static deployment setting of at least 30 s, E4's settle, held outside the
   application database so that a restore cannot change it
   **(choice §10.11)**. It is a floor, not a bound: the captured landing came
   about 11 s after the heal and within it, and nothing shows that every
   landing does; and
3. a recovery observation taken after the floor, with the
   machine-configuration resource version read at the start and end of the
   settle. That version moved on every landed apply in E4 (DS §2.3); a change
   during the settle is recorded, and the decision states how it is explained.

The decision records that it is a judgement, not evidence that the request can
no longer land. The residual is explicit: if the request lands after the
decision, it can overwrite work that the released scope then admitted, which is
the stale A-after-B case reached through accounting that was recorded but not
true (DS §4.4). That landing is not prevented. It is detected only as drift at
the next observation, when the observed digest differs from `Applied`, and is
linked to the operation (§4.1, §6.1).

### 5.3 Interruption points

Required interruption tests cover failure before commitment, after commitment
but before send, after remote acceptance, before response recording, and
before local completion recording. Each test must show the resulting
classification and blocked scope; a generic "mark successful" recovery action
is not valid. E4 killed its executor at each point and found the state this
contract names, with no generic resolution
([DS §4.5](../design/research/20260925-dispatch-safety.md#45-executor-kills-at-each-gate-specification-5s-interruption-points)):

| Point | E4 row | State left | Resolution under this contract |
| --- | --- | --- | --- |
| before commitment | 018 | no operation; the plan still `approved` | another controller runs the plan from §3.1 |
| after commitment, before any attempt | 019 | `committed`, no attempt | takeover; no attempt to account; a recovery observation at the pre-dispatch digest classifies safe to retry |
| accepted, response not recorded | 020 | `sending`, no response | takeover; refused while unaccounted; accounting decision (§5.2); completion observation |
| matching observation, completion not recorded | 021 | `verifying`, response recorded | takeover; a new completion observation; `completed` |

## 6. Drift handling

Design: [§12.4](../design/Talos_Configuration_and_Machine_Management_Design.md#124-drift-policy),
[§7.1](../design/Talos_Configuration_and_Machine_Management_Design.md#71-responsibility-split-and-secret-ingress),
[§9.1](../design/Talos_Configuration_and_Machine_Management_Design.md#91-adopt-an-existing-configured-cluster),
[§13.7](../design/Talos_Configuration_and_Machine_Management_Design.md#137-poc-identity-and-approval-policy).

**No feasibility evidence covers this section.** No investigation exercised
drift freeze, adoption policy or revert; E1's adoption flow covers only
extraction ordering, on the same input and function as import
([FR §6](../design/research/20260925-feasibility-evidence-review.md#6-evidence-per-property)
SP02, SP22; FR §9 item 1). Every transition below is specified, not evidenced;
§9.3 states what would close the gap.

### 6.1 Detection

Drift is an observed configuration digest that differs from the `Applied`
digest while no operation on that machine is `committed`, `sending`,
`verifying` or `unresolved`. Two look-alikes are not drift:

- `Desired` differing from `Applied` is pending convergence (§1).
- A digest mismatch while an operation holds the machine scope is evidence for
  that operation's classification (§5).

The controller observes each managed machine periodically (purpose `drift`);
the interval is open. A mismatch in a `drift` observation, or in a
`restoration` observation after a restore (§7.3 step 4), opens a **drift
record** on the machine's
timeline, naming the observation, the `Applied` digest and the observed digest,
and raises the design §15.3 digest-mismatch alert. The definition above holds
for a `restoration` observation too: while a restored operation on the scope is
still `unresolved`, the mismatch is evidence for its classification (§7.3 step
5), and the drift record opens only once that operation is terminal, at the
scope's marking (§7.3 step 6). A scope whose restored operation stays
`unresolved` is marked `unresolved` and opens no drift record yet. A machine
has at most one open drift record. A drift whose observed digest equals the artifact of an
operation accounted by decision is linked to it as a possible late landing
(§5.2).

Drift is never an automatic apply trigger, and detection does not freeze the
scope by itself **(choice §10.12)**. It needs no freeze to stop stale work: any
plan made before the drift binds the old pre-dispatch digest and fails
comparison 3 (§3.2), and any plan made after it binds the drift record and is a
revert (§6.4). Design §12.4 has the operator choose freeze, adopt or revert.

External Talos access, break-glass included, remains independent of
application approval and is reconciled as drift afterward (design §12.7,
§13.5).

### 6.2 Freeze

Any `author`, `publisher`, `approver` or `recovery-admin` may freeze a machine
scope; only an `approver` unfreezes it (design §13.7 item 6). A freeze is a
durable fact on the machine scope that closes the scope gate: an approved plan
fails comparison 6 of §3.2, and no attempt transaction
succeeds until an `approver` lifts it (§3.3). A freeze does not stop an attempt
already recorded, and it is independent of drift: it may be placed without a
drift record and outlives the record's resolution until lifted.

### 6.3 Adopt

Adoption accepts the configuration the machine now runs. It sends nothing to
the machine: dispatching the adopted release would mutate a machine whose
state is being accepted (design §12.1).

1. **Ingest.** The drifted configuration is read from the node and ingested
   through the compilation contract's pipeline as a drift adoption (§2.3
   there): known and marked secrets are extracted before any backup-visible
   write, and the exact input is encrypted as the baseline under the baseline
   key, which no runtime identity can decrypt. Adoption success, failure or
   interruption leaves no plaintext in drafts, indexes, staging, database
   writes that a base backup, log archive or storage snapshot can capture,
   responses or logs; sanitizing a record afterwards does not satisfy this
   (design §7.1; E1 §6). Which role performs this ingestion the design leaves
   open as an owner decision (design §13.7, "Limits and what stays open"); until
   the owner decides, it is `author`'s, as in persistence's interim rule (see
   `persistence-api.md`) **(choice §10.14)**. Ingestion records the baseline's
   configuration digest (§1) over the same input it encrypts (compilation
   contract §2.3 step 8). An existing-cluster import records it the same way, so
   the handover below compares against the same digest.
2. **Publish.** The sanitized document becomes the machine's new import base,
   and a release compiled from it is published (compilation contract §6, §11).
   Publication resolves nothing and authorizes nothing.
3. **Plan and approve.** An adoption is requested as a plan with operation
   `adopt` **(choice §10.13)**, created by an `author` **(choice §10.14)** and
   approved by an `approver`, as design §13.7 item 3 allows, with the
   self-approval rule. The adopt plan binds the machine, its assignment
   revision, its baseline revision (none for a machine with no `Applied`), the
   open drift record (none for a handover), the adopted release, the baseline's
   configuration digest, a maximum observation age, its expiry, the approval
   policy, and the plan revision with its creator. It has the plan states of
   §2, and any change to a bound value needs a new plan.
4. **Record.** The adopt plan's commitment is the **adoption record**
   transaction. It locks the machine row as §3.2 does, so it is ordered against
   every observation, commitment and drift record of the machine (§4.1), and it
   requires:
   1. no operation exists for the plan (§3.2 comparison 0);
   2. the plan's approval passes §3.2 comparison 1, under the same locks:
      unexpired, not revoked, its identity not revoked, and recorded in the
      current recovery epoch;
   3. the machine's assignment revision and baseline revision equal the bound
      ones, and the bound drift record is still open (for a handover, the
      machine still has no `Applied`);
   4. the machine's latest observation, by revision, was taken after the
      approval, is no older than the age the plan binds, and shows the
      machine's configuration digest equal to the baseline's and its
      assignment revision unchanged; and
   5. no operation holds the machine scope, and the scope gate is open apart
      from a freeze: adoption changes nothing on the machine, so a freeze does
      not block it, but recovery mode without a release of the scope in the
      current epoch does (§7.5).

   Instead of an operation that sends, the transaction creates the plan's
   operation of kind `adopt` directly in `completed`, with the adoption record
   as its outcome. It is never `committed`, `sending`, `verifying` or
   `unresolved`, so it never counts against comparison 4.

The record compares the observed digest with the **baseline's** digest, the
bytes read from the node, not with the recompiled release's artifact
**(choice §10.15)**. Whether an artifact compiled from an unchanged import base
reproduces the node's bytes is not evidenced, and E3 saw the renderer re-encode
a whole configuration (E3 §7); requiring equality with the artifact could make
adoption impossible.

The record sets `Applied` to the adopted release with the baseline's digest,
marked adopted by observation rather than dispatched, and confirms the adopted
release as `Desired`, selecting it if publication has not already (the
selection is persistence's), so that the machine is not at once pending
convergence towards the release it drifted from and a later plan cannot
silently undo the adoption. It advances the baseline revision (§2), so every
plan made before the adoption, such as an approved revert to the previous
release, fails comparison 2 and must be planned and approved again. It closes
the drift record. If a newer observation shows the machine has changed again,
it is the latest one, requirement 4.4 fails and the machine stays drifted. If a
revert closed the drift record first, requirement 4.3 fails, even when a later
edit restored the adopted bytes. Publishing and approving an adopted release
does not by itself resolve drift.

If the adopted release's artifact digest differs from the baseline's, the
machine is then pending convergence (§1), not drifted. Converging it is an
ordinary approved plan whose redacted whole-configuration diff shows what the
re-encoding changes (§2).

**Existing-cluster handover.** The same transaction establishes the first
`Applied` for a machine that has none, which is how existing-cluster adoption
(design §9.1) hands a machine over. Such a machine is not drifted, since there
is no `Applied` to differ from; the adopt plan is the handover authorization,
binds no drift record and no baseline revision, and compares the baseline
digest recorded by import (step 1); requirements 4.1 to 4.5 apply otherwise
unchanged. The rest of
that workflow belongs to the
[adoption work](https://github.com/ginsys/bronzeward/issues/22).

### 6.4 Revert

A revert is an ordinary plan (§2) for a machine with an open drift record,
from the selected applicable release, after checking assignment, current state
and dependencies (design §12.4). It binds the drift record and, as its
pre-dispatch digest, the drifted digest, so if the machine drifts again before
commitment, comparison 3 fails and the plan must be made again. Its approval
is recorded as approving the overwrite of configuration that no reviewer has
seen: the drifted configuration has no sanitized form unless adopted
**(choice §10.16)**. Normal approval applies, and a freeze must be lifted by an
`approver` before it can commit. A revert ends through the normal path: it
advances `Applied` when it reaches `completed`, which closes the drift record.

### 6.5 Drift states

| From | To | Condition |
| --- | --- | --- |
| none | open | A `drift` or `restoration` observation's digest differs from `Applied` with the scope free (§6.1). |
| open | open | Freeze or unfreeze; a further differing observation is recorded on the same record. |
| open | closed (adopted) | An adoption record (§6.3). |
| open | closed (reverted) | The revert operation reaches `completed` (§6.4). |
| open | closed (returned) | A later observation equals `Applied` again with no Bronzeward action, such as a manual revert; recorded as such. |

A revert that ends `failed`, `rejected` or `cancelled` leaves the record open.
An operation's own digest mismatch while it holds the scope never opens a
record (§6.1).

## 7. Recovery after management-state restoration

Design: [§14.6](../design/Talos_Configuration_and_Machine_Management_Design.md#146-explicit-recovery-mode-after-restoration),
[§14.4](../design/Talos_Configuration_and_Machine_Management_Design.md#144-recovery-dependencies),
[§7.7](../design/Talos_Configuration_and_Machine_Management_Design.md#77-poc-deployment-profile),
[§7.8](../design/Talos_Configuration_and_Machine_Management_Design.md#78-poc-retention-and-recovery-policy),
[§13.7](../design/Talos_Configuration_and_Machine_Management_Design.md#137-poc-identity-and-approval-policy).

After restoring the database, OpenBao or the credential store, the operator
enters recovery mode before normal startup (design §7.7 duty 6, §14.6). In the
PoC, entering and leaving recovery mode, releasing scopes and resolving
`unresolved` operations belong to `recovery-admin` (design §13.7 item 5).
OpenBao repairs such as unsealing, restoring, and key or policy changes stay
with the OpenBao administrator, and break-glass access with neither.

**What is evidenced and what is not.** A restore rewinds identifiers and fence
generations on both backends, and a pre-restore ownership token passes the
fence again (observed negative,
[DB §4.7](../design/research/20260924-database-semantics.md#47-s7-restored-state)
rows 027 and 055; reproduced, FR §7 R4). No investigation modelled
recovery-mode entry, executor quiescence or a restore epoch (DS §7;
[KL §6](../design/research/20260924-key-loss-restoration.md#6-limits);
FR §9 item 4). The procedure below is specified, not evidenced; §9.3 states
what would close it.

### 7.1 What a restore does to the fences

A restore erases everything after its snapshot: plans, operations, attempts,
takeovers, approvals, revocations, identity revocations, drift and adoption
records, and releases. It rewinds the counters that issued them: identifiers
drawn from database sequences are reissued, as in the prototypes, and so are
revision numbers and fence generations (DB §4.7). Persistence makes every
identifier that leaves the database random and never reissued (see
`persistence-api.md`), so under it only revision numbers and generations are
reissued; this contract does not rely on that (item 3 below). Anything outside
the database that still holds a pre-restore token or reissued number (a running
executor, a provider object, a log, a machine running an erased release's
artifact) can collide with a newly issued one. A recovery epoch kept as a
counter in the same database is rewound by the same restore (DB §9, inferred),
so "greater than any restored value" does not make it unequal to an epoch
issued after the snapshot.

This contract therefore requires from persistence, see `persistence-api.md`:

1. a **recovery epoch** identifier, issued at installation and again at each
   recovery-mode entry, that is never issued again, including after a restore
   to any earlier snapshot. This contract compares epochs for equality only
   and never orders them. A random identifier of at least 128 bits meets this
   without state outside the database **(choice §10.17)**;
2. every ownership token, approval and scope release carries the epoch in which
   it was issued, and the comparisons of §3.2 and §3.3 compare it; and
3. no rule of this contract matches a release, operation or approval across a
   restore by identifier. A machine is matched to a release by configuration
   digest only.

Approvals and ownership are then safe across a restore in this sense: an
approval or token issued before the current entry, including one the restore
erased or revived, authorizes nothing, because its epoch is not the current
one. Requests already sent are not covered by any fence (§3.4); §7.3 step 1
handles them.

### 7.2 Entry

The controller supports a **recovery start**, a startup option under which it
keeps every dispatch gate closed: it runs no executor, attempts no commitment
or attempt transaction, and serves observation, the checks of §7.3, the
recovery API and the installation-wide acts of §7.5 (drafts, ingestion,
compilation and publication), so that a `blocked` scope's exit through a new
release (§7.4) exists before any scope is released. After a restore of any of the three backup families, the
operator starts it that way, and `recovery-admin` records recovery-mode entry
through the API (design §13.7 item 5, §14.6). The start option only keeps the
gates closed; no server-side command enters recovery mode by itself
**(choice §10.18)**. Bronzeward does not claim to detect a rollback (design
§14.6): starting normally after a restore is an operator error that the epoch
check does not catch, because no entry has issued a new epoch.

The entry record states which backup families were restored, the identity of
each backup and its age, so that the pairing duties of design §7.7 (duties 3
and 4) can be checked (§7.3 step 3). Entry is one transaction that:

1. issues the new epoch;
2. closes the scope gate of every machine scope: comparison 6 fails and no
   attempt transaction succeeds until the scope is released (§7.3 step 7);
3. takes over every non-terminal operation into the new epoch, so each in
   `committed`, `sending` or `verifying` becomes `unresolved`, and each
   `unresolved` one stays `unresolved` under the new owner (§3.4);
4. marks every machine scope **pre-restore unaccounted** (§7.4): the restored
   journal can omit operations dispatched after the snapshot, so no scope is
   presumed quiet because its restored journal shows nothing
   **(choice §10.19)**; and
5. abandons every ingestion claim created before the new epoch (compilation
   contract §3.5).

Approvals from earlier epochs need no action: they fail comparison 1 from the
moment the epoch changes (§3.2).

Once the first scope is released, the operator may restart the controller
normally; comparison 6 still refuses every scope not released in the current
epoch, and recovery mode stays in effect until §7.6.

### 7.3 Procedure

1. **Quiesce.** Stop, or establish as stopped, every controller instance that
   ran against the pre-restoration state, and record the time. Stopping is what
   prevents a further attempt; the epoch refuses one from any instance that was
   missed (§7.1). Neither prevents a request already sent from landing. Every
   scope stays pre-restore unaccounted until `recovery-admin` records the §5.2
   accounting decision for it. In §5.2 condition 2 the stop of every
   pre-restoration instance takes the place of the executor's stop, and the
   settle floor runs from the latest of the three times that condition names,
   not from the stop alone. For a scope whose attempt the restored state does
   not hold, the attempt's transport deadline is unknown, and the latest time
   it could have been takes its place: the stop of the pre-restoration
   instances plus the **maximum transport deadline**, a static deployment
   setting held outside the application database like the settle floor. Plan
   creation refuses a transport deadline above it. One decision may cover many
   scopes, each with its own recovery observation.
2. **Verify** schema, revisions, releases, operation journals and provider and
   key references from the restored state. Re-record, from the operator's
   records, every identity revocation made after the restored backup, which the
   restore erased (design §13.7 item 4). A token revoked after the backup must
   not authenticate again; persistence meets this by refusing every automation
   token from an earlier epoch, so every token is reissued with the server-side
   tool, and by a denied-subject list in deployment configuration, to which
   the re-recorded identities are added (design §13.7 item 1; see
   `persistence-api.md`).
3. **Check dependencies.** Check retained dependencies (design §7.6) and the
   backup pairing of design §7.7, and test the decryption and credentials each
   scope needs (§7.4) under the actual recovery identities, stopping and naming
   what is missing
   ([KL §5.3](../design/research/20260924-key-loss-restoration.md#53-criterion-3-outcomes-prerequisites-and-stopping),
   §7 item 4). The check runs under identities that cannot create a Transit
   key: Transit creates a missing key on encrypt for an identity holding
   `create`, and a key recreated under a lost key's name is not a recovery
   path (KL §5.3, §5.4). Applying a retained artifact and regenerating a
   release need different dependencies (design §7.8, §14.4).
4. **Observe** machine identity, assignment, running version, configuration
   digest, health and cluster membership for reachable targets, after step 1
   (purpose `restoration`).
5. **Reclassify** restored pending operations under §5, never replaying a
   journal entry because it is pending. A restored operation never retries:
   its approval is from an earlier epoch (§5). One with no attempt ends
   `cancelled`; one with an attempt is accounted, by its recorded response or
   by a §5.2 decision such as step 1's, and ends `completed` or `failed` by a
   completion observation taken after that accounting.
6. **Mark** each scope `ready`, `blocked` or `unresolved`, recording the
   missing dependency or evidence (§7.4). An observed digest that the restored
   `Applied` does not explain is drift (§6), not grounds for an apply, even when
   it matches a restored release's artifact (§7.1 item 3).
7. **Release.** `recovery-admin` releases eligible scopes explicitly. The
   release is recorded in the current epoch and opens the scope gate for that
   scope only. It is not approval: a plan for the scope still needs an approval
   recorded in the current epoch (design §14.6, §13.7 item 5).
8. **Leave** recovery mode (§7.6).

A restored desired state is never proof of current machine state, and a scope
release is not blanket approval for pending mutations.

### 7.4 Scope states in recovery

| State | Meaning | Leaves by |
| --- | --- | --- |
| pre-restore unaccounted | set at entry; a request sent before the restore may still land | the step 1 accounting decision, to one of the next three |
| `unresolved` | a restored operation on the scope is `unresolved` | its resolution under §4 and §5, then re-marking |
| `blocked` | the `restoration` observation failed, or a dependency to apply the machine's `Desired` release is missing: its release record, its artifact's key version at or above the decryption floor and decryptable by the executor identity, or the executor's operation credentials (§3.1 item 2) | the dependency restored or repaired, or a newly published release selected as `Desired` whose dependencies are present; then re-marking |
| `ready` | accounted, no operation holds the scope, the dependencies above present, a `restoration` observation recorded after step 1 | release |
| released | released in the current epoch | recovery-mode exit, or a new entry |

The dependency set is the one dispatch checks at use time (§3.1), for the
release a plan would target, not the dependencies of regenerating it
**(choice §10.20)**. A scope whose `Desired` release names a key version lost
for good leaves `blocked` through a new release: ingestion, compilation and
publication stay allowed during recovery mode (§7.5), and a regenerated
release needs the source secret versions it pins, or new ones ingested (design
§14.4; KL §5.3). Every plan still repeats the check at use time (§3.1).

Only `ready` scopes may be released. A drifted `ready` scope may be released
and then handled under §6; release does not resolve drift. A freeze is
independent of these states and survives release.

### 7.5 What recovery mode allows

Recovery mode is enforced per scope, not by refusing the whole installation
**(choice §10.21)**:

| Acts | While recovery mode is in effect |
| --- | --- |
| Acts that only remove authority: approval and identity revocation, plan cancellation, freeze | always allowed, to the roles design §13.7 gives them |
| Acts that record recovery: entry, accounting decisions (§5.2), resolutions of `unresolved` operations, scope marking and release, leaving | always allowed, to `recovery-admin` |
| Drafts, ingestion, compilation and publication | allowed installation-wide: they write only to the database and the provider, and nothing reaches a machine |
| Plan creation and approval, unfreeze, adoption, and anything that could send to a machine | refused on a scope still pre-restore unaccounted |
| Commitment and attempt transactions, and adoption records | refused on every scope not released in the current epoch (comparison 6; §6.3 requirement 4.5) |
| Every act on a released scope | allowed, as outside recovery mode |

Plan creation is refused with approval on a pre-restore unaccounted scope,
since the plan's preconditions would be judged against a scope whose state is
not yet accounted. Approval on a scope that is accounted but not yet released
is allowed: it authorizes nothing until release, because comparison 6 still
refuses the commitment.

### 7.6 Leaving recovery mode

`recovery-admin` leaves recovery mode only when every machine scope is
released **(choice §10.22)**. A scope that cannot yet be released keeps the
installation in recovery mode, but blocks only itself: released scopes are
fully usable (§7.5), and a `blocked` scope has an exit through a new release
(§7.4). One case has no exit: a machine that can never be observed again
(destroyed or permanently unreachable) cannot supply the `restoration` or
recovery observation that `ready` and the §5.2 decision need, so its scope
keeps the installation in recovery mode. The PoC specifies no decommission or
exclusion act for it; that is a gap (§9.3), and until one exists the other
scopes stay fully usable because they are released individually. Leaving
never reopens a scope that was not checked. Leaving ends the recovery
timeline; the epoch stays current until the next entry.

## 8. Invariants and worked interleavings

An implementation and its reviewer can check these directly:

1. **One uncertain operation per machine.** At most one operation per machine
   coordination scope is `committed`, `sending`, `verifying` or `unresolved`.
2. **Commitment and attempt precede send.** No Talos request is sent without a
   durably recorded commitment and attempt earlier on the same timeline, and
   every attempt was admitted by an attempt transaction.
3. **`Applied` follows evidence.** `Applied` changes only on entering
   `completed`, to that operation's bound release and digest, or by an adoption
   record, to the baseline's digest. Both need an observation taken for that
   purpose: after every recorded attempt is accounted for, or after the
   adoption approval and inside the age it binds.
4. **Plans do not change.** Any change to a binding is a new plan that needs a
   new approval.
5. **Release only on a terminal state.** The machine scope and rollout slot
   are released only by `completed`, `rejected`, `failed` or `cancelled`, and
   never while a recorded attempt is unaccounted for.
6. **Absence of evidence classifies nothing.** A timeout, lost response,
   executor exit or pending journal entry alone never yields `completed`,
   `rejected`, `failed`, `cancelled` or safe to retry, and never accounts for an
   attempt.
7. **No plaintext before extraction.** Known and marked secrets never reach a
   backup-visible write in plaintext, adoption included.
8. **Nothing from an earlier epoch authorizes.** An approval, ownership token
   or scope release from before the current recovery epoch never satisfies
   §3.2 or §3.3.
9. **A held scope freezes the assignment.** A machine's assignment does not
   change while an operation on it is `committed`, `sending`, `verifying` or
   `unresolved`.
10. **No mutation through a closed gate.** No commitment or attempt
    transaction succeeds for a scope that is frozen, except the adoption
    record, the adopt plan's commitment, which changes nothing on the machine
    (§6.3 requirement 4.5). None of them, the adoption record included,
    succeeds for a scope under recovery mode without a release in the current
    epoch.
11. **Only a stated identity decides.** Every accounting decision and every
    operator resolution is `recovery-admin`'s, recorded with its basis; the
    controller decides only what evidence determines.
12. **One operation per plan, from commitment.** A plan has no operation before
    its commitment transaction and exactly one after it.
13. **Recovery mode holds each scope, not the installation.** While recovery
    mode is in effect, nothing that could send to a machine, approve or adopt
    succeeds for a pre-restore unaccounted scope, and a released scope is
    governed as outside recovery mode (§7.5).

### 8.1 Revocation racing commitment

Plan P for machine M is `approved`; the controller has recorded its §3.1
evidence for P. A revocation R of P's approval and P's commitment transaction
C, which would create operation O, race.

- **R commits first.** C fails comparison 1. No operation is created, nothing
  is sent, and P is `revoked` (DS row 002).
- **R starts while C holds the approval.** R locks the approval `FOR UPDATE`,
  so it waits for C's `FOR SHARE` read and commits after it (§2). O is
  `committed`, and O's attempt transaction then fails
  comparison 1: `committed` → `unresolved` → `cancelled`, nothing sent (DS row
  003; that R waited is inferred from the harness's record, DS §4.1).
- **R commits after C, before the attempt.** The same outcome (DS row 004).
- **R commits after the attempt transaction.** The attempt is sent, lands and
  completes. R prevents any retry and undoes nothing (DS row 005). This is the
  stated residual.
- **Without the in-transaction comparison** (control row 006), a revocation
  after a single early check is not seen and the artifact is sent.

Each conclusion is only as strong as invariant 2, which E4 showed for its own
executor only (§3.3).

### 8.2 Uncertain outcome blocks a newer plan (stale A-after-B)

Operation A (artifact *a*) for M is `sending` under owner X when the network
partitions. The controller restarts as Y and takes A over; A becomes
`unresolved`.

- A newer plan B for M cannot commit: comparison 4 fails while A is
  `unresolved` (invariant 1; DS row 010). Nothing newer than *a* can be
  dispatched while A is uncertain, so a late delivery of X's request cannot
  overwrite newer configuration. Without the scope index, E4's control row 011
  recorded both A and B `completed` with A's stale send overwriting B.
- Y refuses to resolve A while its attempt is unaccounted (DS rows 012–015). If
  X's response is recorded, A is accounted; otherwise only a §5.2 decision
  accounts for it.
- After accounting, a completion observation of *a* completes A, `Applied`
  becomes *a*, the scope is released, and B's plan, made against the old
  baseline, now fails comparison 2; a plan made afresh, B2, can proceed (DS row
  010).
- If M still reports the pre-dispatch digest, A is safe to retry only while
  attempts remain, its approval still passes comparison 1 and the scope gate is
  open; then one bounded retry runs (DS rows 012, 014, 015). Otherwise A is
  `failed` (§5, §8.4, choice §10.10).

### 8.3 Crash between commitment and send

O is `committed`; the controller X dies before its attempt transaction (DS row
019), or loses ownership there (row 007).

- Y takes O over: the generation rises and O becomes `unresolved`. If X is
  still alive, its attempt transaction fails comparison 7 and it sends nothing
  (row 007; control row 008 shows the send without the fence).
- No attempt was ever recorded, so nothing needs accounting. Y takes a recovery
  observation; at the pre-dispatch digest, O is safe to retry, and Y's first
  attempt transaction admits one attempt. One landing (rows 007, 019).
- Had the approval been revoked meanwhile, the attempt transaction would fail
  comparison 1 and O would become `cancelled`.

### 8.4 A late landing and the accounting decision

A's request goes through the control plane's proxy; the worker is partitioned;
X's client reaches its transport deadline and is killed; the partition heals.

- A is `unresolved` with an unaccounted attempt. `recovery-admin` may not
  account it on X's exit. In E4, accounting on exit alone classified row 013
  `failed` 11 s after the heal, the moment another capture's identical request
  landed; nothing landed there, so `failed` happened to be right, on evidence
  that could not tell the cases apart (DS §4.4). In the reproduction a
  request landed inside row 013's window, just before the observation, which
  was therefore right again (FR §7).
- After the settle floor (§5.2) and a recovery observation: if the observation
  shows *a*, the landing happened and A completes (capture 1 row 012). If it
  shows the pre-dispatch digest, A is retried once, provided attempts remain,
  its approval still passes comparison 1 and its gate is open; otherwise A is
  `failed` (§5).
- If the request lands after the decision, it re-applies *a*. That is harmless
  on top of A's own retry (inferred, §5.1), but if the scope has since passed to
  B and B completed, the landing reverts B. The next `drift` observation sees
  *a* where `Applied` is B's, opens a drift record linked to A, and the operator
  chooses freeze, adopt or revert (§6). The landing is detected, not
  prevented.

### 8.5 Identity revocation

Approver P approved plans Q1 and Q2. Q1's operation is `committed` with no
attempt; Q2's has an attempt recorded. `recovery-admin` records an identity
revocation of P.

- Q1's attempt transaction fails comparison 1: `unresolved`, then `cancelled`.
  An uncommitted plan approved by P cannot commit and ends `revoked`. The
  revocation locks P's principal record, so it waits for any commitment or
  attempt transaction that read it `FOR SHARE` (§2).
- Q2's recorded attempt is sent and runs to its classification. No further
  attempt of Q2 is admitted: comparison 1 refuses it, retries included
  (choice §10.6). If the attempt was accepted and a completion observation
  shows its artifact, Q2 completes; if its outcome is unknown, Q2 is
  `unresolved` and ends through a §5.2 accounting decision and a completion
  observation, `completed` or `failed`.
- Identity revocation was not measured (design §13.7); this walk-through rests
  on the comparisons, not on a row.

### 8.6 Drift: freeze, then adopt or revert

M's `Applied` is release r3 with digest d3, and plan P (r4, pre-dispatch d3)
is approved. During an incident an operator edits M with `talosctl`; a `drift`
observation reads d9. No operation holds M's scope, so drift record D opens.

- P cannot commit: comparison 3 fails, d9 ≠ d3. An `author` freezes M's scope
  while the incident continues; comparison 6 would now fail too.
- **Adopt.** The drifted configuration is ingested as a drift adoption; its
  secrets are extracted and its exact bytes encrypted as the baseline with
  digest d9. Release r5 is published from the new import base. An `author`
  creates an adopt plan binding M, its assignment and baseline revisions, D, r5
  and d9, and an `approver` approves it. A `drift` observation after the
  approval still reads d9 and D is still open, so the adoption record commits
  (the freeze does not block it, §6.3 requirement 4.5, invariant 10):
  `Applied` is (r5, d9, adopted), `Desired` is r5, the baseline
  revision advances, D closes. P now also fails comparison 2 and must be
  planned again. The freeze stays until an `approver` lifts it. If r5's artifact
  digest is not d9, M is pending convergence towards r5.
- **Revert instead.** A `publisher` plans r3 for M; the plan binds D and
  pre-dispatch digest d9, and an `approver` approves the overwrite of the unseen
  change, then lifts the freeze. The operation commits, sends and completes:
  `Applied` is (r3, d3), the baseline revision advances, D closes. Had M changed
  again to d10 before commitment, comparison 3 would have refused it.
- Neither path is evidenced (§6).

### 8.7 Restore to an older backup

Backups of all three families were taken at T0, paired as design §7.7 requires.
At T0, operation S on M3 was `sending`: its attempt was recorded and its
request sent. After T0, S's request was accepted and S completed, `Applied`
becoming *s*; release r4 was published and operation A applied it to M1,
`Applied` becoming *a*; operation C on M2 recorded an attempt whose request is
held by the control plane's proxy; approver P was identity-revoked. The
database is then restored to T0: S is `sending` again with no response, and r4,
A, C and P's revocation do not exist.

- **Entry.** The operator stops every controller instance, starts one in
  recovery start, and `recovery-admin` records entry. A new epoch E is issued;
  every scope's gate closes; every restored non-terminal operation is taken
  over into E, so S becomes `unresolved`; every scope is pre-restore
  unaccounted; ingestion claims are abandoned. Every approval, P's revived ones
  included, is from an earlier epoch and authorizes nothing.
- **A stale instance that was missed** reconnects with a pre-restore token. The
  restore reissued its generation (DB §4.7), but its token's epoch is not E, so
  comparison 7 refuses it; comparison 6 would too.
- **Step 2.** `recovery-admin` re-records P's identity revocation from the
  operator's records, and P is added to the deployment's denied subjects;
  automation tokens are reissued (§7.3 step 2).
- **Steps 1 and 4.** After the settle floor from the stop, `recovery-admin`
  records the accounting decision for M1, M2 and M3, each with its recovery
  observation; for M3 it accounts S's attempt too, whose response the restore
  erased (§5.2). `restoration` observations follow. M1 reads *a*; M1's restored
  `Applied` is the pre-A release, so a drift record opens on M1 (§6.1). r4 does
  not exist in the restored database, and M1 is matched by digest only (§7.1
  item 3). M2 reads its pre-dispatch digest; C does not exist in the restored
  journal.
- **Step 5.** A completion observation of M3, taken after that accounting,
  reads *s*: S completes and `Applied` becomes *s*. Had it read S's
  pre-dispatch digest, S would end `failed` rather than retry, since its
  approval is from an earlier epoch (§5).
- **Steps 3, 6 and 7.** M2's artifact key version is present and decryptable
  under the executor identity: M2 is `ready` and released. M1 and M3 are
  `ready` and released, and M1 is then handled under §6. Had the provider been
  restored from a backup older than the database's, against §7.7's pairing
  duty, a machine whose `Desired` release names a key version that provider
  lacks would be `blocked`, naming that version (KL §3.1 case H, §5.3), until
  the version is restored or a newly published release is selected for it
  (§7.4).
- **During recovery mode.** Drafts, ingestion and publication continue for the
  whole installation, and released scopes are fully usable (§7.5). A plan for
  M2 needs an approval recorded in E. If C's held request lands after that plan
  completes, the next `drift` observation of M2 shows it (§8.4).
- **Leave.** Once every scope is released, `recovery-admin` leaves recovery
  mode (§7.6).

## 9. Verification and evidence limits

Design: [§18.1](../design/Talos_Configuration_and_Machine_Management_Design.md#181-phase-0---evidence-before-implementation-contracts)
(E1, E3, E4, E6),
[§18.2](../design/Talos_Configuration_and_Machine_Management_Design.md#182-phase-1---configuration-control-on-an-existing-cluster).

### 9.1 Transition evidence

The issue's verification walks each transition against the dispatch mechanism
E4 proved and the selected policies:

| Transition or rule | Status | Basis |
| --- | --- | --- |
| Commitment boundary; revocation before, during and after it | evidenced | DS rows 002–006 |
| Scope index; A-after-B stopped while A is uncertain | evidenced | DS rows 010, 011 |
| Takeover fence against a stale owner | evidenced, database only | DS rows 007, 008; DB §4.4 |
| Second executor for the same plan | evidenced | DS row 009 |
| Uncertain send held `unresolved`; refusal while unaccounted | evidenced | DS rows 012–015, 020 |
| Accounting of a lost response | **unsupported**: no bound on late landing; operator decision (§5.2) | DS §6.3; FR §9 item 3 |
| Retry after accounting | evidenced for the accounting E4 made | DS rows 012, 014, 015, 019 |
| `rejected` on `InvalidArgument` | evidenced for a validation error only | DS row 022 |
| `failed` | only on untrue accounting | DS row 013 |
| Reconnects during recording and verification | evidenced | DS rows 016, 017 |
| Interruption at each gate | evidenced | DS rows 018–021 |
| Plan expiry, observation age, contradicting observation, attempt bound | **unsupported**: not exercised | DS §7 |
| Plan cancellation, before and after commitment | **unsupported**: no row cancelled a plan | DS §4.1 |
| Assignment change refused while the scope is held (invariant 9) | **unsupported**: not exercised | none |
| Scope gate (comparison 6) under a freeze or recovery mode | **unsupported**: in the prototype's code, exercised by no row | DS §7 |
| Takeover at controller start | **unsupported**: E4's takeovers were harness commands | DS §2.1 |
| Safe retry against `failed` on a pre-dispatch observation (§5) | **unsupported**: a harness flag in E4 | DS §2.1 |
| Identity revocation, including its refusal of retries | **unsupported**: not measured | design §13.7 item 4 |
| Observation ordering under concurrent writers | **unsupported**: prototype basis unsound | DS §7 |
| Machinery Talos client | **unsupported**: E4 used `talosctl` | §3.5 |
| Drift freeze, adoption record, revert | **unsupported**: no investigation | FR §9 item 1 |
| Recovery-mode entry, quiescence, epoch, per-scope gating | **unsupported**: not modelled; fences rewind | DB §4.7; FR §9 item 4 |
| Recovery dependency check | evidenced for eligibility, not application | KL §5.3, §6 |
| Timeline as a usable surface | **unsupported**: not tested | FR §9 item 5 |

Everything evidenced holds within the limits stated at the top: one operation,
mode, worker, assignment revision and Talos version; PostgreSQL only; rollout
limit one; late landing observed through the control plane's proxy.

### 9.2 Required verification

An implementation of this contract must show, each with a control that can
fail:

- DS's rows 001–023 re-run through the implementation's own controller and
  Talos client (§3.5), including its controls 006, 008 and 011;
- that it sends only after its attempt transaction commits, on every path;
- each lock of §3.2 and §3.3, with the unlocked control showing the race
  (design §7.7 consequences);
- plan expiry, observation age, a contradicting newer observation and the
  attempt bound, each refusing;
- plan cancellation before commitment and after it with no attempt;
- an assignment change refused while an operation holds the scope;
- comparison 6 refusing under a freeze and under recovery mode without a
  release;
- takeover at controller start of every operation another instance owns;
- the §5 precedence: a retry admitted only with attempts left, a passing
  approval and an open gate, and `failed` otherwise;
- identity revocation before commitment, after it with no attempt, and after an
  attempt, with a retry refused;
- observation ordering under a concurrent accounting transaction (§4.1);
- drift detection, freeze, adoption record (success, stale observation,
  changed-again machine, revoked approval, changed baseline revision, drift
  record closed by a revert) and revert (success, re-drift before commitment),
  with E1's leak screen over adoption's backup-visible surfaces;
- recovery-mode entry after a database restore to a snapshot older than a
  takeover, an approval, a revocation and an attempt: a stale instance refused
  by the epoch, every scope pre-restore unaccounted, pre-entry approvals
  refused, a restored operation not retried, approval refused on a
  pre-restore unaccounted scope, release refused for a scope not `ready`, a
  released scope usable while another is not, and a `blocked` scope cleared by
  a new release; and
- the complete existing-cluster slice of design experiment E6.

### 9.3 Gaps carried, and what would close them

These are the evidence review's gaps that this contract must address
(FR §9 items 1, 3, 4 and 5). Each is specified conservatively here; none is
closed.

1. **Drift freeze, sanitized adoption and approved revert** (item 1). Closed by
   an E6-style run on the fixture: an out-of-band `talosctl` change detected
   and frozen; a drift adoption through the compilation pipeline with E1's
   screen over every backup-visible surface on success, rejection and
   interruption; an adoption record refused on a stale or changed observation;
   a revert that completes and one refused by re-drift; and a measurement of
   whether an artifact compiled from an unchanged import base reproduces the
   baseline digest (§6.3).
2. **A bound on when an abandoned request can no longer land** (item 3). Closed
   by evidence of the proxied request's lifetime: the Talos control plane
   proxy's forwarding and cancellation behaviour, established from its source
   and then measured across partition lengths, pauses and connection reuse,
   with enough captures to state a distribution rather than one landing; and
   whether a dial failure proves no send. Until then accounting is an operator
   decision (§5.2), and a late landing is detected as drift, not prevented.
3. **Recovery-mode entry and executor quiescence after restoration** (item 4).
   Closed by a restoration run on the fixture: a database restored to a
   snapshot older than a takeover, an approval, a revocation and a held
   attempt, with a missed stale instance; showing the epoch refusing it, entry
   gating every scope, the held request's landing detected, and the scope
   states, per-scope rules and release of §7.4 and §7.5. DB §4.7 closes only
   the negative half: fences alone do not survive a restore.
4. **The operation timeline** (item 5's timeline). Closed by the E6 slice
   exercising the timeline as an operator reads it: approval, commitment,
   attempts, observations, accounting and outcome linked for one change, one
   interruption, one drift and one restoration, with the timeline's response
   text passed through the compilation contract's redaction.

Narrower gaps: plan expiry, observation age and the attempt bound (DS §7);
plan cancellation, the assignment-change refusal, comparison 6 and takeover at
controller start (§9.1);
observation ordering under concurrency (DS §7); `InvalidArgument` beyond a
validation error, and every other response class (DS §4.6); identity
revocation (design §13.7); the machinery client (§3.5); the backup age
combinations g1/g2/g1 and g2/g1/g2, a restore onto a new OpenBao cluster and
token expiry across a restore (KL §6). A specification gap, not an evidence
one: no act decommissions or excludes a machine that can never be observed
again, so its scope keeps the installation in recovery mode (§7.6).

Until these close, an implementation must expose unresolved outcomes and stop
conflicting work rather than claim safe retry beyond §5.1, exactly-once
execution, universal stale-worker prevention, a bound on late landing, or
detection of an out-of-band change overwritten inside the §3.3 residual window.

The Upgrade/LifecycleClient compatibility deferral remains explicit: this
contract does not claim full E3, upgrade support or lifecycle execution.

## 10. Choices for owner review

Each settles, for the stated reason, a question that neither the design nor the
evidence settles, consistently with the design. Most take the most
conservative option; those that do not say so. Each is marked in place as
**(choice §10.n)**.

1. **Ignore is not a PoC drift policy** (§1, §6). Alternative: support design
   §12.4's bounded Ignore. Which role may ignore is an owner decision the design
   leaves open (design §13.7, "Limits and what stays open"), and design §18.2
   does not require Ignore.
2. **Configuration digest is SHA-256 over the normalized read-back** (§1). E4
   and the fixtures measured it; compilation records it for artifacts and
   baselines (compilation contract choice §16.26). Alternative: a keyed digest
   under the compilation contract's HMAC key, which removes any guessing oracle
   at the cost of a provider call per observation and an unevidenced primitive.
3. **The operation is created by the dispatch commitment; before it the plan is
   the handle** (§2, §3.2). The design (§11.1, §12.5) does not settle when.
   It keeps comparison 0 as E4 ran it (DS row 009) and gives no operation to a
   change that was never dispatched. Alternative: create the operation with
   the plan, and make the commitment its conditional `approved` → `committed`
   transition.
4. **Self-approval marked for any contained authored revision, however old,
   and for a token's recorded responsible human; "undetermined" only as a
   guard** (§2). Alternative: mark only content changed in this release, and
   ignore automation tokens; fewer marks, less visible.
5. **Rollout limit fixed at one** (§3.2). Only limit one was exercised.
   Alternative: design §12.3's configurable worker concurrency.
6. **Identity revocation refuses every later attempt, retries included**
   (§3.3). Design §13.7 item 4 invalidates only approvals no attempt has used;
   a retry is a new send under the revoked identity's authority. Alternative:
   let an approval already used by an attempt stay valid for that operation's
   bounded retries, unless the approval itself is revoked.
7. **Takeover at controller start and on `recovery-admin` request only; no
   timer** (§3.4). Alternative: a lease that expires into takeover, which E4
   did not build and which cannot stop a sent request either.
8. **Machinery Talos client in process, conditional on re-running DS rows; the
   fallback under the same condition** (§3.5). Chosen to keep the decrypted
   artifact in one process, not as the most conservative option: neither the
   client nor the fallback's descriptor channel is what E4 measured.
   Alternative: the pinned `talosctl` subprocess with `--file`, as E4 ran it,
   which puts the plaintext artifact in a named file.
9. **The route is bound in the plan; both E4 routes are allowed** (§3.5).
   Chosen for route flexibility, not as the most conservative option:
   allowing both keeps the control plane's proxy, through which every late
   landing came.
   Alternative: the worker's endpoint only, which avoids that proxy but has one
   row of evidence.
10. **On a pre-dispatch observation after full accounting, retry only while
    attempts remain, the approval passes and the gate is open; otherwise
    `failed`** (§5). E4 left it to a harness flag. Alternative: always
    `failed`, requiring a new plan; or stay `unresolved` until the gate
    reopens, which keeps the scope held.
11. **Accounting a lost response needs a `recovery-admin` decision after a
    settle floor of at least 30 s, held outside the database** (§5.2).
    Alternatives: no floor, leaving it to judgement; a longer floor; or a
    supervisor time bound, which DS §8 says would need its own evidence.
12. **Drift detection does not freeze by itself** (§6.1). Design §12.4 reports
    and lets the operator choose. Alternative: freeze on detection.
13. **An adoption is requested as a plan with operation `adopt`, whose
    commitment is the adoption record** (§6.3). It reuses the plan binding,
    approval, revocation and epoch rules. Alternative: a separate adoption
    approval resource with its own rules.
14. **`author` performs the ingestion that feeds an adoption and creates the
    adopt plan; `approver` approves it** (§6.3). Interim: the design leaves the
    ingestion role open as an owner decision (design §13.7, "Limits and what
    stays open"). Alternative: `publisher`, which design §13.7 item 2 gives
    plan creation, or a new privileged-ingestion role.
15. **The adoption record compares with the baseline's digest, not the
    recompiled artifact's** (§6.3). Alternative: require the artifact to
    reproduce the node's bytes, as the prior draft did; unevidenced, and E3's
    re-encoding suggests it may never hold.
16. **A revert's approval is recorded as approving an unseen overwrite** (§6.4).
    Alternative: an ordinary plan with no such mark.
17. **The recovery epoch is a never-reissued random identifier compared for
    equality** (§7.1). Alternative: a counter held outside the database, which
    another restore could also rewind.
18. **Recovery start keeps the gates closed; entry is always the
    `recovery-admin` API act; no rollback detection** (§7.2). Alternatives:
    entry by a server-side command before the controller starts; or detection
    through a high-water mark held outside the database, which this contract
    does not specify.
19. **Every scope is pre-restore unaccounted at entry** (§7.2). Alternative:
    only scopes with restored non-terminal operations, which misses operations
    dispatched after the snapshot.
20. **A scope is `blocked` on the dependencies to apply its `Desired` release,
    not those to regenerate it** (§7.4). A new release clears it. Alternative:
    release a scope with the missing dependency recorded, relying on the
    use-time check of each plan.
21. **Recovery mode is enforced per scope** (§7.5): authority-removing and
    recovery acts always; drafts, ingestion and publication installation-wide;
    approval, adoption and dispatch refused on a pre-restore unaccounted scope;
    released scopes fully usable. Alternative: refuse every mutation
    installation-wide except those acts until exit, which with choice §10.22
    lets one scope hold the whole installation.
22. **Leaving recovery mode needs every scope released** (§7.6). Alternative:
    leave and freeze the unreleased scopes, which would hand their unfreezing
    to `approver`, outside the recovery role.

## 11. Traceability

| Clause | Design | Evidence |
| --- | --- | --- |
| §1 operation, exclusions | §12.2, §18.2 | DS §6.3; [E3 §4.3](../design/research/20260925-talos-compatibility.md#43-operationrpc-compatibility-against-v1136), [E3 §6.3](../design/research/20260925-talos-compatibility.md#63-criterion-3-unsupported-combinations-and-the-deferred-lifecycle-tests) |
| §1 state values, digest | §12.1 | [Fx §4](../design/research/20260919-investigation-fixtures.md#4-expected-and-observed) check 3; DS §2.3 |
| §2 plan binding | §12.2, §12.7 | [KL §7](../design/research/20260924-key-loss-restoration.md#7-recommendation) item 1; [E3 §7](../design/research/20260925-talos-compatibility.md#7-limits) |
| §2 plan states | §11.1, §12.5 | [DS §4.1](../design/research/20260925-dispatch-safety.md#41-revocation-around-the-commitment-boundary-criterion-1) row 002, [DS §4.2](../design/research/20260925-dispatch-safety.md#42-ownership-loss-and-a-second-executor-criterion-2) row 009 |
| §2 approval, revocation, cancellation | §13.7 items 2–4, 6 | none: policy (FR §9 item 5); DS row 003 for the approval lock |
| §3.1 execution-time evidence | §7.6, §7.8, §12.7 | [KL §5.2](../design/research/20260924-key-loss-restoration.md#52-criterion-2-applying-is-not-regenerating-and-ciphertext-is-not-executability) |
| §3.2 commitment | §12.7, §7.7 | [DS §4.1](../design/research/20260925-dispatch-safety.md#41-revocation-around-the-commitment-boundary-criterion-1), [DS §4.3](../design/research/20260925-dispatch-safety.md#43-the-machine-scope-and-a-after-b-criterion-2); [DB §4.2](../design/research/20260924-database-semantics.md#42-s2-all-or-nothing-publication), [DB §4.3](../design/research/20260924-database-semantics.md#43-s3-unique-operation-intent) |
| §3.3 attempt, residual | §12.7, §13.7 item 4 | DS §4.1, [DS §4.4](../design/research/20260925-dispatch-safety.md#44-uncertain-sends-reconnects-and-accounting-criteria-2-and-3), [DS §6.2](../design/research/20260925-dispatch-safety.md#62-criterion-2-uncertain-sends-stale-ownership-a-after-b); [DB §4.4](../design/research/20260924-database-semantics.md#44-s4-ownership-transitions); [FR §7](../design/research/20260925-feasibility-evidence-review.md#7-reproduction-of-the-disputed-results) |
| §3.4 takeover fence | §12.5 | [DS §4.2](../design/research/20260925-dispatch-safety.md#42-ownership-loss-and-a-second-executor-criterion-2), [DS §8](../design/research/20260925-dispatch-safety.md#8-recommendation); DB §4.4 |
| §3.5 client, route, response text | §7.1, §12.2 | E3 §4.3, [E3 §6.2](../design/research/20260925-talos-compatibility.md#62-criterion-2-subprocess-against-machinery-and-the-structural-reference-evidence), E3 §7; DS §2.1, §4.4 |
| §4 states, accounting | §12.5, §13.7 item 5 | DS §4.4, [DS §4.5](../design/research/20260925-dispatch-safety.md#45-executor-kills-at-each-gate-specification-5s-interruption-points), [DS §6.3](../design/research/20260925-dispatch-safety.md#63-criterion-3-per-operation-per-mode-per-assignment-evidence) |
| §4.1 timeline | §13.6, §15.2, §7.8 | [DS §7](../design/research/20260925-dispatch-safety.md#7-limits); [FR §9](../design/research/20260925-feasibility-evidence-review.md#9-gaps) item 5 |
| §5 classification | §12.5, §12.6 | DS §6.3, [DS §4.6](../design/research/20260925-dispatch-safety.md#46-a-definitive-rejection-row-022); E3 §4.3 |
| §5.2 accounting a lost response | §12.5 | DS §4.4, §6.3, §8; FR §7; FR §9 item 3 |
| §6 drift, adopt plans | §12.4, §7.1, §9.1, §12.1, §13.7 items 3 and 6 | none: [FR §6](../design/research/20260925-feasibility-evidence-review.md#6-evidence-per-property) SP22, FR §9 item 1; extraction only, E1 §6; E3 §7 |
| §7 recovery, per-scope rules | §7.7, §7.8, §13.7 items 1, 4 and 5, §14.4, §14.6 | [DB §4.7](../design/research/20260924-database-semantics.md#47-s7-restored-state), [DB §9](../design/research/20260924-database-semantics.md#9-hand-off); [KL §5.3](../design/research/20260924-key-loss-restoration.md#53-criterion-3-outcomes-prerequisites-and-stopping), [KL §6](../design/research/20260924-key-loss-restoration.md#6-limits); FR §9 item 4 |
| §8 interleavings | §12.5, §12.7, §13.7 | DS rows cited in each walk-through |
| §9 gaps | §18.1, §18.2 | FR §9, [FR §10](../design/research/20260925-feasibility-evidence-review.md#10-recommendations) |
