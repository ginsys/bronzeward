# Omni build-gate refresh: licensing, releases and approvals, direct Talos access

| | |
|---|---|
| **Date** | 24 September 2026 |
| **Work item** | [Refresh Omni build-gate evidence](https://github.com/ginsys/bronzeward/issues/11) |
| **Design reference** | [§18.1 Phase 0](../Talos_Configuration_and_Machine_Management_Design.md#181-phase-0---evidence-before-implementation-contracts), [§20 Comparison with Omni](../Talos_Configuration_and_Machine_Management_Design.md#20-comparison-with-omni), [§20.9 build/no-build gate](../Talos_Configuration_and_Machine_Management_Design.md#209-proposed-differentiators-and-buildno-build-gate) |
| **Artifacts** | This report only. The [investigation fixtures](20260919-investigation-fixtures.md#6-what-each-experiment-gets) give this issue no fixture; it is a documentation and source review. |
| **Decision enabled** | Whether the recorded build decision still rests on current Omni facts for the three hard requirements. Input to the [feasibility review](https://github.com/ginsys/bronzeward/issues/12). No project licence is chosen and no other product is surveyed. |

## 1. Question

Design §20.9 records that the build/no-build gate was evaluated in revision 0.4: production under
an open-source licence, immutable releases with approvals, and direct Talos access with SideroLink
as an optional transport are hard requirements. Operator OpenBao custody is a strong preference,
not a hard requirement. Three hard differentiators satisfy the gate, so the decision was to build,
"subject to re-validation against subsequent Omni releases".

The Omni facts behind that decision are the 3 August 2026 snapshot against Omni v1.9.3. §18.1 says
that snapshot "is not current-market verification" and must be refreshed before implementation
commitment. The [September discussion](20260906-design-discussion-and-peer-review.md) did not
revalidate it either (its §13): "Their historical claims should not be presented as a fresh
competitive or licensing assessment."

The question is narrow. For each of the three hard requirements, does current Omni still fail to
meet it, as the August snapshot said? A requirement Omni now meets would weaken the gate. Anything
Omni added that does not touch the three requirements is out of scope.

## 2. Method

All sources were read on 24 September 2026. Every row in §3 names the source, the exact version
compared, a link and that date.

- **Omni source.** Versions were resolved with `git ls-remote https://github.com/siderolabs/omni`
  on the read date: `main` at `48317b5ee035ec920d7d7ed381f5998b28803dfb`, tag `v1.12.2` at commit
  `020bfffc14c3ea567b3a6d600c9b4ff13de180a5`, tag `v1.9.3` at commit
  `4e7e7bdd7f9273b06227e402fbbacc451d490edb`. `LICENSE` and `client/LICENSE` were read verbatim at
  those refs.
- **Latest release.** The [releases page](https://github.com/siderolabs/omni/releases) listed
  v1.12.2 (23 September 2026) as Latest. Release pages for v1.10.0, v1.11.0, v1.12.0, v1.12.1 and
  v1.12.2 were read, and in review the v1.10.1 to v1.10.6 patch notes as well. `CHANGELOG.md` at
  v1.12.2 was searched for approval, draft, SideroLink, direct-access and licence entries since
  v1.9.3. It holds main-line and beta sections only (1.10.0-beta.0 onwards, with no final v1.10.0,
  v1.11.0 or v1.10.x patch sections).
- **Documentation.** docs.siderolabs.com does not show a version or date for Omni pages. Its pages
  link to their sources in [siderolabs/docs](https://github.com/siderolabs/docs), so each page was
  read as its `.mdx` source at that repository's `main` on the read date,
  `67daa278b9378db3da5b80f6cb38f41db37baa2d`. The page `public/omni/<path>.mdx` is served at
  `https://docs.siderolabs.com/omni/<path>`; this was confirmed for the join page through its edit
  link, whose served copy reported `dateModified` 2026-09-15.
- **Pricing.** The [pricing page](https://www.siderolabs.com/pricing) carries no version or date.
  It was read live on the read date; the served build identifier was
  `dpl_AapCUgDPvpHTXY5dbxExToLJBtnq`.
- **Historical column.** It is taken from the design's §20, whose baseline is 3 August 2026 and
  Omni v1.9.3, with the design's own reference keys (M1-M11). The August documentation pages cannot
  be re-read at their August state: the documentation is unversioned. Only the v1.9.3 `LICENSE` was
  re-read at its tag.

**Tooling caveat.** Release pages and the pricing page were read through a summarising web fetch,
then cross-checked: release notes against the beta and main-line sections of `CHANGELOG.md` at
v1.12.2, the pricing page against its raw text. Licence files and documentation sources were read verbatim. All sources in scope were
reachable; nothing below depends on a page that could not be fetched.

**Status values.** *current-verified*: stated by a primary source read on 24 September 2026.
*historical*: stated in the August snapshot and not re-read at its original version. *preference*:
recorded as a preference in the snapshot; it stays one here. *unresolved*: not settled by any
source read. An absence ("no such feature") is current-verified only for the sources named in its
row, not for Omni as a whole. The Status column grades the current-reading cell; the August
column is historical by construction.

## 3. Findings per requirement

### 3.1 Requirement 1: open-source production licensing target

The Bronzeward side of this requirement is unchanged: an OSI-approved licence is required and the
exact licence is an open point
([design §3](../Talos_Configuration_and_Machine_Management_Design.md#3-current-decisions-preferences-and-open-points)).
This review does not choose it.

| Claim | August 2026 snapshot (Omni v1.9.3, 3 Aug) | Current reading (read 2026-09-24) | Status | Source, version, link |
|---|---|---|---|---|
| Omni server licence | BUSL 1.1; production use requires a commercial licence (design §20.2, M1). | `LICENSE` is Business Source License 1.1 and says it "is not an Open Source license". Additional Use Grant: non-production use, testing and evaluation of Omni itself, or personal home-lab use. Any environment "on which Customer's own development, operations, or business depends" needs a commercial licence. Change License MPL-2.0; Change Date 2030-09-11 at v1.12.2, 2030-09-09 at `main`. | current-verified | [`LICENSE` at v1.12.2](https://github.com/siderolabs/omni/blob/v1.12.2/LICENSE) (commit `020bfff`) and [at `main` `48317b5`](https://github.com/siderolabs/omni/blob/48317b5ee035ec920d7d7ed381f5998b28803dfb/LICENSE), read 2026-09-24 |
| The August licence, re-read at its tag | As above. | `LICENSE` at v1.9.3 has the same Additional Use Grant; Change Date 2030-06-23, Change License MPL-2.0. The August claim holds at its own version. | current-verified | [`LICENSE` at v1.9.3](https://github.com/siderolabs/omni/blob/v1.9.3/LICENSE) (commit `4e7e7bd`), read 2026-09-24 |
| Client library licence | MPL-2.0 (design §20.2). | `client/LICENSE` is Mozilla Public License 2.0. It does not license the server. | current-verified | [`client/LICENSE` at v1.12.2](https://github.com/siderolabs/omni/blob/v1.12.2/client/LICENSE), read 2026-09-24 |
| Vendor reading of production use | Not recorded. | "Production use requires a commercial license: either a self-hosted Omni subscription, or Sidero Labs' hosted SaaS offering". Staging, QA and CI environments an organisation depends on count as production; "there is no free production tier regardless of size". The page says it is interpretation and that the grant is the authoritative text. The FAQ calls the licence "not an open source license"; "What is Omni" repeats that production use requires a commercial licence. | current-verified | [production-vs-non-production](https://github.com/siderolabs/docs/blob/67daa278b9378db3da5b80f6cb38f41db37baa2d/public/omni/self-hosted/production-vs-non-production.mdx), [faqs](https://github.com/siderolabs/docs/blob/67daa278b9378db3da5b80f6cb38f41db37baa2d/public/omni/troubleshooting/faqs.mdx), [what-is-omni](https://github.com/siderolabs/docs/blob/67daa278b9378db3da5b80f6cb38f41db37baa2d/public/omni/overview/what-is-omni.mdx), docs `67daa27`, read 2026-09-24 |
| Commercial plans | Self-hosting requires an Enterprise subscription; the lowest tier is a paid, non-commercial Hobby plan (design §20.2, M11). | Plans listed: Hobby $10/month ("Solo and small setups"), Startup $25/node/month, Omni Enterprise included with Talos Enterprise Linux ($1,000/node/year, 10-node minimum), Edge on request. The FAQ says "Enterprise Linux and Omni run as managed SaaS or fully self-hosted". The page states no production-use terms and does not call Hobby non-commercial. Whether self-hosting needs Enterprise specifically, rather than the documentation's "self-hosted Omni subscription", is not stated. | unresolved | [Pricing](https://www.siderolabs.com/pricing), unversioned, build `dpl_AapCUgDPvpHTXY5dbxExToLJBtnq`, read 2026-09-24 |

The unresolved row does not affect the requirement. Whatever the plans cost, the licence text
itself makes every production deployment of current Omni depend on a commercial licence, and
declares itself not open source.

### 3.2 Requirement 2: explicit releases and approvals

The requirement is the design's draft, compile, publish immutable release, approve, then
controlled rollout (§20.2 "Change activation"; §12.7). The question for Omni is whether a saved
change can wait for an approval before it applies.

| Claim | August 2026 snapshot (Omni v1.9.3, 3 Aug) | Current reading (read 2026-09-24) | Status | Source, version, link |
|---|---|---|---|---|
| Saving a patch applies it | Saving a patch changes desired state and Omni applies it (§20.6, M2, M6). | "Once saved, Omni applies the configuration to the selected machines." | current-verified | [create-a-patch-for-cluster-machines](https://github.com/siderolabs/docs/blob/67daa278b9378db3da5b80f6cb38f41db37baa2d/public/omni/omni-cluster-setup/create-a-patch-for-cluster-machines.mdx), docs `67daa27`, read 2026-09-24 |
| Review surface | Pending and applied configuration history (§20.6, M6). | "Starting with Omni v1.6.0, you can use the configuration diff view to review both pending and applied changes" (Config History tab). It is a view; no page read attaches an approval to it. | current-verified | Same page, docs `67daa27`, read 2026-09-24 |
| Ways to hold a change back | Node locks and rolling machine-set strategies (§20.6, M3). | Locked nodes "retain their configuration from the time they were locked"; "Control plane nodes cannot be locked". A rollout can be staged by locking the nodes to hold back. For worker machine sets, `updateStrategy` and `upgradeStrategy` default to rolling, one machine at a time; control-plane sets always roll one at a time. An imported cluster stays `locked` until `omnictl cluster unlock`, and pending changes can be reviewed before that. Since v1.10.0 a config patch can be disabled: "retained as a resource but is never applied". These hold or pace changes; none is an approval bound to an immutable release. | current-verified | [upgrading-clusters](https://github.com/siderolabs/docs/blob/67daa278b9378db3da5b80f6cb38f41db37baa2d/public/omni/cluster-management/upgrading-clusters.mdx), [cluster-templates reference](https://github.com/siderolabs/docs/blob/67daa278b9378db3da5b80f6cb38f41db37baa2d/public/omni/reference/cluster-templates.mdx), [importing-talos-clusters](https://github.com/siderolabs/docs/blob/67daa278b9378db3da5b80f6cb38f41db37baa2d/public/omni/cluster-management/importing-talos-clusters.mdx), docs `67daa27`; [v1.10.0 release](https://github.com/siderolabs/omni/releases/tag/v1.10.0) (7 Aug 2026); all read 2026-09-24 |
| Cluster templates | Templates can support Git-managed workflows; the live control plane is API-driven (§20.1, M3). | Templates make configuration "reviewable" when kept in a change management system such as `git`; "Syncing the template causes Omni to create the cluster and reconcile it to the declared state." Review happens outside Omni, before the sync. | current-verified | [cluster-template](https://github.com/siderolabs/docs/blob/67daa278b9378db3da5b80f6cb38f41db37baa2d/public/omni/omni-cluster-setup/cluster-template.mdx), docs `67daa27`, read 2026-09-24 |
| Kubernetes bootstrap manifests | Not recorded. | After a Kubernetes upgrade, "Omni shows a diff of the proposed changes before applying them"; the operator applies what is appropriate. This covers bootstrap manifests only, not Talos machine configuration. | current-verified | [upgrading-clusters](https://github.com/siderolabs/docs/blob/67daa278b9378db3da5b80f6cb38f41db37baa2d/public/omni/cluster-management/upgrading-clusters.mdx), docs `67daa27`, read 2026-09-24 |
| No draft/approve release gate | "As of 1.9.3 there is still no draft/approve release gate" (§20.1, M9). | No approval, draft or release-approval feature appears in the release notes of v1.10.0 to v1.10.6, v1.11.0 and v1.12.0 to v1.12.2, or in the beta and main-line sections of `CHANGELOG.md` at v1.12.2 since v1.9.3. None is described on the configuration, patch, template or upgrade pages above. Bounded to those sources. | current-verified | [Releases](https://github.com/siderolabs/omni/releases), latest v1.12.2 (23 Sep 2026); [`CHANGELOG.md` at v1.12.2](https://github.com/siderolabs/omni/blob/v1.12.2/CHANGELOG.md); pages above at docs `67daa27`; read 2026-09-24 |

### 3.3 Requirement 3: direct Talos access with optional SideroLink

The requirement is that the manager reaches Talos directly on internal networks, that direct
Talos mTLS stays available, and that SideroLink is an optional transport (§10, §20.2).

| Claim | August 2026 snapshot (Omni v1.9.3, 3 Aug) | Current reading (read 2026-09-24) | Status | Source, version, link |
|---|---|---|---|---|
| Registration uses SideroLink | Registration is SideroLink-based through v1.9.3 (§20.1, M7). | Two join paths: boot with a "preconfigured SideroLink configuration", or boot standard media and supply a Machine Join Config. The join token that path uses authenticates machines "when they first establish a WireGuard tunnel connection to Omni". Import adds a `SideroLinkConfig` document to each node. "Machine registration is built on top of ... WireGuard"; "SideroLink builds upon WireGuard". | current-verified | [join-machines-to-omni](https://github.com/siderolabs/docs/blob/67daa278b9378db3da5b80f6cb38f41db37baa2d/public/omni/omni-cluster-setup/registering-machines/join-machines-to-omni.mdx), [rotate-siderolink-join-token](https://github.com/siderolabs/docs/blob/67daa278b9378db3da5b80f6cb38f41db37baa2d/public/omni/security-and-authentication/rotate-siderolink-join-token.mdx), [machine-registration](https://github.com/siderolabs/docs/blob/67daa278b9378db3da5b80f6cb38f41db37baa2d/public/omni/infrastructure-and-extensions/machine-registration.mdx), [importing-talos-clusters](https://github.com/siderolabs/docs/blob/67daa278b9378db3da5b80f6cb38f41db37baa2d/public/omni/cluster-management/importing-talos-clusters.mdx), docs `67daa27`, read 2026-09-24 |
| Local Talos API after joining | Disabled once a machine joins (design reference M7). | "The local Talos API is disabled for security reasons, and all future configuration changes must be made through Omni. If you want to access the Talos APIs you will need to re-provision the machine". | current-verified | [join-machines-to-omni](https://github.com/siderolabs/docs/blob/67daa278b9378db3da5b80f6cb38f41db37baa2d/public/omni/omni-cluster-setup/registering-machines/join-machines-to-omni.mdx), docs `67daa27`, read 2026-09-24 |
| Direct configuration writes | Blocked through `talosctl` (§20.6, M2). | "In Omni-managed clusters, direct writes via `talosctl` are **blocked at the API layer**." Omni "*is* the authentication mechanism for external access to Talos and Kubernetes". | current-verified | [how-configuration-works-in-omni](https://github.com/siderolabs/docs/blob/67daa278b9378db3da5b80f6cb38f41db37baa2d/public/omni/omni-cluster-setup/how-configuration-works-in-omni.mdx), [options-for-running-omni](https://github.com/siderolabs/docs/blob/67daa278b9378db3da5b80f6cb38f41db37baa2d/public/omni/self-hosted/options-for-running-omni.mdx), docs `67daa27`, read 2026-09-24 |
| Emergency direct access | Break glass when the management plane is unavailable; the v1.7 direct node access is Omni-mediated (§20.6, M9, M10). | Break glass lets nodes "temporarily allow direct API access on any network interface" with the `os:operator` role. It needs the `--enable-break-glass-configs` server flag on-premises ("Recommended to be disabled") or a support request on SaaS. Afterwards the cluster is "tainted" until CA rotation. An emergency path, not a management transport. Whether the v1.7 Omni-mediated node access the August column cites is still offered was not checked; the pages read do not describe it. | current-verified | [break-glass-emergency-access](https://github.com/siderolabs/docs/blob/67daa278b9378db3da5b80f6cb38f41db37baa2d/public/omni/security-and-authentication/break-glass-emergency-access.mdx), [omni-configuration reference](https://github.com/siderolabs/docs/blob/67daa278b9378db3da5b80f6cb38f41db37baa2d/public/omni/reference/omni-configuration.mdx), docs `67daa27`, read 2026-09-24 |
| No SideroLink-free management mode | "No SideroLink-free management mode" as of v1.9.3 (§20.1, M9). | The configuration reference's `services.siderolink` options select a transport variant (`useGRPCTunnel` tunnels WireGuard over gRPC); none turns SideroLink off. The egress page says "Talos nodes must be able to connect to Omni for cluster management and SideroLink". No release note read (v1.10.0 to v1.10.6, v1.11.0, v1.12.0 to v1.12.2) adds direct management. Bounded to those sources. | current-verified | [omni-configuration reference](https://github.com/siderolabs/docs/blob/67daa278b9378db3da5b80f6cb38f41db37baa2d/public/omni/reference/omni-configuration.mdx), [omni-firewall-egress-requirement](https://github.com/siderolabs/docs/blob/67daa278b9378db3da5b80f6cb38f41db37baa2d/public/omni/omni-cluster-setup/omni-firewall-egress-requirement.mdx), docs `67daa27`; [Releases](https://github.com/siderolabs/omni/releases), latest v1.12.2; read 2026-09-24 |

### 3.4 Outside the three requirements

| Claim | August 2026 snapshot | Current reading | Status | Source, version, link |
|---|---|---|---|---|
| Operator OpenBao custody of secrets and PKI | A strong preference, not a hard requirement (§20.9). | Not re-evaluated; not a gate requirement. | preference | [Design §20.9](../Talos_Configuration_and_Machine_Management_Design.md#209-proposed-differentiators-and-buildno-build-gate), revision 0.5 (baseline 3 Aug 2026), read 2026-09-24 |
| Omni changes since v1.9.3 that touch configuration | Not applicable. | v1.10.0 rejects the Kubernetes CA and service-account key in config patches. v1.11.0 moves install-disk selection to `MachineInstallDiskConfig`. Both widen Omni's reserved fields. Neither affects the three requirements. | current-verified | [v1.10.0](https://github.com/siderolabs/omni/releases/tag/v1.10.0), [v1.11.0](https://github.com/siderolabs/omni/releases/tag/v1.11.0) release notes, read 2026-09-24 |

## 4. What changed since the snapshot

Omni shipped v1.10.0 (7 August 2026), v1.11.0 (4 September), v1.12.0 (11 September) and patch
releases through v1.12.2 (23 September). v1.10.0 was released four days after the August baseline
and before revision 0.5 of the design (6 September), which kept the v1.9.3 baseline.

For the three requirements, nothing changed:

- The licence is still BUSL 1.1 with the same non-production grant; the text at v1.9.3 and at
  v1.12.2 differs only in the Change Date. Each minor release sets a new Change Date (2030-06-23 at
  v1.9.3, 2030-09-11 at v1.12.0 to v1.12.2), so the latest code stays about four years from
  MPL-2.0.
- Saving a patch still applies it. Disabling a patch (v1.10.0) retains a change without applying
  it. It is not a draft that someone approves.
- Every documented join path is SideroLink. The local Talos API is disabled after joining. Direct
  `talosctl` writes are blocked. Among the pages read, break glass is the only direct path
  described.

The August snapshot did not record Sidero Labs' production-use guidance, which was published on 19
June 2026, before the snapshot (siderolabs/docs commit `083c9d3f`, the page's only commit). It
spells out that standing staging, QA and CI environments are production. It is new to this
comparison, not a change in Omni.

## 5. Limits

- **Documentation and source review only.** No Omni instance was run. Behaviour is what the
  licence, release notes and documentation state, not what was observed. In particular, "the
  local Talos API is disabled" is the documentation's statement, not a probe of a joined machine.
- **Absences are bounded.** "No approval gate" and "no SideroLink-free mode" mean none appears in
  the named release notes, changelog range and documentation pages. An undocumented or
  enterprise-only capability would not be seen.
- **Documentation is unversioned.** Pages are pinned by the siderolabs/docs commit read. The site
  may serve a different build later. The August column cannot be re-read at its August state,
  except the v1.9.3 `LICENSE`.
- **Summarised pages.** Release pages and the pricing page were read through a summarising fetch.
  The release findings were cross-checked against the beta and main-line sections of
  `CHANGELOG.md` at v1.12.2, which carry no patch-release sections, and the pricing page
  against its raw text.
- **Not a legal opinion.** Licence rows record what the text says. The production-use page is
  Sidero Labs' own interpretation, as it says itself. Commercial terms were not negotiated or
  confirmed with the vendor.
- **Omni side only.** This review says whether Omni still fails the three requirements. It does
  not show that Bronzeward can meet them; that is the job of the Phase 0 experiments (§18.1).
- **Out of scope.** The project licence, other products, pricing adequacy, and Omni capabilities
  unrelated to the three requirements.

## 6. Recommendation

**Retain the recorded build decision.** For each hard requirement, current Omni (v1.12.2, with
`main` at `48317b5` and the documentation at siderolabs/docs `67daa27`, read 24 September 2026)
still does not meet it:

1. **Open-source production licence: not met.** BUSL 1.1 at v1.12.2 and `main`; the licence calls
   itself "not an Open Source license" and requires a commercial licence for any environment an
   organisation depends on (§3.1).
2. **Explicit releases and approvals: not met.** A saved patch applies. Locks, disabled patches,
   rolling strategies and external Git review hold or pace changes, but no approval binds an
   immutable release before it applies (§3.2).
3. **Direct Talos access with optional SideroLink: not met.** SideroLink is the only documented
   registration and management path. The local Talos API is disabled after joining, and direct
   writes are blocked outside break glass (§3.3).

The August conclusion therefore stands on current evidence, not only on the snapshot. The three
limits that bind it: every absence is bounded to the sources read, nothing was run, and the
commercial-plan row is unresolved but does not bear on the licence finding. Per the issue's
verification method, the owner assesses this; since the conclusion is unchanged, no design
decision changes.

The re-check trigger stays as the design states it: re-check each Omni release (§3, §20.9). Signals
worth watching are a change to `LICENSE`, an approval or draft workflow for
config patches, or a documented way to manage machines without SideroLink.

## 7. Hand-off

**To the [feasibility review](https://github.com/ginsys/bronzeward/issues/12):**

- The three gate rows are current-verified as unmet by Omni v1.12.2 on 24 September 2026 (§3,
  §6). The build decision can be carried forward without re-deciding it.
- The historical v1.9.3 baseline in design §20 is now matched by a dated reading. If the owner
  accepts this report, §20's "Historical comparison baseline" note and the revision 0.5 note at the
  head of the design, which both say current Omni was not re-verified, can cite it instead. The §3 gate
  row ("re-check each Omni release") can add this reading's date. That design edit is not made here.
- The commercial-plan claim in design §20.2 ("self-hosting requires an Enterprise subscription;
  the lowest tier is a paid, non-commercial Hobby plan") is not confirmed by the current pricing
  page (§3.1, unresolved). It does not bear on the gate, but the design's wording should not be
  treated as current.
- OpenBao custody remains a preference and was not re-evaluated.
