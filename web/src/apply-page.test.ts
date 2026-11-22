// @vitest-environment happy-dom
// The apply page mounted (ginsys/bronzeward#1096): an approval the server refuses for one machine says so in that
// machine's row, with the server's answer and what to press next, and leaves the other machine's row as it was.

import { act, createElement as h } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { LINES } from "./apply.ts";
import { ApplyForm } from "./apply.tsx";
import { WritesProvider } from "./forms.tsx";
import { type Reads, ReadsProvider } from "./list.tsx";
import { ShellContext } from "./shell.tsx";
import { Writes } from "./write.ts";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const REL = "rel_r";
const A = "mch_a";
const B = "mch_b";
const T0 = "2026-11-22T08:00:00Z";
const machine = (id: string) => ({ id, cluster: "cls_c", name: id, role: "worker", platform: "metal", desired: REL, applied: null, pendingConvergence: true, formOnly: false, frozen: false, openDrift: null });
const position = (id: string, plan: string) => ({
  machine: machine(id), ingestion: null, draft: null, operation: null, rolloutHeldBy: null, step: "awaiting-approval",
  plan: { id: plan, state: "proposed", operation: "apply-config", release: REL, approvalCurrent: null, createdAt: T0, expiresAt: "2026-11-23T08:00:00Z" },
});
const plan = (id: string, m: string) => ({ id, machine: m, release: REL, operation: "apply-config", createdBy: { principal: "prn_x", role: "publisher" }, createdAt: T0, evidence: { diff: null, validation: null, dryRun: null, baseline: null } });
const release = { id: REL, sources: [], machines: [{ machine: A }, { machine: B }] };

describe("the apply page's refused approval", () => {
  let box: HTMLDivElement;
  let root: Root;
  beforeEach(() => {
    box = document.createElement("div");
    document.body.append(box);
    root = createRoot(box);
  });
  afterEach(async () => {
    await act(async () => root.unmount());
    box.remove();
  });

  test("the refused machine says nothing was approved and what to press, and the other machine's row is unchanged", async () => {
    const served: Record<string, unknown> = {
      [`/machines/${A}`]: machine(A), [`/machines/${B}`]: machine(B),
      [`/machines/${A}/position`]: position(A, "pln_a"), [`/machines/${B}/position`]: position(B, "pln_b"),
      "/plans/pln_a": plan("pln_a", A), "/plans/pln_b": plan("pln_b", B),
    };
    const reads = {
      client: { read: vi.fn(async (path: string) => (path in served ? { kind: "ok", data: served[path], etag: '"1"' } : { kind: "refused", status: 404, problem: null })) },
      settle: (res: unknown, set: (r: unknown) => void, live: () => boolean) => (live() && set(res), true),
      undropped: () => () => true,
      view: "", again: 0, regained: 0, go: () => {}, failure: () => null,
    } as unknown as Reads;
    const writes = new Writes(() => "p");
    const sent: string[] = [];
    vi.spyOn(writes, "submit").mockImplementation(async (w) => {
      sent.push(w.spec.path);
      w.outcome = w.spec.path === "/plans/pln_a/approvals" ? { kind: "refused", status: 409, problem: { type: "urn:bronzeward:problem:conflict", title: "Conflict", status: 409, detail: "the plan changed", instance: "req-1" }, replayed: false } : { kind: "done", status: 201, data: { id: "apr_1" }, replayed: false };
      return w.outcome;
    });
    const settled = () => act(async () => void (await new Promise((r) => setTimeout(r, 40))));
    await act(async () =>
      root.render(h(WritesProvider, { value: writes }, h(ShellContext, { value: { roles: ["author", "publisher", "approver"], offline: false } }, h(ReadsProvider, { value: reads }, h(ApplyForm, { release: REL, r: release as never }))))),
    );
    await settled();
    const row = (m: string) => box.querySelector(`[data-testid="apply-row"][data-machine="${m}"]`)!;
    for (const m of [A, B]) await act(async () => void row(m).querySelector<HTMLInputElement>("input[type=checkbox]")!.click());
    await settled();
    const before = row(B).textContent;
    await act(async () => void [...box.querySelectorAll("button")].find((b) => b.textContent === "Apply")!.click());
    await settled();
    expect(sent).toEqual(["/plans/pln_a/approvals", "/plans/pln_b/approvals"]);
    expect(row(A).textContent).toContain("This is in a state that does not allow the change.");
    expect(row(A).textContent).toContain(LINES.afterRefusal);
    expect(row(A).textContent).toContain("Press Show apply");
    expect(row(B).textContent).not.toContain("Nothing was approved for this machine.");
    expect(row(B).textContent).toBe(before);
  });

  // The person leaves the page while a plan is being made: the request is answered after the page is gone, and the
  // plan, which this page made and nobody saw, is cancelled by that answer. The plan a publisher prepared for B is not.
  test("a plan whose creation is answered after the person left the page is cancelled", async () => {
    const served: Record<string, unknown> = {
      [`/machines/${A}`]: machine(A), [`/machines/${B}`]: machine(B),
      [`/machines/${A}/position`]: { ...position(A, "pln_x"), step: "needs-plan", plan: null }, [`/machines/${B}/position`]: position(B, "pln_b"),
      "/plans/pln_b": plan("pln_b", B),
    };
    const reads = {
      client: { read: vi.fn(async (path: string) => (path in served ? { kind: "ok", data: served[path], etag: '"1"' } : { kind: "refused", status: 404, problem: null })) },
      settle: (res: unknown, set: (r: unknown) => void, live: () => boolean) => (live() && set(res), true),
      undropped: () => () => true,
      view: "", again: 0, regained: 0, go: () => {}, failure: () => null,
    } as unknown as Reads;
    const writes = new Writes(() => "p");
    const sent: string[] = [];
    let answer: () => void = () => {};
    const gate = new Promise<void>((r) => (answer = r));
    vi.spyOn(writes, "submit").mockImplementation(async (w) => {
      sent.push(w.spec.path);
      if (w.spec.path === "/plans") await gate;
      w.outcome = { kind: "done", status: 201, data: { id: "pln_new" }, replayed: false };
      return w.outcome;
    });
    const settled = () => act(async () => void (await new Promise((r) => setTimeout(r, 40))));
    await act(async () =>
      root.render(h(WritesProvider, { value: writes }, h(ShellContext, { value: { roles: ["author", "publisher", "approver"], offline: false } }, h(ReadsProvider, { value: reads }, h(ApplyForm, { release: REL, r: release as never }))))),
    );
    await settled();
    const row = (m: string) => box.querySelector(`[data-testid="apply-row"][data-machine="${m}"]`)!;
    for (const m of [A, B]) await act(async () => void row(m).querySelector<HTMLInputElement>("input[type=checkbox]")!.click());
    await settled();
    await act(async () => void [...box.querySelectorAll("button")].find((b) => b.textContent === "Show apply")!.click());
    await settled();
    expect(sent).toEqual(["/plans"]);
    await act(async () => root.unmount());
    answer();
    await new Promise((r) => setTimeout(r, 40));
    expect(sent).toEqual(["/plans", "/plans/pln_new/cancellations"]);
  });
});
