import { describe, expect, test } from "vitest";
import { applyId, type ApplyIo, applyPath, applyPlans, cancelPlans, type Line, LINES, rowOf, showApply } from "./apply.ts";
import type { Position } from "./nextstep.tsx";
import { Write, type WriteSpec } from "./write.ts";

const REL = `rel_${"r".repeat(26)}`;
const A = `mch_${"a".repeat(26)}`;
const B = `mch_${"b".repeat(26)}`;
const C = `mch_${"c".repeat(26)}`;
const plan = (n: string) => `pln_${n.repeat(26)}`;

// A position as the server serves it, with only what the page reads: the step, this release's plan, the operation, who holds the rollout.
const pos = (step: Position["step"], over: { applied?: string; desired?: string; pending?: boolean; plan?: string; planRelease?: string; held?: string; frozen?: boolean; drift?: boolean } = {}): Position =>
  ({
    machine: { desired: over.desired ?? null, pendingConvergence: over.pending === true, applied: over.applied === undefined ? null : { release: over.applied, source: "operation", importBase: null }, frozen: over.frozen === true, openDrift: over.drift === true ? { revision: 1 } : null },
    plan: over.plan === undefined ? null : { id: over.plan, state: step === "awaiting-dispatch" ? "approved" : "proposed", operation: "apply-config", release: over.planRelease ?? REL, approvalCurrent: null, createdAt: "", expiresAt: "" },
    operation: step === "operation-in-flight" ? { id: "op_x", kind: "apply-config", state: "sending", plan: over.plan ?? "", createdAt: "" } : null,
    rolloutHeldBy: over.held === undefined ? null : { id: "op_y", machine: over.held },
    step,
  }) as unknown as Position;

describe("the apply page's address", () => {
  test("names a release by its own identifier and nothing else", () => {
    expect(applyPath(REL)).toBe(`/releases/${REL}/apply`);
    expect(applyId(`/releases/${REL}/apply`)).toBe(REL);
    expect(applyId(`/releases/${REL}`)).toBeUndefined();
    expect(applyId(`/releases/${A}/apply`)).toBeUndefined();
    expect(applyId(`/releases/${REL}/apply/x`)).toBeUndefined();
    expect(applyId(`/releases/%E0%A4%A/apply`)).toBeUndefined();
  });
});

describe("what a machine's row says", () => {
  const say = (p: Position) => rowOf(p, REL);
  test("one sentence for each place a machine can stand", () => {
    expect(say(pos("needs-plan"))).toEqual({ state: "not-applied", text: LINES.notApplied });
    expect(say(pos("awaiting-approval", { plan: plan("p") }))).toEqual({ state: "ready", text: LINES.ready });
    expect(say(pos("awaiting-dispatch", { plan: plan("p"), held: B }))).toEqual({ state: "waiting", text: LINES.behind });
    expect(say(pos("awaiting-dispatch", { plan: plan("p") }))).toEqual({ state: "waiting", text: LINES.approved });
    expect(say(pos("operation-in-flight", { plan: plan("p") }))).toEqual({ state: "running", text: LINES.running });
    expect(say(pos("operation-unresolved"))).toEqual({ state: "stopped", text: LINES.unresolved });
    expect(say(pos("drift-open", { drift: true }))).toEqual({ state: "stopped", text: LINES.drift });
    expect(say(pos("no-step", { applied: REL }))).toEqual({ state: "applied", text: LINES.applied });
  });
  test("a machine this release leaves unchanged is applied, and one whose configuration still differs is not", () => {
    const older = `rel_${"o".repeat(26)}`;
    expect(say(pos("no-step", { applied: older, desired: REL }))).toEqual({ state: "applied", text: LINES.applied });
    expect(say(pos("needs-plan", { applied: older, desired: REL, pending: true }))).toEqual({ state: "not-applied", text: LINES.notApplied });
    expect(say(pos("needs-plan", { applied: older, desired: older }))).toEqual({ state: "not-applied", text: LINES.notApplied });
  });
  test("a plan of this release for another operation is not an apply plan, and the row says it is decided on the machine's page", () => {
    const adopt = (step: Position["step"]) => ({ ...pos(step, { plan: plan("p") }), plan: { ...pos(step, { plan: plan("p") }).plan!, operation: "adopt" } }) as unknown as Position;
    expect(say(adopt("awaiting-approval"))).toEqual({ state: "stopped", text: LINES.otherPlan });
    expect(say(adopt("awaiting-dispatch"))).toEqual({ state: "stopped", text: LINES.otherPlan });
  });
  test("a plan of another release is not this release's plan, and a frozen machine says so", () => {
    expect(say(pos("awaiting-approval", { plan: plan("p"), planRelease: `rel_${"s".repeat(26)}` }))).toEqual({ state: "not-applied", text: LINES.notApplied });
    expect(say(pos("needs-plan", { frozen: true }))).toEqual({ state: "stopped", text: LINES.frozen });
  });
});

// A server for the page: each machine's position, and what each write does. `log` is every write sent, in order.
function world(positions: Record<string, Position>, refuse: { plan?: string[]; approve?: string[]; unknownPlan?: string[]; unknownApprove?: string[] } = {}) {
  const log: string[] = [];
  // A write with an unknown outcome stays open, and nothing new goes while one is (what the page's `canSend` reads).
  let open = false;
  const lines: Record<string, Line> = {};
  let n = 0;
  const made: Record<string, string> = {};
  const io: ApplyIo = {
    read: async <T,>(path: string) => {
      const m = /^\/machines\/([^/]+)\/position$/.exec(path)?.[1];
      return m !== undefined && positions[m] !== undefined ? ({ kind: "ok", data: positions[m] } as never as T extends never ? never : never) : undefined;
    },
    send: async (spec: WriteSpec) => {
      log.push(`${spec.method} ${spec.path}`);
      const w = new Write(spec);
      const body = spec.body as { machine?: string };
      if (spec.method === "POST" && spec.path === "/plans") {
        const m = body.machine!;
        if (refuse.plan?.includes(m)) return Object.assign(w, { outcome: { kind: "refused", status: 409, problem: { detail: "no" } } });
        if (refuse.unknownPlan?.includes(m)) return (open = true), Object.assign(w, { outcome: { kind: "unknown", why: "the connection was lost", problem: null } });
        const id = plan(String.fromCharCode(97 + ++n));
        made[m] = id;
        return Object.assign(w, { outcome: { kind: "done", data: { id } } });
      }
      const target = /^\/plans\/([^/]+)\/(approvals|cancellations)$/.exec(spec.path);
      if (target?.[2] === "approvals" && refuse.approve?.includes(target[1]!)) return Object.assign(w, { outcome: { kind: "refused", status: 403, problem: { detail: "no" } } });
      if (target?.[2] === "approvals" && refuse.unknownApprove?.includes(target[1]!)) return (open = true), Object.assign(w, { outcome: { kind: "unknown", why: "the connection was lost", problem: null } });
      return Object.assign(w, { outcome: { kind: "done", data: {} } });
    },
    say: (key, line) => void (lines[key] = line),
    live: () => true,
    canSend: () => !open,
    handedOver: () => false,
  };
  return { io, log, lines, made };
}

describe("Show apply", () => {
  test("creates a plan for each selected machine that has none, and reuses one that is ready", async () => {
    const w = world({ [A]: pos("needs-plan"), [B]: pos("awaiting-approval", { plan: plan("z") }) });
    const { plans, made } = await showApply(REL, [A, B], w.io);
    expect(w.log).toEqual(["POST /plans"]);
    expect([...plans.keys()]).toEqual([A, B]);
    expect(plans.get(B)).toBe(plan("z"));
    expect(plans.get(A)).toBe(w.made[A]);
    // Only the plan this press made may be cancelled by the page: the other was prepared by somebody else.
    expect([...made.keys()]).toEqual([w.made[A]]);
    expect(made.get(w.made[A]!)).toBe(A);
  });
  test("a plan is in the caller's map the moment it is made, so a machine no longer chosen can have it cancelled", async () => {
    const w = world({ [A]: pos("needs-plan"), [B]: pos("needs-plan") });
    const held = new Map<string, string>();
    const seenAtB: string[] = [];
    const read = w.io.read;
    const io: ApplyIo = { ...w.io, read: (async (path: string) => {
      if (path === `/machines/${B}/position`) seenAtB.push(...held.keys());
      return read(path);
    }) as ApplyIo["read"] };
    await showApply(REL, [A, B], io, held);
    expect(seenAtB).toEqual([w.made[A]]);
    expect([...held.keys()]).toEqual([w.made[A], w.made[B]]);
  });
  // The person leaves while a plan is being made: the answer of that request is the only place the plan's ID is
  // known, so the plan is cancelled when it arrives, and no more are made.
  test("a plan whose creation is answered after the page stopped caring is cancelled, and the rest are not made", async () => {
    const w = world({ [B]: pos("awaiting-approval", { plan: plan("z") }), [A]: pos("needs-plan"), [C]: pos("needs-plan") });
    let caring = true;
    const send = w.io.send;
    const io: ApplyIo = { ...w.io, live: () => caring, send: async (spec) => {
      const out = await send(spec);
      if (spec.path === "/plans") caring = false;
      return out;
    } };
    const held = new Map<string, string>();
    const { plans } = await showApply(REL, [B, A, C], io, held);
    expect(w.log).toEqual(["POST /plans", `POST /plans/${w.made[A]}/cancellations`]);
    expect([...held]).toEqual([]);
    expect([...plans.keys()]).toEqual([B]);
    // The plan a publisher prepared is reused and never cancelled, whichever way the press ends.
    expect(w.log.join(" ")).not.toContain(plan("z"));
  });
  // The answer arrives after the person left, but the plan was handed over meanwhile (its withheld line was drawn,
  // and that line says the plan stays): the late answer cancels nothing.
  test("a plan handed over to the person is not cancelled by the late answer of its creation", async () => {
    const w = world({ [A]: pos("needs-plan") });
    let caring = true;
    const send = w.io.send;
    const io: ApplyIo = { ...w.io, live: () => caring, handedOver: () => true, send: async (spec) => {
      const out = await send(spec);
      if (spec.path === "/plans") caring = false;
      return out;
    } };
    await showApply(REL, [A], io, new Map());
    expect(w.log).toEqual(["POST /plans"]);
  });
  // A creation whose outcome is unknown has no plan ID for the page: the row says so, and the page cancels nothing.
  test("a creation whose outcome is unknown stops that machine's row and cancels nothing", async () => {
    const w = world({ [A]: pos("needs-plan") });
    const io: ApplyIo = { ...w.io, send: async (spec) => {
      w.log.push(`${spec.method} ${spec.path}`);
      return Object.assign(new Write(spec), { outcome: { kind: "unknown", why: "lost", problem: null } });
    } };
    const held = new Map<string, string>();
    await showApply(REL, [A], io, held);
    expect(w.lines[A]?.state).toBe("stopped");
    expect(w.log).toEqual(["POST /plans"]);
    expect(held.size).toBe(0);
  });
  test("one creation that fails leaves the others made and shown, and nothing to cancel", async () => {
    const w = world({ [A]: pos("needs-plan"), [B]: pos("needs-plan"), [C]: pos("needs-plan") }, { plan: [A] });
    const held = new Map<string, string>();
    const { plans } = await showApply(REL, [A, B, C], w.io, held);
    expect(w.log).toEqual(["POST /plans", "POST /plans", "POST /plans"]);
    expect([...plans.keys()]).toEqual([B, C]);
    expect([...held.keys()]).toEqual([w.made[B], w.made[C]]);
    expect(w.lines[A]?.state).toBe("stopped");
    expect(w.lines[B]?.state).toBe("ready");
    expect(w.lines[C]?.state).toBe("ready");
  });
  test("leaves out a machine that cannot be planned, in words, and goes on with the others", async () => {
    const w = world({ [A]: pos("needs-plan"), [B]: pos("operation-in-flight", { plan: plan("q") }), [C]: pos("needs-plan") }, { plan: [A] });
    const { plans } = await showApply(REL, [A, B, C], w.io);
    expect([...plans.keys()]).toEqual([C]);
    expect(w.lines[A]?.state).toBe("stopped");
    expect(w.lines[B]).toEqual({ state: "running", text: LINES.running });
  });
});

describe("Apply", () => {
  test("approves each plan and carries on past a refusal", async () => {
    const w = world({}, { approve: [plan("a")] });
    await applyPlans(new Map([[A, plan("a")], [B, plan("b")]]), w.io);
    expect(w.log).toEqual([`POST /plans/${plan("a")}/approvals`, `POST /plans/${plan("b")}/approvals`]);
    expect(w.lines[A]?.state).toBe("stopped");
    expect(w.lines[B]?.state).toBe("done");
  });
  test("sends nothing once the page has left", async () => {
    const w = world({});
    await applyPlans(new Map([[A, plan("a")]]), { ...w.io, live: () => false, canSend: () => false });
    expect(w.log).toEqual([]);
  });
});

// A write whose outcome is unknown holds the page: the page's own sentence says nothing new is sent, so the next
// request of a batch is not sent either, in either loop (one guard, `canSend`, decides every send).
describe("a write of unknown outcome stops the rest of its batch", () => {
  type World = ReturnType<typeof world>;
  const cases: { name: string; positions: Record<string, Position>; fault: { unknownPlan?: string[]; unknownApprove?: string[] }; run: (w: World) => Promise<unknown>; sent: string[] }[] = [
    { name: "creations", positions: { [A]: pos("needs-plan"), [B]: pos("needs-plan") }, fault: { unknownPlan: [A] }, run: (w) => showApply(REL, [A, B], w.io), sent: ["POST /plans"] },
    { name: "approvals", positions: {}, fault: { unknownApprove: [plan("a")] }, run: (w) => applyPlans(new Map([[A, plan("a")], [B, plan("b")]]), w.io), sent: [`POST /plans/${plan("a")}/approvals`] },
  ];
  test.each(cases)("$name: the second item is not sent", async (c) => {
    const w = world(c.positions, c.fault);
    await c.run(w);
    expect(w.log).toEqual(c.sent);
  });
});
