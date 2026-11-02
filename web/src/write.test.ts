import { describe, expect, test, vi } from "vitest";
import { type Attempt, Client, type ClientOptions } from "./request.ts";
import { type ShellEvents, ShellReads } from "./shell.tsx";
import { BACKOFF, EARLIER_EPOCH, EPOCH_CHANGED_ASIDE, INGEST_WAIT, MAX_BODY, NO_CREDENTIAL, NO_EPOCH, NO_EPOCH_HEADER, newKey, RECOVERY_START, RENEWED_LAST, SERVER_WAIT, SHELL_GONE, SIGN_IN_CHANGED, SIGN_IN_REFUSED, TOO_LARGE, Write, Writes } from "./write.ts";

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
// GET /recovery from `epochs`: an epoch, none (null), or a whole answer, given or made at its send.
function page(answers: (Response | Error | (() => Response | Error))[], epochs: (string | null | Response | (() => Response))[] = [], opts: ClientOptions = {}) {
  const sent: { method: string; path: string; headers: Record<string, string>; body: unknown }[] = [];
  const fetchMock = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
    const path = String(url).replace("/api/v1", "");
    const method = init?.method ?? "GET";
    sent.push({ method, path, headers: { ...(init?.headers as Record<string, string>) }, body: init?.body });
    const read = (e: string | null | Response | (() => Response)) => (typeof e === "function" ? e() : e instanceof Response ? e : recovery(e));
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
  return { client, writes, sent, waits, fetch: fetchMock as unknown as typeof fetch, signIn: (p: string | undefined) => (who = p), writesSent: () => sent.filter((s) => s.path !== "/recovery") };
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

describe("the spec's settled hook (§8.3)", () => {
  test("gets each outcome the write settles on, a [Repeat exactly]'s included", async () => {
    const answers = [new TypeError("Failed to fetch"), ...BACKOFF.map(() => new TypeError("Failed to fetch")), created({ id: "c1" }, true)];
    const { writes } = page(answers);
    const seen: string[] = [];
    const w = new Write({ method: "POST", path: "/clusters", body: { name: "lab" }, resource: "/clusters", settled: (o) => void seen.push(o.kind) });
    expect((await writes.submit(w)).kind).toBe("unknown");
    expect(seen).toEqual(["unknown"]);
    expect((await writes.submit(w)).kind).toBe("done");
    expect(seen).toEqual(["unknown", "done"]);
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

  test("§8.5: a success without a Bronzeward-Epoch header is saved, sent once, and reports the defect", async () => {
    const bare = new Response(JSON.stringify({ id: "c1" }), { status: 201, headers: { "Content-Type": "application/json" } });
    const { writes, writesSent } = page([bare]);
    expect(await writes.submit(spec())).toEqual({ kind: "done", status: 201, data: { id: "c1" }, replayed: false, defect: NO_EPOCH_HEADER });
    expect(writesSent()).toHaveLength(1);
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

  test("§8.1: a body over 1 MiB in UTF-8 is refused before any send, one of exactly 1 MiB is sent", async () => {
    // {"name":"…"} around the name: 11 bytes. Each é is two bytes and one UTF-16 unit, so the
    // larger body is about half a million characters long.
    const name = "a" + "é".repeat((MAX_BODY - 11 - 1) / 2);
    const at = (n: string) => new Write({ method: "POST", path: "/clusters", body: { name: n }, resource: "/clusters" });
    const big = page([]);
    expect(await big.writes.submit(at(name + "a"))).toEqual({ kind: "not-done", why: TOO_LARGE, problem: null });
    expect(big.sent).toEqual([]);
    const fits = page([created({ id: "c1" })]);
    expect(await fits.writes.submit(at(name))).toMatchObject({ kind: "done" });
    expect(new TextEncoder().encode(String(fits.writesSent()[0]!.body)).length).toBe(MAX_BODY);
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

  test("a 401 on the last attempt still renews: the sign-in stays and the write is not done", async () => {
    const renew = vi.fn(async () => true);
    const busy = () => problem(503, "dependency-unavailable");
    const { writes, writesSent } = page([busy(), busy(), busy(), problem(401, "unauthenticated", {}, null)], [], { renew });
    writes.onEnded = vi.fn();
    expect(await writes.submit(spec())).toMatchObject({ kind: "not-done", why: RENEWED_LAST, problem: { type: "urn:bronzeward:problem:unauthenticated" } });
    expect(renew).toHaveBeenCalledTimes(1);
    expect(writesSent()).toHaveLength(4);
    expect(writes.onEnded).not.toHaveBeenCalled();
  });

  test("the shell that held the client goes: no write is repeatable, and the notices render again", async () => {
    const drop = () => new TypeError("Failed to fetch");
    const { writes, client } = page([drop(), drop(), drop(), drop()]);
    const w = spec();
    await writes.submit(w);
    expect(writes.repeatable(w)).toBe(true);
    const rendered = vi.fn();
    writes.subscribe(rendered);
    writes.disconnect(new Client({ token: () => "SYNTH-token" }));
    expect([writes.repeatable(w), rendered.mock.calls.length]).toEqual([true, 0]);
    writes.disconnect(client);
    expect([writes.repeatable(w), rendered.mock.calls.length]).toEqual([false, 1]);
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

  test("a first send whose answer handling throws, as a renewal while its epoch is learned, may have committed: unknown", async () => {
    const writes = new Writes(() => PRINCIPAL, async () => {});
    const sends: string[] = [];
    writes.client = {
      currentEpoch: EPOCH,
      attempt: async (_m: string, path: string) => {
        sends.push(path);
        throw new Error("synthetic renewal failure");
      },
    } as unknown as Client;
    const w = spec();
    expect((await writes.submit(w)).kind).toBe("unknown");
    expect(sends).toEqual(["/clusters"]);
    expect(writes.open).toEqual([w]);
  });
});

describe("the confirming read's credential refusals (§8.2)", () => {
  test("identity-revoked clears the credential, ends the page and sets the write aside", async () => {
    const onRevoked = vi.fn();
    const { writes, writesSent } = page([new TypeError("Failed to fetch")], [EPOCH, problem(403, "identity-revoked", {}, null)], { onRevoked });
    writes.onEnded = vi.fn();
    expect(await writes.submit(spec())).toEqual({ kind: "set-aside", why: SIGN_IN_REFUSED, ambiguous: true, problem: null });
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

  test("§8.4 item 2: an answer whose status is outside 100-599 is no answer: repeated under the key, then unknown, never not saved", async () => {
    const keys: string[] = [];
    const writes = new Writes(() => PRINCIPAL, async () => {});
    writes.client = {
      currentEpoch: EPOCH,
      attempt: async (_m: string, path: string, h: Record<string, string>) => {
        if (path === "/recovery") return { status: 200, headers: new Headers({ "Bronzeward-Epoch": EPOCH }), data: {} };
        keys.push(h["Idempotency-Key"]!);
        // A status line cut off over plain HTTP, as a browser reads it (fixtures/webhost).
        return { status: 5, headers: new Headers(), data: null };
      },
    } as unknown as Client;
    const w = spec();
    expect(await writes.submit(w)).toMatchObject({ kind: "unknown" });
    expect(keys).toEqual([w.key, w.key, w.key, w.key]);
    expect(w.ambiguous).toBe(true);
  });

  // A client whose write answers come with the ending its epoch read met (request.ts learnEpoch).
  const ending = { kind: "revoked" as const, problem: { type: "urn:bronzeward:problem:identity-revoked", title: "Identity revoked", status: 403 } };
  function ended(answer: Attempt | undefined) {
    const sends: string[] = [];
    const writes = new Writes(() => PRINCIPAL, async () => {});
    writes.client = {
      currentEpoch: EPOCH,
      attempt: async (_m: string, path: string, _h: unknown, _b: unknown, end: { e?: unknown }) => {
        sends.push(path);
        if (path === "/recovery") return { status: 200, headers: new Headers({ "Bronzeward-Epoch": EPOCH }), data: {} };
        end.e = ending;
        return answer;
      },
    } as unknown as Client;
    const outcomes: unknown[] = [];
    const w = spec();
    writes.onEnded = vi.fn(() => void outcomes.push(w.outcome));
    return { writes, w, sends, outcomes };
  }

  test("refused on the read that learns a write's epoch: a success stands and the page ends after it", async () => {
    const { writes, w, outcomes } = ended({ status: 201, headers: new Headers(), data: { id: "c1" } });
    expect(await writes.submit(w)).toMatchObject({ kind: "done", status: 201 });
    expect(writes.onEnded).toHaveBeenCalledWith(ending);
    expect(outcomes).toEqual([expect.objectContaining({ kind: "done" })]);
  });

  test("refused on the read that learns a lost write's epoch: nothing is repeated, the write is set aside and the page ends", async () => {
    const { writes, w, sends } = ended(undefined);
    expect(await writes.submit(w)).toEqual({ kind: "set-aside", why: SIGN_IN_REFUSED, ambiguous: true, problem: null });
    expect(sends).toEqual(["/clusters"]);
    expect(writes.onEnded).toHaveBeenCalledWith(ending);
    expect(writes.open).toEqual([w]);
  });

  test("refused on that read while the write's own body has not arrived: the page ends at once, and once", async () => {
    const writes = new Writes(() => PRINCIPAL, async () => {});
    let answer: (a: Attempt) => void = () => {};
    writes.client = {
      currentEpoch: EPOCH,
      attempt: (_m: string, _p: string, _h: unknown, _b: unknown, end: { e?: unknown; now?: (e: unknown) => void }) => {
        end.e = ending;
        end.now?.(ending);
        return new Promise((r) => (answer = r));
      },
    } as unknown as Client;
    writes.onEnded = vi.fn();
    const w = spec();
    const run = writes.submit(w);
    await vi.waitFor(() => expect(writes.onEnded).toHaveBeenCalledWith(ending));
    expect(w.outcome).toBeUndefined();
    answer({ status: 201, headers: new Headers(), data: { id: "c1" } });
    expect(await run).toMatchObject({ kind: "done" });
    expect(writes.onEnded).toHaveBeenCalledTimes(1);
  });
});

describe("a fragment write's in-progress 409 (§8.4 item 2, AMB-02)", () => {
  const fragment = () =>
    new Write({ method: "PUT", path: "/drafts/drf_1/fragments/f", body: { layer: "role", document: "a: 1\n" }, ifMatch: '"6-t"', resource: "/drafts/drf_1", ingests: true });
  const inProgress = (extra: Record<string, unknown> = {}) => problem(409, "conflict", { ingestion: "ing_1", ...extra });
  // The last in-progress answer's problem, which every unknown or set-aside outcome carries (§8.2).
  const inProgressProblem = { type: "urn:bronzeward:problem:conflict", title: "conflict", status: 409, ingestion: "ing_1" };
  const server = (leaseSeconds: number) =>
    new Response(JSON.stringify({ ingestionClaim: { leaseSeconds, absoluteExpirySeconds: 3600 } }), { status: 200, headers: { "Content-Type": "application/json", "Bronzeward-Epoch": EPOCH } });
  const ok = () => new Response(JSON.stringify({ draft: "drf_1" }), { status: 200, headers: { "Content-Type": "application/json", "Bronzeward-Epoch": EPOCH, "Idempotent-Replayed": "true" } });

  test("a repeat's in-progress 409 is repeated with the same key after 1, 2, then 4 s, each after the checks, and counts as no repeat", async () => {
    const drop = () => new TypeError("Failed to fetch");
    const { writes, waits, sent, writesSent } = page([drop(), inProgress(), server(30), inProgress(), inProgress(), inProgress(), ok()]);
    const w = fragment();
    expect(await writes.submit(w)).toEqual({ kind: "done", status: 200, data: { draft: "drf_1" }, replayed: true });
    // The backoff's first wait, then the in-progress waits; the three repeats are not spent.
    expect(waits).toEqual([1000, 1000, 2000, 4000, 4000]);
    expect(new Set(writesSent().filter((s) => s.method === "PUT").map((s) => s.headers["Idempotency-Key"]))).toEqual(new Set([w.key]));
    expect(sent.filter((s) => s.path === "/server")).toHaveLength(1);
    expect(sent.filter((s) => s.path === "/recovery")).toHaveLength(6);
    expect(w.ingesting).toBeUndefined();
  });

  test("names the ingestion while it waits", async () => {
    const seen: (string | undefined)[] = [];
    const { writes } = page([new TypeError("Failed to fetch"), inProgress(), server(30), ok()]);
    const w = fragment();
    writes.subscribe(() => seen.push(w.ingesting));
    await writes.submit(w);
    expect(seen).toContain("ing_1");
    expect(seen.at(-1)).toBeUndefined();
  });

  test("still in progress at the bound, the outcome is unknown", async () => {
    const { writes, waits } = page([new TypeError("Failed to fetch"), inProgress(), server(30), ...Array.from({ length: 40 }, () => inProgress())]);
    const o = await writes.submit(fragment());
    expect(o).toEqual({ kind: "unknown", why: "the first request with this key was still ingesting (ingestion ing_1) after 120 s", problem: inProgressProblem });
    expect(waits.slice(1).reduce((a, b) => a + b, 0)).toBe(INGEST_WAIT);
  });

  test("the bound counts the time the requests take, not only the waits", async () => {
    // Each answer to the repeat takes 5 s; the waits alone would reach 120 s only after about 30 of them.
    let clock = 0;
    const slow = () => ((clock += 5000), inProgress());
    const env = page([new TypeError("Failed to fetch"), slow, server(30), ...Array.from({ length: 40 }, () => slow)]);
    const writes = new Writes(() => PRINCIPAL, async (ms) => void (clock += ms), () => clock);
    writes.client = env.client;
    const o = await writes.submit(fragment());
    expect(o).toMatchObject({ kind: "unknown", problem: inProgressProblem });
    expect((o as { why: string }).why).toMatch(/still ingesting \(ingestion ing_1\) after 12\d s$/);
    expect(clock).toBeLessThan(INGEST_WAIT + 10_000);
  });

  test("a served claim lease longer than the wait raises the bound", async () => {
    const { writes, waits } = page([new TypeError("Failed to fetch"), inProgress(), server(300), ...Array.from({ length: 90 }, () => inProgress())]);
    const o = await writes.submit(fragment());
    expect(o).toEqual({ kind: "unknown", why: "the first request with this key was still ingesting (ingestion ing_1) after 300 s", problem: inProgressProblem });
    expect(waits.slice(1).reduce((a, b) => a + b, 0)).toBe(300_000);
  });

  test("ginsys/bronzeward#517 H12: the bound's /server read ends the sign-in on a final 401 and stops at a recovery start", async () => {
    let p = page([new TypeError("Failed to fetch"), inProgress(), problem(401, "unauthenticated"), ...Array.from({ length: 5 }, () => inProgress())]);
    p.writes.onEnded = vi.fn();
    expect(await p.writes.submit(fragment())).toEqual({ kind: "set-aside", why: SIGN_IN_REFUSED, ambiguous: true, problem: inProgressProblem });
    expect(p.writes.onEnded).toHaveBeenCalledTimes(1);
    expect(p.writesSent().filter((s) => s.method === "PUT")).toHaveLength(2);
    p = page([new TypeError("Failed to fetch"), inProgress(), problem(409, "recovery-mode-active"), ...Array.from({ length: 5 }, () => inProgress())]);
    const told: string[] = [];
    p.writes.onPreEntry = (q) => void told.push(q.type);
    expect(await p.writes.submit(fragment())).toEqual({ kind: "set-aside", why: RECOVERY_START, ambiguous: true, problem: inProgressProblem });
    expect(told).toEqual(["urn:bronzeward:problem:recovery-mode-active"]);
    expect(p.writesSent().filter((s) => s.method === "PUT")).toHaveLength(2);
  });

  test("ginsys/bronzeward#517 H13: a /server read that stalls bounds the wait at INGEST_WAIT once SERVER_WAIT passed", async () => {
    vi.useFakeTimers();
    try {
      const stall = () => new Promise<Response>(() => {}) as unknown as Response;
      const { writes, waits } = page([new TypeError("Failed to fetch"), inProgress(), stall, ...Array.from({ length: 40 }, () => inProgress())]);
      const run = writes.submit(fragment());
      await vi.advanceTimersByTimeAsync(SERVER_WAIT);
      expect(await run).toEqual({ kind: "unknown", why: "the first request with this key was still ingesting (ingestion ing_1) after 120 s", problem: inProgressProblem });
      expect(waits.slice(1).reduce((a, b) => a + b, 0)).toBe(INGEST_WAIT);
    } finally {
      vi.useRealTimers();
    }
  });

  test("ginsys/bronzeward#517 H13: a final 401 the stalled /server read gets after its write settled still ends the page, once", async () => {
    vi.useFakeTimers();
    try {
      let answer: (r: Response) => void = () => {};
      const late = () => new Promise<Response>((r) => (answer = r)) as unknown as Response;
      const { writes } = page([new TypeError("Failed to fetch"), inProgress(), late, ...Array.from({ length: 40 }, () => inProgress())]);
      writes.onEnded = vi.fn();
      const run = writes.submit(fragment());
      await vi.advanceTimersByTimeAsync(SERVER_WAIT);
      expect(await run).toMatchObject({ kind: "unknown" });
      expect(writes.onEnded).not.toHaveBeenCalled();
      answer(problem(401, "unauthenticated"));
      await vi.waitFor(() => expect(writes.onEnded).toHaveBeenCalledWith(expect.objectContaining({ kind: "unauthenticated" })));
      expect(writes.onEnded).toHaveBeenCalledTimes(1);
    } finally {
      vi.useRealTimers();
    }
  });

  test("ginsys/bronzeward#517 H14: when the /server read fails, the lease the shell read still raises the bound", async () => {
    const { writes, waits } = page([new TypeError("Failed to fetch"), inProgress(), problem(503, "unavailable"), ...Array.from({ length: 90 }, () => inProgress())]);
    writes.lease = 300;
    const o = await writes.submit(fragment());
    expect(o).toEqual({ kind: "unknown", why: "the first request with this key was still ingesting (ingestion ing_1) after 300 s", problem: inProgressProblem });
    expect(waits.slice(1).reduce((a, b) => a + b, 0)).toBe(300_000);
  });

  test("a done write carries the answer's ETag (§8.3)", async () => {
    const answer = ok();
    answer.headers.set("ETag", '"7-u"');
    const { writes } = page([answer]);
    expect(await writes.submit(fragment())).toEqual({ kind: "done", status: 200, data: { draft: "drf_1" }, replayed: true, etag: '"7-u"' });
  });

  test("a 409 with ingestion and draft (published meanwhile) is refused and not repeated", async () => {
    const { writes, writesSent } = page([new TypeError("Failed to fetch"), inProgress({ draft: "drf_1" })]);
    const o = await writes.submit(fragment());
    expect(o).toMatchObject({ kind: "refused", status: 409 });
    expect(writesSent()).toHaveLength(2);
  });

  test("a replayed 409 with ingestion is a recorded refusal, not repeated", async () => {
    const replayed = inProgress();
    replayed.headers.set("Idempotent-Replayed", "true");
    const { writes, writesSent } = page([new TypeError("Failed to fetch"), replayed]);
    const o = await writes.submit(fragment());
    expect(o).toMatchObject({ kind: "refused", status: 409, replayed: true });
    expect(writesSent()).toHaveLength(2);
  });

  test("another sign-in during the wait sets it aside, and nothing more is sent", async () => {
    // The sign-in changes once the 409 is answered, before the wait ends.
    let signIn = (_: string) => {};
    const env = page([new TypeError("Failed to fetch"), inProgress(), () => (signIn("https://idp.invalid SYNTH-sub-2"), server(30))]);
    signIn = env.signIn;
    expect(await env.writes.submit(fragment())).toEqual({ kind: "set-aside", why: SIGN_IN_CHANGED, ambiguous: true, problem: inProgressProblem });
    expect(env.writesSent().filter((s) => s.method === "PUT")).toHaveLength(2);
  });

  test("a write that does not ingest refuses the same 409", async () => {
    const { writes, writesSent } = page([new TypeError("Failed to fetch"), inProgress()]);
    const o = await writes.submit(spec());
    expect(o).toMatchObject({ kind: "refused", status: 409 });
    expect(writesSent()).toHaveLength(2);
  });
});

describe("set aside (§8.4 item 3, §8.5 item 2, journeys §8)", () => {
  test("AMB-05: the confirming read shows another epoch, so the write is not repeated", async () => {
    const { writes, sent } = page([new TypeError("Failed to fetch")], [EPOCH, "e2"]);
    expect(await writes.submit(spec())).toEqual({ kind: "set-aside", why: EPOCH_CHANGED_ASIDE, ambiguous: true, problem: null });
    expect(sent.map((s) => s.path)).toEqual(["/recovery", "/clusters", "/recovery"]);
  });

  test("set aside before any attempt that may have committed, it is not open: nothing committed, with the last 503's problem", async () => {
    const p = page([problem(503, "dependency-unavailable")], [EPOCH, "e2"]);
    expect(await p.writes.submit(spec())).toEqual({ kind: "set-aside", why: EPOCH_CHANGED_ASIDE, ambiguous: false, problem: { type: "urn:bronzeward:problem:dependency-unavailable", title: "dependency-unavailable", status: 503 } });
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

  test("an earlier epoch's record is unknown and open even when every earlier answer committed nothing: the record shows a commit (§8.4 item 7)", async () => {
    const p = page([problem(503, "dependency-unavailable"), problem(409, "conflict", { request: "r1", epoch: "e0" })]);
    const w = spec();
    expect(await p.writes.submit(w)).toEqual({
      kind: "set-aside", why: EARLIER_EPOCH, ambiguous: true, problem: { type: "urn:bronzeward:problem:conflict", title: "conflict", status: 409, request: "r1", epoch: "e0" },
    });
    expect(p.writes.open).toEqual([w]);
  });

  test("AMB-12: a 412 epoch-mismatch", async () => {
    const { writes } = page([new TypeError("Failed to fetch"), problem(412, "epoch-mismatch", { epoch: "e2" }, "e2")]);
    expect(await writes.submit(spec())).toMatchObject({ kind: "set-aside", why: EPOCH_CHANGED_ASIDE, ambiguous: true });
  });

  test("AUTH-07: another sign-in before a repeat sends nothing more", async () => {
    // The other sign-in arrives while the first send is answered.
    const p = page([() => (p.signIn("https://idp.invalid SYNTH-sub-2"), new TypeError("Failed to fetch"))]);
    expect(await p.writes.submit(spec())).toEqual({ kind: "set-aside", why: SIGN_IN_CHANGED, ambiguous: true, problem: null });
    expect(p.writesSent()).toHaveLength(1);
  });

  test.each([
    ["a sign-out", SIGN_IN_CHANGED, true],
    ["the end of the shell that held the client", SHELL_GONE, false],
  ])("%s while a read that learns the epoch is answered sends nothing more", async (_, why, signOut) => {
    const go = () => {
      if (signOut) p.signIn(undefined);
      p.writes.disconnect(p.client);
      return recovery();
    };
    // The read before a repeat: the write is set aside after its one dropped attempt.
    let p = page([new TypeError("Failed to fetch")], [EPOCH, go]);
    expect(await p.writes.submit(spec())).toEqual({ kind: "set-aside", why, ambiguous: true, problem: null });
    expect(p.writesSent()).toHaveLength(1);
    // The read before the first send: nothing is sent.
    p = page([], [go]);
    expect(await p.writes.submit(spec())).toEqual({ kind: "not-done", why, problem: null });
    expect(p.writesSent()).toHaveLength(0);
  });

  test("a sign-out between the last check before a repeat and its send sends nothing more", async () => {
    const p = page([new TypeError("Failed to fetch")], [EPOCH, EPOCH]);
    // The checks pass; the sign-out lands before run() resumes after them.
    const writes = p.writes as unknown as { check: (...a: unknown[]) => Promise<string | undefined> };
    const check = writes.check.bind(p.writes);
    writes.check = async (...a) => {
      const why = await check(...a);
      p.signIn(undefined);
      return why;
    };
    expect(await p.writes.submit(spec())).toEqual({ kind: "set-aside", why: SIGN_IN_CHANGED, ambiguous: true, problem: null });
    expect(p.writesSent()).toHaveLength(1);
  });

  test("a recovery start's process refusal of a write or of its confirming read is told to the shell once the write settles", async () => {
    const start = (scope?: string) => problem(409, "recovery-mode-active", scope === undefined ? {} : { scope });
    // Of the write: refused, and told once; a scope's refusal says nothing of the start.
    let p = page([start()]);
    let told: string[] = [];
    const tell = (w: Writes) => (w.onPreEntry = (q) => void told.push(`${q.type} ${w.open.length}`));
    tell(p.writes);
    expect(await p.writes.submit(spec())).toMatchObject({ kind: "refused", status: 409 });
    expect(told).toEqual(["urn:bronzeward:problem:recovery-mode-active 0"]);
    p = page([start("mch_a")]);
    told = [];
    tell(p.writes);
    await p.writes.submit(spec());
    expect(told).toEqual([]);
    // Of the confirming read before a repeat, under the same epoch: the dropped write is set aside
    // and not sent again, and the shell is told although no write answer follows.
    p = page([new TypeError("Failed to fetch")], [EPOCH, start()]);
    tell(p.writes);
    expect(await p.writes.submit(spec())).toEqual({ kind: "set-aside", why: RECOVERY_START, ambiguous: true, problem: null });
    expect(p.writesSent()).toHaveLength(1);
    expect(told).toEqual(["urn:bronzeward:problem:recovery-mode-active 1"]);
    // Of the read that learns the first send's epoch: nothing is sent.
    p = page([], [start()]);
    told = [];
    tell(p.writes);
    expect(await p.writes.submit(spec())).toMatchObject({ kind: "not-done", why: RECOVERY_START, problem: { type: "urn:bronzeward:problem:recovery-mode-active" } });
    expect(p.writesSent()).toHaveLength(0);
    expect(told).toEqual(["urn:bronzeward:problem:recovery-mode-active 0"]);
  });

  test.each([
    ["without a scope, a recovery start: the shell is told once", undefined, 1],
    ["with a scope, no recovery start: the shell is told nothing", "mch_a", 0],
  ])("a write answered after the page saw another epoch, whose GET /recovery is a 409 recovery-mode-active %s", async (_, scope, times) => {
    // The first epoch read shows e1; the page sees e2 while the write is answered under e1, so the
    // client reads GET /recovery to learn which is current (request.ts learnEpoch).
    const p = page(
      [() => ((p.client as unknown as { observeEpoch(r: Response): boolean }).observeEpoch(recovery("e2")), created({ id: "c1" }))],
      [EPOCH, problem(409, "recovery-mode-active", scope === undefined ? {} : { scope }, null)],
    );
    p.writes.onPreEntry = vi.fn();
    expect(await p.writes.submit(spec())).toMatchObject({ kind: "done", status: 201 });
    expect(p.sent.map((s) => s.path)).toEqual(["/recovery", "/clusters", "/recovery"]);
    expect(p.writes.onPreEntry).toHaveBeenCalledTimes(times);
  });

  test("that recovery start reaches the shell at once while the write's own body has not arrived, and once", async () => {
    let body: ReadableStreamDefaultController<Uint8Array> | undefined;
    const stalled = () =>
      new Response(new ReadableStream<Uint8Array>({ start: (c) => void (body = c) }), { status: 201, headers: { "Content-Type": "application/json", "Bronzeward-Epoch": EPOCH } });
    const p = page(
      [() => ((p.client as unknown as { observeEpoch(r: Response): boolean }).observeEpoch(recovery("e2")), stalled())],
      [EPOCH, problem(409, "recovery-mode-active", {}, null)],
    );
    p.writes.onPreEntry = vi.fn();
    const w = spec();
    const run = p.writes.submit(w);
    await vi.waitFor(() => expect(p.writes.onPreEntry).toHaveBeenCalledTimes(1));
    expect(w.outcome).toBeUndefined();
    body!.enqueue(new TextEncoder().encode(JSON.stringify({ id: "c1" })));
    body!.close();
    expect(await run).toMatchObject({ kind: "done", status: 201 });
    expect(p.writes.onPreEntry).toHaveBeenCalledTimes(1);
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
    expect(unknown.outcome).toEqual({ kind: "set-aside", why: EPOCH_CHANGED_ASIDE, ambiguous: true, problem: null });
    expect(p.writes.repeatable(unknown)).toBe(false);
    expect(await p.writes.submit(unknown)).toBe(unknown.outcome);
  });

  test("set aside during the last repeat, a write that then ends without an answer is set aside, not unknown", async () => {
    const drop = () => new TypeError("Failed to fetch");
    const p = page([drop(), drop(), drop(), () => (p.writes.setAside(EPOCH_CHANGED_ASIDE), drop())]);
    const w = spec();
    expect(await p.writes.submit(w)).toEqual({ kind: "set-aside", why: EPOCH_CHANGED_ASIDE, ambiguous: true, problem: null });
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

  test("a settled repeat stays in the notices with its result until dismissed, but no longer holds the page", async () => {
    const drop = () => new TypeError("Failed to fetch");
    const p = page([drop(), drop(), drop(), drop(), created({ id: "c1" }, true)]);
    const w = spec();
    await p.writes.submit(w);
    await p.writes.submit(w);
    expect(p.writes.open).toEqual([]);
    expect(p.writes.notices).toEqual([w]);
    expect(w.outcome).toMatchObject({ kind: "done", replayed: true });
    p.writes.dismiss(w);
    expect(p.writes.notices).toEqual([]);
  });

  test("a first send that settles leaves no notice", async () => {
    const p = page([created({ id: "c1" })]);
    await p.writes.submit(spec());
    expect(p.writes.notices).toEqual([]);
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

describe("a notice once shown stays until dismissed (B11)", () => {
  test("Add cluster loses its first answer and an automatic repeat succeeds: the notice shows the result until dismissed", async () => {
    let shown: Write[] = [];
    const p = page([new TypeError("Failed to fetch"), () => ((shown = p.writes.notices), created({ id: "c1" }, true))]);
    const w = spec();
    expect(await p.writes.submit(w)).toMatchObject({ kind: "done", replayed: true });
    expect(shown).toEqual([w]);
    expect(p.writes.open).toEqual([]);
    expect(p.writes.notices).toEqual([w]);
    p.writes.dismiss(w);
    expect(p.writes.notices).toEqual([]);
  });

  test("set aside while its answer is on its way, a write that then settles keeps its notice until dismissed", async () => {
    let answer: (r: Response) => void = () => {};
    const p = page([() => new Promise<Response>((r) => (answer = r)) as unknown as Response]);
    const w = spec();
    const run = p.writes.submit(w);
    await vi.waitFor(() => expect(p.writesSent()).toHaveLength(1));
    p.writes.setAside(EPOCH_CHANGED_ASIDE);
    expect(p.writes.notices).toEqual([w]);
    answer(problem(422, "invalid-request"));
    expect((await run).kind).toBe("refused");
    expect(p.writes.notices).toEqual([w]);
    p.writes.dismiss(w);
    expect(p.writes.notices).toEqual([]);
  });

  test("a 500 the repeat answers as done keeps its notice too", async () => {
    const p = page([problem(500, "internal-error"), created({ id: "c1" }, true)]);
    const w = spec();
    await p.writes.submit(w);
    expect(p.writes.notices).toEqual([w]);
  });
});

describe("an unknown or set-aside outcome keeps the problem of the answer behind it (§8.2)", () => {
  const forbidden = () => problem(403, "forbidden", { roles: ["author"], instance: "req-1" });

  test("Add cluster loses its first answer, then a repeat is refused 403 forbidden: unknown, with that refusal's roles and fields", async () => {
    const { writes } = page([new TypeError("Failed to fetch"), forbidden()]);
    expect(await writes.submit(spec())).toEqual({
      kind: "unknown",
      why: "the server answered 403 forbidden",
      problem: { type: "urn:bronzeward:problem:forbidden", title: "forbidden", status: 403, roles: ["author"], instance: "req-1" },
    });
  });

  test("after an ambiguous attempt, repeats answered 503 to the end: unknown, with the last 503's problem", async () => {
    const { writes } = page([new TypeError("Failed to fetch"), ...Array.from({ length: 3 }, () => problem(503, "transient-conflict"))]);
    expect(await writes.submit(spec())).toMatchObject({ kind: "unknown", problem: { type: "urn:bronzeward:problem:transient-conflict" } });
  });

  test("a dropped last attempt has no problem, and neither has a set-aside no answer decided", async () => {
    const drop = () => new TypeError("Failed to fetch");
    expect(await page([drop(), drop(), drop(), drop()]).writes.submit(spec())).toMatchObject({ kind: "unknown", problem: null });
    expect(await page([drop()], [EPOCH, "e2"]).writes.submit(spec())).toMatchObject({ kind: "set-aside", problem: null });
  });

  test("after a 500, a repeat set aside by its epoch check keeps the 500's problem", async () => {
    expect(await page([problem(500, "internal-error")], [EPOCH, "e2"]).writes.submit(spec())).toMatchObject({
      kind: "set-aside",
      why: EPOCH_CHANGED_ASIDE,
      ambiguous: true,
      problem: { type: "urn:bronzeward:problem:internal-error" },
    });
  });

  test("set aside by a 412 epoch-mismatch or a 409 earlier-epoch record, it keeps that answer's problem", async () => {
    const mismatch = await page([new TypeError("Failed to fetch"), problem(412, "epoch-mismatch", { epoch: "e2" }, "e2")]).writes.submit(spec());
    expect(mismatch).toMatchObject({ kind: "set-aside", problem: { type: "urn:bronzeward:problem:epoch-mismatch" } });
    const conflict = await page([new TypeError("Failed to fetch"), problem(409, "conflict", { request: "r1", epoch: "e0" })]).writes.submit(spec());
    expect(conflict).toMatchObject({ kind: "set-aside", problem: { type: "urn:bronzeward:problem:conflict" } });
  });

  test("while the repeats run after a 500, the unknown state carries the 500's problem; setAside keeps an unknown one's", async () => {
    let seen: unknown;
    const p = page([problem(500, "internal-error"), () => ((seen = w.doubt), new TypeError("Failed to fetch")), new TypeError("Failed to fetch"), new TypeError("Failed to fetch")]);
    const w = spec();
    await p.writes.submit(w);
    expect(seen).toMatchObject({ kind: "unknown", why: "the server answered 500 internal-error", problem: { type: "urn:bronzeward:problem:internal-error" } });
    const q = page([new TypeError("Failed to fetch"), forbidden()]);
    const v = spec();
    await q.writes.submit(v);
    q.writes.setAside(EPOCH_CHANGED_ASIDE);
    expect(v.outcome).toMatchObject({ kind: "set-aside", problem: { type: "urn:bronzeward:problem:forbidden" } });
  });
});

describe("a body that never ends does not hold a write", () => {
  const stalled = (status: number, type: string, epoch: string | null = EPOCH) =>
    new Response(new ReadableStream({ start: () => {} }), { status, headers: { "Content-Type": type, ...(epoch === null ? {} : { "Bronzeward-Epoch": epoch }) } });

  test("after a dropped write, the confirming read's success names the epoch in its headers: the repeat is sent and settles", async () => {
    const p = page([new TypeError("Failed to fetch"), created({ id: "c1" }, true)], [stalled(200, "application/json"), stalled(200, "application/json")]);
    expect(await p.writes.submit(spec())).toMatchObject({ kind: "done", replayed: true });
    expect(p.sent.map((s) => s.path)).toEqual(["/recovery", "/clusters", "/recovery", "/clusters"]);
  });

  test("a confirming read refused other than for the sign-in is not waited for: the write is set aside", async () => {
    const proxy = await submitWith(stalled(502, "text/html", null));
    expect(proxy).toEqual({ kind: "set-aside", why: NO_EPOCH, ambiguous: true, problem: null });
    const busy = await submitWith(stalled(503, "application/problem+json", null));
    expect(busy).toEqual({ kind: "set-aside", why: NO_EPOCH, ambiguous: true, problem: null });
  });

  async function submitWith(confirming: Response) {
    const p = page([new TypeError("Failed to fetch")], [EPOCH, confirming]);
    return p.writes.submit(spec());
  }
});

// §8.2, §9.6: where the client's sign-in ends, every pending write of that client stops in the same
// step, before any further send. No shell is bound here, so only the client stops them.
describe("a sign-in that ends stops every other write of its client at once", () => {
  // Write A's first attempt gets no answer, and A waits to repeat until `go`. Its repeat would be the
  // POST after the answers in `rest`.
  async function waiting(rest: Response[], opts: ClientOptions = {}, make?: (f: typeof fetch) => Client) {
    let go!: () => void;
    const gate = new Promise<void>((r) => (go = r));
    const p = page([new TypeError("Failed to fetch"), ...rest, created({ id: "c1" })], [], opts);
    const client = make?.(p.fetch) ?? p.client;
    const writes = new Writes(() => PRINCIPAL, () => gate);
    writes.client = client;
    const a = writes.submit(spec());
    await vi.waitFor(() => expect(p.writesSent()).toHaveLength(1));
    return { ...p, client, writes, a, go };
  }

  test.each([
    ["a write's identity-revoked", 403, "identity-revoked", true],
    ["a write's 401 that its renewal did not clear", 401, "unauthenticated", true],
    ["a read's identity-revoked", 403, "identity-revoked", false],
    ["a read's 401 that its renewal did not clear", 401, "unauthenticated", false],
  ])("%s: the waiting write sends nothing more", async (_, status, code, write) => {
    let token: string | undefined = "SYNTH-token";
    const t = await waiting([problem(status, code)], { token: () => token, onRevoked: () => (token = undefined) });
    if (write) expect(await t.writes.submit(spec())).toMatchObject({ kind: "refused", status });
    else expect(await t.client.read("/server")).toMatchObject({ kind: status === 401 ? "unauthenticated" : "revoked" });
    expect(t.client.writesEnded).toBe(true);
    t.go();
    expect(await t.a).toEqual({ kind: "set-aside", why: SHELL_GONE, ambiguous: true, problem: null });
    expect(t.writesSent()).toHaveLength(2);
  });

  test("a sign-out: the waiting write sends nothing more", async () => {
    let reads!: ShellReads;
    const t = await waiting([], {}, (f) => (reads = new ShellReads({ token: () => "SYNTH-token", fetch: f }, () => ({}) as ShellEvents)).client);
    reads.signOut(() => {});
    t.go();
    expect(await t.a).toEqual({ kind: "set-aside", why: SHELL_GONE, ambiguous: true, problem: null });
    expect(t.writesSent()).toHaveLength(1);
  });

  test("negative control: a refusal that leaves the sign-in standing lets the waiting write repeat", async () => {
    const t = await waiting([problem(403, "forbidden")]);
    expect(await t.writes.submit(spec())).toMatchObject({ kind: "refused", status: 403 });
    expect(t.client.writesEnded).toBe(false);
    t.go();
    expect(await t.a).toMatchObject({ kind: "done", status: 201 });
    expect(t.writesSent()).toHaveLength(3);
  });

  // The page dropped the credential while the repeat's confirming read was answered, before any
  // shell handling: no POST leaves without it.
  test("a send whose credential is gone ends as not sent", async () => {
    let token: string | undefined = "SYNTH-token";
    const drop = () => ((token = undefined), recovery());
    const { writes, sent, writesSent } = page([new TypeError("Failed to fetch"), created({ id: "c1" })], [EPOCH, drop], { token: () => token });
    expect(await writes.submit(spec())).toEqual({ kind: "set-aside", why: NO_CREDENTIAL, ambiguous: true, problem: null });
    expect(writesSent()).toHaveLength(1);
    expect(sent.filter((s) => s.method === "POST").every((s) => s.headers["Authorization"] === "Bearer SYNTH-token")).toBe(true);
  });
});

describe("the recovery entry's exception (§8.4 item 3, REC-04 (a))", () => {
  const entry = () => new Write({ method: "POST", path: "/recovery/entries", body: { reason: "SYNTH" }, resource: "/recovery", entry: true });
  const drop = () => new TypeError("Failed to fetch");
  const observe = (p: ReturnType<typeof page>, epoch: string) => (p.client as unknown as { observeEpoch(r: Response): boolean }).observeEpoch(recovery(epoch));
  // The entry's answer names the epoch its commit minted.
  const minted = (replayed = true) => new Response("{}", { status: 201, headers: { "Content-Type": "application/json", "Bronzeward-Epoch": "e2", ...(replayed ? { "Idempotent-Replayed": "true" } : {}) } });

  test("sends no Bronzeward-Expected-Epoch and reads no epoch before its first send", async () => {
    const p = page([created({ epoch: "e2" })]);
    const w = entry();
    expect(await p.writes.submit(w)).toMatchObject({ kind: "done", status: 201 });
    expect(p.sent.map((s) => `${s.method} ${s.path}`)).toEqual(["POST /recovery/entries"]);
    expect(p.sent[0]!.headers["Bronzeward-Expected-Epoch"]).toBeUndefined();
    expect(p.sent[0]!.headers["Idempotency-Key"]).toBe(w.key);
  });

  test.each([
    ["the 201 was dropped", drop],
    ["the server answered 500", () => problem(500, "internal-error")],
  ])("REC-04: %s, so the repeat has the same key, no epoch header and no confirming read", async (_, first) => {
    const p = page([first(), created({ epoch: "e2" }, true)]);
    const w = entry();
    expect(await p.writes.submit(w)).toMatchObject({ kind: "done", status: 201, replayed: true });
    expect(p.sent.map((s) => s.path)).toEqual(["/recovery/entries", "/recovery/entries"]);
    expect(p.sent.map((s) => s.headers["Idempotency-Key"])).toEqual([w.key, w.key]);
    expect(p.sent.every((s) => s.headers["Bronzeward-Expected-Epoch"] === undefined)).toBe(true);
    expect(p.waits).toEqual([1000]);
  });

  test("the epoch change its own commit causes does not set it aside, in flight or unknown", async () => {
    const p = page([drop(), () => (observe(p, "e2"), p.writes.setAside(EPOCH_CHANGED_ASIDE), minted())]);
    expect(await p.writes.submit(entry())).toMatchObject({ kind: "done", replayed: true });
    expect(p.sent.map((s) => s.path)).toEqual(["/recovery/entries", "/recovery/entries"]);
    // Unknown after every attempt: the page moved to the new epoch, and [Repeat exactly] stays offered.
    const q = page([drop(), drop(), drop(), drop(), minted()]);
    const w = entry();
    expect((await q.writes.submit(w)).kind).toBe("unknown");
    observe(q, "e2");
    q.writes.setAside(EPOCH_CHANGED_ASIDE);
    expect(w.outcome?.kind).toBe("unknown");
    expect(q.writes.repeatable(w)).toBe(true);
    q.sent.length = 0;
    expect(await q.writes.submit(w)).toMatchObject({ kind: "done", replayed: true });
    expect(q.sent.map((s) => s.path)).toEqual(["/recovery/entries"]);
  });

  test("another sign-in still sets it aside before a repeat", async () => {
    const p = page([() => (p.signIn("https://idp.invalid SYNTH-sub-2"), drop())]);
    expect(await p.writes.submit(entry())).toEqual({ kind: "set-aside", why: SIGN_IN_CHANGED, ambiguous: true, problem: null });
    expect(p.sent).toHaveLength(1);
  });

  test("REC-02: a 409 conflict carrying an earlier record is the entry's refusal, which the form reads, not a set-aside", async () => {
    const p = page([problem(409, "conflict", { request: "r1", epoch: "e2" })]);
    expect(await p.writes.submit(entry())).toMatchObject({ kind: "refused", status: 409, problem: { epoch: "e2", request: "r1" } });
  });
});
