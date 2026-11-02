// The draft editor (ginsys/bronzeward#339; #257 B12-B14; journeys §5.4; W4, W11 "Refused" and
// "Conflict"): the Changes list of drafts, Create draft, the draft's page with its entries and the
// fragment entry, Discard draft. Every write goes through write.ts with the draft's last-seen ETag
// as If-Match (client contracts §8.3). A document is typed into a plain textarea (B12) and lives in
// this page's state only: never stored, and guarded while unsaved (§11.4).

import { type ReactNode, useCallback, useEffect, useRef, useState } from "react";
import type { components } from "./api/v1";
import { Form, field, Result, startsNew, Text, unsaved, useSend, useUnsaved, useWrites } from "./forms.tsx";
import { Id, Ref, Scroll, short, useItem, useList, useReads, usePending } from "./list.tsx";
import type { Cluster } from "./machines.tsx";
import { references, type Spot, spotOfLine, spotOfPath, spotOfReference } from "./position.ts";
import type { Problem } from "./request.ts";
import { Action } from "./shell.tsx";
import { Conflict, Refused, Status } from "./states.tsx";
import type { Outcome, Write } from "./write.ts";

type S = components["schemas"];
type Draft = S["DraftBody"];
type Source = S["SourceEntry"];
type Revision = S["FragmentRevisionBody"];
type Fragment = S["FragmentBody"];
type Declared = S["FragmentDeclarations"]["references"];
type Embedded = S["Embedded"];

// Composition order, global first (#257 B17; persistence-api §9.3).
export const LAYERS = ["global", "site", "cluster", "role", "workload", "override"] as const;
export const KINDS = ["string", "integer", "boolean", "mapping"] as const;
export const NOT_RENDERED = "Not rendered yet. A machine's configuration exists only after publication (gap G5).";
export const NO_ETAG = "Not sent: this page has not read the draft under the current epoch. Read it again first.";

// §8.3: the last ETag seen per draft, in this page's memory, valid only under the epoch it was
// seen in (§8.5 item 1).
const etags = new Map<string, { etag: string; epoch: string | undefined }>();
export const keepETag = (draft: string, etag: string, epoch: string | undefined) => void etags.set(draft, { etag, epoch });
export const keptETag = (draft: string, epoch: string | undefined) => {
  const e = etags.get(draft);
  return e !== undefined && e.epoch === epoch ? e.etag : undefined;
};
export const forgetETag = (draft: string) => void etags.delete(draft);

// A write spec's settled hook that keeps a done answer's ETag for its draft, under the epoch the
// write was sent in, the one the server accepted it in. It runs when the write settles, whether
// or not the view that sent it is still shown. An answer that settles after the epoch changed
// keeps nothing: its tag is no longer valid, and a tag read since stays (§8.5 item 1).
export const keepOnDone = (draft: string | ((o: Outcome) => string | undefined), epoch: () => string | undefined) => (o: Outcome, w: Write) => {
  if (o.kind !== "done" || o.etag === undefined || w.epoch === undefined || w.epoch !== epoch()) return;
  const id = typeof draft === "string" ? draft : draft(o);
  if (id !== undefined) keepETag(id, o.etag, w.epoch);
};

// The id of a created draft, when its answer could be read and names one.
export const createdId = (data: unknown): string | undefined => {
  const id = typeof data === "object" && data !== null ? (data as { id?: unknown }).id : undefined;
  return typeof id === "string" ? id : undefined;
};

// A switch to another entry drops the editor's unsaved text: it asks first, as leaving does.
export function mayDrop(): boolean {
  if (unsaved.size === 0) return true;
  return confirm(`${[...unsaved].map((what) => `Unsaved text in ${what}: switching drops it.`).join("\n")}\nSwitch entry?`);
}

// A write's done outcome the first time it is asked for, else undefined: the page acts on each
// done write once, however often it renders after it.
type Done = Extract<Outcome, { kind: "done" }>;
const acted = new WeakSet<Write>();
export function doneOnce(w: Write | undefined): Done | undefined {
  const o = w?.outcome;
  if (w === undefined || o?.kind !== "done" || acted.has(w)) return undefined;
  acted.add(w);
  return o;
}

const enc = encodeURIComponent;
const isSource = (e: Draft["entries"][number]): e is Source => e.kind !== "import-base";
const nameOf = (e: Source) => e.name ?? e.machine ?? "";
const keyOf = (e: Source) => `${e.kind}:${nameOf(e)}`;

// What an entry changes against its base (journeys §5.4's draft show).
export function change(e: Source): string {
  if (e.revision === null) return "removed";
  if (e.head === null) return "new";
  return `changed (base revision ${e.base ?? "none"})`;
}

// The Changes page's drafts, every page read, filtered by cluster in the browser (#339 scope).
export function Drafts() {
  const { go } = useReads();
  const [list] = useList<Draft>("/drafts", "", true);
  const [cluster, setCluster] = useState("");
  const pending = usePending(list, "drafts");
  const items = list.kind === "ok" ? list.data.items : [];
  const clusters = [...new Set(items.map((d) => d.cluster))];
  const shown = items.filter((d) => cluster === "" || d.cluster === cluster);
  return (
    <>
      <p>
        <label>Cluster:{" "}
          <select value={cluster} onChange={(e) => setCluster(e.target.value)}>
            <option value="">all</option>
            {clusters.map((c) => <option key={c} value={c}>{short(c)}</option>)}
          </select>
        </label>{" "}
        <Action label="Create draft" needs={["author"]} onClick={() => go("/drafts/new")} />
      </p>
      {pending ?? (shown.length === 0 ? <Status>No drafts{cluster === "" ? "" : " in this cluster"}.</Status> : (
        <Scroll label="Drafts">
          <thead><tr>{["Draft", "Title", "Cluster", "State", "Revision"].map((h) => <th key={h} scope="col">{h}</th>)}</tr></thead>
          <tbody>
            {shown.map((d) => (
              <tr key={d.id}><th scope="row"><Id id={d.id} /></th><td>{d.title}</td><td><Id id={d.cluster} /></td><td>{d.state}</td><td>{d.revision}</td></tr>
            ))}
          </tbody>
        </Scroll>
      ))}
    </>
  );
}

export function CreateDraft() {
  const [w, send] = useSend();
  const writes = useWrites();
  const [clusters] = useList<Cluster>("/clusters", "", true);
  const settled = keepOnDone((o) => createdId(o.kind === "done" ? o.data : null), () => writes?.client?.currentEpoch);
  const submit = (d: FormData) => send({ method: "POST", path: "/drafts", resource: "/changes", body: { cluster: field(d, "cluster"), title: field(d, "title") }, settled });
  const done = (data: unknown) => {
    const id = createdId(data);
    if (id === undefined) return <>Draft created, but the answer names no draft. Read <Ref to="/changes" id="/changes">the Changes page</Ref>.</>;
    const d = data as Draft;
    return <>Draft <Ref to={`/drafts/${id}`} id={id} /> "{d.title}" created in cluster {short(d.cluster)}.</>;
  };
  const options = clusters.kind === "ok" ? clusters.data.items : [];
  return (
    <>
      <h2>Create draft</h2>
      <Form label="Create draft" onSubmit={submit} w={w} done={done}>
        <p>
          <label>Cluster:{" "}
            <select name="cluster" required defaultValue="" key={options.length}>
              <option value="" disabled>{clusters.kind === "ok" ? "choose" : "clusters not read"}</option>
              {options.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
            </select>
          </label>
        </p>
        <Text label="Title:" name="title" />
      </Form>
    </>
  );
}

// The draft's page (W4). Its first read's ETag is kept; a later read only shows, so a write after
// another author's change is refused (412) instead of overwriting it unseen (§8.3). A write's own
// answer keeps its ETag.
export function DraftItem({ id }: { id: string }) {
  const writes = useWrites();
  const epoch = writes?.client?.currentEpoch;
  const [n, reread] = useState(0);
  const item = useItem<Draft>(`/drafts/${enc(id)}`, undefined, n);
  // Whether the next read's ETag is kept: the first, and one the operator asked for to see a change.
  const keep = useRef(true);
  useEffect(() => {
    if (item.kind !== "ok" || !keep.current) return;
    keep.current = false;
    if (item.etag !== undefined && item.data.state === "open") keepETag(id, item.etag, epoch);
  }, [item, id, epoch]);
  const [selected, select] = useState<string | undefined>();
  const pending = usePending(item, "the draft");
  const d = item.kind === "ok" ? item.data : undefined;
  // A write's answer keeps its ETag as it settles (keepOnDone); the page then reads the draft again.
  const written = useCallback(() => reread((x) => x + 1), []);
  // A switch to another entry, or to a new one, asks first while the editor holds unsaved text.
  const pick = (k: string) => {
    if (k !== selected && mayDrop()) select(k);
  };
  const seeChange = () => {
    keep.current = true;
    reread((x) => x + 1);
  };
  const sources = d?.entries.filter(isSource) ?? [];
  const entry = sources.find((e) => keyOf(e) === selected);
  return (
    <>
      <h2>Draft {short(id)}{d !== undefined && <> "{d.title}" {d.state}, revision {d.revision}, cluster <Id id={d.cluster} /></>}</h2>
      {pending ?? (d !== undefined && (
        <>
          <h3>Entries</h3>
          <ul aria-label="Entries">
            {d.entries.map((e) => isSource(e) ? (
              <li key={keyOf(e)}>
                {e.kind === "fragment" ? <button type="button" aria-pressed={selected === keyOf(e)} onClick={() => pick(keyOf(e))}>fragment {nameOf(e)}</button> : <>{e.kind} {short(nameOf(e))}</>}
                {" "}{change(e)}
              </li>
            ) : <li key={`import-base:${e.machine}`}>import base <Id id={e.machine} /> <Id id={e.revision} /></li>)}
          </ul>
          {d.state === "open" && <p><Action label="Add fragment" needs={["author"]} onClick={() => pick("fragment:")} /></p>}
          {selected !== undefined && selected.startsWith("fragment:") && (
            <FragmentPane key={selected} draft={d} entry={entry} written={written} seeChange={seeChange} />
          )}
          <Status testid="not-rendered">{NOT_RENDERED}</Status>
          {d.state === "open" && <DiscardDraft draft={d} written={written} />}
        </>
      ))}
    </>
  );
}

function DiscardDraft({ draft, written }: { draft: Draft; written: () => void }) {
  const [w, send] = useSend();
  const writes = useWrites();
  const [note, setNote] = useState<string | undefined>();
  const o = w?.outcome;
  useEffect(() => {
    if (doneOnce(w) !== undefined) written();
  }, [o, w, written]);
  // A discarded draft takes no write: its ETag is forgotten as the discard settles.
  const settled = (s: Outcome) => void (s.kind === "done" && forgetETag(draft.id));
  const discard = () => {
    if (!confirm(`Discard draft ${draft.id} "${draft.title}"? It cannot be published afterwards.`)) return;
    const ifMatch = keptETag(draft.id, writes?.client?.currentEpoch);
    setNote(ifMatch === undefined ? NO_ETAG : undefined);
    if (ifMatch !== undefined) send({ method: "POST", path: `/drafts/${enc(draft.id)}/discard`, body: {}, ifMatch, resource: `/drafts/${draft.id}`, settled });
  };
  return (
    <>
      <p><Action label="Discard draft" needs={["author"]} onClick={discard} busy={w !== undefined && w.outcome === undefined} /></p>
      {note !== undefined && <Status>{note}</Status>}
      <WriteResult w={w} done={() => `Draft ${short(draft.id)} discarded.`} />
    </>
  );
}

// What the editor holds: the text, the layer, the marks and the declarations.
export interface Held {
  layer: string;
  document: string;
  marks: string;
  declared: Declared;
  embedded: Embedded[];
}
export const EMPTY: Held = { layer: "role", document: "", marks: "", declared: {}, embedded: [] };
const fromRevision = (r: Revision): Held => ({ layer: r.layer, document: r.document, marks: "", declared: { ...r.declarations.references }, embedded: [...r.declarations.embedded] });
const same = (a: Held, b: Held) => a.layer === b.layer && a.document === b.document && a.marks === b.marks && JSON.stringify(a.declared) === JSON.stringify(b.declared) && JSON.stringify(a.embedded) === JSON.stringify(b.embedded);

// What the editor holds once a revision is read: the read text, unless the operator typed since
// the last save. During a conflict the operator's text is kept aside, so the read is shown.
export const afterRead = (held: Held, saved: Held, read: Held, conflicted: boolean): Held => (conflicted || same(held, saved) ? read : held);

// Whether the editor holds text not saved: its own, or the operator's text a conflict keeps aside.
export const holdsUnsaved = (held: Held, saved: Held, mine: Held | undefined) => !same(held, saved) || (mine !== undefined && !same(mine, saved));

// A 412's conflict: the operator's text, the draft revision it was refused at, and whether the
// operator asked to see their change, after which it follows the draft's revision as read.
interface Clash {
  mine: Held;
  revision?: number;
  showing?: boolean;
}
export const follow = (c: Clash | undefined, revision: number): Clash | undefined => (c?.showing === true ? { ...c, revision } : c);

// Show their change: the conflict keeps the operator's text as typed now, edits after the 412
// included, before the read replaces the editor's text with theirs.
export const shown = (c: Clash | undefined, held: Held): Clash | undefined => (c === undefined ? c : { ...c, mine: held, showing: true });

// The references table's rows: the document's references, then the declarations no reference
// uses, so an obsolete declaration can be undeclared (rule unused-declaration).
export const rows = (document: string, declared: Declared) => {
  const refs = references(document);
  return [...refs, ...Object.keys(declared).filter((d) => !refs.includes(d))];
};

// The line of the marks field holding the n-th mark sent: Save leaves out blank lines.
export function markLine(marks: string, n: number): number | undefined {
  let seen = -1;
  const lines = marks.split("\n");
  for (let i = 0; i < lines.length; i++) if (lines[i] !== "" && ++seen === n) return i;
  return undefined;
}

// Where a 422 points, as text beside the refusal: the reference, the mark's line, or the paths.
export function where(p: Problem | null, marks: string): string | undefined {
  if (p === null) return undefined;
  if (typeof p["reference"] === "string") return `Reference ${p["reference"]}.`;
  if (typeof p["position"] === "number") {
    const line = markLine(marks, p["position"]);
    return line === undefined ? `Mark ${p["position"] + 1}.` : `Mark on line ${line + 1}.`;
  }
  const paths = Array.isArray(p["paths"]) ? p["paths"].filter((x): x is string => typeof x === "string") : [];
  return paths.length === 0 ? undefined : `At ${paths.join(", ")}.`;
}

// A write's line: 412 and 422 here, every other state as the forms show it.
function WriteResult({ w, done, conflict, goTo, marks = "" }: { w: Write | undefined; done: (data: unknown) => ReactNode; conflict?: ReactNode; goTo?: () => void; marks?: string }) {
  const o: Outcome | undefined = w?.outcome;
  if (o?.kind === "refused" && o.status === 412 && conflict !== undefined) return conflict;
  if (o?.kind === "refused" && o.status === 422) {
    const at = where(o.problem, marks);
    return (
      <>
        <Refused testid="write-result" problem={o.problem} status={o.status} verb="saved" />
        {at !== undefined && <Status testid="write-where">{at}</Status>}
        {goTo !== undefined && <button type="button" onClick={goTo}>Go to field</button>}
      </>
    );
  }
  return <Result w={w} done={done} />;
}

// The fragment entry (W4): name, layer, the B12 textarea, marks, and the references list with each
// `!bwref` and its declaration. Save is PUT, remove DELETE; Revert to base restores the base text
// for saving (no route drops an entry).
export function FragmentPane({ draft, entry, written, seeChange }: { draft: Draft; entry: Source | undefined; written: () => void; seeChange: () => void }) {
  const writes = useWrites();
  const { client } = useReads();
  const [name, setName] = useState(entry === undefined ? "" : nameOf(entry));
  // The saved revision, as read; EMPTY for a new entry or a removal.
  const [saved, setSaved] = useState<Held>(EMPTY);
  const [held, hold] = useState<Held>(EMPTY);
  const [note, setNote] = useState<string | undefined>();
  const [conflict, setConflict] = useState<Clash | undefined>();
  const [w, send] = useSend();
  // What the last save or removal sent: once it is done, that text is saved, also for an entry
  // new in the draft, whose pane reads no revision.
  const sent = useRef<Held>(EMPTY);
  // The editor's state as last rendered, for a read that answers after it.
  const now = useRef({ held, saved, conflict });
  now.current = { held, saved, conflict };
  const text = useRef<HTMLTextAreaElement>(null);
  const marks = useRef<HTMLTextAreaElement>(null);
  const open = draft.state === "open";
  // Guarded also once the draft no longer takes writes (discarded by another author): the text is
  // still shown, and leaving drops it.
  useUnsaved(`fragment ${name || "(new)"} of draft ${short(draft.id)}`, holdsUnsaved(held, saved, conflict?.mine));

  useEffect(() => {
    if (entry?.revision == null) return;
    let live = true;
    void client.read<Revision>(`/fragment-revisions/${enc(entry.revision)}`).then((r) => {
      if (!live) return;
      if (r.kind !== "ok") return setNote("The entry's revision could not be read.");
      const h = fromRevision(r.data);
      const { held: typed, saved: was, conflict: c } = now.current;
      setSaved(h);
      hold(afterRead(typed, was, h, c !== undefined));
    });
    return () => void (live = false);
  }, [client, entry?.revision]);

  // Once the operator asked to see their change, the conflict follows the draft as read.
  useEffect(() => setConflict((c) => follow(c, draft.revision)), [draft.revision]);

  const o = w?.outcome;
  useEffect(() => {
    if (doneOnce(w) !== undefined) {
      setSaved(sent.current);
      written();
    }
    if (o?.kind === "refused" && o.status === 412 && conflict === undefined) {
      const mine = held;
      setConflict({ mine });
      // For the revision shown only: this read's ETag is not kept (§8.3).
      void client.read<Draft>(`/drafts/${enc(draft.id)}`).then((r) => r.kind === "ok" && setConflict((c) => (c !== undefined && c.revision === undefined ? { ...c, revision: r.data.revision } : c)));
    }
  }, [o]);

  const path = `/drafts/${enc(draft.id)}/fragments/${enc(name)}`;
  const ifMatch = () => keptETag(draft.id, writes?.client?.currentEpoch);
  const settled = keepOnDone(draft.id, () => writes?.client?.currentEpoch);
  const save = () => {
    const m = ifMatch();
    if (name === "") return setNote("Not sent: name the fragment first.");
    if (m === undefined) return setNote(NO_ETAG);
    const lines = held.marks.split("\n").filter((l) => l !== "");
    const declarations = Object.keys(held.declared).length === 0 && held.embedded.length === 0 ? null : { references: held.declared, embedded: held.embedded };
    setNote(undefined);
    // A Save while the last write is still open sends nothing new, so its text is not the one sent.
    if (startsNew(writes!, w)) sent.current = held;
    send({ method: "PUT", path, ifMatch: m, ingests: true, resource: `/drafts/${draft.id}`, body: { layer: held.layer, document: held.document, marks: lines.length === 0 ? null : lines, declarations }, settled });
  };
  const remove = () => {
    const m = ifMatch();
    if (m === undefined) return setNote(NO_ETAG);
    // A removal drops the entry's text on purpose: once done, the editor holds nothing unsaved.
    if (startsNew(writes!, w)) sent.current = held;
    send({ method: "DELETE", path, ifMatch: m, resource: `/drafts/${draft.id}`, body: undefined, settled });
  };
  const revert = async () => {
    if (entry?.head == null) return setNote("No base: this fragment is new in the draft.");
    const f = await client.read<Fragment>(`/fragments/${enc(entry.head)}`);
    if (f.kind !== "ok") return setNote("The fragment's head could not be read.");
    if (f.data.revision === null) return setNote("No base text: the fragment's head is removed.");
    if (f.data.headRevision !== entry.base) return setNote(`No base text to restore: the head moved since this entry's base (revision ${entry.base ?? "none"}, now ${f.data.headRevision}).`);
    const r = await client.read<Revision>(`/fragment-revisions/${enc(f.data.revision)}`);
    if (r.kind !== "ok") return setNote("The base revision could not be read.");
    hold(fromRevision(r.data));
    setNote(`The base text (revision ${short(f.data.revision)}) is in the editor, not saved. Save writes it as a new revision of this entry; the entry stays in the draft.`);
  };
  const goTo = async () => {
    const p = o?.kind === "refused" ? o.problem : null;
    if (p === null) return;
    let spot: Spot | undefined;
    let into = text.current;
    if (typeof p["reference"] === "string") spot = spotOfReference(held.document, p["reference"]);
    else if (typeof p["position"] === "number") {
      into = marks.current;
      const line = markLine(held.marks, p["position"]);
      spot = line === undefined ? undefined : spotOfLine(held.marks, line);
    } else if (Array.isArray(p["paths"]) && typeof p["paths"][0] === "string") spot = await spotOfPath(held.document, p["paths"][0]);
    if (into === null) return;
    into.focus();
    if (spot !== undefined) into.setSelectionRange(spot.start, spot.end);
  };
  const conflictView = conflict !== undefined && (
    <>
      <Conflict what="draft" revision={conflict.revision ?? draft.revision} onShow={() => { setConflict((c) => shown(c, now.current.held)); seeChange(); }} />
      {entry !== undefined && conflict.revision === draft.revision && (
        <div>
          <label>Their text <textarea readOnly value={entry.document ?? "(removed)"} rows={8} cols={60} {...NO_ASSIST} /></label>
          <label>Your text <textarea readOnly value={conflict.mine.document} rows={8} cols={60} {...NO_ASSIST} /></label>
        </div>
      )}
      <button type="button" onClick={() => { hold(conflict.mine); setConflict(undefined); }} disabled={conflict.revision !== draft.revision}>
        Copy my text into the new revision
      </button>
    </>
  );
  const refs = rows(held.document, held.declared);
  const used = references(held.document);
  const declare = (ref: string, r: Declared[string] | undefined) => {
    const next = { ...held.declared };
    if (r === undefined) delete next[ref];
    else next[ref] = r;
    hold({ ...held, declared: next });
  };
  return (
    <section aria-label={`Fragment ${name || "(new)"}`}>
      <h3>Fragment {entry === undefined ? <label>name <input value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" required /></label> : name}</h3>
      <p>
        <label>Layer:{" "}
          <select value={held.layer} onChange={(e) => hold({ ...held, layer: e.target.value })} disabled={!open}>
            {LAYERS.map((l) => <option key={l}>{l}</option>)}
          </select>
        </label>{" "}
        {entry === undefined ? "new" : change(entry)}{!same(held, saved) && " -> unsaved"}
      </p>
      {entry?.revision === null && <Status>Removed in this draft.</Status>}
      <p><label>Document <textarea ref={text} value={held.document} onChange={(e) => hold({ ...held, document: e.target.value })} readOnly={!open} rows={16} cols={80} {...NO_ASSIST} /></label></p>
      <p><label>Marks, one path per line <textarea ref={marks} value={held.marks} onChange={(e) => hold({ ...held, marks: e.target.value })} readOnly={!open} rows={3} cols={80} {...NO_ASSIST} /></label></p>
      <h4>References in this document</h4>
      {refs.length === 0 ? <p>None.</p> : (
        <Scroll label="References in this document">
          <thead><tr>{["Reference", "Kind", "Version", "Encoding", "State"].map((h) => <th key={h} scope="col">{h}</th>)}</tr></thead>
          <tbody>{refs.map((ref) => <ReferenceRow key={ref} name={ref} declared={held.declared[ref]} used={used.includes(ref)} set={(r) => declare(ref, r)} open={open} />)}</tbody>
        </Scroll>
      )}
      <EmbeddedList embedded={held.embedded} set={(embedded) => hold({ ...held, embedded })} open={open} />
      {open && (
        <p>
          <Action label="Save" needs={["author"]} onClick={save} busy={w !== undefined && w.outcome === undefined} />{" "}
          <Action label="Revert to base" needs={["author"]} onClick={() => void revert()} />{" "}
          {entry !== undefined && entry.revision !== null && <Action label="Remove" needs={["author"]} onClick={remove} />}
        </p>
      )}
      {note !== undefined && <Status testid="editor-note">{note}</Status>}
      <WriteResult w={w} conflict={conflictView || undefined} goTo={() => void goTo()} marks={sent.current.marks} done={(data) => {
        if (w?.spec.method === "DELETE") return `Fragment ${name} removed from draft ${short(draft.id)}.`;
        const r = (data as Partial<S["SourceUpdate"]>).entry?.revision;
        return `Fragment ${name} saved in draft ${short(draft.id)}${typeof r === "string" ? ` (revision ${short(r)})` : ""}.`;
      }} />
    </section>
  );
}

// §11.4 item 2: browser spelling and fill services get nothing of a document or marks.
const NO_ASSIST = { autoComplete: "off", spellCheck: false, autoCorrect: "off", autoCapitalize: "off" } as const;

function ReferenceRow({ name, declared, used, set, open }: { name: string; declared: Declared[string] | undefined; used: boolean; set: (r: Declared[string] | undefined) => void; open: boolean }) {
  const kind = declared?.kind ?? "string";
  return (
    <tr>
      <th scope="row">{name}</th>
      <td>
        <select aria-label={`Kind of ${name}`} value={kind} disabled={!open} onChange={(e) => set({ kind: e.target.value as Declared[string]["kind"], version: declared?.version ?? 1 })}>
          {KINDS.map((k) => <option key={k}>{k}</option>)}
        </select>
      </td>
      <td><input aria-label={`Version of ${name}`} type="number" min={1} value={declared?.version ?? 1} disabled={!open} onChange={(e) => set({ ...(declared ?? { kind }), version: Number(e.target.value) })} /></td>
      <td>
        <select aria-label={`Encoding of ${name}`} value={declared?.encoding ?? ""} disabled={!open || kind !== "string"} onChange={(e) => {
          const { encoding: _, ...rest } = declared ?? { kind, version: 1 };
          set(e.target.value === "base64" ? { ...rest, encoding: "base64" } : rest);
        }}>
          <option value="">none</option><option value="base64">base64</option>
        </select>
      </td>
      <td>
        {declared === undefined ? "carried, not declared" : used ? "declared" : "declared, not in the document"}{" "}
        {open && (declared === undefined ? <button type="button" onClick={() => set({ kind, version: 1 })}>Declare</button> : <button type="button" onClick={() => set(undefined)}>Undeclare</button>)}
      </td>
    </tr>
  );
}

function EmbeddedList({ embedded, set, open }: { embedded: Embedded[]; set: (e: Embedded[]) => void; open: boolean }) {
  const put = (i: number, e: Embedded | undefined) => set(e === undefined ? embedded.filter((_, j) => j !== i) : embedded.map((x, j) => (j === i ? e : x)));
  return (
    <>
      <h4>Embedded documents</h4>
      {embedded.length === 0 && <p>None.</p>}
      <ul aria-label="Embedded documents">
        {embedded.map((e, i) => (
          <li key={i}>
            <label>Path <input value={e.path} readOnly={!open} onChange={(x) => put(i, { ...e, path: x.target.value })} autoComplete="off" /></label>{" "}
            <select aria-label="Format" value={e.format} disabled={!open} onChange={(x) => put(i, { ...e, format: x.target.value as Embedded["format"] })}>
              <option>yaml</option><option>json</option>
            </select>{" "}
            {open && <button type="button" onClick={() => put(i, undefined)}>Remove</button>}
          </li>
        ))}
      </ul>
      {open && <button type="button" onClick={() => set([...embedded, { path: "", format: "yaml" }])}>Add embedded document</button>}
    </>
  );
}
