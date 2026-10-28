// #388: the signed-out page releases its renewer, so it holds no tab lock and is not one of the tabs
// the renewer holder waits on before it sends a refresh token (renewer.ts).
import { $, browser, expect } from "@wdio/globals";
import { SIGNED_OUT } from "../src/app.tsx";
import { LOCK, TAB } from "../src/renewer.ts";

// The page's Web Locks whose names are the renewer's, held or waited for.
const ours = () =>
  browser.execute(
    async (names: string[]) => {
      const { held = [], pending = [] } = await navigator.locks.query();
      return [...held, ...pending].map((l) => l.name ?? "").filter((n) => names.some((p) => n.startsWith(p)));
    },
    [LOCK, TAB],
  );

describe("the signed-out page", () => {
  // Positive control: a signed-in page holds its tab lock, so the query below can see one.
  it("a signed-in page holds a tab lock", async () => {
    await browser.url("/");
    await browser.waitUntil(async () => (await ours()).some((n) => n.startsWith(TAB)), {
      timeout: 20_000, timeoutMsg: "the signed-in page took no tab lock",
    });
  });

  it("#388: holds no renewer lock, held or waited for", async () => {
    await browser.url("/signed-out");
    expect(await $('[data-testid="status"]').getText()).toBe(SIGNED_OUT);
    // Without release() the tab lock it requests on load is held from then on.
    await browser.waitUntil(async () => (await ours()).length === 0, {
      timeout: 5_000, timeoutMsg: "the signed-out page holds or waits for a renewer lock",
    });
    // Still none a while later: no lock was granted to it after the first empty answer.
    await browser.pause(1_000);
    expect(await ours()).toEqual([]);
  });
});
