// The Clusters navigation entry and the cluster page's two sections (ginsys/bronzeward#859): the machines
// of a cluster by state, and its open drafts, filtered in the browser because GET /drafts has no filter.

import { createElement as h, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import type { components } from "./api/v1";
import { ReadsProvider, RESTARTED, type Reads } from "./list.tsx";
import { machineStates, MachineSummary, type Machine, OpenDraftsView, openDrafts, Restarted } from "./machines.tsx";
import { PAGES } from "./router.ts";

type Draft = components["schemas"]["DraftBody"];

const go = () => {};
const reads: Reads = { client: { read: () => new Promise(() => {}) }, settle: () => false, undropped: () => () => true, view: "", again: 0, regained: 0, go, failure: () => null };
const html = (e: ReactNode) => renderToStaticMarkup(h(ReadsProvider, { value: reads }, e));

const machine = (over: Partial<Machine>): Machine => ({
  id: "mch_x", cluster: "cl_a", talosEndpoint: "10.0.0.1:50000", hardware: { talosNodeId: "n1", smbiosUuid: null }, platform: "metal",
  openDrift: null, frozen: false, pendingConvergence: false, desired: null, applied: null, observed: null, ...over,
}) as Machine;
const draft = (over: Partial<Draft>): Draft => ({ id: "drf_x", cluster: "cl_a", title: "t", state: "open", revision: 1, entries: [], ...over });

// One machine with three states, one that matches, one that shows none.
const drifted = machine({ id: "mch_a", openDrift: { revision: 1, observation: "obs_1", lastObservation: "obs_1", lateLanding: null }, frozen: true });
const matches = machine({ id: "mch_b", desired: "rel_1", applied: { release: "rel_1", source: "operation" }, observed: { observation: "obs_2", release: "rel_1", at: "2026-11-18T10:00:00Z" } });
const plain = machine({ id: "mch_c", desired: "rel_1", applied: { release: "rel_1", source: "operation" } });

describe("the Clusters entry in the main navigation", () => {
  test("it follows Machines and goes to the cluster list", () => {
    expect(PAGES.slice(0, 2)).toEqual([["/machines", "Machines"], ["/clusters", "Clusters"]]);
    expect(PAGES.map(([, title]) => title)).toEqual(["Machines", "Clusters", "Changes", "Plans", "Operations", "Sources", "Audit", "Recovery"]);
  });
});

describe("a cluster's machines by state", () => {
  test("counts each badge, how many show none, and how many machines there are", () => {
    expect(machineStates([drifted, matches, plain])).toEqual({
      total: 3,
      counts: [["Drifted", 1], ["Frozen", 1], ["Matches applied", 1], ["Not handed over", 1]],
      none: 1,
    });
    expect(machineStates([])).toEqual({ total: 0, counts: [], none: 0 });
  });

  test("the summary says the states overlap, lists each machine with its states, and says plainly when there are none", () => {
    const out = html(h(MachineSummary, { machines: [drifted, matches, plain] }));
    expect(out).toContain("3 machines");
    expect(out).toContain("1 Drifted");
    expect(out).toContain("1 with no state shown");
    expect(out).toContain("A machine can carry several states");
    expect(out).toContain('href="/machines/mch_a"');
    expect(out).toContain("Drifted, Frozen, Not handed over");
    expect(html(h(MachineSummary, { machines: [] }))).toContain("No machines recorded in this cluster.");
  });
});

describe("a list read again from its start", () => {
  test("says so on either section, and says nothing otherwise", () => {
    const list = (restarted: boolean) => ({ items: [], next: undefined, restarted });
    expect(html(h(Restarted, { list: list(true) }))).toContain(RESTARTED.slice(RESTARTED.indexOf("so it was")));
    expect(html(h(Restarted, { list: list(false) }))).toBe("");
  });
});

describe("a cluster's open drafts", () => {
  test("keeps the open drafts of this cluster, and drops published and discarded ones and other clusters' drafts", () => {
    const all = [
      draft({ id: "drf_1" }), draft({ id: "drf_2", state: "published" }), draft({ id: "drf_3", state: "discarded" }),
      draft({ id: "drf_4", cluster: "cl_b" }), draft({ id: "drf_5", title: "second" }),
    ];
    expect(openDrafts(all, "cl_a").map((d) => d.id)).toEqual(["drf_1", "drf_5"]);
    expect(openDrafts(all, "cl_c")).toEqual([]);
  });

  test("the open drafts are listed with a link, a title and a revision", () => {
    const out = html(h(OpenDraftsView, { drafts: [draft({ id: "drf_1", title: "rotate keys", revision: 3 })] }));
    expect(out).toContain('href="/drafts/drf_1"');
    expect(out).toContain("rotate keys");
    expect(out).not.toContain("No open draft");
  });

  test("no open draft is said plainly", () => {
    expect(html(h(OpenDraftsView, { drafts: [] }))).toContain("No open draft in this cluster.");
  });
});
