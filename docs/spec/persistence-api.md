# Persistence and API contract

This document specifies how the first milestone stores its records, commits
publications, and exposes an authenticated API, as required by
[Specify persistence and API contracts](https://github.com/ginsys/bronzeward/issues/18).
It refines the [current design](../design/Talos_Configuration_and_Machine_Management_Design.md)
§§7.2, 7.4, 7.7, 7.8, 11, 13.7 and 14.6 for the PoC profile: one PostgreSQL
server as the application database and one OpenBao node with KV v2 and Transit
as the provider
([design §7.7](../design/Talos_Configuration_and_Machine_Management_Design.md#77-poc-deployment-profile)).
Each section names the design text it refines. It is a PoC contract, not
evidence that any mechanism here has been built or measured beyond what the
cited rows show.

The evidence it rests on, abbreviated below:

| Short name | Report |
| --- | --- |
| DB | [Database semantics](../design/research/20260924-database-semantics.md) |
| DS | [Approval and dispatch safety](../design/research/20260925-dispatch-safety.md) |
| KL | [Key loss and restoration](../design/research/20260924-key-loss-restoration.md) |
| RC | [Retention and metadata classification](../design/research/20260924-retention-metadata-classification.md) |
| PC | [Provider capability comparison](../design/research/20260924-provider-capability-comparison.md) |
| E1 | [Secret ingress: extraction before persistence](../design/research/20260922-secret-ingress-extraction-before-persistence.md) |
| FR | [Feasibility evidence review](../design/research/20260925-feasibility-evidence-review.md) |

The database evidence holds for PostgreSQL 17.11 at its default isolation, one
server, synthetic load, one capture, and logical `pg_dump` snapshots only
([DB §7](../design/research/20260924-database-semantics.md#7-limits)).
Dispatch safety ran on PostgreSQL only, for one operation and mode
([DS §7](../design/research/20260925-dispatch-safety.md#7-limits)). No
investigation tested application authentication, authorization or the
operation timeline, and none tested a restore epoch
([FR §9](../design/research/20260925-feasibility-evidence-review.md#9-gaps)
items 4 and 5). §16 lists what this contract therefore claims only as
design.

Two sibling contracts share its boundaries: the
[secret ingress and compilation contract](compilation.md) (abbreviated
"compilation" below) and the
[execution and recovery contract](execution-recovery.md) ("execution and
recovery"). Both are cited by section topic and number. The execution and
recovery contract is being revised in parallel, so its section numbers may
change; the topic names the clause.

Where neither the design nor the evidence decides a question, this contract
takes the most conservative option and marks it in place as
**(choice §17.n)**; §17 lists each with its alternative for owner review.
Claims resting on inference rather than a measured row say so in place.

## 1. Scope and interfaces

Design: [§7.2](../design/Talos_Configuration_and_Machine_Management_Design.md#72-relational-revision-model-and-backend-contract),
[§11](../design/Talos_Configuration_and_Machine_Management_Design.md#11-northbound-web-api),
[§13.7](../design/Talos_Configuration_and_Machine_Management_Design.md#137-poc-identity-and-approval-policy).

In scope: identifiers; entities and which records are mutable or immutable;
revisions and optimistic concurrency; transaction boundaries; the publication
protocol with provider failure and orphans; idempotency; the asynchronous
operation handle; API resources, requests, responses and errors;
authentication and per-endpoint authorization, with the acting identity, role
and self-approval mark recorded with every act; migrations; restored state; and
the recovery epoch as a persistence mechanism.

Out of scope, owned elsewhere:

- plan binding, the dispatch commitment, the operation state machine, retry
  and stop rules, drift, the post-restore recovery procedure and timeline
  semantics: execution and recovery. This contract gives those facts a storage
  shape and API routes, and states in §1.2 what it needs from them;
- ingestion, staging claims, keyed digests, composition and the release
  content handed to persistence: compilation §2–§11;
- operating, backing up and restoring the database and OpenBao: the operator
  ([design §7.7](../design/Talos_Configuration_and_Machine_Management_Design.md#77-poc-deployment-profile)
  duties 1–6; [§14.2](../design/Talos_Configuration_and_Machine_Management_Design.md#142-operator-owned-database-and-vault-services));
- the adoption workflow ([ginsys/bronzeward#22](https://github.com/ginsys/bronzeward/issues/22))
  and the edit and publication user flow
  ([ginsys/bronzeward#23](https://github.com/ginsys/bronzeward/issues/23)).

Bronzeward owns the schema, migrations and connection and transaction behavior
(design §7.2). The API process runs as the normal API component identity of
compilation §1: it holds no secret value and no plaintext input.

### 1.1 What this contract takes from compilation

- The draft transaction (compilation §3.4): the draft, its reference rows and
  the claim's `released` state in one transaction. §5 adds the idempotency and
  act records to it.
- The publication hand-off unit (compilation §11): release metadata, and per
  machine the artifact ciphertext and digest, redacted review data, provenance
  and both dependency records with the encryption dependency. §6 commits it.
- The import base as a per-machine source revision (compilation §6).
- Keyed digests under a provider-held key (compilation §4.1), which §7 uses to
  fingerprint request bodies that can hold secrets.
- Staging claims with owner generation, server-clock lease and absolute expiry,
  abandoned at recovery-mode entry (compilation §3.2–§3.5).

It needs from compilation, as cross-contract points:

1. The component of a generation path "that the database does not issue"
   (compilation §2.3 step 6) is the ingestion's claim identifier followed by a
   random value identifier (§6.4) **(choice §17.8)**.
2. The per-machine digest in the hand-off unit is the digest of the
   configuration, not of its ciphertext, so that it is stable across a second
   encryption (§6.2).

### 1.2 What this contract needs from execution and recovery

1. Every fence the commitment and attempt transactions compare includes the
   current recovery epoch's identity (§12.1), read with the installation state
   row `FOR SHARE`.
2. The commitment and attempt transactions read the machine's assignment head
   `FOR SHARE`, so that a publication changing that assignment (§6.2) and a
   commitment serialize: the rule "any transaction that changes a machine's
   assignment must check that machine's coordination scope" in the
   dispatch commitment section of execution and recovery.
3. An approval's epoch comparison is equality with the current epoch's
   identity. The contract's "recorded in the current recovery epoch" already
   reads this way; its counter is kept for order (§12.1).
4. Recovery-mode entry runs as one transaction that also performs §12.2's
   persistence effects.
5. The post-restore procedure includes re-recording identity revocations made
   after the backup was taken, and reissuing automation tokens (§12.3).
6. The operation is created with its plan, in state `planned` (§8.1)
   **(choice §17.11)**.

## 2. Identifiers

Design: [§4.4](../design/Talos_Configuration_and_Machine_Management_Design.md#44-identity-layers),
[§7.7](../design/Talos_Configuration_and_Machine_Management_Design.md#77-poc-deployment-profile).

A restore rewinds identifiers: after restoring a `pg_dump`, the next release
received the id of a release issued after the snapshot, with different content
([DB §4.7](../design/research/20260924-database-semantics.md#47-s7-restored-state),
row 027). Anything outside the database that held the old id (a client, a
log, a provider path) would then name a different record.

Every identifier that leaves the database is therefore generated by the
application, not by a database sequence **(choice §17.1)**:

```text
<prefix>_<26 characters>     128 bits from a cryptographic random source,
                             RFC 4648 base32, lower case, no padding

rel_fgqvcvz3ck7h7234ljgdbzsj6m
```

| Prefix | Entity | Prefix | Entity |
| --- | --- | --- | --- |
| `cl` | cluster | `pln` | plan |
| `mch` | machine (design §4.4 manager machine ID) | `apr` | approval |
| `frg`, `frv` | fragment, fragment revision | `op` | operation |
| `prf`, `prv` | profile, profile revision | `att` | attempt |
| `asg`, `asr` | assignment, assignment revision | `obs` | observation |
| `ibr` | import base revision | `idn` | principal (human or service identity) |
| `drf` | draft | `tok` | automation token |
| `rel` | release | `ing` | ingestion claim |
| `ep` | recovery epoch | `act` | act record |
| `req` | API request | | |

A collision is not checked for beyond the primary-key constraint, which refuses
it. The manager machine ID of design §4.4 is the `mch` identifier.

Internal `bigint` sequence keys may exist for joins and ordering, but never
appear in an API payload, a URL, a cursor, a provider path or a log line. A
restore can reissue them; they are compared only inside one database state.

Revision numbers (§4) are counters and do rewind with a restore. Every place a
revision number leaves the database, it travels with a random token (§4.1).

## 3. Entities and records

Design: [§7.2](../design/Talos_Configuration_and_Machine_Management_Design.md#72-relational-revision-model-and-backend-contract),
[§11.2](../design/Talos_Configuration_and_Machine_Management_Design.md#112-api-resource-boundaries),
[§6.2](../design/Talos_Configuration_and_Machine_Management_Design.md#62-fragments-profiles-and-assignments).

A record is **immutable** (insert-only) or **mutable** (updated in place under a
revision or a fence). No record of either kind is deleted in the PoC: design
§7.8 item 1 derives that no release, artifact, source revision or dependency
record is deleted without a reference graph, and this contract extends that to
every table **(choice §17.9)**. The only in-place clearing is compilation's: a
claim's payload is set to `NULL` on release or abandonment.

Immutable tables carry a trigger that refuses `UPDATE` and `DELETE`, with a test
that the trigger fires **(choice §17.3)**.

| Entity | Kind | Holds | Owner of semantics |
| --- | --- | --- | --- |
| Cluster | mutable, revisioned | name, endpoint, contract, status | this contract |
| Machine | mutable, revisioned | `mch` id, hardware evidence, cluster membership, current freeze (projected from freeze facts) | this contract; freeze is execution and recovery's |
| Fragment | mutable head | name, layer, scope (a cluster or the library), pointer to the head revision | this contract |
| FragmentRevision | immutable | sanitized YAML text, canonical parsed form, declarations, reference rows, author | compilation §2, §5 |
| Profile / ProfileRevision | mutable head / immutable | ordered fragment revision ids | this contract |
| Assignment / AssignmentRevision | mutable head / immutable | per machine: selected profiles and fragments per layer | this contract |
| ImportBaseRevision | immutable | a machine's sanitized imported document with references, baseline ciphertext and its keyed digest | compilation §2.3, §6 |
| Draft | mutable, revisioned | change set: entries and their base head revisions | this contract |
| Release, ReleaseMachine | immutable | the compilation §11 unit; the release covers one cluster and a set of its machines | compilation §11; this contract |
| Dependency record | immutable | effective and reproduction dependencies, encryption dependency with the key identity | compilation §9 |
| DependencyStatus | mutable | last classification per dependency and when first seen `retained` | design §7.6, §7.8 |
| Staging claim | mutable, fenced | state, owner, owner generation, lease, expiry, payload | compilation §3 |
| MachineState | mutable, revisioned | Desired, Applied (with source), baseline revision | execution and recovery (Desired, Applied and Observed) |
| Observation | immutable | observation revision, identity, digest, health | execution and recovery |
| Plan | immutable | the binding | execution and recovery (plan binding) |
| Approval | immutable | plan, approver, role, epoch, self-approval mark | execution and recovery; §10.5 |
| Approval revocation, identity revocation | immutable | who, when, epoch, reason | §10.4; execution and recovery |
| Operation | mutable projection, fenced | kind, state, owner, owner generation, owner epoch, timeline revision | §8; execution and recovery for `apply-config` |
| TimelineEvent, Attempt | immutable | append-only facts of one operation | execution and recovery |
| Scope release, freeze fact | immutable | per machine, with epoch | execution and recovery |
| Principal | mutable | human (`iss`, `sub`) or service identity; revoked flag | §10 |
| AutomationToken | mutable | token id, SHA-256 of the secret, roles, owner, expiry, epoch, revoked time | §10.2 |
| Act | immutable | every mutating act: principal, role, action, subject, epoch, time | §10.5 |
| IdempotencyRecord | immutable | key, fingerprint, stored response | §7 |
| InstallationState, RecoveryEpoch | mutable singleton / immutable | current epoch, recovery mode | §12 |
| schema_migrations | immutable | applied migrations | §11 |

### 3.1 Sources, drafts and heads

A fragment, profile or assignment has one **head**: a mutable pointer to its
current published revision, with a head revision counter and token (§4.1).
Heads advance only in the publication commit (§6.2). Every edit, published or
not, is stored as an immutable revision first; design §7.2's "mutable drafts
remain distinct from releases" is met by the draft being the only mutable
record of unpublished work.

A **draft** belongs to one cluster. It holds entries, each naming a head (or a
new fragment, profile or assignment), the proposed revision (or removal) and
the head revision the author edited from, its **base**. A draft update is
compilation's draft transaction: it inserts the new immutable revision and its
reference rows, sets the entry, and advances the draft's revision (§5, T1).
A draft ends `published` (by §6.2) or `discarded`; neither state accepts
further edits.

### 3.2 The import base

A machine's import base (compilation §6) is not a head. It is the import base
revision of the machine's `Applied` release **(choice §17.5)**. `Applied`
changes only through a completed operation or an adoption record (execution and
recovery, Desired, Applied and Observed), so the import base changes only when
a release carrying a new import base becomes `Applied`. Compilation §6 names
that base the "latest accepted drift adoption"; reading it from `Applied` is
this contract's choice. A draft built from an import or an adoption carries
its new import base revision as an entry. A machine with no `Applied` has no
import base; the only draft that can cover it is one carrying an import base
revision.

## 4. Revisions and optimistic concurrency

Design: [§7.2](../design/Talos_Configuration_and_Machine_Management_Design.md#72-relational-revision-model-and-backend-contract),
[§11.1](../design/Talos_Configuration_and_Machine_Management_Design.md#111-api-style).

Every mutable record carries an integer `revision`, incremented by every write,
and every write is a compare-and-set:

```sql
UPDATE draft
   SET revision = revision + 1, etag_token = $new_token, ...
 WHERE id = $id AND revision = $expected;
-- 0 rows: stale write, refused
```

This is the mechanism DB measured: 20 rounds of 8 writers holding the same
read gave 20 accepted revisions and 140 conflicts, and the control without the
revision predicate lost 140 updates
([DB §4.1](../design/research/20260924-database-semantics.md#41-s1-stale-revision-rejection),
rows 001–003).

### 4.1 ETags

A revision number leaves the database only inside a strong ETag that also
carries a random token, replaced on every write **(choice §17.2)**:

```text
ETag: "5-shw6tpirbqvgj3qjuv2hicf6vm"
```

The server compares the whole value. After a restore, a record's revision 5
can be issued again with other content (DB §4.7); its token will differ, so an
ETag a client saw before the restore never matches.

Mutations of a draft require `If-Match` with the draft's current ETag. A
mismatch is `412 precondition-failed`; a missing header is
`428 precondition-required`. The handler compares `If-Match` before it runs
ingestion, so a stale edit creates no provider generation, and T1 compares it
again under the draft's lock (§5). Heads, clusters and machines are not written
through `If-Match` by the API in the PoC: heads move only by publication
(§6.2), which checks them itself.

### 4.2 Concurrency conflicts in publication

Publication rejects stale input (design §7.4 step 4) in two forms, both
`409 stale-input`, which names each head with its expected and actual head
revision:

- a head the draft changes has moved since the draft's base: another
  publication changed the same fragment, profile or assignment;
- a head the release uses unchanged has moved since compilation's snapshot
  (compilation §6 step 1).

The second check covers inputs the draft did not touch **(choice §17.4)**: DB
row 011 is exactly that case, a release committed on a superseded source. The
author's remedy is a new draft revision from the current heads; how the edit
flow offers that is ginsys/bronzeward#23's.

## 5. Transaction boundaries

Design: [§7.2](../design/Talos_Configuration_and_Machine_Management_Design.md#72-relational-revision-model-and-backend-contract),
[§7.4](../design/Talos_Configuration_and_Machine_Management_Design.md#74-publication-without-distributed-transactions).

Rules for every transaction:

1. **No provider or network I/O inside a transaction** **(choice §17.7)**.
   Provider calls, compilation, Talos requests and response writing happen
   between transactions. A paused server kept a publication's transaction open
   for its whole pause (DB row 059), and every lock it held with it.
2. **Lock what you check.** A value a transaction's decision depends on is
   either locked by the transaction (`FOR SHARE` to hold it, `FOR UPDATE` to
   change it) or tested in the predicate of the conditional write that acts
   on it. Each of the four PostgreSQL clauses of
   [DB §6.3](../design/research/20260924-database-semantics.md#63-criterion-3-backend-specific-limitations-and-costs)
   has a control that failed without it (rows 011, 018, 020, 026), and each is
   required here with a test that can fail (design §7.7 consequences).
3. **Default isolation, read committed.** It is the only level measured
   (DB §7). Correctness rests on rule 2, not on the isolation level.
4. **Server clock.** Leases, expiries and recorded times use the database's
   `now()`, never the caller's clock. DB's leases used the client clock and
   left skew untested (DB §7); compilation §3.2 makes the same choice for
   claims.
5. **Lock order.** Installation state, then machine rows by id, then heads by
   id, then the draft, then operations by id. A detected deadlock aborts the
   transaction, which is retried whole, at most three times, then fails
   `503 transient-conflict` **(choice §17.7)**.
6. **Commit-unknown is resolved by reading, not by assuming.** A connection
   error after `COMMIT` was sent leaves the outcome to the server. In DB row
   061 the release committed after the client was gone, and an idempotent
   retry keyed by name and content returned the existing release
   ([DB §4.2](../design/research/20260924-database-semantics.md#42-s2-all-or-nothing-publication)).
   Every transaction that can be retried has such a key: an idempotency key
   (§7), a natural key (§7.3) or a fence (§5.1).

The transactions this contract defines or constrains:

| # | Transaction | Locks and checks | Writes |
| --- | --- | --- | --- |
| T1 | Draft update (compilation's draft transaction) | installation state `FOR SHARE` (not in recovery mode); draft `FOR UPDATE`, revision equals `If-Match`; claim owner and generation in the release's conditional `UPDATE` | revision rows, reference rows, draft entry, draft revision, claim `released`, idempotency record, act |
| T2 | Publication request | installation state `FOR SHARE`; draft `FOR UPDATE`, `open`, revision equals `If-Match` | publish operation `queued`, idempotency record, act |
| T3 | Publication commit (§6.2) | as §6.2 | release rows, heads, Desired, draft `published`, operation `succeeded` |
| T4 | Plan creation | release published; ER's binding checks | plan, operation `planned`, idempotency record, act |
| T5 | Approval, approval revocation, identity revocation | plan state; principal row `FOR UPDATE` for identity revocation | approval (with epoch and self-approval mark) or revocation, act |
| T6 | Commitment, attempt, adoption record | execution and recovery; with §1.2 items 1–3 | execution and recovery |
| T7 | Timeline append | operation row `FOR UPDATE` | event at `timeline_revision + 1` |
| T8 | Job claim and completion | §5.1 | operation owner fields |
| T9 | Recovery-mode entry | installation state `FOR UPDATE` | §12.2 |
| T10 | Migration | `pg_advisory_xact_lock` | §11 |

T7 allocates a timeline revision under the operation's row lock, so that for
one operation revision order is commit order. DS found that taking an
observation basis as the highest committed revision, without that lock, is
unsound under concurrent writers
([DS §7](../design/research/20260925-dispatch-safety.md#7-limits)); this
allocation removes the case for the timeline, and execution and recovery's
observation ordering relies on it.

### 5.1 Fences and claims

Every fenced record (operation ownership, job claims, staging claims) carries
an owner, an owner generation and the epoch identity in which that generation
was issued. A takeover or claim and every owner transition are single
conditional `UPDATE`s:

```sql
-- takeover (DB §4.4 row 015)
UPDATE operation
   SET owner = $new, owner_gen = owner_gen + 1, owner_epoch = $current_epoch
 WHERE id = $id AND owner_gen = $seen_gen;

-- an owner's own transition, after reading installation_state FOR SHARE
UPDATE operation
   SET ...
 WHERE id = $id AND owner = $me AND owner_gen = $my_gen
   AND owner_epoch = $my_epoch AND $my_epoch = $current_epoch;
```

The ownership check is part of the write, never a prior `SELECT`: the
check-then-insert control recorded a stale attempt
([DB §4.4](../design/research/20260924-database-semantics.md#44-s4-ownership-transitions),
row 018). The epoch term is §12's: after recovery-mode entry no token issued
before it matches, whatever generation the restored row holds.

A job claim re-checks eligibility in the `UPDATE`'s own predicate:

```sql
UPDATE operation
   SET state = 'running', owner = $me, owner_gen = owner_gen + 1,
       owner_epoch = $current_epoch, lease_until = now() + $lease
 WHERE id = (SELECT id FROM operation
              WHERE state = 'queued' AND kind = $kind
              ORDER BY seq LIMIT 1)
   AND state = 'queued';
```

Without the re-check, 309 of 400 jobs were claimed more than once and one was
completed twice ([DB §4.5](../design/research/20260924-database-semantics.md#45-s5-queue-claims),
row 020). A claimer whose lease lapsed is superseded, and its late completion
is refused at the newer generation (row 021). A fence coordinates workers that
use the database; it stops a stale worker's commit, not its work outside the
database (DS §7).

## 6. Publication protocol

Design: [§7.4](../design/Talos_Configuration_and_Machine_Management_Design.md#74-publication-without-distributed-transactions),
[§7.8](../design/Talos_Configuration_and_Machine_Management_Design.md#78-poc-retention-and-recovery-policy).

### 6.1 Steps

| Design §7.4 step | Where | Provider writes | Database writes |
| --- | --- | --- | --- |
| 1. Snapshot, select or create generations, pin | compilation §6 steps 1–2, in a read-only transaction | none: generations already exist from ingestion (compilation §2.3 step 6) | none |
| 2. Check dependencies, resolve, compose, validate | compilation §6 steps 3–8, outside any transaction | none | none |
| 3. Redact, encrypt, record dependencies | compilation §8, §9, §11 | none: Transit encryption keeps no state; the compiler cannot create a key (KL §7 item 1; row 037) | none |
| 4. Atomic commit, rejecting stale input | T3, §6.2 | none | the whole release, or nothing |
| 5. Available for planning | after T3 commits | none | none; publication never authorizes dispatch |

Publication writes nothing to the provider. The only provider objects
Bronzeward creates in the PoC are ingestion's generations, created before the
draft transaction that references them (compilation §2.3). A key version is
created by the OpenBao administrator, outside Bronzeward (design §7.5).

### 6.2 The commit transaction (T3)

```sql
BEGIN;
SELECT current_epoch, recovery_mode FROM installation_state FOR SHARE;
  -- recovery_mode false; current_epoch equals the job's owner_epoch
SELECT ... FROM operation WHERE id = $op FOR UPDATE;
  -- owner, owner_gen, owner_epoch are this worker's (§5.1)
SELECT ... FROM machine WHERE id = ANY($covered) ORDER BY id FOR SHARE;
SELECT head_revision FROM <head tables>
 WHERE id = ANY($changed) ORDER BY id FOR UPDATE;   -- equal to the draft's base
SELECT head_revision FROM <head tables>
 WHERE id = ANY($unchanged) ORDER BY id FOR SHARE;  -- equal to the snapshot
SELECT state, revision FROM draft WHERE id = $draft FOR UPDATE;
  -- open, revision equals the bound draft revision
-- for each machine whose assignment head is in $changed:
--   no operation on its scope is committed, sending, verifying or unresolved
INSERT INTO release ...;             -- unique (draft_id, draft_revision)
INSERT INTO release_machine ...;     -- per machine: ciphertext, digest, review
INSERT INTO release_source ...;      -- every revision and head revision used
INSERT INTO dependency ...;          -- both records and the encryption dependency
UPDATE <head> SET head_revision_id = ..., head_revision = head_revision + 1,
                  etag_token = ...
 WHERE id = ... AND head_revision = $base;
UPDATE machine_state SET desired_release = $release, revision = revision + 1
 WHERE machine_id = ANY($covered);
UPDATE draft SET state = 'published', release_id = $release,
                 revision = revision + 1 WHERE id = $draft;
UPDATE operation SET state = 'succeeded', result_release = $release ...;
INSERT INTO timeline_event ...;      -- T7 rules
COMMIT;
```

Any failed comparison rolls the transaction back; a separate transaction then
records the publish operation `failed` with the error of §9.4. `FOR SHARE` on
the unchanged heads is the clause without which DB row 011 committed a release
on a superseded source; row 010 is the same race with the lock, which made the
writer wait. Atomicity held
for an injected error, a client kill, a server kill and a network partition
with the transaction open (rows 006–008, 058, 060). The machine-scope check is
the rule execution and recovery states for any assignment change; §1.2 item 2
makes it race-free.

The release's covered machines are selected as their `Desired` release in the
same transaction **(choice §17.6)**. Design §11.2 describes a release as
"published cluster/machine desired state" and execution and recovery defines
Desired as the release recorded as selected; neither says when it is selected.
Selection is not authorization: dispatch still needs a plan and an approval.

The release's natural key is `(draft_id, draft_revision)`
**(choice §17.10)**. A release carries a content digest over its metadata and
each machine's configuration digest, not over ciphertext. A second commit for
the same draft revision, after a commit-unknown or from a second worker, finds
the unique key taken; it returns the existing release if the digests match, as
DB row 061 returned `existing=true`, and otherwise fails `409 conflict`, as row
009 refused the same name with different content.

### 6.3 Partial publication

Two directions, each walked in §13.2 and §13.3:

- **Provider write succeeded, database commit failed.** Only ingestion writes
  the provider. Its generations exist and no committed draft references them:
  unused provider objects in design §7.4's sense. Nothing in the database
  refers to them, so nothing can resolve to them.
- **Database commit succeeded, provider state absent.** Bronzeward's order
  (provider write, then the database commit that references it) never
  produces this. A provider restored to an older snapshot does: it lost
  generations created after that snapshot
  ([PC §4](../design/research/20260924-provider-capability-comparison.md#4-the-matrix),
  row 083), and a provider older than the database fails both applying and
  regenerating the newer releases
  ([KL §3.2](../design/research/20260924-key-loss-restoration.md#32-what-each-case-showed),
  case H). The database then references an object the provider does not hold.
  The dependency monitor reads a 404, which stays `unknown`; because
  `DependencyStatus` records the dependency as `retained`, the change alerts at
  once as a regression (design §7.8 item 3;
  [RC §6.4](../design/research/20260924-retention-metadata-classification.md#64-criterion-4-provider-limits-and-the-alert-policy-the-evidence-supports)).
  Compilation refuses a dependency that is not `retained` (compilation §6
  step 3), and the release's applicability is execution and recovery's
  use-time check.

### 6.4 Orphans

An **orphan** is a provider generation under Bronzeward's generation path that
no committed reference row names. The sources, all from ingestion:

1. an ingestion refused or interrupted after compilation §2.3 step 6, whose
   claim is `abandoned`;
2. an ingestion whose draft transaction failed or was never reached;
3. a database restored to a snapshot older than the provider's, which rewinds
   away the claims and reference rows of later ingestions (KL case G shows the
   same for release records).

Each generation path is `gen/<cluster>/<claim id>/<value id>`, both components
application-generated random identifiers (§2) **(choice §17.8)**. The path is
therefore never reissued by a restore, and every orphan is attributable to the
claim that created it, or to a claim the database no longer holds.

Handling in the PoC:

- **Never deleted, never destroyed.** Design §7.8 item 1: Bronzeward removes no
  provider object, and cleanup would need the reference graph of design §7.4.
- **Never reattached.** A later ingestion mints new names (compilation §5.1);
  no orphan is adopted into a new draft, even after a restore
  removed the draft that referenced it. The input is ingested again
  (compilation §3.5) **(choice §17.8)**.
- **Reported.** An orphan report lists generation paths whose claim component
  names an `abandoned` claim, or no claim at all, with the claim's state and
  times. It reads provider metadata under the metadata identity and names only
  paths, never values. Whether that identity may list the generation tree was
  not measured (PC measured metadata reads, not listing); if it may not, the
  report covers abandoned claims only, from the claim's own recorded paths.

## 7. Idempotency

Design: [§11.1](../design/Talos_Configuration_and_Machine_Management_Design.md#111-api-style),
[§12.5](../design/Talos_Configuration_and_Machine_Management_Design.md#125-durable-operations-and-uncertain-outcomes).

### 7.1 The key

Every mutating request (`POST`, `PUT`, `DELETE`) carries an `Idempotency-Key`
header: 16 to 128 characters of `[A-Za-z0-9_-]`. A missing key is
`428 idempotency-key-required` **(choice §17.9)**; design §11.1 asks for keys on
"every mutating operation that might be retried by clients or proxies", and any
of them can be.

A key is scoped to the authenticated principal: two principals may use the same
key independently. The record stores:

| Field | Content |
| --- | --- |
| `principal_id`, `key` | unique together |
| `fingerprint` | method, route template, path parameters, `If-Match` and the canonical JSON body (RFC 8785), hashed |
| `epoch` | the epoch identity at commit |
| `status`, `location`, `etag`, `body` | the response to replay; never a secret value (design §11.1) |
| `operation_id` | for a `202`, the operation it created |

The fingerprint is SHA-256, except for a route whose body can carry
unextracted input (a draft source update or an ingestion request): its
fingerprint is HMAC-SHA-256 under compilation's digest key, computed inside the
ingestion package (compilation §4.1). An unkeyed digest of a low-entropy
secret would be an offline guessing oracle
([E1 §7](../design/research/20260922-secret-ingress-extraction-before-persistence.md#7-limits);
compilation §4.1). A request body is never stored, not even in a transaction
later rolled back: a rolled-back row still reached the write-ahead log
([E1 §4.4](../design/research/20260922-secret-ingress-extraction-before-persistence.md#44-the-forbidden-design-measured)).

### 7.2 Behavior

The record is inserted by the same transaction that commits the request's
effect (T1–T5), and by no other **(choice §17.9)**. A refused request commits no
effect and no record, so a retry is evaluated afresh against current state;
since the refusal changed nothing, re-evaluating it cannot duplicate anything.

| Situation | Response |
| --- | --- |
| New key | executed; record committed with the effect |
| Same key, same fingerprint, record exists | the stored status, headers and body, with `Idempotent-Replayed: true`; nothing executes |
| Same key, other fingerprint | `422 idempotency-key-reused` |
| Same key while the first request's transaction is open | waits on the unique index; then replays if the first committed, or executes if it rolled back |
| Same key after the request that used it was refused | executed afresh |
| Same key after a restore that removed the record | executed afresh (§12.4) |

The waiting row follows PostgreSQL's unique-index semantics and is not
measured: DB row 013 retried the same key sequentially and got the first
operation back with `created=false`
([DB §4.3](../design/research/20260924-database-semantics.md#43-s3-unique-operation-intent)),
but no row raced two requests with one key.

Records are kept for the PoC (§3). An idempotency key prevents duplicate
intent in the manager; it does not make a remote effect exactly once (design
§12.5).

### 7.3 Natural keys

Some effects also have a key of their own, which holds even when a client
retries with a new idempotency key:

| Effect | Natural key | On a second request |
| --- | --- | --- |
| Release | `(draft_id, draft_revision)` | the existing release if the digests match, else `409 conflict` (§6.2) |
| Publish operation | at most one `queued` or `running` per `(draft_id, draft_revision)`, by partial unique index | the existing operation, `202` |
| Operation | one per plan | the plan's operation |
| Approval | at most one unrevoked approval per plan (design §13.7 item 3) | `409 conflict` |
| Active operation per machine scope | partial unique index over `committed`, `sending`, `verifying`, `unresolved` | execution and recovery's refusal (DB row 012; DS row 010) |

## 8. Asynchronous operations

Design: [§11.1](../design/Talos_Configuration_and_Machine_Management_Design.md#111-api-style),
[§15.2](../design/Talos_Configuration_and_Machine_Management_Design.md#152-operation-timeline).

Long work is an **operation**: a durable record with an `op` identifier,
returned in `202 Accepted` with `Location: /api/v1/operations/<id>`. The
identifier is generated and committed with the idempotency record before the
response is written, so a retry of the request returns the same handle.

| Kind | Created by | States |
| --- | --- | --- |
| `publish` | `POST /drafts/{id}/publications` | job states (§8.2) |
| `ingest` | `POST /ingestions` (import, drift adoption) | job states (§8.2) |
| `apply-config` | `POST /plans` | execution and recovery's operation states |
| `adopt` | `POST /plans` with `operation: adopt` | execution and recovery's adoption rules |

### 8.1 Operations of a plan

The operation of a plan is created with the plan, in state `planned`
**(choice §17.11)**. Execution and recovery's state table begins at `planned`
("the immutable plan exists but is not authorized"), and design §11.1's
example reads the plan's operation with `GET /api/v1/operations?plan=...`.
Design §11.1 also says the controller dispatches the plan "and the client
follows the operation it creates"; the alternative reading creates the
operation at the commitment. Either way there is one operation per plan: DS row
009 refused a second executor on "an operation for plan A exists". Here the
commitment is the conditional transition `approved` → `committed`, which a
second executor fails.

### 8.2 Job states

| State | Meaning |
| --- | --- |
| `queued` | Accepted; no worker holds it. |
| `running` | A worker holds it under a fence and lease (§5.1). |
| `succeeded` | Terminal. `result` names the release or draft produced. |
| `failed` | Terminal. `error` is a problem document (§9.4). |

A worker whose lease lapses is superseded by the next claim; its late commit
is refused by the fence. A `publish` job is safe to run again, because its
commit is keyed by draft revision (§6.2). An `ingest` job is not re-run: its
input was held in memory or in staging, and compilation's claim rules decide
between takeover and abandonment (compilation §3.4, §3.5). A lapsed `ingest`
job whose claim is abandoned fails with `ingestion-abandoned`
**(choice §17.12)**.

Recovery-mode entry fails every `queued` or `running` job with
`recovery-mode-entered` (§12.2) **(choice §17.12)**.

### 8.3 The resource and its progress

```json
{
  "id": "op_zcfwgr7trb76z3zmsndrnk5mcm",
  "kind": "publish",
  "state": "running",
  "epoch": "ep_bqeknkmarvikuy7ofil2okekgi",
  "timelineRevision": 3,
  "subject": {"draft": "drf_2rmpezm5rfx47azsgmp66z457a", "draftRevision": 7},
  "createdBy": {"principal": "idn_5u4k6llt7jsktfhcfv35xmdetu",
                "role": "publisher"},
  "createdAt": "2026-09-26T09:14:02Z",
  "result": null,
  "error": null
}
```

Progress is the operation's timeline. `GET /api/v1/operations/{id}/events`
returns it as a cursor-paginated JSON list, or as Server-Sent Events when the
request accepts `text/event-stream`. Each event's SSE `id` is
`<epoch>:<timelineRevision>`. A client resumes with `Last-Event-ID`; an id from
another epoch cannot be resumed, so the server sends a `reset` event and the
timeline from its start. Polling the resource is always supported (design
§11.1). WebSocket is not offered in the PoC.

## 9. API

Design: [§11](../design/Talos_Configuration_and_Machine_Management_Design.md#11-northbound-web-api),
[§13.7](../design/Talos_Configuration_and_Machine_Management_Design.md#137-poc-identity-and-approval-policy).

### 9.1 Conventions

- JSON over HTTPS, under `/api/v1`. Unknown request fields are refused.
- **Deprecation policy** **(choice §17.14)**: within `v1`, fields and routes
  are added, never removed or changed in meaning. A route due for removal
  answers with a `Deprecation` header for at least one minor release first. A
  breaking change is a new version prefix.
- **Pagination**: every collection takes `limit` (default 50, at most 500) and
  an opaque `cursor`, and answers with `next` when more remain. A cursor
  carries the epoch identity; a cursor from another epoch is
  `400 cursor-invalid` **(choice §17.14)**.
- **Redaction**: responses carry sanitized sources, reference names and
  compilation's redacted review data; never a secret value, ciphertext or
  baseline (design §11.1).
- Every response carries `Bronzeward-Epoch` with the current epoch identity,
  and `Bronzeward-Recovery-Mode: true` while recovery mode is in effect.

### 9.2 Resources and routes

"Any role" means any authenticated principal holding at least one role: every
grant confers read (design §13.7 item 2). A route marked *EaR* has its
semantics in execution and recovery; this contract fixes its route, role,
idempotency and conflict behavior.

| Route | Result | Roles |
| --- | --- | --- |
| `GET /clusters`, `/clusters/{id}`, `/machines`, `/machines/{id}`, `/machines/{id}/observations` | 200 | any role |
| `GET /fragments[/{id}]`, `/fragments/{id}/revisions`, `/fragment-revisions/{id}`; the same for profiles and assignments | 200 | any role |
| `GET /drafts[/{id}]`, `/releases[/{id}]`, `/releases/{id}/machines/{m}/review` | 200 | any role |
| `GET /plans[/{id}]`, `/approvals/{id}`, `/operations[/{id}]`, `/operations/{id}/events`, `/acts`, `/recovery` | 200 | any role |
| `POST /ingestions` (import or drift adoption of a machine's configuration) | 202, `ingest` | `author`, human only (§10.3) |
| `POST /clusters`, `POST /machines` (inventory for an existing cluster) | 201 | `author`, human only (§10.3) |
| `POST /drafts` | 201, ETag | `author` |
| `PUT` or `DELETE /drafts/{id}/fragments/{name}`, `/profiles/{name}`, `/assignments/{machine}` | 200, ETag | `author`; `If-Match` |
| `POST /drafts/{id}/discard` | 200 | `author`; `If-Match` |
| `POST /drafts/{id}/publications` | 202, `publish` | `publisher`; `If-Match` |
| `POST /plans` | 201 | `publisher` (EaR) |
| `POST /plans/{id}/cancellations` | 200 | plan creator, `approver`, `recovery-admin` (EaR) |
| `POST /plans/{id}/approvals` | 201 | `approver`, human only (EaR) |
| `POST /approvals/{id}/revocations` | 201 | `approver`, `recovery-admin` (EaR) |
| `POST /machines/{id}/freezes` | 201 | `author`, `publisher`, `approver`, `recovery-admin` (EaR) |
| `POST /machines/{id}/unfreezes` | 201 | `approver` (EaR) |
| `POST /recovery/entries`, `/recovery/exits`, `/recovery/scopes/{machine}/releases` | 201 | `recovery-admin`, human only (EaR; §12) |
| `POST /operations/{id}/resolutions` | 201 | `recovery-admin`, human only (EaR) |
| `POST /identity-revocations` | 201 | `recovery-admin`, human only (§10.4) |

Every `POST`, `PUT` and `DELETE` needs an `Idempotency-Key` (§7). There is no
route that dispatches, and none that issues, lists or revokes automation tokens
or grants roles: those requests reach no handler and answer `404` (design
§13.7 items 1 and 2).

An adoption approval is requested as a plan with `"operation": "adopt"`, bound
and approved as execution and recovery's drift section describes; its
operation sends nothing and ends in the adoption record **(choice §17.15)**.

### 9.3 Examples

Publishing a draft, with a replay after a lost response:

```http
POST /api/v1/drafts/drf_2rmpezm5rfx47azsgmp66z457a/publications
Authorization: Bearer <automation token>
Idempotency-Key: 2f4c9a1e-6b0d-4d7a-9c3e-58a1f0e2b7d4
If-Match: "7-shw6tpirbqvgj3qjuv2hicf6vm"

{}

HTTP/1.1 202 Accepted
Location: /api/v1/operations/op_zcfwgr7trb76z3zmsndrnk5mcm
Bronzeward-Epoch: ep_bqeknkmarvikuy7ofil2okekgi

{"id": "op_zcfwgr7trb76z3zmsndrnk5mcm", "kind": "publish", "state": "queued", ...}

(the client lost that response and sends the same request again)

HTTP/1.1 202 Accepted
Location: /api/v1/operations/op_zcfwgr7trb76z3zmsndrnk5mcm
Idempotent-Replayed: true
```

Planning and approving, as design §11.1 illustrates, with this contract's
identifiers:

```http
POST /api/v1/plans
Idempotency-Key: 9d0e7b36-1c42-4f0a-8e55-c3a7d9f1b260

{"releaseId": "rel_fgqvcvz3ck7h7234ljgdbzsj6m",
 "machine": "mch_tqhcznunhyle4hnxru5hkt35uq",
 "operation": "apply-config", "mode": "no-reboot"}

HTTP/1.1 201 Created
Location: /api/v1/plans/pln_f645lvrsgsehfn6fuboigpcawy

POST /api/v1/plans/pln_f645lvrsgsehfn6fuboigpcawy/approvals
Idempotency-Key: 5b8f2c07-3e19-4a6d-b1f4-7d20e8c93a51

{"planRevision": 1}

HTTP/1.1 201 Created
Location: /api/v1/approvals/apr_2ztr33rjgnabf5zxbwwf7c47vy

{"id": "apr_2ztr33rjgnabf5zxbwwf7c47vy",
 "plan": "pln_f645lvrsgsehfn6fuboigpcawy",
 "approver": {"principal": "idn_6woutisn7uensexh3kk2qlz6ma", "role": "approver"},
 "epoch": "ep_bqeknkmarvikuy7ofil2okekgi",
 "selfApproval": {"marked": true, "reasons": ["created-plan", "authored-change"]}}
```

### 9.4 Errors

Errors are `application/problem+json` (RFC 9457) **(choice §17.14)**. `type`
is `urn:bronzeward:problem:<code>`; `detail` names resources and paths, never a
value; `instance` is the request's identifier, also written to the server log.

```json
{
  "type": "urn:bronzeward:problem:stale-input",
  "title": "An input changed after it was read",
  "status": 409,
  "detail": "Publication refused: 1 head moved.",
  "instance": "req_nzeb4lwqu3iaa7pu6p22ninopi",
  "conflicts": [
    {"head": "frg_rgkebwvneg6mxhid62gec5difi",
     "expected": "3-hnztg6eb5jepsmpyarveova3be",
     "actual": "4-4ycffhy7bf4o2w6pz5b4r75hmu"}
  ]
}
```

| Status | Code | When |
| --- | --- | --- |
| 400 | `invalid-request`, `cursor-invalid` | malformed body, unknown field, bad cursor |
| 401 | `unauthenticated` | no credential, or one that fails §10; with `WWW-Authenticate: Bearer error="invalid_token"` |
| 403 | `forbidden` | no qualifying role; the body names the roles that would qualify |
| 403 | `identity-revoked` | the principal was revoked (§10.4) |
| 404 | `not-found` | no such resource or route |
| 409 | `stale-input` | a publication input moved (§4.2) |
| 409 | `conflict` | the resource is in a state that refuses the act (a published draft, an approved plan, a second approval) |
| 409 | `scope-busy` | an assignment change while an operation holds the machine scope |
| 409 | `recovery-mode-active` | a mutation refused during recovery mode (§12.2) |
| 412 | `precondition-failed` | `If-Match` does not match |
| 422 | `validation-failed` | compilation refused the input; paths and rule, never values (compilation §13) |
| 422 | `idempotency-key-reused` | same key, other request (§7.2) |
| 428 | `precondition-required`, `idempotency-key-required` | `If-Match` or `Idempotency-Key` missing |
| 503 | `dependency-unavailable` | the provider is sealed or unreachable; nothing was committed |
| 503 | `transient-conflict` | deadlock retries exhausted (§5) |
| 503 | `schema-mismatch` | never served: the server does not start (§11) |

An asynchronous operation that fails carries the same problem document in its
`error` field.

## 10. Authentication and authorization

Design: [§13.7](../design/Talos_Configuration_and_Machine_Management_Design.md#137-poc-identity-and-approval-policy),
[§13.3](../design/Talos_Configuration_and_Machine_Management_Design.md#133-enrolment-security).

Every request authenticates; there is no anonymous route besides a liveness
probe that returns no data. A principal is a human, identified by the OIDC
issuer and subject, or a service identity. No human or automation principal
has an OpenBao identity (design §13.7 item 1, derived).

### 10.1 Humans: OIDC

Bronzeward is an OAuth 2.0 resource server. A human's client obtains a JWT
access token from the operator's identity provider and sends it as
`Authorization: Bearer`. Each request is verified on its own;
Bronzeward keeps no server session **(choice §17.16)**:

- signature against the issuer's published keys, asymmetric algorithms only
  (`none` and HMAC algorithms refused);
- `iss` equal to the configured issuer, `aud` containing the configured
  audience, `exp` and `nbf` with at most 60 seconds' skew;
- `exp - iat` at most the configured maximum lifetime, default 15 minutes; a
  longer-lived token is refused;
- the subject is not revoked (§10.4).

The groups claim is mapped to roles in deployment configuration (design §13.7
item 2):

```yaml
auth:
  oidc:
    issuer: https://idp.example.test/realms/ops
    audience: bronzeward
    groupsClaim: groups
    maxTokenLifetime: 15m
  roles:
    viewer: [bw-viewers]
    author: [bw-authors]
    publisher: [bw-publishers]
    approver: [bw-approvers]
    recovery-admin: [bw-recovery]
  deniedSubjects: []    # §10.4
```

A human's roles are those the token's groups map to, evaluated per request.
This answers how an identity-provider change reaches Bronzeward (design §13.7,
"not settled here"): a disabled account or a removed group takes effect when
the client's current token expires, within the configured maximum lifetime.
For immediate effect, `recovery-admin` records an identity revocation (§10.4).

### 10.2 Automation: Bronzeward-issued tokens

A service identity has at most one valid token (design §13.7 item 1)
**(choice §17.17)**:

```text
bwt_<token id>.<secret>      token id: a tok identifier (§2)
                             secret: 256 bits, base64url
```

Only `SHA-256(secret)` is stored, as design §13.3 requires of enrolment tokens.
An unsalted digest is not a guessing target at 256 bits of randomness; the
guessing concern of compilation §4.1 applies to low-entropy values. The
comparison is constant-time.

The server-side command-line tool, run by the operator with database access,
is the only way to issue, rotate, revoke or list tokens; no API route exists
(design §13.7 item 2). It:

- creates a service identity with roles drawn only from `viewer`, `author` and
  `publisher`, refusing `approver` and `recovery-admin`; a check constraint
  refuses them too (design §13.7 item 2);
- records a responsible human principal for the identity
  **(choice §17.18)**, used by §10.5;
- sets a mandatory expiry, default 30 days, at most 90 **(choice §17.17)**;
- rotates by issuing a new token and revoking the old one in the same
  transaction, so one token is valid at a time;
- prints the token once, and writes an act record naming the operator as
  given to the tool.

A token issued before the current recovery epoch is refused (§12.3).

### 10.3 Authorization

Each route names its roles (§9.2). The check runs in the request handler
before any transaction, and again inside the transaction for acts whose
validity the database must hold: an approval checks that the approver is not
revoked, with the principal row read `FOR SHARE`.

When a principal holds several roles that qualify for a route, the act is
recorded under the first qualifying role in the route's listed order
**(choice §17.20)**; one person holding several roles still performs each act
under a single named role (design §13.7 item 2).

Human-only routes refuse automation even where a role would allow it:
approval, recovery and identity revocation because automation never holds
those roles (design §13.7 item 2); and, as an interim position, ingestion and
inventory creation, which require `author` held by a human
**(choice §17.22)**. Design §13.7 leaves "which role performs the privileged
ingestion that feeds an adoption" to an owner decision pending on
[ginsys/bronzeward#14](https://github.com/ginsys/bronzeward/issues/14), and
names no role for inventory records.

Drift **Ignore** is outside PoC scope (execution and recovery, supported
values) and has no route.

### 10.4 Revocation

**Identity revocation** is recorded by `recovery-admin` (design §13.7 item 4):
an immutable revocation row and the principal's `revoked` flag, in one
transaction that locks the principal row. From its commit:

- every approval that identity gave and that no attempt has used authorizes
  nothing: the commitment and attempt transactions read the approver's
  principal row `FOR SHARE` (execution and recovery, dispatch commitment);
- the identity cannot authenticate again: a revoked human subject is refused
  at §10.1, and a service identity's token is revoked with it
  **(choice §17.19)**. A revocation is permanent in the PoC; the person or
  service needs a new identity.

A restore can remove a revocation recorded after the backup. The deployment
configuration's `deniedSubjects` list survives a database restore; the
recovery procedure re-records the lost revocations and the operator adds the
subjects to that list (§12.3) **(choice §17.19)**.

**Losing a role** without an identity revocation is not acted on
**(choice §17.23)**. A human's roles are known only from the token presented
with a request, so a role removed at the identity provider is not observable
for approvals already given. Whether it should invalidate them is an owner
decision pending on ginsys/bronzeward#14; the available remedy is an identity
revocation.

Approval revocation is execution and recovery's; this contract records it as
an immutable row with its act (T5).

### 10.5 Recording every act

Every mutating request that commits writes an **act** row in the same
transaction: act id, principal, principal kind, role exercised, action, subject
identifiers, idempotency key, request id, epoch and server time (design §13.6,
§13.7 item 2). The operation timeline references the acts that touched it. Act
rows are readable by any role (design §13.7 item 2, `viewer`'s audit read) and
are never deleted.

An approval carries a **self-approval mark**, computed in the approval's
transaction. It is marked when any reason holds, and every reason that holds
is recorded **(choice §17.21)**:

| Reason | The approving identity | Basis |
| --- | --- | --- |
| `created-plan` | created the plan | design §13.7 item 3 |
| `published` | published the release | design §13.7 item 3, derived rule |
| `authored-change` | authored a revision the release's draft introduced | design §13.7 item 3, derived rule |
| `authored-reused` | authored a revision the release uses unchanged from an earlier release | edge case (a), left to this contract |
| `owned-automation` | is the responsible human (§10.2) of an automation identity that authored, published or planned it | edge case (b), left to this contract |

Both unsettled cases are marked as self-approval, the conservative reading: a
mark never blocks an approval (design §13.7 item 3 allows self-approval), and
an unmarked self-approval would hide it from the audit.

## 11. Migrations

Design: [§7.2](../design/Talos_Configuration_and_Machine_Management_Design.md#72-relational-revision-model-and-backend-contract),
[§14.2](../design/Talos_Configuration_and_Machine_Management_Design.md#142-operator-owned-database-and-vault-services).

The migrations are embedded in the binary, numbered, and forward-only. Each
runs as one transaction on one connection:

```sql
BEGIN;
SELECT pg_advisory_xact_lock(<constant key>);
SELECT 1 FROM schema_migrations WHERE version = $v;   -- present: skip
-- the migration's statements
INSERT INTO schema_migrations (version, name, checksum, applied_at)
VALUES ($v, $name, $checksum, now());
COMMIT;
```

This is DB's runner: a failed or killed migration left nothing of itself, and
four concurrent runners applied each version once under the lock; without the
lock, three of four failed on a catalog unique index
([DB §4.6](../design/research/20260924-database-semantics.md#46-s6-migrations),
rows 022–026).

Rules **(choice §17.24)**:

1. Migrations run only through an explicit migrate command, with the service
   stopped. Stopping it is an operator step; nothing prevents a server
   started against a schema mid-migration except rule 2.
2. The server refuses to start unless `schema_migrations` holds exactly the
   binary's migrations with matching checksums: an older schema, a newer
   schema and an edited migration are each refused with a message naming the
   versions.
3. A migration touches no provider and dispatches nothing.
4. A migration never rewrites existing rows of an immutable table. It may add
   tables, columns with constant defaults, indexes and constraints.
5. There is no downgrade. Undoing a migration means restoring a database
   backup, which is a restore (§12).

DB tested the engine properties only. No v1 migration tool, online migration
of a large table or downgrade was attempted
([DB §6.2](../design/research/20260924-database-semantics.md#62-criterion-2-intent-ownership-queue-claims-migrations-restored-state)).

## 12. Restored state and the recovery epoch

Design: [§14.6](../design/Talos_Configuration_and_Machine_Management_Design.md#146-explicit-recovery-mode-after-restoration),
[§7.7](../design/Talos_Configuration_and_Machine_Management_Design.md#77-poc-deployment-profile),
[§7.8](../design/Talos_Configuration_and_Machine_Management_Design.md#78-poc-retention-and-recovery-policy).

A restore rewinds data, identifiers and fence generations together; a token
issued after the snapshot is issued again and passes the fence (DB §4.7, row
027). Bronzeward does not assume it can detect a rollback (design §14.6); the
operator enters recovery mode after restoring either service (design §7.7 duty
6). This section is the persistence half of that: what the epoch is, what entry
writes, and what a restored state means for each record. The procedure after
entry is execution and recovery's. The whole mechanism is design, not
evidence: no investigation exercised restoration with an epoch (DS §7; FR §9
item 4).

### 12.1 The epoch

`InstallationState` is a single row: the current epoch's identity, whether
recovery mode is in effect, and the schema version. `RecoveryEpoch` rows are
immutable: identity, sequence number, entry time and entering principal.

An epoch has two values **(choice §17.25)**:

- an **identity**, 128 random bits (§2), minted at installation and at every
  recovery-mode entry;
- a **sequence number**, one more than the highest in the database, which is
  execution and recovery's counter.

Every comparison is equality with the current identity: an approval, a scope
release, a fence, a claim and an automation token are valid only if the epoch
they carry is the current one. The sequence number orders epochs for display
within one history and decides nothing.

This answers DB §9's question. An epoch stored in the database is rewound with
it, so a counter is "greater than any value in the restored state" but can
equal an epoch issued after the snapshot and lost by the restore. A random
identity cannot repeat, so no epoch needs a source that survives the restore.
That argument is inferred, not measured. What the epoch cannot do is notice a
restore after which nobody entered recovery mode: then no new epoch exists,
and a pre-restore token reissued by the rewound fence passes, exactly as in DB
row 027. That residual is design §14.6's, and only the operator's entry closes
it.

### 12.2 Entry

Entry runs as one transaction (T9), from the API (`recovery-admin`) or, for the
case design §7.7 duty 6 describes ("before normal startup"), from a
server-side command run against the database before the service starts
**(choice §17.26)**. It:

1. locks `InstallationState` `FOR UPDATE`, which waits for every transaction
   that read it `FOR SHARE`;
2. inserts a new epoch and makes it current, and sets recovery mode;
3. marks every staging claim from an earlier epoch `abandoned` and clears its
   payload (compilation §3.5);
4. fails every `queued` or `running` job with `recovery-mode-entered`
   (§8.2);
5. performs execution and recovery's entry effects: every operation in
   `committed`, `sending` or `verifying` becomes `unresolved`, and every
   machine scope's gate closes;
6. writes the act record.

From the commit, every fence and claim issued before it fails the epoch term of
§5.1, whatever generation it carries; every approval and scope release from an
earlier epoch authorizes nothing; and every automation token from an earlier
epoch is refused.

While recovery mode is in effect, the API refuses every mutation with
`409 recovery-mode-active`, except recovery acts, freezes, approval and
identity revocations, and plan cancellations, which only remove authority or
record recovery **(choice §17.13)**. Design §14.6 pauses mutation while
allowing observation; reads are unaffected. Leaving recovery mode and
releasing scopes are execution and recovery's.

### 12.3 What a restored state means, record by record

For a database restored to a backup taken at time *T*:

| Record | After the restore | Consequence |
| --- | --- | --- |
| Identifiers issued after *T* | absent, never reissued (§2) | a client's handle for a lost release or operation answers `404`, never another record |
| Revision numbers issued after *T* | reissued with new tokens | a pre-restore ETag answers `412` |
| Fence generations issued after *T* | reissued | refused after entry by the epoch term (§5.1) |
| Head advances, drafts and releases after *T* | heads rewound; drafts and releases absent | re-edited and republished; release records lost this way are KL case G |
| Staging claims | from before *T*, earlier epoch | abandoned at entry; inputs ingested again (compilation §3.5) |
| Ingestion generations after *T* | in the provider if its backup is newer | orphans (§6.4); not reattached |
| Approvals, scope releases | earlier epoch | authorize nothing; a plan needs an approval in the new epoch |
| Idempotency records after *T* | absent | a retry executes afresh (§12.4) |
| Identity revocations after *T* | absent | re-recorded by `recovery-admin`, and the subjects added to `deniedSubjects` |
| Automation tokens | earlier epoch | refused; reissued with the tool **(choice §17.27)** |
| Tokens revoked after *T* | valid again in the rows | refused anyway: earlier epoch |
| `DependencyStatus` | as at *T* | a dependency recorded `retained` and now answering 404 alerts at once as a regression (§6.3) |

The automation-token rule costs a reissue of every service identity's token
after each restore. It is the only way this contract finds to refuse a token
revoked after *T*, whose revocation the restore removed.

The provider may be restored too. The operator pairs the backups: the provider
backup no older than the database backup, the credential store matched to the
provider (design §7.8 item 5). A provider older than the database is KL case H
(§6.3); a database older than the provider is case G, whose later generations
become orphans.

### 12.4 Restore and idempotency

A request whose effect the restore removed also lost its idempotency record,
so its retry executes again. That is the intended result: the effect is gone,
and executing it again is the only way to have it. Its provider side is not
repeated: an ingestion retried after a restore mints new names and new
generations, and the first attempt's generations stay as orphans.

## 13. Worked examples

### 13.1 Concurrent edits

1. Draft `drf_2rmpezm5rfx47azsgmp66z457a` is at revision 7. Authors A and B
   both read ETag `"7-shw6tpirbqvgj3qjuv2hicf6vm"`.
2. A updates fragment `registries` with that `If-Match`. T1 locks the draft,
   finds revision 7, commits revision 8 with a new token; A gets `200` and the
   new ETag.
3. B's update carries the old ETag. The handler compares it before running
   ingestion and answers `412 precondition-failed`; nothing is persisted and no
   generation is created. Had A's commit landed while B's ingestion was
   already running, T1's own comparison would refuse B, and the generations
   B's ingestion created would be orphans of an abandoned claim (§6.4).
4. Another author's draft also changed `registries`, from head revision 3,
   and was published first: the head is now at 4.
5. A's draft is published. T3 locks the `registries` head `FOR UPDATE`, finds 4
   where the draft's base is 3, and rolls back. The operation fails with `409
   stale-input` naming `frg_rgkebwvneg6mxhid62gec5difi`, as in the §9.4
   example.
6. Had the other publication instead changed a fragment A's release uses
   unchanged, the same check would refuse it through the `FOR SHARE` read
   (§4.2). Without that lock, DB row 011 shows the release committing on the
   superseded source.

### 13.2 Provider write succeeded, database commit failed

1. An author imports worker `mch_tqhcznunhyle4hnxru5hkt35uq`. `POST
   /ingestions` commits an `ingest` operation and claim
   `ing_4ycffhy7bf4o2w6pz5b4r75hmu` (compilation §2.3 step 0).
2. Extraction substitutes three values; step 6 creates three generations at
   `gen/cl_oxbgrzprzpvnecj5ve3jht3dha/ing_4ycffhy7bf4o2w6pz5b4r75hmu/<value id>`
   with `cas=0`.
3. The database connection breaks inside the draft transaction. Nothing of the
   draft is committed (DB rows 007, 058, 060 show the same for publication).
   The claim is unreleased.
4. Under transient staging the claim is abandoned at lease lapse; the
   operation fails `ingestion-abandoned`. Under encrypted staging a takeover
   may resume it (compilation §3.4).
5. If it is abandoned, the three generations are orphans. The orphan report
   lists their paths under the abandoned claim. Nothing deletes them (design
   §7.8), and a new import mints new names.

### 13.3 Database commit succeeded, the other side failed

Two cases, both with the database ahead of what the client or the provider
knows:

- **Commit-unknown.** A `publish` worker sends `COMMIT` for T3 and loses the
  connection. The release committed, as in DB row 061. The worker cannot tell,
  so it reads: a release exists for `(drf_2rmpezm5rfx47azsgmp66z457a, 8)`
  with matching digests, so it records the operation `succeeded` if its fence
  still holds. If another worker superseded it, that worker's own T3 hits the
  unique key, finds matching digests and succeeds with the same release. The
  client following the operation sees one release.
- **Provider behind the database.** OpenBao is restored from a snapshot older
  than generations the database references. Nothing in Bronzeward wrote this
  state. The dependency monitor reads 404 for those generations; they were
  `retained`, so it alerts at once. A publication that pins them is refused
  (compilation §6 step 3). A release that already holds their artifact may
  still apply if its key version survives, but cannot be regenerated (KL case
  H; design §7.8, guarantees kept apart).

### 13.4 Repeated requests

1. An automation client posts a publication with key
   `2f4c9a1e-6b0d-4d7a-9c3e-58a1f0e2b7d4`. T2 commits operation
   `op_zcfwgr7trb76z3zmsndrnk5mcm` and the record.
2. A proxy retries the request: the key and fingerprint match; the stored
   `202` is replayed with `Idempotent-Replayed: true`. No second operation.
3. The client retries while T2 is still open: the second insert waits on the
   unique index, then replays (not measured, §7.2).
4. The client reuses the key for a different draft: `422
   idempotency-key-reused`.
5. The client, having lost everything, posts again with a new key. The partial
   unique index on active publish operations returns the running operation;
   after it succeeded, the natural key returns the existing release.
6. A human approves the plan twice with two keys: the second is `409
   conflict`, one approval per plan.

### 13.5 A migration

1. The operator stops the service and runs the migrate command for a binary
   whose migrations 1–12 are applied and whose 13th adds an index.
2. An orchestrator also starts a second migrate run. Both take
   `pg_advisory_xact_lock`; the second waits, then finds version 13 recorded
   and skips it (DB row 025).
3. Had the first run been killed inside migration 13's transaction, nothing of
   13 would exist and the next run would apply it (DB row 024).
4. The operator starts the service. It finds versions 1–13 with matching
   checksums and starts. An older binary started by mistake finds version 13
   it does not know and refuses to start.

### 13.6 A restore to an older backup

State at backup time *T*: epoch `ep_bqeknkmarvikuy7ofil2okekgi` (sequence 1),
release `rel_fgqvcvz3ck7h7234ljgdbzsj6m`, operation
`op_e4t4jm7vp3b5fvmbvossyj43hq` owned by executor X at generation 1.

After *T*:

1. A publication commits `rel_e7d7ysg556p667w5aausvvyrne`.
2. X is taken over by Y (generation 2) and back by X (generation 3); X holds
   token `(ep_bqeknkmarvikuy7ofil2okekgi, 3)`.
3. `recovery-admin` enters recovery mode for an unrelated reason, minting
   `ep_e3t4dznwcjg4uoahvfjk4w6kwy` (sequence 2), and leaves it.
4. An ingestion creates generations under claim
   `ing_4ycffhy7bf4o2w6pz5b4r75hmu`.
5. `recovery-admin` revokes human `idn_6woutisn7uensexh3kk2qlz6ma`.

The operator restores the database from *T* and the provider from a snapshot
taken after it, then, before starting the service, runs the entry command. It
mints `ep_53wiltmcac6xxdggvgg7zcoh5y`, sequence 2 again: the lost epoch's
sequence number, a different identity.

- `rel_e7d7ysg556p667w5aausvvyrne` answers `404`; no new release can be given
  that identifier.
- The operation is `unresolved` (entry). If X is still running with its
  token, its next write carries `ep_bqeknkmarvikuy7ofil2okekgi`, not the
  current epoch, and is refused, whichever generation the rows now hold. With a
  counter alone, the next two takeovers would reissue generations 2 and 3, and
  X's token would pass, as in DB row 027. Execution and recovery's first
  procedure step stops X or waits out its maximum request lifetime; the fence
  refuses only its database writes.
- A client that recorded `ep_e3t4dznwcjg4uoahvfjk4w6kwy` sees
  `ep_53wiltmcac6xxdggvgg7zcoh5y` in `Bronzeward-Epoch` and knows its events and
  cursors do not resume.
- The generations of step 4 are orphans; the input is ingested again.
- The revocation of step 5 is gone. The recovery procedure re-records it, and
  the subject is added to `deniedSubjects`; until then the human could sign
  in, but any approval they gave before entry authorizes nothing.
- Every automation token is refused until the tool reissues it.

## 14. Failure and rejection cases

| Area | Condition | Outcome | Persisted |
| --- | --- | --- | --- |
| Auth | Missing, malformed, expired, over-long or wrongly signed token | `401` | nothing |
| Auth | Revoked or denied subject; token from an earlier epoch | `403 identity-revoked` or `401` | nothing |
| Auth | No qualifying role; automation on a human-only route | `403 forbidden` | nothing |
| Request | Missing `Idempotency-Key` or `If-Match` | `428` | nothing |
| Request | Key reused for another request | `422` | nothing |
| Draft | ETag mismatch | `412` | nothing |
| Draft | Compilation refuses the input | `422`, paths only | claim row; orphans if past compilation §2.3 step 6 |
| Draft | Database fails inside T1 | `503`; claim unreleased | claim row; orphans |
| Publish | Moved head | operation `failed`, `409 stale-input` | operation, act |
| Publish | Assignment change while its scope is held | `failed`, `409 scope-busy` | operation, act |
| Publish | Dependency not `retained`, or provider sealed | `failed`, `503 dependency-unavailable` or `422` | operation, act |
| Publish | Commit-unknown | resolved by reading the natural key | the release, once |
| Publish | Worker superseded | its commit refused by the fence | the other worker's result |
| Any | Mutation during recovery mode | `409 recovery-mode-active` | nothing |
| Any | Deadlock retries exhausted | `503 transient-conflict` | nothing |
| Migrate | Failure or kill inside a migration | migration absent; server refuses to start | earlier migrations |
| Startup | Schema or checksum mismatch | refuses to start | nothing |
| Restore | No recovery-mode entry after a restore | undetected (design §14.6); pre-restore tokens can pass | the residual of §12.1 |

## 15. Invariants

1. **No reissued external identifier.** No identifier that leaves the database
   comes from a database sequence.
2. **Immutable is insert-only.** Immutable tables refuse `UPDATE` and
   `DELETE`; no table is deleted from in the PoC.
3. **Every write is conditional.** A mutable record changes only by a
   compare-and-set on its revision, or a fenced `UPDATE` whose predicate
   includes owner, generation and epoch.
4. **Lock what you check.** Every value a transaction's decision rests on is
   locked or in the predicate of the write that acts on it.
5. **No provider I/O in a transaction.**
6. **Provider first, references after.** No committed row references a
   provider object before the ingestion that created it observed the create
   succeed.
7. **One release per draft revision**, and a release is complete or absent.
8. **Heads move only at publication**, and never past a head revision other
   than the one the draft or snapshot read.
9. **Every committed mutation has an act and an idempotency record** in its
   own transaction; no refused request has either.
10. **No request body, secret value or ciphertext in an idempotency record, an
    act or a problem document.**
11. **Everything that authorizes carries the current epoch** (approvals, scope
    releases, fences, claims, automation tokens), compared by equality.
12. **No automation principal holds `approver` or `recovery-admin`**, and no
    route issues tokens or grants roles.
13. **The server runs only on the schema it was built for.**

## 16. Verification and evidence limits

Design: [§18.1](../design/Talos_Configuration_and_Machine_Management_Design.md#181-phase-0---evidence-before-implementation-contracts),
[§18.2](../design/Talos_Configuration_and_Machine_Management_Design.md#182-phase-1---configuration-control-on-an-existing-cluster).

An implementation of this contract must show, with a control that can fail for
each (design §7.7 consequences):

- the four PostgreSQL clauses: `FOR SHARE` at publication, against DB row
  011's control; the ownership check inside the attempt's `UPDATE`, against
  row 018; the claim's eligibility re-check, against row 020; the migration
  advisory lock, against row 026;
- the epoch term: a fence token issued before a recovery-mode entry refused
  after it, with a control that drops the term and passes the token, run across
  a real `pg_dump` restore as DB row 027 was;
- two requests with one idempotency key in flight together;
- every walk-through of §13, and every refusal of §14;
- the immutability triggers, and startup refusal on each schema mismatch;
- authentication refusals for each token defect in §10.1 and §10.2, and each
  route's role check against the scenarios of design §13.7;
- that no request body reaches the database's data directory, write-ahead log
  or backups on the ingestion and draft routes, by the scan compilation §15
  requires.

Evidence gaps this contract carries rather than closes:

- **Authentication and authorization**: untested by any investigation (FR §9
  item 5, SP30). §10 is design against owner policy.
- **Restore epoch**: inferred (DB §9); no row exercises restoration with an
  epoch, and DS did not exercise the epoch check its code contains (DS §7).
  Restoration is evidenced for `pg_dump` only, not physical backup or
  point-in-time recovery (DB §7).
- **Undetected restore**: a restore without recovery-mode entry is not
  detected (§12.1); design §14.6 accepts this.
- **Recovery-mode entry and executor quiescence**: the other half of FR §9
  item 4, execution and recovery's and still unmodelled.
- **Same-key concurrency**: unmeasured (§7.2).
- **Orphan listing**: the metadata identity's permission to list the
  generation tree was not measured (§6.4).
- **Migrations**: no v1 tool, online migration or downgrade was tested (DB
  §6.2); rule 1 of §11 relies on the operator stopping the service.
- **Isolation**: only read committed was measured; serializable was not
  (DB §7).
- **Ciphertext determinism**: whether Transit's ciphertext differs between two
  encryptions of the same plaintext was not checked; §6.2 does not rely on it
  either way.

## 17. Choices for owner review

Each is the most conservative option consistent with the design where the
design and evidence do not settle the question. Each is marked in place as
**(choice §17.n)**.

1. **Random application-generated identifiers; sequences never leave the
   database** (§2). Alternative: sequence ids with an epoch prefix, which
   still reissue within an epoch no one entered.
2. **ETags carry a random token beside the revision** (§4.1). Alternative:
   revision numbers alone, which match again after a restore.
3. **Immutability enforced by database triggers** (§3). Alternative:
   application convention with tests.
4. **Publication checks every input head, changed or not** (§4.2).
   Alternative: check only heads the draft changes, accepting releases built on
   moved unchanged inputs.
5. **The import base is the Applied release's; no separate head** (§3.2).
   Alternative: an import-base head advanced by the adoption record, which puts
   a persistence write into execution and recovery's transaction.
6. **Publication selects the release as Desired for its machines** (§6.2).
   Alternative: select at plan creation or at approval.
7. **No provider or network I/O inside a transaction; fixed lock order; three
   deadlock retries** (§5). Alternative: provider calls inside the
   transaction, holding its locks for the provider's latency, or a pause.
8. **Generation paths carry the claim id and a random value id; orphans are
   reported, never reattached or deleted** (§1.1, §6.4). Alternatives: a
   separate ledger written before each create; reattaching orphans on
   re-ingestion. Needs a line in compilation §2.3 step 6.
9. **An idempotency key on every mutating request, recorded only with a
   committed effect, and kept; no deletion of any record** (§3, §7).
   Alternatives: keys optional; refusals recorded and replayed; records
   expiring after a window.
10. **The release's natural key is the draft revision, compared by a digest
    without ciphertext** (§6.2). Alternative: DB's release name plus content
    digest over everything.
11. **An operation is created with its plan, in `planned`** (§8.1).
    Alternative: created at the commitment, the other reading of design §11.1.
    Needs confirming in execution and recovery.
12. **Job states `queued`, `running`, `succeeded`, `failed`; ingestion not
    re-run; entry fails active jobs** (§8.2). Alternative: resume jobs after
    recovery.
13. **Recovery mode refuses every mutation except those that only remove
    authority or record recovery** (§12.2). Alternative: refuse only machine
    mutation, allowing edits and publication.
14. **Problem documents with codes; a `v1` additive deprecation policy;
    cursors and event ids tied to the epoch** (§8.3, §9). Alternatives: any
    other error format or policy; cursors that survive a restore.
15. **Adoption approval requested as a plan with `operation: adopt`** (§9.2).
    Alternative: a separate adoption resource.
16. **OIDC access tokens verified per request, no session, 15-minute maximum
    lifetime** (§10.1). Alternative: server sessions with introspection or
    back-channel logout, which propagate disablement faster at the cost of
    state.
17. **One valid automation token per identity, rotated by replacement,
    mandatory expiry of 30 days by default and 90 at most** (§10.2).
    Alternatives: an overlap window during rotation; longer or no expiry.
18. **Every automation identity names a responsible human** (§10.2).
    Alternative: none, leaving edge case (b) of design §13.7 item 3
    unrecordable.
19. **Identity revocation is permanent and refuses authentication; a
    deny list in deployment configuration survives restore** (§10.4).
    Alternative: revocation that only invalidates approvals, as design §13.7
    item 4 states it.
20. **With several qualifying roles, the act is recorded under the first in
    the route's order** (§10.3). Alternative: the client names its role in a
    header.
21. **Both unsettled self-approval cases are marked, each with its reason**
    (§10.5). Alternative: mark neither, or only (b).
22. **Interim, pending ginsys/bronzeward#14: ingestion and inventory creation
    need a human `author`** (§10.3). Alternatives: `publisher`; automation
    allowed.
23. **Interim, pending ginsys/bronzeward#14: losing a role does not
    invalidate approvals** (§10.4). The alternative needs a directory lookup
    or a session to observe the loss.
24. **Migrations by explicit command with the service stopped; startup refuses
    any schema or checksum mismatch; no rewrite of immutable rows; no
    downgrade** (§11). Alternative: migrate at startup.
25. **The epoch is a random identity compared by equality, with a counter for
    order** (§12.1). Alternative: an external high-water mark (for example in
    OpenBao or a file) checked at startup, which could detect some restores
    but adds a dependency that can itself be restored.
26. **Recovery-mode entry also by a server-side command before the service
    starts** (§12.2). Alternative: API only, which needs the service running on
    restored state first.
27. **Automation tokens from an earlier epoch are refused** (§12.3).
    Alternative: keep them, reviving any token whose revocation the restore
    removed.

## 18. Traceability

| Clause | Design | Evidence |
| --- | --- | --- |
| §1 scope, interfaces | §7.2, §11, §13.7 | [FR §10](../design/research/20260925-feasibility-evidence-review.md#10-recommendations) (Persistence) |
| §2 identifiers | §4.4, §7.7 | [DB §4.7](../design/research/20260924-database-semantics.md#47-s7-restored-state) row 027; [DB §9](../design/research/20260924-database-semantics.md#9-hand-off) |
| §3 entities, immutability | §6.2, §7.2, §7.8, §11.2 | none: choices §17.3, §17.5 |
| §4 revisions, ETags | §7.2, §11.1 | [DB §4.1](../design/research/20260924-database-semantics.md#41-s1-stale-revision-rejection) rows 001–003; DB §4.7 |
| §4.2 stale input | §7.4 step 4 | [DB §4.2](../design/research/20260924-database-semantics.md#42-s2-all-or-nothing-publication) rows 010, 011 |
| §5 transactions | §7.2, §7.4 | DB §4.2 rows 059, 061; [DB §6.3](../design/research/20260924-database-semantics.md#63-criterion-3-backend-specific-limitations-and-costs); [DB §7](../design/research/20260924-database-semantics.md#7-limits); [DS §7](../design/research/20260925-dispatch-safety.md#7-limits) |
| §5.1 fences, claims | §7.2, §12.5 | [DB §4.4](../design/research/20260924-database-semantics.md#44-s4-ownership-transitions) rows 015–018; [DB §4.5](../design/research/20260924-database-semantics.md#45-s5-queue-claims) rows 019–021 |
| §6 publication | §7.4, §7.8 | DB §4.2 rows 004–011, 058–061; KL §7 item 1 |
| §6.3 partial publication | §7.4, §7.6, §7.8 | [PC §4](../design/research/20260924-provider-capability-comparison.md#4-the-matrix) row 083; [KL §3.2](../design/research/20260924-key-loss-restoration.md#32-what-each-case-showed) cases G, H; [RC §6.4](../design/research/20260924-retention-metadata-classification.md#64-criterion-4-provider-limits-and-the-alert-policy-the-evidence-supports) |
| §6.4 orphans | §7.4, §7.8 | DB §9; KL case G |
| §7 idempotency | §11.1, §12.5 | [DB §4.3](../design/research/20260924-database-semantics.md#43-s3-unique-operation-intent) rows 012, 013; DB row 061; [E1 §7](../design/research/20260922-secret-ingress-extraction-before-persistence.md#7-limits), [E1 §4.4](../design/research/20260922-secret-ingress-extraction-before-persistence.md#44-the-forbidden-design-measured) |
| §8 operations | §11.1, §15.2 | [DS §4.2](../design/research/20260925-dispatch-safety.md#42-ownership-loss-and-a-second-executor-criterion-2) row 009; DB §4.5 |
| §9 API | §11.1, §11.2, §13.7 | none: FR §9 item 5 |
| §10 authentication, authorization | §13.1, §13.3, §13.6, §13.7 | none: FR §9 item 5; [DS §4.1](../design/research/20260925-dispatch-safety.md#41-revocation-around-the-commitment-boundary-criterion-1) for approval revocation |
| §11 migrations | §7.2, §14.2 | [DB §4.6](../design/research/20260924-database-semantics.md#46-s6-migrations) rows 022–026; [DB §6.2](../design/research/20260924-database-semantics.md#62-criterion-2-intent-ownership-queue-claims-migrations-restored-state) |
| §12 restored state, epoch | §7.7, §7.8, §14.6 | DB §4.7 row 027; DB §9 (inferred); [KL §7](../design/research/20260924-key-loss-restoration.md#7-recommendation) items 2, 5; FR §9 item 4 |
| §16 gaps | §18.1, §18.2 | [FR §9](../design/research/20260925-feasibility-evidence-review.md#9-gaps), FR §10 |
