// What Safari's url override waits for (the override in wdio.conf.ts): the new document complete, because
// safaridriver answers Navigate To while it is still `interactive` and the find that follows can fail with a
// stale element (ginsys/bronzeward#1028). The wait is a function of its reads and its clock, so that
// safari-navigation.test.ts runs it without a browser.

// Reads the document's state until `wait` sees "complete". A timeout names the path, the state last read and
// the wait's own error, so that a failed read (a lost session, an alert) does not read as a slow page.
export async function settled(
  read: () => Promise<string>,
  path: string,
  wait: (until: () => Promise<boolean>, timeout: number) => Promise<unknown>,
  timeout = 15_000,
): Promise<void> {
  let state = "unread";
  await wait(async () => (state = await read()) === "complete", timeout).catch((e: unknown) => {
    throw new Error(`navigation to ${path} left readyState "${state}" after ${timeout / 1000} s: ${e instanceof Error ? e.message : String(e)}`);
  });
}
