// Browser sign-in, renewal and sign-out (client contracts §9.3 as amended by B2, §9.5, §9.7).
// Tokens live in a Session's private fields only (§9.3 item 2). The one storage use is the
// pending sign-in, from the redirect to the callback (§9.3 item 1, B2).

import { BASE_SCOPES, type ClientConfig, trustedIssuer } from "./config.ts";
import { CUT, NOT_JSON, readJson } from "./request.ts";

export const PENDING_KEY = "bronzeward-sign-in";
export const CALLBACK_PATH = "/callback";
export const SIGNED_OUT_PATH = "/signed-out";
// §9.5 item 1: renew when less than 60 s of the access token's lifetime remains.
export const RENEW_BEFORE_MS = 60_000;
// §9.7 item 2: how long sign-out waits for the revocation before it leaves anyway.
export const SIGN_OUT_WAIT_MS = 5_000;
// §9.3 item 3 (#388): how long a refresh-token request may go unanswered before the renewal fails.
export const REFRESH_WAIT_MS = 15_000;

// When to renew, in ms from now: 60 s before expiry, but never sooner than half the remaining
// lifetime, so a provider's short-lived tokens are not renewed one after another without pause.
export function renewDelay(expiresAt: number, now: number): number {
  const left = expiresAt - now;
  return Math.max(left - RENEW_BEFORE_MS, left / 2, 0);
}

const NO_STORE = { cache: "no-store", redirect: "error", credentials: "omit" } as const;

export interface Provider {
  issuer: string;
  authorizationEndpoint: string;
  tokenEndpoint: string;
  revocationEndpoint: string | undefined;
  endSessionEndpoint: string | undefined;
}

export type Principal = { iss: string; sub: string };

export interface Tokens extends Principal {
  access: string;
  refresh: string | undefined;
  // By this browser's clock (§9.5 item 1).
  expiresAt: number;
}

// A provider refusal is the provider's answer; invalid is a defect in what it or the page sent.
export type Failure = { kind: "transport" } | { kind: "refused"; reason: string } | { kind: "invalid"; reason: string };

const invalid = (reason: string): Failure => ({ kind: "invalid", reason });

// OpenID Connect Discovery 1.0 §4.3: the document names exactly the configured issuer.
export async function discover(issuer: string, fetchImpl: typeof fetch = fetch): Promise<{ kind: "ok"; provider: Provider } | Failure> {
  let res: Response;
  try {
    res = await fetchImpl(`${issuer.replace(/\/$/, "")}/.well-known/openid-configuration`, NO_STORE);
  } catch {
    return { kind: "transport" };
  }
  const body = await readJson(res);
  if (body === CUT) return { kind: "transport" };
  if (!res.ok) return invalid(`the identity provider's discovery document answered ${res.status}`);
  if (typeof body !== "object" || body === null) return invalid("the identity provider's discovery document is not a JSON object");
  const doc = body as Record<string, unknown>;
  if (doc["issuer"] !== issuer) return invalid("the identity provider's discovery document names another issuer");
  const endpoint = (name: string, required: boolean): string | undefined | null => {
    const v = doc[name];
    if (v === undefined && !required) return undefined;
    return typeof v === "string" && trustedIssuer(v) ? v : null;
  };
  const authorizationEndpoint = endpoint("authorization_endpoint", true);
  const tokenEndpoint = endpoint("token_endpoint", true);
  const revocationEndpoint = endpoint("revocation_endpoint", false);
  const endSessionEndpoint = endpoint("end_session_endpoint", false);
  if (!authorizationEndpoint || !tokenEndpoint || revocationEndpoint === null || endSessionEndpoint === null) {
    return invalid("an identity provider endpoint is missing, or neither https nor http to a loopback address");
  }
  return { kind: "ok", provider: { issuer, authorizationEndpoint, tokenEndpoint, revocationEndpoint, endSessionEndpoint } };
}

// B2: a path on this origin, never a full URL. A browser reads `\` and a tab or newline inside a
// URL as nothing or as `/`, so either could turn a path into another origin's address.
export function returnPath(path: string): string {
  if (!path.startsWith("/") || path.startsWith("//") || /[\\\x00-\x1f\x7f]/.test(path)) return "/";
  const { pathname } = new URL(path, "http://h");
  return pathname === CALLBACK_PATH || pathname === SIGNED_OUT_PATH ? "/" : path;
}

interface Pending {
  verifier: string;
  state: string;
  nonce: string;
  returnTo: string;
}

// The authorization request (§9.3 item 1): its URL, after the pending entry is stored; nothing is
// stored, and there is no URL, unless `still` holds after the PKCE digest (no redirect follows).
export async function beginSignIn(config: ClientConfig, provider: Provider, returnTo: string, storage: Storage, origin: string, still: () => boolean = () => true): Promise<string | undefined> {
  const pending: Pending = { verifier: random(32), state: random(16), nonce: random(16), returnTo: returnPath(returnTo) };
  const challenge = base64url(new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(pending.verifier))));
  if (!still()) return undefined;
  storage.setItem(PENDING_KEY, JSON.stringify(pending));
  const url = new URL(provider.authorizationEndpoint);
  const params = {
    response_type: "code",
    client_id: config.webClientId,
    redirect_uri: origin + CALLBACK_PATH,
    scope: [...BASE_SCOPES, ...config.scopes].join(" "),
    state: pending.state,
    nonce: pending.nonce,
    code_challenge: challenge,
    code_challenge_method: "S256",
  };
  for (const [k, v] of Object.entries(params)) url.searchParams.set(k, v);
  return url.href;
}

export interface CallbackDeps {
  storage: Storage;
  origin: string;
  fetch?: typeof fetch;
  now?: () => number;
}

// The callback: the pending entry is deleted first, whatever the answer (§9.3 item 1).
export async function completeSignIn(config: ClientConfig, provider: Provider, search: string, deps: CallbackDeps): Promise<{ kind: "ok"; tokens: Tokens; returnTo: string } | Failure> {
  const raw = deps.storage.getItem(PENDING_KEY);
  deps.storage.removeItem(PENDING_KEY);
  const pending = readPending(raw);
  if (pending === undefined) return invalid("no sign-in was in progress in this tab");
  const q = new URLSearchParams(search);
  if (q.get("state") !== pending.state) return invalid("the sign-in answer does not belong to the request this tab sent");
  const error = q.get("error");
  if (error !== null) return { kind: "refused", reason: `the identity provider refused the sign-in${/^[a-z_]{1,64}$/.test(error) ? ` (${error})` : ""}` };
  const code = q.get("code");
  if (code === null || code === "") return invalid("the sign-in answer carries no code");
  const answer = await tokenRequest(provider, {
    grant_type: "authorization_code",
    code,
    redirect_uri: deps.origin + CALLBACK_PATH,
    client_id: config.webClientId,
    code_verifier: pending.verifier,
  }, deps.fetch);
  if (answer.kind !== "ok") return answer;
  // OpenID Connect Core §3.1.3.7: the ID token came straight from the token endpoint over a
  // trusted connection, so its claims are read without checking its signature (item 6).
  const id = typeof answer.body.id_token === "string" ? claims(answer.body.id_token) : undefined;
  if (id === undefined) return invalid("the token answer has no readable ID token");
  const aud = Array.isArray(id["aud"]) ? id["aud"] : [id["aud"]];
  if (id["iss"] !== provider.issuer || !aud.includes(config.webClientId) || id["nonce"] !== pending.nonce || typeof id["sub"] !== "string") {
    return invalid("the ID token does not match this sign-in");
  }
  const now = (deps.now ?? Date.now)();
  // Item 9: the current time is before its expiry, which it must carry.
  if (typeof id["exp"] !== "number" || id["exp"] * 1000 <= now) return invalid("the ID token has expired or carries no expiry");
  return { kind: "ok", tokens: toTokens(answer.body, { iss: provider.issuer, sub: id["sub"] }, now), returnTo: pending.returnTo };
}

export interface SessionOptions {
  fetch?: typeof fetch;
  now?: () => number;
  // §9.5 item 2: a renewal answered for another subject or issuer is a new sign-in.
  onPrincipalChange?: (before: Principal, after: Principal) => void;
  // B1: told the refresh token before it is sent, and given the pair it brings, before the item 2
  // check; `spent` says another tab has sent this refresh token, so it is never sent or given here.
  // False from onRenewing: the token is not sent (#388, every other tab must know it first).
  onRenewing?: (refresh: string) => Promise<boolean> | void;
  onRenewed?: (tokens: Tokens) => void;
  spent?: (refresh: string) => boolean;
}

// One signed-in principal's tokens; nothing outside reads them but the access token itself.
export class Session {
  #tokens: Tokens | undefined;
  #renewing: Promise<boolean> | undefined;
  #spent = false;
  // §9.3 item 3: the provider issues no rotating refresh token, so the page renews by the redirect.
  #byRedirect: boolean;
  // §9.7 item 2: the last refresh token this tab held, kept for sign-out's revocation after it may
  // no longer be sent for renewal (a failed or unrotated renewal, a spent token).
  #revocable: string | undefined;
  // The refresh token a renewal in flight has sent, until it answers or sign-out takes it.
  #inFlight: string | undefined;
  // Every refresh token sent for revocation, none of which is sent again (#388).
  readonly #revoked = new Set<string>();
  // #396: the revocations of what the answers of timed-out refresh requests bring, settled when the
  // answer has come and its refresh token has gone; sign-out waits for them within its bound.
  readonly #late = new Set<Promise<void>>();
  // #388: given up after this tab may have missed a `renewing`, until another pair is taken.
  #forfeited = false;

  constructor(
    private readonly config: ClientConfig,
    private readonly provider: Provider,
    tokens: Tokens,
    private readonly opts: SessionOptions = {},
  ) {
    this.#tokens = tokens;
    this.#byRedirect = tokens.refresh === undefined;
    this.#revocable = tokens.refresh;
  }

  get byRedirect(): boolean {
    return this.#tokens !== undefined && this.#byRedirect;
  }

  get principal(): Principal | undefined {
    return this.#tokens === undefined ? undefined : { iss: this.#tokens.iss, sub: this.#tokens.sub };
  }

  get expiresAt(): number | undefined {
    return this.#tokens?.expiresAt;
  }

  token(): string | undefined {
    return this.#tokens?.access;
  }

  // B1: the pair to post to another tab, unless its refresh token may already be spent, or is being
  // sent: until the renewal answers, the pair has no refresh token, and a new tab would take it for
  // a provider that does not rotate (§9.3 item 3). The renewal posts the pair it brings.
  pair(): Tokens | undefined {
    this.#checkSpent();
    return this.#tokens === undefined || this.#spent || this.#renewing !== undefined ? undefined : { ...this.#tokens };
  }

  // A refresh token another tab has sent is spent here too, whatever the pairs' expiries say.
  #checkSpent(): void {
    const refresh = this.#tokens?.refresh;
    if (refresh !== undefined && this.opts.spent?.(refresh) === true) this.spend();
  }

  // B1: another tab's newer pair for the same principal replaces this one, as does the current pair
  // once this one is forfeited; false if not taken.
  adopt(tokens: Tokens): boolean {
    const t = this.#tokens;
    if (t === undefined || tokens.iss !== t.iss || tokens.sub !== t.sub) return false;
    if (tokens.expiresAt < t.expiresAt || (tokens.expiresAt === t.expiresAt && !this.#forfeited)) return false;
    this.#tokens = { ...tokens };
    this.#spent = false;
    this.#forfeited = false;
    // A pair without a refresh token comes from a provider that does not rotate (§9.3 item 3).
    this.#byRedirect = tokens.refresh === undefined;
    this.#revocable = tokens.refresh ?? this.#revocable;
    return true;
  }

  // B1: another tab may have sent the refresh token with no answer, so it is never sent from here.
  spend(): void {
    if (this.#tokens !== undefined) this.#tokens.refresh = undefined;
    this.#spent = true;
    this.#byRedirect = false;
  }

  // #388: this tab may have missed another tab's `renewing`, so its refresh token is never sent;
  // the page takes another tab's pair, or renews by the redirect (§9.3 item 3). Not a failed renewal.
  forfeit(): void {
    if (this.#tokens === undefined) return;
    this.#tokens.refresh = undefined;
    this.#spent = true;
    this.#byRedirect = true;
    this.#forfeited = true;
  }

  // §9.5: one renewal at a time. A refresh token is sent at most once: after any failure, its
  // rotation may already have happened, so it is dropped and the current token used until refused
  // (item 3). False also when there is none to send: byRedirect says whether the page renews by
  // the redirect instead.
  renew(): Promise<boolean> {
    this.#renewing ??= this.#renew().finally(() => {
      this.#renewing = undefined;
      this.#inFlight = undefined;
    });
    return this.#renewing;
  }

  async #renew(): Promise<boolean> {
    this.#checkSpent();
    const current = this.#tokens;
    const refresh = current?.refresh;
    if (current === undefined || refresh === undefined) return false;
    current.refresh = undefined;
    // Sign-out's handle goes with the token: the pair it brings replaces it, a failure gives it back.
    this.#revocable = undefined;
    this.#inFlight = refresh;
    // Not sent at all when another tab may not know it is spent (#388), sign-out took it, or the
    // page was cleared or took another pair meanwhile.
    const told = this.opts.onRenewing?.(refresh);
    const announced = (told === undefined || (await told) !== false) && this.#inFlight === refresh && this.#tokens === current;
    const request = announced ? tokenRequest(this.provider, { grant_type: "refresh_token", refresh_token: refresh, client_id: this.config.webClientId }, this.opts.fetch) : undefined;
    // #388: a request unanswered after REFRESH_WAIT_MS fails the renewal; its answer, whenever it
    // comes, is an obsolete renewal's, and its refresh token, which nothing else holds, is revoked.
    let timer: ReturnType<typeof setTimeout> | undefined;
    const late = new Promise<"late">((r) => (timer = setTimeout(() => r("late"), REFRESH_WAIT_MS)));
    const answer = request === undefined ? undefined : await Promise.race([request, late]);
    clearTimeout(timer);
    if (answer === "late" && request !== undefined) {
      const revoked: Promise<void> = request
        .then((a) => (a.kind === "ok" ? this.#revoke(a.body.refresh_token || undefined) : undefined), () => undefined)
        .finally(() => this.#late.delete(revoked));
      this.#late.add(revoked);
    }
    // The sent token, unless sign-out has taken it to revoke.
    const sent = this.#inFlight === refresh ? refresh : undefined;
    if (answer === undefined || answer === "late" || answer.kind !== "ok") {
      // Maybe never delivered, so maybe still valid at the provider (CP2-F2): kept for sign-out
      // while the page holds tokens; on a page cleared or signed out meanwhile, never restored
      // (§10.3 item 3) but revoked, as nothing else would; one never sent is left as it is.
      if (this.#tokens === undefined) await this.#revoke(announced ? sent : undefined);
      else this.#revocable ??= refresh;
      // §9.3 item 3: renewal has failed, so this pair, now without a refresh token, goes to no new
      // tab, which would take it for a provider that does not rotate and renew by the redirect.
      if (this.#tokens === current) this.#spent = true;
      return false;
    }
    // Signed out, cleared or replaced meanwhile: the new pair is not kept, and its refresh token,
    // which nothing else holds, is revoked so that it does not outlive the sign-out; a provider
    // that sent none left the one sent valid (CP2-F2).
    if (this.#tokens !== current) {
      await this.#revoke(answer.body.refresh_token || sent);
      return false;
    }
    const id = typeof answer.body.id_token === "string" ? claims(answer.body.id_token) : undefined;
    let before: Principal = { iss: current.iss, sub: current.sub };
    let after: Principal = { iss: typeof id?.["iss"] === "string" ? id["iss"] : current.iss, sub: typeof id?.["sub"] === "string" ? id["sub"] : current.sub };
    // No ID token in the answer: the two access tokens, which the server reads, say whether the
    // account changed, compared with each other and only when both name an issuer and a subject.
    if (id === undefined) {
      const was = principalOf(current.access);
      const now = principalOf(answer.body.access_token);
      if (was !== undefined && now !== undefined && (was.iss !== now.iss || was.sub !== now.sub)) [before, after] = [was, now];
    }
    this.#tokens = toTokens(answer.body, after, this.now());
    this.#revocable = this.#tokens.refresh ?? refresh;
    // §9.3 items 1 and 3: a refresh token that did not rotate is not sent again ("used once"); the
    // provider does not rotate, so the page renews by the redirect from now on.
    if (this.#tokens.refresh === refresh) this.#tokens.refresh = undefined;
    this.#byRedirect = this.#tokens.refresh === undefined;
    this.opts.onRenewed?.({ ...this.#tokens });
    if (after.iss !== before.iss || after.sub !== before.sub) this.opts.onPrincipalChange?.(before, after);
    return true;
  }

  // §10.3 item 3, §8.2 identity-revoked: forget the tokens; nothing is sent.
  clear(): void {
    this.#tokens = undefined;
    this.#revocable = undefined;
  }

  // §9.7 item 2: forget the tokens, revoke the refresh token where the provider allows it, and
  // return where to go: the provider's end-session endpoint if it has one, else the signed-out page.
  // A renewal in flight revokes the pair it receives, or the token it sent if sign-out did not take
  // it, and a refresh request that timed out the pair its answer brings (#396); the wait for all of
  // them is bounded, so a stalled provider delays the redirect by at most SIGN_OUT_WAIT_MS.
  async signOut(origin: string): Promise<string> {
    await this.end();
    if (this.provider.endSessionEndpoint === undefined) return SIGNED_OUT_PATH;
    const url = new URL(this.provider.endSessionEndpoint);
    url.searchParams.set("client_id", this.config.webClientId);
    url.searchParams.set("post_logout_redirect_uri", origin + SIGNED_OUT_PATH);
    return url.href;
  }

  // §9.7 item 2: sign-out's revocations without its redirect, also after another tab's sign-out,
  // which may not have had this tab's last pair (a renewal answered just before its message, #388).
  async end(): Promise<void> {
    const signedIn = this.#tokens !== undefined;
    // Taken now, since the page clears the session as soon as this returns its promise: the last
    // refresh token held, even one no longer sent for renewal (CP2-F2), else the one a renewal in
    // flight sent, which may never reach the provider or never be answered. It goes at once, so
    // that a renewal that never answers cannot hold it back.
    const held = signedIn ? (this.#revocable ?? this.#inFlight) : undefined;
    if (held !== undefined && held === this.#inFlight) this.#inFlight = undefined;
    this.#tokens = undefined;
    let timer: ReturnType<typeof setTimeout> | undefined;
    // The late answers are read once the renewal in flight has settled, since its own request can
    // time out during this wait and become one of them.
    const renewal = (async () => {
      await this.#renewing?.catch(() => false);
      await Promise.all(this.#late);
    })();
    await Promise.race([
      Promise.all([this.#revoke(held), renewal]),
      new Promise((r) => (timer = setTimeout(r, SIGN_OUT_WAIT_MS))),
    ]);
    clearTimeout(timer);
    this.#revocable = undefined;
  }

  // A token already sent for revocation is not sent again: a renewal that answers after sign-out
  // took its token can bring that same token back unrotated (RFC 7009 §2.2 answers 200 either way).
  async #revoke(token: string | undefined): Promise<void> {
    if (token === undefined || this.#revoked.has(token) || this.provider.revocationEndpoint === undefined) return;
    this.#revoked.add(token);
    if (this.#revocable === token) this.#revocable = undefined;
    try {
      await (this.opts.fetch ?? fetch)(this.provider.revocationEndpoint, post({ token, token_type_hint: "refresh_token", client_id: this.config.webClientId }));
    } catch {
      // Unreachable: the token stays valid at the provider until it expires; nothing to retry here.
    }
  }

  private now(): number {
    return (this.opts.now ?? Date.now)();
  }
}

type TokenBody = { access_token: string; refresh_token?: string; id_token?: string; expires_in: number };

const post = (form: Record<string, string>): RequestInit => ({ ...NO_STORE, method: "POST", body: new URLSearchParams(form) });

async function tokenRequest(provider: Provider, form: Record<string, string>, fetchImpl: typeof fetch = fetch): Promise<{ kind: "ok"; body: TokenBody } | Failure> {
  let res: Response;
  try {
    res = await fetchImpl(provider.tokenEndpoint, post(form));
  } catch {
    return { kind: "transport" };
  }
  const body = await readJson(res);
  if (body === CUT) return { kind: "transport" };
  if (!res.ok) return { kind: "refused", reason: `the identity provider refused the token request (${res.status})` };
  if (body === NOT_JSON || typeof body !== "object" || body === null) return invalid("the token answer is not JSON");
  const b = body as Record<string, unknown>;
  const fine =
    typeof b["access_token"] === "string" && b["access_token"] !== "" && typeof b["expires_in"] === "number" && b["expires_in"] > 0 &&
    (b["refresh_token"] === undefined || typeof b["refresh_token"] === "string");
  return fine ? { kind: "ok", body: body as TokenBody } : invalid("the token answer has no access token or lifetime");
}

function toTokens(body: TokenBody, who: Principal, now: number): Tokens {
  return { ...who, access: body.access_token, refresh: body.refresh_token || undefined, expiresAt: now + body.expires_in * 1000 };
}

function readPending(raw: string | null): Pending | undefined {
  if (raw === null) return undefined;
  try {
    const p = JSON.parse(raw) as Record<string, unknown>;
    const fields = ["verifier", "state", "nonce", "returnTo"];
    return fields.every((f) => typeof p[f] === "string") ? (p as unknown as Pending) : undefined;
  } catch {
    return undefined;
  }
}

// §9.6: what this page can read in a token the server refused, never the server's reason (it says
// only 401). Undefined when nothing can be read safely.
export function localReading(access: string, config: ClientConfig, now: number): string | undefined {
  const c = claims(access);
  if (c === undefined) return undefined;
  if (typeof c["iss"] === "string" && c["iss"] !== config.issuer) return `the token is from ${c["iss"]}; this page expects ${config.issuer}`;
  const aud = Array.isArray(c["aud"]) ? c["aud"] : [c["aud"]];
  if (!aud.includes(config.audience)) return `the token is not issued for ${config.audience}; the identity provider needs an audience mapping`;
  // An expiry out of Date's range reads as nothing rather than throwing.
  const exp = typeof c["exp"] === "number" ? new Date(c["exp"] * 1000) : undefined;
  if (exp !== undefined && !Number.isNaN(exp.getTime()) && exp.getTime() <= now) return `the token expired at ${exp.toISOString()}`;
  return undefined;
}

// A JWT's claims, read without verification; undefined when it does not decode.
export function claims(jwt: string): Record<string, unknown> | undefined {
  const part = jwt.split(".")[1];
  if (part === undefined) return undefined;
  try {
    const bytes = Uint8Array.from(atob(part.replace(/-/g, "+").replace(/_/g, "/")), (c) => c.charCodeAt(0));
    const value: unknown = JSON.parse(new TextDecoder().decode(bytes));
    return typeof value === "object" && value !== null && !Array.isArray(value) ? (value as Record<string, unknown>) : undefined;
  } catch {
    return undefined;
  }
}

// The issuer and subject a token names; undefined unless it names both as strings.
function principalOf(jwt: string): Principal | undefined {
  const c = claims(jwt);
  return typeof c?.["iss"] === "string" && typeof c["sub"] === "string" ? { iss: c["iss"], sub: c["sub"] } : undefined;
}

function base64url(bytes: Uint8Array): string {
  let s = "";
  for (const b of bytes) s += String.fromCharCode(b);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

const random = (n: number) => base64url(crypto.getRandomValues(new Uint8Array(n)));
