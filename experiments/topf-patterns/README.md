# Pinned topf compatibility experiments

Research for [#164](https://github.com/ginsys/bronzeward/issues/164); the
[assessment](../../docs/design/research/20261020-topf-patterns.md) owns recommendations
and limits. These are selected upstream unit tests and original Go-overlay probes.
They do not run topf's CLI, integration suite, a cluster or real credentials.

## Reproduce

Requires Git, Python 3.10+, GNU `timeout`, native Go 1.27.1 and a C compiler for
the masking group's race detector. Public Go module downloads require network
access. Use disposable disk-backed paths outside both repositories:

```sh
research_root=/path/on/disk/topf-research
mkdir -p "$research_root/gotmp" "$research_root/gocache" "$research_root/gomodcache"
git clone https://github.com/postfinance/topf.git "$research_root/source"
git -C "$research_root/source" checkout --detach 7aeead7545d6a715e719e2e6241027792f7e1a89
export TMPDIR="$research_root/gotmp" GOTMPDIR="$research_root/gotmp"
export GOCACHE="$research_root/gocache" GOMODCACHE="$research_root/gomodcache"
python3 experiments/topf-patterns/run.py --go /path/to/go1.27.1/bin/go \
  --source "$research_root/source" --output "$research_root/run1"
```

Output must not exist; the source must match the pin and contain no untracked or
ignored files. Pass a native Go binary, not a version-manager shim. The child
environment contains only the documented scratch paths, Go controls and a small
executable path. It does not inherit credentials, GOROOT, user Go settings or a
Go workspace. `GOENV=off`, `GOTOOLCHAIN=local` and `-mod=readonly` prevent those
sources of drift; public module downloads and the local OS/compiler remain inputs.
Go's build overlay adds the original probes without editing upstream files.

Each command has a maximum 600-second wall timeout and a 90-second Go test
timeout; the overall test-command budget is 1180 seconds, plus a ten-second
forced-termination reserve. Two build/test workers limit resource use. No upstream
Taskfile, installation command or workflow is executed.

## Checks and interpretation

| Group | Expected outcome and scope |
| --- | --- |
| composition | Pass: upstream patch parsing, rejection and template cases; original prefix checks for file/all/role/node order and changed environment input. |
| masking | Pass with `-race`: registered-value masking across write boundaries and concurrent registration/writing. |
| secrets-and-version | Pass: upstream old-secret discovery, PEM variants and nil handling; original running-contract versus desired-image selection. |
| dispatch | Pass: bounded worker execution and an original deterministic single-worker failure stopping further admission. |
| write-branch | Pass: a successful local subprocess's synthetic output is stored. This is not encryption. |
| version-controls | Pass: plain tag parsing, malformed input rejection and known-version API selection. |
| encryption-failure | Expected failure with `BW_PLAINTEXT_PERSISTED`: a stub exits 42, but the provider returns success and stores original synthetic bytes. |
| digest-version | Expected failure with `BW_DIGEST_IN_VERSION`: a digest suffix is included in the parsed version. |
| unknown-version | Expected failure with `BW_UNPROVEN_API_SELECTION`: empty/malformed running versions select the Lifecycle API. |

The runner requires every named test's terminal result, exact process status and
negative marker. Missing/skipped tests, extra failing/skipped subtests, compiler
errors and timeouts cannot count as confirmation. A successful harness exit means
that the expected positive and negative results matched, not that integration is
safe. The final upstream status must remain pristine.

The filesystem test writes only an arbitrary synthetic string under a test temp
directory. Its `sops` executable is a local stub; no real encryption key, SOPS
installation, secret provider or service is used. Synthetic version strings do
not establish support for those server versions. Serial failure admission does
not establish cancellation of concurrent or already accepted remote work.

## Retained evidence

The first run on 20 October 2026 took about 56 seconds: 14 named positive tests
passed (eight upstream and six original), with 51 test/subtest pass events, three
expected-negative failures and zero skips. The runner exited 0; source and fixture
bytes remained unchanged. [results.json](evidence/results.json) records versions,
commands, source/fixture hashes and every named outcome. Its command strings are
argument displays; use the runner above to reproduce the exact argument vectors.
[retention.json](evidence/retention.json) binds the tested uncommitted fixture bytes
to Bronzeward base `c0a54f8` and records hashes of every retained raw log.

Per-group JSON lines retain the test output. The composition, secrets-and-version
and version-controls stderr files retain module-download messages; the other six
stderr files were empty and are omitted. The runner substitutes `$GO`, `$SOURCE`,
`$OUTPUT`, `$FIXTURE` and scratch-path placeholders. `overlay.json` is local path
plumbing and is not published. Pre-run PATH/GOROOT diagnostics in the retention
metadata ran no tests; no failed fixture launch is omitted.
[leak-scan.txt](evidence/leak-scan.txt) records the retained files' scan and limits.

[Upstream attempt metadata](evidence/upstream-e2e-attempts.json) and the
[successful-run excerpt](evidence/upstream-e2e-excerpt.txt) are separate QEMU
evidence: attempts 1 and 2 failed before tests on Image Factory reachability;
attempt 3 passed its five integration subtests. None was run locally. The pinned
workflow exercises Talos 1.14.0 to 1.14.1, not a cross-minor upgrade.

The normal `mise run verify` does not execute this optional networked research
fixture. Preserve failed runs and dispose of only this experiment's disposable
source/cache/output after its evidence is accepted and preserved. No real upgrade,
encryption, credential custody, remote accounting or integration feasibility is
established by these synthetic results.
