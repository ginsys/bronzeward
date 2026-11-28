# Talos v1.14 patch qualification

**Evidence code for [ginsys/bronzeward#1275](https://github.com/ginsys/bronzeward/issues/1275). It is
not part of the implementation and is not maintained past the
[research report](../../docs/design/research/20261128-talos-1-14-patch-qualification.md) it produces.**
No product module changes: v1.14.7 exists here only as a patch. The v1.15 question belongs to
[ginsys/bronzeward#1212](https://github.com/ginsys/bronzeward/issues/1212) and its
[`talos-115-beta`](../talos-115-beta/README.md) experiment; this one is a copy of its method with
two builds, A (the pinned v1.14.5) and C (v1.14.7).

Every command works from a clone that holds commit `d8c4ba52`, the base the scripts export.

| Path | What it is |
|---|---|
| `patches/c-machinery-1147.patch` | `go.mod` alone, zero context: machinery v1.14.7 and the four `golang.org/x` modules its `go.mod` raises |
| `probe/compile_probe_test.go.in` | the compile probe. The `.in` suffix keeps Go and lint away; it compiles only after `run/probe` copies it into an export's `internal/compile` |
| `run/build <a\|c> <new dir>` | export the base commit into a new directory with `git archive` and, for `c`, apply the patch |
| `run/probe` | the compile probe (report section 4): build A and C, run the probe on A twice and on C, compare the results and the key paths |
| `run/test2 <a\|c> <build\|api\|rest\|goldens>` | the Go tests (report section 5): build or test one build; the tests need what the `go-db` job profile starts (PostgreSQL, OpenBao). `goldens` runs the three clitest journeys whose golden files name the machinery version, and for `c` rewrites that one string in the export first |
| `evidence/` | what the report cites: module reads, generation reads, the probe log (`test1-probe.log`), the test summaries (`test2-summary.txt`) and for each of the three fixture chains its log and result tables. The two file names keep the numbers the scripts first had; the report's sections are the reference |

The fixture chains on disposable nodes (report section 6) have no script of their own here: they are
`fixtures/scenarios/integrated <record dir> up s1p s3h s2p s3rp s4 s5p down` run in a scratch git
checkout of the export, with `go.mod` and `fixtures/versions.env` edited and committed there only
(see the report for the three pairings and what each edit was).

`go.sum` is not committed for the patched build. `run/build` lets Go write it, requires checksum
database verification (`GOSUMDB=sum.golang.org`, nothing excluded, no `GOFLAGS` but `-v`), and
fails if that changes `go.mod`. A host that sets `GOPRIVATE` or `GONOSUMDB` has to unset both, and
set `GOENV=off`, for this.

`run/probe` prints verdicts, digests, document kinds, key paths, YAML styles and the compiler's
redacted messages. It never prints configuration bytes or keys. All inputs are synthetic.
