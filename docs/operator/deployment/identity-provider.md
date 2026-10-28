# Identity provider: Zitadel

Bronzeward is a generic OIDC relying party. Zitadel is the reference provider, at the release
[#281](https://github.com/ginsys/bronzeward/issues/281) checked live: `v4.19.5`, pinned by digest as
`ZITADEL_IMAGE` in [`fixtures/versions.env`](../../../fixtures/versions.env). The
[live checks](../../design/research/20261022-zitadel-test-idp.md#outcomes) are the evidence for
every behaviour below. Another release, or another provider, needs those checks repeated. This page
sets no credential policy: the lifetimes are yours to choose, within the limits stated.

Host names are synthetic: the site is `https://bronzeward.example.com` and the issuer
`https://idp.example.com`. Every value in angle brackets is your own.

## What Bronzeward needs from a provider

From [client contracts §9.1](../../spec/client-contracts.md#91-identity-provider-requirements) and
[§15](../../spec/client-contracts.md#15-assumptions-and-limits):

- JWT access tokens whose `aud` holds the server's `auth.oidc.audience`. They also need a `jti`,
  and a lifetime no longer than `auth.oidc.maxTokenLifetime` (15 minutes by default).
- A `groups` claim that is an array of strings. The server refuses a token whose groups claim is
  anything else.
- Two public clients, one for the CLI and one for the web interface, each with the
  authorization-code grant, PKCE `S256` and no client secret. The CLI client also has the
  device-authorization grant (decision D4 (a)).
- Refresh tokens that rotate on every use and are bound to the client that received them.
- Cross-origin calls to the token endpoint from the web origin.

## 1. Instance OIDC settings

Under the instance's OIDC settings (`PUT /admin/v1/settings/oidc`), the values the fixture runs
with ([`fixtures/dev-idp`](../../../fixtures/dev-idp)):

```json
{"accessTokenLifetime": "300s", "idTokenLifetime": "300s",
 "refreshTokenIdleExpiration": "600s", "refreshTokenExpiration": "3600s"}
```

- `accessTokenLifetime` must not exceed `auth.oidc.maxTokenLifetime`. It also bounds how long the
  server accepts a token after sign-out (see the consequences below).
- `refreshTokenExpiration` is the absolute limit on renewal, however active the session is. Keep it
  short. It is the only bound on a copied refresh token (consequence 1). #281 saw it enforced: at
  600 s, renewal every 60 s worked to t+550 s and was refused from t+610 s.
- `refreshTokenIdleExpiration` ends a session nobody renewed: refused after 130 s idle at 120 s.

## 2. The project and its roles

Create a project with `projectRoleAssertion` on. Add one role per group that `auth.roles` maps,
and grant each person the roles they hold. The fixture's role keys are `bw-viewers`,
`bw-authors`, `bw-publishers`, `bw-approvers` and `bw-recovery`, as
[`examples/bronzeward.yaml`](../../../examples/bronzeward.yaml) maps them. The project's ID is the
audience.

## 3. The two applications

In the project, register the CLI as a **Native** OIDC application:

```json
{"name": "bronzewardctl", "appType": "OIDC_APP_TYPE_NATIVE", "authMethodType": "OIDC_AUTH_METHOD_TYPE_NONE",
 "redirectUris": ["http://127.0.0.1/callback"], "responseTypes": ["OIDC_RESPONSE_TYPE_CODE"],
 "grantTypes": ["OIDC_GRANT_TYPE_AUTHORIZATION_CODE", "OIDC_GRANT_TYPE_REFRESH_TOKEN", "OIDC_GRANT_TYPE_DEVICE_CODE"],
 "accessTokenType": "OIDC_TOKEN_TYPE_JWT"}
```

Register the web interface as a **User Agent** application:

```json
{"name": "bronzeward-web", "appType": "OIDC_APP_TYPE_USER_AGENT", "authMethodType": "OIDC_AUTH_METHOD_TYPE_NONE",
 "redirectUris": ["https://bronzeward.example.com/callback"],
 "postLogoutRedirectUris": ["https://bronzeward.example.com/signed-out"],
 "responseTypes": ["OIDC_RESPONSE_TYPE_CODE"],
 "grantTypes": ["OIDC_GRANT_TYPE_AUTHORIZATION_CODE", "OIDC_GRANT_TYPE_REFRESH_TOKEN"],
 "accessTokenType": "OIDC_TOKEN_TYPE_JWT"}
```

`accessTokenType` JWT is required: the server verifies tokens locally and cannot use opaque ones.
Leave `devMode` off. The fixture turns it on only so that the web client can use a plain-`http`
redirect. Zitadel returns each application's client ID.

## 4. The groups claim

Zitadel's own role claim is an object, and no scope adds a groups array, so a token from it alone
is refused. An Actions v2 **target** adds the claim:

1. Run a small HTTP service that answers Zitadel's `preaccesstoken` call. It reads
   `user_grants[]` (each with `project_id` and `roles`; absent for a user with no grant), and
   answers `{"append_claims":[{"key":"groups","value":[<role keys in your project>]}]}`. Before
   answering, it checks `Zitadel-Signature: t=<unix>,v1=<hex>`: the HMAC-SHA256 of `<t>.<body>`
   with the target's signing key. It refuses a bad signature or a stale timestamp.
   [`fixtures/oidc/groups`](../../../fixtures/oidc/groups/groups.go) is a tested implementation of
   this contract, built for the fixture. It is not a shipped component.
2. Create the target (`POST /v2/actions/targets`) with `restCall.interruptOnError` set to true.
   Keep the signing key that Zitadel returns.
3. Bind it to the function (`PUT /v2/actions/executions`, condition
   `{"function":{"name":"preaccesstoken"}}`).

## 5. The server and the clients

In the server's configuration ([Configure](../README.md#configure)):

```yaml
auth:
  oidc:
    issuer: https://idp.example.com
    audience: "<project ID>"
    groupsClaim: groups
    maxTokenLifetime: 15m
```

`bronzeward-client.json` at the web root ([hosting](hosting.md#the-client-configuration-file)):

```json
{"issuer": "https://idp.example.com", "audience": "<project ID>",
 "cliClientId": "<CLI client ID>", "webClientId": "<web client ID>",
 "scopes": "urn:zitadel:iam:org:project:id:<project ID>:aud"}
```

Zitadel puts the project ID into `aud` only when the client asks for that scope. Without it, the
server refuses every token.

`bronzewardctl context add --from` does not import `scopes` yet, and the CLI's own sign-in is still
being implemented ([ginsys/bronzeward#254](https://github.com/ginsys/bronzeward/issues/254)). Until
both land, add a CLI context by hand:

```sh
bronzewardctl context add prod --server https://bronzeward.example.com --issuer https://idp.example.com \
  --client-id "<CLI client ID>" --audience "<project ID>" --scopes "urn:zitadel:iam:org:project:id:<project ID>:aud"
```

## Consequences of Zitadel's behaviour

Each line is a #281 observation, with what it means for a deployment.

1. **A replayed refresh token does not end the session** (check 3). Zitadel refuses a token that
   was already used (`400 invalid_request`, not `invalid_grant`) but revokes nothing. Whoever
   renews a copied token first keeps that session until the idle or absolute limit. The legitimate
   client is refused and must sign in again. Only a short `refreshTokenExpiration` bounds this.
2. **Any origin is allowed cross-origin** (check 10). The token endpoint echoes every `Origin`,
   with credentials, and Zitadel has no per-application origin list. The protection is PKCE and
   the binding of each refresh token to its client: the CLI's token is refused at the web client
   ID and the reverse (`400 invalid_client`, check 4). Do not count on the provider to limit
   origins.
3. **Sign-out does not reach the server at once** (check 11). Revocation and end-session end
   Zitadel's session. The server verifies tokens locally, though, so it accepts an access token
   until its `exp` plus 60 s of leeway (`internal/auth/auth.go:95`): up to 6 minutes with 300 s
   tokens. To lock a person out at once, a `recovery-admin` revokes the identity in Bronzeward
   ([persistence and API §10.4](../../spec/persistence-api.md#104-revocation)). List the subject in
   `auth.deniedSubjects` before that, so a database restore cannot undo it.
4. **End-session leaves the refresh token valid**. The web interface's sign-out therefore revokes
   the refresh token itself
   ([§9.7](../../spec/client-contracts.md#97-sign-out-and-cache-clearing) item 2). Never block the
   revocation endpoint.
5. **The idle and absolute limits are enforced** (check 5). The absolute limit ends renewal, not
   the last access token issued before it, which lives its full lifetime.
6. **The groups target is on the sign-in path** (check 7). With `interruptOnError`, a target that
   is down makes the token endpoint answer `500 server_error`, and no token is issued, so nobody
   signs in or renews. That is safer than a token without groups, but the target needs the same
   availability as Zitadel.
7. **Zitadel refuses a target on a loopback or private address** (`Errors.Target.DeniedURL`). It
   also refuses one whose host name does not resolve yet. Its default `HTTPClient.DenyList`
   covers every loopback and private range. The fixture replaces it to reach its own container.
   That is a test-only override: in a deployment, give the target an address the default allows,
   and start it before creating the target.
8. **The CLI's loopback redirect works on any port** (check 9). Registering
   `http://127.0.0.1/callback` once covers every port, with `devMode` on or off. Another path is
   refused.
9. **A device code lives 5 minutes, polled every 5 s** (check 1). An unapproved code answers
   `authorization_pending` until it expires (`expired_token`).

Limits of this evidence: the checks ran on one host, over plain `http` on loopback, under the
external name `localhost`. Behaviour behind TLS or under another external name was not observed.
