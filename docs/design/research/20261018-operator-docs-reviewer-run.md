# Operator documentation: a reviewer run from a fresh checkout

| | |
|---|---|
| **Date** | 18 October 2026 |
| **Work item** | [Operator documentation and PoC package](https://github.com/ginsys/bronzeward/issues/30) |
| **Pages run** | [Operator guide](../../operator/README.md), [walkthrough](../../operator/walkthrough.md) (S0-S5), [recovery](../../operator/recovery.md) (interrupted operations and restoring) |
| **Decision enabled** | Whether the operator pages can be followed as written on the disposable fixture, and which steps had to change first. |

## 1. Setup

A fresh clone of `ginsys/bronzeward` at c4beda4 (main after the walkthrough PR), outside every
worktree, with no local state carried over. The recovery page was run from its pull request's
head (cd8226d), before that pull request merged.

Pinned by the checkout: Go 1.27.1 (`mise.toml`); Talos v1.13.6, Kubernetes 1.36.2, PostgreSQL 17
(alpine digest), OpenBao 2.6.1 (`fixtures/versions.env`). Host: Docker Engine 26.1.5, Linux amd64;
mise 2026.5.18 with `paranoid` false and no trusted-path settings for the clone's directory.
Every secret is the fixture's synthetic material; the one rotated automation token was written to a
mode-600 file and never printed.

The reviewer is an agent session that wrote neither page. It ran each code block in order with the
pages' own helpers (`mint`, `call`, `etag`). Where a section starts from a state the page does not
create (an operation cut off mid-flight), the run produced it with the fixture's interruption seam
and link cuts, the mechanics `fixtures/scenarios/s6` uses (the scenario itself was not run, so its
own prerequisites do not apply). Observation and generation choices followed the pages' prose rules
over the full listings.

The interrupted states were produced on the fixture the walkthrough left, with A paused throughout.
`interrupt <step> <kill|stall>` writes `<step> <kill|stall>` to instance C's control file
`fixtures/.state/server/c/interrupt` by rename, and `interrupt -` removes it. Each release changed
the label of the s3-handover fragment, was published, and was planned for the worker and approved.

| Case | Sequence | State reached |
|---|---|---|
| Cancel | `inject linksplit B worker`; `inject kill B`; start C; release; `interrupt dispatch-committed kill`; plan (transport 120 s) and approve; wait until C exits; `interrupt -`; `inject netsplit worker`; `inject start B`; `inject netjoin worker` | `unresolved` with no attempt; B cannot read the worker until `inject linkjoin B worker` after the resolution |
| Send | `inject kill B`; `inject start C`; release; `interrupt dispatch-attempted stall`; plan (transport 30 s) and approve; wait for one attempt; `inject netsplit worker`; `interrupt -`; wait for `unresolved` | one attempt with no response |
| Snapshot T | in the send case's state: `inject db-snapshot rv30-t`, then `inject bao-snapshot rv30-t` (12:06:22 UTC); then `inject start B`; `inject netjoin worker` | restored in recovery step 1 after the send case completed |

## 2. Results

| Page, step | Outcome |
|---|---|
| README Build | Failed first: `mise run build` refuses an untrusted `mise.toml` in a fresh checkout. Passed after `mise trust` (D1). |
| README Configure | The copy is mode 600 as documented, but untracked and not ignored (D2). |
| README flags | Every command and flag the page names exists. |
| Start the fixture | `up`, `bw migrate`, `inject start A/B/C`, `seed`, `/livez` 204: as documented. |
| Walkthrough S0-S4 | Recorded, imported, adopted, published, planned, approved and applied as documented; S4's timeline showed the commitment, the attempt, the response, the classification and the applied change. S2's review block did not show the renderer the prose names (D3). |
| Walkthrough S5 | Drift opened on an out-of-band patch; adoption and the revert each ended with `openDrift` null. |
| Recovery: interrupted operations | Read, take over, account and cancel worked once the generation and the observations were read across every page (R-D1, R-D2). |
| Recovery: restoring, steps 1-6 | All six steps as documented: `409 recovery-mode-active` before the entry; the entry named the restored operation; the restored automation token was refused until rotated; scope accounting completed the restored operation; marks `ready`, releases `released`, exit returned both scopes to `normal`. |

Recovery details (12:00-12:14 UTC):

- **Cancel case** (C killed after its commitment, B cut from the worker): `attempts` was `null`
  (R-D3); a 9-character key got `400` naming the key rule; `{"generation": 1}` got `409`, since a
  start-time takeover had moved the generation to 2 (R-D2); the resolution to `cancelled` got `201`.
- **Send case** (C stalled after its attempt, the worker cut off): the current generation (2) took
  it over (`201`, generation 3). Accounting with the first read after the transport deadline and a
  recovery read 30 s later got `201`, and the operation completed.
- **Restore** to a snapshot holding that operation unresolved: stop all, restore PostgreSQL and
  OpenBao, recovery start, entry (`201`, one operation), seed token `401`, rotate (`200`), normal
  restart, account both machines (`201`: the worker `unresolved`, then completed; the control plane
  `blocked`), mark, release, exit (`201`). The worker ended with Desired equal to Applied and no
  open drift; the rotated token still worked after the exit.

## 3. Divergences and fixes

| ID | Where | Observed | Fix |
|---|---|---|---|
| D1 | README Build | `mise ERROR Config files ... are not trusted`, rc 1, from mise 2026.5.18 on this host; other mise versions or trust settings may not refuse | `mise trust` line in the build block (this change) |
| D2 | README Configure | `bronzeward.yaml`, holding the DSN password, shows as untracked; `up` counts it as a changed path | `/bronzeward.yaml` in `.gitignore` (this change) |
| D3 | Walkthrough S2 review | `jq .sources` omits the renderer the prose names | `jq '{sources, renderer}'` (this change) |
| R-D1 | Recovery, timeline and observation reads | The timeline is oldest first. The worker's held 605 entries; page 1 (`limit=500`) ended at revision 500 with `next` set and held none of the interrupted operation's entries. Observations are listed by id, not by time (`internal/api/observations.go`), so the first qualifying read is found only over every page, by `basis` | Follow `next` with `&cursor=`; sort observations by `basis` (fixed in the recovery pull request) |
| R-D2 | Recovery, take over | The literal generation 1 is stale after a start-time takeover | Read the generation from the last `commitment` or `ownership-transition` entry (fixed there) |
| R-D3 | Recovery, read | `attempts` is `null` before the first attempt | Stated (fixed there) |

The walkthrough's S5 note, which said the step was unverified until a reviewer ran it, now records
this run.

## 4. Limitations

- The recovery page ran at its pull request's head before the R-D1-R-D3 fixes; the fixed text was
  not run again. The fixes change how values are read, not the acts.
- The observation list stayed on one page (272 entries) in this run, so paging was observed only
  for the timeline. The run's picks scanned that whole page by the prose rules; the accounting
  calls they fed returned `201`.
- Not run: the limits page, the troubleshooting page's commands, and a credential-store restore.
- One fixture, one host, one run: no timing claim beyond what each step returned.
- Three of the reviewer's own wait loops timed out on a quoting error in its driver; the reads
  after each confirmed the documented state (S4's timeline; S5's release, source and `openDrift`).
