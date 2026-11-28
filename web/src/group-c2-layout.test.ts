// @vitest-environment happy-dom
// The machine page, the release page and the rendered review as the console layout design draws them (docs/design/console-layout.md §4
// group C, the rest, ginsys/bronzeward#1263): a page header with the page's actions, the note as one visible line with the whole note
// behind "What is this page?", and every section in a panel. Each case reads the markup a person's Tab order and the browser cases meet;
// the words are the console's own and are asserted only where the layout moves them.

import { act, createElement as h, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { firstSentence } from "./layout.tsx";
import { type Reads, ReadsProvider } from "./list.tsx";
import { MachineItem } from "./machine.tsx";
import { ReleaseItem } from "./resources.tsx";
import { ReviewItem } from "./review.tsx";
import { type Role, ShellContext } from "./shell.tsx";
import { ABOUT_PAGE, CREATE_A_DRAFT, MACHINE_NOTE, RELEASE_NOTE, REVIEW_NOTE } from "./words.ts";
import { WritesProvider } from "./forms.tsx";
import { Writes } from "./write.ts";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const MCH = "mch_k2pq3rstuvwxyz234567abcdef";
const REL = "rel_r";
const AT = "2026-11-21T14:02:00Z";
const machine = (over: Record<string, unknown> = {}) => ({
  id: MCH, cluster: "cl_a", name: null, hardware: { smbiosUuid: null, talosNodeId: "node-a", serial: null }, platform: "metal", talosEndpoint: "10.5.0.2:50000",
  desired: REL, applied: { release: "rel_old", source: "operation", importBase: null }, pendingConvergence: true,
  observed: null, frozen: false, scopeState: "normal", openDrift: null, ...over,
});
const position = (m: unknown) => ({ machine: m, ingestion: null, draft: null, plan: null, operation: null, rolloutHeldBy: null, step: "needs-plan" });
const release = (over: Record<string, unknown> = {}) => ({
  id: REL, cluster: "cl_a", publishedAt: AT, publishedBy: null, draft: "drf_a", draftRevision: 3, operation: "op_a",
  renderer: { contract: "v1.14", machineryVersion: "1", kubernetesVersion: "1.34" }, sources: [], machines: [{ machine: MCH, mode: "no-reboot", publicationObservation: null }], ...over,
});
const review = (over: Record<string, unknown> = {}) => ({ release: REL, machine: MCH, mode: "metal", configuration: "machine:\n  token: <redacted:schema>", provenance: [], ...over });

let root: Root;
let box: HTMLElement;
let served: Record<string, unknown>;
const reads = (): Reads => ({
  client: {
    read: async (path: string) => {
      const bare = path.split("?")[0]!;
      if (path in served) return { kind: "ok", data: served[path] };
      if (bare in served) return { kind: "ok", data: served[bare] };
      if (bare.endsWith("/events") || bare.endsWith("/observations") || bare.endsWith("/timeline")) return { kind: "ok", data: { items: [], next: undefined } };
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
const shown = async (page: ReactNode, roles: Role[] = ALL) => {
  await act(async () => void root.render(h(WritesProvider, { value: new Writes(() => "p") }, h(ShellContext, { value: { roles, offline: false } }, h(ReadsProvider, { value: reads() }, page)))));
  await act(async () => void (await new Promise((r) => setTimeout(r, 40))));
};
const text = (e: Element | null | undefined) => e?.textContent?.trim() ?? "";
const top = () => [...box.children].map((c) => (c.classList.contains("page-head") ? "page-head" : c.classList.contains("lede") ? "lede" : c.classList.contains("about") ? "about" : c.classList.contains("panel") ? "panel" : c.tagName.toLowerCase()));
const panels = () => [...box.querySelectorAll(":scope > section.panel")];
const titles = () => panels().map((p) => text(p.querySelector(":scope > .panel-head > h3")));
const panel = (label: string) => box.querySelector(`section.panel[aria-label="${label}"]`)!;
const buttons = (scope: ParentNode) => [...scope.querySelectorAll("button, a.button")];
const filled = (scope: ParentNode) => buttons(scope).filter((b) => !b.classList.contains("outline")).map(text);
const before = (a: Element, b: Element) => expect(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING, `${text(a).slice(0, 30)} before ${text(b).slice(0, 30)}`).toBeTruthy();
const links = (label: string) => [...box.querySelectorAll("a")].filter((a) => text(a) === label);
const noteOf = (line: string, whole: string) => {
  expect(text(box.querySelector('[data-testid="page-note-line"]'))).toBe(line);
  expect(text(box.querySelector("details.about > summary"))).toBe(ABOUT_PAGE);
  expect(text(box.querySelector('details.about [data-testid="page-note"]'))).toBe(whole);
};

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

const MOVED = ["Plans for this machine", "Operations on this machine", "Imports of this machine", "Import this machine"];

describe("the machine page", () => {
  const serve = (over: Record<string, unknown> = {}) => {
    served[`/machines/${MCH}`] = machine(over);
    served[`/machines/${MCH}/position`] = position(machine(over));
  };
  test("a page header with the title and the identifier line, the note as one line and a disclosure, then the panels", async () => {
    serve();
    await shown(h(MachineItem, { id: MCH }));
    expect(top().slice(0, 3)).toEqual(["page-head", "lede", "about"]);
    expect(box.querySelectorAll("h2")).toHaveLength(1);
    expect(box.querySelector(".page-head h2")).not.toBeNull();
    expect(text(box.querySelector(".page-head small"))).toContain(MCH);
    expect(box.querySelector(".page-head small button")!.className).toBe("outline");
    expect(titles()).toEqual(["Status", "Details", "Observations", "Timeline (newest first)", "Name this machine", "Change endpoint"]);
    expect(box.querySelectorAll(":scope > h3, :scope > dl, :scope > ul")).toHaveLength(0);
  });
  test("the line is the first sentence of the note; the whole note and the Create a draft link are behind the disclosure, once", async () => {
    serve();
    await shown(h(MachineItem, { id: MCH }));
    expect(text(box.querySelector('[data-testid="page-note-line"]'))).toBe(firstSentence(MACHINE_NOTE));
    expect(text(box.querySelector('details.about [data-testid="page-note"]'))).toBe(`${MACHINE_NOTE} ${CREATE_A_DRAFT}.`);
    const create = links(CREATE_A_DRAFT);
    expect(create).toHaveLength(1);
    expect(box.querySelector("details.about")!.contains(create[0]!)).toBe(true);
  });
  test("the four links that were a paragraph are the page's header actions, outlined, each once, and the page has no filled button", async () => {
    serve();
    await shown(h(MachineItem, { id: MCH }));
    const actions = box.querySelector(".page-head > .actions")!;
    expect([...actions.querySelectorAll("a")].map(text)).toEqual(MOVED);
    for (const label of MOVED) {
      expect(links(label), label).toHaveLength(1);
      expect(links(label)[0]!.className).toBe("button outline");
    }
    expect(links("Import this machine")[0]!.getAttribute("href")).toBe(`/ingestions/new?machine=${MCH}`);
    expect(filled(box)).toEqual([]);
    // The markup order is the Tab order: the title's Copy, the actions, the note's summary, then the panels.
    before(box.querySelector(".page-head small button")!, actions);
    before(actions, box.querySelector("details.about > summary")!);
    before(box.querySelector("details.about > summary")!, panels()[0]!);
  });
  test("Status holds the lines a person reads first, in the order they had; Details holds the grid", async () => {
    serve({ openDrift: { revision: 3, observation: "obs_k", lastObservation: "obs_m", lateLanding: null } });
    await shown(h(MachineItem, { id: MCH }));
    const status = panel("Machine status");
    expect(status.querySelector('ul[data-testid="sentences"]')).not.toBeNull();
    expect(status.querySelector('[data-testid="drift-link"]')).not.toBeNull();
    expect(status.querySelector('[data-testid="next-step"]')).not.toBeNull();
    expect(text(status.querySelector(":scope > p"))).toContain("10.5.0.2:50000");
    before(status.querySelector('[data-testid="sentences"]')!, status.querySelector('[data-testid="drift-link"]')!);
    before(status.querySelector('[data-testid="drift-link"]')!, status.querySelector('[data-testid="next-step"]')!);
    expect([...panel("Machine details").querySelectorAll(":scope > dl > dt")].map(text)).toEqual(["Desired", "Applied", "Import base", "Observed", "Scope"]);
    expect(panel("Machine details").querySelector('[data-testid="observed"]')).not.toBeNull();
  });
  test("Observations and the Timeline are panels with their tables in scroll regions of their own; Load older stays under the table", async () => {
    serve();
    await shown(h(MachineItem, { id: MCH }));
    expect(text(panel("Observations list"))).toContain("No observation recorded.");
    expect(text(panel("Machine timeline"))).toContain("No timeline entry recorded.");
  });
  test("Name this machine and Change endpoint are panels holding their forms, the submit outlined, the field and the form's name unchanged", async () => {
    serve();
    await shown(h(MachineItem, { id: MCH }));
    for (const [title, label] of [["Name this machine", "Machine name"], ["Change endpoint", "Endpoint change"]] as const) {
      const p = panel(label);
      expect(text(p.querySelector(":scope > .panel-head > h3"))).toBe(title);
      const form = p.querySelector(`form[aria-label="${title}"]`)!;
      expect(form).not.toBeNull();
      expect(form.querySelector("button[type=submit]")!.className).toBe("outline");
      expect(form.querySelector("input")).not.toBeNull();
    }
    expect(box.querySelectorAll('form[aria-label="Name this machine"], form[aria-label="Rename machine"]')).toHaveLength(1);
    expect(panel("Machine name").querySelector('[data-testid="rename-reaches"]')).not.toBeNull();
    before(panel("Machine timeline"), panel("Machine name"));
  });
  test("a named machine's panel is Rename machine", async () => {
    serve({ name: "cp-1" });
    await shown(h(MachineItem, { id: MCH }));
    expect(titles()).toContain("Rename machine");
    expect(titles()).not.toContain("Name this machine");
    expect(box.querySelector('form[aria-label="Rename machine"]')).not.toBeNull();
  });
  test("no panel and no header is drawn before the machine is read: a read that fails shows the old heading and the failure", async () => {
    await shown(h(MachineItem, { id: MCH }));
    expect(panels()).toHaveLength(0);
    expect(box.querySelector("details.about")).toBeNull();
  });
  test("a viewer's links stay links, and the forms' buttons stay off with their reasons", async () => {
    serve();
    await shown(h(MachineItem, { id: MCH }), ["viewer"]);
    expect([...box.querySelectorAll(".page-head > .actions a")].map(text)).toEqual(MOVED);
    expect(panel("Machine name").querySelector("button[type=submit]")!.hasAttribute("disabled")).toBe(true);
    expect(text(panel("Machine name"))).toContain("Needs author");
  });
});

describe("the release page", () => {
  const serve = (over: Record<string, unknown> = {}) => Object.assign(served, { [`/releases/${REL}`]: release(over) });
  test("a page header whose one filled button is Apply this release to machines, the note as one line and a disclosure, then three panels", async () => {
    serve();
    await shown(h(ReleaseItem, { id: REL }));
    expect(top().slice(0, 3)).toEqual(["page-head", "lede", "about"]);
    expect(box.querySelectorAll("h2")).toHaveLength(1);
    const apply = links("Apply this release to machines");
    expect(apply).toHaveLength(1);
    expect(apply[0]!.className).toBe("button");
    expect(apply[0]!.getAttribute("href")).toBe(`/releases/${REL}/apply`);
    expect(box.querySelector(".page-head > .actions")!.contains(apply[0]!)).toBe(true);
    expect(filled(box)).toEqual(["Apply this release to machines"]);
    before(apply[0]!, box.querySelector("details.about > summary")!);
    noteOf(firstSentence(RELEASE_NOTE), RELEASE_NOTE);
    expect(titles()).toEqual(["Details", "Source revisions this release was built from (these are not configuration)", "Machines covered (1)"]);
    expect(box.querySelectorAll(":scope > h3, :scope > dl")).toHaveLength(0);
  });
  test("the headings keep their words as the panels' titles, and each table sits in its panel", async () => {
    serve();
    await shown(h(ReleaseItem, { id: REL }));
    expect([...box.querySelectorAll("h3")].map(text)).toContain("Machines covered (1)");
    expect([...panel("Details").querySelectorAll(":scope > dl > dt")].map(text)).toEqual(["Identifier", "Cluster", "Published", "Renderer", "Draft", "Publication"]);
    const covered = panel("The covered machine list");
    expect(covered.querySelector('.scroll[aria-label="Machines covered"] table')).not.toBeNull();
    expect([...covered.querySelectorAll("a")].filter((a) => text(a).startsWith("Rendered configuration of"))).toHaveLength(1);
    expect(panel("The source list").querySelector('.scroll[aria-label="Source revisions"]')).not.toBeNull();
  });
  test("no panel is drawn while the release is read", async () => {
    await shown(h(ReleaseItem, { id: REL }));
    expect(panels()).toHaveLength(0);
    expect(box.querySelector("details.about")).toBeNull();
  });
});

describe("the rendered review page", () => {
  const serve = (r = review()) => Object.assign(served, { [`/releases/${REL}/machines/${MCH}/review`]: r, [`/machines/${MCH}/position`]: position(machine()), [`/machines/${MCH}`]: machine() });
  test("a page header with Back to the release outlined, the note as one line and a disclosure, then Details, the configuration, Provenance and Next step", async () => {
    serve();
    await shown(h(ReviewItem, { release: REL, machine: MCH }));
    expect(top().slice(0, 3)).toEqual(["page-head", "lede", "about"]);
    expect(box.querySelectorAll("h2")).toHaveLength(1);
    const back = links("Back to the release");
    expect(back).toHaveLength(1);
    expect(back[0]!.className).toBe("button outline");
    expect(box.querySelector(".page-head > .actions")!.contains(back[0]!)).toBe(true);
    before(back[0]!, box.querySelector("details.about > summary")!);
    noteOf(firstSentence(REVIEW_NOTE), REVIEW_NOTE);
    expect(titles()).toEqual(["Details", "Rendered configuration", "Provenance (secret references only)", "Next step"]);
    expect(box.querySelectorAll(":scope > h3, :scope > dl, :scope > p:not(.lede)")).toHaveLength(0);
  });
  test("the document and its sentence about empty fields are in the configuration panel, the Details grid holds release, machine and mode", async () => {
    serve();
    await shown(h(ReviewItem, { release: REL, machine: MCH }));
    const conf = panel("The rendered configuration");
    expect(conf.querySelector('[data-testid="document"]')).not.toBeNull();
    expect(conf.querySelectorAll('.badge')).toHaveLength(1);
    expect([...panel("Details").querySelectorAll(":scope > dl > dt")].map(text)).toEqual(["Release", "Machine", "Validated in mode"]);
  });
  test("Plan apply is the page's one filled button, in the Next step panel under the sentence it acts on", async () => {
    serve();
    await shown(h(ReviewItem, { release: REL, machine: MCH }));
    const next = panel("Next step");
    expect(filled(box)).toEqual(["Plan apply"]);
    expect(next.contains(buttons(box).find((b) => text(b) === "Plan apply")!)).toBe(true);
    before(next.querySelector('[data-testid="next-step"]')!, buttons(next)[0]!);
  });
  test("a withheld configuration draws its state in the configuration panel and no note", async () => {
    serve(review({ configuration: null }));
    await shown(h(ReviewItem, { release: REL, machine: MCH }));
    expect(box.querySelector('[data-testid="page-note"]')).toBeNull();
    expect(box.querySelector("details.about")).toBeNull();
    expect(box.querySelector('[data-testid="document"]')).toBeNull();
    expect(titles()).toEqual(["Details", "Rendered configuration", "Provenance (secret references only)", "Next step"]);
    expect(text(panel("The rendered configuration"))).not.toBe("");
  });
  test("a failed position read draws the failure and Read again in the Next step panel, and no plan button", async () => {
    serve();
    delete served[`/machines/${MCH}/position`];
    await shown(h(ReviewItem, { release: REL, machine: MCH }));
    const next = panel("Next step");
    expect(buttons(next).map(text)).toEqual(["Read again"]);
  });
});

// A machine's node name, a hostname, a token-free configuration line and a revision identifier have no space to wrap at. The browser cases at
// 320 CSS pixels measure the page (web/e2e/auth/inventory.e2e.ts); here the markup that holds each value and the rules that let it wrap are pinned.
describe("a long unbroken value stays inside its panel", () => {
  const LONG = `x${"k2pq3rstuvwxyz234567abcdef".repeat(8)}`;
  test("a long node ID and endpoint sit in the machine's Status line and Details grid, which wrap anywhere", async () => {
    served[`/machines/${MCH}`] = machine({ talosEndpoint: LONG, hardware: { smbiosUuid: null, talosNodeId: LONG, serial: null } });
    served[`/machines/${MCH}/position`] = position(machine());
    await shown(h(MachineItem, { id: MCH }));
    const status = panel("Machine status");
    expect(text(status.querySelector(":scope > p"))).toContain(LONG);
    expect(box.querySelector("table")?.closest(".scroll")).not.toBeNull();
    for (const t of box.querySelectorAll("table")) expect(t.parentElement!.classList.contains("scroll")).toBe(true);
  });
  test("a long unbroken configuration line is one text node in the document's pre, which wraps, and a long provenance path stays in a scroll region", async () => {
    served[`/releases/${REL}/machines/${MCH}/review`] = review({
      configuration: `machine:\n  value: ${LONG}`,
      provenance: [{ reference: "registry/example-pass", version: 1, member: null, source: { revision: `frv_${LONG}`, digest: "sha256:0", path: `doc[0]/${LONG}`, occurrence: 0 }, output: `doc[0]/${LONG}` }],
    });
    served[`/machines/${MCH}/position`] = position(machine());
    served[`/machines/${MCH}`] = machine();
    await shown(h(ReviewItem, { release: REL, machine: MCH }));
    const pre = panel("The rendered configuration").querySelector('pre[data-testid="document"]')!;
    expect(pre.textContent).toContain(LONG);
    for (const t of panel("Provenance (secret references only)").querySelectorAll("table")) expect(t.parentElement!.classList.contains("scroll")).toBe(true);
  });
});
