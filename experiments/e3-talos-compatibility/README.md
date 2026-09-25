# E3 Talos compatibility experiment

**This is Phase-0 evidence code for [issue 5](https://github.com/ginsys/bronzeward/issues/5). It is
not the v1 implementation and it will not be maintained past the
[research report](../../docs/design/research/20260925-talos-compatibility.md) it produces.** It
selects no Talos client library and no supported-version policy.

## What it is for

The PoC generates, validates and applies Talos machine configurations for a declared contract.
This experiment measures, for six renderer versions ([`versions.env`](versions.env)), what each
does with each target contract, through two implementations of the same operation:

- **talosctl as a subprocess:** that release's `talosctl`, downloaded once and checked against the
  SHA-256 pinned for it in [`versions.env`](versions.env);
- **the Go machinery:** `e3m` ([`machinery/src/`](machinery/src/)), one source built against the
  same tag's `pkg/machinery` module. Each version has its own module under `machinery/<version>/`,
  whose `.go` files are links to `src/`. Two shims differ by version: `secrets.Bundle.Validate`
  takes the version contract from v1.14 on (`bundle_v114.go`), and takes none before
  (`bundle_v112.go`).

| Group | What it records |
|---|---|
| gen | every renderer x target (`current`, v1.10.0 .. v1.15.0, and the version-string edges `1.13`, `v1.13.6`, `v1.15.0-alpha.0`, `v1.99.0`, `bogus`) x Kubernetes (the renderer's default, and the fixture's 1.36.2): both implementations' exit and output, the output digests, whether they are identical, and whether talosctl repeats itself |
| validate | every generated configuration (Kubernetes 1.36.2, `current` and the six contracts) validated by every renderer: one row per configuration and validator, holding four cells (talosctl and machinery, each in container and metal mode) |
| strict | each renderer's own output with `--strict` |
| policy | the Kubernetes and upgrade windows each machinery's `compatibility` package encodes |
| rpc-client | each renderer as a client of the fixture's v1.13.6 worker, through both: version, read the configuration, a dry-run apply and a real apply of a label patch in no-reboot mode |
| rpc-contract | every renderer's worker configuration for every contract, as a no-reboot dry run through the pinned client, both implementations |
| controls | `bogus` refused everywhere; generation repeatable; a different input compares unequal; an invalid configuration refused by every validator and by the node |
| tests | `e3m`'s own tests |

## How it is judged

The gen, validate, strict and policy groups are measurements: what a renderer does is the
finding. Their tables cite each distinct output once, by id, in `messages.tsv`, with any path
under `E3_OUT` replaced by `<file>`.

The rpc and control groups are rows, each with its expectation written in [`run/all`](run/all)
before the run and compared by [`run/lib.sh`](run/lib.sh) with what the harness reads back through
the fixture's pinned `talosctl`: the worker's configuration digest and its machine-configuration
resource version. The rpc-client rows expect every client to work; a mismatch there is a finding.
A control row proves a check can fail; a mismatch there makes the run inconclusive: `run/all`
exits non-zero and `run/collect-evidence` refuses the run.

**Digest normalization.** A digest is the SHA-256 of the configuration with its trailing newlines
replaced by exactly one, as the fixture's own normalization (`fixtures/bin/selftest`) and `e3m`'s
`digest` compute it. "Identical" in the tables means identical after this normalization.

**Secrets.** Every configuration is generated from the fixture cluster's own secrets bundle, so
that it could be applied to the worker. The configurations, the raw outputs and every read of the
worker stay in `E3_OUT`. A transcript keeps an apply's output up to its first diff line, and a
read's standard error only; `e3m` withholds the diff from an error the same way. What is committed of a configuration is its digest and its key-path
differences (`e3m paths`), never a value.

## Running it

A capture needs a fresh fixture, and `E3_OUT` must name a new, empty absolute path on disk,
outside this checkout:

```sh
fixtures/bin/up
E3_OUT=<somewhere with ~1 GiB> experiments/e3-talos-compatibility/run/all
fixtures/bin/evidence <the same directory>
cp -a fixtures/.state/evidence/<bundle> <the same directory>/bundle
fixtures/bin/down
E3_OUT=<the same directory> experiments/e3-talos-compatibility/run/collect-evidence
```

`run/all` downloads the six renderers' `talosctl` into `fixtures/.cache` (about 610 MiB, once;
`fixtures/bin/down --purge` removes them), copies them and six `e3m` builds into `E3_OUT/bin`
(most of its size), and runs the matrix in under two minutes once the fixture is up. `run.txt` records the commit, every
uncommitted input, `started` and `finished`. `run/collect-evidence` copies the text evidence into
[`evidence/`](evidence/), packs the transcripts and the key-path differences one file per group,
rewrites local paths to placeholders, and refuses the result if it holds any of the fixture's
leak-scan patterns or a path under the operator's home. A planted control proves the scan matches
first. Delete `E3_OUT` once the evidence has landed.

`E3_ONLY=<extended regex>` runs only the matching groups, for development; `collect-evidence`
refuses such a run.
