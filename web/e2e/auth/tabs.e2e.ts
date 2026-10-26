// AUTH-07 and AUTH-11 (conformance/cases/): several tabs of the fixture page, one renewing (B1),
// against a synthetic provider that rotates refresh tokens and counts every use.
import { $, browser, expect } from "@wdio/globals";
import { PRINCIPAL_CHANGED } from "../../src/app.tsx";
import { FIXTURE_PAGE } from "../auth.conf.ts";
import type { Counts } from "../provider.ts";
import { counts, fixtureSignIn, settled, storageEmpty, where } from "./helpers.ts";

const SERVER = /^Server .+, API revision \d+$/;

// The first tab signed in, in a tab of its own: after a close WebdriverIO switches to the first
// window, which stays idle.
async function signInTab(flags: Record<string, string>): Promise<string> {
  const handle = await newTab("about:blank");
  expect(await fixtureSignIn("h-viewer", flags)).toMatch(SERVER);
  return handle;
}

// Another tab of the page: it takes the open tabs' pair, or signs in when they have none to give.
async function openTab(): Promise<string> {
  const handle = await newTab(`${FIXTURE_PAGE}/`);
  expect(await settled(FIXTURE_PAGE)).toMatch(SERVER);
  return handle;
}

// Over WebDriver BiDi newWindow does not switch to the tab it opens.
async function newTab(url: string): Promise<string> {
  const w = await browser.newWindow(url);
  const handle = "handle" in w ? w.handle : w.contextId;
  await browser.switchToWindow(handle);
  return handle;
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
      expect(await $('[data-testid="principal"]').isExisting()).toBe(false);
      expect(await $("body").getText()).not.toMatch(/h-viewer|Server /);
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
});

// Only the idle first tab left, so the next case starts with no holder.
afterEach(async () => {
  const [keep, ...rest] = await browser.getWindowHandles();
  for (const h of rest) {
    await browser.switchToWindow(h);
    if ((await where()).startsWith(FIXTURE_PAGE)) await storageEmpty();
    await closeTab(h, keep!);
  }
});
