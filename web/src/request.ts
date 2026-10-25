// The request layer every page uses for API reads (client contracts §6 item 7, §8.2, §8.5, §10.3).
// Writes, their idempotency keys and ambiguous outcomes (§8.4) arrive with the first write view.

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

  constructor(private readonly opts: ClientOptions = {}) {}

  get currentEpoch(): string | undefined { return this.epoch; }

  // A change of principal, route or query makes every read in flight stale.
  setView(view: View): void {
    if (view.principal !== this.view.principal || view.route !== this.view.route || view.query !== this.view.query) {
      this.view = { ...view };
      this.generation++;
    }
  }

  async read<T>(path: string): Promise<ReadResult<T>> {
    const sent = this.generation;
    let renewed = false;
    for (let attempt = 0; ; attempt++) {
      const res = await this.send(path);
      if (res === undefined) return sent === this.generation ? { kind: "transport" } : { kind: "discarded" };
      // A stale response is discarded before its epoch is read, so a late one cannot roll it back.
      if (sent !== this.generation || this.observeEpoch(res)) return { kind: "discarded" };
      if (res.ok) return this.success<T>(res, sent);
      const problem = await readProblem(res);
      if (sent !== this.generation) return { kind: "discarded" }; // changed while the body arrived
      const code = problem?.type.slice(PROBLEM_PREFIX.length);
      if (res.status === 401 && problem !== null) {
        if (!renewed && this.opts.renew !== undefined) {
          renewed = true;
          const again = await this.opts.renew();
          if (sent !== this.generation) return { kind: "discarded" };
          if (again) { attempt--; continue; }
        }
        return { kind: "unauthenticated", problem };
      }
      if (res.status === 403 && code === "identity-revoked" && problem !== null) {
        this.opts.onRevoked?.();
        return { kind: "revoked", problem };
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

  private async send(path: string): Promise<Response | undefined> {
    const headers: Record<string, string> = { Accept: "application/json" };
    const token = this.opts.token?.();
    if (token !== undefined) headers["Authorization"] = `Bearer ${token}`;
    try {
      return await (this.opts.fetch ?? fetch)(API_BASE + path, {
        method: "GET",
        headers,
        cache: "no-store",
        redirect: "error",
        credentials: "omit",
      });
    } catch {
      return undefined;
    }
  }

  // §8.5: every current response's epoch against the last one seen; the first is recorded silently. A
  // response that carries a new epoch is itself discarded, since its read began under the old one.
  private observeEpoch(res: Response): boolean {
    const epoch = res.headers.get("Bronzeward-Epoch");
    if (epoch === null || epoch === this.epoch) return false;
    const first = this.epoch === undefined;
    this.epoch = epoch;
    if (first) return false;
    this.generation++;
    this.opts.onEpochChange?.(epoch, res.headers.get("Bronzeward-Recovery-Mode") === "true");
    return true;
  }

  // A body that fails to parse is a defect; one cut off by the network is a transport failure.
  private async success<T>(res: Response, sent: number): Promise<ReadResult<T>> {
    if (!res.headers.has("Bronzeward-Epoch")) return { kind: "defect", message: "a success carried no Bronzeward-Epoch header" };
    const body = await res.json().then((d: unknown) => ({ d }), (e: unknown) => ({ e }));
    if (sent !== this.generation) return { kind: "discarded" };
    if ("e" in body) return body.e instanceof SyntaxError ? { kind: "defect", message: "the response is not JSON" } : { kind: "transport" };
    return { kind: "ok", data: body.d as T };
  }
}

// A Bronzeward problem document, or null for any other answer (§8.2's last row).
async function readProblem(res: Response): Promise<Problem | null> {
  if (res.ok || !(res.headers.get("Content-Type") ?? "").startsWith("application/problem+json")) return null;
  const body: unknown = await res.json().catch(() => null);
  const type = typeof body === "object" && body !== null && "type" in body ? body.type : undefined;
  return typeof type === "string" && type.startsWith(PROBLEM_PREFIX) ? (body as Problem) : null;
}

const delay = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));
