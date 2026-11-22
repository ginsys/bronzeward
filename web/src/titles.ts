// The titles of releases, plans and operations (client contracts A8, gap G26): each built from fields
// the bodies carry, with the identifier kept as the stable reference. The bodies a title names, the draft,
// release, plan, ingestion and machine, are read by the store in bodies.ts.

import type { components } from "./api/v1";
import { short } from "./list.tsx";
import { SAFE } from "./principals.ts";
import { when } from "./states.tsx";
import { UNTITLED_DRAFT } from "./words.ts";

type S = components["schemas"];

// A draft's title is an author's text: shown only when safe (A8 rule 3), else undefined.
export const draftText = (d: Pick<S["DraftBody"], "title"> | null | undefined): string | undefined =>
  d != null && SAFE.test(d.title) ? d.title : undefined;

// A draft's name: its title where safe, else "Untitled draft" (A8 rule 3); undefined where the draft is not read.
export const draftName = (d: Pick<S["DraftBody"], "title"> | null | undefined): string | undefined =>
  d == null ? undefined : draftText(d) ?? UNTITLED_DRAFT;

// A name an author typed, shown only when it is safe text (A8 rule 3), else undefined.
export const safeText = (s: string): string | undefined => (SAFE.test(s) ? s : undefined);

// A draft in a sentence: its title in quotes where the title is safe text, else "Untitled draft" and the shortened identifier.
export const draftCalled = (d: Pick<S["DraftBody"], "id" | "title">): string => {
  const title = draftText(d);
  return title === undefined ? `${UNTITLED_DRAFT} (${short(d.id)})` : `"${title}"`;
};

// An assignment has no name: it is named by the machine it covers (a list row and a revision's heading both use this).
export const assignmentName = (machine: string): string => `Assignment of ${machine}`;

// A dependency by its object and version, in its page's heading and in its list row; an object that is not safe
// text (A8 rule 3) leaves the shortened identifier.
export const dependencyName = (d: Pick<S["DependencyBody"], "id" | "object" | "version">): string => {
  const object = safeText(d.object);
  return object === undefined ? `Dependency ${short(d.id)}` : `Dependency ${object}, version ${d.version}`;
};

export const releaseTitle = (r: Pick<S["ReleaseBody"], "publishedAt">, d: Pick<S["DraftBody"], "title"> | null | undefined): string | undefined => {
  const text = draftName(d);
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

// A dependency alert's sentence after its subject (the dependency, named once by the row), from its kind and reason
// (dependency monitor section 6.2). `unknown` covers every reason, so `regression` and `persistent` claim only that
// the version cannot be confirmed, and the deleted-or-restored caveat is the specification's own for an absent version.
// A kind of no dependency, or one this client does not know, is a whole sentence: `subject` is false for it.
export function alertSentence(a: Pick<S["AlertBody"], "kind" | "reason" | "deletion">): { subject: boolean; text: string } {
  switch (a.kind) {
    case "lost": return { subject: true, text: "is lost: the provider no longer has this version." };
    case "blocked": return { subject: true, text: "is blocked. The block can be undone; the Reason column says how." };
    case "regression":
      return { subject: true, text: `can no longer be confirmed. It was found before.${a.reason === "absent" ? " The provider may have deleted it, or been restored from a backup older than Bronzeward's database." : ""}` };
    case "persistent": return { subject: true, text: "still cannot be confirmed." };
    case "deletion-scheduled": return { subject: true, text: a.deletion == null || Number.isNaN(Date.parse(a.deletion)) ? "is scheduled for deletion." : `is scheduled for deletion on ${when(a.deletion)}.` };
    case "monitor-stalled": return { subject: false, text: "The dependency monitor has stopped making progress." };
    default: return { subject: false, text: safeText(a.kind) === undefined ? "Alert." : `Alert of kind ${a.kind}.` };
  }
}
