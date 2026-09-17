# Execution and recovery contract

This document specifies the first-milestone execution boundary for
configuration control on an existing cluster. It is derived from design
§§12.2, 12.4–12.7 and 14.6, the E4 concurrency requirements, and the
configuration-control acceptance in §18.2. It is a PoC contract, not evidence
that the mechanisms have passed E4 or E6.

## 1. Supported operation

The PoC supports one operation class: applying a published, encrypted
configuration artifact to an existing worker with Talos `no-reboot` mode.
Publication, observation and recovery are separate capabilities. Bootstrap,
reset, wipe, upgrade, reboot, `try`, `staged`, remote transports and
managed-cluster etcd recovery are out of scope until their operation-specific
safety evidence exists.

The manager stores three distinct values:

| Value | Meaning |
| --- | --- |
| Desired | The approved release selected for the machine. |
| Applied | The release and artifact last verified as successfully applied. |
| Observed | The latest machine-reported identity, running version, configuration digest and health evidence. |

An RPC response alone does not update `Applied`; the configured postconditions
must be observed. An observation timestamp changing does not invalidate a plan
unless the observation is one of its declared preconditions.

## 2. Immutable plan and approval binding

Planning resolves a published artifact. Dispatch never re-renders a draft or
silently substitutes a newer artifact. The immutable plan binds:

- plan, release and exact per-machine artifact identities;
- renderer/provenance and all secret and encryption dependency versions;
- machine identity and assignment revision;
- operation `apply-config` and mode `no-reboot`;
- redacted diff, expected preconditions and postconditions;
- rollout limits, expiry, idempotency key and approval policy;
- the plan revision and authorization identity.

The plan is invalid if any dispatch-relevant binding changes, including the
artifact, assignment, operation, mode, parameters, relevant precondition,
expiry or approval. The executor must replan and obtain approval again rather
than repairing the plan in place.

Approval authorizes exactly this binding. Publication and a green dependency
retention check never authorize dispatch. The trusted application boundary
enforces authorization; the provider does not. Approval self-use and
multi-party rules remain policy inputs, not implicit guarantees of this PoC.

## 3. Dispatch commitment

The database transaction that creates the durable dispatch intent is the
**dispatch commitment boundary**. Before committing it, the executor must
atomically verify all of the following:

1. the plan is unexpired and approved;
2. the artifact, assignment and operation bindings are unchanged;
3. required dependencies are retained and usable under the executing
   identity;
4. the machine observation satisfies the plan's preconditions; and
5. this operation owns the machine's coordination scope and no conflicting
   operation is committed.

Revocation before this transaction commits prevents dispatch. After commitment,
revocation prevents no already-authorized send; cancellation only controls
future steps. The operation timeline must record the commitment before the
Talos request is sent.

This boundary does not claim that a stale worker can be prevented from sending
an already committed request after it loses database ownership. E4 must prove
the selected ownership mechanism for this operation. Until that evidence
exists, conflicting work is stopped and the operation is reported unresolved.
Database leases and fencing tokens coordinate application workers; they are not
remote fencing enforced by Talos.

## 4. Operation timeline and states

Every operation has one durable timeline containing the plan and approval,
observations, ownership transitions, dispatch commitment, request attempts,
responses, reconnects, postconditions and final classification. State changes
are append-only facts with a current projection.

```text
planned -> approved -> committed -> sending -> verifying -> completed
                         |             |          |
                         |             |          +-> unresolved
                         |             +-> unresolved
                         +-> cancelled
```

- `planned`: immutable plan exists but is not authorized.
- `approved`: approval matches the complete plan binding.
- `committed`: the dispatch transaction succeeded; no second conflicting
  operation may be committed.
- `sending`: the request attempt is in flight or its outcome is unknown.
- `verifying`: the manager is collecting identity, digest and health evidence.
- `completed`: postconditions prove the intended artifact is applied and
  healthy.
- `cancelled`: no request was committed; this is not an undo of remote work.
- `unresolved`: evidence cannot establish completion or safe retry.

`completed` requires the expected machine identity, assignment revision,
artifact/configuration digest and applicable health checks. A timeout is not
proof of failure, completion or retry permission.

## 5. Interruption and retry classification

Recovery examines the target and cluster rather than replaying the journal.
For each attempt, operation, mode, assignment revision and intervening state,
the manager classifies the outcome:

| Classification | Required action |
| --- | --- |
| Completed | Record completion and release the coordination scope. |
| Safe to retry | Create a bounded retry within the original plan, expiry and approval. |
| Unresolved | Preserve the assignment and stop dependent or conflicting mutations; observe further or request a specific operator decision. |

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

An observed configuration digest that differs from the desired/applied state is
drift, not an automatic apply trigger. The affected scope enters the policy
selected by the operator:

- **Freeze:** pause mutation while incident work continues.
- **Adopt:** extract known/marked secrets before ordinary persistence, retain
  only sanitized metadata plus protected exact baseline material, then create a
  reviewed draft that must be published and approved.
- **Revert:** create a new plan from the selected applicable release after
  checking assignment, current state and dependencies; normal approval applies.

Adoption failure or interruption must not leave plaintext in drafts, indexes,
staging, backups visible to the application, responses or logs. External
Talos access remains independent of application approval and is reconciled as
drift afterward.

## 7. Recovery after management-state restoration

External database or provider restoration does not automatically resume
management. The operator explicitly enters recovery mode; mutation and
automatic operation resumption are paused while observation and validation
remain available.

The recovery procedure is:

1. Verify schema, revisions, releases, operation journals and provider/key
   references from the restored state.
2. Check retained dependencies and test required decryption and credentials
   under the actual recovery identities.
3. Refresh machine identity, assignment, running version/configuration digest,
   health and cluster membership for reachable targets.
4. Reclassify pending operations using the interruption rules above; never
   replay a stale journal entry solely because it is pending.
5. Mark each scope `ready`, `blocked` or `unresolved`, recording the missing
   dependency or evidence.
6. Require explicit operator release for eligible scopes. Plans, preconditions
   and approval are still required after release.

Observation, applying a retained artifact and compiling a new release have
different dependencies. A missing historical key may block regeneration or a
particular release without blocking unrelated observation. A restored desired
state is never proof of current machine state, and recovery-mode release is
not blanket approval for mutations.

## 8. Verification and evidence limits

E4 must demonstrate revision conflicts, durable intent, ownership transitions,
revocation around commitment, stale A-after-B apply, interrupted requests and
reconnect behavior for the selected database/provider profile. E6 must
exercise the complete existing-cluster slice, including drift and restoration.
Until those experiments pass, implementation must expose unresolved outcomes
and stop conflicting work rather than claim safe retry, exactly-once execution,
or universal stale-worker prevention.

The Talos Upgrade/LifecycleClient compatibility deferral remains explicit:
this contract does not claim full E3, upgrade support or lifecycle execution.
