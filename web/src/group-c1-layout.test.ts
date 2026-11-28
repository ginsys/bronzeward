// @vitest-environment happy-dom
// The plan page, the apply page, the drift page and the operation page's details and Attempts as the console layout design draws them
// (docs/design/console-layout.md §4 group C, plans and delivery, ginsys/bronzeward#1261): a page header, the note as one visible line with the
// whole note behind "What is this page?", and every section in a panel with its actions where they belong. Each case reads the markup a
// person's Tab order and the browser cases meet; the words are the console's own and are asserted only where the layout moves them.

import { readFileSync } from "node:fs";
import { act, createElement as h, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { firstSentence } from "./layout.tsx";
import { type Reads, ReadsProvider } from "./list.tsx";
import { OperationItem, PlanItem, view } from "./resources.tsx";
import { type Role, ShellContext } from "./shell.tsx";
import { ABOUT_PAGE, APPLY_NOTE, DRIFT_NOTE, DRIFT_NOTE_OPEN, OPERATION_NOTE, PLAN_NOTE, PLAN_NOTE_APPROVE } from "./words.ts";
import { WritesProvider } from "./forms.tsx";
import { Writes } from "./write.ts";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const MCH = "mch_k2pq3rstuvwxyz234567abcdef";
const REL = "rel_r";
const OP = "op_k2pq3rstuvwxyz234567abcdef";
const PLN = "pln_k2pq3rstuvwxyz234567abcdef";
const A = "mch_a";
const B = "mch_b";
const AT = "2026-11-21T14:02:00Z";
const machine = (over: Record<string, unknown> = {}) => ({
  id: MCH, cluster: "cl_a", name: "cp1", hardware: { smbiosUuid: null, talosNodeId: "node-a", serial: null }, platform: "metal", talosEndpoint: "10.5.0.2:50000",
  desired: REL, applied: { release: REL, source: "operation", importBase: null }, pendingConvergence: false,
  observed: { observation: "obs_m", at: AT, release: null }, frozen: false, scopeState: "normal",
  openDrift: { revision: 3, observation: "obs_k", lastObservation: "obs_m", lateLanding: null }, ...over,
});
const plan = (state: string, over: Record<string, unknown> = {}) => ({
  id: PLN, state, expiresAt: "2026-11-22T14:02:00Z", machine: MCH, cluster: "cl_a", route: "talos", operation: "apply-config", mode: "no-reboot", release: REL,
  desiredRelease: REL, assignmentRevision: "asr_a", baselineRevision: null, driftRecord: null, epoch: "e0", transportDeadlineSeconds: 60, verificationDeadlineSeconds: 60,
  maxObservationAgeSeconds: 60, checkValiditySeconds: 60, maxAttempts: null, rolloutLimit: 1, approvalPolicy: "one", approval: null, committedOperation: null,
  createdAt: AT, createdBy: { principal: "p0", role: "publisher" },
  evidence: { validation: { result: "ok", at: "t", contract: "v1.14", machineryVersion: "1", kubernetesVersion: "1.34", platformMode: "metal" }, dryRun: { available: false, reason: "none" }, baseline: null, diff: null },
  ...over,
});
// An approved plan whose approval is from before the last recovery entry: the Approval form shows beside Revoke and Cancel.
const earlier = (): Record<string, unknown> => ({
  [`/plans/${PLN}`]: plan("approved", { approval: "apr_a" }),
  "/approvals/apr_a": { id: "apr_a", plan: PLN, approver: { principal: "p", role: "approver" }, epoch: "ep_before", selfApproval: { marked: false, reasons: [] }, unseenOverwrite: false },
  "/identity": { principal: "p", epoch: "ep_now" },
});
const attempt = { id: "att_a", number: 1, at: AT, route: "talos", controller: "c", generation: 1, transportDeadline: AT, verificationDeadline: AT, response: null, accounting: null };
const operation = (over: Record<string, unknown> = {}) => ({
  id: OP, kind: "apply-config", state: "running", subject: { machine: MCH }, createdAt: AT, createdBy: null, epoch: "e0", lastEvent: null, result: null, error: null, attempts: [attempt], ...over,
});
const mchOf = (id: string) => ({ id, cluster: "cl_c", name: id, role: "worker", platform: "metal", desired: REL, applied: null, pendingConvergence: true, formOnly: false, frozen: false, openDrift: null });
const positionOf = (id: string, p: string) => ({
  machine: mchOf(id), ingestion: null, draft: null, operation: null, rolloutHeldBy: null, step: "awaiting-approval",
  plan: { id: p, state: "proposed", operation: "apply-config", release: REL, approvalCurrent: null, createdAt: AT, expiresAt: "2026-11-23T08:00:00Z" },
});
const planOf = (id: string, m: string) => ({ id, machine: m, release: REL, operation: "apply-config", createdBy: { principal: "prn_x", role: "publisher" }, createdAt: AT, evidence: { diff: null, validation: null, dryRun: null, baseline: null } });
const release = (over: Record<string, unknown> = {}) => ({ id: REL, cluster: "cl_c", sources: [], machines: [{ machine: A }, { machine: B }], ...over });

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
const danger = (scope: ParentNode) => buttons(scope).filter((b) => b.classList.contains("outline") && b.getAttribute("data-variant") === "danger").map(text);
const before = (a: Element, b: Element) => expect(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING, `${text(a).slice(0, 30)} before ${text(b).slice(0, 30)}`).toBeTruthy();
const button = (scope: ParentNode, label: string) => [...scope.querySelectorAll("button")].find((b) => text(b) === label)!;

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

// The note of each page as one visible line and the whole note behind the disclosure.
const noteOf = (line: string, whole: string) => {
  expect(text(box.querySelector('[data-testid="page-note-line"]'))).toBe(line);
  expect(text(box.querySelector("details.about > summary"))).toBe(ABOUT_PAGE);
  expect(text(box.querySelector('details.about [data-testid="page-note"]'))).toBe(whole);
};

describe("the plan page", () => {
  test("a page header, the note as one line and a disclosure, then Details and Evidence panels", async () => {
    served[`/plans/${PLN}`] = plan("proposed");
    await shown(h(PlanItem, { id: PLN }));
    expect(top().slice(0, 3)).toEqual(["page-head", "lede", "about"]);
    expect(box.querySelectorAll("h2")).toHaveLength(1);
    expect(box.querySelector(".page-head h2")).not.toBeNull();
    noteOf(firstSentence(PLAN_NOTE), PLAN_NOTE);
    expect(titles().slice(0, 2)).toEqual(["Details", "Evidence"]);
    expect(box.querySelectorAll(":scope > h3")).toHaveLength(0);
    expect([...panel("Details").querySelectorAll(":scope > dl > dt")].map(text)).toContain("Approval");
    expect([...panel("Evidence").querySelectorAll(":scope > dl > dt")].map(text)).toEqual(["Validation", "Dry run", "Change"]);
  });
  test("Operations of this plan is a link in the Details band, the only one", async () => {
    served[`/plans/${PLN}`] = plan("proposed");
    await shown(h(PlanItem, { id: PLN }));
    const links = [...box.querySelectorAll("a")].filter((a) => text(a) === "Operations of this plan");
    expect(links).toHaveLength(1);
    expect(links[0]!.getAttribute("href")).toBe(`/operations?plan=${PLN}`);
    expect(panel("Details").querySelector(":scope > .panel-head .actions")!.contains(links[0]!)).toBe(true);
  });
  test("a proposed plan: Approve is the one filled button, inside the Approval panel with the approval part of the note and its sentence above it", async () => {
    served[`/plans/${PLN}`] = plan("proposed");
    await shown(h(PlanItem, { id: PLN }));
    expect(titles()).toEqual(["Details", "Evidence", "Approval", "Cancel plan"]);
    expect(filled(box)).toEqual(["Approve"]);
    const approval = panel("Approval");
    expect(approval.contains(button(box, "Approve"))).toBe(true);
    expect(approval.querySelector('[data-testid="page-note"]')!.textContent).toBe(PLAN_NOTE_APPROVE);
    expect(box.querySelector("details.about")!.contains(approval.querySelector('[data-testid="page-note"]'))).toBe(false);
    before(approval.querySelector("form p")!, button(approval, "Approve"));
    expect(box.querySelector(".page-head .actions")).toBeNull();
  });
  test("Cancel plan is outlined red, alone in its panel at the end, after Approve", async () => {
    served[`/plans/${PLN}`] = plan("proposed");
    await shown(h(PlanItem, { id: PLN }));
    const cancel = panels().at(-1)!;
    expect(text(cancel.querySelector(":scope > .panel-head h3"))).toBe("Cancel plan");
    expect(cancel.getAttribute("aria-label")).toBe("Cancellation");
    expect(danger(box)).toEqual(["Cancel plan"]);
    expect(buttons(cancel).map(text)).toEqual(["Cancel plan"]);
    expect(cancel.querySelector('form[aria-label="Cancel plan"] input[name="reason"]')).not.toBeNull();
    before(button(box, "Approve"), button(box, "Cancel plan"));
  });
  test("an approved plan with an approval from an earlier recovery entry: Approval, Revoke approval, Cancel plan, in that order, one filled button", async () => {
    Object.assign(served, earlier());
    await shown(h(PlanItem, { id: PLN }));
    expect(titles()).toEqual(["Details", "Evidence", "Approval", "Revoke approval", "Cancel plan"]);
    expect(panels().map((p) => p.getAttribute("aria-label")).slice(2)).toEqual(["Approval", "Revocation", "Cancellation"]);
    expect(filled(box)).toEqual(["Approve"]);
    expect(danger(box)).toEqual(["Revoke approval", "Cancel plan"]);
    expect(panel("Revocation").querySelector('form[aria-label="Revoke approval"]')).not.toBeNull();
    expect(panel("Revocation").querySelector('[data-testid="revoke-limit"]')).not.toBeNull();
    before(button(box, "Approve"), button(box, "Revoke approval"));
    before(button(box, "Revoke approval"), button(box, "Cancel plan"));
  });
  test("the state alerts stay above the first panel, in the order they had", async () => {
    Object.assign(served, earlier());
    await shown(h(PlanItem, { id: PLN }));
    const alert = box.querySelector('[data-testid="plan-old-approval"]')!;
    before(box.querySelector("details.about")!, alert);
    before(alert, panels()[0]!);
  });
  test("a cancelled plan has the two panels and no button at all", async () => {
    served[`/plans/${PLN}`] = plan("cancelled");
    await shown(h(PlanItem, { id: PLN }));
    expect(titles()).toEqual(["Details", "Evidence"]);
    expect(buttons(box).map(text)).toEqual([]);
  });
  test("a viewer's Approve and Cancel plan stay off with their reasons beside them", async () => {
    served[`/plans/${PLN}`] = plan("proposed");
    await shown(h(PlanItem, { id: PLN }), ["viewer"]);
    expect(button(box, "Approve").hasAttribute("disabled")).toBe(true);
    expect(text(box.querySelector('form[aria-label="Approve plan"]'))).toContain("Needs approver");
    expect(button(box, "Cancel plan").hasAttribute("disabled")).toBe(true);
  });
});

describe("the apply page", () => {
  const apply = () => view(`/releases/${REL}/apply`, "", () => {}) as ReactNode;
  const serveMachines = () => Object.assign(served, {
    [`/releases/${REL}`]: release(), [`/machines/${A}`]: mchOf(A), [`/machines/${B}`]: mchOf(B),
    [`/machines/${A}/position`]: positionOf(A, "pln_a"), [`/machines/${B}/position`]: positionOf(B, "pln_b"), "/plans/pln_a": planOf("pln_a", A), "/plans/pln_b": planOf("pln_b", B),
  });
  test("a page header with the title and Back to the release as its action, the note as one line and a disclosure, then the Machines panel", async () => {
    serveMachines();
    await shown(apply());
    expect(top().slice(0, 3)).toEqual(["page-head", "lede", "about"]);
    expect(box.querySelectorAll("h2")).toHaveLength(1);
    expect(text(box.querySelector(".page-head h2"))).toContain("Apply release");
    const back = [...box.querySelectorAll("a")].filter((a) => text(a) === "Back to the release");
    expect(back).toHaveLength(1);
    expect(box.querySelector(".page-head .actions")!.contains(back[0]!)).toBe(true);
    before(back[0]!, box.querySelector("details.about > summary")!);
    noteOf(firstSentence(APPLY_NOTE), APPLY_NOTE);
    expect(titles()).toEqual(["Machines of this release"]);
    expect(panel("The machine list").querySelector('ul[aria-label="Machines of this release"]')).not.toBeNull();
  });
  test("Show apply is outlined and ends the Machines panel, after the rows; the page has no filled button until plans are shown", async () => {
    serveMachines();
    await shown(apply());
    const machines = panel("The machine list");
    const show = button(machines, "Show apply");
    expect(show.className).toBe("outline");
    expect(filled(box)).toEqual([]);
    for (const row of machines.querySelectorAll('[data-testid="apply-row"]')) before(row, show);
    expect(box.querySelector('section[aria-label="Plans to approve"]')).toBeNull();
  });
  test("with plans shown, Apply is the one filled button, at the end of the Plans to approve panel under its sentence", async () => {
    serveMachines();
    await shown(apply());
    for (const m of [A, B]) await act(async () => void box.querySelector<HTMLElement>(`[data-testid="apply-row"][data-machine="${m}"] input[type=checkbox]`)!.click());
    await act(async () => void (await new Promise((r) => setTimeout(r, 40))));
    expect(titles()).toEqual(["Machines of this release", "Plans to approve"]);
    const plans = panel("Plans to approve");
    expect(plans.getAttribute("aria-label")).toBe("Plans to approve");
    expect(filled(box)).toEqual(["Apply"]);
    expect(plans.contains(button(box, "Apply"))).toBe(true);
    before(plans.querySelector('[data-testid="apply-sentence"]')!, button(plans, "Apply"));
    expect(plans.querySelectorAll('[data-testid="apply-plan"]')).toHaveLength(2);
    before(panel("The machine list"), plans);
  });
  test("a release that moved a shared name has its own panel before the Machines panel, with its test id and its note in the body", async () => {
    serveMachines();
    served[`/releases/${REL}`] = release({ sources: [{ kind: "fragment", scope: "library", head: "frg_1", revision: "frv_new", headRevision: null, previous: "frv_old", adoptionRevision: 3 }] });
    await shown(apply());
    expect(titles()).toEqual(["Shared names this release moved", "Machines of this release"]);
    const shared = panel("Shared names this release moved");
    expect(shared.getAttribute("data-testid")).toBe("apply-shared");
    expect(shared.querySelector(":scope > p")).not.toBeNull();
    expect(box.querySelectorAll('[data-testid="apply-shared"]')).toHaveLength(1);
  });
  test("a viewer's Show apply is off with its reason", async () => {
    serveMachines();
    await shown(apply(), ["viewer"]);
    const show = button(box, "Show apply");
    expect(show.hasAttribute("disabled")).toBe(true);
    expect(text(show.parentElement)).toContain("Needs publisher");
  });
});

describe("the drift page", () => {
  const drift = () => view(`/machines/${MCH}/drift`, "", () => {}) as ReactNode;
  const serveOpen = (over: Record<string, unknown> = {}) => Object.assign(served, { [`/machines/${MCH}`]: machine(over) });
  test("a page header with the title and the identifier line, the note as one line and a disclosure, then The change, Freeze, Adopt and Revert", async () => {
    serveOpen();
    await shown(drift());
    expect(top().slice(0, 3)).toEqual(["page-head", "lede", "about"]);
    expect(box.querySelectorAll("h2")).toHaveLength(1);
    expect(text(box.querySelector(".page-head h2"))).toMatch(/^Change on the machine: /);
    expect(text(box.querySelector(".page-head small"))).toContain(MCH);
    expect(box.querySelector(".page-head small button")!.className).toBe("outline");
    expect(titles()).toEqual(["The change", "Freeze", "Adopt", "Revert"]);
    expect(panels().slice(1).map((p) => p.getAttribute("aria-label"))).toEqual(["Freeze", "Adopt", "Revert"]);
  });
  test("the line is the first sentence of the note; the whole note, the open part and the link back are behind the disclosure", async () => {
    serveOpen();
    await shown(drift());
    expect(text(box.querySelector('[data-testid="page-note-line"]'))).toBe(firstSentence(DRIFT_NOTE));
    const whole = text(box.querySelector('details.about [data-testid="page-note"]'));
    expect(whole.startsWith(`${DRIFT_NOTE} ${DRIFT_NOTE_OPEN}`)).toBe(true);
    expect(box.querySelector('details.about [data-testid="page-note"] a')!.getAttribute("href")).toBe(`/machines/${MCH}`);
  });
  test("The change holds the record's lines, and none of them is loose above the first panel", async () => {
    serveOpen();
    await shown(drift());
    const change = panel("The change");
    for (const id of ["drift-record", "drift-late", "drift-observed", "drift-not-shown"]) expect(change.querySelector(`[data-testid="${id}"]`), id).not.toBeNull();
    expect(top().slice(0, 4)).toEqual(["page-head", "lede", "about", "panel"]);
  });
  test("a machine with no open change says so inside The change and offers Freeze alone", async () => {
    serveOpen({ openDrift: null });
    await shown(drift());
    expect(titles()).toEqual(["The change", "Freeze"]);
    expect(panel("The change").querySelector('[data-testid="drift-none"]')).not.toBeNull();
  });
  test("the page has no filled button: Freeze is outlined, and Start adoption and Plan revert are outlined links", async () => {
    serveOpen();
    await shown(drift());
    expect(filled(box)).toEqual([]);
    expect(button(panel("Freeze"), "Freeze").className).toBe("outline");
    const adopt = [...panel("Adopt").querySelectorAll("a")].find((a) => text(a) === "Start adoption")!;
    const revert = [...panel("Revert").querySelectorAll("a")].find((a) => text(a) === "Plan revert")!;
    expect(adopt.className).toBe("button outline");
    expect(revert.className).toBe("button outline");
  });
  test("a frozen machine offers Unfreeze outlined, in the Freeze panel, with the frozen alert before that panel", async () => {
    serveOpen({ frozen: true });
    await shown(drift());
    expect(button(panel("Freeze"), "Unfreeze").className).toBe("outline");
    expect(panel("Freeze").querySelector('form[aria-label="Unfreeze"]')).not.toBeNull();
    before(box.querySelector('[data-testid="drift-frozen"]')!, panel("Freeze"));
  });
  test("a viewer's controls are off with their reasons, as a disabled outlined button where a link was", async () => {
    serveOpen();
    await shown(drift(), ["viewer"]);
    const start = button(panel("Adopt"), "Start adoption");
    expect(start.hasAttribute("disabled")).toBe(true);
    expect(start.className).toBe("outline");
    expect(text(panel("Adopt"))).toContain("Needs author");
  });
});

describe("the operation page", () => {
  test("Details and Attempts are panels after the Timeline, with no loose definition list or heading left", async () => {
    served[`/operations/${OP}`] = operation();
    await shown(h(OperationItem, { id: OP }));
    expect(box.children[0]!.classList.contains("page-head")).toBe(true);
    expect(box.children[1]!.getAttribute("data-testid")).toBe("page-note");
    expect(box.querySelector("details.about")).toBeNull();
    expect(text(box.querySelector('[data-testid="page-note"]'))).toBe(OPERATION_NOTE);
    expect(titles()).toEqual(["Details", "Attempts"]);
    expect(box.querySelectorAll(":scope > dl, :scope > h3")).toHaveLength(0);
    expect([...panel("Details").querySelectorAll(":scope > dl > dt")].map(text)).toEqual(["Kind", "State", "Subject", "Created", "Epoch", "Last event", "Result", "Error"]);
    expect(panel("Attempts").querySelector('.scroll[aria-label="Attempts"] table')).not.toBeNull();
    before(box.querySelector('section[aria-label="Timeline"]')!, panel("Details"));
  });
  test("no attempt says so inside the Attempts panel", async () => {
    served[`/operations/${OP}`] = operation({ attempts: [] });
    await shown(h(OperationItem, { id: OP }));
    expect(text(panel("Attempts"))).toContain("No attempt recorded.");
    expect(panel("Attempts").querySelector("table")).toBeNull();
  });
  test("a publish or import refusal's reference stays in the Details panel, in place of the Error row", async () => {
    served[`/operations/${OP}`] = operation({ state: "failed", error: { type: "urn:bronzeward:problem:conflict", title: "Conflict", status: 409, detail: "the draft changed" } });
    await shown(h(OperationItem, { id: OP }));
    const details = panel("Details");
    expect([...details.querySelectorAll(":scope > dl > dt")].map(text)).not.toContain("Error");
    expect(details.querySelector('[data-testid="request-ref"]') ?? details.querySelector("details")).not.toBeNull();
  });
});

describe("no table of these pages carries a hidden header word, and a long value stays inside its panel", () => {
  test("every table, with a row in each: no action column, and the scroll region holds it", async () => {
    served[`/operations/${OP}`] = operation();
    await shown(h(OperationItem, { id: OP }));
    expect(box.querySelectorAll("table").length).toBeGreaterThan(0);
    expect(box.querySelectorAll("table .sr-only, th.action, td.action")).toHaveLength(0);
    for (const t of box.querySelectorAll("table")) expect(t.parentElement!.classList.contains("scroll")).toBe(true);
  });
  // A plan, operation or epoch identifier has no space to wrap at. Measured in a browser it is the 320 px cases of the plan and operation pages
  // (web/e2e/auth/approval.e2e.ts, operation.e2e.ts); here the markup that holds the value and the rules that let it wrap are pinned.
  const LONG = `x${"k2pq3rstuvwxyz234567abcdef".repeat(8)}`;
  test("a long value sits in a definition of the panel's own list, and the stylesheet wraps a definition anywhere inside a one-flexible-column grid", async () => {
    Object.assign(served, { [`/plans/${PLN}`]: plan("proposed", { assignmentRevision: `asr_${LONG}`, driftRecord: LONG }), [`/operations/${OP}`]: operation({ epoch: LONG, lastEvent: LONG }) });
    await shown(h(PlanItem, { id: PLN }));
    const bound = [...panel("Details").querySelectorAll(":scope > dl > dd")].find((d) => d.textContent!.includes(LONG))!;
    expect(bound).toBeDefined();
    await shown(h(OperationItem, { id: OP, key: "op" }));
    const epoch = [...panel("Details").querySelectorAll(":scope > dl > dd")].filter((d) => d.textContent!.includes(LONG));
    expect(epoch.length).toBeGreaterThanOrEqual(2);
    const css = readFileSync(`${import.meta.dirname}/app.css`, "utf8");
    expect(css).toMatch(/^p, dd, li, h2, h3 \{ overflow-wrap: anywhere; \}/m);
    expect(css).toMatch(/^dl \{[^}]*grid-template-columns: max-content minmax\(0, 1fr\);/m);
  });
});
