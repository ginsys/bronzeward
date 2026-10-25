# Bronzeward

Bronzeward is a proposed self-hosted Talos configuration and machine management platform. It aims to give operators custody of native configuration and secrets, reusable fragments and profiles, immutable publication, explicit approval, provenance and controlled drift through direct Talos access.

The project's proof of concept is under implementation. The [operator guide](docs/operator/README.md) shows how to build and start it and walks one cluster from adoption to drift handling on the disposable fixture. The [current design](docs/design/Talos_Configuration_and_Machine_Management_Design.md) describes intended behavior; it is not evidence of implemented guarantees or passing experiments. The [historical discussion](docs/design/research/20260906-design-discussion-and-peer-review.md) preserves context, and the [design transition review](docs/design/Design_Review_v0.4_to_v0.5.md) records the reconciliation.

The design, specification, research reports and experiment READMEs are also published as a browsable site at <https://ginsys.github.io/bronzeward/>, rebuilt from `main` (see [Documentation site](CONTRIBUTING.md#documentation-site)).

Tracking covers a working configuration-control proof of concept using one database/provider profile selected after investigation, plus operator clients for that same scope:

1. [01 - Feasibility evidence](https://github.com/ginsys/bronzeward/milestone/1): reproducible experiments and reviewed findings.
2. [02 - PoC specification](https://github.com/ginsys/bronzeward/milestone/2): profile and policy decisions, detailed contracts and implementation refinement.
3. [03 - Working configuration-control PoC](https://github.com/ginsys/bronzeward/milestone/3): adoption, publication, approval, safe worker apply, drift handling and interruption/restoration recovery.
4. [04 - Operator experience](https://github.com/ginsys/bronzeward/milestone/4): a CLI and web UI over the existing API, specified in the [operator experience specification](docs/spec/operator-experience.md) and the [client contracts](docs/spec/client-contracts.md), and accepted for implementation by the owner on 25 October 2026.

Use the milestones for priorities and completion criteria, and native GitHub dependencies for blocking relationships.

The [design's configuration-control scope](docs/design/Talos_Configuration_and_Machine_Management_Design.md#182-phase-1---configuration-control-on-an-existing-cluster) defines PoC acceptance and exclusions. See the [specification index](docs/spec/README.md) for contract locations and [delivery terminology](CONTRIBUTING.md#delivery-terminology) for the mapping between design phases and GitHub milestones.

See [CONTRIBUTING.md](CONTRIBUTING.md) for ownership, evidence and review rules and [AGENTS.md](AGENTS.md) for agent instructions. Tracking creation does not complete any tracked work.
