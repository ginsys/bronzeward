import { describe, expect, test } from "vitest";
import { positions } from "./router.ts";

// A session history whose traversals are relative and queued, as a browser's are: each one runs
// after those before it, from the entry it then finds, and fires popstate. Timers run only once
// every queued traversal ran, as after QUIET_MS of a still history.
function session(urls: string[]) {
  const list: { url: string; state: unknown }[] = [{ url: urls[0]!, state: null }];
  let i = 0;
  let pop = () => {};
  const queued: number[] = [];
  const timers: (() => void)[] = [];
  const h = {
    get state() {
      return list[i]!.state;
    },
    go(delta: number) {
      queued.push(delta);
    },
    pushState(state: unknown, _: string, url?: string) {
      list.splice(i + 1, list.length, { url: url ?? list[i]!.url, state });
      i += 1;
    },
    replaceState(state: unknown, _: string, url?: string) {
      list[i]!.state = state;
      if (url !== undefined) list[i]!.url = url;
    },
  };
  // A fragment link (Skip to the page): an entry without state after the current one, and popstate.
  const fragment = () => {
    list.splice(i + 1, list.length, { url: list[i]!.url, state: null });
    i += 1;
    pop();
  };
  // With `timers` false, only the queued traversals run, as within QUIET_MS.
  const run = (withTimers = true) => {
    for (;;) {
      const delta = queued.shift();
      if (delta !== undefined) {
        if (list[i + delta] === undefined) continue;
        i += delta;
        pop();
        continue;
      }
      if (!withTimers) return;
      const t = timers.shift();
      if (t === undefined) return;
      t();
    }
  };
  const p = positions(h, () => list[i]!.url, (f) => void timers.push(f));
  for (const u of urls.slice(1)) p.moved(u);
  return { p, h, run, fragment, urls: () => list.map((e) => e.url), at: () => i, onPop: (f: () => void) => (pop = f) };
}

const asking = (s: ReturnType<typeof session>, answers: boolean[]) => {
  const asked: [string, string][] = [];
  const followed: string[] = [];
  s.onPop(() => {
    if (s.p.popped((to, from) => (asked.push([to, from]), answers.shift() ?? false))) followed.push(s.urls()[s.at()]!);
  });
  return { asked, followed };
};

describe("positions", () => {
  test("a refused Back returns to the shown entry and keeps the Forward entries", () => {
    const s = session(["/machines", "/changes", "/plans"]);
    const { asked, followed } = asking(s, [false, true]);
    s.h.go(-2);
    s.run();
    expect(asked).toEqual([["/machines", "/plans"]]);
    expect(s.urls()).toEqual(["/machines", "/changes", "/plans"]);
    expect(s.at()).toBe(2);
    // The next Back is asked again from the page still shown.
    s.h.go(-1);
    s.run();
    expect(asked).toEqual([["/machines", "/plans"], ["/changes", "/plans"]]);
    expect(followed).toEqual(["/changes"]);
  });

  test("a refused Forward returns to the shown entry, so the next Back does not open the refused page", () => {
    const s = session(["/machines", "/changes"]);
    const { asked, followed } = asking(s, [true, false]);
    s.h.go(-1);
    s.run();
    s.h.go(1);
    s.run();
    expect(s.urls()).toEqual(["/machines", "/changes"]);
    expect(s.at()).toBe(0);
    expect(asked).toEqual([["/machines", "/changes"], ["/changes", "/machines"]]);
    expect(followed).toEqual(["/machines"]);
  });

  test("a second Back queued while the question was open asks nothing more, and the history returns once", () => {
    const s = session(["/machines", "/changes", "/clusters/new", "/plans", "/sources"]);
    s.onPop(() => void s.p.popped());
    s.h.go(-2);
    s.run();
    const { asked } = asking(s, []);
    s.h.go(-1);
    s.h.go(-1);
    s.run();
    expect(asked).toEqual([["/changes", "/clusters/new"]]);
    expect(s.at()).toBe(2);
    expect(s.urls()).toHaveLength(5);
  });

  test("a second Forward queued while the question was open asks nothing more, and the history returns once", () => {
    const s = session(["/machines", "/changes", "/clusters/new", "/plans", "/sources"]);
    s.onPop(() => void s.p.popped());
    s.h.go(-4);
    s.run();
    const { asked } = asking(s, []);
    s.h.go(1);
    s.h.go(1);
    s.run();
    expect(asked).toEqual([["/changes", "/machines"]]);
    expect(s.at()).toBe(0);
    expect(s.urls()).toHaveLength(5);
  });

  test("an in-app move while a refused move returns does nothing, and the numbering stays whole", () => {
    const s = session(["/machines", "/changes", "/clusters/new"]);
    const { asked } = asking(s, [false, false]);
    s.h.go(-1);
    s.run(false);
    // The address is /changes, the page still shows /clusters/new: a move to either does nothing.
    expect(s.p.moved("/plans")).toBe(false);
    expect(s.p.moved("/changes")).toBe(false);
    s.run();
    expect(s.urls()).toEqual(["/machines", "/changes", "/clusters/new"]);
    expect(s.at()).toBe(2);
    // Once returned, a move adds its entry, and a refused Back from it returns to it.
    expect(s.p.moved("/plans")).toBe(true);
    s.h.go(-1);
    s.run();
    expect(s.urls()).toEqual(["/machines", "/changes", "/clusters/new", "/plans"]);
    expect(s.at()).toBe(3);
    expect(asked).toEqual([["/changes", "/clusters/new"], ["/clusters/new", "/plans"]]);
  });

  test("an entry a fragment link added is numbered after the entry it left, and a refused Back to it returns", () => {
    const s = session(["/machines"]);
    s.onPop(() => void s.p.popped());
    s.fragment();
    expect(s.p.moved("/changes")).toBe(true);
    const { asked } = asking(s, []);
    s.h.go(-1);
    s.run();
    expect(asked).toEqual([["/machines", "/changes"]]);
    expect(s.urls()).toEqual(["/machines", "/machines", "/changes"]);
    expect(s.at()).toBe(2);
    expect(s.h.state).toEqual({ at: 2 });
  });

  test("a fragment link during a return, which drops the shown page's entry, ends the return at the shown page's address", () => {
    const s = session(["/machines", "/changes", "/clusters/new"]);
    const { asked } = asking(s, [false, false]);
    s.h.go(-1);
    s.run(false);
    // Skip to the page on /changes: the fragment's entry replaces /clusters/new at its position.
    s.fragment();
    s.run();
    expect(s.urls()).toEqual(["/machines", "/changes", "/clusters/new"]);
    expect(s.at()).toBe(2);
    // The numbering stays whole: a move, then a refused Back by two, returns to the move's entry.
    expect(s.p.moved("/plans")).toBe(true);
    s.h.go(-2);
    s.run();
    expect(s.at()).toBe(3);
    expect(s.urls()[3]).toBe("/plans");
    expect(asked).toEqual([["/changes", "/clusters/new"], ["/changes", "/plans"]]);
  });

  test("a return whose traversal fires nothing ends at the shown page's address, and in-app moves work again", () => {
    const s = session(["/machines", "/changes", "/clusters/new"]);
    asking(s, []);
    s.h.go(-1);
    s.run(false);
    s.h.go = () => {};
    s.run();
    expect(s.urls()).toEqual(["/machines", "/clusters/new", "/clusters/new"]);
    expect(s.at()).toBe(1);
    expect(s.p.moved("/plans")).toBe(true);
    expect(s.urls()).toEqual(["/machines", "/clusters/new", "/plans"]);
    expect(s.h.state).toEqual({ at: 2 });
  });
});
