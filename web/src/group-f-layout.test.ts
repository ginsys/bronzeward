// @vitest-environment happy-dom
// The recovery pages as the console layout design draws them (docs/design/console-layout.md §2.2, §2.3 and §4 group F, item 7,
// ginsys/bronzeward#1268): the recovery console, the state before a recovery start's entry (the enter-recovery form) and the recovery page
// of one operation. Each has a page header, the note as one visible line with the whole note behind "What is this page?", alerts loose
// above the first panel and one panel per part. No button is filled except the enter-recovery form's own action: every other control acts
// on a recorded outcome and which comes first depends on state. Each case reads the markup a person's Tab order and the browser cases meet.

import { act, createElement as h, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { WritesProvider } from "./forms.tsx";
import { firstSentence } from "./layout.tsx";
import { type Reads, ReadsProvider } from "./list.tsx";
import { EnterRecovery, PreEntry, RecoveryConsole } from "./recovery.tsx";
import { RecoveryOperation } from "./recovery-operation.tsx";
import { type Role, ShellContext } from "./shell.tsx";
import {
  ABOUT_PAGE, ENTER_RECOVERY_NOTE, RECOVERY_NOTE, RECOVERY_OPERATION_NOTE, RECOVERY_OPERATION_ROLE_NOTE, RECOVERY_ROLE_NOTE,
} from "./words.ts";
import { Writes } from "./write.ts";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const MCH = "mch_a";
const MCH2 = "mch_b";
const MCH3 = "mch_c";
const OP = "op_k2pq3rstuvwxyz234567abcdefg";
const LONG = `x${"k2pq3rstuvwxyz234567abcdef".repeat(8)}`;
const AT = "2026-11-22T10:00:00Z";

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
const ADMIN: Role[] = ["viewer", "recovery-admin"];
const settle = () => act(async () => void (await new Promise((r) => setTimeout(r, 60))));
const shown = async (page: ReactNode, roles: Role[] = ADMIN) => {
  await act(async () => void root.render(h(WritesProvider, { value: new Writes(() => "p") }, h(ShellContext, { value: { roles, offline: false } }, h(ReadsProvider, { value: reads() }, page)))));
  await settle();
};
const text = (e: Element | null | undefined) => e?.textContent?.trim() ?? "";
const panels = () => [...box.querySelectorAll(":scope > section.panel")];
const titles = () => panels().map((p) => text(p.querySelector(":scope > .panel-head > h3")));
const buttons = (scope: ParentNode) => [...scope.querySelectorAll("button, a.button")];
// A state alert's own action ("Read again", "Dismiss") and the Copy of a refusal's reference are the app's one look for them, so they are not
// page actions.
const filled = (scope: ParentNode) => buttons(scope).filter((b) => !b.classList.contains("outline") && b.closest(".alert, [data-testid='request-ref']") === null).map(text);
const controls = (scope: ParentNode) => [...scope.querySelectorAll("summary, a[href], button, input, select, textarea, [tabindex='0']")];
const before = (a: Element, b: Element) => expect(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING, `${text(a).slice(0, 30)} before ${text(b).slice(0, 30)}`).toBeTruthy();
const noteOf = (note: string, whole = note) => {
  expect(text(box.querySelector('[data-testid="page-note-line"]'))).toBe(firstSentence(note));
  expect(text(box.querySelector("details.about > summary"))).toBe(ABOUT_PAGE);
  expect(text(box.querySelector('details.about [data-testid="page-note"]'))).toBe(whole);
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
const alertOf = (testid: string) => {
  const alert = box.querySelector(`[data-testid="${testid}"]`)!.closest(".alert")!;
  expect(alert.parentElement, `${testid} is loose`).toBe(box);
  return alert;
};
// Each label of a form is the only one of its paragraph: one field per line.
const column = (form: Element) => {
  for (const label of form.querySelectorAll("label")) expect(label.parentElement!.querySelectorAll("label"), text(label)).toHaveLength(1);
  return [...form.querySelectorAll("label")].map((l) => text(l).split(":")[0]);
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

const recovery = (over: Record<string, unknown> = {}) => ({
  epoch: "ep_a", recoveryMode: true,
  entry: { enteredAt: AT, enteredBy: "p0", reason: "restore", stoppedAt: AT, restored: [{ family: "database", backup: LONG, takenAt: AT }] },
  exit: { at: AT, exitedBy: "p0", act: "act_a" },
  scopes: [
    { machine: MCH, state: "ready" }, { machine: MCH2, state: "blocked" }, { machine: MCH3, state: "pre-restore-unaccounted" }, { machine: "mch_d", state: "normal" },
  ],
  ...over,
});

describe("the recovery console", () => {
  test("a page header, the note line and its disclosure, the state alert loose, then a panel per part, the steps last", async () => {
    served["/recovery"] = recovery();
    await shown(h(RecoveryConsole));
    shape();
    noteOf(RECOVERY_NOTE);
    before(box.querySelector("details.about")!, alertOf("recovery-state"));
    expect(titles()).toEqual(["Details", "Scopes in recovery", "Exit recovery", "Revoke identity"]);
    before(alertOf("recovery-state"), panels()[0]!);
    const steps = box.lastElementChild!;
    expect(steps.tagName).toBe("P");
    expect(text(steps)).toMatch(/^Administrator steps, outside the browser:/);
    before(panels().at(-1)!, steps);
    const details = panels()[0]!;
    expect(details.querySelector('[data-testid="recovery-entry"]')).not.toBeNull();
    expect(details.querySelector('[data-testid="recovery-exit"]')).not.toBeNull();
    expect(box.querySelectorAll("h3")).toHaveLength(4);
  });
  // A long machine title, a next step or an answer sits in a cell of the table that is the panel's `.scroll` body, which scrolls by itself
  // (app.css .scroll), so the page does not widen.
  test("the scopes are the panel's scroll body, every cell is inside it, and no button is filled", async () => {
    served["/recovery"] = recovery();
    await shown(h(RecoveryConsole));
    const scroll = panels()[1]!.querySelector(":scope > .scroll")!;
    expect(scroll.querySelector(":scope > table")!.getAttribute("data-testid")).toBe("scopes");
    expect(scroll.querySelectorAll("tbody tr")).toHaveLength(3);
    expect(scroll.querySelectorAll("tbody td")).toHaveLength(12);
    expect(panels()[1]!.querySelectorAll("td")).toHaveLength(12);
    // Mark, Release, Record accounting and its observations button, Exit recovery and Revoke identity are all outlined.
    const labels = buttons(box).map(text);
    for (const l of ["Mark", "Release", "Record accounting", "Read the observations again", "Exit recovery", "Revoke identity"]) expect(labels, l).toContain(l);
    expect(filled(box)).toEqual([]);
  });
  test("the first control after the title is the disclosure's summary", async () => {
    served["/recovery"] = recovery();
    await shown(h(RecoveryConsole));
    expect(controls(box)[0]!.tagName).toBe("SUMMARY");
  });
  test("with no entry and no exit there is no Details panel, and with no scope the empty box is inside its panel", async () => {
    served["/recovery"] = recovery({ entry: null, exit: null, scopes: [] });
    await shown(h(RecoveryConsole));
    expect(titles()).toEqual(["Scopes in recovery", "Exit recovery", "Revoke identity"]);
    expect(panels()[0]!.querySelector('[data-testid="scopes"]')!.closest(".alert")!.parentElement).toBe(panels()[0]);
  });
  test("Exit recovery is its panel's form with the action first, its result after it", async () => {
    served["/recovery"] = recovery();
    await shown(h(RecoveryConsole));
    const form = panels()[2]!.querySelector(":scope > form[aria-label='Exit recovery']")!;
    expect(text(controls(form).at(-1))).toBe("Exit recovery");
    expect(form.querySelector("h3")).toBeNull();
  });
  test("Revoke identity holds its fields one per line in today's order, the confirmation line above the one action, last", async () => {
    served["/recovery"] = recovery();
    await shown(h(RecoveryConsole));
    const form = panels()[3]!.querySelector(":scope > form[aria-label='Revoke identity']")!;
    expect(column(form)).toEqual(["Identity", "Issuer", "Subject", "Reason"]);
    expect(text(controls(form).at(-1))).toBe("Revoke identity");
    expect(form.querySelector("h3")).toBeNull();
  });
  test("a long unbroken backup name sits in a paragraph of the Details panel, which wraps", async () => {
    served["/recovery"] = recovery();
    await shown(h(RecoveryConsole));
    const p = panels()[0]!.querySelector(':scope > p[data-testid="recovery-entry"]')!;
    expect(text(p)).toContain(LONG);
  });
  test("the role sentence of a person without the role is inside the disclosure, and the controls say why they are off", async () => {
    served["/recovery"] = recovery();
    await shown(h(RecoveryConsole), ["viewer"]);
    noteOf(RECOVERY_NOTE, `${RECOVERY_NOTE} ${RECOVERY_ROLE_NOTE}`);
    for (const form of box.querySelectorAll("form[aria-label='Exit recovery'], form[aria-label='Revoke identity']")) expect(text(form)).toContain("Needs recovery-admin");
  });
});

describe("the state before a recovery start's entry", () => {
  const refusal = { kind: "refused", status: 409, problem: { type: "urn:bronzeward:problem:recovery-mode-active", title: "Recovery mode is active", status: 409, detail: "No entry yet." } } as never;
  test("the header, the line, the disclosure, the alerts loose, then one panel with the form: fields one per line, the one filled action last", async () => {
    await shown(h(PreEntry, { res: refusal, onRead: () => {} }));
    shape();
    noteOf(ENTER_RECOVERY_NOTE);
    expect(text(box.querySelector(".page-head h2"))).toBe("Enter recovery");
    const status = alertOf("status");
    before(box.querySelector("details.about")!, status);
    expect(titles()).toEqual(["Enter recovery"]);
    before(status, panels()[0]!);
    expect(panels()).toHaveLength(1);
    const form = panels()[0]!.querySelector(":scope > form[aria-label='Enter recovery']")!;
    expect(form.querySelector("h2")).toBeNull();
    expect(form.querySelector('[data-testid="page-note"]')).toBeNull();
    expect(column(form)).toEqual(["Restored database backup", "taken at", "Restored provider backup", "taken at", "Restored credential-store backup", "taken at", "Restore stopped at", "Reason"]);
    expect([...form.querySelectorAll("input")].map((i) => i.getAttribute("name"))).toEqual([
      "database-backup", "database-taken", "provider-backup", "provider-taken", "credential-store-backup", "credential-store-taken", "stoppedAt", "reason",
    ]);
    expect(text(controls(form).at(-1))).toBe("Enter recovery");
    expect(filled(box)).toEqual(["Enter recovery"]);
    // What the press does and where the administrator steps are come before it.
    const press = controls(form).at(-1)!;
    expect(form.querySelectorAll("ul li").length).toBeGreaterThan(0);
    before(form.querySelector("ul")!, press);
    before(form.querySelector("a[href]")!, press);
    // The disclosure comes first in the Tab order, then the alert's action.
    expect(controls(box)[0]!.tagName).toBe("SUMMARY");
  });
  test("the form alone, with no alerts, draws the same header, note and panel", async () => {
    await shown(h(EnterRecovery, {}));
    shape();
    noteOf(ENTER_RECOVERY_NOTE);
    expect(titles()).toEqual(["Enter recovery"]);
  });
});

describe("the recovery page of one operation", () => {
  const operation = (over: Record<string, unknown> = {}) => ({
    id: OP, kind: "apply-config", state: "unresolved", epoch: "ep_a", lastEvent: 1, subject: { machine: MCH }, createdBy: null, createdAt: AT,
    attempts: [{ id: "att_a", number: 1, generation: 1, transportDeadline: AT, accounting: null }], ...over,
  });
  test("the header holds the state line and the identifier line, then the note, then Take over, Resolve as cancelled and Account for an attempt", async () => {
    served[`/operations/${OP}`] = operation();
    await shown(h(RecoveryOperation, { id: OP }));
    shape();
    noteOf(RECOVERY_OPERATION_NOTE, `${RECOVERY_OPERATION_NOTE} Back to the operation.`);
    const head = box.querySelector(".page-head")!;
    expect(head.querySelector('p.state[data-testid="recovery-operation-state"]')).not.toBeNull();
    expect(text(head.querySelector("small"))).toContain("Copy");
    expect(titles()).toEqual(["Take over", "Resolve as cancelled", "Account for an attempt"]);
    expect(box.querySelectorAll("h2")).toHaveLength(1);
    expect(box.querySelector('details.about a[href^="/operations/"]')).not.toBeNull();
    expect(filled(box)).toEqual([]);
  });
  test("each act is its panel's text, its form with the action first, then its answer region", async () => {
    served[`/operations/${OP}`] = operation({ attempts: [] });
    await shown(h(RecoveryOperation, { id: OP }));
    expect(titles()).toEqual(["Take over", "Resolve as cancelled"]);
    for (const [panel, label, route] of [[panels()[0]!, "Take over", "takeovers"], [panels()[1]!, "Resolve as cancelled", "resolutions"]] as const) {
      expect([...panel.children].map((c) => c.className || c.tagName.toLowerCase())).toEqual(["panel-head", "p", "form", "div"]);
      const form = panel.querySelector(":scope > form")!;
      expect(form.getAttribute("aria-label")).toBe(label);
      expect(form.querySelector("button")!.textContent).toBe(label);
      expect(column(form)).toEqual(["Reason (at most 1024 bytes)"]);
      expect(panel.querySelector(`:scope > [data-testid="${route}-answer"]`)).not.toBeNull();
      expect(panel.querySelectorAll("h3")).toHaveLength(1);
    }
  });
  test("the role sentence of a person without the role is inside the disclosure, and both controls say why they are off", async () => {
    served[`/operations/${OP}`] = operation({ attempts: [] });
    await shown(h(RecoveryOperation, { id: OP }), ["viewer"]);
    expect(text(box.querySelector('details.about [data-testid="page-note"]'))).toContain(RECOVERY_OPERATION_ROLE_NOTE);
    expect(box.querySelectorAll('[data-testid="page-note"]')).toHaveLength(1);
    expect(text(box.querySelector('form[aria-label="Take over"]'))).toContain("Needs recovery-admin");
    expect(text(box.querySelector('form[aria-label="Resolve as cancelled"]'))).toContain("Needs recovery-admin");
  });
  test("the header's controls come first, the machine link then the outlined Copy, then the disclosure's summary", async () => {
    served[`/operations/${OP}`] = operation({ attempts: [] });
    await shown(h(RecoveryOperation, { id: OP }));
    const all = controls(box);
    const copy = all.find((c) => text(c) === "Copy")!;
    expect(copy.classList.contains("outline")).toBe(true);
    expect(box.querySelector(".page-head")!.contains(copy)).toBe(true);
    const summary = box.querySelector("details.about > summary")!;
    expect(all.indexOf(copy)).toBeLessThan(all.indexOf(summary));
    expect(all.indexOf(summary)).toBe(all.indexOf(copy) + 1);
  });
});
