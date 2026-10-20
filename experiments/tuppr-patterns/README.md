# Pinned tuppr compatibility experiments

Research for [#165](https://github.com/ginsys/bronzeward/issues/165). The
[report](../../docs/design/research/20261020-tuppr-patterns.md) owns recommendations
and limitations. This fixture runs selected fake-client upstream tests and two
original negative probes. It installs no operator and contacts no cluster.

## Reproduce

Requires Git, Python 3.10+, GNU `timeout` and Go 1.27.1 on PATH. Go module
downloads need network access; tests use fake Kubernetes/Talos clients. Use a
disposable, disk-backed directory outside either repository for source, evidence
and caches. From the Bronzeward checkout:

```sh
research_root=/path/on/disk/tuppr-research
mkdir -p "$research_root/gotmp" "$research_root/gocache" "$research_root/gomodcache"
git clone https://github.com/home-operations/tuppr.git "$research_root/source"
git -C "$research_root/source" checkout --detach 377fbf03df05420ca833230233e67877e5f343d4
export TMPDIR="$research_root/gotmp" GOTMPDIR="$research_root/gotmp"
export GOCACHE="$research_root/gocache" GOMODCACHE="$research_root/gomodcache"
python3 experiments/tuppr-patterns/run.py \
  --source "$research_root/source" --output "$research_root/run1"
```

The output directory must not exist. The source must be clean at the exact pin.
The script uses `GOWORK=off`, `GOTOOLCHAIN=local`, `-mod=readonly`, two build/test
workers and bounded command/test timeouts. It clears inherited GOFLAGS and GOROOT.
It does not run upstream mise tasks or installation hooks. Go's `-overlay`
adds the two probe files to their upstream packages only for compilation; it
does not modify the source checkout. See `go help build` for the overlay contract.

Go minimum 1.26.5 comes from the pinned `go.mod`. Upstream's `.mise/config.toml`
pins Go 1.27.2; the recorded Go 1.27.1 run is not that CI toolchain. Talos/Kubernetes
version strings inside fake tests are synthetic values, not exercised servers.

## Interpreting the result

`run.py` lists every selected top-level test. Positive groups must exit 0 and
report every named test passing. Negative groups must exit 1, report their named
test failing and include the corresponding `BW_` marker. Missing tests, skipped
tests, unexpected failure/subtest skips, compiler errors and timeouts fail the
experiment. The final source status must remain clean.

- `recovery`: creation, health refusal, retry reset, failed synchronization,
  failed Job with target version, and persisted reboot tracking after Job GC.
- `coordination`: tuppr-resource blocking only.
- `config`: synthetic legacy and multi-document image reads/edits.
- `health`: validation, missing object and empty/failing selection semantics.
- `foreign-cordon`: expected negative, cleanup clears a pre-existing cordon.
- `suspend-job`: expected negative, suspension leaves an existing Job enabled.

The negatives assert Bronzeward integration requirements, not tuppr's promises.
Stopping a Kubernetes Job would still not account for an earlier remote request.
No credentials, live admission/PDB controller, quorum, real upgrade, reboot,
network-loss or cross-controller race are exercised. Scheduling/hooks and
image-resolution paths are source-reviewed only. No lifecycle feasibility or
production-readiness claim follows from the runner's exit 0.

## Retained evidence

[results.json](evidence/results.json) records versions, hashes, commands, exit
codes and the exact test inventory. Per-case JSON lines retain test output;
paths for the fixture, source and output roots are replaced by `$FIXTURE`,
`$SOURCE` and `$OUTPUT`. `overlay.json` is local execution plumbing, not published.
[launch-failure.txt](evidence/launch-failure.txt) preserves the first failed
environment setup. [leak-scan.txt](evidence/leak-scan.txt) states the scan's scope.

Recorded 20 October 2026: 19 selected upstream tests passed (46 test/subtest pass
events), two compatibility probes failed with the expected markers, zero skips;
runner exit 0 and source clean. Script and probe hashes identify the tested
working-tree bytes based on Bronzeward 0439601. Full repository verification is
separate; the normal `mise run verify` does not execute this optional networked
research fixture. Retain failed runs. Dispose of only this experiment's source,
cache and output paths after its evidence is accepted and preserved.
