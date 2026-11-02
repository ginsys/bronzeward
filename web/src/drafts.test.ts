import { readFileSync } from "node:fs";
import { createElement as h } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, describe, expect, test, vi } from "vitest";
import type { components } from "./api/v1";
import { change, type Clash, createdId, declaredOf, DiscardRefused, NOT_DISCARDED, doneOnce, type Editor, type EditorEvent, editor, EMPTY, FragmentPane, forgetETag, type Held, holdsUnsaved, inUse, keepETag, keepOnDone, keptETag, LAYERS, lastGood, markLine, mayDrop, mayRevert, maySend, NOT_RENDERED, NOT_REVERTED, NOT_SENT_CONFLICT, NOT_SENT_UNREAD, NOT_SENT_UNSHOWN, paneEntry, rows, selection, sendRefusal, settledFragment, spotOfMark, unread, where } from "./drafts.tsx";
import { leaving, signingOut, startsNew, unsaved, WritesProvider } from "./forms.tsx";
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

  test("ginsys/bronzeward#517 H16: a quoted name, and a name on the line after its tag, are references", () => {
    const text = "a: !bwref \"x/dq\"\nb: !bwref 'x/sq'\nc: !bwref\n  x/next\nd: !bwref\n  \"x/next-q\"\ne: !bwref \"x/open\n";
    expect(references(text)).toEqual(["x/dq", "x/sq", "x/next", "x/next-q"]);
    const s = spotOfReference(text, "x/next");
    expect(text.slice(s!.start, s!.end)).toBe("!bwref\n  x/next");
    // A comment between the tag and the next line hides nothing it does not hold.
    expect(references("a: !bwref # x/not\n  x/after\n")).toEqual(["x/after"]);
    // A tag with no value, then a key on the next line: the key is no name.
    expect(references("a: !bwref\nb: x\nc: !bwref \"k\": v\n")).toEqual([]);
    // #517 review: the whole scalar is read, so a key with characters outside the grammar is no
    // name either, nor is a scalar the grammar refuses; a quoted name is decoded.
    expect(references("a: !bwref\nb_key: value\n")).toEqual([]);
    expect(references("a: !bwref foo_bar\nb: !bwref 'it''s'\n")).toEqual([]);
    const escaped = 'a: !bwref "x/\\u0066oo"\n';
    expect(references(escaped)).toEqual(["x/foo"]);
    const e = spotOfReference(escaped, "x/foo");
    expect(escaped.slice(e!.start, e!.end)).toBe('!bwref "x/\\u0066oo"');
  });

  test("#517 review round 2: YAML escapes decode, and a scalar with a blank or the grammar's bad segments names nothing", () => {
    expect(references('a: !bwref "x/\\x66oo"\nb: !bwref "x/\\U00000066oo"\n')).toEqual(["x/foo"]);
    expect(references('a: !bwref "x/\\qoo"\nb: !bwref "x/\\x6"\n')).toEqual([]);
    expect(references("a: !bwref x/foo bar\n")).toEqual([]);
    // A plain scalar folded onto a more indented line is one scalar, with a blank: no name.
    expect(references("a: !bwref x/foo\n  bar\nb: !bwref x/two\n")).toEqual(["x/two"]);
    // A more indented key or sequence entry is no continuation.
    expect(references("a: !bwref x/one\n  b: c\n")).toEqual(["x/one"]);
    expect(references("- !bwref x/one\n  - c\n")).toEqual(["x/one"]);
    for (const bad of ["x/foo-", "x//foo", "-x", "x/", "X/foo"]) expect(references(`a: !bwref ${bad}\n`)).toEqual([]);
    // A flow list closed on the next line still ends its name at the bracket.
    expect(references("a: [!bwref x/one,\n   !bwref x/two\n   ]\n")).toEqual(["x/one", "x/two"]);
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

  test("ginsys/bronzeward#517 H10: Sign out asks while an editor holds unsaved text", () => {
    const ask = vi.fn(() => false);
    vi.stubGlobal("confirm", ask);
    expect(signingOut()).toBe(true);
    expect(ask).not.toHaveBeenCalled();
    unsaved.add("fragment f of draft drf_1");
    expect(signingOut()).toBe(false);
    expect(ask).toHaveBeenCalledWith("Unsaved text in fragment f of draft drf_1: signing out drops it.\nSign out?");
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

  test("ginsys/bronzeward#517 H17: a reference named like an object's own property is undeclared until declared", () => {
    expect(declaredOf({}, "constructor")).toBeUndefined();
    expect(declaredOf({}, "tostring")).toBeUndefined();
    expect(declaredOf({ constructor: { kind: "string" as const, version: 1 } }, "constructor")).toEqual({ kind: "string", version: 1 });
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
  const awaiting = (h: Held, revision = 8): Clash => ({ mine: h, phase: "asked", revision, awaits: true });
  const theirs = (h: Held, revision = 8): Clash => ({ mine: h, phase: "theirs", revision });
  const sentOf = (text: Held, whileTheirs = false) => ({ text, whileTheirs });
  const table: [string, Editor, EditorEvent, Editor][] = [
    ["typing without a conflict changes only the text", { held: A, saved: A }, { kind: "type", held: M }, { held: M, saved: A }],
    ["a 412 opens a conflict holding the editor's text", { held: M, saved: A }, { kind: "refused" }, { held: M, saved: A, conflict: refused(M) }],
    ["a second 412 before their text shows keeps the open conflict", { held: M, saved: A, conflict: refused(M, 7) }, { kind: "refused" }, { held: M, saved: A, conflict: refused(M, 7) }],
    ["H6: a 412 once their text shows opens a new conflict holding the editor's text, not the older one", { held: T2, saved: T, conflict: theirs(M), sent: sentOf(T2, true) }, { kind: "refused" }, { held: T2, saved: T, conflict: refused(T2), sent: sentOf(T2, true) }],
    ["the 412's own draft read records the revision refused at", { held: M, saved: A, conflict: refused(M) }, { kind: "at", revision: 7 }, { held: M, saved: A, conflict: refused(M, 7) }],
    ["a later draft read keeps the revision refused at", { held: M, saved: A, conflict: refused(M, 7) }, { kind: "at", revision: 8 }, { held: M, saved: A, conflict: refused(M, 7) }],
    ["R2-F2: text typed after the 412 is the operator's text", { held: M, saved: A, conflict: refused(M, 7) }, { kind: "type", held: M2 }, { held: M2, saved: A, conflict: refused(M2, 7) }],
    ["a draft read before Show their change compares nothing new", { held: M, saved: A, conflict: refused(M, 7) }, { kind: "draft", revision: 8, changed: true }, { held: M, saved: A, conflict: refused(M, 7) }],
    ["R3-F2: a reconnect's revision read during a 412 keeps the typed text", { held: M, saved: A, conflict: refused(M, 7) }, { kind: "read", text: T, since: 0 }, { held: M, saved: T, conflict: refused(M, 7) }],
    ["Show their change asks for their text", { held: M, saved: A, conflict: refused(M, 7) }, { kind: "show" }, { held: M, saved: A, conflict: asked(M) }],
    ["a second Show while asked asks nothing more", { held: M, saved: A, conflict: asked(M) }, { kind: "show" }, { held: M, saved: A, conflict: asked(M) }],
    ["boundary, R4-1: text typed while the asked read is pending is the operator's text", { held: M, saved: A, conflict: asked(M) }, { kind: "type", held: M2 }, { held: M2, saved: A, conflict: asked(M2) }],
    ["boundary, R4-1: Show with no change to this fragment shows its stored text, the operator's aside", { held: M, saved: A, conflict: asked(M) }, { kind: "draft", revision: 8, changed: false }, { held: A, saved: A, conflict: theirs(M) }],
    ["Show with this fragment changed waits for its revision read", { held: M, saved: A, conflict: asked(M) }, { kind: "draft", revision: 8, changed: true }, { held: M, saved: A, conflict: awaiting(M) }],
    ["H7: Show with this fragment removed shows the removal, the operator's text aside", { held: M, saved: A, conflict: asked(M) }, { kind: "draft", revision: 8, changed: false, removed: true }, { held: EMPTY, saved: EMPTY, conflict: theirs(M) }],
    ["H7: a removal while a revision read is awaited shows the removal and ends the wait", { held: M, saved: A, conflict: awaiting(M) }, { kind: "draft", revision: 9, changed: false, removed: true }, { held: EMPTY, saved: EMPTY, conflict: theirs(M, 9) }],
    ["L1: a draft read with no change while a revision read is awaited keeps waiting", { held: A, saved: A, conflict: awaiting(A) }, { kind: "draft", revision: 8, changed: false }, { held: A, saved: A, conflict: awaiting(A) }],
    ["the asked revision read shows their text, the operator's aside", { held: M2, saved: A, conflict: asked(M2, 8) }, { kind: "read", text: T, since: 0 }, { held: T, saved: T, conflict: theirs(M2) }],
    ["the awaited revision read shows their text and ends the wait", { held: M2, saved: A, conflict: awaiting(M2) }, { kind: "read", text: T, since: 0 }, { held: T, saved: T, conflict: theirs(M2) }],
    ["R3-F1: a Show once their text shows keeps the operator's text", { held: T, saved: T, conflict: theirs(M) }, { kind: "show" }, { held: T, saved: T, conflict: theirs(M) }],
    ["text typed once their text shows leaves the operator's text aside", { held: T, saved: T, conflict: theirs(M) }, { kind: "type", held: T2 }, { held: T2, saved: T, conflict: theirs(M) }],
    ["R1-F5: once shown, the conflict follows the draft revision as read", { held: T, saved: T, conflict: theirs(M) }, { kind: "draft", revision: 9, changed: true }, { held: T, saved: T, conflict: theirs(M, 9) }],
    ["boundary, R4-1: a reconnect's revision read after the show keeps text typed since", { held: M2, saved: T, conflict: theirs(M) }, { kind: "read", text: T2, since: 0 }, { held: M2, saved: T2, conflict: theirs(M) }],
    ["a revision read after the show, with nothing typed, shows their latest text", { held: T, saved: T, conflict: theirs(M) }, { kind: "read", text: T2, since: 0 }, { held: T2, saved: T2, conflict: theirs(M) }],
    ["boundary: Copy my text puts the operator's text back and ends the conflict", { held: T2, saved: T, conflict: theirs(M) }, { kind: "copy" }, { held: M, saved: T }],
    ["R7-1: Copy my text before their text shows changes nothing", { held: M, saved: A, conflict: asked(M, 8) }, { kind: "copy" }, { held: M, saved: A, conflict: asked(M, 8) }],
    ["Copy my text without a conflict changes nothing", { held: M, saved: A }, { kind: "copy" }, { held: M, saved: A }],
    ["R1-F2: a revision read keeps text typed since the last save", { held: M, saved: A }, { kind: "read", text: T, since: 0 }, { held: M, saved: T }],
    ["a revision read replaces text nothing was typed into", { held: A, saved: A }, { kind: "read", text: T, since: 0 }, { held: T, saved: T }],
    // Save, sweep (a): the operator types while it is in flight.
    ["a Save records the text it sends, and whether their text shows", { held: M, saved: A, conflict: theirs(M2) }, { kind: "send" }, { held: M, saved: A, conflict: theirs(M2), sent: sentOf(M, true) }],
    ["H8: a Remove records that it removes the text", { held: M, saved: A }, { kind: "send", removes: true }, { held: M, saved: A, sent: { text: M, whileTheirs: false, removes: true } }],
    ["H8: a done Remove saves the removal, so the text still shown stays unsaved", { held: M, saved: A, sent: { text: M, whileTheirs: false, removes: true } }, { kind: "done" }, { held: M, saved: EMPTY, sent: { text: M, whileTheirs: false, removes: true }, writes: 1 }],
    ["R2-F1: a done write marks the text it sent saved, not the text typed since", { held: M2, saved: A, sent: sentOf(M) }, { kind: "done" }, { held: M2, saved: M, sent: sentOf(M), writes: 1 }],
    ["a Save sent while their text shows ends the conflict when done: the operator chose that text", { held: T2, saved: T, conflict: theirs(M), sent: sentOf(T2, true) }, { kind: "done" }, { held: T2, saved: T2, sent: sentOf(T2, true), writes: 1 }],
    ["a Save sent before their text shows ends the conflict when done, if it sent the kept text", { held: T, saved: T, conflict: theirs(M), sent: sentOf(M) }, { kind: "done" }, { held: T, saved: M, sent: sentOf(M), writes: 1 }],
    ["R5-1: a Save sent before their text shows keeps the conflict when done, if text was typed after it", { held: T, saved: T, conflict: theirs(M2), sent: sentOf(M) }, { kind: "done" }, { held: T, saved: M, conflict: theirs(M2), sent: sentOf(M), writes: 1 }],
    // Revision read, sweep (b): an answer older than a done write.
    ["R5-4: a revision read started before a done write is dropped", { held: M, saved: M, writes: 1 }, { kind: "read", text: A, since: 0 }, { held: M, saved: M, writes: 1 }],
    ["a revision read started after the last done write applies", { held: M, saved: M, writes: 1 }, { kind: "read", text: T, since: 1 }, { held: T, saved: T, writes: 1 }],
    // Revert to base, sweep (a) and (b).
    ["Revert to base puts the base text in the editor when nothing changed since the click", { held: M, saved: A }, { kind: "revert", at: 0, text: A, note: "n" }, { held: A, saved: A, note: "n" }],
    ["R5-2: text typed while Revert reads the base text is kept", { held: M2, saved: A, version: 1 }, { kind: "revert", at: 0, text: A, note: "n" }, { held: M2, saved: A, version: 1, note: NOT_REVERTED }],
    ["R5-2: a 412 while Revert reads the base text keeps the operator's text", { held: M, saved: A, conflict: refused(M, 7), version: 1 }, { kind: "revert", at: 0, text: A, note: "n" }, { held: M, saved: A, conflict: refused(M, 7), version: 1, note: NOT_REVERTED }],
    ["an older Revert's answer after a newer one's is not applied", { held: A, saved: T, version: 2 }, { kind: "revert", at: 1, text: T2, note: "n" }, { held: A, saved: T, version: 2, note: NOT_REVERTED }],
    ["Revert to base while their text shows replaces the editor's text, the operator's still aside", { held: T, saved: T, conflict: theirs(M) }, { kind: "revert", at: 0, text: A, note: "n" }, { held: A, saved: T, conflict: theirs(M), note: "n" }],
    // Draft read and Show, sweep (b): an older draft revision after a newer one.
    ["an older draft revision read after a newer one changes nothing", { held: T, saved: T, conflict: theirs(M, 9) }, { kind: "draft", revision: 8, changed: false }, { held: T, saved: T, conflict: theirs(M, 9) }],
    ["an older draft revision while asked shows nothing", { held: M, saved: A, conflict: asked(M, 9) }, { kind: "draft", revision: 8, changed: false }, { held: M, saved: A, conflict: asked(M, 9) }],
    // The 412's own draft read, sweep (a).
    ["text typed while the 412's draft read is in flight stays when it answers", { held: M2, saved: A, conflict: refused(M2) }, { kind: "at", revision: 7 }, { held: M2, saved: A, conflict: refused(M2, 7) }],
    ["a note replaces the editor's note", { held: A, saved: A, note: "x" }, { kind: "note", text: "y" }, { held: A, saved: A, note: "y" }],
  ];
  // `version` has its own rows below; these compare everything else.
  const rest = ({ version: _, ...r }: Editor) => r;
  test.each(table)("%s", (_, from, e, to) => {
    expect(rest(editor(from, e))).toEqual(rest(to));
  });

  // R6-1: every event that changes the editor's text or the conflict raises `version`, and no other
  // does. `editor` raises it after each event, so a new event kind cannot skip it.
  const shown = (s: Editor) => JSON.stringify([s.held, s.conflict?.phase, s.conflict?.mine]);
  test.each(table)("R6-1, version: %s", (_, from, e) => {
    const to = editor(from, e);
    expect(to.version ?? 0).toBe((from.version ?? 0) + (shown(to) === shown(from) ? 0 : 1));
  });

  // R6-1: a Revert to base pending while another event changes the text or the conflict is not applied.
  const pending: [string, Editor, EditorEvent][] = [
    ["Copy my text", { held: T, saved: T, conflict: theirs(M) }, { kind: "copy" }],
    ["Show their change", { held: M, saved: A, conflict: refused(M, 7) }, { kind: "show" }],
    ["a 412", { held: M, saved: A }, { kind: "refused" }],
    ["a revision read", { held: A, saved: A }, { kind: "read", text: T, since: 0 }],
  ];
  test.each(pending)("R6-1: %s while Revert to base is pending keeps the editor's text", (_, from, e) => {
    const between = editor(from, e);
    const late = editor(between, { kind: "revert", at: from.version ?? 0, text: T2, note: "n" });
    expect(late.held).toEqual(between.held);
    expect(late.note).toBe(NOT_REVERTED);
  });

  test("Revert to base waits until their text shows, while the editor's text is the operator's kept text", () => {
    expect(mayRevert(undefined)).toBe(true);
    expect(mayRevert(refused(M, 7))).toBe(false);
    expect(mayRevert(asked(M))).toBe(false);
    expect(mayRevert(theirs(M))).toBe(true);
  });

  const run = (s: Editor, events: EditorEvent[]) => events.reduce(editor, s);
  test("R4-1: after a Show that changed no text of this fragment, a later read keeps what was typed since", () => {
    const s = run({ held: M, saved: A }, [{ kind: "refused" }, { kind: "at", revision: 7 }, { kind: "show" }, { kind: "draft", revision: 8, changed: false }, { kind: "type", held: M2 }, { kind: "draft", revision: 9, changed: true }, { kind: "read", text: T2, since: 0 }]);
    expect(rest(s)).toEqual({ held: M2, saved: T2, conflict: theirs(M, 9) });
  });

  test("R5-1: a Save sent while asked, then typing, then their text: the done Save keeps the typed text", () => {
    const s = run({ held: M, saved: A }, [{ kind: "refused" }, { kind: "show" }, { kind: "draft", revision: 8, changed: true }, { kind: "send" }, { kind: "type", held: M2 }, { kind: "read", text: T, since: 0 }, { kind: "done" }]);
    expect(s.conflict?.mine).toBe(M2);
    expect(s.saved).toBe(M);
    expect(holdsUnsaved(s.held, s.saved, s.conflict?.mine)).toBe(true);
  });

  test("R4-1: text typed while the asked read is pending stays the operator's text", () => {
    const s = run({ held: M, saved: A }, [{ kind: "refused" }, { kind: "show" }, { kind: "type", held: M2 }, { kind: "draft", revision: 8, changed: true }, { kind: "read", text: T, since: 0 }]);
    expect(s.held).toBe(T);
    expect(s.conflict?.mine).toBe(M2);
    expect(holdsUnsaved(s.held, s.saved, s.conflict?.mine)).toBe(true);
  });

  test("R7-1: Copy my text before the asked revision read answers changes nothing, so that read cannot undo it", () => {
    const s = run({ held: A, saved: A }, [{ kind: "refused" }, { kind: "at", revision: 7 }, { kind: "show" }, { kind: "draft", revision: 8, changed: true }, { kind: "copy" }, { kind: "read", text: T, since: 0 }]);
    expect(rest(s)).toEqual({ held: T, saved: T, conflict: theirs(A) });
    expect(rest(editor(s, { kind: "copy" }))).toEqual({ held: A, saved: T });
    expect(holdsUnsaved(A, T, undefined)).toBe(true);
  });

  test("L1: a second Show their change while the revision read is pending does not lose the copied text", () => {
    // The kept text equals the saved text, as after a Save of unchanged text.
    const s = run({ held: A, saved: A }, [{ kind: "refused" }, { kind: "at", revision: 7 }, { kind: "show" }, { kind: "draft", revision: 8, changed: true }, { kind: "show" }, { kind: "draft", revision: 8, changed: false }, { kind: "copy" }, { kind: "read", text: T, since: 0 }]);
    expect(rest(s)).toEqual({ held: T, saved: T, conflict: theirs(A) });
    const copied = editor(s, { kind: "copy" });
    expect(copied.held).toBe(A);
    expect(holdsUnsaved(copied.held, copied.saved, copied.conflict?.mine)).toBe(true);
  });

  test("#517 review: a revision read started while refused still makes Show wait for it, so Copy my text is not undone", () => {
    // The draft read that named the new revision came while the conflict was refused, so it set nothing.
    const s = run({ held: A, saved: A }, [{ kind: "refused" }, { kind: "at", revision: 7 }, { kind: "draft", revision: 8, changed: true }, { kind: "reading", on: true }, { kind: "show" }, { kind: "draft", revision: 8, changed: false }]);
    expect(s.conflict?.phase).toBe("asked");
    expect(editor(s, { kind: "copy" }).held).toBe(A);
    const shown = run(s, [{ kind: "read", text: T, since: 0 }]);
    expect(rest(shown)).toEqual({ held: T, saved: T, conflict: theirs(A, 8) });
    expect(holdsUnsaved(editor(shown, { kind: "copy" }).held, T, undefined)).toBe(true);
    // A read that answered before Show already put their text in `saved`: Show shows it at once.
    const early = run({ held: A, saved: A }, [{ kind: "refused" }, { kind: "reading", on: true }, { kind: "read", text: T, since: 0 }, { kind: "show" }, { kind: "draft", revision: 8, changed: false }]);
    expect(rest(early)).toEqual({ held: T, saved: T, conflict: theirs(A, 8) });
    // A read dropped for another entry's read waits no more.
    expect(run({ held: A, saved: A }, [{ kind: "reading", on: true }, { kind: "reading", on: false }]).reading).toBeUndefined();
  });

  test("#517 review round 2: the components keep the wiring the repairs rely on", () => {
    // The unit suite has no DOM; these guard the lines the reducer and helper rows above assume.
    const src = readFileSync(new URL("./drafts.tsx", import.meta.url), "utf8");
    expect(src).toMatch(/<FragmentPane [^>]*shown=\{!unshown\}/);
    expect(src).toMatch(/const at = selection\(spot\);\n\s*into\.setSelectionRange\(at\.start, at\.end\);/);
    // A failed revision read keeps `reading`: only the cleanup drops it.
    const failure = /if \(r\?\.kind !== "ok"\) \{([^}]*)\}/.exec(src);
    expect(failure?.[1]).toMatch(/setFailed\(true\)/);
    expect(failure?.[1]).not.toMatch(/reading/);
    expect(src).toMatch(/const waits = unread\(entry\?\.revision, loaded, done\);/);
    expect(src).toMatch(/\{unshown && item\.kind !== "ok" && item\.kind !== "loading" && <p><button [^>]*onClick=\{\(\) => reread\(\(x\) => x \+ 1\)\}>\{READ_DRAFT_AGAIN\}/);
  });

  test("#517 review round 2: a revision read that failed before Show keeps Show waiting, so the old text is never theirs", () => {
    // The failed read sends no event: `reading` stays until a read answers or the pane drops it.
    const failed = run({ held: A, saved: A }, [{ kind: "refused" }, { kind: "at", revision: 7 }, { kind: "draft", revision: 8, changed: true }, { kind: "reading", on: true }]);
    const s = run(failed, [{ kind: "show" }, { kind: "draft", revision: 8, changed: false }]);
    expect(s.conflict?.phase).toBe("asked");
    expect(s.conflict?.awaits).toBe(true);
    expect(maySend(s.conflict)).toBe(false);
    // The retried read answers: their text shows, and Copy my text gives back the operator's.
    const shown = run(s, [{ kind: "read", text: T, since: 0 }]);
    expect(rest(shown)).toEqual({ held: T, saved: T, conflict: theirs(A, 8) });
    expect(shown.reading).toBeUndefined();
  });

  test("#517 review: Save and Remove send nothing until a settled write's draft read shows", () => {
    expect(sendRefusal(false, undefined, true)).toBeUndefined();
    expect(sendRefusal(false, undefined, false)).toBe(NOT_SENT_UNSHOWN);
    expect(sendRefusal(true, undefined, false)).toBe(NOT_SENT_UNREAD);
    expect(sendRefusal(false, refused(M, 7), true)).toBe(NOT_SENT_CONFLICT);
  });

  test("H5: Save and Remove wait until their text shows", () => {
    expect(maySend(undefined)).toBe(true);
    expect(maySend(refused(M, 7))).toBe(false);
    expect(maySend(asked(M))).toBe(false);
    expect(maySend(awaiting(M))).toBe(false);
    expect(maySend(theirs(M))).toBe(true);
  });

  test("H6: Copy my text after a second 412 puts back the text that 412 refused", () => {
    const s = run({ held: M, saved: A }, [{ kind: "send" }, { kind: "refused" }, { kind: "at", revision: 7 }, { kind: "show" }, { kind: "draft", revision: 8, changed: true }, { kind: "read", text: T, since: 0 }, { kind: "type", held: T2 }, { kind: "send" }, { kind: "refused" }, { kind: "at", revision: 9 }, { kind: "show" }, { kind: "draft", revision: 10, changed: true }, { kind: "read", text: A, since: 0 }, { kind: "copy" }]);
    expect(rest(s).held).toBe(T2);
  });

  test("H9: the pane takes no edit until the entry's revision is read, or a write of its own is done", () => {
    expect(unread("frv_2", undefined, 0)).toBe(true);
    expect(unread("frv_2", "frv_2", 0)).toBe(false);
    expect(unread("frv_2", undefined, 1)).toBe(false);
    expect(unread(null, undefined, 0)).toBe(false);
    expect(unread(undefined, undefined, 0)).toBe(false);
    // #517 review round 2: a refreshed draft that names a newer revision than the one the pane
    // read waits for that one, though an earlier read succeeded.
    expect(unread("frv_3", "frv_2", 0)).toBe(true);
  });

  test("R6-1: a Revert clicked before a 412, Show their change and Copy my text does not replace the copied text", () => {
    const s = run({ held: M, saved: A }, [{ kind: "send" }, { kind: "refused" }, { kind: "at", revision: 7 }, { kind: "show" }, { kind: "draft", revision: 8, changed: true }, { kind: "read", text: T, since: 0 }, { kind: "copy" }, { kind: "revert", at: 0, text: A, note: "n" }]);
    expect(s.held).toBe(M);
    expect(s.note).toBe(NOT_REVERTED);
  });
});

test("ginsys/bronzeward#517 H15: a Discard's 412 says the draft changed and offers to show it", () => {
  const markup = renderToStaticMarkup(h(DiscardRefused, { onShow: () => {} }));
  expect(markup).toContain(NOT_DISCARDED);
  expect(markup).toContain(">Show their change</button>");
});

test("ginsys/bronzeward#517 H11: the editor reads nothing past the shell's settle", () => {
  expect(readFileSync(new URL("./drafts.tsx", import.meta.url), "utf8")).not.toMatch(/client\.read/);
});

describe("a new fragment and the entry it creates (ginsys/bronzeward#517 H1, H4)", () => {
  const foo = entry({ name: "foo", head: null, base: null });
  const bar = entry({ name: "bar" });

  test("H1: a new fragment's pane edits the entry its Save named, once the draft read shows it", () => {
    expect(paneEntry([bar], "fragment:", undefined)).toBeUndefined();
    expect(paneEntry([bar], "fragment:", "foo")).toBeUndefined();
    expect(paneEntry([bar, foo], "fragment:", "foo")).toBe(foo);
    // A selected entry is that entry, whatever a new pane named before.
    expect(paneEntry([bar, foo], "fragment:bar", "foo")).toBe(bar);
  });

  test("H4: Add fragment refuses a name the draft already holds, a removed entry included", () => {
    const draft = { entries: [bar, entry({ name: "gone", revision: null }), { kind: "import-base", machine: "foo", revision: "imp_1" }] } as Parameters<typeof inUse>[0];
    expect(inUse(draft, "bar")).toBe(true);
    expect(inUse(draft, "gone")).toBe(true);
    expect(inUse(draft, "foo")).toBe(false);
    expect(inUse(draft, "baz")).toBe(false);
  });
});

describe("a fragment write that settles after its pane is gone (ginsys/bronzeward#517 H3)", () => {
  test("its done answer keeps the ETag and reads the draft again; any other answer reads nothing", () => {
    const written = vi.fn();
    const settle = settledFragment("drf_11", () => "e1", written);
    const w = Object.assign(new Write({ method: "PUT", path: "/x", body: {}, resource: "/x" }), { epoch: "e1" });
    settle({ kind: "refused", status: 412, problem: null, replayed: false }, w);
    expect(written).not.toHaveBeenCalled();
    settle({ kind: "done", status: 200, data: {}, replayed: false, etag: '"3-t"' }, w);
    expect(keptETag("drf_11", "e1")).toBe('"3-t"');
    expect(written).toHaveBeenCalledTimes(1);
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

  test("ginsys/bronzeward#517 H18: Go to field finds the mark sent in the marks now shown", () => {
    const sent = "doc[0]/a\n\ndoc[0]/b\ndoc[0]/c";
    const line = (text: string, s: { start: number; end: number } | undefined) => (s === undefined ? undefined : text.slice(s.start, s.end));
    expect(line(sent, spotOfMark(sent, sent, 1))).toBe("doc[0]/b");
    const edited = "doc[0]/new\ndoc[0]/a\n\ndoc[0]/b\ndoc[0]/c";
    expect(line(edited, spotOfMark(sent, edited, 1))).toBe("doc[0]/b");
    expect(spotOfMark(sent, "doc[0]/a\n\ndoc[0]/x", 1)).toBeUndefined();
    expect(spotOfMark(sent, sent, 5)).toBeUndefined();
    // #517 review: no spot selects nothing, so no earlier selection stays.
    expect(selection(undefined)).toEqual({ start: 0, end: 0 });
    expect(selection({ start: 3, end: 9 })).toEqual({ start: 3, end: 9 });
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
