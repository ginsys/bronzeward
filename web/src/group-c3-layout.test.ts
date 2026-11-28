// @vitest-environment happy-dom
// The fragment, profile, assignment, revision, dependency, import and imported configuration pages as the console layout design draws them
// (docs/design/console-layout.md §4 group C, the rest, second part, ginsys/bronzeward#1263): a page header with the identifier line, the note
// as one visible line with the whole note behind "What is this page?", state alerts loose above the first panel, and every section in a panel.
// Each case reads the markup a person's Tab order and the browser cases meet; the words are the console's own and are asserted only where the
// layout moves them.

import { act, createElement as h, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { WritesProvider } from "./forms.tsx";
import { IngestionItem } from "./imports.tsx";
import { firstSentence } from "./layout.tsx";
import { type Reads, ReadsProvider } from "./list.tsx";
import { DependencyItem, ImportBaseItem, RevisionItem, SourceItem } from "./resources.tsx";
import { type Role, ShellContext } from "./shell.tsx";
import { ABOUT_PAGE, DEPENDENCY_NOTE, IMPORT_NOTE, IMPORTED_NOTE, revisionNote, sourceNote } from "./words.ts";
import { Writes } from "./write.ts";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const AT = "2026-11-21T14:02:00Z";
const MCH = "mch_b";
const DRF = "drf_k2pq3rstuvwxyz234567abcdef";
const OP = "op_k2pq3rstuvwxyz234567abcdef";
const ING = "ing_k2pq3rstuvwxyz234567abcdef";
const IBR = "ibr_k2pq3rstuvwxyz234567abcdef";
const LONG = `x${"k2pq3rstuvwxyz234567abcdef".repeat(8)}`;
const list = (items: unknown[] = []) => ({ items, next: undefined });
const ingestion = (state: string, over: Record<string, unknown> = {}) => ({
  id: ING, kind: "import", mode: "encrypted", state, machine: MCH, draft: DRF, operation: OP, review: null, ownerGeneration: 1,
  leaseUntil: new Date(Date.now() + 600_000).toISOString(), expiresAt: new Date(Date.now() + 900_000).toISOString(), createdAt: AT, ...over,
});
const source = (id: string, over: Record<string, unknown> = {}) => ({ id, name: "base", cluster: "cl_a", scope: "cluster", layer: "base", revision: null, headRevision: 0, createdAt: AT, ...over });
const dependency = (over: Record<string, unknown> = {}) => ({
  id: "dep_a", provider: "github", object: "owner/repo", version: 1, created: "2026-11-21", class: "release", reason: null, firstRetainedAt: null, unknownSince: null,
  observedFrom: AT, recordedAt: AT, stale: false, ...over,
});

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
      if (bare.endsWith("/events") || bare.endsWith("/observations") || bare.endsWith("/timeline")) return { kind: "ok", data: list() };
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
const ALL: Role[] = ["author", "publisher", "approver"];
const settle = () => act(async () => void (await new Promise((r) => setTimeout(r, 40))));
const shown = async (page: ReactNode, roles: Role[] = ALL) => {
  await act(async () => void root.render(h(WritesProvider, { value: new Writes(() => "p") }, h(ShellContext, { value: { roles, offline: false } }, h(ReadsProvider, { value: reads() }, page)))));
  await settle();
};
const text = (e: Element | null | undefined) => e?.textContent?.trim() ?? "";
const panels = () => [...box.querySelectorAll(":scope > section.panel")];
const titles = () => panels().map((p) => text(p.querySelector(":scope > .panel-head > h3")));
const panel = (label: string) => box.querySelector(`section.panel[aria-label="${label}"]`)!;
const buttons = (scope: ParentNode) => [...scope.querySelectorAll("button, a.button")];
const button = (label: string) => buttons(box).find((b) => text(b) === label)!;
const filled = (scope: ParentNode) => buttons(scope).filter((b) => !b.classList.contains("outline")).map(text);
const before = (a: Element, b: Element) => expect(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING, `${text(a).slice(0, 30)} before ${text(b).slice(0, 30)}`).toBeTruthy();
const press = async (label: string) => {
  await act(async () => void (button(label) as HTMLElement).click());
  await settle();
};
const noteOf = (note: string) => {
  expect(text(box.querySelector('[data-testid="page-note-line"]'))).toBe(firstSentence(note));
  expect(text(box.querySelector("details.about > summary"))).toBe(ABOUT_PAGE);
  expect(text(box.querySelector('details.about [data-testid="page-note"]'))).toBe(note);
  expect(box.querySelectorAll('[data-testid="page-note"]')).toHaveLength(1);
};
// The header, the note's line, the disclosure, then the page's own content: the shape every page here shares.
const shape = () => {
  const children = [...box.children];
  expect(children[0]!.classList.contains("page-head")).toBe(true);
  expect(children[1]!.classList.contains("lede")).toBe(true);
  expect(children[2]!.classList.contains("about")).toBe(true);
  expect(box.querySelectorAll("h2")).toHaveLength(1);
  expect(box.querySelector(".page-head h2")).not.toBeNull();
  expect(box.querySelectorAll(":scope > h3, :scope > dl, :scope > ul")).toHaveLength(0);
};

beforeEach(() => {
  box = document.createElement("div");
  document.body.append(box);
  root = createRoot(box);
  served = {};
  held = false;
});
afterEach(async () => {
  await act(async () => root.unmount());
  box.remove();
});

describe("the fragment, profile and assignment pages", () => {
  const serve = (kind: string, id: string, over: Record<string, unknown> = {}) => {
    served[`/${kind}s/${id}`] = kind === "assignment" ? { id, cluster: "cl_a", machine: MCH, revision: null, headRevision: 0, createdAt: AT, ...over } : source(id, over);
    served[`/${kind}s/${id}/revisions`] = list();
    served[`/${kind}s/${id}/adoptions`] = list();
  };
  test("a fragment: a page header with no action, the note as one line and a disclosure, then Details and Revisions", async () => {
    serve("fragment", "frg_a");
    await shown(h(SourceItem, { kind: "fragment", id: "frg_a" }));
    shape();
    noteOf(sourceNote("fragment"));
    expect(titles()).toEqual(["Details", "Revisions"]);
    expect(box.querySelector(".page-head .actions")).toBeNull();
    expect([...panel("Details").querySelectorAll(":scope > dl > dt")].map(text)[0]).toBe("Identifier");
    expect(panel("The revision list").querySelector('.scroll[aria-label="Revisions"], p')).not.toBeNull();
    expect(filled(box)).toEqual([]);
  });
  test("a shared fragment adds its clusters in a panel of its own, between Details and Revisions, with its sentence and table inside", async () => {
    serve("fragment", "frg_s", { cluster: null, scope: "shared" });
    served["/fragments/frg_s/adoptions"] = list([{ cluster: "cl_a", revision: "frr_a", behind: false, via: null, release: null, machines: [], adoptionRevision: "aer_a" }]);
    await shown(h(SourceItem, { kind: "fragment", id: "frg_s" }));
    expect(titles()).toEqual(["Details", "Clusters using this fragment", "Revisions"]);
    const shared = panel("Clusters using this fragment");
    expect(shared.querySelector('.scroll[aria-label="Clusters using this name"] table')).not.toBeNull();
    expect(shared.querySelector(":scope > p")).not.toBeNull();
  });
  test("a profile adds the documents of its current revision in a panel", async () => {
    serve("profile", "prf_a", { revision: "prr_a" });
    served["/profile-revisions/prr_a"] = { id: "prr_a", cluster: "cl_a", name: "workers", fragments: [], author: "p0", createdAt: AT };
    await shown(h(SourceItem, { kind: "profile", id: "prf_a" }));
    shape();
    noteOf(sourceNote("profile"));
    expect(titles()).toEqual(["Details", "Documents of the current revision", "Revisions"]);
    expect(text(panel("Documents of the current revision"))).toContain("No fragments pinned.");
  });
  test("an assignment has Details and Revisions and its own note", async () => {
    serve("assignment", "asg_a");
    await shown(h(SourceItem, { kind: "assignment", id: "asg_a" }));
    shape();
    noteOf(sourceNote("assignment"));
    expect(titles()).toEqual(["Details", "Revisions"]);
  });
  test("nothing but the title is drawn while the page is read", async () => {
    held = true;
    await shown(h(SourceItem, { kind: "fragment", id: "frg_a" }));
    expect(box.querySelector(".page-head h2")).not.toBeNull();
    expect(panels()).toHaveLength(0);
    expect(box.querySelector("details.about")).toBeNull();
  });
});

describe("the revision pages", () => {
  test("a fragment revision: the identifier line in the header, then Details, the document under its sanitized notice, and the declared references", async () => {
    served["/fragment-revisions/frr_a"] = { id: "frr_a", cluster: "cl_a", name: "base", layer: "base", document: "a: 1", declarations: { references: { "registry/x": { kind: "secret", version: 1, encoding: null } }, embedded: [] }, author: "p0", createdAt: AT };
    await shown(h(RevisionItem, { kind: "fragment", id: "frr_a" }));
    shape();
    noteOf(revisionNote("fragment"));
    expect(text(box.querySelector(".page-head small"))).toContain("frr_a");
    expect(box.querySelector(".page-head small button")!.className).toBe("outline");
    expect(titles()).toEqual(["Details", "Document", "Declared references"]);
    const doc = panel("Document");
    before(doc.querySelector('[data-testid="sanitized-notice"]')!, doc.querySelector('pre[data-testid="document"]')!);
    expect(doc.querySelector('pre[data-testid="document"]')!.getAttribute("aria-describedby")).toBe("sanitized-notice");
    expect(panel("The declared reference list").querySelector('.scroll[aria-label="Declared references"] table')).not.toBeNull();
    expect(filled(box)).toEqual([]);
  });
  test("a profile revision has Details and its pinned fragments; an assignment revision has Details only", async () => {
    served["/profile-revisions/prr_a"] = { id: "prr_a", cluster: "cl_a", name: "workers", fragments: [], author: "p0", createdAt: AT };
    await shown(h(RevisionItem, { kind: "profile", id: "prr_a" }));
    shape();
    expect(titles()).toEqual(["Details", "Fragments"]);
    await act(async () => root.unmount());
    root = createRoot(box);
    served["/assignment-revisions/asr_a"] = { id: "asr_a", cluster: "cl_a", machine: MCH, profiles: [], fragments: {}, author: "p0", createdAt: AT };
    await shown(h(RevisionItem, { kind: "assignment", id: "asr_a" }));
    shape();
    noteOf(revisionNote("assignment"));
    expect(titles()).toEqual(["Details"]);
  });
});

describe("the dependency page", () => {
  test("a page header, the note as one line and a disclosure, then Details, the releases that depend on it and its alerts", async () => {
    served["/dependencies/dep_a"] = dependency();
    served["/dependencies/dep_a/releases"] = list();
    served["/dependencies/dep_a/alerts"] = list();
    await shown(h(DependencyItem, { id: "dep_a" }));
    shape();
    noteOf(DEPENDENCY_NOTE);
    expect(titles()).toEqual(["Details", "Releases that depend on it", "Alerts"]);
    expect(text(panel("The dependent release list"))).toContain("No release depends on it.");
    expect(text(panel("The alert list"))).toContain("No alerts raised for it.");
    expect([...panel("Details").querySelectorAll(":scope > dl > dt")].map(text)[0]).toBe("Identifier");
  });
});

describe("the import page", () => {
  const serve = (state: string, over: Record<string, unknown> = {}) => {
    served[`/ingestions/${ING}`] = ingestion(state, over);
    served[`/ingestions/${ING}/review`] = { document: "machine:\n  hostname: synthetic\n" };
  };
  test("a page header with the identifier line, the note as one line and a disclosure, the state alert, then Details, Claim and Progress", async () => {
    serve("paused");
    await shown(h(IngestionItem, { id: ING }));
    shape();
    noteOf(IMPORT_NOTE);
    expect(text(box.querySelector(".page-head small"))).toContain(ING);
    expect(box.querySelector(".page-head small button")!.className).toBe("outline");
    expect(box.querySelector(".page-head .actions")).toBeNull();
    expect(titles().slice(0, 2)).toEqual(["Details", "Claim"]);
    expect(titles()[2]).toMatch(/^Progress of operation /);
    expect([...panel("Details").querySelectorAll(":scope > dl > dt")].map(text)).toEqual(["State", "Machine", "Draft", "Operation", "Review", "Owner generation", "Lease", "Expires"]);
    expect(panel("Progress").querySelector('[data-testid="events"]')).not.toBeNull();
    // The state alert stays loose above the first panel; the page draws no live region of its own (the shell's holds the announcement).
    const alert = box.querySelector('[data-testid="allowed"]')!.closest(".alert")!;
    expect(alert.parentElement).toBe(box);
    before(alert, panels()[0]!);
    expect(box.querySelectorAll('[role="status"]')).toHaveLength(0);
  });
  test("the Claim panel takes focus as its section did, and holds the expiry line, the review and every verb with its outcome", async () => {
    serve("paused");
    await shown(h(IngestionItem, { id: ING }));
    const claim = panel("Claim");
    expect(claim.getAttribute("tabindex")).toBe("-1");
    expect(claim.querySelector('section[aria-label="Protected review"]')).not.toBeNull();
    expect(claim.contains(button("Continue import"))).toBe(true);
    expect(claim.contains(button("Abandon import"))).toBe(true);
    expect(text(claim)).toContain("Review expires");
  });
  test("no claim verb is the filled button: Review import, Continue import and Take over import are outlined and Abandon import is outlined red", async () => {
    serve("paused");
    await shown(h(IngestionItem, { id: ING }));
    expect(filled(box)).toEqual([]);
    expect(button("Review import").className).toBe("outline");
    expect(button("Continue import").className).toBe("outline");
    expect(button("Abandon import").className).toBe("outline");
    expect(button("Abandon import").getAttribute("data-variant")).toBe("danger");
    expect(button("Continue import").getAttribute("data-variant")).toBeNull();
    await act(async () => root.unmount());
    root = createRoot(box);
    serve("held", { leaseUntil: new Date(Date.now() - 60_000).toISOString() });
    await shown(h(IngestionItem, { id: ING }));
    expect(filled(box)).toEqual([]);
    expect(button("Take over import").className).toBe("outline");
    expect(button("Abandon import").getAttribute("data-variant")).toBe("danger");
  });
  test("the Tab order is the markup order: the identifier's Copy, the summary, the panels, Review import, Continue import, Abandon import", async () => {
    serve("paused");
    await shown(h(IngestionItem, { id: ING }));
    before(box.querySelector(".page-head small button")!, box.querySelector("details.about > summary")!);
    before(box.querySelector("details.about > summary")!, panels()[0]!);
    before(button("Review import"), button("Continue import"));
    before(button("Continue import"), button("Abandon import"));
  });
  test("an open review has one filled button, Close review, and its marks are outlined; closing it gives the section back its button", async () => {
    serve("paused");
    await shown(h(IngestionItem, { id: ING }));
    await press("Review import");
    expect(box.querySelector('[data-testid="review-text"]')).not.toBeNull();
    expect(filled(box)).toEqual(["Close review"]);
    expect(panel("Claim").contains(button("Close review"))).toBe(true);
    for (const b of buttons(box).filter((x) => ["Mark as secret", "Send marks"].includes(text(x)))) expect(b.className).toBe("outline");
    await press("Close review");
    expect(button("Review import")).toBeDefined();
    expect(filled(box)).toEqual([]);
  });
  test("a page with no claim to act on draws no Claim panel, and a draft update draws no Progress panel", async () => {
    serve("released", { operation: null });
    await shown(h(IngestionItem, { id: ING }));
    expect(titles()).toEqual(["Details"]);
    expect(filled(box)).toEqual([]);
    await act(async () => root.unmount());
    root = createRoot(box);
    serve("held", { kind: "draft-update", machine: null, operation: null });
    await shown(h(IngestionItem, { id: ING }));
    expect(titles().filter((t) => t.startsWith("Progress"))).toEqual([]);
    expect(box.querySelector('section.panel[aria-label="Progress"]')).toBeNull();
  });
  test("while the import is read the page still has its header and its heading, and no panel", async () => {
    held = true;
    await shown(h(IngestionItem, { id: ING }));
    expect(box.querySelector(".page-head h2")!.textContent).toMatch(/^Import /);
    expect(panels()).toHaveLength(0);
    expect(box.querySelector("details.about")).toBeNull();
  });
});

describe("the imported configuration page", () => {
  const serve = () => {
    served[`/import-base-revisions/${IBR}`] = { id: IBR, machine: MCH, author: "p0", createdAt: AT, documentBytes: 24, references: 1, embedded: 0 };
    served[`/import-base-revisions/${IBR}/document`] = { document: "machine:\n  hostname: synthetic\n" };
  };
  test("a page header with the quiet identifier line, the note as one line and a disclosure, then Details and Protected document", async () => {
    serve();
    await shown(h(ImportBaseItem, { id: IBR }));
    shape();
    noteOf(IMPORTED_NOTE);
    expect(box.querySelector(".page-head small span[title]")!.getAttribute("title")).toBe(IBR);
    expect(box.querySelector(".page-head small button")!.className).toBe("outline");
    expect(titles()).toEqual(["Details", "Protected document"]);
    expect([...panel("Details").querySelectorAll(":scope > dl > dt")].map(text)).toEqual(["Machine", "Author", "Imported", "Document size", "References", "Embedded documents"]);
    expect(text(panel("Details").querySelector(":scope > p"))).toContain("The digests and reference names are not shown.");
  });
  test("the Protected document panel takes focus, and its one filled button is Show imported configuration before it is pressed and Close after, never both", async () => {
    serve();
    await shown(h(ImportBaseItem, { id: IBR }));
    const doc = panel("Protected document");
    expect(doc.getAttribute("tabindex")).toBe("-1");
    expect(filled(box)).toEqual(["Show imported configuration"]);
    expect(doc.contains(button("Show imported configuration"))).toBe(true);
    await press("Show imported configuration");
    expect(doc.querySelector('[data-testid="review-notice"]')).not.toBeNull();
    expect(doc.querySelector('textarea[data-testid="review-text"]')).not.toBeNull();
    expect(filled(box)).toEqual(["Close imported configuration"]);
    await press("Close imported configuration");
    expect(filled(box)).toEqual(["Show imported configuration"]);
  });
});

// A dependency's object name, a revision's document line and a reference name have no space to wrap at. The browser cases at 320 CSS pixels
// measure the page (web/e2e/auth/); here the markup that holds each value and the rules that let it wrap are pinned.
describe("a long unbroken value stays inside its panel", () => {
  test("a long object name sits in the dependency's Details grid, directly in its panel", async () => {
    served["/dependencies/dep_a"] = dependency({ object: LONG });
    served["/dependencies/dep_a/releases"] = list();
    served["/dependencies/dep_a/alerts"] = list();
    await shown(h(DependencyItem, { id: "dep_a" }));
    expect(panel("Details").querySelector(":scope > dl")!.textContent).toContain(LONG);
  });
  test("a long document line is one text node in the revision's pre, and a long reference name stays in a scroll region of its own", async () => {
    served["/fragment-revisions/frr_a"] = {
      id: "frr_a", cluster: "cl_a", name: "base", layer: "base", document: `a: ${LONG}`, declarations: { references: { [`registry/${LONG}`]: { kind: "secret", version: 1, encoding: null } }, embedded: [] }, author: "p0", createdAt: AT,
    };
    await shown(h(RevisionItem, { kind: "fragment", id: "frr_a" }));
    expect(panel("Document").querySelector('pre[data-testid="document"]')!.textContent).toContain(LONG);
    const tables = [...panel("The declared reference list").querySelectorAll("table")];
    expect(tables).toHaveLength(1);
    expect(tables[0]!.parentElement!.classList.contains("scroll")).toBe(true);
  });
  test("a long operation name and the imported configuration's text box sit in their panels", async () => {
    served[`/ingestions/${ING}`] = ingestion("paused", { draft: `drf_${LONG}` });
    await shown(h(IngestionItem, { id: ING }));
    expect(panel("Details").textContent).toContain("Draft");
    await act(async () => root.unmount());
    root = createRoot(box);
    served[`/import-base-revisions/${IBR}`] = { id: IBR, machine: MCH, author: "p0", createdAt: AT, documentBytes: 24, references: 0, embedded: 0 };
    served[`/import-base-revisions/${IBR}/document`] = { document: `machine:\n  hostname: ${LONG}\n` };
    await shown(h(ImportBaseItem, { id: IBR }));
    await press("Show imported configuration");
    const area = panel("Protected document").querySelector("textarea")!;
    expect((area as HTMLTextAreaElement).value).toContain(LONG);
    expect(area.closest("section.panel")).toBe(panel("Protected document"));
  });
});
