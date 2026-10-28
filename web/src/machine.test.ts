import { describe, expect, test, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { ADOPTED, at, attribution, details, EITHER, type Entry, FROZEN, HEALTH, latestUnread, nextTimeline, type Observation, POLL_MS, polled, readLater, readOlder, sentences, type Timeline, UNREAD } from "./machine.tsx";
import type { Machine } from "./machines.tsx";
import type { ReadResult } from "./request.ts";

// Synthetic machine and observation reads, as the generated types describe them.
const machine = (over: Partial<Machine>): Machine => ({
  id: "mch_a", cluster: "cl_a", hardware: { smbiosUuid: null, talosNodeId: "node-a", serial: null }, platform: "metal", talosEndpoint: "10.5.0.2:50000",
  desired: "rel_new", applied: { release: "rel_new", source: "operation" }, pendingConvergence: false,
  observed: { observation: "obs_a", at: "2026-10-26T09:46:00Z", release: "rel_new" }, frozen: false, scopeState: "normal", openDrift: null, ...over,
});
const observation = (id: string, basis: number, at: string, unread: Observation["unread"] = {}): Observation => ({
  id, machine: "mch_a", basis, revision: basis + 1, purpose: "scheduled", publicationGeneration: null, plan: null, operation: null,
  endpoint: "10.5.0.2:50000", controller: "ctl_a", startedAt: at, at, access: null, smbiosUuid: null, talosNodeId: "node-a",
  talosClusterId: null, assignmentEvidence: null, runningVersion: null, resourceVersion: null, health: null, unread,
});
const T = at;
const READ = "2026-10-26T09:46:00Z";
const obsA = observation("obs_a", 4, READ);
const drift = { revision: 3, observation: "obs_k", lastObservation: "obs_a", lateLanding: null };
const opened = observation("obs_k", 2, "2026-10-26T09:41:00Z");

describe("journeys §2.4 status sentences, field by field", () => {
  test("observed = applied, by an operation", () => {
    expect(sentences(machine({}), [obsA])).toEqual([`The configuration read at ${T(READ)} is attributed to rel_new, which is the Applied release (by operation).`, HEALTH]);
  });

  test("applied by adoption, with desired = applied.release and without", () => {
    const adopted = { release: "rel_new", source: "adoption" };
    expect(sentences(machine({ applied: adopted }), [obsA])).toEqual([expect.stringContaining("(by adoption)"), ADOPTED, EITHER, HEALTH]);
    expect(sentences(machine({ applied: adopted, desired: "rel_next" }), [obsA])).toEqual([
      expect.stringContaining("(by adoption)"), ADOPTED, "The selected release rel_next carries this machine's applied configuration unchanged.", HEALTH,
    ]);
  });

  test("pendingConvergence, applied not null and applied null", () => {
    expect(sentences(machine({ desired: "rel_next", pendingConvergence: true }), [obsA])).toContain(
      "Pending convergence: the configuration of the selected release rel_next for this machine differs from what Bronzeward applied.");
    expect(sentences(machine({ applied: null, pendingConvergence: true, observed: null }), [])).toEqual([
      "No configuration read recorded.", "Not applied or adopted by Bronzeward.", "Pending convergence: the selected release rel_new has not been applied to this machine.", HEALTH,
    ]);
  });

  test("openDrift is the only authority for drift, even when the read equals the selected release", () => {
    const m = machine({ desired: "rel_next", openDrift: drift, pendingConvergence: true, observed: { observation: "obs_a", at: READ, release: "rel_next" } });
    expect(sentences(m, [obsA, opened])).toEqual([
      `Drifted since ${T(opened.at)}: a read differed from what Bronzeward last verified (rel_new).`, "It equals the selected release; that does not close the drift.", HEALTH,
    ]);
    // observed = applied does not hide an open drift record.
    expect(sentences(machine({ openDrift: drift }), [obsA, opened])[0]).toMatch(/^Drifted since/);
  });

  test("no open drift and a read matching neither", () => {
    expect(sentences(machine({ observed: { observation: "obs_a", at: READ, release: null } }), [obsA])[0]).toBe(
      `The configuration read at ${T(READ)} matched neither Applied nor Desired, and no drift is open; an operation may hold the machine. See the timeline.`);
  });

  test("no open drift and a read equal to the selected release, not the Applied one: an operation holds the machine", () => {
    const m = machine({ desired: "rel_next", pendingConvergence: true, observed: { observation: "obs_a", at: READ, release: "rel_next" } });
    expect(sentences(m, [obsA])[0]).toBe(`The configuration read at ${T(READ)} is attributed to the selected release rel_next.`);
  });

  test("a later read that could not read, named by its causes; an earlier one or a complete one is not", () => {
    const late = observation("obs_p", 5, "2026-10-26T09:51:00Z", { configDigest: { code: "unreachable", cause: "node unreachable" } });
    expect(sentences(machine({}), [obsA, late])).toContain(`The latest read at ${T(late.at)} could not read: configDigest (node unreachable).`);
    const early = observation("obs_e", 3, "2026-10-26T09:40:00Z", { configDigest: { cause: "node unreachable" } });
    expect(latestUnread(machine({}), [obsA, early])).toBeUndefined();
    expect(latestUnread(machine({}), [obsA, observation("obs_q", 6, READ)])).toBeUndefined();
  });

  test("#423 C1: a read's time carries its date, so reads at the same time on different days differ", () => {
    expect(T("2026-10-26T09:46:00Z")).not.toBe(T("2026-10-25T09:46:00Z"));
    expect(T(READ)).toMatch(/^2026-10-2[5-7] \d\d:\d\d$/);
  });

  test("#423 C2: what a read could not read is named field by field, causes shared or not", () => {
    const both = observation("obs_p", 5, "2026-10-26T09:51:00Z", { configDigest: { cause: "none-bound" }, health: { cause: "none-bound" } });
    expect(sentences(machine({}), [obsA, both])).toContain(`The latest read at ${T(both.at)} could not read: configDigest (none-bound); health (none-bound).`);
  });

  test("#423 C3: a read observed names that the observations do not hold yet makes no older read the latest", () => {
    const late = observation("obs_p", 5, "2026-10-26T09:51:00Z", { configDigest: { cause: "node unreachable" } });
    const m = machine({ observed: { observation: "obs_z", at: "2026-10-26T09:56:00Z", release: "rel_new" } });
    expect(latestUnread(m, [obsA, late])).toBeUndefined();
    // Without an observed read every unread read is newer.
    expect(latestUnread(machine({ observed: null }), [late])).toBe(late);
  });

  test("#423 C5: the observed read's attribution, apart from the drift sentences", () => {
    expect(renderToStaticMarkup(attribution(null))).toBe("matches neither Applied nor Desired");
    expect(renderToStaticMarkup(attribution("rel_new"))).toBe('attributed to <span title="rel_new">rel_new</span>');
  });

  test("nothing applied, selected or read, and frozen: no time shown", () => {
    const s = sentences(machine({ applied: null, desired: null, observed: null, frozen: true }), []);
    expect(s).toEqual(["No configuration read recorded.", "Not applied or adopted by Bronzeward.", "No release selected.", FROZEN, HEALTH]);
    expect(s.join(" ")).not.toMatch(/\d\d:\d\d/);
  });

  test("never healthy: every combination ends with Health: not checked", () => {
    for (const m of [machine({}), machine({ openDrift: drift }), machine({ applied: null, observed: null })]) {
      const s = sentences(m, [obsA, opened]);
      expect(s.at(-1)).toBe(HEALTH);
      expect(s.join(" ")).not.toMatch(/healthy|converged|fresh/i);
    }
  });
});

// A client over a list of answers, recording each path read.
function client(answers: ReadResult<unknown>[]) {
  const paths: string[] = [];
  return { paths, read: <T,>(path: string) => (paths.push(path), Promise.resolve(answers.shift() as ReadResult<T>)) };
}
const page = (items: number[], next?: string, tail?: string): ReadResult<unknown> => ({
  kind: "ok", data: { items, ...(next === undefined ? {} : { next }), ...(tail === undefined ? {} : { tail }) },
});
const restart: ReadResult<unknown> = { kind: "restart", problem: { type: "urn:bronzeward:problem:cursor-invalid", title: "Cursor invalid", status: 400 } };
// A timeline read, newest first, with its cursors.
// A read no newer one has superseded.
const on = () => true;
const shown = (items: number[], older?: string, tail?: string, restarted = false): Timeline<number> => ({ items, older, tail, read: true, restarted });

describe("the timeline: newest page, Load older and the poll (client contracts §8.6 item 6)", () => {
  test("the first read is the newest page, in one request with order=newest, shown as answered", async () => {
    const c = client([page([5, 4, 3], "o3", "t5")]);
    expect(await readLater(c, "/t", UNREAD)).toEqual({ kind: "ok", data: shown([5, 4, 3], "o3", "t5") });
    expect(c.paths).toEqual(["/t?order=newest"]);
  });

  test("Load older appends the older page, read with order=newest from the page's next", async () => {
    const c = client([page([2, 1], "o1", "t6")]);
    // The answer's tail is the poll's to follow, not Load older's.
    expect(await readOlder(c, "/t", shown([5, 4, 3], "o3", "t5"), on)).toEqual({ kind: "ok", data: shown([5, 4, 3, 2, 1], "o1", "t5") });
    expect(c.paths).toEqual(["/t?order=newest&cursor=o3"]);
  });

  test("the end of the timeline: a page without next leaves nothing older, so Load older is not offered", async () => {
    const c = client([page([2, 1], undefined, "t5")]);
    const res = await readOlder(c, "/t", shown([5, 4, 3], "o3", "t5"), on);
    expect(res).toEqual({ kind: "ok", data: shown([5, 4, 3, 2, 1], undefined, "t5") });
    // Nothing is read once the start is shown, and a first page without next is the whole timeline.
    expect(await readOlder(c, "/t", shown([1]), on)).toEqual({ kind: "ok", data: shown([1]) });
    expect(c.paths).toEqual(["/t?order=newest&cursor=o3"]);
    expect(await readLater(client([page([1], undefined, "t1")]), "/t", UNREAD)).toEqual({ kind: "ok", data: shown([1], undefined, "t1") });
  });

  test("the poll reads on from tail in oldest order, follows next, keeps the last page's tail and shows later entries first", async () => {
    const c = client([page([4, 5], "c5", "t5"), page([6], undefined, "t6"), page([], undefined, "t6")]);
    const first = await readLater(c, "/t", shown([3, 2], "o2", "t3"));
    expect(first).toEqual({ kind: "ok", data: shown([6, 5, 4, 3, 2], "o2", "t6") });
    expect(await readLater(c, "/t", (first as { data: Timeline<number> }).data)).toEqual(first);
    expect(c.paths).toEqual(["/t?cursor=t3", "/t?cursor=c5", "/t?cursor=t6"]);
  });

  test("no tail while the machine has none: the poll reads from the start", async () => {
    const c = client([page([1, 2], undefined, "t2")]);
    expect(await readLater(c, "/t", shown([]))).toEqual({ kind: "ok", data: shown([2, 1], undefined, "t2") });
    expect(c.paths).toEqual(["/t"]);
  });

  test("a refused cursor reads the newest page again, once, for the poll and for Load older", async () => {
    const c = client([restart, page([9], "o9", "t9"), restart, page([9], "o9", "t9")]);
    expect(await readLater(c, "/t", shown([1, 2], undefined, "old"))).toEqual({ kind: "ok", data: shown([9], "o9", "t9", true) });
    expect(await readOlder(c, "/t", shown([2], "old", "t2"), on)).toEqual({ kind: "ok", data: shown([9], "o9", "t9", true) });
    expect(c.paths).toEqual(["/t?cursor=old", "/t?order=newest", "/t?order=newest&cursor=old", "/t?order=newest"]);
    expect(await readLater(client([restart]), "/t", UNREAD)).toMatchObject({ kind: "refused", status: 400 });
    expect(await readLater(client([{ kind: "transport" }]), "/t", UNREAD)).toEqual({ kind: "transport" });
  });

  test("a restarted timeline says so (PAGE-02) through the reads after it; a first read does not", async () => {
    const c = client([restart, page([9], "o9", "t9"), page([10], undefined, "t10"), page([8], undefined, "t10")]);
    const res = await readLater(c, "/t", shown([1], undefined, "old"));
    expect(res).toMatchObject({ kind: "ok", data: { restarted: true } });
    const later = await readLater(c, "/t", (res as { data: Timeline<number> }).data);
    expect(later).toEqual({ kind: "ok", data: shown([10, 9], "o9", "t10", true) });
    expect(await readOlder(c, "/t", (later as { data: Timeline<number> }).data, on)).toEqual({ kind: "ok", data: shown([10, 9, 8], undefined, "t10", true) });
    expect(await readLater(client([page([1], undefined, "t1")]), "/t", UNREAD)).toMatchObject({ kind: "ok", data: { restarted: false } });
  });

  test("review R1-F1: a restart that fails drops the timeline read before it and its refused cursor", async () => {
    const c = client([restart, { kind: "transport" }]);
    const from = shown([1, 2], undefined, "old");
    const res = await readLater(c, "/t", from);
    expect(res).toEqual({ kind: "dropped", failed: { kind: "transport" } });
    expect(c.paths).toEqual(["/t?cursor=old", "/t?order=newest"]);
    // The next poll reads the newest page again; an ordinary failure keeps the timeline it had.
    expect(nextTimeline(from, res)).toEqual(UNREAD);
    expect(nextTimeline(from, { kind: "transport" })).toBe(from);
  });

  test("review R1-F2: one restart per read: a cursor refused again is surfaced, not read again", async () => {
    const c = client([restart, restart, page([1], "c1")]);
    expect(await readLater(c, "/t", shown([1], undefined, "c1"))).toMatchObject({ kind: "dropped", failed: { kind: "refused", status: 400 } });
    expect(c.paths).toEqual(["/t?cursor=c1", "/t?order=newest"]);
  });

  test("review R1-F3: a traversal a newer read superseded sends no further page request, and still hands on the answer in flight", async () => {
    let going = true;
    const c = client([page([1], "c1"), page([2])]);
    const res = readLater(c, "/t", shown([], undefined, "t0"), () => going);
    going = false;
    expect(await res).toEqual({ kind: "discarded" });
    expect(c.paths).toEqual(["/t?cursor=t0"]);
    const revoked: ReadResult<unknown> = { kind: "revoked", problem: { type: "urn:bronzeward:problem:identity-revoked", title: "Identity revoked", status: 403 } };
    going = true;
    const refused = readLater(client([revoked]), "/t", shown([], undefined, "t0"), () => going);
    going = false;
    expect(await refused).toEqual(revoked);
  });

  test("review L1-F1: a superseded read whose cursor is refused does not read the newest page again", async () => {
    let going = true;
    // Each refused cursor is followed by the page a restart would read.
    const c = client([restart, restart, page([9], "o9", "t9"), page([9], "o9", "t9")]);
    const later = readLater(c, "/t", shown([1], "old", "old"), () => going);
    going = false;
    expect(await later).toEqual({ kind: "discarded" });
    going = true;
    const older = readOlder(c, "/t", shown([1], "old", "t1"), () => going);
    going = false;
    expect(await older).toEqual({ kind: "discarded" });
    expect(c.paths).toEqual(["/t?cursor=old", "/t?order=newest&cursor=old"]);
  });

  test("review L1-F2: a read queued behind another sends nothing once it is superseded or the shell dropped the reads", async () => {
    const c = client([page([9], "o9", "t9")]);
    expect(await readLater(c, "/t", UNREAD, () => false)).toEqual({ kind: "discarded" });
    expect(await readLater(c, "/t", shown([1], "o1", "t1"), () => false)).toEqual({ kind: "discarded" });
    expect(await readOlder(c, "/t", shown([1], "o1", "t1"), () => false)).toEqual({ kind: "discarded" });
    expect(c.paths).toEqual([]);
  });

  test("review R1-F4: a page opened hidden reads nothing until it is visible", async () => {
    vi.useFakeTimers();
    try {
      const page = { visibilityState: "hidden" as DocumentVisibilityState };
      let reads = 0;
      const stop = polled(() => Promise.resolve((reads++, true)), page);
      await vi.advanceTimersByTimeAsync(4 * POLL_MS);
      expect(reads).toBe(0);
      page.visibilityState = "visible";
      await vi.advanceTimersByTimeAsync(POLL_MS);
      expect(reads).toBe(1);
      stop();
    } finally {
      vi.useRealTimers();
    }
  });

  test("polled every 5 s, not while the page is hidden, resumed at the next check once visible, and stopped", async () => {
    vi.useFakeTimers();
    try {
      const page = { visibilityState: "visible" as DocumentVisibilityState };
      let reads = 0;
      const stop = polled(() => Promise.resolve((reads++, true)), page);
      await vi.advanceTimersByTimeAsync(0);
      expect(reads).toBe(1);
      await vi.advanceTimersByTimeAsync(POLL_MS - 1);
      expect(reads).toBe(1);
      await vi.advanceTimersByTimeAsync(1);
      expect(reads).toBe(2);
      page.visibilityState = "hidden";
      await vi.advanceTimersByTimeAsync(4 * POLL_MS);
      expect(reads).toBe(2);
      page.visibilityState = "visible";
      await vi.advanceTimersByTimeAsync(POLL_MS);
      expect(reads).toBe(3);
      stop();
      await vi.advanceTimersByTimeAsync(4 * POLL_MS);
      expect(reads).toBe(3);
      // An answer that ends the polling (a refusal) schedules no further read.
      let once = 0;
      polled(() => Promise.resolve((once++, false)), page);
      await vi.advanceTimersByTimeAsync(4 * POLL_MS);
      expect(once).toBe(1);
    } finally {
      vi.useRealTimers();
    }
  });

  test("an entry's details: who, why and what it names, identifiers shortened", () => {
    const e = (kind: string, entry: unknown): Entry => ({ revision: 1, epoch: "ep", at: READ, kind, entry });
    expect(details(e("freeze", { principal: "h-author", role: "author", reason: "synthetic incident" }))).toBe('h-author author "synthetic incident"');
    expect(details(e("drift", { record: 3, change: "opened", observation: "obs_abcdefghijklmnopqrstuvwxyz" }))).toBe("opened obs_abcd…wxyz");
    expect(details(e("unknown-kind", null))).toBe("");
  });
});
