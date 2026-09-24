# Structural references through native Talos composition

| | |
|---|---|
| **Date** | 24 September 2026 |
| **Work item** | [Experiment E2 - test structural-reference composition](https://github.com/ginsys/bronzeward/issues/3) |
| **Design reference** | [§6.9 Secret references and validation stages](../Talos_Configuration_and_Machine_Management_Design.md#69-secret-references-and-validation-stages), [§18.1 Phase 0](../Talos_Configuration_and_Machine_Management_Design.md#181-phase-0---evidence-before-implementation-contracts) |
| **Artifacts** | [`experiments/e2-structural-references/`](../../../experiments/e2-structural-references/README.md), evidence under [`experiments/e2-structural-references/evidence/`](../../../experiments/e2-structural-references/evidence/) |
| **Decision enabled** | Whether a reference syntax and resolution order exist that keep native Talos composition, and what each candidate would demand of a renderer. This is the input to the [compiler specification and renderer selection](https://github.com/ginsys/bronzeward/issues/17). No syntax is selected here. |

## 1. Question

Design §6.9 lists three reference candidates (an explicit YAML tag, an opted-in marked string and
an external path binding) and states a preference: "resolving only effective references after
upstream composition if upstream typed machinery permits it". It also requires E2 to "test early
materialization with a recorded provenance superset, non-string targets, overridden references and
embedded content", to "distinguish effective artifact dependencies from dependencies needed to
reproduce the complete source graph", and excludes a custom merge engine: "if no candidate works
within that constraint, revise the reference design before implementation."

The question is therefore which candidate and order give **the same materialized configuration as
native Talos composition of the same fragments written with literal values**, on every case the
issue names, with composition and validation left to the pinned upstream `talosctl`.

| Acceptance criterion | Answered in |
|---|---|
| 1. Every candidate with non-string typed targets, overrides, aliases and embedded documents | §3.1 (the cases), §4 (the matrix) |
| 2. Effective-only dependencies against early-resolution supersets; artifact against source-reproduction dependencies | §6.2 |
| 3. Native merge parity and validation of the materialized output, with collisions and rejections | §4, §6.3 |
| 4. Candidate failures and renderer implications, no unsupported semantics selected | §6.4 |

## 2. Candidates and orders

Every case is written once, with each reference as a canonical `!ref <name>` tag. The prototype
derives four forms of the same intent from it:

| Form | A reference to `reg-pass` in a fragment |
|---|---|
| reference-free (native) | `password: e2-registry-password` |
| tag | `password: !bwref reg-pass` |
| marked | `password: "bwref:reg-pass"`, a reference only in a fragment the case opts in |
| binding | the key is absent; `bindings.tsv` holds fragment, reference, document and path |

Each candidate is resolved in two orders:

- **Late**: compose the candidate's fragments with `talosctl machineconfig patch`, then resolve
  references in the composed configuration. After composition no fragment's opt-in is visible, so
  every `bwref:` string counts, and every binding is applied in fragment order.
- **Early**: resolve each fragment on its own, then compose the resolved fragments. This resolves
  every reference in every fragment: the provenance superset.

## 3. What was built

- **`bwref`** (Go, `gopkg.in/yaml.v3`): generates the four forms, resolves one candidate in one
  order and prints where it resolved, and finds sentinel values after composition. It never merges.
  Only an embedded document the case declares as identified is parsed.
- **`run/all`**: per base, case, candidate and order, composes, resolves, re-reads the result
  through a no-op patch (`machine: {}`) so that native and candidate outputs come from the same
  encoder, compares the output byte for byte with native, and runs `talosctl validate --strict`.
- **`run/collect-evidence`**: copies the committable text into `evidence/` and refuses it if it
  holds a leak-refusal pattern (§3.4).

Outcomes: `parity` (byte-identical to native), `differs`, `rejected` (a `talosctl` decode failure,
at stage `compose` or `normalize`), `refused` (the resolver refused). The validation verdict is a
separate column. Every expectation was written into `case.yaml` before the first run; that is
asserted, not shown by the history, because the cases and the prototype landed in one commit.

### 3.1 Cases

| Case | Target | Kind |
|---|---|---|
| string | registry `auth.password` | string |
| int | `machine.features.kubePrism.port` | integer |
| bool | `machine.install.wipe` | boolean |
| bytes | `machine.acceptedCAs[0].crt` | base64 bytes, inside a list element |
| map | registry `auth` | a whole mapping (a typed struct) |
| override-literal | a reference in fragment 1, a literal at the same path in fragment 2 | override |
| override-ref | a literal in fragment 1, a reference at the same path in fragment 2 | override |
| alias | `&secret` on one registry password, `*secret` on another | alias |
| embedded-yaml | `cluster.inlineManifests[name=e2-secret].contents`, identified YAML | embedded |
| embedded-json | `machine.files[path=/var/etc/e2/config.json].content`, identified JSON | embedded |
| unidentified-embedded | the same manifest, not identified | embedded, opaque |
| document | `certificates` of a separate `TrustedRootsConfig` document | multi-document |
| collision | fragment 1 (not opted in) holds `"!bwref reg-user"` and `"bwref:reg-pass"` as literals; fragment 2 holds a real reference | collision |
| type-mismatch | a string value in the integer port field | rejection |
| invalid-value | `cluster.network.dnsDomain` = `e2 not a domain` | rejection by validation |

The last two are rejection cases: native itself fails, so the expected candidate result is the same
failure. `unidentified-embedded` is a negative control for §6.9's "opaque to all three forms unless
explicitly identified": no candidate can express a reference inside it, so no candidate is expected
to reach parity there.

### 3.2 Bases

The issue binds the experiment to the fixture's real machine configurations. Every case runs on two
bases:

- **fixture**: the control-plane configuration `fixtures/bin/up` read back from the running node,
  validated in `container` mode (the fixture runs Talos in Docker);
- **generated**: `talosctl gen config` output for a control plane with fresh `gen secrets`,
  validated in `metal` mode.

The two bases must agree cell by cell on everything but the message, whose line numbers differ.

### 3.3 Pinned versions

| Component | Version |
|---|---|
| `talosctl` | v1.13.6, SHA-256 `540c5e7cb0d3fa3a9b2e1c717ced212727b73bcaf0cf9cf9ba2472ec381041d4`, from `fixtures/versions.env` as of `c74f953` |
| Go | 1.27.1 |
| `gopkg.in/yaml.v3` | v3.0.1 |

`evidence/run.txt` records the prototype commit of the capture (`dc26305`), zero uncommitted
inputs, and the `talosctl` version and digest. Later commits change only comments in `run/` and
make `collect-evidence` stop on a failed scan instead of reading it as clean; the evidence was
collected again from the same capture with that collector and is byte-identical.

### 3.4 Synthetic values and the leak scan

Every reference value is a synthetic `e2-...` string, integer or boolean in `case.yaml`. The bases
carry real-format but throwaway cluster secrets: the fixture's bundle and a fresh `gen secrets`
bundle. Neither bundle and no materialized output is committed; `evidence/` holds the diffs of each
output against native and against its base, each base with every secret line redacted, and the
SHA-256 of every output.

The leak-refusal patterns are every scalar of 16 characters or more in both secrets bundles, plus
the fixture's own scan list: 35 patterns. Two controls stand behind a clean scan:

- **Key material covered**: every base64 value of 40 or more characters in each base is covered by
  a pattern; the same check with a pattern file that covers nothing finds 11 such lines per base.
- **Scan fires**: `collect-evidence` plants one pattern in a control file and requires the scan to
  report exactly that file before the real scan runs.

Result: `35 patterns, control fired, 0 hits in 752 files` (`evidence/leak-scan.tsv`).

### 3.5 Reproduction

```sh
fixtures/bin/up
E2_OUT=<an empty directory outside the checkout> experiments/e2-structural-references/run/all
E2_OUT=<the same directory> experiments/e2-structural-references/run/collect-evidence
fixtures/bin/down
```

Three captures gave the same matrix: the last before the prototype was committed, one at
`a7f4bb5`, and the collected one at `dc26305`, which differs from `a7f4bb5` only in writing `-` for
an empty cell (§7.6). Between the last two, the fixture base's 88 output digests were identical;
the generated base's 88 differed in every output, as they must, because each run generates a fresh
secrets bundle. Only the collected capture is retained; the comparisons with the earlier two are
recorded here, not in `evidence/`.

## 4. The matrix

15 cases × 7 rows (native plus 3 candidates × 2 orders) × 2 bases = 210 rows, 0 differing from the
expectation, both bases agree (`evidence/summary.txt`). Per base:

| Case | tag late | tag early | marked late | marked early | binding late | binding early |
|---|---|---|---|---|---|---|
| string | differs | parity | parity | parity | parity | parity |
| int | rejected | parity | rejected | parity | parity | parity |
| bool | rejected | parity | rejected | parity | parity | parity |
| bytes | rejected | parity | rejected | parity | parity | parity |
| map | rejected | parity | rejected | parity | parity | parity |
| override-literal | parity | parity | parity | parity | **differs** | parity |
| override-ref | differs | parity | parity | parity | parity | parity |
| alias | differs | parity | parity | parity | parity | parity |
| embedded-yaml | parity | parity | parity | parity | parity | parity |
| embedded-json | parity | parity | parity | parity | parity | parity |
| unidentified-embedded | differs | differs | differs | differs | refused | refused |
| document | differs | parity | parity | parity | parity | parity |
| collision | differs | parity | **differs** | parity | parity | parity |
| type-mismatch (native rejected) | rejected | rejected | rejected | rejected | rejected | rejected |
| invalid-value (native invalid) | differs | parity | parity | parity | parity | parity |

Totals per base: **early** reaches parity on all 13 positive cases for every candidate, matches the
native rejection on type-mismatch, and fails as designed on unidentified-embedded. **Late** reaches
parity on 3 cases with the tag, 8 with the marked string and 12 with the binding.

Three late parities are coincidental and do not count in a candidate's favour:

- **tag late, override-literal**: the tag is stripped (§5 below), but fragment 2's literal overwrites
  the stripped value anyway.
- **binding late, override-ref**: the binding happens to sit in the last fragment, so applying it
  after composition gives the value composition would have kept.
- **binding late, bytes**: the binding's path is `machine/acceptedCAs[0]/crt`, an index into the
  fragment's list applied to the composed list. Neither base has `acceptedCAs`, so index 0 is the
  fragment's element. On a base that already held a CA, the binding would overwrite the base's.

## 5. What each late failure is

These are observed behaviours of `talosctl` v1.13.6 strategic merge, recorded with the exact output.

- **A tag on a string field is dropped silently.** Composition keeps the scalar and loses the tag,
  so the reference name becomes the value (`evidence/cases/generated/string/tag-late/diff-native`):

  ```diff
  -                    password: e2-registry-password
  +                    password: reg-pass
  ```

  The same happens through an alias, in the separate `TrustedRootsConfig` document
  (`certificates: roots`) and on the real reference of the collision case. On invalid-value the
  output becomes `dnsDomain: dns-domain`, which is a valid DNS name, so `validate --strict` passes
  a configuration holding the wrong value. Validation is no guard against this failure.
- **A tag or a marker on a non-string field is rejected by typed decoding before any resolver sees
  it**, as §6.9 anticipated ("upstream typed decoding may reject it before patching"):

  ```text
  cannot construct !bwref `prism-port` into int
  cannot construct !bwref `wipe` into bool
  cannot construct !bwref `reg-auth` into v1alpha1.RegistryAuthConfig
  cannot construct !!str `bwref:p...` into int
  cannot construct !!str `bwref:wipe` into bool
  cannot construct !!str `bwref:r...` into v1alpha1.RegistryAuthConfig
  illegal base64 data at input byte 5
  ```

  (each prefixed `error decoding document /v1alpha1/ (line 1): error decoding to *v1alpha1.Config:
  yaml: construct errors: line <n>:`). A marker on a string field survives composition as a string.

  The rejection is shown for the integer, boolean and struct targets. The bytes rejection is weaker
  evidence: `talosctl` base64-decodes the placeholder text, and fails at byte 5 because the
  reference name `extra-ca` holds a `-`. `yaml.v3` decodes a tagged scalar into a string field
  whatever its tag, so a reference name that happens to be valid base64 would likely compose into
  wrong bytes silently, as on a string field. That was not run; likewise a boolean field would
  accept a marker or tag whose text YAML reads as a boolean.
- **A marker cannot honour a per-fragment opt-in after composition.** In the collision case,
  fragment 1 is not opted in and holds the literal `bwref:reg-pass`; resolved late, it is taken for
  a reference:

  ```diff
  -                    password: bwref:reg-pass
  +                    password: e2-registry-password
  ```

  The tag has no such collision: a quoted `"!bwref reg-user"` is a plain string to YAML and stayed
  literal in every order.
- **A binding applied after composition cannot see fragment order.** In override-literal, fragment 2
  overrides the bound path with a literal; late, the binding from fragment 1 is applied on top of
  it:

  ```diff
  -                    password: e2-override-literal
  +                    password: e2-registry-password
  ```

  Getting this right late means knowing, per path, which fragment wrote last: modelling the merge,
  which §6.9 excludes.

## 6. What this decides

### 6.1 Criterion 1: every candidate on the required cases

All three candidates were run in both orders on integer, boolean, base64-bytes and whole-mapping
targets, on both override directions, through an alias, in identified embedded YAML and JSON, in a
second configuration document, and on collision and rejection cases, on two real bases (§3.1, §4).

### 6.2 Criterion 2: effective and superset dependencies

For each cell the matrix records the **superset** (every reference the source fragments name) and
the **effective set** (the references whose values are in the materialized output):

- **Late**, the effective set is what the resolver resolved. It is correct only where the cell
  reaches parity; in override-literal the binding reports `reg-pass` as effective, and it is not.
- **Early**, resolution necessarily covers the superset, so the effective set had to be measured
  separately: a second pass resolves every reference to a distinguishable sentinel (strings
  `E2SENTINEL` plus six digits, integers from 61000), composes, and finds which sentinels survive.
  This is a measurement method, not a proposed remedy (§6.9: "Dummy substitution is an
  investigation option").

Observed:

- **override-literal, early**: superset `reg-pass`, effective set empty. The artifact depends on no
  secret, yet rendering it from source needs `reg-pass`.
- **alias**: one reference, effective at two paths (both registry passwords).
- **bool**: a boolean sentinel cannot be told from a literal, so the early effective set is
  **unidentifiable** by this method (reported as `+unidentifiable: wipe`). Only the binding's late
  resolution names it. How to derive an effective boolean dependency early is open.

In the matrix's `effective` column, `-` means "measured, none survived" in a cell that composed
(override-literal early) and "not measured" in a rejected or refused cell; the `observed` column
tells them apart.

The distinction the design asks for follows directly:

- **Artifact dependencies** are the effective set: what the published artifact contains, and what
  retention, rotation and lost-dependency classification must track for it.
- **Complete source-reproduction dependencies** are the superset: under early resolution every
  reference in every fragment must be readable to render again from source, including overridden
  ones. A lost overridden secret leaves the artifact intact but blocks re-rendering from source.

### 6.3 Criterion 3: parity, validation, collisions and rejections

- **Parity** is byte-for-byte against the native composition of the same fragments with literal
  values. Each base is itself re-read through the no-op patch before use, so every output is
  `talosctl`'s own encoding. The re-read leaves `talosctl`'s output byte-identical (control on the
  generated base); it reformats the fixture's read-back configuration once (control on the fixture
  base), before any case runs. A late cell's output, which the resolver wrote, goes through the
  same re-read, so the re-read adds no difference of its own.
- **Validation** ran on every materialized output. All 124 parity cells validated as native did,
  with the same message, including invalid-value, where native and every parity cell fail with:

  ```text
  1 error occurred: * v1alpha1.Config: 1 error occurred: * "e2 not a domain" is not a valid DNS name
  ```

- **Rejections**: type-mismatch is rejected natively with ``cannot construct !!str `e2-not-...`
  into int``. Every early cell and the binding late are rejected with the same message; the
  binding late at the re-read rather than at composition, and so at a different line of the
  document, because the key it fills is absent from its fragments. The tag and marked late are rejected on their own placeholder instead (§5). The
  resolver refusal, binding only:

  ```text
  bwref: binding v1alpha1 cluster/inlineManifests[name=e2-secret]/contents|yaml/stringData/password:
  cluster/inlineManifests[name=e2-secret]/contents is not an identified embedded document
  ```

- **Collisions**: §5 above. Only the marked string late misread a literal.

### 6.4 Criterion 4: candidate failures and renderer implications

**Late resolution fails for every candidate on at least one required case**, so §6.9's preferred
order ("if upstream typed machinery permits it") is not available within the no-merge-engine
constraint:

| Candidate | Late failure | Why it is not fixable in the renderer without a merge engine |
|---|---|---|
| tag | dropped silently on strings; rejected on integer, boolean and struct fields | composition discards it before the renderer runs |
| marked | rejected on integer, boolean and struct fields; misreads literals | typed decode runs first; opt-in is a fragment property composition erases |
| binding | wrong value when a later fragment overrides; a list index names the fragment's list, not the composed one | needs per-path last-writer knowledge |

**Early resolution reaches native parity for all three candidates.** It hands `talosctl` only
reference-free fragments, so upstream composition and validation are untouched. What each candidate
then demands of a renderer:

- **tag**: a YAML parser that keeps tags, since resolution must happen before any typed decode.
  Quoted text that looks like a tag is safely literal. An unresolved tag inside unidentified
  embedded text is left in the output as `!bwref app-pass` and **validation passes**.
- **marked**: a per-fragment opt-in declaration that the renderer honours before composition. Its
  collision defence exists only early. Like the tag, an unresolved marker inside unidentified text
  is left in the output and validation passes.
- **binding**: bindings keyed by fragment, document and path, kept in step as fragments are edited
  (list selectors such as `[name=e2-secret]` included). It is the only candidate that **refuses** a
  reference into unidentified embedded content instead of leaving reference text behind. (Resolved
  late, a fragment that holds only bound values is empty, and `talosctl` refuses an empty patch, so
  such a fragment must be left out of composition: §7.1. Early, each fragment is refilled first.)

For identified embedded documents, every candidate re-serializes the whole document it resolves
into: the prototype writes JSON with sorted keys and no whitespace. The native literal form was
derived through the same encoder, so embedded-json's parity does not show that an author's
hand-formatted literal would survive; it would differ in formatting. The tag form of the embedded
JSON (`"token": !bwref app-token`) is not JSON either; the prototype parses it as YAML.

For every candidate, early resolution means the compiler identity must read the whole superset
(§6.2), and the release must record the effective set separately if dependency tracking is to be
exact.

## 7. Failures hit while building it

### 7.1 An emptied binding fragment is not a patch

The first capture had 5 unexpected rows, all `binding late`, all `rejected` with
`config not found`: a fragment whose only content was bound became `{}`, which `talosctl` refuses
as a patch. This was a runner defect, not a candidate result. `run/all` now leaves an empty fragment
out of composition and records that it did (`*.skipped`).

### 7.2 An emptied mapping re-encoded in flow style

Deleting every bound key under `stringData` left `stringData: {}`, which re-encoded in flow style
and, once refilled by the binding, broke early parity. The prototype now prunes mappings that only
bound keys filled, and clears flow style on a mapping it adds a key to.

### 7.3 The superset column read the wrong field

The first capture listed fragment names, not references, in the superset column. Fixed before any
result was read from it.

### 7.4 Leak patterns too short, then too wide

A 24-character floor missed the 23-character bootstrap token; the floor is now 16. Taking every
long scalar of each base as a pattern then redacted image references and URLs as well; patterns
now come from the secrets bundles and the fixture's list, and the key-material control (§3.4) shows
no key-like base value is left uncovered. That control first passed silently when its own `awk`
failed; it now requires the no-pattern run to find lines.

### 7.5 `fixtures/bin/up` has no help option

`fixtures/bin/up --help` ignores its argument and starts the fixture. The pipe it was run through
killed it part-way, leaving a partial fixture; `fixtures/bin/down` removed it cleanly, and a normal
`up` followed. No evidence came from the partial fixture.

### 7.6 The evidence failed the whole-tree whitespace check

An empty message cell ended every such matrix row in a tab, the `talosctl` version line ended in a
space, and `talosctl validate` ends its message with a blank line. The first two are the runner's
own formats and were fixed; the validation messages are kept verbatim and exempted in
`.gitattributes`, as E5's transcripts are.

## 8. Limits

- **One Talos version.** Every verdict rests on `talosctl` v1.13.6. Other contracts are not present
  and no candidate result may be generalised to them.
- **Strategic merge only.** JSON6902 is refused for the v1.13 multi-document configuration
  ([fixtures report §5](20260919-investigation-fixtures.md#5-failures-hit-while-building-it)); no JSON patch was tried.
- **Fifteen cases, one machine type.** Both bases are control-plane configurations. Not covered:
  worker configurations, duration and IP/CIDR fields, a list as a reference target, list-element
  overrides (the bytes case's list merged into a base with no such list, §4), `$patch: delete`,
  more than two fragments, and a tag whose reference name is valid base64 or reads as a boolean
  (§5).
- **The fixture base validates in container mode.** Its read-back configuration is a Docker node's;
  `metal` validation applies only to the generated base.
- **Boolean effective dependencies are unmeasured early** (§6.2).
- **The resolver is disposable.** Its choices (per-fragment opt-in, path syntax, alias handling by
  replacing the anchor node) are this experiment's, not a proposed design.
- **Error messages carry value prefixes.** `talosctl`'s decode errors quote the first characters of
  the offending value (`e2-not-...`, `bwref:p...`). A secret placed in a wrongly typed field would
  leak its first seven characters into a validation error. This belongs to
  [sensitivity and provenance tracking](https://github.com/ginsys/bronzeward/issues/4).

## 9. Alternatives and decision enabled

Compared: tag, marked string and binding, each resolved late and early; the reference-free literal
form is the baseline. Not compared: a custom merge engine (excluded by §6.9), general interpolation
(excluded), substring references in opaque text (§6.9 references whole content initially).

Decision enabled: **§6.9's preferred late order is ruled out on this evidence, and early
materialization with a recorded superset is the order that keeps native composition for all three
syntaxes.** The syntax choice is not decided by composition parity, because all three reach it
early; it turns on the renderer implications in §6.4, which the
[compiler specification](https://github.com/ginsys/bronzeward/issues/17) must weigh. Adopting early
resolution is a change to §6.9's stated preference and belongs in the design once accepted.

## 10. Hand-off

**To [sensitivity and provenance tracking](https://github.com/ginsys/bronzeward/issues/4):**

- Every resolution is recorded as reference, document and path (`resolutions.tsv`), and each early
  cell has the sentinel paths that survived composition (`sentinel-found.tsv`). An alias puts one
  reference at two paths.
- `talosctl` decode errors quote a value prefix (§8).
- An unresolved tag or marker inside unidentified embedded text passes validation (§6.4).

**To the [compiler specification](https://github.com/ginsys/bronzeward/issues/17):** §6.4 and §9.
Open there: the effective-dependency method for booleans, whether a renderer must refuse leftover
reference text, and how bindings follow fragment edits.
