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
  and expected postconditions;
- the verification deadline, as a duration from the attempt, and the maximum
  number of attempts;
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

1. the plan is unexpired and approved, its approval is not revoked, and the
   approval was recorded in the current recovery epoch (§7);
2. the artifact, assignment, operation, mode and parameter bindings are
   unchanged;
3. the evidence recorded under §3.1 belongs to this operation, satisfies the
   plan's preconditions, is inside its bound maximum age or validity window,
   and no newer observation of the machine contradicts it;
4. this operation takes the machine's coordination scope, and no other
   operation on that scope is `committed`, `sending`, `verifying` or
   `unresolved`;
5. this operation takes a slot in the plan's rollout scope, counting every
   operation of that scope in `committed`, `sending`, `verifying` or
   `unresolved` against the bound rollout limit; and
6. the **scope gate** is open: the machine scope is not frozen by drift policy
   (§6), and either recovery mode is not in effect or the scope was explicitly
   released in the current recovery epoch (§7).

If any comparison fails, nothing is committed and nothing is sent. Freeze,
recovery mode and scope release are durable database facts precisely so that
they can be compared here; checking them before the transaction would leave a
race in which dispatch proceeds while mutation is meant to be paused.

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

Every attempt, the first included, is recorded by an **attempt transaction**
before its request is sent; the operation timeline therefore shows the
commitment and the attempt before any Talos request. The attempt transaction
repeats comparisons 1–3 and 6 of §3.2, against newly gathered §3.1 evidence
when it is a retry, confirms that this operation still holds the machine scope
and rollout slot and has attempts left under the bound maximum, and records
the attempt identity with the absolute verification deadline derived from the
plan. The first attempt transaction may be the commitment transaction itself.
Reading the approval and recording the attempt later is not sufficient.

Revocation or cancellation before the commitment transaction commits prevents
dispatch. After commitment it is recorded on the timeline and prevents every
attempt whose attempt transaction has not yet committed, so it also permits no
retry. An operation with no recorded attempt then becomes `unresolved` (§4)
with its scope still held; an attempt already recorded runs to its own
classification. Freezing the scope or entering recovery mode closes the scope
gate with the same effect. None of this guarantees that no request is sent:
an executor whose attempt transaction committed earlier may still send.

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
| `committed` | `sending` | The §3.3 attempt transaction succeeds; the executor then sends. |
| `committed` | `unresolved` | Restart, ownership loss, or a revocation, cancellation or closed scope gate after commitment. |
| `sending` | `verifying` | An acceptance response is recorded. |
| `sending` | `rejected` | A definitive pre-mutation rejection response is recorded (§5). |
| `sending` | `unresolved` | Lost response, timeout, restart or ownership loss. |
| `verifying` | `completed` | Postconditions established by a completion observation (below). |
| `verifying` | `unresolved` | Postconditions contradicted, or not established before the recorded verification deadline. |
| `committed`, `sending`, `verifying` | `unresolved` | Recovery-mode entry (§7). |
| `unresolved` | `completed` | Postconditions later established by a completion observation. |
| `unresolved` | `rejected` | A delayed definitive pre-mutation rejection response is recorded for the only outstanding attempt. |
| `unresolved` | `sending` | Classified safe to retry (§5) and the §3.3 attempt transaction succeeds. |
| `unresolved` | `cancelled` | No attempt transaction ever committed for this operation, and none can: the approval is revoked or expired, or the plan is cancelled. |

No other transition is valid. The machine scope and rollout slot are released
only on entering a terminal state. An operator decision may resolve an
`unresolved` operation only into one of the listed targets, recorded with the
deciding identity and the evidence relied on.

A **completion observation** is taken after the operation's latest recorded
attempt and recorded on its timeline with its observation revision and time.
It must show the expected machine identity, assignment revision,
artifact/configuration digest and applicable health checks. An observation
taken before that attempt, or not tied to this operation, never completes it.
A timeout is not proof of failure, completion or retry permission.

## 5. Interruption and retry classification

Design: [§12.5](../design/Talos_Configuration_and_Machine_Management_Design.md#125-durable-operations-and-uncertain-outcomes),
[§12.6](../design/Talos_Configuration_and_Machine_Management_Design.md#126-offline-semantics).

Recovery examines the target and cluster rather than replaying the journal.
For each attempt, operation, mode, assignment revision and intervening state,
the manager classifies the outcome:

| Classification | Required action |
| --- | --- |
| Completed | Record completion, persist the bound release and artifact as `Applied`, and release the scope and slot. |
| Safe to retry | Create a bounded retry within the original plan, expiry and unrevoked approval, admitted only by the §3.3 attempt transaction. |
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

- **Freeze:** pause mutation while incident work continues. The freeze is a
  durable fact on the machine scope that closes the scope gate, so an already
  approved operation fails comparison 6 of §3.2 and no attempt transaction
  succeeds until the operator lifts it.
- **Adopt:** extract known/marked secrets before any ordinary persistence,
  retain only sanitized metadata with references plus an encrypted exact
  baseline artifact, then create a reviewed draft that must be published and
  approved.
- **Revert:** create a new plan from the selected applicable release after
  checking assignment, current state and dependencies; normal approval applies.

Adopt and Revert both end through the normal path: a plan whose preconditions
bind the observed, drifted digest, approved and dispatched under §3. For Adopt
the plan applies the adopted release, which is expected to leave the machine's
configuration unchanged. `Applied` advances only when that operation reaches
`completed`; there is no separate baseline transition, and until then the
scope is still reported as drifted. Publishing and approving an adopted
release does not by itself resolve drift.

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

Recovery-mode entry is a durable database fact that starts a new **recovery
epoch**, a counter incremented by the entry itself and so always greater than
any value in the restored state. Entry closes the scope gate for every machine
scope: comparison 6 of §3.2 fails, no attempt transaction succeeds, and every
operation in `committed`, `sending` or `verifying` becomes `unresolved` (§4).
Approvals and scope releases carry the epoch in which they were recorded.

The recovery procedure is:

1. Stop, or establish as stopped, every executor that ran against the
   pre-restoration state, and wait out the bound verification deadline of any
   request that may be in flight.
2. Verify schema, revisions, releases, operation journals and provider/key
   references from the restored state.
3. Check retained dependencies and provider unlock/recovery material, and test
   required decryption and credentials under the actual recovery identities.
4. Refresh machine identity, assignment, running version/configuration digest,
   health and cluster membership for reachable targets, after step 1.
5. Reclassify pending operations using the interruption rules above; never
   replay a stale journal entry solely because it is pending.
6. Mark each scope `ready`, `blocked` or `unresolved`, recording the missing
   dependency or evidence.
7. Require explicit operator release for eligible scopes. The release is
   recorded in the current recovery epoch and opens the scope gate for that
   scope only. Plans, preconditions and approval are still required after
   release.

The restored journal can omit operations dispatched after the snapshot was
taken, so step 5 alone cannot preserve the one-uncertain-operation invariant:
an erased operation A could still land after a new operation B. Step 1 is what
covers it. A scope for which pre-restoration sends cannot be shown quiesced
stays `unresolved` and is not eligible for release, and an observed state that
the restored `Applied` does not explain is drift (§6), not grounds for an
apply.

A restored database can predate a revocation, and the rollback is not assumed
to be detectable. Every approval recorded before recovery-mode entry is
therefore unverified: it carries an older epoch, fails comparison 1 of §3.2,
and a plan needs an approval recorded in the current recovery epoch.

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
   every attempt was admitted by an attempt transaction.
3. **`Applied` follows evidence.** `Applied` changes only on entering
   `completed`, to that operation's bound release and artifact, and
   `completed` needs an observation taken after the latest recorded attempt.
   Drift adoption is no exception.
4. **Plans do not change.** Any change to a binding is a new plan that needs a
   new approval.
5. **Release only on a terminal state.** The machine scope and rollout slot
   are released only by `completed`, `rejected` or `cancelled`.
6. **Absence of evidence classifies nothing.** A timeout, lost response or
   pending journal entry alone never yields `completed`, `rejected`,
   `cancelled` or safe to retry.
7. **No plaintext before extraction.** Known/marked secrets never reach a
   backup-visible write in plaintext.
8. **Restored approvals authorize nothing.** An approval recorded before the
   current recovery epoch never satisfies §3.2.
9. **A held scope freezes the assignment.** A machine's assignment does not
   change while an operation on it is `committed`, `sending`, `verifying` or
   `unresolved`.
10. **No mutation through a closed gate.** No commitment or attempt
    transaction succeeds for a scope that is frozen, or that is under recovery
    mode without a release in the current recovery epoch.

### 8.1 Revocation racing commitment

Plan P for machine M is `approved`; executor X has recorded its §3.1 evidence.
A revocation R and X's commitment transaction C race.

- **R commits first.** C fails comparison 1. Nothing is committed or sent, and
  P's operation becomes `cancelled`.
- **C commits first.** The operation is `committed` and R is recorded on its
  timeline afterwards. If X's attempt transaction had already committed, that
  attempt runs to its own classification and is never retried. Otherwise the
  attempt transaction now fails comparison 1, X does not send, and the
  operation becomes `unresolved` with M's scope held. Because no attempt
  transaction ever committed and none can, it then becomes `cancelled`. That
  conclusion is only as strong as invariant 2: E4 must show that every
  executor of the selected ownership mechanism sends only after its attempt
  transaction commits.

### 8.2 Lost response, then a newer plan (stale A-after-B)

Operation A (artifact *a*) for M is `sending` under executor X when the
network partitions and X loses ownership. The manager restarts as Y; A becomes
`unresolved`.

- A newer plan B for M cannot commit: comparison 4 fails while A is
  `unresolved` (invariant 1). This is the contract's A-after-B protection.
  Because nothing newer than *a* can be dispatched while A is uncertain, a
  late delivery of X's request cannot overwrite newer configuration.
- Y observes M after A's recorded attempt. If identity, assignment revision,
  digest and health match A's postconditions, A becomes `completed`, `Applied` becomes *a*, the scope is
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
the evidence this contract leaves open: the ownership mechanism, that every
executor sends only after its attempt transaction commits, how
pre-restoration executors are shown quiesced, which Talos responses prove
pre-mutation rejection, and when a retry is safe. E1 must show that extraction precedes backup-visible writes. E6
must exercise the complete existing-cluster slice, including drift and
restoration. Until those experiments pass, implementation must expose
unresolved outcomes and stop conflicting work rather than claim safe retry,
exactly-once execution, universal stale-worker prevention, or detection of an
out-of-band change overwritten inside the §3.3 residual window.

The Talos Upgrade/LifecycleClient compatibility deferral remains explicit:
this contract does not claim full E3, upgrade support or lifecycle execution.
