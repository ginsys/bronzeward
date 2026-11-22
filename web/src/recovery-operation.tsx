// The recovery page of one operation (journeys §5.9, ginsys/bronzeward#365), at /recovery/operations/<id>: take over, and
// resolve as cancelled. Each is a reason of 1 to REASON_MAX bytes and one press; neither asks for a typed identifier, because
// the reason is the record. The controls are on this page only: the operation's page links here for an unresolved operation.

import { type FormEvent, type ReactNode, useCallback, useEffect, useRef, useState } from "react";
import type { components } from "./api/v1";
import { Result, startsNew, useSend, useWrites } from "./forms.tsx";
import { Detail, Id, Ref, short, useItem, useList, usePending, viewOf } from "./list.tsx";
import { recoveryOperationPath } from "./operation-words.ts";
import { PageNote } from "./page-note.tsx";
import { REASON_MAX, reasonProblem } from "./plan.tsx";
import { Who } from "./principals.tsx";
import { Action, useFocusOn, useRoles } from "./shell.tsx";
import { Status } from "./states.tsx";
import { MachineById, OperationHeading } from "./titles.tsx";
import { RECOVERY_OPERATION_NOTE, RECOVERY_OPERATION_ROLE_NOTE, RESOLVE_NO_OUTCOME, RESOLVE_TEXT, TAKE_OVER_TEXT } from "./words.ts";

const enc = encodeURIComponent;
type Operation = components["schemas"]["OperationBody"];
type EventBody = components["schemas"]["EventBody"];
type Takeover = components["schemas"]["TakeoverBody"];
type Resolution = components["schemas"]["ResolutionBody"];

// The operation a recovery path names, decoded; undefined when the path is not `/recovery/operations/<id>`, a segment is not
// valid encoding or the identifier is not an operation's.
export function recoveryOperationId(path: string): string | undefined {
  const m = /^\/recovery\/operations\/([^/]+)$/.exec(path);
  if (m === null) return undefined;
  try {
    const id = decodeURIComponent(m[1]!);
    return viewOf(id) === "operations" && recoveryOperationPath(id) === path ? id : undefined;
  } catch {
    return undefined;
  }
}

// The owner generation a takeover sends. The operation body carries none, so it is the highest `generation` among the events
// and the attempts, and 1 where they show none (journeys §5.9, as the command reads it). The server refuses a generation that
// is not current, so a wrong read sends nothing harmful.
export function ownerGeneration(events: readonly Pick<EventBody, "data">[], attempts: readonly { generation: number }[]): number {
  const seen = [
    ...attempts.map((a) => a.generation),
    ...events.map((e) => (typeof e.data === "object" && e.data !== null ? (e.data as { generation?: unknown }).generation : undefined)),
  ].filter((g): g is number => typeof g === "number" && Number.isInteger(g) && g > 0);
  return seen.length === 0 ? 1 : Math.max(...seen);
}

// Said where the events could not be read: the generation is then read from the attempts alone.
const EVENTS_UNREAD = "The operation's events were not read, so the owner generation is read from its attempts alone.";
// The states a takeover is accepted in, and the one kind it acts on (internal/api/outcome.go takeOverTx, operationMachine).
const TAKEN_OVER_IN = ["committed", "sending", "verifying", "unresolved"];

// Why a control is off for this operation, or undefined where it is on. The server stays authoritative: a read that is stale is
// answered by its refusal, shown in place.
export function takeOverWhy(o: Operation): string | undefined {
  if (o.kind !== "apply-config") return `The operation is a ${o.kind}, and only an apply-config operation is taken over.`;
  return TAKEN_OVER_IN.includes(o.state) ? undefined : `The operation is ${o.state}: only one that is committed, sending, verifying or unresolved is taken over.`;
}
export function resolveWhy(o: Operation): string | undefined {
  if (o.state !== "unresolved") return `The operation is ${o.state}: only an unresolved operation is resolved.`;
  const n = (o.attempts ?? []).length;
  return n === 0 ? undefined : `${n === 1 ? "An attempt is" : `${n} attempts are`} recorded, and the request may have landed: an attempt is accounted for, not cancelled.`;
}

// One act with a reason: the form, the reason the control is off, and the answer, kept in one place so the operation's reread
// does not unmount it. It sends once per press; a lost answer is repeated under its key by the write layer.
function Act({ label, route, op, off, finished, body, recorded }: {
  label: string; route: "takeovers" | "resolutions"; op: string; off: string | undefined; finished: () => void;
  body: (reason: string) => Record<string, unknown>; recorded: (data: unknown) => ReactNode;
}) {
  const writes = useWrites()!;
  const [note, setNote] = useState<string | undefined>();
  const [w, send] = useSend();
  const answer = useRef<HTMLDivElement>(null);
  useFocusOn(w?.outcome, answer);
  const done = w?.outcome?.kind === "done";
  const busy = (w !== undefined && !startsNew(writes, w)) || done || off !== undefined;
  const submit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const reason = String(new FormData(e.currentTarget).get("reason") ?? "");
    const bad = reasonProblem(reason);
    setNote(bad);
    if (bad === undefined && !busy) {
      send({ method: "POST", path: `/operations/${enc(op)}/${route}`, body: body(reason), resource: `/operations/${enc(op)}` });
    }
  };
  useEffect(() => {
    if (done) finished();
  }, [done, finished]);
  return (
    <>
      <form onSubmit={submit} aria-label={label}>
        <p><label>Reason (at most {REASON_MAX} bytes) <input name="reason" autoComplete="off" disabled={busy} /></label></p>
        <p><Action label={label} needs={["recovery-admin"]} submit busy={busy} /></p>
        {off !== undefined && <p data-testid={`${route}-off`}>{off}</p>}
        {note !== undefined && <p role="alert" data-testid={`${route}-input`}>{note}</p>}
      </form>
      <div ref={answer} tabIndex={-1} data-testid={`${route}-answer`}><Result w={w} done={recorded} /></div>
    </>
  );
}

export function RecoveryOperation({ id }: { id: string }) {
  const path = `/operations/${enc(id)}`;
  const [n, setN] = useState(0);
  const reread = useCallback(() => setN((x) => x + 1), []);
  const o = useItem<Operation>(path, undefined, n);
  const [events] = useList<EventBody>(`${path}/events`, "", true);
  const roles = useRoles();
  const po = usePending(o, "the operation");
  if (o.kind !== "ok") return <><h2>Recovery of the operation {short(id)}</h2>{po}</>;
  const op = o.data;
  const generation = ownerGeneration(events.kind === "ok" ? events.data.items : [], op.attempts ?? []);
  const machine = op.subject?.machine;
  const section = (title: string, text: string, body: ReactNode) => (
    <section aria-label={title}>
      <h3>{title}</h3>
      <p>{text}</p>
      {body}
    </section>
  );
  return (
    <>
      <h2>Recovery of the operation: <OperationHeading id={id} o={op} /></h2>
      <Detail id={id} />
      <PageNote parts={[RECOVERY_OPERATION_NOTE, !roles.includes("recovery-admin") && RECOVERY_OPERATION_ROLE_NOTE]}>
        {" "}<Ref to={path} id={id}>Back to the operation</Ref>.
      </PageNote>
      <p data-testid="recovery-operation-state">
        State: {op.state}.{" "}
        {machine !== undefined && <>Machine <MachineById id={machine} />. </>}
        {(op.attempts ?? []).length === 0 ? "No attempt is recorded." : `${(op.attempts ?? []).length} attempt${(op.attempts ?? []).length === 1 ? "" : "s"} recorded.`}
        {" "}Owner generation read: {generation}.
      </p>
      {events.kind !== "ok" && <Status testid="recovery-operation-events">{EVENTS_UNREAD}</Status>}
      {section("Take over", TAKE_OVER_TEXT, (
        <Act label="Take over" route="takeovers" op={id} off={takeOverWhy(op)} finished={reread} body={(reason) => ({ generation, reason })}
          recorded={(d) => {
            const r = d as Takeover;
            return <>Taken over: generation {r.generation}, state {r.state}, recorded by <Who id={r.requestedBy} /> ({r.role}), act <Id id={r.act} />. Nothing was sent to the node.</>;
          }} />
      ))}
      {section("Resolve as cancelled", RESOLVE_TEXT, (
        <Act label="Resolve as cancelled" route="resolutions" op={id} off={resolveWhy(op)} finished={reread} body={(reason) => ({ to: "cancelled", reason })}
          recorded={(d) => {
            const r = d as Resolution;
            return <>Resolved: the operation is {r.state}, recorded by <Who id={r.resolvedBy} /> ({r.role}), act <Id id={r.act} />. {RESOLVE_NO_OUTCOME}</>;
          }} />
      ))}
    </>
  );
}
