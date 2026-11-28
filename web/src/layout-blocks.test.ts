// @vitest-environment happy-dom
// The layout blocks of the console layout design (docs/design/console-layout.md §2, ginsys/bronzeward#1255): Panel, PageHead,
// Note and the variants of Action. Each is mounted and read as markup: what a block draws is what the stylesheet and the
// browser cases select on.

import { readFileSync } from "node:fs";
import { act, createElement as h, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { Note, PageHead, Panel, firstSentence, lastSentence } from "./layout.tsx";
import { Action, ShellContext } from "./shell.tsx";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let box: HTMLDivElement;
let root: Root;
const mount = (node: ReactNode) => act(async () => void root.render(h(ShellContext, { value: { roles: ["author"], offline: false } }, node)));
beforeEach(() => {
  box = document.createElement("div");
  document.body.append(box);
  root = createRoot(box);
});
afterEach(async () => {
  await act(async () => root.unmount());
  box.remove();
});

describe("Action variants", () => {
  const draw = async (variant?: "primary" | "secondary" | "destructive") => {
    await mount(h(Action, { label: "Go", needs: ["author"], ...(variant === undefined ? {} : { variant }) }));
    return box.querySelector("button")!;
  };
  test("primary is the default and is Oat's filled button: no class and no variant attribute", async () => {
    for (const b of [await draw(), await draw("primary")]) {
      expect(b.className).toBe("");
      expect(b.hasAttribute("data-variant")).toBe(false);
    }
  });
  test("secondary is the outline", async () => {
    const b = await draw("secondary");
    expect(b.className).toBe("outline");
    expect(b.hasAttribute("data-variant")).toBe(false);
  });
  test("destructive is the outline in the danger colour", async () => {
    const b = await draw("destructive");
    expect(b.className).toBe("outline");
    expect(b.getAttribute("data-variant")).toBe("danger");
  });
  test("a variant changes nothing about the gate: a role the person lacks still disables the button and names the reason", async () => {
    await act(async () => void root.render(h(ShellContext, { value: { roles: [], offline: false } }, h(Action, { label: "Go", needs: ["author"], variant: "destructive" }))));
    const b = box.querySelector("button")!;
    expect(b.disabled).toBe(true);
    expect(box.textContent).toContain("Needs author");
  });
});

describe("Panel", () => {
  test("a titled band holds the title and the panel's actions, and the body follows it", async () => {
    await mount(h(Panel, { title: "Entries", actions: h("button", { type: "button" }, "Add fragment") }, h("p", null, "body")));
    const section = box.querySelector("section.panel")!;
    expect(section.getAttribute("aria-label")).toBe("Entries");
    const band = section.firstElementChild!;
    expect(band.className).toBe("panel-head");
    expect(band.querySelector("h3")!.textContent).toBe("Entries");
    expect(band.querySelector(".actions button")!.textContent).toBe("Add fragment");
    expect(band.nextElementSibling!.textContent).toBe("body");
  });
  test("a panel without actions draws no empty actions element", async () => {
    await mount(h(Panel, { title: "Shared names" }, h("p", null, "x")));
    expect(box.querySelector(".panel-head .actions")).toBeNull();
  });
});

describe("PageHead", () => {
  test("the title and its lines are on the left and the page's actions on the right, in that order", async () => {
    await mount(h(PageHead, { heading: "Draft: onboard office", actions: h("a", { className: "button", href: "/p" }, "Publish this draft") }, h("p", null, "open, revision 3")));
    const head = box.querySelector(".page-head")!;
    const [titles, actions] = [...head.children];
    expect(titles!.className).toBe("titles");
    expect(titles!.querySelector("h2")!.textContent).toBe("Draft: onboard office");
    expect(titles!.querySelector("p")!.textContent).toBe("open, revision 3");
    expect(actions!.className).toBe("actions");
    expect(actions!.textContent).toBe("Publish this draft");
  });
  test("a page with no action draws no actions element", async () => {
    await mount(h(PageHead, { heading: "Drafts" }));
    expect(box.querySelector(".page-head .actions")).toBeNull();
  });
});

describe("Note", () => {
  const FULL = ["A draft collects changes to one cluster; its entries are below. Click an entry to open it.", "While the draft is open you can publish it. Publishing builds a release and sends nothing to any machine."];
  test("one visible line, and the whole note behind a native disclosure that reads What is this page?", async () => {
    await mount(h(Note, { line: "A draft collects changes to one cluster; its entries are below.", parts: FULL }));
    const line = box.querySelector('[data-testid="page-note-line"]')!;
    expect(line.tagName).toBe("P");
    expect(line.className).toBe("lede");
    const about = box.querySelector("details.about")!;
    expect(about.hasAttribute("open")).toBe(false);
    expect(about.querySelector("summary")!.textContent).toBe("What is this page?");
    expect(about.querySelector('[data-testid="page-note"]')!.textContent).toBe(FULL.join(" "));
  });
  test("the line sits before the disclosure, and the disclosure holds the one page-note", async () => {
    await mount(h(Note, { line: "x", parts: FULL }));
    expect(box.querySelectorAll('[data-testid="page-note"]')).toHaveLength(1);
    const line = box.querySelector('[data-testid="page-note-line"]')!;
    expect(line.nextElementSibling!.tagName).toBe("DETAILS");
  });
  test("a note whose line is the whole note is drawn as the plain page note: no line, no disclosure, the same sentence once", async () => {
    await mount(h(Note, { line: "This is one run of work.", parts: ["This is one run of work."] }));
    expect(box.querySelector("details.about")).toBeNull();
    expect(box.querySelector('[data-testid="page-note-line"]')).toBeNull();
    expect(box.querySelectorAll('[data-testid="page-note"]')).toHaveLength(1);
    expect(box.querySelector('[data-testid="page-note"]')!.textContent).toBe("This is one run of work.");
  });
  test("a part that is false or undefined is left out of the full note, as PageNote leaves it", async () => {
    await mount(h(Note, { line: "x", parts: ["one.", false, undefined, "two."] }));
    expect(box.querySelector('[data-testid="page-note"]')!.textContent).toBe("one. two.");
  });
});

describe("the sentences a page's visible line is built from", () => {
  const note = "A draft collects changes to one cluster; its entries are below. Click an entry to open it.";
  const open = "While the draft is open you can add, publish or discard it. Publishing builds a release and sends nothing to any machine.";
  test("firstSentence and lastSentence are literal slices of the note, never new wording", () => {
    expect(firstSentence(note)).toBe("A draft collects changes to one cluster; its entries are below.");
    expect(lastSentence(open)).toBe("Publishing builds a release and sends nothing to any machine.");
    expect(note).toContain(firstSentence(note));
    expect(open).toContain(lastSentence(open));
  });
  test("a note of one sentence gives that sentence for both", () => {
    expect(firstSentence("Only one.")).toBe("Only one.");
    expect(lastSentence("Only one.")).toBe("Only one.");
  });
});

describe("the stylesheet", () => {
  const css = readFileSync(`${import.meta.dirname}/app.css`, "utf8");
  test("carries the blocks of the layout design, section 2.9, and the one extra rule for a link drawn as a button", () => {
    for (const rule of [".page-head {", ".panel-head {", "details.about {", "button.outline { --_hov: #1c2333; }", 'button.outline[data-variant="danger"] { --_hov: #2b1a20; }', "a.button {", "a.button:active { transform: none; }", "button:active:not(:disabled) { transform: none; }", "td.action, th.action {", "main:has(.page-head) p, main:has(.page-head) dd { max-width: 72ch; }", "section.editor {"]) {
      expect(css, rule).toContain(rule);
    }
  });
  test("the reading width is scoped to a page that has the page header, so no other page's text changes width (the advisory review of the first push)", () => {
    expect(css).not.toMatch(/^main p, main dd \{/m);
    expect(css).not.toMatch(/^main \.alert p/m);
  });
  test("the action column's hidden header word has a positioned cell, so a wide table cannot push it outside the scroll region and scroll the page sideways at 320 px", () => {
    // Measured in headless Chrome at 320 px on the page's own markup: without this rule the page scrolled sideways by 655 px (a long name widens the table).
    expect(css).toContain("th.action { position: relative; }");
    expect(css).toMatch(/\.sr-only \{ position: absolute;/);
  });
  test("a filter a link pinned (a paragraph in the toolbar) has no margin and may shrink and wrap, so a long name cannot widen the page at 320 px", () => {
    expect(css).toContain(".toolbar > p { min-width: 0; margin: 0; overflow-wrap: anywhere; }");
  });
  test("an editor draws its box by padding and has no margin rule on its children, so a field as wide as its parent cannot scroll the page sideways", () => {
    const rule = css.split("\n").find((l) => l.startsWith("section.editor {"))!;
    expect(rule).toContain("padding: 0 var(--s4) var(--s4)");
    expect(css).not.toMatch(/section\.editor > :not/);
  });
  test("adds no colour beyond the two hover values of the design: no new hex colour besides #1c2333 and #2b1a20 in the layout block", () => {
    // Comments are not colours: an issue reference like #1255 reads as one.
    const block = css.slice(css.indexOf("/* Console layout blocks")).replace(/\/\*[\s\S]*?\*\//g, "");
    expect(block.length).toBeGreaterThan(0);
    const hex = [...new Set(block.match(/#[0-9a-fA-F]{3,8}\b/g) ?? [])].sort();
    // #2fb677 is the filled button's existing hover colour, reused by a link drawn as that button.
    expect(hex).toEqual(["#1c2333", "#2b1a20", "#2fb677"]);
  });
});
