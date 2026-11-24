// @vitest-environment happy-dom
// The waiting line's own reads, mounted (ginsys/bronzeward#1149). On the machine page it reads the plan's release review under
// the page's three counted reads, and a fourth settle there starts the next generation of reads before the observations answer,
// so the review settles through the shell's own settle and not the counted one. On the plan page the position is read again at
// each answer of the plan's poll, so a holder that released the name stops being named, and a lookup that failed is tried again.
// Every value is synthetic.

import { act, createElement as h } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { HostWaits, PlanHostWaits } from "./host-waits.tsx";
import { type Reads, ReadsProvider } from "./list.tsx";
import type { Position } from "./nextstep.tsx";
import type { ReadResult } from "./request.ts";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

type Plan = NonNullable<Position["plan"]>;
const at = "2026-11-18T10:00:00Z";
const plan = { id: "pln_1", state: "approved", operation: "apply-config", release: "rel_a", approvalCurrent: true, createdAt: at, expiresAt: at } as Plan;
const machine = { id: "mch_b", cluster: "cl_a", name: "node-b", talosEndpoint: "10.0.0.2:50000", frozen: false, openDrift: null, scopeState: "normal" } as Position["machine"];
const position = (holder: boolean): Position =>
  ({ machine, ingestion: null, draft: null, plan, operation: null, rolloutHeldBy: null, hostnameHeldBy: holder ? { id: "mch_a", name: "node-a" } : null, step: "awaiting-dispatch" }) as Position;

let root: Root;
let box: HTMLElement;
// What the position read answers next, and every path read.
let answer: "holder" | "none" | "failed";
let paths: string[];
let counted: number;
let shell: number;
const settle = (kind: "counted" | "shell"): Reads["settle"] => (res, set, live, held) => {
  if (kind === "counted") counted++;
  else shell++;
  if (res.kind !== "ok" && held()) return true;
  if (live()) set(res);
  return false;
};
const reads = (again: number, withShell: boolean): Reads => ({
  client: {
    read: async <T,>(path: string) => {
      paths.push(path);
      if (path.endsWith("/position")) {
        return (answer === "failed" ? { kind: "refused", status: 500, problem: null } : { kind: "ok", data: position(answer === "holder") }) as ReadResult<T>;
      }
      return { kind: "ok", data: { hostname: { nodeName: "node-a" } } } as ReadResult<T>;
    },
  },
  settle: settle("counted"), ...(withShell ? { shell: settle("shell") } : {}),
  undropped: () => () => true, view: "v", again, regained: 0, go: () => {}, failure: () => "FAILED",
});
// One Reads object per test and kind, as the shell holds one between renders: a fresh one on each render would read again by itself.
let held: Reads | undefined;
const mount = (again: number, withShell: boolean, el: ReturnType<typeof h>) => act(async () => root.render(h(ReadsProvider, { value: (held ??= reads(again, withShell)) }, el)));
const settled = () => act(async () => void (await new Promise((r) => setTimeout(r, 40))));
const line = () => box.querySelector('[data-testid="host-waits"]')?.textContent;

beforeEach(() => {
  answer = "holder";
  held = undefined;
  paths = [];
  counted = 0;
  shell = 0;
  box = document.createElement("div");
  document.body.append(box);
  root = createRoot(box);
});
afterEach(async () => {
  await act(async () => root.unmount());
  box.remove();
});

describe("the machine page's review read", () => {
  test("settles through the shell's settle, outside the page's counted reads", async () => {
    await mount(0, true, h(HostWaits, { position: position(true) }));
    await settled();
    expect(paths).toEqual(["/releases/rel_a/machines/mch_b/review"]);
    expect(counted).toBe(0);
    expect(shell).toBe(1);
    expect(line()).toContain("This plan waits. The last release applied to machine node-a records the Kubernetes node name node-a");
  });
  test("a page with no counting settles it as every read does", async () => {
    await mount(0, false, h(HostWaits, { position: position(true) }));
    await settled();
    expect(counted).toBe(1);
    expect(line()).toContain("node name node-a");
  });
});

describe("the plan page's position read", () => {
  test("is read again at each answer of the plan's poll: a holder that released the name stops being named", async () => {
    await mount(0, false, h(PlanHostWaits, { plan: "pln_1", machine: "mch_b", answers: 0 }));
    await settled();
    expect(line()).toContain("node name node-a");
    answer = "none";
    await mount(0, false, h(PlanHostWaits, { plan: "pln_1", machine: "mch_b", answers: 1 }));
    await settled();
    expect(line()).toBeUndefined();
    expect(paths.filter((p) => p.endsWith("/position")).length).toBe(2);
  });
  test("a lookup that failed is tried again at the next answer", async () => {
    answer = "failed";
    await mount(0, false, h(PlanHostWaits, { plan: "pln_1", machine: "mch_b", answers: 0 }));
    await settled();
    expect(line()).toBeUndefined();
    answer = "holder";
    await mount(0, false, h(PlanHostWaits, { plan: "pln_1", machine: "mch_b", answers: 1 }));
    await settled();
    expect(line()).toContain("node name node-a");
  });
  test("with no new answer the position is not read again", async () => {
    await mount(0, false, h(PlanHostWaits, { plan: "pln_1", machine: "mch_b", answers: 3 }));
    await settled();
    await mount(0, false, h(PlanHostWaits, { plan: "pln_1", machine: "mch_b", answers: 3 }));
    await settled();
    expect(paths.filter((p) => p.endsWith("/position")).length).toBe(1);
  });
});
