// The operation page's progress (ginsys/bronzeward#359; wireframe W8; client contracts §8.6): the
// operation's events as the shared reader (progress.ts) delivers them, and the outcome in words.
// The stream carries no event time, so each row shows the browser's clock at arrival, marked as
// received, until the watch ends; then the server's event times, read from the JSON pages, replace
// them. A page that cannot read them keeps the received times and says so.

import { type ReactNode, useEffect, useReducer, useRef } from "react";
import type { components } from "./api/v1";
import { useWrites } from "./forms.tsx";
import { settledReads } from "./imports.tsx";
import { type Failed, Id, readPages, Ref, useReads } from "./list.tsx";
import { operationNext, operationNow } from "./operation-words.ts";
import { Named } from "./titles.tsx";
import { eventLine, RESTORED, watch } from "./progress.ts";
import { Status } from "./states.tsx";

type Operation = components["schemas"]["OperationBody"];
type EventBody = components["schemas"]["EventBody"];
type Reader = ReturnType<typeof settledReads>;

export const CLOSING = "Closing this page does not stop the operation. Open it again and the server's state is read.";
export const LIVE = "Live: connected (resumes after a drop)";
export const ENDED = "Live: ended.";
export const SERVER_TIMES_UNREAD = "The server's event times were not read, so the times shown are those of this browser when each event arrived.";

// One timeline row: its time is the server's once the watch has ended, the browser's at arrival before.
// A state change row also keeps the state it reached and its cause, which the outcome line names.
export interface Row { id: string; line: string; at: string; server: boolean; to?: string; cause?: string }

// A short server token (a state, a problem code): anything else is not shown.
const TOKEN = /^[A-Za-z0-9_-]{1,64}$/;
const token = (v: unknown): string | undefined => (typeof v === "string" && TOKEN.test(v) ? v : undefined);
// A cause is a token or a sentence the server wrote (`internal/api/settle.go`: "a completion observation
// contradicts the postconditions"). It is shown as text, so it needs no escaping; one with a control or
// format character (a line feed, a bidirectional control) or over 300 characters is not shown.
const CAUSE = /^[^\p{Cc}\p{Cf}]{1,300}$/u;
const causeText = (v: unknown): string | undefined => (typeof v === "string" && CAUSE.test(v.trim()) ? v.trim() : undefined);

// An event as a row: the shared reader's line, except that a state change names the state it reached
// and why (`internal/api/recovery_acts.go` and the other writers of `operation-state` set `from`, `to`
// and `cause`).
export function rowOf(e: { id: string; type: string; data: unknown }, at: string, server: boolean): Row {
  const d = (typeof e.data === "object" && e.data !== null ? e.data : {}) as Record<string, unknown>;
  const to = e.type === "operation-state" ? token(d["to"]) : undefined;
  if (to === undefined) return { id: e.id, line: eventLine(e), at, server };
  const [from, cause] = [token(d["from"]), causeText(d["cause"])];
  return { id: e.id, line: `operation-state: ${from === undefined ? "" : `${from} → `}${to}${cause === undefined ? "" : ` (${cause})`}`, at, server, to, ...(cause !== undefined && { cause }) };
}
export interface TimelineView { rows: Row[]; cleared?: string; end?: ReactNode; done: boolean }
export type TimelineAct =
  | { type: "start" }
  | { type: "show"; row: Row }
  | { type: "clear"; why: string }
  | { type: "server"; rows: Row[] }
  | { type: "end"; text: ReactNode };
export function timelineView(v: TimelineView, a: TimelineAct): TimelineView {
  switch (a.type) {
    case "start": return { rows: [], done: false };
    case "show": return { ...v, rows: [...v.rows, a.row] };
    case "clear": return { ...v, rows: [], cleared: a.why };
    case "server": return { ...v, rows: a.rows, done: true };
    case "end": return { ...v, end: a.text, done: true };
  }
}

// The cause the last change into `state` gave, when a row names one.
export const causeOf = (rows: readonly Row[], state: string) => [...rows].reverse().find((r) => r.to === state)?.cause;

const clock = (at: string) => new Date(at).toLocaleTimeString("en-GB");

// The outcome in words, from the operation as last read. Only a completed operation whose result names
// the applied release is shown as applied; an unresolved one is not terminal and stays followed.
// `why` is the cause the operation's last state change gave, when an event did.
export function Outcome({ o, cause: why }: { o: Operation; cause?: string | undefined }) {
  const machine = o.subject?.machine;
  const attempts = o.attempts ?? [];
  // An apply-config error record names its cause and attempts; a publish or ingest problem document
  // names its code (`type`) and no attempts.
  const err: Record<string, unknown> = o.error ?? {};
  const cause = causeText(err["cause"]) ?? token(typeof err["type"] === "string" ? err["type"].replace(/^urn:bronzeward:problem:/, "") : undefined);
  const n = typeof err["attempts"] === "number" ? err["attempts"] : 0;
  const count = `${n} ${n === 1 ? "attempt" : "attempts"}`;
  switch (o.state) {
    case "completed": {
      const applied = o.result !== null && "appliedRelease" in o.result ? o.result : undefined;
      // An adoption records the machine's configuration as Applied from the observation, with no release applied.
      const adopted = o.result !== null && "adoptionRecord" in o.result ? o.result : undefined;
      if (adopted !== undefined) {
        return (
          <Status testid="outcome">
            Adopted: the adoption record is <Id id={adopted.adoptionRecord} />.
            {machine !== undefined && <> <Ref to={`/machines/${encodeURIComponent(machine)}`} id={machine}>Desired, Applied and Observed of the machine</Ref></>}
          </Status>
        );
      }
      return applied === undefined
        ? <Status testid="outcome">Completed. No applied release is recorded, so nothing is shown as applied.</Status>
        : (
          <Status testid="outcome">
            Applied: this operation applied release <Named id={applied.appliedRelease} />, confirmed by observation <Id id={applied.observation} />.
            {machine !== undefined && <> <Ref to={`/machines/${encodeURIComponent(machine)}`} id={machine}>Desired, Applied and Observed of the machine</Ref></>}
          </Status>
        );
    }
    case "failed": return <Status testid="outcome">Failed{cause === undefined ? "" : `: ${cause}`}{n === 0 ? "" : ` (${count})`}.</Status>;
    case "rejected": return <Status testid="outcome">Rejected: the node refused every attempt as invalid{cause === undefined ? "" : ` (${cause})`}{n === 0 ? "" : `, ${count}`}.</Status>;
    case "cancelled": return <Status testid="outcome">Cancelled{why === undefined ? "" : `: ${why}`}.</Status>;
    case "succeeded": return <Status testid="outcome">Succeeded.</Status>;
    case "unresolved":
      return (
        <Status testid="outcome">
          Outcome unknown{why === undefined ? "" : ` (${why})`}. A recovery administrator must account for {attempts.length === 0 ? "this operation: no attempt is recorded" : `attempt ${attempts[attempts.length - 1]!.number}`}. No conflicting work on this machine proceeds until then.
          {" "}<Ref to="/recovery" id="recovery">Open in Recovery</Ref> (recovery-admin). This page keeps following the operation.
        </Status>
      );
    default: return <Status testid="outcome">In progress: {o.state}.</Status>;
  }
}

// What `follow` is given: where its rows go, the calls that make the page read the operation again
// (`soon` after an event, `again` when the watch ended), how a failed read is shown, and the signal
// that ends it when the page leaves. `received` is the browser's clock at an event's arrival.
export interface Follow {
  dispatch: (a: TimelineAct) => void;
  soon: () => void;
  again: () => void;
  failure: (res: Failed) => ReactNode;
  signal: AbortSignal;
  // The client's epoch now: a final read made when it differs from the epoch the watch ended on, whether
  // the client discarded the read or answered it, is a restore's (§8.5).
  epoch: () => string | undefined;
  // The epoch this operation's page was last mounted under: the shell mounts the page anew on an epoch
  // change (§8.5), so the notice of what the reader cleared is made at the mount (`seenUnder`).
  was?: string | undefined;
  received?: () => string;
}

// The final read may restart the watch this many times after a restore.
const ROUNDS = 3;

// The events of operation `id` followed to the end (§8.6): each as a received row, then, once the
// watch ended, every row again with the server's time from the JSON pages. That read is outside
// `watch`, so the epoch rule is applied here: the client discards a read answered under another epoch
// than the one it knew, and when the watch ended on another one, or the server restarted the list, the
// installation was restored or entered recovery mode, so the notice shows and the watch starts again,
// as the reader does for a stream that reaches a new epoch.
export async function follow(reading: Reader, id: string, f: Follow): Promise<void> {
  const received = f.received ?? (() => new Date().toISOString());
  for (let round = 0; ; round++) {
    f.dispatch(round === 0 ? { type: "start" } : { type: "clear", why: RESTORED });
    // A page mounted under another epoch than the one it was last mounted under shows the notice once.
    const at = f.epoch();
    if (round === 0 && f.was !== undefined && at !== undefined && f.was !== at) f.dispatch({ type: "clear", why: RESTORED });
    const r = await watch(reading, id, {
      show: (e) => {
        f.dispatch({ type: "show", row: rowOf(e, received(), false) });
        f.soon();
      },
      clear: (why) => f.dispatch({ type: "clear", why }),
      signal: f.signal,
    });
    if (r.kind === "stopped" || f.signal.aborted) return;
    if (r.kind === "failed") return f.dispatch({ type: "end", text: f.failure(r.res) });
    f.again();
    const read = await readPages<EventBody>(reading, `/operations/${encodeURIComponent(id)}/events`, "", { items: [], next: undefined, restarted: false }, true, () => !f.signal.aborted);
    if (f.signal.aborted) return;
    // A restore shows in the read's own answer too: a list the server restarted (a refused cursor), or a
    // client that took another epoch than the watch ended on while the read was answered.
    const now = f.epoch();
    const moved = r.epoch !== undefined && now !== undefined && now !== r.epoch;
    const restored = read.kind === "ok" ? read.data.restarted || moved : read.kind === "discarded" && moved;
    if (restored && round < ROUNDS - 1) continue;
    if (read.kind === "ok" && !restored) return f.dispatch({ type: "server", rows: read.data.items.map((e) => rowOf(e, e.at, true)) });
    return f.dispatch({ type: "end", text: SERVER_TIMES_UNREAD });
  }
}

export function TimelineRows({ rows }: { rows: readonly Row[] }) {
  return (
    <ol aria-live="polite" data-testid="events">
      {rows.map((r) => <li key={r.id} data-event-id={r.id}>{r.server ? null : "received "}<time dateTime={r.at}>{clock(r.at)}</time> {r.line}</li>)}
    </ol>
  );
}

// The next step an operator can take from the operation's state; nothing where the outcome line already says it.
export const Next = ({ o }: { o: Operation }) => {
  const next = operationNext(o.kind, o.state);
  return next === undefined ? null : <p data-testid="next">Next: {next}</p>;
};

// The epoch the operation page was last mounted under, with its operation, per client (which outlives the
// page and ends with the sign-in), recorded once at each mount and by nothing a row carries. One rule: a
// page that opens the operation it opened last, under another epoch than that, shows the restored notice
// once. Leaving and coming back after an epoch change reads it too; opening another operation in between
// forgets this one. A client that knows no epoch yet records nothing.
const mountedUnder = new WeakMap<object, { op: string; epoch: string }>();
export function seenUnder(client: object, op: string, now: string | undefined): string | undefined {
  const before = mountedUnder.get(client);
  if (now !== undefined) mountedUnder.set(client, { op, epoch: now });
  return before?.op === op ? before.epoch : undefined;
}

// The page's outcome and timeline: `o` is the operation as last read, and `reread` is called soon after
// events arrive so that read follows the state. The outcome names the cause of the state it shows when
// a state change row gave one.
export function OperationTimeline({ id, o, reread }: { id: string; o: Operation; reread: () => void }) {
  const client = useWrites()?.client;
  const reads = useReads();
  const now = useRef(reads);
  now.current = reads;
  const again = useRef(reread);
  again.current = reread;
  const [{ rows, cleared, end, done }, dispatch] = useReducer(timelineView, { rows: [], done: false });
  useEffect(() => {
    if (client === undefined) return;
    const abort = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const soon = () => {
      clearTimeout(timer);
      timer = setTimeout(() => again.current(), 300);
    };
    const reading = settledReads(client, { settle: now.current.settle, undropped: now.current.undropped }, () => !abort.signal.aborted);
    void follow(reading, id, { dispatch, soon, again: () => again.current(), failure: (res) => now.current.failure(res), signal: abort.signal, epoch: () => client.currentEpoch, was: seenUnder(client, id, client.currentEpoch) });
    return () => {
      clearTimeout(timer);
      abort.abort();
    };
  }, [client, id]);
  return (
    <section aria-label="Timeline">
      <p data-testid="now">{operationNow(o.kind, o.state)}</p>
      <Outcome o={o} cause={causeOf(rows, o.state)} />
      <Next o={o} />
      <div className="panel">
        <h3>Timeline</h3>
        {cleared !== undefined && <Status testid="cleared">{cleared}</Status>}
        <TimelineRows rows={rows} />
        {end !== undefined && <Status testid="ended">{end}</Status>}
        <p>{done ? ENDED : LIVE}</p>
        <p>{CLOSING}</p>
      </div>
    </section>
  );
}
