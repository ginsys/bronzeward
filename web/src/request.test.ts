import { describe, expect, test, vi } from "vitest";
import { API_BASE, Client, type ClientOptions } from "./request.ts";

const EPOCH = "e1";

function problem(status: number, code: string, extra: Record<string, unknown> = {}, epoch: string | null = EPOCH): Response {
  const headers: Record<string, string> = { "Content-Type": "application/problem+json" };
  if (epoch !== null) headers["Bronzeward-Epoch"] = epoch;
  return new Response(
    JSON.stringify({ type: `urn:bronzeward:problem:${code}`, title: code, status, instance: "req-1", ...extra }),
    { status, headers },
  );
}

function ok(body: unknown, epoch: string | null = EPOCH, recovery = false): Response {
  const headers: Record<string, string> = { "Content-Type": "application/json" };
  if (epoch !== null) headers["Bronzeward-Epoch"] = epoch;
  if (recovery) headers["Bronzeward-Recovery-Mode"] = "true";
  return new Response(JSON.stringify(body), { status: 200, headers });
}

// A client whose fetch answers each request with the next response (or throws the next error).
function client(answers: (Response | Error)[], opts: ClientOptions = {}) {
  const fetchMock = vi.fn(async (_url: RequestInfo | URL, _init?: RequestInit) => {
    const next = answers.shift();
    if (next === undefined) throw new Error("no answer left");
    if (next instanceof Error) throw next;
    return next;
  });
  const sleeps: number[] = [];
  const c = new Client({
    sleep: async (ms) => {
      sleeps.push(ms);
    },
    ...opts,
    fetch: fetchMock as unknown as typeof fetch,
  });
  return { c, fetchMock, sleeps };
}

describe("request options", () => {
  test("every read is no-store, refuses redirects, sends no cookie and the bearer token", async () => {
    const { c, fetchMock } = client([ok({})], { token: () => "SYNTH-token" });
    await c.read("/server");
    const [url, init] = fetchMock.mock.calls[0]!;
    expect(url).toBe(`${API_BASE}/server`);
    expect(init).toMatchObject({ method: "GET", cache: "no-store", redirect: "error", credentials: "omit" });
    expect(init?.headers).toEqual({ Accept: "application/json", Authorization: "Bearer SYNTH-token" });
  });

  test("a transport failure, a refused redirect included, is reported once and never repeated", async () => {
    const { c, fetchMock } = client([new TypeError("Failed to fetch")]);
    expect(await c.read("/server")).toEqual({ kind: "transport" });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});

describe("§8.2 read rows", () => {
  test("200 renders the body", async () => {
    expect(await client([ok({ version: "v" })]).c.read("/server")).toEqual({ kind: "ok", data: { version: "v" } });
  });

  test("a success without Bronzeward-Epoch is a defect, not rendered", async () => {
    expect((await client([ok({}, null)]).c.read("/server")).kind).toBe("defect");
  });

  test("400 invalid-request stops without a repeat", async () => {
    const { c, fetchMock } = client([problem(400, "invalid-request", { detail: "bad" })]);
    expect(await c.read("/machines")).toMatchObject({ kind: "refused", status: 400, problem: { detail: "bad" } });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  test("400 cursor-invalid restarts the list", async () => {
    expect((await client([problem(400, "cursor-invalid")]).c.read("/acts?cursor=x")).kind).toBe("restart");
  });

  test("401 renews once and repeats the read", async () => {
    const renew = vi.fn(async () => true);
    const { c, fetchMock } = client([problem(401, "unauthenticated", {}, null), ok({ a: 1 })], { renew });
    expect(await c.read("/machines")).toEqual({ kind: "ok", data: { a: 1 } });
    expect(renew).toHaveBeenCalledTimes(1);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  test("a second 401 stops", async () => {
    const renew = vi.fn(async () => true);
    const { c } = client([problem(401, "unauthenticated", {}, null), problem(401, "unauthenticated", {}, null)], { renew });
    expect((await c.read("/machines")).kind).toBe("unauthenticated");
    expect(renew).toHaveBeenCalledTimes(1);
  });

  test("401 with a failed renewal stops", async () => {
    const { c, fetchMock } = client([problem(401, "unauthenticated", {}, null)], { renew: async () => false });
    expect((await c.read("/machines")).kind).toBe("unauthenticated");
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  test("403 forbidden shows the roles, no retry", async () => {
    const { c, fetchMock } = client([problem(403, "forbidden", { roles: ["publisher"], humanOnly: true })]);
    expect(await c.read("/plans")).toMatchObject({ kind: "refused", status: 403, problem: { roles: ["publisher"], humanOnly: true } });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  test("403 identity-revoked clears the credential and stops", async () => {
    const onRevoked = vi.fn();
    const { c } = client([problem(403, "identity-revoked", {}, null)], { onRevoked });
    expect((await c.read("/identity")).kind).toBe("revoked");
    expect(onRevoked).toHaveBeenCalledTimes(1);
  });

  test("404 not-found", async () => {
    expect(await client([problem(404, "not-found")]).c.read("/machines/m")).toMatchObject({ kind: "refused", status: 404 });
  });

  test.each(["conflict", "stale-input", "scope-busy", "machine-identity-mismatch", "ingestion-abandoned", "recovery-mode-entered", "recovery-mode-active"])(
    "409 %s shows the named resource, never repeated",
    async (code) => {
      const { c, fetchMock } = client([problem(409, code, { machine: "m1" })]);
      expect(await c.read("/machines/m1")).toMatchObject({ kind: "refused", status: 409, problem: { machine: "m1" } });
      expect(fetchMock).toHaveBeenCalledTimes(1);
    },
  );

  test("428 and an unknown 4xx code are refusals by status class", async () => {
    expect((await client([problem(428, "precondition-required")]).c.read("/x")).kind).toBe("refused");
    expect((await client([problem(418, "future-code")]).c.read("/x")).kind).toBe("refused");
  });

  test("a 401 with an unknown code is a refusal, not renewed or repeated", async () => {
    const renew = vi.fn(async () => true);
    const { c, fetchMock } = client([problem(401, "future-code", {}, null), ok({})], { renew });
    expect(await c.read("/x")).toMatchObject({ kind: "refused", status: 401 });
    expect(renew).not.toHaveBeenCalled();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  test("a 4xx problem body that is not JSON is a defect; a 5xx one retries as the 503 row", async () => {
    const broken = (status: number) => new Response("{broken", { status, headers: { "Content-Type": "application/problem+json" } });
    expect(await client([broken(400)]).c.read("/x")).toEqual({ kind: "defect", message: "the problem body is not JSON" });
    const { c, fetchMock } = client([broken(502), ok({})]);
    expect((await c.read("/x")).kind).toBe("ok");
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  test("501 not-implemented is a refusal", async () => {
    expect(await client([problem(501, "not-implemented")]).c.read("/x")).toMatchObject({ kind: "refused", status: 501 });
  });

  test.each(["dependency-unavailable", "talos-access-unavailable", "transient-conflict"])(
    "503 %s retries 1 s doubling, at most 5 tries, then stops",
    async (code) => {
      const answers = Array.from({ length: 5 }, () => problem(503, code));
      const { c, fetchMock, sleeps } = client(answers);
      expect(await c.read("/dependencies")).toMatchObject({ kind: "unavailable", status: 503 });
      expect(fetchMock).toHaveBeenCalledTimes(5);
      expect(sleeps).toEqual([1000, 2000, 4000, 8000]);
    },
  );

  test("503 retry succeeds when the dependency comes back", async () => {
    const { c, fetchMock } = client([problem(503, "dependency-unavailable"), ok({ a: 1 })]);
    expect(await c.read("/dependencies")).toEqual({ kind: "ok", data: { a: 1 } });
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  test("503 epoch-superseded is not repeated against the same process", async () => {
    const { c, fetchMock } = client([problem(503, "epoch-superseded")]);
    expect((await c.read("/machines")).kind).toBe("unavailable");
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  test("500 internal-error is read again", async () => {
    const { c, fetchMock } = client([problem(500, "internal-error"), ok({})]);
    expect((await c.read("/machines")).kind).toBe("ok");
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  test("an unknown Bronzeward 5xx code is a refusal for a read", async () => {
    const { c, fetchMock } = client([problem(507, "future-code")]);
    expect((await c.read("/machines")).kind).toBe("refused");
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  test.each([
    ["a 502 page", new Response("<html>bad gateway</html>", { status: 502, headers: { "Content-Type": "text/html" } })],
    ["a 504 with no body", new Response(null, { status: 504 })],
    ["a problem of another type", new Response(JSON.stringify({ type: "about:blank", status: 503 }), { status: 503, headers: { "Content-Type": "application/problem+json" } })],
  ])("a 5xx that is not a Bronzeward problem (%s) retries as the 503 row", async (_name, first) => {
    const { c, fetchMock } = client([first, ok({})]);
    expect((await c.read("/machines")).kind).toBe("ok");
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });
});

describe("§8.5 epoch changes", () => {
  test("the first epoch is recorded without a notice", async () => {
    const onEpochChange = vi.fn();
    const { c } = client([ok({})], { onEpochChange });
    expect((await c.read("/server")).kind).toBe("ok");
    expect(c.currentEpoch).toBe(EPOCH);
    expect(onEpochChange).not.toHaveBeenCalled();
  });

  test("a changed epoch is recorded, reported with the recovery flag, and its response discarded", async () => {
    const onEpochChange = vi.fn();
    const { c } = client([ok({}), ok({ stale: true }, "e2", true), ok({ fresh: true }, "e2")], { onEpochChange });
    await c.read("/machines");
    expect(await c.read("/machines")).toEqual({ kind: "discarded" });
    expect(onEpochChange).toHaveBeenCalledWith("e2", true);
    expect(c.currentEpoch).toBe("e2");
    expect(await c.read("/machines")).toEqual({ kind: "ok", data: { fresh: true } });
    expect(onEpochChange).toHaveBeenCalledTimes(1);
  });

  test("a refusal carrying a new epoch runs the change too", async () => {
    const onEpochChange = vi.fn();
    const { c } = client([ok({}), problem(400, "cursor-invalid", {}, "e2")], { onEpochChange });
    await c.read("/acts");
    expect(await c.read("/acts?cursor=x")).toEqual({ kind: "discarded" });
    expect(onEpochChange).toHaveBeenCalledWith("e2", false);
  });

  test("recovery mode is reported from the first epoch on and clears on an exit that keeps the epoch", async () => {
    const onEpochChange = vi.fn();
    const onRecoveryModeChange = vi.fn();
    const answers = [ok({}, EPOCH, true), problem(401, "unauthenticated", {}, null), ok({}, EPOCH, true), ok({})];
    const { c } = client(answers, { onEpochChange, onRecoveryModeChange });
    await c.read("/server");
    expect(c.recoveryMode).toBe(true);
    expect((await c.read("/server")).kind).toBe("unauthenticated"); // no epoch header: says nothing
    await c.read("/server");
    expect(c.recoveryMode).toBe(true);
    expect((await c.read("/server")).kind).toBe("ok"); // the exit
    expect(c.recoveryMode).toBe(false);
    expect(onRecoveryModeChange.mock.calls).toEqual([[true], [false]]);
    expect(onEpochChange).not.toHaveBeenCalled();
  });
});

describe("stale-response discard", () => {
  // A read held until release() is called, so the view can change while it is in flight.
  function delayed(response: Response) {
    let release!: () => void;
    const gate = new Promise<void>((r) => (release = r));
    const fetchImpl = (async () => {
      await gate;
      return response;
    }) as unknown as typeof fetch;
    return { fetchImpl, release };
  }

  const base = { principal: "p1", route: "/machines", query: "" };
  test.each([
    ["route", { ...base, route: "/plans" }],
    ["query", { ...base, query: "?cluster=c2" }],
    ["principal", { ...base, principal: "p2" }],
  ])("a response arriving after its %s changed is not rendered", async (_what, next) => {
    const { fetchImpl, release } = delayed(ok({ stale: true }));
    const c = new Client({ fetch: fetchImpl });
    c.setView(base);
    const pending = c.read("/machines");
    c.setView(next);
    release();
    expect(await pending).toEqual({ kind: "discarded" });
  });

  test("a response arriving after another response changed the epoch is not rendered", async () => {
    const { fetchImpl: slow, release } = delayed(ok({ stale: true }, "e2"));
    const answers = [ok({}), null, ok({}, "e2")];
    const c = new Client({
      fetch: (async (...args: Parameters<typeof fetch>) => {
        const next = answers.shift();
        return next === null ? slow(...args) : next!;
      }) as typeof fetch,
    });
    await c.read("/machines"); // e1 recorded
    const pending = c.read("/machines"); // in flight under e1
    expect(await c.read("/plans")).toEqual({ kind: "discarded" }); // reveals e2
    release();
    expect(await pending).toEqual({ kind: "discarded" });
  });

  test("an unchanged view renders", async () => {
    const { fetchImpl, release } = delayed(ok({ a: 1 }));
    const c = new Client({ fetch: fetchImpl });
    c.setView(base);
    const pending = c.read("/machines");
    c.setView({ ...base });
    release();
    expect(await pending).toEqual({ kind: "ok", data: { a: 1 } });
  });

  // Headers at once, the body only after release(): the view can change while the body arrives.
  function slowBody(status: number, contentType: string, body: unknown) {
    let release!: () => void;
    const gate = new Promise<void>((r) => (release = r));
    const stream = new ReadableStream<Uint8Array>({
      async pull(ctl) {
        await gate;
        ctl.enqueue(new TextEncoder().encode(JSON.stringify(body)));
        ctl.close();
      },
    });
    const response = new Response(stream, { status, headers: { "Content-Type": contentType, "Bronzeward-Epoch": EPOCH } });
    return { fetchImpl: (async () => response) as unknown as typeof fetch, release };
  }

  test("a success whose body arrives after the route changed is not rendered", async () => {
    const { fetchImpl, release } = slowBody(200, "application/json", { stale: true });
    const c = new Client({ fetch: fetchImpl });
    c.setView(base);
    const pending = c.read("/machines");
    await new Promise((r) => setTimeout(r, 0)); // the headers have arrived
    c.setView({ ...base, route: "/plans" });
    release();
    expect(await pending).toEqual({ kind: "discarded" });
  });

  test("a revocation whose body arrives after a new sign-in clears nothing", async () => {
    const { fetchImpl, release } = slowBody(403, "application/problem+json", {
      type: "urn:bronzeward:problem:identity-revoked", title: "revoked", status: 403,
    });
    const onRevoked = vi.fn();
    const c = new Client({ fetch: fetchImpl, onRevoked });
    c.setView(base);
    const pending = c.read("/machines");
    await new Promise((r) => setTimeout(r, 0));
    c.setView({ ...base, principal: "p2" });
    release();
    expect(await pending).toEqual({ kind: "discarded" });
    expect(onRevoked).not.toHaveBeenCalled();
  });

  // Answers each request in turn: a response at once, or a held one when it is released.
  function scripted(answers: (Response | typeof fetch)[]) {
    return (async (...args: Parameters<typeof fetch>) => {
      const next = answers.shift();
      if (next === undefined) throw new Error("no answer left");
      return next instanceof Response ? next : next(...args);
    }) as typeof fetch;
  }

  test("a late response under the old epoch does not roll the epoch back", async () => {
    const { fetchImpl: slow, release } = delayed(ok({ stale: true }, "e1"));
    const onEpochChange = vi.fn();
    // The late e1 is repeated once and answered under e2.
    const c = new Client({ onEpochChange, fetch: scripted([ok({}), slow, ok({}, "e2"), ok({}, "e2"), ok({ fresh: true }, "e2")]) });
    await c.read("/machines"); // e1 recorded
    const pending = c.read("/machines"); // in flight under e1
    expect(await c.read("/plans")).toEqual({ kind: "discarded" }); // reveals e2
    release();
    expect(await pending).toEqual({ kind: "discarded" });
    expect(c.currentEpoch).toBe("e2");
    expect(onEpochChange).toHaveBeenCalledTimes(1);
    expect(await c.read("/plans")).toEqual({ kind: "ok", data: { fresh: true } });
  });

  test("a late response from before a recovery exit does not turn recovery mode back on", async () => {
    const { fetchImpl: slow, release } = delayed(ok({ late: true }, "e1", true));
    const onRecoveryModeChange = vi.fn();
    const c = new Client({ onRecoveryModeChange, fetch: scripted([ok({}, "e1", true), slow, ok({}, "e1")]) });
    await c.read("/machines");
    const pending = c.read("/machines"); // in flight in recovery mode
    await c.read("/plans"); // after the exit
    release();
    expect(await pending).toEqual({ kind: "ok", data: { late: true } });
    expect(c.recoveryMode).toBe(false);
    expect(onRecoveryModeChange.mock.calls).toEqual([[true], [false]]);
  });

  // Read A, then a route change, then read B, both held; `recorded` seeds e1 first. Each repeat
  // answers a read whose epoch was ambiguous when it arrived.
  async function race(a: Response, b: Response, repeats: Response[], recorded = true) {
    const ha = delayed(a);
    const hb = delayed(b);
    const onEpochChange = vi.fn();
    const c = new Client({ onEpochChange, fetch: scripted([...(recorded ? [ok({})] : []), ha.fetchImpl, hb.fetchImpl, ...repeats]) });
    c.setView(base);
    if (recorded) await c.read("/machines");
    const first = c.read("/machines");
    c.setView({ ...base, route: "/plans" });
    const second = c.read("/plans");
    return { c, onEpochChange, first, second, releaseA: ha.release, releaseB: hb.release };
  }

  test("a response a view change made stale still reports its new epoch", async () => {
    const r = await race(ok({}, "e2"), ok({ old: true }, "e1"), [ok({}, "e2")]);
    r.releaseA();
    expect(await r.first).toEqual({ kind: "discarded" });
    expect(r.c.currentEpoch).toBe("e2");
    r.releaseB(); // answered before recovery, arriving last
    expect(await r.second).toEqual({ kind: "discarded" });
    expect(r.c.currentEpoch).toBe("e2");
    expect(r.onEpochChange).toHaveBeenCalledTimes(1);
  });

  test("a second recovery seen only by a read sent before the first was recorded is reported", async () => {
    const r = await race(ok({}, "e2"), ok({}, "e3"), [ok({}, "e3")]);
    r.releaseA();
    await r.first;
    r.releaseB();
    expect(await r.second).toEqual({ kind: "discarded" });
    expect(r.c.currentEpoch).toBe("e3");
    expect(r.onEpochChange).toHaveBeenCalledTimes(2);
  });

  test("before any epoch is recorded, a late older epoch does not replace the first one", async () => {
    const r = await race(ok({}, "e2"), ok({ old: true }, "e1"), [ok({ fresh: true }, "e2")], false);
    r.releaseA(); // e2 recorded silently
    await r.first;
    r.releaseB(); // repeated, and its current view renders the answer under e2
    expect(await r.second).toEqual({ kind: "ok", data: { fresh: true } });
    expect(r.c.currentEpoch).toBe("e2");
    expect(r.onEpochChange).not.toHaveBeenCalled();
  });

  test("before any epoch is recorded, a newer epoch arriving second is reported", async () => {
    const r = await race(ok({}, "e2"), ok({ b: 1 }, "e1"), [ok({}, "e2")], false);
    r.releaseB(); // e1 recorded silently, B rendered
    expect(await r.second).toEqual({ kind: "ok", data: { b: 1 } });
    r.releaseA();
    expect(await r.first).toEqual({ kind: "discarded" });
    expect(r.c.currentEpoch).toBe("e2");
    expect(r.onEpochChange).toHaveBeenCalledTimes(1);
  });

  test("a failed renewal that ends after the view changed is discarded, not shown", async () => {
    let c!: Client;
    const renew = vi.fn(async () => {
      c.setView({ ...base, principal: "p2" });
      return false;
    });
    c = new Client({ fetch: (async () => problem(401, "unauthenticated", {}, null)) as unknown as typeof fetch, renew });
    c.setView(base);
    expect(await c.read("/machines")).toEqual({ kind: "discarded" });
    expect(renew).toHaveBeenCalledTimes(1);
  });

  test("a body cut off by the network is a transport failure; one that is not JSON is a defect", async () => {
    const cut = new Response(new ReadableStream({ pull: (ctl) => ctl.error(new TypeError("network error")) }), {
      status: 200, headers: { "Bronzeward-Epoch": EPOCH },
    });
    expect(await client([cut]).c.read("/server")).toEqual({ kind: "transport" });
    const html = new Response("<html>", { status: 200, headers: { "Bronzeward-Epoch": EPOCH } });
    expect(await client([html]).c.read("/server")).toEqual({ kind: "defect", message: "the response is not JSON" });
  });

  test("a problem body cut off by the network is a transport failure, not a refusal", async () => {
    const cut = new Response(new ReadableStream({ pull: (ctl) => ctl.error(new TypeError("network error")) }), {
      status: 403, headers: { "Content-Type": "application/problem+json", "Bronzeward-Epoch": EPOCH },
    });
    const onRevoked = vi.fn();
    expect(await client([cut], { onRevoked }).c.read("/server")).toEqual({ kind: "transport" });
    expect(onRevoked).not.toHaveBeenCalled();
  });

  test("a non-Bronzeward 5xx body cut off by the network is a transport failure, not retried", async () => {
    const cut = new Response(new ReadableStream({ pull: (ctl) => ctl.error(new TypeError("network error")) }), {
      status: 502, headers: { "Content-Type": "text/html" },
    });
    const { c, fetchMock } = client([cut]);
    expect(await c.read("/server")).toEqual({ kind: "transport" });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  test("a view change during a 503 backoff stops the retries", async () => {
    let c!: Client;
    const fetchMock = vi.fn(async () => problem(503, "dependency-unavailable"));
    c = new Client({
      fetch: fetchMock as unknown as typeof fetch,
      sleep: async () => c.setView({ ...base, route: "/plans" }),
    });
    c.setView(base);
    expect(await c.read("/machines")).toEqual({ kind: "discarded" });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  // Regression guards from #331's review: renewal sits next to each of these branches.
  test.each(["malformed", "cut"])("a %s problem body that arrives after the view changed is discarded", async (how) => {
    let release!: () => void;
    const gate = new Promise<void>((r) => (release = r));
    const stream = new ReadableStream<Uint8Array>({
      async pull(ctl) {
        await gate;
        if (how === "cut") return ctl.error(new TypeError("network error"));
        ctl.enqueue(new TextEncoder().encode("{"));
        ctl.close();
      },
    });
    const response = new Response(stream, { status: 403, headers: { "Content-Type": "application/problem+json", "Bronzeward-Epoch": EPOCH } });
    const c = new Client({ fetch: (async () => response) as unknown as typeof fetch });
    c.setView(base);
    const pending = c.read("/machines");
    await new Promise((r) => setTimeout(r, 0));
    c.setView({ ...base, route: "/plans" });
    release();
    expect(await pending).toEqual({ kind: "discarded" });
  });
});

describe("refusals renewal never touches", () => {
  test.each([
    ["no body", () => new Response(null, { status: 401, headers: { "Bronzeward-Epoch": EPOCH } })],
    ["a non-Bronzeward problem", () => new Response(JSON.stringify({ type: "about:blank", title: "x", status: 401 }), {
      status: 401, headers: { "Content-Type": "application/problem+json", "Bronzeward-Epoch": EPOCH },
    })],
  ])("a 401 with %s is a refusal and is not renewed", async (_what, answer) => {
    const renew = vi.fn(async () => true);
    const { c, fetchMock } = client([answer()], { renew });
    expect(await c.read("/server")).toEqual({ kind: "refused", status: 401, problem: null });
    expect(renew).not.toHaveBeenCalled();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  test("five 5xx answers with an unparseable problem body are read five times with backoff, then unavailable", async () => {
    const bad = () => new Response("{", { status: 503, headers: { "Content-Type": "application/problem+json", "Bronzeward-Epoch": EPOCH } });
    const { c, fetchMock, sleeps } = client([bad(), bad(), bad(), bad(), bad()]);
    expect(await c.read("/server")).toEqual({ kind: "unavailable", status: 503, problem: null });
    expect(fetchMock).toHaveBeenCalledTimes(5);
    expect(sleeps).toEqual([1000, 2000, 4000, 8000]);
  });
});
