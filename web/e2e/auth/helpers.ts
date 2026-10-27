// What the AUTH specs share. Only the parts of a URL a case needs cross WebDriver, so the harness
// log never records a token (Zitadel's logout page carries one in its query).
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { $, browser, expect } from "@wdio/globals";
import { CONTROL, FIXTURE_ISSUER, FIXTURE_PAGE, IDP_PAGE, idpEnv } from "../auth.conf.ts";
import type { Faults } from "../injector.ts";
import type { Counts } from "../provider.ts";

const root = fileURLToPath(new URL("../../..", import.meta.url));

export const where = () => browser.execute(() => location.origin + location.pathname);
export const param = (name: string) => browser.execute((n: string) => new URLSearchParams(location.search).get(n), name);
export const status = () => $('[data-testid="status"]').getText();

// Waits until the page is back on `page`/ with its reads settled: the page's state text, or the
// footer's server versions once the shell shows a page. Checked and read in one step in the page:
// an element a re-render replaced between two WebDriver calls would be stale for the second.
export async function settled(page: string): Promise<string> {
  let text: string | null = null;
  await browser.waitUntil(async () => {
    text = await browser.execute((want: string) => {
      if (location.origin + location.pathname !== want) return null;
      const s = document.querySelector<HTMLElement>('[data-testid="status"]');
      if (s !== null) return (s.textContent ?? "").startsWith("Loading") ? null : s.innerText.trim();
      if (document.querySelector("main h2") === null) return null;
      return document.querySelector<HTMLElement>('[data-testid="server"]')?.innerText.trim() ?? null;
    }, `${page}/`);
    return text !== null;
  }, { timeout: 20_000, timeoutMsg: `the page did not settle on ${page}/` });
  return text as unknown as string;
}

// A token the fixture issuer mints, under the synthetic provider's issuer.
export function mint(human: string, flags: Record<string, string> = {}): string {
  const args = ["mint", "-key", `${root}.dev/browser-auth/fixture.key`, "-issuer", FIXTURE_ISSUER, "-human", human];
  for (const [k, v] of Object.entries(flags)) args.push(`-${k}`, v);
  return execFileSync(`${root}bin/oidc-fixture`, args).toString().trim();
}

// Signs the fixture page in: the synthetic provider hands out the token the issuer mints with flags,
// and the injector applies the faults.
export async function fixtureSignIn(human: string, flags: Record<string, string> = {}, f: Faults = {}): Promise<string> {
  const q = new URLSearchParams({ human, ...flags });
  expect((await fetch(`${CONTROL}/next?${q}`)).ok).toBe(true);
  await faults(f);
  await browser.url(`${FIXTURE_PAGE}/`);
  return settled(FIXTURE_PAGE);
}

export const counts = async (): Promise<Counts> => (await fetch(`${CONTROL}/counts`)).json() as never;
export const seen = async (): Promise<string[]> => (await fetch(`${CONTROL}/seen`)).json() as never;
export const faults = async (f: Faults) => expect((await fetch(`${CONTROL}/faults`, { method: "POST", body: JSON.stringify(f) })).ok).toBe(true);
// Lets the answers the `held` fault holds go; returns once the injector has sent each.
export const release = async () => expect((await fetch(`${CONTROL}/release`, { method: "POST" })).ok).toBe(true);
// Whether the page has received every answer to `path` it asked for, by the browser's resource
// timing: an entry appears once its response has ended.
export const answered = (path: string, n: number) =>
  browser.execute((p: string) => performance.getEntriesByType("resource").filter((e) => new URL(e.name).pathname === p).length, `/api/v1${path}`)
    .then((count) => count >= n);

// Signs the test provider's page in as human: the page sends the browser to the provider, which has
// no login UI here; the case completes the authorization request through the Session API, as the
// login UI would, and hands the callback back to the browser.
export async function idpSignIn(human: string): Promise<string> {
  const env = idpEnv();
  const issuer = env["BW_TEST_IDP_ISSUER"]!;
  const users = Object.fromEntries(env["BW_TEST_IDP_USERS"]!.split(",").map((p) => p.split("=")));
  const login = { Authorization: `Bearer ${readFileSync(env["BW_TEST_IDP_LOGIN_TOKEN_FILE"]!, "utf8").trim()}`, "Content-Type": "application/json" };
  await browser.url(`${IDP_PAGE}/`);
  await browser.waitUntil(async () => (await where()) === `${issuer}/ui/v2/login/login`, { timeout: 20_000, timeoutMsg: "the page did not go to the provider" });
  const request = await param("authRequest");
  const s = await fetch(`${issuer}/v2/sessions`, {
    method: "POST", headers: login,
    body: JSON.stringify({ checks: { user: { userId: users[human] }, password: { password: env["BW_TEST_IDP_PASSWORD"] } } }),
  });
  const { sessionId, sessionToken } = (await s.json()) as { sessionId: string; sessionToken: string };
  const cb = await fetch(`${issuer}/v2/oidc/auth_requests/${request}`, { method: "POST", headers: login, body: JSON.stringify({ session: { sessionId, sessionToken } }) });
  expect(cb.status).toBe(200);
  // The code is JWT-shaped: it reaches the browser through the control redirect, never WebDriver.
  await fetch(`${CONTROL}/go`, { method: "POST", body: ((await cb.json()) as { callbackUrl: string }).callbackUrl });
  await browser.url(`${CONTROL}/go`);
  return settled(IDP_PAGE);
}

export const userId = (human: string) => Object.fromEntries(idpEnv()["BW_TEST_IDP_USERS"]!.split(",").map((p) => p.split("=")))[human] as string;

// Client contracts §11.5 item 3: no browser storage holds a value outside a redirect.
export async function storageEmpty() {
  const storage = await browser.execute(async () => ({
    local: Object.entries(localStorage),
    session: Object.entries(sessionStorage),
    indexedDB: "databases" in indexedDB ? (await indexedDB.databases()).map((d) => d.name ?? "") : ["unreadable"],
    caches: "caches" in self ? await caches.keys() : ["unreadable"],
  }));
  expect(storage).toEqual({ local: [], session: [], indexedDB: [], caches: [] });
}
