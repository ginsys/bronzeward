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
// A document marker is followed by a space, a tab or the end of the line: U+00A0 and the other
// Unicode spaces are text (`--- a` is a key).
const MARKER = /^(?:---|\.\.\.)(?:[ \t]|$)/;
// go-yaml (the server's parser) breaks a line at a line feed, a carriage return alone, and U+0085,
// U+2028 and U+2029, in comments and in every kind of scalar; a CRLF is one break. Each of these is
// read as a line feed here, so no later step has to know them. A replacement has the length of what
// it replaces (each is one UTF-16 code unit, the unit the editor reports positions in), so the
// positions found here are positions in the text given (ginsys/bronzeward#544).
const BREAKS = /\r(?!\n)|[\u0085\u{2028}\u{2029}]/gu;
function tags(original: string): { index: number; tag: string; name: string }[] {
  const text = original.replace(BREAKS, "\n");
  let masked = "";
  const lines = text.split("\n");
  const { starts, props } = commentStarts(lines);
  lines.forEach((line, n) => {
    const end = starts[n]!;
    masked += `${line.slice(0, end)}${" ".repeat(line.length - end)}\n`;
  });
  const found: { index: number; tag: string; name: string }[] = [];
  // A plain scalar's words: no blank, flow indicator, or colon before a blank; blanks between words.
  const word = String.raw`(?:[^ \t\r\n,[\]{}:]|:(?=[^ \t\r\n,[\]{}]))+`;
  // A block scalar's header ends its line, a comment aside: | or >, then at most one chomping and
  // one indentation indicator, in either order. The server also takes a # right after it as a
  // comment, which the blanking above leaves.
  const header = String.raw`([|>](?:[-+][1-9]?|[1-9][-+]?)?)(?=[ \t]*(?:#[^\n]*)?\r?\n)`;
  const key = /[ \t]*:(?:[ \t\r\n]|$)/y;
  const tag = new RegExp(String.raw`!bwref[ \t\r\n]+(?:"((?:[^"\\]|\\[^])*)"|'((?:[^'\n]|'')*)'|${header}|(${word}(?:[ \t]+${word})*))`, "y");
  // A tag counts only where the scan read it as a node's property: never in a plain, quoted or block
  // scalar's text. So the pattern is matched at those offsets only: a match that starts in text could
  // run over a real tag.
  for (const at of [...props].sort((a, b) => a - b)) {
    tag.lastIndex = at;
    const m = tag.exec(masked);
    if (m === null) continue;
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

// Where each line's comment starts: a # at a place a comment can start, or the line's length if
// the line has none. What a line is depends on the lines before it, so the lines are read in order
// with the state in `Carry`. It is a reading of YAML's node structure, enough to tell where a
// scalar starts and ends, and no more: it does not check that the document is valid.
interface Carry {
  // Columns of the open block entries: a key, or the indicator of a "- ", "? " or ": " entry.
  stack: number[];
  // Depth of the flow collections open ([ or {), across lines.
  flow: number;
  // A node starts at the next token (after an indicator, a property, a flow indicator or a line
  // start); false inside a scalar or after one.
  ns: boolean;
  // A scalar that goes on in the next line: an open quote, or a plain scalar that a more indented
  // line can continue (in block context; in flow context `ns` alone says it).
  open: "" | '"' | "'" | "plain";
  // The column the next line must exceed to continue the open plain scalar.
  holder: number;
  // An open block scalar: its content is more indented than the holder (YAML 1.2 8.1).
  block: { holder: number; digit: number; indent: number } | undefined;
  // The last token was a quoted scalar, a flow collection or an alias, which takes an adjacent `:`
  // in flow context.
  closed: boolean;
}
const freshCarry = (): Carry => ({ stack: [], flow: 0, ns: true, open: "", holder: -1, block: undefined, closed: false });
const isWs = (c: string | undefined) => c === " " || c === "\t" || c === "\r";
const FLOW = ",[]{}";
// A block scalar header after its indicator. go-yaml reads the indicators only directly after `|` or
// `>`: a chomping sign and then a digit 1-9, or the digit and then the sign (a digit 0 is an error),
// then blanks and an optional comment (yaml_parser_scan_block_scalar, scannerc.go lines 2204-2274,
// go.yaml.in/yaml/v3 v3.0.5). Group 1 holds the indicators, so a digit in the comment is not one.
const HEADER_REST = /^((?:[-+][1-9]?|[1-9][-+]?)?)[ \t\r]*(?:#.*)?$/;

// The index after the closing quote `q` at or after `from`, or -1 if the line ends first.
function skipQuoted(line: string, from: number, q: string): number {
  for (let i = from; i < line.length; i++) {
    const c = line[i];
    if (q === '"' && c === "\\") i++;
    else if (c === q) {
      if (q === "'" && line[i + 1] === "'") i++;
      else return i + 1;
    }
  }
  return -1;
}

// The end of the plain scalar that starts at `from`: before a colon that makes it a key, before a
// flow indicator in flow context, before a # that follows a blank.
function plainEnd(line: string, from: number, flow: number): number {
  let i = from;
  for (; i < line.length; i++) {
    const c = line[i]!;
    if (c === ":" && (i + 1 >= line.length || isWs(line[i + 1]) || (flow > 0 && FLOW.includes(line[i + 1]!)))) break;
    if (flow > 0 && FLOW.includes(c)) break;
    if (c === "#" && i > from && isWs(line[i - 1])) break;
  }
  return i;
}

function pushEntry(stack: number[], col: number) {
  while (stack.length > 0 && stack[stack.length - 1]! > col) stack.pop();
  if (stack.length === 0 || stack[stack.length - 1]! < col) stack.push(col);
}

// The comment start of every line, and the offsets in the text of the !bwref properties read.
function commentStarts(lines: readonly string[]): { starts: number[]; props: Set<number> } {
  const st = freshCarry();
  const props = new Set<number>();
  let base = 0;
  const starts = lines.map((line, n) => {
    // go-yaml drops a byte order mark at the start of the stream without counting a column (the
    // reader), and a second one right after it takes a column, like a blank (the scanner, at
    // index 0). A byte order mark anywhere else is text.
    const lead = n === 0 && line.startsWith("﻿");
    const rest = lead ? line.slice(1) : line;
    const at = scanLine(st, lead && rest.startsWith("﻿") ? ` ${rest.slice(1)}` : rest, base + (lead ? 1 : 0), props);
    base += line.length + 1;
    return lead ? at + 1 : at;
  });
  return { starts, props };
}

function scanLine(st: Carry, line: string, base: number, props: Set<number>): number {
  // YAML indents with U+0020 only, and go-yaml ends an empty line at a line break after those spaces
  // (yaml_parser_scan_block_scalar_breaks, scannerc.go lines 2393-2410, go.yaml.in/yaml/v3 v3.0.5). A
  // tab or another Unicode space (U+00A0, say) is text, which `trim` would count as blank.
  const blank = /^ *\r?$/.test(line);
  const ind = spaces(line);
  const marker = MARKER.test(line);
  // A block scalar's content is no comment, however it looks, and no tag either: a !bwref in it is
  // part of a string, never in `props`.
  if (st.block !== undefined) {
    if (blank) return line.length;
    const b = st.block;
    if (!marker && (b.indent < 0 ? ind > b.holder : ind >= b.indent)) {
      if (b.indent < 0) b.indent = ind;
      return line.length;
    }
    st.block = undefined;
    st.ns = true;
  }
  let j = 0;
  let hold = st.stack.length > 0 ? st.stack[st.stack.length - 1]! : -1;
  let lastPlain = false;
  // In block context a line starts a new node. In flow context a key that closed at the end of the
  // previous line (`? "k"`) still takes a `:` that starts this one.
  if (st.flow === 0) st.closed = false;
  let fresh = false;
  if (marker && st.open !== '"' && st.open !== "'") {
    Object.assign(st, freshCarry());
    // A marker ends every node: the column of the entry above (`hold`) is no longer open.
    hold = -1;
    j = 3;
  } else if (st.open === '"' || st.open === "'") {
    const e = skipQuoted(line, 0, st.open);
    if (e < 0) return line.length;
    st.open = "";
    st.ns = false;
    st.closed = true;
    j = e;
  } else if (st.open === "plain") {
    if (blank) return line.length;
    st.open = "";
    if (ind > st.holder && !/^[ \t]*#/.test(line)) {
      st.ns = false;
      lastPlain = true;
      hold = st.holder;
      j = ind;
    } else {
      st.ns = true;
      fresh = true;
    }
  } else if (st.flow === 0) {
    st.ns = true;
    fresh = true;
  }
  // A blank line or a comment-only line closes no block entry, whatever its indentation.
  if (fresh && st.flow === 0 && !blank && !/^ *#/.test(line)) {
    while (st.stack.length > 0 && st.stack[st.stack.length - 1]! > ind) st.stack.pop();
    hold = st.stack.length > 0 ? st.stack[st.stack.length - 1]! : -1;
  }
  // The column where the current node starts, with its properties: the column of a key.
  let nodeStart = -1;
  // The start of the node whose flow collection opens on this line: the commas and the items inside
  // do not move it, so `[a, b]: |` is a key that starts at the bracket (-2: none opens here).
  let outerStart = -2;
  while (j < line.length) {
    const c = line[j]!;
    const next = line[j + 1];
    if (isWs(c)) {
      j++;
      continue;
    }
    // go-yaml reads a # as a comment where a token starts, also right after a flow indicator.
    // That is after any token but a plain scalar: a closing quote, bracket or alias, say.
    if (c === "#" && (j === 0 || isWs(line[j - 1]) || st.ns || !lastPlain)) {
      // A comment ends the token before it, so in flow context a `:` that starts the next line is
      // the value indicator, also after a plain key.
      if (st.flow > 0) st.closed = true;
      return j;
    }
    if (st.ns) {
      if (c === '"' || c === "'") {
        if (nodeStart < 0) nodeStart = j;
        const e = skipQuoted(line, j + 1, c);
        if (e < 0) {
          st.open = c;
          return line.length;
        }
        j = e;
        st.ns = false;
        lastPlain = false;
        st.closed = true;
      } else if (c === "[" || c === "{") {
        if (nodeStart < 0) nodeStart = j;
        st.flow++;
        if (st.flow === 1) outerStart = nodeStart;
        lastPlain = false;
        j++;
      } else if (st.flow > 0 && c === ",") {
        // An empty node (after a property or an indicator) ends here.
        nodeStart = -1;
        lastPlain = false;
        j++;
      } else if (st.flow > 0 && (c === "]" || c === "}")) {
        st.flow--;
        if (st.flow === 0 && outerStart !== -2) nodeStart = outerStart;
        st.ns = false;
        lastPlain = false;
        st.closed = true;
        j++;
      } else if (c === "!" || c === "&" || c === "*") {
        if (nodeStart < 0) nodeStart = j;
        // go-yaml reads a tag up to a blank (a verbatim tag, !<...>, up to its >), also in flow
        // context. It reads an anchor or an alias name with is_alpha only: 0-9, A-Z, a-z, _ and -
        // (yaml_parser_scan_anchor, scannerc.go line 1912; is_alpha, yamlprivateh.go line 48,
        // go.yaml.in/yaml/v3 v3.0.5). So `*k:"x` ends the alias at the colon.
        let e = j + 1;
        if (c === "!" && next === "<" && line.indexOf(">", j) >= 0) e = line.indexOf(">", j) + 1;
        else if (c === "!") while (e < line.length && !isWs(line[e])) e++;
        else while (e < line.length && /[0-9A-Za-z_-]/.test(line[e]!)) e++;
        if (line.slice(j, e) === "!bwref") props.add(base + j);
        j = e;
        lastPlain = false;
        if (c === "*") {
          st.ns = false;
          st.closed = true;
        }
      } else if (((c === "?" || c === ":") && st.flow > 0) || ((c === "-" || c === "?" || c === ":") && (next === undefined || isWs(next)))) {
        if (st.flow === 0) {
          // A colon that follows properties alone (`&e:`) closes a key that starts at the first one.
          const col = c === ":" && nodeStart >= 0 ? nodeStart : j;
          pushEntry(st.stack, col);
          hold = col;
        }
        nodeStart = -1;
        lastPlain = false;
        j++;
      } else if ((c === "|" || c === ">") && st.flow === 0 && HEADER_REST.test(line.slice(j + 1))) {
        const rest = line.slice(j + 1);
        const digit = /[1-9]/.exec((HEADER_REST.exec(rest) as RegExpExecArray)[1]!);
        st.block = { holder: hold, digit: digit === null ? 0 : Number(digit[0]), indent: -1 };
        if (st.block.digit > 0) st.block.indent = hold >= 0 ? hold + st.block.digit : st.block.digit;
        const hash = rest.indexOf("#");
        return hash < 0 ? line.length : j + 1 + hash;
      } else {
        if (nodeStart < 0) nodeStart = j;
        j = Math.max(plainEnd(line, j, st.flow), j + 1);
        st.ns = false;
        lastPlain = true;
        st.closed = false;
      }
      continue;
    }
    if (c === ":" && (next === undefined || isWs(next) || (st.flow > 0 && (FLOW.includes(next) || st.closed)))) {
      if (st.flow === 0 && nodeStart >= 0) {
        pushEntry(st.stack, nodeStart);
        hold = nodeStart;
      }
      st.ns = true;
      nodeStart = -1;
      lastPlain = false;
      st.closed = false;
      j++;
    } else if (st.flow > 0 && c === ",") {
      st.ns = true;
      nodeStart = -1;
      lastPlain = false;
      st.closed = false;
      j++;
    } else if (st.flow > 0 && (c === "]" || c === "}")) {
      st.flow--;
      if (st.flow === 0 && outerStart !== -2) nodeStart = outerStart;
      lastPlain = false;
      st.closed = true;
      j++;
    } else if (st.flow > 0 && (c === "[" || c === "{")) {
      st.flow++;
      st.ns = true;
      j++;
    } else {
      j = Math.max(plainEnd(line, j, st.flow), j + 1);
      lastPlain = true;
      st.closed = false;
    }
  }
  if (st.flow === 0 && st.ns === false && lastPlain) {
    st.open = "plain";
    st.holder = hold;
  }
  return line.length;
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
  return (e.literal ? literalSpot(text, e, name) : undefined) ?? e.spot;
}

// The place of the first tag `name` in a literal block's own lines, scanned as the document they
// hold: the header line is blanked and every content line loses the block's indentation, so a
// marker (---) or a flow collection inside it reads as at the top of a document. The scan maps its
// offsets back to the text. The indentation is the leading spaces of the first line with text in
// the source minus those of the decoded value's, so an indentation indicator (`|2`) and extra
// spaces in the content count right. A blank line loses at most that many spaces. A line with text
// that has fewer ends the scalar in go-yaml: nothing is returned then.
function literalSpot(text: string, e: { text: string; spot: Spot }, name: string): Spot | undefined {
  const blank = (l: string) => /^ *\r?$/.test(l);
  // A lone carriage return, U+0085, U+2028 and U+2029 break a line in go-yaml (see BREAKS); each
  // is read as a line feed here, with the same length, so the offsets stay the text's.
  const lines = text.slice(e.spot.start, e.spot.end).replace(BREAKS, "\n").split("\n");
  const body = lines.slice(1);
  const src = body.find((l) => !blank(l));
  const dec = e.text.split("\n").find((l) => !blank(l));
  if (src === undefined || dec === undefined) return undefined;
  const cut = Math.max(spaces(src) - spaces(dec), 0);
  // The header line is left out, so the first content line is the first line of the document: a
  // byte order mark at its start is the stream's.
  const kept: string[] = [];
  const removed: number[] = [];
  for (const l of body) {
    const n = Math.min(cut, spaces(l));
    if (n < cut && !blank(l)) return undefined;
    kept.push(l.slice(n));
    removed.push(n);
  }
  const shown = tags(kept.join("\n")).find((t) => t.name === name);
  if (shown === undefined) return undefined;
  // The offset in the text of the character at `i` in the scanned lines.
  const back = (i: number) => {
    let scanned = 0;
    let source = lines[0]!.length + 1;
    for (let k = 0; k < kept.length; k++) {
      if (i <= scanned + kept[k]!.length) return e.spot.start + source + removed[k]! + (i - scanned);
      scanned += kept[k]!.length + 1;
      source += body[k]!.length + 1;
    }
    return e.spot.end;
  };
  return { start: back(shown.index), end: back(shown.index + shown.tag.length - 1) + 1 };
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
