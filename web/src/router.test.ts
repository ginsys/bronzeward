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
  // With `timers` false, only the queued traversals run, as within QUIET_MS. With `steps`, only that
  // many run, and the rest stay queued.
  const run = (withTimers = true, steps = Infinity) => {
    for (;;) {
      if (steps === 0) return;
      const delta = queued.shift();
      if (delta !== undefined) {
        if (list[i + delta] === undefined) continue;
        i += delta;
        steps -= 1;
        pop();
        continue;
      }
      if (!withTimers) return;
      const t = timers.shift();
      if (t === undefined) return;
      t();
    }
  };
  // Fires the next timer only, whatever traversals are still queued, as a slow traversal outlasts it.
  const fire = () => timers.shift()?.();
  // Fires every timer pending, the stale ones (a later traversal restarted the wait) doing nothing.
  const drain = () => {
    while (timers.length > 0) timers.shift()!();
  };
  const p = positions(h, () => list[i]!.url, (f) => void timers.push(f));
  for (const u of urls.slice(1)) p.moved(u);
  return { p, h, run, fire, drain, fragment, urls: () => list.map((e) => e.url), at: () => i, onPop: (f: () => void) => (pop = f) };
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

  test("a return is reported until it ends, so the shell asks nothing for a move that would do nothing", () => {
    const s = session(["/machines", "/changes", "/clusters/new"]);
    asking(s, [false]);
    expect(s.p.returning()).toBe(false);
    s.h.go(-1);
    s.run(false);
    expect(s.p.returning()).toBe(true);
    s.run();
    expect(s.p.returning()).toBe(false);
  });

  test("a return's traversal that lands after the fallback moves no page and asks nothing", () => {
    const s = session(["/machines", "/changes", "/clusters/new"]);
    const { asked, followed } = asking(s, [false, true]);
    s.h.go(-1);
    s.run(false);
    // Both timers fire before the return's traversal runs: the first aims it, the second ends the return.
    s.fire();
    s.fire();
    expect(s.p.returning()).toBe(false);
    s.run();
    expect(asked).toEqual([["/changes", "/clusters/new"]]);
    expect(followed).toEqual([]);
    // The fallback made entry 1 the shown page's; the late traversal went to entry 2 and the history
    // returned from it.
    expect(s.urls()).toEqual(["/machines", "/clusters/new", "/clusters/new"]);
    expect(s.at()).toBe(1);
    expect(s.h.state).toEqual({ at: 1 });
    expect(s.p.returning()).toBe(false);
  });

  test("a late traversal behind an in-app move moves no page, and the history returns to the page that move opened", () => {
    const s = session(["/machines", "/changes"]);
    const { asked, followed } = asking(s, [true, false, true]);
    s.h.go(-1);
    s.run();
    s.h.go(1);
    s.run(false);
    s.fire();
    s.fire();
    // The operator clicks in the gap, then the slow traversal lands one entry back.
    expect(s.p.moved("/plans")).toBe(true);
    s.run();
    expect(asked).toEqual([["/machines", "/changes"], ["/changes", "/machines"]]);
    expect(followed).toEqual(["/machines"]);
    expect(s.urls()).toEqual(["/machines", "/machines", "/plans"]);
    expect(s.at()).toBe(2);
    expect(s.h.state).toEqual({ at: 2 });
  });

  test("a fragment link while the late traversal is still queued does not clear its marker", () => {
    const s = session(["/machines", "/changes", "/plans"]);
    const { asked, followed } = asking(s, [true, false, true]);
    s.h.go(-2);
    s.run();
    s.h.go(2);
    s.run(false);
    s.fire();
    s.fire();
    // Skip to the page (same path, so the guard lets it by), then the slow traversal lands two entries
    // back from the fragment's entry.
    s.fragment();
    s.run();
    expect(asked).toEqual([["/machines", "/plans"], ["/plans", "/machines"], ["/machines", "/machines"]]);
    expect(followed).toEqual(["/machines", "/machines"]);
    expect(s.p.returning()).toBe(false);
    expect(s.at()).toBe(3);
    expect(s.urls()[3]).toBe("/machines");
  });

  test("the marker expires after two quiet periods, so a later move of the same distance asks as usual", () => {
    const s = session(["/machines", "/changes", "/sources"]);
    const { asked, followed } = asking(s, [true, false, true]);
    s.h.go(-2);
    s.run();
    // Two Forwards: the first is refused, and the aim is queued behind the second.
    s.h.go(1);
    s.h.go(1);
    s.run(false, 1);
    s.fire();
    s.run(false);
    s.drain();
    expect(s.p.returning()).toBe(false);
    expect(s.at()).toBe(1);
    s.h.go(-1);
    s.run();
    expect(asked).toHaveLength(3);
    expect(followed).toEqual(["/machines", "/machines"]);
  });

  test("a move that is not the late traversal leaves the marker, so the traversal still to come is absorbed", () => {
    const s = session(["/machines", "/changes", "/clusters/new", "/plans"]);
    const { asked, followed } = asking(s, [false, true]);
    // A second Back is queued behind the refused one, and the aim behind both.
    s.h.go(-1);
    s.h.go(-1);
    s.run(false, 1);
    // Both timers fire before either queued traversal runs: the fallback arms the marker.
    s.fire();
    s.fire();
    s.run();
    // The second Back is the operator's own, asked and followed. The aim lands after it and is absorbed.
    expect(asked).toEqual([["/clusters/new", "/plans"], ["/changes", "/plans"]]);
    expect(followed).toEqual(["/changes"]);
    expect(s.p.returning()).toBe(false);
    expect(s.at()).toBe(1);
  });

  test("the return that absorbed a late traversal arms no marker, so a second slow traversal is asked, not chained", () => {
    const s = session(["/machines", "/changes", "/plans"]);
    const { asked } = asking(s, [false, false]);
    s.h.go(-1);
    s.run(false);
    // The refusal's traversal lands after both timers and is absorbed: no second question.
    s.fire();
    s.fire();
    s.run(false);
    expect(asked).toEqual([["/changes", "/plans"]]);
    // The return that absorbed it is slow too. Its late traversal is an ordinary move to the guard.
    s.drain();
    s.run(false);
    expect(asked).toHaveLength(2);
  });

  test("a Back after a late traversal was absorbed asks as usual", () => {
    const s = session(["/machines", "/changes", "/clusters/new"]);
    const { asked, followed } = asking(s, [false, true, true]);
    s.h.go(-1);
    s.run(false);
    s.fire();
    s.fire();
    s.run();
    s.h.go(-1);
    s.run();
    expect(asked).toEqual([["/changes", "/clusters/new"], ["/machines", "/clusters/new"]]);
    expect(followed).toEqual(["/machines"]);
  });
});
