# PoC acceptance plan

The integrated acceptance plan for the configuration-control PoC of
[design §18.2](../design/Talos_Configuration_and_Machine_Management_Design.md#182-phase-1---configuration-control-on-an-existing-cluster),
owned by the [specification review](https://github.com/ginsys/bronzeward/issues/20) and executed by
[E6: demonstrate PoC acceptance](https://github.com/ginsys/bronzeward/issues/31) against the
implementation of [ginsys/bronzeward#21](https://github.com/ginsys/bronzeward/issues/21) to
[ginsys/bronzeward#30](https://github.com/ginsys/bronzeward/issues/30). For each behaviour §18.2
requires it names the run that shows it, the contract clauses that run exercises, what passes, what
must be refused and what is kept. It is a plan: its results are recorded not here but in the
evidence E6 lands and in the reports a scenario names (S6.2's), and a pass under it is a pass on the
fixture within its limits (§9), never a production claim.

Contracts are cited by short name: **C** is the [compilation contract](compilation.md), **PA** the
[persistence and API contract](persistence-api.md), **ER** the
[execution and recovery contract](execution-recovery.md) and **DM** the
[dependency monitor contract](dependency-monitor.md). Research reports keep the keys of the
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
KV v2 and Transit. `bin/up` creates it; `bin/inject` kills, pauses and partitions the database,
OpenBao and either node, snapshots and restores the database and OpenBao under a name, soft-deletes and destroys KV versions and deletes
Transit keys; `bin/evidence` records digests and scans for synthetic secrets with a positive
control; `bin/down` removes it. This is design §7.7's selected profile in its single-instance
topology. Every secret is synthetic, and each is a scan pattern.

**Additions**, delivered by [ginsys/bronzeward#21](https://github.com/ginsys/bronzeward/issues/21),
none changing a pin the Phase-0 evidence was captured against:

- the Bronzeward server built from the commit under test, as controller instance A, and a second
  instance B for the takeover and restoration scenarios, each a container that `bin/inject` kills,
  pauses and starts like the others;
- `bin/inject linksplit <instance> <node>` and `linkjoin <instance> <node>`, dropping one
  instance's traffic to one node's Talos API while its database and OpenBao stay reachable, since
  `netsplit` detaches the whole container;
- a disposable OIDC issuer ([PA §10.1](persistence-api.md#101-humans-oidc)) with synthetic humans
  `h-author`, `h-publisher`, `h-approver`, `h-recovery`, `h-viewer`, each holding the role it
  names, and `h-all`, holding `author`, `publisher` and `approver`; group claims mapped to roles in
  deployment configuration;
- one automation identity holding `author` and `publisher`, its token issued by the server-side tool
  and `h-all` named as its responsible human
  ([PA §10.2](persistence-api.md#102-automation-bronzeward-issued-tokens));
- `bin/inject seal openbao` and `unseal openbao`, sealing the running provider without stopping it,
  since `start openbao` unseals as it starts;
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

**Execution.** One integrated run executes the pass paths of S0 to S5, S7 (its nominal run) and S8
in order, S3's handover part between S1 and S2, from a fresh `bin/up`, as [ginsys/bronzeward#31](https://github.com/ginsys/bronzeward/issues/31) requires.
S6 is an interruption matrix: it kills and partitions instances, so it runs outside the integrated
run, and #31 takes its results from those runs. Negative controls and interruption matrices run
separately, each from a fresh `bin/up` or a
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
4. From a separate `bin/up`, walk through the API, as the identity each names, every row of the
   [design §13.7](../design/Talos_Configuration_and_Machine_Management_Design.md#137-poc-identity-and-approval-policy)
   scenario table that role alone denies: `h-author` publishes; `h-viewer` edits a draft, then
   publishes; `h-approver` creates a plan; the automation identity, then `h-recovery`, approves a
   plan; the automation identity, `h-author` and `h-publisher` each revoke an approval;
   `h-approver` records an identity revocation; `h-viewer` freezes a scope; `h-author`,
   `h-publisher` and `h-recovery` each unfreeze one; `h-approver` releases a scope and resolves an
   `unresolved` operation; the automation identity enters and leaves recovery mode. Then the
   automation identity calls every route PA §9.2 marks human only. The walk needs no seeded state:
   the role check runs before any transaction (PA §10.3), so each request names a well-formed
   identifier whose record need not exist, and it holds whether or not the route's own handler has
   landed. Last, as PA §10.4 requires, the operator adds `h-all`'s subject to `deniedSubjects`,
   then `h-recovery` records the identity revocation of `h-all`.
5. Request a dispatch, token-issuing or role-granting path: the table's rows denying a direct
   dispatch and a token issued or revoked through the API.

Every other row of that table is exercised in the scenario whose issue builds its route, since its
outcome needs that route's handler, state S0 does not create, or both: the adopted-baseline approval
in S3's handover part; the automation identity's publication in S2; the automation identity's plan, a human
approver's approval, the self-approval cases, cancellation by each role that may cancel and a
`publisher`'s refused cancellation of another identity's plan, revocation before commitment by each
role that may revoke and unapproved dispatch in S3; the revoked-identity approval, the controller's
dispatch and revocation after commitment or attempt (DS rows 002 to 005) in S4; freezing by each
role that may freeze and unfreezing in S5; resolving an `unresolved` operation in S6.1 step 7;
entry and scope release in S7. The OpenBao administrator's and break-glass rows are outside
Bronzeward: break-glass is seen as drift in S5, and the provider's acts are S2's and S7's
injections. Several of these runs are outside the integrated run;
[ginsys/bronzeward#31](https://github.com/ginsys/bronzeward/issues/31) confirms the table from the
integrated run and those runs' retained evidence together (§7.1).

**Clauses exercised.** PA [§9.2](persistence-api.md#92-resources-and-routes),
[§10.1](persistence-api.md#101-humans-oidc),
[§10.2](persistence-api.md#102-automation-bronzeward-issued-tokens),
[§10.3](persistence-api.md#103-authorization), [§10.5](persistence-api.md#105-recording-every-act),
[§11](persistence-api.md#11-migrations), [§13.5](persistence-api.md#135-a-migration),
[§14](persistence-api.md#14-failure-and-rejection-cases).

**Pass criteria.** Between the two migrate runs, each migration is applied exactly once: the lock is
taken per migration ([PA §11](persistence-api.md#11-migrations)), so a run waits on the migration the
other holds, then skips it, and either run may apply any version. Every token
defect answers `401`, except a revoked or denied subject, which answers `403 identity-revoked`; none
creates a principal row. Each denial step 4 walks answers `403 forbidden` and writes no act row; the
identity revocation is recorded with its identity and role
([PA §10.5](persistence-api.md#105-recording-every-act)), and `h-all` is refused afterwards. Step 5 answers `404`: no handler exists.

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

1. `h-author` records the cluster and both machines (`POST /clusters`, `POST /machines`): the
   cluster with the Talos cluster ID `talosctl get info` reports on a node, and each machine with
   its own Talos endpoint and the Talos node ID `talosctl get identity` reports on it, since the
   fixture's nodes report no SMBIOS UUID ([ER choice §10.26](execution-recovery.md#10-choices-for-owner-review)).
2. The fixture, acting as the operator, writes the `os:admin` talosconfig `bin/up` generated to the
   cluster's Talos access path, API path `secret/data/access/talos/<cluster id>`, under the OpenBao
   administrator with `bao kv put -mount=secret access/talos/<cluster id>`, the CLI adding KV v2's
   `data/` ([PA §3.3](persistence-api.md#33-talos-access)). `bin/up` has loaded the
   ingestion and executor policies, each granting `read` on `secret/data/access/talos/*` and
   nothing else there.
3. `h-author` opens the import draft (`POST /drafts`) and keeps its ETag. It imports each node in
   turn (`POST /ingestions` naming that draft, with `If-Match` its current ETag), marking the file
   content's path on the worker, and reads the draft's new ETag after each ingestion succeeds.
4. Each ingestion's draft transaction commits and releases its claim.
5. `bin/evidence` records the post-state and scans.

The handover that follows the import, publishing the import draft and adopting each machine so that
`Applied` holds its baseline, is S3's handover part
([ginsys/bronzeward#25](https://github.com/ginsys/bronzeward/issues/25)).

**Clauses exercised.** C [§2.3](compilation.md#23-pipeline), [§3.1](compilation.md#31-two-modes),
[§3.2](compilation.md#32-claim-states-and-timers), [§4.2](compilation.md#42-the-guard-e1-decision-4),
[§13](compilation.md#13-failure-and-rejection-cases); PA [§3.2](persistence-api.md#32-the-import-base),
[§3.3](persistence-api.md#33-talos-access), [§5.1](persistence-api.md#51-fences-and-claims), [§6.4](persistence-api.md#64-orphans),
[§13.2](persistence-api.md#132-provider-write-succeeded-database-commit-failed).

**Pass criteria.** Neither node's digest nor resource version changed, and no Talos mutation
request appears in any log. The scan finds no synthetic secret outside OpenBao, the marked value
included, here and in every negative control below. Each draft holds `!bwref` references where the
schema list and the mark identified values, each value a create-only generation. The baseline
ciphertext, decrypted by the OpenBao administrator outside Bronzeward, has the recorded
configuration digest, equal to the pre-state; the executor identity's decryption of it is refused.

**Negative controls.**

- A mark addressing no node, an unparseable input, and an extracted value also present in an
  unmarked string: each refused before any provider write, quoting no input text.
- The ingesting process killed after each pipeline step 0 to 8 under transient staging: the claim is
  abandoned at lease lapse, its operation fails `ingestion-abandoned`, and generations already
  created are listed as orphans by the orphan report under its own identity
  ([PA §6.4](persistence-api.md#64-orphans)), none deleted.
- The same under encrypted staging: takeover by a second ingestion principal only after lease lapse;
  the old owner's draft transaction refused; a takeover with nothing to decrypt abandons; with
  OpenBao partitioned the taker keeps the claim `resumed`
  ([C §3.4](compilation.md#34-takeover-e1-decision-2)). Takeover timing, for both states: a
  takeover of the `held` claim, and then of that `resumed` claim, each refused while the current
  owner's lease is live and accepted once it has lapsed, before the absolute expiry.
- Lease extension ([C §3.3](compilation.md#33-lease-extension-e1-decision-3)): a heartbeat from
  another ingestion principal, and one from the old owner after a takeover (its older generation),
  each refused; the owner's own heartbeat after its lease lapsed refused, the claim unchanged; the
  owner's heartbeat while live extends the lease, the positive control. A crash inside the draft transaction: the
  draft opened in step 3 is unchanged, with the ETag it had before the ingestion; no revision or
  entry the transaction wrote is committed, and the claim stays unreleased.
- Talos access ([PA §3.3](persistence-api.md#33-talos-access)): each cause of
  `talos-access-unavailable` and each node-ID or cluster-ID mismatch the fixture can produce, as PA §16 lists them, each ending the
  ingestion with its claim abandoned and nothing read kept.
- Automation on `POST /ingestions`: `403`.
- Operator review ([C §3.6](compilation.md#36-operator-review)), on imports of the worker with
  `review: true`: transient staging refused `422`; the run pauses with its envelope, its lease
  ended; the review route shows the unmarked file secret to the author only, `no-store`, and
  refuses automation `403`; the claim stays paused across a restart. A mark refused before any
  provider write names its rule and position, the envelope unchanged; a mark's run killed at each
  point it re-enters, and a continuation's run killed before its draft transaction, each resumed by
  takeover after lease lapse, the generations made kept. A mark extracting the file secret pauses
  with a new envelope, after which the review no longer shows it, and the continuation commits.
  A mark whose second provider write fails abandons the claim with its first generation kept. A
  paused claim abandoned by the author, and one left until its absolute expiry, are abandoned with
  their generations listed by the orphan report.

**Retained evidence.** Pre- and post-state digests and resource versions; one scan bundle per
success, refusal and interruption point; claim rows and the orphan report.

### S2. Publication

[ginsys/bronzeward#23](https://github.com/ginsys/bronzeward/issues/23),
[ginsys/bronzeward#24](https://github.com/ginsys/bronzeward/issues/24). Design §18.2 items 2 and 3.

**Preconditions.** S1 passed, and S3's handover part
([ginsys/bronzeward#25](https://github.com/ginsys/bronzeward/issues/25)) has run: the worker has
`Applied` from its adoption. The SR and SP matrices
re-run with composition through the compiler's own machinery path reach the same verdicts, with
cells adding references in the import base as well as in fragments, and SP's oracle run over
Bronzeward's own log and support formats
([C §10.1](compilation.md#101-selection-the-go-machinery-in-process), §15); otherwise the compiler
takes the subprocess fallback (C choice §16.24) and the re-run repeats through it before this
scenario runs.

**Steps.**

1. `h-author` opens a draft (`POST /drafts`), since the handover published S1's import draft, and keeps its
   ETag. In it, `h-author` writes a worker fragment with one node-label change, the safe
   `no-reboot` change S4 applies, and a `!bwref` reference to a value extracted in S1. Each
   mutation sends the draft's current ETag in `If-Match` and keeps the ETag it returns.
2. In the same draft, `h-author` creates a worker profile revision listing that fragment revision,
   and revises the worker's assignment to select the profile, with the same ETag handling.
3. `h-publisher` publishes with the draft's latest ETag; the `publish` operation compiles and
   validates the draft and succeeds.
4. `h-author` reads the worker's redacted review data from the release
   (`GET /releases/{id}/machines/{m}/review`); no route previews an unpublished draft.
5. The release is read back: artifacts, the source, profile and assignment revisions it snapshotted,
   dependency records, renderer and contract record, configuration digests; `bin/evidence` records
   the worker's resource version.
6. The dependency monitor classifies the release's dependencies.
7. From one restore of snapshots taken before step 1 (`bin/inject db-snapshot s2-step1`,
   `bao-snapshot s2-step1`; restored with `db-restore s2-step1`, `bao-restore s2-step1`), the
   automation identity repeats steps 1 to 3 as itself, holding `author` and `publisher`
   (design §13.7's automation Publish row). The integrated run includes this step: its release is
   the one later scenarios plan from, and S3's approval part relies on its automation-authored
   revision.

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

**Pass criteria.** The artifact is Transit ciphertext under a key identity the provider does not
reissue on the supported topology (design §7.7); dependency records name exact versions; the renderer is the one the precondition's re-run
selected, the pinned machinery or the pinned `talosctl` subprocess (C §10.1), at the node's
running contract minor. The review data shows the label change and redacts every resolved value and
every value whose provenance is sensitive. The release records the profile and assignment revisions
of step 2, and the label's provenance names the fragment revision the profile selected. The release
is `Desired` for the worker, with no plan and no operation but `publish` created since the
handover's completed `adopt` operations, and no change of the worker's resource version. Immutable rows refuse
`UPDATE` and `DELETE`. Every dependency classifies `retained`
([design §7.8](../design/Talos_Configuration_and_Machine_Management_Design.md#78-poc-retention-and-recovery-policy)).
Step 7's release is published, its edits recorded under `author` and its publication under
`publisher` (PA [§10.3](persistence-api.md#103-authorization) and
[§10.5](persistence-api.md#105-recording-every-act)), and no plan and no operation but `publish`
follows it.

**Negative controls.** Two authors on one ETag, of the fragment and of the profile: the second gets
`412`, nothing persisted. A fragment or profile head moved by another publication:
`409 stale-input`; without `FOR SHARE` the stale release commits (DB
row 011). A literal copy of a resolved value, and the reserved text `!bwref` in a string: refused,
paths only. A fidelity check firing on an injected structural change (C §8.1). A pinned KV version
soft-deleted, then destroyed, then OpenBao partitioned, sealed and paused: `blocked`, `lost`,
`unknown`, each alerted as design §7.8 sets, and a publication pinning it refused. During a
provider-wide outage, the required fresh publication observation cannot read Talos access:
publication refuses `503 dependency-unavailable` at that earlier node-evidence check, retaining
an observation with no access version or node values (PA §6.1.1). The monitor still classifies
and alerts every pinned dependency; the isolated `TestReadPinnedRefuses` check covers the
compiler's own unavailable-dependency refusal. Each provider
object state, against the fixture's OpenBao through the monitor's metadata client
(`TestLiveProviderStates`, run with S2's Go tests, its log kept in S2's evidence), one object per
state, each first `retained`: a KV version soft-deleted (`blocked`), undeleted (`retained`),
destroyed (`lost`) and pruned past `max_versions` (`lost`); a Transit version below the decryption
floor and trimmed (`unknown`, each reason naming its floor); deleted KV metadata and a deleted
Transit key (`unknown`, `regression` on that pass and not again); and the same 404 on a dependency
never `retained`, which alerts nothing until `persistent`. Each alert as
[DM §6.2](dependency-monitor.md#62-alert-kinds) sets and no other. A repeated request under one key
replays; the key reused for another body answers `422`; a `publish` worker killed after `COMMIT`
leaves one release; a lapsed `publish` job is claimed again and its first worker's completion
refused. The draft-update ingestion, as S1's matrix runs it for import: success, refusal, and the
process killed after each pipeline step that applies to a draft update, 0 to 7 (C §2.3 step 8 is
for imports and drift adoptions only), each followed by a scan of every persistence
surface ([C §15](compilation.md#15-verification-and-evidence-limits)). The publication, as instance C
runs it: success, a refusal by the compilation, and the process killed at each of its interruption
points before `COMMIT` (C §15), each followed by the same scan, C's writable layer included; after
each kill the job is claimed again once its lease lapses and publishes one release.

**Retained evidence.** The release record and artifact metadata, the review data as served, the
classifications and alerts, each refusal's problem document, and the resource version before and
after.

### S3. Approval

[ginsys/bronzeward#25](https://github.com/ginsys/bronzeward/issues/25). Design §18.2 item 4,
[§13.7](../design/Talos_Configuration_and_Machine_Management_Design.md#137-poc-identity-and-approval-policy).

S3 has two parts. The handover part hands the imported cluster over to Bronzeward: it follows S1
and precedes S2, in the integrated run as in any other. The approval part, steps 1 to 4, follows S2.

**Preconditions.** For the handover part, S1 passed. For the approval part, S2 passed; the worker
has no plan but the handover's completed `adopt` plan.

**Handover steps.**

1. `h-publisher` publishes S1's import draft, as in S2 steps 3 and 5.
2. `h-publisher` creates an `adopt` plan per machine, binding no drift record and no baseline
   revision; `h-approver` approves each.
3. The controller takes an `evidence` observation of each machine, begun after its approval, then
   commits the adopt plan, which records the adoption
   ([ER §6.3](execution-recovery.md#63-adopt) step 4 item 4).
4. `bin/evidence` records the post-state and scans.

**Steps.**

1. After the adoption controller has observed the worker at least three times following S2 (its
   heartbeat is the fixture's 5 s), confirm that no plan and no operation but `publish` exists
   for it besides the handover's completed `adopt` plan and operation: publication alone
   dispatches nothing. The fixture's drift interval is longer than any run, so no `drift`
   observation joins this check before S5
   ([ginsys/bronzeward#27](https://github.com/ginsys/bronzeward/issues/27)) sets the interval it
   observes at.
2. `h-publisher` creates an `apply-config` plan from the S2 release: `no-reboot`, a bound route,
   deadlines, maximum attempts, expiry and maximum observation age; `h-viewer` reads its
   evidence. The worker holds the import base's configuration, which has no redacted whole form,
   so the diff is served from `Applied`'s release to the plan's release with `withheld: true` and
   no text (PA choice §17.38); what the approver relies on is the release's validation and the
   plan's bound values.
3. `h-approver` approves it.
4. From one restore of snapshots taken before step 2 (`bin/inject db-snapshot s3-step4`,
   `bao-snapshot s3-step4`; restored with `db-restore s3-step4`, `bao-restore s3-step4`),
   prepare the three releases and plans in order, so each plan is made from the worker's `Desired`
   at that moment (ER §2) and the third reuses the second's fragment revision. The automation
   identity creates the first plan from the S2 release. For the second, `h-all` authors a fragment,
   lists it in the profile the worker's assignment selects, publishes and plans that change. For
   the third, `h-author` drafts an unrelated change and `h-publisher` publishes and plans a later
   release carrying `h-all`'s fragment revision unchanged (a release carries the heads its
   compilation uses, not every fragment). After preparation and the link cut described below,
   `h-all`, the automation identity's responsible human, approves the three plans in that order.
   Each of the two publications starts from a draft opened for it (`POST /drafts`), whose
   returned ETag every mutation sends in `If-Match` and replaces, as in S2 step 1. These plans
   stay out of the integrated run, where the controller could commit them before S4: the approval
   part takes snapshots after step 3 (`s3-step3`) and ends restored to them, leaving step 3's
   plan `approved` as S4's precondition and none of the controls' or step 4's plans or releases.
   No plan of the approval part commits; after approval it stays `approved` or ends before commitment:
   the controller dispatches an approved plan, so the approval part first cuts each instance's
   link to the worker (`bin/inject linksplit A|B|C worker`, §2). Step 4 verifies that its restore
   removed every apply-config plan, then joins the links while preparing its releases and plans:
   publication requires fresh node reads. All three plans must remain `proposed`, without an
   approval or operation, until all links are cut again. It then approves them in case order;
   self-approval reasons use each plan's immutable release, and approval does not compare current
   `Desired`. The executor's `evidence` observation fails while disconnected and nothing commits;
   S4 starts by joining the links again, and the commitment-level controls are S4's.

**Clauses exercised.** ER [§2](execution-recovery.md#2-immutable-plan-and-approval-binding)
(the plan states before commitment, approval and its refusals, revocation and cancellation) and, in
the handover part, [§6.3](execution-recovery.md#63-adopt) (existing-cluster handover); PA
[§8.1](persistence-api.md#81-plans-and-their-operations), [§10.4](persistence-api.md#104-revocation),
[§10.5](persistence-api.md#105-recording-every-act). The controls that need the commitment or
attempt transaction (ER [§3.2](execution-recovery.md#32-the-commitment-transaction) comparison 2,
[§8.1](execution-recovery.md#81-revocation-racing-commitment),
[§8.5](execution-recovery.md#85-identity-revocation)) are S4's, below.

**Pass criteria.** In the handover part, each adopt operation is `completed`, and `Applied` holds
the baseline's digest, marked adopted by observation; neither node's digest nor resource version
changed from S1's pre-state, no Talos mutation request appears in any log, and the scan finds no
synthetic secret outside OpenBao. In the approval part, the plan binds every value ER §2 lists, its
expected pre-dispatch digest equal to `Applied`'s. The approval names the plan, approver, role and epoch; step 3's approval is not
marked self-approval. The three step 4 approvals are marked with exactly these reason sets, since
every reason that holds is recorded (PA §10.5): `owned-automation`; `created-plan`, `published`,
`authored-change` and `owned-automation`; and `authored-reused` and `owned-automation`.
`owned-automation` is in every set because every release contains the S2 release's
automation-authored revision, which `h-all` answers for. Each plan is `approved`, and no operation
exists before commitment.

**Negative controls.** Every control of the approval part acts before commitment, and none can
commit while the worker's links are cut (step 4); S4 owns the controls that need the commitment or
attempt transaction (S4's negative controls). In the handover part, from a separate `bin/up`
(§2): an adoption whose evidence observation is older than the plan's bound age is no evidence, so
nothing is refused and no refusal entry is written; the controller observes again after its lease
and the adoption relies on that later observation, `Applied` staying unset until it commits. An
adoption whose latest observation shows another digest than the baseline's: refused once, by
comparison 4.4, and `Applied` stays unset. Automation, `h-recovery` and `h-viewer` approving:
`403`; a second approval, by `h-approver` and by `h-all`: `409`. A plan from the import release the
handover published, which S2 superseded as the worker's `Desired`: `409 conflict` naming the S2
release, no plan created. A plan past its expiry: `expired`, an approval of it `409`, no
operation. A plan cancelled before commitment, once each by its creator `h-publisher`, by its
creator the automation identity, by `h-approver` and by `h-recovery`: `cancelled`, no operation,
the act recorded under the role that permits it; `h-publisher` cancelling a plan the automation
identity created: `403 forbidden`, the plan unchanged (design §13.7 item 6). An approval revoked
before commitment, once by `h-approver` and once by `h-recovery`: `revoked`, nothing sent, the act
recorded under its role. Every dependency `retained` and the plan unapproved, across several
heartbeats of the controller: nothing dispatched.

**Retained evidence.** The handover's post-state digests, resource versions and scan bundle, and
its adopt plans, approvals and adoption records; plan and approval records, the diff as served, and
each case's timeline entries.

### S4. Safe apply

[ginsys/bronzeward#26](https://github.com/ginsys/bronzeward/issues/26). Design §18.2 item 5,
[§12.1](../design/Talos_Configuration_and_Machine_Management_Design.md#121-desired-applied-and-observed-state).

**Preconditions.** S3's plan `approved`. DS rows 001 to 023, with controls 006, 008 and 011, re-run
through the implementation's own controller and Talos client over the worker's own endpoint,
reaching DS's outcomes, with rows 012–014 as the direct-route variants of ER §3.5: 012 and 014
reach row 015's outcome, and 013's accounting at the executor's exit is refused before it succeeds
after the settle floor (the proxy's late landings are S6.2's). Rows 003, 009, 010, 022 and 023
and the controls run as Go checks, the test route
[ER choice §10.32](execution-recovery.md#10-choices-for-owner-review) selects; it names what each
drives (row 011's control is the schema's indexes, not a transaction).
Together they meet the condition
of [ER §3.5](execution-recovery.md#35-talos-client-and-route) and the first item of
[ER §9.2](execution-recovery.md#92-required-verification).

**Steps.** They start when S3's links to the worker are joined again (`bin/inject linkjoin
A|B|C worker`); the controller then dispatches S3's step 3 plan.

1. The controller checks the plan and gate, then records an `evidence` observation and the use-time
   check of the artifact key and operation credentials.
2. The commitment transaction creates the operation `committed`.
3. The attempt transaction records the attempt; the controller then sends `apply-config`, mode
   `no-reboot`, over the bound route.
4. The acceptance is recorded (`verifying`), then a `completion` observation.
5. The operation is `completed`, and `Applied` moves to the release.

**Clauses exercised.** ER
[§3.1](execution-recovery.md#31-execution-time-evidence-gathered-before-the-transaction),
[§3.2](execution-recovery.md#32-the-commitment-transaction),
[§3.3](execution-recovery.md#33-after-commitment),
[§3.5](execution-recovery.md#35-talos-client-and-route),
[§4](execution-recovery.md#4-operation-timeline-and-states),
[§5.1](execution-recovery.md#51-evidence-per-outcome),
[§8](execution-recovery.md#8-invariants-and-worked-interleavings) invariants 1 to 5,
[§8.1](execution-recovery.md#81-revocation-racing-commitment) and
[§8.5](execution-recovery.md#85-identity-revocation); PA
[§8.3](persistence-api.md#83-the-resource-and-its-progress).

**Pass criteria.** The worker's digest equals the artifact's under the ER §1 normalization, and its
resource version moved. On the timeline, evidence, commitment and attempt precede the request, and
the completion observation's basis follows the recorded response. `Desired`, `Applied` and
`Observed` are served as three values and agree only after completion. Scope and rollout slot are
released on `completed`.

**Negative controls.** A plan binding the control plane's proxied route: refused at creation (ER
§10.9). OpenBao running but sealed (`bin/inject seal openbao`, §2), and OpenBao stopped
(`bin/inject kill openbao`): the use-time check fails and is recorded with its result, and no
operation commits and nothing is sent until OpenBao is unsealed or started. An observation older than the plan's maximum age: no
evidence, so no commitment rests on it and no refusal is recorded; the commitment rests on a later
observation (ER choice §10.28). A second plan for the worker while the first holds the scope: the
controller does not take it up while the scope is held, and comparison 4 refuses it when its
transaction runs anyway (a Go check, the route ER choice §10.32 selects); with both scope indexes
dropped, the second plan's operation rows insert directly (DS row 011's control, the schema's). An assignment change while the scope is held: its publication fails
`409 scope-busy`. Every publisher has node access for fresh publication observations: A and B
are joined only after the first operation is confirmed to hold the scope, stalled before its
attempt. All three worker links are cut again before restoring the approved-plan snapshot.
A frozen scope: refused (a Go check; S5 shows the controller passing over such a plan).
An artifact failing node validation: `InvalidArgument`, `rejected`,
resource version unchanged (DS row 022). The ownership check moved out of the attempt's `UPDATE`: a
stale attempt is recorded (DB row 018).

The commitment-level controls of the approval part (S3 keeps those before commitment), each from
a restore of S3's step 4 snapshots with the S2 release planned as in S3 step 2. Each approves the
plan as in S3 step 3 before the change it names, except the two that say the approval follows it.
Pause A and B before these controls and keep them paused throughout; C alone runs their
publication and execution workers. Requests for these controls go to C.
For the assignment-change case, C joins the worker and stalls at `dispatch-evidence` after
approval, before commitment. Its separate publication worker can obtain fresh node observations
while the assignment changes; releasing the stall reaches the comparison-2 refusal. For the
later Desired-release case, C joins while the plan is still proposed, publishes the new release,
and only then receives approval of the earlier plan.
The worker's assignment changed after planning: commitment refused by comparison 2. A later
release published for the worker from a draft opened for it (as in S3 step 4), then the plan
approved, which does not compare `Desired`: commitment refused by comparison 2, since `Desired`
changed, and nothing sent; this step's commitment of the same plan with no publication between is
the control (ER choice §10.24); the publication racing the commitment itself is a *check* (§7.1).
A plan cancelled after commitment with no attempt: the operation goes `unresolved`, then
`cancelled`, and nothing is sent (ER §9.2). A revocation started while a commitment holds the
approval waits for it. Then either the attempt is refused by comparison 1 and nothing is sent, or
the attempt takes the machine row first, is recorded before the revocation and is sent (DS row 003,
ER choice §10.31). The revocation also waits on the machine row and plan state the commitment
holds, so no unlocked control applies. Row 006's control revokes after the commitment, since a revoked plan cannot commit (ER
choice §10.31).
The approver's identity revoked before commitment, and after it with no attempt: the plan cannot
commit; or its operation goes `unresolved` with its scope held, each transition on the timeline,
then `cancelled`, and nothing is sent (ER §3.3). OpenBao paused (`bin/inject pause openbao`) so that
the executor's use-time check (ER §3.1 item 2) holds commitment, the plan then approved, then
`h-approver`'s `approver` group removed at the issuer and `h-approver`'s next approval, under a
fresh token without the role, `403`; with no operation yet on the timeline, `bin/inject unpause
openbao`: the plan commits and its attempt is admitted (PA choice §17.23); S3's approval revocation
before commitment, which refuses, is the contrast.

**Retained evidence.** The operation and its timeline, observation records, the node's digest and
resource version per step, and the DS re-run table.

### S5. Drift: detect, freeze, sanitized adoption, approved revert

[ginsys/bronzeward#27](https://github.com/ginsys/bronzeward/issues/27). Design §18.2 item 6,
[§12.4](../design/Talos_Configuration_and_Machine_Management_Design.md#124-drift-policy). Closing
run: **drift freeze, sanitized adoption and approved revert**
([ER §9.3](execution-recovery.md#93-gaps-carried-and-what-would-close-them) item 1).

**Preconditions.** S4 passed; no drift record is open and no operation holds the worker's scope.
The instances are restarted with a `driftInterval` of seconds, set back to the fixture's when S5
ends: S1 to S4 run with none observing.

**Steps.**

1. With the fixture's pinned `talosctl`, patch the worker out of band in no-reboot mode, adding a
   registry auth password (`machine.registries.config.<host>.auth.password`) that is a new synthetic
   secret. The pinned Talos applies a registries change immediately but not a new file, and the
   executor sends only no-reboot applies, so step 6's revert of a file would be refused.
2. The next `drift` observation opens a drift record and raises the digest-mismatch alert; the scope
   is not frozen by detection.
3. `h-author` freezes the scope.
4. **Adopt.** `h-author` opens a new draft (`POST /drafts`), since the import draft is published,
   and ingests the drifted configuration into it as a drift adoption (`POST /ingestions` with
   `"kind": "drift-adoption"` and `"source": "machine"`, naming that draft, with `If-Match` its
   ETag), marking the new password, S1's file content and S2's node label of it. The adopted import
   base then holds that label as a reference, which S2's label fragment would override (refused,
   C choice §16.20), so `h-author` takes the fragment out of the `workers` profile in the same
   draft, or, where it is the only fragment the profile lists (a profile lists at least one),
   points the worker's assignment back at S3's handover cluster fragment. After the ingestion succeeds, `h-publisher` reads the draft's new ETag, publishes it with that ETag and creates an
   `adopt` plan binding the drift record; `h-approver` approves; after an `evidence` observation begun after the approval, the adoption
   record commits under the freeze.
5. Measure whether an artifact compiled from the unchanged import base reproduces the baseline
   digest.
6. **Revert.** Patch the worker out of band again; the next `drift` observation opens a new drift
   record. `h-publisher` creates a revert plan binding that record and its drifted digest; `h-approver` approves it, recorded as approving an
   unseen overwrite, and unfreezes; the revert runs as S4.
7. From one restore of snapshots taken before step 3 (`bin/inject db-snapshot s5-step3`,
   `bao-snapshot s5-step3`; restored with `db-restore s5-step3`, `bao-restore s5-step3`),
   `h-publisher`, `h-approver`, `h-recovery` and the automation identity each freeze the scope,
   `h-approver` unfreezing it after each (design §13.7's Freeze rows). This step stays out of the integrated run.

**Clauses exercised.** ER [§6.1](execution-recovery.md#61-detection),
[§6.2](execution-recovery.md#62-freeze), [§6.3](execution-recovery.md#63-adopt),
[§6.4](execution-recovery.md#64-revert), [§6.5](execution-recovery.md#65-drift-states),
[§8.6](execution-recovery.md#86-drift-freeze-then-adopt-or-revert); C
[§2.3](compilation.md#23-pipeline) (drift adoption); PA
[§9.2](persistence-api.md#92-resources-and-routes).

**Pass criteria.** The drift record names the observation, `Applied`'s digest and the observed one;
`Desired`, `Applied` and `Observed` stay distinct throughout. While the record is open and the
scope frozen, Bronzeward sends no Talos request that changes the machine, only the reads that
observations and the drift adoption's ingestion make: the resource version changes only with the out-of-band patches
and the revert. After step 4, `Applied` is the adopted release with the baseline's digest, the
baseline revision has advanced and the record is closed `adopted`; the new secret is found nowhere
outside OpenBao, on success, rejection and each interruption of the ingestion. After step 6, the
record is closed `reverted` and the digest is the revert artifact's. Step 5's result is recorded
whichever way it falls. Each of step 7's freezes and unfreezes takes effect and is recorded under
the role that permits it, as step 3's freeze is.

**Negative controls.** An approved revert plan while the scope is frozen (step 3): the controller
passes over it, so nothing is observed for it and no operation exists, then cancelled; comparison
6, which refuses a frozen scope's plan that reaches commitment, is a Go check. An approved ordinary plan while the record is open: refused by comparison 6.
A plan expecting `Applied`'s digest while the machine runs another: commitment refused on its
evidence, and the refusal's transaction opens a drift record (ER choice §10.23). Unfreezing by a
non-`approver`: `403`. No adoption record on a stale observation, which is no evidence and refuses
nothing (ER choice §10.28). The adoption record refused on a machine changed again,
a post-approval observation showing another machine identity with a matching digest, a revoked
approval, a changed baseline revision, a changed `Desired` and a record already closed by
a revert. A revert refused by re-drift before commitment, and one refused after a manual return,
whose refusal closes the record `returned`. A read begun before an `Applied` change opens no
record.

**Retained evidence.** Drift records and alerts; freeze, adoption and revert records; scan bundles
for each adoption run; step 5's measurement; the resource version series.

### S6. Interruption: executor loss, uncertain send, takeover

[ginsys/bronzeward#28](https://github.com/ginsys/bronzeward/issues/28). Design §18.2 item 7,
[§12.5](../design/Talos_Configuration_and_Machine_Management_Design.md#125-durable-operations-and-uncertain-outcomes).

**Preconditions.** S4 passed; instance A running and B stopped; an approved plan for the worker.
In the fixture, S6's A is instance C, which runs the build with the interruption seam (B runs it
too, for S7's held request, and is not interrupted here), and the fixture's A
is paused throughout; in step 4 A sends and B is started.

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
4. Partition the worker during a send over its own endpoint, the only dispatch route (ER §10.9):
   the response is lost. Start B: it takes the operation over at its start (ER §3.4), so the
   executor that sent can record no further attempt (ER §5.2 item 1). Then heal the partition,
   so the worker is readable again.
5. After the takeover and the heal, `h-recovery` records the accounting decision after the settle floor, with a recovery observation
   and the resource version read at both ends of the settle; a completion observation classifies
   the operation.
6. While one operation is `unresolved`, try a newer plan for the worker.
7. Once no operation holds the worker's scope, stop B. For one more plan, kill A after commitment
   and before any attempt, and cut B's link to the worker (`bin/inject linksplit B worker`, §2)
   before starting B: B takes the operation over, `unresolved` (ER §4), and cannot take the
   recovery observation that would classify it safe to retry. `h-recovery` resolves it `cancelled`
   on a recorded reason (`POST /operations/{id}/resolutions`); then
   `bin/inject linkjoin B worker`.

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
step 6's plan waits: no controller takes up a plan whose scope is held, so no use-time check,
commitment or refusal names it (comparison 4 refusing it is DS row 010, a Go check, ER choice
§10.32). A retry is admitted only with attempts left, the approval
passing and the gate open, and never after a newer timeline fact. Step 7's operation ends
`cancelled` with no attempt recorded, the resolution recorded with `h-recovery`, its role
`recovery-admin`, the reason and the evidence relied on (ER §4), and its scope released.

**Negative controls.** Accounting by `h-approver`: refused. Accounting before the settle floor:
refused (DS row 013; in S6 a takeover at B's start precedes the first read, so no live request ends
inside the floor). The fence without its owner and generation comparison: the stale owner records
an attempt (DS row 008). The scope index removed: the newer plan commits while the old one is
uncertain (DS row 011).

#### S6.2 Late-landing bound measurement

Closing run: **the bound on when an abandoned request can no longer land**
([ER §9.3](execution-recovery.md#93-gaps-carried-and-what-would-close-them) item 2).

**Steps.**

The proxied route is not a PoC dispatch route (ER §10.9): its landings are measured with the E4
harness, not through the executor, and its result is the input to any amendment re-admitting it.

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

The measurement and its distribution are in the
[late-landing report](../design/research/20261016-late-landing.md); it adopts no bound.

**Retained evidence (S6).** Per point: the kill time, the timeline, the classification and scope
state. Per capture: partition timing, the resource version series and the landing time.

## 5. Restoration recovery

### S7. Restore, recovery start, entry, per-scope release, exit

[ginsys/bronzeward#29](https://github.com/ginsys/bronzeward/issues/29). Design §18.2 item 7,
[§14.6](../design/Talos_Configuration_and_Machine_Management_Design.md#146-explicit-recovery-mode-after-restoration).
Closing run: **a restoration run with a missed stale instance**
([ER §9.3](execution-recovery.md#93-gaps-carried-and-what-would-close-them) item 3).

**Preconditions.** S4 passed; both machines have `Applied`. Before time *T*, an ingestion claim is
taken, a worker plan is approved but not committed (the pre-entry plan; ER §1 supports no
control-plane `apply-config` plan), and A commits another worker plan and is paused before its attempt (S6.1's stale owner). At *T*:
`bin/inject db-snapshot s7-t`. After *T*: B starts, takes the operation over, classifies it safe to retry
on a recovery observation, records an attempt and sends its request, and is held after the send
with the response unrecorded, so the request lands and the scope stays held (the fixture's B also
runs the build with the interruption seam, as S6's A does); a second worker plan is approved; an approval is revoked; `h-all`'s subject is added to
`deniedSubjects` and then its identity is revoked, leaving `h-approver` for step 9; then
`bin/inject bao-snapshot s7-provider`.

**Two runs.** The **nominal run**, part of the integrated run (§2), follows ER §7.2 and §7.3 step 1:
at step 1, A is stopped as well as B and established stopped before either restore, and stays
stopped; step 4 is skipped, and the pass criteria and controls about A do not apply. The **closing
run** that ER §9.3 item 3 names, with a missed stale instance, runs the steps as written, from a
fresh `bin/up` outside the integrated run: it breaks quiescence on purpose, to show the fences
refusing what an operator missed. A stale process records no refusal entry, so the closing run
shows each of A's refusals by what it leaves and what A logs: no attempt, the queued job still
queued, the pre-entry plan waiting, `503 epoch-superseded`, the second draft's ETag unchanged.

**Steps.**

1. **Restore.** Stop B and record the time. A stays paused, not stopped: it is the missed stale
   instance, whose owner generation the restore makes current again. Restore the database to *T*
   and OpenBao to its snapshot (`bin/inject db-restore s7-t`, `bao-restore s7-provider`), paired as
   PA §12.3 requires:
   the provider backup no older than the database's.
2. **Recovery start.** Start B with the recovery-start flag.
3. **Entry.** `h-recovery` records entry through the API, naming both restored backups, `database`
   and `provider`, with their ages; a new epoch is minted. Entry fails every queued `publish` job
   (PA §12.2). Through B, `h-author` opens a draft with a worker change and `h-publisher` publishes
   it; B's recovery start runs no job worker, so the new-epoch `publish` job stays queued.
   `h-author` opens a second draft through B and keeps its ETag.
4. Unpause A: it tries the attempt it was paused before, and its executor and job worker each find
   the epoch superseded, log it and stop until restarted, so A claims no `publish` job and commits
   no plan; the pre-entry plan waits. Through A, `h-recovery` requests a takeover of the restored
   operation and `h-author` starts an ingestion into the second draft with its ETag; both answer
   `503 epoch-superseded`.
5. Read the worker's configuration digest: it shows the held request's artifact, which landed
   before B was stopped. The closing run must show that landing detected (ER §9.3 item 3), and the
   settle floor is not a bound (ER §5.2): a request landing after step 7's accounting is not seen
   by the marking, so the request is held after it landed, not by pausing the worker, which in the
   [late-landing report](../design/research/20261016-late-landing.md) never let one land.
6. `h-recovery` re-records the identity revocation; the automation token is reissued with the
   server-side tool. Through B, `h-recovery` requests a takeover of the restored operation, which
   entry's takeover already gave B: it stays `unresolved`, and its generation advances with the new
   epoch as its owner epoch, issued under the epoch B adopted at entry (PA §5.1). `h-author` starts
   an ingestion into the second draft through B, with the same ETag, which A's refused request left
   unchanged.
7. Account every scope with one decision after the settle floor, using the maximum transport
   deadline for the attempt the restore erased, each with its recovery observation; check
   dependencies under the recovery identities. Then pause the control plane, take `restoration`
   observations, reclassify the restored operation and mark the scopes.
8. **Release** the worker scope.
9. Unpause the control plane and establish that its Talos API answers, keeping its recovery scope
   `blocked` and unreleased. Publication requires fresh observations of every covered node
   (PA §6.1.1); restored node access does not mark or release the scope. **Restart** B without
   the flag: the recovery start runs no executor and no job worker, and entry has committed.
   B's job worker runs the `publish` job queued in step 3, whose worker change T3 admits only
   because the worker scope is released (PA §12.2); it changes the worker's assignment and
   `Desired`. Through B, `h-publisher` and `h-approver` plan and approve, from that release, a
   worker change in the new epoch: a revert binding the drift record the held request's landing
   opened, as in S5 step 6. B commits it and applies it as S4; A, whose executor stopped in step 4,
   takes no part in it. Check that the control-plane scope remains `blocked`, with no release
   event in this epoch, before publication, after publication and after the completed revert.
10. After a successful `restoration` observation, re-mark and release the control-plane scope.
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
abandoned. A's attempt, under its pre-entry fence token, is refused by the fence: entry's takeover moved
the owner, generation and epoch; its job
claim, takeover and ingestion start are refused by the process-epoch comparison (PA §5.1). No
plan is committed by A: the pre-entry plan, its approval in the old epoch on a scope not released
(comparisons 1 and 6), is never a dispatch candidate and waits. A stale process's commitment of a
new-epoch plan on a released scope, which passes every other comparison, is refused by the
process-epoch comparison, a *check* (negative controls), since A's executor stops at its first
superseded epoch. In step 6 B, after its own entry, takes over with the new epoch as the
owner epoch and starts an ingestion in the new epoch (PA §16). The restored operation, whose
journal holds no attempt, ends `cancelled`; the held request's landing is recorded as drift at
the scope's marking, not applied over, and reverted only by the step 9 plan. The revoked approver is refused before and after entry, and
every pre-restore automation token until reissued. The worker scope is `ready`, is released and
dispatches under a new-epoch approval while the control-plane scope remains `blocked` from its
failed restoration mark, although its node is reachable for publication reads. Only step 10's
new mark and release clear that scope. Exit succeeds only once both scopes are released, and no
restored operation was retried.

**Negative controls.** A plan created and approved in the new epoch on the worker scope once it is
`ready` but before its release: its commitment refused by comparison 6 until the release, and
committed with the scope gate removed (ER §7.5), as *checks*, since B's recovery start runs no
executor. Plan creation and approval on a pre-restore
unaccounted scope, and release
of a scope not `ready`: refused. A pre-entry approval: refused by comparison 1, shown as the plan waiting and as a *check* of the
comparison. A second entry under
another key in the same recovery start: `409`. Exit with a scope unreleased: refused. Entry's
takeover also advances the generation, so the epoch term is isolated by a separate *check* that reproduces
DB row 027's collision in the new epoch: A's pre-restore token was issued by takeovers after the
snapshot, and takeovers after entry reissue the same owner and generation (DB §4.7), so only the
epoch term tells them apart. A's write is refused with the epoch term and passes with it dropped. The process-epoch comparison dropped, as *checks*: a stale process's job claim, takeover,
ingestion start and commitment of a new-epoch plan pass.
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

**Preconditions.** The integrated run's S4 change, S5 drift and S7 restoration are on record, and
the served timeline retained from S6.1's lost-response operation (steps 4 and 5), which runs
separately (§2).

**Steps.**

1. As `h-viewer`, following the documented walkthrough of
   [ginsys/bronzeward#30](https://github.com/ginsys/bronzeward/issues/30), read the worker's
   timeline and each operation's events.
2. From the timeline alone, answer for each of the four: who approved what, what each attempt
   carried and when it was recorded, whether its sending is known from a response or accounting or
   stays unknown, what was observed, why the scope was held, who decided an accounting and on what, and how it
   ended.
3. Hold an event stream open past its token's expiry. Then, with a fresh unexpired token for the
   same subject, revoke the subject as PA §10.4 requires: the operator adds it to `deniedSubjects`,
   then `h-recovery` records the identity revocation. Resume the stream with `Last-Event-ID` and
   that token.

**Clauses exercised.** ER [§4.1](execution-recovery.md#41-timeline-content),
[§3.5](execution-recovery.md#35-talos-client-and-route) (response text); C
[§8.3](compilation.md#83-redaction); PA [§8.3](persistence-api.md#83-the-resource-and-its-progress),
[§10.3](persistence-api.md#103-authorization).

**Pass criteria.** Each answer is reached by following links from the operation or the machine, and
entries are in commit order within the scope. Response text is redacted or withheld, and no served
timeline holds a synthetic secret. The stream ends no later than the token's `exp`, and the
resumption with the unexpired token is refused for the revoked subject; the same resumption before
the revocation succeeds, the control. Because both the deny-list entry and the recorded revocation
refuse it, a separate *check* in a disposable environment records the revocation without the
deny-list entry and shows the revocation alone refusing the resumption. After S7 the `Bronzeward-Epoch` header differs from the pre-restore one. A
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
| Item 1: import without mutation, extraction before persistence, exact encrypted baseline | S1; S3's handover part (the baseline published and adopted) | [ginsys/bronzeward#22](https://github.com/ginsys/bronzeward/issues/22), [ginsys/bronzeward#25](https://github.com/ginsys/bronzeward/issues/25) |
| Item 2: native fragments, references, revisions, provenance, validation stages | S2 | [ginsys/bronzeward#23](https://github.com/ginsys/bronzeward/issues/23) |
| Item 3: immutable encrypted artifacts, exact dependencies, redacted review data, no apply | S2, S3 step 1 | [ginsys/bronzeward#23](https://github.com/ginsys/bronzeward/issues/23), [ginsys/bronzeward#24](https://github.com/ginsys/bronzeward/issues/24) |
| Item 4: authenticated API; plan and approval of one safe worker change | S0, S3 | [ginsys/bronzeward#21](https://github.com/ginsys/bronzeward/issues/21), [ginsys/bronzeward#25](https://github.com/ginsys/bronzeward/issues/25) |
| Item 5: direct dispatch by the E4 mechanism, verification, separate desired/applied/observed | S4 | [ginsys/bronzeward#26](https://github.com/ginsys/bronzeward/issues/26) |
| Item 6: external change detected; freeze, sanitized adoption, approved revert | S5 | [ginsys/bronzeward#27](https://github.com/ginsys/bronzeward/issues/27) |
| Item 7: interrupted execution | S6 | [ginsys/bronzeward#28](https://github.com/ginsys/bronzeward/issues/28) |
| Item 7: external restoration through explicit recovery mode | S7 | [ginsys/bronzeward#29](https://github.com/ginsys/bronzeward/issues/29) |
| Closing paragraph: selected database/provider behaviour (§7.7) | S0 to S8 on the §2 fixture | [ginsys/bronzeward#21](https://github.com/ginsys/bronzeward/issues/21), [ginsys/bronzeward#30](https://github.com/ginsys/bronzeward/issues/30) |
| Closing paragraph: scoped authorization (§13.7) | S0 steps 4 and 5 (every denial by role alone, the human-only refusals and the absent routes), and each other scenario-table row in the scenario S0 names for it: S2 step 7, S3 (its handover part included), S4, S5 steps 3 and 7, S6.1 step 7, S7 | [ginsys/bronzeward#21](https://github.com/ginsys/bronzeward/issues/21), [ginsys/bronzeward#23](https://github.com/ginsys/bronzeward/issues/23), [ginsys/bronzeward#25](https://github.com/ginsys/bronzeward/issues/25), [ginsys/bronzeward#26](https://github.com/ginsys/bronzeward/issues/26), [ginsys/bronzeward#27](https://github.com/ginsys/bronzeward/issues/27), [ginsys/bronzeward#28](https://github.com/ginsys/bronzeward/issues/28), [ginsys/bronzeward#29](https://github.com/ginsys/bronzeward/issues/29) |
| Closing paragraph: usable operation timeline | S8 | [ginsys/bronzeward#26](https://github.com/ginsys/bronzeward/issues/26), [ginsys/bronzeward#30](https://github.com/ginsys/bronzeward/issues/30), [ginsys/bronzeward#31](https://github.com/ginsys/bronzeward/issues/31) |
| §18.1 E6: the existing-cluster vertical slice | the integrated run (S0 to S5, S7's nominal run, S8) and S6's interruption runs (§2) | [ginsys/bronzeward#31](https://github.com/ginsys/bronzeward/issues/31) |

### 7.1 Required verification

Each contract ends with a list of checks an implementation must show, each with a control that can
fail: [C §15](compilation.md#15-verification-and-evidence-limits),
[ER §9.2](execution-recovery.md#92-required-verification),
[PA §16](persistence-api.md#16-verification-and-evidence-limits) and
[DM §10.1](dependency-monitor.md#101-required-verification). **Every item of those lists is
part of this plan.** The scenarios above carry the items on the integrated path; an item marked
*check* runs separately, from a fresh `bin/up` or a recorded snapshot (§2), under the issue named,
with its control and the retained evidence of §2. An issue is not complete while an item mapped to
it has no retained result, and ginsys/bronzeward#31 confirms the table row by row.

| Contract item | Where | Issue (ginsys/bronzeward) |
| --- | --- | --- |
| C §15: ingestion success, refusal and interruption at each pipeline step, every surface scanned | S1 (import); S5 step 4 plus *check* (drift adoption); S2 negative controls (draft update) | #22, #27, #23 |
| C §15: the compiler process's surfaces scanned over successful, rejected and interrupted publications, killed at each point before `COMMIT` while plaintext is held | S2 negative controls (publication); *check* for the stop at each point | #23 |
| C §15: claim lease extension by its owner only, takeover of `held` and `resumed` claims only after lapse, stale owner, crash inside the draft transaction, provider unreachable; the takeover, its refusals, a claim with nothing to decrypt, a decryption failure left `resumed` and retried, a digest mismatch, a takeover racing the old owner's draft transaction in both orders and concurrent takers are `TestTakeOver`, `TestTakeOverRefusals`, `TestTakeOverConcurrentTakers`, `TestTakeoverResumesToT1`, `TestTakeoverRefusals`, `TestTakeoverNothingToDecrypt`, `TestTakeoverDecryptFailureLeavesResumed`, `TestTakeoverDigestMismatchAbandons`, `TestTakeoverRacesT1`, `TestTakeoverConcurrentTakers` and `TestTakeoverNoEcho` | S1 negative controls plus *check* | #22 |
| C §15: SR and SP matrices through the compiler's path, with import-base references and SP's oracle over Bronzeward's own log and support formats; fidelity check | S2 precondition and negative controls | #23 |
| C §15 and PA §16: every refusal of C §13, every walk-through of PA §13 and refusal of PA §14 | the scenario of each clause's issue, plus *check* for the rest | #21 to #29 |
| ER §9.2: DS rows 001–023 through the implementation over the worker's endpoint, with controls 006, 008, 011 and row 013's early accounting refused; if the executor's fallback is taken, the rows through it and the leak scan of its channel | S4 precondition plus *check* for the scan | #26 |
| ER §9.2: sending only after the attempt commits; each §3.2 and §3.3 lock with its unlocked control | S4 and its negative controls (DS rows 003 and 011; row 003's lock has no unlocked control, ER choice §10.31) plus *check*: `TestCommitmentHoldsItsReads` and `TestAttemptHoldsItsReads` with their controls `TestCommitmentReadLockControls` and `TestAttemptReadLockControls`, the attempt's machine lock in `TestAttemptWaitsForTheMachine` with its control, `TestCommitmentRolloutWaitPrecedesTime` with `TestCommitmentRolloutLockControl`, and comparison 6 under the machine's lock, `TestCommitmentFreezeRace` | #25, #26 |
| ER §9.2: expiry, observation age, a contradicting newer observation, the attempt bound, each refusing; the bound exhausted after a lost response leaves no further attempt and ends `failed` | S3, S4 negative controls; *check* for the last two (the last is `TestSettle`'s "bound exhausted after a lost response") | #25, #26, #28 |
| ER §9.2: plan cancellation before commitment and after it with no attempt | S3 negative controls (before), S4 negative controls (after) | #25, #26 |
| ER §9.2: a plan for a release that is not the machine's `Desired` refused at creation; a commitment refused after a publication that changed `Desired`, with S4's commitment as the control; a publication racing the commitment waits for it or precedes it, with the unlocked control; a publication after commitment leaves the attempt admitted (ER §3.3) | S3 negative controls (the refusal at creation), S4 negative controls (the refused commitment) plus *check* for the race and the attempt (`TestCommitmentPublicationRace`, `TestCommitmentPublicationWaits`, `TestAttemptAccepts`) | #25, #26 |
| ER §9.2: a bound health check failing before the verification deadline then passing: `completed`; failing at or after it: `failed`; unreadable at it: `unresolved`; a contradicting digest before it: `failed` at once, the same transaction opening a drift record (ER §4) | *check*: the contradicting digest and the drift record its transaction opens are `TestSettle`'s "contradicted"; the PoC binds no health check, so the health cases wait for the first operation class that binds one (ER choice §10.33, tracked by #188) | #26, #27, #188 |
| ER §9.2: the fixture's nodes each reporting a distinct Talos node ID, no SMBIOS UUID and one Talos cluster ID through the application's read-only Talos client; a commitment and a retry's attempt refused, nothing sent, when the evidence shows another identity key, an SMBIOS UUID absent for a machine recorded by one or present for one recorded by node ID, or another Talos cluster ID; nothing committed or sent after a failed identity read (a failed `systeminformation` read included), missing evidence under ER choice §10.28; with the controls that a machine recorded by node ID whose `systeminformation` read returns no resource is admitted and that a machine recorded by SMBIOS UUID showing another node ID but a matching UUID and cluster ID is admitted, each SMBIOS case against a stubbed Talos response; a scope left `blocked`, not `ready`, by a `restoration` observation with another identity, then `ready` after a matching one (ER choice §10.26) | *check*: the commitment's cases and controls are `TestExecutorCommitmentIdentityKeys` with `TestCommitmentRefusals`, the retry's `TestSettle`'s "retry evidence identity" cases (another SMBIOS UUID, none, another Talos cluster ID, with the matching control; the node-ID cases share the commitment's comparison); the restoration half stays with #29 | #26, #29 |
| ER §9.2: an `apply-config` operation with no attempt not completed by a completion observation of its artifact applied out of band | *check* | #28 |
| ER §9.2: assignment change refused while the scope is held; comparison 6 under a freeze and under recovery mode before release | S4, S7 negative controls | #26, #29 |
| ER §9.2: takeover at start; the §5 precedence; PA §16: a takeover keeps `unresolved` and refuses a terminal operation | S6.1 plus *check* | #28 |
| ER §9.2: identity revocation before commitment, after it with no attempt, after an attempt with its retry refused | S4 negative controls; *check* for the last (`TestSettle`'s "identity revoked after an attempt") | #25, #26, #28 |
| ER §9.2: observation ordering, contradicting higher-basis reads, confirmed reads, the late-read residual | *check*: PoC identity, assignment and digest predicates; the bound-health ordering and confirmed-failure cases wait for the first operation class binding health, with #188 | #28, #27 (adoption read), #188 |
| ER §9.2: plan and commitment refusals on undetected drift; drift detection, freeze, adoption record and revert cases, with the leak screen (a record opened with a `failed` operation is the health-check *check* above) | S5 and its negative controls | #27 |
| ER §9.2: recovery-mode entry after a restore older than a takeover, an approval, a revocation and an attempt | S7 | #29 |
| ER §9.2: the complete existing-cluster E6 slice | the integrated run | #31 |
| PA §16: `FOR SHARE` at publication (DB row 011) | S2 negative controls | #23 |
| PA §16: ownership check inside the attempt's `UPDATE` (row 018) | S4 negative controls, plus *check* (`TestAttemptStaleOwner`, the read-then-write control recording a stale attempt) | #25, #26 |
| PA §16: claim eligibility re-check (row 020): the takeover's conditional `UPDATE` refusing a claim whose absolute expiry passed after it was locked, with the control that decides on the earlier read, is `TestTakeOverRechecksEligibility` | *check* | #22 |
| PA §16: the orphan report's identity, its policy read back and compared exactly, with the extra-stanza control; listing and reading generation metadata, refused every value read (the Talos access path included), metadata read outside the generation path, Transit request and change under the generation path (update, patch, latest-version `DELETE` and undelete included), each refusal with a control granting that request's capability on its path; a provider or database failure mid-run exiting nonzero with no path printed, with the print-as-listed control; the token file's mode, a FIFO and a symlink refused before any provider request; no route serving the report, with the registered-route control; a token file replaced within the run after configuration load read at use, with the read-once control; `--cluster` limited to its subtree, with the walk-everything control; its selection of unreferenced generations of claims that are not live (abandoned, released, or absent from the database), with the claim-state-only control; generations referenced through each kind of reference row, with the one-kind-only control; a release committed during the report, with the split-read control; a release committed after the claim's expiry, with the read-time-abandonment control; expired encrypted claims recorded `held` and `resumed` and a lease-lapsed transient claim with no controller running, listed apart, with the omit-every-live control; a lease-lapsed encrypted claim before its expiry neither reported nor listed, with the lapsed-lease-abandons control; a cluster a restore removed, found without `--cluster`, with the database-clusters-only control; a custom-metadata sentinel on no report surface, with the print-the-response control; nothing changed by a run (a full data dump compared), with the audit-row control | S1 negative controls plus *check* | #22 |
| PA §16: migration advisory lock (row 026); immutability triggers; startup refusal on each schema mismatch; authentication refusals; role checks that role alone decides, and the human-only refusals | S0, S2 pass criteria, plus *check* for startup | #21, #23 |
| PA §16: role checks whose outcome needs the route's handler or state, against the design §13.7 scenarios | S2 step 7, S3 (its handover part included), S4, S5 steps 3 and 7, S6.1 step 7, S7 | #23, #25, #26, #27, #28, #29 |
| PA §16: the epoch term and process-epoch checks; the recovery-start process in the new epoch; per-scope refusals and the recovery-start refusal | S7 | #29 |
| PA §16: one idempotency key in flight twice, with the key-lock control | *check* | #21 |
| DM §10.1 items 2 and 3: each provider object state (KV soft-deleted, undeleted, destroyed, pruned and its metadata deleted; a Transit version below the decryption floor and trimmed, the key deleted) classified through the metadata client under the fixture's policy, with exactly its alerts, and item 3's unseeded control: `TestLiveProviderStates`, run against the fixture's OpenBao | S2 negative controls | #24 |
| DM §10.1 items 2 and 8's publication refusal: OpenBao partitioned, sealed and paused, a pinned KV version soft-deleted then destroyed, their classifications and alerts and the refused publication | S2 step 6 and negative controls | #24 |
| DM §10.1 items 2 and 3 across a restore: a deleted Transit key and an OpenBao snapshot older than the database | S7 variants | #29 |
| DM §10.1 items 1, 4 to 7, 9 to 12, 14 and 15: every classification rule, persistent unknown, metadata-only access, one alert per transition, silence, Transit and KV identity, publication against a transition, logging after a crash, per-dependency staleness, overlapping monitors, the alert log after a database restore | *check* | #24 |
| DM §10.1 item 8's dispatch half: no dispatch admitted by a retained class | *check* (`TestExecutorUseTimeCheckFailure`, the artifact key retained throughout) | #26 |
| DM §10.1 item 13: recovery start records nothing before its entry commits | *check* | #29 |
| PA §16: approval revocation racing commitment (DS row 003) | S4 negative controls | #25, #26 |
| PA §10.4: an approval surviving its approver's loss of the role, contrasted with its revocation (choice §17.23) | S4 negative controls, contrasted with S3's approval revocation | #25, #26 |
| PA §16: identity revocation racing commitment, with the lock control; its timeline entries on exactly the machines it touches (T5c) | *check* | #25 |
| PA §16: a lapsed `publish` job claimed again | S2 negative controls | #23 |
| PA §16: leaving recovery mode racing an inventory request, with the `FOR SHARE` control | *check* | #29 |
| PA §16: concurrent rotations of one service identity, and rotation racing its revocation, with the principal-lock control | *check* | #21 |
| PA §16: a reissue after a restore refused for a service identity `deniedSubjects` lists | *check* | #29 |
| PA §16: a service identity's creation refused under a name `deniedSubjects` lists after the restore lost the listed identity's row, with the control; a revoked identity the restore kept, listed by identifier and name, refused as before with its row unchanged; an automation token refused because its identity's name is listed, with the control | *check*: `TestIssueAfterRestoreRefusedForDeniedServiceName`, `TestDeniedServiceNameWithoutRestore`, `TestAutomationDeniedByName` | #391 |
| PA §16: two inventory requests for one SMBIOS UUID under different keys, one refused, and the same for one Talos node ID and one Talos cluster ID; an inventory body with both identity keys, or neither, refused; missing or unsupported platform refused and release mode required to match the machine platform, with controls | *check*: identity cases plus `TestInventoryRefusals`, `TestInventoryPlatform`, `TestMachinePlatform` and `TestReleaseConstraints` | #22 |
| PA §16 Talos access, ingestion: the read grants with the compiler control, the write, list and delete refusals on the access path for both reading identities with the `update` control, the ingestion identity's `gen/*` refusal, every identity mismatch §3.3 lists, a failed `systeminformation` read included, with the controls that a machine recorded by node ID whose `systeminformation` read returns no resource is accepted and that a machine recorded by SMBIOS UUID whose node reports a matching UUID and cluster ID but another node ID is accepted, each SMBIOS case against a stubbed Talos response, every `talos-access-unavailable` cause with the error-text control, the version identity with the metadata-rewrite control, the access version and dialled endpoint on the `ingest` operation's events after a success and after a failure following the read, direct reads without `node` metadata, the client key in the scan, `TestLiveRoleProbe`'s `PermissionDenied` for `os:reader` and `os:operator` with the control that probes the `os:admin` configuration in the `os:reader` slot; the mismatches, the identity controls, the unavailability causes, the access event with its claim fence, and the error-text control over the problem, events, responses and log are `TestMachineIdentityMismatch`, `TestMachineIngestionIdentityControls`, `TestMachineAccessUnavailable`, `TestMachineIngestion`, `TestMachineAccessEventFencedByTheClaim` and `TestMachineAccessErrorTextControl`, and the scan of the database and temporary files over interrupted ingestions stays with S1 | S1 negative controls plus *check* | #22 |
| PA §16 Talos access, executor: its read at dispatch, recorded as path, version and `created_time`; every `talos-access-unavailable` cause failing the use-time check or the fresh observation with no operation committed and no mutation request sent, with the error-text control; a rotation between the evidence observation and the attempt, the attempt still using the version the observation used, with the control that reads the latest version at send; a rotation before a retry (another version number, or the same number with another `created_time`), the retry's evidence and attempt both using the new version; a rotation before an attempt made again after its transaction failed (either form), refused by comparison 3 and sent once on a new observation read with the new version; the client key in the scan of the database, logs, responses and temporary files over successful, refused and interrupted observations and dispatches; the talosconfig's own `endpoints` and `nodes` ignored | S4 (the scan, its refused and interrupted dispatches being the DS rows) plus *check*: `TestExecutorCompletes` (the recorded read, and every executor test's talosconfig naming unroutable `endpoints` and `nodes`), `TestExecutorUseTimeCheckFailure` and `TestExecutorNodeSideAccessFailure` (each cause), `TestExecutorAccessErrorTextControl`, `TestExecutorSendsWithTheObservedAccess` (the rotation and its latest-version control), `TestExecutorRetryFollowsAccessRotation`, `TestExecutorAttemptAfterAccessRotation` | #26, #172 |
| PA §16 Talos access, observations: each observation purpose recording the access version it read, and a failed access read recorded with its cause and no node values, with the stale-credential control; the observation client held to its read allowlist with the mutating-call control, a plan-less drift observation succeeding, the mutating client refused after revocation | S4, S5, S7 plus *check* (#26's part: `TestObservationRecordsTheAccessItRead` with its stale-credential control, `TestObserveAccessReadFails`) | #26, #27, #29 |
| PA §16 Talos access, endpoint: the grammar refusals on both routes, a DNS name among them, with the IPv4 and IPv6 controls; a replacement after plan creation leaving that plan bound to the old endpoint and binding later plans to the new one, that plan's observations dialling and recording the old endpoint with the current-endpoint control, change, timeline entry and act atomic, with the dispatch-time-resolution control; the change read back from the machine's timeline; the replacement accepted in recovery mode | *check* | #22, #25, #29 |
| PA §16: machine revisions allocated in commit order under concurrent writers, with the unlocked control | *check* (`TestRevisionsInCommitOrder`) | #26 |
| PA §16: an event stream ended by its token's `exp`, resumption refused after revocation | S8 step 3 | #26 |
| PA §16: no request body in the data directory, write-ahead log or backups | S1, S2 scans | #22, #23 |
| PA §16 and C §15: the operator review (C §3.6): transient review refused, pause and its lease end, an owner heartbeat on a paused claim, the review route's roles, `no-store`, states and decryption and integrity failures, a mark extracting a sentinel, a mark on a mapping or an identified embedded document holding a reference refused, a malformed mark path holding a sentinel, a mark preserving the baseline digests, a mark's or continuation's decryption and integrity failures, a mark refused before, at and after its first provider write, a refusal naming the mark by position, a mark that would rewrite other text refused, racing marks, a paused claim's takeover refused, a mark's run killed at each step it re-enters and a continuation's run killed, each resumed, a mark's and continuation's 202 body holding no path or sentinel, a continuation on a moved, discarded or publishing draft, abandonment, expiry, recovery-mode entry and restart, an expired paused claim in the orphan report, each with its control and every surface scanned | S1 review rows plus *check* | #125 |

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
