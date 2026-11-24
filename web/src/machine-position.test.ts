// @vitest-environment happy-dom
// The machine page's position read, through the page's own hook (ginsys/bronzeward#891): the other
// tests draw the line from `positionShown` and `MachineShown` by hand, so a change that stops the
// page from reading the position, or from handing its answer on, passes them all. This file mounts
// the exported `MachineItem` in a DOM and reads the next-step line it draws. It is the only test
// that runs in this environment; the suite's default stays `node`.

import { act, createElement as h } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { WritesProvider } from "./forms.tsx";
import { type Reads, ReadsProvider } from "./list.tsx";
import { MachineItem } from "./machine.tsx";
import type { Machine } from "./machines.tsx";
import type { Position } from "./nextstep.tsx";
import type { ReadResult } from "./request.ts";
import { ShellContext } from "./shell.tsx";
import { Writes } from "./write.ts";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const machine = {
  id: "mch_a", cluster: "cl_a", name: null, hardware: { smbiosUuid: null, talosNodeId: "node-a", serial: null }, platform: "metal", talosEndpoint: "10.5.0.2:50000",
  desired: "rel_new", applied: { release: "rel_new", source: "operation", importBase: null }, pendingConvergence: false,
  observed: { observation: "obs_a", at: "2026-10-26T09:46:00Z", release: "rel_new" }, frozen: false, scopeState: "normal", openDrift: null,
} as unknown as Machine;
// An approved plan on a machine whose scope is `normal`: in recovery mode only a released scope admits a plan.
const position = {
  machine, ingestion: null, draft: null, operation: null, rolloutHeldBy: null, step: "awaiting-dispatch",
  plan: { id: "pln_1", state: "approved", operation: "adopt", release: "rel_new", approvalCurrent: true, createdAt: "2026-10-26T09:46:00Z", expiresAt: "2026-10-26T10:46:00Z" },
} as unknown as Position;
const PLAN_APPROVED = "A plan is approved and its approval is current.";
const IN_RECOVERY = "the server is in recovery mode";

const ok = (data: unknown, extra: { recoveryMode?: true } = {}): ReadResult<unknown> => ({ kind: "ok", data, ...extra });

let root: Root;
let box: HTMLElement;
// What the position's path answers now; every other path of the page answers at once.
let answer: ReadResult<unknown>;
// What the position's plan reads as, and whether its read answers at all.
let planRead: ReadResult<unknown>;
const prepared = { id: "pln_1", createdBy: { principal: "prn_cp3aaaaaaaaaaaaaaaaaaaaaaaa", role: "publisher" }, createdAt: "2026-10-26T09:40:00Z" };

const client: Reads["client"] = {
  read: async <T,>(path: string) =>
    (path.endsWith("/position") ? answer
      : path.startsWith("/plans/") ? planRead
      : path.includes("/observations") || path.includes("/timeline") ? ok({ items: [] })
      : ok(machine)) as ReadResult<T>,
};
// Renders what the shell would: an answer is shown, a read without one is shown only while the view keeps its data.
const settle: Reads["settle"] = (res, set, live, held) => {
  if (res.kind !== "ok" && held()) return true;
  if (live()) set(res);
  return false;
};
const reads = (again: number): Reads => ({ client, settle, undropped: () => () => true, view: "v", again, regained: 0, go: () => {}, failure: () => "FAILED" });

const mount = (again: number) =>
  act(async () => {
    root.render(h(WritesProvider, { value: new Writes(() => "p") }, h(ShellContext, { value: { roles: ["author"], offline: false } },
      h(ReadsProvider, { value: reads(again) }, h(MachineItem, { id: "mch_a" })))));
  });
// The reads of the page and the answers they set run on promises: let them all run.
const settled = () => act(async () => void (await new Promise((r) => setTimeout(r, 20))));
const line = () => box.querySelector('[data-testid="next-step"]')?.textContent ?? null;

beforeEach(() => {
  planRead = ok(prepared);
  box =document.createElement("div");
  document.body.append(box);
  root = createRoot(box);
});
afterEach(async () => {
  await act(async () => root.unmount());
  box.remove();
});

// A plan nobody on this page can name the ID of (a creation whose outcome was unknown) is the position's plan: the
// machine's page says who prepared it and when, from the plan's own read (ginsys/bronzeward#1159, item 2).
describe("the machine page says who prepared the position's plan", () => {
  const by = () => box.querySelector('[data-testid="next-step-by"]')?.textContent ?? null;
  test("a waiting plan shows its preparer and time", async () => {
    answer = ok({ ...position, step: "awaiting-approval" });
    await mount(0);
    await settled();
    expect(by()).toMatch(/^Prepared by .+ on .+\./);
  });
  test("a plan read that fails draws nothing, and the line of the position is still there", async () => {
    answer = ok({ ...position, step: "awaiting-approval" });
    planRead = { kind: "transport" };
    await mount(0);
    await settled();
    expect(by()).toBeNull();
    expect(line()).toContain("A plan waits for approval.");
  });
  test("a position with no plan, or an operation in flight, shows none", async () => {
    planRead = ok(prepared);
    answer = ok({ ...position, step: "operation-in-flight", plan: null, operation: { id: "op_1", kind: "apply-config", state: "sending", plan: "pln_1", createdAt: "2026-10-26T09:46:00Z" } });
    await mount(0);
    await settled();
    expect(by()).toBeNull();
  });
});

describe("ginsys/bronzeward#891: the machine page draws its next-step line from its own position read", () => {
  test("an answered read shows the position's line, outside recovery mode", async () => {
    answer = ok(position);
    await mount(0);
    await settled();
    expect(line()).toContain(PLAN_APPROVED);
    expect(line()).not.toContain("recovery mode");
  });

  test("an answer served in recovery mode shows the recovery wording from that answer", async () => {
    answer = ok(position, { recoveryMode: true });
    await mount(0);
    await settled();
    expect(line()).toContain(IN_RECOVERY);
  });

  test("a read that finds no answer draws no line, not the earlier one", async () => {
    answer = ok(position);
    await mount(0);
    await settled();
    expect(line()).toContain(PLAN_APPROVED);
    answer = { kind: "transport" };
    await mount(1);
    await settled();
    expect(line()).toBeNull();
  });
});
