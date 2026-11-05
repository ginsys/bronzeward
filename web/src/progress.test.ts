import { describe, expect, test } from "vitest";
import { eventLine, type OpEvent, type Pace, RESTORED, RESTORED_WHILE_READ, terminal, watch } from "./progress.ts";
import { API_BASE, Client } from "./request.ts";
import { noHeaders } from "./test-stalls.ts";

const OP = "op_1";
const EVENTS = `${API_BASE}/operations/${OP}/events`;

const ev = (id: string, type = "started", data: unknown = {}) => ({ id, type, data });
const frame = (e: { id: string; type: string; data: unknown }) => `id: ${e.id}\nevent: ${e.type}\ndata: ${JSON.stringify(e.data)}\n\n`;

// A stream answer: its chunks, then the end of the body unless `open`, which holds it open until
// the request is cut. `hang` never sends its headers.
interface Stream { chunks: string[]; epoch?: string | null; open?: boolean; status?: number; type?: string; hang?: boolean }

function json(body: unknown, epoch = "e1", status = 200): Response {
  const headers = { "Content-Type": status === 200 ? "application/json" : "application/problem+json", "Bronzeward-Epoch": epoch };
  return new Response(JSON.stringify(body), { status, headers });
}

// A fake server. Each stream request takes the next of `streams` (an Error is a transport
// failure); each operation read the next of `states`, the last repeated; each JSON events read
// answers `pages`, or the next of `pageRefusals`.
function server(o: { streams: (Stream | Error)[]; states: string[]; pages?: OpEvent[]; pageRefusals?: number[]; epoch?: string }) {
  const opened: Record<string, string>[] = [];
  const urls: string[] = [];
  const fetch = async (url: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const u = String(url);
    urls.push(u);
    const headers = (init?.headers ?? {}) as Record<string, string>;
    if (u === EVENTS && headers["Accept"] === "text/event-stream") {
      opened.push(headers);
      const s = o.streams.shift();
      if (s === undefined) throw new Error("no stream left");
      if (s instanceof Error) throw s;
      if (s.hang === true) return noHeaders(init);
      const body = new ReadableStream<Uint8Array>({
        start(c) {
          for (const chunk of s.chunks) c.enqueue(new TextEncoder().encode(chunk));
          if (s.open === true) init?.signal?.addEventListener("abort", () => c.error(new DOMException("aborted", "AbortError")));
          else c.close();
        },
      });
      const h: Record<string, string> = { "Content-Type": s.type ?? "text/event-stream" };
      if (s.epoch !== null) h["Bronzeward-Epoch"] = s.epoch ?? o.epoch ?? "e1";
      return new Response(body, { status: s.status ?? 200, headers: h });
    }
    if (u.startsWith(EVENTS)) {
      const refusal = o.pageRefusals?.shift();
      if (refusal !== undefined) return json({ type: "urn:bronzeward:problem:forbidden", title: "forbidden", status: refusal }, o.epoch, refusal);
      return json({ items: (o.pages ?? []).map((e) => ({ ...e, at: "2026-11-01T06:00:00Z" })) }, o.epoch);
    }
    if (u === `${API_BASE}/operations/${OP}`) {
      const state = o.states.length > 1 ? o.states.shift()! : o.states[0]!;
      return json({ id: OP, kind: "ingest", state }, o.epoch);
    }
    throw new Error(`unexpected ${u}`);
  };
  const client = new Client({ fetch: fetch as typeof globalThis.fetch, token: () => "SYNTH-token", sleep: async () => {} });
  return { client, opened, urls };
}

function recorder(until?: (shown: OpEvent[]) => boolean) {
  const shown: OpEvent[] = [];
  const log: string[] = [];
  const sleeps: number[] = [];
  const abort = new AbortController();
  const pace: Pace = { sleep: async (ms) => { sleeps.push(ms); }, now: () => 0, idle: 20 };
  return {
    shown, log, sleeps, abort, pace,
    w: {
      show: (e: OpEvent) => { shown.push(e); log.push(`show ${e.id}`); },
      clear: (why: string) => { shown.length = 0; log.push(`clear: ${why}`); },
      ...(until === undefined ? {} : { until: () => until(shown) }),
      signal: abort.signal,
    },
  };
}

describe("the operation progress reader (§8.6)", () => {
  test("a stream's events are shown once and the watch ends on a terminal state with nothing left to drain", async () => {
    const all = [ev("e1:1"), ev("e1:2", "staged"), ev("e1:3", "succeeded")];
    const s = server({ streams: [{ chunks: [all.map(frame).join("")] }], states: ["succeeded"], pages: all });
    const r = recorder();
    const out = await watch(s.client, OP, r.w, r.pace);
    expect(out).toMatchObject({ kind: "ended", operation: { state: "succeeded" } });
    expect(r.shown.map((e) => e.id)).toEqual(["e1:1", "e1:2", "e1:3"]);
    expect(r.shown[1]).toEqual({ id: "e1:2", type: "staged", data: {} });
    expect(s.opened[0]).toEqual({ Accept: "text/event-stream", Authorization: "Bearer SYNTH-token" });
  });

  test("item 2: a cut stream reconnects with Last-Event-ID and the token in a header, never in the URL", async () => {
    const s = server({
      streams: [{ chunks: [frame(ev("e1:1")), frame(ev("e1:2", "staged"))] }, { chunks: [frame(ev("e1:3", "succeeded"))] }],
      states: ["running", "succeeded"],
      pages: [ev("e1:1"), ev("e1:2", "staged"), ev("e1:3", "succeeded")],
    });
    const r = recorder();
    await watch(s.client, OP, r.w, r.pace);
    expect(s.opened[1]).toEqual({ Accept: "text/event-stream", Authorization: "Bearer SYNTH-token", "Last-Event-ID": "e1:2" });
    expect(r.shown.map((e) => e.id)).toEqual(["e1:1", "e1:2", "e1:3"]);
    expect(s.urls.every((u) => !u.includes("SYNTH"))).toBe(true);
  });

  test("item 2: on a terminal state the JSON pages are drained of the events the stream did not send", async () => {
    const s = server({
      streams: [{ chunks: [frame(ev("e1:1"))] }],
      states: ["failed"],
      pages: [ev("e1:1"), ev("e1:2", "staged"), ev("e1:3", "failed", { code: "precondition-failed" })],
    });
    const r = recorder();
    const out = await watch(s.client, OP, r.w, r.pace);
    expect(out).toMatchObject({ kind: "ended", operation: { state: "failed" } });
    expect(r.shown.map((e) => e.id)).toEqual(["e1:1", "e1:2", "e1:3"]);
    expect(r.shown[2]!.data).toEqual({ code: "precondition-failed" });
  });

  test("unresolved is not terminal: the watch reconnects", () => {
    expect(terminal("unresolved")).toBe(false);
    expect(["succeeded", "failed", "completed", "rejected", "cancelled"].every(terminal)).toBe(true);
  });

  test("item 3: a reset clears what was shown and the replay is shown from the start", async () => {
    const s = server({
      streams: [
        { chunks: [frame(ev("e1:1")), frame(ev("e1:2", "staged"))], epoch: "e1" },
        { chunks: ["event: reset\ndata: {}\n\n", frame(ev("e2:1")), frame(ev("e2:2", "staged")), frame(ev("e2:3", "succeeded"))], epoch: "e2" },
      ],
      states: ["running", "succeeded"],
      pages: [ev("e2:1"), ev("e2:2", "staged"), ev("e2:3", "succeeded")],
      epoch: "e2",
    });
    const r = recorder();
    await watch(s.client, OP, r.w, r.pace);
    expect(r.log).toEqual(["show e1:1", "show e1:2", `clear: ${RESTORED}`, "show e2:1", "show e2:2", "show e2:3"]);
  });

  test("item 3: an event of another epoch than the stream's ends it, clears, and reopens without Last-Event-ID", async () => {
    const s = server({
      streams: [
        { chunks: [frame(ev("e1:1")), frame(ev("e2:2", "staged"))], epoch: "e1" },
        { chunks: [frame(ev("e2:1")), frame(ev("e2:2", "staged")), frame(ev("e2:3", "succeeded"))], epoch: "e2" },
      ],
      states: ["running", "succeeded"],
      pages: [ev("e2:1"), ev("e2:2", "staged"), ev("e2:3", "succeeded")],
      epoch: "e2",
    });
    const r = recorder();
    await watch(s.client, OP, r.w, r.pace);
    expect(r.log).toEqual(["show e1:1", `clear: ${RESTORED_WHILE_READ}`, "show e2:1", "show e2:2", "show e2:3"]);
    expect(s.opened[1]).not.toHaveProperty("Last-Event-ID");
  });

  test("item 3: drained pages of another epoch than the last event shown clear and show every event again", async () => {
    const s = server({
      streams: [{ chunks: [frame(ev("e1:1")), frame(ev("e1:2", "staged"))], epoch: "e1" }, new TypeError("Failed to fetch")],
      states: ["running", "succeeded"],
      pages: [ev("e2:1"), ev("e2:2", "succeeded")],
      epoch: "e2",
    });
    const r = recorder();
    await watch(s.client, OP, r.w, r.pace);
    expect(r.log).toEqual(["show e1:1", "show e1:2", `clear: ${RESTORED}`, "show e2:1", "show e2:2"]);
  });

  test("item 2: failed opens are retried after 1 s, doubling to 30 s, and the delay starts again after progress", async () => {
    const down = Array.from({ length: 7 }, () => new TypeError("Failed to fetch"));
    const s = server({
      streams: [...down, { chunks: [frame(ev("e1:1"))] }, new TypeError("Failed to fetch"), { chunks: [frame(ev("e1:2", "succeeded"))] }],
      states: ["running", "running", "running", "running", "running", "running", "running", "running", "running", "succeeded"],
      pages: [ev("e1:1"), ev("e1:2", "succeeded")],
    });
    const r = recorder();
    await watch(s.client, OP, r.w, r.pace);
    expect(r.sleeps).toEqual([1000, 2000, 4000, 8000, 16000, 30000, 30000, 1000]);
    expect(r.shown.map((e) => e.id)).toEqual(["e1:1", "e1:2"]);
  });

  test("item 5: keep-alive comments are ignored and keep the last event ID; a silent stream is cut and reopened", async () => {
    const s = server({
      streams: [{ chunks: [frame(ev("e1:1")), ": keep-alive\n\n"], open: true }, { chunks: [": keep-alive\n\n", frame(ev("e1:2", "succeeded"))] }],
      states: ["running", "succeeded"],
      pages: [ev("e1:1"), ev("e1:2", "succeeded")],
    });
    const r = recorder();
    const out = await watch(s.client, OP, r.w, r.pace);
    expect(out.kind).toBe("ended");
    expect(s.opened[1]!["Last-Event-ID"]).toBe("e1:1");
    expect(r.shown.map((e) => e.id)).toEqual(["e1:1", "e1:2"]);
  });

  test("item 1: a server that answers the stream with JSON is followed through its pages", async () => {
    const s = server({
      streams: [{ chunks: ["{}"], type: "application/json" }],
      states: ["running", "succeeded"],
      pages: [ev("e1:1"), ev("e1:2", "succeeded")],
    });
    const r = recorder();
    const out = await watch(s.client, OP, r.w, r.pace);
    expect(out.kind).toBe("ended");
    expect(s.opened).toHaveLength(1);
    expect(r.shown.map((e) => e.id)).toEqual(["e1:1", "e1:2"]);
  });

  test("until ends the watch on a running operation once it answers true", async () => {
    const s = server({ streams: [{ chunks: [frame(ev("e1:1")), frame(ev("e1:2", "paused", { generation: 1 })), frame(ev("e1:3"))] }], states: ["running"] });
    const r = recorder((shown) => shown.some((e) => e.type === "paused"));
    const out = await watch(s.client, OP, r.w, r.pace);
    expect(out).toMatchObject({ kind: "ended", operation: { state: "running" } });
    expect(r.shown.map((e) => e.id)).toEqual(["e1:1", "e1:2"]);
  });

  test("a refused stream is read as a page, which reports the refusal", async () => {
    const s = server({ streams: [{ chunks: [], status: 403, type: "application/problem+json" }], states: ["running"], pageRefusals: [403] });
    const r = recorder();
    const out = await watch(s.client, OP, r.w, r.pace);
    expect(out).toMatchObject({ kind: "failed", res: { kind: "refused", status: 403 } });
  });

  test("a stream without Bronzeward-Epoch is a defect", async () => {
    const s = server({ streams: [{ chunks: [frame(ev("e1:1"))], epoch: null }], states: ["running"] });
    const r = recorder();
    expect(await watch(s.client, OP, r.w, r.pace)).toEqual({ kind: "failed", res: { kind: "defect", message: "the event stream carried no Bronzeward-Epoch header" } });
  });

  test("an event whose data is not JSON is a defect", async () => {
    const s = server({ streams: [{ chunks: ["id: e1:1\nevent: started\ndata: {not json\n\n"] }], states: ["running"] });
    const r = recorder();
    expect(await watch(s.client, OP, r.w, r.pace)).toEqual({ kind: "failed", res: { kind: "defect", message: "an event's data is not JSON" } });
    expect(r.shown).toEqual([]);
  });

  test("leaving the page stops the watch", async () => {
    const s = server({ streams: [{ chunks: [frame(ev("e1:1"))], open: true }], states: ["running"] });
    const r = recorder();
    r.pace.idle = 60_000;
    const out = watch(s.client, OP, r.w, r.pace);
    await new Promise((done) => setTimeout(done, 5));
    r.abort.abort();
    expect(await out).toEqual({ kind: "stopped" });
  });

  test("§8.4 item 8: a stream whose headers never arrive is cut at the idle period, from the request, and reopened", async () => {
    const s = server({
      streams: [{ chunks: [], hang: true }, { chunks: [frame(ev("e1:1", "succeeded"))] }],
      states: ["running", "succeeded"],
      pages: [ev("e1:1", "succeeded")],
    });
    const r = recorder();
    const out = await watch(s.client, OP, r.w, r.pace);
    expect(out.kind).toBe("ended");
    expect(s.opened).toHaveLength(2);
    expect(r.shown.map((e) => e.id)).toEqual(["e1:1"]);
    expect(r.sleeps).toEqual([1000]);
  });

  test("§9.6: a sign-in that ended before the watch stops it with nothing sent", async () => {
    const s = server({ streams: [{ chunks: [frame(ev("e1:1"))] }], states: ["running"] });
    s.client.endWrites();
    const r = recorder();
    expect(await watch(s.client, OP, r.w, r.pace)).toEqual({ kind: "stopped" });
    expect(s.urls).toEqual([]);
    expect(r.shown).toEqual([]);
  });

  test("§9.6: a sign-in that ends while the watch runs stops it at its next send, and no stream is opened again", async () => {
    const s = server({ streams: [{ chunks: [frame(ev("e1:1"))] }], states: ["running"] });
    const r = recorder();
    const show = r.w.show;
    r.w.show = (e) => { show(e); s.client.endWrites(); };
    expect(await watch(s.client, OP, r.w, r.pace)).toEqual({ kind: "stopped" });
    expect(s.opened).toHaveLength(1);
    expect(r.shown.map((e) => e.id)).toEqual(["e1:1"]);
  });

  test("eventLine names the type, the generation and the code, and nothing else of the data", () => {
    expect(eventLine(ev("e1:1", "attempt", { generation: 3, code: "precondition-failed", secret: "SYNTH-do-not-show" }))).toBe("attempt (generation 3): precondition-failed");
    expect(eventLine(ev("e1:2", "paused"))).toBe("paused");
    expect(eventLine(ev("e1:3", "x", null))).toBe("x");
  });
});
