import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { type Provider, Session, type Tokens } from "./auth.ts";
import type { ClientConfig } from "./config.ts";
import { JOIN_WAIT_MS, LOCK, type Locks, type Message, Renewer, RENEW_WAIT_MS } from "./renewer.ts";

const ISSUER = "http://localhost:58280";
const config: ClientConfig = { issuer: ISSUER, audience: "bronzeward", cliClientId: "cli", webClientId: "web", scopes: [] };
const provider: Provider = { issuer: ISSUER, authorizationEndpoint: `${ISSUER}/a`, tokenEndpoint: `${ISSUER}/t`, revocationEndpoint: undefined, endSessionEndpoint: undefined };
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

// One exclusive lock, granted in request order, as navigator.locks grants it (assumption S9).
class FakeLocks implements Locks {
  held = false;
  readonly queue: (() => void)[] = [];
  request(_name: string, callback: () => Promise<void> | undefined): Promise<unknown> {
    return new Promise((done) => {
      const run = () => {
        this.held = true;
        void Promise.resolve(callback()).then(() => {
          this.held = false;
          done(undefined);
          this.queue.shift()?.();
        });
      };
      if (this.held) this.queue.push(run);
      else run();
    });
  }
  query() {
    return Promise.resolve({ held: this.held ? [{ name: LOCK }] : [] });
  }
}

// A browser of tabs on one origin, and a provider that rotates: only the latest refresh token it
// issued is accepted.
class Browser {
  readonly locks = new FakeLocks();
  readonly tabs = new Set<Tab>();
  readonly sent: string[] = [];
  latest = "SYNTH-r1";
  hang = false;
  // Answers wait for this, when set.
  delay: Promise<void> | undefined;
  #n = 1;
  readonly fetch = (async (_url: RequestInfo | URL, init?: RequestInit) => {
    const refresh = new URLSearchParams(init?.body as URLSearchParams).get("refresh_token") ?? "";
    this.sent.push(refresh);
    if (this.hang) return new Promise<Response>(() => {});
    await this.delay;
    if (refresh !== this.latest) return json({ error: "invalid_grant" }, 400);
    this.#n++;
    this.latest = `SYNTH-r${this.#n}`;
    return json({ access_token: `SYNTH-a${this.#n}`, refresh_token: this.latest, expires_in: 300 });
  }) as typeof fetch;

  open(): Tab {
    const tab = new Tab(this);
    this.tabs.add(tab);
    return tab;
  }
}

class Tab {
  session: Session | undefined;
  readonly renewer: Renewer;
  readonly pairs: Tokens[] = [];

  constructor(private readonly browser: Browser) {
    const channel = {
      postMessage: (m: Message) => {
        const copy = structuredClone(m);
        queueMicrotask(() => {
          for (const t of browser.tabs) if (t !== this) t.renewer.onMessage(copy);
        });
      },
    };
    this.renewer = new Renewer(channel, browser.locks, () => this.session, (t) => {
      this.pairs.push(t);
      this.session?.adopt(t);
    });
  }

  signIn(tokens: Tokens) {
    this.session = new Session(config, provider, tokens, { fetch: this.browser.fetch, ...this.renewer.hooks });
    this.renewer.hold();
  }

  async join(): Promise<Tokens | undefined> {
    const t = await this.renewer.join();
    if (t !== undefined) this.signIn(t);
    return t;
  }

  close() {
    this.browser.tabs.delete(this);
    this.renewer.release();
    this.session = undefined;
  }
}

const first = (over: Partial<Tokens> = {}): Tokens => ({ iss: ISSUER, sub: "u1", access: "SYNTH-a1", refresh: "SYNTH-r1", expiresAt: Date.now() + 300_000, ...over });
const settle = () => vi.advanceTimersByTimeAsync(0);

beforeEach(() => {
  vi.useFakeTimers();
});
afterEach(() => {
  vi.useRealTimers();
});

describe("B1 one renewing tab", () => {
  test("a first tab finds no holder and redirects at once; a new tab takes the holder's pair", async () => {
    const b = new Browser();
    expect(await b.open().join()).toBeUndefined();
    const a = b.open();
    a.signIn(first());
    await settle();
    const c = b.open();
    const joined = c.join();
    await settle();
    expect((await joined)?.access).toBe("SYNTH-a1");
    expect(b.sent).toEqual([]);
  });

  test("a new tab redirects when the holder does not answer in 5 s", async () => {
    const b = new Browser();
    b.open().renewer.hold();
    await settle();
    const joined = b.open().renewer.join();
    await vi.advanceTimersByTimeAsync(JOIN_WAIT_MS);
    expect(await joined).toBeUndefined();
  });

  test("AUTH-11: three tabs at the threshold send one refresh request per renewal, and every tab takes each pair", async () => {
    const b = new Browser();
    const [x, y, z] = [b.open(), b.open(), b.open()];
    x.signIn(first());
    await settle();
    await y.join();
    await z.join();
    await vi.advanceTimersByTimeAsync(240_000);
    const renewed = await Promise.all([x, y, z].map((t) => t.renewer.renew()));
    await settle();
    expect(renewed).toEqual([true, true, true]);
    expect(b.sent).toEqual(["SYNTH-r1"]);
    expect([x, y, z].map((t) => t.session?.token())).toEqual(["SYNTH-a2", "SYNTH-a2", "SYNTH-a2"]);
    // Each tab that becomes holder sends the latest refresh token, so it had every pair.
    x.close();
    await vi.advanceTimersByTimeAsync(240_000);
    expect(await y.renewer.renew()).toBe(true);
    await settle();
    y.close();
    await vi.advanceTimersByTimeAsync(240_000);
    expect(await z.renewer.renew()).toBe(true);
    expect(b.sent).toEqual(["SYNTH-r1", "SYNTH-r2", "SYNTH-r3"]);
  });

  test("AUTH-11: a holder that closes after renewing leaves the refresh token spent; a redirect's pair reaches every tab", async () => {
    const b = new Browser();
    const [x, y, z] = [b.open(), b.open(), b.open()];
    x.signIn(first());
    await settle();
    await y.join();
    await z.join();
    b.hang = true;
    void x.renewer.renew();
    await settle();
    // No pair goes to a new tab while the renewal is in flight: it has no refresh token yet.
    const late = b.open();
    const joined = late.renewer.join();
    await settle();
    b.tabs.delete(late);
    x.close();
    await settle();
    b.hang = false;
    expect(await y.renewer.renew()).toBe(false);
    const asked = z.renewer.renew();
    await vi.advanceTimersByTimeAsync(RENEW_WAIT_MS);
    expect(await asked).toBe(false);
    expect(await joined).toBeUndefined();
    expect(b.sent).toEqual(["SYNTH-r1"]);
    expect([y, z].map((t) => t.session?.token())).toEqual(["SYNTH-a1", "SYNTH-a1"]);
    // §9.5 item 3: no pair for a new tab either, so it signs in again, and posts what it got.
    const again = b.open();
    const none = again.renewer.join();
    await vi.advanceTimersByTimeAsync(JOIN_WAIT_MS);
    expect(await none).toBeUndefined();
    b.latest = "SYNTH-r9";
    const redirect = first({ access: "SYNTH-a9", refresh: "SYNTH-r9", expiresAt: Date.now() + 300_000 });
    again.renewer.post(redirect);
    again.signIn(redirect);
    await settle();
    expect([y, z].map((t) => t.session?.token())).toEqual(["SYNTH-a9", "SYNTH-a9"]);
    expect(await y.renewer.renew()).toBe(true);
    expect(b.sent).toEqual(["SYNTH-r1", "SYNTH-r9"]);
  });

  test("a tab that joins during the holder's renewal takes the pair it brings, never the one being renewed", async () => {
    const b = new Browser();
    const x = b.open();
    x.signIn(first());
    await settle();
    let answer!: () => void;
    b.delay = new Promise((r) => (answer = r));
    const renewed = x.renewer.renew();
    await settle();
    const late = b.open();
    const joined = late.join();
    await settle();
    expect(late.session).toBeUndefined();
    answer();
    expect(await renewed).toBe(true);
    const t = await joined;
    expect([t?.access, t?.refresh]).toEqual(["SYNTH-a2", "SYNTH-r2"]);
    expect(late.session?.byRedirect).toBe(false);
  });

  test("a renewal in flight when the page is refused still gives its pair to the other tabs, whose refresh token it spent", async () => {
    const b = new Browser();
    const [x, y] = [b.open(), b.open()];
    x.signIn(first());
    await settle();
    await y.join();
    await vi.advanceTimersByTimeAsync(1_000);
    let answer!: () => void;
    b.delay = new Promise((r) => (answer = r));
    const renewed = x.renewer.renew();
    await settle();
    // Refused (W1): the page stops renewing and lets the lock go, and keeps its session.
    x.renewer.release();
    await settle();
    answer();
    expect(await renewed).toBe(true);
    await settle();
    expect(y.session?.token()).toBe("SYNTH-a2");
    expect(await y.renewer.renew()).toBe(true);
    expect(b.sent).toEqual(["SYNTH-r1", "SYNTH-r2"]);
  });

  test("a tab that signs in by the redirect while another tab's renewal is unanswered keeps its own refresh token", async () => {
    const b = new Browser();
    const [x, c] = [b.open(), b.open()];
    x.signIn(first());
    await settle();
    b.hang = true;
    void x.renewer.renew();
    await settle();
    // c, at its callback, got its own pair after the `renewing`, and posts it.
    b.latest = "SYNTH-r9";
    const own = first({ access: "SYNTH-a9", refresh: "SYNTH-r9", expiresAt: Date.now() + 600_000 });
    c.signIn(own);
    c.renewer.post(own);
    x.close();
    await settle();
    b.hang = false;
    expect(c.renewer.holder).toBe(true);
    expect(await c.renewer.renew()).toBe(true);
    expect(b.sent).toEqual(["SYNTH-r1", "SYNTH-r9"]);
  });

  test("a tab released while its join awaits the lock query never takes the lock", async () => {
    const b = new Browser();
    const x = b.open();
    x.signIn(first());
    await settle();
    const t = b.open();
    const joined = t.renewer.join();
    t.close();
    expect(await joined).toBeUndefined();
    x.close();
    await settle();
    const y = b.open();
    y.renewer.hold();
    await settle();
    expect(t.renewer.holder).toBe(false);
    expect(y.renewer.holder).toBe(true);
  });

  test("a new tab granted the lock before a pair arrives redirects at once, as after a reload", async () => {
    const b = new Browser();
    const old = b.open();
    old.renewer.hold();
    await settle();
    const joined = b.open().renewer.join();
    await settle();
    old.close();
    await settle();
    expect(await joined).toBeUndefined();
    expect(vi.getTimerCount()).toBe(0);
  });

  test("a tab granted the lock while it waits on the holder renews itself", async () => {
    const b = new Browser();
    const [x, y] = [b.open(), b.open()];
    x.signIn(first());
    await settle();
    await y.join();
    x.session = undefined;
    const asked = y.renewer.renew();
    await settle();
    x.close();
    expect(await asked).toBe(true);
    expect(b.sent).toEqual(["SYNTH-r1"]);
    expect(y.session?.token()).toBe("SYNTH-a2");
  });

  test("a tab waiting on the holder fails after 10 s when the holder's renewal is refused", async () => {
    const b = new Browser();
    const [x, y] = [b.open(), b.open()];
    x.signIn(first({ refresh: "SYNTH-old" }));
    await settle();
    await y.join();
    const asked = y.renewer.renew();
    await vi.advanceTimersByTimeAsync(RENEW_WAIT_MS);
    expect(await asked).toBe(false);
    expect(b.sent).toEqual(["SYNTH-old"]);
  });

  test("§9.5 item 2: a pair for another subject fails the waiting renewal and goes to onPair", async () => {
    const b = new Browser();
    const [x, y] = [b.open(), b.open()];
    x.signIn(first());
    await settle();
    await y.join();
    const asked = y.renewer.renew();
    await settle();
    x.renewer.post(first({ sub: "u2", expiresAt: Date.now() + 1 }));
    await settle();
    expect(await asked).toBe(false);
    expect(y.pairs.at(-1)?.sub).toBe("u2");
    expect(y.session?.principal?.sub).toBe("u1");
  });

  test("a released tab gives the lock to the next one, and one released before the grant never holds it", async () => {
    const b = new Browser();
    const [x, y, z] = [b.open(), b.open(), b.open()];
    x.signIn(first());
    y.renewer.hold();
    z.renewer.hold();
    y.renewer.release();
    x.close();
    await settle();
    z.session = new Session(config, provider, first(), { fetch: b.fetch, ...z.renewer.hooks });
    expect(await z.renewer.renew()).toBe(true);
    expect(b.sent).toEqual(["SYNTH-r1"]);
  });
});
