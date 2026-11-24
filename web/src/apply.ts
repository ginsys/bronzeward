// The apply page's logic (ginsys/bronzeward#1096; operator experience §5.6): a release's machines, chosen by the
// person, each get one apply-config plan ("Show apply"), then one approval each ("Apply"). Every step is decided
// from what the server says now: the machine's position (GET /machines/{id}/position), so a second press reuses a
// plan that is ready and repeats no act whose result exists. A plan the page made and the person did not approve is
// cancelled by the page. No React here: the page gives the reads and the writes.

import type { Load } from "./list.tsx";
import type { Position } from "./nextstep.tsx";
import { planBody } from "./plan.tsx";
import { HOST_WAITS_SHORT } from "./words.ts";
import type { Write, WriteSpec } from "./write.ts";

const enc = encodeURIComponent;

// /releases/<id>/apply: one release's apply page. The identifier is a release's own (`rel_…`) and one path segment.
export const applyPath = (release: string) => `/releases/${enc(release)}/apply`;
export function applyId(path: string): string | undefined {
  const m = /^\/releases\/([^/]+)\/apply$/.exec(path);
  if (m === null) return undefined;
  try {
    const id = decodeURIComponent(m[1]!);
    return /^rel_[a-z0-9]+$/.test(id) ? id : undefined;
  } catch {
    return undefined;
  }
}

export type RowState = "not-applied" | "ready" | "waiting" | "running" | "stopped" | "applied";
export type Line = { state: RowState | "working" | "done"; text: string; write?: Write };
export type Row = { state: RowState; text: string };

// Every sentence of the page's rows and notes, in one block.
export const LINES = {
  notApplied: "Not applied yet.",
  ready: "A plan is ready. It is not approved.",
  approved: "Approved. The controller sends it when its gates allow.",
  behind: "Approved. It waits behind another machine's apply: the controller applies one machine of a cluster at a time.",
  hostWaits: HOST_WAITS_SHORT,
  running: "Applying: the operation is in progress.",
  applied: "Applied: this release is what the machine runs.",
  unresolved: "Stopped: an operation on this machine is unresolved. The controller keeps examining it; open the machine to see it.",
  drift: "Stopped: the machine changed since Bronzeward last verified it. Open the machine to decide on the change first.",
  frozen: "Stopped: the machine is frozen. An approver can unfreeze it on the machine's page.",
  otherPlan: "Stopped: the plan waiting for this machine is not an apply plan. Open the machine to decide on it.",
  otherRelease: "Stopped: the plan waiting for this machine is for another release. Open the machine to decide on it.",
  unreadPlan: "The change of a plan could not be read, so Apply stays off until it can be: reload the page.",
  withheld: "cannot be shown: either the configuration could not be redacted, or the machine holds a configuration other than the one Bronzeward last applied, so the two cannot be compared. Apply stays off while this plan is listed. Open the plan to read what it records and approve it there, or untick this machine. The plan stays either way: cancel it on its own page if it is not wanted.",
  handedOver: "Apply stays off while this plan is listed. Open the plan to read what it records and approve it there, or untick this machine. The plan stays either way: cancel it on its own page if it is not wanted.",
  held: "A write of this page has an unknown outcome, so nothing new is sent. Repeat it or dismiss its notice at the top of the page first.",
  planning: "Creating the plan.",
  approving: "Approving the plan as you.",
  approvedNow: "Approved. The controller sends it when its gates allow; you can leave this page, it continues without it.",
  noRead: "Not read: the server did not answer. Press the button again.",
  noPlan: "The plan was created, but the answer named no plan. Open the machine to find it.",
  preparedBy: "Prepared by",
  madeHere: "Made by this page just now.",
  afterRefusal: "Nothing was approved for this machine. Press Show apply to read where it stands now: a plan that changed or expired is made again.",
  cancelReason: "Not approved: the apply page was left or its selection changed.",
} as const;

// Where a machine stands for this release, in words. A plan that is another release's is not this release's plan.
export function rowOf(p: Position, release: string): Row {
  const m = p.machine;
  const mine = p.plan !== null && p.plan.release === release && p.plan.operation === "apply-config";
  // A waiting plan that is not this release's apply plan is decided on the machine's page: this page applies. The plan
  // of another operation of this release (an adoption, say) and the plan of another release stop the row alike, and the
  // server would refuse a plan for the latter (an apply-config plan is for the machine's Desired release). The
  // command's applyStanding (cmd/bronzewardctl/apply.go) decides the same cases the same way.
  if (p.plan !== null && !mine && (p.step === "awaiting-approval" || p.step === "awaiting-dispatch")) return { state: "stopped", text: p.plan.release === release ? LINES.otherPlan : LINES.otherRelease };
  if (p.step === "operation-unresolved") return { state: "stopped", text: LINES.unresolved };
  if (p.step === "operation-in-flight") return { state: "running", text: LINES.running };
  if (p.step === "awaiting-dispatch" && mine) {
    if (p.hostnameHeldBy != null) return { state: "waiting", text: LINES.hostWaits };
    return p.rolloutHeldBy === null ? { state: "waiting", text: LINES.approved } : { state: "waiting", text: LINES.behind };
  }
  if (m.openDrift != null) return { state: "stopped", text: LINES.drift };
  if (m.frozen) return { state: "stopped", text: LINES.frozen };
  if (p.step === "awaiting-approval" && mine) return { state: "ready", text: LINES.ready };
  // Applied for this release: what the machine runs is this release's, or this release is the Desired one and leaves
  // the Applied configuration unchanged (planFor in plan.tsx offers no plan there either).
  if (m.applied != null && !m.pendingConvergence && (m.applied.release === release || m.desired === release)) return { state: "applied", text: LINES.applied };
  return { state: "not-applied", text: LINES.notApplied };
}

export interface ApplyIo {
  read<T>(path: string): Promise<Load<T> | undefined>;
  send(spec: WriteSpec): Promise<Write>;
  say(machine: string, line: Line): void;
  live(): boolean;
  // Whether the next request of a batch may go: the page is still here and no write of it has an unknown outcome.
  // Every send of both loops asks this, and nothing else.
  canSend(): boolean;
  // Whether the plan was handed over to the person (its withheld line was drawn): the page never cancels it.
  handedOver(id: string): boolean;
}

const done = (w: Write) => w.outcome?.kind === "done";
const idOf = (w: Write): string | undefined => {
  const d = w.outcome?.kind === "done" ? w.outcome.data : undefined;
  const id = typeof d === "object" && d !== null ? (d as { id?: unknown }).id : undefined;
  return typeof id === "string" && id !== "" ? id : undefined;
};

// "Show apply": the plan of each chosen machine, read from where it stands now. A plan that is ready (somebody
// prepared it) is reused and never made twice; a machine that cannot be planned is left out with its reason, and the
// others go on. `made` maps each plan this press created to its machine: the page may cancel those and no others. The
// caller passes its own map, which gains each plan the moment it is made. A plan whose creation is answered after the
// page stopped caring (the person left) is cancelled by that answer, since it is the only place the plan's ID is
// known. A creation whose outcome is unknown has no ID here: the machine's page shows any plan it made.
export async function showApply(release: string, machines: readonly string[], io: ApplyIo, made = new Map<string, string>()): Promise<{ plans: Map<string, string>; made: Map<string, string> }> {
  const plans = new Map<string, string>();
  for (const m of machines) {
    if (!io.live()) break;
    const p = await io.read<Position>(`/machines/${enc(m)}/position`);
    if (p?.kind !== "ok") {
      io.say(m, { state: "stopped", text: LINES.noRead });
      continue;
    }
    // A machine's line is its position's (the page draws it with rowOf): only what the press itself does is said here.
    const row = rowOf(p.data, release);
    if (row.state === "ready") {
      plans.set(m, p.data.plan!.id);
      continue;
    }
    if (row.state !== "not-applied") continue;
    const body = planBody(release, m, "apply-config", {});
    if ("error" in body) {
      io.say(m, { state: "stopped", text: body.error });
      continue;
    }
    if (!io.canSend()) break;
    io.say(m, { state: "working", text: LINES.planning });
    const w = await io.send({ method: "POST", path: "/plans", body: body.body, resource: "/plans" });
    if (!done(w)) {
      io.say(m, { state: "stopped", text: "", write: w });
      continue;
    }
    const id = idOf(w);
    if (id === undefined) {
      io.say(m, { state: "stopped", text: LINES.noPlan });
      continue;
    }
    if (!io.live()) {
      if (!io.handedOver(id)) await cancelPlans([id], io);
      break;
    }
    made.set(id, m);
    plans.set(m, id);
    io.say(m, { state: "ready", text: LINES.ready }); // ends the "working" note; the position's line speaks
  }
  return { plans, made };
}

// "Apply": one approval for each plan shown. A refusal stops that machine and no other. The answer is the plans
// that were approved, which the page must not cancel.
export async function applyPlans(plans: ReadonlyMap<string, string>, io: ApplyIo): Promise<Set<string>> {
  const approved = new Set<string>();
  for (const [m, id] of plans) {
    if (!io.canSend()) break;
    io.say(m, { state: "working", text: LINES.approving });
    const w = await io.send({ method: "POST", path: `/plans/${enc(id)}/approvals`, body: {}, resource: `/plans/${id}` });
    if (done(w)) approved.add(id);
    io.say(m, done(w) ? { state: "done", text: LINES.approvedNow } : { state: "stopped", text: "", write: w });
  }
  return approved;
}

// The plans this page made and the person did not approve. A refusal is ignored: a plan that cannot be cancelled
// expires by itself, and the page has left.
export async function cancelPlans(ids: Iterable<string>, io: Pick<ApplyIo, "send">): Promise<void> {
  for (const id of ids) await io.send({ method: "POST", path: `/plans/${enc(id)}/cancellations`, body: { reason: LINES.cancelReason }, resource: `/plans/${id}` });
}
