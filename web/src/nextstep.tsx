// The machine page's "where you are, what next" line (ginsys/bronzeward#860), worded from
// docs/spec/operator-experience.md §2.3. It is built from the machine's Desired and Applied and from the
// reads below, and shows nothing where a read is unknown: a missing line is honest, a wrong one is not.
// It never says "unchanged" from matching identifiers (§2.3).

import { createContext, useContext, useMemo } from "react";
import type { components } from "./api/v1";
import { publishPath } from "./drafts.tsx";
import { type Keep, type Reads, ReadsProvider, Ref, short, useItem, useList, useReads } from "./list.tsx";
import { planPath } from "./plan.tsx";
import { planFor } from "./review.tsx";

type Machine = components["schemas"]["MachineBody"];
type Ingestion = components["schemas"]["IngestionBody"];
type Plan = components["schemas"]["PlanBody"];
type Draft = components["schemas"]["DraftBody"];
type Operation = components["schemas"]["OperationBody"];

export interface Step { text: string; links: { to: string; label: string; id: string }[] }

// What the page has read; undefined is a read not answered (loading or failed), never an empty answer.
export interface Known {
  ingestions?: Ingestion[] | undefined; draft?: Draft | undefined; proposed?: Plan[] | undefined; approved?: Plan[] | undefined;
  // The machine's operations that still hold its scope, every state read.
  held?: Operation[] | undefined;
}

const RUNNING = new Set(["held", "paused", "resumed"]);
// The operation states that still hold the machine's scope; completed, rejected, cancelled and failed are terminal
// (execution and recovery, the operation state table). `committed` is also a plan's terminal state, which says
// nothing of its operation, so the operations are read themselves.
export const HOLDING = ["committed", "sending", "verifying", "unresolved"] as const;

// The page shows the line only from a machine read that answered and was not found stale since.
export const lineMachine = (m: { kind: string; data?: Machine }, stale: boolean): Machine | undefined => (m.kind === "ok" && !stale ? m.data : undefined);

// The draft of the newest import of this machine that stored one (state `released`).
export const releasedDraft = (ingestions: readonly Ingestion[]): string | undefined =>
  ingestions.filter((i) => i.state === "released" && i.draft !== "").sort((a, b) => b.createdAt.localeCompare(a.createdAt))[0]?.draft;

// The first match of this order wins: 2 an import not finished, 3 an import stored in an open draft, 7 an
// approved plan, 6 a proposed plan, 5 an apply-config plan to create, 4 an adopt plan to create, 1 not handed
// over. States 2, 3 and 1 are for a machine with no Desired and no Applied; 7, 6, 5 and 4 for one whose selected
// release is not carried by what Bronzeward applied. Any other state, and any unknown read, shows no line.
export function nextStep(m: Machine, k: Known): Step | undefined {
  if (m.openDrift !== null) return undefined;
  if (m.desired === null && m.applied === null) {
    if (k.ingestions === undefined) return undefined;
    const running = k.ingestions.find((i) => RUNNING.has(i.state));
    if (running !== undefined) {
      return { text: `An import of this machine is not finished (${running.state}). Next: open the import.`, links: [{ to: `/ingestions/${running.id}`, label: "Open the import", id: running.id }] };
    }
    const stored = releasedDraft(k.ingestions);
    if (stored !== undefined) {
      if (k.draft === undefined) return undefined;
      if (k.draft.id === stored && k.draft.state === "open") {
        return {
          text: `The import was stored in draft ${short(stored)}, which is open. Next: edit it if needed, then a publisher publishes it. Publishing makes a release this machine's Desired and sends nothing to the machine.`,
          links: [{ to: `/drafts/${stored}`, label: "Open the draft", id: stored }, { to: publishPath(stored), label: "Publish this draft", id: stored }],
        };
      }
    }
    return {
      text: "Not handed over: no Desired and no Applied configuration is recorded for this machine. Next: import its configuration. An import reads the node and writes nothing to it.",
      links: [{ to: `/ingestions/new?machine=${m.id}`, label: "Import this machine", id: m.id }],
    };
  }
  const op = m.desired === null ? undefined : planFor(m.desired, m);
  if (m.desired === null || op === undefined || k.proposed === undefined || k.approved === undefined) return undefined;
  const release = m.desired;
  // An operation that holds the machine's scope, or operations not read: no line.
  if (k.held === undefined || k.held.length > 0) return undefined;
  const approved = k.approved.find((p) => p.release === release);
  if (approved !== undefined) {
    return { text: `Plan ${short(approved.id)} for the selected release is approved. Next: the controller commits and applies it. Nothing is left for you to do.`, links: [{ to: `/plans/${approved.id}`, label: "Open the plan", id: approved.id }] };
  }
  const proposed = k.proposed.find((p) => p.release === release);
  if (proposed !== undefined) {
    return { text: `Plan ${short(proposed.id)} for the selected release is proposed. Next: an approver approves it. Approving does not apply it.`, links: [{ to: `/plans/${proposed.id}`, label: "Open the plan", id: proposed.id }] };
  }
  return op === "apply-config"
    ? { text: "The selected release differs from what Bronzeward applied (pending convergence). Next: a publisher creates an apply-config plan; an approver then approves it.", links: [{ to: planPath(release, m.id, op), label: "Create an apply-config plan", id: m.id }] }
    : { text: "A release is selected for this machine and nothing is applied. Next: a publisher creates an adopt plan (handover); an approver then approves it.", links: [{ to: planPath(release, m.id, op), label: "Create an adopt plan", id: m.id }] };
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

const enc = encodeURIComponent;

// These reads keep nothing: a read again that finds no answer must not leave the last answer's guidance on
// the page, since a line that says what to do next is wrong on data the page can no longer vouch for.
const NO_KEEP: Keep = { held: () => false, lost: () => {} };

function StoredDraft({ machine, ingestions, draft }: { machine: Machine; ingestions: Ingestion[]; draft: string }) {
  const item = useItem<Draft>(`/drafts/${enc(draft)}`, NO_KEEP);
  return <NextStepLine step={nextStep(machine, { ingestions, draft: item.kind === "ok" ? item.data : undefined })} />;
}

// A machine with no Desired and no Applied: its imports (one list, by machine), then the draft of the
// newest one that stored a draft.
function ImportStep({ machine }: { machine: Machine }) {
  const [list] = useList<Ingestion>("/ingestions", new URLSearchParams({ machine: machine.id }).toString(), true, NO_KEEP);
  if (list.kind !== "ok") return null;
  const ingestions = list.data.items;
  const stored = ingestions.some((i) => RUNNING.has(i.state)) ? undefined : releasedDraft(ingestions);
  return stored === undefined ? <NextStepLine step={nextStep(machine, { ingestions })} /> : <StoredDraft machine={machine} ingestions={ingestions} draft={stored} />;
}

// A machine whose selected release is not what Bronzeward applied: its proposed and approved plans (two lists,
// by machine and state) and the operations that hold its scope (four lists, by machine and state; usually empty).
function PlanStep({ machine }: { machine: Machine }) {
  const q = (state: string) => new URLSearchParams({ machine: machine.id, state }).toString();
  const [proposed] = useList<Plan>("/plans", q("proposed"), true, NO_KEEP);
  const [approved] = useList<Plan>("/plans", q("approved"), true, NO_KEEP);
  const [a] = useList<Operation>("/operations", q(HOLDING[0]), true, NO_KEEP);
  const [b] = useList<Operation>("/operations", q(HOLDING[1]), true, NO_KEEP);
  const [c] = useList<Operation>("/operations", q(HOLDING[2]), true, NO_KEEP);
  const [d] = useList<Operation>("/operations", q(HOLDING[3]), true, NO_KEEP);
  const items = <T,>(l: { kind: string; data?: { items: T[] } }) => (l.kind === "ok" ? l.data?.items : undefined);
  const ops = [a, b, c, d].map(items);
  const held = ops.some((o) => o === undefined) ? undefined : ops.flatMap((o) => o ?? []);
  return <NextStepLine step={nextStep(machine, { proposed: items(proposed), approved: items(approved), held })} />;
}

// The page counts the settled reads of the machine and its observations to decide when to read them again
// (countingSettle in machine.tsx); this line's reads must not count, so they settle through the page's own settle.
export const OuterSettle = createContext<Reads["settle"] | undefined>(undefined);

export function NextStep({ machine }: { machine: Machine }) {
  const reads = useReads();
  const outer = useContext(OuterSettle);
  const own = useMemo(() => (outer === undefined ? reads : { ...reads, settle: outer }), [reads, outer]);
  const wanted = machine.openDrift === null && (machine.desired === null ? machine.applied === null : planFor(machine.desired, machine) !== undefined);
  if (!wanted) return null;
  return <ReadsProvider value={own}>{machine.desired === null ? <ImportStep machine={machine} /> : <PlanStep machine={machine} />}</ReadsProvider>;
}
