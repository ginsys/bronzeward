// @vitest-environment happy-dom
// The cluster page, the Clusters and Machines lists and the operation page as the console layout design draws them
// (docs/design/console-layout.md §3.2 and §4 group A, ginsys/bronzeward#1257): a page header with the page's one filled button, the note
// as one visible line with the whole note behind "What is this page?", and every table in a panel. Each case reads the markup a
// person's Tab order and the browser cases meet; the words are the console's own and are asserted only where the layout moves them.

import { act, createElement as h, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { firstSentence } from "./layout.tsx";
import { type Reads, ReadsProvider } from "./list.tsx";
import { ClusterItem, Clusters, Machines } from "./machines.tsx";
import { OperationItem } from "./resources.tsx";
import { type Role, ShellContext } from "./shell.tsx";
import { ABOUT_PAGE, CLUSTER_NOTE, CLUSTERS_NOTE, MACHINES_NOTE, OPERATION_NOTE } from "./words.ts";
import { WritesProvider } from "./forms.tsx";
import { Writes } from "./write.ts";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const CL = "cl_a";
const MCH = "mch_b";
const DRF = "drf_k2pq3rstuvwxyz234567abcdef";
const OP = "op_k2pq3rstuvwxyz234567abcdef";
const cluster = { id: CL, name: "office", endpoint: "https://10.5.0.1:6443", contract: "v1.14", talosClusterId: "tcid-office" };
const machine = {
  id: MCH, cluster: CL, name: null, hardware: { smbiosUuid: null, talosNodeId: "node-a", serial: null }, platform: "metal", talosEndpoint: "10.5.0.2:50000",
  desired: null, applied: null, pendingConvergence: false, observed: null, frozen: false, scopeState: "normal", openDrift: null,
};
const draft = { id: DRF, title: "onboard office", state: "open", revision: 3, cluster: CL, entries: [] };
const operation = {
  id: OP, kind: "apply-config", state: "running", subject: { machine: MCH }, createdAt: "2026-11-21T14:02:00Z", createdBy: null, epoch: "e0", lastEvent: null,
  result: null, error: null, attempts: [],
};

let root: Root;
let box: HTMLElement;
let served: Record<string, unknown>;
let held = false;
const reads = (): Reads => ({
  client: {
    read: async (path: string) => {
      if (held) return new Promise(() => {});
      const bare = path.split("?")[0]!;
      if (path in served) return { kind: "ok", data: served[path] };
      if (bare in served) return { kind: "ok", data: served[bare] };
      if (["/machines", "/clusters", "/drafts", "/plans", "/operations"].includes(bare) || bare.endsWith("/events")) return { kind: "ok", data: { items: [], next: undefined } };
      return { kind: "refused", status: 404, problem: null };
    },
  },
  settle: (res: unknown, set: (r: unknown) => void, live: () => boolean) => {
    if (live()) set(res);
    return false;
  },
  undropped: () => () => true,
  view: "v", again: 0, regained: 0, go: () => {}, failure: () => "FAILED",
}) as unknown as Reads;
const shown = async (page: ReactNode, roles: Role[] = ["author", "publisher", "approver"]) => {
  await act(async () => void root.render(h(WritesProvider, { value: new Writes(() => "p") }, h(ShellContext, { value: { roles, offline: false } }, h(ReadsProvider, { value: reads() }, page)))));
  await act(async () => void (await new Promise((r) => setTimeout(r, 30))));
};
const text = (e: Element | null | undefined) => e?.textContent?.trim() ?? "";
const parts = () => [...box.children].map((c) => (c.classList.contains("page-head") ? "page-head" : c.classList.contains("lede") ? "lede" : c.classList.contains("about") ? "about" : c.classList.contains("panel") ? "panel" : c.tagName.toLowerCase()));
const panel = (label: string) => box.querySelector(`section.panel[aria-label="${label}"]`)!;
const buttons = (scope: ParentNode) => [...scope.querySelectorAll("button, a.button")];
const filled = (scope: ParentNode) => buttons(scope).filter((b) => !b.classList.contains("outline")).map(text);
const before = (a: Element, b: Element) => expect(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING, `${text(a).slice(0, 30)} before ${text(b).slice(0, 30)}`).toBeTruthy();

beforeEach(() => {
  box = document.createElement("div");
  document.body.append(box);
  root = createRoot(box);
  served = { [`/clusters/${CL}`]: cluster, "/clusters": { items: [cluster], next: undefined }, "/machines": { items: [machine], next: undefined }, "/drafts": { items: [draft], next: undefined } };
  held = false;
});
afterEach(async () => {
  await act(async () => root.unmount());
  box.remove();
});

describe("the cluster page", () => {
  test("its top is the page header, one line and the disclosure, then three panels, and no loose paragraph above the first panel", async () => {
    await shown(h(ClusterItem, { id: CL }));
    expect(parts()).toEqual(["page-head", "lede", "about", "panel", "panel", "panel"]);
    expect([...box.querySelectorAll("section.panel")].map((p) => p.getAttribute("aria-label"))).toEqual(["Details", "Machines", "Open drafts"]);
    expect(box.querySelectorAll("h2")).toHaveLength(1);
    expect(text(box.querySelector(".page-head h2"))).toBe("Cluster office");
  });
  test("the header keeps the identifier line (Copy as a secondary button), and the page has no filled button", async () => {
    await shown(h(ClusterItem, { id: CL }));
    const head = box.querySelector(".page-head .titles")!;
    expect(text(head.querySelector("small"))).toContain(CL);
    expect(head.querySelector("small button")!.className).toBe("outline");
    expect(filled(box)).toEqual([]);
  });
  test("Details is a two-column grid of the endpoint, the contract and the cluster ID, with the Talos access sentences for an author after it", async () => {
    await shown(h(ClusterItem, { id: CL }));
    const details = panel("Details");
    expect([...details.querySelectorAll(":scope > dl > dt")].map(text)).toEqual(["Kubernetes endpoint", "Contract", "Talos cluster ID"]);
    expect([...details.querySelectorAll(":scope > dl > dd")].map(text)).toEqual([cluster.endpoint, cluster.contract, cluster.talosClusterId]);
    const sign = details.querySelector('[data-testid="talos-access"]')!;
    expect(sign).not.toBeNull();
    before(details.querySelector("dl")!, sign);
    expect(text(sign)).not.toContain("Talos access:");
  });
  test("a viewer has no Talos access sentences, and the Details grid is the same", async () => {
    await shown(h(ClusterItem, { id: CL }), ["viewer"]);
    expect(box.querySelector('[data-testid="talos-access"]')).toBeNull();
    expect(panel("Details").querySelectorAll("dt")).toHaveLength(3);
  });
  test("Machines in this cluster is a link in the Machines panel's band, with the same path and title; the table keeps its words", async () => {
    await shown(h(ClusterItem, { id: CL }));
    const band = panel("Machines").querySelector(":scope > .panel-head")!;
    const link = [...band.querySelectorAll(".actions a")].find((a) => text(a) === "Machines in this cluster")!;
    expect(link.getAttribute("href")).toBe(`/machines?cluster=${CL}`);
    expect(link.getAttribute("title")).toBe(CL);
    expect([...box.querySelectorAll("a")].filter((a) => text(a) === "Machines in this cluster")).toHaveLength(1);
    expect(text(panel("Machines"))).toContain("1 machine:");
    expect(panel("Machines").querySelector('[aria-label="Machines of this cluster"] table')).not.toBeNull();
    expect(panel("Open drafts").querySelector('[aria-label="Open drafts of this cluster"] table')).not.toBeNull();
  });
  test("the panels hold the pending and empty states too", async () => {
    served["/machines"] = { items: [], next: undefined };
    served["/drafts"] = { items: [], next: undefined };
    await shown(h(ClusterItem, { id: CL }));
    expect(text(panel("Machines"))).toContain("No machines recorded in this cluster.");
    expect(text(panel("Open drafts"))).toContain("No open draft in this cluster.");
  });
  test("the line is the first sentence of the note, the whole note is behind What is this page?", async () => {
    await shown(h(ClusterItem, { id: CL }));
    expect(text(box.querySelector('[data-testid="page-note-line"]'))).toBe(firstSentence(CLUSTER_NOTE));
    expect(CLUSTER_NOTE).toContain(firstSentence(CLUSTER_NOTE));
    expect(text(box.querySelector("details.about > summary"))).toBe(ABOUT_PAGE);
    expect(text(box.querySelector('details.about [data-testid="page-note"]'))).toBe(CLUSTER_NOTE);
  });
});

describe("the Machines list", () => {
  test("a page header with its three actions, one filled; the note as a line; then one panel", async () => {
    await shown(h(Machines, { query: "", go: () => {} }));
    expect(parts()).toEqual(["page-head", "lede", "about", "panel"]);
    const actions = box.querySelector(".page-head .actions")!;
    expect(buttons(actions).map(text)).toEqual(["Add machine", "Enter a cluster by hand", "Connect a cluster"]);
    expect(filled(box)).toEqual(["Add machine"]);
    expect(text(box.querySelector(".page-head h2"))).toBe("Machines");
  });
  test("the filters stay a toolbar row, the first thing under the panel's band, then the table, the count and the health line", async () => {
    await shown(h(Machines, { query: "", go: () => {} }));
    const p = panel("The machine list");
    const kids = [...p.children].map((c) => (c.classList.contains("panel-head") ? "band" : c.classList.contains("toolbar") ? "toolbar" : c.classList.contains("scroll") ? "scroll" : c.getAttribute("data-testid") ?? c.tagName.toLowerCase()));
    expect(kids.slice(0, 3)).toEqual(["band", "toolbar", "scroll"]);
    expect([...p.querySelectorAll(".toolbar label")].map((l) => text(l).split(":")[0])).toEqual(["Cluster", "State"]);
    expect(p.querySelectorAll(".toolbar button")).toHaveLength(0);
    expect(text(p.querySelector('[data-testid="count"]'))).toBe("Showing 1 of 1.");
    expect(text(p.lastElementChild)).toBe("Health is not checked.");
  });
  test("an empty list says so inside the panel, as a status line", async () => {
    served["/machines"] = { items: [], next: undefined };
    await shown(h(Machines, { query: "", go: () => {} }));
    expect(text(panel("The machine list").querySelector('[data-testid="status"]'))).toBe("No machines recorded yet.");
  });
  test("the actions come before the note's disclosure, and the filters after it", async () => {
    await shown(h(Machines, { query: "", go: () => {} }));
    before(box.querySelector(".page-head .actions button")!, box.querySelector("details.about > summary")!);
    before(box.querySelector("details.about > summary")!, box.querySelector(".toolbar select")!);
  });
  test("the line is the first sentence of the note", async () => {
    await shown(h(Machines, { query: "", go: () => {} }));
    expect(text(box.querySelector('[data-testid="page-note-line"]'))).toBe(firstSentence(MACHINES_NOTE));
    expect(text(box.querySelector('details.about [data-testid="page-note"]'))).toBe(MACHINES_NOTE);
  });
  test("a viewer's actions are off with their reasons beside them", async () => {
    await shown(h(Machines, { query: "", go: () => {} }), ["viewer"]);
    const add = [...box.querySelectorAll("button")].find((b) => text(b) === "Add machine")!;
    expect(add.hasAttribute("disabled")).toBe(true);
    expect(text(add.parentElement)).toContain("Needs author");
  });
});

describe("a list with a next page keeps its page's one filled button", () => {
  // Show more is a button of the page too: where it is drawn it is the outline, so the header's action stays the only filled one.
  test("Machines: Add machine, with Show more outlined", async () => {
    served["/machines"] = { items: [machine], next: "c1" };
    await shown(h(Machines, { query: "", go: () => {} }));
    const more = [...box.querySelectorAll("button")].find((b) => text(b) === "Show more")!;
    expect(more).toBeDefined();
    expect(more.className).toBe("outline");
    expect(filled(box)).toEqual(["Add machine"]);
  });
  test("Clusters: Connect a cluster, with Show more outlined", async () => {
    served["/clusters"] = { items: [cluster], next: "c1" };
    await shown(h(Clusters, {}));
    const more = [...box.querySelectorAll("button")].find((b) => text(b) === "Show more")!;
    expect(more).toBeDefined();
    expect(more.className).toBe("outline");
    expect(filled(box)).toEqual(["Connect a cluster"]);
  });
});

describe("the Clusters list", () => {
  test("a page header with its two actions, Connect a cluster the filled one; then one panel with the table", async () => {
    await shown(h(Clusters, {}));
    expect(parts()).toEqual(["page-head", "lede", "about", "panel"]);
    expect(buttons(box.querySelector(".page-head .actions")!).map(text)).toEqual(["Enter a cluster by hand", "Connect a cluster"]);
    expect(filled(box)).toEqual(["Connect a cluster"]);
    const p = panel("The cluster list");
    expect(p.querySelector(':scope > .scroll[aria-label="Clusters"] table')).not.toBeNull();
    expect(p.querySelector("a[title='cl_a']")!.getAttribute("href")).toBe("/clusters/cl_a");
  });
  test("the line is the first sentence of the note", async () => {
    await shown(h(Clusters, {}));
    expect(text(box.querySelector('[data-testid="page-note-line"]'))).toBe(firstSentence(CLUSTERS_NOTE));
    expect(text(box.querySelector('details.about [data-testid="page-note"]'))).toBe(CLUSTERS_NOTE);
  });
  test("an empty list says so inside the panel", async () => {
    served["/clusters"] = { items: [], next: undefined };
    await shown(h(Clusters, {}));
    expect(text(panel("The cluster list").querySelector('[data-testid="status"]'))).toBe("No clusters recorded yet.");
  });
});

describe("the operation page", () => {
  test("a page header with the title, the note as the page note (one sentence, so no disclosure), and its panels and loose parts as before", async () => {
    served[`/operations/${OP}`] = operation;
    await shown(h(OperationItem, { id: OP }));
    expect(box.children[0]!.classList.contains("page-head")).toBe(true);
    expect(box.querySelectorAll("h2")).toHaveLength(1);
    expect(box.querySelector(".page-head h2")).not.toBeNull();
    expect(box.querySelector("details.about")).toBeNull();
    expect(box.querySelector('[data-testid="page-note-line"]')).toBeNull();
    expect(text(box.querySelector('[data-testid="page-note"]'))).toBe(OPERATION_NOTE);
    expect(box.children[1]!.getAttribute("data-testid")).toBe("page-note");
    expect([...box.querySelectorAll(".panel > h3")].map(text)).toEqual(["Timeline"]);
    expect(box.querySelector(":scope > dl")).not.toBeNull();
    expect([...box.querySelectorAll(":scope > h3")].map(text)).toEqual(["Attempts"]);
  });
});

describe("no table of these pages carries a hidden header word", () => {
  // A hidden word in a header cell is absolutely positioned, so with no positioned ancestor it sits at the right end of a
  // wide table, outside the scroll region's clip, and scrolls the page sideways (the draft page's Entries table, at 320 px).
  // None of these tables has an action column, so none has one.
  test("the cluster page, the lists and the operation page", async () => {
    served[`/operations/${OP}`] = { ...operation, attempts: [{ id: "att_a", number: 1, at: "2026-11-21T14:02:00Z", route: "talos", controller: "c", generation: 1, transportDeadline: "2026-11-21T14:03:00Z", verificationDeadline: "2026-11-21T14:03:00Z", response: null, accounting: null }] };
    for (const page of [h(ClusterItem, { id: CL }), h(Machines, { query: "", go: () => {} }), h(Clusters, {}), h(OperationItem, { id: OP })]) {
      await shown(page);
      expect(box.querySelectorAll("table").length).toBeGreaterThan(0);
      expect(box.querySelectorAll("table .sr-only, th.action, td.action")).toHaveLength(0);
    }
  });
});
