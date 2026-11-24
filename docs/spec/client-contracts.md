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
with its options and how it was decided: five by the lead, the other six by the owner. The
owner's choices of CLI language, frontend framework, reference identity provider and browser
support matrix are recorded in [§15](#15-assumptions-and-limits).

It builds on the operator journeys of [operator experience](operator-experience.md), proposed for
[Design the operator journeys for bronzewardctl and the web UI](https://github.com/ginsys/bronzeward/issues/252).
References of the form *journeys §8* and gap IDs `G1`-`G12` point at that document. Its
`G13`-`G16` appear here as `G21`-`G24`, its `G17` is this document's `G18`, and its `G18` is this
document's `G25`; this document adds `G13`-`G17`, `G19` and `G20`.

**Facts and proposals are kept apart.** A statement about existing behaviour cites the contract
section or the source line it comes from, read at revision `3cbe953` (`internal/api/api.go:382`
means that file and line; a line citation into client code, `cmd/bronzewardctl/`,
`internal/client/` or `web/`, means that line at revision `36f3bae2`, where the clients were read
when [#262](https://github.com/ginsys/bronzeward/issues/262) moved statements from Proposed to
current; a later change that cites client code at its own head adds one more exception of the form
below, naming the sections whose client citations it covers and "read at the revision that lands"
its pull request, and does not re-pin this revision; the client citations of §8.4 items 5 to 8
and §9.2 to §12 are read at the revision that lands the pull request that moved those sections to
current),
except the code [#277](https://github.com/ginsys/bronzeward/issues/277) adds or changes, the identity read
(`internal/api/identity.go`, `internal/api/routes.go:97`, `internal/api/api.go:339`), which is
read with it applied, and the expected-epoch check
[#293](https://github.com/ginsys/bronzeward/issues/293) added, cited in G21's disposition, §8.4
item 3, D10 and A6, which is read with that change applied. A line citation into client code in
[§8.5](#85-epoch-changes) and [§8.6](#86-event-resumption) is read at the revision that lands the
change that moved those sections' statements from Proposed to current, and not at `36f3bae2`. A
requirement this document proposes is
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
  `internal/auth/tokens.go:372-391`).
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
client (one exception, [§3.3](#33-client-actions-with-no-route): `cluster discover` reads
a cluster with the operator's own talosconfig through `internal/talos/discover`, a read-only
package; the first key is no exception, because it goes to the server through
`POST /clusters/{id}/first-keys` ([§3.2](#32-writes)), never to OpenBao from a client; the client
imports neither `internal/talos` nor `internal/provider`, so it never links the server's Talos or
provider packages), a new credential policy, the decisions of
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
| machine list / show | `GET /machines`, `/machines/{id}` | list | no | filter `cluster`; `pendingConvergence` (G24) and `formOnly` (API revision 13, persistence and API choice §17.48) (`internal/api/reads.go:199-218`) |
| machine position | `GET /machines/{id}/position` | no | no | one snapshot (G25, [persistence and API §9.3](persistence-api.md#93-examples)) |
| machine observations | `GET /machines/{id}/observations` | yes | no | |
| machine timeline | `GET /machines/{id}/timeline` | yes, by revision, oldest or newest first | no | JSON only, no stream (G9); `order`, and `tail`, the forward cursor from the newest entry, API revision 3 ([persistence and API §9.2](persistence-api.md#92-resources-and-routes)) |
| fragment, profile, assignment list / show / history / revision | `GET /{kind}s`, `/{kind}s/{id}`, `/{kind}s/{id}/revisions`, `/{kind}-revisions/{id}` | lists | head item only | `internal/api/routes.go:83-87` |
| draft list / show | `GET /drafts`, `/drafts/{id}` | list | item | the item's ETag is the one draft writes need |
| import list / status | `GET /ingestions`, `/ingestions/{id}` | list | no | filters `state`, `machine` (`internal/api/ingestions.go:280-299`) |
| import review | `GET /ingestions/{id}/review` | no | no | `author`, human; `no-store`; only while `paused` (`internal/api/review.go:30-52`) |
| import base summary | `GET /import-base-revisions/{id}` | no | no | `author`, human; `no-store`; size and counts only, no text, no digest (choice §17.38) |
| import base text | `GET /import-base-revisions/{id}/document` | no | no | `author`, human; for an import read from a node any `author`, for a file import only its importer who continued the review; `no-store` also on refusals (choice §17.41) |
| release list / show | `GET /releases`, `/releases/{id}` | list | no | |
| release review, provenance | `GET /releases/{id}/machines/{m}/review` | no | no | redacted |
| plan list / show | `GET /plans`, `/plans/{id}` | list | no | filters `state`, `machine` (`internal/api/plans.go:429-451`) |
| approval show | `GET /approvals/{id}` | no | none, by design | approvals are immutable |
| operation list / show | `GET /operations`, `/operations/{id}` | list | no | filters `plan`, `machine`, `state`, `kind` (`internal/api/operations.go:95-108`) |
| operation watch | `GET /operations/{id}/events` | JSON pages, or SSE | no | [§8.6](#86-event-resumption) |
| act list | `GET /acts` | yes | no | |
| recovery status | `GET /recovery` | no | no | |
| dependency list / show / releases / alerts | `GET /dependencies` (`class` filter), `/dependencies/{id}`, `/dependencies/{id}/releases`, `/dependencies/{id}/alerts`, `/dependency-alerts` | lists | no | |
| `whoami`, roles in the web header | `GET /identity` | no | no | the caller's kind, issuer and subject or service, principal, roles, expiry and epoch; takes no query (`internal/api/identity.go:22-59`). The web header shows the roles and says who is signed in by name from the provider's userinfo, not from this answer ([§9.3](#93-browser-sign-in) item 6), and shows no epoch |
| version check, server settings, start mode | `GET /server` | no | no | the answering process's version, API revision, features, start mode and read-only settings ([persistence and API §9.2](persistence-api.md#92-resources-and-routes)); refused before a recovery start's entry |

### 3.2 Writes

| Client action | Route | Roles | Human | `If-Match` | Success | Keyed | Start mode |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `cluster add` | `POST /clusters` | author | yes | | 201 | | |
| `machine add` | `POST /machines` | author | yes | | 201 | | |
| `machine set-endpoint` | `POST /machines/{id}/talos-endpoints` | author | yes | | 201 | | |
| `machine rename` (served from API revision 10, [persistence and API §3.5](persistence-api.md#35-a-machines-name)) | `POST /machines/{id}/names` | author | yes | | 201 | | |
| `cluster first-key` | `POST /clusters/{id}/first-keys` | author | yes | | 200 | `document` | |
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

`cluster first-key` sends one first key, a talosconfig, a control plane configuration or a secrets
bundle ([persistence and API §3.4](persistence-api.md#34-first-keys-and-the-talos-signer)). Its flags
are `--kind`, `--file`, `--endpoint` (required for every kind: the Talos node address the server tries
the key at, not a Bronzeward address; [operator experience §5.2](operator-experience.md#52-inventory))
and `--talos-context`. The file is read with a size limit, sent once and never copied, logged or
printed; a talosconfig is cut to one context first; errors name the route's rule and never quote the
server's Talos text. It is the one write sent once ([operator experience §5.2](operator-experience.md#52-inventory)):
each request dials the node, so no answer is repeated as [§8.2](#82-errors) says a write repeats a `503`.
The route stores the key before it commits the act, so only a refusal the route decides before the store
is a refusal that stored nothing (exit 1), and the client names those by problem code: `invalid-request`
(a header, the query or the body of the request, or the input check: `internal/api/api.go:421-442`,
`internal/api/tx.go:42-55`), `unauthenticated` and `identity-revoked` (`internal/api/api.go:326-338`),
`forbidden` (the role, or a route for humans only: `internal/api/api.go:400-408`),
`idempotency-key-required` (`internal/api/api.go:422`), `recovery-mode-active`
(`internal/api/api.go:259-261`), `not-found` (no such route or cluster: `internal/api/api.go:252`,
`internal/api/firstkeys.go:129-136`), `validation-failed` (a rule of the signer on the key, decided
before the write: `internal/api/firstkeys.go:186`), `talos-access-unavailable` (the node did not answer or
accept the key, decided before the write: `internal/api/firstkeys.go:188`) and a `conflict` that names no
earlier request (the key's path moved, nothing written: `internal/api/firstkeys.go:211`). Every other
answer is an unknown outcome (exit 3): every `5xx`, a lost answer, the refusals of the idempotency
lookup (`epoch-mismatch`, a conflict that names the key's record, a reused key), which may follow the
store, and a 4xx with any other code, such as `precondition-failed`, `scope-busy` or a code the client has
not met. The route sends none of those today, so each reads unknown only to be safe; a rerun replaces the
key (`replaced: true`), so the cost of the doubt is one repeat. `unauthenticated` is on the list because
`staleToken` (`internal/api/api.go:324-328`) refuses service tokens only and this route is for humans
(`internal/api/routes.go:127`), so no `401` can follow the store; a refusal added after the prepare step
must not carry a listed code without a marker the client reads, as a conflict's `request` member is. A failure before any byte goes out is
not repeated, and a `401` is not renewed and resent. A repeat that a refusal answers stays unknown. The server's detail is never printed. Each run takes a
key of its own, random like every write's ([§8.4](#84-idempotency-and-ambiguous-responses) item 1), so a
second run supplies the key again and does not replay the first answer; `--idempotency-key`,
`--expect-epoch` and `--expect-principal` repeat an unknown outcome as for any write. It is also the one
write that takes the Ctrl-C ([§8.2](#82-errors)): the request can stall at the node.

### 3.3 Client actions with no route

| Client action | Why no route | Contract |
| --- | --- | --- |
| `login`, `logout` | identity-provider and client concerns; the server keeps no session | [§9](#9-authentication) |
| `context` | client configuration | [§5](#5-endpoint-discovery-and-context-selection) |
| `cluster discover` | reads a cluster's nodes with the operator's own talosconfig and calls no Bronzeward route; prints the `cluster add`, `machine add` and `cluster first-key` commands that record it and runs none of them | `--talosconfig <file>` is required and nothing else is searched (not `$TALOSCONFIG`, not Talos's default file, not the current directory); table output only; the file is read with a size limit and never copied, logged or printed; errors give the node's gRPC code and never its text |
| `cluster onboard` | has no route of its own: it does the discover read, then runs the routes of [§3.2](#32-writes) for those steps, the first key included, the imports itself, and for each imported machine without an assignment one that selects nothing ([operator experience §5.2](operator-experience.md#52-inventory)); it sends every write of one run under the epoch the run's first read showed ([§8.5](#85-epoch-changes)), so once the epoch has changed the next write is not sent and no later one goes out, and the table output says so once and shows the steps as far as they got; a run whose first read shows no epoch sends nothing; it holds no OpenBao address or token | the same `--talosconfig` and read rules; `-o json` and `-o yaml` print one object at the end; the first key is sent once and never logged or printed; errors give the node's gRPC code and never its text |
| automation token issue, rotate, list | server-side command only | not offered by either client |
| dispatch, apply | the controller dispatches approved plans ([design §12.7](../design/Talos_Configuration_and_Machine_Management_Design.md#127-application-approval-and-dispatch-boundary); [§13.7](../design/Talos_Configuration_and_Machine_Management_Design.md#137-poc-identity-and-approval-policy) item 2) | not offered |

## 4. API gap audit

The eighteen gaps the journeys found, checked against the code above, plus seven this audit adds. G21-G24 are the journeys' G13-G16 (expected epoch, start mode, principal display name, convergence status), numbered last because they were added to the journeys last; the journeys' G17 is G18 here, and its G18 (the machine's whole position) is G25 here, numbered last for the same reason.
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
| G8 | Machines have no human-readable name | the machine resource has no name member (`internal/api/inventory.go:260-275`) | decided: an optional stored name, unique within its cluster, from the API revision that adds it ([persistence and API §3.5](persistence-api.md#35-a-machines-name), choice §17.43; Serge, typed 2026-11-20 11:33 CET: "A. Stored name in the API"); until a server serves it, and for a machine without one, clients show the identifier, the endpoint and the node ID (rule A9) |
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
| G23 | No principal display name: bodies carry principal identifiers only, and no route maps one to a subject or name | `internal/api/acts.go:19-30`, `internal/api/releases.go:53`, `internal/api/plans.go:195`, `internal/api/approvals.go:43`, `internal/api/recovery_entries.go:79`; no such route in `internal/api/routes.go:78-136` | API issue A7, served as `GET /principals` from API revision 4 ([persistence and API §9.2](persistence-api.md#92-resources-and-routes)) by [#335](https://github.com/ginsys/bronzeward/issues/335); the CLI's table and detail output and the web client's views resolve identifiers with it, the web client's once for each view, each read again and each return to a view after one with no name, and show the identifier where the read does not name it |
| G24 | No convergence status: the machine body serves `desired` and `applied.release`, and compares the artifact digests only to match an observation, never serving them, so a client cannot tell whether the `Desired` release's digest differs from the `Applied` one, which is what pending convergence means. After an adoption the `Applied` digest is the baseline's, so equal or different release IDs do not settle it | before [#280](https://github.com/ginsys/bronzeward/issues/280): [`internal/api/reads.go:155-166`](https://github.com/ginsys/bronzeward/blob/23c1bc2a233cbdf0916076e62016a82892107053/internal/api/reads.go#L155-L166); [execution and recovery §1](execution-recovery.md#1-supported-operation-and-state-values), [§6.3](execution-recovery.md#63-adopt) | closed by API issue A4 ([#280](https://github.com/ginsys/bronzeward/issues/280)): the machine's `pendingConvergence` compares the `Desired` artifact's digest with the `Applied` digest, `true` too for a `Desired` with no `Applied`, and serves neither digest (`internal/api/reads.go:155-171`) |
| G25 | No read answers a machine's whole position at once (the journeys' G18). The lists a client could combine (`GET /ingestions?machine=`, `GET /plans?machine=&state=`, `GET /operations?machine=&state=`) are separate reads at the default isolation level, so an operation that moves between two of them leaves every list empty, and a committed plan says nothing of its operation | `internal/api/reads.go:40-57`; [persistence and API §8.1](persistence-api.md#81-plans-and-their-operations); the unique scope indexes `internal/migrate/migrations/0001_schema.sql:457-464` | served: `GET /machines/{id}/position` answers the machine, its newest ingestion, draft and plan, the operation holding its scope and one `step` from one snapshot ([persistence and API §9.3](persistence-api.md#93-examples)), implemented by [ginsys/bronzeward#879](https://github.com/ginsys/bronzeward/issues/879), and the web client draws its line from the `step` |
| G26 | Plans, releases and operations carry no name, so both clients show them by identifier (the journeys' G19, [#564](https://github.com/ginsys/bronzeward/issues/564) item 1). A draft has a `title`; a plan names its `machine`, `release` and `operation`, a release its `draft`, an operation its `kind` and `subject` | `api/openapi.yaml` `DraftBody`, `PlanBody`, `ReleaseBody`, `OperationBody` and `OperationSubject` | client rule A8: both clients show a title derived from those fields; no API change; applied in the web client for the three objects' lists and headings (A8 item 6) and in the CLI for their lists and shows (A8 item 7) ([#987](https://github.com/ginsys/bronzeward/issues/987)), the other cross-references not yet ([#996](https://github.com/ginsys/bronzeward/issues/996)) |
| G27 | `GET /plans` filters by `state` and `machine` and not by `cluster`, so a cluster page cannot list its open plans without reading each machine's plans or the whole list (the journeys' G20) | `internal/api/plans.go:429-451` | accepted limitation: the cluster page does not list plans; the filters for releases and drafts stay G9's accepted client-side filtering |
| G28 | An operation body states a kind, a state, a subject and a `createdBy` and says in words nothing of what it did or what comes next, and `createdBy` is null for the `apply-config` and `adopt` operations the controller creates itself, so a client that shows it raw prints `none` | `api/openapi.yaml` `OperationBody`; `internal/api/commit.go:314`, `internal/api/adoption.go:298` (no `created_by` column); `internal/api/publications.go:59`, `internal/api/ingestions.go:202` (set it) | client rule A10: the web client derives the sentence, the next step and the starter from the body and the plan it names; no API change; applied in the web client's operation list and page ([#998](https://github.com/ginsys/bronzeward/issues/998)) and in the CLI's `operation list` and `operation show` ([#1014](https://github.com/ginsys/bronzeward/issues/1014)) |

Not gaps, by design: no dispatch or apply route, no token administration route, no secret
browser, and no WebSocket (polling and SSE only, [persistence and API §8.3](persistence-api.md#83-the-resource-and-its-progress)).

## 5. Endpoint discovery and context selection

A **context** names one Bronzeward installation and how to reach and sign in to
it. It holds no secret. The CLI holds contexts as the table says (`internal/client/config.go:19-56`,
`internal/client/config.go:166-231`); the web client has none (item 3). `oidc.scopes`
and `oidc.audience` are required with `oidc`: `Context.Check` refuses a context that lacks either,
naming each missing field and what to add, and `Open` and the sign-in run it before any request
(`internal/client/config.go:166-203`, `internal/client/client.go:76`, `internal/client/login.go:312`,
`cmd/bronzewardctl/commands.go:164`). A contexts file written
before the rule still loads, and `context list`, `use` and `remove` work on it; only a command that
opens the context refuses. `context add` stores `openid offline_access` when no `--scopes` is given
and requires `--audience`; `--from` stores the base scopes followed by the file's and requires the
file's `audience` (`cmd/bronzewardctl/commands.go:144-148`, `cmd/bronzewardctl/commands.go:160-162`,
`internal/client/discovery.go:31-36`, `internal/client/discovery.go:115-117`). The audience pre-check
(`internal/client/credentials.go:504`) therefore always has an audience to compare.

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
   [§9.2](#92-cli-sign-in). (Built: `internal/client/config.go:58`, `internal/client/config.go:148-162`.)
2. Every command that talks to a server shows the context's name and server in its error and
   wait output, so an operator with two installations never acts on the wrong one unseen (built:
   `internal/client/client.go:101`, `internal/client/client.go:295`). **Proposed:** a
   write in an interactive session always prints the context before it sends. Today a write names the
   context only when a notice happens to carry it, for example the warning of a human-only write
   sent with an automation credential (`internal/client/write.go:222-224`), the recovery banner
   (`internal/client/client.go:364-370`), or a retry or deprecation notice of the preliminary server
   read (`internal/client/client.go:255`, `internal/client/client.go:360`). `writeWith` and `Send`
   print no line of their own for the context (`cmd/bronzewardctl/writes.go:112`,
   `internal/client/write.go:197`), so an ordinary write with no such notice names none, and only
   some recovery commands name it in their own confirmation lines.
3. The web interface serves exactly one installation, the one at its own origin
   ([§10.1](#101-hosting-and-origin)). Switching installations is navigating to another origin,
   which starts with an empty state; nothing read under one origin is shown under another. (Built:
   the web client's paths are relative to its own origin, `web/src/request.ts:4`,
   `web/src/request.ts:378`, `web/src/config.ts:6`; its kept state lives in page memory,
   `web/src/drafts.tsx:46`.)
4. Today the issuer, client ID and audience are configured by hand (G14). Under decision D3 (b),
   a static client-configuration file served next to the web files, `bronzeward-client.json` at
   the web root, names them: a JSON object with the string members `issuer`, `audience`,
   `cliClientId` and `webClientId`, the last two the client IDs of the two registrations of
   [§9.1](#91-identity-provider-requirements) item 2, an optional number member
   `ingestWaitSeconds` (positive, web client only: the bound of the in-progress wait of
   [§8.4](#84-idempotency-and-ambiguous-responses) item 2, in seconds; absent is its default of 120, a wrong type or a
   value that is not positive is a configuration error, and the CLI ignores it), and an optional string member `scopes`:
   space-separated scopes requested after `openid offline_access`, as the CLI's `oidc.scopes`,
   for a provider that puts the audience into a token only when a scope asks for it (Zitadel's
   project audience scope is one). The web client also asks for `profile` itself where the
   provider's discovery lists it, for the name in its header ([§9.3](#93-browser-sign-in) item 6), so
   `scopes` need not name it. Absent is none; repeating `openid` or `offline_access` is a
   configuration error, as is any other member's wrong type (lead decision under the owner's
   2026-10-07 grant, 26 October 2026, recorded in
   [#332](https://github.com/ginsys/bronzeward/issues/332)). The
   web client reads it from its own origin and uses the web client's ID, and the CLI imports it
   with `context add --from <url>` under the TLS rules of [§6](#6-tls-trust) and stores the CLI
   client's ID, and as `oidc.scopes` `openid offline_access` followed by the file's scopes unless
   `--scopes` names others. Entering them by hand stays possible. The server is never asked
   for them through an authenticated route: the client needs them before it can authenticate.
   Built in the web client: it reads the file from its own origin with `credentials: "omit"`,
   requires all four string members, checks `scopes` and uses the web client's ID
   (`web/src/config.ts:11-61`, `web/src/auth.ts:120`). Built in the
   CLI: `context add --from` reads it, requires `issuer`, `cliClientId` and `audience` (it ignores
   `webClientId` and `ingestWaitSeconds`), checks `scopes` the same way, stores the
   CLI client's ID and the scopes as described, sends no `Authorization` header, and keeps the
   by-hand flags (`internal/client/discovery.go:16-59`, `internal/client/discovery.go:69-128`,
   `cmd/bronzewardctl/commands.go:106-118`, `cmd/bronzewardctl/commands.go:150-163`).
5. A context's `server` is checked once when it is added: `GET /livez` must answer `204`
   (`internal/server/server.go:19-21`), which shows only that something answers; it proves
   neither the API nor its version. (Built: `internal/client/client.go:116-139`,
   `cmd/bronzewardctl/commands.go:167-172`.)

## 6. TLS trust

Built in the CLI, except the clauses marked **Proposed** below; built in the web client where an
item names it. Sources are given per item. Item 6 is a browser fact and the server's own rule in
item 1 and its issuer fetches in item 7 are server facts, so none of the three cites client code.
The required check of item 8 stays **Proposed**: it is a test criterion, and this section does not
say that such a test exists.

1. `server` and `oidc.issuer` must be `https`. Plain `http` is accepted only to a loopback
   address. This is the loopback half of the server's own rule for its issuer, which also admits
   hosts the deployment lists (`internal/config/config.go:386-402`); a client has no such list,
   because it cannot know which network protects a host. The development setup
   (`examples/bronzeward.yaml:7`, `examples/bronzeward.yaml:14`) works and nothing else does.
   (Built in the CLI: `internal/client/config.go:166-252`, run by `internal/client/client.go:76` and
   `cmd/bronzewardctl/commands.go:164`. Built in the web client: `web/src/config.ts:48`,
   `web/src/config.ts:64-72` for the issuer and `web/src/auth.ts:73` for the endpoints it reads
   from discovery.)
2. Certificates are verified against the system trust store, or against the context's
   `certificateAuthority` bundle, which then replaces it for that host. Host name verification is
   always on. TLS 1.2 is the minimum. (Built in the CLI: `internal/client/transport.go:31-40`,
   `internal/client/transport.go:77`; `InsecureSkipVerify` appears nowhere in `cmd/`,
   `internal/client/` or `web/src/`, a search whose control, `MinVersion`, finds
   `internal/client/transport.go:31`.)
3. No option disables verification, in configuration or on the command line, and no environment
   variable does. An operator with a private CA adds the bundle to the context. (Built in the CLI:
   the `context add` flags `cmd/bronzewardctl/commands.go:100-112` hold none, the bundle is
   `commands.go:102` and `internal/client/transport.go:32`, and no variable the CLI reads touches
   verification: the reads are `internal/client/config.go:151`, `internal/client/token.go:35`,
   `internal/client/credentials.go:41`, `internal/client/store.go:141`,
   `internal/client/store_linux.go:24`, `internal/client/login.go:289` and
   `cmd/bronzewardctl/main.go:115`. The Go runtime's own `SSL_CERT_FILE` and `SSL_CERT_DIR`
   replace the system roots and disable nothing.)
4. A verification failure names the host, the subject and issuer of the presented certificate and
   the trust source used, and sends nothing. What a TLS or transport error shows that the remote
   side chose, such as a certificate's DNS names, which may hold any ASCII, is shown with each
   control character and each byte that is not UTF-8 escaped (`\x1b`), so it cannot write to the
   terminal. The escaping holds toward the server, toward an https proxy and toward the identity
   provider, in discovery, renewal and revocation, and for the server's own text in an error: the
   reason phrase of every status shown and each member of a problem document
   (`internal/client/printable.go`). It holds outside errors too, for every remote string a command
   shows (item 8 states that rule, with no list). The cases named first were: a `Deprecation` header, the subject and issuer of a signed-in or renewed
   token, of the token `whoami` reads and of the token a 401 is read for, the device grant's
   `user_code` and the server's version; a start mode `GET /server` does not document, `normal` or
   `recovery`, is shown as unknown. A server or issuer URL that holds a character the escaping
   would change is refused before any request uses it, as is such an endpoint or redirect the
   identity provider names. Toward the identity provider and the host `context add --from` reads,
   which may send a token in any of them, an error shows nothing those sent beyond the status code
   with its standard text and an error code RFC 6749, RFC 7009, RFC 8628 or OpenID Connect Core 1.0
   defines: no reason phrase, body, `error_description`, header or value decoded from the answer,
   and of a redirect target or a named endpoint only its scheme and host. A transport failure is
   named by its type (refused, reset, timed out, name not resolved, TLS alert). A trust refusal by
   `--from` or an https proxy still names the host, subject, issuer and trust source, fixed before
   anything is sent; a refused certificate's reason, on every path, is its host-name mismatch, which
   lists its names, or a fixed text (`internal/client/remote.go`). The standard library's log, which net/http writes a peer's text
   to, is discarded. The server's own text keeps the rule above. (Built in the CLI, by clause: the
   trust refusal and the nothing-sent text, `internal/client/transport.go:105-123`, reached from the
   read path `internal/client/client.go:288`, the write path `internal/client/write.go:690`, the
   event stream `internal/client/watch.go:458`, the liveness check `internal/client/client.go:124`
   and `--from` `internal/client/discovery.go:89`; text the identity provider and the `--from`
   host send, shown by its cause and never by its own words, `internal/client/remote.go:62-78`,
   `internal/client/discovery.go:92`, `internal/client/login.go:76`,
   `internal/client/credentials.go:393`, `internal/client/credentials.go:439`; the https proxy's
   certificate, `internal/client/transport.go:77`; URLs refused before use,
   `internal/client/config.go:193-231`; the log, `cmd/bronzewardctl/main.go:123`; a `Deprecation`
   header, `internal/client/client.go:360`; the signed-in subject and issuer,
   `cmd/bronzewardctl/commands.go:279`; the escaping place for errors,
   `internal/client/shown.go:30`.) **Proposed:** the `%q` in an error that shows a redirect's
   `Location` (`internal/client/watch.go:465`) is the Go form, not `\x22`; item 8 states the rule.
5. Certificate pinning is not offered: a bundle per context limits trust enough for this
   milestone, and pinning a leaf breaks on every renewal. (Built: `VerifyPeerCertificate`,
   `VerifyConnection` and an SPKI check appear nowhere in `internal/client/` or `cmd/`, a search
   whose control, `MinVersion`, finds `internal/client/transport.go:31`.)
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
   ([persistence and API §10.1](persistence-api.md#101-humans-oidc)). (Built in the CLI: no
   redirect followed, `internal/client/transport.go:94`; a `3xx` reported with its target,
   `internal/client/client.go:294-295`; the identity provider's redirects limited to `https` or
   loopback, `internal/client/credentials.go:476-485`, and none on a revocation,
   `internal/client/credentials.go:436`; the proxy variables and the verbose line,
   `internal/client/transport.go:24`, `internal/client/transport.go:48-50`; an https proxy dialled
   with TLS, `internal/client/transport.go:51-89`; an `https` URL goes through an http proxy by
   Go's tunnel, which the client leaves to `net/http` (`internal/client/transport.go:20-22`) and
   this section does not read. Built in the web client: `redirect: "error"` on the one send path,
   `web/src/request.ts:378-386`; the generic transport-failure text, `web/src/request.ts:8`,
   `web/src/states.tsx:208`; a write's transport failure as an unknown outcome,
   `web/src/write.ts:418`, `web/src/request.ts:13`.)
8. **Remote text in human output.** The CLI escapes every string that it shows in table, record
   or sentence output (`-o table`, the default) and that did not come from the CLI itself. That
   covers text from the server, the identity provider and a proxy, and text read from a file that
   the CLI did not write. It escapes the string before it formats it into a line, as follows:
   - Each control character (C0, DEL, C1) and each byte that is not UTF-8 becomes `\xNN`, as in
     item 4. The tab and the line feed are included: they become `\x09` and `\x0a`. A value that
     holds either could otherwise move a column or forge a row. Only the separators that the CLI
     writes itself stay raw. Each bidirectional control (U+061C, U+200E, U+200F, U+202A to
     U+202E and U+2066 to U+2069) becomes `\uNNNN`, in item 4 too: it changes the order in which a
     terminal draws the text around it, so a value could read as another. A backslash in remote
     text stays as it is, as in item 4. A value
     that holds the text `\x0a` shows as that text. It looks like an escape but cannot act on the
     terminal, and escaping every backslash would change each value that holds one for no gain in
     safety.
   - The rule has no list of fields. An ID, a state, a name, a title, a principal, a reason, a
     release identifier and a cell the CLI builds from such values are all remote text. The API
     schemas give these members as plain strings with no format (`api/openapi.yaml`, `ClusterBody`
     and `DraftBody`: `id`, `title`, `state` and the like), and the input rules differ from field
     to field: `ClusterInput.name` bans control characters, while `DraftInput.title` limits only its
     length. A member with an enumeration or a `date-time` format, such as `ServerBody.startMode`,
     is no exception: the format binds a correct server, not the bytes that arrive. A number or an
     array is not text, and the rule applies to the strings inside it.
   - A value that the CLI replaces or hides before it shows it is not shown as received, so the
     rule has nothing to escape in it. An unrecognised start mode appears as `unknown` (item 4),
     and the identity provider's error text appears only as the status, the known code or the
     transport cause that item 4 allows.
   - A string that the CLI builds into an error from a remote value, such as a cluster or draft ID
     in a refusal it words itself, follows the same rule. The client's own errors already follow
     item 4.
   - `context list` and `context add` show names, servers and credential kinds from the contexts
     file. That file can come from someone else, so its text is remote text.
   - A document or a diff is split on the document's own line feeds. This is the one exception to
     the line feed escape, because those line feeds are the document's lines. Each line is then
     escaped, the tab included, and shown whole. A person sees a tab as `\x09` and a carriage return as
     `\x0d`. The line itself is never rewrapped.
   - Every error passes one place before the CLI prints it. That place escapes all of the text,
     line feeds included, so an error from outside the CLI (a keyring or a library) cannot start
     a line of its own. The one exception is a multi-line error that the CLI builds itself, such as
     the outcome-unknown block: each of its lines is escaped and the CLI joins them.
   - Everything the CLI writes to the terminal passes one last guard: standard error always (errors,
     notices, prompts and the `-v` lines, such as the proxy URL from the environment), and
     standard output in `-o table`. The guard writes every control character but the line feed
     as an escape. It cannot tell a line feed in a value from the CLI's own, so each place that
     prints a value escapes it first. A flag error shows its rejected value in the same escape
     form, not the quoted form of the Go parser.
   - A value that a message puts between double quotation marks, such as a draft title in
     `draft show` or a context name in an error, also shows its own double quote as `\x22`.
     Without that, a value could close the quotes early and make its text read as the words that
     follow it. The Go `%q` verb did this for the draft title before item 8 replaced it. A
     backslash stays single, as above.
   - Where the CLI shows a value as JSON text that it encodes itself (a timeline entry, an
     operation result or error), the JSON encoder leaves DEL and the C1 range raw. The text is
     escaped again by this rule.
   - A writer between the table writer and the terminal may escape ESC, the C1 range and bytes
     that are not UTF-8 as a second guard. It is not the rule, because it cannot tell a field's
     line feed or tab from the CLI's own separators.
   - Before this item, `draft show` wrote the title with `%q` (escaped) and `draft list` wrote it
     raw, and `cluster list` wrote a cluster's name raw while the server's version was escaped.
     The rule removes that difference.

   Built in the CLI, by clause: the escape forms, `internal/client/printable.go:17-44` (`\xNN` and
   `\uNNNN`), the double quote as `\x22`, `internal/client/printable.go:77-79`, and a rule with no
   list of fields, `cmd/bronzewardctl/output.go:15-29`; the contexts file's text,
   `cmd/bronzewardctl/commands.go:40`, `cmd/bronzewardctl/commands.go:191`; documents and diffs
   split on their own line feeds, `cmd/bronzewardctl/records.go:196`,
   `cmd/bronzewardctl/plans.go:424`; the one place for errors, `internal/client/shown.go:30` and
   `cmd/bronzewardctl/main.go:258`; the last guard, `internal/client/printable.go:87-100`,
   `cmd/bronzewardctl/main.go:130-135`, `cmd/bronzewardctl/output.go:75-85`; a flag error without
   the Go form, `cmd/bronzewardctl/main.go:374`; the table writer, `cmd/bronzewardctl/output.go:137`;
   JSON the CLI encodes itself, `internal/client/printable.go:49-65`,
   `cmd/bronzewardctl/output.go:151`, `cmd/bronzewardctl/writes.go:167`; the `-o json` document and a
   composite object, `cmd/bronzewardctl/output.go:151`, `cmd/bronzewardctl/output.go:170`. **Proposed:**
   the double-quote rule for every message that quotes a remote value. The Go `%q` verb still
   quotes the `Location` header in `internal/client/watch.go:465` and a machine name in
   `cmd/bronzewardctl/onboard.go:173` and `cmd/bronzewardctl/onboard.go:175`.

   **Required check.** One test fills each remote source with a value that holds ESC, a tab, a
   line feed, a carriage return, DEL, a C1 character, a bidirectional control and a byte that is
   not UTF-8. The sources
   are all the remote text that item 8 and item 4 name: every string member of every response type
   in `internal/client/apitypes`, the response headers the CLI shows (`Deprecation`), the
   identity provider's and the proxy's text, every member of the contexts file, and every remote
   value that the CLI puts into an error it words itself. The response-type sweep is one part of
   it. The implementation issue lists the sources, and a source that the list lacks is a defect of
   the list. A source that the CLI replaces or hides (the start mode and the identity provider's
   error text, above) is checked for its prescribed output instead: the test requires `unknown`,
   or the status, code or transport cause, and fails if any injected byte appears. The test runs
   each command that shows a source in `-o table`, and each such error branch. It fails in
   any of these cases:
   - The output holds a raw control byte outside the CLI's own separators.
   - Outside documents, diffs and JSON text that the CLI encodes, a plain value does not appear in
     its escaped form: `\x1b` for ESC, `\x09` for the tab, `\x0a` for the line feed, `\x0d` for the
     carriage return, `\x7f` for DEL, `\x85` for a C1 character such as U+0085 and `\u202e` for
     a bidirectional control such as U+202E. A replacement
     such as `?` fails. A byte that is not UTF-8 and survives decoding appears as `\xNN` with the
     byte's own value. Documents, diffs and JSON text have their own forms, which the two last
     cases below and the rules above state.
   - The output holds an invalid UTF-8 sequence. The test injects a control character and a byte
     that is not UTF-8 into every remote value that it can reach, the raw JSON members
     `OperationBody.Error` and `OperationBody.Result` included, and checks this for every command
     in `-o table`. Where decoding has already replaced the byte with U+FFFD, the check still
     holds. It has no exemption by source.
   - Outside documents and diffs, a line has a different number of columns, or the output a
     different number of lines, from the same command run with plain values. This catches a tab
     that becomes padding, which leaves no raw tab and no extra row.
   - In a document or a diff, the lines are not those of the input split on its line feeds, each
     escaped. The expected lines come from the input, not from the plain-value run.
   - In JSON text that the CLI encodes itself, C0 characters do not appear in the JSON escape
     (`\t`, `\u001b`), or DEL and the C1 range do not appear as `\xNN`.

   A new response type, a new command or a new error branch passes this test before it ships.

   **JSON output.** `-o json` passes the server's document on, with each DEL, C1 and
   bidirectional control character written as `\uNNNN`. The JSON encoder escapes the C0 range
   and leaves these raw. The escape decodes to the same text, so the document keeps its meaning.
   The object a composite command prints (`drift adopt`,
   [operator experience §5.8](operator-experience.md#58-drift-freeze-then-adopt-or-revert)) is one
   document: the escape covers all of it, the nested answers and the text the client adds included.
   The same test runs every command in `-o json` and fails on a raw character of these kinds,
   on a raw C0 character that is not a line feed, or on a document that is not valid JSON.

   **Limit.** `-o yaml` passes the values through the YAML encoder. This item does not cover it.

   **Alternatives and reasons.** (a) Escape only fields that hold free text, and show IDs and
   enumerations raw. The schemas give most of those members as plain strings, and a server that does
   not follow a format it declares still reaches the terminal. The list of fields also grows with every command, which is the drift that
   left the server's version escaped and a cluster's name raw. (b) Keep the list of remote texts
   in item 4, and rely on the server to refuse control characters in its inputs. The CLI would
   then trust the server for resource text and distrust it for the version string. A control
   character that one input route does not refuse, or that arrived before a rule existed, still
   reaches the terminal.

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

Built in both clients; item 2's rule over every other enumeration was not traced and stays
**Proposed** there. Sources are given per item.

1. A client sends only fields the server it talks to is known to accept. Because the server
   refuses unknown request fields, a newer client sending a field an older server lacks fails with
   `400 invalid-request`, which is safe but useless; the client feature-gates such fields on the
   served API revision. (Built in the web client: its minimum, 12, is the highest revision of any
   route it calls, `web/src/shell.tsx:25-33`, `web/src/shell.tsx:476`. Built in the CLI: its minimum, 10, is the highest revision of any
   route it calls, a machine's name (`internal/client/client.go:19-21`); the others it calls are
   `GET /principals` (4), `GET /machines/{id}/position` (6),
   `GET /import-base-revisions/{id}/document` (7) and `POST /clusters/{id}/first-keys` (8). A server
   below 10 is refused once, with both revisions named, before any command's own request
   (`internal/client/client.go:161-163`), so the CLI carries no per-command gate. The clause covers
   request fields and routes: the CLI also reads response members added at revisions 13 to 18 (a
   machine's `role` and `formOnly`, a release's `hostname`), which a server below them omits, and it
   shows nothing for an absent member (`cmd/bronzewardctl/hostname_lines.go:33-35`,
   `cmd/bronzewardctl/reads.go:281`, `cmd/bronzewardctl/reads.go:342`).)
2. A client ignores unknown response fields, and never maps an unknown enumeration value to a
   known state or to success. It shows a start mode as `unknown` ([§6](#6-tls-trust) item 4) and
   any other such value as the text it received. In the CLI's human output, [§6](#6-tls-trust)
   item 8 escapes that text. (Built, for the start mode: the CLI, `internal/client/printable.go:104-110`,
   and the web client, which reads a value that is neither `normal` nor `recovery` as `unknown` and
   keeps no earlier mode for it, `web/src/recovery.tsx:42-48`, `web/src/shell.tsx:467-468`. For an operation's state the CLI treats only the five listed states as
   terminal, `internal/client/watch.go:61-67`. The rest of the item, over every enumeration both
   clients read, was not traced and stays **Proposed**.)
3. A client declares the minimum API revision it needs. It reads `GET /server` on context use and
   refuses, with both revisions named, a server below that minimum, before any write. `GET /server`
   answered `409 recovery-mode-active` without a `scope` member is a recovery start before its
   entry ([§8.5](#85-epoch-changes)), since the read is refused that way only then: the client
   allows only the entry, whose request it already
   knows from revision 1, and reads the revision once the entry has committed. The revision
   describes the process that answered; another instance behind the same endpoint may differ, so
   a refusal names the revision that answer served. The web client's minimum is the highest
   revision of any route it calls, raised in the pull request that adds such a call; it is 12.
   (Built in the CLI: the declared minimum, the read on context use and the refusal naming both
   revisions, `internal/client/client.go:19`, `internal/client/client.go:139-158`; before a write,
   `internal/client/write.go:241-243`; the recovery-start exception, `internal/client/write.go:242`.
   Built in the web client: `web/src/shell.tsx:33-40`, `web/src/shell.tsx:476`, the recovery-start
   refusal read at `web/src/shell.tsx:519`. The CLI's declared minimum is not the highest revision
   of every route it calls: see item 1.)
4. A `Deprecation` header on any response is shown once per command or page load, naming the
   route. (Built: CLI `internal/client/client.go:358-361`; web client `web/src/shell.tsx:40`,
   `web/src/shell.tsx:213`.)
5. Each client prints its own version and the server's, with its API revision, in
   `bronzewardctl version` and in the web footer; the server's as "unknown" while the read is
   refused. (Built: CLI `cmd/bronzewardctl/commands.go:196-220`; web client `web/src/shell.tsx:203-210`.)

## 8. Request handling

### 8.1 Requests

Every request sends `Authorization` and `Accept: application/json` (or
`text/event-stream` for a stream). Every `POST` and `PUT` sends a JSON object, `{}` when the route
takes no member, with `Content-Type: application/json`; a `DELETE` sends neither
([§2](#2-the-api-a-client-meets) request body row). Every write
sends exactly one `Idempotency-Key` ([§8.4](#84-idempotency-and-ambiguous-responses)) and, where
[§3.2](#32-writes) lists it, exactly one `If-Match`; every write but the recovery entry also sends
exactly one `Bronzeward-Expected-Epoch` ([§8.4](#84-idempotency-and-ambiguous-responses) item 3). A body is at most 1 MiB
(`internal/api/body.go:36`); a client refuses a larger input before sending and says so, rather
than relying on the server's `400`. Built in the CLI: the headers, `internal/client/client.go:284`,
`internal/client/write.go:676-686`, including no body and no `Content-Type` on a `DELETE`
(`internal/client/write.go:671-675`, `internal/client/write.go:678-680`); the keyed headers,
`internal/client/write.go:681-686`. Built in the web client: the one-megabyte refusal before any
send, `web/src/write.ts:70`, `web/src/write.ts:327-329`, and the keyed headers,
`web/src/write.ts:348-350`. **Proposed:**
for the web client, a `DELETE` sends neither: its writes always set `Content-Type: application/json`
(`web/src/write.ts:352`). For the CLI, the refusal of a larger body: it checks each document, file
and mark input (`cmd/bronzewardctl/fragments.go:28`, `cmd/bronzewardctl/fragments.go:160`,
`cmd/bronzewardctl/fragments.go:290-294`, `cmd/bronzewardctl/imports.go:200`,
`cmd/bronzewardctl/review.go:275`) but `Send` has no such check, so a body built from flags is not
bounded.

### 8.2 Errors

Client behaviour per problem code, from the code table of
[persistence and API §9.4](persistence-api.md#94-errors) and `internal/api/problem.go:11-36`, built in
both clients except the rows and clauses marked **Proposed** after the table. The
command-line client always shows `title`, `detail` when present, and `instance`, each escaped as
[§6 item 4](#6-tls-trust) states, and never shows a request body (built:
`internal/client/problem.go:28-47`). Exit codes are the journeys'
(journeys §4).

**The web client says it in words** (ginsys/bronzeward#1049). For each of the 26 codes of
`internal/api/problem.go` it shows one sentence for what happened and one for what to do, from one
table (`web/src/problems.tsx`), and never a code, a rule, a scope identifier or a request number
in a sentence. A rule can narrow a code where the general sentence would misstate the cause: a
`409 conflict` with rule `import-not-reviewed` reads "The server did not show you this import's
text. It shows a file import only to the person who continued it after its review, and an import
read from a machine only if the server is new enough to show it. Nothing needs doing: the import itself is not
affected. If this import was read from a machine, ask whoever runs the server whether it is up to
date.", true for every cause the server's `detail` names and for a server that still refuses an
import read from a machine (a client cannot tell from `GET /server`: the API revision is the same
before and after the change). The sentences of the import page (a refused mark, a resume that failed,
an operation that failed) come from the same two tables by code and rule, show a refused mark by its
position only, and where the table has no words for a code say so and point to the event list on the
same page, which keeps the code for a bug report. The server's `detail` is
shown after the sentences as "The server says: …", except where the table above says a path may be
in it (a refused mark: the position only), where it is shown nowhere, copy included. A code with no
entry shows the server's `title`. The status, `code`, `rule`, `scope` (when text), `instance` and
the `request` and `epoch` of an earlier record sit in a closed reference block ("Reference for a bug
report") with one Copy control that copies them as plain text and says whether it copied. Where
the outcome of a write is unknown, the notice says so and says how to check ("Check the current
state before you do it again.") and never says to try again, whatever the code: the next step of a
code's sentence is left out there. (Sources: the table and its rule narrowing, `web/src/problems.tsx:19-76`;
the sentence with no code, rule or request number and the title fallback,
`web/src/problems.tsx:136-143`; the reference block, `web/src/problems.tsx:149-160`,
`web/src/states.tsx:132-139` (the problem in words); the Copy control, `web/src/copy.tsx:11`; the unknown-outcome
sentence, `web/src/states.tsx:180`; the import page's sentences, `web/src/imports.tsx:86-110`.)

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
| 503 `dependency-unavailable`, `talos-access-unavailable`, `transient-conflict` | reads: retry with backoff (1 s doubling to 30 s, at most 5 tries, then stop; the one exception is the operation read of a watch after a stream that ended or a refused drain, [§8.6](#86-event-resumption) item 2, and the CLI's read of principal names under A7, which tries once, because a failed read shows the identifiers and is not worth a wait; the web client's read keeps the bound behind the identifiers it already shows, since nothing waits on it; every other read keeps the bound). Writes: repeat with the same key at most 3 times. If every attempt of the write was answered `503`, report **not done**: each such `503` states that its attempt's own transaction committed nothing, so no act or idempotency record exists for it; a fragment write's claim, a scope mark's observation or its start, or a principal row, taken before it stays ([persistence and API §9.4](persistence-api.md#94-errors)). If any earlier attempt was ambiguous ([§8.4](#84-idempotency-and-ambiguous-responses) item 2), a `503` to the repeat says nothing about that attempt: report the outcome as unknown, as in [§8.4](#84-idempotency-and-ambiguous-responses) item 4. A `503` `epoch-superseded` is not in this row: it is not repeated, by a read or a write, against the same server process, because that process started before the current epoch and cannot succeed until restarted (the CLI names the three codes above only: `internal/client/client.go:336-340`, and its write ends at `internal/client/write.go:431`; the web client's read returns it at once as unavailable, `web/src/request.ts:272-276`, and its write stops at `web/src/write.ts:500-503`). The server sends no `Retry-After` (G20), except `Retry-After: 1` on the `talos-access-unavailable` of `GET /clusters/{id}/talos-access` when a deletion time cannot be decided; the backoff above is not changed by it |
| an unknown code | treat by status class: 4xx as a refusal, 5xx as an unknown outcome for a write |
| a 5xx that is not a Bronzeward problem | an answer that is not `application/problem+json`, or whose `type` does not start `urn:bronzeward:problem:`, for example a `502`, `503` or `504` from the component in front of the server, may come after the server committed: for a write it is an ambiguous outcome as in [§8.4](#84-idempotency-and-ambiguous-responses) item 2, never **not done**; reads retry as for the `503` row |

**Where each row is built**, CLI then web client. `400`s: `internal/client/client.go:311-313`,
`internal/client/list.go:35-39`, `internal/client/list.go:81-98`; `web/src/request.ts:248-249`,
`web/src/machine.tsx:128`; the epoch-change handling only on a changed header,
`internal/client/client.go:190`, `internal/client/client.go:347-356`, `web/src/request.ts:431`.
`401`: `internal/client/client.go:236-244`, `internal/client/write.go:420-425`,
`internal/client/problem.go:38-43`, `internal/client/credentials.go:493-512`;
`web/src/request.ts:231-238`, `web/src/write.ts:436-452`, `web/src/app.tsx:165-170`. `403
forbidden`: `internal/client/problem.go:51-62`, `internal/client/write.go:448-452`;
`web/src/states.tsx:112-121`, `web/src/problems.tsx:70-73`, `web/src/write.ts:459-461`. `403
identity-revoked`: `internal/client/credentials.go:524-538`, `internal/client/problem.go:44-50`;
`web/src/request.ts:244-247`, `web/src/app.tsx:157-173`, `web/src/write.ts:457-462`,
`web/src/auth.ts:377-381`. `409`:
`internal/client/write.go:414-416`, `internal/client/write.go:426-441`,
`internal/client/client.go:328-332`, `cmd/bronzewardctl/fragments.go:78`;
`web/src/request.ts:60-64`, `web/src/write.ts:468-490`, `web/src/drafts.tsx:885`. `412
precondition-failed`: `internal/client/write.go:460-464`, `internal/client/write.go:557-567`;
`web/src/publish.tsx:185-186`, `web/src/problems.tsx:38`. `412 epoch-mismatch`, set aside and never
repeated: `internal/client/write.go:442-444`; `web/src/write.ts:494`. `422`: `internal/client/problem.go:70-86`,
`internal/client/write.go:445-447`; `web/src/problems.tsx:92`, `web/src/write.ts:495`.
`428`: the default case of `internal/client/write.go:465-468`; `web/src/problems.tsx:19`,
`web/src/problems.tsx:36-37`. `500`: `internal/client/write.go:417`; `web/src/write.ts:502-506`. `503`:
reads `internal/client/client.go:175`, `internal/client/client.go:229-274`,
`internal/client/client.go:336-340`, `web/src/request.ts:92-95`, `web/src/request.ts:222-263`; writes
`internal/client/write.go:182-185`, `internal/client/write.go:471-474`, `web/src/write.ts:77-78`,
`web/src/write.ts:496-501`, `web/src/write.ts:515`; the principal-name read that tries once,
`internal/client/client.go:173`, `cmd/bronzewardctl/names.go:39`. An unknown code and a `5xx` that
is no Bronzeward problem: `internal/client/write.go:417`, `internal/client/client.go:313-324`;
`web/src/request.ts:250-266`, `web/src/write.ts:502-506`. Neither
client reads `Retry-After` (`rg 'Retry-After' web/src internal/client cmd` finds nothing; control:
`rg 'Idempotency-Key'` finds `internal/client/write.go:681`). `404`, the server's meaning of
`epoch-mismatch` after an ambiguous attempt, and the server's behaviour for an unfinished first
request stay as server facts.

**Proposed**, where today differs: (a) a `403 forbidden` that carries a `plan` member refuses after
the lookup and is a known refusal. The web client treats it so (`web/src/write.ts:465`); the CLI
treats every `403 forbidden` as decided before the lookup and keeps an ambiguous outcome unknown
(`internal/client/write.go:448-452`). (b) `412 epoch-mismatch`: handle the epoch change with the `epoch`
member. Neither client reads the member to adopt the epoch; both adopt it from the
`Bronzeward-Epoch` header (`internal/client/client.go:347-356`, `web/src/request.ts:431`), and the
CLI only prints the member (`internal/client/write.go:443`). (c) `422 validation-failed` for marks:
the position only, never a path. The web client does so (`web/src/problems.tsx:92-96`) and the CLI
does for a mark refusal that arrives as an event (`cmd/bronzewardctl/review.go:369-381`), but a
`422` answer to a mark write prints `detail`, `path` and `paths` (`internal/client/problem.go:33-34`,
`internal/client/problem.go:75-81`, `cmd/bronzewardctl/review.go:282`). (d) `501 not-implemented` is a
known refusal. The web client excludes it from the unknown-outcome path (`web/src/write.ts:502`); the
CLI treats a `501` problem as ambiguous and repeats it (`internal/client/write.go:417`).

A Ctrl-C ends a read at once: a request in flight and a wait before a retry stop, the command prints
one line that names the request and exits 130. It does not change the retry rules above. The Ctrl-C
is taken for each read only. A write, and a read made inside a write, does not take it, with one
exception: `cluster first-key` takes it for its request in flight, which ends the request and leaves
the outcome unknown (exit 3, with the repeat of [§8.4](#84-idempotency-and-ambiguous-responses) item 4);
a Ctrl-C before the connection is taken sends nothing. The renewal
of a stored sign-in is not cut short: it runs to its end, and the stop follows. After the first Ctrl-C,
the attempt to read the event stream writes no further notice and runs no callback of its caller,
such as showing or clearing an event or asking whether to stop; a write that has already started is
not cut short. Other reads keep their notices. After the first Ctrl-C the client sends no further
read. A command that reads again after it, for a lookup that does not
decide its result, still exits 130 with the stop line of the first Ctrl-C. A write that was not
yet started is not sent. An error that the command returns beside the stop, such as a failed save
of the state, prints first, and the stop line ends the output. An unknown outcome keeps exit 3 and
its text.

The event stream of `publish` and of the import follow opens after their write completed. It is a
read, not a read made inside a write, so it takes the Ctrl-C. The wait before the next attempt to
open it takes the Ctrl-C too. The stop line names the stream request, and the command saves its
state before it exits 130. The stream still has no attempt bound ([§8.4](#84-idempotency-and-ambiguous-responses)
item 8). `operation watch` keeps its own handler of the Ctrl-C and does not use this one.

Built in the CLI, which is the only client these two paragraphs name: the stop on a read and its
line, `internal/client/client.go:217-224`, `internal/client/client.go:251-273`,
`cmd/bronzewardctl/interrupt.go:22-25`, `internal/client/interrupt.go:23-41`,
`cmd/bronzewardctl/main.go:243-252`; reads only, not writes, `internal/client/interrupt.go:23-26`; the
`cluster first-key` exception, `internal/client/write.go:339-349`, `cmd/bronzewardctl/main.go:182-191`,
`cmd/bronzewardctl/main.go:258-259`; the renewal that runs to its end, `internal/client/client.go:226-227`
(a comment) and `internal/client/credentials.go:264` (`renew` takes no context); no notice after the
first Ctrl-C, `internal/client/watch.go:383-397`; no further read, `cmd/bronzewardctl/main.go:179-193`;
a write not yet started is not sent, `internal/client/write.go:199-203`; the error beside the stop,
`cmd/bronzewardctl/main.go:245-252`, `cmd/bronzewardctl/main.go:278-300`; the stream of the import
follow, `internal/client/watch.go:341-379`, `cmd/bronzewardctl/imports.go:405-430`, its save,
`cmd/bronzewardctl/imports.go:409`, the stream loop without an attempt count,
`internal/client/watch.go:104-167`, and `operation watch`'s own handler,
`cmd/bronzewardctl/opwatch.go:184-191`; `publish` saves in a deferred call and follows with the
same `Watch` (`cmd/bronzewardctl/publish.go:38`, `cmd/bronzewardctl/publish.go:100`). The `503` paragraph is built as the
`503` row is (`internal/client/client.go:336-340`, `web/src/write.ts:78`); the server's own lines
in it are server facts.

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
`202` was already a success. Built in the web client, which words a failed operation by code and
rule from the same tables (`web/src/problems.tsx:83-87`, `web/src/imports.tsx:127-133`), and in
neither client does anything repeat the request. **Proposed** for the web client's next step: it
words the operation by `what` only, not the row's `next` (`web/src/problems.tsx:86`), and offers
"Import again" beside it (`web/src/imports.tsx:133`). **Proposed** for the CLI's next step: it shows the
operation's identifier and state with the raw error (`cmd/bronzewardctl/imports.go:592-609`) and
applies the row's next step only for `precondition-failed` and `ingestion-abandoned`
(`cmd/bronzewardctl/imports.go:601-608`); that function names no other code.

### 8.3 ETags

Facts: [§2](#2-the-api-a-client-meets) ETag row. After a restore to a backup taken at *T*, an
ETag issued after *T* answers `412`, while one for a revision the backup holds still matches; the
epoch is not part of the ETag
([persistence and API §12.3](persistence-api.md#123-what-a-restored-state-means-record-by-record),
[§4.1](persistence-api.md#41-etags)). A draft's ETag also moves when the server itself writes the
draft: when an ingestion's job commits its entries (`internal/api/runner.go:439-440`) and at
publication, which also closes the draft (`internal/api/publish.go:398-399`).

The journeys' last-seen rule (journeys §5.4, C5): the client sends the ETag it last
read or was answered for that draft, stores the one each write answers, and on `412` stops and
shows the current state. It never reads the new ETag and resends by itself: that would overwrite
another author's change unseen. One exception: after an import the client itself started on that
draft reaches `succeeded`, it re-reads the draft, since the server's own write moved the ETag. It
adopts the re-read ETag only if the draft's `revision` equals the import operation's
`result.draftRevision` (`internal/api/runner.go:443-444`); a higher revision means someone else
wrote since, and the client treats it as a `412`: it stops and shows the newer state. The CLI
records each ingestion that its own `import start` was answered for in its non-secret state, under
the context, its server and the current epoch, beside the ETags (`internal/client/state.go`). The
record of an ingestion ends when the CLI follows it to its end or abandons it, and an epoch change
drops it with the ETags. A command that removes a record or an ETag at an earlier epoch leaves an
entry that another command kept at a newer epoch unchanged. A continuation or takeover of an ingestion outside that record, another client's
import, adopts no ETag after success: the CLI drops the draft's kept ETag, says so and offers a
read of the draft, since the import can pass over an edit this client never read. ETags are kept per context and draft in memory; the
CLI may keep them in its non-secret state for the length of a scripted sequence. On an epoch
change it drops them all: more than the contract strictly needs, since a pre-backup ETag still
matches, but the client cannot tell which ETags were issued after *T*. The ingestion start's
`If-Match` is the target draft's ETag.

Built in both clients, by clause. The ETag sent and the one each write answers: CLI
`internal/client/state.go:83`, `internal/client/write.go:582`, `cmd/bronzewardctl/imports.go:178`; web `web/src/composition.tsx:192`,
`web/src/drafts.tsx:58-62`, `web/src/write.ts:426-427`. A `412` stops and nothing is resent by
itself: CLI `internal/client/write.go:460-464`, `internal/client/write.go:555-567`; web
`web/src/publish.tsx:185-186`. The re-read after an import that reaches `succeeded`: CLI
`cmd/bronzewardctl/imports.go:629-637`; web `web/src/imports.tsx:115-125`. The CLI's record of its own
ingestions, its end and its drop at an epoch change: `cmd/bronzewardctl/imports.go:570-576`,
`internal/client/client.go:388`, `internal/client/state.go:107-123`,
`internal/client/state.go:131-135`; the web client's own-start map, `web/src/imports.tsx:115`.
ETags per context and draft: CLI `internal/client/state.go:15-26`, `internal/client/state.go:83-105`;
web `web/src/drafts.tsx:44-52`, in page memory. An ETag kept under another epoch is never sent: CLI
`internal/client/client.go:388`, `internal/client/state.go:131`; web `web/src/drafts.tsx:48-51` and
`:58-62`. The ingestion start's `If-Match`: CLI `cmd/bronzewardctl/imports.go:178`; web
`web/src/imports.tsx:566`.

**Proposed**, where today differs: on a `412` the clause says the client "shows the current state".
The CLI prints the revision the draft has now and the command that reads it
(`internal/client/write.go:557-567`). The web publication page says that someone changed the draft
and shows no state: the operator has to press "Read the draft again" first
(`web/src/publish.tsx:177-181`, `web/src/publish.tsx:203`).

### 8.4 Idempotency and ambiguous responses

Facts: a key is scoped to the authenticated principal, a human's being its issuer and subject
(`internal/auth/auth.go:185-188`, `internal/api/idempotency.go:41-43`); the fingerprint covers
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

Items 1 to 8 are built in the clients they name, except where marked **Proposed** in them.

1. **Key.** One key per logical write, 128 bits from the operating system's random source,
   base64url without padding (22 characters, within `internal/api/api.go:394`). Never derived from
   the request content, so a key reveals nothing about a document. (Built: CLI
   `internal/client/write.go:176-180`, `internal/client/write.go:235`; web
   `web/src/write.ts:90-96`.)
2. **Ambiguous.** A write's outcome is unknown when the connection fails after any byte of the
   request was sent, when no complete response arrives (also when item 8's bound ends the
   attempt) unless its status is a `4xx` (item 8), on `500`, or on a `5xx` that is not a
   Bronzeward problem ([§8.2](#82-errors)). The client then repeats the identical request it still holds in memory, with the same key, at most 3 times with backoff.
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
   Built in both clients: what makes an outcome unknown, CLI `internal/client/write.go:417`,
   `internal/client/write.go:658`, `internal/client/write.go:705-708`, web `web/src/write.ts:417-421`,
   `web/src/write.ts:502-505`, `web/src/request.ts:316`; the repeats and backoff, CLI
   `internal/client/write.go:185`, `internal/client/write.go:313`, web `web/src/write.ts:80`,
   `web/src/write.ts:376-389`; the repeated `503`, CLI `internal/client/write.go:419`, web
   `web/src/write.ts:78`, `web/src/write.ts:496-501`; the in-progress `409`, CLI
   `internal/client/write.go:414-438`, web `web/src/write.ts:476-490`; the recorded refusal with both
   members, CLI `internal/client/write.go:465-468`, web `web/src/write.ts:476`, `web/src/write.ts:513`;
   the bound at least the served lease, CLI `internal/client/write.go:245-251`, web
   `web/src/write.ts:548-550`; the replay text, CLI `internal/client/write.go:383-385`, web
   `web/src/write.ts:423`, `web/src/states.tsx:19`; a replayed problem as the earlier refusal in the
   CLI, `internal/client/write.go:414-416`; the keyed `422`, CLI `internal/client/write.go:445-447`, web
   `web/src/write.ts:495`. **Proposed:** (a) a configurable bound for the in-progress wait in the
   web client. The CLI has it (`cmd/bronzewardctl/fragments.go:58`, `internal/client/write.go:434-435`);
   the web client's is the constant `INGEST_WAIT` (`web/src/write.ts:83`) and no setting reads it.
   (b) A replayed refusal in the web client: it carries the success replay text "Already done earlier;
   showing that result." before the refusal (`web/src/states.tsx:19`, `web/src/states.tsx:142-146`),
   where the clause asks for the earlier attempt's refusal alone.
3. **Same principal and epoch only.** Before any repeat the client checks that its credential
   still names the same principal: for OIDC, the same `iss` and `sub` in the renewed token; for
   automation, the same service identity, compared by the `principal` the identity read serves
   (A1, `internal/api/identity.go:56`): the client reads it once before the first send, and again
   with the current credential before a repeat whose token ID changed, so a rotated token of that
   identity is the same principal. An automation client that cannot read it sends nothing, so
   every write it sends has a `principal` to compare. Every write, its first send and each
   repeat, carries the reference epoch in `Bronzeward-Expected-Epoch`: the last epoch the client saw before the
   first send, or the epoch the caller names for the write where one that acts on its reads over time does (the
   adopt button's press names the epoch it began under, and the Connect a cluster page names the epoch its plan's first press began under for every press of that plan (a client that holds no epoch then sends nothing and the page says so), [A11](#14-proposed-api-gap-issues-and-downstream-refinement) item 4). An empty or repeated header is `400 invalid-request`, never taken as absent
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
   `internal/api/recovery_entries.go:256`). That is accepted with the exception, like the
   residual of AMB-10, and A6 does not close it. The entry's answer is then handled as in
   [§8.5](#85-epoch-changes), without setting the entry itself aside (REC-04).
   Built in both clients: the principal check before a repeat, CLI `internal/client/write.go:319-325`,
   `internal/client/write.go:593-622`, web `web/src/write.ts:521-533`, `web/src/write.ts:556`,
   `web/src/app.tsx:123`; the automation identity read once and refused when unreadable, CLI
   `internal/client/write.go:231-234`, `internal/client/write.go:608-616` (the web client has no
   automation credential); the reference epoch on every write, CLI `internal/client/write.go:273`,
   `internal/client/write.go:682-684`, web `web/src/write.ts:340`, `web/src/write.ts:349`; the caller's
   epoch, web `web/src/write.ts:340`, `web/src/handover.tsx:81`, `web/src/handover.tsx:109`,
   `web/src/onboard.ts:392-398` (the CLI sends no caller's epoch: it refuses a first send when the
   epoch moved since the reads that built it, `internal/client/write.go:274-281`); a minimum
   revision of at least 2, CLI `internal/client/client.go:19` (2), web `web/src/shell.tsx:33` (12); the one `GET /recovery`
   before the first send, CLI `internal/client/write.go:283-288`, web `web/src/write.ts:340`,
   `web/src/write.ts:563-565`; the confirming read before a repeat, CLI
   `internal/client/write.go:326-329`, `internal/client/write.go:624-634`, web
   `web/src/write.ts:525`, `web/src/write.ts:532`; setting the write aside, CLI
   `internal/client/write.go:319-329`, `internal/client/write.go:439-444`, `internal/client/write.go:538`,
   web `web/src/write.ts:302`, `web/src/write.ts:393-394`, `web/src/write.ts:468-471`,
   `web/src/write.ts:531-532`; the recovery entry's exception, CLI `internal/client/write.go:101-105`,
   `internal/client/write.go:326`, `internal/client/write.go:682`, `cmd/bronzewardctl/recovery.go:249-266`,
   web `web/src/write.ts:333`, `web/src/write.ts:337`, `web/src/write.ts:349`, `web/src/write.ts:524`; the
   entry's answer handled without setting it aside, CLI `internal/client/write.go:369-381`, web
   `web/src/write.ts:306`.
4. **After the process ends.** The CLI stores no request body, hash of a body, or key journal.
   When it gives up with the outcome unknown, it prints, on standard error, the key, method,
   route, context, principal (issuer and subject, or the service identity's `principal` from
   A1), epoch and the `If-Match` value it sent, if any, and what to read to find
   out. An ETag is not secret. An operator may then repeat with `--idempotency-key <key>
   --expect-epoch <epoch> --expect-principal <principal>`, plus `--if-match <etag>` where the
   route takes one, all as printed. The recovery entry is the exception: its block prints no
   epoch, and its repeat takes only `--idempotency-key <key> --expect-principal <principal>`
   (the exception of item 3 above). The client refuses, sending nothing, when one of them is
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
   that reconstruction. Built in the CLI: no body, hash or journal is stored (`internal/client/state.go:15-26`
   holds ETags and the started ingestions only); the block printed on standard error,
   `internal/client/write.go:143-166`, `cmd/bronzewardctl/main.go:258-260`; the repeat flags,
   `cmd/bronzewardctl/writes.go:33-37`, `cmd/bronzewardctl/writes.go:58-79`; the entry's block without
   an epoch, `internal/client/write.go:151-153`, `cmd/bronzewardctl/recovery.go:243-260`. **Proposed:**
   the refusals when a flag is missing or the principal or epoch differs; those were not read.
5. **Browser.** The page holds pending writes in memory only. Leaving or reloading the page drops
   them; the outcome-unknown notice shows the key and the resource to read, and no repeat is
   offered afterwards. (Built in the web client, the only client the item names: the page's writes
   are held in one in-memory set and nowhere else, `web/src/write.ts:153`, and the notice names the
   key, the route and the resource to read, `web/src/forms.tsx:35`, `web/src/forms.tsx:74-75`,
   `web/src/states.tsx:184-188`.)
6. A client never generates a fresh key for a write it has already sent. (Built in both clients: the
   CLI draws a key once per send and an operator's repeat takes the printed one,
   `internal/client/write.go:235-238`; the web client's key is fixed when its write is made,
   `web/src/write.ts:96`.)
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
   a new one if it is still wanted. (Built in both clients, for the first part: neither sends a
   new key itself, CLI `internal/client/write.go:439-447`, web `web/src/write.ts:468-495`. Built in
   the CLI, for the read: where the command gives it a read of the resource it prints what it shows,
   `internal/client/write.go:544-555`. **Proposed** for the web client: it reads the resource only
   when the operator presses "Show current state", `web/src/forms.tsx:39`,
   `web/src/forms.tsx:75`.)
8. **Attempt bound.** Every request that a client sends to the Bronzeward API has its own bound,
   except the event stream of [§8.6](#86-event-resumption). This item does not cover a request to
   the identity provider: the refresh-token request, for example, keeps the rule of
   [§9.3](#93-browser-sign-in) item 3, and its late answer is still read. The response headers
   must arrive within 30 s after the request is sent. The whole attempt, which is the request, the headers and the complete
   body, must end within 60 s. Each repeat of item 2 and each read of item 3 is a new attempt with
   a new bound. The CLI starts the header timer when it has written the whole request, as Go's
   `ResponseHeaderTimeout` does. The web client cannot see that moment, so it starts the timer
   when it sends the request. That bound is stricter, by the time the upload takes, and a request
   body is at most 1 MiB ([§8.1](#81-requests)). A bound that fires ends the attempt in the same
   way as a connection that the network cut at that point:
   - A read reports the transport failure that it reports for a cut connection.
   - A write is ambiguous under item 2 if a byte of the request possibly left. The web client
     cannot see whether one did, so a write attempt that the bound ends is always ambiguous there,
     never **not done**. The CLI counts an attempt as sent once it has a connection
     (`internal/client/write.go:486`, `internal/client/write.go:658`). A bound that fires before that is a failure with
     nothing sent (AMB-01), and a bound that fires after it is ambiguous.
   - One exception: if a write's headers carried a `4xx` status and the bound fires during its
     body, the server refused that attempt, so it is not ambiguous. The client reports the status
     as a refusal, says that the answer was cut off, and does not repeat. After an earlier
     ambiguous attempt, that refusal says nothing about the earlier attempt, so the outcome stays
     unknown (item 4). This is also what the CLI does when the network cuts such a body
     (`internal/client/write.go:703-709`).
   - An ambiguous write then takes the repeat path of item 2: the same key, at most 3 repeats,
     then the outcome-unknown report of items 4 and 5.

   So no write stays pending without end. The bound ends each attempt, item 2 limits the repeats,
   and the outcome-unknown notice of item 5 settles and offers Dismiss. The in-progress wait of
   item 2 keeps its own longer bound, and each repeat in that wait has the same attempt bound. A
   write that the server answers after the bound is an ambiguous attempt like any other, and its
   repeat uses the same key. The CLI's HTTP client already has these values
   (`internal/client/transport.go:91-95`): `ResponseHeaderTimeout` is 30 s, and `Timeout` is
   1 minute, which includes the body read. It
   also bounds the TLS handshake to 10 s, which a browser does itself. The web client applies the
   bound in its request layer, with one abort signal per attempt that covers the `fetch` and the
   body read ([#487](https://github.com/ginsys/bronzeward/issues/487)). The alternatives are D12
   in [§13](#13-alternatives-and-open-choices). (Built in both clients: the CLI's values,
   `internal/client/transport.go:91-95`; the web client's two bounds and their one signal per
   attempt, `web/src/request.ts:97-110`, `web/src/request.ts:347-352`, with the event stream
   exempt, `web/src/request.ts:263-271`; the web refusal whose answer was cut off,
   `web/src/write.ts:439`, `web/src/states.tsx:21`, `web/src/states.tsx:143`.)

### 8.5 Epoch changes

Facts: an epoch is a random identity minted at installation and at each recovery-mode entry,
compared by equality only
([persistence and API §12.1](persistence-api.md#121-the-epoch)). An epoch change makes cursors
`400 cursor-invalid`, earlier idempotency records `409`, a write whose key has no record and whose
`Bronzeward-Expected-Epoch` names the earlier epoch `412 epoch-mismatch`, earlier automation
tokens `401`, and streams `reset` ([§2](#2-the-api-a-client-meets)).

**Current.** Both clients compare `Bronzeward-Epoch` on every response with the last they saw for
the context (CLI: `internal/client/client.go:345-357`; web: `web/src/request.ts:204-213`,
`web/src/request.ts:431-450`). The web client drops an answer of another epoch than one it recorded
after the send, and reads again to learn the current one (`web/src/request.ts:205-209`). On a
change each:

1. discards what it kept for the context. The CLI keeps no cursor or page between commands, drops
   the ETags kept at an earlier epoch (`internal/client/client.go:388-392`,
   `internal/client/state.go:129-133`) and clears the events a watch showed
   (`internal/client/watch.go:254-257`). Within one list read it keeps the `next` cursor across
   pages and compares no epoch between them: it relies on the server, which binds a cursor to its
   epoch ([persistence and API §12.1](persistence-api.md#121-the-epoch)) and refuses another
   epoch's as `400 cursor-invalid`, after which the CLI reads the list again from its start with
   the items dropped, at most three times (`internal/client/list.go:81-96`). The web client
   replaces the page by the wait for the server, the identity and the recovery state, which it
   reads again
   (`web/src/shell.tsx:398-406`, `web/src/shell.tsx:516-517`), keeps an ETag only for the epoch it
   was seen in (`web/src/drafts.tsx:44-52`) and reads a list again from its start
   (`web/src/list.tsx:149-158`);
2. sets aside the writes still pending, as in [§8.4](#84-idempotency-and-ambiguous-responses)
   item 3, except a recovery entry, which that item repeats under its key. The CLI sets aside the
   write it has in flight (`internal/client/write.go:326-329`, `internal/client/write.go:440-444`;
   the entry's exception is the `!w.Entry` of `internal/client/write.go:326`); the web client sets
   every open write aside (`web/src/write.ts:302-314`, the entry's exception at
   `web/src/write.ts:306`);
3. tells the operator "The installation entered recovery mode or was restored; what was read
   before may no longer hold" (`internal/client/client.go:348-349`, `web/src/request.ts:16-17`,
   `web/src/shell.tsx:542`), and, while `Bronzeward-Recovery-Mode: true` is present, shows the
   recovery banner (journeys §8): the CLI once per command (`internal/client/client.go:364-370`),
   the web client in the page header (`web/src/shell.tsx:539`);
4. records the new epoch. A context's first epoch is recorded without a notice
   (`internal/client/client.go:347-351`, `web/src/request.ts:441-449`).

**Current** for the web client, with one exception to item 1's discard of every cached page: the
page holding unsaved text stays shown, with its actions disabled and a notice to copy the text, and
sends nothing, until the operator opens another page (`web/src/shell.tsx:385-389`,
`web/src/shell.tsx:509-512`). While the new epoch's server and identity reads are pending, and
through a failure a later read can mend (no answer, a `503` after its retries), the shell holds the
page as it was; once both reads answer for an accepted server, it shows under the new identity,
still disabled. On a refusal, a `403` or an incompatible server the page goes. The kept page's
lifetime is in the shell: the page sits under the one element the identity branch uses, so its
unsaved text stays (`web/src/shell.tsx:135-143`), and the kept view is dropped once another page
opens (`web/src/shell.tsx:508-526`). The tests at `web/src/shell.test.ts:248-275` test only which
reads keep the page (`keepsPage`) and that a kept page sends nothing. The retention bullet of
[§8.6](#86-event-resumption) item 6 states the same rule (H10 of
[#517](https://github.com/ginsys/bronzeward/issues/517)).

A command that shows one snapshot built from several reads (`status`, `recovery status`, the
table of `machine show`) shows nothing when an epoch change falls between the first and the last
answer it shows, in every output form, because the earlier answers, with the facts they carry, no
longer hold. `machine show -o json` and `-o yaml` print the machine answer alone, so they have no
second read to compare. After reads that all succeed, it says so and names the command that reads
again. A failed read that the command needs ends it with that read's own error. **Current** for
the CLI: `status` (`cmd/bronzewardctl/reads.go:435`, `cmd/bronzewardctl/reads.go:466-477`),
`recovery status` (`cmd/bronzewardctl/recovery.go:72-78`, before it prints in any form) and
`machine show` (`cmd/bronzewardctl/reads.go:312-337`). The web client has no command of this shape.

Three more commands read twice, and each compares the epoch of the answers themselves. After
`publish` succeeds, it shows no next step for the machines when a machine answer has another epoch
than the release answer, or when any answer showed another epoch than the one before while the
machines were read, even if a later answer set it back. It still reports the publication, which is
done, and names `release show` (it also prints its `release review` line;
`cmd/bronzewardctl/publish.go:150-183`). `-o json` and `-o yaml` print no next steps
(`cmd/bronzewardctl/publish.go:43-47`).
`profile set` sends nothing when the draft answer and the fragment list have different epochs, or
when the epoch changes before the send. It says that nothing was sent and names `draft show`
(`cmd/bronzewardctl/drafts.go:176-179`, `internal/client/write.go:274-281`).
`machine add` names the cluster only when the cluster answer has the epoch of the write's answer
(`cmd/bronzewardctl/writes.go:294-302`). These three are **current** for the CLI.
The wait of `import` does not end on a lapsed lease when the claim or operation answer has another
epoch than the events shown. It reads both again (CLI: `cmd/bronzewardctl/imports.go:499-508`; web:
`web/src/imports.tsx:254-261`). The watch clears the events shown when the
operation it reads has another epoch, even if the operation has no event to replace them, **when
the operation is not terminal** (CLI: `internal/client/watch.go:254-257`; web:
`web/src/progress.ts:223-226`). A terminal read of another epoch than the kept events clears them
only through the pages it drains, and ends once the drained events hold the operation's epoch;
where the pages show another epoch than the operation read's, it is read again after the backoff
(CLI: `internal/client/watch.go:224-236`; web: `web/src/progress.ts:208-216`,
`web/src/progress.ts:268-275`). **Current** for the CLI, as for the web: an empty event list of the
operation's epoch also clears the events kept from another epoch, so the watch ends with nothing
shown again (CLI: `internal/client/watch.go:583-586`; web: `web/src/progress.ts:268`). Every
other multi-page read needs no such comparison, because a cursor carries its epoch and a change
makes it `400 cursor-invalid`.

An epoch is never ordered or parsed: both clients compare it by equality, and split only an event
ID at its first colon to compare the part before it (`internal/client/watch.go:622-626`,
`web/src/progress.ts:101`). A missing `Bronzeward-Epoch` on an authenticated success is a defect
the client reports (CLI: `internal/client/client.go:304-307`, `internal/client/write.go:710-713`,
`internal/client/watch.go:501-505`; web: `web/src/request.ts:459-463`, `web/src/write.ts:428-429`,
`web/src/progress.ts:300-301`).

**Start mode** (G22). Neither the headers nor `GET /recovery` say whether the process was started
for recovery, which decides whether plan creation, approval and unfreeze can succeed (journeys
§5.9). Since [#278](https://github.com/ginsys/bronzeward/issues/278), `GET /server` serves it as
`startMode` ([persistence and API §9.2](persistence-api.md#92-resources-and-routes)), but a
recovery start refuses that read until its entry commits. The client keeps the last mode a
`GET /server` answer or one of the answers below showed, per context, and reads `GET /server`
again on context use and after a new epoch. A restart changes the mode without any signal, so the
kept mode is only the last one shown. **Current:** both clients keep the last mode shown, per
context (CLI: `internal/client/client.go:161-165`, saved at `internal/client/client.go:381`; web: the
shell's `mode`, `web/src/shell.tsx:382`) and read `GET /server` on context use (CLI:
`internal/client/client.go:146-164`; web: `web/src/shell.tsx:458-472`). After a new epoch the web
client reads it again every time (`web/src/shell.tsx:398-406`). The CLI does after a read whose
answer shows the new epoch (`internal/client/client.go:190-194`), and after the recovery entry
commits (`internal/client/write.go:387-393`). **Current** in the CLI: any other write answer that
itself shows a new epoch, whatever its status or body, is followed by that read; a read before or
between the sends that shows one is no answer of the write. The write's own result stands whatever
the read answers, and a failed read is said in a notice. A `409 recovery-mode-active` without a
`scope` member is the exception, even for an operator's repeat: its refusal shows the start mode, and
a recovery start refuses that read until its entry commits, so nothing reads `GET /server` after it,
the outcome's own read of the resource included (`internal/client/write.go:205-218`, `internal/client/write.go:342-349`).

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
   start mode. **Current.** The CLI takes the mode from all three shapes: `GET /server`
   (`internal/client/client.go:151`), a scope-less `recovery-mode-active`
   (`internal/client/client.go:328-332`) and an entry answer after the role check that carries
   `Bronzeward-Epoch`, other than a `401`, a `403` or the item 2 refusal
   (`internal/client/write.go:354-361`). The web client takes it from `GET /server` and from the
   scope-less `recovery-mode-active` (`web/src/recovery.tsx:33-44`, `web/src/shell.tsx:269`,
   `web/src/shell.tsx:300`, `web/src/shell.tsx:466-468`); it takes none from an entry answer
   itself. The entry's `201` carries the new epoch, which makes the shell read `GET /server`
   again (`web/src/request.ts:463-471`, `web/src/shell.tsx:398-406`); a `409 conflict` with
   `epoch` leaves the page on the mode it already showed.
2. **Normal start:** `GET /server` answering `"startMode": "normal"`, or the entry's
   not-a-recovery-start refusal, a `409 conflict` with neither
   an `epoch` nor a `request` member, whose detail says only a recovery start serves the entry. A
   `409 conflict` to the entry is never read as a normal start by its status and code alone.
   **Current.** Both clients take it from `GET /server` (`internal/client/client.go:157`,
   `web/src/recovery.tsx:42-48`, `web/src/shell.tsx:466-468`). The CLI also takes it from the
   entry's refusal, which it identifies by its missing `epoch` and `request` members and by
   `Bronzeward-Epoch` on the answer (`internal/client/write.go:355`,
   `internal/client/write.go:363-365`). The web client reaches the same mode another way: it
   notes the refusal and the shell reads `GET /server` again, which answers `normal`
   (`web/src/recovery.tsx:212-220`); it sets no mode from the refusal itself
   (`web/src/write.ts:514`). Neither client reads the refusal's `detail`.
3. **Unknown** before any such answer. The client then disables no action beforehand, and the
   recovery banner states the mode as unknown or as last shown, until the next such answer.
   **Current** for both: the CLI prints `unknown` for a mode never shown
   (`internal/client/printable.go:104-109`) and its banner names the mode last shown
   (`internal/client/client.go:364-370`); the web client starts at `unknown`
   (`web/src/shell.tsx:382`), says so in its banner (`web/src/recovery.tsx:31`,
   `web/src/recovery.tsx:63-71`), and the banner disables nothing (`web/src/recovery.tsx:61-62`).

Evidence for each refusal and entry answer is in the G22 row of [§4](#4-api-gap-audit). Item 1 relies on the
absent `scope` member: [persistence and API §9.4](persistence-api.md#94-errors) gives one only to a
scope refusal, since contract correction C2 in
[§14](#14-proposed-api-gap-issues-and-downstream-refinement) was applied.

### 8.6 Event resumption

Facts: [§2](#2-the-api-a-client-meets) events row. The SSE stream sends no `retry:` field in the
frames it writes (`internal/api/operations.go:295-353`); between events it polls every 500 ms
(`internal/api/operations.go:210-212`); after 15 s with nothing sent it sends a `: keep-alive`
comment line ([persistence and API §8.3](persistence-api.md#83-the-resource-and-its-progress)).

**Current and Proposed.** After each item, **Current** says what the clients do, with the line
that shows it, and **Proposed** says what a client does not do yet, one sentence per client.

1. A client follows an operation with the stream when it can and with JSON pages otherwise; both
   are complete. It remembers the last event ID it showed. **Current** for both
   (`internal/client/watch.go:122-170`, `web/src/progress.ts:167-197`).
2. When the stream ends it reads `GET /operations/{id}`. A terminal state does not show that the
   last events arrived: the stream may have been cut after the state was written and before its
   last events were (the server writes the state after the last entry and ends the stream only
   once they are written, `internal/api/operations.go:233-234`,
   `internal/api/operations.go:270`). So on a terminal state the client drains the backlog
   first: it reads the JSON event pages to their end and shows every event after the last one it
   showed, then ends the watch. A drain whose pages name another epoch than the operation read,
   an empty list included, clears what was shown and does not end the watch; the operation is
   read again after the backoff. The CLI and the web reader both do this. Any other state,
   `unresolved` included, means the stream ended because the token expired or the
   connection dropped: the client renews if needed and reconnects with `Last-Event-ID` and the
   renewed token, with backoff from 1 s to 30 s on repeated failures. The read of the operation
   that follows a stream that ended, and the read that follows a refused drain of a terminal
   state, are the only reads that do not stop at the five tries of
   [§8.2](#82-errors): when the server is out of reach (a retryable `503`, a 5xx that is not a
   Bronzeward problem, or a transport failure) a restart takes longer than five tries wait, so the
   read is repeated with the same backoff until the watch ends, by its timeout or its caller, and
   the watch then resumes with `Last-Event-ID`. A wait ends within 250 ms of the watch ending, and a
   watch that ends in an outage reports the operation as its last read showed it. A terminal read is
   not that last read once any later answer or event showed another epoch: it is of the installation
   before the restore. The client counts each such sign, and a read stands only while the count is
   the one its answer left. Counting, not comparing the last epoch, means that a later answer of the
   read's own epoch does not undo it. The signs are an answer of another epoch (a failed one
   included), and an event of another epoch than its stream in an open stream, with or without a
   `Bronzeward-Epoch` header on the stream. The same holds for every terminal read of the CLI: the
   first read, a read that the watch kept after the read of its events failed, and the read of a
   watch that read its events to the end. The CLI checks the count after it reads the pages of the
   end. A read whose answer has no `Bronzeward-Epoch` header
   is judged by the count in the same way. A watch that ends in an outage after that has
   no read that settled the state, and the CLI reports the state as unknown (exit 3). A read that fails
   with anything else is not repeated, and every other read keeps the bound of §8.2. A TLS trust
   refusal is the one failure of the two reads that is repeated in the same way, and in the open of
   the event stream: a server restarted behind a proxy can present another certificate for a while,
   and the client accepts none. Each try gives a notice naming the host and the trust source; a watch
   that ends in that state returns the refusal. TLS-01 holds for every other read: one try, nothing sent
   beyond the refused handshake. An attempt that showed
   nothing is a failure. The delay starts again at 1 s after an attempt that showed an event, or
   after a stream that stayed open for 30 s. Only the events shown since the attempt began or
   since the last clear count: an event shown and then cleared in the same attempt, by a `reset`,
   by an event of another epoch, or by an operation read that names another epoch, is not
   progress. A client that gets one event and then an event of another epoch on every attempt
   therefore waits between attempts. The CLI and the web reader both count this way.

   **Current** for both: the drain of a terminal state and the read again after the backoff when
   its pages name another epoch (`internal/client/watch.go:184-236`,
   `web/src/progress.ts:202-217`); the reconnect with `Last-Event-ID` (`internal/client/watch.go:445-446`,
   `web/src/progress.ts:293-295`) and the backoff from 1 s to 30 s
   (`internal/client/watch.go:270-286`, `web/src/progress.ts:233-242`); the attempt that
   showed nothing and what counts as progress (`internal/client/watch.go:275-278`,
   `web/src/progress.ts:237-238`).

   **Current** for the CLI: the operation read after a stream that ended, and after a refused
   drain, is repeated until the watch ends (`internal/client/watch.go:177-178`,
   `internal/client/watch.go:194`); a watch that ends in an outage reports the last read as it
   showed it, unless a terminal read is of an installation restored since
   (`internal/client/watch.go:133-155`, `internal/client/watch.go:211-213`,
   `internal/client/watch.go:240-242`); the count of signs and the exit 3 are checked after the
   pages of the end are read (`cmd/bronzewardctl/opwatch.go:352-361`). The wait between two
   attempts goes in steps of 250 ms that ask whether the watch ended, so a watch that ends
   through its timeout leaves it within one step and reports as above, Ctrl-C keeping its own stop
   (`internal/client/watch.go:341-365`, `internal/client/watch.go:246`, `internal/client/watch.go:281`).
   A TLS trust refusal is repeated in the two reads that run until the watch ends, the event
   stream's open and the operation read, each try giving a notice that names the refusal; a watch
   that ends in that state returns the refusal itself, whatever read of the operation came before, and
   not as the end of the watch: the command then reports the state as not read, never the state of its
   first read (`internal/client/watch.go:138-143`, `internal/client/watch.go:205-210`). An ordinary read is refused at once, one
   try (`internal/client/client.go:245-250`, `internal/client/watch.go:456-460`).

   **Proposed, not built by every client.** Web: an operation read that stays unavailable (a `503`
   or a 5xx once the client's tries ran out) ends the watch as failed, so it is not repeated until
   the watch ends, and the web keeps no terminal read across an outage, so it has no count of signs:
   it takes the epoch of the pages last-wins (`web/src/progress.ts:208-210`,
   `web/src/progress.ts:230-231`, `web/src/progress.ts:253-257`).
   `web/src/progress.test.ts:283-288` pins the failed watch as intended, and its comment says the
   CLI's watch does the same, which it does not (`internal/client/watch.go:177-178`). Web: the
   drain of a terminal state that is refused or stays unavailable also ends the watch as failed,
   where the CLI reads the operation again (`web/src/progress.ts:205-218`,
   `internal/client/watch.go:192-218`); `web/src/progress.test.ts:298-302` pins that too. Web: the
   pause between two attempts and the retry waits of a read are waited out whole when the watch
   ends through its timeout, not within 250 ms, where the CLI's end within one step
   (`web/src/progress.ts:83-90`).
3. A `reset` event clears what the watch showed, prints that the installation was restored or
   entered recovery mode, and shows the replay from the start. The epoch change is handled as in
   [§8.5](#85-epoch-changes). The server sends `reset` only when a stream opens
   (`internal/api/operations.go:264-265`); a stream that stays open across a recovery entry goes
   on with event IDs carrying the new epoch (`internal/api/operations.go:278`). So the client
   compares the epoch part of every event ID (before the `:`) with the stream's
   `Bronzeward-Epoch`; on a difference it ends the stream, applies [§8.5](#85-epoch-changes), clears
   what the watch showed and reopens the stream without `Last-Event-ID` (EVT-05). **Current** for
   both: a `reset` clears what was shown and the stream goes on
   (`internal/client/watch.go:547-549`, `web/src/progress.ts:332-334`); an event ID whose epoch
   part differs from the stream's clears it too and ends the stream, the CLI counting the move
   (`internal/client/watch.go:550-558`, `web/src/progress.ts:335-339`), and the next attempt
   reopens the stream without `Last-Event-ID` after the operation read and the backoff.
4. The browser cannot send `Authorization` with `EventSource`, so the web client reads the stream
   with `fetch` and a streamed response body, sending `Authorization`, `Accept` and
   `Last-Event-ID` itself (assumption S1). No token is ever put in a URL. The stream request has
   neither the headers bound nor the attempt bound of [§8.4](#84-idempotency-and-ambiguous-responses) item 8. A
   client whose sign-in ended ([§9.6](#96-failure-cases)) sends no stream request and stops the watch.
   **Current** for the web, the only client this item names: the stream goes through `fetch` with
   `Accept` and `Last-Event-ID` and no bound (`web/src/progress.ts:372-376`,
   `web/src/request.ts:293-301`, whose `send` takes `bounded` false, against its default of true at
   `web/src/request.ts:364`); a sign-in that ended stops the watch (`web/src/progress.ts:216`,
   `web/src/progress.ts:375`).
5. The keep-alive comment keeps an intermediary with an idle timeout above 15 s from cutting a
   quiet stream; a client ignores comment lines and keeps its last event ID. A connection still
   cut without the operation having ended is covered by item 2. A client takes a stream that
   sent nothing for 45 s, not even a keep-alive comment, as cut. Every line it receives, a
   comment included, starts the 45 s again, and the first wait starts when the request is sent,
   so an answer that never sends its headers ends the same way. The CLI's `watch` and the web
   reader use this value (`internal/client/watch.go:55-57`, `web/src/progress.ts`).
   The web reader reads a refused stream (any status below 500 except 200) as a JSON page of
   the same events. That read renews a refused token (item 2) and reports any other refusal by
   its status. This is the behavior of the web reader, not a rule for every client. The CLI
   renews an eligible `401` of the stream itself and opens the stream again, reports another
   client refusal as the server sent it, and rejects a redirect ([§9.5](#95-renewal),
   `internal/client/watch.go:370-374`, `internal/client/watch.go:464-466`,
   `internal/client/watch.go:477-479`). **Current** for both: the comment lines and the 45 s
   (`internal/client/watch.go:520-528`, `web/src/progress.ts:95`, `web/src/progress.ts:315-322`),
   and the web's refused stream read as pages (`web/src/progress.ts:188-191`,
   `web/src/progress.ts:298-299`).
6. The machine timeline has no stream (G9). A client reads its newest page with `order=newest`
   and keeps that answer's `tail`; "Load older" follows `next` with `order=newest`. While the
   timeline is on screen the client polls forward from `tail` (from the start while there is
   none), in `oldest` order, no more often than every 5 s, follows `next` while it is present,
   and keeps the `tail` of the last page, the one without `next`. A server below API revision 3
   refuses `order` as `400 invalid-request` and serves no `tail`, so a client that reads the timeline this way declares
   revision 3 as its minimum. When a poll returns entries the client did not show, a view that
   also shows the machine and its observations reads both again. It keeps what it shows until
   they answer, and the timeline poll goes on unrestarted: the timeline can record a freeze, an
   observation or an applied release that the first machine read predates. Only one such read
   again is in flight. A poll with new entries during it does not abandon it: the client reads
   both once more after both answer, so polls that come faster than a read do not stop every read.
   A view is one path and query. **Current** for the web: the newest page, "Load older", the poll
   from `tail` in oldest order every 5 s, and the one read again at a time, which for the web
   covers the machine, its observations and its position, and waits for all three before it
   starts another (`web/src/machine.tsx:96-132`, `web/src/machine.tsx:160-172`,
   `web/src/machine.tsx:292-312`, `web/src/machine.tsx:355-372`); the web declares API revision 12
   as its minimum, which includes revision 3 (`web/src/shell.tsx:33`). **Current** for the CLI,
   which has no view that shows the machine: `machine timeline --follow` reads the newest page,
   then polls forward from `tail` every 5 s; the CLI's minimum, 10, includes revision 3, so a
   server below it is refused before any request (`internal/client/client.go:19-21`,
   `cmd/bronzewardctl/timeline.go:109-139`). What W3 keeps when a read again of the machine or its
   observations fails ([#442](https://github.com/ginsys/bronzeward/issues/442)); each test citation
   asserts the rule it follows:
   - A failure the server answers keeps what the view last read and shows the failure above it.
     These failures are a refusal the page shows, a `503` after its retries, and `epoch-superseded`,
     which the client reads as unavailable (`web/src/request.ts:276`;
     `web/src/request.test.ts:211`, `web/src/machine.test.ts:226`, `web/src/machine.test.ts:240`, `web/src/machine.test.ts:395`).
   - A read that gets no answer while the view still shows data reports it only through the
     offline banner ([W11](operator-experience-wireframes.md#w11-empty-withheld-error-and-offline-states)).
     The data stays, with no failure of its own above it, also after a failure the server answered
     (`web/src/shell.test.ts:195`, `web/src/machine.test.ts:431`). A read with nothing to show
     shows the disconnected state in place. When the banner ends because a read answered, W3 reads
     the machine and its observations again once, as for a timeline poll with new entries: one
     read again at a time. Until they answer, W3 shows what it keeps, as during any read again.
     After that, no data older than a failed read stays shown without a sign
     (`web/src/shell.test.ts:213`, `web/src/machine.test.ts:463`,
     `web/e2e/auth/inventory.e2e.ts:366`). A transport failure answers at once, so this read again
     runs at most once per poll interval, counted from when the last one started. A resource that
     keeps getting no answer is not read again at network speed, also when a slow read holds the
     read again (`web/src/machine.test.ts:480`, `web/src/machine.test.ts:507`,
     `web/src/machine.test.ts:537`). A read again that the old view asked for does not run in a
     new view (`web/src/machine.test.ts:537`).
   - A refusal of the credentials (`401` after renewal, `403 identity-revoked`) ends the page
     ([§9.6](#96-failure-cases)), and nothing stays (`web/src/request.test.ts:119`,
     `web/src/request.test.ts:149`, `web/src/shell.test.ts:348`).
   - A new epoch and the process's recovery refusal end the page ([§8.5](#85-epoch-changes)), and
     nothing stays, with one exception for a new epoch: a page holding unsaved text stays shown,
     with its actions disabled, and sends nothing, until the operator opens another page
     (`web/src/shell.tsx:135-143`, `web/src/shell.tsx:508-526`; the tests at
     `web/src/shell.test.ts:248-275` test only which reads keep the page and that it sends
     nothing). The recovery refusal ends the page at once (`web/src/shell.test.ts:302`,
     `web/src/shell.test.ts:316`).
   - An observations read that restarted after a refused cursor and then failed keeps nothing: the
     list goes with its cursor ([§8.2](#82-errors)). W3 still shows the machine's own rows with
     the observations' failure; only the latest read's line needs the observations. If the machine
     read also failed, its failure is still shown (`web/src/machine.test.ts:240`,
     `web/src/machine.test.ts:385`, `web/src/machine.test.ts:585`).
   - A new view keeps nothing of the old view. When the view changes, the old view's answer still
     shows at first. A later failure does not keep it, a read without answer included, and the
     restart flag does not carry over (`web/src/machine.test.ts:213`, `web/src/machine.test.ts:240`,
     `web/src/machine.test.ts:452`).
   - The observations' restart notice stays through the reads again after it within the view
     (`web/src/machine.test.ts:213`, `web/src/machine.test.ts:254`).
7. The operation page ([W8](operator-experience-wireframes.md#w8-operation-timeline)) shows a time on
   each event row. The stream carries no event time (the server sends `at` only in the JSON form,
   as `api/openapi.yaml` says of the events route), so while the watch runs each row shows the browser's clock at the
   event's arrival and is marked "received": the page never presents it as the server's. When the
   watch ends, and each time the page opens an operation again, the page reads the JSON event pages
   once and replaces every row's time with the server's `at`, without the mark. A page that cannot
   read the pages keeps the received times and says that the server's were not read. The CLI's watch
   has its own rule ([operator experience §5.7](operator-experience.md)). **Current** for the web
   (`web/src/operation.tsx:153-182`, the "received" mark at `web/src/operation.tsx:188`, the notice
   at `web/src/operation.tsx:26`). After a watch that failed, the page shows the failure and the
   line that the server's times were not read, and reads no pages (`web/src/operation.tsx:170`;
   `web/src/operation.test.ts:100-105`). **Current** for the web as well.

## 9. Authentication

### 9.1 Identity-provider requirements

Facts: the server accepts a JWT access token signed with an asymmetric algorithm, with `iss`
equal to the configured issuer, `aud` containing the configured audience, `exp`, `iat` and a
non-empty `sub`, and `exp - iat` at most `maxTokenLifetime`, 15 minutes by default; it refuses
a token carrying `nonce` or `at_hash`, which mark an ID token, and one without a `jti`; roles
come from the configured groups claim, mapped in deployment configuration
(`internal/auth/auth.go:145-248`, `internal/config/config.go:121-125`,
`internal/config/config.go:359-360`). A disabled account or removed group takes effect when the
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

Built in the CLI, which is the only client this section names, except the parts marked **Proposed**
in the paragraph "**Proposed**, where today differs" at the end of the section. The paragraph "Built,
by item" before it holds the code sites for each item.

1. `bronzewardctl login` runs the authorization-code grant with PKCE `S256`, `state` and `nonce`,
   using a loopback redirect on `127.0.0.1` with a port the operating system chooses, and opens
   the system browser. If no browser can be opened it prints the URL for the operator to open on
   the same host, and names `login --device` for a host without a browser. The listener accepts
   one callback carrying the sign-in's `state`, and closes; a callback carrying another `state`,
   which any local process that finds the port can send, is answered with an error and ignored,
   and the wait goes on.
2. Headless use, where no browser runs on the host, uses the device-authorization grant only
   (decision D4 (a)): the CLI prints the user code and the verification URI, shows the URI's host
   prominently so that a lookalike prompt stands out, and polls the token endpoint. There is no
   fallback to a loopback redirect over a forwarded port. A sign-in the CLI starts by itself
   (item 3's memory-only case) on a host without a desktop session uses this grant. An explicit
   `bronzewardctl login` keeps item 1's grant unless `--device` is given, because a browser may
   still run on the host without one, for example on another console.
3. **Store.** The refresh token and the current access token are kept in the operating system's
   credential store (Secret Service, Keychain or Windows Credential Manager) under the context's
   name. Where no store is available (decision D2 (a) and (b)), the tokens are kept in memory for
   that process only and each new process runs `login`. On Linux no store is available when no
   D-Bus session bus is named, or when no program on it owns the Secret Service name
   (`org.freedesktop.secrets`) and none can be started for it; a store that answers and then
   fails refuses `login` before the sign-in starts, so nothing is issued that cannot be kept. A
   named bus that cannot be asked, for example a stale address, counts as a store that fails. On
   macOS the store is the user's default keychain, reached through the system's `security` tool: no
   store is available when that tool is missing. Every read, write and removal names the default
   keychain, and the question about a locked keychain asks about that same keychain. An entry in
   another keychain of the search list is not read, and a locked second keychain never shows a
   dialog. The tool gets the secret on its standard input, never
   in an argument, and takes at most 4096 bytes of command line, so a sign-in over about 2.9 KB
   is refused with that limit named and nothing written. The tool reads a backslash, a quote
   and a control character in a name as syntax. A context whose name holds one, which
   `context add` accepts, is refused by the Keychain store, before the sign-in starts, with
   `the macOS Keychain store cannot keep the sign-in of the context "<name>": its name holds a
   backslash, a quote or a control character, which the security tool reads as syntax. Remove the
   context and add it again under a name without them`, and nothing is written. The user renames
   the context by removing it and adding it again. Logout and the removal of such a context
   succeed, since no sign-in was kept under it. A stored entry whose go-keyring encoding is
   malformed is replaced by the next login and removed by logout and by the removal of its
   context. A removal that applies only to one revoked sign-in leaves such an entry alone, because it
   cannot tell whose it is. Under the
   lock the keychain is asked whether it is locked before each call, including a write and a
   removal. A locked keychain gives no answer, and no answer counts as locked. Each call is then
   refused within about 15 seconds (`securityHeldBound`, `internal/client/store_darwin.go:47`), as
   a locked store refuses a command, and nothing is written or removed. On a desktop, the tool can
   show its unlock dialog during that wait. The question and the call after it share one bound of a minute, and the
   tool is stopped when it passes. A keychain can lock between the question and the call. The call
   can then show its password dialog under the renewal lock, for at most that shared minute, and
   the tool is then stopped. An error of the tool
   never carries its standard output, which holds the secret on a read. A sign-in
   whose context was removed or changed while it ran is not kept, and its refresh token
   is revoked. Removing a context removes its stored sign-in under the renewal lock, with a
   bounded read first, and leaves an entry alone when a context of that name was added again
   meanwhile. An explicit per-context opt-in keeps them
   in a file readable only by its owner instead, offered only when the provider issues rotating,
   client-bound refresh tokens to public clients ([§9.1](#91-identity-provider-requirements)
   item 3). CLI processes that share that file renew one at a time, holding a lock on it while
   they renew, and read the new pair from it; a context kept in the credential store has a lock
   of its own in the user's state directory. That lock is never held across the store's unlock
   prompt: the store is read, and may prompt, for at most a minute before the lock is taken;
   with `--no-input` it never prompts, and a locked store is refused as one that failed.
   Under the lock no call to the store shows a prompt, so a store locked by then refuses the
   command, with nothing written or removed. Each store operation under the lock gets at most a
   minute for its connection to the session bus and its calls together. Neither the session bus
   nor the D-Bus library ends a connection or a call that gets no answer, or a write that a bus no
   longer reads. Under the lock the session bus is reached only over a Unix socket or TCP. Another
   transport fails the command, with nothing written or removed. A store that does not answer in
   time fails the command. If the call
   that writes or removes the entry was not yet sent, nothing was written or removed. If that call
   itself got no answer, the command says that its outcome is unknown: the store may still carry
   it out after the lock is released. A command waits at most four minutes for a lock that another
   bronzewardctl process holds, longer than a renewal's bounded read, refresh and write together,
   and then fails, naming the lock file. The stored sign-in is not changed. Other changes of the
   command stay, for example the context that `context remove` already removed.
   One that locks while a renewal waits on the identity
   provider refuses the renewed pair: its rotated refresh token is lost, as with any failed write,
   and the operator signs in again once the current access token expires.
   One renewal is the exception to these failures: the renewal that a command starts by itself
   when less than 60 s of the access token's life is left ([§9.5](#95-renewal) item 1). A store
   that does not answer in time, a write that got no answer and a lock wait of four minutes make
   that renewal fail under [§9.5](#95-renewal) item 3. They do not fail the command. The command
   shows the error as a notice, with the unknown outcome of a write in the words above, and goes
   on with the current token until the server refuses it. A renewal that follows a `401` is not
   this case.
   The opt-in is the context's `oidc.tokenFile`
   ([§5](#5-endpoint-discovery-and-context-selection));
   only in a context that has it may `BRONZEWARD_TOKEN_FILE` override its path. The CLI writes
   that file and clears it as its own stored credential ([§8.2](#82-errors)), and refuses to
   write or clear a file that holds an automation token (`bwt_`) or anything else that is not
   its own document, a refresh-only one included; `login` checks the file before the sign-in
   starts and again before writing, and refuses a symbolic link at its path.
4. No token is printed, written to the configuration file, passed in argv or placed in an
   environment variable by the CLI. No command prints a token.
5. `whoami` shows the issuer and subject from the token's claims, read without verification and
   labelled as the client's reading, and the expiry. Roles come from A1 (`GET /identity`,
   `internal/api/identity.go:39`).

**Built, by item.** Item 1: the grant, PKCE, `state` and `nonce`, `internal/client/login.go:189-227`,
`internal/client/login.go:275`; the printed URL, `internal/client/login.go:199-204`. Item 2: the
device grant, `internal/client/login.go:95-122`, `cmd/bronzewardctl/commands.go:266-276`, and a
self-started sign-in on a host without a desktop, `internal/client/credentials.go:245-246`. Item
3, the store: Linux, `internal/client/store.go:174`, `internal/client/store_linux.go:24-50`,
`internal/client/store_linux.go:94-246`; macOS, `internal/client/store_darwin.go:44-76`,
`internal/client/store_darwin.go:147-329`, `internal/client/store_security.go:19-50`; no store, in
memory, `internal/client/store.go:25-27`, `internal/client/store.go:166-168`; a failing store refuses
`login`, `internal/client/login.go:325-329`; `logout` and context removal,
`internal/client/store.go:128-130`, `internal/client/store.go:195-215`,
`internal/client/store.go:267-315`; a sign-in whose context changed meanwhile,
`internal/client/login.go:349-373`; the lock and the renewal under it,
`internal/client/credentials.go:264-302`, `internal/client/credentials.go:327-356`,
`internal/client/store.go:110-153`; the notice of a renewal that fails,
`internal/client/client.go:92-96`; the token file, `cmd/bronzewardctl/commands.go:111`,
`internal/client/credentials.go:37-65`, `internal/client/credentials.go:94-176`,
`internal/client/login.go:322-386`, `internal/client/token.go:67`. Item 4: `Setenv` appears nowhere in
`cmd/bronzewardctl` or `internal/client` outside tests, `os.Environ` only in the re-execution that sets
`GOTRACEBACK=none` (`cmd/bronzewardctl/main.go:120`), and `exec` is used outside tests only to open the authorization URL
(`internal/client/login.go:297`, the control of that search) and to run `security` with the secret
on its standard input (`internal/client/store_security.go:27-28`); the token's claims are the only
output of a token (`cmd/bronzewardctl/commands.go:281-282`). That is a search of those two
directories for those names, not a trace of every output. Item 5: `cmd/bronzewardctl/commands.go:235-259`,
`internal/client/credentials.go:208-219`.

**Proposed**, where today differs: (a) opening the system browser works on Linux with a desktop
only (`internal/client/login.go:287-303`, `xdg-open`); on macOS the URL is printed and no browser
opens. (b) The credential store is built for Linux and macOS only: other systems report that the
store is not supported (`internal/client/store_other.go`), and the release builds only those two
(`.goreleaser.yaml:15-17`). (c) The entry that is not valid is replaced by the next login and removed
by `logout` on macOS only (`internal/client/store_darwin.go:65`, `internal/client/store_darwin.go:243-251`);
on Linux a malformed entry reads as none (`internal/client/store.go:91`). (d) The token file is
"offered only for a provider that rotates": `context add --oidc-token-file` takes it for any
provider (`cmd/bronzewardctl/commands.go:111`); the unrotated refresh token is dropped later
(`internal/client/credentials.go:295-299`). The sentence that the file may be a "refresh-only one" is
read as "a document with only a refresh token is the CLI's own" (`internal/client/credentials.go:97-99`,
`internal/client/credentials.go:172-175`).

### 9.3 Browser sign-in

Built in the web client, the only client this section names, except the parts marked **Proposed** in
the paragraph "Built, by item" at the end of the section and in the paragraph "**Proposed**, where
today differs" after it. Provider behaviour (the provider answers
a redirect from its own session, rotation, the server's `displayClaim`) and the rejected options are
not claims about client code.

1. The web interface is a public client using the authorization-code grant with PKCE `S256`,
   `state` and `nonce`, with a full-page redirect to the provider. The PKCE verifier, `state`,
   `nonce` and the page path to return to, and one boolean when the sign-in leaves out `profile`
   (item 6), are held in one `sessionStorage` entry only between the
   redirect and the callback, stored only when the redirect follows, and are deleted when the
   callback is handled; this is the one storage
   use the web client makes, and it holds no token. The return path starts with `/` and not `//`,
   holds no backslash or control character, is never a full URL and is never the callback or
   signed-out page; any other is replaced by `/` (decision B2 of
   [#255](https://github.com/ginsys/bronzeward/issues/255)).
2. Access and refresh tokens are held in memory only, in a module the rest of the page cannot
   enumerate. They are never written to `localStorage`, `sessionStorage`, IndexedDB, the Cache
   API, a cookie or a service worker.
3. Under decision D5 (a), the access token is renewed silently with the rotating refresh token
   ([§9.5](#95-renewal)). A rotated refresh token is used once, so one tab renews for all
   (decision B1 of [#255](https://github.com/ginsys/bronzeward/issues/255)). Every signed-in tab
   requests the exclusive Web Lock `bronzeward-renewer` and holds it until `pagehide`, when
   [§10.3](#103-caches-and-back-navigation) item 3 clears its state (assumption S9). Only the
   holder sends refresh-token requests; before each it posts `renewing`, naming the refresh token
   it sends, on the `BroadcastChannel` (assumption S3), and after it the new pair.
   - The holder sends only once no other tab can send the same token
     ([#388](https://github.com/ginsys/bronzeward/issues/388)). Each tab holds an exclusive Web Lock
     `bronzeward-tab:<random>` and opens its channel only once that lock is granted. After posting
     `renewing`, the holder lists the held `bronzeward-tab:` locks other than its own with
     `navigator.locks.query()` and waits, at most 3 s, for an `ack` naming the token from each; a
     tab acks once it has recorded the token as spent. The holder then takes, with `steal`, the
     lock of every tab that did not ack, and sends only if its own tab lock is still its own. A tab
     can hold the token only from a message posted after its channel opened, so after its tab lock
     was granted and before the holder's query: each such tab has acked or lost its lock.
   - A tab whose tab lock was taken (frozen, suspended, or silent for 3 s) may have missed a
     `renewing`. It gives up its refresh token, takes a new tab lock, opens a new channel once that
     lock is granted, and posts a request. Its old channel still delivers what was posted to it
     before, of which the tab takes only a `signed-out` and the tokens a `renewing` names, never a
     pair: the holder may have sent a pair's refresh token without waiting for this tab, before its
     `renewing` naming it arrives. Until the new channel opens, its own `signed-out` and
     `redirecting` go out on the old one. The new channel hears everything posted after it opened;
     3 s later the tab closes its earlier channels, for what other tabs posted to them before has
     normally arrived by then ([#406](https://github.com/ginsys/bronzeward/issues/406)). It then
     takes the holder's current pair, even one of the same expiry as its own, or, granted the
     renewer lock first, renews by the redirect. A holder whose own tab lock was taken before it
     sent sends nothing, and does the same. A tab that stops for good (`pagehide`, sign-out, the
     signed-out page) gives up its tab lock and is not waited for. A tab's own sign-in redirect
     needs nothing: no other tab holds its pair until it posts it. A page restored from the
     back-forward cache reloads (§10.3 item 4).
   - A refresh-token request unanswered after 15 s fails the renewal (§9.5 item 3), so that a
     request the provider never answers holds back no later renewal and no pair for a new tab. Its
     answer, whenever it comes, is not kept: the refresh token it brings, which no tab holds, is
     revoked, and the one sent stays held for sign-out's revocation. A sign-out waits for that answer
     within its 5 s bound (§9.7 item 2), also when the request times out during that wait, so a
     rotated refresh token that
     arrives in time is revoked before the page navigates
     ([#396](https://github.com/ginsys/bronzeward/issues/396)). The request is not aborted,
     since an abort cannot recall a request the provider may already have answered.
   - A tab without the lock never renews: under the 60 s threshold or after a `401` (§9.5 item 1)
     it posts a request and waits for the holder's pair, at most 10 s, then treats renewal as
     failed (§9.5 item 3). Granted the lock while it waits, it renews itself. A holder about to
     renew by redirect posts `redirecting` first. A tab that received it in the last 10 s and is
     granted the lock, while it asks or before its own threshold, waits out the rest of those 10 s
     for that redirect's pair and redirects itself only if none comes, so that one renewal is one
     redirect, not one per tab. `redirecting` names the expiry of the pair the holder leaves with.
     The first pair newer than that ends the wait, and the 10 s with it, so that the next renewal
     of that pair does not wait for a redirect already over; while the 10 s run, the newest such
     expiry received counts, since another sender's older `redirecting` can arrive after it. A
     `redirecting` that reaches a tab already holding a newer pair, late or repeated, starts no wait
     ([#400](https://github.com/ginsys/bronzeward/issues/400)).
   - A holder whose renewal answered a request due first has a new pair, and its own renewal
     timer waits for that pair's threshold instead of renewing again.
   - A refresh token named in a `renewing` is spent in every other tab, whatever the answer: a tab
     never sends it, nor gives its pair to a new tab. A tab that holds it and takes no newer pair,
     or becomes holder before one comes, has failed to renew: the current token is used until
     refused, then the page offers sign-in again.
   - A new tab, a reload, or a page restored from the back-forward cache posts a request. If
     `navigator.locks.query()` shows no holder it is a first tab and redirects at once; otherwise
     it requests the lock and waits 5 s for the pair, then redirects. A holder whose renewal is in
     flight answers with the pair that renewal brings, never the one it is renewing, which has no
     refresh token until then. Granted the lock before a
     pair arrives, no tab that asked before it is left, so it redirects at once. The redirect is
     top-level; the provider answers at once from its own session, without a prompt while that
     session lasts.
   - A pair obtained by a redirect is posted to every tab by the tab that obtained it. A tab takes
     a received pair when it is newer than its own; a pair with another `sub` or `iss` is a new
     sign-in (§9.5 item 2).
   - Pairs are ordered by access-token expiry. If the provider shortens the token lifetime between
     two pairs, a tab can keep the older pair, whose refresh token another tab has sent. Its
     renewal then fails as §9.5 item 3 and the page offers sign-in again. The spent token is told
     by its value, not by expiry, so it is not sent twice.
   - Limits. A silent tab delays one renewal by 3 s; after that its lock is taken and it is no
     longer waited for. A holder that is itself frozen still holds `bronzeward-renewer`: the other tabs' requests go
     unanswered and fail after 10 s (§9.5 item 3) until it resumes or closes. This predates #388
     and is tracked there; taking the renewer lock from a holder is not part of decision B1.
     Lock grants and channel messages are separate task sources, so a tab can be granted the lock
     before the previous holder's `redirecting` reaches it, although that holder posted it before
     navigating and releases the lock only when its page unloads. That tab then redirects too: one
     extra redirect, which the provider answers from its session, and no refresh token sent
     twice. Having every tab acknowledge `redirecting`, as it does `renewing`, would close this at
     up to 3 s added to every redirect renewal; that is not done.
     A channel delivers one sender's messages in order, but nothing orders different senders', so
     no message can show that another tab's earlier posts have reached a rejoined tab's earlier
     channel. One still in transit 3 s after the new channel opened is lost when that channel
     closes. A lost `signed-out` leaves that tab signed in, showing its data, until it is signed
     out itself or a renewal fails; a lost `renewing` names a refresh token the tab gave up on
     rejoining. Keeping every earlier channel open
     until the page is released would close this at one more open channel per rejoin; that is not
     done ([#406](https://github.com/ginsys/bronzeward/issues/406)).

   Rejected: a lock taken per renewal, because a waiting tab can be granted it before the previous
   holder's pair reaches it and then spend the used token; a counter in `localStorage`, which
   item 1 excludes. For #388, also rejected: a fixed delay before sending, which no length makes
   safe against a slow or frozen tab; a hash of the token in `localStorage`, which item 1
   excludes; handing the token over on `pagehide`, which a frozen or crashed tab never fires; and
   giving up the tab lock on the `freeze` event, which only Chromium fires of the browsers in
   [§15](#15-assumptions-and-limits), whereas a steal needs nothing from the frozen tab.

   Where the provider does not issue rotating, client-bound refresh tokens
   ([§9.1](#91-identity-provider-requirements) item 3), renewal also uses that redirect, never a
   hidden frame (item 4). The page renews by the redirect when its pair, from a sign-in or another
   tab, has no refresh token, or when a renewal brings back the refresh token it sent; only the
   lock holder redirects, at the 60 s threshold, and another tab asks it as for any renewal, or
   redirects itself if granted the lock while it asks. A
   failed renewal or a spent token is not this case: renewal has failed (§9.5 item 3), and that
   pair goes to no new tab. A `401` is not either: the page shows W1 and does not redirect, since a
   token the server refuses would come back refused and the page would loop
   ([§9.6](#96-failure-cases)); W1's button is the operator's redirect. That fallback redirects at every
   access-token expiry, the behaviour the
   owner's constraint in D5 refuses, so the constraint holds only with a rotating provider:
   rotation is a provider requirement ([§15](#15-assumptions-and-limits)). No token is kept in
   storage (item 2).

   A renewal, a rescheduled renewal or a redirect still pending when the page is refused
   ([§9.6](#96-failure-cases)), sees another account (§9.5 item 2) or ends
   ([§9.7](#97-sign-out-and-cache-clearing), [§10.3](#103-caches-and-back-navigation) item 3) does
   not go on; only the operator's sign-in button leaves a refused or changed page. A refresh-token
   request already sent still gets its answer. After an end or another account, the pair it brings
   is revoked (§9.7 item 2); if it fails after the page has dropped its tokens, the refresh token it
   sent is revoked, not kept. After a refusal, that pair still goes to the other tabs, whose refresh token the request
   spent, and the refused page sends nothing with it.
4. Silent renewal through a hidden frame is not used: browsers that block third-party cookies make
   it fail unpredictably (assumption S2), and a failure would surface as an unexplained sign-out.
5. Every API call goes to the page's own origin ([§10.1](#101-hosting-and-origin)); only the
   provider's authorization, token, revocation, end-session and (item 6) userinfo endpoints are
   called elsewhere.
6. The header says who is signed in by name, which `GET /identity` does not serve and the access
   token does not carry on every provider. The page adds `profile` to the scopes it requests,
   once, when the provider's discovery document lists it in `scopes_supported` (never otherwise,
   so a provider that does not know it is not refused), then makes one `GET` on the provider's
   `userinfo_endpoint` per sign-in, with the access token, no cookies, no redirect and a 5 s
   bound. It reads only the `name` member, else `preferred_username`, and only from an answer
   whose `sub` is the signed-in subject, and only when the endpoint has the issuer's origin. The
   name is for display: it is never sent to the server, stored or used to decide anything. Any
   failure leaves the header at "Signed in as a person"; it never shows the subject number and
   never holds the page back. A provider that lists `profile` and then refuses it to this client
   answers the sign-in with `invalid_scope`. The sign-in view then shows that refusal and its button
   signs in again without the scope the page added itself (item 3 leaves a refused page only by the
   operator's button). That sign-in's pending entry (item 1) carries one boolean saying so. The page
   reads it before the callback consumes the entry, so the callback, a callback that fails, and
   every renewal redirect after it go on without `profile`.
   Limit: a new sign-in on such a provider, and a new tab that joins another tab's tokens (it
   discovers the provider afresh), still meets one refusal on its first sign-in or redirect
   renewal; the header then has a name only if the provider's userinfo gives one without `profile`. The server's `auth.oidc.displayClaim` names the claim the server shows for a
   person in its own records; the page does not follow it (limit: a provider whose userinfo `name`
   differs from that claim shows the userinfo name in the header). An automation shows "Signed in as
   the automation <service>". The header shows no epoch: the recovery banner names it where it
   matters.

**Built, by item.** Item 1: the grant, PKCE, `state`, `nonce`, the full-page redirect and the callback
checks, `web/src/auth.ts:113-129`, `web/src/auth.ts:157-183`, `web/src/app.tsx:104`; the pending entry
in `sessionStorage`, `web/src/auth.ts:113-116`, `web/src/auth.ts:157-158`, `web/src/app.tsx:192`, the
only storage use in `web/src` outside tests (`web/src/app.tsx:192`,
`web/src/app.tsx:214-215`), found by a search for `sessionStorage`, `localStorage`, `indexedDB`,
`caches.`, `serviceWorker` and `document.cookie`; the return path, `web/src/auth.ts:95-99`. Item 2:
`web/src/auth.ts:206-207`, and the pairs that travel on a `BroadcastChannel`, not in storage,
`web/src/renewer.ts:104`. Item 3: silent renewal, `web/src/auth.ts:302-375`; the holder and the tab
locks, the acknowledgements and the steal, `web/src/renewer.ts:13-21`, `web/src/renewer.ts:102-118`,
`web/src/renewer.ts:133-172`, `web/src/renewer.ts:186-354`, `web/src/auth.ts:255-296`,
`web/src/auth.ts:321-347`; the 15 s bound and the late answer, `web/src/auth.ts:16`,
`web/src/auth.ts:324-355`; the sign-out wait of 5 s, `web/src/auth.ts:14`, `web/src/auth.ts:409-418`; no
abort signal, `web/src/auth.ts:445-461`; the redirect and its thresholds, `web/src/page.ts:22-91`,
`web/src/app.tsx:104-131`, `web/src/app.tsx:208-219`; the page restored from the back-forward cache,
`web/src/app.tsx:89-96`; a pair taken when newer and another account as a new sign-in,
`web/src/auth.ts:268-278`, `web/src/app.tsx:57-61`; the failed renewal and the `401`,
`web/src/auth.ts:285`, `web/src/auth.ts:346`, `web/src/app.tsx:144-170`; the limits,
`web/src/renewer.ts:155`, `web/src/renewer.ts:215`, `web/src/renewer.ts:289-292`; no rotation,
`web/src/auth.ts:232`, `web/src/auth.ts:276`, `web/src/auth.ts:370-371`. Item 4: neither `iframe` nor
`prompt=none` appears in `web/src` outside tests. Item 5: `web/src/request.ts:4`,
`web/src/request.ts:356`, with the other fetches at `web/src/auth.ts:430`, `web/src/auth.ts:448`,
`web/src/userinfo.ts:31` and `web/src/config.ts:30`. **Proposed** for item 5's list of provider
endpoints: it adds the discovery document, which the page fetches (`web/src/auth.ts:60`) and the
sentence does not list. Item 6: `profile`,
`web/src/auth.ts:84-86`, `web/src/auth.ts:123`; the `userinfo` read and the name,
`web/src/userinfo.ts:11-48`, `web/src/app.tsx:164`, `web/src/shell.tsx:363-368`,
`web/src/shell.tsx:534`; `invalid_scope`, `web/src/auth.ts:107`, `web/src/auth.ts:113`,
`web/src/auth.ts:136-146`, `web/src/app.tsx:211-217`; the automation line and no epoch,
`web/src/userinfo.ts:46`, `web/src/shell.tsx:530-536`.

**Proposed**, where today differs: after an end or another account, "the pair it brings is revoked".
The web client does so after an end and when another tab's pair makes the page change account
(`web/src/auth.ts:338-355`), but a renewal that itself answers another account is adopted and then
dropped without revoking its pair (`web/src/auth.ts:366-381`, `web/src/app.tsx:114-120`).

### 9.4 Automation token input

Facts: an automation token is `bwt_<token id>.<secret>`, printed once by the server-side command
(`internal/auth/tokens.go:21`, `internal/auth/tokens.go:306-322`; [persistence and API §10.2](persistence-api.md#102-automation-bronzeward-issued-tokens)),
valid at most 90 days (`internal/auth/tokens.go:25`) and refused when revoked, expired or from
an earlier epoch (`internal/auth/automation.go:47-52`).

Built, except the parts marked **Proposed** below. Sources follow each item.

1. The CLI reads an automation token from a file (`--token-file` or the context's
   `automation.tokenFile`, or, in an automation context, a path in `BRONZEWARD_TOKEN_FILE`) or
   from standard input
   (`--token-stdin`). It never accepts the token as an argument value, nor the token itself in an
   environment variable (decision D6 (a)): `BRONZEWARD_TOKEN_FILE` holds a path, never a secret.
   (Built: `internal/client/token.go:23-40`, `cmd/bronzewardctl/main.go:339-344`; no token flag and
   no other `BRONZEWARD_` variable in `cmd/bronzewardctl` or `internal/client` outside tests, whose
   reads are `BRONZEWARD_TOKEN_FILE` and `BRONZEWARD_CONTEXT`.)
2. A token file must be a regular file readable only by its owner; otherwise the CLI refuses it
   and says why. (Built: `internal/client/token.go:54-70`; the file's owner is not checked.)
3. The CLI recognises an automation context by the `bwt_` prefix and says so before sending a
   request to a human-only route; the server's `403` stays authoritative. (**Proposed**: today the
   CLI knows an automation context from `credential: automation`, `internal/client/client.go:84`,
   `internal/client/config.go:174`, and checks the prefix only when it reads the token,
   `internal/client/token.go:46`. It says so before a write marked human-only,
   `internal/client/write.go:222-224`, but not before a human-only read such as `import review`,
   `cmd/bronzewardctl/review.go:28`.)
4. The web interface accepts no automation token. (Built: `bwt_` appears nowhere in `web/src` outside tests, so the
   page holds only a token its own sign-in or another tab's sign-in produced
   (`web/src/app.tsx:208-211`); the one place that names automation tokens is
   the human-only refusal text, `web/src/states.tsx:22`, which has no prefix.)
5. After a restore, automation tokens from the earlier epoch answer `401`; the CLI's message says
   the token must be reissued by the operator with the server-side command
   ([persistence and API §12.3](persistence-api.md#123-what-a-restored-state-means-record-by-record)).
   (Built: `internal/client/problem.go:42`; the `401` itself is a server fact.)

### 9.5 Renewal

Built in the clients each item names, except the part marked **Proposed**.

1. A client renews an access token when less than 60 s of its lifetime remains, by its own clock,
   and once after a `401` on a token it believed valid. It never retries a `401` more than once
   per request. (Built: CLI `internal/client/credentials.go:33`, `internal/client/credentials.go:255-258`,
   `internal/client/client.go:86`, `internal/client/client.go:236-243`, `internal/client/write.go:420-421`,
   `internal/client/watch.go:436`, `internal/client/watch.go:477`; web `web/src/auth.ts:12`,
   `web/src/auth.ts:20-23`, `web/src/page.ts:88`, `web/src/request.ts:190`,
   `web/src/request.ts:231-238`, `web/src/request.ts:383-417`, `web/src/write.ts:366`,
   `web/src/write.ts:444`. The web client renews after a delay of `max(left - 60 s, left / 2)`, where `left` is the
   remaining lifetime: 60 s before expiry when `left` is 120 s or more, and halfway to expiry when it is shorter.)
2. A renewal that returns a different `sub` or `iss` is a new sign-in: pending writes are set
   aside ([§8.4](#84-idempotency-and-ambiguous-responses) item 3) and the change is shown. (Built:
   CLI `internal/client/credentials.go:308-312`, `internal/client/write.go:319-323`; web
   `web/src/app.tsx:114-120`, `web/src/auth.ts:361-373`, `web/src/write.ts:302`.)
3. A failed renewal (refresh token rejected or expired, provider unreachable) leaves the current
   token in use until the server refuses it, then asks the operator to sign in again. The CLI in
   `--no-input` mode exits with the authentication-refused message instead of prompting. (Built:
   CLI `internal/client/client.go:92-96`, `internal/client/client.go:244-249`,
   `internal/client/credentials.go:501`, `internal/client/credentials.go:511`; web
   `web/src/auth.ts:338-347`, `web/src/app.tsx:165-170`. **Proposed:** the CLI's `--no-input`
   sentence: the spec names no "authentication-refused message", and the CLI never prompts on a
   failed renewal; with no store `--no-input` refuses at `internal/client/credentials.go:243-244` and
   a refused `401` exits 1 at `cmd/bronzewardctl/main.go:269-270`.)
4. Long waits (`operation watch`, `publish` waiting for its operation) renew in-process. (Built:
   `internal/client/watch.go:158`, `internal/client/watch.go:310-319`,
   `cmd/bronzewardctl/publish.go:100`.)

### 9.6 Failure cases

The server answers every token it does not accept with the same `401` and no detail
(`internal/api/api.go:314-322`, `internal/api/problem.go:76-79`). The client may add what it can
read locally, labelled as its own reading, never as the server's reason. The web interface shows
W1's text, "Not accepted by this installation. Sign in again.", for every `401 unauthenticated`
that its one renewal ([§9.5](#95-renewal) item 1) does not clear, with its local reading below it;
a `401` with another body follows [§8.2](#82-errors)'s rows. The texts below that name a context,
and the expired-token text, are the CLI's
(lead decision on consistency, 26 October 2026, under the owner's 2026-10-07 grant).

| Case | What the server does | Client behaviour |
| --- | --- | --- |
| Token expired | `401` (`internal/auth/auth.go:165-166`) | renew ([§9.5](#95-renewal)); if the provider refuses, the CLI says "Your sign-in expired: sign in again" |
| Wrong issuer | `401` | if the token's `iss` differs from `oidc.issuer`: "The token is from `<iss>`; this context expects `<issuer>`. Check the context or the server's configuration." |
| Wrong audience | `401` | if `aud` lacks `oidc.audience`: "The token is not issued for `<audience>`; the identity provider needs an audience mapping" ([§9.1](#91-identity-provider-requirements) item 1) |
| Lifetime over the maximum | `401` (`internal/auth/auth.go:175-176`); the maximum is the deployment's `maxTokenLifetime`, 15 minutes unless configured (`internal/config/config.go:125`, `internal/config/config.go:359-360`) | `GET /server` serves the maximum as `maxTokenLifetimeSeconds` since [#278](https://github.com/ginsys/bronzeward/issues/278), but only to an accepted token, so the client pre-checks against the value the context last read; with none read yet it reports the `401` as any other. If `exp - iat` exceeds it: "The token lives longer than this server accepts; the identity provider's access-token lifetime must be shortened" |
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
| Provider unreachable from the client | no request reaches the server | keep using the current token until it is refused; then "Cannot reach the identity provider `<issuer>`", with the cause by its type ([§6](#6-tls-trust) item 4) |

Built, row by row, except the parts marked **Proposed** after the list. The CLI's texts:
`internal/client/credentials.go:493-512`, `internal/client/problem.go:42-61`; the web client's local
reading and texts: `web/src/auth.ts:480-490`, `web/src/states.tsx:22-23`, `web/src/shell.tsx:104-106`.
Expired token, wrong issuer, wrong audience and a malformed groups claim: CLI
`internal/client/credentials.go:500-505`, `internal/client/credentials.go:511`, web `web/src/page.ts:88`,
`web/src/auth.ts:483-488`, `web/src/states.tsx:23`. Identity revoked: CLI `internal/client/problem.go:44-50`,
`internal/client/credentials.go:524-538`, web `web/src/app.tsx:19`, `web/src/app.tsx:157-160`,
`web/src/app.tsx:171-173`, `web/src/app.tsx:252-254`. Automation token not accepted: CLI
`internal/client/problem.go:42`. Account disabled: the failed renewal of [§9.5](#95-renewal) item 3.
The `503` row: CLI `internal/client/client.go:251-255`, `internal/client/client.go:337-340`, web
`web/src/request.ts:92-93`, `web/src/request.ts:250-253`. Group removed at the provider is a
provider fact, and the client does nothing before the next refusal.

**Proposed**, where today differs: (a) "Lifetime over the maximum": the CLI reads the last
`maxTokenLifetimeSeconds` only to form its reading after a `401` (`internal/client/client.go:152`,
`internal/client/client.go:381-382`, `internal/client/credentials.go:493-512`), not before it sends;
the web client has no such check (`maxTokenLifetime` appears nowhere in `web/src` outside tests,
whereas `localReading` is at `web/src/auth.ts:480`). (b) "An ID token, or no `jti`": the CLI reads it
(`internal/client/credentials.go:508-509`); the web client does not (`at_hash` and `jti` appear
nowhere in `web/src` outside tests). (c) "No role for the action": the web text is as the table
says (`web/src/shell.tsx:106`); the CLI says "Needs one of the roles [...]. You are signed in as `<sub>`."
(`internal/client/problem.go:56-60`). (d) The publisher cancelling a plan it did not create: the web
text is as the table says (`web/src/shell.tsx:104`); the CLI shows the detail in its head line and
"You are signed in as `<sub>`." after it on the same line (`internal/client/problem.go:33-35`,
`internal/client/problem.go:56-61`). (e) "Human-only route with an automation token": the CLI's text is
as the table says (`internal/client/problem.go:53`); the web client's is "This action needs a person
signed in; automation tokens cannot approve." (`web/src/states.tsx:22`). (f) "Provider unreachable
from the client": the CLI prints "cannot reach the identity provider `<issuer>`" with the cause, in
lower case, as a notice at the failed renewal rather than after the refusal
(`internal/client/credentials.go:393`, `internal/client/client.go:94`); the web client keeps the token
and has no such text (`web/src/auth.ts:449-451`). The server's `401`, `403` and `503` answers and
the group and account behaviour at the provider are server or provider facts.

### 9.7 Sign-out and cache clearing

Facts: the server keeps no session, so it cannot end one; an access token stays valid until its
expiry, at most `maxTokenLifetime` after issue, plus the server's 60 s leeway
(`internal/auth/auth.go:95`, `internal/auth/auth.go:165`); an identity revocation is permanent and is not a sign-out
(`internal/api/revocations.go:122`).

Built, except the page header of item 3, which stays **Proposed** (hosting and packaging, as
[§10.2](#102-script-and-content-policy)). Sources follow each item.

1. `bronzewardctl logout` deletes the context's tokens from the store and from its D2 (b) token
   file, if it has one, revokes the refresh token
   at the provider's revocation endpoint if it has one, and says that an access token already
   issued remains valid at the server for up to its remaining lifetime plus that leeway. The
   endpoint is the `revocation_endpoint` of the provider's discovery (RFC 7009, sent as a public
   client). The revocation request follows no redirect, so a 3xx answer is a refused revocation.
   A refusal shows the status and a standard error code only ([§6](#6-tls-trust) item 4).
   Without a `revocation_endpoint`, logout says the refresh token is not revoked; a refused or failed
   revocation still deletes the tokens, says the refresh token is not revoked and exits 1. (Built:
   `internal/client/login.go:422-450`, `internal/client/login.go:432-439`, `internal/client/store.go:180-215`,
   `internal/client/credentials.go:155-176`, `internal/client/credentials.go:411-449`,
   `internal/client/remote.go:39-45`, `cmd/bronzewardctl/commands.go:306-311`,
   `cmd/bronzewardctl/main.go:274`.)
2. The web sign-out discards the in-memory tokens and every cached response, revokes the last
   refresh token the tab held if the provider allows it, even one it no longer sends for renewal
   (after a failed or unrotated renewal, or one another tab may have spent), or else the refresh
   token a renewal in flight sent, at once and without waiting for that renewal, and then the pair
   it brings, and the pair brought by the late answer of a refresh request that timed out
   ([§9.3](#93-browser-sign-in) item 3, [#396](https://github.com/ginsys/bronzeward/issues/396)),
   tells other open tabs of the same origin to discard theirs
   (assumption S3), and redirects to the provider's end-session endpoint where it offers one, with
   `client_id` and `post_logout_redirect_uri` only. It sends no `id_token_hint`: the test
   provider's ID token carries the access token's issuer, audience and lifetime, so the server
   would accept it as a bearer token, and a URL keeps it in history and provider logs. Without the
   hint that provider sends the browser through its login UI's logout page. It then shows a
   signed-out page that holds no data. A tab told of another tab's sign-out hides what it shows at
   once and revokes the last refresh token it holds, as its own sign-out would, since the other
   tab may not have had it (a renewal answered just before its message). Either sign-out waits at
   most 5 s for those revocations, for a renewal in flight to revoke the pair it brings and for a
   timed-out request's answer to revoke its pair; the page then navigates, and an answer that comes
   later finds no page. A told tab then shows the signed-out page,
   which holds no lock and records no other tab's refresh token. A tab acts on one sign-out only:
   a second tab's, or one after its own, would cut that wait short
   ([#388](https://github.com/ginsys/bronzeward/issues/388)). (Built: `web/src/app.tsx:62-68`,
   `web/src/app.tsx:81-86`, `web/src/app.tsx:134-141`, `web/src/app.tsx:183-186`,
   `web/src/app.tsx:245-250`, `web/src/auth.ts:14`, `web/src/auth.ts:330-335`,
   `web/src/auth.ts:349-355`, `web/src/auth.ts:388-418`, `web/src/renewer.ts:73`,
   `web/src/renewer.ts:126-129`, `web/src/renewer.ts:165-172`, `web/src/renewer.ts:203-208`,
   `web/src/shell.tsx:275-278`, `web/src/shell.tsx:452`.)
3. After sign-out, the browser's back button shows no data from before: the web pages are served
   `Cache-Control: no-store`, the application clears its state on `pagehide` and on a page
   restored from the back-forward cache ([§10.3](#103-caches-and-back-navigation) items 3-4,
   assumption S8), and it renders nothing without a token. (Built: the application's part,
   `web/src/app.tsx:89-96` and `web/src/app.tsx:92`, `web/src/app.tsx:236-271`. **Proposed:** the
   header: `web/headers.json:6` declares `Cache-Control: no-store`, and only the test host
   `fixtures/webhost/main.go` applies it in this tree.)

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

The two header lists below stay **Proposed**: they bind the static host of D1 (a), which is
packaging, and this tree only ships their values (`web/headers.json:2-6`, rendered by
`scripts/release/web-package.sh:20`) and sends them from its test host
(`fixtures/webhost/main.go:116-118`). The rest of this section is built in the web client, with
sources after each paragraph. The static host sends, on every page:

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
any script running in the page can call the API as the operator. (Built in the web client:
`web/index.html:8` loads only `/src/main.tsx`; a search of `web/src` and `web/index.html` outside
tests for `https?://` finds a documentation link (`web/src/recovery.tsx:95`), the icon attribution comment
(`web/src/icons.tsx:1`) and URL-shape strings,
and a search for `sentry|analytics|telemetry|onerror|unhandledrejection|sendBeacon|EventSource|WebSocket`
finds nothing; the network calls are `web/src/request.ts:356`, `web/src/auth.ts:60` (discovery),
`web/src/auth.ts:430` (revocation), `web/src/auth.ts:448` (token), `web/src/config.ts:30` and
`web/src/userinfo.ts:31`. No `dangerouslySetInnerHTML`, `innerHTML`,
`outerHTML`, `insertAdjacentHTML` or `document.write` appears in `web/src` outside tests. There is no
syntax highlighting: `yaml` parses (`web/src/position.ts:7`, `web/src/marks.tsx:9`,
`web/src/onboard.ts:9`) and writes one document, the onboarding contexts file
(`web/src/onboard.ts:108`), and a diff is plain text (`web/src/app.css:134`).)

**CSRF.** The API authenticates only by the `Authorization` header and never by a cookie, so a
cross-site request carries no credential. This holds only while no cookie authentication is
added; any future cookie needs its own CSRF design. (The server's part is a server fact. Built in
both clients: the web client sends `credentials: "omit"`, `web/src/request.ts:362`,
`web/src/auth.ts:25`, `web/src/userinfo.ts:31`; the CLI's `http.Client` has no cookie jar,
`internal/client/transport.go:89-96`, and `Jar` and `CookieJar` appear nowhere in `internal/client`.)

### 10.3 Caches and back navigation

Built in the web client, except the page header of item 4 (packaging, as [§10.2](#102-script-and-content-policy)),
which stays **Proposed**; item 1's server half is a server fact. Sources follow each item.

1. Every API read from the web client uses `fetch` with `cache: 'no-store'`. The server sends
   `Cache-Control: no-store` on every API answer ([§2](#2-the-api-a-client-meets) caching row,
   served by A3 for G13), so a browser or an intermediary keeps nothing even from another client.
   (Built: `web/src/request.ts:356-363`, `cache: "no-store"` at `:360`, the one call for reads,
   writes and streams; `web/src/claim.tsx:141-142` and `:207-208` go through it.)
2. The application uses no service worker. (Built: `serviceWorker` appears nowhere in `web/src` or
   `web/index.html`.)
3. On `pagehide` and on sign-out, the application clears its in-memory state and every form field
   that holds a document or marks. (Built: `web/src/app.tsx:81-86`, `web/src/app.tsx:89-93`,
   `web/src/claim.tsx:116-122`.)
4. Pages are served `no-store`, so a page reloaded from history renders nothing until signed in
   again. A browser may still keep a `no-store` page in its back-forward cache (assumption S8);
   on a `pageshow` with `persisted` true the application discards any state and renders nothing
   until signed in again, so the guarantee rests on items 3 and 4, not on the header alone.
   (Built in the client: `web/src/app.tsx:94-96` reloads on a persisted `pageshow`, and the page
   renders nothing after `pagehide`, the `blank` view (`web/src/app.tsx:26-28`,
   `web/src/app.tsx:238-239`); the loading and signed-out views carry no data. **Proposed:** the header, see above.)

### 10.4 Styling and the content policy

Built in the web client, except the parts marked **Proposed** in the paragraphs after item 7.
The web client is styled with Oat (`@knadh/oat`, MIT, pinned to one exact version),
dark first with no light theme (D13). The bundle contains only the Oat files the client uses:
the base, theme, form, button, table and animation files, with the base file first because it
declares the cascade layers. Oat's rules are in layers and the client's own stylesheet is not, so
the client's rule wins wherever the two meet.

1. **Theme.** The page sets `color-scheme: dark` and a `color-scheme` meta element, and overrides
   Oat's colour tokens with plain colours. Every pair of text and background is at least 4.5:1 and
   every control border, table rule and focus ring at least 3:1, measured on the page background
   and on a card; the measured pairs are quoted with the change that sets them.
2. **Focus and disabled controls.** A focused control shows a solid outline of 3 px, offset 2 px,
   in the ring colour (Oat draws 2 px, and none on a text field). A disabled control keeps its full
   contrast (Oat halves its opacity); a disabled button shows it by a dashed border and a disabled
   field by a muted background.
3. **Links and buttons.** A link is underlined wherever it is, except those of the main navigation
   (item 6), which are not text and show what they are by an icon, their group and, for the current
   page, a bar and a bold label; and a button's label wraps at 320 CSS pixels. Hover colours are plain colours without transition, so a colour read at rest is three
   bytes; the contrast measure of the browser cases throws on any other form.
4. **Controls are native.** A select, check box and radio use the browser's own control. Oat draws
   their arrow, mark and dot as `data:` images, and `img-src 'self'` does not match a `data:` URL, so
   the policy of §10.2 is unchanged and no drawn mark is used: the client's stylesheet clears the
   select's background image and the mask image of a checked control's pseudo-element (the browser
   still requests the mask image when only the pseudo-element's content is cleared). A later view that needs an Oat
   component with a `data:` image gets its own decision and does not widen the policy.
5. **The header is not sticky**, so nothing covers a focused element (2.4.11): the top bar and the
   navigation have no fixed or sticky position, and the navigation column has a width only.
6. **The desktop shell** (ginsys/bronzeward#973, on the owner's feedback of 2026-11-20; designed and reviewed at 1280 and 1600 CSS pixels, with no design
   effort below that). A top bar holds the product name, the principal line, the banners and Sign
   out, in the order they already have, so Tab order after the skip link does not change. A left
   navigation, one `nav` landmark and no `aside`, groups the pages as Inventory (Machines, Clusters,
   Fragments and profiles), Delivery (Drafts and releases, Plans, Operations) and Governance (Audit, Recovery) and marks the
   current page by `aria-current`, a bar and a bold label, never by colour alone. The page sits in a
   column of at most 90 rem. Below 68 rem the navigation stacks above the page, which keeps the
   reflow of 1.4.10 at 320 CSS pixels. One spacing scale (`--s1` to `--s6`, 0.25 to 2 rem) is the only
   source of padding, gap and margin in the shell rules. A status line is an alert box, a bordered
   panel holds a page section, and a detail list is a two-column grid; the box is a class and changes
   no role, and every fill is an opaque colour so the contrast measure can read it. The filters and
   actions above a list sit in one wrapping row (the `toolbar` class), each action keeping its refusal
   note beside it, with the markup order unchanged. The web client's words for what a person meets
   ([#1055](https://github.com/ginsys/bronzeward/issues/1055); the specification states the words and sentences once, for both clients, and `bronzewardctl` prints the three machine state words and the sentences of operator experience §2.4, [#1071](https://github.com/ginsys/bronzeward/issues/1071); "Not adopted yet" is the console's word for a machine with no Desired and no Applied, which the command prints as `-` when no other state applies):
   "Drafts and releases" is the page that lists both, and `/drafts` and `/releases` open it; "Fragments and profiles"
   is the page the word "sources" named, and a release's count of what it is built from is headed "Built from"; the
   buttons read "Enter a cluster by hand" (it records a cluster Bronzeward did not create) and "Add machine"; a
   machine's state badges read "Changed on the machine", "Release not applied yet" (with "Differs in form only
   (comments and encoding)" beside it when the server says the release has the same documents, keys and values), "Last
   read matched what was applied" (the specification says a match never by itself means healthy, fresh or converged)
   and "Not adopted yet", with "Frozen" as before. An assignments row and an imports row are headed by what they are, "Assignment of
   cp1" and "Import of cp1", and a dependency by "Dependency <object>, version <n>", with the identifier as the link's title.
   Every titled page of the console shows one note under its title that says what the page is, what to do on it and where to
   go next (the plan page's approval part is drawn inside its Approval section; the signed-out page already says so in
   its own line, and the not-found page's note follows its own line), and a note says only what the page shows in its
   state: a part about Add, Publish, Discard or Adopt is shown only where that control is, the release's
   rendered configuration page shows its note only where the configuration is drawn, and a page that shows a record
   shows its note once the record is read. A note holds no identifier and no role word, uses the word its own page's
   control shows, and adds a link only where the page has none to that place. The one exception is the recovery page,
   whose note adds a sentence naming the recovery role to a person who lacks it.
7. **Icons are decorative inline SVG** (Heroicons 2.2.0 outline, MIT, the paths vendored in one module
   with the notice kept). An icon is an `<svg>` element with `aria-hidden="true"`, no title and no
   style attribute, drawn with presentation attributes, so it is markup and not an image load:
   `img-src 'self'` and `style-src 'self'` need no change, and `require-trusted-types-for 'script'` is
   met because icons are JSX elements and never HTML strings. An icon stands beside the word that
   carries the meaning, outside the elements whose text the status mirror and the browser cases read.

A browser case on the signed-out page fails if Oat's focus rule wins over the client's, if a
`data:` image is requested (it is checked with a positive control, a `data:` image that must be
reported), if a control is not native, or if a hover colour is not an opaque `rgb()`.

**Built, by item.** Oat pinned to one exact version, `web/package.json`; dark only, `web/src/app.css:13`
and no `prefers-color-scheme` rule; the bundle's six files in order, `web/src/main.tsx:4-9`; no `@layer`
in `web/src/app.css`. Item 1: `web/src/app.css:13-27`, `web/index.html:6`; the 4.5:1 text pairs are
measured by `measure()` in `web/e2e/auth/helpers.ts:372` and asserted at
`web/e2e/auth/recovery-accounting.e2e.ts:335`, `web/e2e/auth/publish.e2e.ts:177` and
`web/e2e/auth/operation.e2e.ts:332`. Item 2: `web/src/app.css:29`, `web/src/app.css:44`,
`web/src/app.css:48`, case `web/e2e/styling.e2e.ts:110-120` and `:170-176`. Item 3:
`web/src/app.css:45-47`, `web/src/app.css:86-88`, case `web/e2e/styling.e2e.ts:178-196`. Item 4:
`web/src/app.css:54-58`, case `web/e2e/styling.e2e.ts:122-146` (the positive control at `:137-145`).
Item 5: the only positions in `web/src/app.css` are `.skip` and `.sr-only` (`:31-33`); the
navigation column is a grid track with a width and no position (`:74`, `:77`). Item 6: the top bar, `web/src/shell.tsx:530-545`; the navigation and its
groups, `web/src/shell.tsx:548-557`, `web/src/router.ts:22-26`, `web/src/layout.test.ts:9-17`, with no
`<aside` in `web/src`; the column and the stacking, `web/src/app.css:89-95`; the alert box and the
detail grid, `web/src/states.tsx:85`, `web/src/app.css:70` and `:102`; the words,
`web/src/words.ts:92-104`, `web/src/machines.tsx:25-40`, `web/src/drift.tsx:64`; the row titles,
`web/src/titles.ts:29`, `:35`, `:58`; the page notes, `web/src/page-note.tsx`,
`web/src/page-notes.test.ts`; the CLI's state words and §2.4 sentences,
`cmd/bronzewardctl/reads.go:278-296` and the function `sentences`, `:355-425` (the sentences were not compared word for word
against operator experience §2.4). Item 7: `web/src/icons.tsx:1-49`, `web/src/layout.test.ts:21-29`,
and no `style=` in `web/src` outside tests.

**Proposed**, where today differs: (a) item 1: the 3:1 pairs for borders, rules and the focus ring
are quoted in the header comment of `web/src/app.css:7-10` and no case measures them (the only
related check is the ring colour at `web/e2e/styling.e2e.ts:115`). (b) Item 6: the spacing scale is
`web/src/app.css:75`, but `.alert .icon` takes an off-scale margin (`web/src/app.css:105`), so the
scale is not yet the only source in the shell rules. (c) Item 6: the bordered panel (`web/src/app.css:120`)
holds a section only on `web/src/operation.tsx:241` and `web/src/machines.tsx:334` and `:358`, and the
`toolbar` row (`web/src/app.css:111`) only on the machine and cluster lists
(`web/src/machines.tsx:237`, `:274`); the filters of the other lists are bare `<Filter>` elements
(`web/src/resources.tsx:221`, `:411`, `:514`, `:591`, `:592`). (d) Item 2: a disabled field's muted
background is Oat's own rule, relied on by the comment at `web/src/app.css:36-38` and not opened
here; Oat's layering was not opened either (Oat is not installed in this tree). (e) The note
sentence "holds no identifier and no role word" is asserted by `web/src/page-notes.test.ts`, which
this change did not run, and not every titled page was checked against `PageNote`. (f) A button wrap
at 320 CSS pixels (`web/src/app.css:47`) and the reflow below 68 rem (`web/src/app.css:91-95`) have no
browser case read here. The browser case's green run is a CI fact.

## 11. Protected input

### 11.1 What is protected

Plaintext can pass through a client in these places only:

| Input or output | Route | Why it is protected | Source |
| --- | --- | --- | --- |
| A fragment `document` | `PUT /drafts/{id}/fragments/{name}` | may hold values the extraction pipeline will move to the provider | keyed `document`, `internal/api/fragments.go:29-31`; [compilation §2](compilation.md#2-ingestion-extraction-before-persistence) |
| An ingestion `document` (`source: document`) | `POST /ingestions` | a full machine configuration with its secrets | keyed `document`, `internal/api/ingestions.go:24-26` |
| Marks | `POST /ingestions/{id}/marks`, `POST /ingestions` `marks`, `PUT /drafts/{id}/fragments/{name}` `marks` | a path can spell a secret mapping key | on the mark route `marks` is the keyed member (`internal/api/marks.go:67-68`); on the start route and the fragment write the keyed member is `document`, and `marks` stays in the canonical body that the same keyed fingerprint covers (`internal/api/ingestions.go:24-26`, `internal/api/fragments.go:37`, `internal/api/fragments.go:51-57`, `internal/api/body.go:170-172`); [compilation §3.6](compilation.md#36-operator-review) item 4 |
| The protected review | `GET /ingestions/{id}/review` | can hold a value nobody marked yet | `internal/api/review.go:24-31`; [compilation §3.6](compilation.md#36-operator-review) |
| The import base text | `GET /import-base-revisions/{id}/document` | the stored sanitized document, which can hold a value nobody marked | `internal/api/importbase_document.go`; [persistence and API choice §17.41](persistence-api.md#17-choices-for-owner-review) |
| Tokens | every request | credentials | [§9](#9-authentication) |
| The talosconfig file of `cluster discover` | none: it is read from a file the operator names and goes to Talos nodes only | holds the cluster's client key | [§3.3](#33-client-actions-with-no-route) |
| A first key file of `cluster first-key` or `cluster onboard` | `POST /clusters/{id}/first-keys`: it is read from a file the operator names and sent once | may hold the cluster's CA key | [§3.2](#32-writes), [persistence and API §3.4](persistence-api.md#34-first-keys-and-the-talos-signer) |
| The talosconfig chosen in the web client's Connect a cluster page | `POST /discoveries` and `POST /clusters/{id}/first-keys`, as its cut to one context (`discover.OneContext`'s document) and nothing else | holds the cluster's client key | [operator experience §6](operator-experience.md#6-web-interface) |

Not protected: rendered release reviews, plan diffs and provenance are redacted by the server
([compilation §8.3](compilation.md#83-redaction)) and are handled as ordinary reads.

**Decided: a fragment revision stays readable by every role, with the limit recorded.** `GET /fragment-revisions/{id}` is read by every role and answers a
fragment's sanitized text, which can hold a value nobody marked. The route is not in the table above:
both clients handle it as an ordinary read under the accepted limit that
[persistence and API §9.1](persistence-api.md#91-conventions) states with the owner's decision
(ginsys/bronzeward#974); the web view's display rules are in
[operator experience §9](operator-experience.md#9-secret-handling-in-the-clients) item 9.

The review is served to any human `author`, not only the one who started the ingestion
(`internal/api/routes.go:99`), as the contract intends: "only to the roles that may mark"
([compilation §3.6](compilation.md#36-operator-review)). A client does not narrow this.
The import base text of an import read from a node is served to any `author` too (the
author role may read a node's configuration: choice §17.41). The text of a file import is
stricter: the server answers it only to the author who made the import and continued its
review, and a client offers the action to any `author` and shows the refusal.

### 11.2 Rules for both clients

Built in the clients each item names, except the parts marked **Proposed** inline in an item and in
the paragraph after item 7. Sources follow each item.

1. **Argv and history.** No flag takes a protected value. Documents come from a file, standard
   input or a prompt; marks only from a prompt or standard input ([§11.3](#113-cli-review) item
   3); tokens from [§9.4](#94-automation-token-input). The documentation
   says that a shell here-document is saved in the shell's history like any command, so a document
   goes in a file. (Built, CLI: `--file` takes a path, `cmd/bronzewardctl/imports.go:137`,
   `cmd/bronzewardctl/fragments.go:55`; `--marks` is a switch, `cmd/bronzewardctl/imports.go:146`,
   `cmd/bronzewardctl/fragments.go:57`; marks are read from standard input or a terminal only,
   `cmd/bronzewardctl/fragments.go:235-254`; the other file flags are paths,
   `cmd/bronzewardctl/onboard.go:368`, `cmd/bronzewardctl/firstkey.go:52`.)
2. **Logs and diagnostics.** Debug output (`-v`) prints method, path, status, `instance` and
   timing. It never prints `Authorization`, a request body of a keyed route, the review body, or
   marks. Error output repeats the server's problem document, which redacts a message by template and searches it for exact copies of resolved string values of
   six bytes or more; a shorter copy, or a copy of an integer or boolean, inside an authored literal is not detected, and
   a path or message may quote that literal
   ([persistence and API §9.4](persistence-api.md#94-errors), compilation §8.4). It never repeats the request.
   (Built for the last sentence in both clients: CLI `internal/client/problem.go:26-33`, web
   `web/src/states.tsx:152-164`. The `-v` clause is **Proposed**, see below.)
3. **No telemetry** in either client, and no crash reporter that sends memory or arguments.
   (Built: CLI, no `sentry|telemetry|analytics|crashreport|bugsnag` in `cmd` or `internal/client`
   outside tests, panic handler `cmd/bronzewardctl/main.go:83-88`; web, [§10.2](#102-script-and-content-policy).)
4. **No temporary files and no editor.** A client never writes protected input or the review to a
   temporary file and never opens `$EDITOR` on it. A document the operator edits in their own
   editor is the operator's file; the documentation says that editors keep swap and backup files
   the client cannot control. (Built for the first sentence: a search of `cmd/bronzewardctl` and
   `internal/client` outside tests for `CreateTemp|MkdirTemp|TempDir|EDITOR|VISUAL|exec.Command`
   finds `xdg-open` with a URL only (`internal/client/login.go:297`), the `security` tool on macOS,
   which takes the secret on its standard input (`internal/client/store_security.go:27-28`), a temporary file for the
   context file (`internal/client/config.go:126`) and one for the token file
   (`internal/client/credentials.go:138`, written and renamed: tokens are protected input in the table
   above, so this part is **Proposed**, see (e) below); the web client has no `Blob`,
   `createObjectURL` or save picker. The documentation sentence is **Proposed**.)
5. **Core dumps.** The CLI disables core dumps for its own process before it reads protected input
   (on Linux, `RLIMIT_CORE` 0 and not dumpable). A panic handler prints a fixed message and no
   stack values. (Built: `cmd/bronzewardctl/main.go:79`, `cmd/bronzewardctl/main.go:83-88`,
   `cmd/bronzewardctl/main.go:115-120`, `cmd/bronzewardctl/main.go:126`,
   `cmd/bronzewardctl/coredump_linux.go:10-20`; the commands that read an import fragment, marks, the
   review or the import base are refused off Linux, `cmd/bronzewardctl/fragments.go:74`, `:93`,
   `cmd/bronzewardctl/review.go:38`, `:254`, `cmd/bronzewardctl/imports.go:166`,
   `cmd/bronzewardctl/importbase.go:36`. **Proposed:** the first-key document
   (`cmd/bronzewardctl/firstkey.go:27`) and the automation token (`internal/client/token.go:23`)
   are protected input too and are not refused off Linux.)
6. **Memory.** Protected input is held no longer than the request needs. A write keeps its body
   only while it can be repeated. When it can no longer be repeated, the page releases the body
   and anything its owner holds for it, and offers no repeat of it, also under a later sign-in of
   the same principal and epoch. The page releases it when the operator dismisses the write, when
   an epoch change or another sign-in sets it aside, when the sign-in it was sent under ends
   (sign-out, refusal or revocation, also after the page disconnected the client), and when the
   page is hidden. A garbage-collected runtime cannot guarantee erasure; this is a stated limit,
   not a guarantee. (Built, web: `web/src/write.ts:188-193`, `:206-214`, `:282`, `:301`, `:304-324`,
   `web/src/app.tsx:81-93`. CLI: the review and the import base body are cleared after decoding,
   `cmd/bronzewardctl/review.go:71` and `cmd/bronzewardctl/importbase.go:69`; other request bodies
   live until the process ends, so only that part of the item is read as built; the rest is
   **Proposed** (d) below.)
7. **Clipboard and export.** No copy, download, save, print or share action on the review, on a
   document being submitted, or on marks. (Built: web, `CopyButton` is used only for identifiers and
   problem references, `web/src/list.tsx:248`, `web/src/problems.tsx:184`; CLI,
   `cmd/bronzewardctl/review.go:44-51` refuses `-o` and a non-terminal and no save or export flag
   exists.)

**Proposed**, where today differs: (a) item 1: no operator documentation says that a shell
here-document is saved in history (a search for `here-document`, `heredoc` and `shell history` in
`docs/operator`, `README.md`, the command table `cmd/bronzewardctl/commandtable.go` and
`internal/client` finds none). (b) Item 2: `-v` is documented as naming a proxy a request goes
through (`cmd/bronzewardctl/commandtable.go:291`) and its only writer prints that proxy line
(`internal/client/transport.go:48-49`), so no method, path, status, `instance` or timing is printed;
the clause that it never prints `Authorization`, a body or marks therefore holds without effect.
(c) Item 4: no documentation says that editors keep swap and backup files (a search for `swap` and
`backup file` in `docs/operator` and `cmd` finds none). (d) Item 6 for the CLI: only the review and
the import base body are cleared after decoding; the built note on the item records that other request
bodies live until the process ends, which the clause "held no longer than the request needs" does not allow.
(e) Item 4 for the CLI: the token file is written through a temporary file in its own directory
(`internal/client/credentials.go:138-143`) and tokens are protected input in section 11.1, so the clause
"never writes protected input to a temporary file" is not met for tokens.

### 11.3 CLI review

Built in the CLI (the web interface's part is the last sentence of item 3 and
[§11.4](#114-web-review-and-document-input)), with the sources in the paragraph after item 3, except the
parts marked **Proposed** in the paragraph after those sources. This
qualifies the journeys' candidate C4 against [compilation §3.6](compilation.md#36-operator-review):

1. `import review` shows the review only when standard output is a terminal, through an
   in-process pager that writes nothing to disk. It is refused, with no override, when standard
   output is not a terminal, and it has no `-o json`, `-o yaml`, save or export form. While it
   shows the review it takes SIGINT, SIGTERM and SIGHUP from before the terminal goes raw until
   the terminal is restored. On one it leaves the alternate screen, restores line mode, writes
   nothing more, starts no marks prompt, and ends the process by that signal. A signal the
   process inherited as ignored (`nohup`) stays ignored and is not taken. This holds when the
   terminal has stopped taking output and a write is waiting: the pager stops waiting for that
   write on the signal, restores line mode, tries once, for a fraction of a second, to leave the
   alternate screen, and ends the process by the signal. If the pager ends by its quit key and
   the terminal does not take the bytes that leave the alternate screen in that time, the
   command fails with an error and starts no marks prompt. On a resize
   (SIGWINCH) it reads the new size: with the same width the page stays where it is, and with
   another width the text is wrapped again and the page starts at the same source line. A size
   below the smallest the pager takes shows a "Window too small" notice instead of the page,
   ignores every key but quit, and shows the page again once the size is usable. A nonspacing
   or enclosing combining mark takes no column. A spacing mark takes one, or two when it is
   wide or full-width, such as U+302E. A wrap keeps
   either with the character before it (the marks beyond what a row holds, up to the next character that is not a mark, show as their Go escape);
   full grapheme clusters, such as emoji joined by a
   zero-width joiner, are not kept whole.
2. Before showing it, the CLI states that terminal scrollback, session recording and screen
   capture are outside its control.
3. Marks come from the prompt or standard input. At the prompt a line ends at a line feed, a
   carriage return or both, because Enter typed ahead while the pager had the terminal raw waits
   in the queue as a carriage return; from standard input a line ends at a line feed, or a
   carriage return and a line feed, and a lone carriage return stays in the mark. A refused mark is reported by its
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
   outcome is reported as unknown, not as not applied. The client acts on that read in the same
   turn. The read must have begun after the margin passed, so the bound counts from the delivery
   of that read. While the stream is open, the watch ends within one keep-alive interval (15 s,
   section 8.6 item 5) after that delivery, plus the time of the reads it makes in that turn.
   On the JSON fallback the turns come at the poll delay, which is 30 s at most when pages stay
   empty. The bound is then that delay and not 15 s. The run's
   closing transaction checks the
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

**Built, by clause.** Item 1: the terminal checks and the refusal of `-o` and a non-terminal,
`cmd/bronzewardctl/review.go:44-52`; the pager writes only to its writer, `cmd/bronzewardctl/pager.go`;
the signals are taken before the terminal goes raw and an inherited-ignored one is skipped,
`cmd/bronzewardctl/review.go:131-150`, with the restore and the end by the signal,
`cmd/bronzewardctl/review.go:152-186` and `cmd/bronzewardctl/main.go:96-106`, and no marks prompt
after an error, `cmd/bronzewardctl/review.go:78-79`; a terminal that takes no output,
`cmd/bronzewardctl/pagerout.go:33-66`, and the failed leave, `cmd/bronzewardctl/review.go:168-169`;
the resize, the small window, the cells and the wrap, `cmd/bronzewardctl/pager.go:20-29`,
`cmd/bronzewardctl/pager.go:85-107`, `cmd/bronzewardctl/pager.go:132-148`,
`cmd/bronzewardctl/pager.go:182-232`; the case `internal/clitest/testdata/protected-cases.yaml:37`.
Item 2: `cmd/bronzewardctl/review.go:25-26` and `:75`. Item 3: marks, `cmd/bronzewardctl/fragments.go:235-254`,
`cmd/bronzewardctl/fragments.go:361-368`, `cmd/bronzewardctl/fragments.go:437-446`;
the refusal by position, `internal/client/problem.go` and `cmd/bronzewardctl/review.go:372-386`; the
follow after the `marked` event at the `202`'s generation and the settlement events,
`cmd/bronzewardctl/imports.go:405-470`; `resume-failed` and the offers,
`cmd/bronzewardctl/imports.go:540-545`, `cmd/bronzewardctl/imports.go:546-552`; the lease margin and
the re-read, `cmd/bronzewardctl/imports.go:47-49`, `:422` and the `Until` function at `:476-505`,
which the watch asks after each event shown, at each keep-alive and after each read of the
operation (`internal/client/watch.go:28-50`, `:238`, `:548`), the 15 s keep-alive and the poll
delay doubling to 30 s (`internal/client/watch.go:55-57`, `:228`, `:264`); the lapsed outcome as
unknown, exit 3, `cmd/bronzewardctl/imports.go:546-552`, `cmd/bronzewardctl/review.go:312-366`; a
mark's outcome without a pause at its generation is not applied, `cmd/bronzewardctl/review.go:343-347`;
the continuation and the takeover, `cmd/bronzewardctl/imports.go:240-266`. Web: `web/src/imports.tsx:38`,
`web/src/imports.tsx:50`, `web/src/imports.tsx:210-245`, `web/src/progress.ts:16-39`. The
`import-base document` paragraph: `cmd/bronzewardctl/importbase.go:27-76` (the terminal check, the
refusal of `-o`, the notice and the pager, and no marks prompt) and the refusal shown as it comes,
`cmd/bronzewardctl/importbase.go:63`.

**Proposed**, where today differs: the takeover `409` text "the ingestion has ended" was not searched
for in the CLI, and the order of the lease read against the stream was read only through the code
above and `internal/client/watch_test.go:377-480`, which this change did not run. No shared case
record runs the two `import-base document` refusals against a server in the CLI (see PROT-07 in
[§12](#12-shared-conformance-cases)).

The qualification: the server's `no-store` and the client's refusal to persist limit what the
client keeps. They do not stop the operator, the terminal, a recording or the host from keeping
the text. The requirement is proposed with that limit stated.

`import-base document <ibr>` shows an import base's stored document
(`GET /import-base-revisions/{id}/document`, [persistence and API choice
§17.41](persistence-api.md#17-choices-for-owner-review)) as item 1 shows the review: only when
standard input and output are a terminal, through the same in-process pager, after the item 2
notice, with no `-o json`, `-o yaml`, save or export form, no override, and no marks prompt, since
a stored document is not marked. The server answers any `author` for an import read from a node, and for a
file import only the author who made it from a file they provided and continued its review; the command shows its refusal (`403 forbidden` with `not-the-importer`, or
`409 conflict` with `import-not-reviewed`) as it comes and prints no body.

### 11.4 Web review and document input

Built in the web client, except the documentation sentence of item 4, which stays **Proposed**.
Sources follow each item.

1. The review, and an import base's text (Show imported configuration on the import base page and
   on each import entry of the draft page), is fetched with
   `cache: 'no-store'`, rendered as text in a read-only element with no copy, download or print
   action, and discarded on leaving the page, on `pagehide` and on sign-out. Those pages
   offer the action to any `author` and show the server's refusal in words, as the table of §8.2 says it.
   Beside the opened text the page says that a value found there that should be hidden cannot be
   marked on this import, links the page where the machine is imported again (where Marks is
   filled in), and says the copy read stays stored as it is. (Built: `web/src/claim.tsx:141-142`,
   `:207-208` through `web/src/request.ts:360`; the read-only element, `web/src/claim.tsx:257`; the
   discard, `web/src/claim.tsx:116-122`, `:172`, `:222`; the action for any `author` and the refusal in
   words, `web/src/claim.tsx:190`, `:195`, `:235`, `:240`; the note and the link, `web/src/claim.tsx:258-259`,
   shown on the import base view and not on the review view, where the marks panel stands.)
2. Document and marks fields set `autocomplete="off"`, `spellcheck="false"`, `autocorrect="off"` and
   `autocapitalize="off"`, so browser spelling services do not receive their text (assumption S5).
   (Built: `web/src/forms.tsx:93`, used by `web/src/imports.tsx:719`, `:733`,
   `web/src/drafts.tsx:935-936`, `:967-968`, `web/src/marks.tsx:103` and `web/src/claim.tsx:257`.)
3. No draft of a document or of marks is kept between page loads. (Built: no `localStorage`,
   `indexedDB` or `caches.` in `web/src` outside tests; `sessionStorage` holds only the sign-in
   state, `web/src/app.tsx:192`, `:213-214`.)
4. Browser extensions can read the page; the interface cannot prevent this and says so in its
   documentation. (Built: the page says so, `web/src/claim.tsx:256`. **Proposed:** the documentation
   sentence: a search for `extension` in `docs/operator` finds none.)

### 11.5 Test artifacts

Built in both clients, with the sources after item 3; whether each scan passes is a CI fact this
change did not read. Client tests use synthetic values only, and every retained artifact is scanned for
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

**Built, by item.** CLI: the positive control, `internal/clitest/core_test.go:88-93`; the scan of
`argv`, standard output and standard error, `internal/clitest/cli_test.go:405-416`; the pseudo-terminal
start with the same `argv` scan, `internal/clitest/protected_test.go:83-89`, and the scan of the
retained files, `internal/clitest/protected_test.go:520-535`, case `internal/clitest/testdata/protected-cases.yaml:15`.
Web: `web/e2e/scan.ts` (the retained files, with a planted control), `web/e2e/retention.test.ts:231-256`
(no trace, HAR, video or screenshot call, with the transitive-import positive control at `:231-243`),
`web/e2e/auth/helpers.ts:229-310` (the storage and cache checks with their controls),
`web/e2e/auth/protected.e2e.ts:149-154` (after each protected case), and the CI jobs `browser` and
`browser-auth` that run them (`mise.toml:442-505`). The storage check asserts emptiness, which is
stronger than "no synthetic value".

## 12. Shared conformance cases

One set of cases both clients run, so their behaviour cannot drift apart. Each case
is a language-neutral record (built, with the exceptions in the **Proposed** paragraph after the
list of cases):

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
| AUTH-11 | success | web: three tabs whose access tokens reach the renewal threshold together; second case: the holding tab closes after posting `renewing` and before its answer | first: one refresh request per renewal, counted at the provider, every tab on the new pair, no redirect; second: no refresh request with the spent token, renewal failed as §9.5 item 3, and after one tab signs in again every tab uses its pair |
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
| AMB-14 | disconnect | the injector holds the response headers of a read and of a write for longer than 30 s; second case: it sends `200` headers and holds the body until 60 s after the send; third case: as the second, with `400` headers on the write | first and second: the read reports a transport failure when the bound fires; the write is repeated with the same key at most 3 times, then reported as an unknown outcome with its key and the resource to read, never as not done; the web notice settles and offers Dismiss; third: the write is reported refused with status `400` and a cut-off answer, not repeated ([§8.4](#84-idempotency-and-ambiguous-responses) item 8) |
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
| REC-04 | disconnect | recovery entry commits and its answer is lost: (a) the injector drops the `201`, and the confirming read answers `200` with the new epoch; (b) the server loses its own COMMIT reply and answers `500`, and the confirming read is refused `409 recovery-mode-active`; (b) needs a server-side fault hook, not the injector, and the default server binary has none: the fixture's interrupt build (`-tags fixtureinterrupt`, step `recovery-entry-commit`, action `lose-reply`, `internal/seam`) answers `500` after the commit and stays gated. The server side is covered by `internal/api/recovery_entries_test.go:255-296` and `internal/api/interrupt_seam_on_test.go`, and the client side by the CLI test `TestRecoveryLostReply` and `web/e2e/auth/recovery.e2e.ts` | both: repeated under the same key with no epoch check; `201` with `Idempotent-Replayed: true` and the minted epoch; §8.5 applied without setting the entry aside |
| PROT-01 | success | protected import, review, mark, continue | review only on a terminal (CLI; a pseudo-terminal in tests, its output matched in memory and never retained, [§11.5](#115-test-artifacts) item 1); no synthetic value in any artifact or storage |
| PROT-02 | refusal | `import review` with standard output redirected | refused, nothing written |
| PROT-03 | refusal | mark that is not a path; mark compilation refuses (for example one that would rewrite text); compilation fails on a dependency | first: `422` with `position`; second: `202`, then, after the `marked` event of that `generation`, a `mark-refused` event with `422` and `position` and the ingestion paused again; third: `mark-refused` with `503`, shown by code as not applied; none reported as accepted, no path shown |
| PROT-05 | refusal | mark accepted (`202`), then the run cannot decrypt the staged envelope | after the `marked` event, a `resume-failed` event with `dependency-unavailable`; the watch ends with the operation still `running`; the mark reported as not applied; `import takeover` offered once the lease lapses, and `import abandon`; no wait without end |
| PROT-06 | disconnect | mark accepted (`202`), then the serving process is killed before the run writes any event; second case: the same during a continuation; third case: the same during a takeover; fourth case: during a continuation, the run releases the claim before its lease lapses and its draft transaction is delayed past `leaseUntil` before `COMMIT`, which the fixture's interrupt build does (`-tags fixtureinterrupt`, step `draft-commit`, action `stall`, `internal/seam`) and the default server binary cannot | the client reads the ingestion once its `leaseUntil` has passed; `held` (mark, continuation) or `resumed` (takeover) with `leaseUntil` passed ends the watch with the operation still `running`; the mark, continuation or takeover reported as of unknown outcome, never as not applied; a later read, `import takeover` and `import abandon` offered; in the fourth case the outcome stays unknown, the claim `held` with its lease lapsed, until a later read shows the operation `succeeded`; the read of the operation answers while the commit stalls ([#803](https://github.com/ginsys/bronzeward/issues/803)), and an `abandoned` read is unreachable on a continuation, because an encrypted claim is not due until its `expiresAt` (`internal/staging/sweep.go:13`); no wait until the claim's absolute expiry |
| PROT-04 | disconnect | connection dropped during the review read | nothing persisted; review read again |
| PROT-07 | success, refusal | an import base's document from a file, read by the author who imported and reviewed it, then by another author; and an import read from a node, read by any `author` | the first: the text shows on request only (the CLI on a terminal, in the pager; the web in a read-only field with the notice and no copy, download or print control), is gone on leaving the page and in the pager's quit, and leaves nothing in storage or the HTTP cache; the second: `403 forbidden` with `not-the-importer`, shown as it came, with no text; the third: the text shows the same way for each author |
| COMP-01 | refusal | `GET /server` serves an API revision below the client's minimum | refused before any write, both revisions named; the recovery entry is the exception, since a recovery start refuses that read until its entry commits: the revision shows after the commit, the entry's success stands, and the client says that the read failed |
| COMP-02 | success | response carrying a `Deprecation` header | warning shown once, naming the route |
| ETAG-02 | success | edit after the client's own import succeeded; second case: another author writes between the import and the re-read | first: draft `revision` equals the import's `result.draftRevision`, the next write uses the new ETag, no `412`; second: revision is higher, treated as `412`, newer state shown |
| TLS-02 | refusal | API answers a `3xx` to another origin, to the CLI and to the web client; harness check | the injector sees no request to the target and nothing resent by either client; the CLI names the target |
| TLS-03 | refusal | as TLS-02, to the web client, for a read and for a write; browser message check | the page shows the generic transport-failure message naming a refused redirect as a possible cause and no target; the write is repeated with the same key as in §8.4 item 2 and, when every repeat meets the same redirect, reported as an unknown outcome with its key and the resource to read, never as not done |
| TLS-01 | refusal | certificate from an untrusted CA | nothing sent; host and trust source named |

**Built.** The web client's records are the 40 files of `conformance/cases/` and their browser
files under `web/e2e/` (run by `mise run browser-auth` and `mise run browser`); the CLI's are the
45 case ids of `internal/clitest/testdata/*.yaml`, run by `internal/clitest/cli_test.go:729` for
`testdata/cases.yaml` and by `internal/clitest/protected_test.go:322-323` for the protected cases, with
an in-process TLS injector and `interruptServer` (`internal/clitest/cli_test.go:68`) and the dev
issuer for the sign-in cases (`internal/clitest/login_idp_test.go`). A row marked built has a record
or a named test in every client it names; the CLI-only rows (AUTH-04, AUTH-09, PROT-02, AMB-04,
AMB-10, AMB-11, AMB-13, TLS-01) and the web-only rows (AUTH-08, AUTH-11, TLS-03) are built in that
one client by nature. Whether a case passes is a CI fact this change did not read.

**Proposed**, where today differs: (a) AMB-14 has no case record in either client: its
behaviour exists in both (web `web/src/request.ts:103-104`, CLI `internal/client/transport.go:93-95`,
with unit tests only) and `AMB-14` appears in `conformance/` only inside the text of `PROT-06.yaml`.
(b) PROT-07 has a web record in `web/e2e/auth/protected.e2e.ts:265` and no `conformance/cases/PROT-07.yaml`;
the CLI's behaviour exists (`cmd/bronzewardctl/importbase.go:27-76`) and is tested only by pty unit
tests (`cmd/bronzewardctl/importbase_linux_test.go:40`), with no case against a server in
`internal/clitest`. (c) AMB-12 runs in the web client only as the never-committed variant
(`conformance/cases/AMB-12.yaml`); the removed-by-restore variant is not run there. (d) The AUTH-06
web record covers the stream half only, and the AUTH-02 CLI case runs with no refresh token, so no
renewal attempt is exercised (`internal/clitest/testdata/cases.yaml:110`). (e) AUTH-09's
credential-store half skips without the dbus tools (`internal/clitest/testdata/cases.yaml:202`).

## 13. Alternatives and open choices

D3, D7, D8 and D9 are decided by the lead on 2026-10-21, under the owner's 2026-10-07
delegation, as their rows state. The owner decided D1, D2, D4, D5, D6 and D10 on 2026-10-22, at
the times their rows give; D10's option (a) is served by
[#293](https://github.com/ginsys/bronzeward/issues/293). The lead ruled D11 on 2026-11-01, when
the specification review asked the question in
[#463](https://github.com/ginsys/bronzeward/issues/463); the owner can overrule it. The campaign
lead decided D12 on 2026-11-02, when the specification review asked the question in
[#486](https://github.com/ginsys/bronzeward/issues/486), under the owner's standing rule to take
clearly recommended decisions; it is recorded for owner review. The owner decided D13 on
2026-11-20, as its row states.

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
| D11 | Which remote text the CLI escapes in human output | (a) all remote text, with no list of fields, escaped as a value before it is formatted, tab and line feed included; (b) only fields that hold free text; (c) the list of item 4 as it stands, with the server refusing control characters in its inputs | **Ruled** by the lead (2026-11-01): (a) ([§6](#6-tls-trust) item 8). Reason: the API schemas give most text members as plain strings, and a format that a schema declares binds a correct server, not the bytes that arrive, so no field can be called safe by its format, and a list of fields goes out of date with every command. A test that fills every string member of every response type is what keeps the rule true. Rejected: (b), which needs that list and trusts the formats, declared or not, of a server it cannot check; (c), which trusts the server for resource text and not for its version string, and leaves the CLI open to any input the server does not refuse |
| D12 | A client's bound on its own request | (a) no client bound, so the browser or the operating system decides; (b) one total bound per logical write, its repeats included; (c) a bound per attempt on the response headers and on the whole attempt, with the same values in both clients; (d) an idle bound that fires only when no byte arrives for some time | **Decided** by the campaign lead on 2026-11-02 under the owner's standing rule to take clearly recommended decisions, recorded for owner review: (c), with 30 s for the headers and 60 s for the attempt ([§8.4](#84-idempotency-and-ambiguous-responses) item 8). Reason: `fetch` has no timeout of its own, so under (a) a stalled answer keeps a write on "Saving" or "Repeating" until the operator leaves the page, which drops the write ([#486](https://github.com/ginsys/bronzeward/issues/486)). The CLI already bounds each attempt with these values (`internal/client/transport.go:91-95`), so one pair of values keeps both clients and AMB-14 the same. A request body is at most 1 MiB ([§8.1](#81-requests)), lists come in pages, and a write that starts a long job answers `202` and goes on as an operation ([§8.2](#82-errors)), so a working server is expected to answer well inside 60 s. A write that it answers later is handled as an ambiguous attempt, under the same key. Rejected: (b), which gives a repeat no time of its own after a stalled first attempt used the budget; (d), which a server that sends a byte from time to time never fires, and which Go's HTTP client does not offer for a body read |
| D13 | How the web client is styled | (a) Oat (`@knadh/oat`), the CSS library the sibling control plane uses; (b) a hand-written stylesheet; (c) another library | **Decided** by the owner (Serge, 2026-11-20 08:12 CET): (a), dark first with no light theme ([§10.4](#104-styling-and-the-content-policy), [#969](https://github.com/ginsys/bronzeward/issues/969)). Reason: one look across the owner's projects, with no component code to write or keep. Rejected: (b), which keeps the 28-line stylesheet and no shell; (c), which no one asked for. Oat's drawn `data:` marks conflict with `img-src 'self'`, and the answer is native controls, not a wider policy |

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
G23; until a client resolves them, it shows principal identifiers as it shows every identifier,
the web client shortened and the CLI whole. Specified by
[#335](https://github.com/ginsys/bronzeward/issues/335): `GET /principals`, any role, mapping up
to 100 identifiers in one read, in [persistence and API §9.2](persistence-api.md#92-resources-and-routes);
the batch read was chosen over names in the bodies (choice
[§17.40](persistence-api.md#17-choices-for-owner-review)). A client resolves the identifiers a
web view or a table output shows in requests of at most 100 identifiers each, each given once, and shows a human by its
`display` name, served from API revision 5, or, when `display` is `null`, by subject and issuer (the web client words it "an operator with no name on record, subject <subject> at <issuer>" and says "you" for the signed-in person; the CLI keeps `<subject> at <issuer>`)
([persistence and API §10](persistence-api.md#10-authentication-and-authorization)), a service
by its name, and the identifier for one answered `unknown` or when the read fails: shortened in
the web client, whole in the CLI, which prints every identifier whole. The text is the same in both clients for the same answer, except the web client's "you" for the signed-in person and its wording for a person with no display name, and it is remote text like any other
(§6 item 8). `-o json` and `-o yaml` print the resource with the same values, full identifiers included
([operator experience §4](operator-experience.md#4-command-tree)).

**A8. Titles of plans, releases and operations (client rule, no API change).** Closes G26. Both
clients show a title built from fields the body already carries, with the identifier kept as the
stable reference (the whole identifier as the title attribute in the web client; in the CLI the
identifier keeps its own column of a table and the heading of a detail, for example
`Plan <id>  <state>`, and the title is a TITLE column beside it in a list and a `Title` line under
the heading of a detail, with a dash in the column and no line where no title could be built).
A stored name was the alternative: it needs an API revision and a field set at plan creation
and at publication, and for an operation it could only be generated text, since no operator creates
one. The rule is one for both clients:

| Object | Title | Built from | Reads beyond the object's own |
| --- | --- | --- | --- |
| Release | "`<draft title>`, published `<date>`" | `draft` and `publishedAt` of the release, `title` of `GET /drafts/{id}` | 1: the draft |
| Plan | "`<apply or adopt>` of `<machine>` to `<release title>`" | `operation` (`apply-config` or `adopt`), `machine` and `release` of the plan | 3: its release, that release's draft and its machine |
| Operation | `publish`: "Publish of `<draft title>`"; `ingest`: "Import of `<machine>`", the machine being the one on the ingestion that `subject.ingestion` names, or "Import" when the subject names no ingestion, the ingestion names no machine or its read fails (an operation's `subject` never names a machine for an import: `internal/api/reads.go:436-486` fills `machine` only for a plan's operation); `apply-config` and `adopt`: the title of its plan, which already reads "`<apply or adopt>` of `<machine>` to `<release title>`" (an operation is its plan's execution) | `kind` and `subject` | `publish`: 1; `ingest`: 2 (its ingestion, then that ingestion's machine) or none; `apply-config` and `adopt`: up to 4 (its plan, that plan's release, that release's draft and the plan's machine) |

1. `<machine>` is the machine's name where it has one (A9), else, in the web client once the machine
   is read, its address, or "an unnamed machine" where it has no usable address, never its
   identifier; the CLI shows its identifier, whole. A machine read that gave no body reads "the
   machine" in a title, so the title survives: the titles' store's read on any failure, and a view's
   own read of the machine (an approved plan's, rule 2) where the shell answered it (a 404, an
   exhausted 503). A read still pending leaves the title waiting, and so does a view's own read
   after a transport failure, which the shell holds and which keeps the view as it is until a read
   answers.
2. Each distinct draft, release, plan and machine is read once for each web view, each read again and each
   return to a view after one with no title, as A7 says of names, by the same store and the same
   safe-text rule; the CLI reads each once for each command. A read that fails keeps the
   identifier (a machine's, in a title: rule 1), under the retry bound the 503 row states for the names read (the web client keeps it
   behind the identifier, the CLI tries once). A draft ends `published` or `discarded` and is never
   deleted ([persistence and API §3.1](persistence-api.md#31-sources-drafts-and-heads)), so a missing
   draft is a failed read, not a fallback of its own.
3. A draft title is remote text typed by an author, and it is shown as A7's names are: a text node
   in the web client, with no control or format character and at most 300 code points. In the web client a
   draft whose title is not safe text reads "Untitled draft", with the whole identifier as the title
   attribute and the shortened identifier beside it where the view shows no other; a release's title and a
   publish operation's title built on it read the same words, where the draft is read, and a draft whose read
   failed still leaves the shortened identifier (rule 2). The CLI shows the identifier (§6 item 8 for the CLI,
   as A7 cites it).
4. A title can name a person, so a client keeps it in memory for the web view's generation or for
   the command, and never in storage, a log or an error.
5. `-o json` and `-o yaml` print the resource unchanged.
6. **Applied in the web client** ([#987](https://github.com/ginsys/bronzeward/issues/987)): a release's, plan's and operation's
   list link and item heading show the title (the heading keeps the plan's state and the
   operation's kind and state), the identifier is the title attribute, and a failed read other than a machine's (rule 1) or a
   `subject` lacking what its kind needs leaves the shortened identifier, and an unsafe draft title reads "Untitled draft" (rule 3). A view reads at most four title bodies
   at a time, each distinct one once. A view that names a release, plan, operation, draft, import or cluster by its
   identifier shows the same title, the identifier as the title attribute and the shortened identifier where the read
   does not answer (a cluster reads "This cluster" there) ([#996](https://github.com/ginsys/bronzeward/issues/996)): a plan's release, committed operation and
   change, a release's draft and publication, an import's draft and operation, the plans, imports and dependency
   tables, an act's subjects, an operation's Subject cell (its machine, plan, draft and import), the applied release of
   its outcome line, the review's release and the publication's result line and progress heading. A
   release named in a sentence built as plain text is its title too (the machine, review, plan and approval sentences);
   any other identifier inside such a sentence stays one. A machine
   a view names by identifier is shown by its name (A9) in the plan, operation, import, draft, assignment,
   release, review, recovery and dependency views, read through the same store, or taken from the machine body
   or list the view already holds.
   The drafts lists (the Drafts and releases page and a cluster's open drafts) open with the draft's name by
   rule 3, as the link, with the identifier last under Identifier, and the first of them shows the cluster by its
   name; the Start import choice shows a draft's name by rule 3 too, never the received text. The dependency alert list opens each row with a sentence
   built from the alert's kind and its dependency's object and version, the dependency named once (the link of
   the dependency's page; "This dependency" on that page), then the Kind, Class, Reason, the releases by their
   titles and the time; the identifier is the last column, under Identifier, since an alert has no page of its
   own. A word the server sets and a page prints as a state, kind, class, reason, mode, source, role, action,
   route, cause, operation, platform, hostname, validation result, the way an act came in (Via) or the name of an
   unread field is shown only when it is safe text (item 1, A8 rule 3: no control or format
   character, at most 300 code points), and otherwise reads "(not shown)"; one the server leaves out reads "none"
   where the cell has one, on every page that prints such a word ([#1055](https://github.com/ginsys/bronzeward/issues/1055)).
   A refusal's title and detail are sentences a person must read, so they are never replaced by "(not shown)": the
   page shows each whole (except a refused mark's detail, which §8.2 leaves out), with every control or format character (the same class) read as U+FFFD in its place, and
   cuts a text over 1000 code points with the words "… (the rest is not shown)". The match that decides a next step
   reads the text as received. Other server text is not covered: a version, address, endpoint, contract, identifier, purpose, epoch, event
   type or code, approval policy, observation basis, a result or error record shown as JSON, and the identity
   provider's refusal text. The CLI escapes every
   string it prints instead (§6 item 8).
7. **Applied in the CLI** ([#987](https://github.com/ginsys/bronzeward/issues/987)): `plan list`, `release list` and
   `operation list` print a TITLE column beside the identifier, and `plan show`, `release show` and `operation show`
   print a `Title` line under the heading. A table prints a dash where no title could be built, and a detail no line;
   a draft, release or plan read that fails (and a draft with no title) leaves no title, while a machine that cannot
   be read is named by its identifier and an ingestion that cannot be read is an import with no machine. Each distinct
   draft, release, plan, ingestion and machine is read once for a command, and `plan show` takes the machine from the read its own view makes. `-o json` and `-o yaml` read nothing more. A command that names
   a plan, release, operation, draft or import in a table or a detail prints that object's title after its identifier
   in parentheses (a machine by its name, A9), read through the same cache and once for the command, and the
   identifier alone where the read fails ([#1017](https://github.com/ginsys/bronzeward/issues/1017)): `plan list`'s release;
   `plan show`'s releases (the plan's, the machine's Desired one and the two ends of the diff) and committed operation,
   and `plan cancel`'s; `release list`'s draft; `release show`'s draft and publication; `import status`'s draft and
   operation; the release in the result lines of `publish` and the header of `release review`; `approval show`'s plan;
   and an act's subjects, each of the kind its identifier's prefix names (persistence and API §2: `rel`, `pln`, `op`,
   `drf`, `ing`, `mch`), any other prefix staying as it is. A command a line tells the operator to run, an
   identifier inside a sentence built as plain text, the machine list's Desired and Applied columns, and an operation's
   subject and outcome line stay identifiers: A10 adds words beside them (the `Now` and `Next` rows), and gives no title to
   either.

**A9. A machine's name (client rule; the API revision that adds the name).** Closes G8. A client shows a machine by its
name where the body carries one, with the identifier kept as the stable reference: in the web
client the name as the text and the whole identifier as the title attribute, in the CLI the name
beside the identifier as a cluster's is (`mch_9fd… (node-a)`, [operator experience §4](operator-experience.md#4-command-tree)).
A machine with no name, and a server older than the revision that adds it, which serves no member, read the same
way: in the CLI the identifier, the Talos endpoint and the node ID, as before.

Once its body is read, the web client shows no identifier as a machine's text ([#1055](https://github.com/ginsys/bronzeward/issues/1055)):
until then, and where the read gave none, a link shows the shortened identifier as every other object's does (A8 item 6).
A machine with no name shows its address: the host of its stored endpoint, without the port, shown only
when the endpoint is `a.b.c.d:port` or `[v6]:port` and otherwise not at all. Where one list holds two unnamed
machines on one address, each gets a suffix, `(node 4c1f…)` from its Talos node ID or `(UUID 9a3e…)` from its
SMBIOS UUID (a machine holds one of the two), cut to four characters when longer than eight, or longer where machines
of one address would otherwise read alike, and `unknown` where the value is not safe text
(item 1); a list on another page does not count. A choice list that spans clusters gives the same suffix to
named machines whose choices would read alike, since a name is unique only within its cluster. A machine with
no usable address reads "an unnamed machine" in a sentence and "Unnamed machine" as a heading. Its identifier
stays as the title attribute of its link, and on a machine's own page on a detail line with a Copy button that
says "Copied", or "Select the identifier to copy it" where the browser would not copy.
A cluster's link on that page shows its name where the read gives a safe one, preceded by the word "Cluster",
else "This cluster" alone.
The machine's page names its role after the platform on that line, "control plane" or "worker" in a person's words, and
`machine show` prints a `Role` line; a machine whose read gives no role shows neither, with no sentence about it
([#1096](https://github.com/ginsys/bronzeward/issues/1096), persistence and API choice §17.49).

The draft, import and import-base pages follow the same rule ([#1055](https://github.com/ginsys/bronzeward/issues/1055)).
A draft page's heading is "Draft: `<title>`" (the shortened identifier where the draft has no safe title), with a line
under it giving its state, revision and cluster (named as above), and the identifier on a detail line with Copy. A draft's
entries read as the machine's name, or the fragment's. A publication page heads "Publish draft: `<title>`" with the same
line. An import page heads "Import of `<machine>`: `<state>`" (A8 gives the first part; the state stays so a person sees
at once where it stands), with the identifier on a detail line, and its sentences name the draft by its title and the
imported configuration as "Imported configuration of `<machine>`", never by identifier or revision number. An
imported configuration's page heads "Imported configuration of `<machine>`, imported `<date>`", and a fragment or
assignment revision's "Fragment `<name>`, saved `<date>`" or "Assignment of `<machine>`, saved `<date>`", each with
the identifier on a detail line. A name or title that is not safe text (item 1, A8 rule 3) is not shown: the heading
carries the shortened identifier, as it does until the draft, machine or revision is read, and a draft entry for a
fragment or profile reads "(name not shown)". A machine whose read gave
no body reads "the machine" (A8 rule 1), so the heading survives. The words that show a mark or a rule on an import page, the document view and the publish shortcut are
not part of this rule.

1. The name is remote text like every server string: a declared format binds a correct server, not
   the bytes received (§6 item 8, as A7 cites it). The
   CLI escapes it as it does every other server string. The web client renders it as a text node
   and shows it only when the received value matches the grammar (dot-separated labels of 1 to 63
   characters, lowercase `a-z`, digits and hyphen, neither first nor last a hyphen; 253 characters at
   most; with a dot, no trailing dot and a last label that is not all digits;
   [persistence and API §3.5](persistence-api.md#35-a-machines-name)), else the address; that check
   replaces A7's control and format character test, which a name that passes it cannot fail. The web
   client keeps that grammar in one place, and the onboarding page tests the node's hostname against
   the same constant.
2. A command that takes a `<machine>` takes its identifier or its name. A name is unique only within a
   cluster ([persistence and API §3.5](persistence-api.md#35-a-machines-name)), so the first design
   kept identifiers only, since a command taking a name would need the cluster too. The real-node walk
   recorded on [#1055](https://github.com/ginsys/bronzeward/issues/1055) found that this left a machine
   taken by name in `apply --machine` and refused in the other verbs (`machine show <name>` answered
   404), so the commands now take a name: the command reads the machine
   list once, matches the name across the clusters the credential reads, and refuses a name no machine
   holds and a name that machines of two clusters hold, which answers the need for the cluster
   (`--cluster`, a cluster's name or identifier, narrows it, or the identifier is given). An
   identifier is sent as it is and reads no list. No other name is resolved to an identifier by a
   client, except a profile's fragment pins ([operator experience §5.4](operator-experience.md#54-editing-fragments-profiles-and-assignments)).
   The web client takes none of this; it links by identifier.
3. `machine add` takes an optional `--name`, and `machine rename <id> --name <name>` is a write
   with the role and keying of the table in §3.2. A name another machine of the cluster holds is
   refused `409 name-in-use`, naming that machine; the machine's own current name is `409
   conflict`, and `machine` names the machine itself; a `400 invalid-request` names the grammar.
   `cluster onboard` adopts the machine a `409 conflict` on `POST /machines` names, as an existing
   node (`cmd/bronzewardctl/onboard.go:441`, [operator experience §5.2](operator-experience.md#52-inventory)):
   it must not read `name-in-use` that way, and a client that sends a name tells the two by the
   code. A client that names machines as it records them (`cluster onboard` and the onboarding page)
   takes a `name-in-use` or a `400 invalid-request` on a name it sent as the refusal of the name, never
   of the machine: it records the machine again without the name, goes on, and says in the machine's
   line why it has none and, for `name-in-use`, which machine holds the name.
4. A `name-change` entry on `GET /machines/{id}/timeline` is rendered from its members like any
   other kind, and a client must not fail or drop the row for a kind it does not know:
   `api/openapi.yaml:4178` types `kind` as a free string.
5. A8's derived titles name the machine, so a title held before a rename is stale; A8 rule 4
   already keeps no title beyond the view or the command.
6. **A rename says when it reaches the machine** (compilation §6.1, [persistence and API §3.5](persistence-api.md)).
   Where a client offers the rename (`machine rename`, the web rename form) it says, in words and with no field
   name: "If this machine's hostname comes from its name, the new name reaches the machine only after the next
   release is published and a plan for it is approved. Until then the machine keeps its current hostname." Two
   more sentences depend on a measurement the compiler issue (ginsys/bronzeward#1040) makes, and the client
   carries the one the measurement picks. A restart: "The machine takes the new name when it next restarts." or
   "The machine takes the new name when the plan is applied." Until measured: "Whether the machine needs a restart
   to take the new name is not yet confirmed." The Kubernetes node name: "On the node, the Kubernetes node name
   normally follows the hostname." The web rename form is on the machine's page, beside Change endpoint: "Name this
   machine" for a machine with no name, "Rename machine" for one with a name. It tests the typed name, trimmed,
   against the grammar of item 1 and sends nothing that fails it; for `name-in-use` it shows the machine that holds
   the name as a link.
7. **A release says where each machine's hostname came from** (persistence and API §9.2, the `hostname` member).
   A release review shows, per machine, one of these sentences by `source`, naming the machine by its name where
   it has one: `machine-name`, "Hostname: node-a, taken from the machine's name."; `configuration`, "Hostname:
   set in the configuration. The machine's name does not change it." with no value, since the release records
   none and the value may be a secret reference or withheld; `none`, "Hostname: not set in
   the configuration; the node chooses its own."; `legacy-stable`, "Hostname: the configuration holds the legacy
   stable-hostname setting, so the machine's name is not used. Remove that setting to use the name.";
   `unrecorded` (a release made before the source was recorded), "Hostname: not recorded for this release." and
   no more: it never claims the node chose its own.
   For `machine-name` on a machine whose applied release recorded `none` or `legacy-stable`, the review adds: "This
   release sets the machine's hostname to node-a, taken from its name. The last release applied to it left the
   hostname to the node. The machine's Kubernetes node name normally follows the hostname, so it may change too." followed by the
   restart sentence of item 6. For a machine whose applied release recorded `unrecorded`, the review says only:
   "This release sets the machine's hostname to node-a, taken from its name." followed by the restart sentence of
   item 6. For a machine whose applied release recorded `configuration` (the route of compilation §6.1 item 5, a
   release that deletes the hostname setting so the name takes over): "This release now takes the machine's
   hostname from its name, node-a, in place of the setting it had before." and never the earlier value, which
   the release does not record and which may be a secret reference; it is followed by the Kubernetes node name sentence ("The machine's Kubernetes node name
   normally follows the hostname, so it may change too.") and the restart sentence of item 6. For a machine whose
   applied release recorded another name: "This release sets the
   machine's hostname to node-b, taken from its name. The last release applied to it set node-a." followed by the Kubernetes node name sentence ("The machine's
   Kubernetes node name normally follows the hostname, so it may change too.") and the restart sentence of item 6.
   The sentence describes the release and never claims what the machine reports, since an adopted release may not
   match what the machine holds. Where the machine's current name differs
   from the name the release recorded: "This release used the earlier name node-a. While its hostname comes from the
   machine's name, the next release will use node-b." A `source` the client does not know shows its raw value and no sentence. Where the
   release last applied to the machine cannot be read, the review keeps the sentence of its own source and adds only: "The comparison with the
   release last applied to this machine could not be read." It never guesses the comparison. A publication refused for a
   hostname clash (compilation §6.1 item 6) shows the refusal's own sentence. The CLI and the web console are
   ginsys/bronzeward#1042 and ginsys/bronzeward#1043, and each tests its sentences against these.
8. **A plan says when it waits for another machine's hostname** (persistence and API §9.3, the `hostnameHeldBy`
   member; execution and recovery §3.2 comparison 6a). Where `hostnameHeldBy` names a machine, the plan's page
   or command output and the machine's page say, in words and with no field name: "This plan waits. The last
   release applied to machine node-a records the Kubernetes node name node-a, and this release gives the same
   name to machine node-b. This plan has sent nothing to either machine. Apply the release for machine node-a first,
   or give machine node-b another hostname." The name is the `nodeName` of the plan's release for the machine,
   which is never a secret; each machine is named as A8 rule 1 names it: by its name where it has one, else in the web
   client by its address or "an unnamed machine", in the CLI by its whole identifier. The sentence says "waits", never "refused" or "failed": the plan stays approved and nothing was
   sent. Where a commitment was refused on this ground, the timeline's refusal entry is shown as: "The plan could
   not start yet: the last release applied to machine node-a records the Kubernetes node name node-a." with no hostname value beyond that name. The entry holds the other machine's identifier (`holder`) and the node name (`nodeName`), and a client words it
   from them: the web client names the machine as above when it reads the entry, so a rename leaves no stale name, and the CLI's timeline text reads no machine and
   so names it by its whole identifier. An entry written before these members shows the server's own cause. No sentence of this item says what a node runs or has: each says what a recorded release holds.
   For a machine whose release records `nodeName` `null` (not compared) for any cause of persistence and API §9.3
   that the review hides (an input to the hostname is a secret reference or a value the review redacts, or the review
   of the release is withheld in whole, a literal hostname included), the release review adds: "Not checked: the
   review hides an input to this machine's Kubernetes node name, so a clash with another machine's node name cannot
   be checked." It names no
   cause and no value. For a release whose `nodeName` is absent, not recorded because
   the release was made before it was recorded, the review says: "Not checked: this release was made before
   Kubernetes node names were recorded, so a clash with another machine's node name cannot be checked." and names no
   secret. The sentence is scoped to the node-name comparison: publication still compares such a release's recorded
   hostname with the other machines'. A release with an `unrecorded` source is one of these, and its item 7 sentence ("not recorded for this
   release") stands beside it. A release made after the record with a `none` or `legacy-stable` source and
   `nodeName` `null` gets no sentence of this item: its sentence in item 7 already says the node chooses its own or
   that the setting is legacy. A `hostnameHeldBy` the client cannot show (a machine it cannot read)
   shows "This plan waits for another machine's hostname." and no more. The CLI and the web console are
   ginsys/bronzeward#1042 and ginsys/bronzeward#1043 for the words of item 7; this item's words are built in both
   clients by ginsys/bronzeward#1149.

**A10. An operation says in words what it did, who started it and what comes next (client rule, no API change).**
Closes G28 ([#998](https://github.com/ginsys/bronzeward/issues/998)). The web client's operation page shows one sentence
for each kind and state the server can produce (persistence and API §8.2; execution and recovery §3), ahead of its
outcome line, and under it the next step an operator can take, from the steps of
[operator experience §2.3](operator-experience.md#23-import-publish-plan-approve-apply).

1. The pairs are `publish` with `queued`, `running`, `succeeded` or `failed`; `ingest` with `running`, `succeeded` or
   `failed` (never `queued`); `apply-config` with `committed`, `sending`, `verifying`, `completed`, `rejected`, `failed`,
   `cancelled` or `unresolved`; and `adopt` with `completed` only. The sentences and next steps are the table in
   `web/src/operation-words.ts`, and a unit test lists each pair against its text. The CLI keeps the same table in
   `cmd/bronzewardctl/operation_words.go`, and a Go test reads the web table's source and compares every pair word for word.
2. The outcome facts a state carries stay with the outcome line: the cause, the attempts, the applied release or the
   adoption record, and the Recovery route of an `unresolved` operation, which is its next step too. A failed `publish` or
   `ingest` carries a problem document, so its outcome line, and the publish page's end line, also give its `rule`, its
   `input` ("the base", or the fragment by the `fragment` name and `layer` the server adds, with its position in
   brackets; the position alone where the server names none), its `paths`, its `message` and its `machine` by name, each
   as safe text (item 1, A8 rule 3); the web client shows up to 20 paths and says how many it leaves out. The client shows
   what the server serves. The server redacts a message by template and withholds one it cannot explain, and searches the
   rest for exact copies of resolved string values of six bytes or more (compilation §8.3); a shorter copy, or a copy of
   an integer or boolean, inside an authored literal is not detected, and a message or a path may quote that literal
   from a fragment (compilation §8.4). A run of tabs and line breaks in a message reads as one space. The rule `invalid`,
   Talos's validation of the machine's whole configuration, is said in words and names no fragment; after a
   publication refused with that rule, and no other, the publish page lists the entries of the draft it holds, but only
   while that draft's revision is the one the refused publication was made from. The operation page shows a problem document in the one closed reference
   section, in place of an Error row. The sentence says what
   the state means and the outcome line says what happened in it, so neither repeats the other.
3. A kind or state the table does not know, and a pair the server cannot produce, show both values as the server wrote them
   in a sentence that says the page has no description of it, and no next step. Never a blank. A test runs every pair of the
   server's kind and state lists, which the web client pins equal to the Go lists.
4. `createdBy` is null for `apply-config` and `adopt` only: the controller creates those operations itself, at the
   dispatch commitment or at the commitment of an adopt plan (`internal/api/commit.go:314`, `internal/api/adoption.go:298`),
   so no principal made the request. The list's "Started by" column and the page's "Created" row name the principal and role
   for a publish or an import, and for an apply or an adoption "the controller, committing plan" or "adopting under plan",
   the plan's title as a link, and "created by" the plan's creator where the plan's read gave one. A plan that cannot be
   read leaves the controller and the plan's identifier. The plan is read once with its title (A8 rule 3). A null
   `createdBy` on a `publish` or an `ingest`, which the server never writes, shows as none.
5. An adopt operation's outcome line says the machine's configuration was recorded as Applied, from the adoption record its
   result names. It does not say that no applied release is recorded, which was true of no adopt operation.
6. The CLI applies A10 ([#1014](https://github.com/ginsys/bronzeward/issues/1014)). `operation show` prints the sentence as
   a `Now` row, the next step as a `Next` row, and who started the operation in its `Created` row: the principal and role,
   or for an apply or an adoption the controller, the plan and the plan's creator, from the plan read A8 already makes.
   `operation list` has a `STARTED BY` column with the principal and role, or "the controller", never the plan. It makes
   no read beyond the title reads A8 already makes and the principal names every table reads, and `-o json` and `-o yaml`
   read nothing and print the resource unchanged. Two differences from the web client, each
   tested: (a) for an operation that ended without success, or is unresolved, the outcome's own next step stands in for
   the table's `Next` row, so the step is said once, and where the outcome has no command to give (a failed publish or
   import) it gives the table's text and never a line that sends the reader back to `operation show`; (b) an unknown
   pair says "this command has no description of it". `operation watch` prints the same outcome lines.

**A11. One button adopts a draft's machines (client rule, no API change).**
The web client's draft page offers "Adopt these machines" for a draft that holds an import base
([#1066](https://github.com/ginsys/bronzeward/issues/1066)).

1. It has no route of its own. For each machine of the draft with no assignment entry in it and no assignment head it
   first writes an assignment that selects nothing (a plan binds the release's assignment revision, and Connect gives
   each machine one: a draft connected before that holds none), each write under the ETag the one before answered and
   the first under the page's. It then sends the draft's publication, then for each machine an adopt plan and that
   plan's approval, each through its existing route and each recorded as a typed act is. It never creates an apply-config plan.
   The approval's self-approval reasons are the server's, computed when it is recorded.
2. The sentence above the button is the confirmation, as the sentence above Approve is on the plan page
   ([operator experience §5.6](operator-experience.md#56-plan-and-approval)); no web approval types the plan ID
   ([ginsys/bronzeward#1096](https://github.com/ginsys/bronzeward/issues/1096)). What differs from the plan page is that the
   button approves a plan the person has not been shown, which is why item 3 limits it to a first adoption. The command's
   `approve` shows the plan and asks `Approve it? [y/N]`, as `bronzewardctl apply` does for several plans; no CLI approval
   types the plan ID either.
3. It acts only on a machine with no `Applied`. A machine with an `Applied`, a drift record included, keeps its separate steps.
4. Each act is chosen from the draft's state and the `step` of `GET /machines/{id}/position`, read at that moment. A
   second press, after a closed page or a failed step, therefore sends no act whose result exists. The one request that
   can meet a result is the publication: the server answers a publication already queued or running for the draft's
   revision with that operation ([persistence and API §7.3](persistence-api.md#73-natural-keys)), and the press follows
   it with the progress reader the publication page uses, ending in its words. The publication carries the draft's
   ETag as the page showed it (the one the publication page and the discard keep), so a draft another author changed
   is refused with 412 and never published unseen. The draft's line then says so and offers "Show their change", the
   page's own route for a refused draft write: it reads the draft again and keeps that read's ETag (and asks first while
   the editor holds unsaved text), and the next press sends that ETag. With no ETag kept the press sends nothing and
   says to read the draft again. Every write of one press carries the epoch the press began under as its reference
   epoch, so one made after the installation's epoch changed is refused by the server and set aside in the page's
   words; the press also stops, quietly, before such a write. A write of an earlier press that is not finished (in
   flight, of unknown outcome, or set aside) holds the next press back, with a note, until it is repeated exactly from
   its notice or the notice is dismissed: a new press would send the same act under a new key. A machine whose step
   fails stops alone with the refusal in words, and a later press retries it alone. The same act twice in one press stops
   the machine instead of looping.
5. The roles are the routes' and the draft page's: `author` for the draft page and the machines' configurations,
   `publisher` for the publication and the plan, `approver` for the approval. Without any of the three,
   the button is off and the page names the missing role and the separate steps.
   A press while the draft editor holds unsaved text sends nothing and says to save or discard it first, because the
   publication sends only what is stored.
6. After the approval the controller records the adoption unasked
   ([execution and recovery §6.3](execution-recovery.md#63-adopt)). The page follows each machine for a bounded time and
   says that the person may leave: the adoption finishes without the page.

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
| [#263](https://github.com/ginsys/bronzeward/issues/263) validation | A5, D8; the server-side hooks of the fixture's interrupt build, which landed with [#366](https://github.com/ginsys/bronzeward/issues/366): a COMMIT fault hook for REC-04 (b) and a stall of a draft transaction's `COMMIT` after its claim's release for PROT-06's fourth case | 11.5, 12 |

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
- **S9.** Every matrix browser provides Web Locks (`navigator.locks`), grants an exclusive lock
  to one tab of an origin at a time, in request order, and releases a tab's locks when it closes
  or leaves the page (Web Locks API). AUTH-11 observes the grant order and the release on close.

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
- The first-key route's client side is built
  ([ginsys/bronzeward#900](https://github.com/ginsys/bronzeward/issues/900)): bronzewardctl has the
  `cluster first-key` command, `cluster discover` prints it, and `cluster onboard` sends the first key
  through the same route and takes no OpenBao address or token. [§3.3](#33-client-actions-with-no-route)
  and [operator experience §5.2](operator-experience.md#52-inventory) describe it, including a second
  run that supplies the first key again.
- The web client's Connect a cluster page
  ([ginsys/bronzeward#1001](https://github.com/ginsys/bronzeward/issues/1001), operator experience
  [§6](operator-experience.md#6-web-interface)) does the same steps through the same routes, with the
  talosconfig chosen in the page. It reads no Talos node and holds no OpenBao address: the server's
  discovery reads the nodes. The page cuts the file to one context in memory, as `OneContext` does (a
  golden file read by a Go test and by the page's test keeps the two cuts equal), sends that cut to the
  discovery and to the first key only, and drops its copy when the first key's write is released.
  It sends every write of a plan, in every press of it, under the epoch the plan's first press began under
  (a client that holds no epoch then sends nothing and the page says so;
  [§8.4](#84-idempotency-and-ambiguous-responses) item 3): once the installation's epoch has changed, the
  next write is refused by the server (or not sent, where the shell has already invalidated the page's reads) and set
  aside in the page's words, no later write goes out, and the page names what is not finished (each machine and
  import by name, then the later steps) and offers the file again, which finds what is recorded and begins under
  the new epoch, never a second run of the same plan. Where the shell keeps the page (it held the file as the epoch
  changed, so its actions are off), the page offers a link to the machines instead, from where the cluster is
  connected again.
  The browser case ONBOARD-01 runs the flow against two stand-in nodes, so what a real node answers
  to the deployment's address policy
  ([ginsys/bronzeward#1009](https://github.com/ginsys/bronzeward/issues/1009)) is not proven by it.
- For a person signed in as recovery-admin
  ([#361](https://github.com/ginsys/bronzeward/issues/361)), bronzewardctl reads recovery status
  and sends recovery enter, recovery exit and identity revoke; enter and revoke are sent only after a `y`,
  with nothing typed back. Its tests run REC-02 and REC-04 (a) and (b) against a served
  bronzeward; (b) runs the fixture's interrupt build, whose server-side COMMIT fault hook
  [#366](https://github.com/ginsys/bronzeward/issues/366) added. With
  [#362](https://github.com/ginsys/bronzeward/issues/362) it also sends recovery scope-account,
  account, takeover, resolve, mark and release. Its tests run AMB-05, AMB-07 and AMB-12 with
  scope-account as the dropped write, and the 403 role refusal on all nine recovery write commands.
- Line references were read at `3cbe953`, except those to the identity read and the
  expected-epoch check, which are read with
  [#277](https://github.com/ginsys/bronzeward/issues/277) and
  [#293](https://github.com/ginsys/bronzeward/issues/293) respectively applied (see the opening),
  and those to the web client in [§8.6](#86-event-resumption) item 6, which are read with
  [#443](https://github.com/ginsys/bronzeward/issues/443) applied. They move as the code changes.
- **Safari and 320 CSS pixels** (the lead's decision RULING-20261119-1756-38, under the owner's
  2026-10-07 delegation; the owner may overrule it). The 320 CSS pixel reflow cases are untested in
  Safari, because safaridriver has no viewport command and Safari's window stops at 336 CSS pixels;
  those cases run in Chrome, Edge, Firefox and Firefox ESR. This sits beside the owner's accepted
  limitation that Safari 16 is not run (BW-255-matrix-1, recorded on
  ginsys/bronzeward#255). [`conformance/README.md`](../../conformance/README.md#safari)
  lists the cases.
- Every choice in [§13](#13-alternatives-and-open-choices) and every implementation choice above
  is decided. This document decides no credential policy.
- It does not change any role, human-only rule, route, schema or server setting.
