// The reads every list and item view shares (client contracts §2 pagination row, §8.2 cursor-invalid
// row): cursor pages read on request or all at once, a list restarted when its cursor is refused,
// path links per resource (B3) and shortened identifiers (G23).

import { createContext, type ReactNode, useCallback, useContext, useEffect, useRef, useState } from "react";
import type { Client, ReadResult } from "./request.ts";
import { follow } from "./router.ts";
import { Empty, Loading, Status } from "./states.tsx";

export type Load<T> = { kind: "loading" } | ReadResult<T>;
export type Failed = Exclude<ReadResult<unknown>, { kind: "ok" | "discarded" }>;
// A list's read that failed after the server refused its cursor: what was read before is gone with
// the cursor (§8.2), so nothing of it stays shown.
export type Dropped = { kind: "dropped"; failed: Failed };

export interface Reads {
  client: Pick<Client, "read">;
  // Renders a read's answer while `live`, or hands it to the shell (sign-in, revocation, offline,
  // the process's refusal); without an answer, what `held` says is shown stays under the banner,
  // and the result is true.
  settle: <T>(res: ReadResult<T>, set: (r: Load<T>) => void, live: () => boolean, held: () => boolean) => boolean;
  // From now, true until the shell invalidates every read in flight: the process's refusal, a new
  // epoch, a revocation or sign-out.
  undropped: () => () => boolean;
  // The client's view, path and query: an answer read under another is discarded, so a page's
  // reads start again on each change, the browser's own filters included.
  view: string;
  // Bumped each time the page reads again.
  again: number;
  // Bumped each time the offline banner ends because a read answered.
  regained: number;
  go: (to: string) => void;
  failure: (res: Failed) => ReactNode;
}
const ReadContext = createContext<Reads | undefined>(undefined);
export const ReadsProvider = ReadContext.Provider;
export const useReads = () => useContext(ReadContext)!;

// What a view shows until its read answers with data: loading, nothing for a discarded read, or the
// failure; undefined once there is data.
export function usePending(load: Load<unknown> | Dropped, what: string): ReactNode | undefined {
  const { failure } = useReads();
  if (load.kind === "loading") return <Loading what={what} />;
  if (load.kind === "discarded") return null;
  if (load.kind === "dropped") return <><Status testid="restarted">{RESTARTED}</Status>{failure(load.failed)}</>;
  return load.kind === "ok" ? undefined : failure(load);
}

export const RESTARTED = "The server no longer accepted this list's position, so it was read again from its start.";

// `extra` holds the last page's other members, such as the dependencies' lastPass.
export interface Listed<T> { items: T[]; next: string | undefined; restarted: boolean; extra?: Record<string, unknown> }
export type Page<T> = { items: T[]; next?: string };

// Reads on from `from`: one page, or every page left when `all`, while `going` holds: a read a newer
// one superseded sends no further request, though the answer in flight still settles. A 400
// cursor-invalid drops the items read so far and restarts once from the first page; a failure after
// that is dropped, never the list read before it.
export async function readPages<T>(client: Reads["client"], path: string, query: string, from: Listed<T>, all: boolean, going: () => boolean = () => true): Promise<ReadResult<Listed<T>> | Dropped> {
  let { items, next } = from;
  let restarted = false;
  for (;;) {
    const q = new URLSearchParams(query);
    if (next !== undefined) q.set("cursor", next);
    const res = await client.read<Page<T>>(q.size > 0 ? `${path}?${q}` : path);
    if (res.kind === "restart" && !restarted && next !== undefined) {
      [items, next, restarted] = [[], undefined, true];
    } else if (res.kind === "ok") {
      const { items: got, next: after, ...extra } = res.data;
      [items, next] = [[...items, ...got], after];
      if (!all || next === undefined) return { kind: "ok", data: { items, next, restarted, ...(Object.keys(extra).length > 0 && { extra }) } };
    } else {
      const failed = res.kind === "restart" ? { kind: "refused" as const, status: 400, problem: res.problem } : res;
      return restarted && failed.kind !== "discarded" ? { kind: "dropped", failed } : failed;
    }
    if (!going()) return { kind: "discarded" };
  }
}

// A read's result, and whether it holds data: a read again that finds no answer keeps that data
// under the offline banner, whatever the page's other reads showed.
export function useHeld<T, X extends { kind: string } = never>(): [Load<T> | X, (r: Load<T> | X) => void, () => boolean] {
  const [load, setLoad] = useState<Load<T> | X>({ kind: "loading" });
  const has = useRef(false);
  const put = useCallback((r: Load<T> | X) => {
    has.current = r.kind === "ok";
    setLoad(r);
  }, []);
  return [load, put, useCallback(() => has.current, [])];
}

// What settles a list's read: a dropped read hands its failure on, holds no data, so a failure
// without answer is never kept under the offline banner, and is shown as dropped.
export function settling<D>(res: ReadResult<D> | Dropped, put: (r: Load<D> | Dropped) => void, held: () => boolean): [ReadResult<D>, (r: Load<D>) => void, () => boolean] {
  return res.kind === "dropped" ? [res.failed, () => put(res), () => false] : [res, put, held];
}

// What a view that keeps its own data gives a read (W3, #443): whether it keeps data now, which a
// read without answer keeps under the banner; and, at each read that settles, whether it found no
// answer and kept the data. The view reads again through the page's own `again`.
export interface Keep { held: () => boolean; lost: (lost: boolean) => void }

// The read's own `held`, or the keeping view's; `lost` is told each settled read's outcome.
function useKeep(keep: Keep | undefined, held: () => boolean): [() => boolean, (lost: boolean) => void] {
  const k = useRef(keep);
  k.current = keep;
  return [useCallback(() => (k.current === undefined ? held() : k.current.held()), [held]), useCallback((lost: boolean) => k.current?.lost(lost), [])];
}

// Settles a read and tells `lost` its outcome, only while it is live: a read a newer one superseded
// settles late with nothing kept, and must not clear the lost mark the newer read set.
export function settleKept<T>(settle: Reads["settle"], lost: (lost: boolean) => void, res: ReadResult<T>, set: (r: Load<T>) => void, live: () => boolean, held: () => boolean): void {
  const kept = settle(res, set, live, held);
  if (live()) lost(kept);
}

// A list read from its start each time the path, query or `all` changes, showing loading, and each
// time the view changes or the page reads again, keeping what it shows until the read answers: the
// same list under a new view is still the same data. `more` reads on. One read at a time: while a
// start's read or a `more` is in flight, `more` waits. A read that a newer start superseded reads no
// further page and settles as not live: the shell still takes its refusal of the credentials, and
// ignores the rest.
export function useList<T>(path: string, query: string, all: boolean, keep?: Keep): [Load<Listed<T>> | Dropped, (() => void) | undefined, boolean] {
  const { client, settle, undropped, again, view } = useReads();
  const [list, put, own] = useHeld<Listed<T>, Dropped>();
  const [held, lost] = useKeep(keep, own);
  const start = useRef(0);
  const key = useRef<string | undefined>(undefined);
  // The start whose read is in flight, the first page's or a `more`'s.
  const reading = useRef<number | undefined>(undefined);
  const [busy, setBusy] = useState(false);
  const read = useCallback((from: Listed<T>, every: boolean) => {
    const n = (reading.current = start.current);
    const ok = undropped();
    setBusy(true);
    void readPages<T>(client, path, query, from, every, () => n === start.current).then((res) => {
      const live = n === start.current;
      if (live) {
        reading.current = undefined;
        setBusy(false);
      }
      const [r, set, shows] = settling(res, put, held);
      settleKept(settle, lost, r, set, () => live && ok(), shows);
    });
  }, [client, settle, undropped, put, held, lost, path, query]);
  useEffect(() => {
    start.current++;
    const k = JSON.stringify([path, query, all]);
    if (key.current !== k) {
      key.current = k;
      put({ kind: "loading" });
    }
    read({ items: [], next: undefined, restarted: false }, all);
    return () => void start.current++;
  }, [read, put, view, path, query, all, again]);
  const more = useCallback(() => {
    if (list.kind === "ok" && reading.current !== start.current) read(list.data, false);
  }, [read, list]);
  return [list, list.kind === "ok" && list.data.next !== undefined ? more : undefined, busy];
}

// An item read each time its path changes, showing loading, and each time the view changes or the
// page reads again, keeping what it shows until the read answers. A change of `n` reads it again.
// `onSettled` is told each time a read settles while it is live, whatever it found, a read the shell
// kept the old data for included; a read a newer one superseded does not tell it (ginsys/bronzeward#820).
export function useItem<T>(path: string, keep?: Keep, n = 0, onSettled?: () => void): Load<T> {
  const { client, settle, undropped, again, view } = useReads();
  const [item, put, own] = useHeld<T>();
  const [held, lost] = useKeep(keep, own);
  const key = useRef<string | undefined>(undefined);
  const told = useRef(onSettled);
  told.current = onSettled;
  useEffect(() => {
    const ok = undropped();
    let live = true;
    if (key.current !== path) {
      key.current = path;
      put({ kind: "loading" });
    }
    void client.read<T>(path).then((res) => {
      settleKept(settle, lost, res, put, () => live && ok(), held);
      if (live && ok()) told.current?.();
    });
    return () => void (live = false);
  }, [client, settle, undropped, put, held, lost, view, path, again, n]);
  return item;
}

// A one-off read a view starts itself, settled through the shell as every page read is: a refusal of
// the credentials ends the page, the process's refusal shows the state before the entry, and a read
// without answer shows the offline banner, the view's own text kept (ginsys/bronzeward#517 H11). It
// answers the read's result while `live` and not invalidated by the shell; otherwise undefined.
export async function settledRead<T>(reads: Pick<Reads, "client" | "settle" | "undropped">, path: string, live: () => boolean = () => true, held: () => boolean = () => true): Promise<Load<T> | undefined> {
  const ok = reads.undropped();
  const res = await reads.client.read<T>(path);
  let out: Load<T> | undefined;
  reads.settle(res, (r) => void (out = r), () => live() && ok(), held);
  return out;
}
export function useSettled() {
  const { client, settle, undropped } = useReads();
  return useCallback(<T,>(path: string, live?: () => boolean, held?: () => boolean) => settledRead<T>({ client, settle, undropped }, path, live, held), [client, settle, undropped]);
}

// mch_k2pq…a7xz: the prefix and the first and last four characters; the whole in the title.
export const short = (id: string) => {
  const i = id.indexOf("_") + 1;
  return id.length - i > 10 ? `${id.slice(0, i + 4)}…${id.slice(-4)}` : id;
};

// A resource's own path, opened in the page or, with the browser's own handling, in a new tab.
export function Ref({ to, id, children }: { to: string; id: string; children?: ReactNode }) {
  const { go } = useReads();
  return <a href={to} title={id} onClick={(e) => follow(e, go, to)}>{children ?? short(id)}</a>;
}

// The view each identifier prefix links to; any other identifier is shown shortened.
const VIEWS: Record<string, string> = {
  cl: "clusters", drf: "drafts", mch: "machines", frg: "fragments", frv: "fragment-revisions", prf: "profiles", prv: "profile-revisions",
  asg: "assignments", asr: "assignment-revisions", rel: "releases", pln: "plans", op: "operations", dep: "dependencies",
  ing: "ingestions",
};
export const viewOf = (id: string) => {
  const prefix = id.slice(0, Math.max(id.indexOf("_"), 0));
  return Object.hasOwn(VIEWS, prefix) ? VIEWS[prefix] : undefined;
};

export function Id({ id, none = "none" }: { id: string | null | undefined; none?: string }) {
  if (id == null) return none;
  const view = viewOf(id);
  return view === undefined ? <span title={id}>{short(id)}</span> : <Ref to={`/${view}/${encodeURIComponent(id)}`} id={id} />;
}

export const Head = ({ cols }: { cols: readonly string[] }) => <thead><tr>{cols.map((c) => <th key={c} scope="col">{c}</th>)}</tr></thead>;

// A table that scrolls on its own at narrow widths instead of the page (WCAG 1.4.10), reachable
// by keyboard to scroll.
export const Scroll = ({ label, testid, children }: { label: string; testid?: string; children: ReactNode }) => (
  <div className="scroll" role="region" aria-label={label} tabIndex={0}><table data-testid={testid}>{children}</table></div>
);

// A list with nothing in it, which still says when it was read again from its start (PAGE-02).
export const EmptyList = ({ list, text }: { list: Listed<unknown>; text: string }) => (
  <>{list.restarted && <Status testid="restarted">{RESTARTED}</Status>}<Empty text={text} /></>
);

// The count, the next page on request (one at a time), and the restart notice.
export function More({ list, shown, more, busy }: { list: Listed<unknown>; shown: number; more: (() => void) | undefined; busy: boolean }) {
  return (
    <>
      {list.restarted && <Status testid="restarted">{RESTARTED}</Status>}
      <p data-testid="count">
        {more === undefined ? `Showing ${shown} of ${list.items.length}.` : `Showing ${shown}; more pages to read.`}{" "}
        {more !== undefined && <button type="button" onClick={more} disabled={busy}>Show more</button>}
      </p>
    </>
  );
}
