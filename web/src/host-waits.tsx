// The line of an approved plan that waits for another machine's hostname (client contracts A9 item 8, ginsys/bronzeward#1149).
// The server says who holds the Kubernetes node name in `hostnameHeldBy` of the machine's position and nothing of the name:
// the name is the `nodeName` of the plan's release for the machine, which the release's review carries.

import type { components } from "./api/v1";
import type { ReactNode } from "react";
import { ReadsProvider, useItem, useReads } from "./list.tsx";
import { machineText } from "./machines.tsx";
import type { Position } from "./nextstep.tsx";
import { safeText } from "./safe.ts";
import { MachineById } from "./titles.tsx";
import { HOST_REFUSAL_BEFORE, HOST_WAITS_SHORT, hostRefusalAfter, hostWaits } from "./words.ts";

type Review = components["schemas"]["ReviewBody"];

const enc = encodeURIComponent;

// The sentence for a position, or undefined where it names no holder, or its plan is not an approved apply-config plan.
// `node` is the plan's release's node name for the machine; where it is not known (the review could not be read, or
// holds none, or none that is safe text) the contract's short sentence stands, and no more.
export function hostWaitsText(p: Position, node: string | undefined): string | undefined {
  const holder = p.hostnameHeldBy; // a server older than the member serves none
  if (holder == null || p.plan == null || p.plan.operation !== "apply-config" || p.plan.state !== "approved") return undefined;
  const name = node === undefined ? undefined : safeText(node);
  if (name === undefined || name === "") return HOST_WAITS_SHORT;
  return hostWaits(machineText(holder), name, machineText(p.machine));
}

export function HostWaitsLine({ position, node }: { position: Position; node: string | undefined }) {
  const text = hostWaitsText(position, node);
  return text === undefined ? null : <p data-testid="host-waits">{text}</p>;
}

// The machine timeline's refusal entry of comparison 6a, in the contract's sentence (A9 item 8), or undefined where the entry
// is not that or lacks either member (an entry written before them stays as it is). The holder is named at read time as
// A8 rule 1 names a machine, so a rename leaves no stale name; the node name shows only where it is safe text.
export function hostRefusalNode(kind: string, r: Record<string, unknown>): ReactNode | undefined {
  const holder = r["holder"];
  const node = typeof r["nodeName"] === "string" ? safeText(r["nodeName"]) : undefined;
  if (kind !== "refusal" || r["comparison"] !== "6a" || typeof holder !== "string" || !/^[a-z]+_[a-z2-7]{26}$/.test(holder) || node === undefined || node === "") return undefined;
  return <>{HOST_REFUSAL_BEFORE}<MachineById id={holder} />{hostRefusalAfter(node)}</>;
}

// The line for a position the page already holds. The review is read only where the position names a holder, and the line
// waits for that read: it never shows the short sentence while the name is on its way.
export function HostWaits({ position }: { position: Position | undefined }) {
  if (position === undefined || hostWaitsText(position, undefined) === undefined) return null;
  return <HostWaitsRead position={position} />;
}

// The review is no part of the machine page's three counted reads (machine, observations, position): it settles through the
// shell's own settle, or a fourth settle there would start the next generation before the observations answered.
function HostWaitsRead({ position }: { position: Position }) {
  const reads = useReads();
  return (
    <ReadsProvider value={{ ...reads, settle: reads.shell ?? reads.settle }}>
      <HostWaitsReview position={position} />
    </ReadsProvider>
  );
}

function HostWaitsReview({ position }: { position: Position }) {
  const review = useItem<Review>(`/releases/${enc(position.plan!.release)}/machines/${enc(position.machine.id)}/review`);
  if (review.kind === "loading") return null;
  return <HostWaitsLine position={position} node={review.kind === "ok" && typeof review.data.hostname.nodeName === "string" ? review.data.hostname.nodeName : undefined} />;
}

// The plan page's line: the position of the plan's machine, read again at each answer of the plan's poll (`answers`), and
// only where it names this plan.
export function PlanHostWaits({ plan, machine, answers = 0 }: { plan: string; machine: string; answers?: number }) {
  const pos = useItem<Position>(`/machines/${enc(machine)}/position`, undefined, answers);
  return <HostWaits position={pos.kind === "ok" && pos.data.plan?.id === plan ? pos.data : undefined} />;
}
