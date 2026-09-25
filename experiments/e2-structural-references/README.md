# E2 structural-reference experiment

**This is Phase-0 evidence code for [issue 3](https://github.com/ginsys/bronzeward/issues/3). It is
not the v1 implementation and it will not be maintained past the research report it produces:
[structural-reference composition](../../docs/design/research/20260924-structural-reference-composition.md).**

## What it is for

Design §6.9 asks for a reference syntax and a resolution order that survive native Talos
composition. This experiment tries three candidates — an explicit YAML tag, an opted-in marked
string and an external path binding — resolved either late (on the composed configuration) or
early (on each fragment, before composition), against the pinned `talosctl` from
[`fixtures/versions.env`](../../fixtures/versions.env), and compares every materialized output with
the native composition of the same fragments written with literal values.

It answers a feasibility question. It selects no syntax and establishes nothing about how v1 will
be built.

## What is here

- `cases/<name>/` — one case each: `case.yaml` (the values, the fragment order, the declared
  embedded documents, the native expectation and one expectation per candidate and order, written
  before the first run) and its fragments. Fragments are written once, with every reference as a
  canonical `!ref <name>` tag; the prototype derives the reference-free form and each candidate's
  form from them, so all four are the same intent.
- `bwref` (the Go files) — the resolver prototype. It never merges. `bwref gen` writes the four
  forms, `bwref resolve` substitutes one candidate's references and prints where it did,
  `bwref find` finds sentinel values after composition, `bwref patterns` lists leak-refusal
  patterns. The usage is at the top of `main.go`.
- `run/all` — the whole matrix. `run/collect-evidence` — copies the committable text into
  `evidence/`.
- `evidence/` — the collected run; `matrix.tsv` is the table the report cites.

The candidate forms, as `bwref gen` writes them for a reference named `reg-pass`:

| Form | In the fragment |
|---|---|
| reference-free (native) | `password: e2-registry-password` |
| tag | `password: !bwref reg-pass` |
| marked | `password: "bwref:reg-pass"`, honoured only in a fragment the case opts in (`marked: true`) |
| binding | the key is absent (a list element whose only key is bound stays as `{}`); `bindings.tsv` holds `f1.yaml reg-pass v1alpha1 machine/registries/.../password` |

## Running the matrix

The fixture must be up (`fixtures/bin/up`): one of the two bases is the fixture's own control-plane
configuration, as `up` read it back from the node. The other is `talosctl gen config` output for a
metal node. `E2_OUT` must name an empty directory outside this checkout; the run writes a few MiB.

```sh
E2_OUT=<an empty directory> experiments/e2-structural-references/run/all
E2_OUT=<the same directory> experiments/e2-structural-references/run/collect-evidence
fixtures/bin/down
```

`E2_OUT` holds both bases and both secrets bundles, all synthetic, so it has no default and may not
be inside the checkout. Deleting it afterwards is part of finishing the run.

Per case and base, `run/all` composes the reference-free fragments natively (`machineconfig patch`
onto the base, in fragment order), then for each candidate:

- **late**: composes the candidate's fragments, resolves the composed configuration, and re-reads
  the result through a no-op patch so that it is written by the same encoder as native;
- **early**: resolves each fragment on its own, then composes the resolved fragments; a second pass
  with sentinel values in place of the real ones measures which references survive composition.

Each output is compared byte for byte with native (`parity` or `differs`) and validated
(`validate --strict`, metal mode for the generated base, container mode for the fixture's
docker-mode one). A resolver refusal is `refused`; a `talosctl` decode failure is `rejected`, with
the stage (`compose`, or `normalize` for the re-read). A fragment the binding form empties entirely
is left out of composition, because `talosctl` refuses an empty patch ("config not found").

Controls, all in `controls.tsv`: the no-op patch leaves `talosctl`'s own output byte-identical; each
base validates; every key-like value in each base is covered by a leak-refusal pattern, and the same
check with no patterns finds them; and `collect-evidence` plants one pattern in a control file and
requires the scan to find it before the real scan runs.

## What evidence/ holds and does not

It holds the matrix, the summary, the controls, the run record (commit, uncommitted inputs, tool
versions), every generated fragment form, every message, every resolution and sentinel list, each
output's diff against native and against its base, each base with its secret lines redacted, and
the SHA-256 of every materialized output. It does not hold the outputs themselves or either
secrets bundle: those carry synthetic keys and stay in `E2_OUT`. A base's redacted copy plus a
cell's `diff-base` is that cell's output, less the redacted lines. An empty file is not collected.

Per-case files are packed, one text file per case: `gen/<case>.txt` (the fragment forms, bindings
and superset) and `cases/<base>/<case>.txt` (per cell: messages, `resolutions.tsv`,
`sentinel-found.tsv`, `diff-native`, `diff-base`, `validate.txt`, `*.skipped`). Each file starts
after a `==> <cell>/<file> <==` line; the pack ends with `==> end <==`. One file per cell would be
about 750 files, and GitHub serves no pull-request diff of more than 300 files, so no review could
read it.
