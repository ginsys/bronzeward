# Hosting: TLS, routing and the web files

One origin serves both the web interface and the API
([client contracts §10.1](../../spec/client-contracts.md#101-hosting-and-origin), decision D1 (a)).
The component that terminates TLS routes `/api/v1/` and `/livez` to the server and serves every
other path from the web files. The server listens on plain HTTP, serves no files and sends no CORS
header, and the API never accepts a cookie. The reference terminator is Caddy, at the image
`CADDY_IMAGE` in [`packaging/versions.env`](../../../packaging/versions.env), pinned by digest.

Host names on this page are synthetic: `bronzeward.example.com` for the site and
`idp.example.com` for the identity provider.

## The web archive

A release's `bronzeward-web_<version>.tar.gz` holds:

| File | What it is |
|---|---|
| `dist/` | The web interface's static files. |
| `headers.json` | The response headers every page must carry ([§10.2](../../spec/client-contracts.md#102-script-and-content-policy)). |
| `Caddyfile` | The reference configuration, rendered from `headers.json`. Render it again rather than edit it. |
| `bronzeward-client.example.json` | The client-configuration file to copy and fill in (below). |
| `LICENSE-STATUS.md`, `THIRD_PARTY_NOTICES.txt` | The licence status and the notices of what the files include. |

No release is published yet ([ginsys/bronzeward#16](https://github.com/ginsys/bronzeward/issues/16)
holds the licence). To build the same archive from a checkout, run
`BW_VERSION=v0.0.0-dev mise run package`, which writes it to `dist/`.

## Caddy

The `Caddyfile` reads every deployment value from the environment, so one rendering serves every
deployment:

| Variable | Value |
|---|---|
| `BW_SITE_ADDRESS` | The site, as `https://bronzeward.example.com`. Caddy obtains and renews its certificate. |
| `BW_SERVER` | The server's `listen` address, as `127.0.0.1:8080`. |
| `BW_WEB_ROOT` | The directory holding the archive's `dist/` files. |
| `BW_ISSUER_ORIGIN` | The identity provider's issuer origin, as `https://idp.example.com`. |
| `BW_PROVIDER_ORIGINS` | The issuer's origin, plus the origins of the token and revocation endpoints its discovery document names, separated by spaces. |

```sh
tar -xzf bronzeward-web_<version>.tar.gz
cd bronzeward-web_<version>
install -m 644 bronzeward-client.example.json dist/bronzeward-client.json   # then fill it in
BW_SITE_ADDRESS=https://bronzeward.example.com BW_SERVER=127.0.0.1:8080 BW_WEB_ROOT="$PWD/dist" \
  BW_ISSUER_ORIGIN=https://idp.example.com BW_PROVIDER_ORIGINS=https://idp.example.com \
  caddy run --config Caddyfile --adapter caddyfile
```

Read the provider origins from the issuer's discovery document
(`https://idp.example.com/.well-known/openid-configuration`, members `issuer`, `token_endpoint`
and `revocation_endpoint`). Where all three share one origin, that origin is the whole list. A
missing origin makes the browser refuse sign-in or sign-out under the page's `connect-src`.

`mise run hosting` serves a built archive through this `Caddyfile` in the pinned image
([`scripts/test/hosting-test.py`](../../../scripts/test/hosting-test.py)). It checks every page's
headers against `headers.json`, with and without a `Range` header. It checks that `/livez` and
`/api/v1/` reach the server, and that a client route or a missing file answers with the
application. A deployment that runs another Caddy release, or edits the
file, runs a configuration this repository has not tested.

## Another terminator

Any TLS terminator works if it does what the `Caddyfile` does:

1. Terminate TLS for the site, with TLS 1.2 or later and a certificate the operators' browsers and
   CLI trust ([§6](../../spec/client-contracts.md#6-tls-trust)). A private CA is added per CLI
   context with `--certificate-authority`, never by turning verification off. No option turns it
   off.
2. Route `/api/v1/*` and `/livez` to the server, unchanged, and send nothing else there.
3. Serve every other path from `dist/`, answering a path with no file with `dist/index.html`.
4. Send every header in `headers.json` on every response from the web files, with `{issuerOrigin}`
   and `{providerOrigins}` replaced as above. Serve whole files only: ignore a `Range` header, as the
   `Caddyfile` does, because Caddy sends a refused range's answer without these headers.
5. Add no cookie, CORS header or authentication of its own.

## The client-configuration file

`bronzeward-client.json` at the web root names the identity provider for both clients
([§5](../../spec/client-contracts.md#5-endpoint-discovery-and-context-selection) item 4). It holds
no secret: both clients are public, and the file is read before anyone signs in.

```json
{
  "issuer": "https://idp.example.com",
  "audience": "<the server's auth.oidc.audience>",
  "cliClientId": "<the CLI client's ID>",
  "webClientId": "<the web client's ID>",
  "scopes": "<optional: scopes requested after openid offline_access>"
}
```

Leave `scopes` out when the provider needs none, and never list `openid` or `offline_access`
in it. The web interface reads the file from its own origin. The CLI imports it with
`bronzewardctl context add <name> --from https://bronzeward.example.com`, which stores the CLI
client's ID. The [identity-provider page](identity-provider.md) gives Zitadel's values.

The issuer must be `https`, or `http` to a loopback address. The server's own rule for its
issuer also allows the hosts listed in `auth.oidc.plainHTTPHosts`
([`examples/bronzeward.yaml`](../../../examples/bronzeward.yaml)). The clients have no such list.

## Check it

```sh
curl -sI https://bronzeward.example.com/ | grep -i content-security-policy   # the header set
curl -s -o /dev/null -w '%{http_code}\n' https://bronzeward.example.com/livez  # 204
curl -s https://bronzeward.example.com/bronzeward-client.json                 # the file above
```
