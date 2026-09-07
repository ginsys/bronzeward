# Contributing

Work from a GitHub issue with an explicit objective, scope, exclusions, deliverables, acceptance criteria and verification method. Accept responsibility before assigning yourself. Refine incomplete work before implementation; an issue's existence is not implementation readiness.

## Authority

| Information | Authority |
| --- | --- |
| Work scope, acceptance criteria, ownership and completion | GitHub issue |
| Blocking relationships | Native GitHub dependencies |
| Delivery grouping and completion criteria | GitHub milestone |
| Architecture and product constraints | [Current main design](https://github.com/ginsys/bronzeward/blob/main/docs/design/Talos_Configuration_and_Machine_Management_Design.md) |
| Detailed PoC contracts | [Specification index and reserved contract locations](docs/spec/README.md), produced during [milestone 02](https://github.com/ginsys/bronzeward/milestone/2) |
| Reproducible experimental evidence | Repository research documents and prototypes |
| Historical discussion | [Historical report](docs/design/research/20260906-design-discussion-and-peer-review.md) |

Do not maintain parallel status checklists in repository documents or chat. Issue acceptance criteria belong in the issue; native dependencies alone define blockers. Body links explain design references and outcomes rather than duplicating a dependency checklist. Permanent decisions must not exist only in chat. Accepted design changes update the relevant authoritative document in the same change as their implementation.

The working design is not an implementation specification or proof of passing experiments. Keep observed evidence, proposed behavior and unsupported behavior distinct. The first PoC selects one database/provider profile after investigation. Do not assume every investigated option is supported.

## Delivery terminology

| Design terminology | GitHub delivery grouping |
| --- | --- |
| Phase 0: evidence before implementation contracts (§18.1) | [01 - Feasibility evidence](https://github.com/ginsys/bronzeward/milestone/1) |
| “milestone-1 specification” (§18.1) | [02 - PoC specification](https://github.com/ginsys/bronzeward/milestone/2) |
| Phase 1 / “first milestone”: configuration control (§18.2) | [03 - Working configuration-control PoC](https://github.com/ginsys/bronzeward/milestone/3) |

The [design's configuration-control scope](docs/design/Talos_Configuration_and_Machine_Management_Design.md#182-phase-1---configuration-control-on-an-existing-cluster) owns acceptance and exclusions. GitHub milestone numbers do not renumber the design phases.

## Triage and labels

Use the work-item form. Select investigation, decision, specification, implementation or validation; during triage apply exactly one corresponding `type/investigation`, `type/decision`, `type/specification`, `type/implementation` or `type/validation` label. A form dropdown does not dynamically apply a label.

During triage, require investigations to describe alternatives and the decision their evidence enables. That field is optional at submission so other work types can omit it; incomplete investigations need refinement before work starts. Outcome links are optional and describe enabled decisions or artifacts, never a second blocker list. Form-generated headings may use `###` while seeded bodies use `##`; section names and meaning are the contract, not heading depth.

The label set is managed from [ginsys/.github](https://github.com/ginsys/.github) (`standards/labels.json`); its daily audit reports drift and a manual apply deletes labels it does not know. Issue open/closed state is authoritative for completion. Use `status/in-progress` when accepted work is underway and `status/needs-review` when it awaits review; these two labels are mutually exclusive. Use `status/needs-refinement` while implementation detail or acceptance verification remains incomplete. It may coexist with either workflow label. Add one or more `area/` labels during triage for the design component or repository concern the work touches; a new `area/` value is created live to unblock triage and back-filled into the label registry in the same unit of work.

Refine or subdivide incomplete implementation and acceptance issues with concrete contracts and checks before removing refinement labels. Preserve native dependencies and acceptance coverage when subdividing. Stable `bronzeward-key` markers identify seeded issues and must not be changed or duplicated. Preserve human edits.

The [licence decision](https://github.com/ginsys/bronzeward/issues/16) is independent owner work in milestone 02 and does not block the PoC specification review. Completing a milestone still requires its own issue completion criteria; this independence does not mark the licence work done.

## Evidence and closure

Investigations compare alternatives and identify the decision their evidence enables. Record exact tool versions, synthetic inputs, reproduction commands, expected/observed outcomes, failure cases, limitations and recommendations in landed research documents and minimal prototypes. A negative finding can close an investigation; resulting decisions and remediation remain separately tracked. A review cannot declare a required safety property feasible without supporting evidence.

Implementation follows finalized contracts and includes meaningful verification of applicable success, rejection, interruption and recovery behavior. Update code and related documentation together. Closure requires every specified acceptance criterion, the required evidence and landed artifacts, assessed by the owner or designated reviewer. An open draft PR or a local passing check alone is not closure evidence.

The [PoC compatibility investigation](https://github.com/ginsys/bronzeward/issues/5) defers Upgrade/LifecycleClient execution testing; full design experiment E3 is not claimed complete. The [specification review](https://github.com/ginsys/bronzeward/issues/20) owns the disposition and related closure criteria.

## Change and review workflow

1. Prepare a scoped change on a feature branch; preserve unrelated work and keep the default branch unchanged.
2. Run the [documentation checks](#documentation-checks) and any checks required by the issue, then review the actual diff against the current design/specification. Record exact checks and limitations.
3. Open a draft PR stating the problem, scope, linked issues, validation evidence and outstanding limitations. Include related documentation in the same PR.
4. Have the owner or designated reviewer assess correctness, scope, safety, evidence and documentation. Record findings and resolve them or explicitly record an accepted disposition.
5. Complete required evidence before seeking readiness or merge authorization. The owner may merge their own PR after review. Agents require explicit authorization to change PR readiness, merge or change branch protection.

Every PR runs the `CI` workflow (documentation checks, `actionlint`, conventional-commit subjects, action pins; aggregated as the `checks` context), a Codex review and an advisory Claude review (`PR Review`). Review threads must be resolved before merge. Merges are rebase-only through the merge queue: after review, the owner runs `gh pr merge --auto` and the queue lands the PR once `checks` and `PR Review` report on the merged result. Do not claim CI success without an actual run result. Review and merge are separate actions.

## Documentation checks

With Python 3.9 or newer and PyYAML 6 installed in your Python environment, run from the repository root:

```sh
python3 scripts/verify-docs.py
git diff --check
git diff --cached --check
```

With [mise](https://mise.jdx.dev/) installed, `mise run verify` runs the same documentation check plus `actionlint`, `shellcheck` and the commit-lint fixture tests, which is what the `CI` workflow runs.

The verifier reads repository files as UTF-8 and reports file locations relative to the repository root. It checks root guidance and Markdown under `docs/spec/`: inline relative links and anchors, local equivalents of this repository's `blob/main` document links, balanced fenced blocks and trailing whitespace. It also checks issue-form YAML, field names/types/requiredness, disabled blank issues and the CLAUDE delegation.

Link checks cover inline links without titles or spaces in their destinations. Titled and reference-style links are skipped and need manual review. Trailing whitespace is rejected, including Markdown's two-space hard line breaks; use a paragraph break instead. The verifier does not fetch external links, verify live tracker state, fully lint Markdown or validate design semantics. Review changed external references and the actual diff separately; for a committed PR, also run `git diff --check <base-commit>...HEAD` using its actual base commit.
