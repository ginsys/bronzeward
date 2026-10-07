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
recovery"). Both are cited by section topic and number.

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
- the adoption workflow ([ginsys/bronzeward#22](https://github.com/ginsys/bronzeward/issues/22)
  for the import, [ginsys/bronzeward#25](https://github.com/ginsys/bronzeward/issues/25) for the
  handover) and the edit and publication user flow
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
- The import base as a per-machine source revision (compilation §6), with the
  baseline ciphertext and its keyed digest (compilation §2.3 step 8).
- Keyed digests under a provider-held key (compilation §4.1), which §7 uses to
  fingerprint request bodies that can hold secrets.
- Staging claims with owner generation, server-clock lease and absolute expiry,
  abandoned at recovery-mode entry (compilation §3.2–§3.5).

It needs from compilation, as cross-contract points:

1. The component of a generation path "that the database does not issue"
   (compilation §2.3 step 6) is the ingestion's claim identifier followed by a
   random value identifier (§6.4) **(choice §17.8)**.
2. The hand-off unit carries, per machine, the plaintext configuration digest
   (execution and recovery's configuration digest function, over the artifact's
   plaintext), not only the ciphertext's digest, so that the release records a
   digest stable across a second encryption (§6.2). The import base carries the
   baseline's unkeyed configuration digest next to its keyed digest.
   Compilation §2.3 step 8 and §11 hand both over (compilation choice §16.26).

### 1.2 What this contract needs from execution and recovery

1. Every fence the commitment and attempt transactions compare includes the
   current recovery epoch's identity (§12.1), read with the installation state
   row `FOR SHARE`.
2. The commitment and attempt transactions read the machine's assignment head
   `FOR SHARE`, so that a publication changing that assignment (§6.2) and a
   commitment serialize: the rule "any transaction that changes a machine's
   assignment must check that machine's coordination scope" in the
   dispatch commitment section of execution and recovery.
3. Comparison 1 of the commitment and of every attempt transaction reads the
   approval row and the approving principal's row `FOR SHARE`, so that an
   approval revocation (which locks the approval row `FOR UPDATE`) and an
   identity revocation (which locks the principal row `FOR UPDATE`) wait for it
   or precede it (T5; DS row 003).
4. Recovery-mode entry is the `recovery-admin` API act of §12.2, run as one
   transaction (T9) that also performs §12.2's persistence effects, after the
   service was started with the recovery-start flag **(choice §17.26)**.
5. The post-restore procedure re-records identity revocations made after the
   backup was taken, and reissues automation tokens rather than re-recording
   their revocations: every token from an earlier epoch is refused (§12.3).
   The tool reissues no token to a service identity that `deniedSubjects`
   lists, so a reissue before the re-recording cannot revive a revoked one
   (§10.2, §10.4).
6. The operation of a plan is created by the dispatch commitment, not with the
   plan (§8.1) **(choice §17.11)**; comparison 0 ("no operation exists for this
   plan yet") is the unique index of §7.3.
7. The adoption record is the commitment of a plan with `operation: adopt`
   (§9.2), taken under the machine-scope lock like any commitment
   **(choice §17.15)**.
8. Its statement that a restore makes the next release reuse an erased
   release's identifier is dropped: identifiers here are never reissued (§2).
   Its rule that nothing is matched across a restore by identifier stays, as
   defence in depth.
9. The self-approval reasons agree with §10.5: a token's responsible human is
   also marked when the token created the plan, and "self-approval
   undetermined" never arises, because every automation identity names a
   responsible human **(choice §17.18)**.
10. The recovery epoch is a never-reissued random identity compared by
    equality (§12.1), replacing the counter incremented at entry that its
    earlier text required.

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
| `dep` | monitored dependency ([dependency monitor §2](dependency-monitor.md#2-what-is-monitored)) | `dal` | dependency alert |
| `ep` | recovery epoch | `act` | act record |
| `req` | API request | | |

A collision is not checked for beyond the primary-key constraint, which refuses
it. The manager machine ID of design §4.4, a "stable UUID allocated by this
platform", is the `mch` identifier: stable and platform-allocated, with 128
random bits in this text form rather than in RFC 9562's UUID layout
**(choice §17.1)**.

Internal `bigint` sequence keys may exist for joins and ordering, but never
appear in an API payload, a URL, a cursor, a provider path or a log line. A
restore can reissue them; they are compared only inside one database state.

Revision numbers (§4) are counters and do rewind with a restore. A request
presents one as a precondition only inside a strong ETag, beside a random
token (§4.1); a response body may report one for reading, which no request
sends back **(choice §17.35)**.

## 3. Entities and records

Design: [§7.2](../design/Talos_Configuration_and_Machine_Management_Design.md#72-relational-revision-model-and-backend-contract),
[§11.2](../design/Talos_Configuration_and_Machine_Management_Design.md#112-api-resource-boundaries),
[§6.2](../design/Talos_Configuration_and_Machine_Management_Design.md#62-fragments-profiles-and-assignments).

A record is **immutable** (insert-only) or **mutable** (updated in place under a
revision, a fence or a row lock, §4). No record of either kind is deleted in the PoC: design
§7.8 item 1 derives that no release, artifact, source revision or dependency
record is deleted without a reference graph, and this contract extends that to
every table **(choice §17.9)**. The only in-place clearing is compilation's: a
claim's payload is set to `NULL` on release or abandonment.

Immutable tables carry a trigger that refuses `UPDATE` and `DELETE`, with a test
that the trigger fires **(choice §17.3)**. A source or import base revision's
rows (a fragment or import base revision's reference rows, a profile revision's
pins, an assignment revision's selections) are written in the transaction that
writes the revision, and a
trigger refuses a row for a revision already committed, so a revision's
content cannot grow after it is read. Each revision records its writer, the
full transaction ID set by the database on insert; a row whose revision has
another writer is refused, and one whose revision this transaction cannot see
is refused as a missing reference when it is inserted, not at the end of the
statement.

| Entity | Kind | Holds | Owner of semantics |
| --- | --- | --- | --- |
| Cluster | mutable, revisioned | name, endpoint, Talos cluster ID (unique, §7.3), contract, status | this contract |
| Machine | mutable, revisioned | `mch` id, identity key: its SMBIOS UUID or, for a machine that reports none, its Talos node ID, fixed at inventory (unique, §7.3), hardware evidence, cluster membership, Talos endpoint (§3.3), platform mode: `metal`, `container` or `cloud`, required at inventory with no default and fixed there, the mode its configuration validates in and its release machines record (compilation §6 step 8; choice §17.32), current freeze and recovery scope state (projected from their facts), machine revision counter (§5, T7) | this contract; freeze and scope state are execution and recovery's |
| MachineEndpointChange | immutable | a machine's previous Talos endpoint and new one, who changed it, role, epoch, time (§3.3) | this contract |
| Fragment | mutable head | name, layer, scope (a cluster or the library), pointer to the head revision | this contract |
| FragmentRevision | immutable | sanitized YAML text, canonical parsed form, declarations, reference rows, author | compilation §2, §5 |
| Profile / ProfileRevision | mutable head / immutable | ordered fragment revision ids | this contract |
| Assignment / AssignmentRevision | mutable head / immutable | per machine: selected profiles and fragments per layer | this contract |
| ImportBaseRevision | immutable | a machine's sanitized imported document with references and the embedded documents the import identified (publication rebuilds the base with them, §6.1), baseline ciphertext, its keyed digest and its configuration digest | compilation §2.3, §6 |
| Draft | mutable, revisioned | change set: entries and their base head revisions | this contract |
| Release, ReleaseMachine | immutable | the compilation §11 unit, with each machine's configuration digest (§1.1) and the name of the Transit key its artifact was encrypted under; the release covers one cluster and a set of its machines | compilation §11; this contract |
| Dependency record | immutable | effective and reproduction dependencies, encryption dependency with the key identity: the machine's key at its ciphertext's key version. Each effective dependency has a reproduction dependency of its machine at the same version and creation time, whose source is in the machine's composition and declares it at that version and generation; indexed by its DependencyStatus identity (dependency monitor §6.1) | compilation §9 |
| DependencyStatus | mutable | last classification per provider object version and creation time, and when first seen `retained` | design §7.6, §7.8; [dependency monitor §5](dependency-monitor.md#5-state) |
| DependencyAlert | immutable | every alert the dependency monitor raises | [dependency monitor §6.2](dependency-monitor.md#62-alert-kinds) |
| DependencyMonitor | mutable singleton | the monitor's progress, last completed pass, last stall alert and last logged alert | [dependency monitor §5.1](dependency-monitor.md#51-records) |
| Staging claim | mutable, fenced | state, owner, owner generation, lease, expiry, payload; for a draft entry route, the principal and idempotency key (§7.2) | compilation §3 |
| MachineState | mutable, revisioned | Desired, Applied (with source), baseline revision | execution and recovery (Desired, Applied and Observed) |
| Observation | immutable | purpose, read-start basis, machine revision, identity, assignment evidence, running version, configuration digest, health, or what could not be read | execution and recovery §4.1 |
| Plan | immutable | the binding, creator and role | execution and recovery (plan binding) |
| PlanState | mutable, revisioned | the plan's state (§8.1) with its reason, and its operation once committed | execution and recovery §2; §8.1 |
| Approval | immutable | plan (its identifier: the plan is immutable, §4.1), approver, role, epoch, self-approval mark | execution and recovery; §10.5 |
| Approval revocation, identity revocation, plan cancellation | immutable | what it names, who, role, when, epoch, reason | §10.4; execution and recovery |
| Operation | mutable projection, fenced | kind, state, owner, owner generation, owner epoch, lease; for `publish` and `ingest`, the last event number | §8; execution and recovery for `apply-config` and `adopt` |
| TimelineEvent | immutable | append-only entries of a machine scope (plans, operations and machine-scope facts) with the machine revision, or of a `publish` or `ingest` operation with its event number (§5, T7) | execution and recovery §4.1 |
| Attempt, Response, Accounting decision | immutable | an attempt with its owner token, route and deadlines; its response class and redacted text; an accounting's basis, decider and role | execution and recovery §3, §5 |
| Drift record | mutable projection | machine, observed digest, open or closed with the resolution that closed it; opening and closing are timeline entries | execution and recovery §6 |
| Adoption record | immutable | the adopt plan and its `completed` operation, the adopted release, the baseline digest compared, the observation relied on | execution and recovery §6.3 |
| Scope state mark, scope release, freeze, unfreeze | immutable | per machine, with epoch, identity and role | execution and recovery §6, §7 |
| Principal | mutable | human (`iss`, `sub`) or service identity; creation time; revoked flag | §10 |
| AutomationToken | mutable | token id, SHA-256 of the secret, roles, owner, expiry, epoch, revoked time | §10.2 |
| Act | immutable | every mutating act: principal, role, action, subject, epoch, time | §10.5 |
| IdempotencyRecord | immutable | key, fingerprint, stored response | §7 |
| InstallationState, RecoveryEpoch | mutable singleton / immutable | current epoch, recovery mode; each entry with its restored backups | §12 |
| schema_migrations | immutable | applied migrations | §11 |

### 3.1 Sources, drafts and heads

A fragment, profile or assignment has one **head**: a mutable pointer to its
current published revision, with a head revision counter and token (§4.1).
Heads advance only in the publication commit (§6.2). Every edit, published or
not, is stored as an immutable revision first; design §7.2's "mutable drafts
remain distinct from releases" is met by the draft being the only mutable
record of unpublished work.

A head is created by the publication that first introduces its name. There is
one head per kind, name and scope (a cluster or the library; an assignment's
machine), backed by a unique index. A removal does not delete the head (§3): it
sets the head's revision pointer to none and advances its head revision, so the
releases that used it still name it, a later draft can introduce it again from
that head revision, and compilation treats it as absent.

A **draft** belongs to one cluster. It holds entries, each naming a head (or a
new fragment, profile or assignment, whose base is "absent"), the proposed
revision (or removal) and the head revision the author edited from, its
**base**. A draft update is compilation's draft transaction: it inserts the new
immutable revision and its reference rows, sets the entry, and advances the
draft's revision (§5, T1). A draft ends `published` (by §6.2) or `discarded`;
neither state accepts further edits. While a `publish` operation for the draft
is `queued` or `running`, the draft accepts neither an update nor a discard:
both answer `409 conflict` naming the operation, so the revision the operation
is bound to cannot move under it. Once the operation fails, the draft accepts
edits again.

A profile revision pins fragment **revisions**; an assignment revision selects
profiles and fragments by **name**, per layer, and the publication that
compiles it uses each named head's revision, which T3 holds unchanged
(§6.2). A fragment carries one of six layers, in composition order `global`,
`site`, `cluster`, `role`, `workload`, `override`: design §6.2's seven minus
machine-intrinsic, which is the import base (compilation §6). The draft's own
entry for a name decides over its head, since publication replaces the head
with it: a pin must be the revision this draft proposes for its fragment or,
with no entry for that fragment, the fragment's head revision; a selected name
must be proposed by this draft or, with no entry for it, have a head with a
revision; a name this draft removes can be neither pinned nor selected; and a
fragment selected under a layer must carry that layer. Otherwise the update is
refused `422 validation-failed`, naming the body path (compilation §7, stage
1). Publication checks the same again. A removal of a name the draft
introduces, with no head, leaves an entry whose base is absent and whose
publication creates nothing; a removal of a name with neither a head (one a
publication removed counts) nor an entry is `404`. The PoC accepts cluster scope only: a fragment or
profile names its draft's cluster, and the library scope of choice §17.28 is
refused by the schema until a later phase defines how its draft is reviewed
**(choice §17.31)**.

A draft and its release cover one cluster, while a fragment may belong to the
library. A library fragment changed in one cluster's draft is published once,
through that cluster's release; another cluster using it picks up the new head
at its own next publication, with no draft of its own naming the change. Its
approver sees the change in the plan's redacted whole-configuration diff
(execution and recovery, plan binding) **(choice §17.28)**.

### 3.2 The import base

A machine's import base (compilation §6) is not a head. It is the import base
revision of the machine's `Applied` release **(choice §17.5)**. `Applied`
changes only through a completed operation or an adoption record (execution and
recovery, Desired, Applied and Observed), so the import base every other
publication compiles on changes only when a release carrying a new import base
becomes `Applied`. Compilation §6 names that base the "latest accepted drift
adoption"; reading it from `Applied` is this contract's choice.

A draft built from an import or an adoption carries its new import base
revision as an entry, so the release published from it compiles on the new
base at once: this is execution and recovery's "the sanitized document becomes
the machine's new import base, and a release compiled from it is published"
(adopt, step 2). A machine with no `Applied` has no import base; the only draft
that can cover it is one carrying an import base revision.

Between an adoption's publication and its adoption record, another publication
covering the machine compiles on the old base and becomes the machine's
`Desired` (§6.2). The adopt plan binds the machine's `Desired` at its creation,
and the adoption record refuses if it has changed since (execution and
recovery, adopt, requirement 4.3). A publication before the plan is created is
therefore superseded only by a plan whose approval sees it; one after needs a
new adopt plan. The adoption record sets `Applied` and `Desired` to the adopted
release, and any plan made before the record fails the baseline comparison
afterwards (execution and recovery, comparison 2). A publication compiled on
the old base but committing after the record is refused `409 stale-input`
(§4.2).

### 3.3 Talos access

Design: [§7.1](../design/Talos_Configuration_and_Machine_Management_Design.md#71-responsibility-split-and-secret-ingress),
[§13.1](../design/Talos_Configuration_and_Machine_Management_Design.md#131-trust-boundaries),
[§13.2](../design/Talos_Configuration_and_Machine_Management_Design.md#132-provider-access-separation).

Reading a node needs two things: where to reach it and a credential it accepts.
They are held apart **(choice §17.29)**.

- **The endpoint is per machine, in the database.** `POST /machines` requires
  `talosEndpoint`: an IP literal (an IPv6 literal in brackets), with an
  optional port from 1 to 65535, `50000` when absent; no DNS name, scheme,
  path, user part or whitespace. A name is refused because it can resolve to
  another node between the observation that checks the node's identity and the
  attempt that sends the request, each on its own connection, and a node
  dialled without `node` metadata answers for itself. The endpoint is only dialled: every request
  to the node goes to it and carries no `node` or `nodes` routing metadata,
  so the node dialled answers for itself and no apid forwards it (execution
  and recovery §3.5, choice §10.9 there). Talos compares `node` metadata with
  a node's own bare addresses and refuses what a worker does not recognise
  ("no request forwarding", `internal/app/apid/pkg/director/director.go` at
  v1.13.6), so a value with a port or a DNS alias would fail there. A plan
  binds the endpoint current at its creation as its route, and every
  observation taken for that plan or its operation (execution and recovery
  §4.1) dials that route, never the machine's current endpoint, so the
  identity it reads is that of the node the request will reach. An
  observation taken for no plan, and a `source: machine` ingestion, dial the
  current endpoint; each records the endpoint it dialled.
  `POST /machines/{id}/talos-endpoints` replaces it (an address change, or one
  entered wrongly at inventory, which the identity key's index would otherwise
  leave unrecoverable): under the machine row `FOR UPDATE` (T11), it records a
  MachineEndpointChange, an endpoint-change entry on the machine's timeline
  holding the previous and new endpoint, who and role (execution and recovery
  §4.1, read through `GET /machines/{id}/timeline`), and its act (§10.5) in one
  transaction. An endpoint outside that grammar is a malformed body,
  `400 invalid-request`, on either route, with nothing committed. It changes no existing plan, whose route stays
  the one it bound. Like inventory, it is accepted installation-wide in
  recovery mode (§12.2): it changes nothing on a machine, and a restored
  endpoint may be the one that no longer answers. The column is `NOT NULL`:
  every machine has an endpoint.
- **The credential is per cluster, in the provider.** Talos authorizes a client
  certificate signed by the cluster's own certificate authority, so one
  credential reaches every node of the cluster (design §13.1, "cluster-specific
  credentials"); a per-machine credential would be the same one copied. Each
  cluster's Talos client configuration (a `talosconfig` document) is the KV v2
  secret `secret/data/access/talos/<cluster id>`, data
  `{"talosconfig": "<document>"}`. Its path is derived from the cluster's id
  and no request names it, so no request can point a read at another secret.
  The operator writes it with the provider's own tooling,
  `bao kv put -mount=secret access/talos/<cluster id> talosconfig=@<file>`
  (the CLI takes the logical path and adds KV v2's `data/` itself; policies
  name the API path `secret/data/access/talos/*`), under
  an administrative identity, before the cluster's first `source: machine`
  ingestion; rotating it is writing a new version. No Bronzeward identity
  writes, lists or deletes it, and no route accepts it: the normal API never
  carries a secret value (compilation §1).
- **Who reads it.** The ingestion identity, for a `source: machine` ingestion,
  and the executor identity, for its observations and dispatch (execution and
  recovery §3.1 item 2), each read its latest version at use and record that
  version's identity, never the value: its path, version and `created_time`.
  The version number alone is not an identity: deleting the path's metadata,
  or a provider restore, and writing again issues the same path and version for
  other bytes, which is why compilation §9 and dependency monitor choice §11.13
  identify a KV version by its `created_time` too. A dispatch reads it once,
  before its evidence observation: that observation, the use-time check's
  record and the attempt made on that evidence use that one version, so a
  rotation between them cannot change the credential the request carries,
  and a credential Talos refuses fails the observation before anything is
  committed. A retry gathers new evidence (execution and recovery §3.3),
  which reads it again, and the retry's attempt uses that new version: each
  version binds one evidence observation and the attempt made on it, so a
  rotation before a retry is picked up by that retry. An ingestion records it,
  with the endpoint dialled, as an event of its `ingest` operation (T7, read
  through `GET /operations/{id}/events`), committed once the read returns a
  version and before the node is dialled, so it is kept whether the
  ingestion then succeeds or fails; a read that returns no version records
  none, and the terminal event carries its cause. The executor records it on
  each observation and use-time check (execution and recovery §4.1). The path is disjoint from the secret generations
  under `secret/data/gen/*`, so the ingestion identity still never reads what
  it creates (compilation §1, choice §16.1 there). The compiler, the
  dependency monitor and the normal API cannot read it.
- **What it can do.** Reading a node's machine configuration needs the
  `os:admin` Talos role, as observed on the fixture (`internal/talos`
  `TestLiveRoleProbe`, Talos v1.13.6, recorded in the fixture README:
  `os:reader` and `os:operator` read the version but are refused the
  `MachineConfig` resource with `PermissionDenied`). The probe reports those denials but
  asserts only that `os:admin` reads, so the claim rests on that observation
  until the probe asserts the denial code for both roles (§16). The same certificate can apply, reboot and
  reset; Talos cannot express a read-only grant for it. The bound on what
  ingestion does with it is the application's read-only Talos client, whose
  source is held to an allowlist of machinery calls by test. The executor
  holds two clients built from it: every observation, with or without a plan
  (`drift`, `restoration` and `recovery` have none), goes through the same
  read-only client, so it needs no approved plan; the client that sends a
  mutating request is used only by dispatch, after execution and recovery
  §3.1's approval check, which also gates artifact decryption (design §13.1,
  owner decision 2 October 2026). The provider cannot tell these uses apart:
  the executor identity's read grant holds at any time, and the bound is the
  application's, as for ingestion.
- **What is used.** Only the talosconfig's certificate authority, client
  certificate and key. Its contexts' `endpoints` and `nodes` are ignored: the
  client dials only the endpoint chosen above (the machine's, or the plan's
  route) and sends no `node` metadata, whatever the document names.
- **What is kept.** The value lives only in the memory of the process that read
  it, for the use it was read for: an ingestion; one observation outside a
  dispatch's evidence-and-attempt pair (`drift`, `restoration`, `recovery`
  or `completion`, which may follow the last attempt); or an evidence
  observation and the attempt made on it. It is discarded when that use ends
  or is abandoned; a retry reads afresh (above). It is never stored in the database, logged, returned
  or quoted in an error (compilation §13), and never written to a file:
  neither reader passes it to a client as a file path.
- **Identity before use.** Before using a configuration read for ingestion, the
  ingestion reads the node's SMBIOS UUID, Talos node ID and Talos cluster ID
  on the same connection and compares them with the machine record (§7.3) and
  its cluster's record, the identity execution and recovery's choice §10.26
  defines. For a machine recorded by SMBIOS UUID, a different or absent UUID;
  for one recorded by Talos node ID, a different node ID or any reported
  SMBIOS UUID; for either, an identity read that fails (an error one of the three reads
  returns other than gRPC `Unavailable` or `DeadlineExceeded`; those are how a
  credential the node refuses and an endpoint that does not answer surface, so
  either code is the failure below whichever side ended the request, and a
  `systeminformation` read that succeeds with no resource is a node reporting
  none), or a node that accepts
  this credential but reports another Talos cluster ID (possible only where
  clusters share a Talos certificate authority): each fails the ingestion with
  `409 machine-identity-mismatch`, as below, and nothing read is kept. A node behind another certificate authority, or another cluster's
  talosconfig under another certificate authority written at this cluster's
  path, fails the mutual TLS handshake before any read: a credential Talos
  refuses, below. Under a shared certificate authority, another cluster's
  talosconfig reaches this cluster's node and reads it; the identity check
  then passes, since the node is the recorded one, and the ingestion succeeds.
  Dispatch has execution and recovery's own comparison (its §3.2
  comparison 3). The values were read with `talosctl`, not yet through the
  application's read-only client, as that choice states, and a node swapped
  between the identity read and the configuration read is the residual it
  states.
- **Failure.** An absent secret, one this identity cannot read, a provider
  that is sealed or does not answer, a document that is not a usable
  talosconfig, a credential Talos refuses, or an endpoint that does not answer
  is the problem `503 talos-access-unavailable`, naming the cluster or machine
  and never a value or the parser's or client's own text. For an ingestion it arises after `POST /ingestions` has
  committed the claim and its `running` operation (T11; compilation §2.3
  step 0), so it ends them as a refused input does: one transaction, under
  the claim's owner check (§5.1), writes the claim `abandoned` (compilation
  §3.2) and fails the operation with this problem and its terminal event
  (§8.2). No generation, draft revision or read value is written. The
  ingestion is not retried in place, as no `ingest` operation is (§8.2): once
  the cause is repaired, the operator starts a new one. For
  dispatch, a provider-side cause (an absent, unreadable or malformed
  configuration, or a sealed or silent provider) fails the use-time check
  (execution and recovery §3.1 item 2), and a node-side cause (a credential
  Talos refuses, or a silent endpoint) fails the fresh observation (item 1);
  either way no operation is committed and nothing is dispatched, and the
  timeline keeps the evidence: the observation-started entry and the failed
  observation or use-time check (execution and recovery §4.1). A restore of the provider
  to an older snapshot can bring back a superseded credential; if Talos no
  longer accepts it, reads fail the same way until the operator writes a
  current one.

## 4. Revisions and optimistic concurrency

Design: [§7.2](../design/Talos_Configuration_and_Machine_Management_Design.md#72-relational-revision-model-and-backend-contract),
[§11.1](../design/Talos_Configuration_and_Machine_Management_Design.md#111-api-style).

A record the §3 table marks revisioned, and a head's head revision (§3.1),
carries an integer `revision`, incremented by every write. Every write to a
mutable record is conditional, in one of the three forms of §15 invariant 3: a
fenced `UPDATE` (§5.1), a write under a row lock the same transaction took
`FOR UPDATE` and then checked, or a compare-and-set on the revision. The
compare-and-set is the form for a revision read outside the transaction (an
`If-Match`, a draft's base, a revision an operation bound):

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

A revision number a request presents as a precondition is only ever inside a
strong ETag that also carries a random token, replaced on every write
**(choice §17.2)**:

```text
ETag: "5-m3oxmlfh6phr7aigshdydcb4ji"
```

The server compares the whole value. After a restore, a record's revision 5
can be issued again with other content (DB §4.7); its token will differ, so an
ETag issued after the backup never matches a reissued revision. An ETag of the
revision the backup holds still matches the restored record, whose content it
describes; the epoch does not enter the ETag.

A response body may also report a revision number on its own: a draft's
`revision`, a draft entry's `base`, a source's `headRevision`, an operation's
or a release's `draftRevision`. It describes the record as of that response,
for reading and for ordering within one database state. No request takes a
bare revision number, so a restore that reissues one can never make it match
**(choice §17.35)**. A request that binds an immutable record names the
record's identifier, which is never reissued (§2): an approval binds its plan
by the plan's identifier in its path, and the plan's content cannot change
under it (§9.3).

Mutations of a draft require `If-Match` with the draft's current ETag. A
mismatch is `412 precondition-failed`; a missing header is
`428 precondition-required`. The handler compares `If-Match` before it runs
ingestion, so a stale edit creates no provider generation, and T1 compares it
again under the draft's lock (§5). `POST /ingestions` writes its draft later,
from an `ingest` job: its T11 compares `If-Match` before it creates the
operation, which binds it, and the job's T1 compares that bound revision
instead. Heads, clusters and machines are not written
through `If-Match` by the API in the PoC: heads move only by publication
(§6.2), which checks them itself.

### 4.2 Concurrency conflicts in publication

Publication rejects stale input (design §7.4 step 4) in three forms, all
`409 stale-input`, which names each head, or machine, with its expected and
actual revision:

- a head the draft changes has moved since the draft's base: another
  publication changed the same fragment, profile or assignment, or introduced
  the name the draft introduces (expected "absent");
- a head the release uses unchanged has moved since compilation's snapshot
  (compilation §6 step 1);
- a covered machine's import base (§3.2), where the draft carries none for it,
  is no longer the import base revision the snapshot read: an adoption record
  or a completed operation changed its `Applied` in between.

The second and third checks cover inputs the draft did not touch
**(choice §17.4)**: DB row 011 is exactly that case, a release committed on a
superseded source. The
author's remedy is a new draft revision from the current heads; how the edit
flow offers that is ginsys/bronzeward#23's.

## 5. Transaction boundaries

Design: [§7.2](../design/Talos_Configuration_and_Machine_Management_Design.md#72-relational-revision-model-and-backend-contract),
[§7.4](../design/Talos_Configuration_and_Machine_Management_Design.md#74-publication-without-distributed-transactions).

Rules for every transaction:

1. **No provider or network I/O inside a transaction** **(choice §17.7)**.
   Provider calls, compilation, Talos requests and response writing happen
   between transactions. A paused server kept a publication's transaction open
   for its whole pause (DB row 059, a stall DB infers from the row's timing),
   and with it, by inference, every lock the transaction held.
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
   claims. A time that must follow a lock wait, a DependencyStatus row's
   `recorded_at` (dependency monitor §6.1), the DependencyMonitor row's
   progress (dependency monitor §6.3), a staging claim's lease and expiry,
   when created and when checked (compilation §3.5), and a publish job's lease,
   when claimed, extended and checked at completion (§5.1), is the database's `clock_timestamp()`
   read after the lock is held, since `now()` is fixed when the transaction
   began.
5. **Lock order.** The request's idempotency-key lock (§7.2), installation
   state, machine rows by id (each with its MachineState), heads by id, the
   draft, principals by id, approvals by id, plan states by id, then operations
   by id. The act-order lock comes last, just before the act is written, and is
   held to the end of the transaction, so acts become visible in recording
   order and a `GET /acts` cursor never passes an act that commits later
   (§10.5). A read of an immutable row needs no lock and may come first, for
   example the plan binding that names the machine to lock. A detected
   deadlock aborts the transaction, which is retried whole, at most three
   times, then fails `503 transient-conflict` **(choice §17.7)**.
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
| T1 | Draft update (compilation's draft transaction) | key lock (§7.2); installation state `FOR SHARE`; the fragment and profile heads a profile's pins or an assignment's selections are checked against (§3.1), `FOR SHARE` in id order, a head created after this step counting as no head; draft `FOR UPDATE`, `open`, revision equals `If-Match`, no `publish` operation for it `queued` or `running` (§3.1; draft discard in T11 checks the same); claim owner and generation in the release's conditional `UPDATE` | revision rows, reference rows, draft entry, draft revision, claim `released`, idempotency record, act. An `ingest` job's draft transaction takes no key lock and writes neither record: the `POST /ingestions` request's T11 wrote them. In place of `If-Match` it compares the draft's revision with the one the operation bound from that request's `If-Match` (§9.2); a moved draft fails the operation `412 precondition-failed`, and a draft no longer `open` or one with an active publication fails it `409 conflict` (naming that publication), each with its terminal event, in the owner-checked transaction that abandons its claim once the draft transaction has rolled back (§8.2). An import of a machine already in the draft replaces its draft entry. Its claim owner and generation are the operation's (§5.1), and the transaction that releases the claim also writes the operation `succeeded`, with its result and terminal event (T7), under the same owner check, so no `running` operation outlives its released claim |
| T2 | Publication request | key lock; installation state `FOR SHARE`; draft `FOR UPDATE`: a `published` draft answers `409 conflict` naming its release, otherwise `open` and revision equals `If-Match` | publish operation `queued` (or the active `publish` one, §7.3), idempotency record, act |
| T3 | Publication commit (§6.2) | as §6.2 | release rows, heads, Desired, draft `published`, operation `succeeded`, its event |
| T4 | Plan creation | key lock; installation state `FOR SHARE` (§12.2); machine row `FOR UPDATE`, its scope not pre-restore unaccounted (§12.2); release published and, for an `apply-config` plan, the machine's `Desired` (execution and recovery choice §10.24); execution and recovery's binding checks | plan, plan state `proposed`, machine timeline entry, idempotency record, act |
| T5a | Approval | key lock; installation state `FOR SHARE` (§12.2); machine row `FOR UPDATE`, its scope not pre-restore unaccounted; approver's principal `FOR SHARE`, not revoked; plan state `FOR UPDATE`, unexpired, and `proposed`, or `approved` by an approval from an earlier epoch | approval (unique per plan and epoch, with the self-approval mark), plan state `approved`, machine timeline entry, idempotency record, act |
| T5b | Approval revocation | key lock; installation state `FOR SHARE`; machine row `FOR UPDATE`; approval `FOR UPDATE`, which waits for a commitment or attempt holding it `FOR SHARE` (§1.2 item 3); plan state `FOR UPDATE` | revocation row; plan state `revoked` only when the plan is `approved` by the named approval (an earlier-epoch approval that a current one replaced, or a plan already terminal, keeps its state); machine timeline entry, idempotency record, act |
| T5c | Identity revocation | key lock; installation state `FOR SHARE`; every machine row `FOR UPDATE`, in id order, as T9 (rule 5); principal `FOR UPDATE`, which waits likewise | revocation row, principal `revoked`, a service identity's token revoked; an identity revocation entry (T7) on the timeline of each machine with a plan that identity approved whose plan or operation is not terminal, read under those machine locks (execution and recovery §4.1); idempotency record, act |
| T6 | Commitment, attempt, adoption record | execution and recovery; with §1.2 items 1–3 and 6; a commitment also compares the committing process's epoch with the current one (§5.1) | execution and recovery; the commitment creates the operation, and an adopt plan's commitment creates it in `completed` with the adoption record (§8.1) |
| T7 | Timeline append | machine row `FOR UPDATE` for every entry in a machine scope: plan, operation or machine-scope fact; operation row `FOR UPDATE` for an entry of a `publish` or `ingest` operation | entry at the machine's `revision_counter + 1`, or at the operation's next event number |
| T8 | Job claim, lease extension and completion; takeover of an `apply-config` operation; a staging claim's takeover, and its abandonment by the sweep, by a takeover with nothing to decrypt (compilation §3.4, §3.5) or by its owner after a refusal, under the owner check (compilation §2.3) | §5.1; for a takeover, its machine row `FOR UPDATE` first (T7); for a staging claim, compilation's conditional `UPDATE` of the claim | operation owner fields; for a job's completion, also its state and terminal event (§8.2); for a takeover, also its state and the ownership-transition entry on the machine's timeline (T7); for a staging claim, the claim and its `ingest` operation together: a takeover moves the operation's owner fields with the claim's, and an abandonment fails the operation with its terminal event (§8.2): `ingestion-abandoned` from the sweep or a takeover, the refusal's own problem from its owner |
| T9 | Recovery-mode entry | key lock; installation state `FOR UPDATE`, its epoch the one the process read at its recovery start (§12.2); every machine row `FOR UPDATE`; the row of each `publish` or `ingest` operation it fails `FOR UPDATE` (T7), in rule 5's order | §12.2 |
| T10 | Migration | `pg_advisory_xact_lock` | §11 |
| T11 | Any other API request (§9.2): inventory, draft creation and discard, ingestion start, marks, takeover and abandonment, plan cancellation, freeze and unfreeze, recovery acts other than entry, accounting decisions, resolutions, takeover requests | key lock; installation state `FOR SHARE` (§12.2); the effect's own locks in rule 5's order, as execution and recovery or compilation define the effect. Leaving recovery mode takes installation state `FOR UPDATE` instead, before it checks that every machine scope is released: it waits for an inventory request, which holds that row `FOR SHARE`, and then sees the machine that request inserted | the effect, idempotency record, act. Ingestion start writes the staging claim and its `ingest` operation `running` together, only if the serving process's epoch is the current one (§5.1), after writing abandoned a due claim whose operation holds its draft revision's natural key (§7.3, compilation §3.5); an abandonment also fails the claim's `ingest` operation `ingestion-abandoned`, with its terminal event (§8.2) |

T7 allocates every revision in a machine scope, for a plan, an operation or a
machine-scope fact alike, from one per-machine counter under the machine row's
lock, which is the machine-scope lock of this contract, so that revision order
within a scope is commit order, as execution and recovery's timeline ordering
rule requires (its §4.1). DS found that taking an observation basis as the
highest committed revision, without such a lock, is unsound under concurrent
writers ([DS §7](../design/research/20260925-dispatch-safety.md#7-limits));
this allocation removes that case. A `publish` or `ingest` operation has no
machine scope; its entries carry an event number allocated under the
operation's row lock, which orders that operation's entries for the event
stream (§8.3) and nothing else.

### 5.1 Fences and claims

Every fenced record (operation ownership, job claims, staging claims) carries
an owner, an owner generation and the epoch identity in which that generation
was issued. A takeover or claim and every owner transition are single
conditional `UPDATE`s:

```sql
-- takeover of an apply-config operation (DB §4.4 row 015),
-- after reading installation_state FOR SHARE
UPDATE operation
   SET owner = $new, owner_gen = owner_gen + 1, owner_epoch = $current_epoch,
       state = 'unresolved'
 WHERE id = $id AND owner_gen = $seen_gen AND $my_epoch = $current_epoch
   AND state IN ('committed', 'sending', 'verifying', 'unresolved');

-- an owner's own transition, after reading installation_state FOR SHARE
UPDATE operation
   SET ...
 WHERE id = $id AND owner = $me AND owner_gen = $my_gen
   AND owner_epoch = $my_epoch AND $my_epoch = $current_epoch;
```

A takeover moves a `committed`, `sending` or `verifying` operation to
`unresolved` and keeps an `unresolved` one `unresolved` under its new owner; it
never touches a terminal one. Those state semantics are execution and
recovery's (its §3.4); this contract only makes them one conditional write,
in the same transaction as the ownership-transition entry its timeline
requires (its §4.1), under the machine row's lock (T8, T7).
The ownership check is part of the write, never a prior `SELECT`: the
check-then-insert control recorded a stale attempt
([DB §4.4](../design/research/20260924-database-semantics.md#44-s4-ownership-transitions),
row 018). The epoch term is §12's: after recovery-mode entry no token issued
before it matches, whatever generation the restored row holds. Every issuance
of ownership stamps the current epoch: a takeover (of an operation or a
staging claim) and a job claim, and equally the first issuance, the
commitment's operation at generation 1 (T6) and the staging claim with its
`ingest` operation at ingestion start (T11). Each therefore also requires the
caller's own epoch to be the current one, read from `installation_state`
`FOR SHARE` in the same transaction (`$my_epoch = $current_epoch`): a process
started before a recovery-mode entry can neither take over, claim a job,
commit a dispatch nor start an ingestion after it. A process's `$my_epoch` is
the epoch it read at its start, with one exception: once the entry of the
process started with the recovery-start flag has committed, that process
adopts the epoch its entry minted as its `$my_epoch`, and keeps the epoch it
read at its start separately, for entry's once-per-start comparison (T9,
§12.2 step 1). No other process ever changes its `$my_epoch`; one started
before the entry is restarted to act again, and an API request it serves that
would issue ownership is refused `503 epoch-superseded`. Entry's own takeovers make that
comparison before entry mints the new epoch, and take over into it (§12.2
steps 1 and 5).

A job claim re-checks eligibility in the `UPDATE`'s own predicate. For a
`publish` job, a `running` job whose lease has lapsed is eligible again
**(choice §17.12)**:

```sql
-- after reading installation_state FOR SHARE; the candidate is locked
-- first, since an UPDATE forms its new row, lease included, before it
-- waits for the row
SELECT id FROM operation
 WHERE kind = 'publish'
   AND (state = 'queued'
        OR (state = 'running' AND lease_until < clock_timestamp()))
 ORDER BY seq LIMIT 1
   FOR UPDATE;                                  -- $id
UPDATE operation
   SET state = 'running', owner = $me, owner_gen = owner_gen + 1,
       owner_epoch = $current_epoch,
       lease_until = clock_timestamp() + $lease
 WHERE id = $id
   AND (state = 'queued'
        OR (state = 'running' AND lease_until < clock_timestamp()))
   AND $my_epoch = $current_epoch;

-- lease extension, by the owner only, while the lease is still live;
-- the row is locked first, so the lease is compared after any wait for it
SELECT 1 FROM operation WHERE id = $id FOR UPDATE;
UPDATE operation
   SET lease_until = clock_timestamp() + $lease
 WHERE id = $id AND owner = $me AND owner_gen = $my_gen
   AND owner_epoch = $my_epoch AND $my_epoch = $current_epoch
   AND state = 'running' AND lease_until > clock_timestamp();
```

An `ingest` operation has no job claim: the staging claim is the single
ownership of an ingestion. `POST /ingestions` creates the operation `running`
in the transaction that creates its claim (compilation §2.3 step 0; T11),
with the claim's owner, owner generation and epoch; it is never `queued`, and
the job claim above, which selects `publish` operations only, never takes it.
Its lease is the claim's, which only the claim's owner extends (compilation
§3.3). A takeover of the claim (compilation §3.4) moves the operation's owner
fields to the claim's new owner and generation in the same transaction (T8),
conditional on the generation the takeover read, and the draft transaction (T1) compares the claim's owner and generation, as
compilation §3.2 requires. A lapsed `ingest` operation is not re-run:
compilation's claim rules decide between takeover and abandonment (§8.2).

Without the re-check, 309 of 400 jobs were claimed more than once and one was
completed twice ([DB §4.5](../design/research/20260924-database-semantics.md#45-s5-queue-claims),
row 020). A claimer whose lease lapsed is superseded by the next claim, and its
late completion is refused at the newer generation, as row 021 claimed a job
after its lease expired and refused the first claimer's completion. A lapsed
lease is never extended by its old owner, and its owner never completes the job
either, even before another claim: the completion, T3 or the failure
transaction, locks the operation's row and requires the same owner predicate
with a live lease, read at that time, as compilation §3.5 fences a staging
claim. An `UPDATE` evaluates its predicate before it waits for a row and does
not evaluate it again when the holder ends without changing the row, so each
owner statement locks the row first. The claim appends the event
`{"type": "claimed", "generation": <n>}` at the generation it took (§8.3). A
publish job's lease is the deployment's `ingestion.lease`, which its owner
extends every `ingestion.heartbeat`, the timers ingestion's claims use
**(choice §17.34)**. Each serving process with a provider runs one publish
worker, which runs one job at a time to its end. It looks for an eligible job
when a publication request that process served commits, and every `heartbeat`,
so it also finds a job another process queued or one whose lease lapsed. A
worker whose claim the epoch term refuses claims nothing until its process
restarts. A process without a provider runs no worker, and its publication
route answers `503 dependency-unavailable` (§9.2). A fence coordinates
workers that use the database; it stops a stale worker's commit, not its work
outside the database (DS §7).

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
draft transaction that references them (compilation §2.3). Key changes stay
with the OpenBao administrator, outside Bronzeward's roles (design §13.7
item 5).

**Step 1, the snapshot.** One read-only `REPEATABLE READ` transaction reads
the draft at the revision its operation is bound to (a draft no longer open,
or moved, is `409 conflict`, as T3 would refuse it), the cluster's contract
and the release's coverage: every machine of the cluster with an import base
after the draft (its entry, else its `Applied` release's, §3.2), each with its
assignment after the draft, or with none, compiling on its base alone
**(choice §17.33)**. A machine with an assignment but no import base is not
covered: a draft that changes or removes its assignment is refused
`422 validation-failed` naming the machine, and a release covering no machine
is refused `422` naming the cluster. Within a layer, the profiles' pins come
first, by the assignment's profile position and then the pin's, then the
assignment's own fragments of that layer by position (compilation §6 step 1).
§3.1's pin and selection rules are checked again over every profile and
assignment the release names, changed or not: a stale pin, or a missing,
removed or wrong-layer name, is refused `422 validation-failed` naming the
profile or machine, the body path and the revision. The snapshot lists the
heads the release uses without a draft entry, which T3 holds unchanged
(§6.2): each covered machine's assignment head (one with no revision
included), its selected profiles, and the selected and pinned fragments. A
machine never assigned has no assignment head for T3 to hold, so an
assignment first created for it after the snapshot is not detected as stale
input; the release commits with the machine compiled on its base alone.

**Steps 2 and 3, the pins.** Each pinned KV version is classified once and
read once, however many sources pin it, and every pin is classified before
any is read. A pin's recorded identity is the creation time of its status
row, the one a KV version holds (dependency monitor §5.1); a classification or
read giving another refuses (compilation §6 step 3). Each status's `began` is a database
time read just before the classifications, or just before the encryptions for
the artifact key: never later than its request began, so T3's re-check (§6.2)
can only refuse more.

**Refusals.** A compilation refusal (composition, validation, contract,
Kubernetes version, ingestion's rules applied while composing) is
`422 validation-failed` naming the rule, the machine, the input and the
redacted paths and message (compilation §8.3); covered machines naming
different Kubernetes versions are `422` with rule `kubernetes`. A dependency
classified `blocked` or `lost`, or whose identity changed (a creation time
other than the recorded one, the first read's, or none), is
`422 validation-failed` naming its provider, object, version and reference.
One the provider's answer leaves `unknown` for another reason is
`503 dependency-unavailable` naming it with class `unknown`: so is a value
read or an encryption the provider refuses, finds absent or answers
unintelligibly after the classification, and an artifact key's first read
with no readable `Date`; and so is a provider that cannot be reached. The
provider's error behind such a read, encryption or unreachable provider is
logged with the operation. A stored source that ingestion's own well-formedness
checks no longer accept is the server's error (`500`), not a refusal:
persistence stores only what ingestion produced and checked, so it means the
stored text and its reference rows disagree. No refusal holds a value.

### 6.2 The commit transaction (T3)

```sql
BEGIN;
SELECT current_epoch FROM installation_state FOR SHARE;
  -- equals the job's owner_epoch
SELECT ... FROM machine WHERE id = ANY($covered) ORDER BY id FOR SHARE;
SELECT 1 FROM machine_state s JOIN machine m ON m.id = s.machine_id
 WHERE m.cluster_id = $cluster ORDER BY s.machine_id FOR UPDATE OF s;
  -- every machine state of the cluster, covered or not
SELECT ... FROM machine_state ...;   -- each one's import base, in a statement
                                     -- of its own (see below)
-- one pass over $changed ∪ $unchanged in id order, one row at a time:
SELECT head_revision FROM <head table> WHERE id = $head FOR UPDATE;
  -- a changed head
SELECT head_revision FROM <head table> WHERE id = $head FOR SHARE;
  -- an unchanged head
SELECT state, revision FROM draft WHERE id = $draft FOR UPDATE;
SELECT ... FROM operation WHERE id = $op FOR UPDATE;
  -- owner, owner_gen, owner_epoch are this worker's (§5.1)
SELECT id, digest FROM release
 WHERE draft_id = $draft AND draft_revision = $bound_revision;
  -- present, digests equal, and this operation already `succeeded` with
  -- that release -> COMMIT with no write and return it (a commit-unknown
  -- retry); present, digests equal, operation not yet `succeeded` -> skip to
  -- the operation's UPDATE and its event with that release; digests
  -- differ -> 409 conflict. Absent: continue.
  -- draft open, revision equals the bound draft revision
  -- $changed head revisions equal the draft's base
  -- $unchanged head revisions equal the snapshot
  -- each covered machine's import base, where the draft carries none,
  --   equals the snapshot's (§4.2), read under the MachineState lock
  -- no machine of the cluster the release does not cover has an import
  --   base: one covered since the snapshot is 409 stale-input naming the
  --   machine, expected "absent"
  -- for each machine whose assignment head is in $changed:
  --   no operation on its scope is committed, sending, verifying or unresolved;
  --   in recovery mode, its scope released in the current epoch
INSERT INTO dependency_status ... ON CONFLICT DO NOTHING;
                                     -- `retained`, per provider object version
                                     -- and creation time without a row, in
                                     -- (provider_object, version, created)
                                     -- order (dependency monitor §3, §5.2); a
                                     -- KV version holds one creation time, so
                                     -- another's row is kept, not added to
SELECT created, class, deletion_observed, recorded_at FROM dependency_status
 WHERE (provider_object, version) IN ($named) ORDER BY id FOR SHARE;
  -- every named version's rows, a concurrent publication's included: a KV
  -- row under another creation time refuses the publication 422 (identity
  -- changed, §6.1); a row recorded not before publication began that
  -- version's classification refuses it, as compilation §6 step 3 does,
  -- when its class is not `retained` or its scheduled deletion is not the
  -- one publication observed (dependency monitor §5.2)
INSERT INTO release ...;             -- unique (draft_id, draft_revision);
                                     -- published by the principal that
                                     -- requested $op, in its role
INSERT INTO release_machine ...;     -- per machine: ciphertext and its digest,
                                     -- the Transit key it was encrypted under,
                                     -- configuration digest, review data; its
                                     -- assignment revision its assignment
                                     -- source's (checked at commit)
INSERT INTO release_source ...;      -- every revision and head revision used:
                                     -- each profile pin a fragment source,
                                     -- each selected name a source with a
                                     -- revision (checked at commit)
  -- per machine, its reproduction dependencies and provenance records
  --   against the sources its composition names (import base, its profiles'
  --   pins, its selected fragments): each record's source among them with
  --   that source's text digest, its reference declared there at the same
  --   version, generation, encoding and kind; every declaration with a
  --   reproduction dependency and a provenance record, both naming the same
  --   occurrences, each provenance record with exactly one outcome and
  --   stored paths (§9.3 review read), every member of each occurrence (a
  --   mapping's member count) with one outcome: one override, or output
  --   records, not counted, since two alias outputs can read the same once
  --   redacted (compilation §5.2, §8.2, §8.3, §9).
  --   A disagreement is the compiled release's defect: 500 internal-error,
  --   nothing committed
INSERT INTO dependency ...;          -- both records, each reproduction source
                                     -- the machine's import base or a release
                                     -- source's fragment revision, each
                                     -- effective one with a reproduction
                                     -- dependency of its machine at the same
                                     -- version and creation time, whose source
                                     -- is in the machine's composition and
                                     -- declares it at that version and
                                     -- generation; and one encryption
                                     -- dependency per machine, naming the
                                     -- machine's key at its ciphertext's key
                                     -- version (checked at commit)
UPDATE <head> SET head_revision_id = ..., head_revision = head_revision + 1,
                  etag_token = ...
 WHERE id = ... AND head_revision = $base;
                                     -- a removal sets head_revision_id = NULL
INSERT INTO <head> ...;              -- per name the draft introduces,
                                     -- head_revision 1; unique (kind, scope,
                                     -- name): a violation is 409 stale-input
UPDATE machine_state SET desired_release = $release, revision = revision + 1
 WHERE machine_id = ANY($covered);   -- rows locked above
UPDATE draft SET state = 'published', release_id = $release,
                 revision = revision + 1, etag_token = ...
 WHERE id = $draft AND state = 'open' AND revision = $bound_revision;
UPDATE operation SET state = 'succeeded', result_release = $release ...
 WHERE id = $op AND owner = $me AND owner_gen = $my_gen
   AND owner_epoch = $my_epoch;
INSERT INTO timeline_event ...;      -- T7 rules
COMMIT;
```

The locks follow rule 5's order, and the checks run after all of them. Heads
are locked in one pass by id, whatever their mode: two publications that each
change a head the other uses unchanged then meet in the same order, one waiting
for the other, where two separate passes (changed heads first, then unchanged)
could each hold one head and wait for the other's. Head identifiers carry their
kind's prefix (§2), so one id order covers every head table. The
release lookup comes first, so that a second commit for a draft revision
already published, after a commit-unknown or from a worker that superseded the
first, meets the existing release before the draft and head checks that the
first commit has made fail. Any failed comparison rolls the transaction back; a
separate transaction then records the publish operation `failed` with the error
of §9.4 and its terminal event (§8.2). `FOR SHARE` on
the unchanged heads is the clause without which DB row 011 committed a release
on a superseded source; row 010 is the same race with the lock, which made the
writer wait. Atomicity held
for an injected error, a client kill, a server kill and a network partition
with the transaction open (rows 006–008, 058, 060). Every machine state of the
cluster is locked, not only the covered ones, so the coverage (a release
covers every machine of the cluster with an import base) cannot change before
COMMIT, and the import bases are read by a statement after the lock: under
READ COMMITTED a locking read that waited re-checks only the row it locked,
not the rows joined to it, so a read in the locking statement could return an
import base committed while it waited, but miss a draft entry or a machine
covered meanwhile. Two publications seeding one KV version meet at the
version's unique index, as two introducing one name do: the second waits for
the first, keeps its row and reads its creation time. The machine-scope check is
the rule execution and recovery states for any assignment change; §1.2 item 2
makes it race-free. A name the draft introduces has no row to lock: two
publications introducing the same name meet at the unique index, where the
second insert waits for the first transaction and fails once it commits, so
the second publication fails `409 stale-input` with the head's expected
revision "absent" and its actual one. Publication writes only the database and
sends nothing to a machine, and stays allowed during recovery mode (§12.2).
T3 consults recovery mode only for an assignment change: execution and
recovery refuses one, in recovery mode, on a scope not released in the current
epoch, because an attempt the restored state does not hold may still be in
flight for the old assignment. T3 reads that scope state from the machine row
it holds `FOR SHARE` and refuses `409 recovery-mode-active`, naming the scope.

The release's covered machines are selected as their `Desired` release in the
same transaction **(choice §17.6)**. Design §11.2 describes a release as
"published cluster/machine desired state" and execution and recovery defines
Desired as the release recorded as selected; neither says when it is selected.
Selection is not authorization: dispatch still needs a plan and an approval.
Every plan binds the `Desired` release current at its creation, and its
commitment compares it under the machine row's lock that T3 also holds, so a
publication leaves each covered machine's uncommitted plans unable to commit
(execution and recovery §2, §3.2 comparison 2).

The release's natural key is `(draft_id, draft_revision)`
**(choice §17.10)**. Each release machine records the plaintext configuration
digest compilation hands over (§1.1), and the release carries a content digest
over its metadata and those configuration digests, not over ciphertext. A
second commit for the same draft revision, after a commit-unknown or from a
second worker, finds that release by the lookup above (the draft's lock
serializes the two commits, and the unique key backs the lookup); it returns the existing
release if the digests match, as DB row 061 returned `existing=true`, and
otherwise fails `409 conflict`, as row 009 refused the same name with different
content.

The release's renderer and contract record (compilation §10.2) is taken
before T3, by the compiler: the contract is the cluster's, refused unless it is
the minor of the machinery module the server was built with; the machinery
version and checksum are that build's own record of the module, and a build
that does not record them, or holds a replaced module, cannot publish (a server
fault, `500`); the Kubernetes version is the kubelet image tag of every covered
machine's validated configuration, which must agree across them and lie in the
machinery's `SupportedWith` window. A refused contract or Kubernetes version is
`422 validation-failed`, naming the rule and the check, never the image or a path
(compilation choices §16.32 to §16.34).

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
- **Reported.** The operator command `bronzeward orphans --config <file>
  [--cluster <id>]`, run on the server host as `migrate` and `token` are, reports
  orphans; no route serves them. It authenticates to the provider with the
  **orphan-report identity** and no other: a static token of its own, in the
  file the configuration's `provider.reportTokenFile` names (mode 0600 or
  tighter, read at use), whose policy grants `read` and `list` on
  `secret/metadata/gen/*` and nothing else: no `secret/data/*`, no other
  metadata path, no Transit **(choice §17.30)**. The command lists
  `secret/metadata/gen/<cluster id>/` and then each `<claim id>/` under it,
  for the cluster given or, without `--cluster`, for every cluster directory
  `secret/metadata/gen/` lists, so that a cluster a restore removed from the
  database, its id now recorded nowhere else, is still found (source 3);
  all of this outside any transaction (rule 1 of §5), and then reads the committed
  reference rows naming those paths and the claims their claim components name
  in one statement, inside a read-only transaction, so that both come from one snapshot under read committed
  (rule 3 of §5): read apart, a draft transaction committing between the two
  reads would show a generation unreferenced and its claim `released`. It
  reports each listed path that no committed reference row names and whose
  claim row does not record `held` or `resumed`, with
  the claim's id and, where its row exists, its state, creation time and
  absolute expiry. The reference rows decide, not the claim's state: a
  `released` claim can leave an orphan (a further mark's generations created
  before an interruption at compilation §2.3 step 8, its earlier payload then
  resumed and released, compilation §3), and recovery-mode entry marks an
  earlier epoch's claims `abandoned` whatever their generations' references
  (§12.2). A claim recorded `held` or `resumed` gives no orphan because its
  ingestion may still be in flight, even past its expiry: the report goes by
  the state the row records, not by the read-time treatment of compilation
  §3.5, since a draft transaction that passed its owner check before the
  expiry may still release the claim (compilation choice §16.30). A recorded
  `abandoned` is final: the sweep and every other abandonment take the
  claim's row lock first and so follow that draft transaction. The
  unreferenced paths of a claim recorded `held` or `resumed` that compilation
  §3.5 already treats as `abandoned`, past its absolute expiry or, for a
  `transient` claim, past its lease, are listed apart, as expired and not yet
  abandoned, with the same fields plus the claim's mode and lease, and never
  counted as orphans: with no controller running, no sweep marks such a claim
  `abandoned`, and omitting them would hide its orphans for as long as none
  runs. A claim row is committed by ingestion start (T11,
  compilation §2.3 step 0) before the first generation is created at step 6,
  so a path without one is source 3 above, or a claim this database never
  held. The report names paths, claim ids, states, modes and times only, never
  a value or custom metadata, on its output, in a log or in an error, and
  changes nothing in the provider or the database. It prints nothing until
  every listing and the statement have succeeded: a provider or database
  failure at any point exits nonzero with an error naming the failed step
  and no path, so a partial report is never printed as a complete one.

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
| `fingerprint_key` | for a keyed fingerprint, the digest key's identity and version (compilation §4.1); otherwise empty |
| `epoch` | the epoch identity at commit; for an entry (T9), the epoch it minted |
| `status`, `location`, `etag`, `body` | the response to replay; never a secret value (design §11.1) |
| `operation_id` | for a `202`, the operation it created |

The fingerprint is SHA-256, except for a route whose body can carry
unextracted input (a draft source update or an ingestion request): its
fingerprint is HMAC-SHA-256 under compilation's digest key, computed inside the
ingestion package (compilation §4.1). The record names the key version, and a
retry's fingerprint is recomputed under that version, not the current one, so a
rotation of the digest key does not turn a retry into
`422 idempotency-key-reused`. If the provider can no longer compute under that
version, the retry cannot be compared and is answered `422` all the same; the
client retries under a new key. T1's `If-Match` refuses a draft update that
already committed. An ingestion whose `ingest` operation is still `running` is
refused by that operation's natural key (§7.3), which names it;
once the operation succeeded its draft write moved the draft, and `If-Match`
refuses the new request, and once it failed the new request is the only one.
A draft entry that compilation refused after its claim existed committed only
its claim and the refusal's records (§7.2), so nothing refuses the new
request: it ingests afresh and, refused again, leaves a second abandoned claim
whose generations are orphans (§6.4). That is the cost of this path; the
stored refusal is not replayed under the new key.
How digests compare across a rotation is open in
compilation §4.1. An unkeyed digest of a low-entropy
secret would be an offline guessing oracle
([E1 §7](../design/research/20260922-secret-ingress-extraction-before-persistence.md#7-limits);
compilation §4.1). A request body is never stored, not even in a transaction
later rolled back: a rolled-back row still reached the write-ahead log
([E1 §4.4](../design/research/20260922-secret-ingress-extraction-before-persistence.md#44-the-forbidden-design-measured)).

### 7.2 Behavior

The record is inserted by the same transaction that commits the request's
effect (T1, T2, T4, T5a–T5c, T9 and T11 of §5), and by no other
**(choice §17.9)**, apart from the draft entry routes' refusals below. A
refused request commits no effect and no record, so a retry is evaluated afresh
against current state; since the refusal changed nothing, re-evaluating it
cannot duplicate anything. Transactions that serve
no request (T3, T6, T7, T8) write no record.

Before running anything, the handler looks the key up and replays a committed
record without executing. A record whose `epoch` is not the current one is not
replayed: its response describes an act that a recovery-mode entry has since
fenced (an approval that authorizes nothing, a job that entry failed), and
executing it afresh would repeat the act without the client asking again. The
request is refused `409 conflict`, naming the stored request and its epoch,
and the client retries under a new key **(choice §17.9)**. The request's
transaction then takes, as its first statement, a transaction-scoped advisory
lock on a hash of the principal and key (`pg_advisory_xact_lock`), and looks
the key up again under it, by the same rules. A
concurrent duplicate therefore waits for the first transaction to end. The
unique index on principal and key backs the lock. The draft entry routes run
ingestion before T1, outside that lock; their claim carries the principal and
key under a partial unique index over live claims, so of two concurrent
duplicates only one creates a claim and ingests, and the other is answered as
below.

The draft entry routes are also the one exception to "a refusal commits no
record", because their ingestion persists a staging claim before T1 that a
refusal does not undo. Their claim records the principal and the idempotency
key. A `4xx` refusal after the claim exists (compilation's `422`, or T1's
`412` or `409`) commits the
idempotency record with the refusal's response, and the act of §10.5 naming
the refused request, in the transaction that records the claim's outcome: the
claim and its provider generations persist, so the audit records who caused
them. A retry therefore replays the refusal and ingests nothing. A `5xx`
refusal records nothing; a retry ingests afresh once the earlier claim has
ended, as below.
A retry that finds a claim for its key but no record looks at the claim's
lease (compilation §3). While the lease is live, the first request may still be
ingesting, and the retry answers `409 conflict` naming the request in progress;
the client retries later and then gets the first request's replay. Once the
lease has lapsed, the earlier attempt has ended without a commit (a `503`
inside T1, a killed handler), and the retry abandons that claim (compilation
§3.5) and ingests afresh. Each key thus holds at most one live claim, and an
abandoned claim's generations are reported as orphans (§6.4).

| Situation | Response |
| --- | --- |
| New key | executed; record committed with the effect |
| Same key, same fingerprint, record from the current epoch | the stored status, headers and body, with `Idempotent-Replayed: true`; nothing executes |
| Same key, record from an earlier epoch | `409 conflict` naming the stored request and its epoch; nothing executes |
| Same key, other fingerprint | `422 idempotency-key-reused` |
| Same key while the first request's transaction is open | waits on the key's lock; then replays if the first committed, or executes if it rolled back |
| Same key after the request that used it was refused | executed afresh; on a draft entry route refused after ingestion, the stored refusal replayed |
| Same key after a restore that removed the record | executed afresh (§12.4) |

The waiting row follows PostgreSQL's advisory-lock semantics and is not
measured for requests: DB row 013 retried the same key sequentially and got the
first operation back with `created=false`
([DB §4.3](../design/research/20260924-database-semantics.md#43-s3-unique-operation-intent)),
and the migration runners waited on a transaction-scoped advisory lock
(DB §4.6), but no row raced two requests with one key.

Records are kept for the PoC (§3). An idempotency key prevents duplicate
intent in the manager; it does not make a remote effect exactly once (design
§12.5).

### 7.3 Natural keys

Some effects also have a key of their own, which holds even when a client
retries with a new idempotency key:

| Effect | Natural key | On a second request |
| --- | --- | --- |
| Release | `(draft_id, draft_revision)` | the existing release if the digests match, else `409 conflict` (§6.2) |
| Publish operation | at most one `queued` or `running` `publish` operation per `(draft_id, draft_revision)`, by a partial unique index over `kind = 'publish'` only | the existing `publish` operation, `202` |
| Ingest operation of `POST /ingestions` | at most one `running` `ingest` operation per `(draft_id, draft_revision)` it binds, by a partial unique index over `kind = 'ingest'` only: only one of them could write that draft revision, and each would write the provider | `409 conflict` naming the existing operation |
| Operation | one per plan, by unique index on the plan, created at the commitment (§8.1) | execution and recovery's comparison 0 refuses a second commitment (DS row 009) |
| Approval | one per plan and epoch, by unique index on `(plan_id, epoch)` (design §13.7 item 3); a revoked approval makes its uncommitted plan `revoked` (§8.1), and only an unexpired plan approved in an earlier epoch is approved again, in the current one (execution and recovery §2) | `409 conflict` |
| Active operation per machine scope | partial unique index over `committed`, `sending`, `verifying`, `unresolved` | execution and recovery's refusal (DB row 012; DS row 010) |
| Active operation per rollout scope | at the PoC rollout limit of one, a partial unique index on the rollout scope over `committed`, `sending`, `verifying`, `unresolved` (execution and recovery §3.2, comparison 5); not evidenced there | execution and recovery's refusal |
| Machine | its identity key: `smbiosUuid` or `talosNodeId`, exactly one of which `POST /machines` requires, each by its own unique index across the installation | `409 conflict` naming the existing machine |
| Cluster | its `talosClusterId`, required by `POST /clusters`, by unique index across the installation | `409 conflict` naming the existing cluster |

The publish and ingest indexes are separate, each over its own kind, so a
`publish` and an `ingest` operation bound to one draft revision never collide,
and T2 returns only a `publish` operation as the existing one.

The machine key is fixed at inventory and never changes: the operator supplies
the machine's SMBIOS UUID when it reports one (`talosctl get
systeminformation`), and otherwise its Talos node ID (`talosctl get
identity`); a body with both, or neither, is `400 invalid-request`
(execution and recovery's choice §10.26). The key makes one supplied value
one record, so one coordination scope: two inventory requests for it under
different idempotency keys cannot both commit. It is design §8.5's detection
of a duplicate SMBIOS UUID, never a merge. Design §4.4 calls hardware evidence
"not an infallible primary key": SMBIOS's nil and all-ones values are
accepted as a UUID, so a second machine reporting the value of one already
recorded, like a clone sharing a UUID, cannot be inventoried under it;
recorded under its node ID instead, it is refused at every comparison below,
since its node reports a UUID, so it cannot be used in the PoC.
A Talos node ID is an opaque string accepted as Talos prints it (44
alphanumeric characters on the nodes read for choice §10.26); it is compared
byte for byte, never normalised. The
cluster key is the Talos cluster ID the operator reads from one of its nodes
(`talosctl get info`), the URL-safe, padded base64 encoding of 32 bytes, also
compared byte for byte; a second cluster generated from the same Talos
secrets bundle shares it and is refused `409 conflict`. The indexes cannot catch a record entered with a
wrong value, or under the node-ID key for a machine that reports a UUID;
execution and recovery refuses either at dispatch instead. There, the node's
observed identity must match this record and its cluster's before any send
(its §3.2 comparison 3) and before a scope is `ready` after a restore (its
§7.4), so a dispatch whose evidence read shows a node that is not the recorded
machine, or a record entered with a wrong value, is refused before it sends.
A node swapped between that read and the send is a residual its choice §10.26
states.

## 8. Asynchronous operations

Design: [§11.1](../design/Talos_Configuration_and_Machine_Management_Design.md#111-api-style),
[§15.2](../design/Talos_Configuration_and_Machine_Management_Design.md#152-operation-timeline).

Long work is an **operation**: a durable record with an `op` identifier. A
request that starts one returns it in `202 Accepted` with
`Location: /api/v1/operations/<id>`; the identifier is generated and committed
with the idempotency record before the response is written, so a retry of the
request returns the same handle. The controller creates the operation of a
plan itself, at the dispatch commitment (§8.1).

| Kind | Created by | States |
| --- | --- | --- |
| `publish` | `POST /drafts/{id}/publications` | job states (§8.2) |
| `ingest` | `POST /ingestions` (import, drift adoption), `running` with its staging claim (§5.1); a mark or takeover on a staged ingestion answers with that ingestion's operation | job states (§8.2), never `queued` |
| `apply-config` | the dispatch commitment of an approved plan | execution and recovery's operation states |
| `adopt` | the commitment of an approved plan with `operation: adopt`, which records the adoption and sends nothing | created directly in `completed`, with the adoption record as its outcome (execution and recovery §6.3) |

### 8.1 Plans and their operations

A plan's operation is created by the dispatch commitment, not with the plan
**(choice §17.11)**. Until then the plan resource is the handle: design §11.1
has the client create a plan and record an approval, "the controller then
dispatches the approved plan (§12.7), and the client follows the operation it
creates", and its `GET /api/v1/operations?plan=...` answers an empty list until
the commitment and the one operation after it. Design §12.5 asks for "durable
intent before send", which the commitment's operation row is. The commitment
transaction inserts the operation, links the plan state to it and moves the
plan to `committed`; the unique index on the operation's plan (§7.3) is
execution and recovery's comparison 0, which DS row 009 exercised when it
refused a second executor on "an operation for plan A exists".

The plan's state is a mutable projection beside the immutable binding
(PlanState, §3); its semantics are execution and recovery's:

| Plan state | Entered by |
| --- | --- |
| `proposed` | plan creation (T4) |
| `approved` | the approval (T5a) |
| `committed` | the commitment, which creates the operation (T6); terminal for the plan |
| `revoked` | before the commitment: a revocation of the approval currently authorizing it (T5b) or of that approval's identity (T5c); terminal |
| `cancelled` | before the commitment: a cancellation (T11); terminal |
| `expired` | before the commitment: the plan's expiry; terminal |

A cancellation and an approval revocation write the projection in their own
transaction. Expiry and a revocation of the approving identity are evaluated
by the server clock and the principal's `revoked` flag whenever the plan is
read or locked, and are written by the next transaction that locks the plan
state; execution and recovery's comparison 1 refuses such a plan either way.
An `approved` plan whose approval is from an earlier epoch stays `approved`
but cannot commit until an `approver` approves it again in the current epoch
(execution and recovery §2; T5a). After `committed`, the operation carries the
state; a revocation, cancellation or expiry after it is recorded against the
plan and reaches the operation through execution and recovery's §3.3.

T5b, T5c and a cancellation do not change the operation themselves. A
`committed` operation always has an owner holding a lease (§5.1), and the
owner's next attempt transaction fails comparison 1. That transaction rolls
back, and a separate T6 transaction, under the machine row and operation locks,
records the operation `unresolved` with its timeline entry (execution and
recovery §4, `committed` → `unresolved`). With no attempt ever committed and
none possible, the same transaction records it `cancelled` (`unresolved` →
`cancelled`), which releases its scope. An owner that stops before that loses
its lease: the takeover (T8) records the operation `unresolved`, and the new
owner performs the same classification. The scope is therefore released
without operator action.

### 8.2 Job states

| State | Meaning |
| --- | --- |
| `queued` | Accepted; no worker holds it. A `publish` operation only. |
| `running` | A worker holds it under a fence and lease (§5.1); an `ingest` operation, the owner of its staging claim. |
| `succeeded` | Terminal. `result` names the release or draft produced. |
| `failed` | Terminal. `error` is a problem document (§9.4). |

Every transaction that moves a `publish` or `ingest` operation to `succeeded`
or `failed` appends its terminal event (T7) in the same transaction, so the
event stream (§8.3) shows how it ended. That includes every abandonment below
and recovery-mode entry (T9), not only the commits of T1 and T3.

A worker whose lease lapses is superseded by the next claim; its late commit
is refused by the fence. A `publish` job is safe to run again, because its
commit is keyed by draft revision (§6.2). An `ingest` operation is not re-run:
its input was held in memory or in staging, and compilation's claim rules
decide between takeover and abandonment (compilation §3.4, §3.5). Every
transaction that writes a staging claim `abandoned` fails that claim's
`ingest` operation, if one is still `running`, with `ingestion-abandoned` in
the same transaction: the operator's abandonment (T11), an ingestion start
whose draft revision's running operation has a claim that is due (T11), the
sweep that writes a lapsed transient claim or an expired one abandoned and a takeover with
nothing to decrypt (T8; compilation §3.4, §3.5), and recovery-mode entry (T9)
**(choice §17.12)**. An owner that refuses its own ingestion (compilation
§2.3), a failed node read of a `source: machine` ingestion (§3.3) among them,
abandons its claim too, and fails the operation with that refusal's problem
in the same transaction instead.

Recovery-mode entry fails every `queued` or `running` `publish` job with
`recovery-mode-entered`, and every `running` `ingest` operation with
`ingestion-abandoned` as it abandons its claim (§12.2) **(choice §17.12)**.

### 8.3 The resource and its progress

```json
{
  "id": "op_zcfwgr7trb76z3zmsndrnk5mcm",
  "kind": "publish",
  "state": "running",
  "epoch": "ep_bqeknkmarvikuy7ofil2okekgi",
  "lastEvent": 3,
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
`<epoch>:<revision>`, where the revision is the entry's machine revision for
a plan operation and its event number for a `publish` or `ingest` operation
(§5, T7); either increases along one operation's events. A client resumes
with `Last-Event-ID`; an id from another epoch cannot be resumed, so the
server sends a `reset` event and the timeline from its start. The server ends
a stream no later than the `exp` of the token that opened it; the client
reconnects with `Last-Event-ID` and a fresh token, verified as any request
(§10.1), so an identity revocation, a denied subject or a changed group takes
effect on a stream within the token's lifetime. Epochs are compared by equality only; the entry
time of each `RecoveryEpoch` row orders them for display. A plan operation's
events begin at its commitment and link its plan and approval, whose entries
are on the machine's timeline (`GET /machines/{id}/timeline`). Polling the
resource is always supported (design §11.1). WebSocket is not offered in the
PoC **(choice §17.14)**.

An `ingest` operation's events are `{"type": "started"}`, written with the
operation; `{"type": "staged"}` once an encrypted claim's envelope is stored;
`{"type": "taken-over", "generation": <n>}` when a takeover (compilation §3.4)
resumes the claim at owner generation `n`; `{"type": "resume-failed", "code":
"dependency-unavailable"}` when that owner cannot decrypt the envelope, the
claim left `resumed` to its lease; and one terminal event, `{"type":
"succeeded", "importBaseRevision": "<ibr identifier>"}` or `{"type": "failed",
"code": "<problem code>"}`. A takeover with nothing to decrypt writes the
failure with `"cause": "nothing-to-decrypt"`. No event carries input text. The operation resource shows its stored row: an operation
whose claim a read already treats as abandoned (compilation §3.5) stays
`running` here until a sweep writes it: the next periodic sweep, or a later
one if that sweep fails or waits for a lock. An ingestion start of the same
draft revision writes it first (T11), so the natural key (§7.3) never refuses
on a claim already treated as abandoned.
`GET /ingestions/{id}` reports the claim's state as read.

A `publish` operation's events are `{"type": "queued"}`, written with the
operation (T2); `{"type": "claimed", "generation": <n>}` at each job claim
(§5.1), so a job claimed again after its lease lapsed shows each generation;
and one terminal event, `{"type": "succeeded", "release": "<release
identifier>"}` (T3) or `{"type": "failed", "code": "<problem code>"}` (§6.2).
No event carries input text.

## 9. API

Design: [§11](../design/Talos_Configuration_and_Machine_Management_Design.md#11-northbound-web-api),
[§13.7](../design/Talos_Configuration_and_Machine_Management_Design.md#137-poc-identity-and-approval-policy).

### 9.1 Conventions

- JSON under `/api/v1`, reached by clients over HTTPS. TLS may terminate in
  front of the server (an ingress or API gateway); the hop behind it is the
  deployment's choice: plain http on a network it protects, TLS per service,
  or encryption by the network layer. The PoC server listens on plain http.
  Unknown request fields are refused **(choice §17.14)**.
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
- Every response to an authenticated request carries `Bronzeward-Epoch` with
  the current epoch identity, and `Bronzeward-Recovery-Mode: true` while
  recovery mode is in effect. A `401`, a `403 identity-revoked` and the
  liveness probe carry neither: they answer a caller who has not
  authenticated, and the epoch is data (§10).

### 9.2 Resources and routes

"Any role" means any authenticated principal holding at least one role: every
grant confers read (design §13.7 item 2). A route marked *EaR* has its
semantics in execution and recovery; this contract fixes its route, role,
idempotency and conflict behavior.

| Route | Result | Roles |
| --- | --- | --- |
| `GET /clusters`, `/clusters/{id}`, `/machines`, `/machines/{id}`, `/machines/{id}/observations`, `/machines/{id}/timeline` | 200 | any role |
| `GET /fragments[/{id}]`, `/fragments/{id}/revisions`, `/fragment-revisions/{id}`; the same for profiles and assignments | 200 | any role |
| `GET /drafts[/{id}]`, `/ingestions/{id}`, `/releases[/{id}]`, `/releases/{id}/machines/{m}/review` | 200 | any role |
| `GET /plans[/{id}]`, `/approvals/{id}`, `/operations[/{id}]`, `/operations/{id}/events`, `/acts`, `/recovery` | 200 | any role |
| `GET /dependencies[/{id}[/releases\|/alerts]]`, `/dependency-alerts` ([dependency monitor §7.2](dependency-monitor.md#72-read-routes)) | 200 | any role |
| `POST /ingestions` (import or drift adoption of a machine's configuration), with `If-Match` carrying the named draft's ETag, which the operation binds | 202, `ingest`, created `running` with its staging claim (§5.1) | `author`, human only (§10.3) |
| `POST /ingestions/{id}/marks`, `/takeovers` (a further mark on a staged ingestion; compilation's explicit operator recovery request, §3.4 there) | 202, the ingestion's `ingest` operation | `author`, human only (§10.3) |
| `POST /ingestions/{id}/abandonments` (an operator's abandonment, compilation §3.2), which fails the ingestion's `ingest` operation (§8.2) | 200 | `author`, human only (§10.3) |
| `POST /clusters`, `POST /machines` (inventory for an existing cluster; a machine with its Talos endpoint, §3.3, and its `platform`, §3) | 201 | `author`, human only (§10.3) |
| `POST /machines/{id}/talos-endpoints` (replace a machine's Talos endpoint, §3.3) | 201 | `author`, human only (§10.3) |
| `POST /drafts` | 201, ETag | `author` |
| `PUT` or `DELETE /drafts/{id}/fragments/{name}`, `/profiles/{name}`, `/assignments/{machine}` | 200, ETag | `author`; `If-Match` |
| `POST /drafts/{id}/discard` | 200 | `author`; `If-Match` |
| `POST /drafts/{id}/publications` | 202, `publish`, created `queued` (§5.1), or the one already queued or running for the draft revision (§7.3); the body is the operation resource, with its `Location`. `503 dependency-unavailable` from a process without a provider | `publisher`; `If-Match` |
| `POST /plans` with `operation: apply-config` | 201 | `publisher` (EaR) |
| `POST /plans` with `operation: adopt` | 201 | `publisher` (EaR; §10.3) |
| `POST /plans/{id}/cancellations` | 200 | the creating identity, under the role it created the plan with; `approver`; `recovery-admin` (EaR; design §13.7 item 6) |
| `POST /plans/{id}/approvals` | 201 | `approver`, human only (EaR) |
| `POST /approvals/{id}/revocations` | 201 | `approver`, `recovery-admin` (EaR) |
| `POST /machines/{id}/freezes` | 201 | `author`, `publisher`, `approver`, `recovery-admin` (EaR) |
| `POST /machines/{id}/unfreezes` | 201 | `approver` (EaR) |
| `POST /recovery/entries`, `/recovery/exits`, `/recovery/scopes/{machine}/marks`, `/recovery/scopes/{machine}/releases` | 201 | `recovery-admin`, human only (EaR; §12) |
| `POST /recovery/accountings` (the post-restore accounting decision, covering one or more scopes) | 201 | `recovery-admin`, human only (EaR) |
| `POST /operations/{id}/attempts/{attempt}/accountings`, `/operations/{id}/takeovers`, `/operations/{id}/resolutions` | 201 | `recovery-admin`, human only (EaR) |
| `POST /identity-revocations` | 201 | `recovery-admin`, human only (§10.4) |

Every `POST`, `PUT` and `DELETE` needs an `Idempotency-Key` (§7). There is no
route that dispatches, none that issues, rotates or lists automation tokens,
none that revokes a token except by revoking its service identity (T5c,
§10.4), and none that grants roles: those requests reach no handler and answer `404` (design
§13.7 items 1 and 2). How a staged ingestion is reviewed, and when its `ingest`
operation ends, belong to the operator review pause
([ginsys/bronzeward#104](https://github.com/ginsys/bronzeward/issues/104)); this
contract fixes those routes' roles, idempotency and records.

An adoption approval is requested as a plan with `"operation": "adopt"`, which
binds what execution and recovery's adoption section lists, is approved by an
`approver` like any plan, and whose commitment records the adoption, creating
the plan's `adopt` operation directly in `completed`, and sends nothing
**(choice §17.15)**. Its creator is a `publisher`, as for every plan
**(choice §17.22)**.

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
identifiers. The automation identity `idn_5u4k6llt7jsktfhcfv35xmdetu` creates
the plan; the approving human is its responsible human (§10.2) and authored one
of the draft's revisions, so the approval is marked with both reasons (§10.5):

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

{}

HTTP/1.1 201 Created
Location: /api/v1/approvals/apr_2ztr33rjgnabf5zxbwwf7c47vy

{"id": "apr_2ztr33rjgnabf5zxbwwf7c47vy",
 "plan": "pln_f645lvrsgsehfn6fuboigpcawy",
 "approver": {"principal": "idn_6woutisn7uensexh3kk2qlz6ma", "role": "approver"},
 "epoch": "ep_bqeknkmarvikuy7ofil2okekgi",
 "selfApproval": {"marked": true, "reasons": ["owned-automation", "authored-change"]}}

GET /api/v1/plans/pln_f645lvrsgsehfn6fuboigpcawy

HTTP/1.1 200 OK

{"id": "pln_f645lvrsgsehfn6fuboigpcawy",
 "revision": 1,
 "state": "approved",
 "committedOperation": null,
 "release": "rel_fgqvcvz3ck7h7234ljgdbzsj6m",
 "machine": "mch_tqhcznunhyle4hnxru5hkt35uq",
 "operation": "apply-config", "mode": "no-reboot",
 "approval": "apr_2ztr33rjgnabf5zxbwwf7c47vy",
 "createdBy": {"principal": "idn_5u4k6llt7jsktfhcfv35xmdetu", "role": "publisher"},
 "expiresAt": "2026-09-26T21:14:02Z"}
```

`committedOperation` stays `null` until the commitment (§8.1). The plan's
other bound values and its plan-time evidence (execution and recovery, plan
binding) are omitted here.

Creating a draft, and later updating one of its fragments after five other
updates have taken the draft to revision 6. A request body that can hold a
secret value is fingerprinted with a keyed digest and never stored (§7.1); the
response carries only the sanitized document:

```http
POST /api/v1/drafts
Idempotency-Key: 0c6a55e2-8f3b-4a17-9d20-6b1e4f7a3c98

{"cluster": "cl_oxbgrzprzpvnecj5ve3jht3dha", "title": "registry mirror"}

HTTP/1.1 201 Created
Location: /api/v1/drafts/drf_2rmpezm5rfx47azsgmp66z457a
ETag: "1-ebup2hadf76apv5qcz6j6ai2q4"

{"id": "drf_2rmpezm5rfx47azsgmp66z457a",
 "cluster": "cl_oxbgrzprzpvnecj5ve3jht3dha",
 "state": "open", "revision": 1, "entries": []}

PUT /api/v1/drafts/drf_2rmpezm5rfx47azsgmp66z457a/fragments/registries
Idempotency-Key: 6f1d8b40-2a7e-4c53-b9e1-0d4c7a2f5e36
If-Match: "6-o3nj2wivrgbpbet4kq43iz4hlq"

{"layer": "cluster",
 "document": "machine:\n  nodeLabels:\n    example.test/token: <value>\n",
 "marks": ["doc[0]/machine/nodeLabels/example.test~1token"]}

HTTP/1.1 200 OK
ETag: "7-shw6tpirbqvgj3qjuv2hicf6vm"

{"draft": "drf_2rmpezm5rfx47azsgmp66z457a",
 "entry": {"kind": "fragment", "name": "registries",
           "head": "frg_rgkebwvneg6mxhid62gec5difi", "base": 3,
           "revision": "frv_sqb745zrpl2xltek22ai7sbdue",
           "document": "machine:\n  nodeLabels:\n    example.test/token: !bwref registry/example-token\n"},
 "ingestion": "ing_iehib5tgttedjuggkawygvlnym"}
```

`<value>` stands for the value the operator submits; it appears in no stored
record or response. `DELETE` on the same route proposes the removal, with no
body. `marks` are compilation §2.2 paths.

The body's optional `declarations` member holds the compilation §5.2
declarations of the `!bwref` tags the document already carries, as
`{"references": {"<name>": {"kind", "version", "encoding"}}, "embedded":
[{"path", "format"}]}`. A marked value is extracted to a new generation under
this update's claim; a carried reference resolves to the generation of a
reference row of the draft's cluster, a fragment revision's or an import
base's, with the same name, kind and version. A carried name that matches no
such row, or rows with different generations, is `422 validation-failed` with
`"rule": "unknown-reference"` and the `"reference"` name. The update ingests
under a claim of kind `draft-update` that names the draft and stages
transiently only (compilation §3): no envelope is written, so an interrupted
update is never taken over, and its retry ingests afresh. The answer's
`document` is the sanitized text, and `ingestion` names the claim; a refusal
after the claim exists names it too. The ingestion resource of that claim has
`kind` `draft-update`, its `draft`, and `machine` and `operation` `null`.

A profile pins fragment revisions in order; an assignment selects profiles,
then fragments per layer, by name (§3.1). Neither body can hold a secret
value, so neither route ingests: each is a plain T1 under the SHA-256
fingerprint (§7.1). A name the draft introduces has `"head": null` and
`"base": null`; a removal has `"revision": null`:

```http
PUT /api/v1/drafts/drf_2rmpezm5rfx47azsgmp66z457a/profiles/workers
Idempotency-Key: 3d5f0e9a-71c2-4b8e-a6d4-2f9c1b7e0a53
If-Match: "7-shw6tpirbqvgj3qjuv2hicf6vm"

{"fragments": ["frv_sqb745zrpl2xltek22ai7sbdue"]}

HTTP/1.1 200 OK
ETag: "8-a4kc2xq7zxgbgnmwvtqwhk3f5e"

{"draft": "drf_2rmpezm5rfx47azsgmp66z457a",
 "entry": {"kind": "profile", "name": "workers", "head": null, "base": null,
           "revision": "prv_6h2o4vzxkdyb7xg5qwsl3fjrua"}}

PUT /api/v1/drafts/drf_2rmpezm5rfx47azsgmp66z457a/assignments/mch_tqhcznunhyle4hnxru5hkt35uq
Idempotency-Key: 9b7e2c14-0f6d-4a38-8e51-c3a9d2f47b06
If-Match: "8-a4kc2xq7zxgbgnmwvtqwhk3f5e"

{"profiles": ["workers"], "fragments": {"cluster": ["registries"]}}

HTTP/1.1 200 OK
ETag: "9-pq3vylwbn4ijc5mc6z6yq2dmzi"

{"draft": "drf_2rmpezm5rfx47azsgmp66z457a",
 "entry": {"kind": "assignment", "machine": "mch_tqhcznunhyle4hnxru5hkt35uq",
           "head": "asg_kyzk4xw2xq2jtc3pnu6d5vbz3e", "base": 2,
           "revision": "asr_e7v7jq6g4e3tsx2wq5ynldkb3a"}}
```

Here `registries` must carry the `cluster` layer, as its fragment entry does. Discarding a draft takes an
empty object and answers the draft, `discarded`:

```http
POST /api/v1/drafts/drf_2rmpezm5rfx47azsgmp66z457a/discard
Idempotency-Key: 5c0a8f3e-6b14-4d7a-9e2c-71f5b8d3a046
If-Match: "9-pq3vylwbn4ijc5mc6z6yq2dmzi"

{}

HTTP/1.1 200 OK

{"id": "drf_2rmpezm5rfx47azsgmp66z457a",
 "cluster": "cl_oxbgrzprzpvnecj5ve3jht3dha", "title": "registry mirror",
 "state": "discarded", "revision": 10, "entries": [...]}
```

A draft's `entries` list its import base entries in machine order, then its
fragment, profile and assignment entries in that order, by name or machine,
each as the update route answered it. A head answers its current revision
(`null` once removed) and head revision, with `"<headRevision>-<token>"` as
its ETag (§4.1). Its `revisions` are every revision of its name, or its
machine, published or not, in identifier order. A revision answers its
content: a fragment revision its sanitized `document`, `layer` and
`declarations` (each reference row's name with its kind, version and encoding,
never its generation path, and its `embedded` documents), a profile
revision its `fragments` pins in order, an assignment revision its `profiles`
and `fragments` per layer. The discarded draft moved no head, so `registries`
still answers head revision 3, the base the draft edited from, while the
draft's own revisions stay readable by id:

```http
GET /api/v1/fragments/frg_rgkebwvneg6mxhid62gec5difi

HTTP/1.1 200 OK
ETag: "3-m3oxmlfh6phr7aigshdydcb4ji"

{"id": "frg_rgkebwvneg6mxhid62gec5difi",
 "cluster": "cl_oxbgrzprzpvnecj5ve3jht3dha", "scope": "cluster",
 "name": "registries", "layer": "cluster",
 "revision": "frv_lc4wfn2tqz3mxu7h5kdo6ybvpe", "headRevision": 3,
 "createdAt": "2026-09-20T08:30:11Z"}

GET /api/v1/assignment-revisions/asr_e7v7jq6g4e3tsx2wq5ynldkb3a

HTTP/1.1 200 OK

{"id": "asr_e7v7jq6g4e3tsx2wq5ynldkb3a",
 "cluster": "cl_oxbgrzprzpvnecj5ve3jht3dha",
 "machine": "mch_tqhcznunhyle4hnxru5hkt35uq",
 "profiles": ["workers"], "fragments": {"cluster": ["registries"]},
 "author": "idn_5u4k6llt7jsktfhcfv35xmdetu",
 "createdAt": "2026-09-26T09:12:40Z"}
```

Starting an import, and reading a release and a machine:

```http
POST /api/v1/ingestions
Idempotency-Key: 8e2b1c64-5d0a-4f97-a3c8-19b7e6d4f052
If-Match: "7-shw6tpirbqvgj3qjuv2hicf6vm"

{"kind": "import", "machine": "mch_tqhcznunhyle4hnxru5hkt35uq",
 "draft": "drf_2rmpezm5rfx47azsgmp66z457a",
 "source": "machine", "staging": "transient", "marks": []}

HTTP/1.1 202 Accepted
Location: /api/v1/operations/op_f6hekztxvswfhzqoe2wbyqnbtq

{"operation": "op_f6hekztxvswfhzqoe2wbyqnbtq",
 "ingestion": "ing_4ycffhy7bf4o2w6pz5b4r75hmu"}

GET /api/v1/ingestions/ing_4ycffhy7bf4o2w6pz5b4r75hmu

HTTP/1.1 200 OK

{"id": "ing_4ycffhy7bf4o2w6pz5b4r75hmu", "kind": "import",
 "mode": "transient", "state": "held",
 "machine": "mch_tqhcznunhyle4hnxru5hkt35uq",
 "draft": "drf_2rmpezm5rfx47azsgmp66z457a",
 "operation": "op_f6hekztxvswfhzqoe2wbyqnbtq", "ownerGeneration": 1,
 "leaseUntil": "2026-09-26T09:20:15Z", "expiresAt": "2026-09-26T09:30:00Z",
 "createdAt": "2026-09-26T09:20:00Z"}

GET /api/v1/releases/rel_fgqvcvz3ck7h7234ljgdbzsj6m

HTTP/1.1 200 OK

{"id": "rel_fgqvcvz3ck7h7234ljgdbzsj6m",
 "cluster": "cl_oxbgrzprzpvnecj5ve3jht3dha",
 "draft": "drf_2rmpezm5rfx47azsgmp66z457a", "draftRevision": 7,
 "operation": "op_xq2mbn6fwt5l4ry3hk7cdzjv4e",
 "publishedBy": {"principal": "idn_5u4k6llt7jsktfhcfv35xmdetu", "role": "publisher"},
 "publishedAt": "2026-09-26T09:14:05Z",
 "renderer": {"contract": "v1.13", "machineryVersion": "v1.13.6",
              "machineryChecksum": "h1:2rBcdYQ4m1u3oPmvbMQw3F9dZb8i0EwQnJ6y5Kx8sJ0=",
              "kubernetesVersion": "v1.36.0"},
 "sources": [{"kind": "fragment", "head": "frg_rgkebwvneg6mxhid62gec5difi",
              "revision": "frv_sqb745zrpl2xltek22ai7sbdue", "headRevision": 4}],
 "machines": [{"machine": "mch_tqhcznunhyle4hnxru5hkt35uq", "mode": "metal",
               "review": "/api/v1/releases/rel_fgqvcvz3ck7h7234ljgdbzsj6m/machines/mch_tqhcznunhyle4hnxru5hkt35uq/review"}]}

GET /api/v1/releases/rel_fgqvcvz3ck7h7234ljgdbzsj6m/machines/mch_tqhcznunhyle4hnxru5hkt35uq/review

HTTP/1.1 200 OK

{"release": "rel_fgqvcvz3ck7h7234ljgdbzsj6m",
 "machine": "mch_tqhcznunhyle4hnxru5hkt35uq", "mode": "metal",
 "configuration": "machine:\n  type: worker\n  token: <redacted:schema>\n  ...",
 "provenance": [{"reference": "registry/example-pass", "version": 3,
                 "source": {"revision": "frv_sqb745zrpl2xltek22ai7sbdue", "digest": "4fc8...eeb8",
                            "path": "doc[0]/machine/registries", "occurrence": 0},
                 "output": "doc[0]/machine/registries/config/<redacted>/auth/password"}]}

GET /api/v1/machines/mch_tqhcznunhyle4hnxru5hkt35uq

HTTP/1.1 200 OK

{"id": "mch_tqhcznunhyle4hnxru5hkt35uq",
 "cluster": "cl_oxbgrzprzpvnecj5ve3jht3dha",
 "hardware": {"smbiosUuid": "...", "talosNodeId": null, "serial": "..."},
 "platform": "metal",
 "talosEndpoint": "10.55.0.3:50000",
 "desired": "rel_fgqvcvz3ck7h7234ljgdbzsj6m",
 "applied": {"release": "rel_uxpkmwd6ckxmj4z75j7y2mcxb4", "source": "operation"},
 "frozen": false, "scopeState": "normal", "openDrift": null}
```

`source` is `machine` to read the configuration from the node, at the
machine's Talos endpoint with its cluster's Talos access (§3.3), or `document`
with the text in a `document` field. The ingestion resource is its staging
claim as a read treats it (compilation §3.5): `state` is `abandoned` once the
claim is past its absolute expiry, or a transient one past its lease, whether
or not the sweep has written it. It never carries the owner string, the
payload or its digest. `POST /ingestions/{id}/abandonments` takes `{}` and
answers 200 with the resource; a claim already `released` or `abandoned`, as
a read treats it, is `409 conflict`. `POST /ingestions/{id}/takeovers` takes
`{}` and answers 202 with `{"operation": "<op identifier>", "ingestion":
"<ingestion identifier>"}` and a `Location` at the operation; a claim that is
transient, ended as a read treats it, still leased or of an earlier epoch is
`409 conflict`, and nothing changes. `scopeState` is `normal`, or one of
execution and recovery's recovery scope states while recovery mode is in
effect.

A release answers its renderer and contract record (compilation §10.2) and,
per source, the head, the revision the release left it at (`null`: removed)
and the head revision; per machine, the mode it was validated in and its
review route. `GET /releases` lists releases in identifier order, each as the
item read answers it. The review answers the redacted configuration and the
provenance records publication stored (compilation §8.2, §8.3, §11), never
traced again. Each record carries §8.2's fields alone: `reference`,
`version`, `encoding` (absent when none), `member` (absent for a scalar
reference), `source` (the import base or fragment revision, the SHA-256 of
its stored sanitized text as `digest`, the occurrence's redacted `path`, and
`occurrence`, its position among its source revision's occurrences, never
below 0)
and exactly one outcome, `output` (the redacted output path) or
`overriddenBy` (the overriding fragment revision and its `digest`). A stored
record with any other field, a `null`, a field name in another case, an
encoding outside compilation §5.2's enum, a reference outside its grammar or
a path that is neither a compilation §2.2 path nor `<redacted>` whole (§8.3)
is never forwarded: the read is `500 internal-error`. A configuration that could not be redacted is `null`, with a
`notice` saying so. No release read carries a ciphertext, an artifact or
configuration digest or the release digest; a release that does not cover
the machine is `404 not-found`.

Entering recovery mode, which names the restored backups (§12.2):

```http
POST /api/v1/recovery/entries
Idempotency-Key: 3a9f0d71-c6e2-4b85-8f14-72d0b5a9e6c3

{"restored": [
   {"family": "database", "backup": "pg-2026-09-25T02:00Z",
    "takenAt": "2026-09-25T02:00:00Z"},
   {"family": "provider", "backup": "bao-2026-09-25T02:30Z",
    "takenAt": "2026-09-25T02:30:00Z"}],
 "reason": "database restored after storage failure"}

HTTP/1.1 201 Created
Location: /api/v1/recovery
Bronzeward-Epoch: ep_53wiltmcac6xxdggvgg7zcoh5y
Bronzeward-Recovery-Mode: true
```

Recording an identity revocation (§10.4) by `recovery-admin`, naming a subject
of the configured issuer that never signed in; the operator listed it in
`deniedSubjects` first:

```http
POST /api/v1/identity-revocations
Idempotency-Key: 3f1d7c52-8a64-4e0b-b2c9-6e5a1f0d8b47

{"iss": "https://idp.example", "sub": "h-all", "reason": "left the team"}

HTTP/1.1 201 Created
Bronzeward-Epoch: ep_bqeknkmarvikuy7ofil2okekgi

{"identity": "idn_gq5mhfqgf6rkwdkelynnhahifa",
 "revokedBy": "idn_vrvke5r5apullj3n5t3dhiwbpm", "role": "recovery-admin",
 "reason": "left the team", "act": "act_e6akeffm47l5knthcpqf4ot6yi",
 "epoch": "ep_bqeknkmarvikuy7ofil2okekgi", "at": "2026-09-29T10:00:00Z",
 "deniedSubjectsListed": true,
 "note": "A database restore can remove this revocation. ..."}
```

The identity is named by `identity` (an `idn` identifier) or by `iss` and
`sub`, never both; `reason` is required. A second revocation of one identity is
`409 conflict`: a revocation is permanent.

### 9.4 Errors

Errors are `application/problem+json` (RFC 9457) **(choice §17.14)**. `type`
is `urn:bronzeward:problem:<code>`; `detail` names resources and paths, never a
value; `instance` is the request's identifier, also written to the server log,
or, in a failed operation's `error`, the operation's.

```json
{
  "type": "urn:bronzeward:problem:stale-input",
  "title": "An input changed after it was read",
  "status": 409,
  "detail": "Publication refused: 1 input(s) moved.",
  "instance": "op_zcfwgr7trb76z3zmsndrnk5mcm",
  "conflicts": [
    {"head": "frg_rgkebwvneg6mxhid62gec5difi", "expected": 3, "actual": 4}
  ]
}
```

A `stale-input` conflict names a head by its identifier, with head revisions
as integers: the draft's base, or the compilation snapshot's for a head used
unchanged, and the one T3 read under its lock; `"absent"` for a name the draft
introduces. It names a machine by its identifier, with import base revision
identifiers, `"absent"` where there is none. A draft keeps only the base
counter, not the ETag token, so the token is not named.

| Status | Code | When |
| --- | --- | --- |
| 400 | `invalid-request`, `cursor-invalid` | malformed body (a Talos endpoint outside §3.3's grammar included), unknown field, bad cursor |
| 401 | `unauthenticated` | no credential, or one that fails §10's token checks (a revoked or denied subject is `403 identity-revoked`); with `WWW-Authenticate: Bearer error="invalid_token"` |
| 403 | `forbidden` | no qualifying role; the body names the roles that would qualify |
| 403 | `identity-revoked` | the principal was revoked (§10.4) |
| 404 | `not-found` | no such resource or route |
| 409 | `stale-input` | a publication input moved, or a name the draft introduces was introduced first (§4.2) |
| 409 | `conflict` | the resource is in a state that refuses the act (a published draft, whose release the body names; an update or discard of a draft with a `queued` or `running` publish operation, which the body names (§3.1); a plan that is not `proposed` and not awaiting re-approval in the current epoch; a second approval in one epoch; a draft entry retry while the first request's claim is live (§7.2); an ingestion for a draft revision that has one `running` (§7.3); an inventory request for an SMBIOS UUID or Talos node ID already recorded, naming its machine, or for a Talos cluster ID already recorded, naming its cluster (§7.3); a key whose record is from an earlier epoch (§7.2); an entry whose key has a record from before this recovery start (§12.4); a second entry in one recovery start (§12.2); a publish operation whose draft is no longer `open` at the revision it bound, naming the draft, or whose draft revision has a release with other content, naming it (§6.2)) |
| 409 | `machine-identity-mismatch` | the error of a failed `ingest` operation: its `source: machine` read reached a node whose identity key is not the machine record's or whose Talos cluster ID is not its cluster record's, whose identity read failed, or whose SMBIOS UUID is absent for a machine recorded by one or present for a machine recorded by node ID (§3.3); its claim is abandoned and nothing read is kept |
| 409 | `ingestion-abandoned` | the error of a failed `ingest` operation whose staging claim was abandoned: by an operator's abandonment, by the sweep at the claim's absolute expiry or, under transient staging, at its lease lapse, by an ingestion start of the same draft revision once the claim is due, by a takeover with nothing to decrypt, or by recovery-mode entry (§8.2); the generations it created are orphans (§6.4) |
| 409 | `scope-busy` | an assignment change while an operation holds the machine scope |
| 409 | `recovery-mode-active` | an act refused on a scope still pre-restore unaccounted, a publication changing the assignment of a scope not released in the current epoch, or any request but liveness and entry under the recovery-start flag before entry (§12.2); the body names the scope |
| 412 | `precondition-failed` | `If-Match` does not match |
| 422 | `validation-failed` | compilation refused the input; paths and rule, never values (compilation §13). Also a publication refused by T3's re-check: a version's status recorded not before publication began classifying it is not `retained`, or carries a scheduled deletion other than the one publication observed (dependency monitor §5.2); the body names its `dependency` by provider, object and version |
| 422 | `idempotency-key-reused` | same key, other request (§7.2) |
| 428 | `precondition-required`, `idempotency-key-required` | `If-Match` or `Idempotency-Key` missing |
| 500 | `internal-error` | an unexpected server failure; the body says whether anything was committed or the outcome is unknown, in which case a retry under the same `Idempotency-Key` answers it (§5 rule 6) |
| 501 | `not-implemented` | a §9.2 route whose handler has not landed yet: routed, authenticated and role-checked, nothing committed. PoC delivery state only; it disappears when every route has its handler |
| 503 | `dependency-unavailable` | the provider is sealed or unreachable, or authentication could not reach the identity provider or the database; nothing was committed. Not at a Talos access read, which is `talos-access-unavailable` |
| 503 | `talos-access-unavailable` | the error of a failed `ingest` operation, or the cause recorded by a dispatch's use-time check or fresh observation that failed on it (execution and recovery §3.1): the cluster's Talos access configuration is absent, unreadable by the identity, unreachable behind a sealed or silent provider, not a usable talosconfig or refused by Talos, or the machine's Talos endpoint did not answer (§3.3); the body names the cluster or machine, never a value; an ingestion's claim is abandoned and nothing read is kept. A use-time check that fails on the access read and on another dependency at once, as under a sealed provider, reports this code; its timeline entry records each dependency's result (execution and recovery §4.1) |
| 503 | `transient-conflict` | deadlock retries exhausted (§5) |
| 503 | `epoch-superseded` | the serving process started before the current epoch, so it may issue no ownership (§5.1); nothing was committed |
| 503 | `schema-mismatch` | never served: the server does not start (§11) |

An asynchronous operation that fails carries the same problem document in its
`error` field.

## 10. Authentication and authorization

Design: [§13.7](../design/Talos_Configuration_and_Machine_Management_Design.md#137-poc-identity-and-approval-policy),
[§13.3](../design/Talos_Configuration_and_Machine_Management_Design.md#133-enrolment-security).

Every request authenticates; there is no anonymous route besides a liveness
probe that returns no data. A principal is a human, identified by the OIDC
issuer and subject, or a service identity.

A human's Principal row is created on first use by a mutating request that has
been admitted: authenticated, its role checked (§10.3), and past the checks
that need no transaction (§9.4's `400` and `428`, and the recovery-start
flag's refusals, §12.2). A read, and a request
refused before that point, creates no row. When an admitted request's verified
token names an `(iss, sub)` with no row, the handler inserts one in its own
short transaction before the request's transaction:
`INSERT INTO principal (kind, iss, sub) VALUES ('human', $iss, $sub) ON CONFLICT (iss, sub) DO NOTHING`,
backed by a unique index on `(iss, sub)`, then reads the row. Of two concurrent
first requests, one inserts and the other finds the committed row, so both use
one principal. The row starts unrevoked and stores no roles: roles come from
each request's token (§10.3). It records its creation time. If the request
is then refused inside its transaction, the row stays: it names only a subject
that held a qualifying role, and grants nothing (§14). A
`deniedSubjects` entry is checked from
configuration before the insert, so a denied subject gets no row. An identity
revocation (T5c) naming an `(iss, sub)` that has never signed in creates the
row the same way, then locks it, so a revocation can precede a first sign-in.
That insert skips the `deniedSubjects` check: §10.4 has the operator list a
subject before revoking it, so the check would leave such a subject
unrecordable. The row it creates is revoked by the same request.
A service identity's row is created by the command-line tool with its token
(§10.2). No human or automation principal
has an OpenBao identity (design §13.7 item 1, derived).

### 10.1 Humans: OIDC

Bronzeward is an OAuth 2.0 resource server. A human's client obtains a JWT
access token from the operator's identity provider and sends it as
`Authorization: Bearer`. Each request is verified on its own, and an event
stream ends no later than its token's `exp` (§8.3); Bronzeward keeps no server
session **(choice §17.16)**:

- signature against the issuer's published keys, asymmetric algorithms only
  (`none` and HMAC algorithms refused);
- `iss` equal to the configured issuer, `aud` containing the configured
  audience, `exp` and `nbf` with at most 60 seconds' skew;
- `sub` present and a non-empty string; otherwise the token is refused `401`;
- `iat` present and no later than `now()` plus the 60 seconds' skew; a token
  without it, or issued in the future, is refused;
- `exp - iat` at most the configured maximum lifetime, default 15 minutes; a
  longer-lived token is refused;
- the subject is not revoked or denied (§10.4). A token that passes the
  checks above but names a revoked or denied subject is answered
  `403 identity-revoked`, not `401`.

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

The server reaches the identity provider over https, over plain http to its
own host, or over plain http to a host the deployment lists in
`auth.oidc.plainHTTPHosts` (bare host names, compared exactly). The rule holds
for the issuer URL, for the key set URL its discovery document advertises and
for every redirect: the keys fetched decide whose tokens verify, so listing a
host states that the network to it is protected. A discovery document or
redirect naming an unlisted plain-http host is refused. A plain-http request
goes directly to its host, never through a proxy the environment names
(`HTTP_PROXY`), which would sit on that protected path; an https request may
use one, since the proxy only tunnels its TLS.

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
is the only way to issue, rotate or list tokens, and to revoke one without
revoking its identity; no API route does any of that (design §13.7 item 2).
An identity revocation through the API (§10.4) revokes the identity's token
with it. It:

- creates a service identity with roles drawn only from `viewer`, `author` and
  `publisher`, refusing `approver` and `recovery-admin`; a check constraint
  refuses them too (design §13.7 item 2);
- records a responsible human principal for the identity
  **(choice §17.18)**, used by §10.5;
- sets a mandatory expiry, default 30 days, at most 90 **(choice §17.17)**;
- rotates by issuing a new token and revoking the unrevoked one, expired or
  not, in the same transaction, so one token is valid at a time. The
  reissue after a restore (§12.3) is such a rotation. Every tool transaction
  that issues or revokes an identity's token first takes the installation
  state `FOR SHARE`, as T5c does, so recovery-mode entry (T9) cannot change
  the epoch between the token's or act's epoch and the commit (rule 5's
  order). It then locks, in id order, the principal row `FOR UPDATE`, the
  lock T5c takes, and each human it records `FOR SHARE`, refusing one that
  is revoked by then; it refuses to issue a token to a revoked
  identity or to one that `deniedSubjects` lists (§10.4): two
  rotations of one identity serialize, the second replacing the first's
  token, and a rotation racing an identity revocation either precedes it,
  its token then revoked by T5c, or waits and is refused. A partial unique
  index on the owner over unrevoked tokens backs the invariant;
- prints the token once, and writes an act record naming the operator as
  given to the tool.

A token issued before the current recovery epoch is refused (§12.3), and the
token of an identity that `deniedSubjects` lists is refused
`403 identity-revoked` (§10.4).

### 10.3 Authorization

Each route names its roles (§9.2). The check runs in the request handler
before any transaction, and again inside the transaction for acts whose
validity the database must hold: an approval checks that the approver is not
revoked, with the principal row read `FOR SHARE` (T5a).

When a principal holds several roles that qualify for a route, the act is
recorded under the first qualifying role in the route's listed order
**(choice §17.20)**; one person holding several roles still performs each act
under a single named role (design §13.7 item 2).

Human-only routes refuse automation even where a role would allow it:
approval, recovery and identity revocation because automation never holds
those roles (design §13.7 item 2); and ingestion (with its marks, takeover and
abandonment) and inventory creation, which require `author` held by a human
**(choice §17.22)**, and the replacement of a machine's Talos endpoint, which
corrects inventory and follows the same rule (§3.3). Design §13.7 left "which role performs the privileged
ingestion that feeds an adoption" to the owner, who decided it on #20, and
names no role for inventory records. An adoption plan is a plan: a `publisher` creates it, as
every plan (design §13.7 item 2; owner decision 2a on #14), and an `approver`
approves it, as design §13.7 item 3 already says.

A plan's cancellation is checked before any transaction against `publisher`,
`approver` and `recovery-admin`, since every plan's creator is a `publisher`
(choice §17.22). Whether a `publisher` created the plan is the handler's
check, inside its transaction.

Drift **Ignore** is outside PoC scope (execution and recovery, supported
values) and has no route.

### 10.4 Revocation

**Identity revocation** is recorded by `recovery-admin` (design §13.7 item 4):
an immutable revocation row and the principal's `revoked` flag, in one
transaction that locks the principal row `FOR UPDATE` (T5c). From its commit:

- no commitment or attempt transaction admits an approval that identity gave:
  execution and recovery's comparison 1, repeated in every attempt transaction,
  refuses it, and reads the principal row `FOR SHARE` so that the revocation
  waits for it or precedes it (§1.2 item 3). An attempt already recorded is not
  reached; an operation left with an attempt in flight becomes `unresolved`
  and goes to `recovery-admin`'s accounting (execution and recovery, accounting
  a lost response). The uncommitted plans it approved read `revoked` (§8.1);
- the identity cannot authenticate again: a revoked human subject is refused
  at §10.1, and a service identity's token is revoked with it
  **(choice §17.19)**. This goes beyond design §13.7 item 4, which states what
  the revocation invalidates and not whether the identity may still sign in; it
  does not contradict it. A revocation is permanent in the PoC; the person or
  service needs a new identity.

A restore can remove a revocation recorded after the backup. The deployment
configuration's `deniedSubjects` list survives a database restore, so the
operator adds the subject to that list before recording the revocation, not
after it and not after a restore: a human by `(iss, sub)`, a service identity
by its `idn` identifier. A revocation recorded first could be lost to a
failure and a restore before the list names the subject, leaving no trace of
it. The revocation's response restates the rule. A restored database that lost the revocation
then still refuses the subject before and after entry, so a revoked
`recovery-admin` can neither enter recovery mode nor act in it, and the tool
issues no token to a listed service identity, even before the lost revocation
is re-recorded (§10.2). The recovery procedure re-records the lost revocation
rows (§12.3) **(choice §17.19)**.

**Losing a role** without an identity revocation is not acted on
**(choice §17.23)**. A human's roles are known only from the token presented
with a request, so a role removed at the identity provider is not observable
for approvals already given. Whether it should invalidate them was a question
design §13.7 left to the owner, who decided it for the PoC (choice §17.23);
the available remedies are revoking the approval or the identity.

Approval revocation is execution and recovery's; this contract records it as
an immutable row with its act, in a transaction that first locks the approval
row `FOR UPDATE`, so that it waits for a commitment or attempt transaction
reading that approval `FOR SHARE` (T5b; DS row 003). A lock read does not fire
the approval table's immutability trigger, which refuses only `UPDATE` and
`DELETE`.

### 10.5 Recording every act

Every mutating request that commits writes an **act** row in the same
transaction: act id, principal, principal kind, role exercised, action, subject
identifiers, idempotency key, request id, epoch and server time (design §13.6,
§13.7 item 2). The operation timeline references the acts that touched it. Act
rows are readable by any role (design §13.7 item 2, `viewer`'s audit read) and
are never deleted. Every transaction that writes an act, the token tool's
included, takes the act-order lock just before it (rule 5), so acts become
visible in recording order.

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
6. Until the first release, the schema is one migration,
   `0001_schema.sql`, edited in place: a schema change edits the statement
   that creates what it changes, and no migration alters another. A database
   migrated from an earlier version of that file fails rule 2's checksum
   comparison and is recreated; nothing upgrades it. Rule 4 binds from the
   first release.

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
immutable: identity, entry time, entering principal and, for an entry after a
restore, each restored backup family with the backup's identity and age, so
that the pairing duties of design §7.7 can be checked (execution and recovery,
entry).

An epoch is an **identity**: 128 random bits (§2), minted at installation and
at every recovery-mode entry, never issued again, and compared by equality
only, as execution and recovery's revision requires (§1.2 item 10)
**(choice §17.25)**. An approval, a
scope release, a fence, a claim and an automation token are valid only if the
epoch they carry is the current one. No counter orders epochs; where a reader
wants them in order, the entry time of their `RecoveryEpoch` rows orders them
for display, and decides nothing.

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

Design §7.7 duty 6 has the operator enter recovery mode "before normal
startup", and design §13.7 item 5 and §14.6 give entering it to
`recovery-admin`. The two meet in execution and recovery's recovery start
**(choice §17.26)**:

- Before the restore of any backup family begins, the operator stops, or
  establishes as stopped, every controller and API process, and keeps them
  stopped until entry commits, except the single process started with the
  recovery-start flag after the restore (execution and recovery §7.2). A
  restore of OpenBao or of the credential store alone restores no database,
  so stopping processes only before they reach the restored database would
  fence nothing. This is an operator step: nothing here enforces it, and
  until entry commits the epoch fence cannot tell such a process from a
  current one.
- After a restore, the operator starts the service with a server-side
  **recovery-start flag**. The flag is process state, not database state. Under
  it the process runs no executor and no job worker, runs no dependency monitor
  pass or watchdog until entry has committed
  ([dependency monitor §6.1](dependency-monitor.md#61-passes)), attempts no commitment or
  attempt transaction, and until entry has committed serves the liveness probe
  and `POST /recovery/entries` only. Every other request, reads, the other
  recovery routes and observation included, is refused with
  `409 recovery-mode-active`. Nothing is then recorded in the restored epoch
  that entry is about to fence, and nothing is disclosed to a credential that
  the restored database still accepts: an automation token revoked after the
  backup is valid in the restored epoch until entry mints the new one (§12.3).
  After entry, still under the flag and with the epoch its entry minted as its
  own (§5.1), it serves what execution and recovery's recovery start serves (observation, the recovery routes and the
  installation-wide acts of its §7.5). The dispatch gates therefore stay closed
  before entry, whatever the restored database says.
- Entry itself is an API act by `recovery-admin`, human only:
  `POST /recovery/entries`, whose body names each restored backup family with
  the backup's identity and age (§9.3). Only a process started with the flag
  serves it, and at most one entry commits per start (step 1 below). No
  server-side command enters recovery mode on its own; the command-line tool
  of §10.2 handles automation tokens only.
- Once entry has committed, recovery mode and the scope gates are database
  facts that every commitment and attempt compares, so the service may be
  restarted without the flag.

Entry runs as one transaction (T9). It:

1. locks `InstallationState` `FOR UPDATE`, which waits for every transaction
   that read it `FOR SHARE`, and checks that the current epoch is still the
   one the process read when it started with the recovery-start flag;
   otherwise an entry has already committed since that start, and the request
   is refused `409 conflict` naming that entry's epoch, so two entry requests
   under different keys mint one epoch between them **(choice §17.26)**; then
   locks every machine row `FOR UPDATE`;
2. inserts the `RecoveryEpoch` row and makes it current, and sets recovery
   mode;
3. marks every staging claim from an earlier epoch `abandoned`, clears its
   payload (compilation §3.5) and fails its `ingest` operation, if one is
   still `running`, with `ingestion-abandoned` and its terminal event (§8.2);
4. fails every `queued` or `running` `publish` job with
   `recovery-mode-entered` and its terminal event (§8.2);
5. performs execution and recovery's entry effects: it closes every machine
   scope's gate, takes over every non-terminal operation into the new epoch
   (each in `committed`, `sending` or `verifying` becomes `unresolved`), and
   marks every machine scope pre-restore unaccounted, each recorded on the
   machine's timeline (T7);
6. writes the idempotency record and the act.

From the commit, every fence and claim issued before it fails the epoch term of
§5.1, whatever generation it carries; every approval and scope release from an
earlier epoch authorizes nothing; and every automation token from an earlier
epoch is refused.

While recovery mode is in effect, what the API accepts depends on the act and
the scope **(choice §17.13)**:

- **Always accepted**, by the roles design §13.7 gives them: acts that only
  remove authority (approval and identity revocation, plan cancellation,
  freeze) and acts that record recovery (accounting decisions, takeover
  requests, scope marks, scope release, resolutions, leaving recovery mode).
- **Accepted installation-wide**: inventory (a machine's Talos endpoint
  change included, §3.3), drafts, ingestion and
  publication. They write the database and the provider and change nothing on
  a machine; a `source: machine` ingestion reads the node, which is the
  observation design §14.6 allows. A machine inventoried after entry gets a
  scope that is pre-restore unaccounted, as entry sets every other (the
  restore may have erased an earlier inventory of it), and follows execution
  and recovery §7.4 from there. Clearing a scope `blocked` on a lost key
  version needs a new publication, followed by re-approval (design §7.7 duty
  4).
- **Refused with `409 recovery-mode-active` on a scope still pre-restore
  unaccounted**: plan creation, approval, adoption plans and unfreezing.
- **Refused on every scope not released in the current epoch**: the
  commitment, the attempt and the adoption record, by execution and recovery's
  scope gate (its comparison 6 and adoption requirement 4.5), and a
  publication that changes the machine's assignment, which T3 refuses
  `409 recovery-mode-active` by execution and recovery's rule for assignment
  changes (§6.2). An approval on a scope accounted for but not yet released is
  accepted and authorizes nothing until the release.
- **A released scope is fully usable**, as execution and recovery states.

Design §14.6 "pauses mutation and automatic resumption while allowing the
observation and checks needed"; this contract reads the pause as covering
everything that can reach a machine through a scope not yet accounted for.
Reads are unaffected. Leaving recovery mode needs every scope released
(execution and recovery); since nothing refused above is needed to account for,
repair or release a scope, leaving cannot deadlock.

### 12.3 What a restored state means, record by record

For a database restored to a backup taken at time *T*:

| Record | After the restore | Consequence |
| --- | --- | --- |
| Identifiers issued after *T* | absent, never reissued (§2) | a client's handle for a lost release or operation answers `404`, never another record |
| Revision numbers issued after *T* | reissued with new tokens | an ETag issued after *T* answers `412`; one of the revision the backup holds still matches (§4.1) |
| Fence generations issued after *T* | reissued | refused after entry by the epoch term (§5.1) |
| Head advances, drafts and releases after *T* | heads rewound; drafts and releases absent | re-edited and republished; release records lost this way are KL case G |
| Staging claims | from before *T*, earlier epoch | abandoned at entry; inputs ingested again (compilation §3.5) |
| Ingestion generations after *T* | in the provider if its backup is newer | orphans (§6.4); not reattached |
| Approvals, scope releases | earlier epoch | authorize nothing; a plan needs an approval in the new epoch |
| Idempotency records after *T* | absent | a retry executes afresh (§12.4) |
| Idempotency records from before *T* | earlier epoch once entry commits | a retry is refused `409 conflict` (§7.2) |
| Identity revocations after *T* | absent | re-recorded by `recovery-admin`; the subject, human or service identity, is already refused by `deniedSubjects`, and the tool reissues no token to a listed service identity (§10.2, §10.4) |
| Automation tokens | earlier epoch | refused; reissued with the tool **(choice §17.27)** |
| Tokens revoked after *T* | valid again in the rows | refused anyway: earlier epoch |
| `DependencyStatus` | as at *T* | a dependency recorded `retained` and now answering 404 alerts at once as a regression (§6.3) |
| `DependencyAlert` after *T* | absent | their log lines remain ([dependency monitor §9](dependency-monitor.md#9-restored-state)) |
| `DependencyMonitor` | as at *T* | an alert recorded before *T* and logged after it is logged again, told apart by its `dal` (at-least-once delivery); the alerts after *T* are absent |

The automation-token rule costs a reissue of every service identity's token
after each restore. It is the only way this contract finds to refuse a token
replaced by a rotation after *T*, which the restore revived; a token whose
identity was revoked after *T* is also refused through `deniedSubjects`
(§10.4).

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

The one record a restore must not replay is an earlier entry. A restored
database can hold the idempotency record of a `POST /recovery/entries` from an
earlier recovery cycle; replaying it would answer `201` with no new epoch
minted and no scope closed. The process started with the recovery-start flag
remembers the epoch its own entry minted. Until it has one, an entry request
whose key has a stored record is not replayed: it is refused with
`409 conflict`, naming the stored entry and its epoch, and the operator
retries under a new key. Once this process's entry has committed, a retry of
that entry replays as usual.

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
   /ingestions` commits claim `ing_4ycffhy7bf4o2w6pz5b4r75hmu` (compilation
   §2.3 step 0) and its `ingest` operation, `running` under the claim's owner
   and generation (§5.1).
2. Extraction substitutes three values; step 6 creates three generations at
   `gen/cl_oxbgrzprzpvnecj5ve3jht3dha/ing_4ycffhy7bf4o2w6pz5b4r75hmu/<value id>`
   with `cas=0`.
3. The database connection breaks inside the draft transaction. Nothing of the
   draft is committed (DB rows 007, 058, 060 show the same for publication).
   The claim is unreleased.
4. Under transient staging the claim is abandoned at lease lapse; the sweep
   that writes the abandonment fails the operation `ingestion-abandoned` in
   the same transaction (§8.2). Under encrypted staging a takeover may resume
   it, moving the operation's owner fields with the claim (compilation §3.4;
   §5.1).
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
  still holds. If another worker superseded it, that worker's own T3 finds the
  release by its first check (§6.2), before the draft and head checks the first
  commit made fail, compares digests and succeeds with the same release. The
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
3. The client retries while T2 is still open: the retry's transaction waits on
   the key's lock, then finds the committed record and replays it (not
   measured, §7.2). Had the request been a draft entry update, the second copy
   would have found the first's live claim for the key before ingesting and
   been answered `409 conflict`; a later retry replays the first's response.
4. The client reuses the key for a different draft: `422
   idempotency-key-reused`.
5. The client, having lost everything, posts again with a new key. While the
   operation is queued or running, the partial unique index on active publish
   operations returns it, `202`. After it succeeded, the draft is `published`,
   and T2 answers `409 conflict` naming the draft's release.
6. A human approves the plan twice with two keys: the second is `409
   conflict`, one approval per plan.

### 13.5 A migration

A release after the first; before it, there is one migration (§11 rule 6).

1. The operator stops the service and runs the migrate command for a binary
   whose migrations 1–3 are applied and whose 4th adds an index.
2. An orchestrator also starts a second migrate run. Both take
   `pg_advisory_xact_lock`; the second waits, then finds version 4 recorded
   and skips it (DB row 025).
3. Had the first run been killed inside migration 4's transaction, nothing of
   4 would exist and the next run would apply it (DB row 024).
4. The operator starts the service. It finds versions 1–4 with matching
   checksums and starts. An older binary started by mistake finds version 4
   it does not know and refuses to start.

### 13.6 A restore to an older backup

State at backup time *T*: epoch `ep_bqeknkmarvikuy7ofil2okekgi`, release
`rel_fgqvcvz3ck7h7234ljgdbzsj6m`, operation `op_e4t4jm7vp3b5fvmbvossyj43hq`
on machine `mch_tqhcznunhyle4hnxru5hkt35uq`, owned by executor X at
generation 1.

After *T*:

1. A publication commits `rel_e7d7ysg556p667w5aausvvyrne`.
2. X is taken over by Y (generation 2) and back by X (generation 3); X holds
   token `(ep_bqeknkmarvikuy7ofil2okekgi, 3)`.
3. `recovery-admin` enters recovery mode for an unrelated reason, minting
   `ep_e3t4dznwcjg4uoahvfjk4w6kwy`, and leaves it.
4. An ingestion creates generations under claim
   `ing_4ycffhy7bf4o2w6pz5b4r75hmu`.
5. The operator adds human `idn_6woutisn7uensexh3kk2qlz6ma`'s subject to `deniedSubjects`, then `recovery-admin` revokes the identity (§10.4).

The operator stops every service instance, then restores the database from *T*
and the provider from a snapshot taken after it. The restored database names
`ep_bqeknkmarvikuy7ofil2okekgi` as current and knows nothing of
`ep_e3t4dznwcjg4uoahvfjk4w6kwy`. The operator starts one instance with the
recovery-start flag, whose dispatch gates stay closed and which refuses every
request but liveness and entry, and keeps the others stopped until entry
commits.
`recovery-admin` then records entry through the API, naming both restored
backups. Entry mints `ep_53wiltmcac6xxdggvgg7zcoh5y`, a new random identity: it
equals neither the restored epoch nor the lost one.

- `rel_e7d7ysg556p667w5aausvvyrne` answers `404`; no new release can be given
  that identifier.
- The operation is `unresolved` (entry) and its machine's scope is pre-restore
  unaccounted. If X is still running with its token, its next write carries
  `ep_bqeknkmarvikuy7ofil2okekgi`, not the current epoch, and is refused,
  whichever generation the rows now hold. With a counter alone, the next two
  takeovers would reissue generations 2 and 3, and X's token would pass, as in
  DB row 027. The operator was to stop X, or establish it as stopped, before
  the restore began and keep it stopped until entry committed (§12.2); the
  fence refuses only its database writes, and only after entry.
- A client that recorded `ep_e3t4dznwcjg4uoahvfjk4w6kwy` sees
  `ep_53wiltmcac6xxdggvgg7zcoh5y` in `Bronzeward-Epoch` and knows its events and
  cursors do not resume.
- The generations of step 4 are orphans; the input is ingested again.
- The revocation row of step 5 is gone, but its subject has been in
  `deniedSubjects` since step 5 (§10.4), so the human is refused before and
  after entry. The recovery procedure re-records the row; any approval they
  gave before entry authorizes nothing in any case.
- Every automation token is refused until the tool reissues it.
- During recovery mode an `author` edits the change that step 1 published into
  a draft again and a `publisher` publishes it, which recovery mode allows
  unless it changes the assignment of a scope not yet released (§6.2). A
  plan for `mch_tqhcznunhyle4hnxru5hkt35uq` is refused `409
  recovery-mode-active` while its scope is pre-restore unaccounted. Once the
  scope is accounted for, its restored operation resolved and the scope
  released (execution and recovery), it is fully usable: a plan, an approval in
  the new epoch and the commitment proceed there, while other scopes stay
  closed until they are released too.

## 14. Failure and rejection cases

| Area | Condition | Outcome | Persisted |
| --- | --- | --- | --- |
| Auth | Missing, malformed, expired, over-long or wrongly signed token | `401` | nothing |
| Auth | Revoked or denied subject; token from an earlier epoch | `403 identity-revoked` for a revoked or denied subject; `401` for a token from an earlier epoch | nothing |
| Auth | No qualifying role; automation on a human-only route | `403 forbidden` | nothing |
| Request | Missing `Idempotency-Key` or `If-Match` | `428` | nothing |
| Request | Key reused for another request | `422` | nothing |
| Request | Same key while the first is in flight | waits on the key's lock, then replays or executes; on a draft entry route whose first request still holds a live claim, `409 conflict` (§7.2) | one effect |
| Request | Entry retried with a key whose record predates this recovery start | `409 conflict` naming the stored entry (§12.4) | nothing |
| Request | Key whose record is from an earlier epoch | `409 conflict` naming the stored request and its epoch (§7.2) | nothing |
| Request | Keyed fingerprint whose digest-key version the provider can no longer compute under | `422`; under a new key, an ingestion whose operation is still active is `409 conflict` naming it (§7.1, §7.3), and a draft entry that compilation refused after its claim existed ingests afresh (§7.1) | nothing; under the new key, a draft entry refused again leaves another abandoned claim and its orphans |
| Draft | ETag mismatch | `412` | nothing |
| Draft | Update or discard while a publish operation for it is queued or running | `409 conflict` naming the operation | nothing |
| Draft | Compilation refuses the input | `422`, paths only; a retry under the same key replays it (§7.2) | claim row with its principal and key, the refusal's idempotency record and act; orphans if past compilation §2.3 step 6 |
| Draft | Database fails inside T1 | `503`; claim unreleased | claim row; orphans |
| Publish | Moved head, a covered machine's import base changed (§4.2), or a machine of the cluster covered since the snapshot (§6.2, expected "absent") | operation `failed`, `409 stale-input` | operation, act |
| Publish | A name the draft introduces was introduced by another publication first | operation `failed`, `409 stale-input` (expected "absent") | operation, act |
| Publish | Assignment change while its scope is held | `failed`, `409 scope-busy` | operation, act |
| Publish | Dependency `blocked` or `lost`, or its identity changed (§6.1), a concurrent publication's seed of another KV creation time included (§6.2) | `failed`, `422 validation-failed` naming it | operation, act |
| Publish | Dependency left `unknown` for another reason (a value read or encryption refused, absent or not understood; an artifact key read with no `Date`), or provider sealed or unreachable (§6.1) | `failed`, `503 dependency-unavailable` naming it | operation, act |
| Publish | Compilation refuses, or covered machines name different Kubernetes versions (§6.1) | `failed`, `422 validation-failed`, rule and paths only | operation, act |
| Publish | Stale pin or selection; a changed assignment of a machine with no import base; no covered machine (§6.1) | `failed`, `422 validation-failed` | operation, act |
| Publish | Commit-unknown | resolved by reading the natural key | the release, once |
| Publish | Deadlock retries exhausted, `COMMIT` rejected (a deferred constraint), or a statement breaking an integrity constraint (a release machine whose mode is not its machine's platform, for one) | operation `failed`, `503 transient-conflict` or `500 internal-error` | operation, act |
| Publish | Compiled release disagrees with its sources' reference declarations or text digests | operation `failed`, `500 internal-error`; the clause logged | operation, act |
| Publish | Worker superseded, or its lease lapsed | its commit refused by the fence; the job claimed again | the other worker's result |
| Publish | New request for a draft already published | `409 conflict` naming the release | nothing |
| Plan | Second approval of a plan in one epoch | `409 conflict` | nothing |
| Plan | An `apply-config` plan for a release that is not the machine's `Desired` | `409 conflict` naming the `Desired` release | nothing |
| Plan | Approval or identity revocation racing a commitment | the revoker waits for the commitment or precedes it (§1.2 item 3) | the revocation, after or before the commitment |
| Recovery | Plan creation, approval, adoption plan or unfreeze on a scope still pre-restore unaccounted | `409 recovery-mode-active` | nothing |
| Recovery | Commitment, attempt or adoption record on a scope not released in the current epoch | refused by execution and recovery's scope gate | its refusal entry |
| Recovery | Publication changing the assignment of a scope not released in the current epoch | operation `failed`, `409 recovery-mode-active` (§6.2) | operation, act |
| Recovery | Any request but liveness and entry under the recovery-start flag, before entry | `409 recovery-mode-active` | nothing |
| Recovery | A second entry in one recovery start, under another key | `409 conflict` naming the epoch the first minted (§12.2) | nothing |
| Any | Deadlock retries exhausted | `503 transient-conflict` | nothing |
| Migrate | Failure or kill inside a migration | migration absent; server refuses to start | earlier migrations |
| Startup | Schema or checksum mismatch | refuses to start | nothing |
| Restore | No recovery-mode entry after a restore | undetected (design §14.6); pre-restore tokens can pass | the residual of §12.1 |

"Nothing" is apart from a human's Principal row, which a first admitted
mutating request creates before its transaction and a refusal inside that
transaction leaves (§10), and apart from the row an identity revocation
creates for a subject that never signed in, which a refusal inside its
transaction leaves too; neither row grants anything. The `401`, `403` and
`428` rows, and the refusals under the recovery-start flag before entry,
create none.

## 15. Invariants

1. **No reissued external identifier.** No identifier that leaves the database
   comes from a database sequence.
2. **Immutable is insert-only.** Immutable tables refuse `UPDATE` and
   `DELETE`; no table is deleted from in the PoC.
3. **Every write is conditional.** A mutable record changes only by a
   compare-and-set on its revision, by a fenced `UPDATE` whose predicate
   includes owner, generation and epoch, or under a row lock that the same
   transaction took `FOR UPDATE` and then checked.
4. **Lock what you check.** Every value a transaction's decision rests on is
   locked or in the predicate of the write that acts on it.
5. **No provider I/O in a transaction.**
6. **Provider first, references after.** No committed row references a
   provider object before the ingestion that created it observed the create
   succeed.
7. **One release per draft revision**, and a release is complete or absent.
8. **Heads move only at publication**, and never past a head revision other
   than the one the draft or snapshot read.
9. **Every committed API request has an act and an idempotency record** in
   its own transaction; no refused request has either, except a draft entry
   route refused after its staging claim exists, which commits both for the
   refusal (§7.2). Transactions that serve
   no request record timeline entries instead.
10. **No request body, secret value or ciphertext in an idempotency record, an
    act or a problem document.**
11. **Everything that authorizes carries the current epoch** (approvals, scope
    releases, fences, claims, automation tokens), compared by equality, and
    every issuance of a fence or claim, the first included, also requires the
    issuing process's own epoch to be current (§5.1).
12. **No automation principal holds `approver` or `recovery-admin`**, and no
    route issues tokens or grants roles.
13. **The server runs only on the schema it was built for.**
14. **One approval per plan and epoch, and at most one operation per plan**,
    the operation created by the dispatch commitment.
15. **Timeline order is commit order within a machine scope.**
16. **Recovery mode never blocks its own exit.** Nothing it refuses is needed to
    account for, repair or release a scope.

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
  a real `pg_dump` restore as DB row 027 was; a takeover, a job claim, a
  commitment and an ingestion start by a process started before the entry
  each refused after it, with a control that drops
  `$my_epoch = $current_epoch` (§5.1); and the recovery-start process, after
  its own entry, taking over and starting an ingestion in the new epoch;
- two requests with one idempotency key in flight together, with a control
  that drops the key lock;
- an approval revocation and an identity revocation each started while a
  commitment holds the approval, waiting for it, with a control that inserts
  the revocation without the lock and does not wait (DS row 003);
- an identity revocation appending its entry to the timeline of every machine
  with a plan that identity approved whose plan or operation is not terminal,
  and of no other machine (T5c);
- a lapsed `publish` job claimed again and its first worker's completion
  refused;
- leaving recovery mode racing an inventory request, refused while the new
  machine's scope is not released, with a control that reads installation
  state `FOR SHARE` and leaves;
- two concurrent rotations of one service identity, and a rotation racing its
  identity revocation, leaving at most one valid token and none after the
  revocation, with a control that drops the principal lock; a reissue after a
  restore refused for a service identity that `deniedSubjects` lists although
  the restored database lost its revocation; two concurrent
  inventory requests for one SMBIOS UUID under different keys, one refused,
  and the same for one Talos node ID and for one Talos cluster ID; an
  inventory body with both `smbiosUuid` and `talosNodeId`, or neither, refused;
  an inventory body without a `platform`, or with one other than `metal`,
  `container` and `cloud`, refused, and a release machine whose mode is not
  its machine's platform refused, each with its control;
- machine revisions, shared by plan, operation and machine-scope entries,
  allocated in commit order under concurrent writers to one scope, with a
  control that allocates without the lock;
- a takeover keeping an `unresolved` operation `unresolved`, and refusing a
  terminal one;
- recovery mode's per-scope refusals and allowances of §12.2, and the refusal
  of every request but liveness and entry under the recovery-start flag;
- every walk-through of §13, and every refusal of §14;
- the immutability triggers, and startup refusal on each schema mismatch;
- authentication refusals for each token defect in §10.1 and §10.2, a
  missing, empty and non-string `sub` included, and each route's role check
  against the scenarios of design §13.7;
- an event stream ended no later than its token's `exp`, and its resumption
  with `Last-Event-ID` refused under a token whose subject was revoked
  meanwhile (§8.3);
- that no request body reaches the database's data directory, write-ahead log
  or backups on the ingestion and draft routes, by the scan compilation §15
  requires;
- Talos access (§3.3): the ingestion and executor identities each reading
  `secret/data/access/talos/<cluster id>`, and the compiler, metadata and
  normal API identities refused it, with a control that grants the compiler
  the read and must then succeed; the ingestion and executor identities each
  refused write, list and delete on the access path, with a control that
  grants one of them `update` and must then succeed; the ingestion identity
  refused every `secret/data/gen/*` read; a
  `source: machine` ingestion failing `machine-identity-mismatch` with nothing
  kept against a node whose SMBIOS UUID differs, reports none for a machine
  recorded by one, or reports one for a machine recorded by node ID; whose
  node ID differs, for a machine recorded by node ID; whose Talos cluster ID
  differs; and whose identity read fails, a failed `systeminformation` read
  for a machine recorded by node ID included; with the controls that a
  machine recorded by node ID whose `systeminformation` read returns no
  resource is accepted, and that a machine recorded by SMBIOS UUID whose node
  reports a matching UUID and cluster ID but another node ID is accepted;
  each SMBIOS case against a stubbed Talos
  response, since the fixture's nodes report none (execution and recovery
  choice §10.26); each cause §3.3 lists for
  `talos-access-unavailable` (an absent secret, the reading identity's read
  denied, OpenBao sealed and partitioned, a malformed talosconfig carrying a
  synthetic client key, a well-formed credential Talos refuses, another
  cluster's talosconfig under a certificate authority the node does not trust
  at this cluster's path, an endpoint that does not
  answer), through both readers: for an ingestion, failing with it, the
  claim `abandoned` and the operation `failed` with its terminal event and no
  generation; for dispatch, the provider-side causes failing the use-time
  check (execution and recovery §3.1 item 2) and a refused credential or a
  silent endpoint failing the fresh observation (item 1), with no operation
  committed and no mutation request sent; every observation purpose
  (`evidence`, `completion`, `recovery`, `drift`, `restoration`) recording
  the access version it read, and on a failed access read none of the node's
  values and its cause, with a control that reuses a credential read before a
  rotation and must then be told apart by its version; for each, no part of the
  talosconfig in the problem, the timeline, the logs or the
  scan, with a control that passes the parser's or client's error text through
  and must then be caught; `POST /machines` and the replacement route each
  refusing `invalid-request` with nothing committed for an endpoint with a
  scheme, a path, a user part, whitespace, a DNS name, an unbracketed IPv6 literal, a port
  out of range, against IPv4 and bracketed IPv6 controls accepted; the
  endpoint change read back from the machine's timeline with both endpoints;
  the version identity recorded as path, version and
  `created_time`, with a control that deletes the path's metadata and rewrites
  it, whose new version the record must tell apart; an ingestion's access
  version and dialled endpoint read back from its `ingest` operation's events
  after a success and after a failure following the read (a malformed
  talosconfig, a refused credential, an identity mismatch); reads of the fixture's worker by its own endpoint with no
  `node` metadata, under an explicit port and the default port;
  an endpoint replacement from A to B after a plan was created, the plan
  still bound to A, its evidence and completion observations dialling A and
  recording it, with a control that dials the machine's current endpoint and
  must then fail, a plan created afterwards bound to B, and the change, its
  timeline entry and its act all present or all absent, with a control that resolves the
  route from the machine record at dispatch and must then fail; the same
  replacement accepted in recovery mode on a scope still pre-restore
  unaccounted; a talosconfig whose contexts' `endpoints` and `nodes` name the
  fixture's other node, every read still reaching the endpoint chosen with no
  `node` metadata; a rotation between a dispatch's evidence observation
  and its attempt, the attempt still using the version the observation used,
  with a control that reads the latest version at send and must then fail,
  and a rotation before a retry, the retry's evidence and attempt both using
  the new version;
  `TestLiveRoleProbe` asserting `PermissionDenied` for `os:reader` and
  `os:operator` on `MachineConfig`, with a control that probes the `os:admin`
  configuration in the `os:reader` slot and must then fail; the executor's observation client held to its read
  allowlist by test, with a control that adds a mutating call and must then
  fail, a `drift` observation succeeding with no plan, and the mutating
  client refused for a plan whose approval was revoked; and the
  talosconfig's client key in compilation §15's scan of the database, logs,
  responses and temporary files, over successful, refused and interrupted
  ingestions, executor observations and dispatches, with a positive control;
- the orphan report (§6.4): the orphan-report identity's token carrying
  exactly one policy, whose rules, read back from the provider with the
  fixture's administrative token, grant `read` and `list` on
  `secret/metadata/gen/*` and no other path or capability, with a control
  that adds any one other stanza and must then fail, so that the boundary
  does not rest on a list of probes; the identity listing
  `secret/metadata/gen/` and `secret/metadata/gen/<cluster id>/` and a
  claim's directory under it and reading a generation's metadata, and refused
  each of: a `secret/data/gen/*` read and a `secret/data/access/talos/*` read
  (`read`), a `list` of `secret/metadata/access/` (`list`) and a read of an
  access path's metadata under it (`read`), a Transit encrypt and decrypt
  (`update` on the key's `transit/encrypt/` and `transit/decrypt/` paths), and
  every change under the generation path: a `secret/data/gen/*` create
  (`create`), write to an existing generation (`update`), `PATCH` (`patch`)
  and `DELETE` of its latest version (`delete`), a metadata update
  (`update`) and a metadata delete (`delete`) under `secret/metadata/gen/*`,
  and a `secret/delete/gen/*`, `secret/undelete/gen/*` and
  `secret/destroy/gen/*` request (`update`); each refusal paired with a
  control that grants the identity the capability named beside it on that
  path alone, under which the same request must then succeed (against a
  disposable path for every change), so that a malformed probe cannot pass as
  a refusal;
  the command refusing a `provider.reportTokenFile` with group or other
  permission bits, one that is a FIFO and one that is a symlink, each before
  any provider request; no route serving the report, none of the routes
  naming it and its candidate paths answering `404` to a human and to
  automation, with a control that registers such a route and must then fail;
  and, with the file replaced
  within one run after the configuration has loaded and before the first
  provider request, the replacement token the one that reaches the provider,
  with a control that reads the file once at configuration load and must then
  fail by sending the old token;
  with two populated clusters, `--cluster <id>` listing and reporting only
  that cluster's subtree, with a control that always walks every cluster and
  must then fail;
  with two populated claim directories, the provider sealed after a
  generation in the first is listed and before the second is listed, and the
  database statement failing, each exiting nonzero with no path printed, with
  a control that prints each generation path as it is listed and must then
  fail by printing that first generation;
  the report naming an unreferenced generation of an `abandoned` claim, of a
  `released` claim and of a path whose claim row does not exist, and not the
  generations of a `held` or a `resumed` claim nor a referenced generation of
  an `abandoned` claim, with a control that selects by claim state alone and
  must then fail; an `abandoned` claim's generations referenced one through an
  import-base reference row and one through a fragment revision's reference
  row (once that revision's schema exists), neither reported, with a control
  that reads one kind of reference row only and must then fail; a draft transaction releasing a claim committed while the
  report runs, its generations not reported, with a control that reads the
  reference rows and the claims in two statements, commits the draft
  transaction between them and must then report them; a claim past its
  absolute expiry, still recorded `held` while a draft transaction that
  passed its owner check holds its lock and then commits, its generations not
  reported as orphans, with a control that treats the claim as `abandoned` at
  read time and must then report them; with no controller running, an
  `encrypted` claim recorded `held` and one recorded `resumed`, each past its
  absolute expiry, and a `transient` claim past its lease but not its expiry,
  recorded `held`, their unreferenced generations listed as expired and not
  yet abandoned, with a control that omits every `held` or `resumed` claim's
  generations and must then fail; an `encrypted` claim recorded `held` past its lease but
  not its absolute expiry (still open to takeover), its generations neither
  reported nor listed apart, with a control that treats every lapsed lease as
  abandonment and must then fail; without
  `--cluster`, the generations under a cluster directory whose cluster and
  claim rows a restore removed reported, with a control that lists only the
  clusters the database records and must then fail; a generation whose custom
  metadata holds a synthetic sentinel, the sentinel absent from the report's
  output, logs and errors, with a control that prints the provider's metadata
  response and must then fail; and the provider's versions and every table's
  rows (a full data dump compared before and after) unchanged by a run, with
  a control that records one audit row and must then fail.

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
- **Orphan listing**: PC measured listing under one generation path (row
  065); the check above measures listing at each level of the generation tree
  under the orphan-report identity's policy on the OpenBao version the fixture
  pins against a dev-mode server, and S1 the cluster and claim levels, under
  `--cluster`, against the fixture's (§6.4). No
  other OpenBao version, and no Vault, is measured.
- **Unkeyed configuration digests**: releases and import base revisions persist
  unkeyed SHA-256 digests of whole configurations (§1.1, §6.2), whose
  guessability was not assessed (compilation §4.1; execution and recovery's
  configuration digest). Such a digest is only as unguessable as the whole
  configuration it covers.
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
   database; the manager machine ID is a `mch` identifier** (§2).
   Alternatives: sequence ids with an epoch prefix, which still reissue within
   an epoch no one entered; for machines, an RFC 9562 UUID, as design §4.4's
   wording suggests.
2. **ETags carry a random token beside the revision** (§4.1). Alternative:
   revision numbers alone, which match again after a restore.
3. **Immutability enforced by database triggers** (§3). Alternative:
   application convention with tests.
4. **Publication checks every input head, changed or not, and each covered
   machine's import base** (§4.2).
   Alternative: check only heads the draft changes, accepting releases built on
   moved unchanged inputs.
5. **The import base is the Applied release's; no separate head** (§3.2).
   Alternative: an import-base head advanced by the adoption record, which puts
   a persistence write into execution and recovery's transaction.
6. **Publication selects the release as Desired for its machines** (§6.2).
   Owner decision, 2026-09-27 (ginsys/bronzeward#20); design §12.6 now says
   so, and approval gates only the plan that applies the release, which binds
   the selection current at its creation (execution and recovery choice
   §10.24).
   Alternative: select at plan creation or at approval, towards which design
   §12.6's earlier "select the latest applicable approved release" pointed.
7. **No provider or network I/O inside a transaction; fixed lock order; three
   deadlock retries** (§5). Alternative: provider calls inside the
   transaction, holding its locks for the provider's latency, or a pause.
8. **Generation paths carry the claim id and a random value id; orphans are
   reported, never reattached or deleted** (§1.1, §6.4). Alternatives: a
   separate ledger written before each create; reattaching orphans on
   re-ingestion. Compilation §2.3 step 6 states the component.
9. **An idempotency key on every mutating request, recorded only with a
   committed effect, and kept; a record replayed only in its own epoch; no
   deletion of any record** (§3, §7). Alternatives: keys optional; refusals
   recorded and replayed; records expiring after a window; replay across
   epochs, whose response then describes a fenced act.
10. **The release's natural key is the draft revision, compared by a digest
    without ciphertext** (§6.2). Alternative: DB's release name plus content
    digest over everything.
11. **A plan's operation is created by the dispatch commitment; before it, the
    plan is the handle, with execution and recovery's states `proposed`,
    `approved`, `committed` and terminal `revoked`, `cancelled` and
    `expired`; an adopt plan's commitment creates its operation in
    `completed`** (§1.2, §8.1). Design §11.1 and §12.5 do not settle when the
    operation is created. Alternative: create it with the plan, in a
    pre-commitment state, and make the commitment its conditional transition
    to `committed`.
12. **Job states `queued`, `running`, `succeeded`, `failed`; a lapsed
    `publish` job is claimed again; an `ingest` operation is created `running`
    and owned through its staging claim, is never queued or re-run, and fails
    with every abandonment of its claim; entry fails active jobs** (§5.1,
    §8.2). Alternatives: resume jobs after recovery; an `ingest` job with its
    own job claim beside the staging claim, two fences for one ingestion.
13. **Recovery mode is per scope** (§12.2): acts that only remove authority or
    record recovery are always accepted; inventory, drafts, ingestion and
    publication are accepted installation-wide, except a publication changing
    the assignment of a scope not yet released; plan creation, approval,
    adoption plans and unfreezing are refused on a scope still pre-restore
    unaccounted; commitments, attempts and adoption records are refused on
    every scope not released in the current epoch; a released scope is fully
    usable. This reads design §14.6's
    pause as covering what can reach a machine. Alternative: refuse every
    mutation but removals of authority and recovery acts installation-wide,
    which cannot clear a scope blocked on a lost key version while exit needs
    every scope released.
14. **Problem documents with codes; a `v1` additive deprecation policy;
    unknown request fields refused; pagination of 50 by default and 500 at
    most; cursors and event ids tied to the epoch; Server-Sent Events, no
    WebSocket** (§8.3, §9). Alternatives: any other error format or policy;
    unknown fields ignored; cursors that survive a restore; WebSocket, which
    design §11.1 also allows.
15. **Adoption approval is a plan with `operation: adopt`, approved by an
    `approver`, whose commitment records the adoption** (§1.2, §9.2).
    Alternative: a separate adoption resource.
16. **OIDC access tokens verified per request, no session, 15-minute maximum
    lifetime; asymmetric signatures only, `iat` required, 60 seconds' skew**
    (§10.1). Alternative: server sessions with introspection or back-channel
    logout, which propagate disablement faster at the cost of state.
17. **One valid automation token per identity, rotated by replacement,
    mandatory expiry of 30 days by default and 90 at most** (§10.2).
    Alternatives: an overlap window during rotation; longer or no expiry.
18. **Every automation identity names a responsible human** (§10.2).
    Alternative: none, leaving edge case (b) of design §13.7 item 3
    unrecordable.
19. **Identity revocation is permanent and refuses authentication; a
    deny list in deployment configuration, naming humans and service
    identities, survives restore and blocks a token reissue** (§10.4). This
    goes beyond design §13.7 item 4, which states what a revocation
    invalidates, without contradicting it. Because identity providers usually
    keep a subject stable, a mistaken revocation locks that person out until
    they are given a new identity. Owner decision, 2026-09-27
    (ginsys/bronzeward#20): accepted with that lockout; a mistaken revocation
    is corrected by issuing a new subject, never by un-revoking. Alternative:
    revocation that only invalidates approvals, as design §13.7 item 4 states
    it, or a reversible revocation, which needs its own restore rules.
20. **With several qualifying roles, the act is recorded under the first in
    the route's order** (§10.3). Alternative: the client names its role in a
    header.
21. **Both unsettled self-approval cases are marked, each with its reason**
    (§10.5). Alternative: mark neither, or only (b).
22. **The owner's answer to the question design §13.7 left open ("which
    role performs the privileged ingestion that feeds an adoption"):
    ingestion, with its marks, takeover and abandonment, and inventory
    creation need a human `author`; a `publisher` creates adoption plans, as
    every plan, and an `approver` approves them** (§9.2, §10.3). Owner
    decision, 2026-09-27 (ginsys/bronzeward#20): no longer interim; a human
    `author` ingests and automation does not. Alternatives: `publisher` for
    ingestion; a dedicated ingestion role; automation allowed.
23. **Losing a role does not invalidate approvals given under it** (§10.4),
    the question design §13.7 left to the owner. Owner decision, 2026-09-27
    (ginsys/bronzeward#20): accepted for the PoC, stated as a limit; the
    operator revokes the approval or the identity when someone loses the role.
    The alternative needs a directory lookup or a session to observe the loss.
24. **Migrations by explicit command with the service stopped; startup refuses
    any schema or checksum mismatch; no rewrite of immutable rows; no
    downgrade** (§11). Alternative: migrate at startup. Until the first
    release, one migration edited in place, with earlier databases recreated
    (§11 rule 6). Owner decision, 2026-10-05 (ginsys/bronzeward#112).
    Alternative: a migration per change from the start, each altering earlier
    ones, which is an upgrade path no unreleased database needs.
25. **The epoch is a random, never-reissued 128-bit identity compared by
    equality, with no counter** (§12.1), as execution and recovery's revision
    requires (§1.2 item 10).
    Alternatives: a counter in the database, which a restore rewinds; an
    external high-water mark (for example in OpenBao or a file) checked at
    startup, which could detect some restores but adds a dependency that can
    itself be restored.
26. **A server-side recovery-start flag keeps the dispatch gates closed; entry
    still needs the `recovery-admin` API act, served only under the flag and
    committed at most once per start** (§12.2). Alternatives: entry by a
    server-side command before the service starts, which performs no role
    check and so sidesteps design §13.7 item 5; for the once-per-start rule, an
    expected epoch the client sends with the entry, which fences the same
    duplicate but lets any process enter.
27. **Automation tokens from an earlier epoch are refused** (§12.3).
    Alternative: keep them, reviving any token whose revocation the restore
    removed.
28. **Drafts and releases cover one cluster, while library fragments are
    shared** (§3.1): a library change published through one cluster reaches
    another only at that cluster's next publication, reviewed in its plan's
    diff. Alternatives: a library change in a draft of its own; a publication
    that covers every cluster using the changed fragment.
29. **Talos access: the endpoint per machine in the database, the credential
    per cluster in the provider at a path derived from the cluster's id,
    written by the operator and read at use by the ingestion and executor
    identities** (§3.3). Owner decision, 2026-10-02 (ginsys/bronzeward#22):
    the secret provider holds management credentials (design §7.1), so the
    credential gets the provider's versions, audit and backup pairing, and
    each cluster's credential stays its own. Alternatives: one
    deployment-wide talosconfig file named in the server's configuration,
    which keeps a credential for every cluster outside the provider and its
    backups; a credential stored per machine, which Talos does not have (one
    cluster authority signs for every node); a route accepting the
    talosconfig, which would carry a secret through the normal API.
    Executor observations, which may have no plan, use a read-only client
    and need no approval; only decryption and mutating requests do (§3.3;
    owner decision, 2026-10-02). Alternatives: a separate observer identity,
    holding the same mutation-capable credential; observations under the
    ingestion identity, which can write secret generations.
30. **The orphan report lists generation paths under an identity of its own,
    `read` and `list` on `secret/metadata/gen/*` only, with its own token
    file** (§6.4). Owner decision, 2026-10-03 (ginsys/bronzeward#22): a
    listing reveals random identifiers, never values, and the dependency
    monitor's metadata identity keeps its `read`-only grant (dependency
    monitor choice §11.9). Alternatives: grant the metadata identity `list`
    on `secret/metadata/gen/*`, one credential fewer but a capability the
    monitor never uses, the alternative §11.9 rejected; no listing, reporting
    abandoned claims from the database alone, which names no path (a claim
    records none, and choice 8 rejected a path ledger) and cannot see the
    orphans of a claim a restore removed.
31. **A profile pins fragment revisions; an assignment selects heads by
    name; six fragment layers; cluster scope only in the PoC** (§3.1). A
    profile is "an ordered list of fragment revisions" (design §6.2), while
    T3's check of unchanged heads and choice 28's library pickup need the
    assignment to name heads. Pins and names are checked when the draft is
    updated and again at publication. Alternatives: profiles naming heads
    too, which makes a profile's content move without an edit to it;
    assignments pinning revisions, which leaves T3's unchanged heads nothing
    to protect and a library change no way to reach a cluster without an
    edit to every assignment. Library scope waits for a phase that defines
    its review (ginsys/bronzeward#23 scope).
32. **The operator records each machine's platform mode at inventory** (§3,
    `platform`). Compilation §6 step 8 validates "in the node's platform
    mode", and no other record holds it: a `source: document` import never
    reads the node, and a document carries no mode. The mode is fixed with
    the machine, like its identity key, and each release machine records it
    as its `mode`. Alternatives: read it from the node at each publication,
    which adds a Talos read to a step that holds no Talos access and fails
    for a document-only machine; a cluster-wide mode, which a mixed cluster
    cannot use; a default, which records a guess as a fact.
33. **A release covers every machine of its cluster that has an import
    base** (§6.1), each with its assignment after the draft, or with none,
    compiling on its base alone. Design §11.2 makes a release the cluster's
    desired state, and a library fragment's new head reaches machines no
    draft entry names (choice 28). Alternatives: only the machines a draft
    entry touches, which leaves a changed fragment's other users on their old
    release; every assigned machine, refusing one without an import base,
    which blocks every publication of the cluster until each assigned machine
    is imported. Cost: each publication compiles every covered machine, and
    one machine's failing dependency refuses the cluster's publication.
34. **A publish job uses ingestion's timers** (§5.1): its lease is
    `ingestion.lease`, its owner extends it every `ingestion.heartbeat`, and
    the process's publish worker also looks for an eligible job every
    `heartbeat`. A process has one set of timers, already validated in order
    (the heartbeat shorter than the lease), and a publication, like an
    ingestion, holds its job only while it works outside the database.
    Alternatives: a publication block with its own lease and heartbeat, which
    adds configuration no PoC case tunes apart; a fixed lease with no
    extension, which a compilation of many machines can outlast, letting a
    second worker take a job the first is still building.
35. **Bodies report revision numbers for reading; a precondition takes the
    whole ETag or an immutable identifier** (§2, §4.1). A number in a body
    shows a reader where a record stands, and binding a request to it would
    let a restore that reissues the number match it falsely. So no request
    takes one: a draft write takes the draft's ETag, and an approval names its
    plan by the plan's identifier, never reissued, whose content is
    immutable. Alternatives: bodies report a number only in the ETag form,
    which needs a token on every record a body names, heads and plans
    included, for no request that would use it; an approval bound by the
    plan's state ETag, which adds nothing, since the approval is checked
    against the plan's current state under its lock and the plan it approves
    cannot change.

## 18. Traceability

| Clause | Design | Evidence |
| --- | --- | --- |
| §1 scope, interfaces | §7.2, §11, §13.7 | [FR §10](../design/research/20260925-feasibility-evidence-review.md#10-recommendations) (Persistence) |
| §2 identifiers | §4.4, §7.7 | [DB §4.7](../design/research/20260924-database-semantics.md#47-s7-restored-state) row 027; [DB §9](../design/research/20260924-database-semantics.md#9-hand-off) |
| §3 entities, immutability | §4.4, §6.2, §7.2, §7.8, §11.2 | none: choices §17.3, §17.5, §17.28, §17.31, §17.32 |
| §3.3 Talos access | §7.1, §13.1, §13.2 | `os:admin` needed to read the machine configuration: the fixture's `internal/talos` `TestLiveRoleProbe` (Talos v1.13.6); the provider read grant on `secret/data/access/talos/*` not measured (choice §17.29) |
| §4 revisions, ETags | §7.2, §11.1 | [DB §4.1](../design/research/20260924-database-semantics.md#41-s1-stale-revision-rejection) rows 001–003; DB §4.7; bodies and bindings: choice §17.35 |
| §4.2 stale input | §7.4 step 4 | [DB §4.2](../design/research/20260924-database-semantics.md#42-s2-all-or-nothing-publication) rows 010, 011 |
| §5 transactions | §7.2, §7.4 | DB §4.2 rows 059, 061; [DB §6.3](../design/research/20260924-database-semantics.md#63-criterion-3-backend-specific-limitations-and-costs); [DB §7](../design/research/20260924-database-semantics.md#7-limits); [DS §7](../design/research/20260925-dispatch-safety.md#7-limits) |
| §5.1 fences, claims | §7.2, §12.5 | [DB §4.4](../design/research/20260924-database-semantics.md#44-s4-ownership-transitions) rows 015–018; [DB §4.5](../design/research/20260924-database-semantics.md#45-s5-queue-claims) rows 019–021; publish job timers: choice §17.34 |
| §6 publication | §7.4, §7.8, §11.2 | DB §4.2 rows 004–011, 058–061; KL §7 item 1; choice §17.33 |
| §6.3 partial publication | §7.4, §7.6, §7.8 | [PC §4](../design/research/20260924-provider-capability-comparison.md#4-the-matrix) row 083; [KL §3.2](../design/research/20260924-key-loss-restoration.md#32-what-each-case-showed) cases G, H; [RC §6.4](../design/research/20260924-retention-metadata-classification.md#64-criterion-4-provider-limits-and-the-alert-policy-the-evidence-supports) |
| §6.4 orphans | §7.4, §7.8, §13.2 | DB §9; KL case G; PC §4 row 065; the orphan-report identity's grants and the report's selection: §16 check, the pinned OpenBao only (choice §17.30) |
| §7 idempotency | §11.1, §12.5 | [DB §4.3](../design/research/20260924-database-semantics.md#43-s3-unique-operation-intent) rows 012, 013; DB row 061; DB §4.6 (advisory lock); [E1 §7](../design/research/20260922-secret-ingress-extraction-before-persistence.md#7-limits), [E1 §4.4](../design/research/20260922-secret-ingress-extraction-before-persistence.md#44-the-forbidden-design-measured) |
| §8 operations | §11.1, §12.5, §15.2 | [DS §4.2](../design/research/20260925-dispatch-safety.md#42-ownership-loss-and-a-second-executor-criterion-2) row 009; DB §4.5 rows 020, 021 |
| §9 API | §11.1, §11.2, §13.7 | none: FR §9 item 5 |
| §10 authentication, authorization | §13.1, §13.3, §13.6, §13.7 | none: FR §9 item 5; [DS §4.1](../design/research/20260925-dispatch-safety.md#41-revocation-around-the-commitment-boundary-criterion-1) row 003 for approval revocation and its lock |
| §11 migrations | §7.2, §14.2 | [DB §4.6](../design/research/20260924-database-semantics.md#46-s6-migrations) rows 022–026; [DB §6.2](../design/research/20260924-database-semantics.md#62-criterion-2-intent-ownership-queue-claims-migrations-restored-state) |
| §12 restored state, epoch | §7.7, §7.8, §14.6 | DB §4.7 row 027; DB §9 (inferred); [KL §7](../design/research/20260924-key-loss-restoration.md#7-recommendation) items 2, 5; FR §9 item 4 |
| §16 gaps | §18.1, §18.2 | [FR §9](../design/research/20260925-feasibility-evidence-review.md#9-gaps), FR §10 |
