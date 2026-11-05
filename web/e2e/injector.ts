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
  // A redirect for this path, after any hold, which the page refuses: a transport failure as the
  // page sees it (client contracts §6 item 7). A connection closed here closes the page's too (the
  // static host aborts its answer), so the page meets the same failure.
  redirect?: string;
  // The answer to this path held this long, in milliseconds; with a query, only to that query.
  hold?: { path: string; ms: number; query?: string };
  // The answer to this path (or these) held until the case releases it (POST /release on the
  // control server), so a case orders a late answer after what it asserted first, whatever the
  // runner's timing.
  held?: string | string[];
  // As `held`, for a write that takes a step: held after the server answered it and that answer
  // was logged, so the case knows the write committed before it acts.
  heldAnswered?: string;
  // A 404 not-found problem for this path, instead of the server's answer.
  missing?: string;
  // A 403 identity-revoked problem for this path, instead of the server's answer.
  revoked?: string;
  // A 409 recovery-mode-active problem without a scope member for these paths, instead of the
  // server's answer: a process restarted for recovery under the same epoch, its entry not yet made.
  processRefused?: string[];
  // W3 and the read-only items: the body of a successful answer to each path, with the server's
  // status and headers, or, for an item the deployment does not hold, its 404 answered 200: the
  // server has authenticated the read, and the body is a read the deployment cannot produce without
  // a Talos node or a publication. A refusal is never replaced.
  bodies?: Record<string, unknown>;
  // The write cases (#338): each write to `path` takes the next step, and passes once none is left.
  steps?: { path: string; take: Step[] };
}

// drop: the request read and its connection closed, nothing forwarded; commit-drop: forwarded, the
// server's answer dropped; commit-500: forwarded, its answer replaced by 500 internal-error; 503:
// answered 503 dependency-unavailable, nothing forwarded; redirect: answered 302 to REDIRECTED; epoch: a new epoch
// committed, then forwarded; commit-drop-epoch: as commit-drop, then a new epoch. For the draft
// cases (#339): pass-drop: forwarded, and the client's connection closed at once while the server
// runs on (AMB-02); commit-drop-retire: as commit-drop, then the digest key's versions retired
// (AMB-06). For REC-01's scope variant (#627): scope-refused: answered 409 recovery-mode-active with
// a `scope` member, nothing forwarded.
export type Step = "drop" | "commit-drop" | "commit-500" | "503" | "redirect" | "epoch" | "commit-drop-epoch" | "pass-drop" | "commit-drop-retire" | "scope-refused";

// The scope the injected scope refusal names: a synthetic ID, no machine of any deployment.
export const INJECTED_SCOPE = "mch_synthetic_scope_627";

export interface Injector {
  server: Server;
  faults: Faults;
  // Every API path asked, in order, and each server answer passed on: path, query and status.
  seen: string[];
  answered: string[];
  // The path of each answer the `held` fault holds now, so a case acts only once one is held.
  holding: string[];
  // Lets every held answer go on; resolves once each has been sent.
  release: () => Promise<void>;
  // Every write received, as "<method> <route> <Idempotency-Key> <Bronzeward-Expected-Epoch>".
  writes: string[];
  // Commits a new epoch in the deployment the writes go to, as a recovery entry does.
  epoch: () => void;
  // The requests that reached REDIRECTED: a redirect the page followed.
  redirected: number;
  // Retires the versions of the digest key of the deployment the writes go to (AMB-06).
  retire: () => Promise<void>;
}

// Where every injected redirect points: a path on the page's own origin, which the page's CSP admits
// and the static host passes to this injector, so only the page's own refusal keeps it unreached.
export const REDIRECTED = "/api/v1/__redirected";

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
    faults: {}, seen: [], answered: [], holding: [], writes: [], redirected: 0, server: undefined as never, epoch: () => {}, retire: async () => {},
    release: async () => {
      inj.holding = [];
      const now = waiting.splice(0);
      for (const w of now) w.go();
      await Promise.all(now.map((w) => w.sent));
    },
  };
  inj.server = createServer((req, res) => {
    const { pathname: path, search } = new URL(req.url ?? "/", upstream);
    if (path === REDIRECTED) {
      inj.redirected++;
      return void res.writeHead(200, { "Content-Type": "application/json" }).end("{}");
    }
    if (path.startsWith("/api/v1/")) inj.seen.push(path.slice("/api/v1".length));
    const f = inj.faults;
    const route = path.slice("/api/v1".length);
    const problem = (status: number, code: string) => res.writeHead(status, { "Content-Type": "application/problem+json" })
      .end(JSON.stringify({ type: `urn:bronzeward:problem:${code}`, title: code, status, detail: "injected", instance: "req-injected" }));
    const forwarded = () => Object.fromEntries(Object.entries(req.headers).filter(([k]) => k !== "host" && k !== "connection")) as Record<string, string>;
    const passed = (up: Response) => Object.fromEntries([...up.headers].filter(([k]) => !["content-length", "content-encoding", "transfer-encoding", "connection"].includes(k)));
    // Holds this answer until the case releases it (POST /release), and logs that it is held.
    const hold = () => {
      inj.holding.push(route);
      return new Promise<void>((go) => waiting.push({ go, sent: new Promise((done) => res.once("close", () => done())) }));
    };
    const answerStep = async (step: Step) => {
      const sent = new Uint8Array(await body(req));
      if (step === "drop") return req.socket.destroy();
      if (step === "503") return problem(503, "dependency-unavailable");
      if (step === "scope-refused") {
        return res.writeHead(409, { "Content-Type": "application/problem+json" })
          .end(JSON.stringify({ type: "urn:bronzeward:problem:recovery-mode-active", title: "Recovery mode active", status: 409, detail: "injected", instance: "req-injected", scope: INJECTED_SCOPE }));
      }
      if (step === "redirect") return res.writeHead(302, { Location: REDIRECTED }).end();
      if (step === "epoch") inj.epoch();
      if (step === "pass-drop") {
        // The server keeps running the request after the client lost its connection.
        void fetch((f.upstream ?? upstream) + (req.url ?? "/"), { method: req.method!, headers: forwarded(), body: sent, redirect: "manual" })
          .then(async (up) => (await up.arrayBuffer(), inj.answered.push(`${route}${search} ${up.status}`)), (e: unknown) => console.error(`injector pass-drop: ${String(e)}`));
        return req.socket.destroy();
      }
      const up = await fetch((f.upstream ?? upstream) + (req.url ?? "/"), { method: req.method!, headers: forwarded(), body: sent, redirect: "manual" });
      const out = Buffer.from(await up.arrayBuffer());
      inj.answered.push(`${route}${search} ${up.status}`);
      if (f.heldAnswered === route) await hold();
      if (step === "commit-500") return problem(500, "internal-error");
      if (step === "epoch") return res.writeHead(up.status, passed(up)).end(out);
      req.socket.destroy();
      if (step === "commit-drop-epoch") inj.epoch();
      if (step === "commit-drop-retire") await inj.retire();
    };
    // A write is logged as it arrives, so a case reads it while its answer is held.
    const write = req.method !== "GET" && req.method !== "HEAD";
    if (write) inj.writes.push(`${req.method} ${route} ${req.headers["idempotency-key"]} ${req.headers["bronzeward-expected-epoch"]}`);
    void (async () => {
      if (f.hold?.path === route && (f.hold.query ?? search) === search) await new Promise((r) => setTimeout(r, f.hold!.ms));
      if ([f.held ?? []].flat().includes(route)) await hold();
      const step = write && f.steps?.path === route ? f.steps.take.shift() : undefined;
      // A step that cannot be taken (the epoch refused) closes the connection, and is logged.
      if (step !== undefined) return void answerStep(step).catch((e: unknown) => (console.error(`injector step ${step}: ${String(e)}`), req.socket.destroy()));
      if (f.redirect === route) return void res.writeHead(302, { Location: REDIRECTED }).end();
      if (f.missing === route) {
        const problem = { type: "urn:bronzeward:problem:not-found", title: "Not found", status: 404, detail: "injected", instance: "req-injected" };
        return void res.writeHead(404, { "Content-Type": "application/problem+json" }).end(JSON.stringify(problem));
      }
      if (f.revoked === route) {
        const problem = { type: "urn:bronzeward:problem:identity-revoked", title: "Identity revoked", status: 403, detail: "injected", instance: "req-injected" };
        return void res.writeHead(403, { "Content-Type": "application/problem+json" }).end(JSON.stringify(problem));
      }
      if (f.processRefused?.includes(route)) {
        const problem = { type: "urn:bronzeward:problem:recovery-mode-active", title: "Recovery mode active", status: 409, detail: "injected", instance: "req-injected" };
        return void res.writeHead(409, { "Content-Type": "application/problem+json" }).end(JSON.stringify(problem));
      }
      const headers = Object.fromEntries(Object.entries(req.headers).filter(([k]) => k !== "host" && k !== "connection")) as Record<string, string>;
      const sent = req.method === "GET" || req.method === "HEAD" ? null : new Uint8Array(await body(req));
      const up = await fetch((f.upstream ?? upstream) + (req.url ?? "/"), { method: req.method ?? "GET", headers, body: sent, redirect: "manual" });
      const back = Object.fromEntries([...up.headers].filter(([k]) => !["content-length", "content-encoding", "transfer-encoding", "connection"].includes(k)));
      // The import cases (#340): an operation's event stream passes on as it arrives, since the
      // server ends it only at a terminal state; the page closing it cancels the server's.
      if (up.ok && up.body !== null && (up.headers.get("content-type") ?? "").startsWith("text/event-stream")) {
        const reader = up.body.getReader();
        res.once("close", () => void reader.cancel().catch(() => {}));
        res.writeHead(up.status, back);
        for (let r = await reader.read().catch(() => undefined); r !== undefined && !r.done; r = await reader.read().catch(() => undefined)) res.write(r.value);
        inj.answered.push(`${route}${search} ${up.status}`);
        return void res.end();
      }
      let out = Buffer.from(await up.arrayBuffer());
      if (route === "/server" && f.revision !== undefined && up.ok) {
        out = Buffer.from(JSON.stringify({ ...JSON.parse(out.toString()), apiRevision: f.revision }));
      }
      let status = up.status;
      const stand = f.bodies?.[route];
      if (stand !== undefined && (up.ok || up.status === 404)) {
        out = Buffer.from(JSON.stringify(stand));
        if (!up.ok) [status, back["content-type"]] = [200, "application/json"];
      }
      if (f.deprecate === route) back["deprecation"] = "@1767225600";
      inj.answered.push(`${route}${search} ${up.status}`);
      res.writeHead(status, back).end(out);
    })().catch(() => req.socket.destroy());
  });
  return new Promise((resolve, reject) => {
    inj.server.once("error", reject);
    inj.server.listen(port, "127.0.0.1", () => resolve(inj));
  });
}
