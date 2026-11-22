// The handover button's logic (ginsys/bronzeward#1066; operator experience §5.6 for the web client):
// publish the draft, then for each machine create its adopt plan and approve it, each act the existing
// route called once. Every step is decided from what the server says now: the draft's state and the
// machine's position (`step`, GET /machines/{id}/position). So a second press, after a closed page or a
// failed step, continues from the recorded state and repeats no act whose result exists. Only a machine
// with no Applied configuration is acted on: one that has an Applied (a drift record included) keeps its
// separate steps. No React here: the page gives the reads and the writes.

import type { components } from "./api/v1";
import type { Load } from "./list.tsx";
import type { Position } from "./nextstep.tsx";
import { planBody } from "./plan.tsx";
import type { Write, WriteSpec } from "./write.ts";

type Draft = components["schemas"]["DraftBody"];

export const HANDOVER_TITLE = "Start managing these machines";
export const HANDOVER_SENTENCE =
  "This adds an assignment that selects nothing for each machine that has none, publishes the draft, then creates a handover plan for each machine and approves each plan as you, recorded as your own approval. It sends nothing to any machine. A change you apply later keeps its own plan and approval.";
export const UNSAVED = "The draft editor holds text you have not saved, and a press publishes only what is saved. Save it or discard it, then press again. Nothing was sent.";
// A write of an earlier press that stays open (in flight, of unknown outcome, or set aside) may still commit or be
// repeated, so a new press would send the same act under a new key: it waits for the notice's resolution.
export const UNRESOLVED = "A request of an earlier press has not finished: its outcome is unknown or it is still being sent. Repeat it from its notice or dismiss the notice, then press again, so no step is sent twice. Nothing was sent.";
// The press publishes (publisher), creates each plan (publisher) and approves it (approver), and the draft
// page's author role is what reads the draft's machines and their configurations: all three are needed.
export const ROLES = ["author", "publisher", "approver"] as const;
export const missingRoles = (held: readonly string[]) => ROLES.filter((r) => !held.includes(r));
export const MISSING = (missing: readonly string[]) =>
  `Needs the ${missing.join(" and ")} role. The separate steps stay: publish this draft, then on each machine's release page plan the adopt (handover), then approve each plan.`;
// A press publishes the draft as the page showed it: with no ETag kept for it, nothing is sent. This is the
// sentence of every draft write on the page (drafts.tsx re-exports it); it sits here so this file stays free of React.
export const NO_ETAG = "Not sent: this page has not read the draft under the current epoch. Read it again first.";

// The publication's 412: the draft changed since the page read it. The draft line offers "Show their change"
// (the page's own route for a refused draft write), and the press after it carries the new ETag.
export const NOT_PUBLISHED = "Not published: someone changed this draft since this page read it. Nothing was sent to any machine.";

// How long a machine that waits on the controller is followed on this page, and how often it is read. The
// publication is followed by the progress reader (watch), which has its own pace.
export const WAIT_MS = 2000;
export const WAIT_POLLS = 90;

// One line of the page's progress: the draft's, or one machine's.
export type Line = { state: "working" | "waiting" | "done" | "left" | "stopped"; text: string; write?: Write };

export const LINES = {
  publishing: "Publishing the draft.",
  published: "Published. Nothing was sent to any machine.",
  planning: "Creating the handover plan.",
  approving: "Approving the plan as you.",
  waiting: "The plan is approved. The controller records the handover when its gates allow; you can leave this page, it finishes without it.",
  slow: "Still waiting for the controller. It continues without this page. Press the button again to read where it is.",
  managed: "Managed. Bronzeward holds this machine's configuration; nothing was sent to it.",
  already: "Already managed.",
  applied: "Left as it is: this machine already has an applied configuration, so its changes keep their own steps. Open the machine to see the next one.",
  repeated: "Stopped: the machine's position did not change after this step. Open the machine to see where it is.",
  unresolved: "Stopped: an operation on this machine is unresolved. The controller keeps examining it; open the machine to see it.",
  draftGone: (state: string) => `This draft is ${state}, so it cannot be published.`,
  noRead: "Not read: the server did not answer. Press the button again.",
  publishUnread: "The publication's progress could not be read. Nothing was sent to any machine. Press the button again to read where it is.",
  noOperation: "The publication was accepted without naming its operation. Find it in the operations list, kind publish.",
  noPlan: "The plan was created, but the answer named no plan. Open the machine to find it.",
  notImported: "Left as it is: this machine has no stored import to hand over. Open the machine to see its next step.",
  stillOpen: "Stopped: the draft still reads open after its publication. Press the button again.",
  assigning: "Adding an assignment that selects nothing for each machine that has none, so each release holds one.",
  noAnswerETag: "Stopped: the answer to an assignment named no ETag for the draft, so the press cannot tell whether it changed since. Nothing more was sent. Read the draft again, then press the button again.",
  noAssignmentRead:"Not read: the machines' assignments could not be read. Nothing was sent. Press the button again.",
} as const;

export type Act =
  | { kind: "plan"; release: string }
  | { kind: "approve"; plan: string }
  | { kind: "wait" }
  | { kind: "managed" }
  | { kind: "stop"; text: string };

// What to do next for a machine that had no Applied when the press began.
export function actFor(p: Position): Act {
  if (p.machine.applied !== null) return { kind: "managed" };
  switch (p.step) {
    case "needs-plan":
      return p.machine.desired === null ? { kind: "stop", text: LINES.notImported } : { kind: "plan", release: p.machine.desired };
    case "awaiting-approval":
      return p.plan === null ? { kind: "stop", text: LINES.repeated } : { kind: "approve", plan: p.plan.id };
    case "awaiting-dispatch":
    case "operation-in-flight":
      return { kind: "wait" };
    case "operation-unresolved":
      return { kind: "stop", text: LINES.unresolved };
    case "draft-to-publish":
      return { kind: "stop", text: LINES.stillOpen };
    default:
      return { kind: "stop", text: LINES.notImported };
  }
}

// How the publication ended, as the progress reader (watch) and publishEnd read it: a release was named, the
// words that say why none was, the page left, or the operation could not be read.
export type Followed = { kind: "released" } | { kind: "ended"; text: string } | { kind: "stopped" } | { kind: "unread" };

export interface Io {
  read<T>(path: string): Promise<Load<T> | undefined>;
  // Follows the publish operation to its end.
  follow(operation: string): Promise<Followed>;
  // The write sent and settled; its `outcome` says how it ended.
  send(spec: WriteSpec): Promise<Write>;
  wait(ms: number): Promise<void>;
  say(key: string, line: Line): void;
  live(): boolean;
}

const enc = encodeURIComponent;
export const DRAFT = "draft";
const done = (w: Write) => w.outcome?.kind === "done";
const idOf = (w: Write): string | undefined => {
  const d = w.outcome?.kind === "done" ? w.outcome.data : undefined;
  const id = typeof d === "object" && d !== null ? (d as { id?: unknown }).id : undefined;
  return typeof id === "string" && id !== "" ? id : undefined;
};

// The machines that already have an assignment: an entry of the draft, or a head on the server (every page read).
async function assigned(d: Draft, io: Io): Promise<Set<string> | undefined> {
  const held = new Set(d.entries.flatMap((e) => (e.kind === "assignment" && e.machine !== undefined ? [e.machine] : [])));
  for (let cursor: string | undefined; ; ) {
    const r = await io.read<{ items: { machine: string }[]; next?: string }>(cursor === undefined ? "/assignments" : `/assignments?cursor=${enc(cursor)}`);
    if (r?.kind !== "ok") return undefined;
    for (const a of r.data.items) held.add(a.machine);
    if (r.data.next === undefined) return held;
    cursor = r.data.next;
  }
}

// The draft: published first when it is still open; true when the machines can follow. `etag` is the draft's
// ETag as the page showed it: the publication carries it, so a draft another author changed since is refused
// (412) and never published unseen, and with none the press sends nothing.
async function publish(id: string, machines: readonly string[], io: Io, etag: string | undefined): Promise<boolean> {
  const say = (line: Line) => io.say(DRAFT, line);
  const d = await io.read<Draft>(`/drafts/${enc(id)}`);
  if (d?.kind !== "ok") return say({ state: "stopped", text: LINES.noRead }), false;
  if (d.data.state === "published") return say({ state: "done", text: "Already published." }), true;
  if (d.data.state !== "open") return say({ state: "stopped", text: LINES.draftGone(d.data.state) }), false;
  if (etag === undefined) return say({ state: "stopped", text: NO_ETAG }), false;
  // Every write is preceded by this check: the press stops where the page left or the shell invalidated its reads.
  if (!io.live()) return false;
  // A machine connected before this change holds an import and no assignment, and a plan binds an assignment
  // revision: the press adds an assignment that selects nothing for each, in the draft it is about to publish.
  const held = await assigned(d.data, io);
  if (held === undefined) return say({ state: "stopped", text: LINES.noAssignmentRead }), false;
  let tag: string = etag;
  for (const m of machines.filter((m) => !held.has(m))) {
    if (!io.live()) return false;
    say({ state: "working", text: LINES.assigning });
    const w = await io.send({ method: "PUT", path: `/drafts/${enc(id)}/assignments/${enc(m)}`, body: { profiles: [], fragments: {} }, resource: `/drafts/${id}`, ifMatch: tag });
    if (!done(w)) return say({ state: "stopped", text: "", write: w }), false;
    // The next write carries the ETag this answer gave. An answer without one stops the press: a draft read in its
    // place could hold another author's change that the remaining writes and the publication would then pass over.
    const next = w.outcome?.kind === "done" ? w.outcome.etag : undefined;
    if (next === undefined) return say({ state: "stopped", text: LINES.noAnswerETag }), false;
    tag = next;
  }
  if (!io.live()) return false;
  say({ state: "working", text: LINES.publishing });
  // The server answers a publication already queued or running for this draft revision with that operation
  // (persistence and API §7.3, its natural key), so a press while one runs follows it and creates no second.
  const w = await io.send({ method: "POST", path: `/drafts/${enc(id)}/publications`, body: {}, resource: `/drafts/${id}`, ifMatch: tag });
  if (!done(w)) return say({ state: "stopped", text: "", write: w }), false;
  const op = idOf(w);
  if (op === undefined) return say({ state: "stopped", text: LINES.noOperation }), false;
  const f = await io.follow(op);
  switch (f.kind) {
    case "released":
      return say({ state: "done", text: LINES.published }), true;
    case "ended":
      return say({ state: "stopped", text: f.text }), false;
    case "unread":
      return say({ state: "stopped", text: LINES.publishUnread }), false;
    case "stopped":
      return false;
  }
}

// One machine, from where the server says it is, to managed or to the step that stopped it.
async function machine(m: string, io: Io): Promise<void> {
  const say = (line: Line) => io.say(m, line);
  const did = new Set<string>();
  let first = true;
  let polls = 0;
  while (io.live()) {
    const p = await io.read<Position>(`/machines/${enc(m)}/position`);
    if (p?.kind !== "ok") return say({ state: "stopped", text: LINES.noRead });
    if (first && p.data.machine.applied !== null) return say(p.data.step === "no-step" ? { state: "done", text: LINES.already } : { state: "left", text: LINES.applied });
    first = false;
    const a = actFor(p.data);
    if (a.kind === "managed") return say({ state: "done", text: LINES.managed });
    if (a.kind === "stop") return say({ state: a.text === LINES.notImported ? "left" : "stopped", text: a.text });
    if (a.kind === "wait") {
      if (++polls > WAIT_POLLS) return say({ state: "waiting", text: LINES.slow });
      say({ state: "waiting", text: LINES.waiting });
      await io.wait(WAIT_MS);
      continue;
    }
    // The same act twice in one press means the position did not move after it: stop, never loop.
    const key = a.kind === "plan" ? `plan:${a.release}` : `approve:${a.plan}`;
    if (did.has(key)) return say({ state: "stopped", text: LINES.repeated });
    did.add(key);
    // The position was read before the wait for this turn: the press may since have been invalidated.
    if (!io.live()) return;
    if (a.kind === "plan") {
      say({ state: "working", text: LINES.planning });
      const made = planBody(a.release, m, "adopt", {});
      if ("error" in made) return say({ state: "stopped", text: made.error });
      const w = await io.send({ method: "POST", path: "/plans", body: made.body, resource: "/plans" });
      if (!done(w)) return say({ state: "stopped", text: "", write: w });
      if (idOf(w) === undefined) return say({ state: "stopped", text: LINES.noPlan });
    } else {
      say({ state: "working", text: LINES.approving });
      const w = await io.send({ method: "POST", path: `/plans/${enc(a.plan)}/approvals`, body: {}, resource: `/plans/${a.plan}` });
      if (!done(w)) return say({ state: "stopped", text: "", write: w });
    }
  }
}

// The press: the draft first, then every machine at once (each is its own record and may fail alone).
export async function handOver(draft: string, machines: readonly string[], io: Io, etag: string | undefined): Promise<void> {
  if (!(await publish(draft, machines, io, etag))) return;
  await Promise.all(machines.map((m) => machine(m, io)));
}
