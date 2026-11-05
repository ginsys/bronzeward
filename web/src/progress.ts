// The operation progress reader (decision B16 of #257, client contracts §8.6 items 1-5), which the
// import, publication and plan pages share. It follows the CLI's reader (internal/client/watch.go)
// step by step: the stream first, the JSON pages when the server answers without one, the
// operation read whenever a stream ends, and the backlog drained on a terminal state.

import type { components } from "./api/v1";
import { type Failed, readPages } from "./list.tsx";
import { type Client, NOT_SENT } from "./request.ts";

export type Operation = components["schemas"]["OperationBody"];

// One event, from the stream or the JSON pages: its id is `<epoch>:<number>` (persistence and API
// §8.3). `data` is the event's own data; the JSON pages' `at` is not kept.
export interface OpEvent { id: string; type: string; data: unknown }

// An event as the CLI's watch prints it: its type, generation and code. No event carries input text.
export function eventLine(e: OpEvent): string {
  const d = (typeof e.data === "object" && e.data !== null ? e.data : {}) as { generation?: number; code?: string };
  return `${e.type}${d.generation === undefined ? "" : ` (generation ${d.generation})`}${d.code === undefined ? "" : `: ${d.code}`}`;
}

export const RESTORED ="The installation was restored or entered recovery mode, so the operation's events are shown again from the start.";
export const RESTORED_WHILE_READ =
  "The installation entered recovery mode or was restored while the events were read, so the operation's events are shown again from the start.";

// How a watch shows what it reads. `show` gets each event once, in order. `clear` says that what was
// shown no longer holds, before the events are shown again from the start. `until`, when set, ends
// the watch before the operation is terminal: it is asked after each event shown, at each
// keep-alive and after each read of the operation, and once it answers true it stays true.
// `signal` ends the watch when the page leaves.
export interface Watch {
  show: (e: OpEvent) => void;
  clear: (why: string) => void;
  until?: () => boolean;
  signal: AbortSignal;
}

export type Watched =
  // The operation as last read: terminal with every event shown, or `until` answered true.
  | { kind: "ended"; operation: Operation }
  | { kind: "stopped" }
  // A refusal or a defect of the operation's read, its events' read or its stream.
  | { kind: "failed"; res: Failed };

export interface Pace {
  sleep: (ms: number, signal: AbortSignal) => Promise<void>;
  now: () => number;
  // A stream that sends nothing, not even its keep-alive comment every 15 s, for this long is taken
  // as cut (§8.6 item 5). The wait starts at the request, so an answer that never sends its headers
  // ends the same way. The stream has no other client timeout (§8.4 item 8; the CLI's watch.go).
  idle: number;
}

const PACE: Pace = {
  sleep: (ms, signal) => new Promise((done) => {
    const t = setTimeout(done, ms);
    signal.addEventListener("abort", () => { clearTimeout(t); done(); }, { once: true });
  }),
  now: () => Date.now(),
  idle: 45_000,
};

// No event follows these states. `unresolved` is not terminal: a recovery administrator resolves it.
export const terminal = (state: string) => ["succeeded", "failed", "completed", "rejected", "cancelled"].includes(state);

const epochOf = (id: string) => id.split(":", 1)[0]!;
const numberOf = (id: string) => Number(id.slice(id.indexOf(":") + 1));

type Reader = Pick<Client, "read" | "open" | "currentEpoch" | "writesEnded">;

// What one watch shares between its stream, its pages and its loop.
class Run {
  last = ""; // the ID of the last event shown
  // What the watch showed: an attempt that showed nothing is a failure. A clear is not counted, so a
  // stream whose events never match its epoch, cleared on every attempt, backs off (the CLI's watch.go).
  steps = 0;
  halted = false;
  constructor(readonly w: Watch) {}

  stop(): boolean {
    this.halted ||= this.w.until?.() === true;
    return this.halted;
  }

  // Shows `e` unless it is not after the last event shown, which a stream and the pages after it
  // can both carry.
  show(e: OpEvent): void {
    if (this.last !== "" && epochOf(e.id) === epochOf(this.last) && numberOf(e.id) <= numberOf(this.last)) return;
    this.steps++;
    this.w.show(e);
    this.last = e.id;
  }

  // What `until` answered held for the events now cleared: it is asked again.
  clear(why: string): void {
    this.halted = false;
    this.last = "";
    this.w.clear(why);
  }
}

// Follows the operation `op` until its state is terminal and every event before it was shown. A
// stream that failed to open, or ended within a second having shown nothing, is retried after 1 s,
// doubling to 30 s; JSON pages are read again at the same pace. Renewal is the page's renewer's: the
// reads renew a refused token, and each stream is opened with the token current at that moment.
export async function watch(client: Reader, op: string, w: Watch, pace: Pace = PACE): Promise<Watched> {
  const run = new Run(w);
  const path = `/operations/${encodeURIComponent(op)}`;
  let pages = false;
  let delay = 1000;
  for (;;) {
    // The sign-in ended: nothing more is sent. The JSON pages never reach the stream's check for it.
    if (w.signal.aborted || client.writesEnded) return { kind: "stopped" };
    const before = run.steps;
    const began = pace.now();
    const s: Streamed = pages ? "pages" : await stream(client, path, run, pace);
    // The sign-in ended: nothing more is sent, and the page that asked is ending too.
    if (s === "signed-out") return { kind: "stopped" };
    if (typeof s === "object") return { kind: "failed", res: s };
    const opened = s !== "unopened";
    pages = s === "pages";
    // A refusal of the stream is read as a page: its read renews a refused token, and reports any
    // other refusal by its status.
    if (s === "pages" || s === "refused") {
      const drained = await drain(client, path, run, true);
      if (typeof drained === "object") return { kind: "failed", res: drained };
    }
    // The attempt's own time: a slow operation read does not skip the backoff.
    const took = pace.now() - began;
    if (w.signal.aborted) return { kind: "stopped" };
    const res = await client.read<Operation>(path);
    if (res.kind === "ok" && terminal(res.data.state)) {
      // The epoch this read held, taken before the drain reads anything.
      const epoch = client.currentEpoch;
      // A terminal state settles the watch, so the last events are all shown, until or not.
      run.halted = false;
      const drained = await drain(client, path, run, false);
      if (drained === "read") {
        if (w.signal.aborted) return { kind: "stopped" };
        if (epoch === undefined || run.last === "" || epochOf(run.last) === epoch) return { kind: "ended", operation: res.data };
        // Events of another epoch than the operation read's are of a restored installation, where the
        // operation can be in another state: it is read again, after the delay so a server whose
        // events never match is not asked again without a pause (the CLI's watch.go).
        await pace.sleep(delay, w.signal);
        delay = Math.min(2 * delay, 30_000);
        continue;
      }
      if (drained !== "discarded") return { kind: "failed", res: drained };
    } else if (res.kind === "ok") {
      if (run.stop()) return { kind: "ended", operation: res.data };
    } else if (res.kind !== "discarded" && res.kind !== "transport" && res.kind !== "unavailable") {
      return { kind: "failed", res };
    }
    // A read discarded for a new epoch, or with no answer, is read again: the stream that follows
    // starts from the reset. JSON pages are read again after the delay, which starts again at 1 s
    // once they show something new.
    const progress = run.steps !== before;
    if (progress) delay = 1000;
    if (pages || res.kind !== "ok" || (!progress && (!opened || took < 1000))) {
      await pace.sleep(delay, w.signal);
      if (!progress) delay = Math.min(2 * delay, 30_000);
    }
  }
}

// Reads the JSON event pages to their end and shows every event after the last one shown (§8.6
// item 2). Events of another epoch than the last one shown are read after a restore or a recovery
// entry: what was shown is cleared and every event shown again. A read discarded for a new epoch,
// or with no answer, shows nothing: the loop reads again.
async function drain(client: Reader, path: string, run: Run, stoppable: boolean): Promise<"read" | "discarded" | Failed> {
  const read = await readPages<OpEvent>(client, `${path}/events`, "", { items: [], next: undefined, restarted: false }, true, () => !run.w.signal.aborted);
  const res = read.kind === "dropped" ? read.failed : read;
  if (res.kind === "discarded" || res.kind === "transport" || res.kind === "unavailable") return "discarded";
  if (res.kind !== "ok") return res;
  for (const [i, e] of res.data.items.entries()) {
    if (i === 0 && run.last !== "" && epochOf(e.id) !== epochOf(run.last)) run.clear(RESTORED);
    run.show({ id: e.id, type: e.type, data: e.data });
    if (stoppable && run.stop()) break;
  }
  return "read";
}

type Streamed = "unopened" | "ended" | "pages" | "refused" | "signed-out" | Failed;

// Reads the operation's event stream once, showing each event after the last one shown. "pages"
// when the server answers without a stream. A transport failure, a 5xx or a cut connection end it as
// "unopened" or "ended": the watch reads the operation next.
async function stream(client: Reader, path: string, run: Run, pace: Pace): Promise<Streamed> {
  const cut = new AbortController();
  const leave = () => cut.abort();
  run.w.signal.addEventListener("abort", leave, { once: true });
  let idle = setTimeout(leave, pace.idle);
  try {
    const headers: Record<string, string> = { Accept: "text/event-stream" };
    if (run.last !== "") headers["Last-Event-ID"] = run.last;
    const res = await client.open(`${path}/events`, headers, cut.signal);
    if (res === NOT_SENT) return "signed-out";
    if (res === undefined) return "unopened";
    if (res.status !== 200) return res.status >= 500 ? "unopened" : "refused";
    if (!(res.headers.get("Content-Type") ?? "").startsWith("text/event-stream")) return "pages";
    const epoch = res.headers.get("Bronzeward-Epoch");
    if (epoch === null || res.body === null) return { kind: "defect", message: "the event stream carried no Bronzeward-Epoch header" };
    const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
    let buf = "";
    let ev = { id: "", type: "", data: [] as string[] };
    for (;;) {
      // A cut connection, the idle cut or leaving the page rejects the read.
      const chunk = await reader.read().catch(() => undefined);
      if (chunk === undefined || chunk.done) return "ended";
      buf += chunk.value;
      for (let i = buf.indexOf("\n"); i >= 0; i = buf.indexOf("\n")) {
        // Every complete line starts the wait again, and bytes of a line still open do not: a
        // connection that trickles bytes with no line feed is cut as a silent one is (§8.6 item 5).
        clearTimeout(idle);
        idle = setTimeout(leave, pace.idle);
        const line = buf.slice(0, i).replace(/\r$/, "");
        buf = buf.slice(i + 1);
        if (line.startsWith(":")) {
          // A comment: the keep-alive (§8.6 item 5).
          if (run.stop()) return "ended";
          continue;
        }
        if (line !== "") {
          const at = line.indexOf(":");
          const [field, value] = at < 0 ? [line, ""] : [line.slice(0, at), line.slice(at + 1).replace(/^ /, "")];
          if (field === "id") ev.id = value;
          else if (field === "event") ev.type = value;
          else if (field === "data") ev.data.push(value);
          continue;
        }
        if (ev.type === "reset") {
          run.clear(RESTORED);
        } else if (ev.id !== "" && epochOf(ev.id) !== epoch) {
          // The installation entered recovery mode while the stream stayed open (§8.6 item 3): the
          // operation read that follows applies §8.5, and the stream reopens from the start.
          run.clear(RESTORED_WHILE_READ);
          return "ended";
        } else if (ev.id !== "") {
          let data: unknown;
          try {
            data = JSON.parse(ev.data.join("\n"));
          } catch {
            return { kind: "defect", message: "an event's data is not JSON" };
          }
          run.show({ id: ev.id, type: ev.type, data });
          if (run.stop()) return "ended";
        }
        ev = { id: "", type: "", data: [] };
      }
    }
  } finally {
    clearTimeout(idle);
    run.w.signal.removeEventListener("abort", leave);
    cut.abort();
  }
}
