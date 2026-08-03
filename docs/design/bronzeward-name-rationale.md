# Bronzeward: Project Name and Rationale

## Decision

The project will be called **Bronzeward**.

> **Bronzeward**  
> *An operator-owned control plane for Talos configuration and machine lifecycle.*

The name connects directly to the mythology behind **Talos**, while remaining independent enough not to suggest that the project is an official Sidero Labs product.

---

## The Talos mythology

In Greek mythology, **Talos** was a bronze guardian associated with the island of Crete. Depending on the version of the myth, he was either created by the divine smith **Hephaestus**, given to Europa or King Minos, or described as the last survivor of a mythical bronze race.

Across the different traditions, his purpose remains broadly consistent:

- He was a **purpose-built guardian**.
- He continuously patrolled the coast of Crete.
- He protected the island against unauthorized arrivals.
- In one tradition, he carried the laws of Minos on bronze tablets and travelled through Crete to ensure that those laws were observed.
- His apparently invulnerable body depended on a single vein carrying **ichor**, the life-fluid of the gods, sealed at the ankle by a bronze pin.

This makes Talos more than an early mythological robot. He represents a combination of:

- automation;
- continuous observation;
- enforcement of declared rules;
- protection of a defined domain;
- strong identity and purpose;
- and dependence on carefully protected trust material.

Those ideas map unusually well to Talos Linux: a narrowly designed operating system that runs without a conventional shell or SSH service, is managed through an API, and continuously converges toward declarative machine configuration.

---

## What Bronzeward means

The name combines two words:

### Bronze

**Bronze** refers directly to Talos, the bronze guardian.

It connects Bronzeward to the Talos ecosystem without reusing the Talos name itself. This avoids implying that Bronzeward is an official Sidero Labs component while still giving the project a meaningful and recognizable origin story.

### Ward

**Ward** has two useful meanings:

1. **To ward** means to guard or protect.
2. **A ward** is something placed under another party's care or stewardship.

Both meanings fit the project.

Bronzeward protects the configuration, credentials, identity, and lifecycle of Talos machines. At the same time, Talos machines are entrusted to Bronzeward for controlled enrolment, assignment, configuration, reconciliation, reset, reuse, and retirement.

The combined meaning is therefore:

> **The system responsible for guarding and stewarding the bronze guardians.**

---

## Architectural meaning

Talos Linux is the guardian that runs on each machine. Bronzeward operates one level above it.

```text
Bronzeward
    manages identities, secrets, configuration releases,
    assignments, rollouts, and lifecycle

Talos
    validates and applies native machine configuration,
    then continuously enforces that configuration

Kubernetes
    runs the workloads on the configured Talos machines
```

The mythology can be mapped directly to the design:

| Mythological element | Bronzeward interpretation |
|---|---|
| Talos, the bronze guardian | A Talos Linux machine |
| Crete | The cluster or operational domain |
| Talos' patrol | Continuous observation and reconciliation |
| The bronze tablets | Published Talos configuration releases |
| The laws of Minos | Declared desired state |
| Assignment to guard Crete | Assigning a machine to a cluster and role |
| Ichor | Cluster secrets, PKI, and machine identity |
| The bronze pin | Critical trust and recovery dependencies |
| Returning from duty | Resetting a node to an available maintenance pool |

This mapping is not merely decorative. It describes the actual product:

- Native Talos configuration is composed from reusable fragments and profiles.
- Configurations are published as immutable, auditable releases.
- Secrets and PKI remain under operator control, stored in OpenBao.
- Machines are enrolled, approved, assigned, configured, observed, reset, and reused.
- Desired, applied, and observed state are tracked separately.
- Drift and failed rollouts are detected rather than hidden.
- Direct Talos access and break-glass recovery remain possible.
- Infrastructure creation remains outside the project's responsibility.

---

## Why Bronzeward fits better than the alternatives

### Talos Steward

**Talos Steward** describes the role very accurately, but it could appear to be an official Talos or Sidero Labs product. Bronzeward preserves the stewardship idea without creating that ambiguity.

### Hephaestus or Forge

Hephaestus is the maker of Talos and therefore suggests image building, machine creation, or infrastructure provisioning. Bronzeward does compile configuration, but it does not create VMs, operate BMCs, manage PXE, or manufacture Talos itself.

### Minos

Minos represents authority and law, which matches configuration ownership and assignment. However, the name carries broader mythological baggage and suggests centralized rule more than transparent, operator-owned stewardship.

### Nomos

Nomos, meaning law or established rule, would suit a pure configuration-policy system. Bronzeward covers the wider machine lifecycle as well: enrolment, assignment, maintenance mode, reset, reuse, recovery, and retirement.

### Ichor

Ichor is an excellent metaphor for secrets and PKI, but too narrow for the complete platform. It is better suited as an internal name for the secret-management subsystem.

### Dockmaster

Dockmaster is a strong operational metaphor and fits a maritime project family, but it lacks the direct relationship with Talos mythology. Bronzeward gives the project a more distinctive independent identity.

---

## Product positioning

Bronzeward should be described as:

> **An open, self-hosted, operator-owned control plane for Talos configuration and machine lifecycle.**

A longer description:

> Bronzeward manages already-available Talos machines from maintenance mode through active cluster membership and back again. It uses native Talos configuration and APIs, PostgreSQL for desired state and operational history, OpenBao for secrets and PKI, and transport options suited to both internal networks and remote sites. It deliberately avoids CAPI, mandatory Git workflows, infrastructure provisioning, and proprietary configuration abstractions.

Bronzeward is not intended to replace:

- hypervisor or cloud APIs;
- bare-metal provisioning systems;
- PXE, Redfish, or BMC tooling;
- Kubernetes GitOps controllers;
- or Talos' own configuration and execution model.

Its purpose is to manage the lifecycle around Talos while keeping Talos itself authoritative for machine configuration.

---

## Recommended tagline

Primary tagline:

> **Operator-owned Talos configuration and machine lifecycle management.**

Alternative concise taglines:

- **Guard the machines. Keep the configuration.**
- **Native Talos management under your control.**
- **Configuration, identity, and lifecycle for Talos Linux.**
- **A configuration-centric control plane for Talos Linux.**

The first is the most memorable. The last is the most technically precise.

---

## Naming conventions

The public product name should remain simply:

```text
Bronzeward
```

Suggested repository and binary names:

```text
bronzeward
bronzeward-api
bronzeward-controller
bronzeward-ui
bronzeward-connector
```

The public API should use ordinary technical resource names:

```text
/api/v1/machines
/api/v1/clusters
/api/v1/config-fragments
/api/v1/profiles
/api/v1/releases
/api/v1/operations
```

Mythological names may be used sparingly for internal components, but should not make the API difficult to understand.

---

## A coherent mythological component family

A restrained internal naming scheme could look like this:

```text
Bronzeward
├── Forge
│   configuration generation and compilation
│
├── Tablets
│   fragments, profiles, revisions, and releases
│
├── Ichor
│   OpenBao, PKI, and secret generations
│
├── Circuit
│   reconciliation, rollout, and durable operations
│
├── Watch
│   machine observation and connectivity
│
└── Registry
    machine identity, enrolment, and assignment
```

The names have a consistent relationship to the Talos mythology while still describing recognizable technical responsibilities:

| Component | Mythological connection | Technical responsibility |
|---|---|---|
| **Forge** | Hephaestus' workshop and the creation of the bronze guardian | Combines upstream Talos generation, native configuration fragments, cluster identity, and secret inputs into complete machine configurations |
| **Tablets** | The bronze tablets carrying the laws enforced by Talos | Stores and manages fragments, profiles, revisions, immutable releases, structural diffs, and provenance |
| **Ichor** | The life-fluid that gave Talos identity and power | Integrates with OpenBao and manages PKI, Talos secret generations, credentials, and rotation workflows |
| **Circuit** | Talos' repeated patrol around Crete | Runs reconciliation, rollout, retry, approval, reset, upgrade, and other durable operation workflows |
| **Watch** | The guardian observing the coastline | Maintains machine connectivity, observations, health, drift detection, logs, and events |
| **Registry** | The record of which guardian is entrusted with which territory | Manages stable machine identity, enrolment, approval, cluster assignment, role, and lifecycle state |

This family is coherent without turning the product into a mythology puzzle. The internal names can make architecture discussions memorable, but they should remain secondary to plain technical terminology.

The public API should therefore stay deliberately boring and self-explanatory:

```http
/api/v1/machines
/api/v1/clusters
/api/v1/config-fragments
/api/v1/profiles
/api/v1/releases
/api/v1/operations
```

Public objects should be named `Machine`, `Cluster`, `ConfigFragment`, `Profile`, `Release`, and `Operation`—not `Guardian`, `Tablet`, or `IchorVessel`. Mythology is useful branding; it is a poor substitute for an understandable API.

These component names should also remain optional implementation boundaries rather than prematurely forcing the codebase into separate deployable services. For example, `Forge` and `Tablets` may initially be packages inside the Bronzeward controller rather than independent processes. The names describe responsibilities, not a mandatory microservice diagram.

---

## Final rationale

Bronzeward works because it captures the relationship between the platform and Talos precisely:

- Talos is the bronze guardian.
- Talos receives and enforces declarative law.
- Bronzeward manages the guardians, their identities, their assignments, and the laws they receive.
- Bronzeward protects the trust material that gives those machines authority.
- Bronzeward also avoids reproducing the weakness in the myth: an impressive system should not depend on one undocumented bronze pin.

The name is distinctive, technically relevant, easy to explain, and broad enough to cover the full design without implying infrastructure provisioning or official upstream affiliation.
