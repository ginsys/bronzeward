# Client contracts: authentication, API behaviour and compatibility

**Status: accepted** by the owner on 25 October 2026 at 21:15 CET, in the owner's workflow
session: "A: accept now". Option A accepted the client contracts as they stood at `daa4393`, as
assessed in the [acceptance assessment](https://github.com/ginsys/bronzeward/issues/253#issuecomment-6400565386).
This document specifies what any Bronzeward client, the
`bronzewardctl` command-line client and the web interface alike, must do against the existing
management API, for
[Specify client authentication, API behavior and compatibility](https://github.com/ginsys/bronzeward/issues/253)
in [04 - Operator experience: CLI and web UI](https://github.com/ginsys/bronzeward/milestone/4).
It implements no client, adds no route and changes no role, credential policy or server
behaviour. Each design decision it needs is listed in [§13](#13-alternatives-and-open-choices)
with its options and how it was decided: four by the lead, the other six by the owner. The
owner's choices of CLI language, frontend framework, reference identity provider and browser
support matrix are recorded in [§15](#15-assumptions-and-limits).

It builds on the operator journeys of [operator experience](operator-experience.md), proposed for
[Design the operator journeys for bronzewardctl and the web UI](https://github.com/ginsys/bronzeward/issues/252).
References of the form *journeys §8* and gap IDs `G1`-`G12` point at that document. Its
`G13`-`G16` appear here as `G21`-`G24`, and its `G17` is this document's `G18`; this document
adds `G13`-`G17`, `G19` and `G20`.

**Facts and proposals are kept apart.** A statement about existing behaviour cites the contract
section or the source line it comes from, read at revision `3cbe953` (`internal/api/api.go:382`
means that file and line), except the code
[#277](https://github.com/ginsys/bronzeward/issues/277) adds or changes, the identity read
(`internal/api/identity.go`, `internal/api/routes.go:97`, `internal/api/api.go:339`), which is
read with it applied, and the expected-epoch check
[#293](https://github.com/ginsys/bronzeward/issues/293) added, cited in G21's disposition, §8.4
item 3, D10 and A6, which is read with that change applied. A requirement this document proposes is
marked **Proposed**; *must*
in a Proposed item is a requirement on the clients once the proposal is accepted, not a claim
about code that exists. A behaviour the API does not support today is listed in
[§4](#4-api-gap-audit) and is never presented as available. Statements that rest on outside
standards or on reasoning without a citation are listed in [§15](#15-assumptions-and-limits).

## Contents

- [1. Scope and exclusions](#1-scope-and-exclusions)
- [2. The API a client meets](#2-the-api-a-client-meets)
- [3. Action, endpoint and role matrix](#3-action-endpoint-and-role-matrix)
- [4. API gap audit](#4-api-gap-audit)
- [5. Endpoint discovery and context selection](#5-endpoint-discovery-and-context-selection)
- [6. TLS trust](#6-tls-trust)
- [7. Compatibility and versioning](#7-compatibility-and-versioning)
- [8. Request handling](#8-request-handling)
- [9. Authentication](#9-authentication)
- [10. Browser security](#10-browser-security)
- [11. Protected input](#11-protected-input)
- [12. Shared conformance cases](#12-shared-conformance-cases)
- [13. Alternatives and open choices](#13-alternatives-and-open-choices)
- [14. Proposed API-gap issues and downstream refinement](#14-proposed-api-gap-issues-and-downstream-refinement)
- [15. Assumptions and limits](#15-assumptions-and-limits)

## 1. Scope and exclusions

In scope, as the issue states: an action-to-endpoint and role matrix with a gap audit against
the current routes and schemas; endpoint discovery and context selection, TLS trust, client and
server compatibility, errors, pagination, ETags, idempotency across ambiguous responses, epoch
changes and event resumption; OIDC sign-in, renewal and sign-out for the CLI and the browser,
token handling and automation token input; protected ingestion input and review handling; and
conformance cases both clients share.

Kept unchanged, as the issue requires:

- Bronzeward is an OAuth 2.0 resource server that verifies each request on its own and keeps no
  server session ([persistence and API §10.1](persistence-api.md#101-humans-oidc)).
- Five capability roles, human roles from identity-provider groups, automation limited to
  `viewer`, `author` and `publisher`
  ([design §13.7](../design/Talos_Configuration_and_Machine_Management_Design.md#137-poc-identity-and-approval-policy);
  `internal/auth/tokens.go:361-380`).
- Human-only routes refuse automation whatever its roles (`internal/api/api.go:382-383`).
- Automation tokens are issued, rotated and listed, and revoked without revoking their identity,
  only by the server-side command; no route does any of that
  ([persistence and API §10.2](persistence-api.md#102-automation-bronzeward-issued-tokens)). An
  identity revocation through the API revokes a service identity's token with it (same section;
  `internal/auth/identity.go:66-69`, reached from `internal/api/revocations.go:113`). The comment
  at `cmd/bronzeward/token.go:36-37` still claims no route revokes a token; it predates that and
  is stale. The revocation is the `identity revoke` action of
  [§3.2](#32-writes), a recovery-admin act, not token administration. Neither client offers token
  administration.

Excluded: a server session or backend-for-frontend, direct provider or Talos access from a
client, a new credential policy, the decisions of
[#265](https://github.com/ginsys/bronzeward/issues/265) and
[#266](https://github.com/ginsys/bronzeward/issues/266), and any client implementation.
[#162](https://github.com/ginsys/bronzeward/issues/162) concerns Talos access and does not supply
this client sign-in ([talosctl-oidc research, follow-ups and limits](../design/research/20261021-talosctl-oidc.md#follow-ups-and-limits));
its live observations of one identity provider are used below only as evidence about token
behaviour.

## 2. The API a client meets

The facts every later section relies on, read from the code and the contracts.

| Topic | Fact | Source |
| --- | --- | --- |
| Base path | Routes are `METHOD /api/v1<pattern>`; a request no route takes is `404 not-found`, after authentication, or `409 recovery-mode-active` before a recovery start's entry commits | `internal/api/api.go:230-236`, `internal/api/api.go:247-254` |
| Anonymous surface | `GET` and `HEAD /livez` answer `204` with no body and no authentication; every other request authenticates first, an unknown path and `OPTIONS` included | `internal/server/server.go:10-24`, `internal/api/api.go:260-272` |
| Transport | The server listens on plain http; TLS terminates in front of it | `internal/server/server.go:34-42`; [persistence and API §9.1](persistence-api.md#91-conventions) |
| Timeouts | Reading a request, body included, 30 s; headers 10 s; idle 2 min; no write timeout (the event stream outlives any fixed bound) | `internal/server/server.go:27-42` |
| Request body | Every `POST` and `PUT` sends one JSON object (`{}` when the route takes no member) with `Content-Type: application/json`, at most 1 MiB, unknown members refused, each violation `400 invalid-request`; a `DELETE` with a body is `400`, while its `Content-Type` is not checked (clients send none) | `internal/api/body.go:36-72`, `internal/api/drafts.go:44`; [persistence and API §9.1](persistence-api.md#91-conventions) |
| Success body | `application/json` | `internal/api/reads.go:35`, `internal/api/idempotency.go:85` |
| Errors | `application/problem+json` with `type` `urn:bronzeward:problem:<code>`, `title`, `status`, `instance` (the request ID, also logged), optional `detail` and extension members; `detail` never names a value | `internal/api/problem.go:76-98`; [persistence and API §9.4](persistence-api.md#94-errors) |
| Request ID | Every API answer, authenticated or not, carries `Bronzeward-Request-ID`, equal to the `instance` of a problem the request answers; a replayed stored problem keeps the `instance` of the request that stored it, while the header names the replaying request; liveness carries none | [persistence and API §9.1](persistence-api.md#91-conventions) |
| Credential | `Authorization: Bearer <token>`; a value starting `bwt_` is an automation token, anything else an OIDC access token | `internal/auth/auth.go:132-141`, `internal/auth/tokens.go:21` |
| Epoch headers | A response to an authenticated request carries `Bronzeward-Epoch`, and `Bronzeward-Recovery-Mode: true` while recovery mode was in effect, as the request read them: in the read that decided the answer where one did, otherwise in the installation-state read made once it authenticated; a committed recovery entry or exit names the epoch and mode it leaves in place, and an entry committing after the read can make a refusal name the epoch it replaced; `401`, `403 identity-revoked`, liveness and a `503` from authentication or from that installation-state read carry neither | `internal/api/api.go:267-283`, `internal/api/api.go:325-333`, `internal/api/problem.go:68-71`; [persistence and API §9.1](persistence-api.md#91-conventions) |
| Pagination | `limit` 1-500, default 50; opaque `cursor`; answer `{"items": [...], "next": "..."}` with `next` omitted on the last page; a cursor carries its epoch; one from another epoch, or one that does not parse, is `400 cursor-invalid`; any other query key is `400` | `internal/api/acts.go:33`, `internal/api/acts.go:135-160`, `internal/api/reads.go:59-63`, `internal/api/reads.go:92-112` |
| ETags | Strong `"<revision>-<token>"`, token replaced on every write; served on drafts and source heads; `If-Match` required on draft-scoped writes, publication and ingestion start; missing `428 precondition-required`, mismatch `412 precondition-failed`; no `If-None-Match` | `internal/api/routes.go:98-119`, `internal/api/api.go:407-416`; [persistence and API §4.1](persistence-api.md#41-etags) |
| Idempotency | Every `POST`, `PUT`, `DELETE` needs one `Idempotency-Key` of 16-128 `[A-Za-z0-9_-]` (`428 idempotency-key-required` if absent); scoped per principal; same request replays with `Idempotent-Replayed: true`; another request under the key is `422 idempotency-key-reused`; a record from an earlier epoch is `409 conflict`; with no record, a `Bronzeward-Expected-Epoch` naming another epoch is `412 epoch-mismatch` ([§8.4](#84-idempotency-and-ambiguous-responses) item 3, A6) | `internal/api/api.go:394-405`, `internal/api/idempotency.go:41-43`, `internal/api/idempotency.go:60-92`, `internal/api/idempotency.go:98-106`; [persistence and API §7.2](persistence-api.md#72-behavior) |
| Unknown commit | A commit whose outcome the server could not learn is `500 internal-error`, "retrying with the same Idempotency-Key answers it" | `internal/api/tx.go:312`; [persistence and API §9.4](persistence-api.md#94-errors) |
| Asynchronous work | Ingestion start, mark, continuation, takeover and publication answer `202` with an operation; plans and approvals `201` | [persistence and API §8](persistence-api.md#8-asynchronous-operations), [§9.2](persistence-api.md#92-resources-and-routes) |
| Events | `GET /operations/{id}/events` is a cursor-paged JSON list, or Server-Sent Events when `Accept` contains `text/event-stream`; event ID `<epoch>:<number>`; resume with `Last-Event-ID`; an ID from another epoch gets `event: reset` and a replay from the start; after 15 s with nothing sent the stream sends the comment line `: keep-alive`, with no ID and no event ([persistence and API §8.3](persistence-api.md#83-the-resource-and-its-progress)); the stream ends when the operation is terminal or at the opening token's expiry; `unresolved` is not terminal | `internal/api/operations.go:183-353`; [persistence and API §8.3](persistence-api.md#83-the-resource-and-its-progress) |
| Caching | Every API answer, authenticated or not, problems and the event stream included, sends `Cache-Control: no-store`; liveness sends no cache header | [persistence and API §9.1](persistence-api.md#91-conventions) |
| Browser support | No CORS headers, no `Origin` check, no cookie, no CSRF token anywhere in the server | search of `internal/` and `cmd/` for `Access-Control`, `Origin`, `Cookie`; [persistence and API §10.1](persistence-api.md#101-humans-oidc) "no server session" |
| Identity read | `GET /identity` answers who the caller is, which roles it holds and the epoch, to any authenticated credential, one with no role included | `internal/api/routes.go:97`, `internal/api/api.go:339`; [persistence and API §9.2](persistence-api.md#92-resources-and-routes) |
| Version read | `GET /server` answers the answering process's version, API revision, features, start mode and read-only settings, to any role; refused before a recovery start's entry commits | [persistence and API §9.1](persistence-api.md#91-conventions), [§9.2](persistence-api.md#92-resources-and-routes) |

## 3. Action, endpoint and role matrix

Every route of `internal/api/routes.go:78-136` with the client action that uses it. No route
answers `501` today: each sets a read or an effect (`internal/api/api.go:366-374`). "Human"
means the route refuses automation with `403 forbidden` and `humanOnly: true`
(`internal/api/api.go:382-383`). "Keyed" means the body carries unextracted input, fingerprinted
with the provider's digest key instead of SHA-256 (`internal/api/routes.go:20-23`). In the
"Start mode" column, "normal" means a process started for recovery refuses the route even after
its entry (`internal/api/api.go:350-353`), and "recovery" means a normal start refuses it
(`internal/api/api.go:356-359`).

### 3.1 Reads

Every read takes any role (`internal/api/routes.go:60`, `internal/api/routes.go:65`) except the
protected review and the identity read, which serves any authenticated credential, one that holds
no role included (`internal/api/api.go:339`).

| Client action | Route | Paged | ETag served | Notes |
| --- | --- | --- | --- | --- |
| cluster list / show | `GET /clusters`, `/clusters/{id}` | list | no | |
| machine list / show | `GET /machines`, `/machines/{id}` | list | no | filter `cluster`; `pendingConvergence` (G24) (`internal/api/reads.go:199-218`) |
| machine observations | `GET /machines/{id}/observations` | yes | no | |
| machine timeline | `GET /machines/{id}/timeline` | yes, by revision, oldest or newest first | no | JSON only, no stream (G9); `order`, and `tail`, the forward cursor from the newest entry, API revision 3 ([persistence and API §9.2](persistence-api.md#92-resources-and-routes)) |
| fragment, profile, assignment list / show / history / revision | `GET /{kind}s`, `/{kind}s/{id}`, `/{kind}s/{id}/revisions`, `/{kind}-revisions/{id}` | lists | head item only | `internal/api/routes.go:83-87` |
| draft list / show | `GET /drafts`, `/drafts/{id}` | list | item | the item's ETag is the one draft writes need |
| import list / status | `GET /ingestions`, `/ingestions/{id}` | list | no | filters `state`, `machine` (`internal/api/ingestions.go:280-299`) |
| import review | `GET /ingestions/{id}/review` | no | no | `author`, human; `no-store`; only while `paused` (`internal/api/review.go:30-52`) |
| release list / show | `GET /releases`, `/releases/{id}` | list | no | |
| release review, provenance | `GET /releases/{id}/machines/{m}/review` | no | no | redacted |
| plan list / show | `GET /plans`, `/plans/{id}` | list | no | filters `state`, `machine` (`internal/api/plans.go:429-451`) |
| approval show | `GET /approvals/{id}` | no | none, by design | approvals are immutable |
| operation list / show | `GET /operations`, `/operations/{id}` | list | no | filters `plan`, `machine`, `state`, `kind` (`internal/api/operations.go:95-108`) |
| operation watch | `GET /operations/{id}/events` | JSON pages, or SSE | no | [§8.6](#86-event-resumption) |
| act list | `GET /acts` | yes | no | |
| recovery status | `GET /recovery` | no | no | |
| dependency list / show / releases / alerts | `GET /dependencies` (`class` filter), `/dependencies/{id}`, `/dependencies/{id}/releases`, `/dependencies/{id}/alerts`, `/dependency-alerts` | lists | no | |
| `whoami`, roles in the web header | `GET /identity` | no | no | the caller's kind, issuer and subject or service, principal, roles, expiry and epoch; takes no query (`internal/api/identity.go:22-59`) |
| version check, server settings, start mode | `GET /server` | no | no | the answering process's version, API revision, features, start mode and read-only settings ([persistence and API §9.2](persistence-api.md#92-resources-and-routes)); refused before a recovery start's entry |

### 3.2 Writes

| Client action | Route | Roles | Human | `If-Match` | Success | Keyed | Start mode |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `cluster add` | `POST /clusters` | author | yes | | 201 | | |
| `machine add` | `POST /machines` | author | yes | | 201 | | |
| `machine set-endpoint` | `POST /machines/{id}/talos-endpoints` | author | yes | | 201 | | |
| `import start` | `POST /ingestions` | author | yes | draft's | 202 | `document` | |
| `import mark` | `POST /ingestions/{id}/marks` | author | yes | | 202 | `marks` | |
| `import continue` / `takeover` | `POST /ingestions/{id}/continuations` / `takeovers` | author | yes | | 202 | | |
| `import abandon` | `POST /ingestions/{id}/abandonments` | author | yes | | 200 | | |
| `draft create` | `POST /drafts` | author | | | 201, ETag | | |
| `fragment set` / `rm` | `PUT` / `DELETE /drafts/{id}/fragments/{name}` | author | | yes | 200, ETag | `document` (PUT) | |
| `profile set` / `rm` | `PUT` / `DELETE /drafts/{id}/profiles/{name}` | author | | yes | 200, ETag | | |
| `assignment set` / `rm` | `PUT` / `DELETE /drafts/{id}/assignments/{machine}` | author | | yes | 200, ETag | | |
| `draft discard` | `POST /drafts/{id}/discard` | author | | yes | 200 | | |
| `publish` | `POST /drafts/{id}/publications` | publisher | | yes | 202 | | |
| `plan create`, `drift revert` | `POST /plans` | publisher | | | 201 | | normal |
| `plan cancel` | `POST /plans/{id}/cancellations` | publisher (its own plan), approver, recovery-admin | | | 200 | | |
| `approve` | `POST /plans/{id}/approvals` | approver | yes | | 201 | | normal |
| `approval revoke` | `POST /approvals/{id}/revocations` | approver, recovery-admin | | | 201 | | |
| `machine freeze` | `POST /machines/{id}/freezes` | author, publisher, approver, recovery-admin | | | 201 | | |
| `machine unfreeze` | `POST /machines/{id}/unfreezes` | approver | | | 201 | | normal |
| `recovery enter` | `POST /recovery/entries` | recovery-admin | yes | | 201 | | recovery |
| `recovery exit` | `POST /recovery/exits` | recovery-admin | yes | | 201 | | |
| `recovery mark` / `release` | `POST /recovery/scopes/{machine}/marks` / `releases` | recovery-admin | yes | | 201 | | |
| `recovery scope-account` | `POST /recovery/accountings` | recovery-admin | yes | | 201 | | |
| `recovery account` | `POST /operations/{id}/attempts/{attempt}/accountings` | recovery-admin | yes | | 201 | | |
| `recovery takeover` / `resolve` | `POST /operations/{id}/takeovers` / `resolutions` | recovery-admin | yes | | 201 | | |
| `identity revoke` | `POST /identity-revocations` | recovery-admin | yes | | 201 | | |

Sources: `internal/api/routes.go:98-135`; the success status each handler returns (for example
`internal/api/inventory.go:172`, `internal/api/drafts.go:303`, `internal/api/ingestions.go:436`,
`internal/api/cancellations.go:144`, `internal/api/publications.go:76`); and
[persistence and API §9.2](persistence-api.md#92-resources-and-routes). The publisher's
cancellation of only its own plan is checked in the handler
(`internal/api/cancellations.go:61-67`). The ingestion start's `If-Match` is the target draft's
ETag (`internal/api/ingestions.go:153-154`).

### 3.3 Client actions with no route

| Client action | Why no route | Contract |
| --- | --- | --- |
| `login`, `logout` | identity-provider and client concerns; the server keeps no session | [§9](#9-authentication) |
| `context` | client configuration | [§5](#5-endpoint-discovery-and-context-selection) |
| automation token issue, rotate, list | server-side command only | not offered by either client |
| dispatch, apply | the controller dispatches approved plans ([design §12.7](../design/Talos_Configuration_and_Machine_Management_Design.md#127-application-approval-and-dispatch-boundary); [§13.7](../design/Talos_Configuration_and_Machine_Management_Design.md#137-poc-identity-and-approval-policy) item 2) | not offered |

## 4. API gap audit

The seventeen gaps the journeys found, checked against the code above, plus seven this audit adds. G21-G24 are the journeys' G13-G16 (expected epoch, start mode, principal display name, convergence status), numbered last because they were added to the journeys last; the journeys' G17 is G18 here.
Each has a disposition: an **API issue** proposed in [§14](#14-proposed-api-gap-issues-and-downstream-refinement),
a **contract correction** to an existing specification, also in §14, a **later design issue**
out of this milestone, also in §14, a **client rule** this document states, a **client
composite** over existing routes, an **accepted limitation**, or a **decision** listed in
[§13](#13-alternatives-and-open-choices). The owner accepted every disposition, including
"accepted", with this document. Nothing here is presented to a client as
supported.

| ID | Gap | Evidence | Disposition |
| --- | --- | --- | --- |
| G1 | No identity, role or capability read; roles appear only in a `403` body | `internal/api/api.go:380-392`; before [#277](https://github.com/ginsys/bronzeward/issues/277), no route in [`internal/api/routes.go:78-136`](https://github.com/ginsys/bronzeward/blob/3cbe953e87c6836799db569855670d48b70bb527/internal/api/routes.go#L78-L136) | closed by API issue A1 ([#277](https://github.com/ginsys/bronzeward/issues/277)): `GET /identity` serves the caller's identity, roles and epoch (`internal/api/identity.go:22-31`), a credential with no role included (`internal/api/api.go:339`) |
| G2 | No server version or compatibility read; only anonymous `/livez` | before [#278](https://github.com/ginsys/bronzeward/issues/278): `internal/server/server.go:19-22` | closed by API issue A2 ([#278](https://github.com/ginsys/bronzeward/issues/278)): `GET /server` serves the version and the API revision ([persistence and API §9.1](persistence-api.md#91-conventions), [§9.2](persistence-api.md#92-resources-and-routes)) |
| G3 | An operation does not name its ingestion; only the ingestion side links them: its read serves `operation`, and the start, mark and takeover `202` bodies name both | before [#280](https://github.com/ginsys/bronzeward/issues/280), the operation's subject was a draft or a plan and machine, never an ingestion ([`internal/api/reads.go:358-373`](https://github.com/ginsys/bronzeward/blob/23c1bc2a233cbdf0916076e62016a82892107053/internal/api/reads.go#L358-L373), `internal/api/reads.go:423-425`); the ingestion side: `internal/api/ingestions.go:235`, `internal/api/ingestions.go:256-260`, `internal/api/ingestions.go:217-218`, `internal/api/marks.go:106`, `internal/api/ingestions.go:355` | closed by API issue A4 ([#280](https://github.com/ginsys/bronzeward/issues/280)): an `ingest` operation's `subject` names its `ingestion` (`internal/api/reads.go:346`, `internal/api/reads.go:396-398`) |
| G4 | A draft does not name the release it produced | the draft read selects no release (`internal/api/reads.go:231`) | accepted limitation: the publish operation's `result.release` answers it |
| G5 | No render of a draft before publication | no such route in `internal/api/routes.go:78-136` | later design issue L1: a compilation design change, not a client contract |
| G6 | Provenance covers secret-reference occurrences only, not "which fragment set this field" | [compilation §8.1](compilation.md#81-provenance-by-tracer-composition) attributes stand-ins to reference occurrences | later design issue L1, as G5 |
| G7 | Drift content is not served | the machine's drift and observation reads name the observation, its time and a matched release, and no configuration (`internal/api/drift.go:150-155`, `internal/api/inventory.go:251-257`, `internal/api/observations.go:16-17`) | later design issue L2: serving observed configuration is a redaction design question; until then clients use a drift-adoption import with review |
| G8 | Machines have no human-readable name | the machine resource has no name member (`internal/api/inventory.go:260-275`) | accepted limitation for this milestone; clients show endpoint and node ID |
| G9 | No list filters on machines, plans, releases, drafts or acts; no machine timeline stream | `internal/api/acts.go:135-145` refuses any key but `limit`, `cursor` and the route's filters; before [#280](https://github.com/ginsys/bronzeward/issues/280), filters existed only on operations and dependencies | API issue A4 for the filters the journeys need first: plans by `state` and `machine`, machines by `cluster`; accepted limitation for the release, draft and act filters, which clients apply over the pages they read; timeline polling accepted. A4's filters are served ([#280](https://github.com/ginsys/bronzeward/issues/280)): plans by `state` and `machine` (`internal/api/plans.go:429-451`), machines by `cluster` (`internal/api/reads.go:199-218`) |
| G10 | No ingestion list | before [#280](https://github.com/ginsys/bronzeward/issues/280): [`internal/api/routes.go:90`](https://github.com/ginsys/bronzeward/blob/23c1bc2a233cbdf0916076e62016a82892107053/internal/api/routes.go#L90) | closed by API issue A4 ([#280](https://github.com/ginsys/bronzeward/issues/280)): `GET /ingestions`, filtered by `state` and `machine` (`internal/api/ingestions.go:280-299`) |
| G11 | Recovery marks, releases and accountings have no list route | `internal/api/routes.go:128-135` | accepted limitation: `GET /recovery` and the machine timeline |
| G12 | No browser access path: no CORS, and every request including a preflight `OPTIONS` must authenticate | `internal/server/server.go:10-13`, `internal/api/api.go:260-272`; [§10.1](#101-hosting-and-origin) | decision D1, decided (a): same-origin hosting, no API change |
| G13 | Authenticated reads set no `Cache-Control`; a browser may keep them in its HTTP cache | [`internal/api/review.go:31`](https://github.com/ginsys/bronzeward/blob/23c1bc2a233cbdf0916076e62016a82892107053/internal/api/review.go#L31), [`internal/api/operations.go:281`](https://github.com/ginsys/bronzeward/blob/23c1bc2a233cbdf0916076e62016a82892107053/internal/api/operations.go#L281) were the only `Cache-Control` writes | API issue A3; served: [§2](#2-the-api-a-client-meets) caching row |
| G14 | No anonymous way to learn the issuer, audience and client registration a deployment expects | [§2](#2-the-api-a-client-meets) anonymous row | decision D3, decided (b): a static client-configuration file next to the web files ([§5](#5-endpoint-discovery-and-context-selection) item 4) |
| G15 | The contract's prose describes the `reset` stream event, but its ingest and publish event-type lists omit it, so a client built from the lists does not expect it | `internal/api/operations.go:299-301`; [persistence and API §8.3](persistence-api.md#83-the-resource-and-its-progress) | contract correction C1 (documentation only); applied |
| G16 | No request-ID response header: a client learns the request ID only from a problem's `instance` | [`internal/api/problem.go:84-90`](https://github.com/ginsys/bronzeward/blob/23c1bc2a233cbdf0916076e62016a82892107053/internal/api/problem.go#L84-L90); no other header set | API issue A3; served: [§2](#2-the-api-a-client-meets) request-ID row |
| G17 | A request with an unsupported method on a known path is answered by the catch-all `404`, not `405` | `internal/api/api.go:234-236` (inferred from `ServeMux` method patterns; untested) | accepted; clients treat `404 not-found` on a known route as "not supported by this server" only together with A2's revision read ([§7](#7-compatibility-and-versioning)) |
| G18 | The settle floor and transport deadlines that bound recovery accounting are deployment settings no route serves before an accounting decision, which is when a client needs them to compute when the end observation may begin (the journeys' G17). After one, a successful accounting's timeline facts carry `settleFloor` and `anchor`, and the settle-floor refusal states the floor and its anchor only, so no route serves the maximum transport deadline at all. Separately, on a normal start a plan-creation refusal states the maximum transport deadline at any time, before a decision too; a recovery start refuses plan creation before that check | [execution and recovery §5.2](execution-recovery.md#52-accounting-a-lost-response); `examples/bronzeward.yaml:29-44`; after a decision: `internal/api/settle.go:160-161`, `internal/api/recovery_accountings.go:209-211`, `internal/api/recovery_accountings.go:230-231`, `internal/api/settle.go:148-150`; the maximum: `internal/api/plans.go:113-114`, `internal/api/routes.go:120`, `internal/api/api.go:350-352` | folded into A2, closed by [#278](https://github.com/ginsys/bronzeward/issues/278): `GET /server` serves the settle floor, the maximum transport deadline and the plan defaults, its transport deadline included, before a decision too, and on a recovery start once its entry has committed ([persistence and API §9.2](persistence-api.md#92-resources-and-routes)) |
| G19 | The fixture issuer mints tokens but offers no authorization, token, device or revocation endpoint, so no interactive sign-in can be tested against it | `fixtures/oidc/issuer/issuer.go:1-6` ("no login, no clients, no refresh"), `fixtures/oidc/issuer/issuer.go:113-126` (discovery and key set only), `fixtures/oidc/main.go:25-29` | validation-fixture issue A5; served ([#281](https://github.com/ginsys/bronzeward/issues/281)): the pinned test identity provider of [the fixtures' README](../../fixtures/README.md#test-identity-provider) and its sign-in self-test (`internal/api/idp_test.go`). Provider residuals, recorded in [the Zitadel test-provider record](../design/research/20261022-zitadel-test-idp.md#provider-residuals): no refresh-token reuse detection; any origin reflected with credentials, no per-app allowlist; a revoked session's access token still accepted by the server's local verification until `exp` plus its 60 s leeway (`internal/auth/auth.go:95`), so at most 6 minutes; end-session leaving the refresh token usable, so the proposed web sign-out revokes it ([§9.7](#97-sign-out-and-cache-clearing) item 2) |
| G20 | `503` answers carry no `Retry-After`, and the contract gives no retry guidance for `dependency-unavailable`, `transient-conflict` or `epoch-superseded` | search of `internal/` for `Retry-After`; [persistence and API §9.4](persistence-api.md#94-errors) | client rule in [§8.2](#82-errors); no API change proposed |
| G21 | Before [#293](https://github.com/ginsys/bronzeward/issues/293), a human's mutation took no expected-epoch precondition: only `Idempotency-Key` and `If-Match` were checked, and the epoch was reported only in an answer. After a write's answer is lost, and either a restore removes its record or the first attempt never committed, the same request with the same key executed afresh in the new epoch, because the in-transaction lookup finds no record ([persistence and API §12.4](persistence-api.md#124-restore-and-idempotency)); the read before repeating of [§8.4](#84-idempotency-and-ambiguous-responses) item 3 left a race. A service principal is checked through its token: it carries its epoch, and the write's own transaction refuses one from another epoch with `401` | before #293: [`internal/api/api.go:396-418`](https://github.com/ginsys/bronzeward/blob/3cbe953e87c6836799db569855670d48b70bb527/internal/api/api.go#L396-L418), [`internal/api/tx.go:190-198`](https://github.com/ginsys/bronzeward/blob/3cbe953e87c6836799db569855670d48b70bb527/internal/api/tx.go#L190-L198); service check: `internal/auth/automation.go:51-52`, `internal/api/idempotency.go:28-30`, `internal/api/idempotency.go:47-49`, `internal/api/api.go:305-311` | closed by API issue A6 ([#293](https://github.com/ginsys/bronzeward/issues/293)), D10 option (a): a mutation may carry `Bronzeward-Expected-Epoch`, one non-empty header (`internal/api/api.go:426-433`), and a lookup that finds no record refuses another epoch `412 epoch-mismatch`, naming the current one and committing no act or idempotency record (what the route committed before that lookup stays, [persistence and API §7.2](persistence-api.md#72-behavior)); only a lookup under the key's lock refuses, so a key with a record still answers it, AMB-07's `409` included (`internal/api/idempotency.go:52-58`, `internal/api/tx.go:71-81`, [persistence and API §7.2](persistence-api.md#72-behavior)) |
| G22 | No read of the process's start mode. `GET /recovery` serves epoch, recovery mode, entry, exit and scopes, and the headers carry only `Bronzeward-Epoch` and `Bronzeward-Recovery-Mode`. Past the role check, a recovery start shows only in an answer: a `409 recovery-mode-active` without a `scope` member, to any request but the entry before the entry commits and to plan creation, approval or unfreeze after it, or any recovery-entry answer except its not-a-recovery-start refusal. A normal start shows only in that refusal: a plain `409 conflict` with neither an `epoch` nor a `request` member, while every other `409 conflict` the entry answers carries `epoch`. The scope refusals that share the `recovery-mode-active` code carry `scope` | `internal/api/recovery_exits.go:123-129`, `internal/api/api.go:326-332`; process refusals: `internal/api/api.go:242-244`, `internal/api/api.go:345-359`, `internal/api/recovery_exits.go:211-213`, `internal/api/routes.go:120-127`; entry: `internal/api/recovery_entries.go:91-92`, `internal/api/recovery_entries.go:106-108`, `internal/api/recovery_entries.go:133-135`, `internal/api/idempotency.go:65-67`; scope refusals: for example `internal/api/plans.go:226-227` | closed after the entry by API issue A2 ([#278](https://github.com/ginsys/bronzeward/issues/278)): `GET /server` serves `startMode` ([persistence and API §9.2](persistence-api.md#92-resources-and-routes)). Before a recovery start's entry commits that read is refused like every other request, so the client reading of [§8.5](#85-epoch-changes) stays for that window. That reading rests on the absent `scope` member, which the contract said every `recovery-mode-active` carried until contract correction C2, now applied |
| G23 | No principal display name: bodies carry principal identifiers only, and no route maps one to a subject or name | `internal/api/acts.go:19-30`, `internal/api/releases.go:53`, `internal/api/plans.go:195`, `internal/api/approvals.go:43`, `internal/api/recovery_entries.go:79`; no such route in `internal/api/routes.go:78-136` | API issue A7, specified as `GET /principals` ([persistence and API §9.2](persistence-api.md#92-resources-and-routes)) by [#335](https://github.com/ginsys/bronzeward/issues/335); until it is served clients show shortened principal identifiers |
| G24 | No convergence status: the machine body serves `desired` and `applied.release`, and compares the artifact digests only to match an observation, never serving them, so a client cannot tell whether the `Desired` release's digest differs from the `Applied` one, which is what pending convergence means. After an adoption the `Applied` digest is the baseline's, so equal or different release IDs do not settle it | before [#280](https://github.com/ginsys/bronzeward/issues/280): [`internal/api/reads.go:155-166`](https://github.com/ginsys/bronzeward/blob/23c1bc2a233cbdf0916076e62016a82892107053/internal/api/reads.go#L155-L166); [execution and recovery §1](execution-recovery.md#1-supported-operation-and-state-values), [§6.3](execution-recovery.md#63-adopt) | closed by API issue A4 ([#280](https://github.com/ginsys/bronzeward/issues/280)): the machine's `pendingConvergence` compares the `Desired` artifact's digest with the `Applied` digest, `true` too for a `Desired` with no `Applied`, and serves neither digest (`internal/api/reads.go:155-171`) |

Not gaps, by design: no dispatch or apply route, no token administration route, no secret
browser, and no WebSocket (polling and SSE only, [persistence and API §8.3](persistence-api.md#83-the-resource-and-its-progress)).

## 5. Endpoint discovery and context selection

**Proposed.** A **context** names one Bronzeward installation and how to reach and sign in to
it. It holds no secret.

| Field | Meaning | Required |
| --- | --- | --- |
| `name` | the operator's label | yes |
| `server` | base URL; the client appends `/api/v1`. A path prefix is allowed for deployments that route by path | yes |
| `certificateAuthority` | path to a PEM bundle that replaces the system trust store for `server` ([§6](#6-tls-trust)) | no |
| `credential` | `oidc` or `automation` | yes |
| `oidc.issuer`, `oidc.clientID`, `oidc.scopes` | the identity provider and the public client registered for this client ([§9.1](#91-identity-provider-requirements)) | with `oidc` |
| `oidc.audience` | the value the server's `auth.oidc.audience` holds, used only for the client's own pre-check ([§9.6](#96-failure-cases)) | with `oidc` |
| `oidc.certificateAuthority` | PEM bundle for the issuer, if not the system store | no |
| `oidc.tokenFile` | path to the owner-only token file of the D2 (b) opt-in; its presence is the opt-in ([§9.2](#92-cli-sign-in) item 3) | no |
| `automation.tokenFile` | path to the automation token ([§9.4](#94-automation-token-input)) | with `automation`, unless given per invocation |
| `lastEpoch` | the last `Bronzeward-Epoch` seen; not secret ([§8.5](#85-epoch-changes)) | written by the client |

1. The CLI keeps contexts in a configuration file under the user's configuration directory, with
   one current context. Precedence: `--context`, then `BRONZEWARD_CONTEXT`, then the current
   context. The file holds no token, key or secret; tokens live in the store of
   [§9.2](#92-cli-sign-in).
2. Every command that talks to a server shows the context's name and server in its error and
   wait output, so an operator with two installations never acts on the wrong one unseen. A
   write in an interactive session prints the context before it sends.
3. The web interface serves exactly one installation, the one at its own origin
   ([§10.1](#101-hosting-and-origin)). Switching installations is navigating to another origin,
   which starts with an empty state; nothing read under one origin is shown under another.
4. Today the issuer, client ID and audience are configured by hand (G14). Under decision D3 (b),
   a static client-configuration file served next to the web files, `bronzeward-client.json` at
   the web root, names them: a JSON object with the string members `issuer`, `audience`,
   `cliClientId` and `webClientId`, the last two the client IDs of the two registrations of
   [§9.1](#91-identity-provider-requirements) item 2, and an optional string member `scopes`:
   space-separated scopes requested after `openid offline_access`, as the CLI's `oidc.scopes`,
   for a provider that puts the audience into a token only when a scope asks for it (Zitadel's
   project audience scope is one). Absent is none; repeating `openid` or `offline_access` is a
   configuration error, as is any other member's wrong type (lead decision under the owner's
   2026-10-07 grant, 26 October 2026, recorded in
   [#332](https://github.com/ginsys/bronzeward/issues/332)). The
   web client reads it from its own origin and uses the web client's ID, and the CLI imports it
   with `context add --from <url>` under the TLS rules of [§6](#6-tls-trust) and stores the CLI
   client's ID. Entering them by hand stays possible. The server is never asked
   for them through an authenticated route: the client needs them before it can authenticate.
5. A context's `server` is checked once when it is added: `GET /livez` must answer `204`
   (`internal/server/server.go:19-21`), which shows only that something answers; it proves
   neither the API nor its version.

## 6. TLS trust

**Proposed.**

1. `server` and `oidc.issuer` must be `https`. Plain `http` is accepted only to a loopback
   address. This is the loopback half of the server's own rule for its issuer, which also admits
   hosts the deployment lists (`internal/config/config.go:382-398`); a client has no such list,
   because it cannot know which network protects a host. The development setup
   (`examples/bronzeward.yaml:7`, `examples/bronzeward.yaml:14`) works and nothing else does.
2. Certificates are verified against the system trust store, or against the context's
   `certificateAuthority` bundle, which then replaces it for that host. Host name verification is
   always on. TLS 1.2 is the minimum.
3. No option disables verification, in configuration or on the command line, and no environment
   variable does. An operator with a private CA adds the bundle to the context.
4. A verification failure names the host, the subject and issuer of the presented certificate and
   the trust source used, and sends nothing. What a TLS or transport error shows that the remote
   side chose, such as a certificate's DNS names, which may hold any ASCII, is shown with each
   control character and each byte that is not UTF-8 escaped (`\x1b`), so it cannot write to the
   terminal. The escaping holds toward the server, toward an https proxy and toward the identity
   provider, in discovery, renewal and revocation, and for the server's own text in an error: the
   reason phrase of every status shown and each member of a problem document
   (`internal/client/printable.go`). A server or issuer URL that holds a character the escaping
   would change is refused before any request uses it, as is such an endpoint or redirect the
   identity provider names.
5. Certificate pinning is not offered: a bundle per context limits trust enough for this
   milestone, and pinning a leaf breaks on every renewal.
6. The browser uses the browser's own trust and has no override.
7. **Redirects and proxies.** Neither client follows a redirect from the API, so `Authorization`
   and a request body never reach another URL and nothing is resent. The CLI reports a `3xx` with
   its target. The web client issues every API call with `fetch`'s `redirect: 'error'`, because a
   browser otherwise follows a `307`/`308` with the same method and body. The page then sees only a
   network error, which it cannot tell apart from any other transport failure (assumption S7): it
   shows a generic transport-failure message that names a refused redirect as one possible cause,
   never a target. For a write that error is an ambiguous outcome and is handled as in
   [§8.4](#84-idempotency-and-ambiguous-responses) item 2, since the page cannot tell whether the
   request reached the server. Toward
   the identity provider the CLI follows redirects only to `https` (or loopback) URLs, and none
   on a token revocation ([§9.7](#97-sign-out-and-cache-clearing) item 1). The CLI
   honours the standard proxy environment variables, says so in verbose output, and never sends
   `Authorization` to a proxy in clear text: an `https` URL goes through a proxy only by a
   tunnel. The server's own issuer fetches follow a comparable rule
   ([persistence and API §10.1](persistence-api.md#101-humans-oidc)).

## 7. Compatibility and versioning

Facts: the API is `/api/v1`; within it fields and routes are only added, never removed or changed
in meaning; a route due for removal answers with a `Deprecation` header for at least one minor
release first; a breaking change is a new prefix; unknown request fields are refused
([persistence and API §9.1](persistence-api.md#91-conventions)). Since
[#278](https://github.com/ginsys/bronzeward/issues/278), `GET /server` reports the answering
process's version, its API revision, a whole number raised by one with each additive change within
`v1` and listed in §9.1's revision table, and the optional features it serves (G2,
[persistence and API §9.2](persistence-api.md#92-resources-and-routes)). Before a recovery start's
entry commits the read is refused with `409 recovery-mode-active`, as every request but the entry
is.

**Proposed.**

1. A client sends only fields the server it talks to is known to accept. Because the server
   refuses unknown request fields, a newer client sending a field an older server lacks fails with
   `400 invalid-request`, which is safe but useless; the client feature-gates such fields on the
   served API revision.
2. A client ignores unknown response fields, and shows an unknown enumeration value raw, never as
   a known state and never as success.
3. A client declares the minimum API revision it needs. It reads `GET /server` on context use and
   refuses, with both revisions named, a server below that minimum, before any write. `GET /server`
   answered `409 recovery-mode-active` without a `scope` member is a recovery start before its
   entry ([§8.5](#85-epoch-changes)), since the read is refused that way only then: the client
   allows only the entry, whose request it already
   knows from revision 1, and reads the revision once the entry has committed. The revision
   describes the process that answered; another instance behind the same endpoint may differ, so
   a refusal names the revision that answer served.
4. A `Deprecation` header on any response is shown once per command or page load, naming the
   route.
5. Each client prints its own version and the server's, with its API revision, in
   `bronzewardctl version` and in the web footer; the server's as "unknown" while the read is
   refused.

## 8. Request handling

### 8.1 Requests

**Proposed.** Every request sends `Authorization` and `Accept: application/json` (or
`text/event-stream` for a stream). Every `POST` and `PUT` sends a JSON object, `{}` when the route
takes no member, with `Content-Type: application/json`; a `DELETE` sends neither
([§2](#2-the-api-a-client-meets) request body row). Every write
sends exactly one `Idempotency-Key` ([§8.4](#84-idempotency-and-ambiguous-responses)) and, where
[§3.2](#32-writes) lists it, exactly one `If-Match`; every write but the recovery entry also sends
exactly one `Bronzeward-Expected-Epoch` ([§8.4](#84-idempotency-and-ambiguous-responses) item 3). A body is at most 1 MiB
(`internal/api/body.go:36`); a client refuses a larger input before sending and says so, rather
than relying on the server's `400`.

### 8.2 Errors

**Proposed** client behaviour per problem code, from the code table of
[persistence and API §9.4](persistence-api.md#94-errors) and `internal/api/problem.go:11-36`. The
client always shows `title`, `detail` when present, and `instance`, each escaped as
[§6 item 4](#6-tls-trust) states, and never shows a request body. Exit codes are the journeys'
(journeys §4).

| Status, code | Client behaviour |
| --- | --- |
| 400 `invalid-request` | a malformed request: operator input, a client defect or an incompatible server. Operator input includes a mark that is not a valid path on the start and fragment routes (`internal/api/ingestions.go:69-73`, `internal/api/fragments.go:52-56`; the index is in `detail`, there is no `position` member), an empty document (`internal/api/tx.go:119-120`), an invalid name (`internal/api/drafts.go:190-191`) and the recovery entry's body checks (`internal/api/recovery_entries.go:49-71`). Show `detail` and stop; never repeat |
| 400 `cursor-invalid` | discard the cursor and restart the list. Run the epoch-change handling of [§8.5](#85-epoch-changes) only when `Bronzeward-Epoch` changed: the same code also answers a cursor that does not parse (`internal/api/acts.go:154-156`) |
| 401 `unauthenticated` | renew once ([§9.5](#95-renewal)); repeat a read; for a write follow [§8.4](#84-idempotency-and-ambiguous-responses); a second `401` stops with the failure text of [§9.6](#96-failure-cases) |
| 403 `forbidden` | show the route's `roles`, and "humans only" when `humanOnly` is true; no retry. For the plan cancellation's creator check, the form that carries a `plan` member, show its `detail` instead: its `roles` are the route's, `publisher` included, which the caller already holds (`internal/api/cancellations.go:64-72`, `internal/api/routes.go:123`). After an ambiguous attempt, the route-level role check's refusal, like the process `409`s, is decided before the idempotency lookup (`internal/api/api.go:339-359`, `internal/api/api.go:380-391`; for example a renewal dropped a role, [§9.6](#96-failure-cases)), so it says nothing about the earlier attempt: the outcome stays unknown ([§8.4](#84-idempotency-and-ambiguous-responses) item 4). The plan cancellation's creator check, a `403 forbidden` that carries a `plan` member, refuses after the lookup instead (`internal/api/cancellations.go:61-73`, `internal/api/tx.go:198-202`): it means the key has no record, in this epoch or an earlier one, so the earlier attempt did not commit or a restore removed it |
| 403 `identity-revoked` | stop; clear the context's stored credential, meaning the tokens the client itself keeps in its credential store, its memory or its D2 (b) token file, never an operator-supplied automation token file, which it leaves untouched and reports as no longer accepted; do not start a new sign-in by itself ([§9.6](#96-failure-cases)) |
| 404 `not-found` | the resource does not exist in this epoch; after a restore a handle issued later answers `404` ([persistence and API §12.3](persistence-api.md#123-what-a-restored-state-means-record-by-record)) |
| 409 `conflict`, `stale-input`, `scope-busy`, `machine-identity-mismatch`, `ingestion-abandoned`, `recovery-mode-entered`, `recovery-mode-active` | show the named resource and state; read it; never repeat automatically, with one exception. A `409 recovery-mode-active` without a `scope` member, and a `409 conflict` to the recovery entry without `epoch` and `request` members, are process refusals that show the start mode ([§8.5](#85-epoch-changes)). A `409 conflict` that carries `request` and `epoch` is an earlier-epoch idempotency record: it is read and reported, not repeated, under either key ([§8.4](#84-idempotency-and-ambiguous-responses) item 7). The one exception: a `409 conflict` on a draft fragment write (`PUT /drafts/{id}/fragments/{name}`) that answers a repeat, carries an `ingestion` member but no `draft` member, and has no `Idempotent-Replayed: true` is the first request still running, or ended without committing while its claim's lease has not lapsed (`internal/api/fragments.go:105-114`, `internal/api/tx.go:80-98`; looked up by principal and key, so it never answers a first send). A `409` with both `ingestion` and `draft`, replayed or not, is a recorded refusal (`internal/api/fragments.go:291-296`, `internal/api/drafts.go:94-104`): the write was refused, not pending. For the in-progress case, repeat the identical request with the same key, with backoff, until it answers otherwise or the client's wait bound of [§8.4](#84-idempotency-and-ambiguous-responses) item 2 expires; the contract says the repeat then gets the first request's replay ([persistence and API §7.2](persistence-api.md#72-behavior); [§8.4](#84-idempotency-and-ambiguous-responses) item 2), or, when the first request ended without committing, executes as a new request, unless its `Bronzeward-Expected-Epoch` names another epoch (`412 epoch-mismatch`, A6), reported as this send's own result (`internal/api/fragments.go:105-114`) |
| 412 `precondition-failed` | someone else wrote first: stop, show the newer state, never refetch and resend ([§8.3](#83-etags)) |
| 412 `epoch-mismatch` | the epoch is no longer the one the write named in `Bronzeward-Expected-Epoch` and the key has no record: this send's own transaction committed nothing, though a fragment write's claim, a scope mark's observation or a principal row taken before the refusal stays ([persistence and API §7.2](persistence-api.md#72-behavior)). Handle the epoch change of [§8.5](#85-epoch-changes) with the `epoch` member, and set the write aside as in [§8.4](#84-idempotency-and-ambiguous-responses) item 3; never repeat it. After an ambiguous attempt it means that attempt's record is gone or never committed ([persistence and API §7.2](persistence-api.md#72-behavior)) |
| 422 `validation-failed` | show `rule` and position extensions; for marks only the position, never a path. Most mark refusals arrive later, as an event ([§11.3](#113-cli-review)) |
| 422 `idempotency-key-reused` | on a first send: the key was used for a different request; nothing was done; a client defect unless the operator supplied the key. On a repeat to a keyed route (fragment write, ingestion start, mark): the outcome is **unknown**, because a byte-identical repeat is also answered `422` when the provider can no longer compute under the digest-key version the record names, or the recorded key is not one the current key can be matched to; a rotation alone still replays ([persistence and API §7.1](persistence-api.md#71-the-key); `internal/api/idempotency.go:94-121`); read the resource and report what it shows |
| 428 `precondition-required`, `idempotency-key-required` | a client defect; show and stop |
| 500 `internal-error` | the outcome may be unknown: repeat the identical request with the same key ([§8.4](#84-idempotency-and-ambiguous-responses)) |
| 501 `not-implemented` | not served by this server; no route answers it today |
| 503 `dependency-unavailable`, `talos-access-unavailable`, `transient-conflict`, `epoch-superseded` | reads: retry with backoff (1 s doubling to 30 s, at most 5 tries, then stop). Writes: repeat with the same key at most 3 times. If every attempt of the write was answered `503`, report **not done**: each such `503` states that its attempt's own transaction committed nothing, so no act or idempotency record exists for it; a fragment write's claim, a scope mark's observation or its start, or a principal row, taken before it stays ([persistence and API §9.4](persistence-api.md#94-errors)). If any earlier attempt was ambiguous ([§8.4](#84-idempotency-and-ambiguous-responses) item 2), a `503` to the repeat says nothing about that attempt: report the outcome as unknown, as in [§8.4](#84-idempotency-and-ambiguous-responses) item 4. `epoch-superseded` is not repeated against the same server process: it started before the current epoch and cannot succeed until restarted. The server sends no `Retry-After` (G20) |
| an unknown code | treat by status class: 4xx as a refusal, 5xx as an unknown outcome for a write |
| a 5xx that is not a Bronzeward problem | an answer that is not `application/problem+json`, or whose `type` does not start `urn:bronzeward:problem:`, for example a `502`, `503` or `504` from the component in front of the server, may come after the server committed: for a write it is an ambiguous outcome as in [§8.4](#84-idempotency-and-ambiguous-responses) item 2, never **not done**; reads retry as for the `503` row |

The `503` row applies only to a Bronzeward problem with one of its four codes. The write `503`s
say that the attempt they answer committed nothing of its own transaction (what a route committed
before it stays, as the row says); for example authentication's
(`internal/api/api.go:319`), the installation-state read's (`internal/api/api.go:276`), the keyed
fingerprint's (`internal/api/tx.go:110`, `internal/api/tx.go:123`) and the transaction's
(`internal/api/tx.go:308`, `internal/api/tx.go:310`, `internal/api/tx.go:314`). This list is not
exhaustive, and none of them speaks for an earlier attempt: a keyed route computes its
fingerprint before its idempotency lookup (`internal/api/tx.go:58-64`), so a provider outage
answers a repeat `503` even when the first attempt's record exists. The repeat is still sent with
the same key, which costs nothing.

**Failed operations.** Several codes above (`machine-identity-mismatch`, `ingestion-abandoned`,
`recovery-mode-entered`, mostly `talos-access-unavailable`) usually arrive not as a response but
in an operation's `error` member or in an event's problem, where `instance` is the operation's
identifier, not a request's ([persistence and API §9.4](persistence-api.md#94-errors)). A client
shows such a problem with the operation's identifier and final state, applies the row above for
what the operator may do next, and never repeats the request that started the operation: the
`202` was already a success.

### 8.3 ETags

Facts: [§2](#2-the-api-a-client-meets) ETag row. After a restore to a backup taken at *T*, an
ETag issued after *T* answers `412`, while one for a revision the backup holds still matches; the
epoch is not part of the ETag
([persistence and API §12.3](persistence-api.md#123-what-a-restored-state-means-record-by-record),
[§4.1](persistence-api.md#41-etags)). A draft's ETag also moves when the server itself writes the
draft: when an ingestion's job commits its entries (`internal/api/runner.go:439-440`) and at
publication, which also closes the draft (`internal/api/publish.go:398-399`).

**Proposed.** The journeys' last-seen rule (journeys §5.4, C5): the client sends the ETag it last
read or was answered for that draft, stores the one each write answers, and on `412` stops and
shows the current state. It never reads the new ETag and resends by itself: that would overwrite
another author's change unseen. One exception: after an import the client itself started on that
draft reaches `succeeded`, it re-reads the draft, since the server's own write moved the ETag. It
adopts the re-read ETag only if the draft's `revision` equals the import operation's
`result.draftRevision` (`internal/api/runner.go:443-444`); a higher revision means someone else
wrote since, and the client treats it as a `412`: it stops and shows the newer state. ETags are kept per context and draft in memory; the
CLI may keep them in its non-secret state for the length of a scripted sequence. On an epoch
change it drops them all: more than the contract strictly needs, since a pre-backup ETag still
matches, but the client cannot tell which ETags were issued after *T*. The ingestion start's
`If-Match` is the target draft's ETag.

### 8.4 Idempotency and ambiguous responses

Facts: a key is scoped to the authenticated principal, a human's being its issuer and subject
(`internal/auth/auth.go:190-192`, `internal/api/idempotency.go:41-43`); the fingerprint covers
method, route, path parameters, `If-Match` and the canonical body, keyed with the provider's
digest key on keyed routes (`internal/api/body.go:165-185`); where the key has a record, the same
request under it replays, another is `422`, an earlier epoch's record is `409`
(`internal/api/idempotency.go:60-106`); where it has none, any request under it executes as a new
one (`internal/api/tx.go:198-202`), unless its `Bronzeward-Expected-Epoch` names another epoch
(`412 epoch-mismatch`, A6); a request whose effect a restore removed lost its record too and
executes again under the same exception
([persistence and API §12.4](persistence-api.md#124-restore-and-idempotency)); the same key does
not make a remote mutation exactly-once
([persistence and API §7.2](persistence-api.md#72-behavior)). The same exception holds wherever
this document, D9 in [§13](#13-alternatives-and-open-choices) included, says that a request
finding no record executes.

Keys are not global. The record's primary key is the pair of principal and key
(`internal/migrate/migrations/0001_schema.sql:491-508`), and the lookup joins on both, with the
authenticated principal's identifier (`internal/api/idempotency.go:41-43`). Two principals using
the same key string therefore never meet: the second finds no record and executes as a new
request, neither replayed nor refused `422`. The fingerprint itself names neither principal nor
epoch (`internal/api/body.go:170-187`); the principal is in the lookup, and the epoch is compared
on the record found (`internal/api/idempotency.go:41-42`, `internal/api/idempotency.go:65-68`).

The principal is the authenticated identity, not the credential: for automation it is the
service identity that owns the token (`internal/auth/automation.go:58`), so a rotated token keeps
the same idempotency scope.

The server's fingerprint is what makes a repeat safe, but only against a record: a repeat that is
not byte-identical in meaning is refused `422` and does nothing when the first attempt committed
and its record is current. When the first attempt never committed, or a restore removed its
record, there is nothing to compare with, and a changed repeat executes as a new request with the
changed input. A client therefore repeats only the exact request it sent, including its
`If-Match`, never a reconstruction it cannot prove identical. On a keyed route a byte-identical
repeat can also be answered `422`, when the provider can no longer compute under the digest-key version the record
names or the recorded key cannot be matched to the current one; a rotation alone still replays
([persistence and API §7.1](persistence-api.md#71-the-key);
`internal/api/idempotency.go:94-121`); then the first request may well have committed. The
dangers are a repeat under another principal, which the server treats as a new request, and a
repeat across a restore, which executes again by design unless it carries
`Bronzeward-Expected-Epoch` and an entry has minted a new epoch (`412 epoch-mismatch`, A6). A restore after which nobody entered
recovery mode mints no new epoch, so no epoch comparison can detect it
([persistence and API §12.1](persistence-api.md#121-the-epoch)); that residual is the
contract's, and this specification does not close it.

**Proposed.**

1. **Key.** One key per logical write, 128 bits from the operating system's random source,
   base64url without padding (22 characters, within `internal/api/api.go:394`). Never derived from
   the request content, so a key reveals nothing about a document.
2. **Ambiguous.** A write's outcome is unknown when the connection fails after any byte of the
   request was sent, when no complete response arrives, on `500`, or on a `5xx` that is not a
   Bronzeward problem ([§8.2](#82-errors)). The client then repeats the
   identical request it still holds in memory, with the same key, at most 3 times with backoff.
   A write's `503` is repeated the same way; it states only that the attempt it answers committed
   nothing, so after an ambiguous attempt the outcome stays unknown ([§8.2](#82-errors)). The
   in-progress `409` of a fragment write (route plus `409 conflict` plus an `ingestion` member,
   no `draft` member, answering a repeat and without `Idempotent-Replayed: true`, never the
   detail text; `internal/api/fragments.go:105-114`) is repeated with backoff until the first
   request has answered. A `409` with both `ingestion` and `draft` members, replayed or not, is a
   recorded refusal (for example the draft closed or a publication active;
   `internal/api/drafts.go:94-104`, `internal/api/fragments.go:291-296`) and is reported as
   refused, not repeated. The server keeps that claim alive for as long as the first request
   runs (`internal/api/fragments.go:87`). A first request that ended without committing (a `5xx`
   from its transaction or from prepare) only stops the heartbeat; the claim is abandoned at once
   only after a `5xx` from extraction or generation creation (`internal/api/fragments.go:157-167`,
   `internal/api/fragments.go:172-193`), otherwise when its lease
   lapses (`internal/api/fragments.go:105-114`). The client bounds the wait itself: a fixed
   maximum, configurable and at least the claim lease, after which the outcome is reported
   unknown as in item 4. The deployment's claim timers are an example of a sensible value
   (`examples/bronzeward.yaml:76-80`), served by `GET /server` as `ingestionClaim` since
   [#278](https://github.com/ginsys/bronzeward/issues/278): the client's bound is at least the
   served `leaseSeconds` when that member is not null. A replay
   (`Idempotent-Replayed: true`) is reported as "already done earlier; showing that result", except
   a replayed `application/problem+json`, which is reported as the earlier attempt's refusal (the
   fragment write replays recorded refusals, `internal/api/fragments.go:291-296`,
   `internal/api/idempotency.go:80-91`); a
   `422` to a repeat on a keyed route is reported as an unknown outcome ([§8.2](#82-errors)).
3. **Same principal and epoch only.** Before any repeat the client checks that its credential
   still names the same principal: for OIDC, the same `iss` and `sub` in the renewed token; for
   automation, the same service identity, compared by the `principal` the identity read serves
   (A1, `internal/api/identity.go:56`): the client reads it once before the first send, and again
   with the current credential before a repeat whose token ID changed, so a rotated token of that
   identity is the same principal. An automation client that cannot read it sends nothing, so
   every write it sends has a `principal` to compare. Every write, its first send and each
   repeat, carries the reference epoch in `Bronzeward-Expected-Epoch`: the last epoch the client saw before the
   first send. An empty or repeated header is `400 invalid-request`, never taken as absent
   (`internal/api/api.go:426-433`). The header needs API revision 2
   ([persistence and API §9.1](persistence-api.md#91-conventions)): an earlier server ignores it
   without error, so a client that relies on it declares revision 2 as its minimum
   ([§7](#7-compatibility-and-versioning)). A client that has seen none reads `GET /recovery` (any role,
   `internal/api/routes.go:94`) once before that first send. The server checks the header
   whenever a lookup finds no record for the key, and refuses only under the key's lock: in the
   write's own transaction, a fragment write's claim transaction or the first lookup's fresh read
   (A6, `internal/api/idempotency.go:52-58`, `internal/api/tx.go:204-210`,
   `internal/api/fragments.go:95-103`, `internal/api/tx.go:71-81`),
   so a recovery entry that commits at any point before the write's own transaction is caught: if the first
   attempt's record survived, the repeat meets the earlier-epoch `409` of item 7 (AMB-07);
   otherwise, when a restore removed it or the first attempt never committed, the repeat is
   refused `412 epoch-mismatch` naming the current epoch (G21, AMB-12). A route's work outside
   those lookups can refuse first, and a fragment write's claim, a scope mark's `observation-started` entry or a
   principal row taken before the refusal stays ([persistence and API §7.2](persistence-api.md#72-behavior),
   §14), but no act or idempotency record commits. A service principal's repeat is refused `401`
   instead, its token carrying its epoch. Before a
   repeat the client still confirms the epoch with one `GET /recovery` and compares its
   `Bronzeward-Epoch` with the reference epoch, so a write already known to be across an epoch
   change is not sent again; the header of the failed attempt is not enough, because a dropped
   connection shows none. If the principal or the epoch differs, the confirming read yields no
   epoch header (for example its own `503`), or the repeat is answered that `409` or `412`, the
   write is set aside: not repeated, not sent under a new key; the client reads the resource and
   the operator decides (journeys §8).
   The recovery entry (`POST /recovery/entries`) is the one exception. Its own commit mints the
   new epoch, and until the process has seen that commit it refuses every other request,
   `GET /recovery` included, with `409 recovery-mode-active` (`internal/api/api.go:343-347`). When
   only the `201` was lost, the process saw the commit before answering, so the confirming read
   shows the new epoch and a same-key repeat replays the entry
   (`internal/api/recovery_entries_test.go:236-253`; the replay: `internal/api/idempotency.go:60-82`,
   `internal/api/recovery_entries.go:102-113`). When the server lost its own COMMIT reply
   and answered `500`, only a same-key repeat completes the entry
   (`internal/api/recovery_entries_test.go:255-296`). So the client repeats an entry whose outcome
   is unknown under the same key, checking the principal but not the epoch, here and in item 4,
   and sends the entry without `Bronzeward-Expected-Epoch`, on its first send and every repeat.
   The server replays only this process's own entry and refuses any other entry record with a
   `409 conflict` carrying `request` and `epoch` (`internal/api/recovery_entries.go:95-114`), so
   the exception cannot replay an earlier entry or carry any other write across the epoch change.
   The entry itself can cross one: where its record is missing, because it never committed or a
   restore removed it, and a later recovery start is waiting for its entry, the repeat becomes
   that start's entry, with the earlier body's restored backups, stop time and reason, and is
   reported as this send's own `201` (`internal/api/tx.go:198-202`,
   `internal/api/recovery_entries.go:133-135`). That is accepted with the exception, like the
   residual of AMB-10, and A6 does not close it. The entry's answer is then handled as in
   [§8.5](#85-epoch-changes), without setting the entry itself aside (REC-04).
4. **After the process ends.** The CLI stores no request body, hash of a body, or key journal.
   When it gives up with the outcome unknown, it prints, on standard error, the key, method,
   route, context, principal (issuer and subject, or the service identity's `principal` from
   A1), epoch and the `If-Match` value it sent, if any, and what to read to find
   out. An ETag is not secret. An operator may then repeat with `--idempotency-key <key>
   --expect-epoch <epoch> --expect-principal <principal>`, plus `--if-match <etag>` where the
   route takes one, all as printed. The client refuses, sending nothing, when one of them is
   missing or, for a human, when its token's `iss` and `sub` are not the printed ones. An automation
   client first sends item 3's identity read and refuses when that `principal` is not the printed
   one. Otherwise it refuses after sending only item 3's reads (that identity read, for
   automation, and the confirming read) when the epoch differs as in that item (not for the recovery
   entry). It
   never substitutes an ETag it reads now: `If-Match` is part
   of the fingerprint ([persistence and API §7.1](persistence-api.md#71-the-key)), so a new one
   turns the repeat into another request. The operator supplies the input and the `If-Match` value
   again, and the client keeps nothing to check either against. Where the first attempt's record
   exists, a changed input or `If-Match` is refused `422` and does nothing (AMB-04); where it does
   not, because the first attempt never committed or a restore removed it, the changed input
   executes (AMB-10), and a changed `If-Match` executes when it matches the current resource and
   is refused `412` otherwise (AMB-13). An operator who cannot reconstruct the exact
   input stops and reconciles from the resource instead of repeating. This is option (a) of
   decision D9, for the journeys' open question on key metadata across a restart, as decided in
   [§13](#13-alternatives-and-open-choices); option (b), a local journal, would replay without
   that reconstruction.
5. **Browser.** The page holds pending writes in memory only. Leaving or reloading the page drops
   them; the outcome-unknown notice shows the key and the resource to read, and no repeat is
   offered afterwards.
6. A client never generates a fresh key for a write it has already sent.
7. **Deliberate departure from the contract's advice.** For an earlier-epoch record (`409`) and
   for a keyed `422` it cannot compare, the contract and the server's `detail` tell the client to
   retry under a new key ([persistence and API §7.2](persistence-api.md#72-behavior),
   [§7.1](persistence-api.md#71-the-key); `internal/api/idempotency.go:66`). A client does not do
   that automatically. In both cases the first request may have committed: an earlier-epoch record
   exists after any recovery-mode entry, with or without a restore, and its effect may be in the
   current state; an incomparable `422` may follow a commit. The recorded act may also have been
   fenced by that entry (an approval that authorizes nothing, a failed job), and executing afresh
   repeats it ([persistence and API §7.2](persistence-api.md#72-behavior)); whether that repeat
   is wanted depends on the route and the state, which a client cannot judge. The
   client reads the resource, reports what it shows, and lets the operator send the write again as
   a new one if it is still wanted.

### 8.5 Epoch changes

Facts: an epoch is a random identity minted at installation and at each recovery-mode entry,
compared by equality only
([persistence and API §12.1](persistence-api.md#121-the-epoch)). An epoch change makes cursors
`400 cursor-invalid`, earlier idempotency records `409`, a write whose key has no record and whose
`Bronzeward-Expected-Epoch` names the earlier epoch `412 epoch-mismatch`, earlier automation
tokens `401`, and streams `reset` ([§2](#2-the-api-a-client-meets)).

**Proposed.** The client compares `Bronzeward-Epoch` on every response with the last it saw for
the context. On a change it:

1. discards every cursor, cached page, ETag and stream position for the context;
2. sets aside every pending write as in [§8.4](#84-idempotency-and-ambiguous-responses) item 3,
   except a recovery entry, which that item repeats under its key;
3. tells the operator "The installation entered recovery mode or was restored; what was read
   before may no longer hold", and, while `Bronzeward-Recovery-Mode: true` is present, shows the
   recovery banner (journeys §8);
4. records the new epoch. A context's first epoch is recorded without a notice.

An epoch is never ordered or parsed. A missing `Bronzeward-Epoch` on an authenticated success is
a defect the client reports.

**Start mode** (G22). Neither the headers nor `GET /recovery` say whether the process was started
for recovery, which decides whether plan creation, approval and unfreeze can succeed (journeys
§5.9). Since [#278](https://github.com/ginsys/bronzeward/issues/278), `GET /server` serves it as
`startMode` ([persistence and API §9.2](persistence-api.md#92-resources-and-routes)), but a
recovery start refuses that read until its entry commits. The client keeps the last mode a
`GET /server` answer or one of the answers below showed, per context, and reads `GET /server`
again on context use and after a new epoch. A restart changes the mode without any signal, so the
kept mode is only the last one shown:

1. **Recovery start:** `GET /server` answering `"startMode": "recovery"`, which a recovery start
   serves only after its entry; a `409 recovery-mode-active` without a `scope` member; or any answer of
   `POST /recovery/entries` given after authentication and the role check other than the refusal
   in item 2, its `201` and its other `409 conflict` answers included (each of these carries
   `epoch`). A `401`, a `403`, or any answer without `Bronzeward-Epoch` to the entry shows
   nothing: both `503`s decided before routing, the authentication one and the installation-state
   read's (`internal/api/api.go:274-277`), and any `5xx` that is not a Bronzeward problem
   ([§8.2](#82-errors)) lack the header, and every other answer after the role check carries it,
   except a `401` or a `403 identity-revoked` found there, which drops it
   (`internal/api/problem.go:63-71`). A
   `recovery-mode-active` with a `scope` member is a scope's refusal and says nothing about the
   start mode.
2. **Normal start:** `GET /server` answering `"startMode": "normal"`, or the entry's
   not-a-recovery-start refusal, a `409 conflict` with neither
   an `epoch` nor a `request` member, whose detail says only a recovery start serves the entry. A
   `409 conflict` to the entry is never read as a normal start by its status and code alone.
3. **Unknown** before any such answer. The client then disables no action beforehand, and the
   recovery banner states the mode as unknown or as last shown, until the next such answer.

Evidence for each refusal and entry answer is in the G22 row of [§4](#4-api-gap-audit). Item 1 relies on the
absent `scope` member: [persistence and API §9.4](persistence-api.md#94-errors) gives one only to a
scope refusal, since contract correction C2 in
[§14](#14-proposed-api-gap-issues-and-downstream-refinement) was applied.

### 8.6 Event resumption

Facts: [§2](#2-the-api-a-client-meets) events row. The SSE stream sends no `retry:` field in the
frames it writes (`internal/api/operations.go:295-353`); between events it polls every 500 ms
(`internal/api/operations.go:210-212`); after 15 s with nothing sent it sends a `: keep-alive`
comment line ([persistence and API §8.3](persistence-api.md#83-the-resource-and-its-progress)).

**Proposed.**

1. A client follows an operation with the stream when it can and with JSON pages otherwise; both
   are complete. It remembers the last event ID it showed.
2. When the stream ends it reads `GET /operations/{id}`. A terminal state does not show that the
   last events arrived: the stream may have been cut after the state was written and before its
   last events were (the server writes the state after the last entry and ends the stream only
   once they are written, `internal/api/operations.go:233-234`,
   `internal/api/operations.go:270`). So on a terminal state the client drains the backlog
   first: it reads the JSON event pages to their end and shows every event after the last one it
   showed, then ends the watch. Any other state, `unresolved` included, means the stream ended because the token expired or the
   connection dropped: the client renews if needed and reconnects with `Last-Event-ID` and the
   renewed token, with backoff from 1 s to 30 s on repeated failures.
3. A `reset` event clears what the watch showed, prints that the installation was restored or
   entered recovery mode, and shows the replay from the start. The epoch change is handled as in
   [§8.5](#85-epoch-changes). The server sends `reset` only when a stream opens
   (`internal/api/operations.go:264-265`); a stream that stays open across a recovery entry goes
   on with event IDs carrying the new epoch (`internal/api/operations.go:278`). So the client
   compares the epoch part of every event ID (before the `:`) with the stream's
   `Bronzeward-Epoch`; on a difference it ends the stream, applies [§8.5](#85-epoch-changes), clears
   what the watch showed and reopens the stream without `Last-Event-ID` (EVT-05).
4. The browser cannot send `Authorization` with `EventSource`, so the web client reads the stream
   with `fetch` and a streamed response body, sending `Authorization`, `Accept` and
   `Last-Event-ID` itself (assumption S1). No token is ever put in a URL.
5. The keep-alive comment keeps an intermediary with an idle timeout above 15 s from cutting a
   quiet stream; a client ignores comment lines and keeps its last event ID. A connection still
   cut without the operation having ended is covered by item 2.
6. The machine timeline has no stream (G9). A client reads its newest page with `order=newest`
   and keeps that answer's `tail`; "Load older" follows `next` with `order=newest`. While the
   timeline is on screen the client polls forward from `tail` (from the start while there is
   none), in `oldest` order, no more often than every 5 s, follows `next` while it is present,
   and keeps the `tail` of the last page, the one without `next`. A server below API revision 3
   refuses `order` as `400 invalid-request` and serves no `tail`, so a client that reads the timeline this way declares
   revision 3 as its minimum.

## 9. Authentication

### 9.1 Identity-provider requirements

Facts: the server accepts a JWT access token signed with an asymmetric algorithm, with `iss`
equal to the configured issuer, `aud` containing the configured audience, `exp`, `iat` and a
non-empty `sub`, and `exp - iat` at most `maxTokenLifetime`, 15 minutes by default; it refuses
a token carrying `nonce` or `at_hash`, which mark an ID token, and one without a `jti`; roles
come from the configured groups claim, mapped in deployment configuration
(`internal/auth/auth.go:145-248`, `internal/config/config.go:121-125`,
`internal/config/config.go:353-356`). A disabled account or removed group takes effect when the
current token expires
([persistence and API §10.1](persistence-api.md#101-humans-oidc)).

**Proposed** deployment requirements, which the packaging ticket documents:

1. The identity provider issues **access tokens** that are JWTs carrying the audience, the groups
   claim, a `jti` and a lifetime no longer than `maxTokenLifetime`. An ID token is never sent to
   the API; the server refuses one ([persistence and API §10.1](persistence-api.md#101-humans-oidc)).
   Many providers set an access token's audience to the requesting client; the deployment
   configures an audience mapping or resource so `aud` contains the server's value.
2. Two public clients are registered, one for the CLI and one for the web interface, each using
   the authorization-code grant with PKCE `S256` and no client secret. The CLI client allows
   loopback redirect URIs with any port; the web client allows exactly the web origin's callback
   path. The CLI client also has the device-authorization grant enabled (decision D4 (a)).
3. Every case in which a client keeps a refresh token (the CLI's credential store of
   [§9.2](#92-cli-sign-in) item 3; its process memory under D2 (a), since a long wait renews
   in-process, [§9.5](#95-renewal) item 4; its token file under D2 (b); the web client's memory
   under D5 (a)) is conditional on refresh tokens issued to public clients rotating on every use
   and being bound to the client. Where the provider does not offer that, no client keeps a
   refresh token: the CLI keeps only the access token, in its credential store or memory, offers
   no token file and runs `login` again when the access token expires; the web client renews by
   the redirect of [§9.3](#93-browser-sign-in) item 3. This
   document sets no refresh-token policy itself. The provider's session length, not the client,
   bounds how long an operator stays signed in.
4. The web client's origin is allowed to call the provider's token endpoint cross-origin.

### 9.2 CLI sign-in

**Proposed.**

1. `bronzewardctl login` runs the authorization-code grant with PKCE `S256`, `state` and `nonce`,
   using a loopback redirect on `127.0.0.1` with a port the operating system chooses, and opens
   the system browser. If no browser can be opened it prints the URL for the operator to open on
   the same host. The listener accepts one callback, checks `state`, and closes.
2. Headless use, where no browser runs on the host, uses the device-authorization grant only
   (decision D4 (a)): the CLI prints the user code and the verification URI, shows the URI's host
   prominently so that a lookalike prompt stands out, and polls the token endpoint. There is no
   fallback to a loopback redirect over a forwarded port.
3. **Store.** The refresh token and the current access token are kept in the operating system's
   credential store (Secret Service, Keychain or Windows Credential Manager) under the context's
   name. Where no store is available (decision D2 (a) and (b)), the tokens are kept in memory for
   that process only and each new process runs `login`. An explicit per-context opt-in keeps them
   in a file readable only by its owner instead, offered only when the provider issues rotating,
   client-bound refresh tokens to public clients ([§9.1](#91-identity-provider-requirements)
   item 3). CLI processes that share that file renew one at a time, holding a lock on it while
   they renew, and read the new pair from it. The opt-in is the context's `oidc.tokenFile`
   ([§5](#5-endpoint-discovery-and-context-selection));
   only in a context that has it may `BRONZEWARD_TOKEN_FILE` override its path. The CLI writes
   that file and clears it as its own stored credential ([§8.2](#82-errors)), and refuses to
   write or clear a file that holds an automation token (`bwt_`).
4. No token is printed, written to the configuration file, passed in argv or placed in an
   environment variable by the CLI. No command prints a token.
5. `whoami` shows the issuer and subject from the token's claims, read without verification and
   labelled as the client's reading, and the expiry. Roles come from A1 (`GET /identity`,
   `internal/api/identity.go:39`).

### 9.3 Browser sign-in

**Proposed.**

1. The web interface is a public client using the authorization-code grant with PKCE `S256`,
   `state` and `nonce`, with a full-page redirect to the provider. The PKCE verifier, `state`,
   `nonce` and the page path to return to are held in one `sessionStorage` entry only between the
   redirect and the callback and are deleted when the callback is handled; this is the one storage
   use the web client makes, and it holds no token. The return path starts with `/` and not `//`,
   holds no backslash or control character, is never a full URL and is never the callback or
   signed-out page; any other is replaced by `/` (decision B2 of
   [#255](https://github.com/ginsys/bronzeward/issues/255)).
2. Access and refresh tokens are held in memory only, in a module the rest of the page cannot
   enumerate. They are never written to `localStorage`, `sessionStorage`, IndexedDB, the Cache
   API, a cookie or a service worker.
3. Under decision D5 (a), the access token is renewed silently with the rotating refresh token
   ([§9.5](#95-renewal)). A new tab receives the tokens from an open tab of the same origin over
   `BroadcastChannel` (assumption S3). A reload, or a first tab when none is open, makes a
   top-level redirect to the provider, which answers at once from its own session, without a
   prompt while that session lasts. A rotated refresh token is used once, so the open tabs do not
   renew separately: one tab renews and sends the new pair to every open tab over the same
   channel, and a tab that receives a pair drops its own renewal. Where the provider does not
   issue rotating, client-bound
   refresh tokens ([§9.1](#91-identity-provider-requirements) item 3), renewal also uses that
   redirect, never a hidden frame (item 4). That fallback redirects at every
   access-token expiry, the behaviour the owner's constraint in D5 refuses, so the constraint holds
   only with a rotating provider: rotation is a provider requirement
   ([§15](#15-assumptions-and-limits)). No token is kept in storage (item 2).
4. Silent renewal through a hidden frame is not used: browsers that block third-party cookies make
   it fail unpredictably (assumption S2), and a failure would surface as an unexplained sign-out.
5. Every API call goes to the page's own origin ([§10.1](#101-hosting-and-origin)); only the
   provider's authorization, token, revocation and end-session endpoints are called elsewhere.

### 9.4 Automation token input

Facts: an automation token is `bwt_<token id>.<secret>`, printed once by the server-side command
(`internal/auth/tokens.go:21`, `internal/auth/tokens.go:295-311`; [persistence and API §10.2](persistence-api.md#102-automation-bronzeward-issued-tokens)),
valid at most 90 days (`internal/auth/tokens.go:25`) and refused when revoked, expired or from
an earlier epoch (`internal/auth/automation.go:47-52`).

**Proposed.**

1. The CLI reads an automation token from a file (`--token-file` or the context's
   `automation.tokenFile`, or, in an automation context, a path in `BRONZEWARD_TOKEN_FILE`) or
   from standard input
   (`--token-stdin`). It never accepts the token as an argument value, nor the token itself in an
   environment variable (decision D6 (a)): `BRONZEWARD_TOKEN_FILE` holds a path, never a secret.
2. A token file must be a regular file readable only by its owner; otherwise the CLI refuses it
   and says why.
3. The CLI recognises an automation context by the `bwt_` prefix and says so before sending a
   request to a human-only route; the server's `403` stays authoritative.
4. The web interface accepts no automation token.
5. After a restore, automation tokens from the earlier epoch answer `401`; the CLI's message says
   the token must be reissued by the operator with the server-side command
   ([persistence and API §12.3](persistence-api.md#123-what-a-restored-state-means-record-by-record)).

### 9.5 Renewal

**Proposed.**

1. A client renews an access token when less than 60 s of its lifetime remains, by its own clock,
   and once after a `401` on a token it believed valid. It never retries a `401` more than once
   per request.
2. A renewal that returns a different `sub` or `iss` is a new sign-in: pending writes are set
   aside ([§8.4](#84-idempotency-and-ambiguous-responses) item 3) and the change is shown.
3. A failed renewal (refresh token rejected or expired, provider unreachable) leaves the current
   token in use until the server refuses it, then asks the operator to sign in again. The CLI in
   `--no-input` mode exits with the authentication-refused message instead of prompting.
4. Long waits (`operation watch`, `publish` waiting for its operation) renew in-process.

### 9.6 Failure cases

The server answers every token it does not accept with the same `401` and no detail
(`internal/api/api.go:314-322`, `internal/api/problem.go:76-79`). The client may add what it can
read locally, labelled as its own reading, never as the server's reason. The web interface shows
W1's text, "Not accepted by this installation. Sign in again.", for every `401 unauthenticated`
that its one renewal ([§9.5](#95-renewal) item 1) does not clear, with its local reading below it;
a `401` with another body follows [§8.2](#82-errors)'s rows. The texts below that name a context,
and the expired-token text, are the CLI's
(lead decision on consistency, 26 October 2026, under the owner's 2026-10-07 grant).

| Case | What the server does | Client behaviour (**Proposed**) |
| --- | --- | --- |
| Token expired | `401` (`internal/auth/auth.go:165-166`) | renew ([§9.5](#95-renewal)); if the provider refuses, the CLI says "Your sign-in expired: sign in again" |
| Wrong issuer | `401` | if the token's `iss` differs from `oidc.issuer`: "The token is from `<iss>`; this context expects `<issuer>`. Check the context or the server's configuration." |
| Wrong audience | `401` | if `aud` lacks `oidc.audience`: "The token is not issued for `<audience>`; the identity provider needs an audience mapping" ([§9.1](#91-identity-provider-requirements) item 1) |
| Lifetime over the maximum | `401` (`internal/auth/auth.go:175-176`); the maximum is the deployment's `maxTokenLifetime`, 15 minutes unless configured (`internal/config/config.go:125`, `internal/config/config.go:353-356`) | `GET /server` serves the maximum as `maxTokenLifetimeSeconds` since [#278](https://github.com/ginsys/bronzeward/issues/278), but only to an accepted token, so the client pre-checks against the value the context last read; with none read yet it reports the `401` as any other. If `exp - iat` exceeds it: "The token lives longer than this server accepts; the identity provider's access-token lifetime must be shortened" |
| An ID token, or no `jti` | `401` (`internal/auth/auth.go:177-180`) | if the token carries `nonce` or `at_hash`, or lacks `jti`: "The identity provider sent an ID token, or an access token without `jti`; the client or the provider's configuration is at fault" ([§9.1](#91-identity-provider-requirements) item 1) |
| Groups claim malformed | `401` (`internal/auth/auth.go:217-227`) | "Not accepted by `<context>`: sign in again"; nothing more can be read safely |
| No role for the action | `403 forbidden` with the route's roles | "Needs `<roles>`; you are signed in as `<sub>`" |
| A principal whose only qualifying role is publisher cancels a plan it did not create | `403 forbidden` with a `plan` member and the route's roles, `publisher` included (`internal/api/cancellations.go:64-72`) | "`<detail>`; you are signed in as `<sub>`", not the roles ([§8.2](#82-errors)) |
| Human-only route with an automation token | `403 forbidden`, `humanOnly: true` | "This needs a person signed in, not an automation token" |
| Group removed at the provider | takes effect when the current token expires ([persistence and API §10.1](persistence-api.md#101-humans-oidc)) | nothing to detect before; after renewal the next refusal names the roles |
| Account disabled at the provider | the current token is accepted until it expires; in the #162 experiment a refresh after disablement was refused ([talosctl-oidc live observations](../design/research/20261021-talosctl-oidc.md#live-observations), one provider only) | the failed renewal of [§9.5](#95-renewal) item 3 |
| Identity revoked in Bronzeward, or listed in `deniedSubjects` | `403 identity-revoked` (`internal/api/api.go:316-317`) for both. A recorded revocation is permanent (`internal/api/revocations.go:122`); a `deniedSubjects` entry is deployment configuration and lasts while the entry does (`internal/auth/auth.go:186-187`, `internal/auth/automation.go:45`) | "Your access was revoked or is denied by this installation; contact a recovery administrator." Clear the stored credential as in [§8.2](#82-errors), never an operator-supplied automation token file; no automatic new sign-in, which would be refused the same way. The client cannot tell the two causes apart |
| Automation token revoked, rotated or from an earlier epoch | `401` (`internal/auth/automation.go:47-52`) | "The automation token was not accepted: it may be revoked, rotated, expired or from before a restore. The operator reissues it with the server-side command." |
| Provider or database unreachable from the server | `503 dependency-unavailable`, nothing committed | retry as [§8.2](#82-errors) |
| Provider unreachable from the client | no request reaches the server | keep using the current token until it is refused; then "Cannot reach the identity provider `<issuer>`" |

### 9.7 Sign-out and cache clearing

Facts: the server keeps no session, so it cannot end one; an access token stays valid until its
expiry, at most `maxTokenLifetime` after issue, plus the server's 60 s leeway
(`internal/auth/auth.go:95`, `internal/auth/auth.go:165`); an identity revocation is permanent and is not a sign-out
(`internal/api/revocations.go:122`).

**Proposed.**

1. `bronzewardctl logout` deletes the context's tokens from the store and from its D2 (b) token
   file, if it has one, revokes the refresh token
   at the provider's revocation endpoint if it has one, and says that an access token already
   issued remains valid at the server for up to its remaining lifetime plus that leeway. The
   endpoint is the `revocation_endpoint` of the provider's discovery (RFC 7009, sent as a public
   client). The revocation request follows no redirect, so a 3xx answer is a refused revocation.
   Without a `revocation_endpoint`, logout says the refresh token is not revoked; a refused or failed
   revocation still deletes the tokens, says the refresh token is not revoked and exits 1.
2. The web sign-out discards the in-memory tokens and every cached response, revokes the refresh
   token if the provider allows it, tells other open tabs of the same origin to discard theirs
   (assumption S3), and redirects to the provider's end-session endpoint where it offers one, with
   `client_id` and `post_logout_redirect_uri` only. It sends no `id_token_hint`: the test
   provider's ID token carries the access token's issuer, audience and lifetime, so the server
   would accept it as a bearer token, and a URL keeps it in history and provider logs. Without the
   hint that provider sends the browser through its login UI's logout page. It then shows a
   signed-out page that holds no data.
3. After sign-out, the browser's back button shows no data from before: the web pages are served
   `Cache-Control: no-store`, the application clears its state on `pagehide` and on a page
   restored from the back-forward cache ([§10.3](#103-caches-and-back-navigation) items 3-4,
   assumption S8), and it renders nothing without a token.

## 10. Browser security

### 10.1 Hosting and origin

Fact: a cross-origin request carrying `Authorization`, `Idempotency-Key`, `If-Match` or a JSON
`Content-Type` makes the browser send a preflight `OPTIONS` without credentials (assumption S4);
this server authenticates every request but liveness, `OPTIONS` included, and answers `401`
(`internal/server/server.go:10-13`, `internal/api/api.go:260-272`). A browser on another origin
therefore cannot use the API today, and making it possible would add an anonymous path to the
server.

**Proposed**, under decision D1 (a): the web interface is a set of static files served from the
**same origin** as the API by the component that terminates TLS in front of the server, which routes
`/api/v1/` and `/livez` to the server and everything else to the static files. The server is not
changed, serves no files and sends no CORS header, and the API never accepts a cookie. The web
package ships the static files and the response headers the static host must send.

### 10.2 Script and content policy

**Proposed.** The static host sends, on every page:

- `Content-Security-Policy: default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self';
  connect-src 'self' <provider origins>; form-action <issuer origin>; frame-ancestors 'none';
  base-uri 'none'`, with `require-trusted-types-for 'script'` where supported. `<provider
  origins>` are the issuer's origin and the origins of the token and revocation endpoints its
  discovery document names, which may differ from the issuer's
  ([§9.3](#93-browser-sign-in) item 5);
- `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`,
  `Cross-Origin-Opener-Policy: same-origin`, `Cache-Control: no-store`.

The application loads no third-party script, font or analytics, and includes no telemetry or
remote error reporting. Every value from the API, configuration documents included, is inserted
as text, never as HTML; a YAML or diff view that highlights syntax builds its nodes from escaped
text. These limit, but cannot remove, the risk that an injected script uses the in-memory token:
any script running in the page can call the API as the operator.

**CSRF.** The API authenticates only by the `Authorization` header and never by a cookie, so a
cross-site request carries no credential. This holds only while no cookie authentication is
added; any future cookie needs its own CSRF design.

### 10.3 Caches and back navigation

**Proposed.**

1. Every API read from the web client uses `fetch` with `cache: 'no-store'`. The server sends
   `Cache-Control: no-store` on every API answer ([§2](#2-the-api-a-client-meets) caching row,
   served by A3 for G13), so a browser or an intermediary keeps nothing even from another client.
2. The application uses no service worker.
3. On `pagehide` and on sign-out, the application clears its in-memory state and every form field
   that holds a document or marks.
4. Pages are served `no-store`, so a page reloaded from history renders nothing until signed in
   again. A browser may still keep a `no-store` page in its back-forward cache (assumption S8);
   on a `pageshow` with `persisted` true the application discards any state and renders nothing
   until signed in again, so the guarantee rests on items 3 and 4, not on the header alone.

## 11. Protected input

### 11.1 What is protected

Plaintext can pass through a client in these places only:

| Input or output | Route | Why it is protected | Source |
| --- | --- | --- | --- |
| A fragment `document` | `PUT /drafts/{id}/fragments/{name}` | may hold values the extraction pipeline will move to the provider | keyed `document`, `internal/api/fragments.go:29-31`; [compilation §2](compilation.md#2-ingestion-extraction-before-persistence) |
| An ingestion `document` (`source: document`) | `POST /ingestions` | a full machine configuration with its secrets | keyed `document`, `internal/api/ingestions.go:24-26` |
| Marks | `POST /ingestions/{id}/marks`, `POST /ingestions` `marks`, `PUT /drafts/{id}/fragments/{name}` `marks` | a path can spell a secret mapping key | on the mark route `marks` is the keyed member (`internal/api/marks.go:67-68`); on the start route and the fragment write the keyed member is `document`, and `marks` stays in the canonical body that the same keyed fingerprint covers (`internal/api/ingestions.go:24-26`, `internal/api/fragments.go:37`, `internal/api/fragments.go:51-57`, `internal/api/body.go:170-172`); [compilation §3.6](compilation.md#36-operator-review) item 4 |
| The protected review | `GET /ingestions/{id}/review` | can hold a value nobody marked yet | `internal/api/review.go:24-31`; [compilation §3.6](compilation.md#36-operator-review) |
| Tokens | every request | credentials | [§9](#9-authentication) |

Not protected: rendered release reviews, plan diffs and provenance are redacted by the server
([compilation §8.3](compilation.md#83-redaction)) and are handled as ordinary reads.

The review is served to any human `author`, not only the one who started the ingestion
(`internal/api/routes.go:99`), as the contract intends: "only to the roles that may mark"
([compilation §3.6](compilation.md#36-operator-review)). A client does not narrow this.

### 11.2 Rules for both clients

**Proposed.**

1. **Argv and history.** No flag takes a protected value. Documents come from a file, standard
   input or a prompt; marks only from a prompt or standard input ([§11.3](#113-cli-review) item
   3); tokens from [§9.4](#94-automation-token-input). The documentation
   says that a shell here-document is saved in the shell's history like any command, so a document
   goes in a file.
2. **Logs and diagnostics.** Debug output (`-v`) prints method, path, status, `instance` and
   timing. It never prints `Authorization`, a request body of a keyed route, the review body, or
   marks. Error output repeats the server's problem document, which names no value
   ([persistence and API §9.4](persistence-api.md#94-errors)), and never the request.
3. **No telemetry** in either client, and no crash reporter that sends memory or arguments.
4. **No temporary files and no editor.** A client never writes protected input or the review to a
   temporary file and never opens `$EDITOR` on it. A document the operator edits in their own
   editor is the operator's file; the documentation says that editors keep swap and backup files
   the client cannot control.
5. **Core dumps.** The CLI disables core dumps for its own process before it reads protected input
   (on Linux, `RLIMIT_CORE` 0 and not dumpable). A panic handler prints a fixed message and no
   stack values.
6. **Memory.** Protected input is held no longer than the request needs. A garbage-collected
   runtime cannot guarantee erasure; this is a stated limit, not a guarantee.
7. **Clipboard and export.** No copy, download, save, print or share action on the review, on a
   document being submitted, or on marks.

### 11.3 CLI review

**Proposed**, qualifying the journeys' candidate C4 against
[compilation §3.6](compilation.md#36-operator-review):

1. `import review` shows the review only when standard output is a terminal, through an
   in-process pager that writes nothing to disk. It is refused, with no override, when standard
   output is not a terminal, and it has no `-o json`, `-o yaml`, save or export form.
2. Before showing it, the CLI states that terminal scrollback, session recording and screen
   capture are outside its control.
3. Marks come from the prompt or standard input. A refused mark is reported by its
   position only, as the server reports it, and a refusal can arrive two ways. A mark that is not
   a path at all is refused at once, `422 validation-failed` with a `position`
   (`internal/api/marks.go:42`). Every refusal compilation makes arrives after the `202`, as a
   `mark-refused` event on the ingestion's operation carrying the problem
   (`internal/api/runner.go:306-314`;
   [compilation §3.6](compilation.md#36-operator-review) item 4), and the ingestion returns to
   paused. After a mark's `202` (`{operation, ingestion, generation}`,
   `internal/api/marks.go:105-106`) the client therefore follows the operation's events
   ([§8.6](#86-event-resumption)) after the `marked` event whose `generation` equals the `202`'s
   (`internal/api/marks.go:102`), until the ingestion pauses again, ends, or the run records
   `resume-failed`. That event (`dependency-unavailable`) ends the watch without ending the
   operation: the staged envelope could not be decrypted, the claim stays `held` to its lease and
   the operation `running` (`internal/api/runner.go:212-231`;
   [persistence and API §9.3](persistence-api.md#93-examples)). The client reports the mark as
   not applied, and offers `import takeover` once the lease lapses, or `import abandon`
   ([compilation §3.6](compilation.md#36-operator-review) item 6). A run can also stop without
   writing any event, for example when the serving process is killed: the claim stays `held`
   and the operation `running` until the claim's absolute expiry
   (`internal/staging/sweep.go:10-13`, `internal/staging/sweep.go:134-138`), and the stream
   shows nothing. So while it follows, the client reads `GET /ingestions/{id}` again whenever
   the `leaseUntil` it last read has passed by its own clock, plus a margin for clock difference
   (`internal/api/ingestions.go:238`). A read that still shows `held` or `resumed` with
   `leaseUntil` passed ends the watch, with the operation still `running`, but the mark's
   outcome is reported as unknown, not as not applied. The run's closing transaction checks the
   lease once, under the claim's row lock, not again at `COMMIT`: a mark's is its pause,
   `mark-refused` pause or abandonment; a continuation's is its draft transaction releasing the
   claim, or an abandonment; a takeover's is either of those, or a pause while the claim's review
   is still `pending`, since a takeover of a marked claim resumes without the mark and pauses
   again (`internal/api/runner.go:243-253`;
   [compilation §3.5](compilation.md#35-expiry-abandonment-and-restoration);
   `internal/staging/claim.go:217-241`, `internal/api/runner.go:372-377`). So a run that passed
   that check before the lease lapsed can still commit, while this read takes no lock and shows
   the claim's earlier state, or `abandoned` once its absolute expiry has passed. The client
   offers a later read and the same offers as `resume-failed`. Only the operation settles the
   outcome: its terminal state; for a mark, the `paused` or `mark-refused` event at the `202`'s
   generation, or a `resume-failed` event after its `marked` event, which leaves the mark not
   applied (PROT-05); for a takeover, the `paused` event at its `taken-over` event's generation
   (`internal/api/ingestions.go:362`; its `202` names none); or a later takeover, seen as this
   client's own takeover `202` or as a `taken-over` event at a later generation than the watched
   run's, whichever client sent it, which fences the earlier run off (it takes the claim's row
   lock first, `internal/staging/takeover.go:65`). An earlier mark is then not applied, since the
   takeover resumes without it, and the operator marks again; an earlier continuation is carried
   on by the takeover's run (`internal/staging/mark.go:32-34`). For a mark, any outcome settled
   above without a `paused` or `mark-refused` event at the `202`'s generation is not applied, so
   a later `succeeded` is never read as the mark applied. A claim read `abandoned`, or a takeover's
   `409 conflict` "the ingestion has ended" (`internal/api/ingestions.go:340-341`), while the
   operation is still `running` settles nothing. A continuation's
   watch (`import continue`) and a takeover's (`import takeover`, which leaves the claim
   `resumed`, `internal/staging/takeover.go:84`) end the same two ways. It reports a
   `mark-refused` event's `422` as a refusal by position, never as an accepted mark; a
   `mark-refused` problem without a position (`503 dependency-unavailable` or `500
   internal-error`, `internal/api/runner.go:328-338`) is shown by its code as a mark that was not
   applied. The web interface does the same.

The qualification: the server's `no-store` and the client's refusal to persist limit what the
client keeps. They do not stop the operator, the terminal, a recording or the host from keeping
the text. The requirement is proposed with that limit stated.

### 11.4 Web review and document input

**Proposed.**

1. The review is fetched with `cache: 'no-store'`, rendered as text in a read-only element, and
   discarded on leaving the page, on `pagehide` and on sign-out.
2. Document and marks fields set `autocomplete="off"`, `spellcheck="false"`, `autocorrect="off"` and
   `autocapitalize="off"`, so browser spelling services do not receive their text (assumption S5).
3. No draft of a document or of marks is kept between page loads.
4. Browser extensions can read the page; the interface cannot prevent this and says so in its
   documentation.

### 11.5 Test artifacts

**Proposed.** Client tests use synthetic values only, and every retained artifact is scanned for
them as the fixtures already do for evidence
([fixtures, synthetic secrets](../../fixtures/README.md#synthetic-secrets);
[acceptance plan](acceptance-plan.md) line 50 describes the scan with a positive control):

1. CLI process tests capture standard output, standard error and the process's argument list, and
   the scan includes all three. The one exception is a protected review (PROT-01): it runs on a
   pseudo-terminal, and its output is matched in memory and never retained (the fixtures' review
   rows match the route's answer the same way; [fixtures README](../../fixtures/README.md), S1
   operator review rows).
2. Browser tests never retain a trace, HAR, video or screenshot of a protected review or of a page
   holding a document; the other pages may keep them, as the web ticket requires. The scan covers
   every retained file and fails on any synthetic value, with a positive control that plants one.
3. Tests also check that browser storage (local, session except during the redirect, IndexedDB,
   Cache API) and the HTTP cache hold no synthetic value after each protected journey.

## 12. Shared conformance cases

**Proposed.** One set of cases both clients run, so their behaviour cannot drift apart. Each case
is a language-neutral record:

```yaml
id: AMB-02
setup: draft at revision 3; signed in as h-author        # fixture state, synthetic
action: fragment set worker-labels --file <synthetic file>
exchange:                                               # what the client must send
  - PUT /api/v1/drafts/{draft}/fragments/worker-labels
    headers: [Authorization, Idempotency-Key, If-Match: <draft ETag>, Bronzeward-Expected-Epoch: <reference epoch>]
fault: drop the connection after the request body is sent
expect:
  client: repeats with the same key and same body; on the in-progress 409 repeats again with backoff
  server: replay with Idempotent-Replayed, or the first execution
  shown: "saved" with draft revision 4; exit 0
  absent: [synthetic value in stdout, stderr, argv, logs, storage]
```

Cases run against a disposable server and the fixture issuer, with an injector between client and
server for faults. Interactive sign-in cases need an issuer with authorization endpoints (G19).

| ID | Area | Case | Expected |
| --- | --- | --- | --- |
| AUTH-01 | success | sign in, read machines | `Bronzeward-Epoch` recorded; list shown |
| AUTH-02 | refusal | each fixture defect `expired`, `wrong-issuer`, `wrong-audience`, `over-lifetime`, `alg-none`, `groups-not-array`, `id-token-nonce`, `id-token-at-hash`, `no-jti` (`fixtures/oidc/issuer/issuer.go:40-49`) | `401`; one renewal attempt at most; the client's local reading labelled as such |
| AUTH-03 | refusal | token without the needed group (`-without-group`) | `403 forbidden`; roles named |
| AUTH-04 | refusal | automation token on approve | warned before sending that the route is human-only ([§9.4](#94-automation-token-input) item 3); the request sent is answered `403 forbidden` with `humanOnly`, shown as in [§9.6](#96-failure-cases) |
| AUTH-05 | refusal | identity revoked | `403 identity-revoked`; stored credential cleared; no new sign-in loop |
| AUTH-06 | success | token expires during `operation watch` | renewed; stream resumed with `Last-Event-ID` |
| AUTH-07 | refusal | renewal returns a different subject with a write pending | write set aside; change shown |
| AUTH-08 | success | sign-out, then back navigation (web) | no data shown; storage empty |
| AUTH-09 | success | `bronzewardctl logout` | tokens gone from the credential store and from a D2 (b) token file; refresh token revoked where the provider allows; remaining access-token validity stated |
| AUTH-10 | refusal | subject listed in `deniedSubjects` | `403 identity-revoked`; same handling as AUTH-05 |
| ETAG-01 | refusal | another author writes first | `412`; no resend; newer state shown |
| AMB-01 | disconnect | connection dropped before any request byte is sent | ordinary retry |
| AMB-02 | disconnect | fragment write dropped after the body is sent | repeat with the same key; an in-progress `409` (with `ingestion`, no `draft`, not replayed) is repeated with backoff up to the client's wait bound, then outcome unknown; a `409` with `ingestion` and `draft` (draft published meanwhile) is reported refused, not repeated; otherwise replay or first execution |
| AMB-03 | refusal | `500 internal-error` | repeat with the same key; replayed result |
| AMB-04 | refusal | first attempt committed, its answer dropped; CLI exits with outcome unknown; operator repeats with the printed values and a changed file (existing record) | `422 idempotency-key-reused`; reported as: this send did nothing, the original outcome is still unknown; resource read |
| AMB-10 | disconnect | first attempt dropped by the injector before the server receives it; CLI exits with outcome unknown; operator repeats with the printed values and a changed file (missing record) | the changed input executes as a new request; the client reports this send's answer as its own result, never as a replay or as the original outcome; the case records that no client check prevents it ([§8.4](#84-idempotency-and-ambiguous-responses) item 4) |
| AMB-11 | refusal | as AMB-04 or AMB-10, with the printed `--if-match` omitted, with another principal signed in, or after a recovery entry changed the epoch from the printed `--expect-epoch` | refused by the client before sending the write; without `--if-match` or under another principal nothing is sent; in the epoch variant only the confirming read is sent ([§8.4](#84-idempotency-and-ambiguous-responses) item 3); the write is not sent |
| AMB-13 | refusal | as AMB-04 or AMB-10, with `--if-match` given a freshly read ETag instead of the printed one | not detectable by the client ([§8.4](#84-idempotency-and-ambiguous-responses) item 4); with the first attempt's record: `422`, as AMB-04; without it: executes when the ETag matches the current resource, reported as in AMB-10, else `412`, reported as: this send did nothing, the original outcome is still unknown; resource read; the case records that no client check prevents it |
| AMB-05 | restore | write dropped, then recovery entry before the repeat | epoch confirmed by a read before repeating; it differs, so not repeated; resource read; operator decides |
| AMB-06 | refusal | keyed repeat after the digest key's recorded version became uncomputable | `422`; reported as unknown outcome; resource read; no new key sent |
| AMB-07 | restore | signed in as a human principal (a service principal's repeat is refused `401` instead, [§8.4](#84-idempotency-and-ambiguous-responses) item 3); write dropped; recovery entry injected after the confirming read and before the repeat, so the repeat meets an earlier-epoch record | `409 conflict` with `request` and `epoch`; no new key sent; resource read |
| AMB-12 | restore | as AMB-07, but the first attempt's record is missing: a restore to a backup taken before it removed it, or the first attempt never committed (G21) | the repeat carries the first attempt's epoch in `Bronzeward-Expected-Epoch` and is refused `412 epoch-mismatch` naming the current epoch, with no act or idempotency record committed (A6, [#293](https://github.com/ginsys/bronzeward/issues/293)); resource read |
| AMB-08 | refusal | write answered `503 dependency-unavailable`, e.g. ingestion start with no provider configured | repeated at most 3 times with the same key; then reported not done |
| AMB-09 | disconnect | write dropped after the body is sent; every repeat answered `503` | not reported as not done: outcome unknown, key and resource printed as in §8.4 item 4 |
| PAGE-01 | success | list over three pages | every item once, in order |
| PAGE-02 | restore | cursor used after recovery entry | `400 cursor-invalid`; list restarted; notice shown |
| EVT-01 | disconnect | stream cut mid-operation | reconnect with `Last-Event-ID`; no event shown twice |
| EVT-02 | restore | `Last-Event-ID` from the earlier epoch | `reset`; display cleared and replayed |
| EVT-03 | success | operation ends `unresolved` then `cancelled` by resolution | watch continues past `unresolved`; ends at `cancelled` |
| EVT-04 | disconnect | stream cut after the operation's terminal state is written and before its last events are sent | the operation read shows the terminal state; the client drains the JSON event pages, shows the missing events once, then ends the watch |
| EVT-05 | restore | recovery entry, on another process over the same database, while a stream stays open | the first event ID with another epoch ends the stream; §8.5 applied; display cleared; stream reopened without `Last-Event-ID` |
| REC-01 | restore | recovery mode on | banner; refusals name scope or process restriction |
| REC-02 | refusal | recovery entry sent to a normal start; then to a recovery start whose entry committed under another key | first: `409 conflict` without `epoch` or `request`, read as a normal start; second: `409 conflict` with `epoch`, read as a recovery start, never as a normal one |
| REC-03 | refusal | plan creation on a recovery start after its entry; plan creation on a pre-restore unaccounted scope of a normal start | first: `409 recovery-mode-active` without `scope`, read as a recovery start; second: with `scope`, start mode unchanged |
| REC-04 | disconnect | recovery entry commits and its answer is lost: (a) the injector drops the `201`, and the confirming read answers `200` with the new epoch; (b) the server loses its own COMMIT reply and answers `500`, and the confirming read is refused `409 recovery-mode-active`; (b) needs a server-side fault hook, not the injector, and the server binary has none (the test-only `commit` option, `internal/api/api.go:131`, `:190`): its server side is covered by `internal/api/recovery_entries_test.go:255-296`, and its client side waits for that hook, a prerequisite of #263 | both: repeated under the same key with no epoch check; `201` with `Idempotent-Replayed: true` and the minted epoch; §8.5 applied without setting the entry aside |
| PROT-01 | success | protected import, review, mark, continue | review only on a terminal (CLI; a pseudo-terminal in tests, its output matched in memory and never retained, [§11.5](#115-test-artifacts) item 1); no synthetic value in any artifact or storage |
| PROT-02 | refusal | `import review` with standard output redirected | refused, nothing written |
| PROT-03 | refusal | mark that is not a path; mark compilation refuses (for example one that would rewrite text); compilation fails on a dependency | first: `422` with `position`; second: `202`, then, after the `marked` event of that `generation`, a `mark-refused` event with `422` and `position` and the ingestion paused again; third: `mark-refused` with `503`, shown by code as not applied; none reported as accepted, no path shown |
| PROT-05 | refusal | mark accepted (`202`), then the run cannot decrypt the staged envelope | after the `marked` event, a `resume-failed` event with `dependency-unavailable`; the watch ends with the operation still `running`; the mark reported as not applied; `import takeover` offered once the lease lapses, and `import abandon`; no wait without end |
| PROT-06 | disconnect | mark accepted (`202`), then the serving process is killed before the run writes any event; second case: the same during a continuation; third case: the same during a takeover; fourth case: during a continuation, the run releases the claim before its lease lapses and its draft transaction is delayed past `leaseUntil` before `COMMIT`, which needs a server-side fault hook the server binary does not have, a prerequisite of #263 | the client reads the ingestion once its `leaseUntil` has passed; `held` (mark, continuation) or `resumed` (takeover) with `leaseUntil` passed ends the watch with the operation still `running`; the mark, continuation or takeover reported as of unknown outcome, never as not applied; a later read, `import takeover` and `import abandon` offered; in the fourth case the outcome stays unknown, even when a read shows the claim `abandoned`, until a later read shows the operation `succeeded`; no wait until the claim's absolute expiry |
| PROT-04 | disconnect | connection dropped during the review read | nothing persisted; review read again |
| COMP-01 | refusal | `GET /server` serves an API revision below the client's minimum | refused before any write, both revisions named |
| COMP-02 | success | response carrying a `Deprecation` header | warning shown once, naming the route |
| ETAG-02 | success | edit after the client's own import succeeded; second case: another author writes between the import and the re-read | first: draft `revision` equals the import's `result.draftRevision`, the next write uses the new ETag, no `412`; second: revision is higher, treated as `412`, newer state shown |
| TLS-02 | refusal | API answers a `3xx` to another origin, to the CLI and to the web client; harness check | the injector sees no request to the target and nothing resent by either client; the CLI names the target |
| TLS-03 | refusal | as TLS-02, to the web client, for a read and for a write; browser message check | the page shows the generic transport-failure message naming a refused redirect as a possible cause and no target; the write is repeated with the same key as in §8.4 item 2 and, when every repeat meets the same redirect, reported as an unknown outcome with its key and the resource to read, never as not done |
| TLS-01 | refusal | certificate from an untrusted CA | nothing sent; host and trust source named |

## 13. Alternatives and open choices

D3, D7, D8 and D9 are decided by the lead on 2026-10-21, under the owner's 2026-10-07
delegation, as their rows state. The owner decided D1, D2, D4, D5, D6 and D10 on 2026-10-22, at
the times their rows give; D10's option (a) is served by
[#293](https://github.com/ginsys/bronzeward/issues/293).

| ID | Choice | Options | Decision and reason |
| --- | --- | --- | --- |
| D1 | Web hosting and origin | (a) static files on the API's origin via the TLS terminator; (b) another origin with CORS added to the server; (c) the server serves the files; (d) backend-for-frontend | **Decided** by the owner (Serge, 2026-10-22 09:50 CEST): (a). The TLS front end routes `/api/v1/` and `/livez` to the server and serves the static web files for everything else ([§10.1](#101-hosting-and-origin)). Reason: no server change, no anonymous route, no cookie. Rejected: (b) adds an unauthenticated `OPTIONS` path and (c) an unauthenticated file route, both against "every request authenticates"; (d) is excluded by the issue unless decided explicitly |
| D2 | CLI token store without an OS credential store | (a) memory only, sign in per process; (b) file readable only by its owner, opt-in per context; (c) file by default | **Decided** by the owner (Serge, 2026-10-22 09:58 CEST): (a) by default, with (b) as an explicit per-context opt-in, offered only if the provider issues rotating, client-bound refresh tokens to public clients ([§9.1](#91-identity-provider-requirements) item 3, [§9.2](#92-cli-sign-in) item 3). The opt-in is the context's `oidc.tokenFile`, whose path `BRONZEWARD_TOKEN_FILE` may override in that context only. (a) also holds a refresh token in process memory, which [§9.1](#91-identity-provider-requirements) item 3 conditions on rotation as well. Reason: a refresh token on disk is a long-lived credential, so keeping one there is an operator's visible choice per context. Rejected: (c), which puts one on disk without that choice |
| D3 | How a client learns issuer, client ID and audience | (a) by hand in the context; (b) a static client-configuration file served next to the web files; (c) an anonymous API route | **Decided** by the lead, 2026-10-21, under the owner's 2026-10-07 delegation: (a) now, and (b) if D1 is decided (a). Reason: under same-origin hosting the web client needs the file anyway, and the CLI can import it with `context add --from <url>` under TLS; (c) adds a second anonymous route. D1 is decided (a), so (b) applies, and entering the values by hand stays possible ([§5](#5-endpoint-discovery-and-context-selection) item 4) |
| D4 | CLI sign-in without a local browser | (a) device-authorization grant; (b) loopback over a forwarded port; (c) both | **Decided** by the owner (Serge, 2026-10-22 10:04 CEST): (a) only, with no forwarded-loopback fallback ([§9.2](#92-cli-sign-in) item 2). Reason: device flow suits SSH sessions; it is phishable by a lookalike prompt, so the CLI shows the verification URI's host prominently. Rejected: (b) and (c) |
| D5 | Browser renewal and new tabs | (a) access and refresh tokens in memory only, renewed silently by the rotating refresh token; a new tab receives them from an open tab over `BroadcastChannel`; a reload or first tab makes a top-level redirect to the provider, which answers from its own session without a prompt; no token in `localStorage`, `sessionStorage` or IndexedDB; without rotation, renewal by that redirect, never a hidden frame; (b) a refresh token bound by DPoP, kept in browser storage; (c) an authentication proxy that holds the session and gives the browser a cookie | **Decided** by the owner (Serge, 2026-10-22 10:20 CEST): (a) ([§9.3](#93-browser-sign-in) item 3). The owner's constraints (10:10 CEST): "reload and new tab is not acceptable / re-auth manually every 15m is not acceptable". Reason: (a) meets both without writing a token to storage. Rejected: (b), which keeps a credential in storage; (c), a stateful component in front of an API that accepts no cookie ([§10.1](#101-hosting-and-origin)). An earlier set of options, (b) silent frame renewal and (c) a redirect at every expiry, was replaced: the first breaks under third-party cookie blocking (assumption S2), the second interrupts work every 15 minutes. (a)'s fallback for a provider without rotation behaves as that second option did, so the constraints hold only with a rotating provider, which §15 lists as a provider requirement |
| D6 | Automation token in an environment variable | (a) not accepted, file or stdin only; (b) accepted as `BRONZEWARD_TOKEN` | **Decided** by the owner (Serge, 2026-10-22 09:59 CEST): (a). The path variable `BRONZEWARD_TOKEN_FILE` is accepted; a secret in an environment variable never is ([§9.4](#94-automation-token-input) item 1). Reason: an environment variable is inherited by every child process and readable by the same user's processes; CI systems can write the token to a file first. Rejected: (b), the common CI convention |
| D7 | Generated or handwritten clients | (a) both handwritten; (b) an OpenAPI description of v1 in the repository, checked against the route table by a test, generating request and response types for both clients, with handwritten behaviour (idempotency, ETags, epochs, renewal); (c) fully generated clients | **Decided** by the lead, 2026-10-21, under the owner's 2026-10-07 delegation: (b), generating types only; all behaviour stays handwritten. Reason: the types stay aligned with the server while the behaviour this document specifies stays explicit and tested by the shared cases; no OpenAPI description existed then (search of the repository); `api/openapi.yaml` now provides it ([#275](https://github.com/ginsys/bronzeward/issues/275)). The Go CLI generates its types too rather than reusing the server's ([#254](https://github.com/ginsys/bronzeward/issues/254) decision T1, 2026-10-25; [§15](#15-assumptions-and-limits)) |
| D8 | Conformance case format and home | (a) case records in the repository run by both clients' harnesses; (b) separate suites per client | **Decided** by the lead, 2026-10-21, under the owner's 2026-10-07 delegation: (a), as [§12](#12-shared-conformance-cases) |
| D9 | Idempotency key metadata across a CLI restart | (a) no local journal: on giving up, the CLI prints the key, route, principal, epoch and `If-Match`, and the operator repeats with them and the same input ([§8.4](#84-idempotency-and-ambiguous-responses) item 4); (b) a local journal of pending keys, owner-readable, replayed on the next run | **Decided** by the lead, 2026-10-21, under the owner's 2026-10-07 delegation: (a). Reason: a journal would hold request context on disk and need its own expiry and epoch rules. Its cost is that the operator must supply the exact input again: where no record exists, a changed input executes (AMB-10), so an operator who cannot reconstruct it reconciles from the resource; (b) avoids that reconstruction only if the journal holds the request itself, documents included, which on the keyed routes (fragment write, ingestion start, mark) would write protected plaintext to disk against [§11.2](#112-rules-for-both-clients) item 4 and the secret-ingress boundary. So (b) could journal request bodies only for non-keyed routes, and for the keyed ones offers nothing over (a) |
| D10 | Epoch check on a human's repeated write (G21) | (a) server-enforced: mutations accept an expected-epoch request header and refuse, committing no act or idempotency record, when it differs from the current epoch, giving humans an epoch check like the one service tokens already get (API issue A6); (b) client-only check of [§8.4](#84-idempotency-and-ambiguous-responses) item 3 for humans, with the race between its confirming read and the repeat explicitly accepted | **Decided** by the owner (Serge, 2026-10-22 08:43 CEST): (a), served by [#293](https://github.com/ginsys/bronzeward/issues/293). Reason: it closes the race at the only place that knows the epoch at commit time, and keeps the earlier-epoch `409` of AMB-07 because, unlike the service check, which refuses before any record is answered (`internal/api/idempotency.go:47-49`), it runs only when a lookup finds no record and refuses only under the key's lock: the write's own, a fragment write's claim transaction's or the first lookup's fresh read (`internal/api/idempotency.go:52-58`, `internal/api/tx.go:204-210`, `internal/api/fragments.go:95-103`, `internal/api/tx.go:71-81`, as A6). Rejected: (b), which needs no server change and leaves a short window, but a human's repeat that lands in it after a restore removed the record, or whose first attempt never committed, executes afresh, with no error to any party |

## 14. Proposed API-gap issues and downstream refinement

This document creates no issue. The owner decides which to open and links them natively.

**A1. Serve the caller's identity and roles.** An authenticated `GET` that answers the
principal's kind, issuer and subject or service name, the principal ID when one exists, the roles
the credential maps to, the credential's expiry and the current epoch. It must answer a
credential that maps to no role, which the current role check would refuse
(`internal/api/api.go:385-391`), so it needs a decision on whether a role-less identity may read
its own identity. Closes G1; blocks the CLI and web sign-in tickets' role display. Served since
[#277](https://github.com/ginsys/bronzeward/issues/277): `GET /identity` in [persistence and API §9.2](persistence-api.md#92-resources-and-routes),
and the role-less read decided in [§10.3](persistence-api.md#103-authorization).

**A2. Serve the server version, API revision and installation settings.** An authenticated
`GET` with the server's version, the API revision within v1, the optional features it serves, and
the read-only settings a client shows: `maxTokenLifetime`, the settle floor, the maximum transport
deadline, the ingestion claim's lease and absolute expiry, and the plan defaults; and the serving
process's start mode, normal or recovery. Closes G2, G18 and G22; gives the in-progress `409` wait of [§8.4](#84-idempotency-and-ambiguous-responses) item 2
a server-derived bound; lets clients refuse an incompatible server
(the CLI ticket's acceptance criterion). Served since
[#278](https://github.com/ginsys/bronzeward/issues/278): `GET /server`, any role, in
[persistence and API §9.2](persistence-api.md#92-resources-and-routes), and the API revision and
its table in [§9.1](persistence-api.md#91-conventions). It describes the process that answered.
A recovery start refuses it before its entry commits, as every request but the entry, so G22
stays open for that window ([§8.5](#85-epoch-changes)).

**A3. Response headers for clients.** `Cache-Control: no-store` on every authenticated response,
a request-ID response header equal to the problem `instance`, and keep-alive comments on the event
stream at a fixed interval. Closes G13 and G16, and the idle-cut risk in
[§8.6](#86-event-resumption). Served since [#279](https://github.com/ginsys/bronzeward/issues/279).

**A4. Navigation reads for the journeys.** A list of ingestions (filterable by state and machine),
the ingestion named in its operation's `subject`, and list filters: plans by `state` and
`machine`, machines by `cluster`; and a member on the machine stating whether it is pending
convergence, computed from the digests the server already compares. Closes G3, G10, G24 and the
first part of G9. Served since [#280](https://github.com/ginsys/bronzeward/issues/280): the routes
and filters in [persistence and API §9.2](persistence-api.md#92-resources-and-routes), the ingest
`subject` in [§8.3](persistence-api.md#83-the-resource-and-its-progress) and `pendingConvergence`
in [§9.3](persistence-api.md#93-examples); a machine with a `Desired` release and no `Applied`
state reads as pending convergence.

**A5. A sign-in-capable test issuer.** Add a pinned Zitadel, the reference provider of
[§15](#15-assumptions-and-limits), to the fixtures
([#281](https://github.com/ginsys/bronzeward/issues/281)), with authorization-code and PKCE, refresh with rotation, revocation,
end-session and device authorization, which D4 (a) makes the only headless sign-in, so client sign-in is tested against an endpoint
and not a minted token. Closes G19; blocks the sign-in tickets' verification. Served since
[#281](https://github.com/ginsys/bronzeward/issues/281), with the residuals listed in G19
([fixtures README](../../fixtures/README.md#test-identity-provider)).

**A6. An expected-epoch precondition on mutations** (D10 option (a); served by
[#293](https://github.com/ginsys/bronzeward/issues/293)). Every mutating route accepts an
optional `Bronzeward-Expected-Epoch` naming the epoch the client expects, checked only when the
idempotency lookup finds no record, and refused only by a lookup under the key's lock: the
write's own, a fragment write's claim transaction's or a fresh one (`internal/api/idempotency.go:52-58`,
`internal/api/tx.go:204-210`, `internal/api/fragments.go:95-103`, `internal/api/tx.go:71-81`),
so an earlier-epoch record
still answers its `409` (AMB-07); a mismatch is refused `412 epoch-mismatch` naming the current
epoch and commits no act or idempotency record, while what the route committed before that lookup,
a fragment write's claim, a scope mark's observation or a principal row, stays
([persistence and API §7.2](persistence-api.md#72-behavior)). It gives human
principals an epoch check like the one service tokens get, but placed differently: the service
check refuses a stale token before any record is answered
(`internal/api/idempotency.go:47-49`). Both clients send it on every write and every repeat,
except the recovery entry, neither its first send nor a repeat: its first send is already
refused by its own first step when another entry has committed since the process started
(`internal/api/recovery_entries.go:133-135`), and its own commit changes the epoch, which
[§8.4](#84-idempotency-and-ambiguous-responses) item 3 lets its repeat cross. The server's
own-entry check still bounds that repeat, and its residual (a repeat with no record becoming a
later start's entry) stays open. Closes G21.

**A7. Principal display names.** A way to show a principal identifier as a person or service:
either a read that maps an identifier to its kind and issuer and subject or service name, or that
name served next to the identifier in the bodies that carry one (acts, releases, plans,
approvals, recovery entries). Which identities a caller may resolve is part of the issue. Closes
G23; until then clients show shortened principal identifiers. Specified by
[#335](https://github.com/ginsys/bronzeward/issues/335): `GET /principals`, any role, mapping up
to 100 identifiers in one read, in [persistence and API §9.2](persistence-api.md#92-resources-and-routes);
the batch read was chosen over names in the bodies (choice
[§17.40](persistence-api.md#17-choices-for-owner-review)). A client resolves the identifiers a
view or an output shows in requests of at most 100 distinct identifiers each, and shows a human by subject and issuer, a service by its
name, and the shortened identifier for one answered `unknown` or when the read fails. Whether a
human also has a display name is pending ([persistence and API §10](persistence-api.md#10-authentication-and-authorization)).

**C1. Contract correction.** Add the `reset` event to the stream contract of
[persistence and API §8.3](persistence-api.md#83-the-resource-and-its-progress) (G15). A
documentation change for the landing of this specification or its own small change. Applied: both
lists name it.

**C2. Contract correction.** The `recovery-mode-active` row of
[persistence and API §9.4](persistence-api.md#94-errors) said the body names the scope, but the
process refusals of a recovery start, before and after its entry, carry no `scope` member
(`internal/api/api.go:242-244`, `internal/api/recovery_exits.go:211-213`). The code is right: a
process refusal concerns no scope. State that only the scope refusals name the scope (G22,
[§8.5](#85-epoch-changes)). A documentation change, like C1. Applied: the row says so.

**L1. Later design issue: draft render and field provenance.** A render of a draft before
publication, and provenance that says which fragment set a field, are compilation design
changes outside this milestone (G5, G6). Proposed as one issue for a later milestone; no client
ticket waits for it.

**L2. Later design issue: serving observed configuration.** Whether and how drift content is
served is a redaction design question outside this milestone (G7); until it is settled clients
use a drift-adoption import with review.

**Downstream refinement.** Each client ticket cites this document's
sections as its contract, and its `status/needs-refinement` label is lifted only with the
dependencies below in place:

| Ticket | Depends on | Sections |
| --- | --- | --- |
| [#254](https://github.com/ginsys/bronzeward/issues/254) CLI sign-in, contexts, inventory | A1, A2, A5, D2-D4, D6, D9, D10 | 5, 6, 7, 8, 9, 12 |
| [#255](https://github.com/ginsys/bronzeward/issues/255) web sign-in, navigation, inventory | A1, A2, A3, A5, D1, D3, D5, D10 | 5, 7, 8, 9, 10, 12 |
| [#256](https://github.com/ginsys/bronzeward/issues/256), [#257](https://github.com/ginsys/bronzeward/issues/257) import, drafts, publication | A4, D9 (CLI), D10 | 8.3, 8.4, 11 |
| [#258](https://github.com/ginsys/bronzeward/issues/258), [#259](https://github.com/ginsys/bronzeward/issues/259) plans, approval, operations | A4 (plan filters), D9 (CLI), D10 | 8.4, 8.6 |
| [#260](https://github.com/ginsys/bronzeward/issues/260), [#261](https://github.com/ginsys/bronzeward/issues/261) drift and recovery | A2 (settings, start mode), D9 (CLI), D10 | 8.4, 8.5, 8.6 |
| [#262](https://github.com/ginsys/bronzeward/issues/262) packaging and documentation | D1, D3, D7 | 6, 7, 9.1, 10.1, 10.2 |
| [#263](https://github.com/ginsys/bronzeward/issues/263) validation | A5, D8; a server-side COMMIT fault hook for REC-04 (b); a server-side hook delaying a draft transaction's `COMMIT` after its claim's release for PROT-06's fourth case | 11.5, 12 |

A7 blocks no ticket: until it lands, the tickets from #256 to #261 show shortened principal
identifiers (G23). A4's convergence member, served since #280, did not block the inventory views of
#254 and #255 either (G24).

## 15. Assumptions and limits

Assumptions this document relies on without a citation in this repository:

- **S1.** The browser `EventSource` interface cannot send an `Authorization` header; a streamed
  `fetch` response can (WHATWG HTML and Fetch standards).
- **S2.** Browsers that block third-party cookies make hidden-frame OIDC renewal unreliable.
- **S3.** Tabs of one origin can signal each other with `BroadcastChannel`.
- **S4.** A cross-origin request with an `Authorization` header, a custom header or a JSON content
  type is preceded by a preflight `OPTIONS` sent without credentials (Fetch standard, CORS
  protocol).
- **S5.** Browser spelling services can send field text to a remote service unless spell checking
  is off for the field.
- **S6.** The identity providers operators run can issue JWT access tokens with a configurable
  audience and groups claim, PKCE public clients and rotating refresh tokens. One provider was
  exercised for Talos access
  ([talosctl-oidc research](../design/research/20261021-talosctl-oidc.md#pinned-sources-and-fixture)),
  and #281 exercised Zitadel v4.19.5 for this client's grants
  ([live checks](../design/research/20261022-zitadel-test-idp.md#outcomes)); no other release or
  product was.
- **S7.** A browser `fetch` follows a redirect by default and resends the method and body on a
  `307` or `308`; `redirect: 'error'` makes any redirect a network error instead (Fetch standard).
- **S8.** `Cache-Control: no-store` does not keep every browser from restoring a page from its
  back-forward cache; a page restored that way receives `pageshow` with `persisted` true (HTML
  standard).

Implementation choices the owner made on 2026-10-22:

- **CLI language** (Serge, 10:23 CEST): Go, as a second binary, `cmd/bronzewardctl`, beside the
  server's `cmd/bronzeward`. Rejected: TypeScript shared with the web client; Rust or Python.
  D7 (b) generates both clients' types from the OpenAPI description. The Go CLI does not reuse
  the server's Go types: decision T1 of [#254](https://github.com/ginsys/bronzeward/issues/254)
  (the lead, 2026-10-25, under the owner's 2026-10-07 delegation) keeps D7 (b) for the CLI. The
  types are generated by oapi-codegen v2.8.1 into `internal/client/apitypes` (`mise run
  generate`), and `mise run go` fails when the generated file is stale. Reasons: the server's
  response types are unexported, and importing `internal/api` would link the server's provider,
  Talos and database access into the CLI.
- **Frontend framework** (Serge, 10:27 CEST): TypeScript and React, built with Vite into static
  files, with no server-side rendering, as D1 (a) implies. Dependencies follow a tight policy:
  pinned versions, no large UI kit, updates through Renovate. The API types are generated from
  the OpenAPI description, as D7 (b) states. The operation stream is read with `fetch`, because
  `EventSource` cannot send an `Authorization` header (assumption S1). Rejected: Svelte; Lit web
  components or no framework.
- **Browser support matrix** (Serge, 11:58 CEST): current desktop browsers, the last two major
  versions of Chrome, Edge and Firefox, plus Firefox's current extended-support release, and
  Safari 16 or later. Mobile browsers may work but are not a tested target. Reason: it covers desk
  use and every browser feature D5 (a) needs: `BroadcastChannel`, in Safari since 15.4, and
  streamed `fetch` and WebCrypto, in all of them. Rejected: mobile Safari and Chrome as tested
  targets, because responsive layouts across eleven screens cost too much for the PoC; a single
  browser.
- **Reference identity provider** (Serge, 10:33 CEST): Zitadel, for the test issuer
  ([#281](https://github.com/ginsys/bronzeward/issues/281)), the packaging guide
  ([#262](https://github.com/ginsys/bronzeward/issues/262)) and the usability validation
  ([#263](https://github.com/ginsys/bronzeward/issues/263)). Bronzeward itself stays a generic
  OIDC relying party. Rejected: Keycloak, the one provider exercised live in this repository
  ([talosctl-oidc research](../design/research/20261021-talosctl-oidc.md)); no reference provider,
  with only the stub issuer.

The decisions above add these requirements on any identity provider a deployment uses. #281
verified each against Zitadel v4.19.5
([live checks](../design/research/20261022-zitadel-test-idp.md#outcomes)): the device grant
(check 1), rotation on both clients (check 2, and the self-test's CLI renewal), refusal of a
rotated-out token (check 3), binding to the client (check 4), the audience (check 6, with the
project's `aud` scope) and a groups claim, which needs an Actions v2 target because the role
claim is an object and no groups scope exists (checks 7-8):

- the device-authorization grant on the CLI client (D4 (a));
- refresh tokens that rotate on every use and are bound to the client, for public clients of both
  kinds, the browser client and the native CLI client (D2 (b), D5 (a));
- JWT access tokens whose audience carries the server's value ([§9.1](#91-identity-provider-requirements)
  item 1);
- a groups claim that is an array of strings. The server refuses a token whose groups claim is
  not one (`internal/auth/auth.go:213-225`). Zitadel's role claim is an object, so the test
  provider adds a `groups` array from an Actions v2 target.

Residual risk of the D2 (b) token file and of D5 (a), with Zitadel v4.19.5, observed by #281
(check 3): a replayed refresh token that was already used is refused, but the session is not
revoked. Whoever refreshes a copied token first therefore keeps that session until its idle or
absolute expiry, and the legitimate client is refused and signs in again. Rotation does not
prevent this; the mitigation is deployment configuration, a short absolute refresh-token
expiration, the instance OIDC setting `refreshTokenExpiration`, which bounds renewal (check 5).
The packaging guide of #262 documents it.

Limits:

- This specification is accepted and partly implemented: with an automation token
  ([#254](https://github.com/ginsys/bronzeward/issues/254)), bronzewardctl reads version, whoami,
  status, clusters, machines with their timeline and observations, fragment, profile and assignment
  heads, revisions and history, drafts, releases, plans, operations and acts. Its tests run the
  [§12](#12-shared-conformance-cases) cases TLS-01, TLS-02, COMP-01, COMP-02, PAGE-01, PAGE-02 and
  REC-01. bronzewardctl's other reads of [§3.1](#31-reads), its sign-in and the web client are
  not implemented yet. No synthetic contract probe was run against the
  server for this document: every server behaviour
  above is read from source and contract text, and G17 is inferred, not observed.
- Line references were read at `3cbe953`, except those to the identity read and the
  expected-epoch check, which are read with
  [#277](https://github.com/ginsys/bronzeward/issues/277) and
  [#293](https://github.com/ginsys/bronzeward/issues/293) respectively applied (see the opening),
  and move as the code changes.
- Every choice in [§13](#13-alternatives-and-open-choices) and every implementation choice above
  is decided. This document decides no credential policy.
- It does not change any role, human-only rule, route, schema or server setting.
