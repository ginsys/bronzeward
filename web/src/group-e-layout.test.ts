// @vitest-environment happy-dom
// The six entry-form pages as the console layout design draws them (docs/design/console-layout.md §2.2 and §4 group E, item 6,
// ginsys/bronzeward#1266): a page header with no action, the note as one visible line with the whole note behind "What is this page?",
// alerts loose above the panel, and the form in one panel with its fields in one column and its one action at the end. Each case reads
// the markup a person's Tab order and the browser cases meet; the words are the console's own and are asserted only where the layout
// moves them.

import { act, createElement as h, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { CreateDraft } from "./drafts.tsx";
import { AddCluster, RecordMachine, WritesProvider } from "./forms.tsx";
import { StartImport } from "./imports.tsx";
import { firstSentence } from "./layout.tsx";
import { type Reads, ReadsProvider } from "./list.tsx";
import type { Plan } from "./onboard.ts";
import { ConfirmScreen, OnboardCluster } from "./onboard.tsx";
import { PlanNew } from "./plan.tsx";
import { type Role, ShellContext } from "./shell.tsx";
import {
  ABOUT_PAGE, ADD_MACHINE, ADD_MACHINE_NOTE, BY_HAND_NOTE, CONNECT_NOTE, CONNECT_STEP_ONE, CONNECT_STEP_TWO, CONNECT_THIS_CLUSTER, CREATE_DRAFT_NOTE, CREATE_PLAN_NOTE,
  DRIFT_ADOPTION_NOTE, ENTER_A_CLUSTER_BY_HAND, START_IMPORT_NOTE,
} from "./words.ts";
import { Writes } from "./write.ts";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const REL = "rel_a";
const MCH = "mch_b";
const LONG = `x${"k2pq3rstuvwxyz234567abcdef".repeat(8)}`;
const list = (items: unknown[] = []) => ({ items, next: undefined });

let root: Root;
let box: HTMLElement;
let served: Record<string, unknown>;
const reads = (): Reads => ({
  client: {
    read: async (path: string) => {
      const bare = path.split("?")[0]!;
      if (path in served) return { kind: "ok", data: served[path] };
      if (bare in served) return { kind: "ok", data: served[bare] };
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
const buttons = (scope: ParentNode) => [...scope.querySelectorAll("button, a.button")];
const filled = (scope: ParentNode) => buttons(scope).filter((b) => !b.classList.contains("outline")).map(text);
const controls = (scope: ParentNode) => [...scope.querySelectorAll("summary, a[href], button, input, select, textarea, [tabindex='0']")];
const before = (a: Element, b: Element) => expect(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING, `${text(a).slice(0, 30)} before ${text(b).slice(0, 30)}`).toBeTruthy();
const noteOf = (note: string) => {
  expect(text(box.querySelector('[data-testid="page-note-line"]'))).toBe(firstSentence(note));
  expect(text(box.querySelector("details.about > summary"))).toBe(ABOUT_PAGE);
  expect(text(box.querySelector('details.about [data-testid="page-note"]'))).toBe(note);
  expect(box.querySelectorAll('[data-testid="page-note"]')).toHaveLength(1);
};
// The header, the note's line, the disclosure, then the page's own content.
const shape = () => {
  const children = [...box.children];
  expect(children[0]!.classList.contains("page-head")).toBe(true);
  expect(children[1]!.classList.contains("lede")).toBe(true);
  expect(children[2]!.classList.contains("about")).toBe(true);
  expect(box.querySelectorAll("h2")).toHaveLength(1);
  expect(box.querySelector(".page-head .actions")).toBeNull();
};
// The form is the panel's own child, its one action is its last control, and it is the page's one filled button.
const oneForm = (label: string) => {
  const form = box.querySelector(":scope > section.panel > form")!;
  expect(form).not.toBeNull();
  expect(panels()).toHaveLength(1);
  const last = controls(form).at(-1)!;
  expect(text(last)).toBe(label);
  expect(filled(box)).toEqual([label]);
  // The Tab order is the markup order: the disclosure first, then the form's controls.
  before(box.querySelector("details.about > summary")!, controls(form)[0]!);
  return form;
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

describe("Create draft", () => {
  test("a page header, the note line and its disclosure, then the form in one panel with its one action last", async () => {
    served["/clusters"] = list([{ id: "cl_a", name: "office" }]);
    await shown(h(CreateDraft));
    shape();
    noteOf(CREATE_DRAFT_NOTE);
    expect(titles()).toEqual(["Create draft"]);
    const form = oneForm("Create draft");
    expect([...form.querySelectorAll("label")].map((l) => text(l).split(":")[0])).toEqual(["Cluster", "Title"]);
    expect(form.querySelector("label select[name=cluster]")).not.toBeNull();
  });
  test("a long cluster name is an option of the cluster select, directly in the form", async () => {
    served["/clusters"] = list([{ id: "cl_a", name: LONG }]);
    await shown(h(CreateDraft));
    const select = box.querySelector(":scope > section.panel > form select[name=cluster]")!;
    expect(text(select)).toContain(LONG);
  });
});

describe("Enter a cluster by hand", () => {
  test("the form in one panel, its four fields in one column in today's order, its one action last", async () => {
    await shown(h(AddCluster));
    shape();
    noteOf(BY_HAND_NOTE);
    expect(titles()).toEqual([ENTER_A_CLUSTER_BY_HAND]);
    const form = oneForm(ENTER_A_CLUSTER_BY_HAND);
    expect([...form.querySelectorAll("input")].map((i) => i.getAttribute("name"))).toEqual(["name", "endpoint", "contract", "talosClusterId"]);
    for (const i of form.querySelectorAll("input")) expect(i.closest("label")).not.toBeNull();
  });
});

describe("Add machine", () => {
  test("the form in one panel, its fields in today's order, a long cluster name only an option of its select", async () => {
    served["/clusters"] = list([{ id: "cl_a", name: LONG }]);
    await shown(h(RecordMachine, { query: "" }));
    shape();
    noteOf(ADD_MACHINE_NOTE);
    expect(titles()).toEqual([ADD_MACHINE]);
    const form = oneForm(ADD_MACHINE);
    expect([...form.querySelectorAll("select, input")].map((i) => i.getAttribute("name"))).toEqual(["cluster", "talosNodeId", "platform", "talosEndpoint"]);
    expect(text(form.querySelector("select[name=cluster]"))).toContain(LONG);
  });
});

describe("Plan new", () => {
  test("the form in one panel titled Create plan, the target sentence and the optional values inside it", async () => {
    await shown(h(PlanNew, { query: `release=${REL}&machine=${MCH}` }));
    shape();
    noteOf(CREATE_PLAN_NOTE);
    expect(titles()).toEqual(["Create plan"]);
    const form = oneForm("Create plan");
    expect(form.getAttribute("aria-label")).toBe("Create plan");
    expect(form.querySelector("select")).not.toBeNull();
    expect(form.querySelectorAll("input[inputmode=numeric]").length).toBeGreaterThan(0);
  });
  test("with no target it keeps its one alert and draws no header and no panel", async () => {
    await shown(h(PlanNew, { query: "" }));
    expect(box.querySelector('[data-testid="no-target"]')).not.toBeNull();
    expect(box.querySelector(".page-head")).toBeNull();
    expect(panels()).toHaveLength(0);
  });
});

describe("Start import and Start drift adoption", () => {
  test("Start import: the form in one panel, Create draft beside the draft select outlined, Start import the one filled button, last", async () => {
    served["/machines"] = list();
    served["/drafts"] = list();
    await shown(h(StartImport, { query: "" }));
    shape();
    noteOf(START_IMPORT_NOTE);
    expect(titles()).toEqual(["Start import"]);
    const form = oneForm("Start import");
    const create = buttons(form).find((b) => text(b) === "Create draft")!;
    expect(create.classList.contains("outline")).toBe(true);
    expect(create.getAttribute("type")).toBe("button");
  });
  test("Start drift adoption has its own heading, title and note", async () => {
    await shown(h(StartImport, { query: "kind=drift-adoption&source=machine" }));
    shape();
    noteOf(DRIFT_ADOPTION_NOTE);
    expect(text(box.querySelector(".page-head h2"))).toBe("Start drift adoption");
    expect(titles()).toEqual(["Start drift adoption"]);
    oneForm("Start drift adoption");
  });
  test("a refused query is an alert loose between the disclosure and the panel", async () => {
    await shown(h(StartImport, { query: "kind=nonsense" }));
    const alert = box.querySelector('[data-testid="start-refused"]')!.closest(".alert")!;
    expect(alert.parentElement).toBe(box);
    before(box.querySelector("details.about")!, alert);
    before(alert, panels()[0]!);
  });
});

describe("Connect a cluster", () => {
  test("step one is a panel titled by its step, holding the form whose action is Read cluster", async () => {
    await shown(h(OnboardCluster));
    shape();
    noteOf(CONNECT_NOTE);
    expect(titles()).toEqual([CONNECT_STEP_ONE]);
    const form = oneForm("Read cluster");
    expect(form.getAttribute("aria-label")).toBe("Read the cluster");
    expect(box.querySelectorAll("h3")).toHaveLength(1);
    expect([...form.querySelectorAll("input")].map((i) => i.getAttribute("name"))).toEqual(["file", "name", "context", "endpoint"]);
  });
  test("step two is a panel titled by its step: the found sentence, the members in a scroll body, then the form with Connect this cluster last", async () => {
    const plan = {
      found: { version: "v1.11.0", contract: "v1.11", members: [{ nodeId: "n1", hostname: LONG, role: "controlplane", address: `10.0.0.1`, platform: "metal", answered: true }], endpoint: "10.0.0.1:50000", complete: true },
      endpoint: "10.0.0.1:50000", name: "office", kube: "https://10.0.0.1:6443",
    } as unknown as Plan;
    await shown(h(ConfirmScreen, { p: plan, busy: false, onSubmit: () => {} }));
    expect(titles()).toEqual([CONNECT_STEP_TWO]);
    const panel = panels()[0]!;
    expect([...panel.children].map((c) => c.className || c.tagName.toLowerCase())).toEqual(["panel-head", "p", "scroll", "form"]);
    const scroll = panel.querySelector(":scope > .scroll")!;
    expect(text(scroll.querySelector("td"))).toBe(LONG);
    expect([...scroll.querySelectorAll("th")].map(text)).toEqual(["Hostname", "Role", "Address", "Platform", "Answered"]);
    const form = panel.querySelector(":scope > form")!;
    expect(form.getAttribute("aria-label")).toBe("Record the cluster");
    expect(text(controls(form).at(-1))).toBe(CONNECT_THIS_CLUSTER);
    expect(filled(box)).toEqual([CONNECT_THIS_CLUSTER]);
    expect(form.querySelector('[data-testid="onboard-name"]')).not.toBeNull();
    expect(form.querySelector("input[name=kube]")).not.toBeNull();
  });
});
