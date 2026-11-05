// The import pages (#340, decision B15 of #257): the wizard, and the import page with its progress
// (B16, progress.ts) and the draft's ETag after a released import (client contracts §8.3, ETAG-02).
// They follow the CLI's import start and status (cmd/bronzewardctl/imports.go). A document or marks
// are protected input (§11.1-§11.4): held in this page's memory only, sent once, and dropped once
// the start is answered. The review, continue, takeover and abandon controls are W5's (#341).

import { type ReactNode, useCallback, useEffect, useRef, useState } from "react";
import type { components } from "./api/v1";
import { keepETag, keptETag, NO_ASSIST } from "./drafts.tsx";
import { Form, field, useSend, useUnsaved, useWrites } from "./forms.tsx";
import { Id, type Reads, Ref, short, useItem, useList, usePending, useReads, useSettled } from "./list.tsx";
import { eventLine, terminal, watch } from "./progress.ts";
import { Action } from "./shell.tsx";
import { Status, when } from "./states.tsx";
import type { Outcome } from "./write.ts";

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

// The imports this page started, each with its draft: only their success adopts the draft's ETag
// (§8.3). In this page's memory only.
export const started = new Map<string, string>();

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

// What a watch's end says. A success of an import this page started adopts the draft's ETag only at
// the import's own draft revision (ETAG-02): a higher one means another write followed, treated as a
// 412.
export async function ended(client: Reads["client"], b: Ingestion, o: Operation, last: string, now: number, epoch: string | undefined): Promise<ReactNode> {
  if (o.state === "succeeded") {
    const r = o.result as S["IngestResult"] | null;
    if (r === null || typeof r.draftRevision !== "number") return `Import operation ${short(o.id)} succeeded without naming its draft revision.`;
    const done = <>Import {short(b.id)} released into draft <Id id={r.draft} /> (revision {r.draftRevision}), import base {short(r.importBaseRevision)}.</>;
    if (started.get(b.id) !== r.draft) return done;
    const d = await client.read<Draft>(`/drafts/${enc(r.draft)}`);
    if (d.kind !== "ok" || d.etag === undefined) return <>{done} The draft could not be read again, so its ETag is not kept. Open it before writing again.</>;
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
  if (last === "paused") return `Paused for review (expires ${when(b.expiresAt)}).`;
  if (last === "resume-failed") return `Import ${short(b.id)} was not applied: its staged input could not be decrypted (resume-failed). The claim stays held until its lease lapses at ${when(b.leaseUntil)}; then take it over, or abandon it now.`;
  if (lapsed(b, now)) return `Its claim is ${b.state} and its lease lapsed at ${when(b.leaseUntil)}, while operation ${short(o.id)} still runs: the outcome is unknown. Read it again later, take it over, or abandon it.`;
  return undefined;
}

// The import page (/ingestions/{id}): a reload reads the ingestion and shows what its state allows.
export function IngestionItem({ id }: { id: string }) {
  const [n, setN] = useState(0);
  const item = useItem<Ingestion>(`/ingestions/${enc(id)}`, undefined, n);
  const pending = usePending(item, "the import");
  const again = useCallback(() => setN((x) => x + 1), []);
  const b = item.kind === "ok" ? item.data : undefined;
  const now = Date.now();
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
          <Progress b={b} again={again} />
        </>
      )}
    </>
  );
}

// The import's operation followed to its end (§8.6), or until the claim pauses, its run records
// resume-failed, or a read made once the lease passed shows it lapsed (§11.3, as the CLI follows).
function Progress({ b, again }: { b: Ingestion; again: () => void }) {
  const client = useWrites()?.client;
  const { failure } = useReads();
  const [lines, setLines] = useState<string[]>([]);
  const [cleared, setCleared] = useState<string>();
  const [end, setEnd] = useState<ReactNode>();
  // The latest read of the ingestion and the page's failure view, for the watch begun before them.
  const latest = useRef(b);
  latest.current = b;
  const shown = useRef(failure);
  shown.current = failure;
  useEffect(() => {
    const wait = Date.parse(b.leaseUntil) + LEASE_MARGIN - Date.now();
    if (!running(b) || wait <= 0) return;
    const t = setTimeout(again, wait);
    return () => clearTimeout(t);
  }, [b, again]);
  const op = b.operation;
  useEffect(() => {
    if (client === undefined || op === null) return;
    const abort = new AbortController();
    let last = "";
    setLines([]);
    setEnd(undefined);
    void watch(client, op, {
      show: (e) => { last = e.type; setLines((l) => [...l, eventLine(e)]); },
      clear: (why) => { last = ""; setCleared(why); setLines([]); },
      until: () => last === "paused" || last === "resume-failed" || lapsed(latest.current, Date.now()),
      signal: abort.signal,
    }).then(async (r) => {
      if (r.kind === "stopped" || abort.signal.aborted) return;
      const text = r.kind === "failed" ? shown.current(r.res) : await ended(client, latest.current, r.operation, last, Date.now(), client.currentEpoch);
      if (!abort.signal.aborted) {
        setEnd(text);
        again();
      }
    });
    return () => abort.abort();
  }, [client, op, again]);
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
  const marks = i.marks.split("\n").map((s) => s.trim()).filter((s) => s !== "");
  return {
    kind: i.kind, machine: i.machine, draft: i.draft, source: i.source, staging: i.staging, review: i.staging === "encrypted" && i.review,
    ...(marks.length > 0 && { marks }), ...(i.source === "document" && { document: i.doc }),
  };
}

// Protected input is held no longer than its request needs (§11.2 item 6): once the write has any
// outcome, a refusal that ended it included, the fields are empty.
export const heldInput = (input: { doc: string; marks: string }, o: Outcome | undefined) => (o === undefined ? input : { doc: "", marks: "" });
// The leave guard asks while typed input is not yet sent.
export const unsent = (input: { doc: string; marks: string }, o: Outcome | undefined) => o === undefined && (input.doc !== "" || input.marks !== "");

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
  const [machines] = useList<Machine>("/machines", "", true);
  const [drafts] = useList<Draft>("/drafts", "", true);
  const m = machines.kind === "ok" ? machines.data.items.find((x) => x.id === machine) : undefined;
  const open = drafts.kind === "ok" && m !== undefined ? drafts.data.items.filter((d) => d.cluster === m.cluster && d.state === "open") : [];
  const o = w?.outcome;
  const { doc, marks } = heldInput(typed, o);
  useUnsaved("the import's document or marks", unsent(typed, o));
  useEffect(() => {
    if (o === undefined) return;
    setTyped({ doc: "", marks: "" });
    if (o.kind !== "done") return;
    const a = o.data as { ingestion?: string } | null;
    if (typeof a?.ingestion === "string") go(`/ingestions/${a.ingestion}`);
  }, [o, go]);
  const submit = async (d: FormData) => {
    if (reading.current) return;
    setNote(undefined);
    const draft = field(d, "draft");
    const refusal = params.refusal ?? documentRefusal(source, doc);
    if (refusal !== undefined) return setNote(refusal);
    const epoch = writes?.client?.currentEpoch;
    let ifMatch = keptETag(draft, epoch);
    if (ifMatch === undefined) {
      // The start's If-Match is the draft's ETag (§8.3): read the draft for it when none is kept.
      reading.current = true;
      const r = await read<Draft>(`/drafts/${enc(draft)}`);
      reading.current = false;
      if (r === undefined || r.kind !== "ok" || r.etag === undefined) return setNote("Not sent: the draft could not be read for its ETag. Try again.");
      keepETag(draft, r.etag, epoch);
      ifMatch = r.etag;
    }
    send({
      method: "POST", path: "/ingestions", resource: "/ingestions", ifMatch,
      body: startBody({ kind: params.kind, machine, draft, source, staging, review, doc, marks }),
      // Only this page's own import adopts the draft's ETag later (ETAG-02), also when the page left.
      settled: (out) => {
        const a = out.kind === "done" ? (out.data as { ingestion?: string } | null) : null;
        if (typeof a?.ingestion === "string") started.set(a.ingestion, draft);
      },
    });
  };
  const choose = (f: File | undefined) => void f?.text().then((t) => setTyped((x) => ({ ...x, doc: t })), () => setNote("The file could not be read."));
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
            <p><label>Document (YAML):<br /><textarea rows={16} cols={80} value={doc} onChange={(e) => setTyped((x) => ({ ...x, doc: e.target.value }))} {...NO_ASSIST} /></label></p>
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
    </>
  );
}
