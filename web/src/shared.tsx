// Shared fragments and profiles in the web (operator experience §5.4, ginsys/bronzeward#1193): what a draft's
// shared entries reach in the draft's cluster, and the clusters that use a shared name. Both only read. A cluster
// is never moved from here: adopting is an entry of that cluster's own draft.

import type { ReactNode } from "react";
import type { components } from "./api/v1";
import { EmptyList, Head, Id, More, Scroll, useItem, useList, usePending } from "./list.tsx";
import { safeText, safeWord } from "./safe.ts";
import { when } from "./states.tsx";
import { ClusterLink, MachineById, useBody } from "./titles.tsx";
import { NAME_NOT_SHOWN, SHARED_PAGE_NOTE, SHARED_REACH_NOTE } from "./words.ts";

type S = components["schemas"];
type Item = S["ImpactItem"];
type ImpactMachine = S["ImpactMachine"];

/** A list's scope cell: the library's is "shared"; any other value is shown as sent. */
export const scopeWords = (scope: string): string => (scope === "library" ? "shared" : scope);

const machines = (n: number) => (n === 1 ? "1 machine" : `${n} machines`);

// The changes the API names, in the order a person reads them, then any other value as sent: an entry that reaches
// machines never reads as one that reaches none.
const KNOWN: Record<string, string> = { gains: "Gains it here", loses: "Loses it here", keeps: "Keeps its revision here" };
const REASONS: Record<string, string> = {
  assignment: "its assignment selects the name",
  profile: "a profile of this cluster pins it",
  adoption: "only a shared profile's pin brings it",
};
// A value the server chose is looked up by its own key only: "constructor" or "__proto__" is an unknown value, never a method.
const known = (words: Record<string, string>, key: string): string | undefined => (Object.hasOwn(words, key) ? words[key] : undefined);
export const changesOf = (ms: readonly ImpactMachine[]): string[] => {
  const order = ["gains", "loses", "keeps"];
  for (const m of ms) if (!order.includes(m.change)) order.push(m.change);
  return order.filter((c) => ms.some((m) => m.change === c));
};
const SHOWN = 10;

export function Machines({ ids }: { ids: readonly string[] }) {
  return (
    <>
      {ids.slice(0, SHOWN).map((id, i) => <span key={id}>{i > 0 && ", "}<MachineById id={id} /></span>)}
      {ids.length > SHOWN && ` and ${ids.length - SHOWN} more`}
    </>
  );
}

/** One entry's reach: the machines of this cluster by change, and the other clusters it leaves as they are. */
export function ReachItem({ it }: { it: Item }) {
  const rows: ReactNode[] = [];
  for (const change of changesOf(it.machines)) {
    const ms = it.machines.filter((m) => m.change === change);
    const reasons = [...new Set(ms.map((m) => known(REASONS, m.reason) ?? safeWord(m.reason)))];
    rows.push(
      <li key={change}>
        {known(KNOWN, change) ?? `${safeWord(change)} here`}: {machines(ms.length)} (<Machines ids={ms.map((m) => m.machine)} />; {reasons.join(", ")})
      </li>,
    );
  }
  for (const [unchanged, label] of [[true, "Stays unchanged"], [false, "Also changes"]] as const) {
    const cs = it.otherClusters.filter((c) => c.unchanged === unchanged);
    if (cs.length === 0) continue;
    rows.push(
      <li key={label}>
        {label}: {cs.map((c, i) => (
          <span key={c.cluster}>{i > 0 && ", "}<ClusterLink id={c.cluster} /> on the revision <SavedRevision kind={it.kind === "profile" ? "profile" : "fragment"} id={c.revision} />{c.via !== null && ` through the shared profile ${safeText(c.via) ?? NAME_NOT_SHOWN}`}</span>
        ))}
      </li>,
    );
  }
  return (
    <li>
      {safeWord(it.kind)} {safeText(it.name) ?? NAME_NOT_SHOWN} (shared library)
      <ul>{rows.length === 0 ? <li>Reaches no machine of this cluster.</li> : rows}</ul>
    </li>
  );
}

/** The reach of a draft's shared entries; the entries of the cluster's own are not shown here. */
export function ReachItems({ items }: { items: readonly Item[] }) {
  const shared = items.filter((it) => it.scope === "library");
  return shared.length === 0 ? <p>The draft holds no shared entry.</p> : <ul aria-label="Reach of shared entries">{shared.map((it) => <ReachItem key={`${it.kind}:${it.name}`} it={it} />)}</ul>;
}

/** The reach block of a draft's page. `n` is bumped to read it again after a write. */
export function Reach({ draft, n }: { draft: string; n: number }) {
  const item = useItem<S["ImpactBody"]>(`/drafts/${encodeURIComponent(draft)}/impact`, undefined, n);
  const pending = usePending(item, "what the shared entries reach");
  return (
    <section aria-label="Reach of shared entries">
      <h3>What the shared entries reach</h3>
      <p>{SHARED_REACH_NOTE}</p>
      {item.kind === "ok" ? <ReachItems items={item.data.items} /> : pending}
    </section>
  );
}

type Adoption = S["ClusterAdoptionBody"];

/** How a cluster uses the name: adopted on the head or behind it, or only through a shared profile (never an adoption). */
export const useOf = (a: Pick<Adoption, "behind" | "via">): string => {
  const head = a.behind ? "behind the shared head" : "on the shared head";
  return a.via !== null ? `through the shared profile ${safeText(a.via) ?? NAME_NOT_SHOWN} (not an adoption), ${head}` : head;
};

export function AdoptionRows({ rows, kind = "fragment" }: { rows: readonly Adoption[]; kind?: "fragment" | "profile" }) {
  return (
    <Scroll label="Clusters using this name">
      <Head cols={["Cluster", "Revision", "Use", "Release", "Machines"]} />
      <tbody>
        {rows.map((a) => (
          <tr key={`${a.cluster}:${a.via ?? ""}`}>
            <th scope="row"><ClusterLink id={a.cluster} /></th>
            <td><SavedRevision kind={kind} id={a.revision} /></td>
            <td>{useOf(a)}</td>
            <td><Id id={a.release} none="none yet" /></td>
            <td>{a.machines.length}</td>
          </tr>
        ))}
      </tbody>
    </Scroll>
  );
}

/** A revision as a person reads it: when it was saved, once read, with the identifier as the link's title (the shortened one until then). */
export function SavedRevision({ kind, id }: { kind: "fragment" | "profile"; id: string }) {
  const body = useBody<{ createdAt?: string }>(`/${kind}-revisions/${encodeURIComponent(id)}`);
  return <Id id={id} text={body?.createdAt === undefined ? undefined : `saved ${when(body.createdAt)}`} />;
}

/** A shared fragment's or profile's page: every cluster that uses the name, with the revision it is on. */
export function SharedClusters({ kind, head }: { kind: "fragment" | "profile"; head: string }) {
  const [list, more, busy] = useList<Adoption>(`/${kind}s/${encodeURIComponent(head)}/adoptions`, "", false);
  const pending = usePending(list, "the clusters using this name");
  return (
    <>
      <h3>Clusters using this {kind}</h3>
      <p>{SHARED_PAGE_NOTE}</p>
      {list.kind !== "ok" ? pending : list.data.items.length === 0 && more === undefined ? <EmptyList list={list.data} text="No cluster uses this name yet." /> : (
        <><AdoptionRows rows={list.data.items} kind={kind} /><More list={list.data} shown={list.data.items.length} more={more} busy={busy} /></>
      )}
    </>
  );
}
