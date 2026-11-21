// The titles of releases, plans and operations (client contracts A8, gap G26): each built from fields
// the bodies carry, with the identifier kept as the stable reference. The bodies a title names, the draft,
// release, plan, ingestion and machine, are read by the store in bodies.ts.

import type { components } from "./api/v1";
import { SAFE } from "./principals.ts";
import { when } from "./states.tsx";

type S = components["schemas"];

// A draft's title is an author's text: shown only when safe (A8 rule 3), else the identifier.
export const draftText = (d: Pick<S["DraftBody"], "title"> | null | undefined): string | undefined =>
  d != null && SAFE.test(d.title) ? d.title : undefined;

// A name an author typed, shown only when it is safe text (A8 rule 3), else undefined.
export const safeText = (s: string): string | undefined => (SAFE.test(s) ? s : undefined);

// A draft in a sentence: its title in quotes where the title is safe text, else `fallback` (the shortened identifier).
export const draftCalled = (d: Pick<S["DraftBody"], "title"> | null | undefined, fallback: string): string => {
  const title = draftText(d);
  return title === undefined ? fallback : `"${title}"`;
};

export const releaseTitle =(r: Pick<S["ReleaseBody"], "publishedAt">, d: Pick<S["DraftBody"], "title"> | null | undefined): string | undefined => {
  const text = draftText(d);
  return text === undefined ? undefined : `${text}, published ${when(r.publishedAt)}`;
};

export const planTitle = (p: Pick<S["PlanBody"], "operation">, machine: string, release: string | undefined): string | undefined =>
  release === undefined ? undefined : `${p.operation === "adopt" ? "Adopt" : "Apply"} of ${machine} to ${release}`;

// A machine's imported configuration, as a link and, with the date, as its page's title. An import base has
// no number a person could read, so the day it was imported is what tells two of one machine apart.
export const importedConfiguration = (machine: string | undefined): string => (machine === undefined ? "Imported configuration" : `Imported configuration of ${machine}`);
export const importedConfigurationTitle = (machine: string | undefined, createdAt: string): string => `${importedConfiguration(machine)}, imported ${when(createdAt)}`;
// A revision of a fragment, profile or assignment has no number either: what it revises and when it was saved.
export const savedTitle = (what: string, createdAt: string): string => `${what}, saved ${when(createdAt)}`;

// `parts` holds the text of what the operation's subject names, each undefined where the subject names
// nothing of the kind or the read gave none: a draft's title, a machine's text, a plan's title.
export function operationTitle(kind: string, parts: { draft?: string | undefined; machine?: string | undefined; plan?: string | undefined }): string | undefined {
  switch (kind) {
    case "publish": return parts.draft === undefined ? undefined : `Publish of ${parts.draft}`;
    case "ingest": return parts.machine === undefined ? "Import" : `Import of ${parts.machine}`;
    case "apply-config":
    case "adopt": return parts.plan;
    default: return undefined;
  }
}
