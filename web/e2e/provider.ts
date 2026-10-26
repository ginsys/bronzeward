// A synthetic OpenID provider for the specs that need a signed-in page without the test provider's
// login. It completes each authorization request at once and hands out whatever `next` returns.
// The redirect specs (wdio.conf.ts) use it with no server behind the API; the AUTH fixture cases
// (auth.conf.ts) put it in front of the fixture issuer, whose key set it serves as its own, so the
// server verifies the fixture's minted tokens under this provider's issuer. The test provider
// covers the real login flow (decision B7).
import { createHash, randomBytes } from "node:crypto";
import { createServer, type Server } from "node:http";

export const FAKE_ISSUER = "http://127.0.0.1:58445";

export interface Grant {
  access: string;
  sub: string;
}

export interface ProviderOptions {
  issuer: string;
  pageOrigin: string;
  clientId: string;
  next: () => Grant;
  // The fixture issuer whose /jwks this serves; absent, the provider serves no key set.
  keys?: string;
  // Hand out a refresh token, and refuse its use, which is counted.
  refresh?: boolean;
}

export interface Provider {
  server: Server;
  counts: { authorize: number; refresh: number };
}

const b64url = (s: string | Buffer) => Buffer.from(s).toString("base64url");

export function startProvider(o: ProviderOptions): Promise<Provider> {
  const codes = new Map<string, { nonce: string; challenge: string; grant: Grant }>();
  const counts = { authorize: 0, refresh: 0 };
  const server = createServer((req, res) => {
    const url = new URL(req.url ?? "/", o.issuer);
    const headers = { "Cache-Control": "no-store", "Access-Control-Allow-Origin": o.pageOrigin, "Content-Type": "application/json" };
    if (url.pathname === "/.well-known/openid-configuration") {
      return res.writeHead(200, headers).end(JSON.stringify({
        issuer: o.issuer, authorization_endpoint: `${o.issuer}/authorize`, token_endpoint: `${o.issuer}/token`,
        ...(o.keys !== undefined ? { jwks_uri: `${o.issuer}/jwks` } : {}),
      }));
    }
    if (url.pathname === "/jwks" && o.keys !== undefined) {
      void fetch(`${o.keys}/jwks`).then(async (r) => res.writeHead(r.status, headers).end(await r.text()), () => res.writeHead(502).end());
      return;
    }
    if (url.pathname === "/authorize") {
      counts.authorize++;
      const q = url.searchParams;
      if (q.get("client_id") !== o.clientId || q.get("redirect_uri") !== `${o.pageOrigin}/callback` || q.get("code_challenge_method") !== "S256") {
        return res.writeHead(400).end();
      }
      const code = randomBytes(16).toString("hex");
      codes.set(code, { nonce: q.get("nonce") ?? "", challenge: q.get("code_challenge") ?? "", grant: o.next() });
      return res.writeHead(302, { Location: `${o.pageOrigin}/callback?code=${code}&state=${encodeURIComponent(q.get("state") ?? "")}` }).end();
    }
    if (url.pathname === "/token" && req.method === "POST") {
      let body = "";
      req.on("data", (c: Buffer) => (body += c.toString()));
      req.on("end", () => {
        const form = new URLSearchParams(body);
        if (form.get("grant_type") === "refresh_token") {
          counts.refresh++;
          return res.writeHead(400, headers).end(JSON.stringify({ error: "invalid_grant" }));
        }
        const code = codes.get(form.get("code") ?? "");
        codes.delete(form.get("code") ?? "");
        if (code === undefined || b64url(createHash("sha256").update(form.get("code_verifier") ?? "").digest()) !== code.challenge) {
          return res.writeHead(400, headers).end(JSON.stringify({ error: "invalid_grant" }));
        }
        const id = [b64url('{"alg":"none"}'), b64url(JSON.stringify({ iss: o.issuer, aud: [o.clientId], sub: code.grant.sub, nonce: code.nonce })), ""].join(".");
        res.writeHead(200, headers).end(JSON.stringify({
          access_token: code.grant.access, expires_in: 300, id_token: id,
          ...(o.refresh === true ? { refresh_token: randomBytes(16).toString("hex") } : {}),
        }));
      });
      return;
    }
    res.writeHead(404).end();
  });
  const port = Number(new URL(o.issuer).port);
  return new Promise((resolve) => server.listen(port, "127.0.0.1", () => resolve({ server, counts })));
}
