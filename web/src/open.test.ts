// Client.open(), the stream path of the progress reader (client contracts §8.4 item 8, §8.6 items 4
// and 5). The reads and writes of the client are tested in request.test.ts.

import { afterEach, describe, expect, test, vi } from "vitest";
import { API_BASE, Client, type ClientOptions, NOT_SENT } from "./request.ts";
import { endlessBody, noHeaders } from "./test-stalls.ts";

const EPOCH = "e1";
const EVENTS = "/operations/op_1/events";
const streamHeaders = { "Content-Type": "text/event-stream", "Bronzeward-Epoch": EPOCH };
const stream = (epoch = EPOCH, extra: Record<string, string> = {}) =>
  new Response("", { status: 200, headers: { ...streamHeaders, "Bronzeward-Epoch": epoch, ...extra } });

// A client whose fetch answers each request with the next response.
function client(answers: Response[], opts: ClientOptions = {}) {
  const fetchMock = vi.fn(async (_url: RequestInfo | URL, _init?: RequestInit) => {
    const next = answers.shift();
    if (next === undefined) throw new Error("no answer left");
    return next;
  });
  return { c: new Client({ ...opts, fetch: fetchMock as unknown as typeof fetch }), fetchMock };
}

describe("the event stream through open()", () => {
  afterEach(() => vi.useRealTimers());

  test("no HEADERS_BOUND or ATTEMPT_BOUND ends it: headers at 31 s, an open body at 150 s, and only its own signal cuts it", async () => {
    vi.useFakeTimers();
    const signals: (AbortSignal | null | undefined)[] = [];
    const fetchImpl = vi.fn(async (_url: RequestInfo | URL, init?: RequestInit) => {
      signals.push(init?.signal);
      await new Promise((resolve) => setTimeout(resolve, 31_000));
      return endlessBody(init, 200, streamHeaders);
    });
    const c = new Client({ fetch: fetchImpl as unknown as typeof fetch });
    const cut = new AbortController();
    const opened = c.open(EVENTS, { Accept: "text/event-stream" }, cut.signal);
    await vi.advanceTimersByTimeAsync(31_000);
    const res = (await opened) as Response;
    expect(res.status).toBe(200);
    const reading = res.body!.getReader().read().then(() => "data", () => "cut");
    await vi.advanceTimersByTimeAsync(150_000);
    expect(signals[0]?.aborted).toBe(false);
    cut.abort();
    expect(signals[0]?.aborted).toBe(true);
    expect(await reading).toBe("cut");
  });

  test("an answer whose headers never arrive stays open past 60 s and ends, with no answer, when the reader's signal fires", async () => {
    vi.useFakeTimers();
    const fetchImpl = vi.fn(async (_url: RequestInfo | URL, init?: RequestInit) => noHeaders(init));
    const c = new Client({ fetch: fetchImpl as unknown as typeof fetch });
    const cut = new AbortController();
    const box: { done: boolean; value?: unknown } = { done: false };
    void c.open(EVENTS, {}, cut.signal).then((value) => Object.assign(box, { done: true, value }));
    await vi.advanceTimersByTimeAsync(90_000);
    expect(box.done).toBe(false);
    cut.abort();
    await vi.advanceTimersByTimeAsync(0);
    expect(box).toEqual({ done: true, value: undefined });
  });

  test("a sign-in that ended sends nothing: open() answers NOT_SENT", async () => {
    const { c, fetchMock } = client([stream()]);
    c.endWrites();
    expect(await c.open(EVENTS, { Accept: "text/event-stream" }, new AbortController().signal)).toBe(NOT_SENT);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  test("a stream answered under a new epoch runs onEpochChange once and is still returned", async () => {
    const changes: [string, boolean][] = [];
    const { c } = client([stream("e1"), stream("e2"), stream("e2")], { onEpochChange: (e, r) => void changes.push([e, r]) });
    const signal = new AbortController().signal;
    for (const epoch of ["e1", "e2", "e2"]) {
      const res = (await c.open(EVENTS, {}, signal)) as Response;
      expect(res.headers.get("Bronzeward-Epoch")).toBe(epoch);
    }
    expect(changes).toEqual([["e2", false]]);
  });

  test("the token is sent as Authorization, Accept and Last-Event-ID as given, and the URL carries no token", async () => {
    const { c, fetchMock } = client([stream()], { token: () => "SYNTH-token" });
    await c.open(`${EVENTS}?x=1`, { Accept: "text/event-stream", "Last-Event-ID": "e1:2" }, new AbortController().signal);
    const [url, init] = fetchMock.mock.calls[0]!;
    expect(String(url)).toBe(`${API_BASE}${EVENTS}?x=1`);
    expect(String(url)).not.toContain("SYNTH");
    expect(init?.headers).toEqual({ Accept: "text/event-stream", "Last-Event-ID": "e1:2", Authorization: "Bearer SYNTH-token" });
    expect(init?.method).toBe("GET");
  });

  test("a Deprecation header of the stream's route is told once", async () => {
    const told: string[] = [];
    const { c } = client([stream(EPOCH, { Deprecation: "true" }), stream(EPOCH, { Deprecation: "true" })], { onDeprecation: (r) => void told.push(r) });
    const signal = new AbortController().signal;
    await c.open(EVENTS, {}, signal);
    await c.open(EVENTS, {}, signal);
    expect(told).toEqual([`${API_BASE}${EVENTS}`]);
  });
});
