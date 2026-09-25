# Sensitivity and provenance through native Talos composition

| | |
|---|---|
| **Date** | 25 September 2026 |
| **Work item** | [E2: prove sensitivity and provenance tracking](https://github.com/ginsys/bronzeward/issues/4) |
| **Design reference** | [§6.9 Secret references and validation stages](../Talos_Configuration_and_Machine_Management_Design.md#69-secret-references-and-validation-stages), [§18.1 Phase 0](../Talos_Configuration_and_Machine_Management_Design.md#181-phase-0---evidence-before-implementation-contracts) |
| **Builds on** | [Structural references through native Talos composition](20260924-structural-reference-composition.md) (issue 3): its resolver, its cases and its finding that early resolution is the order that keeps native composition |
| **Artifacts** | [`experiments/e2-sensitivity-provenance/`](../../../experiments/e2-sensitivity-provenance/README.md), evidence under [`experiments/e2-sensitivity-provenance/evidence/`](../../../experiments/e2-sensitivity-provenance/evidence/) |
| **Decision enabled** | Which sensitivity and provenance representation a redaction and dependency-record contract can rest on, and what it leaves exposed. This is an input to [secret ingress and compilation](https://github.com/ginsys/bronzeward/issues/17). No production redaction design is proposed. |

## 1. Question

Design §6.9 requires that sensitivity and source provenance recorded at resolution time survive
"resolution, upstream composition, list changes, aliases and embedded serialization", so that
redaction of diffs, validation errors, logs and support data follows from provenance, and states
that value matching alone is insufficient. It excludes a custom merge engine.

The question is therefore whether, with `talosctl machineconfig patch` as the only composer, the
output leaf each reference's value became can be known for every transformation the issue names,
including moved and overwritten values, and whether redaction derived from that knowledge leaves
nothing of a referenced value in the artifacts value matching would leak.

| Acceptance criterion | Answered in |
|---|---|
| 1. Path/source sensitivity through each transformation, including moved or overwritten values | §3.1 (the cases), §4.2 (provenance per case), §6.1 |
| 2. Redaction of diffs and errors without relying solely on matching secret values | §4.1 (the leak matrix), §5, §6.2 |
| 3. Remaining leakage risks; the provenance needed for effective and reproduction dependency records | §6.3 |

## 2. Candidates

Seven representations were compared on the same artifacts. The first two are the baselines: no
redaction, and the value matching §6.9 calls insufficient.

| Representation | Redacts |
|---|---|
| `none` | nothing |
| `value` | every text occurrence of a known resolved value (strings and bytes; integers and booleans are not matchable text) |
| `resolution-path` | the leaf each fragment's resolution wrote, at that fragment's path, read on the composed output |
| `schema` | every field the Talos machinery (`pkg/machinery` v1.13.6, `RedactSecrets`) marks secret |
| `composed-path` | the output leaf each reference's value became, found by a tracer pass through composition |
| `path+schema` | `composed-path` and `schema` |
| `path+schema+value` | all three |

A path representation writes `<redacted:REF@VERSION>` (with `#leaf` for a mapping reference's
leaf), so a diff still shows which reference and which version changed. `schema` writes
`<redacted:schema>` and `value` writes `<redacted:value>`.

**The tracer pass.** For each case the prototype derives, besides the real values, a trace pass in
which every reference occurrence gets a stand-in of the same shape: each letter becomes `x`, each
digit `0`, every other byte stays (so YAML and JSON quote it the same and a validation rule judges
it the same), and each line of five bytes or more starts with `zq` and a three-digit id. Integers
get `61000 + id`. A boolean's tracer is its own value; one extra pass per boolean flips it, and the
leaf that flips is the boolean's. Both passes are resolved by issue 3's `bwref` and composed by
`talosctl` from the same relative paths. The real and trace compositions must have the same tree
shape (the fidelity check); then every output leaf that holds a tracer is attributed to that
tracer's reference, occurrence and fragment. For an overwritten reference, the trace pass's proper
prefixes (fragments 1..j) are composed too, and the first fragment after which its tracer
disappears is recorded as the one that overrode it. Nothing here merges: `talosctl` composes every
pass, and the prototype only reads outputs.

**Messages.** A path representation redacts a `talosctl` message by template: the same step on the
trace input gives a message whose tracer quotes (whole, or a prefix such as `zq000xx...`) mark where
the real message quotes a value; everything between them must match exactly, and each quote is
replaced by its reference's token. A message whose text differs in a way no tracer explains, or
whose step succeeded in one pass and failed in the other, is withheld. Representations without a
composed path show messages as `talosctl` printed them (`value` then value-matches them).

**Diffs.** A diff that shows a base leaf beside its redacted output leaf would reveal a boolean or
an integer by elimination (`-wipe: false` next to `+wipe: <redacted:...>`). A path representation
therefore redacts the base side of such a pair as `<redacted:paired>`.

## 3. What was built

- **`bwprov`** (Go, `gopkg.in/yaml.v3`, `pkg/machinery` for the schema representation): derives the
  real, trace and flip passes, attributes tracers after composition, writes every representation's
  diff, errors, log and support data, the provenance and dependency records, the expectations and
  the controls; pairs two revisions; merges and summarises the run.
- **`bwref`**, built unchanged from issue 3's module: generates the forms and resolves them.
- **`run/all`**: per base and case, derives, resolves (the tag form, early, for the trace passes;
  every candidate for the real pass, to re-check issue 3's premise), composes, validates, analyses;
  then the revision pairs and the summary. It refuses an incomplete run.
- **`run/collect-evidence`**: copies the committable text into `evidence/`, masks the secrets
  bundles' public values, and refuses the collection on any secret, synthetic value or home path.
- **The oracle**: independent of every representation, it looks for each synthetic reference value
  in each artifact in nine forms: exact; as placed (a modifier's output); YAML-escaped;
  JSON-escaped; base64; one line of eight bytes or more of a multi-line value; the seven-byte
  prefix plus `...` a decode error quotes; any twelve-byte window of the value that neither the base
  nor the case's fragments also hold; and, for an integer or a boolean, `<key>: <value>`. A base
  secret is looked for exact.

Every expectation was written into `case.yaml` before the first run. As in issue 3, that is
asserted, not shown by the history, because cases and prototype landed together.

### 3.1 Cases

Fifteen cases reuse issue 3's fragments unchanged (`from:` in `case.yaml`), with `BWSYNTH-` values:

| Case | Target | Transformation |
|---|---|---|
| string | registry `auth.password` | resolution |
| int | `machine.features.kubePrism.port` | resolution, integer |
| bool | `machine.install.wipe` | resolution, boolean |
| bytes | `machine.acceptedCAs[0].crt` | base64 re-encoding |
| map | registry `auth`, a whole mapping | resolution, two leaves |
| override-literal | a reference, then a literal at the same path | reference overwritten by a literal |
| override-ref | a literal, then a reference at the same path | literal overwritten by a reference |
| alias | `&secret` on one registry password, `*secret` on another | alias to two paths |
| embedded-yaml | a value with a tab and quotes, in an identified inline manifest | embedded YAML re-serialization |
| embedded-json | a value with `<&>` and quotes, in an identified JSON file | embedded JSON re-serialization |
| unidentified-embedded | the same manifest, not identified | opaque embedded text (negative control) |
| document | `certificates` of a `TrustedRootsConfig` document | second document |
| collision | literal look-alikes beside a real reference | resolution |
| type-mismatch | a string in the integer port field | rejected by decoding |
| invalid-value | `cluster.network.dnsDomain` with spaces | rejected by validation |

Thirteen are new:

| Case | Setup | Transformation |
|---|---|---|
| list-append | a literal `extraManifests` URL, then a referenced one in fragment 2 | list change: the reference lands at composed index 1, not its fragment index 0 |
| list-duplicate | two inline manifests named `e2sp-dup`, the second holding a reference | list change; native fails validation on the duplicate name |
| ref-over-ref | reference `pass-a`, then reference `pass-b` at the same path | reference overwritten by a reference |
| delete | a reference, then `$patch: delete` on it | reference removed |
| map-partial | a mapping reference whose password a later literal overwrites | partially overwritten |
| whole-content | a multi-line script as a machine file's whole content | resolution, block scalar |
| yaml-escape | a password with a tab, quotes and a backslash | YAML escaping |
| duplicate-literal | a reference, and the same value pasted as a literal annotation | literal copy (the control where provenance must miss) |
| moved-r1, moved-r2 | the same reference at `registry.example.test`, then at `mirror.example.test` | moved value, as a revision pair |
| rotate-r1, rotate-r2 | the same path, value and version 1, then value and version 2 | rotated value, as a revision pair |
| type-mismatch-two | two string values in two integer fields, of 10 and 25 characters | decode error quoting a whole value and a prefix |

`list-append`'s binding premise is `refused`: issue 3's generator cannot bind a list element, which
the first development run showed before any result was read (§7.1).

### 3.2 Bases

As in issue 3, every case runs on the fixture's own control-plane configuration (validated in
`container` mode) and on `talosctl gen config` output with a fresh secrets bundle (`metal` mode).
Each base's secrets are the key, secret and token scalars of its bundle; `value` knows them, and
the oracle looks for them as base secrets, separately from reference values.

### 3.3 Pinned versions

| Component | Version |
|---|---|
| `talosctl` | v1.13.6, SHA-256 `540c5e7cb0d3fa3a9b2e1c717ced212727b73bcaf0cf9cf9ba2472ec381041d4`, from `fixtures/versions.env` |
| `github.com/siderolabs/talos/pkg/machinery` | v1.13.6 |
| Go | 1.27.1 |
| `gopkg.in/yaml.v3` | v3.0.1 |

`evidence/run.txt` records the capture's commit (`456df31`), zero uncommitted inputs, and the
`talosctl` version and digest.

### 3.4 Synthetic values and the leak scan

Every reference value is a synthetic `BWSYNTH-` string, or a synthetic integer or boolean. The bases
carry real-format but throwaway secrets. No bundle, composition or unredacted artifact is committed;
`evidence/` holds each case's four artifacts under `path+schema+value`, the revision pairs' diffs,
every merged table and the SHA-256 of every composition.

The refusal patterns are every long scalar of both bundles, every bundle secret, the fixture's own
scan list (which holds the literal `BWSYNTH-` prefix) and `BWSYNTH`: 36 patterns. Certificates and
other public bundle values are masked as `<masked:bundle-public>` before the scan (272 masked).
`collect-evidence` plants one pattern in a control file and requires the scan to report exactly
that file first. Result: `36 patterns, control fired, 0 hits in 42 files` (`evidence/leak-scan.tsv`).

The fixture's own leak scan (`fixtures/bin/evidence` over the `none`, `composed-path` and
`path+schema+value` artifacts) is corroboration only, as the issue requires: it matches exact copies
of the fixture's pattern list, so it cannot see a re-encoded or prefix-quoted value. Files with
hits, of 228 per representation: `none` 106, `composed-path` 34 (the fixture base's own secrets,
which a reference path does not cover, and the literal copy), `path+schema+value` 0
(`evidence/fixture-scan.tsv`).

### 3.5 Reproduction

```sh
fixtures/bin/up
E2SP_OUT=<an empty directory outside the checkout> experiments/e2-sensitivity-provenance/run/all
fixtures/bin/evidence <E2SP_OUT>/artifacts/none <E2SP_OUT>/artifacts/composed-path \
  <E2SP_OUT>/artifacts/path+schema+value
# copy the bundle fixtures/bin/evidence names to <E2SP_OUT>/fixture-bundle/
fixtures/bin/down
E2SP_OUT=<the same directory> experiments/e2-sensitivity-provenance/run/collect-evidence
```

One capture is committed. Three development runs on the generated base alone preceded it; the last
of them gave the same summary as the capture's generated half.

## 4. Results

28 cases and 2 revision pairs on 2 bases: 56 cells and 4 pairs, 1356 expectations, **2 observations
differing from their expectation** (the same one on both bases, §4.3), every control that must fire
fired, verdict `complete` (`evidence/summary.txt`). The two bases agree on every leak verdict and on
every provenance count.

### 4.1 Which representations leak a referenced value

Per case, `L` where the oracle found a referenced value in any of the four artifacts (both bases
identical):

| Case | none | value | resolution-path | schema | composed-path | path+schema | path+schema+value |
|---|---|---|---|---|---|---|---|
| string | L | - | - | - | - | - | - |
| int | L | L | - | L | - | - | - |
| bool | L | L | - | L | - | - | - |
| bytes | L | L | - | L | - | - | - |
| map | L | - | - | L | - | - | - |
| override-literal | L | - | - | - | - | - | - |
| override-ref | L | - | - | - | - | - | - |
| alias | L | - | - | - | - | - | - |
| embedded-yaml | L | L | - | L | - | - | - |
| embedded-json | L | L | - | L | - | - | - |
| unidentified-embedded | - | - | - | - | - | - | - |
| document | L | - | - | L | - | - | - |
| collision | L | - | - | - | - | - | - |
| type-mismatch | L | L | L | L | - | - | - |
| invalid-value | L | - | L | L | - | - | - |
| list-append | L | - | L | L | - | - | - |
| list-duplicate | L | - | - | L | - | - | - |
| ref-over-ref | L | - | - | - | - | - | - |
| delete | L | - | - | - | - | - | - |
| map-partial | L | - | - | L | - | - | - |
| whole-content | L | - | - | L | - | - | - |
| yaml-escape | L | L | - | - | - | - | - |
| duplicate-literal | L | - | L | L | L | L | - |
| moved-r1, moved-r2 | L | - | - | - | - | - | - |
| rotate-r1, rotate-r2 | L | - | - | - | - | - | - |
| type-mismatch-two | L | L | L | L | - | - | - |

Totals, per base, of 28 cells (`evidence/summary.txt`, which counts both bases):

| Representation | Cells leaking a referenced value | Cells leaking a base secret |
|---|---|---|
| none | 27 | 26 |
| value | 8 | 0 |
| resolution-path | 5 | 26 |
| schema | 15 | 0 |
| composed-path | 1 | 26 |
| path+schema | 1 | 0 |
| path+schema+value | 0 | 0 |

Base secrets leak in 26 cells, not 28: the two type-mismatch cells have no composition to show.
The revision pairs' diffs: `none` leaks and `path+schema+value` is clean for both pairs on both
bases (`evidence/pair-leaks.tsv`).

### 4.2 Provenance per case

From `evidence/cells.tsv` and `evidence/provenance.tsv`, per base (both identical):

| Case | Reference occurrences | Output leaves attributed | Status recorded |
|---|---|---|---|
| alias | 1 | 2 (both registry passwords) | effective at two output paths |
| list-append | 1 | 1, at `cluster/extraManifests[1]` from source `[0]` | effective, index moved by composition |
| override-literal | 1 | 0 | overridden, by `f2.yaml` |
| delete | 1 | 0 | overridden, by `f2.yaml` (the delete directive) |
| ref-over-ref | 2 | 1 | `pass-b` effective; `pass-a` overridden by `f2.yaml` |
| map-partial | 1 (two leaves) | 1 | `#username` effective, `#password` overridden by `f2.yaml` |
| moved-r2 | 1 | 1, at `mirror.example.test` | effective at the new path |
| rotate-r2 | 1 | 1 | effective, version 2 |
| bool, int | 1 | 1 | effective (bool by its flip pass) |
| unidentified-embedded | 1 | 0 | unresolved (no tracer could be placed) |
| type-mismatch, type-mismatch-two | 1, 2 | 0 | not composed |

Every other case: one output leaf per tracer, effective. The fidelity check held in every cell: the
real and trace compositions had the same shape, and the trace pass resolved the same paths as the
real one (`resolution-records-match`, 56 of 56).

`resolution-path`, read on the composed output, misses 1 leaf and names 1 wrong leaf in
list-append, names the literal that overrode it in override-literal and map-partial, and names
nothing in delete (`resolution-missed` and `resolution-extra` in `cells.tsv`).

### 4.3 The observation that differed from its expectation

`alias` under `resolution-path` was expected to leak and was clean on both bases. The expectation
assumed `bwref` records only the anchored path; it records both paths an alias writes (2
resolutions for 1 occurrence), so the fragment paths name both output leaves. The expectation was
wrong, not the representation. It is kept as written and counted as unexpected.

### 4.4 Controls

| Control | Claim it guards | Result, all cells |
|---|---|---|
| `fidelity-check-fires` | "the trace composition reproduced the real one": a corrupted string tracer must fail it | fired 40; not applicable 12 (no string tracer in the output: bool, int, bytes, delete, override-literal, unidentified-embedded); absent 4 (the two type-mismatch cases, nothing composed) |
| `template-check-fires-patch`, `-validate` | "the message template matched": a real message with text its template lacks must be withheld | fired 4 and 2, every cell with a redacted message |
| `per-side-diff-exposes-base-value` | "the paired diff hides a non-string base value": without pairing the base value shows | fired 6 (bool, int, invalid-value, both bases) |
| `stale-paths-leak` | moved-r2 under moved-r1's composed paths must leak | fired 2 (moved); did not fire on rotate, as expected |
| `stale-values-leak` | rotate-r2 under rotate-r1's values must leak | fired 2 (rotate); did not fire on moved, as expected |
| `stale-paths-version` | rotate-r1's tokens must name another version than rotate-r2's | fired 2 (rotate); did not fire on moved, as expected |
| leak-scan planted control | "the committed evidence holds no refusal pattern" | fired |
| positive control (not a row) | value matching misses and provenance catches | `value` leaks and `composed-path` is clean in 8 cases per base (§4.1) |
| negative control (not a row) | provenance misses a copy it did not produce | duplicate-literal leaks under every representation without `value` |
| `trace-holds-no-secret`, `base-holds-no-case-secret` | the passes are separated | pass 56 each |
| `schema-covers-base-secrets` | every bundle secret is at a schema leaf of its base | pass 56 |
| `render-identity`, base `validates-*` | rendering with nothing redacted is the text `talosctl` wrote; each base validates | pass |

`bwprov summary` makes the run incomplete if any must-fire control did not fire where applicable,
if a pair's required control did not fire, if a fidelity check failed, or if a unit is missing.

## 5. What each failure is

Every leak in §4.1 is one of the following. Each is an observed artifact of this capture.

- **Value matching misses re-encoded and non-text values.** bytes appears base64-encoded
  (`crt: QldTWU5USC1leHRyYS1jYS1jZXJ0aWZpY2F0ZQ==`), yaml-escape and embedded-yaml as escaped YAML
  (`"BWSYNTH-app-password\twith \"quotes\""`), embedded-json as escaped JSON (`<&>`),
  and integers and booleans are not matchable at all. Under `composed-path` each is the token of the
  leaf the tracer reached: `crt: <redacted:extra-ca@1>`, `"token":"<redacted:app-token@1>"`.
- **Value matching misses a quoted prefix.** `talosctl`'s decode error quotes the first seven bytes
  of a value longer than ten and adds `...`:

  ```text
  value:          line 4: cannot construct !!str `BWSYNTH...` into int
  composed-path:  line 4: cannot construct !!str `<redacted:prism-port@1>...` into int
  ```

  In type-mismatch-two the ten-character value is quoted whole and the longer one as a prefix, in
  one message; both are redacted by the template. This is issue 3's hand-off item.
- **Fragment paths go wrong when composition moves a leaf.** list-append under `resolution-path`:

  ```diff
  +    extraManifests:
  +        - <redacted:manifest-url@1>
  +        - https://e2sp.example.test/BWSYNTH-manifest-token.yaml
  ```

  The literal at composed index 0 is redacted and the reference at index 1 is shown. Fragment paths
  also leave every message unredacted (invalid-value, type-mismatch).
- **The schema covers only fields Talos types mark secret.** A registry password is one; a registry
  user name, a port, a boolean, a CA certificate, a manifest URL, an inline manifest, a file's
  content, a DNS domain and an annotation are not, so `schema` leaks in 15 cases. When the machinery cannot load a composition (both
  type-mismatch cases), there is no schema at all.
- **A composed path covers only what came through a reference.** Base secrets are not references,
  so `composed-path` leaks them in every composed cell's support data (the full configuration); the
  schema covers them. A literal copy of a referenced value is not a reference either:

  ```text
  composed-path:      e2sp.example.test/pasted: BWSYNTH-duplicated-literal-password
  path+schema+value:  e2sp.example.test/pasted: <redacted:value>
  ```

- **Stale provenance leaks.** Reusing the previous revision's composed paths on the next revision
  shows a moved value (moved pair); reusing its values shows a rotated one (rotate pair). Each
  revision's own trace pass redacts both, and its tokens show the change without the value:

  ```diff
  -                    password: <redacted:reg-pass@1>
  +                    password: <redacted:reg-pass@2>
  ```

## 6. What this decides

### 6.1 Criterion 1: sensitivity through each transformation

For every transformation the issue names, the tracer pass located each reference's value in the
composed output without modelling the merge: resolution (strings, integers, booleans, mappings,
whole block content), base64 re-encoding, alias to two paths, identified embedded YAML and JSON,
a second document, list append (the index changes) and duplicate list entries, a moved value, a
rotated value, and a reference overwritten by a literal, by a reference and by a delete directive,
wholly or in one leaf (§4.2). Each attribution names reference, version, occurrence, source
fragment and its SHA-256, source path, and output path; an overwritten one names the fragment that
overrode it. The prototype never merges: every composition is `talosctl`'s, one extra composition
per boolean and one per fragment prefix. **The negative result that would have called for a
design revision, provenance needing a merge engine, was not found.**

What the tracer cannot place, it reports: an unresolved reference inside unidentified embedded text
(no value to trace), and a composition that failed (not composed).

### 6.2 Criterion 2: redaction without relying on value matching

Redaction from composed paths plus the Talos schema (`path+schema`) left no referenced value and no
base secret in the diffs, errors, logs and support data of 27 of 28 cases per base, including the 8
where value matching leaks (§4.1). Every token in those artifacts comes from an attribution, not from
the value's text; messages are redacted by the trace pass's template, not by searching for the
value. The one case it leaks is the literal copy (duplicate-literal), which has no provenance by
construction; adding value matching (`path+schema+value`) closes it for the exact form. So value
matching is a supplement for text the author wrote outside a reference, not the mechanism.

The representations are complementary, not ranked: composed paths cover referenced values, the
schema covers base secrets and Talos-typed secret fields, value matching covers exact literal
copies. Neither of the first two alone is clean (composed-path leaks base secrets in 26 cells per
base; schema leaks referenced values in 15).

### 6.3 Criterion 3: remaining leakage risks and dependency records

**Dependency records.** From the provenance record (`evidence/provenance.tsv`,
`evidence/dependencies.tsv`):

- An **effective** dependency is a reference and version whose tracer reached an output leaf. It is
  what the artifact contains. override-literal, delete: none; ref-over-ref: `pass-b@1` only;
  map-partial: `reg-auth@1`, one leaf of two; alias: `reg-secret@1`, once, at two paths.
- A **reproduction** dependency is every reference occurrence in the source fragments, with the
  fragment and its digest: override-literal needs `reg-pass@1` from `f1.yaml@61032feac965` although
  the artifact holds none of it; ref-over-ref needs both `pass-a@1` and `pass-b@1`;
  unidentified-embedded records `app-pass@1` although it was never resolved.

The provenance needed for both is, per occurrence: reference, version, source fragment and digest,
source document and path, and after composition either the output document and path, or the
fragment that overrode it, or `unresolved`. The version is needed for rotation (the token and the
record change with it); the fragment digest ties a reproduction record to the source revision. This
matches issue 3's §6.2 distinction and measures it for booleans, which issue 3's sentinel pass
could not identify: the flip pass does.

**Leakage risks after redaction** (with `path+schema+value`, the only representation with no leak
here):

- **Literal copies in other forms.** Value matching catches an exact copy; a copy that is
  re-encoded, split, hashed or partial is caught by nothing. No case ran one.
- **A secret in a base field the schema does not mark.** Composed paths cover only references, the
  schema only Talos's marked fields. None was run; §6.9's secret-ingress boundary is what must keep
  such values out of bases.
- **Unidentified embedded text.** It is opaque to every representation. It held no value here
  because nothing resolves inside it, but a literal secret there would be seen only by exact value
  matching.
- **Stale provenance.** Paths or values from another revision leak moved and rotated values
  (§4.4). The attribution must be recomputed for every composition that is shown.
- **Messages the template cannot explain are withheld, not shown**, so a support bundle loses them.
  A tracer that changed a message's outcome (a validation rule judging the stand-in differently from
  the value) would be withheld the same way; no case produced one.
- **Tokens inside re-serialized embedded JSON are escaped** (`<redacted:app-token@1>`),
  so a reader searching for `<redacted:` will not find them. The redaction re-serializes the
  document it enters (compact, sorted keys, as `bwref` wrote it).
- **A boolean's trace pass holds the real boolean**, since its tracer is the value; only the flip
  pass attributes it. The trace pass is internal and never shown.

## 7. Failures hit while building it

### 7.1 The binding form cannot hold a list-element reference

`bwref gen` writes the forms in order and stops at the binding with `a binding needs a mapping key`
on list-append. The first development run showed it; `run/all` now records the missing form as a
resolver refusal with `gen`'s message, and list-append's premise says `binding: refused`.

### 7.2 The trace pass held case values

The first version of `trace-holds-no-secret` failed on bool (the tracer is the value), on
duplicate-literal (an authored literal the trace pass keeps) and on list-append (the literal URL in
fragment 1). A boolean and a value the case's fragments hold literally are now exempt from that
control, and the oracle's twelve-byte windows skip text the base or the case's fragments also hold
(the URL prefix list-append's literal shares with its reference). An authored literal copy in an
artifact still counts as a leak (duplicate-literal).

### 7.3 The schema note quoted the value

When the machinery could not load a rejected fragment, the support data printed its error, which
quotes the value's prefix, so `path+schema` leaked on type-mismatch. The note is now fixed text.

### 7.4 Two writers to one message file

Validation first wrote its standard output and standard error through two opens of one file; one
overwrote the other. It now writes both through one descriptor.

### 7.5 An expectation that differs is a result

The first summary called a run incomplete on any differing observation, which would have hidden
§4.3. It now counts them as `unexpected`; only a failed fidelity check, a missing unit or a control
that did not fire makes a run incomplete.

## 8. Limits

- **One Talos version, strategic merge only.** Every result rests on `talosctl` v1.13.6 and its
  machinery module; JSON6902 was not tried (issue 3 §8).
- **Early resolution is the premise.** The tracer pass resolves before composition, as issue 3
  found necessary; nothing here tests provenance under late resolution.
- **Twenty-eight cases, control-plane bases only.** Not covered: worker configurations, durations,
  IP and CIDR fields, a list as a whole reference, more than two fragments, list-element overrides
  by selector, a hand-formatted embedded literal, a re-encoded or partial literal copy, a secret in
  an unmarked base field.
- **Tracer limits no case reached.** A string line shorter than five bytes gets no id and relies on
  its position; an integer tracer (61000 plus its id) could equal a literal integer in the base or
  fragments; a validation rule could judge a tracer differently from its value (the message is then
  withheld, which is safe but was not observed).
- **Controls that passed without a firing counterpart.** `trace-holds-no-secret`,
  `base-holds-no-case-secret` and `schema-covers-base-secrets` passed in every cell; the first two
  use the same oracle that fired in 27 of 28 cells under `none`, and the first failed during
  development (§7.2), but the third was never seen to fail.
- **The fidelity control was not exercised in 16 cells** (§4.4): 12 with no string tracer in the
  output and 4 with no composition. There the fidelity check itself still ran and passed.
- **The oracle searches nine forms.** A transformation outside them (hashing, splitting across
  lines other than a value's own) would be invisible to it, as to the fixture's scan.
- **The prototype is disposable.** Its tracer format, token syntax and table layouts are this
  experiment's, not a proposed design.

## 9. Alternatives and decision enabled

Compared: no redaction; value matching; fragment resolution paths; the Talos machinery's schema;
composed paths found by a tracer pass; and their combinations (§2). Not compared: a custom merge
engine (excluded by §6.9); instrumenting `talosctl` itself; a production redaction library.

Decision enabled: **provenance can be carried through native composition without a merge engine,
by composing a shape-preserving tracer pass with `talosctl` alongside the real one, and redaction
from those composed paths plus the Talos schema covers every referenced value and base secret here
except an authored literal copy.** A redaction and provenance contract can rest on: per-occurrence
provenance recorded at resolution; a tracer composition for every shown composition; template-based
message redaction with withholding as the fallback; paired redaction of non-string diff sides;
the schema for base and Talos-typed secrets; value matching only as a supplement for literal copies.
Whether that is the contract, and at what cost per render (one extra composition per boolean and
per fragment prefix), is for the [compiler specification](https://github.com/ginsys/bronzeward/issues/17).

## 10. Hand-off

**To [secret ingress and compilation](https://github.com/ginsys/bronzeward/issues/17):**

- The provenance record (§6.3) and the two dependency records derived from it; the effective
  boolean dependency is measurable by a flip pass.
- The tracer pass as the provenance method, and its limits (§8).
- Open there: whether a renderer must refuse literal copies of a referenced value (duplicate-literal
  shows only value matching sees them), how withheld messages reach a support bundle, how embedded
  documents keep their author's formatting once a token is written into them, and whether provenance
  must be recomputed per shown revision (§5, stale provenance: it must, on this evidence).

**To [retention and recovery policy](https://github.com/ginsys/bronzeward/issues/15):** a reproduction
dependency can outlive every effective one (override-literal, delete): losing it leaves the artifact
intact but blocks re-rendering from source.
