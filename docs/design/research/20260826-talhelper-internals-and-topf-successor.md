# Findings on talhelper's internals, and on topf as its recommended successor

*Source-level follow-up to the 25 August spike note — mechanism evidence, not new conclusions*

| **Document status**    | Research note — source-level findings, not a design decision                                    |
|-------------------------|---------------------------------------------------------------------------------------------------|
| **Date**                | 26 August 2026                                                                                     |
| **Technical baseline**  | talhelper v3.1.17 (archived); topf v0.5.0; Talos machinery v1.14.0-alpha.2 (talhelper) / v1.13.8 (topf) |
| **Bearing on**          | [Talos Configuration and Machine Management Design](../Talos_Configuration_and_Machine_Management_Design.md) — the multi-version renderer, and [20260825-generic-machine-config-generation-talhelper.md](20260825-generic-machine-config-generation-talhelper.md) |

> **Origin:** `budimanjojo/talhelper` was archived on 26 August 2026 at `v3.1.17`. The maintainer's
> release note states this is the final release and recommends two successors, `postfinance/topf`
> and `mirceanton/talstomize`. This note reads talhelper's actual source (the 25 August note was an
> external black-box spike) and reads topf's source in the same depth, to convert empirical findings
> into mechanism-level evidence and to check whether either successor changes the design conclusions
> already recorded. All citations are `path:line` in the named repo at the stated ref, read directly
> via the GitHub API this session — nothing here is from documentation alone unless stated. topf's
> own published comparison page (`postfinance.github.io/topf/main/comparison/`) was checked and
> confirmed to say nothing about Talos version-support ranges.

---

## 1. How talhelper actually works

One `talconfig.yaml` (+ SOPS `talsecret.yaml`, + `talenv.yaml`) produces one machine config per node
plus a `talosconfig`. Self-described as "like Kustomize but for Talos manifest files with SOPS
support natively" (`README.md`).

The pipeline, traced end to end:

1. `pkg/config/loader.go:16` `LoadAndValidateFromFile` — read, envsubst, rewrite relative paths,
   unmarshal.
2. `pkg/config/loader.go:56-66` — merge the top-level `controlPlane:`/`worker:` group block into
   each node via `OverrideGlobalCfg`.
3. `pkg/config/nodeconfigs.go:13` `mergeNodeConfigs` — this merge is **reflect-based whole-field
   replace**, not a deep merge: a non-zero node field wins outright over the group field
   (`nodeconfigs.go:25-40`). Only three fields concatenate instead — `Patches`, `ExtraManifests`,
   `CertSANs` — group value first, node value appended (`nodeconfigs.go:15-24`), citing a specific
   upstream bug report for the ordering choice.
4. `pkg/talos/input.go:20` `NewClusterInput` — `tconfig.ParseContractFromVersion(c.GetTalosVersion())`,
   decrypt the secrets bundle with SOPS, then upstream `generate.NewInput(...)` with
   `generate.WithVersionContract(vc)` (`input.go:86`).
5. `pkg/talos/nodeconfig.go:26` `GenerateNodeConfig` — `input.Config(TypeControlPlane|TypeWorker)`,
   then talhelper's own convenience fields are written directly onto `RawV1Alpha1()`.
6. `pkg/generate/config.go:102` → `pkg/talos/multidocs.go:16-28` `AddMultiDocs` — talhelper-generated
   extra documents (resolver, hostname, firewall, extension services, volumes, user volumes, and
   link/bond/vlan/vip/wireguard/bridge/dhcp network documents) are rendered to YAML and merged in as
   a **strategic merge patch** via `configpatcher`, with an explicit TODO in the source admitting the
   cleaner route — appending documents through the upstream API — collides with what `generate`
   already emits (`multidocs.go:25-27`).
7. `pkg/generate/config.go:107-121` — patches are applied **node patches first, then top-level
   cluster-wide `patches:` last**, so cluster-wide patches take precedence over node-specific ones.
   Combined with step 3, the full effective precedence is: group → node → cluster.
8. `pkg/patcher/patcher.go:53` `PatchesPatcher` — per patch: optional SOPS decrypt, Go templating
   against the config generated so far, envsubst, then `configpatcher.LoadPatches` +
   `configpatcher.Apply`. Supports strategic-merge and RFC6902 JSON patches.
9. `pkg/generate/config.go:132` — validate, re-encode at 2-space indent, write to disk.

## 2. Talos version handling in talhelper — the mechanism behind the 25 August findings

**One mechanism, borrowed wholesale from upstream machinery:** `VersionContract`, parsed once at
`pkg/talos/input.go:23` and threaded through every subsequent decision.

- **The contract is capability predicates, not version-string comparison.** `siderolabs/talos`
  `pkg/machinery/config/contract.go` at `v1.14.0-alpha.2` defines **30** of them (32 methods on
  `*VersionContract`, minus `String` and `Greater`) — e.g. `SecretboxEncryptionSupported()` (>1.2),
  `KubePrismEnabled()` (>1.5), `MultidocNetworkConfigSupported()` (>1.11),
  `GrubUseUKICmdlineDefault()` (>1.11), `MultidocKubernetesConfigSupported()` (>1.13),
  `MultidocSysctlConfigSupported()` (>1.13). Each predicate is a one-line `contract.Greater(...)`
  comparison against a named well-known contract constant (`contract.go:26-43`).
- **talhelper branches on those same predicates for its own convenience fields**, and this is the
  concrete mechanism the 25 August note's spike observed empirically. Hostname, nameservers and
  `disableSearchDomain` go to legacy inline `machine.network.*` when
  `!MultidocNetworkConfigSupported()` (`nodeconfig.go:50-66`) and to separate multidoc documents when
  it is (`multidocs.go:58-75`). Network interfaces the same way (`nodeconfig.go:88-92` vs
  `multidocs.go:113-147`). CNI the same way again — inline `cluster.network.cni` versus a
  `KubeFlannelCNIConfigV1Alpha1` document (`pkg/generate/config.go:55-89`). Secrets are contract-aware
  too: the AESCBC-vs-secretbox fixup is double-guarded by `SecretboxEncryptionSupported()` **and**
  `!MultidocKubernetesConfigSupported()` (`nodeconfig.go:44-48`), and `secrets.NewBundle(clock, &vc)`
  itself branches on `UseRSAServiceAccountKey()` and on `contract.Greater(TalosVersion1_2)` inside
  machinery (`siderolabs/talos` `pkg/machinery/config/generate/secrets/bundle.go:255,286`).
- **Backwards-compatible only, and upstream says so directly.** `contract.go:16-17`: *"Config
  generation only supports backwards compatibility (e.g. Talos 0.9 can generate configs for Talos 0.9
  and 0.8). Matching version of the machinery package is required to generate configs for the current
  version of Talos."* This is upstream's own statement of exactly what the 25 August note derived
  empirically from the spike.
- **Hard forward cap, three independent ways:**
  - `go.mod:22` pins `github.com/siderolabs/talos/pkg/machinery v1.14.0-alpha.2`, and machinery is
    its own Go module (`siderolabs/talos` `pkg/machinery/go.mod:1`) — a single binary cannot hold two
    machinery versions at once.
  - `pkg/config/validator.go:70-84` hard-codes the accepted major.minor list `v1.2` … `v1.14`;
    anything outside is a hard `InvalidTalosVersion` validation error.
  - `pkg/config/defaults.go:16` defaults to `LatestTalosVersion = "v1.13.9"` when `talosVersion:` is
    unset.
  Now archived at that pin, talhelper can never render for Talos v1.15+.

**A genuine design defect, only visible from source: talhelper conflates two independent version
knobs.** `talosVersion:` in `talconfig.yaml` drives **both** the schema contract passed to
`WithVersionContract` (`input.go:86`) **and** the installer image tag passed to `WithInstallImage`
(`input.go:88`, literally `"ghcr.io/siderolabs/installer:"+c.GetTalosVersion()`). One string, two
purposes. This makes a staged upgrade — render a still-v1.11-shaped config while installing a v1.13
image — inexpressible. Neither this nor the 25 August note previously identified this; it only
becomes visible reading `input.go` directly.

### Correction to the 25 August note

§4 of the prior note states that talhelper's native network-interface/VIP fields "emit the
*new-style* multi-document network config … whereas the target environment was deliberately pinned to
the *legacy* inline style," implying a hard-coded choice. Source reading refines this: talhelper
**does** version-address those fields — see `nodeconfig.go:88-92` vs `multidocs.go:129-135` above.
The representation is a function of the declared `talosVersion:`, not fixed. What is genuinely
unavailable is choosing the legacy representation while declaring a ≥v1.12 target. The lesson from §4
survives unchanged — a convenience field silently pins a config-layout representation — but the
mechanism is "tied to the declared contract," not "hard-coded." (This correction has also been
applied directly to the 25 August note.)

**Maintenance-treadmill artefacts, now frozen by the archive:** a bundled 2.6 MB
`pkg/config/schemas/talos-extensions.json` mapping Talos version → valid system extensions and
overlays, regenerated by the separate `hack/tsehelper` tool against the extensions registry; the
hard-coded supported-version list; the machinery pin itself. None of these can move again.

## 3. topf — architecture

`postfinance/topf`, MIT licence, created 2025-12-24, **165 commits**, last push 2026-08-25 (one day
before this note), release `v0.5.0` (2026-08-10) with regular `v0.x` cadence and `-rc` prereleases
before each (`v0.5.0-rc.0/1`, `v0.4.2-rc.0`, …). 99 stars, 8 forks, 9 open issues. Contributors: 94 +
35 commits from two people (`clementnuss`, `sebastian-stephan`) against ~139 human commits total —
effectively two-maintainer, not single. Documented production use: PostFinance runs it in a GitLab CI
pipeline with in-house nodes-provider and secrets-provider binaries and a shared config repository
pulled before `topf apply` (`docs/production-usage.md`), which the codebase's provider abstraction
(`pkg/providers/`) exists specifically to support.

**Input model.** One small `topf.yaml` (`pkg/config/topf_config.go:19-57`: `ClusterName`,
`ClusterEndpoint`, `KubernetesVersion`, `TalosVersion`, `SchematicID`, `Factory`, `Platform`,
`SecureBoot`, `SecretsProvider`, `NodesProvider`, `PatchesDir`, `SecretsPath`, `Nodes[]`, `Data`) plus
a **directory tree of patch files**, deliberately kept separate from node definitions
(`docs/comparison.md`: "This makes patches easier to review in pull requests"). Node schema is
`pkg/config/node.go:14-32`: `Host, IP, Role (control-plane|worker), Data`, plus per-node overrides
`TalosVersion, SchematicID, Factory, Platform, SecureBoot`.

**Layering is three fixed directories, no inheritance graph:** `all/` → `<role>/` →
`node/<host>/`, lexicographic within each, later wins — `PatchContext.Load()`
(`pkg/config/patches.go:37-65`). Each folder is walked recursively for `*.ya?ml(\.tpl)?` files,
following symlinks (`patches.go:67-142`). Every YAML document in every file becomes its own
`configpatcher.Patch`; RFC6902 JSON patches are explicitly rejected with a dedicated error, citing
their deprecation in Talos ≥v1.12 (`patches.go:210-217`).

**Templating: Go `text/template` + sprig, `missingkey=error`, `.tpl` files only**
(`patches.go:144-166`). Template context (`PatchContext`, `patches.go:24-35`) exposes
`.ClusterName .ClusterEndpoint .KubernetesVersion .TalosVersion .SchematicID .Data.<k>
.Node.{Host,Role,IP,Data}`.

**Generation pipeline — native machinery, no `talosctl` shell-out for `render`.** The only
`exec.Command` calls in the entire tool are `sops`, `vals`, and user-supplied provider binaries.
Main entrypoint, `generateNodeConfig` (`internal/topf/nodes.go:105-184`), quoted in full for the
version-relevant part:

```go
secretsBundle, err := t.Secrets()
...
versionContract, err := talosconfig.ParseContractFromVersion(node.TalosVersion())
...
configBundleOpts := []bundle.Option{
    bundle.WithInputOptions(&bundle.InputOptions{
        ClusterName: t.Config().ClusterName,
        Endpoint:    t.Config().ClusterEndpoint.String(),
        KubeVersion: strings.TrimPrefix(t.Config().KubernetesVersion, "v"),
        GenOptions: []generate.Option{
            generate.WithSecretsBundle(secretsBundle),
            generate.WithVersionContract(versionContract),
        },
    }),
    bundle.WithVerbose(false),
}
```

(`internal/topf/nodes.go:144-167`). Note topf goes through `config/bundle.NewBundle` rather than
calling `generate.NewInput`/`input.Config` directly — `config/bundle` is machinery's own thin wrapper
that builds both control-plane and worker configs from one `InputOptions` plus role-specific patch
lists (`bundle.WithPatchControlPlane`/`WithPatchWorker`, `nodes.go:169-174`). `config/configloader`
and `config/container` are not imported anywhere in the non-test source.

**Abstractions — essentially none.** The entire cluster and node schema is the two structs cited
above. Exactly **one** synthesized field exists in the whole tool: an installer-image patch,
constructed as plain YAML and **prepended** to the patch list so any user patch can override it
(`internal/topf/nodes.go:26-39,140`):

```go
func installerImagePatch(image string) (configpatcher.Patch, error) {
    patchBytes, err := yaml.Marshal(map[string]any{
        "machine": map[string]any{"install": map[string]any{"image": image}},
    })
    ...
    return configpatcher.LoadPatch(patchBytes)
}
```

Even the node hostname is not derived automatically — you write a `HostnameConfig` patch yourself
(confirmed by the absence of any hostname-setting code outside the example directory). For a
version-spanning fleet this is topf's strongest structural property: there is almost nothing built in
for a new Talos config layout to break.

**Secrets — read *and* generate, unlike talstomize.** `t.Secrets()` (`internal/topf/secrets.go:19-47`)
loads an existing bundle from the configured `SecretsProvider` (default: local `secrets.yaml`, with
transparent SOPS decryption via `decryption.Cache`; or a pluggable external binary), and if none is
found, **generates one interactively** — `secrets.NewBundle(clock, nil)` (`secrets.go:58`, nil
contract = current version) — then stores it back through the same provider
(`generateAndStoreSecrets`, `secrets.go:49-81`). Every sensitive value in the bundle is registered
with a masking writer so it cannot leak to stdout (`secrets.go:83-102`,
`internal/maskedwriter/maskedwriter.go`).

**Image Factory / schematic.** Schematic IDs are computed **locally by default** (no network call)
via `image-factory/pkg/schematic`'s `schematic.Unmarshal(content).ID()`
(`internal/schematic/resolver.go:127-135`); `--submit-to-factory` optionally POSTs it via
`factoryclient.SchematicCreate`, deduplicated per `factory:id` key with `singleflight`
(`resolver.go:137-173`). `@`-prefixed schematic references support the same `.tpl` templating as
patches (`resolver.go:104-125`).

## 4. Talos version handling in topf — a materially better model than talhelper, but capped the same way

**The version-resolution chain deliberately prefers the live node**, `internal/topf/node.go:44-48`:

```go
// TalosVersion returns the Talos version to use for config generation.
// Fallback chain: running (from live node) -> topf.yaml -> bundled Talos version.
func (n *Node) TalosVersion() string {
    return strings.TrimPrefix(cmp.Or(n.runningVersion, n.t.Config().TalosVersion, version.Tag), "v")
}
```

`n.runningVersion` is populated by `collectNodeInfo` (`internal/topf/nodes.go:41-103`), which reads
`runtime.Version` **over the node's own COSI API** — available even in maintenance mode, per an
explicit code comment (`nodes.go:90`) — alongside `runtime.MachineStatus` and the `schematic`
extension version. This only runs when `render --online` (or any live-touching command) is used;
offline `render` falls back to the configured `topf.yaml` `talosVersion:`, then to the compiled-in
`machinery/version.Tag`.

**Crucially, the installer image computation uses the *configured* version, not the running one**
(`internal/topf/node.go:71-84`):

```go
func (n *Node) InstallerImage() string {
    ...
    talosVersion := strings.TrimPrefix(cmp.Or(n.Node.TalosVersion, cfg.TalosVersion, version.Tag), "v")
    ...
}
```

**This is the single most transferable idea found across the whole successor landscape: the schema
contract answers "what does this node run right now," and the installer image answers "what do I
want it to become," and topf keeps those as two genuinely independent values** — exactly the
distinction talhelper's single `talosVersion:` field collapses (§2). topf's `upgrade` command then
*acts* on that separation directly: it compares `node.RunningVersion()`/`RunningSchematic()` against
the schematic and version extracted from the desired installer image
(`internal/cmd/upgrade/upgrade.go:245-272`), and even picks the correct upgrade RPC based on the
running version — the legacy `MachineService.Upgrade` below Talos 1.13, the newer
`LifecycleClient.Upgrade` at or above (`upgrade.go:294-317`, gated by `supportsLifecycleUpgrade`,
`upgrade.go:304-316`) — a concrete, version-gated behavioural switch beyond config rendering.

**But the forward cap is identical to talhelper's, for the identical underlying reason.**
`ParseContractFromVersion` and `WithVersionContract` each appear **exactly once** in the whole
codebase (`internal/topf/nodes.go:149,162`); topf calls **no** capability predicate itself anywhere —
all contract-driven layout decisions happen invisibly inside machinery. `go.mod:12-13` pins **both**
`github.com/siderolabs/talos v1.13.8` and `github.com/siderolabs/talos/pkg/machinery v1.13.8`, no
`replace` directive. There is no flag, config key, or plugin mechanism to render with a different
machinery version — the only lever is which topf binary you run. Two `TODO (v1.14):` markers in
`internal/topf/node.go:21,24` (hard-coded default schematic hash and factory hostname, pending
`images.DefaultInstallerImageSchematic`/`gendata.ImageFactory`) show the maintainers track this
manually, release by release. No Talos-version-support matrix or compatibility statement exists
anywhere in the docs tree — checked `docs/comparison.md`, `docs/alternatives.md`,
`docs/production-usage.md`, `docs/configuration.md`, `docs/getting-started.md`, and the published
`postfinance.github.io/topf/main/comparison/` page.

**Practical bound (inference, not verified by reproduction):** because machinery's own doc comment
(§2) states generation is backwards-compatible only, a topf v0.5.0 binary should in principle render
correctly for Talos ≤1.13 and fail or mis-render for 1.14+ until topf itself bumps its pin and
releases — the same failure shape the 25 August spike observed empirically against talhelper. This
was not reproduced against a real old node in this session.

**The cap is currently moot for 1.14 specifically, and the two projects' posture toward it
differs.** Talos `v1.14.0` is still pre-release as of this note (`v1.14.0-rc.2`, published
2026-08-25 — checked via the upstream releases API); no production tool should be pinned to an
unreleased rc, so neither project lagging GA 1.14 support would be unremarkable on its own. What
differs is whether either is *tracking* it ahead of GA:
- **topf is not.** Checked `main` (still pins `v1.13.8`, identical to the `v0.5.0` tag — no bump),
  every open PR (6, all Dependabot dependency bumps — GitHub Actions, `image-factory`, `kubectl`,
  `cli/v3` — none touching `siderolabs/talos`), and every open issue (8, none mentioning Talos 1.14
  or a version bump). There is no branch or in-flight work of any kind toward 1.14 support.
- **talhelper was.** Its *final* release, `v3.1.17`, had already bumped machinery to
  `v1.14.0-alpha.2` (§2, `go.mod:22`) — a pre-release pin, ahead of where topf sits today, evidence
  that talhelper was actively tracking Talos's pre-release cycle right up to the day it was
  archived.

So the forward cap itself is not a defect — it is what "backwards-compatible only" necessarily
produces in any tool built this way, topf included. The thing worth watching is cadence: whether
topf picks up 1.14 promptly once it reaches GA is a real signal of whether it will keep pace with
Talos releases going forward, given no work toward it has started with 1.14 already at rc.2.

## 5. What this means for bronzeward

- **Three independent source-level confirmations now**, not one spike: talhelper, topf, and (per the
  companion note on talstomize) talstomize all hit the identical forward cap, for the identical
  reason — machinery is a single Go module, one version links into one binary, and upstream states
  generation is backwards-compatible only. The 0.4 design's "version-addressed renderer" cannot be a
  library-selection detail inside one process; it is necessarily **multi-process or multi-binary**,
  one pinned machinery build per supported contract window, selected per machine by that machine's
  target Talos version.
- **Model the schema contract and the installer image tag as two independent inputs from day one.**
  topf's `TalosVersion()` (running-preferred, for the contract) versus `InstallerImage()`
  (configured-preferred, for the image tag) is a proven, working separation of exactly the two
  concerns talhelper conflates into one field and one bug class. Adopt the distinction, not
  necessarily the "prefer the live node" default — bronzeward's release model (explicit, reviewed
  Releases per the 0.4 design) may prefer the configured target for both by design, but the two
  values must be independently settable.
- **Drift and upgrade decisions belong on the running node's own reported state, not on the rendered
  config or the declared install image.** topf's `upgrade` compares `RunningVersion()`/
  `RunningSchematic()` (read live over COSI) against the desired installer image before deciding
  whether to act, and even branches its upgrade RPC choice on the running version. talstomize's `diff`
  makes the same discovery independently (per the companion note). This is now a repeated,
  independently-arrived-at pattern worth treating as a requirement, not an optional nicety.
- **A near-zero convenience-abstraction surface is what let topf survive Talos releases at all** —
  its only synthesized field is the installer image patch, deliberately overridable. This reinforces,
  with a second independent data point, the 25 August note's §4 conclusion that the platform's own
  abstraction layer must stay thin and version-addressed in the same way the renderer is.
- **Group→node config merge semantics deserve an explicit, singular decision.** talhelper's is
  whole-field replace by reflection with three hand-listed concatenation exceptions — a surprising
  rule, and one where the *interesting* composition (multiple patches touching the same list) only
  ever happens in the patch chain, never in the struct merge. bronzeward's fixed layers should compose
  one way, not two different ways depending on which field is involved.
- **topf's directory-per-scope patch layout (`all/`, `<role>/`, `node/<host>/`) is a plausible
  reviewability pattern** for a git-backed fragment tree, independent of the version-handling
  findings — worth considering purely for how well it reviews in a pull request, separate from
  bronzeward's own release/approval model.

---

## 6. talstomize — quick assessment (no source deep-dive; comparison only)

`mirceanton/talstomize`, MIT, 5 stars, single-maintainer (the only other "contributor" is his own
Renovate bot), created 2026-08-06, `v0.1.0-rc.1` prerelease — three weeks old at the time of this
note. Kustomize as a *name*, not as *semantics*: one `talstomize.yaml` with ordered patch lists
(implicit hostname → implicit install-image → cluster `patches` → role patches → node patches), not
a base/overlay directory tree.

**It has no Talos version-contract handling at all.** `generate.NewInput` is called with only
`WithSecretsBundle`, `WithEndpointList`, `WithAdditionalSubjectAltNames`, optional `WithDNSDomain` —
no `WithVersionContract`. `Options.VersionContract` stays `nil`, which machinery defines as
*current*, so every render emits the newest layout its pinned machinery (`v1.13.9`) knows,
regardless of what the target node actually runs. This is qualitatively worse than talhelper's and
topf's forward cap (§2, §4): those tools *have* the contract mechanism and are capped only going
forward past their pin; talstomize doesn't invoke the mechanism at all, so it is not even
version-*aware* for older targets — it would silently mis-render for any node behind the newest
contract, not just ahead of the pin. Scope is narrow by design: no secret generation (reads an
existing `talosctl gen secrets` bundle), no upgrade orchestration, four subcommands (`build`,
`apply`, `diff`, `version`).

**One idea worth the same credit as topf's, arrived at independently:** `diff` compares rendered
config against live machineconfig *and separately* against the node's actually-booted OS version,
installed extensions and kernel args — because `install.image` is not evidence of what is running.
Same insight as topf's `RunningVersion()`-driven `upgrade` (§4), discovered independently by a
different single-person project. Two independent implementations converging on "trust the running
node, not the declared install image" is a stronger signal for bronzeware's drift design than either
alone.

**Three-way comparison, for reference:**

| | talhelper | topf | talstomize |
|---|---|---|---|
| Version contract | Yes, conflated with install image | Yes, correctly split (running vs. configured) | **None called** |
| Abstraction surface | Heavy (native fields) | Near-zero (one synthesized patch) | Near-zero |
| Secrets | Generate + SOPS | Generate + SOPS/vals | Read-only, external gen required |
| Maturity | Archived, 694★ | Active, 99★, documented production use | 3 weeks old, 5★, solo |
| Drift/upgrade vs. live node | No | Yes (`upgrade`) | Yes (`diff`) |

**Verdict for bronzeward:** talstomize reinforces the fragment-composition and running-state-drift
lessons a third time but contributes nothing architecturally new, and its missing version-contract
call makes it the one of the three actively unsuitable for a version-spanning fleet — not merely
capped like the other two, but unaware of the concern entirely. Not a candidate worth deeper
evaluation.

---

## What this note does not establish

Nothing here was reproduced by actually running either tool against a real Talos node of a different
minor version than its pinned machinery — every forward-cap claim rests on reading the mechanism
(machinery's own module boundary, the doc comment in `contract.go`, the absence of any override path)
plus the 25 August note's empirical spike against talhelper specifically, not a fresh spike against
topf. topf's exact backwards-compatibility window (how far below v1.13.8 it actually renders
correctly) was not tested. §6's talstomize assessment is a comparison pass, not a source deep-dive —
no `path:line` citations there, unlike §1-4. This note says
nothing about bootstrap sequencing, secret rotation at fleet scale, or transport — all still open in
the main design document.
