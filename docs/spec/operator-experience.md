# Operator experience: bronzewardctl and web UI journeys

**Status: accepted for implementation** by the owner on 25 October 2026 at 21:42 CET, in the
campaign coordination session, answering whether to accept milestone 04 with the refined scope of
[#254](https://github.com/ginsys/bronzeward/issues/254) and
[#255](https://github.com/ginsys/bronzeward/issues/255): "A". This
document sets out the operator journeys for a
`bronzewardctl` command-line client and a web interface over the existing management API, for
[Design the operator journeys for bronzewardctl and the web UI](https://github.com/ginsys/bronzeward/issues/252)
in [04 - Operator experience: CLI and web UI](https://github.com/ginsys/bronzeward/milestone/4).
It specifies no client implementation, frontend framework, login flow, hosting model,
distribution target or credential policy, and it adds no API route. The linked
[wireframes](operator-experience-wireframes.md) are part of this document.

**Accepted.** The recommendations C1-C5 of [§12](#12-alternatives-and-recommendations) are
accepted with this document; C4, the protected review in the CLI, as
[client contracts §11.3](client-contracts.md#113-cli-review) qualifies it. This document is not a
usability or accessibility measurement: the tasks of
[§13](#13-usability-and-accessibility-acceptance) are run on the implemented clients.

**Facts and proposals are kept apart.** A statement about existing behaviour cites the contract
section or the source line it comes from (`internal/api/routes.go:98` means that file and line at
revision `e1c8cb3`). The expected-epoch check that
[#293](https://github.com/ginsys/bronzeward/issues/293) added did not exist there, so the
citations in [§8](#8-states-every-view-must-handle) are read at the revision that lands
[#309](https://github.com/ginsys/bronzeward/issues/309), with that check applied. Client behaviour this document proposes is
marked **Proposed**; with the acceptance above, the marker separates accepted client behaviour
from existing behaviour, and in §13 it keeps its meaning of a target for the owner to set. A
behaviour the API does not support today is listed in
[§11](#11-api-gap-audit) and is not presented as available.

## Contents

- [1. Scope and exclusions](#1-scope-and-exclusions)
- [2. What the operator must be able to tell apart](#2-what-the-operator-must-be-able-to-tell-apart)
- [3. Roles, humans and automation](#3-roles-humans-and-automation)
- [4. Command tree](#4-command-tree)
- [5. Transcripts](#5-transcripts)
- [6. Web interface](#6-web-interface)
- [7. Role journeys](#7-role-journeys)
- [8. States every view must handle](#8-states-every-view-must-handle)
- [9. Secret handling in the clients](#9-secret-handling-in-the-clients)
- [10. Action, route and role matrix](#10-action-route-and-role-matrix)
- [11. API gap audit](#11-api-gap-audit)
- [12. Alternatives and recommendations](#12-alternatives-and-recommendations)
- [13. Usability and accessibility acceptance](#13-usability-and-accessibility-acceptance)
- [14. Proposed refinement of the milestone tickets](#14-proposed-refinement-of-the-milestone-tickets)
- [15. Limits](#15-limits)

## 1. Scope and exclusions

The scope is the accepted configuration-control PoC
([design §18.2](../design/Talos_Configuration_and_Machine_Management_Design.md#182-phase-1---configuration-control-on-an-existing-cluster)):
record an existing cluster and its machines, import their configuration with secrets extracted,
edit native fragments, profiles and assignments, publish, plan, approve, watch the controller
apply, detect drift and adopt or revert it, and recover after a management-state restore.

Why this is needed: the PoC passed its technical acceptance, but the owner judged the operator
experience inadequate. The executable offers only `serve`, `migrate`, `token` and `orphans`
(`cmd/bronzeward/main.go:34-56`), there is no web UI, and the documented path is shell functions
around HTTP requests, JSON, resource identifiers and conditional headers
([timeline assessment §4](../design/research/20261020-poc-timeline-assessment.md#4-human-facing-usability)).
The operator walkthrough needs a `call` helper that threads a bearer token, `Idempotency-Key`
and `If-Match` by hand (`docs/operator/walkthrough.md:30-42`), polls for state, and extracts
references from review provenance with `jq` (`docs/operator/walkthrough.md:184-186`).

Excluded, as the milestone states: upgrades, reset, bootstrap and enrolment, new transports,
provider administration, general secret browsing, direct Talos control, a new credential policy
and production-readiness claims. The licence decision
([#16](https://github.com/ginsys/bronzeward/issues/16)) and the operator-access and
service-credential decisions ([#265](https://github.com/ginsys/bronzeward/issues/265),
[#266](https://github.com/ginsys/bronzeward/issues/266)) are not touched. Client authentication,
API behaviour and compatibility belong to
[#253](https://github.com/ginsys/bronzeward/issues/253), downstream of this document.

## 2. What the operator must be able to tell apart

Three questions raised about the PoC's output show where it misleads:
what the `sources` of a release are and where the configuration itself is; why a key in a YAML
document is empty; and whether an imported configuration was changed when a plan says it will
apply one. Each client view must answer these without the operator reading the specifications.
The examples below are synthetic. They do not reproduce any captured document, and no statement
here says which key was seen empty.

### 2.1 Source, release pointers and rendered configuration

| Thing | What it is | Where it is read | It is not |
| --- | --- | --- | --- |
| Fragment revision | An immutable native Talos YAML `document`, sanitized, plus its `declarations` (`internal/api/sources.go:53-70`) | `GET /fragment-revisions/{id}` | A machine's configuration |
| Profile revision | An ordered list of fragment revision IDs (`internal/api/sources.go:72-78`) | `GET /profile-revisions/{id}` | A merged document |
| Assignment revision | For one machine: profile names and per-layer fragment names (`internal/api/sources.go:80-88`) | `GET /assignment-revisions/{id}` | A configuration |
| Release `sources[]` | Pointers: `kind`, `head`, `revision` (null once removed) and `headRevision` (`internal/api/releases.go:27-58`) | `GET /releases/{id}` | The configuration |
| Per-machine review | The rendered, redacted machine configuration, or `null` with a `notice`, plus reference `provenance` (`internal/api/releases.go:60-100`) | `GET /releases/{id}/machines/{m}/review` | The encrypted artifact that is applied |

Composition order is fixed: layers from global to override, and inside a layer profile pins
first, then the assignment's own fragments
([compilation §6](compilation.md#6-resolution-order-and-composition)). The encrypted full
artifact is never served; the review is its redacted rendering.

**Proposed** presentation rule: a release view never shows `sources` under a heading that could
be read as configuration. The CLI prints them under `Built from:` and the web page under
"Source revisions this release was built from", and both put a per-machine "Rendered
configuration" link beside each machine, which opens the review. See
[W6](operator-experience-wireframes.md#w6-release-and-rendered-review).

### 2.2 Four values that look alike

A synthetic fragment as an author stores it:

```yaml
machine:
  registries:
    config:
      registry.example.invalid:
        auth:
          username: ci-pull
          password: !bwref registry/example-pass
  nodeLabels:
    team: ""
```

with the declaration `registry/example-pass: {kind: string, version: 1}`. The rendered review of
a machine that composes it shows, illustratively (the pinned machinery's encoding decides the
exact text):

```yaml
machine:
  registries:
    config:
      registry.example.invalid:
        auth:
          username: ci-pull
          password: <redacted:registry/example-pass@1>
  nodeLabels:
    team: ""
```

| Seen | Meaning | Source |
| --- | --- | --- |
| `!bwref registry/example-pass` in a source | A reference to a secret held by the provider at the declared version; the value is never stored in the database or served | [compilation §5](compilation.md#5-reference-grammar-and-declaration), design [§6.9](../design/Talos_Configuration_and_Machine_Management_Design.md#69-secret-references-and-validation-stages) |
| `<redacted:REF@VERSION>` or `<redacted:REF@VERSION#N>` in a review | The rendered field came from that reference, or from the member at position N of a mapping reference; the value exists in the encrypted artifact | [compilation §8.3](compilation.md#83-redaction) |
| `<redacted:schema>`, `<redacted:value>`, `<redacted:paired>`, `<redacted>` | Redacted because the pinned machinery marks the field secret, because the text is a copy of a resolved value, because it is a base leaf of a diff whose output holds a redaction token and the output does not show it unchanged at the same path (or a base key the output does not show in the same mapping), or because a path itself holds a value | [compilation §8.3](compilation.md#83-redaction) |
| `configuration: null` with `notice` | The review is withheld because no redacted form could be produced (`internal/api/releases.go:157-158`); nothing is known from it about the content | [compilation §8.3](compilation.md#83-redaction) |
| `team: ""` | An empty value in the rendered review: no redaction token, and no provenance record names its path. It is empty in a source only if that source revision has been read and shows it | the document and the provenance list |

**Proposed** presentation rules:

1. A redaction token is shown as a styled badge with its kind spelled out ("secret
   `registry/example-pass`, version 1"), never as bare text a reader could take for a literal.
   Plain-text CLI output keeps the token verbatim, so it can be searched for.
2. A withheld review is a distinct state: "Configuration not shown: it could not be redacted."
   It is never rendered as an empty document, an empty diff or "no changes"
   ([W11](operator-experience-wireframes.md#w11-empty-withheld-error-and-offline-states)).
3. Before a client explains why a field looks empty, it checks the provenance record and the
   redaction token. Where neither names the field, it says only that the value is empty in the
   rendered review, and points to the fragment revisions that set the field if provenance lists
   them. It never guesses that an empty value was redacted.
4. The provenance view lists exactly the records the API returns: reference, version, the source
   revision and redacted path, and either the output path or the revision that overrode it
   (`internal/api/releases.go:78-100`). It covers reference occurrences, not every field. The
   design wants a full "which fragment last set this field" view
   ([design §6.2](../design/Talos_Configuration_and_Machine_Management_Design.md#62-fragments-profiles-and-assignments));
   that is a gap ([§11](#11-api-gap-audit), G6), not a client feature.

### 2.3 Import, publish, plan, approve, apply

| Step | Who | What changes | What it does not do |
| --- | --- | --- | --- |
| Import (ingestion) | human `author` | Reads the node's configuration, stores its secrets as provider generations and a sanitized import base in a draft ([compilation §2](compilation.md#2-ingestion-extraction-before-persistence)) | Writes nothing to the node (`docs/operator/walkthrough.md:120-121`) |
| Edit | `author` | Draft entries: fragments, profiles, assignments | Renders nothing until publication ([§11](#11-api-gap-audit), G5) |
| Publish | `publisher` | Compiles every covered machine, encrypts the artifacts, creates an immutable release that becomes each machine's `Desired` ([design §12.6](../design/Talos_Configuration_and_Machine_Management_Design.md#126-offline-semantics)) | Sends nothing to a machine; authorizes nothing ([design §7.4](../design/Talos_Configuration_and_Machine_Management_Design.md#74-publication-without-distributed-transactions)) |
| Plan | `publisher` | An immutable plan binding release, machine, operation, mode, assignment revision, deadlines, expiry, epoch and evidence (`internal/api/plans.go:172-201`) | Approves or dispatches nothing |
| Approve | human `approver` | Records the approval of that exact plan | Does not apply; the controller commits later after its own checks ([execution and recovery §3](execution-recovery.md#3-dispatch-commitment)) |
| Apply | the controller | Commits, sends, verifies, records `Applied` | Is not a client command |
| Adopt | the controller after an approved `adopt` plan | Records `Applied` by observation | Sends nothing ([execution and recovery §6.3](execution-recovery.md#63-adopt)) |

The answer to "the configuration was imported; was it changed?" is in the evidence, not in the
step names. Adoption records the adopted release as `Applied` with the digest of the baseline,
the bytes read from the node, not the digest of the recompiled artifact. Whether an artifact compiled from an unchanged import
reproduces the node's bytes is not evidenced, and the renderer was seen re-encoding a whole
configuration ([execution and recovery §6.3](execution-recovery.md#63-adopt)). An adopt plan
therefore reports `evidence.baseline.pendingConvergence` (`internal/api/plans.go:138-144`), and
the first `apply-config` plan after it carries a whole-configuration redacted diff, or says it is
withheld (`internal/api/plans.go:128-136`). **Proposed:** the plan view answers "will this change
the machine?" only from that diff: "Changes shown below", "No differences in the redacted
configurations", or "Diff withheld: the change cannot be shown". It never infers "unchanged"
from matching release IDs, names or sources.

### 2.4 Desired, applied and observed

`GET /machines/{id}` returns `desired` (a release ID or null), `applied {release, source}` (or
null) with source `operation` or `adoption`, `observed {observation, at, release}` (or null),
`frozen`, `scopeState`, `openDrift` and `pendingConvergence` (`internal/api/reads.go:155-196`). `observed` is the
observation with the highest basis that read a configuration digest, not necessarily the newest
observation: a later read that failed or left the digest unread is not it. `observed.release`
names the applied release when that observation's digest equals the `Applied` digest, the
desired release when it equals the `Desired` release's, and is null otherwise, including when it
equals some older release's; the digest itself is never served
(`internal/api/reads.go:155-171`). The newest observations, with their unread causes, are read
from `GET /machines/{id}/observations` (`internal/api/observations.go:14-40`) and the timeline.

Drift is defined against `Applied`, not against releases: an observed digest that differs from
`Applied` while no operation holds the machine opens a drift record, even when it equals
`Desired` or a historical release
([execution and recovery §6.1](execution-recovery.md#61-detection)). `openDrift` is therefore
the authority for "drifted", never `observed.release`. `Applied` is the configuration Bronzeward
last verified or adopted. After an adoption it holds the digest of the node's own bytes, while
the adopted release's compiled artifact may differ from them; equal release IDs then do not show
that the artifact is what runs
([execution and recovery §6.3](execution-recovery.md#63-adopt)).

**Proposed** status sentences, derived only from those fields, the observations list and the
timeline:

| Fields | Sentence |
| --- | --- |
| no open drift, observed.release = applied.release | "The configuration read at `<observed.at>` is attributed to `<observed.release>`, which is the `Applied` release (by `<applied.source>`)." |
| as above, applied.source = adoption | adds "Adopted from the node's own bytes; the release's compiled configuration may differ. The adopt plan's `pendingConvergence` evidence shows whether it does; a later `apply-config` plan withholds its diff when it does." If desired = applied.release, also adds "The read may match the adopted bytes or this release's compiled configuration; the API does not say which." (`internal/api/reads.go:163-164`) |
| pendingConvergence, applied not null, no open drift | adds "Pending convergence: the configuration of the selected release `<desired>` for this machine differs from what Bronzeward applied." |
| pendingConvergence, applied null | adds "Pending convergence: the selected release `<desired>` has not been applied to this machine." |
| desired not null, desired ≠ applied.release, not pendingConvergence, no open drift | adds "The selected release `<desired>` carries this machine's applied configuration unchanged." |
| openDrift not null | "Drifted since `<at of the openDrift.observation observation>`: a read differed from what Bronzeward last verified (`<applied.release>`)." If observed.release = desired, adds "It equals the selected release; that does not close the drift." |
| no open drift, observed.release null | "The configuration read at `<observed.at>` matched neither `Applied` nor `Desired`, and no drift is open; an operation may hold the machine. See the timeline." |
| no open drift, observed.release = desired ≠ applied.release (an operation holds the machine: no drift opens while one does, [execution and recovery §6.1](execution-recovery.md#61-detection)) | "The configuration read at `<observed.at>` is attributed to the selected release `<observed.release>`." |
| an observation with a higher `basis` than the one `observed` names (the machine read selects `observed` by highest basis, `internal/api/reads.go:171`; the observations list serves `basis`, `internal/api/observations.go:21`), with unread causes | adds "The latest read at `<time>` could not read: `<causes>`." |
| applied null | "Not applied or adopted by Bronzeward." |
| desired null | "No release selected." |
| observed null | "No configuration read recorded." No time is shown. |
| frozen | adds "Frozen: no new commitment or attempt until an approver unfreezes; an attempt already recorded is not stopped, and an adoption can still be recorded." |

A matching release ID never by itself means healthy, fresh or converged: no health check is
bound in the PoC (`health: none-bound`, `internal/api/observe.go:86`), and every sentence says
"Health: not checked". Pending convergence and drift are different
([execution and recovery §6.1](execution-recovery.md#61-detection)). Pending convergence is the
`Desired` release's artifact digest for the machine differing from the `Applied` digest
([execution and recovery §1](execution-recovery.md#1-supported-operation-and-state-values)),
which after an adoption is the digest of the node's own bytes
([execution and recovery §6.3](execution-recovery.md#63-adopt)), so equal release IDs do not
rule it out. The machine read serves it as `pendingConvergence`, without either digest
(`internal/api/reads.go:155-171`; [§11](#11-api-gap-audit), G16). A newly selected release can
carry the same configuration for an unchanged machine, and is then not pending; a machine with a
`Desired` release and no `Applied` is. The adopt plan's `pendingConvergence` evidence shows the
same comparison for the release it adopts (`internal/api/plans.go:315`). A freeze closes the scope gate for commitments and attempts but
does not stop an attempt already recorded, and does not block an adoption record
([execution and recovery §6.2](execution-recovery.md#62-freeze),
[§6.3](execution-recovery.md#63-adopt) requirement 4.5). See
[W3](operator-experience-wireframes.md#w3-machine-detail).

## 3. Roles, humans and automation

Roles are separate grants, installation-wide in the PoC, mapped from the identity provider's
group claims for humans; automation holds only `viewer`, `author` and `publisher`
([design §13.7](../design/Talos_Configuration_and_Machine_Management_Design.md#137-poc-identity-and-approval-policy);
`internal/auth/tokens.go:365-370`). Human-only routes refuse automation whatever its roles
([persistence and API §10.3](persistence-api.md#103-authorization)).

| Role | Can, through the clients | Human only in this role |
| --- | --- | --- |
| viewer | read every ordinary resource, including acts and timelines; never secrets, and not the protected import review, which is human-author-only (`internal/api/routes.go:99`) | no |
| author | record clusters and machines and replace Talos endpoints; import and review; edit and discard drafts; freeze | cluster, machine and endpoint records, every ingestion route |
| publisher | publish, create plans, cancel plans it created, freeze | no |
| approver | approve plans, revoke approvals, cancel plans, freeze, unfreeze | approval |
| recovery-admin | recovery entry and exit, accounting, takeovers, resolutions, scope marks and releases, identity revocation, revoke approvals, cancel plans, freeze | every recovery route; approval revocation, plan cancellation and freeze are not human-only |

Sources: `internal/api/routes.go:98-135`. The token administration for automation stays a
server-side command (`cmd/bronzeward/token.go:22-26`); neither client offers it.

**Proposed:** both clients show the roles the current credential holds, and disable an action
the identity cannot take with the reason ("needs approver", "humans only"). A 403 body lists
only the roles the route accepts (`internal/api/api.go:380-392`); the roles the credential holds
come from `GET /identity`, served since [#277](https://github.com/ginsys/bronzeward/issues/277) ([§11](#11-api-gap-audit), G1). The server's
refusal stays authoritative.

## 4. Command tree

**Proposed.** Noun-verb, as `kubectl`, `talosctl` and `gh` users expect. The three steps that
must stay distinct get three distinct words: `publish` and `approve` are top-level verbs, and
`plan` is its own noun ([§12](#12-alternatives-and-recommendations), C1). Every leaf maps to a row in
[§10](#10-action-route-and-role-matrix).

```text
bronzewardctl
  login | logout                      sign in or out of the current context (#253)
  context list | use | add | remove   API endpoint, TLS trust and identity per installation
  whoami                              identity, roles, epoch, recovery mode (GET /identity)
  status                              installation epoch, recovery mode, open drift, unresolved ops

  cluster list | show | add
  machine list | show | add | set-endpoint
  machine timeline | observations    paged; --follow polls (G9)
  machine freeze | unfreeze           --reason required

  draft create | list | show | discard
  fragment set | rm | show | history  set reads the document from a file or stdin, never argv
  profile set | rm | show | history
  assignment set | rm | show | history

  import start                        --machine --draft [--review]
  import status | review | mark | continue | takeover | abandon

  publish <draft>                     waits for the publish operation unless --no-wait
  release list | show | review | provenance
  plan create | list | show | cancel
  approve <plan>                      shows the plan evidence and asks for the plan ID
  approval show | revoke

  operation list | show | watch       watch follows events and resumes after a disconnect
  act list                            the audit record
  drift show <machine>
  drift adopt <machine>               composite: drift-adoption import, publish, then plan create
  drift revert <machine>              composite: plan create (apply-config) for desired

  recovery status | enter | exit
  recovery takeover | account | resolve | scope-account | mark | release
  identity revoke

Global: --context, -o table|json|yaml, --no-input, --no-wait, --idempotency-key, --if-match
```

**Proposed** conventions:

- Exit codes: 0 success; 1 refused by the server (problem shown); 2 usage error; 3 outcome
  unknown (ambiguous response; the command prints its idempotency key and what to read before
  deciding, [§8](#8-states-every-view-must-handle));
  4 a waited-for operation ended `failed`, `rejected` or `cancelled`; 5 still `unresolved` at the
  end of a wait.
- `-o json` prints the API resource unchanged, so scripts never depend on table layout. The
  protected import review has no `-o json` or `-o yaml` form ([§9](#9-secret-handling-in-the-clients)). Tables
  follow `NO_COLOR` and degrade to plain text when stdout is not a terminal.
- A command that starts an operation prints its ID and waits by default, showing events;
  `--no-wait` returns at once.
- Names are accepted where the API keys by name (fragments, profiles and the draft path) and
  identifiers everywhere else. A cluster is addressed by its ID and displayed with its recorded
  `name` beside it (`cl_9fd… (lab-a)`); the client resolves no names, except fragment names within a draft for a profile's pins ([§5.4](#54-editing-fragments-profiles-and-assignments)). Machines have no human name in the API
  ([§11](#11-api-gap-audit), G8), so tables show the Talos endpoint beside the identifier, and
  the machine detail adds the node ID.
- No command applies configuration. Dispatch is the controller's; the clients plan, approve,
  watch and cancel.

## 5. Transcripts

Synthetic data throughout. Identifiers are shortened as `mch_tqhc…35uq`; real output prints them
whole. Text after `#` is commentary, not output. Each transcript names the role the signed-in
identity holds. Other people appear by readable names (`ops-ben`). The API serves only their
principal identifiers today; gap G15 ([§11](#11-api-gap-audit)) adds a human's subject and
issuer, and its display name from API revision 5
([persistence and API §10](persistence-api.md#10-authentication-and-authorization)). A human
without one is shown by subject and issuer.

### 5.1 Sign-in and context

```text
$ bronzewardctl context add lab --server https://bronzeward.lab.example.invalid
Context "lab" added. TLS: system trust store.
$ bronzewardctl login --context lab
Opening the identity provider sign-in for lab ...        # mechanism decided in #253
Signed in as ops-anna@example.invalid.
$ bronzewardctl whoami
Context   lab (https://bronzeward.lab.example.invalid)
Identity  ops-anna@example.invalid   human
Roles     author, publisher           # GET /identity (G1)
Epoch     ep_bqek…ekgi   recovery mode: off
Token     expires in 14m                                      # renewal: #253
```

By default the server refuses a human token whose lifetime exceeds 15 minutes
(`internal/auth/auth.go:175`, `internal/config/config.go:125`, `internal/config/config.go:354-355`),
so renewal is part of every journey, not an edge case.
The server answers a token it does not accept, whether for issuer, audience, expiry or
signature, with the same `401 unauthenticated` and no detail; a revoked identity with
`403 identity-revoked`; and an unreachable identity provider or database with
`503 dependency-unavailable` (`internal/api/api.go:314-322`;
[persistence and API §10.1](persistence-api.md#101-humans-oidc)). The client says only what the
response establishes: "Not accepted by lab: sign in again", "Your access was revoked; contact a
recovery administrator", "Authentication could not reach the identity provider or the database;
nothing was committed".
**Proposed for #253:** a client may add what it can see locally, such as its own token's
expiry time, labelled as the client's reading and never as the server's reason. A renewal that
returns a different principal is shown as a new sign-in, never silently continued
([§8](#8-states-every-view-must-handle)).

### 5.2 Inventory

```text
$ bronzewardctl machine list --cluster cl_9fd…m3ka
MACHINE         ENDPOINT          DESIRED        APPLIED (SOURCE)          OBSERVED             STATE
mch_k2p…7xqa    10.5.0.2:50000    rel_fgqv…j6m   rel_fgqv…j6m (operation)  =applied, 2m ago     matches applied
mch_tqhc…35uq   10.5.0.3:50000    rel_fgqv…j6m   rel_p4nx…8wd (adoption)   =applied, 1m ago     pending convergence
mch_w4ze…n3fa   10.5.0.4:50000    rel_fgqv…j6m   rel_fgqv…j6m (operation)  no match, 5m ago     drifted, frozen
Health is not checked in this installation.
```

`OBSERVED` shows how the last configuration read compares, not a health result; "no match"
means it equals neither `Applied` nor `Desired`, and "drifted" comes from the open drift record
([§2.4](#24-desired-applied-and-observed)).

`GET /machines` filters by `cluster` (`internal/api/reads.go:199-218`), so `machine list --cluster`
passes it to the server ([§11](#11-api-gap-audit), G9).

Recording a machine is a human `author` act, with the node's Talos identity read beforehand
([acceptance plan S1](acceptance-plan.md#s1-adoption-of-the-existing-cluster)):

```text
$ bronzewardctl machine add --cluster cl_9fd…m3ka --talos-node-id 9Zs…= --platform metal \
    --talos-endpoint 10.5.0.5
Machine mch_b3c…aa2q recorded in cluster cl_9fd…m3ka (lab-a). Endpoint 10.5.0.5:50000.
Next: give Bronzeward the cluster's Talos access in the provider, then import its configuration.
```

`talosEndpoint` is an IP address with an optional port, stored with the default port 50000
(`internal/talos/endpoint.go:15-19`). Writing the talosconfig to the provider stays outside
Bronzeward ([persistence and API §3.3](persistence-api.md#33-talos-access)).

### 5.3 Protected import with review

```text
$ bronzewardctl draft create --cluster cl_9fd…m3ka --title "import worker-3"
Draft drf_2rmp…457a open, revision 1.
$ bronzewardctl import start --machine mch_b3c…aa2q --draft drf_2rmp…457a --review
Import ing_6fd…q2la started (operation op_zcfw…5mcm). Staging: encrypted. Review: requested.
  staged ... paused for review (generation 1, expires 14:52).
Run: bronzewardctl import review ing_6fd…q2la
$ bronzewardctl import review ing_6fd…q2la
[ protected review: this client writes it nowhere but this terminal. Terminal scrollback,
  session recording and screen capture are outside its control. ]
machine:
  install:
    disk: /dev/sda
  files:
    - path: /var/etc/example.conf
      content: "token = BWSYNTH-example"            # synthetic; not recognised by the schema
  ...
Mark a value as secret by its path, one per line; empty line to finish:
mark> doc[0]/machine/files/0/content
mark>
Marked 1 path; re-staged and paused again (generation 2). Review again or continue.
$ bronzewardctl import continue ing_6fd…q2la
Continued. Import released into draft drf_2rmp…457a (now revision 2).
```

Facts: review needs encrypted staging and is requested when the import starts
(`internal/api/ingestions.go:121-124`). The review is human-author-only and answered with
`Cache-Control: no-store` (`internal/api/review.go:18-31`). It may contain secrets the schema
did not recognise; that is why it exists ([compilation §3.6](compilation.md#36-operator-review)).
A mark is refused with the rule and the mark's position, never its path
([compilation §3.6](compilation.md#36-operator-review), item 4). A continuation is accepted
(`202`) and runs in the background; if the draft moved meanwhile, that run ends the operation
`failed` and abandons the claim itself, so nothing is left to abandon
(`internal/api/continuations.go:60-63`, `internal/api/runner.go:108-112`,
`internal/api/runner.go:384-386`, `internal/api/runner.go:457-468`). Marks are HMAC-keyed in the idempotency
fingerprint and never stored (`internal/api/marks.go:58-68`).

**Proposed**, as qualified by [client contracts §11.3](client-contracts.md#113-cli-review) against
[compilation §3.6](compilation.md#36-operator-review): the review is shown only on a terminal,
through an in-process pager with no temporary file and no `$EDITOR`. It is refused when stdout
is not a terminal, with no override, and it has no `-o json`, `-o yaml`, export or save form: a
warning would not stop ordinary redirection or log retention. This limits what the client
itself persists; it does not prevent capture by the terminal, a recording, a host crash dump or
the operator copying it. Marks come from the prompt or stdin, never from argv or a client-read file,
because a mark's path can name a secret mapping key
([persistence and API §7.1](persistence-api.md#71-the-key); [§9](#9-secret-handling-in-the-clients),
[§12](#12-alternatives-and-recommendations) C4).

Failure branches the clients must show, each from `GET /ingestions/{id}` states `held`, `paused`,
`resumed`, `released` and `abandoned` ([compilation §3.2](compilation.md#32-claim-states-and-timers)):

```text
$ bronzewardctl import continue ing_6fd…q2la
Continued (operation op_zcfw…5mcm) ... failed: precondition-failed. The draft changed after
this import started, so the import was not applied and its claim is abandoned.   # exit 4
Import again against the draft's current revision:
  bronzewardctl import start --machine mch_b3c…aa2q --draft drf_2rmp…457a --review
$ bronzewardctl import status ing_8nw…c5ra                 # a different import, its runner gone
State: held, lease lapsed 3m ago (owner generation 2). Encrypted staging.
Take it over to resume, or abandon it:
  bronzewardctl import takeover ing_8nw…c5ra
```

A machine recorded from an existing cluster has no `Applied` yet, and an `apply-config` plan
for it is refused until it is handed over (`internal/api/plans.go:272-275`). The handover
gives the machine an assignment selecting something in the import draft, publishes it and adopts
the machine with an adopt plan that binds no drift record. A machine published with no assignment
has no assignment revision in the release, and its adopt plan is refused
(`internal/api/publish.go:713`, `internal/api/plans.go:325-328`; `docs/operator/walkthrough.md:128-141`)
([execution and recovery §6.3](execution-recovery.md#63-adopt), existing-cluster handover;
[acceptance plan S3](acceptance-plan.md#s3-approval), handover part):

```text
$ bronzewardctl assignment set mch_b3c…aa2q --draft drf_2rmp…457a --fragment cluster=cluster-base
Assignment saved (draft revision 3).
$ bronzewardctl publish drf_2rmp…457a                          # role: publisher
Publishing draft drf_2rmp…457a revision 3 (operation op_t6j…w2na) ... succeeded.
Release rel_9vbn…c1ta is now Desired for 4 machines: every machine of cluster cl_9fd…m3ka with
an import base. Nothing has been sent to any machine.
mch_b3c…aa2q has no Applied: hand it over with an adopt plan.
$ bronzewardctl plan create --release rel_9vbn…c1ta --machine mch_b3c…aa2q --operation adopt
Plan pln_h2d…r8qa proposed: adopt, handover (no drift record, no baseline revision).
It sends nothing to the machine. It is not approved.
$ bronzewardctl approve pln_h2d…r8qa                           # role: approver, human
...
Approved. The controller records the adoption without sending anything; Applied becomes
rel_9vbn…c1ta (adoption) once it does.
```

The approval screen is §5.6's, with the adoption baseline evidence in place of a diff
(`internal/api/plans.go:138-168`). An adoption records the digest of the node's own bytes
([§2.4](#24-desired-applied-and-observed)); the plan's evidence says whether the release's
artifact differs from them (`pendingConvergence`, `internal/api/plans.go:315`). In this
example the adopt plan reports `pendingConvergence: false`: the compiled artifact is
byte-identical to what the node runs, which equal values alone would not establish. §5.6's later
`apply-config` plan shows a diff from `rel_9vbn…c1ta`; in this example none of the plan's
withholding cases applies (`internal/api/plans.go:470-477`).
Had the artifact differed, that plan would show "Diff withheld": no redacted form exists of what
the machine runs (`internal/api/plans.go:457-472`).

### 5.4 Editing fragments, profiles and assignments

```text
$ bronzewardctl fragment set worker-labels --draft drf_8kdq…z6pa --layer role \
    --file worker-labels.yaml --declarations worker-labels.refs.yaml
Fragment worker-labels saved in draft drf_8kdq…z6pa (revision frv_3jn…k4ma; draft revision 4).
$ bronzewardctl profile set workers --draft drf_8kdq…z6pa --fragments worker-labels,worker-sysctls
Profile workers saved: pins worker-labels frv_3jn…k4ma, worker-sysctls frv_8wq…c3ra (draft revision 5).
A profile pins revisions: after changing either fragment, set the profile again to use it.
$ bronzewardctl assignment set mch_b3c…aa2q --draft drf_8kdq…z6pa --profiles workers \
    --fragment cluster=cluster-base
Assignment saved (draft revision 6).
$ bronzewardctl draft show drf_8kdq…z6pa
Draft drf_8kdq…z6pa "worker labels" open, revision 6, cluster cl_9fd… (lab-a)
  import base   mch_b3c…aa2q   ibr_2xx…qa4m
  fragment      worker-labels  role     changed (base frv_1ab…, now frv_3jn…)
  profile       workers        2 fragments
  assignment    mch_b3c…aa2q   profiles: workers; cluster: cluster-base
Not rendered yet: publish to see each machine's configuration.   # G5
```

A profile lists fragment revision IDs, not names (`internal/api/sources.go:72-78`;
`docs/operator/walkthrough.md:199`); the CLI resolves each name to its revision in the draft and
says that the profile pins it. The draft's ETag changes on every write
(`internal/api/drafts.go:174-180`). **Proposed:** the
CLI remembers the last ETag it saw for each draft in the context, sends it as `If-Match`, and
stores the new one from each response. If another author changed the draft:

```text
$ bronzewardctl fragment set worker-labels --draft drf_8kdq…z6pa --file worker-labels.yaml
Refused: precondition-failed. The draft changed since you last read it (you saw revision 6, it is
now revision 7). Nothing was saved. Review the change before writing again:
  bronzewardctl draft show drf_8kdq…z6pa
```

The client never fetches the new ETag and repeats the write by itself, which would overwrite the
other author's change unseen ([persistence and API §4.1](persistence-api.md#41-etags)). A
fragment `document` is never in argv: `--file` or stdin.

### 5.5 Publication, rendered review and provenance

```text
$ bronzewardctl publish drf_8kdq…z6pa          # role: publisher
Publishing draft drf_8kdq…z6pa revision 6 (operation op_m2a…z7ka) ... succeeded.
Release rel_h7rt…2bd is now Desired for 4 machines: every machine of cluster cl_9fd…m3ka with
an import base. Nothing has been sent to any machine.
Next (mch_b3c…aa2q has an Applied, so apply-config; a machine with none is handed over first, §5.3):
  bronzewardctl plan create --release rel_h7rt…2bd --machine mch_b3c…aa2q --operation apply-config
$ bronzewardctl release show rel_h7rt…2bd
Release rel_h7rt…2bd  cluster cl_9fd… (lab-a)  published by ops-ben (publisher) 10:02
Renderer   contract v1.13, machinery v1.13.6, Kubernetes v1.34.1
Built from (source revisions, not configuration):
  fragment     cluster-base    frv_5cz…h1ma
  fragment     worker-labels   frv_3jn…k4ma
  fragment     worker-sysctls  frv_8wq…c3ra
  profile      workers         prv_7ce…m2qa
  assignment   mch_b3c…aa2q    asr_5qp…d7fa
  assignment   3 more, one per other covered machine
Rendered configuration per machine (4):
  mch_b3c…aa2q   validated: metal   bronzewardctl release review rel_h7rt…2bd mch_b3c…aa2q
  mch_k2p…7xqa, mch_tqhc…35uq, mch_w4ze…n3fa: the same command with their IDs
$ bronzewardctl release provenance rel_h7rt…2bd mch_b3c…aa2q
REFERENCE                VERSION  FROM (REVISION, PATH)                           RENDERED AT
registry/example-pass    1        frv_3jn…k4ma, doc[0]/machine/registries/…/password  doc[0]/machine/registries/…/password
Provenance covers secret references only; other fields are not traced.         # G6
```

The publish operation's `result` names the release (`internal/api/publish.go:323`); a draft
does not (`internal/api/inventory.go:349-356`, [§11](#11-api-gap-audit) G4). A publication that
cannot get fresh evidence for every covered machine fails rather than select a new `Desired`
([design §12.6](../design/Talos_Configuration_and_Machine_Management_Design.md#126-offline-semantics)).

### 5.6 Plan and approval

```text
$ bronzewardctl plan create --release rel_h7rt…2bd --machine mch_b3c…aa2q \
    --operation apply-config --mode no-reboot             # signed in as ops-ben; role: publisher
Plan pln_f645…cawy proposed. It is not approved and nothing will run until an approver approves.
$ bronzewardctl approve pln_f645…cawy                      # ops-ben; role: approver, human
Plan pln_f645…cawy  proposed  expires 2026-10-22 10:05 (in 23h59m)
  Machine       mch_b3c…aa2q  10.5.0.5:50000  cluster cl_9fd… (lab-a)  route 10.5.0.5:50000
  Operation     apply-config, mode no-reboot
  Release       rel_h7rt…2bd   (machine Desired: rel_h7rt…2bd)
  Assignment    asr_5qp…d7fa   baseline revision 4    drift record: none
  Deadlines     transport 1m, verification 10m, observation age 5m, attempts 3
  Created by    ops-ben (publisher)   epoch ep_bqek…ekgi
Evidence
  Validation    passed at publication (contract v1.13, machinery v1.13.6)
  Dry run       not available: the PoC has none
  Diff          whole redacted configuration, applied rel_9vbn…c1ta -> rel_h7rt…2bd
    @@ machine.nodeLabels @@
    +  team: storage
Self-approval   predicted: you created this plan and published this release, and authored no
                fragment, profile or assignment revision in it. Import-base authorship and
                automation you own are not predicted. The approval records the actual
                reasons; self-approval is marked and allowed.
Type the plan ID to approve, or press Enter to stop: pln_f645…cawy
Approved (apr_2ztr…47vy). Recorded self-approval reasons: created-plan, published.
The controller re-checks the plan at use time; it applies it only if every check passes.
```

The fields shown are the plan resource's (`internal/api/plans.go:172-201`) and its evidence
kinds: diff or withheld diff, adoption baseline, validation, and an unavailable dry run
(`internal/api/plans.go:128-168`). Omitted plan values take configured defaults
(`internal/config/config.go:141-148`). Self-approval is computed when the approval is recorded,
with the reasons `created-plan`, `published`, `authored-change`, `authored-reused` and
`owned-automation`, then marked and allowed (`internal/api/approvals.go:220-226`;
[design §13.7](../design/Talos_Configuration_and_Machine_Management_Design.md#137-poc-identity-and-approval-policy)).
Before approval a client predicts `created-plan` and `published` from the plan's creator and the
release's publisher, and `authored-change` and `authored-reused` for fragment, profile and
assignment revisions from the release's `sources[]` (`internal/api/releases.go:29-34`), each
revision's `author` (`internal/api/sources.go:60`, `internal/api/sources.go:77`,
`internal/api/sources.go:87`) and the draft's entries, which name the revisions it introduced
(`internal/api/reads.go:265-287`; `internal/api/approvals.go:200-215`). Import-base authorship
and `owned-automation` cannot be predicted: no read serves an import-base revision's author for
a release, or a service principal's responsible human. The prediction therefore never presents
an approver as clear of self-approval; the returned approval owns the reasons.

**Proposed:** `approve` always prints the plan before asking, the confirmation is the plan ID
typed back (not `y`), and approval has no `--yes`. These are guards against approving by habit
without the evidence on screen; they do not prove that a person typed it, and a script can type
a plan ID. Human-only approval is enforced by the server from the authenticated principal's
class: an automation credential gets `403` with `humanOnly: true`
(`internal/api/api.go:380-392`), and the CLI says so before sending when the context holds an
automation token.

A revert approval adds one line, from `unseenOverwrite` (`internal/api/approvals.go:49-51`):

```text
Overwrites    the drifted configuration, which no reviewer has seen (drift opened 09:41).
              Approving records that you approve overwriting it.
```

### 5.7 Operation tracking

```text
$ bronzewardctl operation watch op_q8r…t2na
apply-config  mch_b3c…aa2q  plan pln_f645…cawy
10:06:02  committed            attempt 1 (controller ctl-b, generation 3)
10:06:03  sending              route 10.5.0.5:50000
10:06:04  response accepted
10:06:31  verifying            observation obs_x7m…
10:06:40  completed            applied rel_h7rt…2bd (operation)
$ bronzewardctl operation watch op_q8r…t2na          # after a network drop
Reconnected; resumed after event ep_bqek…ekgi:14.
```

Events stream as Server-Sent Events with `Last-Event-ID` `<epoch>:<number>`; a different epoch
yields a `reset` event and a replay from the start; the stream ends at a terminal state or at the
expiry of the token that opened it (`internal/api/operations.go:231-266`). `unresolved` is not
terminal (`internal/api/operations.go:144-151`). **Proposed:** `watch` reconnects with the last
event ID and a renewed token; on `reset` it prints "The installation was restored to an earlier
state; replaying this operation's events from the start" and clears what it showed.

```text
10:06:03  sending              route 10.5.0.5:50000
10:07:03  transport deadline passed; no response recorded
10:07:03  unresolved           completion and retry safety are unknown. A recovery
                               administrator must account for attempt 1. No conflicting
                               work on this machine proceeds until then.
Still unresolved; exiting with 5.
```

### 5.8 Drift: freeze, then adopt or revert

This scenario happens before the publications of §5.3 and §5.5, while every covered machine's
`Desired` is still `rel_fgqv…j6m` ([§5.2](#52-inventory)).

```text
$ bronzewardctl drift show mch_w4ze…n3fa
Drift opened 09:41 by observation obs_k3d…, last seen 09:46. Late landing: no.
A configuration read differed from what Bronzeward last verified: Applied rel_fgqv…j6m.
Bronzeward cannot show what changed until it is imported for adoption.          # G7
Choose: freeze while you decide; adopt it into a release; or revert to Desired.
$ bronzewardctl machine freeze mch_w4ze…n3fa --reason "incident 4711"
Frozen. No new commitment or attempt for this machine until an approver unfreezes it.
An attempt already recorded is not stopped; an adoption can still be recorded.
```

Drift compares the observed digest with `Applied`; an observation equal to `Desired` or to an
older release is still drift
([execution and recovery §6.1](execution-recovery.md#61-detection)). Detection neither freezes
nor applies anything. A freeze makes approved plans fail at commitment and refuses attempt
transactions, does not stop an attempt already recorded, survives drift resolution, and only an
`approver` lifts it ([execution and recovery §6.2](execution-recovery.md#62-freeze)).

Adopt, as a composite command over existing routes:

```text
$ bronzewardctl drift adopt mch_w4ze…n3fa --draft drf_9pq…a1ka --review   # role: author, human
Step 1/3  import (drift-adoption) ing_4hw…, paused for review -> review and mark, then continue
...
Step 2/3  publish: needs a publisher.   bronzewardctl publish drf_9pq…a1ka
Step 3/3  plan an adoption: needs a publisher, then an approver.
          bronzewardctl plan create --release <release> --machine mch_w4ze…n3fa --operation adopt
```

The adopt plan binds the open drift record; approval leads the controller to record the
adoption without sending anything, even while frozen
([execution and recovery §6.3](execution-recovery.md#63-adopt)). An adopted base that now holds
a value as a reference refuses a fragment that overrides it, so the author must stop selecting
such fragments (`docs/operator/walkthrough.md:303-306`). **Proposed:** the composite command
prints each role's step instead of failing at the first role the caller lacks.

Revert:

```text
$ bronzewardctl drift revert mch_w4ze…n3fa                     # role: publisher
Plan pln_c9x… proposed: apply-config of Desired rel_fgqv…j6m, bound to drift opened 09:41.
The machine is frozen: the plan cannot commit until an approver approves it and, separately,
unfreezes the machine.
```

A revert is an `apply-config` plan of the machine's `Desired` while drift is open, approved with
`unseenOverwrite: true`; a freeze must be lifted by an `approver` before it can commit
([execution and recovery §6.4](execution-recovery.md#64-revert); `docs/operator/walkthrough.md:340-353`).
It binds the drifted digest, so if the machine drifts again first the plan fails and must be
made again.

### 5.9 Recovery after a restore

```text
$ bronzewardctl status
Recovery mode: on since 10:42 (epoch ep_r2l…). Scopes: 1 pre-restore-unaccounted, 1 unresolved, 2 ready.
Server: started for recovery (GET /server; G14). Plan creation, approval and unfreeze are refused. Imports
  complete here; publications are accepted but complete only after a normal restart (no
  publish worker runs). Recovery mode stays on after that restart.
Drafts can be edited. No scope can commit, attempt or adopt until it is released; the
  pre-restore-unaccounted scope also refuses planning and approval.
$ bronzewardctl recovery status
MACHINE         SCOPE STATE              NEXT
mch_k2p…7xqa    pre-restore-unaccounted  account the scope (recovery scope-account)
mch_tqhc…35uq   ready                    release (recovery-admin)
mch_w4ze…n3fa   ready                    release (recovery-admin)
mch_b3c…aa2q    unresolved               restored op_q8r…t2na holds it: classified after the
                                         normal restart, then re-mark
$ bronzewardctl recovery scope-account mch_k2p…7xqa --start obs_a… --end obs_b…
Shows: restore stopped at 10:31. Start must be the first successful read after the latest of
that stop plus the maximum transport deadline and any restored unaccounted attempt's deadline;
end must be a recovery observation begun at least the settle floor after the latest of that
point and the start. Settle floor 30s and maximum transport deadline 2m, as `GET /server`
serves them (G17).
Resource version read at both ends, unchanged between them.
This records your judgment that no request sent before the restore, from any attempt the
restored journal shows or one it lost, can still land on this machine. It may already have
landed; nothing here shows whether it did. The settle floor is a minimum wait, not a proven
bound: a request landing after this can overwrite work admitted once the scope is released,
and would be seen only as drift at a later observation.
Explanation (required if the resource version moved): <empty>
Type the machine ID to record this accounting: mch_k2p…7xqa
Recorded. Scope mch_k2p…7xqa is now blocked until it is marked.
```

Scope accounting is step 1's decision: it accounts every unaccounted attempt on the machine at
once, and is refused on a scope that is not pre-restore unaccounted. The scope then becomes
`unresolved` if a restored operation still holds it, and otherwise `blocked` until it is marked
(`internal/api/recovery_accountings.go:143-226`;
[execution and recovery §7.3](execution-recovery.md#73-procedure), step 1). An `unresolved`
scope is not accounted again. A restored operation with no attempt is ended `cancelled` by the
recovery start itself, sending nothing, once the scope is accounted; one with an attempt, as
here, waits for the normal restart's executor to classify it. The scope is then re-marked
(`internal/api/recovery_reads.go:78-108`;
[§7.3](execution-recovery.md#73-procedure) step 5,
[§7.4](execution-recovery.md#74-scope-states-in-recovery)). A `recovery-admin` resolution is
the manual path for an operation with no attempt only. Both accountings, this
one and the per-attempt accounting of a lost response outside a restore (`recovery account`,
[§5.7](#57-operation-tracking)), are operator judgments with the same named settle refusals: a
start that is not the first read after the deadline, an end that is not a recovery observation,
a resource version not read at both ends, an end before the settle floor, or a moved resource
version without an explanation (`internal/api/settle.go:111-154`). The settle-floor refusal
states the floor and the point it counts from (`internal/api/settle.go:148-150`). Since
[#278](https://github.com/ginsys/bronzeward/issues/278), `GET /server` serves the floor and the
maximum transport deadline as values before any decision, on a recovery start once its entry has
committed, so the client shows both before the first one (G17). After a successful accounting its timeline entries carry `facts.settleFloor` and `facts.anchor`
(`internal/api/settle.go:160-161`, `internal/api/recovery_accountings.go:209-211`,
`internal/api/recovery_accountings.go:230-231`). **Proposed:** the end-observation choice offers only observations
with purpose `recovery`, and an observation without a `resourceVersion` is shown unusable at
either end (`internal/api/observations.go:23`, `internal/api/observations.go:37`). The per-attempt
accounting also refuses an attempt already accounted and an executor not stopped or taken over
(`internal/api/settle.go:50-67`;
[execution and recovery §5.2](execution-recovery.md#52-accounting-a-lost-response)). The
decision records a judgment, not evidence that the request can no longer land, and the residual
late-landing risk stays explicit there. Marking, releasing a scope and exiting are separate
acts, and a scope release is not an approval
([execution and recovery §7.3](execution-recovery.md#73-procedure)). **Proposed:** no client
offers a generic "retry" or "mark successful" button, and none infers safety from a matching
digest.

Recovery mode is enforced per scope, not installation-wide
([execution and recovery §7.5](execution-recovery.md#75-what-recovery-mode-allows),
`docs/spec/execution-recovery.md:1472-1490`): acts that only remove authority (revocations, plan
cancellation, freeze) and the recovery acts are always allowed to their roles; drafts,
ingestion and compilation are allowed everywhere, and so is publication, except that a
publication changing the assignment of a machine whose scope is not released in the current
epoch fails `recovery-mode-active`, or `scope-busy` first when an operation holds that scope
(`internal/api/publish.go:470-475`;
[persistence and API §12.2](persistence-api.md#122-entry)); plan creation, approval, unfreeze
and adoption are refused on a scope still pre-restore unaccounted; commitment, attempts and
adoption records are refused on every scope not released in the current epoch; a released scope
works as outside recovery mode. A scope `blocked` by a lost key version leaves that state through
a newly published release, so drafting, importing and publishing must stay reachable
([§7.4](execution-recovery.md#74-scope-states-in-recovery)); that publication must leave the
unreleased scope's assignment unchanged, or it fails as above. Separately, the process started
for recovery serves only liveness and the entry act before entry; after entry it serves the
recovery routes, the authority-removing acts and the installation-wide acts, but refuses plan
creation, approval and unfreezing. It runs no publish worker, adopter, drift detector or
executor, so a publication it accepts completes only after the normal restart, while an import
runs at once on this process (`internal/api/api.go:190-198`, `internal/api/runner.go:28-37`). The operator may restart it normally once entry has
committed, and recovery mode continues
(`internal/api/routes.go:18`, `internal/api/routes.go:120-127`;
`docs/spec/execution-recovery.md:1286-1298`, `docs/spec/execution-recovery.md:1337-1342`). Entry is available only on a server started for
recovery (`cmd/bronzeward/main.go:132-134`). **Proposed:** both clients show which restriction
applies to an action, the scope's or the process's, and the clients say so when entry is
refused.

## 6. Web interface

**Proposed** navigation ([§12](#12-alternatives-and-recommendations), C2): a persistent header
with context, identity, roles, epoch and a recovery-mode banner; a left navigation of
**Machines**, **Changes**, **Plans**, **Operations**, **Sources**, **Audit** and, for a
`recovery-admin` or during recovery mode, **Recovery**. The machine page is the hub; a change
moves through separate pages for draft, release, plan and approval, each with its own button and
role.

| Screen | Wireframe | Main routes |
| --- | --- | --- |
| Sign-in and context | [W1](operator-experience-wireframes.md#w1-sign-in-and-context) | (#253) |
| Machines | [W2](operator-experience-wireframes.md#w2-machines) | `GET /machines`, `GET /clusters` |
| Machine detail | [W3](operator-experience-wireframes.md#w3-machine-detail) | `GET /machines/{id}`, `/observations`, `/timeline` |
| Draft editor | [W4](operator-experience-wireframes.md#w4-draft-editor) | `GET /drafts/{id}`, draft `PUT`/`DELETE`, discard |
| Protected review | [W5](operator-experience-wireframes.md#w5-protected-import-review) | `GET /ingestions/{id}/review`, marks, continuations |
| Release and rendered review | [W6](operator-experience-wireframes.md#w6-release-and-rendered-review) | `GET /releases/{id}`, `/machines/{m}/review` |
| Plan and approval | [W7](operator-experience-wireframes.md#w7-plan-and-approval) | `GET /plans/{id}`, approvals, cancellations |
| Operation timeline | [W8](operator-experience-wireframes.md#w8-operation-timeline) | `GET /operations/{id}`, `/events` |
| Drift decision | [W9](operator-experience-wireframes.md#w9-drift-decision) | machine, freezes, ingestions, plans |
| Recovery console | [W10](operator-experience-wireframes.md#w10-recovery-console) | `GET /recovery`, recovery routes |
| States | [W11](operator-experience-wireframes.md#w11-empty-withheld-error-and-offline-states) | all |

The fragment editor edits native YAML ([§12](#12-alternatives-and-recommendations), C3), with
structured controls only for the layer, declarations, a profile's ordered list and an
assignment's selections.

## 7. Role journeys

Each journey is a sequence a usability task in [§13](#13-usability-and-accessibility-acceptance)
tests. "Sees" lists what must be visible before the step's action is enabled.

**Viewer.** Sign in, pick the context, open Machines, find the drifted machine, read its
desired, applied and observed state with observation time, open the timeline, open the plan and
approval that last applied it, read the audit acts. From the roles `GET /identity` serves (G1),
every action button is absent or disabled with "needs author" or a similar reason
([§3](#3-roles-humans-and-automation)); an action the viewer takes anyway shows the server's `403`
with the roles the route accepts. Restriction: nothing secret is served to any role
except the protected review, which is author-only.

**Author.** Record a cluster and its machines (human); import with review (human), mark missed
secrets, continue; create a draft; edit fragments, profiles and assignments; resolve a 412 by
reading the other change; discard a draft; freeze a machine. Sees: draft revision and which
entries changed against their base; every `!bwref` with its declaration; "not rendered until
published". An automation author can edit drafts but never reaches the ingestion routes, cluster
or machine records (`internal/api/routes.go:98-106`).

**Publisher.** Publish a draft and wait for the operation; read the release's source revisions
and each machine's rendered review; create an apply or adopt plan; cancel a plan it created.
Sees: that publication makes the release `Desired` and sends nothing; the plan's evidence before
the plan leaves its hands. Automation may publish and plan (`internal/api/routes.go:119-120`).

**Approver.** Open a proposed plan; read target machine, route, operation, mode, release,
assignment revision, baseline, drift record, deadlines, expiry, epoch, evidence, self-approval
reasons and `unseenOverwrite`; approve by typing the plan ID; watch the operation; revoke an
approval or cancel a plan; unfreeze. Human only for approval. Sees: that revoking after the
controller committed does not stop an attempt already recorded
([design §13.7](../design/Talos_Configuration_and_Machine_Management_Design.md#137-poc-identity-and-approval-policy)).

**Recovery administrator.** See the recovery banner; read `GET /recovery` scope states; take
over an operation whose owner is stopped; account an attempt with two named observations and a
written explanation where required; resolve an attempt-less `unresolved` operation as
cancelled; account, mark and release scopes; exit; revoke an identity after adding it to the
deployment's denied subjects. Human only. Sees: the settle floor described as a minimum wait,
not a bound, with its value and the maximum transport deadline as `GET /server` serves them
(G17);
a mark's `missing[]` list before a release (`internal/api/recovery_marks.go:195-213`).

**Automation.** A CI job with an automation token: create a draft, set fragments from files,
publish, create a plan, then stop. It cannot import, approve, recover or record inventory. The
CLI's non-interactive mode (`--no-input`) never prompts; an approval attempt is warned before
sending with "approval is for humans", and the server refuses it `403` with `humanOnly`
(client contracts AUTH-04).

## 8. States every view must handle

| State | Cause | CLI | Web |
| --- | --- | --- | --- |
| Loading | request in flight | spinner on a terminal only | skeleton and an `aria-live` "Loading machines" |
| Empty | list with no items | "No drafts yet. Create one: …" | empty state with the first action |
| Withheld | review `configuration: null` | the notice, never an empty document | [W11](operator-experience-wireframes.md#w11-empty-withheld-error-and-offline-states) |
| Unread or stale observation | `unread` causes, old `at` (`internal/api/observe.go:56-198`) | cause per field ("node unreachable", "provider access denied") | same, never green |
| Refused | `application/problem+json` ([persistence and API §9.4](persistence-api.md#94-errors)) | title, code, what to do, `instance` | inline at the control, summary at the top |
| Conflict | `412 precondition-failed`, `409 conflict` | stop and point at the newer state; a `409` naming the ingestion still in progress under the same key instead waits and repeats with that key ([persistence and API §7.2](persistence-api.md#72-behavior)); a refusal stored after the claim also names its ingestion (`internal/api/fragments.go:289-296`) and its first answer is unmarked (`internal/api/tx.go:111`), so an unmarked one may be either, and one same-key repeat settles it: a stored refusal then comes back marked `Idempotent-Replayed` and is final | show both and offer reload; never auto-merge |
| Ambiguous response | timeout or connection loss after sending | before exiting 3, offers to repeat the request it still holds in memory with the same key and its expected epoch; after exit, prints the key and the resource to read | "Unknown whether this was saved"; [Repeat exactly] only while the page holds the request, for the same sign-in and epoch |
| Replayed | `Idempotent-Replayed: true` (`internal/api/idempotency.go:81-87`) | "Already done earlier; showing that result" | same |
| Key reused | `422 idempotency-key-reused` | the request could not be matched to the earlier one under that key (it differed, or the recorded digest-key version can no longer be computed, [persistence and API §7.1](persistence-api.md#71-the-key)); nothing was done; retry under a new key | same |
| Offline | no connection | retry reads with backoff; never queue writes | banner; actions disabled; last-read time shown |
| Authentication refused | `401 unauthenticated`: not accepted, possibly but not necessarily because it expired | renew and repeat reads; repeat a pending write with the same key only if the renewed token is the same principal and the epoch is unchanged | same |
| Principal or epoch changed with a write pending | renewal returned another principal, a context switch, or a new `Bronzeward-Epoch` | the pending write is set aside: not repeated and not sent under a new key; the client reads the current resource and the operator decides afresh | same, with the pending write shown as "outcome unknown: it may already have been committed", and its repeat disabled |
| Recovery mode | `Bronzeward-Recovery-Mode: true`, `409 recovery-mode-active` naming the scope | banner line on every command with the scope summary | persistent banner; an action is disabled only where its scope's state or the recovery-start process refuses it, with that reason ([§5.9](#59-recovery-after-a-restore)); the start mode is read from `GET /server` (G14), whose refusal before a recovery start's entry, a `409 recovery-mode-active` without a `scope` member, itself shows a recovery start; until that read answers, the mode is also known from the last answer of plan creation, approval or unfreeze, which a recovery start refuses without a `scope` member, or of recovery entry, which a normal start refuses with a `409 conflict` carrying neither `epoch` nor `request`; the entry's `201` and its other `409 conflict` answers carry `epoch` and show a recovery start ([client contracts §8.5](client-contracts.md#85-epoch-changes)) |
| Epoch changed | new `Bronzeward-Epoch`, `400 cursor-invalid`, SSE `reset` | discard cached pages and cursors, say why | same |
| Unresolved operation | `state: unresolved` | exit 5; who must act | the operation and machine both show it; no retry button |
| Human-only refusal | `403 forbidden`, `humanOnly: true` | "This needs a person signed in, not an automation token" | same |

Idempotency facts: a key is scoped to the authenticated principal, so another principal's use
of it is a new request; the fingerprint covers the method, route template, path parameters,
`If-Match` and the canonical body. The same key with the same fingerprint replays the stored
answer in the current epoch, a different fingerprint is `422`, a claim still in progress under
the key is `409` naming its ingestion (`internal/api/fragments.go:113-114`), a record from an earlier epoch is
`409`, and after a restore that removed the record the same key executes afresh, unless its
`Bronzeward-Expected-Epoch` names an earlier epoch, which is refused `412 epoch-mismatch`
([persistence and API §7.1](persistence-api.md#71-the-key), §7.2; `internal/api/idempotency.go:52-58`;
`docs/spec/persistence-api.md:1243-1253`, `docs/spec/persistence-api.md:1351-1370`,
`docs/spec/persistence-api.md:1372-1381`;
`internal/api/idempotency.go:63-87`). The same key does not make a remote mutation exactly-once
([design §12.5](../design/Talos_Configuration_and_Machine_Management_Design.md#125-durable-operations-and-uncertain-outcomes)).

**Proposed:** a client keeps a key per write until it has an answer. A repeat with that key is
offered only for the exact request it still holds in memory, for the same principal and epoch.
Once the process ends or the page is left, the client can no longer show that a request is
identical: a file supplied again may have changed, and a changed file is a new request. It then
offers no ordinary repeat; it prints the key and reads the current resource, and the operator
decides afresh. On a principal, context or epoch change it sets pending writes aside and never
silently creates a new key for them. It never writes a request body, or a hash of a low-entropy
secret, to disk to repeat it later. Whether any key metadata is kept across a restart, and how
it is compared, is #253's to decide.

An automation token is bound to the epoch it was issued in and refused `401` in any
other (`internal/api/api.go:310-316`), so after a restore followed by a recovery-mode entry its
repeat is not executed; it is set aside like any write pending across an epoch change. After a
restore nobody entered, no new epoch exists and a pre-restore token can pass
([persistence and API §12.1](persistence-api.md#121-the-epoch), the residual of design §14.6). A human session's write
carries the epoch it last saw in `Bronzeward-Expected-Epoch`, which the write's own transaction
checks when its key has no record (`internal/api/idempotency.go:52-58`,
[#293](https://github.com/ginsys/bronzeward/issues/293)). If a write commits, its answer is lost
and a restore then removes its record, the repeat is refused `412 epoch-mismatch` once an entry
has minted a new epoch, instead of executing afresh, and is set aside the same way. The recovery
entry, which mints the epoch, is sent without the header
([client contracts §8.4](client-contracts.md#84-idempotency-and-ambiguous-responses)), and the
restore nobody entered stays the same residual
([persistence and API §12.1](persistence-api.md#121-the-epoch)).

## 9. Secret handling in the clients

**Proposed** journey requirements. The mechanisms are #253's.

1. No value is ever put in argv by a documented journey: fragment documents, ingestion
   documents and tokens come from a file, stdin, a prompt or the browser. Marks come only from
   a prompt, stdin or the browser: a mark's path can name a secret mapping key, and the
   server holds marks in the serving process only, never stored
   ([compilation §3.6](compilation.md#36-operator-review), item 3). A client-read marks file
   would be a stored copy; an operator who pipes marks from a file owns that copy.
2. The protected review is shown and discarded: nothing the client controls keeps it, so no
   temporary file, editor swap file, shell history, log, crash report, telemetry, clipboard
   helper, browser storage, service worker cache, HTTP cache, request journal or export. It has
   no JSON, YAML, save or non-terminal output form. The server's `no-store`
   (`internal/api/review.go:30-31`) is necessary, not sufficient.
3. This covers client-controlled persistence only. Terminal scrollback, session recording, a
   host crash dump, screen capture and an operator copying text by hand are outside the
   client's control, and the clients say so rather than claim the review is unrecorded. The
   mechanism is qualified in #253 against [compilation §3.6](compilation.md#36-operator-review).
4. Copy-to-clipboard and "download" are absent on the protected review and on any field showing
   a redaction token's neighbourhood.
5. Rendered reviews, plan diffs and provenance are redacted by the server and may be cached like
   other reads; the clients do not undo redaction or ask for values.
6. Error messages repeat the server's problem without request bodies; a refused mark is
   identified by position only, as the server does.
7. Debug output (`-v`) prints methods, paths, status and request IDs, never headers carrying
   credentials or bodies of keyed routes.

## 10. Action, route and role matrix

Every client action above maps to one existing route, or is a composite of them, or is a gap.
Routes are under `/api/v1` (`internal/api/routes.go:62-136`).

| Action (CLI) | Route | Roles | Human only | If-Match |
| --- | --- | --- | --- | --- |
| reads (`list`, `show`, `history`, `timeline`, `observations`, `act list`, `operation show`) | `GET` resource routes | any | no | no |
| `operation watch` | `GET /operations/{id}/events` (SSE) | any | no | no |
| `cluster add` | `POST /clusters` | author | yes | no |
| `machine add` | `POST /machines` | author | yes | no |
| `machine set-endpoint` | `POST /machines/{id}/talos-endpoints` | author | yes | no |
| `import start` | `POST /ingestions` | author | yes | draft's |
| `import review` | `GET /ingestions/{id}/review` | author | yes | no |
| `import mark` / `continue` / `takeover` / `abandon` | `POST /ingestions/{id}/marks` / `continuations` / `takeovers` / `abandonments` | author | yes | no |
| `import status` | `GET /ingestions/{id}` | any | no | no |
| `draft create` | `POST /drafts` | author | no | no |
| `fragment set` / `rm`, `profile set` / `rm` | `PUT` / `DELETE /drafts/{id}/fragments/{name}`, `/drafts/{id}/profiles/{name}` | author | no | yes |
| `assignment set` / `rm` | `PUT` / `DELETE /drafts/{id}/assignments/{machine}` | author | no | yes |
| `draft discard` | `POST /drafts/{id}/discard` | author | no | yes |
| `publish` | `POST /drafts/{id}/publications` | publisher | no | yes |
| `release review` / `provenance` | `GET /releases/{id}/machines/{m}/review` | any | no | no |
| `plan create`, `drift revert` | `POST /plans` | publisher | no | no |
| `plan cancel` | `POST /plans/{id}/cancellations` | publisher (own), approver, recovery-admin | no | no |
| `approve` | `POST /plans/{id}/approvals` | approver | yes | no |
| `approval revoke` | `POST /approvals/{id}/revocations` | approver, recovery-admin | no | no |
| `machine freeze` | `POST /machines/{id}/freezes` | author, publisher, approver, recovery-admin | no | no |
| `machine unfreeze` | `POST /machines/{id}/unfreezes` | approver | no | no |
| `drift adopt` | composite: `POST /ingestions` (drift-adoption), then `publish`, `POST /plans` (adopt) | author, then publisher, then approver | ingestion and approval | as parts |
| `recovery status` | `GET /recovery` | any | no | no |
| `recovery enter` | `POST /recovery/entries` | recovery-admin | yes | no |
| `recovery exit` | `POST /recovery/exits` | recovery-admin | yes | no |
| `recovery mark` / `release` | `POST /recovery/scopes/{machine}/marks` / `releases` | recovery-admin | yes | no |
| `recovery scope-account` | `POST /recovery/accountings` | recovery-admin | yes | no |
| `recovery account` | `POST /operations/{id}/attempts/{attempt}/accountings` | recovery-admin | yes | no |
| `recovery takeover` / `resolve` | `POST /operations/{id}/takeovers` / `resolutions` | recovery-admin | yes | no |
| `identity revoke` | `POST /identity-revocations` | recovery-admin | yes | no |
| `login`, `logout`, `context` | none: client and identity provider (#253) | — | — | — |
| `whoami`, `status` (roles) | `GET /identity` | any authenticated credential, none needed | no | no |
| `status` (start mode, server settings), the version check of a command's first request in a context (not the `context use` command) | `GET /server` | any | no | no |

Every mutating route requires an `Idempotency-Key`
([persistence and API §7](persistence-api.md#7-idempotency)). Plan creation and approval, and
unfreeze, are refused by a server started for recovery, even after recovery entry
(`internal/api/routes.go:18`, `internal/api/routes.go:120-127`).

## 11. API gap audit

Gaps found against the current routes and schemas. None is invented as available above; #253
decides whether each becomes a separately refined API issue, a client-side composite or an
accepted limitation.

| ID | Gap | Journey affected | Current workaround |
| --- | --- | --- | --- |
| G1 | No identity, role or capability read; a 403 body lists only the roles the route accepts, never the caller's (`internal/api/api.go:380-392`) | showing who is signed in and which actions are allowed | served since [#277](https://github.com/ginsys/bronzeward/issues/277): `GET /identity` ([persistence and API §9.2](persistence-api.md#92-resources-and-routes)) |
| G2 | No version or compatibility read; only unauthenticated `GET /livez` (before [#278](https://github.com/ginsys/bronzeward/issues/278): `internal/server/server.go:19-21`) | client/server compatibility, context setup | served since [#278](https://github.com/ginsys/bronzeward/issues/278): `GET /server` serves the version and the API revision ([client contracts §7](client-contracts.md#7-compatibility-and-versioning)) |
| G3 | An operation does not name its ingestion, and no ingestion listing exists (G10); the ingestion names its operation (`internal/api/ingestions.go:235`), not the reverse before [#280](https://github.com/ginsys/bronzeward/issues/280) ([`internal/api/reads.go:323-328`](https://github.com/ginsys/bronzeward/blob/23c1bc2a233cbdf0916076e62016a82892107053/internal/api/reads.go#L323-L328)) | resuming an import after the client lost the 202 | served since [#280](https://github.com/ginsys/bronzeward/issues/280): an `ingest` operation's `subject` names its `ingestion` (`internal/api/reads.go:396-398`), and `GET /ingestions` lists them (G10) |
| G4 | A draft does not name its release; releases have no draft filter (`internal/api/inventory.go:349-356`, `internal/api/releases.go:215`) | "what did this draft publish?" | the publish operation's `result.release` while the client holds it; otherwise paging releases and matching `draft` (`internal/api/releases.go:50-51`), the client-side scan G9 accepts |
| G5 | No render of a draft before publication | authors cannot see a machine's configuration before publishing | publish, then review |
| G6 | Provenance covers reference occurrences only (`internal/api/releases.go:78-100`), not "which fragment set this field" ([design §6.2](../design/Talos_Configuration_and_Machine_Management_Design.md#62-fragments-profiles-and-assignments)) | field-origin inspector | none |
| G7 | Drift content is not served: no observed configuration or digest (`internal/api/reads.go:155-171`) | deciding adopt or revert from what changed | a drift-adoption import with review, or `talosctl` outside Bronzeward |
| G8 | Machines have no human-readable name (`internal/api/inventory.go:177-224`) | finding a machine | endpoint and node ID |
| G9 | No list filters on machines, plans, releases, drafts or acts (before [#280](https://github.com/ginsys/bronzeward/issues/280): [`internal/api/reads.go:78-80`](https://github.com/ginsys/bronzeward/blob/23c1bc2a233cbdf0916076e62016a82892107053/internal/api/reads.go#L78-L80); `internal/api/acts.go:72`); operations filter by `plan`, `machine`, `state`, `kind` (`internal/api/operations.go:97`); no machine timeline stream | "plans awaiting my approval", per-cluster views, following a timeline | served since [#280](https://github.com/ginsys/bronzeward/issues/280): plans by `state` and `machine` (`internal/api/plans.go:429-451`), machines by `cluster` (`internal/api/reads.go:199-218`); client-side paging for releases, drafts and acts; polling |
| G10 | No listing of ingestions: only `GET /ingestions/{id}` (before [#280](https://github.com/ginsys/bronzeward/issues/280): [`internal/api/routes.go:90`](https://github.com/ginsys/bronzeward/blob/23c1bc2a233cbdf0916076e62016a82892107053/internal/api/routes.go#L90)) | finding a paused or lapsed import | served since [#280](https://github.com/ginsys/bronzeward/issues/280): `GET /ingestions`, filtered by `state` and `machine` (`internal/api/ingestions.go:280-299`) |
| G11 | Recovery marks, releases and accountings have no list route; `GET /recovery` shows only their resulting scope states (`internal/api/recovery_exits.go:123-148`), the records themselves appear in machine timelines and acts | recovery console history | timeline |
| G12 | Browser use: no statement of CORS, origin checks or hosting of a web client | the web interface at all | #253 |
| G13 | Before [#293](https://github.com/ginsys/bronzeward/issues/293), a human session's mutation took no expected-epoch precondition: only `Idempotency-Key` and `If-Match` were checked, and the epoch was reported only in an answer. After a committed write's answer is lost and a restore removes its record, the same request with the same key executed afresh in the new epoch ([persistence and API §7.2](persistence-api.md#72-behavior)); a read before repeating left a race. An automation token is bound to its issuing epoch and refused `401` in any other (`internal/api/api.go:305-311`) | repeating an ambiguous write ([§8](#8-states-every-view-must-handle), C5, G3, [W11](operator-experience-wireframes.md#w11-empty-withheld-error-and-offline-states)) | closed by #293: the client sends `Bronzeward-Expected-Epoch`, and a repeat whose key has no record is refused `412 epoch-mismatch` once the epoch moved ([client contracts](client-contracts.md#4-api-gap-audit) G21) |
| G14 | No read of the process's start mode (before [#278](https://github.com/ginsys/bronzeward/issues/278)): `GET /recovery` serves epoch, recovery mode, entry, exit and scopes (`internal/api/recovery_exits.go:123-129`), the headers carry only `Bronzeward-Epoch` and `Bronzeward-Recovery-Mode` (`internal/api/api.go:326-332`). A recovery start shows only in an answer: a `409 recovery-mode-active` without a `scope` member (`internal/api/api.go:242-244`, `internal/api/recovery_exits.go:211-213`), or any recovery-entry answer past the role check but its not-a-recovery-start refusal, of which its `201` and its other `409 conflict` answers carry `epoch` (`internal/api/recovery_entries.go:77`); a normal start shows in that refusal, a plain `409 conflict` with neither an `epoch` nor a `request` member (`internal/api/api.go:356-359`, `internal/api/recovery_entries.go:91-92`, `internal/api/recovery_entries.go:106-108`, `internal/api/recovery_entries.go:133-135`, `internal/api/idempotency.go:65-67`) | the recovery banner and `status` stating that plan creation, approval and unfreeze wait for a normal restart ([§5.9](#59-recovery-after-a-restore)) | the answers of those three routes: past the role check, a recovery-start refusal (no `scope` member) shows a recovery start (`internal/api/api.go:339-353`); a `409 conflict` to recovery entry shows a normal start only without `epoch` and `request` members, and the client tells it apart from the entry's other `409 conflict` answers by those members, never by status and code alone ([client contracts §8.5](client-contracts.md#85-epoch-changes)); before [#278](https://github.com/ginsys/bronzeward/issues/278), nothing showed it before one was sent, so actions could not be disabled beforehand and the banner held until the next such answer. Served since [#278](https://github.com/ginsys/bronzeward/issues/278) except before a recovery start's entry: `GET /server` serves `startMode`, and is refused as every request but the entry until the entry commits; in that window its own refusal, a `409 recovery-mode-active` without a `scope` member, shows a recovery start, as those answers do |
| G15 | No principal display name: bodies carry principal identifiers only (`internal/api/acts.go:19-30`, `internal/api/releases.go:53`), and no route maps one to a subject or name | showing who published, planned, approved or entered recovery | shortened principal identifiers, until the view resolves them with `GET /principals`, served from API revision 4 ([client contracts](client-contracts.md#14-proposed-api-gap-issues-and-downstream-refinement) A7, [#335](https://github.com/ginsys/bronzeward/issues/335)) |
| G16 | No convergence status: a machine body serves `desired` and `applied.release` but not whether the `Desired` release's artifact digest for the machine differs from the `Applied` digest (before [#280](https://github.com/ginsys/bronzeward/issues/280): [`internal/api/reads.go:155-167`](https://github.com/ginsys/bronzeward/blob/23c1bc2a233cbdf0916076e62016a82892107053/internal/api/reads.go#L155-L167)), which is what pending convergence means ([execution and recovery §1](execution-recovery.md#1-supported-operation-and-state-values)); after an adoption the `Applied` digest is the baseline's ([execution and recovery §6.3](execution-recovery.md#63-adopt)), so equal release IDs do not rule it out | showing whether a machine still needs a plan ([§2.4](#24-desired-applied-and-observed), [§5.2](#52-inventory), [W2](operator-experience-wireframes.md#w2-machines)) | served since [#280](https://github.com/ginsys/bronzeward/issues/280): the machine's `pendingConvergence`, which serves neither digest (`internal/api/reads.go:155-171`) |
| G17 | Before a decision no route serves the settle floor, and no route serves the maximum transport deadline at all; the accounting journey needs both before a decision: `SettleFloor` defaults to 30 s and may be configured higher, `MaxTransportDeadline` is required deployment configuration with no default (`internal/config/config.go:134-135`, `internal/config/config.go:161-164`), and neither is on `GET /recovery` (`internal/api/recovery_exits.go:123-148`). After a decision, a successful accounting's entries carry `facts.settleFloor` and `facts.anchor` on the machine timeline and operation events (`internal/api/settle.go:160-161`, `internal/api/recovery_accountings.go:209-211`, `internal/api/recovery_accountings.go:230-231`). Separately, on a normal start a plan-creation refusal states the maximum at any time, before a decision too; a recovery start refuses plan creation before that check (`internal/api/plans.go:113-114`, `internal/api/routes.go:120`, `internal/api/api.go:350-352`). The scope-accounting anchor is the later of the restore stop plus that maximum and any restored unaccounted attempt's transport deadline (`internal/api/recovery_accountings.go:85-87`, `internal/api/recovery_accountings.go:143`, `internal/api/recovery_accountings.go:188-189`), so a client cannot compute it | the recovery accounting journey: when the end observation may begin ([§5.9](#59-recovery-after-a-restore), [W10](operator-experience-wireframes.md#w10-recovery-console)) | served since [#278](https://github.com/ginsys/bronzeward/issues/278): `GET /server` serves the settle floor and the maximum transport deadline, after a recovery start's entry too; the refusal text still states the floor and the point it counts from after a failed decision (`internal/api/settle.go:148-150`) |

Not gaps, by design: no client dispatch or apply route
([design §12.7](../design/Talos_Configuration_and_Machine_Management_Design.md#127-application-approval-and-dispatch-boundary));
no automation-token administration through the API
([persistence and API §10.2](persistence-api.md#102-automation-bronzeward-issued-tokens)); no
general secret browser (milestone exclusion).

## 12. Alternatives and recommendations

| ID | Choice | Options | Recommendation and reason | Status |
| --- | --- | --- | --- | --- |
| C1 | CLI vocabulary | noun-verb (`plan create`); verb-noun (`create plan`); workflow verbs only | Noun-verb plus `publish` and `approve` as top-level verbs: matches the API resources and `kubectl`/`talosctl`, keeps help discoverable, and keeps the three distinct steps as distinct words | accepted; usability unmeasured |
| C2 | Web navigation | resource tabs only; a single change wizard; machine hub plus a staged change pipeline | Hub plus pipeline: operators start from a machine's state, while publish, plan and approve stay separate pages, buttons and roles, which a wizard would blur | accepted; usability unmeasured |
| C3 | Editing | native YAML editor; forms over the Talos schema; both | Native YAML for documents and structured controls for metadata only: the design makes native Talos configuration canonical ([design §6.1](../design/Talos_Configuration_and_Machine_Management_Design.md#61-canonical-model)), and a schema form would be a second model to keep in step with every Talos version | accepted; usability unmeasured |
| C4 | Protected review in the CLI | terminal pager in process; temporary file in `$EDITOR`; web-only review | Terminal pager in process; non-terminal output refused with no override; no JSON, YAML, save or export form; marks by prompt or stdin, never a client-read file. An editor leaves swap and backup files the client cannot control ([compilation §3.6](compilation.md#36-operator-review)); web-only would make the CLI unable to finish an import. It limits client-controlled persistence only and does not prevent terminal or host capture ([§9](#9-secret-handling-in-the-clients)) | accepted as [client contracts §11.3](client-contracts.md#113-cli-review) qualifies it; no standard-output export form in any case |
| C5 | Concurrency and retries | last-seen ETag; re-read then write; journal of requests | Last-seen ETag per draft, stop on 412; repeat an ambiguous write with its key only on request, for the exact request held in memory, the same principal and the same epoch ([§8](#8-states-every-view-must-handle)), an epoch the write's own transaction checks against the one the client last saw (G13, closed by #293); no body journal: re-reading before writing is a silent overwrite, and a journal would store documents and marks | accepted, within the limits of §8; any persistence across restarts is #253's |

## 13. Usability and accessibility acceptance

**Proposed** acceptance tasks, run with synthetic data on the fixture, by participants who did
not write the clients, without the specifications, raw HTTP or a wrapper. Each task is run in
the CLI and in the web interface. A walkthrough by an agent does not count as a participant
([timeline assessment §3](../design/research/20261020-poc-timeline-assessment.md#3-criterion-verdicts-and-independent-review)).

| Task | Role | Done when | Wrong-belief probe |
| --- | --- | --- | --- |
| T1 Sign in, pick a context, say which roles you hold | any | correct context and roles stated | — |
| T2 Find the drifted machine and state its desired, applied and observed release and observation time | viewer | all four correct | "Is it healthy?" Expected: not established |
| T3 Import a machine with review, mark one unmarked secret, continue, then hand it over: assign it, publish the import draft, plan and approve the adopt | author, publisher, approver | import released; mark recorded; `Applied` set by the adoption | "Is the secret in the draft?" Expected: a reference only |
| T4 Change a node label for the workers and save it | author | draft entry saved | "Has any machine changed?" Expected: no |
| T5 Recover from a concurrent edit | author | other change read, own change re-applied | — |
| T6 Publish and find one machine's rendered configuration | publisher | review opened for that machine | "Are the sources the configuration?" Expected: no |
| T7 Explain an empty-looking and a redacted field in the review | publisher | each explained from the token or provenance | "Why is this empty?" Expected: answer from metadata, or "empty in the rendered review"; "empty in the source" only after reading that source revision |
| T8 Plan and approve the change; say what will change | publisher, approver | approved; change described from the diff | "Was the imported configuration changed?" Expected: only what the diff shows |
| T9 Follow the apply to completion through a disconnect | viewer | completion reported with time | — |
| T10 Freeze a drifted machine, then revert or adopt it | author to approver | drift closed as chosen | "Did detection revert it?" Expected: no |
| T11 Account a pre-restore-unaccounted scope in recovery, then mark and release it | recovery-admin | scope released with stated evidence | "Is release an approval?" Expected: no |

**Measures per task:** completed unaided (yes or no), time, number of refusals hit, help
lookups, and the answer to the probe. **Baseline friction**, counted from the current
walkthrough for T4, T6 and T8 together (its S2 and S3 sections, `docs/operator/walkthrough.md:177-243`):
14 hand-written HTTP requests, seven of them writes each with its own hand-chosen idempotency
key; four ETags captured by hand and passed as `If-Match` to four writes; one hand-repeated poll
for the publication; two `jq` extractions of a reference and its version from provenance before
the edit; and tokens for four roles. The acceptance target is proposed as: no HTTP, no hand-threaded
header, no `jq`, and every probe answered correctly by at least four of five participants. These
numbers are proposals for the owner to set, not measured results.

**User judgment points** the owner or a designated reviewer must assess, because a test cannot:
whether the status sentences in [§2.4](#24-desired-applied-and-observed) are understood as
intended; whether the approval screen shows enough to approve; whether the recovery console's
wording makes the accounting judgment clear without overstating its safety.

**Accessibility, web (proposed target WCAG 2.2 AA):** keyboard operation of every action
including the YAML editor and approval (2.1.1, 2.1.2); visible focus not hidden by the header or
banners (2.4.7, 2.4.11); logical focus order through the plan evidence (2.4.3); status and
operation progress announced without moving focus (4.1.3); errors identified in text at the
field (3.3.1); contrast of text and redaction badges (1.4.3, 1.4.11); reflow at 320 CSS pixels
without losing the diff (1.4.10); target size (2.5.8); colour never the only signal of
drift, freeze or failure (1.4.1). Check each with a keyboard-only pass and a screen-reader pass
on the tasks above. An automated checker or a reviewed wireframe does not establish conformance
([WCAG 2.2](https://www.w3.org/TR/WCAG22/)).

**Accessibility, CLI:** `NO_COLOR` and non-terminal output produce plain text with the same
information; no meaning is carried by colour or symbols alone; progress is line-based when not
on a terminal; `-o json` is stable for scripts and assistive tools.

## 14. Proposed refinement of the milestone tickets

This document edits no issue. Each journey stays covered by at least one implementation ticket
per client and by the validation ticket. #254 and #255 are refined against #253's contracts; any
other ticket still carrying `status/needs-refinement` is refined the same way before
implementation.

| Journey (section) | CLI | Web | API/contract | Validation |
| --- | --- | --- | --- | --- |
| Sign-in, context, roles (5.1) | [#254](https://github.com/ginsys/bronzeward/issues/254) | [#255](https://github.com/ginsys/bronzeward/issues/255) | #253: login, G1, G2 | [#263](https://github.com/ginsys/bronzeward/issues/263) T1 |
| Inventory and machine state (5.2, 2.4) | #254 | #255 | #253: G8, G9, G16 | #263 T2 |
| Protected import and review (5.3) | [#256](https://github.com/ginsys/bronzeward/issues/256) | [#257](https://github.com/ginsys/bronzeward/issues/257) | #253: review handling, G3, G10 | #263 T3 |
| Fragments, profiles, assignments (5.4) | #256 | #257 | #253: ETags, G5 | #263 T4, T5 |
| Publication, rendered review, provenance (5.5, 2.1, 2.2) | #256 | #257 | #253: G4, G6, G15 | #263 T6, T7 |
| Plan and approval (5.6) | [#258](https://github.com/ginsys/bronzeward/issues/258) | [#259](https://github.com/ginsys/bronzeward/issues/259) | #253: G9, G15 | #263 T8 |
| Operation tracking (5.7) | #258 | #259 | #253: events, epochs | #263 T9 |
| Drift (5.8) | [#260](https://github.com/ginsys/bronzeward/issues/260) | [#261](https://github.com/ginsys/bronzeward/issues/261) | #253: G7 | #263 T10 |
| Recovery (5.9) | #260 | #261 | #253: G11, G14, G15, G17 | #263 T11 |
| Packaging and documentation | [#262](https://github.com/ginsys/bronzeward/issues/262) | #262 | #253: compatibility | #263 |

Proposed changes: the web inventory ticket (#255) also carries the shared web shell (header,
banners, state catalogue of [§8](#8-states-every-view-must-handle)); and #253 records, for each
gap G1-G17, whether it becomes an API issue that natively blocks the client tickets it affects.

**Qualifications this document left to #253**, besides the gaps, and where the
[client contracts](client-contracts.md) settle them:

- the retry boundary of C5: whether the same-key repeat is offered at all (G13 closed by #293), whether any key
  or request may survive a client restart, and how an ambiguous outcome is shown after one
  ([client contracts §8.4](client-contracts.md#84-idempotency-and-ambiguous-responses), D9);
- the protected review of C4: the mechanism must be shown to keep plaintext out of argv, shell
  history, logs, editor files and caches, or be replaced
  ([client contracts §11.2](client-contracts.md#112-rules-for-both-clients),
  [§11.3](client-contracts.md#113-cli-review));
- sign-in, renewal, expiry and sign-out behind T1, and the 401, 403 and 503 states of
  [§8](#8-states-every-view-must-handle) ([client contracts §9](client-contracts.md#9-authentication),
  [§8.2](client-contracts.md#82-errors));
- hosting, origin and browser storage rules for the web client (G12)
  ([client contracts §10](client-contracts.md#10-browser-security),
  [§9.3](client-contracts.md#93-browser-sign-in), D1, D5).

## 15. Limits

- This document is accepted and partly implemented: [client contracts §15](client-contracts.md#15-assumptions-and-limits)
  names what bronzewardctl does so far. No participant has used either client; the tasks and targets in
  [§13](#13-usability-and-accessibility-acceptance) are unmeasured.
- Line references were read at `e1c8cb3` and move as code changes, except the expected-epoch
  check's, which are read with #293 applied (see the opening).
- It does not choose packaging targets. The sign-in flows, token storage and hosting are decided
  in [client contracts §13](client-contracts.md#13-alternatives-and-open-choices), and the
  implementation choices in its [§15](client-contracts.md#15-assumptions-and-limits): the CLI in
  Go, as a second binary `cmd/bronzewardctl`; the web client in TypeScript and React, built with
  Vite; and current desktop browsers as the tested targets, with mobile browsers untested.
- It does not change any role, human-only rule, route, schema or credential policy, and it does
  not decide #16, #265 or #266.
- Per-cluster role scoping, policy-based approval, `Ignore` drift and modes other than
  `no-reboot` are outside the PoC and are not designed here
  ([design §12.4](../design/Talos_Configuration_and_Machine_Management_Design.md#124-drift-policy),
  [design §13.7](../design/Talos_Configuration_and_Machine_Management_Design.md#137-poc-identity-and-approval-policy)).
