# PoC acceptance: integrated evidence and assessment

| | |
|---|---|
| **Date** | 2026-10-19 |
| **Work item** | [E6: demonstrate PoC acceptance](https://github.com/ginsys/bronzeward/issues/31) |
| **Design reference** | [§18.1](../Talos_Configuration_and_Machine_Management_Design.md#181-phase-0---evidence-before-implementation-contracts) E6, [§18.2](../Talos_Configuration_and_Machine_Management_Design.md#182-phase-1---configuration-control-on-an-existing-cluster); the [acceptance plan](../../spec/acceptance-plan.md) (AP) |
| **Artifacts** | The nine historical run records of §4, two repair regressions (§4.1) and their `bin/evidence` bundles: kept by the owner, not published (§3.4) |
| **Decision enabled** | Whether the owner accepts the configuration-control PoC on the fixture profile, with the gaps of §8 carried or closed. It makes no production claim. |

## 1. Question

Does the implementation of
[ginsys/bronzeward#21](https://github.com/ginsys/bronzeward/issues/21) to
[ginsys/bronzeward#30](https://github.com/ginsys/bronzeward/issues/30) pass the acceptance plan?
The plan defines the runs (AP §1 to §6) and the evidence each keeps (AP §2). This review records
those runs and answers [AP §7](../../spec/acceptance-plan.md#7-coverage) and
[§7.1](../../spec/acceptance-plan.md#71-required-verification) row by row, as AP §7.1 requires.

## 2. What binds every result here

- **Fixture:** the [AP §2](../../spec/acceptance-plan.md#2-fixture-and-common-evidence) fixture at
  the pins of `fixtures/versions.env`, blob `efd7c1431ebc0c3da9d258a13bb60c1c6112243f`. This is
  design §7.7's selected profile in its single-instance topology.
- **Commits:** Bronzeward and the fixture, one repository, every run's checkout with no changed path
  (§3.3). `run` and `controls`: `fcecfcaf2a6f724972688d1a21d81a06b7a72054`, this change's fixture
  fixes. The other seven runs: `ab327884527fee3b3ed32f0e2dfc8ffde5bf0ce3`, main after
  [ginsys/bronzeward#235](https://github.com/ginsys/bronzeward/pull/235). Between the two, no Go
  file differs. The fixture differs where a worker S5 step 4 pointed back at S3's handover cluster
  fragment selects no workers profile: S5 step 4 itself, the dispatch helper adding a role fragment
  to the worker's assignment instead of the workers profile there, and S7 step 3, which sends the
  worker's current selection with its cluster fragment added where ab32788 sent the workers profile
  with that fragment. In the S7 runs at ab32788 the worker's assignment is S2's,
  `{"profiles": ["workers"], "fragments": {}}`: no step between S2 and S7 puts it. So S7 step 3
  sends the same JSON value at both commits, compact at fcecfca, with no same-key retry. None of the
  seven runs at ab32788 runs S5, so the dispatch helper's path there is the unchanged one. Only S7 step 3's check text differs: it
  read "beside the workers profile" at ab32788. The text of S0's `SKIP` row for the earlier-epoch
  automation token,
  `fixtures/README.md` and the acceptance plan also differ.
- **Repair regressions:** the two additional chains at `7fe6f14a6853d8e7be65d46bda72884b4a4cc040`
  are recorded separately in §4.1; they do not change the historical revision bindings above.
- **Limits:** the plan's [§9](../../spec/acceptance-plan.md#9-limits) limits bind every result.
  A pass is a pass on the fixture, never a production claim.

## 3. Runs

Nine historical acceptance runs, each from its own fresh `bin/up`, all through `fixtures/scenarios/integrated`: the
`run` and `controls` presets at fcecfca, the `s6` preset and six step lists at ab32788 (§2). The
runner runs the steps in order, stops at the first that fails, and writes
`chain.log` (the checkout and each step's start, end and exit status), one `<step>.log` per step
and a copy of the fixture's evidence directories, under `<preset>-<UTC start>` for a preset and
`steps-<UTC start>` for a step list; a list names each step at most once. Each scenario step writes
`<scenario>.tsv` in its evidence directory: one row per check, with `PASS`, `FAIL` or `SKIP`, its
acceptance-plan reference and what it checked. A pass mode (a step ending in `p`) runs the pass
paths alone. It writes one `SKIP` row for each negative control or matrix it leaves out, ending
"the full mode runs this, from a fresh up"; the `controls` run is that full mode.

| Run | Steps | What it carries |
| --- | --- | --- |
| `run` | `up s0p s1p s3h s2p s3rp s4 s5p s7 s8 down` | the integrated run (AP §2, "Execution"): the pass paths of S0 to S5, S7's nominal run and S8, S3's handover between S1 and S2 |
| `controls` | `up s0 s1 s3h s2 s3r s4c s4x s4 s5 s5roles down` | the same chain in the full modes: the negative controls, matrices and Go rows `run` leaves out, S4's controls (`s4c`) and commitment-level controls (`s4x`, from restores of S3's snapshots) before S4's run, and S5 step 7 (`s5roles`) |
| `s6` | `up s1 s3h s2 s3r s4 s4ds s6 down` | S4's dispatch-safety rows (`s4ds`) and S6.1's interruption points; S8's step 2 reads its S6.1 timeline |
| S1 node ID | `up s1nid down` | S1's Talos access refusal for another node ID |
| S3 handover controls | `up s1 s3hc down` | the controls that refuse or hold an adoption before it commits |
| S7 closing | `up s1 s3h s2 s3r s4 s7c down` | S7's closing run (ER §9.3 item 3): C as the missed stale instance, its epoch refusals and the refusals that leave no state |
| S7 variants | `up s1 s3h s2 s3r s4 s7 s7n s7t s7o down` | S7's nominal run, then its no-entry, Transit and older-OpenBao variants on the same up |
| S0 walk | `up s0walk down` | S0 step 4's denied walk and the identity revocation it ends with, from its own `bin/up` as S0 specifies |
| S8 controls | `up s1 s8controls s8revocation down` | S8's negative controls and the revocation-alone case, on instance A as the disposable environment |

S6.2's measurement is already landed in the [late-landing report](20261016-late-landing.md).

### 3.1 The integrated run

| Step | Command | Exit | Checks (PASS / FAIL / SKIP) | Retained evidence |
| --- | --- | --- | --- | --- |
| `up` | `bin/up` | 0 | — | — |
| `s0p` | `scenarios/s0 pass` | 0 | 56 / 0 / 2 | `s0-pass-20261019T052351Z`, 1 bundle |
| `s1p` | `scenarios/s1 pass` | 0 | 36 / 0 / 2 | `s1-pass-20261019T053359Z`, 5 bundles |
| `s3h` | `scenarios/s3 handover` | 0 | 29 / 0 / 0 | `s3-handover-20261019T053514Z`, 3 bundles |
| `s2p` | `scenarios/s2 pass` | 0 | 53 / 0 / 4 | `s2-pass-20261019T053559Z`, 4 bundles |
| `s3rp` | `scenarios/s3 pass` | 0 | 74 / 0 / 1 | `s3-pass-20261019T053733Z`, 4 bundles |
| `s4` | `scenarios/s4 run` | 0 | 33 / 0 / 0 | `s4-run-20261019T053935Z`, 3 bundles |
| `s5p` | `scenarios/s5 pass` | 0 | 73 / 0 / 3 | `s5-pass-20261019T054036Z`, 8 bundles |
| `s7` | `scenarios/s7 run` | 0 | 125 / 0 / 0 | `s7-run-20261019T054312Z`, 4 bundles |
| `s8` | `scenarios/s8 run` | 0 | 55 / 0 / 2 | `s8-run-20261019T055053Z`, 3 bundles |
| `down` | `bin/down` | 0 | — | — |

### 3.2 Separate runs

Each from a fresh `bin/up` (AP §2). AP §7.1 items marked *check* are the CI tests of §6.1.

| Run | Step | Command | Exit | Checks (PASS / FAIL / SKIP) | Retained evidence |
| --- | --- | --- | --- | --- | --- |
| `controls` | `up` | `bin/up` | 0 | — | — |
| `controls` | `s0` | `scenarios/s0 run` | 0 | 62 / 0 / 1 | `s0-run-20261019T055929Z`, 1 bundle |
| `controls` | `s1` | `scenarios/s1 run` | 0 | 453 / 0 / 0 | `s1-run-20261019T061151Z`, 52 bundles |
| `controls` | `s3h` | `scenarios/s3 handover` | 0 | 29 / 0 / 0 | `s3-handover-20261019T063830Z`, 3 bundles |
| `controls` | `s2` | `scenarios/s2 run` | 0 | 334 / 0 / 0 | `s2-run-20261019T063944Z`, 23 bundles |
| `controls` | `s3r` | `scenarios/s3 run` | 0 | 95 / 0 / 0 | `s3-run-20261019T070417Z`, 5 bundles |
| `controls` | `s4c` | `scenarios/s4 controls` | 0 | 63 / 0 / 2 | `s4-controls-20261019T070820Z`, 3 bundles |
| `controls` | `s4x` | `scenarios/s4 restores` | 0 | 138 / 0 / 1 | `s4-restores-20261019T071208Z`, 8 bundles |
| `controls` | `s4` | `scenarios/s4 run` | 0 | 33 / 0 / 0 | `s4-run-20261019T071945Z`, 3 bundles |
| `controls` | `s5` | `scenarios/s5 run` | 0 | 170 / 0 / 0 | `s5-run-20261019T072136Z`, 17 bundles |
| `controls` | `s5roles` | `scenarios/s5 roles` | 0 | 17 / 0 / 0 | `s5-roles-20261019T073612Z`, 1 bundle |
| `controls` | `down` | `bin/down` | 0 | — | — |
| `s6` | `up` | `bin/up` | 0 | — | — |
| `s6` | `s1` | `scenarios/s1 run` | 0 | 453 / 0 / 0 | `s1-run-20261018T191441Z`, 52 bundles |
| `s6` | `s3h` | `scenarios/s3 handover` | 0 | 29 / 0 / 0 | `s3-handover-20261018T194024Z`, 3 bundles |
| `s6` | `s2` | `scenarios/s2 run` | 0 | 334 / 0 / 0 | `s2-run-20261018T194202Z`, 23 bundles |
| `s6` | `s3r` | `scenarios/s3 run` | 0 | 95 / 0 / 0 | `s3-run-20261018T200723Z`, 5 bundles |
| `s6` | `s4` | `scenarios/s4 run` | 0 | 33 / 0 / 0 | `s4-run-20261018T201127Z`, 3 bundles |
| `s6` | `s4ds` | `scenarios/s4 ds` | 0 | 298 / 0 / 0 | `s4-ds-20261018T201439Z`, 18 bundles |
| `s6` | `s6` | `scenarios/s6 run` | 0 | 199 / 0 / 0 | `s6-run-20261018T205620Z`, 13 bundles |
| `s6` | `down` | `bin/down` | 0 | — | — |
| S1 node ID | `up` | `bin/up` | 0 | — | — |
| S1 node ID | `s1nid` | `scenarios/s1 node-id` | 0 | 24 / 0 / 0 | `s1-node-id-20261019T005223Z`, 4 bundles |
| S1 node ID | `down` | `bin/down` | 0 | — | — |
| S3 handover controls | `up` | `bin/up` | 0 | — | — |
| S3 handover controls | `s1` | `scenarios/s1 run` | 0 | 453 / 0 / 0 | `s1-run-20261019T005521Z`, 52 bundles |
| S3 handover controls | `s3hc` | `scenarios/s3 handover-controls` | 0 | 43 / 0 / 0 | `s3-handover-controls-20261019T012047Z`, 4 bundles |
| S3 handover controls | `down` | `bin/down` | 0 | — | — |
| S7 closing | `up` | `bin/up` | 0 | — | — |
| S7 closing | `s1` | `scenarios/s1 run` | 0 | 458 / 0 / 0 | `s1-run-20261019T012517Z`, 52 bundles |
| S7 closing | `s3h` | `scenarios/s3 handover` | 0 | 29 / 0 / 0 | `s3-handover-20261019T015252Z`, 3 bundles |
| S7 closing | `s2` | `scenarios/s2 run` | 0 | 334 / 0 / 0 | `s2-run-20261019T015412Z`, 23 bundles |
| S7 closing | `s3r` | `scenarios/s3 run` | 0 | 95 / 0 / 0 | `s3-run-20261019T021640Z`, 5 bundles |
| S7 closing | `s4` | `scenarios/s4 run` | 0 | 33 / 0 / 0 | `s4-run-20261019T022109Z`, 3 bundles |
| S7 closing | `s7c` | `scenarios/s7 closing` | 0 | 142 / 0 / 3 | `s7-closing-20261019T022326Z`, 4 bundles |
| S7 closing | `down` | `bin/down` | 0 | — | — |
| S7 variants | `up` | `bin/up` | 0 | — | — |
| S7 variants | `s1` | `scenarios/s1 run` | 0 | 458 / 0 / 0 | `s1-run-20261019T023518Z`, 52 bundles |
| S7 variants | `s3h` | `scenarios/s3 handover` | 0 | 29 / 0 / 0 | `s3-handover-20261019T030306Z`, 3 bundles |
| S7 variants | `s2` | `scenarios/s2 run` | 0 | 334 / 0 / 0 | `s2-run-20261019T030426Z`, 23 bundles |
| S7 variants | `s3r` | `scenarios/s3 run` | 0 | 95 / 0 / 0 | `s3-run-20261019T032901Z`, 5 bundles |
| S7 variants | `s4` | `scenarios/s4 run` | 0 | 33 / 0 / 0 | `s4-run-20261019T033334Z`, 3 bundles |
| S7 variants | `s7` | `scenarios/s7 run` | 0 | 125 / 0 / 0 | `s7-run-20261019T033536Z`, 4 bundles |
| S7 variants | `s7n` | `scenarios/s7 no-entry` | 0 | 49 / 0 / 0 | `s7-no-entry-20261019T034544Z`, 3 bundles |
| S7 variants | `s7t` | `scenarios/s7 transit` | 0 | 46 / 0 / 0 | `s7-transit-20261019T035054Z`, 1 bundle |
| S7 variants | `s7o` | `scenarios/s7 old-bao` | 0 | 41 / 0 / 0 | `s7-old-bao-20261019T035825Z`, 1 bundle |
| S7 variants | `down` | `bin/down` | 0 | — | — |
| S0 walk | `up` | `bin/up` | 0 | — | — |
| S0 walk | `s0walk` | `scenarios/s0 walk` | 0 | 52 / 0 / 0 | `s0-walk-20261019T040259Z`, 1 bundle |
| S0 walk | `down` | `bin/down` | 0 | — | — |
| S8 controls | `up` | `bin/up` | 0 | — | — |
| S8 controls | `s1` | `scenarios/s1 run` | 0 | 453 / 0 / 0 | `s1-run-20261019T040539Z`, 52 bundles |
| S8 controls | `s8controls` | `scenarios/s8 controls` | 0 | 18 / 0 / 1 | `s8-controls-20261019T043234Z`, 2 bundles |
| S8 controls | `s8revocation` | `scenarios/s8 revocation` | 0 | 9 / 0 / 0 | `s8-revocation-20261019T043327Z`, 1 bundle |
| S8 controls | `down` | `bin/down` | 0 | — | — |

### 3.3 Versions

Per run, from its record:
- **Record:** the record directory, `<preset>-<UTC start>` or `steps-<UTC start>`.
- **Checkout:** the first line of `chain.log`, `checkout <OID>, <n> changed paths; steps: …`, with
  `<n>` 0; each bundle's `versions.txt` (`manifest commit`, `created at`) naming the same OID and
  its `fixtures-diff.txt` empty.
- **Pins:** the `fixtures/versions.env` blob, `git rev-parse <checkout>:fixtures/versions.env`.
- **Server image:** the image ID `bin/up` built and recorded in `.state/up-image`, and the `image`
  rows of `bw-fixture-bronzeward-a`, `-b`, `-c` and `-b-recovery` in `versions.txt`.
- **Versions:** from `versions.txt`: `talosctl` and `talos nodes`, `kubernetes`, `openbao` and
  `postgres` with their `image` rows, `docker` and `compose`.

A run whose bundles disagree on any of these is not one run. Versions the runs share are given once,
under the table.

| Run | Record | Checkout (changed paths) | Pins blob | Server image |
| --- | --- | --- | --- | --- |
| `run` | `run-20261019T052145Z` | `fcecfcaf2a6f` (0) | `efd7c1431ebc` | `6e31bb6ab4aa` |
| `controls` | `controls-20261019T055715Z` | `fcecfcaf2a6f` (0) | `efd7c1431ebc` | `d4b8c1c21333` |
| `s6` | `s6-20261018T191226Z` | `ab327884527f` (0) | `efd7c1431ebc` | `74661b51c9cc` |
| S1 node ID | `steps-20261019T005015Z` | `ab327884527f` (0) | `efd7c1431ebc` | `e5dd6e22b059` |
| S3 handover controls | `steps-20261019T005313Z` | `ab327884527f` (0) | `efd7c1431ebc` | `a3cb9895bc26` |
| S7 closing | `steps-20261019T012309Z` | `ab327884527f` (0) | `efd7c1431ebc` | `f2e6efca2bb5` |
| S7 variants | `steps-20261019T023237Z` | `ab327884527f` (0) | `efd7c1431ebc` | `17e4616f9b37` |
| S0 walk | `steps-20261019T040050Z` | `ab327884527f` (0) | `efd7c1431ebc` | `82cdf5079447` |
| S8 controls | `steps-20261019T040326Z` | `ab327884527f` (0) | `efd7c1431ebc` | `dc0f3ca92012` |

Every bundle of every run (569) agrees: `manifest commit` and `created at` its run's
checkout (fcecfca for `run` and `controls`, ab32788 for the others) and an empty
`fixtures-diff.txt`. The server image is built by each `bin/up`, so its ID is the run's own; within
a run all four server containers share it. Versions, the same for all runs:
- Talos v1.13.6, `talosctl` and both nodes, image
  `ghcr.io/siderolabs/talos:v1.13.6@sha256:f2e2b7e5812b2b59c1acfe6af7516231aeeef79fb1ffff6b57ad987f8dd47a6e`;
- Kubernetes 1.36.2 (requested);
- OpenBao v2.6.1 (`ba7ad8861d0578cd4da4f7b9e5a6756d30484f8f`), image
  `ghcr.io/openbao/openbao:2.6.1@sha256:5b2486ab0fb90bbc788cc345b0a08616dfb375873ee8be5df3a2fd4d378a67e0`;
- PostgreSQL 17.11, image
  `docker.io/library/postgres:17-alpine@sha256:f02121de6f74d30d8a94cd1d9584125e2178d7e6c377d8130112d4e52d867995`;
- Docker 26.1.5+dfsg1 (client and server), Compose 2.27.1; `sops` 3.13.3, `age` v1.3.2.

### 3.4 Common evidence

Per run (AP §2):
- the fixture and Bronzeward commits and the `versions.env` blob;
- commands and exit codes;
- request and response transcripts with tokens withheld;
- the migration record;
- machine timelines as served;
- a `bin/evidence` bundle with its positive control found.

Each run's record and bundles are kept by the owner, not published: the bundles hold the fixture's
synthetic secrets with their scans. The commands of §4 regenerate them.

## 4. Reproduction

From `fixtures/` in a clean checkout at each revision below (§2), with `mise run dev-db` and
`mise run dev-bao` up for the Go rows (S0's, S1's, S2's, S4's dispatch-safety rows and S8's), and
`<rec>` a record directory outside `fixtures/` (`bin/down` removes the fixture's state). `s6` runs
before `run`, whose S8 step reads the S6.1 timeline it kept. Finish each chain, including `down`,
before changing checkout. The commands reproduce the historical runs at their own revisions;
running them at a later revision produces new evidence.

At `ab327884527fee3b3ed32f0e2dfc8ffde5bf0ce3`:

```
scenarios/integrated <rec> s6
scenarios/integrated <rec> up s1nid down
scenarios/integrated <rec> up s1 s3hc down
scenarios/integrated <rec> up s1 s3h s2 s3r s4 s7c down
scenarios/integrated <rec> up s1 s3h s2 s3r s4 s7 s7n s7t s7o down
scenarios/integrated <rec> up s0walk down
scenarios/integrated <rec> up s1 s8controls s8revocation down
```

At `fcecfcaf2a6f` (the `run` and `controls` checkout in §3.3), using the retained S6.1 timeline
from the preceding revision:

```
S8_S6_KEPT=<rec>/s6-<UTC start>/evidence/s6-run-<UTC> scenarios/integrated <rec> run
scenarios/integrated <rec> controls
```

Records written:
- `s6`: `s6-20261018T191226Z`. It supersedes `s6-20261018T153139Z`, which passed but checked out
  3a1ffdb, a branch commit before its merge, not the commit of §2.
- `run`: `run-20261019T052145Z`, with `S8_S6_KEPT` the `s6` record's `s6-run-20261018T205620Z`.
  S5 step 4 pointed the worker's assignment back at S3's handover cluster fragment, since S2's
  label fragment was the workers profile's only one. From then on the worker selects no workers
  profile, so S7's held-operation release and S8's release added their role fragments to the
  worker's assignment, and S7 step 3 added its cluster fragment beside the worker's current
  selection. It supersedes three records:
  - `run-20261018T213240Z`, the `run` preset at ab32788, stopped at `s5p` (exit 1, S5 step 4's
    profile change refused with 400 for an empty fragment list);
  - `steps-20261018T220329Z`, which passed at ab32788 with `s4ds` before `s5p`, so that S5 relied on
    the dispatch-safety rows' fragments, where AP §2 runs those separately;
  - `run-20261019T045222Z`, the `run` preset at an earlier commit of this change, stopped at `s7`
    (S7 step 3's release left the worker's configuration digest unchanged).
- `controls`: `controls-20261019T055715Z`. With no S4 dispatch-safety rows before it, S5 step 4
  again pointed the worker's assignment back at S3's handover cluster fragment. It supersedes
  `steps-20261018T224934Z`, which passed at ab32788 with `s4ds` before `s5`.
- S1 node ID: `steps-20261019T005015Z`.
- S3 handover controls: `steps-20261019T005313Z`.
- S7 closing: `steps-20261019T012309Z`.
- S7 variants: `steps-20261019T023237Z`.
- S0 walk: `steps-20261019T040050Z`.
- S8 controls: `steps-20261019T040326Z`.

Every step of these nine records exited 0 and no check failed. Across the 569 bundles, each leak
scan found only its positive control, except where S8's negative control planted a copy of a
secret to be found (`S8 negative controls`, "the scan reports the copy"); no bundle names a source
it could not capture.

### 4.1 Repair regression: both worker selections

Local review found that the older-OpenBao variant would remove `s2-labels` from
the unused `workers` profile after S5 had reassigned the worker. That profile
contained only that fragment, so the edit would be refused with 400 for an empty
fragment list. This was a source finding, not another recorded failed chain. Commit
`7fe6f14a6853d8e7be65d46bda72884b4a4cc040` skips the edit when the worker does not
select `workers`; when it does, the existing fragment-removal behavior remains.
No public API, schema or Go code changed.

These two fresh chains ran serially at that clean commit, with no tracked edits
during execution. They supplement the nine historical records above. They do not
replace the AP §2 integrated run, its S8 timeline or the separate S6 evidence.

| Path | Record | UTC start to finish | Bundles | Server image (sha256) |
| --- | --- | --- | --- | --- |
| Reassigned worker | `steps-20261019T100727Z` | 2026-10-19T10:07:27Z to 2026-10-19T11:43:35Z | 112 | `bd18cd9f76b9c0fba3212dd5ce7d74c0d2f9b259cc869316ec98b841492dc8a6` |
| Selected workers profile | `steps-20261019T114919Z` | 2026-10-19T11:49:19Z to 2026-10-19T12:14:38Z | 28 | `37c24cb5baf555816f53b1808f31367a18d788c9415237d798c39940f99e4f94` |

| Path | Step | Exit | Checks (PASS / FAIL / SKIP) | Retained evidence |
| --- | --- | --- | --- | --- |
| Reassigned worker | `s1` | 0 | 458 / 0 / 0 | `s1-run-20261019T100935Z` |
| Reassigned worker | `s3h` | 0 | 29 / 0 / 0 | `s3-handover-20261019T103717Z` |
| Reassigned worker | `s2` | 0 | 334 / 0 / 0 | `s2-run-20261019T103831Z` |
| Reassigned worker | `s3r` | 0 | 95 / 0 / 0 | `s3-run-20261019T110350Z` |
| Reassigned worker | `s4` | 0 | 33 / 0 / 0 | `s4-run-20261019T110747Z` |
| Reassigned worker | `s5` | 0 | 170 / 0 / 0 | `s5-run-20261019T110935Z` |
| Reassigned worker | `s7` | 0 | 125 / 0 / 0 | `s7-run-20261019T112413Z` |
| Reassigned worker | `s7n` | 0 | 49 / 0 / 0 | `s7-no-entry-20261019T113249Z` |
| Reassigned worker | `s7t` | 0 | 46 / 0 / 0 | `s7-transit-20261019T113456Z` |
| Reassigned worker | `s7o` | 0 | 41 / 0 / 0 | `s7-old-bao-20261019T114126Z` |
| Selected workers profile | `s1p` | 0 | 36 / 0 / 2 | `s1-pass-20261019T115132Z` |
| Selected workers profile | `s3h` | 0 | 29 / 0 / 0 | `s3-handover-20261019T115245Z` |
| Selected workers profile | `s2p` | 0 | 53 / 0 / 4 | `s2-pass-20261019T115330Z` |
| Selected workers profile | `s3rp` | 0 | 74 / 0 / 1 | `s3-pass-20261019T115519Z` |
| Selected workers profile | `s4` | 0 | 33 / 0 / 0 | `s4-run-20261019T115713Z` |
| Selected workers profile | `s7` | 0 | 125 / 0 / 0 | `s7-run-20261019T115807Z` |
| Selected workers profile | `s7n` | 0 | 49 / 0 / 0 | `s7-no-entry-20261019T120540Z` |
| Selected workers profile | `s7t` | 0 | 46 / 0 / 0 | `s7-transit-20261019T120646Z` |
| Selected workers profile | `s7o` | 0 | 41 / 0 / 0 | `s7-old-bao-20261019T121255Z` |

Every `up` and `down` also exited 0. Teardown reported no fixture containers,
networks, volumes or state left. The profile-selected chain deliberately uses
S1/S2/S3 pass modes; their explicit skips remain in their TSVs. It exercises the
selected-profile branch, not another full interruption matrix. The first chain
runs the full modes and S5's reassignment before the same S7 variants.

Each bundle's `manifest commit` and `created at` equal the repair commit, each
`fixtures-diff.txt` is empty, and each leak scan finds only its intentional
positive control. No source is recorded as unavailable. All versions and pinned
images are those in §3.3, except the per-run server image IDs above; the pins blob
is unchanged. Bundles remain owner-kept and unpublished under §3.4.

From the repository root at the repair commit, with the same development services
as §4, `<rec>` outside `fixtures/` and an existing writable `<tmp>` outside the
fixture state:

```sh
export TMPDIR='<tmp>' GOTMPDIR='<tmp>'
mise exec -- fixtures/scenarios/integrated '<rec>' up s1 s3h s2 s3r s4 s5 s7 s7n s7t s7o down
mise exec -- fixtures/scenarios/integrated '<rec>' up s1p s3h s2p s3rp s4 s7 s7n s7t s7o down
```

The first run's older-OpenBao check explicitly records that the worker selects
no `workers` profile and skips the unused profile edit. The second run retains
the selected-profile removal check. Both publish and apply the imported artifact
before the database snapshot, then restore the older provider, record its missing
dependency alerts and refuse regeneration. Neither changes the acceptance limits
or supplies an owner's verdict.

## 5. Coverage (AP §7)

Runs and steps are those of §3; "`controls`: `s1`" is the `s1` step of the `controls` run.
Scenario passes describe their observed paths. The dated supplement in §6.3
adds the R5 refusal and R45 observation-access mapping and committed checks.
The 20 October independent timeline reading and delegated assessment supply
the current six criterion verdicts in §7, with their limits in §9.

| Design §18.2 | Scenario | Runs: steps | Result | Verdict |
| --- | --- | --- | --- | --- |
| Item 1: import without mutation, extraction before persistence, exact encrypted baseline | S1; S3's handover part | `run`: `s1p`, `s3h`; `controls`: `s1`, `s3h`; S3 handover controls: `s3hc` | passed: every step named exited 0, no check failed (§4) | met |
| Item 2: native fragments, references, revisions, provenance, validation stages | S2 | `run`: `s2p`; `controls`: `s2` | passed: every step named exited 0, no check failed (§4) | met |
| Item 3: immutable encrypted artifacts, exact dependencies, redacted review data, no apply | S2, S3 step 1 | `run`: `s2p`, `s3rp`; `controls`: `s2`, `s3r` | passed: every step named exited 0, no check failed (§4) | met |
| Item 4: authenticated API; plan and approval of one safe worker change | S0, S3 | `run`: `s0p`, `s3rp`; `controls`: `s0`, `s3r`; S0 walk: `s0walk` | passed: every step named exited 0, no check failed (§4) | evidenced by the runs and R5 supplement (§6.3); dated verdict in §7 |
| Item 5: direct dispatch, verification, separate desired/applied/observed | S4 | `run`: `s4`; `s6`: `s4ds` | passed: every step named exited 0, no check failed (§4) | evidenced by the runs and R45 supplement (§6.3); dated verdict in §7 |
| Item 6: external change detected; freeze, sanitized adoption, approved revert | S5 | `run`: `s5p`; `controls`: `s5` | passed: every step named exited 0, no check failed (§4) | met |
| Item 7: interrupted execution | S6 | `s6`: `s6` | passed: every step named exited 0, no check failed (§4) | met |
| Item 7: external restoration through explicit recovery mode | S7 | `run`: `s7` (the nominal run; its pass criterion "every pre-restore automation token until reissued", PA §17 choice 27: `S7 step 6`, the seed automation token 401 after the restore and after its reissue, which S0 step 3 leaves out with a `SKIP`); S7 closing: `s7c`; S7 variants: `s7`, `s7n`, `s7t`, `s7o` | passed: every step named exited 0, no check failed (§4) | met |
| Selected database/provider behaviour (§7.7) | S0 to S8 on the AP §2 fixture | all nine runs | passed: every step named exited 0, no check failed (§4) | met |
| Scoped authorization (§13.7) | S0 steps 4 and 5, and the scenario-table rows AP §7 names | S0 walk: `s0walk`; `run`: `s0p`, `s2p` (step 7), `s3h`, `s3rp`, `s4`, `s5p` (step 3), `s7`; `controls`: `s4c`, `s4x`, `s5roles` (step 7); `s6`: `s6` (step 7); S7 closing: `s7c` | passed: every step named exited 0, no check failed (§4) | met |
| Usable operation timeline | S8, with a second reviewer's record | `run`: `s8`; S8 controls: `s8controls`, `s8revocation`; the reviewer's record (`run`'s `s8` writes `SKIP` for it) | passed: every step named exited 0, no check failed (§4) | live part met; independent reading assessed on 20 October (§7, §9), preserving the original SKIP |
| §18.1 E6: the existing-cluster vertical slice | §3.1 and S6's runs | `run`; `s6` | passed: every step named exited 0, no check failed (§4) | checks supplemented in §6.3; independent reading and dated verdicts in §7 and §9 |

## 6. Required verification (AP §7.1)

Row R*n* is the *n*th row of [AP §7.1](../../spec/acceptance-plan.md#71-required-verification), in
its order. The item is shortened to its leading clause; the full item and its "Where" are in the
plan. An item with no retained result leaves its issue incomplete (AP §7.1).

| Row | Contract item (leading clause) | Issue (ginsys/bronzeward) | Checks (§6.1; supplement §6.3) | Left for the live run | Verdict |
| --- | --- | --- | --- | --- | --- |
| R1 | C §15: ingestion success, refusal and interruption at each pipeline step, every surface scanned | #22, #27, #23 | drift-adoption success and its start refusals | S1; S5 step 4 with drift adoption's interruption at each step and its scan; S2's negative controls | met live and in CI |
| R2 | C §15: the compiler process's surfaces scanned over successful, rejected and interrupted publications, killed at … | #23 | original six-point checks; #247's seven-point run can retain earlier observation entries but no release/Desired change before commit; generation-2 takeover | S2's revision-bound scans, including #247 full S2 at `aede267` | met live and in CI |
| R3 | C §15: claim lease extension by its owner only, takeover of `held` and `resumed` claims only after lapse, stale … | #22 | every named takeover test, lease extension, a crash inside the draft transaction, provider unreachable | S1's negative controls | met live and in CI |
| R4 | C §15: SR and SP matrices through the compiler's path, with import-base references and SP's oracle over Bronzeward's … | #23 | — | S2's precondition and negative controls | met live |
| R5 | C §15 and PA §16: every refusal of C §13, every walk-through of PA §13 and refusal of PA §14 | #21 to #29 | clause-level mapping in the supplement; separate #247 publication-minor evidence; PA14-13 synchronous failure and control checked | retained scenario refusals and scans, including early publication access refusal | evidenced by the clause mapping, landed repairs and fresh combined-tree checks (§6.3); #241 landed and accepted (§6.3) |
| R6 | ER §9.2: DS rows 001–023 through the implementation over the worker's endpoint, with controls 006, 008, 011 and row … | #26 | — | S4's precondition (DS rows over the worker's endpoint); the fallback's scan does not apply, the fallback is not built (ER §9.2) | met by live rows and Go interleaving checks (ER choice §10.32); the fallback is not built (ER §9.2) |
| R7 | ER §9.2: sending only after the attempt commits | #25, #26 | every named lock test with its control | S4 and DS rows 003 and 011 | met live and in CI |
| R8 | ER §9.2: expiry, observation age, a contradicting newer observation, the attempt bound, each refusing | #25, #26, #28 | the attempt bound refusing; the bound exhausted after a lost response | expiry, observation age and a contradicting newer observation (S3, S4) | met live and in CI |
| R9 | ER §9.2: plan cancellation before commitment and after it with no attempt | #25, #26 | — | S3's and S4's negative controls | met live |
| R10 | ER §9.2: a plan for a release that is not the machine's `Desired` refused at creation | #25, #26 | the publication racing the commitment in both orders, with the unlocked control; the attempt still admitted | the refusal at plan creation (S3) and the refused commitment (S4) | met live and in CI |
| R11 | ER §9.2: a bound health check failing before the verification deadline then passing | #26, #27, #188 | a contradicting digest: `failed` at once, the drift record in the same transaction | — | contradicting digest met in CI; health cases carried (§8) |
| R12 | ER §9.2: the fixture's nodes each reporting a distinct Talos node ID, no SMBIOS UUID and one Talos cluster ID through … | #26, #29 | the commitment's and retry's identity cases with their controls against a stub; the restoration half | the fixture's nodes read through the real client | met live and in CI |
| R13 | ER §9.2: an `apply-config` operation with no attempt not completed by a completion observation of its artifact applied … | #28 | the schema refusing completion with no attempt; a recovery observation not completing it; a completion observation of the artifact applied not completing it, with its control | — | met in CI |
| R14 | ER §9.2: assignment change refused while the scope is held; comparison 6 under freeze/recovery before release | #26, #29 | comparison 6 under a freeze and before release in recovery mode | S4's held-assignment refusal and S5's frozen-scope revert passed over | met live and in CI |
| R15 | ER §9.2: takeover at start | #28 | takeover at start, the §5 precedence, `unresolved` kept, a terminal operation refused | S6.1 | met live and in CI |
| R16 | ER §9.2: identity revocation before commitment, after it with no attempt, after an attempt with its retry refused | #25, #26, #28 | the retry refused after an attempt | before commitment and after it with no attempt (S4) | met live and in CI |
| R17 | ER §9.2: observation ordering, contradicting higher-basis reads, confirmed reads, the late-read residual | #28, #27 (adoption read) | ordering, contradicting higher-basis and confirmed reads, the late-read residual, the adoption read; a completion read in flight across a concurrent accounting, with the record-order control | — | PoC observation predicates met in CI; bound-health ordering carried to #188 (§8) |
| R18 | ER §9.2: plan and commitment refusals on undetected drift | #27 | a commitment refused on drifted evidence, opening the record, and comparison 6 refusing an ordinary plan while it is open; a drifted adoption's refusals; a revert's refusals, re-drift and a manual return closing `returned` | S5, and two of its negative controls: the revert on a frozen scope passed over, the unfreeze by a non-approver refused | met live and in CI |
| R19 | ER §9.2: recovery-mode entry after a restore older than a takeover, an approval, a revocation and an attempt | #29 | — | S7 | met live |
| R20 | ER §9.2: the complete existing-cluster E6 slice | #31 | — | the integrated run (§3.1) | live run and R5/R45 checks evidenced; independent reading and delegated assessment recorded in §7 and §9 |
| R21 | PA §16: `FOR SHARE` at publication (DB row 011) | #23 | — | S2's negative controls | met live |
| R22 | PA §16: ownership check inside the attempt's `UPDATE` (row 018) | #25, #26 | the conditional write and its read-then-write control | S4's negative controls | met live and in CI |
| R23 | PA §16: claim eligibility re-check (row 020) | #22 | the refusal after expiry in the lock wait, with the earlier-read control | — | met in CI |
| R24 | PA §16: the orphan report's identity, its policy read back and compared exactly, with the extra-stanza control | #22 | every clause, each with its control; both reference kinds read, with the one-kind-only control (the defect fixed by ginsys/bronzeward#233) | S1's negative controls | met live and in CI |
| R25 | PA §16: migration advisory lock (row 026) | #21, #23 | the startup refusal on each schema mismatch | the migration lock and immutability triggers (S0, S2) | met live and in CI |
| R26 | PA §16: role checks whose outcome needs the route's handler or state, against the design §13.7 scenarios | #23, #25, #26, #27, #28, #29 | — | S2 step 7, S3 to S5, S6.1 step 7, S7 | met live |
| R27 | PA §16: the epoch term and process-epoch checks | #29 | — | S7 | met live |
| R28 | PA §16: one idempotency key in flight twice, with the key-lock control | #21 | one key in flight twice, with the key-lock control | — | met in CI |
| R29 | DM §10.1 items 2 and 3: each provider object state classified through the metadata client under the fixture's policy, with exactly its alerts | #24 | the provider states against CI's development OpenBao | the same against the fixture's OpenBao (S2) | met live and in CI |
| R30 | DM §10.1 items 2 and 8's publication refusal | #24 | — | S2 step 6 and its negative controls | met live |
| R31 | DM §10.1 items 2 and 3 across a restore: a deleted Transit key and an OpenBao snapshot older than the database | #29 | — | S7's variants | met live |
| R32 | DM §10.1 items 1, 4 to 7, 9 to 12, 14 and 15: every classification rule, persistent unknown, metadata-only access, … | #24 | each listed item, with item 1's clock control and item 4's no-last-alert control | — | met in CI |
| R33 | DM §10.1 item 8's dispatch half: no dispatch admitted by a retained class | #26 | no dispatch admitted while the artifact key is retained | — | met in CI |
| R34 | DM §10.1 item 13: recovery start records nothing before its entry commits | #29 | the monitor and sweep start only after the entry, with the normal-start control | — | met in CI |
| R35 | PA §16: approval revocation racing commitment (DS row 003) | #25, #26 | approval revocation waits for commitment (`TestCommitmentRevocationsWait/approval`) | the same Go checks replayed by `s4ds` for DS row 003, using a stand-in node (ER choice §10.32) | met in CI and replayed in the fixture suite |
| R36 | PA §10.4: an approval surviving its approver's loss of the role, contrasted with its revocation (choice §17.23) | #25, #26 | — | S4's negative controls, contrasted with S3 | met live |
| R37 | PA §16: identity revocation racing commitment, with the lock control | #25 | the race; the T5c entries on exactly the machines touched; the T5c installation lock with its `FOR SHARE` control | — | met in CI |
| R38 | PA §16: a lapsed `publish` job claimed again | #23 | — | S2's negative controls | met live |
| R39 | PA §16: leaving recovery mode racing an inventory request, with the `FOR SHARE` control | #29 | the exit refused, with the `FOR SHARE` control | — | met in CI |
| R40 | PA §16: concurrent rotations of one service identity, and rotation racing its revocation, with the principal-lock … | #21 | both races, each with the principal-lock control | — | met in CI |
| R41 | PA §16: a reissue after a restore refused for a service identity `deniedSubjects` lists | #29 | the reissue refused after a simulated restore, with the no-list control | — | met in CI |
| R42 | PA §16: two inventory requests for one SMBIOS UUID under different keys, one refused, and the same for one Talos node … | #22 | each identity key's race with its control; both keys/neither and platform refusals; matching/mismatched release-mode controls | — | met in CI; platform checks rerun in §6.3 |
| R43 | PA §16: Talos access, ingestion | #22 | every named test; the access path's grants and refusals | `TestLiveRoleProbe` (skips in CI: no Talos node); S1's scan | met live and in CI |
| R44 | PA §16: Talos access, executor | #26, #172 | every named test, against a stand-in node | S4's scan of refused and interrupted dispatches | met live and in CI |
| R45 | PA §16: Talos access, observations | #26, #27, #29 | six purposes and 30 access failures; 12 latest/stale loop assertions, mutating-call and post-revocation controls; plan-less drift | retained S4, S5, S7 evidence; #247 publication observations | fresh six-purpose checks passed (§6.3); #241 landed and accepted (§6.3) |
| R46 | PA §16: Talos access, endpoint | #22, #25, #29 | the grammar, atomicity, recovery mode, a plan dialling its old endpoint, with the current-endpoint and dispatch-time-resolution controls; a later plan bound to the new endpoint | — | met in CI |
| R47 | PA §16: machine revisions allocated in commit order under concurrent writers, with the unlocked control | #26 | commit order under concurrent writers, with the unlocked control | — | met in CI |
| R48 | PA §16: an event stream ended by its token's `exp`, resumption refused after revocation | #26 | the stream ended at `exp` (in CI too) | S8 step 3 | met live and in CI |
| R49 | PA §16: no request body in the data directory, write-ahead log or backups | #22, #23 | — | S1's and S2's scans | met live |
| R50 | PA §16 and C §15: the operator review (C §3.6) | #125 | every clause but the killed mark runs; paused claims in the orphan report, live and past expiry | S1's review rows and scans, which alone show a mark's run killed at each point it re-enters | met live and in CI |

### 6.1 Tests per row

Paths and lines are at cde5e38, main after [ginsys/bronzeward#234](https://github.com/ginsys/bronzeward/pull/234). Each test ran in CI's `go-db` job (PostgreSQL 17, OpenBao 2.6.1 in development mode, no Talos node): on main at a636c2a, workflow run 39136967990, which succeeded, or, for the tests and subtests marked *at cde5e38*, in the `go-db` job of ginsys/bronzeward#234, which added them. At ab32788, main after [ginsys/bronzeward#235](https://github.com/ginsys/bronzeward/pull/235) and the commit seven of the live runs checked out, and at fcecfca, which `run` and `controls` checked out, no Go file differs from cde5e38; CI workflow run 39157577288 there succeeded, its `go-db` job with 21 packages `ok` and none failing. A test that skips without a Talos node is marked; it counts only in the live run.

- **R1**: `TestDriftAdoptionIngestion` (internal/api/drift_adoption_test.go:187), `TestDriftAdoptionRefusals` (internal/api/drift_adoption_test.go:218).
- **R2**: `TestPublishRunStopped` (internal/api/worker_test.go:309).
- **R3**: `TestTakeOver` (internal/staging/takeover_test.go:82), `TestTakeOverRefusals` (internal/staging/takeover_test.go:148), `TestTakeOverConcurrentTakers` (internal/staging/takeover_test.go:332), `TestTakeoverResumesToT1` (internal/api/takeover_test.go:112), `TestTakeoverRefusals` (internal/api/takeover_test.go:248), `TestTakeoverNothingToDecrypt` (internal/api/takeover_test.go:351), `TestTakeoverDecryptFailureLeavesResumed` (internal/api/takeover_test.go:382), `TestTakeoverDigestMismatchAbandons` (internal/api/takeover_test.go:432), `TestTakeoverRacesT1` (internal/api/takeover_test.go:456), `TestTakeoverConcurrentTakers` (internal/api/takeover_test.go:538), `TestTakeoverNoEcho` (internal/api/takeover_test.go:564), `TestHeartbeat` (internal/staging/claim_test.go:236), `TestCrashInsideT1LeavesDraft` (internal/api/runner_test.go:343), `TestProviderUnavailableAbandons` (internal/api/runner_test.go:465).
- **R5**: all clause mappings and the additional checks are in the [refusal/access supplement](20261019-poc-coverage.md); earlier named checks: `TestCommitmentRefusals` (internal/api/commit_test.go:274), `TestPlanCreationRefusals` (internal/api/plans_test.go:455), `TestInventoryRefusals` (internal/api/inventory_test.go:355), `TestProblemCodesAreSpecified` (internal/api/router_test.go:98).
- **R7**: `TestCommitmentHoldsItsReads` (internal/api/commit_test.go:637), `TestAttemptHoldsItsReads` (internal/api/attempt_test.go:471), `TestCommitmentReadLockControls` (internal/api/commit_test.go:882), `TestAttemptReadLockControls` (internal/api/attempt_test.go:522), `TestAttemptWaitsForTheMachine` (internal/api/attempt_test.go:668), `TestCommitmentRolloutWaitPrecedesTime` (internal/api/commit_test.go:830), `TestCommitmentRolloutLockControl` (internal/api/commit_test.go:916), `TestCommitmentFreezeRace` (internal/api/commit_test.go:948).
- **R8**: `TestCommitmentRefusals` (internal/api/commit_test.go:274) and `TestAttemptRefusals` (internal/api/attempt_test.go:217), "a newer observation shows another digest", `TestSettle` (internal/api/settle_test.go:374), "bound exhausted after a lost response".
- **R10**: `TestCommitmentPublicationRace` (internal/api/commit_test.go:763), `TestCommitmentPublicationWaits` (internal/api/commit_test.go:990), `TestAttemptAccepts` (internal/api/attempt_test.go:132).
- **R11**: `TestSettle` (internal/api/settle_test.go:374), "contradicted".
- **R12**: `TestExecutorCommitmentIdentityKeys` (internal/api/executor_test.go:306), `TestCommitmentRefusals` (internal/api/commit_test.go:274), `TestSettle` (internal/api/settle_test.go:374), "retry evidence identity", `TestRecoveryScopeMark` (internal/api/recovery_marks_test.go:91).
- **R13**: `TestAttempts` (internal/migrate/attempt_test.go:104), `TestSettle` (internal/api/settle_test.go:374), "safe to retry before the first attempt"; at cde5e38: `TestSettle` (internal/api/settle_test.go:374), "no attempt not completed by its artifact applied" (line 735).
- **R14**: `TestCommitmentRefusals` (internal/api/commit_test.go:274), "frozen"; `TestCommitmentScopeGateControl` (internal/api/epoch_test.go:201), recovery mode before release, with the dropped-gate control.
- **R15**: `TestExecutorTakesOverAtStart` (internal/api/executor_test.go:1188), `TestExecutorCompletesATakenOverAcceptedAttempt` (internal/api/executor_test.go:785), `TestSettle` (internal/api/settle_test.go:374), "no retry", `TestTakeOver` (internal/api/outcome_test.go:79), `TestTakeOverRefusals` (internal/api/outcome_test.go:131).
- **R16**: `TestSettle` (internal/api/settle_test.go:374), "identity revoked after an attempt".
- **R17**: `TestSettle` (internal/api/settle_test.go:374), `TestAccountByDecision` (internal/api/settle_test.go:180), `TestMachineObservedHighestBasis` (internal/api/operations_read_test.go:271), `TestResidualLateRead` (internal/api/residual_test.go:80), `TestRevertRefusals` (internal/api/drift_revert_test.go:140), `TestAdoptionRefusals` (internal/api/adoption_test.go:301); at cde5e38: `TestSettle` (internal/api/settle_test.go:374), "read in flight across a concurrent accounting" (line 815), with its record-order control.
- **R18**: `TestCommitmentDrift` (internal/api/drift_test.go:34), `TestDriftedAdoptionRefusals` (internal/api/drift_adoption_test.go:136), `TestRevertRefusals` (internal/api/drift_revert_test.go:140).
- **R22**: `TestAttemptStaleOwner` (internal/api/attempt_test.go:742).
- **R23**: `TestTakeOverRechecksEligibility` (internal/staging/takeover_test.go:304).
- **R24**: `TestSelection` (internal/orphans/orphans_test.go:249), `TestClusterScope` (internal/orphans/orphans_test.go:309), `TestReleaseDuringRun` (internal/orphans/orphans_test.go:325), `TestHeldPastExpiryUnderLock` (internal/orphans/orphans_test.go:350), `TestFailures` (internal/orphans/orphans_test.go:385), `TestOrphansSelects` (cmd/bronzeward/orphans_test.go:282), `TestOrphansClusterScope` (cmd/bronzeward/orphans_test.go:316), `TestOrphansTokenFileRefused` (cmd/bronzeward/orphans_test.go:346), `TestOrphansTokenReadAtUse` (cmd/bronzeward/orphans_test.go:417), `TestOrphansFailuresPrintNothing` (cmd/bronzeward/orphans_test.go:444), `TestOrphansNoCustomMetadata` (cmd/bronzeward/orphans_test.go:499), `TestOrphansChangesNothing` (cmd/bronzeward/orphans_test.go:558), `TestLiveReportPolicyExact` (internal/provider/report_live_test.go:96), `TestLiveReportIdentity` (internal/provider/report_live_test.go:227), `TestNoOrphanReportRoute` (internal/api/router_test.go:196); at cde5e38: `TestSelection` (internal/orphans/orphans_test.go:249), generations named only by fragment reference rows, with the one-kind-only control in one and two statements (lines 292-293).
- **R25**: `TestCheckNoSchema` (internal/migrate/check_test.go:42), `TestCheckNotInstalled` (internal/migrate/check_test.go:48), `TestCheckOlderSchema` (internal/migrate/check_test.go:53), `TestCheckNewerSchema` (internal/migrate/check_test.go:60), `TestCheckStaleSchemaVersion` (internal/migrate/check_test.go:70), `TestCheckEditedMigration` (internal/migrate/check_test.go:85), `TestRenamedMigrationRefused` (internal/migrate/check_test.go:94), `TestServeRefusesAnUnmigratedDatabase` (cmd/bronzeward/main_test.go:36).
- **R28**: `TestSameKeyInFlight` (internal/api/tx_test.go:214), `TestSameKeyInFlightNoKeyLockControl` (internal/api/tx_test.go:238).
- **R29**: `TestLiveProviderStates` (internal/monitor/live_test.go:129).
- **R32**: `TestClassify` (internal/classify/classify_test.go:60), `TestPassPersistent` (internal/monitor/monitor_test.go:258), `TestPassMetadataIdentity` (internal/monitor/evidence_test.go:64), `TestLiveMetadataIdentity` (internal/provider/identity_live_test.go:170), `TestRecordOneAlertAcrossInstances` (internal/monitor/evidence_test.go:136), `TestWatchdogStalled` (internal/monitor/watchdog_test.go:41), `TestWatchdogSlowPass` (internal/monitor/watchdog_test.go:84), `TestWatchdogHungHolder` (internal/monitor/watchdog_test.go:139), `TestRunHungPasses` (internal/monitor/watchdog_test.go:172), `TestProgressAfterMonitorLock` (internal/monitor/monitor_test.go:554), `TestEncryptArtifactsRefuses` (internal/compile/dependencies_test.go:326), `TestReadPinned` (internal/compile/dependencies_test.go:208), `TestPublishRacingMonitorNamedByAlert` (internal/api/publish_monitor_test.go:129), `TestPublishFirstNamedByTwoNamedByAlert` (internal/api/publish_monitor_test.go:185), `TestPublishFirstNamedConcurrentlyRefused` (internal/api/publish_monitor_test.go:262), `TestPublishAfterMonitorWaitedRefused` (internal/api/publish_monitor_test.go:280), `TestPassDeletionScheduled` (internal/monitor/monitor_test.go:283), `TestLoggerOrderAndCursor` (internal/monitor/logger_test.go:129), `TestLoggerPausedWrite` (internal/monitor/logger_test.go:194), `TestLoggerBacklog` (internal/monitor/logger_test.go:446), `TestDependencyStaleOwnObservation` (internal/api/dependencies_test.go:130), `TestOverlappingMonitors` (internal/monitor/evidence_test.go:252), `TestOverlappingMonitorIdleSession` (internal/monitor/evidence_test.go:282), `TestPassRequestTimeout` (internal/monitor/evidence_test.go:328), `TestLoggerAfterRestore` (internal/monitor/evidence_test.go:385); at cde5e38: `TestClassify` (internal/classify/classify_test.go:60), item 1's clock control (line 300), `TestPersistentEveryInterval` (internal/monitor/transition_test.go:120), item 4 with its no-last-alert control.
- **R33**: `TestExecutorUseTimeCheckFailure` (internal/api/executor_test.go:245).
- **R34**: `TestRecoveryStartMonitorsAfterEntry` (cmd/bronzeward/sweep_test.go:336), `TestRecoveryStartDoesNotSweep` (cmd/bronzeward/sweep_test.go:296), `TestRecoveryStartSweepsAfterEntry` (cmd/bronzeward/sweep_test.go:320).
- **R35**: `TestCommitmentRevocationsWait/approval` (internal/api/commit_test.go:715). `TestExecutorRevocationHeld` (internal/api/executor_test.go:677) exercises the executor through the Talos client against `talostest.Serve`, a stand-in node. Both run in CI and are replayed by `s4ds`; the replay does not establish an additional real-node interleaving (ER choice §10.32).
- **R37**: `TestCommitmentRevocationsWait/identity` (internal/api/commit_test.go:715), `TestIdentityRevocationMachineEntries` (internal/api/revocation_machines_test.go:48), `TestIdentityRevocationHoldsInventory` (internal/api/revocation_machines_test.go:103); at cde5e38: `TestIdentityRevocationHoldsInventory` (internal/api/revocation_machines_test.go:103), the T5c lock with its `FOR SHARE` control (line 106).
- **R39**: `TestRecoveryExitRacesInventory` (internal/api/recovery_exits_test.go:75).
- **R40**: `TestConcurrentRotationsSerialize` (internal/auth/race_test.go:182), `TestConcurrentRotationsNoLockControl` (internal/auth/race_test.go:202), `TestRotationThenIdentityRevocation` (internal/auth/race_test.go:244), `TestIdentityRevocationThenRotation` (internal/auth/race_test.go:264), `TestRevocationRaceNoLockControl` (internal/auth/race_test.go:291).
- **R41**: `TestReissueAfterRestoreRefusedForDeniedIdentity` (internal/auth/tokens_test.go:232).
- **R42**: platform coverage additionally uses `TestInventoryPlatform`, `TestMachinePlatform` and `TestReleaseConstraints` (§6.3); earlier named checks: `TestIdentityKeyConcurrent` (internal/api/inventory_test.go:261), `TestSMBIOSUUIDIsOneMachine` (internal/api/inventory_test.go:210), `TestSMBIOSUUIDControl` (internal/api/inventory_test.go:243), `TestInventoryRefusals` (internal/api/inventory_test.go:355).
- **R43**: `TestMachineIdentityMismatch` (internal/api/machine_ingest_test.go:239), `TestMachineIngestionIdentityControls` (internal/api/machine_ingest_test.go:216), `TestMachineAccessUnavailable` (internal/api/machine_ingest_test.go:314), `TestMachineIngestion` (internal/api/machine_ingest_test.go:180), `TestMachineAccessEventFencedByTheClaim` (internal/api/machine_ingest_test.go:400), `TestMachineAccessErrorTextControl` (internal/api/machine_ingest_test.go:378), `TestLiveTalosAccess` (internal/provider/live_test.go:447); skipped in CI: `TestLiveRoleProbe` (internal/talos/live_test.go:199).
- **R44**: `TestExecutorCompletes` (internal/api/executor_test.go:157), `TestExecutorUseTimeCheckFailure` (internal/api/executor_test.go:245), `TestExecutorNodeSideAccessFailure` (internal/api/executor_test.go:386), `TestExecutorAccessErrorTextControl` (internal/api/executor_test.go:431), `TestExecutorSendsWithTheObservedAccess` (internal/api/executor_test.go:1013), `TestExecutorRetryFollowsAccessRotation` (internal/api/executor_test.go:961), `TestExecutorAttemptAfterAccessRotation` (internal/api/executor_test.go:518).
- **R45**: `TestObservationPurposeAccessFailure`, `TestMachineryCallsAllowlisted`, `TestExecutorRevokedControl`, `TestExecutorRevocationHeld`, `TestObserveNodeReadFails` and `TestObserveErrorTextControl` supplement the earlier checks (§6.3): `TestObservationRecordsTheAccessItRead` (internal/api/observe_test.go:588), `TestObserveAccessReadFails` (internal/api/observe_test.go:304), `TestObserveDrift` (internal/api/observe_test.go:218).
- **R46**: `TestParseEndpointRefuses` (internal/talos/endpoint_test.go:23), `TestTalosEndpointRefusals` (internal/api/endpoint_test.go:78), `TestInventoryTalosEndpoint` (internal/api/inventory_test.go:128), `TestTalosEndpointReplacement` (internal/api/endpoint_test.go:37), `TestTalosEndpointAtomic` (internal/api/endpoint_test.go:121), `TestTalosEndpointConcurrent` (internal/api/endpoint_test.go:152), `TestTalosEndpointInRecoveryMode` (internal/api/endpoint_test.go:178), `TestObservePlanRoute` (internal/api/observe_test.go:484); at cde5e38: `TestObservePlanRoute` (internal/api/observe_test.go:484), with the dispatch-time-resolution control and a later plan bound to the new endpoint.
- **R47**: `TestRevisionsInCommitOrder` (internal/api/observe_test.go:628).
- **R48**: `TestEventStreamEndsAtTokenExpiry` (internal/api/operations_read_test.go:333).
- **R50**: `TestReviewNeedsEncryptedStaging` (internal/api/review_test.go:55), `TestPause` (internal/staging/pause_test.go:35), `TestPausedOwnerTransitionsRefused` (internal/staging/pause_test.go:105), `TestReviewRouteAnswersPaused` (internal/api/review_route_test.go:61), `TestReviewRouteRoles` (internal/api/review_route_test.go:117), `TestReviewRouteFailuresLeaveClaim` (internal/api/review_route_test.go:207), `TestMarkRefusalNamesNoPath` (internal/api/marks_test.go:288), `TestMarkFirstGenerationFailureReturnsPaused` (internal/api/marks_test.go:338), `TestMarkPartWayFailureAbandons` (internal/api/marks_test.go:364), `TestMarkRace` (internal/api/marks_test.go:445), `TestTakeOverPausedRefused` (internal/staging/pause_test.go:136), `TestMarkInterruptedTakeoverPausesWithoutMarks` (internal/api/marks_test.go:470), `TestMarkStoppedAtEncryptionHolds` (internal/api/marks_test.go:510), `TestContinuationKilledBeforeT1TakeoverCommits` (internal/api/continuations_test.go:307), `TestContinuationDraftChecks` (internal/api/continuations_test.go:164), `TestAbandonPaused` (internal/api/review_test.go:172), `TestPausedDueAtExpiry` (internal/staging/pause_test.go:159), `TestRecoveryEntryAbandonsPausedClaim` (internal/api/review_route_test.go:256), `TestStartSweepKeepsPausedClaim` (cmd/bronzeward/sweep_test.go:161); at cde5e38: `TestSelection` (internal/orphans/orphans_test.go:249) and `TestWrite` (internal/orphans/orphans_test.go:425), a paused claim's generations kept while live and reported past its expiry.

### 6.2 Runs per row

This table preserves the original run results and their then-open R5/R45 gaps.
It does not attribute later checks to those historical revisions. The dated
supplement in §6.3 supplies the current additional evidence.

Where each row's live part runs (§3), with the references its checks carry in `<scenario>.tsv`. A
row a pass mode leaves out shows there as a `SKIP` row naming what; the `controls` run, or the run
named, carries it. "CI" is §6.1. Counts given are the checks each reference held in the `s1` step of
the `s6` run, `s6-20261018T191226Z` (`scenarios/s1 run`, 453 checks), the full mode. The S1 review
rows take the path the claim's state allows, so their count can differ between runs.

| Row | Live runs: steps (check references) | CI or carried | Result |
| --- | --- | --- | --- |
| R1 | `run`: `s1p` (`S1 step 1` to `S1 step 5`, `S1 pass`), `s5p` (`S5 step 4`); `controls`: `s1` (`S1 interruption, C §3.4`, 99; `S1 interruption, C §3.5`, 99), `s2` (`S2 negative controls, C §15`: the draft updates), `s5` (`S5 step 4, C §15`: the drift adoption's matrix) | CI: drift adoption | passed |
| R2 | `controls`: `s2` (`S2 negative controls, C §15`: the publications) | CI: the stop at each point | passed |
| R3 | `controls` and `s6`: `s1` (`S1 takeover, C §3.4`, 25; `S1 takeover, C §15 check`, 9; `S1 lease extension, C §3.3`, 5; `S1 interruption`, 2; `S1 Go tests`, 1) | CI | passed |
| R4 | `run`: `s2p` (`S2 precondition, C §10.1`); `controls`: `s2` (`S2 negative controls`, `S2 Go tests`) | — | passed |
| R5 | each scenario's refusals in `run`, and their negative controls in `controls` | CI: the listed refusal tests and specified-codes check; full mapping pending #241 | partially evidenced; clause-level coverage pending #241 (§8) |
| R6 | `s6`: `s4ds` (`S4 DS row 001` to `S4 DS row 021`, the table `ds.tsv`; the rows no request reaches, as the Go checks of `ds-go.log`) | CI: the scan | passed |
| R7 | `run`: `s4` (`S4 step 1` to `S4 step 5`); `s6`: `s4ds` (DS rows 003 and 011) | CI: every named lock test | passed |
| R8 | `controls`: `s3r` (`S3 negative controls`: expiry), `s4c` (`S4 negative controls`: the evidence older than the plan binds, no evidence, nothing refused; ER choice §10.28) | CI: a contradicting newer observation (`TestCommitmentRefusals` and `TestAttemptRefusals`, "a newer observation shows another digest"), the bound | passed |
| R9 | `controls`: `s3r` (`S3 negative controls`: cancellation before commitment), `s4x` (`S4 negative controls`: the plan cancelled once committed with no attempt) | — | passed |
| R10 | `controls`: `s3r` (`S3 negative controls`: the superseded release refused at creation), `s4x` (`S4 negative controls`: comparison 2 refusing the commitment) | CI: the race and the attempt | passed |
| R11 | — | CI: the contradicting digest; the health cases carried (§8) | contradicting digest met in CI; health cases carried (§8) |
| R12 | `run`: `s1p` (`S1 step 1`: two node IDs, one cluster ID; then the imports through the application's client), `s4` (the commitment's evidence observation) | CI: the refusals and their controls | passed |
| R13 | — | CI | met in CI |
| R14 | `controls`: `s4c` (`S4 negative controls`: the publication changing the worker's assignment, 409 `scope-busy`), `s5` (`S5 negative controls`: the frozen-scope revert plan passed over) | CI: comparison 6 under a freeze (`TestCommitmentRefusals/frozen`) and before release in recovery mode (`TestCommitmentScopeGateControl`) | passed |
| R15 | `s6`: `s6` (`S6.1 018` to `S6.1 021`, `S6.1 stale`, `S6.1 send`, `S6.1 cancel`; the table `s6-points.tsv`) | CI | passed |
| R16 | `controls`: `s4x` (`S4 negative controls`: the identity revoked before the commitment, comparison 1; and once committed with no attempt) | CI: after an attempt | passed |
| R17 | — | CI | PoC observation predicates met in CI; bound-health ordering carried to #188 (§8) |
| R18 | `run`: `s5p`; `controls`: `s5` (`S5 negative controls`: the revert on a frozen scope passed over, the unfreeze by a non-approver 403) | CI: the commitment, adoption and revert refusals | passed |
| R19 | `run` and S7 variants: `s7` (`S7 before T`, `S7 at T`, `S7 after T`, `S7 step 1` to `S7 step 11`; step 4 is the closing run's); S7 closing: `s7c` | — | passed |
| R20 | `run`, whole; `s6` | — | live run passed; R5/R45 coverage and the second reviewer remain open (§8) |
| R21 | `controls` and `s6`: `s2` (`S2 Go tests`) | — | passed |
| R22 | `s6`: `s4ds` (`TestAttemptStaleOwner` in `ds-go.log`) | CI | passed |
| R23 | — | CI | met in CI |
| R24 | `controls` and `s6`: `s1` (`S1 interruption, orphans`, 4) | CI | passed |
| R25 | `run`: `s0p`, `s2p` (`S2 pass`); `controls`: `s0` (`S0 negative control`: the schema controls and the restore) | CI: startup | passed |
| R26 | `run`: `s2p` (`S2 step 7`), `s3h`, `s3rp`, `s4`, `s5p` (`S5 step 3`), `s7`; `controls`: `s5roles` (`S5 step 7`); `s6`: `s6` (`S6.1 cancel`, step 7) | — | passed |
| R27 | `run`: `s7` (the recovery-start process in the new epoch); S7 closing: `s7c` (`S7 step 4`: C's takeover and import, 503 `epoch-superseded`; `S7 negative controls`: the second entry, plan and approval on an unaccounted scope, the blocked release, the exit with a scope unreleased, and three `SKIP` rows naming the Go controls) | CI: the dropped-comparison controls `TestFenceEpochTermRow027` (internal/staging/epoch_test.go:138), `TestProcessEpochControl` (internal/api/epoch_test.go:88), `TestCommitmentScopeGateControl` (internal/api/epoch_test.go:201) | passed |
| R28 | — | CI | met in CI |
| R29 | `controls` and `s6`: `s2` (`S2 negative controls (DM §10.1 items 2 and 3)`: `TestLiveProviderStates` in `go-bao-rows.log`) | CI | passed |
| R30 | `run`: `s2p` (`S2 step 6`); `controls`: `s2` (`S2 negative controls (step 6)`) | — | passed |
| R31 | S7 variants: `s7t` (`S7 Transit key`), `s7o` (`S7 older OpenBao`) | — | passed |
| R32 | — | CI | met in CI |
| R33 | — | CI | met in CI |
| R34 | — | CI | met in CI |
| R35 | `s6`: `s4ds` (DS row 003 in `ds.tsv`; `TestCommitmentRevocationsWait/approval` and `TestExecutorRevocationHeld` in `ds-go.log`) | CI: the same approval and executor tests, using a stand-in node | passed; Go interleaving checks (ER choice §10.32) |
| R36 | `controls`: `s4x` (`S4 negative controls`: h-approver's next approval 403 without the approver group, the earlier approval committed and its attempt admitted), contrasted with `s3r` (`S3 negative controls`: the approval revocation) | — | passed |
| R37 | — | CI | met in CI |
| R38 | `controls` and `s6`: `s2` (`S2 Go tests`: `TestPublishRunLapsed`) | CI | passed |
| R39 | — | CI | met in CI |
| R40 | — | CI | met in CI |
| R41 | — | CI | met in CI |
| R42 | — | CI | met in CI |
| R43 | `controls` and `s6`: `s1` (`S1 access`, 14; `S1 access, PA §16`, 1; `S1 access, PA §16 check`, 6; `S1 inventory, PA §16 check`, 5; `S1 Go tests`: `TestLiveRoleProbe` against the fixture's nodes); S1 node ID: `s1nid` (the refusal for another node ID) | CI | passed |
| R44 | `run`: `s4`; `s6`: `s4ds` (the scans of its refused and interrupted dispatches) | CI | passed |
| R45 | `run`: `s4`, `s5p`, `s7` | CI | partially evidenced; observation-access coverage pending #241 (§8) |
| R46 | — | CI | met in CI |
| R47 | — | CI | met in CI |
| R48 | `run`: `s8` (`S8 step 3`); S8 controls: `s8controls` (`S8 negative controls`), `s8revocation` (`S8 revocation alone`) | CI | passed |
| R49 | `run`: `s1p` (`S1 backups, PA §16`; `S1 step 5`), `s2p` (`S2 backups, C §15`) | — | passed |
| R50 | `controls` and `s6`: `s1` (`S1 review, PA §16`, 47; `S1 review kill, PA §16`, 90) | CI | passed |

### 6.3 Refusal and observation supplement (19 October 2026)

[#241](https://github.com/ginsys/bronzeward/issues/241)'s
[clause-level report](20261019-poc-coverage.md) maps all C §13 refusals,
PA §13 examples and PA §14 refusals, and every observation-access clause of
R45. Its [sanitized results](evidence/20261019-poc-coverage.txt) bind the fresh
checks to `c9398d45c1b690e8e11ce541dbb76af0ec690308`: the normal database/provider
suite passed (21 tested packages); the focused run passed 17 tests, with 63
PASS lines and six explicit environment skips. Both removed-authoring-scope
controls failed as intended. Log screens and their positive control passed;
the report states their exact limits. No new live Talos result is claimed.

The audit found a publication-stage defect: an inconsistent stored source could
reach a provider read across clusters. Supported authoring already filtered that
reference. [#244](https://github.com/ginsys/bronzeward/issues/244) repaired the
missing publication check and landed as `bc59346428ea267addfca37881c14eb4ecfda108`
([repair evidence](20261019-publication-cluster-scope.md)); the retained failing
probe is not evidence of remote exploit reachability.

R42's platform/mode assertions were also traced and rerun. R11 and R17 retain
future bound-health validation in #188. The original scenarios, revisions,
failed attempts, skips and fixture limits above remain unchanged. This
supplement supplies additional R5/R45 evidence; it does not supply the independent
S8 review, any of the six delegated criterion verdicts (§7), or the dated
assessment (§9).
Local review then reproduced a second publication defect: a node-minor mismatch
did not refuse publication at the reviewed revision.
[#247](https://github.com/ginsys/bronzeward/issues/247) now has separately
recorded repair evidence in the [publication report](20261019-publication-node-evidence.md):
the database/provider gate at `e04d627`, four guard-removal controls and full
S2 at `aede267`, and the repaired integrated chain at `44e8cf8`. The
clause-level report links each exact revision and preserves the failed chains.
Its #247 report reference at `032ecd4` also retains the S2 assertion controls at
`6fe0af3` (old assertion: 13 PASS / 15 FAIL, expected exit 1; corrected assertion:
28 PASS, exit 0; zero SQL errors) and the committed-reply-loss checks at
`7d06094` (seven top-level tests, nine PASS records, zero FAIL/SKIP, exit 0).
The former use nullable temporary tables, not evidence of reachable production
corruption. The latter retain already-committed success and original evidence
without reclaim or reread; the rolled-back `08006` control remains a separate
unfinished-operation case. Neither replaces any live run. PR248 landed at
`b3a479c0c0a4170d95d33e71b0ce799cd51a5eb7` on 2026-10-19
at 19:14:04 UTC, with an equal verified tree and passing branch/queue CI;
#247 was assessed and closed. R2's current summary names all seven publication interruption points
and retained observation entries; §6.2's original runs remain unchanged.
Provider-wide outages now refuse at the required publication access read;
`TestReadPinnedRefuses` separately retains compiler dependency-read coverage.
Failed publication observation starts/results can persist without a release.
The original 25-case run covers five purposes. At `1cc6ddc4960615e42db646e21e8428960d95606f`,
the fresh combined-tree run passed 22 focused top-level tests (100 PASS records,
no FAIL/SKIP), including all 30 named failures across six purposes and 12
latest/stale loop assertions. The full verbose database/provider suite passed
21 tested packages (two without tests), with 6 explicit environment skips
retained in the [sanitized results](evidence/20261019-poc-coverage.txt).
Both complete logs passed the four-canary/private-key-header screen with its
five-pattern positive control. Exact commands, versions, timing and hashes are
retained there; no new live Talos or human S8 review is claimed. PR249 landed as
`c9b70ffbf4d3b13b775ee949d67bca1f03183294` on 19 October at 19:57:05 UTC;
[#241 was assessed and closed](https://github.com/ginsys/bronzeward/issues/241).
The review also identified missing synchronous T1 database-failure
response coverage. The added check and its successful-commit control pass at
`02d012c222d51714a9ce08b7087f8c9d1fab35fb`; changing the failure's HTTP 503 to
500 makes the check fail as intended. The supplement retains both outcomes.

## 7. Acceptance criteria of ginsys/bronzeward#31

The current verdicts below come from the
[20 October delegated assessment](20261020-poc-timeline-assessment.md#3-criterion-verdicts-and-independent-review)
and its independent agent timeline reading. The owner's negative usability feedback
and the assessment's limits still apply. The
[original pending verdicts](https://github.com/ginsys/bronzeward/blob/c9b70ffbf4d3b13b775ee949d67bca1f03183294/docs/design/research/20261018-poc-acceptance-evidence.md#7-acceptance-criteria-of-ginsysbronzeward31)
remain in the historical revision; no run or human-review skip is relabelled.

| Criterion | Answered by | Verdict |
| --- | --- | --- |
| Adoption without mutation, extraction before persistence, immutable encrypted artifacts with redacted provenance | §5 items 1 to 3 | Technical evidence met by the complete campaign and guided adoption |
| Publication alone cannot dispatch; plan and approval bind the exact safe no-reboot worker change | §5 items 3 and 4 | Met; proposal/publication performed no apply, and immutable plan bindings were re-read before approval |
| Direct apply and verify, separate desired/applied/observed, a usable approval-to-outcome timeline | §5 item 5 and the timeline row | Met for the PoC: execution, traceability and independent factual interpretation; no general usability pass |
| Drift report, freeze, sanitized adoption, approved revert | §5 item 6 | Met on the selected fixture, preserving earlier refusal coverage |
| Interrupted-operation and restoration recovery; unresolved old execution blocks conflicting mutations | §5 item 7 (both rows) | Met within the documented decision-based recovery limits |
| Reproducible integrated evidence and owner assessment, with limitations and no production claim | §3, §4, §9 and §10 | Met once this assessment lands, supported by the retained campaign, delegated assessment and independent reading |

## 8. Gaps and carried items

- R11's health-window cases and R17's bound-health observation-ordering cases are carried to
  [ginsys/bronzeward#188](https://github.com/ginsys/bronzeward/issues/188). The PoC binds no
  health check (ER choice §10.33).
- Found while mapping §6 to the tests in CI, and closed by
  [ginsys/bronzeward#233](https://github.com/ginsys/bronzeward/issues/233) (merged as cde5e38,
  tests in §6.1):
  - **R24:** the orphan report read `import_base_reference` only, so a generation that only a
    `fragment_reference` row named was reported. A code defect, fixed: both kinds are read, with
    the one-kind-only control.
  - **R13**, **R17**, **R32**, **R37**, **R46**: the missing test or control added.
  - **R50:** paused claims in the orphan report added. A mark's run killed at each point it
    re-enters has no Go test: AP S1 assigns it to the live run's review rows
    ([AP S1](../../spec/acceptance-plan.md#s1-adoption-of-the-existing-cluster)), so R50 rests on
    them.
- Open after the original runs; later disposition is in the dated assessment (§9):
  - **The timeline's second reviewer** (§5): AP S8's pass criterion needs a second reviewer to reach
    the same answers from the timeline; `run`'s `s8` writes `SKIP` for it and for the reads after
    the operator walkthrough of ginsys/bronzeward#30, by a person. That added characterization
    is distinguished from AP S8's actual reviewer criterion in the 20 October assessment.
- **R5 and R45 additional evidence:** the clause audit, missing checks and controls
  are recorded in §6.3 and the linked #241 supplement. The publication defect
  found by that audit was separately repaired and landed under #244. A second
  publication-minor defect has separate repair evidence under #247, mapped in
  §6.3 with its verified PR248 landing. The synchronous T1 failure check and
  control passed, with the wrong-status control failing. The fresh six-purpose
  matrix and full database/provider suite passed at `1cc6ddc`; #241's
  publication, review and closeout assessment are complete (§6.3). These
  later results do not retroactively change any original run's coverage.
- Pointer `SKIP` rows, each naming the check that carries what it leaves out: S7's three
  dropped-comparison controls (§6.1, R27), S4's frozen-scope, index-drop and revocation-wait rows
  (`s4ds`, `s5`, §6.1), and S0 step 3's earlier-epoch automation token (S7 step 6, §5).

## 9. Owner assessment

On 20 October 2026 the owner delegated straightforward factual assessment to the
executing agent. Its [dated assessment](20261020-poc-timeline-assessment.md)
records the criterion verdicts, exact timeline evidence and limitations. The owner
separately assessed the API-and-script operator experience negatively. This is
not a claim that the owner performed the walkthrough or independently interpreted
the four timelines. The independent agent reading and delegated S8 disposition are
recorded there; issue closure still requires this evidence to land.

## 10. Limits

- The fixture's limits (AP §9) bind every result. A scenario shows the build it ran against, on
  the paths it drove.
- Excluded by design §18.2, and not exercised: new-machine enrolment, reset and reuse, cluster
  bootstrap, upgrades, remote transports and managed-cluster etcd recovery
  ([AP §8](../../spec/acceptance-plan.md#8-not-covered-by-the-poc)).
- No result here is a production-readiness claim.

## 11. Publication node evidence supplement (2026-10-19)

ginsys/bronzeward#247 adds fresh read-only executor observations before
publication, including first adoption, and retains each machine's evidence
link. The [repair report](20261019-publication-node-evidence.md) records the
full database/provider gate at `e04d627`, the matching and refusal stand-ins,
four guard-removal controls at `aede267`, full S2 with 373 passing checks at
that same revision before its chain stopped at S3, and the fresh live chain
at `44e8cf8`:
`up s1p s3h s2p s3r s4c s4x s4 s5p s7 s7n s7t s7o down`.
Every step passed; all 48 bundles passed the revision, empty-diff,
capture and positive-control-only scan audit; teardown completed. Pass-mode
skips and all three failed attempts remain explicit in the
[sanitized evidence](evidence/20261019-publication-node-evidence.txt).

The live profile is Talos 1.13.6. Different/missing/malformed version reports
are stand-in evidence, not a second live profile or an upgrade. Provider-wide
outages now refuse at the required publication node-access read before compiler
pinned-value reads; the separate compiler refusal tests retain that coverage.

This supplies the publication-minor repair evidence for R5. The clause-level
R5 mapping and all-purpose R45 access checks remain with
ginsys/bronzeward#241, including the new publication purpose. The older rows
above retain their original revisions and limitations. This supplement alone
supplies no independent S8 review or acceptance assessment; the later reading
and six delegated verdicts are recorded separately in §7 and §9.
