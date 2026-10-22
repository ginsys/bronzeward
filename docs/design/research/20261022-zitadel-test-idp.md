# Zitadel as the test identity provider: live checks

22 October 2026. Outcome record for [#281](https://github.com/ginsys/bronzeward/issues/281).
Every check passed or was answered. Zitadel is the clients' test identity provider
([`fixtures/dev-idp`](../../../fixtures/README.md#test-identity-provider)). This record selects no
identity provider for deployments and sets no credential policy.

## Scope and decision enabled

Before the self-test depends on it, establish that the pinned release does what the
[client contracts §9](../../spec/client-contracts.md#9-authentication) need from a provider:
authorization code with PKCE, refresh rotation bound to the client, the device grant, revocation,
end-session, cross-origin token calls, and a token the server's verifier accepts with roles. The
decision enabled is P1-P4 of the issue: Zitadel, digest-pinned; the device leg in scope; headless
login through the Session API; groups from an Actions v2 target.

## Pinned version and setup

- `ghcr.io/zitadel/zitadel:v4.19.5@sha256:a586456a208e0db4c633db0ba43c427c24b37846138b8b13ff16ad8e003a18d3`
  (`ZITADEL_IMAGE`), `start-from-init`, TLS disabled, external name `localhost`; its database at
  the `POSTGRES_IMAGE` pin.
- Synthetic inputs only: a fixed master key, first-instance machine users with token files, the
  humans of `issuer.Humans` plus `h-none` with one synthetic password.
- Instance OIDC settings for the checks: access and ID tokens 300 s, refresh idle 120 s and
  absolute 600 s. `fixtures/dev-idp` keeps 300 s and lengthens renewal to 10 min and 1 h.
- Headless login: authorize is answered by a redirect to `/ui/v2/login/login?authRequest=V2_<id>`.
  The login machine user creates a session with `POST /v2/sessions` (user and password checks)
  and finishes with `POST /v2/oidc/auth_requests/<id>`, whose `callbackUrl` carries the code.

Reproduction: `mise run dev-db`, `mise run dev-idp`, `mise run go-idp` repeats the passing paths of
checks 1-4, 6, 7, 9, 11 and 12 and end-session (`internal/api/idp_test.go`): check 4 in the
web-to-CLI direction, check 9 on the Native app with devMode off. Checks 5, 8 and 10, the deny
list, and the remaining parts (check 4's CLI-to-web direction, check 7's target-down `500`, check 9
with devMode on and another path) were run by hand with `curl` against the same container and these
settings, as below.

## Outcomes

| # | Check | Observed | Result |
| ---: | :--- | :--- | :--- |
| 1 | Device flow, Native PKCE app | `expires_in` 300, `interval` 5. Approved through `GET /v2/oidc/device_authorization/<user code>` and `POST /v2/oidc/device_authorization/<id>` with the session: token issued. Denied: `access_denied`. Unapproved: `authorization_pending` to t+270 s, `expired_token` at t+300 s | pass |
| 2 | User Agent app refresh | `offline_access` and the refresh grant give a refresh token; refreshing rotates it | pass |
| 3 | Rotated-out refresh token | `400 invalid_request` (`Errors.OIDCSession.RefreshTokenInvalid`), not `invalid_grant`. Reuse does not revoke the session: the current token still renews | pass, residual |
| 4 | Refresh token at another client | the CLI's token at the web client ID, and the web's at the CLI's, same project: `400 invalid_client` | pass |
| 5 | Idle and absolute expiry | refused after 130 s idle; renewing every 60 s works to t+550 s, refused from t+610 s. The last access token still lives its full 300 s past the absolute end | pass |
| 6 | `aud` | with scope `urn:zitadel:iam:org:project:id:<project>:aud`: every app's client ID and the project ID; the server's single audience is the project ID | pass |
| 7 | groups claim | `["bw-authors"]`, `["bw-viewers"]`; `[]` with no grant, present. Re-run on renewal. Target down: token endpoint `500 server_error`, no token | pass |
| 8 | Native groups scope | none: `scopes_supported` lists none, and neither `groups` nor `urn:zitadel:iam:org:project:roles` adds a claim to the JWT access token (`aud`, `client_id`, `exp`, `iat`, `iss`, `jti`, `nbf`, `sub`) | answered: target required |
| 9 | Loopback redirect on any port | `http://127.0.0.1/callback` registered: ports 49152, 50001 and 61999 accepted, with devMode on and off; another path `400` | pass |
| 10 | Cross-origin token call | preflight `204` and the `POST` carry `Access-Control-Allow-Origin` for `http://localhost:58443`, with credentials. Any origin is echoed (`https://evil.example` too) | pass, residual |
| 11 | Public revocation | `POST /oauth/v2/revoke` with the token and client ID only: `200`; the refresh token is refused and the provider's userinfo refuses the access token (`401`) | pass, residual |
| 12 | Access-token lifetime | set to 300 s: `exp - iat` = 300 | pass |

End-session (an acceptance criterion, not a numbered check): `GET /oidc/v1/end_session` with
`id_token_hint`, client ID, post-logout URI and state redirects (`302`) straight to that URI with
the state. The provider then refuses the session's access token, but the `offline_access` refresh
token still renews.

Groups target details: the call is `POST` with `function` `function/preaccesstoken`,
`user_grants[]` (each with `project_id` and `roles`, absent with no grant), `user`, `userinfo`,
`org` and `application`. It is signed `Zitadel-Signature: t=<unix>,v1=<hex>`, the HMAC-SHA256 of
`<t>.<body>` with the signing key Zitadel returns when the target is created. The answer
`{"append_claims":[{"key":"groups","value":[...]}]}` adds the claim.

Failure found and worked around: target creation is refused with `Errors.Target.DeniedURL` for a
loopback or private address, and for a host name that does not resolve yet. The default
`HTTPClient.DenyList` holds every loopback and private range. `fixtures/dev-idp` replaces it with
`169.254.0.0/16` (`ZITADEL_HTTPCLIENT_DENYLIST`) and starts the target before creating it. That
override belongs to this throwaway provider only, never to a production default.

## Provider residuals

Behaviour of the pinned release that a client must not rely on otherwise:

- **No reuse detection (3).** Presenting a rotated-out refresh token is refused but does not end
  the session. A leaked old token is useless, but its use goes unnoticed.
- **Any origin reflected (10).** The token endpoint echoes every `Origin` with
  `Access-Control-Allow-Credentials: true`; there is no per-app origin allowlist. The public
  clients carry no secret, so the token request's own checks (PKCE, refresh-token binding) are
  the protection.
- **Revocation is not seen by the server (11).** Revocation and end-session end the provider's
  session, but the server verifies access tokens locally, so it accepts one until its `exp` plus
  the verifier's 60 s leeway (`internal/auth/auth.go:95`), at most 6 minutes here
  ([§9.7](../../spec/client-contracts.md#97-sign-out-and-cache-clearing)).
- **End-session leaves renewal.** The refresh token survives end-session, so the web sign-out's
  revocation step ([§9.7](../../spec/client-contracts.md#97-sign-out-and-cache-clearing) item 2)
  is what ends renewal.
- **Absolute expiry (5)** bounds renewal, not the last access token issued before it.

## Alternatives and limits

The issue weighed the alternatives (P1-P4). Zitadel's own roles claim was rejected for groups
because it is an object (`internal/auth/auth.go:209-221`). A login container and HTML scraping
were not needed, because the Session API plays the login UI. The human-run sign-in path stays
with [#263](https://github.com/ginsys/bronzeward/issues/263).

Limits:
- Checks ran on one host against one release. A version bump repeats them.
- Plain http on loopback only. Behaviour behind TLS or under another external name is not
  covered.
- The self-test shortens the device code to 1 minute, so the 5-minute default rests on check 1.
- AUTH-07, a different subject on renewal, cannot be produced by a real provider on request, and
  stays with the fault injector of [§12](../../spec/client-contracts.md#12-shared-conformance-cases).
