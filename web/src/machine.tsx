// The machine detail (wireframe W3): the status sentences of journeys §2.4, derived only from the
// machine read and its observations, then the observations and the timeline. The timeline has no
// stream; it is polled with its cursor no more often than every 5 s while the page is visible
// (client contracts §8.6 item 6).

import { useEffect, useRef } from "react";
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

const at = (s: string) => clock(new Date(s));
const causes = (o: Observation) => Object.values(o.unread).map((u) => u.cause);

// The newest read, when it has a higher basis than the one observed names and could not read
// something. The machine read selects observed by highest basis among reads of the digest.
export function latestUnread(m: Machine, obs: Observation[]): Observation | undefined {
  const latest = obs.reduce<Observation | undefined>((a, o) => (a === undefined || o.basis > a.basis ? o : a), undefined);
  const basis = obs.find((o) => o.id === m.observed?.observation)?.basis ?? -1;
  return latest !== undefined && latest.basis > basis && causes(latest).length > 0 ? latest : undefined;
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

// A list read to its end, and the page it ended on: the last page has no next, so the next poll
// reads that page again with the cursor that read it and replaces its items from start.
export interface Tail<T> { items: T[]; start: number; cursor: string | undefined }

// Reads on while `going` holds, as readPages does: a read a newer one superseded sends no further
// request. A refused cursor (another epoch) reads the timeline again from its start, once; a failure
// after that is dropped, with what was read before it.
export async function readTail<T>(client: Reads["client"], path: string, from: Tail<T>, going: () => boolean = () => true): Promise<ReadResult<Tail<T>> | Dropped> {
  let { items, start, cursor } = from;
  let restarted = false;
  for (;;) {
    const res = await client.read<Page<T>>(cursor === undefined ? path : `${path}?${new URLSearchParams({ cursor })}`);
    if (res.kind === "restart" && !restarted && cursor !== undefined) {
      [items, start, cursor, restarted] = [[], 0, undefined, true];
    } else if (res.kind === "ok") {
      items = [...items.slice(0, start), ...res.data.items];
      if (res.data.next === undefined) return { kind: "ok", data: { items, start, cursor } };
      [start, cursor] = [items.length, res.data.next];
    } else {
      const failed = res.kind === "restart" ? { kind: "refused" as const, status: 400, problem: res.problem } : res;
      return restarted && failed.kind !== "discarded" ? { kind: "dropped", failed } : failed;
    }
    if (!going()) return { kind: "discarded" };
  }
}

const EMPTY: Tail<never> = { items: [], start: 0, cursor: undefined };
// The tail the next poll reads on from: the answer's, none after a drop, else the one it had.
export const nextTail = <T,>(tail: Tail<T>, res: ReadResult<Tail<T>> | Dropped): Tail<T> => (res.kind === "ok" ? res.data : res.kind === "dropped" ? EMPTY : tail);

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

// The timeline, read to its end and then polled while the page is visible, showing loading when the
// path changes and keeping what it shows when the page reads again. A transport failure keeps
// polling, so the shell's offline banner clears once a poll answers again. A failure after a refused
// cursor drops the timeline and that cursor. A superseded poll reads no further page and settles as
// not live, so the shell still takes its refusal of the credentials.
function useTimeline(path: string): Load<Tail<Entry>> | Dropped {
  const { client, settle, undropped, again } = useReads();
  const [tl, put, held] = useHeld<Tail<Entry>, Dropped>();
  const key = useRef<string | undefined>(undefined);
  useEffect(() => {
    let tail: Tail<Entry> = EMPTY;
    let live = true;
    const ok = undropped();
    if (key.current !== path) {
      key.current = path;
      put({ kind: "loading" });
    }
    const stop = polled(async () => {
      const res = await readTail<Entry>(client, path, tail, () => live && ok());
      const [r, set, shows] = settling(res, put, held);
      settle(r, set, () => live && ok(), shows);
      if (!live || !ok()) return false;
      tail = nextTail(tail, res);
      return r.kind === "ok" || r.kind === "transport";
    });
    return () => {
      live = false;
      stop();
    };
  }, [client, settle, undropped, put, held, path, again]);
  return tl;
}

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
  const tl = useTimeline(`${path}/timeline`);
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
        <dt>Observed</dt><dd>{d.observed === null ? "none" : <>read at {at(d.observed.at)} (<span title={d.observed.observation}>{short(d.observed.observation)}</span>)</>}</dd>
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
        <table data-testid="timeline">
          <Head cols={["At", "Kind", "Details"]} />
          <tbody>
            {[...tl.data.items].reverse().map((e) => (
              <tr key={`${e.epoch}:${e.revision}`}>
                <td>{at(e.at)}</td>
                <td>{e.kind}</td>
                <td>{details(e)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  );
}
