// Path routes over the History API (decision B3 of ginsys/bronzeward#255): no router library. The
// static host answers every path outside /api/v1/ and /livez with the application.

import { type MouseEvent, useCallback, useEffect, useRef, useState } from "react";

// The navigation of the header wireframe; Recovery is the recovery console's (#261).
export const PAGES = [
  ["/machines", "Machines"],
  ["/changes", "Changes"],
  ["/plans", "Plans"],
  ["/operations", "Operations"],
  ["/sources", "Sources"],
  ["/audit", "Audit"],
] as const;

type Entries = Pick<History, "state" | "go" | "pushState" | "replaceState">;
type Ask = (to: string, from: string) => boolean;
const position = (state: unknown): number | undefined => {
  const at = typeof state === "object" && state !== null ? (state as { at?: unknown }).at : undefined;
  return typeof at === "number" ? at : undefined;
};

// How long the history must stay still before a refused move returns.
export const QUIET_MS = 100;

// Every entry carries its position in the history. A refused Back or Forward then returns to the
// shown page's position (history.go), so the entries on both sides stay. A traversal is relative
// and queued: a Back pressed while the leave question was open lands after the refusal. So, until
// the return ends, a traversal asks nothing and an in-app move does nothing, and the return is aimed
// once, after the history was still for QUIET_MS. If that does not reach the shown page's position
// (an entry went, or the traversal went past the end and fired nothing), the current entry takes
// the shown page's address instead. Either way the return ends with the address of the page shown.
export function positions(h: Entries, here: () => string, later: (f: () => void) => void = (f) => void setTimeout(f, QUIET_MS)) {
  // The position of the entry the history is at, as the page last saw it.
  let current = position(h.state) ?? 0;
  if (position(h.state) === undefined) h.replaceState({ at: current }, "");
  let shown = { to: here(), at: current };
  // The position a refusal returns to, until the return ends; whether its history.go was made.
  let home: number | undefined;
  let aimed = false;
  let still = 0;
  // The current entry's position. One without a position was added by a fragment link (Skip to the
  // page), after the entry the history was at.
  const locate = (): number => {
    let at = position(h.state);
    if (at === undefined) {
      at = current + 1;
      h.replaceState({ at }, "");
    }
    return (current = at);
  };
  const end = (at: number) => {
    if (here() !== shown.to) h.replaceState({ at }, "", shown.to);
    shown = { to: shown.to, at };
    home = undefined;
  };
  const settle = () => {
    const mine = ++still;
    later(() => {
      if (mine !== still || home === undefined) return;
      const at = locate();
      if (at === home || aimed) return end(at);
      aimed = true;
      h.go(home - at);
      settle();
    });
  };
  return {
    // On popstate: true when the page follows the move.
    popped(ask?: Ask): boolean {
      const to = here();
      const at = locate();
      if (home !== undefined) {
        if (at === home) end(at);
        else settle();
        return false;
      }
      if (ask !== undefined && !ask(to, shown.to)) {
        home = shown.at;
        aimed = false;
        settle();
        return false;
      }
      shown = { to, at };
      return true;
    },
    // An in-app move, which adds an entry unless `to` is the address already. During a return the
    // address is not the shown page's yet: the move does nothing and is false.
    moved(to: string): boolean {
      if (home !== undefined) return false;
      if (to === here()) return true;
      current = locate() + 1;
      shown = { to, at: current };
      h.pushState({ at: current }, "", to);
      return true;
    },
  };
}

// One per document: the positions outlive a remount of the view.
let entries: ReturnType<typeof positions> | undefined;
const ours = () => (entries ??= positions(history, () => location.pathname + location.search));

// The current path, its query, and a navigation that adds a history entry. The query is tracked
// apart from the path, so a query-only move (Back from ?cluster=c2 to ?cluster=c1) is seen too.
// `guard` is asked before Back or Forward moves the page from `from` to `to`; refused, the history
// returns to the entry of the page still shown.
export function usePath(guard?: Ask): [string, (to: string) => void, string] {
  const [path, setPath] = useState(location.pathname);
  const [query, setQuery] = useState(location.search);
  const asks = useRef(guard);
  asks.current = guard;
  const here = useCallback(() => {
    setPath(location.pathname);
    setQuery(location.search);
  }, []);
  useEffect(() => {
    ours();
    const popped = () => {
      if (ours().popped(asks.current)) here();
    };
    addEventListener("popstate", popped);
    return () => removeEventListener("popstate", popped);
  }, [here]);
  const go = useCallback((to: string) => {
    if (ours().moved(to)) here();
  }, [here]);
  return [path, go, query];
}

// A plain left click navigates in the page; any other keeps the browser's own handling.
export function follow(e: MouseEvent, go: (to: string) => void, to: string): void {
  if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
  e.preventDefault();
  go(to);
}
