# Design review: revision 0.4 to 0.5

Date: 6 September 2026

Status: Documentation reconciliation and remaining evidence; not an implementation sign-off.

Baseline: `2cadaaee8b007d8c6a896669d033b81c409cfaa2`, main design revision 0.4 of 3 August 2026. Target: [main design revision 0.5](Talos_Configuration_and_Machine_Management_Design.md). The [historical discussion and peer review](research/20260906-design-discussion-and-peer-review.md) preserves decisions, criticism, withdrawals and the final qualifications.

This document replaces `Design_Review_v0.2_to_v0.3.md`. The earlier review remains available in Git history; it described an older transition and is no longer the current design review.

## Assessment

The project remains a working architecture concept with no application implementation. The direction is coherent enough to specify and prototype **configuration control on an existing cluster**. Implementation contracts still depend on the experiments in design §18.1, especially secret ingress, key recovery, structural references and dispatch ownership. Review agreement does not establish passing experiments, backend support or production readiness.

Revision 0.5 preserves native Talos configuration, fixed composition layers, immutable publication, direct access, the Go implementation decision and the eventual lifecycle scope. It removes unsupported guarantees and reconciles accepted owner decisions with the earlier workflow descriptions.

## What changed and why

| Area | Revision 0.4 position or gap | Revision 0.5 disposition |
|---|---|---|
| Secret ingress | Original YAML/parsed config stored; drift adoption wrote observed effective config into a draft | §§6.9, 7.1 and 9.1 require known/marked secrets to be extracted before ordinary persistence, including errors and interrupted staging; §12.4 applies the same rule to drift adoption. |
| Secret identification | Bundle-focused model | Talos bundle extraction automated by default; reliable schema assistance for other secrets; operator marking remains authoritative and unidentified secrets remain the operator's responsibility. |
| References | Declaration and execution details undefined in the discussion | §6.9 fixes whole-value structural semantics and named validation stages. Grammar, declaration, timing and encoding enum remain open pending typed-composition evidence; no custom merge engine. |
| Retention | Immutable Talos generations; insufficient coverage of other secrets and artifact keys | §§7.4–7.6 extend dependency retention and cleanup constraints to all referenced secret/crypto versions and retained recovery sets; immutable artifact bytes alone are insufficient. |
| Dependency checking | No explicit metadata-only contract | §7.6 distinguishes retained, blocked, lost and unknown from evidence, alerts persistent unknown and separates retention checks from use-time readability/decryption/authorization. |
| Provider boundary | OpenBao KV/Transit tied to the architecture | OpenBao remains primary; simpler local age/SOPS alternatives are investigations. Deployment policies cannot enforce retention against external administrators. |
| Database | PostgreSQL/JSONB and CloudNativePG preferred throughout | §§7.2 and 14.2 introduce the SQLite investigation and semantic portability criteria; MySQL/MariaDB are conditional on negligible added cost. Database/vault server operation stays external. |
| Publication | PostgreSQL/OpenBao sequence and loose unreferenced-generation cleanup | §7.4 pins inputs/dependencies, encrypts before atomic publication, checks source conflicts and requires reference-aware cleanup. Publication has no dispatch authority. |
| Renderer | One pinned talosctl binary per contract implied | §6.5 mirrors native generation capabilities, separates support policy from compatibility, preserves exact artifacts and tests RPC/version combinations. No indefinite historical toolchain requirement. |
| Approval | Role permissions could imply vault-enforced release approval | §§12.7 and 13.2 state the trusted application boundary, immutable plan binding and required dispatch commitment semantics. Self/multi-party policy remains open. |
| Interrupted execution | Locks, idempotency and resumption described too broadly | §12.5 classifies evidence per operation, mode, assignment and intervening state. Stale A-after-B apply is unsafe; database fencing is not remote fencing. Unresolved outcomes stop conflicting work. |
| Apply modes | `try` suggested without full completion semantics | §12.2 identifies the automatic revert timer and distinguishes staged acceptance from running state; no separate confirmation RPC assumed. |
| Restoration | Backup-set inventory without an activation boundary | §14.6 requires explicit recovery mode, current observations, dependency/use checks, scoped readiness and explicit release from recovery before mutations resume. |
| First milestone | Core configuration phase coexisted with full reuse-loop acceptance | §18.2 selects existing-cluster configuration control; §18.5 preserves the full round trip as later lifecycle acceptance. Etcd snapshots remain later version 1 scope. |
| Operation catalogue | “Required lock” mixed coordination and approval; RotateSecrets mandated multiple approvers | Appendix B has separate coordination and authorization columns; approval cardinality is not preselected. |
| Diagrams | Three known stale/illegible PNG presentations | Figures 2–4 use maintainable Mermaid diagrams with encryption before publication, provider-neutral roles and deployment-defined remote endpoints. Old image assets remain historical. |

## Reconciliation coverage

Line numbers below are at the baseline HEAD, not the revised document. Section references identify the replacement contract.

| Baseline passage | Current location |
|---|---|
| Main design `:353`, original TEXT/JSONB storage | §§7.1–7.2 |
| `:357`, immutable secret generations | §§7.3, 7.5–7.6 |
| `:381`, garbage collection | §7.4 |
| `:717`, drift adoption | §12.4 with §7.1 |
| `:275`, renderer pinning | §6.5 |
| `:695`, planning and apply modes | §§6.9, 12.2, 12.7 |
| `:725`, durable operations | §12.5 |
| `:753`, decrypting published artifact contexts | §§12.7, 13.2 |
| `:797`, recovery set | §§14.4, 14.6 |
| `:878`, PostgreSQL baseline | §§7.2, 14.2, 17.1 |
| `:960`, first phase | §§18.1–18.2 |
| `:1028`, full-round-trip acceptance | §18.5, preserved as later lifecycle acceptance |
| `:1312`, RotateSecrets multi-party approval | Appendix B; §12.7 |

C1 is **requirement reconciled, implementation untested**: there is no implemented leak to repair, but the old workflow would permit one. C2 is **retention contract extended, provider layout and enforcement evidence pending**: the discussion did not already choose a mutable-path scheme for logical references.

## Open choices and planned evidence

The owner has decided the product boundaries; the remaining choices must not be hidden in examples or silently settled by an implementation shortcut.

| Choice | Evidence required |
|---|---|
| Reference grammar/declaration, resolution timing and encoding enum | E2: non-string fields, overrides, embedded serialization, sensitivity paths and upstream merge parity. Early resolution may record a source-provenance superset; distinguish it from effective artifact dependencies. |
| Secret layout, retention windows and unknown-alert interval | E5: metadata-only classifications, retention limits, cleanup and recovery-set dependencies; no metadata green treated as dispatch permission. |
| Local encryption, unlock/key custody and provider migration | E5: complete capability inventory, loss/restore scenarios and actual usability under the calling identity. Presence of an age file is insufficient. |
| SQLite support and possible additional SQL backends | E4: atomicity, revisions, concurrency, ownership, migrations and recovery; abstraction alone does not establish support. |
| Renderer implementation and tested compatibility matrix | E3: generation, validation and relevant RPC/version combinations, separately from upstream support policy. |
| Approval policy and safe dispatch/ownership mechanism | E4: revocation around a specified commitment boundary, stale executors, uncertain sends and conflicting revisions. Mechanism selection must follow the per-operation safety property. |
| Exact retention/product defaults and later lifecycle contracts | Owner policy plus E1–E6 results; existing open licence, authentication and deployment questions remain in main §§16 and 19. |

E1 (plaintext ingress sweep) and E5 (encryption/key recovery) share early priority. E6 is the integrated existing-cluster acceptance slice after E2–E4 establish feasible mechanisms. These experiment definitions are visible in the main design; no unseen reviewer estimates or experiment results are adopted.

## Verification and limits

This revision is documentation-only. Validation covers Markdown structure, local links, reconciliation coverage and searches for stale claims; it does not exercise Talos, OpenBao, SQLite or an application runtime. No cluster was changed. The historical record intentionally retains superseded wording with subsequent corrections rather than rewriting the discussion as if the final position had been known from the start.

The main reference appendix identifies the primary sources consulted for the September corrections. The Omni comparison and older technical baseline remain explicitly dated 3 August; they were not refreshed into a current product/pricing/licensing assessment in this revision. Refresh the build-gate evidence and run the planned experiments before treating the design as an implementation specification.
