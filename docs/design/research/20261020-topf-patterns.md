# topf patterns and integration assessment

20 October 2026. Research for [#164](https://github.com/ginsys/bronzeward/issues/164).
Source review and the local synthetic fixture are complete. They do not establish
safe integration or real lifecycle behavior.

## Recommendation and decision enabled

Continue using Talos machinery directly within Bronzeward's existing compiler and
operation contracts. Adapt selected composition, version-separation, masking and
test ideas. Do not adopt topf as a library, copy its execution/provider code, or
deploy its CLI behind an operator wrapper on this evidence.

The inspected execution path generates configuration from current files and live
observations, while Bronzeward dispatches an already approved immutable artifact.
Its secret providers and in-memory orchestration also lack Bronzeward's versioned
secret-reference, journal and uncertain-outcome contracts. These are substantive
integration changes, not properties supplied by a wrapper.

This assessment enables a later choice of patterns. It does not implement lifecycle
support or prove a safe integration. The [compilation contract](../../spec/compilation.md),
[persistence/API contract](../../spec/persistence-api.md),
[execution/recovery contract](../../spec/execution-recovery.md) (ER), and
[upgrade safeguard investigation](20261019-upgrade-workflow-design.md) remain
authoritative. No credentials policy or licence decision changes.

## Source and evidence boundaries

The inspected topf revision is
[`7aeead7545d6a715e719e2e6241027792f7e1a89`](https://github.com/postfinance/topf/tree/7aeead7545d6a715e719e2e6241027792f7e1a89).
Release `v0.6.1` resolves to `22f2dda15f5718c3eb5cc803a2e087f2457679aa`, a different
revision. The issue's earlier `232db62` review and
[August research note](20260826-talhelper-internals-and-topf-successor.md) remain
historical evidence, not current verification. All upstream source links below
identify the newly inspected commit.

The module requires Go 1.27.0 and depends on Talos and Talos machinery 1.14.1,
COSI runtime 1.16.3, Kubernetes modules 0.37.0, Sprig 3.3.0, yaml.v3 3.0.1 and a
yaml.v4 prerelease. The local fixture used Go 1.27.1. Bronzeward's current
compiler still uses machinery 1.13.6; importing topf's module would introduce a
different dependency selection, requiring explicit compatibility review.

The [minimal fixture](../../../experiments/topf-patterns/README.md) supplies original
tests through Go's overlay and leaves the source checkout unchanged. Its selected
upstream tests use synthetic configuration and a local `sops` stub. The fixture
does not contact Talos or Kubernetes, use real credentials or encryption, run an
upgrade or QEMU, or execute topf's CLI or an operator.

## Component assessment

The following findings are source-reviewed. Local results below apply only to the
named fixture tests; they cannot qualify the complete component.

| Candidate | Recommendation and evidence limits |
| --- | --- |
| Composition | Adapt explicit ordering and rejection tests. [Patch loading](https://github.com/postfinance/topf/blob/7aeead7545d6a715e719e2e6241027792f7e1a89/pkg/config/patches.go) applies `all/`, role and node directories, with lexical traversal and native strategic patches. It follows symlinks and reads mutable files. Bronzeward needs recorded source revisions and its existing profile/fragment order; this is not a direct import format or replacement compiler. |
| Templates | Reject live templating during approved dispatch. The same loader exposes Sprig `env`, permits live `RuntimeData`, and routes `.tpl` files outside its SOPS/vals read path. Environment-derived values are not thereby registered as secrets. Inputs would need capture, sanitization and versioned provenance before publication, not a repeat render when applying. |
| Versions and install documents | Adapt separation of observed config contract and desired image. [Node methods](https://github.com/postfinance/topf/blob/7aeead7545d6a715e719e2e6241027792f7e1a89/internal/topf/node.go) choose the contract from running version, then cluster setting, then bundled version; desired installer selection also considers a per-node override. `InstallerImageRef` prefers `UnattendedInstallConfig` over a legacy image. [Generation](https://github.com/postfinance/topf/blob/7aeead7545d6a715e719e2e6241027792f7e1a89/internal/topf/nodes.go) derives the contract from those current observations. Bronzeward instead pins and compares the approved renderer and operation contract. |
| Apply and observation | Adapt dry-run and watch-before-initial-read test ideas, not the execution interface. [Apply orchestration](https://github.com/postfinance/topf/blob/7aeead7545d6a715e719e2e6241027792f7e1a89/internal/cmd/apply/apply.go) calls `FilteredNodes`, which reads live state and generates configuration. Within one [Node.Apply call](https://github.com/postfinance/topf/blob/7aeead7545d6a715e719e2e6241027792f7e1a89/internal/topf/apply.go), dry run and actual apply reuse the same encoded bytes; the mismatch is across the approved-artifact boundary, not a second encoding between those two RPCs. [Stabilize](https://github.com/postfinance/topf/blob/7aeead7545d6a715e719e2e6241027792f7e1a89/internal/topf/wait.go) watches machine readiness over a duration. That is not an artifact digest check or accounting for every old attempt. These RPC paths remain unexecuted locally. |
| Rollout | Adapt bounded admission and role partitioning as implementation ideas. [The node pool](https://github.com/postfinance/topf/blob/7aeead7545d6a715e719e2e6241027792f7e1a89/internal/nodepool/nodepool.go) stops taking more work after an observed error but allows in-flight operations to finish without cancelling their context. The apply command serializes control-plane nodes and bounds workers. Neither behavior proves quorum safety, ownership fencing or remote cancellation. |
| Lifecycle | Reject unchanged orchestration. [Upgrade](https://github.com/postfinance/topf/blob/7aeead7545d6a715e719e2e6241027792f7e1a89/internal/cmd/upgrade/upgrade.go) selects the newer API for known versions at least 1.13.0 and also for empty or unparsable versions. It pulls and installs before draining/rebooting; staging can add node metadata and return before reboot. Later failures leave partial remote state without Bronzeward's durable phase records. Direct deletion after failed eviction can bypass PDBs when explicitly enabled. Uncordon follows successful stabilization, without proving that this operation owned a previous cordon. All real lifecycle behavior remains source-only here. |
| Version parsing | Reject the existing parser for immutable image identity. The same upgrade file parses the entire suffix after `:v` as a version, including `@sha256:…`. [Upstream #171](https://github.com/postfinance/topf/issues/171) remains open as inspected on 20 October. Bronzeward must bind image digest and semantic source/target versions separately and refuse unknown compatibility; an image-string comparison is insufficient. |
| Secret custody | Reject unchanged providers. [The filesystem writer](https://github.com/postfinance/topf/blob/7aeead7545d6a715e719e2e6241027792f7e1a89/pkg/providers/secrets_filesystem.go) retains original bytes when SOPS exits unsuccessfully, writes them to disk and returns success if the write succeeds. [The docs](https://github.com/postfinance/topf/blob/7aeead7545d6a715e719e2e6241027792f7e1a89/docs/providers.md) describe plaintext fallback when SOPS is unavailable; the source applies it to any SOPS failure, which the probe reproduces with a present stub exiting 42. This broader fallback is incompatible with Bronzeward's boundary. [Get/Put](https://github.com/postfinance/topf/blob/7aeead7545d6a715e719e2e6241027792f7e1a89/pkg/providers/secrets.go) identifies a cluster bundle without an explicit version or CAS. [Binary providers](https://github.com/postfinance/topf/blob/7aeead7545d6a715e719e2e6241027792f7e1a89/pkg/providers/secrets_binary.go) pass stderr through and have no context/deadline argument. |
| Redaction and render output | Adapt registered-secret streaming tests, not the ingress boundary. [The writer](https://github.com/postfinance/topf/blob/7aeead7545d6a715e719e2e6241027792f7e1a89/internal/maskedwriter/maskedwriter.go) handles overlapping values and split writes. [Old-secret collection](https://github.com/postfinance/topf/blob/7aeead7545d6a715e719e2e6241027792f7e1a89/internal/topf/current_config_secrets.go) registers known fields and encoding variants; it does not supply sensitivity provenance through arbitrary composition. [Render output](https://github.com/postfinance/topf/blob/7aeead7545d6a715e719e2e6241027792f7e1a89/cmd/topf/render.go) writes full encoded configuration to files. File permissions do not turn plaintext into protected ingress or encrypted artifacts. |

## Architectural fit and partial outcomes

| Bronzeward requirement | Conflict and required treatment |
| --- | --- |
| Exact approved dispatch | Current files, environment, provider output and live observations can change between topf invocations. Keep Bronzeward's immutable encrypted artifact, digest, target assignment, renderer binding and approval checks. A future extraction would need an artifact-only executor interface. |
| Protected secret ingress | Plaintext fallback, bundle-level unversioned Get/Put and full-config render output do not implement extraction before persistence. Keep the existing provider/reference boundary; do not route a topf provider into it unchanged. |
| Provenance | A list of secret strings for output masking is not compilation's per-occurrence attribution, override history or fail-closed tracing. Keep Bronzeward's redacted provenance and source revisions. |
| Durable journals and ownership | The inspected runtime holds working state in memory and returns command results. It supplies no Bronzeward operation owner generation, restore epoch, transactional dispatch commitment or attempt journal. These remain manager-owned requirements. |
| Uncertain outcomes and coexistence | A client error, timeout, stopped worker pool or Ready node does not prove an old request cannot arrive. ER requires every attempt accounted for before a conflicting operation can proceed. A Kubernetes Job/operator wrapper would add another execution owner and would need a separate fencing and recovery design. |

For example, after installation succeeds and drain fails, installation, cordon and
partially evicted Pod facts must remain distinct. Re-running a CLI must not silently
restart the whole operation. If a reboot response is lost, readiness can inform
observation but cannot release ownership while the earlier request is unaccounted.
If secret encryption fails, compilation must refuse publication without persisting
the plaintext. These requirements come from ER, compilation and #166; this research
does not implement or demonstrate them through a topf integration.

The boundary preserved by this deliverable is limited: Bronzeward runtime and
dependencies remain unchanged, and incompatible substitutions are rejected. No
safe integration, real credential rotation or complete interruption/recovery matrix
is claimed. Human usability of the accepted PoC is not improved by this work.

## Reuse options and maintenance

1. **Direct Talos machinery — retain.** This keeps Bronzeward's current boundaries
   and tests under its own ownership. Later version upgrades still require the
   compiler compatibility matrix and operation-specific qualification; direct use
   is not a blanket guarantee of upgrade safety.
2. **Patterns and test ideas — recommended.** Reimplement only ideas selected by a
   later specification, with source attribution and original regression tests.
   Composition order, observed/desired version separation, split-write masking and
   bounded admission are useful examples. No upstream source enters runtime.
3. **Upstream public extraction — untested and not selected.** Core orchestration,
   masking and node-pool code lives under Go `internal/`. Public `pkg/config` and
   `pkg/providers` exist, but [configuration loading](https://github.com/postfinance/topf/blob/7aeead7545d6a715e719e2e6241027792f7e1a89/pkg/config/topf_config.go)
   is coupled to filesystem/decryption/binary-provider behavior; some public
   signatures expose the internal decryption-cache type. A useful extraction needs
   stable pure inputs, injectable effects and explicit compatibility ownership.
   No prototype establishes such a public API here.
4. **Selective copying or vendoring — not selected.** Copying would make Bronzeward
   responsible for the selected files, their tests, provenance and changed upstream
   semantics. Vendoring does not remove `internal/` restrictions or custody/approval
   conflicts. The larger Talos/Kubernetes dependency surface would need isolation or
   coordinated version selection. Neither the ownership nor update cost is justified
   by the current recommendation.
5. **CLI/operator deployment — not selected and untested.** An operator wrapper is
   a proposed additional component, not a capability topf supplies in the inspected
   paths. Deployment would require credential custody, workload and RBAC ownership,
   availability outside the managed cluster, immutable inputs, durable attempt
   recording, cancellation/restart rules and coexistence qualification. The wrapper
   would still have to address the underlying execution and provider mismatches.

The root [LICENSE](https://github.com/postfinance/topf/blob/7aeead7545d6a715e719e2e6241027792f7e1a89/LICENSE)
contains MIT terms. This is a source/provenance observation, not an incorporation
decision or assessment of every dependency's obligations. Any later copying or
extraction needs an identified maintainer, file/dependency licence review, pinned
provenance and a reviewed update process. The independent [#16](https://github.com/ginsys/bronzeward/issues/16)
decision remains untouched.

## Executed evidence and limits

The first local run completed on 20 October, 21:47:29–21:48:25 UTC (about 56
seconds), with Go 1.27.1. Fourteen named positive tests passed: eight selected
upstream tests and six original probes. Including subtests, the logs contain 51
pass events, three expected failures and no skips. The runner exited 0 because
every expected result and negative marker matched; this is not a safe-integration
verdict. Source and fixture checks matched before and after the run.

| Local group | Observed result and narrow conclusion |
| --- | --- |
| Composition | Four tests passed: selected upstream parsing/template cases, original patch-order checks and changed environment input. Mutable environment input changes a template result. |
| Masking | Two upstream tests passed under `-race`, including overlapping/split writes and concurrent registration/writes. This covers registered strings, not arbitrary sensitivity provenance. |
| Secrets and version selection | Three upstream old-secret collection tests and the original observed-contract/desired-image probe passed. Synthetic version strings establish method selection, not support for a live server. |
| Dispatch | The upstream pool test and original single-worker failure probe passed. Later admission stops after that serial failure; cancellation of in-flight remote work is not tested. |
| Write branch | The synthetic subprocess output was stored. The subprocess was a local stub, not SOPS encryption. |
| Version controls | Plain-tag parsing, malformed-image rejection and the selected known-version gate cases passed. |
| Encryption failure | Expected exit 1 with `BW_PLAINTEXT_PERSISTED`: the stub ran and exited 42; the provider returned success and stored the original synthetic bytes. |
| Digest parsing | Expected exit 1 with `BW_DIGEST_IN_VERSION`: the parsed version was `1.14.1@sha256:` followed by the fixture's 64 `a` characters. |
| Unknown version | Expected exit 1 with `BW_UNPROVEN_API_SELECTION`: both empty and malformed version strings selected the Lifecycle API. |

[Recorded results](../../../experiments/topf-patterns/evidence/results.json) retain
versions, commands, named outcomes, process statuses and fixture/source hashes.
[Retention metadata](../../../experiments/topf-patterns/evidence/retention.json)
binds the tested working-tree bytes to Bronzeward base `c0a54f8`, lists log hashes,
omitted empty stderr files and the pre-run toolchain diagnostics. No fixture launch
failed; those earlier shell diagnostics executed no tests. The [fixture README](../../../experiments/topf-patterns/README.md)
links the raw test output and [leak-scan scope](../../../experiments/topf-patterns/evidence/leak-scan.txt).
There was no CLI, real encryption, API, cluster, upgrade, credential rotation or
remote interruption/recovery execution. The serial admission probe is not such a
recovery test. Unsupported reuse avenues remain untested rather than receiving a
feasibility claim from these results.

Separately, upstream [E2E run 38545129337, attempt 3](https://github.com/postfinance/topf/actions/runs/38545129337/attempts/3)
at the inspected source passed on 16 October with Go 1.27.2. Recovered logs contain
five passing subtests: maintenance-node discovery, bootstrap, clean dry run, label
patch lifecycle and upgrade. The pinned workflow/configuration boots three
control-plane nodes at 1.14.0 and targets the module's 1.14.1. Attempts 1 and 2
failed before tests because the Image Factory schematics endpoint was unreachable.
The [attempt metadata](../../../experiments/topf-patterns/evidence/upstream-e2e-attempts.json)
preserves all three outcomes and raw-log hashes; the [excerpt](../../../experiments/topf-patterns/evidence/upstream-e2e-excerpt.txt)
retains the successful test terminal lines. The successful rerun does not erase
those failures. This is upstream execution,
not local reproduction, cross-minor upgrade coverage or Bronzeward acceptance.
The [PostFinance production-use document](https://github.com/postfinance/topf/blob/7aeead7545d6a715e719e2e6241027792f7e1a89/docs/production-usage.md)
describes their setup; it is not an independently verified production claim here.

## Follow-ups

[Lifecycle specification #237](https://github.com/ginsys/bronzeward/issues/237)
already owns approved image/version/RPC bindings, partial installation/drain/cordon
facts, health checks and ownership. This report supplies concrete incompatible
defaults and test ideas. [Qualification #238](https://github.com/ginsys/bronzeward/issues/238)
owns real VM/hardware sequencing, the older API transition, PDB/quorum effects,
late requests, takeover and restore. [#239](https://github.com/ginsys/bronzeward/issues/239)
and [#240](https://github.com/ginsys/bronzeward/issues/240) remain later implementation.
[Health validation #188](https://github.com/ginsys/bronzeward/issues/188) stays open
until a later operation class binds a health check.

No new implementation is selected by a patterns-only recommendation. Any later
topf package extraction, provider adapter or deployment needs its own decision and
refined issue before implementation. No duplicate blocker list is introduced;
native GitHub dependencies remain authoritative. Required real credential custody,
remote accounting, lifecycle interruption/recovery and coexistence evidence remains
unresolved and cannot be replaced by these synthetic tests or passing CI.
