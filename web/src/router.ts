// Path routes over the History API (decision B3 of ginsys/bronzeward#255): no router library. The
// static host answers every path outside /api/v1/ and /livez with the application.

import { type MouseEvent, useCallback, useEffect, useState } from "react";

// The navigation of the header wireframe; Recovery is the recovery console's (#261).
export const PAGES = [
  ["/machines", "Machines"],
  ["/changes", "Changes"],
  ["/plans", "Plans"],
  ["/operations", "Operations"],
  ["/sources", "Sources"],
  ["/audit", "Audit"],
] as const;

// The current path, its query, and a navigation that adds a history entry. The query is tracked
// apart from the path, so a query-only move (Back from ?cluster=c2 to ?cluster=c1) is seen too.
export function usePath(): [string, (to: string) => void, string] {
  const [path, setPath] = useState(location.pathname);
  const [query, setQuery] = useState(location.search);
  const here = useCallback(() => {
    setPath(location.pathname);
    setQuery(location.search);
  }, []);
  useEffect(() => {
    addEventListener("popstate", here);
    return () => removeEventListener("popstate", here);
  }, [here]);
  const go = useCallback((to: string) => {
    if (to !== location.pathname + location.search) history.pushState(null, "", to);
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
