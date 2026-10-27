import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { navigate, PageState, type Phase, renewals } from "./page.ts";

beforeEach(() => {
  vi.useFakeTimers();
});
afterEach(() => {
  vi.useRealTimers();
});

describe("the page-level state", () => {
  test.each([
    ["starting", "signed-in", true],
    ["signed-in", "refused", true],
    ["signed-in", "changed", true],
    ["refused", "signed-in", false],
    ["changed", "signed-in", false],
    ["refused", "ended", true],
    ["ended", "starting", false],
    ["ended", "signed-in", false],
  ] as const)("%s to %s: %s", (from, to, moved) => {
    const page = at(from);
    const still = page.mark();
    expect(page.to(to)).toBe(moved);
    expect(page.phase).toBe(moved ? to : from);
    expect(still()).toBe(!moved);
  });
});

// The page reached `phase` through the table's moves.
function at(phase: Phase): PageState {
  const page = new PageState();
  const path: Record<Phase, Phase[]> = { starting: [], "signed-in": ["signed-in"], refused: ["signed-in", "refused"], changed: ["signed-in", "changed"], ended: ["ended"] };
  for (const p of path[phase]) page.to(p);
  return page;
}

// A renewal timer over a session `byRedirect` or not, in a tab that holds the lock or not; the
// renewal and the redirect are held until the test releases them.
function harness(opts: { byRedirect?: boolean; holder?: boolean } = {}) {
  const page = at("signed-in");
  const s = { expiresAt: Date.now() + 120_000, byRedirect: opts.byRedirect ?? false };
  let renewed: (ok: boolean) => void = () => {};
  let digested: (url: string) => void = () => {};
  const renew = vi.fn(() => new Promise<boolean>((r) => (renewed = r)));
  const assign = vi.fn();
  const redirect = vi.fn((still: () => boolean) => navigate(new Promise<string>((r) => (digested = r)), still, assign));
  const t = renewals(page, { session: () => s, renew, holder: () => opts.holder ?? true, redirect });
  t.schedule();
  return { page, s, t, renew, redirect, assign, renewed: (ok: boolean) => renewed(ok), digested: (u: string) => digested(u) };
}

describe("§9.5 renewals", () => {
  test("item 1: a successful renewal schedules the next one", async () => {
    const h = harness();
    await vi.advanceTimersByTimeAsync(60_000);
    expect(h.renew).toHaveBeenCalledTimes(1);
    h.s.expiresAt = Date.now() + 120_000;
    h.renewed(true);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(h.renew).toHaveBeenCalledTimes(2);
  });

  // #388: the holder renewed for another tab, whose request was due first.
  test("a timer whose pair was renewed meanwhile waits for the new threshold", async () => {
    const h = harness();
    await vi.advanceTimersByTimeAsync(30_000);
    h.s.expiresAt += 300_000;
    await vi.advanceTimersByTimeAsync(30_000);
    expect(h.renew).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(300_000);
    expect(h.renew).toHaveBeenCalledTimes(1);
  });

  // R7-F1: a refusal while a renewal is in flight; the renewal then succeeds.
  test.each([
    ["the timer's", () => void vi.advanceTimersByTime(60_000)],
    ["a retried request's", (t: ReturnType<typeof renewals>) => void t.renew()],
  ] as const)("R7-F1: a refusal stops %s renewal still in flight", async (_name, start) => {
    const h = harness();
    start(h.t);
    expect(h.renew).toHaveBeenCalledTimes(1);
    h.page.to("refused");
    h.s.expiresAt = Date.now() + 120_000;
    h.renewed(true);
    await vi.runAllTimersAsync();
    expect(h.renew).toHaveBeenCalledTimes(1);
    expect(h.redirect).not.toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
  });

  test("a retried request's renewal answers false once the page has moved", async () => {
    const h = harness();
    const answer = h.t.renew();
    h.page.to("changed");
    h.renewed(true);
    expect(await answer).toBe(false);
  });

  // R6-F1: the timer is left armed (no stop()); the move alone keeps it from renewing.
  test.each(["refused", "changed", "ended"] as const)("R6-F1: after the page moves to %s, the timer neither renews nor redirects", async (phase) => {
    for (const byRedirect of [false, true]) {
      const h = harness({ byRedirect });
      h.page.to(phase);
      await vi.advanceTimersByTimeAsync(60_000);
      expect(h.renew).not.toHaveBeenCalled();
      expect(h.redirect).not.toHaveBeenCalled();
    }
  });

  test("§9.3 item 3: without a rotating refresh token the holder renews by the redirect once its renewal answers false", async () => {
    const h = harness({ byRedirect: true });
    await vi.advanceTimersByTimeAsync(60_000);
    expect(h.redirect).not.toHaveBeenCalled();
    h.renewed(false);
    await vi.advanceTimersByTimeAsync(0);
    h.digested("https://idp.example.invalid/authorize?x");
    await vi.runAllTimersAsync();
    expect(h.assign).toHaveBeenCalledWith("https://idp.example.invalid/authorize?x");
  });

  test("§9.3 item 3: a tab without the lock asks the holder, and never redirects itself", async () => {
    const h = harness({ byRedirect: true, holder: false });
    await vi.advanceTimersByTimeAsync(60_000);
    expect(h.renew).toHaveBeenCalledTimes(1);
    expect(h.redirect).not.toHaveBeenCalled();
  });

  // A tab that asked the holder is granted the lock (the holder closed): its renewal answers false,
  // since it has no refresh token to send, and it renews by the redirect; a failed renewal does not.
  test.each([
    [true, 1],
    [false, 0],
  ] as const)("§9.3 item 3: granted the lock while it waits, a tab with byRedirect %s redirects %i times", async (byRedirect, times) => {
    const opts = { byRedirect, holder: false };
    const h = harness(opts);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(h.renew).toHaveBeenCalledTimes(1);
    opts.holder = true;
    h.renewed(false);
    await vi.advanceTimersByTimeAsync(0);
    expect(h.redirect).toHaveBeenCalledTimes(times);
  });

  // R8-F1: the redirect renewal awaits the PKCE digest when the page moves.
  test.each([
    ["a final 401", "refused"],
    ["another account", "changed"],
    ["a sign-out, another tab's sign-out or pagehide", "ended"],
  ] as const)("R8-F1: after %s during the PKCE digest, the renewal does not navigate", async (_name, phase) => {
    const h = harness({ byRedirect: true });
    await vi.advanceTimersByTimeAsync(60_000);
    h.renewed(false);
    await vi.advanceTimersByTimeAsync(0);
    expect(h.redirect).toHaveBeenCalledTimes(1);
    h.page.to(phase);
    h.digested("https://idp.example.invalid/authorize?x");
    await vi.runAllTimersAsync();
    expect(h.assign).not.toHaveBeenCalled();
  });
});

// CP2-F1: a first tab's sign-in and the sign-in button await the PKCE digest too.
describe("navigation after the PKCE digest", () => {
  test.each([
    ["a first tab's sign-in", "starting"],
    ["the button on the W1 page", "refused"],
    ["the button after another account's renewal", "changed"],
  ] as const)("CP2-F1: %s does not navigate once the page has ended", async (_name, phase) => {
    const page = at(phase);
    const assign = vi.fn();
    let digested: (url: string) => void = () => {};
    const going = navigate(new Promise<string>((r) => (digested = r)), page.mark(), assign);
    page.to("ended");
    digested("https://idp.example.invalid/authorize?x");
    expect(await going).toBe(false);
    expect(assign).not.toHaveBeenCalled();
  });

  test("with no move, it navigates", async () => {
    const page = at("refused");
    const assign = vi.fn();
    expect(await navigate(Promise.resolve("https://idp.example.invalid/authorize?x"), page.mark(), assign)).toBe(true);
    expect(assign).toHaveBeenCalledWith("https://idp.example.invalid/authorize?x");
  });
});
