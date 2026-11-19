import { createElement as h, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import type { components } from "./api/v1";
import { ReadsProvider, type Reads } from "./list.tsx";
import { CLOSING, ENDED, follow, LIVE, OperationTimeline, Outcome, type Row, SERVER_TIMES_UNREAD, TimelineRows, type TimelineView, timelineView } from "./operation.tsx";
import { API_BASE, Client } from "./request.ts";

type Operation = components["schemas"]["OperationBody"];

const OP = "op_1";
const EVENTS = `${API_BASE}/operations/${OP}/events`;
const reads: Reads = { client: { read: () => new Promise(() => {}) }, settle: () => false, undropped: () => () => true, view: "", again: 0, regained: 0, go: () => {}, failure: () => null };
const html = (e: ReactNode) => renderToStaticMarkup(h(ReadsProvider, { value: reads }, e));

const op = (o: Partial<Operation> = {}): Operation => ({
  id: OP, kind: "apply-config", state: "completed", epoch: "e1", lastEvent: 2, subject: { machine: "mch_a", plan: "pln_a" }, createdBy: null,
  createdAt: "2026-11-19T10:06:00Z", result: null, error: null, ...o,
});
const text = (e: ReactNode) => html(e).replace(/<[^>]+>/g, "").replaceAll("&#x27;", "'");

// The events of the fake server: the stream sends them, the JSON pages answer them with the server's times.
const EVENTS_SENT = [
  { id: "e1:1", type: "commitment", at: "2026-11-19T10:06:02Z", data: { generation: 3 } },
  { id: "e1:2", type: "operation-state", at: "2026-11-19T10:07:03Z", data: {} },
];
const frame = (e: { id: string; type: string; data: unknown }) => `id: ${e.id}\nevent: ${e.type}\ndata: ${JSON.stringify(e.data)}\n\n`;
const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": status === 200 ? "application/json" : "application/problem+json", "Bronzeward-Epoch": "e1" } });

// A fake server whose operation is `state` and whose JSON pages answer the first `pages` reads, then 403.
function server(state: string, pages: number) {
  let page = 0;
  const fetch = async (url: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const u = String(url);
    const headers = (init?.headers ?? {}) as Record<string, string>;
    if (u === EVENTS && headers["Accept"] === "text/event-stream") {
      return new Response(EVENTS_SENT.map(frame).join(""), { status: 200, headers: { "Content-Type": "text/event-stream", "Bronzeward-Epoch": "e1" } });
    }
    if (u.startsWith(EVENTS)) return ++page <= pages ? json({ items: EVENTS_SENT }) : json({ type: "urn:bronzeward:problem:forbidden", title: "forbidden", status: 403 }, 403);
    if (u === `${API_BASE}/operations/${OP}`) return state === "unreadable" ? json({ type: "urn:bronzeward:problem:forbidden", title: "forbidden", status: 403 }, 403) : json({ id: OP, kind: "apply-config", state });
    throw new Error(`unexpected ${u}`);
  };
  return new Client({ fetch: fetch as typeof globalThis.fetch, token: () => "SYNTH-token", sleep: async () => {} });
}

// What `follow` did: each view after each action, and how often it asked for a read again.
async function run(state: string, pages: number) {
  let view: TimelineView = { rows: [], done: false };
  const views: TimelineView[] = [];
  const calls = { soon: 0, again: 0 };
  await follow(server(state, pages), OP, {
    dispatch: (a) => {
      view = timelineView(view, a);
      views.push(view);
    },
    soon: () => void calls.soon++,
    again: () => void calls.again++,
    failure: () => "not read",
    signal: new AbortController().signal,
    received: () => "2026-11-19T10:00:00Z",
  });
  return { view, views, calls };
}

describe("the timeline (ginsys/bronzeward#359, W8)", () => {
  test("each event is a received row, and the server's times replace the rows when the watch ends", async () => {
    const { view, views, calls } = await run("completed", 3);
    // While the watch ran, every row was the browser's: marked received, never the server's time.
    const during = views.filter((v) => v.rows.length > 0 && !v.done);
    expect(during.length).toBeGreaterThan(0);
    for (const v of during) expect(v.rows.every((r) => !r.server && r.at === "2026-11-19T10:00:00Z")).toBe(true);
    // After it, each row carries the server's time, once, in order, and the watch is done.
    expect(view.done).toBe(true);
    expect(view.rows.map((r) => [r.id, r.at, r.server])).toEqual([["e1:1", "2026-11-19T10:06:02Z", true], ["e1:2", "2026-11-19T10:07:03Z", true]]);
    expect(view.rows.map((r) => r.line)).toEqual(["commitment (generation 3)", "operation-state"]);
    expect(calls.soon).toBe(2);
    expect(calls.again).toBe(1);
  });

  test("a read of the server's times that fails leaves the received rows, and says so", async () => {
    const { view } = await run("completed", 1);
    expect(view.rows.length).toBe(2);
    expect(view.rows.every((r) => !r.server)).toBe(true);
    expect(view.end).toBe(SERVER_TIMES_UNREAD);
    expect(view.done).toBe(true);
  });

  test("a read of the operation that fails ends the watch with the failure and no server times", async () => {
    const { view } = await run("unreadable", 3);
    expect(view.end).toBe("not read");
    expect(view.rows.every((r) => !r.server)).toBe(true);
  });

  test("the reducer: a new watch starts empty, a clear takes the rows back, an end keeps them", () => {
    const row = (id: string): Row => ({ id, line: "x", at: "2026-11-19T10:00:00Z", server: false });
    const shown = timelineView(timelineView({ rows: [], done: false }, { type: "show", row: row("e1:1") }), { type: "show", row: row("e1:2") });
    expect(shown.rows.map((r) => r.id)).toEqual(["e1:1", "e1:2"]);
    expect(timelineView(shown, { type: "clear", why: "restored" })).toEqual({ rows: [], cleared: "restored", done: false });
    expect(timelineView(shown, { type: "end", text: "t" })).toEqual({ rows: shown.rows, end: "t", done: true });
    expect(timelineView(timelineView(shown, { type: "end", text: "t" }), { type: "start" })).toEqual({ rows: [], done: false });
  });

  test("a row is marked received until the server's time replaces it", () => {
    const rows: Row[] = [{ id: "e1:1", line: "commitment", at: "2026-11-19T10:06:02Z", server: false }, { id: "e1:2", line: "response", at: "2026-11-19T10:07:03Z", server: true }];
    const flat = text(h(TimelineRows, { rows }));
    expect(flat).toMatch(/^received \d\d:\d\d:\d\d commitment\d\d:\d\d:\d\d response$/);
  });

  test("the page says it follows the operation and that closing it stops nothing, and has no retry control", () => {
    const page = h(OperationTimeline, { id: OP, reread: () => {} });
    expect(text(page)).toContain(LIVE);
    expect(text(page)).toContain(CLOSING);
    expect(text(page)).not.toContain(ENDED);
    expect(html(page)).not.toMatch(/<button/i);
  });
});

describe("the outcome lines", () => {
  const states: [string, Partial<Operation>, RegExp][] = [
    ["completed with an applied release", { state: "completed", result: { appliedRelease: "rel_aaaaaaaaaaaaaaaaaaaaaaaaaa", observation: "obs_aaaaaaaaaaaaaaaaaaaaaaaaaa" } }, /^Applied: release .* is the machine's Applied release, confirmed by observation .*Desired, Applied and Observed of the machine$/],
    ["completed with no applied release", { state: "completed", result: null }, /^Completed\. No applied release is recorded, so nothing is shown as applied\.$/],
    ["failed", { state: "failed", error: { outcome: "failed", cause: "verification-timeout", observation: "obs_a", attempts: 2 } }, /^Failed: verification-timeout \(2 attempts\)\.$/],
    ["rejected", { state: "rejected", error: { outcome: "rejected", cause: "invalid-config", attempts: 1 } }, /^Rejected: the node refused every attempt as invalid \(invalid-config\), 1 attempt\.$/],
    ["cancelled", { state: "cancelled" }, /^Cancelled\.$/],
    ["unresolved", { state: "unresolved", attempts: [{ number: 1 } as components["schemas"]["AttemptBody"]] }, /^Outcome unknown\. A recovery administrator must account for attempt 1\. No conflicting work on this machine proceeds until then\. Open in Recovery \(recovery-admin\)\. This page keeps following the operation\.$/],
    ["running", { state: "sending" }, /^In progress: sending\.$/],
  ];
  for (const [name, o, line] of states) {
    test(`${name} has its own line, and no retry control`, () => {
      expect(text(h(Outcome, { o: op(o) }))).toMatch(line);
      expect(html(h(Outcome, { o: op(o) }))).not.toMatch(/<button/i);
    });
  }

  test("only a completed operation whose result names the applied release is shown as applied", () => {
    for (const state of ["succeeded", "failed", "rejected", "cancelled", "unresolved", "sending", "committed"]) {
      expect(text(h(Outcome, { o: op({ state, result: { appliedRelease: "rel_a", observation: "obs_a" } }) })), state).not.toContain("Applied: release");
    }
  });

  test("an unresolved outcome links to Recovery, and an applied one to the machine's page", () => {
    expect(html(h(Outcome, { o: op({ state: "unresolved" }) }))).toContain('href="/recovery"');
    expect(html(h(Outcome, { o: op({ state: "completed", result: { appliedRelease: "rel_a", observation: "obs_a" } }) }))).toContain('href="/machines/mch_a"');
  });
});
