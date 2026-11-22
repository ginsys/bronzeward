// The "where you are, what next" line of the machine page (ginsys/bronzeward#860) and of the release review page
// (ginsys/bronzeward#1077), worded from
// docs/spec/operator-experience.md §2.3. It is drawn from one answer and from nothing else: the `step` of
// `GET /machines/{id}/position` (ginsys/bronzeward#879), which the server decides from facts read in one
// snapshot. A step this client does not know reads as `no-step`, and `no-step` draws no line: it claims neither
// "nothing to do" nor "unchanged" (§2.3).

import type { components } from "./api/v1";
import { Ref } from "./list.tsx";
import { planFor, planPath } from "./plan.tsx";
import { NOT_MANAGED_YET } from "./words.ts";

export type Position = components["schemas"]["PositionBody"];

export interface Step { text: string; links: { to: string; label: string; id: string }[] }

export const NOT_MANAGED_YET_STEP =
  `${NOT_MANAGED_YET}: no Desired and no Applied configuration is recorded for this machine. Next: import its configuration. An import reads the node and writes nothing to it.`;

// What holds an approved plan, by the plan's kind (§2.3): an apply-config plan waits for a frozen machine, a scope
// state the scope-state rule refuses and another machine's rollout holder; an adopt plan waits only for the machine
// scope and the scope-state rule. In recovery mode only a `released` scope admits a plan.
function holds(p: Position, recovery: boolean): string[] {
  const out: string[] = [];
  const scope = p.machine.scopeState;
  if (p.plan?.operation === "apply-config" && p.machine.frozen) out.push("the machine is frozen");
  if (scope !== "normal" && scope !== "released") out.push(`the machine's scope state is ${scope}`);
  else if (recovery && scope !== "released") out.push("the server is in recovery mode and the machine's scope is not released");
  if (p.plan?.operation === "apply-config" && p.rolloutHeldBy !== null) out.push(`another machine's operation holds the cluster's rollout scope (${p.rolloutHeldBy.id})`);
  return out;
}

// The creation page for the plan the machine's state offers for its Desired release (the same choice the release
// page's plan button makes); the machine's plans where it offers none.
function needsPlanLinks(p: Position): Step["links"] {
  const m = p.machine.id;
  const kind = p.machine.desired === null ? undefined : planFor(p.machine.desired, p.machine);
  if (p.machine.desired === null || kind === undefined) return [{ to: `/plans?machine=${m}`, label: "Plans for this machine", id: m }];
  return [{ to: planPath(p.machine.desired, m, kind), label: kind === "adopt" ? "Create the adopt plan" : "Create the apply-config plan", id: m }];
}

// The line for a position, or undefined for `no-step` and for a value this client does not know.
export function nextStep(p: Position, recovery = false): Step | undefined {
  const m = p.machine.id;
  const ing = p.ingestion === null ? [] : [{ to: `/ingestions/${p.ingestion.id}`, label: "The import", id: p.ingestion.id }];
  const op = p.operation === null ? [] : [{ to: `/operations/${p.operation.id}`, label: "The operation", id: p.operation.id }];
  const plan = p.plan === null ? [] : [{ to: `/plans/${p.plan.id}`, label: "The plan", id: p.plan.id }];
  switch (p.step) {
    case "not-handed-over":
      return { text: NOT_MANAGED_YET_STEP, links: [{ to: `/ingestions/new?machine=${m}`, label: "Import this machine", id: m }] };
    case "import-stalled":
      return { text: "An import's runner has stopped: its lease lapsed and its staging is encrypted. Next: take it over to resume it, or abandon it.", links: ing };
    case "import-running":
      return { text: "An import of this machine is running.", links: ing };
    case "import-paused":
      return { text: "An import waits for review of the staged change.", links: ing };
    case "draft-to-publish":
      return {
        text: "The import is stored in an open draft. Next: publish it; that makes its release this machine's Desired release. Publishing sends nothing to the machine.",
        links: p.draft === null ? [] : [{ to: `/drafts/${p.draft.id}`, label: "The draft", id: p.draft.id }],
      };
    case "needs-plan":
      return {
        text: "A release is selected and not yet applied. Next: create a plan. With no Applied configuration the plan is an adopt (it accepts the configuration the node already runs, the handover); with an Applied configuration and no open drift it is an apply-config (it sends the release). The server refuses the other kind in each case.",
        links: needsPlanLinks(p),
      };
    case "awaiting-approval":
      return {
        text: p.plan?.approvalCurrent === false ? "A plan waits for approval. Its approval is from an earlier epoch: the plan must be approved again." : "A plan waits for approval.",
        links: plan,
      };
    case "awaiting-dispatch": {
      const held = holds(p, recovery);
      return {
        text: "A plan is approved and its approval is current. The controller runs it when its gates allow." + (held.length === 0 ? "" : ` Held now: ${held.join("; ")}.`),
        links: plan,
      };
    }
    case "operation-in-flight":
      return { text: `An operation is changing this machine (${p.operation?.state ?? "in flight"}). Next: wait for it to end.`, links: op };
    case "operation-unresolved":
      return {
        text: "An operation on this machine is unresolved: its outcome is not yet established. The controller keeps examining it and may settle it or classify it safe to retry on its own. This page offers no retry action; a recovery admin decides only an operation that stays unresolved.",
        links: op,
      };
    case "drift-open":
      // The drift view of operator experience §5.8 is not a page of this client yet, so the line links to none.
      return { text: "The node differs from what Bronzeward recorded. Next: adopt the node's configuration, or revert it.", links: [] };
    default:
      return undefined; // no-step, or a step a later API revision added
  }
}

export function NextStepLine({ step }: { step: Step | undefined }) {
  if (step === undefined) return null;
  return (
    <p data-testid="next-step">
      {step.text}{" "}
      {step.links.map((l, i) => <span key={l.to}>{i > 0 && " · "}<Ref to={l.to} id={l.id}>{l.label}</Ref></span>)}
    </p>
  );
}

export const NextStep = ({ position, recovery = false }: { position: Position | undefined; recovery?: boolean | undefined }) =>
  <NextStepLine step={position === undefined ? undefined : nextStep(position, recovery)} />;
