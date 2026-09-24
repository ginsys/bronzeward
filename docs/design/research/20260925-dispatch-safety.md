# Approval and dispatch safety: the commitment boundary against a live Talos worker

| | |
|---|---|
| **Date** | 25 September 2026 |
| **Work item** | [Experiment E4 - approval and dispatch safety](https://github.com/ginsys/bronzeward/issues/7) |
| **Design reference** | [§12.5 Durable operations and uncertain outcomes](../Talos_Configuration_and_Machine_Management_Design.md#125-durable-operations-and-uncertain-outcomes), [§12.7 Application approval and dispatch boundary](../Talos_Configuration_and_Machine_Management_Design.md#127-application-approval-and-dispatch-boundary), [§18.1 Phase 0](../Talos_Configuration_and_Machine_Management_Design.md#181-phase-0---evidence-before-implementation-contracts); specification [execution-recovery §3](../../spec/execution-recovery.md#3-dispatch-commitment), [§4](../../spec/execution-recovery.md#4-operation-timeline-and-states), [§5](../../spec/execution-recovery.md#5-interruption-and-retry-classification), [§8](../../spec/execution-recovery.md#8-invariants-and-worked-interleavings) |
| **Artifacts** | [`experiments/e4-dispatch-safety/`](../../../experiments/e4-dispatch-safety/README.md), evidence under [`experiments/e4-dispatch-safety/evidence/`](../../../experiments/e4-dispatch-safety/evidence/) |
| **Decision enabled** | Whether the specification's commitment and attempt transactions, with a generation-fenced owner and a machine-scope index, keep revoked, stale and conflicting work from reaching a Talos node, what they cannot prevent, and which evidence accounts for an attempt. This is input to the [execution and recovery contracts](https://github.com/ginsys/bronzeward/issues/19). No executor design, schema or Talos client library is selected here. |

## 1. Question

The specification puts one database transaction between approval and dispatch, the *dispatch
commitment boundary* (§3.2), and an attempt transaction before every send (§3.3). It leaves open
what only an experiment can show (§9):
- the ownership mechanism;
- that every executor sends only after its attempt transaction commits;
- which Talos responses prove that nothing was mutated;
- when a retry is safe.

Issue 7 asks for three things:
1. the boundary defined and tested, with revocation prevented before it;
2. uncertain sends, stale ownership and an old A executor after a newer B, each either shown safe
   or stopped as unresolved;
3. per-operation, per-mode and per-assignment evidence for completion, retry and stopping, with no
   claim of exactly-once or universal retry.

Each outcome is judged from what independent readers find afterwards: the database, the worker's
configuration and the fixture's injection timeline. What the executor reports about itself does
not decide it.

Out of scope, and owned elsewhere: the database semantics themselves on each backend
([issue 6](https://github.com/ginsys/bronzeward/issues/6)); restoration of management state
(specification §7); Talos API compatibility across versions
([issue 5](https://github.com/ginsys/bronzeward/issues/5)).

## 2. What was built

### 2.1 The executor and its boundary

`e4x` ([`main.go`](../../../experiments/e4-dispatch-safety/main.go),
[`store.go`](../../../experiments/e4-dispatch-safety/store.go)) is one process per executor. Its
identity comes from `E4X_OWNER`. It keeps the operation in PostgreSQL through `lib/pq`, and it
reaches the worker with the pinned `talosctl`, both to read the configuration back and to send
`apply-config --mode=no-reboot --file <artifact>`. By default it goes through the control plane's
endpoint with `--nodes <worker>`; one row sends to the worker's own endpoint instead. `e4x run`
takes these steps:

1. It checks at use time that the artifact file still has the bound digest.
2. It records the §3.1 evidence observation: a read-back of the worker's configuration.
3. It runs the **commitment transaction**, `Store.Commit` (`store.go:325`).
   - Comparison 1 (approval) reads the approval `FOR SHARE`, so a revocation must wait for the
     transaction or precede it.
   - Comparisons 2 and 6 cover the machine's assignment and baseline revisions and the scope gate.
   - Comparison 3: the evidence must be this operation's, inside its maximum age, uncontradicted,
     and at the bound pre-dispatch digest.
   - Comparison 4 is a partial unique index, `operations_one_uncertain`: one operation per
     machine in `committed`, `sending`, `verifying` or `unresolved`. At the PoC rollout limit of
     one, that index also stands for comparison 5.
4. It runs the **attempt transaction**, `Store.Attempt`. It repeats comparisons 1–3 and 6, checks
   the owner and its generation under `FOR UPDATE`, bounds the attempts, and on a retry refuses if
   anything was recorded after the classification it is bound to. Only then does it record the
   attempt and its verification deadline.
5. It sends, with a transport deadline no later than that verification deadline.
6. It records the response. An acceptance moves the operation to `verifying`. A definitive
   rejection moves it to `rejected`. A timeout or a transport error moves it to `unresolved`.
7. It polls completion observations until the verification deadline and completes on a matching
   digest.

**The dispatch commitment boundary is the `COMMIT` of `Store.Commit`'s transaction**, issued by
`inTx` (`store.go:139`). Every row in §4.1 places its revocation relative to that point.

`e4x takeover` moves ownership to a new executor and bumps the generation. `e4x account` records
that an attempt is accounted for, on stated evidence. `e4x recover` is the new owner's
resolution: it refuses while any attempt is unaccounted, takes a completion observation, and then
completes, fails, or, with `-retry`, classifies a safe retry and runs a new attempt transaction.
The schema is the smallest that carries the specification's comparisons; it is not the Bronzeward
schema.

### 2.2 Gates and rows

An executor started with `E4X_GATES=<dir>` stops at any named gate the harness has armed:
`evidence`, `commit` (inside the commitment transaction, after every comparison), `committed`,
`attempt` (inside the attempt transaction), `send`, `response`, `verify` and `complete`. It writes
`<gate>.reached` and waits for `<gate>.go`
([`gate.go`](../../../experiments/e4-dispatch-safety/gate.go)). That is the interleaving control:
it places a revocation, a takeover, a second executor, a fault or a `SIGKILL` at an exact point,
with no sleeps between the executor's steps.

A *row* is one case. [`run/all`](../../../experiments/e4-dispatch-safety/run/all) writes the row's
expectation before running it. [`run/lib.sh`](../../../experiments/e4-dispatch-safety/run/lib.sh)
then compares the expectation with the readers' results and each process's exit status, and adds
one line to [`cells.tsv`](../../../experiments/e4-dispatch-safety/evidence/cells.tsv). A mismatch is
recorded, not retried. Every row starts from an empty schema and one machine whose `Applied`
digest is the worker's current configuration. Each artifact is that configuration with one node
label (`bronzeward.test/e4x=<row>-<artifact>`) added, so every row's artifact differs from every
earlier one and from the pre-dispatch digest. The worker applies it without a reboot.

`=` and `~` expectations are asserted. A `seen:` expectation is recorded, not judged, though the
row fails if it is missing. It is used where a value is timing-dependent and the row's finding
is its distribution, not one value.

### 2.3 Independent readers

- **Database:** `psql` inside the fixture's PostgreSQL container, with
  [`readers/op.sql`](../../../experiments/e4-dispatch-safety/readers/op.sql). It returns the
  operation's state, its owner and generation, and every attempt with its owner. It also reports
  whether any attempt was recorded by an executor that had already lost ownership
  (`stale_attempts`), every response, how many attempts are accounted for, and where a revocation
  fell relative to the commitment and the first attempt, by timeline revision.
- **Worker:** the harness's own `talosctl` reads, outside `e4x`. They give the configuration's
  digest, named `pre` or after the row's artifact it matches; the machine-configuration resource's
  version, which moves on every landed apply; its last-change time; and
  machined's counts of `ApplyConfiguration` calls answered `OK` and `InvalidArgument`, as deltas
  since the row began (`worker.landed`, `worker.ok`, `worker.invalid`).
- **Timeline:** the fixture's `injections.log`, with a `begin` line and a `done`, `failed rc=N` or
  `unknown rc=N` line for every fault.

**Digest normalization.** A digest is the SHA-256 of
`talosctl get machineconfig v1alpha1 -o jsonpath='{.spec}'` with its trailing newlines replaced by
exactly one. This is the fixture's own normalization (`fixtures/bin/selftest`): the jsonpath
output carries one newline more than the file that was sent. `e4x` normalizes both sides, in Go
(`normalizeReadBack`, `talos.go:37`). The harness normalizes only the read-back (`w_digest`,
`run/lib.sh`) and hashes the artifact file as written. That is exact only because
`talosctl machineconfig patch` ends the file with one newline. A difference would fail closed:
the read-back would match no artifact (`is=other`), and the row would be a mismatch. None was.

### 2.4 Positive controls

Three rows each remove one mechanism, and each must let through the failure that mechanism
prevents:

| Control | Mechanism removed | Failure it must show |
|---|---|---|
| `-mode naive` | the approval comparison inside both transactions; the approval is checked once, before the evidence | a revocation after that check is not seen, and the artifact is sent |
| `-mode nofence` | the owner, generation and state comparisons in the attempt transaction, which are where a takeover is seen | an executor that lost ownership records an attempt and sends |
| `-no-scope` | the `operations_one_uncertain` index | a newer plan B commits and completes while A's attempt is held before its send; A's send then lands and overwrites B |

## 3. Reproduction and identities

```sh
fixtures/bin/up
E4D_OUT=<a new, empty directory outside the checkout> experiments/e4-dispatch-safety/run/all
fixtures/bin/evidence <the same directory>
cp -a fixtures/.state/evidence/<bundle> <the same directory>/bundle
E4D_OUT=<the same directory> experiments/e4-dispatch-safety/run/collect-evidence
fixtures/bin/down
```

`run/all` refuses to run without the fixture. It records in
[`run.txt`](../../../experiments/e4-dispatch-safety/evidence/run.txt) the commit it ran at, every
uncommitted input, the Go, `talosctl`, Talos, PostgreSQL and module versions, the server's default
isolation and the settle time. `run/collect-evidence` refuses a partial run. It packs the
transcripts one file per group under
[`evidence/transcripts/`](../../../experiments/e4-dispatch-safety/evidence/transcripts/): each
row's section begins with a `==> <n>-<group>-<row> <==` line. The row number used in this report
is the `n` column of `cells.tsv`. The bundle stays outside the repository: it holds the fixture's
state, and the applied artifacts hold the fixture's synthetic Talos secrets. What is committed is
a summary of the bundle and a manifest of it.

Two complete captures were taken, from the same measured inputs:

| | Capture 2 (primary) | Capture 1 |
|---|---|---|
| Evidence | [`evidence/`](../../../experiments/e4-dispatch-safety/evidence/) | [`evidence-capture1/`](../../../experiments/e4-dispatch-safety/evidence-capture1/) |
| Commit | `1f28961`, 0 uncommitted inputs | `f2c7c86`, 0 uncommitted inputs |
| Run (UTC, 24 September 2026) | 23:22:11 to 23:26:14 | 23:06:50 to 23:10:53 |
| Rows | 23, 0 mismatches | 23, 0 mismatches |
| Leak scan | 16 patterns; the control fired; 0 hits in 12 files | the same |

Both ran against Talos v1.13.6 (`talosctl` and the worker) and PostgreSQL 17.11
(`docker.io/library/postgres:17-alpine@sha256:f02121de6f74d30d8a94cd1d9584125e2178d7e6c377d8130112d4e52d867995`).
The server's default isolation was `read committed`, the driver `lib/pq` v1.10.9, Go 1.27.1, and
the settle time 30 s. In both, the bundle's own scan matched only its planted control, its pattern
list and the applied artifacts in `E4D_OUT`, none of which is committed.

`f2c7c86` is not on this branch. It was rewritten as `1f28961` to drop a build output committed by
mistake. The rewrite also changed the README, one `mise.toml` build line, and the collector, which
now drops trailing whitespace. Later commits let the collector write to `E4D_EVIDENCE`, tightened
its destination guard and moved its leak-scan control out of the checkout. `run/all`,
`run/lib.sh`, the Go sources, the readers and the fixtures are byte-identical between the two.
Capture 1's `run.txt` therefore names a commit that cannot be fetched. It is kept because it
holds the one captured late landing (§4.4). Its evidence was re-collected with the current
collector.

## 4. Results

Row numbers are the `n` column of `cells.tsv`. Times are UTC on 24 September 2026, from capture 2
unless stated. Executor log lines carry the host clock to the nanosecond, and `injections.log` has
whole seconds. The worker's resource `updated` time is in whole seconds, on the worker's clock.
The two captures agree on every asserted cell. They differ only in row 012's recorded (`seen:`)
cells, which §4.4 reports.

### 4.1 Revocation around the commitment boundary (criterion 1)

| Row | Revocation placed | Operation | Attempts | Worker |
|---|---|---|---|---|
| 001 | none | `completed` | 1, accepted | artifact, 1 landing |
| 002 | after the evidence, before the commitment transaction began | `cancelled`; X refused on comparison 1 | 0 | `pre`, no landing |
| 003 | while the commitment transaction held the approval `FOR SHARE` | `cancelled`; the revoker waited, and X's attempt was refused on comparison 1 | 0 | `pre`, no landing |
| 004 | between the commitment and the attempt | `cancelled`; attempt refused on comparison 1 | 0 | `pre`, no landing |
| 005 | after the attempt was recorded, before the send | `completed`; the attempt was sent anyway | 1 | artifact, 1 landing |
| 006 | control `-mode naive`: after its single early check | `completed`; the revocation was not seen | 1 | artifact, 1 landing |

- **Row 003 places the revocation after the boundary.** From 23:22:14.893, X was held at the
  `commit` gate inside its commitment transaction. A revocation started while X was held there
  waited on the `FOR SHARE` lock. The harness found it still running before releasing X
  (`revoker_waited=yes`). X committed at 23:22:16.910, and the revocation's timeline entry followed
  at 23:22:16.913; the reader places it `after-commit`, by timeline revision. X's attempt
  transaction then failed comparison 1 at 23:22:16.936. No attempt had been recorded, so the
  operation went `committed` → `unresolved` → `cancelled`, as specification §8.1 describes.
  Capture 1 gave the same order: commit at .311, revocation at .314. The row does not show the
  lock itself. X writes its `committed` timeline entry before the gate, so any later revocation
  reads `after-commit`. The attempt transaction's comparison 1 would also refuse X without
  `FOR SHARE`. The evidence that the revocation waited is `revoker_waited=yes`: the harness
  found it still running 2 s after it started. The 3 ms log order is further evidence.
- **Rows 002 and 004** show the two sides of the boundary without a race. Before it, nothing is
  committed; after it, nothing is attempted.
- **Row 005 is the residual that specification §3.3 states, measured.** The attempt was recorded at
  23:22:18.186, and the revocation followed at 23:22:18.244 while X was held at the `send` gate. X
  sent at 23:22:18.246, and the configuration landed. The revocation stopped nothing that was
  already recorded.
- **Control row 006:** the comparison inside the transactions is removed. A revocation at
  23:22:18.939, after the early check and before the commitment at 23:22:18.947, did not stop the
  dispatch.

### 4.2 Ownership loss and a second executor (criterion 2)

- **Row 007:** X was taken over between its commitment and its attempt, with Y at generation 2.
  X's attempt transaction refused on comparison 7 (owner). X exited with 3, having sent nothing.
  Y recovered with `-retry`. With no attempts recorded, it took a completion observation at `pre`,
  classified a retry and made one attempt as `Y@2`. `stale_attempts=0`, and one landing.
- **Row 008, control `-mode nofence`:** the same interleaving without the owner, generation and
  state comparisons. X recorded attempt 1 as `X@1` after the takeover (`stale_attempts=1`) and
  sent it, and the configuration landed. The operation was left `sending`, owned by Y, with an attempt Y did not
  make. Those comparisons are what kept X from sending in row 007, but the rows do not separate
  them. A takeover both raises the generation and moves in-flight work from `committed` to
  `unresolved` (`Takeover`, `store.go`), and either comparison alone would have refused X. Row
  007's refusal names the owner only because that check runs first.
- **Row 009:** a second executor, Z, ran the same plan while X was held inside its commitment
  transaction. Z waited about two seconds, until X committed. It was then refused on comparison 0
  ("an operation for plan A exists", exit 3), and X completed. One attempt, one landing.

### 4.3 The machine scope and A after B (criterion 2)

- **Row 010:** while A was held at `send`, B for the same machine was refused on comparison 4 (the
  scope index). After A completed, B's plan was refused on comparison 2: A's completion had moved
  the machine's baseline revision. A plan made afresh, B2, then completed. The worker ends at B2,
  after two landings.
- **Row 011, control `-no-scope`:** the index is removed. A committed at 23:22:25.089 and was held
  at `send`. B then committed at 23:22:25.192 and completed at 23:22:25.266. The harness read B on
  the worker (`after-b.is=b`, resource version 10). A was then released: it sent at 23:22:25.375
  and completed, and the worker ends at A (version 11). **A stale A overwrote the newer B, and
  both operations recorded `completed`.** With the index, the same interleaving is row 010.

The contract's A-after-B protection is therefore not a Talos property. It is comparison 4 plus
the rule that an unresolved operation keeps the scope. Rows 012, 014 and 015 below leave
operations `unresolved` for 30 s or more (row 013 for 11 s), and throughout that time the index
would refuse a newer plan.

### 4.4 Uncertain sends, reconnects and accounting (criteria 2 and 3)

Rows 012–015 held the executor at `send`, injected the fault, then released the send. Row 016 held
it at `response`, and row 017 at `verify`. The transport deadline was 15 s. Rows 012–015 then
waited a settle time before accounting. Row 013 is the exception: it accounts the moment X exits.

| Row | Fault (capture 2, `injections.log`) | X's result | Landed after X gave up | Resolution |
|---|---|---|---|---|
| 012 | `netsplit worker` 23:22:26 → `netjoin` 23:22:41; the request goes through the control plane | `unknown` at the transport deadline, 23:22:41.566 | no, through the 30 s settle (resource version 11 unchanged) | Y refused while the attempt was unaccounted (exit 4). Once accounted, Y observed `pre` and classified a retry; attempt 2 was accepted; `completed`, one landing |
| 013 | as 012 (23:23:12 → 23:23:28) | `unknown` at 23:23:27.999 | no, through the 30 s settle after classification | accounted on X's exit alone, at 23:23:28.028. Y's first readable observation (23:23:39, 11 s after the heal) showed `pre`: **`failed`** (Y exit 5) |
| 014 | `pause worker` 23:24:10 → `unpause` 23:24:30; through the control plane | `unknown` at 23:24:25.348 | no | accounted 30 s after the unpause; observation `pre`; retry classified; attempt 2 accepted; `completed`, one landing |
| 015 | `netsplit worker` 23:25:01 → `netjoin` 23:25:16; `talosctl` straight to the worker's endpoint | `talosctl` exited after 14.3 s with `Unavailable` ("Error while dialing"), classed `unknown` | no | as 014: attempt 2, `completed`, one landing |
| 016 | `kill postgres` 23:25:47 → `start postgres` 23:25:52–54, after the response and before it was recorded | accepted at 23:25:47.139; recording retried once a second, and succeeded at 23:25:54.553 | yes: the send itself | `completed`, 1 attempt |
| 017 | `netsplit worker` 23:25:55 → `netjoin` 23:26:03, after the response was recorded | accepted at 23:25:55.235 | yes: the send itself | the completion read begun during the partition was killed at 23:26:05.555; the next, at 23:26:08.706, matched; `completed` |

**Capture 1's row 012 landed late.** The fault was the same: `netsplit` 23:07:04 → `netjoin`
23:07:20, through the control plane. X's `talosctl` was killed at its transport deadline,
23:07:19.87, and the partition healed at 23:07:20. The configuration then landed. The
machine-configuration resource went from version 11 to 12 with `updated` 23:07:31 (the worker is
a container on the same host and shares its clock; `updated` has whole-second resolution), and
machined counted one more `ApplyConfiguration` answered OK. Y observed the artifact after the settle and
completed the operation on its first attempt, with no retry. Capture 1's rows 013–017 had the
same outcomes as capture 2's. Its row 013 also saw `pre` 11 s after the heal and classified the
operation `failed`.

What these rows show:

- **An executor's exit does not account for its attempt.** A request sent through the control
  plane's API proxy outlived the client that sent it, and landed about 11 s after the partition
  healed. That happened once in the four control-plane partitions over the two captures (rows 012
  and 013 of each). A development probe, not committed, saw one more (§5). The eight uncertain
  sends in rows 012–015 landed late only that once.
- **Accounting on exit alone gave a wrong basis for a terminal state.** Row 013 accounts the
  moment X exits, and both captures classified it `failed` 11 s after the heal. That is when
  capture 1's row 012 request landed. Nothing landed in either row 013, so `failed` happened to
  match the machine. The evidence behind it could not have told the two cases apart. Neither
  capture explains why one request landed and the other three did not.
- **Recovery did the right thing under the contract.** Whenever an attempt was unaccounted, Y
  refused to resolve (exit 4, `event=unaccounted`), and the scope stayed held. Once accounting was
  recorded, the completion observation decided the outcome:
  - a matching digest completed the operation without a retry (capture 1, row 012);
  - a `pre` digest led to one bounded retry, which the attempt transaction admitted (capture 2
    rows 012, 014 and 015, and capture 1 rows 014 and 015).

  Exactly one landing was counted wherever a retry ran.
- **A retry is safe only as long as the accounting is true.** Those retries were accounted after a
  30 s settle and a `pre` observation. Suppose a request abandoned like capture 1's row 012 landed
  after that observation. Landing on top of the retry, it would re-apply the same artifact: for
  `apply-config --mode=no-reboot`, that should change no configuration. An identical re-apply was
  not captured here (§5). But the
  retry's completion releases the scope. If a newer plan B then completed first, the late request
  would overwrite B. That is the stale A-after-B case of specification §8.2, reached through
  accounting that was recorded but not true.
- **A dial failure was not treated as proof of no send.** In capture 2's row 015, `talosctl`
  reported `Unavailable` while dialing the partitioned worker directly. The prototype classes
  every `Unavailable` as `unknown`. Whether a dial error proves that nothing was sent is not tested
  here.
- **Reconnects:** a database outage between the response and its recording (row 016) cost 7.4 s
  from the response, 7.0 s of it after the harness released the executor. The executor retried
  the recording once a second until the database accepted it. A partition during verification (row 017) cost one failed read. The operation completed within its
  verification deadline.

### 4.5 Executor kills at each gate (specification §5's interruption points)

| Row | Killed (SIGKILL, exit 137) at | State left | Resolution |
|---|---|---|---|
| 018 | `evidence`: before commitment | no operation | another executor, Y, ran the plan from the start (`Y@1`); `completed` |
| 019 | `committed`: after commitment, before any attempt | `committed`, 0 attempts | takeover; Y's `recover -retry` found no attempts, observed `pre` and retried; one attempt by `Y@2`; `completed` |
| 020 | `response`: accepted by Talos, not yet recorded | `sending`, response `none` | takeover; recovery refused while unaccounted (exit 4); accounted ("executor X killed after talosctl returned, and reaped"); the completion observation matched; `completed`, 1 attempt |
| 021 | `complete`: matching observation made, completion not recorded | `verifying`, response `accepted` | takeover; recover; new completion observation; `completed` |

Each kill left the state the specification names for that point. None needed a generic "mark
successful". Row 020 completed only after its attempt was accounted for and a new completion
observation was taken.

### 4.6 A definitive rejection (row 022)

An artifact whose `machine.type` is `bogus` was answered `InvalidArgument` in 22 ms (24 ms in
capture 1). The operation went to `rejected`. The worker's digest stayed `pre` and its resource
version stayed at 20, while machined counted one more `InvalidArgument`. For this operation and
mode, the `InvalidArgument` from `apply-config` arrived before any mutation. That rests on one
input class, a validation error, and is not a claim about other error codes.

### 4.7 The prototype's tests (row 023)

`go test -count=1 -p 1 -v ./...` ran against the fixture's PostgreSQL: 11 tests passed, none
skipped, in each capture.
- `TestRetryRefusedAfterNewerTimelineFact` shows that a late acceptance recorded after a retry
  classification makes the retry's attempt transaction refuse (comparison 8).
- `TestEvidenceMayShowArtifactOnlyOnRetry` shows that only a retry may proceed on evidence showing
  the artifact (specification §3.2 item 3).

## 5. Failures hit while building it

- **Two harness bugs, both found in development runs and fixed before capture 1.**
  - A reader's label argument was ignored, so the kill rows' `killed.*` keys read as unset.
  - Row 013 recovered before the worker was readable after the heal, so its completion read
    failed. It now waits up to 30 s for a readable worker and records the wait: 11 s in both
    captures.
- **Development probes, not committed.** Manual probes against the fixture, run before the
  harness, set two things that `run/lib.sh` comments on.
  - One probe saw an abandoned request land 11.6 s after its client gave up, and 6 s after the
    partition healed. That set the 30 s settle.
  - Another saw the resource version move on an identical re-apply, and not on a rejected one.
  Neither transcript is committed, so neither is evidence here. The first is a second late
  landing beside capture 1's row 012.
- **Test order.** The store's tests were written alongside it, and their first failing run was a
  compile failure, not a behavioural one. The one rule added after the development runs had a
  behavioural failing test first: only a retry may proceed on evidence showing the artifact's
  digest.
- **A build output was committed, then removed.** The repository's Go check built this module
  into its own directory, and the binary was committed with the prototype. The check now writes to
  `/dev/null`. The prototype commit was rewritten without the binary, and capture 2 was taken at
  the rewritten commit (§3).
- **An oversized manifest was avoided.** The committed bundle manifest follows the form used by
  the database-semantics experiment, where a PostgreSQL data directory, if present, is reduced to
  one digest line. There, 2000-odd per-file lines made a diff hunk the pull-request review could
  not read.

## 6. What this decides

### 6.1 Criterion 1: the boundary and pre-commit revocation

The dispatch commitment boundary is the `COMMIT` of `Store.Commit`.
- In row 003, a revocation racing the boundary finished after the commitment's `COMMIT` and
  prevented every attempt. That it waited on the `FOR SHARE` lock is inferred from
  `revoker_waited=yes` and the log order, not read from PostgreSQL's lock view (§4.1).
- Rows 002 and 004 show the same outcome from either side.
- The control row 006 shows the prevention failing without the in-transaction comparison.

A revocation after an attempt was recorded does not stop that attempt (row 005). This is the
specification's stated residual, not a defect.

### 6.2 Criterion 2: uncertain sends, stale ownership, A after B

- **Uncertain sends are safe under the contract, as long as accounting is true.** Every partition
  or pause at the send left the operation `unresolved`, with the scope held. Recovery refused
  while any attempt was unaccounted.
- **`e4x` sends only after its attempt transaction commits.** Its one call to
  `talosctl apply-config` (`main.go:268`) follows a successful `Store.Attempt` (`main.go:245`) on
  every path. Every row's log shows `event=attempt` before `event=send`. This establishes
  specification invariant 2 for this prototype only, not for any other executor.
- **Stale ownership is stopped by the takeover fence.** A takeover raises the generation and
  moves in-flight work to `unresolved`, and the attempt transaction compares both (row 007). The
  control row 008, without those comparisons, shows a stale executor sending. The rows do not
  show that either half alone suffices. The fence stops only what has not yet been sent: a
  database fence is not a Talos fence (§7).
- **A after B is demonstrably safe only because conflicting work is stopped.** Row 010 refuses B
  while A holds the scope, then refuses B's plan after A because the baseline moved. Row 011 shows
  A overwriting B without the scope index.

### 6.3 Criterion 3: per-operation, per-mode, per-assignment evidence

This covers one operation (`apply-config`), one mode (`no-reboot`) and one assignment revision
(the machine's first), on one worker.

| Outcome | Evidence that sufficed here |
|---|---|
| `completed` | every attempt accounted for, by a recorded response or a recorded accounting, then a later completion observation of the artifact's digest |
| `rejected` | a recorded `InvalidArgument` response to every attempt, with the worker's resource version unchanged |
| `failed` | every attempt accounted for, and a completion observation contradicting the artifact. Row 013 shows that the accounting has to be true, not merely recorded |
| safe to retry | every attempt accounted for; no accepted response; a completion observation at the pre-dispatch digest; a new attempt transaction bound to the classification's revision |
| stop (`unresolved`) | any attempt with neither a definitive response nor accounting (rows 012–015, 020; rows 012–015 recorded an `unknown` response). Also no successful completion read by the attempt deadline: in the code (`main.go:332`), but no row reaches it |
| stop (`cancelled`) | a revocation before the commitment, or after it with no attempt recorded |

**Not established:** what evidence proves that an abandoned request can no longer land. The
client's exit is not enough (capture 1, row 012). The 30 s settle before the retries is a choice,
not a bound: nothing here shows how long the control plane's proxy can hold a request.

Nothing here claims exactly-once delivery: row 005 sends after the revocation. Nothing here claims
retry is always safe: the retries were of one `no-reboot` apply, under one accounting basis.

## 7. Limits

- **One worker, two captures, one operation and mode.** Every artifact adds one node label. There
  is no reboot-requiring or staged apply, no control-plane target and no maintenance mode.
- **PostgreSQL only.** Specification §9 asks for this on each candidate profile. SQLite's locking
  differs ([database-semantics investigation](https://github.com/ginsys/bronzeward/issues/6)),
  and the commitment and attempt transactions were not run on it here.
- **A database fence is not a Talos fence.** The fence and the scope index stop only executors
  that consult the database. Nothing here stops a request already sent, or one sent by anything
  that bypasses the database, including direct `talosctl`.
- **The late landing is one observation in four captured partitions.** A development probe saw
  another (§5), but that one is not committed. Only one of the four captured control-plane
  partitions landed late, and the cause of the difference was not established. Candidates
  include the control plane's API proxy, gRPC connection reuse, and the partition's effect on
  each.
- **Accounting is a harness action.** `e4x account` stands for an operator or a supervisor
  attesting that an executor can no longer send. The prototype does not decide when that is true.
- **No restoration, recovery epoch, drift freeze or rollout limit above one.**
  - Comparison 6 and the epoch check exist in the code, but no row exercises them.
  - No row or test exercises plan expiry, the observation's maximum age, a newer contradicting
    observation, or the attempt bound (comparison 9).
  - `TestCommitRefusesStaleEvidence` tests a digest mismatch, not an observation's age.
  - Specification §9 also asks how pre-restoration executors are shown to be quiesced. That is not
    answered here.
- **The evidence bundle was captured after the rows, with no node partitioned.** The fixture notes
  capture during a partition as never exercised, and it was not attempted.
- **The kill rows do not record which process was signalled.** `ex_kill` signals the executor
  it finds as the `timeout` wrapper's child, and falls back to the wrapper if it finds none. The
  transcript names only a PID.
  - Two things show the executor died: its log ends at the gate, and no later effect of it was
    seen. Each row's starting resource version equals the previous row's final one.
  - Neither proves the process exited.
- **Mechanisms, not an implementation.** The schema, gates and executor are the smallest that carry
  the specification's comparisons.

## 8. Recommendation

- **Keep the specification's commitment and attempt transactions as written.** Each comparison
  that a control removed let through the failure it exists to prevent.
- **Take ownership fencing into the execution contract as the takeover fence.** A takeover
  raises the generation and moves in-flight work to `unresolved`. The attempt transaction
  compares both (rows 007 and 008). Record in the contract that it bounds only unsent work.
  - None of the alternatives was built or measured here:
    - An expiring lease bounds a stale owner in time, but it too cannot stop a request that has
      already been sent.
    - An advisory lock held for the executor's lifetime ties ownership to one database
      session. It is lost with the connection, not with the executor.
    - A compare-and-apply on the Talos side would fence the node itself. Whether `apply-config`
      offers any precondition on the current configuration was not checked.
- **Do not treat an executor's exit, a killed client or a transport timeout as accounting.**
  Until there is evidence of a Talos-side bound on a proxied request's lifetime, accounting for an
  attempt whose response was lost needs an operator decision. A supervisor time bound would need
  its own evidence first. That question belongs to the
  [execution and recovery contracts](https://github.com/ginsys/bronzeward/issues/19).
- **Keep the scope held for every unresolved operation.** It is the only thing that made A after B
  safe here.

## 9. Hand-off

- **To the [execution and recovery contracts](https://github.com/ginsys/bronzeward/issues/19):**
  - the boundary's location and behaviour (§6.1);
  - the takeover fence (generation and state) as the ownership mechanism for executors that use
    the database (§6.2);
  - the §6.3 table of evidence per outcome, for `apply-config`/`no-reboot`;
  - `InvalidArgument` as a pre-mutation rejection, for validation errors;
  - the open questions:
    - what accounts for an attempt whose request may still be held by the control plane's proxy;
    - whether a dial failure proves no send.
- **To profile selection:** the SQLite gap in §7. The commitment and attempt transactions were not
  run on SQLite.
