// @vitest-environment happy-dom
// The release review page's position read, through the page's own hook (ginsys/bronzeward#1077): a failed
// read shows the failure and [Read again]; pressing it reads the position again, and the answer brings the
// sentence and the plan button back. The static renders of review.test.ts cannot press a button.

import { act, createElement as h } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { type Reads, ReadsProvider } from "./list.tsx";
import type { Position } from "./nextstep.tsx";
import type { ReadResult } from "./request.ts";
import { ReviewItem } from "./review.tsx";
import { ShellContext } from "./shell.tsx";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const rel = "rel_a";
const machine = {
  id: "mch_b", cluster: "cl_a", name: null, hardware: { smbiosUuid: null, talosNodeId: "node-a", serial: null }, platform: "metal", talosEndpoint: "10.5.0.2:50000",
  desired: rel, applied: { release: "rel_old", source: "operation", importBase: null }, pendingConvergence: true,
  observed: null, frozen: false, scopeState: "normal", openDrift: null,
};
const position = (step: string) => ({ machine, ingestion: null, draft: null, plan: null, operation: null, rolloutHeldBy: null, step }) as unknown as Position;

let root: Root;
let box: HTMLElement;
// What the position's path answers now, and how many times it was read.
let answer: ReadResult<unknown>;
let reads = 0;

const client: Reads["client"] = {
  read: async <T,>(path: string) => {
    if (path.endsWith("/position")) {
      reads += 1;
      return answer as ReadResult<T>;
    }
    return { kind: "ok", data: { release: rel, machine: "mch_b", mode: "metal", configuration: "a: b", provenance: [] } } as ReadResult<T>;
  },
};
const settle: Reads["settle"] = (res, set, live) => {
  if (live()) set(res);
  return false;
};
const value: Reads = { client, settle, undropped: () => () => true, view: "v", again: 0, regained: 0, go: () => {}, failure: () => "FAILED" };

const mount = () =>
  act(async () => {
    root.render(h(ShellContext, { value: { roles: ["publisher"], offline: false } }, h(ReadsProvider, { value }, h(ReviewItem, { release: rel, machine: "mch_b" }))));
  });
const settled = () => act(async () => void (await new Promise((r) => setTimeout(r, 20))));
const text = () => box.textContent ?? "";
const button = (label: string) => [...box.querySelectorAll("button")].find((b) => b.textContent === label);

beforeEach(() => {
  box = document.createElement("div");
  document.body.append(box);
  root = createRoot(box);
  reads = 0;
});
afterEach(async () => {
  await act(async () => root.unmount());
  box.remove();
});

describe("the release review page's position read", () => {
  test("[Read again] after a failed position read reads it again, and the answer brings back the sentence and the plan button", async () => {
    answer = { kind: "unavailable", status: 503, problem: null };
    await mount();
    await settled();
    expect(reads).toBe(1);
    expect(text()).toContain("FAILED");
    expect(box.querySelector('[data-testid="next-step"]')).toBeNull();
    expect(button("Plan apply")).toBeUndefined();

    answer = { kind: "ok", data: position("needs-plan") };
    await act(async () => button("Read again")!.click());
    await settled();
    expect(reads).toBe(2);
    expect(box.querySelector('[data-testid="next-step"]')?.textContent).toContain("An apply-config plan sends this release to it.");
    expect(button("Plan apply")).toBeDefined();
    expect(button("Read again")).toBeUndefined();
  });

  test("a position answered with an operation in flight draws its line and no plan button", async () => {
    answer = { kind: "ok", data: { ...position("operation-in-flight"), operation: { id: "op_1", state: "sending" } } };
    await mount();
    await settled();
    expect(box.querySelector('[data-testid="next-step"]')?.textContent).toContain("An operation is changing this machine (sending)");
    expect(button("Plan apply")).toBeUndefined();
    expect(text()).not.toMatch(/nothing has been sent yet/i);
  });
});
