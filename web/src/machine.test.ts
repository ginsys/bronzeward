import { describe, expect, test, vi } from "vitest";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { ADOPTED, advanced, advanceReads, againW3, countingSettle, at, attribution, details, EITHER, type Entry, FROZEN, HEALTH, keepW3, kept, keptW3, latestUnread, MachineShown, nextTimeline, type Observation, Observations, POLL_MS, polled, queued, readLater, readOlder, sentences, stepAdvance, type Timeline, TimelineRows, UNREAD, type W3Kept } from "./machine.tsx";
import { type ShellEvents, ShellReads } from "./shell.tsx";
import { type Load, type Reads, ReadsProvider, RESTARTED } from "./list.tsx";
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
    // A release links to its view.
    const reads = { go: () => {} } as unknown as Reads;
    expect(renderToStaticMarkup(createElement(ReadsProvider, { value: reads }, attribution("rel_new")))).toBe('attributed to <a href="/releases/rel_new" title="rel_new">rel_new</a>');
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

  test("review C1: Load older before a new view's newest page answered reads nothing and replaces nothing shown", async () => {
    const c = client([page([1], undefined, "t1")]);
    const res = await readOlder(c, "/t", UNREAD, on);
    expect(res).toEqual({ kind: "discarded" });
    expect(c.paths).toEqual([]);
    expect(nextTimeline(UNREAD, res)).toBe(UNREAD);
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

  test("#424 item 1: the timeline and the observations each show the shared restart notice after a restart (PAGE-02), and not before", () => {
    const notice = (testid: string) => `<p role="status" data-testid="${testid}">${RESTARTED.replace(/'/g, "&#x27;")}</p>`;
    const rows = (restarted: boolean) => renderToStaticMarkup(createElement(TimelineRows, { tl: { ...UNREAD, read: true, restarted }, older: undefined, busy: false }));
    const observations = (restarted: boolean) => renderToStaticMarkup(createElement(Observations, { read: { items: [], next: undefined, restarted } }));
    expect(rows(true)).toContain(notice("timeline-restarted"));
    expect(observations(true)).toContain(notice("observations-restarted"));
    expect(rows(false)).not.toContain(RESTARTED.slice(0, 20));
    expect(observations(false)).not.toContain(RESTARTED.slice(0, 20));
  });

  test("#424 item 1: the observations' restart notice stays through the reads again after it, and goes with the view", () => {
    const flag = (o: { restarted: boolean }) => o.restarted;
    const ok = (restarted: boolean) => ({ kind: "ok" as const, data: { items: [], next: undefined, restarted } });
    const after = kept(kept(undefined, ok(true), "v", flag), ok(false), "v", flag);
    expect(after.restarted).toBe(true);
    expect(kept(after, ok(false), "w", flag).restarted).toBe(false);
    expect(kept(kept(undefined, ok(false), "v", flag), ok(false), "v", flag).restarted).toBe(false);
    // A view that changes only the query first shows the old view's answer: its flag stays behind.
    const old = ok(true);
    const moved = kept(kept(kept(undefined, old, "v", flag), old, "w", flag), ok(false), "w", flag);
    expect(moved.restarted).toBe(false);
  });

  test("#424: W3 shows a read again's failure above the data kept, and the observations' notice from what is kept", () => {
    const m = machine({ frozen: true });
    const read = { items: [obsA], next: undefined, restarted: false };
    const failed = { kind: "unavailable" as const, status: 503, problem: null };
    const reads = { go: () => {}, failure: () => "FAILED" } as unknown as Reads;
    const out = renderToStaticMarkup(createElement(ReadsProvider, { value: reads }, createElement(MachineShown, {
      id: "mch_a", m: failed, obs: { kind: "ok", data: read }, km: { view: "v", data: m, restarted: false }, ko: { view: "v", data: read, restarted: true },
      tl: { kind: "ok", data: { ...UNREAD, read: true } }, kt: { view: "v", data: { ...UNREAD, read: true }, restarted: false }, older: undefined, busy: false,
    })));
    expect(out).toMatch(/<h2>Machine mch_a<\/h2>FAILED<p>Cluster /);
    expect(out).toContain(FROZEN);
    expect(out).toContain('data-testid="observations-restarted"');
  });

  test("#424 item 2: a read again that fails, its 503 retries spent too, keeps the last answer under it; a new path, view or a dropped read keeps nothing", () => {
    const shown = kept(undefined, { kind: "ok", data: machine({ frozen: true }) }, "v");
    const failed = kept<Machine>(shown, { kind: "unavailable", status: 503, problem: null }, "v");
    expect(failed.data).toEqual(machine({ frozen: true }));
    expect(kept<Machine>(failed, { kind: "refused", status: 404, problem: null }, "v").data).toEqual(machine({ frozen: true }));
    expect(kept<Machine>(failed, { kind: "ok", data: machine({}) }, "v").data).toEqual(machine({}));
    expect(kept<Machine>(failed, { kind: "unavailable", status: 503, problem: null }, "w").data).toBeUndefined();
    expect(kept<Machine>(failed, { kind: "loading" }, "v").data).toBeUndefined();
    expect(kept<Machine>(failed, { kind: "dropped", failed: { kind: "transport" } }, "v").data).toBeUndefined();
    // A view that changes only the query first shows the old view's answer; a failure then keeps none of it.
    const old = { kind: "ok" as const, data: machine({ frozen: true }) };
    expect(kept<Machine>(kept(kept(undefined, old, "v"), old, "w"), { kind: "unavailable", status: 503, problem: null }, "w").data).toBeUndefined();
  });

  test("#424: W3 keeps the machine under a failure and the observations' restart flag, each from its own read", () => {
    const list = (restarted: boolean) => ({ kind: "ok" as const, data: { items: [obsA], next: undefined, restarted } });
    const tl = { kind: "ok" as const, data: { ...UNREAD, read: true } };
    const first = keptW3(undefined, { kind: "ok", data: machine({ frozen: true }) }, list(true), tl, "v");
    const [km, ko] = keptW3(first, { kind: "unavailable", status: 503, problem: null }, list(false), tl, "v");
    expect(km.data).toEqual(machine({ frozen: true }));
    expect(ko.restarted).toBe(true);
  });

  // The counter's steps only: MachineItem's wiring of them is not rendered here (no DOM renderer).
  test("#424 item 2, the read-again counter: advances faster than one read again do not abandon it; one more read again follows when both its reads settle", () => {
    const k = "v\n0";
    const s = (settled: number, key = k) => ({ key, settled });
    const run = (events: Parameters<typeof stepAdvance>[1][], from = advanceReads(k)) => events.reduce(stepAdvance, from);
    // The first page's two reads settle; the first advance then reads again.
    const first = run([s(0), s(0), "advance"]);
    expect(first).toEqual({ key: k, n: 1, left: 2, dirty: false });
    // Three more advances while the read again is in flight: it stays the one in flight.
    const busy = run([s(0), s(0), "advance", "advance", "advance", "advance"]);
    expect(busy.n).toBe(1);
    // A late answer from an earlier read counts for nothing; the read again's own two complete it.
    const done = run([s(0), s(1)], busy);
    expect(done).toEqual({ key: k, n: 1, left: 1, dirty: true });
    expect(stepAdvance(done, s(1))).toEqual({ key: k, n: 2, left: 2, dirty: false });
    // With no advance meanwhile, it ends there.
    expect(run([s(0), s(0), "advance", s(1), s(1)])).toEqual({ key: k, n: 1, left: 0, dirty: false });
  });

  test("#424 item 2, the read-again counter: a new view or read again of the page counts only its own reads", () => {
    const k = "v\n0";
    const run = (events: Parameters<typeof stepAdvance>[1][], from = advanceReads(k)) => events.reduce(stepAdvance, from);
    // A read again in flight with one more marked, then the page reads again (a new key).
    const moved = run([{ key: k, settled: 0 }, { key: k, settled: 0 }, "advance", "advance", { key: "v\n1" }]);
    expect(moved).toEqual({ key: "v\n1", n: 1, left: 2, dirty: false });
    // The earlier key's two reads still settle: they neither complete the new reads nor start another.
    const late = run([{ key: k, settled: 1 }, { key: k, settled: 1 }], moved);
    expect(late).toBe(moved);
    // An advance while the new key's reads are in flight waits for them.
    const waiting = run(["advance", { key: "v\n1", settled: 1 }], moved);
    expect(waiting).toEqual({ key: "v\n1", n: 1, left: 1, dirty: true });
    expect(stepAdvance(waiting, { key: "v\n1", settled: 1 })).toEqual({ key: "v\n1", n: 2, left: 2, dirty: false });
    // The same key again changes nothing.
    expect(stepAdvance(moved, { key: "v\n1" })).toBe(moved);
  });

  test("#424 item 2, the read-again counter: a read from an earlier visit to the same view settles on the page but counts for nothing", () => {
    // A, then B, then A again with no advance: the key and read again are A's first visit's.
    const k = "A\n0";
    let a = stepAdvance(stepAdvance(advanceReads(k), { key: "B\n0" }), { key: k });
    const outer: unknown[] = [];
    const settle = countingSettle((res) => outer.push(res) < 0, (e) => (a = stepAdvance(a, e)), k, a.n);
    const res = { kind: "ok", status: 200, data: {} } as unknown as Parameters<typeof settle>[0];
    // The first visit's two reads settle late: the page still takes both, the count does not move.
    settle(res, () => {}, () => false, () => false);
    settle(res, () => {}, () => false, () => false);
    expect(outer).toHaveLength(2);
    expect(a).toEqual({ key: k, n: 0, left: 2, dirty: false });
    // The return visit's own two reads count.
    settle(res, () => {}, () => true, () => false);
    settle(res, () => {}, () => true, () => false);
    expect(a).toEqual({ key: k, n: 0, left: 0, dirty: false });
  });

  test("#424 item 1: the timeline's restart notice stays through a read again in the same view, and goes with the view", () => {
    const m = { kind: "ok" as const, data: machine({}) };
    const obs = { kind: "ok" as const, data: { items: [], next: undefined, restarted: false } };
    // A read again (back online, the operator's read again) reads the timeline from its newest page: no restart.
    const tl = (restarted: boolean) => ({ kind: "ok" as const, data: { ...UNREAD, read: true, restarted } });
    const after = keptW3(keptW3(undefined, m, obs, tl(true), "v"), m, obs, tl(false), "v");
    expect(after[2].restarted).toBe(true);
    const reads = { go: () => {}, failure: () => "FAILED" } as unknown as Reads;
    const out = renderToStaticMarkup(createElement(ReadsProvider, { value: reads }, createElement(MachineShown, {
      id: "mch_a", m, obs, km: after[0], ko: after[1], tl: tl(false), kt: after[2], older: undefined, busy: false,
    })));
    expect(out).toContain('data-testid="timeline-restarted"');
    expect(keptW3(after, m, obs, tl(false), "w")[2].restarted).toBe(false);
  });

  test("#424: a machine read again that fails shows its failure even when the observations keep nothing", () => {
    const reads = { go: () => {}, failure: () => "FAILED" } as unknown as Reads;
    const out = renderToStaticMarkup(createElement(ReadsProvider, { value: reads }, createElement(MachineShown, {
      id: "mch_a", m: { kind: "refused", status: 404, problem: null }, obs: { kind: "dropped", failed: { kind: "transport" } },
      km: { view: "v", data: machine({}), restarted: false }, ko: { view: "v", data: undefined, restarted: false },
      tl: { kind: "ok", data: { ...UNREAD, read: true } }, kt: { view: "v", data: { ...UNREAD, read: true }, restarted: false }, older: undefined, busy: false,
    })));
    expect(out).toMatch(/<\/h2>FAILED<div><p role="status" data-testid="restarted">/);
  });

  test("#443: an observations read again that fails shows its failure after the heading, and the observations kept still show", () => {
    const reads = { go: () => {}, failure: () => "FAILED" } as unknown as Reads;
    const read = { items: [obsA], next: undefined, restarted: false };
    const failed = { kind: "unavailable" as const, status: 503, problem: null };
    const shown = (m: Load<Machine>) => renderToStaticMarkup(createElement(ReadsProvider, { value: reads }, createElement(MachineShown, {
      id: "mch_a", m, obs: failed, km: { view: "v", data: machine({ frozen: true }), restarted: false }, ko: { view: "v", data: read, restarted: false },
      tl: { kind: "ok", data: { ...UNREAD, read: true } }, kt: { view: "v", data: { ...UNREAD, read: true }, restarted: false }, older: undefined, busy: false,
    })));
    const out = shown({ kind: "ok", data: machine({ frozen: true }) });
    expect(out).toMatch(/<h2>Machine mch_a<\/h2>FAILED<p>Cluster /);
    expect(out).toContain('data-testid="observations"');
    expect(out).toContain('title="obs_a"');
    // Both reads failed, both kept: both failures, then the machine's rows and the observations.
    const both = shown(failed);
    expect(both).toMatch(/<h2>Machine mch_a<\/h2>FAILEDFAILED<p>Cluster /);
    expect(both).toContain(FROZEN);
    expect(both).toContain('data-testid="observations"');
  });

  describe("#443: a read without answer, settled by the shell with what W3 keeps", () => {
    const events: ShellEvents = { onUnauthenticated: vi.fn(), onRevoked: vi.fn(), onEpochChange: vi.fn(), onPreEntry: vi.fn(), setMode: vi.fn(), observe: vi.fn() };
    const shell = new ShellReads({ fetch: vi.fn() as unknown as typeof globalThis.fetch, sleep: async () => {} }, () => events);
    const okM = { kind: "ok" as const, data: machine({ frozen: true }) };
    const list = { kind: "ok" as const, data: { items: [obsA], next: undefined, restarted: false } };
    const failed = { kind: "unavailable" as const, status: 503, problem: null };
    const tl = { kind: "ok" as const, data: { ...UNREAD, read: true } };
    // Settles `res` for resource `i` as W3 does, and says whether it was kept under the banner.
    function settled(k: W3Kept, i: 0 | 1, res: ReadResult<unknown>, was: [boolean, boolean] = [false, false]) {
      const lost = { current: [...was] as [boolean, boolean] };
      const keep = keepW3({ current: k }, i, 0, lost);
      const set = vi.fn();
      vi.mocked(events.observe).mockClear();
      keep.lost(shell.settle(res, set, undefined, () => true, keep.held));
      return { set, lost: lost.current, observed: vi.mocked(events.observe).mock.calls.map(([e]) => e) };
    }

    test("after a kept failure: the data stays under the banner, and the failure does not move", () => {
      // The machine's last read failed: its own result holds no data, but W3 keeps the answer before.
      const k = keptW3(keptW3(undefined, okM, list, tl, "v"), failed, list, tl, "v");
      expect(k[0].data).toEqual(machine({ frozen: true }));
      const r = settled(k, 0, { kind: "transport" });
      expect(r.set).not.toHaveBeenCalled();
      expect(r.observed).toEqual(["lost"]);
      expect(r.lost).toEqual([true, false]);
      // The observations, kept from their own answer, too.
      expect(settled(k, 1, { kind: "transport" }).lost).toEqual([false, true]);
    });

    test("in a new view: nothing is kept until the new view first answers, so the read shows the disconnected state", () => {
      const k = keptW3(keptW3(undefined, okM, list, tl, "v"), okM, list, tl, "w");
      const r = settled(k, 0, { kind: "transport" });
      expect(r.set).toHaveBeenCalledWith({ kind: "transport" });
      expect(r.observed).toEqual([]);
      expect(r.lost).toEqual([false, false]);
      // Once the new view answers, its own answer is kept under the banner.
      const answered = keptW3(k, { kind: "ok", data: machine({}) }, list, tl, "w");
      expect(settled(answered, 0, { kind: "transport" }).lost).toEqual([true, false]);
    });

    test("an answer clears the lost mark; a banner ended by an answer reads each lost resource again once", () => {
      const k = keptW3(undefined, okM, list, tl, "v");
      expect(settled(k, 0, okM, [true, true]).lost).toEqual([false, true]);
      expect(settled(k, 1, failed, [true, true]).lost).toEqual([true, false]);
      expect(againW3([3, 5], [true, false])).toEqual([4, 5]);
      expect(againW3([3, 5], [true, true])).toEqual([4, 6]);
      expect(againW3([3, 5], [false, false])).toEqual([3, 5]);
    });
  });

  test("#443 case 5: with no observations kept, W3 still shows the machine's own rows under the observations' failure", () => {
    const reads = { go: () => {}, failure: () => "FAILED" } as unknown as Reads;
    const shown = (m: Load<Machine>, km: Machine | undefined) => renderToStaticMarkup(createElement(ReadsProvider, { value: reads }, createElement(MachineShown, {
      id: "mch_a", m, obs: { kind: "unavailable", status: 503, problem: null },
      km: { view: "v", data: km, restarted: false }, ko: { view: "v", data: undefined, restarted: false },
      tl: { kind: "ok", data: { ...UNREAD, read: true } }, kt: { view: "v", data: { ...UNREAD, read: true }, restarted: false }, older: undefined, busy: false,
    })));
    const answered = shown({ kind: "ok", data: machine({ frozen: true }) }, undefined);
    expect(answered).toMatch(/<h2>Machine mch_a<\/h2>FAILED<p>Cluster /);
    expect(answered).toContain('data-testid="sentences"');
    expect(answered).toContain(FROZEN);
    expect(answered).toContain("<dt>Desired</dt>");
    expect(answered).toContain("<h3>Timeline (newest first)</h3>");
    // Only the observations need the observations: no list, no empty list.
    expect(answered).not.toContain("<h3>Observations</h3>");
    expect(answered).not.toContain("No observation recorded.");
    // Both reads failed, the machine kept: both failures, then the kept machine's rows.
    expect(shown({ kind: "unavailable", status: 503, problem: null }, machine({ frozen: true }))).toMatch(/<\/h2>FAILEDFAILED<p>Cluster [^]*data-testid="sentences"/);
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

  test("#429: a poll queued behind a slow Load older sends nothing while the page is hidden, and polls again once it is visible", async () => {
    vi.useFakeTimers();
    try {
      const doc = { visibilityState: "visible" as DocumentVisibilityState };
      const c = client([page([1], "o1", "t1"), page([2])]);
      const settled: string[] = [];
      const step = queued<number>(on, on, (res) => settled.push(res.kind));
      const stop = polled((visible) => step((from) => readLater<number>(c, "/t", from, on), visible), doc);
      await vi.advanceTimersByTimeAsync(0);
      expect(c.paths).toEqual(["/t?order=newest"]);
      let answer = () => {};
      const slow = new Promise<void>((r) => (answer = r));
      void step(async (from) => (await slow, { kind: "ok", data: from }), on);
      // The poll falls due while the page is visible and waits behind Load older; the page hides first.
      await vi.advanceTimersByTimeAsync(POLL_MS);
      doc.visibilityState = "hidden";
      answer();
      await vi.advanceTimersByTimeAsync(4 * POLL_MS);
      expect(c.paths).toEqual(["/t?order=newest"]);
      expect(settled).toEqual(["ok", "ok"]);
      doc.visibilityState = "visible";
      await vi.advanceTimersByTimeAsync(POLL_MS);
      expect(c.paths).toEqual(["/t?order=newest", "/t?cursor=t1"]);
      stop();
    } finally {
      vi.useRealTimers();
    }
  });

  test("#424 item 2: a poll that brings entries not shown reads the machine again; the first read, an unchanged poll and a failure do not", async () => {
    const from = shown([1], undefined, "t1");
    const later = await readLater(client([page([2], undefined, "t2")]), "/t", from);
    expect(advanced(from, later)).toBe(true);
    expect(advanced(UNREAD, await readLater(client([page([1], undefined, "t1")]), "/t", UNREAD))).toBe(false);
    expect(advanced(from, await readLater(client([page([], undefined, "t1")]), "/t", from))).toBe(false);
    expect(advanced(from, { kind: "transport" })).toBe(false);
    // A restart's newest page with entries not shown counts too.
    expect(advanced(from, await readLater(client([restart, page([3], undefined, "t3")]), "/t", from))).toBe(true);
  });

  test("#424 item 2: a Load older that restarts, and the read after a dropped restart, count when they bring entries not shown", async () => {
    const moved: boolean[] = [];
    const step = queued<number>(on, on, (_res, m) => moved.push(m));
    const c = client([page([1], "o1", "t1"), restart, page([2, 1], "o1", "t2"), restart, { kind: "transport" }, page([3, 2, 1], "o1", "t3"), page([], undefined, "t3")]);
    await step((from) => readLater<number>(c, "/t", from), on);
    // Load older on a refused cursor reads the newest page again, with an entry not shown.
    await step((from) => readOlder<number>(c, "/t", from, on), on);
    // A poll's refused cursor, then a failure: dropped; the next poll reads the newest page from UNREAD.
    await step((from) => readLater<number>(c, "/t", from), on);
    await step((from) => readLater<number>(c, "/t", from), on);
    // Nothing new: no count.
    await step((from) => readLater<number>(c, "/t", from), on);
    expect(moved).toEqual([false, true, false, true, false]);
  });

  test("#424 item 2: a restart, or the read after a drop, that reads the same newest entry again as a new object does not count", async () => {
    type E = { revision: number };
    const ok = (revisions: number[], next?: string, tail?: string): ReadResult<unknown> => ({
      kind: "ok", data: { items: revisions.map((revision) => ({ revision })), ...(next === undefined ? {} : { next }), ...(tail === undefined ? {} : { tail }) },
    });
    const moved: boolean[] = [];
    const step = queued<E>(on, on, (_res, m) => moved.push(m));
    const c = client([ok([1], "o1", "t1"), restart, ok([1], "o1", "t1"), restart, { kind: "transport" }, ok([1], "o1", "t1"), restart, ok([2, 1], "o1", "t2")]);
    await step((from) => readLater<E>(c, "/t", from), on);
    // Load older on a refused cursor: the newest page again, its entry equal but built anew.
    await step((from) => readOlder<E>(c, "/t", from, on), on);
    // A poll's refused cursor, then a failure: dropped; the next poll reads the same newest entry.
    await step((from) => readLater<E>(c, "/t", from), on);
    await step((from) => readLater<E>(c, "/t", from), on);
    // A restart that does bring a newer entry counts.
    await step((from) => readOlder<E>(c, "/t", from, on), on);
    expect(moved).toEqual([false, false, false, false, true]);
  });

  test("an entry's details: who, why and what it names, identifiers shortened", () => {
    const e = (kind: string, entry: unknown): Entry => ({ revision: 1, epoch: "ep", at: READ, kind, entry });
    expect(details(e("freeze", { principal: "h-author", role: "author", reason: "synthetic incident" }))).toBe('h-author author "synthetic incident"');
    expect(details(e("drift", { record: 3, change: "opened", observation: "obs_abcdefghijklmnopqrstuvwxyz" }))).toBe("opened obs_abcd…wxyz");
    expect(details(e("unknown-kind", null))).toBe("");
  });
});
