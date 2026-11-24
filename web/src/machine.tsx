// The machine detail (wireframe W3): the status sentences of journeys §2.4, derived only from the
// machine read and its observations, then the observations and the timeline. The timeline has no
// stream; it is polled with its cursor no more often than every 5 s while the page is visible
// (client contracts §8.6 item 6).

import { Fragment, type ReactNode, useCallback, useEffect, useMemo, useReducer, useRef, useState } from "react";
import type { components } from "./api/v1";
import { ChangeEndpoint, RenameMachine } from "./forms.tsx";
import { Detail, type Dropped, Head, Id, type Keep, type Listed, type Load, type Page, Ref, type Reads, ReadsProvider, RESTARTED, Scroll, settling, short, useHeld, useItem, useList, usePending, useReads } from "./list.tsx";
import { type Machine, machineHeading, machineName, node, release } from "./machines.tsx";
import { ClusterRef, Named, useReleaseNames } from "./titles.tsx";
import { safeWord } from "./safe.ts";
import { PageNote } from "./page-note.tsx";
import { CHANGED_ON_THE_MACHINE, CREATE_A_DRAFT, DECIDE_ON_THE_CHANGE, DIFFERS_IN_FORM_ONLY, FORM_ONLY_NOTE, FREEZE_THIS_MACHINE, MACHINE_NOTE, RELEASE_NOT_APPLIED_YET, roleWords, UNFREEZE_OR_DECIDE, UNFREEZE_THE_MACHINE } from "./words.ts";
import { HostWaits } from "./host-waits.tsx";
import { NextStep, PlanPreparedBy, type Position } from "./nextstep.tsx";
import { driftPath } from "./plan.tsx";
import { principalId } from "./principals.ts";
import { Who } from "./principals.tsx";
import type { ReadResult } from "./request.ts";
import { clock, Status } from "./states.tsx";

export type Observation = components["schemas"]["ObservationBody"];
export type Entry = components["schemas"]["TimelineEntry"];

export const ADOPTED =
  "Adopted from the node's own bytes; the release's compiled configuration may differ. The adopt plan's evidence says whether it does (release not applied yet); a later apply-config plan withholds its diff when it does, unless it differs in form only.";
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
export const causes = (o: Observation) => Object.entries(o.unread).map(([field, u]) => `${safeWord(field)} (${safeWord(u.cause)})`);

// The newest read, when it has a higher basis than the one observed names and could not read
// something. The machine read selects observed by highest basis among reads of the digest. The
// machine and its observations are read apart, so when observed names a read the observations
// do not hold yet, nothing is said to be newer than it.
export function latestUnread(m: Machine, obs: Observation[]): Observation | undefined {
  const latest = obs.reduce<Observation | undefined>((a, o) => (a === undefined || o.basis > a.basis ? o : a), undefined);
  const basis = m.observed === null ? -1 : obs.find((o) => o.id === m.observed?.observation)?.basis;
  return latest !== undefined && basis !== undefined && latest.basis > basis && causes(latest).length > 0 ? latest : undefined;
}

// The reads an open drift names, from the observations a page holds: the one that opened it and the one that last
// saw it. Either is undefined when the page does not hold it (the API serves no opening time, so its time is that
// of the observation that opened it). The drift page words them from the same lookup.
export function driftReads(drift: NonNullable<Machine["openDrift"]>, obs: Observation[]): { opened: Observation | undefined; last: Observation | undefined } {
  return { opened: obs.find((x) => x.id === drift.observation), last: obs.find((x) => x.id === drift.lastObservation) };
}

// The words of the machine page's standing link to the drift page, which follow the machine's state.
export const driftLinkText = (m: Pick<Machine, "openDrift" | "frozen">): string =>
  m.frozen ? (m.openDrift === null ? UNFREEZE_THE_MACHINE : UNFREEZE_OR_DECIDE) : m.openDrift === null ? FREEZE_THIS_MACHINE : DECIDE_ON_THE_CHANGE;

// Journeys §2.4's sentences. openDrift alone says "drifted"; a matching release never says healthy.
export function sentences(m: Machine, obs: Observation[], name: (release: string) => string = short): string[] {
  const { desired: d, applied: a, observed: o, openDrift: drift } = m;
  const out: string[] = [];
  if (drift !== null) {
    const { opened } = driftReads(drift, obs);
    out.push(`${CHANGED_ON_THE_MACHINE} since ${opened === undefined ? `the read ${short(drift.observation)}` : at(opened.at)}: a read differed from what Bronzeward last verified (${a === null ? "none" : name(a.release)}).`);
    if (o?.release != null && o.release === d) out.push("It equals the selected release; that does not close the drift.");
  } else if (o === null) {
    out.push("No configuration read recorded.");
  } else if (a !== null && o.release === a.release) {
    out.push(`The configuration read at ${at(o.at)} is attributed to ${name(o.release)}, which is the Applied release (by ${safeWord(a.source)}).`);
    if (a.source === "adoption") out.push(ADOPTED);
    if (a.source === "adoption" && d === a.release) out.push(EITHER);
  } else if (o.release === null) {
    out.push(`The configuration read at ${at(o.at)} matched neither Applied nor Desired, and no drift is open; an operation may hold the machine. See the timeline.`);
  } else {
    // The read is the selected release's with no drift open: an operation holds the machine.
    out.push(`The configuration read at ${at(o.at)} is attributed to the selected release ${name(o.release)}.`);
  }
  const late = latestUnread(m, obs);
  if (late !== undefined) out.push(`The latest read at ${at(late.at)} could not read: ${causes(late).join("; ")}.`);
  if (a === null) out.push("Not applied or adopted by Bronzeward.");
  if (d === null) out.push("No release selected.");
  else if (m.pendingConvergence && a === null) out.push(`${RELEASE_NOT_APPLIED_YET}: the selected release ${name(d)} has not been applied to this machine.`);
  else if (m.formOnly) out.push(`${DIFFERS_IN_FORM_ONLY}: the selected release ${name(d)} for this machine. ${FORM_ONLY_NOTE}`);
  else if (m.pendingConvergence && drift === null) out.push(`${RELEASE_NOT_APPLIED_YET}: the configuration of the selected release ${name(d)} for this machine differs from what Bronzeward applied.`);
  else if (!m.pendingConvergence && drift === null && a !== null && d !== a.release) out.push(`The selected release ${name(d)} carries this machine's applied configuration unchanged.`);
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
// An identifier is shortened, and links to its page where its prefix has one (a plan, an operation: W3);
// a principal shows its name (G23). A name-change entry (client contracts A9 item 4) stores the
// machine's previous name (null on a first naming) and its new one: they show, old then new, only
// where they pass the name grammar. Other kinds store `previous` and `new` too (endpoint-change), so
// this is by kind. Any other kind, known or not, renders from the members it has.
export function details(e: Entry): ReactNode {
  const r = (e.entry ?? {}) as Record<string, unknown>;
  const names = e.kind === "name-change" ? [r["previous"], r["new"]].map(machineName).filter((n): n is string => n !== undefined) : [];
  return [...names.map((n, i) => ({ k: `name${i}`, v: n })), ...SHOWN.filter((k) => typeof r[k] === "string" && r[k] !== "").map((k) => ({ k, v: r[k] as string }))].map(({ k, v }, i) => (
    <Fragment key={k}>{i > 0 && " "}{k === "reason" ? `"${safeWord(v)}"` : k === "principal" && principalId(v) ? <Who id={v} /> : (k === "plan" || k === "operation" || ((k === "from" || k === "to") && v.startsWith("rel_"))) && /^[a-z]+_[a-z2-7]{26}$/.test(v) ? <Named id={v} /> : /^[a-z]+_[a-z2-7]{26}$/.test(v) ? <Id id={v} /> : safeWord(v)}</Fragment>
  ));
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

// The machine, its observations and its position read again for the timeline's advances, one read again at a
// time: `n` counts them, `left` is how many of the current one's three reads (the machine, its
// observations and its position) have not settled, the first page's reads included. An advance while they are in
// flight does not abandon them: it marks one more read again, which starts when all settle.
// Advances faster than one read would otherwise discard every read again before it answers. Each
// read is counted under the page's own reads (`key`: its view and read again): a new key starts its
// own three reads, and a read of an earlier key, which still settles, counts for nothing.
export type AdvanceReads = { key: string; n: number; left: number; dirty: boolean };
export const advanceReads = (key: string): AdvanceReads => ({ key, n: 0, left: 3, dirty: false });
export const stepAdvance = (a: AdvanceReads, e: "advance" | { key: string } | { key: string; settled: number }): AdvanceReads => {
  const next = { ...a, n: a.n + 1, left: 3, dirty: false };
  if (e === "advance") return a.left > 0 ? { ...a, dirty: true } : next;
  if (!("settled" in e)) return e.key === a.key ? a : { ...a, key: e.key, left: 3, dirty: false };
  if (e.key !== a.key || e.settled !== a.n || a.left === 0) return a;
  return a.left === 1 && a.dirty ? next : { ...a, left: a.left - 1 };
};
// The page's settle, which also counts the read for the read again in flight. Every answer goes to
// the page's settle (a refusal of the credentials ends the page from any read), but only a live read
// counts: a read the page no longer waits for, such as one from an earlier visit to the same view,
// can carry the same key and read again.
export const countingSettle = (outer: Reads["settle"], step: (e: { key: string; settled: number }) => void, key: string, n: number): Reads["settle"] =>
  (res, set, live, held) => {
    const lost = outer(res, set, live, held);
    if (live()) step({ key, settled: n });
    return lost;
  };

// The timeline is read under the page's reads. The machine and its observations are read again, as
// when the page reads again, each time a timeline read brings entries W3 did not show, one read
// again at a time; the poll goes on unrestarted (#424).
export function MachineItem({ id }: { id: string }) {
  const path = `/machines/${encodeURIComponent(id)}`;
  const reads = useReads();
  const [tl, older, busy, advances] = useTimeline(`${path}/timeline`);
  const key = `${reads.view}\n${reads.again}`;
  const [again, step] = useReducer(stepAdvance, key, advanceReads);
  useEffect(() => step({ key }), [key]);
  useEffect(() => void (advances > 0 && step("advance")), [advances]);
  const settle = useMemo(() => countingSettle(reads.settle, step, key, again.n), [reads.settle, key, again.n]);
  // A banner end's read again and a changed endpoint read the machine and its observations again as
  // an advance does: after the reads in flight settle, and once for any advances while they are in
  // flight.
  const readAgain = useCallback(() => step("advance"), []);
  return (
    <ReadsProvider value={{ ...reads, settle, again: reads.again + again.n }}>
      <MachineState id={id} path={path} tl={tl} older={older} busy={busy} readAgain={readAgain} changed={readAgain} />
    </ReadsProvider>
  );
}

// A read of the machine or its observations that finds no answer keeps what this view kept under the
// offline banner, and none of an old view's answer (client contracts §8.6 item 6, #443).
export function keepW3(k: { current: W3Kept | undefined }, i: 0 | 1, lost: { current: [boolean, boolean] }): Keep {
  return { held: () => k.current?.[i].data !== undefined, lost: (l) => void (lost.current[i] = l) };
}
// Whether a banner ended by an answer asks for a read again: a resource found no answer since the
// last one. It clears the marks it reads.
export function lostSince(lost: { current: [boolean, boolean] }): boolean {
  const [m, o] = lost.current;
  lost.current = [false, false];
  return m || o;
}

// Runs `run` when asked, and paces the work it asks for at most once per POLL_MS from when that work
// starts: `run` can only ask, and the read-again counter can hold the read again behind a slow read
// in flight. An ask within POLL_MS of the last start waits until then; asks while one waits, or while
// a run's work has not started, add nothing. `started` marks the start; `stop` drops what waits. A
// transport failure answers at once, so a read again after each banner end, unpaced, could follow a
// resource that keeps failing at network speed.
export function pacer(run: () => void, now: () => number = Date.now, after: (f: () => void, ms: number) => ReturnType<typeof setTimeout> = setTimeout): [() => void, () => void, () => void] {
  let last = -Infinity;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let asked = false;
  const go = () => {
    timer = undefined;
    asked = true;
    run();
  };
  const ask = () => {
    if (timer !== undefined || asked) return;
    const wait = last + POLL_MS - now();
    if (wait <= 0) go();
    else timer = after(go, wait);
  };
  const started = () => {
    if (!asked) return;
    asked = false;
    last = now();
  };
  const stop = () => {
    clearTimeout(timer);
    timer = undefined;
    asked = false;
  };
  return [ask, started, stop];
}

// W3's read again after a banner end, from what the page shows at each render: its view, its read
// again count (`again`, which changes when a read again of the page starts) and its count of banner
// ends (`regained`). A banner end asks the pacer when a resource found no answer since the last one;
// a change of `again` marks the start; a new view drops what the old view asked for and its marks.
// The first render only notes what it sees. Returns the observe and the stop.
export function recovery(lost: { current: [boolean, boolean] }, run: () => void, now?: () => number, after?: (f: () => void, ms: number) => ReturnType<typeof setTimeout>): [(view: string, again: number, regained: number) => void, () => void] {
  const [ask, started, stop] = pacer(run, now, after);
  let seen: [string, number, number] | undefined;
  const observe = (view: string, again: number, regained: number) => {
    const was = seen;
    seen = [view, again, regained];
    if (was === undefined) return;
    if (was[0] !== view) {
      stop();
      lost.current = [false, false];
      return;
    }
    if (was[1] !== again) started();
    if (was[2] !== regained && lostSince(lost)) ask();
  };
  return [observe, stop];
}

// A read that keeps no earlier answer: a failure replaces what it showed.
export const NO_KEEP: Keep = { held: () => false, lost: () => undefined };

// What the line is drawn from: the position of an answered read and the recovery mode that answer was served
// under, and nothing otherwise.
export const positionShown = (pos: Load<Position>): { position: Position | undefined; recovery: boolean } =>
  pos.kind === "ok" ? { position: pos.data, recovery: pos.recoveryMode === true } : { position: undefined, recovery: false };

// When a read's answer ends the banner and a resource found no answer, the machine and its
// observations are read again once, as for a timeline advance: one read again at a time, so this
// read again and the timeline's do not abandon each other, and at most once per POLL_MS from when the
// last one started (`recovery`).
function MachineState({ id, path, tl, older, busy, readAgain, changed }: { id: string; path: string; tl: Load<Timeline<Entry>> | Dropped; older: (() => void) | undefined; busy: boolean; readAgain: () => void; changed: () => void }) {
  const { view, regained, again: n } = useReads();
  const k = useRef<W3Kept>(undefined);
  const lost = useRef<[boolean, boolean]>([false, false]);
  const m = useItem<Machine>(path, keepW3(k, 0, lost));
  const [obs] = useList<Observation>(`${path}/observations`, "", true, keepW3(k, 1, lost));
  // The position keeps nothing: its line is drawn only from its own latest answer (operator experience §2.3),
  // and the recovery mode it is read under is that answer's own header, never another response's.
  const pos = useItem<Position>(`${path}/position`, NO_KEEP);
  const again = useRef(readAgain);
  again.current = readAgain;
  const [observe, stop] = useMemo(() => recovery(lost, () => again.current()), []);
  useEffect(() => stop, [stop]);
  useEffect(() => observe(view, n, regained));
  const [km, ko, kt] = (k.current = keptW3(k.current, m, obs, tl, view));
  return <MachineShown id={id} m={m} obs={obs} km={km} ko={ko} tl={tl} kt={kt} older={older} busy={busy} changed={changed} {...positionShown(pos)} />;
}

// W3 as read: each read's answer, else what it keeps with its failure above it. The machine's rows
// need only the machine: with no observations to show, they show without the latest read's line and
// the observations' list (client contracts §8.6 item 6, case 5).
export function MachineShown({ id, m, obs, km, ko, tl, kt, older, busy, changed, position, recovery }: { id: string; m: Load<Machine>; obs: Load<Listed<Observation>> | Dropped; km: Kept<Machine>; ko: Kept<Listed<Observation>>; tl: Load<Timeline<Entry>> | Dropped; kt: Kept<Timeline<Entry>>; older: (() => void) | undefined; busy: boolean; changed?: () => void; position?: Position | undefined; recovery?: boolean | undefined }) {
  const [pm, po, timeline] = [usePending(m, "the machine"), usePending(obs, "the observations"), usePending(tl, "the timeline")];
  const d = m.kind === "ok" ? m.data : km.data;
  const read = obs.kind === "ok" ? obs.data : ko.data;
  const name = useReleaseNames(d?.desired, d?.applied?.release, d?.observed?.release);
  if (d === undefined) return <><h2>Machine {short(id)}</h2>{pm}</>;
  const list = read === undefined ? [] : byBasis(read.items);
  return (
    <>
      <h2>{machineHeading(d)}</h2>
      <Detail id={d.id} />
      <PageNote parts={[MACHINE_NOTE]}> <Ref to="/drafts/new" id="drafts">{CREATE_A_DRAFT}</Ref>.</PageNote>
      {m.kind !== "ok" && pm}
      {obs.kind !== "ok" && po}
      <p><ClusterRef id={d.cluster} />, {d.talosEndpoint}, {node(d)}, {safeWord(d.platform)}{d.role !== null && d.role !== undefined && <>, {safeWord(roleWords(d.role))}</>}.</p>
      {changed !== undefined && <RenameMachine machine={d} onChanged={changed} />}
      {changed !== undefined && <ChangeEndpoint machine={d} onChanged={changed} />}
      <ul aria-label="Status" data-testid="sentences">{sentences(d, list, name).map((s) => <li key={s}>{s}</li>)}</ul>
      <p data-testid="drift-link"><Ref to={driftPath(d.id)} id={d.id}>{driftLinkText(d)}</Ref></p>
      <NextStep position={position} recovery={recovery} />
      <HostWaits position={position} />
      <PlanPreparedBy position={position} />
      <dl>
        <dt>Desired</dt><dd>{release(d.desired)}</dd>
        <dt>Applied</dt><dd>{d.applied === null ? "none" : <>{release(d.applied.release)} by {safeWord(d.applied.source)}</>}</dd>
        <dt>Import base</dt><dd data-testid="import-base"><Id id={d.applied?.importBase} none="none" /></dd>
        <dt>Observed</dt><dd data-testid="observed">{d.observed === null ? "none" : <>read at {at(d.observed.at)} (<span title={d.observed.observation}>{short(d.observed.observation)}</span>): {attribution(d.observed.release)}</>}</dd>
        <dt>Scope</dt><dd>{safeWord(d.scopeState)}</dd>
      </dl>
      <p>
        <Ref to={`/plans?machine=${d.id}`} id={d.id}>Plans for this machine</Ref>{" · "}
        <Ref to={`/operations?machine=${d.id}`} id={d.id}>Operations on this machine</Ref>{" · "}
        <Ref to={`/ingestions?machine=${d.id}`} id={d.id}>Imports of this machine</Ref>{" · "}
        <Ref to={`/ingestions/new?machine=${d.id}`} id={d.id}>Import this machine</Ref>
      </p>
      {read !== undefined && <><h3>Observations</h3><Observations read={{ ...read, restarted: ko.restarted }} /></>}
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
                  <td>{safeWord(e.kind)}</td>
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
