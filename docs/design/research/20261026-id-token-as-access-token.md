# ID token presented as a human access token

26 October 2026. Outcome record for [#370](https://github.com/ginsys/bronzeward/issues/370). The
server's human verifier accepts an ID token from the pinned test provider as the human. The check
chosen to refuse it is tracked by [#372](https://github.com/ginsys/bronzeward/issues/372). This
record selects no identity provider for deployments and sets no credential policy.

## Scope and decision enabled

The verifier checks signature, `iss`, `aud`, `exp`, `iat`, `nbf`, `sub`, the lifetime, the groups
claim and the deny and revocation lists (`internal/auth/auth.go:145-185`,
[persistence and API §10.1](../../spec/persistence-api.md#101-humans-oidc)). None of these tells an
access token from an ID token, and
[client contracts §9.1](../../spec/client-contracts.md#91-identity-provider-requirements) item 1
states only that a client never sends one. This record establishes what the pinned provider puts
in each token, which of the issue's four alternatives can tell them apart there, and at what cost.

Decision enabled: once the check of #372 lands, the web sign-out
([client contracts §9.7](../../spec/client-contracts.md#97-sign-out-and-cache-clearing) item 2)
can send an `id_token_hint` to the provider's end-session endpoint, which the sign-out
implementation of [#332](https://github.com/ginsys/bronzeward/issues/332) avoids because a leaked
ID token would authenticate at the server; and AUTH-08 can run in full.

## Pinned versions and inputs

- `ghcr.io/zitadel/zitadel:v4.19.5@sha256:a586456a208e0db4c633db0ba43c427c24b37846138b8b13ff16ad8e003a18d3`
  (`ZITADEL_IMAGE`, `fixtures/versions.env:26`), set up by `fixtures/dev-idp` as in the
  [test-provider record](20261022-zitadel-test-idp.md); Zitadel v4.19.5 builds on
  `github.com/zitadel/oidc/v3` v3.47.5.
- Synthetic inputs only: the human `h-viewer` of the test issuer's set, granted `bw-viewers`, with
  `fixtures/dev-idp`'s synthetic password; the CLI (Native, PKCE) and web (User Agent) clients of
  the `bronzeward` project, JWT access tokens, project role assertion on; groups added to access
  tokens by the Actions v2 target on `preaccesstoken`.
- Scope `openid offline_access urn:zitadel:iam:org:project:id:<project>:aud`, and a nonce on the
  authorization requests, as the clients send them (`internal/api/idp_test.go`).

## Reproduction

`mise run dev-db`, `mise run dev-idp`, then `mise run go-idp`, which runs
`TestIdentityProviderIDToken` (`internal/api/idtoken_test.go`) with the rest of the sign-in
self-test. It skips without the provider's variables. For each grant it decodes both tokens'
headers and claims without a signature check, asserts the header type, the exact audience and the
presence or absence of every claim in the table below (no value other than `typ` and `aud` is
kept), applies each candidate check to both, and presents both tokens to
the server's real verifier through `GET /identity`. It asserts today's behaviour, the finding
included, so a provider or verifier change fails it; #372 turns the ID token's assertion into a
refusal.

## Observed

Identical in all four grants: authorization code on the CLI client, authorization code on the web
client, refresh on the web client, device authorization on the CLI client.

| | ID token | access token |
| --- | --- | --- |
| header `typ` | `JWT` | `JWT` |
| `aud` | the CLI client, the web client, the project | the same three |
| `at_hash` | present | absent |
| `nonce` | present for code and refresh; absent for device, whose request carries none | absent |
| `azp`, `auth_time`, `amr`, `sid` | present | absent |
| `client_id` | present | present |
| `jti` | absent | present |
| `groups` | absent | present |
| verifier | accepted as `h-viewer`, Human, roles `[]` | accepted as `h-viewer`, roles `[viewer]` |

Expected by the specification: the ID token refused. Observed: accepted. The ID token has no role
only because it carries no groups claim; it still reaches every route that needs none, such as
`GET /identity` (`internal/api/routes.go:105`).

Why, from the provider's source at tag v4.19.5:

- Every grant (code, refresh, device, client credentials, JWT profile) builds its response in
  `accessTokenResponseFromSession` (`internal/api/oidc/token.go:30-57`). Both tokens are signed by
  the same signer (`:32`, `:44`, `:54`), which sets the header type `JWT` for every token
  (`internal/api/oidc/sign/signer.go:50`); no setting changes it.
- The access token's audience is `session.Audience` (`token.go:133-141`); the ID token's is the
  same `session.Audience` (`:54`, `:93`), with the client ID appended if missing
  (zitadel/oidc `pkg/oidc/token.go:182`, `:340-347`). The session's audience names every
  application of the project and the project itself, so no audience can be in one token and not
  the other.
- The ID token gets `at_hash` whenever an access token is issued with it (`token.go:105-110`) and
  `client_id` alongside `azp` (zitadel/oidc `pkg/oidc/token.go:194-195`). The access token carries
  `client_id` and `jti` (`:120-121`).
- Claims added by an action reach the access token from the `preaccesstoken` function
  (`token.go:123`) and the ID token from the `preuserinfo` function (`token.go:79`).

## Alternatives

| # | Check | Result on the pinned provider | Cost to deployments |
| ---: | :--- | :--- | :--- |
| 1 | Require `typ: at+jwt` (RFC 9068 §2.1) | refuses both tokens: both are `JWT`, fixed in the signer | unusable with this provider |
| 2 | Refuse a token carrying `nonce` or `at_hash` (OIDC Core §2, §3.1.3.6) | refuses every observed ID token (by `at_hash`; device has no `nonce`), accepts every access token | none here. Relies on the provider adding `at_hash`, which OIDC Core makes optional at the token endpoint; an ID token issued without an access token has none |
| 2b | Require `jti` (RFC 9068 §2.2) | refuses every observed ID token, accepts every access token. `client_id`, RFC 9068's other required claim, is in both and does not discriminate | a provider whose access tokens lack `jti` is refused, which RFC 9068 permits |
| 3 | A distinct access-token audience no ID token names | impossible: both tokens take the session's audience. Shown from source; not tried live, since adding an application to the `bronzeward` project would change every token the shared test provider issues | unusable with this provider |
| 4 | Provider configuration only | not a fix: the one lever is keeping role claims out of ID tokens, which leaves the no-role identity, and no server check holds it | a configuration mistake turns the finding into full role equivalence (below) |

Severity amplifier for 4: a deployment whose action adds the groups claim on the `preuserinfo`
function as well as `preaccesstoken` gives an ID token the access token's roles, from a token sent
to every relying party in the project and carried in an `id_token_hint`. Inferred from the source
above; not configured live. Zitadel's own role claim, which ID-token role assertion adds, is an
object, and the server refuses a non-array groups claim (`internal/auth/auth.go:213-221`,
`fixtures/oidc/groups/groups.go:4-5`), so pointing `groupsClaim` at it grants nothing.

## Recommendation

Alternatives 2 and 2b together, as #372 implements: the server refuses `401` a human token that
carries `nonce` or `at_hash`, or lacks `jti`. Each refuses every observed ID token alone; together
they also cover an ID token issued without an access token (no `at_hash`, still no `jti`) and a
provider that puts `jti` into ID tokens too (still `at_hash`). The provider requirement is that
access tokens carry `jti`, which the pinned provider meets with no configuration; the in-process
test issuer (`fixtures/oidc/issuer`) does not issue `jti` today and must. #372 carries the
amendments: persistence and API §10.1 adds the check to its list, client contracts §9.1 adds
`jti` to the access-token requirement and states that the server refuses an ID token.

This restores the specification's existing intent that only access tokens reach the API; it adds
no credential policy. The PoC runs one provider profile, where the check costs nothing.

## Limitations

- One provider, one version. Other providers' token formats were not measured; a provider that
  issues `typ: at+jwt` or a distinct audience could use alternative 1 or 3 instead.
- A marker claim added to access tokens only, through an Actions target, was not tried: the
  executions are instance-wide on the shared test provider.
- An ID token issued without an access token (implicit, `response_type=id_token`) was not
  exercised; the test clients do not enable it.
- Alternative 3 and the amplifier rest on the source, not on a live configuration.
