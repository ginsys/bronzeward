# Bronzeward agent instructions

## Start from the work item

- Read the issue, its native GitHub dependencies and milestone, the [current main design](https://github.com/ginsys/bronzeward/blob/main/docs/design/Talos_Configuration_and_Machine_Management_Design.md), the relevant specification and existing code before changing anything.
- The issue owns scope, acceptance criteria, ownership and completion. Native dependencies alone own blocking; milestones own delivery grouping and completion criteria. The current main design owns architecture/product constraints; the milestone-02 specification owns detailed PoC contracts. Research documents/prototypes own reproducible evidence; the [historical report](docs/design/research/20260906-design-discussion-and-peer-review.md) supplies historical context.
- Leave issues unassigned until someone accepts responsibility. Do not implement an issue carrying `status::needs-refinement`; the specification review must refine or subdivide it with concrete contracts, checks and preserved acceptance coverage before removing that label.
- Preserve scope and unrelated changes. Do not infer database/provider support, policy, licence choice or production readiness from an investigation or design proposal. The first PoC uses one profile selected after evidence.

## Record evidence and decisions

- Keep permanent decisions in the appropriate authoritative design/specification document, with rationale and alternatives. Update that document when a design change is accepted. Do not leave decisions only in chat or create parallel status checklists.
- Put work acceptance criteria in the issue and blocking relationships in native dependencies. Issue-body cross-references explain design and enabled outcomes; they must not become a second blocker list.
- Investigations record pinned versions, synthetic inputs, reproduction commands, expected/observed outcomes, failure cases, alternatives, limitations and the decision enabled. Land reports and minimal prototypes before closure.
- Negative findings can close investigations, but resulting decisions/remediation remain separately tracked. Required safety properties need evidence before a feasibility claim.
- Explicitly preserve the Upgrade/LifecycleClient execution-testing deferral in the PoC compatibility and feasibility reviews. It is an unmet part of full E3. The specification review must record its disposition and reconcile affected design wording before closure.
- Implementation must follow refined contracts and verify applicable success, rejection, interruption and recovery behavior. Never use real credentials in fixtures or ordinary evidence; respect the design's secret-ingress boundary.

## Track and review honestly

- Use exactly one work type label: `type/investigation`, `type/decision`, `type/specification`, `type/implementation` or `type/validation`. Triage must apply the label matching the form dropdown; the dropdown does not apply it automatically. Preserve existing default labels.
- Open/closed state owns completion. `status::in-progress` and `status::needs-review` are mutually exclusive; `status::needs-refinement` describes incomplete detail and may coexist with either. Preserve stable `bronzeward-key` markers; never duplicate them or overwrite human edits during setup reruns.
- Prepare changes on a feature branch. Run appropriate checks and inspect the actual diff. For documentation, validate links, Markdown structure, issue-form YAML and `git diff --check`; use project-defined tasks when available.
- Open a draft PR with the problem, scope, issue links, validation evidence and outstanding limitations. Keep code and related documentation in the same PR. The owner or designated reviewer assesses it; resolve findings and complete evidence before seeking readiness/merge authorization.
- Do not claim tests or CI passed unless they ran successfully. This tracking setup establishes no CI pipeline or review automation. Do not change branch protection, undraft or merge the setup PR.
- Close issues only after specified evidence and artifacts have landed and acceptance has been assessed. Keep tracking current with real evidence; creating issues does not complete them.

## Delivery limits

Tracking covers feasibility, specification and the working configuration-control PoC only. The independent owner licence decision belongs to milestone 02 but does not block specification review. Upgrade/lifecycle execution, new-machine enrolment, reset/reuse, bootstrap, remote transports and managed-cluster etcd recovery remain outside PoC scope.

PoC acceptance requires adoption without mutation, encrypted publication, exact approval, direct safe no-reboot worker apply/verification, desired/applied/observed state and an operation timeline, drift handling, interrupted execution and explicit restoration recovery. Unresolved old execution blocks conflicting work. A happy-path demo is insufficient and does not establish production readiness.
