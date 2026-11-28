// @vitest-environment happy-dom
// The six list pages as the console layout design draws them (docs/design/console-layout.md §4 group B, ginsys/bronzeward#1259): Drafts and
// releases, Plans, Operations, Imports, Fragments and profiles and Audit. Each has a page header (with its one filled button where it has
// one), the note as one visible line with the whole note behind "What is this page?", and every table in a panel with the filters as a
// toolbar row under the band. Each case reads the markup a person's Tab order and the browser cases meet; the words are the console's own.

import { act, createElement as h, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { firstSentence } from "./layout.tsx";
import { type Reads, ReadsProvider } from "./list.tsx";
import { Audit, Changes, Imports, Operations, Plans, Sources } from "./resources.tsx";
import { type Role, ShellContext } from "./shell.tsx";
import { ABOUT_PAGE, AUDIT_NOTE, CHANGES_NOTE, IMPORTS_NOTE, OPEN_A_MACHINE, OPERATIONS_NOTE, PLANS_NOTE, PLANS_NOTE_END, SOURCES_NOTE } from "./words.ts";
import { WritesProvider } from "./forms.tsx";
import { Writes } from "./write.ts";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const CL = "cl_a";
const MCH = "mch_b";
const DRF = "drf_k2pq3rstuvwxyz234567abcdef";
const OP = "op_k2pq3rstuvwxyz234567abcdef";
const PLN = "pln_k2pq3rstuvwxyz234567abcdef";
const REL = "rel_k2pq3rstuvwxyz234567abcdef";
const AT = "2026-11-21T14:02:00Z";
const draft = { id: DRF, title: "onboard office", state: "open", revision: 3, cluster: CL, entries: [] };
const operation = { id: OP, kind: "apply-config", state: "running", subject: { machine: MCH }, createdAt: AT, createdBy: null, epoch: "e0", lastEvent: null, result: null, error: null, attempts: [] };
const plan = { id: PLN, state: "proposed", cluster: CL, machine: MCH, release: REL, operation: "apply-config", mode: null, createdAt: AT, expiresAt: AT };
const ingestion = { id: "ing_a", kind: "import", mode: "encrypted", state: "held", machine: MCH, draft: DRF, operation: null, review: null, ownerGeneration: 1, leaseUntil: AT, expiresAt: AT, createdAt: AT };
const fragment = { id: "frg_a", name: "base", cluster: CL, scope: "cluster", layer: "base", revision: null, headRevision: 0, createdAt: AT };
const profile = { id: "prf_a", name: "workers", cluster: CL, scope: "cluster", layer: "base", revision: null, headRevision: 0, createdAt: AT };
const assignment = { id: "asg_a", cluster: CL, machine: MCH, revision: null, headRevision: 0, createdAt: AT };
const acted = { id: "act_a", at: AT, principal: "p0", principalKind: "human", via: "api", role: null, action: "draft.create", subjects: [], requestId: null };
const lists = ["/drafts", "/releases", "/plans", "/operations", "/ingestions", "/acts", "/fragments", "/profiles", "/assignments", "/dependencies", "/dependency-alerts", "/machines", "/clusters"];

let root: Root;
let box: HTMLElement;
let served: Record<string, unknown>;
let went: string[];
const reads = (): Reads => ({
  client: {
    read: async (path: string) => {
      const bare = path.split("?")[0]!;
      if (path in served) return { kind: "ok", data: served[path] };
      if (bare in served) return { kind: "ok", data: served[bare] };
      if (lists.includes(bare)) return { kind: "ok", data: { items: [], next: undefined } };
      return { kind: "refused", status: 404, problem: null };
    },
  },
  settle: (res: unknown, set: (r: unknown) => void, live: () => boolean) => {
    if (live()) set(res);
    return false;
  },
  undropped: () => () => true,
  view: "v", again: 0, regained: 0, go: (to: string) => void went.push(to), failure: () => "FAILED",
}) as unknown as Reads;
const shown = async (page: ReactNode, roles: Role[] = ["author", "publisher", "approver"]) => {
  await act(async () => void root.render(h(WritesProvider, { value: new Writes(() => "p") }, h(ShellContext, { value: { roles, offline: false } }, h(ReadsProvider, { value: reads() }, page)))));
  await act(async () => void (await new Promise((r) => setTimeout(r, 30))));
};
const go = (to: string) => void went.push(to);
const text = (e: Element | null | undefined) => e?.textContent?.trim() ?? "";
const parts = () => [...box.children].map((c) => (c.classList.contains("page-head") ? "page-head" : c.classList.contains("lede") ? "lede" : c.classList.contains("about") ? "about" : c.classList.contains("panel") ? "panel" : c.tagName.toLowerCase()));
const panel = (label: string) => box.querySelector(`section.panel[aria-label="${label}"]`)!;
const buttons = (scope: ParentNode) => [...scope.querySelectorAll("button, a.button")];
const filled = (scope: ParentNode) => buttons(scope).filter((b) => !b.classList.contains("outline")).map(text);
const before = (a: Element, b: Element) => expect(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING, `${text(a).slice(0, 30)} before ${text(b).slice(0, 30)}`).toBeTruthy();
const kids = (p: Element) => [...p.children].map((c) => (c.classList.contains("panel-head") ? "band" : c.classList.contains("toolbar") ? "toolbar" : c.classList.contains("scroll") ? "scroll" : c.getAttribute("data-testid") ?? c.tagName.toLowerCase()));

beforeEach(() => {
  box = document.createElement("div");
  document.body.append(box);
  root = createRoot(box);
  served = {};
  went = [];
});
afterEach(async () => {
  await act(async () => root.unmount());
  box.remove();
});

// Each page: its component, title, note, the label of the panel that holds its table, the panel's first toolbar filters and the page's filled button.
const pages = [
  { name: "Drafts and releases", page: () => h(Changes, {}), title: "Drafts and releases", note: CHANGES_NOTE, panels: ["The draft list", "The release list"], filters: ["Cluster"], filled: ["Create draft"] },
  { name: "Plans", page: () => h(Plans, { query: "", go }), title: "Plans", note: PLANS_NOTE, panels: ["The plan list"], filters: ["State"], filled: [] },
  { name: "Operations", page: () => h(Operations, { query: "", go }), title: "Operations", note: OPERATIONS_NOTE, panels: ["The operation list"], filters: ["Kind", "State"], filled: [] },
  { name: "Imports", page: () => h(Imports, { query: "", go }), title: "Imports", note: IMPORTS_NOTE, panels: ["The import list"], filters: ["State"], filled: ["Start import"] },
  { name: "Fragments and profiles", page: () => h(Sources, { query: "", go }), title: "Fragments and profiles", note: SOURCES_NOTE, panels: ["The fragment list", "The profile list", "The assignment list", "The dependency list", "The dependency alert list"], filters: ["Class"], filled: [] },
  { name: "Audit", page: () => h(Audit, {}), title: "Audit", note: AUDIT_NOTE, panels: ["The act list"], filters: [], filled: [] },
] as const;
// The note as the whole page note reads: Plans' carries its link between its two parts.
const wholeNote = (p: (typeof pages)[number]) => (p.name === "Plans" ? `${PLANS_NOTE} ${OPEN_A_MACHINE} ${PLANS_NOTE_END}` : p.note);

describe.each(pages)("$name", (p) => {
  test("a page header with the title, the note as one line and a disclosure, then its panels", async () => {
    await shown(p.page());
    expect(parts().slice(0, 3)).toEqual(["page-head", "lede", "about"]);
    expect(box.querySelectorAll("h2")).toHaveLength(1);
    expect(text(box.querySelector(".page-head h2"))).toBe(p.title);
    expect([...box.querySelectorAll(":scope > section.panel")].map((s) => s.getAttribute("aria-label"))).toEqual(p.panels);
  });
  test("the line is the first sentence of the note, the whole note is behind What is this page?", async () => {
    await shown(p.page());
    expect(text(box.querySelector('[data-testid="page-note-line"]'))).toBe(firstSentence(p.note));
    expect(text(box.querySelector("details.about > summary"))).toBe(ABOUT_PAGE);
    expect(text(box.querySelector('details.about [data-testid="page-note"]'))).toBe(wholeNote(p));
  });
  test("the page's filled buttons are exactly those of the plan", async () => {
    await shown(p.page());
    expect(filled(box)).toEqual(p.filled);
    if (p.filled.length > 0) expect(buttons(box.querySelector(".page-head .actions")!).map(text)).toEqual(p.filled);
  });
  test("the actions come before the note's disclosure, and the filters after it", async () => {
    await shown(p.page());
    const action = box.querySelector(".page-head .actions button");
    if (action !== null) before(action, box.querySelector("details.about > summary")!);
    const select = box.querySelector(".toolbar select");
    if (select !== null) before(box.querySelector("details.about > summary")!, select);
  });
  test("the first panel's filters are a toolbar row, the first thing under its band", async () => {
    await shown(p.page());
    const first = panel(p.panels[0]!);
    if (p.name !== "Fragments and profiles" && p.name !== "Audit") {
      expect(kids(first).slice(0, 2)).toEqual(["band", "toolbar"]);
      expect([...first.querySelectorAll(".toolbar label")].map((l) => text(l).split(":")[0])).toEqual(p.filters);
    }
    expect(box.querySelectorAll(":scope > .toolbar, :scope > select, :scope > label")).toHaveLength(0);
  });
});

describe("empty lists say so inside their panel, as a status line", () => {
  test.each([
    ["The draft list", () => h(Changes, {}), "No drafts."],
    ["The release list", () => h(Changes, {}), "No releases published yet."],
    ["The plan list", () => h(Plans, { query: "", go }), "No plans created yet."],
    ["The operation list", () => h(Operations, { query: "", go }), "No operations recorded yet."],
    ["The import list", () => h(Imports, { query: "", go }), "No imports recorded yet."],
    ["The assignment list", () => h(Sources, { query: "", go }), "No assignments recorded yet."],
    ["The dependency alert list", () => h(Sources, { query: "", go }), "No dependency alerts raised."],
    ["The act list", () => h(Audit, {}), "No acts recorded yet."],
  ] as const)("%s", async (label, page, empty) => {
    await shown(page());
    expect(text(panel(label).querySelector('[data-testid="status"]'))).toBe(empty);
  });
});

describe("Drafts and releases", () => {
  test("Create draft is in the page header; the Cluster filter and the table are in the Drafts panel", async () => {
    served["/drafts"] = { items: [draft], next: undefined };
    await shown(h(Changes, {}));
    expect(text(box.querySelector(".page-head .actions button"))).toBe("Create draft");
    expect([...panel("The draft list").querySelectorAll("button")]).toHaveLength(0);
    expect(panel("The draft list").querySelector('.scroll[aria-label="Drafts"] table')).not.toBeNull();
    before(box.querySelector(".page-head .actions button")!, box.querySelector(".toolbar select")!);
  });
  test("the Imports link stays a plain line between the two panels, as the note says", async () => {
    await shown(h(Changes, {}));
    expect(parts()).toEqual(["page-head", "lede", "about", "panel", "p", "panel"]);
    const line = box.children[4]!;
    expect(text(line)).toBe("Imports, of every machine.");
    expect(line.querySelector("a")!.getAttribute("href")).toBe("/ingestions");
  });
  test("a viewer's Create draft is off with its reason beside it", async () => {
    await shown(h(Changes, {}), ["viewer"]);
    const create = [...box.querySelectorAll("button")].find((b) => text(b) === "Create draft")!;
    expect(create.hasAttribute("disabled")).toBe(true);
    expect(text(create.parentElement)).toContain("Needs author");
  });
  test("Create draft goes where it went", async () => {
    await shown(h(Changes, {}));
    await act(async () => void [...box.querySelectorAll("button")].find((b) => text(b) === "Create draft")!.click());
    expect(went).toEqual(["/drafts/new"]);
  });
  test("a releases list with a next page keeps Create draft the one filled button", async () => {
    served["/releases"] = { items: [], next: "c1" };
    await shown(h(Changes, {}));
    const more = [...box.querySelectorAll("button")].find((b) => text(b) === "Show more")!;
    expect(more.className).toBe("outline");
    expect(filled(box)).toEqual(["Create draft"]);
  });
});

describe("Imports", () => {
  test("a list with a next page keeps Start import the one filled button", async () => {
    served["/ingestions"] = { items: [ingestion], next: "c1" };
    await shown(h(Imports, { query: "", go }));
    const more = [...box.querySelectorAll("button")].find((b) => text(b) === "Show more")!;
    expect(more.className).toBe("outline");
    expect(filled(box)).toEqual(["Start import"]);
  });
  test("Start import keeps its machine filter in the path", async () => {
    await shown(h(Imports, { query: `machine=${MCH}`, go }));
    await act(async () => void [...box.querySelectorAll("button")].find((b) => text(b) === "Start import")!.click());
    expect(went).toEqual([`/ingestions/new?machine=${MCH}`]);
  });
});

describe("a filter set by a link from another view stays the first thing in the toolbar, and Show all still moves focus to the next filter", () => {
  test("Plans: Machine, then State", async () => {
    // The focus move looks for the page's own controls, so the page is drawn inside the shell's main element.
    await shown(h("main", null, h(Plans, { query: `machine=${MCH}`, go })));
    const bar = panel("The plan list").querySelector(".toolbar")!;
    expect(text(bar.firstElementChild)).toContain("Show all");
    expect(bar.querySelectorAll("select")).toHaveLength(1);
    await act(async () => void [...bar.querySelectorAll("button")].find((b) => text(b) === "Show all")!.click());
    expect(went).toEqual(["/plans"]);
    expect(document.activeElement).toBe(bar.querySelector("select"));
  });
  test("Operations: Machine, Plan, then Kind and State", async () => {
    await shown(h(Operations, { query: `plan=${PLN}&machine=${MCH}`, go }));
    const bar = panel("The operation list").querySelector(".toolbar")!;
    expect([...bar.children].map((c) => c.tagName.toLowerCase() + (text(c).includes("Show all") ? ":pinned" : ""))).toEqual(["p:pinned", "p:pinned", "label", "label"]);
  });
});

describe("Fragments and profiles keeps its sections as five panels and its Class filter and last-pass line in the dependencies panel", () => {
  test("the panel titles are the old headings, in the old order", async () => {
    await shown(h(Sources, { query: "", go }));
    expect([...box.querySelectorAll(":scope > .panel > .panel-head > h3")].map(text)).toEqual(["Fragments", "Profiles", "Assignments", "Dependencies", "Dependency alerts"]);
    expect(box.querySelectorAll(":scope > h3")).toHaveLength(0);
  });
  test("the Class filter is the toolbar of the dependencies panel, the last-pass line follows it, then the table or its status", async () => {
    served["/dependencies"] = { items: [], next: undefined, lastPass: null };
    await shown(h(Sources, { query: "", go }));
    const p = panel("The dependency list");
    expect(kids(p).slice(0, 3)).toEqual(["band", "toolbar", "last-pass"]);
    expect([...p.querySelectorAll(".toolbar label")].map((l) => text(l).split(":")[0])).toEqual(["Class"]);
  });
  test("each table region keeps its own label, distinct from its panel's", async () => {
    served["/fragments"] = { items: [fragment], next: undefined };
    await shown(h(Sources, { query: "", go }));
    expect(panel("The fragment list").querySelector('.scroll[aria-label="Fragments"] table')).not.toBeNull();
    expect(box.querySelectorAll('[aria-label="Fragments"]')).toHaveLength(1);
  });
});

describe("no table of these pages carries a hidden header word", () => {
  // A hidden word in a header cell is absolutely positioned, so with no positioned ancestor it sits at the right end of a wide table, outside
  // the scroll region's clip, and scrolls the page sideways (the draft page's Entries table, at 320 px). None of these tables has an action
  // column, so none has one.
  test("every one of the six, with a row in each table", async () => {
    Object.assign(served, {
      "/drafts": { items: [draft], next: undefined }, "/plans": { items: [plan], next: undefined }, "/operations": { items: [operation], next: undefined },
      "/ingestions": { items: [ingestion], next: undefined }, "/acts": { items: [acted], next: undefined }, "/fragments": { items: [fragment], next: undefined },
      "/profiles": { items: [profile], next: undefined }, "/assignments": { items: [assignment], next: undefined },
    });
    for (const p of pages) {
      await shown(p.page()).catch((e: unknown) => {
        throw new Error(`${p.name}: ${String(e)}`);
      });
      expect(box.querySelectorAll("table").length, p.name).toBeGreaterThan(0);
      expect(box.querySelectorAll("table .sr-only, th.action, td.action"), p.name).toHaveLength(0);
    }
  });
});
