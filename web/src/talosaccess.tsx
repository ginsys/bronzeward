// The Talos access sign on the cluster page (ginsys/bronzeward#856): whether Bronzeward holds the
// cluster's Talos CA key, from GET /clusters/{id}/talos-access, for an author only. It says "held"
// and never "working": a held key is one a node accepted when it was supplied, and a later CA
// replacement can make it stale. The words are for a person: no status token, no path, no policy.

import type { ReactNode } from "react";
import type { components } from "./api/v1";
import { type Load, Ref, useItem } from "./list.tsx";
import { useRoles } from "./shell.tsx";
import { when } from "./states.tsx";

export type TalosAccess = components["schemas"]["TalosAccessBody"];

// The page where an operator supplies a key, named in words and linked.
const ONBOARD = "Onboard cluster page";
export const ONBOARD_PATH = "/clusters/onboard";

export const CHECKING = "Checking whether the Talos access key is held.";
export const HELD_MEANS = "Held means stored: a node accepted it when it was supplied.";
export const NOT_SUPPLIED = `Talos access key not supplied yet. Supply one on the ${ONBOARD}.`;
export const DELETED = `Talos access key was deleted in the secret store. Supply one again on the ${ONBOARD}.`;
export const CANNOT_TELL = "Cannot tell whether the Talos access key is held: the secret store did not give a usable answer. Open the page again to retry.";

// What the sign says for a read's result, one paragraph each; null for a read that was discarded. Any
// result that is not a usable answer is the one "cannot tell" text: an unreachable or sealed store
// never reads as "not supplied".
export function talosAccessWords(load: Load<TalosAccess>): string[] | null {
  if (load.kind === "loading") return [CHECKING];
  if (load.kind === "discarded") return null;
  if (load.kind !== "ok") return [CANNOT_TELL];
  const d = load.data;
  if (d.present === true) {
    if (typeof d.version !== "number" || typeof d.createdTime !== "string") return [CANNOT_TELL];
    return [`Talos access key held: supplied ${when(d.createdTime)} (version ${d.version}).`, HELD_MEANS];
  }
  if (d.reason === "never-written-or-removed") return [NOT_SUPPLIED];
  if (d.reason === "latest-version-deleted") return [DELETED];
  return [CANNOT_TELL];
}

// A paragraph with the page's name made a link to it.
function Words({ text }: { text: string }): ReactNode {
  const [head, tail] = text.split(ONBOARD);
  if (tail === undefined) return text;
  return <>{head}<Ref to={ONBOARD_PATH} id={ONBOARD}>{ONBOARD}</Ref>{tail}</>;
}

export function TalosAccessView({ load }: { load: Load<TalosAccess> }) {
  const words = talosAccessWords(load);
  if (words === null) return null;
  return <div data-testid="talos-access">{words.map((w) => <p key={w}><Words text={w} /></p>)}</div>;
}

// The read runs only where this component is mounted, and it is mounted only for an author: a hook
// cannot be conditional, so the gate is the parent's.
function TalosAccessRead({ cluster }: { cluster: string }) {
  return <TalosAccessView load={useItem<TalosAccess>(`/clusters/${encodeURIComponent(cluster)}/talos-access`)} />;
}

// The sign for a cluster: nothing, and no request, unless the roles hold author. The server's refusal
// stays authoritative; this only keeps the page from asking what it will be refused.
export function TalosAccessSign({ cluster }: { cluster: string }) {
  return useRoles().includes("author") ? <TalosAccessRead cluster={cluster} /> : null;
}
