// The injector between client and server of client contracts §12, for the shell's cases: it passes
// every request to the server and applies the faults a spec sets through the control routes.
import { createServer, type IncomingMessage, type Server } from "node:http";

export interface Faults {
  // REC-01: every request goes to this server instead, a recovery start the control route made.
  upstream?: string;
  // COMP-01: GET /server's apiRevision, rewritten.
  revision?: number;
  // COMP-02: a Deprecation header on the answers to this path.
  deprecate?: string;
  // A redirect for this path, which the page refuses: a transport failure as the page sees it
  // (client contracts §6 item 7). A connection closed here would reach the page as the static
  // host's 502.
  redirect?: string;
  // The answer to this path held this long, in milliseconds.
  hold?: { path: string; ms: number };
  // The answer to this path held until the case releases it (POST /release on the control server),
  // so a case orders a late answer after what it asserted first, whatever the runner's timing.
  held?: string;
  // A 404 not-found problem for this path, instead of the server's answer.
  missing?: string;
  // A 403 identity-revoked problem for this path, instead of the server's answer.
  revoked?: string;
}

export interface Injector {
  server: Server;
  faults: Faults;
  // Every API path asked, in order.
  seen: string[];
  // Lets every held answer go on; resolves once each has been sent.
  release: () => Promise<void>;
}

const body = (req: IncomingMessage) =>
  new Promise<Buffer>((resolve, reject) => {
    const chunks: Buffer[] = [];
    req.on("data", (c: Buffer) => chunks.push(c));
    req.on("end", () => resolve(Buffer.concat(chunks)));
    req.on("error", reject);
  });

export function startInjector(upstream: string, port: number): Promise<Injector> {
  // Each held answer: its release, and its having been sent.
  const waiting: { go: () => void; sent: Promise<void> }[] = [];
  const inj: Injector = {
    faults: {}, seen: [], server: undefined as never,
    release: async () => {
      const now = waiting.splice(0);
      for (const w of now) w.go();
      await Promise.all(now.map((w) => w.sent));
    },
  };
  inj.server = createServer((req, res) => {
    const path = new URL(req.url ?? "/", upstream).pathname;
    if (path.startsWith("/api/v1/")) inj.seen.push(path.slice("/api/v1".length));
    const f = inj.faults;
    const route = path.slice("/api/v1".length);
    if (f.redirect === route) return void res.writeHead(302, { Location: "http://127.0.0.1:9/" }).end();
    void (async () => {
      if (f.hold?.path === route) await new Promise((r) => setTimeout(r, f.hold!.ms));
      if (f.held === route) {
        await new Promise<void>((go) => waiting.push({ go, sent: new Promise((done) => res.once("close", () => done())) }));
      }
      if (f.missing === route) {
        const problem = { type: "urn:bronzeward:problem:not-found", title: "Not found", status: 404, detail: "injected", instance: "req-injected" };
        return void res.writeHead(404, { "Content-Type": "application/problem+json" }).end(JSON.stringify(problem));
      }
      if (f.revoked === route) {
        const problem = { type: "urn:bronzeward:problem:identity-revoked", title: "Identity revoked", status: 403, detail: "injected", instance: "req-injected" };
        return void res.writeHead(403, { "Content-Type": "application/problem+json" }).end(JSON.stringify(problem));
      }
      const headers = Object.fromEntries(Object.entries(req.headers).filter(([k]) => k !== "host" && k !== "connection")) as Record<string, string>;
      const sent = req.method === "GET" || req.method === "HEAD" ? null : new Uint8Array(await body(req));
      const up = await fetch((f.upstream ?? upstream) + (req.url ?? "/"), { method: req.method ?? "GET", headers, body: sent, redirect: "manual" });
      let out = Buffer.from(await up.arrayBuffer());
      const back = Object.fromEntries([...up.headers].filter(([k]) => !["content-length", "content-encoding", "transfer-encoding", "connection"].includes(k)));
      if (route === "/server" && f.revision !== undefined && up.ok) {
        out = Buffer.from(JSON.stringify({ ...JSON.parse(out.toString()), apiRevision: f.revision }));
      }
      if (f.deprecate === route) back["deprecation"] = "@1767225600";
      res.writeHead(up.status, back).end(out);
    })().catch(() => req.socket.destroy());
  });
  return new Promise((resolve, reject) => {
    inj.server.once("error", reject);
    inj.server.listen(port, "127.0.0.1", () => resolve(inj));
  });
}
