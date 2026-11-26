// The legs' Firefox is fetched once, by the launcher (ginsys/bronzeward#1121). Without a binary in its capability every
// spec worker's session asks Mozilla for the channel's version and then sends one HEAD for the archive, even with the
// browser in the cache. Neither request is retried, and a HEAD answered with anything but 200 failed that worker's
// session ("Couldn't find a matching firefox browser"). With a binary a session makes neither request. `setup` and
// `wait` are parameters so that firefox-browser.test.ts runs this without a download.
import { setTimeout as sleep } from "node:timers/promises";
import { setupBrowser } from "@wdio/utils/node";
import { SevereServiceError } from "webdriverio";

const attempts = 4;

// For each Firefox capability of the run, fetches its channel or build with WebdriverIO's own setup, into the cache a
// worker would use, and names the binary in the capability. The launcher hands the same objects to the workers.
export async function fetchFirefox(
  capabilities: WebdriverIO.Capabilities[],
  cacheDir: string | undefined,
  setup: typeof setupBrowser = setupBrowser,
  wait: (ms: number) => Promise<unknown> = sleep,
) {
  for (const capability of capabilities) {
    const { browserName, browserVersion } = capability;
    if (browserName !== "firefox") continue;
    for (let attempt = 1; ; attempt++) {
      try {
        const fetched = await setup(cacheDir === undefined ? {} : { cacheDir }, [{ browserName, browserVersion: browserVersion! }]);
        const { executablePath } = fetched![0] as { executablePath: string };
        capability["moz:firefoxOptions"] = { ...capability["moz:firefoxOptions"], binary: executablePath };
        console.log(`firefox ${browserVersion}: ${executablePath} (fetched once, by the launcher)`);
        break;
      } catch (e) {
        // Severe, so that the launcher stops the run instead of leaving each worker to ask for itself.
        if (attempt === attempts) throw new SevereServiceError(`firefox ${browserVersion} could not be fetched in ${attempts} attempts: ${String(e)}`);
        console.error(`firefox ${browserVersion}: fetch attempt ${attempt} failed, trying again: ${String(e)}`);
        await wait(attempt * 5000);
      }
    }
  }
}
