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

Retained evidence bundle: `walkthrough-31-20261020`.
The owner retains the complete captures and a sanitized four-case package. Each case
contains `operation.json`, `events.json`, `timeline.json`, `observations.json`
and individual observations; the guided plans are in `human-review/api/`.
The separate S6 plan's proposed-state response and approval were recovered from
original HTTP captures `0047.txt`/`0048.txt` into `independent-review/`, with a
source/extraction manifest. The original input manifest still verifies all 542 files.
Databases, provider snapshots and credentials remain outside this small package.

To reproduce the analysis, follow the [timeline-reading guide](../../operator/troubleshooting.md#reading-the-timeline)
and [recovery reads](../../operator/recovery.md#read-it-first). Follow every page
and linked observation. For a mechanical comparison, the pinned
[`fixtures/s8-answers.jq`](../../../fixtures/s8-answers.jq) accepts the four case
directories below. Set `BW_REPO` to the absolute checkout path and `BW_EVIDENCE`
to the absolute retained-bundle path; the command then works from any directory:

```sh
: "${BW_REPO:?Set the absolute Bronzeward checkout path}"
: "${BW_EVIDENCE:?Set the absolute walkthrough-31-20261020 bundle path}"
for BW_CASE in 01-worker-apply 02-drift 03-interruption 04-restoration; do
  BW_CASE_DIR="$BW_EVIDENCE/human-review/$BW_CASE"
  jq -n --slurpfile op "$BW_CASE_DIR/operation.json" \
    --slurpfile ev "$BW_CASE_DIR/events.json" \
    --slurpfile tl "$BW_CASE_DIR/timeline.json" \
    --slurpfile obs "$BW_CASE_DIR/observations.json" \
    -f "$BW_REPO/fixtures/s8-answers.jq" || exit
done
```

The primary assessment ran six canonical `linked`/`applied` predicates from
[`fixtures/scenarios/s8`](../../../fixtures/scenarios/s8) over these records:
all exited 0. Twenty-three direct source cross-checks also passed. These checks
support consistency; they do not substitute for independent interpretation.
Complete captures and positive-control secret scans are retained with the runs.

## 2. Four timeline readings

Revisions below are machine-scope commit order, not a global clock. Attempts
precede sending and cannot prove delivery. Digests are withheld from served records;
drift/convergence judgements and separately attributed harness checks have distinct bases.
Shortened IDs below are unique prefixes in the retained case; full IDs remain in its JSON.
Backup times are operator-entered; use each entry's epoch, not an event ID's read-time epoch.

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
  Applied moves `rel_yend…` → `rel_qczl…` at 25. Separately, `worker-apply/guided-apply.tsv` rows 15/19/20/22
  establish the worker change, unchanged control plane and state convergence.

### Drift, adoption and revert

Revert operation `op_yefcszh4txsxfcckfl5vsohzfe`, plan
`pln_v5kacaliyd73722kspaxty626e`, in the same pre-restoration epoch.

- Drift 46 opens; author `idn_3zqiy6psrcymxc3fbxvvbzfika` freezes at 71.
  Approver `idn_q6bzuafr5x6jp2mlpnltaecjne` approves adoption plan
  `pln_ubxzhc372azatqi6wl47asd3gu` at 135: adoption 138 has no attempt,
  Applied 139 names `rel_u3pm…`, and drift 140 closes adopted.
- The same approver approves the revert at 187 (`unseenOverwrite=true`) and
  unfreezes at 188, after a second drift opens at 161; commitment follows at 195.
- Attempt 196 (bounds below) receives accepted response 197, accounting 198;
  completion observation `obs_h4lw…` supports completed 203 and reverted drift 205.
  Observed resource versions progress 3 → 4 → 5 across the two drifts and revert.
- Separately, `s5-pass-20261020T110711Z/s5.tsv` row 26 proves the missing-mark
  refusal before secret persistence. Unfreezing does not itself approve a revert.

### Lost-response interruption

Original separate S6 record `s6-run-20261018T205620Z`, operation
`op_aer7okgheiftyrkmlvoaijxqdu`, plan `pln_i4ipf5rll3b7okk24syob3lfji`.
Its epoch is `ep_ub5hbpow5yfbakbopuqliqayke`; identifiers belong to that separate fixture.

- Approver `idn_vs7fthjzcy2egcbisobnn2vvw4` approves at 449 before commitment 453
  and attempt 454. Transport response 455 has code 14; unresolved 456 precedes takeover 457.
- Accounting 465 is a recovery-admin decision by
  `idn_nqb3frxy2gd6lwb72l7knjs7e4`, citing start observation
  `obs_iwzo2oh3ci4q66gnc4cfuvfllq` and end observation
  `obs_pkdrj4h65gpcre3rr4wkfgbsy4`: versions 20/20, anchor
  `2026-10-18T21:20:09.306882Z`, settle floor 30 s. Completion later observes version 21.
- Classification 468 permits retry only after that accounting. Attempt 470
  receives accepted response 471; subsequent observation supports completion 477.
- Conflicting plan `pln_h5fp…` at 460 is approved at 461 by that approver, revoked
  at 462 and never committed. Wrong-role accounting is separately proved by
  original `s6.tsv` row 151. Decision accounting is not proof of non-delivery.
  The focused 20 October replay does not replace this record.

### Restoration

Restored operation `op_2sk3bf4brucrnjivezuan3uxla`, plan
`pln_y24wwajahj7iwmdycs7zijszda`; recovery epoch
`ep_sr34wsje3iirfyupzmrcpfpnya`.

- `idn_q6bzuafr5x6jp2mlpnltaecjne` approves `pln_y24wwajahj7iwmdycs7zijszda`
  at 264 (`apr_ntvgkddvplirvguxdg5hgshboe`), then the separate waiting plan
  `pln_upizcgfor7upcuybk6rpa7ss2a` at 270 (`apr_h3cyj553pmiiha477xqwzvt2ie`).
  Both target `rel_a57a…` in the old epoch.
  Approval 270 still looks live after restoration; its later revocation was erased.
  Commitment 268 precedes the backup; recovery ownership 271, unresolved 272 and hold 273 follow entry.
- Recovery-admin `idn_n2fmoxkmpf4yalolmhwkczx2wm` records scope accounting 279,
  citing `obs_t4kfff4ri4tsbb3gvi4oxwgbue` and
  `obs_l7ecpovyfph4ysxky4h5fyuhmu`, resource versions 7/7 and the 30-second settle floor.
- State 281 is cancelled: its approval belongs to the earlier epoch.
  No attempt exists in the restored journal, and that operation is not retried.
- Served observations show version 6 at `obs_6ixi…`, then 7 at accounting and drift
  284; they establish change, not its cause or time relative to the backup.
  The post-backup send is separately established by `s7-run-20261020T111224Z/s7.tsv`
  rows 32/76/94; rows 36/53 establish the erased revocation.
- The same approver approves `pln_cvdwpkq4dks322scc2bamb6lri` at 290 in the new epoch
  (`unseenOverwrite=true`). Its operation `op_q6fkjnhwd2them6pns7afzpkaq` attempts at 295,
  receives accepted response 296/accounting 297, observes version 8 at `obs_4lz2…`,
  completes at 302 and moves Applied from `rel_eojo…` to newly published `rel_gfmr…`
  at 303. Drift closes reverted at 304; exit 305 returns both scopes to normal.
- Zero attempts in restored history is not evidence that no post-backup request
  reached the node. The old and new epochs must remain distinct.

The remaining attempts all use `10.55.0.3:50000`; times below are UTC on the case's date.

| Case / revision | Recorded | Transport deadline | Verification deadline |
| --- | --- | --- | --- |
| Drift / 196 | 11:08:52.875814 | 11:10:52.875814 | 11:13:52.875814 |
| S6 / 454 | 21:19:36.593424 | 21:20:06.593424 | 21:24:36.593424 |
| S6 / 470 | 21:20:49.345186 | 21:21:19.345186 | 21:25:49.345186 |
| Restoration revert / 295 | 11:19:21.01762 | 11:21:21.01762 | 11:24:21.01762 |

## 3. Criterion verdicts and independent review

The six delegated criterion verdicts are recorded in the
[acceptance record's current table](20261018-poc-acceptance-evidence.md#7-acceptance-criteria-of-ginsysbronzeward31).
This assessment supplies their dated rationale and independent-review evidence;
it does not maintain a second verdict table.

Claude independently read the four served captures before comparing this report
at `91b71d7`. Its retained `independent-review/claude-initial-review.md`
(SHA256 `ed5626582b0e96ca597e1e88b99a90d8c96961037624788f15692787cfe96502`)
identified missing links and harness/timeline attribution; §2 corrects those findings.
The confirming Claude review `6c80ac46` at `ad89e68` found all five fixes complete
and no new defects. Its retained `independent-review/claude-confirmation.md`
(SHA256 `ed61896c9ece78c75df98873f304e8e91f3f73c5fcede67c31916033cb4e4018`)
records the actual procedure: read scope-ordered timelines, operations, recovery
events, linked plans/approvals and observations; it reported agreement on all four cases.
It made no live API calls, exercised no event stream and did not verify pagination or hashes.

That confirmation missed a restoration approval mismatch subsequently identified
by hosted Codex review at `35e4c1c`. The retained timeline and linked plan responses
bind approval 264 to `pln_y24…` and approval 270 to `pln_upiz…`, as corrected in §2.
This correction supersedes that part of the earlier confirmation; the original
review is retained unchanged and does not prove the mistaken binding.

**Delegated factual disposition:** S8's independent reading is met. The reviewer left
two interpretations open: whether an agent qualifies and whether retained-response
reading suffices instead of issuing the walkthrough's live, paged GETs.
This assessment accepts both: AP S8, ER §9.3 and design §18.2 name no human or
owner-named reviewer; AP S8 explicitly takes the separately retained S6 timeline
and calls for evidence “as served”. The reviewer followed the documented reading
procedure over those responses. Capture completeness rests on the original harness's
retain-every-page check; stream/authentication coverage remains in the original S8
run (acceptance record R48). Neither was independently re-exercised here.
The older “person” wording was an added characterization, not an acceptance-plan
condition. This disposition uses the owner's factual-assessment delegation, not
reviewer authority or a waiver. Original person-read SKIPs remain historical. Neither this review nor
the [earlier agent review](20261018-operator-docs-reviewer-run.md#1-setup) proves unaided human usability.

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
- After the guided runs listed in §1, the documentation repairs added no runtime
  scenario; their verification re-read retained evidence and replayed the analysis.
- Issue31 and milestone03 remain open until this assessment lands and closure is
  recorded in the authoritative tracker. Licence and later implementation exclusions remain.
