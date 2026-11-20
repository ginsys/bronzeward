// The titles of releases, plans and operations (client contracts A8, gap G26): each built from fields
// the bodies carry, with the identifier kept as the stable reference. The store reads the draft, release,
// plan, ingestion and machine a title names, each once for one generation of the page's reads, four at a
// time, and keeps them in memory only: a title can name a person (a draft's title is an author's text),
// so nothing here is stored, put in a URL or an attribute, logged or put in an error.

import type { components } from "./api/v1";
import { SAFE } from "./principals.ts";
import type { Client, ReadResult } from "./request.ts";
import { when } from "./states.tsx";

type S = components["schemas"];

export const TITLE_READS = 4;

export interface Bodies {
  // Asks for the paths not yet asked for under `gen`; a new `gen` forgets what an older one held.
  // `settle` gets the refusals of a read that the page's shell must act on.
  want(gen: string, paths: readonly string[], settle?: (res: ReadResult<unknown>) => void): void;
  // The body read at `path` under `gen`: undefined while pending and for another generation, null when
  // the read did not answer with a body.
  get(gen: string, path: string): unknown;
  subscribe(listener: () => void): () => void;
  // Resolves when every read started so far has answered (for tests).
  idle(): Promise<void>;
}

export function createBodies(read: (path: string) => Promise<ReadResult<unknown>>, limit = TITLE_READS): Bodies {
  let current = "";
  // A path is asked for once: its key is present, its value is the body, null or undefined while pending.
  let known = new Map<string, unknown>();
  let queue: string[] = [];
  let running = 0;
  const listeners = new Set<() => void>();
  const quiet: (() => void)[] = [];
  const tell = () => listeners.forEach((l) => l());
  const reset = (gen: string) => void ([current, known, queue] = [gen, new Map(), []]);
  let settle: ((res: ReadResult<unknown>) => void) | undefined;

  // An answer belongs to the map that was current when its read went out.
  const pump = () => {
    while (running < limit && queue.length > 0) {
      const path = queue.shift()!;
      const mine = known;
      const settleHere = settle;
      running++;
      const done = (res: ReadResult<unknown> | undefined) => {
        // Only a refusal the shell must act on goes to it: any other answer would count as one of the
        // page's own reads, and a failed title read raises no banner.
        if (res?.kind === "unauthenticated" || res?.kind === "revoked" || res?.kind === "refused") settleHere?.(res);
        if (mine !== known) return;
        known.set(path, res?.kind === "ok" ? res.data : null);
        tell();
      };
      read(path).then(done, () => done(undefined)).finally(() => {
        running--;
        pump();
        if (running === 0 && queue.length === 0) quiet.splice(0).forEach((r) => r());
      });
    }
  };

  return {
    want(gen, paths, settleWith) {
      settle = settleWith;
      if (gen !== current) {
        reset(gen);
        tell();
      }
      const fresh = [...new Set(paths)].filter((p) => !known.has(p));
      if (fresh.length === 0) return;
      for (const p of fresh) known.set(p, undefined);
      queue.push(...fresh);
      pump();
    },
    get: (gen, path) => (gen === current ? known.get(path) : undefined),
    subscribe(listener) {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
        // No page shows a title any more: the next visit reads again. A remount in the same turn keeps
        // what was read.
        if (listeners.size === 0) setTimeout(() => listeners.size === 0 && reset(""), 0);
      };
    },
    async idle() {
      while (running > 0 || queue.length > 0) await new Promise<void>((r) => quiet.push(r));
    },
  };
}

// One store for each client: the bodies of every page read through it, one generation at a time.
const stores = new WeakMap<object, Bodies>();
export function bodiesOf(client: Pick<Client, "read">): Bodies {
  let b = stores.get(client);
  if (b === undefined) stores.set(client, (b = createBodies((path) => client.read<unknown>(path))));
  return b;
}

// A draft's title is an author's text: shown only when safe (A8 rule 3), else the identifier.
export const draftText = (d: Pick<S["DraftBody"], "title"> | null | undefined): string | undefined =>
  d != null && SAFE.test(d.title) ? d.title : undefined;

export const releaseTitle = (r: Pick<S["ReleaseBody"], "publishedAt">, d: Pick<S["DraftBody"], "title"> | null | undefined): string | undefined => {
  const text = draftText(d);
  return text === undefined ? undefined : `${text}, published ${when(r.publishedAt)}`;
};

export const planTitle = (p: Pick<S["PlanBody"], "operation">, machine: string, release: string | undefined): string | undefined =>
  release === undefined ? undefined : `${p.operation === "adopt" ? "Adopt" : "Apply"} of ${machine} to ${release}`;

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
