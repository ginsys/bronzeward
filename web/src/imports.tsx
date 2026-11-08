// The import pages (#340, decision B15 of #257): the wizard, and the import page with its progress
// (B16, progress.ts) and the draft's ETag after a released import (client contracts §8.3, ETAG-02).
// They follow the CLI's import start and status (cmd/bronzewardctl/imports.go). A document or marks
// are protected input (§11.1-§11.4): held in this page's memory only, sent once, and dropped once
// the start is answered. The review, continue, takeover and abandon controls are W5's (claim.tsx).

import { type ReactNode, useCallback, useEffect, useMemo, useReducer, useRef, useState } from "react";
import type { components } from "./api/v1";
import { Claim } from "./claim.tsx";
import { keepETag, keptETag, NO_ASSIST } from "./drafts.tsx";
import { Form, field, useSend, useUnsaved, useWrites } from "./forms.tsx";
import { Id, type Load, type Reads, Ref, short, useItem, useList, usePending, useReads, useSettled } from "./list.tsx";
import { markLines } from "./marks.tsx";
import { eventLine, type Refusal, refusalOf, refusalText, terminal, type Watch, watch } from "./progress.ts";
import type { Client, ReadResult } from "./request.ts";
import { Action } from "./shell.tsx";
import { Status, when } from "./states.tsx";
import type { Outcome, WriteSpec } from "./write.ts";

type S = components["schemas"];
export type Ingestion = S["IngestionBody"];
type Draft = S["DraftBody"];
type Machine = S["MachineBody"];
type Operation = S["OperationBody"];

const enc = encodeURIComponent;
// The server's limit on an ingestion's document (IngestionInput.document).
export const MAX_DOCUMENT = 512 << 10;
// How long after the lease it last read the page reads the ingestion again, as the CLI does.
export const LEASE_MARGIN = 10_000;

// The imports this page started, each with its draft and the epoch its write was sent under: only
// their success under that same epoch adopts the draft's ETag (§8.3, §8.5). A revision can be issued
// again with other content after a restore (persistence API, "do rewind with a restore"), so an
// import of an earlier epoch is not matched by the draft's revision alone. In this page's memory only.
export const started = new Map<string, { draft: string; epoch: string | undefined }>();

export const againPath = (b: Pick<Ingestion, "machine" | "draft">) =>
  `/ingestions/new?${new URLSearchParams({ ...(b.machine === null ? {} : { machine: b.machine }), draft: b.draft })}`;
const ImportAgain = ({ b }: { b: Pick<Ingestion, "machine" | "draft"> }) => <Ref to={againPath(b)} id={b.draft}>Import again</Ref>;

const running = (b: Ingestion) => b.state === "held" || b.state === "resumed";
export const lapsed = (b: Ingestion, now: number) => running(b) && now > Date.parse(b.leaseUntil);
const lease = (b: Ingestion, now: number) => (!running(b) ? "none" : `${lapsed(b, now) ? "lapsed at" : "until"} ${when(b.leaseUntil)}`);

// What an ingestion's state allows, as the CLI's import status says it. A state this client does not
// know is shown as ended (G7): the API types it as a plain string.
export function allowed(b: Ingestion, now: number): ReactNode {
  switch (b.state) {
    case "paused":
      return "Paused for review. Continue it, or abandon it.";
    case "held":
    case "resumed":
      // Only encrypted staging can be taken over; a transient one is abandoned and imported again (the CLI's import status).
      if (b.mode === "transient") {
        return lapsed(b, now)
          ? "Its run stopped renewing the lease. Transient staging cannot be taken over: abandon it and import again."
          : "Its run holds the claim. Transient staging cannot be taken over; it can be abandoned now.";
      }
      return lapsed(b, now)
        ? "Its run stopped renewing the lease. Take it over to resume, or abandon it."
        : "Its run holds the claim. Once the lease lapses it can be taken over. It can be abandoned now.";
    case "released":
      return <>Released into draft <Id id={b.draft} />. Nothing is left to do.</>;
  }
  return <>Ended: {b.state}. <ImportAgain b={b} /></>;
}

const code = (error: unknown) => {
  const t = typeof error === "object" && error !== null && "type" in error ? String(error.type) : "";
  return t.replace(/^urn:bronzeward:problem:/, "");
};

// Why a mark was not applied although its operation succeeded: refused at its pause, its staged input
// not decryptable, or no pause at its generation at all.
const notApplied = (last: string, run: Pick<RunState, "refused" | "replacedBy">, o: Operation) =>
  run.replacedBy !== undefined ? `a takeover at generation ${run.replacedBy} resumed the import without it, and operation ${short(o.id)} then released it.`
  : run.refused !== undefined ? `it was refused (${refusalText(run.refused)}), and operation ${short(o.id)} then released the import without it.`
  : last === "resume-failed" ? `its staged input could not be decrypted (resume-failed), and operation ${short(o.id)} then released the import without it.`
  : `operation ${short(o.id)} succeeded without pausing at the mark's generation.`;

// What a watch's end says. A success of an import this page started adopts the draft's ETag only at
// the import's own draft revision (ETAG-02): a higher one means another write followed, treated as a
// 412. `epoch` is that of the operation read the watch ended on, and the ETag is kept only when the
// draft's own read has the same epoch: an ETag read under another epoch is not kept (§8.5, "discards
// every ... ETag", and the commands that read twice compare the epoch of the answers themselves).
export async function ended(client: Pick<Client, "readWithEpoch">, b: Ingestion, o: Operation, last: string, now: number, epoch: string | undefined, run?: Pick<RunState, "marked" | "refused" | "replacedBy">): Promise<ReactNode> {
  if (o.state === "succeeded") {
    const r = o.result as S["IngestResult"] | null;
    if (r === null || typeof r.draftRevision !== "number") return `Import operation ${short(o.id)} succeeded without naming its draft revision.`;
    // A mark that no pause applied is not applied, whatever released the import after it (the CLI's markOutcome).
    const lost = run !== undefined && ((run.marked && (last !== "paused" || run.refused !== undefined)) || run.replacedBy !== undefined) ? `The mark was not applied: ${notApplied(last, run, o)} ` : "";
    const done = <>{lost}Import {short(b.id)} released into draft <Id id={r.draft} /> (revision {r.draftRevision}), import base {short(r.importBaseRevision)}.</>;
    const own = started.get(b.id);
    if (own?.draft !== r.draft) return done;
    if (own.epoch === undefined || own.epoch !== epoch) return <>{done} The installation entered recovery mode or was restored since this import started, so the draft's ETag is not kept. Open it before writing again.</>;
    const { result: d, epoch: read } = await client.readWithEpoch<Draft>(`/drafts/${enc(r.draft)}`);
    if (d.kind !== "ok" || d.etag === undefined) return <>{done} The draft could not be read again, so its ETag is not kept. Open it before writing again.</>;
    if (read !== epoch) return <>{done} The installation entered recovery mode or was restored while the draft was read again, so its ETag is not kept. Open it before writing again.</>;
    if (d.data.revision !== r.draftRevision) {
      return <>{done} Draft {short(r.draft)} is now revision {d.data.revision}: another write followed this import's revision {r.draftRevision}. Its ETag is not kept. <Ref to={`/drafts/${r.draft}`} id={r.draft}>Review the change</Ref> before writing again.</>;
    }
    keepETag(r.draft, d.etag, epoch);
    return done;
  }
  if (terminal(o.state)) {
    const reasons: Record<string, string> = {
      "precondition-failed": " The draft changed after this import started, so the import was not applied and its claim is abandoned.",
      "ingestion-abandoned": " The ingestion was abandoned, so nothing of it reached the draft.",
    };
    const why = reasons[code(o.error)] ?? "";
    return <>Import {short(b.id)} (operation {short(o.id)}) {o.state}: {code(o.error) || "no code"}.{why} <ImportAgain b={b} /></>;
  }
  // An abandoned claim whose operation has no recorded end: the pause the events replay is not current.
  if (b.state === "abandoned") return `Import ${short(b.id)} is abandoned, but operation ${short(o.id)} has no recorded end yet: its outcome is unknown. Read it again later.`;
  if (last === "paused" && run?.refused !== undefined) {
    return `The mark was not applied: it was refused (${refusalText(run.refused)}). The import is paused again (expires ${when(b.expiresAt)}). Review it again and mark again, continue it, or abandon it.`;
  }
  if (last === "paused" && run?.replacedBy !== undefined) {
    return `The earlier mark was not applied: a takeover at generation ${run.replacedBy} resumed the import without it. The import is paused again (expires ${when(b.expiresAt)}). Review it again and mark again, continue it, or abandon it.`;
  }
  if (last === "paused" && run?.marked) return `The marks were applied: re-staged and paused again (expires ${when(b.expiresAt)}). Review again, continue or abandon it.`;
  if (last === "paused") return `Paused for review (expires ${when(b.expiresAt)}).`;
  if (last === "resume-failed") return `Import ${short(b.id)} was not applied: its staged input could not be decrypted (resume-failed). The claim stays held until its lease lapses at ${when(b.leaseUntil)}; then take it over, or abandon it now.`;
  if (lapsed(b, now)) {
    const next = b.mode === "transient" ? "or abandon it (transient staging cannot be taken over)" : "take it over, or abandon it";
    // A takeover discarded the mark whatever the takeover's own run does next, so that part is known.
    const lost = run?.replacedBy !== undefined ? `The earlier mark was not applied: a takeover at generation ${run.replacedBy} resumed the import without it. ` : "";
    return `${lost}Its claim is ${b.state} and its lease lapsed at ${when(b.leaseUntil)}, while operation ${short(o.id)} still runs: the outcome is unknown. Read it again later, ${next}.`;
  }
  return undefined;
}

// The import page (/ingestions/{id}): a reload reads the ingestion and shows what its state allows.
export function IngestionItem({ id }: { id: string }) {
  const [n, setN] = useState(0);
  const { again: shellAgain, view } = useReads();
  const again = useCallback(() => setN((x) => x + 1), []);
  // A read that found no answer keeps the import shown and is tried again (item J).
  const [refreshFailed, setRefreshFailed] = useState(false);
  const retry = useMemo(() => refreshRetry(again, setRefreshFailed), [again]);
  useEffect(() => retry.stop, [retry]);
  const has = useRef(false);
  const keep = useMemo(() => ({ held: () => has.current, lost: retry.lost }), [retry]);
  const item = useItem<Ingestion>(`/ingestions/${enc(id)}`, keep, n);
  has.current = item.kind === "ok";
  const pending = usePending(item, "the import");
  const b = item.kind === "ok" ? item.data : undefined;
  const now = Date.now();
  const stamp = useRef<ReadStamp>({ keys: [], began: now, read: undefined });
  stamp.current = stampRead(stamp.current, b, [n, shellAgain, view], now);
  return (
    <>
      <h2>Import {short(id)}{b === undefined ? "" : `: ${b.state}`}</h2>
      {b === undefined ? pending : (
        <>
          <dl>
            <dt>State</dt><dd>{b.state}, {b.kind}, {b.mode} staging</dd>
            <dt>Machine</dt><dd><Id id={b.machine} /></dd>
            <dt>Draft</dt><dd><Id id={b.draft} /></dd>
            <dt>Operation</dt><dd><Id id={b.operation} /></dd>
            <dt>Review</dt><dd>{b.review ?? "not requested"}</dd>
            <dt>Owner generation</dt><dd>{b.ownerGeneration}</dd>
            <dt>Lease</dt><dd>{lease(b, now)}</dd>
            <dt>Expires</dt><dd>{when(b.expiresAt)}</dd>
          </dl>
          <Status testid="allowed">{allowed(b, now)}</Status>
          <Claim key={b.id} b={b} again={again} now={now} />
          {refreshFailed && <Status testid="refresh-failed">{REFRESH_FAILED}</Status>}
          <Progress b={b} readAt={stamp.current.read?.at ?? now} again={again} />
        </>
      )}
    </>
  );
}

// The events of the run that holds the claim now (the CLI's follow, imports.go): the operation's
// events are shown from its start, so a pause or resume-failed of an earlier run settles nothing.
// `gen` is the last generation a run started at; the first run (generation 1) is ours from the start.
// A mark's run also keeps that it took the claim (`marked`) and, when compilation refused it, why
// (`refused`: the rule and position, never a path). Only a pause at the mark's generation applies it.
// A takeover resumes without a mark that no pause or refusal at its generation had ended (`open`, the
// generation of that mark), so the mark is not applied (`replacedBy`: the takeover's generation, §11.3).
// `open` follows every event, ours or not: the watch restarts at the takeover and reads from the start.
export interface RunState { ours: boolean; gen: number; settled?: "paused" | "resume-failed"; marked?: true; refused?: Refusal; open?: number | undefined; replacedBy?: number | undefined }
export const startRun = (ownerGeneration: number): RunState => ({ ours: ownerGeneration <= 1, gen: ownerGeneration - 1 });
export function onEvent(s: RunState, type: string, data: unknown): RunState {
  const g = (data as { generation?: unknown } | null)?.generation;
  const gen = typeof g === "number" ? g : 0;
  const open = type === "marked" ? gen : (type === "paused" || type === "mark-refused") && gen === s.open ? undefined : s.open;
  const t = open === s.open ? s : { ...s, open };
  if (["continued", "taken-over", "marked"].includes(type) && gen > t.gen) {
    if (type === "taken-over") return { ours: true, gen, replacedBy: open === undefined ? t.replacedBy : gen };
    if (type === "marked") return { ours: true, gen, open, marked: true as const };
    // A continuation after the pause keeps what is known of the earlier mark: its refusal, or the takeover that discarded it.
    return { ours: true, gen, open, ...(t.refused !== undefined && { marked: true as const, refused: t.refused }), ...(t.replacedBy !== undefined && { replacedBy: t.replacedBy }) };
  }
  if (!t.ours) return t;
  if (type === "mark-refused" && gen >= t.gen) return { ...t, gen, refused: refusalOf(data) };
  if (type === "paused" && gen >= t.gen) return { ...t, ours: true, gen, settled: "paused" };
  if (type === "resume-failed") return { ...t, settled: "resume-failed" };
  return t;
}
// The watch ends on a settled run, or on a read of the ingestion made once the lease it showed had
// passed LEASE_MARGIN ago that still shows the claim lapsed (§11.3, PROT-06): a read made earlier
// can predate a renewal, so an event before the refresh settles nothing. An abandoned claim has no run
// left to settle: the expiry can read abandoned before the sweep fails the operation, and the restarted
// watch replays the old pause, so the replayed events settle nothing. It ends at the operation's
// terminal event, or when ABANDONED_SETTLE has passed since the page first read the claim abandoned.
// A lapsed lease decides only once `caught` says the operation's newest event was shown, because the
// events not yet shown can hold the run's outcome (the CLI's Until, imports.go).
export const lapsedRead = (read: { b: Ingestion; at: number }, now: number) =>
  read.b.state !== "abandoned" && read.at > Date.parse(read.b.leaseUntil) + LEASE_MARGIN && lapsed(read.b, now);
export const stopsWatch = (settled: RunState["settled"], read: { b: Ingestion; at: number }, now: number, abandonedAt?: number, caught = false) =>
  read.b.state === "abandoned"
    ? abandonedAt !== undefined && now - abandonedAt >= ABANDONED_SETTLE
    : settled !== undefined || (caught && lapsedRead(read, now));

// The epoch and number in an event's ID, `<epoch>:<number>`.
export interface Seen { epoch: string; n: number }
const seenOf = (id: string): Seen => ({ epoch: id.split(":", 1)[0]!, n: Number(id.slice(id.indexOf(":") + 1)) });
// Whether the events shown reach the operation's newest one. Both reads must be of the epoch of the
// events shown: after a restore the operation can have fewer events, and its last number would then
// read as shown, so a differing epoch decides nothing (§8.5; the CLI compares the same way). A read
// that named no epoch is not a mismatch.
export function caughtUp(shown: Seen | undefined, claimEpoch: string | undefined, opEpoch: string | undefined, lastEvent: number): boolean | "reset" {
  if (shown !== undefined && (claimEpoch !== undefined && claimEpoch !== shown.epoch || opEpoch !== undefined && opEpoch !== shown.epoch)) return "reset";
  return (shown?.n ?? 0) >= lastEvent;
}

// The client the watch and its end read through. Each read's answer goes to the page's `settle` as
// the list and item reads' answers do, so an unresolved 401 or a revocation reaches the shell (sign-in,
// revocation) and a transport failure its offline banner, and nothing is rendered once the watch left.
// `undropped` is taken per read: the shell can invalidate reads in flight between two of them.
export function settledReads(client: Pick<Client, "read" | "readWithEpoch" | "open" | "writesEnded">, reads: Pick<Reads, "settle" | "undropped">, live: () => boolean): Pick<Client, "read" | "readWithEpoch" | "open" | "writesEnded"> {
  const settle = <T,>(res: ReadResult<T>, ok: () => boolean) => void reads.settle(res, () => {}, () => live() && ok(), () => true);
  return {
    read: async <T,>(path: string) => {
      const ok = reads.undropped();
      const res = await client.read<T>(path);
      settle(res, ok);
      return res;
    },
    readWithEpoch: async <T,>(path: string) => {
      const ok = reads.undropped();
      const got = await client.readWithEpoch<T>(path);
      settle(got.result, ok);
      return got;
    },
    open: (path, headers, signal) => client.open(path, headers, signal),
    get writesEnded() { return client.writesEnded; },
  };
}

export interface FollowOptions {
  client: Pick<Client, "readWithEpoch">;
  ingestion: string; op: string; generation: number; signal: AbortSignal;
  latest: () => { b: Ingestion; at: number };
  abandonedAt: () => number | undefined;
  now: () => number;
  view: (a: WatchAct) => void;
}
// The callbacks of one watch of an import's operation, and the run it settled. A lapsed lease read
// starts one check: the claim and the operation are read again, and the lease decides only when the
// last event number shown reaches the operation's `lastEvent` (the CLI's Until). The check's answer is
// used at the next ask, which the watch makes after each event, keep-alive and operation read.
export function followRun(o: FollowOptions): { w: Watch; settled: () => RunState["settled"]; state: () => RunState } {
  let run = startRun(o.generation);
  let shown: Seen | undefined;
  let caught = false, busy = false, behind: number | undefined, epoch = 0;
  const check = async () => {
    busy = true;
    const mine = epoch;
    try {
      const claim = await o.client.readWithEpoch<Ingestion>(`/ingestions/${enc(o.ingestion)}`);
      const operation = await o.client.readWithEpoch<Operation>(`/operations/${enc(o.op)}`);
      // A clear since the check began took back the events it compared with.
      if (o.signal.aborted || mine !== epoch || claim.result.kind !== "ok" || operation.result.kind !== "ok") return;
      const last = operation.result.data.lastEvent;
      const v = caughtUp(shown, claim.epoch, operation.epoch, last);
      if (v === "reset") return;
      behind = last;
      caught = v;
    } finally {
      busy = false;
    }
  };
  const ask = () => {
    if (!caught && !busy && (behind === undefined || (shown?.n ?? 0) >= behind)) void check();
    return caught;
  };
  return {
    settled: () => run.settled,
    state: () => run,
    w: {
      show: (e) => { shown = seenOf(e.id); run = onEvent(run, e.type, e.data); o.view({ type: "show", line: eventLine(e) }); },
      clear: (why) => { run = startRun(o.generation); shown = undefined; caught = false; behind = undefined; epoch++; o.view({ type: "clear", why }); },
      until: () => {
        const read = o.latest();
        return stopsWatch(run.settled, read, o.now(), o.abandonedAt(), run.settled === undefined && lapsedRead(read, o.now()) && ask());
      },
      signal: o.signal,
    },
  };
}

// How long the page follows an abandoned claim's operation for its terminal event. This is a client
// bound, not the server's configured sweep interval: the sweep runs every `ingestion.sweep`, or every
// minute without that setting, so 120 s is two default intervals and a working sweep ends the
// operation first. The count starts at the first read that showed the claim abandoned.
export const ABANDONED_SETTLE = 120_000;
// The instant of that first read: a later read of the same abandoned claim, or a reconnect of the
// watch, keeps it, so rereads cannot extend the bound. A claim that is not abandoned has none.
export const abandonedSince = (b: Pick<Ingestion, "state">, since: number | undefined, now: number): number | undefined => (b.state === "abandoned" ? (since ?? now) : undefined);

// What the watch of an import's operation depends on. A continuation or takeover advances the claim
// generation and keeps the operation id, so the generation is part of the key: the watch restarts at it.
// An abandonment keeps both and fails the operation, so the abandoned state is part of the key too: a
// watch stopped at the pause reads the failure event.
export const watchDeps = (client: Client | undefined, b: Ingestion, again: () => void) => [client, b.operation, b.ownerGeneration, b.state === "abandoned", again] as const;

// When the read of the ingestion that the page shows began (ginsys/bronzeward#644 item C). The lease
// stop compares it with the lease plus the margin, and a snapshot begun before the lease ended can be
// delivered after it, with a renewal in between. `keys` are what starts a read (the page's own refresh,
// the shell's, the view): a change in a render stamps `began` with that render's time, before the read
// that follows it begins, so the stamp is never later than the read's own start. An answer is stamped
// with the `began` of the render before the one that carries it, so a read triggered in the very render
// that delivers an older answer does not move that answer's stamp.
export interface ReadStamp { keys: readonly unknown[]; began: number; read: { b: Ingestion; at: number } | undefined }
export function stampRead(s: ReadStamp, b: Ingestion | undefined, keys: readonly unknown[], now: number): ReadStamp {
  const read = b !== undefined && s.read?.b !== b ? { b, at: s.began } : s.read;
  const same = keys.length === s.keys.length && keys.every((k, i) => k === s.keys[i]);
  return { keys, began: same ? s.began : now, read };
}

// A refresh of the ingestion that found no answer is tried again, 1 s apart doubling to 30 s, and at
// most 5 tries in all (client contracts §8.2, the reads' retry rule), so a lapsed lease is not
// watched until the page leaves. After the last try the page says so (ginsys/bronzeward#644 item J).
export const REFRESH_TRIES = 5;
export const REFRESH_FAILED = "The import could not be read again after 5 tries, so its lease is not known to have lapsed. Reload the page to read it again.";
export const refreshDelay = (failures: number) => Math.min(1000 * 2 ** (failures - 1), 30_000);
export function refreshRetry(again: () => void, failed: (failed: boolean) => void) {
  let failures = 0;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const stop = () => { clearTimeout(timer); timer = undefined; };
  return {
    stop,
    // Told the outcome of each settled read: true when it found no answer and kept the data shown.
    lost: (lost: boolean) => {
      stop();
      if (!lost) {
        failures = 0;
        failed(false);
        return;
      }
      if (++failures >= REFRESH_TRIES) return failed(true);
      timer = setTimeout(again, refreshDelay(failures));
    },
  };
}

// What the Progress section shows: the event lines, the note that they were cleared (a restore or a
// new epoch) and the end text. A watch starting resets all three, so a note or end of an earlier
// operation never shows beside the next one's events.
export interface WatchView { lines: string[]; cleared?: string; end?: ReactNode }
export type WatchAct = { type: "start" } | { type: "show"; line: string } | { type: "clear"; why: string } | { type: "end"; text: ReactNode };
export function watchView(v: WatchView, a: WatchAct): WatchView {
  switch (a.type) {
    case "start": return { lines: [] };
    case "show": return { ...v, lines: [...v.lines, a.line] };
    case "clear": return { ...v, lines: [], cleared: a.why };
    case "end": return { ...v, end: a.text };
  }
}

// The import's operation followed to its end (§8.6), or until the claim pauses, its run records
// resume-failed, or a read made once the lease passed shows it lapsed (§11.3, as the CLI follows).
function Progress({ b, readAt, again }: { b: Ingestion; readAt: number; again: () => void }) {
  const client = useWrites()?.client;
  const { failure, settle, undropped } = useReads();
  const [{ lines, cleared, end }, show] = useReducer(watchView, { lines: [] });
  // The latest read of the ingestion and the page's failure view, for the watch begun before them.
  // `at` is when that read began, for the lease stop (item C); `seen` is when it was delivered, from
  // which the bound on an abandoned claim counts.
  const latest = useRef({ b, at: readAt, seen: Date.now() });
  const abandonedAt = useRef<number>(undefined);
  if (latest.current.b !== b) latest.current = { b, at: readAt, seen: Date.now() };
  abandonedAt.current = abandonedSince(b, abandonedAt.current, latest.current.seen);
  const shown = useRef(failure);
  shown.current = failure;
  useEffect(() => {
    const wait = Date.parse(b.leaseUntil) + LEASE_MARGIN - Date.now();
    if (!running(b) || wait <= 0) return;
    const t = setTimeout(again, wait);
    return () => clearTimeout(t);
  }, [b, again]);
  const op = b.operation;
  const generation = b.ownerGeneration;
  const ingestionId = b.id;
  useEffect(() => {
    if (client === undefined || op === null) return;
    const abort = new AbortController();
    show({ type: "start" });
    // The watch's reads and its end's go through the page's settle, as the page's own reads do.
    const reading = settledReads(client, { settle, undropped }, () => !abort.signal.aborted);
    const run = followRun({
      client: reading, ingestion: ingestionId, op, generation, signal: abort.signal,
      latest: () => latest.current, abandonedAt: () => abandonedAt.current, now: () => Date.now(), view: show,
    });
    void watch(reading, op, run.w).then(async (r) => {
      if (r.kind === "stopped" || abort.signal.aborted) return;
      // The ETag is kept under the epoch of the operation read the watch ended on, not under the client's
      // epoch now, which another response can have moved since (§8.5).
      const text = r.kind === "failed" ? shown.current(r.res) : await ended(reading, latest.current.b, r.operation, run.state().settled ?? "", Date.now(), r.epoch, run.state());
      if (!abort.signal.aborted) {
        show({ type: "end", text });
        again();
      }
    });
    return () => abort.abort();
  }, watchDeps(client, b, again));
  if (op === null) return null;
  return (
    <section aria-label="Progress">
      <h3>Progress of operation <Id id={op} /></h3>
      {cleared !== undefined && <Status testid="cleared">{cleared}</Status>}
      <ol aria-live="polite" data-testid="events">{lines.map((l, i) => <li key={i}>{l}</li>)}</ol>
      {end !== undefined && <Status testid="ended">{end}</Status>}
    </section>
  );
}

// The start route's query (RUL-0654-363): `kind` is import (the default) or drift-adoption, `source`
// machine or document. A drift adoption ingests the machine's own bytes, so its source is machine
// (internal/api/ingestions.go:49). Any other value is refused here, and the page sends nothing.
export interface StartParams { kind: "import" | "drift-adoption"; source: "machine" | "document"; refusal?: string }
export function startParams(q: URLSearchParams): StartParams {
  const kind = q.get("kind") ?? "import";
  const source = q.get("source") ?? "machine";
  if (kind !== "import" && kind !== "drift-adoption") return { kind: "import", source: "machine", refusal: `Not sent: the kind "${kind}" is neither import nor drift-adoption.` };
  if (source !== "machine" && source !== "document") return { kind, source: "machine", refusal: `Not sent: the source "${source}" is neither machine nor document.` };
  if (kind === "drift-adoption" && source === "document") return { kind, source: "machine", refusal: "Not sent: a drift adoption reads the machine's own configuration, so its source is machine, not a document." };
  return { kind, source };
}

// A document the page will not send: none, or over the server's limit (a body over 1 MiB is the
// write layer's refusal, which names itself).
export function documentRefusal(source: "machine" | "document", doc: string): string | undefined {
  if (source !== "document") return undefined;
  if (doc === "") return "Not sent: paste the document or choose its file.";
  return new TextEncoder().encode(doc).length > MAX_DOCUMENT ? "Not sent: the document is over the server's 512 KiB limit." : undefined;
}

export interface StartInput {
  kind: StartParams["kind"]; machine: string; draft: string; source: StartParams["source"];
  staging: "encrypted" | "transient"; review: boolean; doc: string; marks: string;
}
// The start's body. A review needs encrypted staging (G5), so a transient staging never asks for one.
export function startBody(i: StartInput): Record<string, unknown> {
  const marks = markLines(i.marks);
  return {
    kind: i.kind, machine: i.machine, draft: i.draft, source: i.source, staging: i.staging, review: i.staging === "encrypted" && i.review,
    ...(marks.length > 0 && { marks }), ...(i.source === "document" && { document: i.doc }),
  };
}

// Protected input is held no longer than its request needs (§11.2 item 6): the fields are emptied
// when the request is sent, and the write layer tells the start through `released` when the write can
// no longer be repeated: a settled outcome other than unknown, a set-aside or dismissed write, or the
// end of its client's writes. Until then the body stays for [Repeat exactly], which sends it again.
// The layer drops its own reference to the body; `released` drops the page's copy of it.
//
// The start's write: the draft's ETag as If-Match, and a done answer's ingestion remembered for the
// draft, so that only this page's own import adopts the draft's ETag later (ETAG-02), also when the
// page left. The callback keeps the draft's id only, so the kept request does not reach the input.
export function sendStart(send: (spec: WriteSpec) => void, clear: () => void, i: StartInput, ifMatch: string): void {
  const body = startBody(i);
  const draft = i.draft;
  clear();
  send({
    method: "POST", path: "/ingestions", resource: "/ingestions", ifMatch, body,
    released: () => {
      delete body["document"];
      delete body["marks"];
    },
    settled: (out, w) => {
      const a = out.kind === "done" ? (out.data as { ingestion?: string } | null) : null;
      if (typeof a?.ingestion === "string") started.set(a.ingestion, { draft, epoch: w.epoch });
      if (out.kind === "refused" && out.status === 412) refusedTags.set(draft, ifMatch);
    },
  });
}
// The ETag of each draft whose start the server refused with a 412 (ginsys/bronzeward#644 item H):
// sending the same ETag again is refused again, so nothing is sent until the draft's page, which the
// 412 links to, has read the draft and kept another. In this page's memory only.
const refusedTags = new Map<string, string>();
export const forgetRefused = (draft: string) => void refusedTags.delete(draft);
export const REVIEW_FIRST = "Not sent: the draft changed since this ETag was read. Review the draft first; its page reads the ETag again.";
// What a refused start offers: the draft the 412 is about, to read before starting again.
export const reviewFirst = (o: Outcome | undefined, draft: string | undefined): ReactNode =>
  o?.kind === "refused" && o.status === 412 && draft !== undefined
    ? <p><Ref to={`/drafts/${draft}`} id={draft}>Review the draft</Ref> before starting again: another write changed it.</p>
    : undefined;
// The typed fields after a start was sent: only a field that still equals the text that was sent is
// emptied. Text typed while the draft's ETag was read is neither sent nor released, so it stays,
// and the leave guard keeps asking for it (ginsys/bronzeward#644 item G).
export const keepTyped = (now: { doc: string; marks: string }, sent: { doc: string; marks: string }) => ({
  doc: now.doc === sent.doc ? "" : now.doc,
  marks: now.marks === sent.marks ? "" : now.marks,
});
// The start's If-Match is the draft's ETag (§8.3): the kept one, else the draft read again. The write
// is sent under the client's epoch at its send, so nothing is sent when that epoch is not the one the
// ETag was read under (§8.5): `send` runs in the same turn as the check. Returns why nothing was sent.
export async function startETag(read: (path: string) => Promise<Load<Draft> | undefined>, current: () => string | undefined, draft: string, send: (ifMatch: string) => void): Promise<string | undefined> {
  const epoch = current();
  const kept = keptETag(draft, epoch);
  if (kept !== undefined) {
    if (refusedTags.get(draft) === kept) return REVIEW_FIRST;
    forgetRefused(draft);
    send(kept);
    return undefined;
  }
  const r = await read(`/drafts/${enc(draft)}`);
  if (r === undefined || r.kind !== "ok" || r.etag === undefined) return "Not sent: the draft could not be read for its ETag. Try again.";
  if (current() !== epoch) return "Not sent: the installation entered recovery mode or was restored while the draft was read. Try again.";
  keepETag(draft, r.etag, epoch);
  send(r.etag);
  return undefined;
}
// The leave guard asks while typed input is not yet sent.
export const unsent = (input: { doc: string; marks: string }) => input.doc !== "" || input.marks !== "";

// Reads a chosen file into the document field. Only the newest choice counts: a read of an earlier file
// that finishes later neither replaces the newer text nor reports a failure.
export function fileReader(set: (text: string) => void, note: (why: string) => void) {
  let latest = 0;
  return (f: { text: () => Promise<string> } | undefined) => {
    const mine = ++latest;
    void f?.text().then((t) => { if (mine === latest) set(t); }, () => { if (mine === latest) note("The file could not be read."); });
  };
}

// Text typed or pasted into the document field ends a file read that is still pending, so the read cannot
// replace the newer text when it finishes (choose(undefined) moves the reader's counter and reads nothing).
export function typedDocument(cancelRead: (f: undefined) => void, set: (text: string) => void) {
  return (text: string) => { cancelRead(undefined); set(text); };
}

// The import wizard (B15) on one page: machine, draft, source, staging and review, optional marks.
export function StartImport({ query }: { query: string }) {
  const q = new URLSearchParams(query);
  const params = startParams(q);
  const { go } = useReads();
  const read = useSettled();
  const writes = useWrites();
  const [w, send] = useSend();
  const [machine, setMachine] = useState(q.get("machine") ?? "");
  const [source, setSource] = useState(params.source);
  const [staging, setStaging] = useState<StartInput["staging"]>("encrypted");
  const [review, setReview] = useState(true);
  const [typed, setTyped] = useState({ doc: "", marks: "" });
  const [note, setNote] = useState<string>();
  const reading = useRef(false);
  // The draft of the start last sent, for the link a 412 shows.
  const sentDraft = useRef<string>(undefined);
  const [machines] = useList<Machine>("/machines", "", true);
  const [drafts] = useList<Draft>("/drafts", "", true);
  const m = machines.kind === "ok" ? machines.data.items.find((x) => x.id === machine) : undefined;
  const open = drafts.kind === "ok" && m !== undefined ? drafts.data.items.filter((d) => d.cluster === m.cluster && d.state === "open") : [];
  const o = w?.outcome;
  const { doc, marks } = typed;
  useUnsaved("the import's document or marks", unsent(typed));
  useEffect(() => {
    if (o?.kind !== "done") return;
    const a = o.data as { ingestion?: string } | null;
    if (typeof a?.ingestion === "string") go(`/ingestions/${a.ingestion}`);
  }, [o, go]);
  const submit = async (d: FormData) => {
    if (reading.current) return;
    setNote(undefined);
    const draft = field(d, "draft");
    const refusal = params.refusal ?? documentRefusal(source, doc);
    if (refusal !== undefined) return setNote(refusal);
    reading.current = true;
    try {
      const why = await startETag(read<Draft>, () => writes?.client?.currentEpoch, draft,
        (ifMatch) => {
          sentDraft.current = draft;
          // Only the text that was submitted is emptied: more typed while the draft's ETag was read stays.
          sendStart(send, () => setTyped((now) => keepTyped(now, { doc, marks })), { kind: params.kind, machine, draft, source, staging, review, doc, marks }, ifMatch);
        });
      if (why !== undefined) setNote(why);
    } finally {
      reading.current = false;
    }
  };
  const choose = useRef(fileReader((t) => setTyped((x) => ({ ...x, doc: t })), setNote)).current;
  const typeDocument = typedDocument(choose, (t) => setTyped((x) => ({ ...x, doc: t })));
  const label = params.kind === "drift-adoption" ? "Start drift adoption" : "Start import";
  return (
    <>
      <h2>{label}</h2>
      {params.refusal !== undefined && <Status testid="start-refused">{params.refusal}</Status>}
      <Form label={label} onSubmit={(d) => void submit(d)} w={w} done={(data) => `Import ${short((data as { ingestion: string }).ingestion)} started.`}>
        <p>
          <label>Machine:{" "}
            <select name="machine" required value={machine} onChange={(e) => setMachine(e.target.value)}>
              <option value="" disabled>{machines.kind === "ok" ? "choose" : "machines not read"}</option>
              {(machines.kind === "ok" ? machines.data.items : []).map((x) => <option key={x.id} value={x.id}>{short(x.id)} ({x.talosEndpoint})</option>)}
            </select>
          </label>
        </p>
        <p>
          <label>Draft:{" "}
            <select name="draft" required defaultValue={q.get("draft") ?? ""} key={`${machine}:${open.length}`}>
              <option value="" disabled>{m === undefined ? "choose a machine first" : open.length === 0 ? "no open draft in this machine's cluster" : "choose"}</option>
              {open.map((d) => <option key={d.id} value={d.id}>{d.title} ({short(d.id)}, revision {d.revision})</option>)}
            </select>
          </label>{" "}
          <Action label="Create draft" needs={["author"]} onClick={() => go("/drafts/new")} />
        </p>
        <fieldset>
          <legend>Source</legend>
          <label><input type="radio" name="source" value="machine" checked={source === "machine"} onChange={() => setSource("machine")} /> Read from the machine</label>{" "}
          <label><input type="radio" name="source" value="document" checked={source === "document"} disabled={params.kind === "drift-adoption"} onChange={() => setSource("document")} /> A document, pasted or from a file</label>
        </fieldset>
        {source === "document" && (
          <>
            <p><label>Document (YAML):<br /><textarea rows={16} cols={80} value={doc} onChange={(e) => typeDocument(e.target.value)} {...NO_ASSIST} /></label></p>
            <p><label>Or read it from a file:{" "}<input type="file" accept=".yaml,.yml" onChange={(e) => { const f = e.target.files?.[0]; e.target.value = ""; choose(f); }} /></label></p>
          </>
        )}
        <fieldset>
          <legend>Staging</legend>
          <label><input type="radio" name="staging" value="encrypted" checked={staging === "encrypted"} onChange={() => setStaging("encrypted")} /> Encrypted staging</label>{" "}
          <label><input type="radio" name="staging" value="transient" checked={staging === "transient"} onChange={() => setStaging("transient")} /> Transient staging</label>
        </fieldset>
        <p>
          <label><input type="checkbox" name="review" checked={staging === "encrypted" && review} disabled={staging !== "encrypted"} onChange={(e) => setReview(e.target.checked)} /> Pause for review before the draft changes</label>
          {staging !== "encrypted" && " A review needs encrypted staging."}
        </p>
        <p>
          <label>Marks, one path per line (optional):<br /><textarea rows={4} cols={80} value={marks} onChange={(e) => setTyped((x) => ({ ...x, marks: e.target.value }))} {...NO_ASSIST} /></label><br />
          A mark names a value to keep secret by its path, such as doc[0]/machine/files/0/content.
        </p>
        {note !== undefined && <Status testid="start-note">{note}</Status>}
      </Form>
      {reviewFirst(o, sentDraft.current)}
    </>
  );
}
