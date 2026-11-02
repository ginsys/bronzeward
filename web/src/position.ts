// Where a refusal points in a document the operator typed, for [Go to field] (#257 B13). The
// `yaml` package parses the text for positions only: nothing is re-encoded, stored or sent. It is
// loaded on the first lookup, so no other page carries it.

export interface Spot {
  start: number;
  end: number;
}

// One JSON Pointer segment, unescaped (RFC 6901): ~1 is "/", ~0 is "~".
const segment = (s: string) => s.replace(/~1/g, "/").replace(/~0/g, "~");

// A compilation §2.2 path: doc[<n>], then a JSON Pointer inside that document. The part after "|"
// points into an embedded document, which is a string here, so the field holding it is the spot.
export async function spotOfPath(text: string, path: string): Promise<Spot | undefined> {
  const m = /^doc\[(\d+)\]((?:\/[^|]*)?)/.exec(path);
  if (m === null) return undefined;
  const { isMap, isNode, isScalar, isSeq, parseAllDocuments } = await import("yaml");
  const docs = parseAllDocuments(text);
  const doc = Array.isArray(docs) ? docs[Number(m[1])] : undefined;
  if (doc === undefined) return undefined;
  // Each segment follows the node it steps into: a list's index, or a mapping's key by its text,
  // unquoted, as the server compares it (internal/ingest/path.go): 8080 and "8080" match "8080",
  // and 01 does not match "1".
  let node: unknown = doc.contents;
  for (const k of m[2] === "" ? [] : m[2]!.slice(1).split("/").map(segment)) {
    if (isSeq(node)) node = /^(0|[1-9]\d*)$/.test(k) ? node.items[Number(k)] : undefined;
    else if (isMap(node)) node = node.items.find((p) => isScalar(p.key) && (p.key.source ?? String(p.key.value)) === k)?.value;
    else node = undefined;
  }
  if (!isNode(node) || node.range == null) return undefined;
  return { start: node.range[0], end: node.range[1] };
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
function tags(text: string): { index: number; tag: string; name: string }[] {
  let masked = "";
  for (const line of text.split("\n")) {
    const end = commentStart(line);
    masked += `${line.slice(0, end)}${" ".repeat(line.length - end)}\n`;
  }
  const found: { index: number; tag: string; name: string }[] = [];
  // A plain scalar's words: no blank, flow indicator, or colon before a blank; blanks between words.
  const word = String.raw`(?:[^\s,[\]{}:]|:(?=[^\s,[\]{}]))+`;
  // A block scalar's header ends its line: | or >, then its chomping and indentation indicators.
  const header = String.raw`([|>][-+1-9]{0,2})(?=[ \t]*\n)`;
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
    if (name !== undefined && NAME.test(name)) found.push({ index: m.index, tag: text.slice(m.index, end), name });
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
// line. A tag that starts its line is the value of the nearest line above that is less indented,
// so the next line may continue it at the tag's own indentation; -1 if no line is above.
function holder(masked: string, tag: number): number {
  const start = masked.lastIndexOf("\n", tag) + 1;
  const own = indent(lineAt(masked, start));
  if (masked.slice(start, tag).trim() !== "") return own;
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
// reference only with strip chomping (-), which ends it with no line break, and with one content
// line, so none is inside it. That line is more indented than the node that holds the scalar, and
// its indentation is the content's. The lines are read from the text: a # in them is no comment.
// With an indentation indicator the content's indentation depends on the holding node's column,
// which this scan does not read: such a scalar names nothing here, and the server still checks it.
function block(text: string, masked: string, tag: number, end: number, header: string): { name: string; end: number } | undefined {
  if (!header.includes("-") || /\d/.test(header)) return undefined;
  const start = text.indexOf("\n", end) + 1;
  const first = lineAt(text, start);
  if (first.trim() === "" || indent(first) <= holder(masked, tag)) return undefined;
  const after = start + first.length;
  const next = nextLine(text, after + 1);
  if (next !== undefined && indent(next) >= indent(first) && !MARKER.test(next)) return undefined;
  return { name: first.slice(indent(first)), end: after };
}

// A double-quoted scalar's text with YAML's escapes decoded (YAML 1.2 §5.7); undefined for an
// escape YAML does not have. An escaped line break and the next line's leading blanks are dropped;
// an unescaped one folds into a blank or a line break, which no name holds.
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
    if (k === "\n") {
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
function commentStart(line: string): number {
  let quote: string | undefined;
  for (let i = 0; i < line.length; i++) {
    const c = line[i]!;
    if (quote !== undefined) {
      if (c === "'" && quote === "'" && line[i + 1] === "'") i++;
      else if (c === quote) quote = undefined;
      else if (c === "\\" && quote === '"') i++;
    } else if ((c === '"' || c === "'") && startsScalar(line, i)) quote = c;
    else if (c === "#" && (i === 0 || line[i - 1] === " " || line[i - 1] === "\t")) return i;
  }
  return line.length;
}

// Whether a scalar starts at `i`: at the line's indentation, after a flow indicator ([ { ,), or
// after a blank that follows a key's colon or an item's or a complex key's indicator (- ?).
// Anything else before it is a plain scalar already started.
function startsScalar(line: string, i: number): boolean {
  let j = i - 1;
  while (j >= 0 && (line[j] === " " || line[j] === "\t")) j--;
  if (j < 0 || "[{,".includes(line[j]!)) return true;
  if (j === i - 1) return false;
  if (line[j] === ":") return true;
  return (line[j] === "-" || line[j] === "?") && (j === 0 || line[j - 1] === " " || line[j - 1] === "\t");
}

// The tag of a reference the refusal names: the first `!bwref <name>` in the text.
export function spotOfReference(text: string, name: string): Spot | undefined {
  const t = tags(text).find((t) => t.name === name);
  return t === undefined ? undefined : { start: t.index, end: t.index + t.tag.length };
}

// The n-th line (from 0) of the marks field.
export function spotOfLine(text: string, n: number): Spot | undefined {
  const lines = text.split("\n");
  if (n < 0 || n >= lines.length) return undefined;
  const start = lines.slice(0, n).reduce((a, l) => a + l.length + 1, 0);
  return { start, end: start + lines[n]!.length };
}

// The references a document carries, in order, each once.
export const references = (text: string) => [...new Set(tags(text).map((t) => t.name))];
