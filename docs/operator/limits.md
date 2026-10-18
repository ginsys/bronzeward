# Limits of the proof of concept

The PoC shows configuration control on an existing cluster, within the limits below. It makes no
release or production-readiness claim. The authoritative lists are the acceptance plan's
[§8 Not covered](../spec/acceptance-plan.md#8-not-covered-by-the-poc) and
[§9 Limits](../spec/acceptance-plan.md#9-limits).

## The profile and versions

One profile, selected in [design §7.7](../design/Talos_Configuration_and_Machine_Management_Design.md):
PostgreSQL, and OpenBao KV v2 with Transit, both run by you, each as a single instance. No other
database or provider is supported.

The fixture pins, in [`fixtures/versions.env`](../../fixtures/versions.env):

| Component | Version |
|---|---|
| Talos | v1.13.6 |
| Kubernetes | 1.36.2 |
| OpenBao | 2.6.1, by digest |
| PostgreSQL | `postgres:17-alpine`, by digest |

Its cluster is one control plane and one worker, in containers: no reboot, no disks, no installer.
Results hold for that topology and that Talos version only.

## Not covered

- Enrolling new machines, resetting or reusing them, and bootstrapping a cluster.
- Upgrades. The PoC never upgrades Talos, and the Upgrade/LifecycleClient path is deferred, so the
  E3 compatibility evidence is not complete E3
  ([specification review §8](../design/research/20260927-poc-specification-review.md)).
- Remote transports, and recovery of a managed cluster's etcd.
- Applying to a control plane: only workers are dispatched to.
- Any apply mode but `no-reboot`, and a rollout of more than one machine at a time.
- A bounded `Ignore` drift policy.
- SQLite, MySQL and MariaDB; replicated or pooled PostgreSQL or OpenBao; physical backups and
  point-in-time restore.

## Behaviour to know

- **Losing a role does not withdraw approvals.** An approval stays valid after its approver leaves
  the `approver` group. Revoke the approval (`POST /approvals/{id}/revocations`) or the identity
  (`POST /identity-revocations`) when someone loses the role.
- **Identity revocation is permanent.** A revoked subject is refused for good, including on
  retries. A mistaken revocation is corrected only by giving that person a new subject at the
  identity provider.
- **The configuration digest is unkeyed.** Bronzeward records a plain SHA-256 of each whole machine
  configuration. For a configuration without generated key material, that digest lets anyone who
  can read it test guesses of the configuration offline. This was accepted for the PoC and not
  assessed further.
- **No late-landing bound.** A request that was sent but whose response was lost may still land
  later; a measured one landed 26 to 28 s after its send. No upper bound was adopted, so accounting
  for such an attempt stays a `recovery-admin` decision after the settle floor. A later landing is
  found as drift only if it leaves the configuration different from Applied
  ([recovery](recovery.md#account-for-an-attempt-with-no-response)).
- **Leak scanning is exact-copy.** The checks that no secret value leaked look for exact copies of
  each value only.
- **A scenario shows only the paths it drove.**

## Open gaps

The specification review's §10 lists the contracts the PoC leaves open
([specification review](../design/research/20260927-poc-specification-review.md)).
