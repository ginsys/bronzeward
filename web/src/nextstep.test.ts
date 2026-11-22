// The machine page's "where you are, what next" line (ginsys/bronzeward#860), drawn from the `step` of
// GET /machines/{id}/position (ginsys/bronzeward#879) and worded from docs/spec/operator-experience.md §2.3.

import { createElement as h, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import type { components } from "./api/v1";
import { ReadsProvider, type Reads } from "./list.tsx";
import { NextStep, NextStepLine, nextStep, NOT_MANAGED_YET_STEP, type Position } from "./nextstep.tsx";

type Machine = components["schemas"]["MachineBody"];

const go = () => {};
const reads: Reads = { client: { read: () => new Promise(() => {}) }, settle: () => false, undropped: () => () => true, view: "", again: 0, regained: 0, go, failure: () => null };
const html = (e: ReactNode) => renderToStaticMarkup(h(ReadsProvider, { value: reads }, e));

const machine = (over: Partial<Machine> = {}): Machine => ({
  id: "mch_x", cluster: "cl_a", name: null, talosEndpoint: "10.0.0.1:50000", hardware: { talosNodeId: "n1", smbiosUuid: null }, platform: "metal",
  openDrift: null, frozen: false, scopeState: "normal", pendingConvergence: false, desired: null, applied: null, observed: null, ...over,
}) as Machine;
const at = "2026-11-18T10:00:00Z";
const ingestion = (state: string, leaseLapsed = false) => ({ id: "ing_1", kind: "import", state, draft: "drf_1", operation: "op_1", createdAt: at, leaseLapsed });
const plan = (over: Partial<NonNullable<Position["plan"]>> = {}) =>
  ({ id: "pln_1", state: "approved", operation: "apply-config", release: "rel_a", approvalCurrent: true, createdAt: at, expiresAt: at, ...over }) as NonNullable<Position["plan"]>;
const position = (step: string, over: Partial<Position> = {}): Position =>
  ({ machine: machine(), ingestion: null, draft: null, plan: null, operation: null, rolloutHeldBy: null, step, ...over }) as Position;

describe("each step", () => {
  test("not-handed-over keeps its wording and links to the import", () => {
    const s = nextStep(position("not-handed-over"));
    expect(s?.text).toBe(NOT_MANAGED_YET_STEP);
    expect(s?.text).toContain("writes nothing to it");
    expect(s?.links).toEqual([{ to: "/ingestions/new?machine=mch_x", label: "Import this machine", id: "mch_x" }]);
  });

  test("the import steps link to the ingestion; a draft to publish links to the draft", () => {
    for (const [step, state, lapsed] of [["import-running", "held", false], ["import-stalled", "held", true], ["import-paused", "paused", false]] as const) {
      expect(nextStep(position(step, { ingestion: ingestion(state, lapsed) as Position["ingestion"] }))?.links).toEqual([{ to: "/ingestions/ing_1", label: "The import", id: "ing_1" }]);
    }
    const d = nextStep(position("draft-to-publish", { ingestion: ingestion("released") as Position["ingestion"], draft: { id: "drf_1", state: "open", revision: 2 } }));
    expect(d?.text).toContain("Publishing sends nothing to the machine");
    expect(d?.links).toEqual([{ to: "/drafts/drf_1", label: "The draft", id: "drf_1" }]);
    expect(nextStep(position("import-stalled", { ingestion: ingestion("held", true) as Position["ingestion"] }))?.text).toContain("take it over");
  });

  test("needs-plan names both plan kinds and links to the creation page for the kind the machine's state offers", () => {
    const adopt = nextStep(position("needs-plan", { machine: machine({ desired: "rel_a", applied: null }) }));
    expect(adopt?.text).toContain("adopt");
    expect(adopt?.text).toContain("apply-config");
    expect(adopt?.links).toEqual([{ to: "/plans/new?release=rel_a&machine=mch_x&operation=adopt", label: "Create the adopt plan", id: "mch_x" }]);
    const applied = { release: "rel_0", source: "apply", importBase: null } as Machine["applied"];
    const apply = nextStep(position("needs-plan", { machine: machine({ desired: "rel_a", applied, pendingConvergence: true }) }));
    expect(apply?.links).toEqual([{ to: "/plans/new?release=rel_a&machine=mch_x&operation=apply-config", label: "Create the apply-config plan", id: "mch_x" }]);
  });

  test("needs-plan links to the machine's plans where the state offers no plan kind", () => {
    expect(nextStep(position("needs-plan"))?.links[0]?.to).toBe("/plans?machine=mch_x");
  });

  test("awaiting-approval says when the approval is from an earlier epoch", () => {
    expect(nextStep(position("awaiting-approval", { plan: plan({ state: "proposed", approvalCurrent: null }) }))?.text).toBe("A plan waits for approval.");
    expect(nextStep(position("awaiting-approval", { plan: plan({ approvalCurrent: false }) }))?.text).toContain("approved again");
  });

  test("awaiting-dispatch names what holds the plan by the plan's kind", () => {
    const apply = (over: Partial<Position>, recovery = false) => nextStep(position("awaiting-dispatch", { plan: plan(), ...over }), recovery)?.text;
    expect(apply({})).not.toContain("Held now");
    expect(apply({ machine: machine({ frozen: true }) })).toContain("the machine is frozen");
    expect(apply({ machine: machine({ scopeState: "pre-restore-unaccounted" }) })).toContain("scope state is pre-restore-unaccounted");
    expect(apply({ rolloutHeldBy: { id: "op_9", machine: "mch_y" } })).toContain("rollout scope (op_9)");
    // An adopt plan is held by neither a freeze nor another machine's rollout holder.
    const adopt = { plan: plan({ operation: "adopt" }), machine: machine({ frozen: true }), rolloutHeldBy: { id: "op_9", machine: "mch_y" } };
    expect(apply(adopt)).not.toContain("Held now");
    // Recovery mode: only a released scope admits a plan.
    expect(apply({}, true)).toContain("recovery mode");
    expect(apply({ machine: machine({ scopeState: "released" }) }, true)).not.toContain("Held now");
  });

  test("the operation steps name the operation's state and link to it", () => {
    const op = { id: "op_1", kind: "apply-config", state: "sending", plan: "pln_1", createdAt: at } as Position["operation"];
    const s = nextStep(position("operation-in-flight", { operation: op }));
    expect(s?.text).toContain("sending");
    expect(s?.links).toEqual([{ to: "/operations/op_1", label: "The operation", id: "op_1" }]);
    expect(nextStep(position("operation-unresolved", { operation: op }))?.text).toContain("no retry action");
  });

  test("drift-open says the node differs and links to the machine's drift page", () => {
    const s = nextStep(position("drift-open"));
    expect(s?.text).toContain("differs");
    expect(s?.links).toEqual([{ to: "/machines/mch_x/drift", label: "Decide on the change made on the machine", id: "mch_x" }]);
  });
});

describe("no line", () => {
  test("no-step, a step the client does not know, and no position give no line", () => {
    expect(nextStep(position("no-step"))).toBeUndefined();
    expect(nextStep(position("a-step-from-a-later-revision"))).toBeUndefined();
    expect(html(h(NextStep, { position: undefined }))).toBe("");
    expect(html(h(NextStepLine, { step: undefined }))).toBe("");
  });

  test("no line says unchanged or nothing to do", () => {
    for (const step of ["not-handed-over", "import-running", "needs-plan", "awaiting-approval", "awaiting-dispatch", "drift-open"]) {
      const t = nextStep(position(step, { plan: plan(), ingestion: ingestion("held") as Position["ingestion"] }))?.text.toLowerCase() ?? "";
      expect(t).not.toContain("unchanged");
      expect(t).not.toContain("nothing to do");
    }
  });
});

describe("the markup", () => {
  test("a step renders as one paragraph with its link", () => {
    const out = html(h(NextStep, { position: position("not-handed-over") }));
    expect(out).toContain('data-testid="next-step"');
    expect(out).toContain('href="/ingestions/new?machine=mch_x"');
    expect(out).toContain("Import this machine");
  });
});
