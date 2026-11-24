// The line of an approved plan that waits for another machine's hostname (client contracts A9 item 8, ginsys/bronzeward#1149).
// The server says who holds the Kubernetes node name in `hostnameHeldBy` of the machine's position and nothing of the name:
// the name is the `nodeName` of the plan's release for the machine, which the release's review carries.

import type { components } from "./api/v1";
import { useItem } from "./list.tsx";
import { machineText } from "./machines.tsx";
import type { Position } from "./nextstep.tsx";
import { safeText } from "./safe.ts";
import { HOST_WAITS_SHORT, hostWaits } from "./words.ts";

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

// The line for a position the page already holds. The review is read only where the position names a holder, and the line
// waits for that read: it never shows the short sentence while the name is on its way.
export function HostWaits({ position }: { position: Position | undefined }) {
  if (position === undefined || hostWaitsText(position, undefined) === undefined) return null;
  return <HostWaitsRead position={position} />;
}

function HostWaitsRead({ position }: { position: Position }) {
  const review = useItem<Review>(`/releases/${enc(position.plan!.release)}/machines/${enc(position.machine.id)}/review`);
  if (review.kind === "loading") return null;
  return <HostWaitsLine position={position} node={review.kind === "ok" && typeof review.data.hostname.nodeName === "string" ? review.data.hostname.nodeName : undefined} />;
}

// The plan page's line: the position of the plan's machine, read once, and only where it names this plan.
export function PlanHostWaits({ plan, machine }: { plan: string; machine: string }) {
  const pos = useItem<Position>(`/machines/${enc(machine)}/position`);
  return <HostWaits position={pos.kind === "ok" && pos.data.plan?.id === plan ? pos.data : undefined} />;
}
