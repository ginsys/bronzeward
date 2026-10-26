import { createHash } from "node:crypto";
import { describe, expect, test, vi } from "vitest";
import { beginSignIn, claims, completeSignIn, discover, localReading, PENDING_KEY, type Provider, renewDelay, returnPath, Session, SIGN_OUT_WAIT_MS, SIGNED_OUT_PATH, type Tokens } from "./auth.ts";
import type { ClientConfig } from "./config.ts";

const ORIGIN = "http://localhost:58443";
const ISSUER = "http://localhost:58280";
const config: ClientConfig = { issuer: ISSUER, audience: "bronzeward", cliClientId: "cli", webClientId: "web", scopes: ["urn:example:aud"] };
const provider: Provider = {
  issuer: ISSUER,
  authorizationEndpoint: `${ISSUER}/oauth/v2/authorize`,
  tokenEndpoint: `${ISSUER}/oauth/v2/token`,
  revocationEndpoint: `${ISSUER}/oauth/v2/revoke`,
  endSessionEndpoint: `${ISSUER}/oidc/v1/end_session`,
};

class MemoryStorage {
  readonly items = new Map<string, string>();
  getItem(k: string) { return this.items.get(k) ?? null; }
  setItem(k: string, v: string) { this.items.set(k, v); }
  removeItem(k: string) { this.items.delete(k); }
}
const memory = () => new MemoryStorage() as unknown as Storage & MemoryStorage;

const b64url = (s: string) => Buffer.from(s).toString("base64url");
const jwt = (c: Record<string, unknown>) => `${b64url('{"alg":"RS256"}')}.${b64url(JSON.stringify(c))}.sig`;
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

function answers(list: (Response | Error)[]) {
  return vi.fn(async (_url: RequestInfo | URL, _init?: RequestInit) => {
    const next = list.shift();
    if (next === undefined) throw new Error("no answer left");
    if (next instanceof Error) throw next;
    return next;
  });
}
const form = (init: RequestInit | undefined) => Object.fromEntries(init?.body as URLSearchParams);

describe("B2 return path", () => {
  test.each([
    ["/machines?cluster=c1#x", "/machines?cluster=c1#x"],
    ["https://evil.example.invalid/", "/"],
    ["//evil.example.invalid/", "/"],
    ["/\\evil.example.invalid/", "/"],
    ["/\t/evil.example.invalid/", "/"],
    ["machines", "/"],
    ["/callback?code=x", "/"],
    ["/signed-out", "/"],
  ])("%j returns to %j", (path, want) => {
    expect(returnPath(path)).toBe(want);
  });
});

describe("discovery", () => {
  const doc = {
    issuer: ISSUER,
    authorization_endpoint: provider.authorizationEndpoint,
    token_endpoint: provider.tokenEndpoint,
    revocation_endpoint: provider.revocationEndpoint,
    end_session_endpoint: provider.endSessionEndpoint,
  };

  test("reads the issuer's document, no-store, refusing redirects", async () => {
    const f = answers([json(doc)]);
    expect(await discover(`${ISSUER}/`, f as unknown as typeof fetch)).toEqual({ kind: "invalid", reason: "the identity provider's discovery document names another issuer" });
    const g = answers([json(doc)]);
    expect(await discover(ISSUER, g as unknown as typeof fetch)).toEqual({ kind: "ok", provider });
    expect(g).toHaveBeenCalledWith(`${ISSUER}/.well-known/openid-configuration`, { cache: "no-store", redirect: "error", credentials: "omit" });
  });

  test("without the optional endpoints, and refusing an untrusted one", async () => {
    const { revocation_endpoint: _r, end_session_endpoint: _e, ...bare } = doc;
    expect(await discover(ISSUER, answers([json(bare)]) as unknown as typeof fetch)).toEqual({
      kind: "ok",
      provider: { ...provider, revocationEndpoint: undefined, endSessionEndpoint: undefined },
    });
    const bad = { ...doc, token_endpoint: "http://idp.example.invalid/token" };
    expect((await discover(ISSUER, answers([json(bad)]) as unknown as typeof fetch)).kind).toBe("invalid");
    expect((await discover(ISSUER, answers([new TypeError("x")]) as unknown as typeof fetch)).kind).toBe("transport");
  });
});

// A sign-in begun with the storage, and the pending values the request carried.
async function begun(returnTo = "/machines") {
  const storage = memory();
  const url = new URL((await beginSignIn(config, provider, returnTo, storage, ORIGIN))!);
  const pending = JSON.parse(storage.getItem(PENDING_KEY)!) as Record<string, string>;
  return { storage, url, pending };
}

describe("§9.3 item 1 authorization request", () => {
  test("PKCE S256, state and nonce; the scopes after openid offline_access; only the pending entry stored", async () => {
    const { storage, url, pending } = await begun("/machines?cluster=c1");
    expect(url.origin + url.pathname).toBe(provider.authorizationEndpoint);
    const q = Object.fromEntries(url.searchParams);
    expect(q).toEqual({
      response_type: "code",
      client_id: "web",
      redirect_uri: `${ORIGIN}/callback`,
      scope: "openid offline_access urn:example:aud",
      state: pending["state"],
      nonce: pending["nonce"],
      code_challenge: createHash("sha256").update(pending["verifier"]!).digest("base64url"),
      code_challenge_method: "S256",
    });
    expect(pending["verifier"]!.length).toBe(43);
    expect(pending["returnTo"]).toBe("/machines?cluster=c1");
    expect([...storage.items.keys()]).toEqual([PENDING_KEY]);
  });

  test("a page that moves during the PKCE digest stores no pending entry and gets no URL", async () => {
    const storage = memory();
    let still = true;
    const url = beginSignIn(config, provider, "/machines", storage, ORIGIN, () => still);
    still = false;
    expect(await url).toBeUndefined();
    expect(storage.items.size).toBe(0);
  });
});

describe("§9.3 item 1 callback", () => {
  const tokenAnswer = (nonce: string, extra: Record<string, unknown> = {}) =>
    json({ access_token: "SYNTH-access", refresh_token: "SYNTH-refresh", expires_in: 300, id_token: jwt({ iss: ISSUER, aud: ["web", "p1"], sub: "u1", nonce, exp: 4_000_000_000, ...extra }) });

  test("exchanges the code with the verifier and deletes the pending entry", async () => {
    const { storage, pending } = await begun();
    const f = answers([tokenAnswer(pending["nonce"]!)]);
    const res = await completeSignIn(config, provider, `?code=c1&state=${pending["state"]}`, { storage, origin: ORIGIN, fetch: f as unknown as typeof fetch, now: () => 1000 });
    expect(res).toEqual({
      kind: "ok",
      tokens: { iss: ISSUER, sub: "u1", access: "SYNTH-access", refresh: "SYNTH-refresh", expiresAt: 301_000 },
      returnTo: "/machines",
    });
    expect(storage.items.size).toBe(0);
    const [url, init] = f.mock.calls[0]!;
    expect(url).toBe(provider.tokenEndpoint);
    expect(init).toMatchObject({ method: "POST", cache: "no-store", redirect: "error", credentials: "omit" });
    expect(form(init)).toEqual({ grant_type: "authorization_code", code: "c1", redirect_uri: `${ORIGIN}/callback`, client_id: "web", code_verifier: pending["verifier"] });
  });

  test.each([
    ["another state", (p: Record<string, string>) => `?code=c1&state=${p["state"]}x`, "invalid"],
    ["no code", (p: Record<string, string>) => `?state=${p["state"]}`, "invalid"],
    ["the provider's refusal", (p: Record<string, string>) => `?error=access_denied&state=${p["state"]}`, "refused"],
  ])("%s: nothing is sent and the entry is gone", async (_name, search, kind) => {
    const { storage, pending } = await begun();
    const f = answers([]);
    const res = await completeSignIn(config, provider, search(pending), { storage, origin: ORIGIN, fetch: f as unknown as typeof fetch });
    expect(res.kind).toBe(kind);
    expect(f).not.toHaveBeenCalled();
    expect(storage.items.size).toBe(0);
  });

  test("a callback with no sign-in in progress, or replayed, is refused", async () => {
    const { storage, pending } = await begun();
    const f = answers([tokenAnswer(pending["nonce"]!)]);
    const deps = { storage, origin: ORIGIN, fetch: f as unknown as typeof fetch };
    expect((await completeSignIn(config, provider, `?code=c1&state=${pending["state"]}`, deps)).kind).toBe("ok");
    expect(await completeSignIn(config, provider, `?code=c1&state=${pending["state"]}`, deps)).toEqual({ kind: "invalid", reason: "no sign-in was in progress in this tab" });
    expect(f).toHaveBeenCalledTimes(1);
  });

  test.each([
    ["another nonce", { nonce: "other" }],
    ["another issuer", { iss: "http://127.0.0.1:5556" }],
    ["another audience", { aud: "cli" }],
  ])("an ID token with %s is refused", async (_name, extra) => {
    const { storage, pending } = await begun();
    const f = answers([tokenAnswer(pending["nonce"]!, extra)]);
    const res = await completeSignIn(config, provider, `?code=c1&state=${pending["state"]}`, { storage, origin: ORIGIN, fetch: f as unknown as typeof fetch });
    expect(res).toEqual({ kind: "invalid", reason: "the ID token does not match this sign-in" });
  });

  test.each([
    ["has expired", { exp: 1 }],
    ["expires now", { exp: 2 }],
    ["carries no expiry", { exp: undefined }],
    ["carries a text expiry", { exp: "4000000000" }],
  ])("an ID token that %s is refused", async (_name, extra) => {
    const { storage, pending } = await begun();
    const f = answers([tokenAnswer(pending["nonce"]!, extra)]);
    const res = await completeSignIn(config, provider, `?code=c1&state=${pending["state"]}`, { storage, origin: ORIGIN, fetch: f as unknown as typeof fetch, now: () => 2000 });
    expect(res).toEqual({ kind: "invalid", reason: "the ID token has expired or carries no expiry" });
  });

  test("a refused exchange, a cut-off answer and an answer without an access token", async () => {
    for (const [answer, kind] of [
      [json({ error: "invalid_grant" }, 400), "refused"],
      [new Response(new ReadableStream({ pull: (c) => c.error(new TypeError("network error")) })), "transport"],
      [json({ token_type: "Bearer" }), "invalid"],
    ] as const) {
      const { storage, pending } = await begun();
      const res = await completeSignIn(config, provider, `?code=c1&state=${pending["state"]}`, { storage, origin: ORIGIN, fetch: answers([answer]) as unknown as typeof fetch });
      expect(res.kind).toBe(kind);
    }
  });
});

const tokens = (over: Partial<Tokens> = {}): Tokens => ({ iss: ISSUER, sub: "u1", access: "SYNTH-a1", refresh: "SYNTH-r1", expiresAt: 300_000, ...over });

describe("§9.5 renewal", () => {
  test("one renewal at a time, with the rotated pair kept and its expiry by the page's clock", async () => {
    const f = answers([
      json({ access_token: "SYNTH-a2", refresh_token: "SYNTH-r2", expires_in: 300 }),
      json({ access_token: "SYNTH-a3", refresh_token: "SYNTH-r3", expires_in: 300 }),
    ]);
    const s = new Session(config, provider, tokens(), { fetch: f as unknown as typeof fetch, now: () => 10_000 });
    expect(await Promise.all([s.renew(), s.renew()])).toEqual([true, true]);
    expect(f).toHaveBeenCalledTimes(1);
    expect(form(f.mock.calls[0]![1])).toEqual({ grant_type: "refresh_token", refresh_token: "SYNTH-r1", client_id: "web" });
    expect(s.token()).toBe("SYNTH-a2");
    expect(s.expiresAt).toBe(310_000);
    // The same session renews again with the refresh token the first renewal returned.
    expect(await s.renew()).toBe(true);
    expect(f.mock.calls.map((c) => form(c[1])["refresh_token"])).toEqual(["SYNTH-r1", "SYNTH-r2"]);
    expect(s.token()).toBe("SYNTH-a3");
  });

  test.each([
    ["refused", json({ error: "invalid_grant" }, 400)],
    ["unreachable", new TypeError("Failed to fetch")],
  ])("a refresh token is sent once: after a %s renewal the current token stays and nothing is sent again", async (_name, answer) => {
    const f = answers([answer]);
    const s = new Session(config, provider, tokens(), { fetch: f as unknown as typeof fetch });
    expect(await s.renew()).toBe(false);
    expect(s.token()).toBe("SYNTH-a1");
    expect(await s.renew()).toBe(false);
    expect(f).toHaveBeenCalledTimes(1);
  });

  test.each([
    ["returned unchanged", { refresh_token: "SYNTH-r1" }],
    ["not returned", {}],
  ])("§9.3 items 1 and 3: a refresh token %s is not sent again, and the page renews by the redirect", async (_name, extra) => {
    const f = answers([json({ access_token: "SYNTH-a2", expires_in: 300, ...extra })]);
    const s = new Session(config, provider, tokens(), { fetch: f as unknown as typeof fetch });
    expect(s.byRedirect).toBe(false);
    expect(await s.renew()).toBe(true);
    expect(s.token()).toBe("SYNTH-a2");
    expect(s.byRedirect).toBe(true);
    expect(await s.renew()).toBe(false);
    expect(f).toHaveBeenCalledTimes(1);
  });

  test("a sign-in that brought no refresh token sends no renewal, and renews by the redirect", async () => {
    const f = answers([]);
    const s = new Session(config, provider, tokens({ refresh: undefined }), { fetch: f as unknown as typeof fetch });
    expect(s.byRedirect).toBe(true);
    expect(await s.renew()).toBe(false);
    expect(f).not.toHaveBeenCalled();
    s.clear();
    expect(s.byRedirect).toBe(false);
  });

  // §9.5 item 3: a failed or spent refresh token is a failed renewal, never a redirect.
  test.each([
    ["undelivered", (s: Session) => s.renew(), [new TypeError("Failed to fetch")]],
    ["refused", (s: Session) => s.renew(), [json({ error: "invalid_grant" }, 400)]],
    ["spent by another tab", (s: Session) => s.spend(), []],
  ] as const)("a refresh token %s leaves no renewal by the redirect, here or in a new tab", async (_name, act, list) => {
    const s = new Session(config, provider, tokens(), { fetch: answers([...list]) as unknown as typeof fetch });
    await act(s);
    expect(s.byRedirect).toBe(false);
    expect(await s.renew()).toBe(false);
    // A new tab given this pair, without its refresh token, would renew by the redirect.
    expect(s.pair()).toBeUndefined();
  });

  test("a renewal that fails after another tab's pair was taken leaves that pair to give", async () => {
    let release!: (r: Response) => void;
    const f = vi.fn(() => new Promise<Response>((r) => (release = r)));
    const s = new Session(config, provider, tokens(), { fetch: f as unknown as typeof fetch });
    const renewal = s.renew();
    expect(s.adopt(tokens({ access: "SYNTH-a3", refresh: "SYNTH-r3", expiresAt: 500_000 }))).toBe(true);
    release(json({ error: "invalid_grant" }, 400));
    expect(await renewal).toBe(false);
    expect(s.pair()?.refresh).toBe("SYNTH-r3");
  });

  test("another tab's pair without a refresh token renews by the redirect; with one, by the refresh token", () => {
    const s = new Session(config, provider, tokens(), { fetch: answers([]) as unknown as typeof fetch });
    expect(s.adopt(tokens({ refresh: undefined, expiresAt: 400_000 }))).toBe(true);
    expect(s.byRedirect).toBe(true);
    expect(s.adopt(tokens({ refresh: "SYNTH-r3", expiresAt: 500_000 }))).toBe(true);
    expect(s.byRedirect).toBe(false);
  });

  test("a renewal with no ID token, whose access token names another subject, is a new sign-in", async () => {
    const onPrincipalChange = vi.fn();
    const access = (sub: string) => jwt({ iss: ISSUER, sub });
    const f = answers([
      json({ access_token: access("u1"), refresh_token: "SYNTH-r2", expires_in: 300 }),
      json({ access_token: access("u2"), refresh_token: "SYNTH-r3", expires_in: 300 }),
    ]);
    const s = new Session(config, provider, tokens({ access: access("u1") }), { fetch: f as unknown as typeof fetch, onPrincipalChange });
    await s.renew();
    expect(onPrincipalChange).not.toHaveBeenCalled();
    await s.renew();
    expect(onPrincipalChange).toHaveBeenCalledWith({ iss: ISSUER, sub: "u1" }, { iss: ISSUER, sub: "u2" });
  });

  test("with no ID token, an old access token that names no subject reads as no change", async () => {
    const onPrincipalChange = vi.fn();
    // The renewed token names u2, so a comparison against the incomplete old token would report it.
    const f = answers([json({ access_token: jwt({ iss: ISSUER, sub: "u2" }), refresh_token: "SYNTH-r2", expires_in: 300 })]);
    const s = new Session(config, provider, tokens({ access: jwt({ iss: ISSUER }) }), { fetch: f as unknown as typeof fetch, onPrincipalChange });
    await s.renew();
    expect(onPrincipalChange).not.toHaveBeenCalled();
  });

  test("with no ID token, an access-token account change is shown even when the new subject matches the ID token's", async () => {
    const onPrincipalChange = vi.fn();
    const f = answers([json({ access_token: jwt({ iss: ISSUER, sub: "u1" }), refresh_token: "SYNTH-r2", expires_in: 300 })]);
    // The ID token named u1; the access token the session held named api-user.
    const s = new Session(config, provider, tokens({ access: jwt({ iss: ISSUER, sub: "api-user" }) }), { fetch: f as unknown as typeof fetch, onPrincipalChange });
    await s.renew();
    expect(onPrincipalChange).toHaveBeenCalledWith({ iss: ISSUER, sub: "api-user" }, { iss: ISSUER, sub: "u1" });
  });

  test("a renewal for another subject is a new sign-in", async () => {
    const onPrincipalChange = vi.fn();
    const f = answers([json({ access_token: "SYNTH-a2", refresh_token: "SYNTH-r2", expires_in: 300, id_token: jwt({ iss: ISSUER, sub: "u2" }) })]);
    const s = new Session(config, provider, tokens(), { fetch: f as unknown as typeof fetch, onPrincipalChange });
    await s.renew();
    expect(onPrincipalChange).toHaveBeenCalledWith({ iss: ISSUER, sub: "u1" }, { iss: ISSUER, sub: "u2" });
    expect(s.principal).toEqual({ iss: ISSUER, sub: "u2" });
  });

  test("B1: only a newer pair of the same principal is adopted, and a spent refresh token is neither sent nor posted", async () => {
    const f = answers([]);
    const s = new Session(config, provider, tokens(), { fetch: f as unknown as typeof fetch });
    expect(s.adopt(tokens({ access: "SYNTH-a0", expiresAt: 300_000 }))).toBe(false);
    expect(s.adopt(tokens({ access: "SYNTH-a2", sub: "u2", expiresAt: 400_000 }))).toBe(false);
    expect(s.adopt(tokens({ access: "SYNTH-a2", expiresAt: 400_000 }))).toBe(true);
    expect(s.token()).toBe("SYNTH-a2");
    expect(s.pair()).toEqual(tokens({ access: "SYNTH-a2", expiresAt: 400_000 }));
    s.spend();
    expect(s.pair()).toBeUndefined();
    expect(await s.renew()).toBe(false);
    expect(f).not.toHaveBeenCalled();
    expect(s.adopt(tokens({ access: "SYNTH-a3", refresh: "SYNTH-r3", expiresAt: 500_000 }))).toBe(true);
    expect(s.pair()?.refresh).toBe("SYNTH-r3");
    s.clear();
    expect(s.adopt(tokens({ expiresAt: 600_000 }))).toBe(false);
  });

  test("a pair that arrives after the session was cleared is not kept, and its refresh token is revoked", async () => {
    let release!: (r: Response) => void;
    const f = vi.fn((url: RequestInfo | URL, _init?: RequestInit) => (url === provider.tokenEndpoint ? new Promise<Response>((r) => (release = r)) : Promise.resolve(new Response(null, { status: 200 }))));
    const s = new Session(config, provider, tokens(), { fetch: f as unknown as typeof fetch });
    const renewal = s.renew();
    s.clear();
    release(json({ access_token: "SYNTH-a2", refresh_token: "SYNTH-r2", expires_in: 300 }));
    expect(await renewal).toBe(false);
    expect(s.token()).toBeUndefined();
    expect(f.mock.calls[1]![0]).toBe(provider.revocationEndpoint);
    expect(form(f.mock.calls[1]![1])["token"]).toBe("SYNTH-r2");
  });
});

describe("§9.7 item 2 sign-out", () => {
  test("revokes the refresh token, forgets the tokens and goes to end-session with no token in the URL", async () => {
    const f = answers([new Response(null, { status: 200 })]);
    const s = new Session(config, provider, tokens(), { fetch: f as unknown as typeof fetch });
    const to = new URL(await s.signOut(ORIGIN));
    expect(form(f.mock.calls[0]![1])).toEqual({ token: "SYNTH-r1", token_type_hint: "refresh_token", client_id: "web" });
    expect(f.mock.calls[0]![0]).toBe(provider.revocationEndpoint);
    expect(to.origin + to.pathname).toBe(provider.endSessionEndpoint);
    expect(Object.fromEntries(to.searchParams)).toEqual({ client_id: "web", post_logout_redirect_uri: `${ORIGIN}${SIGNED_OUT_PATH}` });
    expect(s.token()).toBeUndefined();
  });

  test("during a renewal, waits for it and revokes the refresh token it brings", async () => {
    let release!: (r: Response) => void;
    const f = vi.fn((url: RequestInfo | URL, _init?: RequestInit) => (url === provider.tokenEndpoint ? new Promise<Response>((r) => (release = r)) : Promise.resolve(new Response(null, { status: 200 }))));
    const s = new Session(config, provider, tokens(), { fetch: f as unknown as typeof fetch });
    const renewal = s.renew();
    let settled = false;
    const out = s.signOut(ORIGIN).then(() => (settled = true));
    await new Promise((r) => setTimeout(r, 10));
    // Still waiting for the renewal: the pair it brings is not yet revoked.
    expect(settled).toBe(false);
    release(json({ access_token: "SYNTH-a2", refresh_token: "SYNTH-r2", expires_in: 300 }));
    await out;
    expect(await renewal).toBe(false);
    // SYNTH-r1, sent by the renewal, goes at once: the provider has either rotated it, when its
    // revocation changes nothing or ends the family at sign-out anyway, or never received it. Then
    // the pair the renewal brought.
    expect(f.mock.calls.filter((c) => c[0] === provider.revocationEndpoint).map((c) => form(c[1])["token"])).toEqual(["SYNTH-r1", "SYNTH-r2"]);
    expect(s.token()).toBeUndefined();
  });

  test("a revocation that never answers delays sign-out by at most SIGN_OUT_WAIT_MS", async () => {
    vi.useFakeTimers();
    try {
      const f = vi.fn(() => new Promise<Response>(() => {}));
      const s = new Session(config, { ...provider, endSessionEndpoint: undefined }, tokens(), { fetch: f as unknown as typeof fetch });
      let to: string | undefined;
      void s.signOut(ORIGIN).then((u) => (to = u));
      await vi.advanceTimersByTimeAsync(SIGN_OUT_WAIT_MS - 1);
      expect(to).toBeUndefined();
      await vi.advanceTimersByTimeAsync(1);
      expect(to).toBe(SIGNED_OUT_PATH);
    } finally {
      vi.useRealTimers();
    }
  });

  test("during a renewal that never answers, revokes at once the refresh token of a pair taken from another tab", async () => {
    vi.useFakeTimers();
    try {
      const f = vi.fn((url: RequestInfo | URL, _init?: RequestInit) => (url === provider.tokenEndpoint ? new Promise<Response>(() => {}) : Promise.resolve(new Response(null, { status: 200 }))));
      const s = new Session(config, provider, tokens(), { fetch: f as unknown as typeof fetch });
      void s.renew();
      expect(s.adopt(tokens({ access: "SYNTH-a3", refresh: "SYNTH-r3", expiresAt: 600_000 }))).toBe(true);
      void s.signOut(ORIGIN);
      await vi.advanceTimersByTimeAsync(0);
      expect(f.mock.calls.filter((c) => c[0] === provider.revocationEndpoint).map((c) => form(c[1])["token"])).toEqual(["SYNTH-r3"]);
    } finally {
      vi.useRealTimers();
    }
  });

  test("during a renewal that never answers, revokes at once the refresh token it sent", async () => {
    vi.useFakeTimers();
    try {
      const f = vi.fn((url: RequestInfo | URL, _init?: RequestInit) => (url === provider.tokenEndpoint ? new Promise<Response>(() => {}) : Promise.resolve(new Response(null, { status: 200 }))));
      const s = new Session(config, provider, tokens(), { fetch: f as unknown as typeof fetch });
      void s.renew();
      void s.signOut(ORIGIN);
      await vi.advanceTimersByTimeAsync(0);
      expect(f.mock.calls.filter((c) => c[0] === provider.revocationEndpoint).map((c) => form(c[1])["token"])).toEqual(["SYNTH-r1"]);
    } finally {
      vi.useRealTimers();
    }
  });

  // §10.3 item 3: a failure after the page cleared the session gives the sent token to nothing that
  // stays; it is revoked instead, since no sign-out will.
  test("a renewal that fails after the page cleared the session revokes the refresh token it sent", async () => {
    let release!: (r: Response) => void;
    const f = vi.fn((url: RequestInfo | URL, _init?: RequestInit) => (url === provider.tokenEndpoint ? new Promise<Response>((r) => (release = r)) : Promise.resolve(new Response(null, { status: 200 }))));
    const s = new Session(config, provider, tokens(), { fetch: f as unknown as typeof fetch });
    const renewal = s.renew();
    s.clear();
    release(new Response(null, { status: 503 }));
    expect(await renewal).toBe(false);
    expect(f.mock.calls.filter((c) => c[0] === provider.revocationEndpoint).map((c) => form(c[1])["token"])).toEqual(["SYNTH-r1"]);
  });

  // CP2-F2: a refresh token no longer sent for renewal is still revoked at sign-out.
  test.each([
    ["an undelivered renewal", [new TypeError("Failed to fetch")], (s: Session) => s.renew(), "SYNTH-r1"],
    ["a refused renewal", [json({ error: "invalid_grant" }, 400)], (s: Session) => s.renew(), "SYNTH-r1"],
    ["an unrotated renewal", [json({ access_token: "SYNTH-a2", refresh_token: "SYNTH-r1", expires_in: 300 })], (s: Session) => s.renew(), "SYNTH-r1"],
    ["a renewal that brings no refresh token", [json({ access_token: "SYNTH-a2", expires_in: 300 })], (s: Session) => s.renew(), "SYNTH-r1"],
    ["a token another tab may have spent", [], (s: Session) => s.spend(), "SYNTH-r1"],
    ["a rotated renewal", [json({ access_token: "SYNTH-a2", refresh_token: "SYNTH-r2", expires_in: 300 })], (s: Session) => s.renew(), "SYNTH-r2"],
  ] as const)("after %s, sign-out revokes the last refresh token held", async (_name, list, before, revoked) => {
    const f = answers([...list, new Response(null, { status: 200 })]);
    const s = new Session(config, provider, tokens(), { fetch: f as unknown as typeof fetch });
    await before(s);
    // As the page does: it clears the session as soon as sign-out has taken the tokens.
    const out = s.signOut(ORIGIN);
    s.clear();
    await out;
    expect(f.mock.calls.filter((c) => c[0] === provider.revocationEndpoint).map((c) => form(c[1])["token"])).toEqual([revoked]);
  });

  test("during a renewal whose answer brings no refresh token, revokes the one it sent", async () => {
    let release!: (r: Response) => void;
    const f = vi.fn((url: RequestInfo | URL, _init?: RequestInit) => (url === provider.tokenEndpoint ? new Promise<Response>((r) => (release = r)) : Promise.resolve(new Response(null, { status: 200 }))));
    const s = new Session(config, provider, tokens(), { fetch: f as unknown as typeof fetch });
    const renewal = s.renew();
    const out = s.signOut(ORIGIN);
    s.clear();
    release(json({ access_token: "SYNTH-a2", expires_in: 300 }));
    await out;
    expect(await renewal).toBe(false);
    expect(f.mock.calls.filter((c) => c[0] === provider.revocationEndpoint).map((c) => form(c[1])["token"])).toEqual(["SYNTH-r1"]);
  });

  test("during a renewal that fails, and with the page cleared meanwhile, revokes the refresh token it gives back", async () => {
    let release!: (r: Response) => void;
    const f = vi.fn((url: RequestInfo | URL, _init?: RequestInit) => (url === provider.tokenEndpoint ? new Promise<Response>((r) => (release = r)) : Promise.resolve(new Response(null, { status: 200 }))));
    const s = new Session(config, provider, tokens(), { fetch: f as unknown as typeof fetch });
    const renewal = s.renew();
    const out = s.signOut(ORIGIN);
    s.clear();
    release(json({ error: "invalid_grant" }, 400));
    await out;
    expect(await renewal).toBe(false);
    expect(f.mock.calls.filter((c) => c[0] === provider.revocationEndpoint).map((c) => form(c[1])["token"])).toEqual(["SYNTH-r1"]);
  });

  test("after a cleared page, sign-out revokes nothing", async () => {
    const f = answers([]);
    const s = new Session(config, provider, tokens(), { fetch: f as unknown as typeof fetch });
    s.clear();
    await s.signOut(ORIGIN);
    expect(f).not.toHaveBeenCalled();
  });

  test("an unreachable revocation still signs out; without end-session, the signed-out page", async () => {
    const f = answers([new TypeError("Failed to fetch")]);
    const s = new Session(config, { ...provider, endSessionEndpoint: undefined }, tokens(), { fetch: f as unknown as typeof fetch });
    expect(await s.signOut(ORIGIN)).toBe(SIGNED_OUT_PATH);
    expect(s.token()).toBeUndefined();
  });
});

test("JWT claims are read only when they decode to an object", () => {
  expect(claims(jwt({ sub: "u1" }))).toEqual({ sub: "u1" });
  expect(claims("nodots")).toBeUndefined();
  expect(claims(`a.${b64url("[1]")}.c`)).toBeUndefined();
  expect(claims("a.%%%.c")).toBeUndefined();
});

test("§9.6 the browser's own reading of a refused token", () => {
  const now = Date.UTC(2026, 9, 26);
  const exp = now / 1000 + 300;
  expect(localReading(jwt({ iss: ISSUER, aud: "bronzeward", exp }), config, now)).toBeUndefined();
  expect(localReading(jwt({ iss: ISSUER, aud: ["x", "bronzeward"], exp }), config, now)).toBeUndefined();
  expect(localReading(jwt({ iss: "https://other", aud: "bronzeward", exp }), config, now)).toBe(`the token is from https://other; this page expects ${ISSUER}`);
  expect(localReading(jwt({ iss: ISSUER, aud: "other", exp }), config, now)).toBe("the token is not issued for bronzeward; the identity provider needs an audience mapping");
  expect(localReading(jwt({ iss: ISSUER, aud: "bronzeward", exp: now / 1000 }), config, now)).toBe("the token expired at 2026-10-26T00:00:00.000Z");
  expect(localReading(jwt({ iss: ISSUER, aud: "bronzeward", exp: -1e100 }), config, now)).toBeUndefined();
  expect(localReading("opaque", config, now)).toBeUndefined();
});

test("§9.5 item 1 renews 60 s before expiry, never sooner than half the remaining lifetime", () => {
  expect(renewDelay(300_000, 0)).toBe(240_000);
  expect(renewDelay(30_000, 0)).toBe(15_000);
  expect(renewDelay(1_000, 2_000)).toBe(0);
});
