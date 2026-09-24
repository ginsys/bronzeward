# E4 database-semantics experiment

**This is Phase-0 evidence code for [issue 6](https://github.com/ginsys/bronzeward/issues/6). It is
not the v1 implementation and it will not be maintained past the
[research report](../../docs/design/research/20260924-database-semantics.md) it produces.** It
selects no driver, ORM, schema or migration tool.

## What it is for

[Design §7.2](../../docs/design/Talos_Configuration_and_Machine_Management_Design.md#72-relational-revision-model-and-backend-contract)
requires revision conflicts, all-or-nothing publication, unique operation intent, ownership
transitions, safe queue claims, migrations and restoration behaviour to be proved on each proposed
backend. This prototype exercises each of them on PostgreSQL 17 (the
[investigation fixture](../../fixtures/README.md)) and on SQLite (the engine the pure-Go driver
links), with one schema and every dialect difference an explicit, countable branch.

| Scenario | Semantic |
|---|---|
| S1 | stale revision rejection: compare-and-set on a fragment's revision |
| S2 | all-or-nothing publication, including client and server interruption and commit-unknown |
| S3 | unique operation intent: one active operation per machine scope, idempotent by key |
| S4 | ownership transitions: a generation-fenced takeover, and an attempt that must hold ownership |
| S5 | queue claims: fenced leases, eight workers draining 400 jobs |
| S6 | migrations: rollback on failure and on SIGKILL, concurrent runners |
| S7 | restored state: what a snapshot/restore takes back and what gets issued again |
| S8 | the single-writer cost: an unrelated write during a long publication |

Every negative claim has a positive control that removes the mechanism and must make the reader
find the failure: blind writes (S1), an unlocked source check (S2), check-then-insert ownership
(S4), a naive claim (S5), unlocked or deferred migration runners (S6).

## How a row is judged

Each row is one scenario on one backend. `run/all` writes its expectation before running it; the
clients are separate `e4db` processes started together behind a start barrier; and the outcome is
read back from the database by an independent reader, not from the clients' own reports:
`psql` inside the fixture's PostgreSQL container, and the host's `sqlite3` CLI, a different SQLite
build from the one the prototype links. The reader queries are in [`readers/`](readers/).
`run/lib.sh` compares and records one line in `cells.tsv` per row, and keeps every client's output,
every injection and every reader result as the row's transcript. A mismatch is recorded, not
retried.

## Running it

A capture needs a fresh fixture, and `E4_OUT` must name a new, empty absolute path on disk,
outside this checkout:

```sh
fixtures/bin/up
E4_OUT=<somewhere with ~50 MiB> experiments/e4-database-semantics/run/all
fixtures/bin/evidence <the same directory>
cp -a fixtures/.state/evidence/<bundle> <the same directory>/bundle
E4_OUT=<the same directory> experiments/e4-database-semantics/run/collect-evidence
fixtures/bin/down
```

`run/all` builds `e4db`, runs the matrix and then the prototype's own
`go test` against both backends. `run/collect-evidence` copies the text evidence into
[`evidence/`](evidence/), packs the transcripts one file per scenario and backend, rewrites local
paths to placeholders, and refuses anything matching this run's fixture credentials or a path in
the operator's home. An optional `bundle-summary.txt` in `E4_OUT` (the bundle's versions and leak
scan) is carried along.

The unit tests need no fixture: `go test ./...` runs them on a temporary SQLite file, and
`E4_TEST_PG_DSN=<dsn> go test ./...` on PostgreSQL.

The PostgreSQL DSN, which carries the password, reaches `e4db` through the environment
(`E4_PG_DSN`), never argv. `E4_BACKEND`, `E4_SQLITE_PATH` and `E4_SQLITE_TXLOCK` select the rest;
see [`main.go`](main.go).

## What it deliberately does not do

It does not dispatch to Talos, model approvals or the commitment-to-send boundary; that is
[issue 7](https://github.com/ginsys/bronzeward/issues/7). It does not run MySQL or MariaDB: the
fixture has no such service, and the report records that absence. It does not test replication,
failover or any backup tool the operator would run; the design assigns those to the operator.
