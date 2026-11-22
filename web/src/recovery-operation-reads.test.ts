// @vitest-environment happy-dom
// The recovery page of one operation, mounted: each of its two controls against each state of the events read, which carry the owner
// generation take over sends (ginsys/bronzeward#365). Take over waits while that read is pending, the first time and again after an
// answer, and a failed read leaves it on with its note; Resolve as cancelled reads only the operation and never waits. Every value is synthetic.

import { act, createElement as h } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { WritesProvider } from "./forms.tsx";
import { type Reads, ReadsProvider } from "./list.tsx";
import type { ReadResult } from "./request.ts";
import { EVENTS_LOADING, RecoveryOperation } from "./recovery-operation.tsx";
import { ShellContext } from "./shell.tsx";
import { Writes } from "./write.ts";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const OP = "op_k2pq3rstuvwxyz234567abcdefg";
const operation = { id: OP, kind: "apply-config", state: "unresolved", epoch: "ep_a", lastEvent: 1, subject: {}, attempts: [], createdBy: null, createdAt: "2026-11-22T10:00:00Z" };

let root: Root;
let box: HTMLElement;
// What the events read answers: nothing yet, a list, or a refusal.
let events: "pending" | "answered" | "failed";
const settle: Reads["settle"] = (res, set, live, held) => {
  if (res.kind !== "ok" && held()) return true;
  if (live()) set(res);
  return false;
};
const reads = (again: number): Reads => ({
  client: {
    read: async <T,>(path: string) => {
      if (path.split("?")[0] === `/operations/${OP}/events`) {
        if (events === "pending") return new Promise<ReadResult<T>>(() => {});
        return (events === "failed" ? { kind: "refused", status: 500, problem: null } : { kind: "ok", data: { items: [] } }) as ReadResult<T>;
      }
      return (path === `/operations/${OP}` ? { kind: "ok", data: operation } : { kind: "refused", status: 404, problem: null }) as ReadResult<T>;
    },
  },
  settle, undropped: () => () => true, view: "v", again, regained: 0, go: () => {}, failure: () => "FAILED",
});
const page = (again: number) =>
  h(ReadsProvider, { value: reads(again) }, h(WritesProvider, { value: new Writes(() => "p") }, h(ShellContext, { value: { roles: ["recovery-admin"], offline: false } }, h(RecoveryOperation, { id: OP }))));
const render = (again: number) => act(async () => root.render(page(again)));
const settled = () => act(async () => void (await new Promise((r) => setTimeout(r, 60))));

type Control = { off: boolean; why: string | undefined; reason: boolean };
const control = (label: string): Control => {
  const form = box.querySelector(`form[aria-label="${label}"]`)!;
  const route = label === "Take over" ? "takeovers" : "resolutions";
  return {
    off: (form.querySelector("button") as HTMLButtonElement).disabled,
    why: box.querySelector(`[data-testid="${route}-off"]`)?.textContent ?? undefined,
    reason: (form.querySelector('input[name="reason"]') as HTMLInputElement).disabled,
  };
};
const note = () => box.querySelector('[data-testid="recovery-operation-events"]')?.textContent;

beforeEach(() => {
  box = document.createElement("div");
  document.body.append(box);
  root = createRoot(box);
});
afterEach(async () => {
  await act(async () => root.unmount());
  box.remove();
});

// Each row: how the events read stands, then Take over and Resolve as cancelled, and the note about the read.
describe("the controls against the state of the events read", () => {
  const ROWS: [string, () => Promise<void>, { takeOver: "off" | "on"; note: "none" | "unread" }][] = [
    ["the first read is pending", async () => {
      events = "pending";
      await render(0);
      await settled();
    }, { takeOver: "off", note: "none" }],
    ["a second read is pending after an answer", async () => {
      events = "answered";
      await render(0);
      await settled();
      events = "pending";
      await render(1);
      await settled();
    }, { takeOver: "off", note: "none" }],
    ["the read failed", async () => {
      events = "failed";
      await render(0);
      await settled();
    }, { takeOver: "on", note: "unread" }],
    ["the read answered", async () => {
      events = "answered";
      await render(0);
      await settled();
    }, { takeOver: "on", note: "none" }],
  ];
  test.each(ROWS)("%s", async (_, arrange, want) => {
    await arrange();
    const take = control("Take over");
    const resolve = control("Resolve as cancelled");
    expect(take.off).toBe(want.takeOver === "off");
    expect(take.reason).toBe(want.takeOver === "off");
    expect(take.why).toBe(want.takeOver === "off" ? EVENTS_LOADING : undefined);
    // Resolve reads the operation's own state and attempts, so it is on in every row.
    expect(resolve).toEqual({ off: false, why: undefined, reason: false });
    if (want.note === "none") expect(note()).toBeUndefined();
    else expect(note()).toContain("were not read");
  });
});
