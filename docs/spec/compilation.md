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

Every result in those reports holds for one Talos version (v1.13.6), strategic
merge patches only, container nodes and exact-copy leak scanning
([FR §2](../design/research/20260925-feasibility-evidence-review.md#2-what-binds-every-conclusion-here)).
This contract inherits those limits; §15 lists them.

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

The design's "privileged ingestion/compiler" role is split into two
identities, so that the one that creates secrets never reads them:

| Identity | Needs | Must not have | Evidence for the split |
| --- | --- | --- | --- |
| Ingestion | create-only secret generations; encrypt and decrypt with the staging key (§3); encrypt with the artifact key, for the baseline (§2.3) | secret reads; artifact decryption; machine operation | PC `publisher`: create on its generation paths only ([PC §2](../design/research/20260924-provider-capability-comparison.md#2-candidates-and-identities)) |
| Compiler | read the pinned secret versions; encrypt with the artifact key | artifact or staging decryption; secret creation; machine operation | PC `compiler`: read on secret data, encrypt on the artifact key (PC §2) |
| Executor | decrypt artifacts, gated by approval | secret reads; staging decryption | PC `executor`; [execution and recovery §3.1](execution-recovery.md#31-execution-time-evidence-gathered-before-the-transaction) |
| Metadata | §7.6 classification | any value | PC `metadata` |
| Normal API | metadata and workflow | any secret value, any plaintext input | design §13.1 |

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
E1 §8 item 5); this escaping scheme is chosen here and is untested.

A path inside an identified embedded document (§5.4) appends `|<format>` and a
second pointer: `doc[0]/cluster/inlineManifests/0/contents|yaml/stringData/password`.

### 2.3 Pipeline

1. **Read** into the unresolved type (§2.1).
2. **Parse** every document of the stream. A parse failure refuses the input;
   the refusal quotes no input text.
3. **Identify** the values to extract: every field the pinned Talos machinery
   marks secret (the `pkg/machinery` `RedactSecrets` field list, SP §2), and
   every path the operator marks in the request. A mark that addresses no node
   refuses the input before any provider write, as E1's rejected rows were
   ([E1 §4](../design/research/20260922-secret-ingress-extraction-before-persistence.md#4-expected-and-observed),
   4.6). Over-extraction is accepted: E1's detector extracted `cluster.id`,
   which is not a secret, and that is a cost, not a leak (E1 4.7).
4. **Substitute** each identified value by a reference (§5) with a new logical
   name, version and declaration, producing the candidate sanitized document.
5. **Guard** (§4.2). A guard hit refuses the input; no provider object has been
   created yet.
6. **Create** one provider generation per extracted value, create-only (`cas=0`)
   at a path of its own, which also satisfies design §7.8's rule never to write
   a path past its `max_versions`. The path includes a component that the
   database does not issue, because a restore rewinds database identifiers
   ([DB §4.7](../design/research/20260924-database-semantics.md#47-s7-restored-state))
   and a path derived from one alone could collide after a restore; with
   `cas=0` such a collision is refused rather than overwritten.
7. **Construct** the sanitized value. Only now may it reach staging (§3) or the
   draft transaction.
8. For import and drift adoption, **encrypt the exact input** as a baseline
   artifact under the artifact rules of §11, and record its digest (§4.1). The
   baseline is the only persisted form of the unextracted input.

An interruption at any step leaves no plaintext on a backup-visible surface,
which is what E1's 80-row screen, interrupting runs at ten points, and its
crashed and held bundles measured for its prototype (E1 4.2, 4.6). It can leave provider generations
that no draft references (step 6 done, step 7 not). They are unused provider
objects under [design §7.4](../design/Talos_Configuration_and_Machine_Management_Design.md#74-publication-without-distributed-transactions);
the PoC deletes none of them (§7.8).

### 2.4 What identification does not promise

Schema identification covers Talos-typed secret fields only. E1 measured recall
9/9 and precision 9/10 on the fields its environment produced, and a secret
placed at `machine.files[].content` was missed until the operator marked it
(E1 4.7). Talos exposes no field-level sensitivity metadata through its API
(E1 4.7); the library's field list exists but covers only Talos-typed secret
fields ([FR §8](../design/research/20260925-feasibility-evidence-review.md#8-cross-report-findings)
C1). Operator marking is therefore load-bearing, not a fallback, and an
unmarked value outside those fields carries no promise of detection (design
§6.9; E1 §8 item 4).

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
| Encrypted staging | the claim row carries the change as one envelope (document and references) encrypted under a staging key | a second ingestion principal (§3.4) | only an ingestion whose review must survive its process |

The difference is a recovery owner, not secrecy: both keep plaintext out of
ordinary persistence (E1 §6). Transient staging has no recovery owner by
construction; the only correct answer to its interruption is to abandon the
claim and ingest again. Encrypted staging recovers through a second principal
at the price of a total provider dependency at recovery: with the provider
unreachable the recovery fails and the claim stays held (E1 4.2, §6, §8 item 3).

The staging key is separate from the artifact key, and only ingestion
identities can decrypt it, so the executor, which can decrypt artifacts, cannot
read staged changes. A resume decrypts the envelope, refuses a payload that is
not a complete envelope, checks its digest, and only then constructs the
sanitized value through the ingestion package, as E1's resume path did (E1 §3).
It cannot re-run the guard: the extracted values are in the provider, and the
ingestion identity cannot read them. E1 found a recovery that silently dropped
the references because only the document was staged (E1 5.16).

### 3.2 Claim states and timers

A claim row exists for every ingestion in either mode, with a state, an owner,
an owner generation and two server-clock times: a **lease** that only its
owner can extend, and an **absolute expiry** fixed when the claim is created
and never extended. Both are evaluated by the database clock, not the caller's
(E1 5.15).

| State | Meaning |
| --- | --- |
| `held` | Owned by the ingestion that created it. |
| `resumed` | Taken over by another ingestion principal (encrypted staging only). |
| `released` | The draft transaction committed. The payload is cleared to `NULL`; the row stays (E1 §6). |
| `abandoned` | Terminal without a draft: absolute expiry, a lapsed transient claim, an operator's abandonment or recovery-mode entry (§3.5). The payload is cleared. |

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
only while the lease and the absolute expiry are both still in the future. A
lapsed lease is never revived by its old owner; after the lapse only a takeover
(§3.4) or abandonment can change the claim. E1's prototype extended any live
`held` claim for any caller and left the question to this contract (E1 5.16,
§7).

### 3.4 Takeover (E1 decision 2)

A claim is taken over only when all of these hold, in one conditional `UPDATE`
that sets the new owner and increments the owner generation:

- the claim is under encrypted staging and in state `held` or `resumed`;
- its lease has lapsed and its absolute expiry has not;
- it was created in the current recovery epoch (§3.5);
- the request comes from an ingestion principal, on an explicit operator
  recovery request recorded with the operator's identity.

The new state is `resumed`. Accepting `resumed` claims is what removes E1's
stranded case: its `Resume` accepted only `held`, so a claim whose taker
crashed stayed `resumed` with its ciphertext and no one could take it over
(E1 4.2, §7). The generation fence is the same mechanism DB measured for queue
claims, where a worker whose lease expired had its late completion refused at
the newer fence (DB §4.5 row 021,
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

A restore rewinds claim rows and fence generations with the rest of the
database (DB §4.7). A claim created before the current recovery epoch
([execution and recovery §7](execution-recovery.md#7-recovery-after-management-state-restoration))
is therefore never resumed or committed: recovery-mode entry abandons it, and
its input is ingested again.

## 4. Correlation digests and the extraction guard

Design: [§7.1](../design/Talos_Configuration_and_Machine_Management_Design.md#71-responsibility-split-and-secret-ingress).

### 4.1 Keyed digests (E1 decision 1)

Every persisted correlator of a secret value (journal records, reference rows,
the baseline's per-value records) is HMAC-SHA-256 under a key held by the
provider, not by the database or its backups. The record names the key's
identity and version. An unkeyed digest of a secret value is never persisted.

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

Both checks skip the scalar content of this run's `!bwref` nodes, which is a
reference name, as E1 skipped the references it substituted (E1 5.19).

v1 keeps both. A false refusal leaks nothing; a false pass persists plaintext
(E1 5.19). The refusal names the matching paths and the rule, never the value.
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
or digit. The grammar fixes only the lexical form; how a logical name maps to a
provider path is provider layout, which stays separate and open (design §7.3). A reference resolves to one exact version, pinned at
publication (§6).

The tag is chosen over the opted-in marked string and the external path binding.
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
  its refill moves a key to the end of its mapping, which changes the bytes of an
  embedded YAML document for any key that is not last (SR §6.4); and a
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
  registry/example-pass: {kind: string}
  pki/extra-ca: {kind: string, encoding: base64}
  app/db-pass: {kind: string}
embedded:
  - {path: "doc[0]/cluster/inlineManifests/0/contents", format: yaml}
```

- `kind` is one of `string`, `integer`, `boolean` or `mapping` (a mapping of
  those scalar kinds). It must equal the kind stored in the pinned secret
  version; a mismatch refuses compilation and nothing is coerced.
- `encoding` is optional and comes from a closed enum. The PoC enum has one
  member, `base64`, which places the standard base64 encoding of a `string`
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
  integer, boolean, base64-bytes, string and whole-mapping targets, a list
  element and a second document (SR §3.1, §4).
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
tagged value is not JSON (SR §6.4). Parity was shown only for documents whose
literal form was derived through the same encoders, and for embedded YAML only
with the reference at the last key of its mapping (SR §6.4, §8).

**Unidentified embedded text** is opaque: nothing resolves inside it, and a
secret in it must be marked as the whole scalar (§4.2).

### 5.5 Reserved text

A string scalar that contains the text `!bwref` is refused, at authoring (§7
stage 1) and again in the composed output (§6 step 7). A tag inside
unidentified embedded text is such a string, and SR shows it would otherwise be
left in the output with validation passing (SR §6.4). The cost is that a literal
look-alike, which YAML would keep literal, is refused too.

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

For each machine:

1. **Snapshot** the source and assignment revisions and order the fragments by
   the fixed layers of design §6.2 and their explicit order within a layer.
2. **Pin** a version for every declared reference in every selected fragment,
   including references a later fragment will override. This is the source
   superset; resolving early necessarily reads all of it (SR §6.2, §6.4).
3. **Check dependencies**: every pinned secret version must classify `retained`
   (design §7.6), and the compiler identity must read it at the point of use.
   `blocked`, `lost` or `unknown` refuses publication.
4. **Resolve** each fragment with a tag-preserving parser, replacing each tagged
   node by its typed value (with its encoding) before any typed decode, and each
   identified embedded document as in §5.4. Every tag must resolve; there is no
   partial result.
5. **Compose** the reference-free fragments with the selected renderer (§10) in
   the order of step 1, using native strategic-merge semantics: last writer
   wins, and `$patch: delete` removes. The compiler performs no merge of its own
   (design §6.9). JSON6902 patches are refused: they were not tested and are
   refused for the v1.13 multi-document configuration (SR §8).
6. **Trace** the composition for provenance (§8.1).
7. **Check the output**: no string scalar holds reserved text (§5.5), and no
   resolved `string` value of six bytes or more, in its stored or placed form,
   appears as an exact copy at an output leaf that provenance does not attribute
   to its reference. A copy is refused and reported by path only, as a
   source-side exposure: the value was already written in plaintext into a
   fragment, and its remedy is to mark that literal, which extracts it to a new
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
| 1. Authoring | YAML syntax of every document; tags only as §5.3 allows; every tag declared and every declaration used; name grammar; `kind` and `encoding` values; embedded identifications; reserved text (§5.5); paths well-formed (§2.2) | The fragment is well-formed source. A draft with references is not a validated native configuration. | The draft update is refused, naming paths |
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
the same renderer and the same fragment order:

- a **trace pass**, in which every resolved value is replaced by a
  shape-preserving stand-in (letters become `x`, digits `0`, other bytes stay; an
  integer gets a reserved stand-in), so that every output leaf holding a
  stand-in is attributed to its reference occurrence;
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

### 8.2 The provenance record

Recorded per reference occurrence at resolution and completed after
composition (SP §6.3):

| Field | Content |
| --- | --- |
| reference, version | logical name and pinned version |
| encoding | the declared modifier, if any |
| source | fragment revision, its SHA-256, source document and path |
| outcome | exactly one of: output document and path (one row per path, so an alias has two); or the fragment that overrode it |

SP's third outcome, `unresolved`, cannot occur in a published release, because
every tag must resolve (§6 step 4) and a tag inside unidentified text is
refused (§5.5).

Provenance is recomputed for every composition that is shown. Reusing another
revision's paths leaked a moved value, and reusing its values leaked a rotated
one (SP §4.4, §5).

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
cases on either base; composed paths alone leaked base secrets in 26 cells per base, the
schema alone referenced values in 15, and value matching alone 8
([SP §4.1](../design/research/20260925-sensitivity-provenance.md#41-which-representations-leak-a-referenced-value)).
A token names the reference and version, so a rotation shows as
`<redacted:reg-pass@1>` becoming `<redacted:reg-pass@2>` without either value.

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
  holds a boolean reference.
- **Withheld means not persisted.** A withheld message is replaced by a fixed
  notice naming the step; its text is not stored, logged or put into a support
  bundle, consistent with design §15.4.
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
  source fragments, with the fragment revision and its digest, overridden ones
  included. This is what rendering the same release again from
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

The PoC compiler generates, composes and validates through the Talos Go
machinery (`pkg/machinery`) linked into the protected compilation process, not
by running `talosctl` as a subprocess.

Within E3's matrix the two are the same: identical generation in 132 of 132
rows, the same validation verdict in all 2016 cells, the same RPC outcomes in
12 of 12 client rows and 42 of 42 contract rows (E3 §4.3, §6.2). The choice is
packaging, not compatibility (E3 §8). On packaging:

- **The plaintext boundary.** Early resolution hands the renderer fragments
  that hold resolved secret values, and generating a base configuration needs
  the secrets bundle. A subprocess receives them as files or as arguments. A
  command-line argument is readable by other local processes, and a temporary
  file is one of the surfaces design §7.1 names and E1 had to control for
  ([E1 §3](../design/research/20260922-secret-ingress-extraction-before-persistence.md#3-what-was-built),
  4.3). In process, they stay in the memory of the one process that already
  holds them. This is the deciding reason.
- **The machinery is needed anyway.** Schema redaction uses its secret-field
  list (SP §2), and only its `compatibility` package refuses a Kubernetes
  version outside a target's window (E3 §6.3 item 5).
- **Costs accepted.** `talosctl validate`'s mode type is under the talos
  module's `internal/` tree, so the compiler implements the machinery's runtime
  mode interface itself, as E3's prototype did; `secrets.Bundle.Validate`
  changed signature in v1.14, so a second contract minor needs a shim and a
  second build (E3 §6.2). While a deployment targets one minor, neither is
  needed. A second minor means a second binary in either
  implementation (E3 §8), and then the plaintext boundary returns between the
  builds; that is outside the PoC.

**Condition on the selection.** SR's and SP's parity and provenance results were
measured through `talosctl machineconfig patch`. Composition through the
machinery's `configpatcher` is inferred, not measured (E3 §6.2; FR §8 C4). Before
the compiler is accepted, the SR and SP matrices must be re-run with composition
through the compiler's own machinery path and reach the same verdicts. If they
do not, this selection is reopened; the alternative is the pinned `talosctl`
subprocess, with plaintext passed only through inherited descriptors, never a
named file or an argument, which would then need its own leak evidence.

The executor's Talos client is not selected here. E3 shows the same RPC
outcomes through both implementations (E3 §4.3); the choice belongs to
[execution and recovery](execution-recovery.md).

### 10.2 Pinning and refusals

- **Pin to the node's minor.** The machinery module's minor equals the target
  node's running Talos minor. In E3, a v1.13 renderer's output was accepted by
  the v1.13.6 node and a v1.14 renderer's was refused (E3 §8).
- **The PoC compiles only the node's running contract minor.** A v1.12 contract
  was also accepted, but v1.10 and v1.11 contracts were refused in immediate
  mode (E3 §4.3), and the PoC applies in `no-reboot` mode only.
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
key with the compiler identity, which can encrypt but not decrypt (PC §2). Step 4
is persistence's: the compiler hands over, as one unit, only sanitized or
encrypted values:

- release metadata: source and assignment revisions, the renderer and contract
  record (§10.2), the stage 3 result;
- per machine: the artifact ciphertext and its digest, the redacted review
  data, the provenance records (§8.2) and both dependency records with the
  encryption dependency (§9).

The persistence contract commits them atomically and rejects stale inputs
([ginsys/bronzeward#18](https://github.com/ginsys/bronzeward/issues/18)). A failure
before that commit persists nothing of the release: the ciphertext is held only
by the compiler until the commit, and the secret generations pinned in step 2
already existed. Step 5
is unchanged: publication makes a release available for planning and approval
and never authorizes dispatch (execution and recovery §2).

The interface types hold no plaintext. The plaintext configuration exists only
inside the compilation process between §6 step 4 and encryption.

## 12. Worked examples

### 12.1 An overridden reference

Fragment 1 (cluster layer) sets `password: !bwref registry/example-pass`;
fragment 2 (cluster-machine override) sets `password: example-override` at the
same path. Both are resolved (step 4), native composition keeps fragment 2's
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
`{kind: boolean}` is attributed by its flip pass; a diff against a base with
`wipe: false` shows `-wipe: <redacted:paired>` beside
`+wipe: <redacted:install/wipe@1>`.

### 12.3 An interrupted, reviewed import under encrypted staging

1. The operator imports a worker and marks `doc[0]/machine/files/0/content`.
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

## 13. Failure and rejection cases

| Stage | Condition | Outcome | Persisted |
| --- | --- | --- | --- |
| Ingestion | Parse failure, or a mark addressing no node | Refused before any provider write | Nothing |
| Ingestion | Guard hit (§4.2) | Refused, naming paths | Nothing |
| Ingestion | Provider write fails part-way | Refused | Unused provider generations only |
| Ingestion | Crash before staging or the draft transaction | Transient: claim abandoned at lease lapse; encrypted: takeover (§3.4) | No plaintext; unused generations; claim row |
| Ingestion | Crash inside the draft transaction | Claim unreleased with payload; no draft | Claim row with ciphertext (encrypted) |
| Ingestion | Stale owner heartbeat, commit or release | Refused by owner generation | Nothing |
| Ingestion | Absolute expiry, or recovery-mode entry | Claim abandoned; re-ingest | Claim row, payload cleared |
| Authoring | Undeclared or unused name, other local tag, tag on a key or sequence, reserved text, bad path | Draft update refused | Nothing |
| Compilation | Dependency not `retained`, or unreadable by the compiler | Publication refused | Nothing |
| Compilation | Kind mismatch, or `base64` on a non-string | Publication refused | Nothing |
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
  and a draft update, scanning drafts, indexes, logs, temporary files, staging,
  the write-ahead log and backups, as E1 did (E1 §4);
- lease extension refused to a non-owner; takeover of `held` and `resumed`
  claims only after lease lapse; a stale owner refused after takeover; a crash
  inside the draft transaction; recovery with the provider unreachable;
- the SR and SP matrices through the compiler's own composition path (§10.1),
  and SP's oracle over the PoC's own log and support formats (§8.4);
- every refusal in §13.

Evidence gaps this contract carries rather than closes:

- **Keyed-digest mechanism**: no provider HMAC primitive was exercised, and
  comparison across its key rotation is undesigned (§4.1).
- **Machinery composition parity**: inferred only (FR §8 C4); the §10.1
  condition.
- **Claim contention**: E1 ran one process against one claim; no two
  principals raced, and no clock was skewed (E1 §7).
- **Addressing**: the JSON Pointer scheme (§2.2) is untested.
- **Kinds and cases not run**: durations, IP and CIDR fields, a list as a
  target, list-element overrides by selector, more than two fragments, worker
  configurations, a tag name that is valid base64 or a YAML 1.1 boolean word,
  and a hand-formatted embedded literal (SR §8; SP §8).
- **Messages**: a quoted boolean or authored literal cannot be marked (§8.3
  withholds rather than marks); withheld messages reach no support bundle.
- **Tracer limits no case reached**: a string line under five bytes carries no
  id, an integer stand-in can equal a literal integer, and a validation rule can
  judge a stand-in differently from its value (SP §8); the last is refused under
  §8.1.
- **Embedded formatting**: an author's formatting is not preserved (§5.4).
- **Ingestion input**: only configurations read back from a node were
  ingested; a generated configuration before Talos normalizes it was not (E1 §7).
- **Leak detection**: exact copies only; the OpenBao storage volume is not
  observable, and Transit plaintext crossed loopback without TLS in the fixture
  (E1 §7).
- **Validation mode**: SR and SP validated the fixture base in container mode;
  metal mode only on generated bases (SR §8).

The Upgrade/LifecycleClient deferral in §10.3 stands: this contract does not
claim full E3, upgrade support or lifecycle execution.

## 16. Choices for owner review

Each is the most conservative option consistent with the design where the
evidence does not settle the question.

1. **Tag syntax `!bwref <name>`.** Alternatives: marked string, binding. All
   reach early parity; the tag has no opt-in, no collision and no stale address
   (§5.1).
2. **Declarations beside the fragment, `encoding` enum `{base64}`, one
   declaration per name per fragment.** Alternative: per-occurrence modifiers;
   not evidenced (§5.2).
3. **Kinds string, integer, boolean, mapping; lists, floats and nulls
   refused.** List targets were not run (§5.3).
4. **RFC 6901 addressing with `doc[n]`.** Untested; any scheme that escapes `.`
   and `[` would meet E1's finding (§2.2).
5. **Reserved text `!bwref` refused in any string.** Catches a tag left in
   unidentified text at the cost of refusing literal look-alikes (§5.5).
6. **An exact literal copy of a resolved value refuses publication.**
   Alternative: redact by value and publish; rejected because the copy is
   already a plaintext exposure in source (§6 step 7).
7. **Transient staging by default; encrypted only for review that must survive
   the process; a separate staging key.** E1 recommends the split, not the key
   separation (§3.1).
8. **Takeover only after lease lapse, before absolute expiry, on an explicit
   operator request, fenced by owner generation.** Alternative: automatic
   takeover; no contention evidence supports it (§3.4).
9. **Claims from an earlier recovery epoch are abandoned.** A restore rewinds
   generations (§3.5).
10. **HMAC under a provider-held key for persisted value digests.** Alternative:
    per-run salt, which breaks cross-run comparison (§4.1).
11. **Keep the substring search beside value comparison.** Alternative: value
    comparison only, which misses embedded secrets (§4.2).
12. **Withhold verbatim messages from steps with boolean inputs, and never
    persist a withheld message.** Until a quote can be marked (§8.3).
13. **Go machinery in process, conditional on a parity re-run.** Alternative:
    pinned `talosctl` subprocess, which keeps measured parity but moves
    plaintext across a process boundary (§10.1).
14. **Compile only the node's running contract minor.** v1.12 would also be
    accepted by a v1.13 node; one contract keeps paths and validation single
    (§10.2).
15. **Provider generation paths include a component the database does not
    issue.** Guards against identifier reuse after a restore (§2.3).

## 17. Traceability

| Clause | Design | Evidence |
| --- | --- | --- |
| §1 identities | §13.1, §13.2 | [PC §2](../design/research/20260924-provider-capability-comparison.md#2-candidates-and-identities) |
| §2.1 typed boundary | §7.1 | [E1 §6](../design/research/20260922-secret-ingress-extraction-before-persistence.md#6-what-this-decides), [E1 §8](../design/research/20260922-secret-ingress-extraction-before-persistence.md#8-recommendation) item 2 |
| §2.2 addressing | §7.1 | [E1 §7](../design/research/20260922-secret-ingress-extraction-before-persistence.md#7-limits); E1 §8 item 5 |
| §2.3 pipeline, orphans | §7.1, §7.4, §7.8 | [E1 §4](../design/research/20260922-secret-ingress-extraction-before-persistence.md#4-expected-and-observed) (4.2, 4.4, 4.6); [DB §4.7](../design/research/20260924-database-semantics.md#47-s7-restored-state) |
| §2.4 identification limits | §6.9 | E1 4.7; [FR §8](../design/research/20260925-feasibility-evidence-review.md#8-cross-report-findings) C1; [SP §5](../design/research/20260925-sensitivity-provenance.md#5-what-each-failure-is) |
| §3.1 staging modes | §7.1 | E1 4.2, §6, §8 item 3; E1 5.16 |
| §3.2–§3.4 claims, lease, takeover | §7.1 | E1 4.2, 5.15, 5.16, 5.20, §7; [DB §4.4](../design/research/20260924-database-semantics.md#44-s4-ownership-transitions), [DB §4.5](../design/research/20260924-database-semantics.md#45-s5-queue-claims) row 021 |
| §3.5 restoration | §14.6 | DB §4.7; [execution and recovery §7](execution-recovery.md#7-recovery-after-management-state-restoration) |
| §4.1 keyed digests | §7.1 | E1 §7, §8 item 7 |
| §4.2 guard | §7.1, §6.9 | [E1 5.18](../design/research/20260922-secret-ingress-extraction-before-persistence.md#518-the-fourth-review-and-the-fixes-made-after-the-evidence), [E1 5.19](../design/research/20260922-secret-ingress-extraction-before-persistence.md#519-advisory-rounds-five-to-eighteen-the-prototype-hardened-the-evidence-unchanged) |
| §5.1 tag syntax | §6.9 | [SR §5](../design/research/20260924-structural-reference-composition.md#5-what-each-late-failure-is), [SR §6.4](../design/research/20260924-structural-reference-composition.md#64-criterion-4-candidate-failures-and-renderer-implications), [SR §9](../design/research/20260924-structural-reference-composition.md#9-alternatives-and-decision-enabled); [SP §7.1](../design/research/20260925-sensitivity-provenance.md#71-the-binding-form-cannot-hold-a-list-element-reference); [E3 §6.2](../design/research/20260925-talos-compatibility.md#62-criterion-2-subprocess-against-machinery-and-the-structural-reference-evidence) |
| §5.2 declarations, enum | §6.9 | [SP §2](../design/research/20260925-sensitivity-provenance.md#2-candidates); SR §3.1 |
| §5.3, §5.4 targets, aliases, embedded | §6.3, §6.9 | [SR §4](../design/research/20260924-structural-reference-composition.md#4-the-matrix), SR §6.2, §6.4, [SR §8](../design/research/20260924-structural-reference-composition.md#8-limits); [SP §4.2](../design/research/20260925-sensitivity-provenance.md#42-provenance-per-case) |
| §5.5 reserved text | §6.9 | SR §6.4, [SR §10](../design/research/20260924-structural-reference-composition.md#10-hand-off) |
| §6 early resolution, superset | §6.2, §6.9, §7.4 | SR §4, §5, [SR §6.2](../design/research/20260924-structural-reference-composition.md#62-criterion-2-effective-and-superset-dependencies), §6.4, §9 |
| §6 step 7 literal copies | §6.9 | [SP §6.3](../design/research/20260925-sensitivity-provenance.md#63-criterion-3-remaining-leakage-risks-and-dependency-records), [SP §10](../design/research/20260925-sensitivity-provenance.md#10-hand-off) |
| §7 validation stages | §6.9 | [SR §6.3](../design/research/20260924-structural-reference-composition.md#63-criterion-3-parity-validation-collisions-and-rejections) |
| §8.1 tracer, fidelity | §6.9 | [SP §6.1](../design/research/20260925-sensitivity-provenance.md#61-criterion-1-sensitivity-through-each-transformation), [SP §8](../design/research/20260925-sensitivity-provenance.md#8-limits), [SP §9](../design/research/20260925-sensitivity-provenance.md#9-alternatives-and-decision-enabled) |
| §8.2 provenance record | §6.9 | SP §6.3; [SP §4.4](../design/research/20260925-sensitivity-provenance.md#44-controls) |
| §8.3 redaction | §6.9, §15.4 | [SP §4.1](../design/research/20260925-sensitivity-provenance.md#41-which-representations-leak-a-referenced-value), [SP §6.2](../design/research/20260925-sensitivity-provenance.md#62-criterion-2-redaction-without-relying-on-value-matching), SP §6.3; [E3 §7](../design/research/20260925-talos-compatibility.md#7-limits) |
| §9 dependency records | §6.9, §7.5, §7.8 | SR §6.2; SP §6.3; FR §8 C2; [KL §5.2](../design/research/20260924-key-loss-restoration.md#52-criterion-2-applying-is-not-regenerating-and-ciphertext-is-not-executability), [KL §7](../design/research/20260924-key-loss-restoration.md#7-recommendation) item 1 |
| §10.1 renderer selection | §6.5 | E3 §6.2, [E3 §8](../design/research/20260925-talos-compatibility.md#8-recommendation); FR §8 C4; [E1 §3](../design/research/20260922-secret-ingress-extraction-before-persistence.md#3-what-was-built) |
| §10.2 pinning, refusals | §6.5 | [E3 §4.1](../design/research/20260925-talos-compatibility.md#41-generation-capability), [E3 §4.2](../design/research/20260925-talos-compatibility.md#42-validation), [E3 §4.3](../design/research/20260925-talos-compatibility.md#43-operationrpc-compatibility-against-v1136), [E3 §4.4](../design/research/20260925-talos-compatibility.md#44-upstream-support-policy), E3 §8 |
| §10.3 limits, deferral | §6.5, §18.1 E3 | [E3 §6.3](../design/research/20260925-talos-compatibility.md#63-criterion-3-unsupported-combinations-and-the-deferred-lifecycle-tests), E3 §7; [FR §2](../design/research/20260925-feasibility-evidence-review.md#2-what-binds-every-conclusion-here) |
| §11 publication hand-off | §6.6, §7.4 | PC §2; [DB §4.2](../design/research/20260924-database-semantics.md#42-s2-all-or-nothing-publication) |
| §15 gaps | §18.1, §18.2 | [FR §9](../design/research/20260925-feasibility-evidence-review.md#9-gaps), [FR §10](../design/research/20260925-feasibility-evidence-review.md#10-recommendations) |
