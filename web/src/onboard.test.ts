import { readFileSync } from "node:fs";
import { parse } from "yaml";
import { describe, expect, test, vi } from "vitest";
import { cutContext, discoverCluster, type Env, type Line, onboardCluster, type Plan, sender } from "./onboard.ts";
import type { Client } from "./request.ts";
import { type Outcome, Writes, type WriteSpec } from "./write.ts";

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
  machines = new Map<string, { id: string; cluster: string; key: string }>();
  drafts = new Map<string, { id: string; cluster: string; title: string; state: string; rev: number }>();
  ingestions = new Map<string, { id: string; machine: string; draft: string; state: string; polls: number }>();
  keys = 0;
  discovery: typeof DISCOVERY = DISCOVERY;
  fail = new Map<string, Outcome>();
  // A route whose next send commits and then answers with this outcome: the answer was lost.
  lost = new Map<string, Outcome>();
  private n = 0;
  private id = (p: string) => `${p}_${++this.n}`;

  env = (): Env => ({
    send: async (spec: WriteSpec) => {
      const route = `${spec.method} ${spec.path}`;
      this.requests.push(route);
      this.sends.push({ route, body: structuredClone(spec.body) });
      const f = this.fail.get(route);
      if (f !== undefined) {
        this.fail.delete(route);
        spec.released?.({} as never);
        return f;
      }
      const o = this.write(spec);
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
        const m = { id: this.id("mch"), cluster: b["cluster"]!, key };
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
    ["a machine", "POST /machines", refused(422, "validation-failed"), "machine:n1"],
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
      expect(r.why).toContain("talos-access-unavailable");
      expect(r.why).not.toContain(CANARY);
    }
  });
});
