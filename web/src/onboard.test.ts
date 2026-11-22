import { readFileSync } from "node:fs";
import { parse } from "yaml";
import { describe, expect, test, vi } from "vitest";
import { cutContext, discoverCluster, type Env, endpointNote, isAddress, type Line, nameOf, NO_EPOCH_YET, onboardCluster, type Plan, pressOnboarding, sender, stoppedSentence } from "./onboard.ts";
import type { Client } from "./request.ts";
import { EPOCH_CHANGED_ASIDE, type Outcome, Writes, type WriteSpec } from "./write.ts";

const CANARY = "BWSYNTH-onboard-canary";
const ID = "YnJvbnpld2FyZC1lMmUtb25ib2FyZC1jbHVzdGVyLTA=";
const talosconfig = (extra = "") => `# ${CANARY}\ncontext: a\ncontexts:\n  a:\n    endpoints: [10.0.0.1:50000]\n    nodes: [10.9.9.9]\n    ca: QS1DQQ==\n    crt: QS1DUlQ=\n    key: ${CANARY}-key\n  b:\n    endpoints: [10.1.0.1]\n    ca: Qi1DQQ==\n    crt: Qi1DUlQ=\n    key: Qi1LRVk=\n${extra}`;

const problem = (status: number, code: string, extra: Record<string, unknown> = {}) => ({ type: `urn:bronzeward:problem:${code}`, title: code, status, ...extra });
const done = (data: unknown, status = 201, etag?: string): Outcome => ({ kind: "done", status, data, replayed: false, ...(etag !== undefined && { etag }) });
const refused = (status: number, code: string, extra: Record<string, unknown> = {}): Outcome => ({ kind: "refused", status, problem: problem(status, code, extra), replayed: false });

const MEMBERS = [
  { nodeId: "n1", hostname: "cp-1", address: "10.0.0.1", role: "controlplane", smbiosUuid: "11111111-2222-3333-4444-555555555555", platform: "metal", answered: true },
  { nodeId: "n2", hostname: "W_2", address: "10.0.0.2", role: "worker", smbiosUuid: "", platform: "metal", answered: true },
  { nodeId: "n3", hostname: "w-3", address: "10.0.0.3", role: "worker", smbiosUuid: "", platform: "", answered: false },
];
const DISCOVERY = { clusterId: ID, version: "v1.14.5", contract: "v1.14", supported: true, endpoint: "10.0.0.1:50000", kubernetesEndpoint: "https://10.0.0.1:6443", members: MEMBERS, complete: true };

// The server, in memory: what the routes the onboarding uses keep and answer. `fail` answers a route's
// next send with the outcome given, once.
class Server {
  requests: string[] = [];
  sends: { route: string; body: unknown }[] = [];
  released: string[] = [];
  clusters = new Map<string, { id: string; name: string; talosClusterId: string }>();
  machines = new Map<string, { id: string; cluster: string; key: string; name?: unknown }>();
  drafts = new Map<string, { id: string; cluster: string; title: string; state: string; rev: number }>();
  ingestions = new Map<string, { id: string; machine: string; draft: string; state: string; polls: number }>();
  keys = 0;
  discovery: typeof DISCOVERY = DISCOVERY;
  fail = new Map<string, Outcome>();
  // A route whose next send commits and then answers with this outcome: the answer was lost.
  lost = new Map<string, Outcome>();
  // The installation's epoch, and the epoch each write was sent under (`spec.epoch`, none when the caller named none).
  // A write sent under another epoch than this one is set aside, as the write layer does on a 412 epoch-mismatch.
  epoch = "e1";
  epochs: (string | undefined)[] = [];
  // Called with the route of each write the server committed: a test moves `epoch` here.
  committed: (route: string) => void = () => {};
  private n = 0;
  private id = (p: string) => `${p}_${++this.n}`;

  env = (): Env => ({
    send: async (spec: WriteSpec) => {
      const route = `${spec.method} ${spec.path}`;
      this.requests.push(route);
      this.sends.push({ route, body: structuredClone(spec.body) });
      this.epochs.push(spec.epoch);
      if (spec.epoch !== undefined && spec.epoch !== this.epoch) {
        spec.released?.({} as never);
        return { kind: "set-aside", why: EPOCH_CHANGED_ASIDE, ambiguous: false, problem: null };
      }
      const f = this.fail.get(route);
      if (f !== undefined) {
        this.fail.delete(route);
        spec.released?.({} as never);
        return f;
      }
      const o = this.write(spec);
      this.committed(route);
      const gone = this.lost.get(route);
      if (gone !== undefined) {
        this.lost.delete(route);
        return gone;
      }
      if (o.kind !== "unknown") spec.released?.({} as never);
      return o;
    },
    read: async (path: string) => ({ kind: "ok", ...this.get(path) }) as never,
    wait: async () => {},
  });

  private write(spec: WriteSpec): Outcome {
    const b = spec.body as Record<string, string>;
    switch (`${spec.method} ${spec.path.replace(/\/(cl|mch|drf)_[0-9]+/, "/{id}")}`) {
      case "POST /discoveries":
        return done(this.discovery, 200);
      case "POST /clusters": {
        const have = [...this.clusters.values()].find((c) => c.name === b["name"] || c.talosClusterId === b["talosClusterId"]);
        if (have !== undefined) return refused(409, "conflict", { cluster: have.id });
        const c = { id: this.id("cl"), name: b["name"]!, talosClusterId: b["talosClusterId"]! };
        this.clusters.set(c.id, c);
        return done({ ...c, endpoint: b["endpoint"], contract: b["contract"] });
      }
      case "POST /clusters/{id}/first-keys":
        this.keys++;
        return done({ cluster: spec.path.split("/")[2], kind: "talosconfig", replaced: this.keys > 1 }, 200);
      case "POST /machines": {
        const key = b["smbiosUuid"] ?? b["talosNodeId"]!;
        const have = [...this.machines.values()].find((m) => m.key === key);
        if (have !== undefined) return refused(409, "conflict", { machine: have.id });
        // A name another machine of the cluster holds, as the server refuses it.
        const holder = b["name"] === undefined ? undefined : [...this.machines.values()].find((m) => m.cluster === b["cluster"] && m.name === b["name"]);
        if (holder !== undefined) return refused(409, "name-in-use", { machine: holder.id });
        const m = { id: this.id("mch"), cluster: b["cluster"]!, key, name: b["name"] };
        this.machines.set(m.id, m);
        return done({ ...m, name: b["name"] ?? null });
      }
      case "POST /drafts": {
        const d = { id: this.id("drf"), cluster: b["cluster"]!, title: b["title"]!, state: "open", rev: 1 };
        this.drafts.set(d.id, d);
        return done(d, 201, `"${d.rev}"`);
      }
      case "POST /ingestions": {
        const d = this.drafts.get(b["draft"]!)!;
        if (spec.ifMatch !== `"${d.rev}"`) return refused(412, "precondition-failed", { draft: d.id });
        d.rev++;
        const i = { id: this.id("ing"), machine: b["machine"]!, draft: d.id, state: "held", polls: 0 };
        this.ingestions.set(i.id, i);
        return done({ ingestion: i.id, operation: "op_1" }, 202);
      }
    }
    throw new Error(`the fake has no route for ${spec.method} ${spec.path}`);
  }

  private get(path: string): { data: unknown; etag?: string } {
    this.requests.push(`GET ${path}`);
    const [p, q = ""] = path.split("?");
    const query = new URLSearchParams(q);
    if (p === "/drafts") return { data: { items: [...this.drafts.values()] } };
    if (p === "/ingestions") return { data: { items: [...this.ingestions.values()].filter((i) => i.machine === query.get("machine")) } };
    const m = /^\/(machines|drafts|ingestions)\/(.+)$/.exec(p!);
    if (/^\/clusters\/cl_[0-9]+$/.test(p!)) return { data: this.clusters.get(p!.slice("/clusters/".length)) };
    if (m?.[1] === "machines") return { data: this.machines.get(m[2]!) };
    if (m?.[1] === "drafts") return { data: this.drafts.get(m[2]!), etag: `"${this.drafts.get(m[2]!)!.rev}"` };
    if (m?.[1] === "ingestions") {
      const i = this.ingestions.get(m[2]!)!;
      if (++i.polls > 1 && i.state === "held") i.state = "released";
      return { data: i };
    }
    throw new Error(`the fake has no read for ${path}`);
  }
}

async function planOf(s: Server): Promise<Plan> {
  const r = await discoverCluster(s.env(), talosconfig(), {});
  if (!r.ok) throw new Error(r.why);
  r.plan.name = "lab";
  r.plan.kube = DISCOVERY.kubernetesEndpoint;
  return r.plan;
}

const run = async (s: Server, plan: Plan) => {
  let lines: Line[] = [];
  const ok = await onboardCluster(s.env(), plan, (l) => (lines = l));
  return { ok, lines };
};
const posts = (s: Server) => s.requests.filter((r) => r.startsWith("POST"));

describe("the cut of one context (cutContext)", () => {
  const dir = "../internal/talos/discover/testdata";
  const sample = readFileSync(`${dir}/two-contexts.talosconfig`, "utf8");
  const golden = readFileSync(`${dir}/two-contexts.b.talosconfig`, "utf8");

  test("parses to the document discover.OneContext writes for the same file (the Go golden)", () => {
    expect(parse(cutContext(sample, "b").document)).toEqual(parse(golden));
    expect(cutContext(sample, "b").endpoint).toBe("10.1.0.1");
  });

  test("takes the current context without a name, and keeps no other context, endpoint, node or auth block", () => {
    const cut = cutContext(sample, undefined);
    const doc = parse(cut.document) as { context: string; contexts: Record<string, Record<string, unknown>> };
    expect(doc.context).toBe("a");
    expect(Object.keys(doc.contexts)).toEqual(["a"]);
    expect(Object.keys(doc.contexts["a"]!).sort()).toEqual(["ca", "crt", "endpoints", "key"]);
    for (const left of ["10.9.9.9", "a-password", "b-identity", "Qi1LRVk=", "# Synthetic"]) expect(cut.document).not.toContain(left);
    expect(cut.endpoint).toBe("10.0.0.1:50000");
  });

  test("refuses what is no talosconfig, an unknown context and a context without credentials, quoting none of the text", () => {
    const refusals = [
      () => cutContext(`{ not: [valid, ${CANARY}`, undefined),
      () => cutContext(`hello: ${CANARY}`, undefined),
      () => cutContext(sample, "c"),
      () => cutContext(`context: a\ncontexts:\n  a:\n    ca: ${CANARY}\n`, undefined),
    ];
    for (const f of refusals) {
      let why = "";
      try {
        f();
      } catch (e) {
        why = String(e);
      }
      expect(why).not.toBe("");
      expect(why).not.toContain(CANARY);
    }
  });

  test("the parser's warnings reach no console call, whatever the file holds", () => {
    const spies = [vi.spyOn(console, "warn"), vi.spyOn(console, "error"), vi.spyOn(console, "log"), vi.spyOn(console, "info"), vi.spyOn(process, "emitWarning")];
    try {
      cutContext(`context: a\ncontexts:\n  a:\n    ca: QQ==\n    crt: QQ==\n    key: !secret ${CANARY}-tag\n`, undefined);
      for (const s of spies) expect(s).not.toHaveBeenCalled();
    } finally {
      for (const s of spies) s.mockRestore();
    }
  });
});

// The page's send over the real write layer, whose client answers from a script.
describe("the page's send (sender)", () => {
  const ok = async () => ({ status: 200, headers: new Headers({ "Bronzeward-Epoch": "e1" }), data: {} });
  const lose = async () => {
    throw new Error("synthetic: the answer was lost");
  };
  const layer = (...script: (() => Promise<unknown>)[]) => {
    const writes = new Writes(() => "p", async () => {});
    const calls: string[] = [];
    writes.client = { currentEpoch: "e1", attempt: async (m: string, path: string) => (m === "GET" ? ok() : (calls.push(path), script.shift()!())) } as unknown as Client;
    return { writes, calls };
  };
  const secret = (document = CANARY, released: () => void = () => {}): WriteSpec => ({ method: "POST", path: "/clusters/cl_1/first-keys", resource: "/clusters/cl_1", body: { document }, released });
  // What the layer still holds of the document: an open write with its body.
  const holding = (w: Writes) => w.open.filter((x) => x.spec.body !== undefined);

  test("an unknown write that carries the document is repeated as the same write: one body held, and none once it is done", async () => {
    const { writes, calls } = layer(lose, lose, ok);
    const send = sender(writes, () => true);
    expect((await send(secret())).kind).toBe("unknown");
    expect((await send(secret())).kind).toBe("unknown");
    expect(holding(writes)).toHaveLength(1);
    expect((await send(secret())).kind).toBe("done");
    expect(calls).toHaveLength(3);
    expect(holding(writes)).toHaveLength(0);
  });

  test("the discovery, which carries the cut document too, is a write the sender keeps", async () => {
    const specs: WriteSpec[] = [];
    const s = new Server();
    const env = { ...s.env(), send: async (spec: WriteSpec) => (specs.push(spec), s.env().send(spec)) };
    expect((await discoverCluster(env, talosconfig(), {})).ok).toBe(true);
    expect(specs[0]).toMatchObject({ path: "/discoveries", released: expect.any(Function) });
  });

  test("a second send while the first is in flight sends nothing and shares its answer", async () => {
    let answer!: () => void;
    const { writes, calls } = layer(() => new Promise((r) => (answer = () => r(ok()))));
    const send = sender(writes, () => true);
    const a = send(secret());
    const b = send(secret());
    await Promise.resolve();
    answer();
    expect([(await a).kind, (await b).kind]).toEqual(["done", "done"]);
    expect(calls).toHaveLength(1);
  });

  test("a page entered again repeats what the page before it left open, and the new page's hook is the one told", async () => {
    const { writes, calls } = layer(lose, ok);
    expect((await sender(writes, () => true)(secret())).kind).toBe("unknown");
    const released = vi.fn();
    expect((await sender(writes, () => true)(secret(CANARY, released))).kind).toBe("done");
    expect(calls).toHaveLength(2);
    expect(released).toHaveBeenCalledTimes(1);
    expect(holding(writes)).toHaveLength(0);
  });

  test("another file's body dismisses the open write first, releasing its body", async () => {
    const { writes } = layer(lose, lose);
    const first = vi.fn();
    const send = sender(writes, () => true);
    await send(secret("first-document", first));
    await send(secret("second-document"));
    expect(first).toHaveBeenCalledTimes(1);
    expect(holding(writes).map((w) => (w.spec.body as { document: string }).document)).toEqual(["second-document"]);
  });

  test("a write on another route dismisses the open one: onboarding another cluster after an unknown first key holds one document", async () => {
    const { writes } = layer(lose, lose);
    const send = sender(writes, () => true);
    await send(secret());
    await send({ method: "POST", path: "/discoveries", resource: "/clusters", body: { document: "other-document" }, released: () => {} });
    expect(holding(writes).map((w) => (w.spec.body as { document: string }).document)).toEqual(["other-document"]);
  });

  test("another body sent while the first is in flight waits for its answer, then dismisses what it left open", async () => {
    let lost!: () => void;
    const { writes } = layer(() => new Promise((_r, rej) => (lost = () => rej(new Error("synthetic: the answer was lost")))), lose);
    const send = sender(writes, () => true);
    const first = send(secret("first-document"));
    const second = send(secret("second-document"));
    await Promise.resolve();
    lost();
    expect([(await first).kind, (await second).kind]).toEqual(["unknown", "unknown"]);
    expect(holding(writes).map((w) => (w.spec.body as { document: string }).document)).toEqual(["second-document"]);
  });

  test("three callers in a row leave one document held: each waits for the one before it", async () => {
    let lost!: () => void;
    const { writes } = layer(() => new Promise((_r, rej) => (lost = () => rej(new Error("synthetic: the answer was lost")))), lose, lose);
    const send = sender(writes, () => true);
    const a = send(secret("a-document"));
    const b = send(secret("b-document"));
    const c = send(secret("c-document"));
    await new Promise((r) => setTimeout(r, 0));
    expect(holding(writes).map((w) => (w.spec.body as { document: string }).document)).toEqual(["a-document"]);
    lost();
    await Promise.all([a, b, c]);
    expect(holding(writes).map((w) => (w.spec.body as { document: string }).document)).toEqual(["c-document"]);
  });

  test("a request whose page is left while it waits for the open write sends nothing", async () => {
    let lost!: () => void;
    const { writes, calls } = layer(() => new Promise((_r, rej) => (lost = () => rej(new Error("synthetic: the answer was lost")))));
    let here = true;
    const a = sender(writes, () => true)(secret("a-document"));
    const b = sender(writes, () => here)(secret("b-document"));
    await new Promise((r) => setTimeout(r, 0));
    here = false;
    lost();
    expect((await a).kind).toBe("unknown");
    expect((await b).kind).toBe("not-done");
    expect(calls).toHaveLength(1);
    expect(holding(writes)).toHaveLength(0);
  });

  test("a queued request that would reuse an open write sends nothing once its page is left", async () => {
    let lost!: () => void;
    const { writes, calls } = layer(() => new Promise((_r, rej) => (lost = () => rej(new Error("synthetic: the answer was lost")))), lose);
    let here = true;
    const a = sender(writes, () => true)(secret("a-document"));
    const b = sender(writes, () => true)(secret("b-document"));
    const again = sender(writes, () => here)(secret("b-document"));
    await new Promise((r) => setTimeout(r, 0));
    here = false;
    lost();
    expect([(await a).kind, (await b).kind, (await again).kind]).toEqual(["unknown", "unknown", "not-done"]);
    expect(calls).toHaveLength(2);
  });

  test("a write that carries no document is a new write each time, and nothing is sent once the page is left", async () => {
    const { writes, calls } = layer(lose, lose);
    const plain: WriteSpec = { method: "POST", path: "/machines", resource: "/machines", body: {} };
    const send = sender(writes, () => true);
    await send(plain);
    await send(plain);
    expect(writes.open).toHaveLength(2);
    expect((await sender(writes, () => false)(secret())).kind).toBe("not-done");
    expect(calls).toHaveLength(2);
  });
});

describe("onboarding a cluster from a talosconfig (ONBOARD-01's steps, over an in-memory server)", () => {
  test("discovers with one context only, then records the cluster, sends the first key once, records the machines, opens the draft and imports each machine into it", async () => {
    const s = new Server();
    const plan = await planOf(s);
    const { ok, lines } = await run(s, plan);
    expect(ok).toBe(true);
    expect(lines.filter((l) => l.state !== "done").map((l) => [l.key, l.state])).toEqual([["machine:n3", "skipped"]]);
    expect(posts(s)).toEqual([
      "POST /discoveries", "POST /clusters", `POST /clusters/cl_1/first-keys`, "POST /machines", "POST /machines", "POST /drafts", "POST /ingestions", "POST /ingestions",
    ]);
    // The discovery and the first key carry the cut document: one context, the file's comment gone.
    for (const route of ["POST /discoveries", "POST /clusters/cl_1/first-keys"]) {
      const sent = s.sends.find((x) => x.route === route)!.body as { kind: string; document: string };
      expect(sent.kind).toBe("talosconfig");
      expect(Object.keys((parse(sent.document) as { contexts: object }).contexts)).toEqual(["a"]);
      expect(sent.document).not.toContain("10.9.9.9");
      expect(sent.document).not.toContain(`# ${CANARY}`);
    }
    // The first control-plane member that answered carries the first key.
    expect((s.sends.find((x) => x.route === "POST /clusters/cl_1/first-keys")!.body as { endpoint: string }).endpoint).toBe("10.0.0.1");
    // A machine is recorded by its SMBIOS UUID when the node reports one, else by its node ID; a hostname the
    // API cannot take as a name is left out, and the member that did not answer is not recorded.
    expect(s.sends.filter((x) => x.route === "POST /machines").map((x) => x.body)).toEqual([
      { cluster: "cl_1", smbiosUuid: MEMBERS[0]!.smbiosUuid, platform: "metal", talosEndpoint: "10.0.0.1", name: "cp-1" },
      { cluster: "cl_1", talosNodeId: "n2", platform: "metal", talosEndpoint: "10.0.0.2" },
    ]);
    expect([...s.ingestions.values()].map((i) => i.state)).toEqual(["released", "released"]);
    expect(lines.map((l) => l.key)).toEqual(["cluster", "first-key", "machine:n1", "machine:n2", "machine:n3", "draft", "import:n1", "import:n2"]);
  });

  // One failure per step: the run stops there, says which step, and a run again finishes the rest
  // without sending anything twice that the server kept.
  const failures: [string, string, Outcome, string][] = [
    ["the cluster", "POST /clusters", refused(422, "validation-failed"), "cluster"],
    ["the first key", "POST /clusters/cl_1/first-keys", refused(422, "validation-failed"), "first-key"],
    // Not a refusal of the name, which the run goes past (the tests of onboard naming).
    ["a machine", "POST /machines", refused(403, "forbidden"), "machine:n1"],
    ["the draft", "POST /drafts", refused(422, "validation-failed"), "draft"],
    ["an import", "POST /ingestions", refused(409, "conflict"), "import:n1"],
    ["an import whose answer was lost before it committed", "POST /ingestions", { kind: "unknown", why: "the connection ended", problem: null }, "import:n1"],
  ];
  test.each(failures)("%s fails: the run stops at it, and running again finishes, then changes nothing", async (_n, route, outcome, key) => {
    const s = new Server();
    const plan = await planOf(s);
    s.fail.set(route, outcome);
    const first = await run(s, plan);
    expect(first.ok).toBe(false);
    const failed = first.lines.find((l) => l.state === "failed" || l.state === "unknown")!;
    expect(failed.key).toBe(key);
    expect(first.lines.at(-1)).toBe(failed);
    expect(first.lines.slice(0, -1).every((l) => l.state === "done" || l.state === "skipped")).toBe(true);
    // A first key that did not end well is released with its write, so nothing here holds the file any more:
    // running again says so, and the file chosen again finds what the server holds.
    let again = plan;
    if (key === "first-key") {
      expect((await run(s, plan)).lines.find((l) => l.key === key)?.detail).toContain("choose the file again");
      again = await planOf(s);
    }
    const second = await run(s, again);
    expect(second.ok).toBe(true);
    expect(second.lines.every((l) => l.state === "done" || l.state === "skipped" || l.state === "existing")).toBe(true);
    const sent = s.requests.length;
    const third = await run(s, again);
    expect(third.ok).toBe(true);
    expect(s.requests.slice(sent).filter((r) => r.startsWith("POST"))).toEqual([]);
    // Nothing is recorded twice, and each machine was imported once.
    expect(s.clusters.size).toBe(1);
    expect(s.machines.size).toBe(2);
    expect(s.drafts.size).toBe(1);
    expect(s.ingestions.size).toBe(2);
  });

  test("a failed step's line is in words, with the problem's code and request number only in its reference", async () => {
    const s = new Server();
    const plan = await planOf(s);
    s.fail.set("POST /drafts", refused(422, "validation-failed", { detail: "a title is needed", instance: "req_d" }));
    const line = (await run(s, plan)).lines.at(-1)!;
    expect(line).toMatchObject({ key: "draft", state: "failed", reference: { status: 422 } });
    expect(line.detail).toMatch(/^not saved\. /);
    expect(line.detail).not.toMatch(/validation-failed|req_d|The server answered/);
    expect(line.detail).toContain("The server says: a title is needed.");
  });

  test("a step whose outcome is unknown says to check the current state, with no next step to repeat", async () => {
    const s = new Server();
    const plan = await planOf(s);
    s.fail.set("POST /ingestions", { kind: "unknown", why: "the connection ended", problem: problem(503, "dependency-unavailable", { instance: "req_u" }) });
    const line = (await run(s, plan)).lines.at(-1)!;
    expect(line).toMatchObject({ key: "import:n1", state: "unknown", reference: { status: 503 } });
    expect(line.detail).toMatch(/^unknown whether it was saved: the connection ended\. Check the current state before you do it again\. /);
    expect(line.detail).not.toMatch(/req_u|dependency-unavailable/);
    // The problem's own next step ("Try again ...") would send the person to repeat an act that may have happened.
    expect(line.detail).not.toMatch(/try again/i);
  });

  test("an import that committed but whose answer was lost is found through GET /ingestions?machine= on the next run, and not started again", async () => {
    const s = new Server();
    const plan = await planOf(s);
    s.lost.set("POST /ingestions", { kind: "unknown", why: "the connection ended", problem: null });
    const first = await run(s, plan);
    expect(first.ok).toBe(false);
    expect(first.lines.at(-1)).toMatchObject({ key: "import:n1", state: "unknown" });
    const machine = plan.machines["n1"]!;
    expect([...s.ingestions.values()].filter((i) => i.machine === machine)).toHaveLength(1);
    const sent = s.requests.length;
    const second = await run(s, plan);
    expect(second.ok).toBe(true);
    const after = s.requests.slice(sent);
    expect(after).toContain(`GET /ingestions?machine=${machine}`);
    // Only the second machine's import is started: the first was adopted.
    expect(after.filter((r) => r === "POST /ingestions")).toHaveLength(1);
    expect([...s.ingestions.values()].filter((i) => i.machine === machine)).toHaveLength(1);
    expect(second.lines.find((l) => l.key === "import:n1")?.state).toBe("existing");
  });

  test("a cluster the server already holds keeps its own name: the file chosen again under another name finds the same draft and imports", async () => {
    const s = new Server();
    expect((await run(s, await planOf(s))).ok).toBe(true);
    const again = await planOf(s);
    again.name = "another";
    const { ok, lines } = await run(s, again);
    expect(ok).toBe(true);
    expect(lines[0]).toMatchObject({ key: "cluster", label: "Cluster lab", state: "existing" });
    expect(again.name).toBe("lab");
    expect(s.drafts.size).toBe(1);
    expect(s.ingestions.size).toBe(2);
  });

  test("an IPv6 member is sent bracketed, for the first key and for its machine", async () => {
    const s = new Server();
    s.discovery = { ...DISCOVERY, members: [{ ...MEMBERS[0]!, address: "fd00::1" }, { ...MEMBERS[1]!, address: "fd00::2" }, MEMBERS[2]!] };
    expect((await run(s, await planOf(s))).ok).toBe(true);
    expect((s.sends.find((x) => x.route.endsWith("/first-keys"))!.body as { endpoint: string }).endpoint).toBe("[fd00::1]");
    const endpoints = s.sends.filter((x) => x.route === "POST /machines").map((x) => (x.body as { talosEndpoint: string }).talosEndpoint);
    expect(endpoints).toEqual(["[fd00::1]", "[fd00::2]"]);
  });

  test("after a reload the same file finds what the server holds: cluster, machines, draft and imports are adopted, the first key is sent again", async () => {
    const s = new Server();
    expect((await run(s, await planOf(s))).ok).toBe(true);
    const before = { c: s.clusters.size, m: s.machines.size, d: s.drafts.size, i: s.ingestions.size };
    const again = await run(s, await planOf(s));
    expect(again.ok).toBe(true);
    expect(again.lines.map((l) => l.state)).toEqual(["existing", "done", "existing", "existing", "skipped", "existing", "existing", "existing"]);
    expect({ c: s.clusters.size, m: s.machines.size, d: s.drafts.size, i: s.ingestions.size }).toEqual(before);
    expect(s.keys).toBe(2);
  });

  test("stops at a machine the server holds in another cluster, and runs nothing after it", async () => {
    const s = new Server();
    s.machines.set("mch_x", { id: "mch_x", cluster: "cl_other", key: MEMBERS[0]!.smbiosUuid });
    const plan = await planOf(s);
    const { ok, lines } = await run(s, plan);
    expect(ok).toBe(false);
    expect(lines.find((l) => l.state === "failed")?.key).toBe("machine:n1");
    expect(posts(s)).not.toContain("POST /drafts");
  });

  test("an unsupported contract, and a cluster with no control-plane member that answered, are refused before anything is recorded", async () => {
    for (const [found, why] of [
      [{ ...DISCOVERY, supported: false }, "v1.14"],
      [{ ...DISCOVERY, members: MEMBERS.map((m) => ({ ...m, answered: false })) }, "control-plane"],
    ] as const) {
      const s = new Server();
      s.discovery = found;
      const r = await discoverCluster(s.env(), talosconfig(), {});
      expect(r.ok).toBe(false);
      if (!r.ok) expect(r.why).toContain(why);
      expect(s.requests).toEqual(["POST /discoveries"]);
    }
  });

  test("the talosconfig reaches no line, no kept state and no refusal text, and the plan drops it once the first key's write is released", async () => {
    const s = new Server();
    s.fail.set("POST /machines", refused(422, "validation-failed", { detail: "synthetic rule" }));
    const plan = await planOf(s);
    expect(plan.document).toContain(CANARY);
    const first = await run(s, plan);
    expect(plan.document).toBeUndefined();
    expect(JSON.stringify(first.lines)).not.toContain(CANARY);
    expect(JSON.stringify(plan)).not.toContain(CANARY);
    const second = await run(s, plan);
    expect(second.ok).toBe(true);
    expect(JSON.stringify([second.lines, plan])).not.toContain(CANARY);
    // Only the discovery and the first key ever carried the key, and the file's comment was in no request.
    expect(new Set(s.sends.filter((x) => JSON.stringify(x.body).includes(`${CANARY}-key`)).map((x) => x.route))).toEqual(new Set(["POST /discoveries", "POST /clusters/cl_1/first-keys"]));
    expect(s.sends.filter((x) => JSON.stringify(x.body).includes(`# ${CANARY}`))).toEqual([]);
  });

  test("a refused discovery says why, quotes no text of the file and makes no plan", async () => {
    const s = new Server();
    const env = s.env();
    s.fail.set("POST /discoveries", refused(503, "talos-access-unavailable", { detail: "synthetic: the endpoint did not answer" }));
    const r = await discoverCluster(env, talosconfig(), {});
    expect(r.ok).toBe(false);
    if (!r.ok) {
      // In words: no code, and the problem goes to the reference the page shows beside the note.
      expect(r.why).toContain("The server could not work with Talos for this machine or cluster.");
      expect(r.why).not.toContain("talos-access-unavailable");
      expect(r.why).not.toContain(CANARY);
      expect(r.reference?.status).toBe(503);
      expect(r.reference?.problem.type).toBe("urn:bronzeward:problem:talos-access-unavailable");
    }
  });
});

// The endpoint, in words, before anything is sent (ginsys/bronzeward#1049). The discovery route takes an IP
// address or a DNS name and resolves a name once (persistence-api.md §3.3), so a name from the file is sent.
const named = (endpoint: string) => talosconfig().replace("[10.0.0.1:50000]", `[${endpoint}]`);

describe("what counts as an address follows the server's first split", () => {
  // The shape is all the page judges, to say whether the server resolved a name: everything is sent, and
  // the server's own sentence says what is wrong with it (an octet, a port, the brackets).
  test.each([
    "10.0.0.1",
    "10.0.0.1:50000",
    "[::1]",
    "[::1]:50000",
    "[fd00::1]",
    "[::ffff:10.0.0.1]:50000",
    "::1",
    "fd00::3",
    "999.0.0.1",
    "10.0.0.1:99999",
    "[::1]50000",
    "[::1",
    "[10.0.0.1]",
  ])("%s has the shape of an address", (endpoint) => {
    expect(isAddress(endpoint)).toBe(true);
  });
  test.each([
    "node.example.test",
    "node.example.test:50000",
    "cafe:50000",
    "cafe",
    "10.0.0.1.example.test",
    "10.0.0:50000",
    "",
    "[node.example.test]:50000",
    "[node.example.test]",
    "[cafe]",
    "[dead.beef]:50000",
    "[x:y@[::1]",
    "node:80:90",
  ])("%s is a name, or not an address at all", (endpoint) => {
    expect(isAddress(endpoint)).toBe(false);
  });
});

describe("the endpoint the page will use, said before the button", () => {
  test("an address from the file, or typed, is the endpoint the page will try", () => {
    expect(endpointNote(talosconfig(), "", "")).toBe("The page will try the cluster at 10.0.0.1:50000.");
    expect(endpointNote(talosconfig(), "b", "")).toBe("The page will try the cluster at 10.1.0.1.");
    expect(endpointNote(named("node.example.test"), "", "10.2.0.1")).toBe("The page will try the cluster at 10.2.0.1.");
  });

  test("a name, from the file or typed, is the endpoint the page will try, as it stands", () => {
    const say = (endpoint: string) => `The page will try the cluster at ${endpoint}.`;
    expect(endpointNote(named("node.example.test"), "", "")).toBe(say("node.example.test"));
    expect(endpointNote(named("cafe:50000"), "", "")).toBe(say("cafe:50000"));
    expect(endpointNote(talosconfig(), "", "[node.example.test]:50000")).toBe(say("[node.example.test]:50000"));
    expect(endpointNote(named("999.0.0.1"), "", "")).toBe(say("999.0.0.1"));
    expect(endpointNote(talosconfig(), "", "lb.example.test")).toBe(say("lb.example.test"));
  });

  test("a context that lists several endpoints says the page tries the first of them; a typed one is alone", () => {
    const several = talosconfig().replace("endpoints: [10.0.0.1:50000]", "endpoints: [cp.example.test, 10.0.0.2, 10.0.0.3]");
    expect(endpointNote(several, "", "")).toBe("The page will try the first of 3 endpoints, cp.example.test.");
    expect(endpointNote(several, "", "10.9.9.9")).toBe("The page will try the cluster at 10.9.9.9.");
    // One entry, and an entry that is not text, are not a list.
    expect(endpointNote(talosconfig(), "", "")).toBe("The page will try the cluster at 10.0.0.1:50000.");
    expect(endpointNote(talosconfig().replace("endpoints: [10.0.0.1:50000]", "endpoints: [10.0.0.1:50000, 7]"), "", "")).toBe("The page will try the cluster at 10.0.0.1:50000.");
  });

  test("a context with no endpoint and none typed asks for an IP address", () => {
    const none = talosconfig().replace("    endpoints: [10.0.0.1:50000]\n", "");
    expect(endpointNote(none, "", "")).toBe("The talosconfig's context names no endpoint: type the IP address of a control-plane node.");
  });

  test("no file, or a file that is not a talosconfig, says nothing", () => {
    expect(endpointNote(undefined, "", "")).toBeUndefined();
    expect(endpointNote("hello: world", "", "")).toBeUndefined();
    expect(endpointNote(talosconfig(), "c", "")).toBeUndefined();
  });
});

describe("a name is sent as it stands, and discovery's failures are whole sentences with no lead of their own", () => {
  test("a name from the file, and a typed one, are sent as they are, and the plan keeps what was sent", async () => {
    for (const [text, typed, sent] of [
      [named("node.example.test"), "", "node.example.test"],
      [talosconfig(), "lb.example.test:50000", "lb.example.test:50000"],
    ] as const) {
      const s = new Server();
      const r = await discoverCluster(s.env(), text, { endpoint: typed });
      expect(r.ok).toBe(true);
      expect((s.sends[0]!.body as { endpoint: string }).endpoint).toBe(sent);
      if (r.ok) expect(r.plan.endpoint).toBe(sent);
    }
  });

  test("a refused read of a name says to type a control-plane node's address; one of an address does not", async () => {
    for (const [typed, hint] of [["lb.example.test", true], ["10.0.0.1", false]] as const) {
      const s = new Server();
      s.fail.set("POST /discoveries", refused(503, "talos-access-unavailable", { title: "Talos is unavailable", detail: "the endpoint did not resolve", instance: "req_x" }));
      const r = await discoverCluster(s.env(), talosconfig(), { endpoint: typed });
      expect(r.ok).toBe(false);
      if (r.ok) continue;
      expect(r.why.startsWith("The server could not work with Talos for this machine or cluster. The server's explanation follows. The server says: the endpoint did not resolve.")).toBe(true);
      expect(r.why).not.toMatch(/req_x|Talos is unavailable|The server answered/);
      expect(r.why.includes(" is a name: the nodes' certificates may not list it. Add the name to machine.certSANs of the control-plane machines, or type the IP address, and the port if it is not 50000, of a control-plane node in the endpoint field.")).toBe(hint);
    }
  });

  test("a typed address wins over a name in the file and is sent", async () => {
    const s = new Server();
    const r = await discoverCluster(s.env(), named("node.example.test"), { endpoint: "10.2.0.1" });
    expect(r.ok).toBe(true);
    expect((s.sends[0]!.body as { endpoint: string }).endpoint).toBe("10.2.0.1");
  });

  test.each([
    ["a file that is not a talosconfig", "hello: world", {}, "The file is not a talosconfig."],
    ["a context the file does not hold", talosconfig(), { context: "c" }, "The talosconfig holds no context of that name."],
    ["a context with no endpoint", talosconfig().replace("    endpoints: [10.0.0.1:50000]\n", ""), {}, "The talosconfig's context names no endpoint: type the IP address of a control-plane node."],
  ])("%s", async (_what, text, options, why) => {
    const s = new Server();
    const r = await discoverCluster(s.env(), text, options);
    expect(r).toEqual({ ok: false, why });
    expect(s.requests).toEqual([]);
  });

  test("a refused read says what happened and what to do, with the problem's code, title and request number only in the reference", async () => {
    const s = new Server();
    s.fail.set("POST /discoveries", refused(400, "invalid-request", { title: "The request is malformed", detail: "endpoint must be an IP literal", instance: "req_x" }));
    const r = await discoverCluster(s.env(), talosconfig(), {});
    expect(r).toMatchObject({ ok: false, why: "The request was malformed, so the server did not act on it. Check what you entered and change it before sending again. If nothing looks wrong, report it with the reference below. The server says: endpoint must be an IP literal.", reference: { status: 400 } });
    expect(JSON.stringify(r)).toContain("req_x");
    if (!r.ok) expect(r.why).not.toMatch(/req_x|invalid-request|The request is malformed|The server answered/);
  });

  test("a refusal with no problem document is the status alone, and carries no reference", async () => {
    const env: Env = { ...new Server().env(), send: async () => ({ kind: "refused", status: 502, problem: null, replayed: false }) };
    expect(await discoverCluster(env, talosconfig(), {})).toEqual({ ok: false, why: "The server answered 502." });
  });

  test("a failed send names what happened, capitalised and with one full stop", async () => {
    const env: Env = { ...new Server().env(), send: async () => ({ kind: "not-done", why: "the page was left", problem: null }) };
    expect(await discoverCluster(env, talosconfig(), {})).toEqual({ ok: false, why: "The page was left." });
  });

  test("an unsupported contract and a cluster with no control-plane member answer in whole sentences", async () => {
    for (const [found, why] of [
      [{ ...DISCOVERY, supported: false }, "Contract v1.14 is not the contract of the machinery in this server, so it would refuse the cluster."],
      [{ ...DISCOVERY, members: MEMBERS.map((m) => ({ ...m, answered: false })) }, "No control-plane member answered, so no address can carry the first key. Run it again once one does."],
    ] as const) {
      const s = new Server();
      s.discovery = found;
      expect(await discoverCluster(s.env(), talosconfig(), {})).toEqual({ ok: false, why });
    }
  });
});

describe("onboard naming: a machine is named by the hostname its node reports", () => {
  const long = `${`${"a".repeat(63)}.`.repeat(3)}${"a".repeat(61)}`;
  const names: [string, string, { name?: string; note?: string }][] = [
    ["a short hostname", "cp-1", { name: "cp-1" }],
    ["a full hostname", "alcorak.office.ginsys.net", { name: "alcorak.office.ginsys.net" }],
    ["a one-label hostname of digits", "42", { name: "42" }],
    ["the longest hostname", long, { name: long }],
    ["upper case, lowered and said so", "Alcorak.Office.Ginsys.Net", { name: "alcorak.office.ginsys.net", note: "named by its hostname in lower case" }],
    ["no hostname", "", { note: "no name: the node reported no hostname" }],
    ["a letter only Unicode lowercasing changes", "İ.example", { note: 'no name: the hostname "İ.example" is not a valid name' }],
    ["an underscore", "W_2", { note: 'no name: the hostname "W_2" is not a valid name' }],
    ["a trailing dot", "alcorak.office.", { note: 'no name: the hostname "alcorak.office." is not a valid name' }],
    ["an address", "10.0.0.1", { note: 'no name: the hostname "10.0.0.1" is not a valid name' }],
    ["254 characters", `${long}a`, { note: `no name: the hostname ${JSON.stringify(`${long.slice(0, 64)}…`)} is not a valid name` }],
  ];
  test.each(names)("%s", (_what, hostname, want) => {
    expect(nameOf(hostname)).toEqual(want);
  });

  test("a run names each machine it records by its full hostname and says in its line why one stays unnamed", async () => {
    const s = new Server();
    s.discovery = {
      ...DISCOVERY,
      members: [
        { ...MEMBERS[0]!, hostname: "alcorak.office.ginsys.net" },
        { ...MEMBERS[1]!, hostname: "W_2" },
        { ...MEMBERS[1]!, nodeId: "n4", address: "10.0.0.4", hostname: "" },
        { ...MEMBERS[1]!, nodeId: "n5", address: "10.0.0.5", hostname: "Node-5.Example.Net" },
      ],
    };
    const { ok, lines } = await run(s, await planOf(s));
    expect(ok).toBe(true);
    expect(s.sends.filter((x) => x.route === "POST /machines").map((x) => (x.body as { name?: string }).name)).toEqual(["alcorak.office.ginsys.net", undefined, undefined, "node-5.example.net"]);
    expect(lines.filter((l) => l.key.startsWith("machine:")).map((l) => [l.key, l.detail])).toEqual([
      ["machine:n1", undefined],
      ["machine:n2", 'no name: the hostname "W_2" is not a valid name'],
      ["machine:n4", "no name: the node reported no hostname"],
      ["machine:n5", "named by its hostname in lower case"],
    ]);
  });

  test("a name another machine holds never stops the run: the machine is recorded without it and the line names the holder", async () => {
    const s = new Server();
    s.discovery = {
      ...DISCOVERY,
      members: [
        { ...MEMBERS[0]!, hostname: "alcorak.office.ginsys.net" },
        { ...MEMBERS[1]!, hostname: "alcorak.office.ginsys.net" },
      ],
    };
    const { ok, lines } = await run(s, await planOf(s));
    expect(ok).toBe(true);
    expect(s.sends.filter((x) => x.route === "POST /machines").map((x) => (x.body as { name?: string }).name)).toEqual(["alcorak.office.ginsys.net", "alcorak.office.ginsys.net", undefined]);
    const holder = [...s.machines.values()].find((m) => m.name === "alcorak.office.ginsys.net")!.id;
    // The words never carry the holder's identifier; the line carries it beside them, for the link.
    expect(lines.filter((l) => l.key.startsWith("machine:")).map((l) => [l.key, l.state, l.detail, l.holder])).toEqual([
      ["machine:n1", "done", undefined, undefined],
      ["machine:n2", "done", 'no name: the name "alcorak.office.ginsys.net" is already held by another machine of the cluster; rename that machine, then name this one', holder],
    ]);
    expect(s.machines.size).toBe(2);
  });

  test("a name the server's grammar refuses is treated the same: the machine is recorded without it", async () => {
    const s = new Server();
    s.discovery = { ...DISCOVERY, members: [{ ...MEMBERS[0]!, hostname: "cp-1" }] };
    s.fail.set("POST /machines", refused(400, "invalid-request"));
    const { ok, lines } = await run(s, await planOf(s));
    expect(ok).toBe(true);
    expect(s.sends.filter((x) => x.route === "POST /machines").map((x) => (x.body as { name?: string }).name)).toEqual(["cp-1", undefined]);
    expect(lines.find((l) => l.key === "machine:n1")?.detail).toBe('no name: the server refused the hostname "cp-1" as a name');
  });
});

describe("every write of an onboarding is sent under the epoch its plan's first press began under (ginsys/bronzeward#1075)", () => {
  const press = async (s: Server, plan: Plan, epoch: string | undefined, valid: () => boolean = () => true) => {
    let lines: Line[] = [];
    // The client's own epoch is the server's, as the client sees it through the answers it reads.
    const r = await pressOnboarding(s.env(), plan, { current: () => (epoch === undefined ? undefined : s.epoch), valid }, (l) => (lines = l));
    return { ...r, lines };
  };
  const writes = (s: Server) => s.sends.map((x, i) => ({ route: x.route, epoch: s.epochs[i] })).filter((x) => x.route !== "POST /discoveries");

  test("the cluster, the first key, each machine, the draft and each import carry the epoch of the press, not a later one", async () => {
    const s = new Server();
    const plan = await planOf(s);
    s.epochs.length = 0;
    s.sends.length = 0;
    const r = await press(s, plan, "e1");
    expect(r.ok).toBe(true);
    expect(writes(s).map((w) => w.route.replace(/cl_[0-9]+/, "{cl}"))).toEqual(["POST /clusters", "POST /clusters/{cl}/first-keys", "POST /machines", "POST /machines", "POST /drafts", "POST /ingestions", "POST /ingestions"]);
    expect(new Set(writes(s).map((w) => w.epoch))).toEqual(new Set(["e1"]));
  });

  test("the epoch changes after the first key: the next write carries the old epoch, is set aside in the write layer's words, and no later write goes out", async () => {
    const s = new Server();
    const plan = await planOf(s);
    s.committed = (route) => {
      if (route.endsWith("/first-keys")) s.epoch = "e2";
    };
    s.sends.length = 0;
    s.epochs.length = 0;
    const r = await press(s, plan, "e1");
    expect(r.ok).toBe(false);
    expect(r.epochChanged).toBe(true);
    expect(writes(s).map((w) => [w.route.replace(/cl_[0-9]+/, "{cl}"), w.epoch])).toEqual([
      ["POST /clusters", "e1"],
      ["POST /clusters/{cl}/first-keys", "e1"],
      ["POST /machines", "e1"],
    ]);
    const done = r.lines.filter((l) => l.state === "done").map((l) => l.label);
    expect(done).toEqual(["Cluster lab", "First key"]);
    const stopped = r.lines.find((l) => l.state === "failed")!;
    expect(stopped.detail).toBe(`not saved: ${EPOCH_CHANGED_ASIDE}.`);
    expect(plan.unsent).toEqual(["Machine cp-1", "Machine W_2", "Draft", "Imports"]);
  });

  test("an epoch stop before the first key drops the page's copy of the talosconfig, which nothing will send now", async () => {
    const s = new Server();
    const plan = await planOf(s);
    s.committed = (route) => {
      if (route === "POST /clusters") s.epoch = "e2";
    };
    expect(plan.document).toBeDefined();
    const r = await press(s, plan, "e1");
    expect(r.epochChanged).toBe(true);
    expect(plan.keyed).toBe(false);
    expect(plan.document).toBeUndefined();
    expect(plan.unsent).toEqual(["Machines", "Draft", "Imports"]);
  });

  test("the shell ends the reads of the press with the epoch moved: nothing more is sent, not even a write that would be refused, and the plan is retired", async () => {
    const s = new Server();
    const plan = await planOf(s);
    s.sends.length = 0;
    s.epochs.length = 0;
    let open = true;
    s.committed = (route) => {
      if (route.endsWith("/first-keys")) {
        open = false;
        s.epoch = "e2";
      }
    };
    const r = await press(s, plan, "e1", () => open);
    expect(r.ok).toBe(false);
    expect(r.epochChanged).toBe(true);
    expect(writes(s).map((w) => w.route.replace(/cl_[0-9]+/, "{cl}"))).toEqual(["POST /clusters", "POST /clusters/{cl}/first-keys"]);
    expect(r.lines.find((l) => l.state === "failed")!.detail).toBe("not saved: the page's reads ended.");
    expect(plan.document).toBeUndefined();
  });

  test("the shell ends the reads with the epoch unchanged (a revocation, a sign-out): the press stops quietly, says nothing of an epoch, and the plan can be run again", async () => {
    const s = new Server();
    const plan = await planOf(s);
    s.sends.length = 0;
    s.epochs.length = 0;
    let open = true;
    s.committed = (route) => {
      if (route.endsWith("/first-keys")) open = false;
    };
    const r = await press(s, plan, "e1", () => open);
    expect(r.ok).toBe(false);
    expect(r.epochChanged).toBe(false);
    expect(writes(s).map((w) => w.route.replace(/cl_[0-9]+/, "{cl}"))).toEqual(["POST /clusters", "POST /clusters/{cl}/first-keys"]);
    expect(stoppedSentence("Machine cp-1", r.epochChanged, false, plan.unsent)).toContain("Running it again");
    open = true;
    const again = await press(s, plan, "e1");
    expect(again.ok).toBe(true);
    expect(plan.epoch).toBe("e1");
  });

  test("a second press of the same plan sends under the epoch of the first, so the ids it holds are never sent under the new one; a new plan begins under the new epoch", async () => {
    const s = new Server();
    const plan = await planOf(s);
    s.committed = (route) => {
      if (route.endsWith("/first-keys")) s.epoch = "e2";
    };
    await press(s, plan, "e1");
    s.sends.length = 0;
    s.epochs.length = 0;
    const again = await press(s, plan, "e2");
    expect(again.ok).toBe(false);
    expect(again.epochChanged).toBe(true);
    expect(plan.epoch).toBe("e1");
    expect(writes(s).map((w) => w.epoch)).toEqual(["e1"]);
    s.committed = () => {};
    const fresh = await planOf(s);
    s.sends.length = 0;
    s.epochs.length = 0;
    const r = await press(s, fresh, "e2");
    expect(r.ok).toBe(true);
    expect(fresh.epoch).toBe("e2");
    expect(new Set(writes(s).map((w) => w.epoch))).toEqual(new Set(["e2"]));
  });

  test("a client that holds no epoch sends nothing and says so, and the press after it learns one pins the plan and runs", async () => {
    const s = new Server();
    const plan = await planOf(s);
    s.sends.length = 0;
    s.epochs.length = 0;
    let known: string | undefined;
    const current = () => known;
    let lines: Line[] = [];
    const first = await pressOnboarding(s.env(), plan, { current, valid: () => true }, (l) => (lines = l));
    expect(first).toEqual({ ok: false, epochChanged: false });
    expect(lines).toEqual([{ key: "epoch", label: "Epoch", state: "failed", detail: NO_EPOCH_YET }]);
    expect(writes(s)).toEqual([]);
    expect(plan.epoch).toBeUndefined();
    expect(plan.document).toBeDefined();
    // The page reads again and the client holds an epoch: the same plan runs, every write under it.
    known = "e1";
    const second = await pressOnboarding(s.env(), plan, { current, valid: () => true }, () => {});
    expect(second.ok).toBe(true);
    expect(plan.epoch).toBe("e1");
    expect(new Set(writes(s).map((w) => w.epoch))).toEqual(new Set(["e1"]));
  });

  test("a stop that only a read saw (the answer discarded under a new epoch) still says the epoch changed and retires the plan", async () => {
    const s = new Server();
    const plan = await planOf(s);
    s.clusters.set("cl_9", { id: "cl_9", name: "lab", talosClusterId: ID });
    const env: Env = { ...s.env(), read: async () => ((s.epoch = "e2"), { kind: "discarded" } as never) };
    const r = await pressOnboarding(env, plan, { current: () => s.epoch, valid: () => true }, () => {});
    expect(r.ok).toBe(false);
    expect(r.epochChanged).toBe(true);
    expect(plan.document).toBeUndefined();
  });

  test("a stop inside the imports names each import not finished, not only the steps after it", async () => {
    const s = new Server();
    const plan = await planOf(s);
    s.committed = (route) => {
      if (route === "POST /drafts") s.epoch = "e2";
    };
    const r = await press(s, plan, "e1");
    expect(r.epochChanged).toBe(true);
    expect(plan.unsent).toEqual(["Import cp-1", "Import W_2"]);
  });

  test("an import whose write was answered before the epoch moved is not described as unsent: the page says not finished", async () => {
    const s = new Server();
    const plan = await planOf(s);
    const base = s.env();
    const env: Env = { ...base, read: async (path: string) => (/^\/ingestions\/ing_/.test(path) ? ((s.epoch = "e2"), { kind: "discarded" } as never) : base.read(path)) };
    const r = await pressOnboarding(env, plan, { current: () => s.epoch, valid: () => true }, () => {});
    expect(r.epochChanged).toBe(true);
    expect(posts(s).filter((x) => x === "POST /ingestions")).toHaveLength(1);
    expect(stoppedSentence("Import cp-1", true, true, plan.unsent)).toContain("not finished: Import cp-1, Import W_2");
    expect(stoppedSentence("Import cp-1", true, true, plan.unsent)).not.toContain("not sent");
  });

  test("the shell keeps the page while it held the file: the sentence sends the person to the machines, never to a disabled button", () => {
    expect(stoppedSentence("Cluster lab", true, true, ["First key"], true)).toBe(
      "Stopped at Cluster lab. The installation's epoch changed after this onboarding began, so nothing more was sent under it (not finished: First key). Open the machines and connect the cluster again: choosing the file finds what is recorded and finishes the rest under the new epoch.",
    );
  });

  test("the page's sentence after an epoch stop says what is not finished and offers the file, never the run again", () => {
    expect(stoppedSentence("Machine cp-1", true, true, ["Draft", "Imports"])).toBe(
      "Stopped at Machine cp-1. The installation's epoch changed after this onboarding began, so nothing more was sent under it (not finished: Draft, Imports). Choosing the file again finds what is recorded and finishes the rest under the new epoch.",
    );
    expect(stoppedSentence("Imports", true, true, [])).not.toContain("not finished");
    expect(stoppedSentence("Draft", false, false, ["Imports"])).toBe("Stopped at Draft. Running it again finds what is recorded and finishes the rest.");
    expect(stoppedSentence("First key", false, true, undefined)).toBe("Stopped at First key. Running it again finds what is recorded and finishes the rest: choose the file again.");
  });
});
