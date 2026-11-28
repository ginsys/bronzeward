// The recovery page of one operation (journeys §5.9, ginsys/bronzeward#365), at /recovery/operations/<id>: take over, and
// resolve as cancelled. Each is a reason of 1 to REASON_MAX bytes and one press; neither asks for a typed identifier, because
// the reason is the record. The controls are on this page only: the operation's page links here for an unresolved operation.

import { type FormEvent, type ReactNode, useCallback, useEffect, useRef, useState } from "react";
import { ATTEMPT_WHAT, AccountingForm, accountedAttempt } from "./accounting.tsx";
import type { components } from "./api/v1";
import { Result, startsNew, useSend, useWrites } from "./forms.tsx";
import { Detail, Id, Ref, short, useItem, useList, usePending, viewOf } from "./list.tsx";
import { recoveryOperationPath } from "./operation-words.ts";
import { firstSentence, Note, PageHead, Panel } from "./layout.tsx";
import { REASON_MAX, reasonProblem } from "./plan.tsx";
import { Who } from "./principals.tsx";
import { safeWord } from "./safe.ts";
import { Action, useFocusOn, useRoles } from "./shell.tsx";
import { instant } from "./start-mark.ts";
import { Status } from "./states.tsx";
import { MachineById, OperationHeading } from "./titles.tsx";
import { ACCOUNT_TEXT, RECOVERY_OPERATION_NOTE, RECOVERY_OPERATION_ROLE_NOTE, RESOLVE_NO_OUTCOME, RESOLVE_TEXT, TAKE_OVER_TEXT } from "./words.ts";

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

// The generation a takeover's answer names, or 0 where it names none.
export function answeredGeneration(answer: unknown): number {
  const g = typeof answer === "object" && answer !== null ? (answer as { generation?: unknown }).generation : undefined;
  return typeof g === "number" && Number.isInteger(g) && g > 0 ? g : 0;
}

// Said where the events could not be read: the generation is then read from the attempts alone.
const EVENTS_UNREAD = "The operation's events were not read, so the owner generation is read from its attempts alone.";
// Said while they are still being read, as the reason Take over is off.
export const EVENTS_LOADING = "The operation's events are still being read: the owner generation comes from them.";
// The note on the page about the events read: only once the read has failed, never while it is loading or after it answered.
export const eventsNote = (kind: string): string | undefined => (kind === "ok" || kind === "loading" ? undefined : EVENTS_UNREAD);
// The states a takeover is accepted in, and the one kind it acts on (internal/api/outcome.go takeOverTx, operationMachine).
const TAKEN_OVER_IN = ["committed", "sending", "verifying", "unresolved"];

// Why a control is off for this operation, or undefined where it is on. The server stays authoritative: a read that is stale is
// answered by its refusal, shown in place.
// The server's words in these sentences (a kind, a state, a role) are shown only as safe text: any other reads "(not shown)".
// Take over sends the owner generation read from the events, so it waits while they are still being read; a read that failed
// does not hold it (the generation then comes from the attempts alone, which the note says).
export function takeOverWhy(o: Operation, eventsLoading = false): string | undefined {
  if (o.kind !== "apply-config") return `The operation is a ${safeWord(o.kind)}, and only an apply-config operation is taken over.`;
  if (!TAKEN_OVER_IN.includes(o.state)) return `The operation is ${safeWord(o.state)}: only one that is committed, sending, verifying or unresolved is taken over.`;
  return eventsLoading ? EVENTS_LOADING : undefined;
}
// Resolve reads only the operation's own state and attempts, which are read before the page shows, so it never waits for the events.
export function resolveWhy(o: Operation): string | undefined {
  if (o.state !== "unresolved") return `The operation is ${safeWord(o.state)}: only an unresolved operation is resolved.`;
  const n = (o.attempts ?? []).length;
  return n === 0 ? undefined : `${n === 1 ? "An attempt is" : `${n} attempts are`} recorded, and the request may have landed: an attempt is accounted for, not cancelled.`;
}

export const stateLine = (state: string) => `State: ${safeWord(state)}.`;
export const takenOver = (r: Takeover) => (
  <>Taken over: generation {r.generation}, state {safeWord(r.state)}, recorded by <Who id={r.requestedBy} /> ({safeWord(r.role)}), act <Id id={r.act} />. Nothing was sent to the node.</>
);
export const resolved = (r: Resolution) => (
  <>Resolved: the operation is {safeWord(r.state)}, recorded by <Who id={r.resolvedBy} /> ({safeWord(r.role)}), act <Id id={r.act} />. {RESOLVE_NO_OUTCOME}</>
);

// One act with a reason: the form, the reason the control is off, and the answer, kept in one place so the operation's reread
// does not unmount it. It sends once per press; a lost answer is repeated under its key by the write layer.
function Act({ label, route, op, off, finished, body, recorded }: {
  label: string; route: "takeovers" | "resolutions"; op: string; off: string | undefined; finished: (answer: unknown) => void;
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
  const answered = w?.outcome?.kind === "done" ? w.outcome.data : undefined;
  useEffect(() => {
    if (done) finished(answered);
  }, [done, finished, answered]);
  return (
    <>
      <form onSubmit={submit} aria-label={label}>
        <p><label>Reason (at most {REASON_MAX} bytes) <input name="reason" autoComplete="off" disabled={busy} /></label></p>
        <p><Action label={label} needs={["recovery-admin"]} submit busy={busy} variant="secondary" /></p>
        {off !== undefined && <p data-testid={`${route}-off`}>{off}</p>}
        {note !== undefined && <p role="alert" data-testid={`${route}-input`}>{note}</p>}
      </form>
      <div ref={answer} tabIndex={-1} data-testid={`${route}-answer`}><Result w={w} done={recorded} /></div>
    </>
  );
}

// The accounting of each attempt that has none: the form of a lost response, one per attempt.
// An attempt whose form was pressed stays here, off, so the answer of its own accounting is not dropped by the operation's reread;
// one accounted elsewhere, which this page never pressed, leaves as soon as a read shows it.
function Accounts({ op, machine, attempts }: { op: string; machine: string; attempts: readonly { id: string; number: number; transportDeadline: string; accounting?: unknown }[] }) {
  const [started, setStarted] = useState<readonly string[]>([]);
  const due = (a: { id: string; accounting?: unknown }) => a.accounting == null;
  const shown = attempts.filter((a) => due(a) || started.includes(a.id));
  if (shown.length === 0) return null;
  return (
    <Panel title="Account for an attempt">
      <p>{ACCOUNT_TEXT}</p>
      {shown.map((a) => (
        <div key={a.id}>
          <h4>Attempt {a.number}</h4>
          <AccountingForm machine={machine} path={`/operations/${enc(op)}/attempts/${enc(a.id)}/accountings`} resource={`/operations/${enc(op)}`}
            what={ATTEMPT_WHAT} done={accountedAttempt} due={due(a)} deadline={{ at: instant(a.transportDeadline) }} onStart={() => setStarted((s) => (s.includes(a.id) ? s : [...s, a.id]))} />
        </div>
      ))}
    </Panel>
  );
}

export function RecoveryOperation({ id }: { id: string }) {
  const path = `/operations/${enc(id)}`;
  const [n, setN] = useState(0);
  const reread = useCallback(() => setN((x) => x + 1), []);
  // The generation a takeover that finished on this page answered: the events are not read again, so it is the newest the page knows.
  const [taken, setTaken] = useState(0);
  const tookOver = useCallback((answer: unknown) => {
    setTaken((g) => Math.max(g, answeredGeneration(answer)));
    reread();
  }, [reread]);
  const o = useItem<Operation>(path, undefined, n);
  const [events, , rereading] = useList<EventBody>(`${path}/events`, "", true);
  const roles = useRoles();
  const po = usePending(o, "the operation");
  if (o.kind !== "ok") return <><h2>Recovery of the operation {short(id)}</h2>{po}</>;
  const op = o.data;
  const generation = Math.max(ownerGeneration(events.kind === "ok" ? events.data.items : [], op.attempts ?? []), taken);
  const machine = op.subject?.machine;
  const section = (title: string, text: string, body: ReactNode) => (
    <Panel title={title}>
      <p>{text}</p>
      {body}
    </Panel>
  );
  return (
    <>
      <PageHead heading={<>Recovery of the operation: <OperationHeading id={id} o={op} /></>}>
        <p className="state" data-testid="recovery-operation-state">
          {stateLine(op.state)}{" "}
          {machine !== undefined && <>Machine <MachineById id={machine} />. </>}
          {(op.attempts ?? []).length === 0 ? "No attempt is recorded." : `${(op.attempts ?? []).length} attempt${(op.attempts ?? []).length === 1 ? "" : "s"} recorded.`}
          {" "}Owner generation read: {generation}.
        </p>
        <Detail id={id} secondary />
      </PageHead>
      <Note line={firstSentence(RECOVERY_OPERATION_NOTE)} parts={[RECOVERY_OPERATION_NOTE, !roles.includes("recovery-admin") && RECOVERY_OPERATION_ROLE_NOTE]}>
        {" "}<Ref to={path} id={id}>Back to the operation</Ref>.
      </Note>
      {eventsNote(events.kind) !== undefined && <Status testid="recovery-operation-events">{eventsNote(events.kind)}</Status>}
      {section("Take over", TAKE_OVER_TEXT, (
        <Act label="Take over" route="takeovers" op={id} off={takeOverWhy(op, events.kind === "loading" || rereading)} finished={tookOver} body={(reason) => ({ generation, reason })}
          recorded={(d) => takenOver(d as Takeover)} />
      ))}
      {section("Resolve as cancelled", RESOLVE_TEXT, (
        <Act label="Resolve as cancelled" route="resolutions" op={id} off={resolveWhy(op)} finished={reread} body={(reason) => ({ to: "cancelled", reason })}
          recorded={(d) => resolved(d as Resolution)} />
      ))}
      {machine !== undefined && op.state === "unresolved" && (
        <Accounts op={id} machine={machine} attempts={op.attempts ?? []} />
      )}
    </>
  );
}
