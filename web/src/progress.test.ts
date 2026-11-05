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
// `trickle` sends those bytes every 5 ms, with no line feed, until the request is cut.
interface Stream { chunks: string[]; epoch?: string | null; open?: boolean; status?: number; type?: string; hang?: boolean; trickle?: string }

function json(body: unknown, epoch = "e1", status = 200): Response {
  const headers = { "Content-Type": status === 200 ? "application/json" : "application/problem+json", "Bronzeward-Epoch": epoch };
  return new Response(JSON.stringify(body), { status, headers });
}

// A fake server. Each stream request takes the next of `streams` (an Error is a transport
// failure); each operation read the next of `states`, the last repeated; each JSON events read
// answers `pages`, or the next of `pageRefusals`. A state `x@e2` is read under the epoch e2, and
// the pages are read under `pagesEpoch`, and a read of `/probe` is answered under it too. Each operation
// read takes `readTime` ms of the `clock`, and each stream request `streamTime` ms. The pages are
// answered when `pagesGate` is open.
function server(o: {
  streams: (Stream | Error)[]; states: string[]; pages?: OpEvent[]; pageRefusals?: number[]; epoch?: string;
  pagesEpoch?: string; probeEpoch?: string; clock?: { t: number }; readTime?: number; streamTime?: number; pagesGate?: Promise<void>;
}) {
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
      if (o.clock !== undefined) o.clock.t += o.streamTime ?? 0;
      if (s.hang === true) return noHeaders(init);
      let drip: ReturnType<typeof setInterval> | undefined;
      const body = new ReadableStream<Uint8Array>({
        start(c) {
          for (const chunk of s.chunks) c.enqueue(new TextEncoder().encode(chunk));
          if (s.trickle !== undefined) drip = setInterval(() => c.enqueue(new TextEncoder().encode(s.trickle!)), 5);
          if (s.open === true || s.trickle !== undefined) {
            init?.signal?.addEventListener("abort", () => { clearInterval(drip); c.error(new DOMException("aborted", "AbortError")); });
          } else c.close();
        },
        cancel() { clearInterval(drip); },
      });
      const h: Record<string, string> = { "Content-Type": s.type ?? "text/event-stream" };
      if (s.epoch !== null) h["Bronzeward-Epoch"] = s.epoch ?? o.epoch ?? "e1";
      return new Response(body, { status: s.status ?? 200, headers: h });
    }
    if (u.startsWith(EVENTS)) {
      await o.pagesGate;
      const refusal = o.pageRefusals?.shift();
      if (refusal !== undefined) return json({ type: "urn:bronzeward:problem:forbidden", title: "forbidden", status: refusal }, o.epoch, refusal);
      return json({ items: (o.pages ?? []).map((e) => ({ ...e, at: "2026-11-01T06:00:00Z" })) }, o.pagesEpoch ?? o.epoch);
    }
    if (u === `${API_BASE}/operations/${OP}`) {
      const [state, epoch] = (o.states.length > 1 ? o.states.shift()! : o.states[0]!).split("@");
      if (o.clock !== undefined) o.clock.t += o.readTime ?? 0;
      return json({ id: OP, kind: "ingest", state }, epoch ?? o.epoch);
    }
    if (u === `${API_BASE}/probe`) return json({}, o.probeEpoch ?? o.pagesEpoch ?? o.epoch);
    throw new Error(`unexpected ${u}`);
  };
  const client = new Client({ fetch: fetch as typeof globalThis.fetch, token: () => "SYNTH-token", sleep: async () => {} });
  return { client, opened, urls };
}

function recorder(until?: (shown: OpEvent[]) => boolean, clock = { t: 0 }) {
  const shown: OpEvent[] = [];
  const log: string[] = [];
  const sleeps: number[] = [];
  const abort = new AbortController();
  const pace: Pace = { sleep: async (ms) => { sleeps.push(ms); }, now: () => clock.t, idle: 20 };
  return {
    shown, log, sleeps, abort, pace, clock,
    w: {
      show: (e: OpEvent) => { shown.push(e); log.push(`show ${e.id}`); },
      clear: (why: string) => { shown.length = 0; log.push(`clear: ${why}`); },
      ...(until === undefined ? {} : { until: () => until(shown) }),
      signal: abort.signal,
    },
  };
}

// A reader over the fake server's client in which another response records the epoch that `/probe`
// names, once, around the first read of a path `where` accepts: `before` that read is sent, or `after`
// its response was used and before the watch resumes.
function reader(s: ReturnType<typeof server>, where: (path: string) => boolean, when: "before" | "after") {
  let done = false;
  const probe = async (path: string) => {
    if (done || !where(path)) return;
    done = true;
    await s.client.read("/probe");
  };
  return {
    read: (async (path: string) => {
      if (when === "before") await probe(path);
      const result = await s.client.read(path);
      if (when === "after") await probe(path);
      return result;
    }) as typeof s.client.read,
    readWithEpoch: (async (path: string) => {
      if (when === "before") await probe(path);
      const result = await s.client.readWithEpoch(path);
      if (when === "after") await probe(path);
      return result;
    }) as typeof s.client.readWithEpoch,
    open: (...a: Parameters<typeof s.client.open>) => s.client.open(...a),
    get currentEpoch() { return s.client.currentEpoch; },
    get writesEnded() { return s.client.writesEnded; },
  };
}
const eventsPath = (path: string) => path.endsWith("/events");
const operationPath = (path: string) => path === `/operations/${OP}`;

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

  // Findings of the first review of the reader (R1-n).

  test("R1-1: a terminal read whose drain shows the events of a restored installation does not end the watch", async () => {
    const s = server({
      streams: [{ chunks: [frame(ev("e1:1"))] }, { chunks: [], epoch: "e2" }],
      states: ["failed@e1", "succeeded@e2"],
      pages: [ev("e2:1"), ev("e2:2", "staged"), ev("e2:3", "succeeded")],
      pagesEpoch: "e2",
    });
    // Another response records the restore after the operation read and before the drain's read.
    const r = recorder();
    const out = await watch(reader(s, eventsPath, "before"), OP, r.w, r.pace);
    expect(out).toMatchObject({ kind: "ended", operation: { state: "succeeded" } });
    expect(r.log).toEqual(["show e1:1", `clear: ${RESTORED}`, "show e2:1", "show e2:2", "show e2:3"]);
    expect(r.sleeps).toEqual([1000]);
  });

  test("R1-2: a clear asks until again: the replay of a restored installation is shown past its first event", async () => {
    const s = server({
      streams: [
        { chunks: [frame(ev("e1:1")), frame(ev("e1:2", "paused"))], epoch: "e1" },
        { chunks: ["event: reset\ndata: {}\n\n", frame(ev("e2:1")), frame(ev("e2:2", "staged")), frame(ev("e2:3", "paused"))], epoch: "e2" },
      ],
      states: ["running@e2", "running@e2"],
      epoch: "e2",
    });
    // The old paused event stops the watch; after the clear only the replay's own paused event does.
    const r = recorder((shown) => shown.some((e) => e.type === "paused"));
    const out = await watch(s.client, OP, r.w, r.pace);
    expect(out).toMatchObject({ kind: "ended", operation: { state: "running" } });
    expect(r.log).toEqual(["show e1:1", "show e1:2", `clear: ${RESTORED}`, "show e2:1", "show e2:2", "show e2:3"]);
  });

  test("R1-4: the JSON pages stop the watch once the sign-in ended, and no further read is sent", async () => {
    const s = server({ streams: [{ chunks: ["{}"], type: "application/json" }], states: ["running"], pages: [ev("e1:1")] });
    const r = recorder();
    // A sleep that lets the timers run, and ends a watch that never stops by itself.
    r.pace.sleep = async (ms) => {
      r.sleeps.push(ms);
      await new Promise((done) => setTimeout(done, 0));
      if (r.sleeps.length >= 20) r.abort.abort();
    };
    const show = r.w.show;
    r.w.show = (e) => { show(e); s.client.endWrites(); };
    expect(await watch(s.client, OP, r.w, r.pace)).toEqual({ kind: "stopped" });
    expect(r.abort.signal.aborted).toBe(false);
    expect(r.sleeps.length).toBeLessThanOrEqual(1);
  });

  test("R1-5: events cleared on every attempt are not progress: the watch backs off", async () => {
    const mismatched = () => ({ chunks: [frame(ev("e2:1"))], epoch: "e1" });
    const s = server({
      streams: [mismatched(), mismatched(), mismatched(), mismatched(), { chunks: [frame(ev("e1:1", "succeeded"))] }],
      states: ["running", "running", "running", "running", "succeeded"],
      pages: [ev("e1:1", "succeeded")],
    });
    const r = recorder();
    expect((await watch(s.client, OP, r.w, r.pace)).kind).toBe("ended");
    // The mismatched event is cleared, never shown, so no attempt before the last made progress.
    expect(r.sleeps).toEqual([1000, 2000, 4000, 8000]);
  });

  test("R1-6: a slow operation read does not skip the backoff of a stream that ended at once", async () => {
    const clock = { t: 0 };
    const s = server({
      streams: [{ chunks: [] }, { chunks: [] }, { chunks: [] }, { chunks: [frame(ev("e1:1", "succeeded"))] }],
      states: ["running", "running", "running", "succeeded"],
      pages: [ev("e1:1", "succeeded")],
      clock,
      readTime: 1500,
    });
    const r = recorder(undefined, clock);
    expect((await watch(s.client, OP, r.w, r.pace)).kind).toBe("ended");
    expect(r.sleeps).toEqual([1000, 2000, 4000]);
  });

  test("R1-7: bytes that never make a line do not keep the idle timer alive: the stream is cut and reopened", async () => {
    const s = server({
      streams: [{ chunks: [], trickle: "x" }, { chunks: [frame(ev("e1:1", "succeeded"))] }],
      states: ["running", "succeeded"],
      pages: [ev("e1:1", "succeeded")],
    });
    const r = recorder();
    const out = await Promise.race([watch(s.client, OP, r.w, r.pace), new Promise((done) => setTimeout(() => done("still open"), 300))]);
    r.abort.abort();
    expect(out).toMatchObject({ kind: "ended" });
    expect(s.opened).toHaveLength(2);
  });

  // Findings of the second review of the reader (R2-n).

  test("R2-1: the epoch of a terminal read is that of its response, not the client's epoch when the watch resumes", async () => {
    const s = server({
      streams: [{ chunks: [frame(ev("e1:1"))] }, { chunks: [], epoch: "e2" }],
      states: ["failed@e1", "succeeded@e2"],
      pages: [ev("e2:1"), ev("e2:2", "staged"), ev("e2:3", "succeeded")],
      pagesEpoch: "e2",
    });
    const r = recorder();
    // Another response records the restore after the operation response was used and before the watch resumes.
    const out = await watch(reader(s, operationPath, "after"), OP, r.w, r.pace);
    expect(out).toMatchObject({ kind: "ended", operation: { state: "succeeded" } });
    expect(r.log).toEqual(["show e1:1", `clear: ${RESTORED}`, "show e2:1", "show e2:2", "show e2:3"]);
  });

  test("R2-2: a running read under another epoch than the last event clears before until is asked", async () => {
    const s = server({
      streams: [
        { chunks: [frame(ev("e1:1")), frame(ev("e1:2", "paused"))] },
        { chunks: [frame(ev("e2:1")), frame(ev("e2:2", "paused"))], epoch: "e2" },
      ],
      states: ["running@e2"],
      pagesEpoch: "e2",
    });
    // The old paused event stops the stream; the read that follows is under e2, so it must not stop the watch.
    const r = recorder((shown) => shown.some((e) => e.type === "paused"));
    const out = await watch(reader(s, operationPath, "before"), OP, r.w, r.pace);
    expect(out).toMatchObject({ kind: "ended", operation: { state: "running" } });
    expect(r.log).toEqual(["show e1:1", "show e1:2", `clear: ${RESTORED}`, "show e2:1", "show e2:2"]);
  });

  test("R2-3: events shown and then cleared in one attempt are not progress: the watch backs off", async () => {
    const shownThenMismatched = () => ({ chunks: [frame(ev("e1:1")), frame(ev("e2:2"))], epoch: "e1" });
    const s = server({
      streams: [shownThenMismatched(), shownThenMismatched(), shownThenMismatched(), { chunks: [frame(ev("e1:1", "succeeded"))] }],
      states: ["running", "running", "running", "succeeded"],
      pages: [ev("e1:1", "succeeded")],
    });
    const r = recorder();
    expect((await watch(s.client, OP, r.w, r.pace)).kind).toBe("ended");
    expect(r.sleeps).toEqual([1000, 2000, 4000]);
  });

  test("R2-3b: events cleared at once because the operation read names another epoch are not progress", async () => {
    const s = server({
      streams: [{ chunks: [frame(ev("e1:1")), frame(ev("e1:2"))] }, { chunks: [frame(ev("e2:1", "succeeded"))], epoch: "e2" }],
      states: ["running@e2", "succeeded@e2"],
      pages: [ev("e2:1", "succeeded")],
      pagesEpoch: "e2",
    });
    const r = recorder();
    expect((await watch(reader(s, operationPath, "before"), OP, r.w, r.pace)).kind).toBe("ended");
    expect(r.log).toEqual(["show e1:1", "show e1:2", `clear: ${RESTORED}`, "show e2:1"]);
    expect(r.sleeps).toEqual([1000]);
  });

  test("R2-3c: a reset in the stream followed by its replay is progress: no wait before the next attempt", async () => {
    const s = server({
      streams: [
        { chunks: [frame(ev("e1:1")), "event: reset\ndata: {}\n\n", frame(ev("e1:2"))] },
        { chunks: [frame(ev("e1:3", "succeeded"))] },
      ],
      states: ["running", "succeeded"],
      pages: [ev("e1:2"), ev("e1:3", "succeeded")],
    });
    const r = recorder();
    expect((await watch(s.client, OP, r.w, r.pace)).kind).toBe("ended");
    expect(r.log).toEqual(["show e1:1", `clear: ${RESTORED}`, "show e1:2", "show e1:3"]);
    expect(r.sleeps).toEqual([]);
  });

  test("R2-4: a stream that stayed open for a second or more and showed nothing still backs off", async () => {
    const clock = { t: 0 };
    const s = server({
      streams: [{ chunks: [] }, { chunks: [] }, { chunks: [] }, { chunks: [frame(ev("e1:1", "succeeded"))] }],
      states: ["running", "running", "running", "succeeded"],
      pages: [ev("e1:1", "succeeded")],
      clock,
      streamTime: 1100,
    });
    const r = recorder(undefined, clock);
    expect((await watch(s.client, OP, r.w, r.pace)).kind).toBe("ended");
    expect(r.sleeps).toEqual([1000, 2000, 4000]);
  });

  test("R2-4b: a stream that stayed open for 30 s without an event starts the delay again at 1 s", async () => {
    const clock = { t: 0 };
    const s = server({
      streams: [{ chunks: [] }, { chunks: [] }, { chunks: [] }, { chunks: [frame(ev("e1:1", "succeeded"))] }],
      states: ["running", "running", "running", "succeeded"],
      pages: [ev("e1:1", "succeeded")],
      clock,
      streamTime: 31_000,
    });
    const r = recorder(undefined, clock);
    expect((await watch(s.client, OP, r.w, r.pace)).kind).toBe("ended");
    expect(r.sleeps).toEqual([1000, 1000, 1000]);
  });

  test("R2-5: a final page that arrives after the watch ended is not delivered", async () => {
    let open!: () => void;
    const gate = new Promise<void>((done) => { open = done; });
    const s = server({ streams: [{ chunks: [] }], states: ["succeeded"], pages: [ev("e1:1"), ev("e1:2", "succeeded")], pagesGate: gate });
    const r = recorder();
    const out = watch(s.client, OP, r.w, r.pace);
    await new Promise((done) => setTimeout(done, 10));
    r.abort.abort();
    open();
    expect(await out).toEqual({ kind: "stopped" });
    expect(r.log).toEqual([]);
  });

  // Findings of the third review of the reader (R3-n).

  test("R3-1: an empty drain under another epoch than the terminal read clears what was shown and does not end the watch", async () => {
    const s = server({
      streams: [{ chunks: [frame(ev("e1:1"))] }, { chunks: [], epoch: "e2" }],
      states: ["failed@e1", "succeeded@e2"],
      pages: [],
      pagesEpoch: "e2",
    });
    const r = recorder();
    // Another response records the restore after the operation read and before the drain's read, and the drain's list is empty.
    const out = await watch(reader(s, eventsPath, "before"), OP, r.w, r.pace);
    expect(out).toMatchObject({ kind: "ended", operation: { state: "succeeded" } });
    expect(r.log).toEqual(["show e1:1", `clear: ${RESTORED}`]);
    expect(r.sleeps).toEqual([1000]);
  });

  test("R3-1b: an empty drain under another epoch than the terminal read does not end the watch when nothing was shown", async () => {
    const s = server({
      streams: [{ chunks: [] }, { chunks: [], epoch: "e2" }],
      states: ["failed@e1", "succeeded@e2"],
      pages: [],
      pagesEpoch: "e2",
    });
    const r = recorder();
    const out = await watch(reader(s, eventsPath, "before"), OP, r.w, r.pace);
    expect(out).toMatchObject({ kind: "ended", operation: { state: "succeeded" } });
    expect(r.log).toEqual([]);
    expect(r.sleeps).toEqual([1000]);
  });

  // The page leaves while the operation read is pending: the reader's read resolves after the abort.
  const leavingAtRead = (s: ReturnType<typeof server>, abort: AbortController) => ({
    read: ((path: string) => s.client.read(path)) as typeof s.client.read,
    readWithEpoch: (async (path: string) => {
      const out = await s.client.readWithEpoch(path);
      if (operationPath(path)) abort.abort();
      return out;
    }) as typeof s.client.readWithEpoch,
    open: (...a: Parameters<typeof s.client.open>) => s.client.open(...a),
    get currentEpoch() { return s.client.currentEpoch; },
    get writesEnded() { return s.client.writesEnded; },
  });

  test("R3-2: a running operation read that resolves after the watch was aborted does not ask until or end the watch", async () => {
    const s = server({ streams: [{ chunks: [] }], states: ["running"] });
    let asked = 0;
    const r = recorder(() => { asked++; return true; });
    expect(await watch(leavingAtRead(s, r.abort), OP, r.w, r.pace)).toEqual({ kind: "stopped" });
    expect(asked).toBe(0);
    expect(r.log).toEqual([]);
  });

  test("R3-2b: a terminal operation read that resolves after the watch was aborted sends no events request", async () => {
    const s = server({ streams: [{ chunks: [] }], states: ["succeeded"], pages: [ev("e1:1", "succeeded")] });
    const r = recorder();
    expect(await watch(leavingAtRead(s, r.abort), OP, r.w, r.pace)).toEqual({ kind: "stopped" });
    // The stream's own request is the only one to the events path.
    expect(s.urls.filter((u) => u.startsWith(EVENTS))).toEqual([EVENTS]);
    expect(r.log).toEqual([]);
  });

  // Findings of the fourth round (R4-n): what the caller keeps from the operation it was given is kept
  // under the epoch of that read, which the client's epoch can have left by the time the caller looks.

  test("R4-1: a terminal end carries the epoch of the operation read, not the client's epoch after the drain", async () => {
    const all = [ev("e1:1"), ev("e1:2", "succeeded")];
    const s = server({ streams: [{ chunks: [all.map(frame).join("")] }], states: ["succeeded"], pages: all, probeEpoch: "e2" });
    const r = recorder();
    // Another response records the epoch e2 after the last events page was used, before the watch returns.
    const w = reader(s, eventsPath, "after");
    const out = await watch(w, OP, r.w, r.pace);
    expect(w.currentEpoch).toBe("e2");
    expect(out).toEqual({ kind: "ended", operation: { id: OP, kind: "ingest", state: "succeeded" }, epoch: "e1" });
  });

  test("R4-1b: an end on until carries the epoch of the operation read, not the client's epoch after it", async () => {
    const s = server({ streams: [{ chunks: [frame(ev("e1:1", "paused"))] }], states: ["running"], probeEpoch: "e2" });
    const r = recorder((shown) => shown.some((e) => e.type === "paused"));
    // Another response records the epoch e2 after the operation response was used, before the watch resumes.
    const w = reader(s, operationPath, "after");
    const out = await watch(w, OP, r.w, r.pace);
    expect(w.currentEpoch).toBe("e2");
    expect(out).toEqual({ kind: "ended", operation: { id: OP, kind: "ingest", state: "running" }, epoch: "e1" });
  });

  test("eventLine names the type, the generation and the code, and nothing else of the data", () => {
    expect(eventLine(ev("e1:1", "attempt", { generation: 3, code: "precondition-failed", secret: "SYNTH-do-not-show" }))).toBe("attempt (generation 3): precondition-failed");
    expect(eventLine(ev("e1:2", "paused"))).toBe("paused");
    expect(eventLine(ev("e1:3", "x", null))).toBe("x");
  });
});
