# Analysing an imported configuration, and proving a refactoring equivalent

| | |
|---|---|
| **Date** | 18 November 2026 |
| **Work item** | [Investigate analysis of imported configurations and the equivalence proof](https://github.com/ginsys/bronzeward/issues/861) |
| **Design reference** | [§9.1 Adopt an existing configured cluster](../Talos_Configuration_and_Machine_Management_Design.md#91-adopt-an-existing-configured-cluster), [§20.7 Existing-cluster adoption](../Talos_Configuration_and_Machine_Management_Design.md#207-existing-cluster-adoption) |
| **Specification reference** | [compilation §6 and §8.4](../../spec/compilation.md), [persistence and API §3.2, §6.1](../../spec/persistence-api.md) |
| **Artifacts** | [`evidence/20261118-import-equivalence/`](evidence/20261118-import-equivalence/): `gen.sh`, `measure.go`, `runall.sh`, `output.txt` |
| **Decision enabled** | Which of three ways to turn an imported configuration into shared fragments become implementation issues, and which definition of "the output does not change" the owner accepts. The decision is the owner's; nothing here selects one. |

## 1. Question

An import keeps each machine's configuration as that machine's import base, one whole document per
machine. Design §9.1 step 5 says "Refactoring into profiles is a later draft change that must prove
equivalent output", and §20.7 says reconstruction "must prove equivalence". No document defines
that sentence. This note defines it, measures what each definition can and cannot see, and
compares three ways to get from a set of import bases to shared fragments:

- **(a) manual refactoring:** the operator writes the fragments and a thinner base.
- **(b) assisted proposal:** a tool compares the imported machines and proposes the split; the
  operator reviews it.
- **(c) automatic split:** the tool computes the split and a check accepts it.

| Acceptance criterion of ginsys/bronzeward#861 | Answered in |
|---|---|
| Each option has evidence and limits | §6 |
| The equivalence definition is written; is the publication's render of every covered machine enough? | §3, §8 |
| How a comparison handles values redaction does not cover (compilation §8.4) | §7 |

## 2. Pinned inputs

| Item | Value |
|---|---|
| Repository | `ginsys/bronzeward` at commit `71f5b4ebff31a963680b8b26e1dc6efb4d49414e` (main when measured) |
| `talosctl` | v1.14.5, sha256 `ff432b68986709e6b69c563a08a2ced4232d5196b0dccd6d2e783e9d714d7d7d` (the repository's `TALOSCTL_SHA256`) |
| Talos machinery | `github.com/siderolabs/talos/pkg/machinery v1.14.5` (`go.mod`) |
| Go | 1.27.1 |
| Code under test | `internal/ingest` (`Read`, `Extract`, `Commit`) and `internal/compile` (`Compile`, `ModeMetal`), called as a library |

Every input is synthetic. `gen.sh` writes one secrets bundle with `talosctl gen secrets`, one
control-plane and one worker configuration with `talosctl gen config bw-synth https://10.55.0.2:6443`,
then six machines (three control planes `cp1` to `cp3`, three workers `w1` to `w3`) with
`talosctl machineconfig patch`. The per-machine differences are the interface address, the hostname
document, the install disk selector (`cp2` differs), a registry mirror on workers, a node label on
`cp3` and a sysctl on `w3`. Nothing in it is a real credential or a real cluster.

## 3. Equivalence, written down

A refactoring replaces a machine's import base `B` by a thinner base `B'` plus fragments `F`. The
claim to prove, per machine, is that composing `B'` and `F` gives the configuration the node
already runs. "The configuration the node runs" is the **baseline**: the bytes read from the node,
whose digest the import base carries ([persistence and API §1.1](../../spec/persistence-api.md),
point 2). Five levels, from strict to blind:

| Level | Two configurations are equal when | Ignores | Sees a changed secret |
|---|---|---|---|
| E0 | `talos.ConfigurationDigest` is equal: SHA-256 of the bytes, trailing newlines folded | nothing but trailing newlines | yes |
| E1 | both are loaded and re-encoded by the machinery (typed load, encoder without comments) first, then E0 | comments, key order, formatting and the typed encoding's normalisation | yes, by construction |
| E2 | the parsed documents, matched by kind and name, have equal trees | comments, key order, document order, formatting; **not** list order | yes, by construction |
| E3 | the same documents as typed machinery documents (`configloader`), matched by kind and name, are equal | as E2, plus whatever the typed form normalises | yes, by construction |
| E4 | the redacted forms, with the reference names masked, are equal as E2 | as E2, and the value behind every reference | **not behind a reference** (measured); a literal secret sits in the tree, so a change shows |

"By construction" means the level reads the plaintext, so a different value is a different tree. N4
(§5.5) measured it for E0 only: its harness does not change the plaintext columns.

Recommendation: **E3 is the definition**, computed server-side from plaintext, with E0 reported
beside it. Reasons, each measured below: E0 fails on any move of a document between base and
fragments (§5.3) and on comments (§5.1); E2 and E3 held for every machine in the unmodified splits
the compiler accepted, and were false in the controls N1, N2 and N5 that changed or removed
something (§5.3); E4 cannot see a changed secret behind a reference (§5.5). E3 still counts list
order, a conservative choice that was tested structurally only (§5.4), and calls two forms that
Talos treats alike different (§5.4, explicit false against omitted), so it can refuse a refactoring
that is in fact equivalent. It accepted none of the three mistaken compositions of §5.3 (N1, N2,
N5); that is a result on six machines and three mistakes, not a proof of soundness.

## 4. Method

`measure.go` (`//go:build ignore`, run with `go run`) builds each machine's import base the way
ingestion does (`ingest.Read`, `Extract`, `Commit` with an in-memory value store), so no database
or provider is involved. It renders through `compile.Compile` and, as a control, composes the same
inputs with the machinery's patcher directly (`plainCompose`); the two digests must agree
(`control=true` in every row). Only counts, paths and booleans are printed, never a value.

Reproduce:

1. Put the pinned `talosctl` at `bin/talosctl` beside `gen.sh`, then `bash gen.sh <work dir>`.
2. `bash runall.sh <repository checkout> <work dir> <output file>` (exit status 1 if any
   measurement failed).
3. Compare the result with `evidence/20261118-import-equivalence/output.txt`. `gen.sh` makes fresh
   keys and certificates on every run, so a fresh run differs in the random reference names and in
   the byte counts (encoded certificate and key lengths vary by a few bytes); the booleans, the
   leaf counts and the refusals should not. The committed output is the run the note quotes.

## 5. Measurements

### 5.1 B1: an unchanged import base against the node's bytes

Expected before the run: render of an unchanged base equals the node's bytes. Observed: true at
E0 to E3 for the six machines, whose bytes `machineconfig patch` wrote without comments (11630 to
11670 bytes for control planes, 3341 to 3389 for workers). The two files `gen config` wrote with
comments (19226 bytes with 95 comment lines; 9085 with 88) render to 11509 and 3093 bytes:
**E0 false, E1 to E3 true.** The compiler always re-encodes without comments, so a digest
comparison against comment-bearing bytes never holds. Whether a real node stores or serves its
configuration with comments was not measured.

### 5.2 B2 and M2: what a comparison of sanitized bases sees

Expected: two machines holding the same secret share a reference name. Observed: **no**. Distinct
import bases share 0 reference names even where the values are equal (`cp1` and `cp2`: 9 of 9
values equal, 0 shared names; a control plane and a worker: 3 of 3). Reference names are random and
never derived from the value, so a comparison over sanitized documents cannot see that two machines
hold the same secret, and cannot propose a shared reference.

Leaves by class over the sanitized documents, a list counting as one leaf:

| Machine | Leaves | Common to all | Common to the role | Machine-specific | Of which references |
|---|---|---|---|---|---|
| `cp1`, `cp2` | 72 | 24 | 36 | 12 | 8 |
| `cp3` | 73 | 24 | 36 | 13 | 8 |
| `w1`, `w2` | 34 | 24 | 5 | 5 | 3 |
| `w3` | 35 | 24 | 5 | 6 | 3 |

320 leaves over six machines against 118 in the proposal (common once, role once per role, machine
leaves each). The machine-specific leaves that are not references are the interface address, the
install disk selector, the hostname, the etcd encryption `resources` list (it holds a reference),
the `cp3` node label and the `w3` sysctl: the things an operator expects to differ.

### 5.3 M3: a split, composed and compared

The split puts every leaf common to all machines in a common fragment, every leaf common to a role
in a role fragment, and leaves the rest in a thin base. Taken leaf by leaf, the fragments are
2020 bytes (common), 4009 (control plane) and 975 (worker). After rule R2 below, which keeps some
of those leaves in the base, they are 1218, 1704 and 942 bytes.

| Variant | Result |
|---|---|
| Leaf by leaf | **Refused on every machine.** Control planes: `base-override` at `fragment[0]` (`machine.ca.key`, `cluster.etcd.ca.key`). Workers: "trusted CA certificates are required". A strategic-merge patch merges a mapping as a unit, so a fragment cannot take one leaf of a mapping whose other leaves stay in the base. |
| Rule R2: where the direct leaves of one mapping fall into different classes, all of them stay in the base (a nested mapping below is classified on its own); documents emptied by the split are dropped | Every machine composes. **E2 true, E3 true, E4 true; E0 false and E1 false** because document order changed (`docOrderSame=false`). Thin base: 11630 to 4025 bytes (`cp1`), 3341 to 1161 (`w1`). |
| R2, emptied documents kept as identity-only shells in the base | **E0 true, E1 true, E2 true, E3 true, E4 true, `docOrderSame=true`** for all six. Thin base: 5160 bytes (`cp1`), 1784 (`w1`). |

Negative controls, each of which must be detected, on `cp1` and `w1`. Under both R2 variants a
changed kubelet image in the common fragment (N1) and a dropped `dnsDomain` (N2) give E0 to E4
false. The role fragment left out (N5) is detected: false at every level for `cp1` with the
emptied documents dropped, and refused as invalid in the other cases (`w1`:
`diskSelector.match is required`; `cp1` with shells: seven validation errors). Leaf by leaf, all of
N1, N2 and N5 are refused before any comparison.

The fragments in the reverse order (N6) change nothing under R2, as expected: the common and role
fragments share no leaf. Leaf by leaf it does matter for `w1`: the forward order is refused, and the
reverse order composes and compares equal at E2 to E4. Order can therefore decide whether a
composition is accepted; the cause was not investigated.

Two consequences. A path-level proposal is **not composable as it stands**: in the forward order the
compiler accepted the split on all six machines only under R2, a rule about the patcher's merge
granularity (the one leaf-by-leaf acceptance was `w1` in the reverse order), so a proposal that has
not been composed and compared is not a proposal. And moving a document out of the base is
visible to E0 and E1: the check must not be a digest of the render.

### 5.4 N3: pairs (`w1`)

| Pair | Same behaviour? | E0 | E1 | E2 | E3 |
|---|---|---|---|---|---|
| `podSubnets` reversed (a structural list-order test: both variants load, neither was validated, and two IPv4 pod subnets are not a valid Talos configuration) | not tested | false | false | false | false |
| control: the comment-free machine against its re-encoding | yes | true | true | true | true |
| `defaultRuntimeSeccompProfileEnabled` explicit true (as `gen config` writes it) against omitted | no: omitted reads as false | false | false | false | false |
| the same field, explicit false against omitted | yes: omitted reads as false | false | false | false | false |

The sources for the last two rows: `config/generate/kubernetes.go:186` in machinery v1.14.5 writes
`true`, and `config/types/k8s/kubelet.go:201` returns false for an omitted field. The comment case
is B1 (§5.1), not this table: the control row uses a machine that has no comments.

List order counts at every level: a conservative choice, since which Talos lists are ordered was
not tested field by field. The last row is the one pair here that Talos treats alike and
every level calls different, so **E3 can refuse an equivalent refactoring**. A further level that
applies the typed defaults would close that, and was not built.

### 5.5 N4: a changed secret behind an unchanged reference

Expected: E0 false, E4 true. Observed: E0 false (the digest covers the plaintext), **E4 true**. The
redacted form shows `<redacted:NAME@VERSION>`: the reference name and its version, not the value.
A comparison of redacted forms, or of sanitized sources, cannot see a value that changes behind an
unchanged reference. Only E0 and E4 were read: the harness feeds the same plaintext to the E1 to E3
columns, so their detection of this change is an inference from how they are built, not a result.
Without masking the names, E4 was false for every machine in every variant, because the baseline
was ingested a second time and its names differ (B2); the masked form is the only one that can be
true.

## 6. The three options

| | (a) manual | (b) assisted proposal | (c) automatic split |
|---|---|---|---|
| Needs | the operator writes fragments and a thin base | a proposal engine over the imported machines, plus the check | the engine plus the check, with no review |
| Evidence | N1, N2 and N5 show the check detects the mistakes a manual edit makes (§5.3) | §5.2 sizes the proposal (118 against 320 leaves); §5.3 shows it composes in the forward order on all six machines only with R2 | as (b): the check accepted a split on every machine, once R2 and the shells were applied |
| Limits | an edited thin base has to be written somewhere (below) | the engine sees only sanitized documents, so it cannot see shared secrets (B2) and cannot tell an unmarked secret from a plain value (§7) | the check proves equality with the baseline; it does not prove the split is a good one: 118 leaves can still put a machine-specific value into a role fragment |
| The check | required | required | required, and it is the only review |

**The write route is missing for all three.** Every fragment is a patch onto the machine's own
import base (compilation §6), so a refactoring needs a new import base revision, thinner than the
last. In persistence and API §3.2 the only draft entry that carries one comes from an import or an
adoption, and `POST /ingestions` is the only route in that document that writes an import base. I
found no route by which an author writes a thinner base. Any of the three options therefore needs
either a new write route or a split performed at ingestion; both are specification work this note
does not do.

**Effect of the owner's answer on ginsys/bronzeward#869** (closing comment, 18 November 2026,
17:08 CET: "Whatever the user provides, bw gets access and saves the signing keys"). The answer
widens which first key a user may supply (a talosconfig, controlplane node configurations, or an
address with a secrets bundle). It concerns how access is obtained, not whether a baseline exists:
design §9.1 steps 2 and 4 observe each node's effective configuration and keep it as the baseline
whatever the first key was, so equivalence stays relevant to every machine adopted that way. Only a
machine that runs no configuration yet has no baseline, and so nothing to compare; its
configuration is built from fragments from the start. The PoC excludes enrolment and bootstrap
(compilation §6), so that case is outside the present scope. I did not check which first keys the
observation step can read a configuration with.

**Prior art.** Omni derives the patches from a generated default configuration and the difference
([design §20.7](../Talos_Configuration_and_Machine_Management_Design.md#207-existing-cluster-adoption)).
That is option (b) with a different base: it needs a default to diff against, and this design has
none, since the compiler never generates a base (compilation §6).

**Recommendation.** Take (b) first, behind the check of §3, and do not build (c): the proposal
engine and the check are the same work, and (c) is (b) without the human who can say that a
machine-specific value belongs in a fragment. (a) needs no engine and needs the check and the write
route, so it is the cheapest to ship and the least helpful.

## 7. Values redaction does not cover

Compilation §8.4 lists what redaction misses: a re-encoded, split or hashed copy of a secret; a
secret in a field the schema does not mark and nothing references; unidentified embedded text;
short or integer secrets quoted in a message.

- **The check runs on plaintext and prints paths.** E0 saw a changed value behind a reference
  (N4); E1 to E3 are built to see it (§5.5). They run inside the compiler's process, where both the
  old base and the new composition resolve to plaintext, and they report a path and a yes or no,
  never a value. E4, the only level computable from what the API serves, is blind to a change behind
  a reference (N4). Compilation §8.4 lists secrets nothing identifies: those are not behind a
  reference, so a change to one appears in the redacted tree and E4 sees it. What E4 cannot do is
  recognise such a value as a secret. A change is detectable; a secret that was never marked stays
  unknown to every level.
- **A proposal can copy an unmarked secret into a fragment.** The engine reads sanitized documents,
  where an unmarked secret is a plain leaf, so it can put one into a shared fragment as source text.
  Compilation §8.4 says §6 step 7 refuses only exact copies of a secret value, so a re-encoded or
  partial copy is not refused. This case is a reading of §8.4 and was not measured: the synthetic cluster
  holds no unmarked secret.
- **Embedded text** is one leaf to the engine and to every level: equal or not equal, never partly.

## 8. Is the publication's render of every covered machine enough?

The publication page says "Publishing renders the configuration of every machine of the cluster that
has an import base, from this draft's revision, and makes the release Desired for those machines"
(`web/src/publish.tsx:152`). From persistence and API §6.1 and §9.3 (its `pendingConvergence` paragraph):

- Publication compiles every covered machine and commits a release carrying each plaintext
  configuration digest. In §6.1's steps and the stale-input refusals of §4.2 I found no comparison
  of the output with the import base: the checks named there concern heads and the import base
  revision, not the output.
- `pendingConvergence` is true exactly when the `Desired` digest differs from the `Applied` digest,
  and after an adoption the `Applied` digest is the import base's. So after a publication, a machine
  whose release compiled the baseline's bytes reads `pendingConvergence=false`: an E0 check,
  available per machine. Neither digest is served (choice §17.38), and the adopt plan's diff is
  withheld for an import-base-held node.
- That check comes **after** the release is Desired, so it is a result, not a preview. It is binary,
  with no path to repair. It fails on a document moved between base and fragments (§5.3) and on a
  node whose bytes carry comments (§5.1).

**So the render is enough for E0 only, and E0 is the level that fails on the legitimate
refactoring.** The check of §3 needs a compile of both the old base and the new composition before
publication, and a per-machine report of paths. It does not need a new source of plaintext: the
compiler already produces both (B1, `control=true`).

## 9. Limits and failures

- Synthetic cluster of six machines from one `gen config`: no real node, no comments served by a
  real node, no operator-written legacy `machine.install` or `machine.network.hostname`
  (v1.14.5 refused both and `gen.sh` uses the documents instead).
- The leaf-by-leaf split was refused before R2 (§5.3). An earlier version of the program also
  matched documents by position, and reported false at E2 to E4 for a split whose leaves were equal
  but whose document order had changed; the committed program matches by kind and name, and that
  earlier run's output was not kept.
- E4 is false when the names are not masked (§5.5); the masked form is what §3 defines.
- Fragment order was observed to matter only in one leaf-by-leaf case (§5.3) and its cause was not
  investigated. A case with two fragments that set the same leaf was not run.
- N4 measured E0 and E4 only (§5.5); E1 to E3 detect a changed secret by construction, not by
  measurement.
- The fresh-generation byte counts are not reproducible (§4); the committed output is the quoted run.
- The shells variant keeps documents the split emptied. Whether an identity-only document is valid
  for every document kind was measured on the six machines only.
- A heavy run (fixture, cluster, browser) was not made; nothing here was applied to a node.
- E3 can refuse an equivalent refactoring (§5.4). Typed defaults were not applied.
- Not delivered: any code, the write route of §6, the specification of the check, the unmarked-secret
  case of §7.

## 10. Decision for the owner

1. Is **E3, computed from plaintext before publication, with paths only**, the definition of "the
   output does not change"? Recommendation: yes. The alternative, a digest of the render, fails on
   the refactoring it is meant to approve.
2. Which of (a), (b) and (c) become implementation issues, and in which milestone? Recommendation:
   (b) with the check; (a) as the fallback with the same check; no (c).
3. Does a thinner import base get a write route, or is the split done at ingestion? Not
   recommended without a specification first; this note found no route.
