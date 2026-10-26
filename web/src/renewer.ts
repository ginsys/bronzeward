// One renewing tab (decision B1 of ginsys/bronzeward#255, client contracts §9.3 item 3): the holder
// of the Web Lock `bronzeward-renewer` alone sends refresh tokens; every other tab of the origin
// asks it over the BroadcastChannel and takes the pair it posts.

import type { Session, SessionOptions, Tokens } from "./auth.ts";

export const LOCK = "bronzeward-renewer";
// B1: how long a tab waits for the holder's pair when renewing, and as a new tab.
export const RENEW_WAIT_MS = 10_000;
export const JOIN_WAIT_MS = 5_000;

export type Message =
  | { type: "signed-out" }
  // `of`: the refresh token the holder is sending, spent wherever else it is held.
  | { type: "renewing"; of: string }
  // `due`: the expiry of the sender's pair, which reached the threshold or was refused; absent
  // from a new tab, which takes the current pair.
  | { type: "request"; due?: number }
  | { type: "pair"; tokens: Tokens };

export interface Channel {
  postMessage(message: Message): void;
}

export interface Locks {
  request(name: string, callback: () => Promise<void> | undefined): Promise<unknown>;
  query(): Promise<{ held?: { name?: string }[] }>;
}

export class Renewer {
  #holder = false;
  #release: (() => void) | undefined;
  // Released for good: a page that stops renewing never holds the lock again (no tab re-signs in
  // without a navigation), and a join still awaiting its query must not take it afterwards.
  #closed = false;
  // Every refresh token another tab announced in a `renewing`: whatever its answer, it may be spent,
  // so it is never sent or given from here. Told by token, not by expiry, so that a pair with a
  // shorter lifetime cannot hide it.
  readonly #sent = new Set<string>();
  readonly #waiting = new Set<(t: Tokens) => void>();
  readonly #granted = new Set<() => void>();

  constructor(
    private readonly channel: Channel,
    private readonly locks: Locks,
    private readonly session: () => Session | undefined,
    // A pair from another tab, after any waiting renewal has taken it.
    private readonly onPair: (t: Tokens) => void,
  ) {}

  // This tab holds the lock now: it alone renews, and by the redirect where §9.3 item 3 says so.
  get holder(): boolean {
    return this.#holder;
  }

  // What a Session tells the other tabs.
  readonly hooks: Pick<SessionOptions, "onRenewing" | "onRenewed" | "spent"> = {
    onRenewing: (of) => this.channel.postMessage({ type: "renewing", of }),
    onRenewed: (tokens) => this.channel.postMessage({ type: "pair", tokens }),
    spent: (refresh) => this.#sent.has(refresh),
  };

  // A new tab, or one restored from the back-forward cache: the holder's pair, or undefined when
  // there is no holder, it does not answer in time or leaves first, and the page redirects. The
  // lock is requested at once: granted before a pair, no tab that asked before this one is left.
  async join(): Promise<Tokens | undefined> {
    const { held } = await this.locks.query();
    if (this.#closed || !held?.some((l) => l.name === LOCK)) return undefined;
    this.hold();
    const t = await this.#wait(JOIN_WAIT_MS, () => true, { type: "request" });
    return t === "holder" ? undefined : t;
  }

  // A pair obtained by a redirect goes to every tab.
  post(tokens: Tokens): void {
    this.channel.postMessage({ type: "pair", tokens });
  }

  // Signed in: wait for the lock and keep it until release().
  hold(): void {
    if (this.#closed || this.#release !== undefined) return;
    let released = false;
    let end!: () => void;
    const held = new Promise<void>((r) => (end = r));
    this.#release = () => {
      released = true;
      this.#holder = false;
      end();
    };
    void this.locks.request(LOCK, () => {
      if (released) return undefined;
      this.#holder = true;
      for (const g of this.#granted) g();
      return held;
    });
  }

  // On pagehide, sign-out or when the tokens are gone, so that another tab can renew.
  release(): void {
    this.#closed = true;
    this.#release?.();
    this.#release = undefined;
  }

  // §9.5 item 1: the holder renews; another tab asks it and waits for a newer pair, at most 10 s,
  // or fails as item 3, unless the lock comes to it first. False also when the pair is another
  // principal's (item 2, onPair's).
  async renew(): Promise<boolean> {
    const s = this.session();
    const before = s?.expiresAt;
    const who = s?.principal;
    if (s === undefined || before === undefined || who === undefined) return false;
    if (this.#holder) return s.renew();
    const t = await this.#wait(RENEW_WAIT_MS, (p) => p.expiresAt > before || p.iss !== who.iss || p.sub !== who.sub, { type: "request", due: before });
    if (this.session() !== s || t === undefined) return false;
    return t === "holder" ? s.renew() : t.iss === who.iss && t.sub === who.sub;
  }

  onMessage(m: Message): void {
    if (m.type === "renewing" && typeof m.of === "string") this.#sent.add(m.of);
    if (m.type === "request") void this.#answer(typeof m.due === "number" ? m.due : undefined);
    if (m.type !== "pair" || !isTokens(m.tokens)) return;
    for (const w of this.#waiting) w(m.tokens);
    this.onPair(m.tokens);
  }

  // The holder renews for a tab whose pair is its own and due, and posts its pair to any other;
  // the renewal posts its own pair, and nothing goes out when it fails or the token may be spent.
  async #answer(due: number | undefined): Promise<void> {
    const s = this.session();
    if (!this.#holder || s === undefined) return;
    const at = s.expiresAt;
    if (due !== undefined && at !== undefined && due >= at) {
      await s.renew();
      return;
    }
    const pair = s.pair();
    if (pair !== undefined) this.post(pair);
  }

  // A pair `accept` takes, "holder" once this tab is granted the lock, or undefined after `ms`.
  #wait(ms: number, accept: (t: Tokens) => boolean, request: Message): Promise<Tokens | "holder" | undefined> {
    return new Promise((resolve) => {
      const done = (t: Tokens | "holder" | undefined) => {
        clearTimeout(timer);
        this.#waiting.delete(take);
        this.#granted.delete(granted);
        resolve(t);
      };
      const take = (t: Tokens) => accept(t) && done(t);
      const granted = () => done("holder");
      const timer = setTimeout(() => done(undefined), ms);
      this.#waiting.add(take);
      this.#granted.add(granted);
      this.channel.postMessage(request);
    });
  }
}

function isTokens(t: unknown): t is Tokens {
  const v = t as Record<string, unknown> | null;
  return typeof v === "object" && v !== null && typeof v["iss"] === "string" && typeof v["sub"] === "string" && typeof v["access"] === "string" &&
    (v["refresh"] === undefined || typeof v["refresh"] === "string") && typeof v["expiresAt"] === "number";
}
