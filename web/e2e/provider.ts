// A synthetic OpenID provider for the specs that need a signed-in page without the test provider's
// login. It completes each authorization request at once and hands out whatever `next` returns.
// The redirect specs (wdio.conf.ts) use it with no server behind the API; the AUTH fixture cases
// (auth.conf.ts) put it in front of the fixture issuer, whose key set it serves as its own, so the
// server verifies the fixture's minted tokens under this provider's issuer. The test provider
// covers the real login flow (decision B7).
import { createHash, randomBytes } from "node:crypto";
import { createServer, type Server, type ServerResponse } from "node:http";

export const FAKE_ISSUER = "http://127.0.0.1:58445";

export interface Grant {
  access: string;
  sub: string;
  // The expires_in of every answer (300 if absent), whatever the access token's own lifetime.
  lifetime?: number;
  // Rotate: accept the latest refresh token issued under this grant and answer with a new pair,
  // for `renewed`'s access token and subject if set (AUTH-07). Without it every use is refused.
  rotate?: boolean;
  renewed?: Grant;
  // How many refresh requests to take and never answer (AUTH-11's second and third cases).
  hold?: number;
  // Issue no refresh token: a provider that does not rotate (AUTH-11's fourth case).
  bare?: boolean;
}

export interface ProviderOptions {
  issuer: string;
  pageOrigin: string;
  clientId: string;
  next: () => Grant;
  // The fixture issuer whose /jwks this serves; absent, the provider serves no key set.
  keys?: string;
  // Hand out refresh tokens that start with this (the run's canary, so the artifact scan finds
  // one); uses, uses of a rotated-out token and revocations are counted.
  refresh?: string;
}

export interface Counts {
  authorize: number;
  refresh: number;
  spent: number;
  revoke: number;
}

export interface Provider {
  server: Server;
  counts: Counts;
}

const b64url = (s: string | Buffer) => Buffer.from(s).toString("base64url");

export function startProvider(o: ProviderOptions): Promise<Provider> {
  const codes = new Map<string, { nonce: string; challenge: string; grant: Grant }>();
  const counts: Counts = { authorize: 0, refresh: 0, spent: 0, revoke: 0 };
  // The latest refresh token of each sign-in, and those rotated out.
  const latest = new Map<string, Grant>();
  const spent = new Set<string>();
  const answer = (res: ServerResponse, headers: Record<string, string>, grant: Grant, nonce?: string) => {
    const now = Math.floor(Date.now() / 1000);
    const lifetime = grant.lifetime ?? 300;
    const id = [b64url('{"alg":"none"}'), b64url(JSON.stringify({ iss: o.issuer, aud: [o.clientId], sub: grant.sub, ...(nonce !== undefined ? { nonce } : {}), iat: now, exp: now + 300 })), ""].join(".");
    const refresh = o.refresh !== undefined && grant.bare !== true ? `${o.refresh}-refresh-${randomBytes(8).toString("hex")}` : undefined;
    if (refresh !== undefined) latest.set(refresh, grant);
    res.writeHead(200, headers).end(JSON.stringify({ access_token: grant.access, expires_in: lifetime, id_token: id, ...(refresh !== undefined ? { refresh_token: refresh } : {}) }));
  };
  const server = createServer((req, res) => {
    const url = new URL(req.url ?? "/", o.issuer);
    const headers = { "Cache-Control": "no-store", "Access-Control-Allow-Origin": o.pageOrigin, "Content-Type": "application/json" };
    if (url.pathname === "/.well-known/openid-configuration") {
      return res.writeHead(200, headers).end(JSON.stringify({
        issuer: o.issuer, authorization_endpoint: `${o.issuer}/authorize`, token_endpoint: `${o.issuer}/token`,
        ...(o.keys !== undefined ? { jwks_uri: `${o.issuer}/jwks` } : {}),
        ...(o.refresh !== undefined ? { revocation_endpoint: `${o.issuer}/revoke` } : {}),
      }));
    }
    if (url.pathname === "/revoke" && req.method === "POST") {
      let body = "";
      req.on("data", (c: Buffer) => (body += c.toString()));
      req.on("end", () => {
        if (new URLSearchParams(body).get("token")?.startsWith(o.refresh ?? "\0")) counts.revoke++;
        res.writeHead(200, headers).end();
      });
      return;
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
        const refused = () => res.writeHead(400, headers).end(JSON.stringify({ error: "invalid_grant" }));
        if (form.get("grant_type") === "refresh_token") {
          counts.refresh++;
          const token = form.get("refresh_token") ?? "";
          if (spent.has(token)) counts.spent++;
          const grant = latest.get(token);
          if (grant?.rotate !== true) return refused();
          latest.delete(token);
          spent.add(token);
          if ((grant.hold ?? 0) > 0) return void grant.hold!--;
          return answer(res, headers, grant.renewed ?? grant);
        }
        const code = codes.get(form.get("code") ?? "");
        codes.delete(form.get("code") ?? "");
        if (code === undefined || b64url(createHash("sha256").update(form.get("code_verifier") ?? "").digest()) !== code.challenge) return refused();
        answer(res, headers, code.grant, code.nonce);
      });
      return;
    }
    res.writeHead(404).end();
  });
  const port = Number(new URL(o.issuer).port);
  return new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(port, "127.0.0.1", () => resolve({ server, counts }));
  });
}
