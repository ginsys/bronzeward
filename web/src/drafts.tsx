// The draft editor (ginsys/bronzeward#339; #257 B12-B14; journeys §5.4; W4, W11 "Refused" and
// "Conflict"): the Changes list of drafts, Create draft, the draft's page with its entries and the
// fragment entry, Discard draft. Every write goes through write.ts with the draft's last-seen ETag
// as If-Match (client contracts §8.3). A document is typed into a plain textarea (B12) and lives in
// this page's state only: never stored, and guarded while unsaved (§11.4).

import { type ReactNode, useCallback, useEffect, useRef, useState } from "react";
import type { components } from "./api/v1";
import { Form, field, Result, Text, useSend, useUnsaved, useWrites } from "./forms.tsx";
import { Id, Ref, Scroll, short, useItem, useList, useReads, usePending } from "./list.tsx";
import type { Cluster } from "./machines.tsx";
import { references, type Spot, spotOfLine, spotOfPath, spotOfReference } from "./position.ts";
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
  const o = w?.outcome;
  useEffect(() => {
    if (o?.kind === "done" && o.etag !== undefined) keepETag((o.data as Draft).id, o.etag, writes?.client?.currentEpoch);
  }, [o, writes]);
  const submit = (d: FormData) => send({ method: "POST", path: "/drafts", resource: "/changes", body: { cluster: field(d, "cluster"), title: field(d, "title") } });
  const done = (data: unknown) => {
    const d = data as Draft;
    return <>Draft <Ref to={`/drafts/${d.id}`} id={d.id} /> "{d.title}" created in cluster {short(d.cluster)}.</>;
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
  const written = useCallback((etag: string | undefined) => {
    if (etag !== undefined) keepETag(id, etag, writes?.client?.currentEpoch);
    reread((x) => x + 1);
  }, [id, writes]);
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
                {e.kind === "fragment" ? <button type="button" aria-pressed={selected === keyOf(e)} onClick={() => select(keyOf(e))}>fragment {nameOf(e)}</button> : <>{e.kind} {short(nameOf(e))}</>}
                {" "}{change(e)}
              </li>
            ) : <li key={`import-base:${e.machine}`}>import base <Id id={e.machine} /> <Id id={e.revision} /></li>)}
          </ul>
          {d.state === "open" && <p><Action label="Add fragment" needs={["author"]} onClick={() => select("fragment:")} /></p>}
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

function DiscardDraft({ draft, written }: { draft: Draft; written: (etag: string | undefined) => void }) {
  const [w, send] = useSend();
  const writes = useWrites();
  const [note, setNote] = useState<string | undefined>();
  const o = w?.outcome;
  useEffect(() => {
    if (doneOnce(w) === undefined) return;
    forgetETag(draft.id);
    written(undefined);
  }, [o, w, draft.id, written]);
  const discard = () => {
    if (!confirm(`Discard draft ${draft.id} "${draft.title}"? It cannot be published afterwards.`)) return;
    const ifMatch = keptETag(draft.id, writes?.client?.currentEpoch);
    setNote(ifMatch === undefined ? NO_ETAG : undefined);
    if (ifMatch !== undefined) send({ method: "POST", path: `/drafts/${enc(draft.id)}/discard`, body: {}, ifMatch, resource: `/drafts/${draft.id}` });
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
interface Held {
  layer: string;
  document: string;
  marks: string;
  declared: Declared;
  embedded: Embedded[];
}
const EMPTY: Held = { layer: "role", document: "", marks: "", declared: {}, embedded: [] };
const fromRevision = (r: Revision): Held => ({ layer: r.layer, document: r.document, marks: "", declared: { ...r.declarations.references }, embedded: [...r.declarations.embedded] });
const same = (a: Held, b: Held) => a.layer === b.layer && a.document === b.document && a.marks === b.marks && JSON.stringify(a.declared) === JSON.stringify(b.declared) && JSON.stringify(a.embedded) === JSON.stringify(b.embedded);

// A write's line: 412 and 422 here, every other state as the forms show it.
function WriteResult({ w, done, conflict, goTo }: { w: Write | undefined; done: (data: unknown) => ReactNode; conflict?: ReactNode; goTo?: () => void }) {
  const o: Outcome | undefined = w?.outcome;
  if (o?.kind === "refused" && o.status === 412 && conflict !== undefined) return conflict;
  if (o?.kind === "refused" && o.status === 422) {
    return (
      <>
        <Refused testid="write-result" problem={o.problem} status={o.status} verb="saved" />
        {goTo !== undefined && <button type="button" onClick={goTo}>Go to field</button>}
      </>
    );
  }
  return <Result w={w} done={done} />;
}

// The fragment entry (W4): name, layer, the B12 textarea, marks, and the references list with each
// `!bwref` and its declaration. Save is PUT, remove DELETE; Revert to base restores the base text
// for saving (no route drops an entry).
export function FragmentPane({ draft, entry, written, seeChange }: { draft: Draft; entry: Source | undefined; written: (etag: string | undefined) => void; seeChange: () => void }) {
  const writes = useWrites();
  const { client } = useReads();
  const [name, setName] = useState(entry === undefined ? "" : nameOf(entry));
  // The saved revision, as read; EMPTY for a new entry or a removal.
  const [saved, setSaved] = useState<Held>(EMPTY);
  const [held, hold] = useState<Held>(EMPTY);
  const [note, setNote] = useState<string | undefined>();
  const [conflict, setConflict] = useState<{ mine: Held; revision?: number; theirs?: string } | undefined>();
  const [w, send] = useSend();
  // What the last save or removal sent: once it is done, that text is saved, also for an entry
  // new in the draft, whose pane reads no revision.
  const sent = useRef<Held>(EMPTY);
  const text = useRef<HTMLTextAreaElement>(null);
  const marks = useRef<HTMLTextAreaElement>(null);
  const open = draft.state === "open";
  useUnsaved(`fragment ${name || "(new)"} of draft ${short(draft.id)}`, open && !same(held, saved));

  useEffect(() => {
    if (entry?.revision == null) return;
    let live = true;
    void client.read<Revision>(`/fragment-revisions/${enc(entry.revision)}`).then((r) => {
      if (!live) return;
      if (r.kind !== "ok") return setNote("The entry's revision could not be read.");
      const h = fromRevision(r.data);
      setSaved(h);
      hold(h);
    });
    return () => void (live = false);
  }, [client, entry?.revision]);

  const o = w?.outcome;
  useEffect(() => {
    const done = doneOnce(w);
    if (done !== undefined) {
      setSaved(sent.current);
      written(done.etag);
    }
    if (o?.kind === "refused" && o.status === 412 && conflict === undefined) {
      const mine = held;
      setConflict({ mine });
      // For the revision shown only: this read's ETag is not kept (§8.3).
      void client.read<Draft>(`/drafts/${enc(draft.id)}`).then((r) => r.kind === "ok" && setConflict({ mine, revision: r.data.revision }));
    }
  }, [o]);

  const path = `/drafts/${enc(draft.id)}/fragments/${enc(name)}`;
  const ifMatch = () => keptETag(draft.id, writes?.client?.currentEpoch);
  const save = () => {
    const m = ifMatch();
    if (name === "") return setNote("Not sent: name the fragment first.");
    if (m === undefined) return setNote(NO_ETAG);
    const lines = held.marks.split("\n").filter((l) => l !== "");
    const declarations = Object.keys(held.declared).length === 0 && held.embedded.length === 0 ? null : { references: held.declared, embedded: held.embedded };
    setNote(undefined);
    sent.current = held;
    send({ method: "PUT", path, ifMatch: m, ingests: true, resource: `/drafts/${draft.id}`, body: { layer: held.layer, document: held.document, marks: lines.length === 0 ? null : lines, declarations } });
  };
  const remove = () => {
    const m = ifMatch();
    if (m === undefined) return setNote(NO_ETAG);
    // A removal drops the entry's text on purpose: once done, the editor holds nothing unsaved.
    sent.current = held;
    send({ method: "DELETE", path, ifMatch: m, resource: `/drafts/${draft.id}`, body: undefined });
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
      spot = spotOfLine(held.marks, p["position"]);
    } else if (Array.isArray(p["paths"]) && typeof p["paths"][0] === "string") spot = await spotOfPath(held.document, p["paths"][0]);
    if (into === null) return;
    into.focus();
    if (spot !== undefined) into.setSelectionRange(spot.start, spot.end);
  };
  const conflictView = conflict !== undefined && (
    <>
      <Conflict what="draft" revision={conflict.revision ?? draft.revision} onShow={seeChange} />
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
  const refs = references(held.document);
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
          <tbody>{refs.map((ref) => <ReferenceRow key={ref} name={ref} declared={held.declared[ref]} set={(r) => declare(ref, r)} open={open} />)}</tbody>
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
      <WriteResult w={w} conflict={conflictView || undefined} goTo={() => void goTo()} done={(data) => {
        const u = data as S["SourceUpdate"];
        return u.entry.revision === null ? `Fragment ${name} removed from draft ${short(draft.id)}.` : `Fragment ${name} saved in draft ${short(draft.id)} (revision ${short(u.entry.revision)}).`;
      }} />
    </section>
  );
}

// §11.4 item 2: browser spelling and fill services get nothing of a document or marks.
const NO_ASSIST = { autoComplete: "off", spellCheck: false, autoCorrect: "off", autoCapitalize: "off" } as const;

function ReferenceRow({ name, declared, set, open }: { name: string; declared: Declared[string] | undefined; set: (r: Declared[string] | undefined) => void; open: boolean }) {
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
        {declared === undefined ? "carried, not declared" : "declared"}{" "}
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
