# Bronzeward agent instructions

## Start from the work item

- Read the issue, its native GitHub dependencies and milestone, the [current main design](https://github.com/ginsys/bronzeward/blob/main/docs/design/Talos_Configuration_and_Machine_Management_Design.md), the relevant contracts in the [specification index](docs/spec/README.md) and existing code before changing anything.
- The issue owns scope, acceptance criteria, ownership and completion. Native dependencies alone own blocking; milestones own delivery grouping and completion criteria. The current main design owns architecture/product constraints; the milestone-02 specification owns detailed PoC contracts. Research documents/prototypes own reproducible evidence; the [historical report](docs/design/research/20260906-design-discussion-and-peer-review.md) supplies historical context.
- The design's “first milestone” means GitHub milestone 03; its “milestone-1 specification” means GitHub milestone 02. See [delivery terminology](CONTRIBUTING.md#delivery-terminology).
- Leave issues unassigned until someone accepts responsibility. Do not implement an issue carrying `status/needs-refinement`; the specification review must refine or subdivide it with concrete contracts, checks and preserved acceptance coverage before removing that label.
- Preserve scope and unrelated changes. Do not infer database/provider support, policy, licence choice or production readiness from an investigation or design proposal. The first PoC uses one profile selected after evidence.

## Record evidence and decisions

- Keep permanent decisions in the appropriate authoritative design/specification document, with rationale and alternatives. Update that document when a design change is accepted. Do not leave decisions only in chat or create parallel status checklists.
- Put work acceptance criteria in the issue and blocking relationships in native dependencies. Issue-body cross-references explain design and enabled outcomes; they must not become a second blocker list.
- Investigations record pinned versions, synthetic inputs, reproduction commands, expected/observed outcomes, failure cases, alternatives, limitations and the decision enabled. Land reports and minimal prototypes before closure.
- Negative findings can close investigations, but resulting decisions/remediation remain separately tracked. Required safety properties need evidence before a feasibility claim.
- Preserve documented evidence gaps and deferrals; follow the [evidence and closure rules](CONTRIBUTING.md#evidence-and-closure) and the issue's acceptance criteria without copying those criteria into repository guidance.
- Implementation must follow refined contracts and verify applicable success, rejection, interruption and recovery behavior. Never use real credentials in fixtures or ordinary evidence; respect the design's secret-ingress boundary.

## Track and review honestly

- Use exactly one work type label: `type/investigation`, `type/decision`, `type/specification`, `type/implementation` or `type/validation`. Triage must apply the label matching the form dropdown and require alternatives/decision-enabled evidence for investigations; the form does neither conditionally. Add `area/` labels in triage; labels are managed from ginsys/.github and a value not in its registry is deleted on the next apply, so back-fill any new `area/` there in the same unit of work.
- Open/closed state owns completion. `status/in-progress` and `status/needs-review` are mutually exclusive; `status/needs-refinement` describes incomplete detail and may coexist with either. Preserve stable `bronzeward-key` markers; never duplicate them or overwrite human edits.
- Prepare changes on a feature branch. Run `python3 scripts/verify-docs.py`, `git diff --check` and `git diff --cached --check`, plus issue-specific checks, and inspect the actual diff. [Documentation checks](CONTRIBUTING.md#documentation-checks) define prerequisites and coverage limits.
- Open a draft PR with the problem, scope, issue links, validation evidence and outstanding limitations. Keep code and related documentation in the same PR. The owner or designated reviewer assesses it; resolve findings and complete evidence before seeking readiness/merge authorization.
- Do not claim tests or CI passed unless they ran successfully. Every PR runs the `CI` workflow (`checks` context), a Codex review and an advisory Claude review; resolve every review thread. Merges are rebase-only through the merge queue (`gh pr merge --auto`, run by the owner after review). Changing PR readiness, merging or changing branch protection requires explicit owner authorization; the owner may merge their own PR after review.
- Close issues only after specified evidence and artifacts have landed and acceptance has been assessed. Keep tracking current with real evidence; creating issues does not complete them.

## Delivery limits

Tracking covers feasibility, specification and the working configuration-control PoC only. The independent owner licence decision belongs to milestone 02 but does not block specification review.

Use [design §18.2](docs/design/Talos_Configuration_and_Machine_Management_Design.md#182-phase-1---configuration-control-on-an-existing-cluster) for PoC acceptance and exclusions and the [working PoC milestone](https://github.com/ginsys/bronzeward/milestone/3) for delivery completion criteria.
