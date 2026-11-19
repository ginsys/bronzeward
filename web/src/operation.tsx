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
import { eventLine, watch } from "./progress.ts";
import { Status } from "./states.tsx";

type Operation = components["schemas"]["OperationBody"];
type EventBody = components["schemas"]["EventBody"];
type Reader = ReturnType<typeof settledReads>;

export const CLOSING = "Closing this page does not stop the operation. Open it again and the server's state is read.";
export const LIVE = "Live: following this operation. It reconnects after a drop.";
export const ENDED = "Live: ended.";
export const SERVER_TIMES_UNREAD = "The server's event times were not read, so the times shown are those of this browser when each event arrived.";

// One timeline row: its time is the server's once the watch has ended, the browser's at arrival before.
export interface Row { id: string; line: string; at: string; server: boolean }
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

const clock = (at: string) => new Date(at).toLocaleTimeString("en-GB");

// The outcome in words, from the operation as last read. Only a completed operation whose result names
// the applied release is shown as applied; an unresolved one is not terminal and stays followed.
export function Outcome({ o }: { o: Operation }) {
  const machine = o.subject?.machine;
  const attempts = o.attempts ?? [];
  // An apply-config error record names its cause and attempts; a problem document names neither.
  const err: Record<string, unknown> = o.error ?? {};
  const cause = typeof err["cause"] === "string" ? err["cause"] : undefined;
  const n = typeof err["attempts"] === "number" ? err["attempts"] : attempts.length;
  switch (o.state) {
    case "completed": {
      const applied = o.result !== null && "appliedRelease" in o.result ? o.result : undefined;
      return applied === undefined
        ? <Status testid="outcome">Completed. No applied release is recorded, so nothing is shown as applied.</Status>
        : (
          <Status testid="outcome">
            Applied: release <Id id={applied.appliedRelease} /> is the machine's Applied release, confirmed by observation <Id id={applied.observation} />.
            {machine !== undefined && <> <Ref to={`/machines/${encodeURIComponent(machine)}`} id={machine}>Desired, Applied and Observed of the machine</Ref></>}
          </Status>
        );
    }
    case "failed": return <Status testid="outcome">Failed{cause === undefined ? "" : `: ${cause}`} ({n} {n === 1 ? "attempt" : "attempts"}).</Status>;
    case "rejected": return <Status testid="outcome">Rejected: the node refused every attempt as invalid{cause === undefined ? "" : ` (${cause})`}, {n} {n === 1 ? "attempt" : "attempts"}.</Status>;
    case "cancelled": return <Status testid="outcome">Cancelled.</Status>;
    case "succeeded": return <Status testid="outcome">Succeeded.</Status>;
    case "unresolved":
      return (
        <Status testid="outcome">
          Outcome unknown. A recovery administrator must account for attempt {attempts.length === 0 ? "" : attempts[attempts.length - 1]!.number}. No conflicting work on this machine proceeds until then.
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
  received?: () => string;
}

// The events of operation `id` followed to the end (§8.6): each as a received row, then, once the
// watch ended, every row again with the server's time from the JSON pages.
export async function follow(reading: Reader, id: string, f: Follow): Promise<void> {
  const received = f.received ?? (() => new Date().toISOString());
  f.dispatch({ type: "start" });
  const r = await watch(reading, id, {
    show: (e) => {
      f.dispatch({ type: "show", row: { id: e.id, line: eventLine(e), at: received(), server: false } });
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
  f.dispatch(read.kind === "ok"
    ? { type: "server", rows: read.data.items.map((e) => ({ id: e.id, line: eventLine(e), at: e.at, server: true })) }
    : { type: "end", text: SERVER_TIMES_UNREAD });
}

export function TimelineRows({ rows }: { rows: readonly Row[] }) {
  return (
    <ol aria-live="polite" data-testid="events">
      {rows.map((r) => <li key={r.id}>{r.server ? null : "received "}<time dateTime={r.at}>{clock(r.at)}</time> {r.line}</li>)}
    </ol>
  );
}

// The page's timeline, with `reread` called soon after events arrive so its own read of the operation
// follows the state.
export function OperationTimeline({ id, reread }: { id: string; reread: () => void }) {
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
    void follow(reading, id, { dispatch, soon, again: () => again.current(), failure: (res) => now.current.failure(res), signal: abort.signal });
    return () => {
      clearTimeout(timer);
      abort.abort();
    };
  }, [client, id]);
  return (
    <section aria-label="Timeline">
      <h3>Timeline</h3>
      {cleared !== undefined && <Status testid="cleared">{cleared}</Status>}
      <TimelineRows rows={rows} />
      {end !== undefined && <Status testid="ended">{end}</Status>}
      <p>{done ? ENDED : LIVE}</p>
      <p>{CLOSING}</p>
    </section>
  );
}
