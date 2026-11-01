// The machine detail (wireframe W3): the status sentences of journeys §2.4, derived only from the
// machine read and its observations, then the observations and the timeline. The timeline has no
// stream; it is polled with its cursor no more often than every 5 s while the page is visible
// (client contracts §8.6 item 6).

import { useCallback, useEffect, useReducer, useRef, useState } from "react";
import type { components } from "./api/v1";
import { type Dropped, Head, type Listed, type Load, type Page, Ref, type Reads, ReadsProvider, RESTARTED, Scroll, settling, short, useHeld, useItem, useList, usePending, useReads } from "./list.tsx";
import { type Machine, node, release } from "./machines.tsx";
import type { ReadResult } from "./request.ts";
import { clock, Status } from "./states.tsx";

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
// `tail` is where the poll reads on from (oldest order), `read` is false until the newest page
// answered, and `restarted` says a refused cursor read the timeline again (PAGE-02), as a list's does.
export interface Timeline<T> { items: T[]; older: string | undefined; tail: string | undefined; read: boolean; restarted: boolean }
type TimelinePage<T> = Page<T> & { tail?: string };

export const UNREAD: Timeline<never> = { items: [], older: undefined, tail: undefined, read: false, restarted: false };

// The newest page, which the first read and the read after a refused cursor take.
async function readNewest<T>(client: Reads["client"], path: string, restarted = false): Promise<ReadResult<Timeline<T>>> {
  const res = await client.read<TimelinePage<T>>(`${path}?order=newest`);
  if (res.kind === "restart") return { kind: "refused", status: 400, problem: res.problem };
  return res.kind === "ok" ? { kind: "ok", data: { items: res.data.items, older: res.data.next, tail: res.data.tail, read: true, restarted } } : res;
}

// A refused cursor (another epoch) reads the newest page again, once, and the timeline then says it
// restarted; a failure after that is dropped, with what was shown before it.
async function restart<T>(client: Reads["client"], path: string): Promise<ReadResult<Timeline<T>> | Dropped> {
  const res = await readNewest<T>(client, path, true);
  return res.kind === "ok" || res.kind === "discarded" ? res : { kind: "dropped", failed: res };
}

// The poll: the newest page first, then on from `tail` in oldest order, following next while
// `going` holds, as readPages does, and keeping the tail of the last page. Later entries go first.
// A read superseded before it starts, or before a restart, sends nothing more.
export async function readLater<T>(client: Reads["client"], path: string, from: Timeline<T>, going: () => boolean = () => true): Promise<ReadResult<Timeline<T>> | Dropped> {
  if (!going()) return { kind: "discarded" };
  if (!from.read) return readNewest(client, path);
  let cursor = from.tail;
  let later: T[] = [];
  for (;;) {
    const res = await client.read<TimelinePage<T>>(cursor === undefined ? path : `${path}?${new URLSearchParams({ cursor })}`);
    if (res.kind === "restart" && cursor !== undefined) return going() ? restart(client, path) : { kind: "discarded" };
    if (res.kind === "restart") return { kind: "refused", status: 400, problem: res.problem };
    if (res.kind !== "ok") return res;
    later = [...later, ...res.data.items];
    if (res.data.next === undefined) return { kind: "ok", data: { ...from, items: [...later.reverse(), ...from.items], tail: res.data.tail ?? from.tail } };
    cursor = res.data.next;
    if (!going()) return { kind: "discarded" };
  }
}

// "Load older": the page before the oldest shown, appended; none left once its answer has no next.
// Superseded as the poll is; `going` has no default, so no caller can leave it out. Before the newest
// page answered (a new view whose first read failed) there is nothing to read on from, and what is
// still shown stays.
export async function readOlder<T>(client: Reads["client"], path: string, from: Timeline<T>, going: () => boolean): Promise<ReadResult<Timeline<T>> | Dropped> {
  if (!going() || !from.read) return { kind: "discarded" };
  if (from.older === undefined) return { kind: "ok", data: from };
  const res = await client.read<TimelinePage<T>>(`${path}?${new URLSearchParams({ order: "newest", cursor: from.older })}`);
  if (res.kind === "restart") return going() ? restart(client, path) : { kind: "discarded" };
  return res.kind === "ok" ? { kind: "ok", data: { ...from, items: [...from.items, ...res.data.items], older: res.data.next } } : res;
}

// What the next read starts from: the answer's timeline, the newest page again after a drop, else
// the one it had.
export const nextTimeline = <T,>(tl: Timeline<T>, res: ReadResult<Timeline<T>> | Dropped): Timeline<T> => (res.kind === "ok" ? res.data : res.kind === "dropped" ? UNREAD : tl);

// Whether a read after the first brought entries the last answered timeline did not show, a
// restart's included: W3 then reads the machine and its observations again (#424). A restart reads
// the entries again, so they compare by value.
export const advanced = <T,>(last: Timeline<T>, res: ReadResult<Timeline<T>> | Dropped) =>
  last.read && res.kind === "ok" && JSON.stringify(res.data.items[0]) !== JSON.stringify(last.items[0]);

export const POLL_MS = 5000;

// Reads now, then POLL_MS after each answer for which read returns true. While the page is
// hidden a due read, the first one too, waits for the next check, POLL_MS later; `visible` lets a
// read that waits its turn check again when it starts. Returns the stop.
export function polled(read: (visible: () => boolean) => Promise<boolean>, page: Pick<Document, "visibilityState"> = document): () => void {
  let timer: ReturnType<typeof setTimeout> | undefined;
  let live = true;
  const visible = () => page.visibilityState === "visible";
  const run = () => void read(visible).then((more) => {
    if (live && more) timer = setTimeout(due, POLL_MS);
  });
  const due = () => (visible() ? run() : (timer = setTimeout(due, POLL_MS)));
  due();
  return () => {
    live = false;
    clearTimeout(timer);
  };
}

type TimelineRead<T> = (from: Timeline<T>) => Promise<ReadResult<Timeline<T>> | Dropped>;

// The timeline's reads, one at a time, each on from what the one before left, each answer handed
// to `settle`; none starts once `live` fails, and none goes on once `going` fails. A read whose
// `when` fails by its turn (a poll queued while the page went hidden) sends nothing, changes
// nothing, and polling goes on. `settle` also learns whether the answer advanced past the last one
// answered, whichever read brought it: a poll, a Load older that restarted, or the read after a drop.
// Each says whether polling goes on.
export function queued<T>(live: () => boolean, going: () => boolean, settle: (res: ReadResult<Timeline<T>> | Dropped, moved: boolean) => void) {
  let shown: Timeline<T> = UNREAD;
  let last: Timeline<T> = UNREAD;
  let queue = Promise.resolve(true);
  return (read: TimelineRead<T>, when: () => boolean) =>
    (queue = queue.then(async () => {
      if (!live()) return false;
      if (!when()) return true;
      const res = await read(shown);
      settle(res, advanced(last, res));
      if (!going()) return false;
      shown = nextTimeline(shown, res);
      if (res.kind === "ok") last = res.data;
      const r = res.kind === "dropped" ? res.failed : res;
      return r.kind === "ok" || r.kind === "transport";
    }));
}

// The timeline, its newest page first and then polled while the page is visible, showing loading
// when the path changes and keeping what it shows when the page reads again: a new view reads the
// newest page again, as a list reads its first. A transport failure keeps polling, so the shell's
// offline banner clears once a poll answers again. A failure after a refused cursor drops the
// timeline and that cursor. A superseded poll reads no further page and settles as not live, so the
// shell still takes its refusal of the credentials. The poll and "Load older" read one at a time,
// each on from what the one before left; `older` is undefined at the timeline's start. The count
// goes up each time a read, a poll or a restarted Load older, brings entries the timeline did not show.
function useTimeline(path: string): [Load<Timeline<Entry>> | Dropped, (() => void) | undefined, boolean, number] {
  const { client, settle, undropped, again, view } = useReads();
  const [tl, put, held] = useHeld<Timeline<Entry>, Dropped>();
  const key = useRef<string | undefined>(undefined);
  const older = useRef<(() => void) | undefined>(undefined);
  const [busy, setBusy] = useState(false);
  const [advances, setAdvances] = useState(0);
  useEffect(() => {
    let live = true;
    const ok = undropped();
    if (key.current !== path) {
      key.current = path;
      put({ kind: "loading" });
    }
    // A read queued behind another starts only while neither a new view nor the process's refusal
    // has dropped it.
    const going = () => live && ok();
    const step = queued<Entry>(() => live, going, (res, moved) => {
      const [r, set, shows] = settling(res, put, held);
      settle(r, set, going, shows);
      if (moved && going()) setAdvances((n) => n + 1);
    });
    older.current = () => {
      setBusy(true);
      void step((from) => readOlder<Entry>(client, path, from, going), () => true).then(() => live && setBusy(false));
    };
    const stop = polled((visible) => step((from) => readLater<Entry>(client, path, from, going), visible));
    return () => {
      live = false;
      older.current = undefined;
      setBusy(false);
      stop();
    };
  }, [client, settle, undropped, put, held, view, path, again]);
  const more = useCallback(() => older.current?.(), []);
  return [tl, tl.kind === "ok" && tl.data.older !== undefined ? more : undefined, busy, advances];
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

const byBasis = (items: Observation[]) => [...items].sort((x, y) => y.basis - x.basis);

// What W3 keeps of a read within one view (#424): the last answer this view read, shown under a
// read again that failed, and whether a read restarted it from its first page, said through the
// reads after it. Only an answer new to `was` is this view's: a new view first shows the old view's
// answer, which it keeps nothing of. A new path or view, and a dropped read (a restart that failed),
// keep nothing; a new epoch and the process's recovery refusal end the page, and a refusal of the
// credentials never reaches it.
export interface Kept<T> { view: string; load?: Load<T> | Dropped; data: T | undefined; restarted: boolean }
export function kept<T>(was: Kept<T> | undefined, load: Load<T> | Dropped, view: string, restarted: (d: T) => boolean = () => false): Kept<T> {
  const k = was?.view === view && load.kind !== "loading" && load.kind !== "dropped" ? was : { view, data: undefined, restarted: false };
  const fresh = load !== was?.load;
  if (load.kind !== "ok" || (!fresh && k.data === undefined)) return { ...k, load };
  return { view, load, data: load.data, restarted: k.restarted || (fresh && restarted(load.data)) };
}
// The timeline keeps only its restart flag: a read again of the page (back online, the operator's
// read again) reads it from its newest page, an answer that carries no restart.
export type W3Kept = [Kept<Machine>, Kept<Listed<Observation>>, Kept<Timeline<Entry>>];
export const keptW3 = (was: W3Kept | undefined, m: Load<Machine>, obs: Load<Listed<Observation>> | Dropped, tl: Load<Timeline<Entry>> | Dropped, view: string): W3Kept =>
  [kept(was?.[0], m, view), kept(was?.[1], obs, view, (o) => o.restarted), kept(was?.[2], tl, view, (t) => t.restarted)];

// The machine and its observations read again for the timeline's advances, one read again at a
// time: `n` counts them, `left` is how many of the current one's two reads (the machine and its
// observations) have not settled, the first page's reads included. An advance while they are in
// flight does not abandon them: it marks one more read again, which starts when both settle.
// Advances faster than one read would otherwise discard every read again before it answers.
export type AdvanceReads = { n: number; left: number; dirty: boolean };
export const ADVANCE_READS: AdvanceReads = { n: 0, left: 2, dirty: false };
export const stepAdvance = (a: AdvanceReads, e: "advance" | { settled: number }): AdvanceReads => {
  const next = { n: a.n + 1, left: 2, dirty: false };
  if (e === "advance") return a.left > 0 ? { ...a, dirty: true } : next;
  if (e.settled !== a.n || a.left === 0) return a;
  return a.left === 1 && a.dirty ? next : { ...a, left: a.left - 1 };
};

// The timeline is read under the page's reads. The machine and its observations are read again, as
// when the page reads again, each time a timeline read brings entries W3 did not show, one read
// again at a time; the poll goes on unrestarted (#424).
export function MachineItem({ id }: { id: string }) {
  const path = `/machines/${encodeURIComponent(id)}`;
  const reads = useReads();
  const [tl, older, busy, advances] = useTimeline(`${path}/timeline`);
  const [again, step] = useReducer(stepAdvance, ADVANCE_READS);
  useEffect(() => void (advances > 0 && step("advance")), [advances]);
  const outer = reads.settle;
  const settle = useCallback<Reads["settle"]>((res, set, live, held) => {
    outer(res, set, live, held);
    step({ settled: again.n });
  }, [outer, again.n]);
  return (
    <ReadsProvider value={{ ...reads, settle, again: reads.again + again.n }}>
      <MachineState id={id} path={path} tl={tl} older={older} busy={busy} />
    </ReadsProvider>
  );
}

function MachineState({ id, path, tl, older, busy }: { id: string; path: string; tl: Load<Timeline<Entry>> | Dropped; older: (() => void) | undefined; busy: boolean }) {
  const m = useItem<Machine>(path);
  const [obs] = useList<Observation>(`${path}/observations`, "", true);
  const { view } = useReads();
  const k = useRef<W3Kept>(undefined);
  const [km, ko, kt] = (k.current = keptW3(k.current, m, obs, tl, view));
  return <MachineShown id={id} m={m} obs={obs} km={km} ko={ko} tl={tl} kt={kt} older={older} busy={busy} />;
}

// W3 as read: each read's answer, else what it keeps with its failure above it.
export function MachineShown({ id, m, obs, km, ko, tl, kt, older, busy }: { id: string; m: Load<Machine>; obs: Load<Listed<Observation>> | Dropped; km: Kept<Machine>; ko: Kept<Listed<Observation>>; tl: Load<Timeline<Entry>> | Dropped; kt: Kept<Timeline<Entry>>; older: (() => void) | undefined; busy: boolean }) {
  const [pm, po, timeline] = [usePending(m, "the machine"), usePending(obs, "the observations"), usePending(tl, "the timeline")];
  const d = m.kind === "ok" ? m.data : km.data;
  const read = obs.kind === "ok" ? obs.data : ko.data;
  if (d === undefined) return <><h2>Machine {short(id)}</h2>{pm}</>;
  if (read === undefined) return <><h2>Machine {short(id)}</h2>{m.kind !== "ok" && pm}{po}</>;
  const list = byBasis(read.items);
  return (
    <>
      <h2>Machine {d.id}</h2>
      {m.kind !== "ok" && pm}
      {obs.kind !== "ok" && po}
      <p>Cluster <Ref to={`/clusters/${d.cluster}`} id={d.cluster} />, {d.talosEndpoint}, {node(d)}, {d.platform}.</p>
      <ul aria-label="Status" data-testid="sentences">{sentences(d, list).map((s) => <li key={s}>{s}</li>)}</ul>
      <dl>
        <dt>Desired</dt><dd>{release(d.desired)}</dd>
        <dt>Applied</dt><dd>{d.applied === null ? "none" : <>{release(d.applied.release)} by {d.applied.source}</>}</dd>
        <dt>Observed</dt><dd data-testid="observed">{d.observed === null ? "none" : <>read at {at(d.observed.at)} (<span title={d.observed.observation}>{short(d.observed.observation)}</span>): {attribution(d.observed.release)}</>}</dd>
        <dt>Scope</dt><dd>{d.scopeState}</dd>
      </dl>
      <p>
        <Ref to={`/plans?machine=${d.id}`} id={d.id}>Plans for this machine</Ref>{" · "}
        <Ref to={`/operations?machine=${d.id}`} id={d.id}>Operations on this machine</Ref>
      </p>
      <h3>Observations</h3>
      <Observations read={{ ...read, restarted: ko.restarted }} />
      <h3>Timeline (newest first)</h3>
      {tl.kind !== "ok" ? timeline : <TimelineRows tl={{ ...tl.data, restarted: kt.restarted }} older={older} busy={busy} />}
    </>
  );
}

// A section read again from its start after a refused cursor says so, with a list's notice, until
// the view changes (PAGE-02, journeys §8 "Epoch changed").
const restartedNotice = (restarted: boolean, testid: string) => restarted && <Status testid={testid}>{RESTARTED}</Status>;

// The observations as read, newest basis first; the whole read, so its `restarted` goes with it.
export function Observations({ read }: { read: Listed<Observation> }) {
  const list = byBasis(read.items);
  return (
    <>
      {restartedNotice(read.restarted, "observations-restarted")}
      {list.length === 0 ? <p>No observation recorded.</p> : (
        <Scroll label="Observations" testid="observations">
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
        </Scroll>
      )}
    </>
  );
}

export function TimelineRows({ tl, older, busy }: { tl: Timeline<Entry>; older: (() => void) | undefined; busy: boolean }) {
  return (
    <>
      {restartedNotice(tl.restarted, "timeline-restarted")}
      {tl.items.length === 0 ? <p>No timeline entry recorded.</p> : (
        <>
          <Scroll label="Timeline" testid="timeline">
            <Head cols={["At", "Kind", "Details"]} />
            <tbody>
              {tl.items.map((e) => (
                <tr key={`${e.epoch}:${e.revision}`}>
                  <td>{at(e.at)}</td>
                  <td>{e.kind}</td>
                  <td>{details(e)}</td>
                </tr>
              ))}
            </tbody>
          </Scroll>
          {older !== undefined && <p><button type="button" onClick={older} disabled={busy}>Load older</button></p>}
        </>
      )}
    </>
  );
}
