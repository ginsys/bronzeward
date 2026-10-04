# C10 machinery gate (SR half)

Evidence for the condition in [compilation §10.1](../../docs/spec/compilation.md#101-selection-the-go-machinery-in-process),
for [issue 23](https://github.com/ginsys/bronzeward/issues/23): SR's matrix re-run with
composition through the compiler's own machinery path, which SR only inferred. SP's matrix is not
run here; until it is, the §10.1 condition is not met.

## What it measures

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

## Bases and versions

- `fixture`: the running fixture's control-plane configuration (`fixtures/bin/up`), container mode.
- `generated`: `talosctl gen secrets` and `gen config` output for a metal node, without docs and
  examples, metal mode.

Both are re-read through talosctl's no-op patch, as SR did. talosctl is the pinned v1.13.6 from
[`fixtures/versions.env`](../../fixtures/versions.env), digest-checked; the machinery is the
root module's v1.13.6. `evidence/run.txt` records the commit and every version.

## Running it

```sh
fixtures/bin/up
C10_OUT=/an/empty/dir/outside/the/checkout experiments/c10-machinery-gate/run/all
C10_OUT=/the/same/dir experiments/c10-machinery-gate/run/collect-evidence
```

`run/all` reuses SR's `run/lib.sh` (pinned talosctl, SR's prototype for the literal and tag
forms) and runs `TestMachineryGate` in `internal/compile` with `C10_OUT` set; without it the test
is skipped. The test writes `matrix.tsv`, `controls.tsv` and `summary.txt` and fails on any
unexpected row. `run/all` also records the run's leak-refusal patterns (every long scalar of either
base's secrets bundle and the fixture's leak-scan patterns) in `C10_OUT/patterns.txt`.
`run/collect-evidence` copies those results and `run.txt` into `evidence/` only if every file scans
clean against that record; a match or a failed scan refuses and copies nothing. The outputs
themselves hold the bases' synthetic secrets and stay in `C10_OUT`.

## Result

`evidence/summary.txt`: 15 cases on both bases, 30 rows, 0 unexpected.

- The machinery's composition equals talosctl's byte for byte, with the same verdict, in all 28
  composed rows; both reject `type-mismatch`.
- The compiler reaches parity in the 12 cases SR found at parity early, including the validation
  failure of `invalid-value`, and rejects `type-mismatch` at composition as talosctl does.
- `collision` and `unidentified-embedded` are refused at authoring with `reserved-text`, as the
  contract requires.

## Limits

- SR's cases only: SR's own kinds-not-run list stands (compilation §15).
- SR's case references stand in fragments only. The bases carry references only for the schema
  secrets ingestion extracts from them.
- Composition is in process; the compiler process's surfaces are not scanned here (§15).
- One machinery minor (v1.13.6).
