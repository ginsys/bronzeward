// Shared by the specs that sign the page in through the synthetic provider and then read what its first API read left
// (tls.e2e.ts, packaged.e2e.ts).
import { browser } from "@wdio/globals";

// The page signs in through the synthetic provider first; this waits until it is back on `path`
// with its read settled, and returns the status the read left. Read in one step in the page: a
// status text gone between two WebDriver calls would hold the second for its whole implicit wait.
export async function settled(path: string): Promise<string> {
  let shown = "";
  await browser.waitUntil(async () => {
    const [at, status] = await browser.execute(() => [
      location.pathname + location.search,
      document.querySelector('[data-testid="status"]')?.textContent ?? "",
    ]);
    shown = status;
    return at === path && status !== "" && !status.startsWith("Loading");
  }, { timeout: 20_000, timeoutMsg: `the page did not settle on ${path}` });
  return shown;
}
