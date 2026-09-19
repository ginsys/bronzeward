# Execution and recovery contract

This document specifies the first-milestone execution boundary for
configuration control on an existing cluster, as required by
[Specify execution and recovery](https://github.com/ginsys/bronzeward/issues/19).
It is derived from the [current design](../design/Talos_Configuration_and_Machine_Management_Design.md)
§§7.1, 7.4, 7.6, 12.1–12.7, 14.4 and 14.6, the E1, E4 and E6 experiments in
[§18.1](../design/Talos_Configuration_and_Machine_Management_Design.md#181-phase-0---evidence-before-implementation-contracts)
and the acceptance in
[§18.2](../design/Talos_Configuration_and_Machine_Management_Design.md#182-phase-1---configuration-control-on-an-existing-cluster).
Each section names the design text it refines. It is a PoC contract, not
evidence that the mechanisms have passed E4 or E6.

## 1. Supported operation and state values

Design: [§12.1](../design/Talos_Configuration_and_Machine_Management_Design.md#121-desired-applied-and-observed-state),
[§12.2](../design/Talos_Configuration_and_Machine_Management_Design.md#122-plan-before-apply).

The PoC supports one operation class: applying a published, encrypted
configuration artifact to an existing worker with Talos `no-reboot` mode.
Publication, observation and recovery are separate capabilities. Bootstrap,
reset, wipe, upgrade, reboot, `try`, `staged`, remote transports and
managed-cluster etcd recovery are out of scope until their operation-specific
safety evidence exists. The design's bounded **Ignore** drift policy is also
out of PoC scope; §6 specifies freeze, adopt and revert only.

The manager stores three distinct values:

| Value | Meaning |
| --- | --- |
| Desired | The release the application database records as selected for the machine. Selection is not authorization; approval belongs to a plan (§2). |
| Applied | The release and artifact last verified as successfully applied. |
| Observed | The latest machine-reported identity, running version, configuration digest and health evidence, with its observation revision and time. |

`Applied` changes only when an operation reaches `completed` (§4), and then to
that operation's bound release and artifact. An RPC response alone does not
update `Applied`. `Desired` differing from `Applied` is pending convergence,
not drift (§6). A newer observation does not invalidate a plan unless it
contradicts one of the plan's declared preconditions; observation freshness at
dispatch is governed by §3.

## 2. Immutable plan and approval binding

Design: [§12.2](../design/Talos_Configuration_and_Machine_Management_Design.md#122-plan-before-apply),
[§12.7](../design/Talos_Configuration_and_Machine_Management_Design.md#127-application-approval-and-dispatch-boundary).

Planning resolves a published artifact. Dispatch never re-renders a draft or
silently substitutes a newer artifact. The immutable plan binds:

- plan, release and exact per-machine artifact identities;
- renderer/provenance and all secret and encryption dependency versions;
- machine identity and assignment revision;
- operation `apply-config`, mode `no-reboot` and every operation parameter
  value that reaches the Talos request;
- expected preconditions, including the maximum age of the execution-time
  observation and the validity window of the use-time dependency check (§3),
  and
  expected postconditions;
- rollout scope and limits, expiry, idempotency key and approval policy;
- the plan revision and authorization identity.

The plan also carries **plan-time evidence**: the redacted diff, the upstream
validation result and any available dry-run evidence. Plan-time evidence
informs review and approval. It is never a substitute for the execution-time
checks against live state in §3, and the timeline records the two separately.

The plan is invalid if any dispatch-relevant binding changes, including the
artifact, assignment, operation, mode, a parameter value, a relevant
precondition, expiry or approval. The executor must replan and obtain approval
again rather than repairing the plan in place.

Approval authorizes exactly this binding. Publication and a green dependency
retention check never authorize dispatch. The trusted application boundary
enforces authorization; the provider does not. Approval self-use and
multi-party rules remain policy inputs, not implicit guarantees of this PoC.

## 3. Dispatch commitment

Design: [§12.7](../design/Talos_Configuration_and_Machine_Management_Design.md#127-application-approval-and-dispatch-boundary),
[§12.5](../design/Talos_Configuration_and_Machine_Management_Design.md#125-durable-operations-and-uncertain-outcomes),
[§12.3](../design/Talos_Configuration_and_Machine_Management_Design.md#123-rollout-policy),
[§7.4](../design/Talos_Configuration_and_Machine_Management_Design.md#74-publication-without-distributed-transactions).

The database and the external systems do not share a transaction. Commitment
therefore has two phases with different guarantees, and a residual window that
this contract states rather than hides.

### 3.1 Execution-time evidence, gathered before the transaction

The executor gathers and durably records on the operation timeline:

1. a fresh machine observation taken for this dispatch: identity, assignment,
   configuration digest, and the health and capacity evidence named by the
   plan's preconditions, with its observation revision and time; and
2. a use-time check, with its time, of the dependencies dispatch itself needs
   under the executing identity: decryption of the bound artifact and the
   operation credentials for the target.

Item 2 is deliberately narrow. The executor holds scoped artifact decryption
and operation credentials, not compiler-level secret access. Source secret
versions are tracked by metadata-only retention checks
([§7.6](../design/Talos_Configuration_and_Machine_Management_Design.md#76-metadata-only-dependency-checks));
losing one can block regeneration but does not make a retained, decryptable
artifact inapplicable, and is not a dispatch precondition.

A stored observation older than the plan's maximum age never satisfies item 1,
whatever it says. A machine that cannot be observed cannot be dispatched to.

### 3.2 The commitment transaction

The database transaction that creates the durable dispatch intent is the
**dispatch commitment boundary**. It compares database facts only, atomically:

1. the plan is unexpired, approved and its approval is not revoked;
2. the artifact, assignment, operation, mode and parameter bindings are
   unchanged;
3. the evidence recorded under §3.1 belongs to this operation, satisfies the
   plan's preconditions, is inside its bound maximum age or validity window,
   and no newer observation of the machine contradicts it;
4. this operation takes the machine's coordination scope, and no other
   operation on that scope is `committed`, `sending`, `verifying` or
   `unresolved`; and
5. this operation takes a slot in the plan's rollout scope, counting every
   operation of that scope in `committed`, `sending`, `verifying` or
   `unresolved` against the bound rollout limit.

If any comparison fails, nothing is committed and nothing is sent.

The machine scope also protects the binding it was taken for. Any transaction
that changes a machine's assignment must check that machine's coordination
scope and is refused while an operation on it is `committed`, `sending`,
`verifying` or `unresolved`. Detecting a changed assignment revision only
during verification would be too late: the artifact for the old revision
would already have reached the machine.

The PoC
default rollout limit is one. Drain does not apply to a `no-reboot` apply; the
cluster health and capacity gate is a bound precondition checked under item 3.

### 3.3 After commitment

The owning executor durably records an attempt identity before each send; the
operation timeline therefore shows the commitment and the attempt before any
Talos request. The first attempt is authorized by the commitment transaction.
Every later attempt is recorded by its own **retry transaction**, which
repeats comparisons 1–3 of §3.2 against newly gathered §3.1 evidence and
confirms that this operation still holds the machine scope and rollout slot.
A revocation that commits before the retry transaction therefore prevents the
retry; reading the approval and recording the attempt later is not sufficient.

Revocation or cancellation before the transaction commits prevents dispatch.
After commitment it is recorded on the timeline and controls future steps
only: no retry is permitted, and an owner that sees it before recording an
attempt must not send. An operation with no recorded attempt then becomes
`unresolved` (§4) with its scope still held; an attempt already recorded runs
to its own classification. It is not a guarantee that no request is sent.

The residual window is explicit. The machine or a dependency can change after
§3.1 and before the request arrives. Some such changes surface: as a failure
before any attempt is recorded, as a definitive rejection, or as a
postcondition mismatch, each classified under §5. Not all do. `apply-config`
sends a full configuration, so an out-of-band change that lands inside the
window, such as an emergency `talosctl` edit, can be overwritten by a request
that then verifies and completes normally, leaving no trace in this operation's
evidence. The bound maximum observation age narrows this window; it does not
close it, and this contract claims no detection of such a change. Closing it
needs a compare-and-apply mechanism on the Talos side, which is outside the
PoC and is not assumed.

This boundary does not claim that a stale worker can be prevented from sending
an already committed request after it loses database ownership. E4 must prove
the selected ownership mechanism for this operation. Until that evidence
exists, conflicting work is stopped and the operation is reported unresolved.
Database leases and fencing tokens coordinate application workers; they are not
remote fencing enforced by Talos.

## 4. Operation timeline and states

Design: [§12.5](../design/Talos_Configuration_and_Machine_Management_Design.md#125-durable-operations-and-uncertain-outcomes).

Every operation has one durable timeline containing the plan and approval,
plan-time evidence, execution-time evidence, ownership transitions, dispatch
commitment, request attempts, responses, reconnects, revocations and
cancellations, postconditions and final classification. State changes are
append-only facts with a current projection.

| State | Meaning |
| --- | --- |
| `planned` | The immutable plan exists but is not authorized. |
| `approved` | Approval matches the complete plan binding. |
| `committed` | The commitment transaction succeeded; the machine scope and a rollout slot are held. |
| `sending` | An attempt is recorded; the request is in flight or its outcome is unknown. |
| `verifying` | The request was accepted; the manager is collecting identity, digest and health evidence. |
| `completed` | Terminal. Postconditions prove the bound artifact is applied and healthy; `Applied` is updated. |
| `rejected` | Terminal. A recorded, definitive Talos response proves the request was refused before any mutation. |
| `cancelled` | Terminal. No request was sent. This is never an undo of remote work. |
| `unresolved` | Evidence cannot yet establish a terminal state or safe retry. Scope and slot stay held. |

| From | To | Condition |
| --- | --- | --- |
| `planned` | `approved` | Approval recorded against the complete binding. |
| `planned`, `approved` | `cancelled` | Cancellation, expiry or revocation before commitment. |
| `approved` | `committed` | The §3.2 transaction succeeds. |
| `committed` | `sending` | The owning executor records an attempt, then sends. |
| `committed` | `unresolved` | Restart, ownership loss, or revocation/cancellation after commitment: a stale worker may still send. |
| `sending` | `verifying` | An acceptance response is recorded. |
| `sending` | `rejected` | A definitive pre-mutation rejection response is recorded (§5). |
| `sending` | `unresolved` | Lost response, timeout, restart or ownership loss. |
| `verifying` | `completed` | Postconditions observed. |
| `verifying` | `unresolved` | Postconditions contradicted, or not established before the deadline. |
| `unresolved` | `completed` | Later evidence establishes the postconditions for this operation's assignment revision and artifact. |
| `unresolved` | `sending` | Classified safe to retry (§5) and the §3.3 retry transaction succeeds. |
| `unresolved` | `cancelled` | Evidence establishes that no request was sent and none can still be sent. |

No other transition is valid. The machine scope and rollout slot are released
only on entering a terminal state. An operator decision may resolve an
`unresolved` operation only into one of the listed targets, recorded with the
deciding identity and the evidence relied on.

`completed` requires the expected machine identity, assignment revision,
artifact/configuration digest and applicable health checks. A timeout is not
proof of failure, completion or retry permission.

## 5. Interruption and retry classification

Design: [§12.5](../design/Talos_Configuration_and_Machine_Management_Design.md#125-durable-operations-and-uncertain-outcomes),
[§12.6](../design/Talos_Configuration_and_Machine_Management_Design.md#126-offline-semantics).

Recovery examines the target and cluster rather than replaying the journal.
For each attempt, operation, mode, assignment revision and intervening state,
the manager classifies the outcome:

| Classification | Required action |
| --- | --- |
| Completed | Record completion, persist the bound release and artifact as `Applied`, and release the scope and slot. |
| Safe to retry | Create a bounded retry within the original plan, expiry and unrevoked approval, admitted only by the §3.3 retry transaction. |
| Unresolved | Preserve the assignment and stop dependent or conflicting mutations; observe further or request a specific operator decision. |
| Rejected | Record the response as proof of non-mutation, release the scope and slot, and require a corrected plan; the unchanged plan is not retried. |

Completed, safe to retry and unresolved are the design's classifications for
an outcome made uncertain by restart, ownership loss or transport failure.
`Rejected` covers the different case of a definitive response that was received
and recorded. It is never assigned after an interruption without that recorded
response, and only response classes shown by evidence to precede any mutation
qualify; every other error is `unresolved`.

The manager must observe after a lost response, restart, ownership loss or
reconnect. Before a retry or dependent mutation it rechecks identity,
assignment, current digest and relevant health. An old operation that may have
been accepted remotely blocks conflicting work until its outcome is resolved.
Idempotency keys and journals prevent duplicate application intent in the
manager, but do not provide exactly-once remote execution.

Required interruption tests cover failure before commitment, after commitment
but before send, after remote acceptance, before response recording, and
before local completion recording. Each test must show the resulting
classification and blocked scope; a generic “mark successful” recovery action
is not valid.

## 6. Drift handling

Design: [§12.4](../design/Talos_Configuration_and_Machine_Management_Design.md#124-drift-policy),
[§7.1](../design/Talos_Configuration_and_Machine_Management_Design.md#71-responsibility-split-and-secret-ingress).

Drift is an observed configuration digest that differs from the last verified
`Applied` state while no operation on that machine is `committed`, `sending`,
`verifying` or `unresolved`. Two look-alikes are not drift:

- `Desired` differing from `Applied` is pending convergence. It is resolved by
  a plan and approval, and an offline machine in this state is simply not yet
  converged.
- A digest mismatch while an operation holds the machine scope is evidence for
  that operation's classification (§5).

Drift is never an automatic apply trigger. The affected scope enters the
policy selected by the operator:

- **Freeze:** pause mutation while incident work continues.
- **Adopt:** extract known/marked secrets before any ordinary persistence,
  retain only sanitized metadata with references plus an encrypted exact
  baseline artifact, then create a reviewed draft that must be published and
  approved.
- **Revert:** create a new plan from the selected applicable release after
  checking assignment, current state and dependencies; normal approval applies.

Extraction precedes every backup-visible write. Adoption success, failure or
interruption must not leave plaintext in drafts, indexes, staging, database or
staging writes that a base backup, log archive or storage snapshot can
capture, responses or logs, whether or not the application can read the
resulting backup. Sanitizing a record afterwards does not satisfy this.
External Talos access remains independent of application approval and is
reconciled as drift afterward.

## 7. Recovery after management-state restoration

Design: [§14.6](../design/Talos_Configuration_and_Machine_Management_Design.md#146-explicit-recovery-mode-after-restoration),
[§14.4](../design/Talos_Configuration_and_Machine_Management_Design.md#144-recovery-dependencies),
[§7.6](../design/Talos_Configuration_and_Machine_Management_Design.md#76-metadata-only-dependency-checks).

External database or provider restoration does not automatically resume
management. The operator explicitly enters recovery mode; mutation and
automatic operation resumption are paused while observation and validation
remain available.

The recovery procedure is:

1. Verify schema, revisions, releases, operation journals and provider/key
   references from the restored state.
2. Check retained dependencies and provider unlock/recovery material, and test
   required decryption and credentials under the actual recovery identities.
3. Refresh machine identity, assignment, running version/configuration digest,
   health and cluster membership for reachable targets.
4. Reclassify pending operations using the interruption rules above; never
   replay a stale journal entry solely because it is pending.
5. Mark each scope `ready`, `blocked` or `unresolved`, recording the missing
   dependency or evidence.
6. Require explicit operator release for eligible scopes. Plans, preconditions
   and approval are still required after release.

A restored database can predate a revocation, and the rollback is not assumed
to be detectable. Every approval recorded before recovery-mode entry is
therefore unverified: it authorizes no dispatch, and a plan needs an approval
recorded after recovery-mode entry before §3.2 can succeed.

Observation, applying a retained artifact and compiling a new release have
different dependencies. A missing historical key may block regeneration or a
particular release without blocking unrelated observation. A restored desired
state is never proof of current machine state, and recovery-mode release is
not blanket approval for mutations.

## 8. Invariants and worked interleavings

An implementation and its reviewer can check these directly:

1. **One uncertain operation per machine.** At most one operation per machine
   coordination scope is `committed`, `sending`, `verifying` or `unresolved`.
2. **Commitment and attempt precede send.** No Talos request is sent without a
   durably recorded commitment and attempt earlier on the same timeline, and
   every attempt after the first was admitted by its own retry transaction.
3. **`Applied` follows evidence.** `Applied` changes only on entering
   `completed`, to that operation's bound release and artifact.
4. **Plans do not change.** Any change to a binding is a new plan that needs a
   new approval.
5. **Release only on a terminal state.** The machine scope and rollout slot
   are released only by `completed`, `rejected` or `cancelled`.
6. **Absence of evidence classifies nothing.** A timeout, lost response or
   pending journal entry alone never yields `completed`, `rejected`,
   `cancelled` or safe to retry.
7. **No plaintext before extraction.** Known/marked secrets never reach a
   backup-visible write in plaintext.
8. **Restored approvals authorize nothing.** An approval recorded before
   recovery-mode entry never satisfies §3.2.
9. **A held scope freezes the assignment.** A machine's assignment does not
   change while an operation on it is `committed`, `sending`, `verifying` or
   `unresolved`.

### 8.1 Revocation racing commitment

Plan P for machine M is `approved`; executor X has recorded its §3.1 evidence.
A revocation R and X's commitment transaction C race.

- **R commits first.** C fails comparison 1. Nothing is committed or sent, and
  P's operation becomes `cancelled`.
- **C commits first.** The operation is `committed` and R is recorded on its
  timeline afterwards. If X has already recorded an attempt, that attempt runs
  to its own classification and is never retried. Otherwise X does not send,
  and the operation becomes `unresolved` with M's scope held, because a worker
  that did not see R may still send. It reaches `cancelled`
  only on evidence that no request was or can be sent, or `completed` if
  observation shows the artifact applied. What counts as evidence of
  non-dispatch for the selected ownership mechanism is an E4 result; until
  then it needs a specific operator decision.

### 8.2 Lost response, then a newer plan (stale A-after-B)

Operation A (artifact *a*) for M is `sending` under executor X when the
network partitions and X loses ownership. The manager restarts as Y; A becomes
`unresolved`.

- A newer plan B for M cannot commit: comparison 4 fails while A is
  `unresolved` (invariant 1). This is the contract's A-after-B protection.
  Because nothing newer than *a* can be dispatched while A is uncertain, a
  late delivery of X's request cannot overwrite newer configuration.
- Y observes M. If identity, assignment revision, digest and health match A's
  postconditions, A becomes `completed`, `Applied` becomes *a*, the scope is
  released, and B may then run its own §3.
- If M still reports the previous digest, Y cannot tell "never delivered" from
  "still in flight". A stays `unresolved`. It becomes safe to retry only if
  evidence establishes retry safety for `apply-config`/`no-reboot`, this
  assignment revision and the intervening state; whether that holds is an E4
  result, not a claim of this contract.

## 9. Verification and evidence limits

Design: [§18.1](../design/Talos_Configuration_and_Machine_Management_Design.md#181-phase-0---evidence-before-implementation-contracts)
(E1, E3, E4, E6).

E4 must demonstrate revision conflicts, durable intent, ownership transitions,
revocation around commitment, stale A-after-B apply, interrupted requests and
reconnect behavior for the selected database/provider profile, and must supply
the evidence this contract leaves open: the ownership mechanism, what proves
non-dispatch, which Talos responses prove pre-mutation rejection, and when a
retry is safe. E1 must show that extraction precedes backup-visible writes. E6
must exercise the complete existing-cluster slice, including drift and
restoration. Until those experiments pass, implementation must expose
unresolved outcomes and stop conflicting work rather than claim safe retry,
exactly-once execution, universal stale-worker prevention, or detection of an
out-of-band change overwritten inside the §3.3 residual window.

The Talos Upgrade/LifecycleClient compatibility deferral remains explicit:
this contract does not claim full E3, upgrade support or lifecycle execution.
