import { createElement as h, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import type { components } from "./api/v1";
import { ReadsProvider, type Reads, Id } from "./list.tsx";
import { RevokedBy } from "./approval.tsx";
import { RecoveryFacts } from "./recovery.tsx";
import { ACTS, by, REVISIONS } from "./resources.tsx";
import { createNames, namesOf, principalId, principalText, PRINCIPAL_READS } from "./principals.ts";
import { Who } from "./principals.tsx";
import type { Client, ReadResult } from "./request.ts";

type P = components["schemas"]["PrincipalBody"];
// Client contracts A7 and G23: what the web client shows for a principal identifier, and how it reads them.
const id = (i: number) => `idn_${String.fromCharCode(97 + Math.floor(i / 26))}${String.fromCharCode(97 + (i % 26))}${"a".repeat(24)}`;
const human = (i: number, more: Partial<P> = {}): P => ({ id: id(i), kind: "human", issuer: "https://idp.example", subject: `sub-${i}`, display: null, service: null, ...more });
const service = (i: number, name: string): P => ({ id: id(i), kind: "service", issuer: null, subject: null, display: null, service: name });
const unknown = (i: number): P => ({ id: id(i), kind: "unknown", issuer: null, subject: null, display: null, service: null });
const answer = (items: P[]): ReadResult<{ items: P[] }> => ({ kind: "ok", data: { items } });

describe("principalText: the fallbacks of A7 (G23)", () => {
  test("a human's display name, else its subject at its issuer, a service's name", () => {
    expect(principalText(human(0, { display: "Ada Lovelace" }))).toBe("Ada Lovelace");
    expect(principalText(human(0))).toBe("sub-0 at https://idp.example");
    expect(principalText(human(0, { display: "" }))).toBe("sub-0 at https://idp.example");
    expect(principalText(service(1, "backup-agent"))).toBe("backup-agent");
  });

  test("nothing for an unknown identifier, a read that did not answer, or a record that names nobody", () => {
    expect(principalText(unknown(2))).toBeUndefined();
    expect(principalText(undefined)).toBeUndefined();
    expect(principalText(human(0, { subject: null }))).toBeUndefined();
    expect(principalText(service(1, ""))).toBeUndefined();
  });

  test("text with a control or format character, or over 300 code points, is not shown (the operation cause rule)", () => {
    for (const bad of ["Ada\nLovelace", "Ada\u001b[31m", "Ada‮Lovelace", "a‍b", "x".repeat(301)]) {
      expect(principalText(human(0, { display: bad })), JSON.stringify(bad)).toBeUndefined();
    }
    // One unsafe member makes the whole text unsafe, a member the text does not use included.
    expect(principalText(human(0, { display: "Ada", issuer: "https://idp‮.example" }))).toBeUndefined();
    expect(principalText(human(0, { display: "x".repeat(300) }))).toBe("x".repeat(300));
    expect(principalText(human(0, { display: "😀".repeat(300) }))).toBe("😀".repeat(300));
  });
});

describe("the store: at most 100 identifiers a read, each once", () => {
  const stub = (answers: (ids: string[]) => ReadResult<{ items: P[] }> = (ids) => answer(ids.map((_, i) => human(i)).map((p, i) => ({ ...p, id: ids[i]! })))) => {
    const calls: string[][] = [];
    const names = createNames(async (path) => {
      const ids = new URL(path, "http://x").searchParams.getAll("id");
      calls.push(ids);
      return answers(ids);
    });
    return { names, calls };
  };

  test("250 distinct identifiers with repeats make reads of 100, 100 and 50, no identifier twice", async () => {
    const { names, calls } = stub();
    const ids = Array.from({ length: 250 }, (_, i) => id(i));
    names.want("g", ids);
    names.want("g", [...ids.slice(0, 20), id(3)]);
    await names.idle();
    expect(calls.map((c) => c.length)).toEqual([PRINCIPAL_READS, PRINCIPAL_READS, 50]);
    expect(new Set(calls.flat()).size).toBe(250);
    expect(calls.flat()).toEqual(ids);
  });

  test("wants made in one tick go out as one read, and a known identifier is not read again", async () => {
    const { names, calls } = stub();
    names.want("g", [id(1)]);
    names.want("g", [id(2)]);
    await names.idle();
    names.want("g", [id(1), id(2)]);
    await names.idle();
    expect(calls).toEqual([[id(1), id(2)]]);
    expect(names.get("g", id(1))?.subject).toBe("sub-0");
  });

  test("an identifier the route would refuse is never sent: one such identifier fails the whole read", async () => {
    const { names, calls } = stub();
    // The last character of a 128-bit base32 body carries four bits and two zero bits: only a, e, i, m, q, u, y and 4 end an identifier (internal/id/id.go Parse).
    const trailing = `idn_${"a".repeat(25)}b`;
    names.want("g", ["h-publisher", "idn_SHORT", `idn_${"A".repeat(26)}`, trailing, id(1), `idn_${"a".repeat(25)}4`]);
    await names.idle();
    expect(calls).toEqual([[id(1), `idn_${"a".repeat(25)}4`]]);
    expect(principalId(trailing)).toBe(false);
    const none = stub();
    none.names.want("g", ["h-publisher", "prn_synthetic_me"]);
    await none.names.idle();
    expect(none.calls).toEqual([]);
  });

  test("a failed read, a refusal, and an identifier the answer leaves out show the fallback and are not asked again", async () => {
    const failed = stub(() => ({ kind: "unavailable", status: 503, problem: null }));
    failed.names.want("g", [id(1), id(2)]);
    await failed.names.idle();
    expect(failed.names.get("g", id(1))).toBeUndefined();
    failed.names.want("g", [id(1)]);
    await failed.names.idle();
    expect(failed.calls).toHaveLength(1);
    const left = stub(() => answer([human(1)]));
    left.names.want("g", [id(1), id(2)]);
    await left.names.idle();
    expect(left.names.get("g", id(1))?.kind).toBe("human");
    expect(left.names.get("g", id(2))).toBeUndefined();
    const thrown = createNames(() => Promise.reject(new Error("offline")));
    thrown.want("g", [id(1)]);
    await expect(thrown.idle()).resolves.toBeUndefined();
  });

  test("a new generation (a view, a read again, a new epoch) forgets every name, and an answer of the old one is dropped", async () => {
    let release: (() => void) | undefined;
    const calls: string[][] = [];
    const names = createNames(async (path) => {
      calls.push(new URL(path, "http://x").searchParams.getAll("id"));
      if (calls.length === 1) await new Promise<void>((r) => void (release = r));
      return answer([human(1)]);
    });
    names.want("view-a|0", [id(1)]);
    await Promise.resolve();
    await Promise.resolve();
    names.want("view-a|1", [id(1)]);
    release?.();
    await names.idle();
    // The first read answered under the old generation: only the second answer is kept.
    expect(calls).toHaveLength(2);
    expect(names.get("view-a|0", id(1))).toBeUndefined();
    expect(names.get("view-a|1", id(1))?.id).toBe(id(1));
  });

  test("leaving every view that shows a name forgets the names, and a remount in the same turn does not", async () => {
    const { names, calls } = stub();
    const off = names.subscribe(() => {});
    names.want("g", [id(1)]);
    await names.idle();
    // A remount in one commit (a list drawn again, StrictMode) keeps what was read.
    off();
    names.subscribe(() => {});
    await new Promise((r) => setTimeout(r, 0));
    names.want("g", [id(1)]);
    await names.idle();
    expect(calls).toHaveLength(1);
    // Another subscriber still shows names: one leaving forgets nothing.
    const gone = names.subscribe(() => {});
    gone();
    await new Promise((r) => setTimeout(r, 0));
    expect(names.get("g", id(1))?.kind).toBe("human");
    // Every subscriber gone for a turn (a page with no name): a later visit under the same generation reads again.
    const last = stub();
    const offLast = last.names.subscribe(() => {});
    last.names.want("g", [id(1)]);
    await last.names.idle();
    offLast();
    await new Promise((r) => setTimeout(r, 0));
    expect(last.names.get("g", id(1))).toBeUndefined();
    last.names.want("g", [id(1)]);
    await last.names.idle();
    expect(last.calls).toHaveLength(2);
  });

  test("a read of an earlier visit that answers late, under the same generation, is dropped", async () => {
    const releases: (() => void)[] = [];
    let n = 0;
    const names = createNames(async () => {
      const mine = n++;
      if (mine === 0) await new Promise<void>((r) => releases.push(r));
      return mine === 0 ? ({ kind: "discarded" } as ReadResult<{ items: P[] }>) : answer([human(1)]);
    });
    const off = names.subscribe(() => {});
    names.want("g", [id(1)]);
    await Promise.resolve();
    await Promise.resolve();
    // The visit ends (no view shows a name), and the operator returns under the same generation before the first read answered.
    off();
    await new Promise((r) => setTimeout(r, 0));
    names.subscribe(() => {});
    names.want("g", [id(1)]);
    await Promise.resolve();
    await Promise.resolve();
    await Promise.resolve();
    releases[0]?.();
    await names.idle();
    await new Promise((r) => setTimeout(r, 0));
    expect(names.get("g", id(1))?.kind).toBe("human");
  });

  test("a refusal the shell must act on is handed to the page's settle, and the name stays the fallback; no other answer is", async () => {
    const problem = { type: "t", title: "t", status: 401 } as never;
    const results: ReadResult<{ items: P[] }>[] = [
      { kind: "unauthenticated", problem: null },
      { kind: "revoked", problem },
      { kind: "refused", status: 409, problem: null },
      answer([human(1)]),
      { kind: "unavailable", status: 503, problem: null },
      { kind: "transport" },
      { kind: "discarded" },
    ];
    const settled: string[] = [];
    for (const res of results) {
      const names = createNames(async () => res);
      names.want("g", [id(1)], (r) => void settled.push(r.kind));
      await names.idle();
      if (res.kind !== "ok") expect(names.get("g", id(1))).toBeUndefined();
    }
    expect(settled).toEqual(["unauthenticated", "revoked", "refused"]);
  });

  test("it tells its subscribers when an answer arrives", async () => {
    const { names } = stub();
    let told = 0;
    const off = names.subscribe(() => void told++);
    names.want("g", [id(1)]);
    await names.idle();
    expect(told).toBeGreaterThan(0);
    off();
  });
});

// A client whose route answers each identifier with `of(id)`, or with `result`; the store of that client is the one Who reads.
const make = (of: (id: string) => P = (i) => human(0, { id: i }), result?: ReadResult<{ items: P[] }>) => {
  const client: Pick<Client, "read"> = { read: (async (path: string) => result ?? answer(new URL(path, "http://x").searchParams.getAll("id").map(of))) as Pick<Client, "read">["read"] };
  const reads: Reads = { client, settle: () => false, undropped: () => () => true, view: "v", again: 0, regained: 0, go: () => {}, failure: () => null };
  return { names: namesOf(client), show: (n: ReactNode) => renderToStaticMarkup(h(ReadsProvider, { value: reads }, n)) };
};
const gen = "v|0";
const shortened = `<span title="${id(1)}">idn_abaa…aaaa</span>`;


describe("Who (G23): the name, with the identifier in the title, else the shortened identifier", () => {
  test("shows the name once it is read, and the identifier whole in the title, never the name", async () => {
    const { names, show } = make();
    expect(show(h(Who, { id: id(1) }))).toBe(shortened);
    names.want(gen, [id(1)]);
    await names.idle();
    expect(show(h(Who, { id: id(1) }))).toBe(`<span title="${id(1)}">sub-0 at https://idp.example</span>`);
  });

  test("shows the shortened identifier when the read failed, the identifier is unknown or the name is not safe", async () => {
    for (const [label, m] of [
      ["failed", make(undefined, { kind: "transport" })],
      ["unknown", make((i) => ({ ...unknown(1), id: i }))],
      ["unsafe", make((i) => human(0, { id: i, display: "Ada\u202eLovelace" }))],
    ] as const) {
      m.names.want(gen, [id(1)]);
      await m.names.idle();
      expect(m.show(h(Who, { id: id(1) })), label).toBe(shortened);
    }
  });

  test("an identifier that is not well formed, or none, is shown as Id shows it", () => {
    const { show } = make();
    expect(show(h(Who, { id: "h-publisher" }))).toBe(show(h(Id, { id: "h-publisher" })));
    expect(show(h(Who, { id: "h-publisher" }))).toBe('<span title="h-publisher">h-pu…sher</span>');
    expect(show(h(Who, { id: null }))).toBe("none");
    expect(show(h(Who, { id: undefined, none: "an approver" }))).toBe("an approver");
  });

  test("markup in a name is shown as text", async () => {
    const { names, show } = make((i) => human(0, { id: i, display: "<script>alert(1)</script>" }));
    names.want(gen, [id(1)]);
    await names.idle();
    expect(show(h(Who, { id: id(1) }))).toBe(`<span title="${id(1)}">&lt;script&gt;alert(1)&lt;/script&gt;</span>`);
  });
});

describe("the sites that show a principal (G23): each shows the name, and the shortened identifier until it is read", () => {
  const named = "sub-0 at https://idp.example";
  const seeded = async () => {
    const m = make();
    m.names.want(gen, [id(1)]);
    await m.names.idle();
    return m;
  };

  test("a release's, plan's or operation's By, a revision's Author and an act's Principal", async () => {
    const { show } = await seeded();
    expect(show(by({ principal: id(1), role: "publisher" }))).toBe(`<span title="${id(1)}">${named}</span> (publisher)`);
    expect(show(by(null))).toBe("none");
    const act = { principal: id(1), principalKind: "human" } as Parameters<(typeof ACTS)[number][1]>[0];
    expect(show(ACTS[1]![1](act))).toBe(`<span title="${id(1)}">${named}</span> (human)`);
    const revision = { id: "frv_x", author: id(1), createdAt: "2026-11-20T04:00:00Z" };
    expect(show(REVISIONS[1]![1](revision))).toBe(`<span title="${id(1)}">${named}</span>`);
    expect(make().show(by({ principal: id(1), role: "publisher" }))).toBe(`${shortened} (publisher)`);
  });

  test("a revocation's author, an approver when it is not known", async () => {
    const { show } = await seeded();
    expect(show(h(RevokedBy, { principal: id(1), reason: "seeded" }))).toBe(`Revoked by <span title="${id(1)}">${named}</span>: seeded`);
    expect(show(h(RevokedBy, { principal: undefined, reason: "seeded" }))).toBe("Revoked by an approver: seeded");
  });

  test("the recovery console's entry and exit", async () => {
    const { show } = await seeded();
    const at = "2026-11-20T04:00:00Z";
    const r = { epoch: "ep_x", recoveryMode: false, scopes: [], entry: { enteredBy: id(1), enteredAt: at, stoppedAt: at, reason: "drill", restored: [] }, exit: { exitedBy: id(1), at, act: "act_x" } };
    // Static markup separates adjacent text nodes with a comment that the DOM does not hold.
    const out = show(h(RecoveryFacts, { r })).replaceAll("<!-- -->", "");
    expect(out).toContain(`by <span title="${id(1)}">${named}</span>: drill`);
    expect(out).toContain(`by <span title="${id(1)}">${named}</span> (act act_x)`);
  });
});
