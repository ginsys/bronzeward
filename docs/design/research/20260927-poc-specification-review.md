# PoC specification review

Specification review for [ginsys/bronzeward#20](https://github.com/ginsys/bronzeward/issues/20). It
reads the three milestone-02 contracts against design
[§18.1 and §18.2](../Talos_Configuration_and_Machine_Management_Design.md#18-phased-implementation-and-proof-of-concept),
[§13.7](../Talos_Configuration_and_Machine_Management_Design.md#137-poc-identity-and-approval-policy)
and the [feasibility evidence review](20260925-feasibility-evidence-review.md), traces each required
property to evidence, contract clause and acceptance scenario, checks the contracts against each
other, lists the owner-review choices with a recommendation for each, maps every carried gap to an
issue or to later work, and gives the refinement text for
[ginsys/bronzeward#21](https://github.com/ginsys/bronzeward/issues/21) to
[ginsys/bronzeward#31](https://github.com/ginsys/bronzeward/issues/31). It decides nothing: the owner
or designated reviewer accepts the specification (criterion 5), and the refinement labels come off
only when the issues carry the refined text.

Read at `main` aa58272 (2026-09-27), with the
[acceptance plan](../../spec/acceptance-plan.md) and the E3 disposition wording of §8 landing in the
same change as this report.

## 1. Question

Is the PoC specification consistent, traceable and implementable for the selected profile? Put
narrowly: for each property design §18.2 requires, does a contract clause specify it, does evidence
support it, and does a scenario of the acceptance plan show it; where two contracts meet, do they
say the same thing; and what does each implementation issue need before its refinement label comes
off?

## 2. What binds every conclusion

Carried forward from the feasibility evidence review
([FR §2](20260925-feasibility-evidence-review.md#2-what-binds-every-conclusion-here)) and from the
issue:

- **Upgrade/LifecycleClient execution testing is deferred and design experiment E3 has not passed.
  PoC compatibility is not full-E3 completion** (§8).
- The fixture limits bind every conclusion: exact-copy leak scanning, one Talos version, containers
  without reboot, upgrade, reset, installer or disk behaviour, one control plane and one worker
  ([fixtures report §7](20260919-investigation-fixtures.md#7-limits)).
- A contract clause is a specification, not evidence. "Specified" below never means "shown"; only
  an executed acceptance scenario turns a specified property into an evidenced one, and only within
  the fixture limits.
- Licence selection (S04) is independent and does not block this review.

No row below is a production claim.

## 3. Sources

| Key | Source | Read at |
|---|---|---|
| C | [compilation contract](../../spec/compilation.md) (ginsys/bronzeward#17) | aa58272 |
| PA | [persistence and API contract](../../spec/persistence-api.md) (ginsys/bronzeward#18) | aa58272 |
| ER | [execution and recovery contract](../../spec/execution-recovery.md) (ginsys/bronzeward#19) | aa58272 |
| AP | [acceptance plan](../../spec/acceptance-plan.md) (ginsys/bronzeward#20) | this change |
| FR | [feasibility evidence review](20260925-feasibility-evidence-review.md) (ginsys/bronzeward#12) | aa58272 |
| Design | [design document](../Talos_Configuration_and_Machine_Management_Design.md) | aa58272 |

The E-reports (Fx, E1, SR, SP, E3, DB, DS, PC, RC, KL, OM) keep the keys, issues and pins of
[FR §3](20260925-feasibility-evidence-review.md#3-sources); FR §3.1 establishes that every
fixture-based capture carries the same `fixtures/versions.env` blob, which is unchanged at aa58272.
Line numbers in §6 are at aa58272. The repairs of §7 moved some of those lines; the section cited
with each stays the reference.

## 4. Properties

One row per property the PoC must have, drawn from the numbered §18.2 items and its closing
paragraph, the §13.7 policy items, and FR's SP rows (FR §4) where they bear on the PoC.

| ID | Property | Design | FR |
|---|---|---|---|
| PR01 | Import reads the live configuration without mutating it | §18.2 item 1 | SP01 |
| PR02 | Extraction precedes every durable write on import and drift adoption, across success, rejection, crash and restart; protected staging has a recovery owner | §18.1 E1; §18.2 item 1 | SP02, SP03 |
| PR03 | Schema-based detection assessed without a completeness claim; marking load-bearing | §18.1 E1 | SP04 |
| PR04 | Exact encrypted baseline | §18.2 item 1 | SP05 |
| PR05 | Composition with references reaches native merge parity, no custom merge semantics | §18.2 item 2 | SP06 |
| PR06 | Named validation stages; materialized validation | §18.2 item 2 | SP07 |
| PR07 | Sensitivity provenance; redaction not solely by value matching | §18.2 item 3 | SP08 |
| PR08 | Exact dependency records, effective and reproduction | §18.2 item 3 | SP09 |
| PR09 | Stale-revision writes rejected | §18.2 item 2 | SP12 |
| PR10 | Publication atomic and idempotent | §18.2 item 3 | SP13 |
| PR11 | Immutable encrypted per-machine artifacts | §18.2 item 3 | SP32 |
| PR12 | Publication, checks or a `retained` verdict alone cannot dispatch | §18.2 item 3 | SP14, SP25 |
| PR13 | Dependency classification and alerts; unknown never pass or lost | §18.2 closing (§7.8) | SP25, SP26 |
| PR14 | Minimal authenticated API; authorization scoped by capability | §18.2 item 4 and closing; §13.7 items 1–2 | SP30 |
| PR15 | Plan bound to the exact artifact and assignment; one approval; self-approval recorded | §18.2 item 4; §13.7 item 3 | SP17 |
| PR16 | Approval and identity revocation reach what §13.7 item 4 states | §13.7 item 4 | SP17 |
| PR17 | Unique durable intent; ownership loss fenced; A-after-B overwrite prevented | §18.1 E4 | SP15, SP18, SP19 |
| PR18 | Direct dispatch by the E4 mechanism, verification, separate desired/applied/observed | §18.2 item 5 | SP10, SP21 |
| PR19 | A node whose observed identity differs from the plan's is refused before any send | §18.2 items 4–5 ("exact ... assignment") | none |
| PR20 | Drift detected; freeze, sanitized adoption and approved revert | §18.2 item 6 | SP22 |
| PR21 | Interrupted RPCs and reconnects end safe or explicitly unresolved | §18.2 item 7 | SP20 |
| PR22 | A bound on when an abandoned request can no longer land | §18.2 item 7 | FR §9 item 3 |
| PR23 | Restoration recovered through explicit recovery mode; conflicting work stopped | §18.2 item 7; §13.7 item 5; §14.6 | SP23 |
| PR24 | Usable operation timeline | §18.2 closing | SP30 |
| PR25 | Selected database/provider behaviour, including migrations | §18.2 closing (§7.7); §18.1 E4 | SP16, SP24, SP28 |
| PR26 | Upgrade/LifecycleClient transition | §18.1 E3 | SP11 |
| PR27 | The E6 existing-cluster vertical slice | §18.1 E6 | SP31 |

Not a PoC property: enrolment, reset/reuse, bootstrap, upgrades, remote transports and
managed-cluster etcd recovery (§18.2 exclusions; AP §8).

## 5. Matrix

Status codes:

- **evidenced**: FR grades the property P for the tested mechanism, and a contract clause specifies
  that mechanism.
- **specified-not-evidenced**: a contract clause (or, for PR13, a decided design policy) specifies
  it; evidence is partial or absent; the named scenario is what would show it.
- **gap**: no clause specifies it, or the clause substitutes an operator decision for the missing
  property. §10 assigns each.

| ID | Evidence (FR grade) | Clause | Scenario | Status |
|---|---|---|---|---|
| PR01 | E1 (P) | C §2.3; ER §6.3 | S1 | evidenced |
| PR02 | E1 (p); pipeline interruption never measured (C §15) | C §2.3, §3 | S1, S5 | specified-not-evidenced |
| PR03 | E1, SP (p) | C §2.1, §2.4 | S1 | specified-not-evidenced |
| PR04 | E1 (P) | C §2.3 step 8 | S1 | evidenced |
| PR05 | SR (P at the SR boundary); machinery parity inferred (FR §8 C4) | C §5, §6, §10.1 | S2 | specified-not-evidenced: the selected machinery path is the C §10.1 condition |
| PR06 | SR (P), E3 (p) | C §7 | S2 | specified-not-evidenced |
| PR07 | SP (P), E1 (p) | C §8 | S2, S8 | specified-not-evidenced |
| PR08 | SP (P), SR (p) | C §9 | S2 | specified-not-evidenced |
| PR09 | DB (P) | PA §4 | S2 | evidenced |
| PR10 | DB (P) | PA §6, §7 | S2 | evidenced |
| PR11 | PC (p) | C §11; PA §3, §6.2 | S2 | specified-not-evidenced |
| PR12 | PC, RC (p) | ER §2; C §11; PA §6.2 | S2, S3 step 1 | specified-not-evidenced |
| PR13 | RC (P), PC (P) cells; overall p | design §7.6, §7.8, §15.3; PA §3 (DependencyStatus); C §6 step 3 | S2 | specified-not-evidenced; no contract section owns the monitor (§10) |
| PR14 | none (G) | PA §9, §10 | S0, S3 | specified-not-evidenced |
| PR15 | DS (p) | ER §2; PA §8.1, §10.5 | S3 | specified-not-evidenced |
| PR16 | DS rows 002–005 for approvals (p); identity revocation unmeasured | ER §3.2, §3.3, §8.5; PA §10.4 | S3, S7 | specified-not-evidenced; approval half evidenced |
| PR17 | DB (P), DS (P, p) | ER §3.2, §3.4; PA §5.1 | S4, S6.1 | evidenced |
| PR18 | DS, E3 (p) | ER §1, §3.5, §4 | S4 | specified-not-evidenced: the machinery client is the ER §3.5 condition |
| PR19 | none | none: an open point in PA §7.3 | none | gap |
| PR20 | none (G) | ER §6; C §2.3 | S5 | specified-not-evidenced |
| PR21 | DB (P), DS (p) | ER §5 | S6.1 | specified-not-evidenced |
| PR22 | DS: late landings in three of six partitions (FR §7) | ER §5.2: an operator decision after a settle floor, not a bound | S6.2 | gap |
| PR23 | DB (N), KL (p), DS (G) | ER §7; PA §12; C §3.5 | S7 | specified-not-evidenced; the epoch answering the N finding is inferred (DB §9) |
| PR24 | none (G) | ER §4.1; PA §8.3 | S8 | specified-not-evidenced |
| PR25 | PC (P), DB (p) | PA §5, §11 | S0 to S8 | specified-not-evidenced |
| PR26 | none (G) | none: deferred | none (AP §8) | gap, beyond the PoC (§8) |
| PR27 | none (G) | AP | the integrated run | specified-not-evidenced |

**27 properties:** evidenced 5 (PR01, PR04, PR09, PR10, PR17), specified-not-evidenced 19, gap 3
(PR19, PR22, PR26). Every §18.2 item and closing-paragraph requirement has at least one scenario
([AP §7](../../spec/acceptance-plan.md#7-coverage)).

## 6. Cross-contract consistency

Checked where two contracts state the same fact. Paths are under `docs/spec/`, lines at aa58272.
Items 1, 2, 4, 6 and 7 are repaired by
[ginsys/bronzeward#56](https://github.com/ginsys/bronzeward/pull/56), which lands before this
report; item 3 stays a gap (§10); item 5 needs no change.

**Inconsistent or open at aa58272:**

1. **Configuration-digest hand-off called open, but landed.** `persistence-api.md:104-110` (PA §1.1
   item 2) says the compilation hand-off of the plaintext and baseline configuration digests is open
   "until that amendment lands"; `compilation.md:159-166` (§2.3 step 8) and `compilation.md:971-973`
   (§11) carry both. PA's gap bullet `persistence-api.md:2059-2061` is stale for the same reason.
2. **What the recovery start serves before entry.** `execution-recovery.md:1129-1135` (ER §7.2) says
   the recovery-start controller serves observation, the recovery API, drafts, ingestion,
   compilation and publication, without distinguishing before and after entry;
   `persistence-api.md:1616-1625` (PA §12.2) serves only liveness and entry until entry commits, and
   `persistence-api.md:1935` (PA §14) refuses every other request. PA is stricter and is what AP S7
   tests; ER needs the same before-entry limit. ER §7.2 also allowed the normal restart only after
   the first released scope, PA §12.2 after entry; the repair aligns both (§7).
3. **Pre-send identity check.** `persistence-api.md:869-873` (PA §7.3) records that no contract
   refuses a node whose observed identity differs from the plan's before the first send; in
   `execution-recovery.md:163-167` (ER §2) machine identity is a postcondition, not a precondition,
   so ER §3.2 comparison 3 does not check it. Property PR19; a gap (§10).
4. **Ingestion role wording.** `execution-recovery.md:953-955` (ER §6.3) gives the ingestion to
   `author`; `persistence-api.md:1431-1436` (PA §10.3) requires `author` held by a human. ER cites
   PA's rule, so the effect agrees; the word "human" is missing.
5. **"Self-approval undetermined".** `execution-recovery.md:223-225` (ER §2) keeps it as a guard;
   `persistence-api.md:143-146` (PA §1.2 item 9) says it never arises, because every automation
   identity names a responsible human. Consistent in effect; PA asked ER to state that it never
   arises.
6. **The generation-path component.** `compilation.md:146-150` (C §2.3 step 6) requires a component
   the database does not issue without naming it; PA §17.8 names it (claim id plus a random value
   id) and says it "Needs a line in compilation §2.3 step 6".
7. **Parallel-revision note.** `persistence-api.md:42-44` says ER "is being revised in parallel";
   ER has landed.

**Consistent** (checked, no action): PA §1.2 item 8 and ER §7.1 (`execution-recovery.md:1097-1100`)
on reissued identifiers; ER §7.5's publication row (`execution-recovery.md:1277`) and PA §12.2's
assignment-change exception (`persistence-api.md:1683-1688`), which applies ER's own assignment
rule; and the choice pairs in §9.

## 7. Carried findings

The findings carried into this review on
[ginsys/bronzeward#20](https://github.com/ginsys/bronzeward/issues/20): two from the final head of
the first execution-recovery pull request (A, B), the post-round findings of the persistence (J–N)
and execution-recovery (C–I) pull requests, and six review threads declined there. Each was
re-verified against aa58272; the repairs are
[ginsys/bronzeward#56](https://github.com/ginsys/bronzeward/pull/56).

| ID | Finding | Disposition |
|---|---|---|
| A | ER §4: an attempt already sent when its executor dies can land after a newer operation | Addressed as disclosed: ER §5.2 states the residual, a late landing is detected as drift, not prevented; ER §9.3 gap 2 and PR22 carry the missing bound |
| B | ER §7: a reconnecting executor passes comparison 1 on a restored approval before entry | Addressed as an operator step: ER §7.2 requires every controller stopped until entry commits, which Bronzeward neither enforces nor detects (choice ER §10.18); an enforced fence would be a new mechanism |
| C | ER §7.2 served drafts, ingestion and publication before entry; PA §12.2 serves only liveness and entry | Fixed: ER §7.2 and choice §10.18 now match PA |
| D | ER §6.3: the adoption record compared no machine identity | Fixed: the adopt plan binds the identity and requirement 4.4 compares it |
| E | ER §4: a first contradicting completion read fails the operation before the verification deadline | Gap, owner decision: whether the deadline is a convergence window, and for which postconditions (§10) |
| F | ER §4: an operation with no attempt could complete after a takeover | Fixed: an `apply-config` operation needs a recorded attempt to complete or fail |
| G | ER §7.3: any successful restoration read makes a scope `ready` | Gap, owner decision: what the read must match, with PR19 (§10) |
| H | ER §7.3: a restored operation ends `cancelled` though an attempt may have been sent after the snapshot | Fixed as wording: `cancelled` after a restore refers to the restored journal and is never proof that nothing was sent |
| I | ER §3.3: `unresolved` and `cancelled` read as simultaneous | Fixed: a sequence |
| J | PA: a service identity's token reissued after a restore before its revocation is re-recorded | Fixed: `deniedSubjects` names service identities from the revocation on, and the tool issues no token to a listed identity |
| K | PA T11: leaving recovery mode and inventory both read installation state `FOR SHARE` | Fixed: leaving takes it `FOR UPDATE` |
| L | PA §8.2, T8, T9: jobs failed without a terminal event | Fixed: every move of a job to a terminal state appends its terminal event |
| M | PA §7.2: a refused ingestion retried under a new key after rotation ingests again | Fixed as disclosure: the cost is stated in PA §7.1 and §14 |
| N | PA §10.5: a refused draft entry that persists a claim wrote no act | Fixed: it writes the act |
| Threads | Epoch representation, adoption `Desired` binding, pre-commitment states, first-baseline adoption plan | Addressed by the execution-recovery text as landed |
| Threads | `Desired` selection on the machine timeline; drift reads after an assignment change | Refuted: ER §4.1 excludes `Desired` selection from the machine-scope facts; drift compares with `Applied`, which an assignment change does not move |

The fresh review of the repairs raised one further point: under recovery start no job worker runs
(PA §12.2), and ER §7.2 allowed the normal restart only once the first scope was released while
PA §12.2 allows it once entry has committed, so a publication clearing a `blocked` scope could not
run. ginsys/bronzeward#56 aligns ER §7.2 with PA: the restart may follow entry.

## 8. E3 disposition

**Disposition.** The Upgrade/LifecycleClient transition tests that
[E3 §6.3](20260925-talos-compatibility.md#63-criterion-3-unsupported-combinations-and-the-deferred-lifecycle-tests)
deferred are deferred to the machine-lifecycle phase. They are now a named acceptance item of
[design §18.3](../Talos_Configuration_and_Machine_Management_Design.md#183-phase-2---machine-lifecycle)
and part of the
[§18.5 proof](../Talos_Configuration_and_Machine_Management_Design.md#185-later-lifecycle-acceptance-proof):
on VMs or hardware, an upgrade through `MachineService.Upgrade` and through
`LifecycleClient.Upgrade`, the version gate that chooses between them, an upgrade across the v1.13
boundary, a later configuration contract applied after an upgrade, reboot-mode apply and the
upstream upgrade windows. The fixtures report names Talos in QEMU as the tool, rejected for Phase 0
because its provisioner needs root
([fixtures report §2](20260919-investigation-fixtures.md#2-alternatives-compared)).

**What the PoC claims.** E3's report is PoC compatibility evidence: render, validation and
`apply-config` compatibility at the node's running contract minor (C §10.2). It is not full E3; a
search of the repository outside the research reports for E3 and deferral wording found no claim
that E3 passed. The PoC does not upgrade Talos (§18.2 exclusions; AP §8), so the
deferral removes nothing the PoC needs.

**Design wording reconciled** in the same change: the §18.1 E3 row keeps its definition and adds the
disposition; §18.3 gains the upgrade-transition item; §18.5 states that neither the lifecycle
phase nor E3 can be complete before that item passes; §6.5's compatibility paragraph names the
phase;
`CONTRIBUTING.md` states the disposition instead of deferring it to this review. The §13.7 item 4
parenthetical calling ER "landed but not yet accepted" was removed as stale.

## 9. Owner-review choices

All 77 choices the contracts put to the owner: C §16 (26), ER §10 (23), PA §17 (28). The default
recommendation is **accept as specified**. FLAG marks a choice where the reviewer recommends the
owner decide explicitly; each is expanded below the table.

| ID | Subject | Recommendation |
|---|---|---|
| C16.1 | Ingestion and compiler as two identities | accept as specified |
| C16.2 | RFC 6901 addressing with `doc[n]` | accept as specified |
| C16.3 | Create-only `cas=0` generations at paths of their own | accept as specified |
| C16.4 | Baseline under its own key, decryptable by no runtime identity | accept as specified |
| C16.5 | Transient staging by default; separate staging key | accept as specified |
| C16.6 | Absolute expiry beside the lease | accept as specified |
| C16.7 | Owner-only lease extension; takeover only on request after lapse | accept as specified |
| C16.8 | Abandonment evaluated at read time, written by a sweep | accept as specified |
| C16.9 | Claims from an earlier epoch abandoned | accept as specified |
| C16.10 | HMAC under a provider-held key for value digests | accept as specified |
| C16.11 | Substring search kept beside value comparison | accept as specified |
| C16.12 | Every secret scoped to one cluster | accept as specified |
| C16.13 | A new name for every extracted value | accept as specified |
| C16.14 | Declarations name the exact version | accept as specified |
| C16.15 | Tag syntax `!bwref <name>` | accept as specified |
| C16.16 | Kinds string, integer, boolean, mapping; lists refused | accept as specified |
| C16.17 | Declarations beside the fragment; `encoding` enum | accept as specified |
| C16.18 | SR's re-serialization rules for embedded documents | accept as specified |
| C16.19 | Reserved text `!bwref` refused in any string | accept as specified |
| C16.20 | Import base stands in for §6.7's generation resources | accept as specified |
| C16.21 | An exact literal copy of a resolved value refuses publication | accept as specified |
| C16.22 | SP's stand-in format and token syntax | accept as specified |
| C16.23 | Withhold verbatim messages from boolean-input steps | accept as specified |
| C16.24 | Go machinery renderer in process, conditional on a parity re-run | FLAG F2 |
| C16.25 | Compile only the node's running contract minor | accept as specified |
| C16.26 | Unkeyed configuration digest of baselines and artifacts | FLAG F1 |
| ER10.1 | Ignore is not a PoC drift policy | accept as specified; the role question stays open beyond the PoC |
| ER10.2 | Configuration digest is SHA-256 over the normalized read-back | FLAG F1 |
| ER10.3 | Operation created by the dispatch commitment | accept as specified |
| ER10.4 | Self-approval marked for any contained revision and a token's human | accept as specified |
| ER10.5 | Rollout limit fixed at one | accept as specified |
| ER10.6 | Identity revocation refuses every later attempt, retries included | FLAG F6 |
| ER10.7 | Takeover at start and on request only; no timer | accept as specified |
| ER10.8 | Machinery Talos client in process, conditional on DS re-runs | FLAG F2 |
| ER10.9 | Route bound in the plan; both E4 routes allowed | FLAG F3 |
| ER10.10 | Retry after full accounting only with attempts, approval and gate | accept as specified |
| ER10.11 | Accounting by `recovery-admin` after a 30 s settle floor | FLAG F8 |
| ER10.12 | Drift detection does not freeze by itself | accept as specified |
| ER10.13 | Adoption as a plan with operation `adopt` | accept as specified |
| ER10.14 | `author` performs the ingestion feeding an adoption (interim) | FLAG F4 |
| ER10.15 | Adoption record compares with the baseline's digest | accept as specified |
| ER10.16 | A revert's approval recorded as approving an unseen overwrite | accept as specified |
| ER10.17 | Recovery epoch a never-reissued random identifier | accept as specified |
| ER10.18 | Recovery start keeps gates closed; entry by the API act | accept as specified |
| ER10.19 | Every scope pre-restore unaccounted at entry | accept as specified |
| ER10.20 | Scope `blocked` on apply dependencies, not regeneration | accept as specified |
| ER10.21 | Recovery mode enforced per scope | accept as specified |
| ER10.22 | Leaving recovery mode needs every scope released | accept as specified |
| ER10.23 | An ordinary plan cannot overwrite undetected drift | accept as specified |
| PA17.1 | Random application identifiers; `mch` machine ID | accept as specified |
| PA17.2 | ETags carry a random token beside the revision | accept as specified |
| PA17.3 | Immutability enforced by database triggers | accept as specified |
| PA17.4 | Publication checks every input head and import base | accept as specified |
| PA17.5 | Import base is the Applied release's; no separate head | accept as specified |
| PA17.6 | Publication selects the release as Desired | FLAG F7 |
| PA17.7 | No provider I/O inside a transaction; fixed lock order | accept as specified |
| PA17.8 | Generation paths carry claim id and random value id; orphans reported | accept as specified; C §2.3 step 6 now states the component (§6 item 6) |
| PA17.9 | Idempotency key on every mutating request, kept, epoch-bound | accept as specified |
| PA17.10 | Release natural key is the draft revision | accept as specified |
| PA17.11 | Operation created by the dispatch commitment | accept as specified |
| PA17.12 | Job states; lapsed `publish` reclaimed; `ingest` never re-run | accept as specified |
| PA17.13 | Recovery mode per scope | accept as specified |
| PA17.14 | Problem documents, `v1` policy, pagination, SSE | accept as specified |
| PA17.15 | Adoption approval is a plan with `operation: adopt` | accept as specified |
| PA17.16 | OIDC tokens verified per request, 15-minute maximum | accept as specified |
| PA17.17 | One automation token per identity, 30/90-day expiry | accept as specified |
| PA17.18 | Every automation identity names a responsible human | accept as specified |
| PA17.19 | Identity revocation permanent; deny list survives restore | FLAG F6 |
| PA17.20 | Several qualifying roles: first in the route's order | accept as specified |
| PA17.21 | Both unsettled self-approval cases marked | accept as specified |
| PA17.22 | Ingestion and inventory need a human `author` (interim) | FLAG F4 |
| PA17.23 | Losing a role does not invalidate approvals (interim) | FLAG F5 |
| PA17.24 | Migrations by explicit command, service stopped; no downgrade | accept as specified |
| PA17.25 | Epoch a random 128-bit identity, no counter | accept as specified |
| PA17.26 | Recovery-start flag; entry once per start by the API act | accept as specified |
| PA17.27 | Automation tokens from an earlier epoch refused | accept as specified |
| PA17.28 | Drafts and releases per cluster; library fragments shared | accept as specified |

**Flags.**

- **F1: unkeyed configuration digest (C16.26, ER10.2).** Both contracts say it is not the most
  conservative option: a whole-configuration SHA-256 is a guessing oracle when the only unknown
  parts are low-entropy secrets, and PA §16 and C §15 carry it as unassessed. The alternative, a
  keyed digest under the C §4.1 HMAC key, costs a provider call per observation and rests on an
  HMAC primitive no investigation exercised. Recommendation: accept for the PoC, with the residual
  recorded in the acceptance evidence; revisit before any production use.
- **F2: machinery in process (C16.24, ER10.8).** The renderer and the Talos client both leave what
  was measured (subprocess `talosctl`), to keep plaintext in one process; the deciding reason is
  inferred. Both are conditional on re-runs (SR/SP matrices through the compiler, DS rows through
  the client), which AP S2 and S4 carry as preconditions. Recommendation: accept, and treat a failed
  re-run as selecting the subprocess fallback, not as a contract change.
- **F3: both routes allowed (ER10.9).** Chosen for route flexibility. Every late landing DS observed
  came through the control plane's proxy, and PR22 has no bound. The alternative, the worker's own
  endpoint only, has one row of evidence. Recommendation: the owner decides; the reviewer leans to
  the worker route only until AP S6.2 records a proxy distribution, since that removes the observed
  late-landing path from the PoC's dispatch.
- **F4: ingestion role (ER10.14, PA17.22).** Interim, pending the question design §13.7 leaves open.
  A human `author` ingests; automation cannot. Recommendation: decide the question on
  ginsys/bronzeward#14 before ginsys/bronzeward#22 starts; the interim is safe to accept if not.
- **F5: role loss (PA17.23).** Interim, pending §13.7's open question. Approvals survive loss of the
  `approver` role; only an identity revocation (item 4) reaches them. The alternative needs a
  directory lookup or session state. Recommendation: accept the interim for the PoC, stated in
  ginsys/bronzeward#30's limits, and record the owner's answer on ginsys/bronzeward#14.
- **F6: identity revocation scope (PA17.19, ER10.6).** Both go beyond §13.7 item 4 without
  contradicting it: revocation is permanent, refuses authentication, and refuses retries of an
  already-used approval. A mistaken revocation locks the person out until given a new identity.
  Recommendation: accept, and have the owner confirm the lockout consequence explicitly.
- **F7: Desired at publication (PA17.6).** Design §12.6's "select the latest applicable approved
  release" points to selection at approval. Selecting at publication is what AP S2 tests ("the
  release is `Desired` for the worker, with no plan"). Recommendation: the owner confirms; if
  accepted, design §12.6 needs matching wording so the contract does not silently override it.
- **F8: settle floor (ER10.11).** Accounting a lost response is a `recovery-admin` decision after at
  least 30 s, a choice not a bound (FR §9 item 3). Recommendation: accept for the PoC; AP S6.2's
  measurement is the input for any change.

**Pairs checked.** Each pair states the same rule: C16.26 and ER10.2 (digest); C16.24 and ER10.8
(in-process machinery, separate conditions); C16.9, ER §7.2 item 5 and PA §12.2 (earlier-epoch
claims abandoned); ER10.3 and PA17.11 (operation at commitment); ER10.4 and PA17.18, PA17.21
(self-approval; §6 item 5); ER10.13 and PA17.15 (adopt plan); ER10.14 and PA17.22 (ingestion role;
§6 item 4); ER10.17 and PA17.25 (random epoch); ER10.18 and PA17.26 (recovery start and entry; §6
item 2 on what is served before entry); ER10.21, ER10.22 and PA17.13 (per-scope recovery mode and
exit); ER10.6 and PA17.19 (identity revocation). C16.7 and ER10.7 apply the same no-timer takeover
rule to different objects (claims, operations). ER10.23 has no PA counterpart and needs none.

## 10. Gaps

Every gap the contracts and FR carry, with its owner. "Beyond the PoC" means it stays open after
ginsys/bronzeward#31 and is stated in ginsys/bronzeward#30's limits.

**FR §9** ([FR §9](20260925-feasibility-evidence-review.md#9-gaps)):

| Gap | Where it goes |
|---|---|
| 1. Drift freeze, sanitized adoption, approved revert; E6 | ginsys/bronzeward#27 (AP S5); ginsys/bronzeward#31 |
| 2. Upgrade/LifecycleClient transition | beyond the PoC: design §18.3 item (§8) |
| 3. Late-landing bound | ginsys/bronzeward#28 (AP S6.2); if no bound results, beyond the PoC, accounting staying an operator decision |
| 4. Recovery mode after restoration | ginsys/bronzeward#29 (AP S7) |
| 5. Authenticated API, scoped authorization, timeline | ginsys/bronzeward#21 (S0), ginsys/bronzeward#25 (S3), ginsys/bronzeward#26 and ginsys/bronzeward#31 (S8) |
| 6. SQLite dispatch; MySQL/MariaDB | beyond the PoC: profile is PostgreSQL |
| Narrower: machinery parity; migrations; plan expiry, observation age, attempt bound | ginsys/bronzeward#23; ginsys/bronzeward#21; ginsys/bronzeward#25, ginsys/bronzeward#26, ginsys/bronzeward#28 |
| Narrower: exact-copy scanning; age combinations g1/g2/g1 and g2/g1/g2; Transit `soft_deleted`; crash-to-sealed | beyond the PoC |

**ER §9.3** ([ER §9.3](../../spec/execution-recovery.md#93-gaps-carried-and-what-would-close-them)):
items 1 to 4 are AP S5, S6.2, S7 and S8 (ginsys/bronzeward#27, #28, #29, #26). Plan expiry and
cancellation, the assignment-change refusal and identity revocation go to ginsys/bronzeward#25;
observation age, `InvalidArgument` for a validation error and the machinery client to
ginsys/bronzeward#26; the attempt bound and takeover at start to ginsys/bronzeward#28; observation
ordering under a concurrent accounting transaction, which ER §9.2 requires, to
ginsys/bronzeward#28; comparison 6 to ginsys/bronzeward#27 and
ginsys/bronzeward#29. Beyond the PoC: other response classes, the two age combinations, a new OpenBao cluster, token expiry
across a restore. The two specification gaps (no decommission act for a machine never observed
again; completion consulting only recorded observations) stay open beyond the PoC, with their
residuals stated in ER §9.3, unless the owner asks for a rule first.

**PA §16** ([PA §16](../../spec/persistence-api.md#16-verification-and-evidence-limits)):
authentication, same-key concurrency and migrations to ginsys/bronzeward#21; restore epoch and
recovery-entry quiescence to ginsys/bronzeward#29; orphan listing to ginsys/bronzeward#22; unkeyed
digests to F1; the cross-contract bullet was stale and is removed (§6 item 1). Beyond the PoC: undetected restore (design
§14.6 accepts it), online migration and downgrade, serializable isolation, ciphertext determinism.

**C §15** ([C §15](../../spec/compilation.md#15-verification-and-evidence-limits)): pipeline
interruption, the HMAC primitive, claim contention, staging-key, baseline-key and HMAC policies,
and addressing to ginsys/bronzeward#22; machinery composition parity, the fidelity and paired-diff
controls, messages and the plaintext boundary to ginsys/bronzeward#23; whole-configuration digests
to F1. Beyond the PoC: detector precision, digest key rotation, clock skew, generated-configuration
ingestion, kinds not run, tracer limits, embedded formatting, exact-copy leak detection and
metal-mode validation.

**Found by this review:**

- **PR19, pre-send identity, with G.** Needs ER §2 to bind machine identity as a precondition and
  ER §3.2 comparison 3 to check it, and the same rule to define what a restoration read must match
  before a scope is `ready` (§7 G). An owner decision on the identity evidence (hardware identity,
  assignment evidence, membership) precedes the rule; ginsys/bronzeward#26 and ginsys/bronzeward#29
  carry the checks once it exists.
- **E, the verification deadline.** Whether a first contradicting completion read may fail an
  operation before its deadline, or the deadline is a convergence window, and for which
  postconditions (§7 E). An owner decision, then an ER §4 state-machine change;
  ginsys/bronzeward#26 carries the check.
- **PR13, the dependency monitor.** No contract section owns the classification procedure, alert
  timing or least-privilege metadata access; design §7.6, §7.8 and §15.3 set the policy.
  ginsys/bronzeward#24 takes design §7.8 and RC §6.4 as its contract.

## 11. Refinement mapping

Each block can be lifted verbatim into its issue. Paths are repository paths; "AP" is
`docs/spec/acceptance-plan.md`.

**ginsys/bronzeward#21, runnable foundation.**
- Contracts: `persistence-api.md` §2, §5, §9, §10, §11; §14 rows for authentication and migration.
- Acceptance: AP S0 steps 1–5 and its negative controls; AP §2 fixture additions (instances A and B,
  disposable OIDC issuer, automation token tool, deployment settings).
- Checks: concurrent migrate runs with the advisory-lock control; startup refusal on schema or
  checksum mismatch; every token defect `401`; each design §13.7 scenario row through the API; no
  dispatch, token or role route (`404`); same-key idempotency with the key-lock control.
- Carries: authentication is untested by any investigation (PA §16).

**ginsys/bronzeward#22, adoption.**
- Contracts: `compilation.md` §2, §3, §4, §13; `persistence-api.md` §3.2, §5.1, §6.4;
  `execution-recovery.md` §6.3 (existing-cluster handover).
- Acceptance: AP S1.
- Checks: pre/post digest and resource version unchanged; scan of every surface with its positive
  control on success, each refusal and each pipeline step 0–8 interruption under both staging modes;
  takeover only after lease lapse; orphans listed, none deleted; automation refused on ingestion;
  executor cannot decrypt the baseline.
- Carries: pipeline interruption unmeasured; HMAC primitive unexercised; JSON Pointer addressing
  untested (C §15). Owner choice F4 decides who ingests.

**ginsys/bronzeward#23, edit and publish.**
- Contracts: `compilation.md` §5–§11; `persistence-api.md` §4, §6, §7.
- Acceptance: AP S2 steps 1–4 and negative controls.
- Checks: SR and SP matrices through the compiler's own path (the C §10.1 condition, before relying
  on the machinery); ETag `412`; `409 stale-input` with the `FOR SHARE` control; literal copy and
  reserved text refused; fidelity check fires on an injected change; immutable rows refuse writes;
  idempotent replay and `422` on reuse; publication creates no plan or operation.
- Carries: machinery parity inferred; paired-diff control loose; messages withheld (C §15). Owner
  choice F7 (Desired at publication).

**ginsys/bronzeward#24, retention checks.**
- Contracts: design §7.6, §7.8 and §15.3 (the decided PoC policy); `persistence-api.md` §3
  (DependencyStatus), §6.3; `compilation.md` §6 step 3, §9. No contract section owns the monitor.
- Acceptance: AP S2 step 5 and its soft-delete, destroy and partition controls; AP S7 variants.
- Checks: `retained`, `blocked`, `lost`, `unknown` per RC's evidence; a 404 stays `unknown`; a
  `retained` dependency turning unreadable alerts at once; persistent unknown re-alerts after 15
  minutes; the monitor reads metadata only; publication pinning a non-`retained` version refused;
  a `retained` verdict never admits dispatch (use-time check stays ER §3.1).
- Carries: Transit `soft_deleted` and the same-second window (RC §7).

**ginsys/bronzeward#25, plan and approve.**
- Contracts: `execution-recovery.md` §2, §3.2 comparisons 1–2, §3.3, §8.1, §8.5;
  `persistence-api.md` §8.1, §10.3–§10.5.
- Acceptance: AP S3.
- Checks: plan binds every ER §2 value; one approval, automation and non-approvers `403`; both
  self-approval marks; assignment change refuses commitment; expiry; approval revocation before and
  after commitment with the DS row 003 lock control; identity revocation before and after
  commitment; publication alone dispatches nothing.
- Carries: identity revocation unmeasured. Owner choices F5, F6.

**ginsys/bronzeward#26, safe apply.**
- Contracts: `execution-recovery.md` §1, §3.1–§3.5, §4, §5.1, §8; `persistence-api.md` §8.3.
- Acceptance: AP S4; AP S8 for the timeline, with ginsys/bronzeward#31.
- Checks: DS rows 001–005, 012–017 and 022 re-run through the implementation's client (ER §3.5
  condition); digest equals the artifact's after completion; evidence, commitment and attempt
  precede the request; Desired, Applied, Observed served separately; sealed OpenBao, stale
  observation, second plan (scope-index control), frozen scope and `InvalidArgument` each refused.
- Gap to close first: the pre-send identity check (§10, PR19). Owner choices F2, F3.

**ginsys/bronzeward#27, drift.**
- Contracts: `execution-recovery.md` §6, §8.6; `compilation.md` §2.3 (drift adoption);
  `persistence-api.md` §9.2.
- Acceptance: AP S5 (ER §9.3 closing run 1).
- Checks: detection opens a record without freezing; freeze by any listed role, unfreeze by
  `approver` only; sanitized adoption with the E1 scan on success, rejection and interruption;
  adoption record refused on each stale input; revert refused on re-drift and closed `returned` on
  a manual return; an ordinary plan refused while a record is open; the baseline-reproduction
  measurement recorded.

**ginsys/bronzeward#28, interrupted execution.**
- Contracts: `execution-recovery.md` §3.4, §4, §5, §8.2–§8.4; `persistence-api.md` §5.1.
- Acceptance: AP S6.1 and S6.2 (ER §9.3 closing run 2).
- Checks: each ER §5.3 interruption point ends as its table says; stale owner refused in the
  attempt `UPDATE` with the DS row 008 control; lost response held `unresolved` until a
  `recovery-admin` decision after the settle floor; newer plan refused (DS row 011 control); retry
  only under ER §5's conditions; observation ordering under a concurrent accounting transaction
  (ER §4.1, §9.2); the late-landing distribution recorded whichever way it falls.
- Carries: no bound unless S6.2 produces one. Owner choice F8.

**ginsys/bronzeward#29, restoration.**
- Contracts: `execution-recovery.md` §7, §8.7; `persistence-api.md` §12, §13.6;
  `compilation.md` §3.5.
- Acceptance: AP S7 (ER §9.3 closing run 3), including its Transit-key and older-OpenBao variants.
- Checks: recovery start answers only liveness and entry before entry; entry mints an epoch, marks
  every scope, takes over non-terminal operations, abandons claims; the missed stale instance
  refused by the epoch, with the epoch-term control; restored operation never retried; per-scope
  refusals; exit only when every scope is released; the no-entry restart residual shown.
- Depends on the §6 item 2 repair (what the recovery start serves).

**ginsys/bronzeward#30, package and document.**
- Acceptance: AP S8 step 1's walkthrough; AP §8 and §9 as the documented limits.
- Checks: a reviewer runs the walkthrough from a fresh checkout and disposable environment,
  including interruption and restoration; limits list the profile and versions, the gaps §10 marks
  beyond the PoC, the interim owner choices, and that E3 is not complete.

**ginsys/bronzeward#31, E6 acceptance.**
- Contract: the acceptance plan, `docs/spec/acceptance-plan.md`, as accepted under
  ginsys/bronzeward#20.
- Checks: one integrated run of S0–S8 from a fresh `bin/up`; negatives and matrices separately;
  common evidence per AP §2; the coverage table of AP §7 answered row by row; no production claim.

## 12. Acceptance criteria

| Criterion of ginsys/bronzeward#20 | Where | State |
|---|---|---|
| 1. Trace properties to evidence and contracts; resolve inconsistencies and required gaps | §4–§6, §10 | Traced. §6 items 1, 2, 4, 6 and 7 and §7 C, D, F and H–N are repaired by ginsys/bronzeward#56; the rest are disclosed, need no change or are gaps assigned in §10 |
| 2. E3 disposition and design wording; no full-E3 claim | §8; design §18.1, §18.3, §18.5; `CONTRIBUTING.md` | Recorded here and in the design |
| 3. Refine every P issue before removing its label | §11 | Text ready; the issues are not yet edited and keep their labels |
| 4. Approve an integrated acceptance plan | [AP](../../spec/acceptance-plan.md) | Plan landed; approval is the owner's |
| 5. Owner or designated reviewer acceptance, without S04 | not this report | Recorded by the owner on ginsys/bronzeward#20 |

## 13. Limits

- This review reads the contracts and reports; it runs nothing. A status is as strong as the
  evidence FR graded and no stronger.
- Cross-contract checking covered the points where two contracts name the same fact, rule or
  choice; clauses stated in one contract only were read for the properties in §4, not audited
  line by line.
- The property list is this review's reading of §18.1, §18.2 and §13.7; a property those sections
  imply but do not state is not covered.
- Line numbers are at aa58272 and move with the parallel repair.

## 14. Hand-off

- ginsys/bronzeward#20: the owner's acceptance (criterion 5), the eight flagged choices (§9), and
  approval of the acceptance plan (criterion 4).
- ginsys/bronzeward#56, the spec repair: §6 items 1, 2, 4, 6 and 7, and §7 C, D, F and H–N.
- ginsys/bronzeward#14: the ingestion-role and role-loss questions (F4, F5).
- ginsys/bronzeward#21 to ginsys/bronzeward#31: the §11 text, then removal of
  `status/needs-refinement` once each issue carries it.
- Later lifecycle work: the upgrade-transition item of design §18.3 (§8).
