# Dependency monitor contract

This document specifies how the first milestone classifies the secret and key
versions its releases depend on, when it alerts on them and what the
classification may never be used for, as required by
[Implement dependency retention checks](https://github.com/ginsys/bronzeward/issues/24).
It refines the [current design](../design/Talos_Configuration_and_Machine_Management_Design.md)
§7.6, §7.8 item 3 and §15.3 for the PoC profile, one OpenBao node with KV v2
and Transit as the provider
([design §7.7](../design/Talos_Configuration_and_Machine_Management_Design.md#77-poc-deployment-profile)).
The [specification review](https://github.com/ginsys/bronzeward/issues/20)
found that no contract owned the monitor: the design set its policy and the
retention run supplied evidence, but no section fixed the classification
procedure, the alert timing or the metadata-only access. This is that
section. It is a PoC contract, not evidence that any mechanism here has been
built.

The evidence it rests on, abbreviated below:

| Short name | Report |
| --- | --- |
| RC | [Retention and metadata classification](../design/research/20260924-retention-metadata-classification.md) |
| PC | [Provider capability comparison](../design/research/20260924-provider-capability-comparison.md) |
| KL | [Key loss and restoration](../design/research/20260924-key-loss-restoration.md) |

Three sibling contracts share its boundaries: the
[persistence and API contract](persistence-api.md) ("PA"), the
[secret ingress and compilation contract](compilation.md) ("compilation") and
the [execution and recovery contract](execution-recovery.md) ("execution and
recovery"). Where neither the design nor the evidence decides a question, this
contract takes the most conservative option and marks it in place as
**(choice §11.n)**; §11 lists each with its alternative for owner review.

## 1. Scope and interfaces

Design: [§7.6](../design/Talos_Configuration_and_Machine_Management_Design.md#76-metadata-only-dependency-checks),
[§7.8](../design/Talos_Configuration_and_Machine_Management_Design.md#78-poc-retention-and-recovery-policy),
[§15.3](../design/Talos_Configuration_and_Machine_Management_Design.md#153-metrics-and-alerts).

The monitor classifies every dependency of every release as `retained`,
`blocked`, `lost` or `unknown` (design §7.6), records the latest class, raises
the alerts of design §7.8 item 3 and serves both through the API. It takes:

- from compilation §9, the dependency records of each release: its effective
  and reproduction dependencies (KV versions) and its encryption dependency
  (a Transit key by its identity, with the version);
- from PA §6.2, the commit transaction (T3) that records them, which also
  seeds their status (§5.2);
- from compilation §1, the metadata identity (§4).

It gives:

- to compilation §6 step 3 and §11, the classification procedure of §3, which
  publication runs afresh for each pinned version and for its encryption
  dependency;
- to PA §9.2, three read routes (§7.2);
- to execution and recovery, nothing it may rely on for authority (§8).

The PoC deletes no dependency record (design §7.8 item 1), so the set the
monitor watches only grows. Local stores are out of scope: they are not the
selected profile, and RC found they give only `retained` and `unknown`
([RC §8](../design/research/20260924-retention-metadata-classification.md#8-recommendation)
item 2).

## 2. What is monitored

A **monitored dependency** is one provider object version under its §3
identity: a KV path with a version and its `created_time`, or a Transit key
identity with a version. A Transit key version recreated under the same name
and number is another monitored dependency, with its own status; a KV version
recreated so is refused at publication (§5.1), so it never has one. One provider object
version named by several releases, or by both dependency records of one
release, is one monitored dependency with one status **(choice §11.2)**. Its
alerts name every release whose dependency records reference it (a repeated
`deletion-scheduled` warning names only those not yet warned, §6.2); §5.2 orders
a publication against a transition, so no committed release is missing from
its alert, and §6.2 warns of a scheduled deletion every release committed
against it while the deletion is still ahead at the next pass; one that takes
effect sooner alerts as `blocked` instead (§10.2).

Each monitored dependency has a `dep` identifier (PA §2), created when its
first dependency record is committed. The identifier names the status, not the
provider object: after a database restore (§9) an identifier issued after the
backup is absent and is never reissued.

## 3. Classification procedure

Design: §7.6. Evidence: [RC §3.1](../design/research/20260924-retention-metadata-classification.md#31-rules-classifier-rows),
[RC §6.1, §6.2](../design/research/20260924-retention-metadata-classification.md#62-criterion-2-nothing-uncertain-becomes-lost).

One classification is one provider answer to one request:

1. **Ask by name.** One `GET` per monitored dependency: `secret/metadata/<path>`
   for a KV version, `transit/keys/<name>` for a Transit key. Never a `LIST`;
   a listing without the entry is never evidence (RC §6.2, "Missing
   listings").
2. **Take the provider's time.** A KV deletion time is compared with the
   answer's `Date` header, never with the monitor's clock
   ([RC §5.6](../design/research/20260924-retention-metadata-classification.md#56-a-deletion-time-compared-with-the-clients-clock)).
   The header is read only in one of the three HTTP-date forms (RFC 9110
   §5.6.7), exactly as that form writes it, so in whole seconds; any other
   text, a fractional second included, is not readable.
3. **Apply the rules.** The first row that matches decides:

| Provider | Answer | Class | Reason |
| --- | --- | --- | --- |
| any | the recorded version is not a positive integer | `unknown` | `malformed` |
| any | HTTP 403 | `unknown` | `denied` |
| any | HTTP 404 | `unknown` | `absent` |
| any | HTTP 503 | `unknown` | `unavailable` |
| any | no connection, no answer within the request timeout (§6.1), an answer that does not parse, any other status | `unknown` | `unreachable` or `unreadable` |
| KV v2 | `destroyed: true` for the version | `lost` | `destroyed` |
| KV v2 | version below `oldest_version`, when that is above 0 | `lost` | `pruned` |
| KV v2 | version above `current_version`, or absent from the answer | `unknown` | `insufficient-evidence` |
| KV v2 | the version's `created_time` missing, or other than the one the dependency record carries (compilation §9) | `unknown` | `identity-mismatch` **(choice §11.13)** |
| KV v2 | `deletion_time` set, and in the same second as `Date` or with no readable `Date` | `unknown` | `deletion-time-undecidable` |
| KV v2 | `deletion_time` before `Date` | `blocked` | `soft-deleted` (reversible by undelete) |
| KV v2 | `deletion_time` after `Date` | `retained` | `deletion-scheduled`, naming the time |
| KV v2 | no `deletion_time` | `retained` | none |
| Transit | the recorded version is in the key's `keys` map with a creation time other than the one the dependency record carries (compilation §9) | `unknown` | `identity-mismatch` **(choice §11.10)** |
| Transit | `soft_deleted` anything but `false` | `unknown` | `soft-delete-unobserved` |
| Transit | `min_available_version`, `min_decryption_version` or `latest_version` missing | `unknown` | `insufficient-evidence` |
| Transit | version above `latest_version`, listed in the `keys` map or not | `unknown` | `insufficient-evidence` (contradictory metadata, never classified by RC) |
| Transit | version absent from the `keys` map, below `min_available_version`, when that is above 0 | `unknown` | `trimmed-unverified` |
| Transit | version absent from the `keys` map, below `min_decryption_version` | `unknown` | `below-decryption-floor-unverified` (reversible by lowering it, if the key is the recorded one) |
| Transit | version absent from the `keys` map | `unknown` | `insufficient-evidence` |
| Transit | version below `min_available_version`, when that is above 0 | `lost` | `trimmed` |
| Transit | version below `min_decryption_version` | `blocked` | `below-decryption-floor` (reversible by lowering it) |
| Transit | otherwise | `retained` | none |

A Transit key's identity is its name together with the creation time its
`keys` map gives for the recorded version **(choice §11.12)**: a key deleted
and recreated under the same name gives its versions new creation times
([KL §7](../design/research/20260924-key-loss-restoration.md#7-recommendation)
item 1, inferred). Publication records it from two reads bracketing the
encryption (compilation §11), so a key recreated between them is refused, not
recorded. It also refuses a version whose creation time is not in a second
before the first read's `Date`: creation times are whole seconds, so only a
version still in its creation second could share it with a key recreated
later, and a version one second old cannot.
The `keys` map decides no floor, since it hides trimmed and blocked versions
alike (RC §3.1). A version absent from it cannot have its identity checked, so
it is `unknown` whatever the floors say: a replacement key's floor says
nothing of the recorded key, and lowering it restores none of that key's
ciphertext. The reason names the floor the version is below, if any, so an
operator who knows the key is the recorded one can act on it. While the `keys`
map hides every version below a floor, Transit gives neither `lost` nor
`blocked`; a loss still alerts at once as a `regression` (§6.2). The `lost`
and `blocked` rows apply only to a version the map lists with its recorded
identity.

A KV version's identity is its path and version together with the version's
`created_time` **(choice §11.13)**. Deleting a path's metadata and writing it
again restarts its version numbers, so a replacement can answer for the
recorded path and version with no deletion time. Publication's classification
takes the `created_time` from its own answer, its value read must give the
same one, and compilation §9 records it (compilation §6 steps 3 and 4); every
later classification compares it. The destroyed and pruned rows come first:
KV metadata deletion removes every version, so the recorded version is gone
whichever path answers.

A 404 is `unknown` and never `lost`. OpenBao answers a deleted Transit key, a
KV path whose metadata was deleted and a name that never existed alike, with no
tombstone
([RC §6.4](../design/research/20260924-retention-metadata-classification.md#64-criterion-4-provider-limits-and-the-alert-policy-the-evidence-supports)).
Only the change from `retained` shows such a loss (§6). No timeout, repetition
or age turns `unknown` into `lost` (design §7.6), and nothing but a provider
answer changes a class.

The procedure is a pure function of the recorded dependency and the answer's
status, headers and body. The monitor (§6) and compilation §6 step 3 call the
same function.

## 4. The metadata identity

Design: §7.6, [§13.2](../design/Talos_Configuration_and_Machine_Management_Design.md#132-provider-access-separation).
Evidence: [RC §6.3](../design/research/20260924-retention-metadata-classification.md#63-criterion-3-a-retained-verdict-grants-nothing)
row 057; PC §4.1.

The monitor authenticates to OpenBao with the metadata identity of
compilation §1 and with nothing else. Its policy grants `read` on
`secret/metadata/*` and `transit/keys/*` and no other capability
**(choice §11.9)**: no `list`, no `secret/data/*`, no Transit encrypt,
decrypt, rewrap or key configuration. It is a credential of its own, not
shared with the ingestion, compiler or executor identities, and the monitor
uses no other. Whether the monitor shares a process with components that hold
those identities is a deployment choice, as for every role of the trusted
controller (design §5, figure 2).

Holding no data read, the monitor cannot read a value, so nothing it logs,
stores or serves can carry one. RC row 057 showed the metadata token refused
(HTTP 403) on the value it had just classified.

## 5. State

### 5.1 Records

Three tables, all in PA §3:

| Entity | Kind | Holds |
| --- | --- | --- |
| DependencyStatus | mutable, row-locked | `dep` identifier; provider object, version and the creation time of its §3 identity; class and reason; when it was first recorded `retained`; since when it has been `unknown`, if it is; when its last `persistent` alert was raised; the scheduled deletion time last observed and the one last warned; the database time the latest recorded classification's request began (`observed_from`) and the database time it was recorded (`recorded_at`); the answer's `Date`, if any |
| DependencyAlert | immutable | `dal` identifier; recording sequence, allocated under the DependencyMonitor row lock (§6.1); the `dep` identifier; kind (§6.2); class and reason; provider object, version and creation time; the releases it names; the scheduled deletion time it warns of, for a `deletion-scheduled` alert only; `observed_from` and `Date`; epoch; time recorded. A `monitor-stalled` alert concerns no dependency: its `dep` identifier, provider object, version, class, reason, releases, `observed_from` and `Date` are empty |
| DependencyMonitor | mutable singleton, row-locked | the monitor's last progress (§6.3); its last completed pass; when it last raised `monitor-stalled`; the recording sequence of the last alert logged (§7.1) |

A DependencyStatus row is updated only under its row lock, in a transaction
that holds no provider request (PA §5 rule 1): the provider is asked first,
then the transaction records the answer. A row's `recorded_at` is never
before its `observed_from`; the schema refuses one that is, since §5.2's
re-check dates a transition by it. A KV version holds one row: the schema
refuses a second creation time for a KV path and version, since publication
fixes a pin's identity from that row (PA §6.1) and refuses another, so a
second could only come from two publications seeding the version
concurrently (§5.2).

An alert's insert locks the DependencyMonitor row itself before the schema
allocates its recording sequence, so no writer can allocate one outside that
lock (§7.1). The schema also refuses an alert whose kind, class and reason
are not a row of §3 and §6.2, a `monitor-stalled` alert with any dependency
field, and an alert that names a release twice or a release that does not
reference its version. The DependencyMonitor row is created with the
installation, its progress the installation time, so a monitor that never
runs is stalled after three intervals (§6.3). The schema refuses an update
that moves its progress or its last logged sequence back.

### 5.2 Seeding at publication

Publication's commit transaction (PA §6.2, T3) inserts a DependencyStatus row
for each provider object version and creation time its dependency records name
that has none, in provider object, version and creation time order, with class
`retained`, the reason and
scheduled deletion time publication's own classification gave, first seen
`retained` at the time publication began that version's classification, and
`observed_from` equal to that time **(choice §11.3)**. A version that already
has a row keeps it; a KV version with a row under another creation time, a
concurrent publication's included, keeps that row and refuses the
publication `422`, its identity changed (PA §6.2). Publication classifies every pinned version (compilation §6
step 3) and its encryption dependency (compilation §11), recording the Transit
identity of §3 from reads bracketing the encryption. It refuses every version
that is not `retained`, so every seeded row is `retained`. A seeded scheduled
deletion still ahead at the next pass is warned by it (§6.2).

After its inserts, T3 locks the DependencyStatus row of every version it names
`FOR SHARE`, in `dep` order, rows a concurrent publication inserted included. A
row whose class is not `retained` and whose `recorded_at` is not earlier than
the time publication began that version's classification refuses the
publication, as compilation §6 step 3 refuses a version. The comparison takes the time the class was recorded,
not the time its request began, since a request that began before
publication's own can observe a change after it; a time equal to publication's
start counts as after it, since a request cannot be shown to precede it. The
monitor reads a dependency's referencing releases after taking its row lock
(§6.1 step 5). A transition and a publication naming the same version are
therefore ordered: either the publication sees it and is refused, or its alert
names the release. A refusal can be conservative, for a change observed before
publication's own request but recorded after it began; the retried
publication classifies afresh. A scheduled deletion leaves a version
`retained` and needs no such ordering: §6.2 warns each release committed
against it, whenever it commits, while the deletion is still ahead at the
next pass; a shorter one alerts as `blocked` (§10.2).

Without this seed, a dependency lost between publication and the monitor's
first pass would be a dependency never seen `retained`, alerted after 15
minutes instead of at once as a regression.

## 6. Passes and alerts

Design: §7.8 item 3, §15.3. Evidence: RC §6.2 row 043, RC §6.4.

### 6.1 Passes

Every server instance runs the monitor, in recovery mode as outside it
**(choice §11.7)**. A process started with the recovery-start flag runs
neither passes nor the watchdog (§6.3) until its recovery-mode entry has
committed (PA §12.2), so nothing is recorded in the restored epoch before entry
fences it. A pass classifies every monitored dependency once. For each, on one
database session held from step 1 to step 5, it:

1. takes a session-level advisory lock on the dependency with
   `pg_try_advisory_lock`. If another instance holds it, that instance is
   classifying the dependency, and this pass skips it. The session runs with
   an idle-session timeout of 30 seconds, so the server ends a session whose
   process stopped and releases its lock;
2. reads the database time as `observed_from`;
3. sends the request of §3, with a request timeout of 10 seconds, holding no
   transaction (PA §5 rule 1);
4. classifies the answer;
5. in one transaction, locks the DependencyStatus row `FOR UPDATE` and records
   the class with `clock_timestamp()` read after the row lock is held as
   `recorded_at`, not the transaction's `now()`: that is fixed when the
   transaction began, before any wait for the lock, and would date a
   transition before a publication that began during the wait. It then reads
   the releases that reference the dependency, locks the DependencyMonitor
   row, inserts the alerts of §6.2 that the recorded class, or a release not
   yet warned of a scheduled deletion, calls for, and records its progress
   (§6.3). Each alert names every one of those releases, except a
   `deletion-scheduled` alert, which names only those the version's earlier
   alerts for the same scheduled time did not (§6.2). An alert's recording sequence is
   allocated under that lock, which is held to commit, so sequences commit in
   order (§7.1). It commits, then releases the advisory lock.

A request that fails, is refused or times out, and an answer that cannot be
read, are not exits: §3 classifies each `unknown` with its reason, and step 5
records it like any class, so an outage drives `regression` and `persistent`
(§6.2). Every other exit from steps 2 to 5 releases the advisory lock too, or
ends the session: an internal failure that abandons the dependency for this
pass, and a step 5 transaction that fails, is refused or times out. A session-level lock outlives a rolled-back
transaction, so a session kept for reuse while still holding it would keep
every pass from that dependency until the session ended.

The advisory lock serializes steps 2 to 5 per dependency across instances, so
classifications are recorded in the order their answers were observed, and an
older answer never overwrites a newer one **(choice §11.7)**. Step 5 runs on the
session that holds the lock: a session the server ended cannot record, and a
process that reconnects has lost the lock and starts again from step 1.

A pass starts 60 seconds after the previous one started, or at once if the
previous one took longer **(choice §11.4)**. "At once" in design §7.8 means
on the first pass that observes the change, so at most one interval and one
pass's duration after it happened. Because an alert is raised only in the
transaction that records its transition under the row lock, two instances
raise one alert per transition, not two.

The interval, the request timeout, the idle-session timeout, the
persistent-unknown interval (15 minutes, design §7.8), the stall bound, the
timeouts of a DependencyMonitor lock holder (§6.3) and the logger's batch
bound (§7.1) are fixed PoC values, held as named constants in one place so
that a later version can make them configurable.

### 6.2 Alert kinds

| Transition or state | Kind | When |
| --- | --- | --- |
| any class to `lost` | `lost` | at once, once per entry into `lost` |
| any class to `blocked` | `blocked` | at once, once per entry into `blocked`; the alert states that the block is reversible and how (reason, §3) |
| `retained` or `blocked` to `unknown` | `regression` | at once; the alert states that a 404 may be a deletion or a restore older than the database (PA §6.3) |
| `unknown` for 15 minutes since `unknown_since`, including a dependency never recorded `retained` | `persistent` | at 15 minutes, then every 15 minutes while it stays `unknown` **(choice §11.6)** |
| `retained` with a scheduled deletion time not yet warned to every release referencing the version | `deletion-scheduled` | at once, once per distinct scheduled time; again for that time, naming only the releases its earlier warnings did not |
| no progress for three intervals | `monitor-stalled` | §6.3 |

`unknown_since` is set when the class becomes `unknown` and cleared when it
leaves `unknown`. A dependency leaving `blocked`, `lost` or `unknown` for
`retained` raises no alert; its new class is served (§7.2). A failing provider
metadata check (design §15.3) shows as `unknown` with its reason, and alerts
through `regression` and `persistent`.

`deletion-scheduled` is raised by the pass that records a schedule while its
time is still ahead, and by any later pass that finds a release its earlier
warnings for that time did not name: the releases referencing the version,
read after the row lock (§6.1 step 5), are compared with those named by the
version's `deletion-scheduled` alerts that warn of that same time. A release committed against a schedule
already warned, before or while that warning's pass ran, is therefore named
by the next pass, if the deletion is still ahead then. A deletion that takes effect before a pass records it,
one only publication saw included (a row publication seeds carries the
schedule for the read routes; an existing row keeps its own, since T3 inserts
only missing rows, §5.2), is first recorded as `blocked`, which alerts at once. The
warning is therefore not guaranteed for a schedule shorter than one interval
and one pass (§10.2).

### 6.3 Silence is not health

Each step 5 of §6.1 and each completed pass record the monitor's progress on
the DependencyMonitor row, and a completed pass records its completion time.
Progress is `clock_timestamp()` read after the row lock is held, not the
transaction's `now()`, which a wait for the lock would leave behind, and it
never moves back: a transaction that began before another recorded keeps the
later time. A pass slowed by request timeouts, as during a partition, still
progresses. When there has been no progress for three intervals, the instance
that notices raises one `monitor-stalled` alert, recorded under that row's
lock, and raises it again after each further three intervals without progress
**(choice §11.8)**.

The noticing is not a pass's: a hung pass would never notice. Each instance
runs a watchdog on a schedule of its own, once per interval, independent of
its passes. It reads the DependencyMonitor row with a plain read, which waits
for no lock, and compares the last progress and the last `monitor-stalled`
with the database time. Only when an alert is due does it lock the row, check
again under the lock and insert the alert. A watchdog records no progress, so
a watchdog alone never keeps the monitor from being stalled.

A lock holder that hangs cannot hold the DependencyMonitor row for long:
every transaction that locks it, a pass's, a watchdog's and a logger's
(§7.1), runs with a statement timeout and an idle-in-transaction timeout of 10
seconds, so the server ends a stuck holder's transaction and releases the
lock. A watchdog whose own lock wait times out tries again on its next run,
which then finds the lock free.

The read routes (§7.2) serve the last completed pass's time, and mark a served
class `stale` when its own `observed_from` is more than three intervals before
the database's time, so a reader never takes an old classification for a
current one (design §15.3), a seeded one included. A server that is down
raises nothing: its liveness probe is the deployment's to watch.

## 7. Delivery

**Owner decision (2026-09-29):** each alert is a structured log line and a
DependencyAlert row, and the status and alerts are served by read routes. A
metrics endpoint and push delivery are not part of the PoC.

### 7.1 The log line and the record

The transaction of §6.1 step 5 inserts the DependencyAlert row; the row is the
record. After it commits, after a watchdog's `monitor-stalled` alert commits
(§6.3), since the passes may be the thing that hangs, and at the start of
every pass, the instance logs the alerts not yet logged. A log write can
block, so no lock a pass or the watchdog needs is held across it:

1. The logger's transaction takes a transaction-level advisory lock that only
   loggers take, and runs with an idle-in-transaction timeout of 10 seconds.
   An instance that finds the lock held logs nothing this time. The
   transaction reads the last logged sequence.
2. A separate short transaction locks the DependencyMonitor row `FOR SHARE`,
   reads at most 100 alerts above that sequence in recording order, and
   commits.
3. With no DependencyMonitor lock held, the logger writes one log line per
   alert read. Each line has the stable event name `dependency-alert` and the
   row's fields: the `dal` and `dep` identifiers, kind, class, reason,
   provider object, version and creation time, the releases it names, and,
   for a `deletion-scheduled` alert, the scheduled deletion time it warns of.
4. The logger's transaction advances the last logged sequence to the last
   alert written and commits. While step 2 found 100, the logger repeats
   from step 1, so a backlog advances one bounded batch at a time and a batch
   that outlives the timeout repeats only itself.

Every alert is inserted under the DependencyMonitor row lock, held to its
commit (§6.1, §6.3), and step 2 reads the alerts by a statement after its
`FOR SHARE` lock is granted, which waits for every such holder. So when the
logger reads, every alert with a sequence already allocated has committed or
rolled back: the cursor never passes a sequence that commits later. A
rolled-back allocation leaves a gap that nothing fills. A logger paused or
blocked in step 3 delays only other loggers, and only until its timeout ends
its transaction; its lines are then written again.

A process that dies after an alert commits but before its line leaves the
alert above the last logged sequence, and the next instance to log writes it.
A process that dies after writing lines but before advancing writes them
again: delivery is at least once, and the `dal` identifier tells a repeat.
Neither the line nor the row carries a value (§4).

### 7.2 Read routes

Added to PA §9.2, any role, paginated as PA §9.1 sets:

| Route | Result |
| --- | --- |
| `GET /dependencies`, optionally `?class=<class>` | each monitored dependency with its class, reason, first-seen-`retained` time, `unknown_since`, the time of its latest recorded classification and `stale`; the collection carries the last completed pass's time |
| `GET /dependencies/{id}` | one, with the fields of the collection's items; its releases and alerts are the two routes below, which grow without bound |
| `GET /dependencies/{id}/releases` | the releases referencing it, in release order |
| `GET /dependencies/{id}/alerts` | its alerts, in recording order |
| `GET /dependency-alerts` | every alert in recording order |

They serve provider paths, key names and versions, which are reference names
(PA §9.1, "Redaction"), never a value or ciphertext.

## 8. What a classification is not

Design: §7.6 ("retention and authority are separate checks"). Evidence:
RC §6.3.

- **Not publication's check.** Compilation §6 step 3 classifies each pinned
  version afresh with the procedure of §3 and never reads DependencyStatus,
  which can be a pass behind **(choice §11.11)**. A version that is not
  `retained` refuses publication.
- **Not authority.** A `retained` class admits no dispatch, approves nothing
  and stands in for no read or decrypt. Execution's use-time check, the
  executor decrypting the artifact at the point of use, stays execution and
  recovery §3.1; a retained dependency with the executor's access revoked is
  still refused there (RC rows 063, 064).
- **Not a deletion.** The monitor writes no provider object and changes no
  class but by a provider answer. Restricting Transit key deletion and KV
  metadata deletion, and setting KV `max_versions` wherever a path is
  overwritten, stay deployment requirements
  ([RC §9](../design/research/20260924-retention-metadata-classification.md#9-hand-off);
  design §7.8 item 1).

## 9. Restored state

For a database restored to a backup taken at time *T* (PA §12.3):

- DependencyStatus is as at *T*. A dependency recorded `retained` that now
  answers 404 raises `regression` on the first pass (PA §6.3, §13.3).
- DependencyAlert rows after *T* are absent. Their log lines remain.
- The DependencyMonitor row is as at *T*, so an alert recorded before *T* and
  logged after it is logged again: delivery stays at least once, and a
  consumer tells the repeat by its `dal` identifier (§7.1). Alerts recorded
  after *T* are gone and are not logged again.
- `dep` and `dal` identifiers issued after *T* are absent and never reissued
  (PA §2).

For a provider restored to a snapshot older than the database, the generations
created after the snapshot answer 404 and raise `regression` at once, and a
publication pinning them is refused (PA §13.3).

## 10. Verification and evidence limits

### 10.1 Required verification

An implementation of this contract must show, each with a control that can
fail:

1. **Every row of §3's table** on synthetic answers, through the
   implementation's classifier, including the same-second and no-`Date` rows,
   a set `soft_deleted` and each missing floor. Control: comparing the
   deletion time with the monitor's clock instead of `Date` misclassifies the
   same-second row.
2. **Against the fixture's OpenBao**, through the monitor: `retained`; a KV
   version soft-deleted (`blocked`), undeleted (`retained`), destroyed
   (`lost`); pruned past `max_versions` (`lost`); a Transit version below the
   decryption floor and trimmed (`unknown`, each reason naming its floor, since
   the `keys` map hides both); a deleted Transit key
   and deleted KV metadata (404, `unknown`); OpenBao sealed (503), partitioned
   and paused (`unknown`). Each alert as §6.2 sets and no other.
3. **A retained dependency turning unreadable alerts at once**: a 404 after
   `retained` raises `regression` on the next pass. Control: without the seed
   of §5.2, a dependency lost before the first pass raises nothing until
   `persistent`.
4. **Persistent unknown**: `persistent` at 15 minutes and every 15 minutes
   after, never before, with the class `unknown` throughout. The intervals may
   be shortened by a test-only setting. Control: without the last-alert check,
   `persistent` repeats on every pass.
5. **Metadata only**: the metadata identity is refused a `secret/data/*` read
   and a Transit decrypt of a dependency it classified `retained`, and every
   provider request the monitor sends carries the metadata identity's token.
   Control: the same reads with a token that has them succeed.
6. **One alert per transition across instances**: two instances observing one
   transition raise one alert. Control: without the row lock, two.
7. **Silence**: passes stopped by an injected hang on every instance raise
   `monitor-stalled` after three intervals, through the watchdog, and the
   routes serve `stale`. Control: a pass slowed past three intervals by
   request timeouts raises nothing, and measuring from completed passes
   instead of progress raises it there; running the stall check inside the
   pass loop raises nothing under the hang. A pass hung while it holds the
   DependencyMonitor row lock has its transaction ended within 10 seconds, and
   the watchdog raises `monitor-stalled`; control: without the lock holder's
   timeouts, every watchdog run times out on the lock and nothing is raised. A
   step 5 that waits for the DependencyMonitor lock records progress no
   earlier than the grant, and never earlier than progress already recorded;
   control: recording `now()`, it records the time its transaction began.
8. **Publication refused** for a pinned version or an encryption dependency
   that is `blocked`, `lost` or `unknown` (compilation §6 step 3, §11), and
   **no dispatch admitted by a retained class** (execution and recovery §3.1).
9. **A recreated Transit key** under the same name classifies
   `identity-mismatch`, and a key recreated between publication's encryption
   and its second read refuses the publication. Controls: comparing the name
   and version alone classifies it `retained`; taking the identity from the
   read after encryption alone records the new key's identity for the old
   key's ciphertext. A version created in the second of the first read's
   `Date` is refused and recorded one second later. Control: without that
   check, a key deleted and recreated within its creation second is recorded,
   and its lost version later classifies `retained`. A version within every
   floor but absent from the `keys` map classifies `unknown`; control:
   without that row, it classifies `retained`. A recreated key whose floors
   are above the recorded version, which its `keys` map omits, classifies
   `unknown` naming the floor; control: with the floor rows before the
   absent rows, it classifies `blocked` or `lost`. A KV path whose metadata
   was deleted and written again to the recorded version classifies
   `identity-mismatch`, and a path rewritten between publication's
   classification and its value read refuses the publication; controls:
   comparing path and version alone classifies it `retained` and records the
   replacement.
10. **A publication racing a transition**: a publication naming a monitored
    version while the monitor records its change from `retained` is refused,
    or is named by the alert, in three orders: the row exists; two
    publications are the first to name the version; the monitor's request
    began before publication's and observed the change after it. Controls:
    without T3's `FOR SHARE` lock, or locking before its insert, or comparing
    `observed_from` instead of `recorded_at`, the release commits and the
    alert omits it. A fourth order: the monitor's transaction begins, waits
    for the row lock while publication begins its classification, then
    records the change; publication is refused. Control: taking
    `recorded_at` from `now()`, the release commits and the alert omits it.
    A change recorded at exactly the time publication began its
    classification refuses it; control: comparing "later than", the release
    commits. A `retained` row with a scheduled deletion does not refuse it,
    whenever recorded or warned; control: refusing such a row recorded not
    before publication began, the release is refused. A release committed against a schedule
    already warned, before and during the warning's pass, is named by the
    next pass's `deletion-scheduled` alert for that time, which names no
    release an earlier warning named; controls: warning once per scheduled
    time only, the later release is never named; without excluding the
    releases already named, every pass warns again.
11. **The log after a crash**: a process stopped between an alert's commit
    and its log line has the line written by the next instance to log, and an
    alert whose transaction commits after a later-started one's is logged.
    Controls: without the last logged sequence, the line is never written;
    allocating the sequence before the DependencyMonitor row lock skips the
    later-committing alert. A logger paused in its log write delays no pass
    and no watchdog, and its lines are written again after its timeout.
    Control: holding the DependencyMonitor row lock across the write stalls
    the passes, and the watchdog records no `monitor-stalled`. With every
    pass hung, the watchdog's `monitor-stalled` alert is logged; control:
    logging only after a pass leaves it unlogged. A backlog of 250 alerts
    with a log sink slowed so that 250 writes outlast the timeout and 100 do
    not is logged in three batches; control: without the batch bound, the
    cursor never moves.
12. **Stale per dependency**: a seeded status whose `observed_from` is older
    than three intervals is served `stale` while passes complete. Control:
    deriving `stale` from the last completed pass serves it as current.
13. **Recovery start**: a process started with the recovery-start flag records
    no classification, alert or progress before its entry commits, and runs
    passes after it. Control: starting the monitor with the process records a
    status change in the restored epoch before entry.
14. **Overlapping monitors**: two instances classify one dependency while the
    provider loses it; one holds `retained` from before the loss and pauses
    before recording, the other asks after the loss. The status ends `lost`,
    with one `lost` alert. With the first paused past the idle-session
    timeout, its session is ended and its record refused, and the second
    records `lost`. Controls: without the advisory lock, the older `retained`
    overwrites `lost` and the next pass raises a second `lost` alert;
    recording on a new session after the first ended, the same. A step 5
    whose transaction fails on a session kept for reuse leaves the
    dependency to the next pass on any instance; control: releasing the
    advisory lock only after a commit, every later pass skips it until the
    session ends. A provider request that times out is recorded `unknown`
    by step 5 and raises `regression` after `retained`; control: treating it
    as an exit, the status stays `retained` and nothing is raised.
15. **A database restore** (§9): with alert A recorded before a backup and
    logged after it, and alert B recorded after it, the restored database
    holds A and not B, and the next logger writes A's line again under A's
    `dal` identifier and never writes B's. A new alert after the restore gets
    a `dal` identifier neither had, and is logged even when its recording
    sequence is the one B had. Controls: keeping the last logged sequence
    outside the database leaves A unlogged again and skips that new alert;
    giving a line an identifier of its own writes A's repeat under a new one.

Items 2, 3 and 8's publication refusal are acceptance-plan S2's negative
controls and S7's variants; the rest run as *checks*
([acceptance plan §7.1](acceptance-plan.md#71-required-verification)).

### 10.2 Evidence limits

- **One OpenBao topology**, single node and one unseal share; HTTP 503 was
  seen only as "sealed" ([RC §7](../design/research/20260924-retention-metadata-classification.md#7-limits)).
- **Transit `soft_deleted`** was false throughout RC; a set flag is `unknown`
  because its reversibility was never observed.
- **The same-second window** was not captured; its row rests on RC's rule
  checks. Clock skew between hosts was not produced.
- **The Transit identity** rests on KL's inference (KL §7 item 1): no report
  captured the `keys` map's creation times, and a key recreated under the same
  name was not classified. Publication's one-second rule (§3) rests on the
  creation time and `Date` coming from one clock that does not step back,
  which holds on the single node measured; on several nodes, or after a
  provider clock step, a recreation within a second could still reissue an
  identity. A version the `keys` map hides below a floor has its identity
  unchecked, so Transit gives `unknown` for it, never `lost` or `blocked`
  (§3).
- **The KV identity** rests on the `created_time` KV metadata gives each
  version; no report deleted a path's metadata and wrote it again, so a
  replacement sharing its original's `created_time` was not ruled out.
- **Read-only policy.** RC's metadata token also held `list`; this contract
  drops it, since the procedure never lists. The implementation's policy tests
  show `read` alone answers a generation's KV metadata and a Transit key's
  state, each classified `retained`, and that the identity is refused a value
  read, a list, a decrypt and an encryption under the key it classified and a
  key configuration, each beside a control with the refused grant added,
  against OpenBao 2.6.1 in dev mode, not the fixture's Raft node. That the
  monitor sends every request with this token is item 5's, still to show.
- **A short deletion schedule** can reach `blocked` without a
  `deletion-scheduled` warning (§6.2); `blocked` still alerts at once.
- **No interval is measured.** 60 seconds, 10 seconds, 15 minutes and three
  intervals are choices; the evidence shows only that a partition, a pause and
  a seal look alike from the client (RC §6.4).

## 11. Choices for owner review

Each is marked in place as **(choice §11.n)**. Owner decision, 2026-09-29
(ginsys/bronzeward#67): choices 1 to 11 stand as written, with choice 7's
per-dependency advisory lock and choice 8's measure by progress and
lock-free watchdog read decided the same day in that pull request's review.
Choices 12 and 13, which answer that review, were accepted as written on
2026-09-30; the inferences they rest on (§10.2) become evidence through item 9
of §10.1. Choice 12's residual risk, an identity reissued after a provider
clock step or on several provider nodes, is accepted for the PoC in design §7.7.

1. **A document of its own** rather than a section of the persistence and API
   contract, whose section numbers the acceptance plan cites. Alternative: a
   new section there, renumbering §15 onwards.
2. **One status per provider object version and §3 identity**, shared by
   every release that names it (§2). Alternatives: one per dependency record,
   which alerts once per release for one event; one per object and version
   alone, which gives a recreated version the status of the one it replaced.
3. **Publication seeds the status as `retained`** (§5.2). Alternative: leave
   the first classification to the monitor, delaying the alert for a loss
   before its first pass to `persistent`.
4. **A pass every 60 seconds, 10 seconds per request** (§6.1). Alternatives:
   a shorter interval, costing provider load per dependency; a longer one,
   delaying "at once".
5. **Delivery**: not a choice; the owner decided it (§7).
6. **`blocked` and `lost` alert once per entry; `persistent` repeats every 15
   minutes** (§6.2). Alternative: repeat every alert while its condition
   holds.
7. **Every instance runs the monitor**, deduplicated by the status row lock
   and serialized per dependency by a session advisory lock held from the
   request's start to its record (§6.1). Alternatives: one elected instance,
   which needs a lease and its failover; or ordering records by when the
   request began, which lets an answer observed before a change overwrite
   one observed after it.
8. **`monitor-stalled` after three intervals without progress** (§6.3),
   noticed by a watchdog that reads without a lock, with every
   DependencyMonitor lock holder bounded by server timeouts. Alternatives:
   one interval, alerting on a single slow classification; three intervals
   without a completed pass, which alerts on a pass merely slowed by
   timeouts; or stall state on a row of its own, which the logger's ordering
   by the DependencyMonitor lock (§7.1) cannot then cover.
9. **The metadata identity holds `read` only**, no `list` (§4). Alternative:
   RC's policy with `list`, which the procedure never uses.
10. **A Transit identity mismatch is `unknown`**, alerted as a regression if
    the dependency was `retained` (§3). Alternative: `lost`, which the
    evidence does not support.
11. **Publication classifies afresh** rather than reading the status (§8).
    Alternative: trust a status younger than one interval, saving a request
    per pinned version but admitting a version lost since the last pass.
12. **The Transit identity is the version's creation time** from the key's
    `keys` map (§3). Alternatives: name and version alone, which cannot tell
    a recreated key; or leave the identity unfixed, which leaves the
    `identity-mismatch` row unimplementable.
13. **The KV identity is the version's `created_time`** (§3), recorded at
    publication and compared by every classification. Alternatives: path and
    version alone, trusting the deployment's restriction on metadata
    deletion, which classifies a replacement `retained`; or every KV version
    `unknown` once its metadata could have been rewritten, which no answer
    can rule out.

## 12. Traceability

| Clause | Design | Evidence |
| --- | --- | --- |
| §2 monitored dependencies | §7.5, §7.8 item 1 | compilation §9 |
| §3 classification | §7.6 | [RC §3.1](../design/research/20260924-retention-metadata-classification.md#31-rules-classifier-rows), RC §5.6, RC §6.1, RC §6.2, RC §6.4; KL §7 item 1 |
| §4 metadata identity | §5, §7.6, §13.2 | RC §6.3 row 057; [PC §4.1](../design/research/20260924-provider-capability-comparison.md#41-permissions) |
| §5 state, seeding | §7.6, §7.8 | RC §8 item 1 |
| §6 passes, alerts | §7.8 item 3, §15.3 | RC §6.2 row 043; RC §6.4 |
| §7 delivery | §15.3 | none: owner decision |
| §8 not authority | §7.6 | RC §6.3 rows 056–064 |
| §9 restored state | §7.7, §7.8 | [KL §3.2](../design/research/20260924-key-loss-restoration.md#32-what-each-case-showed) cases G, H; PC §4 row 083 |
