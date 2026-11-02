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
    replaceState(state: unknown) {
      list[i]!.state = state;
    },
  };
  const run = () => {
    for (;;) {
      const delta = queued.shift();
      if (delta !== undefined) {
        if (list[i + delta] === undefined) continue;
        i += delta;
        pop();
        continue;
      }
      const t = timers.shift();
      if (t === undefined) return;
      t();
    }
  };
  const p = positions(h, () => list[i]!.url, (f) => void timers.push(f));
  for (const u of urls.slice(1)) p.pushed(u);
  return { p, h, run, urls: () => list.map((e) => e.url), at: () => i, onPop: (f: () => void) => (pop = f) };
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

  test("a refused move to an entry the page did not add leaves it under a new entry", () => {
    const s = session(["/machines"]);
    s.h.pushState(null, "", "/elsewhere");
    s.p.pushed("/changes");
    asking(s, []);
    // Back lands on the unnumbered /elsewhere; refused, /changes is shown under a new entry.
    s.h.go(-1);
    s.run();
    expect(s.urls()).toEqual(["/machines", "/elsewhere", "/changes"]);
    expect(s.at()).toBe(2);
  });
});
