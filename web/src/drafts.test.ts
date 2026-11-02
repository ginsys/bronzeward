import { createElement as h } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, describe, expect, test, vi } from "vitest";
import type { components } from "./api/v1";
import { change, type Clash, createdId, doneOnce, type Editor, type EditorEvent, editor, EMPTY, FragmentPane, forgetETag, type Held, holdsUnsaved, keepETag, keepOnDone, keptETag, LAYERS, lastGood, markLine, mayDrop, mayRevert, NOT_RENDERED, rows, where } from "./drafts.tsx";
import { leaving, startsNew, unsaved, WritesProvider } from "./forms.tsx";
import { type Load, type Reads, ReadsProvider } from "./list.tsx";
import { references, spotOfLine, spotOfPath, spotOfReference } from "./position.ts";
import { ShellContext } from "./shell.tsx";
import { Write, Writes } from "./write.ts";

type Source = components["schemas"]["SourceEntry"];
const entry = (e: Partial<Source>): Source => ({ kind: "fragment", name: "f", head: "frg_1", base: 3, revision: "frv_2", document: "a: 1\n", ...e });

afterEach(() => {
  vi.unstubAllGlobals();
  unsaved.clear();
});

describe("an entry's change against its base (journeys §5.4)", () => {
  test("removed, new, or changed from its base revision", () => {
    expect(change(entry({ revision: null }))).toBe("removed");
    expect(change(entry({ head: null, base: null }))).toBe("new");
    expect(change(entry({}))).toBe("changed (base revision 3)");
  });
});

describe("the last-seen ETag per draft (§8.3, §8.5 item 1)", () => {
  test("is kept per draft, valid only under the epoch it was seen in, and forgotten", () => {
    keepETag("drf_1", '"6-t"', "e1");
    expect(keptETag("drf_1", "e1")).toBe('"6-t"');
    expect(keptETag("drf_1", "e2")).toBeUndefined();
    expect(keptETag("drf_2", "e1")).toBeUndefined();
    forgetETag("drf_1");
    expect(keptETag("drf_1", "e1")).toBeUndefined();
  });
});

describe("a done write is acted on once", () => {
  // A discard's done effect read the draft again on each render after it, and no read settled.
  test("its outcome is given the first time only, and not before it is done", () => {
    const w = new Write({ method: "POST", path: "/drafts/drf_1/discard", body: {}, resource: "/drafts/drf_1" });
    expect(doneOnce(undefined)).toBeUndefined();
    expect(doneOnce(w)).toBeUndefined();
    w.outcome = { kind: "refused", status: 412, problem: null, replayed: false };
    expect(doneOnce(w)).toBeUndefined();
    const done = { kind: "done", status: 200, data: null, replayed: false, etag: '"7-t"' } as const;
    w.outcome = done;
    expect(doneOnce(w)).toBe(done);
    expect(doneOnce(w)).toBeUndefined();
    const other = new Write(w.spec);
    other.outcome = done;
    expect(doneOnce(other)).toBe(done);
  });
});

describe("[Go to field] positions (B13)", () => {
  const doc = "machine:\n  nodeLabels:\n    example.test/role: worker\n  files:\n    - content: |\n        a: b\n---\ncluster:\n  name: lab\n";
  test("a compilation §2.2 path, pointer escapes included, in the document it names", async () => {
    const label = await spotOfPath(doc, "doc[0]/machine/nodeLabels/example.test~1role");
    expect(doc.slice(label!.start, label!.end)).toBe("worker");
    const second = await spotOfPath(doc, "doc[1]/cluster/name");
    expect(doc.slice(second!.start, second!.end)).toBe("lab");
  });

  test("a path into an embedded document points at the field holding it", async () => {
    const s = await spotOfPath(doc, "doc[0]/machine/files/0/content|yaml/a");
    expect(doc.slice(s!.start, s!.end)).toContain("a: b");
  });

  test("a path that names nothing, or no path, gives no position", async () => {
    expect(await spotOfPath(doc, "doc[0]/machine/missing")).toBeUndefined();
    expect(await spotOfPath(doc, "doc[5]/x")).toBeUndefined();
    expect(await spotOfPath(doc, "machine.files")).toBeUndefined();
  });

  test("a reference by its tag, and a mark by its line", () => {
    const text = "a: !bwref registry/pass-2\nb: !bwref registry/pass\n";
    const s = spotOfReference(text, "registry/pass");
    expect(text.slice(s!.start, s!.end)).toBe("!bwref registry/pass");
    expect(spotOfReference(text, "registry/none")).toBeUndefined();
    const marks = "doc[0]/a\ndoc[0]/b\n";
    const m = spotOfLine(marks, 1);
    expect(marks.slice(m!.start, m!.end)).toBe("doc[0]/b");
    expect(spotOfLine(marks, 9)).toBeUndefined();
  });

  test("the references a document carries, each once, in order", () => {
    expect(references("a: !bwref x/one\nb: !bwref x/two\nc: !bwref x/one\n")).toEqual(["x/one", "x/two"]);
  });

  test("a flow list's reference ends at its comma, and a comment's text is no reference", () => {
    const text = "a: [!bwref ghost, !bwref x/two]\n# !bwref commented\nb: c # !bwref trailing\nd: '# not a comment' # !bwref after-quote\n";
    expect(references(text)).toEqual(["ghost", "x/two"]);
    const s = spotOfReference(text, "ghost");
    expect(text.slice(s!.start, s!.end)).toBe("!bwref ghost");
    expect(spotOfReference(text, "commented")).toBeUndefined();
    expect(spotOfReference(text, "trailing")).toBeUndefined();
    expect(spotOfReference(text, "after-quote")).toBeUndefined();
  });

  test("an apostrophe inside a plain scalar opens no quote, and '' stays inside a single-quoted one", () => {
    const text = "a: can't # !bwref ghost\nb: 'it''s # x' # !bwref ghost\nc: !bwref ghost\n";
    expect(references(text)).toEqual(["ghost"]);
    const s = spotOfReference(text, "ghost");
    expect(s!.start).toBe(text.indexOf("c: ") + 3);
  });

  test("a quote opens no scalar inside a plain one, after a blank either", () => {
    const text = "note: dogs 'n cats # !bwref ghost\nlist: [ 'a # b', \"c\" ] # !bwref ghost\n- 'x # y' # !bwref ghost\nref: !bwref ghost\n";
    const s = spotOfReference(text, "ghost");
    expect(s!.start).toBe(text.indexOf("ref: ") + 5);
  });

  test("a mapping key matches by its text, as the server reads it: 01 is not 1", async () => {
    const text = "m:\n  01: first\n  1: second\n";
    const s = await spotOfPath(text, "doc[0]/m/1");
    expect(text.slice(s!.start, s!.end)).toBe("second");
    const z = await spotOfPath(text, "doc[0]/m/01");
    expect(text.slice(z!.start, z!.end)).toBe("first");
  });

  test("a numeric pointer segment is a list index in a list and a key in a mapping", async () => {
    const text = "ports:\n  \"8080\": web\n  0: zero\nlist:\n  - first\n  - second\n";
    const key = await spotOfPath(text, "doc[0]/ports/8080");
    expect(text.slice(key!.start, key!.end)).toBe("web");
    const zero = await spotOfPath(text, "doc[0]/ports/0");
    expect(text.slice(zero!.start, zero!.end)).toBe("zero");
    const item = await spotOfPath(text, "doc[0]/list/1");
    expect(text.slice(item!.start, item!.end)).toBe("second");
    expect(await spotOfPath(text, "doc[0]/list/x")).toBeUndefined();
  });
});

describe("the unsaved-change guard (§11.4 item 3)", () => {
  test("a move to another page asks while an editor holds unsaved text", () => {
    const ask = vi.fn(() => false);
    vi.stubGlobal("confirm", ask);
    vi.stubGlobal("location", new URL("https://bw.invalid/drafts/drf_1"));
    const writes = new Writes(() => "p");
    expect(leaving(writes, "/machines")).toBe(true);
    unsaved.add("fragment f of draft drf_1");
    expect(leaving(writes, "/drafts/drf_1")).toBe(true);
    expect(leaving(writes, "/machines")).toBe(false);
    expect(ask).toHaveBeenCalledWith("Unsaved text in fragment f of draft drf_1: leaving drops it.\nLeave this page?");
    // A write notice's own way out still asks for unsaved text.
    expect(leaving(writes, "/machines", true)).toBe(false);
    expect(ask).toHaveBeenCalledTimes(2);
  });
});

describe("the editor's text is kept until the operator drops it (#339 round 1)", () => {
  const held = (document: string): Held => ({ ...EMPTY, document });

  test("a switch to another entry asks while an editor holds unsaved text", () => {
    const ask = vi.fn(() => false);
    vi.stubGlobal("confirm", ask);
    expect(mayDrop()).toBe(true);
    expect(ask).not.toHaveBeenCalled();
    unsaved.add("fragment f of draft drf_1");
    expect(mayDrop()).toBe(false);
    expect(ask).toHaveBeenCalledWith("Unsaved text in fragment f of draft drf_1: switching drops it.\nSwitch entry?");
  });

  test("an open conflict keeps the guard, even once the editor shows their text", () => {
    const theirs = held("a: 2\n");
    expect(holdsUnsaved(theirs, theirs, undefined)).toBe(false);
    expect(holdsUnsaved(held("x"), theirs, undefined)).toBe(true);
    expect(holdsUnsaved(theirs, theirs, held("a: mine\n"))).toBe(true);
    expect(holdsUnsaved(theirs, theirs, theirs)).toBe(false);
  });

  test("the references table lists the document's references, then declarations no reference uses", () => {
    expect(rows("a: !bwref x/one\n", { "x/one": { kind: "string", version: 1 }, old: { kind: "string", version: 1 } })).toEqual(["x/one", "old"]);
    expect(rows("a: 1\n", {})).toEqual([]);
  });
});

// The conflict flow's state table (W11 "Conflict", B14): one row per transition. A row named after
// a review finding keeps that failure covered.
describe("the editor's text and a 412's conflict, one transition per row", () => {
  const held = (document: string): Held => ({ ...EMPTY, document });
  const [A, M, M2, T, T2] = ["a: base\n", "a: mine\n", "a: mine\nb: more\n", "a: theirs\n", "a: later\n"].map(held) as [Held, Held, Held, Held, Held];
  const refused = (h: Held, revision?: number): Clash => ({ mine: h, phase: "refused", ...(revision !== undefined && { revision }) });
  const asked = (h: Held, revision = 7): Clash => ({ mine: h, phase: "asked", revision });
  const theirs = (h: Held, revision = 8): Clash => ({ mine: h, phase: "theirs", revision });
  const table: [string, Editor, EditorEvent, Editor][] = [
    ["typing without a conflict changes only the text", { held: A, saved: A }, { kind: "type", held: M }, { held: M, saved: A }],
    ["a 412 opens a conflict holding the editor's text", { held: M, saved: A }, { kind: "refused" }, { held: M, saved: A, conflict: refused(M) }],
    ["a second 412 keeps the open conflict", { held: T, saved: T, conflict: theirs(M) }, { kind: "refused" }, { held: T, saved: T, conflict: theirs(M) }],
    ["the 412's own draft read records the revision refused at", { held: M, saved: A, conflict: refused(M) }, { kind: "at", revision: 7 }, { held: M, saved: A, conflict: refused(M, 7) }],
    ["a later draft read keeps the revision refused at", { held: M, saved: A, conflict: refused(M, 7) }, { kind: "at", revision: 8 }, { held: M, saved: A, conflict: refused(M, 7) }],
    ["R2-F2: text typed after the 412 is the operator's text", { held: M, saved: A, conflict: refused(M, 7) }, { kind: "type", held: M2 }, { held: M2, saved: A, conflict: refused(M2, 7) }],
    ["a draft read before Show their change compares nothing new", { held: M, saved: A, conflict: refused(M, 7) }, { kind: "draft", revision: 8, changed: true }, { held: M, saved: A, conflict: refused(M, 7) }],
    ["R3-F2: a reconnect's revision read during a 412 keeps the typed text", { held: M, saved: A, conflict: refused(M, 7) }, { kind: "read", text: T }, { held: M, saved: T, conflict: refused(M, 7) }],
    ["Show their change asks for their text", { held: M, saved: A, conflict: refused(M, 7) }, { kind: "show" }, { held: M, saved: A, conflict: asked(M) }],
    ["a second Show while asked asks nothing more", { held: M, saved: A, conflict: asked(M) }, { kind: "show" }, { held: M, saved: A, conflict: asked(M) }],
    ["boundary, R4-1: text typed while the asked read is pending is the operator's text", { held: M, saved: A, conflict: asked(M) }, { kind: "type", held: M2 }, { held: M2, saved: A, conflict: asked(M2) }],
    ["boundary, R4-1: Show with no change to this fragment shows its stored text, the operator's aside", { held: M, saved: A, conflict: asked(M) }, { kind: "draft", revision: 8, changed: false }, { held: A, saved: A, conflict: theirs(M) }],
    ["Show with this fragment changed waits for its revision read", { held: M, saved: A, conflict: asked(M) }, { kind: "draft", revision: 8, changed: true }, { held: M, saved: A, conflict: asked(M, 8) }],
    ["the asked revision read shows their text, the operator's aside", { held: M2, saved: A, conflict: asked(M2, 8) }, { kind: "read", text: T }, { held: T, saved: T, conflict: theirs(M2) }],
    ["R3-F1: a Show once their text shows keeps the operator's text", { held: T, saved: T, conflict: theirs(M) }, { kind: "show" }, { held: T, saved: T, conflict: theirs(M) }],
    ["text typed once their text shows leaves the operator's text aside", { held: T, saved: T, conflict: theirs(M) }, { kind: "type", held: T2 }, { held: T2, saved: T, conflict: theirs(M) }],
    ["R1-F5: once shown, the conflict follows the draft revision as read", { held: T, saved: T, conflict: theirs(M) }, { kind: "draft", revision: 9, changed: true }, { held: T, saved: T, conflict: theirs(M, 9) }],
    ["boundary, R4-1: a reconnect's revision read after the show keeps text typed since", { held: M2, saved: T, conflict: theirs(M) }, { kind: "read", text: T2 }, { held: M2, saved: T2, conflict: theirs(M) }],
    ["a revision read after the show, with nothing typed, shows their latest text", { held: T, saved: T, conflict: theirs(M) }, { kind: "read", text: T2 }, { held: T2, saved: T2, conflict: theirs(M) }],
    ["boundary: Copy my text puts the operator's text back and ends the conflict", { held: T2, saved: T, conflict: theirs(M) }, { kind: "copy" }, { held: M, saved: T }],
    ["Copy my text without a conflict changes nothing", { held: M, saved: A }, { kind: "copy" }, { held: M, saved: A }],
    ["R1-F2: a revision read keeps text typed since the last save", { held: M, saved: A }, { kind: "read", text: T }, { held: M, saved: T }],
    ["a revision read replaces text nothing was typed into", { held: A, saved: A }, { kind: "read", text: T }, { held: T, saved: T }],
    ["R2-F1: a done write marks the text it sent saved, not the text typed since", { held: M2, saved: A }, { kind: "done", sent: M }, { held: M2, saved: M }],
    ["a done write ends the conflict: the operator saved the text chosen", { held: T2, saved: T, conflict: theirs(M) }, { kind: "done", sent: T2 }, { held: T2, saved: T2 }],
  ];
  test.each(table)("%s", (_, from, e, to) => {
    expect(editor(from, e)).toEqual(to);
  });

  test("Revert to base waits until their text shows, while the editor's text is the operator's kept text", () => {
    expect(mayRevert(undefined)).toBe(true);
    expect(mayRevert(refused(M, 7))).toBe(false);
    expect(mayRevert(asked(M))).toBe(false);
    expect(mayRevert(theirs(M))).toBe(true);
  });

  const run = (s: Editor, events: EditorEvent[]) => events.reduce(editor, s);
  test("R4-1: after a Show that changed no text of this fragment, a later read keeps what was typed since", () => {
    const s = run({ held: M, saved: A }, [{ kind: "refused" }, { kind: "at", revision: 7 }, { kind: "show" }, { kind: "draft", revision: 8, changed: false }, { kind: "type", held: M2 }, { kind: "draft", revision: 9, changed: true }, { kind: "read", text: T2 }]);
    expect(s).toEqual({ held: M2, saved: T2, conflict: theirs(M, 9) });
  });

  test("R4-1: text typed while the asked read is pending stays the operator's text", () => {
    const s = run({ held: M, saved: A }, [{ kind: "refused" }, { kind: "show" }, { kind: "type", held: M2 }, { kind: "draft", revision: 8, changed: true }, { kind: "read", text: T }]);
    expect(s.held).toBe(T);
    expect(s.conflict?.mine).toBe(M2);
    expect(holdsUnsaved(s.held, s.saved, s.conflict?.mine)).toBe(true);
  });
});

describe("a draft read again without data (#339 round 4)", () => {
  const ok = (revision: number): Load<{ revision: number }> => ({ kind: "ok", data: { revision } });
  const failed: Load<{ revision: number }> = { kind: "unavailable", status: 503, problem: null };

  test("R4-2: keeps the last draft read for the same id, so the editor and its unsaved text stay", () => {
    expect(lastGood(undefined, "drf_1", ok(1))).toEqual({ id: "drf_1", data: { revision: 1 } });
    expect(lastGood({ id: "drf_1", data: { revision: 1 } }, "drf_1", failed)).toEqual({ id: "drf_1", data: { revision: 1 } });
    expect(lastGood({ id: "drf_1", data: { revision: 1 } }, "drf_2", failed)).toBeUndefined();
    expect(lastGood({ id: "drf_1", data: { revision: 1 } }, "drf_2", { kind: "loading" })).toBeUndefined();
  });
});

describe("a Save that sends nothing new (§8.4 item 1)", () => {
  test("starts a new write only once the form's last write is no longer open", async () => {
    const writes = new Writes(() => "p");
    expect(startsNew(writes, undefined)).toBe(true);
    const w = new Write({ method: "PUT", path: "/x", body: {}, resource: "/x" });
    // An unknown outcome keeps the write open: a second Save sends nothing, so its text is not the one sent.
    w.outcome = { kind: "unknown", why: "w", problem: null };
    (writes as unknown as { all: Set<Write> }).all.add(w);
    expect(startsNew(writes, w)).toBe(false);
    w.outcome = { kind: "done", status: 200, data: {}, replayed: false };
    expect(startsNew(writes, w)).toBe(true);
  });
});

describe("a write's answer, whether or not its view is still shown (§8.3)", () => {
  test("a done write's ETag is kept for its draft under the epoch it was sent in, while that epoch is current", () => {
    let epoch = "e1";
    const sentIn = (e: string) => Object.assign(new Write({ method: "PUT", path: "/x", body: {}, resource: "/x" }), { epoch: e });
    const settle = keepOnDone("drf_9", () => epoch);
    settle({ kind: "refused", status: 412, problem: null, replayed: false }, sentIn("e1"));
    expect(keptETag("drf_9", "e1")).toBeUndefined();
    settle({ kind: "done", status: 200, data: {}, replayed: false, etag: '"8-t"' }, sentIn("e1"));
    expect(keptETag("drf_9", "e1")).toBe('"8-t"');
    // An answer to a write sent in e1 that settles once e2 is current keeps nothing: a tag read
    // under e2 stays.
    epoch = "e2";
    keepETag("drf_9", '"9-t"', "e2");
    settle({ kind: "done", status: 200, data: {}, replayed: false, etag: '"8-u"' }, sentIn("e1"));
    expect(keptETag("drf_9", "e2")).toBe('"9-t"');
    keepOnDone((o) => createdId(o.kind === "done" ? o.data : null), () => epoch)({ kind: "done", status: 201, data: { id: "drf_10" }, replayed: false, etag: '"1-t"' }, sentIn("e2"));
    expect(keptETag("drf_10", "e2")).toBe('"1-t"');
  });

  test("a created draft's id is read only from an object that carries one", () => {
    expect(createdId({ id: "drf_1" })).toBe("drf_1");
    for (const d of [null, "drf_1", {}, { id: 3 }]) expect(createdId(d)).toBeUndefined();
    // A done answer with no readable draft: the keep hook keeps nothing and throws nothing.
    const w = Object.assign(new Write({ method: "POST", path: "/drafts", body: {}, resource: "/changes" }), { epoch: "e1" });
    expect(() => keepOnDone((o) => createdId(o.kind === "done" ? o.data : null), () => "e1")({ kind: "done", status: 201, data: null, replayed: false, etag: '"1-t"' }, w)).not.toThrow();
  });
});

describe("a 422 names where it points (W11 Refused)", () => {
  const marks = "doc[0]/a\n\ndoc[0]/b\n";
  test("the reference, the mark's line, or the paths", () => {
    const p = (extra: Record<string, unknown>) => ({ type: "urn:bronzeward:problem:validation-failed", title: "t", status: 422, ...extra });
    expect(where(p({ reference: "ghost" }), marks)).toBe("Reference ghost.");
    // The position counts the marks sent, which leave out blank lines.
    expect(where(p({ position: 1 }), marks)).toBe("Mark on line 3.");
    expect(where(p({ paths: ["doc[0]/a", "doc[0]/b"] }), marks)).toBe("At doc[0]/a, doc[0]/b.");
    expect(where(p({}), marks)).toBeUndefined();
    expect(where(null, marks)).toBeUndefined();
  });

  test("a mark's position is the line of the n-th mark sent", () => {
    expect(markLine(marks, 0)).toBe(0);
    expect(markLine(marks, 1)).toBe(2);
    expect(markLine(marks, 2)).toBeUndefined();
  });
});

describe("the fragment entry (W4, §11.4 item 2)", () => {
  const reads: Reads = { client: { read: () => new Promise(() => {}) }, settle: () => true, undropped: () => () => true, view: "", again: 0, regained: 0, go: () => {}, failure: () => null };
  const draft = { id: "drf_1", cluster: "cl_1", title: "t", state: "open", revision: 6, entries: [] };
  const pane = (state = "open") =>
    renderToStaticMarkup(h(WritesProvider, { value: new Writes(() => "p") }, h(ShellContext, { value: { roles: ["author"], offline: false } },
      h(ReadsProvider, { value: reads }, h(FragmentPane, { draft: { ...draft, state }, entry: undefined, written: () => {}, seeChange: () => {} })))));

  test("the document and marks fields give the browser's spelling and fill services nothing", () => {
    const out = pane();
    const areas = out.match(/<textarea[^>]*>/g)!;
    expect(areas).toHaveLength(2);
    // HTML attribute names are case-insensitive; React writes them as typed.
    for (const a of areas.map((x) => x.toLowerCase())) for (const attr of ['autocomplete="off"', 'spellcheck="false"', 'autocorrect="off"', 'autocapitalize="off"']) expect(a).toContain(attr);
  });

  test("offers the six layers in composition order, Save and Revert to base", () => {
    const out = pane();
    expect([...out.matchAll(/<option[^>]*>(\w+)<\/option>/g)].map((m) => m[1]).slice(0, 6)).toEqual([...LAYERS]);
    expect(out).toContain(">Save</button>");
    expect(out).toContain(">Revert to base</button>");
  });

  test("a draft that is not open is read only", () => {
    const out = pane("discarded");
    expect(out).not.toContain(">Save</button>");
    for (const a of out.match(/<textarea[^>]*>/g)!) expect(a).toContain("readOnly");
  });

  test("the footer text names gap G5", () => {
    expect(NOT_RENDERED).toBe("Not rendered yet. A machine's configuration exists only after publication (gap G5).");
  });
});
