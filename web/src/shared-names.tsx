// The shared names of a draft's page (operator experience §5.4, ginsys/bronzeward#1193): each shared fragment and
// profile with this cluster's view of it, and Adopt and Stop following, which record an entry in this draft and
// nothing else. A cluster is never moved from a shared page; adopting is an entry of the cluster's own draft.

import { useState } from "react";
import type { components } from "./api/v1";
import { isAdoption, isShared, isSource, keptETag, NO_ETAG, settledFragment, showTheirChange, WriteResult } from "./drafts.tsx";
import { useSend, useWrites } from "./forms.tsx";
import { useList, usePending } from "./list.tsx";
import { safeText } from "./safe.ts";
import { SavedRevision } from "./shared.tsx";
import { Action } from "./shell.tsx";
import { Status } from "./states.tsx";
import { ADOPT_PIN_NOTE, adoptSentence, AGAIN_ADOPT, AGAIN_STOP, NAME_NOT_SHOWN, REMOVED_NAME_NOTE, SHARED_NAMES_NOTE, stopSentence } from "./words.ts";

type S = components["schemas"];
type Draft = S["DraftBody"];
type Adoption = S["ClusterAdoptionBody"];
type Kind = "fragment" | "profile";
// The part of a head this block reads; a shared head has no cluster.
interface Head { id: string; name: string; cluster: string | null; revision: string | null }

/** What the draft says of a shared name: an adoption, the removal of one, a shared edit, or a shared removal. */
export type Slot = { kind: "adopts"; revision: string; base: number | null } | { kind: "stops"; base: number | null } | { kind: "edits" } | { kind: "removed" } | undefined;
export function slotOf(entries: Draft["entries"], kind: Kind, name: string): Slot {
  for (const e of entries) {
    if (isAdoption(e) && e.of === kind && e.name === name) return e.revision === null ? { kind: "stops", base: e.base } : { kind: "adopts", revision: e.revision, base: e.base };
    if (isSource(e) && isShared(e) && e.kind === kind && e.name === name) return e.revision === null ? { kind: "removed" } : { kind: "edits" };
  }
  return undefined;
}

/** This cluster's view of a name and what a person may do: the draft's entry first, else the cluster's adoption, else a shared profile's pin (no adoption). */
export type State = { kind: "edits" | "removed" | "none" } | { kind: "stops"; again: boolean } | { kind: "follows"; revision: string; behind: boolean } | { kind: "adopts"; revision: string; behind: boolean; removable: boolean; again: boolean } | { kind: "via"; via: string };
export function stateOf(slot: Slot, rows: readonly Adoption[], cluster: string, head: string): State {
  const mine = rows.filter((r) => r.cluster === cluster);
  const own = mine.find((r) => r.via === null);
  // `again`: the entry's base (the counter it was staged on, none when the cluster had no row) is not the row's now, so publication
  // would refuse it; a press records the current counter. A removal whose row is gone cannot be pressed again (404), and over a removed row neither can a removal of a
  // re-adoption (`removable`, persistence-api §3.1.1).
  if (slot?.kind === "adopts") return { kind: "adopts", revision: slot.revision, behind: slot.revision !== head, removable: slot.base === null || own !== undefined, again: (own?.adoptionRevision ?? null) !== slot.base };
  if (slot?.kind === "stops") return { kind: "stops", again: own !== undefined && own.adoptionRevision !== slot.base };
  if (slot !== undefined) return { kind: slot.kind };
  if (own !== undefined) return { kind: "follows", revision: own.revision, behind: own.behind };
  const through = mine.find((r) => r.via !== null)?.via;
  return through == null ? { kind: "none" } : { kind: "via", via: through };
}
// Adopt where the cluster is not on the head's revision and the name has a head, and for any adoption the draft staged: a row another
// draft removed is not in the list, so a staged entry gone stale cannot always be seen, and pressing again only records the
// current counter. Stop following where something is followed.
export const canAdopt = (s: State) => s.kind === "none" || s.kind === "stops" || s.kind === "via" || s.kind === "adopts" || (s.kind === "follows" && s.behind);
export const canStop = (s: State) => s.kind === "follows" || (s.kind === "adopts" && s.removable) || (s.kind === "stops" && s.again);

/** A name as a person reads it: the server's name where it is safe text. */
export const shownName = (name: string) => safeText(name) ?? NAME_NOT_SHOWN;
/** What a done press says; the name passes the safe-text rule, as everywhere on the page. */
export const recorded = (kind: Kind, name: string, stops: boolean) =>
  stops ? `Recorded: this cluster stops following ${kind} ${shownName(name)}.` : `Recorded: this cluster adopts ${kind} ${shownName(name)}.${kind === "fragment" ? ` ${ADOPT_PIN_NOTE}` : ""}`;

/** The request of a press: Adopt puts the head's revision, Stop following removes the adoption. */
export const adoptionWrite = (draft: string, kind: Kind, name: string, revision: string | null) => ({
  method: revision === null ? ("DELETE" as const) : ("PUT" as const),
  path: `/drafts/${encodeURIComponent(draft)}/adoptions/${kind}s/${encodeURIComponent(name)}`,
  body: revision === null ? undefined : { revision },
});

export const NOT_RECORDED = "Not recorded: someone changed this draft since this page read it.";

function Press({ draft, kind, name, revision, label, seeChange }: { draft: Draft; kind: Kind; name: string; revision: string | null; label: string; seeChange: () => void }) {
  const [w, send] = useSend();
  const writes = useWrites();
  const [note, setNote] = useState<string | undefined>();
  const press = () => {
    const epoch = writes?.client?.currentEpoch;
    const ifMatch = keptETag(draft.id, epoch);
    setNote(ifMatch === undefined ? NO_ETAG : undefined);
    // The settled hook keeps the answer's ETag and reads the draft again on whichever page shows it when the write settles, so a
    // page opened again after this one was left is not shown old entries under the newer ETag (ginsys/bronzeward#517 H3).
    if (ifMatch !== undefined) send({ ...adoptionWrite(draft.id, kind, name, revision), ifMatch, resource: `/drafts/${draft.id}`, settled: settledFragment(draft.id, () => writes?.client?.currentEpoch) });
  };
  const refused = (
    <Status testid="write-result" role="alert" actions={<button type="button" onClick={() => setNote(showTheirChange(seeChange))}>Show their change</button>}>{NOT_RECORDED}</Status>
  );
  const done = () => recorded(kind, name, revision === null);
  return (
    <>
      <Action label={label} needs={["author"]} onClick={press} busy={w !== undefined && w.outcome === undefined} />
      {note !== undefined && <Status>{note}</Status>}
      <WriteResult w={w} conflict={refused} done={done} />
    </>
  );
}

function Words({ kind, state, gone }: { kind: Kind; state: State; gone: boolean }) {
  switch (state.kind) {
    case "edits": return <>This draft changes the shared name: publishing it makes this cluster follow the new revision.</>;
    case "removed": return <>This draft removes the shared name from the library.</>;
    case "stops": return <>This draft stops this cluster following it.{state.again && ` ${AGAIN_STOP}`}</>;
    case "none": return <>Not followed by this cluster.</>;
    case "via": return <>Reached through the shared profile {safeText(state.via) ?? NAME_NOT_SHOWN}, which is not an adoption.</>;
    default:
      return <>{state.kind === "adopts" ? "This draft adopts" : "Followed at"} the revision <SavedRevision kind={kind} id={state.revision} />{state.behind && !gone && ", behind the shared head"}.{gone && ` ${REMOVED_NAME_NOTE}`}{state.kind === "adopts" && state.again && ` ${AGAIN_ADOPT}`}</>;
  }
}

function NameRow({ kind, head, draft, seeChange }: { kind: Kind; head: Head; draft: Draft; seeChange: () => void }) {
  const [list] = useList<Adoption>(`/${kind}s/${encodeURIComponent(head.id)}/adoptions`, "", true);
  const pending = usePending(list, "who follows this name");
  const state = stateOf(slotOf(draft.entries, kind, head.name), list.kind === "ok" ? list.data.items : [], draft.cluster, head.revision ?? "");
  const shown = safeText(head.name) ?? NAME_NOT_SHOWN;
  // A name that is not read yet offers nothing: its view decides what a press does.
  const ready = list.kind === "ok";
  // A removed name this cluster does not follow has nothing to adopt and nothing to stop.
  const gone = head.revision === null;
  if (ready && gone && (state.kind === "none" || state.kind === "via" || state.kind === "removed")) return null;
  return (
    <li>
      {kind} {shown}: {pending ?? <Words kind={kind} state={state} gone={gone} />}
      {ready && head.revision !== null && canAdopt(state) && (
        <div>{adoptSentence(kind, shown)}{" "}<Press draft={draft} kind={kind} name={head.name} revision={head.revision} label={`Adopt ${shown}`} seeChange={seeChange} /></div>
      )}
      {ready && canStop(state) && (
        <div>{stopSentence(kind, shown)}{" "}<Press draft={draft} kind={kind} name={head.name} revision={null} label={`Stop following ${shown}`} seeChange={seeChange} /></div>
      )}
    </li>
  );
}

function Names({ kind, draft, seeChange }: { kind: Kind; draft: Draft; seeChange: () => void }) {
  const [list] = useList<Head>(`/${kind}s`, "", true);
  const pending = usePending(list, `the shared ${kind}s`);
  if (list.kind !== "ok") return <>{pending}</>;
  // A removed head is still a head (persistence-api §3.1.1): a cluster that follows its name keeps the revision, so the row stays to say so and to stop following.
  const heads = list.data.items.filter((h) => h.cluster === null);
  return (
    <>
      {!heads.some((h) => h.revision !== null) && <p>No shared {kind} has a revision to adopt.</p>}
      {heads.length > 0 && <ul aria-label={`Shared ${kind}s`}>{heads.map((h) => <NameRow key={h.id} kind={kind} head={h} draft={draft} seeChange={seeChange} />)}</ul>}
    </>
  );
}

/** The block of an open draft's page. Its reads start when it is opened, so a draft page that never uses it reads nothing more. */
export function SharedNames({ draft, seeChange }: { draft: Draft; seeChange: () => void }) {
  const [open, setOpen] = useState(false);
  return (
    <section aria-label="Shared names">
      <h3>Shared names</h3>
      <p>{SHARED_NAMES_NOTE}</p>
      {!open ? <p><button type="button" onClick={() => setOpen(true)}>Show shared names</button></p> : (
        <>
          <h4>Shared fragments</h4>
          <Names kind="fragment" draft={draft} seeChange={seeChange} />
          <h4>Shared profiles</h4>
          <Names kind="profile" draft={draft} seeChange={seeChange} />
        </>
      )}
    </section>
  );
}
