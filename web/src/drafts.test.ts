import { readFileSync } from "node:fs";
import { createElement as h } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, describe, expect, test, vi } from "vitest";
import type { components } from "./api/v1";
import { change, type Clash, createdId, WriteResult, declaredOf, DiscardRefused, NOT_DISCARDED, doneOnce, type Editor, type EditorEvent, editor, EMPTY, FRESH, FragmentPane, forgetETag, type Held, holdsUnsaved, inUse, keepETag, keepOnDone, keptETag, LAYERS, lastGood, markLine, mayDrop, mayRevert, maySend, NOT_RENDERED, NOT_REVERTED, NOT_SENT_CONFLICT, NOT_SENT_UNREAD, NOT_SENT_UNSHOWN, paneEntry, rows, selection, sendRefusal, settledFragment, spotOfMark, staleRead, unread, watchWrites, where } from "./drafts.tsx";
import { leaving, signingOut, startsNew, unsaved, WritesProvider } from "./forms.tsx";
import { type Load, type Reads, ReadsProvider } from "./list.tsx";
import { references, spotOfLine, spotOfPath, spotOfReference } from "./position.ts";
import { ShellContext } from "./shell.tsx";
import { CUT_OFF } from "./states.tsx";
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

  test("ginsys/bronzeward#569: the fragment pane reads references with the embedded documents it holds", () => {
    // The unit suite has no DOM; this guards the pane's three reads of the held document.
    const src = readFileSync(new URL("./drafts.tsx", import.meta.url), "utf8");
    expect(src).toContain("const refs = rows(held.document, held.declared, held.embedded);");
    expect(src).toContain("const used = references(held.document, held.embedded);");
    expect(src).toContain('spotOfReference(held.document, p["reference"], held.embedded)');
    expect(rows("cfg: 'a: !bwref x/one'\n", {}, [{ path: "doc[0]/cfg" }])).toEqual(["x/one"]);
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
    // #517 review round 3: a tag that starts its line folds onto a line at its own indentation.
    expect(references("a:\n  !bwref x/foo\n  bar\nb: !bwref x/two\n")).toEqual(["x/two"]);
    expect(references("a:\n  !bwref x/foo\nb: c\n")).toEqual(["x/foo"]);
    expect(references("a: [\n  !bwref x/one,\n  !bwref x/two\n]\n")).toEqual(["x/one", "x/two"]);
    expect(references("a:\n  - !bwref x/one\n  - !bwref x/two\n")).toEqual(["x/one", "x/two"]);
    // #517 review round 4: a document marker ends a top-level scalar.
    expect(references("!bwref x/one\n---\n!bwref x/two\n")).toEqual(["x/one", "x/two"]);
    expect(references("!bwref x/one\n...\n")).toEqual(["x/one"]);
    expect(references("!bwref x/one\n--- # next\n")).toEqual(["x/one"]);
    // A more indented key or sequence entry is no continuation.
    expect(references("a: !bwref x/one\n  b: c\n")).toEqual(["x/one"]);
    expect(references("- !bwref x/one\n  - c\n")).toEqual(["x/one"]);
    for (const bad of ["x/foo-", "x//foo", "-x", "x/", "X/foo"]) expect(references(`a: !bwref ${bad}\n`)).toEqual([]);
    // A flow list closed on the next line still ends its name at the bracket.
    expect(references("a: [!bwref x/one,\n   !bwref x/two\n   ]\n")).toEqual(["x/one", "x/two"]);
  });

  test("ginsys/bronzeward#534 C7: a strip-chomped block scalar and a double-quoted name with an escaped line break are references", () => {
    const text = 'a: !bwref |-\n  x/foo\nb: !bwref >-\n  x/folded\n\nc: !bwref "x/\\\n   esc"\n- !bwref |-\n  x/seq\nd:\n# c\n  !bwref |-\n    x/deep\n';
    expect(references(text)).toEqual(["x/foo", "x/folded", "x/esc", "x/seq", "x/deep"]);
    const s = spotOfReference(text, "x/foo");
    expect(text.slice(s!.start, s!.end)).toBe("!bwref |-\n  x/foo");
    const e = spotOfReference(text, "x/esc");
    expect(text.slice(e!.start, e!.end)).toBe('!bwref "x/\\\n   esc"');
    expect(references("!bwref |-\n x/root\n...\n")).toEqual(["x/root"]);
    // Local review round 2, checked against go-yaml v3: a content line that ends the text keeps no
    // line break, whatever the chomping.
    for (const h of ["|", ">", "|+", ">+"]) expect(references(`a: !bwref ${h}\n  x/foo`)).toEqual(["x/foo"]);
    // Each of these decodes to a value with a line break or a blank, or to nothing (checked against
    // the yaml package): no name.
    for (const none of [
      "a: !bwref |\n  x/foo\n", // clip chomping keeps the final line break
      "a: !bwref |+\n  x/foo\n",
      "a: !bwref |-\n  x/foo\n  x/bar\n", // two content lines
      "a: !bwref |-\n\n  x/foo\n", // a leading empty line
      "a: !bwref |-\n  x/foo # c\n", // a # inside a block scalar is text
      "a: !bwref |-\nx/foo\n", // not more indented than its key: no content
      "a: !bwref |-\n", // no content at all
      'a: !bwref "x/\n  foo"\n', // an unescaped line break folds into a blank
      "a: !bwref |2-\n  x/foo\n", // an indentation indicator is not read here
      // Local review: blanks beyond the content's indentation are content, and so is a
      // non-breaking space, which is no indentation.
      "a: !bwref |-\n  x/foo\n    \n",
      "a: !bwref |-\n  x/foo\n   \nb: c\n",
      "a: !bwref |-\n   x/foo\n",
      // Round 2 (go-yaml v3): a line break or a blank line after the content is kept, a top-level
      // scalar's content needs one space, and a header that ends the text holds nothing.
      "a: !bwref |\n  x/foo\n  ",
      "a: !bwref |+\n  x/foo\n",
      "!bwref |-\nx/root\n",
      "a: !bwref |-",
    ]) expect(references(none)).toEqual([]);
    // Blank lines no deeper than the content's indentation are chomped.
    expect(references("a: !bwref |-\n  x/foo\n  \n\nb: c\n")).toEqual(["x/foo"]);
    // A tag that starts the document's second line, under an empty first one, is still read.
    expect(references("\n  !bwref x/a\n")).toEqual(["x/a"]);
    // Round 3, checked against go-yaml v3: a comment may follow the header, with or without a
    // blank, a header holds each indicator once, and a CRLF line break is one line break.
    for (const one of [
      "a: !bwref |-# comment\n  x/foo\n",
      "a: !bwref |- # comment\n  x/foo\n",
      "a: !bwref >-#c\n  x/foo\n",
      "a: !bwref |#c\n  x/foo",
      "a: !bwref |-\r\n  x/foo\r\n",
      "a: !bwref |\r\n  x/foo",
      "a: !bwref |-\r\n  x/foo\r\n  \r\nb: c\r\n",
      'a: !bwref "x/\\\r\n  foo"\n',
    ]) expect(references(one)).toEqual(["x/foo"]);
    const crlf = "a: !bwref |-\r\n  x/foo\r\nb: c\r\n";
    const c = spotOfReference(crlf, "x/foo");
    expect(crlf.slice(c!.start, c!.end)).toBe("!bwref |-\r\n  x/foo");
    for (const none of [
      "a: !bwref |--\n  x/foo\n", // go-yaml refuses a repeated indicator
      "a: !bwref |-+\n  x/foo\n",
      "a: !bwref |\r\n  x/foo\r\n", // clip chomping keeps the CRLF as a line break
      "a: !bwref |-\r\n  x/foo\r\n   \r\nb: c\r\n", // a deeper blank line is content
    ]) expect(references(none)).toEqual([]);
  });

  // ginsys/bronzeward#544: each expected result below was read from go-yaml v3.0.5 (go.mod's pin) by
  // decoding the same document, not written from memory.
  describe("ginsys/bronzeward#544: the scanner reads the four forms the way go-yaml does", () => {
    test("case 1: a blank line with a tab in or after a block scalar refuses the document, so it names nothing", () => {
      for (const none of [
        "a: !bwref |-\n  x/foo\n\t\nb: c\n", // go-yaml: found a tab character where an indentation space is expected
        "a: !bwref |-\n  x/foo\n \t\nb: c\n",
        "a: !bwref |-\n  x/foo\n\t",
        "a: !bwref |-\r\n  x/foo\r\n\t\r\nb: c\r\n",
        "a: !bwref |-\n\t\n  x/foo\n",
      ]) expect(references(none)).toEqual([]);
      // Spaces alone on that line are still chomped.
      expect(references("a: !bwref |-\n  x/foo\n  \nb: c\n")).toEqual(["x/foo"]);
    });

    test("case 2: content under a key that follows a sequence dash must be deeper than the key", () => {
      for (const none of [
        "- a: !bwref |-\n  x/foo\n", // go-yaml: could not find expected ':'
        "- - a: !bwref |-\n    x/foo\n", // the key is at column 4
        "- - a: !bwref |-\n  x/foo\n",
        "-   a: !bwref |-\n  x/foo\n",
        "- - !bwref |-\n  x/foo\n", // a tag straight after the inner dash: content must pass that dash
      ]) expect(references(none)).toEqual([]);
      for (const one of [
        "- a: !bwref |-\n    x/foo\n",
        "- a: !bwref |-\n   x/foo\n", // one space past the key's column 2 is not enough: column 3 is
        "-   a: !bwref |-\n     x/foo\n",
        "- !bwref |-\n  x/foo\n", // the entry's own value: indented past the dash
        "- !bwref |-\n x/foo\n",
        "- - !bwref |-\n   x/foo\n",
      ]) expect(references(one)).toEqual(["x/foo"]);
      // An anchor after a dash belongs to the entry, not to a key (Copilot review of the PR): the
      // content only has to pass the dash, as without the anchor. An anchored key is still a key.
      for (const one of [
        "- &entry !bwref |-\n  x/foo\n",
        "- &entry !bwref |-\n x/foo\n",
        "- - &e !bwref |-\n   x/foo\n",
        "-   &e !bwref |-\n  x/foo\n",
        "- &e a: !bwref |-\n   x/foo\n",
        "a:\n  - &e !bwref |-\n    x/foo\n",
      ]) expect(references(one)).toEqual(["x/foo"]);
      // `&e:` is an anchored empty key (the anchor ends before the colon), so the content must pass
      // the key's column: go-yaml refuses the first two and accepts the last two.
      for (const none of [
        "- - &e !bwref |-\n  x/foo\n",
        "- &e a: !bwref |-\n  x/foo\n",
        "- &e: !bwref |-\n  x/foo\n",
        "- - &e: !bwref |-\n    x/foo\n",
      ]) expect(references(none)).toEqual([]);
      for (const one of ["- &e: !bwref |-\n   x/foo\n", "- - &e: !bwref |-\n     x/foo\n"]) expect(references(one)).toEqual(["x/foo"]);
      // The key's column binds a block scalar's content only. A plain scalar's next line is read as
      // before: go-yaml refuses the first (could not find expected ':') and folds the second into
      // "x/foo y", so neither names a reference.
      expect(references("- a: !bwref x/foo\n  y\n")).toEqual([]);
      expect(references("- a: !bwref x/foo\n    y\n")).toEqual([]);
    });

    test("case 3: U+0085, U+2028 and U+2029 break a line, in comments, plain, quoted and block scalars alike", () => {
      for (const br of ["\u0085", "\u{2028}", "\u{2029}"]) {
        expect(references(`a: !bwref |-\n  x/foo${br}`)).toEqual(["x/foo"]);
        expect(references(`a: !bwref |-\n  x/foo${br}b: c\n`)).toEqual(["x/foo"]);
        expect(references(`a: !bwref x/foo # c${br}b: !bwref x/bar\n`)).toEqual(["x/foo", "x/bar"]);
        expect(references(`a: !bwref x/foo${br}b: d\n`)).toEqual(["x/foo"]);
        // The break folds the plain and single-quoted scalar into a blank: no name.
        expect(references(`a: !bwref x/foo${br}  y\n`)).toEqual([]);
        expect(references(`a: !bwref 'x/foo${br}  y'\n`)).toEqual([]);
        // A second content line after the break: not one line of content.
        expect(references(`a: !bwref |-\n  x/foo${br}  y\n`)).toEqual([]);
      }
      // After a backslash the break is an escaped line break, which a double-quoted scalar drops.
      expect(references('a: !bwref "x/\\\u0085foo"\n')).toEqual(["x/foo"]);
    });

    test("case 4: a carriage return alone breaks a line, and CRLF stays one break", () => {
      expect(references("a: !bwref |-\r  x/foo\r")).toEqual(["x/foo"]);
      expect(references("a: !bwref |-\r  x/foo\rb: c\r")).toEqual(["x/foo"]);
      expect(references("a: !bwref x/foo # c\rb: !bwref x/bar\r")).toEqual(["x/foo", "x/bar"]);
      expect(references('a: !bwref "x/\\\rfoo"\n')).toEqual(["x/foo"]);
      expect(references("a: !bwref |-\r  x/foo\r  y\r")).toEqual([]);
      expect(references("a: !bwref x/foo\r  y\r")).toEqual([]);
      // CRLF is one break: the same documents with CRLF read as with LF, and a CRLF does not add an
      // empty line that would change what a block scalar holds.
      expect(references("a: !bwref |-\r\n  x/foo\r\nb: c\r\n")).toEqual(["x/foo"]);
      expect(references("a: !bwref |\r\n  x/foo\r\n")).toEqual([]);
      expect(references("a: !bwref |-\r\n  x/foo\r\n\r\nb: c\r\n")).toEqual(["x/foo"]);
      // A mix on one document: CR, LF and CRLF.
      expect(references("a: !bwref x/one\rb: !bwref |-\n  x/two\r\nc: !bwref x/three\n")).toEqual(["x/one", "x/two", "x/three"]);
    });

    test("a spot still addresses the original text after a line break is read as one", () => {
      for (const br of ["\r", "\u0085", "\u{2028}", "\u{2029}"]) {
        const text = `a: !bwref |-${br}  x/foo${br}b: !bwref x/bar${br}`;
        expect(references(text)).toEqual(["x/foo", "x/bar"]);
        const s = spotOfReference(text, "x/foo");
        expect(text.slice(s!.start, s!.end)).toBe(`!bwref |-${br}  x/foo`);
        const b = spotOfReference(text, "x/bar");
        expect(text.slice(b!.start, b!.end)).toBe("!bwref x/bar");
      }
    });
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
  const typed = (c: Clash): Clash => ({ ...c, typed: true });
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
    ["ginsys/bronzeward#534 N1: a second Show while asked marks its draft read pending", { held: M, saved: A, conflict: asked(M) }, { kind: "show" }, { held: M, saved: A, conflict: { ...asked(M), again: true } }],
    ["N1: a revision read before the repeated Show's draft read shows their text, with Copy waiting", { held: M, saved: A, conflict: { ...awaiting(M), again: true } }, { kind: "read", text: T, since: 0 }, { held: T, saved: T, conflict: { ...theirs(M), awaits: true } }],
    ["N1: the repeated Show's draft read ends that mark, and the revision read is still awaited", { held: M, saved: A, conflict: { ...awaiting(M), again: true } }, { kind: "draft", revision: 8, changed: false }, { held: M, saved: A, conflict: awaiting(M) }],
    ["boundary, R4-1: text typed while the asked read is pending is the operator's text", { held: M, saved: A, conflict: asked(M) }, { kind: "type", held: M2 }, { held: M2, saved: A, conflict: asked(M2) }],
    ["boundary, R4-1: Show with no change to this fragment shows its stored text, the operator's aside", { held: M, saved: A, conflict: asked(M) }, { kind: "draft", revision: 8, changed: false }, { held: A, saved: A, conflict: theirs(M) }],
    ["Show with this fragment changed waits for its revision read", { held: M, saved: A, conflict: asked(M) }, { kind: "draft", revision: 8, changed: true }, { held: M, saved: A, conflict: awaiting(M) }],
    ["H7: Show with this fragment removed shows the removal, the operator's text aside", { held: M, saved: A, conflict: asked(M) }, { kind: "draft", revision: 8, changed: false, removed: true }, { held: EMPTY, saved: EMPTY, conflict: theirs(M) }],
    ["H7: a removal while a revision read is awaited shows the removal and ends the wait", { held: M, saved: A, conflict: awaiting(M) }, { kind: "draft", revision: 9, changed: false, removed: true }, { held: EMPTY, saved: EMPTY, conflict: theirs(M, 9) }],
    ["L1: a draft read with no change while a revision read is awaited keeps waiting", { held: A, saved: A, conflict: awaiting(A) }, { kind: "draft", revision: 8, changed: false }, { held: A, saved: A, conflict: awaiting(A) }],
    ["N1: the asked revision read before Show's draft read shows their text, the operator's aside, Copy waiting", { held: M2, saved: A, conflict: asked(M2, 8) }, { kind: "read", text: T, since: 0 }, { held: T, saved: T, conflict: { ...theirs(M2), awaits: true } }],
    ["the awaited revision read shows their text and ends the wait", { held: M2, saved: A, conflict: awaiting(M2) }, { kind: "read", text: T, since: 0 }, { held: T, saved: T, conflict: theirs(M2) }],
    ["R3-F1: a Show once their text shows keeps the operator's text", { held: T, saved: T, conflict: theirs(M) }, { kind: "show" }, { held: T, saved: T, conflict: { ...theirs(M), awaits: true } }],
    ["PR review: Copy my text while a revision read is pending changes nothing", { held: T, saved: T, conflict: theirs(M), reading: true }, { kind: "copy" }, { held: T, saved: T, conflict: theirs(M), reading: true }],
    ["PR review: Copy my text while Show waits for its reads changes nothing", { held: T, saved: T, conflict: { ...theirs(M), awaits: true } }, { kind: "copy" }, { held: T, saved: T, conflict: { ...theirs(M), awaits: true } }],
    ["text typed once their text shows leaves the operator's text aside, and marks it typed", { held: T, saved: T, conflict: theirs(M) }, { kind: "type", held: T2 }, { held: T2, saved: T, conflict: typed(theirs(M)) }],
    ["R1-F5: once shown, the conflict follows the draft revision as read", { held: T, saved: T, conflict: theirs(M) }, { kind: "draft", revision: 9, changed: true }, { held: T, saved: T, conflict: theirs(M, 9) }],
    ["ginsys/bronzeward#551 item 2: another author's newer text read after the show shows, the text typed since aside", { held: M2, saved: T, conflict: typed(theirs(M)) }, { kind: "read", text: T2, since: 0, revision: 9 }, { held: T2, saved: T2, conflict: theirs(M2, 9) }],
    ["#551 local review: after the pane's own done Remove, nothing typed since, another author's newer text shows and the operator's text stays aside", { held: T, saved: EMPTY, conflict: theirs(M) }, { kind: "read", text: T2, since: 0, revision: 10 }, { held: T2, saved: T2, conflict: theirs(M, 10) }],
    ["#551 item 2: the read of the pane's own saved revision after the show keeps text typed since", { held: M2, saved: T, conflict: typed(theirs(M)), own: "frv_9" }, { kind: "read", text: T2, since: 0, id: "frv_9" }, { held: M2, saved: T2, conflict: typed(theirs(M)), own: "frv_9" }],
    ["#551 item 2: a read after the show while a Save is open is held back for that Save's answer", { held: M2, saved: T, conflict: typed(theirs(M)), sent: { ...sentOf(M2, true), open: true } }, { kind: "read", text: T2, since: 0, revision: 9 }, { held: M2, saved: T2, conflict: typed(theirs(M)), sent: { ...sentOf(M2, true), open: true, later: { revision: 9, text: T2, aside: true } } }],
    ["#551 item 2: that Save failing shows the held-back read, the typed text aside", { held: M2, saved: T2, conflict: typed(theirs(M)), sent: { ...sentOf(M2, true), open: true, later: { revision: 9 } } }, { kind: "failed" }, { held: T2, saved: T2, conflict: theirs(M2, 9), sent: sentOf(M2, true) }],
    ["#551 item 2: that Save failing after a later draft read names the newer draft revision", { held: M2, saved: T2, conflict: typed(theirs(M, 10)), sent: { ...sentOf(M2, true), open: true, later: { revision: 10 } } }, { kind: "failed" }, { held: T2, saved: T2, conflict: theirs(M2, 10), sent: sentOf(M2, true) }],
    ["#551 local review: a failed Remove with nothing typed since the show keeps the operator's text aside", { held: T, saved: T2, conflict: theirs(M), sent: { text: T, whileTheirs: true, open: true, removes: true, later: { revision: 9 } } }, { kind: "failed" }, { held: T2, saved: T2, conflict: theirs(M, 9), sent: { text: T, whileTheirs: true, removes: true } }],
    ["#551 item 1: another author's removal after the show shows the removal, the operator's text still aside", { held: T, saved: T, conflict: theirs(M) }, { kind: "draft", revision: 9, changed: false, removed: true }, { held: EMPTY, saved: EMPTY, conflict: theirs(M, 9) }],
    ["#551 item 1: a removal after the show keeps text typed since aside", { held: T2, saved: T, conflict: typed(theirs(M)) }, { kind: "draft", revision: 9, changed: false, removed: true }, { held: EMPTY, saved: EMPTY, conflict: theirs(T2, 9) }],
    ["#551 item 1: a repeated Show's draft read that reports a removal shows it and ends the wait", { held: T, saved: T, conflict: { ...theirs(M), awaits: true } }, { kind: "draft", revision: 9, changed: false, removed: true }, { held: EMPTY, saved: EMPTY, conflict: theirs(M, 9) }],
    ["#551 item 1, H8: the pane's own done Remove after the show keeps its text", { held: T, saved: EMPTY, conflict: theirs(M) }, { kind: "draft", revision: 9, changed: false, removed: true }, { held: T, saved: EMPTY, conflict: theirs(M, 9) }],
    ["#551 item 1, #559: a removal after the show while a write is open is held back for that write's answer", { held: T2, saved: T, conflict: typed(theirs(M)), sent: { text: T2, whileTheirs: true, open: true, removes: true } }, { kind: "draft", revision: 9, changed: false, removed: true }, { held: T2, saved: EMPTY, conflict: typed(theirs(M, 9)), sent: { text: T2, whileTheirs: true, open: true, removes: true, later: { revision: 9, removed: true } } }],
    ["#551 item 3: a draft read that changes only another entry moves a held-back read's revision", { held: M, saved: T, sent: { ...sentOf(M), open: true, later: { revision: 8 } } }, { kind: "draft", revision: 9, changed: false }, { held: M, saved: T, sent: { ...sentOf(M), open: true, later: { revision: 9 } } }],
    ["a revision read after the show, with nothing typed, shows their latest text", { held: T, saved: T, conflict: theirs(M) }, { kind: "read", text: T2, since: 0 }, { held: T2, saved: T2, conflict: theirs(M) }],
    ["boundary: Copy my text puts the operator's text back and ends the conflict", { held: T2, saved: T, conflict: theirs(M) }, { kind: "copy" }, { held: M, saved: T }],
    ["R7-1: Copy my text before their text shows changes nothing", { held: M, saved: A, conflict: asked(M, 8) }, { kind: "copy" }, { held: M, saved: A, conflict: asked(M, 8) }],
    ["Copy my text without a conflict changes nothing", { held: M, saved: A }, { kind: "copy" }, { held: M, saved: A }],
    ["#534 C2: a revision read with another text while text is typed shows theirs, the typed text aside", { held: M, saved: A }, { kind: "read", text: T, since: 0, revision: 8 }, { held: T, saved: T, conflict: theirs(M) }],
    ["local review: the read of the pane's own saved revision keeps text typed since, though the server stored another text", { held: M2, saved: M, own: "frv_9" }, { kind: "read", text: T, since: 0, id: "frv_9" }, { held: M2, saved: T, own: "frv_9" }],
    ["R1-F2: a read again of the revision shown keeps text typed since", { held: M, saved: A }, { kind: "read", text: A, since: 0, id: "frv_8" }, { held: M, saved: A }],
    ["a done Save records the revision it created", { held: M2, saved: A, sent: sentOf(M) }, { kind: "done", revision: "frv_9" }, { held: M2, saved: M, sent: sentOf(M), writes: 1, own: "frv_9" }],
    ["#534 C3: another author's removal empties a clean editor", { held: A, saved: A }, { kind: "draft", revision: 8, changed: false, removed: true }, { held: EMPTY, saved: EMPTY, gone: 8 }],
    ["#534 C3: another author's removal keeps typed text aside", { held: M, saved: A }, { kind: "draft", revision: 8, changed: false, removed: true }, { held: EMPTY, saved: EMPTY, gone: 8, conflict: theirs(M) }],
    ["H8: the pane's own done Remove keeps its text when the draft read shows the removal", { held: M, saved: EMPTY }, { kind: "draft", revision: 8, changed: false, removed: true }, { held: M, saved: EMPTY, gone: 8 }],
    ["a revision read replaces text nothing was typed into", { held: A, saved: A }, { kind: "read", text: T, since: 0 }, { held: T, saved: T }],
    // Save, sweep (a): the operator types while it is in flight.
    ["a Save records the text it sends, and whether their text shows", { held: M, saved: A, conflict: theirs(M2) }, { kind: "send" }, { held: M, saved: A, conflict: theirs(M2), sent: { ...sentOf(M, true), open: true } }],
    ["H8: a Remove records that it removes the text", { held: M, saved: A }, { kind: "send", removes: true }, { held: M, saved: A, sent: { text: M, whileTheirs: false, open: true, removes: true } }],
    ["#534 N2: a done Remove while their text shows keeps the operator's text aside for Copy", { held: T, saved: T, conflict: theirs(M), sent: { text: T, whileTheirs: true, removes: true } }, { kind: "done" }, { held: T, saved: EMPTY, conflict: theirs(M), sent: { text: T, whileTheirs: true, removes: true }, writes: 1 }],
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
    ["Revert to base while their text shows replaces the editor's text, the operator's still aside, and marks it typed (#551 local review)", { held: T, saved: T, conflict: theirs(M) }, { kind: "revert", at: 0, text: A, note: "n" }, { held: A, saved: T, conflict: typed(theirs(M)), note: "n" }],
    // Draft read and Show, sweep (b): an older draft revision after a newer one.
    ["an older draft revision read after a newer one changes nothing", { held: T, saved: T, conflict: theirs(M, 9) }, { kind: "draft", revision: 8, changed: false }, { held: T, saved: T, conflict: theirs(M, 9) }],
    ["an older draft revision while asked shows nothing", { held: M, saved: A, conflict: asked(M, 9) }, { kind: "draft", revision: 8, changed: false }, { held: M, saved: A, conflict: asked(M, 9) }],
    // ginsys/bronzeward#609: a conflict follows the newer of its revision and the one a read names.
    ["#609: a read naming an older draft revision does not move the conflict back", { held: M2, saved: T, conflict: theirs(M, 11) }, { kind: "read", text: T2, since: 0, revision: 10, id: "frv_10" }, { held: T2, saved: T2, conflict: theirs(M, 11) }],
    ["#609: a read naming the revision the conflict follows keeps it", { held: M2, saved: T, conflict: theirs(M, 11) }, { kind: "read", text: T2, since: 0, revision: 11, id: "frv_11" }, { held: T2, saved: T2, conflict: theirs(M, 11) }],
    ["#609: a read naming a newer draft revision moves the conflict forward", { held: M2, saved: T, conflict: theirs(M, 11) }, { kind: "read", text: T2, since: 0, revision: 12, id: "frv_12" }, { held: T2, saved: T2, conflict: theirs(M, 12) }],
    ["#609: a read that names no revision keeps the revision the conflict follows", { held: M2, saved: T, conflict: theirs(M, 11) }, { kind: "read", text: T2, since: 0, id: "frv_10" }, { held: T2, saved: T2, conflict: theirs(M, 11) }],
    ["#609: with no conflict, a read naming a revision opens one at that revision", { held: M, saved: A }, { kind: "read", text: T, since: 0, revision: 10, id: "frv_10" }, { held: T, saved: T, conflict: theirs(M, 10) }],
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
  test("ginsys/bronzeward#551 item 2: after a Show that changed no text of this fragment, another author's later text shows and Copy my text gives back what was typed since", () => {
    const s = run({ held: M, saved: A }, [{ kind: "refused" }, { kind: "at", revision: 7 }, { kind: "show" }, { kind: "draft", revision: 8, changed: false }, { kind: "type", held: M2 }, { kind: "draft", revision: 9, changed: true }, { kind: "read", text: T2, since: 0, revision: 9 }]);
    expect(rest(s)).toEqual({ held: T2, saved: T2, conflict: theirs(M2, 9) });
    expect(rest(editor(s, { kind: "copy" }))).toEqual({ held: M2, saved: T2 });
  });

  test("ginsys/bronzeward#551 item 1: a repeated Show whose draft read reports a removal shows the removal, and Copy my text gives back the operator's text", () => {
    const s = run({ held: A, saved: A }, [{ kind: "type", held: M }, { kind: "refused" }, { kind: "at", revision: 7 }, { kind: "show" }, { kind: "draft", revision: 8, changed: false }, { kind: "show" }, { kind: "draft", revision: 9, changed: false, removed: true }]);
    expect(rest(s)).toEqual({ held: EMPTY, saved: EMPTY, conflict: theirs(M, 9) });
    expect(rest(editor(s, { kind: "copy" }))).toEqual({ held: M, saved: EMPTY, gone: 9 });
  });

  test("ginsys/bronzeward#551 local review: after the pane's own done Remove, another author restores the entry, and Copy my text still gives back the operator's text", () => {
    const s = run({ held: A, saved: A }, [{ kind: "type", held: M }, { kind: "refused" }, { kind: "at", revision: 7 }, { kind: "show" }, { kind: "draft", revision: 8, changed: true }, { kind: "read", text: T, since: 0, revision: 8 }, { kind: "send", removes: true }, { kind: "done" }, { kind: "draft", revision: 9, changed: false, removed: true }, { kind: "reading", on: true }, { kind: "draft", revision: 10, changed: true }, { kind: "read", text: T2, since: 1, revision: 10 }]);
    expect(rest(s)).toEqual({ held: T2, saved: T2, conflict: theirs(M, 10), sent: { text: T, whileTheirs: true, removes: true }, writes: 1 });
    expect(editor(s, { kind: "copy" }).held).toEqual(M);
  });

  test("ginsys/bronzeward#551 local review round 2: with nothing typed since their text showed, a newer read during a Save shows at once, so the done Save cannot drop it", () => {
    const s = run({ held: T, saved: T, conflict: theirs(M) }, [{ kind: "send" }, { kind: "read", text: T2, since: 0, revision: 10, id: "frv_10" }, { kind: "done", revision: "frv_9" }]);
    // ginsys/bronzeward#613 changed this result: saved was T, so the pane labelled the entry's own text T2
    // unsaved. T2 was read from the entry, so the pane records it as saved and holds nothing unsaved.
    expect(rest(s)).toEqual({ held: T2, saved: T2, sent: sentOf(T, true), writes: 1, own: "frv_9" });
    expect(holdsUnsaved(s.held, s.saved, s.conflict?.mine)).toBe(false);
  });

  test("ginsys/bronzeward#551 local review round 2: text from Revert to base after their text showed is the operator's when a newer read comes", () => {
    const s = run({ held: T, saved: T, conflict: theirs(M) }, [{ kind: "revert", at: 0, text: A, note: "n" }, { kind: "read", text: T2, since: 0, revision: 9 }]);
    expect(rest(s)).toEqual({ held: T2, saved: T2, conflict: theirs(A, 9), note: "n" });
    expect(editor(s, { kind: "copy" }).held).toEqual(A);
  });

  test("ginsys/bronzeward#551 item 3: a read held back while a Save is open names the draft revision of a later draft read that changed only another entry", () => {
    const s = run({ held: A, saved: A }, [{ kind: "type", held: M }, { kind: "send" }, { kind: "read", text: T, since: 0, revision: 8 }, { kind: "draft", revision: 9, changed: false }, { kind: "failed" }]);
    expect(rest(s)).toEqual({ held: T, saved: T, sent: sentOf(M), conflict: theirs(M, 9) });
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
    // A failed revision read of a revision never loaded keeps `reading`: only the cleanup drops it. A reread of the
    // revision already loaded turns it off, or Copy my text stays ignored (ginsys/bronzeward#583 round 9).
    const failure = /if \(r\?\.kind !== "ok"\) \{\n([\s\S]*?)\n      \}/.exec(src);
    expect(failure?.[1]).toMatch(/setFailed\(true\)/);
    expect(failure?.[1]?.match(/reading/g)?.length).toBe(1);
    expect(failure?.[1]).toMatch(/if \(loaded === revision\) act\(\{ kind: "reading", on: false \}\);/);
    expect(src).toMatch(/const waits = unread\(entry\?\.revision, loaded\);/);
    // #517 review round 3: a read with text records its revision, and a draft read again retries
    // a failed one; without either the pane never edits, or stays waiting after Show.
    expect(src).toMatch(/setLoaded\(revision\);\n(?:\s*\/\/[^\n]*\n)*\s*act\(\{ kind: "read", text: fromRevision\(r\.data\), since: done, revision: drafted\.current, id: revision \}\);/);
    expect(src).toMatch(/act\(\{ kind: "draft", revision: draft\.revision, changed[^\n]*\n\s*if \(!changed && failed\) retry\(\(x\) => x \+ 1\);/);
    // #534 rounds 4 and 5: a final outcome other than done or a 412 ends the open write, a set-aside
    // one included. An unknown one does not: [Repeat exactly] may still commit it, or answer 412.
    expect(src).toMatch(/\} else if \(o\?\.kind === "refused" \|\| o\?\.kind === "not-done" \|\| o\?\.kind === "set-aside"\) act\(\{ kind: "failed" \}\);/);
    // PR review: also when the page keeps the data it read before (a failed read with no answer).
    expect(src).toMatch(/\{unshown && item\.kind !== "loading" && <p><button [^>]*onClick=\{\(\) => reread\(\(x\) => x \+ 1\)\}>\{READ_DRAFT_AGAIN\}/);
    // PR review: Copy my text stays off while a repeated Show waits for its reads.
    expect(src).toContain(`disabled={conflict.phase !== "theirs" || conflict.awaits === true || waits || conflict.revision !== draft.revision}`);
  });

  test("PR review: Show their change clicked again holds Copy until its reads answer", () => {
    const shown = run({ held: A, saved: A }, [{ kind: "refused" }, { kind: "at", revision: 7 }, { kind: "show" }, { kind: "draft", revision: 8, changed: true }, { kind: "reading", on: true }, { kind: "read", text: T, since: 0 }]);
    expect(rest(shown)).toEqual({ held: T, saved: T, conflict: theirs(A, 8) });
    // Shown again: from the click, Copy changes nothing, also once the draft read names a newer
    // revision whose read is pending.
    const again = run(shown, [{ kind: "show" }]);
    expect(again.conflict?.awaits).toBe(true);
    expect(rest(editor(again, { kind: "copy" }))).toEqual(rest(again));
    const pending = run(again, [{ kind: "draft", revision: 9, changed: true }, { kind: "reading", on: true }]);
    expect(rest(editor(pending, { kind: "copy" }))).toEqual(rest(pending));
    // The newer text shows, then Copy gives back the operator's text, which no read replaces.
    const newer = run(pending, [{ kind: "read", text: T2, since: 0 }]);
    expect(rest(newer)).toEqual({ held: T2, saved: T2, conflict: theirs(A, 9) });
    expect(editor(newer, { kind: "copy" }).held).toBe(A);
    // A draft read with no new revision ends the wait at once.
    const same = run(again, [{ kind: "draft", revision: 8, changed: false }]);
    expect(same.conflict?.awaits).toBeUndefined();
    expect(editor(same, { kind: "copy" }).held).toBe(A);
  });

  test("round-6 review: an older revision's read that answers before the repeated Show's draft read releases nothing", () => {
    // Their text shows at revision 8, and the operator's kept text equals the saved text (the L1 case).
    const shown = run({ held: T, saved: T, conflict: theirs(T, 8) }, []);
    // Show again: it reads the draft again and retries the revision shown, whose read answers first.
    const early = run(shown, [{ kind: "show" }, { kind: "reading", on: true }, { kind: "read", text: T, since: 0 }]);
    expect(early.conflict?.awaits).toBe(true);
    expect(rest(editor(early, { kind: "copy" }))).toEqual(rest(early));
    // The draft read names revision 9: Copy waits for its read, and that read shows their newer text.
    const named = run(early, [{ kind: "reading", on: true }, { kind: "draft", revision: 9, changed: true }]);
    expect(named.conflict?.awaits).toBeUndefined();
    expect(rest(editor(named, { kind: "copy" }))).toEqual(rest(named));
    const newer = run(named, [{ kind: "read", text: T2, since: 0 }]);
    expect(rest(newer)).toEqual({ held: T2, saved: T2, conflict: theirs(T, 9) });
    // Copy now keeps the operator's text: no read is left to replace it.
    expect(editor(newer, { kind: "copy" }).held).toBe(T);
    // A changed draft read marks the revision read pending even before its reading event.
    expect(run(early, [{ kind: "draft", revision: 9, changed: true }]).reading).toBe(true);
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
    expect(unread("frv_2", undefined)).toBe(true);
    expect(unread("frv_2", "frv_2")).toBe(false);
    expect(unread(null, undefined)).toBe(false);
    expect(unread(undefined, undefined)).toBe(false);
    // #517 review round 2: a refreshed draft that names a newer revision than the one the pane
    // read waits for that one, though an earlier read succeeded.
    expect(unread("frv_3", "frv_2")).toBe(true);
    // #517 review round 3: a Save of the pane's own is no exception. After it, a Discard's Show
    // their change can name another author's revision, which the pane must read before it edits.
    expect(unread.length).toBe(2);
  });

  test("R6-1: a Revert clicked before a 412, Show their change and Copy my text does not replace the copied text", () => {
    const s = run({ held: M, saved: A }, [{ kind: "send" }, { kind: "refused" }, { kind: "at", revision: 7 }, { kind: "show" }, { kind: "draft", revision: 8, changed: true }, { kind: "read", text: T, since: 0 }, { kind: "copy" }, { kind: "revert", at: 0, text: A, note: "n" }]);
    expect(s.held).toBe(M);
    expect(s.note).toBe(NOT_REVERTED);
  });

  // A Discard's Show their change adopts the new ETag with no fragment 412 (H15): the pane's next
  // reads must show what changed before any Save or Remove sends.
  test("ginsys/bronzeward#534 C2: text typed during a Discard's Show their change is kept aside, and their revision shows", () => {
    const s = run({ held: A, saved: A }, [{ kind: "type", held: M }, { kind: "draft", revision: 8, changed: true }, { kind: "reading", on: true }, { kind: "read", text: T, since: 0, revision: 8 }]);
    expect(rest(s)).toEqual({ held: T, saved: T, conflict: theirs(M) });
    expect(holdsUnsaved(s.held, s.saved, s.conflict?.mine)).toBe(true);
    // Save now sends the text that shows; Copy my text gives back the typed text.
    expect(sendRefusal(false, s.conflict, true)).toBeUndefined();
    expect(editor(s, { kind: "copy" }).held).toBe(M);
  });

  test("local review: the pane's own Save, typed into while it is sent, reads back a stored text that differs and opens no conflict", () => {
    const s = run({ held: A, saved: A }, [{ kind: "type", held: M }, { kind: "send" }, { kind: "type", held: M2 }, { kind: "done", revision: "frv_9" }, { kind: "draft", revision: 8, changed: true }, { kind: "reading", on: true }, { kind: "read", text: T, since: 1, revision: 8, id: "frv_9" }]);
    expect(s.conflict).toBeUndefined();
    expect(s.held).toBe(M2);
    expect(s.saved).toBe(T);
    // Another author's revision read after it still shows as theirs.
    const later = run(s, [{ kind: "draft", revision: 9, changed: true }, { kind: "read", text: T2, since: 1, revision: 9, id: "frv_10" }]);
    expect(later.conflict).toEqual({ mine: M2, phase: "theirs", revision: 9 });
  });

  test("ginsys/bronzeward#534 C3: another author's removal read by a Discard's Show their change shows before any Save", () => {
    const clean = run({ held: A, saved: A }, [{ kind: "draft", revision: 8, changed: false, removed: true }]);
    expect(rest(clean)).toEqual({ held: EMPTY, saved: EMPTY, gone: 8 });
    const typed = run({ held: A, saved: A }, [{ kind: "type", held: M }, { kind: "draft", revision: 8, changed: false, removed: true }]);
    expect(rest(typed)).toEqual({ held: EMPTY, saved: EMPTY, gone: 8, conflict: theirs(M) });
    // The pane's own Remove saves the removal, and the text it shows stays (H8).
    const own = run({ held: A, saved: A }, [{ kind: "send", removes: true }, { kind: "done" }, { kind: "draft", revision: 8, changed: false, removed: true }]);
    expect(own.held).toBe(A);
    expect(own.conflict).toBeUndefined();
  });

  test("ginsys/bronzeward#534 round 3: a read that shows the pane's own write before its answer opens no conflict", () => {
    // A reconnect's draft read shows the pane's own Remove before the Remove's answer: the text stays (H8).
    for (const held of [A, M]) {
      const early = run({ held, saved: A }, [{ kind: "send", removes: true }, { kind: "draft", revision: 8, changed: false, removed: true }]);
      expect(early.held).toBe(held);
      expect(early.conflict).toBeUndefined();
      const done = run(early, [{ kind: "done" }]);
      expect(rest(done)).toEqual({ held, saved: EMPTY, sent: { text: held, whileTheirs: false, removes: true }, writes: 1 });
    }
    // The read of a Save's revision, before the Save's answer names it, keeps the typed text.
    const saving = run({ held: M, saved: A }, [{ kind: "send" }, { kind: "type", held: M2 }, { kind: "read", text: T, since: 0, revision: 8, id: "frv_9" }]);
    expect(saving.held).toBe(M2);
    expect(saving.conflict).toBeUndefined();
    expect(run(saving, [{ kind: "done", revision: "frv_9" }]).saved).toBe(M);
    // Once the answer came, another author's removal shows again (C3), also after a 412.
    const after = run({ held: A, saved: A }, [{ kind: "send" }, { kind: "done" }, { kind: "type", held: M }, { kind: "draft", revision: 9, changed: false, removed: true }]);
    expect(after.conflict).toEqual(theirs(M, 9));
    const refusedRun = run({ held: M, saved: A }, [{ kind: "send", removes: true }, { kind: "refused" }]);
    expect(refusedRun.sent?.open).toBeUndefined();
    // A write that committed nothing is not the change a later read shows.
    const failed = run({ held: M, saved: A }, [{ kind: "send", removes: true }, { kind: "failed" }, { kind: "draft", revision: 9, changed: false, removed: true }]);
    expect(failed.conflict).toEqual(theirs(M, 9));
  });

  test("ginsys/bronzeward#534 round 4: a read held back while the pane's write was open shows as theirs once that write fails", () => {
    // Another author's text, read while a Save is open: a write that commits nothing shows it.
    const saving = run({ held: A, saved: A }, [{ kind: "type", held: M }, { kind: "send" }, { kind: "read", text: T, since: 0, revision: 8, id: "frv_8" }]);
    expect(saving.held).toBe(M);
    expect(saving.conflict).toBeUndefined();
    expect(rest(run(saving, [{ kind: "failed" }]))).toEqual({ held: T, saved: T, conflict: theirs(M, 8), sent: sentOf(M) });
    // Their removal, read while a Remove is open, also from a clean editor.
    for (const held of [A, M]) {
      const removing = run({ held, saved: A }, [{ kind: "send", removes: true }, { kind: "draft", revision: 9, changed: false, removed: true }, { kind: "failed" }]);
      expect(removing.held).toBe(EMPTY);
      expect(removing.conflict).toEqual(theirs(held, 9));
    }
    // A 412 ends the held-back read as a conflict of its own. A done Save shows it as theirs unless it
    // is the Save's own revision (ginsys/bronzeward#559, which reversed this assertion: it was
    // `held: M, saved: M` with no conflict, the read dropped).
    const done = run(saving, [{ kind: "done", revision: "frv_9" }]);
    expect(rest(done)).toEqual({ held: T, saved: T, conflict: theirs(M, 8), sent: sentOf(M), writes: 1, own: "frv_9" });
    const refusedRun = run(saving, [{ kind: "refused" }]);
    expect(refusedRun.conflict).toEqual({ mine: M, phase: "refused" });
    expect(refusedRun.sent?.later).toBeUndefined();
    // Round 5: an unknown outcome dispatches nothing, so [Repeat exactly] finds the write still
    // open. Its 412 keeps the typed text, and a read before its done keeps a Remove's text.
    const repeated = run(saving, [{ kind: "refused" }, { kind: "show" }, { kind: "draft", revision: 8, changed: false }]);
    expect(repeated.conflict?.mine).toBe(M);
    expect(run(repeated, [{ kind: "copy" }]).held).toBe(M);
    const removed = run({ held: A, saved: A }, [{ kind: "send", removes: true }, { kind: "draft", revision: 9, changed: false, removed: true }, { kind: "done" }]);
    expect(removed.held).toBe(A);
    expect(removed.conflict).toBeUndefined();
  });

  // ginsys/bronzeward#559: another author's text or removal, read while a Save is open, shows once
  // that Save is done. `theirs10` is the read of their revision 10, which the Save (revision 9) preceded.
  const theirs10: EditorEvent = { kind: "read", text: T, since: 0, revision: 10, id: "frv_10" };
  const sending = (...events: EditorEvent[]) => run({ held: A, saved: A }, [{ kind: "type", held: M }, { kind: "send" }, ...events]);
  const gone = (revision: number): EditorEvent => ({ kind: "draft", revision, changed: false, removed: true });

  test("ginsys/bronzeward#559: a done Save shows another author's revision that a read held back while the Save was open", () => {
    const s = run(sending(theirs10), [{ kind: "done", revision: "frv_9" }]);
    expect(rest(s)).toEqual({ held: T, saved: T, conflict: theirs(M, 10), sent: sentOf(M), writes: 1, own: "frv_9" });
    expect(editor(s, { kind: "copy" }).held).toBe(M);
  });

  test("ginsys/bronzeward#559 case 1: a read that is the Save's own, or cannot be told from it, opens no conflict", () => {
    const own = run(sending({ kind: "read", text: T, since: 0, revision: 9, id: "frv_9" }), [{ kind: "done", revision: "frv_9" }]);
    expect(rest(own)).toEqual({ held: M, saved: M, sent: sentOf(M), writes: 1, own: "frv_9" });
    // The answer without the created revision's id, and a read without its id.
    const noAnswerId = run(sending(theirs10), [{ kind: "done" }]);
    expect(rest(noAnswerId)).toEqual({ held: M, saved: M, sent: sentOf(M), writes: 1 });
    const noReadId = run(sending({ kind: "read", text: T, since: 0, revision: 10 }), [{ kind: "done", revision: "frv_9" }]);
    expect(rest(noReadId)).toEqual({ held: M, saved: M, sent: sentOf(M), writes: 1, own: "frv_9" });
  });

  test("ginsys/bronzeward#559 case 2: the conflict names the newest draft revision, also from a read that changed only another entry", () => {
    const named = run(sending(theirs10, { kind: "draft", revision: 11, changed: false }), [{ kind: "done", revision: "frv_9" }]);
    expect(named.conflict).toEqual(theirs(M, 11));
    // A draft read that changed this entry names a revision whose read is in flight: Copy waits for it.
    const reading = run(sending(theirs10, { kind: "draft", revision: 11, changed: true }, { kind: "reading", on: true }), [{ kind: "done", revision: "frv_9" }]);
    expect(reading.conflict).toEqual(theirs(M, 10));
    expect(rest(editor(reading, { kind: "copy" }))).toEqual(rest(reading));
    // The same once their text shows before the Save.
    const shown = run({ held: T, saved: T, conflict: theirs(M) }, [{ kind: "type", held: M2 }, { kind: "send" }, { ...theirs10, text: T2 }, { kind: "draft", revision: 11, changed: false }, { kind: "done", revision: "frv_9" }]);
    expect(shown.conflict).toEqual(theirs(M2, 11));
    expect(shown.held).toBe(T2);
  });

  test("ginsys/bronzeward#559 case 3: a repeated Show's wait stays through the done Save", () => {
    const s = run({ held: T, saved: T, conflict: theirs(M) }, [{ kind: "type", held: M2 }, { kind: "send" }, { kind: "show" }, { ...theirs10, text: T2 }, { kind: "done", revision: "frv_9" }]);
    expect(s.conflict).toEqual({ mine: M2, phase: "theirs", revision: 10, awaits: true });
    expect(rest(editor(s, { kind: "copy" }))).toEqual(rest(s));
    // Show's draft read ends the wait, and Copy gives back the operator's text.
    expect(editor(run(s, [{ kind: "draft", revision: 10, changed: false }]), { kind: "copy" }).held).toBe(M2);
  });

  test("ginsys/bronzeward#559 case 4: another author's removal, read while the Save was open, shows at its draft revision once the Save is done", () => {
    const removed = { held: EMPTY, saved: EMPTY, conflict: theirs(M, 11), sent: sentOf(M), writes: 1, own: "frv_9" };
    // A removal after their text read, and a removal alone.
    expect(rest(run(sending(theirs10, gone(11)), [{ kind: "done", revision: "frv_9" }]))).toEqual(removed);
    expect(rest(run(sending(gone(11)), [{ kind: "done", revision: "frv_9" }]))).toEqual(removed);
    // Once their text shows: the removal is held back, and shows at the newest draft revision.
    const shown = run({ held: T, saved: T, conflict: theirs(M) }, [{ kind: "type", held: M2 }, { kind: "send" }, gone(11), { kind: "draft", revision: 12, changed: false }, { kind: "done", revision: "frv_9" }]);
    expect(rest(shown)).toEqual({ held: EMPTY, saved: EMPTY, conflict: theirs(M2, 12), sent: sentOf(M2, true), writes: 1, own: "frv_9" });
    // A newer text read replaces the removal, and a removal replaces the text read.
    const replaced = run(sending(gone(11), { ...theirs10, text: T2, revision: 12, id: "frv_12" }), [{ kind: "done", revision: "frv_9" }]);
    expect(replaced.conflict).toEqual(theirs(M, 12));
    expect(replaced.held).toBe(T2);
  });

  // The two findings of the hosted review of #561 and the regression of its reverted repair (c283b8fc).
  const typedSend = (c: Clash) => run({ held: T, saved: T, conflict: c }, [{ kind: "type", held: M2 }, { kind: "send" }]);

  test("ginsys/bronzeward#559 review 1: once their text shows, a removal read while a Save is open shows when that Save fails, so a next Save cannot restore the old text over it", () => {
    const s = run(typedSend(theirs(M, 8)), [gone(9), { kind: "failed" }]);
    expect(rest(s)).toEqual({ held: EMPTY, saved: EMPTY, sent: sentOf(M2, true), conflict: theirs(M2, 9) });
    expect(editor(s, { kind: "copy" }).held).toBe(M2);
  });

  test("ginsys/bronzeward#559 review 2: a removal held back without a conflict follows a later draft read that still reports it, so the failed Save names that revision", () => {
    const s = run(sending(gone(8), gone(9), { kind: "failed" }), []);
    expect(rest(s)).toEqual({ held: EMPTY, saved: EMPTY, sent: sentOf(M), conflict: theirs(M, 9) });
    expect(editor(s, { kind: "copy" }).held).toBe(M);
  });

  test("ginsys/bronzeward#559 r7 (c283b8fc): a second removal read in the theirs phase moves the held-back revision too, also after Copy my text", () => {
    const s = run(typedSend(theirs(M, 8)), [gone(9), gone(10), { kind: "copy" }, { kind: "failed" }]);
    expect(rest(s)).toEqual({ held: EMPTY, saved: EMPTY, sent: sentOf(M2, true), conflict: theirs(M, 10), gone: 10 });
    // A draft read that changed only another entry, after the removal, moves it as well.
    const other = run(typedSend(theirs(M, 8)), [gone(9), { kind: "draft", revision: 10, changed: false }, { kind: "failed" }]);
    expect(other.conflict).toEqual(theirs(M2, 10));
  });

  // This test first said the conflict names revision 10, the revision of the text held back. That moved the
  // conflict back from the newer draft revision it already followed, which main never did (it named the
  // larger of the two); the hosted advisory review and local review round 7 of #583 found it.
  test("ginsys/bronzeward#559 failed Save: the conflict keeps the newer draft revision it follows, and Copy waits for the read in flight", () => {
    const s = run(typedSend(theirs(M, 8)), [{ ...theirs10, text: T2 }, { kind: "draft", revision: 11, changed: true }, { kind: "reading", on: true }, { kind: "failed" }]);
    expect(s.conflict).toEqual(theirs(M2, 11));
    expect(rest(editor(s, { kind: "copy" }))).toEqual(rest(s));
  });

  test("ginsys/bronzeward#559 round 7 finding 3: a done Save keeps the newer draft revision the conflict follows, and Copy waits for the read in flight", () => {
    const s = run(typedSend(theirs(M, 8)), [{ ...theirs10, text: T2 }, { kind: "draft", revision: 11, changed: true }, { kind: "reading", on: true }, { kind: "done", revision: "frv_9" }]);
    expect(s.conflict).toEqual(theirs(M2, 11));
    expect(rest(editor(s, { kind: "copy" }))).toEqual(rest(s));
    // Without a newer draft revision, the held-back revision names it, as before.
    const same = run(typedSend(theirs(M, 8)), [{ ...theirs10, text: T2 }, { kind: "done", revision: "frv_9" }]);
    expect(same.conflict).toEqual(theirs(M2, 10));
  });

  test("ginsys/bronzeward#559 local review 1: a draft read that repeats the pane's own done Remove while a Save is open opens no conflict for it", () => {
    // The pane reads its Remove's revision 9 before it sends the Save that restores the text.
    const own = [{ kind: "send", removes: true }, { kind: "done" }, gone(9), { kind: "send" }] as EditorEvent[];
    const s = run({ held: T, saved: T, conflict: theirs(M) }, [...own, gone(9), { kind: "done", revision: "frv_9" }]);
    expect(rest(s)).toEqual({ held: T, saved: T, sent: sentOf(T, true), writes: 2, own: "frv_9" });
    // A removal by another author at a newer revision, read while that Save is open, still shows.
    const theirsGone = run({ held: T, saved: T, conflict: theirs(M) }, [...own, gone(11), { kind: "done", revision: "frv_10" }]);
    expect(theirsGone.held).toBe(EMPTY);
    expect(theirsGone.saved).toBe(EMPTY);
    expect(theirsGone.conflict).toEqual(theirs(T, 11));
  });

  test("ginsys/bronzeward#559 local review 2: a newer read that shows at once replaces the read held back, so a done Save does not show the older one", () => {
    const newer: EditorEvent = { kind: "read", text: T, since: 0, revision: 11, id: "frv_11" };
    const s = run(sending({ ...theirs10, text: M }, newer), [{ kind: "done", revision: "frv_9" }]);
    expect(s.held).toBe(T);
    expect(s.conflict).toBeUndefined();
    expect(run(s, [{ kind: "draft", revision: 11, changed: false }]).conflict).toBeUndefined();
  });

  test("ginsys/bronzeward#559 item 4: a revision read that answers after a write finished is read again, not marked loaded", () => {
    // The editor drops the answer of a read that began before a done write ...
    expect(rest(run({ held: M, saved: M, writes: 1 }, [{ kind: "read", text: T, since: 0, revision: 10, id: "frv_10" }]))).toEqual({ held: M, saved: M, writes: 1 });
    // ... so the pane reads again, as the effect hooks cannot run in this node environment (#534 PR review does the same).
    const src = readFileSync(new URL("./drafts.tsx", import.meta.url), "utf8");
    expect(src).toMatch(/finished\.current = done;/);
    expect(src).toMatch(/if \(staleRead\(finished\.current, done\)\) \{\n\s*if \(loaded === revision\) act\(\{ kind: "reading", on: false \}\);\n\s*return setFailed\(true\);\n\s*\}\n\s*setLoaded\(revision\);/);
    // The stale read is not read again at once: the pre-write revision could answer again (PR review).
    expect(src).not.toMatch(/finished\.current !== done\) return retry/);
    // A read that answers after the done answer is dispatched and before the next render sees the write as finished (hosted review).
    expect(src).toMatch(/finished\.current \+= 1;\n\s*act\(\{ kind: "done"/);
  });

  test("ginsys/bronzeward#559 PR review: a revision read that began before a write finished is stale, and the pane shows it as failed", () => {
    expect(staleRead(1, 0)).toBe(true);
    expect(staleRead(2, 0)).toBe(true);
    expect(staleRead(0, 0)).toBe(false);
    expect(staleRead(3, 3)).toBe(false);
    // The pane starts from FRESH, and a stale read sets `failed` with no note: the next draft read that names a new revision reads it again.
    const src = readFileSync(new URL("./drafts.tsx", import.meta.url), "utf8");
    expect(src).toMatch(/useReducer\(editor, FRESH\)/);
    expect(src).toMatch(/if \(staleRead\(finished\.current, done\)\) \{/);
    // The gap that stays: a stale answer that `live` drops after the refresh is not covered by a test.
  });

  test("ginsys/bronzeward#559 PR review: a reread of an already loaded revision that ends in a stale or a failed read turns reading off, so Copy my text works again", () => {
    // The dead end: Copy my text is ignored while `reading` is on, and nothing turns it off when the reread ends without an answer.
    const theirsShown = run({ held: T, saved: T, conflict: theirs(M) }, [{ kind: "reading", on: true }]);
    expect(editor(theirsShown, { kind: "copy" })).toBe(theirsShown);
    const ended = run(theirsShown, [{ kind: "reading", on: false }]);
    expect(editor(ended, { kind: "copy" }).held).toBe(M);
    // The three panes turn `reading` off in both branches, only for a revision that is already loaded; a revision never loaded stays unread.
    const fragment = readFileSync(new URL("./drafts.tsx", import.meta.url), "utf8");
    const composition = readFileSync(new URL("./composition.tsx", import.meta.url), "utf8");
    const off = /if \(loaded === revision\) act\(\{ kind: "reading", on: false \}\);/g;
    expect(fragment.match(off)?.length).toBe(2);
    expect(composition.match(off)?.length).toBe(4);
    expect(fragment).toMatch(/if \(loaded === revision\) act\(\{ kind: "reading", on: false \}\);\n\s*setFailed\(true\);\n\s*return setNote\(/);
    expect(composition.match(/if \(loaded === revision\) act\(\{ kind: "reading", on: false \}\);\n\s*setFailed\(true\);\n\s*return setNote\(/g)?.length).toBe(2);
  });

  test("ginsys/bronzeward#559 PR review: another author's removal of a new fragment, read while its first Save is open, shows when that Save is done", () => {
    // The first Save committed, the removal came before its answer: the pane started from nothing removed.
    expect(FRESH).toEqual({ held: EMPTY, saved: EMPTY, gone: -1 });
    const s = run(FRESH, [{ kind: "type", held: M }, { kind: "send" }, gone(5), { kind: "done", revision: "frv_1" }]);
    expect(rest(s)).toEqual({ held: EMPTY, saved: EMPTY, conflict: theirs(M, 5), sent: sentOf(M), writes: 1, own: "frv_1" });
    expect(editor(s, { kind: "copy" }).held).toBe(M);
    // A newer removal read while the Save is open moves the one held back.
    const newer = run(FRESH, [{ kind: "type", held: M }, { kind: "send" }, gone(5), gone(7), { kind: "done", revision: "frv_1" }]);
    expect(newer.conflict).toEqual(theirs(M, 7));
    // A failed first Save shows the removal as well.
    const failed = run(FRESH, [{ kind: "type", held: M }, { kind: "send" }, gone(5), { kind: "failed" }]);
    expect(failed.held).toBe(EMPTY);
    expect(failed.conflict).toEqual(theirs(M, 5));
    // A removal read before the Save is the entry's own state: the Save restores it, and the same revision read again is not another author's.
    const known = run(FRESH, [gone(5), { kind: "type", held: M }, { kind: "send" }, gone(5), { kind: "done", revision: "frv_1" }]);
    expect(rest(known)).toEqual({ held: M, saved: M, sent: sentOf(M), writes: 1, own: "frv_1" });
    // With no write open, a removal read is as before: it sets the revision gone.
    expect(rest(run(FRESH, [gone(5)]))).toEqual({ held: EMPTY, saved: EMPTY, gone: 5 });
  });

  test("ginsys/bronzeward#559 hosted review: without a conflict, another author's removal at a newer revision than the pane's own done Remove shows when the restoring Save is done", () => {
    const removed = [{ kind: "send", removes: true }, { kind: "done" }, gone(9), { kind: "send" }] as EditorEvent[];
    const s = run({ held: T, saved: T }, [...removed, gone(11), { kind: "done", revision: "frv_10" }]);
    expect(rest(s)).toEqual({ held: EMPTY, saved: EMPTY, conflict: theirs(T, 11), sent: sentOf(T), writes: 2, own: "frv_10" });
    expect(editor(s, { kind: "copy" }).held).toBe(T);
    // The read of the pane's own Remove, repeated while the Save is open, is not another author's.
    const own = run({ held: T, saved: T }, [...removed, gone(9), { kind: "done", revision: "frv_10" }]);
    expect(rest(own)).toEqual({ held: T, saved: T, sent: sentOf(T), writes: 2, own: "frv_10" });
    // Before the pane's own removal was read, a removal cannot be told from it: the text stays.
    const unread = run({ held: T, saved: T }, [{ kind: "send", removes: true }, { kind: "done" }, { kind: "send" }, gone(11), { kind: "done", revision: "frv_10" }]);
    expect(rest(unread)).toEqual({ held: T, saved: T, sent: sentOf(T), writes: 2, own: "frv_10" });
  });

  // The three findings the hosted review of #571 reported after the repair of 333ee670.
  test("ginsys/bronzeward#559 follow-up 1: another author's removal that first empties a clean editor is followed, so a newer removal shows when a Save typed since is done", () => {
    const s = run({ held: T, saved: T }, [gone(9), { kind: "type", held: T2 }, { kind: "send" }, gone(11), { kind: "done", revision: "frv_10" }]);
    expect(rest(s)).toEqual({ held: EMPTY, saved: EMPTY, conflict: theirs(T2, 11), sent: sentOf(T2), writes: 1, own: "frv_10" });
    expect(editor(s, { kind: "copy" }).held).toBe(T2);
  });

  test("ginsys/bronzeward#559 follow-up 2: once a done Save shows the read held back, a newer accepted read moves the conflict to its revision", () => {
    const done = run(sending(theirs10, { kind: "draft", revision: 11, changed: true }), [{ kind: "done", revision: "frv_9" }]);
    expect(done.conflict).toEqual(theirs(M, 10));
    const s = run(done, [{ kind: "read", text: T2, since: 1, revision: 11, id: "frv_11" }]);
    expect(rest(s)).toEqual({ held: T2, saved: T2, sent: sentOf(M), conflict: theirs(M, 11), writes: 1, own: "frv_9" });
    // A read that names no newer revision leaves it.
    expect(run(done, [{ kind: "read", text: T2, since: 1, revision: 9, id: "frv_8" }]).conflict).toEqual(theirs(M, 10));
    // The same text, read again at the newer revision, moves it too (local review 1).
    const same = run(done, [{ kind: "read", text: T, since: 1, revision: 11, id: "frv_11" }]);
    expect(rest(same)).toEqual({ held: T, saved: T, sent: sentOf(M), conflict: theirs(M, 11), writes: 1, own: "frv_9" });
  });

  test("ginsys/bronzeward#559 follow-up 4: Copy my text keeps the revision of the removal shown, so a newer removal shows when the next Save is done", () => {
    const shown = run(sending(gone(10)), [{ kind: "done", revision: "frv_9" }]);
    expect(shown.conflict).toEqual(theirs(M, 10));
    const s = run(shown, [{ kind: "copy" }, { kind: "send" }, gone(12), { kind: "done", revision: "frv_11" }]);
    expect(s.held).toEqual(EMPTY);
    expect(s.conflict).toEqual(theirs(M, 12));
    // The removal shown again, at the same revision, is not newer.
    const again = run(shown, [{ kind: "copy" }, { kind: "send" }, gone(10), { kind: "done", revision: "frv_11" }]);
    expect(again.conflict).toBeUndefined();
    expect(again.held).toEqual(M);
  });

  test("ginsys/bronzeward#559 follow-up 3: the same text read again at a newer revision while a Save is open moves the read held back, so a failed or done Save names that revision", () => {
    const again: EditorEvent = { kind: "read", text: T, since: 0, revision: 11, id: "frv_11" };
    const failed = run(sending(theirs10, again), [{ kind: "failed" }]);
    expect(rest(failed)).toEqual({ held: T, saved: T, sent: sentOf(M), conflict: theirs(M, 11) });
    const done = run(sending(theirs10, again), [{ kind: "done", revision: "frv_9" }]);
    expect(rest(done)).toEqual({ held: T, saved: T, sent: sentOf(M), conflict: theirs(M, 11), writes: 1, own: "frv_9" });
  });

  // ginsys/bronzeward#608 and #613: another author's text, read while the pane's own write is open, shows
  // once that write is done, also with nothing typed. A Remove creates no text revision, so no text read
  // is its own; a Save's own revision is told by its id (#559).
  const removing = (...events: EditorEvent[]) => run({ held: A, saved: A }, [{ kind: "type", held: M }, { kind: "send", removes: true }, ...events]);
  const removes = (text: Held, whileTheirs = false) => ({ text, whileTheirs, removes: true as const });

  test("ginsys/bronzeward#608: a done Remove shows the text another author restored while it was open, and Copy my text gives back the operator's text", () => {
    const s = run(removing(theirs10), [{ kind: "done" }]);
    expect(rest(s)).toEqual({ held: T, saved: T, sent: removes(M), conflict: theirs(M, 10), writes: 1 });
    const copied = editor(s, { kind: "copy" });
    expect(copied.held).toBe(M);
    expect(copied.conflict).toBeUndefined();
    expect(run(copied, [{ kind: "send" }]).sent?.open).toBe(true);
  });

  test("ginsys/bronzeward#608: with nothing typed, a done Remove shows the restored text as the entry's text, not as unsaved text", () => {
    const s = run({ held: A, saved: A }, [{ kind: "send", removes: true }, theirs10, { kind: "done" }]);
    expect(rest(s)).toEqual({ held: T, saved: T, sent: removes(A), writes: 1 });
    expect(holdsUnsaved(s.held, s.saved, s.conflict?.mine)).toBe(false);
  });

  test("ginsys/bronzeward#608: once their text shows, a done Remove keeps the operator's text aside and shows the text restored meanwhile", () => {
    const s = run({ held: T, saved: T, conflict: theirs(M) }, [{ kind: "send", removes: true }, { kind: "read", text: T2, since: 0, revision: 10, id: "frv_10" }, { kind: "done" }]);
    expect(rest(s)).toEqual({ held: T2, saved: T2, sent: removes(T, true), conflict: theirs(M, 10), writes: 1 });
  });

  test("ginsys/bronzeward#608: the pane's own revision, read while a Remove is open, and a removal after the restoration open no conflict", () => {
    const own = run({ held: A, saved: A, own: "frv_9" }, [{ kind: "type", held: M }, { kind: "send", removes: true }, { kind: "read", text: { ...A, document: "a: stored\n" }, since: 0, revision: 9, id: "frv_9" }, { kind: "done" }]);
    expect(rest(own)).toEqual({ held: M, saved: EMPTY, sent: removes(M), writes: 1 });
    const removed = run(removing(theirs10, gone(11)), [{ kind: "done" }]);
    expect(rest(removed)).toEqual({ held: M, saved: EMPTY, sent: removes(M), writes: 1 });
  });

  test("ginsys/bronzeward#608: a failed Remove shows the text restored while it was open, the operator's text aside", () => {
    expect(rest(run(removing(theirs10), [{ kind: "failed" }]))).toEqual({ held: T, saved: T, sent: removes(M), conflict: theirs(M, 10) });
  });

  test("ginsys/bronzeward#613: with nothing typed, a done Save records another author's text read while it was open as saved", () => {
    const s = run({ held: A, saved: A }, [{ kind: "send" }, theirs10, { kind: "done", revision: "frv_9" }]);
    expect(rest(s)).toEqual({ held: T, saved: T, sent: sentOf(A), writes: 1, own: "frv_9" });
    expect(holdsUnsaved(s.held, s.saved, s.conflict?.mine)).toBe(false);
  });

  test("ginsys/bronzeward#613: a done Save sent while their text showed records a newer text read meanwhile as saved", () => {
    const s = run({ held: T, saved: T, conflict: theirs(M) }, [{ kind: "send" }, { kind: "read", text: T2, since: 0, revision: 10, id: "frv_10" }, { kind: "done", revision: "frv_9" }]);
    expect(rest(s)).toEqual({ held: T2, saved: T2, sent: sentOf(T, true), writes: 1, own: "frv_9" });
    expect(holdsUnsaved(s.held, s.saved, s.conflict?.mine)).toBe(false);
  });

  test("ginsys/bronzeward#613: with nothing typed, the Save's own revision, or one that cannot be told from it, records nothing as another author's", () => {
    const stored = { kind: "read", text: { ...A, document: "a: stored\n" }, since: 0, revision: 9, id: "frv_9" } as const;
    const own = run({ held: A, saved: A }, [{ kind: "send" }, stored, { kind: "done", revision: "frv_9" }]);
    expect(rest(own)).toEqual({ held: stored.text, saved: A, sent: sentOf(A), writes: 1, own: "frv_9" });
    const unknown = run({ held: A, saved: A }, [{ kind: "send" }, stored, { kind: "done" }]);
    expect(rest(unknown)).toEqual({ held: stored.text, saved: A, sent: sentOf(A), writes: 1 });
  });

  test("ginsys/bronzeward#613 local review: text typed after a read that showed at once goes aside when the Save or Remove is done", () => {
    const save = run({ held: A, saved: A }, [{ kind: "send" }, theirs10, { kind: "type", held: M }, { kind: "done", revision: "frv_9" }]);
    expect(rest(save)).toEqual({ held: T, saved: T, sent: sentOf(A), conflict: theirs(M, 10), writes: 1, own: "frv_9" });
    expect(editor(save, { kind: "copy" }).held).toBe(M);
    const remove = run({ held: A, saved: A }, [{ kind: "send", removes: true }, theirs10, { kind: "type", held: M }, { kind: "done" }]);
    expect(rest(remove)).toEqual({ held: T, saved: T, sent: removes(A), conflict: theirs(M, 10), writes: 1 });
    expect(editor(remove, { kind: "copy" }).held).toBe(M);
  });

  test("ginsys/bronzeward#534 N1: Show clicked again while asked holds Copy until its draft read answers, also when a revision read answers first", () => {
    const asked8 = run({ held: A, saved: A }, [{ kind: "refused" }, { kind: "at", revision: 7 }, { kind: "show" }, { kind: "draft", revision: 8, changed: true }]);
    const early = run(asked8, [{ kind: "show" }, { kind: "reading", on: true }, { kind: "read", text: T, since: 0 }]);
    expect(early.conflict?.awaits).toBe(true);
    expect(rest(editor(early, { kind: "copy" }))).toEqual(rest(early));
    // That draft read names revision 9: Copy waits for its read.
    const named = run(early, [{ kind: "reading", on: true }, { kind: "draft", revision: 9, changed: true }]);
    expect(rest(editor(named, { kind: "copy" }))).toEqual(rest(named));
    const newer = run(named, [{ kind: "read", text: T2, since: 0 }]);
    expect(rest(newer)).toEqual({ held: T2, saved: T2, conflict: theirs(A, 9) });
    expect(editor(newer, { kind: "copy" }).held).toBe(A);
  });

  test("ginsys/bronzeward#534 N2: a Remove after Show their change keeps the operator's text for Copy my text", () => {
    const s = run({ held: M, saved: A }, [{ kind: "refused" }, { kind: "at", revision: 7 }, { kind: "show" }, { kind: "draft", revision: 8, changed: true }, { kind: "read", text: T, since: 0 }, { kind: "send", removes: true }, { kind: "done" }]);
    expect(s.conflict?.mine).toBe(M);
    expect(holdsUnsaved(s.held, s.saved, s.conflict?.mine)).toBe(true);
    expect(editor(s, { kind: "copy" }).held).toBe(M);
  });

  test("ginsys/bronzeward#534 PR review: a dismissed unknown write ends the open write, and a revision read names the draft revision shown when it answers", () => {
    const src = readFileSync(new URL("./drafts.tsx", import.meta.url), "utf8");
    // F3: dismissing an unknown write changes no outcome, so the pane watches registry membership.
    expect(src).toMatch(/const dismissed = o\?\.kind === "unknown" && writes\?\.open\.includes\(w!\) !== true;\n\s*useEffect\(\(\) => \{\n\s*if \(dismissed\) act\(\{ kind: "failed" \}\);\n\s*\}, \[dismissed\]\);/);
    // F4: a draft read that changed only another entry while the revision read was pending.
    expect(src).toMatch(/readRevision\.current = entry\?\.revision;\n\s*drafted\.current = draft\.revision;/);
    expect(src).not.toMatch(/kind: "read", text: fromRevision\(r\.data\), since: done, revision: draft\.revision/);
    // What `failed` then does with a read held back while the write was open.
    const s = run({ held: A, saved: A }, [{ kind: "type", held: M }, { kind: "send" }, { kind: "read", text: T, since: 0, revision: 9 }, { kind: "failed" }]);
    expect(rest(s)).toEqual({ held: T, saved: T, sent: sentOf(M), conflict: theirs(M, 9) });
  });

  test("ginsys/bronzeward#534 C1: the draft page registers for its draft's settled writes while it is shown", () => {
    const src = readFileSync(new URL("./drafts.tsx", import.meta.url), "utf8");
    expect(src).toContain("useEffect(() => watchWrites(id, written), [id, written]);");
  });
});

test("ginsys/bronzeward#517 H15: a Discard's 412 says the draft changed and offers to show it", () => {
  const markup = renderToStaticMarkup(h(DiscardRefused, { onShow: () => {} }));
  expect(markup).toContain(NOT_DISCARDED);
  expect(markup).toContain(">Show their change</button>");
});

test("§8.4 item 8: a 412 or 422 refusal whose answer was cut off says so; a 412 still shows its conflict view, a 422 no field view", () => {
  const w = new Write({ method: "PUT", path: "/drafts/drf_1/sources/f", body: {}, resource: "/drafts/drf_1" });
  const shown = (status: number) => {
    w.outcome = { kind: "refused", status, problem: null, replayed: false, cut: true };
    return renderToStaticMarkup(h(WritesProvider, { value: new Writes(() => "p") }, h(ShellContext, { value: { roles: ["author"], offline: false } },
      h(WriteResult, { w, done: () => "DONE", conflict: h("p", null, "CONFLICT"), goTo: () => {} }))));
  };
  for (const status of [412, 422]) {
    const out = shown(status);
    expect(out).toContain(`Not saved: the server answered ${status}. ${CUT_OFF}`);
    // The editor treats every 412 as a conflict, so the conflict view must show with the cut-off line.
    expect(out.includes("CONFLICT")).toBe(status === 412);
    expect(out).not.toContain("Go to field");
  }
  w.outcome = { kind: "refused", status: 412, problem: null, replayed: false };
  expect(renderToStaticMarkup(h(WritesProvider, { value: new Writes(() => "p") }, h(WriteResult, { w, done: () => "DONE", conflict: h("p", null, "CONFLICT") })))).toContain("CONFLICT");
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
    const stop = watchWrites("drf_11", written);
    const settle = settledFragment("drf_11", () => "e1");
    const w = Object.assign(new Write({ method: "PUT", path: "/x", body: {}, resource: "/x" }), { epoch: "e1" });
    settle({ kind: "refused", status: 412, problem: null, replayed: false }, w);
    expect(written).not.toHaveBeenCalled();
    settle({ kind: "done", status: 200, data: {}, replayed: false, etag: '"3-t"' }, w);
    expect(keptETag("drf_11", "e1")).toBe('"3-t"');
    expect(written).toHaveBeenCalledTimes(1);
    stop();
  });

  test("ginsys/bronzeward#534 C1: a Save that settles after its page closed reads the draft again on the page shown now, which shows the saved text as a conflict", () => {
    const [sender, reopened, otherDraft] = [vi.fn(), vi.fn(), vi.fn()];
    const settle = settledFragment("drf_12", () => "e1");
    // The operator saves, leaves before the Save commits, and opens the draft again: the new page
    // read the old revision.
    const leave = watchWrites("drf_12", sender);
    leave();
    const stop = watchWrites("drf_12", reopened);
    const stopOther = watchWrites("drf_13", otherDraft);
    const w = Object.assign(new Write({ method: "PUT", path: "/x", body: {}, resource: "/x" }), { epoch: "e1" });
    settle({ kind: "done", status: 200, data: {}, replayed: false, etag: '"4-t"' }, w);
    expect(sender).not.toHaveBeenCalled();
    expect(reopened).toHaveBeenCalledTimes(1);
    expect(otherDraft).not.toHaveBeenCalled();
    stop();
    stopOther();
    // That read names the Save's revision. The pane holds text typed since it opened: the read shows
    // the saved text and keeps the typed text aside, so no Save sends it over that revision unseen.
    const [old, typed, saved] = ["a: old\n", "a: typed\n", "a: saved\n"].map((document) => ({ ...EMPTY, document })) as [Held, Held, Held];
    const s = editor({ held: typed, saved: old }, { kind: "read", text: saved, since: 0, revision: 9 });
    expect(s.held).toBe(saved);
    expect(s.conflict).toEqual({ mine: typed, phase: "theirs", revision: 9 });
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
      h(ReadsProvider, { value: reads }, h(FragmentPane, { draft: { ...draft, state }, entry: undefined, seeChange: () => {} })))));

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
