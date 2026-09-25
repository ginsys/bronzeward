# Database semantics: PostgreSQL and SQLite against the §7.2 backend contract

| | |
|---|---|
| **Date** | 24 September 2026 |
| **Work item** | [Experiment E4 - test database semantics](https://github.com/ginsys/bronzeward/issues/6) |
| **Design reference** | [§7.2 Relational revision model and backend contract](../Talos_Configuration_and_Machine_Management_Design.md#72-relational-revision-model-and-backend-contract), [§7.4 Publication without distributed transactions](../Talos_Configuration_and_Machine_Management_Design.md#74-publication-without-distributed-transactions), [§18.1 Phase 0](../Talos_Configuration_and_Machine_Management_Design.md#181-phase-0---evidence-before-implementation-contracts); specification [execution-recovery §3.2](../../spec/execution-recovery.md#32-the-commitment-transaction) and [§7](../../spec/execution-recovery.md#7-recovery-after-management-state-restoration) |
| **Artifacts** | [`experiments/e4-database-semantics/`](../../../experiments/e4-database-semantics/README.md), evidence under [`experiments/e4-database-semantics/evidence/`](../../../experiments/e4-database-semantics/evidence/) |
| **Decision enabled** | Whether PostgreSQL and SQLite each supply the §7.2 semantics, by which mechanism, and at what backend-specific cost; that MySQL/MariaDB have no evidence here. This is input to [deployment profile selection](https://github.com/ginsys/bronzeward/issues/13) and [persistence/API contracts](https://github.com/ginsys/bronzeward/issues/18). No database, driver, ORM, schema or migration tool is selected here. |

## 1. Question

Design §7.2 says: "The investigation must prove revision conflicts, all-or-nothing publication,
unique operation intent, ownership transitions, safe queue claims, migrations and restoration
behavior on each proposed backend. Do not assume PostgreSQL-specific locking or JSON features have
portable equivalents." PostgreSQL is the server option; SQLite is to be investigated "for small
single-instance deployments using the same required semantics"; MySQL/MariaDB are conditional
"only if they satisfy the contract with negligible additional implementation and maintenance
cost".

The question is therefore not which database is faster. It is whether each backend holds each
semantic under concurrent clients and interruption, what mechanism holds it, and how much of the
code has to differ between the two to get there. Each semantic is judged from the data an
independent reader finds afterwards, never from what the client reports.

Out of scope, and owned elsewhere: approval, the commitment-to-send boundary, Talos dispatch and
stale executors ([approval and dispatch safety](https://github.com/ginsys/bronzeward/issues/7));
replication, failover and the operator's backup tooling, which the design assigns to the operator.

## 2. Backends and identities

| | PostgreSQL | SQLite |
|---|---|---|
| Engine | 17.11 (`version()`: "PostgreSQL 17.11 on x86_64-pc-linux-musl"), the investigation fixture's container, image `docker.io/library/postgres:17-alpine@sha256:f02121de6f74d30d8a94cd1d9584125e2178d7e6c377d8130112d4e52d867995` | 3.53.4 (`sqlite_version()`), linked into the prototype by `modernc.org/sqlite` v1.59.0 |
| Driver | `github.com/lib/pq` v1.10.9, the driver E1 used | `modernc.org/sqlite` v1.59.0, pure Go, no cgo |
| Connection | TCP to the fixture's published port; the DSN, which carries this run's generated password, reaches the client through the environment only | a file under `fixtures/.state/data/e4`; every connection sets `busy_timeout(5000)`, `journal_mode(WAL)`, `foreign_keys(1)`, and begins every transaction `IMMEDIATE` ([`db.go:136`](../../../experiments/e4-database-semantics/db.go)) |
| Isolation | the server's default, `read committed` (`SHOW default_transaction_isolation`, recorded in [`run.txt`](../../../experiments/e4-database-semantics/evidence/run.txt)); the prototype sets none | one writer at a time: `BEGIN IMMEDIATE` takes the write lock at `BEGIN` |
| Independent reader | `psql` inside the PostgreSQL container, over its unix socket | the host's `sqlite3` CLI, 3.46.1, a separate build from the one the prototype links |

The two drivers are measurement tools. Neither is proposed for v1.

## 3. What was built

### 3.1 Rows

A *row* is one scenario on one backend. [`run/all`](../../../experiments/e4-database-semantics/run/all)
writes each row's expectation before running it, starts the clients as separate `e4db` processes
(several at once behind a start barrier where the row races them), injects the row's interruption,
and then reads the resulting database with the independent reader. The reader queries are in
[`readers/`](../../../experiments/e4-database-semantics/readers/). [`run/lib.sh`](../../../experiments/e4-database-semantics/run/lib.sh)
compares the expectation with the reading and with each client's exit status and output, and
appends one line to [`cells.tsv`](../../../experiments/e4-database-semantics/evidence/cells.tsv).
A mismatch is recorded, not retried. Every row starts from an empty schema: a dropped and recreated
`public` schema on PostgreSQL, a new file on SQLite.

The row number in this report is the `n` column of `cells.tsv`. Transcripts are packed one file per
scenario and backend, `evidence/transcripts/<scenario>-<backend>.txt`; each row's section begins
with a `==> <n>-<scenario>-<backend>-<row> <==` line and holds every client's command and output,
every injection, and every reader result.

| Scenario | Semantic | Rows (PostgreSQL, SQLite) |
|---|---|---|
| S1 | stale revision rejection | 001-003, 029-031 |
| S2 | all-or-nothing publication | 004-011, 032-039; PostgreSQL-only interruption rows 058-061 |
| S3 | unique operation intent | 012-014, 040-042 |
| S4 | ownership transitions | 015-018, 043-046 |
| S5 | queue claims | 019-021, 047-049; PostgreSQL-only `SKIP LOCKED` row 057 |
| S6 | migrations | 022-026, 050-054 |
| S7 | restored state | 027, 055 |
| S8 | an unrelated write during a long publication | 028, 056 |
| tests | the prototype's own `go test` against each backend | 062 (SQLite), 063 (PostgreSQL) |

### 3.2 Positive controls

A negative result ("no lost update", "no double claim") proves nothing unless the same reader finds
the failure when the mechanism is removed. Each semantic that rests on a mechanism has a control row
that removes it:

| Mechanism | Control | Where the control fires |
|---|---|---|
| revision compare-and-set (S1) | blind writes, no revision predicate | both backends |
| source lock at publication (S2) | the revision check without `FOR SHARE` | PostgreSQL; SQLite needs no row lock (§4.2) |
| ownership check inside the write (S4) | check, then insert | PostgreSQL; SQLite as above |
| guarded claim (S5) | claim without re-checking eligibility | PostgreSQL; SQLite as above |
| migration lock (S6) | no advisory lock on PostgreSQL; a deferred transaction on SQLite | both backends |

On SQLite the S2, S4 and S5 control rows (039, 046, 048) still begin every transaction `IMMEDIATE`,
which serializes every write transaction, so they remove nothing there. The S2 and S4 rows show only
that the naive code is also safe while the write lock is taken at `BEGIN`. The S5 row (048) does
not show even that: one worker made all 400 claims, and the other seven each waited out the busy
timeout once and then found the queue empty, so no two naive claimers ever competed for a job
(§4.5). Whether the naive claim is safe on SQLite under concurrent claimers was not measured.
The S6 SQLite control (054) is
the only row that runs without `IMMEDIATE`. Whether S2, S4 or S5 would stay correct on SQLite in
deferred mode was not run; SQLite's snapshot rules would likely refuse a stale read-then-write with
`SQLITE_BUSY_SNAPSHOT` rather than commit it, which is an inference, not a result.

### 3.3 One schema, and every difference counted

The schema is four migrations per dialect
([`migrations/`](../../../experiments/e4-database-semantics/migrations/)). The scenario SQL is shared
and written with `?` placeholders. Every place where SQL or transaction handling differs is a branch
on the dialect value, so the inventory below is complete for this prototype
(`grep -n 'case Postgres\|case SQLite\|[!=]= Postgres\|[!=]= SQLite'` over the non-test
sources, plus the two migration trees):

| # | Difference | PostgreSQL | SQLite | Where |
|---|---|---|---|---|
| 1 | placeholders | `$n`, rebound from `?` for every statement | `?` | [`db.go:42`](../../../experiments/e4-database-semantics/db.go) |
| 2 | connection setup | none | four DSN settings; `_txlock=immediate`, under which every SQLite row except the S6 control ran | [`db.go:136`](../../../experiments/e4-database-semantics/db.go) |
| 3 | error classification | SQLSTATE codes | primary and extended result codes | [`db.go:195`](../../../experiments/e4-database-semantics/db.go) |
| 4 | holding a read valid until commit | `SELECT … FOR SHARE` on the source revision | nothing: the write lock is already held | [`scenarios.go:222`](../../../experiments/e4-database-semantics/scenarios.go) |
| 5 | non-blocking queue claim | `FOR UPDATE SKIP LOCKED` | unavailable; refused | [`scenarios.go:457`](../../../experiments/e4-database-semantics/scenarios.go) |
| 6 | migration mutual exclusion | `pg_advisory_xact_lock` in each migration transaction | nothing: `BEGIN IMMEDIATE` | [`migrate.go:183`](../../../experiments/e4-database-semantics/migrate.go) |
| 7 | adding a constraint to an existing table | `ALTER TABLE … ADD CONSTRAINT`, one statement | a table rebuild (create, copy, drop, rename, recreate the index), with foreign-key enforcement switched off on the connection before `BEGIN` and `PRAGMA foreign_key_check` before `COMMIT` | [`migrate.go:124`](../../../experiments/e4-database-semantics/migrate.go), [`migrate.go:150`](../../../experiments/e4-database-semantics/migrate.go), `migrations/*/0004_job_state_check.sql` |
| 8 | column types | `BIGSERIAL`, `BIGINT`, `BYTEA` | `INTEGER PRIMARY KEY AUTOINCREMENT`, `INTEGER`, `BLOB` | `migrations/*/0001-0003` |

`db.go:163` also branches, to choose the version query the run record cites; it is reporting, not
semantics. What did **not** need a branch: `RETURNING`, partial unique indexes, `UPDATE … WHERE id =
(subquery)`, transactional DDL, and the conditional `UPDATE` every compare-and-set rests on. Item 8
has a semantic edge: without `AUTOINCREMENT`, SQLite may reuse the largest rowid after a delete,
where a PostgreSQL sequence never goes back; the migration comment records why it is there.

### 3.4 Pinned versions

| Component | Version |
|---|---|
| PostgreSQL | 17.11, image digest above, from the fixture's evidence bundle ([`bundle-summary.txt`](../../../experiments/e4-database-semantics/evidence/bundle-summary.txt)) and `run.txt` |
| SQLite, linked | 3.53.4, `modernc.org/sqlite` v1.59.0, `modernc.org/libc` v1.75.7 |
| SQLite, reader | host `sqlite3` 3.46.1 |
| `lib/pq` | v1.10.9 |
| Go | 1.27.1 |
| Captured from | the commit in [`run.txt`](../../../experiments/e4-database-semantics/evidence/run.txt), with the uncommitted inputs it lists |

The fixture does not pin the SQLite engine; the prototype's module graph does, and every run
records `sqlite_version()` and the module versions from the built binary (`go version -m`).

### 3.5 Synthetic values and the leak scan

No secret value is stored: artifact "ciphertext" is synthetic bytes, and the only credential is the
fixture's generated PostgreSQL password, which reaches the clients through the environment only.
After `run/all`, `fixtures/bin/evidence` scans the fixture state and the run directory for this
run's credentials and canary; its hits are the planted positive control, that control's copy inside
the S7 SQLite store snapshot, and the run's own copy of the pattern list, and nothing else
([`bundle-summary.txt`](../../../experiments/e4-database-semantics/evidence/bundle-summary.txt)).
`run/collect-evidence` then refuses any collected file that matches a pattern or names a path in
the operator's home, after proving with a planted file that the scan matches
([`leak-scan.tsv`](../../../experiments/e4-database-semantics/evidence/leak-scan.tsv)).

### 3.6 Reproduction

```sh
fixtures/bin/up
E4_OUT=<new empty absolute path outside the checkout> experiments/e4-database-semantics/run/all
fixtures/bin/evidence <E4_OUT>
cp -a fixtures/.state/evidence/<bundle> <E4_OUT>/bundle      # before down, which removes .state
E4_OUT=<E4_OUT> experiments/e4-database-semantics/run/collect-evidence
fixtures/bin/down
```

`run/all` took under three minutes after `up` in the final capture (`started` and `finished` in
`run.txt`). The bundle stays in `E4_OUT`: it holds a PostgreSQL data directory and dumps and is not
committed. `run/collect-evidence` refuses to run without it and carries its versions and scan
result into `evidence/` as [`bundle-summary.txt`](../../../experiments/e4-database-semantics/evidence/bundle-summary.txt),
with every copied file and its SHA-256 in
[`bundle-manifest.txt`](../../../experiments/e4-database-semantics/evidence/bundle-manifest.txt);
the 2338 files of the PostgreSQL data directory are one line there, the digest of their lines in
the full manifest, which stays in `E4_OUT`.

## 4. Results

Every row matched its expectation: 63 rows, 0 mismatches
([`run.txt`](../../../experiments/e4-database-semantics/evidence/run.txt)). Every positive control
fired where §3.2 says it should. The tables give what the reader found; the transcripts hold the
client outputs.

### 4.1 S1: stale revision rejection

A writer reads a fragment's revision and updates it with `WHERE name = ? AND revision = ?`; zero
rows affected is a stale write, rejected. Every accepted write also appends a `revision` row to an
append-only ledger, which is what the reader counts.

| Row | What runs | PostgreSQL | SQLite |
|---|---|---|---|
| cas (001, 029) | 8 writers in one process, 20 rounds, all holding the same read each round | 20 wins, 140 conflicts; 20 revisions recorded, 0 duplicate, 0 lost | same |
| cas-processes (002, 030) | 4 processes of 4 writers, released together | 0 duplicate, 0 lost | same |
| blind-control (003, 031) | the same race without the revision predicate | 160 writes accepted onto 20 revisions: 140 lost updates, 20 duplicated revision numbers | same |

The compare-and-set is portable SQL and holds on both. The control shows the reader detects exactly
the failure it rules out.

### 4.2 S2: all-or-nothing publication

`Publish` checks every pinned source revision, inserts the release, its sources and each artifact,
and a `publish` ledger row, in one transaction. A release is complete when its artifact count equals
the count it declares. A retry with the same name and the same content digest returns the existing
release; the same name with different content is refused.

| Row | What runs | PostgreSQL | SQLite |
|---|---|---|---|
| normal (004, 032) | 8 artifacts | 1 release, 8 artifacts, 1 source | same |
| stale-input (005, 033) | the source moves on first | refused `stale`; nothing written | same |
| fail-at-5 (006, 034) | an injected SQL error before artifact 5 | nothing written | same |
| client-sigkill (007, 035) | the client is killed holding the transaction after 4 artifacts | nothing written | same |
| kill-before-commit (008, 036) | killed at the point it would send `COMMIT`; then a retry | nothing, then 1 complete release, `existing=false` | same |
| retry (009, 037) | publish, identical retry, different retry | `existing=true`, then refused `conflict`; 1 release | same |
| race-locked (010, 038) | a writer updates the pinned source while the publication holds its transaction for 3 s | the writer waits; 0 releases stale at commit | same |
| race-unlocked-control (011, 039) | the same, without `FOR SHARE` | **1 release stale at commit**: the writer committed first and the release was published on a superseded source | 0: the writer waited for the write lock |
| server-kill (058) | `inject kill postgres` with the transaction open | client `class=conn` at its next statement; nothing written | no server |
| server-pause (059) | `inject pause postgres` for 3 s with the transaction open | the client stalls and then commits: 1 complete release | no server |
| netsplit (060) | `inject netsplit postgres` for 5 s with the transaction open | client `class=conn`; nothing written | no server |
| commit-unknown (061) | `COMMIT` sent to a paused server, the client killed, the server resumed, then a retry | **the release committed** (read before the retry: 1 release, 8 artifacts); the retry returns `existing=true` for the same id | no server |

Atomicity holds on both, including every interruption tried. Two findings matter for the design:

- **On PostgreSQL, a revision check alone is not enough.** §7.4 step 4 requires "rejecting stale
  input revisions". Under the default isolation a plain `SELECT` of the revision does not stop
  another transaction changing it before commit (row 011). `SELECT … FOR SHARE` does (row 010).
  SQLite needs no such lock because its writers are serialized. This is exactly the §7.2 warning
  that locking does not port: the same correct-looking code is safe on one backend and not the
  other.
- **The commit-unknown outcome is decided by the server, and only the data says which.** Here the
  release committed after the client was gone. An idempotent retry keyed by release name and
  content digest resolved it without a duplicate. A retry that is not idempotent, or a client that
  treats the lost answer as a failure, would have been wrong.

### 4.3 S3: unique operation intent

An operation row carries a unique idempotency key, and a partial unique index allows at most one
operation per machine scope in `committed`, `sending`, `verifying` or `unresolved`, the states of
[execution-recovery §3.2](../../spec/execution-recovery.md#32-the-commitment-transaction)
comparison 4.

| Row | What runs | PostgreSQL | SQLite |
|---|---|---|---|
| race-processes (012, 040) | 8 processes, 8 keys, one machine, released together | 1 created, 7 refused `scope-busy`; 1 operation, never two active on the scope | same |
| retry-same-key (013, 041) | the same key twice | the second returns operation 1, `created=false` | same |
| after-completion (014, 042) | a second key is refused, the first completes, the second is accepted; another machine is independent | 3 operations, never two active on a scope | same |

Both enforce it with the same schema: partial unique indexes are portable between these two.

### 4.4 S4: ownership transitions

An operation's owner carries a generation. A takeover is `UPDATE … SET owner = ?, owner_gen =
owner_gen + 1 WHERE id = ? AND owner_gen = ?`. An attempt must be recorded by the current owner:
the prototype does it as a conditional `UPDATE … WHERE owner = ? AND owner_gen = ?` first, which
both checks and locks, and appends the attempt to the timeline only if it matched.

| Row | What runs | PostgreSQL | SQLite |
|---|---|---|---|
| takeover-race-processes (015, 043) | 8 processes take over generation 1 at once | exactly 1 wins; 1 takeover recorded | same |
| stale-owner-fenced (016, 044) | the old owner attempts after a takeover | refused `fenced`; the new owner's attempt is recorded; 0 stale attempts | same |
| attempt-race-fenced (017, 045) | an attempt holds its transaction 3 s after its check while a takeover runs | the takeover waits for it; 0 stale attempts | same |
| attempt-race-check-then-insert-control (018, 046) | the same, with the ownership read by a plain `SELECT` and the attempt inserted after | **1 stale attempt**: the takeover committed in between and the old owner's attempt was recorded after it | 0: serialized |

This is the database half of the specification's requirement that the attempt transaction
"confirms that the executor recording it is the operation's current owner" and that "reading the
approval or the ownership and recording the attempt later is not sufficient" (specification §3.3).
Row 018 is that insufficiency, measured. It is database fencing only: nothing here stops an
executor that already committed its attempt from sending, which specification §3.3 also says.

### 4.5 S5: queue claims

A claim sets `state = 'claimed'`, increments the job's fence and sets a lease; completion is
`UPDATE … WHERE id = ? AND fence = ? AND state = 'claimed'`. Every claim and completion appends to
`job_event`, which the reader uses to count double claims. Eight worker processes drain 400 jobs.

| Row | Mode | PostgreSQL | SQLite |
|---|---|---|---|
| workers-guarded (019, 047) | `WHERE id = (SELECT … LIMIT 1) AND <eligible>` | 400 done, 400 claims, 400 completions, 0 jobs claimed or completed more than once | same |
| workers-naive (020, 048) | the same without re-checking eligibility | **1105 claims, 309 jobs claimed more than once, 401 completions, 1 job completed twice**; the fence refused the other 704 completions | 400 claims, 400 completions, 0 more than once, all 400 by one worker: no claimers competed, so this is not evidence the naive claim is safe |
| workers-skip-locked (057) | `… FOR UPDATE SKIP LOCKED` | 400 done, 400 completions, 0 more than once | unavailable |
| lease-expiry (021, 049) | worker a claims with a 1 s lease; b cannot claim early, then claims after expiry at fence 2; a's late completion is refused | as described; the job ends `done` by b at fence 2 | same |

The naive mode is the portable-looking query that is wrong on PostgreSQL: under the default
isolation, an `UPDATE` that waited on a row another claimer changed re-checks only its own `WHERE`
against the new row version, and `id = <the id the subquery already chose>` still holds. The guarded
mode adds the eligibility predicate to that `WHERE`, so the re-check fails and the claimer retries.
The fence refuses the superseded claimer's completion, but it does not make the naive mode safe.
A claimer that waited on a job whose holder then completed it re-claims the `done` job: the
re-check sees only `id = …`, the claim sets `state = 'claimed'` and a new fence, and the second
completion carries that new fence and succeeds. Row 020 recorded this once in 400 jobs; the row
asserts only that the counts were recorded, because how often the interleaving happens varies
between captures. The guarded and `SKIP LOCKED` rows assert 400 completions and none twice.

Cost under contention, from the per-worker lines in the transcripts (one capture; timings vary
between captures):

| | PostgreSQL guarded | PostgreSQL `SKIP LOCKED` | SQLite guarded | SQLite naive |
|---|---|---|---|---|
| wall time for 400 jobs | about 1.4 s | about 0.4 s | about 7.2-7.3 s | about 5.4-5.8 s |
| per worker | 35-90 jobs, 221-320 lost races each | 50 jobs, 0 lost races | 1-262 jobs; two workers each waited out the 5 s busy timeout and failed once with `SQLITE_BUSY` | one worker did all 400; the other seven each waited out the busy timeout, failed once, and found the queue empty |

On SQLite more workers buy nothing, and the hand-off of the write lock is not fair: a worker that
keeps committing can keep the lock while others wait out their busy timeout. How the jobs spread
across workers differed between captures of the same code.

### 4.6 S6: migrations

The fixtures describe no migration surface, so the prototype carries its own runner
([`migrate.go`](../../../experiments/e4-database-semantics/migrate.go)): each migration is one
transaction on a pinned connection that checks `schema_migrations`, runs the migration's SQL and
records it. On PostgreSQL the transaction first takes `pg_advisory_xact_lock`; on SQLite `BEGIN
IMMEDIATE` is the lock.

| Row | What runs | PostgreSQL | SQLite |
|---|---|---|---|
| fresh (022, 050) | four migrations | all applied; the constraint of migration 4 exists | same, migration 4 by table rebuild |
| fail-then-rerun (023, 051) | an injected error inside migration 3, then a second runner | migration 2 kept, migration 3 and its table absent; the re-run applies 3 and 4 | same |
| sigkill-then-rerun (024, 052) | the runner killed inside migration 3's transaction | as above | same |
| concurrent-locked (025, 053) | migration 1 applied, then 4 runners released together, each holding migration 2 open 500 ms | 0 failures; each version applied once (3 applications across the 4 runners) | same |
| concurrent-unlocked-control (026, 054) | the same without the lock | 3 of 4 runners fail with a unique violation on the system catalog (`pg_class_relname_nsp_index`) as they create the same table; each version still recorded once | 3 of 4 fail with `SQLITE_BUSY` at once, not after the busy timeout: a deferred transaction that has read cannot wait its way into a write |

Transactional DDL holds on both: a failed or killed migration leaves nothing of itself. Without
the lock neither backend applied a migration twice, but concurrent runners failed instead of
waiting, which a deployment with more than one starting instance would see as a failed start.

### 4.7 S7: restored state

After release r1 and operation 1 (owner x at generation 1), the database is snapshotted: `db-snapshot`
(a `pg_dump`) on PostgreSQL; on SQLite `store-snapshot`, a copy of the files, taken only after the
reader confirmed no process held the database open (`fuser`; row notes `quiesced-at-snapshot=yes`,
`quiesced-at-restore=yes`). After the snapshot: release r2, takeovers to y (generation 2) and back
to x (generation 3), an attempt by x at 3, operation 2. Then the restore.

| Observation (027, 055) | PostgreSQL | SQLite |
|---|---|---|
| before the restore | 2 releases, 2 takeovers, 1 attempt | same |
| after the restore | 1 release, 0 takeovers, 0 attempts, 1 operation, owner x at generation 1 | same |
| the next release | gets **id 2**, r2's id, with different content | same: `AUTOINCREMENT`'s counter is in the copied file |
| the next two takeovers | issue generations 2 and 3 again, to y and then x | same |
| x's pre-restore token (x, 3) | **passes the fence again** | same |

A restore rewinds identifiers and fencing generations along with the data, on both backends and
with both snapshot methods. Anything outside the database that still holds a pre-restore identifier
or token (an executor, a provider object named after a release id, a log) can collide with a newly
issued one, and the fence cannot tell them apart. This is the hazard the specification's recovery
epoch exists for (§7): after restore, an old token and a new one are indistinguishable by value
alone.

### 4.8 S8: the single-writer cost

A publication holds its write transaction for 7 s; another client updates an unrelated fragment.

| Row | PostgreSQL | SQLite |
|---|---|---|
| unrelated-writer (028, 056) | the write completes in 3 ms | the write waits the whole 5 s busy timeout and fails with `SQLITE_BUSY`; nothing written |

Every write transaction on SQLite blocks every other writer for its whole duration. This is the
cost of the serialization under which every SQLite row except the S6 control ran.

### 4.9 The prototype's tests

`go test` passed against both backends at the captured commit (rows 062, 063). The tests cover the
placeholder rebinding, error classification, the busy timeout, the migration runner (fresh,
failure rollback, concurrent runners, the SQLite rebuild), S1-S5, and the S1, S2 and S4 controls.
The S5 naive claim and the S6 unlocked runners have no test; their rows are their only evidence.

## 5. Failures hit while building it

### 5.1 The SQLite migration control could not fail

The first capture's SQLite `concurrent-unlocked-control` row (deferred transactions) found no
failure. The four runners started with an empty database, and the busy handler serialized them at
the first write, the `schema_migrations` bootstrap, before any of them had read anything a later
write could invalidate. The control was therefore a second copy of the locked row. Applying
migration 1 before releasing the runners means all four read "migration 2 missing" from the same
state and then race to write; the control then failed as expected (row 054). A control that did not
fire was recorded as inconclusive, not as a pass.

### 5.2 A run record counted its own output as input

`run.txt` records how many of the prototype's and fixture's files differ from the captured commit.
The count first included `evidence/`, the collector's own output from an earlier capture, and so
reported uncommitted inputs where there were none of substance. The count now excludes `evidence/`
and lists each uncommitted path by name.

### 5.3 The evidence bundle was not copied out before teardown

`fixtures/bin/down` removes `.state`, and with it the evidence bundle. One capture summarized the
bundle but did not copy it, which the issue's run design requires; the capture was repeated with the
copy made before `down`.

### 5.4 Constraints on existing SQLite tables

SQLite cannot add a `CHECK` constraint to an existing table. The documented rebuild drops the old
table, which another table references, so it needs foreign-key enforcement off; SQLite ignores that
pragma inside a transaction. The runner therefore switches it off on the connection before `BEGIN`,
runs `PRAGMA foreign_key_check` before `COMMIT`, and switches it back on afterwards (§3.3 item 7).

## 6. What this decides

### 6.1 Criterion 1: stale revision rejection and all-or-nothing publication

Reproduced on both backends: S1 (§4.1) and S2 (§4.2), including client kill before and during the
transaction on both, and server kill, pause, network partition and commit-unknown on PostgreSQL.
On PostgreSQL stale rejection at publication additionally needs the source rows locked for the
check (`FOR SHARE`); without it a release commits on a superseded source (row 011).

### 6.2 Criterion 2: intent, ownership, queue claims, migrations, restored state

Exercised on both backends:

- **Unique intent** holds with one schema (§4.3).
- **Ownership transitions** hold when the ownership check is part of the write that records the
  attempt; a separate read is unsafe on PostgreSQL (§4.4).
- **Queue claims** are safe with the guarded claim and fencing on both. The naive claim is unsafe
  on PostgreSQL even with the fence: it claims jobs more than once, and it can re-claim a job that
  is already `done` and complete it again, which the fence cannot see (§4.5). On SQLite the naive
  row had a single active claimer, so it says nothing about the naive claim there.
- **Migrations** roll back on failure and on kill, and concurrent runners apply each version once
  under the lock (§4.6).
- **Restored state** rewinds data, identifiers and fencing generations together; tokens issued after
  the snapshot are issued again and pass (§4.7).

**The migration gap the issue names is resolved as follows.** The fixtures supply no migration
surface. The prototype's runner is sufficient evidence for what §7.2 asks of the *engines*:
transactional DDL, rollback on failure and interruption, and mutual exclusion of concurrent runners
by an advisory lock (PostgreSQL) or the write lock (SQLite). It is not evidence for any v1 migration
tool, for online migration of a large table, or for downgrade, none of which was attempted.

### 6.3 Criterion 3: backend-specific limitations and costs

**PostgreSQL** supplies every semantic, but not with the SQL that looks portable. Four of the
prototype's correct statements are correct on PostgreSQL only because of a clause or predicate that
SQLite does not need: `FOR SHARE` at publication, the ownership check inside the attempt's
`UPDATE`, the eligibility re-check in the claim, and the migration advisory lock. Each control shows
the failure without it (rows 011, 018, 020, 026). Its costs are operational: a server to run, which §7.2 assigns to the
operator.

**SQLite** supplies every semantic here, by one mechanism: every write transaction takes the write
lock at `BEGIN`. That is also its limitation:

- one writer at a time, for the whole write transaction (S8), so a long write stalls every other
  write and a busy timeout must be longer than the longest write transaction;
- no useful write concurrency, and an unfair hand-off under contention (S5);
- every SQLite row except the S6 control ran under `_txlock=immediate`. The one deferred-mode row
  (054) failed its concurrent runners with `SQLITE_BUSY` where PostgreSQL waits: an availability
  failure, not a wrong result. Whether the other semantics would stay correct in deferred mode was
  not run (§3.2);
- schema changes that PostgreSQL does in one statement need a rebuild (§3.3 item 7);
- a file copy is a valid backup only while nothing holds the database open (the issue's own limit;
  S7 quiesced before both snapshot and restore);
- the engine version is whatever the program links, so it must be recorded with every result.

**Implementation cost of supporting both**, measured on this prototype: 8 differences (§3.3). Two
concern the semantics themselves (items 4 and 6) and one is an optional PostgreSQL-only claim mode
(item 5); one is a migration that differs completely (item 7); four are mechanical (placeholders,
connection setup, error codes, column types). The two shared statements PostgreSQL needs and SQLite
does not (the ownership and eligibility predicates) are not branches, because they are harmless on
SQLite; they are a cost all the same, since only a PostgreSQL test shows why they are there. Two migration trees must be kept in step. Every semantic needs its own test on each
backend, because the controls show that a query correct on one can be wrong on the other; this
prototype's matrix runs each row twice for that reason.

**MySQL/MariaDB: no evidence.** The fixture has no such service and no row was run. §7.2 admits them
only with evidence of equivalent semantics and negligible cost; this investigation supplies neither,
and §3.3 suggests the cost would not be negligible: `SKIP LOCKED`, advisory locks, partial unique
indexes, transactional DDL and `RETURNING` are each a point where a third dialect would need its own
evidence.

## 7. Limits

- **Synthetic load, one capture.** 8 writers or workers, 400 jobs, small rows. Throughput numbers
  are one capture on one host and show relative shape, not capacity.
- **One engine version each.** PostgreSQL 17.11 and SQLite 3.53.4. Results are reproducible with
  those versions recorded, not across versions.
- **Default isolation.** PostgreSQL ran at its default level. Serializable isolation, which would
  change several control results into serialization failures, was not run.
- **Client-clock leases.** Lease expiry compares the claiming client's clock (S5). Workers with
  skewed clocks were not tested; a server-clock lease is an implementation choice this does not make.
- **SQLite naive claim not contended.** In row 048 one worker made every claim and the other seven
  never claimed, so the naive claim on SQLite is unmeasured, not shown safe. A run that forces
  claimers to interleave (for example by yielding the write lock between claims) was not made.
- **Ledger order is insert order.** The S2 and S4 readers order events by an identity column, which
  records when a row was inserted, not when its transaction committed. In the rows that use it the
  competing transaction commits before the one it is compared with, by construction.
- **The network partition reached the client at once.** Through the fixture's published port,
  `netsplit` surfaced as a broken connection at the client's next statement (row 060), not as a
  silent partition that would leave a client waiting on a TCP timeout. That case was not produced.
- **No replication, failover, pooling or operator backup tool.** `db-snapshot` is a `pg_dump`; a
  physical backup or point-in-time recovery may behave differently at the edges and was not run.
- **One SQLite process model.** Several processes on one host, one file, WAL mode. Network
  filesystems and multiple hosts were not tested and are outside SQLite's own documented use.
- **Mechanisms, not an implementation.** The schema is the smallest that answers each semantic; it
  is not the Bronzeward schema.
- **Two error paths an implementation must not copy.** `Classify` (`db.go`) treats any `net.Error`
  as a lost connection, which would also catch `context.DeadlineExceeded`, and `TakeOver` and
  `RecordAttempt` (`scenarios.go`) fold a `RowsAffected` error into the fenced result.
  Neither could change this capture: the prototype sets no context deadline, and both drivers'
  `RowsAffected` return a nil error (`database/sql/driver.RowsAffected`, which `lib/pq` returns,
  and `modernc.org/sqlite`'s `result`). The measured code was left as captured.
- **The S6 SQLite control's transcript does not show what makes it a control.** Row 054 selects
  deferred transactions through `E4_SQLITE_TXLOCK=deferred` (`run/all`, `s6_concurrent`), not a
  flag, so its recorded command lines are the same as row 053's. Only the outcomes differ.
- **Three harness weaknesses that did not reach the evidence.**
  - `e4_kill` (`run/lib.sh`) falls back to killing the `timeout` wrapper when `pgrep` finds no
    `e4db` child, and `pgrep` is not in the preflight. In all seven kill rows, the PID that got
    the SIGKILL is the one `e4db` reported for itself.
  - `RaceRevisions` (`scenarios.go`) counts a busy outcome instead of retrying it. Both S1
    compare-and-set rows assert `busy=0`, and `TestS1CompareAndSetKeepsEveryWrite` fails on any
    busy outcome, so one could not pass silently.
  - The tests' `read` helper (`scenarios_test.go`) does not check `rows.Err()`. The harness's
    readers are `psql` and `sqlite3`, not this helper. In the tests, a truncated read can only drop
    keys, and every key a test checks is compared with a non-empty value, so a dropped key fails.

## 8. Recommendation

This does not select a database. It narrows what
[deployment profile selection](https://github.com/ginsys/bronzeward/issues/13) has to choose between.

1. **PostgreSQL meets every semantic §7.2 names, provided the implementation locks what it checks.**
   Nothing here argues against it as the server option. Its SQL needs row locks or re-checked
   predicates in exactly the places a naive port would omit them, and each such place needs a test
   that can fail.
2. **SQLite meets every semantic for a single-instance deployment in which write transactions are
   short**, because it serializes all writes. The cost is throughput and write latency under
   contention, not correctness. If it is offered, every transaction should begin `IMMEDIATE`, the
   mode every SQLite result here was measured in (deferred mode was measured for migrations only,
   §3.2), write transactions must not span slow work (encryption, provider calls, network I/O), and
   a file-copy backup must be taken with the database quiesced: a quiesced copy restored correctly
   here, and an unquiesced copy was not tried. SQLite's online backup API was not tested.
3. **Supporting both roughly doubles the database test matrix and keeps two migration trees**, on
   top of the dialect branches in §3.3. If the PoC needs one database, PostgreSQL is the one the
   design already names as the server option, and the one on which every control found the failure
   it looks for, so its tests are the ones that can fail.
4. **MySQL/MariaDB stay unsupported** until someone produces the equivalent evidence and a
   negligible-cost case; nothing here does.

## 9. Hand-off

**To [approval and dispatch safety](https://github.com/ginsys/bronzeward/issues/7):**

- The ownership record that holds on both backends is a conditional `UPDATE` on the owner and
  generation inside the attempt transaction (row 017). A read followed by an insert records a stale
  attempt on PostgreSQL (row 018). This is database fencing only.
- A restore re-issues owner generations: a pre-restore token passes the fence again (§4.7). Any
  executor running across a restore must be stopped by something other than the fence, which is
  what specification §7 step 1 requires.
- In the one commit-unknown row (061), the client could not tell whether its release committed;
  reading the data and retrying idempotently resolved it without a duplicate.

**To [persistence/API contracts](https://github.com/ginsys/bronzeward/issues/18):**

- Required locking per semantic: `FOR SHARE` on source revisions at publication, a conditional
  `UPDATE` for ownership and completion, an eligibility-re-checking claim, an advisory lock for
  migrations (PostgreSQL); `BEGIN IMMEDIATE` for every transaction (SQLite).
- Idempotency by name plus content digest resolved commit-unknown without a duplicate.
- Identifiers and generations are not unique across a restore (§4.7). Inference, not measured: a
  recovery epoch stored in the same database is rewound by the same restore, so "greater than any
  value in the restored state" holds but does not by itself make it greater than an epoch issued
  after the snapshot. Whether the epoch needs a source that survives a restore is for the contract
  to decide.

**To [key loss and restoration](https://github.com/ginsys/bronzeward/issues/10):** an application
database restore rewinds release ids (§4.7). Inference, not measured: a provider object named after
a release id issued after the snapshot would then match a different release issued after the
restore.

**To [deployment profile selection](https://github.com/ginsys/bronzeward/issues/13):** §8 above.
