// The request layer every page uses for API reads (client contracts §6 item 7, §8.2, §8.5, §10.3),
// and the single send the write layer (write.ts, §8.4) repeats under its own rules.

export const API_BASE = "/api/v1";

// Shown for every transport failure, including a refused redirect (§6 item 7, assumption S7):
// the page cannot tell them apart, and never names a redirect's target.
export const TRANSPORT_FAILURE =
  "The server could not be reached. The network may be down, or the server answered with a redirect, which this page refuses.";

export const EPOCH_CHANGED =
  "The installation entered recovery mode or was restored; what was read before may no longer hold";

export interface Problem {
  type: string;
  title: string;
  status: number;
  detail?: string;
  instance?: string;
  [extension: string]: unknown;
}

export type ReadResult<T> =
  | { kind: "ok"; data: T }
  // The principal, epoch, route or query changed while the read was in flight: not rendered.
  | { kind: "discarded" }
  // 400 cursor-invalid: drop the cursor and read the list from its start.
  | { kind: "restart"; problem: Problem }
  | { kind: "refused"; status: number; problem: Problem | null }
  | { kind: "unauthenticated"; problem: Problem | null }
  | { kind: "revoked"; problem: Problem }
  | { kind: "unavailable"; status: number; problem: Problem | null }
  | { kind: "transport" }
  | { kind: "defect"; message: string };

// A write's answer: a success's JSON or NOT_JSON; for a refusal its problem, null when the answer
// is not a Bronzeward problem, or NOT_JSON.
export interface Attempt {
  status: number;
  headers: Headers;
  data: unknown;
}

// A credential refusal that ends the signed-in page, as a read's does (§8.2 401 and 403 rows,
// §9.6): a 401 its one renewal did not clear, or identity-revoked of the sign-in still current.
export type Ending = { kind: "unauthenticated"; problem: Problem | null } | { kind: "revoked"; problem: Problem };

// What a response must still match to be rendered.
export interface View {
  principal: string;
  route: string;
  query: string;
}

export interface ClientOptions {
  token?: () => string | undefined;
  // Renewal (§9.5); a 401 is repeated once when it resolves true.
  renew?: () => Promise<boolean>;
  // 403 identity-revoked: clear the credential the client keeps (§8.2), never sign in by itself.
  onRevoked?: () => void;
  // §8.5 items 1 and 3: discard cursors, pages, ETags and stream positions; tell the operator.
  onEpochChange?: (epoch: string, recoveryMode: boolean) => void;
  // §8.5 item 3: the recovery banner shows while responses carry Bronzeward-Recovery-Mode: true,
  // from the first epoch on, and clears on a recovery exit, which keeps the epoch.
  onRecoveryModeChange?: (on: boolean) => void;
  // §7 item 4: a route answered with a Deprecation header, told once per route.
  onDeprecation?: (route: string) => void;
  fetch?: typeof fetch;
  sleep?: (ms: number) => Promise<void>;
}

const PROBLEM_PREFIX = "urn:bronzeward:problem:";
const RETRIED_503 = new Set(["dependency-unavailable", "talos-access-unavailable", "transient-conflict"]);
// §8.2 503 row for reads: 1 s doubling to 30 s, at most 5 tries.
const MAX_TRIES = 5;
const backoff = (attempt: number) => Math.min(1000 * 2 ** attempt, 30_000);

export class Client {
  private view: View = { principal: "", route: "", query: "" };
  private epoch: string | undefined;
  private generation = 0;
  private drops = 0;
  private epochRecords = 0;
  private recovery = false;
  private readonly deprecated = new Set<string>();

  constructor(private readonly opts: ClientOptions = {}) {}

  get currentEpoch(): string | undefined { return this.epoch; }
  get recoveryMode(): boolean { return this.recovery; }

  // A change of principal, route or query makes every read in flight stale.
  setView(view: View): void {
    if (view.principal !== this.view.principal || view.route !== this.view.route || view.query !== this.view.query) {
      this.view = { ...view };
      this.generation++;
    }
  }

  // Every read in flight is stale, under the same view: an answer still to come is discarded (a
  // revocation of the sign-in still current excepted, as for any stale read) and nothing is sent
  // again, not even to learn an ambiguous epoch.
  dropReads(): void {
    this.generation++;
    this.drops++;
  }

  async read<T>(path: string): Promise<ReadResult<T>> {
    const sent = this.generation;
    const drops = this.drops;
    const principal = this.view.principal;
    let renewed = false;
    for (let attempt = 0; ; attempt++) {
      const records = this.epochRecords;
      const res = await this.send(path);
      if (res === undefined) return sent === this.generation ? { kind: "transport" } : { kind: "discarded" };
      const route = API_BASE + path.split("?")[0];
      if (res.headers.has("Deprecation") && !this.deprecated.has(route)) {
        this.deprecated.add(route);
        this.opts.onDeprecation?.(route);
      }
      // Epochs are unordered: another epoch than one recorded after the send may be older or newer,
      // so the read is sent again to learn the current one. Any other is read, even on a stale read.
      const epoch = res.headers.get("Bronzeward-Epoch");
      if (epoch !== null && epoch !== this.epoch && records !== this.epochRecords) {
        if (attempt + 1 >= MAX_TRIES || drops !== this.drops) return { kind: "discarded" };
        continue;
      }
      // A 403 is read before a stale answer is dropped: identity-revoked of the sign-in still current
      // clears its credential at once (§8.2), even when the view moved on; its data stays dropped.
      const stale = this.observeEpoch(res) || sent !== this.generation;
      if (stale && !(res.status === 403 && principal === this.view.principal)) return { kind: "discarded" };
      if (res.ok) return this.success<T>(res, sent);
      const body = await readProblem(res);
      const problem = body === NOT_JSON || body === CUT ? null : body;
      const code = problem?.type.slice(PROBLEM_PREFIX.length);
      if (res.status === 403 && code === "identity-revoked" && problem !== null && principal === this.view.principal) {
        this.opts.onRevoked?.();
        return { kind: "revoked", problem };
      }
      if (stale || sent !== this.generation) return { kind: "discarded" }; // changed while the body arrived
      if (body === CUT) return { kind: "transport" };
      // A problem body that does not parse is a defect, as a success's is; a 5xx stays §8.2's last row.
      if (body === NOT_JSON && res.status < 500) return { kind: "defect", message: "the problem body is not JSON" };
      // Any other 401 code is a refusal by its status class (§8.2's unknown-code row).
      if (res.status === 401 && code === "unauthenticated") {
        if (!renewed && this.opts.renew !== undefined) {
          renewed = true;
          const again = await this.opts.renew();
          if (sent !== this.generation) return { kind: "discarded" };
          if (again) { attempt--; continue; }
        }
        return { kind: "unauthenticated", problem };
      }
      if (res.status === 400 && code === "cursor-invalid" && problem !== null) return { kind: "restart", problem };
      if (res.status < 500) return { kind: "refused", status: res.status, problem };
      // A 500, one of the three retried 503s, or a 5xx that is not a Bronzeward problem (§8.2's
      // last row) is read again; epoch-superseded never succeeds against this process, and any
      // other Bronzeward code is a refusal by its status class.
      const retried = problem === null || code === "internal-error" || (res.status === 503 && RETRIED_503.has(code ?? ""));
      if (!retried) return { kind: code === "epoch-superseded" ? "unavailable" : "refused", status: res.status, problem };
      if (attempt + 1 >= MAX_TRIES) return { kind: "unavailable", status: res.status, problem };
      await (this.opts.sleep ?? delay)(backoff(attempt));
      if (sent !== this.generation) return { kind: "discarded" };
    }
  }

  // One send of a write, or of its confirming read, for the write layer (write.ts): never repeated
  // here and never discarded as stale. Its epoch is observed as a read's is, so a new one sets every
  // pending write aside through onEpochChange. Undefined when no complete answer arrived, a redirect
  // refused by the browser included (§6 item 7). A credential refusal of a read sent to learn its
  // epoch goes to `end`, even when this answer is then lost.
  async attempt(method: string, path: string, headers: Record<string, string> = {}, body?: string, end: { e?: Ending } = {}): Promise<Attempt | undefined> {
    const records = this.epochRecords;
    const res = await this.send(path, method, headers, body);
    if (res === undefined) return undefined;
    const ending = await this.learnEpoch(res, records);
    if (ending !== undefined) end.e = ending;
    const route = API_BASE + path.split("?")[0];
    if (res.headers.has("Deprecation") && !this.deprecated.has(route)) {
      this.deprecated.add(route);
      this.opts.onDeprecation?.(route);
    }
    const data = res.ok ? await readJson(res) : await readProblem(res);
    if (data === CUT) return undefined;
    return { status: res.status, headers: res.headers, data };
  }

  // §9.5 renewal, for a write's 401: true when the page holds a renewed token.
  renew(): Promise<boolean> {
    return this.opts.renew?.() ?? Promise.resolve(false);
  }

  // §8.2 identity-revoked of the sign-in still current, from a write's answer.
  revoked(): void {
    this.opts.onRevoked?.();
  }

  private async send(path: string, method = "GET", extra: Record<string, string> = {}, body?: string): Promise<Response | undefined> {
    const headers: Record<string, string> = { Accept: "application/json", ...extra };
    const token = this.opts.token?.();
    if (token !== undefined) headers["Authorization"] = `Bearer ${token}`;
    try {
      return await (this.opts.fetch ?? fetch)(API_BASE + path, {
        method,
        headers,
        ...(body === undefined ? {} : { body }),
        cache: "no-store",
        redirect: "error",
        credentials: "omit",
      });
    } catch {
      return undefined;
    }
  }

  // Epochs are unordered (§8.5). An answer that names another epoch than one recorded after its
  // send may be the older one, so it is not adopted: GET /recovery is read, at most MAX_TRIES times,
  // until an answer names the epoch recorded last or nothing was recorded since its own send, as a
  // read is sent again. The write itself is never sent again here. Each read's credential refusals
  // are a read's (§8.2): a 401 renews once and reads again; a second 401 or identity-revoked stops
  // and is returned, the epoch unlearned.
  private async learnEpoch(res: Response, records: number): Promise<Ending | undefined> {
    let renewed = false;
    for (let tries = 0; ; tries++) {
      const epoch = res.headers.get("Bronzeward-Epoch");
      if (epoch === null || epoch === this.epoch || records === this.epochRecords) {
        this.observeEpoch(res);
        return undefined;
      }
      if (tries + 1 >= MAX_TRIES) return undefined;
      const sent = this.epochRecords;
      const next = await this.send("/recovery");
      if (next === undefined) return undefined;
      const body = next.ok ? await next.arrayBuffer().then(() => null, () => null) : await readProblem(next);
      const problem = body === NOT_JSON || body === CUT ? null : body;
      const code = problem?.type.slice(PROBLEM_PREFIX.length);
      if (next.status === 401 && code === "unauthenticated") {
        if (renewed || !(await this.renew())) return { kind: "unauthenticated", problem };
        // `res` is still the answer to learn from: the next turn reads again.
        renewed = true;
        continue;
      }
      if (next.status === 403 && code === "identity-revoked" && problem !== null) {
        this.revoked();
        return { kind: "revoked", problem };
      }
      res = next;
      records = sent;
    }
  }

  // §8.5: a response's epoch against the last one seen; the first is recorded silently. A response
  // that carries a new epoch is itself discarded, since its read began under the old one.
  private observeEpoch(res: Response): boolean {
    const epoch = res.headers.get("Bronzeward-Epoch");
    if (epoch === null) return false;
    const recovery = res.headers.get("Bronzeward-Recovery-Mode") === "true";
    // An entry mints an epoch in recovery mode and its exit keeps that epoch, so within one epoch
    // the mode only turns off: a late response from before the exit cannot turn it back on.
    if (epoch === this.epoch) {
      if (!recovery) this.setRecoveryMode(false);
      return false;
    }
    const first = this.epoch === undefined;
    this.epoch = epoch;
    this.epochRecords++;
    if (!first) {
      this.generation++;
      this.opts.onEpochChange?.(epoch, recovery);
    }
    this.setRecoveryMode(recovery);
    return !first;
  }

  private setRecoveryMode(on: boolean): void {
    if (on === this.recovery) return;
    this.recovery = on;
    this.opts.onRecoveryModeChange?.(on);
  }

  // A body that fails to parse is a defect; one cut off by the network is a transport failure.
  private async success<T>(res: Response, sent: number): Promise<ReadResult<T>> {
    if (!res.headers.has("Bronzeward-Epoch")) return { kind: "defect", message: "a success carried no Bronzeward-Epoch header" };
    const body = await readJson(res);
    if (sent !== this.generation) return { kind: "discarded" };
    if (body === CUT) return { kind: "transport" };
    if (body === NOT_JSON) return { kind: "defect", message: "the response is not JSON" };
    return { kind: "ok", data: body as T };
  }
}

// A body's JSON; NOT_JSON when it does not parse, CUT when the network cut it off.
export const NOT_JSON = Symbol("not JSON");
export const CUT = Symbol("cut off");
export async function readJson(res: Response): Promise<unknown> {
  return res.json().catch((e: unknown) => (e instanceof SyntaxError ? NOT_JSON : CUT));
}

// A Bronzeward problem document, null for any other answer (§8.2's last row), NOT_JSON or CUT.
async function readProblem(res: Response): Promise<Problem | null | typeof NOT_JSON | typeof CUT> {
  if (res.ok) return null;
  // Any other answer's body is read too, so one cut off by the network is still a transport failure.
  if (!(res.headers.get("Content-Type") ?? "").startsWith("application/problem+json")) {
    return res.arrayBuffer().then(() => null, () => CUT);
  }
  const body = await readJson(res);
  if (body === CUT || body === NOT_JSON) return body;
  const type = typeof body === "object" && body !== null && "type" in body ? body.type : undefined;
  return typeof type === "string" && type.startsWith(PROBLEM_PREFIX) ? (body as Problem) : null;
}

const delay = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));
