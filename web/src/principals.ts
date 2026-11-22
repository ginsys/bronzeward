// Who a principal identifier names (client contracts A7, gap G23): the names `GET /principals` gives,
// read in requests of at most 100 identifiers, each once, held in memory for one generation of the
// page's reads, and the text that shows them. A name is personal data: it is never stored, put in a
// URL or an attribute, logged or put in an error.

import type { components } from "./api/v1";
import type { Client, ReadResult } from "./request.ts";
import { SAFE } from "./safe.ts";

export type Principal = components["schemas"]["PrincipalBody"];

export const PRINCIPAL_READS = 100;
// The route answers 400 to the whole request for one identifier of another shape, and the e2e stubs
// use identifiers of other shapes, which are shown as they are. The 26 characters hold 128 bits and two
// zero bits, so the last is one of a, e, i, m, q, u, y and 4 (internal/id Parse refuses any other).
export const principalId = (id: string) => /^idn_[a-z2-7]{25}[aeimquy4]$/.test(id);

export { SAFE };

// A human's display name, else its subject at its issuer; a service's name. Nothing (the caller shows
// the shortened identifier) for an identifier no row holds, a read that did not answer, a record that
// names nobody, and text any member of which is not safe.
export function principalText(p: Principal | undefined): string | undefined {
  if (p === undefined) return undefined;
  const given = [p.display, p.subject, p.issuer, p.service].filter((v): v is string => v != null && v !== "");
  if (!given.every((v) => SAFE.test(v))) return undefined;
  if (p.kind === "service") return p.service || undefined;
  if (p.kind !== "human") return undefined;
  return p.display || (p.subject && p.issuer ? `${p.subject} at ${p.issuer}` : undefined);
}

export interface Names {
  // Reads the names of the identifiers not yet asked for under `gen`, in the next microtask so that
  // every identifier of one render goes out together. A new `gen` forgets what an older one held.
  // `settle` gets the refusals of the read that the page's shell must act on (the credentials, a
  // revocation, the process's refusal), whichever read meets them first.
  want(gen: string, ids: readonly string[], settle?: (res: ReadResult<unknown>) => void): void;
  // The name read for `id` under `gen`; undefined while pending, when the read failed, and for any
  // other generation.
  get(gen: string, id: string): Principal | undefined;
  subscribe(listener: () => void): () => void;
  // Resolves when every read started so far has answered (for tests).
  idle(): Promise<void>;
}

export function createNames(read: (path: string) => Promise<ReadResult<{ items: Principal[] }>>): Names {
  let current = "";
  // An identifier is asked for once: its key is present, its value is the answer or undefined.
  let known = new Map<string, Principal | undefined>();
  let queued: string[] = [];
  let tail: Promise<unknown> = Promise.resolve();
  const listeners = new Set<() => void>();
  const tell = () => listeners.forEach((l) => l());
  // The page's, from its latest want. One batch serves the identifiers of many `Who`, and every closure
  // of one generation settles the same page's reads the same way, so the latest is as good as any.
  let settle: ((res: ReadResult<unknown>) => void) | undefined;

  // An answer belongs to the map that was current when its read went out: the same generation can come
  // back (a return to a view) with a map of its own.
  const send = (ids: string[]) => {
    const mine = known;
    const settleHere = settle;
    return read(`/principals?${new URLSearchParams(ids.map((i) => ["id", i]))}`).then(
      (res) => {
        // Only a refusal the shell must act on goes to it: any other answer would count as one of a
        // page's own reads (machine.tsx countingSettle), and a failed lookup raises no banner.
        if (res.kind === "unauthenticated" || res.kind === "revoked" || res.kind === "refused") settleHere?.(res);
        if (mine !== known) return;
        // An identifier the answer leaves out, a failed read or a refusal show the identifier.
        const items = res.kind === "ok" ? res.data.items : [];
        for (const i of ids) known.set(i, items.find((p) => p.id === i));
        tell();
      },
      () => {},
    );
  };
  const flush = () => {
    const ids = queued;
    queued = [];
    if (ids.length === 0) return;
    const reads: Promise<void>[] = [];
    for (let i = 0; i < ids.length; i += PRINCIPAL_READS) reads.push(send(ids.slice(i, i + PRINCIPAL_READS)));
    tail = Promise.all([tail, ...reads]);
  };

  return {
    want(gen, ids, settleWith) {
      settle = settleWith;
      if (gen !== current) {
        [current, known, queued] = [gen, new Map(), []];
        tell();
      }
      const fresh = [...new Set(ids)].filter((i) => principalId(i) && !known.has(i));
      if (fresh.length === 0) return;
      for (const i of fresh) known.set(i, undefined);
      if (queued.length === 0) queueMicrotask(flush);
      queued.push(...fresh);
    },
    get: (gen, id) => (gen === current ? known.get(id) : undefined),
    subscribe(listener) {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
        // No page shows a name any more (a view without one is open): the next visit reads again. A
        // remount in the same turn keeps what was read.
        if (listeners.size === 0) setTimeout(() => listeners.size === 0 && ([current, known, queued] = ["", new Map(), []]), 0);
      };
    },
    async idle() {
      await Promise.resolve();
      await tail;
    },
  };
}

// One store for each client: the names of every page read through it, one generation at a time.
const stores = new WeakMap<object, Names>();
export function namesOf(client: Pick<Client, "read">): Names {
  let names = stores.get(client);
  if (names === undefined) stores.set(client, (names = createNames((path) => client.read<{ items: Principal[] }>(path))));
  return names;
}
