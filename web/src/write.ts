// The write layer every form uses (ginsys/bronzeward#338; #257 B10 and B11; client contracts §8.2
// write rows, §8.4, §8.5 item 2). One logical write keeps one key from its first send to its last
// repeat. Its answer is never discarded as stale: it settles the write's own record even after the
// page moved on. Pending writes live in this page's memory only (§8.4 item 5). A fragment write's
// in-progress 409 is repeated within the wait bound (§8.4 item 2, AMB-02; #339). The recovery
// entry's exception (§8.4 item 3) is the `entry` flag of the spec (#364).

import { type Attempt, type Client, type End, type Ending, NOT_JSON, NOT_SENT, preEntry, type Problem, WRITE_TRANSPORT_FAILURE } from "./request.ts";

export interface WriteSpec {
  method: "POST" | "PUT" | "DELETE";
  // Under /api/v1.
  path: string;
  body: unknown;
  // Where the route takes one (§8.3).
  ifMatch?: string;
  // The page that shows the resource, named in the notice and opened by [Show current state].
  resource: string;
  // A fragment write, which ingests: a repeat's in-progress 409 waits for the first request.
  ingests?: boolean;
  // The recovery entry (§8.4 item 3): its own commit mints the new epoch, so it is sent without
  // Bronzeward-Expected-Epoch and with no epoch read, repeated under its key with the principal
  // checked and not the epoch, and never set aside by an epoch change.
  entry?: boolean;
  // Called with each outcome the write settles on, a [Repeat exactly]'s included, whether or not
  // the view that sent it is still shown: a draft's ETag is kept from it (§8.3, #339).
  settled?: (o: Outcome, w: Write) => void;
}

export type Outcome =
  // `defect` when the success carried no Bronzeward-Epoch header (§8.5): still saved, never repeated.
  // `etag` where the answer carries one (§8.3).
  | { kind: "done"; status: number; data: unknown; replayed: boolean; defect?: string; etag?: string }
  // This send's refusal, or the earlier attempt's when replayed (§8.4 item 2).
  | { kind: "refused"; status: number; problem: Problem | null; replayed: boolean }
  // Every attempt was answered a 503 that committed nothing (§8.2), or nothing was sent.
  | { kind: "not-done"; why: string; problem: Problem | null }
  // An attempt may have committed (§8.4 item 2). [Repeat exactly] is offered while `repeatable`.
  // `problem` is the last answer's, shown as every problem is (§8.2); null when it carried none.
  | { kind: "unknown"; why: string; problem: Problem | null }
  // Not repeated and not sent under a new key (§8.4 item 3, journeys §8). `problem` is the answer's
  // that set it aside, null when no answer did.
  | { kind: "set-aside"; why: string; ambiguous: boolean; problem: Problem | null };

// Each `why` is a clause the notice and the forms show after "Not saved:" or the state's text.
export const SIGN_IN_CHANGED = "your sign-in changed";
export const EPOCH_CHANGED_ASIDE = "the installation's epoch changed";
export const NO_EPOCH = "the read of the installation's epoch got no answer that names one";
export const SIGN_IN_REFUSED = "the server no longer accepts your sign-in";
export const SHELL_GONE = "the page that sent it ended";
export const NO_CREDENTIAL = "the page no longer holds your sign-in";
export const RECOVERY_START = "the server was started for recovery and serves only the recovery entry until it is recorded";
export const NO_EPOCH_HEADER = "Its answer carried no Bronzeward-Epoch header, a defect of the server or of a proxy before it.";
export const EARLIER_EPOCH ="the key's record is from an earlier epoch";
export const TOO_LARGE ="the input is larger than 1 MiB, the most the server takes, so nothing was sent";
// The largest body the server takes, in UTF-8 bytes (§8.1, internal/api/body.go maxBody).
export const MAX_BODY = 1 << 20;
export const RENEWED_LAST ="the last attempt was answered 401 and your sign-in was renewed, with no attempt left to send it again";
// The page failed while it held the write, for example a renewal that threw.
const PAGE_FAILED = "the page failed while it sent it";

export type { Ending };
const PREFIX = "urn:bronzeward:problem:";
// The 503s a write repeats; each states that its own attempt committed nothing (§8.2).
const REPEATED_503 = new Set(["dependency-unavailable", "talos-access-unavailable", "transient-conflict"]);
// The waits before each of a write's at most three repeats (§8.4 item 2, as the CLI's #346).
export const BACKOFF = [1000, 2000, 4000];
// How long a repeat waits for the first request of its key to finish ingesting, unless the
// served claim lease is longer (as the CLI's --ingest-wait, #347).
export const INGEST_WAIT = 120_000;
// How long the in-progress wait's GET /server may take before the bound falls back without it
// (ginsys/bronzeward#517 H13).
export const SERVER_WAIT = 10_000;

// One key per logical write: 128 bits from the browser's random source, base64url without padding
// (§8.4 item 1), never derived from the content.
export function newKey(): string {
  const b = crypto.getRandomValues(new Uint8Array(16));
  return btoa(String.fromCharCode(...b)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

export class Write {
  readonly key = newKey();
  readonly route: string;
  // The principal and the reference epoch of the first send; every repeat is checked against both.
  principal = "";
  epoch: string | undefined;
  // Some attempt may have committed.
  ambiguous = false;
  // While the automatic repeats run after an attempt that may have committed: why the outcome is
  // unknown, with the answer's problem, so the notice shows at once (B11), before the run settles.
  doubt: Extract<Outcome, { kind: "unknown" }> | undefined;
  // Why the write was set aside while a run was in flight (§8.5 item 2).
  aside: string | undefined;
  outcome: Outcome | undefined;
  // While [Repeat exactly] runs: the unknown outcome it repeats, which the notice keeps showing.
  repeating: Extract<Outcome, { kind: "unknown" }> | undefined;
  // The notice showed it before it settled (unknown, set aside, or a [Repeat exactly]): the notice
  // shows its result until dismissed (B11), since the form that sent it may no longer be shown.
  kept = false;
  // The run in flight; a second submit returns it and sends nothing.
  running: Promise<Outcome> | undefined;
  // The ingestion a repeat's in-progress 409 names while the run waits for it.
  ingesting: string | undefined;
  constructor(readonly spec: WriteSpec) {
    this.route = `${spec.method} /api/v1${spec.path}`;
  }
}

const isOpen = (w: Write) => w.outcome === undefined || w.outcome.kind === "unknown" || (w.outcome.kind === "set-aside" && w.outcome.ambiguous);
const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));
const problemOf = (d: unknown): Problem | null =>
  typeof d === "object" && d !== null && typeof (d as Problem).type === "string" && (d as Problem).type.startsWith(PREFIX) ? (d as Problem) : null;

// The page's writes. `client` is the shell's, bound while it is shown; `principal` is the current
// sign-in's issuer and subject.
export class Writes {
  private bound: Client | undefined;
  private unbind: (() => void) | undefined;
  // The page is being left by a notice's own [Show current state]: the next unload guard does not
  // ask, and clears it.
  unloading = false;
  // The shell's handling of a credential refusal, bound while it is shown. A write's answer reaches
  // it after the write's own outcome is settled, so the write's notice survives the page's change.
  onEnded: ((e: Ending) => void) | undefined;
  // The shell's handling of a recovery start's process refusal (§8.5 item 1), bound while it is
  // shown. It is told once the write's outcome is settled, or at once when the read that learns
  // the epoch of the write's answer meets it, so a stalled answer body cannot hide it.
  onPreEntry: ((p: Problem) => void) | undefined;
  // The claim lease, in seconds, of the shell's last GET /server: the in-progress wait's bound
  // when its own read of it fails (ginsys/bronzeward#517 H14).
  lease: number | undefined;
  private readonly all = new Set<Write>();
  private readonly listeners = new Set<() => void>();
  private version = 0;

  constructor(
    private readonly principal: () => string | undefined,
    private readonly wait: (ms: number) => Promise<void> = sleep,
    // The clock the in-progress wait bound is measured on, in milliseconds.
    private readonly now: () => number = () => performance.now(),
  ) {}

  // The notices render again without [Repeat exactly] once the bound client's sign-in ends.
  get client(): Client | undefined {
    return this.bound;
  }
  set client(c: Client | undefined) {
    if (c === this.bound) return;
    this.unbind?.();
    this.bound = c;
    this.unbind = c?.onWritesEnded?.(() => this.changed());
  }

  // useSyncExternalStore's pair.
  readonly subscribe = (f: () => void) => {
    this.listeners.add(f);
    return () => void this.listeners.delete(f);
  };
  readonly snapshot = () => this.version;
  // The shell that bound `client` is gone: no write is repeated through it, so the notices render
  // again without [Repeat exactly].
  disconnect(client: Client): void {
    if (this.client !== client) return;
    this.client = undefined;
    this.changed();
  }
  // The page was hidden (§10.3 item 3): its writes go with it. A page restored from the
  // back-forward cache reloads, and that reload asks nothing for a write no notice shows any more.
  forget(): void {
    this.all.clear();
    this.changed();
  }
  private changed(): void {
    this.version++;
    for (const f of this.listeners) f();
  }

  // The writes the leave guards ask about and a form waits on: in flight, unknown, or set aside
  // after an attempt that may have committed. One set aside before any such attempt committed
  // nothing, and the form says so.
  get open(): Write[] {
    return [...this.all].filter(isOpen);
  }

  // The writes the notices show: the open ones, and each one a notice showed before it settled,
  // until dismissed.
  get notices(): Write[] {
    return [...this.all].filter((w) => isOpen(w) || w.kept);
  }

  // Sends `w`, or returns the run in flight: a second submit of the same logical write sends nothing.
  // Once the write has an outcome, submitting it again is [Repeat exactly], allowed only while
  // `repeatable`.
  submit(w: Write): Promise<Outcome> {
    if (w.running !== undefined) return w.running;
    if (w.outcome !== undefined && !this.repeatable(w)) return Promise.resolve(w.outcome);
    this.all.add(w);
    w.repeating = w.outcome?.kind === "unknown" ? w.outcome : undefined;
    const repeat = w.repeating !== undefined;
    w.outcome = undefined;
    // An ending learned before the write's own answer is read ends the page at once; any other
    // reaches it when the write settles. Either reaches it once.
    let told = false;
    const tell = (e: Ending) => {
      if (told) return;
      told = true;
      this.onEnded?.(e);
    };
    // A recovery start's process refusal reaches the shell the same way, once.
    let toldPre = false;
    const tellPre = (p: Problem) => {
      if (toldPre) return;
      toldPre = true;
      this.onPreEntry?.(p);
    };
    const end: End = { now: tell, nowPre: tellPre };
    // A run that throws settles the write too: unknown once an attempt may have committed.
    const failed = (): Outcome => (w.ambiguous ? { kind: "unknown", why: PAGE_FAILED, problem: null } : { kind: "not-done", why: PAGE_FAILED, problem: null });
    w.running = this.run(w, end).catch(failed).then((r) => {
      // Set aside while its last attempt was in flight, a write that then got no answer is not
      // offered for repeating; an answer still settles it.
      const o: Outcome = r.kind === "unknown" && w.aside !== undefined ? { kind: "set-aside", why: w.aside, ambiguous: true, problem: r.problem } : r;
      if (o.kind === "unknown") w.ambiguous = true;
      // Each state the notice shows while the run is in flight (see forms.tsx WriteNotices).
      const shown = repeat || w.doubt !== undefined || w.aside !== undefined;
      w.outcome = o;
      w.repeating = undefined;
      w.doubt = undefined;
      w.running = undefined;
      w.ingesting = undefined;
      w.spec.settled?.(o, w);
      // The one removal before a dismissal: a write no notice showed, which its form reports.
      if (!isOpen(w)) {
        if (shown) w.kept = true;
        else this.all.delete(w);
      }
      this.changed();
      if (end.e !== undefined) tell(end.e);
      else if (end.pre !== undefined) tellPre(end.pre);
      return o;
    });
    this.changed();
    return w.running;
  }

  // [Repeat exactly]: only while this page holds the request, for the same sign-in and epoch.
  repeatable(w: Write): boolean {
    // A page with no client (after a disconnect) has nothing to send with: the entry's exception to the
    // epoch check does not offer a repeat that would end as not done and drop the unknown outcome.
    return w.outcome?.kind === "unknown" && this.principal() === w.principal && this.client !== undefined && this.client.writesEnded !== true && (w.spec.entry === true || this.client.currentEpoch === w.epoch);
  }

  // §8.5 item 2 and journeys §8: a new epoch or another sign-in sets every open write aside. One in
  // flight is set aside before its next repeat; its answer still in flight still settles it.
  setAside(why: string): void {
    for (const w of this.all) {
      // The recovery entry crosses the epoch change its own commit makes (§8.4 item 3); another
      // sign-in still sets it aside.
      if (w.spec.entry === true && why === EPOCH_CHANGED_ASIDE) continue;
      if (w.outcome === undefined) w.aside ??= why;
      else if (w.outcome.kind === "unknown") w.outcome = { kind: "set-aside", why, ambiguous: true, problem: w.outcome.problem };
    }
    this.changed();
  }

  dismiss(w: Write): void {
    if (w.outcome === undefined) return;
    this.all.delete(w);
    this.changed();
  }

  private async run(w: Write, end: End): Promise<Outcome> {
    const client = this.client;
    const who = this.principal();
    if (client === undefined || who === undefined) return { kind: "not-done", why: "there is no sign-in to send it under", problem: null };
    const body = JSON.stringify(w.spec.body);
    // A larger body is refused here, before any send and before the read of the epoch (§8.1).
    if (body !== undefined && new TextEncoder().encode(body).length > MAX_BODY) return { kind: "not-done", why: TOO_LARGE, problem: null };
    // A [Repeat exactly] is a repeat: it is checked before it is sent. The recovery entry has no
    // reference epoch, so its first run is told by the principal it recorded.
    const entry = w.spec.entry === true;
    const again = entry ? w.principal !== "" : w.epoch !== undefined;
    if (!again) {
      w.principal = who;
      if (!entry) {
        // The last epoch seen before the first send, else one GET /recovery (§8.4 item 3).
        w.epoch = client.currentEpoch ?? (await this.confirm(client, end));
        if (end.e !== undefined) return { kind: "not-done", why: SIGN_IN_REFUSED, problem: end.e.problem };
        if (end.pre !== undefined) return { kind: "not-done", why: RECOVERY_START, problem: end.pre };
        const gone = this.gone(w, client);
        if (gone !== undefined) return { kind: "not-done", why: gone, problem: null };
        if (w.epoch === undefined) return { kind: "not-done", why: NO_EPOCH, problem: null };
      }
    }
    const headers: Record<string, string> = { "Content-Type": "application/json", "Idempotency-Key": w.key };
    if (!entry) headers["Bronzeward-Expected-Epoch"] = w.epoch!;
    if (w.spec.ifMatch !== undefined) headers["If-Match"] = w.spec.ifMatch;
    // The last answer, as status and code, and its problem; undefined and null while the last
    // attempt got none. A [Repeat exactly] starts from the problem of the answer it repeats.
    let last: string | undefined;
    let lastProblem: Problem | null = w.repeating?.problem ?? null;
    // Set aside by a check before a send or by an answer's refusal: either way with the last
    // answer's problem (§8.2), which a check before a send leaves as it was.
    const aside = (why: string): Outcome => ({ kind: "set-aside", why, ambiguous: w.ambiguous, problem: lastProblem });
    const unknown = (why = last === undefined ? WRITE_TRANSPORT_FAILURE : `the server answered ${last}`): Outcome => ({ kind: "unknown", why, problem: lastProblem });
    // A refusal decided before the idempotency lookup says nothing of an earlier ambiguous attempt.
    const early = (o: Outcome): Outcome => (w.ambiguous ? unknown() : o);
    let p503: Problem | null = null;
    let renewed = false;
    // An attempt that may have committed: the outcome is unknown from now on, and shown so while
    // the repeats run.
    const doubt = () => {
      w.ambiguous = true;
      w.doubt = { kind: "unknown", why: last === undefined ? WRITE_TRANSPORT_FAILURE : `the server answered ${last}`, problem: lastProblem };
      this.changed();
    };
    // The wait bound, read once at the first in-progress 409, when the run started waiting, and
    // how long it waited. The time spent counts the repeats' requests, not only the waits.
    let bound: number | undefined;
    let since = 0;
    let waited = 0;
    const spent = () => Math.max(waited, this.now() - since);
    for (let n = 0; n <= BACKOFF.length; n++) {
      // A read sent to learn the last answer's epoch ended the sign-in: nothing is repeated.
      if (n > 0 && end.e !== undefined) return aside(SIGN_IN_REFUSED);
      // A read sent to learn it met a recovery start: nothing is repeated, and nothing read again.
      if (n > 0 && end.pre !== undefined) return aside(RECOVERY_START);
      if (w.ingesting !== undefined) {
        // Each wait doubles from 1 s up to 4 s and ends at the bound.
        const step = Math.min(1000 << Math.min(Math.floor(waited / 1000), 2), bound! - spent());
        if (step > 0) {
          await this.wait(step);
          waited += step;
        }
      } else if (n > 0) await this.wait(BACKOFF[n - 1]!);
      if (n > 0 || again) {
        // Looked at again once run() resumes: a sign-out, an end of the shell or a set-aside can
        // land between check()'s last look and this send.
        const why = (await this.check(client, w, end)) ?? w.aside ?? this.gone(w, client);
        if (why !== undefined) return aside(why);
      }
      let a: Attempt | undefined | typeof NOT_SENT;
      try {
        a = await client.attempt(w.spec.method, w.spec.path, headers, body, end);
      } catch (e) {
        // attempt() catches its own send's failure, so a throw comes after the write went out, for
        // example a renewal that threw while its epoch was learned: it may have committed.
        w.ambiguous = true;
        throw e;
      }
      if (w.ingesting !== undefined) {
        w.ingesting = undefined;
        this.changed();
      }
      // Nothing went out: the sign-in ended, or its credential is gone, since the checks above.
      if (a === NOT_SENT) {
        const why = this.gone(w, client) ?? NO_CREDENTIAL;
        return n > 0 || again ? aside(why) : { kind: "not-done", why, problem: null };
      }
      // No complete answer, a redirect the browser refused included: some byte may have gone out
      // (§8.4 item 2, TLS-03). A status outside 100-599 is no HTTP answer either: a browser reads a
      // status line cut off by a close as a whole answer over plain HTTP.
      if (a === undefined || a.status < 100 || a.status > 599) {
        last = undefined;
        lastProblem = null;
        doubt();
        continue;
      }
      const replayed = a.headers.get("Idempotent-Replayed") === "true";
      if (a.status >= 200 && a.status < 300) {
        const done: Outcome = { kind: "done", status: a.status, data: a.data === NOT_JSON ? null : a.data, replayed };
        const etag = a.headers.get("ETag");
        const tagged: Outcome = etag === null ? done : { ...done, etag };
        // An authenticated success always names the epoch: a missing header is reported (§8.5).
        return a.headers.has("Bronzeward-Epoch") ? tagged : { ...tagged, defect: NO_EPOCH_HEADER };
      }
      const p = problemOf(a.data);
      const code = p === null ? undefined : p.type.slice(PREFIX.length);
      last = code === undefined ? `${a.status}` : `${a.status} ${code}`;
      lastProblem = p;
      const refused: Outcome = { kind: "refused", status: a.status, problem: p, replayed };
      if (a.status === 401) {
        // Renew once; renewed, the next attempt is a repeat with its checks (§8.2 401 row). Renewed
        // after the last attempt, the sign-in stays and the write is not sent again.
        if (code === "unauthenticated" && !renewed && (await client.renew())) {
          renewed = true;
          if (n < BACKOFF.length) continue;
          return early({ kind: "not-done", why: RENEWED_LAST, problem: p });
        }
        // Any other 401 code is a refusal by its status class, as for a read.
        if (code === "unauthenticated") {
          client.endWrites();
          end.e = { kind: "unauthenticated", problem: p };
        }
        return early(refused);
      }
      if (a.status === 403 && code === "identity-revoked") {
        client.revoked();
        // A code is read only from a problem, so `p` is one.
        end.e = { kind: "revoked", problem: p! };
        return early(refused);
      }
      // The route-level role check, decided before the lookup; only the plan cancellation's creator
      // check, which carries `plan`, refuses after it (§8.2 403 row).
      if (a.status === 403 && code === "forbidden" && p?.["plan"] === undefined) return early(refused);
      // An earlier epoch's record of this key: that request committed, and its effect may be in the
      // current state (§8.4 item 7). It is set aside as unknown, never as not saved.
      // An entry's too: its key's record of an earlier epoch that is not this process's entry (the
      // server answers `request` and `epoch`) means the first send committed, so a repeat that
      // reaches a restarted process must not read it as a refusal. REC-02's competing entry under
      // another key carries `epoch` alone.
      if (a.status === 409 && code === "conflict" && p?.["request"] !== undefined && p["epoch"] !== undefined) {
        w.ambiguous = true;
        return aside(EARLIER_EPOCH);
      }
      // A repeat's in-progress 409: the first request still ingests, or ended without committing
      // while its claim's lease runs (§8.4 item 2, AMB-02). It is repeated with the same key until
      // it answers otherwise, within the bound, and counts as no repeat. One that also names a
      // draft (published meanwhile), or a replayed one, is a recorded refusal, below.
      if (w.spec.ingests && a.status === 409 && code === "conflict" && p?.["ingestion"] !== undefined && p["draft"] === undefined && !replayed && (n > 0 || again)) {
        const ingestion = String(p["ingestion"]);
        if (bound === undefined) {
          since = this.now();
          bound = await this.ingestBound(client, end);
          // Its read ended the sign-in or met a recovery start: nothing is repeated (H12).
          if (end.e !== undefined) return aside(SIGN_IN_REFUSED);
          if (end.pre !== undefined) return aside(RECOVERY_START);
        }
        if (spent() >= bound) return unknown(`the first request with this key was still ingesting (ingestion ${ingestion}) after ${Math.round(spent() / 1000)} s`);
        w.ingesting = ingestion;
        this.changed();
        n--;
        continue;
      }
      // A process refusal, decided before the lookup (§8.2 409 row): a recovery start, which
      // attempt() recorded in `end.pre` for the shell to show (§8.5 item 1).
      if (preEntry(a.status, p) !== undefined) return early(refused);
      if (a.status === 412 && code === "epoch-mismatch") return aside(EPOCH_CHANGED_ASIDE);
      if (a.status === 422 && code === "idempotency-key-reused" && (n > 0 || again)) return unknown("this repeat could not be matched to the earlier request under its key, so it did nothing");
      if (a.status === 503 && (REPEATED_503.has(code ?? "") || code === "epoch-superseded")) {
        p503 = p;
        // epoch-superseded cannot succeed against this server process (§8.2 503 row).
        if (code === "epoch-superseded") break;
        continue;
      }
      if (a.status >= 500 && code !== "not-implemented") {
        // 500 internal-error, an unknown 5xx code, or a 5xx that is not a Bronzeward problem.
        doubt();
        continue;
      }
      // This send's own answer: a repeat that found the key's record would have replayed it.
      return refused;
    }
    return w.ambiguous ? unknown() : { kind: "not-done", why: `every attempt was answered ${last}, which commits nothing`, problem: p503 };
  }

  // Before a repeat: the same sign-in, and one GET /recovery that shows the reference epoch.
  private async check(client: Client, w: Write, end: End): Promise<string | undefined> {
    if (w.aside !== undefined) return w.aside;
    const before = this.gone(w, client);
    if (before !== undefined) return before;
    // The entry checks the principal and not the epoch (§8.4 item 3).
    if (w.spec.entry === true) return undefined;
    const now = await this.confirm(client, end);
    if (w.aside !== undefined) return w.aside;
    if (end.e !== undefined) return SIGN_IN_REFUSED;
    if (end.pre !== undefined) return RECOVERY_START;
    const after = this.gone(w, client);
    if (after !== undefined) return after;
    if (now === undefined) return NO_EPOCH;
    return now === w.epoch ? undefined : EPOCH_CHANGED_ASIDE;
  }

  // The in-progress wait bound: INGEST_WAIT, or the claim lease if longer (§8.4 item 2). The lease
  // is the one GET /server serves now, or else the one the shell read (H14). That read is a read:
  // its credential refusals and a recovery start's refusal go to `end` (H12). It is not waited for
  // beyond SERVER_WAIT (H13).
  private async ingestBound(client: Client, end: End): Promise<number> {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const late = new Promise<undefined>((r) => (timer = setTimeout(() => r(undefined), SERVER_WAIT)));
    const reading = this.read(client, "/server", end, false);
    const a = await Promise.race([reading, late]);
    clearTimeout(timer);
    // A refusal that read learns after the bound stopped waiting still ends the page, at once: the
    // write may have settled already, and its settling tells only what it saw then. Told once.
    if (a === undefined) void reading.then(() => (end.e !== undefined ? end.now?.(end.e) : end.pre !== undefined && end.nowPre?.(end.pre)), () => undefined);
    const served = a?.status === 200 ? (a.data as { ingestionClaim?: { leaseSeconds?: unknown } } | null)?.ingestionClaim?.leaseSeconds : undefined;
    const lease = typeof served === "number" ? served : this.lease;
    return lease !== undefined && lease * 1000 > INGEST_WAIT ? lease * 1000 : INGEST_WAIT;
  }

  // A sign-out, an ended sign-in or the end of the shell that held the client: nothing more is sent
  // through it. Looked at after every wait, since any of them can land while a read is answered.
  private gone(w: Write, client: Client): string | undefined {
    if (this.principal() !== w.principal) return SIGN_IN_CHANGED;
    return this.client === client && client.writesEnded !== true ? undefined : SHELL_GONE;
  }

  // The epoch one GET /recovery shows (§8.4 item 3).
  private async confirm(client: Client, end: End): Promise<string | undefined> {
    return (await this.read(client, "/recovery", end, true))?.headers.get("Bronzeward-Epoch") ?? undefined;
  }

  // One read for the write layer. Its credential refusals are a read's (§8.2): a 401 renews once
  // and is sent again, and a second 401 or identity-revoked ends the page through `end`.
  // Undefined then, or after a recovery start's refusal that attempt() recorded in `end`, or when
  // nothing was sent, which the caller's next look names.
  private async read(client: Client, path: string, end: End, epochOnly: boolean): Promise<Attempt | undefined> {
    for (let renewed = false; ; renewed = true) {
      const a = await client.attempt("GET", path, {}, undefined, end, epochOnly);
      if (end.e !== undefined || end.pre !== undefined || a === NOT_SENT) return undefined;
      const p = a === undefined ? null : problemOf(a.data);
      const code = p?.type.slice(PREFIX.length);
      if (a?.status === 401 && code === "unauthenticated") {
        if (!renewed && (await client.renew())) continue;
        client.endWrites();
        end.e = { kind: "unauthenticated", problem: p };
        return undefined;
      }
      if (a?.status === 403 && code === "identity-revoked") {
        client.revoked();
        end.e = { kind: "revoked", problem: p! };
        return undefined;
      }
      return a;
    }
  }
}
