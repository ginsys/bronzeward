# E4 approval and dispatch-safety experiment

**This is Phase-0 evidence code for [issue 7](https://github.com/ginsys/bronzeward/issues/7). It is
not the v1 implementation and it will not be maintained past the
[research report](../../docs/design/research/20260925-dispatch-safety.md) it produces.** It
selects no executor design, schema or Talos client library.

## What it is for

The [execution and recovery specification](../../docs/spec/execution-recovery.md) puts one
database transaction between approval and dispatch: the **dispatch commitment boundary** (§3.2).
Its approval, scope and evidence comparisons happen inside it. The attempt transaction (§3.3)
repeats them before each send and adds the owner, generation and state comparisons. Recovery (§4)
resolves an operation whose attempts have been accounted for. `e4x`, the executor here, implements that contract against the
fixture's PostgreSQL and applies a real machine configuration to the fixture's live Talos worker
with `talosctl`. The boundary is the `COMMIT` in `Store.Commit` ([`store.go`](store.go)). An
attempt is recorded by `Store.Attempt` before anything is sent.

| Group | Rows |
|---|---|
| revocation | none; before the commitment; during it; between the commitment and the attempt; after the attempt (the §8.1 residual); a control that checks the approval once, early |
| ownership | taken over before the attempt; a control with no owner, generation or state comparison; a second executor for the same plan |
| scope | a second plan for the machine while the first is in flight, and after it; a control without the machine-scope index (§8.2's stale A after B) |
| fault | a partition or pause of the worker during the send, through the control plane and directly; PostgreSQL killed before the response is recorded; a partition during verification; accounting on the executor's exit alone |
| kill | the executor SIGKILLed at the evidence, committed, response and complete gates |
| rejected | a configuration the worker's own validation refuses |
| tests | the prototype's `go test` against the fixture's PostgreSQL |

Each of the three controls removes one mechanism, `-mode naive`, `-mode nofence` or `-no-scope`,
and must let the failure that mechanism prevents through.

## How a row is judged

`run/all` writes each row's expectation before running it. Every executor is a separate `e4x`
process, and the harness holds it at a named gate (`E4X_GATES`) to place a revocation, a takeover,
a fault or a kill at an exact point. The outcome is read by readers independent of `e4x`:

- `psql` inside the fixture's PostgreSQL container, with the queries in [`readers/`](readers/);
- the harness's own `talosctl` reads of the worker: the configuration's digest, the resource's
  version and last-change time, and machined's count of accepted and refused applies.

The fixture's `injections.log` is the timeline of every fault. `run/lib.sh` compares the result and
records one line per row in `cells.tsv`. It keeps every executor's log, every injection and every
reader result as the row's transcript. A mismatch is recorded, not retried.

**Digest normalization.** A digest is the SHA-256 of
`talosctl get machineconfig v1alpha1 -o jsonpath='{.spec}'`, with its trailing newlines replaced
by exactly one. That is the fixture's own normalization (`fixtures/bin/selftest`): the jsonpath
output carries one newline more than the file that was sent. `e4x` normalizes both sides. The
harness hashes the artifact file as written, which `talosctl machineconfig patch` ends with one
newline; a difference would fail closed, as a mismatch.

## Running it

A capture needs a fresh fixture, and `E4D_OUT` must name a new, empty absolute path on disk,
outside this checkout:

```sh
fixtures/bin/up
E4D_OUT=<somewhere with ~50 MiB> experiments/e4-dispatch-safety/run/all
fixtures/bin/evidence <the same directory>
cp -a fixtures/.state/evidence/<bundle> <the same directory>/bundle
E4D_OUT=<the same directory> experiments/e4-dispatch-safety/run/collect-evidence
fixtures/bin/down
```

`run/all` builds `e4x` and runs the matrix in a few minutes, most of them the fault rows'
transport deadlines and settle time (`run.txt` records `started` and `finished`). `run/collect-evidence` copies the text evidence into
[`evidence/`](evidence/) and packs the transcripts one file per group. It rewrites local paths to
placeholders, drops trailing whitespace from the summary files (`talosctl version` ends its lines
with a space), and refuses anything matching the fixture's leak-scan patterns or a path in the
operator's home. The machine configurations the rows applied hold the fixture's synthetic Talos
secrets, and they stay in `E4D_OUT` with the bundle. The committed manifest gives every copied
bundle file its SHA-256, except the PostgreSQL data directory: that gets a single line, the digest
of its part of the full manifest.

[`evidence-capture1/`](evidence-capture1/) holds an earlier complete capture from the same measured
inputs, collected with `E4D_EVIDENCE=<this directory>/evidence-capture1`; the report says why it is
kept.

The unit tests need no fixture for the Talos parsing. The store tests skip unless
`E4X_TEST_PG_DSN=<dsn>` names a PostgreSQL database they may reset.

`e4x` reads its secrets and endpoints from the environment, never argv: `E4X_PG_DSN`,
`E4X_TALOSCTL`, `E4X_ENDPOINT`, `E4X_NODE`, `E4X_GATES`, plus `E4X_OWNER` for the executor's
identity. Its exit status is 0 completed, 3 refused, 4 unresolved, or 5 another terminal state
(rejected, failed, cancelled). See [`main.go`](main.go).

## What it deliberately does not do

- **One worker, no reboots.** Every artifact changes only a node label, which applies without a
  reboot. It does not test staged or reboot-requiring applies, maintenance mode or a control-plane
  target.
- **No exactly-once claim.** An attempt already recorded can be sent after a revocation. An
  abandoned request can land after its executor gave up. The rows measure both.
- **Accounting is a harness action.** `e4x account` stands for a human or an external system
  attesting that an executor can no longer send. The prototype does not decide when that is true.
- **Not the Bronzeward schema or executor.** The schema is the smallest that carries the
  specification's comparisons. The database-semantics questions are
  [issue 6](https://github.com/ginsys/bronzeward/issues/6).
