# Talos compatibility: generation, validation, configuration RPCs and support policy

| | |
|---|---|
| **Date** | 25 September 2026 |
| **Work item** | [E3: establish PoC Talos compatibility](https://github.com/ginsys/bronzeward/issues/5) |
| **Design reference** | [§6.5 Renderer and contract pinning](../Talos_Configuration_and_Machine_Management_Design.md#65-renderer-and-contract-pinning), [§18.1 Phase 0](../Talos_Configuration_and_Machine_Management_Design.md#181-phase-0---evidence-before-implementation-contracts), [§18.2 Phase 1](../Talos_Configuration_and_Machine_Management_Design.md#182-phase-1---configuration-control-on-an-existing-cluster) |
| **Artifacts** | [`experiments/e3-talos-compatibility/`](../../../experiments/e3-talos-compatibility/README.md), evidence under [`experiments/e3-talos-compatibility/evidence/`](../../../experiments/e3-talos-compatibility/evidence/) |
| **Decision enabled** | The compatibility boundary a PoC renderer can rely on, and what choosing `talosctl` as a subprocess or the Go machinery costs. Input to [renderer selection](https://github.com/ginsys/bronzeward/issues/17). No renderer, fleet version set or support policy is selected here. |

**This is not a pass of design experiment E3.** The §18.1 row for E3 includes the
Upgrade/LifecycleClient transition. Nothing here executed an upgrade, a reboot or any lifecycle
RPC; the fixture's Talos nodes are containers, where those operations do not exist (this
report's §6.3).

## 1. Question

Design §6.5 separates four things a release records and a renderer must respect: the observed
running version, the desired image, the generation contract and the renderer version. It then says
that "backward-generation constants, upstream release-support policy and working machine
operations are separate axes", and that "the machinery minor being at least the target contract
minor is a prerequisite, not a sufficient compatibility test". Issue 5 asks for a tested matrix
that keeps those axes apart:

1. **Generation capability**: which contracts a renderer produces, and what it does when asked for
   one it cannot produce.
2. **Validation**: which validators accept which renderer's output.
3. **Operation/RPC compatibility**: which clients complete the PoC's configuration RPCs against the
   fixture's live v1.13.6 node, and which generated configurations that node accepts.
4. **Upstream support policy**: what the machinery encodes and what the upstream documentation
   states.

It also asks whether `talosctl` as a subprocess and the Go machinery (`pkg/machinery`) behave the
same on the same cells, and what each implies for the structural-reference evidence of
[issue 3](https://github.com/ginsys/bronzeward/issues/3).

## 2. Renderers, targets and the two implementations

Six renderers ([`versions.env`](../../../experiments/e3-talos-compatibility/versions.env)), chosen to
cross every boundary the PoC could meet: the previous minor, the first and a later patch of the
fixture's minor, the fixture's own pin, the next minor, and a prerelease.

| Renderer | Why |
|---|---|
| v1.12.12 | previous minor, latest patch |
| v1.13.0 | first release of the fixture's minor |
| v1.13.6 | the fixture's pin (`fixtures/versions.env` as of c74f953), the reference for every comparison |
| v1.13.10 | a later patch of the fixture's minor, newer than the node |
| v1.14.1 | next minor |
| v1.15.0-alpha.0 | prerelease |

Targets: `current` (no `--talos-version`), the contracts v1.10.0 to v1.15.0, and five
version-string edges: `1.13` (no `v`), `v1.13.6` (a patch), `v1.15.0-alpha.0` (a prerelease),
`v1.99.0` (a minor no renderer knows) and `bogus`. Kubernetes: each renderer's own default, and the
fixture's 1.36.2.

Every cell runs through both implementations:

- **talosctl as a subprocess:** that release's `talosctl`, downloaded once and checked against the
  release's own `sha256sum.txt`.
- **the Go machinery:** `e3m`, one source
  ([`machinery/src/main.go`](../../../experiments/e3-talos-compatibility/machinery/src/main.go))
  built once per version against that tag's `pkg/machinery` module. `gen` goes through
  `bundle.NewBundle` as `talosctl gen config` does; `validate` calls `Config.Validate` in a runtime
  mode; `version`, `read` and `apply` use the machinery client.

## 3. What was built

### 3.1 Groups

A table row holds several cells: one per implementation, and for validation one per
implementation and runtime mode.

| Group | Rows (cells) | What it records |
|---|---|---|
| gen | 144 rows (288 cells) | every renderer x target x Kubernetes setting: both implementations' exit and output, the output digests, whether the two are identical, and whether talosctl repeats itself |
| validate | 504 rows (2016 cells) | every generated configuration at Kubernetes 1.36.2 (7 targets x 2 machine types per renderer) x every renderer as validator; each row's four cells are talosctl and the machinery, each in container and metal mode |
| strict | 84 rows (336 cells) | each renderer's own output with `--strict`, the same four cells |
| policy | 96 answers per machinery | the Kubernetes and upgrade windows each machinery's `compatibility` package encodes |
| rpc-client | 12 rows | each renderer as a client of the v1.13.6 worker, through both: version, read, dry-run apply and real apply of a label patch in no-reboot mode |
| rpc-contract | 42 rows | every renderer's worker configuration for `current` and each contract, as a no-reboot dry run through the pinned client, both implementations |
| controls | 5 rows | positive controls, below |
| tests | 1 row | `e3m`'s own tests |

The gen, validate, strict and policy groups are measurements: the renderer's behaviour is the
finding, and each distinct message is cited once by id
([`messages.tsv`](../../../experiments/e3-talos-compatibility/evidence/messages.tsv)). The rpc and
control groups are rows with an expectation written in `run/all` before the run and compared with
what the harness reads back from the worker through the pinned `talosctl`: the configuration
digest and the machine-configuration resource version.

**Controls** (all fired in the capture):

| Control | Proves | Observed |
|---|---|---|
| `gen-bogus-refused` | generation can fail | `bogus` refused in all 12 gen rows for it (6 renderers x 2 Kubernetes settings), through both implementations in each |
| `gen-repeatable` | a digest difference is not noise | every one of the 132 generating rows compares `same` with its talosctl repeat; 0 differ |
| `compare-unequal` | the comparison can report a difference | a different cluster name compares unequal, and `e3m paths` names `0/cluster/clusterName` |
| `validate-invalid-refused` | validation can fail | a worker with `type: bogus` accepted by 0 of 24 validator cells (6 renderers x 2 modes x 2 implementations), and all 24 outputs name `unknown machine type "bogus"` |
| `rpc-invalid-refused` | the node can refuse | the same file refused by the node through both clients, each naming `unknown machine type "bogus"`; the worker's digest and version unchanged |

### 3.2 Pinned versions

| Component | Version |
|---|---|
| `talosctl` | the six renderers, each SHA-256 in [`versions.env`](../../../experiments/e3-talos-compatibility/versions.env) and [`tools.tsv`](../../../experiments/e3-talos-compatibility/evidence/tools.tsv) |
| `pkg/machinery` | the same six tags, `machinery/<version>/go.mod` and `go.sum` |
| Go | go1.27.1 |
| Live node | Talos v1.13.6 control plane and worker, Kubernetes 1.36.2; image digests in [`bundle-summary.txt`](../../../experiments/e3-talos-compatibility/evidence/bundle-summary.txt) |
| Fixture pins | `fixtures/versions.env` as of c74f953 |
| Captured from | 2809915, 0 uncommitted inputs ([`run.txt`](../../../experiments/e3-talos-compatibility/evidence/run.txt)) |

The machinery defaults Kubernetes to 1.35.8 (v1.12.12), 1.36.0 (v1.13.0), 1.36.2 (v1.13.6), 1.36.3
(v1.13.10) and 1.37.0 (v1.14.1, v1.15.0-alpha.0). Every configuration names the installer image
`ghcr.io/siderolabs/installer:v1.13.6`, because the renderers' own defaults differ and would make
every cross-renderer comparison unequal for that reason alone.

### 3.3 Secrets and the leak scan

Every configuration is generated from the fixture cluster's own secrets bundle so that it can be
applied to the worker. The configurations, raw outputs and worker reads therefore hold the
fixture's secrets, and stay in `E3_OUT` outside the checkout. What is committed of a configuration
is its digest and its key-path differences (`e3m paths`: `+`/`-`/`~` and a path, never a value).
A transcript keeps an apply's output only up to its first diff line, and a read's standard error
only, with a line count and digest for what was withheld. `e3m` withholds a diff from its own
errors the same way, printing `withheld_lines=` instead.

`fixtures/bin/evidence` ran with `E3_OUT` as an extra scan path. It matched its planted control,
the pattern list, and the generated and raw configurations in `E3_OUT`, as expected, and nothing
in the fixture's own state beyond the control. `run/collect-evidence` then planted its own control,
confirmed it fired, and found 0 hits of the 16 patterns and 0 paths under the operator's home in
the 22 committed files ([`leak-scan.tsv`](../../../experiments/e3-talos-compatibility/evidence/leak-scan.tsv)).

### 3.4 Reproduction

```sh
fixtures/bin/up
E3_OUT=<new empty absolute path outside the checkout> experiments/e3-talos-compatibility/run/all
fixtures/bin/evidence <the same path>
cp -a fixtures/.state/evidence/<bundle> <the same path>/bundle
fixtures/bin/down
E3_OUT=<the same path> experiments/e3-talos-compatibility/run/collect-evidence
```

`run/all` took 84 seconds with the six `talosctl` downloads already cached and the fixture up
(`started`/`finished` in `run.txt`). `E3_OUT` needs about 1 GiB, mostly the tool copies.

## 4. The matrix

The four columns are kept apart. A cell in one column says nothing about the others: an output that
validates may still be refused by the node, and a combination the node accepts may be outside
upstream support.

### 4.1 Generation capability

Source: [`gen.tsv`](../../../experiments/e3-talos-compatibility/evidence/gen.tsv).

- **talosctl and the machinery produce identical output in all 132 generating rows**, and talosctl
  repeats itself in every one. "Identical" here and below means equal digests after the
  trailing-newline normalization (the experiment's README).
- **`bogus` is refused** by every renderer through both (M3, M4: `error parsing version "vbogus"`).
- **A target newer than the renderer is not refused. The renderer silently produces its own current
  contract.** At Kubernetes 1.36.2, grouping each renderer's outputs by digest:

  | Renderer | Identical groups of targets |
  |---|---|
  | v1.12.12 | {v1.10.0, v1.11.0}; {current, v1.12.0, v1.13.0, v1.14.0, v1.15.0, 1.13, v1.13.6, v1.15.0-alpha.0, v1.99.0} |
  | v1.13.0, v1.13.6, v1.13.10 | {v1.10.0, v1.11.0}; {v1.12.0}; {current, v1.13.0, v1.14.0, v1.15.0, 1.13, v1.13.6, v1.15.0-alpha.0, v1.99.0} |
  | v1.14.1 | {v1.10.0, v1.11.0}; {v1.12.0}; {v1.13.0, 1.13, v1.13.6}; {current, v1.14.0, v1.15.0, v1.15.0-alpha.0, v1.99.0} |
  | v1.15.0-alpha.0 | {v1.10.0, v1.11.0}; {v1.12.0}; {v1.13.0, 1.13, v1.13.6}; {current, v1.14.0, v1.15.0, v1.15.0-alpha.0, v1.99.0} |

  So v1.12.12 asked for v1.13.0 returns a v1.12 configuration, exit 0, no warning. `1.13` and
  `v1.13.6` are the v1.13.0 contract: the patch and the missing `v` are ignored.
- **A given contract's output is the same from every renderer that knows it.** Across renderers at
  Kubernetes 1.36.2, four distinct outputs cover every contract cell: one for v1.10/v1.11 (all six
  renderers), one for v1.12 (all six), one for v1.13 (the five renderers from v1.13.0 on), one for
  v1.14/v1.15 (v1.14.1 and v1.15.0-alpha.0). The clamped outputs join the group of the renderer's
  own current contract.
- **v1.10.0 and v1.11.0 produce identical output** in every renderer, for these inputs.
- **The prerelease adds nothing.** v1.15.0-alpha.0's v1.15.0 and `current` outputs equal v1.14.1's
  v1.14.0 output.
- **v1.14 changes the document layout.** Against v1.13.6, v1.14.1's worker configuration loses
  `cluster/*`, `machine/kubelet`, `machine/install` and two `machine/features` fields from the
  v1alpha1 document and gains 13 documents, among them `KubeletConfig`, `DiscoveryServiceConfig`,
  `KubeClusterConfig` and `UnattendedInstallConfig`
  ([`paths/renderers.txt`](../../../experiments/e3-talos-compatibility/evidence/paths/renderers.txt)).
  Against the pinned renderer's current worker output, its v1.10/v1.11 contracts add
  `machine/features/{apidCheckExtKeyUsage,rbac,stableHostname}` and `machine/network`, and lack
  `machine/install/grubUseUKICmdline` and the `HostnameConfig` document; v1.12 adds only
  `machine/network`
  ([`paths/contracts.txt`](../../../experiments/e3-talos-compatibility/evidence/paths/contracts.txt)).

### 4.2 Validation

Source: [`validate.tsv`](../../../experiments/e3-talos-compatibility/evidence/validate.tsv),
[`strict.tsv`](../../../experiments/e3-talos-compatibility/evidence/strict.tsv).

| Configuration | v1.12.12 to v1.13.10 validators | v1.14.1, v1.15.0-alpha.0 validators |
|---|---|---|
| any renderer's v1.10 to v1.13 contract, or a pre-v1.14 renderer's clamped output | valid, container and metal, both | valid, both |
| v1.14.1 or v1.15.0-alpha.0 at `current`, v1.14.0 or v1.15.0 | **refused** by both: `"DiscoveryServiceConfig" "v1alpha1": not registered` (M8 to M11) | valid, both |

- Of 504 rows, 456 are valid in all four cells and 48 are refused in all four (4 validators x
  6 configurations x 2 machine types): no row mixes verdicts, so talosctl and the machinery agree in
  all 2016 cells, with the same message text. Validation was only given talosctl's files, which
  are identical to the machinery's (§4.1).
- v1.14.1 accepts v1.15.0-alpha.0's output, which is the v1.14 layout (§4.1).
- **Neither generation nor validation enforces the Kubernetes window.** v1.12.12 generates and
  validates a worker for Kubernetes 1.36.2 (`gen.tsv`, `validate.tsv`) although its own
  `compatibility` package refuses 1.36.2 for the v1.12 target as too new
  ([`policy/v1.12.12.tsv`](../../../experiments/e3-talos-compatibility/evidence/policy/v1.12.12.tsv));
  every renderer's v1.10 contract, whose window ends at 1.33 (§4.4), does the same. Only the
  `compatibility` package refuses such a combination, and only when asked.
- `--strict` refuses nothing: all 84 own-output rows (336 cells) valid, because no renderer emits
  a warning for its own output.

### 4.3 Operation/RPC compatibility against v1.13.6

Source: [`rows.tsv`](../../../experiments/e3-talos-compatibility/evidence/rows.tsv), transcripts in
[`transcripts/`](../../../experiments/e3-talos-compatibility/evidence/transcripts/).

**Clients (rpc-client, 12 of 12 rows match).** Every renderer, through both implementations,
reads the node's version (`v1.13.6`), reads its machine configuration with the same digest as the
harness's own read, completes a no-reboot dry run that leaves the configuration and its version
unchanged, and applies a label patch that the harness then reads back as landed. The three
`talosctl` clients newer than the node print, on standard error and only for the configuration read
(`talosctl get`):

```text
WARNING: 10.55.0.3: server version 1.13.6 is older than client version 1.13.10
```

(and the same for 1.14.1 and 1.15.0-alpha.0). Their `version` and `apply-config` steps print none.
The node returned no apply warnings to the machinery client (`apply_warnings=0` in every row). The
machinery module has no skew check of this kind, so `e3m` prints none by construction; a machinery
caller that wants one has to compare versions itself. Standard output was unaffected, so a
subprocess caller that separates the streams reads the same bytes.

**Configurations (rpc-contract, 42 of 42 rows match: both implementations reach the same verdict
and nothing lands).** Each renderer's worker configuration was dry-run applied to the live worker
in no-reboot mode through the pinned client, as `talosctl` and as the machinery. The node's reason
is the same text through both in every refused row, once `e3m`'s quoting of `"` is removed:

| Worker configuration | Node's answer |
|---|---|
| v1.12 or v1.13 contract, from any renderer | accepted |
| a pre-v1.14 renderer's clamped v1.14.0/v1.15.0 output (which is its own current contract) | accepted |
| v1.14.1 or v1.15.0-alpha.0 at `current`, v1.14.0 or v1.15.0 | refused: `error decoding document v1alpha1/DiscoveryServiceConfig/default … not registered` |
| v1.10 or v1.11 contract, from any renderer | refused: `this config change can't be applied in immediate mode` |

The v1.10/v1.11 refusal is about the apply mode, not the contract's decodability. That is an
inference from the refusal text and from §4.2, where v1.13.6 validates these files: the node
appears to have decoded the file and declined to apply it without a reboot. Against the live
worker, those contracts differ in the paths above (§4.1) plus `certSANs`, the `nodeLabels` left by
the rpc-client rows' label patches, and the generated `install` fields
([`paths/live-worker.txt`](../../../experiments/e3-talos-compatibility/evidence/paths/live-worker.txt)).
The accepted v1.12 contract and the refused v1.10/v1.11 contracts differ from each other only in
`machine/features/{apidCheckExtKeyUsage,rbac,stableHostname}`, `machine/install/grubUseUKICmdline`
and the `HostnameConfig` document, so the reboot-only change is among those; which one is not
isolated here. Whether they apply with a reboot cannot be measured in containers.

### 4.4 Upstream support policy

**What each machinery encodes** (`compatibility` package, [`policy/`](../../../experiments/e3-talos-compatibility/evidence/policy/)):
Kubernetes accepted by `KubernetesVersion.SupportedWith`, over the points 1.29.0 to 1.37.0:

| Talos target | Kubernetes accepted | Upgrade accepted from (`TalosVersion.UpgradeableFrom`, over v1.10 to v1.15) |
|---|---|---|
| 1.10 | 1.29 to 1.33 | 1.10, 1.11 |
| 1.11 | 1.29 to 1.34 | 1.10 to 1.12 |
| 1.12 | 1.30 to 1.35 | 1.10 to 1.13 |
| 1.13 | 1.31 to 1.36 | 1.11 to 1.14 |
| 1.14 | 1.32 to 1.37 | 1.12 to 1.15 |
| 1.15 | 1.33 to 1.37 | 1.13 to 1.15 |

- Each machinery that knows a target gives the same answer for it. A machinery that predates the
  target has no answer: `compatibility with version 1.14.0 is not supported` and `upgrades to
  version 1.13.0 are not supported`.
- A bound at a test point's edge (Kubernetes 1.29 for 1.10 and 1.11, 1.37 for 1.14 and 1.15; host
  1.10 for 1.10 to 1.12; host 1.15 for 1.14 and 1.15) is the sample's edge, not necessarily the
  machinery's.
- The "upgrade" window includes newer host versions, that is, downgrades: 1.13 accepts a 1.14
  host, and refuses 1.15 as `too new to downgrade`.

**What the upstream documentation states** (read 2026-09-25, no last-updated date on the pages;
the cells and sentences used here are recorded verbatim in
[`upstream-docs.md`](../../../experiments/e3-talos-compatibility/upstream-docs.md)):

- The [v1.13 support matrix](https://docs.siderolabs.com/talos/v1.13/getting-started/support-matrix)
  lists Kubernetes 1.31 to 1.36 for 1.13 and 1.30 to 1.35 for 1.12; community support for 1.13
  ends at the 1.14.0 release, given as "2026-08-30, TBD".
- The [v1.14 support matrix](https://docs.siderolabs.com/talos/v1.14/getting-started/support-matrix)
  lists Kubernetes **1.33 to 1.37** for 1.14 and gives the 1.14.0 release as 2026-08-27. The
  machinery accepts **1.32** for 1.14, and the two pages disagree on the 1.14.0 date.
- The [upgrade guide](https://docs.siderolabs.com/talos/v1.13/configure-your-talos-cluster/lifecycle-management/upgrading-talos)
  says the migration "is only tested between adjacent minor releases" and recommends upgrading
  through the latest patch of every intermediate minor. For the client, it recommends "the version
  that matches the current running version of the cluster".
- The older `talos.dev/v1.13/introduction/support-matrix/` URL now returns 404.

So the machinery's windows are wider than the documented tested path: it accepts a two-minor
upgrade and a one-minor downgrade that the documentation does not describe as tested, and one
Kubernetes minor more for 1.14 than its own documentation lists.

## 5. Failures hit while building it

### 5.1 A harness read died on SIGPIPE

The first capture stopped at "could not read the worker's resource version". `awk … exit` read the
first match and closed its input, and the upstream `talosctl` then failed with SIGPIPE, which
`pipefail` turned into a failure. The read now captures the output first and parses it afterwards.

### 5.2 A stale artifact digest made refusals look like landings

The second capture's rpc-contract rows read the worker as `is=artifact` after a refused dry run: the
previous row's artifact digest was still set. `row_begin` now clears every per-row digest.

### 5.3 The skew warning broke the read digest

`talosctl` v1.13.10 and later print the skew warning (§4.3) with the configuration, and a combined
stream no longer matched the harness's digest. Reads now keep standard output as the raw
configuration and standard error in the transcript.

### 5.4 Error and dry-run output embed the configuration

A refused or dry-run apply prints a configuration diff, which holds the fixture's secrets.
Transcripts keep an apply's output up to the first diff line and record the line count and digest
of the rest. That was tested against the raw outputs of the second capture (0 leak-pattern hits);
those outputs were not kept. The pre-draft review then found that `e3m` printed a refusal's whole
error, diff included, on one line that this filter could not see. `e3m` now withholds the diff
itself: in the final capture each of the 12 immediate-mode refusals through the machinery withheld
13 lines.

### 5.5 The first committed bundle manifest was 124 KB

The fixture bundle's `postgres-data/` alone is over a thousand manifest lines. The committed
manifest lists everything else and gives that directory one line, the digest of its part of the
full manifest, which stays in `E3_OUT`.

### 5.6 The harness did not enforce its own controls

The pre-draft review found that `run/all` wrote `control mismatches N` and finished normally
whatever N was, and that `run/collect-evidence` never read it. A failed control now makes `run/all`
exit non-zero, and the collector refuses the run. The same review hardened two controls that could
pass vacuously: repeatability now requires every generating row to compare `same`, and the invalid-
configuration controls require the refusal to name the planted reason, so that a connection
failure cannot read as a refusal. The capture was repeated at 2809915 with every control firing.

### 5.7 The packed transcripts failed the whitespace check

`talosctl version` ends its `Built:` line in spaces. The collector already dropped trailing
whitespace from the tables but not from the packed transcripts, and the first commit of this
capture's evidence failed `git diff --check` on twelve such lines. The packs now drop it too, and
the evidence was collected again from the same capture; only those twelve lines changed.

## 6. What this decides

### 6.1 Criterion 1: a matrix with four separate columns

§4.1 to §4.4 are the four columns, each from its own evidence file. Stated per axis for the PoC's
live v1.13.6 node:

| Renderer | Generation | Validation by v1.13.6 | Node accepts (no-reboot dry run) | Upstream |
|---|---|---|---|---|
| v1.12.12 | v1.10 to v1.12; newer targets clamped to v1.12 | valid | v1.12 contract yes; v1.10/v1.11 not in immediate mode | client one minor older than the node; not the documented recommendation |
| v1.13.0, v1.13.6, v1.13.10 | v1.10 to v1.13; newer clamped to v1.13 | valid | v1.12, v1.13 yes; v1.10/v1.11 not in immediate mode | matches the node's minor |
| v1.14.1 | v1.10 to v1.14; newer clamped to v1.14 | v1.14 output refused | v1.12, v1.13 yes; v1.14 refused; v1.10/v1.11 not in immediate mode | next minor; not the documented recommendation |
| v1.15.0-alpha.0 | as v1.14.1; the v1.15 contract equals v1.14 | as v1.14.1 | as v1.14.1 | prerelease |

The clients' own skew warning (v1.13.10 and later `talosctl`, on reads only) is an RPC observation
and stays in §4.3.

### 6.2 Criterion 2: subprocess against machinery, and the structural-reference evidence

**Behaviour is the same.** In every cell measured through both, the two implementations agree:
identical generation (132 of 132 rows), the same validation verdict and message text (504 rows,
2016 cells; strict 84 rows, 336 cells), the same RPC outcomes for the 6 client pairs, the same
node verdict and reason for all 42 contract dry runs, and the node's refusal of the invalid
control. Two things were not compared: validation of the machinery's own files (identical to
talosctl's, §4.1), and the skew warning, which the machinery has no code for (§4.3). The choice
between them is not a compatibility choice within this matrix. It is a packaging and
maintenance one:

| | talosctl subprocess | Go machinery |
|---|---|---|
| Several renderer versions in one manager | one binary per version (98 to 119 MB each here), checked by digest | one build per version: a Go build links a single version of `pkg/machinery`, so several versions mean several binaries or plugin processes anyway (the six modules under `machinery/`) |
| Version-to-version API drift | CLI flags: `apply-config --mode` lists `auto, no-reboot, reboot, staged, try` in v1.12.12 to v1.13.10 and drops `reboot` from v1.14.1 on (`talosctl apply-config --help`; not in the committed evidence) | `secrets.Bundle.Validate` gained a version-contract parameter in v1.14, which needed two shims selected per module (`bundle_v112.go`, `bundle_v114.go`) |
| API surface not public | none needed | `talosctl validate`'s `--mode` type lives in the talos module's `internal/` tree; `e3m` implements the machinery's `RuntimeMode` interface itself |
| Output handling | separate stdout from stderr (skew warning, §4.3); parse text | structured errors and results; the dry-run `ModeDetails` still carry the configuration diff and must be treated as secret |
| Silent clamping (§4.1) | same | same: the caller must enforce "renderer minor ≥ target minor" itself |

**Structural references.** The
[composition evidence](https://github.com/ginsys/bronzeward/issues/3) (report in
[ginsys/bronzeward#42](https://github.com/ginsys/bronzeward/pull/42), not yet on `main`) found that
references must be resolved *before* native composition, so that `talosctl` receives only
reference-free fragments, and it measured that on v1.13.6 only. E3 adds:

- Because generation is identical between the two implementations, early resolution's parity
  result does not depend on the choice between them **for generation at v1.13.6**. E3 did not
  compose patches through the machinery's `configpatcher`; every patch here went through the pinned
  `talosctl machineconfig patch`, so parity of composition through the machinery is inferred, not
  measured.
- Because a contract's output is identical across every renderer that knows it (§4.1), the
  issue-3 parity verdicts plausibly hold for the v1.12 and v1.13 contracts from other renderers.
  That is an inference from generation only; composition on other renderers was not run.
- The v1.14 layout moves many v1alpha1 paths into separate documents (§4.1). A reference addressed
  by document and path, as issue 3's binding candidate is, would point somewhere else under a v1.14
  contract. A contract change is therefore also a reference-address change, and the compiler must
  version its paths with the contract.
- The machinery returns the same error text as `talosctl` (M8/M9, the rpc-contract reasons and the
  controls; M4's `invalid talos-version:` prefix is `e3m`'s own). Issue
  3's observation that decode errors quote a value prefix is a property of the machinery's decoder,
  not of the CLI, and applies to both implementations.

### 6.3 Criterion 3: unsupported combinations, and the deferred lifecycle tests

**Unsupported, with the observed rationale:**

1. **A contract newer than the renderer.** Not refused: silently rendered as the renderer's current
   contract (§4.1). A manager that trusts the renderer's exit code would record the wrong contract.
2. **v1.14+ configurations on a pre-v1.14 validator or node.** Refused on decode: the
   `DiscoveryServiceConfig` document is not registered before v1.14 (§4.2, §4.3).
3. **v1.10/v1.11 contracts applied to a v1.13 node in no-reboot mode.** Refused by the node as not
   applicable in immediate mode (§4.3). With a reboot: not measurable here.
4. **An unparseable version string** (`bogus`). Refused by every renderer.
5. **Kubernetes outside a target's `SupportedWith` window.** Generated and validated without
   complaint (§4.2); only the machinery's `compatibility` package refuses it (§4.4). Like item 1, a
   manager that relies on the renderer would not notice. Not exercised against a node.
6. **Talos targets a machinery predates.** The `compatibility` package has no answer for them
   (§4.4).
7. **The v1.15.0-alpha.0 prerelease as a v1.15 renderer.** It produces no v1.15-specific output
   (§4.1), so it cannot stand in for v1.15.0.

**Deferred: Upgrade/LifecycleClient transition execution.** Not tested. The fixture's Talos nodes
run as containers, where upgrade, reboot, reset, disk and installer behaviour do not exist
([fixtures report §6](20260919-investigation-fixtures.md#6-what-each-experiment-gets)). The
[renderer research](20260826-talhelper-internals-and-topf-successor.md) records topf switching
from the legacy `MachineService.Upgrade` below Talos 1.13 to `LifecycleClient.Upgrade` at or above
it. None of the following was executed: an upgrade through either RPC, the version gate that
chooses between them, an upgrade across the v1.13 boundary, a v1.14 contract applied after an
upgrade to v1.14, any reboot-mode apply, and the §4.4 upgrade windows. **Disposition:** these
belong to the machine-lifecycle phase ([§18.3](../Talos_Configuration_and_Machine_Management_Design.md#183-phase-2---machine-lifecycle))
and its acceptance proof ([§18.5](../Talos_Configuration_and_Machine_Management_Design.md#185-later-lifecycle-acceptance-proof)),
on VMs or hardware rather than containers. The fixtures report names Talos in QEMU as the tool for
them, rejected for Phase 0 because its provisioner requires root
([fixtures report §2](20260919-investigation-fixtures.md#2-alternatives-compared)). They remain an
unmet part of design experiment E3.

### 6.4 Criterion 4: no pass claimed

Design experiment E3 did **not** pass. This report establishes a tested boundary for configuration
generation, validation and the PoC's configuration RPCs against one live v1.13.6 node. The
Upgrade/LifecycleClient transition, which the §18.1 row names, was not executed (§6.3).

## 7. Limits

- **One live version.** Every RPC result is against Talos v1.13.6. No node ran any other version,
  so "the node accepts" means "a v1.13.6 node accepts".
- **Containers, one control plane and one worker.** No reboot, upgrade, reset, installer or disk
  behaviour; no remote transport; no multi-worker rollout.
- **Validation and RPC coverage is narrower than generation.** Validation covers Kubernetes 1.36.2
  and `current` plus the six contracts, not the version-string edges or the renderers' default
  Kubernetes versions. The contract dry runs used the worker configuration and the pinned version
  of each implementation only; only the label patch was really applied.
- **The contract dry runs ran against the worker as the rpc-client rows left it**, carrying their
  12 label patches, not against the fixture's initial configuration.
- **One set of generation inputs.** One cluster name, endpoint, installer image and secrets bundle,
  no patches at generation, no docs or examples. Other options may change which contracts differ.
- **The policy points are a sample.** Kubernetes 1.29.0 to 1.37.0 (ten points) and Talos v1.10 to
  v1.15 (six points). Bounds outside that range are not measured.
- **Upstream documentation is dated by reading, not by the page.** Neither support-matrix page
  carries a last-updated date, and the two disagree (§4.4).
- **The `--mode reboot` observation** (§6.2) comes from each binary's `--help`, read after the
  capture; it is not in the committed evidence.
- **`E3_OUT`'s guard is a prefix test on the path as given.** It refuses any path beginning with
  the checkout's path, a sibling such as `<checkout>-out` included, which fails closed. Nothing is
  canonicalized, so a path that reaches the checkout through a `..` component or a symlink without
  beginning with its path passes.
- **Kubernetes component images inside the nodes are pinned by version, not by digest**
  ([fixtures report](20260919-investigation-fixtures.md)).

## 8. Recommendation

For the PoC's existing-cluster configuration control:

- **Pin the renderer to the node's minor**, as the upstream client guidance recommends. In this
  matrix, a v1.13 renderer's default output is accepted by the v1.13.6 node and a v1.14 renderer's is
  refused. Only `talosctl` warns about the skew, and only on reads.
- **Refuse, in Bronzeward itself, any target contract whose minor exceeds the renderer's, and any
  Kubernetes version outside the target's window.** The renderers refuse neither (§6.3 items 1 and
  5). §6.5's "prerequisite" is therefore Bronzeward's check to make, not the renderer's.
- **Record the contract, not only the renderer.** A release that records "rendered by v1.14.1" does
  not say whether a v1.13 node can decode it; the contract does.
- **Choose between subprocess and machinery on packaging, not compatibility.** Within this matrix
  they behaved identically wherever both were measured (§6.2). Either way, several renderer versions mean several binaries.
- **Treat the machinery's `compatibility` answers as permissive,** wider than the documented tested
  path. A Bronzeward support policy, if one is wanted, is a separate decision.

## 9. Hand-off

- **To [renderer selection](https://github.com/ginsys/bronzeward/issues/17):** §6.2's packaging
  table, the silent clamping (§4.1), and the contract-versioned paths under v1.14 (§6.2).
- **To the [feasibility review](https://github.com/ginsys/bronzeward/issues/12):** the deferred
  lifecycle execution (§6.3) is a permanent caveat of this investigation; design E3 did not pass
  (§6.4).
- **To [issue 3](https://github.com/ginsys/bronzeward/issues/3)'s follow-ups:** composition parity
  through the machinery's `configpatcher`, and on renderers other than v1.13.6, is inferred here,
  not measured (§6.2).
