// The profile entry of the draft editor (ginsys/bronzeward#339; journeys §5.4; W4): an ordered list
// of the fragment revisions a profile pins, moved by keyboard. A profile lists revision ids, not
// names, so a later save of a fragment does not change it. The list lives in the fragment editor's
// `Held` as one revision id per line, so `editor` gives it the same conflict rules (B14), unsaved
// guard and unknown-outcome handling as a fragment's text, unchanged.
//
// The assignment entry (also #339) works the same way: its list lives in the editor's text as one
// `<slot> <name>` line per member, where the slot is `profile` or one of the six layers.

import { useEffect, useReducer, useRef, useState } from "react";
import type { components } from "./api/v1";
import { change, declaredOf, doneOnce, editor, EMPTY, type Held, holdsUnsaved, inUse, keptETag, NO_ETAG, rows, sendRefusal, settledFragment, unread, WriteResult } from "./drafts.tsx";
import { startsNew, useSend, useUnsaved, useWrites } from "./forms.tsx";
import { LAYERS } from "./layers.ts";
import { short, useList, usePending, useSettled } from "./list.tsx";
import { references } from "./position.ts";
import { Action } from "./shell.tsx";
import { Conflict, Status } from "./states.tsx";

type S = components["schemas"];
type Draft = S["DraftBody"];
type Source = S["SourceEntry"];
type Fragment = S["FragmentBody"];
type Profile = S["ProfileBody"];
type Machine = S["MachineBody"];
type Revision = S["FragmentRevisionBody"];
type Declared = S["FragmentDeclarations"]["references"];
const enc = encodeURIComponent;

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

// The pins whose name a read must still give. Nothing is asked while the cluster's fragment list
// pages: before it ends, every base pin looks unknown (ginsys/bronzeward#563).
export const unknownPins = (pins: string[], known: Map<string, string>, names: Record<string, string>, loading: boolean) =>
  loading ? [] : pins.filter((r) => !known.has(r) && names[r] === undefined);

// Whether the view is still shown: an answer that arrives later sets nothing.
function useMounted() {
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => void (mounted.current = false);
  }, []);
  return mounted;
}
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
  const lookup = unknownPins(pins, known, names, fragments.kind === "loading").join(",");
  // Each uncached pin is asked for once, on its own: an answer changes `lookup` and restarts this
  // effect, which must not ask again for a pin whose read is still in flight.
  const asked = useRef(new Set<string>());
  const mounted = useMounted();
  useEffect(() => {
    for (const r of lookup === "" ? [] : lookup.split(",")) {
      if (asked.current.has(r)) continue;
      asked.current.add(r);
      void read<S["FragmentRevisionBody"]>(`/fragment-revisions/${encodeURIComponent(r)}`, () => mounted.current).then((x) => {
        if (mounted.current) setNames((n) => ({ ...n, [r]: x?.kind === "ok" ? x.data.name : "" }));
      });
    }
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

// ---- The assignment entry (W4) ----

export const ASSIGNMENT_MAX = 256;
export const ASSIGNMENT_STATEMENT = "An assignment selects profiles and fragments of a machine by name. A later save of one of them changes what the machine composes.";
export const NOT_OFFERED = "not available in this draft";
export const SLOTS = ["profile", ...LAYERS] as const;
export type Slot = (typeof SLOTS)[number];
export type Layer = (typeof LAYERS)[number];
export type Lists = Record<Slot, string[]>;
export const noLists = (): Lists => ({ profile: [], global: [], site: [], cluster: [], role: [], workload: [], override: [] });
const isLayer = (x: string): x is Layer => (LAYERS as readonly string[]).includes(x);

// The text the editor holds: one `<slot> <name>` line per member, profiles first, then the layers in
// composition order. No member is no text.
export function listsOf(h: Held): Lists {
  const lists = noLists();
  for (const line of h.document === "" ? [] : h.document.split("\n")) {
    const i = line.indexOf(" ");
    const slot = line.slice(0, i) as Slot;
    if (i > 0 && SLOTS.includes(slot)) lists[slot].push(line.slice(i + 1));
  }
  return lists;
}
export const withLists = (base: Held, lists: Lists): Held => ({ ...base, document: SLOTS.flatMap((s) => lists[s].map((n) => `${s} ${n}`)).join("\n") });
export function fromRevision(r: Pick<S["AssignmentRevisionBody"], "profiles" | "fragments">): Lists {
  const lists = noLists();
  lists.profile = [...r.profiles];
  for (const l of LAYERS) lists[l] = Object.hasOwn(r.fragments, l) ? [...r.fragments[l]!] : [];
  return lists;
}

const total = (l: Lists) => SLOTS.reduce((n, s) => n + l[s].length, 0);
// An empty layer is left out: a layer that is sent lists at least one fragment.
export const assignmentBody = (l: Lists) => ({
  profiles: l.profile,
  fragments: Object.fromEntries(LAYERS.filter((x) => l[x].length > 0).map((x) => [x, l[x]])),
});

// Why Save sends nothing for this list, if it does not (api: updateAssignment).
export function assignmentRefusal(l: Lists): string | undefined {
  const n = total(l);
  if (n === 0) return "Not sent: an assignment selects at least one profile or fragment. Remove the entry to drop the assignment.";
  if (n > ASSIGNMENT_MAX) return `Not sent: an assignment selects at most ${ASSIGNMENT_MAX} profiles and fragments in all.`;
  const twice = (names: string[]) => names.find((x, i) => names.indexOf(x) !== i);
  const p = twice(l.profile);
  if (p !== undefined) return `Not sent: profile ${p} is selected twice.`;
  const f = twice(LAYERS.flatMap((x) => l[x]));
  return f === undefined ? undefined : `Not sent: fragment ${f} is selected twice.`;
}

// The profiles an assignment can name: the draft's own, and the cluster's with a head revision. A
// profile the draft removes is not offered.
export function profileOffers(draft: Pick<Draft, "cluster" | "entries">, profiles: Profile[]): string[] {
  const held = draft.entries.filter((e): e is Source => e.kind === "profile");
  const names = new Set(held.map((e) => e.name));
  const own = held.flatMap((e) => (e.revision == null || e.name === undefined ? [] : [e.name]));
  const base = profiles.flatMap((p) => (p.cluster !== draft.cluster || p.revision === null || names.has(p.name) ? [] : [p.name]));
  return [...own, ...base].sort();
}

// The fragments a layer can name: only fragments of that layer. A fragment the draft holds has the
// layer of its revision, `layers` by revision id; one whose layer is not read yet is left out.
export function fragmentOffers(draft: Pick<Draft, "cluster" | "entries">, fragments: Fragment[], layers: Record<string, string>): Record<Layer, string[]> {
  const out: Record<Layer, string[]> = { global: [], site: [], cluster: [], role: [], workload: [], override: [] };
  const held = draft.entries.filter((e): e is Source => e.kind === "fragment");
  const names = new Set(held.map((e) => e.name));
  for (const e of held) {
    const layer = e.revision == null || !Object.hasOwn(layers, e.revision) ? "" : layers[e.revision]!;
    if (e.name !== undefined && isLayer(layer)) out[layer].push(e.name);
  }
  for (const f of fragments) if (f.cluster === draft.cluster && f.revision !== null && !names.has(f.name) && isLayer(f.layer)) out[f.layer].push(f.name);
  for (const l of LAYERS) out[l].sort();
  return out;
}

// The machines of the draft's cluster that the draft holds no assignment for.
export const machineOffers = (draft: Pick<Draft, "cluster" | "entries">, machines: Machine[]) =>
  machines.filter((m) => m.cluster === draft.cluster && !inUse(draft, m.id, "assignment"));

// Composition order (operator-experience §5.4): layers from global to override; inside a layer the
// profile pins first, in profile order, then the assignment's own fragments.
export interface Composed { layer: Layer; name: string; revision: string; via: string | undefined }
// A revision is composed once, at its first position, as the server composes it.
export function compose(profiles: { name: string; pins: { name: string; revision: string; layer: string }[] }[], own: { layer: Layer; name: string; revision: string }[]): Composed[] {
  const seen = new Set<string>();
  return LAYERS.flatMap((layer) => [
    ...profiles.flatMap((p) => p.pins.filter((x) => x.layer === layer).map((x) => ({ layer, name: x.name, revision: x.revision, via: p.name }))),
    ...own.filter((x) => x.layer === layer).map((x) => ({ layer, name: x.name, revision: x.revision, via: undefined })),
  ]).filter((c) => !seen.has(c.revision) && seen.add(c.revision));
}

// The draft's own revision of a name, else the head of the cluster's: what a composition reads.
export function revisionOf(draft: Pick<Draft, "cluster" | "entries">, profiles: Profile[], fragments: Fragment[], kind: "profile" | "fragment", name: string) {
  const own = draft.entries.find((e): e is Source => e.kind === kind && e.name === name);
  return own !== undefined ? own.revision : (kind === "profile" ? profiles : fragments).find((x) => x.cluster === draft.cluster && x.name === name)?.revision;
}
// What a composition read depends on: the lists and the revisions their names resolve to now. A
// result belongs to the same key, so a change of either one makes it stale.
export const sourceKey = (draft: Pick<Draft, "cluster" | "entries">, lists: Lists, profiles: Profile[], fragments: Fragment[]) =>
  [...lists.profile.map((n) => `profile ${n} ${revisionOf(draft, profiles, fragments, "profile", n) ?? "-"}`),
    ...LAYERS.flatMap((l) => lists[l].map((n) => `${l} ${n} ${revisionOf(draft, profiles, fragments, "fragment", n) ?? "-"}`))].join("\n");

// What a fragment revision does with each reference: its name, its declaration, and the state.
// Never a value, and never the document.
export interface RefState { name: string; declared: Declared[string] | undefined; state: "declared" | "carried, not declared" | "declared, not in the document" }
export function referenceStates(document: string, declared: Declared): RefState[] {
  const used = references(document);
  return rows(document, declared).map((name) => {
    const d = declaredOf(declared, name);
    return { name, declared: d, state: d === undefined ? "carried, not declared" : used.includes(name) ? "declared" : "declared, not in the document" };
  });
}

export interface Composition { order: Composed[]; revisions: Record<string, Revision> }
// Reads what the order needs, one read at a time: the revision of each profile, the fragment
// revisions it pins, and the revision of each fragment the assignment selects. The text of a failure
// when a read gives none.
export async function readComposition(
  draft: Pick<Draft, "cluster" | "entries">, lists: Lists, profiles: Profile[], fragments: Fragment[],
  get: <T>(path: string) => Promise<T | undefined>,
): Promise<Composition | string> {
  const revisions: Record<string, Revision> = {};
  const fragment = async (id: string) => {
    if (!Object.hasOwn(revisions, id)) {
      const r = await get<Revision>(`/fragment-revisions/${enc(id)}`);
      if (r === undefined) return undefined;
      revisions[id] = r;
    }
    return revisions[id];
  };
  const used: Parameters<typeof compose>[0] = [];
  for (const name of lists.profile) {
    const id = revisionOf(draft, profiles, fragments, "profile", name);
    if (id == null) return `Profile ${name} has no revision to read.`;
    const p = await get<S["ProfileRevisionBody"]>(`/profile-revisions/${enc(id)}`);
    if (p === undefined) return `The revision of profile ${name} could not be read.`;
    const pins: Parameters<typeof compose>[0][number]["pins"] = [];
    for (const r of p.fragments) {
      const f = await fragment(r);
      if (f === undefined) return `A fragment revision that profile ${name} pins could not be read.`;
      pins.push({ name: f.name, revision: f.id, layer: f.layer });
    }
    used.push({ name, pins });
  }
  const own: Parameters<typeof compose>[1] = [];
  for (const layer of LAYERS) {
    for (const name of lists[layer]) {
      const id = revisionOf(draft, profiles, fragments, "fragment", name);
      if (id == null) return `Fragment ${name} has no revision to read.`;
      const f = await fragment(id);
      if (f === undefined) return `The revision of fragment ${name} could not be read.`;
      if (f.layer !== layer) return `Fragment ${name} is in layer ${f.layer}, not ${layer}. Remove it from the list and add it again.`;
      own.push({ layer, name, revision: f.id });
    }
  }
  return { order: compose(used, own), revisions };
}

// One section of the list (the profiles, or one layer): rows with Move up, Move down and Remove, and
// a picker for what the section can still take. After a move the focus stays on the moved row's
// button, or on the other one at the end of the list.
function Members({ slot, names, offers, ready, open, editable, change: set }: { slot: Slot; names: string[]; offers: string[]; ready: boolean; open: boolean; editable: boolean; change: (next: string[]) => void }) {
  const title = slot === "profile" ? "Profiles" : `Layer ${slot}`;
  const buttons = useRef(new Map<string, HTMLButtonElement>());
  const focus = useRef<{ name: string; by: -1 | 1 }>(undefined);
  useEffect(() => {
    const f = focus.current;
    if (f === undefined) return;
    focus.current = undefined;
    const mine = buttons.current.get(`${f.name}:${f.by}`);
    (mine !== undefined && !mine.disabled ? mine : buttons.current.get(`${f.name}:${-f.by}`))?.focus();
  });
  const [chosen, setPick] = useState("");
  const free = offers.filter((n) => !names.includes(n));
  const pick = free.includes(chosen) ? chosen : "";
  const ref = (n: string, by: -1 | 1) => (b: HTMLButtonElement | null) => void (b === null ? buttons.current.delete(`${n}:${by}`) : buttons.current.set(`${n}:${by}`, b));
  return (
    <>
      <h4>{title}, in order</h4>
      {names.length === 0 ? <p>None.</p> : (
        <ol aria-label={title}>
          {names.map((n, i) => (
            <li key={n}>
              {n}{ready && !offers.includes(n) && ` ${NOT_OFFERED}`}{" "}
              <button type="button" ref={ref(n, -1)} aria-label={`Move up ${slot} ${n}`} disabled={!editable || i === 0} onClick={() => { focus.current = { name: n, by: -1 }; set(move(names, i, -1)); }}>Move up</button>{" "}
              <button type="button" ref={ref(n, 1)} aria-label={`Move down ${slot} ${n}`} disabled={!editable || i === names.length - 1} onClick={() => { focus.current = { name: n, by: 1 }; set(move(names, i, 1)); }}>Move down</button>{" "}
              <button type="button" aria-label={`Remove ${slot} ${n} from the list`} disabled={!editable} onClick={() => set(withoutPin(names, i))}>Remove from list</button>
            </li>
          ))}
        </ol>
      )}
      {open && (
        <p>
          <label>{slot === "profile" ? "Profile:" : "Fragment:"}{" "}
            <select aria-label={`Add to ${title}`} value={pick} onChange={(e) => setPick(e.target.value)} disabled={!editable}>
              <option value="">choose</option>
              {free.map((n) => <option key={n} value={n}>{n}</option>)}
            </select>
          </label>{" "}
          <button type="button" disabled={!editable || pick === ""} onClick={() => { set(withPin(names, pick)); setPick(""); }}>Add to {slot === "profile" ? "profiles" : `layer ${slot}`}</button>
        </p>
      )}
    </>
  );
}

// The assignment entry (W4): the machine, the profiles in order, the six layers' fragments in order,
// and on request the composition order and the references. Save is PUT, Remove DELETE.
export function AssignmentPane({ draft, entry, seeChange, claim = () => {}, shown = true }: { draft: Draft; entry: Source | undefined; seeChange: () => void; claim?: (name: string) => void; shown?: boolean }) {
  const writes = useWrites();
  const read = useSettled();
  const mounted = useMounted();
  const [typed, setMachine] = useState("");
  const machine = entry === undefined ? typed : (entry.machine ?? "");
  const [attempt, retry] = useState(0);
  const [{ held, saved, conflict, writes: done = 0, note }, act] = useReducer(editor, { held: EMPTY, saved: EMPTY });
  const lists = listsOf(held);
  const hold = (next: Lists) => act({ kind: "type", held: withLists(held, next) });
  const setNote = (text?: string) => act({ kind: "note", ...(text !== undefined && { text }) });
  const [w, send] = useSend();
  const [loaded, setLoaded] = useState<string | undefined>();
  const [failed, setFailed] = useState(false);
  const waits = unread(entry?.revision, loaded);
  const open = draft.state === "open";
  const editable = open && !waits;
  const unsavedList = holdsUnsaved(held, saved, conflict?.mine);
  useUnsaved(`assignment ${machine === "" ? "(new)" : short(machine)} of draft ${short(draft.id)}`, unsavedList);
  const drafted = useRef(draft.revision);

  // What the lists can offer. A fragment the draft holds has the layer of its revision, read once.
  const [profiles] = useList<Profile>("/profiles", "", true);
  const [fragments] = useList<Fragment>("/fragments", "", true);
  const [machines] = useList<Machine>("/machines", new URLSearchParams({ cluster: draft.cluster }).toString(), true);
  const [layers, setLayers] = useState<Record<string, string>>({});
  const asked = useRef(new Set<string>());
  // A layer read that failed is asked again by the next draft read or by the button: the fragment
  // stays out of the pickers meanwhile, and Show stays off.
  const [layerFailed, setLayerFailed] = useState<Record<string, true>>({});
  const [layerTry, tryLayers] = useState(0);
  useEffect(() => {
    for (const e of draft.entries) {
      const r = e.kind === "fragment" ? e.revision : null;
      if (r == null || asked.current.has(r)) continue;
      asked.current.add(r);
      void read<Revision>(`/fragment-revisions/${enc(r)}`, () => mounted.current).then((x) => {
        if (!mounted.current) return;
        if (x?.kind === "ok") return setLayers((l) => ({ ...l, [r]: x.data.layer }));
        asked.current.delete(r);
        setLayerFailed((f) => ({ ...f, [r]: true }));
      });
    }
  }, [read, draft.entries, layerTry]);
  const layersRead = draft.entries.every((e) => e.kind !== "fragment" || e.revision == null || layers[e.revision] !== undefined);
  // Only a revision the draft holds now, whose last read failed, offers the button.
  const layersFailed = draft.entries.some((e) => e.kind === "fragment" && e.revision != null && layers[e.revision] === undefined && layerFailed[e.revision] === true);
  const ready = profiles.kind === "ok" && fragments.kind === "ok" && layersRead;
  const profileList = profiles.kind === "ok" ? profiles.data.items : [];
  const fragmentList = fragments.kind === "ok" ? fragments.data.items : [];
  const forProfiles = profileOffers(draft, profileList);
  const forLayers = fragmentOffers(draft, fragmentList, layers);

  useEffect(() => {
    const revision = entry?.revision;
    if (revision == null) return;
    let live = true;
    setFailed(false);
    act({ kind: "reading", on: true });
    void read<S["AssignmentRevisionBody"]>(`/assignment-revisions/${enc(revision)}`, () => live).then((r) => {
      if (!live) return;
      if (r?.kind !== "ok") {
        setFailed(true);
        return setNote("The entry's revision could not be read.");
      }
      setLoaded(revision);
      act({ kind: "read", text: withLists(EMPTY, fromRevision(r.data)), since: done, revision: drafted.current, id: revision });
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
      void read<Draft>(`/drafts/${enc(draft.id)}`).then((r) => r?.kind === "ok" && act({ kind: "at", revision: r.data.revision }));
    } else if (o?.kind === "refused" || o?.kind === "not-done" || o?.kind === "set-aside") act({ kind: "failed" });
  }, [o]);
  const dismissed = o?.kind === "unknown" && writes?.open.includes(w!) !== true;
  useEffect(() => {
    if (dismissed) act({ kind: "failed" });
  }, [dismissed]);

  const path = `/drafts/${enc(draft.id)}/assignments/${enc(machine)}`;
  const ifMatch = () => keptETag(draft.id, writes?.client?.currentEpoch);
  const settled = settledFragment(draft.id, () => writes?.client?.currentEpoch);
  const save = () => {
    const m = ifMatch();
    if (machine === "") return setNote("Not sent: choose the machine first.");
    const refusal = sendRefusal(waits, conflict, shown);
    if (refusal !== undefined) return setNote(refusal);
    if (entry === undefined && inUse(draft, machine, "assignment")) return setNote("Not sent: this draft already holds an assignment for this machine. Open it from Entries to change it.");
    const bad = assignmentRefusal(lists);
    if (bad !== undefined) return setNote(bad);
    if (m === undefined) return setNote(NO_ETAG);
    setNote(undefined);
    if (startsNew(writes!, w)) {
      act({ kind: "send" });
      if (entry === undefined) claim(machine);
    }
    send({ method: "PUT", path, ifMatch: m, resource: `/drafts/${draft.id}`, body: assignmentBody(lists), settled });
  };
  const remove = () => {
    const m = ifMatch();
    const refusal = sendRefusal(waits, conflict, shown);
    if (refusal !== undefined) return setNote(refusal);
    if (m === undefined) return setNote(NO_ETAG);
    if (startsNew(writes!, w)) act({ kind: "send", removes: true });
    send({ method: "DELETE", path, ifMatch: m, resource: `/drafts/${draft.id}`, body: undefined, settled });
  };

  // Show composition and references: on request, one read at a time. The result belongs to the list
  // and the revisions its names resolved to when it was read.
  const [reads, setReads] = useState<number | undefined>();
  const [result, setResult] = useState<{ key: string; value: Composition | string } | undefined>();
  const nowKey = sourceKey(draft, lists, profileList, fragmentList);
  const show = () => {
    const key = nowKey;
    setReads(0);
    const get = async <T,>(p: string) => {
      const r = await read<T>(p, () => mounted.current);
      if (mounted.current) setReads((n) => (n ?? 0) + 1);
      return r?.kind === "ok" ? r.data : undefined;
    };
    void readComposition(draft, lists, profileList, fragmentList, get).then((value) => {
      if (!mounted.current) return;
      setReads(undefined);
      setResult({ key, value });
    });
  };
  const composed = result?.key === nowKey && typeof result.value !== "string" ? result.value : undefined;

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
          <ul aria-label="Your list">{SLOTS.flatMap((s) => listsOf(conflict.mine)[s].map((n) => <li key={`${s} ${n}`}>{s} {n}</li>))}</ul>
        </div>
      )}
      <button type="button" onClick={() => act({ kind: "copy" })} disabled={conflict.phase !== "theirs" || conflict.awaits === true || waits || conflict.revision !== draft.revision}>
        Copy my text into the new revision
      </button>
    </>
  );
  const pending = usePending(entry === undefined ? machines : profiles, entry === undefined ? "machines" : "profiles");
  const chooseable = machineOffers(draft, machines.kind === "ok" ? machines.data.items : []);
  return (
    <section aria-label={`Assignment ${machine === "" ? "(new)" : short(machine)}`}>
      <h3>
        Assignment{" "}
        {entry === undefined ? (
          <label>machine{" "}
            <select value={typed} onChange={(e) => setMachine(e.target.value)} required>
              <option value="">choose</option>
              {chooseable.map((m) => <option key={m.id} value={m.id}>{short(m.id)} {m.talosEndpoint}</option>)}
            </select>
          </label>
        ) : short(machine)}
      </h3>
      <p>{entry === undefined ? "new" : change(entry)}{unsavedList && " -> unsaved"}</p>
      <p>{ASSIGNMENT_STATEMENT}</p>
      {entry?.revision === null && <Status>Removed in this draft.</Status>}
      {waits && (failed
        ? <p><button type="button" onClick={() => retry((x) => x + 1)}>Read the entry again</button></p>
        : <Status>Reading the entry's text.</Status>)}
      {layersFailed && (
        <p><button type="button" onClick={() => { setLayerFailed({}); tryLayers((x) => x + 1); }}>Read the fragment layers again</button></p>
      )}
      <Members slot="profile" names={lists.profile} offers={forProfiles} ready={ready} open={open} editable={editable} change={(next) => hold({ ...lists, profile: next })} />
      {LAYERS.map((l) => (
        <Members key={l} slot={l} names={lists[l]} offers={forLayers[l]} ready={ready} open={open} editable={editable} change={(next) => hold({ ...lists, [l]: next })} />
      ))}
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
        if (w?.spec.method === "DELETE") return `Assignment of machine ${short(machine)} removed from draft ${short(draft.id)}.`;
        const r = (data as Partial<S["SourceUpdate"]>).entry?.revision;
        return `Assignment of machine ${short(machine)} saved in draft ${short(draft.id)}${typeof r === "string" ? ` (revision ${short(r)})` : ""}.`;
      }} />
      <h4>Composition and references</h4>
      <p>
        <button type="button" onClick={show} disabled={!ready || reads !== undefined || total(lists) === 0}>Show composition and references</button>
        {reads !== undefined && ` Reads done: ${reads}.`}
      </p>
      {result !== undefined && result.key !== nowKey && <Status>The list or a revision it names changed after this was read. Show it again.</Status>}
      {result?.key === nowKey && typeof result.value === "string" && <Status testid="composition-note">{result.value}</Status>}
      {composed !== undefined && (
        <>
          <ol aria-label="Composition order">
            {composed.order.map((c, i) => <li key={i}>{c.layer} {c.name} <code>{short(c.revision)}</code> {c.via === undefined ? "from this assignment" : `pinned by profile ${c.via}`}</li>)}
          </ol>
          <ul aria-label="Reference states">
            {[...new Set(composed.order.map((c) => c.revision))].flatMap((id) => {
              const f = composed.revisions[id]!;
              const refs = referenceStates(f.document, f.declarations.references);
              return refs.length === 0
                ? [<li key={id}>{f.name}: no references</li>]
                : refs.map((r) => <li key={`${id} ${r.name}`}>{f.name} {r.name}: {r.declared === undefined ? "no declaration" : `${r.declared.kind}, version ${r.declared.version}`}, {r.state}</li>);
            })}
          </ul>
        </>
      )}
    </section>
  );
}
