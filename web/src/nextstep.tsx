// The machine page's "where you are, what next" line (ginsys/bronzeward#860), worded from
// docs/spec/operator-experience.md §2.3. It is built from the machine's own `desired` and `applied` alone, with
// no read of its own, so it is as current as the Desired and Applied rows beside it. It says nothing where the
// machine is not in the one state it names, and it never says "unchanged" from matching identifiers (§2.3).

import type { components } from "./api/v1";
import { Ref } from "./list.tsx";

type Machine = components["schemas"]["MachineBody"];

export interface Step { text: string; links: { to: string; label: string; id: string }[] }

export const NOT_HANDED_OVER =
  "Not handed over: no Desired and no Applied configuration is recorded for this machine. Next: import its configuration. An import reads the node and writes nothing to it.";

// A machine with no Desired and no Applied (and no open drift) is not handed over; the import is its next step.
// Any other state shows no line.
export function nextStep(m: Machine): Step | undefined {
  if (m.openDrift !== null || m.desired !== null || m.applied !== null) return undefined;
  return { text: NOT_HANDED_OVER, links: [{ to: `/ingestions/new?machine=${m.id}`, label: "Import this machine", id: m.id }] };
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

export const NextStep = ({ machine }: { machine: Machine }) => <NextStepLine step={nextStep(machine)} />;
