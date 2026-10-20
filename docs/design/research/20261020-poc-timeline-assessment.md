# PoC timeline and delegated technical assessment

| | |
| --- | --- |
| Date | 20 October 2026 |
| Work item | [E6: demonstrate PoC acceptance](https://github.com/ginsys/bronzeward/issues/31) |
| Contracts | [Design §18.2](../Talos_Configuration_and_Machine_Management_Design.md#182-phase-1---configuration-control-on-an-existing-cluster), [AP S8](../../spec/acceptance-plan.md#s8-the-operation-timeline-as-an-operator-reads-it), [ER §9.3](../../spec/execution-recovery.md#93-gaps-carried-and-what-would-close-them) |
| Primary assessor | The Codex agent that executed the guided walkthrough; not an independent reviewer |
| Decision enabled | Assess the technical PoC from retained records and distinguish timeline traceability from operator convenience |

## 1. Authority and evidence

The owner delegated straightforward factual assessment to the executing agent on
20 October: “Do this yourself for the obvious cases.” This report records that
agent's conclusions, not a claim that the owner executed the commands or independently
read every timeline. The owner separately assessed the presented operator experience
negatively (§4). Authorization to execute a demonstration is not itself acceptance.

The [acceptance record](20261018-poc-acceptance-evidence.md) remains the source for
the complete integrated run, controls and matrices. This report supplements it:
it does not change their revisions, results, failed runs or explicit skips.

The guided walkthrough used the clean revision
`c9b70ffbf4d3b13b775ee949d67bca1f03183294`, the existing disposable fixture pins,
and synthetic credentials. The separate full S6 record below remains at
`ab327884527fee3b3ed32f0e2dfc8ffde5bf0ce3`. No source files changed during execution.

| Guided stage | Retained record | PASS / FAIL / SKIP |
| --- | --- | --- |
| Adoption | `adoption-execution` | 34 / 0 / 0 |
| Worker proposal | `worker-proposal` | 26 / 0 / 0 |
| Worker apply | `worker-apply` | 28 / 0 / 0 |
| Drift pass | `steps-20261020T110711Z` | 73 / 0 / 3 |
| Focused lost-response replay | `lost-response` | 58 / 0 / 0 |
| Nominal restoration | `steps-20261020T111224Z` | 128 / 0 / 0 |
| Final API reads | `handoff-api` | 74 / 0 / 0 |

The S5 skips are the frozen-revert control, ingestion interruption matrix and wrong-role
unfreeze control, already exercised in the earlier complete campaign. The focused
same-startup lost-response replay supplements the original separate S6; it does not
replace that scenario's other interruption points or refusal controls.

The owner retains the complete captures and a sanitized four-case package. Each case
contains `operation.json`, `events.json`, `timeline.json`, `observations.json`,
individual linked observations and plans. The package's input manifest hashes the
original copied records. Full databases, provider snapshots and credentials are
excluded from that small package and remain in the original captures.

To reproduce the analysis, follow the [timeline-reading guide](../../operator/troubleshooting.md#reading-the-timeline)
and [recovery reads](../../operator/recovery.md#read-it-first). Follow every page
and linked observation. For a mechanical comparison, the pinned
[`fixtures/s8-answers.jq`](../../../fixtures/s8-answers.jq) accepts:

```sh
jq -n --slurpfile op operation.json --slurpfile ev events.json \
  --slurpfile tl timeline.json --slurpfile obs observations.json \
  -f fixtures/s8-answers.jq
```

The primary assessment ran six canonical `linked`/`applied` predicates from
[`fixtures/scenarios/s8`](../../../fixtures/scenarios/s8) over these records:
all exited 0. Twenty-three direct source cross-checks also passed. These checks
support consistency; they do not substitute for independent interpretation.
Complete captures and positive-control secret scans are retained with the runs.

## 2. Four timeline readings

Revisions below are machine-scope commit order, not a global clock. An attempt
entry precedes sending and cannot alone prove that the request left the process.

### Normal worker apply

Operation `op_p6ts4luxskabqvs5qbnmeycje4`, plan
`pln_kceberl7fmjm35hz45aoaos22e`, epoch `ep_uscegvuhoz65rmnviaomq6amje`.

- Approval revision 12 names approver `idn_q6bzuafr5x6jp2mlpnltaecjne`.
  Commitment 16 binds that approval, plan and evidence observation.
- Attempt 17 records the worker route `10.55.0.3:50000`, evidence observation,
  controller/generation and both deadlines. It was recorded at
  `2026-10-20T11:06:35.219438Z`; transport and verification deadlines are
  `11:08:35.219438Z` and `11:11:35.219438Z`.
- Response 18 is accepted; accounting 19 rests on that response.
  This case requires no recovery-admin accounting decision.
- Completion observation 22 follows observation-started 21. Classification 23
  relies on the response, accounting and observation; state 24 is completed and
  Applied moves at 25. The captured node checks show the worker changed and the
  control plane did not; Desired, Applied and Observed converge.

### Drift, adoption and revert

Revert operation `op_yefcszh4txsxfcckfl5vsohzfe`, plan
`pln_v5kacaliyd73722kspaxty626e`, in the same pre-restoration epoch.

- Drift 46 opens, freeze 71 precedes adoption 138, and closure 140 is adopted.
  The missing-secret-mark control was refused before secret persistence.
- A second drift opens at 161. Approval 187 explicitly records
  `unseenOverwrite=true`; unfreeze 188 precedes commitment 195.
- Attempt 196 receives accepted response 197. The operation completes at 203
  and the second drift closes reverted at 205.
- These are two different drift records. Adopting the first does not explain
  away the second, and unfreezing does not itself approve a revert.

### Lost-response interruption

Original separate S6 record `s6-run-20261018T205620Z`, operation
`op_aer7okgheiftyrkmlvoaijxqdu`, plan `pln_i4ipf5rll3b7okk24syob3lfji`.
Its epoch and resource identifiers belong to that separate fixture.

- Approval 449 precedes commitment 453 and attempt 454. Transport response 455
  has code 14; state 456 is unresolved, followed by takeover 457.
- Accounting 465 is a recovery-admin decision by
  `idn_nqb3frxy2gd6lwb72l7knjs7e4`, citing start observation
  `obs_iwzo2oh3ci4q66gnc4cfuvfllq` and end observation
  `obs_pkdrj4h65gpcre3rr4wkfgbsy4`.
- Classification 468 permits retry only after that accounting. Attempt 470
  receives accepted response 471; subsequent observation supports completion 477.
- The original full S6 also exercised conflicting-plan waiting and wrong-role
  accounting. Accounting by decision is not proof that the first request never
  reached the node. The focused 20 October replay has its own identifiers and
  does not replace this record.

### Restoration

Restored operation `op_2sk3bf4brucrnjivezuan3uxla`, plan
`pln_y24wwajahj7iwmdycs7zijszda`; recovery epoch
`ep_sr34wsje3iirfyupzmrcpfpnya`.

- Approval 264 and commitment 268 precede the backup boundary.
  Recovery ownership 271, unresolved state 272 and scope hold 273 follow recovery entry.
- Recovery-admin `idn_n2fmoxkmpf4yalolmhwkczx2wm` records scope accounting 279,
  citing `obs_t4kfff4ri4tsbb3gvi4oxwgbue` and
  `obs_l7ecpovyfph4ysxky4h5fyuhmu`, resource versions 7/7 and the 30-second settle floor.
- State 281 is cancelled: its approval belongs to the earlier epoch.
  No attempt exists in the restored journal, and that operation is not retried.
- A request after the backup had changed the node. Fresh observations expose
  that discrepancy; a fresh approved revert closes drift at 304, and exit 305
  returns the scopes to normal.
- Zero attempts in restored history is not evidence that no post-backup request
  reached the node. The old and new epochs must remain distinct.

## 3. Criterion verdicts and independent review

| Issue criterion | Primary delegated verdict |
| --- | --- |
| Adoption without mutation, extraction before persistence, immutable encrypted artifacts and redacted provenance | Technical evidence met by the complete campaign and guided adoption |
| Publication cannot dispatch; approval binds the exact worker change | Met; proposal/publication performed no apply, and immutable plan bindings were re-read before approval |
| Apply/verify, separate desired/applied/observed and usable timeline | Technical execution and traceability met; independent timeline interpretation pending |
| Drift, freeze, sanitized adoption and approved revert | Met on the selected fixture, preserving earlier refusal coverage |
| Interruption/restoration recovery and conflicting-execution blocking | Met within the documented decision-based recovery limits |
| Reproducible evidence and assessment with limitations | Primary delegated assessment recorded; final acceptance awaits independent review disposition |

AP S8 asks a second reviewer following the walkthrough to reach the same answers.
The [earlier documentation reviewer](20261018-operator-docs-reviewer-run.md#1-setup)
was explicitly an agent that authored neither page. That is relevant precedent
for factual review, not evidence that this report already received one.

The original fixture's person-read and second-reviewer `SKIP` entries remain
historical facts. This report does not convert an agent review into a person's
trial. The independent reviewer must identify its actual provenance and report
any questions the served records cannot answer. No second-reviewer verdict is
claimed in this revision.

## 4. Human-facing usability

The owner assessed the demonstrated experience as inadequate: even simple queries
required a convenience wrapper, while interaction remained a raw API.
Source inspection supports that finding. The executable provides `serve`,
`migrate`, `token` and `orphans`; it has no ordinary configuration-workflow CLI.
There is no web UI. The documented path uses shell functions, HTTP requests,
JSON, resource identifiers and conditional request headers. The temporary viewer
wrapper was demonstration tooling, not a product client.

This is a negative usability assessment now, not merely an unperformed study.
It does not contradict the narrower PoC requirement for a minimal authenticated
API, and it must not be represented as satisfactory day-to-day operator usability.
A supported client needs separately scoped product work; this report chooses
neither a CLI nor a web UI and adds neither to PoC acceptance.

## 5. Limits and remaining work

- The first apply plan withholds its diff because its adopted baseline has no
  equivalent redacted whole configuration, as PA §9.3 and choice §17.38 permit.
  Target review and publication validation are not a live-baseline diff.
- No health check is bound. Completion proves the bound configuration
  postconditions, not workload health or production readiness.
- Observation-based accounting and the settle floor do not establish a universal
  upper bound on late request delivery.
- The earlier acceptance campaigns retain responsibility for negative matrices,
  interruption points and variant coverage omitted by the guided pass.
- No new runtime scenario was run to prepare this assessment.
- Issue31 and milestone03 remain open pending independent review and final
  acceptance disposition. Licence and later lifecycle/transport exclusions remain.
