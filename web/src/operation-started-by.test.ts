// @vitest-environment happy-dom
// Who started an operation (client contracts A10, ginsys/bronzeward#998): the operations list and page
// name the principal for a publish or an import, and for an apply or an adoption, which the controller
// creates itself, the controller, the plan it committed and who created that plan; never "none".

import { act, createElement as h, type ReactElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { type Reads, ReadsProvider } from "./list.tsx";
import type { ReadResult } from "./request.ts";
import { OperationItem, Operations, StartedBy } from "./resources.tsx";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const ok = (data: unknown): ReadResult<unknown> => ({ kind: "ok", data });
const missing: ReadResult<unknown> = { kind: "refused", status: 404, problem: null };
const at = "2026-11-19T10:00:00Z";
const MCH = "mch_k2pq3rstuvwxyz234567abcdef";
const DRF = "drf_k2pq3rstuvwxyz234567abcdef";
const REL = "rel_k2pq3rstuvwxyz234567abcdef";
const PLN = "pln_k2pq3rstuvwxyz234567abcdef";
const OP = "op_k2pq3rstuvwxyz234567abcdefg";
const creator = { principal: "usr_creator", role: "publisher" };
const plan = (operation: string) => ({ id: PLN, state: "approved", cluster: "cl_a", machine: MCH, release: REL, operation, createdBy: creator });
const operation = (kind: string, createdBy: unknown, subject: unknown) => ({ id: OP, kind, state: "completed", epoch: "ep_a", lastEvent: 1, subject, createdBy, createdAt: at, result: null, error: null });

let root: Root;
let box: HTMLElement;
let answers: Record<string, ReadResult<unknown>>;

const settle: Reads["settle"] = (res, set, live, held) => {
  if (res.kind !== "ok" && held()) return true;
  if (live()) set(res);
  return false;
};
const reads = (): Reads => ({
  client: { read: async <T,>(path: string) => (answers[path] ?? missing) as ReadResult<T> },
  settle, undropped: () => () => true, view: "v", again: 0, regained: 0, go: () => {}, failure: () => "FAILED",
});
const mount = (view: ReactElement) => act(async () => root.render(h(ReadsProvider, { value: reads() }, view)));
const settled = () => act(async () => void (await new Promise((r) => setTimeout(r, 60))));
const text = () => box.textContent;
const started = async (o: unknown) => {
  await mount(h(StartedBy, { o: o as never }));
  await settled();
  return text();
};

beforeEach(() => {
  box = document.createElement("div");
  document.body.append(box);
  root = createRoot(box);
  answers = {
    [`/machines/${MCH}`]: ok({ id: MCH, name: "worker-1" }),
    [`/drafts/${DRF}`]: ok({ id: DRF, title: "Add the office labels" }),
    [`/releases/${REL}`]: ok({ id: REL, cluster: "cl_a", draft: DRF, draftRevision: 3, operation: OP, publishedBy: null, publishedAt: at, renderer: {}, sources: [], machines: [] }),
    [`/plans/${PLN}`]: ok(plan("apply-config")),
  };
});
afterEach(async () => {
  await act(async () => root.unmount());
  box.remove();
  localStorage.clear();
  sessionStorage.clear();
});

// The list and the page use it: a call site that went back to the raw `createdBy` would print none here.
describe("the operations list and the operation page", () => {
  const publish = { ...operation("publish", { principal: "usr_a", role: "publisher" }, { draft: DRF, draftRevision: 3 }), id: "op_pub" };
  const apply = { ...operation("apply-config", null, { plan: PLN, machine: MCH }), id: "op_apply" };
  const adopt = { ...operation("adopt", null, { plan: PLN, machine: MCH }), id: "op_adopt" };

  test("the list's Started by column names the controller and the plan for an apply and an adoption", async () => {
    answers["/operations"] = ok({ items: [publish, apply, adopt] });
    await mount(h(Operations, { query: "", go: () => {} }));
    await settled();
    const rows = [...box.querySelectorAll("tbody tr")].map((tr) => [...tr.children].map((c) => c.textContent ?? ""));
    expect([...box.querySelectorAll("th")].map((th) => th.textContent)).toContain("Started by");
    expect([...box.querySelectorAll("th")].map((th) => th.textContent)).not.toContain("Created by");
    const startedCol = [...box.querySelectorAll("th")].findIndex((th) => th.textContent === "Started by");
    expect(rows.map((r) => r[startedCol])).toEqual([
      expect.stringMatching(/\(publisher\)$/),
      expect.stringMatching(/^the controller, committing plan Apply of worker-1 to .*\(created by .*\(publisher\)\)$/),
      expect.stringMatching(/^the controller, adopting under plan /),
    ]);
  });

  test("the page's Created row names the controller and the plan, and never none", async () => {
    answers["/operations/op_apply"] = ok(apply);
    await mount(h(OperationItem, { id: "op_apply" }));
    await settled();
    const created = [...box.querySelectorAll("dt")].find((dt) => dt.textContent === "Created")?.nextElementSibling?.textContent;
    expect(created).toMatch(/ by the controller, committing plan Apply of worker-1 to .*\(created by .*\(publisher\)\)$/);
    expect(created).not.toContain("none");
  });
});

describe("who started an operation", () => {
  test("a publish names the principal and its role", async () => {
    expect(await started(operation("publish", { principal: "usr_a", role: "publisher" }, { draft: DRF, draftRevision: 3 }))).toMatch(/\(publisher\)$/);
  });

  test("an import names the principal and its role", async () => {
    expect(await started(operation("ingest", { principal: "usr_b", role: "author" }, { draft: DRF, draftRevision: 3, ingestion: "ing_a" }))).toMatch(/\(author\)$/);
  });

  test("an apply the controller created names the controller, the plan's title and who created the plan", async () => {
    const s = await started(operation("apply-config", null, { plan: PLN, machine: MCH }));
    expect(s).toMatch(/^the controller, committing plan Apply of worker-1 to Add the office labels, published .*\(created by .*\(publisher\)\)$/);
    expect(s).not.toContain("none");
  });

  test("an adoption the controller created names the controller, the plan's title and who created the plan", async () => {
    answers[`/plans/${PLN}`] = ok(plan("adopt"));
    const s = await started(operation("adopt", null, { plan: PLN, machine: MCH }));
    expect(s).toMatch(/^the controller, adopting under plan Adopt of worker-1 to Add the office labels, published .*\(created by .*\(publisher\)\)$/);
    expect(s).not.toContain("none");
  });

  test("a plan that cannot be read leaves the controller and the plan's identifier, with no creator", async () => {
    answers[`/plans/${PLN}`] = missing;
    const s = await started(operation("apply-config", null, { plan: PLN, machine: MCH }));
    expect(s).toMatch(/^the controller, committing plan pln_/);
    expect(s).not.toContain("created by");
    expect(s).not.toContain("none");
  });

  test("an operation with no creator and no plan named is the controller alone", async () => {
    expect(await started(operation("adopt", null, null))).toBe("the controller");
  });

  test("a publish with no creator, which the server never writes, is shown as none", async () => {
    expect(await started(operation("publish", null, { draft: DRF, draftRevision: 3 }))).toBe("none");
  });
});
