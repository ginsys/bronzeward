// Path routes over the History API (decision B3 of ginsys/bronzeward#255): no router library. The
// static host answers every path outside /api/v1/ and /livez with the application.

import { type MouseEvent, useCallback, useEffect, useRef, useState } from "react";

// The navigation of the header wireframe. Recovery is the recovery console's (#261, #364): the shell
// shows it to a recovery-admin, and to every role while recovery mode is on.
export const PAGES = [
  ["/machines", "Machines"],
  ["/changes", "Changes"],
  ["/plans", "Plans"],
  ["/operations", "Operations"],
  ["/sources", "Sources"],
  ["/audit", "Audit"],
  ["/recovery", "Recovery"],
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
// The traversal of a return that ended by that fallback may still be queued and land later (#505),
// behind an in-app move or not. For two more quiet periods, the first move that lands where that
// traversal lands is taken for it: it moves no page and asks nothing, and the history returns again,
// without arming another marker. Position cannot tell that traversal from the operator's own move to
// the same entry, so the marker is short. An own move of the same distance in that time is taken for
// it too: nothing is asked and the history returns to the shown page. After the marker expires, a
// move reaches the leave question like any other.
export function positions(h: Entries, here: () => string, later: (f: () => void) => void = (f) => void setTimeout(f, QUIET_MS)) {
  // The position of the entry the history is at, as the page last saw it.
  let current = position(h.state) ?? 0;
  if (position(h.state) === undefined) h.replaceState({ at: current }, "");
  let shown = { to: here(), at: current };
  // The position a refusal returns to, until the return ends; whether its history.go was made.
  let home: number | undefined;
  let aimed = false;
  let still = 0;
  // The distance of the return's history.go, and, once the fallback ended the return, of the
  // traversal that may still land.
  let aim = 0;
  let stray: number | undefined;
  let lapse = 0;
  // Whether the return began with a refusal. Only that return arms `stray`: one that began with a late
  // traversal never does, or slow returns would bounce for good.
  let refusal = false;
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
      if (at === home) return end(at);
      if (aimed) {
        end(at);
        if (refusal) {
          const mine = ++lapse;
          stray = aim;
          later(() => later(() => void (mine === lapse && (stray = undefined))));
        }
        return;
      }
      aimed = true;
      aim = home - at;
      h.go(aim);
      settle();
    });
  };
  return {
    // On popstate: true when the page follows the move.
    popped(ask?: Ask): boolean {
      const to = here();
      const from = current;
      const fragment = position(h.state) === undefined;
      const at = locate();
      if (home !== undefined) {
        if (at === home) end(at);
        else settle();
        return false;
      }
      // The late traversal of a return that ended: the history returns again, and nothing is asked. A
      // fragment link's entry is not it, and a move that is not it leaves the marker for the traversal
      // still to come.
      const late = !fragment && stray !== undefined && at === from + stray;
      refusal = !late;
      if (late || (ask !== undefined && !ask(to, shown.to))) {
        stray = undefined;
        home = shown.at;
        aimed = false;
        settle();
        return false;
      }
      shown = { to, at };
      return true;
    },
    // Whether a refused move is returning: an in-app move does nothing, so the page asks nothing for it.
    returning(): boolean {
      return home !== undefined;
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
// returns to the entry of the page still shown. The fourth value tells whether that return is under
// way, when an in-app move does nothing.
export function usePath(guard?: Ask): [string, (to: string) => void, string, () => boolean] {
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
  const returning = useCallback(() => ours().returning(), []);
  return [path, go, query, returning];
}

// A plain left click navigates in the page; any other keeps the browser's own handling.
export function follow(e: MouseEvent, go: (to: string) => void, to: string): void {
  if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
  e.preventDefault();
  go(to);
}
