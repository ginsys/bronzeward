// The draft editor (ginsys/bronzeward#339; #257 B12-B14; journeys §5.4; W4, W11 "Refused" and
// "Conflict"): the Changes list of drafts, Create draft, the draft's page with its entries and the
// fragment entry, Discard draft. Every write goes through write.ts with the draft's last-seen ETag
// as If-Match (client contracts §8.3). A document is typed into a plain textarea (B12) and lives in
// this page's state only: never stored, and guarded while unsaved (§11.4).

import { type ReactNode, useCallback, useEffect, useReducer, useRef, useState } from "react";
import type { components } from "./api/v1";
import { AssignmentPane, ProfilePane } from "./composition.tsx";
import { Form, field, Result, startsNew, Text, unsaved, useSend, useUnsaved, useWrites } from "./forms.tsx";
import { LAYERS } from "./layers.ts";
import { Id, type Load, Ref, Scroll, short, useItem, useList, useReads, usePending, useSettled } from "./list.tsx";
import type { Cluster } from "./machines.tsx";
import { type EmbeddedPath, references, type Spot, spotOfLine, spotOfPath, spotOfReference } from "./position.ts";
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

export { LAYERS };
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

// The draft pages shown now, per draft: each one's `written`, which reads that draft again.
const shownPages = new Map<string, Set<() => void>>();
export function watchWrites(draft: string, written: () => void): () => void {
  const pages = shownPages.get(draft) ?? new Set();
  shownPages.set(draft, pages.add(written));
  return () => {
    pages.delete(written);
    if (pages.size === 0 && shownPages.get(draft) === pages) shownPages.delete(draft);
  };
}

// A fragment write's settled hook: a done answer keeps its ETag and reads the draft again, also when
// the operator moved to another entry while it was in flight. Otherwise the entry shows its old
// revision under the new ETag, and a second Save overwrites the first unseen (ginsys/bronzeward#517 H3).
// The page that reads it again is the one shown when the write settles, not the one that sent it:
// the operator may have left and opened the draft again meanwhile (ginsys/bronzeward#534 C1).
export const settledFragment = (draft: string, epoch: () => string | undefined) => {
  const keep = keepOnDone(draft, epoch);
  return (o: Outcome, w: Write) => {
    keep(o, w);
    if (o.kind === "done") for (const written of [...(shownPages.get(draft) ?? [])]) written();
  };
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

// The entry a fragment pane edits: the selected one, or for Add fragment the entry its Save named,
// once a draft read shows it. The pane then reads that entry's revisions and conflicts
// (ginsys/bronzeward#517 H1).
export function paneEntry(sources: Source[], selected: string | undefined, claimed: string | undefined) {
  const k = (selected === "fragment:" || selected === "profile:" || selected === "assignment:") && claimed !== undefined ? `${selected}${claimed}` : selected;
  return sources.find((e) => keyOf(e) === k);
}

// Whether the draft already holds an entry of this kind and name, a removed one included: a PUT to
// it would replace that entry unread (ginsys/bronzeward#517 H4).
export const inUse = (draft: Pick<Draft, "entries">, name: string, kind: "fragment" | "profile" | "assignment" = "fragment") => draft.entries.some((e) => isSource(e) && e.kind === kind && nameOf(e) === name);

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
  // The name Add fragment's last Save sent: the pane edits that entry once the draft holds it (H1).
  const [claimed, claim] = useState<string | undefined>();
  // Each pick opens a new pane; binding a new pane to its entry keeps it.
  const [pane, nextPane] = useState(0);
  const pending = usePending(item, "the draft");
  const last = useRef<{ id: string; data: Draft }>(undefined);
  last.current = lastGood(last.current, id, item);
  const d = last.current?.data;
  // A write's answer keeps its ETag as it settles (keepOnDone); the page then reads the draft again.
  // Until that read shows, the entries shown are older than the ETag, so no pane sends (finding of
  // the ginsys/bronzeward#517 review: a pane opened again during that read showed the old text).
  // A newer read discards an older one (useItem), so the next one with data is the one after it.
  const [unshown, setUnshown] = useState(false);
  const written = useCallback(() => {
    setUnshown(true);
    reread((x) => x + 1);
  }, []);
  useEffect(() => {
    if (item.kind === "ok") setUnshown(false);
  }, [item]);
  useEffect(() => watchWrites(id, written), [id, written]);
  const sources = d?.entries.filter(isSource) ?? [];
  const entry = paneEntry(sources, selected, claimed);
  const current = entry === undefined ? selected : keyOf(entry);
  // A switch to another entry, or to a new one, asks first while the editor holds unsaved text.
  const pick = (k: string) => {
    if (k === current || !mayDrop()) return;
    select(k);
    claim(undefined);
    nextPane((x) => x + 1);
  };
  const seeChange = () => {
    keep.current = true;
    reread((x) => x + 1);
  };
  return (
    <>
      <h2>Draft {short(id)}{d !== undefined && <> "{d.title}" {d.state}, revision {d.revision}, cluster <Id id={d.cluster} /></>}</h2>
      {pending}
      {/* That read failed, also when the page keeps the data read before (no answer): reading it
          again keeps the editor, and lets Save and Remove send again. */}
      {unshown && item.kind !== "loading" && <p><button type="button" onClick={() => reread((x) => x + 1)}>{READ_DRAFT_AGAIN}</button></p>}
      {d !== undefined && (
        <>
          <h3>Entries</h3>
          <ul aria-label="Entries">
            {d.entries.map((e) => isSource(e) ? (
              <li key={keyOf(e)}>
                <button type="button" aria-pressed={current === keyOf(e)} onClick={() => pick(keyOf(e))}>{e.kind} {e.kind === "assignment" ? short(nameOf(e)) : nameOf(e)}</button>
                {" "}{change(e)}
              </li>
            ) : <li key={`import-base:${e.machine}`}>import base <Id id={e.machine} /> <Id id={e.revision} /></li>)}
          </ul>
          {d.state === "open" && <p><Action label="Add fragment" needs={["author"]} onClick={() => pick("fragment:")} />{" "}<Action label="Add profile" needs={["author"]} onClick={() => pick("profile:")} />{" "}<Action label="Add assignment" needs={["author"]} onClick={() => pick("assignment:")} /></p>}
          {selected !== undefined && selected.startsWith("fragment:") && (
            <FragmentPane key={pane} draft={d} entry={entry} seeChange={seeChange} claim={claim} shown={!unshown} />
          )}
          {selected !== undefined && selected.startsWith("profile:") && (
            <ProfilePane key={pane} draft={d} entry={entry} seeChange={seeChange} claim={claim} shown={!unshown} />
          )}
          {selected !== undefined && selected.startsWith("assignment:") && (
            <AssignmentPane key={pane} draft={d} entry={entry} seeChange={seeChange} claim={claim} shown={!unshown} />
          )}
          <Status testid="not-rendered">{NOT_RENDERED}</Status>
          {d.state === "open" && <DiscardDraft draft={d} written={written} seeChange={seeChange} />}
        </>
      )}
    </>
  );
}

// A Discard's 412: the draft changed since the page read it. Show their change reads it again and
// keeps that read's ETag, so a Discard after it applies to the draft as shown (ginsys/bronzeward#517 H15).
export const NOT_DISCARDED = "Not discarded: someone changed this draft since this page read it.";
// The new ETag would also let the fragment editor's Save overwrite their change unseen, so an editor
// with unsaved text is saved or left first.
export const EDITOR_FIRST = "Not read: the fragment editor holds unsaved text. Save it or switch entries first.";
export const DiscardRefused = ({ onShow }: { onShow: () => void }) => (
  <Status testid="write-result" actions={<button type="button" onClick={onShow}>Show their change</button>}>{NOT_DISCARDED}</Status>
);

function DiscardDraft({ draft, written, seeChange }: { draft: Draft; written: () => void; seeChange: () => void }) {
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
      <WriteResult w={w} conflict={<DiscardRefused onShow={() => (unsaved.size > 0 ? setNote(EDITOR_FIRST) : seeChange())} />} done={() => `Draft ${short(draft.id)} discarded.`} />
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

// Whether the editor holds text not saved: its own, or the operator's text a conflict keeps aside.
export const holdsUnsaved = (held: Held, saved: Held, mine: Held | undefined) => !same(held, saved) || (mine !== undefined && !same(mine, saved));

// The fragment editor's text and a 412's conflict (W11 "Conflict", B14), changed only by `editor`.
// `held` is the editor's text, `saved` the entry's text as last read or saved. A conflict is
// "refused" after the 412, "asked" once Show their change read the draft again, and "theirs" once
// their text shows. Until theirs shows, `mine` is the editor's text, so nothing typed is lost; then
// it keeps the operator's text aside. `revision`: the draft revision refused at, once shown the
// draft's revision as read. `sent`: the last Save's or Remove's text, and whether their text showed
// then; `writes`: the done writes so far. Each event that answers an async request says when it
// started, so an answer older than the operator's action changes nothing. `version` counts the
// events that changed the editor's text or the conflict: Revert to base applies only at its click's.
// `awaits`: while asked, a draft read named a new revision of this entry, and only its revision read
// shows their text (ginsys/bronzeward#517 L1); once theirs, Show their change was clicked again and
// its draft read has not answered, so Copy waits; a new revision that read names then waits on
// `reading`. `sent.removes`: the write was a Remove, which saves
// the removal, not the text (H8). `reading`: the entry's newest revision is not read yet, its read
// pending or failed, whatever the conflict's phase was when it started, so Show their change waits
// for it too; only a read with text clears it. `again`: while asked, Show their change was clicked
// again and its draft read has not answered, so a revision read that answers first shows their text
// with Copy waiting (ginsys/bronzeward#534 N1).
// `own`: the revision the pane's last done Save created. The server stores its own form of the
// text sent, so the read of that revision is known by its id, not by its text.
// Without a 412, a conflict opens when a read shows another author's text or removal while the
// editor holds unsaved text, or once a Discard's Show their change adopted that read's ETag
// (ginsys/bronzeward#534 C1-C3): the typed text is kept aside, never saved over theirs unseen.
// Once their text shows, another author's later text or removal shows the same way, text typed
// since then kept aside in place of the older one (ginsys/bronzeward#551). `typed`: text was typed
// since their text showed. A done Remove also leaves the editor unlike `saved`, so the editor's
// text alone does not tell (local review).
// `sent.open`: the write may still commit, its answer not come yet. A read meanwhile may show that
// write's own change, so it opens no such conflict; another author's change refuses the write with
// a 412 instead (local review round 3). `sent.later`: such a read came: the revision it named, its
// id and text, or `removed` for another author's removal; each newer one replaces the older whole.
// If the write's last answer is neither done nor a 412, that read shows as theirs, the typed text
// kept aside (rounds 4 and 5). So does it once a Save is done, unless the read is the Save's own
// revision, or the answer or the read has no id to tell (ginsys/bronzeward#559).
export interface Clash { mine: Held; phase: "refused" | "asked" | "theirs"; revision?: number; awaits?: true; again?: true; typed?: true }
export interface Later { revision?: number; id?: string; text?: Held; removed?: true; aside?: true }
export interface Editor { held: Held; saved: Held; conflict?: Clash; sent?: { text: Held; whileTheirs: boolean; removes?: true; open?: true; later?: Later }; writes?: number; note?: string | undefined; version?: number; reading?: true; own?: string; gone?: number }
// The pane's first state. `gone` follows the revision of a removal: a number is the revision known
// to be removed, -1 means nothing was removed yet, and unset means the pane's own done Remove saved
// EMPTY and its draft read has not shown the removal. A new fragment starts with -1, so another
// author's removal, read while its first Save is open, is held back for that Save's answer and shows
// when it is done, not dropped as the pane's own Remove (ginsys/bronzeward#559 PR review).
export const FRESH: Editor = { held: EMPTY, saved: EMPTY, gone: -1 };

// A revision read that began before a write finished may name the revision from before that write,
// since the draft refresh has not answered: it is not used (ginsys/bronzeward#559 PR review).
export const staleRead = (finishedNow: number, startedAt: number): boolean => finishedNow !== startedAt;

// The newer of two draft revisions a conflict can name: the one it already follows, or the one of a text held
// back while a write was open. A conflict never moves back to an older revision (ginsys/bronzeward#559 review).
const newest = (a: number | undefined, b: number | undefined): number | undefined => (a === undefined ? b : b === undefined ? a : Math.max(a, b));

export type EditorEvent =
  | { kind: "type"; held: Held } // the operator's edit
  | { kind: "refused" } // a write's 412
  // The write's last answer is neither done nor a 412: another refusal, nothing committed, or set
  // aside. An unknown one is not last: [Repeat exactly] may still commit it, or answer 412.
  | { kind: "failed" }
  | { kind: "at"; revision: number } // the 412's own draft read
  | { kind: "show" } // Show their change
  // The page's draft read. changed: this entry has a new revision to read; removed: the entry is a removal.
  | { kind: "draft"; revision: number; changed: boolean; removed?: true }
  | { kind: "reading"; on: boolean } // the entry's revision read starts, or is dropped for another
  // The entry's revision read, started after `since` done writes, for the draft revision that named it.
  // `id`: the revision read. `reread`: it is the revision the pane had loaded already, read again.
  | { kind: "read"; text: Held; since: number; revision?: number; id?: string; reread?: true }
  | { kind: "copy" } // Copy my text into the new revision
  | { kind: "send"; removes?: true } // a Save or Remove sends the editor's text
  | { kind: "done"; revision?: string } // that write is done; a Save's answer names the revision it created
  | { kind: "revert"; at: number; text: Held; note: string } // Revert to base's read answers; `at`: the version at the click
  | { kind: "note"; text?: string };

export const NOT_REVERTED = "Not reverted: the text changed while the base text was read, or a conflict keeps your text. Show their change first.";

// A draft read while a Save is open and their text shows: what the read held back stands for now.
// A removal replaces it whole; a read that changed only another entry moves its revision, so a
// later `failed` or done names the draft revision the conflict follows (ginsys/bronzeward#559).
function noted(s: Editor, e: EditorEvent): Editor {
  const { conflict: c, sent } = s;
  if (e.kind !== "draft" || sent?.open !== true) return s;
  // Without a conflict, the pane's own done Remove saved EMPTY and `gone` is the removal revision its
  // draft read showed. A removal at a newer revision, read while a Save is open, is another
  // author's: it is held back as a removal read with text saved is (#559 hosted review).
  if (c === undefined) return e.removed === true && same(s.saved, EMPTY) && s.gone !== undefined && e.revision > s.gone && sent.later?.removed !== true ? { ...s, sent: { ...sent, later: { revision: e.revision, removed: true } } } : s;
  if (c.phase !== "theirs" || e.revision < (c.revision ?? 0)) return s;
  // A removal at a revision the conflict follows already, with the saved text empty, is taken as the
  // pane's own done Remove read again. A draft read names no author, so this is an assumption. It
  // fails when another author removes the entry again before the pane's read of its own Remove
  // answers, with or without restoring it first: the entry is removed at that revision either way,
  // and no text is lost.
  // A removal at a newer revision is another author's (#559 local reviews 1 to 3).
  if (e.removed === true) return same(s.saved, EMPTY) && sent.later?.removed !== true && c.revision !== undefined && e.revision <= c.revision ? s : { ...s, sent: { ...sent, later: { revision: e.revision, removed: true } } };
  return sent.later === undefined || e.changed ? s : { ...s, sent: { ...sent, later: { ...sent.later, revision: e.revision } } };
}

export function editor(s: Editor, e: EditorEvent): Editor {
  const n = step(noted(s, e), e);
  const moved = !same(s.held, n.held) || s.conflict?.phase !== n.conflict?.phase || (s.conflict !== undefined && n.conflict !== undefined && !same(s.conflict.mine, n.conflict.mine));
  return moved ? { ...n, version: (s.version ?? 0) + 1 } : n;
}

function step(s: Editor, e: EditorEvent): Editor {
  const c = s.conflict;
  const at = (x: Partial<Clash>, held = s.held): Editor => ({ ...s, held, conflict: { ...c!, ...x } });
  const writes = s.writes ?? 0;
  const { conflict: _, ...ended } = s;
  // Their newer text or removal shows: text typed since the last shown is the operator's now.
  const kept = (x: Clash): Clash => {
    const { typed: _typed, ...rest } = x;
    return { ...rest, mine: x.typed === true ? s.held : x.mine };
  };
  switch (e.kind) {
    case "type":
      if (c?.phase === "theirs") return { ...s, held: e.held, conflict: { ...c, typed: true } };
      return c === undefined ? { ...s, held: e.held } : at({ mine: e.held }, e.held);
    case "refused":
      // Once their text shows, the operator's text is the editor's: a new 412 keeps that, not the
      // text the last conflict kept aside (ginsys/bronzeward#517 H6). Before, both are the same.
      return c === undefined || c.phase === "theirs" ? { ...s, ...closed(s), conflict: { mine: s.held, phase: "refused" } } : { ...s, ...closed(s) };
    case "failed": {
      // A read held back while the write was open shows now, also once their text shows: then the
      // conflict has followed every draft read since, so the newer revision names it (#551 item 2).
      const later = s.sent?.later;
      if (later === undefined || (c !== undefined && c.phase !== "theirs") || same(s.held, s.saved)) return { ...s, ...closed(s) };
      const revision = newest(later.revision, c?.revision);
      return { ...s, ...closed(s), held: s.saved, conflict: { ...(c === undefined ? { mine: s.held } : kept(c)), phase: "theirs", ...(revision !== undefined && { revision }) } };
    }
    case "at":
      return c !== undefined && c.revision === undefined ? at({ revision: e.revision }) : s;
    case "show":
      // Shown again once their text shows: Copy waits until the draft read, and the revision read it
      // starts, answer, so a newer revision cannot replace the copied text (PR review).
      return c?.phase === "refused" ? at({ phase: "asked" }) : c?.phase === "asked" ? at({ again: true }) : c?.phase === "theirs" ? at({ awaits: true }) : s;
    case "draft":
      // Another author removed this entry, which no 412 announced (C3): the removal shows, and text
      // typed since the last read or save is kept aside. The pane's own done Remove saved EMPTY,
      // and keeps the text it shows (H8).
      if (c === undefined) {
        // A read held back for the open write now stands for this draft revision: it changed only
        // another entry (ginsys/bronzeward#551 item 3).
        if (s.sent?.open === true && s.sent.later !== undefined && !e.changed && e.removed !== true) return { ...s, sent: { ...s.sent, later: { ...s.sent.later, revision: e.revision } } };
        // A newer removal read moves the revision of a removal held back (#559).
        if (e.removed === true && s.sent?.open === true && s.sent.later?.removed === true && e.revision > (s.sent.later.revision ?? 0)) return { ...s, sent: { ...s.sent, later: { revision: e.revision, removed: true } } };
        if (e.removed === true && same(s.saved, EMPTY) && s.sent?.open !== true) return e.revision > (s.gone ?? -1) ? { ...s, gone: e.revision } : s;
        if (e.removed !== true || same(s.saved, EMPTY)) return s;
        if (s.sent?.open === true) return { ...s, saved: EMPTY, sent: { ...s.sent, later: { revision: e.revision, removed: true } } };
        // The revision this removal read shows is followed too, so a newer one is told from it (#559).
        return { ...s, held: EMPTY, saved: EMPTY, gone: e.revision, ...(!same(s.held, s.saved) && { conflict: { mine: s.held, phase: "theirs" as const, revision: e.revision } }) };
      }
      if (c.phase === "refused" || e.revision < (c.revision ?? 0)) return s;
      // The draft read a repeated Show started has answered (N1).
      if (c.again === true) {
        const { again: _again, ...answered } = c;
        return step({ ...s, conflict: answered }, e);
      }
      // Their change removed this entry: their text is the removal, which no revision read shows (H7).
      if (c.phase === "asked" && e.removed === true) {
        const { awaits: _awaited, ...shown } = c;
        return { ...s, held: EMPTY, saved: EMPTY, conflict: { ...shown, phase: "theirs", revision: e.revision } };
      }
      // A revision read pending since before Show, a draft read ignored while refused started it,
      // also gives their text.
      if (c.phase === "asked" && (e.changed || s.reading === true)) return at({ revision: e.revision, awaits: true });
      // Once their text shows, another author's removal shows too, as C3 does without a conflict, and
      // ends a repeated Show's wait. Text typed since is the operator's text now (H6). The pane's own
      // done Remove saved EMPTY and keeps its text (H8). An open write's answer settles it first
      // (ginsys/bronzeward#551 item 1): the removal is held back as without a conflict, `noted` records
      // it in `sent.later`, and it shows once that write fails or a Save is done (#559).
      if (c.phase === "theirs" && e.removed === true && !same(s.saved, EMPTY)) {
        const { awaits: _awaited, ...shown } = c;
        if (s.sent?.open === true) return { ...s, saved: EMPTY, conflict: { ...shown, revision: e.revision } };
        return { ...s, held: EMPTY, saved: EMPTY, conflict: { ...kept(shown), revision: e.revision } };
      }
      // Shown again: the draft read it started ends the wait, since a newer draft read discards an
      // older one. A new revision it names is read next, and Copy waits for that read (`reading`);
      // a revision read that answers before this draft read ends nothing (round-6 review).
      if (c.phase === "theirs" && c.awaits === true) {
        const { awaits: _awaited, ...shown } = c;
        return { ...s, ...(e.changed && { reading: true as const }), conflict: { ...shown, revision: e.revision } };
      }
      // Their change left this entry as read: its saved text is theirs. A later draft read that
      // finds the awaited revision already named changes nothing.
      return c.phase === "asked" && c.awaits !== true ? at({ phase: "theirs", revision: e.revision }, s.saved) : at({ revision: e.revision });
    case "reading": {
      const { reading: _was, ...idle } = s;
      return e.on ? { ...s, reading: true } : idle;
    }
    case "read": {
      const { reading: _was, ...idle } = s;
      if (e.since !== writes) return idle;
      // The read Show asked for shows their text; any other replaces only text not typed into. One
      // that answers before Show's draft read leaves Copy waiting for that read (N1): it may name a
      // newer revision.
      if (c?.phase === "asked") {
        const { awaits, again, ...shown } = c;
        return { ...idle, saved: e.text, held: e.text, conflict: { ...shown, phase: "theirs", ...((awaits !== true || again === true) && { awaits: true as const }) } };
      }
      // Without a conflict, another author's text read while the editor holds typed text shows, and
      // the typed text is kept aside (C1, C2). Once their text shows, another author's newer text
      // shows, and text typed since is kept aside in place of the older (ginsys/bronzeward#551
      // item 2); a repeated Show still waits. The read of the pane's own done Save is not theirs,
      // and a read while a write is open waits for that write's answer. Once their text shows with
      // nothing to keep aside, the read replaces the text, as below: held back, a done write would
      // drop it (#551 local review).
      // While the pane's own Save or Remove is open, a read of a revision the pane has not read before
      // is held back until the answer, also when its text equals the saved text: a restore brings back
      // the text removed, and another author can store the saved text again. A done Remove shows it.
      // A done Save drops it only when it is the Save's own revision, which only the answer names. The
      // Remove sends the draft's ETag as If-Match, so a revision stored before it gets a 412, not a
      // done. If the Remove is done, a new revision read now came after it; a write that fails shows
      // the text read in any case. `reread` is the revision the pane loaded already, read again; it
      // stays dropped (ginsys/bronzeward#623 review, #624).
      const unseen = e.reread !== true && s.sent?.open === true;
      const another = (s.own === undefined || e.id !== s.own) && (!same(e.text, s.saved) || unseen);
      const keeps = c === undefined ? !same(s.held, s.saved) : c.phase === "theirs" && (c.typed === true || !same(s.held, s.saved));
      if (another && keeps) {
        if (s.sent?.open === true) return { ...idle, saved: e.text, sent: { ...s.sent, later: { ...(e.revision !== undefined && { revision: e.revision }), ...(e.id !== undefined && { id: e.id }), text: e.text, aside: true } } };
        // A conflict keeps the newer of the revision it follows and the one this read names (#609).
        const revision = newest(e.revision, c?.revision);
        return { ...idle, saved: e.text, held: e.text, conflict: { ...(c === undefined ? { mine: s.held } : kept(c)), phase: "theirs", ...(revision !== undefined && { revision }) } };
      }
      // Once theirs shows, a read never ends a repeated Show's wait: only its draft read does. A newer
      // read of another author's text shows at once here, and a read held back is older than it: a
      // done Save must not show that one (ginsys/bronzeward#559 review 2).
      const shown = { ...idle, saved: e.text, held: same(s.held, s.saved) ? e.text : s.held };
      const open = s.sent;
      const later = open?.later;
      // The same text read again at a newer revision, while a Save is open, moves the read held
      // back, so a failed or done Save names that revision (#559 follow-up 3).
      if (open !== undefined && !another && later?.text !== undefined && e.revision !== undefined && e.revision > (later.revision ?? 0)) return { ...shown, sent: { ...open, later: { ...later, revision: e.revision, ...(e.id !== undefined && { id: e.id }) } } };
      // A newer accepted read moves the conflict that shows their text to its revision, also when the
      // Save that showed it is done, and also when its text is the one shown (#559 follow-up 2). The
      // pane's own revision is not theirs.
      const moved = c?.phase === "theirs" && e.revision !== undefined && e.revision > (c.revision ?? 0) && (e.id === undefined || e.id !== s.own) ? { ...shown, conflict: { ...c, revision: e.revision } } : shown;
      // While a write is open, a read of another author's text shows at once and is also held back for
      // that write's answer, with nothing typed to keep aside: a done write must not record an older text
      // as saved. A newer read replaces the older whole. Text typed after it is kept aside at done (#608, #613).
      if (!another || open?.open !== true) return moved;
      return { ...moved, sent: { ...open, later: { ...(e.revision !== undefined && { revision: e.revision }), ...(e.id !== undefined && { id: e.id }), text: e.text } } };
    }
    case "copy":
      // Only once their text shows and no read is pending: a pending one could replace the copied text.
      // Their removal stays followed: a newer one, read while the next Save is open, is told from it (#559).
      return c?.phase === "theirs" && c.awaits !== true && s.reading !== true ? { ...ended, held: c.mine, ...(same(s.saved, EMPTY) && c.revision !== undefined && { gone: c.revision }) } : s;
    case "send":
      return { ...s, sent: { text: s.held, whileTheirs: c?.phase === "theirs", open: true, ...(e.removes === true && { removes: true as const }) } };
    case "done": {
      const sent = s.sent?.text ?? s.held;
      // A Save's done: a read held back while it was open is another author's unless it is the
      // Save's own revision. With no id on the answer or the read, it cannot be told, and stays the
      // Save's own (ginsys/bronzeward#559). Their text or removal shows, the editor's text aside.
      const later = s.sent?.later;
      // A Remove creates no text revision, so a text read while it was open is never its own: it shows
      // with no id to compare (#608). A removal read meanwhile ends the entry removed either way.
      const removes = s.sent?.removes === true;
      const other = later === undefined ? undefined : later.removed === true ? (removes ? undefined : EMPTY)
        : removes || (later.id !== undefined && e.revision !== undefined && later.id !== e.revision) ? later.text : undefined;
      if (later !== undefined && other !== undefined) {
        const revision = newest(later.revision, c?.revision);
        const { own: _own, gone: _gone, conflict: _conflict, ...after } = s;
        // The operator's text goes aside when a removal shows, or when text was typed, before the read
        // (`aside`) or since it (the editor's text differs from the saved text). A Remove saved no text,
        // so a conflict that keeps the operator's text stays. Otherwise nothing of the operator's is lost:
        // the text read is the entry's, and the pane records it as saved (#613).
        const conflict: Clash | undefined = removes && c !== undefined ? { ...kept(c), phase: "theirs", ...(revision !== undefined && { revision }) }
          : later.removed === true || later.aside === true || !same(s.held, s.saved) ? { mine: s.held, phase: "theirs", ...(revision !== undefined && { revision }), ...(c?.awaits === true && { awaits: true as const }) } : undefined;
        return { ...after, ...closed(s), ...(e.revision !== undefined && { own: e.revision }), held: other, saved: other, writes: writes + 1, ...(conflict !== undefined && { conflict }) };
      }
      // The text the operator chose is saved: a Save sent while theirs showed, or the kept text itself.
      // A Remove saves no text, so the operator's kept text stays for Copy my text (N2).
      const ends = c === undefined || (s.sent?.removes !== true && (s.sent?.whileTheirs === true || same(c.mine, sent)));
      // A done Remove saves the removal: the text still shown is not stored anywhere (H8).
      // The removal revision `gone` follows ends with a done write: a new removal read names it again.
      const { own: _own, gone: _gone, ...after } = ends ? ended : s;
      return { ...after, ...closed(s), ...(e.revision !== undefined && { own: e.revision }), saved: s.sent?.removes === true ? EMPTY : sent, writes: writes + 1 };
    }
    case "revert":
      // Once their text shows, the reverted text is the operator's, as typed text is (#551 local review).
      if (!mayRevert(c) || e.at !== (s.version ?? 0)) return { ...s, note: NOT_REVERTED };
      return { ...s, held: e.text, note: e.note, ...(c !== undefined && { conflict: { ...c, typed: true as const } }) };
    case "note":
      return { ...s, note: e.text };
  }
}

// The last write's answer came: a later read is no longer possibly that write's own.
function closed(s: Editor): Pick<Editor, "sent"> {
  if (s.sent === undefined) return {};
  const { open: _open, later: _later, ...sent } = s.sent;
  return { sent };
}

// Revert to base replaces the editor's text: before their text shows, that is the operator's text
// the conflict keeps, so it waits until then.
export const mayRevert = (c: Clash | undefined) => c === undefined || c.phase === "theirs";
// Save and Remove wait the same way: once Show their change adopted the new ETag, either would
// overwrite or remove their change before it shows (ginsys/bronzeward#517 H5).
export const maySend = mayRevert;
export const NOT_SENT_CONFLICT = "Not sent: someone changed this draft. Show their change first; Save and Remove wait until their text shows.";

// Whether the pane still lacks the entry's text: the revision the draft shown names is not the
// one it read last. Until then the pane takes no edit and sends nothing, so a Save cannot replace
// text it never showed (ginsys/bronzeward#517 H9), also after a draft read named a newer revision.
// A Save of its own is no exception: after it, a Discard's Show their change can name another
// author's revision (review), so the pane reads even its own new revision before it edits again.
export const unread = (revision: string | null | undefined, loaded: string | undefined) => revision != null && revision !== loaded;
export const NOT_SENT_UNREAD = "Not sent: the entry's text is not read yet.";
// A fragment write settled and kept its ETag, but the draft read it started has not shown yet: the
// entries shown are older than that ETag, so a pane opened on them would overwrite the write unseen.
export const NOT_SENT_UNSHOWN = "Not sent: the draft is being read again after a save. Wait until it shows.";
export const READ_DRAFT_AGAIN = "Read the draft again";

// Why Save or Remove sends nothing now, if it does not send.
export const sendRefusal = (waits: boolean, conflict: Clash | undefined, shown: boolean) =>
  waits ? NOT_SENT_UNREAD : !maySend(conflict) ? NOT_SENT_CONFLICT : !shown ? NOT_SENT_UNSHOWN : undefined;

// The draft the page shows: the last read with data for this id, so a read again that fails keeps
// the editor and its unsaved text (W4); the failure shows above it.
export function lastGood<T>(last: { id: string; data: T } | undefined, id: string, item: Load<T>) {
  if (item.kind === "ok") return { id, data: item.data };
  return last?.id === id ? last : undefined;
}

// A reference's declaration: an own property only, so `constructor` is not Object's (ginsys/bronzeward#517 H17).
export const declaredOf = (declared: Declared, ref: string): Declared[string] | undefined => (Object.hasOwn(declared, ref) ? declared[ref] : undefined);

// The references table's rows: the document's references, then the declarations no reference
// uses, so an obsolete declaration can be undeclared (rule unused-declaration). The references of
// the embedded documents it declares count as the document's (ginsys/bronzeward#569).
export const rows = (document: string, declared: Declared, embedded: readonly EmbeddedPath[] = []) => {
  const refs = references(document, embedded);
  return [...refs, ...Object.keys(declared).filter((d) => !refs.includes(d))];
};

// The line of the marks field holding the n-th mark sent: Save leaves out blank lines.
export function markLine(marks: string, n: number): number | undefined {
  let seen = -1;
  const lines = marks.split("\n");
  for (let i = 0; i < lines.length; i++) if (lines[i] !== "" && ++seen === n) return i;
  return undefined;
}

// Go to field's spot for the n-th mark sent: that mark's text in the marks now shown, on the line
// the refusal names while it still holds it, so text typed since the Save moves it to no other
// mark (ginsys/bronzeward#517 H18). None once that mark is gone.
export function spotOfMark(sent: string, current: string, n: number): Spot | undefined {
  const line = markLine(sent, n);
  if (line === undefined) return undefined;
  const mark = sent.split("\n")[line]!;
  const lines = current.split("\n");
  const at = lines[line] === mark ? line : lines.indexOf(mark);
  return at < 0 ? undefined : spotOfLine(current, at);
}

// The range Go to field selects: the spot, or an empty range at the start.
export const selection = (spot: Spot | undefined): Spot => spot ?? { start: 0, end: 0 };

// Where a 422 points, as text beside the refusal: the reference, the mark's line, or the paths.
export function where(p: Problem | null, marks: string): string | undefined {
  if (p === null) return undefined;
  if (typeof p["reference"] === "string") return `Reference ${p["reference"]}.`;
  if (typeof p["position"] === "number") {
    const line = markLine(marks, p["position"]);
    return line === undefined ? `Mark ${p["position"] + 1}.` : `Mark on line ${line + 1}.`;
  }
  // A profile's or an assignment's refusal names one path; a fragment's, a list of them.
  const paths = Array.isArray(p["paths"]) ? p["paths"].filter((x): x is string => typeof x === "string") : typeof p["path"] === "string" ? [p["path"]] : [];
  return paths.length === 0 ? undefined : `At ${paths.join(", ")}.`;
}

// A write's line: 412 and 422 here, every other state as the forms show it.
export function WriteResult({ w, done, conflict, goTo, marks = "" }: { w: Write | undefined; done: (data: unknown) => ReactNode; conflict?: ReactNode; goTo?: () => void; marks?: string }) {
  const o: Outcome | undefined = w?.outcome;
  // A refusal whose answer was cut off (§8.4 item 8) has no problem to show, so it gets the status and
  // the cut-off text, as every other form shows it. A 412 still opens its conflict view below that line:
  // the editor treats every 412 as a conflict (it reads the draft again), and expects this view to show it.
  if (o?.kind === "refused" && o.cut === true) {
    return (
      <>
        <Result w={w} done={done} />
        {o.status === 412 && conflict}
      </>
    );
  }
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
export function FragmentPane({ draft, entry, seeChange, claim = () => {}, shown = true }: { draft: Draft; entry: Source | undefined; seeChange: () => void; claim?: (name: string) => void; shown?: boolean }) {
  const writes = useWrites();
  // Every read settles through the shell: a refusal of the credentials ends the page (H11).
  const read = useSettled();
  // A new fragment's typed name, until the pane edits the entry its Save created (H1).
  const [typed, setName] = useState("");
  const name = entry === undefined ? typed : nameOf(entry);
  // Show their change while their revision read is awaited reads it again: a failed read retries.
  const [attempt, retry] = useState(0);
  // The saved text is EMPTY for a new entry or a removal.
  // Once the last Save or Remove is done, the text it sent is saved, also for an entry new in the
  // draft, whose pane reads no revision.
  const [{ held, saved, conflict, sent, writes: done = 0, note, version = 0 }, act] = useReducer(editor, FRESH);
  const hold = (h: Held) => act({ kind: "type", held: h });
  const setNote = (text?: string) => act({ kind: "note", ...(text !== undefined && { text }) });
  const [w, send] = useSend();
  const text = useRef<HTMLTextAreaElement>(null);
  const marks = useRef<HTMLTextAreaElement>(null);
  // The revision the pane read last, and whether the latest read failed: until it read the revision
  // the draft shown names, the pane takes no edit (H9).
  const [loaded, setLoaded] = useState<string | undefined>();
  const [failed, setFailed] = useState(false);
  const waits = unread(entry?.revision, loaded);
  const open = draft.state === "open";
  const editable = open && !waits;
  // Guarded also once the draft no longer takes writes (discarded by another author): the text is
  // still shown, and leaving drops it.
  useUnsaved(`fragment ${name || "(new)"} of draft ${short(draft.id)}`, holdsUnsaved(held, saved, conflict?.mine));
  // The draft revision the page shows now, for a revision read that answers later.
  const drafted = useRef(draft.revision);
  // The done writes now, for a revision read that answers after one finished.
  const finished = useRef(done);
  finished.current = done;

  useEffect(() => {
    const revision = entry?.revision;
    if (revision == null) return;
    let live = true;
    setFailed(false);
    act({ kind: "reading", on: true });
    void read<Revision>(`/fragment-revisions/${enc(revision)}`, () => live).then((r) => {
      if (!live) return;
      // A failed read leaves the revision unread: Show their change still waits for it (review).
      // A reread of the revision already loaded ends here: `reading` goes off, or Copy my text stays ignored.
      if (r?.kind !== "ok") {
        if (loaded === revision) act({ kind: "reading", on: false });
        setFailed(true);
        return setNote("The entry's revision could not be read.");
      }
      // A write finished while this read was pending: the editor drops the answer of a read that
      // began before it, and it may name the revision from before the write, since the draft refresh
      // has not answered. It is not read again at once: it fails without a note, and the draft read
      // that names the new revision reads it again (ginsys/bronzeward#559 PR review).
      if (staleRead(finished.current, done)) {
        if (loaded === revision) act({ kind: "reading", on: false });
        return setFailed(true);
      }
      setLoaded(revision);
      // The draft revision shown when the read answers: one that changed only another entry while
      // it was pending still names this text, and Copy my text compares with it (PR review).
      act({ kind: "read", text: fromRevision(r.data), since: done, revision: drafted.current, id: revision, ...(loaded === revision && { reread: true as const }) });
    });
    return () => {
      live = false;
      act({ kind: "reading", on: false });
    };
  }, [read, entry?.revision, attempt]);

  // Each draft read the page shows, and whether it gives this entry a new revision to read, or
  // removes it. A draft read again also reads a revision that failed again.
  const readRevision = useRef(entry?.revision);
  useEffect(() => {
    const changed = entry?.revision != null && entry.revision !== readRevision.current;
    readRevision.current = entry?.revision;
    drafted.current = draft.revision;
    act({ kind: "draft", revision: draft.revision, changed, ...(entry?.revision === null && { removed: true as const }) });
    if (!changed && failed) retry((x) => x + 1);
  }, [draft]);

  const o = w?.outcome;
  useEffect(() => {
    // The settled hook reads the draft again (H3).
    const d = doneOnce(w);
    const created = (d?.data as Partial<S["SourceUpdate"]> | null | undefined)?.entry?.revision;
    if (d !== undefined) {
      // Counted at once: a revision read that answers before the next render must see this write
      // as finished, since the editor drops its answer (ginsys/bronzeward#559 hosted review).
      finished.current += 1;
      act({ kind: "done", ...(typeof created === "string" && { revision: created }) });
    }
    if (o?.kind === "refused" && o.status === 412) {
      act({ kind: "refused" });
      // For the revision shown only: this read's ETag is not kept (§8.3).
      void read<Draft>(`/drafts/${enc(draft.id)}`).then((r) => r?.kind === "ok" && act({ kind: "at", revision: r.data.revision }));
    } else if (o?.kind === "refused" || o?.kind === "not-done" || o?.kind === "set-aside") act({ kind: "failed" });
  }, [o]);
  // A dismissed unknown write leaves the registry with its outcome unchanged: no answer settles it
  // now, and a new Save may send, so it ends the open write too (PR review).
  const dismissed = o?.kind === "unknown" && writes?.open.includes(w!) !== true;
  useEffect(() => {
    if (dismissed) act({ kind: "failed" });
  }, [dismissed]);

  const path = `/drafts/${enc(draft.id)}/fragments/${enc(name)}`;
  const ifMatch = () => keptETag(draft.id, writes?.client?.currentEpoch);
  const settled = settledFragment(draft.id, () => writes?.client?.currentEpoch);
  const save = () => {
    const m = ifMatch();
    if (name === "") return setNote("Not sent: name the fragment first.");
    const refusal = sendRefusal(waits, conflict, shown);
    if (refusal !== undefined) return setNote(refusal);
    if (entry === undefined && inUse(draft, name)) return setNote(`Not sent: this draft already holds fragment ${name}. Open it from Entries to change it.`);
    if (m === undefined) return setNote(NO_ETAG);
    const lines = held.marks.split("\n").filter((l) => l !== "");
    const declarations = Object.keys(held.declared).length === 0 && held.embedded.length === 0 ? null : { references: held.declared, embedded: held.embedded };
    setNote(undefined);
    // A Save while the last write is still open sends nothing new, so its text is not the one sent.
    if (startsNew(writes!, w)) {
      act({ kind: "send" });
      if (entry === undefined) claim(name);
    }
    send({ method: "PUT", path, ifMatch: m, ingests: true, resource: `/drafts/${draft.id}`, body: { layer: held.layer, document: held.document, marks: lines.length === 0 ? null : lines, declarations }, settled });
  };
  const remove = () => {
    const m = ifMatch();
    const refusal = sendRefusal(waits, conflict, shown);
    if (refusal !== undefined) return setNote(refusal);
    if (m === undefined) return setNote(NO_ETAG);
    // Once done, the removal is saved: the text still shown is unsaved until a Save stores it (H8).
    if (startsNew(writes!, w)) act({ kind: "send", removes: true });
    send({ method: "DELETE", path, ifMatch: m, resource: `/drafts/${draft.id}`, body: undefined, settled });
  };
  // The reducer applies the base text only if nothing changed the text or the conflict since the
  // click (sweep).
  const revert = async () => {
    const at = version;
    if (!mayRevert(conflict)) return setNote(NOT_REVERTED);
    if (entry?.head == null) return setNote("No base: this fragment is new in the draft.");
    const f = await read<Fragment>(`/fragments/${enc(entry.head)}`);
    if (f?.kind !== "ok") return setNote("The fragment's head could not be read.");
    if (f.data.revision === null) return setNote("No base text: the fragment's head is removed.");
    if (f.data.headRevision !== entry.base) return setNote(`No base text to restore: the head moved since this entry's base (revision ${entry.base ?? "none"}, now ${f.data.headRevision}).`);
    const r = await read<Revision>(`/fragment-revisions/${enc(f.data.revision)}`);
    if (r?.kind !== "ok") return setNote("The base revision could not be read.");
    act({ kind: "revert", at, text: fromRevision(r.data), note: `The base text (revision ${short(f.data.revision)}) is in the editor, not saved. Save writes it as a new revision of this entry; the entry stays in the draft.` });
  };
  const goTo = async () => {
    const p = o?.kind === "refused" ? o.problem : null;
    if (p === null) return;
    let spot: Spot | undefined;
    let into = text.current;
    if (typeof p["reference"] === "string") spot = spotOfReference(held.document, p["reference"], held.embedded);
    else if (typeof p["position"] === "number") {
      into = marks.current;
      spot = spotOfMark(sent?.text.marks ?? held.marks, held.marks, p["position"]);
    } else if (Array.isArray(p["paths"]) && typeof p["paths"][0] === "string") spot = await spotOfPath(held.document, p["paths"][0]);
    if (into === null) return;
    into.focus();
    // No spot selects nothing: an earlier selection would point at an unrelated field (W4).
    const at = selection(spot);
    into.setSelectionRange(at.start, at.end);
  };
  const conflictView = conflict !== undefined && (
    <>
      <Conflict what="draft" revision={conflict.revision ?? draft.revision} onShow={() => {
        if (conflict.awaits === true) retry((x) => x + 1);
        act({ kind: "show" });
        seeChange();
      }} />
      {entry !== undefined && conflict.revision === draft.revision && (
        <div>
          <label>Their text <textarea readOnly value={entry.document ?? "(removed)"} rows={8} cols={60} {...NO_ASSIST} /></label>
          <label>Your text <textarea readOnly value={conflict.mine.document} rows={8} cols={60} {...NO_ASSIST} /></label>
        </div>
      )}
      <button type="button" onClick={() => act({ kind: "copy" })} disabled={conflict.phase !== "theirs" || conflict.awaits === true || waits || conflict.revision !== draft.revision}>
        Copy my text into the new revision
      </button>
    </>
  );
  const refs = rows(held.document, held.declared, held.embedded);
  const used = references(held.document, held.embedded);
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
          <select value={held.layer} onChange={(e) => hold({ ...held, layer: e.target.value })} disabled={!editable}>
            {LAYERS.map((l) => <option key={l}>{l}</option>)}
          </select>
        </label>{" "}
        {entry === undefined ? "new" : change(entry)}{!same(held, saved) && " -> unsaved"}
      </p>
      {entry?.revision === null && <Status>Removed in this draft.</Status>}
      {waits && (failed
        ? <p><button type="button" onClick={() => retry((x) => x + 1)}>Read the entry again</button></p>
        : <Status>Reading the entry's text.</Status>)}
      <p><label>Document <textarea ref={text} value={held.document} onChange={(e) => hold({ ...held, document: e.target.value })} readOnly={!editable} rows={16} cols={80} {...NO_ASSIST} /></label></p>
      <p><label>Marks, one path per line <textarea ref={marks} value={held.marks} onChange={(e) => hold({ ...held, marks: e.target.value })} readOnly={!editable} rows={3} cols={80} {...NO_ASSIST} /></label></p>
      <h4>References in this document</h4>
      {refs.length === 0 ? <p>None.</p> : (
        <Scroll label="References in this document">
          <thead><tr>{["Reference", "Kind", "Version", "Encoding", "State"].map((h) => <th key={h} scope="col">{h}</th>)}</tr></thead>
          <tbody>{refs.map((ref) => <ReferenceRow key={ref} name={ref} declared={declaredOf(held.declared, ref)} used={used.includes(ref)} set={(r) => declare(ref, r)} open={editable} />)}</tbody>
        </Scroll>
      )}
      <EmbeddedList embedded={held.embedded} set={(embedded) => hold({ ...held, embedded })} open={editable} />
      {open && (
        <p>
          <Action label="Save" needs={["author"]} onClick={save} busy={w !== undefined && w.outcome === undefined} />{" "}
          <Action label="Revert to base" needs={["author"]} onClick={() => void revert()} />{" "}
          {entry !== undefined && entry.revision !== null && <Action label="Remove" needs={["author"]} onClick={remove} />}
        </p>
      )}
      {note !== undefined && <Status testid="editor-note">{note}</Status>}
      {/* A conflict a done write kept stays shown, with its Copy my text. */}
      {!(o?.kind === "refused" && o.status === 412) && conflictView}
      <WriteResult w={w} conflict={conflictView || undefined} goTo={() => void goTo()} marks={sent?.text.marks ?? ""} done={(data) => {
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
