import { describe, expect, test } from "vitest";
import { positions } from "./router.ts";

// A session history: its entries, the current one, and the popstate a traversal fires.
function session(first: string) {
  const list: { url: string; state: unknown }[] = [{ url: first, state: null }];
  let i = 0;
  let pop = () => {};
  const h = {
    get state() {
      return list[i]!.state;
    },
    go(delta: number) {
      i += delta;
      pop();
    },
    pushState(state: unknown, _: string, url?: string) {
      list.splice(i + 1, list.length, { url: url ?? list[i]!.url, state });
      i += 1;
    },
    replaceState(state: unknown) {
      list[i]!.state = state;
    },
  };
  return { h, here: () => list[i]!.url, urls: () => list.map((e) => e.url), at: () => i, onPop: (f: () => void) => (pop = f) };
}

describe("positions", () => {
  test("a refused Back returns to the shown entry and keeps the Forward entries", () => {
    const s = session("/machines");
    const p = positions(s.h, s.here);
    p.pushed("/changes");
    p.pushed("/plans");
    const asked: [string, string][] = [];
    let refuse = true;
    const ask = (to: string, from: string) => (asked.push([to, from]), !refuse);
    const followed: boolean[] = [];
    s.onPop(() => followed.push(p.popped(ask)));
    s.h.go(-2);
    expect(asked).toEqual([["/machines", "/plans"]]);
    expect(followed).toEqual([false, false]);
    expect(s.urls()).toEqual(["/machines", "/changes", "/plans"]);
    expect(s.here()).toBe("/plans");
    // The next Back is asked again from the page still shown.
    refuse = false;
    s.h.go(-1);
    expect(asked).toEqual([["/machines", "/plans"], ["/changes", "/plans"]]);
    expect(s.here()).toBe("/changes");
  });

  test("a refused Forward returns to the shown entry, so the next Back does not open the refused page", () => {
    const s = session("/machines");
    const p = positions(s.h, s.here);
    p.pushed("/changes");
    let refuse = false;
    const asked: [string, string][] = [];
    s.onPop(() => p.popped((to, from) => (asked.push([to, from]), !refuse)));
    s.h.go(-1);
    expect(s.here()).toBe("/machines");
    refuse = true;
    s.h.go(1);
    expect(s.here()).toBe("/machines");
    expect(s.urls()).toEqual(["/machines", "/changes"]);
    expect(s.at()).toBe(0);
    expect(asked).toEqual([["/machines", "/changes"], ["/changes", "/machines"]]);
  });

  test("a refused move to an entry the page did not add leaves it under a new entry", () => {
    const s = session("/machines");
    const p = positions(s.h, s.here);
    s.h.pushState(null, "", "/elsewhere");
    p.pushed("/changes");
    s.onPop(() => p.popped(() => false));
    // Back lands on the unnumbered /elsewhere; refused, /changes is shown under a new entry.
    s.h.go(-1);
    expect(s.urls()).toEqual(["/machines", "/elsewhere", "/changes"]);
    expect(s.at()).toBe(2);
  });
});
