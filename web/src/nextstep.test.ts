// The machine page's "where you are, what next" line (ginsys/bronzeward#860): one sentence per state of
// docs/spec/operator-experience.md §2.3, first match of a stated order, and no line where a read is unknown.

import { createElement as h, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import type { components } from "./api/v1";
import { publishPath } from "./drafts.tsx";
import { ReadsProvider, type Reads } from "./list.tsx";
import { planPath } from "./plan.tsx";
import { type Known, newestCommitted, nextStep, NextStepLine, releasedDraft } from "./nextstep.tsx";

type Machine = components["schemas"]["MachineBody"];
type Ingestion = components["schemas"]["IngestionBody"];
type Plan = components["schemas"]["PlanBody"];
type Draft = components["schemas"]["DraftBody"];

const go = () => {};
const reads: Reads = { client: { read: () => new Promise(() => {}) }, settle: () => false, undropped: () => () => true, view: "", again: 0, regained: 0, go, failure: () => null };
const html = (e: ReactNode) => renderToStaticMarkup(h(ReadsProvider, { value: reads }, e));

const machine = (over: Partial<Machine>): Machine => ({
  id: "mch_x", cluster: "cl_a", talosEndpoint: "10.0.0.1:50000", hardware: { talosNodeId: "n1", smbiosUuid: null }, platform: "metal",
  openDrift: null, frozen: false, pendingConvergence: false, desired: null, applied: null, observed: null, ...over,
}) as Machine;
const ingestion = (over: Partial<Ingestion>): Ingestion => ({ id: "ing_x", kind: "import", mode: "x", state: "released", machine: "mch_x", draft: "drf_x", operation: null, review: null, ownerGeneration: 1, leaseUntil: "2026-11-18T10:00:00Z", expiresAt: "2026-11-19T10:00:00Z", createdAt: "2026-11-18T10:00:00Z", ...over }) as Ingestion;
const plan = (over: Partial<Plan>): Plan => ({ id: "pln_x", state: "proposed", machine: "mch_x", release: "rel_a", operation: "adopt", ...over }) as Plan;
const draft = (over: Partial<Draft>): Draft => ({ id: "drf_x", cluster: "cl_a", title: "t", state: "open", revision: 1, entries: [], ...over });

const fresh = machine({});
const published = machine({ desired: "rel_a" });
const behind = machine({ desired: "rel_a", applied: { release: "rel_old", source: "operation" }, pendingConvergence: true });
const links = (s: ReturnType<typeof nextStep>) => s?.links.map((l) => l.to);

describe("the line for each state, in §2.3's terms", () => {
  test("1: a machine with no Desired and no Applied is not handed over, and the import is offered", () => {
    const s = nextStep(fresh, { ingestions: [] });
    expect(s?.text).toContain("Not handed over");
    expect(s?.text).toContain("import");
    expect(links(s)).toEqual(["/ingestions/new?machine=mch_x"]);
  });

  test("2: an import still running is named, with its page", () => {
    for (const state of ["held", "paused", "resumed"]) {
      const s = nextStep(fresh, { ingestions: [ingestion({ id: "ing_1", state })] });
      expect(s?.text).toContain("import of this machine is not finished");
      expect(links(s)).toEqual(["/ingestions/ing_1"]);
    }
  });

  test("3: an import released into an open draft says publish that draft", () => {
    const s = nextStep(fresh, { ingestions: [ingestion({ draft: "drf_9" })], draft: draft({ id: "drf_9" }) });
    expect(s?.text).toContain("stored in draft");
    expect(s?.text).toContain("publisher");
    expect(links(s)).toEqual(["/drafts/drf_9", publishPath("drf_9")]);
  });

  test("3: a released import whose draft is not open falls back to the import offer", () => {
    expect(nextStep(fresh, { ingestions: [ingestion({ draft: "drf_9" })], draft: draft({ id: "drf_9", state: "discarded" }) })?.text).toContain("Not handed over");
  });

  test("4: a selected release with nothing applied asks a publisher for an adopt plan", () => {
    const s = nextStep(published, { proposed: [], approved: [], committed: [] });
    expect(s?.text).toContain("adopt plan");
    expect(links(s)).toEqual([planPath("rel_a", "mch_x", "adopt")]);
  });

  test("5: pending convergence asks a publisher for an apply-config plan", () => {
    const s = nextStep(behind, { proposed: [], approved: [], committed: [] });
    expect(s?.text).toContain("apply-config plan");
    expect(links(s)).toEqual([planPath("rel_a", "mch_x", "apply-config")]);
  });

  test("6: a proposed plan for the selected release asks an approver, and says approving does not apply", () => {
    const s = nextStep(published, { proposed: [plan({ id: "pln_1" })], approved: [], committed: [] });
    expect(s?.text).toContain("approver");
    expect(s?.text).toContain("does not apply");
    expect(links(s)).toEqual(["/plans/pln_1"]);
  });

  test("7: an approved plan for the selected release says the controller applies it", () => {
    const s = nextStep(behind, { proposed: [], approved: [plan({ id: "pln_2", state: "approved", operation: "apply-config" })], committed: [] });
    expect(s?.text).toContain("controller");
    expect(links(s)).toEqual(["/plans/pln_2"]);
  });

  test("a plan for another release is ignored", () => {
    expect(nextStep(published, { proposed: [plan({ release: "rel_other" })], approved: [], committed: [plan({ release: "rel_other", state: "committed" })] })?.text).toContain("adopt plan");
  });
});

describe("overlapping states: the first match of 2, 3, 7, 6, 5, 4, 1 wins", () => {
  test("2 over 3: a running import and a released one together name the running one", () => {
    const k: Known = { ingestions: [ingestion({ id: "ing_a", state: "released", draft: "drf_9" }), ingestion({ id: "ing_b", state: "held" })], draft: draft({ id: "drf_9" }) };
    expect(links(nextStep(fresh, k))).toEqual(["/ingestions/ing_b"]);
  });

  test("7 over 6: an approved and a proposed plan together name the approved one", () => {
    const k: Known = { proposed: [plan({ id: "pln_p" })], approved: [plan({ id: "pln_a", state: "approved" })], committed: [] };
    expect(links(nextStep(published, k))).toEqual(["/plans/pln_a"]);
  });

  test("the newest released import names its draft", () => {
    const older = ingestion({ id: "ing_o", draft: "drf_old", createdAt: "2026-11-17T10:00:00Z" });
    const newer = ingestion({ id: "ing_n", draft: "drf_new", createdAt: "2026-11-18T10:00:00Z" });
    expect(releasedDraft([older, newer])).toBe("drf_new");
    expect(releasedDraft([ingestion({ state: "abandoned" })])).toBeUndefined();
  });
});

describe("no line where the page cannot say", () => {
  test("a read not answered gives no line, never 'nothing to do' or a wrong step", () => {
    expect(nextStep(fresh, {})).toBeUndefined();
    expect(nextStep(fresh, { ingestions: [ingestion({ draft: "drf_9" })] })).toBeUndefined();
    expect(nextStep(published, { proposed: [], committed: [] })).toBeUndefined();
    expect(nextStep(published, { approved: [], committed: [] })).toBeUndefined();
    expect(nextStep(published, { proposed: [], approved: [] })).toBeUndefined();
  });

  test("a committed plan's operation decides: in flight or not read gives no line, a finished one does not hide the next step", () => {
    const committed = [plan({ id: "pln_c", state: "committed", operation: "apply-config", committedOperation: "op_c" })];
    const op = (state: string) => ({ id: "op_c", state }) as components["schemas"]["OperationBody"];
    const lists = { proposed: [], approved: [plan({ id: "pln_n", state: "approved" })], committed };
    for (const state of ["committed", "sending", "verifying", "unresolved"]) expect(nextStep(behind, { ...lists, operation: op(state) })).toBeUndefined();
    expect(nextStep(behind, lists)).toBeUndefined();
    // A completed adopt plan stays committed; the later plan's step is still shown.
    for (const state of ["completed", "rejected", "cancelled", "failed"]) expect(links(nextStep(behind, { ...lists, operation: op(state) }))).toEqual(["/plans/pln_n"]);
  });

  test("the newest committed plan of the selected release is the one whose operation counts", () => {
    const old = plan({ id: "pln_1", state: "committed", createdAt: "2026-11-17T10:00:00Z" });
    const recent = plan({ id: "pln_2", state: "committed", createdAt: "2026-11-18T10:00:00Z" });
    expect(newestCommitted([old, recent, plan({ id: "pln_3", state: "committed", release: "rel_other" })], "rel_a")?.id).toBe("pln_2");
    expect(newestCommitted([plan({ state: "approved" })], "rel_a")).toBeUndefined();
  });

  test("open drift gives no line, whatever the pending state and the plan reads say", () => {
    const drifted = machine({ desired: "rel_a", applied: { release: "rel_old", source: "operation" }, pendingConvergence: true, openDrift: { revision: 1, observation: "obs_1", lastObservation: "obs_1", lateLanding: null } });
    expect(nextStep(drifted, { proposed: [], approved: [], committed: [] })).toBeUndefined();
  });

  test("open drift, a selected release already applied, and Applied with no Desired give no line", () => {
    expect(nextStep(machine({ desired: "rel_a", applied: { release: "rel_a", source: "operation" } }), { proposed: [], approved: [], committed: [] })).toBeUndefined();
    expect(nextStep(machine({ applied: { release: "rel_a", source: "adoption" } }), { ingestions: [] })).toBeUndefined();
    expect(nextStep(machine({ desired: "rel_a", applied: { release: "rel_a", source: "operation" }, openDrift: { revision: 1, observation: "obs_1", lastObservation: "obs_1", lateLanding: null } }), { proposed: [], approved: [], committed: [] })).toBeUndefined();
  });

  test("a line never says unchanged", () => {
    for (const s of [nextStep(fresh, { ingestions: [] }), nextStep(published, { proposed: [], approved: [], committed: [] }), nextStep(behind, { proposed: [], approved: [], committed: [] })]) expect(s?.text.toLowerCase()).not.toContain("unchanged");
  });
});

describe("the markup", () => {
  test("a step renders as one paragraph with its links", () => {
    const out = html(h(NextStepLine, { step: nextStep(published, { proposed: [plan({ id: "pln_1" })], approved: [], committed: [] }) }));
    expect(out).toContain('data-testid="next-step"');
    expect(out).toContain('href="/plans/pln_1"');
  });

  test("no step renders nothing", () => {
    expect(html(h(NextStepLine, { step: undefined }))).toBe("");
  });
});
