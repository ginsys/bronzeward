# Contributing

Work from a GitHub issue with an explicit objective, scope, exclusions, deliverables, acceptance criteria and verification method. Accept responsibility before assigning yourself. Refine incomplete work before implementation; an issue's existence is not implementation readiness.

## Authority

| Information | Authority |
| --- | --- |
| Work scope, acceptance criteria, ownership and completion | GitHub issue |
| Blocking relationships | Native GitHub dependencies |
| Delivery grouping and completion criteria | GitHub milestone |
| Architecture and product constraints | [Current main design](https://github.com/ginsys/bronzeward/blob/main/docs/design/Talos_Configuration_and_Machine_Management_Design.md) |
| Detailed PoC contracts | Specification produced during [milestone 02](https://github.com/ginsys/bronzeward/milestone/2) |
| Reproducible experimental evidence | Repository research documents and prototypes |
| Historical discussion | [Historical report](docs/design/research/20260906-design-discussion-and-peer-review.md) |

Do not maintain parallel status checklists in repository documents or chat. Issue acceptance criteria belong in the issue; native dependencies alone define blockers. Body links explain design references and outcomes rather than duplicating a dependency checklist. Permanent decisions must not exist only in chat. Accepted design changes update the relevant authoritative document in the same change as their implementation.

The working design is not an implementation specification or proof of passing experiments. Keep observed evidence, proposed behavior and unsupported behavior distinct. The first PoC selects one database/provider profile after investigation. Do not assume every investigated option is supported.

## Triage and labels

Use the work-item form. Select investigation, decision, specification, implementation or validation; during triage apply exactly one corresponding `type/investigation`, `type/decision`, `type/specification`, `type/implementation` or `type/validation` label. A form dropdown does not dynamically apply a label.

Keep the existing GitHub default labels. Issue open/closed state is authoritative for completion. Use `status::in-progress` when accepted work is underway and `status::needs-review` when it awaits review; these two labels are mutually exclusive. Use `status::needs-refinement` while implementation detail or acceptance verification remains incomplete. It may coexist with either workflow label.

All initial PoC implementation and acceptance issues need refinement. The [specification review](https://github.com/ginsys/bronzeward/issues/20) must refine or subdivide them, establish concrete contracts/checks and preserve acceptance coverage before removing refinement labels. Preserve native dependencies when subdividing. Stable `bronzeward-key` markers identify seeded issues and must not be changed or duplicated. A `bronzeward-seed` marker records the seeded body hash for setup recovery; human edits are allowed and must never be silently overwritten by a seeding rerun.

The [licence decision](https://github.com/ginsys/bronzeward/issues/16) is independent owner work in milestone 02 and does not block the PoC specification review. Completing a milestone still requires its own issue completion criteria; this independence does not mark the licence work done.

## Evidence and closure

Investigations compare alternatives and identify the decision their evidence enables. Record exact tool versions, synthetic inputs, reproduction commands, expected/observed outcomes, failure cases, limitations and recommendations in landed research documents and minimal prototypes. A negative finding can close an investigation; resulting decisions and remediation remain separately tracked. A review cannot declare a required safety property feasible without supporting evidence.

Implementation follows finalized contracts and includes meaningful verification of applicable success, rejection, interruption and recovery behavior. Update code and related documentation together. Closure requires every specified acceptance criterion, the required evidence and landed artifacts, assessed by the owner or designated reviewer. An open draft PR or a local passing check alone is not closure evidence.

The [PoC compatibility investigation](https://github.com/ginsys/bronzeward/issues/5) explicitly defers Upgrade/LifecycleClient execution testing. The [feasibility review](https://github.com/ginsys/bronzeward/issues/12) must retain that limitation; the [specification review](https://github.com/ginsys/bronzeward/issues/20) must record its disposition and reconcile affected design wording before closure. Do not claim the full design E3 experiment passed.

## Change and review workflow

1. Prepare a scoped change on a feature branch; preserve unrelated work and keep the default branch unchanged.
2. Run appropriate checks and review the actual diff against the issue and current design/specification. For documentation changes, check links, Markdown structure, issue-form YAML and `git diff --check`. Record exact checks and limitations. Use project-defined tasks when available.
3. Open a draft PR stating the problem, scope, linked issues, validation evidence and outstanding limitations. Include related documentation in the same PR.
4. Have the owner or designated reviewer assess correctness, scope, safety, evidence and documentation. Record findings and resolve them or explicitly record an accepted disposition.
5. Complete required evidence before seeking readiness or merge authorization. Do not merge your own PR or change branch protection as part of routine work.

No review automation or CI pipeline is established by this tracking setup. Do not claim CI success without an actual pipeline result. The setup PR remains draft for owner review; review and merge are separate actions.

## PoC acceptance boundary

The [working PoC milestone](https://github.com/ginsys/bronzeward/milestone/3) requires adoption without mutation, native editing and encrypted publication, exact plan approval, direct safe no-reboot worker apply/verification, separate desired/applied/observed state and a usable operation timeline. It also requires drift freeze/sanitized adoption/approved revert, interrupted-operation recovery and explicit restoration recovery that blocks unresolved/conflicting mutations. A happy-path demo alone is insufficient.

New-machine enrolment, reset/reuse, bootstrap, upgrades, remote transports and managed-cluster etcd recovery remain later work. The PoC is not a production-readiness claim.
