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
the adoption workflow ([ginsys/bronzeward#22](https://github.com/ginsys/bronzeward/issues/22));
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
| Ingestion | create-only secret generations; HMAC with the digest key (§4.1); encrypt and decrypt with the staging key (§3); encrypt with the baseline key (§2.3) | secret reads; artifact and baseline decryption; machine operation | `publisher`: `create` on `secret/data/gen/*` only, and a refused replace ([PC §2](../design/research/20260924-provider-capability-comparison.md#2-candidates-and-identities), [PC §4.1](../design/research/20260924-provider-capability-comparison.md#41-permissions) rows 001, 002). Staging-key, baseline-key and HMAC use were not measured. |
| Compiler | read the pinned secret versions; encrypt with the artifact key | artifact, baseline or staging decryption; secret creation; machine operation | `compiler`: `read` on `secret/data/*`, encrypt on the artifact key, decrypt refused (PC §4.1 rows 004, 038, 040) |
| Executor | decrypt artifacts, gated by approval | secret reads; staging and baseline decryption | `executor`: decrypt only, secret read refused (PC §4.1 rows 039, 005); [execution and recovery §3.1](execution-recovery.md#31-execution-time-evidence-gathered-before-the-transaction) |
| Metadata | [design §7.6](../design/Talos_Configuration_and_Machine_Management_Design.md#76-metadata-only-dependency-checks) classification | any value | `metadata`: KV metadata and Transit key state, no data (PC §4.1) |
| Normal API | metadata and workflow | any secret value, any plaintext input | not measured; design §13.1 |

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
key, is addressable: `doc[0]/machine/nodeLabels/example.test~1role`. Every
document of the stream is addressable. E1 found both gaps in its own path
syntax ([E1 §7](../design/research/20260922-secret-ingress-extraction-before-persistence.md#7-limits);
E1 §8 item 5); this escaping scheme is chosen here and is untested
**(choice §16.2)**.

A path inside an identified embedded document (§5.4) appends `|<format>` and a
second pointer: `doc[0]/cluster/inlineManifests/0/contents|yaml/stringData/password`.

### 2.3 Pipeline

0. **Claim.** The claim row (§3.2) is created in state `held`, with no
   payload, before the input is read.
1. **Read** into the unresolved type (§2.1).
2. **Parse** every document of the stream. A parse failure refuses the input;
   the refusal quotes no input text.
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
4. **Substitute** each identified value by a reference (§5) under a newly
   minted logical name at version 1, with its declaration (§5.2), producing the
   candidate sanitized document. Names are minted as §5.1 states.
5. **Guard** (§4.2). A guard hit refuses the input; no provider object has been
   created yet.
6. **Create** one provider generation per extracted value, create-only (`cas=0`)
   at a path of its own. This takes the first branch of design §7.8 item 1
   (a path of its own, rather than an overwritten path with an explicit
   `max_versions`) **(choice §16.3)**. The path includes a component that the
   database does not issue. The reason is inferred, not measured: a restore
   rewinds database identifiers
   ([DB §4.7](../design/research/20260924-database-semantics.md#47-s7-restored-state)),
   and DB infers that a provider object named after such an identifier could
   then match a different object issued after the restore
   ([DB §9](../design/research/20260924-database-semantics.md#9-hand-off));
   with `cas=0`, such a collision is refused rather than overwritten.
7. **Construct** the sanitized value. Only now may it reach staging (§3) or the
   draft transaction.
8. For import and drift adoption, **encrypt the exact input** as the baseline
   under the baseline key, and record its keyed digest (§4.1). The baseline
   ciphertext is persisted by the draft transaction, with the draft. It is the
   only persisted form of the unextracted input. The baseline key is separate
   from the artifact key: ingestion may encrypt with it, and no identity in §1
   may decrypt with it, so the executor cannot read a baseline
   **(choice §16.4)**. Decrypting a baseline, for example to compare its bytes
   with the node's configuration, is an administrative operation outside this
   contract.

Under encrypted staging, the claim's payload is written after step 8, and not
before: one envelope holding the sanitized document, its references and, for
import and drift adoption, the baseline ciphertext, so that a taker has
everything the draft transaction persists.

What E1 measured about interruption, for its own prototype and pipeline: its
80-row screen interrupted runs at ten points and found no synthetic secret in
the run root and the live tables at that moment, and "says nothing about the
heap, the write-ahead log or the backups" (E1 4.6). The database's disk and
backup-visible surfaces were read only in the captured bundles: the honest
runs, the runs held and killed in review, the run killed inside the database
transaction, and the recovery runs (E1 4.2). This pipeline differs from E1's
(the guard runs before the provider write, digests are keyed, the baseline is
step 8), so no interruption of this pipeline has been measured; §15 requires
it.

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
reviews an import and marks further paths. A further mark re-enters the
pipeline at §2.3 step 3 on the staged document.

### 3.1 Two modes

| Mode | Where the change lives | Recovery owner | Use |
| --- | --- | --- | --- |
| Protected transient | the ingesting process's memory only; the claim row carries no payload | none | default, for every ingestion that does not pause across the lifetime of its process |
| Encrypted staging | the claim row carries the change as one envelope (document, references and any baseline ciphertext, §2.3) encrypted under a staging key | a second ingestion principal (§3.4) | only an ingestion whose review must survive its process |

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
a payload that is not a complete envelope, checks its digest, and only then
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
| `held` | Owned by the ingestion that created it, including while that ingestion continues after its own review. |
| `resumed` | Taken over by another ingestion principal (encrypted staging only). |
| `released` | The draft transaction committed. The payload is cleared to `NULL`; the row stays (E1 §6). |
| `abandoned` | Terminal without a draft: a refused input, absolute expiry, a lapsed transient claim, a takeover with nothing to decrypt, an operator's abandonment or recovery-mode entry (§3.5). The payload is cleared. |

Owner identity: for transient staging, the run identity, process id and
process start token, as in E1; for encrypted staging, the ingestion principal
and its instance. An owner's own transition (lease extension, the draft
transaction's release) checks owner and owner generation, and a takeover checks
the generation it read, each in the same conditional `UPDATE` that makes the
change. That is the shape DB §4.4 measured: a check followed by a separate
write recorded a stale attempt in its control row 018
([DB §4.4](../design/research/20260924-database-semantics.md#44-s4-ownership-transitions)).

### 3.3 Lease extension (E1 decision 3)

Only the current owner at the current owner generation may extend a lease, and
only while the lease and the absolute expiry are both still in the future
**(choice §16.7)**. A lapsed lease is never revived by its old owner; after the
lapse only a takeover (§3.4) or abandonment can change the claim. E1's
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
an owner's own continuation leaves the claim `held`, `resumed` means only taken
over, and both states can be taken over, which removes that stranded case.

A claim whose payload was never written (encrypted staging interrupted before
the end of §2.3 step 8) has nothing to decrypt: a takeover of it abandons it,
and the input is ingested again.

The generation fence is the same mechanism DB measured for queue claims, where
a worker whose lease expired had its late completion refused at the newer fence
(DB §4.5 row 021,
[DB §4.5](../design/research/20260924-database-semantics.md#45-s5-queue-claims)).
Its effects:

- the old owner's heartbeat, draft transaction and release all fail, because
  each is conditional on its own generation;
- the draft transaction writes the draft, its reference rows and the claim's
  `released` state in one transaction, so a crash inside it leaves the claim
  unreleased with its payload intact and no draft, which is E1's
  `crashed-in-db-txn` result (E1 4.2), and a successful commit leaves nothing
  to redo;
- a takeover whose decryption fails, for example with the provider unreachable,
  leaves the taker owning the claim until its lease lapses; the claim can be
  taken over again until its absolute expiry and is then abandoned.

The claim row's generation fence coordinates ingestion workers only. It does
not stop a stale process from doing work outside the database; it stops that
work from being committed.

### 3.5 Expiry, abandonment and restoration

At absolute expiry, or when a transient claim's lease lapses, the claim becomes
`abandoned`, its payload is cleared, and the input must be ingested again. An
abandoned run's provider generations remain as unused objects (§2.3).

Abandonment is evaluated at read time and written by a sweep
**(choice §16.8)**: every transition and every read treats a claim past its
absolute expiry, or a transient claim past its lease, as `abandoned`, whether
or not the row says so yet; an ingestion-identity sweep then writes the state
and clears the payload, at startup and periodically. A late sweep therefore
delays only the clearing of ciphertext, never a refusal. The sweep interval is
open.

A restore rewinds claim rows and fence generations with the rest of the
database (DB §4.7). A claim created before the current recovery epoch
([execution and recovery §7](execution-recovery.md#7-recovery-after-management-state-restoration))
is therefore never resumed or committed: recovery-mode entry abandons it, and
its input is ingested again **(choice §16.9)**.

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
§8 item 7). Which provider primitive computes the HMAC, and how digests are
compared across a rotation of its key, is not evidenced and is open (§15).

This covers digests of individual secret values. Whole-configuration digests
used to compare observed and applied state
([execution and recovery §3.2](execution-recovery.md#32-the-commitment-transaction))
are not changed by this contract; their guessability was not assessed.

### 4.2 The guard (E1 decision 4)

Before a sanitized value is constructed, the candidate document is parsed back
and checked twice against every value extracted in this run:

1. **Value comparison.** Every scalar in every document, mapping keys and
   unaddressable subtrees included, is compared as a parsed value. This catches
   copies the encoder rewrote, such as a multi-line block or an escaped string
   ([E1 5.18](../design/research/20260922-secret-ingress-extraction-before-persistence.md#518-the-fourth-review-and-the-fixes-made-after-the-evidence),
   and 5.19 for the unaddressable keys).
2. **Substring search.** Every scalar is searched for every extracted value as a
   substring. This catches a secret embedded in a larger value, such as a token
   inside a URL or a join command, which value comparison cannot see.

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
  alternative, deferred until the digest mechanism is evidenced
  **(choice §16.13)**.
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
  pinned secret version; a mismatch refuses compilation and nothing is coerced.
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
   order within a layer.
2. **Pin** every declared reference in the import base and in every selected
   fragment, including references a later fragment will override: record the
   declared version (§5.1) together with the provider object it names. This is
   the source superset; resolving early necessarily reads all of it (SR §6.2,
   §6.4).
3. **Check dependencies**: every pinned secret version must classify `retained`
   (design §7.6), and the compiler identity must read it at the point of use.
   `blocked`, `lost` or `unknown` refuses publication.
4. **Resolve** the import base and each fragment with a tag-preserving parser,
   replacing each tagged node by its typed value (with its encoding) before any
   typed decode, and each identified embedded document as in §5.4. Every tag
   must resolve; there is no partial result.
5. **Compose** the reference-free fragments onto the resolved import base with
   the selected renderer (§10) in the order of step 1, using native
   strategic-merge semantics: last writer wins, and `$patch: delete` removes.
   The compiler performs no merge of its own (design §6.9). JSON6902 patches
   are refused: they were not tested and are refused for the v1.13
   multi-document configuration (SR §8).
6. **Trace** the composition for provenance (§8.1).
7. **Check the output**: no string scalar holds reserved text (§5.5), and no
   resolved `string` value of six bytes or more, in its stored or placed form,
   appears as an exact copy at an output leaf that provenance does not attribute
   to its reference; and no ordinary fragment overrode a reference of the
   import base (choice §16.20). A copy is refused **(choice §16.21)** and
   reported by path only, as a source-side exposure: the value was already
   written in plaintext into a fragment, and its remedy is to mark that literal, which extracts it to a new
   generation; whether to rotate the secret is the operator's decision. SP's
   duplicate-literal case is the evidence that only value matching sees such a
   copy ([SP §6.3](../design/research/20260925-sensitivity-provenance.md#63-criterion-3-remaining-leakage-risks-and-dependency-records));
   the six-byte floor is SP's value matcher's (SP §2).
8. **Validate** the complete materialized configuration with the pinned renderer
   in the node's platform mode (§7 stage 3).

The compiler's only transformations of native YAML are step 4's replacement of
tagged nodes and re-serialization of identified embedded documents. Everything
else about the output is the renderer's, which is what "preserving native merge
semantics" means here. Overrides, aliases, list changes and multi-document
streams are the renderer's behaviour, observed by §8.1, never modelled.

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

The trace and flip compositions carry each value's length and character
classes, so they are handled like the real composition: never persisted, logged
or shown.

**Fidelity fails closed.** Publication is refused when any resolved real
fragment lacks its trace or flip counterpart, when the real and trace
compositions differ in tree shape, or when one pass composes or validates and
the other does not. SP's prototype skipped a fragment whose trace was missing,
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
| source | import base or fragment revision, its SHA-256, source document and path |
| outcome | exactly one of: output document and path (one row per path, so an alias has two); or the fragment that overrode it |

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
| Composed path | every leaf §8.1 attributes to a reference | `<redacted:REF@VERSION>`, and `<redacted:REF@VERSION#leaf>` for one leaf of a mapping reference |
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

- **Paired diffs.** Where a diff shows a base leaf beside a redacted output
  leaf, the base side is redacted as `<redacted:paired>`, whatever its kind; a
  boolean could otherwise be read by elimination (SP §2, §4.4).
- **Messages.** A renderer message is redacted by template: the same step on
  the trace pass gives a message whose stand-in quotes mark where the real one
  quotes a value, and each is replaced by its token. A message the template
  cannot explain, or a step that succeeded in one pass and failed in the other,
  is **withheld** (SP §2, §6.2). Decode errors quote a seven-byte prefix of a
  wrongly typed value (SR §8), which the template covers (SP §5).
- **Verbatim messages from boolean inputs are withheld.** A boolean's stand-in
  is its own value, so a message quoting it matches its trace message and
  nothing marks the quote (SP §6.3). Until a way to mark such a quote exists, a
  message that the template reports verbatim is withheld when the step's input
  holds a boolean reference **(choice §16.23)**.
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
  notice naming the step; its text is not stored, logged or put into a support
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
  source needs.

A reproduction dependency can outlive every effective one: an overridden
reference is absent from the artifact but still needed to re-render it
(SP §6.3, override-literal and delete). Applying a retained artifact needs the
artifact, its encryption key version and the executor's credentials, not the
source secrets; regenerating needs every reproduction dependency
([KL §5.2](../design/research/20260924-key-loss-restoration.md#52-criterion-2-applying-is-not-regenerating-and-ciphertext-is-not-executability)).

The artifact record also carries its encryption dependency, the Transit key by
an identity the provider cannot reissue together with its version, not by name
and version alone ([KL §7](../design/research/20260924-key-loss-restoration.md#7-recommendation)
item 1, inferred), and the renderer and contract record of §10.2.

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
do not, this selection is reopened; the alternative is the pinned `talosctl`
subprocess, with plaintext passed only through inherited descriptors, never a
named file or an argument. That channel's exposure is unmeasured too, and it
would need the same leak scan.

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
  (E3 §4.2). For Talos 1.13 the machinery accepts Kubernetes 1.31 to 1.36, which
  equals the documented range (E3 §4.4); the machinery's windows are wider than
  the documented tested path elsewhere, and a stricter support policy is a
  separate decision (E3 §8).
- **Record the contract, not only the renderer.** The release records the
  target contract, the machinery module version and its checksum, the Kubernetes
  version and the validation mode. A renderer version alone does not say whether
  a node can decode the output (E3 §8).
- **Paths are versioned with the contract.** The v1.14 layout moves many
  v1alpha1 paths into separate documents (E3 §4.1), so marks, declarations and
  embedded identifications hold for the contract their fragment revision was
  written for. A contract change is a new fragment revision that passes stage 1
  again; paths are never translated automatically.

### 10.3 Compatibility limits and the deferral

Evidenced: generation, validation and the PoC's configuration RPCs (version
read, configuration read, no-reboot dry run and a label-patch apply) against one
live v1.13.6 node, in containers, with one control plane and one worker (E3 §7).
Not evidenced: any other live version, reboot-mode applies, a JSON6902 patch,
composition on renderers other than v1.13.6, worker configurations in SR and
SP, and every upgrade path.

Unsupported, with the observed reason (E3 §6.3): a contract newer than the
renderer (silently clamped); v1.14+ output on a pre-v1.14 validator or node
(`DiscoveryServiceConfig` not registered); v1.10/v1.11 contracts on a v1.13 node
in no-reboot mode (refused in immediate mode; that the mode is the cause is
inferred); an unparseable version string; Kubernetes outside the window; targets
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
Step 4 is persistence's: the compiler hands over, as one unit, only sanitized
or encrypted values:

- release metadata: the import base, source and assignment revisions, the
  renderer and contract record (§10.2), the stage 3 result;
- per machine: the artifact ciphertext and its digest, the redacted review
  data, the provenance records (§8.2) and both dependency records with the
  encryption dependency (§9).

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

1. The operator imports a worker, choosing encrypted staging because the review
   must survive the process, and marks `doc[0]/machine/files/0/content`.
   Ingestion identifies the bundle's secret fields and the marked path, the guard
   passes, provider generations are created, and the sanitized change is staged
   encrypted with a claim `held` by ingestion instance A.
2. A is killed during review. Its lease lapses; the absolute expiry has not.
3. The operator requests recovery. Instance B takes the claim over (`resumed`,
   owner generation 2), decrypts the envelope, checks its digest and continues
   the review; the guard is not re-run (§3.1).
4. A, restarted with its old state, sends a heartbeat at generation 1; it is
   refused. B's draft transaction commits the draft, its reference rows and
   `released` together.
5. Had the provider been unreachable at step 3, B's decryption would fail, the
   claim would stay `resumed` under B until B's lease lapsed, and a later
   takeover could retry until the absolute expiry, after which the claim is
   abandoned and the worker is imported again.

### 12.4 A rejected draft update

1. An operator submits a draft update that adds a node label holding a token
   and a machine file whose script embeds the same token in a command line. The
   operator marks the label, `doc[0]/machine/nodeLabels/example.test~1token`
   (§2.2 escaping); the file content is not marked, and the schema does not
   cover it (§2.4).
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
   the review data, the provenance and both dependency records. Persistence
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
| Ingestion | Crash inside the draft transaction | Claim unreleased with payload; no draft | Claim row with ciphertext (encrypted) |
| Ingestion | Stale owner heartbeat, commit or release | Refused by owner generation | Nothing |
| Ingestion | Absolute expiry, or recovery-mode entry | Claim abandoned; re-ingest | Claim row, payload cleared |
| Authoring | Undeclared or unused name, other local tag, tag on a key or sequence, reserved text, bad path | Draft update refused | Nothing |
| Compilation | Dependency not `retained`, or unreadable by the compiler | Publication refused | Nothing |
| Compilation | Kind mismatch, or `base64` on a non-string | Publication refused | Nothing |
| Compilation | A reference to a secret of another cluster (§5.1) | Publication refused | Nothing |
| Compilation | An ordinary fragment overrides or deletes an import-base reference (§6) | Publication refused, naming the fragment and path | Nothing |
| Compilation | Contract newer than renderer or not the node's minor; Kubernetes outside the window | Publication refused | Nothing |
| Compilation | Native composition or validation rejects | Publication refused; message redacted or withheld (§8.3) | Nothing |
| Compilation | Trace or flip fragment missing, shape differs, or passes disagree | Publication refused (§8.1) | Nothing |
| Compilation | Reserved text or an unattributed exact copy in the output | Publication refused, naming paths | Nothing |
| Compilation | Encryption fails, or persistence rejects a stale input | Publication refused | Nothing of the release |

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
8. **No unkeyed secret digest is persisted.**
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
- the SR and SP matrices through the compiler's own composition path (§10.1),
  with references in the import base as well as in fragments, and SP's oracle
  over the PoC's own log and support formats (§8.4);
- a fidelity check that fires on an injected structural change (§8.1);
- every refusal in §13.

Evidence gaps this contract carries rather than closes:

**Ingestion and staging**

- **Interruption of this pipeline**: never measured. E1's screen covered the
  run root and live tables only, and backup-visible surfaces were read only in
  its captured bundles, for a different pipeline (§2.3; E1 4.2, 4.6).
- **Schema detector coverage**: `schema-covers-base-secrets` was never seen to
  fail and ran on control-plane bases only; disk-encryption, installer and disk
  configuration were absent from the environment; the list's precision was not
  measured (§2.4; SP §4.4, §8; E1 §7).
- **Keyed-digest mechanism**: no provider HMAC primitive was exercised, and
  comparison across its key rotation is undesigned (§4.1).
- **Claim contention**: E1 ran one process against one claim; no two
  principals raced, and no clock was skewed (E1 §7). Lease, expiry and
  heartbeat values are open (§3.2).
- **Identities**: PC measured the `publisher` create-only policy and the
  compiler and executor policies, not staging-key, baseline-key or HMAC use
  (§1).
- **Addressing**: the JSON Pointer scheme (§2.2) is untested.
- **Ingestion input**: only configurations read back from a node were
  ingested; a generated configuration before Talos normalizes it was not (E1 §7).

**Composition, provenance and redaction**

- **Machinery composition parity**: inferred only (FR §8 C4); the §10.1
  condition. References inside a base were never resolved: SR's and SP's bases
  held literal secrets (§6).
- **Kinds and cases not run**: durations, IP and CIDR fields, a list as a
  target, list-element overrides by selector, more than two fragments, worker
  configurations, a tag name that is valid base64 or a YAML 1.1 boolean word,
  and a hand-formatted embedded literal (SR §8; SP §8).
- **Fidelity control**: a one-mutation smoke test that never injected a
  structural change, not exercised in 16 of 56 cells (SP §4.4, §8).
- **Paired-diff control**: loose; it shows the base text is present in the
  per-side diff, not that it sits on a removed line (SP §8).
- **Messages**: a quoted boolean cannot be marked, so such messages are
  withheld; an authored literal copying a short, integer or boolean secret is
  not detected; withheld messages reach no support bundle (§8.3, §8.4).
- **Tracer limits no case reached**: a string line under five bytes carries no
  id, an integer stand-in can equal a literal integer, and a validation rule can
  judge a stand-in differently from its value (SP §8); the last is refused under
  §8.1.
- **Embedded formatting**: an author's formatting is not preserved (§5.4).

**Renderer and environment**

- **Plaintext boundary**: neither the in-process path nor a subprocess channel
  was scanned for exposure; the deciding reason in §10.1 is inferred.
- **Leak detection**: exact copies only; the OpenBao storage volume is not
  observable, and Transit plaintext crossed loopback without TLS in the fixture
  (E1 §7).
- **Validation mode**: SR and SP validated the fixture base in container mode;
  metal mode only on generated bases (SR §8).

The Upgrade/LifecycleClient deferral in §10.3 stands: this contract does not
claim full E3, upgrade support or lifecycle execution.

## 16. Choices for owner review

Each is the most conservative option consistent with the design where the
evidence does not settle the question. Each is marked in place as
**(choice §16.n)**.

1. **Ingestion and compiler as two identities** (§1). Alternative: design
   §13.2's single role. Input to identity and approval policy, which owns the
   identities.
2. **RFC 6901 addressing with `doc[n]`** (§2.2). Untested; any scheme that
   escapes `.` and `[` would meet E1's finding.
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
    Alternative: deduplicate by keyed digest; avoids churn, but rests on the
    unevidenced digest mechanism.
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
25. **Compile only the node's running contract minor** (§10.2). v1.12 would
    also be accepted by a v1.13 node; one contract keeps paths and validation
    single.

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
| §4.1 keyed digests | §7.1 | E1 §7, §8 item 7 |
| §4.2 guard | §7.1, §6.9 | [E1 5.18](../design/research/20260922-secret-ingress-extraction-before-persistence.md#518-the-fourth-review-and-the-fixes-made-after-the-evidence), [E1 5.19](../design/research/20260922-secret-ingress-extraction-before-persistence.md#519-advisory-rounds-five-to-eighteen-the-prototype-hardened-the-evidence-unchanged) |
| §5.1 scope, naming, versions | §6.9, §13.2 | none: choices §16.12 to §16.14 |
| §5.1 tag syntax | §6.9 | [SR §5](../design/research/20260924-structural-reference-composition.md#5-what-each-late-failure-is), [SR §6.4](../design/research/20260924-structural-reference-composition.md#64-criterion-4-candidate-failures-and-renderer-implications), [SR §9](../design/research/20260924-structural-reference-composition.md#9-alternatives-and-decision-enabled); [SP §7.1](../design/research/20260925-sensitivity-provenance.md#71-the-binding-form-cannot-hold-a-list-element-reference); [E3 §6.2](../design/research/20260925-talos-compatibility.md#62-criterion-2-subprocess-against-machinery-and-the-structural-reference-evidence) |
| §5.2 declarations, enum | §6.9 | [SP §2](../design/research/20260925-sensitivity-provenance.md#2-candidates); SR §3.1 |
| §5.3, §5.4 targets, aliases, embedded | §6.3, §6.9 | [SR §4](../design/research/20260924-structural-reference-composition.md#4-the-matrix), SR §6.2, §6.4, [SR §8](../design/research/20260924-structural-reference-composition.md#8-limits); [SP §3.1](../design/research/20260925-sensitivity-provenance.md#31-cases), [SP §4.2](../design/research/20260925-sensitivity-provenance.md#42-provenance-per-case) |
| §5.5 reserved text | §6.9 | SR §6.4, [SR §10](../design/research/20260924-structural-reference-composition.md#10-hand-off) |
| §6 import base | §6.7, §18.2, A.3 | none: a departure recorded as choice §16.20 |
| §6 early resolution, superset | §6.2, §6.9, §7.4 | SR §4, §5, [SR §6.2](../design/research/20260924-structural-reference-composition.md#62-criterion-2-effective-and-superset-dependencies), §6.4, §9 |
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
