// The reads every list and item view shares (client contracts §2 pagination row, §8.2 cursor-invalid
// row): cursor pages read on request or all at once, a list restarted when its cursor is refused,
// path links per resource (B3) and shortened identifiers (G23).

import { createContext, type ReactNode, useCallback, useContext, useEffect, useRef, useState } from "react";
import type { Client, ReadResult } from "./request.ts";
import { follow } from "./router.ts";
import { Loading, Status } from "./states.tsx";

export type Load<T> = { kind: "loading" } | ReadResult<T>;
export type Failed = Exclude<ReadResult<unknown>, { kind: "ok" | "discarded" }>;

export interface Reads {
  client: Pick<Client, "read">;
  // Renders a read's answer while `live`, or hands it to the shell (sign-in, revocation, offline,
  // the process's refusal); without an answer, what `held` says is shown stays under the banner.
  settle: <T>(res: ReadResult<T>, set: (r: Load<T>) => void, live: () => boolean, held: () => boolean) => void;
  // From now, true until the process's refusal drops every read in flight.
  undropped: () => () => boolean;
  // The client's view, path and query: an answer read under another is discarded, so a page's
  // reads start again on each change, the browser's own filters included.
  view: string;
  // Bumped each time the page reads again.
  again: number;
  go: (to: string) => void;
  failure: (res: Failed) => ReactNode;
}
const ReadContext = createContext<Reads | undefined>(undefined);
export const ReadsProvider = ReadContext.Provider;
const useReads = () => useContext(ReadContext)!;

// What a view shows until its read answers with data: loading, nothing for a discarded read, or the
// failure; undefined once there is data.
export function usePending(load: Load<unknown>, what: string): ReactNode | undefined {
  const { failure } = useReads();
  if (load.kind === "loading") return <Loading what={what} />;
  if (load.kind === "discarded") return null;
  return load.kind === "ok" ? undefined : failure(load);
}

export const RESTARTED = "The server no longer accepted this list's position, so it was read again from its start.";

export interface Listed<T> { items: T[]; next: string | undefined; restarted: boolean }
type Page<T> = { items: T[]; next?: string };

// Reads on from `from`: one page, or every page left when `all`. A 400 cursor-invalid drops the
// items read so far and restarts once from the first page.
export async function readPages<T>(client: Reads["client"], path: string, query: string, from: Listed<T>, all: boolean): Promise<ReadResult<Listed<T>>> {
  let { items, next } = from;
  let restarted = false;
  for (;;) {
    const q = new URLSearchParams(query);
    if (next !== undefined) q.set("cursor", next);
    const res = await client.read<Page<T>>(q.size > 0 ? `${path}?${q}` : path);
    if (res.kind === "restart" && !restarted && next !== undefined) {
      [items, next, restarted] = [[], undefined, true];
      continue;
    }
    if (res.kind === "restart") return { kind: "refused", status: 400, problem: res.problem };
    if (res.kind !== "ok") return res;
    [items, next] = [[...items, ...res.data.items], res.data.next];
    if (!all || next === undefined) return { kind: "ok", data: { items, next, restarted } };
  }
}

// A read's result, and whether it holds data: a read again that finds no answer keeps that data
// under the offline banner, whatever the page's other reads showed.
function useHeld<T>(): [Load<T>, (r: Load<T>) => void, () => boolean] {
  const [load, setLoad] = useState<Load<T>>({ kind: "loading" });
  const has = useRef(false);
  const put = useCallback((r: Load<T>) => {
    has.current = r.kind === "ok";
    setLoad(r);
  }, []);
  return [load, put, useCallback(() => has.current, [])];
}

// A list read from its start each time the path, query or `all` changes, showing loading, and each
// time the view changes or the page reads again, keeping what it shows until the read answers: the
// same list under a new view is still the same data. `more` reads on. One read at a time: while a
// start's read or a `more` is in flight, `more` waits. A read that a newer start superseded settles
// as not live: the shell still takes its refusal of the credentials, and ignores the rest.
export function useList<T>(path: string, query: string, all: boolean): [Load<Listed<T>>, (() => void) | undefined, boolean] {
  const { client, settle, undropped, again, view } = useReads();
  const [list, put, held] = useHeld<Listed<T>>();
  const start = useRef(0);
  const key = useRef<string | undefined>(undefined);
  // The start whose read is in flight, the first page's or a `more`'s.
  const reading = useRef<number | undefined>(undefined);
  const [busy, setBusy] = useState(false);
  const read = useCallback((from: Listed<T>, every: boolean) => {
    const n = (reading.current = start.current);
    const ok = undropped();
    setBusy(true);
    void readPages<T>(client, path, query, from, every).then((res) => {
      const live = n === start.current;
      if (live) {
        reading.current = undefined;
        setBusy(false);
      }
      settle(res, put, () => live && ok(), held);
    });
  }, [client, settle, undropped, put, held, path, query]);
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
// page reads again, keeping what it shows until the read answers.
export function useItem<T>(path: string): Load<T> {
  const { client, settle, undropped, again, view } = useReads();
  const [item, put, held] = useHeld<T>();
  const key = useRef<string | undefined>(undefined);
  useEffect(() => {
    const ok = undropped();
    let live = true;
    if (key.current !== path) {
      key.current = path;
      put({ kind: "loading" });
    }
    void client.read<T>(path).then((res) => settle(res, put, () => live && ok(), held));
    return () => void (live = false);
  }, [client, settle, undropped, put, held, view, path, again]);
  return item;
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
