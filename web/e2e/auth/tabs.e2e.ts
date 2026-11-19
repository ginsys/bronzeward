// AUTH-07 and AUTH-11 (conformance/cases/): several tabs of the fixture page, one renewing (B1),
// against a synthetic provider that rotates refresh tokens and counts every use.
import { $, browser, expect } from "@wdio/globals";
import { PRINCIPAL_CHANGED } from "../../src/app.tsx";
import { FIXTURE_PAGE } from "../auth.conf.ts";
import type { Counts } from "../provider.ts";
import { absent, counts, fixtureSignIn, newTab, settled, storageEmpty, where } from "./helpers.ts";

const SERVER = /^Server .+, API revision \d+$/;

// The first tab signed in, in a tab of its own: after a close WebdriverIO switches to the first
// window, which stays idle. The handle is read once the tab has settled: Safari's handle names a page,
// and a navigation to another origin (the sign-in's redirects) gives the tab another one.
async function signInTab(flags: Record<string, string>): Promise<string> {
  await newTab("about:blank");
  expect(await fixtureSignIn("h-viewer", flags)).toMatch(SERVER);
  return browser.getWindowHandle();
}

// Another tab of the page: it takes the open tabs' pair, or signs in when they have none to give.
async function openTab(): Promise<string> {
  await newTab(`${FIXTURE_PAGE}/`);
  expect(await settled(FIXTURE_PAGE)).toMatch(SERVER);
  return browser.getWindowHandle();
}

async function closeTab(handle: string, then: string) {
  await browser.switchToWindow(handle);
  await browser.closeWindow();
  await browser.switchToWindow(then);
}

const reach = (k: keyof Counts, n: number) =>
  browser.waitUntil(async () => (await counts())[k] >= n, { timeout: 30_000, timeoutMsg: `the provider did not see ${n} ${k}` });

describe("several tabs", () => {
  it("AUTH-07: a renewal for another subject is shown in every tab, and their data discarded", async function () {
    this.timeout(120_000);
    // expires_in 24 s: the renewal comes 12 s after the sign-in, once the second tab is open.
    const first = await signInTab({ rotate: "1", lifetime: "24", "renew-as": "h-author" });
    const second = await openTab();
    for (const tab of [first, second]) {
      await browser.switchToWindow(tab);
      await expect($('[data-testid="status"]')).toHaveText(PRINCIPAL_CHANGED, { wait: 20_000 });
      await absent(() => $('[data-testid="principal"]').isExisting(), "the principal after the renewal for another subject");
      // Nothing read under the old subject stays: no principal, no server version (§7 item 5).
      expect(await $("body").getText()).not.toMatch(/h-viewer|API revision/);
      expect(await $('[data-testid="server"]').getText()).toBe("Server unknown");
    }
    expect(await counts()).toMatchObject({ authorize: 1, refresh: 1, spent: 0 });
  });

  it("AUTH-11: three tabs at the threshold send one refresh request per renewal, every tab on each pair, no redirect", async function () {
    this.timeout(120_000);
    // expires_in 20 s: each pair is renewed 10 s after it was issued.
    const holder = await signInTab({ rotate: "1", lifetime: "20" });
    const next = await openTab();
    const last = await openTab();
    await reach("refresh", 1);
    await browser.pause(2000);
    expect(await counts()).toEqual({ authorize: 1, refresh: 1, spent: 0, revoke: 0 });
    // Each tab that takes the lock renews with the latest refresh token, so it had each pair.
    await closeTab(holder, next);
    await reach("refresh", 2);
    await closeTab(next, last);
    await reach("refresh", 3);
    expect(await counts()).toMatchObject({ authorize: 1, refresh: 3, spent: 0 });
  });

  it("AUTH-11: the holder closes after posting renewing; the spent token is never sent, and a new sign-in reaches every tab", async function () {
    this.timeout(120_000);
    // The provider takes the first refresh request and never answers it.
    const holder = await signInTab({ rotate: "1", lifetime: "20", hold: "1" });
    const next = await openTab();
    const last = await openTab();
    // Every tab took the first pair before its renewal began.
    expect(await counts()).toMatchObject({ authorize: 1, refresh: 0 });
    await reach("refresh", 1);
    await closeTab(holder, next);
    // The waiting tab gives up after 10 s (§9.5 item 3); the tabs keep the current token.
    await browser.pause(12_000);
    expect(await counts()).toMatchObject({ authorize: 1, refresh: 1, spent: 0 });
    expect(await settled(FIXTURE_PAGE)).toMatch(SERVER);
    // A new tab gets no pair whose refresh token may be spent, waits 5 s and signs in again.
    await openTab();
    expect((await counts()).authorize).toBe(2);
    // The redirect's pair reached the holder, which renews with it, and then the next tab.
    await reach("refresh", 2);
    await closeTab(next, last);
    await reach("refresh", 3);
    expect(await counts()).toMatchObject({ authorize: 2, refresh: 3, spent: 0 });
  });

  it("AUTH-11: a refresh request never answered fails after 15 s, and the holder renews with the next pair", async function () {
    this.timeout(120_000);
    const holder = await signInTab({ rotate: "1", lifetime: "20", hold: "1" });
    await reach("refresh", 1);
    // Past the 15 s bound (#388), the holder's renewal has failed and holds back nothing.
    await browser.pause(16_000);
    // A new tab gets no pair whose refresh token was sent, waits 5 s and signs in again; the holder
    // takes its pair and renews with it at that pair's threshold.
    await openTab();
    await browser.switchToWindow(holder);
    await reach("refresh", 2);
    expect(await counts()).toMatchObject({ authorize: 2, refresh: 2, spent: 0 });
    expect(await settled(FIXTURE_PAGE)).toMatch(SERVER);
  });

  it("AUTH-11: without a refresh token, two tabs at the threshold renew by one redirect, the holder's", async function () {
    this.timeout(120_000);
    // expires_in 20 s and no refresh token: each pair is renewed by the redirect 10 s after it was
    // issued. The other tab, granted the lock when the holder leaves, waits for the redirect's pair.
    await signInTab({ lifetime: "20", bare: "1" });
    await openTab();
    await reach("authorize", 2);
    await browser.pause(3000);
    expect(await counts()).toMatchObject({ authorize: 2, refresh: 0 });
    // The tab that waited holds the lock now and redirects in turn.
    await reach("authorize", 3);
    await browser.pause(3000);
    expect(await counts()).toMatchObject({ authorize: 3, refresh: 0, spent: 0 });
  });
});

// Only the idle first tab left, so the next case starts with no holder. The tabs of the page are the
// ones to close, found by their address: Safari neither lists its windows in the order it opened them nor
// keeps a handle across a navigation to another origin.
afterEach(async () => {
  for (const h of await browser.getWindowHandles()) {
    await browser.switchToWindow(h);
    if (!(await where()).startsWith(FIXTURE_PAGE)) continue;
    // A tab in the middle of a redirect holds its sign-in state in session storage until it settles.
    await browser.waitUntil(() => storageEmpty().then(() => true, () => false), { timeout: 15_000, interval: 500 }).catch(() => undefined);
    await storageEmpty();
    // The last window cannot be closed (the session would end): a case may have left it on the page.
    if ((await browser.getWindowHandles()).length === 1) await browser.url("about:blank");
    else await browser.closeWindow();
  }
  const [idle, ...more] = await browser.getWindowHandles();
  if (idle === undefined || more.length > 0) throw new Error(`${more.length + (idle === undefined ? 0 : 1)} windows are open, one expected`);
  await browser.switchToWindow(idle);
});
