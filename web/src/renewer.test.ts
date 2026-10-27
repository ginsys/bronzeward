import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { type Provider, Session, type Tokens } from "./auth.ts";
import type { ClientConfig } from "./config.ts";
import { ACK_WAIT_MS, type Channel, JOIN_WAIT_MS, type Locks, type Message, REDIRECT_WAIT_MS, Renewer, RENEW_WAIT_MS, TAB } from "./renewer.ts";

const ISSUER = "http://localhost:58280";
const config: ClientConfig = { issuer: ISSUER, audience: "bronzeward", cliClientId: "cli", webClientId: "web", scopes: [] };
const provider: Provider = { issuer: ISSUER, authorizationEndpoint: `${ISSUER}/a`, tokenEndpoint: `${ISSUER}/t`, revocationEndpoint: `${ISSUER}/r`, endSessionEndpoint: undefined };
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

// Exclusive locks by name, each granted in request order, as navigator.locks grants them
// (assumption S9). A steal grants at once and rejects the stolen holder's request.
class FakeLocks implements Locks {
  readonly #held = new Map<string, (e: Error) => void>();
  readonly #queue = new Map<string, (() => void)[]>();
  request(name: string, options: { steal?: boolean }, callback: () => Promise<void> | undefined): Promise<unknown> {
    return new Promise((done, fail) => {
      const run = () => {
        const stolen = (e: Error) => fail(e);
        this.#held.set(name, stolen);
        void Promise.resolve(callback()).then(() => {
          if (this.#held.get(name) !== stolen) return;
          this.#held.delete(name);
          done(undefined);
          this.#queue.get(name)?.shift()?.();
        });
      };
      const holder = this.#held.get(name);
      if (options.steal === true) {
        holder?.(new DOMException("stolen", "AbortError"));
        run();
      } else if (holder !== undefined) this.#queue.set(name, [...(this.#queue.get(name) ?? []), run]);
      else run();
    });
  }
  query() {
    return Promise.resolve({ held: [...this.#held.keys()].map((name) => ({ name })) });
  }
}

// A browser of tabs on one origin, and a provider that rotates: only the latest refresh token it
// issued is accepted.
class Browser {
  readonly locks = new FakeLocks();
  // Every open channel, and the tab it belongs to. A message reaches the channels open when it
  // was posted that are still open when it is delivered, the sender's excepted.
  readonly channels = new Map<Channel, Tab>();
  readonly sent: string[] = [];
  readonly revoked: string[] = [];
  latest = "SYNTH-r1";
  hang = false;
  // Answers wait for this, when set.
  delay: Promise<void> | undefined;
  // The access-token lifetime of each answer, in seconds.
  lifetime = 300;
  #n = 1;
  readonly fetch = (async (url: RequestInfo | URL, init?: RequestInit) => {
    const form = new URLSearchParams(init?.body as URLSearchParams);
    if (url === provider.revocationEndpoint) {
      this.revoked.push(form.get("token") ?? "");
      return new Response(null, { status: 200 });
    }
    const refresh = form.get("refresh_token") ?? "";
    this.sent.push(refresh);
    if (this.hang) return new Promise<Response>(() => {});
    await this.delay;
    if (refresh !== this.latest) return json({ error: "invalid_grant" }, 400);
    this.#n++;
    this.latest = `SYNTH-r${this.#n}`;
    return json({ access_token: `SYNTH-a${this.#n}`, refresh_token: this.latest, expires_in: this.lifetime });
  }) as typeof fetch;

  open(): Tab {
    return new Tab(this);
  }

  channel(tab: Tab): Channel {
    const c: Channel = {
      onmessage: null,
      // Serialized at the post, and deserialized for each destination.
      postMessage: (m: Message) => {
        const sent = structuredClone(m);
        const to = [...this.channels].filter(([o]) => o !== c);
        queueMicrotask(() => {
          for (const [o, t] of to) if (this.channels.has(o)) t.deliver(o, structuredClone(sent));
        });
      },
      close: () => void this.channels.delete(c),
    };
    this.channels.set(c, tab);
    return c;
  }
}

class Tab {
  session: Session | undefined;
  readonly renewer: Renewer;
  readonly pairs: Tokens[] = [];
  signedOut = 0;
  // Settled once the session has revoked what it held after another tab's sign-out.
  ended: Promise<void> | undefined;
  // Called at each of this tab's lock queries, before it is answered.
  onQuery: (() => void) | undefined;
  // Frozen: messages wait, as do the settlements of its lock requests, until resume().
  #frozen: Promise<void> | undefined;
  #thaw = () => {};
  readonly #queued: [Channel, Message][] = [];

  constructor(private readonly browser: Browser) {
    const locks: Locks = {
      request: (name, options, callback) => this.#awake(browser.locks.request(name, options, callback)),
      query: () => {
        const held = browser.locks.query();
        this.onQuery?.();
        return held;
      },
    };
    this.renewer = new Renewer(() => browser.channel(this), locks, () => this.session, (t) => {
      this.pairs.push(t);
      this.session?.adopt(t);
    }, (ended) => {
      this.signedOut++;
      this.ended = ended;
    });
  }

  deliver(c: Channel, m: Message) {
    if (this.#frozen !== undefined) this.#queued.push([c, m]);
    else c.onmessage?.({ data: m } as MessageEvent);
  }

  freeze() {
    this.#frozen = new Promise((r) => (this.#thaw = r));
  }

  // The messages first, then the lock settlements.
  resume() {
    this.#frozen = undefined;
    for (const [c, m] of this.#queued.splice(0)) if (this.browser.channels.has(c)) this.deliver(c, m);
    this.#thaw();
  }

  // The other way round: they are separate task sources, in no defined order. Only the first
  // `messages` are delivered; the test ends before the others arrive.
  async resumeLocksFirst(messages = Infinity) {
    this.#thaw();
    await settle();
    this.#queued.splice(messages);
    this.resume();
  }

  #awake<T>(p: Promise<T>): Promise<T> {
    return p.then(
      async (v) => {
        await this.#frozen;
        return v;
      },
      async (e: unknown) => {
        await this.#frozen;
        throw e;
      },
    );
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
    this.renewer.close();
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
    late.close();
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

  test("a refresh token another tab sent is never sent or given again, though the provider shortened the lifetime of the pairs after it", async () => {
    const b = new Browser();
    const [x, y] = [b.open(), b.open()];
    x.signIn(first());
    await settle();
    await y.join();
    // Each pair x obtains expires before y's, so y keeps its pair and the spent SYNTH-r1.
    b.lifetime = 100;
    expect(await x.renewer.renew()).toBe(true);
    await settle();
    b.lifetime = 110;
    expect(await x.renewer.renew()).toBe(true);
    await settle();
    expect(y.session?.token()).toBe("SYNTH-a1");
    x.close();
    await settle();
    expect(y.renewer.holder).toBe(true);
    const late = b.open();
    const joined = late.renewer.join();
    await vi.advanceTimersByTimeAsync(JOIN_WAIT_MS);
    expect(await joined).toBeUndefined();
    expect(await y.renewer.renew()).toBe(false);
    expect(b.sent).toEqual(["SYNTH-r1", "SYNTH-r2"]);
  });

  test("a released tab sends and gives no refresh token, announced or not", async () => {
    const b = new Browser();
    const [x, y] = [b.open(), b.open()];
    x.signIn(first());
    await settle();
    await y.join();
    expect(await x.renewer.renew()).toBe(true);
    await settle();
    expect([y.renewer.hooks.spent?.("SYNTH-r1"), y.renewer.hooks.spent?.("SYNTH-r2")]).toEqual([true, false]);
    y.renewer.release();
    expect(y.renewer.hooks.spent?.("SYNTH-r2")).toBe(true);
    expect(y.session?.pair()).toBeUndefined();
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

// #388: no tab that may hold a refresh token becomes holder without knowing it is spent.
describe("#388 announced renewals", () => {
  const tabLocks = async (b: Browser) => (await b.locks.query()).held.filter((l) => l.name.startsWith(TAB)).length;

  test("the holder sends a refresh token only once every other tab has recorded it as spent", async () => {
    const b = new Browser();
    const [x, y] = [b.open(), b.open()];
    x.signIn(first());
    await settle();
    await y.join();
    y.freeze();
    const renewed = x.renewer.renew();
    await vi.advanceTimersByTimeAsync(ACK_WAIT_MS - 1);
    expect(b.sent).toEqual([]);
    y.resume();
    expect(await renewed).toBe(true);
    await settle();
    expect(b.sent).toEqual(["SYNTH-r1"]);
    expect(y.renewer.hooks.spent?.("SYNTH-r1")).toBe(true);
    expect(y.session?.pair()?.refresh).toBe("SYNTH-r2");
  });

  test("a holder that leaves before every tab has acked sends nothing and takes no lock", async () => {
    const b = new Browser();
    const [x, y] = [b.open(), b.open()];
    x.signIn(first());
    await settle();
    await y.join();
    y.freeze();
    const renewed = x.renewer.renew();
    await settle();
    x.close();
    await vi.advanceTimersByTimeAsync(ACK_WAIT_MS);
    expect(await renewed).toBe(false);
    expect(b.sent).toEqual([]);
    expect(await tabLocks(b)).toBe(1);
  });

  test("a silent tab's lock is taken after ACK_WAIT_MS; on resume it gives up its pair and takes the holder's", async () => {
    const b = new Browser();
    const [x, y] = [b.open(), b.open()];
    x.signIn(first());
    await settle();
    await y.join();
    y.freeze();
    const renewed = x.renewer.renew();
    await vi.advanceTimersByTimeAsync(ACK_WAIT_MS);
    expect(await renewed).toBe(true);
    expect(b.sent).toEqual(["SYNTH-r1"]);
    y.resume();
    await settle();
    expect(y.session?.pair()?.refresh).toBe("SYNTH-r2");
    expect(await tabLocks(b)).toBe(2);
    x.close();
    await settle();
    expect(await y.renewer.renew()).toBe(true);
    expect(b.sent).toEqual(["SYNTH-r1", "SYNTH-r2"]);
  });

  test("a resumed tab that renews before it learns its lock was taken sends nothing, and renews by the redirect", async () => {
    const b = new Browser();
    const [x, y] = [b.open(), b.open()];
    x.signIn(first());
    await settle();
    await y.join();
    y.freeze();
    void x.renewer.renew();
    await vi.advanceTimersByTimeAsync(ACK_WAIT_MS);
    x.close();
    await settle();
    // Its renewal timer runs first on resume; the queued messages and the stolen lock come after.
    expect(y.renewer.holder).toBe(true);
    expect(await y.renewer.renew()).toBe(false);
    expect(b.sent).toEqual(["SYNTH-r1"]);
    expect(y.session?.byRedirect).toBe(true);
    y.resume();
    await settle();
    expect(await tabLocks(b)).toBe(1);
  });

  test("a holder released while its last lock query is answered sends nothing", async () => {
    const b = new Browser();
    const [x, y] = [b.open(), b.open()];
    x.signIn(first());
    await settle();
    await y.join();
    let queries = 0;
    // Another tab's sign-out reaches x between its own-lock query and the answer.
    x.onQuery = () => void (++queries === 2 && x.renewer.release());
    expect(await x.renewer.renew()).toBe(false);
    expect(queries).toBe(2);
    expect(b.sent).toEqual([]);
  });

  test("a released tab is not waited on, and still hears another tab's sign-out", async () => {
    const b = new Browser();
    const [x, y] = [b.open(), b.open()];
    x.signIn(first());
    await settle();
    await y.join();
    y.renewer.release();
    expect(await x.renewer.renew()).toBe(true);
    expect(vi.getTimerCount()).toBe(0);
    x.renewer.signOut();
    await settle();
    expect(y.signedOut).toBe(1);
  });

  test.each([
    ["while it asks, takes the redirect's pair", true, true],
    ["while it asks, renews by the redirect itself after REDIRECT_WAIT_MS", true, false],
    ["before its own threshold, takes the redirect's pair", false, true],
    ["before its own threshold, renews by the redirect itself after REDIRECT_WAIT_MS", false, false],
  ] as const)("granted the lock %s", async (_name, asking, back) => {
    const b = new Browser();
    const [x, y] = [b.open(), b.open()];
    x.signIn(first({ refresh: undefined }));
    await settle();
    await y.join();
    expect(y.session?.byRedirect).toBe(true);
    const asked = asking ? y.renewer.renew() : undefined;
    await settle();
    // The holder renews by the redirect: `redirecting`, pagehide, then the callback posts the pair.
    x.renewer.redirecting();
    x.close();
    await settle();
    const renewed = asked ?? y.renewer.renew();
    let answered = false;
    void renewed.then(() => (answered = true));
    await vi.advanceTimersByTimeAsync(REDIRECT_WAIT_MS - 1);
    expect(y.renewer.holder).toBe(true);
    // Still waiting for the redirect's pair: no second redirect yet.
    expect(answered).toBe(false);
    if (back) {
      const pair = first({ access: "SYNTH-a9", refresh: undefined, expiresAt: Date.now() + 300_000 });
      b.open().renewer.post(pair);
    }
    await vi.advanceTimersByTimeAsync(1);
    expect(await renewed).toBe(back);
    expect(y.session?.token()).toBe(back ? "SYNTH-a9" : "SYNTH-a1");
    expect(b.sent).toEqual([]);
  });

  test("a holder renewed just before another tab's sign-out revokes the pair it holds, which that tab never had", async () => {
    const b = new Browser();
    const [x, y] = [b.open(), b.open()];
    x.signIn(first());
    await settle();
    await y.join();
    y.freeze();
    const renewed = x.renewer.renew();
    await vi.advanceTimersByTimeAsync(ACK_WAIT_MS);
    expect(await renewed).toBe(true);
    // y, frozen, has not had the new pair: its sign-out revokes the token it last held.
    const out = y.session?.signOut("http://localhost");
    y.renewer.signOut();
    await settle();
    expect(x.signedOut).toBe(1);
    await x.ended;
    await out;
    expect(b.revoked.sort()).toEqual(["SYNTH-r1", "SYNTH-r2"]);
    expect(x.session?.token()).toBeUndefined();
  });

  test("a sign-out is acted on once: another tab's after this tab's own, or a second one, changes nothing", async () => {
    const b = new Browser();
    const [x, y, z] = [b.open(), b.open(), b.open()];
    x.signIn(first());
    await settle();
    await y.join();
    await z.join();
    y.renewer.signOut();
    z.renewer.signOut();
    await settle();
    expect([x.signedOut, y.signedOut, z.signedOut]).toEqual([1, 0, 0]);
  });

  test("a resumed tab whose lock was taken still hears a sign-out posted while it was frozen, its lock's loss handled first", async () => {
    const b = new Browser();
    const [x, y, z] = [b.open(), b.open(), b.open()];
    x.signIn(first());
    await settle();
    await y.join();
    await z.join();
    y.freeze();
    const renewed = x.renewer.renew();
    await vi.advanceTimersByTimeAsync(ACK_WAIT_MS);
    expect(await renewed).toBe(true);
    z.renewer.signOut();
    await settle();
    await y.resumeLocksFirst();
    await settle();
    expect(y.signedOut).toBe(1);
  });

  // The holder sent r2 without waiting for y, whose tab lock it had taken: the pair bringing r2,
  // posted before y rejoined, must not let y send r2 before the `renewing` naming it arrives.
  test("a rejoined tab takes no pair posted before it rejoined", async () => {
    const b = new Browser();
    const [x, y] = [b.open(), b.open()];
    x.signIn(first());
    await settle();
    await y.join();
    y.freeze();
    const one = x.renewer.renew();
    await vi.advanceTimersByTimeAsync(ACK_WAIT_MS);
    expect(await one).toBe(true);
    expect(await x.renewer.renew()).toBe(true);
    expect(b.sent).toEqual(["SYNTH-r1", "SYNTH-r2"]);
    x.close();
    // Queued at y: renewing r1, pair r2, renewing r2, pair r3. Its lock's loss first, then two.
    await y.resumeLocksFirst(2);
    await settle();
    expect(y.renewer.holder).toBe(true);
    await y.renewer.renew();
    expect(b.sent).toEqual(["SYNTH-r1", "SYNTH-r2"]);
  });

  test("a tab released before its tab lock is granted does not wait for its channel", async () => {
    const b = new Browser();
    let grant = () => {};
    const locks: Locks = {
      request: (name, options, callback) =>
        name.startsWith(TAB) ? new Promise((done) => (grant = () => void Promise.resolve(callback()).then(done))) : b.locks.request(name, options, callback),
      query: () => b.locks.query(),
    };
    const r = new Renewer(() => {
      throw new Error("no channel for a released tab");
    }, locks, () => undefined, () => {});
    let joined = false;
    void r.join().then(() => (joined = true));
    r.release();
    grant();
    await settle();
    expect(joined).toBe(true);
  });
});
