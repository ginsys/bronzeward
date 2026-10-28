import { createElement as h } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import { type Listed, More, readPages, RESTARTED, short } from "./list.tsx";
import type { ReadResult } from "./request.ts";
import { badges, type Machine, observed } from "./machines.tsx";

// A client over a list of answers, recording each path read.
function client(answers: ReadResult<unknown>[]) {
  const paths: string[] = [];
  return { paths, read: <T,>(path: string) => (paths.push(path), Promise.resolve(answers.shift() as ReadResult<T>)) };
}
const page = (items: number[], next?: string): ReadResult<unknown> => ({ kind: "ok", data: next === undefined ? { items } : { items, next } });
const restart: ReadResult<unknown> = { kind: "restart", problem: { type: "urn:bronzeward:problem:cursor-invalid", title: "Cursor invalid", status: 400 } };
const start: Listed<number> = { items: [], next: undefined, restarted: false };

describe("cursor pages", () => {
  test("PAGE-01: every item once, in order, the filter kept and the cursor added", async () => {
    const c = client([page([1, 2], "c1"), page([3, 4], "c2"), page([5])]);
    const res = await readPages(c, "/machines", "cluster=cl_a", start, true);
    expect(res).toEqual({ kind: "ok", data: { items: [1, 2, 3, 4, 5], next: undefined, restarted: false } });
    expect(c.paths).toEqual(["/machines?cluster=cl_a", "/machines?cluster=cl_a&cursor=c1", "/machines?cluster=cl_a&cursor=c2"]);
  });

  test("one page on request, then the next from its cursor", async () => {
    const c = client([page([1], "c1"), page([2])]);
    const first = await readPages(c, "/clusters", "", start, false);
    expect(first).toEqual({ kind: "ok", data: { items: [1], next: "c1", restarted: false } });
    expect(await readPages(c, "/clusters", "", (first as { data: Listed<number> }).data, false)).toEqual({ kind: "ok", data: { items: [1, 2], next: undefined, restarted: false } });
    expect(c.paths).toEqual(["/clusters", "/clusters?cursor=c1"]);
  });

  test("a refused cursor drops what was read and restarts once from the first page", async () => {
    const c = client([restart, page([1, 2], "c9"), page([3])]);
    expect(await readPages(c, "/machines", "", { items: [7, 8], next: "old", restarted: false }, true)).toEqual({ kind: "ok", data: { items: [1, 2, 3], next: undefined, restarted: true } });
    expect(c.paths).toEqual(["/machines?cursor=old", "/machines", "/machines?cursor=c9"]);
    // A first page refused that way, or a second restart, is a refusal.
    expect(await readPages(client([restart]), "/machines", "", start, false)).toMatchObject({ kind: "refused", status: 400 });
    expect(await readPages(client([restart, page([1], "c1"), restart]), "/machines", "", { items: [], next: "old", restarted: false }, true)).toMatchObject({ kind: "refused", status: 400 });
  });

  test("any other answer is the list's", async () => {
    expect(await readPages(client([page([1], "c1"), { kind: "transport" }]), "/machines", "", start, true)).toEqual({ kind: "transport" });
    expect(await readPages(client([{ kind: "discarded" }]), "/machines", "", start, true)).toEqual({ kind: "discarded" });
  });

  test("the count, the next page on request (disabled while one is read) and the restart notice", () => {
    expect(renderToStaticMarkup(h(More, { list: { items: [1, 2, 3, 4], next: undefined, restarted: false }, shown: 4, more: undefined, busy: false }))).toBe('<p data-testid="count">Showing 4 of 4. </p>');
    const more = renderToStaticMarkup(h(More, { list: { items: [1, 2], next: "c", restarted: true }, shown: 1, more: () => {}, busy: false }));
    expect(more).toContain(`<p role="status" data-testid="restarted">${RESTARTED.replace(/'/g, "&#x27;")}</p>`);
    expect(more).toContain('Showing 1; more pages to read. <button type="button">Show more</button>');
    const busy = renderToStaticMarkup(h(More, { list: { items: [1, 2], next: "c", restarted: false }, shown: 2, more: () => {}, busy: true }));
    expect(busy).toContain('<button type="button" disabled="">Show more</button>');
  });

  test("G23: identifiers shortened to the prefix and four characters at each end", () => {
    expect(short("mch_k2pqrstuvwxyzabcdefgha7xz")).toBe("mch_k2pq…a7xz");
    expect(short("cl_abcdefghij")).toBe("cl_abcdefghij");
  });
});

// The clock the observed column is rendered at, pinned.
const NOW = Date.parse("2026-10-28T09:46:30Z");

// Synthetic machine reads, as the generated types describe them.
const machine = (over: Partial<Machine>): Machine => ({
  id: "mch_a", cluster: "cl_a", hardware: { smbiosUuid: null, talosNodeId: "node-a", serial: null }, platform: "metal", talosEndpoint: "10.5.0.2:50000",
  desired: "rel_new", applied: { release: "rel_new", source: "operation" }, pendingConvergence: false,
  observed: { observation: "obs_a", at: "2026-10-26T09:46:00Z", release: "rel_new" }, frozen: false, scopeState: "normal", openDrift: null, ...over,
});

describe("W2 badges and the observed column", () => {
  test("each state from the list's fields", () => {
    expect(badges(machine({}))).toEqual(["Matches applied"]);
    expect(badges(machine({ pendingConvergence: true, desired: "rel_next" }))).toEqual(["Pending convergence"]);
    expect(badges(machine({ openDrift: { revision: 1, observation: "obs_k", lastObservation: "obs_m", lateLanding: null }, frozen: true }))).toEqual(["Drifted", "Frozen"]);
    expect(badges(machine({ desired: null, applied: null, observed: null }))).toEqual(["Not handed over"]);
  });

  test("openDrift is the only authority for drift: an observation equal to Desired with drift open is no match of Applied", () => {
    const m = machine({ desired: "rel_next", observed: { observation: "obs_b", at: "2026-10-26T09:46:00Z", release: "rel_next" }, openDrift: { revision: 2, observation: "obs_b", lastObservation: "obs_b", lateLanding: null } });
    expect(badges(m)).toEqual(["Drifted"]);
    expect(observed(m, NOW)).toBe("= desired, 2 d ago");
    // A read that matched neither, without drift open, is not drift and not a match.
    expect(badges(machine({ observed: { observation: "obs_c", at: "2026-10-26T09:46:00Z", release: null } }))).toEqual([]);
  });

  test("observed names what the read matched, or none", () => {
    expect(observed(machine({}), NOW)).toBe("= applied, 2 d ago");
    expect(observed(machine({ observed: { observation: "obs_c", at: "2026-10-26T09:46:00Z", release: null } }), NOW)).toBe("no match, 2 d ago");
    expect(observed(machine({ observed: null }), NOW)).toBe("none");
  });

  test("observed shows the reading's age, so readings at the same time on different days differ", () => {
    const read = (at: string) => observed(machine({ observed: { observation: "obs_d", at, release: "rel_new" } }), NOW);
    expect(read("2026-10-28T09:46:00Z")).toBe("= applied, just now");
    expect(read("2026-10-28T09:44:00Z")).toBe("= applied, 2 min ago");
    expect(read("2026-10-28T06:40:00Z")).toBe("= applied, 3 h ago");
    expect(read("2026-10-21T09:46:00Z")).toBe("= applied, 7 d ago");
  });

  test("a reading ahead of the browser's clock never looks fresh, and an unparseable time has no age", () => {
    const read = (at: string) => observed(machine({ observed: { observation: "obs_e", at, release: "rel_new" } }), NOW);
    expect(read("2026-10-28T09:47:00Z")).toBe("= applied, just now");
    expect(read("2026-10-29T09:46:00Z")).toBe("= applied, ahead of this browser's clock");
    expect(read("")).toBe("= applied, age unknown");
    expect(read("not a time")).toBe("= applied, age unknown");
  });
});
