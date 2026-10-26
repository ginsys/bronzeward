// A synthetic OpenID provider for the specs that need a signed-in page but no real provider or
// server (the API answers with a redirect there). It completes each authorization request at once
// for one synthetic subject. The AUTH cases use the test provider instead (decision B7).
// Every token carries the run's canary, so the artifact scan finds any token an artifact keeps.
import { createHash, randomBytes } from "node:crypto";
import { createServer, type Server } from "node:http";

export const FAKE_ISSUER = "http://127.0.0.1:58445";

const b64url = (s: string | Buffer) => Buffer.from(s).toString("base64url");

export function startProvider(pageOrigin: string, clientId: string, canary: string): Promise<Server> {
  const codes = new Map<string, { nonce: string; challenge: string }>();
  const server = createServer((req, res) => {
    const url = new URL(req.url ?? "/", FAKE_ISSUER);
    const headers = { "Cache-Control": "no-store", "Access-Control-Allow-Origin": pageOrigin, "Content-Type": "application/json" };
    if (url.pathname === "/.well-known/openid-configuration") {
      return res.writeHead(200, headers).end(JSON.stringify({ issuer: FAKE_ISSUER, authorization_endpoint: `${FAKE_ISSUER}/authorize`, token_endpoint: `${FAKE_ISSUER}/token` }));
    }
    if (url.pathname === "/authorize") {
      const q = url.searchParams;
      if (q.get("client_id") !== clientId || q.get("redirect_uri") !== `${pageOrigin}/callback` || q.get("code_challenge_method") !== "S256") {
        return res.writeHead(400).end();
      }
      const code = randomBytes(16).toString("hex");
      codes.set(code, { nonce: q.get("nonce") ?? "", challenge: q.get("code_challenge") ?? "" });
      return res.writeHead(302, { Location: `${pageOrigin}/callback?code=${code}&state=${encodeURIComponent(q.get("state") ?? "")}` }).end();
    }
    if (url.pathname === "/token" && req.method === "POST") {
      let body = "";
      req.on("data", (c: Buffer) => (body += c.toString()));
      req.on("end", () => {
        const form = new URLSearchParams(body);
        const grant = codes.get(form.get("code") ?? "");
        codes.delete(form.get("code") ?? "");
        if (grant === undefined || b64url(createHash("sha256").update(form.get("code_verifier") ?? "").digest()) !== grant.challenge) {
          return res.writeHead(400, headers).end(JSON.stringify({ error: "invalid_grant" }));
        }
        const id = [b64url('{"alg":"none"}'), b64url(JSON.stringify({ iss: FAKE_ISSUER, aud: [clientId], sub: "h-synthetic", nonce: grant.nonce })), ""].join(".");
        res.writeHead(200, headers).end(JSON.stringify({ access_token: `${canary}-access`, expires_in: 300, id_token: id }));
      });
      return;
    }
    res.writeHead(404).end();
  });
  return new Promise((resolve) => server.listen(58445, "127.0.0.1", () => resolve(server)));
}
