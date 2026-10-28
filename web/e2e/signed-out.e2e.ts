// #388: the signed-out page must release its renewer, since before it sends a refresh token the
// renewer holder waits, up to ACK_WAIT_MS, for an ack from every other held tab lock (renewer.ts).
// This checks that the page holds and waits for no renewer lock.
import { $, browser, expect } from "@wdio/globals";
import { SIGNED_OUT } from "../src/app.tsx";
import { LOCK, TAB } from "../src/renewer.ts";

// The page's Web Locks whose names are the renewer's, held and waited for.
const ours = () =>
  browser.execute(
    async (names: string[]) => {
      const { held = [], pending = [] } = await navigator.locks.query();
      const mine = (ls: LockInfo[]) => ls.map((l) => l.name ?? "").filter((n) => names.some((p) => n.startsWith(p)));
      return { held: mine(held), pending: mine(pending) };
    },
    [LOCK, TAB],
  );

describe("the signed-out page", () => {
  // Positive control: the page at / holds a tab lock once settled, so the query can see a held one.
  // It does not show how the page settled.
  it("the page at / holds a tab lock once settled", async () => {
    await browser.url("/");
    await browser.waitUntil(async () => {
      const [at, status] = await browser.execute(() => [
        location.pathname,
        document.querySelector('[data-testid="status"]')?.textContent ?? "",
      ]);
      return at === "/" && status !== "" && !status.startsWith("Loading");
    }, { timeout: 20_000, timeoutMsg: "the page did not settle on /" });
    expect((await ours()).held.some((n) => n.startsWith(TAB))).toBe(true);
  });

  it("#388: holds no renewer lock, held or waited for", async () => {
    await browser.url("/signed-out");
    await expect($('[data-testid="status"]')).toHaveText(SIGNED_OUT);
    // With the release() call deleted, this times out: the tab lock requested on load stays held.
    await browser.waitUntil(async () => {
      const { held, pending } = await ours();
      return held.length === 0 && pending.length === 0;
    }, { timeout: 5_000, timeoutMsg: "the signed-out page holds or waits for a renewer lock" });
    // Checked again a second later: none held or pending then either.
    await browser.pause(1_000);
    expect(await ours()).toEqual({ held: [], pending: [] });
  });
});
