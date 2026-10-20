# tuppr patterns and reuse investigation

20 October 2026. Research for [#165](https://github.com/ginsys/bronzeward/issues/165).

## Recommendation and decision enabled

Use selected **patterns**, implemented within Bronzeward's existing contracts.
Do not incorporate tuppr code, add it as a runtime dependency, or deploy its
operator as a Bronzeward executor on this evidence. The useful patterns are
persisted per-node deadlines, observation after temporary execution objects
disappear, explicit maintenance windows, declarative health predicates and
failure-oriented test cases. Their upstream state transitions are not a drop-in
implementation of Bronzeward's approval and recovery rules.

The investigation enables the later lifecycle specification to select these
patterns while rejecting incompatible execution and configuration ownership.
It does not enable upgrades. [Design §18.3](../Talos_Configuration_and_Machine_Management_Design.md#183-phase-2---machine-lifecycle),
the [execution/recovery contract](../../spec/execution-recovery.md) (ER), and the
[upgrade safeguard investigation](20261019-upgrade-workflow-design.md) remain
authoritative. The PoC still supports its existing configuration-control scope.

## Sources and reproduction scope

The inspected tuppr revision is
[`377fbf03df05420ca833230233e67877e5f343d4`](https://github.com/home-operations/tuppr/tree/377fbf03df05420ca833230233e67877e5f343d4).
It is not release `0.5.7`, whose target is `2d0f0311a8fc2f3cdd639f441b88fe22efd7f32d`.
All upstream links below name the inspected commit.

The [minimal fixture](../../../experiments/tuppr-patterns/README.md) executes
selected upstream tests with fake Kubernetes and Talos clients. Two original
Bronzeward compatibility probes are supplied through Go's build overlay: the
upstream source checkout stays unchanged. It does not run the controller binary,
integration suite, QEMU harness, container runtime, Kubernetes API or Talos RPCs.
Synthetic configuration strings and object names are the only test inputs.

The module declares Go 1.26.5; upstream's tool configuration pins Go 1.27.2. The
recorded run uses Go 1.27.1 on Linux/amd64, which satisfies the module's minimum
but is not the upstream CI toolchain. Relevant dependency pins are Talos machinery
1.14.1, Kubernetes modules 0.37.1, controller-runtime 0.25.1, CEL 0.32.0,
go-cron 0.16.1 and yaml.v3 3.0.1. The fixture records module/config hashes and its
own source hashes. Fake node version strings such as `v1.12.0` are test data,
not evidence of an upgrade against that Talos version.

## Component assessment

| Candidate | Recommendation | Rationale, alternative and evidence limit |
| --- | --- | --- |
| Upgrade progress and recovery | Adapt the persisted-deadline and post-Job-GC observation patterns; reject unchanged state-machine reuse. | [Reboot tracking](https://github.com/home-operations/tuppr/blob/377fbf03df05420ca833230233e67877e5f343d4/internal/controller/talosupgrade/jobs.go) survives Job loss. However, timeout can produce a terminal failed state, and a failed Job can be accepted when the node reports the target version. ER requires accounting for every old attempt before terminal classification or scope release; a Ready/version observation alone is insufficient. |
| Drain and rollout | Reimplement operation-owned cordon/drain phases; retain upstream scenarios as test ideas. | The [drainer](https://github.com/home-operations/tuppr/blob/377fbf03df05420ca833230233e67877e5f343d4/internal/controller/drain/drain.go) uses eviction and waits for selected Pod UIDs, but its uncordon helper clears the unschedulable flag without proving ownership. [Batch orchestration](https://github.com/home-operations/tuppr/blob/377fbf03df05420ca833230233e67877e5f343d4/internal/controller/talosupgrade/upgrade.go) treats volume-detach timeout as best-effort. Preserve explicit partial eviction, foreign cordon and unknown detach outcomes instead. No real PDB, quorum or detach qualification is claimed. |
| Health | Adapt declarative validation and full evaluation rounds; reject the checker unchanged as an operation completion policy. | The [CEL checker](https://github.com/home-operations/tuppr/blob/377fbf03df05420ca833230233e67877e5f343d4/internal/healthcheck/checker.go) distinguishes expressions and selection, but an empty unfiltered list passes while an empty selector result fails. Its default ten-minute timeout can be increased by check timeouts; a shorter parent context can still end it. Bronzeward needs bound coverage, freshness, pass/fail/unreadable and deadline semantics, not an inferred generic healthy boolean. |
| Scheduling and hooks | Adapt explicit clock/timezone/window evaluation; reject automatic hook bypass or execution as implicit approval. | [Maintenance windows](https://github.com/home-operations/tuppr/blob/377fbf03df05420ca833230233e67877e5f343d4/internal/controller/maintenance/window.go) accept an explicit time and return the next eligible window; nil windows allow execution. Source review also finds a seven-day backward search for the preceding cron occurrence. Completed pre-hooks [suppress inter-batch health checks](https://github.com/home-operations/tuppr/blob/377fbf03df05420ca833230233e67877e5f343d4/internal/controller/talosupgrade/upgrade.go#L327-L348); the initial check still runs. Future Bronzeward plans must bind these choices and define continuation when a window closes. Scheduling and hook execution were not exercised locally. |
| Image and compatibility | Adapt runtime schematic discovery, extension-preservation refusals and pre-pull separation. | [Image construction](https://github.com/home-operations/tuppr/blob/377fbf03df05420ca833230233e67877e5f343d4/internal/controller/talosupgrade/upgrade.go) derives targets from live configuration, runtime extensions and mutable node annotations. Bronzeward instead needs approved image digest, platform, source/target versions, schematic and client/RPC bindings. A pre-pull is a mutation; it is not read-only preview. Version selection does not prove a supported transition. |
| Native configuration | Reject the read/edit/reapply helper as the configuration-writing path. | [PatchNodeInstallImage](https://github.com/home-operations/tuppr/blob/377fbf03df05420ca833230233e67877e5f343d4/internal/talos/client.go#L257-L279) reads full machine configuration, edits the install image and applies the resulting configuration with retries. [Multi-document handling](https://github.com/home-operations/tuppr/blob/377fbf03df05420ca833230233e67877e5f343d4/internal/talos/machineconfig.go) usefully preserves unknown documents, but does not provide Bronzeward's secret ingress, immutable artifact or concurrent-write contracts. Completion can continue after image synchronization fails. Use a separately approved Bronzeward artifact transition instead. |
| Operational visibility | Adapt phase/condition names and expiring auxiliary leases; retain the operation timeline as the audit authority. | [Status patches](https://github.com/home-operations/tuppr/blob/377fbf03df05420ca833230233e67877e5f343d4/internal/controller/upgradeaudit/status.go) replace mutable fields; [history](https://github.com/home-operations/tuppr/blob/377fbf03df05420ca833230233e67877e5f343d4/internal/controller/upgradeaudit/upgradeaudit.go) is bounded. Neither conditions, metrics nor notifications substitute for intent, dispatch, response and recovery records. [Alertmanager leases](https://github.com/home-operations/tuppr/blob/377fbf03df05420ca833230233e67877e5f343d4/internal/alerting/silencer.go) expire if renewal stops, but add external mutations and credentials; no integration is selected. |
| Test design | Adapt fake-clock/status-restart cases and multi-shape qualification scenarios. | The [controller unit tests](https://github.com/home-operations/tuppr/blob/377fbf03df05420ca833230233e67877e5f343d4/internal/controller/talosupgrade/controller_test.go) expose useful partial outcomes cheaply. The [QEMU scenarios](https://github.com/home-operations/tuppr/blob/377fbf03df05420ca833230233e67877e5f343d4/test/e2e-qemu/README.md) cover different cluster shapes, but their Talos 1.14+ requirement does not establish the older Upgrade/LifecycleClient transition. No upstream CI result is relabelled as a local test. |

## Executed checks

Run 2 completed at 20:34:46 UTC on 20 October 2026. All 19 selected upstream
top-level tests passed (46 passing test/subtest result events); both compatibility
probes failed as expected. No selected test was skipped. The runner exited 0
because every expected outcome matched, and the upstream checkout remained clean.
[Results and commands](../../../experiments/tuppr-patterns/evidence/results.json)
and per-case JSON logs are retained with the fixture.

| Group | Expected and observed | What this establishes |
| --- | --- | --- |
| Recovery, 11 tests | Exit 0; all pass | Job creation, health refusal, explicit retry reset, persisted deadlines, observation after Job GC, timeout handling and cleanup of stale status behave as the selected fake-client tests assert. It also reproduces completion despite image-sync failure and a failed Job followed by an observed target version. |
| Coordination, 1 test with subcases | Exit 0; all pass | Blocking among tuppr upgrade resources; no Bronzeward coexistence proof. |
| Configuration, 2 tests with subcases | Exit 0; all pass | Synthetic legacy/multi-document image extraction and editing, unknown-document preservation and rejection of missing image data. No real secret or concurrent writer was involved. |
| Health, 5 tests with subcases | Exit 0; all pass | Validation, absent object, one failing resource and the two empty-selection behaviours. Not a full health-window timing test. |
| Foreign cordon | Exit 1; named probe and marker match | Cleanup clears the node's pre-existing cordon: unchanged reuse fails the Bronzeward ownership requirement. |
| Suspended active Job | Exit 1; named probe and marker match | Reconciliation suspension leaves the existing Job enabled: it is not an execution cancellation mechanism. |

Run 1 stopped before any test: inherited GOROOT named an uninstalled Go 1.27.2.
The runner now removes that inherited override so the selected Go binary locates
its own installation. The failed launch is retained as an environment failure,
not counted as a test. The successful run used a working-tree fixture based on
Bronzeward 0439601; its exact script/probe hashes, rather than an invented committed
revision, identify those tested bytes.

The two compatibility probes assert Bronzeward requirements against unmodified
upstream code. Their expected failures are evidence against reuse, not defects
claimed against tuppr's own documented contract. The harness accepts a negative
case only if the named test fails with its specific marker; compiler errors,
timeouts, missing tests or unrelated failures do not count as confirmation.

The foreign-cordon probe begins with a node already cordoned by another actor,
calls the existing cordon helper, then its cleanup helper. The required outcome
is that the original cordon survives. The suspension probe creates an existing
active Job and a suspended TalosUpgrade, then reconciles it. It requires the Job
to be stopped at the Kubernetes-object level. Even satisfying that weaker check
would not prove that an already accepted remote request cannot act later.

## Boundary preservation and rejected integration

| Bronzeward boundary | What unchanged reuse would permit | This deliverable and later requirement |
| --- | --- | --- |
| Exact immutable approval | Mutable CR spec, node overrides, generation reset and automatic completion-cycle restart can change the target or repeat work. | No controller integration or runtime dependency is added. Later #237 must bind targets, phase policy and retry authority before dispatch; configuration changes require a new plan. |
| Configuration ownership | Full-config read/edit/reapply can conflict with approved artifacts or another writer; synchronization failure does not necessarily prevent tuppr completion. | Reject this write path. Preserve the existing sanitized ingress/compiler/artifact path and define an explicit owned transition, including read-version and conflict handling. The fixture does not test secret ingress. |
| Credentials | In-cluster access [requires Talos API access with os:admin](https://github.com/home-operations/tuppr/blob/377fbf03df05420ca833230233e67877e5f343d4/docs/requirements.md), and operation depends on the managed Kubernetes API. | No namespace grant, credential, provider change or cluster access is introduced. An operator alternative would need separately accepted custody, rotation, revocation, access loss and recovery policy. Fake clients cannot prove those properties. |
| Durable audit and uncertain outcomes | Mutable status can declare failure or completion from deadline/version evidence while an earlier request's effect remains uncertain. | Retain ER §4/§5: record each external mutation's intent and attempt, distinguish target response from transport result, and hold scope until old attempts are accounted for. No version/Ready observation, Job deletion, client exit or timeout supplies that proof by itself. |
| Coexistence and cordon ownership | [Coordination](https://github.com/home-operations/tuppr/blob/377fbf03df05420ca833230233e67877e5f343d4/internal/controller/coordination/coordinator.go) lists tuppr upgrade CRs, not Bronzeward operation scopes. Suspension leaves existing Jobs enabled; cleanup lacks cordon provenance. | Do not co-deploy as an executor on this evidence. Require a single mutation owner, operation-owned cordon facts, takeover/restore rules and accounting for any prior external work. A shared label or suspended reconciler is insufficient. |

The boundary preservation demonstrated by this changeset is deliberately narrow:
the existing application is unchanged, no upstream implementation enters its
dependency graph, and the research probes reject unsafe substitutions. This is
not a demonstrated safe integration. Required safety evidence remains necessary
before making that feasibility claim.

For example, after installation succeeds but drain fails, a future operation
must retain separate installation, cordon and partial-eviction facts. It cannot
return to an undifferentiated pending state and repeat installation automatically.
After a lost reboot response, even an observed Ready node cannot release scope
while the earlier request may still arrive. After a controller restart and Job
garbage collection, persisted phase deadlines remain useful, but timeout leaves
unaccounted work unresolved rather than granting a new mutation owner. These are
applications of ER and #166, not a new executor implemented by this report.

## Reuse alternatives and maintenance

1. **Patterns only — recommended.** Implement later contracts within Bronzeward's
   native operation model. Link the source inspiration and independently reproduce
   its relevant failure cases. Bronzeward owns implementation and regression tests;
   no automatic tracking of upstream main changes behaviour.
2. **Selected source extraction — not selected.** Even the small maintenance-window
   helper imports tuppr CRD types and go-cron. The drainer imports Kubernetes API
   and controller-runtime and lacks ownership semantics; the CEL checker also
   imports tuppr API/metrics. Extraction needs a stable input boundary and locally
   owned contracts and tests. Updating copied files would require an explicit
   maintainer, pinned provenance and review of changed semantics, not a dependency
   bump alone. The current evidence does not justify that cost.
3. **Library or vendoring — not selected.** Useful packages are under Go `internal/`,
   not a supported public library interface available to Bronzeward. A public
   upstream extraction or maintained local fork would be necessary. Vendoring the
   operator does not remove mutable-plan, credential, audit or remote-accounting
   mismatches and brings its dependency/update surface into Bronzeward.
4. **Operator deployment — rejected for this integration on current evidence.**
   Its reconciliation ownership and privileged in-cluster access are material
   architectural changes, not a smaller route to Bronzeward lifecycle support.
   No operator is deployed and no live coexistence experiment is claimed.

The upstream root [LICENSE](https://github.com/home-operations/tuppr/blob/377fbf03df05420ca833230233e67877e5f343d4/LICENSE)
contains AGPL-3.0; some source files retain other licence headers and provenance,
including the drain implementation. That is a provenance observation, not an
incorporation permission or legal conclusion. No source is copied or vendored
into Bronzeward runtime. Any future incorporation needs a specific file/dependency
review and owner decision; [#16](https://github.com/ginsys/bronzeward/issues/16)
remains independent and open. This investigation does not select a licence.

## Follow-ups and unresolved evidence

The existing [lifecycle specification #237](https://github.com/ginsys/bronzeward/issues/237)
covers immutable bindings, phase accounting, foreign cordons, partial outcomes,
health semantics and observer credentials. This report supplies concrete rejected
substitutions and test inputs for that work. The existing
[VM/hardware qualification #238](https://github.com/ginsys/bronzeward/issues/238)
owns real installation/drain/reboot order, older API transition, quorum/PDB effects,
late requests, takeover, restore and negative controls. These obligations are not
completed by fake-client tests.

[Talos implementation #239](https://github.com/ginsys/bronzeward/issues/239) and
[Kubernetes implementation #240](https://github.com/ginsys/bronzeward/issues/240)
remain separate later work. [Health-window validation #188](https://github.com/ginsys/bronzeward/issues/188)
stays open until a later operation class binds a health check; this research adds
none to the PoC. No duplicate implementation issue or new blocker list is created.
Native GitHub dependencies remain authoritative.

Remaining unproven areas include a complete interruption/retry matrix for a real
upgrade, remote attempt accounting, cross-controller races, real credential
custody/redaction, real PDB/quorum/volume-detach behaviour and upgrade transitions
through the older version boundary. The recommendation avoids adopting components
that would require those proofs; it does not waive them for future lifecycle work.
