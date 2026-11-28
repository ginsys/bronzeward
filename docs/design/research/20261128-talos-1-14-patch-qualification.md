# Talos v1.14.7: qualification of the v1.14 patch baseline against v1.14.5

| | |
|---|---|
| **Date** | 28 November 2026 |
| **Work item** | [Qualify the Talos 1.14 patch baseline](https://github.com/ginsys/bronzeward/issues/1275) |
| **Contract** | [compilation §10](../../spec/compilation.md#10-renderer-selection-and-compatibility-limits) |
| **Artifacts** | [`experiments/talos-114-patch/`](../../../experiments/talos-114-patch/README.md), evidence under [`experiments/talos-114-patch/evidence/`](../../../experiments/talos-114-patch/evidence/) |
| **Decision enabled** | Whether to move the pinned v1.14.5 (module, `talosctl`, node image) to v1.14.7, and the exact edits. **No pin is changed here.** |

The v1.15 question belongs to [ginsys/bronzeward#1212](https://github.com/ginsys/bronzeward/issues/1212)
and its [report](20261126-talos-115-beta-first-tests.md). That report already built v1.14.7 as its
build C and found it equal to v1.14.5 in every item it compared; this one repeats the measurement on
today's `main`, adds the etcd-encryption cases, and runs the tests and the fixture.

## 1. Question

With `github.com/siderolabs/talos/pkg/machinery` at v1.14.7, `talosctl` v1.14.7 and the v1.14.7 node
image:

1. What changed between v1.14.5 and v1.14.7, and does any of it reach a shipped binary?
2. Does Bronzeward's compile path produce what it produces at v1.14.5?
3. Do the existing tests pass, and if not, why?
4. Does the fixture chain pass on disposable nodes, with the new server on an old node and the old
   server on a new node?
5. What exact edits does the move need?

## 2. Versions and builds

| Build | Machinery | How it is made |
|---|---|---|
| A | v1.14.5 | the product code at `d8c4ba52` (the head of `main` when the work started) |
| C | v1.14.7 | A and [`patches/c-machinery-1147.patch`](../../../experiments/talos-114-patch/patches/c-machinery-1147.patch) (`go.mod` only) |

Module reads ([`versions-read.txt`](../../../experiments/talos-114-patch/evidence/versions-read.txt)):

| Version | Published | `go.sum` hash of the module | `go.mod` hash |
|---|---|---|---|
| v1.14.5 | 2026-10-26 | `h1:s7tKS2fe2TjD8mVfD4NG2mF6GMJn5THhsgt4/x5+eUM=` | `h1:PEzCUOIG+87NIqBsONm/eQEQQfgoOwWL+r5ANhCrQGU=` |
| v1.14.6 | 2026-11-11 | `h1:gLXBpRIsxS+Pz2fw5v/+8ZjyD5AyEZuGl96X58Hl9C8=` | `h1:Xpqf4j02CiOuRUhUIfSAmjZncZ4qw/7JziUC6ZoiIxs=` |
| v1.14.7 | 2026-11-24 | `h1:LHw7vZO2hUSTYW4NRQVX8gLHsg8Odw7OSUXP7t+BEdQ=` | `h1:Xpqf4j02CiOuRUhUIfSAmjZncZ4qw/7JziUC6ZoiIxs=` |

All three came through the checksum database (`GOSUMDB=sum.golang.org`, nothing excluded for the
module). v1.14.6 and v1.14.7 have the same `go.mod`.

The patches hold `go.mod` only. `go.sum` is not committed: `run/build` has Go write it, refuses to
run with checksum database verification off, and fails if that changes `go.mod`. The earlier
experiment found that the secret scan which runs before a commit is sent to a cluster Job refuses
a `go.sum` line, so none is carried here; the lines the implementation needs are in §7.

Pins read the same day ([`pins-read`](../../../experiments/talos-114-patch/evidence/versions-read.txt),
last section): `talosctl-linux-amd64` of v1.14.7 has SHA-256 `823deb86…cfb18` (full value in §7) in
the release's `sha256sum.txt`, and the downloaded binary hashes to the same; the node image index
of `ghcr.io/siderolabs/talos:v1.14.7` is `sha256:cf23135c…1cc3` (full in §7). The same reads of
v1.14.5 returned the values `fixtures/versions.env` pins today (three of three equal), which is the
control.

## 3. Test 1: what changed, and where it reaches

**Command:** `diff -ruN` of the two module trees, then `go list -deps` over the shipped binaries
(evidence: [`module-diff-5-7.patch`](../../../experiments/talos-114-patch/evidence/module-diff-5-7.patch),
[`generation.txt`](../../../experiments/talos-114-patch/evidence/generation.txt)).

Nine files differ between v1.14.5 and v1.14.7:

- `config/generate/kubernetes.go` and `generate_test.go`: the one behavioural change, below.
- `constants/constants.go` and the `gendata` and `version` data files: default versions
  (kernel 6.18.58 to 6.18.65, etcd v3.7.3 to v3.7.4, Go 1.26.10 to 1.26.11, Flannel, and the tag).
  The only one of these constants a shipped package reads is `DefaultKubernetesVersion`
  (`internal/compile/renderer.go:105`), which is unchanged.
- `go.mod` and `go.sum`: `golang.org/x/net` v0.58.0 to v0.62.0, `x/crypto` v0.55.0 to v0.58.0,
  `x/sync` v0.22.0 to v0.24.0, `x/sys` v0.47.0 to v0.49.0, `x/text` v0.41.0 to v0.43.0. The first
  change is in v1.14.6; v1.14.7 changes only the constants and data files (4 files).
- The `config/schemas/config.schema.json` file did not change (§5).

**The change.** In v1.14.5, `generate` writes the AES-CBC key into the `KubeEtcdEncryptionConfig`
document under a provider named `secretbox`; v1.14.7 names it `aescbc`. A synthetic bundle with
only an AES-CBC secret gives, at v1.14.5, one provider of kind `secretbox`; at v1.14.7, one of
kind `aescbc`. Secretbox-only bundles are identical; a bundle with both gives `secretbox` then
`secretbox` at v1.14.5 and `secretbox` then `aescbc` at v1.14.7. Only provider kinds, key names and
secret lengths were printed.

**Does it reach a shipped binary?** No. The import closure of `./cmd/bronzeward`, `./cmd/bronzewardctl`
and the fixture programs holds `config/generate` in none of them (`generate=0` on every row);
`config/generate/secrets` is in `cmd/bronzeward` only. The fixture's `-tags fixtureinterrupt` build
is the same. Control: the test builds of `internal/compile`, `internal/ingest`, `internal/api` and
`internal/clitest`, which call `generate.NewInput`, do list it. A source read of non-test,
non-experiment importers found none. So the fix changes only what the tests and experiments
generate as their input, never what a shipped binary writes.

## 4. Test 2: the compile path

**Command:** `experiments/talos-114-patch/run/probe`, in one cluster Job. It builds A and C, runs
[the probe](../../../experiments/talos-114-patch/probe/compile_probe_test.go.in) inside each
build's `internal/compile`, A twice, and compares. A generates the input bytes and every build reads
the same ones; the three encryption inputs are rewritten first to the shapes the fixed generator
writes (a provider kind of `aescbc` for the AES-CBC key, which A names `secretbox`), and the probe
checks the kinds before it writes them (`secretbox`; `aescbc`; `secretbox,aescbc`). Evidence: [`test1-probe.log`](../../../experiments/talos-114-patch/evidence/test1-probe.log)
(`VERIFY_DONE rc=0`).

The 31 cases of each run are the ginsys/bronzeward#1212 probe's compositions (fragments, references
of each kind, secret marks, embedded documents, refusals, three platform modes, the hostname fill),
three imported configurations that carry each provider shape (`enc-sbox`, `enc-aescbc`,
`enc-both`) and three generated by the build itself (`own-enc-*`). A difference is any unequal
verdict, rule, path, redacted message, materialized or redacted digest, record digest, form-only
answer, hostname decision or Kubernetes version asked.

**Controls:** A against its second run: no difference in the results and 0 of 46 key-path files
(`keydiff compared=46 differing=0 unmatched=0`; the comparison fails if it compares no file or
leaves one unmatched). A changed label value gives another digest
(`full-kubenode-labels`, `control-kubenode-other`).

**Result.** C equals A in every compared item of the 28 composition and imported cases, including
`enc-aescbc` and `enc-both` (the same materialized, redacted and record digests). It differs in
four files of the 46, all from the build's own generation:

| File | Difference |
|---|---|
| `gen-enc-aescbc.yaml`, `mat-own-enc-aescbc.yaml` | provider `secretbox` key `key1` at A, provider `aescbc` key `key1` at C |
| `gen-enc-both.yaml`, `mat-own-enc-both.yaml` | the second provider, the same change |

The four files are the §3 change. Four more differences: the generated control-plane and worker
documents, under the v1.14 contract and under the library's default, have another digest with
comments at C, while the comment-free output has the same digest. The cause was not isolated and
none is claimed: the module's default versions moved (§3), and the commented output may carry
version-dependent text. It is test input only (§3).

## 5. Test 3: the existing tests

**Command:** [`run/test2`](../../../experiments/talos-114-patch/run/test2) as routed cluster
Jobs in profile `go-db` (PostgreSQL and OpenBao), one at a time. Evidence:
[`test2-summary.txt`](../../../experiments/talos-114-patch/evidence/test2-summary.txt).

| Set | At | Result |
|---|---|---|
| `go build ./...`, `go vet ./...` | C | both pass |
| `internal/api` | C | pass (1270 s) |
| every other package (33) | C | 32 pass or have no tests; `internal/clitest` fails in three tests |
| the three failing clitest tests (`TestApprovals`, `TestDrift`, `TestProtected`) | C, with the version string in the export rewritten | pass |
| the same three tests | A | pass in two of three runs (below) |

The three fail because their golden transcripts (`internal/clitest/testdata/journeys/t3.txt`,
`t8.txt`, `t10.txt`) hold `machinery v1.14.5` nine times (3, 2 and 4), and the run prints
`machinery v1.14.7`. The `goldens` set rewrites exactly that string in the export and the tests pass;
at A they pass unchanged, on two of three runs. In the one that failed (the second, which was the
first run of the final revision of `run/test2`), `TestApprovals` and `TestDrift` passed and
`TestProtected` failed in `PROT-02` to `PROT-06`: `PROT-02`'s import start answered that its
claim's lease had lapsed 17 s ago, and the four after it were refused because an ingestion of the
same draft was still running. That run was slow (`TestApprovals` took 71 s there and 40 s in the
last, passing one), the lease is a time bound, and the failure is at A, which has no v1.14.7 code; the
third run passed. It is read as a timing failure under load of a test this change does not touch,
and not isolated further. Both reruns check that the three tests ran and passed; the evidence
keeps the failed run's lines. The skips are 17
in the rest set and 9 in `internal/api`, each with the reason it printed in the summary: services the
pod does not start (no `dbus-daemon` or `gnome-keyring-daemon`, no test identity provider, no Talos
node, no packaged binaries); the browser harness's own steps (`TestE2E*`, three in `internal/api`);
and two tests, `TestMachineryGate` and `TestSensitivityGate`, which are left to their own script
(`experiments/c10-machinery-gate/run/all`) and are not run in a Go test run at A either.

The web console reads the Talos config schema from `web/src/talos-schema/v1.14.json`.
`TestWebSchemaAssets` passes at C; read directly, the asset and the module's
`config/schemas/config.schema.json` at v1.14.5 and v1.14.7 have the same SHA-256
(`5d83e2f7…c4822a`), so `mise run web-schema` makes no change.

Three of the tests that reach `generate.NewInput` (all through `newStandIn`,
`internal/clitest/publication_test.go:77`) failed at C. Their failure is the golden
string above, which the rewrite clears; no failure traces to the generator change. Whether the
other tests that generate their input at C depend on the provider kind was not isolated: they pass.

## 6. Test 4: the fixture chain

**Command:** `fixtures/scenarios/integrated <dir> up s1p s3h s2p s3rp s4 s5p down` (import,
publication, handover, no-reboot dry run, apply, read back, drift) in a scratch git checkout of the
export of `d8c4ba52`, with `go.mod` and `fixtures/versions.env` edited and committed there only, on
host Docker through the host test queue, one chain at a time. The `/home` floor of 20480 MiB was
read before each (37028, 33334 and 37587 MiB available) and each chain's evidence was
trimmed to its logs and result tables before the next. A control-plane encryption change was not
applied to any node, and none ran on a real cluster. Each chain's `prepared.txt` in the evidence
holds the script's own record of the run: the free-space reading, the four Talos lines of
`fixtures/versions.env`, the machinery line of the chain's `go.mod` as committed in the scratch
checkout, and that checkout's commit. The scratch checkouts were deleted with the chains.

| Chain | Server | Node image and `talosctl` | Result | Time |
|---|---|---|---|---|
| `c7` | C (v1.14.7) | v1.14.7 | every step exit 0, `0 failed` | 1157 s |
| `c5` | C (v1.14.7) | v1.14.5 (the pinned) | every step exit 0, `0 failed` | 1002 s |
| `a7` | A (v1.14.5) | v1.14.7 | every step exit 0, `0 failed` | 1029 s |

Each is inside the 1500 s limit of the fixture README, and a chain measured once has no spread. The
six result tables (`s1`, `s2`, `s3 handover`, `s3 pass`, `s4`, `s5`) of `c5` and `a7` are
byte-identical to `c7`'s ([`summary.txt`](../../../experiments/talos-114-patch/evidence/chains/summary.txt),
`cmp` rc 0 on all twelve): PASS 37, 53, 29, 77, 33 and 73, SKIP 2, 4, 0, 1, 0 and 3, FAIL none. So the
new server works on the old node and on the new one, and the old server works on the new node.

## 7. What it informs, and the edits

**Recommendation: move to v1.14.7 in one implementation change.** Nothing here found a Bronzeward
behaviour that depends on the v1.14.5 module. The one behavioural change in the module is in a code
path no shipped binary reaches. The edits, which this report does not make:

1. `go.mod`: `github.com/siderolabs/talos/pkg/machinery v1.14.7`; `golang.org/x/net v0.62.0` and
   `golang.org/x/text v0.43.0` (direct), `golang.org/x/crypto v0.58.0` and `golang.org/x/sync
   v0.24.0` (indirect). `golang.org/x/sys` stays at v0.49.0.
2. `go.sum`: run `go mod tidy` with the checksum database on. In the export of build C it wrote
   ([`export-go-diff.txt`](../../../experiments/talos-114-patch/evidence/export-go-diff.txt)) the machinery lines
   `h1:LHw7vZO2hUSTYW4NRQVX8gLHsg8Odw7OSUXP7t+BEdQ=` and `/go.mod h1:Xpqf4j02CiOuRUhUIfSAmjZncZ4qw/7JziUC6ZoiIxs=`
   and the `x/crypto`, `x/net`, `x/sync`, `x/text` and `x/tools` (v0.48.0 to v0.51.0) lines for the new versions.
3. `fixtures/versions.env`: `TALOS_VERSION=v1.14.7`;
   `TALOSCTL_URL=https://github.com/siderolabs/talos/releases/download/v1.14.7/talosctl-linux-amd64`;
   `TALOSCTL_SHA256=823deb86e20d79f08d000b266e63e4bffc772ee80364ce405bad5659bcacfb18`;
   `TALOS_IMAGE=ghcr.io/siderolabs/talos:v1.14.7@sha256:cf23135c4dc42f4031f6e68af92e312f2a1540c4071005d47ed82dbd58201cc3`.
4. `internal/clitest/testdata/journeys/t3.txt`, `t8.txt` and `t10.txt`: `machinery v1.14.5` to
   `machinery v1.14.7` (nine strings; the tests' own `-update` writes them).
5. `web/src/talos-schema/v1.14.json`: no change.
6. Text that names the baseline or the fixture as v1.14.5 and is meant to say what is pinned today
   (for example `docs/operator/README.md`, `docs/operator/limits.md`, `fixtures/README.md`, the
   specification and design documents; find them with `rg 'v?1\.14\.5'` outside the research
   directory) moves with the change. Historical evidence stays at its pin.

**Which earlier claims need a rerun.** None of the acceptance evidence is invalidated: it records
what ran at v1.14.5 and says so. After the change, the integrated chain on the new pins is the one
already run here as `c7`; the implementation cites it or reruns it at its own head. The
label-activation evidence of
[ginsys/bronzeward#714](https://github.com/ginsys/bronzeward/issues/714) is a separate experiment on a
v1.14.5 node; it was not rerun here and stays a v1.14.5 claim until it is.

## 8. Limits

- Containers, one control plane and one worker, one Kubernetes version. All inputs synthetic.
- The chains cover import, publication, handover, dry run, apply, read back and drift as the
  integrated chain defines them. They do not cover reboot, upgrade, a control-plane encryption
  change or hardware.
- A fixture chain measured once each has no spread, and a pass says the chain passed.
- The failing tests were attributed by the A run of the same three tests, and the cause by rewriting
  one string; no other test run at A was repeated, since the 32 other packages passed at C.
- The module hashes are read through the host's module proxy and checksum database; the node image
  digest and the release checksums were read from the registry and release page on 28 November 2026.
- The `go.sum` lines of §7 are those `go mod tidy` wrote in the export; the implementation writes
  its own.
- The `build`, `api` and `rest` sets at C ran with the first revision of `run/build` and
  `run/test2`, which differs from the committed one only in the `goldens` set and in the checksum
  database refusal for build A. The probe and the two `goldens` runs are from the committed revision.
- The upstream release notes of v1.14.6 and v1.14.7 were not read; the diff of the module is.
