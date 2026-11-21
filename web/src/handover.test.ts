import { createElement as h } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import { WritesProvider } from "./forms.tsx";
import { Handover } from "./handover.tsx";
import { actFor, DRAFT, type Followed, handOver, HANDOVER_SENTENCE, type Io, type Line, LINES, MISSING, missingRoles, NO_ETAG } from "./handover.ts";
import { type Reads, ReadsProvider } from "./list.tsx";
import type { Position } from "./nextstep.tsx";
import { ShellContext } from "./shell.tsx";
import { Write, type WriteSpec, Writes } from "./write.ts";

const REL = `rel_${"r".repeat(26)}`;
const A = `mch_${"a".repeat(26)}`;
const B = `mch_${"b".repeat(26)}`;

// What the server holds for one machine; the position is derived from it as the server decides it.
interface M { applied: boolean; desired: boolean; plan: "none" | "proposed" | "approved"; drift?: boolean; unresolved?: boolean; refusePlan?: boolean; holdsAfterApproval?: number }

// A server for the handover: the draft, the machines and what each write does to them. `log` is every write sent.
function world(draft: { state: string; running?: boolean; etag?: string; followed?: Followed }, machines: Record<string, M>) {
  const log: string[] = [];
  // The If-Match each publication carried, and each operation followed to its end.
  const ifMatches: (string | undefined)[] = [];
  const follows: string[] = [];
  // A publication takes effect once: the draft is published and each machine's Desired is the release.
  const publishNow = () => {
    draft.state = "published";
    for (const m of Object.values(machines)) m.desired = true;
  };
  let waits = 0;
  const waitsAfter: Record<string, number> = {};
  const step = (m: M): Position["step"] =>
    m.unresolved === true ? "operation-unresolved"
      : m.plan === "approved" ? "awaiting-dispatch"
        : m.plan === "proposed" ? "awaiting-approval"
          : m.drift === true ? "drift-open"
            : m.desired && !m.applied ? "needs-plan"
              : !m.desired && !m.applied ? "not-handed-over" : "no-step";
  const position = (id: string): Position => {
    const m = machines[id]!;
    return {
      machine: { id, applied: m.applied ? { release: REL } : null, desired: m.desired ? REL : null, openDrift: m.drift === true ? {} : null },
      plan: m.plan === "none" ? null : { id: `pln_${id}`, state: m.plan, operation: "adopt", release: REL, approvalCurrent: m.plan === "approved" },
      step: step(m),
    } as unknown as Position;
  };
  const wrote = (spec: WriteSpec, outcome: Write["outcome"]) => Object.assign(new Write(spec), { outcome });
  const done = (data: unknown) => ({ kind: "done" as const, status: 201, data, replayed: false });
  const refused = (status: number) => ({ kind: "refused" as const, status, problem: null, replayed: false });
  const lines: Record<string, Line> = {};
  const plans: { machine: string; operation: string; releaseId: string }[] = [];
  const io: Io = {
    read: <T,>(path: string) => {
      const machine = /^\/machines\/(mch_\w+)\/position$/.exec(path)?.[1];
      if (machine !== undefined) return Promise.resolve({ kind: "ok", data: position(machine) } as never);
      if (path.startsWith("/drafts/")) return Promise.resolve({ kind: "ok", data: draft, etag: draft.etag ?? '"1"' } as never);
      return Promise.resolve(undefined as T);
    },
    follow: (op) => {
      follows.push(op);
      // A running publication ends when it is followed to its end.
      if (draft.running === true) {
        draft.running = false;
        publishNow();
      }
      return Promise.resolve(draft.followed ?? { kind: "released" });
    },
    send: (spec) => {
      log.push(`${spec.method} ${spec.path}`);
      if (spec.path.endsWith("/publications")) {
        ifMatches.push(spec.ifMatch);
        // The server's draft is at its own ETag: a publication that does not carry it is refused (412).
        if (draft.state !== "open" || spec.ifMatch !== (draft.etag ?? '"1"')) return Promise.resolve(wrote(spec, refused(412)));
        // A publication already running for this draft revision is answered with its operation, not a second one.
        if (draft.running !== true) publishNow();
        return Promise.resolve(wrote(spec, done({ id: "op_1" })));
      }
      if (spec.path === "/plans") {
        const body = spec.body as { machine: string; operation: string; releaseId: string };
        // The button only ever hands over: an adopt plan for the machine's Desired release.
        if (body.operation !== "adopt" || body.releaseId !== REL) throw new Error(`unexpected plan ${JSON.stringify(body)}`);
        plans.push(body);
        const id = body.machine;
        const m = machines[id]!;
        if (m.refusePlan === true) return Promise.resolve(wrote(spec, refused(409)));
        if (m.holdsAfterApproval !== -1) m.plan = "proposed";
        return Promise.resolve(wrote(spec, done({ id: `pln_${id}` })));
      }
      const id = /^\/plans\/pln_(mch_\w+)\/approvals$/.exec(spec.path)![1]!;
      machines[id]!.plan = "approved";
      waitsAfter[id] = waits;
      return Promise.resolve(wrote(spec, done({ id: "apr_1" })));
    },
    wait: () => {
      waits++;
      // The controller records the handover of an approved plan after its wait.
      for (const [id, m] of Object.entries(machines)) if (m.plan === "approved" && m.holdsAfterApproval !== Infinity && waits - (waitsAfter[id] ?? 0) >= (m.holdsAfterApproval ?? 1)) Object.assign(m, { applied: true, plan: "none" });
      return Promise.resolve();
    },
    say: (key, line) => void (lines[key] = line),
    live: () => true,
  };
  return { io, log, lines, machines, plans, ifMatches, follows };
}
const fresh = (): M => ({ applied: false, desired: false, plan: "none" });
// The draft's ETag as the page shows it.
const SHOWN = '"1"';
const text = (l: Line | undefined) => `${l?.state}: ${l?.text}`;

describe("the handover button (ginsys/bronzeward#1066): one press, every act the existing route, once", () => {
  test("all roles, a fresh draft of two machines: one publish, one plan and one approval each, nothing else, and both end managed", async () => {
    const w = world({ state: "open" }, { [A]: fresh(), [B]: fresh() });
    await handOver("drf_1", [A, B], w.io, SHOWN);
    expect([...w.log].sort()).toEqual([
      "POST /drafts/drf_1/publications", "POST /plans", "POST /plans", `POST /plans/pln_${A}/approvals`, `POST /plans/pln_${B}/approvals`,
    ].sort());
    expect(w.log[0]).toBe("POST /drafts/drf_1/publications");
    expect(text(w.lines[DRAFT])).toBe(`done: ${LINES.published}`);
    expect(text(w.lines[A])).toBe(`done: ${LINES.managed}`);
    expect(text(w.lines[B])).toBe(`done: ${LINES.managed}`);
    // Only adopt plans for the Desired release are made: the button never applies (the mock refuses any other plan).
    expect(w.plans.map((p) => p.machine).sort()).toEqual([A, B]);
    expect(w.plans.every((p) => p.operation === "adopt" && p.releaseId === REL)).toBe(true);
    expect(LINES.managed).toContain("nothing was sent to it");
  });

  test("a publication already running for the draft is followed through the operation the server answers, and the machines then go on", async () => {
    const w = world({ state: "open", running: true }, { [A]: fresh() });
    await handOver("drf_1", [A], w.io, SHOWN);
    // One request, answered with the running operation: no second publication exists, and the press waits for that one.
    expect(w.log.filter((l) => l.endsWith("/publications"))).toHaveLength(1);
    expect(text(w.lines[DRAFT])).toBe(`done: ${LINES.published}`);
    expect(text(w.lines[A])).toBe(`done: ${LINES.managed}`);
  });

  test("a second press after the page closed halfway: A's plan waits for approval, B is managed, so exactly one approval is sent", async () => {
    const w = world({ state: "published" }, { [A]: { ...fresh(), desired: true, plan: "proposed" }, [B]: { ...fresh(), desired: true, applied: true } });
    await handOver("drf_1", [A, B], w.io, SHOWN);
    expect(w.log).toEqual([`POST /plans/pln_${A}/approvals`]);
    expect(text(w.lines[DRAFT])).toBe("done: Already published.");
    expect(text(w.lines[A])).toBe(`done: ${LINES.managed}`);
    expect(text(w.lines[B])).toBe(`done: ${LINES.already}`);
  });

  test("a machine whose plan is refused stops alone with its write shown; the other ends managed; pressing again acts on the failed one only", async () => {
    const w = world({ state: "open" }, { [A]: fresh(), [B]: { ...fresh(), refusePlan: true } });
    await handOver("drf_1", [A, B], w.io, SHOWN);
    expect(w.lines[B]?.state).toBe("stopped");
    expect(w.lines[B]?.write?.outcome).toMatchObject({ kind: "refused", status: 409 });
    expect(text(w.lines[A])).toBe(`done: ${LINES.managed}`);
    w.log.length = 0;
    delete w.machines[B]!.refusePlan;
    await handOver("drf_1", [A, B], w.io, SHOWN);
    expect(w.log).toEqual(["POST /plans", `POST /plans/pln_${B}/approvals`]);
    expect(text(w.lines[A])).toBe(`done: ${LINES.already}`);
    expect(text(w.lines[B])).toBe(`done: ${LINES.managed}`);
  });

  test("a machine with an Applied configuration and an open drift record gets no write and keeps its separate steps", async () => {
    const w = world({ state: "open" }, { [A]: fresh(), [B]: { ...fresh(), applied: true, desired: true, drift: true } });
    await handOver("drf_1", [A, B], w.io, SHOWN);
    expect(w.log.filter((l) => l.includes(B))).toEqual([]);
    expect(w.log.filter((l) => l === "POST /plans")).toHaveLength(1);
    expect(text(w.lines[B])).toBe(`left: ${LINES.applied}`);
    expect(text(w.lines[A])).toBe(`done: ${LINES.managed}`);
  });

  test("the same act never repeats in one press: a created plan the position does not show stops the machine", async () => {
    const w = world({ state: "published" }, { [A]: { ...fresh(), desired: true, holdsAfterApproval: -1 } });
    await handOver("drf_1", [A], w.io, SHOWN);
    expect(w.log).toEqual(["POST /plans"]);
    expect(text(w.lines[A])).toBe(`stopped: ${LINES.repeated}`);
  });

  test("an approved plan the controller has not recorded is waited for, then left to a later press with the words for it", async () => {
    const w = world({ state: "published" }, { [A]: { ...fresh(), desired: true, plan: "approved", holdsAfterApproval: Infinity } });
    await handOver("drf_1", [A], w.io, SHOWN);
    expect(w.log).toEqual([]);
    expect(text(w.lines[A])).toBe(`waiting: ${LINES.slow}`);
    // The line while it waits says the person need not stay.
    expect(LINES.waiting).toContain("you can leave this page");
  });

  test("an unresolved operation stops its machine in words", async () => {
    const w = world({ state: "published" }, { [A]: { ...fresh(), desired: true, plan: "none", unresolved: true } });
    await handOver("drf_1", [A], w.io, SHOWN);
    expect(text(w.lines[A])).toBe(`stopped: ${LINES.unresolved}`);
    expect(w.log).toEqual([]);
  });

  test("a discarded draft is not published and no machine is read", async () => {
    const w = world({ state: "discarded" }, { [A]: fresh() });
    await handOver("drf_1", [A], w.io, SHOWN);
    expect(w.log).toEqual([]);
    expect(text(w.lines[DRAFT])).toBe(`stopped: ${LINES.draftGone("discarded")}`);
    expect(w.lines[A]).toBeUndefined();
  });

  test("a publish the server refuses stops everything with that write shown", async () => {
    const w = world({ state: "open" }, { [A]: fresh() });
    const send = w.io.send;
    w.io.send = (spec) => (spec.path.endsWith("/publications") ? Promise.resolve(Object.assign(new Write(spec), { outcome: { kind: "refused" as const, status: 412, problem: null, replayed: false } })) : send(spec));
    await handOver("drf_1", [A], w.io, SHOWN);
    expect(w.lines[DRAFT]?.state).toBe("stopped");
    expect(w.lines[DRAFT]?.write?.outcome).toMatchObject({ status: 412 });
    expect(w.lines[A]).toBeUndefined();
  });

  test("the publication carries the ETag the page showed, even where the press's own read of the draft is newer", async () => {
    const w = world({ state: "open" }, { [A]: fresh() });
    await handOver("drf_1", [A], w.io, SHOWN);
    expect(w.ifMatches).toEqual([SHOWN]);
    // Another author changed the draft after the page showed it: the server's ETag differs, the page's is sent,
    // the publication is refused (412) with that write shown, and nothing else is sent.
    const other = world({ state: "open", etag: '"2"' }, { [A]: fresh() });
    await handOver("drf_1", [A], other.io, SHOWN);
    expect(other.ifMatches).toEqual([SHOWN]);
    expect(other.log).toEqual(["POST /drafts/drf_1/publications"]);
    expect(other.lines[DRAFT]?.write?.outcome).toMatchObject({ kind: "refused", status: 412 });
    expect(other.lines[A]).toBeUndefined();
  });

  test("with no ETag kept for the draft nothing is sent, and the line says to read the draft again", async () => {
    const w = world({ state: "open" }, { [A]: fresh() });
    await handOver("drf_1", [A], w.io, undefined);
    expect(w.log).toEqual([]);
    expect(text(w.lines[DRAFT])).toBe(`stopped: ${NO_ETAG}`);
    expect(w.lines[A]).toBeUndefined();
  });

  test("the publication is followed through the progress reader, and its end decides: a failed one stops everything in the reader's words", async () => {
    const w = world({ state: "open", followed: { kind: "ended", text: "Publication operation op_1 failed: boom. Nothing has been sent to any machine." } }, { [A]: fresh() });
    await handOver("drf_1", [A], w.io, SHOWN);
    expect(w.follows).toEqual(["op_1"]);
    expect(text(w.lines[DRAFT])).toBe("stopped: Publication operation op_1 failed: boom. Nothing has been sent to any machine.");
    expect(w.log).toEqual(["POST /drafts/drf_1/publications"]);
    expect(w.lines[A]).toBeUndefined();
  });

  test("a publication whose progress cannot be read stops in words, and one followed on a page that left says nothing", async () => {
    const unread = world({ state: "open", followed: { kind: "unread" } }, { [A]: fresh() });
    await handOver("drf_1", [A], unread.io, SHOWN);
    expect(text(unread.lines[DRAFT])).toBe(`stopped: ${LINES.publishUnread}`);
    expect(unread.lines[A]).toBeUndefined();
    const left = world({ state: "open", followed: { kind: "stopped" } }, { [A]: fresh() });
    await handOver("drf_1", [A], left.io, SHOWN);
    expect(left.lines[A]).toBeUndefined();
    expect(left.lines[DRAFT]?.text).toBe(LINES.publishing);
  });
});

describe("the act a position calls for", () => {
  const p = (step: Position["step"], more: Record<string, unknown> = {}) => ({ machine: { applied: null, desired: REL, ...((more["machine"] as object) ?? {}) }, plan: more["plan"] ?? null, step }) as unknown as Position;
  test("each step of a machine with no Applied", () => {
    expect(actFor(p("needs-plan"))).toEqual({ kind: "plan", release: REL });
    expect(actFor(p("awaiting-approval", { plan: { id: "pln_1" } }))).toEqual({ kind: "approve", plan: "pln_1" });
    expect(actFor(p("awaiting-dispatch"))).toEqual({ kind: "wait" });
    expect(actFor(p("operation-in-flight"))).toEqual({ kind: "wait" });
    expect(actFor(p("operation-unresolved")).kind).toBe("stop");
    expect(actFor(p("not-handed-over")).kind).toBe("stop");
    expect(actFor(p("import-paused")).kind).toBe("stop");
  });
  test("a machine that has an Applied reads as managed", () => {
    expect(actFor(p("no-step", { machine: { applied: {} } }))).toEqual({ kind: "managed" });
  });
});

describe("the page: the sentence, and the roles", () => {
  const noReads: Reads = { client: { read: () => new Promise(() => {}) }, settle: () => true, undropped: () => () => true, view: "", again: 0, regained: 0, go: () => {}, failure: () => null };
  const page = (roles: readonly ("author" | "publisher" | "approver")[]) =>
    renderToStaticMarkup(h(WritesProvider, { value: new Writes(() => "p") }, h(ShellContext, { value: { roles, offline: false } }, h(ReadsProvider, { value: noReads }, h(Handover, { draft: "drf_1", machines: [A], etag: () => SHOWN, seeChange: () => undefined, written: () => {} })))));
  const button = (markup: string) => /<button[^>]*>Start managing these machines<\/button>/.exec(markup)![0];

  test("with all three roles the button is on and the sentence says what the press does in order, and that nothing is sent to a machine", () => {
    const markup = page(["author", "publisher", "approver"]);
    expect(button(markup)).not.toContain("disabled");
    expect(markup).toContain(HANDOVER_SENTENCE);
    expect(HANDOVER_SENTENCE).toMatch(/publishes the draft, then creates a handover plan for each machine and approves each plan as you/);
    expect(HANDOVER_SENTENCE).toContain("It sends nothing to any machine.");
    expect(HANDOVER_SENTENCE).not.toMatch(/read|reviewed/);
  });

  test("a missing role turns the button off and names the role and the separate steps", () => {
    expect(missingRoles(["publisher", "approver"])).toEqual(["author"]);
    expect(missingRoles(["author", "approver"])).toEqual(["publisher"]);
    // Publisher and approver without author: the button is off and the separate steps are shown.
    for (const roles of [["publisher", "approver"], ["author", "approver"], ["author", "publisher"], ["publisher"], ["approver"], ["author"]] as const) {
      const markup = page(roles);
      expect(button(markup), roles.join()).toContain("disabled");
      expect(markup).toContain(MISSING(missingRoles(roles)).replace(/'/g, "&#x27;"));
    }
  });
});
