// Where a refusal points in a document the operator typed, for [Go to field] (#257 B13), and the
// references a document carries. The `yaml` package parses the text for positions and for the
// decoded text of embedded documents only: nothing is re-encoded, stored or sent. It is imported
// with this module, not on the first lookup: the references of an embedded document are read on
// every render of the fragment and assignment panes, so an import that resolves later would show a
// declared reference as not in the document until it does (ginsys/bronzeward#569).
import { isAlias, isMap, isNode, isScalar, isSeq, parseAllDocuments } from "yaml";

export interface Spot {
  start: number;
  end: number;
}

// An embedded document a fragment declares (internal/ingest/declare.go Embedded): the path of the
// string scalar that holds it.
export interface EmbeddedPath {
  path: string;
}

// One JSON Pointer segment, unescaped (RFC 6901): ~1 is "/", ~0 is "~".
const segment = (s: string) => s.replace(/~1/g, "/").replace(/~0/g, "~");

// The node at a compilation §2.2 path in parsed documents: doc[<n>], then a JSON Pointer inside
// that document. The part after "|" points into an embedded document, which is a string here, so
// the field holding it is the node. Each segment follows the node it steps into: a list's index,
// or a mapping's key by its text, unquoted, as the server compares it (internal/ingest/path.go):
// 8080 and "8080" match "8080", and 01 does not match "1". As on the server, an alias, as a key or
// as a node, stands for the node its anchor names.
function nodeAt(docs: ReturnType<typeof parseAllDocuments>, path: string): unknown {
  const m = /^doc\[(\d+)\]((?:\/[^|]*)?)/.exec(path);
  if (m === null) return undefined;
  const doc = Array.isArray(docs) ? docs[Number(m[1])] : undefined;
  if (doc === undefined) return undefined;
  const deref = (n: unknown): unknown => (isAlias(n) ? n.resolve(doc) : n);
  let node: unknown = deref(doc.contents);
  for (const k of m[2] === "" ? [] : m[2]!.slice(1).split("/").map(segment)) {
    if (isSeq(node)) node = /^(0|[1-9]\d*)$/.test(k) ? node.items[Number(k)] : undefined;
    else if (isMap(node))
      node = node.items.find((p) => {
        const key = deref(p.key);
        return isScalar(key) && (key.source ?? String(key.value)) === k;
      })?.value;
    else node = undefined;
    node = deref(node);
  }
  return node;
}

// The documents of the text, parsed with every line break the server breaks a line at read as a
// line feed (BREAKS, below), so positions stay positions in the text given.
const parse = (text: string) => parseAllDocuments(text.replace(BREAKS, "\n"));

export async function spotOfPath(text: string, path: string): Promise<Spot | undefined> {
  const node = nodeAt(parse(text), path);
  if (!isNode(node) || node.range == null) return undefined;
  return { start: node.range[0], end: node.range[1] };
}

// The embedded documents of the text that a fragment declares: each declared path that holds a
// string scalar, with that scalar's decoded text and its place in the text. As the server
// (internal/ingest/declare.go embeddedDocument), a scalar with a tag other than !!str holds no
// embedded document, and a path into an embedded document (with "|") declares none. The
// non-specific tag ! makes a quoted or block scalar a !!str (go-yaml), and a plain scalar cannot
// hold a reference's tag. A path that holds no string here adds nothing; the server refuses it
// (rule embedded).
//
// Known difference: go-yaml keeps U+2028 and U+2029 in a scalar's value, and the embedded
// document's own parse breaks a line there, while `parse` reads them as line feeds that fold. So in
// a folded or quoted holder, a line after one of them can be read here as part of the line before.
// The server's reading stands. Matching it needs a copy of go-yaml's line handling.
const STR = "tag:yaml.org,2002:str";
function embeddedTexts(text: string, embedded: readonly EmbeddedPath[]): { text: string; spot: Spot; literal: boolean }[] {
  if (embedded.length === 0) return [];
  const docs = parse(text);
  const found: { text: string; spot: Spot; literal: boolean }[] = [];
  for (const { path } of embedded) {
    const node = path.includes("|") ? undefined : nodeAt(docs, path);
    if (!isScalar(node) || typeof node.value !== "string" || (node.tag !== undefined && node.tag !== STR && node.tag !== "!") || node.range == null) continue;
    found.push({ text: node.value, spot: { start: node.range[0], end: node.range[1] }, literal: node.type === "BLOCK_LITERAL" });
  }
  return found;
}

// The reference tags in the text, outside comments: a reference name is the §5.1 grammar's
// characters (internal/ingest/declare.go), so a flow list's comma or bracket ends it. The name may
// be quoted, and may stand on a line after its tag (ginsys/bronzeward#517 H16). Comments are
// blanked first, keeping every offset, so a match can cross a line without reading one. The whole
// scalar after the tag is read and decoded, then held to the server's grammar: a scalar followed
// by a colon and a blank is a key, never the tag's value, and one with other characters names no
// reference. A scalar written over several lines is read whole: a double-quoted one decodes an
// escaped line break to nothing (ginsys/bronzeward#534 C7), and a line break YAML folds into a
// blank names nothing. A plain one followed by a more indented line is skipped, never cut short.
const NAME = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\/[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$/;
const MARKER = /^(?:---|\.\.\.)(?:\s|$)/;
// go-yaml (the server's parser) breaks a line at a line feed, a carriage return alone, and U+0085,
// U+2028 and U+2029, in comments and in every kind of scalar; a CRLF is one break. Each of these is
// read as a line feed here, so no later step has to know them. A replacement has the length of what
// it replaces (each is one UTF-16 code unit, the unit the editor reports positions in), so the
// positions found here are positions in the text given (ginsys/bronzeward#544).
const BREAKS = /\r(?!\n)|[\u0085\u{2028}\u{2029}]/gu;
// A line that ends with a block scalar's header after a blank or at its start, node properties
// allowed before it. The server also takes a # right after the header as a comment.
const HEADER_END = /(?:^|[ \t])(?:[!&][^ \t]*[ \t]+)*[|>][-+1-9]{0,2}[ \t]*(?:#.*)?$/;
// A line that ends where a node can follow on the next line: after a colon, a sequence entry's or
// a complex key's indicator, a node property, or a document marker.
const OPEN_END = /(?::|(?:^|[ \t])(?:[-?]|[!&][^ \t]*))$|^(?:---|\.\.\.)$/;
// The column of the deepest block node a line starts: the key after its sequence dashes
// (`- a: 1` starts a mapping at column 2), else its last dash (`- x` is an entry at column 0, so
// a more indented line continues x), else its indentation.
function column(kept: string): number {
  const dashes = /^[ \t]*(?:-[ \t]+)*/.exec(kept)![0].length;
  if (dashes === indent(kept) || /:(?:[ \t]|$)/.test(kept.slice(dashes))) return dashes;
  return kept.lastIndexOf("-", dashes - 1);
}
function tags(original: string): { index: number; tag: string; name: string }[] {
  const text = original.replace(BREAKS, "\n");
  // The depth of the open flow collections carries from line to line (ginsys/bronzeward#578).
  // Only a line where a node can start opens one at its start: a line inside a flow collection, a
  // line after one that ends open (OPEN_END), and a line no more indented than the last line that
  // started a node. A more indented line continues a scalar. A block scalar's content lines, as
  // deep as its first one, open none, and a document marker closes every collection.
  let masked = "";
  let flow = 0;
  let content = -1; // the content's indentation; 0 before its first line with text; -1 outside one
  let headed = 0; // the indentation of the line that holds the block scalar's header
  let base = 0;
  let open = true;
  for (const line of text.split("\n")) {
    if (content !== -1 && line.trim() !== "") {
      if (content === 0 && indent(line) > headed) content = indent(line);
      else if (content === 0 || indent(line) < content) content = -1;
    }
    const held = content !== -1;
    if (MARKER.test(line)) [flow, open, base] = [0, true, 0];
    const fresh = !held && flow === 0 && (open || indent(line) <= base);
    const c = commentStart(line, held ? 0 : flow, held || flow > 0 || fresh);
    if (!held) {
      const kept = line.slice(0, c.end).trimEnd();
      if (fresh && kept.trim() !== "") base = column(kept);
      if (kept.trim() !== "") open = OPEN_END.test(kept);
      flow = c.flow;
      if (flow === 0 && HEADER_END.test(kept)) [content, headed] = [0, indent(line)];
    }
    masked += `${line.slice(0, c.end)}${" ".repeat(line.length - c.end)}\n`;
  }
  const found: { index: number; tag: string; name: string }[] = [];
  // A plain scalar's words: no blank, flow indicator, or colon before a blank; blanks between words.
  const word = String.raw`(?:[^\s,[\]{}:]|:(?=[^\s,[\]{}]))+`;
  // A block scalar's header ends its line, a comment aside: | or >, then at most one chomping and
  // one indentation indicator, in either order. The server also takes a # right after it as a
  // comment, which the blanking above leaves.
  const header = String.raw`([|>](?:[-+][1-9]?|[1-9][-+]?)?)(?=[ \t]*(?:#[^\n]*)?\r?\n)`;
  const key = /[ \t]*:(?:\s|$)/y;
  for (const m of masked.matchAll(new RegExp(String.raw`!bwref\s+(?:"((?:[^"\\]|\\[^])*)"|'((?:[^'\n]|'')*)'|${header}|(${word}(?:[ \t]+${word})*))`, "g"))) {
    let end = m.index + m[0].length;
    key.lastIndex = end;
    if (key.test(masked)) continue;
    let name: string | undefined;
    if (m[3] !== undefined) {
      const b = block(text, masked, m.index, end, m[3]);
      if (b === undefined) continue;
      ({ name, end } = b);
    } else if (m[4] !== undefined) {
      if (continues(masked, m.index, end)) continue;
      name = m[4];
    } else name = m[1] !== undefined ? decoded(m[1]) : m[2]!.replaceAll("''", "'");
    if (name !== undefined && NAME.test(name)) found.push({ index: m.index, tag: original.slice(m.index, end), name });
  }
  return found;
}

const indent = (line: string) => line.length - line.trimStart().length;
// The line that starts at `start`, without its line break.
function lineAt(s: string, start: number): string {
  const end = s.indexOf("\n", start);
  return s.slice(start, end === -1 ? undefined : end);
}
// The first line at or after `start` that holds text. Each scan stops there, so the scan per tag
// does not read the rest of the document (ginsys/bronzeward#534 N3).
function nextLine(s: string, start: number): string | undefined {
  for (let at = start; at < s.length; ) {
    const l = lineAt(s, at);
    if (l.trim() !== "") return l;
    at += l.length + 1;
  }
  return undefined;
}

// The indentation of the node that holds the scalar tagged at `tag`: the key or entry on the tag's
// line. With `compact`, after sequence dashes (`- - a: !bwref`) that is the column of the key, or of
// the last dash when the tag follows it directly, also after an anchor (`- &e !bwref |-`), which
// belongs to the entry: a compact mapping's keys sit at the column after the dash
// (ginsys/bronzeward#544). Only a block scalar's content is bound by that column. A plain
// scalar's next line is read against the line's own indentation, as before, so that a form go-yaml
// refuses (`- a: !bwref x/foo` then `  y`) still names nothing. A tag that starts its line is the
// value of the nearest line above that is less indented, so the next line may continue it at the
// tag's own indentation; -1 if no line is above.
function holder(masked: string, tag: number, compact = false): number {
  const start = masked.lastIndexOf("\n", tag) + 1;
  const own = indent(lineAt(masked, start));
  const before = masked.slice(start, tag);
  if (before.trim() !== "") {
    if (!compact) return own;
    const dashes = /^ *(?:-(?: +|$))*/.exec(before)![0];
    if (dashes.length === own) return own;
    const rest = before.slice(dashes.length);
    return /^(?:&[A-Za-z0-9_-]+\s+)*$/.test(rest) ? dashes.lastIndexOf("-") : dashes.length;
  }
  for (let e = start - 1; e >= 0; ) {
    const s = e === 0 ? 0 : masked.lastIndexOf("\n", e - 1) + 1;
    const l = masked.slice(s, e);
    if (l.trim() !== "" && indent(l) < own) return indent(l);
    e = s - 1;
  }
  return -1;
}

// Whether a plain scalar ending at `end` goes on to the next line: it ends its line, and the next
// line with text is more indented than the node that holds the scalar and is no key, sequence
// entry or flow indicator. A plain scalar holds no colon before a blank, so a line with one is a
// key.
function continues(masked: string, tag: number, end: number): boolean {
  const eol = masked.indexOf("\n", end);
  if (eol === -1 || masked.slice(end, eol).trim() !== "") return false;
  const next = nextLine(masked, eol + 1);
  // A document marker (--- or ...) ends every scalar, also a top-level one.
  if (next === undefined || indent(next) <= holder(masked, tag) || MARKER.test(next)) return false;
  const t = next.trim();
  return !/^(?:[,\]}]|[-?](?:\s|$))/.test(t) && !/:(?:\s|$)/.test(t);
}

// A block scalar's name and where it ends (YAML 1.2 §8.1, ginsys/bronzeward#534 C7). It names a
// reference only with one content line, so no line break is inside it, and with none after it:
// strip chomping (-) drops that one, and a line that ends the text has none to keep. That line is
// more indented than the node that holds the scalar, at least one space at the top level as the
// server reads it (go-yaml), and its indentation, in spaces only, is the content's. The scalar goes on until a line with text is
// less indented: a later line with text at that indentation, or a blank line with more than it, is
// content too. The lines are read from the text: a # in them is no comment.
// With an indentation indicator the content's indentation depends on the holding node's column,
// which this scan does not read: such a scalar names nothing here, and the server still checks it.
// A CRLF is one line break: a line's carriage return before its line feed is no text. A blank line
// that holds a tab, before the next line with text, makes go-yaml refuse the whole document, so the
// scalar names nothing (ginsys/bronzeward#544). The other line breaks are read as line feeds before
// this runs (see BREAKS).
const spaces = (line: string) => line.length - line.replace(/^ +/, "").length;
const bare = (line: string) => (line.endsWith("\r") ? line.slice(0, -1) : line);
function block(text: string, masked: string, tag: number, end: number, header: string): { name: string; end: number } | undefined {
  const eol = text.indexOf("\n", end);
  if (/\d/.test(header) || eol === -1) return undefined;
  const start = eol + 1;
  const line = lineAt(text, start);
  const first = bare(line);
  const depth = spaces(first);
  if (first.trim() === "" || depth <= Math.max(holder(masked, tag, true), 0)) return undefined;
  if (!header.includes("-") && start + first.length < text.length) return undefined;
  for (let at = start + line.length + 1; at < text.length; ) {
    const l = lineAt(text, at);
    if (l.trim() !== "") {
      if (spaces(l) >= depth && !MARKER.test(l)) return undefined;
      break;
    }
    // go-yaml refuses a document with a tab on a blank line here, whatever else is on it.
    if (l.includes("\t") || bare(l).length > depth) return undefined;
    at += l.length + 1;
  }
  return { name: first.slice(depth), end: start + first.length };
}

// A double-quoted scalar's text with YAML's escapes decoded (YAML 1.2 §5.7); undefined for an
// escape YAML does not have. An escaped line break (LF or CRLF) and the next line's leading blanks
// are dropped; an unescaped one folds into a blank or a line break, which no name holds.
const ESCAPES: Record<string, string> = { "0": "\0", a: "\x07", b: "\b", t: "\t", "\t": "\t", n: "\n", v: "\v", f: "\f", r: "\r", e: "\x1b", " ": " ", '"': '"', "/": "/", "\\": "\\", N: "\x85", _: "\xa0", L: " ", P: " " };
const HEX: Record<string, number> = { x: 2, u: 4, U: 8 };
function decoded(body: string): string | undefined {
  let out = "";
  for (let i = 0; i < body.length; i++) {
    if (body[i] !== "\\") {
      out += body[i] === "\n" ? " " : body[i];
      continue;
    }
    const k = body[++i] ?? "";
    if (k === "\r" && body[i + 1] === "\n") i++;
    if (body[i] === "\n") {
      while (body[i + 1] === " " || body[i + 1] === "\t") i++;
      continue;
    }
    const n = HEX[k];
    if (n === undefined) {
      if (!Object.hasOwn(ESCAPES, k)) return undefined;
      out += ESCAPES[k];
      continue;
    }
    const digits = body.slice(i + 1, i + 1 + n);
    const code = /^[0-9a-fA-F]+$/.test(digits) && digits.length === n ? parseInt(digits, 16) : -1;
    if (code < 0 || code > 0x10ffff) return undefined;
    out += String.fromCodePoint(code);
    i += n;
  }
  return out;
}

// Where a line's comment starts: a # at its start or after a blank, outside quotes; the line's
// length if it has none. A quote opens a scalar only where one starts, so the apostrophes in
// `can't` and `dogs 'n cats` open none; '' inside a single-quoted scalar is an apostrophe.
// `flow` is the depth of the flow collections ([ {) open at the line's start; the result carries
// the depth at its end, so that the next line is read in the same context (ginsys/bronzeward#578).
// A [ or { opens one only where a node starts, and a ] or } closes one only while one is open.
// `start` says whether a node can start at the line's indentation (see tags).
function commentStart(line: string, flow: number, start: boolean): { end: number; flow: number } {
  let quote: string | undefined;
  let closed = -1;
  for (let i = 0; i < line.length; i++) {
    const c = line[i]!;
    if (quote !== undefined) {
      if (c === "'" && quote === "'" && line[i + 1] === "'") i++;
      else if (c === quote) [quote, closed] = [undefined, i];
      else if (c === "\\" && quote === '"') i++;
    } else if ((c === '"' || c === "'") && startsScalar(line, i, flow > 0, closed, start)) quote = c;
    else if (c === "#" && (i === 0 || blank(line[i - 1]))) return { end: i, flow };
    else if ((c === "[" || c === "{") && startsScalar(line, i, flow > 0, closed, start)) flow++;
    else if ((c === "]" || c === "}") && flow > 0) flow--;
  }
  return { end: line.length, flow };
}

// Whether a node starts at `i`: at the line's indentation or after a document marker where
// `start` allows one, after a blank that follows a key's colon or an item's or a complex key's
// indicator (- ?), or, in a flow collection only, after a flow indicator ([ { ,). In a flow
// collection, as go-yaml reads it, an indicator right after a flow indicator counts, a complex
// key's indicator needs no blank after it, and a colon is a value's indicator, with no blank after
// it, where a token starts: at the line's start, after a blank, a ] or }, or a quoted key
// (`closed` is where the last quote on the line closed). Node properties (a tag, an anchor) and
// the blanks after them may stand between the indicator and the node. Anything else before it is
// a plain scalar already started.
const blank = (c: string | undefined) => c === " " || c === "\t";
function startsScalar(line: string, i: number, flow: boolean, closed: number, start: boolean): boolean {
  const from = MARKER.test(line) ? 3 : 0;
  const opener = (c: string | undefined) => flow && c !== undefined && "[{,".includes(c);
  const indicator = (j: number) => j === from || blank(line[j - 1]) || opener(line[j - 1]);
  const colon = (j: number) => flow && line[j] === ":" && (j === 0 || blank(line[j - 1]) || closed === j - 1 || "]}".includes(line[j - 1]!) || opener(line[j - 1]));
  for (let k = i; ; ) {
    let j = k - 1;
    while (j >= from && blank(line[j])) j--;
    if (j < from) return start;
    if (opener(line[j])) return true;
    if (flow && line[j] === "?" && indicator(j)) return true;
    if (colon(j)) return true;
    if (j === k - 1) return false;
    if (line[j] === ":") return true;
    if ((line[j] === "-" || line[j] === "?") && indicator(j)) return true;
    let s = j;
    while (s > from && !blank(line[s - 1]) && !opener(line[s - 1]) && !colon(s - 1)) s--;
    if (flow && line[s] === "?" && indicator(s)) s++;
    if (line[s] !== "!" && line[s] !== "&") return false;
    k = s;
  }
}

// The tags of the text outside the declared embedded documents, and those documents. A declared
// embedded document's references are read from its decoded text only, as on the server: a tag its
// text shows can be in a comment once the document is decoded, for example a folded line after a
// comment line.
function scan(text: string, embedded: readonly EmbeddedPath[]) {
  const held = embeddedTexts(text, embedded);
  const outer = tags(text).filter((t) => !held.some((e) => t.index >= e.spot.start && t.index < e.spot.end));
  return { outer, held };
}

// The tag of a reference the refusal names: the first `!bwref <name>` in the text. A reference
// only an embedded document carries points at its first tag in a literal block, whose lines are
// the document's lines, and else at the scalar: a folded or quoted scalar's text can show a tag
// that a comment holds once the scalar is decoded.
export function spotOfReference(text: string, name: string, embedded: readonly EmbeddedPath[] = []): Spot | undefined {
  const { outer, held } = scan(text, embedded);
  const t = outer.find((t) => t.name === name);
  if (t !== undefined) return { start: t.index, end: t.index + t.tag.length };
  const e = held.find((e) => tags(e.text).some((t) => t.name === name));
  if (e === undefined) return undefined;
  const shown = e.literal ? tags(text).find((t) => t.name === name && t.index >= e.spot.start && t.index < e.spot.end) : undefined;
  return shown === undefined ? e.spot : { start: shown.index, end: shown.index + shown.tag.length };
}

// The n-th line (from 0) of the marks field.
export function spotOfLine(text: string, n: number): Spot | undefined {
  const lines = text.split("\n");
  if (n < 0 || n >= lines.length) return undefined;
  const start = lines.slice(0, n).reduce((a, l) => a + l.length + 1, 0);
  return { start, end: start + lines[n]!.length };
}

// The references a document carries, each once: those in its text outside its declared embedded
// documents in order, then those in the decoded text of each embedded document it declares, in
// declaration order. The server reads an embedded document's references from its parsed text
// (internal/ingest/declare.go walkEmbedded), so a quoted or folded one carries them although its
// tags are not tags in the outer text.
export const references = (text: string, embedded: readonly EmbeddedPath[] = []) => {
  const { outer, held } = scan(text, embedded);
  return [...new Set([...outer, ...held.flatMap((e) => tags(e.text))].map((t) => t.name))];
};
