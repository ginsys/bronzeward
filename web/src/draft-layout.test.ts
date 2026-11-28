// @vitest-environment happy-dom
// The draft page as the console layout design draws it (docs/design/console-layout.md §3.1, ginsys/bronzeward#1255): a page
// header with the one filled button, Entries as one panel with a table, Shared names, Adopt these machines, the rendered
// configuration and Discard this draft as panels in that order. Each case reads the markup a person's Tab order and the browser
// cases meet; the words are the console's own and are asserted only where the layout moves them.

import { act, createElement as h, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { DraftItem } from "./drafts.tsx";
import { WritesProvider } from "./forms.tsx";
import { type Reads, ReadsProvider } from "./list.tsx";
import { type Role, ShellContext } from "./shell.tsx";
import { DISCARD_SENTENCE, DRAFT_NOTE, DRAFT_NOTE_OPEN, RENDERED_TITLE } from "./words.ts";
import { Writes } from "./write.ts";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const DRF = "drf_k2pq3rstuvwxyz234567abcdef";
const MCH = "mch_b";
const draft = (state: string, entries: unknown[]) => ({ id: DRF, title: "onboard office", state, revision: 3, cluster: "cl_a", entries });
const imported = { kind: "import-base", machine: MCH, revision: "ibr_a" };
const fragment = { kind: "fragment", name: "ntp", layer: "role", revision: "frv_a", head: null, base: null };
const changed = { kind: "fragment", name: "dns", layer: "role", revision: "frv_b", head: "frv_b", base: 2 };
const removed = { kind: "profile", name: "edge", layer: "role", revision: null, head: "prv_a", base: 1 };
const adoption = { kind: "adoption", of: "fragment", name: "chrony", head: "frv_x", base: null, revision: "frv_x" };

let root: Root;
let box: HTMLElement;
let served: Record<string, unknown>;
const reads = (): Reads => ({
  client: {
    read: async (path: string) => {
      const bare = path.split("?")[0]!;
      if (path in served) return { kind: "ok", data: served[path] };
      if (bare in served) return { kind: "ok", data: served[bare] };
      if (["/fragments", "/profiles", "/assignments", "/machines", "/clusters"].includes(bare) || bare.endsWith("/adoptions") || bare.endsWith("/reach")) return { kind: "ok", data: { items: [], next: undefined } };
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
const mount = (page: ReactNode, roles: Role[] = ["author", "publisher", "approver"]) =>
  act(async () => void root.render(h(WritesProvider, { value: new Writes(() => "p") }, h(ShellContext, { value: { roles, offline: false } }, h(ReadsProvider, { value: reads() }, page)))));
const shown = async (state: string, entries: unknown[], roles?: Role[]) => {
  served[`/drafts/${DRF}`] = draft(state, entries);
  await mount(h(DraftItem, { id: DRF }), roles);
  await act(async () => void (await new Promise((r) => setTimeout(r, 30))));
};
const panels = () => [...box.querySelectorAll("section.panel")];
const panel = (label: string) => box.querySelector(`section.panel[aria-label="${label}"]`)!;
const text = (e: Element | null | undefined) => e?.textContent ?? "";
const before = (a: Element, b: Element) => expect(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING, `${text(a).slice(0, 30)} before ${text(b).slice(0, 30)}`).toBeTruthy();

beforeEach(() => {
  box = document.createElement("div");
  document.body.append(box);
  root = createRoot(box);
  served = {};
});
afterEach(async () => {
  await act(async () => root.unmount());
  box.remove();
});

describe("the open draft page, panel by panel", () => {
  test("the panels come in the design's order: Entries, Shared names, Adopt these machines, the rendered configuration, Discard this draft", async () => {
    await shown("open", [fragment, imported]);
    expect(panels().map((p) => p.getAttribute("aria-label"))).toEqual(["Entries", "Shared names", "Adopt these machines", RENDERED_TITLE, "Discard this draft"]);
  });
  test("a published draft with an import keeps Entries, Adopt these machines and the rendered configuration, and no Shared names or Discard", async () => {
    await shown("published", [imported]);
    expect(panels().map((p) => p.getAttribute("aria-label"))).toEqual(["Entries", "Adopt these machines", RENDERED_TITLE]);
  });
  test("every panel title is an h3 in the panel's band, and the page's own title is the one h2", async () => {
    await shown("open", [fragment]);
    expect(box.querySelectorAll("h2")).toHaveLength(1);
    for (const p of panels()) expect(p.querySelector(":scope > .panel-head > h3"), p.getAttribute("aria-label")!).not.toBeNull();
  });
});

describe("the page header and its one filled button", () => {
  test("Publish this draft is a link drawn as the filled button, in the header, and is the only filled control of an open draft's page", async () => {
    await shown("open", [fragment, imported, adoption]);
    const publish = [...box.querySelectorAll("a")].find((a) => a.textContent === "Publish this draft")!;
    expect(publish.getAttribute("href")).toBe(`/publications/new?draft=${DRF}`);
    expect(publish.className).toBe("button");
    expect(publish.closest(".page-head .actions")).not.toBeNull();
    const filled = [...box.querySelectorAll("button, a.button")].filter((c) => !c.classList.contains("outline"));
    expect(filled.map((c) => text(c))).toEqual(["Publish this draft"]);
  });
  test("the identifier line's Copy is a secondary button there, and the header keeps the state line and the identifier line", async () => {
    await shown("open", [fragment]);
    const head = box.querySelector(".page-head .titles")!;
    expect(head.querySelector('[data-testid="draft-line"]')).not.toBeNull();
    expect(head.querySelector("small button")!.className).toBe("outline");
  });
  test("a draft that is not open has no Publish this draft", async () => {
    await shown("published", [fragment]);
    expect([...box.querySelectorAll("a")].some((a) => a.textContent === "Publish this draft")).toBe(false);
  });
});

describe("the note: one line and the whole note behind What is this page?", () => {
  test("an open draft's line is two literal sentences of its own note, and the full note is the three parts as before", async () => {
    await shown("open", [fragment]);
    const line = text(box.querySelector('[data-testid="page-note-line"]'));
    expect(line).toBe("A draft collects changes to one cluster; its entries are below. Publishing builds a release and sends nothing to any machine.");
    expect(DRAFT_NOTE).toContain(line.split(". ")[0]!);
    expect(DRAFT_NOTE_OPEN).toContain("Publishing builds a release and sends nothing to any machine.");
    expect(box.querySelector("details.about summary")!.textContent).toBe("What is this page?");
  });
  test("a draft that is not open shows the first sentence alone", async () => {
    await shown("discarded", []);
    expect(text(box.querySelector('[data-testid="page-note-line"]'))).toBe("A draft collects changes to one cluster; its entries are below.");
  });
});

describe("Entries: one panel, one table, one action per row", () => {
  test("the columns, then a row for each entry that reads name, kind, where and what changes", async () => {
    await shown("open", [fragment, changed, removed, adoption]);
    const region = panel("Entries").querySelector('[aria-label="Entries table"]')!;
    const cols = [...region.querySelectorAll("thead th")].map((c) => text(c));
    expect(cols).toEqual(["Name", "Kind", "Where", "What changes", "Action"]);
    const rows = [...region.querySelectorAll("tbody tr")].map((r) => [...r.children].map((c) => text(c)));
    expect(rows[0]).toEqual(["ntp", "fragment", "this cluster", "new", "Open"]);
    expect(rows[1]).toEqual(["dns", "fragment", "this cluster", "changed (base revision 2)", "Open"]);
    expect(rows[2]).toEqual(["edge", "profile", "this cluster", "removed", "Open"]);
    expect(rows[3]).toEqual(["chrony", "fragment", "shared library", "this cluster adopts it (base adoption none)", "No editor"]);
  });
  test("the first column is the row header, and the action column's header word is hidden from sight but not from a screen reader", async () => {
    await shown("open", [fragment]);
    const row = panel("Entries").querySelector("tbody tr")!;
    expect(row.firstElementChild!.tagName).toBe("TH");
    expect(row.firstElementChild!.getAttribute("scope")).toBe("row");
    const last = panel("Entries").querySelector("thead th:last-child")!;
    expect(last.className).toBe("action");
    expect(last.querySelector(".sr-only")!.textContent).toBe("Action");
  });
  test("a row's Open is a secondary button named by its entry, and it carries the pressed state of the open editor", async () => {
    await shown("open", [fragment]);
    const open = panel("Entries").querySelector<HTMLButtonElement>("tbody button")!;
    expect(open.className).toBe("outline");
    expect(open.getAttribute("aria-label")).toBe("Open fragment ntp");
    expect(open.getAttribute("aria-pressed")).toBe("false");
    expect(open.closest("td")!.className).toBe("action");
  });
  test("an entry whose name is not safe text shows no part of it, in the cell or in the button's name", async () => {
    await shown("open", [{ ...fragment, name: "a‮b" }]);
    const row = panel("Entries").querySelector("tbody tr")!;
    expect(text(row.firstElementChild)).toBe("(name not shown)");
    expect(row.querySelector("button")!.getAttribute("aria-label")).toBe("Open fragment (name not shown)");
  });
  test("Add fragment, Add profile and Add assignment are secondary buttons in the Entries band, above its table", async () => {
    await shown("open", [fragment]);
    const band = panel("Entries").querySelector(":scope > .panel-head")!;
    const adds = [...band.querySelectorAll(".actions button")];
    expect(adds.map((b) => text(b))).toEqual(["Add fragment", "Add profile", "Add assignment"]);
    expect(adds.every((b) => b.className === "outline")).toBe(true);
    before(band, panel("Entries").querySelector("table")!);
  });
  test("a draft that is not open has no Add buttons and no actions in its band", async () => {
    await shown("published", [fragment]);
    expect(panel("Entries").querySelector(".panel-head .actions")).toBeNull();
  });
  test("an import is one row with one Open link to its page: no second button, no protected-document section on the draft page", async () => {
    await shown("open", [imported]);
    const row = panel("Entries").querySelector("tbody tr")!;
    expect([...row.children].map((c) => text(c))[0]).toMatch(/^Imported configuration/);
    const open = row.querySelector("a.button.outline")!;
    expect(text(open)).toBe("Open");
    expect(open.getAttribute("href")).toBe("/import-base-revisions/ibr_a");
    expect(open.getAttribute("aria-label")).toMatch(/^Open imported configuration/);
    expect(row.querySelectorAll("button, a")).toHaveLength(1);
    expect(box.querySelector('section[aria-label="Protected document"]')).toBeNull();
    expect(text(box)).not.toContain("Show imported configuration");
    expect(text(box)).not.toContain("PROTECTED DOCUMENT");
  });
});

describe("the entry's editor opens as a panel directly under Entries", () => {
  test("Add fragment opens the editor right after the Entries panel, before Shared names", async () => {
    await shown("open", [fragment, adoption]);
    await act(async () => void [...box.querySelectorAll("button")].find((b) => b.textContent === "Add fragment")!.click());
    const editor = box.querySelector('section[aria-label="Fragment (new)"]')!;
    expect(panel("Entries").nextElementSibling).toBe(editor);
    // The editor is drawn as a panel by its own rule (app.css, `section.editor`), not by `.panel`: that rule puts a margin on every child,
    // which sideways-scrolls the page at 320 px under a field as wide as its parent.
    expect(editor.className).toBe("editor");
    expect(editor.querySelector(":scope > h3")).not.toBeNull();
    before(editor, panel("Shared names"));
  });
});

describe("Shared names: its own panel, still read only on request", () => {
  test("closed, the band holds the title and Show shared names, and no list is drawn; pressed, the lists appear in the body", async () => {
    await shown("open", [fragment]);
    const shared = panel("Shared names");
    expect(shared.querySelector(".panel-head h3")!.textContent).toBe("Shared names");
    const show = shared.querySelector(".panel-head button")!;
    expect(text(show)).toBe("Show shared names");
    expect(show.className).toBe("outline");
    expect(shared.querySelector("ul")).toBeNull();
    await act(async () => void (show as HTMLButtonElement).click());
    await act(async () => void (await new Promise((r) => setTimeout(r, 30))));
    expect(text(shared)).toContain("Shared fragments");
    expect(shared.querySelector(".panel-head button")).toBeNull();
  });
});

describe("Adopt these machines: its own panel, its button outlined", () => {
  test("the sentence stays whole above the button, and the page's filled button stays Publish this draft", async () => {
    await shown("open", [imported]);
    const adopt = panel("Adopt these machines");
    expect(adopt.querySelector('[data-testid="handover-sentence"]')).not.toBeNull();
    const press = [...adopt.querySelectorAll("button")].find((b) => b.textContent === "Adopt these machines")!;
    expect(press.className).toBe("outline");
  });
});

describe("the rendered configuration: a panel that holds today's line", () => {
  test("the not-rendered line is inside its panel, with its test id", async () => {
    await shown("open", [fragment]);
    const p = panel(RENDERED_TITLE);
    expect(p.querySelector('[data-testid="not-rendered"]')).not.toBeNull();
  });
});

describe("Discard this draft: the last panel, one sentence above one outlined red button", () => {
  test("it is last, alone in its panel, and the one press behaviour is unchanged", async () => {
    await shown("open", [fragment, imported]);
    const last = panels().at(-1)!;
    expect(last.getAttribute("aria-label")).toBe("Discard this draft");
    expect(text(last.querySelector('[data-testid="discard-sentence"]'))).toBe(DISCARD_SENTENCE);
    const buttons = [...last.querySelectorAll("button")];
    expect(buttons.map((b) => text(b))).toEqual(["Discard draft"]);
    expect(buttons[0]!.className).toBe("outline");
    expect(buttons[0]!.getAttribute("data-variant")).toBe("danger");
    before(last.querySelector('[data-testid="discard-sentence"]')!, buttons[0]!);
  });
  test("it is never next to the primary: Publish this draft is in the header, the whole page above", async () => {
    await shown("open", [fragment]);
    const publish = [...box.querySelectorAll("a")].find((a) => a.textContent === "Publish this draft")!;
    before(publish, panel("Entries"));
    before(panel("Entries"), panel("Discard this draft"));
  });
});

describe("the Tab order is the markup order (client contracts §10.4 item 6)", () => {
  test("Publish this draft, then the note's disclosure, then the Add buttons, then the first Open, Shared names, Adopt these machines and Discard draft", async () => {
    await shown("open", [fragment, imported]);
    const tabbable = [...box.querySelectorAll<HTMLElement>("a[href], button:not(:disabled), summary")];
    const names = tabbable.map((e) => (e.tagName === "SUMMARY" ? "summary" : text(e)));
    const at = (n: string) => names.indexOf(n);
    expect(at("Publish this draft")).toBeGreaterThan(-1);
    expect([at("Publish this draft"), at("summary"), at("Add fragment"), at("Add profile"), at("Add assignment"), at("Open"), at("Show shared names"), at("Adopt these machines"), at("Discard draft")]).toEqual(
      [...[at("Publish this draft"), at("summary"), at("Add fragment"), at("Add profile"), at("Add assignment"), at("Open"), at("Show shared names"), at("Adopt these machines"), at("Discard draft")]].sort((a, b) => a - b),
    );
    expect(at("Copy")).toBeLessThan(at("Publish this draft"));
  });
});
