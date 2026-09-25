# Feasibility evidence review

Evidence review for [ginsys/bronzeward#12](https://github.com/ginsys/bronzeward/issues/12). It reads
the ten landed milestone-01 reports against design [§18.1 and §18.2](../Talos_Configuration_and_Machine_Management_Design.md#18-phased-implementation-and-proof-of-concept),
links each required property to evidence or to an explicit gap, re-runs the disputed results that
decide the matrix's boundaries, and lists recommendations. It decides nothing: the decisions and
specifications it feeds are tracked in their own issues, and the owner's assessment (criterion 4) is
recorded separately, after this report.

Read at `main` 41c995c (2026-09-25), after every contributing report had landed.

## 1. Question

Does the evidence support a viable configuration-control PoC specification? Put narrowly: for each
property the design requires of that PoC, is there reproducible evidence that the tested mechanism
has it, evidence that it does not, or no evidence at all?

## 2. What binds every conclusion here

Carried forward verbatim from the issue:

- **Upgrade/LifecycleClient execution testing is deferred and the full design E3 experiment has not
  passed. PoC compatibility is not full-E3 completion.**
- Aggregation adds coverage, never confidence. This review asserts nothing a contributing report
  does not already evidence.
- The fixture limits constrain every conclusion, and combining reports relaxes none of them:
  exact-copy-only leak scanning, one Talos version, no reboot, upgrade, reset, installer or disk
  behaviour, one control plane and one worker, and the unknown-under-partition classification being
  each experiment client's own judgement rather than the fixture's
  ([fixtures report §7](20260919-investigation-fixtures.md#7-limits)).

A "pass" below always means "the tested mechanism passed under those limits". It is never a
production claim.

## 3. Sources

| Key | Issue | Report | Evidence |
|---|---|---|---|
| Fx | ginsys/bronzeward#1 | [investigation fixtures](20260919-investigation-fixtures.md) | [`fixtures/`](../../../fixtures) |
| E1 | ginsys/bronzeward#2 | [secret ingress](20260922-secret-ingress-extraction-before-persistence.md) | [evidence](../../../experiments/e1-secret-ingress/evidence) |
| SR | ginsys/bronzeward#3 | [structural references](20260924-structural-reference-composition.md) | [evidence](../../../experiments/e2-structural-references/evidence) |
| SP | ginsys/bronzeward#4 | [sensitivity and provenance](20260925-sensitivity-provenance.md) | [evidence](../../../experiments/e2-sensitivity-provenance/evidence) |
| E3 | ginsys/bronzeward#5 | [Talos compatibility](20260925-talos-compatibility.md) | [evidence](../../../experiments/e3-talos-compatibility/evidence) |
| DB | ginsys/bronzeward#6 | [database semantics](20260924-database-semantics.md) | [evidence](../../../experiments/e4-database-semantics/evidence) |
| DS | ginsys/bronzeward#7 | [dispatch safety](20260925-dispatch-safety.md) | [evidence](../../../experiments/e4-dispatch-safety/evidence) |
| PC | ginsys/bronzeward#8 | [provider capabilities](20260924-provider-capability-comparison.md) | [evidence](../../../experiments/e5-provider-capabilities/evidence) |
| RC | ginsys/bronzeward#9 | [retention classification](20260924-retention-metadata-classification.md) | [evidence](../../../experiments/e5-retention-classification/evidence) |
| KL | ginsys/bronzeward#10 | [key loss and restoration](20260924-key-loss-restoration.md) | [evidence](../../../experiments/e5-key-loss-restoration/evidence) |
| OM | ginsys/bronzeward#11 | [Omni build-gate refresh](20260924-omni-build-gate-refresh.md) | documentation review, no fixture |

Each report's own reproduction section gives the commands; the evidence directories hold the
collected rows, summaries and fixture bundles. Section numbers below (for example "DB §4.7") refer
to these reports.

### 3.1 Pins and comparability

The issue requires the `fixtures/versions.env` commit each report was captured against, so that a
cell recorded against a different one is not merged silently.

- `fixtures/versions.env` last changed at c74f953; its blob is `8171420` at `main`.
- Every fixture-based capture commit carries the same blob: E1 e637bf0, SR dc26305, SP 456df31,
  E3 2809915, DB ee8d7e2, DS 1f28961 and f2c7c86, PC aecdef4, RC 1c057f7, KL 5b29fed (checked with
  `git rev-parse <commit>:fixtures/versions.env` in a clone that holds them). None of the ten is an
  ancestor of `main`: each PR was rebased, and several rewritten, before it merged. Three can still
  be fetched through their pull-request refs: e637bf0 (`refs/pull/37/head`), aecdef4
  (`refs/pull/38/head`) and 1c057f7 (`refs/pull/39/head`). The other seven exist only in the
  author's clone, and DS §3 already records that f2c7c86 cannot be fetched. For those seven the pin
  rests on that local check and on `versions.env` being unchanged on `main` since c74f953, which
  predates every capture.
- **Every fixture-based cell is therefore directly comparable.** OM uses no fixture (Omni v1.12.2,
  read 2026-09-24).

Shared pins: Talos v1.13.6, Kubernetes 1.36.2, OpenBao 2.6.1, PostgreSQL 17 (17.11 observed), sops
v3.13.3, age v1.3.2. Report-specific pins: SR and SP yaml.v3 3.0.1 and `pkg/machinery` v1.13.6;
E3 renderers v1.12.12 to v1.15.0-alpha.0; DB SQLite 3.53.4 (modernc v1.59.0); DS PostgreSQL only.

## 4. Properties

One row per property the design requires of the PoC, drawn from the §18.1 table rows E1–E6, the
Omni refresh paragraph after it, and the numbered §18.2 acceptance items, as read at `main`.

| ID | Property | Design |
|---|---|---|
| SP01 | Import reads the live config without mutating it | §18.2 item 1 |
| SP02 | Extraction precedes every durable write, on import and drift adoption, across success, rejection, crash and restart | §18.1 E1; §18.2 item 1 |
| SP03 | Protected staging has a recovery owner | §18.1 E1 |
| SP04 | Schema-based secret detection, assessed without a completeness claim | §18.1 E1; §18.2 item 1 |
| SP05 | Exact encrypted baseline | §18.2 item 1 |
| SP06 | Fragment and reference composition reaches native merge parity without custom merge semantics | §18.1 E2; §18.2 item 2 |
| SP07 | Materialized validation; named validation stages | §18.1 E2; §18.2 item 2 |
| SP08 | Sensitivity provenance; redaction not solely by value matching | §18.1 E2; §18.2 item 3 |
| SP09 | Effective versus reproduction dependencies recorded | §18.1 E2; §18.2 item 3 |
| SP10 | Native Talos render, validation and RPC compatibility at the PoC boundary | §18.1 E3 |
| SP11 | Upgrade/LifecycleClient transition | §18.1 E3 |
| SP12 | Stale-revision writes rejected | §18.1 E4; §18.2 item 2 |
| SP13 | Publication atomic and idempotent | §18.1 E4; §18.2 item 3 |
| SP14 | Publication or checks alone cannot trigger dispatch | §18.1 E6; §18.2 item 3 |
| SP15 | Unique durable intent | §18.1 E4 |
| SP16 | Schema migrations | §18.1 E4 |
| SP17 | Revocation before commitment; plan bound to the exact artifact and assignment | §18.1 E4; §18.2 item 4 |
| SP18 | Ownership loss fenced | §18.1 E4 |
| SP19 | A-after-B stale overwrite prevented | §18.1 E4 |
| SP20 | Interrupted RPCs and reconnects end safe or explicitly unresolved | §18.1 E4; §18.2 item 7 |
| SP21 | Direct Talos dispatch and verification; desired, applied and observed kept separate | §18.2 item 5 |
| SP22 | Drift detection, freeze, sanitized adoption, approved revert | §18.1 E6; §18.2 item 6 |
| SP23 | Recovery after external restoration through explicit recovery mode; conflicting work stopped | §18.2 item 7 |
| SP24 | Provider capabilities: custody, unlock, rotation, migration | §18.1 E5; §18.2 closing paragraph |
| SP25 | Metadata-only classification; unknown is never pass or lost; retained grants nothing | §18.1 E5 |
| SP26 | Pruning, soft deletion, destruction, reversible-decryption floors | §18.1 E5 |
| SP27 | Key loss and differing-age restores; applying ciphertext versus regeneration | §18.1 E5 |
| SP28 | Additional SQL backends and SQLite parity | §18.1 E4 |
| SP29 | Omni build-gate refresh | §18.1, paragraph after the table |
| SP30 | Minimal authenticated API, scoped authorization, usable operation timeline | §18.2 item 4 and closing paragraph |
| SP31 | E6 existing-cluster vertical slice | §18.1 E6 |
| SP32 | Immutable encrypted per-machine artifacts | §18.2 item 3 |

Not a property of this PoC: reset/reachability and the remote proxy/tunnel spikes. §18.1 assigns
them to later phases and §18.2 excludes them.

## 5. Matrix

Codes, mapped to the issue's pass/fail/unknown:

- **P**: pass. The report evidences the property for the tested mechanism.
- **p**: partial. Pass for the evidenced part, unknown for the remainder, which §6 names.
- **N**: fail. A valid negative finding: the tested design lacks the property.
- **G**: unknown. An explicit gap; the report scoped the property and did not test it, or no
  investigation exists.
- **–**: the report does not bear on the property.

| ID | Fx | E1 | SR | SP | E3 | DB | DS | PC | RC | KL | OM | Overall |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| SP01 | – | P | – | – | – | – | – | – | – | – | – | P |
| SP02 | – | p | – | – | – | – | – | – | – | – | – | p |
| SP03 | – | p | – | – | – | – | – | – | – | – | – | p |
| SP04 | – | p | – | p | – | – | – | – | – | – | – | p |
| SP05 | – | P | – | – | – | – | – | – | – | – | – | P |
| SP06 | – | – | P | – | p | – | – | – | – | – | – | P |
| SP07 | – | – | P | – | p | – | – | – | – | – | – | p |
| SP08 | – | p | – | P | – | – | – | – | – | – | – | p |
| SP09 | – | – | p | P | – | – | – | – | – | – | – | p |
| SP10 | – | – | – | – | p | – | – | – | – | – | – | p |
| SP11 | G | – | – | – | G | – | – | – | – | – | – | G |
| SP12 | – | – | – | – | – | P | – | – | – | – | – | P |
| SP13 | – | – | – | – | – | P | – | – | – | – | – | P |
| SP14 | – | – | – | – | – | – | – | p | p | – | – | p |
| SP15 | – | – | – | – | – | P | – | – | – | – | – | P |
| SP16 | – | – | – | – | – | p | – | – | – | – | – | p |
| SP17 | – | – | – | – | – | – | p | – | – | – | – | p |
| SP18 | – | – | – | – | – | P | p | – | – | – | – | p |
| SP19 | – | – | – | – | – | – | P | – | – | – | – | P |
| SP20 | – | – | – | – | – | P | p | – | – | – | – | p |
| SP21 | – | – | – | – | p | – | p | – | – | – | – | p |
| SP22 | – | – | – | – | – | – | – | – | – | – | – | G |
| SP23 | – | – | – | – | – | N | G | – | – | p | – | N |
| SP24 | – | – | – | – | – | – | – | P | – | – | – | p |
| SP25 | – | – | – | – | – | – | – | – | P | – | – | p |
| SP26 | – | – | – | – | – | – | – | P | p | – | – | p |
| SP27 | p | – | – | – | – | – | – | – | – | p | – | p |
| SP28 | – | – | – | – | – | p | G | – | – | – | – | p |
| SP29 | – | – | – | – | – | – | – | – | – | – | P | P |
| SP30 | – | – | – | – | – | – | – | G | G | G | – | G |
| SP31 | – | – | – | – | – | – | – | – | – | – | – | G |
| SP32 | – | – | – | – | – | – | – | p | – | – | – | p |

**Overall, 32 properties:** P 8 (SP01, SP05, SP06, SP12, SP13, SP15, SP19, SP29), p 19, N 1 (SP23),
G 4 (SP11, SP22, SP30, SP31).

**Cells, 47 bearing on a property:** P 16, p 23, N 1, G 7.

An overall grade can differ from a single cell. SP24 is partial although the PC cell passes,
because OpenBao passes and the local candidates do not, and no profile is selected yet
(ginsys/bronzeward#13). SP06 passes although the E3 cell is partial, because E3 only infers parity
beyond the SR boundary (§8, C4); the SR boundary itself is measured. SP25 is partial although the
RC cell passes, for the same reason SP14 is: the classification is measured, but that a retained
verdict grants no dispatch authority is shown by construction only (RC §6.3).

## 6. Evidence per property

Observed results and the reports' own inferences are kept apart. "Inferred" marks a claim its
report marks as an inference; this review never promotes one to observed.

**SP01.** E1 §4.2, against design §9.1: the first and last bundles of the matrix bracket every
run, and their `talos-config-sha256` values are identical (observed).

**SP02.** E1 §4.2: 14 honest, held, crashed and recovered bundles each carry only the 2 controls
(observed). E1 §4.4: the forbidden redact-after design leaves plaintext in the WAL with a clean dump
(the negative control fires). E1 §6: the ordering is enforced by a call-site test, not by the
compiler. *Partial because* import and adoption share one input and one function (E1 §4.2, §7), so
adoption is not independent coverage.

**SP03.** E1 §6: transient staging has no recovery owner; encrypted staging has one, but it depends
entirely on the provider (a netsplit fails recovery). E1 §4.2: the crashed draft transaction's claim
is stranded, because no takeover policy exists.

**SP04.** E1 §4.7: recall 9/9 on identified fields, 9/10 once a secret sits in
`machine.files[].content`; precision 9/10 (`cluster.id` spurious). SP §4.1: schema-only redaction
still leaks referenced values in 15 of 28 cells. Detection is assessed, and no completeness is
claimed, as the design asks. See C1 on what "schema" means in each report.

**SP05.** E1 §4.2: baseline verification, 5 runs, each decrypting to 25,922 bytes (observed).

**SP06.** SR §4: 210 rows, 0 differing; early resolution reaches native parity for all three
syntaxes. SR §5, §9: late resolution is ruled out. E3 §6.2: parity through the machinery
configpatcher and on other renderers is **inferred, not measured** (C4). Limits (SR §8): one
version, strategic merge only, control-plane bases only.

**SP07.** SR §6.3: 124 parity cells validate as native does (observed). SR §5: under late resolution the tag syntax is
dropped silently and validation passes the wrong value; tag and marker are rejected on typed fields
(negative sub-results). E3 §4.2: v1.14 output is refused by pre-v1.14 validators, `--strict`
refuses nothing, and the Kubernetes window is not enforced. *Partial because* named validation
stages are a specification item (ginsys/bronzeward#17), not a measured property.

**SP08.** SP §4.1: `path+schema+value` leaks 0 referenced values and 0 base secrets in 28 cells per
base; value-only leaks 8, schema-only 15, resolution-path 5 (and 26 base secrets), none 27 (26).
SP §4: 56 cells, 1356 expectations, 2 unexpected observations, both the same expectation error
(SP §4.3). SP §6.3 names the residual risks (literal copies in other forms, unmarked base fields,
unidentified embedded text, stale provenance). E1 §5.17: redaction covered only exported fields
until fixed. *Overall partial because* the passing representation depends on marking, which E1 §8
calls load-bearing; SP §5 shows why: the schema covers only Talos-typed secret fields.

**SP09.** SR §6.2: boolean effective dependencies could not be identified under early resolution.
SP §6.1–§6.3: a flip pass measures them (C2). *SR partial, SP pass.* Overall partial, because the
flip pass is one prototype technique on one version.

**SP10.** E3 §4.1: talosctl and machinery generate identically; the renderer clamps silently when
the target contract is newer than itself (negative sub-result). E3 §4.3: rpc-client 12/12 and
rpc-contract 42/42 agree; the v1.13.6 node refuses v1.14 output; v1.10 and v1.11 "can't be applied
in immediate mode" (that the cause is the mode and not decodability is **inferred**). E3 §4.4:
upstream policy windows are wider than documented support. E3 §6.4: design experiment E3 did not
pass.

**SP11.** E3 §6.3: "Upgrade/LifecycleClient transition execution. Not tested." Fx §2: QEMU was
rejected for Phase 0 (it needs root) and is named as the tool for the deferred tests. **Gap,
deferred by decision; see §2.**

**SP12.** DB §4.1 (S1): compare-and-swap rejection on both backends.

**SP13.** DB §4.2 (S2): all-or-nothing on both backends, including every interruption tried;
PostgreSQL needs `FOR SHARE` (row 011 is the control). On PostgreSQL only, a server kill and a
netsplit write nothing, a pause stalls and then commits, and a commit-unknown (row 061) committed
and was resolved by an idempotent retry. For rows 059–061 those values are read from the
transcripts, not asserted by the rows (DB §4.2).

**SP14.** RC §6.3: retained grants carry no authority; that dispatch needs more is shown by
construction only. PC §4: the compiler/executor split holds through OpenBao policy (create-only for
the publisher), but an administrator can still add a version (row 087). *Partial because* no
dispatch path exists to try publication against, which is E6 work.

**SP15.** DB §4.3 (S3): a partial unique index holds intent unique on both backends.

**SP16.** DB §4.6 (S6): transactional DDL holds on both backends, and a failed or killed
migration leaves nothing of itself; the unlocked control fails concurrent runners on both. *Partial
because* the runner is the prototype's own; the fixtures describe no migration surface.

**SP17.** DS §4.1 rows 001–006: the boundary is the COMMIT inside `Store.Commit`; row 005 is the
stated residual (a send after the attempt is recorded); row 006 is the naive control. DS §7: plan
expiry, observation age and the attempt bound were not exercised.

**SP18.** DB §4.4 (S4), with row 018 as the PostgreSQL control. DS §4.2: takeover fence (row 007),
no-fence control (row 008), second executor (row 009). DS §7: the database fence is not a Talos
fence.

**SP19.** DS §4.3: the machine-scope index (row 010); the no-scope control (row 011) shows A
overwriting B.

**SP20.** DB §4.2 row 061: commit-unknown resolved by idempotent retry. DS §4.4 rows 012–017: in
capture 1 row 012 landed about 11 s after the partition healed; executor exit is not accounting;
row 013 was classified `failed` on exit-only accounting (negative sub-result). DS §6.3: what proves
an abandoned request can no longer land is **not established**; the 30 s settle is a choice.

**SP21.** DS §4–§6.3: dispatch direct to Talos for one operation, mode and assignment. DS §7:
accounting is a harness action, and the observation basis is unsound under concurrency. E3 §7: the
label patch is applied as a full re-encode (the worker config read shrank from 428 lines to 55),
and what changed is not recorded.

**SP22.** No investigation. E1's adoption flow covers only extraction ordering (SP02), not freeze,
adoption policy or revert. **Gap.**

**SP23.** DB §4.7 (S7): restoring the database rewinds ids and fence generations, and a pre-restore
token passes again (observed negative). DB §9 (**inferred**): the epoch is rewound by a restore, and
a provider object could collide with a reissued release id. DS §7: no restoration, epoch or drift
freeze was tested. KL §6: no recovery-mode entry or quiescence was modelled. **Fail for the tested
design** (fences alone do not survive a restore), and **unknown** for the explicit recovery mode
the design requires, which nothing exercised.

**SP24.** PC §4 and §6.1 (9 elements, 194 cells, 0 mismatches) and PC §8: OpenBao meets every evidenced part;
the local age store is convention-only and fails the design §7.1 condition (negative); SOPS is a
primitive that loses concurrent writes (row 149, negative) and is import/export only. PC §6.2–§6.3:
in both local layouts the keys live in the same tree as the ciphertext; migration passes plaintext through one process; pruned
versions and Transit ciphertext cannot move.

**SP25.** RC §4.1: OpenBao yields retained, blocked, lost and unknown. RC §6.2: nothing uncertain
becomes lost. RC §6.4: a deleted key or its metadata returns 404, the same as a name that never
existed, so it is unknown, not lost. RC §4.2: the local candidates give retained or unknown only.
*Partial because* RC §6.3 shows that a retained verdict grants no dispatch authority by
construction only (see SP14).

**SP26.** PC §4: reversible floor and rewrap. RC §7: Transit `soft_deleted` and the same-second
window were not exercised. RC §8: OpenBao is sufficient except for whole-key or whole-path deletion.

**SP27.** KL §3.1: cases A–L (there is no case F), 112 rows, 0 mismatches. KL §3.2 case D: a key recreated under the
lost name yields `vault:v1:`, indistinguishable from the lost key's prefix. KL §5.1: 6 of 8 age
combinations; g1/g2/g1 and g2/g1/g2 stay open. KL §5.2: applying stored ciphertext is not
regeneration, and ciphertext is not executability. KL §5.3: the recovery prerequisite check stops
and names what is missing, but is not read-only at the provider (Transit upsert). KL §6: no restore
onto a new cluster. Fx §4
check 8: a provider snapshot restores a destroyed version and a deleted key (fixture self-test).

**SP28.** DB §4.1–§4.4: S1–S4 on both backends. DB §4.5 (S5): the naive claim is unsafe on
PostgreSQL; on SQLite it is unmeasured, because row 048 had one active claimer (the re-run in §7
had six, with no double claim, still under serialized writers). DB §4.8: SQLite
blocks writers. DB §6.3: eight dialect differences. MySQL/MariaDB: no evidence. DS §7: dispatch ran
on PostgreSQL only.

**SP29.** OM §3.1–§3.3: all three hard requirements are still unmet (BUSL licence; no approval gate;
SideroLink only, with the local API disabled), with absences bounded to the sources read; the
commercial-plan row is unresolved. OM §6: retain the build decision. Documentation and source review
only.

**SP30.** No investigation. In PC, RC and KL the administrator's operations ran as root (PC §7,
RC §7, KL §6), while the other provider identities ran under least-privilege policies (PC §8); no
report tested application authentication, scoped authorization or an operation timeline. **Gap.**

**SP31.** No investigation. **Gap.**

**SP32.** PC §6.2: a `cas=0` create cannot replace a generation, and the publisher's policy allows
nothing else; but the `gen/` paths are not `cas_required`, and the administrator added a version to
one (row 087). A generation is immutable because of who holds which policy, not because of its
path. *Partial because* immutability against the administrator is not provided.

## 7. Reproduction of the disputed results

The issue asks for disputed and high-risk results to be reproduced. Twelve were identified (R1–R12).
Three experiment re-runs cover the ones that set the single negative (SP23) and the gap boundaries
(SP20, SP28, SP24). They ran one at a time on the fixture, from `main` 41c995c, into fresh output
directories, and were compared with the committed evidence.

All three ran to completion with every step exiting 0 and no harness mismatch: database 63 rows,
dispatch 23, provider 194. The collected files the verdicts rest on are in
[`experiments/f12-reproduction/`](../../../experiments/f12-reproduction/README.md), with the
commands. A harness row can match while a recorded (`seen:`) value differs, so each target was
compared by value.

| Target | Committed evidence | Re-run | Verdict |
|---|---|---|---|
| R2, DB row 020: PostgreSQL naive claim | 309 jobs claimed twice; 401 completions, 1 job completed twice | 308 claimed twice; 400 completions, 0 completed twice | **Double claims reproduced; the double completion did not recur.** DB §4.5's "once in 400 jobs" rests on one capture |
| R2, DB row 048: SQLite naive claim | one worker made all 400 claims; no claimers competed | 6 workers claimed; none claimed a job twice | **Differs.** Competition now happened once, still under serialized (`IMMEDIATE`) writers. One sample, not a safety result |
| R3, DB row 061: commit-unknown | release committed after the kill; the retry returns the existing release | identical | **Reproduced** |
| R4, DB rows 027 and 055: restore | the next release reuses id 2; generations 2 and 3 are issued again; the pre-restore token passes | identical on both backends | **Reproduced.** The SP23 negative stands |
| R1, DS row 012: late landing | primary capture: no late landing, retry; capture 1: landed about 11 s after the heal | landed about 11 s after the heal; completed with no retry | **Differs from the primary capture, matches capture 1** |
| R1, DS row 013: accounting on exit | both captures: nothing landed, classified `failed` | landed about 11 s after the heal, just before Y's first readable observation; classified `completed` | **Differs.** A late landing occurred inside row 013's window; it came before the observation, so the classification was correct this time |
| R5, DS row 003: `FOR SHARE` wait | the revoker waited; revocation 3 ms after the commit | identical | **Reproduced**, with DS §4.1's caveat that the wait is inferred from the revoker's record |
| DS rows 014–017 | as DS §4.4 | same outcomes | **Reproduced** |
| R11, PC row 149: SOPS concurrent writes | 9 of 10 updates lost, both writers exit 0 | 8 of 10 lost, both writers exit 0 | **Reproduced; the count differs** |

What changes:

- **Late landings through the control-plane proxy are common, not rare.** DS §4.4 reports one in
  four control-plane partitions over two captures. With this run it is three in six (both
  partitions here landed). This does not move SP20's grade, which was already partial because no
  bound on late landing is established (DS §6.3), but it makes that gap more urgent for
  ginsys/bronzeward#19. In row 013 nothing landed in either committed capture, and here the landing
  preceded the observation, so no misclassification has been observed; but a landing can now fall
  inside the window between exit-only accounting and the observation, which is the hazard DS §4.4
  describes.
- **DB §4.5's naive-claim figures are per-capture.** The PostgreSQL double claim is stable (309,
  308); the double completion is not (1, 0). On SQLite, the naive claim has now been observed once
  with competing claimers and no double claim, under serialized writers; that stays short of the
  safety result DB §3.2 and §6.3 decline to claim.
- **No grade in §5 changes.** The results that fix the single negative (R4) and the commit-unknown
  resolution (R3) reproduced exactly.

Not re-run, with the reason:

- R6 (E1 WAL residue counts), R7 (E3 immediate-mode refusal), R8 (E3 re-encoding patch), R9 (KL
  recreated-key prefix), R10 (RC unanswered destroy) and R12 (the SP fidelity and E3 diff-filter
  checkers). None of them sets a grade in §5 or a gap boundary in §9, each report already states
  the limit, and each needs its own fixture run; they remain as their reports record them. For
  R12: the SP fidelity check as captured could pass silently only when a trace or flip fragment
  was missing, and SP §8 records that none was; the SP analyser has since been changed to record
  a missing one as a fidelity failure, without re-running the capture. E3's diff filter fails open
  on an unrecognised diff format, and E3 §7 records that no refused row's output held a scan
  pattern; E3 recommends the implementation fail closed.

## 8. Cross-report findings

- **C1: two meanings of "schema".** E1 §4.7 says Talos ships no field-level sensitivity metadata a
  detector could use. It examined the running API only: `sensitivity: sensitive` is per resource
  (35 of 167 resource definitions) and talosctl exports no schema (E1 §4.7). SP §2 and §4.4 use the
  `pkg/machinery` v1.13.6 `RedactSecrets` field list, a library surface E1 did not examine. Both
  are right about the surface each read. The qualification for ginsys/bronzeward#17: a field-level
  list exists in the library, and it covers only Talos-typed secret fields (SP §5), so marking
  stays load-bearing (E1 §8).
- **C2: refinement.** SR §6.2 found boolean effective dependencies unidentifiable under early
  resolution; SP §6.3 measures them with a flip pass. SP supersedes SR on that point.
- **C3: dependency.** SP rests on SR's early-resolution premise (SP §8). A change to the SR choice
  reopens SP.
- **C4: inference carried as reach.** E3 §6.2 extends E2 parity to the machinery configpatcher and
  other renderers by inference only.
- **C5: stale text.** E3 §6.2 calls ginsys/bronzeward#42 "not yet on `main`"; it merged before
  ginsys/bronzeward#45 did. The statement was true when written and changes no result.

## 9. Gaps

Required by the PoC and not evidenced:

1. **Drift freeze, sanitized adoption and approved revert, and the E6 vertical slice** (SP22,
   SP31). No investigation exists.
2. **Upgrade/LifecycleClient transition** (SP11). Deferred by decision (§2).
3. **A bound on when an abandoned request can no longer land** (SP20, §18.2 item 7). The executor's
   exit is not accounting, and the 30 s settle is a choice, not a bound (DS §6.3). Late landings
   occurred in three of six control-plane partitions (§7). DS §8 assigns this to
   ginsys/bronzeward#19, and it is not part of the lifecycle deferral: it was observed on the
   PoC's own `apply-config` path.
4. **Recovery mode after external restoration** (SP23). A restore rewinds fence generations
   (DB §4.7); recovery-mode entry, executor quiescence and a restore epoch are unmodelled (DS §7,
   KL §6).
5. **Authenticated API, scoped authorization and the operation timeline** (SP30). Untested; the E5
   reports ran their administrator operations as root, and no application identity was tested.
6. **Dispatch on SQLite** (SP28, DS §7) and any MySQL/MariaDB evidence (DB §6.3).

Narrower, and open:

- E2 parity through the machinery and on renderers other than v1.13.6 is inferred (E3 §6.2).
- Migrations beyond a prototype runner (DB §4.6); plan expiry, observation age and the attempt
  bound (DS §7).
- Leak scanning is exact-copy only (Fx §7, E1 §7); the OpenBao volume is unobservable and Transit
  plaintext crosses loopback without TLS (E1 §7).
- The age combinations g1/g2/g1 and g2/g1/g2 (KL §5.1); Transit `soft_deleted` and the same-second
  window (RC §7); the crash-to-sealed window (PC §7).

## 10. Recommendations

Candidates tied to evidence, for the issues that own each decision or contract. None is a
decision.

**Decisions.**

- **Profile selection (ginsys/bronzeward#13).** PostgreSQL with OpenBao is the only pairing with
  evidence on every exercised axis: DB §8 names PostgreSQL the design's server option; DS ran on
  PostgreSQL only; PC §8 and RC §8 find OpenBao sufficient; the local age store does not meet §7.1's
  required-properties condition, because every property it showed is writer convention (PC §8,
  RC §4.2); SOPS is import/export only (PC §8, row 149). Choosing SQLite would
  need dispatch evidence on it first.
- **Identity and approval (ginsys/bronzeward#14).** The revocation boundary is the COMMIT inside
  `Store.Commit`, with a stated residual (DS §4.1 row 005). There is no evidence on identity or
  approval cardinality (SP30). The publisher/administrator split relies on OpenBao policy, and an
  administrator can still add a version (PC §4 row 087).
- **Retention and recovery (ginsys/bronzeward#15).** RC §6.4 proposes an alert policy in which a
  404 stays unknown. Keys sit in the same tree as the ciphertext in both local layouts (PC §6.3). Pairing backups by age is inferred
  (KL §7). A recreated key cannot be told apart by ciphertext prefix (KL §3.2 case D).
- **Licence (ginsys/bronzeward#16).** No report evidences bronzeward's own licence choice. OM §3.1
  (Omni's BUSL) bears only on the build-versus-adopt gate.

**Specifications.**

- **Compiler (ginsys/bronzeward#17).** Early resolution with a source superset (SR §9). Marking is
  load-bearing (E1 §8; SP §5 and C1 for why). Redact by path, schema and value (SP §4.1). Pin the renderer to
  the node's minor version, refuse a newer target contract and an out-of-window Kubernetes version,
  and record the contract (E3 §8). Reference configuration by contract, since v1.14 changes paths
  (E3 §6.2).
- **Persistence (ginsys/bronzeward#18).** Compare-and-swap (DB §4.1), `FOR SHARE` for PostgreSQL
  publication (DB §4.2 row 011), a partial unique index for intent (DB §4.3), the eight dialect
  differences (DB §6.3), encrypted staging where a change must be resumable (E1 §8), and a restore
  epoch because ids and fences rewind (DB §4.7; the epoch design is inferred, DB §9).
- **Execution (ginsys/bronzeward#19).** The commit boundary and fence (DS §4.1–§4.2), the machine
  scope index (DS §4.3), accounting that is not exit-only (DS §4.4 row 013), a settle bound with a
  stated basis (DS §6.3), `InvalidArgument` before mutation (DS §4.6), post-restore recovery that
  does not trust fence generations (DB §4.7), ciphertext is not executability (KL §5.2), and a
  record of what a re-encoding patch changed (E3 §7).

Two §9 gaps are carried in part. The execution candidates carry item 3 into ginsys/bronzeward#19.
The restore epoch (#18) and post-restore recovery that does not trust fence generations (#19)
carry item 4's fencing half; its recovery-mode entry and executor quiescence remain uncovered. No
candidate covers the other §9 gaps. Each uncovered gap needs its own tracked work, or an explicit
owner deferral recorded where the PoC acceptance lives.

## 11. Acceptance criteria

| Criterion | Where |
|---|---|
| 1. Each required property linked to reproducible evidence or an explicit gap; negative findings kept | §4–§6; the SP23 negative stands as a finding (§6, §7) |
| 2. Viable recommendations; decisions and remediation tracked separately; nothing declared feasible without evidence | §10, pointing to ginsys/bronzeward#13–#19; §9 lists what is not evidenced |
| 3. Upgrade/LifecycleClient deferral retained; PoC compatibility distinguished from full E3 | §2, SP10 and SP11 in §6 |
| 4. Owner or designated reviewer assessment, with links to the ensuing work | **Not part of this report.** Recorded by the owner on ginsys/bronzeward#12 |

## 12. Limits

- This review reads the reports; apart from §7 it re-runs nothing. A cell is as strong as its
  report's evidence and no stronger.
- The property list is this review's reading of §18.1 and §18.2; a property the design implies but
  does not state is not covered.
- Grades are per property at the level the reports tested. "P" never means "production-ready", and
  no row generalises past the fixture limits in §2.

## 13. Hand-off

- ginsys/bronzeward#12: the owner's assessment (criterion 4).
- ginsys/bronzeward#13–#16: the decision briefs draw on §10.
- ginsys/bronzeward#17–#19: the specification candidates in §10, with C1 for #17.
- The gaps in §9 need tracking or an explicit owner deferral before the PoC specification review
  (ginsys/bronzeward#20).
