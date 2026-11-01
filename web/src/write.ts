// The write layer every form uses (ginsys/bronzeward#338; #257 B10 and B11; client contracts §8.2
// write rows, §8.4, §8.5 item 2). One logical write keeps one key from its first send to its last
// repeat. Its answer is never discarded as stale: it settles the write's own record even after the
// page moved on. Pending writes live in this page's memory only (§8.4 item 5). The fragment
// write's in-progress 409 and the recovery entry's exception (§8.4 items 2 and 3) are not served
// here: no form of this layer sends either route.

import { type Client, type Ending, NOT_JSON, type Problem } from "./request.ts";

export interface WriteSpec {
  method: "POST" | "PUT" | "DELETE";
  // Under /api/v1.
  path: string;
  body: unknown;
  // Where the route takes one (§8.3).
  ifMatch?: string;
  // The page that shows the resource, named in the notice and opened by [Show current state].
  resource: string;
}

export type Outcome =
  | { kind: "done"; status: number; data: unknown; replayed: boolean }
  // This send's refusal, or the earlier attempt's when replayed (§8.4 item 2).
  | { kind: "refused"; status: number; problem: Problem | null; replayed: boolean }
  // Every attempt was answered a 503 that committed nothing (§8.2), or nothing was sent.
  | { kind: "not-done"; why: string; problem: Problem | null }
  // An attempt may have committed (§8.4 item 2). [Repeat exactly] is offered while `repeatable`.
  | { kind: "unknown"; why: string }
  // Not repeated and not sent under a new key (§8.4 item 3, journeys §8).
  | { kind: "set-aside"; why: string; ambiguous: boolean };

// Each `why` is a clause the notice and the forms show after "Not saved:" or the state's text.
export const SIGN_IN_CHANGED = "your sign-in changed";
export const EPOCH_CHANGED_ASIDE = "the installation's epoch changed";
export const NO_EPOCH = "the read of the installation's epoch got no answer that names one";
export const SIGN_IN_REFUSED = "the server no longer accepts your sign-in";
// The page failed while it held the write, for example a renewal that threw.
const PAGE_FAILED = "the page failed while it sent it";

export type { Ending };
const DROPPED = "the connection dropped";
const PREFIX = "urn:bronzeward:problem:";
// The 503s a write repeats; each states that its own attempt committed nothing (§8.2).
const REPEATED_503 = new Set(["dependency-unavailable", "talos-access-unavailable", "transient-conflict"]);
// The waits before each of a write's at most three repeats (§8.4 item 2, as the CLI's #346).
export const BACKOFF = [1000, 2000, 4000];

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
  // Why the write was set aside while a run was in flight (§8.5 item 2).
  aside: string | undefined;
  outcome: Outcome | undefined;
  // While [Repeat exactly] runs: the unknown outcome it repeats, which the notice keeps showing.
  repeating: Extract<Outcome, { kind: "unknown" }> | undefined;
  // The run in flight; a second submit returns it and sends nothing.
  running: Promise<Outcome> | undefined;
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
  client: Client | undefined;
  // The page is being left by a notice's own [Show current state]: the next unload guard does not
  // ask, and clears it.
  unloading = false;
  // The shell's handling of a credential refusal, bound while it is shown. A write's answer reaches
  // it after the write's own outcome is settled, so the write's notice survives the page's change.
  onEnded: ((e: Ending) => void) | undefined;
  private readonly all = new Set<Write>();
  private readonly listeners = new Set<() => void>();
  private version = 0;

  constructor(private readonly principal: () => string | undefined, private readonly wait: (ms: number) => Promise<void> = sleep) {}

  // useSyncExternalStore's pair.
  readonly subscribe = (f: () => void) => {
    this.listeners.add(f);
    return () => void this.listeners.delete(f);
  };
  readonly snapshot = () => this.version;
  private changed(): void {
    this.version++;
    for (const f of this.listeners) f();
  }

  // The writes the notice shows and the leave guards ask about: in flight, unknown, or set aside
  // after an attempt that may have committed. One set aside before any such attempt committed
  // nothing, and the form says so.
  get open(): Write[] {
    return [...this.all].filter(isOpen);
  }

  // Sends `w`, or returns the run in flight: a second submit of the same logical write sends nothing.
  // Once the write has an outcome, submitting it again is [Repeat exactly], allowed only while
  // `repeatable`.
  submit(w: Write): Promise<Outcome> {
    if (w.running !== undefined) return w.running;
    if (w.outcome !== undefined && !this.repeatable(w)) return Promise.resolve(w.outcome);
    this.all.add(w);
    w.repeating = w.outcome?.kind === "unknown" ? w.outcome : undefined;
    w.outcome = undefined;
    const end: { e?: Ending } = {};
    // A run that throws settles the write too: unknown once an attempt may have committed.
    const failed = (): Outcome => (w.ambiguous ? { kind: "unknown", why: PAGE_FAILED } : { kind: "not-done", why: PAGE_FAILED, problem: null });
    w.running = this.run(w, end).catch(failed).then((r) => {
      // Set aside while its last attempt was in flight, a write that then got no answer is not
      // offered for repeating; an answer still settles it.
      const o: Outcome = r.kind === "unknown" && w.aside !== undefined ? { kind: "set-aside", why: w.aside, ambiguous: true } : r;
      if (o.kind === "unknown") w.ambiguous = true;
      w.outcome = o;
      w.repeating = undefined;
      w.running = undefined;
      if (!isOpen(w)) this.all.delete(w);
      this.changed();
      if (end.e !== undefined) this.onEnded?.(end.e);
      return o;
    });
    this.changed();
    return w.running;
  }

  // [Repeat exactly]: only while this page holds the request, for the same sign-in and epoch.
  repeatable(w: Write): boolean {
    return w.outcome?.kind === "unknown" && this.principal() === w.principal && this.client?.currentEpoch === w.epoch;
  }

  // §8.5 item 2 and journeys §8: a new epoch or another sign-in sets every open write aside. One in
  // flight is set aside before its next repeat; its answer still in flight still settles it.
  setAside(why: string): void {
    for (const w of this.all) {
      if (w.outcome === undefined) w.aside ??= why;
      else if (w.outcome.kind === "unknown") w.outcome = { kind: "set-aside", why, ambiguous: true };
    }
    this.changed();
  }

  dismiss(w: Write): void {
    if (w.outcome === undefined) return;
    this.all.delete(w);
    this.changed();
  }

  private async run(w: Write, end: { e?: Ending }): Promise<Outcome> {
    const client = this.client;
    const who = this.principal();
    if (client === undefined || who === undefined) return { kind: "not-done", why: "there is no sign-in to send it under", problem: null };
    // A [Repeat exactly] is a repeat: it is checked before it is sent.
    const again = w.epoch !== undefined;
    if (!again) {
      w.principal = who;
      // The last epoch seen before the first send, else one GET /recovery (§8.4 item 3).
      w.epoch = client.currentEpoch ?? (await this.confirm(client, end));
      if (end.e !== undefined) return { kind: "not-done", why: SIGN_IN_REFUSED, problem: end.e.problem };
      if (w.epoch === undefined) return { kind: "not-done", why: NO_EPOCH, problem: null };
    }
    const headers: Record<string, string> = { "Content-Type": "application/json", "Idempotency-Key": w.key, "Bronzeward-Expected-Epoch": w.epoch! };
    if (w.spec.ifMatch !== undefined) headers["If-Match"] = w.spec.ifMatch;
    const body = JSON.stringify(w.spec.body);
    const aside = (why: string): Outcome => ({ kind: "set-aside", why, ambiguous: w.ambiguous });
    // The last answer, as status and code; undefined while the last attempt got none.
    let last: string | undefined;
    const unknown = (why = last === undefined ? DROPPED : `the server answered ${last}`): Outcome => ({ kind: "unknown", why });
    // A refusal decided before the idempotency lookup says nothing of an earlier ambiguous attempt.
    const early = (o: Outcome): Outcome => (w.ambiguous ? unknown() : o);
    let p503: Problem | null = null;
    let renewed = false;
    for (let n = 0; n <= BACKOFF.length; n++) {
      // A read sent to learn the last answer's epoch ended the sign-in: nothing is repeated.
      if (n > 0 && end.e !== undefined) return aside(SIGN_IN_REFUSED);
      if (n > 0) await this.wait(BACKOFF[n - 1]!);
      if (n > 0 || again) {
        const why = await this.check(client, w, end);
        if (why !== undefined) return aside(why);
      }
      const a = await client.attempt(w.spec.method, w.spec.path, headers, body, end);
      // No complete answer, a redirect the browser refused included: some byte may have gone out
      // (§8.4 item 2, TLS-03).
      if (a === undefined) {
        w.ambiguous = true;
        last = undefined;
        continue;
      }
      const replayed = a.headers.get("Idempotent-Replayed") === "true";
      if (a.status >= 200 && a.status < 300) return { kind: "done", status: a.status, data: a.data === NOT_JSON ? null : a.data, replayed };
      const p = problemOf(a.data);
      const code = p === null ? undefined : p.type.slice(PREFIX.length);
      last = code === undefined ? `${a.status}` : `${a.status} ${code}`;
      const refused: Outcome = { kind: "refused", status: a.status, problem: p, replayed };
      if (a.status === 401) {
        // Renew once; renewed, the next attempt is a repeat with its checks (§8.2 401 row).
        if (code === "unauthenticated" && !renewed && n < BACKOFF.length && (await client.renew())) {
          renewed = true;
          continue;
        }
        // Any other 401 code is a refusal by its status class, as for a read.
        if (code === "unauthenticated") end.e = { kind: "unauthenticated", problem: p };
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
      if (a.status === 409 && code === "conflict" && p?.["request"] !== undefined && p["epoch"] !== undefined) return aside("the key's record is from an earlier epoch");
      // A process refusal, decided before the lookup (§8.2 409 row).
      if (a.status === 409 && code === "recovery-mode-active" && p?.["scope"] === undefined) return early(refused);
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
        w.ambiguous = true;
        continue;
      }
      // This send's own answer: a repeat that found the key's record would have replayed it.
      return refused;
    }
    return w.ambiguous ? unknown() : { kind: "not-done", why: `every attempt was answered ${last}, which commits nothing`, problem: p503 };
  }

  // Before a repeat: the same sign-in, and one GET /recovery that shows the reference epoch.
  private async check(client: Client, w: Write, end: { e?: Ending }): Promise<string | undefined> {
    if (w.aside !== undefined) return w.aside;
    if (this.principal() !== w.principal) return SIGN_IN_CHANGED;
    const now = await this.confirm(client, end);
    if (w.aside !== undefined) return w.aside;
    if (end.e !== undefined) return SIGN_IN_REFUSED;
    if (now === undefined) return NO_EPOCH;
    return now === w.epoch ? undefined : EPOCH_CHANGED_ASIDE;
  }

  // The epoch one GET /recovery shows (§8.4 item 3). It is a read, so its credential refusals are
  // a read's (§8.2): a 401 renews once and is sent again, and a second 401 or identity-revoked
  // ends the page through `end`.
  private async confirm(client: Client, end: { e?: Ending }): Promise<string | undefined> {
    for (let renewed = false; ; renewed = true) {
      const a = await client.attempt("GET", "/recovery", {}, undefined, end);
      if (end.e !== undefined) return undefined;
      const p = a === undefined ? null : problemOf(a.data);
      const code = p?.type.slice(PREFIX.length);
      if (a?.status === 401 && code === "unauthenticated") {
        if (!renewed && (await client.renew())) continue;
        end.e = { kind: "unauthenticated", problem: p };
        return undefined;
      }
      if (a?.status === 403 && code === "identity-revoked") {
        client.revoked();
        end.e = { kind: "revoked", problem: p! };
        return undefined;
      }
      return a?.headers.get("Bronzeward-Epoch") ?? undefined;
    }
  }
}
