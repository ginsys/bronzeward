# E2 sensitivity and provenance experiment

**This is Phase-0 evidence code for [issue 4](https://github.com/ginsys/bronzeward/issues/4). It is
not the v1 implementation and it will not be maintained past the research report it produces:
[sensitivity and provenance tracking](../../docs/design/research/20260925-sensitivity-provenance.md).**

## What it is for

Design §6.9 requires that a referenced secret stay redacted through composition: in diffs,
validation errors, logs and support data. This experiment asks whether that redaction can follow
from provenance — which output leaf a reference's value became — rather than from matching the
value's text, and records what each representation leaks.

It builds on [issue 3's experiment](../e2-structural-references/README.md), unchanged: its resolver
`bwref` is built from that directory, and fifteen of the cases here reuse its cases' fragments.
Resolution is early (per fragment, before composition), as issue 3 found; `talosctl machineconfig
patch` (strategic merge, the pinned version from [`fixtures/versions.env`](../../fixtures/versions.env))
is the only thing that composes. Nothing here merges.

## What is here

- `cases/<name>/case.yaml` — one case each: either `from: <issue-3 case>` or its own fragments, and
  the expectations written before the first run (which representations leak, the premise per
  candidate, the effective, overridden and unresolved references, the message outcomes).
- `bwprov` (the Go files) — the tracker and redactor. `bwprov derive` writes, per case, the real
  pass, the trace pass (every reference occurrence renamed and given a tracer value of the same
  shape) and one flip pass per boolean tracer. `bwprov analyse` finds the tracers in the trace
  composition, maps each to the real composition's leaf, and writes every representation's
  artifacts, the oracle's findings, the expectations and the controls. `bwprov pair` diffs two
  revisions; `bwprov summary` merges every unit and decides completeness.
- `run/all` — the whole matrix on both bases. `run/collect-evidence` — copies the committable text
  into `evidence/`.
- `evidence/` — the collected run; `summary.txt` is the table the report cites.

The representations, from none to all:

| Representation | Redacts |
|---|---|
| `none` | nothing |
| `value` | every text occurrence of a known secret value (the baseline) |
| `resolution-path` | the leaf each fragment's resolution wrote, at that fragment's path |
| `schema` | every field the Talos machinery marks secret |
| `composed-path` | the output leaf each reference's value became, found by tracer |
| `path+schema` | both of the above |
| `path+schema+value` | all three |

## Running the matrix

The fixture must be up: one base is the fixture's own control-plane configuration. The other is
`talosctl gen config` output for a metal node. `E2SP_OUT` must name an empty directory outside this
checkout; the run writes about 80 MiB, half of it the two built binaries.

```sh
fixtures/bin/up
E2SP_OUT=<an empty directory> experiments/e2-sensitivity-provenance/run/all
fixtures/bin/evidence <E2SP_OUT>/artifacts/none <E2SP_OUT>/artifacts/composed-path \
  <E2SP_OUT>/artifacts/path+schema+value
# copy the bundle fixtures/bin/evidence names to <E2SP_OUT>/fixture-bundle/
fixtures/bin/down
E2SP_OUT=<the same directory> experiments/e2-sensitivity-provenance/run/collect-evidence
```

`E2SP_OUT` holds both secrets bundles and every unredacted artifact, all synthetic, so it has no
default and may not be inside the checkout (it is compared canonical, at a path boundary). `run/all`
refuses a non-empty directory and `collect-evidence` one without the marker `run/all` writes, so
two runs cannot mix. Deleting it afterwards is part of finishing the run.

A run is complete only when every case and pair produced a unit on both bases, every tracer
composition reproduced the real one's shape, and every control that must fire fired; otherwise
`run/all` writes `incomplete` and exits non-zero, and `collect-evidence` refuses the directory. An
observation that differs from its expectation is a result, counted as `unexpected` in the summary,
not a failure of the run.

Controls, in `controls.tsv`: a corrupted tracer must fail the fidelity check; a template check per
talosctl step must redact a message that quotes a value; a per-side diff must expose a base value
that the paired diff hides; the stale-provenance controls on the revision pairs must fire; the
trace pass must hold no secret, and the base no case value; the schema must cover each base's
secrets; every configuration re-rendered with nothing redacted must be the text talosctl or bwref
wrote; the trace pass must resolve the same paths as the real one. `collect-evidence` plants one pattern in a control file and requires the scan to find
it before the real scan runs.

## What evidence/ holds and does not

It holds the summary and the merged tables (`expectations.tsv`, `controls.tsv`, `leaks.tsv`,
`pair-leaks.tsv`, `cells.tsv`, `provenance.tsv`, `dependencies.tsv`), the base controls, the run
record, the SHA-256 of every composition, one pack per case (`cases/<case>.txt`: each base's diff,
errors, log and support data under `path+schema+value`) and one per revision pair
(`pairs/<pair>.txt`: the `path+schema+value` and `composed-path` diffs, controls and expectations),
and the fixture leak scan's per-representation counts (`fixture-scan.tsv`, corroboration only).

It does not hold any other representation's artifacts, the compositions or either secrets bundle:
they carry synthetic secrets and stay in `E2SP_OUT`. Certificates and other non-secret bundle
values in the packs are masked as `<masked:bundle-public>`, counted in `leak-scan.tsv`. Each packed
file starts after a `==> <base>/<file> <==` line; a pack ends with `==> end <==`.
