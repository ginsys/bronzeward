// The bodies a page's titles are built on (client contracts A8 rule 2): the store reads each path once for one
// generation of the page's reads, four at a time, and keeps what it read in memory only: a title can name a
// person (a draft's title is an author's text), so nothing here is stored, put in a URL or an attribute,
// logged or put in an error. It sits apart from the titles so that the list view's item read can offer the
// body it read itself, with no import of the generated API types.

import type { Client, ReadResult } from "./request.ts";

export const TITLE_READS = 4;

// The generation of a page's reads: they start again with the view and with each read again, a new epoch
// included, and a title read under another generation is not kept.
export const genOf = (view: string, again: number) => `${view}|${again}`;

export interface Bodies {
  // Asks for the paths not yet asked for under `gen`; a new `gen` forgets what an older one held.
  // `settle` gets the refusals of a read that the page's shell must act on.
  want(gen: string, paths: readonly string[], settle?: (res: ReadResult<unknown>) => void): void;
  // The body read at `path` under `gen`: undefined while pending and for another generation, null when
  // the read did not answer with a body.
  get(gen: string, path: string): unknown;
  // Keeps a body the page read itself under `gen`, unless that path has an answer or a read in flight, so
  // a title built on the page's own object reads it again no more.
  offer(gen: string, path: string, body: unknown): void;
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
    offer(gen, path, body) {
      if (gen !== current) reset(gen);
      if (known.has(path)) return;
      known.set(path, body);
      tell();
    },
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
