# PoC acceptance plan

The integrated acceptance plan for the configuration-control PoC of
[design §18.2](../design/Talos_Configuration_and_Machine_Management_Design.md#182-phase-1---configuration-control-on-an-existing-cluster),
owned by the [specification review](https://github.com/ginsys/bronzeward/issues/20) and executed by
[E6: demonstrate PoC acceptance](https://github.com/ginsys/bronzeward/issues/31) against the
implementation of [ginsys/bronzeward#21](https://github.com/ginsys/bronzeward/issues/21) to
[ginsys/bronzeward#30](https://github.com/ginsys/bronzeward/issues/30). For each behaviour §18.2
requires it names the run that shows it, the contract clauses that run exercises, what passes, what
must be refused and what is kept. It is a plan: nothing here has been executed, and a pass under it
is a pass on the fixture within its limits (§9), never a production claim.

Contracts are cited by short name: **C** is the [compilation contract](compilation.md), **PA** the
[persistence and API contract](persistence-api.md) and **ER** the
[execution and recovery contract](execution-recovery.md). Research reports keep the keys of the
[feasibility evidence review §3](../design/research/20260925-feasibility-evidence-review.md#3-sources)
(DB, DS, E1, E3, Fx, KL).

## 1. How a scenario is built

Each scenario states its implementing issue and design items, then:

- **preconditions**: the state it starts from, on the fixture of §2;
- **steps**, numbered, each one act or one observation;
- **clauses exercised**: the contract sections whose behaviour the steps show;
- **pass criteria**, as observations a reviewer can repeat;
- **negative controls**: the refusals the run must show and, where a contract requires one, the
  control that removes a mechanism and shows the failure it exists to prevent (design
  [§7.7](../design/Talos_Configuration_and_Machine_Management_Design.md#77-poc-deployment-profile),
  consequences). A check whose control cannot fail proves nothing;
- **retained evidence**, kept with the acceptance evidence that
  [ginsys/bronzeward#31](https://github.com/ginsys/bronzeward/issues/31) lands.

The four closing runs of
[ER §9.3](execution-recovery.md#93-gaps-carried-and-what-would-close-them) are folded in by name:
drift freeze, sanitized adoption and approved revert (S5); the late-landing bound measurement
(S6.2); a restoration run with a missed stale instance (S7); and the operation timeline as an
operator reads it (S8).

## 2. Fixture and common evidence

**Fixture.** The disposable environment in [`fixtures/`](../../fixtures/README.md) at the pins of
`fixtures/versions.env`
([Fx §3](../design/research/20260919-investigation-fixtures.md#3-what-was-built)): Talos v1.13.6
with one control plane and one worker in Docker, PostgreSQL 17 and a single-node OpenBao 2.6.1 with
KV v2 and Transit. `bin/up` creates it; `bin/inject` kills, pauses and partitions any container,
snapshots and restores the database and OpenBao, soft-deletes and destroys KV versions and deletes
Transit keys; `bin/evidence` records digests and scans for synthetic secrets with a positive
control; `bin/down` removes it. This is design §7.7's selected profile in its single-instance
topology. Every secret is synthetic, and each is a scan pattern.

**Additions**, delivered by [ginsys/bronzeward#21](https://github.com/ginsys/bronzeward/issues/21),
none changing a pin the Phase-0 evidence was captured against:

- the Bronzeward server built from the commit under test, as controller instance A, and a second
  instance B for the takeover and restoration scenarios;
- a disposable OIDC issuer ([PA §10.1](persistence-api.md#101-humans-oidc)) with synthetic humans
  `h-author`, `h-publisher`, `h-approver`, `h-recovery`, `h-viewer`, each holding the role it
  names, and `h-all`, holding `author`, `publisher` and `approver`; group claims mapped to roles in
  deployment configuration;
- one automation identity holding `author` and `publisher`, its token issued by the server-side tool
  and `h-all` named as its responsible human
  ([PA §10.2](persistence-api.md#102-automation-bronzeward-issued-tokens));
- the deployment settings held outside the database: a settle floor of 30 s
  ([ER §5.2](execution-recovery.md#52-accounting-a-lost-response)), the maximum transport deadline
  ([ER §7.3](execution-recovery.md#73-procedure) step 1) and `deniedSubjects`
  ([PA §10.4](persistence-api.md#104-revocation)).

**Common evidence for every run:** the fixture and Bronzeward commits and the `versions.env` blob;
commands and exit codes; request and response transcripts with tokens withheld; the migration
record; machine timelines as served by `GET /machines/{id}/timeline`; and a `bin/evidence` bundle
scanning the database data directory, write-ahead log and dump, OpenBao metadata, container and
Bronzeward logs, temporary and staging paths and every backup taken, with its positive control
found.

**Execution.** One integrated run executes the pass paths of S0 to S8 in order from a fresh
`bin/up`, as [ginsys/bronzeward#31](https://github.com/ginsys/bronzeward/issues/31) requires.
Negative controls and interruption matrices run separately, each from a fresh `bin/up` or a
recorded snapshot, so that a refused or killed step leaves no state a later scenario relies on.

## 3. Foundation

### S0. Startup, migrations and authenticated access

[ginsys/bronzeward#21](https://github.com/ginsys/bronzeward/issues/21). Design §18.2 item 4 and
closing paragraph.

**Preconditions.** A clean checkout; `bin/up` done; no Bronzeward schema.

**Steps.**

1. Build and run the checks with the documented commands.
2. Run the migrate command twice concurrently with the service stopped; start the server and call
   the liveness route.
3. Send a request with no token, then one per token defect of PA §10.1 and §10.2, a missing, empty
   and non-string `sub` included.
4. Walk every row of the
   [design §13.7](../design/Talos_Configuration_and_Machine_Management_Design.md#137-poc-identity-and-approval-policy)
   scenario table through the API, as the identity it names.
5. Request a dispatch, token-issuing or role-granting path.

**Clauses exercised.** PA [§9.2](persistence-api.md#92-resources-and-routes),
[§10.1](persistence-api.md#101-humans-oidc),
[§10.2](persistence-api.md#102-automation-bronzeward-issued-tokens),
[§10.3](persistence-api.md#103-authorization), [§10.5](persistence-api.md#105-recording-every-act),
[§11](persistence-api.md#11-migrations), [§13.5](persistence-api.md#135-a-migration),
[§14](persistence-api.md#14-failure-and-rejection-cases).

**Pass criteria.** One migrate run applies the schema; the other waits, then skips it. Every token
defect answers `401`, except a revoked or denied subject, which answers `403 identity-revoked`; none
creates a principal row. Each §13.7 row ends as the table says, and each
allowed act is recorded with its identity and role. Step 5 answers `404`: no handler exists.

**Negative controls.** A binary that does not know the newest migration, and a checksum mismatch,
each refuse to start. The advisory-lock control (DB row 026) shows the concurrent-run failure
without the lock. Automation on a human-only route: `403 forbidden`.

**Retained evidence.** Build and check logs; both migrate runs; the scenario walk as a table of
request, identity, role and response.

## 4. Configuration control

### S1. Adoption of the existing cluster

[ginsys/bronzeward#22](https://github.com/ginsys/bronzeward/issues/22). Design §18.2 item 1,
[§9.1](../design/Talos_Configuration_and_Machine_Management_Design.md#91-adopt-an-existing-configured-cluster).

**Preconditions.** S0 passed. Before any Bronzeward act, `bin/evidence` records each node's
configuration digest and machine-configuration resource version (the pre-state). The worker carries
one synthetic secret outside the Talos schema's secret fields, in `machine.files[].content`.

**Steps.**

1. `h-author` records the cluster and both machines (`POST /clusters`, `POST /machines`).
2. `h-author` imports each node (`POST /ingestions`), marking the file content's path on the worker.
3. The draft transaction commits and releases the claim.
4. `h-publisher` publishes the import draft, as in S2 steps 3 and 4.
5. `h-publisher` creates an `adopt` plan per machine, binding no drift record and no baseline
   revision; `h-approver` approves each.
6. The controller commits each adopt plan, which records the adoption.
7. `bin/evidence` records the post-state and scans.

**Clauses exercised.** C [§2.3](compilation.md#23-pipeline), [§3.1](compilation.md#31-two-modes),
[§3.2](compilation.md#32-claim-states-and-timers), [§4.2](compilation.md#42-the-guard-e1-decision-4),
[§13](compilation.md#13-failure-and-rejection-cases); ER [§6.3](execution-recovery.md#63-adopt)
(existing-cluster handover); PA [§3.2](persistence-api.md#32-the-import-base),
[§5.1](persistence-api.md#51-fences-and-claims), [§6.4](persistence-api.md#64-orphans),
[§13.2](persistence-api.md#132-provider-write-succeeded-database-commit-failed).

**Pass criteria.** Neither node's digest nor resource version changed, and no Talos mutation
request appears in any log. The scan finds no synthetic secret outside OpenBao, the marked value
included, here and in every negative control below. Each draft holds `!bwref` references where the
schema list and the mark identified values, each value a create-only generation. The baseline
ciphertext, decrypted by the OpenBao administrator outside Bronzeward, has the recorded
configuration digest, equal to the pre-state; the executor identity's decryption of it is refused.
Each adopt operation is `completed`, and `Applied` holds the baseline's digest, marked adopted by
observation.

**Negative controls.**

- A mark addressing no node, an unparseable input, and an extracted value also present in an
  unmarked string: each refused before any provider write, quoting no input text.
- The ingesting process killed after each pipeline step 0 to 8 under transient staging: the claim is
  abandoned at lease lapse, its operation fails `ingestion-abandoned`, and generations already
  created are listed as orphans, none deleted.
- The same under encrypted staging: takeover by a second ingestion principal only after lease lapse;
  the old owner's draft transaction refused; a takeover with nothing to decrypt abandons; with
  OpenBao partitioned the taker keeps the claim `resumed`
  ([C §3.4](compilation.md#34-takeover-e1-decision-2)). A crash inside the draft transaction: no
  draft, and the claim stays unreleased.
- Automation on `POST /ingestions`: `403`. An adoption record whose latest observation is older than
  the bound age, or shows another digest: refused, and `Applied` stays unset.

**Retained evidence.** Pre- and post-state digests and resource versions; one scan bundle per
success, refusal and interruption point; claim rows and the orphan report; the adopt plans,
approvals and adoption records.

### S2. Publication

[ginsys/bronzeward#23](https://github.com/ginsys/bronzeward/issues/23),
[ginsys/bronzeward#24](https://github.com/ginsys/bronzeward/issues/24). Design §18.2 items 2 and 3.

**Preconditions.** S1 passed; the worker has `Applied` from its adoption. The SR and SP matrices
re-run with composition through the compiler's own machinery path reach the same verdicts
([C §10.1](compilation.md#101-selection-the-go-machinery-in-process)); otherwise the machinery selection
is reopened and this scenario does not run.

**Steps.**

1. `h-author` drafts a worker fragment with one node-label change, the safe `no-reboot` change S4
   applies, and a `!bwref` reference to a value extracted in S1.
2. The draft compiles and validates; `h-author` reads the worker's redacted review data.
3. `h-publisher` publishes; the `publish` operation succeeds.
4. The release is read back: artifacts, dependency records, renderer and contract record,
   configuration digests; `bin/evidence` records the worker's resource version.
5. The dependency monitor classifies the release's dependencies.

**Clauses exercised.** C [§5.1](compilation.md#51-syntax),
[§6](compilation.md#6-resolution-order-and-composition), [§7](compilation.md#7-validation-stages),
[§8.1](compilation.md#81-provenance-by-tracer-composition), [§8.3](compilation.md#83-redaction),
[§9](compilation.md#9-dependency-records), [§10.2](compilation.md#102-pinning-and-refusals),
[§11](compilation.md#11-publication-hand-off); PA [§4.1](persistence-api.md#41-etags),
[§4.2](persistence-api.md#42-concurrency-conflicts-in-publication),
[§6.2](persistence-api.md#62-the-commit-transaction-t3),
[§6.3](persistence-api.md#63-partial-publication), [§7.2](persistence-api.md#72-behavior),
[§13.1](persistence-api.md#131-concurrent-edits), [§13.4](persistence-api.md#134-repeated-requests);
ER [§1](execution-recovery.md#1-supported-operation-and-state-values).

**Pass criteria.** The artifact is Transit ciphertext under a key identity the provider cannot
reissue; dependency records name exact versions; the renderer is the pinned machinery at the node's
running contract minor. The review data shows the label change and redacts every resolved value and
every value whose provenance is sensitive. The release is `Desired` for the worker, with no plan, no
operation but `publish` and no change of the worker's resource version. Immutable rows refuse
`UPDATE` and `DELETE`. Every dependency classifies `retained`
([design §7.8](../design/Talos_Configuration_and_Machine_Management_Design.md#78-poc-retention-and-recovery-policy)).

**Negative controls.** Two authors on one ETag: the second gets `412`, nothing persisted. A head
moved by another publication: `409 stale-input`; without `FOR SHARE` the stale release commits (DB
row 011). A literal copy of a resolved value, and the reserved text `!bwref` in a string: refused,
paths only. A fidelity check firing on an injected structural change (C §8.1). A pinned KV version
soft-deleted, then destroyed, then OpenBao partitioned: `blocked`, `lost`, `unknown`, each alerted
as design §7.8 sets, and a publication pinning it refused. A repeated request under one key
replays; the key reused for another body answers `422`; a `publish` worker killed after `COMMIT`
leaves one release; a lapsed `publish` job is claimed again and its first worker's completion
refused.

**Retained evidence.** The release record and artifact metadata, the review data as served, the
classifications and alerts, each refusal's problem document, and the resource version before and
after.

### S3. Approval

[ginsys/bronzeward#25](https://github.com/ginsys/bronzeward/issues/25). Design §18.2 item 4,
[§13.7](../design/Talos_Configuration_and_Machine_Management_Design.md#137-poc-identity-and-approval-policy).

**Preconditions.** S2 passed; no plan exists for the worker.

**Steps.**

1. After at least one `drift` observation of the worker following S2, confirm that no plan or
   operation exists for it: publication alone dispatches nothing.
2. `h-publisher` creates an `apply-config` plan from the S2 release: `no-reboot`, a bound route,
   deadlines, maximum attempts, expiry and maximum observation age; `h-viewer` reads its redacted
   whole-configuration diff.
3. `h-approver` approves it.
4. Separately, `h-all` authors, publishes, plans and approves a change, and approves a plan the
   automation identity created.

**Clauses exercised.** ER [§2](execution-recovery.md#2-immutable-plan-and-approval-binding),
[§3.2](execution-recovery.md#32-the-commitment-transaction) comparisons 1 and 2,
[§8.1](execution-recovery.md#81-revocation-racing-commitment),
[§8.5](execution-recovery.md#85-identity-revocation); PA
[§8.1](persistence-api.md#81-plans-and-their-operations), [§10.4](persistence-api.md#104-revocation),
[§10.5](persistence-api.md#105-recording-every-act).

**Pass criteria.** The plan binds every value ER §2 lists, its expected pre-dispatch digest equal to
`Applied`'s. The approval names plan revision, approver, role and epoch; both step 4 approvals are
marked self-approval. The plan is `approved`, and no operation exists before commitment.

**Negative controls.** Automation, and `h-recovery` alone, approving: `403`; a second approval:
`409`. The worker's assignment changed after planning: commitment refused by comparison 2. A plan
past its expiry: `expired`, no operation. An approval revoked before commitment: `revoked`, nothing
sent; a revocation started while a commitment holds the approval waits for it, and the control that
inserts without the lock does not wait (DS row 003). The approver's identity revoked before
commitment, and after it with no attempt: the plan cannot commit, or its operation ends
`cancelled`. Every dependency `retained` and the plan unapproved: nothing dispatched.

**Retained evidence.** Plan and approval records, the diff as served, and each case's timeline
entries.

### S4. Safe apply

[ginsys/bronzeward#26](https://github.com/ginsys/bronzeward/issues/26). Design §18.2 item 5,
[§12.1](../design/Talos_Configuration_and_Machine_Management_Design.md#121-desired-applied-and-observed-state).

**Preconditions.** S3's plan `approved`. DS rows 001 to 005, 012 to 017 and 022 re-run through the
implementation's Talos client, reaching DS's outcomes: the condition of
[ER §3.5](execution-recovery.md#35-talos-client-and-route).

**Steps.**

1. The controller checks the plan and gate, then records an `evidence` observation and the use-time
   check of the artifact key and operation credentials.
2. The commitment transaction creates the operation `committed`.
3. The attempt transaction records the attempt; the controller then sends `apply-config`, mode
   `no-reboot`, over the bound route.
4. The acceptance is recorded (`verifying`), then a `completion` observation.
5. The operation is `completed`, and `Applied` moves to the release.
6. Repeat over the other route with a second release.

**Clauses exercised.** ER
[§3.1](execution-recovery.md#31-execution-time-evidence-gathered-before-the-transaction),
[§3.2](execution-recovery.md#32-the-commitment-transaction),
[§3.3](execution-recovery.md#33-after-commitment),
[§3.5](execution-recovery.md#35-talos-client-and-route),
[§4](execution-recovery.md#4-operation-timeline-and-states),
[§5.1](execution-recovery.md#51-evidence-per-outcome),
[§8](execution-recovery.md#8-invariants-and-worked-interleavings) invariants 1 to 5; PA
[§8.3](persistence-api.md#83-the-resource-and-its-progress).

**Pass criteria.** The worker's digest equals the artifact's under the ER §1 normalization, and its
resource version moved. On the timeline, evidence, commitment and attempt precede the request, and
the completion observation's basis follows the recorded response. `Desired`, `Applied` and
`Observed` are served as three values and agree only after completion. Scope and rollout slot are
released on `completed`.

**Negative controls.** OpenBao sealed (`bin/inject kill openbao`): the use-time check fails, and
nothing commits until it is unsealed. An observation older than the plan's maximum age: commitment
refused. A second plan for the worker while the first holds the scope: refused by comparison 4;
without the unique index both commit (DS row 011). An assignment change while the scope is held,
and a frozen scope: refused. An artifact failing node validation: `InvalidArgument`, `rejected`,
resource version unchanged (DS row 022). The ownership check moved out of the attempt's `UPDATE`: a
stale attempt is recorded (DB row 018).

**Retained evidence.** The operation and its timeline, observation records, the node's digest and
resource version per step, and the DS re-run table.

### S5. Drift: detect, freeze, sanitized adoption, approved revert

[ginsys/bronzeward#27](https://github.com/ginsys/bronzeward/issues/27). Design §18.2 item 6,
[§12.4](../design/Talos_Configuration_and_Machine_Management_Design.md#124-drift-policy). Closing
run: **drift freeze, sanitized adoption and approved revert**
([ER §9.3](execution-recovery.md#93-gaps-carried-and-what-would-close-them) item 1).

**Preconditions.** S4 passed; no drift record is open and no operation holds the worker's scope.

**Steps.**

1. With the fixture's pinned `talosctl`, patch the worker out of band, adding a file whose content
   is a new synthetic secret.
2. The next `drift` observation opens a drift record and raises the digest-mismatch alert; the scope
   is not frozen by detection.
3. `h-author` freezes the scope.
4. **Adopt.** `h-author` ingests the drifted configuration as a drift adoption, marking the new
   file's content; `h-publisher` publishes it and creates an `adopt` plan binding the drift record;
   `h-approver` approves; the adoption record commits under the freeze.
5. Measure whether an artifact compiled from the unchanged import base reproduces the baseline
   digest.
6. **Revert.** Patch the worker out of band again. `h-publisher` creates a revert plan binding the
   new drift record and its drifted digest; `h-approver` approves it, recorded as approving an
   unseen overwrite, and unfreezes; the revert runs as S4.

**Clauses exercised.** ER [§6.1](execution-recovery.md#61-detection),
[§6.2](execution-recovery.md#62-freeze), [§6.3](execution-recovery.md#63-adopt),
[§6.4](execution-recovery.md#64-revert), [§6.5](execution-recovery.md#65-drift-states),
[§8.6](execution-recovery.md#86-drift-freeze-then-adopt-or-revert); C
[§2.3](compilation.md#23-pipeline) (drift adoption); PA
[§9.2](persistence-api.md#92-resources-and-routes).

**Pass criteria.** The drift record names the observation, `Applied`'s digest and the observed one;
`Desired`, `Applied` and `Observed` stay distinct throughout. No Talos request is sent while the
record is open and the scope frozen: the resource version changes only with the out-of-band patches
and the revert. After step 4, `Applied` is the adopted release with the baseline's digest, the
baseline revision has advanced and the record is closed `adopted`; the new secret is found nowhere
outside OpenBao, on success, rejection and each interruption of the ingestion. After step 6, the
record is closed `reverted` and the digest is the revert artifact's. Step 5's result is recorded
whichever way it falls.

**Negative controls.** An approved ordinary plan while the record is open: refused by comparison 6.
A plan expecting `Applied`'s digest while the machine runs another: commitment refused on its
evidence, and the refusal's transaction opens a drift record (ER choice §10.23). Unfreezing by a
non-`approver`: `403`. The adoption record refused on a stale observation, a machine changed again,
a revoked approval, a changed baseline revision, a changed `Desired` and a record already closed by
a revert. A revert refused by re-drift before commitment, and one refused after a manual return,
whose refusal closes the record `returned`. A read begun before an `Applied` change opens no
record.

**Retained evidence.** Drift records and alerts; freeze, adoption and revert records; scan bundles
for each adoption run; step 5's measurement; the resource version series.

### S6. Interruption: executor loss, uncertain send, takeover

[ginsys/bronzeward#28](https://github.com/ginsys/bronzeward/issues/28). Design §18.2 item 7,
[§12.5](../design/Talos_Configuration_and_Machine_Management_Design.md#125-durable-operations-and-uncertain-outcomes).

**Preconditions.** S4 passed; instance A running and B stopped; an approved plan for the worker.

#### S6.1 Interruption points and takeover

**Steps.**

1. From a fresh approved plan per point of
   [ER §5.3](execution-recovery.md#53-interruption-points), kill A before commitment, after
   commitment before any attempt, after the acceptance before it is recorded, and after a matching
   observation before completion is recorded. For one more plan, pause A after commitment and
   before its attempt instead of killing it: the stale owner.
2. Start B; it takes over every non-terminal operation A owned.
3. Unpause A; let it try to record its attempt. A restarted instance would not do: it takes over
   at its start (ER §3.4) and its attempts are then legitimate.
4. Partition the worker during a send over the worker route, and the control plane during one over
   its route: the response is lost.
5. `h-recovery` records the accounting decision after the settle floor, with a recovery observation
   and the resource version read at both ends of the settle; a completion observation classifies
   the operation.
6. While one operation is `unresolved`, try a newer plan for the worker.

**Clauses exercised.** ER [§3.4](execution-recovery.md#34-ownership-the-takeover-fence),
[§4](execution-recovery.md#4-operation-timeline-and-states),
[§5](execution-recovery.md#5-interruption-and-retry-classification),
[§5.2](execution-recovery.md#52-accounting-a-lost-response),
[§5.3](execution-recovery.md#53-interruption-points),
[§8.2](execution-recovery.md#82-uncertain-outcome-blocks-a-newer-plan-stale-a-after-b),
[§8.3](execution-recovery.md#83-crash-between-commitment-and-send),
[§8.4](execution-recovery.md#84-a-late-landing-and-the-accounting-decision); PA
[§5.1](persistence-api.md#51-fences-and-claims).

**Pass criteria.** Each point leaves the state the ER §5.3 table names and resolves as it says; no
path marks an operation successful without a completion observation. A's attempt after the
takeover is refused inside the attempt `UPDATE`, and A sends nothing. A lost response keeps the
operation `unresolved` with its scope held until the decision, however well the digest matches, and
step 6's plan is refused by comparison 4. A retry is admitted only with attempts left, the approval
passing and the gate open, and never after a newer timeline fact.

**Negative controls.** Accounting before the settle floor, or by `h-approver`: refused. The fence
without its owner and generation comparison: the stale owner records an attempt (DS row 008). The
scope index removed: the newer plan commits while the old one is uncertain (DS row 011).

#### S6.2 Late-landing bound measurement

Closing run: **the bound on when an abandoned request can no longer land**
([ER §9.3](execution-recovery.md#93-gaps-carried-and-what-would-close-them) item 2).

**Steps.**

1. From the Talos v1.13.6 source, establish how the control plane's API proxy forwards and cancels
   a proxied request, and state what that predicts.
2. Measure landings through the proxy across partition lengths, pauses of each side and connection
   reuse, with enough captures per cell to state a distribution, the resource version serving as
   the landing signal
   ([DS §4.4](../design/research/20260925-dispatch-safety.md#44-uncertain-sends-reconnects-and-accounting-criteria-2-and-3)).
3. Over the worker route, test whether a dial failure proves no send.

**Pass criteria.** A recorded distribution, with its method and the prediction, whichever way it
falls. A bound enters [ER §5.2](execution-recovery.md#52-accounting-a-lost-response) only through
an accepted contract change; until then accounting stays a `recovery-admin` decision, and a late
landing is detected as drift, not prevented.

**Retained evidence (S6).** Per point: the kill time, the timeline, the classification and scope
state. Per capture: partition timing, the resource version series and the landing time.

## 5. Restoration recovery

### S7. Restore, recovery start, entry, per-scope release, exit

[ginsys/bronzeward#29](https://github.com/ginsys/bronzeward/issues/29). Design §18.2 item 7,
[§14.6](../design/Talos_Configuration_and_Machine_Management_Design.md#146-explicit-recovery-mode-after-restoration).
Closing run: **a restoration run with a missed stale instance**
([ER §9.3](execution-recovery.md#93-gaps-carried-and-what-would-close-them) item 3).

**Preconditions.** S4 passed; both machines have `Applied`. Before time *T*, an ingestion claim is
taken, and A commits a worker plan and is paused before its attempt (S6.1's stale owner). At *T*:
`bin/inject db-snapshot`. After *T*: B starts, takes the operation over, classifies it safe to retry
on a recovery observation and records an attempt whose request is held by pausing the worker; a
second worker plan is approved; an approval is revoked; an approver's subject is added to
`deniedSubjects` and then its identity is revoked; then `bin/inject bao-snapshot`.

**Steps.**

1. **Restore.** Stop B and record the time. A stays paused, not stopped: it is the missed stale
   instance, whose owner generation the restore makes current again. Restore the database to *T*.
2. **Recovery start.** Start B with the recovery-start flag.
3. **Entry.** `h-recovery` records entry through the API, naming both backups and their ages; a new
   epoch is minted.
4. Unpause A; it tries a takeover, a commitment and an attempt.
5. Unpause the worker: the held request may land.
6. `h-recovery` re-records the identity revocation; the automation token is reissued with the
   server-side tool.
7. Account every scope with one decision after the settle floor, using the maximum transport
   deadline for the attempt the restore erased, each with its recovery observation; check
   dependencies under the recovery identities. Then pause the control plane, take `restoration`
   observations, reclassify the restored operation and mark the scopes.
8. **Restart** B without the flag: the recovery start runs no executor and no job worker, and entry
   has committed.
9. **Release** the worker scope; `h-publisher` and `h-approver` plan and approve a worker change in
   the new epoch, which applies as S4. If the held request landed, the change is a revert binding
   the drift record, as in S5 step 6.
10. Unpause the control plane; after a successful `restoration` observation, re-mark and release
    its scope.
11. **Exit** recovery mode.

**Clauses exercised.** ER [§7.1](execution-recovery.md#71-what-a-restore-does-to-the-fences),
[§7.2](execution-recovery.md#72-entry), [§7.3](execution-recovery.md#73-procedure),
[§7.4](execution-recovery.md#74-scope-states-in-recovery),
[§7.5](execution-recovery.md#75-what-recovery-mode-allows),
[§7.6](execution-recovery.md#76-leaving-recovery-mode),
[§8.7](execution-recovery.md#87-restore-to-an-older-backup); PA
[§12.1](persistence-api.md#121-the-epoch), [§12.2](persistence-api.md#122-entry),
[§12.3](persistence-api.md#123-what-a-restored-state-means-record-by-record),
[§12.4](persistence-api.md#124-restore-and-idempotency),
[§13.6](persistence-api.md#136-a-restore-to-an-older-backup); C
[§3.5](compilation.md#35-expiry-abandonment-and-restoration).

**Pass criteria.** Before entry, B answers every request but liveness and entry
`409 recovery-mode-active` and runs no executor. After entry every scope is pre-restore
unaccounted, every non-terminal operation is `unresolved` under B, and the ingestion claim is
abandoned. Each of A's writes in step 4 is refused by the epoch. The restored operation, whose
journal holds no attempt, ends `cancelled`; a landing of the held request is recorded as drift at
the scope's marking, not applied over. The revoked approver is refused before and after entry, and
every pre-restore automation token until reissued. The worker scope is `ready`, is released and
dispatches under a new-epoch approval while the control-plane scope is `blocked` on its failed
observation, until step 10 clears it. Exit succeeds only once both scopes are released, and no
restored operation was retried.

**Negative controls.** Plan creation and approval on a pre-restore unaccounted scope, and release
of a scope not `ready`: refused. A pre-entry approval: refused by comparison 1. A second entry under
another key in the same recovery start: `409`. Exit with a scope unreleased: refused. The epoch
term dropped from the fence: A's token passes across the `pg_dump` restore, as DB row 027 showed.
The same restore started normally, with no entry: A's token passes; this residual is stated (PA
§14, last row) and shown, not claimed closed. A variant deleting the Transit key the `Desired`
releases name: each scope is `blocked` on it and leaves only through a newly published release
under a new key, selected as `Desired`. A variant with the OpenBao snapshot older than the database:
referenced generations read `404` and alert at once, and a publication pinning them is refused (PA
§13.3).

**Retained evidence.** The entry record with backup identities and ages, the recovery timeline, A's
refused writes, scope states after each step, the accounting decision with its observations, and
the scan of every backup.

## 6. The operation timeline

### S8. The operation timeline as an operator reads it

[ginsys/bronzeward#26](https://github.com/ginsys/bronzeward/issues/26),
[ginsys/bronzeward#30](https://github.com/ginsys/bronzeward/issues/30),
[ginsys/bronzeward#31](https://github.com/ginsys/bronzeward/issues/31). Design §18.2 closing
paragraph,
[§15.2](../design/Talos_Configuration_and_Machine_Management_Design.md#152-operation-timeline).
Closing run: **the operation timeline**
([ER §9.3](execution-recovery.md#93-gaps-carried-and-what-would-close-them) item 4).

**Preconditions.** The integrated run's S4 change, one S6 interruption, the S5 drift and the S7
restoration are on record.

**Steps.**

1. As `h-viewer`, following the documented walkthrough of
   [ginsys/bronzeward#30](https://github.com/ginsys/bronzeward/issues/30), read the worker's
   timeline and each operation's events.
2. From the timeline alone, answer for each of the four: who approved what, what was sent and when,
   what was observed, why the scope was held, who decided an accounting and on what, and how it
   ended.
3. Hold an event stream open past its token's expiry; resume it with `Last-Event-ID` after
   revoking the subject.

**Clauses exercised.** ER [§4.1](execution-recovery.md#41-timeline-content),
[§3.5](execution-recovery.md#35-talos-client-and-route) (response text); C
[§8.3](compilation.md#83-redaction); PA [§8.3](persistence-api.md#83-the-resource-and-its-progress),
[§10.3](persistence-api.md#103-authorization).

**Pass criteria.** Each answer is reached by following links from the operation or the machine, and
entries are in commit order within the scope. Response text is redacted or withheld, and no served
timeline holds a synthetic secret. The stream ends no later than the token's `exp`, and the
resumption is refused. After S7 the `Bronzeward-Epoch` header differs from the pre-restore one. A
second reviewer following the walkthrough reaches the same answers.

**Negative controls.** `h-viewer` mutating anything: `403`. A Talos error whose text embeds
configuration, provoked as in S4's `InvalidArgument` case, is served redacted or withheld, never
verbatim. The scan's positive control: a copy of a served timeline with a synthetic secret appended
is reported.

**Retained evidence.** Timelines and event streams as served, the answers with the entries that
support them, and the reviewer's record.

## 7. Coverage

| Design §18.2 | Scenario | Issue |
| --- | --- | --- |
| Item 1: import without mutation, extraction before persistence, exact encrypted baseline | S1 | [ginsys/bronzeward#22](https://github.com/ginsys/bronzeward/issues/22) |
| Item 2: native fragments, references, revisions, provenance, validation stages | S2 | [ginsys/bronzeward#23](https://github.com/ginsys/bronzeward/issues/23) |
| Item 3: immutable encrypted artifacts, exact dependencies, redacted review data, no apply | S2, S3 step 1 | [ginsys/bronzeward#23](https://github.com/ginsys/bronzeward/issues/23), [ginsys/bronzeward#24](https://github.com/ginsys/bronzeward/issues/24) |
| Item 4: authenticated API; plan and approval of one safe worker change | S0, S3 | [ginsys/bronzeward#21](https://github.com/ginsys/bronzeward/issues/21), [ginsys/bronzeward#25](https://github.com/ginsys/bronzeward/issues/25) |
| Item 5: direct dispatch by the E4 mechanism, verification, separate desired/applied/observed | S4 | [ginsys/bronzeward#26](https://github.com/ginsys/bronzeward/issues/26) |
| Item 6: external change detected; freeze, sanitized adoption, approved revert | S5 | [ginsys/bronzeward#27](https://github.com/ginsys/bronzeward/issues/27) |
| Item 7: interrupted execution | S6 | [ginsys/bronzeward#28](https://github.com/ginsys/bronzeward/issues/28) |
| Item 7: external restoration through explicit recovery mode | S7 | [ginsys/bronzeward#29](https://github.com/ginsys/bronzeward/issues/29) |
| Closing paragraph: selected database/provider behaviour (§7.7) | S0 to S8 on the §2 fixture | [ginsys/bronzeward#21](https://github.com/ginsys/bronzeward/issues/21), [ginsys/bronzeward#30](https://github.com/ginsys/bronzeward/issues/30) |
| Closing paragraph: scoped authorization (§13.7) | S0 step 4, S3 | [ginsys/bronzeward#21](https://github.com/ginsys/bronzeward/issues/21), [ginsys/bronzeward#25](https://github.com/ginsys/bronzeward/issues/25) |
| Closing paragraph: usable operation timeline | S8 | [ginsys/bronzeward#26](https://github.com/ginsys/bronzeward/issues/26), [ginsys/bronzeward#31](https://github.com/ginsys/bronzeward/issues/31) |
| §18.1 E6: the existing-cluster vertical slice | the integrated run, S0 to S8 | [ginsys/bronzeward#31](https://github.com/ginsys/bronzeward/issues/31) |

## 8. Not covered by the PoC

Excluded by design §18.2 until later phases prove each operation class; no scenario exercises:

- new-machine enrolment;
- reset and reuse of a machine;
- cluster bootstrap;
- upgrades, including the Upgrade/LifecycleClient transition that
  [E3 §6.3](../design/research/20260925-talos-compatibility.md#63-criterion-3-unsupported-combinations-and-the-deferred-lifecycle-tests)
  deferred to the machine-lifecycle phase
  ([design §18.3](../design/Talos_Configuration_and_Machine_Management_Design.md#183-phase-2---machine-lifecycle));
  passing this plan is not full E3;
- remote transports;
- managed-cluster etcd recovery.

Also outside this plan: dispatch to a control plane, any mode but `no-reboot`, a rollout limit
above one, the bounded Ignore drift policy, SQLite and MySQL/MariaDB, replicated or pooled database
and OpenBao topologies, physical backup and point-in-time restore, and any production claim.

## 9. Limits

- The fixture's limits bind every result: exact-copy leak scanning, one Talos version, containers
  without reboot, disks or installer, one control plane and one worker
  ([Fx §7](../design/research/20260919-investigation-fixtures.md#7-limits)).
- A scenario shows the behaviour of the build it ran against, on the paths it drove; interleavings
  no step forces are not shown.
- S6.2 may end without a usable bound; that outcome is recorded, and accounting stays an operator
  decision. A contract change that renumbers a cited clause updates this plan in the same change.
