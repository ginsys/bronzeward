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

// The current path, and a navigation that adds a history entry.
export function usePath(): [string, (to: string) => void] {
  const [path, setPath] = useState(location.pathname);
  useEffect(() => {
    const on = () => setPath(location.pathname);
    addEventListener("popstate", on);
    return () => removeEventListener("popstate", on);
  }, []);
  const go = useCallback((to: string) => {
    if (to !== location.pathname + location.search) history.pushState(null, "", to);
    setPath(location.pathname);
  }, []);
  return [path, go];
}

// A plain left click navigates in the page; any other keeps the browser's own handling.
export function follow(e: MouseEvent, go: (to: string) => void, to: string): void {
  if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
  e.preventDefault();
  go(to);
}
