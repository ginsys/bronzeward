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
import { type Write, Writes } from "./write.ts";

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

// The page mounted over a served world: `served` answers reads, `answer` decides each write's outcome, `sent` lists the writes.
// `slow.ms` delays every machine position read; `calls` counts the reads of each path; `dismiss` closes the unknown
// writes (their notices dismissed) and `open` is what `writes.open` answers (the mocked submit does not track it);
// `fake` runs the page under fake timers, which `settled` then advances.
function mount(served: Record<string, unknown>, answer: (path: string) => unknown, fake = false) {
  const box = document.createElement("div");
  document.body.append(box);
  const root = createRoot(box);
  const slow = { ms: 0 };
  const calls: Record<string, number> = {};
  const reads = {
    client: { read: vi.fn(async (path: string) => {
      calls[path] = (calls[path] ?? 0) + 1;
      if (slow.ms > 0 && path.endsWith("/position")) await new Promise((r) => setTimeout(r, slow.ms));
      return path in served ? { kind: "ok", data: served[path], etag: '"1"' } : { kind: "refused", status: 404, problem: null };
    }) },
    settle: (res: unknown, set: (r: unknown) => void, live: () => boolean) => (live() && set(res), true),
    undropped: () => () => true,
    view: "", again: 0, regained: 0, go: () => {}, failure: () => null,
  } as unknown as Reads;
  const writes = new Writes(() => "p");
  const sent: string[] = [];
  const all: Write[] = [];
  const closed = new Set<Write>();
  const wire = (x: Writes) => {
    vi.spyOn(x, "submit").mockImplementation(async (w) => {
      sent.push(w.spec.path);
      all.push(w);
      w.outcome = answer(w.spec.path) as never;
      return w.outcome;
    });
    vi.spyOn(x, "open", "get").mockImplementation(() => all.filter((w) => w.outcome?.kind === "unknown" && !closed.has(w)));
  };
  wire(writes);
  const dismiss = () => all.forEach((w) => closed.add(w));
  const settled = () => act(async () => void (fake ? await vi.advanceTimersByTimeAsync(40) : await new Promise((r) => setTimeout(r, 40))));
  const elapse = (ms: number) => act(async () => void (await vi.advanceTimersByTimeAsync(ms)));
  const row = (m: string) => box.querySelector(`[data-testid="apply-row"][data-machine="${m}"]`)!;
  const button = (label: string) => [...box.querySelectorAll("button")].find((b) => b.textContent === label)!;
  const off = (label: string) => button(label).hasAttribute("disabled") || button(label).getAttribute("aria-disabled") === "true";
  const render = async (w: Writes) => {
    if (w !== writes) wire(w);
    await act(async () =>
      root.render(h(WritesProvider, { value: w }, h(ShellContext, { value: { roles: ["author", "publisher", "approver"], offline: false } }, h(ReadsProvider, { value: reads }, h(ApplyForm, { release: REL, r: release as never }))))),
    );
    await settled();
  };
  const choose = async (m: string) => {
    await act(async () => void row(m).querySelector<HTMLInputElement>("input[type=checkbox]")!.click());
    await settled();
  };
  const press = async (label: string) => {
    await act(async () => void button(label).click());
    await settled();
  };
  const stop = async () => {
    await act(async () => root.unmount());
    box.remove();
  };
  const start = () => render(writes);
  return { sent, row, button, off, start, render, choose, press, stop, settled, elapse, slow, calls, dismiss, box };
}
const done = (data: unknown) => ({ kind: "done", status: 201, data, replayed: false });
const served2 = (over: Record<string, unknown> = {}): Record<string, unknown> => ({
  [`/machines/${A}`]: machine(A), [`/machines/${B}`]: machine(B),
  [`/machines/${A}/position`]: position(A, "pln_a"), [`/machines/${B}/position`]: position(B, "pln_b"),
  "/plans/pln_a": plan("pln_a", A), "/plans/pln_b": plan("pln_b", B),
  ...over,
});

describe("the apply page's plans and approvals", () => {
  test("Apply stays off while a plan's change could not be read", async () => {
    const served = served2();
    delete served["/plans/pln_a"];
    const p = mount(served, () => done({ id: "apr_1" }));
    await p.start();
    await p.choose(A);
    expect(p.box.querySelector('[data-testid="apply-unread"]')).not.toBeNull();
    expect(p.button("Apply").hasAttribute("disabled") || p.button("Apply").getAttribute("aria-disabled") === "true").toBe(true);
    await p.stop();
  });

  test("a machine no longer chosen has its plan cancelled, and the plan of a machine still chosen is kept", async () => {
    const none = (m: string) => ({ ...position(m, "x"), step: "needs-plan", plan: null });
    let n = 0;
    const p = mount(served2({ [`/machines/${A}/position`]: none(A), [`/machines/${B}/position`]: none(B) }), (path) => (path === "/plans" ? done({ id: `pln_made${++n}` }) : done({})));
    await p.start();
    await p.choose(A);
    await p.choose(B);
    await p.press("Show apply");
    expect(p.sent).toEqual(["/plans", "/plans"]);
    await p.choose(A);
    // The plan made for A (the first) is cancelled and the one made for B stays.
    expect(p.sent).toEqual(["/plans", "/plans", "/plans/pln_made1/cancellations"]);
    await p.stop();
  });

  test("a plan Apply was pressed for is never cancelled, when the choice changes or the person leaves", async () => {
    const p = mount(served2(), (path) => done(path.endsWith("/approvals") ? { id: "apr_1" } : {}));
    await p.start();
    await p.choose(A);
    await p.choose(B);
    await p.press("Apply");
    expect(p.sent).toEqual(["/plans/pln_a/approvals", "/plans/pln_b/approvals"]);
    await p.stop();
    expect(p.sent.filter((s) => s.endsWith("/cancellations"))).toEqual([]);
  });

  // The server withholds a plan's diff when a configuration cannot be redacted or compared: the page says so beside the
  // plan, names where to go, and keeps Apply off; a plan whose change is shown leaves Apply on.
  test("Apply stays off while a plan's change is withheld, with the way to the plan, and is on when it is shown", async () => {
    const withheld = { ...plan("pln_a", A), evidence: { diff: { withheld: true, from: "rel_x", to: REL, unified: "", fromFormOnly: false }, validation: null, dryRun: null, baseline: null } };
    const p = mount(served2({ "/plans/pln_a": withheld }), () => done({ id: "apr_1" }));
    await p.start();
    await p.choose(A);
    expect(p.off("Apply")).toBe(true);
    expect(p.box.querySelector('[data-testid="apply-unread"]')).toBeNull();
    const note = p.box.querySelector('[data-testid="apply-withheld"]')!;
    expect(note.textContent).toContain("could not be redacted");
    expect(note.textContent).toContain("Open the plan");
    expect([...note.querySelectorAll("a")].find((a) => a.textContent === "Open the plan")?.getAttribute("href")).toContain("/plans/pln_a");
    await p.choose(B);
    expect(p.off("Apply")).toBe(true);
    await p.choose(A);
    expect(p.box.querySelector('[data-testid="apply-withheld"]')).toBeNull();
    expect(p.off("Apply")).toBe(false);
    await p.stop();
  });

  // forms.tsx's rule: a new write starts only once the last one ended or its notice was dismissed.
  test("Show apply and Apply send nothing new while a write of the page has an unknown outcome", async () => {
    const none = (m: string) => ({ ...position(m, "x"), step: "needs-plan", plan: null });
    const p = mount(served2({ [`/machines/${A}/position`]: none(A) }), (path) => (path === "/plans" ? { kind: "unknown", why: "the connection was lost", problem: null } : done({ id: "apr_1" })));
    await p.start();
    await p.choose(A);
    expect(p.off("Show apply")).toBe(false);
    await p.press("Show apply");
    expect(p.sent).toEqual(["/plans"]);
    expect(p.box.querySelector('[data-testid="apply-held"]')).not.toBeNull();
    expect(p.off("Show apply")).toBe(true);
    await p.press("Show apply");
    expect(p.sent).toEqual(["/plans"]);
    // Its notice dismissed, the page is free to start a new write.
    p.dismiss();
    await p.choose(B);
    expect(p.box.querySelector('[data-testid="apply-held"]')).toBeNull();
    expect(p.off("Show apply")).toBe(false);
    await p.stop();
  });

  test("Apply and Show apply are off while an approval of the page has an unknown outcome", async () => {
    const p = mount(served2(), (path) => (path === "/plans/pln_a/approvals" ? { kind: "unknown", why: "the connection was lost", problem: null } : done({ id: "apr_1" })));
    await p.start();
    await p.choose(A);
    await p.press("Apply");
    expect(p.sent).toEqual(["/plans/pln_a/approvals"]);
    expect(p.off("Apply")).toBe(true);
    expect(p.off("Show apply")).toBe(true);
    await p.press("Apply");
    expect(p.sent).toEqual(["/plans/pln_a/approvals"]);
    await p.stop();
  });

  // list.tsx drops a read whose effect ran again, so a poll on a clock drops every read slower than the clock: the next
  // read starts five seconds after the last one settled.
  test("the rows are read again five seconds after the last read settled, never while one is slow", async () => {
    vi.useFakeTimers();
    try {
      const p = mount(served2(), () => done({ id: "apr_1" }), true);
      await p.start();
      await p.choose(A);
      await p.press("Apply");
      p.slow.ms = 7000;
      const path = `/machines/${A}/position`;
      await p.elapse(5000);
      const first = p.calls[path]!;
      await p.elapse(3000);
      // 8 s on: the read that started at 5 s took 7 s and has not settled; no second read started.
      expect(p.calls[path]).toBe(first);
      await p.elapse(5000);
      // It settled at 12 s and the next read waits until 17 s.
      expect(p.calls[path]).toBe(first);
      await p.elapse(5000);
      expect(p.calls[path]).toBe(first + 1);
      await p.stop();
    } finally {
      vi.useRealTimers();
    }
  });

  test("the plans a page made are cancelled when the person leaves, and not when its writes object is replaced", async () => {
    const none = (m: string) => ({ ...position(m, "x"), step: "needs-plan", plan: null });
    const p = mount(served2({ [`/machines/${A}/position`]: none(A) }), (path) => (path === "/plans" ? done({ id: "pln_new" }) : done({})));
    await p.start();
    await p.choose(A);
    await p.press("Show apply");
    expect(p.sent).toEqual(["/plans"]);
    await p.render(new Writes(() => "q"));
    expect(p.sent).toEqual(["/plans"]);
    await p.stop();
    expect(p.sent).toEqual(["/plans", "/plans/pln_new/cancellations"]);
  });

  test("an approval whose outcome is unknown is not said to be unapproved", async () => {
    const p = mount(served2(), (path) => (path === "/plans/pln_a/approvals" ? { kind: "unknown", why: "the connection was lost", problem: null } : done({ id: "apr_1" })));
    await p.start();
    await p.choose(A);
    await p.press("Apply");
    expect(p.row(A).textContent).not.toContain("Nothing was approved for this machine.");
    await p.stop();
  });
});
