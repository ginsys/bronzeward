# C10 machinery gate

Evidence for the condition in [compilation §10.1](../../docs/spec/compilation.md#101-selection-the-go-machinery-in-process),
for [issue 23](https://github.com/ginsys/bronzeward/issues/23): SR's and SP's matrices re-run
with composition through the compiler's own machinery path, which SR and SP only inferred.

## What it measures

### SR half

On each of SR's two bases and for each of SR's 15 cases
([E2 structural references](../e2-structural-references/README.md)):

| Column | What runs |
|---|---|
| `talosctl` | The pinned `talosctl machineconfig patch` of the case's literal (reference-free) fragments onto the base, then `talosctl validate --strict` in the base's mode: `pass`, `invalid` or `rejected`. This is what SR measured. |
| `machinery` | The same composition and verdict in process, through the machinery's `configpatcher` (`LoadPatch`, `Apply`, re-encoded without comments) and `Validate` (local, strict). |
| `bytes` | Whether the two outputs are byte-identical. |
| `observed` | The compiler on SR's tag form: each fragment and the base read and extracted by `internal/ingest` (schema secrets become references too), resolved, composed by `internal/compile` and compared with talosctl's output: `parity`, `differs`, `rejected` (the machinery refused to compose) or `refused` (a bronzeward rule refused it). |
| `validate` | The compiler's verdict on its own output, beside talosctl's (`native-validate`). |

`expected` is SR's tag/early expectation, except for `collision` and `unidentified-embedded`:
a quoted reference look-alike and a reference inside unidentified embedded text are reserved
text, refused at authoring by contract (compilation §5.5, choice §16.19); those two rows must be
refused at authoring with ingestion's `reserved-text` rule, not by any other stage or rule. A row
is unexpected if the machinery and talosctl disagree in outcome, bytes or verdict, if the
compiler's outcome is not the expected one, if a parity output's verdict differs from talosctl's,
or if an expected authoring refusal came from another stage or rule.

Controls, per base: the base alone through ingestion and the compiler equals the base talosctl
normalized, and validates in its mode.

### SP half

For each of SP's 28 cases
([E2 sensitivity and provenance](../e2-sensitivity-provenance/README.md)), SP's real pass as
SP's own tool derives it (`bwprov derive`: SR's fragments or the case's own, with the case's
synthetic values placed), on both bases, in two cells:

| Cell | What runs |
|---|---|
| `fragments` | The case as SP ran it, references in fragments. `talosctl`, `machinery` and `bytes` as in the SR half; the compiler must reach talosctl's outcome (parity with its bytes), SP's effective and overridden references at the case's versions, and SP's message outcome for the patch and validate steps (`none`, `verbatim`, `redacted`; a `withheld` message meets `redacted`, compilation §8.3). |
| `base-ref` | The fragments up to the first one holding a case reference, composed with the base and moved into the import base the way a reference reaches one (compilation §2.3): the composition ingested with a mark at each output path provenance gave their references (a mapping marked whole, identified embedded documents declared), then the remaining fragments compiled on it. It must reach the `fragments` cell's bytes, or be refused `base-override` where a later fragment overrides what moved (choice §16.20). |

In every cell, SP's oracle, ported to Go with its own form tests, must find none of the case's
values and none of the base bundle's secrets (in any of SP's forms: exact, placed, escaped,
base64, line, prefix, fragment, keyed) in anything the compiler renders: the redacted
configuration, the error as the server log prints it (`%v`) and under `%+v` and `%#v`, the
compiled value under every verb, and the provenance, effective and reproduction records under
`%+v` and as JSON.

By contract, four cases are expected refused rather than at SP's verdict:

- `collision` and `unidentified-embedded`: `reserved-text` at authoring, as in the SR half.
- `duplicate-literal`: `exact-copy` (choice §16.21); a value also written as a literal is a
  source-side exposure. SP's case is the evidence that only value matching sees it.
- `map-partial`: `exact-copy`. The literal that overrides one member of a mapping reference
  writes that member's key, a value the provider holds (§4.2), in plaintext. The remedy is to
  override the whole mapping.

Controls, per base: the `moved` pair's output paths differ and the `rotate` pair's effective
versions differ, each cell at its case's expectations; and the oracle finds a value planted in a
scanned text, so a clean scan is not vacuous.

## Bases and versions

- `fixture`: the running fixture's control-plane configuration (`fixtures/bin/up`), container mode.
- `generated`: `talosctl gen secrets` and `gen config` output for a metal node, without docs and
  examples, metal mode.

Both are re-read through talosctl's no-op patch, as SR did. talosctl is the pinned v1.13.6 from
[`fixtures/versions.env`](../../fixtures/versions.env), digest-checked; the machinery is the
root module's v1.13.6. `evidence/run.txt` records the commit and every version. The SP half's
base secrets are each bundle's key, secret and token scalars, as SP reads them.

## Running it

```sh
fixtures/bin/up
C10_OUT=/an/empty/dir/outside/the/checkout experiments/c10-machinery-gate/run/all
C10_OUT=/the/same/dir experiments/c10-machinery-gate/run/collect-evidence
```

`run/all` reuses SR's `run/lib.sh` (pinned talosctl, SR's prototype for the literal and tag
forms), builds SP's `bwprov`, and runs `TestMachineryGate` and `TestSensitivityGate` in
`internal/compile` with `C10_OUT` set; without it both are skipped. They write `matrix.tsv`,
`controls.tsv` and `summary.txt` (SR) and `sp-matrix.tsv`, `sp-controls.tsv` and
`sp-summary.txt` (SP), and fail on any unexpected row. `run/all` also records the run's
leak-refusal patterns (every long scalar of either base's secrets bundle and the fixture's
leak-scan patterns) in `C10_OUT/patterns.txt`. `run/collect-evidence` copies those results and
`run.txt` into `evidence/` only if every file scans clean against that record and for `BWSYNTH`,
the marker every SP value carries; a match or a failed scan refuses and copies nothing. The
outputs themselves hold synthetic secrets and stay in `C10_OUT`.

## Result

`evidence/summary.txt`: SR's 15 cases on both bases, 30 rows, 0 unexpected.

- The machinery's composition equals talosctl's byte for byte, with the same verdict, in all 28
  composed rows; both reject `type-mismatch`.
- The compiler reaches parity in the 12 cases SR found at parity early, including the validation
  failure of `invalid-value`, and rejects `type-mismatch` at composition as talosctl does.
- `collision` and `unidentified-embedded` are refused at authoring with `reserved-text`, as the
  contract requires.

`evidence/sp-summary.txt`: SP's 28 cases on both bases, 112 rows, 0 unexpected.

- The machinery's composition equals talosctl's byte for byte, with the same verdict, in all 52
  composed `fragments` rows (48 pass, 4 invalid); both reject the two type-mismatch cases.
- The compiler reaches parity in 20 cases on both bases, with SP's effective and overridden
  references at the case's versions; it reaches the validation failure of `invalid-value` and
  `list-duplicate` with SP's message outcome, and rejects the two type-mismatch cases at
  composition with the patch message redacted.
- The four by-contract cases are refused as above.
- `base-ref`: 38 cells apply; 32 reach the `fragments` cell's bytes and 6 (`delete`,
  `override-literal`, `ref-over-ref`) are refused `base-override`. 18 do not apply: 16 whose
  `fragments` cell is not at parity (rejected, invalid or refused), and `bool`, whose marked `true` equals other
  `true` scalars in the base (ingestion's guard, §4.2's accepted cost).
- The oracle finds nothing in any of the 112 cells, and both pairs and the planted-value control
  pass on both bases.

## Limits

- SR's and SP's cases only: their kinds-not-run lists stand (compilation §15).
- References reach a base only by marks on a composition the compiler produced; a hand-written
  import base with references is not run.
- SP's pair controls compared bwprov's diffs; bronzeward has no diff yet, so the pairs check
  versions and output paths only.
- Composition is in process; the compiler process's other surfaces (publications, their
  temporary files, the publish worker's log) are not scanned here (§15).
- One machinery minor (v1.13.6).
