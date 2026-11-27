// What a release moved of the shared library, for the apply page (operator experience §5.4, ginsys/bronzeward#1193
// contract 13): the sources whose adoption moved, and the machines of this cluster each one reaches, derived from the
// release's own assignment and profile revisions. It reads nothing and moves no cluster.

import type { components } from "./api/v1";

type S = components["schemas"];
export type Source = S["ReleaseSourceBody"];

/** The parts of an assignment revision that say what a machine uses. */
export interface Assignment {
  profiles: readonly string[];
  fragments: Readonly<Record<string, readonly string[]>>;
}

/** A source of the library whose adoption this release set or moved: `adoptionRevision` is set and `previous` differs from `revision`. */
export const movedSources = (sources: readonly Source[]): Source[] => sources.filter((s) => s.scope === "library" && s.adoptionRevision !== null && s.previous !== s.revision);

export interface Reach {
  machines: string[];
  // A machine's assignment or a profile's pins could not be read, so the list may be short.
  unknown: boolean;
}

/**
 * The machines of this release a moved source reaches. A fragment reaches a machine whose assignment selects its name in a
 * layer or whose selected profile pins the revision; a profile reaches a machine whose assignment selects its name. A removed
 * name (no revision) reaches none. `assignments` holds the machines read so far (keyed by machine, read from the server),
 * and `pins` the fragment revisions each profile of the release pins, by profile name (undefined where a profile revision
 * could not be read).
 */
export function reach(s: Source, name: string, machines: readonly string[], assignments: Readonly<Record<string, Assignment>>, pins: ReadonlyMap<string, readonly string[]> | undefined): Reach {
  const rev = s.revision;
  if (rev === null) return { machines: [], unknown: false };
  const out: Reach = { machines: [], unknown: s.kind === "fragment" && pins === undefined };
  for (const m of machines) {
    const a = assignments[m];
    if (a === undefined) {
      out.unknown = true;
      continue;
    }
    const hit = s.kind === "profile" ? a.profiles.includes(name) : Object.values(a.fragments).some((l) => l.includes(name)) || a.profiles.some((p) => pins?.get(p)?.includes(rev) === true);
    if (hit) out.machines.push(m);
  }
  return out;
}
