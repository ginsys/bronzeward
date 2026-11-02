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

// Every entry the page adds carries its position in the history. A refused Back or Forward then
// returns by the same distance (history.go), so the entries on both sides stay. The return fires one
// more popstate, which is not asked again. An entry without a position, which the page did not add,
// is left under a new entry instead.
export function positions(h: Entries, here: () => string) {
  if (position(h.state) === undefined) h.replaceState({ at: 0 }, "");
  let shown = { to: here(), at: position(h.state) ?? 0 };
  let back: number | undefined;
  return {
    // On popstate: true when the page follows the move.
    popped(ask?: Ask): boolean {
      const to = here();
      const at = position(h.state);
      if (back !== undefined && at === back) {
        back = undefined;
        return false;
      }
      back = undefined;
      if (ask !== undefined && !ask(to, shown.to)) {
        if (at === undefined) {
          shown = { to: shown.to, at: shown.at + 1 };
          h.pushState({ at: shown.at }, "", shown.to);
        } else {
          back = shown.at;
          h.go(shown.at - at);
        }
        return false;
      }
      if (at === undefined) h.replaceState({ at: shown.at + 1 }, "");
      shown = { to, at: at ?? shown.at + 1 };
      return true;
    },
    pushed(to: string): void {
      shown = { to, at: shown.at + 1 };
      h.pushState({ at: shown.at }, "", to);
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
    if (to !== location.pathname + location.search) ours().pushed(to);
    here();
  }, [here]);
  return [path, go, query];
}

// A plain left click navigates in the page; any other keeps the browser's own handling.
export function follow(e: MouseEvent, go: (to: string) => void, to: string): void {
  if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
  e.preventDefault();
  go(to);
}
