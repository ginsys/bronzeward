// The profile entry of the draft editor (ginsys/bronzeward#339; journeys §5.4; W4): an ordered list
// of the fragment revisions a profile pins, moved by keyboard. A profile lists revision ids, not
// names, so a later save of a fragment does not change it. The list lives in the fragment editor's
// `Held` as one revision id per line, so `editor` gives it the same conflict rules (B14), unsaved
// guard and unknown-outcome handling as a fragment's text, unchanged.

import { useEffect, useReducer, useRef, useState } from "react";
import type { components } from "./api/v1";
import { change, doneOnce, editor, EMPTY, type Held, holdsUnsaved, inUse, keptETag, NO_ETAG, sendRefusal, settledFragment, unread, WriteResult } from "./drafts.tsx";
import { startsNew, useSend, useUnsaved, useWrites } from "./forms.tsx";
import { short, useList, usePending, useSettled } from "./list.tsx";
import { Action } from "./shell.tsx";
import { Conflict, Status } from "./states.tsx";

type S = components["schemas"];
type Draft = S["DraftBody"];
type Source = S["SourceEntry"];
type Fragment = S["FragmentBody"];

export const PINS_MAX = 256;
export const PINS_STATEMENT = "A profile pins these fragment revisions. A later save of a fragment does not change the pin.";
export const NOT_CURRENT = "pin is not current";

// The pins are the editor's text: one revision id per line, and an empty list is no text.
export const pinsOf = (h: Held): string[] => (h.document === "" ? [] : h.document.split("\n"));
export const pinned = (base: Held, pins: string[]): Held => ({ ...base, document: pins.join("\n") });

// The list after a move of row `i` by one place; the list itself when the move leaves the list.
export function move(pins: string[], i: number, by: -1 | 1): string[] {
  const j = i + by;
  if (i < 0 || i >= pins.length || j < 0 || j >= pins.length) return pins;
  const next = [...pins];
  [next[i], next[j]] = [next[j]!, next[i]!];
  return next;
}
// A revision pinned twice would only repeat itself: adding it again changes nothing.
export const withPin = (pins: string[], revision: string) => (pins.includes(revision) ? pins : [...pins, revision]);
export const withoutPin = (pins: string[], i: number) => pins.filter((_, j) => j !== i);

export const pinRefusal = (pins: string[]) =>
  pins.length === 0 ? "Not sent: a profile pins at least one fragment revision. Remove the entry to drop the profile."
    : pins.length > PINS_MAX ? `Not sent: a profile pins at most ${PINS_MAX} fragment revisions.` : undefined;
export const profileBody = (pins: string[]) => ({ fragments: pins });

// A revision a pin can name: the draft's own revision of a fragment, or for a fragment the draft
// holds no entry for, the fragment's head revision (api: updateProfile). A fragment the draft removes
// has none, and a fragment of another cluster is not offered.
export interface Choice { name: string; revision: string; from: "draft" | "base" }
export function choices(draft: { cluster: string; entries: Draft["entries"] }, fragments: Fragment[]): Choice[] {
  const held = draft.entries.filter((e): e is Source => e.kind === "fragment");
  const names = new Set(held.map((e) => e.name));
  const own = held.flatMap((e) => (e.revision == null || e.name === undefined ? [] : [{ name: e.name, revision: e.revision, from: "draft" as const }]));
  const base = fragments.flatMap((f) => (f.cluster !== draft.cluster || f.revision === null || names.has(f.name) ? [] : [{ name: f.name, revision: f.revision, from: "base" as const }]));
  return [...own, ...base].sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0));
}
export const isCurrent = (pin: string, options: Choice[]) => options.some((c) => c.revision === pin);
// The picker's selection, or none once a draft read took that revision away.
export const stillOffered = (chosen: string, options: Choice[]) => (isCurrent(chosen, options) ? chosen : "");

// The profile entry (W4): name, the pins in order with Move up, Move down and Remove, and an Add
// control. Save is PUT, Remove DELETE.
export function ProfilePane({ draft, entry, seeChange, claim = () => {}, shown = true }: { draft: Draft; entry: Source | undefined; seeChange: () => void; claim?: (name: string) => void; shown?: boolean }) {
  const writes = useWrites();
  const read = useSettled();
  const [typed, setName] = useState("");
  const name = entry === undefined ? typed : (entry.name ?? "");
  const [attempt, retry] = useState(0);
  const [{ held, saved, conflict, writes: done = 0, note }, act] = useReducer(editor, { held: EMPTY, saved: EMPTY });
  const hold = (pins: string[]) => act({ kind: "type", held: pinned(held, pins) });
  const setNote = (text?: string) => act({ kind: "note", ...(text !== undefined && { text }) });
  const [w, send] = useSend();
  const [loaded, setLoaded] = useState<string | undefined>();
  const [failed, setFailed] = useState(false);
  const waits = unread(entry?.revision, loaded);
  const open = draft.state === "open";
  const editable = open && !waits;
  const unsavedList = holdsUnsaved(held, saved, conflict?.mine);
  useUnsaved(`profile ${name || "(new)"} of draft ${short(draft.id)}`, unsavedList);
  const drafted = useRef(draft.revision);

  // The pins the draft can name, and the names of the pins it cannot.
  const [fragments] = useList<Fragment>("/fragments", "", true);
  const listed = fragments.kind === "ok";
  const options = choices(draft, listed ? fragments.data.items : []);
  const [names, setNames] = useState<Record<string, string>>({});
  const pins = pinsOf(held);
  const known = new Map(options.map((c) => [c.revision, c.name]));
  const label = (revision: string) => known.get(revision) ?? (names[revision] || short(revision));
  const lookup = pins.filter((r) => !known.has(r) && names[r] === undefined).join(",");
  useEffect(() => {
    if (lookup === "") return;
    let live = true;
    void (async () => {
      for (const r of lookup.split(",")) {
        const x = await read<S["FragmentRevisionBody"]>(`/fragment-revisions/${encodeURIComponent(r)}`, () => live);
        if (!live) return;
        setNames((n) => ({ ...n, [r]: x?.kind === "ok" ? x.data.name : "" }));
      }
    })();
    return () => void (live = false);
  }, [read, lookup]);

  useEffect(() => {
    const revision = entry?.revision;
    if (revision == null) return;
    let live = true;
    setFailed(false);
    act({ kind: "reading", on: true });
    void read<S["ProfileRevisionBody"]>(`/profile-revisions/${encodeURIComponent(revision)}`, () => live).then((r) => {
      if (!live) return;
      if (r?.kind !== "ok") {
        setFailed(true);
        return setNote("The entry's revision could not be read.");
      }
      setLoaded(revision);
      act({ kind: "read", text: pinned(EMPTY, r.data.fragments), since: done, revision: drafted.current, id: revision });
    });
    return () => {
      live = false;
      act({ kind: "reading", on: false });
    };
  }, [read, entry?.revision, attempt]);

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
    const d = doneOnce(w);
    const created = (d?.data as Partial<S["SourceUpdate"]> | null | undefined)?.entry?.revision;
    if (d !== undefined) act({ kind: "done", ...(typeof created === "string" && { revision: created }) });
    if (o?.kind === "refused" && o.status === 412) {
      act({ kind: "refused" });
      void read<Draft>(`/drafts/${encodeURIComponent(draft.id)}`).then((r) => r?.kind === "ok" && act({ kind: "at", revision: r.data.revision }));
    } else if (o?.kind === "refused" || o?.kind === "not-done" || o?.kind === "set-aside") act({ kind: "failed" });
  }, [o]);
  const dismissed = o?.kind === "unknown" && writes?.open.includes(w!) !== true;
  useEffect(() => {
    if (dismissed) act({ kind: "failed" });
  }, [dismissed]);

  const path = `/drafts/${encodeURIComponent(draft.id)}/profiles/${encodeURIComponent(name)}`;
  const ifMatch = () => keptETag(draft.id, writes?.client?.currentEpoch);
  const settled = settledFragment(draft.id, () => writes?.client?.currentEpoch);
  const save = () => {
    const m = ifMatch();
    if (name === "") return setNote("Not sent: name the profile first.");
    const refusal = sendRefusal(waits, conflict, shown);
    if (refusal !== undefined) return setNote(refusal);
    if (entry === undefined && inUse(draft, name, "profile")) return setNote(`Not sent: this draft already holds profile ${name}. Open it from Entries to change it.`);
    const bad = pinRefusal(pins);
    if (bad !== undefined) return setNote(bad);
    if (m === undefined) return setNote(NO_ETAG);
    setNote(undefined);
    if (startsNew(writes!, w)) {
      act({ kind: "send" });
      if (entry === undefined) claim(name);
    }
    send({ method: "PUT", path, ifMatch: m, resource: `/drafts/${draft.id}`, body: profileBody(pins), settled });
  };
  const remove = () => {
    const m = ifMatch();
    const refusal = sendRefusal(waits, conflict, shown);
    if (refusal !== undefined) return setNote(refusal);
    if (m === undefined) return setNote(NO_ETAG);
    if (startsNew(writes!, w)) act({ kind: "send", removes: true });
    send({ method: "DELETE", path, ifMatch: m, resource: `/drafts/${draft.id}`, body: undefined, settled });
  };

  // After a move the focus stays on the moved row's button, or on the other one at the end of the list.
  const buttons = useRef(new Map<string, HTMLButtonElement>());
  const focus = useRef<{ revision: string; by: -1 | 1 }>(undefined);
  useEffect(() => {
    const f = focus.current;
    if (f === undefined) return;
    focus.current = undefined;
    const mine = buttons.current.get(`${f.revision}:${f.by}`);
    (mine !== undefined && !mine.disabled ? mine : buttons.current.get(`${f.revision}:${-f.by}`))?.focus();
  });
  const shift = (i: number, by: -1 | 1) => {
    focus.current = { revision: pins[i]!, by };
    hold(move(pins, i, by));
  };

  const [chosen, setPick] = useState("");
  const free = options.filter((c) => !pins.includes(c.revision));
  // A revision the list no longer offers (a draft read replaced it) is not chosen any more.
  const pick = stillOffered(chosen, free);
  const add = () => {
    if (pick === "") return;
    hold(withPin(pins, pick));
    setPick("");
  };
  const conflictView = conflict !== undefined && (
    <>
      <Conflict what="draft" revision={conflict.revision ?? draft.revision} onShow={() => {
        if (conflict.awaits === true) retry((x) => x + 1);
        act({ kind: "show" });
        seeChange();
      }} />
      {conflict.revision === draft.revision && (
        <div>
          <h4>Your list</h4>
          <ol aria-label="Your pins">{pinsOf(conflict.mine).map((r) => <li key={r}>{label(r)} <code>{short(r)}</code></li>)}</ol>
        </div>
      )}
      <button type="button" onClick={() => act({ kind: "copy" })} disabled={conflict.phase !== "theirs" || conflict.awaits === true || waits || conflict.revision !== draft.revision}>
        Copy my text into the new revision
      </button>
    </>
  );
  const pending = usePending(fragments, "fragments");
  return (
    <section aria-label={`Profile ${name || "(new)"}`}>
      <h3>Profile {entry === undefined ? <label>name <input value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" required /></label> : name}</h3>
      <p>{entry === undefined ? "new" : change(entry)}{unsavedList && " -> unsaved"}</p>
      <p>{PINS_STATEMENT}</p>
      {entry?.revision === null && <Status>Removed in this draft.</Status>}
      {waits && (failed
        ? <p><button type="button" onClick={() => retry((x) => x + 1)}>Read the entry again</button></p>
        : <Status>Reading the entry's text.</Status>)}
      <h4>Pinned fragment revisions, in order</h4>
      {pins.length === 0 ? <p>None.</p> : (
        <ol aria-label="Pinned fragment revisions">
          {pins.map((r, i) => (
            <li key={r}>
              {label(r)} <code>{short(r)}</code>{listed && !isCurrent(r, options) && ` ${NOT_CURRENT}`}{" "}
              <button type="button" ref={(b) => void (b === null ? buttons.current.delete(`${r}:-1`) : buttons.current.set(`${r}:-1`, b))} aria-label={`Move up ${label(r)}`} disabled={!editable || i === 0} onClick={() => shift(i, -1)}>Move up</button>{" "}
              <button type="button" ref={(b) => void (b === null ? buttons.current.delete(`${r}:1`) : buttons.current.set(`${r}:1`, b))} aria-label={`Move down ${label(r)}`} disabled={!editable || i === pins.length - 1} onClick={() => shift(i, 1)}>Move down</button>{" "}
              <button type="button" aria-label={`Remove ${label(r)} from the list`} disabled={!editable} onClick={() => hold(withoutPin(pins, i))}>Remove from list</button>
            </li>
          ))}
        </ol>
      )}
      {open && (
        <p>
          <label>Fragment:{" "}
            <select value={pick} onChange={(e) => setPick(e.target.value)} disabled={!editable}>
              <option value="">choose</option>
              {free.map((c) => <option key={c.revision} value={c.revision}>{c.name} ({c.from === "draft" ? "this draft" : "base"}, {short(c.revision)})</option>)}
            </select>
          </label>{" "}
          <button type="button" onClick={add} disabled={!editable || pick === ""}>Add to profile</button>
        </p>
      )}
      {open && pending}
      {open && (
        <p>
          <Action label="Save" needs={["author"]} onClick={save} busy={w !== undefined && w.outcome === undefined} />{" "}
          {entry !== undefined && entry.revision !== null && <Action label="Remove" needs={["author"]} onClick={remove} />}
        </p>
      )}
      {note !== undefined && <Status testid="editor-note">{note}</Status>}
      {!(o?.kind === "refused" && o.status === 412) && conflictView}
      <WriteResult w={w} conflict={conflictView || undefined} done={(data) => {
        if (w?.spec.method === "DELETE") return `Profile ${name} removed from draft ${short(draft.id)}.`;
        const r = (data as Partial<S["SourceUpdate"]>).entry?.revision;
        return `Profile ${name} saved in draft ${short(draft.id)}${typeof r === "string" ? ` (revision ${short(r)})` : ""}.`;
      }} />
    </section>
  );
}
