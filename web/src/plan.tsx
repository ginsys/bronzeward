// The plan creation page (ginsys/bronzeward#357; decision B19 of #259; journeys §5.6; CLI row T19 of
// #258). /plans/new?release=&machine=&operation= sends POST /plans with the publisher's optional values,
// each left out when blank so the configured default applies, and goes to the new plan. It sends no
// approval and no apply: "Plan proposed. It is not approved, and nothing runs until an approver approves."

import { type FormEvent, type ReactNode, useEffect, useState } from "react";
import type { components } from "./api/v1";
import { Result, startsNew, useSend, useWrites } from "./forms.tsx";
import { Id, Ref, useReads } from "./list.tsx";
import type { Problem } from "./request.ts";
import { Action } from "./shell.tsx";
import { Status } from "./states.tsx";

type PlanInput = components["schemas"]["PlanInput"];
type Operation = PlanInput["operation"];

const enc = encodeURIComponent;

export const PROPOSED = "Plan proposed. It is not approved, and nothing runs until an approver approves.";
export const NO_TARGET = "Not read: the address names no release and machine. Open the release and choose a plan.";

export const planPath = (release: string, machine: string, operation: Operation) =>
  `/plans/new?${new URLSearchParams({ release, machine, operation })}`;

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

// What the operator does next after one of the server's three refusals of a plan (internal/api/plans.go: the
// release is not the machine's Desired, the machine has no Applied, the assignment moved since the release was
// compiled), and the two adopt refusals. The page acts on none of them. Another refusal has no next step here.
export function nextAfter(p: Problem | null, release: string, machine: string): ReactNode {
  const d = p?.detail ?? "";
  if (d.includes("is for the machine's Desired release")) {
    const desired = typeof p?.["desired"] === "string" ? p["desired"] : undefined;
    return <>Plan the machine's Desired release{desired !== undefined && <> <Id id={desired} /></>} instead, or publish again to make this release Desired.</>;
  }
  if (d.includes("has no Applied configuration")) {
    return <>Hand the machine over first: <Ref to={planPath(release, machine, "adopt")} id={machine}>create an adopt plan</Ref> for it. Nothing has been sent.</>;
  }
  if (d.includes("assignment has moved")) return <>Publish the draft again, so a release is compiled from the machine's current assignment, then plan that release.</>;
  if (d.includes("adopt only a drifted machine")) return <>There is nothing to adopt. To send this release, create an apply-config plan for it.</>;
  return null;
}

function Create({ release, machine, first }: { release: string; machine: string; first: Operation }) {
  const writes = useWrites()!;
  const { go } = useReads();
  const [operation, setOperation] = useState<Operation>(first);
  const [note, setNote] = useState<string | undefined>();
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
    if ("body" in made) send({ method: "POST", path: "/plans", body: made.body, resource: "/plans" });
  };
  const next = o?.kind === "refused" && o.status === 409 ? nextAfter(o.problem, release, machine) : null;
  const busy = (w !== undefined && !startsNew(writes, w)) || o?.kind === "done";
  return (
    <form onSubmit={submit} aria-label="Create plan">
      <p>Plan for machine <Id id={machine} /> and release <Id id={release} />.</p>
      <p>
        <label>Operation{" "}
          <select value={operation} onChange={(e) => setOperation(e.target.value as Operation)} disabled={busy}>
            <option value="apply-config">apply-config (send this release, mode no-reboot)</option>
            <option value="adopt">adopt (hand over: sends nothing to the machine)</option>
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
      <Result w={w} done={(): ReactNode => (planned === undefined ? <>The plan was created, but the answer named no plan. Find it in <Ref to="/plans" id="plans">the plans list</Ref>.</> : <>{PROPOSED} <Ref to={`/plans/${enc(planned)}`} id={planned} /></>)} />
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
      <Create key={`${release}/${machine}/${first}`} release={release} machine={machine} first={first} />
    </>
  );
}
