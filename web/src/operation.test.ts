import { createElement as h, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import type { components } from "./api/v1";
import { ReadsProvider, type Reads } from "./list.tsx";
import { causeOf, CLOSING, ENDED, follow, LIVE, OperationTimeline, Outcome, type Row, rowOf, seenUnder, SERVER_TIMES_UNREAD, TimelineRows, type TimelineView, timelineView } from "./operation.tsx";
import { RESTORED } from "./progress.ts";
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
// The same events with their ids under `epoch`, as the server writes them.
const sent = (epoch: string) => EVENTS_SENT.map((e) => ({ ...e, id: e.id.replace("e1", epoch) }));
const frame = (e: { id: string; type: string; data: unknown }) => `id: ${e.id}\nevent: ${e.type}\ndata: ${JSON.stringify(e.data)}\n\n`;
const json = (body: unknown, status = 200, epoch = "e1") =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": status === 200 ? "application/json" : "application/problem+json", "Bronzeward-Epoch": epoch } });

// A fake server whose operation is `state` and whose JSON pages answer the first `pages` reads, then 403.
// Every answer is under e1 until the second pages read; that read and every answer after it is under `later`.
function server(state: string, pages: number, later = "e1") {
  let page = 0;
  let epoch = "e1";
  const fetch = async (url: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const u = String(url);
    const headers = (init?.headers ?? {}) as Record<string, string>;
    if (u === EVENTS && headers["Accept"] === "text/event-stream") {
      return new Response(sent(epoch).map(frame).join(""), { status: 200, headers: { "Content-Type": "text/event-stream", "Bronzeward-Epoch": epoch } });
    }
    if (u.startsWith(EVENTS)) {
      if (++page === 2) epoch = later;
      return page <= pages ? json({ items: sent(epoch) }, 200, epoch) : json({ type: "urn:bronzeward:problem:forbidden", title: "forbidden", status: 403 }, 403);
    }
    if (u === `${API_BASE}/operations/${OP}`) return state === "unreadable" ? json({ type: "urn:bronzeward:problem:forbidden", title: "forbidden", status: 403 }, 403) : json({ id: OP, kind: "apply-config", state }, 200, epoch);
    throw new Error(`unexpected ${u}`);
  };
  return new Client({ fetch: fetch as typeof globalThis.fetch, token: () => "SYNTH-token", sleep: async () => {} });
}

// What `follow` did: each view after each action, and how often it asked for a read again.
async function run(state: string, pages: number, later = "e1") {
  let view: TimelineView = { rows: [], done: false };
  const views: TimelineView[] = [];
  const calls = { soon: 0, again: 0 };
  const client = server(state, pages, later);
  await follow(client, OP, {
    dispatch: (a) => {
      view = timelineView(view, a);
      views.push(view);
    },
    soon: () => void calls.soon++,
    again: () => void calls.again++,
    failure: () => "not read",
    signal: new AbortController().signal,
    epoch: () => client.currentEpoch,
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

  test("pages answered under another epoch than the watch ended on show the restored notice and the replayed list", async () => {
    const { view } = await run("completed", 5, "e2");
    expect(view.cleared).toBe(RESTORED);
    expect(view.rows.every((r) => r.server)).toBe(true);
    expect(view.rows.length).toBe(2);
    expect((await run("completed", 3)).view.cleared).toBeUndefined();
  });

  // A fake whose final read comes back `ok` and still marks a restore: "restart" refuses the first cursor
  // (the server restarted the list); "epoch" has the client at e2 when the read ends, as when the page's
  // own read of the operation took it there during the watch, while the pages answer `ok`. Only the first
  // read is marked, so the next round reads plainly.
  async function restoredOk(how: "restart" | "epoch") {
    const epoch = "e1";
    let armed = false;
    let refused = false;
    const fetch = async (url: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
      const u = String(url);
      const headers = (init?.headers ?? {}) as Record<string, string>;
      if (u === EVENTS && headers["Accept"] === "text/event-stream") {
        return new Response(sent(epoch).map(frame).join(""), { status: 200, headers: { "Content-Type": "text/event-stream", "Bronzeward-Epoch": epoch } });
      }
      if (u.startsWith(EVENTS)) {
        if (armed && how === "restart") {
          if (u.includes("cursor=")) {
            armed = false;
            refused = true;
            return json({ type: "urn:bronzeward:problem:cursor-invalid", title: "cursor-invalid", status: 400 }, 400, epoch);
          }
          if (!refused) return json({ items: sent(epoch).slice(0, 1), next: "c1" }, 200, epoch);
        }
        return json({ items: sent(epoch) }, 200, epoch);
      }
      if (u === `${API_BASE}/operations/${OP}`) return json({ id: OP, kind: "apply-config", state: "completed" }, 200, epoch);
      throw new Error(`unexpected ${u}`);
    };
    const client = new Client({ fetch: fetch as typeof globalThis.fetch, token: () => "SYNTH-token", sleep: async () => {} });
    let view: TimelineView = { rows: [], done: false };
    let again = 0;
    let moved = how === "epoch";
    await follow(client, OP, {
      dispatch: (a) => void (view = timelineView(view, a)),
      soon: () => {},
      again: () => void (armed = again++ === 0),
      failure: () => "not read",
      signal: new AbortController().signal,
      epoch: () => (moved && again === 1 ? ((moved = false), "e2") : client.currentEpoch),
      received: () => "2026-11-19T10:00:00Z",
    });
    return { view, again };
  }

  test("a final read the server restarted shows the restored notice and watches again", async () => {
    const { view, again } = await restoredOk("restart");
    expect(again).toBe(2);
    expect(view.cleared).toBe(RESTORED);
    expect(view.rows.every((r) => r.server)).toBe(true);
    expect(view.rows.length).toBe(2);
  });

  test("a final read answered under another epoch than the watch ended on shows the restored notice and watches again", async () => {
    const { view, again } = await restoredOk("epoch");
    expect(again).toBe(2);
    expect(view.cleared).toBe(RESTORED);
    expect(view.rows.map((r) => r.id)).toEqual(["e1:1", "e1:2"]);
    expect(view.rows.every((r) => r.server)).toBe(true);
  });

  // The shell mounts the page anew on an epoch change, so the notice is made at the mount: `was` is the epoch
  // the page was last mounted under, and the client's epoch is e1.
  async function mounted(was: string | undefined) {
    let view: TimelineView = { rows: [], done: false };
    const acts: string[] = [];
    const client = server("completed", 3);
    await follow(client, OP, {
      dispatch: (a) => {
        acts.push(a.type);
        view = timelineView(view, a);
      },
      soon: () => {}, again: () => {}, failure: () => "not read", signal: new AbortController().signal, epoch: () => "e1", was, received: () => "2026-11-19T10:00:00Z",
    });
    return { view, acts };
  }

  test("a page mounted under another epoch than it was last mounted under shows the restored notice once, then the rows of the new epoch", async () => {
    const { view, acts } = await mounted("e0");
    expect(view.cleared).toBe(RESTORED);
    expect(acts.filter((a) => a === "clear")).toEqual(["clear"]);
    expect(acts.slice(0, 2)).toEqual(["start", "clear"]);
    expect(view.rows.map((r) => r.id)).toEqual(["e1:1", "e1:2"]);
  });

  test("a page mounted under the epoch it showed under, or with nothing shown before, shows no notice", async () => {
    for (const was of ["e1", undefined]) {
      const { view, acts } = await mounted(was);
      expect(view.cleared).toBeUndefined();
      expect(acts).not.toContain("clear");
      expect(view.rows.length).toBe(2);
    }
  });

  // One rule, recorded once at each mount and by nothing a row carries, so a replay reaching a page that is being
  // replaced changes nothing: the page that opens the operation it opened last, under another epoch, owes the notice once.
  test("the page's memory: the operation opened last, per client; leaving and coming back reads it, another operation in between forgets it", () => {
    const client = {};
    expect(seenUnder(client, "op_1", "e1")).toBeUndefined();
    // Coming back under the same epoch owes nothing; the epoch changing owes the notice, once.
    expect(seenUnder(client, "op_1", "e1")).toBe("e1");
    expect(seenUnder(client, "op_1", "e2")).toBe("e1");
    expect(seenUnder(client, "op_1", "e2")).toBe("e2");
    expect(seenUnder({}, "op_1", "e2")).toBeUndefined();
    seenUnder(client, "op_2", "e2");
    expect(seenUnder(client, "op_1", "e2")).toBeUndefined();
    // A client that knows no epoch yet records nothing and forgets nothing.
    const fresh = {};
    seenUnder(fresh, "op_1", "e1");
    expect(seenUnder(fresh, "op_1", undefined)).toBe("e1");
    expect(seenUnder(fresh, "op_1", "e1")).toBe("e1");
  });

  test("a state change row names the state it reached and its cause, a token or a sentence the server wrote", () => {
    const state = (data: unknown) => rowOf({ id: "e1:3", type: "operation-state", data }, "2026-11-19T10:08:00Z", true);
    expect(state({ from: "unresolved", to: "cancelled", cause: "resolution" })).toMatchObject({ line: "operation-state: unresolved → cancelled (resolution)", to: "cancelled", cause: "resolution" });
    // The sentences `internal/api/settle.go` writes as causes.
    expect(state({ from: "committed", to: "failed", cause: "a completion observation contradicts the postconditions" })).toMatchObject({ line: "operation-state: committed → failed (a completion observation contradicts the postconditions)", cause: "a completion observation contradicts the postconditions" });
    expect(state({ to: "unresolved", cause: "no completion observation by the verification deadline" }).cause).toBe("no completion observation by the verification deadline");
    // Not shown: a line feed, a bidirectional control, a cause over 300 characters, a state that is no token.
    for (const cause of ["a\nb", "a‮b", "x".repeat(301), 7]) {
      expect(state({ to: "unresolved", cause })).toMatchObject({ line: "operation-state: unresolved", to: "unresolved" });
      expect(state({ to: "unresolved", cause }).cause).toBeUndefined();
    }
    expect(state({ to: "a b", cause: "resolution" })).toMatchObject({ line: "operation-state" });
    expect(state({ to: { x: 1 } })).toMatchObject({ line: "operation-state" });
    expect(rowOf({ id: "e1:1", type: "commitment", data: { generation: 3, to: "x" } }, "2026-11-19T10:08:00Z", true).to).toBeUndefined();
  });

  test("the outcome's cause is that of the last change into the state shown", () => {
    const row = (id: string, to: string, cause?: string): Row => ({ id, line: "x", at: "2026-11-19T10:00:00Z", server: true, to, ...(cause !== undefined && { cause }) });
    const rows = [row("e1:1", "unresolved", "takeover"), row("e1:2", "cancelled", "resolution"), row("e1:3", "unresolved", "recovery-entry")];
    expect(causeOf(rows, "cancelled")).toBe("resolution");
    expect(causeOf(rows, "unresolved")).toBe("recovery-entry");
    expect(causeOf(rows, "completed")).toBeUndefined();
    expect(causeOf([], "cancelled")).toBeUndefined();
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

  // A hook for the fixture run's comparison (fixtures/participants/web-watch.mjs): each row names the server's
  // event id, which the page shows nowhere else. It is an attribute, so the text above is unchanged.
  test("each row carries the server's event id and nothing else of the event", () => {
    const rows: Row[] = [{ id: "e1:1", line: "commitment", at: "2026-11-19T10:06:02Z", server: false }, { id: "e1:2", line: "response", at: "2026-11-19T10:07:03Z", server: true }];
    const markup = renderToStaticMarkup(h(TimelineRows, { rows }));
    expect([...markup.matchAll(/<li data-event-id="([^"]*)">/g)].map((m) => m[1])).toEqual(["e1:1", "e1:2"]);
    expect(markup.match(/<li[^>]*>/g)).toHaveLength(2);
  });

  // ginsys/bronzeward#998: the page says what the operation did and what comes next, above and below its outcome.
  test("the page opens with the sentence of its kind and state and ends its outcome with the next step", () => {
    const page = (o: Partial<Operation>) => h(OperationTimeline, { id: OP, o: op(o), reread: () => {} });
    const markup = html(page({ kind: "apply-config", state: "rejected" }));
    expect(markup).toMatch(/data-testid="now">The machine was not changed\.<\/p>/);
    expect(markup).toMatch(/data-testid="next">Next: Correct the release and create a new plan\.<\/p>/);
    expect(markup.indexOf('data-testid="now"')).toBeLessThan(markup.indexOf('data-testid="outcome"'));
    expect(markup.indexOf('data-testid="outcome"')).toBeLessThan(markup.indexOf('data-testid="next"'));
  });

  test("an unresolved operation carries no second next step, since its outcome line names the Recovery route", () => {
    expect(html(h(OperationTimeline, { id: OP, o: op({ state: "unresolved" }), reread: () => {} }))).not.toContain('data-testid="next"');
  });

  test("an unknown kind or state shows both raw values, never a blank, and no next step", () => {
    const markup = html(h(OperationTimeline, { id: OP, o: op({ kind: "frobnicate", state: "sideways" }), reread: () => {} }));
    expect(markup).toContain("An operation of kind frobnicate in state sideways; this page has no description of it.");
    expect(markup).toContain("In progress: sideways.");
    expect(markup).not.toContain('data-testid="next"');
  });

  test("an adoption says it recorded the machine's configuration as Applied, not that no applied release is recorded", () => {
    const line = text(h(Outcome, { o: op({ kind: "adopt", state: "completed", result: { adoptionRecord: "pln_aaaaaaaaaaaaaaaaaaaaaaaaaa" } }) }));
    expect(line).toMatch(/^Adopted: the adoption record is .*Desired, Applied and Observed of the machine$/);
    expect(line).not.toContain("No applied release");
  });

  test("the page says it follows the operation and that closing it stops nothing, and has no retry control", () => {
    const page = h(OperationTimeline, { id: OP, o: op({ state: "sending" }), reread: () => {} });
    expect(text(page)).toContain(LIVE);
    expect(text(page)).toContain(CLOSING);
    expect(text(page)).not.toContain(ENDED);
    expect(html(page)).not.toMatch(/<button/i);
  });
});

describe("the outcome lines", () => {
  const states: [string, Partial<Operation>, RegExp][] = [
    ["completed with an applied release", { state: "completed", result: { appliedRelease: "rel_aaaaaaaaaaaaaaaaaaaaaaaaaa", observation: "obs_aaaaaaaaaaaaaaaaaaaaaaaaaa" } }, /^Applied: this operation applied release .*, confirmed by observation .*Desired, Applied and Observed of the machine$/],
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

  test("a failed publish or ingest names its problem's code, and says nothing of attempts it has none of", () => {
    const problem = (type: string) => ({ type, title: "t", status: 503, instance: "req_a" }) as unknown as Operation["error"];
    expect(text(h(Outcome, { o: op({ state: "failed", error: problem("urn:bronzeward:problem:dependency-unavailable") }) }))).toBe("Failed: dependency-unavailable.");
    expect(text(h(Outcome, { o: op({ state: "failed", error: problem("urn:bronzeward:problem:x y") }) }))).toBe("Failed.");
  });

  test("a failed operation names the sentence the server wrote as its cause", () => {
    const error = { outcome: "failed", cause: "a completion observation contradicts the postconditions", observation: "obs_a", attempts: 2 } as const;
    expect(text(h(Outcome, { o: op({ state: "failed", error }) }))).toBe("Failed: a completion observation contradicts the postconditions (2 attempts).");
    expect(text(h(Outcome, { o: op({ state: "failed", error: { ...error, cause: "a\nb" } }) }))).toBe("Failed (2 attempts).");
  });

  test("an unresolved operation names a sentence cause from its state change row", () => {
    const row = rowOf({ id: "e1:3", type: "operation-state", data: { from: "committed", to: "unresolved", cause: "no completion observation by the verification deadline" } }, "2026-11-19T10:08:00Z", true);
    expect(text(h(Outcome, { o: op({ state: "unresolved" }), cause: causeOf([row], "unresolved") }))).toMatch(/^Outcome unknown \(no completion observation by the verification deadline\)\. /);
  });

  test("a cancelled operation names its cause when an event gave one", () => {
    expect(text(h(Outcome, { o: op({ state: "cancelled" }), cause: "resolution" }))).toBe("Cancelled: resolution.");
  });

  test("an unresolved operation names the cause of the change into it when an event gave one", () => {
    expect(text(h(Outcome, { o: op({ state: "unresolved" }), cause: "takeover" }))).toMatch(/^Outcome unknown \(takeover\)\. A recovery administrator must account for /);
    expect(text(h(Outcome, { o: op({ state: "unresolved" }) }))).toMatch(/^Outcome unknown\. A recovery administrator/);
  });

  test("an unresolved operation with no attempt does not name one", () => {
    const flat = text(h(Outcome, { o: op({ state: "unresolved", attempts: [] }) }));
    expect(flat).toContain("A recovery administrator must account for this operation: no attempt is recorded.");
    expect(flat).not.toMatch(/attempt \./);
  });

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
