// #388: the signed-out page releases its renewer, so it holds no tab lock and is not one of the tabs
// the renewer holder waits on before it sends a refresh token (renewer.ts).
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
  // Positive control: any other page holds its tab lock once settled (here the page signs in, then
  // the shell's first read fails: every API request is redirected), so the query below can see a
  // held one. It shows the query sees the lock, not that sign-in succeeded.
  it("a page other than /signed-out holds a tab lock once settled", async () => {
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
    // Without release() the tab lock it requests on load is held from then on.
    await browser.waitUntil(async () => {
      const { held, pending } = await ours();
      return held.length === 0 && pending.length === 0;
    }, { timeout: 5_000, timeoutMsg: "the signed-out page holds or waits for a renewer lock" });
    // Still none a while later: no lock was granted to it after the first empty answer.
    await browser.pause(1_000);
    expect(await ours()).toEqual({ held: [], pending: [] });
  });
});
