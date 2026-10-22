# Client contracts: authentication, API behaviour and compatibility

**Status: proposed, not assessed.** This document specifies what any Bronzeward client, the
`bronzewardctl` command-line client and the web interface alike, must do against the existing
management API, for
[Specify client authentication, API behavior and compatibility](https://github.com/ginsys/bronzeward/issues/253)
in [04 - Operator experience: CLI and web UI](https://github.com/ginsys/bronzeward/milestone/4).
It implements no client, adds no route and changes no role, credential policy or server
behaviour. Each design decision it needs is listed in [§13](#13-alternatives-and-open-choices)
with its options; four are decided there, and the others are open choices with a recommendation,
for the owner to settle.

It builds on the operator journeys of [operator experience](operator-experience.md), proposed for
[Design the operator journeys for bronzewardctl and the web UI](https://github.com/ginsys/bronzeward/issues/252).
References of the form *journeys §8* and gap IDs `G1`-`G12` point at that document. Its
`G13`-`G16` appear here as `G21`-`G24`, and its `G17` is this document's `G18`; this document
adds `G13`-`G17`, `G19` and `G20`.

**Facts and proposals are kept apart.** A statement about existing behaviour cites the contract
section or the source line it comes from, read at revision `9b5804a` (`internal/api/api.go:374`
means that file and line). A requirement this document proposes is marked **Proposed**; *must*
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
- Human-only routes refuse automation whatever its roles (`internal/api/api.go:374-375`).
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
| Base path | Routes are `METHOD /api/v1<pattern>`; a request no route takes is `404 not-found`, after authentication, or `409 recovery-mode-active` before a recovery start's entry commits | `internal/api/api.go:229-235`, `internal/api/api.go:246-253` |
| Anonymous surface | `GET` and `HEAD /livez` answer `204` with no body and no authentication; every other request authenticates first, an unknown path and `OPTIONS` included | `internal/server/server.go:10-24`, `internal/api/api.go:255-264` |
| Transport | The server listens on plain http; TLS terminates in front of it | `internal/server/server.go:34-42`; [persistence and API §9.1](persistence-api.md#91-conventions) |
| Timeouts | Reading a request, body included, 30 s; headers 10 s; idle 2 min; no write timeout (the event stream outlives any fixed bound) | `internal/server/server.go:27-42` |
| Request body | Every `POST` and `PUT` sends one JSON object (`{}` when the route takes no member) with `Content-Type: application/json`, at most 1 MiB, unknown members refused, each violation `400 invalid-request`; a `DELETE` with a body is `400`, while its `Content-Type` is not checked (clients send none) | `internal/api/body.go:36-66`, `internal/api/drafts.go:44`; [persistence and API §9.1](persistence-api.md#91-conventions) |
| Success body | `application/json` | `internal/api/reads.go:35`, `internal/api/idempotency.go:85` |
| Errors | `application/problem+json` with `type` `urn:bronzeward:problem:<code>`, `title`, `status`, `instance` (the request ID, also logged), optional `detail` and extension members; `detail` never names a value | `internal/api/problem.go:76-98`; [persistence and API §9.4](persistence-api.md#94-errors) |
| Credential | `Authorization: Bearer <token>`; a value starting `bwt_` is an automation token, anything else an OIDC access token | `internal/auth/auth.go:132-141`, `internal/auth/tokens.go:21` |
| Epoch headers | A response to an authenticated request carries `Bronzeward-Epoch`, and `Bronzeward-Recovery-Mode: true` while recovery mode is in effect, once the installation state was read; `401`, `403 identity-revoked`, liveness and a `503` from authentication or from the installation-state read carry neither | `internal/api/api.go:259-275`, `internal/api/api.go:317-325`, `internal/api/problem.go:68-71`; [persistence and API §9.1](persistence-api.md#91-conventions) |
| Pagination | `limit` 1-500, default 50; opaque `cursor`; answer `{"items": [...], "next": "..."}` with `next` omitted on the last page; a cursor carries its epoch; one from another epoch, or one that does not parse, is `400 cursor-invalid`; any other query key is `400` | `internal/api/acts.go:33`, `internal/api/acts.go:135-160`, `internal/api/reads.go:59-63`, `internal/api/reads.go:92-112` |
| ETags | Strong `"<revision>-<token>"`, token replaced on every write; served on drafts and source heads; `If-Match` required on draft-scoped writes, publication and ingestion start; missing `428 precondition-required`, mismatch `412 precondition-failed`; no `If-None-Match` | `internal/api/routes.go:98-119`, `internal/api/api.go:399-408`; [persistence and API §4.1](persistence-api.md#41-etags) |
| Idempotency | Every `POST`, `PUT`, `DELETE` needs one `Idempotency-Key` of 16-128 `[A-Za-z0-9_-]` (`428 idempotency-key-required` if absent); scoped per principal; same request replays with `Idempotent-Replayed: true`; another request under the key is `422 idempotency-key-reused`; a record from an earlier epoch is `409 conflict` | `internal/api/api.go:386-397`, `internal/api/idempotency.go:41-43`, `internal/api/idempotency.go:60-92`, `internal/api/idempotency.go:98-106`; [persistence and API §7.2](persistence-api.md#72-behavior) |
| Unknown commit | A commit whose outcome the server could not learn is `500 internal-error`, "retrying with the same Idempotency-Key answers it" | `internal/api/tx.go:312`; [persistence and API §9.4](persistence-api.md#94-errors) |
| Asynchronous work | Ingestion start, mark, continuation, takeover and publication answer `202` with an operation; plans and approvals `201` | [persistence and API §8](persistence-api.md#8-asynchronous-operations), [§9.2](persistence-api.md#92-resources-and-routes) |
| Events | `GET /operations/{id}/events` is a cursor-paged JSON list, or Server-Sent Events when `Accept` contains `text/event-stream`; event ID `<epoch>:<number>`; resume with `Last-Event-ID`; an ID from another epoch gets `event: reset` and a replay from the start; the stream ends when the operation is terminal or at the opening token's expiry; `unresolved` is not terminal | `internal/api/operations.go:183-305`; [persistence and API §8.3](persistence-api.md#83-the-resource-and-its-progress) |
| Caching | Only the protected review and the event stream send `Cache-Control: no-store`; no other response sets cache headers | `internal/api/review.go:31`, `internal/api/operations.go:281` (search of `internal/` and `cmd/` for `Cache-Control`) |
| Browser support | No CORS headers, no `Origin` check, no cookie, no CSRF token anywhere in the server | search of `internal/` and `cmd/` for `Access-Control`, `Origin`, `Cookie`; [persistence and API §10.1](persistence-api.md#101-humans-oidc) "no server session" |
| Identity and version reads | None: no route answers who the caller is, which roles it holds, or which server version runs | `internal/api/routes.go:78-136` |

## 3. Action, endpoint and role matrix

Every route of `internal/api/routes.go:78-136` with the client action that uses it. No route
answers `501` today: each sets a read or an effect (`internal/api/api.go:358-366`). "Human"
means the route refuses automation with `403 forbidden` and `humanOnly: true`
(`internal/api/api.go:374-375`). "Keyed" means the body carries unextracted input, fingerprinted
with the provider's digest key instead of SHA-256 (`internal/api/routes.go:20-23`). In the
"Start mode" column, "normal" means a process started for recovery refuses the route even after
its entry (`internal/api/api.go:342-345`), and "recovery" means a normal start refuses it
(`internal/api/api.go:348-351`).

### 3.1 Reads

Every read takes any role (`internal/api/routes.go:60`, `internal/api/routes.go:65`) except the
protected review.

| Client action | Route | Paged | ETag served | Notes |
| --- | --- | --- | --- | --- |
| cluster list / show | `GET /clusters`, `/clusters/{id}` | list | no | |
| machine list / show | `GET /machines`, `/machines/{id}` | list | no | no filter (G9) |
| machine observations | `GET /machines/{id}/observations` | yes | no | |
| machine timeline | `GET /machines/{id}/timeline` | yes, by revision | no | JSON only, no stream (G9) |
| fragment, profile, assignment list / show / history / revision | `GET /{kind}s`, `/{kind}s/{id}`, `/{kind}s/{id}/revisions`, `/{kind}-revisions/{id}` | lists | head item only | `internal/api/routes.go:83-87` |
| draft list / show | `GET /drafts`, `/drafts/{id}` | list | item | the item's ETag is the one draft writes need |
| import status | `GET /ingestions/{id}` | no | no | no ingestion list (G10) |
| import review | `GET /ingestions/{id}/review` | no | no | `author`, human; `no-store`; only while `paused` (`internal/api/review.go:30-52`) |
| release list / show | `GET /releases`, `/releases/{id}` | list | no | |
| release review, provenance | `GET /releases/{id}/machines/{m}/review` | no | no | redacted |
| plan list / show | `GET /plans`, `/plans/{id}` | list | no | no filter (G9) |
| approval show | `GET /approvals/{id}` | no | none, by design | approvals are immutable |
| operation list / show | `GET /operations`, `/operations/{id}` | list | no | filters `plan`, `machine`, `state`, `kind` (`internal/api/operations.go:95-108`) |
| operation watch | `GET /operations/{id}/events` | JSON pages, or SSE | no | [§8.6](#86-event-resumption) |
| act list | `GET /acts` | yes | no | |
| recovery status | `GET /recovery` | no | no | |
| dependency list / show / releases / alerts | `GET /dependencies` (`class` filter), `/dependencies/{id}`, `/dependencies/{id}/releases`, `/dependencies/{id}/alerts`, `/dependency-alerts` | lists | no | |

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
`internal/api/inventory.go:172`, `internal/api/drafts.go:303`, `internal/api/ingestions.go:404`,
`internal/api/cancellations.go:144`, `internal/api/publications.go:76`); and
[persistence and API §9.2](persistence-api.md#92-resources-and-routes). The publisher's
cancellation of only its own plan is checked in the handler
(`internal/api/cancellations.go:61-67`). The ingestion start's `If-Match` is the target draft's
ETag (`internal/api/ingestions.go:152-153`).

### 3.3 Client actions with no route

| Client action | Why no route | Contract |
| --- | --- | --- |
| `login`, `logout` | identity-provider and client concerns; the server keeps no session | [§9](#9-authentication) |
| `context` | client configuration | [§5](#5-endpoint-discovery-and-context-selection) |
| `whoami`, roles in the web header | G1 | [§4](#4-api-gap-audit) |
| version check | G2 | [§7](#7-compatibility-and-versioning) |
| automation token issue, rotate, list | server-side command only | not offered by either client |
| dispatch, apply | the controller dispatches approved plans ([design §12.7](../design/Talos_Configuration_and_Machine_Management_Design.md#127-application-approval-and-dispatch-boundary); [§13.7](../design/Talos_Configuration_and_Machine_Management_Design.md#137-poc-identity-and-approval-policy) item 2) | not offered |

## 4. API gap audit

The seventeen gaps the journeys found, checked against the code above, plus seven this audit adds. G21-G24 are the journeys' G13-G16 (expected epoch, start mode, principal display name, convergence status), numbered last because they were added to the journeys last; the journeys' G17 is G18 here.
Each has a disposition: an **API issue** proposed in [§14](#14-proposed-api-gap-issues-and-downstream-refinement),
a **contract correction** to an existing specification, also in §14, a **later design issue**
out of this milestone, also in §14, a **client rule** this document states, a **client
composite** over existing routes, an **accepted limitation**, or a **decision** listed in
[§13](#13-alternatives-and-open-choices). Every disposition, including "accepted", is
this draft's proposal until the owner assesses it. Nothing here is presented to a client as
supported.

| ID | Gap | Evidence | Disposition |
| --- | --- | --- | --- |
| G1 | No identity, role or capability read; roles appear only in a `403` body | `internal/api/api.go:372-384`; no route in `internal/api/routes.go:78-136` | API issue A1; blocks the sign-in and inventory tickets |
| G2 | No server version or compatibility read; only anonymous `/livez` | `internal/server/server.go:19-22` | API issue A2; blocks every client's compatibility refusal |
| G3 | An operation does not name its ingestion; only the ingestion side links them: its read serves `operation`, and the start, mark and takeover `202` bodies name both | the operation's subject is a draft or a plan and machine, never an ingestion (`internal/api/reads.go:358-373`, `internal/api/reads.go:398-400`); the ingestion side: `internal/api/ingestions.go:234`, `internal/api/ingestions.go:255-260`, `internal/api/marks.go:106`, `internal/api/ingestions.go:323` | API issue A4 (operation `subject`) |
| G4 | A draft does not name the release it produced | the draft read selects no release (`internal/api/reads.go:211`) | accepted limitation: the publish operation's `result.release` answers it |
| G5 | No render of a draft before publication | no such route in `internal/api/routes.go:78-136` | later design issue L1: a compilation design change, not a client contract |
| G6 | Provenance covers secret-reference occurrences only, not "which fragment set this field" | [compilation §8.1](compilation.md#81-provenance-by-tracer-composition) attributes stand-ins to reference occurrences | later design issue L1, as G5 |
| G7 | Drift content is not served | the machine's drift and observation reads name the observation, its time and a matched release, and no configuration (`internal/api/drift.go:150-155`, `internal/api/inventory.go:251-257`, `internal/api/observations.go:16-17`) | later design issue L2: serving observed configuration is a redaction design question; until then clients use a drift-adoption import with review |
| G8 | Machines have no human-readable name | the machine resource has no name member (`internal/api/inventory.go:260-272`) | accepted limitation for this milestone; clients show endpoint and node ID |
| G9 | No list filters on machines, plans, releases, drafts or acts; no machine timeline stream | `internal/api/acts.go:135-145` refuses any key but `limit`, `cursor` and the route's filters; filters exist only on operations and dependencies | API issue A4 for the filters the journeys need first: plans by `state` and `machine`, machines by `cluster`; accepted limitation for the release, draft and act filters, which clients apply over the pages they read; timeline polling accepted |
| G10 | No ingestion list | `internal/api/routes.go:90` | API issue A4 |
| G11 | Recovery marks, releases and accountings have no list route | `internal/api/routes.go:128-135` | accepted limitation: `GET /recovery` and the machine timeline |
| G12 | No browser access path: no CORS, and every request including a preflight `OPTIONS` must authenticate | `internal/server/server.go:10-13`, `internal/api/api.go:255-264`; [§10.1](#101-hosting-and-origin) | decision D1; under the recommended same-origin hosting no API change |
| G13 | Authenticated reads set no `Cache-Control`; a browser may keep them in its HTTP cache | [§2](#2-the-api-a-client-meets) caching row | API issue A3 |
| G14 | No anonymous way to learn the issuer, audience and client registration a deployment expects | [§2](#2-the-api-a-client-meets) anonymous row | decision D3 |
| G15 | The contract's prose describes the `reset` stream event, but its ingest and publish event-type lists omit it, so a client built from the lists does not expect it | `internal/api/operations.go:283-285`; [persistence and API §8.3](persistence-api.md#83-the-resource-and-its-progress) | contract correction C1 (documentation only) |
| G16 | No request-ID response header: a client learns the request ID only from a problem's `instance` | `internal/api/problem.go:84-90`; no other header set | API issue A3 |
| G17 | A request with an unsupported method on a known path is answered by the catch-all `404`, not `405` | `internal/api/api.go:233-235` (inferred from `ServeMux` method patterns; untested) | accepted; clients treat `404 not-found` on a known route as "not supported by this server" only together with A2's version read |
| G18 | The settle floor and transport deadlines that bound recovery accounting are deployment settings no route serves before an accounting decision, which is when a client needs them to compute when the end observation may begin (the journeys' G17). After one, a successful accounting's timeline facts carry `settleFloor` and `anchor`, the settle-floor refusal states the floor and its anchor only, and a plan-creation refusal states the maximum transport deadline | [execution and recovery §5.2](execution-recovery.md#52-accounting-a-lost-response); `examples/bronzeward.yaml:29-44`; after a decision: `internal/api/settle.go:160-161`, `internal/api/recovery_accountings.go:209-211`, `internal/api/settle.go:148-150`, `internal/api/plans.go:113-114` | folded into A2 (served as read-only installation settings) |
| G19 | The fixture issuer mints tokens but offers no authorization, token, device or revocation endpoint, so no interactive sign-in can be tested against it | `fixtures/oidc/issuer/issuer.go:1-6` ("no login, no clients, no refresh"), `fixtures/oidc/issuer/issuer.go:110-123` (discovery and key set only), `fixtures/oidc/main.go:22-25` | validation-fixture issue A5 |
| G20 | `503` answers carry no `Retry-After`, and the contract gives no retry guidance for `dependency-unavailable`, `transient-conflict` or `epoch-superseded` | search of `internal/` for `Retry-After`; [persistence and API §9.4](persistence-api.md#94-errors) | client rule in [§8.2](#82-errors); no API change proposed |
| G21 | A human's mutation takes no expected-epoch precondition: only `Idempotency-Key` and `If-Match` are checked (`internal/api/api.go:388-409`), and the epoch is reported only in an answer (`internal/api/api.go:318-319`). After a write's answer is lost, and either a restore removes its record or the first attempt never committed, the same request with the same key executes afresh in the new epoch, because the in-transaction lookup finds no record (`internal/api/tx.go:190-198`) ([persistence and API §12.4](persistence-api.md#124-restore-and-idempotency)); the read before repeating of [§8.4](#84-idempotency-and-ambiguous-responses) item 3 leaves a race. A service principal is already checked: its token carries its epoch, and the write's own transaction refuses one from another epoch with `401` | `internal/api/api.go:388-409`, `internal/api/api.go:318-319`, `internal/api/tx.go:190-198`; service check: `internal/auth/automation.go:51-52`, `internal/api/idempotency.go:28-30`, `internal/api/idempotency.go:47-49`, `internal/api/api.go:297-303` | decision D10; API issue A6 if D10 chooses a server check |
| G22 | No read of the process's start mode. `GET /recovery` serves epoch, recovery mode, entry, exit and scopes, and the headers carry only `Bronzeward-Epoch` and `Bronzeward-Recovery-Mode`. Past the role check, a recovery start shows only in an answer: a `409 recovery-mode-active` without a `scope` member, to any request but the entry before the entry commits and to plan creation, approval or unfreeze after it, or any recovery-entry answer except its not-a-recovery-start refusal. A normal start shows only in that refusal: a plain `409 conflict` with neither an `epoch` nor a `request` member, while every other `409 conflict` the entry answers carries `epoch`. The scope refusals that share the `recovery-mode-active` code carry `scope` | `internal/api/recovery_exits.go:123-129`, `internal/api/api.go:318-324`; process refusals: `internal/api/api.go:241-243`, `internal/api/api.go:337-351`, `internal/api/recovery_exits.go:211-213`, `internal/api/routes.go:120-127`; entry: `internal/api/recovery_entries.go:91-92`, `internal/api/recovery_entries.go:106-108`, `internal/api/recovery_entries.go:133-135`, `internal/api/idempotency.go:65-67`; scope refusals: for example `internal/api/plans.go:226-227` | API issue A2 (the start mode served with the version); until then the client reading of [§8.5](#85-epoch-changes), with the limit that nothing shows the mode before such an answer. That reading rests on the absent `scope` member, which the contract says every `recovery-mode-active` carries: contract correction C2 |
| G23 | No principal display name: bodies carry principal identifiers only, and no route maps one to a subject or name | `internal/api/acts.go:19-30`, `internal/api/releases.go:53`, `internal/api/plans.go:195`, `internal/api/approvals.go:43`, `internal/api/recovery_entries.go:79`; no such route in `internal/api/routes.go:78-136` | API issue A7; until then clients show shortened principal identifiers |
| G24 | No convergence status: the machine body serves `desired` and `applied.release`, and compares the artifact digests only to match an observation, never serving them, so a client cannot tell whether the `Desired` release's digest differs from the `Applied` one, which is what pending convergence means. After an adoption the `Applied` digest is the baseline's, so equal or different release IDs do not settle it | `internal/api/reads.go:155-166`; [execution and recovery §1](execution-recovery.md#1-supported-operation-and-state-values), [§6.3](execution-recovery.md#63-adopt) | API issue A4 (a pending-convergence member on the machine); until then clients show the release difference only, never labelled as pending convergence |

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
4. Today the issuer, client ID and audience are configured by hand (G14). Decision D3 chooses how
   a client learns them later. The server is never asked for them through an authenticated route:
   the client needs them before it can authenticate.
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
   the trust source used, and sends nothing.
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
   the identity provider the CLI follows redirects only to `https` (or loopback) URLs. The CLI
   honours the standard proxy environment variables, says so in verbose output, and never sends
   `Authorization` to a proxy in clear text: an `https` URL goes through a proxy only by a
   tunnel. The server's own issuer fetches follow a comparable rule
   ([persistence and API §10.1](persistence-api.md#101-humans-oidc)).

## 7. Compatibility and versioning

Facts: the API is `/api/v1`; within it fields and routes are only added, never removed or changed
in meaning; a route due for removal answers with a `Deprecation` header for at least one minor
release first; a breaking change is a new prefix; unknown request fields are refused
([persistence and API §9.1](persistence-api.md#91-conventions)). No route reports the server's
version (G2).

**Proposed.**

1. A client sends only fields the server it talks to is known to accept. Because the server
   refuses unknown request fields, a newer client sending a field an older server lacks fails with
   `400 invalid-request`, which is safe but useless; once A2 lands, the client feature-gates such
   fields on the served API revision.
2. A client ignores unknown response fields, and shows an unknown enumeration value raw, never as
   a known state and never as success.
3. A client declares the minimum API revision it needs. Once A2 lands, it reads the version on
   context use and refuses, with both values named, a server below that minimum. Until A2 lands,
   the clients and the server are released together from one repository revision, the packaging
   ticket documents that pairing, and no client claims to detect an incompatible server.
4. A `Deprecation` header on any response is shown once per command or page load, naming the
   route.
5. Each client prints its own version and, once A2 lands, the server's, in `bronzewardctl version`
   and in the web footer.

## 8. Request handling

### 8.1 Requests

**Proposed.** Every request sends `Authorization` and `Accept: application/json` (or
`text/event-stream` for a stream). Every `POST` and `PUT` sends a JSON object, `{}` when the route
takes no member, with `Content-Type: application/json`; a `DELETE` sends neither
([§2](#2-the-api-a-client-meets) request body row). Every write
sends exactly one `Idempotency-Key` ([§8.4](#84-idempotency-and-ambiguous-responses)) and, where
[§3.2](#32-writes) lists it, exactly one `If-Match`. A body is at most 1 MiB
(`internal/api/body.go:36`); a client refuses a larger input before sending and says so, rather
than relying on the server's `400`.

### 8.2 Errors

**Proposed** client behaviour per problem code, from the code table of
[persistence and API §9.4](persistence-api.md#94-errors) and `internal/api/problem.go:11-36`. The
client always shows `title`, `detail` when present, and `instance`, and never shows a request
body. Exit codes are the journeys' (journeys §4).

| Status, code | Client behaviour |
| --- | --- |
| 400 `invalid-request` | a malformed request: operator input, a client defect or an incompatible server. Operator input includes a mark that is not a valid path on the start and fragment routes (`internal/api/ingestions.go:68-72`, `internal/api/fragments.go:52-56`; the index is in `detail`, there is no `position` member), an empty document (`internal/api/tx.go:119-120`), an invalid name (`internal/api/drafts.go:190-191`) and the recovery entry's body checks (`internal/api/recovery_entries.go:49-71`). Show `detail` and stop; never repeat |
| 400 `cursor-invalid` | discard the cursor and restart the list. Run the epoch-change handling of [§8.5](#85-epoch-changes) only when `Bronzeward-Epoch` changed: the same code also answers a cursor that does not parse (`internal/api/acts.go:154-156`) |
| 401 `unauthenticated` | renew once ([§9.5](#95-renewal)); repeat a read; for a write follow [§8.4](#84-idempotency-and-ambiguous-responses); a second `401` stops with the failure text of [§9.6](#96-failure-cases) |
| 403 `forbidden` | show the route's `roles`, and "humans only" when `humanOnly` is true; no retry. After an ambiguous attempt, this refusal, like the process `409`s, is decided before the idempotency lookup (`internal/api/api.go:331-345`; for example a renewal dropped a role, [§9.6](#96-failure-cases)), so it says nothing about the earlier attempt: the outcome stays unknown ([§8.4](#84-idempotency-and-ambiguous-responses) item 4) |
| 403 `identity-revoked` | stop; clear the context's stored credential, meaning the tokens the client itself keeps in its credential store or memory, never an operator-supplied token file, which it leaves untouched and reports as no longer accepted; do not start a new sign-in by itself ([§9.6](#96-failure-cases)) |
| 404 `not-found` | the resource does not exist in this epoch; after a restore a handle issued later answers `404` ([persistence and API §12.3](persistence-api.md#123-what-a-restored-state-means-record-by-record)) |
| 409 `conflict`, `stale-input`, `scope-busy`, `machine-identity-mismatch`, `ingestion-abandoned`, `recovery-mode-entered`, `recovery-mode-active` | show the named resource and state; read it; never repeat automatically, with one exception. A `409 recovery-mode-active` without a `scope` member, and a `409 conflict` to the recovery entry without `epoch` and `request` members, are process refusals that show the start mode ([§8.5](#85-epoch-changes)). A `409 conflict` that carries `request` and `epoch` is an earlier-epoch idempotency record: it is read and reported, not repeated, under either key ([§8.4](#84-idempotency-and-ambiguous-responses) item 7). The one exception: a `409 conflict` on a draft fragment write (`PUT /drafts/{id}/fragments/{name}`) that answers a repeat, carries an `ingestion` member but no `draft` member, and has no `Idempotent-Replayed: true` is the first request still running, or ended without committing while its claim's lease has not lapsed (`internal/api/fragments.go:105-114`, `internal/api/tx.go:80-98`; looked up by principal and key, so it never answers a first send). A `409` with both `ingestion` and `draft`, replayed or not, is a recorded refusal (`internal/api/fragments.go:291-296`, `internal/api/drafts.go:94-104`): the write was refused, not pending. For the in-progress case, repeat the identical request with the same key, with backoff, until it answers otherwise or the client's wait bound of [§8.4](#84-idempotency-and-ambiguous-responses) item 2 expires; the contract says the repeat then gets the first request's replay ([persistence and API §7.2](persistence-api.md#72-behavior); [§8.4](#84-idempotency-and-ambiguous-responses) item 2) |
| 412 `precondition-failed` | someone else wrote first: stop, show the newer state, never refetch and resend ([§8.3](#83-etags)) |
| 422 `validation-failed` | show `rule` and position extensions; for marks only the position, never a path. Most mark refusals arrive later, as an event ([§11.3](#113-cli-review)) |
| 422 `idempotency-key-reused` | on a first send: the key was used for a different request; nothing was done; a client defect unless the operator supplied the key. On a repeat to a keyed route (fragment write, ingestion start, mark): the outcome is **unknown**, because a byte-identical repeat is also answered `422` when the provider can no longer compute under the digest-key version the record names, or the recorded key is not one the current key can be matched to; a rotation alone still replays ([persistence and API §7.1](persistence-api.md#71-the-key); `internal/api/idempotency.go:94-121`); read the resource and report what it shows |
| 428 `precondition-required`, `idempotency-key-required` | a client defect; show and stop |
| 500 `internal-error` | the outcome may be unknown: repeat the identical request with the same key ([§8.4](#84-idempotency-and-ambiguous-responses)) |
| 501 `not-implemented` | not served by this server; no route answers it today |
| 503 `dependency-unavailable`, `talos-access-unavailable`, `transient-conflict`, `epoch-superseded` | reads: retry with backoff (1 s doubling to 30 s, at most 5 tries, then stop). Writes: repeat with the same key at most 3 times. If every attempt of the write was answered `503`, report **not done**: each such `503` states that nothing was committed by that attempt. If any earlier attempt was ambiguous ([§8.4](#84-idempotency-and-ambiguous-responses) item 2), a `503` to the repeat says nothing about that attempt: report the outcome as unknown, as in [§8.4](#84-idempotency-and-ambiguous-responses) item 4. `epoch-superseded` is not repeated against the same server process: it started before the current epoch and cannot succeed until restarted. The server sends no `Retry-After` (G20) |
| an unknown code | treat by status class: 4xx as a refusal, 5xx as an unknown outcome for a write |
| a 5xx that is not a Bronzeward problem | an answer without `application/problem+json` and a `type` starting `urn:bronzeward:problem:`, for example a `502`, `503` or `504` from the component in front of the server, may come after the server committed: for a write it is an ambiguous outcome as in [§8.4](#84-idempotency-and-ambiguous-responses) item 2, never **not done**; reads retry as for the `503` row |

The `503` row applies only to a Bronzeward problem with one of its four codes. The write `503`s
say that the attempt they answer committed nothing; for example authentication's
(`internal/api/api.go:311`), the installation-state read's (`internal/api/api.go:268`), the keyed
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
(`internal/auth/auth.go:186-188`, `internal/api/idempotency.go:41-43`); the fingerprint covers
method, route, path parameters, `If-Match` and the canonical body, keyed with the provider's
digest key on keyed routes (`internal/api/body.go:165-185`); where the key has a record, the same
request under it replays, another is `422`, an earlier epoch's record is `409`
(`internal/api/idempotency.go:60-106`); where it has none, any request under it executes as a new
one (`internal/api/tx.go:198-202`); a request whose effect a restore removed lost its record
too and executes again
([persistence and API §12.4](persistence-api.md#124-restore-and-idempotency)); the same key does
not make a remote mutation exactly-once
([persistence and API §7.2](persistence-api.md#72-behavior)).

Keys are not global. The record's primary key is the pair of principal and key
(`internal/migrate/migrations/0001_schema.sql:491-508`), and the lookup joins on both, with the
authenticated principal's identifier (`internal/api/idempotency.go:41-43`). Two principals using
the same key string therefore never meet: the second finds no record and executes as a new
request, neither replayed nor refused `422`. The fingerprint itself names neither principal nor
epoch (`internal/api/body.go:170-187`); the principal is in the lookup, and the epoch is compared
on the record found (`internal/api/idempotency.go:41`, `internal/api/idempotency.go:65-68`).

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
repeat across a restore, which executes again by design. A restore after which nobody entered
recovery mode mints no new epoch, so no epoch comparison can detect it
([persistence and API §12.1](persistence-api.md#121-the-epoch)); that residual is the
contract's, and this specification does not close it.

**Proposed.**

1. **Key.** One key per logical write, 128 bits from the operating system's random source,
   base64url without padding (22 characters, within `internal/api/api.go:386`). Never derived from
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
   only after an extraction `5xx` (`internal/api/fragments.go:163-167`), otherwise when its lease
   lapses (`internal/api/fragments.go:105-114`). The client bounds the wait itself: a fixed
   maximum, configurable and at least the claim lease, after which the outcome is reported
   unknown as in item 4. The deployment's claim timers are an example of a sensible value
   (`examples/bronzeward.yaml:76-80`); the client cannot read them until A2 serves them. A replay
   (`Idempotent-Replayed: true`) is reported as "already done earlier; showing that result", except
   a replayed `application/problem+json`, which is reported as the earlier attempt's refusal (the
   fragment write replays recorded refusals, `internal/api/fragments.go:291-296`,
   `internal/api/idempotency.go:80-91`); a
   `422` to a repeat on a keyed route is reported as an unknown outcome ([§8.2](#82-errors)).
3. **Same principal and epoch only.** Before any repeat the client checks that its credential
   still names the same principal: for OIDC, the same `iss` and `sub` in the renewed token; for
   automation, the same service identity. The CLI learns the identity only from the token ID (the
   part between `bwt_` and `.`) until A1 serves it, so until then a different token ID counts as
   a different principal; this may set aside a write the server would have replayed after a
   rotation, which is the safe direction. It then confirms the current epoch with one
   authenticated read (`GET /recovery`, any role, `internal/api/routes.go:94`) and compares that
   response's `Bronzeward-Epoch` with the reference epoch: the last one the client saw before the
   first send. A client that has seen none reads `GET /recovery` once before that first send. The
   header of the failed attempt is not enough, because a dropped connection shows none. If either
   differs, or the confirming read yields no epoch header (for example its own `503`), the write
   is set aside: not repeated, not sent under a new key; the client reads the resource and the
   operator decides (journeys §8). For a human principal the check is the client's alone: a
   recovery entry between the confirming read and the repeat is not detected by the client. If the
   first attempt's record survived, the repeat meets the earlier-epoch `409` of item 7 (AMB-07); it
   executes afresh only when a restore removed that record or the first attempt never committed
   (G21, AMB-12). A service principal's repeat is refused `401` by the server instead. Whether the server
   enforces the epoch for humans too, or this residual risk is accepted, is decision D10.
   The recovery entry (`POST /recovery/entries`) is the one exception. Its own commit mints the
   new epoch, and until the process has seen that commit it refuses every other request,
   `GET /recovery` included, with `409 recovery-mode-active` (`internal/api/api.go:335-339`). When
   only the `201` was lost, the process saw the commit before answering, so the confirming read
   shows the new epoch and a same-key repeat replays the entry
   (`internal/api/recovery_entries_test.go:236-252`; the replay: `internal/api/idempotency.go:60-82`,
   `internal/api/recovery_entries.go:102-113`). When the server lost its own COMMIT reply
   and answered `500`, only a same-key repeat completes the entry
   (`internal/api/recovery_entries_test.go:254-295`). So the client repeats an entry whose outcome
   is unknown under the same key, checking the principal but not the epoch, here and in item 4.
   The server replays only this process's own entry and refuses any other entry record with a
   `409 conflict` carrying `request` and `epoch` (`internal/api/recovery_entries.go:95-114`), so
   the exception cannot replay an earlier entry or carry any other write across the epoch change.
   The entry itself can cross one: where its record is missing, because it never committed or a
   restore removed it, and a later recovery start is waiting for its entry, the repeat becomes
   that start's entry, with the earlier body's restored backups, stop time and reason, and is
   reported as this send's own `201` (`internal/api/tx.go:198-202`,
   `internal/api/recovery_entries.go:133-135`). That is accepted with the exception, like the
   residuals of AMB-10 and AMB-12, and A6 does not close it. The entry's answer is then handled as in
   [§8.5](#85-epoch-changes), without setting the entry itself aside (REC-04).
4. **After the process ends.** The CLI stores no request body, hash of a body, or key journal.
   When it gives up with the outcome unknown, it prints, on standard error, the key, method,
   route, context, principal (issuer and subject, or the token ID that stands for the service
   identity until A1), epoch and the `If-Match` value it sent, if any, and what to read to find
   out. An ETag is not secret. An operator may then repeat with `--idempotency-key <key>
   --expect-epoch <epoch> --expect-principal <principal>`, plus `--if-match <etag>` where the
   route takes one, all as printed. The client refuses, sending nothing, when one of them is
   missing, when the epoch differs as in item 3 (not for the recovery entry), or when its current
   principal is not the printed one. It never substitutes an ETag it reads now: `If-Match` is part
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
`400 cursor-invalid`, earlier idempotency records `409`, earlier automation tokens `401`, and
streams `reset` ([§2](#2-the-api-a-client-meets)).

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
§5.9). Until A2 serves it, the client keeps the last mode an answer showed, per context. A
restart changes the mode without any signal, so the kept mode is only the last one shown:

1. **Recovery start:** a `409 recovery-mode-active` without a `scope` member, or any answer of
   `POST /recovery/entries` given after authentication and the role check other than the refusal
   in item 2, its `201` and its other `409 conflict` answers included (each of these carries
   `epoch`). A `401`, a `403`, or any answer without `Bronzeward-Epoch` to the entry shows
   nothing: both `503`s decided before routing, the authentication one and the installation-state
   read's (`internal/api/api.go:266-269`), and any `5xx` that is not a Bronzeward problem
   ([§8.2](#82-errors)) lack the header, and every answer after the role check carries it. A
   `recovery-mode-active` with a `scope` member is a scope's refusal and says nothing about the
   start mode.
2. **Normal start:** only the entry's not-a-recovery-start refusal, a `409 conflict` with neither
   an `epoch` nor a `request` member, whose detail says only a recovery start serves the entry. A
   `409 conflict` to the entry is never read as a normal start by its status and code alone.
3. **Unknown** before any such answer. The client then disables no action beforehand, and the
   recovery banner states the mode as unknown or as last shown, until the next such answer.

Evidence for each answer is in the G22 row of [§4](#4-api-gap-audit). Item 1 relies on the code,
not the contract: persistence and API says every `409 recovery-mode-active` names the scope, and
the process refusals do not; contract correction C2 in
[§14](#14-proposed-api-gap-issues-and-downstream-refinement) records the divergence.

### 8.6 Event resumption

Facts: [§2](#2-the-api-a-client-meets) events row. The SSE stream sends no keep-alive comment and
no `retry:` field in the frames it writes (`internal/api/operations.go:279-305`); between events
it polls every 500 ms (`internal/api/operations.go:210-213`).

**Proposed.**

1. A client follows an operation with the stream when it can and with JSON pages otherwise; both
   are complete. It remembers the last event ID it showed.
2. When the stream ends it reads `GET /operations/{id}`. A terminal state does not show that the
   last events arrived: the stream may have been cut after the state was written and before its
   last events were (the server writes the state after the last entry and ends the stream only
   once they are written, `internal/api/operations.go:217-218`,
   `internal/api/operations.go:254`). So on a terminal state the client drains the backlog
   first: it reads the JSON event pages to their end and shows every event after the last one it
   showed, then ends the watch. Any other state, `unresolved` included, means the stream ended because the token expired or the
   connection dropped: the client renews if needed and reconnects with `Last-Event-ID` and the
   renewed token, with backoff from 1 s to 30 s on repeated failures.
3. A `reset` event clears what the watch showed, prints that the installation was restored or
   entered recovery mode, and shows the replay from the start. The epoch change is handled as in
   [§8.5](#85-epoch-changes). The server sends `reset` only when a stream opens
   (`internal/api/operations.go:248-249`); a stream that stays open across a recovery entry goes
   on with event IDs carrying the new epoch (`internal/api/operations.go:262`). So the client
   compares the epoch part of every event ID (before the `:`) with the stream's
   `Bronzeward-Epoch`; on a difference it ends the stream, applies [§8.5](#85-epoch-changes), clears
   what the watch showed and reopens the stream without `Last-Event-ID` (EVT-05).
4. The browser cannot send `Authorization` with `EventSource`, so the web client reads the stream
   with `fetch` and a streamed response body, sending `Authorization`, `Accept` and
   `Last-Event-ID` itself (assumption S1). No token is ever put in a URL.
5. A connection idle beyond an intermediary's timeout can be cut without the operation having
   ended; item 2 covers it. Whether the server should send keep-alive comments is left to A3.
6. The machine timeline has no stream; a client polls it with its cursor no more often than every
   5 s while it is on screen (G9).

## 9. Authentication

### 9.1 Identity-provider requirements

Facts: the server accepts a JWT access token signed with an asymmetric algorithm, with `iss`
equal to the configured issuer, `aud` containing the configured audience, `exp`, `iat` and a
non-empty `sub`, and `exp - iat` at most `maxTokenLifetime`, 15 minutes by default; roles come
from the configured groups claim, mapped in deployment configuration
(`internal/auth/auth.go:145-228`, `internal/config/config.go:121-125`,
`internal/config/config.go:353-356`). A disabled account or removed group takes effect when the
current token expires
([persistence and API §10.1](persistence-api.md#101-humans-oidc)).

**Proposed** deployment requirements, which the packaging ticket documents:

1. The identity provider issues **access tokens** that are JWTs carrying the audience, the groups
   claim and a lifetime no longer than `maxTokenLifetime`. An ID token is never sent to the API.
   Many providers set an access token's audience to the requesting client; the deployment
   configures an audience mapping or resource so `aud` contains the server's value.
2. Two public clients are registered, one for the CLI and one for the web interface, each using
   the authorization-code grant with PKCE `S256` and no client secret. The CLI client allows
   loopback redirect URIs with any port; the web client allows exactly the web origin's callback
   path. The device-authorization grant is enabled on the CLI client only if decision D4 adopts it.
3. Any choice under which a client keeps a refresh token (the CLI's credential store of
   [§9.2](#92-cli-sign-in) item 3; every D2 option, D2(a)'s process memory included, since a long
   wait renews in-process, [§9.5](#95-renewal) item 4; D5(a)) is conditional on refresh tokens issued to
   public clients rotating on every use and being bound to the client; this document sets no
   refresh-token policy itself. The provider's session length, not the client, bounds how long an operator
   stays signed in.
4. The web client's origin is allowed to call the provider's token endpoint cross-origin.

### 9.2 CLI sign-in

**Proposed.**

1. `bronzewardctl login` runs the authorization-code grant with PKCE `S256`, `state` and `nonce`,
   using a loopback redirect on `127.0.0.1` with a port the operating system chooses, and opens
   the system browser. If no browser can be opened it prints the URL for the operator to open on
   the same host. The listener accepts one callback, checks `state`, and closes.
2. Headless use, where no browser runs on the host, is decision D4: device-authorization grant,
   or loopback over a forwarded port.
3. **Store.** The refresh token and the current access token are kept in the operating system's
   credential store (Secret Service, Keychain or Windows Credential Manager) under the context's
   name. Where no store is available, the behaviour is decision D2; the recommendation is to keep
   the tokens in memory for that process only and require `login` for each new process, with an
   explicit per-context opt-in to a file readable only by its owner.
4. No token is printed, written to the configuration file, passed in argv or placed in an
   environment variable by the CLI. No command prints a token.
5. `whoami` shows the issuer and subject from the token's claims, read without verification and
   labelled as the client's reading, and the expiry. Roles come from A1; until it lands the CLI
   says roles are not known.

### 9.3 Browser sign-in

**Proposed.**

1. The web interface is a public client using the authorization-code grant with PKCE `S256`,
   `state` and `nonce`, with a full-page redirect to the provider. The PKCE verifier, `state` and
   `nonce` are held in `sessionStorage` only between the redirect and the callback and are deleted
   when the callback is handled; this is the one storage use the web client makes, and it holds no
   token.
2. Access and refresh tokens are held in memory only, in a module the rest of the page cannot
   enumerate. They are never written to `localStorage`, `sessionStorage`, IndexedDB, the Cache
   API, a cookie or a service worker.
3. A reload or a new tab signs in again through the provider; the provider's own session makes
   that a redirect without a password prompt while it lasts. This is the cost of keeping tokens out
   of storage, under the recommendation of decision D5.
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
   `automation.tokenFile`, or a path in `BRONZEWARD_TOKEN_FILE`) or from standard input
   (`--token-stdin`). It never accepts the token as an argument value. Whether it also accepts
   the token itself in an environment variable is decision D6.
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
(`internal/api/api.go:306-314`, `internal/api/problem.go:76-79`). The client may add what it can
read locally, labelled as its own reading, never as the server's reason.

| Case | What the server does | Client behaviour (**Proposed**) |
| --- | --- | --- |
| Token expired | `401` (`internal/auth/auth.go:165-166`) | renew ([§9.5](#95-renewal)); if the provider refuses, "Your sign-in expired: sign in again" |
| Wrong issuer | `401` | if the token's `iss` differs from `oidc.issuer`: "The token is from `<iss>`; this context expects `<issuer>`. Check the context or the server's configuration." |
| Wrong audience | `401` | if `aud` lacks `oidc.audience`: "The token is not issued for `<audience>`; the identity provider needs an audience mapping" ([§9.1](#91-identity-provider-requirements) item 1) |
| Lifetime over the maximum | `401` (`internal/auth/auth.go:175-176`); the maximum is the deployment's `maxTokenLifetime`, 15 minutes unless configured (`internal/config/config.go:125`, `internal/config/config.go:353-356`) | no local pre-check until A2 serves the setting, because the client cannot know the configured value; afterwards, if `exp - iat` exceeds it: "The token lives longer than this server accepts; the identity provider's access-token lifetime must be shortened" |
| Groups claim malformed | `401` (`internal/auth/auth.go:213-223`) | "Not accepted by `<context>`: sign in again"; nothing more can be read safely |
| No role for the action | `403 forbidden` with the route's roles | "Needs `<roles>`; you are signed in as `<sub>`" |
| Human-only route with an automation token | `403 forbidden`, `humanOnly: true` | "This needs a person signed in, not an automation token" |
| Group removed at the provider | takes effect when the current token expires ([persistence and API §10.1](persistence-api.md#101-humans-oidc)) | nothing to detect before; after renewal the next refusal names the roles |
| Account disabled at the provider | the current token is accepted until it expires; in the #162 experiment a refresh after disablement was refused ([talosctl-oidc live observations](../design/research/20261021-talosctl-oidc.md#live-observations), one provider only) | the failed renewal of [§9.5](#95-renewal) item 3 |
| Identity revoked in Bronzeward, or listed in `deniedSubjects` | `403 identity-revoked` (`internal/api/api.go:308-309`) for both. A recorded revocation is permanent (`internal/api/revocations.go:122`); a `deniedSubjects` entry is deployment configuration and lasts while the entry does (`internal/auth/auth.go:182-183`, `internal/auth/automation.go:45`) | "Your access was revoked or is denied by this installation; contact a recovery administrator." Clear the stored credential as in [§8.2](#82-errors), never an operator-supplied token file; no automatic new sign-in, which would be refused the same way. The client cannot tell the two causes apart |
| Automation token revoked, rotated or from an earlier epoch | `401` (`internal/auth/automation.go:47-52`) | "The automation token was not accepted: it may be revoked, rotated, expired or from before a restore. The operator reissues it with the server-side command." |
| Provider or database unreachable from the server | `503 dependency-unavailable`, nothing committed | retry as [§8.2](#82-errors) |
| Provider unreachable from the client | no request reaches the server | keep using the current token until it is refused; then "Cannot reach the identity provider `<issuer>`" |

### 9.7 Sign-out and cache clearing

Facts: the server keeps no session, so it cannot end one; an access token stays valid until its
expiry, at most `maxTokenLifetime`; an identity revocation is permanent and is not a sign-out
(`internal/api/revocations.go:122`).

**Proposed.**

1. `bronzewardctl logout` deletes the context's tokens from the store, revokes the refresh token
   at the provider's revocation endpoint if it has one, and says that an access token already
   issued remains valid at the server for up to its remaining lifetime.
2. The web sign-out discards the in-memory tokens and every cached response, revokes the refresh
   token if the provider allows it, tells other open tabs of the same origin to discard theirs
   (assumption S3), and redirects to the provider's end-session endpoint where it offers one. It
   then shows a signed-out page that holds no data.
3. After sign-out, the browser's back button shows no data from before: the web pages are served
   `Cache-Control: no-store`, the application clears its state on `pagehide` and on a page
   restored from the back-forward cache ([§10.3](#103-caches-and-back-navigation) items 3-4,
   assumption S8), and it renders nothing without a token.

## 10. Browser security

### 10.1 Hosting and origin

Fact: a cross-origin request carrying `Authorization`, `Idempotency-Key`, `If-Match` or a JSON
`Content-Type` makes the browser send a preflight `OPTIONS` without credentials (assumption S4);
this server authenticates every request but liveness, `OPTIONS` included, and answers `401`
(`internal/server/server.go:10-13`, `internal/api/api.go:255-264`). A browser on another origin
therefore cannot use the API today, and making it possible would add an anonymous path to the
server.

**Proposed** (decision D1): the web interface is a set of static files served from the **same
origin** as the API by the component that terminates TLS in front of the server, which routes
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

1. Every API read from the web client uses `fetch` with `cache: 'no-store'`. A3 asks the server to
   send `Cache-Control: no-store` on every authenticated response (G13), so a browser or an
   intermediary keeps nothing even from another client.
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
| An ingestion `document` (`source: document`) | `POST /ingestions` | a full machine configuration with its secrets | keyed `document`, `internal/api/ingestions.go:23-25` |
| Marks | `POST /ingestions/{id}/marks`, `POST /ingestions` `marks`, `PUT /drafts/{id}/fragments/{name}` `marks` | a path can spell a secret mapping key | on the mark route `marks` is the keyed member (`internal/api/marks.go:67-68`); on the start route and the fragment write the keyed member is `document`, and `marks` stays in the canonical body that the same keyed fingerprint covers (`internal/api/ingestions.go:23-25`, `internal/api/fragments.go:37`, `internal/api/fragments.go:51-57`, `internal/api/body.go:170-172`); [compilation §3.6](compilation.md#36-operator-review) item 4 |
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
   (`internal/api/ingestions.go:237`). A read that still shows `held` or `resumed` with
   `leaseUntil` passed ends the watch as `resume-failed` does: the mark is reported as not
   applied, with the same offers. A lapsed lease lets the run commit nothing more
   (`internal/staging/claim.go:69`). A continuation's watch (`import continue`) and a takeover's
   (`import takeover`, which leaves the claim `resumed`, `internal/staging/takeover.go:84`) end
   the same two ways. It reports a
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
    headers: [Authorization, Idempotency-Key, If-Match: <draft ETag>]
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
| AUTH-02 | refusal | each fixture defect `expired`, `wrong-issuer`, `wrong-audience`, `over-lifetime`, `alg-none`, `groups-not-array` (`fixtures/oidc/issuer/issuer.go:42-46`) | `401`; one renewal attempt at most; the client's local reading labelled as such |
| AUTH-03 | refusal | token without the needed group (`-without-group`) | `403 forbidden`; roles named |
| AUTH-04 | refusal | automation token on approve | warned before sending that the route is human-only ([§9.4](#94-automation-token-input) item 3); the request sent is answered `403 forbidden` with `humanOnly`, shown as in [§9.6](#96-failure-cases) |
| AUTH-05 | refusal | identity revoked | `403 identity-revoked`; stored credential cleared; no new sign-in loop |
| AUTH-06 | success | token expires during `operation watch` | renewed; stream resumed with `Last-Event-ID` |
| AUTH-07 | refusal | renewal returns a different subject with a write pending | write set aside; change shown |
| AUTH-08 | success | sign-out, then back navigation (web) | no data shown; storage empty |
| AUTH-09 | success | `bronzewardctl logout` | tokens gone from the credential store; refresh token revoked where the provider allows; remaining access-token validity stated |
| AUTH-10 | refusal | subject listed in `deniedSubjects` | `403 identity-revoked`; same handling as AUTH-05 |
| ETAG-01 | refusal | another author writes first | `412`; no resend; newer state shown |
| AMB-01 | disconnect | connection dropped before any request byte is sent | ordinary retry |
| AMB-02 | disconnect | fragment write dropped after the body is sent | repeat with the same key; an in-progress `409` (with `ingestion`, no `draft`, not replayed) is repeated with backoff up to the client's wait bound, then outcome unknown; a `409` with `ingestion` and `draft` (draft published meanwhile) is reported refused, not repeated; otherwise replay or first execution |
| AMB-03 | refusal | `500 internal-error` | repeat with the same key; replayed result |
| AMB-04 | refusal | first attempt committed, its answer dropped; CLI exits with outcome unknown; operator repeats with the printed values and a changed file (existing record) | `422 idempotency-key-reused`; reported as: this send did nothing, the original outcome is still unknown; resource read |
| AMB-10 | disconnect | first attempt dropped by the injector before the server receives it; CLI exits with outcome unknown; operator repeats with the printed values and a changed file (missing record) | the changed input executes as a new request; the client reports this send's answer as its own result, never as a replay or as the original outcome; the case records that no client check prevents it ([§8.4](#84-idempotency-and-ambiguous-responses) item 4) |
| AMB-11 | refusal | as AMB-04 or AMB-10, with the printed `--if-match` omitted, with another principal signed in, or after a recovery entry changed the epoch from the printed `--expect-epoch` | refused by the client before sending; nothing sent |
| AMB-13 | refusal | as AMB-04 or AMB-10, with `--if-match` given a freshly read ETag instead of the printed one | not detectable by the client ([§8.4](#84-idempotency-and-ambiguous-responses) item 4); with the first attempt's record: `422`, as AMB-04; without it: executes when the ETag matches the current resource, reported as in AMB-10, else `412`, reported as: this send did nothing, the original outcome is still unknown; resource read; the case records that no client check prevents it |
| AMB-05 | restore | write dropped, then recovery entry before the repeat | epoch confirmed by a read before repeating; it differs, so not repeated; resource read; operator decides |
| AMB-06 | refusal | keyed repeat after the digest key's recorded version became uncomputable | `422`; reported as unknown outcome; resource read; no new key sent |
| AMB-07 | restore | signed in as a human principal (a service principal's repeat is refused `401` instead, [§8.4](#84-idempotency-and-ambiguous-responses) item 3); write dropped; recovery entry injected after the confirming read and before the repeat, so the repeat meets an earlier-epoch record | `409 conflict` with `request` and `epoch`; no new key sent; resource read |
| AMB-12 | restore | as AMB-07, but the first attempt's record is missing: a restore to a backup taken before it removed it, or the first attempt never committed (G21) | with A6 (D10 option (a)): refused naming the current epoch, nothing committed, resource read; with D10 option (b): the repeat executes afresh in the new epoch, the accepted residual, and the case records that no client check detects it |
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
| REC-04 | disconnect | recovery entry commits and its answer is lost: (a) the injector drops the `201`, and the confirming read answers `200` with the new epoch; (b) the server loses its own COMMIT reply and answers `500`, and the confirming read is refused `409 recovery-mode-active`; (b) needs a server-side fault hook, not the injector, and the server binary has none (the test-only `commit` option, `internal/api/api.go:131`, `:189`): its server side is covered by `internal/api/recovery_entries_test.go:254-295`, and its client side waits for that hook, a prerequisite of #263 | both: repeated under the same key with no epoch check; `201` with `Idempotent-Replayed: true` and the minted epoch; §8.5 applied without setting the entry aside |
| PROT-01 | success | protected import, review, mark, continue | review only on a terminal (CLI; a pseudo-terminal in tests, its output matched in memory and never retained, [§11.5](#115-test-artifacts) item 1); no synthetic value in any artifact or storage |
| PROT-02 | refusal | `import review` with standard output redirected | refused, nothing written |
| PROT-03 | refusal | mark that is not a path; mark compilation refuses (for example one that would rewrite text); compilation fails on a dependency | first: `422` with `position`; second: `202`, then, after the `marked` event of that `generation`, a `mark-refused` event with `422` and `position` and the ingestion paused again; third: `mark-refused` with `503`, shown by code as not applied; none reported as accepted, no path shown |
| PROT-05 | refusal | mark accepted (`202`), then the run cannot decrypt the staged envelope | after the `marked` event, a `resume-failed` event with `dependency-unavailable`; the watch ends with the operation still `running`; the mark reported as not applied; `import takeover` offered once the lease lapses, and `import abandon`; no wait without end |
| PROT-06 | disconnect | mark accepted (`202`), then the serving process is killed before the run writes any event; second case: the same during a continuation; third case: the same during a takeover | the client reads the ingestion once its `leaseUntil` has passed; `held` (mark, continuation) or `resumed` (takeover) with `leaseUntil` passed ends the watch with the operation still `running`; the mark, continuation or takeover reported as not applied; `import takeover` and `import abandon` offered; no wait until the claim's absolute expiry |
| PROT-04 | disconnect | connection dropped during the review read | nothing persisted; review read again |
| COMP-01 | refusal | server below the client's minimum API revision (after A2) | refused before any write, both versions named |
| COMP-02 | success | response carrying a `Deprecation` header | warning shown once, naming the route |
| ETAG-02 | success | edit after the client's own import succeeded; second case: another author writes between the import and the re-read | first: draft `revision` equals the import's `result.draftRevision`, the next write uses the new ETag, no `412`; second: revision is higher, treated as `412`, newer state shown |
| TLS-02 | refusal | API answers a `3xx` to another origin, to the CLI and to the web client; harness check | the injector sees no request to the target and nothing resent by either client; the CLI names the target |
| TLS-03 | refusal | as TLS-02, to the web client, for a read and for a write; browser message check | the page shows the generic transport-failure message naming a refused redirect as a possible cause and no target; the write is repeated with the same key as in §8.4 item 2 and, when every repeat meets the same redirect, reported as an unknown outcome with its key and the resource to read, never as not done |
| TLS-01 | refusal | certificate from an untrusted CA | nothing sent; host and trust source named |

## 13. Alternatives and open choices

D3, D7, D8 and D9 are decided by the lead on 2026-10-21, under the owner's 2026-10-07
delegation, as their rows state. D1, D2, D4, D5, D6 and D10 stay open until the owner settles
them; their recommendation is this draft's, not a decision.

| ID | Choice | Options | Recommendation and reason |
| --- | --- | --- | --- |
| D1 | Web hosting and origin | (a) static files on the API's origin via the TLS terminator; (b) another origin with CORS added to the server; (c) the server serves the files; (d) backend-for-frontend | (a): no server change, no anonymous preflight path, no cookie; (b) adds an unauthenticated `OPTIONS` path, (c) an unauthenticated file route, both against "every request authenticates"; (d) is excluded by the issue unless decided explicitly |
| D2 | CLI token store without an OS credential store | (a) memory only, sign in per process; (b) file readable only by its owner, opt-in per context; (c) file by default | (a) by default with (b) as an explicit opt-in, each conditional on rotating, client-bound refresh tokens ([§9.1](#91-identity-provider-requirements) item 3), since (a) also holds one in process memory: a refresh token on disk is a long-lived credential; the choice is visible per context |
| D3 | How a client learns issuer, client ID and audience | (a) by hand in the context; (b) a static client-configuration file served next to the web files; (c) an anonymous API route | **Decided** by the lead, 2026-10-21, under the owner's 2026-10-07 delegation: (a) now, and (b) if D1 is decided (a). Reason: under same-origin hosting the web client needs the file anyway, and the CLI can import it with `context add --from <url>` under TLS; (c) adds a second anonymous route |
| D4 | CLI sign-in without a local browser | (a) device-authorization grant; (b) loopback over a forwarded port; (c) both | (a) offered when the provider supports it, else (b): device flow suits SSH sessions but is phishable by a lookalike prompt, so the CLI shows the verification URI's host prominently |
| D5 | Browser renewal | (a) rotating refresh token in memory; (b) silent frame renewal; (c) redirect at every expiry | (a), conditional on the provider rotating and client-binding refresh tokens ([§9.1](#91-identity-provider-requirements) item 3): (b) breaks under third-party cookie blocking; (c) interrupts work every 15 minutes |
| D6 | Automation token in an environment variable | (a) not accepted, file or stdin only; (b) accepted as `BRONZEWARD_TOKEN` | (a): an environment variable is inherited by every child process and readable by the same user's processes; CI systems can write the token to a file first. (b) is the common CI convention; if chosen, the CLI never passes its environment to a child process |
| D7 | Generated or handwritten clients | (a) both handwritten; (b) an OpenAPI description of v1 in the repository, checked against the route table by a test, generating request and response types for both clients, with handwritten behaviour (idempotency, ETags, epochs, renewal); (c) fully generated clients | **Decided** by the lead, 2026-10-21, under the owner's 2026-10-07 delegation: (b), generating types only; all behaviour stays handwritten. Reason: the types stay aligned with the server while the behaviour this document specifies stays explicit and tested by the shared cases; no OpenAPI description exists today (search of the repository) |
| D8 | Conformance case format and home | (a) case records in the repository run by both clients' harnesses; (b) separate suites per client | **Decided** by the lead, 2026-10-21, under the owner's 2026-10-07 delegation: (a), as [§12](#12-shared-conformance-cases) |
| D9 | Idempotency key metadata across a CLI restart | (a) no local journal: on giving up, the CLI prints the key, route, principal, epoch and `If-Match`, and the operator repeats with them and the same input ([§8.4](#84-idempotency-and-ambiguous-responses) item 4); (b) a local journal of pending keys, owner-readable, replayed on the next run | **Decided** by the lead, 2026-10-21, under the owner's 2026-10-07 delegation: (a). Reason: a journal would hold request context on disk and need its own expiry and epoch rules. Its cost is that the operator must supply the exact input again: where no record exists, a changed input executes (AMB-10), so an operator who cannot reconstruct it reconciles from the resource; (b) avoids that reconstruction only if the journal holds the request itself, documents included, which on the keyed routes (fragment write, ingestion start, mark) would write protected plaintext to disk against [§11.2](#112-rules-for-both-clients) item 4 and the secret-ingress boundary. So (b) could journal request bodies only for non-keyed routes, and for the keyed ones offers nothing over (a) |
| D10 | Epoch check on a human's repeated write (G21) | (a) server-enforced: mutations accept an expected-epoch request header and refuse, committing nothing, when it differs from the current epoch, giving humans an epoch check like the one service tokens already get (API issue A6); (b) client-only check of [§8.4](#84-idempotency-and-ambiguous-responses) item 3 for humans, with the race between its confirming read and the repeat explicitly accepted | (a): it closes the race at the only place that knows the epoch at commit time, and keeps the earlier-epoch `409` of AMB-07 because, unlike the service check, which refuses before any record is answered (`internal/api/idempotency.go:47-49`), it runs only when the in-transaction lookup finds no record (`internal/api/tx.go:198-199`); (b) needs no server change and the window is short, but a human's repeat that lands in it after a restore removed the record, or whose first attempt never committed, executes afresh, with no error to any party |

## 14. Proposed API-gap issues and downstream refinement

This document creates no issue. The owner decides which to open and links them natively.

**A1. Serve the caller's identity and roles.** An authenticated `GET` that answers the
principal's kind, issuer and subject or service name, the principal ID when one exists, the roles
the credential maps to, the credential's expiry and the current epoch. It must answer a
credential that maps to no role, which the current role check would refuse
(`internal/api/api.go:377-383`), so it needs a decision on whether a role-less identity may read
its own identity. Closes G1; blocks the CLI and web sign-in tickets' role display.

**A2. Serve the server version, API revision and installation settings.** An authenticated
`GET` with the server's version, the API revision within v1, the optional features it serves, and
the read-only settings a client shows: `maxTokenLifetime`, the settle floor, the maximum transport
deadline, the ingestion claim's lease and absolute expiry, and the plan defaults; and the serving
process's start mode, normal or recovery. Closes G2, G18 and G22; gives the in-progress `409` wait of [§8.4](#84-idempotency-and-ambiguous-responses) item 2
a server-derived bound; lets clients refuse an incompatible server
(the CLI ticket's acceptance criterion).

**A3. Response headers for clients.** `Cache-Control: no-store` on every authenticated response,
a request-ID response header equal to the problem `instance`, and keep-alive comments on the event
stream at a fixed interval. Closes G13 and G16, and the idle-cut risk in
[§8.6](#86-event-resumption).

**A4. Navigation reads for the journeys.** A list of ingestions (filterable by state and machine),
the ingestion named in its operation's `subject`, and list filters: plans by `state` and
`machine`, machines by `cluster`; and a member on the machine stating whether it is pending
convergence, computed from the digests the server already compares. Closes G3, G10, G24 and the
first part of G9.

**A5. A sign-in-capable test issuer.** Extend the fixture issuer, or add a pinned identity
provider to the fixtures, with authorization-code and PKCE, refresh with rotation, revocation,
end-session and optionally device authorization, so client sign-in is tested against an endpoint
and not a minted token. Closes G19; blocks the sign-in tickets' verification.

**A6. An expected-epoch precondition on mutations** (only if D10 chooses (a)). Every mutating
route accepts an optional request header naming the epoch the client expects, checked in the same
transaction as the write and only when the idempotency lookup finds no record
(`internal/api/tx.go:198-199`), so an earlier-epoch record still answers its `409` (AMB-07); a
mismatch is refused with a problem naming the current epoch and commits nothing. It gives human
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
G23; until then clients show shortened principal identifiers.

**C1. Contract correction.** Add the `reset` event to the stream contract of
[persistence and API §8.3](persistence-api.md#83-the-resource-and-its-progress) (G15). A
documentation change for the landing of this specification or its own small change.

**C2. Contract correction.** The `recovery-mode-active` row of
[persistence and API §9.4](persistence-api.md#94-errors) says the body names the scope, but the
process refusals of a recovery start, before and after its entry, carry no `scope` member
(`internal/api/api.go:241-243`, `internal/api/recovery_exits.go:211-213`). The code is right: a
process refusal concerns no scope. State that only the scope refusals name the scope (G22,
[§8.5](#85-epoch-changes)). A documentation change, like C1.

**L1. Later design issue: draft render and field provenance.** A render of a draft before
publication, and provenance that says which fragment set a field, are compilation design
changes outside this milestone (G5, G6). Proposed as one issue for a later milestone; no client
ticket waits for it.

**L2. Later design issue: serving observed configuration.** Whether and how drift content is
served is a redaction design question outside this milestone (G7); until it is settled clients
use a drift-adoption import with review.

**Downstream refinement, proposed for the owner.** Each client ticket cites this document's
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
| [#263](https://github.com/ginsys/bronzeward/issues/263) validation | A5, D8; a server-side COMMIT fault hook for REC-04 (b) | 11.5, 12 |

A7 blocks no ticket: until it lands, the tickets from #256 to #261 show shortened principal
identifiers (G23). A4's convergence member does not block the inventory views of #254 and #255
either: until it lands they show the release difference only (G24).

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
  audience and groups claim, PKCE public clients and rotating refresh tokens. Only one provider
  was exercised in this repository, for Talos access, not for this client
  ([talosctl-oidc research](../design/research/20261021-talosctl-oidc.md#pinned-sources-and-fixture)).
- **S7.** A browser `fetch` follows a redirect by default and resends the method and body on a
  `307` or `308`; `redirect: 'error'` makes any redirect a network error instead (Fetch standard).
- **S8.** `Cache-Control: no-store` does not keep every browser from restoring a page from its
  back-forward cache; a page restored that way receives `pageshow` with `persisted` true (HTML
  standard).

Limits:

- This is a proposal. No client exists and no case of [§12](#12-shared-conformance-cases) has run.
  No synthetic contract probe was run against the server for this draft: every server behaviour
  above is read from source and contract text, and G17 is inferred, not observed.
- Line references were read at `9b5804a` and move as the code changes.
- It decides no hosting topology, frontend framework, CLI language, identity provider, browser
  support matrix or credential policy; the choices in [§13](#13-alternatives-and-open-choices)
  other than D3, D7, D8 and D9 remain open.
- It does not change any role, human-only rule, route, schema or server setting.
