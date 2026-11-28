// The plan creation page (ginsys/bronzeward#357; decision B19 of #259; journeys §5.6; CLI row T19 of
// #258). /plans/new?release=&machine=&operation= sends POST /plans with the publisher's optional values,
// each left out when blank so the configured default applies, and goes to the new plan. It sends no
// approval and no apply: "Plan proposed. It is not approved, and nothing runs until an approver approves."

import { type FormEvent, type ReactNode, useEffect, useMemo, useState } from "react";
import type { components } from "./api/v1";
import { Result, startsNew, useSend, useWrites } from "./forms.tsx";
import { PlanHostWaits } from "./host-waits.tsx";
import { Panel } from "./layout.tsx";
import { type Load, Id, Ref, short, usePending, useReads, useSettled } from "./list.tsx";
import { PageNote } from "./page-note.tsx";
import type { Problem } from "./request.ts";
import { Action } from "./shell.tsx";
import { Status, when } from "./states.tsx";
import { MachineById, Named, useBody, useReleaseNames } from "./titles.tsx";
import { CREATE_PLAN_NOTE, UNFREEZE_THE_MACHINE } from "./words.ts";

type PlanInput = components["schemas"]["PlanInput"];
type Operation = PlanInput["operation"];
type Plan = components["schemas"]["PlanBody"];

const enc = encodeURIComponent;

export const PROPOSED = "Plan proposed. It is not approved, and nothing runs until an approver approves.";
export const NO_TARGET = "Not read: the address names no release and machine. Open the release and choose a plan.";

export const planPath = (release: string, machine: string, operation: Operation) =>
  `/plans/new?${new URLSearchParams({ release, machine, operation })}`;

// The drift page of a machine (wireframe W9): where a change made on the machine is decided and a machine is frozen.
export const driftPath = (machine: string) => `/machines/${enc(machine)}/drift`;

// The plan this machine's state offers for this release (B18): a handover for a machine with no Applied, an
// apply-config plan while the configuration differs. None where the text says nothing is left to send.
export function planFor(release: string, m: Pick<components["schemas"]["MachineBody"], "desired" | "applied" | "pendingConvergence">): "adopt" | "apply-config" | undefined {
  if (m.desired !== release) return undefined;
  if (m.applied === null) return "adopt";
  return m.pendingConvergence ? "apply-config" : undefined;
}

// The plans this browser just created: the plan page shows PROPOSED once for each, as the answer to the
// creation it came from. A reload shows the plan's own state line alone.
const fresh = new Set<string>();
export const takeFresh = (id: string) => fresh.delete(id);

// An optional value: its key in PlanInput, its label, its upper bound (api/openapi.yaml PlanInput). An
// adopt plan is offered no deadline and no attempt limit.
type Limit = { key: keyof PlanInput; label: string; max: number; apply: boolean };
export const LIMITS: readonly Limit[] = [
  { key: "expiresInSeconds", label: "Expires in (seconds)", max: 604800, apply: false },
  { key: "maxObservationAgeSeconds", label: "Longest age of the machine's last observation (seconds)", max: 604800, apply: false },
  { key: "checkValiditySeconds", label: "How long a check stays valid (seconds)", max: 604800, apply: false },
  { key: "transportDeadlineSeconds", label: "Transport deadline (seconds)", max: 604800, apply: true },
  { key: "verificationDeadlineSeconds", label: "Verification deadline (seconds)", max: 604800, apply: true },
  { key: "maxAttempts", label: "Attempt limit", max: 10, apply: true },
];

// The request body for the values typed, or what is wrong with them. A blank value is left out; an adopt
// plan carries none of the values only an apply-config plan takes.
export function planBody(release: string, machine: string, operation: Operation, typed: Readonly<Record<string, string>>): { body: PlanInput } | { error: string } {
  const body: Record<string, unknown> = { releaseId: release, machine, operation };
  if (operation === "apply-config") body["mode"] = "no-reboot";
  for (const l of LIMITS) {
    const text = (typed[l.key] ?? "").trim();
    if (text === "" || (operation === "adopt" && l.apply)) continue;
    const n = Number(text);
    if (!/^\d+$/.test(text) || n < 1 || n > l.max) return { error: `${l.label} must be a whole number from 1 to ${l.max}, or left blank.` };
    body[l.key] = n;
  }
  const t = body["transportDeadlineSeconds"], v = body["verificationDeadlineSeconds"];
  if (typeof t === "number" && typeof v === "number" && t > v) return { error: "The transport deadline must not be longer than the verification deadline." };
  return { body: body as unknown as PlanInput };
}

// What the operator does next after one of the server's refusals of a plan (internal/api/plans.go: the
// release is not the machine's Desired, the machine has no Applied, the machine has no assignment at all, the
// assignment moved since the release was compiled), and the two adopt refusals. The page acts on none of them.
// Another refusal has no next step here.
export function nextAfter(p: Problem | null, release: string, machine: string): ReactNode {
  const d = p?.detail ?? "";
  if (d.includes("is for the machine's Desired release")) {
    const desired = typeof p?.["desired"] === "string" ? p["desired"] : undefined;
    return <>Plan the machine's Desired release{desired !== undefined && <> <Named id={desired} /></>} instead. A published draft cannot be published again: to send this release's changes, change a new draft and publish it, then plan the release it makes.</>;
  }
  if (d.includes("has no Applied configuration")) {
    return <>Adopt the machine first: <Ref to={planPath(release, machine, "adopt")} id={machine}>create an adopt plan</Ref> for it. Nothing has been sent.</>;
  }
  if (d.includes("the machine has no assignment")) return <>Add an assignment for this machine to a draft (one that selects nothing is enough), publish that draft, then plan the release it makes.</>;
  if (d.includes("assignment has moved")) return <>Change a new draft and publish it, so a release is compiled from the machine's current assignment, then plan that release.</>;
  if (d.includes("adopt only a drifted machine")) return <>There is nothing to adopt. To send this release, create an apply-config plan for it.</>;
  return null;
}

function Create({ release, machine, first }: { release: string; machine: string; first: Operation }) {
  const writes = useWrites()!;
  const { go } = useReads();
  const [operation, setOperation] = useState<Operation>(first);
  const [note, setNote] = useState<string | undefined>();
  // The operation the last submit was for: what that submit's answer says is shown only under it.
  const [sentFor, setSentFor] = useState<Operation | undefined>();
  const [w, send] = useSend();
  const o = w?.outcome;
  const id = o?.kind === "done" && typeof o.data === "object" && o.data !== null ? (o.data as { id?: unknown }).id : undefined;
  const planned = typeof id === "string" ? id : undefined;
  // The write is recorded whether or not this page is still shown; the page opens the plan only while it is.
  useEffect(() => {
    if (planned === undefined) return;
    fresh.add(planned);
    go(`/plans/${enc(planned)}`);
  }, [planned, go]);
  const submit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const typed = Object.fromEntries([...new FormData(e.currentTarget)].map(([k, v]) => [k, String(v)]));
    const made = planBody(release, machine, operation, typed);
    setNote("error" in made ? made.error : undefined);
    if ("body" in made) {
      setSentFor(operation);
      send({ method: "POST", path: "/plans", body: made.body, resource: "/plans" });
    }
  };
  const mine = sentFor === operation;
  const next = mine && o?.kind === "refused" && o.status === 409 ? nextAfter(o.problem, release, machine) : null;
  const busy = (w !== undefined && !startsNew(writes, w)) || o?.kind === "done";
  return (
    <form onSubmit={submit} aria-label="Create plan">
      <p>Plan for machine <MachineById id={machine} /> and release <Named id={release} />.</p>
      <p>
        <label>Operation{" "}
          <select value={operation} onChange={(e) => (setOperation(e.target.value as Operation), setNote(undefined))} disabled={busy}>
            <option value="apply-config">apply-config (send this release, mode no-reboot)</option>
            <option value="adopt">adopt (records what the machine runs; sends nothing to it)</option>
          </select>
        </label>
      </p>
      <p>Leave a value blank to use the configured default.</p>
      {LIMITS.filter((l) => operation === "apply-config" || !l.apply).map((l) => (
        <p key={l.key}>
          <label>{l.label}{" "}<input name={l.key} inputMode="numeric" autoComplete="off" disabled={busy} /></label>
        </p>
      ))}
      <p><Action label="Create plan" needs={["publisher"]} submit busy={busy} /></p>
      {note !== undefined && <p role="alert" data-testid="plan-input">{note}</p>}
      {mine && <Result w={w} done={(): ReactNode => (planned === undefined ? <>The plan was created, but the answer named no plan. Find it in <Ref to="/plans" id="plans">the plans list</Ref>.</> : <>{PROPOSED} <Named id={planned} /></>)} />}
      {next !== null && <p data-testid="plan-next">{next}</p>}
    </form>
  );
}

// /plans/new: one page per release, machine and operation, so a move to another keeps none of the last one's values.
export function PlanNew({ query }: { query: string }) {
  const q = new URLSearchParams(query);
  const release = q.get("release") ?? "", machine = q.get("machine") ?? "";
  if (release === "" || machine === "") return <Status testid="no-target">{NO_TARGET}</Status>;
  const first: Operation = q.get("operation") === "adopt" ? "adopt" : "apply-config";
  return (
    <>
      <h2>Create a plan</h2>
      <PageNote parts={[CREATE_PLAN_NOTE]} />
      <Create key={`${release}/${machine}/${first}`} release={release} machine={machine} first={first} />
    </>
  );
}

// The plan page's lines about the plan's state (decisions B20 and B22 of ginsys/bronzeward#259, journeys §5.6).
// None of them sends anything: a link opens the creation page, which sends nothing until the operator submits it.
export const STALE_AFTER_RECOVERY = "This approval is from before the last recovery entry and authorizes nothing. An approver may approve again.";
export const CANCEL_NOTE = "Cancelling after the controller commits ends the operation only while no attempt is recorded.";
export const REASON_MAX = 1024;

const again = (p: Plan) => <> <Ref to={planPath(p.release, p.machine, p.operation as Operation)} id={p.id}>Create a new plan</Ref>.</>;
const TERMINAL: Record<string, (p: Plan) => ReactNode> = {
  expired: (p) => <>Expired at {when(p.expiresAt)}. Nothing runs from it.</>,
  cancelled: () => <>Cancelled. Nothing runs from it.</>,
  revoked: () => <>Its approval was revoked. Nothing runs from it.</>,
};

// What differs between the plan and the machine now: the Desired release and the drift record the plan was made on.
export function staleReasons(p: Pick<Plan, "desiredRelease" | "driftRecord">, m: Pick<components["schemas"]["MachineBody"], "desired" | "openDrift">, releaseName: (release: string) => string = short): string[] {
  const out: string[] = [];
  const name = (id: string | null) => (id === null ? "none" : releaseName(id));
  if (m.desired !== p.desiredRelease) out.push(`the machine's Desired release is now ${name(m.desired)}, and it was ${name(p.desiredRelease)}`);
  const drift = m.openDrift?.revision ?? null;
  if (drift !== p.driftRecord) out.push(`the machine's drift record is now ${drift ?? "none"}, and it was ${p.driftRecord ?? "none"}`);
  return out;
}

type Approval = components["schemas"]["ApprovalBody"];
type Identity = components["schemas"]["IdentityBody"];
type Machine = components["schemas"]["MachineBody"];
type Reads = { approval: Load<Approval>; now: Load<Identity>; machine: Load<Machine> };
const LOADING: Reads = { approval: { kind: "loading" }, now: { kind: "loading" }, machine: { kind: "loading" } };

// The plan is read again `wait` after `settled()`, and never before: the timer starts only when the last read settled,
// so a slow answer is not cancelled by the next read (ginsys/bronzeward#820). A second `settled()` restarts the one
// timer; `stop()` clears it.
export function poll(reread: () => void, wait = 5000) {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const stop = () => clearTimeout(timer);
  return { settled: () => { stop(); timer = setTimeout(reread, wait); }, stop };
}

// The approved plan's own reads, and the poll. One chain, so nothing is superseded while it is in flight: each settled
// read of the plan (`answers` counts them, so a read the shell did not answer counts too) starts the three reads, and
// five seconds after they are all answered the plan is read again. A read the shell did not answer (offline, a
// transport failure) keeps what is shown, and the poll goes on, for the supporting reads and for the plan's own.
function Approved({ p, reread, answers, onMachine }: { p: Plan; reread: () => void; answers: number; onMachine?: ((m: Machine | null) => void) | undefined }) {
  const settled = useSettled();
  const [seen, setSeen] = useState<Reads>(LOADING);
  const timer = useMemo(() => poll(reread), [reread]);
  useEffect(() => timer.stop, [timer]);
  useEffect(() => {
    let live = true;
    timer.stop();
    void (async () => {
      const [approval, now, machine] = await Promise.all([
        settled<Approval>(`/approvals/${enc(p.approval ?? "")}`, () => live),
        settled<Identity>("/identity", () => live),
        settled<Machine>(`/machines/${enc(p.machine)}`, () => live),
      ]);
      if (!live) return;
      setSeen((s) => ({ approval: approval ?? s.approval, now: now ?? s.now, machine: machine ?? s.machine }));
      // The plan's heading and target name the machine from this read: no read of their own (A8 rule 2).
      // A read that answered with no body (a 404, an exhausted 503) is reported as null, so the title survives it.
      if (machine !== undefined) onMachine?.(machine.kind === "ok" ? machine.data : null);
      timer.settled();
    })();
    return () => void (live = false);
  }, [p, answers, settled, timer, onMachine]);
  const { approval, now, machine } = seen;
  const pending = [usePending(approval, "the approval"), usePending(machine, "the machine")];
  const releaseName = useReleaseNames(machine.kind === "ok" ? machine.data.desired : undefined, p.desiredRelease);
  const reasons = machine.kind === "ok" ? staleReasons(p, machine.data, releaseName) : [];
  const old = approval.kind === "ok" && now.kind === "ok" && approval.data.epoch !== now.data.epoch;
  return (
    <>
      {(old || reasons.length > 0) && (
        <Status testid="plan-state">
          {old && <span data-testid="plan-old-approval">{STALE_AFTER_RECOVERY} </span>}
          {reasons.length > 0 && (
            <span data-testid="plan-stale">
              Predicted stale: {reasons.join(", and ")}. The controller reads the machine again when it commits and refuses a plan whose terms no longer hold.{again(p)}
            </span>
          )}
        </Status>
      )}
      {pending}
    </>
  );
}

export function PlanState({ p, reread, answers = 0, onMachine }: { p: Plan; reread: () => void; answers?: number; onMachine?: (m: Machine | null) => void }) {
  const terminal = TERMINAL[p.state];
  return (
    <>
      {terminal !== undefined && <Status testid="plan-terminal"><span className="badge">{p.state}</span> {terminal(p)}{again(p)}</Status>}
      {p.state === "approved" && <Approved p={p} reread={reread} answers={answers} onMachine={onMachine} />}
      {p.state === "approved" && p.operation === "apply-config" && <PlanHostWaits plan={p.id} machine={p.machine} answers={answers} />}
      {p.state === "committed" && p.committedOperation !== null && <Status testid="plan-committed">Committed: operation <Named id={p.committedOperation} />.</Status>}
    </>
  );
}

// A freeze holds every apply-config plan at commitment, a revert included. The line is the plan page's, for a plan that is
// still waiting: it says so and names the step that releases it. An approved plan reads its machine already (`machine`,
// the body its `Approved` reads), and a proposed one reads it here, so each plan state reads the machine once.
export const PLAN_FROZEN = "The machine is frozen: this plan cannot commit until an approver unfreezes the machine.";
function FrozenSaid({ p, machine }: { p: Plan; machine: Machine | null | undefined }) {
  if (machine?.id !== p.machine || !machine.frozen) return null;
  return <Status testid="plan-frozen">{PLAN_FROZEN} <Ref to={driftPath(p.machine)} id={p.machine}>{UNFREEZE_THE_MACHINE}</Ref>.</Status>;
}
function ProposedFrozen({ p }: { p: Plan }) {
  // The titles' store: the plan's heading and target ask for the same path, so the machine is read once.
  const m = useBody<Machine>(`/machines/${enc(p.machine)}`);
  return <FrozenSaid p={p} machine={m} />;
}
export function FrozenLine({ p, machine }: { p: Plan; machine: Machine | null | undefined }) {
  if (p.operation !== "apply-config") return null;
  if (p.state === "approved") return <FrozenSaid p={p} machine={machine} />;
  return p.state === "proposed" ? <ProposedFrozen p={p} /> : null;
}

export const reasonProblem = (text: string): string | undefined =>
  text.trim() === "" ? "Give a reason." : new TextEncoder().encode(text).length > REASON_MAX ? `The reason must be at most ${REASON_MAX} bytes.` : undefined;

// [Cancel plan] (B22): a reason of 1 to 1024 bytes, and the limit of a cancellation stated on the control.
export function CancelPlan({ id, reread }: { id: string; reread: () => void }) {
  const writes = useWrites()!;
  const [note, setNote] = useState<string | undefined>();
  const [w, send] = useSend();
  const done = w?.outcome?.kind === "done";
  useEffect(() => {
    if (done) reread();
  }, [done, reread]);
  const submit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const reason = String(new FormData(e.currentTarget).get("reason") ?? "");
    const bad = reasonProblem(reason);
    setNote(bad);
    if (bad === undefined) send({ method: "POST", path: `/plans/${enc(id)}/cancellations`, body: { reason }, resource: `/plans/${enc(id)}` });
  };
  const busy = (w !== undefined && !startsNew(writes, w)) || done;
  return (
    <Panel title="Cancel plan" label="Cancellation">
      <form onSubmit={submit} aria-label="Cancel plan">
        <p><label>Reason <input name="reason" autoComplete="off" disabled={busy} /></label></p>
        <p><Action label="Cancel plan" variant="destructive" needs={["publisher", "approver", "recovery-admin"]} submit busy={busy} /> {CANCEL_NOTE}</p>
        {note !== undefined && <p role="alert" data-testid="cancel-input">{note}</p>}
        <Result w={w} done={() => "Cancellation recorded."} />
      </form>
    </Panel>
  );
}
