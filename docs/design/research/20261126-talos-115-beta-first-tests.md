# Talos v1.15.0-beta.1: first measurements of the library against v1.14

| | |
|---|---|
| **Date** | 26 November 2026 |
| **Work item** | [Support Talos v1.14, move to v1.15 when it is released](https://github.com/ginsys/bronzeward/issues/1212) |
| **Contract** | [compilation §10](../../spec/compilation.md#10-renderer-selection-and-compatibility-limits) |
| **Artifacts** | [`experiments/talos-115-beta/`](../../../experiments/talos-115-beta/README.md), evidence under [`experiments/talos-115-beta/evidence/`](../../../experiments/talos-115-beta/evidence/) |
| **Decision enabled** | Input to which Talos versions Bronzeward supports and how. **No support decision is made here.** |

**These are results on a beta.** Each one is to be measured again on the v1.15.0 release.

## 1. Question

With `github.com/siderolabs/talos/pkg/machinery` at v1.15.0-beta.1:

1. Does Bronzeward's own compile path produce, for a v1.14 contract, what it produces at v1.14.5?
2. Which existing tests fail, and why?
3. Does a live v1.14 node, and a v1.15 beta node, take the beta build's output through a no-reboot
   dry run, an apply, a read back and a drift read?
4. What does the newer validator accept in a v1.14-contract composition that a v1.14 node refuses?

[E3](20260925-talos-compatibility.md) compared generation and validation across renderers and ran
live against v1.13.6 only. It did not compose through Bronzeward's compiler.

## 2. Versions and builds

| Build | Machinery | How it is made |
|---|---|---|
| A | v1.14.5 | the product code at `cf597ab9` |
| C | v1.14.7, the latest v1.14 patch on the day ([`versions-read.txt`](../../../experiments/talos-115-beta/evidence/versions-read.txt)) | A and `patches/c-machinery-1147.patch` |
| B | v1.15.0-beta.1 | A and `patches/b-machinery-beta1.patch` |
| 2a | v1.15.0-beta.1 | B and `patches/2a-talostest-cosi.patch` (one call in a test helper, §4) |
| 2b | v1.15.0-beta.1 | 2a and `patches/2b-contract-v114.patch` (measurement only, §4) |

The beta module forces other module changes; the largest are `cosi-project/runtime` v1.16.3 to
v1.17.1, `go.yaml.in/yaml/v4` v4.0.0-rc.6 to v4.0.0, `google.golang.org/grpc` v1.83.2 to v1.85.0
and `siderolabs/crypto` v0.6.5 to v0.6.6. The full list is the B patch. C forces newer
`golang.org/x` modules only (its patch).

The module patches hold `go.mod` only. `go.sum` is not committed for B and C: `run/build` has Go
write it, refuses to run with checksum database verification off, and fails if that changes
`go.mod`. An earlier form of the patches held `go.sum` lines, and the secret scan that runs before
a commit is sent to a cluster Job refused it (`BUNDLE_SCAN_HIT`) on one line, a public module
checksum. The lines were removed; the scanner was not changed and nothing was allow-listed.

Live pins are in [`versions.env`](../../../experiments/talos-115-beta/versions.env): the v1.14.5
node image and `talosctl` of the fixture, the v1.15.0-beta.1 image by digest and its `talosctl` by
SHA-256, and Kubernetes 1.36.2. The Jobs ran Go 1.27.1 (the `TOOLCHAIN` lines of the logs). The
live log does not record the Go version of the host build.

## 3. Test 1: the compile path

**Command:** `experiments/talos-115-beta/run/probe`, in one cluster Job. It builds A, C and B,
runs [the probe](../../../experiments/talos-115-beta/probe/compile_probe_test.go.in) inside each
build's `internal/compile`, A twice, and compares. A generates the input bytes; every build reads
the same ones. Evidence: [`evidence/test1-job.log`](../../../experiments/talos-115-beta/evidence/test1-job.log)
(`VERIFY_DONE rc=0`).

The 56 cases are 25 compositions after the compiler tests' synthetic inputs (fragments,
references of each kind, secret marks, identified embedded documents, refusals, three platform
modes, the hostname fill), 17 cases for document kinds only the beta registers, and 14 for fields
only the beta declares in a kind both know. A difference is any unequal verdict, rule, path,
redacted message, materialized or redacted digest, record digest, form-only answer, hostname
decision or Kubernetes version asked as v1.14.5.

**Controls:** A against its second run shows no difference. A changed label value gives another
digest (`full-kubenode-labels`, `control-kubenode-other`).

### 3.1 Generation

With contract v1.14 set, the comment-free output for a control plane and a worker has the same
digest in A, C and B. The output with comments differs in each.

With no contract set, B's default is its own: it adds `DiskSMARTConfig` and
`WorkloadResourceConfig`, `readinessCheck` in `KubePrismConfig`, `EtcdCAConfig` and
`EtcdMemberConfig` on a control plane (removing the v1alpha1 `cluster.etcd.ca`), and
`machine.features.trustdCheckExtKeyUsage` on a worker. Bronzeward generates no base
configuration (compilation §6); the compiler tests' own generator sets no contract, so under B
they build on a v1.15 base.

### 3.2 Composition

C equals A in every compared item. B equals A in every compared item of the 25 compositions except
the output bytes of two:

| Case | Path | A writes the value | B writes it |
|---|---|---|---|
| `full-hostname-fill` | `doc[8]/auto` | single-quoted | double-quoted |
| `trim-each-kind-embedded` | `doc[0]/cluster/inlineManifests/1/name` | single-quoted | double-quoted |

Paths and values are equal; the materialized and redacted digests are not. So the same sources
compiled by B can give another configuration digest than A gave. The likely cause is the forced
`go.yaml.in/yaml/v4` change, which C does not make; **this is inferred and was not isolated**.
Neither case was applied to a node.

Asked with B's own machinery version, `KubernetesVersion` refuses every case ("the renderer's
version is not known"): Bronzeward holds a Kubernetes window for minor 1.14 only. The machinery's
windows are 1.32.0 to 1.37.99 for 1.14 and 1.33.0 to 1.38.99 for 1.15 (`talos114`, `talos115`
constants of the two modules, quoted in
[`versions-read.txt`](../../../experiments/talos-115-beta/evidence/versions-read.txt)).

### 3.3 What B accepts and A does not

A refuses all 31 new-kind and new-field cases at ingestion (`schema-unloadable`). On a
v1.14-contract control plane base, in metal mode, B accepts:

- **15 of the 17 new kinds:** `ContentLibraryConfig`, `CPUPartitionConfig`, `CPUScalingConfig`,
  `DiskSMARTConfig`, `InlineDataVolumeConfig`, `KubeAuthenticationWebhookConfig`,
  `NetworkLinkIngressConfig`, `MacVLANConfig`, `SeccompProfileConfig`, `StaticEndpointsConfig`,
  `StoragePoolConfig`, `VirtualMachineConfig`, `VirtualMachineProfileConfig`, `VXLANConfig`,
  `WorkloadResourceConfig`. It refuses `EtcdCAConfig` and `EtcdMemberConfig` there, because
  v1alpha1 already sets `.cluster.etcd`.
- **All 14 new fields tried** (the `field-` cases in the log).

`VirtualMachineProfileConfig` compiles, but `KubernetesVersion` then refuses it ("the kubelet
image cannot be shown"). From the code, not tested apart: that function looks for
`machine.kubelet.image` in each document other than `KubeletConfig`, and this kind has a top-level
`machine` string.

**Not tried:** [`evidence/new-fields.txt`](../../../experiments/talos-115-beta/evidence/new-fields.txt)
lists every YAML key the beta's config types declare and v1.14.5's do not (and three it removes),
made by `run/new-fields.py`. Among the lines in existing kinds, the container fields, the BGP
neighbor fields, the NFS mount, the user volume mount mode, the trim chunk fields, the authorizer
kubeconfig and the link selector of `EthernetConfig` have no case. Each new kind was tried with
one minimal document.

## 4. Test 2: the existing tests

Each variant is built inside a cluster Job from the committed patches (`run/test2`, from a local
commit of this branch that holds these scripts and patches; the product code it exports is
`cf597ab9`'s), and run with the `go-db` profile (PostgreSQL and OpenBao). The export is not a git
checkout, so the base, variant `a` in the same form, is the reference: a failure counts against
the beta only if `a` does not show it. Per-Job files are
[`evidence/test2-*.txt`](../../../experiments/talos-115-beta/evidence/): the Job's status lines,
and `run/failures.py`'s reading, which lists each failing leaf test with the cause its own output
shows. "Contract" means the output holds the contract rule's refusal ("this server renders only
v1.15"). Counts of results include subtests.

| Variant, set | File | Result | Failing leaf tests |
|---|---|---|---|
| bump only (2a-raw), `go build` and `go vet` | `test2-raw.txt` | both fail | one package: `internal/talos/talostest`, `undefined: inmem.Build` |
| 2a, `internal/api` | `test2-2a-api.txt` | PASS 287, FAIL 1302 | 1157, all the contract rule |
| 2a, the other packages | `test2-2a-rest.txt` | PASS 2770, FAIL 51 | 46: 34 contract (`internal/clitest`), 12 other |
| 2b (contract kept at v1.14), `internal/api` | `test2-2b-api.txt` | PASS 1689, FAIL 63 | 55, none the contract rule |
| 2b, other packages, first half (up to `internal/ingest`) | `test2-2b-rest-first.txt` | PASS 2175, FAIL 21 | 17 |
| 2b, other packages, second half | `test2-2b-rest-second.txt` | PASS 656, FAIL 4 | 4 |
| base `a`, other packages | `test2-a-rest.txt` | PASS 2884, FAIL 3 | 2: `internal/clitest` `TestProtected/PROT-06`, `TestProtected/T3` |
| base `a`, `internal/api`, whole set | `test2-a-api.txt` | **no verdict**: `go test` hit its 40 minute limit | none before the cut (PASS 991) |
| base `a`, `internal/api`, the 27 top-level tests that fail under 2b | `test2-a-api-narrow.txt` | PASS 71, FAIL 0; all 27 names started | 0 |

The first 2b run for the other packages ended on `LVJ_TIMEOUT` with no verdict
(`test2-2b-rest.txt`), and was rerun once in two halves; the two files above are that rerun.

**Reading.**

- **The module bump alone does not build the tests** (2a-raw): the forced
  `cosi-project/runtime` v1.17.1 no longer has `inmem.Build`, which one test helper
  (`internal/talos/talostest`) calls. No product package fails to build. One call in that helper,
  adapted, builds everything (2a).
- **Most failures are the contract rule, as `compilation` choice 16.33 says it would be**: 1157
  of 1157 in `internal/api` and 34 of the 35 in `internal/clitest` under 2a.
- **With the contract kept at v1.14 (2b), other failures remain, and the base passes them.**
  - `internal/api`: 55 leaves in 27 top-level tests; the base passes all 27. Most show `refused
    (kubernetes): the kubelet image names no Kubernetes version`: the beta's
    `constants.DefaultKubernetesVersion` is `1.38.0-beta.0` (v1.14.5: `1.37.1`; `versions-read.txt`), a prerelease, which
    Bronzeward's release-tag pattern does not take. Worker and publisher tests (`TestNewStartsPublisher`,
    `TestPublishWorker`, `TestPublishRunStopped` and others) show only `operation failed`; their own
    cause was not read. The 63 FAIL results include the subtests of those leaves. **Only these 27
    tests have a base reading** in `internal/api` (the whole-set base run timed out), so for the
    rest of that package the base is unknown, and they passed or did not run under 2b.
  - Other packages, first half: 17 leaves. Two of them (`TestProtected/PROT-06`, `TestProtected/T3`)
    the base fails too, but with other messages, so they may or may not be the beta's. The other 15
    pass on the base. `internal/compile` (5): `TestKubernetesVersion` (two cases) and
    `TestCompileTheFullGeneratedStream` show the same prerelease cause;
    `TestCompileAttributesTheGeneratedBase` reports that the generated base's CA key is not
    attributed at `doc[0]/cluster/etcd/ca/key`, which is where §3.1 shows the beta moving it to
    `EtcdCAConfig` (that link is inferred);
    `TestCompileStillRefusesAMarkedLeafWhoseStandInIsInvalid` is refused as invalid, and its 2a
    file shows `etcd config is already set in v1alpha1 config`. `internal/ingest` (1):
    `TestIdentifySchemaSetOfGeneratedConfig` (the machinery does not redact four paths of the new
    generated document set). `internal/clitest` (9 beyond the base): `TestPublication/EVT-04` shows
    `rule: kubernetes`; the other eight are plan commands that exit 4 where the test wants 0, and
    their cause was not read.
  - Other packages, second half: 4 leaves, all failing only under the beta, none in the base:
    `internal/talos` `TestTalosAPIMethods` (the machinery's `machine.ContentLibraryService`,
    `HypervisorService` and `StorageService` are not in the test's list of services), and three
    `internal/talos/signer` tests that expect a certificate's `NotBefore` 300 s before signing and
    find 360 s (cause not read). `internal/talos/discover` passes under 2b, and fails under 2a only
    on the contract rule.
- **What the base shows by itself:** in this pod form two `internal/clitest` tests fail
  (`TestProtected/PROT-06` and `T3`, a terminal and a process matter), so they say nothing of the
  beta.

**Not covered.** Web and shell tests do not load the module and were not run. The first 2b run for
the other packages timed out; nothing is claimed from it beyond what its file shows. Under 2a,
the contract rule fails `internal/api` and `internal/clitest` so widely that what else would fail
there is unknown, which is why 2b exists.

## 5. Test 3: live nodes

**Command:** `experiments/talos-115-beta/run/live`, on the host (it needs Docker), through the
host's run queue. Each scenario is a new two-node Docker cluster, destroyed before the next; only
its worker is sent anything. The node's own configuration is the import base. A and B compile it
in container mode: plain, with a `KubeNodeConfig` label from a synthetic reference, and with a
second label value. B's label output goes through a `talosctl` no-reboot dry run, then
Bronzeward's own applier and reader. Evidence:
[`evidence/test3-live.log`](../../../experiments/talos-115-beta/evidence/test3-live.log).

The per-case digests of the 27 accepted new kinds and fields were removed from that file after the
secret scan that runs before a commit is sent to a cluster Job flagged one line; the verdicts stay.

A first run stopped on its own invalid-configuration control, before any apply: the node's
configuration keeps comments, and the pattern that planted the defect expected the line to end
after the value. The log is of the second run, with that one pattern corrected. That run used the
script as it was then (SHA-256 `447bf419…`, cut to eight characters here), which is the version
at an earlier commit of this branch; the difference is in
[`live-script-record.txt`](../../../experiments/talos-115-beta/evidence/live-script-record.txt).
The link between that digest and the run rests on the lead's approval of the digest and a re-read
before the run, neither of them a committed file. The committed script differs: a control that
fails now stops the run, where the run's script ignored the failure of one control and only printed
a note for the other, and the invalid-configuration control must see the node's own reason for the
planted defect. Both controls passed in all three scenarios, so the stop never came into play,
and the committed script has not been run.

**Controls, in all three scenarios:** a dry run of the node's unchanged configuration is accepted;
a configuration with machine type `bogus` is refused (the node's reason, `unknown machine type
"bogus"`, was read in the run's saved output, which is not committed because such output can hold
the configuration); two planted addresses are refused by the script before any write; after a
planted second apply the drift read reports a difference.

| Scenario | A and B compile the node's configuration | Dry run, apply, read back, drift read after 60 s |
|---|---|---|
| v1.14.5 node | equal digests, all three | accepted; read back equals the compiled digest; unchanged after 60 s |
| v1.14-contract node on the beta image (made by `talosctl` v1.14.5) | equal digests, all three | the same |
| v1.15.0-beta.1 node (made by the beta `talosctl`) | A refuses the base at ingestion (`schema-unloadable`); B compiles | the same, with B |

The drift read compares Bronzeward's configuration digest of what the node returns with that of
the compiled bytes.

### 5.1 The newer validator against a v1.14.5 node

On the v1.14.5 worker's configuration, B was given 16 new kinds (all but `EtcdCAConfig`) and the
14 fields. It accepted 27. It refused three as for control plane machines only
(`EtcdMemberConfig`, `KubeAuthenticationWebhookConfig`, the `KubeFlannelCNIConfig` field), the
first and last with a second reason each.

**The node refused all 27 in a no-reboot dry run:** the 14 kinds as `not registered`, the 13 fields
as `unknown keys found during decoding`. The node's configuration was unchanged afterwards. A
refused all 30 at ingestion.

So with the library moved to the beta and the contract kept at v1.14, nothing on the compile path
would stop these before a node does. Publication's other checks were not exercised.

## 6. What this informs

The five questions are those in the [work item](https://github.com/ginsys/bronzeward/issues/1212).

| Question | What was measured |
|---|---|
| 1. Can a v1.15 renderer produce v1.14 configuration a live v1.14 node accepts, with composition, no-reboot apply and drift detection as before? | On the beta, for the cases here: yes for a label fragment on the node's own configuration. Two other compositions differ in quoting and were not applied. |
| 2. Does the newer validator accept what a v1.14 node cannot decode, and what refuses it? | Yes (§5.1). On the compile path B does not refuse it; the v1.14.5 node does. |
| 3. Shared fragments across clusters on different minors | Not examined, except that the same sources can give other bytes under B (§3.2). |
| 4. The Kubernetes window | It is keyed on the machinery's minor; B has none for 1.15 (§3.2). The two minors' windows differ at both ends. |
| 5. One renderer or one per minor | Evidence for neither: B reproduced A's output for most cases here and accepts much that a v1.14 node refuses. |

## 7. Limits

- A beta. Nothing here says what v1.15.0 does.
- Containers, one control plane and one worker; only the worker was sent anything. No reboot,
  upgrade, VM or hardware. Documents for control plane machines were not dry-run.
- One Kubernetes version. All inputs synthetic.
- Live, the only fragment applied is a `KubeNodeConfig` label. Publication, including the contract
  rule, was not exercised live.
- The composition gate of
  [`experiments/c10-machinery-gate/`](../../../experiments/c10-machinery-gate/README.md) was not
  run on B.
- No upstream release-support policy was read.
