// One renewing tab (decision B1 of ginsys/bronzeward#255, client contracts §9.3 item 3): the holder
// of the Web Lock `bronzeward-renewer` alone sends refresh tokens; every other tab of the origin
// asks it over the BroadcastChannel and takes the pair it posts.
//
// A refresh token goes out only once every other live tab has acknowledged its `renewing`, so no
// tab that may hold it can become holder without knowing it is spent (#388). Each tab holds an
// exclusive lock `bronzeward-tab:<id>` and opens its channel only once that lock is granted: a
// message reaches it only if it was posted after the grant, so the holder's lock query, made after
// it learnt of the token, lists every tab that may hold it.

import type { Session, SessionOptions, Tokens } from "./auth.ts";

export const LOCK = "bronzeward-renewer";
export const TAB = "bronzeward-tab:";
// B1: how long a tab waits for the holder's pair when renewing, and as a new tab.
export const RENEW_WAIT_MS = 10_000;
export const JOIN_WAIT_MS = 5_000;
// #388: how long the holder waits for the other tabs' acks before it takes a silent tab's lock, and
// how long a tab granted the lock waits for the pair of a redirect before it redirects itself.
export const ACK_WAIT_MS = 3_000;
export const REDIRECT_WAIT_MS = 10_000;

export type Message =
  | { type: "signed-out" }
  // `of`: the refresh token the holder is about to send, spent wherever else it is held.
  | { type: "renewing"; of: string }
  // `tab`: the acking tab's lock name; it has recorded `of` as spent.
  | { type: "ack"; of: string; tab: string }
  // `due`: the expiry of the sender's pair, which reached the threshold or was refused; absent
  // from a new tab, which takes the current pair.
  | { type: "request"; due?: number }
  | { type: "pair"; tokens: Tokens }
  // The holder leaves to renew by the redirect, whose pair comes to every tab.
  | { type: "redirecting" };

export interface Channel {
  postMessage(message: Message): void;
  close(): void;
  onmessage: ((e: MessageEvent) => void) | null;
}

export interface Locks {
  request(name: string, options: { steal?: boolean }, callback: () => Promise<void> | undefined): Promise<unknown>;
  query(): Promise<{ held?: { name?: string }[] }>;
}

export class Renewer {
  #holder = false;
  #release: (() => void) | undefined;
  // Released for good: a page that stops renewing never holds the lock again (no tab re-signs in
  // without a navigation), and a join still awaiting its query must not take it afterwards.
  #closed = false;
  // This tab's lock name, its channel once that lock is granted, and the end of the lock.
  #tab = "";
  #channel: Channel | undefined;
  #ready: Promise<void> = Promise.resolve();
  #leave: (() => void) | undefined;
  // The channels of the tab locks taken from this tab: they still deliver what was posted to them
  // before, of which this tab keeps only a sign-out and the refresh tokens announced as spent (#388).
  readonly #stale: Channel[] = [];
  // Until when another tab's redirect may still bring a pair.
  #redirectUntil = 0;
  // A sign-out, this tab's or another's, is acted on once: a second one must not cut short the
  // first one's bounded wait (#388).
  #signedOut = false;
  // Every refresh token another tab announced in a `renewing`: whatever its answer, it may be spent,
  // so it is never sent or given from here. Told by token, not by expiry, so that a pair with a
  // shorter lifetime cannot hide it. Erased on release() (§10.3 item 3), after which this tab sends
  // and gives nothing.
  readonly #sent = new Set<string>();
  readonly #waiting = new Set<(t: Tokens) => void>();
  readonly #granted = new Set<() => void>();
  readonly #acks = new Set<(m: { of: string; tab: string }) => void>();

  constructor(
    private readonly open: () => Channel,
    private readonly locks: Locks,
    private readonly session: () => Session | undefined,
    // A pair from another tab, after any waiting renewal has taken it.
    private readonly onPair: (t: Tokens) => void,
    // §9.7 item 2: another tab signed out; `ended` settles once this tab's session has revoked what
    // it holds and a renewal in flight the pair it brings, at most SIGN_OUT_WAIT_MS.
    private readonly onSignedOut: (ended: Promise<void>) => void = () => {},
  ) {
    this.#start();
  }

  // This tab holds the lock now: it alone renews, and by the redirect where §9.3 item 3 says so.
  get holder(): boolean {
    return this.#holder;
  }

  // What a Session tells the other tabs.
  readonly hooks: Pick<SessionOptions, "onRenewing" | "onRenewed" | "spent"> = {
    onRenewing: (of) => this.#announce(of),
    onRenewed: (tokens) => this.#post({ type: "pair", tokens }),
    spent: (refresh) => this.#closed || this.#sent.has(refresh),
  };

  // A new tab, or one restored from the back-forward cache: the holder's pair, or undefined when
  // there is no holder, it does not answer in time or leaves first, and the page redirects. The
  // lock is requested at once: granted before a pair, no tab that asked before this one is left.
  async join(): Promise<Tokens | undefined> {
    await this.#ready;
    const { held } = await this.locks.query();
    if (this.#closed || !held?.some((l) => l.name === LOCK)) return undefined;
    this.hold();
    const t = await this.#wait(JOIN_WAIT_MS, () => true, { type: "request" });
    return t === "holder" ? undefined : t;
  }

  // A pair obtained by a redirect goes to every tab.
  post(tokens: Tokens): void {
    this.#post({ type: "pair", tokens });
  }

  // §9.7 item 2: tell the other tabs, before this one releases its channel.
  signOut(): void {
    this.#signedOut = true;
    this.#now({ type: "signed-out" });
  }

  // §9.3 item 3: just before the holder's renewal by the redirect, so that the next holder waits
  // for its pair rather than redirecting too (#388).
  redirecting(): void {
    this.#now({ type: "redirecting" });
  }

  // Posted at once, not when the channel opens: the page may be released first. While a rejoin
  // waits for its new tab lock, the old channel still reaches the other tabs.
  #now(m: Message): void {
    (this.#channel ?? this.#stale.at(-1))?.postMessage(m);
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
    void this.locks.request(LOCK, {}, () => {
      if (released) return undefined;
      this.#holder = true;
      for (const g of this.#granted) g();
      return held;
    });
  }

  // On pagehide, sign-out or when the tokens are gone, so that another tab can renew; the tab is no
  // longer one the holder waits on, and hears only `signed-out` from now on.
  release(): void {
    this.#closed = true;
    this.#sent.clear();
    this.#release?.();
    this.#release = undefined;
    this.#leave?.();
    this.#leave = undefined;
  }

  // On unmount: nothing more is heard either.
  close(): void {
    this.release();
    this.#channel?.close();
    this.#channel = undefined;
    for (const c of this.#stale.splice(0)) c.close();
  }

  // §9.5 item 1: the holder renews; another tab asks it and waits for a newer pair, at most 10 s,
  // or fails as item 3, unless the lock comes to it first. False also when the pair is another
  // principal's (item 2, onPair's), and for a holder that renews by the redirect.
  async renew(): Promise<boolean> {
    const s = this.session();
    const before = s?.expiresAt;
    const who = s?.principal;
    if (s === undefined || before === undefined || who === undefined) return false;
    const accept = (p: Tokens) => p.expiresAt > before || p.iss !== who.iss || p.sub !== who.sub;
    let t = this.#holder ? "holder" : await this.#wait(RENEW_WAIT_MS, accept, { type: "request", due: before });
    // #388: within REDIRECT_WAIT_MS of the previous holder's `redirecting`, a holder that would
    // redirect too waits for that redirect's pair, granted the lock while it asked or before.
    const left = this.#redirectUntil - Date.now();
    if (t === "holder" && this.session() === s && s.byRedirect && left > 0) t = (await this.#wait(left, accept)) ?? "holder";
    if (this.session() !== s || t === undefined) return false;
    return t === "holder" ? s.renew() : t.iss === who.iss && t.sub === who.sub;
  }

  onMessage(m: Message): void {
    if (m.type === "signed-out") {
      if (this.#signedOut) return;
      this.#signedOut = true;
      return this.onSignedOut(this.session()?.end() ?? Promise.resolve());
    }
    if (this.#closed) return;
    if (m.type === "renewing" && typeof m.of === "string") {
      this.#sent.add(m.of);
      this.#post({ type: "ack", of: m.of, tab: this.#tab });
    }
    if (m.type === "ack") for (const a of this.#acks) a(m);
    if (m.type === "redirecting") this.#redirectUntil = Date.now() + REDIRECT_WAIT_MS;
    if (m.type === "request") void this.#answer(typeof m.due === "number" ? m.due : undefined);
    if (m.type !== "pair" || !isTokens(m.tokens)) return;
    for (const w of this.#waiting) w(m.tokens);
    this.onPair(m.tokens);
  }

  // Take a new tab lock, then open the channel. A lock taken from this tab (another holder found it
  // silent, frozen or suspended) means messages may have been missed: the tab starts over.
  #start(): void {
    const tab = `${TAB}${random()}`;
    this.#tab = tab;
    let opened!: () => void;
    this.#ready = new Promise((r) => (opened = r));
    const held = new Promise<void>((r) => (this.#leave = r));
    this.locks
      .request(tab, {}, () => {
        // Released first: whatever awaits the channel goes on, and finds the tab closed.
        if (this.#closed || this.#tab !== tab) return void opened();
        const channel = this.open();
        channel.onmessage = (e) => {
          const m = e.data as Message | null;
          if (typeof m === "object" && m !== null) this.onMessage(m);
        };
        this.#channel = channel;
        opened();
        return held;
      })
      .catch(() => this.#rejoin(tab));
  }

  // §9.3 item 3: this tab's refresh token may have been sent by a holder it never heard, so it is
  // given up, and the tab asks for the current pair, or renews by the redirect as holder.
  #rejoin(tab: string): void {
    if (this.#closed || this.#tab !== tab) return;
    this.session()?.forfeit();
    // A pair posted before the new tab lock may hold a token another tab sent without waiting for
    // this one: never taken. A sign-out posted then still applies, and a `renewing` still spends.
    const old = this.#channel;
    if (old !== undefined) {
      old.onmessage = (e) => {
        const m = e.data as Message | null;
        if (typeof m !== "object" || m === null) return;
        if (m.type === "signed-out") this.onMessage(m);
        else if (m.type === "renewing" && typeof m.of === "string" && !this.#closed) this.#sent.add(m.of);
      };
      this.#stale.push(old);
    }
    this.#channel = undefined;
    this.#leave?.();
    this.#start();
    this.#post({ type: "request" });
  }

  // A released tab still posts the pair of a renewal it had in flight; it acks and asks no more.
  #post(m: Message): void {
    void this.#ready.then(() => this.#channel?.postMessage(m));
  }

  // #388: true once every other live tab has recorded `of` as spent and this tab's own lock is still
  // its own; a silent tab's lock is taken after ACK_WAIT_MS, which starts that tab over.
  async #announce(of: string): Promise<boolean> {
    const acked = new Set<string>();
    let check = () => {};
    const ack = (m: { of: string; tab: string }) => {
      if (m.of === of) acked.add(m.tab);
      check();
    };
    this.#acks.add(ack);
    try {
      this.#post({ type: "renewing", of });
      await this.#ready;
      const tab = this.#tab;
      const others = (await this.#held()).filter((n) => n.startsWith(TAB) && n !== tab);
      await new Promise<void>((done) => {
        const timer = setTimeout(done, ACK_WAIT_MS);
        check = () => {
          if (others.every((n) => acked.has(n))) {
            clearTimeout(timer);
            done();
          }
        };
        check();
      });
      if (this.#closed || this.#tab !== tab) return false;
      for (const n of others) if (!acked.has(n)) await this.locks.request(n, { steal: true }, () => undefined);
      const mine = (await this.#held()).includes(tab);
      // Released or rejoined meanwhile (another tab's sign-out, pagehide): nothing is sent.
      if (this.#closed || this.#tab !== tab) return false;
      if (!mine) this.#rejoin(tab);
      return mine;
    } finally {
      this.#acks.delete(ack);
    }
  }

  async #held(): Promise<string[]> {
    const { held } = await this.locks.query();
    return (held ?? []).flatMap((l) => (typeof l.name === "string" ? [l.name] : []));
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
  #wait(ms: number, accept: (t: Tokens) => boolean, request?: Message): Promise<Tokens | "holder" | undefined> {
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
      if (request !== undefined) {
        this.#granted.add(granted);
        this.#post(request);
      }
    });
  }
}

function random(): string {
  return Array.from(crypto.getRandomValues(new Uint8Array(12)), (b) => b.toString(16).padStart(2, "0")).join("");
}

function isTokens(t: unknown): t is Tokens {
  const v = t as Record<string, unknown> | null;
  return typeof v === "object" && v !== null && typeof v["iss"] === "string" && typeof v["sub"] === "string" && typeof v["access"] === "string" &&
    (v["refresh"] === undefined || typeof v["refresh"] === "string") && typeof v["expiresAt"] === "number";
}
