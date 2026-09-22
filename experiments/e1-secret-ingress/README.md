# E1 secret-ingress experiment

**This is Phase-0 evidence code for [issue 2](https://github.com/ginsys/bronzeward/issues/2). It is
not the v1 implementation and it will not be maintained past the research report it produces.**

The [design](../../docs/design/Talos_Configuration_and_Machine_Management_Design.md#181-phase-0---evidence-before-implementation-contracts)
permits it in as many words: "Narrow prototypes are allowed to obtain that evidence."

## What it is for

Design §7.1 requires that known or operator-marked secrets be extracted **before any ordinary
plaintext persistence**, across import, drift adoption, draft updates, original YAML text, parsed
indexes, request logs, error reports and staging — and forbids retaining an observed configuration
in a plaintext draft to redact later, because backups and history would already hold it.

This program exists to make that claim testable against the
[investigation fixtures](../../fixtures/README.md), and to produce the evidence a research report
can cite. It answers a feasibility question. It does not establish how v1 will be built.

## Why its own module

`experiments/e1-secret-ingress` is a separate Go module on purpose. When an implementation module
appears at the repository root, its `./...` will not reach this code, so disposable evidence code
cannot be swept into the implementation by accident. `mise run go` names each experiment module
explicitly for the same reason.

## Running the matrix

The fixtures must be up first (`fixtures/bin/up`), and `E1_OUT` must name an absolute path on disk,
outside this checkout, with room for the bundles:

```sh
E1_OUT=<somewhere with a few hundred MiB> experiments/e1-secret-ingress/run/screen
E1_OUT=<the same directory>               experiments/e1-secret-ingress/run/matrix
E1_OUT=<the same directory>               experiments/e1-secret-ingress/run/schema
E1_OUT=<the same directory>               experiments/e1-secret-ingress/run/collect-evidence
```

`E1_OUT` has no default on purpose. Every bundle carries a PostgreSQL data directory, so a full
matrix runs to hundreds of megabytes; a default under `/tmp` would put that in RAM on most systems,
and one inside the checkout would be destroyed by `fixtures/bin/down` along with the state it is
evidence about. Deleting it afterwards is part of finishing the run — nothing sweeps it.

- `run/screen` is Tier A: every combination of flow, staging mode, outcome and crash point, with no
  bundle captured. Its only authority is finding candidates. A passing row says the run root and the
  live tables were clean at that moment and says nothing about the heap, the write-ahead log or the
  backups.
  Pass `--marks <file>` or `--mark-suffix <list>` to re-run it against a different mark source; the
  table is named for the source so the two can be compared. Pass `--reset-tables` to empty the
  prototype's tables first, which is required after a matrix has left the deliberate controls'
  plaintext in the live rows, and must never be passed while a matrix or a capture is in flight.
- `run/matrix` is Tier B: the runs that get a `fixtures/bin/evidence` bundle, each asserted by
  `run/assert-bundle`. `run/capture` takes one of them on its own.
- `run/schema` measures detection against a ground truth built from the fixture's own rule over the
  secrets bundle, and plants a secret where no rule looks to show what detection misses and the
  operator mark catches. It writes the values it works from into `E1_OUT`; those files hold real
  synthetic secrets and are never committed.
- `run/collect-evidence` copies the committable text into `evidence/`, rewriting absolute paths and
  refusing to leave anything the fixture's own patterns match. Run it last, with the fixture still
  up: it re-reads the control plane and the injection log.

Every honest bundle's `leak-scan.txt` must hold **exactly two lines**: the fixture's own positive
control and the reachability control each run plants in its run root. The scan records hits only, so
a directory that scanned clean is identical in the report to one it never walked — fewer than two
lines means the run root went unscanned and the bundle proves nothing about it. More than two is a
leak, and which secret it was is settled by opening the file named, never by the count: the
fixture's synthetic values share a prefix.

A control that comes back clean means that surface is not evidence. Say which one and change the
design before drawing a conclusion from it, not after.

One database serves the whole matrix, and the plaintext a `--persist-first` control commits stays
readable in the heap, the write-ahead log, the dump and the snapshots for every run after it. So
`run/matrix` takes every bundle that must scan clean before the first control, and the run that
closes the §9.1 machine-state bracket at the end is asserted as `residue` instead: the run is
honest, the database is not, and a clean scan there would mean the controls' plaintext had gone
away on its own. Re-running the matrix therefore needs a fresh fixture (`fixtures/bin/down` then
`fixtures/bin/up`), not just a fresh `E1_OUT`.

## What it deliberately does not do

No upstream Talos composition, merge or typed validation. No reference resolution. No release,
plan, approval or dispatch. No SQLite, no age/SOPS, no rotation or retention. No migration
framework, no concurrency, no server.

It selects nothing: not the marked-secret syntax, not a secret or encryption provider, not a
database driver, not a deployment profile. Those decisions belong to their own work items and are
made from evidence, not from what this prototype happened to import.
