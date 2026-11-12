// The publication page (ginsys/bronzeward#342; journeys §5.5; decision T18 of #256, whose publish
// command this follows: cmd/bronzewardctl/publish.go). It reads the draft, sends POST
// /drafts/{id}/publications with the draft's kept ETag (§8.3), follows the publish operation with the
// progress reader (B16) and, once it succeeded, names the release from the operation's result (G4) and
// says that nothing was sent to any machine. The release page shows what each machine needs next (W6).

import { type ReactNode, useEffect, useReducer, useRef, useState } from "react";
import type { components } from "./api/v1";
import { forgetETag, keepETag, keptETag, lastGood, NO_ETAG, WriteResult } from "./drafts.tsx";
import { startsNew, useSend, useWrites } from "./forms.tsx";
import { settledReads, watchView } from "./imports.tsx";
import { Id, Ref, short, useItem, usePending, useReads } from "./list.tsx";
import { eventLine, refusalOf, terminal, watch } from "./progress.ts";
import { Action } from "./shell.tsx";
import { Status } from "./states.tsx";
import type { Outcome } from "./write.ts";

type S = components["schemas"];
type Draft = S["DraftBody"];
type Operation = S["OperationBody"];

const enc = encodeURIComponent;

export const SENT_NOTHING = "Nothing has been sent to any machine.";
export const NOT_OPEN = (state: string) => `This draft is ${state}, not open: it cannot be published.`;
export const NO_DRAFT = "Not read: the address names no draft. Open the draft and choose Publish.";

// What the publish operation's end says: the release it names, or the text that explains why none is
// named. A failed operation sent nothing to any machine, whatever its code.
export type Ended = { release: string } | { text: string };
export function publishEnd(o: Operation): Ended {
  if (o.state === "succeeded") {
    const r = o.result as S["PublishResult"] | null;
    if (r === null || typeof r.release !== "string" || r.release === "") return { text: `Publication operation ${short(o.id)} succeeded without naming its release.` };
    return { release: r.release };
  }
  const code = refusalOf({ problem: o.error }).code;
  return { text: `Publication operation ${short(o.id)} ${o.state}${code === undefined ? "" : `: ${code}`}. ${SENT_NOTHING}` };
}

// What the accepted answer says: the draft's revision and the operation, each only when the answer names it.
export function accepted(draft: string, data: unknown): string {
  const o = typeof data === "object" && data !== null ? (data as Partial<Operation>) : {};
  const rev = o.subject?.draftRevision;
  const op = operationId(data);
  return `Publishing draft ${short(draft)} revision ${rev ?? "?"}${op === undefined ? "." : ` (operation ${short(op)}).`}`;
}

// The id of the operation a publication's answer names, when it can be read and names one.
export const operationId = (data: unknown): string | undefined => {
  const id = typeof data === "object" && data !== null ? (data as { id?: unknown }).id : undefined;
  return typeof id === "string" && id !== "" ? id : undefined;
};

// An operation read under another epoch than the client's now decides nothing (§8.5, §8.6 item 2):
// the installation was restored or entered recovery mode after it, so the state is not established.
export const notEstablished = (op: string) =>
  `Publication operation ${short(op)} ended when it was read, but the installation was restored or entered recovery mode after that, so its outcome is not established. Read the operation again.`;

// Whether Publish stays locked. A write still open (in flight, or unknown with its notice not yet dismissed)
// locks it. An accepted one keeps it locked until its operation is known to have failed, because only then
// has the draft stayed open. A dismissed unknown outcome locks nothing: the write is no longer open.
export function publishLocked(open: boolean, o: Outcome | undefined, failed: string | undefined): boolean {
  if (open) return true;
  if (o?.kind !== "done") return false;
  const op = operationId(o.data);
  return op === undefined || failed !== op;
}

function Progress({ op, draft, onFailed }: { op: string; draft: string; onFailed: (op: string) => void }) {
  const client = useWrites()?.client;
  const reads = useReads();
  const now = useRef(reads);
  now.current = reads;
  const [{ lines, cleared, end }, show] = useReducer(watchView, { lines: [] });
  useEffect(() => {
    if (client === undefined) return;
    const abort = new AbortController();
    show({ type: "start" });
    const reading = settledReads(client, { settle: now.current.settle, undropped: now.current.undropped }, () => !abort.signal.aborted);
    void watch(reading, op, {
      show: (e) => show({ type: "show", line: eventLine(e) }),
      clear: (why) => show({ type: "clear", why }),
      signal: abort.signal,
    }).then((r) => {
      if (r.kind === "stopped" || abort.signal.aborted) return;
      if (r.kind === "failed") return show({ type: "end", text: now.current.failure(r.res) });
      if (!terminal(r.operation.state)) return show({ type: "end", text: notEstablished(op) });
      if (r.epoch === undefined || r.epoch !== client.currentEpoch) return show({ type: "end", text: notEstablished(op) });
      const e = publishEnd(r.operation);
      // A published draft takes no write: its ETag is forgotten once the operation is known to have succeeded,
      // whether or not its result names the release (the CLI does the same).
      if (r.operation.state === "succeeded") forgetETag(draft);
      // A publication that definitively failed left the draft open: Publish may send again (the server
      // reuses only a queued or running operation).
      else onFailed(op);
      show({ type: "end", text: "release" in e ? <>Publication succeeded: release <Id id={e.release} /> was published. {SENT_NOTHING}</> : e.text });
    });
    return () => abort.abort();
  }, [client, op, draft, onFailed]);
  return (
    <section aria-label="Progress">
      <h3>Progress of operation <Id id={op} /></h3>
      {cleared !== undefined && <Status testid="cleared">{cleared}</Status>}
      <ol aria-live="polite" data-testid="events">{lines.map((l, i) => <li key={i}>{l}</li>)}</ol>
      {end !== undefined && <Status testid="ended">{end}</Status>}
    </section>
  );
}

function Publish({ id }: { id: string }) {
  const writes = useWrites();
  const epoch = writes?.client?.currentEpoch;
  const [n, reread] = useState(0);
  const item = useItem<Draft>(`/drafts/${enc(id)}`, undefined, n);
  // Whether the next read's ETag is kept: the first, and one the operator asked for after a refusal.
  const keep = useRef(true);
  useEffect(() => {
    if (item.kind !== "ok" || !keep.current) return;
    keep.current = false;
    if (item.etag !== undefined && item.data.state === "open") keepETag(id, item.etag, epoch);
  }, [item, id, epoch]);
  const pending = usePending(item, "the draft");
  const last = useRef<{ id: string; data: Draft }>(undefined);
  last.current = lastGood(last.current, id, item);
  const d = last.current?.data;
  const [w, send] = useSend();
  const [note, setNote] = useState<string | undefined>();
  const o = w?.outcome;
  const op = o?.kind === "done" ? operationId(o.data) : undefined;
  const readAgain = () => {
    keep.current = true;
    setNote(undefined);
    reread((x) => x + 1);
  };
  const publish = () => {
    const ifMatch = keptETag(id, epoch);
    setNote(ifMatch === undefined ? NO_ETAG : undefined);
    // A 412 says the kept ETag is stale: it is forgotten, so Publish sends nothing until the draft is read again.
    const settled = (s: Outcome) => void (s.kind === "refused" && s.status === 412 && forgetETag(id));
    if (ifMatch !== undefined) send({ method: "POST", path: `/drafts/${enc(id)}/publications`, body: {}, ifMatch, resource: `/drafts/${id}`, settled });
  };
  const [failed, setFailed] = useState<string | undefined>();
  const busy = publishLocked(writes !== undefined && w !== undefined && !startsNew(writes, w), o, failed);
  return (
    <>
      <h2>Publish draft {short(id)}{d !== undefined && <> "{d.title}" {d.state}, revision {d.revision}, cluster <Id id={d.cluster} /></>}</h2>
      {pending}
      {d !== undefined && d.state !== "open" && <Status testid="not-open">{NOT_OPEN(d.state)}</Status>}
      {d !== undefined && d.state === "open" && (
        <>
          <p>Publishing renders the configuration of every machine of the cluster that has an import base, from this draft's revision, and makes the release Desired for those machines. {SENT_NOTHING}</p>
          <p><Action label="Publish" needs={["publisher"]} onClick={publish} busy={busy} /></p>
        </>
      )}
      {note !== undefined && <Status testid="no-etag">{note} <button type="button" onClick={readAgain}>Read the draft again</button></Status>}
      <WriteResult
        w={w}
        done={(data): ReactNode => accepted(id, data)}
        conflict={<Status testid="write-result">Not published: someone changed this draft since this page read it. <button type="button" onClick={readAgain}>Read the draft again</button></Status>}
      />
      {o?.kind === "done" && typeof o.data === "object" && op === undefined && <Status testid="no-operation">The publication was accepted without naming its operation. Find it in the operations list, kind publish.</Status>}
      {op !== undefined && <Progress key={op} op={op} draft={id} onFailed={setFailed} />}
      <p><Ref to={`/drafts/${id}`} id={id}>Back to the draft</Ref></p>
    </>
  );
}

// /publications/new?draft=<id>: one page per draft, keyed by it so a move to another draft keeps none
// of the last one's reads or ETag (as the draft page does).
export function PublishDraft({ query }: { query: string }) {
  const draft = new URLSearchParams(query).get("draft") ?? "";
  return draft === "" ? <Status testid="no-draft">{NO_DRAFT}</Status> : <Publish key={draft} id={draft} />;
}
