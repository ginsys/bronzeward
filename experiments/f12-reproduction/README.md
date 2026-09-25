# Reproduction runs for the feasibility evidence review

The [feasibility evidence review](../../docs/design/research/20260925-feasibility-evidence-review.md)
§7 re-ran three landed experiments to check the results it calls disputed or high-risk. This
directory keeps the part of each re-run's collected evidence that those results rest on. Nothing
here is a new experiment: every file was produced by the experiment's own harness, unchanged, at
`main` 41c995c with 0 uncommitted inputs, on 25 September 2026.

## What was run

One fixture at a time, each following its experiment's README:

| Experiment | Commands | Rows / mismatches |
|---|---|---|
| [database semantics](../e4-database-semantics/README.md) | `fixtures/bin/up`; `E4_OUT=<new empty dir> experiments/e4-database-semantics/run/all`; `fixtures/bin/evidence <E4_OUT>`; copy the bundle into `<E4_OUT>/bundle`; `E4_OUT=<E4_OUT> experiments/e4-database-semantics/run/collect-evidence`; `fixtures/bin/down` | 63 / 0 |
| [dispatch safety](../e4-dispatch-safety/README.md) | the same shape with `E4D_OUT` and `experiments/e4-dispatch-safety/run/` | 23 / 0 |
| [provider capabilities](../e5-provider-capabilities/README.md) | `fixtures/bin/up`; with `E5_OUT=<dir>`: `run/test-lib`, `run/all`, `run/collect-evidence`; `fixtures/bin/down` | 194 / 0 |

Every step exited 0. `collect-evidence` writes into the experiment's own `evidence/`; that output
was copied here and the committed evidence left as it was.

## Files

- `evidence/e4-database-semantics/`: `cells.tsv`, `run.txt`, `bundle-summary.txt`, and the S2, S5 and
  S7 transcripts (rows 020, 048, 061, 027 and 055).
- `evidence/e4-dispatch-safety/`: `cells.tsv`, `run.txt`, `bundle-summary.txt`, `injections.log`,
  and the fault and revocation transcripts (rows 003 and 012–017).
- `evidence/e5-provider-capabilities/`: `cells.tsv`, `versions.txt`, `leak-scan.txt`, and the row
  149 transcript.
- `evidence/SHA256SUMS`: a digest of each file above.

Each experiment's leak scan matched only what its report documents: for database semantics the
control, its copy inside the S7 store backup, and the pattern list; for dispatch safety the control, the
pattern list and the applied artifacts in `E4D_OUT`; for provider capabilities the 3 controls and
nothing else (`bundle-summary.txt` and `leak-scan.txt` here). The fixture bundles and the applied
artifacts hold synthetic secrets and are not kept.

## Reading them

Compare each file with the same path under the experiment's own `evidence/`. A harness row passes
when every asserted cell matches; `seen:` cells are recorded, not asserted, so a row can match while
a recorded value differs. The review's §7 lists every difference that bears on a conclusion.
