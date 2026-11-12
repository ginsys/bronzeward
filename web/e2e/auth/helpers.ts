// What the AUTH specs share. Only the parts of a URL a case needs cross WebDriver, so the harness
// log never records a token (Zitadel's logout page carries one in its query).
import { execFileSync } from "node:child_process";
import { randomBytes } from "node:crypto";
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

// Waits until the page is back on `page`/ with its reads settled: the footer's server versions once
// the shell shows a page, which may hold a state of its own (an empty list), else the state text.
// Checked and read in one step in the page: an element a re-render replaced between two WebDriver
// calls would be stale for the second.
export async function settled(page: string): Promise<string> {
  let text: string | null = null;
  await browser.waitUntil(async () => {
    text = await browser.execute((want: string) => {
      if (location.origin + location.pathname !== want) return null;
      const s = document.querySelector<HTMLElement>('[data-testid="status"]');
      if ((s?.textContent ?? "").startsWith("Loading")) return null;
      if (document.querySelector("main h2") !== null) return document.querySelector<HTMLElement>('[data-testid="server"]')?.innerText.trim() ?? null;
      return s !== null ? s.innerText.trim() : null;
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

// A case's start leaves the page an earlier case left, maybe with a write still open, without its
// leave dialog. Firefox's navigation stops at that dialog, even with unhandledPromptBehavior
// accepting it (wdio.conf.ts), so the next case would never start. A capturing listener on window
// runs before the page's own (forms.tsx useUnloadGuard) and stops it. The guard keeps its own cases.
async function leaveUnasked(): Promise<void> {
  await browser.execute(() => addEventListener("beforeunload", (e) => e.stopImmediatePropagation(), { capture: true })).catch(() => {});
}

// Signs the fixture page in: the synthetic provider hands out the token the issuer mints with flags,
// and the injector applies the faults.
export async function fixtureSignIn(human: string, flags: Record<string, string> = {}, f: Faults = {}): Promise<string> {
  const q = new URLSearchParams({ human, ...flags });
  expect((await fetch(`${CONTROL}/next?${q}`)).ok).toBe(true);
  await faults(f);
  await leaveUnasked();
  await browser.url(`${FIXTURE_PAGE}/`);
  return settled(FIXTURE_PAGE);
}

export const hex = () => randomBytes(4).toString("hex");

// Starts the disposable deployment as the control server's query says ("start=recovery&keep": for
// recovery, on the earlier one's database) and returns its URL.
export async function disposable(query: string): Promise<string> {
  const res = await fetch(`${CONTROL}/disposable?${query}`, { method: "POST" });
  expect(res.status).toBe(200);
  return res.text();
}

// One API call to the disposable deployment at `at`, as `human`, under the epoch it reports now.
export async function api(at: string, human: string, method: string, path: string, body?: unknown) {
  const auth = { Authorization: `Bearer ${mint(human)}` };
  const server = await fetch(`${at}/api/v1/server`, { headers: auth });
  const epoch = server.headers.get("Bronzeward-Epoch") ?? "";
  const r = await fetch(`${at}/api/v1${path}`, {
    method,
    headers: { ...auth, "Content-Type": "application/json", "Idempotency-Key": randomBytes(16).toString("base64url"), "Bronzeward-Expected-Epoch": epoch },
    body: body === undefined ? null : JSON.stringify(body),
  });
  return { status: r.status, body: (await r.json().catch(() => undefined)) as Record<string, unknown> | undefined };
}

// A synthetic cluster and one machine on it, made as h-author; returns the machine's id.
export async function syntheticMachine(at: string): Promise<string> {
  const name = `synthetic-${hex()}`;
  const c = await api(at, "h-author", "POST", "/clusters", { name, endpoint: `https://${name}.example.test:6443`, contract: "v1.14", talosClusterId: `${randomBytes(32).toString("base64url")}=` });
  expect(c.status).toBe(201);
  const m = await api(at, "h-author", "POST", "/machines", { cluster: c.body!["id"], talosNodeId: `synthetic-node-${hex()}`, platform: "metal", talosEndpoint: `192.0.2.${1 + (randomBytes(1)[0]! % 254)}` });
  expect(m.status).toBe(201);
  return m.body!["id"] as string;
}

export const counts = async (): Promise<Counts> => (await fetch(`${CONTROL}/counts`)).json() as never;
export const seen = async (): Promise<string[]> => (await fetch(`${CONTROL}/seen`)).json() as never;
// The server's answers the injector passed on, as "<route><query> <status>".
export const answers = async (): Promise<string[]> => (await fetch(`${CONTROL}/answered`)).json() as never;
export const faults = async (f: Faults) => expect((await fetch(`${CONTROL}/faults`, { method: "POST", body: JSON.stringify(f) })).ok).toBe(true);
// The paths whose answers the `held` fault holds now.
export const holding = async (): Promise<string[]> => (await fetch(`${CONTROL}/held`)).json() as never;
// Lets the answers the `held` fault holds go; returns once the injector has sent each.
export const release = async () => expect((await fetch(`${CONTROL}/release`, { method: "POST" })).ok).toBe(true);
// The point the disposable deployment's interrupt build stops at ("<step> <action>", ginsys/bronzeward#366);
// no point removes it, which ends a stall. Only after a sign-in with the faults' upstream on that deployment.
export const interrupt = async (point = "") => expect((await fetch(`${CONTROL}/interrupt?point=${encodeURIComponent(point)}`, { method: "POST" })).ok).toBe(true);
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

// What the page's storage holds, read the way client contracts §11.5 item 3 lists it.
const storageState = () =>
  browser.execute(async () => ({
    local: Object.entries(localStorage),
    session: Object.entries(sessionStorage),
    indexedDB: "databases" in indexedDB ? (await indexedDB.databases()).map((d) => d.name ?? "") : ["unreadable"],
    caches: "caches" in self ? await caches.keys() : ["unreadable"],
  }));

// Client contracts §11.5 item 3: no browser storage holds a value outside a redirect.
export async function storageEmpty() {
  expect(await storageState()).toEqual({ local: [], session: [], indexedDB: [], caches: [] });
}

// The positive control of storageEmpty (ginsys/bronzeward#341, client contracts §11.5): plants a
// harmless marker in each of the four stores and expects the same read to see every one, then removes
// them. The marker is not the run's canary, which never crosses WebDriver.
export async function storageControl() {
  try {
    await browser.execute(async () => {
      localStorage.setItem("bw-control", "marker");
      sessionStorage.setItem("bw-control", "marker");
      await new Promise<void>((ok, bad) => {
        const r = indexedDB.open("bw-control");
        r.onsuccess = () => (r.result.close(), ok());
        r.onerror = () => bad(r.error);
      });
      await caches.open("bw-control");
    });
    const s = await storageState();
    expect(s.local).toEqual([["bw-control", "marker"]]);
    expect(s.session).toEqual([["bw-control", "marker"]]);
    expect(s.indexedDB).toContain("bw-control");
    expect(s.caches).toContain("bw-control");
  } finally {
    await browser.execute(async () => {
      localStorage.removeItem("bw-control");
      sessionStorage.removeItem("bw-control");
      await new Promise<void>((ok) => {
        const r = indexedDB.deleteDatabase("bw-control");
        r.onsuccess = r.onerror = r.onblocked = () => ok();
      });
      await caches.delete("bw-control");
    });
  }
  await storageEmpty();
}

// Whether the browser's HTTP cache answers a same-origin GET of `path` with no request: a hit is an
// ok answer, a miss is a refused or failed one (a network error in Chrome and Edge, 504 in Firefox).
export const cacheHolds = (path: string) =>
  browser.execute(async (p: string) => {
    try {
      return (await fetch(p, { cache: "only-if-cached", mode: "same-origin" })).ok;
    } catch {
      return false;
    }
  }, path);

// The positive control of cacheHolds (ginsys/bronzeward#341): the injector answers one path with a
// cacheable response (every real answer says no-store), the page reads it once, and cacheHolds must
// then see it. Without this the check would pass in a browser whose probe never finds anything.
export async function cacheControl(upstream: string) {
  const path = "/api/v1/__cache-probe";
  await faults({ upstream, cacheable: "/__cache-probe" });
  try {
    expect(await browser.execute(async (p: string) => (await fetch(p)).ok, path)).toBe(true);
    expect(await cacheHolds(path)).toBe(true);
  } finally {
    await faults({ upstream });
  }
}
