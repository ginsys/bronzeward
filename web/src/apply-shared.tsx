// The apply page's shared block (operator experience §5.4, ginsys/bronzeward#1193): each shared fragment or profile whose
// adoption this release moved, with the revision before and after and the machines of this cluster it reaches; the other
// clusters still behind it are named as information. Nothing here chooses, plans or approves for another cluster.

import { useEffect, useState } from "react";
import type { components } from "./api/v1";
import { type Assignment, movedSources, reach, type Source } from "./apply-shared.ts";
import { More, Ref, short, useItem, useList, usePending, useSettled } from "./list.tsx";
import { NO_LONGER_FOLLOWED } from "./resources.tsx";
import { safeText, safeWord } from "./safe.ts";
import { Machines, SavedRevision, useOf } from "./shared.tsx";
import { useClusterText } from "./titles.tsx";
import { NAME_NOT_SHOWN, APPLY_SHARED_NONE_BEHIND, APPLY_SHARED_NOTE, APPLY_SHARED_UNKNOWN } from "./words.ts";

type S = components["schemas"];
const enc = encodeURIComponent;

const machines = (n: number) => (n === 1 ? "1 machine" : `${n} machines`);

// Another cluster by its name once read, else by its shortened identifier: never "This cluster", which is what a cluster link
// says of a cluster it has not read and is wrong for a cluster that is not this one.
function OtherCluster({ id }: { id: string }) {
  const text = useClusterText(id);
  return <Ref to={`/clusters/${enc(id)}`} id={id}>{text ?? short(id)}</Ref>;
}

// A moved source of a kind this page knows: its name, where it came from and went to, what it reaches here, and the
// other clusters still behind it.
function Moved({ s, cluster, ids, assignments, pins }: { s: Source; cluster: string; ids: readonly string[]; assignments: Readonly<Record<string, Assignment>> | undefined; pins: ReadonlyMap<string, readonly string[]> | undefined }) {
  const kind = s.kind === "profile" ? "profile" : "fragment";
  const head = useItem<S["FragmentBody"] | S["ProfileBody"]>(`/${kind}s/${enc(s.head)}`);
  const [list, more, busy] = useList<S["ClusterAdoptionBody"]>(`/${kind}s/${enc(s.head)}/adoptions`, "behind=true", false);
  const pending = usePending(list, "the clusters behind it");
  const name = head.kind === "ok" ? safeText(head.data.name) ?? NAME_NOT_SHOWN : undefined;
  const there = assignments === undefined || head.kind !== "ok" ? undefined : reach(s, head.data.name, ids, assignments, pins);
  const others = list.kind === "ok" ? list.data.items.filter((a) => a.cluster !== cluster) : [];
  return (
    <li data-testid="apply-shared-source" data-kind={kind}>
      {kind} {name ?? "(reading)"}:{" "}
      {s.revision === null
        ? <>{s.previous !== null && <>was <SavedRevision kind={kind} id={s.previous} />, </>}{NO_LONGER_FOLLOWED}</>
        : <>{s.previous === null ? "adopted, with no revision before it, at " : <>from <SavedRevision kind={kind} id={s.previous} /> to </>}<SavedRevision kind={kind} id={s.revision} /></>}
      <ul>
        <li data-testid="apply-shared-reach">
          {there === undefined ? "Reading which machines it reaches." : there.machines.length === 0 ? "Reaches no machine of this release." : <>Reaches {machines(there.machines.length)} here: <Machines ids={there.machines} />.</>}
          {there?.unknown === true && <> {APPLY_SHARED_UNKNOWN}</>}
        </li>
        <li data-testid="apply-shared-others">
          {list.kind !== "ok" ? pending : others.length === 0 && more === undefined ? APPLY_SHARED_NONE_BEHIND : (
            <>
              Other clusters behind it, for information:
              <ul>{others.map((a) => <li key={`${a.cluster}:${a.via ?? ""}`} data-testid="apply-shared-other"><OtherCluster id={a.cluster} /> on <SavedRevision kind={kind} id={a.revision} />, {useOf(a)}</li>)}</ul>
              <More list={{ ...list.data, items: others }} shown={others.length} more={more} busy={busy} />
            </>
          )}
        </li>
      </ul>
    </li>
  );
}

function Block({ r, moved, assignments }: { r: S["ReleaseBody"]; moved: Source[]; assignments: Readonly<Record<string, Assignment>> | undefined }) {
  const read = useSettled();
  // A fragment is also reached through a profile that pins its revision: the release's profile revisions say which.
  const needPins = moved.some((s) => s.kind === "fragment" && s.revision !== null);
  const [pins, setPins] = useState<ReadonlyMap<string, readonly string[]> | undefined>(undefined);
  useEffect(() => {
    if (!needPins) return;
    let on = true;
    void (async () => {
      const out = new Map<string, readonly string[]>();
      let whole = true;
      for (const s of r.sources) {
        if (s.kind !== "profile" || s.revision === null) continue;
        const p = await read<S["ProfileRevisionBody"]>(`/profile-revisions/${enc(s.revision)}`, () => on);
        if (p?.kind === "ok") out.set(p.data.name, p.data.fragments);
        else whole = false;
      }
      if (on) setPins(whole ? out : undefined);
    })();
    return () => void (on = false);
  }, [needPins, r.sources, read]);
  const ids = r.machines.map((m) => m.machine);
  return (
    <section aria-label="Shared names this release moved" data-testid="apply-shared">
      <h3>Shared names this release moved</h3>
      <p>{APPLY_SHARED_NOTE}</p>
      <ul>
        {moved.map((s) => s.kind === "fragment" || s.kind === "profile"
          ? <Moved key={`${s.kind}:${s.head}`} s={s} cluster={r.cluster} ids={ids} assignments={assignments} pins={pins} />
          : <li key={`${s.kind}:${s.head}`}>{safeWord(s.kind)}: moved by this release; this page does not know how to show its reach.</li>)}
      </ul>
    </section>
  );
}

/** The block above the machines: nothing for a release that moved no shared name. `assignments` is undefined until the page has read them. */
export function SharedBlock({ r, assignments }: { r: S["ReleaseBody"]; assignments: Readonly<Record<string, Assignment>> | undefined }) {
  const moved = movedSources(r.sources);
  return moved.length === 0 ? null : <Block r={r} moved={moved} assignments={assignments} />;
}
