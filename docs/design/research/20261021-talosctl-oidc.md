# Talos OIDC operator access and credential lifecycle

21 October 2026. Research for [#162](https://github.com/ginsys/bronzeward/issues/162).
The disposable fixture completed successfully. The recommendation is to defer
the optional operator companion and keep the current executor credential model.
No deployment or credential-policy decision is made by this document.

## Scope and decision enabled

Evaluate `talosctl-oidc` as a separate direct operator-access companion, and
native Talos certificate issuance as a separate future service-credential
mechanism. Neither replaces Bronzeward application authentication, approved
artifact dispatch, provider custody or independent emergency access.

The distinction matters: after receiving a certificate, an operator calls Talos
directly. Bronzeward cannot authorize, fence or attribute those commands through
its operation journal. Administrative changes enter the existing drift/recovery
process. Issuing a short-lived certificate is not evidence that all access ends
at that certificate's expiry.

## Pinned sources and fixture

The upstream source is
[`d3cd122341537d3235bd5dfe7ced2171ec765227`](https://github.com/qjoly/talosctl-oidc/tree/d3cd122341537d3235bd5dfe7ced2171ec765227),
unchanged since the issue's earlier source review. Its module uses Go 1.26.8 and
Talos machinery 1.13.5. The local binary was built with Go 1.27.1; build metadata
identifies that exact pristine source. The original RPC probe uses Bronzeward's
machinery 1.13.6. The disposable node and CLI are Talos 1.13.6, Kubernetes 1.36.2;
Keycloak 26.8.1 provides synthetic users, groups and a dedicated public OIDC
client using authorization code and S256 PKCE. The fixture pins container
digests rather than relying on a moving tag.

Keycloak is only an investigation dependency. It does not select a product IdP.
The IdP listens on loopback HTTP; the exchange service uses a separate TLS CA
that the CLI explicitly trusts. HOME, XDG configuration and D-Bus are isolated,
so the test cannot write the host keyring. All generated credentials and raw
logs remain private run state. Evidence exports contain certificate metadata,
sanitized commands, exit statuses, timings and audit projections, never keys or
usable tokens. The independent emergency credential is created with the cluster
and is not obtained through the exchange service.

## Source findings

| Boundary | Finding at the pinned source |
| --- | --- |
| Talos issuer | [`pkg/talosapi/talosapi.go`](https://github.com/qjoly/talosctl-oidc/blob/d3cd122341537d3235bd5dfe7ced2171ec765227/pkg/talosapi/talosapi.go) reads a privileged talosconfig and creates a fresh Talos client for each exchange. `GenerateClientConfiguration` signs through Talos; the exchange needs no Talos CA private key. File rereading supports rotation but supplies no provider-version or attempt binding. |
| Default roles | [`pkg/config/config.go`](https://github.com/qjoly/talosctl-oidc/blob/d3cd122341537d3235bd5dfe7ced2171ec765227/pkg/config/config.go) defaults to `os:admin` when neither roles nor RBAC rules exist. With rules and empty fallback roles, unmatched identities reach the exchange's 403 path. The fixture explicitly maps reader/admin groups and leaves fallback empty. |
| JWT validation | [`pkg/oidc/jwt.go`](https://github.com/qjoly/talosctl-oidc/blob/d3cd122341537d3235bd5dfe7ced2171ec765227/pkg/oidc/jwt.go) validates signature, audience, expiry and optional not-before. The exchange passes no custom algorithm list, selecting the asymmetric default. HMAC implementation exists but is outside that default path. The validator does not require nonempty `sub`, require/check `iat`, or bound `exp - iat`; it trims one trailing issuer slash. These differ from PA §10.1 and prevent substituting this validator for Bronzeward authentication. This is source evidence, not a live malformed-token test. |
| Tokens and renewal | [`cmd/login.go`](https://github.com/qjoly/talosctl-oidc/blob/d3cd122341537d3235bd5dfe7ced2171ec765227/cmd/login.go) reuses a cached ID token until its stored expiry, derives that expiry from the token response's access-token lifetime, tries refresh, then interactive authentication. Certificate renewal uses a five-minute margin. The fixture's 45-second certificate therefore exercises renewal on the next invocation. |
| Local storage | [`pkg/keychain/keychain.go`](https://github.com/qjoly/talosctl-oidc/blob/d3cd122341537d3235bd5dfe7ced2171ec765227/pkg/keychain/keychain.go) falls back from the keyring to a mode-0600 JSON file and reads both sources. The CLI also writes certificates and keys to talosconfig. This can suit an explicitly accepted operator workstation model; it is incompatible with the executor's current memory-only custody contract. |
| Issuer outage | [`pkg/oidc/jwks_cache.go`](https://github.com/qjoly/talosctl-oidc/blob/d3cd122341537d3235bd5dfe7ced2171ec765227/pkg/oidc/jwks_cache.go) returns stale keys when refresh fails, with no additional stale-age bound. [`pkg/server/server.go`](https://github.com/qjoly/talosctl-oidc/blob/d3cd122341537d3235bd5dfe7ced2171ec765227/pkg/server/server.go) initializes discovery lazily and caches keys for five minutes. A restart loses this cache. Token expiry still applies; a stale-key fallback is not proof of immediate issuer-side revocation. |
| Audit | The exchange logs authentication and issuance, including subject, issuer, email, roles, request ID and certificate fingerprint. An unmatched role emits authentication success followed by HTTP 403 without a dedicated role-denial audit event. [`pkg/audit/audit.go`](https://github.com/qjoly/talosctl-oidc/blob/d3cd122341537d3235bd5dfe7ced2171ec765227/pkg/audit/audit.go) writes best-effort JSON and ignores write errors. This is neither durable Bronzeward operation accounting nor a tamper-evident log. |

Talos 1.13.6's `GenerateClientConfiguration` method requires `os:admin` and its
handler accepts a positive requested lifetime without imposing a maximum. The
role injector uses the authenticated TLS peer's roles; certificate validity is
checked at connection establishment rather than explicitly on each RPC. These
are source findings; the separate live expiry and remint results follow.

## Live observations

The third chain ran from `3ddf1333fee7f54eab70fb0b51bd28928b7525ef`
with the versions above, from 01:02:31Z to 01:11:55Z on 21 October, completing
in 564.516 seconds. Its [events](../../../experiments/talosctl-oidc/evidence/run3/events.json)
record 81 events and all 30 command exits: 25 expected zero, five expected
nonzero, all matched. HTTP and certificate assertions also passed; no case was
skipped. Scoped teardown removed the owned containers/network, preserved all
pre-existing container IDs, and verified both source checkouts and fixture
hashes stayed unchanged. This is one successful chain, not repeated-run or
production qualification. [Retention metadata](../../../experiments/talosctl-oidc/evidence/retention.json)
binds all exported files to their hashes; private credentials and raw logs are
excluded from publication.

| Case | Expected and observed behavior |
| --- | --- |
| Login and renewal | The real CLI completed authorization-code/S256-PKCE login, stored the token cache with mode 0600, and obtained an `os:reader` certificate. `Version` succeeded. A second invocation reused the ID token and issued a different certificate fingerprint. This proves certificate renewal; automatic ID-token refresh after natural token expiry was not exercised. |
| Role denial | The reader's attempt to mint an administrator certificate returned `PermissionDenied` and created no credential file. An authenticated identity with no mapped group was refused by the CLI. |
| Group removal | Within 0.189 seconds of removal, cached CLI renewal and direct exchange still succeeded; a fresh login produced no groups and exchange returned 403. The cached token's original group claim remained valid. This is a sampled removal response, not a measured upper revocation bound. |
| Account disablement | Cached-token exchange still returned 200; refresh returned 400 within 0.043 seconds; the already issued certificate still completed `Version`. The imported user had a refresh token. No immediate revocation of existing tokens or certificates was observed. |
| Certificate expiry | A 45-second reader certificate expired at 01:05:35Z. At 01:05:37.003Z, `Version` still succeeded on the held connection, with TCP dial count unchanged at one. A new connection returned `Unavailable`. The successful held RPC and later emergency probes distinguish this from an unavailable node. This is one post-expiry sample, not an unlimited-duration test. |
| Administrator reminting | A 45-second administrator certificate requested and received a one-hour administrator certificate. At 01:06:24Z the original failed on a new connection, while the reminted credential succeeded; its recorded expiry was 02:05:37Z. Short issuer TTL is therefore not a hard authority-duration bound for `os:admin`. |
| Issuance versus commands | Direct `Version` left the exchange audit at five rows before and after. The final audit had 32 rows and 13 issuance events, each containing subject, issuer, email, request ID, roles and certificate fingerprint. These identify issuance, not subsequent direct commands or Bronzeward approval. |
| Issuer and exchange outages | Issuance returned 200 immediately after stopping the IdP and again after 306.006 seconds, beyond the five-minute key-cache interval. After stopping and restarting the exchange with the IdP still down, the same token returned 401. Restoring the IdP restored issuance (200). The independent emergency credential completed `Version` during the IdP outage, while the exchange was stopped, and after the cold restart. |
| Talos outage | Pausing the sole disposable node caused emergency `Version` to return `DeadlineExceeded` after five seconds; the exchange request reached its eight-second client timeout. Unpausing restored emergency access and exchange issuance. An independent credential cannot make an unavailable node respond. A timeout does not prove that no issuance request could have taken effect. |

The tests use ordinary session scopes (`openid,profile,email`) and 15-minute
tokens. Cached-token acceptance was sampled before token expiry; there is no
claim that expired tokens remain valid. This fixture measures account disablement,
not account deletion, session logout, signing-key rotation or every IdP's behavior.

Two earlier attempts remain failures with their original revisions and scoped
teardown results: [first attempt](../../../experiments/talosctl-oidc/evidence/run1/events.json)
and [second attempt](../../../experiments/talosctl-oidc/evidence/run2/events.json).

- `faae408` stopped at the first login after 45 seconds (exit 124). Keycloak logged
  `cookie_not_found`. Keycloak 26.8.1 treats loopback as a secure context and sets
  `Secure` cookies, whereas Python's default cookie jar does not send them over
  HTTP. The helper repair permits them only on the exact test IdP origin and
  refuses cookies on the callback or another port. Four focused checks reproduce
  the old failure and verify the boundary.
- `aaceff9` completed the browser callback, then the token endpoint returned 400:
  the CLI's default `offline_access` scope was not allowed by the imported realm.
  The final profile requests session scopes explicitly; it does not grant offline
  access. All later cases in these two attempts were unexecuted, not passes.

The cookie explanation is grounded in Keycloak's pinned
[secure-context resolver](https://github.com/keycloak/keycloak/blob/26.8.1/services/src/main/java/org/keycloak/utils/SecureContextResolver.java)
and [cookie provider](https://github.com/keycloak/keycloak/blob/26.8.1/services/src/main/java/org/keycloak/cookie/DefaultCookieProvider.java),
the failed server event, and the successful follow-up browser metadata. These
are fixture setup limitations, not changes to upstream or Bronzeward policy.

## Executor compatibility

The [Talos-access contract](../../spec/persistence-api.md#33-talos-access) and
[execution/recovery contract](../../spec/execution-recovery.md) remain unchanged.
The following comparison is source/design analysis; no OpenBao integration was
implemented or tested by this investigation.

| Current requirement | Why the upstream issuer/CLI is insufficient |
| --- | --- |
| Cluster-derived provider path | `internal/provider/access.go` derives `access/talos/<cluster>` and reads the latest OpenBao KV value. The upstream issuer accepts a filesystem path and carries no equivalent cluster/provider authority boundary. |
| Version identity | Bronzeward records path, version and `created_time`; the last field distinguishes reused version numbers after metadata deletion or restore. A certificate fingerprint or filesystem timestamp cannot replace that identity without a new contract. |
| Observation/attempt binding | `internal/api/dispatch.go` reads one credential for evidence and the corresponding dispatch attempt, preserving it across that pair. A new certificate on each API call would break that association unless issuance is explicitly bound and recorded. |
| Retry and rotation | A retry reads afresh, compares the access identity, and obtains new evidence when it changes. A CLI auto-renewal loop does not participate in the transaction or prove that an older request is accounted for. |
| Memory-only custody | `internal/talos/talos.go` accepts credential bytes and selects only CA/certificate/key plus the independently chosen endpoint. It does not use talosconfig paths or routing metadata. Upstream file/keyring caches and filesystem issuer input cannot enter this path unchanged. |
| Approval and authority | Machine-configuration reads require `os:admin`; the same credential can mutate Talos. Short expiry does not enforce Bronzeward approval, read-only client restrictions or ownership. Issuance must not become a second dispatch owner. |
| Outage/recovery | A new issuance dependency needs a defined failure state, expiration during an evidence/attempt window, retry behavior, restore handling, issuer rotation and independent recovery. Passing an operator login does not qualify those properties. |

Native issuance may reduce routine exposure compared with long-lived service
credentials, but selecting it requires a separately refined lifecycle
specification. The issuance authority itself remains powerful; a certificate
can outlive the issuer connection and an administrator can request another
certificate. None of these changes is adopted here.

## Security refresh

Upstream [nightly security run 39393624254](https://github.com/qjoly/talosctl-oidc/actions/runs/39393624254)
on 20 October used the same source pin and Go 1.26.8. Its govulncheck 1.8.0 job
failed with eleven reachable standard-library findings; two imported-package
and three module findings were not called. Three fuzz jobs passed. The scanner
reported fixes in Go 1.26.9 for those standard-library findings. This refresh
supersedes neither the historical October 11 scan nor the exact local build
identity. It does not establish deployment exploitability or clear the local
Go 1.27.1 build; no fresh local vulnerability scan is claimed.

## Alternatives and recommendation

Defer adoption of the companion until there is an explicit direct-access use
case and an accepted revocation, workstation-custody and audit model. The test
demonstrates useful reader-first login and default-deny role mapping, but also
cached-token access after removal, held connections past expiry, administrator
reminting and outage-dependent behavior. Reject unchanged use as Bronzeward
application authentication, executor integration or a hard administrative-access
duration boundary. This is a research recommendation, not a new policy.

1. Retain existing operator talosconfigs: no new identity/exchange availability
   dependency, but credential distribution, local custody and rotation remain
   operational responsibilities.
2. Deploy the companion separately: offers centralized login and group-based
   issuance, while direct operations bypass Bronzeward. It adds a privileged
   issuer, IdP/JWKS behavior, workstation caching, audit and renewal failure
   modes. Default-deny groups and reader-first access narrow the scope; they do
   not solve administrative revocation.
3. Use native issuance for service credentials: potentially useful mechanism,
   separately specified against provider identities, evidence/attempt binding
   and memory-only use. No direct upstream integration is demonstrated.

The upstream MIT licence is a pinned source observation, not adoption or a
licence decision for Bronzeward. Product/framework/IdP choices and deployment
remain outside this research. Preserve [#16](https://github.com/ginsys/bronzeward/issues/16).

## Follow-ups and limits

The separate [operator-access decision](https://github.com/ginsys/bronzeward/issues/265)
owns whether to offer this companion, the acceptable access-removal model and
any resulting deployment/qualification work. The separate
[service-credential decision](https://github.com/ginsys/bronzeward/issues/266)
owns whether to change the existing provider model and, if selected, the
required lifecycle specification and implementation sequence. Both are tracked
in milestone 05 and depend natively on assessed #162 evidence. Neither blocks
Bronzeward CLI/web login or selects an IdP, framework or credential policy.

This experiment used one Linux amd64 Docker node, synthetic identities and a
single Keycloak realm. It did not qualify HA, production TLS/ingress, another
IdP, device flow, OS keyrings, offline tokens, the background watch loop, natural
ID-token expiry/refresh success, signing-key rotation, account deletion, durable
audit under storage failure, or executor integration. The upstream security
scan is refreshed evidence, not a local vulnerability clearance. No live
upgrade, lifecycle operation or production service was deployed.

Retained projections passed exact synthetic-value, private-key-marker and JWT
checks with positive controls: four distinct known values in each failed run,
34 in the successful run, zero matches. Raw private state intentionally contains
synthetic credentials and is not publishable. See each run's `leak-scan.json`
for scope and limits; this is evidence hygiene, not proof that upstream never
logs secrets under other configurations.
