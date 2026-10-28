// The machine detail (wireframe W3): the status sentences of journeys §2.4, derived only from the
// machine read and its observations, then the observations and the timeline. The timeline has no
// stream; it is polled with its cursor no more often than every 5 s while the page is visible
// (client contracts §8.6 item 6).

import { useCallback, useEffect, useRef, useState } from "react";
import type { components } from "./api/v1";
import { type Dropped, type Load, type Page, Ref, type Reads, settling, short, useHeld, useItem, useList, usePending, useReads } from "./list.tsx";
import { Head, type Machine, node, release } from "./machines.tsx";
import type { ReadResult } from "./request.ts";
import { clock } from "./states.tsx";

export type Observation = components["schemas"]["ObservationBody"];
export type Entry = components["schemas"]["TimelineEntry"];

export const ADOPTED =
  "Adopted from the node's own bytes; the release's compiled configuration may differ. The adopt plan's pendingConvergence evidence shows whether it does; a later apply-config plan withholds its diff when it does.";
export const EITHER = "The read may match the adopted bytes or this release's compiled configuration; the API does not say which.";
export const FROZEN =
  "Frozen: no new commitment or attempt until an approver unfreezes; an attempt already recorded is not stopped, and an adoption can still be recorded.";
export const HEALTH = "Health: not checked.";

// A read's date and time: this view shows the machine's whole history, so the time of day alone
// would make an old read look like today's. Built by hand, so every browser shows it alike.
const two = (n: number) => String(n).padStart(2, "0");
export function at(s: string): string {
  const d = new Date(s);
  return `${d.getFullYear()}-${two(d.getMonth() + 1)}-${two(d.getDate())} ${clock(d)}`;
}
// What a read could not read, field by field: the API gives several fields the same cause.
const causes = (o: Observation) => Object.entries(o.unread).map(([field, u]) => `${field} (${u.cause})`);

// The newest read, when it has a higher basis than the one observed names and could not read
// something. The machine read selects observed by highest basis among reads of the digest. The
// machine and its observations are read apart, so when observed names a read the observations
// do not hold yet, nothing is said to be newer than it.
export function latestUnread(m: Machine, obs: Observation[]): Observation | undefined {
  const latest = obs.reduce<Observation | undefined>((a, o) => (a === undefined || o.basis > a.basis ? o : a), undefined);
  const basis = m.observed === null ? -1 : obs.find((o) => o.id === m.observed?.observation)?.basis;
  return latest !== undefined && basis !== undefined && latest.basis > basis && causes(latest).length > 0 ? latest : undefined;
}

// Journeys §2.4's sentences. openDrift alone says "drifted"; a matching release never says healthy.
export function sentences(m: Machine, obs: Observation[]): string[] {
  const { desired: d, applied: a, observed: o, openDrift: drift } = m;
  const out: string[] = [];
  if (drift !== null) {
    const opened = obs.find((x) => x.id === drift.observation);
    out.push(`Drifted since ${opened === undefined ? `the read ${short(drift.observation)}` : at(opened.at)}: a read differed from what Bronzeward last verified (${a === null ? "none" : short(a.release)}).`);
    if (o?.release != null && o.release === d) out.push("It equals the selected release; that does not close the drift.");
  } else if (o === null) {
    out.push("No configuration read recorded.");
  } else if (a !== null && o.release === a.release) {
    out.push(`The configuration read at ${at(o.at)} is attributed to ${short(o.release)}, which is the Applied release (by ${a.source}).`);
    if (a.source === "adoption") out.push(ADOPTED);
    if (a.source === "adoption" && d === a.release) out.push(EITHER);
  } else if (o.release === null) {
    out.push(`The configuration read at ${at(o.at)} matched neither Applied nor Desired, and no drift is open; an operation may hold the machine. See the timeline.`);
  } else {
    // The read is the selected release's with no drift open: an operation holds the machine.
    out.push(`The configuration read at ${at(o.at)} is attributed to the selected release ${short(o.release)}.`);
  }
  const late = latestUnread(m, obs);
  if (late !== undefined) out.push(`The latest read at ${at(late.at)} could not read: ${causes(late).join("; ")}.`);
  if (a === null) out.push("Not applied or adopted by Bronzeward.");
  if (d === null) out.push("No release selected.");
  else if (m.pendingConvergence && a === null) out.push(`Pending convergence: the selected release ${short(d)} has not been applied to this machine.`);
  else if (m.pendingConvergence && drift === null) out.push(`Pending convergence: the configuration of the selected release ${short(d)} for this machine differs from what Bronzeward applied.`);
  else if (!m.pendingConvergence && drift === null && a !== null && d !== a.release) out.push(`The selected release ${short(d)} carries this machine's applied configuration unchanged.`);
  if (m.frozen) out.push(FROZEN);
  out.push(HEALTH);
  return out;
}

// The timeline as shown, newest first: `older` reads the page before the oldest shown (order=newest),
// `tail` is where the poll reads on from (oldest order), and `read` is false until the newest page
// answered.
export interface Timeline<T> { items: T[]; older: string | undefined; tail: string | undefined; read: boolean }
type TimelinePage<T> = Page<T> & { tail?: string };

export const UNREAD: Timeline<never> = { items: [], older: undefined, tail: undefined, read: false };

// The newest page, which the first read and the read after a refused cursor take.
async function readNewest<T>(client: Reads["client"], path: string): Promise<ReadResult<Timeline<T>>> {
  const res = await client.read<TimelinePage<T>>(`${path}?order=newest`);
  if (res.kind === "restart") return { kind: "refused", status: 400, problem: res.problem };
  return res.kind === "ok" ? { kind: "ok", data: { items: res.data.items, older: res.data.next, tail: res.data.tail, read: true } } : res;
}

// A refused cursor (another epoch) reads the newest page again, once; a failure after that is
// dropped, with what was shown before it.
async function restart<T>(client: Reads["client"], path: string): Promise<ReadResult<Timeline<T>> | Dropped> {
  const res = await readNewest<T>(client, path);
  return res.kind === "ok" || res.kind === "discarded" ? res : { kind: "dropped", failed: res };
}

// The poll: the newest page first, then on from `tail` in oldest order, following next while
// `going` holds, as readPages does, and keeping the tail of the last page. Later entries go first.
export async function readLater<T>(client: Reads["client"], path: string, from: Timeline<T>, going: () => boolean = () => true): Promise<ReadResult<Timeline<T>> | Dropped> {
  if (!from.read) return readNewest(client, path);
  let cursor = from.tail;
  let later: T[] = [];
  for (;;) {
    const res = await client.read<TimelinePage<T>>(cursor === undefined ? path : `${path}?${new URLSearchParams({ cursor })}`);
    if (res.kind === "restart" && cursor !== undefined) return restart(client, path);
    if (res.kind === "restart") return { kind: "refused", status: 400, problem: res.problem };
    if (res.kind !== "ok") return res;
    later = [...later, ...res.data.items];
    if (res.data.next === undefined) return { kind: "ok", data: { ...from, items: [...later.reverse(), ...from.items], tail: res.data.tail ?? from.tail } };
    cursor = res.data.next;
    if (!going()) return { kind: "discarded" };
  }
}

// "Load older": the page before the oldest shown, appended; none left once its answer has no next.
export async function readOlder<T>(client: Reads["client"], path: string, from: Timeline<T>): Promise<ReadResult<Timeline<T>> | Dropped> {
  if (from.older === undefined) return { kind: "ok", data: from };
  const res = await client.read<TimelinePage<T>>(`${path}?${new URLSearchParams({ order: "newest", cursor: from.older })}`);
  if (res.kind === "restart") return restart(client, path);
  return res.kind === "ok" ? { kind: "ok", data: { ...from, items: [...from.items, ...res.data.items], older: res.data.next } } : res;
}

// What the next read starts from: the answer's timeline, the newest page again after a drop, else
// the one it had.
export const nextTimeline = <T,>(tl: Timeline<T>, res: ReadResult<Timeline<T>> | Dropped): Timeline<T> => (res.kind === "ok" ? res.data : res.kind === "dropped" ? UNREAD : tl);

export const POLL_MS = 5000;

// Reads now, then POLL_MS after each answer for which read returns true. While the page is
// hidden a due read, the first one too, waits for the next check, POLL_MS later. Returns the stop.
export function polled(read: () => Promise<boolean>, page: Pick<Document, "visibilityState"> = document): () => void {
  let timer: ReturnType<typeof setTimeout> | undefined;
  let live = true;
  const run = () => void read().then((more) => {
    if (live && more) timer = setTimeout(due, POLL_MS);
  });
  const due = () => (page.visibilityState === "visible" ? run() : (timer = setTimeout(due, POLL_MS)));
  due();
  return () => {
    live = false;
    clearTimeout(timer);
  };
}

// The timeline, its newest page first and then polled while the page is visible, showing loading
// when the path changes and keeping what it shows when the page reads again: a new view reads the
// newest page again, as a list reads its first. A transport failure keeps polling, so the shell's
// offline banner clears once a poll answers again. A failure after a refused cursor drops the
// timeline and that cursor. A superseded poll reads no further page and settles as not live, so the
// shell still takes its refusal of the credentials. The poll and "Load older" read one at a time,
// each on from what the one before left; `older` is undefined at the timeline's start.
function useTimeline(path: string): [Load<Timeline<Entry>> | Dropped, (() => void) | undefined, boolean] {
  const { client, settle, undropped, again, view } = useReads();
  const [tl, put, held] = useHeld<Timeline<Entry>, Dropped>();
  const key = useRef<string | undefined>(undefined);
  const older = useRef<(() => void) | undefined>(undefined);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    let shown: Timeline<Entry> = UNREAD;
    let live = true;
    let queue = Promise.resolve(true);
    const ok = undropped();
    if (key.current !== path) {
      key.current = path;
      put({ kind: "loading" });
    }
    const step = (read: (from: Timeline<Entry>) => Promise<ReadResult<Timeline<Entry>> | Dropped>) =>
      (queue = queue.then(async () => {
        if (!live) return false;
        const res = await read(shown);
        const [r, set, shows] = settling(res, put, held);
        settle(r, set, () => live && ok(), shows);
        if (!live || !ok()) return false;
        shown = nextTimeline(shown, res);
        return r.kind === "ok" || r.kind === "transport";
      }));
    older.current = () => {
      setBusy(true);
      void step((from) => readOlder<Entry>(client, path, from)).then(() => live && setBusy(false));
    };
    const stop = polled(() => step((from) => readLater<Entry>(client, path, from, () => live && ok())));
    return () => {
      live = false;
      older.current = undefined;
      setBusy(false);
      stop();
    };
  }, [client, settle, undropped, put, held, view, path, again]);
  const more = useCallback(() => older.current?.(), []);
  return [tl, tl.kind === "ok" && tl.data.older !== undefined ? more : undefined, busy];
}

// The observed read's attribution, shown apart from the sentences: under an open drift they speak of
// the drift, and desired, applied and observed stay separate states.
export const attribution = (r: string | null) => (r === null ? "matches neither Applied nor Desired" : <>attributed to {release(r)}</>);

// A timeline entry's members that name what it is about; the rest stays in the API.
const SHOWN = ["change", "resolution", "from", "to", "principal", "role", "reason", "cause", "observation", "operation", "plan"];
export function details(e: Entry): string {
  const r = (e.entry ?? {}) as Record<string, unknown>;
  return SHOWN.filter((k) => typeof r[k] === "string" && r[k] !== "")
    .map((k) => (k === "reason" ? `"${r[k] as string}"` : /^[a-z]+_[a-z2-7]{26}$/.test(r[k] as string) ? short(r[k] as string) : (r[k] as string)))
    .join(" ");
}

export function MachineItem({ id }: { id: string }) {
  const path = `/machines/${encodeURIComponent(id)}`;
  const m = useItem<Machine>(path);
  const [obs] = useList<Observation>(`${path}/observations`, "", true);
  const [tl, older, busy] = useTimeline(`${path}/timeline`);
  const [pm, po, timeline] = [usePending(m, "the machine"), usePending(obs, "the observations"), usePending(tl, "the timeline")];
  if (m.kind !== "ok") return <><h2>Machine {short(id)}</h2>{pm}</>;
  if (obs.kind !== "ok") return <><h2>Machine {short(id)}</h2>{po}</>;
  const d = m.data;
  const list = [...obs.data.items].sort((x, y) => y.basis - x.basis);
  return (
    <>
      <h2>Machine {d.id}</h2>
      <p>Cluster <Ref to={`/clusters/${d.cluster}`} id={d.cluster} />, {d.talosEndpoint}, {node(d)}, {d.platform}.</p>
      <ul aria-label="Status" data-testid="sentences">{sentences(d, list).map((s) => <li key={s}>{s}</li>)}</ul>
      <dl>
        <dt>Desired</dt><dd>{release(d.desired)}</dd>
        <dt>Applied</dt><dd>{d.applied === null ? "none" : <>{release(d.applied.release)} by {d.applied.source}</>}</dd>
        <dt>Observed</dt><dd data-testid="observed">{d.observed === null ? "none" : <>read at {at(d.observed.at)} (<span title={d.observed.observation}>{short(d.observed.observation)}</span>): {attribution(d.observed.release)}</>}</dd>
        <dt>Scope</dt><dd>{d.scopeState}</dd>
      </dl>
      <h3>Observations</h3>
      {list.length === 0 ? <p>No observation recorded.</p> : (
        <table data-testid="observations">
          <Head cols={["Observation", "Basis", "Read at", "Purpose", "Not read"]} />
          <tbody>
            {list.map((o) => (
              <tr key={o.id}>
                <th scope="row"><span title={o.id}>{short(o.id)}</span></th>
                <td>{o.basis}</td>
                <td>{at(o.at)}</td>
                <td>{o.purpose}</td>
                <td>{causes(o).length === 0 ? "nothing" : causes(o).join("; ")}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <h3>Timeline (newest first)</h3>
      {tl.kind !== "ok" ? timeline : tl.data.items.length === 0 ? <p>No timeline entry recorded.</p> : (
        <>
          <table data-testid="timeline">
            <Head cols={["At", "Kind", "Details"]} />
            <tbody>
              {tl.data.items.map((e) => (
                <tr key={`${e.epoch}:${e.revision}`}>
                  <td>{at(e.at)}</td>
                  <td>{e.kind}</td>
                  <td>{details(e)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {older !== undefined && <p><button type="button" onClick={older} disabled={busy}>Load older</button></p>}
        </>
      )}
    </>
  );
}
