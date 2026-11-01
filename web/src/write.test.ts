import { describe, expect, test, vi } from "vitest";
import { Client, type ClientOptions } from "./request.ts";
import { BACKOFF, EPOCH_CHANGED_ASIDE, NO_EPOCH, newKey, SIGN_IN_CHANGED, SIGN_IN_REFUSED, Write, Writes } from "./write.ts";

const EPOCH = "e1";
const PRINCIPAL = "https://idp.invalid SYNTH-sub-1";

function problem(status: number, code: string, extra: Record<string, unknown> = {}, epoch: string | null = EPOCH): Response {
  const headers: Record<string, string> = { "Content-Type": "application/problem+json" };
  if (epoch !== null) headers["Bronzeward-Epoch"] = epoch;
  return new Response(JSON.stringify({ type: `urn:bronzeward:problem:${code}`, title: code, status, ...extra }), { status, headers });
}

function created(body: unknown, replayed = false): Response {
  const headers: Record<string, string> = { "Content-Type": "application/json", "Bronzeward-Epoch": EPOCH };
  if (replayed) headers["Idempotent-Replayed"] = "true";
  return new Response(JSON.stringify(body), { status: 201, headers });
}

const recovery = (epoch: string | null = EPOCH) =>
  new Response(JSON.stringify({ mode: "normal" }), { status: 200, headers: { "Content-Type": "application/json", ...(epoch === null ? {} : { "Bronzeward-Epoch": epoch }) } });

// A page whose writes are answered from `answers`, a function called at its send, and whose
// GET /recovery from `epochs`: an epoch, none (null), or a whole answer.
function page(answers: (Response | Error | (() => Response | Error))[], epochs: (string | null | Response)[] = [], opts: ClientOptions = {}) {
  const sent: { method: string; path: string; headers: Record<string, string>; body: unknown }[] = [];
  const fetchMock = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    const path = String(url).replace("/api/v1", "");
    const method = init?.method ?? "GET";
    sent.push({ method, path, headers: { ...(init?.headers as Record<string, string>) }, body: init?.body });
    const read = (e: string | null | Response) => (e instanceof Response ? e : recovery(e));
    const given = path === "/recovery" ? (epochs.length > 0 ? read(epochs.shift()!) : recovery()) : answers.shift();
    const next = typeof given === "function" ? given() : given;
    if (next === undefined) throw new Error("no answer left");
    if (next instanceof Error) throw next;
    return next;
  });
  const client = new Client({ token: () => "SYNTH-token", ...opts, fetch: fetchMock as unknown as typeof fetch });
  let who: string | undefined = PRINCIPAL;
  const waits: number[] = [];
  const writes = new Writes(() => who, async (ms) => void waits.push(ms));
  writes.client = client;
  return { client, writes, sent, waits, signIn: (p: string | undefined) => (who = p), writesSent: () => sent.filter((s) => s.path !== "/recovery") };
}

const spec = () => new Write({ method: "POST", path: "/clusters", body: { name: "lab" }, resource: "/clusters" });

describe("the key (§8.4 item 1)", () => {
  test("is 128 random bits, base64url without padding: 22 characters the server accepts", () => {
    const keys = new Set(Array.from({ length: 64 }, newKey));
    expect(keys.size).toBe(64);
    for (const k of keys) expect(k).toMatch(/^[A-Za-z0-9_-]{22}$/);
  });

  test("is one per logical write, kept on every repeat, and never derived from the content", async () => {
    const { writes, writesSent } = page([new TypeError("Failed to fetch"), created({ id: "c1" }, true)]);
    const a = spec();
    const b = spec();
    expect(a.key).not.toBe(b.key);
    await writes.submit(a);
    expect(writesSent().map((s) => s.headers["Idempotency-Key"])).toEqual([a.key, a.key]);
  });
});

describe("each send (§8.4 item 3)", () => {
  test("carries the key, the reference epoch, JSON and If-Match where given; reads GET /recovery once when no epoch was seen", async () => {
    const { writes, sent } = page([created({ id: "c1" })]);
    const w = new Write({ method: "PUT", path: "/clusters/c1", body: { name: "lab" }, ifMatch: '"v1"', resource: "/clusters/c1" });
    expect(await writes.submit(w)).toEqual({ kind: "done", status: 201, data: { id: "c1" }, replayed: false });
    expect(sent.map((s) => `${s.method} ${s.path}`)).toEqual(["GET /recovery", "PUT /clusters/c1"]);
    expect(sent[1]!.headers).toEqual({
      Accept: "application/json",
      Authorization: "Bearer SYNTH-token",
      "Content-Type": "application/json",
      "Idempotency-Key": w.key,
      "Bronzeward-Expected-Epoch": EPOCH,
      "If-Match": '"v1"',
    });
    expect(sent[1]!.body).toBe('{"name":"lab"}');
  });

  test("uses the last epoch the page saw, without a read, and sends no If-Match where none is given", async () => {
    const { client, writes, sent } = page([created({})], []);
    await client.attempt("GET", "/recovery");
    sent.length = 0;
    await writes.submit(spec());
    expect(sent.map((s) => s.path)).toEqual(["/clusters"]);
    expect(sent[0]!.headers["If-Match"]).toBeUndefined();
  });

  test("sends nothing when the read shows no epoch", async () => {
    const { writes, writesSent } = page([], [null]);
    expect(await writes.submit(spec())).toEqual({ kind: "not-done", why: NO_EPOCH, problem: null });
    expect(writesSent()).toEqual([]);
  });

  test("a second submit while the first runs sends nothing and returns the same outcome", async () => {
    const { writes, writesSent } = page([created({ id: "c1" })]);
    const w = spec();
    const [a, b] = await Promise.all([writes.submit(w), writes.submit(w)]);
    expect(a).toBe(b);
    expect(writesSent()).toHaveLength(1);
  });
});

describe("repeats (§8.2, §8.4 item 2)", () => {
  test("AMB-03: a 500 is repeated with the same key; the replay is reported as already done", async () => {
    const { writes, waits } = page([problem(500, "internal-error"), created({ id: "c1" }, true)]);
    expect(await writes.submit(spec())).toEqual({ kind: "done", status: 201, data: { id: "c1" }, replayed: true });
    expect(waits).toEqual([1000]);
  });

  test("at most 3 repeats after 1, 2 and 4 s, each after one confirming read; then unknown", async () => {
    const drop = () => new TypeError("Failed to fetch");
    const { writes, sent, waits } = page([drop(), drop(), drop(), drop()]);
    const w = spec();
    const o = await writes.submit(w);
    expect(o.kind).toBe("unknown");
    expect(waits).toEqual(BACKOFF);
    expect(sent.map((s) => s.path)).toEqual(["/recovery", "/clusters", "/recovery", "/clusters", "/recovery", "/clusters", "/recovery", "/clusters"]);
    expect(writes.open).toEqual([w]);
    expect(writes.repeatable(w)).toBe(true);
  });

  test("AMB-08: every attempt a 503 that commits nothing is not done", async () => {
    const { writes, writesSent } = page(Array.from({ length: 4 }, () => problem(503, "dependency-unavailable")));
    const o = await writes.submit(spec());
    expect(o).toMatchObject({ kind: "not-done", problem: { type: "urn:bronzeward:problem:dependency-unavailable" } });
    expect(writesSent()).toHaveLength(4);
    expect(writes.open).toEqual([]);
  });

  test("AMB-09: after a dropped attempt, 503s say nothing of it: unknown, never not done", async () => {
    const { writes } = page([new TypeError("Failed to fetch"), ...Array.from({ length: 3 }, () => problem(503, "transient-conflict"))]);
    expect((await writes.submit(spec())).kind).toBe("unknown");
  });

  test("epoch-superseded is not repeated", async () => {
    const { writes, writesSent } = page([problem(503, "epoch-superseded")]);
    expect((await writes.submit(spec())).kind).toBe("not-done");
    expect(writesSent()).toHaveLength(1);
  });

  test("a 5xx that is not a Bronzeward problem, or an unknown 5xx code, is ambiguous", async () => {
    const proxy = new Response("bad gateway", { status: 502, headers: { "Content-Type": "text/html" } });
    const { writes } = page([proxy, problem(503, "some-new-code"), ...Array.from({ length: 2 }, () => problem(503, "dependency-unavailable"))]);
    expect((await writes.submit(spec())).kind).toBe("unknown");
  });

  test("a 4xx is this send's own refusal, never repeated", async () => {
    const { writes, writesSent } = page([problem(422, "validation-failed", { rule: "name" })]);
    expect(await writes.submit(spec())).toMatchObject({ kind: "refused", status: 422, replayed: false });
    expect(writesSent()).toHaveLength(1);
  });

  test("after an ambiguous attempt, a refusal decided before the lookup leaves the outcome unknown", async () => {
    const { writes } = page([problem(500, "internal-error"), problem(403, "forbidden", { roles: ["author"] })]);
    expect((await writes.submit(spec())).kind).toBe("unknown");
  });

  test("a replayed refusal is the earlier attempt's", async () => {
    const earlier = problem(409, "stale-input");
    earlier.headers.set("Idempotent-Replayed", "true");
    const { writes } = page([new TypeError("Failed to fetch"), earlier]);
    expect(await writes.submit(spec())).toMatchObject({ kind: "refused", status: 409, replayed: true });
  });

  test("a 422 idempotency-key-reused to a repeat is unknown", async () => {
    const { writes } = page([new TypeError("Failed to fetch"), problem(422, "idempotency-key-reused")]);
    expect((await writes.submit(spec())).kind).toBe("unknown");
  });

  test("a 401 renews once and repeats after the checks; a second 401 stops", async () => {
    const renew = vi.fn(async () => true);
    const { writes, writesSent } = page([problem(401, "unauthenticated", {}, null), problem(401, "unauthenticated", {}, null)], [], { renew });
    expect(await writes.submit(spec())).toMatchObject({ kind: "refused", status: 401 });
    expect(renew).toHaveBeenCalledTimes(1);
    expect(writesSent()).toHaveLength(2);
  });

  test("403 identity-revoked clears the credential", async () => {
    const onRevoked = vi.fn();
    const { writes } = page([problem(403, "identity-revoked", {}, null)], [], { onRevoked });
    expect((await writes.submit(spec())).kind).toBe("refused");
    expect(onRevoked).toHaveBeenCalledTimes(1);
  });

  test("a 401 its renewal does not clear ends the page, after the write's outcome is settled", async () => {
    const { writes } = page([problem(401, "unauthenticated", {}, null)], [], { renew: async () => false });
    const w = spec();
    const outcomes: unknown[] = [];
    writes.onEnded = vi.fn(() => void outcomes.push(w.outcome));
    await writes.submit(w);
    expect(writes.onEnded).toHaveBeenCalledWith({ kind: "unauthenticated", problem: expect.objectContaining({ type: "urn:bronzeward:problem:unauthenticated" }) });
    expect(outcomes).toEqual([expect.objectContaining({ kind: "refused", status: 401 })]);
  });

  test("another 401 code is a refusal by its status class and ends nothing", async () => {
    const { writes } = page([problem(401, "some-new-code", {}, null)]);
    writes.onEnded = vi.fn();
    expect((await writes.submit(spec())).kind).toBe("refused");
    expect(writes.onEnded).not.toHaveBeenCalled();
  });

  test("identity-revoked after an ambiguous attempt ends the page and keeps the write unknown", async () => {
    const onRevoked = vi.fn();
    const { writes } = page([new TypeError("Failed to fetch"), problem(403, "identity-revoked", {}, null)], [], { onRevoked });
    const w = spec();
    writes.onEnded = vi.fn();
    expect((await writes.submit(w)).kind).toBe("unknown");
    expect(onRevoked).toHaveBeenCalledTimes(1);
    expect(writes.onEnded).toHaveBeenCalledWith({ kind: "revoked", problem: expect.objectContaining({ type: "urn:bronzeward:problem:identity-revoked" }) });
    expect(writes.open).toEqual([w]);
  });

  test("a run that throws settles the write: not done before any attempt may have committed, else unknown", async () => {
    const renew = async () => {
      throw new Error("synthetic renewal failure");
    };
    const a = page([problem(401, "unauthenticated", {}, null)], [], { renew });
    const w = spec();
    expect((await a.writes.submit(w)).kind).toBe("not-done");
    expect(w.running).toBeUndefined();
    expect(a.writes.open).toEqual([]);
    const b = page([new TypeError("Failed to fetch"), problem(401, "unauthenticated", {}, null)], [], { renew });
    const v = spec();
    expect((await b.writes.submit(v)).kind).toBe("unknown");
    expect(b.writes.open).toEqual([v]);
  });
});

describe("the confirming read's credential refusals (§8.2)", () => {
  test("identity-revoked clears the credential, ends the page and sets the write aside", async () => {
    const onRevoked = vi.fn();
    const { writes, writesSent } = page([new TypeError("Failed to fetch")], [EPOCH, problem(403, "identity-revoked", {}, null)], { onRevoked });
    writes.onEnded = vi.fn();
    expect(await writes.submit(spec())).toEqual({ kind: "set-aside", why: SIGN_IN_REFUSED, ambiguous: true });
    expect(onRevoked).toHaveBeenCalledTimes(1);
    expect(writes.onEnded).toHaveBeenCalledWith(expect.objectContaining({ kind: "revoked" }));
    expect(writesSent()).toHaveLength(1);
  });

  test("a 401 renews once and reads again", async () => {
    const renew = vi.fn(async () => true);
    const { writes, sent } = page([new TypeError("Failed to fetch"), created({ id: "c1" }, true)], [EPOCH, problem(401, "unauthenticated", {}, null), EPOCH], { renew });
    expect(await writes.submit(spec())).toMatchObject({ kind: "done", replayed: true });
    expect(renew).toHaveBeenCalledTimes(1);
    expect(sent.map((s) => s.path)).toEqual(["/recovery", "/clusters", "/recovery", "/recovery", "/clusters"]);
  });

  test("before the first send, a 401 the renewal does not clear sends nothing and ends the page", async () => {
    const { writes, writesSent } = page([], [problem(401, "unauthenticated", {}, null)], { renew: async () => false });
    writes.onEnded = vi.fn();
    expect(await writes.submit(spec())).toMatchObject({ kind: "not-done", why: SIGN_IN_REFUSED, problem: { type: "urn:bronzeward:problem:unauthenticated" } });
    expect(writes.onEnded).toHaveBeenCalledWith(expect.objectContaining({ kind: "unauthenticated" }));
    expect(writesSent()).toEqual([]);
  });
});

describe("set aside (§8.4 item 3, §8.5 item 2, journeys §8)", () => {
  test("AMB-05: the confirming read shows another epoch, so the write is not repeated", async () => {
    const { writes, sent } = page([new TypeError("Failed to fetch")], [EPOCH, "e2"]);
    expect(await writes.submit(spec())).toEqual({ kind: "set-aside", why: EPOCH_CHANGED_ASIDE, ambiguous: true });
    expect(sent.map((s) => s.path)).toEqual(["/recovery", "/clusters", "/recovery"]);
  });

  test("set aside before any attempt that may have committed, it is not open: nothing committed", async () => {
    const p = page([problem(503, "dependency-unavailable")], [EPOCH, "e2"]);
    expect(await p.writes.submit(spec())).toEqual({ kind: "set-aside", why: EPOCH_CHANGED_ASIDE, ambiguous: false });
    expect(p.writes.open).toEqual([]);
  });

  test("a confirming read with no epoch sets it aside", async () => {
    const { writes, writesSent } = page([new TypeError("Failed to fetch")], [EPOCH, null]);
    expect(await writes.submit(spec())).toMatchObject({ kind: "set-aside", why: NO_EPOCH });
    expect(writesSent()).toHaveLength(1);
  });

  test("AMB-07: a 409 conflict carrying an earlier epoch's record", async () => {
    const { writes } = page([new TypeError("Failed to fetch"), problem(409, "conflict", { request: "r1", epoch: "e0" })]);
    expect((await writes.submit(spec())).kind).toBe("set-aside");
  });

  test("AMB-12: a 412 epoch-mismatch", async () => {
    const { writes } = page([new TypeError("Failed to fetch"), problem(412, "epoch-mismatch", { epoch: "e2" }, "e2")]);
    expect(await writes.submit(spec())).toMatchObject({ kind: "set-aside", why: EPOCH_CHANGED_ASIDE, ambiguous: true });
  });

  test("AUTH-07: another sign-in before a repeat sends nothing more", async () => {
    const p = page([new TypeError("Failed to fetch")]);
    const run = p.writes.submit(spec());
    p.signIn("https://idp.invalid SYNTH-sub-2");
    expect(await run).toEqual({ kind: "set-aside", why: SIGN_IN_CHANGED, ambiguous: true });
    expect(p.writesSent()).toHaveLength(1);
  });

  test("setAside marks a write in flight and turns an unknown one aside; neither is repeatable", async () => {
    const drop = () => new TypeError("Failed to fetch");
    const p = page([drop(), drop(), drop(), drop(), drop()]);
    const unknown = spec();
    await p.writes.submit(unknown);
    const flying = spec();
    const run = p.writes.submit(flying);
    p.writes.setAside(EPOCH_CHANGED_ASIDE);
    expect(await run).toMatchObject({ kind: "set-aside", why: EPOCH_CHANGED_ASIDE });
    expect(unknown.outcome).toEqual({ kind: "set-aside", why: EPOCH_CHANGED_ASIDE, ambiguous: true });
    expect(p.writes.repeatable(unknown)).toBe(false);
    expect(await p.writes.submit(unknown)).toBe(unknown.outcome);
  });

  test("set aside during the last repeat, a write that then ends without an answer is set aside, not unknown", async () => {
    const drop = () => new TypeError("Failed to fetch");
    const p = page([drop(), drop(), drop(), () => (p.writes.setAside(EPOCH_CHANGED_ASIDE), drop())]);
    const w = spec();
    expect(await p.writes.submit(w)).toEqual({ kind: "set-aside", why: EPOCH_CHANGED_ASIDE, ambiguous: true });
    expect(p.writesSent()).toHaveLength(4);
    expect(p.writes.repeatable(w)).toBe(false);
    expect(p.writes.open).toEqual([w]);
  });
});

describe("[Repeat exactly]", () => {
  test("repeats under the same key after the checks, while the sign-in and epoch are the same", async () => {
    const drop = () => new TypeError("Failed to fetch");
    const p = page([drop(), drop(), drop(), drop(), created({ id: "c1" }, true)]);
    const w = spec();
    await p.writes.submit(w);
    p.sent.length = 0;
    expect(await p.writes.submit(w)).toMatchObject({ kind: "done", replayed: true });
    expect(p.sent.map((s) => s.path)).toEqual(["/recovery", "/clusters"]);
    expect(p.sent[1]!.headers["Idempotency-Key"]).toBe(w.key);
    expect(p.writes.open).toEqual([]);
  });

  test("keeps the unknown outcome it repeats as `repeating` while it runs", async () => {
    const drop = () => new TypeError("Failed to fetch");
    let seen: unknown;
    const p = page([drop(), drop(), drop(), drop(), () => ((seen = w.repeating), created({ id: "c1" }, true))]);
    const w = spec();
    const first = await p.writes.submit(w);
    await p.writes.submit(w);
    expect(seen).toBe(first);
    expect(w.repeating).toBeUndefined();
  });

  test("is not offered under another sign-in", async () => {
    const drop = () => new TypeError("Failed to fetch");
    const p = page([drop(), drop(), drop(), drop()]);
    const w = spec();
    await p.writes.submit(w);
    p.signIn("https://idp.invalid SYNTH-sub-2");
    expect(p.writes.repeatable(w)).toBe(false);
  });
});
