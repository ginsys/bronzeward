# Secret ingress and compilation contract

This document specifies how the first milestone takes configuration in, keeps
secrets out of ordinary persistence, and compiles published per-machine
artifacts, as required by
[Specify secret ingress and compilation](https://github.com/ginsys/bronzeward/issues/17).
It refines the [current design](../design/Talos_Configuration_and_Machine_Management_Design.md)
§§6.2, 6.3, 6.5, 6.6, 6.9, 7.1, 7.4, 7.7, 7.8 and 13.2 for the PoC profile
(PostgreSQL with OpenBao KV v2 and Transit). Each section names the design text
it refines. It is a PoC contract, not evidence that any mechanism has passed
design experiment E2, E3 or E6.

The evidence it rests on, abbreviated below:

| Short name | Report |
| --- | --- |
| E1 | [Secret ingress: extraction before persistence](../design/research/20260922-secret-ingress-extraction-before-persistence.md) |
| SR | [Structural reference composition](../design/research/20260924-structural-reference-composition.md) |
| SP | [Sensitivity and provenance](../design/research/20260925-sensitivity-provenance.md) |
| E3 | [Talos compatibility](../design/research/20260925-talos-compatibility.md) |
| FR | [Feasibility evidence review](../design/research/20260925-feasibility-evidence-review.md) |
| DB | [Database semantics](../design/research/20260924-database-semantics.md) |
| KL | [Key loss and restoration](../design/research/20260924-key-loss-restoration.md) |
| PC | [Provider capability comparison](../design/research/20260924-provider-capability-comparison.md) |

Every result in those reports holds for one Talos version (v1.13.6), container
nodes with one control plane and one worker, and exact-copy leak scanning
([FR §2](../design/research/20260925-feasibility-evidence-review.md#2-what-binds-every-conclusion-here)),
and the composition results for strategic merge patches only
([SR §8](../design/research/20260924-structural-reference-composition.md#8-limits);
[SP §8](../design/research/20260925-sensitivity-provenance.md#8-limits)).
This contract inherits those limits; §15 lists them.

Where the evidence does not decide a question, this contract takes the most
conservative option and marks it in place as **(choice §16.n)**; §16 lists each
with its alternative for owner review.

## 1. Scope, roles and interfaces

Design: [§13.2](../design/Talos_Configuration_and_Machine_Management_Design.md#132-provider-access-separation),
[§7.1](../design/Talos_Configuration_and_Machine_Management_Design.md#71-responsibility-split-and-secret-ingress).

In scope: ingestion (initial import, drift adoption and draft updates), secret
staging and its interruption recovery, the reference grammar and its
declarations, resolution order, validation stages, sensitivity, provenance and
redaction, dependency records, renderer selection, and the hand-off to
publication.

Out of scope, owned elsewhere: the persistence schema and API
([ginsys/bronzeward#18](https://github.com/ginsys/bronzeward/issues/18)), which
this contract uses only through the interfaces in §3 and §11; planning,
approval and dispatch ([execution and recovery](execution-recovery.md)); identity
and approval policy
([ginsys/bronzeward#14](https://github.com/ginsys/bronzeward/issues/14)), which
owns the concrete provider policies; retention windows beyond
[design §7.8](../design/Talos_Configuration_and_Machine_Management_Design.md#78-poc-retention-and-recovery-policy);
the adoption workflow ([ginsys/bronzeward#22](https://github.com/ginsys/bronzeward/issues/22)
for the import, [ginsys/bronzeward#25](https://github.com/ginsys/bronzeward/issues/25) for the
adoption);
and the edit and publication user flow
([ginsys/bronzeward#23](https://github.com/ginsys/bronzeward/issues/23)).

The design's single "privileged ingestion/compiler" role (design §13.2) is
split into two identities, so that the one that creates secrets never reads
them **(choice §16.1)**. The application identities themselves are owned by
[identity and approval policy](https://github.com/ginsys/bronzeward/issues/14)
(design §7.7); this table states what this contract needs from them, as input
to that work:

| Identity | Needs | Must not have | What PC measured |
| --- | --- | --- | --- |
| Ingestion | create-only secret generations; HMAC with the digest key (§4.1); encrypt and decrypt with the staging key (§3); encrypt with the baseline key (§2.3); obtain a certificate from the signer, for a `source: machine` ingestion (below) | every other secret read; artifact and baseline decryption; machine operation | `publisher`: `create` on `secret/data/gen/*` only, and a refused replace ([PC §2](../design/research/20260924-provider-capability-comparison.md#2-candidates-and-identities), [PC §4.1](../design/research/20260924-provider-capability-comparison.md#41-permissions) rows 001, 002). Staging-key, baseline-key and HMAC use were not measured. |
| Compiler | read the pinned secret versions; encrypt with the artifact key | artifact, baseline or staging decryption; secret creation; the signer's path `secret/data/access/talos-ca/*`; machine operation | `compiler`: `read` on `secret/data/*`, encrypt on the artifact key, decrypt refused (PC §4.1 rows 004, 038, 040) |
| Executor | decrypt artifacts, gated by approval; obtain a certificate from the signer (below) | every other secret read; staging and baseline decryption | `executor`: decrypt only, secret read refused (PC §4.1 rows 039, 005); [execution and recovery §3.1](execution-recovery.md#31-execution-time-evidence-gathered-before-the-transaction) |
| Signer | `create`, `read` and `update` on `secret/data/access/talos-ca/*`, for [persistence and API §3.4](persistence-api.md#34-first-keys-and-the-talos-signer) | `list`, `delete`, any other path, any decryption; machine operation | not measured; [persistence and API §16](persistence-api.md#16-verification-and-evidence-limits) requires the grants |
| Metadata | [design §7.6](../design/Talos_Configuration_and_Machine_Management_Design.md#76-metadata-only-dependency-checks) classification | any value | `metadata`: KV metadata and Transit key state, no data (PC §4.1) |
| Orphan report | `list` and `read` on `secret/metadata/gen/*`, for [persistence and API §6.4](persistence-api.md#64-orphans)'s report | any value; any other path; Transit | `metadata` listed under one generation path (PC §4 row 065); this identity's grants and refusals measured by [persistence and API §16](persistence-api.md#16-verification-and-evidence-limits)'s check, on the pinned OpenBao only |
| Access presence | `read` on `secret/metadata/access/talos-ca/*`, for [persistence and API §3.3](persistence-api.md#33-talos-access)'s presence read (choice §17.42 there) | any value; any other path, `list`; Transit | not measured; its grants and refusals are specified in persistence and API §16 |
| Normal API | metadata and workflow | any secret value, any plaintext input | not measured; design §13.1 |

The Talos CA key is the one management credential in the provider: each
cluster's `os` CA key at `secret/data/access/talos-ca/<cluster id>`, held by the
signer identity and supplied by an author through a first key, never written by
the operator with the provider's tooling ([persistence and API
§3.3](persistence-api.md#33-talos-access), [§3.4](persistence-api.md#34-first-keys-and-the-talos-signer))
**(choice §16.29)**. The ingestion and executor identities never read it: each
obtains a short-lived certificate from the signer. The signer's path holds the
only copy Bronzeward uses to sign; a control plane configuration imported from a
machine carries the same `machine.ca.key` as a schema secret, so §2.3 extracts it
into an ordinary secret generation like every other secret, and the compiler
reads that generation to render the configuration. This contract does not change
that copy and does not claim the compiler never sees the key's value; it claims
only that the compiler cannot read the signer's path and signs nothing
(whether the compiler needs its own copy is open in ginsys/bronzeward#889). Its path
is disjoint from `secret/data/gen/*`, so the ingestion identity still never reads
a secret it creates (choice §16.1). The access-presence identity reads only
the path's metadata, for the presence answer (persistence and API §3.3). The compiler's grant PC measured, `read` on
`secret/data/*`, would cover it; the compiler's grant is `read` on
`secret/data/gen/*`, as the fixture's compiler policy already has it
(`fixtures/openbao/policies/bw-compiler.hcl`). No grant on the path was
measured; persistence and API §16 requires each. A certificate it signs for a
configuration read carries `os:admin`, the only role that reads a machine
configuration, so the "no machine operation" above is the application's own
bound, a read-only Talos client, not one the provider or Talos enforces.

Ingestion and compilation run in protected processing: a process that holds
plaintext only in memory, writes no temporary file, and does not log input,
resolved values or renderer output except through §8.

## 2. Ingestion: extraction before persistence

Design: [§7.1](../design/Talos_Configuration_and_Machine_Management_Design.md#71-responsibility-split-and-secret-ingress),
[§6.9](../design/Talos_Configuration_and_Machine_Management_Design.md#69-secret-references-and-validation-stages),
[§9.1](../design/Talos_Configuration_and_Machine_Management_Design.md#91-adopt-an-existing-configured-cluster),
[§12.4](../design/Talos_Configuration_and_Machine_Management_Design.md#124-drift-policy).

Every input that can hold a secret passes through one ingestion pipeline before
anything about it is persisted: a configuration read back from a node for
import or drift adoption, and every draft update, including an operator's
edited YAML text. The API layer hands the request body to the pipeline without
logging or storing it.

### 2.1 Typed boundary

The reader returns the input as an **unresolved** value that no generic sink
accepts: it has no string, formatting or serialization method that yields the
bytes, and it can be passed only to the parser inside the ingestion package.
Every ordinary persistence function accepts only a **sanitized** value, and the
sanitized type is constructed only inside the ingestion package, by an
unexported constructor, from extraction or from the staging resume path (§3).
The zero value is refused by every persistence function. This is E1's
structural argument moved from a call-site test into the compiler, and started
at the reader instead of after parsing
([E1 §8](../design/research/20260922-secret-ingress-extraction-before-persistence.md#8-recommendation)
item 2; [E1 §6](../design/research/20260922-secret-ingress-extraction-before-persistence.md#6-what-this-decides)).

### 2.2 Addressing

A path names one node of one document of a multi-document stream:
`doc[<n>]` followed by an [RFC 6901](https://www.rfc-editor.org/rfc/rfc6901)
JSON Pointer inside that document, for example
`doc[0]/machine/files/0/content`. JSON Pointer escapes `/` as `~1` and `~` as
`~0`, so a key holding a dot or a bracket, such as a node label or annotation
key, is addressable: `doc[N]/labels/example.test~1role`. A node label is in
the node's `KubeNodeConfig` document, not under `machine`, and `N` is the
position of that document in its stream: read from the stream, never assumed.
Every document of the stream is addressable. E1 found both gaps in its own path
syntax ([E1 §7](../design/research/20260922-secret-ingress-extraction-before-persistence.md#7-limits);
E1 §8 item 5); this escaping scheme is chosen here **(choice §16.2)**, and §15
states what tests it.

A path inside an identified embedded document (§5.4) appends `|<format>` and a
second pointer: `doc[0]/cluster/inlineManifests/0/contents|yaml/stringData/password`.
The first `|` ends the outer pointer, so an outer token cannot hold `|`; an
inner token can. An outer mapping key holding `|` is therefore refused at parse
(§2.3 step 2), and so is resolving a `mapping` value whose key holds `|` outside
an embedded document (`bad-path`, at the reference's path): either would
give a node a path that reads as an embedded document's.

### 2.3 Pipeline

0. **Claim.** The claim row (§3.2) is created in state `held`, with no
   payload, before the input is read. With `source: document` the request
   body carries the input: it is decoded into the unresolved type and
   fingerprinted in memory first (persistence-api §7.1), and nothing of it is
   extracted or stored before the claim exists.
1. **Read** into the unresolved type (§2.1).
2. **Parse** every document of the stream. A parse failure refuses the input;
   the refusal quotes no input text. A mapping that holds a key twice is a
   parse failure, as YAML forbids it, so a path never names two nodes. So is a
   mapping key that is not a scalar, which no path token can name, an outer
   mapping key holding `|` (§2.2), and an outer `!!binary` key, which decodes
   to text its token does not show.
2a. **Release a filled hostname** (§6.1, **choice §16.37**). For an import
   (onboarding included) or a drift adoption of a machine that has a stored
   name, a `HostnameConfig` document of the parsed stream whose `hostname`
   equals that name exactly, as the API read it for this request, is removed
   from the stream, whole, before anything is identified. The stream's
   positions (§2.2) stay those of the input: an initial operator mark is
   resolved against the stream as step 2 parsed it, with the removed document
   still counted, so no surviving document changes its `doc[N]`. Only the
   stored sanitized document is shorter, and a later mark on a staged document
   (§3) uses that document's own positions. A declaration of an embedded
   document (§5.2) after the removed one is stored under its new position, and
   one inside the removed document is dropped with it. The document is recorded as
   filled from the name, not kept as an explicit hostname. Talos's document
   holds only `auto` and `hostname`, so nothing else is lost, and a document
   that kept `auto: off` would stop the fill (§6.1). **A legacy hostname is
   released the same way, as a key**: where the stream holds no `HostnameConfig`
   document (Talos refuses both) and step 5a will fill the hostname (the
   stable-hostname guard: `machine.features.stableHostname` is not `true`, and
   neither it nor any mapping that holds it is a reference or carries a mark of
   step 3, or step 5a would suppress the fill and the hostname would be lost),
   a `machine.network.hostname` that is a plain string equal to the name exactly
   is removed from that document, the key alone: its parent mapping stays,
   even left empty. The key goes because the filled document cannot stand
   beside it (§6.1), and nothing else is lost. It is recorded as filled from
   the name, with "exactly the fill's own document" **false**: the node holds
   the hostname as a v1alpha1 key and the release sends a document, which is
   not a difference of form, so the form-only verdict of §6 step 8 reads false
   for it (§6.1 item 5). No document is removed, so no position changes. Only
   that document or that key is removed. A hostname that differs from the name
   (a different case included; a full hostname equal to a full name is removed
   like any other, and `auto: null` counts as no `auto`), a machine with no
   name, and a configuration with neither stay as they are. The removal comes
   first so that nothing is minted for a value that is then dropped: an
   operator mark that addresses a node of the removed document or the removed
   key refuses the input, as a mark that addresses no node does (step 3). So
   does a mark made later, in review (§3.6), on `machine.features.stableHostname`
   or a mapping that holds it where this step removed a legacy key: the value
   would become a reference, step 5a would suppress the fill and the hostname
   would be lost. The person imports again with that mark supplied at the start.
   The decision and the name it used are kept beside the sanitized document, in
   the claim's staged payload, and carried unchanged through a mark, a
   continuation and a takeover, so that a recovered run still holds the name it
   compared; the draft transaction refuses when the machine's name is no
   longer that one
   ([persistence and API §3.2](persistence-api.md), choice §17.45 there). The
   name and whether the removed document was exactly the fill's own (a removed
   key never is) are stored with the import base revision (§6.1 item 5). It is
   the one value ingestion removes that is not a secret, and the baseline of
   step 8 stays the exact input.
3. **Identify** the values to extract: every field the pinned Talos machinery
   marks secret (the `pkg/machinery` `RedactSecrets` field list, SP §2), and
   every path the operator marks in the request. A mark that addresses no node
   refuses the input before any provider write, as E1's rejected rows were
   ([E1 §4](../design/research/20260922-secret-ingress-extraction-before-persistence.md#4-expected-and-observed),
   4.6). What the schema list is evidenced to cover, and what not, is §2.4.
   A node that already carries a `!bwref` tag is excluded from identification
   and substitution: it is already a reference, so a draft update of a
   sanitized document, or a further mark on a staged one (§3), mints no second
   name for it. Its content is a reference name, not a value, so it adds
   nothing to the extracted values the guard (§4.2) searches for.
   Identification reads such a node as a null, so a document whose identified
   nodes the machinery cannot load as nulls (a mark on a document's `kind`)
   refuses the input: once stored, it could not be ingested again.
   A document is loaded as composition loads a fragment, so its `$patch:
   delete` directives (step 5 of §6) are accepted. The machinery drops a
   directive's mapping whole, so a directive mapping that holds anything
   besides the directive refuses the input: that content would be stored
   without ever being loaded. A directive in a list entry refuses the input
   too, since the member that selects the entry is dropped unloaded and could
   hold a schema secret; list-element overrides by selector are not run (§15).
4. **Substitute** each identified value by a reference (§5) under a newly
   minted logical name at version 1, with its declaration (§5.2), producing the
   candidate sanitized document. Names are minted as §5.1 states. The
   candidate is the pinned encoder's output written again from its own parse
   until it no longer changes: the encoder moves a blank line inside a run of
   comments, as talosctl writes them, and a later mark (§3.6 item 3) refuses
   any text its re-encoding changes. The guard searches that final text.
5. **Guard** (§4.2). A guard hit refuses the input; no provider object has been
   created yet.
6. **Create** one provider generation per extracted value, create-only (`cas=0`)
   at a path of its own. This takes the first branch of design §7.8 item 1
   (a path of its own, rather than an overwritten path with an explicit
   `max_versions`) **(choice §16.3)**. The path includes a component that the
   database does not issue: the staging claim's identifier followed by a random
   value identifier
   ([persistence §1.1](persistence-api.md#11-what-this-contract-takes-from-compilation)
   item 1). The reason is inferred, not measured: a restore
   rewinds database identifiers
   ([DB §4.7](../design/research/20260924-database-semantics.md#47-s7-restored-state)),
   and DB infers that a provider object named after such an identifier could
   then match a different object issued after the restore
   ([DB §9](../design/research/20260924-database-semantics.md#9-hand-off));
   with `cas=0`, such a collision is refused rather than overwritten. The
   generation's data is the value with its kind, `{"kind": …, "value": …}`
   **(choice §16.28)**.
7. **Construct** the sanitized value. Only now may it reach staging (§3) or the
   draft transaction.
8. For import and drift adoption, **encrypt the exact input** as the baseline
   under the baseline key, and record its keyed digest (§4.1) and its
   configuration digest, the function of
   [execution and recovery §1](execution-recovery.md#1-supported-operation-and-state-values)
   over the same input, read as that section defines **(choice §16.26)**. The
   baseline
   ciphertext is persisted by the draft transaction, with the draft. It is the
   only persisted form of the unextracted input. The baseline key is separate
   from the artifact key: ingestion may encrypt with it, and no identity in §1
   may decrypt with it, so the executor cannot read a baseline
   **(choice §16.4)**. Decrypting a baseline, for example to compare its bytes
   with the node's configuration, is an administrative operation outside this
   contract.

Under encrypted staging, the claim's payload is written after step 8, and not
before: one envelope holding the sanitized document, its references, the removed
hostname's decision and name where step 2a made one (neither is secret) and,
for import and drift adoption, the baseline ciphertext with its keyed digest and
its configuration digest, so that a taker has everything the draft
transaction persists; no identity can decrypt the baseline to compute them
again.

What E1 measured about interruption, for its own prototype and pipeline: its
80-row screen interrupted runs at ten points and found no synthetic secret in
the run root and the live tables at that moment, and "says nothing about the
heap, the write-ahead log or the backups" (E1 4.6). The database's disk and
backup-visible surfaces were read only in the captured bundles: the honest
runs, the runs held and killed in review, the run killed inside the database
transaction, and the recovery runs (E1 4.2). This pipeline differs from E1's
(the guard runs before the provider write, digests are keyed, the baseline is
step 8), so E1 measured no interruption of this pipeline; §15 states what
does.

An interruption after step 6 can leave provider generations that no draft
references. They are unused provider objects under
[design §7.4](../design/Talos_Configuration_and_Machine_Management_Design.md#74-publication-without-distributed-transactions);
the PoC deletes none of them (design §7.8).

### 2.4 What identification does not promise

The selected detector is the machinery's `RedactSecrets` field list, not E1's
detector. The evidence for it is SP's `schema-covers-base-secrets` control: no
leaf of either base that holds a bundle secret lay outside the schema's leaves,
in all 56 cells, with 9 schema leaves per base
([SP §4.4](../design/research/20260925-sensitivity-provenance.md#44-controls)).
Its limits: that control was never seen to fail, and both bases are
control-plane configurations (SP §8); the Docker-provisioned environment has no
disk-encryption, installer or disk configuration, which are secret-bearing
areas ([E1 §7](../design/research/20260922-secret-ingress-extraction-before-persistence.md#7-limits)).
The list covers Talos-typed secret fields only (SP §5;
[FR §8](../design/research/20260925-feasibility-evidence-review.md#8-cross-report-findings)
C1): a file's content, an inline manifest, a registry user name and an
annotation are not among them (SP §5), and the schema alone left the
whole-content case unredacted (SP §4.1). Talos exposes no field-level
sensitivity metadata through its running API (E1 4.7).

E1's own figures describe a different detector, its 11 name-keyed rules: recall
9/9, precision 9/10 with `cluster.id` over-extracted, and a secret at
`machine.files[].content` missed until the operator marked it (E1 4.7). They
show the division of responsibility, not the coverage of the selected list.
Over-extraction by the selected list is accepted as a cost, not a leak, but no
measurement of its precision exists.

Operator marking is therefore load-bearing, not a fallback, and an unmarked
value outside those fields carries no promise of detection (design §6.9; E1 §8
item 4).

## 3. Staging, ownership and interruption recovery

Design: [§7.1](../design/Talos_Configuration_and_Machine_Management_Design.md#71-responsibility-split-and-secret-ingress).

Staging holds a sanitized change (the sanitized document and its references)
between extraction and the draft transaction, for example while an operator
reviews an import and marks further paths (§3.6). A further mark re-enters the
pipeline at §2.3 step 3 on the staged document.

### 3.1 Two modes

| Mode | Where the change lives | Recovery owner | Use |
| --- | --- | --- | --- |
| Protected transient | the ingesting process's memory only; the claim row carries no payload | none | default, for every ingestion that does not pause across the lifetime of its process |
| Encrypted staging | the claim row carries the change as one envelope (document, references and any baseline ciphertext with its two digests, §2.3) encrypted under a staging key | a second ingestion principal (§3.4) | only an ingestion whose review must survive its process |

The difference is a recovery owner, not secrecy: both keep plaintext out of
ordinary persistence (E1 §6). Transient staging has no recovery owner by
construction; the only correct answer to its interruption is to abandon the
claim and ingest again. Encrypted staging recovers through a second principal
at the price of a total provider dependency at recovery: with the provider
unreachable, E1's recovery failed and its claim stayed `held` (E1 4.2, §6, §8
item 3). Under this contract the takeover's conditional `UPDATE` precedes the
decryption, so the same failure leaves the claim `resumed` under the taker
(§3.4). Transient staging by default and encrypted staging only where a review
must survive its process follow E1's recommendation **(choice §16.5)**.

The staging key is separate from the artifact key **(choice §16.5)**, and only
ingestion identities can decrypt it, so the executor, which can decrypt
artifacts, cannot read staged changes. A resume decrypts the envelope, refuses
a payload that is not a complete envelope, checks its digest (the SHA-256 of
the envelope's plaintext, stored beside the payload and cleared with it), and only then
constructs the sanitized value through the ingestion package, as E1's resume
path did (E1 §3).
It cannot re-run the guard: the extracted values are in the provider, and the
ingestion identity cannot read them. E1 found a recovery that silently dropped
the references because only the document was staged (E1 5.16).

### 3.2 Claim states and timers

A claim row exists for every ingestion in either mode, created at §2.3 step 0,
with a state, an owner, an owner generation and two server-clock times: a
**lease** that only its owner can extend, and an **absolute expiry** fixed when
the claim is created and never extended **(choice §16.6)**. Both are evaluated
by the database clock, not the caller's (E1 5.15). E1's claims had a lease and
server-side expiry only; the absolute expiry is added here so that repeated
takeovers cannot keep a staged change alive indefinitely.

The lease length, the absolute expiry and the heartbeat interval are **open**:
no evidence bounds them. The contract requires only that the heartbeat interval
is shorter than the lease and the lease shorter than the absolute expiry.

| State | Meaning |
| --- | --- |
| `held` | Owned by the ingestion that created it, or by the run an operator's mark or continuation started on a paused claim (§3.6). |
| `paused` | Staged and awaiting the operator's review: no process owns it, and it holds its payload (encrypted staging with review requested only, §3.6). |
| `resumed` | Taken over by another ingestion principal (encrypted staging only). |
| `released` | The draft transaction committed. The payload is cleared to `NULL`; the row stays (E1 §6). |
| `abandoned` | Terminal without a draft: a refused input, absolute expiry, a lapsed transient claim, a takeover with nothing to decrypt, an operator's abandonment or recovery-mode entry (§3.5). The payload is cleared. |

Owner identity: for transient staging, the run identity, process id and
process start token, as in E1; for encrypted staging, the ingestion principal
and its instance. In both modes the owner string is
`<instance>/<process id>/<start token>`, its instance named by the deployment's
configuration. An owner's own transition (lease extension, storing the
payload, the pause, the draft transaction's release) checks owner and owner
generation, and requires the state `held` or `resumed`, and a takeover, a mark
or a continuation (§3.6) checks the generation it read, each in the same
conditional `UPDATE` that makes the change. That is the shape DB §4.4 measured: a check followed by a separate
write recorded a stale attempt in its control row 018
([DB §4.4](../design/research/20260924-database-semantics.md#44-s4-ownership-transitions)).

### 3.3 Lease extension (E1 decision 3)

Only the current owner at the current owner generation may extend a lease, and
only while the lease and the absolute expiry are both still in the future
**(choice §16.7)**. A lapsed lease is never revived by its old owner; after the
lapse only a takeover (§3.4) or abandonment can change the claim, and for a
`paused` claim, whose lease the pause ends, an operator's mark or continuation
instead of a takeover (§3.6). E1's
prototype extended any live `held` claim for any caller and left the question
to this contract (E1 5.16, §7).

### 3.4 Takeover (E1 decision 2)

A claim is taken over only when all of these hold, in one conditional `UPDATE`
that sets the new owner and increments the owner generation **(choice §16.7)**:

- the claim is under encrypted staging and in state `held` or `resumed`;
- its lease has lapsed and its absolute expiry has not;
- it was created in the current recovery epoch (§3.5);
- the request comes from an ingestion principal, on an explicit operator
  recovery request recorded with the operator's identity.

The new state is `resumed`. In E1, `resumed` also marked the original run's own
continuation after review (E1 5.7), and the run killed inside the draft
transaction left its own claim `resumed` with its ciphertext; E1's `Resume`
accepted only `held`, so no one could take that claim over (E1 4.2, §7). Here
a continuation after review (§3.6) leaves the claim `held`, `resumed` means only taken
over, and both states can be taken over, which removes that stranded case.

A claim whose payload was never written (encrypted staging interrupted before
the end of §2.3 step 8) has nothing to decrypt: a takeover of it abandons it,
and the input is ingested again.

A `paused` claim is not taken over: no process owns it, and the operator's mark
or continuation takes it instead (§3.6). A taken-over claim whose review is
still pending pauses once its envelope is decrypted and its digest checked, in
place of the draft transaction; one whose review was continued, or that never
requested one, goes on to the draft transaction.

The generation fence is the same mechanism DB measured for queue claims, where
a worker whose lease expired had its late completion refused at the newer fence
(DB §4.5 row 021,
[DB §4.5](../design/research/20260924-database-semantics.md#45-s5-queue-claims)).
Its effects:

- the old owner's heartbeat, draft transaction and release all fail, because
  each is conditional on its own generation;
- the draft transaction writes the draft, its reference rows and the claim's
  `released` state in one transaction, so a crash inside it leaves the claim
  unreleased with its payload intact and nothing of its draft write committed, which is E1's
  `crashed-in-db-txn` result (E1 4.2), and a successful commit leaves nothing
  to redo;
- a takeover whose decryption fails, for example with the provider unreachable,
  leaves the taker owning the claim until its lease lapses; the claim can be
  taken over again until its absolute expiry and is then abandoned;
- a decrypted envelope that does not match the claim's stored digest, or does
  not decode, is an integrity failure: the taker abandons the claim, its
  operation fails `500 internal-error`, and the draft transaction writes
  nothing.

The claim row's generation fence coordinates ingestion workers only. It does
not stop a stale process from doing work outside the database; it stops that
work from being committed.

### 3.5 Expiry, abandonment and restoration

At absolute expiry, or when a transient claim's lease lapses, the claim becomes
`abandoned`, its payload is cleared, and the input must be ingested again. A
`paused` claim is abandoned at its absolute expiry like any other: a review
never extends it (§3.6). An abandoned run's provider generations remain as
unused objects (§2.3).

Abandonment is evaluated at read time and written by a sweep
**(choice §16.8)**: every transition and every read treats a claim past its
absolute expiry, or a transient claim past its lease, as `abandoned`, whether
or not the row says so yet, except the orphan report, which goes by the
recorded state ([persistence and API §6.4](persistence-api.md#64-orphans));
an ingestion-identity sweep then writes the state
and clears the payload, at startup and periodically. A late sweep therefore
delays only the clearing of ciphertext, never a refusal. A process started
with the recovery-start flag runs no sweep before its entry, since it records
nothing before then ([persistence and API §12.2](persistence-api.md#122-entry)),
and the entry abandons every claim of an earlier epoch itself; it sweeps once
its entry has committed. The sweep interval is open.

Every comparison with a claim's lease or absolute expiry, a new claim's lease
and expiry, and a heartbeat's new lease, use the database's current time,
`clock_timestamp()`, not the
transaction's start `now()`
([persistence and API §5](persistence-api.md#5-transaction-boundaries) rule 4).
An owner transition takes the claim's row lock before it evaluates its
conditional `UPDATE`: PostgreSQL evaluates an `UPDATE`'s predicate before it
waits for a row and does not evaluate it again when the holder ends without
changing the row, so a deadline that passed during that wait would go unseen.
A heartbeat or release that waited for the claim until past its lease or
expiry is therefore refused, and a heartbeat extends the lease from the time it
holds the claim. A transaction that began before a lapse reads the claim
`abandoned` once the lapse has passed.

A draft transaction (T1) is not checked again at `COMMIT` **(choice §16.30)**.
Its release passes the owner check while it holds the claim's row lock, and
holds that lock to its end: no sweep, takeover or abandonment can act on the
claim meanwhile, so the release is ordered at the instant it was checked, when
the claim was live. A T1 still running when the expiry passes commits a claim
that was released in time. If it rolls back instead, the claim is due and
abandoned as any other. Until it ends, a read that does not wait for the lock
already reports the claim `abandoned`; the commit then shows it `released`.

A restore rewinds claim rows and fence generations with the rest of the
database (DB §4.7). A claim created before the current recovery epoch
([execution and recovery §7](execution-recovery.md#7-recovery-after-management-state-restoration))
is therefore never resumed or committed: recovery-mode entry abandons it, and
its input is ingested again **(choice §16.9)**.

### 3.6 Operator review

An import or drift adoption under encrypted staging may request review when it
starts ([persistence and API §9.3](persistence-api.md#93-examples));
the claim records the review as `pending`. Transient staging cannot request
one: its change lives only in its process's memory. Review lets an operator
read the sanitized change before anything of it reaches ordinary persistence,
and mark a value the schema list missed (§2.4) **(choice §16.35)**.

1. **Pause.** Once its envelope is ready (the end of §2.3 step 8), a run whose
   claim's review is `pending` does not start the draft transaction. Its owner
   transition stores the envelope, sets the claim `paused` and ends its lease
   at the current time, in one transaction, and the run stops. A taken-over
   claim whose review is `pending` pauses in the same way once its envelope is
   decrypted and checked (§3.4), its stored envelope unchanged. No process owns
   a `paused` claim and none extends its lease; its owner transitions (§3.2)
   all fail, including the pausing run's own late heartbeat.
2. **Show.** The operator reads the staged change of a `paused` claim. The
   serving process decrypts the envelope with its ingestion identity, checks
   its digest and answers the sanitized document and its declarations, never
   the baseline ciphertext, a generation path or the digest. Nothing of the
   answer is stored or logged. A decryption failure or an integrity failure
   (§3.1) refuses the read and leaves the claim unchanged.
3. **Mark.** An operator's mark takes the claim from `paused` in one conditional
   `UPDATE`, which requires the state `paused`, the owner generation it read, an
   absolute expiry still in the future and a claim created in the current
   recovery epoch (§3.5): the serving instance becomes its owner at the next
   owner generation with a new lease, and the state is `held`. The mark is an
   owner transition: it takes the claim's row lock before that `UPDATE`
   (§3.5), so an absolute expiry that passes while it waits for the lock
   refuses it. Its run decrypts
   the envelope, checks its digest and re-enters §2.3 at step 3 on the staged
   sanitized document with the new marks. A mark whose target holds an
   existing reference is refused (`mark-kind`): a mapping with a `!bwref`
   member, which has no kind (§5.2, choice §16.16), and the string of an
   identified embedded document (§5.4) holding a `!bwref` anywhere inside it.
   The reference's earlier value is in the provider, which this run cannot
   read, so it could be neither extracted nor kept. A mark on a `!bwref` node
   itself is excluded as step 3 states. A mark on `machine.features.stableHostname`
   or a mapping that holds it refuses where step 2a removed a legacy hostname key
   (step 2a).
   Step 5's guard searches for the
   values this mark extracts. The earlier values are in the provider, which the
   ingestion identity cannot read, so the guard cannot search for them; they
   were guarded when they were extracted, and that result holds only if the
   mark adds no text but its references. The re-substituted document must
   therefore equal the staged one except at the nodes this mark replaces: every
   other scalar, an embedded document's text included, is unchanged byte for
   byte. A mark that would change other text, as re-encoding an embedded
   document in another format does, is refused (`mark-rewrites-text`); the
   remedy is to abandon and ingest again with the mark at the start, where the
   run holds every value it guards. Step 6 creates the new generations under
   the same claim. Step 8 does not run again: the envelope's baseline is already the exact input. The new envelope
   (the re-substituted document, every reference and the baseline ciphertext
   with its two digests, carried over unchanged) replaces the payload and its digest in the transaction that pauses the claim
   again (item 1). The marks are held by the serving process only, never
   stored.
4. **A refused mark.** A mark refused before any provider write (a mark that
   addresses no node, a guard hit, a node the machinery cannot load as a null,
   §2.3 step 3, a node holding a reference or text the mark would rewrite,
   item 3) returns the claim to
   `paused` with its earlier envelope and digest unchanged, and the refusal is
   recorded on the ingestion's operation; the review goes on. The refusal names
   the rule, and the mark by its position in the request, and no path at all:
   an operator's path, or a staged document's path whose tokens together
   spell it (a guard hit's), can hold an earlier extracted value, which the
   redaction of refusals (§8.3) cannot recognise once the run no longer holds
   it. Several marks at fault name the first in the request. A document that
   does not load with its nodes stored as nulls names a mark whose node,
   stored after those identified before it, stops it loading while those
   before it load: found by bisection, in a number of machinery loads
   logarithmic in that document's nodes rather than one per mark. A node
   under another marked node is stored with it and its mark is never named.
   Where storing more nodes lets the document load again, the mark named is
   such a mark, not necessarily the first. A refusal of the stream after
   substitution (a reference at a document's root, or used as a key through
   an alias) arises from every mark whose reference stands out of place; with
   none, a refusal of an identified embedded document arises from every mark
   that took its holder out, marking it, an alias of it or a node above it. A
   provider failure on the mark's first generation, before any
   generation was created, is refused the same way: the claim returns to
   `paused` with its earlier envelope, and a generation the failed request may
   have created is referenced by no row and reported as an orphan once the
   claim ends. A mark refused
   after a provider write (§2.3 step 6 failing part-way) abandons the claim
   with that refusal, and the generations it created are unused (§2.3).
5. **Continue.** The operator's continuation takes the claim as a mark does and
   records the review as `continued`, with the principal that continued it
   (`continued_by`, in the same `UPDATE`; a mark records none). The record
   shows who continued the review, never who read it, and an import base's
   text is read by any `author` when it was read from a node, and otherwise only
   by the author who imported it from a file and continued
   it ([persistence and API §3.2](persistence-api.md#32-the-import-base), choice
   §17.41 there). Its run decrypts the envelope, checks its
   digest and runs the draft transaction, which releases the claim (§3.4). A
   draft that moved while the claim was paused fails the draft transaction as
   for any ingestion, and the claim is abandoned.
6. **Interruption.** A mark's or a continuation's run that stops, or that
   cannot decrypt the envelope (recorded as for a takeover's, persistence and
   API §8.3), leaves the claim `held` to its lease, with the envelope stored
   before it. An integrity failure in that run (§3.1) abandons the claim and
   fails its operation as a takeover's does (§3.4). A mark's new
   envelope is stored only as it pauses. A takeover (§3.4) then resumes that
   envelope: a review still `pending` pauses again, without the interrupted
   mark, and a `continued` one runs the draft transaction. Generations an
   interrupted mark created are referenced by no row and are reported as
   orphans once the claim ends ([persistence and API §6.4](persistence-api.md#64-orphans)).
7. **End.** A `paused` claim ends by continuation and release, by the
   operator's abandonment, at its absolute expiry, which no review extends, or
   at recovery-mode entry if it is of an earlier epoch (§3.5). A process restart
   changes nothing: the claim and its envelope are in the database, and any
   ingestion instance of the current epoch serves the next request.

No step puts plaintext into the database, a log or an error: the envelope stays
encrypted in the claim, marks live only in the serving process, a refusal names its rule and the mark's position, and
the show's answer leaves the server only in the response to the requesting
operator. That answer can still hold a value no one has marked yet, which is
why it is shown before the draft transaction and only to the roles that may
mark ([persistence and API §9.2](persistence-api.md#92-resources-and-routes)).

## 4. Correlation digests and the extraction guard

Design: [§7.1](../design/Talos_Configuration_and_Machine_Management_Design.md#71-responsibility-split-and-secret-ingress).

### 4.1 Keyed digests (E1 decision 1)

Every persisted correlator of a secret value (journal records, reference rows,
the baseline's per-value records) is HMAC-SHA-256 under a key held by the
provider, not by the database or its backups **(choice §16.10)**. The record
names the key's identity and version. An unkeyed digest of a secret value is
never persisted.

E1 persisted unsalted SHA-256 digests. For long random Talos key material that
is not a practical guessing target; for an operator-marked low-entropy value it
is an offline guessing oracle held in exactly the database and backups that
§7.1 protects. A keyed digest keeps cross-run correlation and removes the
oracle; a per-run salt also removes it but breaks the cross-run comparison the
baseline check relies on
([E1 §7](../design/research/20260922-secret-ingress-extraction-before-persistence.md#7-limits),
§8 item 7). The HMAC is OpenBao Transit `hmac` with `sha2-256`, under a
dedicated digest key that only ingestion identities may use **(choice
§16.27)**. The record names that key and the key version Transit reports, as
`transit/<key>@v<N>`, at most 256 bytes; configuration refuses a key name over
227 bytes, so the reference fits at any version of up to 19 digits and no
provider write precedes a record that could not be stored. How digests are
compared across a rotation of the key is undesigned, and the PoC never rotates
it (§15).

This covers digests of individual secret values. Whole-configuration digests
used to compare observed and applied state
([execution and recovery §3.2](execution-recovery.md#32-the-commitment-transaction))
are not changed by this contract; their guessability was not assessed. Two of
them are recorded here, unkeyed: the baseline's configuration digest (§2.3
step 8) and each artifact's plaintext configuration digest (§11)
**(choice §16.26)**. Execution and recovery needs both, and no identity that
plans, dispatches or observes can compute them.

### 4.2 The guard (E1 decision 4)

Before a sanitized value is constructed, the candidate document is parsed back
and checked twice against every value extracted in this run. An extracted
mapping contributes its keys as well as its members, since the provider stores
both:

1. **Value comparison.** Every scalar in every document, mapping keys and
   unaddressable subtrees included, is compared as a parsed value. This catches
   copies the encoder rewrote, such as a multi-line block or an escaped string
   ([E1 5.18](../design/research/20260922-secret-ingress-extraction-before-persistence.md#518-the-fourth-review-and-the-fixes-made-after-the-evidence),
   and 5.19 for the unaddressable keys).
2. **Substring search.** Every scalar is searched for every extracted value as a
   substring. This catches a secret embedded in a larger value, such as a token
   inside a URL or a join command, which value comparison cannot see. A
   mapping's key is searched for as a whole token: an occurrence counts only
   where neither neighbouring byte is an ASCII letter or digit. Key names are
   common words, and contained in a longer one they are no copy: a marked
   registry `auth` mapping's key `username` sits inside the `usernames` key every
   Talos base holds. §6 step 7 matches keys the same way.

The substring search skips the scalar content of this run's `!bwref` nodes,
which is a reference name, as E1's substring search skipped the references it
substituted (E1 5.19). The value comparison does not skip them.

v1 keeps both **(choice §16.11)**. A false refusal leaks nothing; a false pass
persists plaintext (E1 5.19). The refusal names the matching paths and the rule, never the value.
The remedy is to mark the containing scalar, which is then referenced whole, as
design §6.9 requires for arbitrary text. The accepted cost: a very short marked
value can match unrelated scalars and refuse the input until the mark is
removed or the containing scalars are marked.

What the guard cannot see: a copy that was transformed (re-encoded, split,
hashed or partial). Leak scanning in the evidence finds exact copies only
(E1 §7; FR §2), and this guard is exact in the same sense.

## 5. Reference grammar and declaration

Design: [§6.9](../design/Talos_Configuration_and_Machine_Management_Design.md#69-secret-references-and-validation-stages),
[§6.3](../design/Talos_Configuration_and_Machine_Management_Design.md#63-yaml-anchors).

### 5.1 Syntax

A reference is the explicit YAML local tag `!bwref` on a node whose scalar
content is a logical secret name:

```yaml
machine:
  registries:
    config:
      registry.example.test:
        auth:
          username: bronzeward
          password: !bwref registry/example-pass
```

Name grammar: one or more segments joined by `/`; a segment is one or more
lowercase ASCII letters, digits and hyphens, and begins and ends with a letter
or digit. The grammar fixes only the lexical form. How a logical name maps to a
provider path is provider layout (design §7.3), open beyond the PoC constraints
of §2.3 step 6.

**Scope, naming and versions** (design §6.9: a reference names "a scoped
logical secret" and resolves "to an exact version"):

- **Scope.** Every logical secret belongs to one cluster, recorded when it is
  created; the scope is metadata of the secret, not part of the name's lexical
  form. A machine's compilation refuses a reference whose secret belongs to
  another cluster. Library-wide secrets shared across clusters are not provided
  in the PoC **(choice §16.12)**.
- **Naming.** Ingestion mints a new name for every value it extracts, including
  a value it extracted before, at re-adoption or in a later draft update. A
  minted name is unique within its scope, never reused for another secret, and
  contains no part of the value and no digest of it. The exact scheme is open
  beyond those constraints. The cost is churn: re-adopting an unchanged
  configuration yields new names, so diffs and dependency records change for
  secrets that did not. Deduplicating by keyed digest (§4.1) is the
  alternative, deferred while comparing digests across a rotation of the
  digest key is undesigned **(choice §16.13)**.
- **Versions.** The declaration (§5.2) names the exact version. The compiler
  never selects a latest version; a different version is a new fragment
  revision, reviewed as a source change **(choice §16.14)**. In the PoC every
  secret ingestion creates is at version 1 (§2.3 step 4); a later version is
  created only by rotation tooling (design §13.2), which this contract does not
  specify.

The tag is chosen over the opted-in marked string and the external path binding
**(choice §16.15)**.
All three reach native parity when resolved early, so composition parity does
not decide between them ([SR §9](../design/research/20260924-structural-reference-composition.md#9-alternatives-and-decision-enabled));
the renderer implications do
([SR §6.4](../design/research/20260924-structural-reference-composition.md#64-criterion-4-candidate-failures-and-renderer-implications)):

- The tag needs no per-fragment opt-in, and a quoted look-alike such as
  `"!bwref reg-user"` is a plain string to YAML and stayed literal in every
  order. The marked string's collision defence exists only under early
  resolution and depends on an opt-in declaration (SR §5, §6.4).
- The binding needs bindings kept in step with fragment edits, including list
  selectors; it cannot hold a list-element reference
  ([SP §7.1](../design/research/20260925-sensitivity-provenance.md#71-the-binding-form-cannot-hold-a-list-element-reference));
  its refill adds a key at the end of its mapping, so SR expects a binding to
  an embedded-YAML key that is not last to change the document's bytes, a case
  it did not run (SR §6.4, §8); and a
  document-and-path address points elsewhere after the v1.14 layout change
  ([E3 §6.2](../design/research/20260925-talos-compatibility.md#62-criterion-2-subprocess-against-machinery-and-the-structural-reference-evidence)).
- The tag's costs are a tag-preserving parser before any typed decode (§6) and
  the fact that an unresolved tag inside unidentified embedded text passes
  validation (SR §6.4). §5.5 refuses that case.

The provenance evidence (SP) was produced with the tag form resolved early, so
this choice keeps SP's results applicable (SP §3, §8).

### 5.2 Declarations

Each fragment revision carries, beside its native YAML and outside it, a
declaration for every name it references:

```yaml
references:
  registry/example-pass: {kind: string, version: 1}
  pki/extra-ca: {kind: string, version: 1, encoding: base64}
  app/db-pass: {kind: string, version: 1}
embedded:
  - {path: "doc[0]/cluster/inlineManifests/0/contents", format: yaml}
```

- `kind` is one of `string`, `integer`, `boolean` or `mapping` (a mapping of
  those scalar kinds) **(choice §16.16)**. It must equal the kind stored in the
  pinned secret version (its `kind`, **choice §16.28**); a mismatch refuses
  compilation and nothing is coerced.
- `version` is the exact version the reference resolves to (§5.1).
- `encoding` is optional and comes from a closed enum **(choice §16.17)**. The
  PoC enum has one member, `base64`, which places the standard base64 encoding of a `string`
  secret's UTF-8 bytes. It is the only modifier the evidence defined (SP's
  prototype refuses any other). A value already stored in its placed form, as
  in SR's bytes case, needs no modifier. One name has one declaration per
  fragment revision, so a fragment cannot use one secret both raw and
  encoded.
- `embedded` identifies embedded documents (§5.4); `format` is `yaml` or `json`.

The declaration is what design §6.9's "literal text must remain literal unless
explicitly declared as a reference" refers to: an undeclared tag name, an
unused declaration, or an identification of a path that holds no string is an
authoring error.

### 5.3 Where a reference may stand

- On a scalar or mapping node that is a mapping value or a list element, in any
  document of the stream, or inside an identified embedded document. SR ran
  integer, boolean, base64-bytes, string and whole-mapping targets, a value
  inside a list element's mapping (`machine.acceptedCAs[0].crt`) and a second
  document (SR §3.1, §4); a reference that is itself a list element is
  evidenced by SP's list-append case, which landed at
  `cluster/extraManifests[1]` (SP §3.1, §4.2).
- Not on a mapping key, and not on a sequence as a whole: a list as a reference
  target was not run (SR §8), so the PoC refuses it.
- A target that is a member of a marked mapping, reached only through that
  mapping, is extracted as part of it: one reference, not a reference inside a
  reference. Marking a registry's whole `auth` mapping, whose password the
  schema also identifies, is the case. A member that aliases a target, or a
  target also reached outside the mapping, refuses the input. So does a mapping
  target whose key or member carries an anchor: the reference drops it, and an
  alias of it would then name an earlier anchor of the same name, or none.
- References replace complete parsed values. There is no interpolation, and
  arbitrary text is referenced whole (design §6.9).
- No local tag other than `!bwref` is accepted in a fragment: its behaviour
  under typed decoding is unknown.

### 5.4 Aliases and embedded documents

**Aliases.** An anchor may be placed on a tagged node and aliased elsewhere in
the same document (design §6.3). Resolution replaces the tagged node, so every
alias of it yields the same value; the reference is recorded once per resolved
path, which is how SR and SP attributed one reference to two output paths
(SR §6.2; [SP §4.2](../design/research/20260925-sensitivity-provenance.md#42-provenance-per-case)).

**Identified embedded documents.** An embedded document named in `embedded` is
parsed in its declared format, its references resolved, and the whole document
re-serialized: JSON compact with sorted keys, YAML with two-space indentation
through the pinned encoder. The author's formatting, comments and quoting are
not preserved, and an identified JSON document is authored as YAML, because a
tagged value is not JSON (SR §6.4). These are SR's prototype rules, which SR
calls the experiment's and not a design (SR §8); they are adopted because they
are the only rules parity was shown for **(choice §16.18)**. Parity was shown
only for documents whose literal form was derived through the same encoders,
and for embedded YAML only with the reference at the last key of its mapping
(SR §6.4, §8).

**Unidentified embedded text** is opaque: nothing resolves inside it, and a
secret in it must be marked as the whole scalar (§4.2).

### 5.5 Reserved text

A string scalar that contains the text `!bwref` is refused, at authoring (§7
stage 1) and again in the composed output (§6 step 7). A tag inside
unidentified embedded text is such a string, and SR shows it would otherwise be
left in the output with validation passing (SR §6.4). The cost is that a literal
look-alike, which YAML would keep literal, is refused too **(choice §16.19)**.

## 6. Resolution order and composition

Design: [§6.9](../design/Talos_Configuration_and_Machine_Management_Design.md#69-secret-references-and-validation-stages),
[§6.2](../design/Talos_Configuration_and_Machine_Management_Design.md#62-fragments-profiles-and-assignments),
[§7.4](../design/Talos_Configuration_and_Machine_Management_Design.md#74-publication-without-distributed-transactions)
steps 1–2.

References are resolved **early**: each fragment on its own, before native
composition. Late resolution (compose, then resolve) failed for every candidate
on at least one required case: a tag on a string field is dropped silently and
its name becomes the value, typed decoding rejects a placeholder on an integer,
boolean, bytes or struct field, and a binding applied after composition cannot
see which fragment wrote last (SR §5, §6.4). Early resolution reached
byte-for-byte native parity for the tag on all 13 cases where parity was
expected, matched the native rejection on type-mismatch, and failed as designed
on unidentified embedded text: 210 rows, 0 differing from expectation
([SR §4](../design/research/20260924-structural-reference-composition.md#4-the-matrix)).
This departs from design §6.9's stated preference for late resolution, which SR
rules out on this evidence.

**The first composition input is the machine's import base.** The PoC excludes
new-machine enrolment and cluster bootstrap
([design §18.2](../design/Talos_Configuration_and_Machine_Management_Design.md#182-phase-1---configuration-control-on-an-existing-cluster)),
so the compiler never generates a base configuration and never assembles a
secrets bundle. The import base is the sanitized document ingested for the
machine at import, or at its latest accepted drift adoption: a source revision
of its own, not a fragment in any layer, holding a reference for every value
ingestion extracted, the Talos bundle's included. Every selected fragment is a
patch onto it, as SR's and SP's fragments were patches onto their bases.

This departs from design §6.7, which gives cluster identity and secrets
"dedicated cluster and secret-generation resources; injected by the compiler
and protected from ordinary fragments", and from the `secretGeneration` field
of the illustrative release record in design A.3. For the PoC, the import base's
references stand in for those resources, a release records its import base
revision in their place, and they are protected by refusal: publication is
refused when an ordinary fragment overrides or deletes a value the import base
holds as a reference, which the prefix compositions of §8.1 detect
**(choice §16.20)**. A later phase that generates configurations must specify
the dedicated resources and how a bundle is assembled from them.

For each machine:

1. **Snapshot** the import base, the source and assignment revisions, and
   order the fragments by the fixed layers of design §6.2 and their explicit
   order within a layer: the profiles' pins first, by the assignment's
   profile position and then the pin's, then the assignment's own fragments
   of the layer by position. A fragment revision selected more than once, as
   two profiles may pin it, is composed once, at its first position
   **(choice §16.31)**. Which machines a release covers, and what the
   snapshot reads, are
   [persistence-api §6.1](persistence-api.md#61-steps)'s.
2. **Pin** every declared reference in the import base and in every selected
   fragment, including references a later fragment will override: record the
   declared version (§5.1) together with the provider object it names. This is
   the source superset; resolving early necessarily reads all of it (SR §6.2,
   §6.4).
3. **Check dependencies**: every pinned secret version must classify `retained`
   (design §7.6), classified afresh by the
   [dependency monitor's procedure](dependency-monitor.md#3-classification-procedure),
   never read from its stored status, and the compiler identity must read it at
   the point of use. `blocked`, `lost` or `unknown` refuses publication. A KV
   version's classification also takes its `created_time` from the metadata
   answer, the identity §9 records; one other than the identity first
   recorded for that version refuses
   ([persistence-api §6.1](persistence-api.md#61-steps)).
4. **Resolve** the import base and each fragment with a tag-preserving parser,
   replacing each tagged node by its typed value (with its encoding) before any
   typed decode, and each identified embedded document as in §5.4. Every tag
   must resolve; there is no partial result. A KV value read whose
   `created_time` differs from the one step 3 took refuses publication: the
   path was deleted and written again between the two reads.
5. **Compose** the reference-free fragments onto the resolved import base with
   the selected renderer (§10) in the order of step 1, using native
   strategic-merge semantics: last writer wins, and `$patch: delete` removes.
   The compiler performs no merge of its own (design §6.9). JSON6902 patches
   are refused: they were not tested and are refused for the v1.13
   multi-document configuration (SR §8; not rerun on v1.14).
5a. **Fill the hostname** (§6.1, **choice §16.36**). For a machine that has a
   name, read the composed configuration's hostname with the machinery's
   accessor. Where none is set and none of the exceptions of §6.1 item 2
   holds, compose one more patch, a `HostnameConfig` with
   `auto: off` and `hostname` set to the name, as the last input. The decision
   is taken once, from this composition, and every trace and flip pass of §8.1
   composes the same patch. Before it, the composition is read again with each
   boolean reference flipped in turn, a boolean member of a `mapping`
   reference included: where a flip changes whether `machine.features.stableHostname`
   is set, its value depends on a secret, and it counts as set whatever the
   resolved value is. The compiler does not know which leaf a reference holds
   until step 6, so only the flip tells it. The patch is a source that holds no
   reference, so no record of §8.2 names it, and the compile that composes it
   takes no decision of its own: the decision above stands. A reference at
   `auto` is refused at step 6 already, and `hostname` is the document's only
   other value field, so no occurrence can sit in a document the patch replaces.
6. **Trace** the composition for provenance (§8.1).
7. **Check the output**: no string scalar holds reserved text (§5.5), and no
   resolved `string` value's stored or placed form of six bytes or more (each
   form measured by its own length, so a short value's longer base64 placed
   form counts) appears as an exact copy at an output leaf that provenance does not attribute
   to its reference; and no ordinary fragment overrode a reference of the
   import base (choice §16.20). A copy is refused **(choice §16.21)** and
   reported by path only, as a source-side exposure: the value was already
   written in plaintext into a fragment, and its remedy is to mark that literal, which extracts it to a new
   generation; whether to rotate the secret is the operator's decision. SP's
   duplicate-literal case is the evidence that only value matching sees such a
   copy ([SP §6.3](../design/research/20260925-sensitivity-provenance.md#63-criterion-3-remaining-leakage-risks-and-dependency-records));
   the six-byte floor is SP's value matcher's (SP §2). A copy is a value
   contained in a leaf, not only one equal to it, so a literal that embeds the
   value is refused too. A `mapping` value's keys are values (§4.2), so each
   key of six bytes or more is matched too, whatever its member's kind, but
   as a whole token: an occurrence counts only where neither neighbouring
   byte is an ASCII letter or digit. Key names are common words, and contained
   in a longer one they are no copy: SP's map case places a key `username`,
   and every Talos base holds `usernames`. A stored form that is valid standard base64 is also matched
   in its canonical re-encoding, which the machinery writes for a byte field
   (§8.1); the floor applies to that re-encoding's own length, since decoding
   skips line breaks. Every output leaf is checked, including the leaves of
   an identified embedded document that holds no reference, and so is every
   output mapping key, reported at the path of the value it names, except a
   key a `mapping` reference placed beside its own attributed member. A leaf that
   provenance attributes to any reference is
   never a copy: a reference replaces a whole node, so another reference's
   value inside it is an overlap of two values, not a literal in a source. An
   override refusal names the first fragment in composition order that
   overrode an import base reference, and only the base paths that fragment
   overrode. The
   check runs before validation, so a fragment that both overrides an import
   base reference and invalidates the configuration is refused as an override.
8. **Validate** the complete materialized configuration with the pinned renderer
   in the node's platform mode, the one its machine records (persistence-api
   §3, choice §17.32 there) (§7 stage 3). Validation runs in the target's mode with
   the local checks and is not strict. A warning of the machinery, such as a
   deprecation the node still accepts, does not refuse a publication.

**The form-only verdict (choice §16.39).** For each machine, publication
records whether the composed configuration and the resolved import base hold
the same documents in the same order, with every key and every value equal,
secrets included, so that they differ in comments, key order, indentation,
quoting or flow style only. It is computed on the plaintext inside the
compilation and is never shown with a value. It is false for anything it
cannot decide: an alias, an anchor, a key that is not a string, a repeated key, a tag
outside YAML's core schema, a parse error, and an import base with an
identified embedded document, whose string the resolver writes back in its own
encoding (§5.4). It proves that the artifact equals the resolved base, and not
that the node runs it: the pinned secret values are the provider's version, and
the baseline cannot be decrypted by any identity (§2.3), so no comparison with
the node's own bytes is offered. One document is set aside: where the import
removed a `HostnameConfig` that was exactly the fill's own (§2.3 step 2a: `apiVersion`, `kind`,
`auto: off` and the hostname, nothing else), the decided hostname source is the machine's name and
the name is the one recorded at import, the composition's own fill is set aside and every
other document is compared as above, so the machine reads true at its first apply. After a rename
the verdict is false and what differs is the hostname; a removed document that held more than the fill writes,
or any document the composition changes besides it, keeps the verdict false. So does a removed legacy
`machine.network.hostname` (§2.3 step 2a): it is recorded as not the fill's own, because the key becomes a document
and equal behaviour across that move is unmeasured, so no fill is set aside for it and its machine reads false at the first apply.
A base that omits a
key the composition writes with an empty value (a configuration not written by
`talosctl` can be in that case) also reads false: the release then holds a key the base lacks, and the first apply's diff is withheld. The comparison does not treat an empty
default as absent; that would change what the verdict means. The compilation's
own transformations, the digest and the artifact are unchanged.

The compiler's only transformations of native YAML are step 4's replacement of
tagged nodes and re-serialization of identified embedded documents, and the
one hostname patch of step 5a. Everything
else about the output is the renderer's, which is what "preserving native merge
semantics" means here. Overrides, aliases, list changes and multi-document
streams are the renderer's behaviour, observed by §8.1, never modelled.

### 6.1 The hostname, filled from the machine's name

Design: none. Owner direction, 2026-11-20 (ginsys/bronzeward#951; Serge, typed
11:36 CET in the bronzeward lead's session): "when setting the hostname, the
default could be the machine name (if set)". This section specifies it
(ginsys/bronzeward#976). A machine may carry a stored name, unique within its
cluster, a hostname of one or more dot-separated labels of lowercase letters,
digits and hyphen, so always a valid hostname, a full one where the machine has
a domain ([persistence and API §3.5](persistence-api.md)). Until
this section the compiler wrote no hostname, and the name and the hostname were
independent.

**What Talos does** (read from the pinned machinery, v1.14.5,
`pkg/machinery/config/types/network/hostname.go`, and the Talos 1.14 page
"Hostname"). A `HostnameConfig` document has two fields, `auto` (`stable` or
`off`) and `hostname` (lines 69 and 80). `hostname` "has the highest priority
over any other hostname sources: DHCP, cloud-init", and `auto` is the lowest.
`hostname` beside an `auto` other than `off` fails validation (line 125), and
a document with neither fails too (line 121). The document refuses to coexist
with a v1alpha1 `machine.network.hostname`, or with the v1alpha1
`machine.features.stableHostname` feature set (lines 159-167,
`config/types/v1alpha1/v1alpha1_network_bridge.go:39-50`). For the contracts
above v1.11 the machinery generates `auto: stable` in a `HostnameConfig`
(`config/generate/network.go:39-43`, `config/contract.go:231-234`), and its
own patch for a static hostname is `auto: off` with `hostname`
(`config/generate/stdpatches/stdpatches.go:24-31`). The accessor
`NetworkHostnameConfig()` returns the first `HostnameConfig` document, else the
v1alpha1 configuration (`config/container/container.go:544-556`). The
Kubernetes node name is derived from the hostname by default (Talos 1.14,
troubleshooting, "kubectl get nodes reports wrong node name").

**Measured at specification time** with a scratch test on the generated v1.14
base (the compiler issue, ginsys/bronzeward#1040, turns each case into a
test): the base reads hostname empty and `auto: stable`; the patch of choice
§16.36 merges and validates in `metal` and `cloud`; a fragment `hostname` alone
over the generated `auto: stable` fails validation in every mode; a patch
composed after a fragment's hostname, or after an `auto: off` fragment,
overwrites it. So the merge alone never lets an explicit hostname win: the
compiler reads the composition first (step 5a). In `container` mode the
generated base fails validation with and without the patch, so that mode is
unmeasured.

1. **What the compiler writes (choice §16.36).** A `HostnameConfig` document
   with `auto: off` and `hostname` set to the machine's name, composed as one
   patch by the renderer's native merge, in `metal`, `container` and `cloud`
   alike: the document's validation reads no mode (line 115). Only the
   contract the PoC compiles is concerned (§10.2: the node's running minor,
   v1.12 or above for this document); the legacy `machine.network.hostname` is
   never written. Alternatives: `metal` only, which leaves a named container
   or cloud node with a name and no effect and needs a second rule for no
   measured reason; the legacy field, which v1.14 refuses beside the document.
   Cost: `container` and `cloud` behaviour is unmeasured (item 4 of "Not
   shown" at the end of this section).
2. **Which hostname wins (choice §16.36).** An explicit hostname always wins;
   the name fills only an absent one. A hostname is explicit when the
   composition's accessor returns one (choice §16.36). These compositions are absent and get
   the fill: a `HostnameConfig` with `auto: stable` and no hostname, and no
   hostname document at all. These get **no** fill: a `HostnameConfig` with
   `auto: off` and no hostname (the operator chose another source, DHCP or the
   cloud, and the fill would override it); a composition whose v1alpha1 document sets
   `machine.features.stableHostname: true`, because the document refuses to
   coexist with it and the compiler does not edit that field (the composition
   decides, as step 5a does: a base that sets it and a fragment that turns it
   off compose to a configuration without it, and that machine gets the fill;
   a `stableHostname` whose value depends on a secret reference (the flip of
   step 5a) counts as set whatever its value, so that machine gets no fill and, where it has a name, its source is
   `legacy-stable`, and neither the fill nor the source tells the value); and a machine
   with no name. The review says the last two in words (persistence and API
   §9.2). Alternatives: the name always wins, so an operator could never
   differ from it, against "default"; a refusal where both exist, which
   refuses every imported machine that already has a hostname.
3. **A rename needs a release and a plan.** A release compiles with the name
   the machine had at its snapshot and records it (persistence and API §3.5,
   §6.2, choice §17.44); a rename changes no release. A machine whose
   hostname comes from its name takes the new name only from the next release
   and an approved plan. Where the clients offer the rename they say so
   (client contracts A9 item 6).
4. **What a release records.** The hostname's source and, where it came from
   the name, the name used, per release machine (§11, persistence and API
   §9.2). The provenance records of §8.2 stay secret-reference occurrences
   alone: a hostname is not a reference, so no stand-in is placed for it and
   the trace and flip passes carry the same patch as the real composition.
5. **Import, adoption and drift (choice §16.37).** The equality rule of
   §2.3 step 2a: at import (onboarding included) and at drift adoption, a
   `HostnameConfig` whose hostname equals the machine's name is removed from
   the stored base and recorded as filled, and so is a legacy
   `machine.network.hostname` that equals it where no such document is present
   and the stable-hostname guard of step 2a holds (the key alone goes). Why: an onboarded machine takes its
   name from its own hostname
   ([operator experience §5.2](operator-experience.md)), so its import base
   holds that hostname explicitly, and without the rule a rename would never
   reach it; every adoption would likewise freeze a filled hostname as
   explicit. The whole document goes: it holds only `auto` and `hostname`, and
   an `auto: off` left behind would stop the fill (decision 2). The baseline
   stays the exact input, so the adoption comparison
   ([execution and recovery §6.3](execution-recovery.md)) is unchanged. A
   release compiled from the thinner base writes the document again, as
   `auto: off` with `hostname`, so its artifact digest can differ from the
   baseline's and the machine is then pending convergence, which §6.3 already
   covers. The record of step 2a (the name and whether the removed document was
   the fill's own) is stored with the import base revision, and the form-only
   verdict (§6, step 8) sets that one document aside, where it was exactly the
   fill's own, while the name is the recorded one. **A removed legacy key is recorded as not the fill's own, so
   its machine's verdict reads false at the first apply**: the node holds the
   key and the release sends a document, and whether Talos treats that move as
   no change on the node is unmeasured here. **A hostname equal to the
   machine's previous name is not released (choice §16.37, option A rejected
   for now).** After a rename and before the next release is applied, an import,
   onboarding again or a drift adoption (which runs step 2a too) reads a node
   whose document or key still holds the previous name. It differs from the current name, so it stays explicit: the
   release records the source `configuration` (persistence and API §9.2) and
   the rename does not reach the node. The import knows the current name only,
   and cannot tell the fill's earlier write from an operator's hostname that
   happens to equal an old name. The normal path (item 3: rename, release,
   plan) never imports in that window and is not affected. The way out for a
   document is the `$patch: delete` fragment below; **for a legacy key no such
   fragment is measured**. Drift is otherwise ordinary: a hostname changed on the node by hand
   is a configuration edit and a digest mismatch; a hostname from DHCP, cloud
   or `auto: stable` is not in the configuration and is not drift. A machine
   with no name at import keeps its hostname explicit when it is named later;
   deleting that document with a fragment `$patch: delete` (the form of
   [the import-equivalence evidence](../design/research/evidence/20261118-import-equivalence/gen.sh))
   hands it to the fill. A filled hostname needs no such step.
6. **A filled hostname that another machine already holds (choice §16.38).**
   Names are unique within a cluster, so two filled hostnames are never equal,
   but a name with dots can still clash: where the effective `RegisterWithFQDN`
   value is not true, Talos derives the node name from the first label, so the
   names `a.x.org` and `a.y.org` give one node name, `a`. Two clashes follow.
   One is a hostname filled from a name against another machine's filled
   hostname that gives the same Kubernetes node name. The other is a hostname
   filled from a name against another machine's explicit hostname that gives
   the same one. In both, two
   nodes would register one Node object, and the second kubelet would take over
   the first's. The compared value is the node name Talos derives, copied here
   because the Talos package that computes it is internal (`nodename.go`,
   `FromHostname`, v1.14.5): take the hostname, or only its first label when
   the composition's effective `RegisterWithFQDN` value is not true (the
   accessor Talos reads, `K8sNodeConfig().RegisterWithFQDN()`: the
   `KubeNodeConfig` document's field where that document exists, else
   `machine.kubelet.registerWithFQDN`, `container.go`; Talos splits at the
   first dot into hostname and domain, `hostname_spec.go`, `ParseFQDN`);
   lower-case `A` to `Z`; turn each underscore into a hyphen; keep only `a` to
   `z`, `0` to `9`, the hyphen and the dot, ASCII only, and drop every other
   character, Unicode letters and digits included; then trim hyphens and dots
   from both ends. So `node-a.example.org`,
   `NODE_A.example.org` and `_node-a_` clash with a filled `node-a`, and with
   `registerWithFQDN` set `node-a.example.org` does not. A domain that DHCP or
   a cloud supplies is not in the configuration and is not compared. An
   explicit hostname that carries a secret reference is not compared either,
   nor one whose compared node name depends on a secret reference (a
   `registerWithFQDN` value held in one), nor one the review redacts by value or
   whose review is withheld in whole (§8.3): the cases in which the recorded
   `nodeName` is `null`. A clash with one is therefore not
   caught: comparing it would make the refusal an equality test on a secret,
   and an author could confirm a guess by renaming a machine and publishing.
   Publication refuses it, before any release is committed,
   `422 validation-failed` with the rule `hostname`, naming both machines by
   name where they have one and by identifier only where they have none, and
   printing no explicit value, which may be a secret reference: "Machine
   node-a would take the hostname node-a from its name, but the hostname
   configured on machine node-b gives the same Kubernetes node name. Rename
   machine node-a, or change the hostname in the configuration of machine
   node-b." Renaming node-b would not help, since its explicit hostname does
   not follow its name (item 2). Where both hostnames are filled from names, the
   refusal reads: "Machine a.x.org and machine a.y.org would both register the
   Kubernetes node name a. Rename one of them, or set `registerWithFQDN` on
   both." Either rename helps, since both hostnames follow the names. Each
   machine's node name is computed from its own composition, so with
   `registerWithFQDN` true on both the node names are the full names and
   differ, and with it true on one only the compared values are `a.x.org` and
   `a`, which differ too. The check was built with the hostname fill
   (ginsys/bronzeward#1040), whose acceptance cases include `a.x.org` against
   `a.y.org` with `registerWithFQDN` unset (refused) and true on both (accepted).
   A node name that moves between machines is the second clash: a machine whose
   release gives it a node name equal to the node name that another machine's
   last applied release recorded (the release's `nodeName`, persistence and API
   §9.3), and that its own applied release does not already hold, is refused the
   same way, naming both machines, until that machine's
   applied release no longer holds it. The recorded value is the derived node
   name above, so the comparison covers a hostname filled from a name, an
   explicit hostname and the `registerWithFQDN` setting alike; it is not made
   where either `nodeName` is `null` (not compared), which it is where a secret
   reference or a value the review redacts (§8.3, any length) is an input to the
   name, where the review is withheld in whole and where no hostname is set, or absent (not recorded), which it is on a
   release made before the record existed; for such an applied release with
   source `machine-name` the recorded name is compared as it was, so that release
   keeps the protection it had. Each machine has its own plan, so a release that renames machine node-a
   and gives its old name to machine node-b could be applied in the wrong
   order and leave two nodes with one hostname. Once both renames are stored,
   every release carries machine node-b's new name and is refused, so the
   operator first gives machine node-b its earlier name or another one,
   publishes and applies the release for machine node-a, and then renames
   machine node-b again. A temporary name is itself filled as the hostname
   where nothing else sets one. The refusal says so: "Machine node-b would take
   the hostname node-a from its name, but the last release applied to machine
   node-a still gives the Kubernetes node name node-a. Give machine node-b another name for now,
   apply the release for machine node-a, then give machine node-b the name
   node-a." Each machine is named by the name it holds when the release is
   published, and by its identifier where it has none, so in that sentence the
   machine that holds the name now is named by it and the other by its current
   name; only the hostname is the old name. Where the machine that would take the
   node name has an explicit hostname, the refusal reads: "Machine node-b's
   configured hostname gives the Kubernetes node name node-a, but the last
   release applied to machine node-a records that. Give machine node-b
   another hostname for now, apply the release for machine node-a, then set it."
   It names the derived node name, which is never a secret, and no configured
   value. To exchange the names of two machines, give the first a third
   name; if the second already holds the first's old name, give the second its
   earlier name. Publish and apply the release for the first machine. Give the
   second machine the first's old name, publish and apply. Then give the first
   machine its final name, publish and apply. The check is no guarantee. It reads the recorded `nodeName` of each
   machine's applied release (persistence and API §9.3), so it does not cover a
   hostname that depends on a secret reference or a value the review redacts or
   withholds (`nodeName` `null`, not compared), a machine whose configuration sets no hostname (`null` as well:
   the node chooses its own), a release with an explicit hostname made before the
   record existed (absent, not recorded), or an adopted baseline whose record names a
   hostname the node does not run (execution and recovery §9.3). An apply that
   is committed but not finished, and a name that moved after publication, are
   the commitment's: its hostname gate refuses a plan while another machine's
   applied release holds the name (execution and recovery §3.2 comparison 6a,
   choice §10.39).
   The check reads the machines of one release, and a release covers every
   machine of its cluster that has an import base
   ([persistence and API choice §17.33](persistence-api.md)); a machine with no
   import base is not compared, and a later change to that coverage must carry
   this check with it. A node name a machine already holds in its own applied
   release is not a name it takes: two machines that already shared a node name
   are not new, and are not refused here, so the check refuses only a machine
   that would take a name it does not hold now. Alternative: a review warning only, which leaves the failure on the
   live cluster to whoever approves the plan.

**Measured on the fixture** (ginsys/bronzeward#1040, 24 November 2026; the
scenario `hostname-measure` of the fixture, which a person can run again).
After a rename, an import from the node keeps the old name as an explicit
hostname, so the rename does not reach the node (the rename row below). The
measured node is the fixture's worker, Talos v1.14.5 running as a container
and named from its own hostname, taken through the product: draft, import,
publish, plan, approval, operation. The fixture starts its nodes with
`talosctl cluster create docker` (`fixtures/bin/up`), so a node is a Docker
container. A container has no reboot (the fixture README, Known limits), and
the uptime that `/proc/uptime` shows inside it is the host kernel's, so this
record holds no restart reading of any kind, in a container, a virtual machine
or hardware. "Node name" below is Talos's `nodename` resource
(`talosctl get nodename`), the name Talos derives from the hostname for the
Kubernetes Node; the Node object itself (`kubectl get nodes`) was not read.
The node's hostname (`talosctl get hostnamestatus`) and node name before and
after each row are in the record that was kept.

| Row | What was applied | Result |
|---|---|---|
| default | the named worker's first release: the document `auto: off` plus its name, the node having an automatic hostname | completed; node name unchanged |
| explicit | the worker imported again from the node, which now holds `auto: off` plus its name (the commonest case): the document removed at import and filled again | completed; node name unchanged |
| rename | the worker renamed to a name with dots, imported again from the node, published and applied | the release records the source `configuration`, not `machine-name`: the node's old document is not equal to the new name, so it stays, and decision 2 lets an explicit hostname win; node name unchanged |
| rename back | the worker renamed back to its first name, imported again, applied | source `machine-name`; node name unchanged |
| stored | the worker renamed to `hm-stored.fixture.test`, then a release made from its stored import base: its assignment in a new draft selects nothing, nothing is imported, published, planned, approved and applied | source `machine-name` with the new name; the node's hostname and node name read `bw-fixture-worker-1` before and `hm-stored` after (the name's first label) |

The first four rows sent the node a hostname equal to the one it already ran,
so an unchanged node name is what those rows predict. What they show is that the
rewritten document with an unchanged hostname (the commonest case) applies
and completes through the product on the fixture's container node, and that
the form of the document differs without the hostname doing so. The `stored`
row shows that a rename reaches the container node with no re-import, and that
its node name followed the hostname there. No row shows anything about a
restart. The dry run of the same file (`apply-config --dry-run
--mode no-reboot`, the node's own configuration with its `HostnameConfig`,
which held the keys `apiVersion`, `kind` and `auto`, replaced by the filled
one, which is the product's merge of decision 1) answered "Applied
configuration without a reboot" (Talos's classification of the change in
no-reboot mode, not an observed restart), with a diff of 5 lines added and 3 removed
in two places. The import-equivalence measurement
([the 20261118 evidence](../design/research/20261118-import-analysis-and-equivalence.md),
section 11, `output.txt` last section) shows, for each of the six machines,
that the node's own document is removed and recorded at import in every case,
recorded as exactly the fill's own only where it is `auto: off` plus the name,
and that the form-only verdict (step 8) is true only for that case.

**Not shown**, each with its cause. (1) Whether a virtual machine or hardware
node applies a *changed* hostname, and what becomes of its node name then: the
`stored` row measured the container only. (2) Whether the hostname and node
name of the container stay changed after a restart: a container has no reboot
(5). (3) The form-only verdict of a plan: it is the
evidence of an adopt plan, and no row adopted. (4) `container` and `cloud`
modes, and `registerWithFQDN` set against unset: the fixture has no scenario
for them, so they stay read and not measured, and so does a `cloud` node's own
hostname being overridden by the static one (`hostname` has the highest
priority, line 73). (5) A restart of any kind: the fixture's node is a
container, which has no reboot, and its uptime is the host's, so whether the
rewritten document, or a changed hostname, needs a restart stays unmeasured in
a container, a virtual machine and hardware alike, until a run reads a start
time of the node itself. The client sentences that
mention a restart stay conditional on (5), and the one about the node name
says "normally" (client contracts A9 items 6 and 7). The fill is implemented by ginsys/bronzeward#1040, the API
record by ginsys/bronzeward#1041, and the client words by
ginsys/bronzeward#1042 and ginsys/bronzeward#1043.

## 7. Validation stages

Design: [§6.9](../design/Talos_Configuration_and_Machine_Management_Design.md#69-secret-references-and-validation-stages).

| Stage | Checks | Guarantee | On failure |
| --- | --- | --- | --- |
| 1. Authoring | YAML syntax of every document; tags only as §5.3 allows; every tag declared and every declaration used; name grammar; `kind`, `version` and `encoding` values; embedded identifications; reserved text (§5.5); paths well-formed (§2.2) | The fragment is well-formed source. A draft with references is not a validated native configuration. | The draft update is refused, naming paths |
| 2. Composition and materialization | §6 steps 1–7 under the compiler identity: pinning, dependency state, kind match, resolution, native composition, tracing, output checks | The configuration was produced by native composition of reference-free fragments, with every reference resolved and attributed | Publication is refused; nothing is committed |
| 3. Release validation | §6 step 8 on the actual complete configuration with the pinned renderer; then redaction (§8) and artifact encryption (§11) | The encrypted artifact is exactly what upstream validation accepted | Publication is refused; the validation message is shown only as §8.3 allows |
| 4. Execution checks | Plan, target and operation preconditions; decryption and credentials under the executor identity at use | Owned by [execution and recovery §3](execution-recovery.md#3-dispatch-commitment) | As specified there |

A native rejection in stage 2 or 3 is a correct result, not a compiler error:
SR's type-mismatch and invalid-value cases were rejected by early resolution
exactly as native composition of the literal values was (SR §6.3).

## 8. Sensitivity, provenance and redaction

Design: [§6.9](../design/Talos_Configuration_and_Machine_Management_Design.md#69-secret-references-and-validation-stages),
[§6.6](../design/Talos_Configuration_and_Machine_Management_Design.md#66-release-pipeline).

### 8.1 Provenance by tracer composition

For every composition that is published or shown, the compiler composes, with
the same renderer, the same import base and the same fragment order:

- a **trace pass**, in which every resolved value is replaced by a
  shape-preserving stand-in, so that every output leaf holding a stand-in is
  attributed to its reference occurrence. The stand-in format is SP's: each
  letter becomes `x`, each digit `0`, every other byte stays, each line of five
  bytes or more starts with `zq` and a three-digit occurrence id, and an integer
  becomes 61000 plus its id (SP §2). SP calls that format the experiment's, not
  a design (SP §8); it is adopted because SP's results were measured with it
  **(choice §16.22)**;
- a **flip pass** per boolean reference, whose flipped leaf is that boolean's;
- a composition of each proper **fragment prefix**, to name the fragment after
  which an overridden reference's stand-in disappears.

This located every referenced value through resolution, typed values, base64
re-encoding, aliases, identified embedded YAML and JSON, a second document,
list append and duplicate list entries, moved and rotated values, and
references overwritten by a literal, by another reference and by a delete
directive, without a merge engine
([SP §6.1](../design/research/20260925-sensitivity-provenance.md#61-criterion-1-sensitivity-through-each-transformation)).
The cost is one extra composition per boolean and per fragment prefix (SP §9).

The implementation settles eight points SP left open:

- **Canonical base64.** The machinery decodes the byte fields it holds as
  base64, such as `cluster.ca.key`, and writes them again in canonical
  standard base64, which can change the last character before the padding. A
  string stand-in is therefore also matched in that re-encoding. Without it,
  3 of the 9 schema secrets of a generated control-plane base were not
  attributed (a probe at PoC time, kept as a regression test).
- **Embedded documents in the output.** Composition does not keep source
  paths, so an identified embedded document is found in the output by exact
  match of the text the trace pass wrote for it, whether or not a reference
  stands in it, so that §6 step 7 sees its leaves. A document met on one side
  only fails closed, and so does a text that matches documents of two formats
  (a boolean has no stand-in, so a JSON and a YAML document can trace alike).
- **Short stand-ins.** A stand-in with no line long enough for the `zq` head
  holds only `x`, `X`, `0` and punctuation, so every longer stand-in of the
  same shape holds it. Such a stand-in is found only where a leaf, or a leaf's
  base64 decoding, equals it. Two stand-ins of one composition of which either
  holds the other, placed or before encoding, cannot be told apart, and the
  composition is refused as `trace-indistinct`, naming both occurrences. A
  mapping reference with no member is refused the same way: no leaf could
  carry its stand-in.
- **Multibyte characters.** When the `zq` head ends
  inside a multibyte character, that character's remaining bytes become `x`,
  so the stand-in stays valid UTF-8 and keeps its byte length; otherwise the
  trace pass could not compose a value the real one accepts.
- **Mapping members.** A member of a mapping reference is named by its
  position in key order, never by its key, which is a value the provider holds
  (§4.2).
- **Attribution.** SP's rule is adopted whole: every leaf where the real and
  trace compositions differ must carry a stand-in, and every leaf that carries
  one must differ. A stand-in that happens to equal an unchanged literal of
  the composition therefore fails closed rather than being attributed.
- **Overrides.** A prefix is composed only for an occurrence that reached no
  output leaf, or for an import base occurrence that reaches fewer output
  leaves than it does in the import base alone: an aliased reference one of
  whose aliases a fragment replaced or deleted. That fragment, the one after
  which fewer leaves first carry it, overrode the reference (§6 step 7). The
  import base alone is composed for this whenever it holds a reference and a
  fragment follows. Each prefix is composed from the real values too, and a
  prefix leaf carries a stand-in only where it differs from the real prefix's,
  as in attribution, so a literal shaped like a stand-in is not counted. An
  occurrence absent from its own source's prefix fails closed.
  A boolean that reached no output leaf is flipped in the prefixes from its
  own on, and the fragment after which flipping it stops changing a leaf is
  the one that overrode it; SP left that outcome unknown, which §8.2's
  "exactly one" outcome does not allow. The prefixes are not validated: a
  prefix may be legitimately incomplete.
- **Schema secrets restored for validation.** The machinery at v1.14.5 checks
  the content of a secret field, so a stand-in where a PEM block or a key
  stands is invalid, and a generated control-plane base failed the trace pass
  while it passed the real one. Validation therefore runs on a restored copy of
  the trace pass and of each flip pass, in which the real value is put back, in
  memory only, into every leaf that holds a schema secret (a field the
  machinery's redaction changes, §6). A leaf the operator marked keeps its
  stand-in, so a marked leaf whose stand-in the machinery rejects is still
  refused as a fidelity failure. Each place is tested on its own: a member of a
  mapping value, an alias of a reference and an alias of a container that holds
  a reference are restored only where the schema holds a secret, and keep the
  stand-in elsewhere. Only validation uses the
  restored copies.
  Attribution, the tree-shape comparison and the prefixes read the stand-in
  passes, as before. A restored pass that fails validation gives the bare
  fidelity refusal. The first restored pass also supplies the template for the
  machinery's validation messages, and those go through the message redaction
  of §8.3 like the real composition's, so no text from a restored pass leaves
  unredacted. The exposure does not grow: the real composition already holds the
  same values in memory. Rejected: validating the real composition only, which
  drops the control that one pass must not validate where the other does not;
  and keeping the stand-ins, which refuses every base that holds a PEM block or
  a key.

The trace and flip compositions carry each value's length and character
classes, so they are handled like the real composition: never persisted, logged
or shown.

**Fidelity fails closed.** Publication is refused when any resolved real
fragment lacks its trace or flip counterpart, when the real and trace
compositions differ in tree shape, or when one pass composes or validates and
the other does not (a pass validates on its restored copy, above). SP's prototype skipped a fragment whose trace was missing,
which would have shown its references unredacted while reporting the run
complete ([SP §8](../design/research/20260925-sensitivity-provenance.md#8-limits)).
SP's control for the fidelity check was a one-mutation smoke test that never
injected a structural change, and it was not exercised in 16 of 56 cells
(SP §4.4, §8), so the tree-shape comparison is required here without evidence
that it detects a structural difference (§15).

### 8.2 The provenance record

Recorded per reference occurrence at resolution and completed after
composition (SP §6.3):

| Field | Content |
| --- | --- |
| reference, version | logical name and pinned version |
| encoding | the declared modifier, if any |
| member | for a mapping reference, the member's position in key order (§8.1) |
| source | import base or fragment revision, its SHA-256, source document and path, and the occurrence's position among its source revision's occurrences (§9); a mapping's members and an alias's outputs share their occurrence's position |
| outcome | exactly one of: output document and path (one row per path, so an alias has two); or the fragment that overrode it |

Every path in the record is written with its value tokens redacted (§8.3), as
is the path of a reproduction dependency (§9).

The compiler also hands publication the declared kind each record was compiled
as and, for a mapping, its member count. Publication holds the kind to the
source's declaration and requires an outcome for every member before committing
([PA §6.2](persistence-api.md#62-the-commit-transaction-t3)). The stored record
omits both: the declaration holds the kind, and the members' records the count.

SP's third outcome, `unresolved`, cannot occur in a published release, because
every tag must resolve (§6 step 4) and a tag inside unidentified text is
refused (§5.5).

Provenance is computed from each composition's own trace pass, never carried
over from another revision's. Reusing another revision's paths leaked a moved
value, and reusing its values leaked a rotated one (SP §4.4, §5). This does not
mean reading secrets again to display a stored diff: the redacted review data
of a published release (§11) is derived once, at publication, from that
release's own trace, and is shown as stored. A composition that is not a
published release, such as a draft preview, is traced when it is composed.

### 8.3 Redaction

Every surface derived from a composition (redacted diffs, validation and
composition messages, logs, API responses, support bundles, dry-run output)
is redacted by three complementary means, all applied:

| Means | Covers | Token |
| --- | --- | --- |
| Composed path | every leaf §8.1 attributes to a reference | `<redacted:REF@VERSION>`, and `<redacted:REF@VERSION#N>` for the member at position N of a mapping reference (§8.1) |
| Schema | every field the pinned machinery marks secret, which covers the base secrets | `<redacted:schema>` |
| Value | exact copies of resolved values that no path covers | `<redacted:value>` |

Together they left no referenced value and no base secret in any of the 28
cases on either base; composed paths alone leaked base secrets in 26 cells per
base, the schema alone referenced values in 15, and value matching alone 8
([SP §4.1](../design/research/20260925-sensitivity-provenance.md#41-which-representations-leak-a-referenced-value)).
A token names the reference and version, so a rotation shows as
`<redacted:reg-pass@1>` becoming `<redacted:reg-pass@2>` without either value.
The token syntax is SP's, which SP calls the experiment's (SP §8); it is adopted
with the stand-in format **(choice §16.22)**.

- **Paths.** A path names keys, and a key can be a value: a mapping
  reference's key, or a literal written as a key. Every path a compilation
  surface shows, in a refusal, a provenance record or a dependency record, has
  each token that equals a resolved `string` value, a mapping reference's key
  or `string` member, the standard base64 encoding of one of them or its
  canonical re-encoding (§6 step 7), or that
  contains one of them of six bytes or more, written `<redacted>`. Integer and
  boolean values are not looked for, as in the value means. The rendered path
  is checked too, as written and unescaped, for a value of six bytes or more
  anywhere and for any value as a run of whole tokens, since escaping can
  spell a value no token holds and a value can span tokens; such a path keeps
  its document only. A path that does
  not parse, or any path of a compilation whose pinned values do not all
  decode, is written `<redacted>` whole.
- **Paired diffs.** Once a diff's output side holds a redaction token
  anywhere, every base leaf the output does not show unchanged at the same
  path is redacted as `<redacted:paired>`, whatever its kind; a boolean could
  otherwise be read by elimination (SP §2, §4.4), and a redacted value may
  have come from a base value at another path (a shifted index, a renamed key,
  a moved value). Every base key the output does not show in the same mapping
  is paired too. A whole token is shown as itself. An output with no token
  pairs nothing. The base values stay readable in the base release's own
  redacted configuration.
- **Messages.** A renderer message is redacted by template: the same step on
  the trace pass gives a message whose stand-in quotes mark where the real one
  quotes a value, and each is replaced by its token. The text around the
  quotes must match exactly and split the real message in one way only. A
  message the template cannot explain or explains in more than one way, or a
  step that succeeded in one pass and failed in the other, is **withheld**
  (SP §2, §6.2). Decode errors quote a seven-byte prefix of a wrongly typed
  value (SR §8), which the template covers (SP §5). The value means then
  applies to what remains.
- **Verbatim messages from boolean inputs are withheld.** A boolean's stand-in
  is its own value, so a message quoting it matches its trace message and
  nothing marks the quote (SP §6.3). Until a way to mark such a quote exists, a
  message that the template reports verbatim is withheld when the step's input
  holds a boolean reference **(choice §16.23)**. A redacted message from such a
  step is withheld too, since its unmarked text can quote the boolean as well.
  A mapping reference's keys are unmarked the same way: the trace pass keeps
  them, so a message naming one (an invalid label key, say) matches its trace
  message verbatim, and a key may be shorter than the value means' floor. A
  step whose input holds a mapping reference is withheld likewise. A
  composition step's input is the import base and the fragments up to the one
  rejected; validation's is every source. A message is also withheld when a
  value form survives in it, as when a reference's name spells its value, or
  when a token it would hold names a reference whose name equals a value form
  of any length, which the six-byte search cannot see.
- **Verbatim messages quoting an authored literal are shown.** SP hands this
  case to this contract (SP §10). An authored literal is source text that
  ingestion has already passed as not secret and that ordinary persistence
  holds in the fragment, so quoting it discloses nothing the fragment does not.
  The exception is a literal that copies a secret. The value means redacts an
  exact copy of a resolved `string` value of six bytes or more in any message,
  and §6 step 7 refuses publication when such a copy stands at an output leaf. A
  copy that is shorter, or of an integer or boolean value, is not detected and
  is listed in §8.4 **(choice §16.23)**.
- **Withheld means not persisted.** A withheld message is replaced by a fixed
  notice naming the step, `<withheld: the composition message may quote a
  sensitive value>` or the same for validation; its text is not stored, logged
  or put into a support
  bundle, consistent with design §15.4 **(choice §16.23)**. SP leaves open how
  withheld messages reach a support bundle (SP §10); under this contract they
  do not.
- **Output that embeds the configuration.** Renderer output that carries a
  configuration or configuration diff, such as a refused or dry-run apply
  (E3 5.4, §7), is withheld as a whole unless it is positively recognised as
  one of the explained message templates. The filter fails closed, unlike the
  E3 prototype's.
- **Embedded JSON.** A token written into re-serialized embedded JSON has its
  angle brackets written as JSON Unicode escapes, so a reader searching for the
  literal token prefix will not find it (SP §6.3).
- **Value forms.** The value means looks for the forms §6 step 7 refuses: a
  resolved `string` value, a mapping reference's key or `string` member, the
  standard base64 encoding of one of them and its canonical re-encoding. In a
  message it replaces each run of text covered by copies of six bytes or more,
  copies that overlap or touch as one run, so no part of a copy is left. In the
  redacted configuration it also replaces a whole scalar or key equal to a form
  of any length, which step 7 cannot refuse when the value is under six bytes.
  Integer and boolean values are not looked for, as in the path means.
- **The redacted configuration.** The compiled configuration is shown as the
  real composition with the three means applied: each attributed leaf, and
  each key of a mapping reference's member, is its reference's token; each
  field the machinery marks secret is `<redacted:schema>`; the value means
  covers the rest. Identified embedded documents are written back with their
  tokens. A compilation whose pinned values do not all decode, whose rendering
  still holds a value form of six bytes or more, or one of whose tokens names
  a reference whose name equals a value form, shows nothing and says so. A
  value form that only partly copies a reference name of the same fragment is
  the authored-literal case of §8.4.

### 8.4 What redaction does not cover

- A literal copy of a secret in another form: re-encoded, split, hashed or
  partial. Value matching sees exact copies only (SP §6.3), and §6 step 7
  refuses only exact copies.
- A secret in a base field that the schema does not mark and nothing
  references. Ingestion (§2) is what must keep such values out of fragments.
- Unidentified embedded text, which is opaque to every means except exact value
  matching.
- An authored literal that copies a secret value shorter than six bytes, or an
  integer or boolean secret value, quoted verbatim in a message (§8.3).
- The formats: SP measured its prototype's own log and support formats (SP §3).
  The PoC's formats must be re-checked against the same oracle (§15).

## 9. Dependency records

Design: [§6.9](../design/Talos_Configuration_and_Machine_Management_Design.md#69-secret-references-and-validation-stages),
[§7.5](../design/Talos_Configuration_and_Machine_Management_Design.md#75-rotation-and-retention-contract),
[§7.8](../design/Talos_Configuration_and_Machine_Management_Design.md#78-poc-retention-and-recovery-policy).

Each published artifact carries two secret-dependency records, derived from the
provenance record (§8.2) (SP §6.3; SR §6.2):

- **Effective (artifact) dependencies**: each reference and version whose
  stand-in reached an output leaf, booleans included through the flip pass.
  This is what the artifact contains and what rotation and lost-dependency
  classification track for it. SR could not identify boolean effective
  dependencies with its sentinel method; SP's flip pass can, and supersedes it
  (FR §8 C2).
- **Reproduction (source) dependencies**: every reference occurrence in the
  import base and the source fragments, with the revision and its digest,
  overridden ones included. This is what rendering the same release again from
  source needs. Two occurrences whose redacted paths read the same (§8.3), such
  as two keys of one mapping, are two dependencies, told apart by their
  position among their source revision's occurrences.

A reproduction dependency can outlive every effective one: an overridden
reference is absent from the artifact but still needed to re-render it
(SP §6.3, override-literal and delete). Applying a retained artifact needs the
artifact, its encryption key version and the executor's credentials, not the
source secrets; regenerating needs every reproduction dependency
([KL §5.2](../design/research/20260924-key-loss-restoration.md#52-criterion-2-applying-is-not-regenerating-and-ciphertext-is-not-executability)).

The artifact record also carries its encryption dependency, the Transit key by
an identity the provider does not reissue together with its version, not by name
and version alone ([KL §7](../design/research/20260924-key-loss-restoration.md#7-recommendation)
item 1, inferred): the key's name and the creation time its `keys` map gives for
that version, as the
[dependency monitor's classification](dependency-monitor.md#3-classification-procedure)
reads it (§11). The provider does not reissue that creation time on the
supported topology, one node whose clock does not step back; the residual risk
of a backwards clock step or several provider nodes is accepted for the PoC in
[design §7.7](../design/Talos_Configuration_and_Machine_Management_Design.md#77-poc-deployment-profile). Each pinned KV version is recorded the same way, with the
`created_time` §6 steps 3 and 4 agreed on: KV metadata deletion restarts a
path's version numbers, so path and version alone can name a replacement. It
also carries the renderer and contract record of §10.2.

## 10. Renderer selection and compatibility limits

Design: [§6.5](../design/Talos_Configuration_and_Machine_Management_Design.md#65-renderer-and-contract-pinning).

### 10.1 Selection: the Go machinery, in process

The PoC compiler composes and validates through the Talos Go machinery
(`pkg/machinery`) linked into the protected compilation process, not by
running `talosctl` as a subprocess **(choice §16.24)**. It generates no base
configuration (§6).

Within E3's matrix the two are the same: identical generation in 132 of 132
rows, the same validation verdict in all 2016 cells, the same RPC outcomes for
the 6 client pairs, and the same exit status for both in 42 of 42 contract dry
runs, a verdict that compares exit statuses only
(E3 §4.3, §6.2, [§7](../design/research/20260925-talos-compatibility.md#7-limits)).
The choice is packaging, not compatibility (E3 §8). On packaging:

- **The plaintext boundary.** Early resolution hands the renderer an import
  base and fragments that hold resolved secret values. A subprocess must
  receive them through some channel, such as files, arguments or inherited
  descriptors. A command-line argument is readable by other local processes,
  and a temporary file is one of the surfaces design §7.1 names and E1 had to
  control for
  ([E1 §3](../design/research/20260922-secret-ingress-extraction-before-persistence.md#3-what-was-built),
  4.3). In process, they stay in the memory of the one process that already
  holds them. This is the deciding reason, and it is inferred: neither path's
  exposure was measured, and §15 requires the compiler process to be scanned as
  E1 scanned ingestion.
- **The machinery is needed anyway.** Schema redaction uses its secret-field
  list (SP §2), and only its `compatibility` package refuses a Kubernetes
  version outside a target's window (E3 §6.3 item 5).
- **Costs accepted.** `talosctl validate`'s mode type is under the talos
  module's `internal/` tree, so the compiler implements the machinery's
  `RuntimeMode` interface itself, as E3's prototype did, whatever the minor
  (E3 §6.2). `secrets.Bundle.Validate` changed signature in v1.14, so a second
  contract minor needs a shim and a second build (E3 §6.2); while a deployment
  targets one minor, neither the shim nor the second build is needed. A second
  minor means a second binary in either implementation (E3 §8), and then the
  plaintext boundary returns between the builds; that is outside the PoC.

**Condition on the selection.** SR's and SP's parity and provenance results were
measured through `talosctl machineconfig patch`. Composition through the
machinery's `configpatcher` is inferred, not measured (E3 §6.2; FR §8 C4). Before
the compiler is accepted, the SR and SP matrices must be re-run with composition
through the compiler's own machinery path and reach the same verdicts. If they
do not, the compiler uses the pinned `talosctl` subprocess instead, without
reopening this selection (owner decision, choice §16.24). Plaintext then passes
only through inherited descriptors, never a named file or an argument. That
channel's exposure is unmeasured too: before the fallback is accepted, the SR
and SP matrices are re-run through it and reach the same verdicts, and §15's
leak scan covers its channel. If the fallback fails either, the compiler is
not accepted and the selection returns to the owner.

**SR half measured.** The [machinery gate](../../experiments/c10-machinery-gate/README.md)
re-ran SR's 15 cases on both of SR's bases: the machinery's composition of the
literal form equals talosctl's byte for byte with the same validation verdict
in every case, and the compiler on the tag form reaches SR's tag/early verdict
in every case except the two §5.5 refuses at authoring by contract (a quoted
look-alike and a reference inside unidentified embedded text).

**SP half measured; the condition is met.** The same gate re-ran SP's 28 cases
on both bases. The machinery's composition equals talosctl's byte for byte
with the same verdict in every composed case. The compiler reaches SP's
outcome, effective and overridden references at the case's versions and
message outcomes in every case except four refused by contract: the two §5.5
refuses at authoring, and `duplicate-literal` and `map-partial`, exact copies
(choice §16.21; the latter's literal writes a mapping reference's key). With
the case's first referencing fragments moved into the import base by marks,
the remaining fragments reach the same bytes, or are refused where they
override what moved (choice §16.20). SP's oracle finds no case value and no
base secret in anything the compiler renders. The compiler process's leak scan
over publications is §15's, not this condition's.

The executor's Talos client is not selected here. E3 shows the same RPC
outcomes through both implementations (E3 §4.3); the choice belongs to
[execution and recovery](execution-recovery.md).

### 10.2 Pinning and refusals

- **Pin to the node's minor.** The machinery module's minor equals the target
  node's running Talos minor. In E3, a v1.13 renderer's output was accepted by
  the v1.13.6 node and a v1.14 renderer's was refused (E3 §8).
- **The PoC compiles only the node's running contract minor**
  **(choice §16.25)**. A v1.12 contract was also accepted, but v1.10 and v1.11
  contracts were refused in immediate mode (E3 §4.3), and the PoC applies in
  `no-reboot` mode only.
- **Refuse, in Bronzeward, a target contract whose minor exceeds the
  renderer's.** The renderer silently renders its own contract instead
  (E3 §4.1, §6.3 item 1).
- **Refuse a Kubernetes version outside the pinned machinery's `SupportedWith`
  window** for the target. Neither generation nor validation refuses it
  (E3 §4.2). For Talos 1.14 the machinery accepts Kubernetes 1.32 to 1.37, while the
  documented range is 1.33 to 1.37 (E3 §4.4); the machinery's windows are wider than
  the documented tested path, and a stricter support policy is a
  separate decision (E3 §8). The version is the kubelet image tag of the
  validated configuration, the machinery's default image when none is set, and
  every machine a release covers must name the same one **(choice §16.32)**.
  The window is that of the Talos minor of the machinery version itself.
- **Record the contract, not only the renderer.** The release records the
  target contract, the machinery module version and its checksum, the Kubernetes
  version and the validation mode. A renderer version alone does not say whether
  a node can decode the output (E3 §8). The machinery version and checksum are
  the compiling build's own record of the module, as `go version -m` prints it
  **(choice §16.34)**; the contract is refused unless it is the machinery's
  minor **(choice §16.33)**.
- **Paths are versioned with the contract.** The v1.14 layout moves many
  v1alpha1 paths into separate documents (E3 §4.1), so marks, declarations and
  embedded identifications hold for the contract their fragment revision was
  written for. A contract change is a new fragment revision that passes stage 1
  again; paths are never translated automatically. A fragment that sets a
  v1alpha1 field that a v1.14 document in the composition now owns is refused
  by validation, and so is a v1alpha1 document that holds a WireGuard
  interface the v1.14 schema no longer loads.

Publication establishes the running minor using a fresh read-only executor
observation for every covered machine, before resolving compiler inputs. The
compiler receives the evidence reference, never Talos credentials. Missing or
unreadable identity/version evidence refuses publication, including initial
adoption; an older cached observation cannot substitute for this read. The
transaction rechecks the evidence and retains its immutable link per artifact
([persistence and API §6.1.1](persistence-api.md#611-publication-observations)).
This is the owner-approved ginsys/bronzeward#247 refinement (2026-10-19). It deliberately makes
publication depend on node availability. Reusing prior observations was rejected
because initial adoption has none and cached versions can silently become stale;
deferring this check to dispatch does not satisfy §13 row 17.

### 10.3 Compatibility limits and the deferral

Evidenced: generation, validation and the PoC's configuration RPCs (version
read, configuration read, no-reboot dry run and a label-patch apply) against one
live v1.13.6 node, in containers, with one control plane and one worker (E3 §7).
Not evidenced: any other live version, reboot-mode applies, a JSON6902 patch,
composition on renderers other than v1.13.6, worker configurations in SR and
SP, and every upgrade path. Generation and validation are evidenced on v1.14.1
(E3 §4.1 and §4.2). A live v1.14 node and composition on a v1.14 renderer are
not evidenced until the fixture move (ginsys/bronzeward#713) and the
composition rerun.

Unsupported, with the observed reason (E3 §6.3): a contract newer than the
renderer (silently clamped); v1.14+ output on a pre-v1.14 validator or node
(`DiscoveryServiceConfig` not registered); v1.10/v1.11 contracts on a v1.13 node
in no-reboot mode (refused in immediate mode; that the mode is the cause is
inferred; not tested on v1.14); an unparseable version string; Kubernetes outside the window; targets
the machinery predates; and the v1.15.0-alpha.0 prerelease as a v1.15 renderer.
`--strict` validation refused nothing in E3 (E3 §4.2).

Carried forward verbatim ([FR §2](../design/research/20260925-feasibility-evidence-review.md#2-what-binds-every-conclusion-here)):

> **Upgrade/LifecycleClient execution testing is deferred and the full design E3
> experiment has not passed. PoC compatibility is not full-E3 completion.**

This contract claims no upgrade support, no lifecycle execution and no full E3
([E3 §6.3](../design/research/20260925-talos-compatibility.md#63-criterion-3-unsupported-combinations-and-the-deferred-lifecycle-tests)).

## 11. Publication hand-off

Design: [§7.4](../design/Talos_Configuration_and_Machine_Management_Design.md#74-publication-without-distributed-transactions),
[§6.6](../design/Talos_Configuration_and_Machine_Management_Design.md#66-release-pipeline).

§6 performs design §7.4 steps 1 and 2. Step 3 is the compiler's: derive the
redacted review data (§8) and encrypt every full artifact under the artifact
key with the compiler identity, which can encrypt but not decrypt (PC §2).
Before encrypting, the compiler reads the artifact key's metadata with the
metadata identity and keeps the creation time its `keys` map gives each
version. After encrypting, it classifies the key version each encryption used
afresh, with the metadata identity, by the
[dependency monitor's procedure](dependency-monitor.md#3-classification-procedure).
Anything but `retained` refuses publication, as §6 step 3 refuses a source
version, and so does a creation time for that version that the first read did
not give or gave differently. So does a version whose creation time is not in
a second before the first read's `Date`, or a first read with no readable
`Date`: OpenBao gives creation times in whole seconds, so a key deleted and
recreated within one second could reissue an identity, and a version that
existed in a later second can only be deleted, and its name recreated, in a
later second still. The identity §9 records is that creation time, the same in
both reads, so a key deleted and recreated around the encryption is refused
rather than recorded under the new key's identity. A refusal for a version
created in the current second clears on a retry a second later. Step 4 is
persistence's: the compiler hands over, as one unit, only sanitized
or encrypted values:

- release metadata: the import base, source and assignment revisions, the
  renderer and contract record (§10.2), the stage 3 result;
- per machine: the artifact ciphertext and its digest, the configuration
  digest of its plaintext
  ([execution and recovery §1](execution-recovery.md#1-supported-operation-and-state-values)),
  the redacted review data, the provenance records (§8.2), the hostname record
  (§6.1 item 4: its source, where filled the name used, and the derived node
  name of §6.1 item 6, `null` where a secret reference or a value the review
  redacts is an input, the review is withheld in whole, or no hostname is set)
  and both
  dependency records with the encryption dependency (§9);
- per pinned version and encryption dependency: the classification step 3 and
  §11 made, as the dependency monitor seeds it
  ([dependency monitor §5.2](dependency-monitor.md#52-seeding-at-publication)):
  class, reason, the scheduled deletion time if any, and the database time the
  classification's request began, read before it
  ([persistence-api §6.1](persistence-api.md#61-steps)).

The persistence contract commits them atomically and rejects stale inputs
([ginsys/bronzeward#18](https://github.com/ginsys/bronzeward/issues/18)). A
failure before that commit persists nothing of the release: the ciphertext is
held only by the compiler until the commit, and the secret generations pinned in
step 2 already existed. Step 5 is unchanged: publication makes a release
available for planning and approval and never authorizes dispatch (execution
and recovery §2).

The interface types hold no plaintext. The plaintext configuration exists only
inside the compilation process between §6 step 4 and encryption.

## 12. Worked examples

### 12.1 An overridden reference

Fragment 1 (cluster layer) sets `password: !bwref registry/example-pass`;
fragment 2 (cluster-machine override) sets `password: example-override` at the
same path. Neither is the import base, so §6's import-base protection does not
apply. Both are resolved (step 4), native composition keeps fragment 2's
literal, and the prefix composition shows the stand-in disappearing after
fragment 2. Result, as in SR's and SP's override-literal case:

| Record | Content |
| --- | --- |
| provenance | `registry/example-pass@1`, from fragment 1: overridden by fragment 2 |
| effective | none |
| reproduction | `registry/example-pass@1` from fragment 1 at its digest |

Losing that secret version blocks re-rendering from source but not applying the
retained artifact (§9).

### 12.2 An alias and a boolean

`password: &p !bwref registry/example-pass` on one registry and `password: *p` on
another yield one occurrence effective at two output paths, each redacted as
`<redacted:registry/example-pass@1>`. `wipe: !bwref install/wipe` with
`{kind: boolean, version: 1}` is attributed by its flip pass; a diff against a
previous release with `wipe: false` shows `-wipe: <redacted:paired>` beside
`+wipe: <redacted:install/wipe@1>`.

### 12.3 An interrupted, reviewed import under encrypted staging

1. The operator imports a worker with encrypted staging and review requested,
   because the review must survive the process. Ingestion identifies the
   bundle's secret fields, the guard passes, provider generations are created,
   and the run of ingestion instance A seals the envelope.
2. A is killed before it stores the envelope and pauses. Its lease lapses; the
   absolute expiry has not.
3. The operator requests recovery. The takeover finds no payload, abandons the
   claim and fails its operation `ingestion-abandoned` (§3.4); the operator
   imports the worker again, and this time A, restarted, stores the envelope
   and pauses the claim (§3.6), owner generation 1; A's run stops there.
4. The operator reads the staged change and sees a machine file whose content
   holds a token, a field the schema list does not cover (§2.4). They mark
   `doc[0]/machine/files/0/content`. Instance B takes the claim (`held`, owner
   generation 2), decrypts the envelope, checks its digest, extracts the file
   content under a new name, guards that value, creates its generation and
   pauses the claim again with the new envelope.
5. A heartbeat that A's stopped run of step 3 sends late, at generation 1, is
   refused on both the claim's state and its generation. The operator continues; B takes the claim at generation 3 and its
   draft transaction commits the draft, its reference rows and `released`
   together.
6. Had the provider been unreachable at step 4, B's decryption would fail, the
   claim would stay `held` under B until B's lease lapsed, and a takeover
   would then resume the stored envelope and pause the claim again, until the
   absolute expiry, after which the claim is abandoned and the worker is
   imported again.

### 12.4 A rejected draft update

1. An operator submits a draft update that adds a node label holding a token
   and a machine file whose script embeds the same token in a command line. The
   update's text holds the `v1alpha1` document first, with the file, and the
   `KubeNodeConfig` document second, with the label. The operator marks the
   label, `doc[1]/labels/example.test~1token` (§2.2 escaping); the file content
   is not marked, and the schema does not cover it (§2.4).
2. Steps 0 to 4 run: the claim is `held`, the label's value is substituted by
   a reference under a newly minted name.
3. Step 5: the value comparison passes, because no other scalar equals the
   token; the substring search finds it inside
   `doc[0]/machine/files/0/content`. The input is refused with that path and the
   rule `substring`, and no value. No provider generation was created, and the
   claim becomes `abandoned` with no payload.
4. The operator marks `doc[0]/machine/files/0/content` as well and resubmits.
   Both values are extracted, the script under its own name, referenced whole
   (design §6.9), and the guard passes.

### 12.5 From draft to encrypted release

1. Worker `w1` has an import base from its import. A cluster-layer fragment
   sets `password: !bwref registry/example-pass`, declared
   `{kind: string, version: 1}`; a cluster-machine fragment adds a node label.
2. §6 steps 1 to 3: the import base and both fragments are snapshotted, every
   reference in them is pinned with its provider object, and each classifies
   `retained`.
3. Steps 4 and 5: all three are resolved and composed through the machinery
   onto the import base. Step 6: the trace pass and the proper prefix
   compositions (the import base alone, and with the first fragment) attribute
   every stand-in; there is no boolean, so no flip pass. Step 7: no reserved
   text, no unattributed copy, no import-base reference overridden.
4. Step 8: the configuration validates in the node's platform mode. The review
   data is derived from this composition's trace: a diff against `w1`'s previous
   release shows the password as `<redacted:registry/example-pass@1>`, the
   label in clear, and the import base's secrets under their own tokens and
   `<redacted:schema>`.
5. The compiler encrypts the configuration under the artifact key and hands
   persistence one unit (§11): the import base, source and assignment
   revisions, the renderer and contract record, the ciphertext and its digest,
   the configuration digest of its plaintext (execution and recovery §1), the
   review data, the provenance and both dependency records. Persistence
   commits it atomically; nothing is dispatched.
6. Had step 8 rejected the configuration, the message would be shown through
   its template or withheld (§8.3), and nothing of the release would be
   persisted.

## 13. Failure and rejection cases

| Stage | Condition | Outcome | Persisted |
| --- | --- | --- | --- |
| Ingestion | Parse failure, or a mark addressing no node | Refused before any provider write; claim abandoned | Claim row, no payload |
| Ingestion | Guard hit (§4.2) | Refused, naming paths and rule; claim abandoned | Claim row, no payload |
| Ingestion | Provider write fails part-way | Refused; claim abandoned | Claim row, no payload; unused provider generations |
| Ingestion | Crash before the payload is written (end of §2.3 step 8) | Transient: abandoned at lease lapse; encrypted: a takeover finds nothing to decrypt and abandons it | Claim row, no payload; unused generations if past step 6 |
| Ingestion | Crash after the payload is written, before the draft transaction | Transient: abandoned at lease lapse; encrypted: takeover (§3.4) | Claim row with ciphertext (encrypted); unused generations until the draft commits |
| Ingestion | Crash inside the draft transaction | Claim unreleased with payload; the named draft unchanged, none of the transaction's entries or revisions committed | Claim row with ciphertext (encrypted) |
| Ingestion | A mark on a paused claim refused before any provider write (§3.6) | The claim is `paused` again with its earlier envelope; the refusal is recorded on its operation | Claim row with its earlier ciphertext |
| Ingestion | A mark refused after a provider write (§3.6) | Refused; claim abandoned | Claim row, no payload; unused provider generations |
| Ingestion | Crash in a mark's or a continuation's run (§3.6) | Takeover resumes the stored envelope: a pending review pauses again, a continued one runs the draft transaction | Claim row with its stored ciphertext; unused generations of an interrupted mark |
| Ingestion | Stale owner heartbeat, commit or release, or one after the claim paused | Refused by owner generation or state | Nothing |
| Ingestion | Absolute expiry, the operator's abandonment, or recovery-mode entry, a paused claim included | Claim abandoned; re-ingest | Claim row, payload cleared |
| Authoring | Undeclared or unused name, other local tag, tag on a key or sequence, reserved text, bad path | Draft update refused | Nothing |
| Compilation | Dependency not `retained`, or unreadable by the compiler | Publication refused | Nothing |
| Compilation | Kind mismatch, or `base64` on a non-string | Publication refused | Nothing |
| Compilation | A reference to a secret of another cluster (§5.1) | Publication refused | Nothing |
| Compilation | An ordinary fragment overrides or deletes an import-base reference (§6) | Publication refused, naming the fragment and path | Nothing |
| Compilation | Contract newer than renderer or not the node's minor; Kubernetes outside the window | Publication refused | Nothing |
| Compilation | Native composition or validation rejects | Publication refused; message redacted or withheld (§8.3) | Nothing |
| Compilation | A hostname filled from a machine's name that another machine of the cluster holds explicitly, or a node name that another machine's applied release holds (§6.1 item 6) | Publication refused, rule `hostname`, naming both machines | Nothing |
| Ingestion | The machine's name changed between the removal of a filled hostname (§2.3 step 2a) and the draft transaction | The draft transaction is refused `409 stale-input`; re-ingest | Claim row, no payload |
| Compilation | Trace or flip fragment missing, shape differs, or passes disagree | Publication refused (§8.1) | Nothing |
| Compilation | Reserved text or an unattributed exact copy in the output | Publication refused, naming paths | Nothing |
| Compilation | Encryption fails, or persistence rejects a stale input | Publication refused | Nothing of the release |

For compilation/publication rows, "Nothing" means nothing of the release.
The publication operation, act and events, and any immutable publication
observation starts/results already recorded, remain as specified in
persistence/API §6.1.1 and §14. A refusal commits no release or Desired change;
an interrupted observation can leave its start without a completed result.

A refusal never quotes a value, and a refusal is never retried by weakening a
check.

## 14. Invariants

1. **No plaintext before extraction.** Ordinary persistence accepts only the
   sanitized type, which only the ingestion package constructs, and the input is
   unresolved from the reader on.
2. **Literal stays literal.** Only a declared `!bwref` tag is a reference.
3. **The compiler never merges.** Composition, overrides, aliases and list
   changes are the renderer's.
4. **Every reference resolves before composition**, or nothing is published.
5. **Nothing is shown without its own provenance.** Every shown composition has
   its own trace; an unexplained message or configuration-bearing output is
   withheld.
6. **Superset recorded, effective recorded.** Every release carries both
   dependency records.
7. **One owner per claim generation.** Every claim transition is conditional on
   the owner generation, and an owner's transitions on the owner too; no
   transition revives a lapsed lease for its old owner.
8. **No unkeyed secret digest is persisted.** The only unkeyed digests over
   plaintext are the whole-configuration digests of §4.1; an artifact's
   ciphertext digest (§11) is over ciphertext.
9. **Renderer minor equals the node's minor and is at least the target
   contract's**; Kubernetes is inside the window.

## 15. Verification and evidence limits

Design: [§18.1](../design/Talos_Configuration_and_Machine_Management_Design.md#181-phase-0---evidence-before-implementation-contracts),
[§18.2](../design/Talos_Configuration_and_Machine_Management_Design.md#182-phase-1---configuration-control-on-an-existing-cluster).

An implementation of this contract must show, with a control that can fail for
each:

- successful, rejected and interrupted ingestion through import, drift adoption
  and a draft update, with this pipeline interrupted at each of its steps
  (§2.3), scanning drafts, indexes, logs, temporary files, staging, the
  database's data directory, the write-ahead log and backups for every run, not
  only for selected bundles as E1 did (E1 4.2, 4.6);
- the same scan of the compiler process's surfaces (temporary files, logs,
  error reports and, if the §10.1 fallback is taken, the subprocess channel)
  over successful, rejected and interrupted publications (§10.1);
- lease extension refused to a non-owner; takeover of `held` and `resumed`
  claims only after lease lapse; a takeover with nothing to decrypt; a stale
  owner refused after takeover; a crash inside the draft transaction; recovery
  with the provider unreachable;
- the operator review of §3.6: a pause, a show, a mark that extracts a value,
  a mark refused before and one after a provider write, a continuation, a
  crash in a mark's and in a continuation's run, and a paused claim abandoned,
  expired and kept across a restart, each scanned as the first item requires,
  as persistence and API §16 lists;
- the SR and SP matrices through the compiler's own composition path (§10.1),
  with references in the import base as well as in fragments, and SP's oracle
  over the PoC's own log and support formats (§8.4);
- a fidelity check that fires on an injected structural change (§8.1);
- every refusal in §13.

Evidence gaps this contract carries rather than closes:

The limits below retain the distinction between the original experiments and
implementation evidence. Current scenario results, check mappings and open
assessment items are in the [PoC acceptance record](../design/research/20261018-poc-acceptance-evidence.md)
and its [refusal/access supplement](../design/research/20261019-poc-coverage.md).
Those results do not erase an experiment's untested cases or supply the owner's
acceptance.

**Ingestion and staging**

- **Interruption of this pipeline**: measured separately for import, fragment update and drift adoption on the selected fixture. S1 kills
  the ingestion at each of steps 0 to 8 under both staging modes. After each
  kill it checks the claim, the operation and the draft's ETag, and that as
  many generations as were created before the kill are still listed under the
  claim; versions are not inspected. It then scans the logs and the database's
  data directory, write-ahead log included, for every fixture secret beside a
  positive control. One database dump, taken after the last kill, is scanned
  at the end, with a control that the dump holds an interrupted claim's row
  (§2.3;
  [acceptance plan S1](acceptance-plan.md#s1-adoption-of-the-existing-cluster)).
  E1's screen covered the run root and live tables only, for a different
  pipeline (E1 4.2, 4.6). A draft update is measured the same way by S2's
  negative controls, for a fragment update only: the update succeeds, is
  refused twice, and is killed at each of steps 0 to 7 under transient
  staging, its only mode; after each kill the claim is swept, the generations
  made are still listed and the draft's ETag is unchanged, and every outcome is
  scanned, the logs, the data directory, the write-ahead log and a dump taken
  after the last kill among the surfaces
  ([acceptance plan S2](acceptance-plan.md#s2-publication)). Profile and
  assignment updates ingest no document and hold no claim. S5's full mode
  separately runs drift adoption through success, refusal and its interruption
  points, with the retained checks and scans listed in acceptance-record R1;
  its pass mode deliberately omits that matrix. These are fixture results,
  not broader renderer or provider-topology coverage.
- **Operator review** (§3.6): measured on the fixture and in Go checks. S1
  pauses a reviewed import, shows it, keeps it paused across a restart,
  refuses a mark before and after a provider write, kills a mark's run at each
  point it re-enters and a continuation's run before its draft transaction,
  each resumed by takeover, and abandons a claim by request and by its
  absolute expiry, every outcome scanned
  ([acceptance plan S1](acceptance-plan.md#s1-adoption-of-the-existing-cluster)).
  Recovery-mode entry with a paused claim is measured in a Go check only
  ([persistence and API §16](persistence-api.md#16-verification-and-evidence-limits)),
  not on the fixture.
- **Schema detector coverage**: `schema-covers-base-secrets` was never seen to
  fail and ran on control-plane bases only; disk-encryption, installer and disk
  configuration were absent from the environment; the list's precision was not
  measured (§2.4; SP §4.4, §8; E1 §7).
- **Keyed-digest mechanism**: the primitive is chosen (§4.1, choice §16.27).
  Its determinism, its keying (a digest is not the input's SHA-256) and its
  restriction to ingestion identities were exercised against OpenBao 2.6.1 in
  dev mode, in memory, not against the fixture's Raft node. Comparison across a
  rotation of the key is undesigned. A digest key deleted and recreated under
  the same name is not handled either: its digests stop matching, so a
  comparison fails rather than passes.
- **Whole-configuration digests**: the unkeyed baseline and artifact
  configuration digests are only as unguessable as the whole configuration;
  that was not assessed (§4.1).
- **Claim contention**: E1 ran one process against one claim (E1 §7). The
  implementation's tests race two takers of one lapsed claim, at the claim and
  at the takeover route, and exactly one wins (§3.4); they refuse each owner
  statement that breaks one term of its fence (§3.2). No clock was skewed: both
  times are the database's. Lease, expiry and heartbeat values are open (§3.2).
- **Identities**: PC measured the `publisher` create-only policy and the
  compiler and executor policies (§1). The implementation's policy tests also
  exercise staging-key, baseline-key and HMAC use under the fixture's
  policies, each refusal beside a control with the refused grant added, against
  OpenBao 2.6.1 in dev mode. They read the compiler and metadata policies back
  from the provider and compare them exactly; the compiler identity reads a
  pinned generation and encrypts an artifact the executor decrypts, and is
  refused an artifact decrypt, a generation create, a generation's metadata
  and the Talos access. There, a `cas=0` create on an existing generation
  is refused to ingestion by its policy (403) before check-and-set is reached:
  with `update` added to that policy, the same request is refused by
  check-and-set (400) instead.
- **Addressing**: the scheme (§2.2) is tested by the implementation's own
  parser only: each path formats back to itself, `~0` and `~1` escapes, `|`
  inside an embedded document's tokens, and refused malformed paths. S1 marks
  a value by such a path. No other RFC 6901 implementation was compared.
- **Ingestion input**: only configurations read back from a node were
  ingested; a generated configuration before Talos normalizes it was not (E1 §7).

**Composition, provenance and redaction**

- **Machinery composition parity**: measured for SR's and SP's matrices on
  both of SR's bases (§10.1, machinery gate). The gate's bases pass through
  ingestion, so their schema secrets are references resolved by the compiler;
  case references reach a base only by marks on a composition the compiler
  produced, never in a hand-written import base. SP's pair controls compared
  its tool's diffs; bronzeward has no diff yet, so the pairs check versions
  and output paths only.
- **Kinds and cases not run**: durations, IP and CIDR fields, a list as a
  target, list-element overrides by selector, more than two fragments, worker
  configurations, a tag name that is valid base64 or a YAML 1.1 boolean word,
  and a hand-formatted embedded literal (SR §8; SP §8).
- **Fidelity control**: SP's was a one-mutation smoke test that never
  injected a structural change, not exercised in 16 of 56 cells (SP §4.4, §8).
  The implementation's unit tests inject a changed tree into the comparison
  (a key added, a key renamed, a scalar made a list, a tag changed, an
  untraced leaf changed, a stand-in planted in an unchanged leaf, a flip pass
  with a key added) and each is refused; a trace pass that fails validation
  while the real composition passes is refused too. Those injections are made
  on the compared trees, not through a renderer that misbehaves.
- **Paired-diff control**: loose; it shows the base text is present in the
  per-side diff, not that it sits on a removed line (SP §8).
- **Messages**: a quoted boolean cannot be marked, so such messages are
  withheld; an authored literal copying a short, integer or boolean secret is
  not detected; withheld messages reach no support bundle (§8.3, §8.4). The
  implementation's unit tests drive the machinery's own type-mismatch and
  invalid-value messages through the template, and control a template
  mismatch, an ambiguous split and a step that fails in one pass only; no
  test drives a message from a renderer that misbehaves. Paired diffs
  (`<redacted:paired>`) are applied by the first surface that shows a diff;
  the redacted configuration shows none.
- **Tracer limits no case reached**: a string line under five bytes carries no
  id, an integer stand-in can equal a literal integer, and a validation rule can
  judge a stand-in differently from its value (SP §8); the last is refused under
  §8.1, and since v1.14.5 checks the content of a PEM block or a key it
  is met for every schema secret, which §8.1 settles by restoring those values
  in the validation passes. A marked leaf outside the schema secrets still keeps
  its stand-in there, so a rule that judges that stand-in differently still
  blocks the publication. A short stand-in that equals an unchanged literal is refused as a
  fidelity failure (§8.1), so a short value can block a publication that would
  otherwise be correct; no case measured how often.
- **Embedded formatting**: an author's formatting is not preserved (§5.4).

**Dependencies**

- **Step 3, step 4 and §11's bracket** are tested on synthetic answers only:
  each refusal (not `retained`, a changed `created_time`, a first read with no
  readable `Date`, a version created in or after that `Date`'s second or
  missing from the first read, a version recreated or below a floor at the
  second read) beside its passing case, and a mutation of each check fails its
  test. No key or path was recreated against a provider (dependency monitor
  §10.2); S2's publications pass them on the fixture's provider, where nothing
  was recreated.

**Renderer and environment**

- **Plaintext boundary**: the in-process path is scanned by S2's negative
  controls, on one instance, for one covered machine (the worker). A
  publication succeeds, one is refused by the compilation (`base-override`),
  and one is killed at each of its interruption points: `publish-claim`
  (after the claim), `publish-snapshot` (the snapshot read),
  `publish-observations` (fresh executor evidence recorded), `publish-pins` (the pinned values read and held), `publish-compile` (every
  covered machine compiled, its configuration held in plaintext),
  `publish-encrypt` (the artifacts encrypted, the plaintexts still held) and
  `publish-commit` (every statement of the commit transaction made, before
  its `COMMIT`). After each, the logs, the instance's writable layer, the
  database's data directory and write-ahead log are scanned for every fixture
  secret beside a positive control, and a dump taken after the last at the
  end. A stop at a point commits no release; completed observation starts/results remain. The operation stays `running` at its
  claim's generation until the lease lapses, then another worker claims it and
  publishes one release; the implementation's tests stop at each point the
  same way ([acceptance plan S2](acceptance-plan.md#s2-publication)). The
  process's memory is not scanned, no subprocess channel exists to scan, and
  the deciding reason in §10.1 is inferred.
- **Leak detection**: exact copies only; the OpenBao storage volume is not
  observable, and Transit plaintext crossed loopback without TLS in the fixture
  (E1 §7).
- **Validation mode**: SR and SP validated the fixture base in container mode;
  metal mode only on generated bases (SR §8).

The Upgrade/LifecycleClient deferral in §10.3 stands: this contract does not
claim full E3, upgrade support or lifecycle execution.

## 16. Choices for owner review

Each is the most conservative option consistent with the design where the
evidence does not settle the question, unless it says otherwise. Each is marked
in place as
**(choice §16.n)**.

1. **Ingestion and compiler as two identities** (§1). Alternative: design
   §13.2's single role. Input to identity and approval policy, which owns the
   identities.
2. **RFC 6901 addressing with `doc[n]`** (§2.2). Any scheme that escapes `.`
   and `[` would meet E1's finding.
3. **Create-only `cas=0` generations at paths of their own, with a component
   the database does not issue** (§2.3 step 6). Alternative: design §7.8 item
   1's overwritten path with explicit `max_versions`. Collision rationale
   inferred (DB §9).
4. **Baseline under its own key, decryptable by no runtime identity** (§2.3
   step 8). Alternative: the artifact key, which makes it executor-decryptable.
5. **Transient staging by default; encrypted only for review that must survive
   the process; a separate staging key** (§3.1). E1 recommends the split, not
   the key separation.
6. **An absolute expiry beside the lease** (§3.2). Alternative: lease only, as
   in E1; repeated takeovers could then keep a claim alive indefinitely.
7. **Owner-only lease extension; takeover only after lease lapse, before
   absolute expiry, on an explicit operator request, fenced by owner
   generation** (§3.3, §3.4). Alternative: automatic takeover; no contention
   evidence supports it.
8. **Abandonment evaluated at read time, written by a sweep** (§3.5).
   Alternative: a sweep alone, which would let a late sweep delay refusals.
9. **Claims from an earlier recovery epoch are abandoned** (§3.5). A restore
   rewinds generations.
10. **HMAC under a provider-held key for persisted value digests** (§4.1).
    Alternative: per-run salt, which breaks cross-run comparison.
11. **Keep the substring search beside value comparison** (§4.2). Alternative:
    value comparison only, which misses embedded secrets.
12. **Every secret scoped to one cluster; no library-wide secrets** (§5.1).
    Alternative: library-wide scope for cross-cluster fragments.
13. **A new name for every extracted value, even an unchanged one** (§5.1).
    Alternative: deduplicate by keyed digest; avoids churn, but rests on
    digest comparison, which is undesigned across a rotation of the digest key
    (§4.1).
14. **Declarations name the exact version; no latest-version selection**
    (§5.1, §5.2). Alternative: pin the latest `retained` version at
    publication.
15. **Tag syntax `!bwref <name>`** (§5.1). Alternatives: marked string,
    binding. All reach early parity; the tag has no opt-in, no collision and no
    stale address.
16. **Kinds string, integer, boolean, mapping; lists, floats and nulls
    refused** (§5.2, §5.3). List targets were not run.
17. **Declarations beside the fragment, `encoding` enum `{base64}`, one
    declaration per name per fragment** (§5.2). Alternative: per-occurrence
    modifiers; not evidenced.
18. **SR's re-serialization rules for identified embedded documents; JSON
    authored as YAML** (§5.4). SR calls them the experiment's; parity was shown
    only for them.
19. **Reserved text `!bwref` refused in any string** (§5.5). Catches a tag left
    in unidentified text at the cost of refusing literal look-alikes.
20. **The import base stands in for design §6.7's secret-generation resources;
    overriding its references is refused** (§6). Alternative: allow overrides,
    flagged in review.
21. **An exact literal copy of a resolved value refuses publication** (§6 step
    7). Alternative: redact by value and publish; the copy is already a
    plaintext exposure in source.
22. **SP's stand-in format and token syntax** (§8.1, §8.3). SP calls them the
    experiment's; its results were measured with them.
23. **Withhold verbatim messages from steps with boolean inputs, show verbatim
    authored literals, never persist a withheld message** (§8.3). Until a quote
    can be marked.
24. **Go machinery in process, conditional on a parity re-run** (§10.1).
    Alternative: pinned `talosctl` subprocess; keeps measured parity, moves
    plaintext across a process boundary. The deciding reason is inferred.
    Owner decision, 2026-09-27 (ginsys/bronzeward#20): in process; a failed
    re-run selects the subprocess fallback, whose channel §15 scans, without
    reopening this choice; only a failure of the fallback too returns it to
    the owner.
25. **Compile only the node's running contract minor** (§10.2). v1.12 would
    also be accepted by a v1.13 node (not tested on v1.14); one contract keeps
    paths and validation single.
26. **Record the unkeyed configuration digest of each baseline and each
    artifact's plaintext** (§2.3 step 8, §4.1, §11). Added for
    [execution and recovery §1](execution-recovery.md#1-supported-operation-and-state-values),
    which compares them with observed digests to plan, verify and adopt, and
    whose planning and observing identities cannot decrypt either. Not the
    most conservative option: an unkeyed digest of a whole configuration is
    only as unguessable as the whole configuration, and a configuration whose
    only unknown parts are low-entropy secrets is a guessing target. Owner
    decision, 2026-09-27 (ginsys/bronzeward#20): accepted for the PoC. A Talos
    configuration also holds the cluster's generated key material, which an
    attacker would need to know before the digest could test a guess; a
    configuration without such material stays an unassessed residual.
    Alternative: a keyed digest under the §4.1 key for these too, at the cost
    of a provider call per observation (execution and recovery choice
    §10.2).
27. **Keyed digests by Transit `hmac` (`sha2-256`) under a dedicated digest
    key that only ingestion identities may use, recorded as
    `transit/<key>@v<N>`** (§4.1). Alternatives: an in-process HMAC under a
    key read from KV, which brings the key into process memory and needs a
    secret read by ingestion; an HMAC under the staging key, which leaves the
    digest without a key of its own. The record could also carry the key
    version's creation time (design §7.7's key identity), at the cost of a
    metadata read per digest. Owner decision, 2026-09-30
    (ginsys/bronzeward#22).
28. **A generation's data is `{"kind": …, "value": …}`** (§2.3 step 6, §5.2):
    `kind` is one of §5.2's kinds and `value` the JSON value of that kind, an
    integer as a JSON number and a mapping as an object of scalars, so the
    compiler compares the stored kind with the declaration. Alternative: E1's
    `{"value": "<string>"}`, with the kind only in the declaration, which
    leaves §5.2's comparison nothing to compare. Owner decision, 2026-10-01
    (ginsys/bronzeward#22).
29. **A signer identity alone reads each cluster's Talos CA key from the
    provider, at a path disjoint from the secret generations, and the ingestion
    and executor identities obtain a short-lived certificate from it** (§1;
    [persistence and API choice §17.29](persistence-api.md#17-choices-for-owner-review)).
    Replaces the choice of 2026-10-02 (ginsys/bronzeward#22), by the owner
    decision of 2026-11-18 17:08 CET (ginsys/bronzeward#869). Alternatives: the
    identities reading an operator-written talosconfig (the earlier choice); a
    talosconfig file in the server's configuration, outside the provider. A
    separate identity handing a credential to both adds a process boundary
    without narrowing what the credential can do; the signer's reason is the
    owner's decision to hold the signing key, not that boundary.
30. **A draft transaction's release is checked against the claim's deadlines
    once, under the claim's row lock, not again at `COMMIT`** (§3.5). The
    lock orders the release before any abandonment. Alternative: a deferred
    check at `COMMIT` (a deferred constraint trigger comparing the expiry with
    `clock_timestamp()`), which would roll back an import whose claim was live
    when released, over a gap in which no other transaction could act on it.
31. **A fragment revision selected more than once is composed once, at its
    first position** (§6 step 1). Two profiles of one layer may pin the same
    revision. Composed twice, it would be recorded as overridden by itself
    (§8.2), against design §6.2's aim that the fragment that last set a field
    can be shown. The compiler refuses a composition naming a revision twice.
    Cost: a later profile cannot reassert a shared revision. With profiles
    `P = [A]` and `Q = [B, A]` in that order, B's value wins where A and B set
    the same field, although Q orders A after B; an intended later override
    belongs in a fragment of its own. Alternatives: refuse the selection at draft update and publication, which
    forbids sharing one fragment between two profiles of a layer; or compose it
    at its last position, which lets a repeat silently undo every fragment
    between the two.
32. **The release's Kubernetes version is the kubelet image tag** (§10.2).
    No record holds a cluster's Kubernetes version, and the kubelet image is
    the one field every machine's configuration carries (the machinery
    defaults it when unset). The tag must be a plain `vX.Y.Z` release, read
    as the machinery reads it (after the last `:v`, up to a digest), and every
    covered machine must name the same version; a release is refused
    otherwise. An image a reference placed, or one redaction would change, is
    refused rather than recorded, since the record is not secret. Only the
    kubelet's tag is recorded and checked; the control-plane images'
    tags are checked by the node at apply (its runtime validation).
    Alternatives: a Kubernetes version on the cluster record, which the
    operator would keep in step with every configuration by hand; or the
    API server image, which worker configurations do not carry.
33. **The contract must equal the machinery's minor** (§10.2). Choice §16.25
    compiles only the node's running contract, and the machinery renders a
    newer contract silently as its own, so a cluster contract other than the
    renderer's minor is refused at publication. Alternative: accept an older
    contract the machinery can still render (E3 §4.3 accepted v1.12), which
    the PoC does not test.
34. **The machinery version and checksum come from the build** (§10.2):
    the module's version and go.sum `h1:` checksum as the server binary's build
    information records them. A build that records neither, or holds a replaced
    module, refuses every publication, since its renderer cannot be named.
    Alternatives: constants kept beside `go.mod` by hand, which can drift from
    what was compiled; or the module's version alone, which does not identify
    the module's content.
35. **Review pauses the claim, which no process then owns** (§3.6): a claim
    whose review is `pending` stores its envelope and becomes `paused`, its
    lease ended; the operator's mark or continuation takes it at the next owner
    generation, and a mark refused before any provider write returns it to
    `paused` unchanged. Design §7.1 asks for review that survives the process,
    which is what encrypted staging is for. Alternatives: review inside the
    leased run (E1's own continuation, E1 5.7), which ends with its process;
    a paused claim left `held` with a lapsed lease, continued by takeover,
    which no reader can tell from a crashed run and whose takeover would skip
    the review; or abandoning the claim on every refused mark, which discards a
    review for a mistyped path. A mark run cannot read the values extracted
    before it, so it may add no text but its references (§3.6 item 3), and its
    refusals name marks by position. Alternatives: carry the extracted values
    in the envelope so the guard can search for them, which has ingestion read
    back the secrets it created, against choice §16.1; or re-run the whole
    pipeline from the exact input, which needs baseline decryption that no
    identity holds (choice §16.4).
36. **A named machine's name is its Talos hostname when nothing sets one**
    (§6 step 5a, §6.1 decisions 1 and 2). Owner direction, 2026-11-20
    (ginsys/bronzeward#976; Serge, typed 11:36 CET in the bronzeward lead's
    session): "when setting the hostname, the default could be the machine name
    (if set)". Design choices, owned by this specification and open to review:
    the compiler writes `HostnameConfig` with `auto: off` and `hostname`, in
    every platform mode; it reads the composition's hostname first, because the
    renderer's merge alone lets the last writer win (measured, §6.1); an
    explicit hostname wins; a `HostnameConfig` with `auto: off` and no
    hostname, a composition with the legacy stable-hostname feature, and a machine
    with no name get no fill. Alternatives: the name always wins; a conflict
    refusal; a per-cluster switch, off by default, which would make the default
    Serge asked for off. The cost is that every named machine the fill applies to
    (the exceptions above excluded) may change its hostname at its next release,
    where the hostname it runs differs from its name, shown in the plan's
    whole-configuration diff and needing approval, and that a filled hostname
    overrides one a DHCP or cloud source gave.
37. **A hostname equal to the machine's name is released at import and
    adoption** (§2.3 step 2a, §6.1 item 5). The document is removed from the
    stored base, whole, so the fill applies on every later release and a rename
    reaches the machine. Alternatives: keep it explicit and rely on the operator
    deleting it with a fragment, which fails the common case (an onboarded
    machine is named from its own hostname); compare the name at each
    publication and ignore an equal hostname, which leaves a second copy of the
    hostname in the stored base that a rename makes stale and a review that
    cannot tell filled from explicit. The cost is one value ingestion changes
    that is not a secret, an amendment of the import-equivalence check, and a
    release that can read as pending convergence against the baseline.
    **Extended to the legacy key** (ginsys/bronzeward#1054): a `machine.network.hostname` equal to the name is
    removed as a key, where no `HostnameConfig` is present and the
    stable-hostname guard of step 2a holds, for the same reason: the onboarded machine is named from its own
    hostname whichever field holds it, and no hand-over is measured for a key.
    Alternative: limit the claim to a document and let the review say "set in the
    configuration" for a key (client contracts A9 item 7), which is documentation
    only and leaves a renamed machine never following its name. Its removal is
    recorded as not the fill's own, so its verdict reads false at the first
    apply. **A hostname equal to the previous name is not released.**
    Alternatives: option A, compare the name the machine's last applied
    `machine-name` release used, read with the request and kept in the staged
    payload, with the stale check widened to it, the removed hostname stored
    beside the current name and the verdict true only where they are equal: the
    right fix and the larger change (a second compared value and a wider stale
    check), for a window measured once, in a fixture, and not yet outside one;
    option B, every name in the machine's `name-change` history, which removes
    an operator's hostname that merely equals an old name, with nothing showing
    the document was the fill's. Cost of the choice: after a rename and before
    its apply, a re-import keeps the previous name explicit (§6.1 item 5).
38. **Publication refuses a filled hostname another machine holds
    explicitly** (§6.1 item 6). Alternative: a review warning only. The failure
    lands on a live cluster and is not cheap to undo, so a refusal is chosen
    over a warning that leaves it to whoever approves the plan. The same choice
    covers a machine whose release gives it a node name that another machine's
    applied release holds, compared on the recorded `nodeName` (persistence and
    API §9.3) so that an explicit hostname and `registerWithFQDN` are covered
    too; the commitment refuses what moved after publication (execution and
    recovery choice §10.39). Agent decision, 2026-11-23
    (ginsys/bronzeward#1052).
39. **Publication records whether a machine's configuration differs from its
    import base in form only** (§6 step 8; persistence and API choice §17.48).
    Alternatives: a compilation that reproduces the node's text when nothing is
    added, which one later fragment defeats and which drops a normalisation the
    compilation states on purpose; a normalised form in every comparison, which
    makes Applied and drift stop meaning the bytes on the node.

## 17. Traceability

| Clause | Design | Evidence |
| --- | --- | --- |
| §1 identities | §7.7, §13.1, §13.2 | [PC §2](../design/research/20260924-provider-capability-comparison.md#2-candidates-and-identities), [PC §4.1](../design/research/20260924-provider-capability-comparison.md#41-permissions) |
| §2.1 typed boundary | §7.1 | [E1 §6](../design/research/20260922-secret-ingress-extraction-before-persistence.md#6-what-this-decides), [E1 §8](../design/research/20260922-secret-ingress-extraction-before-persistence.md#8-recommendation) item 2 |
| §2.2 addressing | §7.1 | [E1 §7](../design/research/20260922-secret-ingress-extraction-before-persistence.md#7-limits); E1 §8 item 5 |
| §2.3 pipeline, orphans | §7.1, §7.4, §7.8 | [E1 §4](../design/research/20260922-secret-ingress-extraction-before-persistence.md#4-expected-and-observed) (4.2, 4.4, 4.6); [DB §4.7](../design/research/20260924-database-semantics.md#47-s7-restored-state), [DB §9](../design/research/20260924-database-semantics.md#9-hand-off) |
| §2.4 identification limits | §6.9 | [SP §4.4](../design/research/20260925-sensitivity-provenance.md#44-controls), [SP §5](../design/research/20260925-sensitivity-provenance.md#5-what-each-failure-is), [SP §8](../design/research/20260925-sensitivity-provenance.md#8-limits); E1 4.7, [E1 §7](../design/research/20260922-secret-ingress-extraction-before-persistence.md#7-limits); [FR §8](../design/research/20260925-feasibility-evidence-review.md#8-cross-report-findings) C1 |
| §3.1 staging modes | §7.1 | E1 4.2, §6, §8 item 3; E1 5.16 |
| §3.2–§3.4 claims, lease, takeover | §7.1 | E1 4.2, 5.15, 5.16, 5.20, §7; [DB §4.4](../design/research/20260924-database-semantics.md#44-s4-ownership-transitions), [DB §4.5](../design/research/20260924-database-semantics.md#45-s5-queue-claims) row 021 |
| §3.5 restoration | §14.6 | DB §4.7; [execution and recovery §7](execution-recovery.md#7-recovery-after-management-state-restoration) |
| §3.6 operator review | §7.1, §6.9 | none: choice §16.35; E1 5.7, 4.7; [persistence and API §16](persistence-api.md#16-verification-and-evidence-limits) |
| §4.1 keyed digests | §7.1 | E1 §7, §8 item 7; choice §16.27 |
| §2.3 step 8, §4.1, §11 whole-configuration digests | §7.1, §12.1 | none: choice §16.26; [execution and recovery §1](execution-recovery.md#1-supported-operation-and-state-values) |
| §4.2 guard | §7.1, §6.9 | [E1 5.18](../design/research/20260922-secret-ingress-extraction-before-persistence.md#518-the-fourth-review-and-the-fixes-made-after-the-evidence), [E1 5.19](../design/research/20260922-secret-ingress-extraction-before-persistence.md#519-advisory-rounds-five-to-eighteen-the-prototype-hardened-the-evidence-unchanged) |
| §5.1 scope, naming, versions | §6.9, §13.2 | none: choices §16.12 to §16.14 |
| §5.1 tag syntax | §6.9 | [SR §5](../design/research/20260924-structural-reference-composition.md#5-what-each-late-failure-is), [SR §6.4](../design/research/20260924-structural-reference-composition.md#64-criterion-4-candidate-failures-and-renderer-implications), [SR §9](../design/research/20260924-structural-reference-composition.md#9-alternatives-and-decision-enabled); [SP §7.1](../design/research/20260925-sensitivity-provenance.md#71-the-binding-form-cannot-hold-a-list-element-reference); [E3 §6.2](../design/research/20260925-talos-compatibility.md#62-criterion-2-subprocess-against-machinery-and-the-structural-reference-evidence) |
| §5.2 declarations, enum | §6.9 | [SP §2](../design/research/20260925-sensitivity-provenance.md#2-candidates); SR §3.1 |
| §5.3, §5.4 targets, aliases, embedded | §6.3, §6.9 | [SR §4](../design/research/20260924-structural-reference-composition.md#4-the-matrix), SR §6.2, §6.4, [SR §8](../design/research/20260924-structural-reference-composition.md#8-limits); [SP §3.1](../design/research/20260925-sensitivity-provenance.md#31-cases), [SP §4.2](../design/research/20260925-sensitivity-provenance.md#42-provenance-per-case) |
| §5.5 reserved text | §6.9 | SR §6.4, [SR §10](../design/research/20260924-structural-reference-composition.md#10-hand-off) |
| §6 import base | §6.7, §18.2, A.3 | none: a departure recorded as choice §16.20 |
| §6 early resolution, superset | §6.2, §6.9, §7.4 | SR §4, §5, [SR §6.2](../design/research/20260924-structural-reference-composition.md#62-criterion-2-effective-and-superset-dependencies), §6.4, §9 |
| §2.3 step 2a, §6 step 5a, §6.1 hostname | none | Talos machinery v1.14.5 `config/types/network/hostname.go`, `config/generate/network.go`, `config/container/container.go`, the Talos 1.14 "Hostname" page; a scratch probe on the generated base, not kept (§6.1); none for `container` and `cloud`: choices §16.36 to §16.38 |
| §6 step 7 literal copies | §6.9 | [SP §6.3](../design/research/20260925-sensitivity-provenance.md#63-criterion-3-remaining-leakage-risks-and-dependency-records), [SP §10](../design/research/20260925-sensitivity-provenance.md#10-hand-off) |
| §7 validation stages | §6.9 | [SR §6.3](../design/research/20260924-structural-reference-composition.md#63-criterion-3-parity-validation-collisions-and-rejections) |
| §8.1 tracer, fidelity | §6.9 | [SP §6.1](../design/research/20260925-sensitivity-provenance.md#61-criterion-1-sensitivity-through-each-transformation), [SP §8](../design/research/20260925-sensitivity-provenance.md#8-limits), [SP §9](../design/research/20260925-sensitivity-provenance.md#9-alternatives-and-decision-enabled) |
| §8.2 provenance record | §6.9 | SP §6.3; [SP §4.4](../design/research/20260925-sensitivity-provenance.md#44-controls) |
| §8.3 redaction | §6.9, §15.4 | [SP §4.1](../design/research/20260925-sensitivity-provenance.md#41-which-representations-leak-a-referenced-value), [SP §6.2](../design/research/20260925-sensitivity-provenance.md#62-criterion-2-redaction-without-relying-on-value-matching), SP §6.3, SP §10; [E3 §7](../design/research/20260925-talos-compatibility.md#7-limits) |
| §9 dependency records | §6.9, §7.5, §7.8 | SR §6.2; SP §6.3; FR §8 C2; [KL §5.2](../design/research/20260924-key-loss-restoration.md#52-criterion-2-applying-is-not-regenerating-and-ciphertext-is-not-executability), [KL §7](../design/research/20260924-key-loss-restoration.md#7-recommendation) item 1 |
| §10.1 renderer selection | §6.5 | E3 §6.2, E3 §7, [E3 §8](../design/research/20260925-talos-compatibility.md#8-recommendation); FR §8 C4; [E1 §3](../design/research/20260922-secret-ingress-extraction-before-persistence.md#3-what-was-built) |
| §10.2 pinning, refusals | §6.5 | [E3 §4.1](../design/research/20260925-talos-compatibility.md#41-generation-capability), [E3 §4.2](../design/research/20260925-talos-compatibility.md#42-validation), [E3 §4.3](../design/research/20260925-talos-compatibility.md#43-operationrpc-compatibility-against-v1136), [E3 §4.4](../design/research/20260925-talos-compatibility.md#44-upstream-support-policy), E3 §8 |
| §10.3 limits, deferral | §6.5, §18.1 E3 | [E3 §6.3](../design/research/20260925-talos-compatibility.md#63-criterion-3-unsupported-combinations-and-the-deferred-lifecycle-tests), E3 §7; [FR §2](../design/research/20260925-feasibility-evidence-review.md#2-what-binds-every-conclusion-here) |
| §11 publication hand-off | §6.6, §7.4 | PC §2; [DB §4.2](../design/research/20260924-database-semantics.md#42-s2-all-or-nothing-publication) |
| §15 gaps | §18.1, §18.2 | [FR §9](../design/research/20260925-feasibility-evidence-review.md#9-gaps), [FR §10](../design/research/20260925-feasibility-evidence-review.md#10-recommendations) |
