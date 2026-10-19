# Upgrade safeguards and recovery design

Research for [ginsys/bronzeward#166](https://github.com/ginsys/bronzeward/issues/166),
19 October 2026. This is a design for the later machine-lifecycle phase, not
upgrade support or evidence that an upgrade is safe. The PoC still supports
only its specified worker `apply-config`/`no-reboot` operation.

The recommended direction is a reviewed, immutable upgrade plan with durable
per-step intent, explicit partial-outcome recovery, and fresh health evidence
before progressing to another node. A successful command, a timeout or a
healthy node alone cannot establish what an abandoned request may still do.

## 1. Evidence and scope

The issue describes an operator-paced procedure used on seven nodes. Its
reported drain failures are motivating observations, not experiments reproduced
here: the script, service products and versions, cluster configuration and
original transcripts were not supplied. No production cluster was contacted.

This investigation reads the following pinned upstream sources. It adopts no
upstream code. The research fixture records source hashes and checks the named
call paths; a reversed synthetic phase order and a corrupted-cache control
exercise the checker, not the proposed recovery model.

| Source | Pin | What it establishes |
| --- | --- | --- |
| Talos | v1.14.0, `9abd05af449ebf9cb1827648298291afce18d714` | CLI upgrade order, API selection, Kubernetes upgrade and dry-run call paths |
| go-kubernetes | v0.2.41, `0caf1f226727cc05f80b92c27c9116e68fb5532d` | The dependency Talos pins; manifest inventory creation and SSA dispatch |
| fluxcd/pkg/ssa | v0.77.0, `b98e2b0975e0863f32790229ce7c9f097ed67706` | The dependency go-kubernetes pins; forced field ownership in apply |
| SideroLabs documentation | `d7cf508eebc2f8dcc1439560c4856735e8c82fc4`; documentation MCP queried 2026-10-19 | v1.14 configuration activation and documented reboot-impact examples |
| Bronzeward | main `ab327884527fee3b3ed32f0e2dfc8ffde5bf0ce3` | Design and ER constraints used by this note |

The documentation MCP at `https://docs.siderolabs.com/mcp` supplied
`search_sidero_documentation` and
`query_docs_filesystem_sidero_documentation`. Talos searches specified
`v1.14`; page reads used the paths returned by search. The documentation's
version variable currently renders v1.14.2. These documentation observations
are distinct from inspection of the v1.14.0 source, and neither is a live
lifecycle test. The repository's [E3 report](20260925-talos-compatibility.md#63-criterion-3-unsupported-combinations-and-the-deferred-lifecycle-tests)
still leaves lifecycle execution unproven. Other official documentation links
below were read on 2026-10-19; only the configuration page is part of the
hash-pinned documentation input.

## 2. Findings that change the procedure's interpretation

1. **Install can precede drain.** In the pinned Talos
   [`upgradeViaLifecycleService`](https://github.com/siderolabs/talos/blob/9abd05af449ebf9cb1827648298291afce18d714/cmd/talosctl/cmd/talos/upgrade.go),
   image pull and `upgradeInternal` precede `drainNodes`. Reboot follows drain.
   A drain error returns before the deferred uncordon is installed. This source
   path explains how the issue's reported partial state is possible; it does
   not reproduce that incident. The same function selects the lifecycle path
   using `>1.13.0-alpha.2 <2.0.0`, with a legacy path for an out-of-range
   server. A major/minor comparison alone loses the prerelease boundary.
2. **Kubernetes dry run is not an unconditional no-write guarantee.** The
   [upgrade path](https://github.com/siderolabs/talos/blob/9abd05af449ebf9cb1827648298291afce18d714/pkg/cluster/kubernetes/talos_managed.go)
   calls pre-pull when `PrePullImages` is enabled independently of `DryRun`.
   SSA diff obtains an inventory; the pinned
   [inventory factory](https://github.com/siderolabs/go-kubernetes/blob/0caf1f226727cc05f80b92c27c9116e68fb5532d/kubernetes/ssa/internal/inventory/configmap/inventory.go)
   creates a missing ConfigMap, and its caller can create the namespace. A
   read-only Bronzeward preview must avoid both paths; checking existence once
   is insufficient if the object disappears between that check and the call.
3. **`--manifests-force=false` does not mean no field takeover.** That option
   concerns recreation after immutable-field changes. The pinned
   [SSA apply](https://github.com/fluxcd/pkg/blob/b98e2b0975e0863f32790229ce7c9f097ed67706/ssa/manager_apply.go)
   uses `client.ForceOwnership` independently. Inspect field ownership and
   refuse overlap before using an executor with this behavior.
4. **PDB checks are evidence, not a reservation.** Kubernetes
   [PDB status](https://kubernetes.io/docs/reference/kubernetes-api/policy/pod-disruption-budget-v1/)
   is valid only for the observed object generation. Eviction admission can
   change after a read. Match target pods to budgets, account for the
   unhealthy-pod eviction policy, and let the Eviction API enforce the budget.
   Zero allowed evictions does not mean every node's drain is blocked: the
   pods matched and their readiness matter. Preserve conservative cluster
   health policy separately from the narrower eviction feasibility check.
5. **A drain deadline is not proof that draining stopped.** Kubernetes
   [eviction](https://kubernetes.io/docs/concepts/scheduling-eviction/api-eviction/)
   respects pod termination grace periods. A five-minute client wait cannot
   establish completion of a thirty-minute shutdown. The old executor and
   accepted evictions remain part of the recovery evidence.
6. **Configuration storage and activation differ in v1.14.** The
   [configuration guide](https://docs.siderolabs.com/talos/v1.14/configure-your-talos-cluster/system-configuration/editing-machine-configuration)
   removes automatic reboot from apply. Explicit staging leaves the running
   configuration unchanged; an immediate apply may change the stored/running
   configuration while a service retains old settings. Reading the new
   configuration digest does not prove those settings are active.

## 3. Safeguard dispositions

“Adopt” below means retain in the later-phase design. It does not enable an
operation or extend the PoC contract. P is plan validation, E execution and
O observation. Every executing check must be repeated on current evidence;
a plan-time check is not a dispatch authorization.

| Reference safeguard | Disposition and placement | Rationale and required boundary |
| --- | --- | --- |
| Select one node by declared inventory, derive address and client | Adopt, P/E/O | Bind machine identity, cluster, assignment revision and route. Re-observe before each mutation; a reused address must not select another machine. |
| Preserve running schematic on intermediate hops; final pinned schematic | Adopt, P/E/O | Bind current and target schematic and architecture per hop. Refuse unknown customizations; compare the observed target after boot. |
| Resolve installer to registry digest | Adopt, P/E | Bind image digest, platform and provenance. Registry availability is separate from compatibility or a signature policy, which this note does not choose. |
| Default to fleet dry run; explicit act flag | Change, P/E | Preview is read-only and authorization is a plan approval, not a shell flag. Exclude pre-pull, inventory creation, server-side mutation and credential generation from preview. |
| Print worker-first, control-plane-last commands | Change, P | Present an ordered rollout plan with stop points. Start with one worker at a time and one control plane at a time; ordering remains conditional on capacity, quorum and the operation's evidence. Printed commands cannot carry durable authority. |
| Exactly one node per act, lock one upgrade at a time | Adopt initially, E | One cluster rollout slot plus the machine scope, with owner/generation/epoch checks in each attempt transaction. A local process lock does not fence an old remote request. |
| Refuse downgrade and skipped minor | Adopt, P/E | The tested adjacent-minor path is narrower than machinery compatibility. Downgrade remains unsupported; never infer upgrade safety from accepted version parsing. |
| Require latest current-minor patch before a minor hop | Change, P/E | Bind a dated release-catalogue snapshot and selected patch. Re-evaluate if the catalogue changes before dispatch; do not silently replace an approved target. Offline or unknown catalogue evidence holds the plan. |
| Kubelet within target OS support window | Adopt, P/E | Check every affected node and both published support policy and selected machinery. E3's documented disagreement is unresolved evidence, not permission to choose the wider window. |
| Refuse nonexistent installer image | Adopt, P/E | Resolve the exact platform image before approval and confirm availability before installation. Absence or unreadability must not fall back to a tag or default installer. |
| Re-read every health check each round | Adopt, O/E | A round has a bounded collection interval and explicit freshness. It passes only when all bound checks pass within that round. Prior successes cannot mask a later failure. |
| Bound each call by remaining time | Adopt, E/O | Persist phase and verification deadlines. A call gets at most remaining time; a timed-out read is unreadable, not failed health or permission to retry a mutation. |
| All nodes Ready, none cordoned; Talos/etcd/control-plane/kubelet health | Change, P/O/E | Keep a full preflight and completion gate. During recovery, allow only a named target's expected, operation-owned cordon and explicitly bound temporary impairments; never label them healthy or exempt another node. |
| Audit PDB specifications and live zero-disruption status | Change, P/O/E | Resolve selectors against target pods, observed generations and unhealthy-pod policy. Show permanent and temporary blockers separately. Do not edit a budget or bypass eviction to pass the gate. |
| Deployments, StatefulSets and DaemonSets fully ready | Adopt as default, P/O | Bind workload identities, desired generations and readiness predicates. Intentional partitions, scale-to-zero and expected node-local loss need explicit policy, never an implicit exception. |
| Database operator phase/readiness | Adopt as a provider category, P/O | Version-specific native status predicate; both phase and readiness checked. Exact product and CRD version remain to be selected and evidenced. |
| Object-store endpoint; broker per-server stream health | Change, P/O | Named read-only API/proxy routes with bounded responses and per-server coverage. Pod Ready alone is not an equivalent signal. Missing access is unreadable; no exec or new service secret fallback. |
| Metrics-storage operator and MQTT cluster status | Adopt as provider categories, P/O | Native, versioned predicates must cover all members required by that product's contract. No products or field names are invented from the generic issue description. |
| Run client matching running OS | Adopt, P/E | Pin client and server versions per hop and qualify the exact RPC path. Matching versions is [upstream guidance](https://docs.siderolabs.com/talos/v1.14/getting-started/talosctl#linux-and-macos), not proof of either upgrade API's recovery behavior. |
| Install, cordon, drain, reboot | Change, E/O | Record separate intents/results and observe actual partial progress. Select a proved ordering per API/version. Do not model the opaque command as one atomic step. |
| Wait for version, schematic and Ready; uncordon if needed | Change, E/O | Those are necessary postconditions, not attempt accounting. Uncordon only an operation-owned cordon with unchanged ownership evidence, under bound recovery authority and fresh capacity checks. |
| Run full post-gate before next node | Adopt, O/E | Hold rollout progression until every mutation is accounted and target and cluster postconditions hold. A target-only success cannot hide service degradation. |
| Kubernetes upgrades refuse mixed OS versions and skipped minor | Adopt as initial policy, P/E | Finish and verify the OS rollout first. This is deliberately stricter than a library accepting compatible mixed versions; widening it needs separate qualification. |
| Check Kubernetes target against lowest OS minor | Change, P/E | Check every node's actual version plus Kubernetes component skew and release-specific migrations. The minimum version alone is not a complete compatibility proof. |
| Refuse missing manifest inventory and GitOps-owned bootstrap objects | Adopt, P/E | Preview uses read-only inventory access and no create-on-read library. Execution binds object UIDs/resource versions and ownership; unknown ownership or overlap refuses. An explicit ownership handover is separate work. |
| Kubernetes dry run does not pre-pull | Adopt, P/E | Inspect the actual call path and test zero writes; setting `DryRun` alone is insufficient in the pinned source. |

## 4. Immutable plan and health-provider boundary

A later operation contract must bind the rollout's node identities and order,
assignment revisions, source/target versions, image digests and schematics,
client/RPC selection, compatibility evidence, explicit modes, all phase
parameters, health policy revision, expiry, approval, retry limits and transport
and verification deadlines. It also binds the recovery actions it authorizes.
Changing these inputs requires a new plan/approval under design §12.7.

Health policy should be declarative per cluster with a small set of audited,
versioned readers. A binding names the Kubernetes resource or approved service
proxy route, identity/selection scope, predicate, required coverage, freshness
and timeout. Readers return pass, fail or unreadable with safe reason codes,
observation basis and object identity/version. They do not return secret-bearing
objects, execute arbitrary commands or accept an arbitrary URL as a health check.

Existing Kubernetes API authorization should suffice for the selected native
signal where possible. Bronzeward's PoC has no general Kubernetes observer
credential contract. This note does not grant one or change Talos credentials:
future specification must define the credential ingress, minimal RBAC, rotation,
revocation and redaction checks. A service needing extra credentials is a separate
policy decision or an unavailable check, never an automatic credential request.

Alternatives: hard-code the seven-node installation's products (simple but
neither supplied nor portable); arbitrary scripts/Jobs (flexible but introduce
execution, credentials and mutable code into the gate); use only generic Ready
(insufficient for the named service failures). Declarative native readers give
reviewable predicates while retaining product-specific evidence requirements.

## 5. Partial-upgrade recovery model

Keep the ER operation states. Installation, cordon, drain and boot are durable
phase facts beneath an operation, not new terminal states and not additions to
today's schema. Each external mutation needs its own recorded attempt and
accounting. A response accepting installation is not evidence that a reboot or
drain completed. Each phase's postconditions and response semantics need their
own VM/hardware qualification.

Record the original and target OS/schematic/image, machine and Kubernetes node
identity, assignment, owner/generation/epoch, phase intent and attempt, deadlines,
response class, observation basis, boot identity where available, and cordon
provenance. Cordon evidence includes whether the node was already unschedulable,
the observed object version and the operation that changed it. Kubernetes' boolean
`unschedulable` alone does not establish ownership. A later ownership conflict
prevents automatic uncordon.

| Observed situation | Classification and permitted next step |
| --- | --- |
| Preview or preflight refuses, no commitment | No operation; preserve the refusal and replan after the cause changes. |
| Committed, no attempt, then takeover or authority lost | `unresolved`, scope held; inspect current state. Cancellation is permitted only under ER's no-attempt rule. A new attempt needs valid authority and operation-specific retry evidence. |
| Image installed, drain refused or timed out, node cordoned | `unresolved` unless every phase attempt is accounted and a specified terminal postcondition is established or contradicted. Keep installation and cordon facts; do not reinstall or uncordon simply to make preflight green. |
| Drain partly complete, long-running pod termination | Keep scope and rollout slot. Record accepted evictions and remaining pods/grace deadlines. Wait/observe, or seek the specifically authorized recovery action; never force-delete as an implicit timeout fallback. |
| Reboot requested, response lost | `unresolved` even if target version later appears. Establish accounting for that request before releasing scope or starting another upgrade. |
| Target booted and Ready, but a service check still fails before deadline | Continue verification; do not advance the rollout. At/after the bound deadline apply the bound-health rule; unreadable remains unresolved. |
| Every required phase has its proved outcome; at least one authorized mutation attempt exists; every attempt accounted; fresh bound postconditions match | `completed`; release the scope and slot, then re-evaluate the next node's gate. A target reached out of band cannot complete an unattempted upgrade. Keep OS result separate from the existing configuration `Applied` value. |
| Every attempt accounted; a terminal postcondition contradicts | `failed`, provided the ER ordering and health-window conditions hold and an unresolved operation is not classified safe to retry. A corrective plan is required; neither downgrade nor rollback is automatically authorized. If an upgrade might still act, the accounting premise is absent and the result remains unresolved. |
| Restore or ownership change during any phase | ER's epoch and takeover rules prevent further stale attempts. Stop old executors, enter explicit recovery and reconcile external state; a restored journal may have lost installation, cordon or reboot facts. |

**Resume is not rerun.** The new owner observes the exact current phase and
accounts prior attempts. It resumes only a missing step whose retry/continuation
safety is established for this API/version and whose approved parameters still
hold. Otherwise a new corrective plan is required. The PoC's configuration-digest
retry proof and thirty-second accounting floor do not establish upgrade retry
safety. A newer healthy observation does not fence an old RPC.

**Abort is not undo.** Before any attempt, cancellation follows ER. After an
attempt, revoke future authority and retain unresolved accounting as needed.
Restoring schedulability, replacing an installed image or rebooting are separate
mutations with explicit authority and evidence. Automatic rollback and blanket
uncordon were rejected because they can undo another operator's work or activate
an unverified image.

**Drain deadlines must be planned.** Compare the configured drain bound with
the affected workloads' termination grace periods and recovery budget. If the
bound is shorter, refuse or present an explicit, reviewed partial-drain recovery
plan; never silently increase it after approval. Extending a wait does not repair
a hung application and does not prove the old drain request stopped.

## 6. Configuration activation under Talos 1.14

The MCP documentation's examples include etcd settings, environments retained by
running services, removal or parameter changes of loaded kernel modules, volume
encryption-key rotation, and workload-isolation settings. These can need reboot
or service restart for activation. Install settings are different: a reboot
alone does not apply them to an already installed node. The list is explicitly
non-exhaustive.

Use a versioned impact classification with source evidence: immediate,
requires explicit activation, install/upgrade-only, or unknown. Bind it to the
whole effective configuration diff, not only the edited field. Unknown holds
the operation for review. A no-reboot RPC response or matching configuration
digest cannot decide activation. Qualify the required observations on VMs or
hardware before permitting a new class; retain the existing PoC's narrower
v1.13.6 evidence.

Alternatives rejected: reboot after every change (adds unapproved disruption);
infer service activation from the apply response alone; consider every
accepted v1.14 apply immediately effective (confuses stored configuration with
service state). Explicit staging and explicit reboot remain separate operations,
with their own approval and interruption tests.

## 7. Kubernetes and GitOps ownership

The initial design refuses a Kubernetes upgrade while an OS rollout is partial,
while required compatibility evidence is unknown, or while a bootstrap object
has conflicting or unknown management ownership. Read inventory and managed
fields without creating anything. `--manifests-force` controls a different
behavior from SSA field ownership, so merely leaving it false is insufficient.

A future contract can select either GitOps-owned manifests with a proved upgrade
path that omits their reconciliation, or a separately approved ownership
handover. It must show object creation, patching, pruning, ownership races and
partial failure behavior before using either. Automatically pausing GitOps or
taking over fields is rejected. The source investigation establishes the hazard;
it does not validate a safe replacement executor.

## 8. Reproduction, outcomes and limits

The [source-path fixture](../../../experiments/lifecycle-upgrade-design/README.md)
contains a manifest of all 11 repository/revision/path/SHA-256 tuples, a
standard-library Python checker and sanitized results. Reproduce from its
directory with Python 3.10 or later:

```sh
python3 source-check.py --fetch /tmp/bronzeward-upgrade-sources
python3 source-check.py /tmp/bronzeward-upgrade-sources
```

On 19 October 2026 with Python 3.13.5, the fresh fetch and offline rerun both
exited 0. Each checked 11 pinned hashes, nine call-path/documentation assertions
and the reversed synthetic phase-order control. The fetch additionally checked
all 11 download hashes. Replacing the cached `talos-upgrade` with synthetic text
made the checker exit 1 at its hash check, as expected. The fixture README gives
the exact control recipe and links the retained outcomes. Its output scan found
no private-key block, provider-token field, client-key field or temporary control
path; no application secret-handling claim follows from that public-source scan.

The source checks are static evidence; the controls validate the checker,
not the recovery model. No upgrade, drain, reboot,
Kubernetes mutation, service-health probe, real credentials or live recovery is
part of the experiment. No availability, quorum, storage safety, support-matrix
completeness or universal retry claim follows.

## 9. Work this enables

The follow-ups are [ginsys/bronzeward#237](https://github.com/ginsys/bronzeward/issues/237)
for the operation/observer/activation contracts,
[ginsys/bronzeward#238](https://github.com/ginsys/bronzeward/issues/238) for VM/hardware
qualification, [ginsys/bronzeward#239](https://github.com/ginsys/bronzeward/issues/239)
for Talos implementation, and
[ginsys/bronzeward#240](https://github.com/ginsys/bronzeward/issues/240) for Kubernetes
implementation. Their acceptance criteria and native dependencies own delivery;
these links explain the work enabled, not a second blocker list.

The next specification work is the lifecycle plan/phase contract, the observer
and credential boundary, and the Kubernetes ownership contract. Implementation
and VM/hardware qualification stay separately tracked outside the PoC. The
bound-health tests remain [ginsys/bronzeward#188](https://github.com/ginsys/bronzeward/issues/188)
until a future operation class binds a health check; this note adds none to
`apply-config`. The model supplies criteria for the later
[ginsys/bronzeward#165](https://github.com/ginsys/bronzeward/issues/165) pattern
comparison; it neither evaluates nor adopts tuppr here.
