// The shared names of a draft's page (operator experience §5.4, ginsys/bronzeward#1193): each shared fragment and
// profile with this cluster's view of it, and Adopt and Stop following, which record an entry in this draft and
// nothing else. A cluster is never moved from a shared page; adopting is an entry of the cluster's own draft.

import { useEffect, useState } from "react";
import type { components } from "./api/v1";
import { doneOnce, isAdoption, isShared, isSource, keepOnDone, keptETag, NO_ETAG, showTheirChange, WriteResult } from "./drafts.tsx";
import { useSend, useWrites } from "./forms.tsx";
import { useList, usePending } from "./list.tsx";
import { safeText } from "./safe.ts";
import { SavedRevision } from "./shared.tsx";
import { Action } from "./shell.tsx";
import { Status } from "./states.tsx";
import { ADOPT_PIN_NOTE, adoptSentence, NAME_NOT_SHOWN, SHARED_NAMES_NOTE, stopSentence } from "./words.ts";

type S = components["schemas"];
type Draft = S["DraftBody"];
type Adoption = S["ClusterAdoptionBody"];
type Kind = "fragment" | "profile";
// The part of a head this block reads; a shared head has no cluster.
interface Head { id: string; name: string; cluster: string | null; revision: string | null }

/** What the draft says of a shared name: an adoption, the removal of one, a shared edit, or a shared removal. */
export type Slot = { kind: "adopts"; revision: string } | { kind: "stops" } | { kind: "edits" } | { kind: "removed" } | undefined;
export function slotOf(entries: Draft["entries"], kind: Kind, name: string): Slot {
  for (const e of entries) {
    if (isAdoption(e) && e.of === kind && e.name === name) return e.revision === null ? { kind: "stops" } : { kind: "adopts", revision: e.revision };
    if (isSource(e) && isShared(e) && e.kind === kind && e.name === name) return e.revision === null ? { kind: "removed" } : { kind: "edits" };
  }
  return undefined;
}

/** This cluster's view of a name and what a person may do: the draft's entry first, else the cluster's adoption, else a shared profile's pin (no adoption). */
export type State = { kind: "edits" | "removed" | "stops" | "none" } | { kind: "adopts" | "follows"; revision: string; behind: boolean } | { kind: "via"; via: string };
export function stateOf(slot: Slot, rows: readonly Adoption[], cluster: string, head: string): State {
  if (slot?.kind === "adopts") return { kind: "adopts", revision: slot.revision, behind: slot.revision !== head };
  if (slot !== undefined) return { kind: slot.kind };
  const mine = rows.filter((r) => r.cluster === cluster);
  const own = mine.find((r) => r.via === null);
  if (own !== undefined) return { kind: "follows", revision: own.revision, behind: own.behind };
  const through = mine.find((r) => r.via !== null)?.via;
  return through == null ? { kind: "none" } : { kind: "via", via: through };
}
// Adopt where the cluster is not on the head's revision and the name has a head; Stop following where something is followed.
export const canAdopt = (s: State) => s.kind === "none" || s.kind === "stops" || s.kind === "via" || ((s.kind === "adopts" || s.kind === "follows") && s.behind);
export const canStop = (s: State) => s.kind === "adopts" || s.kind === "follows";

/** The request of a press: Adopt puts the head's revision, Stop following removes the adoption. */
export const adoptionWrite = (draft: string, kind: Kind, name: string, revision: string | null) => ({
  method: revision === null ? ("DELETE" as const) : ("PUT" as const),
  path: `/drafts/${encodeURIComponent(draft)}/adoptions/${kind}s/${encodeURIComponent(name)}`,
  body: revision === null ? undefined : { revision },
});

export const NOT_RECORDED = "Not recorded: someone changed this draft since this page read it.";

function Press({ draft, kind, name, revision, label, written, seeChange }: { draft: Draft; kind: Kind; name: string; revision: string | null; label: string; written: () => void; seeChange: () => void }) {
  const [w, send] = useSend();
  const writes = useWrites();
  const [note, setNote] = useState<string | undefined>();
  const o = w?.outcome;
  useEffect(() => {
    if (doneOnce(w) !== undefined) written();
  }, [o, w, written]);
  const press = () => {
    const epoch = writes?.client?.currentEpoch;
    const ifMatch = keptETag(draft.id, epoch);
    setNote(ifMatch === undefined ? NO_ETAG : undefined);
    if (ifMatch !== undefined) send({ ...adoptionWrite(draft.id, kind, name, revision), ifMatch, resource: `/drafts/${draft.id}`, settled: keepOnDone(draft.id, () => writes?.client?.currentEpoch) });
  };
  const refused = (
    <Status testid="write-result" role="alert" actions={<button type="button" onClick={() => setNote(showTheirChange(seeChange))}>Show their change</button>}>{NOT_RECORDED}</Status>
  );
  const done = () => (revision === null ? `Recorded: this cluster stops following ${kind} ${name}.` : `Recorded: this cluster adopts ${kind} ${name}. ${kind === "fragment" ? ADOPT_PIN_NOTE : ""}`);
  return (
    <>
      <Action label={label} needs={["author"]} onClick={press} busy={w !== undefined && w.outcome === undefined} />
      {note !== undefined && <Status>{note}</Status>}
      <WriteResult w={w} conflict={refused} done={done} />
    </>
  );
}

function Words({ kind, state }: { kind: Kind; state: State }) {
  switch (state.kind) {
    case "edits": return <>This draft changes the shared name: publishing it makes this cluster follow the new revision.</>;
    case "removed": return <>This draft removes the shared name from the library.</>;
    case "stops": return <>This draft stops this cluster following it.</>;
    case "none": return <>Not followed by this cluster.</>;
    case "via": return <>Reached through the shared profile {safeText(state.via) ?? NAME_NOT_SHOWN}, which is not an adoption.</>;
    default:
      return <>{state.kind === "adopts" ? "This draft adopts" : "Followed at"} <SavedRevision kind={kind} id={state.revision} />{state.behind && ", behind the shared head"}.</>;
  }
}

function NameRow({ kind, head, draft, written, seeChange }: { kind: Kind; head: Head; draft: Draft; written: () => void; seeChange: () => void }) {
  const [list] = useList<Adoption>(`/${kind}s/${encodeURIComponent(head.id)}/adoptions`, "", true);
  const pending = usePending(list, "who follows this name");
  const state = stateOf(slotOf(draft.entries, kind, head.name), list.kind === "ok" ? list.data.items : [], draft.cluster, head.revision ?? "");
  const shown = safeText(head.name) ?? NAME_NOT_SHOWN;
  // A name that is not read yet offers nothing: its view decides what a press does.
  const ready = list.kind === "ok" || slotOf(draft.entries, kind, head.name) !== undefined;
  return (
    <li>
      {kind} {shown}: {pending ?? <Words kind={kind} state={state} />}
      {ready && head.revision !== null && canAdopt(state) && (
        <p>{adoptSentence(kind, shown)}{" "}<Press draft={draft} kind={kind} name={head.name} revision={head.revision} label={`Adopt ${shown}`} written={written} seeChange={seeChange} /></p>
      )}
      {ready && canStop(state) && (
        <p>{stopSentence(kind, shown)}{" "}<Press draft={draft} kind={kind} name={head.name} revision={null} label={`Stop following ${shown}`} written={written} seeChange={seeChange} /></p>
      )}
    </li>
  );
}

function Names({ kind, draft, written, seeChange }: { kind: Kind; draft: Draft; written: () => void; seeChange: () => void }) {
  const [list] = useList<Head>(`/${kind}s`, "", true);
  const pending = usePending(list, `the shared ${kind}s`);
  if (list.kind !== "ok") return <>{pending}</>;
  const heads = list.data.items.filter((h) => h.cluster === null && h.revision !== null);
  return heads.length === 0 ? <p>No shared {kind} has a revision to adopt.</p> : <ul aria-label={`Shared ${kind}s`}>{heads.map((h) => <NameRow key={h.id} kind={kind} head={h} draft={draft} written={written} seeChange={seeChange} />)}</ul>;
}

/** The block of an open draft's page. Its reads start when it is opened, so a draft page that never uses it reads nothing more. */
export function SharedNames({ draft, written, seeChange }: { draft: Draft; written: () => void; seeChange: () => void }) {
  const [open, setOpen] = useState(false);
  return (
    <section aria-label="Shared names">
      <h3>Shared names</h3>
      <p>{SHARED_NAMES_NOTE}</p>
      {!open ? <p><button type="button" onClick={() => setOpen(true)}>Show shared names</button></p> : (
        <>
          <h4>Shared fragments</h4>
          <Names kind="fragment" draft={draft} written={written} seeChange={seeChange} />
          <h4>Shared profiles</h4>
          <Names kind="profile" draft={draft} written={written} seeChange={seeChange} />
        </>
      )}
    </section>
  );
}
