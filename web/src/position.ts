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
// reference. A scalar written over several lines names none here either: a quoted one is read on
// one line, and a plain one followed by a more indented line is skipped, never cut short.
const NAME = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\/[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$/;
function tags(text: string): { index: number; tag: string; name: string }[] {
  let masked = "";
  for (const line of text.split("\n")) {
    const end = commentStart(line);
    masked += `${line.slice(0, end)}${" ".repeat(line.length - end)}\n`;
  }
  const found: { index: number; tag: string; name: string }[] = [];
  // A plain scalar's words: no blank, flow indicator, or colon before a blank; blanks between words.
  const word = String.raw`(?:[^\s,[\]{}:]|:(?=[^\s,[\]{}]))+`;
  for (const m of masked.matchAll(new RegExp(String.raw`!bwref\s+(?:"((?:[^"\\\n]|\\.)*)"|'((?:[^'\n]|'')*)'|(${word}(?:[ \t]+${word})*))`, "g"))) {
    const end = m.index + m[0].length;
    if (/^[ \t]*:(?:\s|$)/.test(masked.slice(end))) continue;
    if (m[3] !== undefined && continues(masked, m.index, end)) continue;
    const name = m[1] !== undefined ? decoded(m[1]) : m[2] !== undefined ? m[2].replaceAll("''", "'") : m[3];
    if (name !== undefined && NAME.test(name)) found.push({ index: m.index, tag: text.slice(m.index, end), name });
  }
  return found;
}

// Whether a plain scalar ending at `end` goes on to the next line: it ends its line, and the next
// line with text is more indented than the node that holds the scalar and is no key, sequence
// entry or flow indicator. A plain scalar holds no colon before a blank, so a line with one is a
// key. The holder is the key or entry on the tag's line; a tag that starts its line is the value
// of the nearest line above that is less indented, so the next line may continue it at the tag's
// own indentation.
function continues(masked: string, tag: number, end: number): boolean {
  const eol = masked.indexOf("\n", end);
  if (masked.slice(end, eol).trim() !== "") return false;
  const indent = (line: string) => line.length - line.trimStart().length;
  const start = masked.lastIndexOf("\n", tag) + 1;
  const own = indent(masked.slice(start, masked.indexOf("\n", start)));
  let holder = own;
  if (masked.slice(start, tag).trim() === "") {
    const above = masked.slice(0, start).split("\n").reverse().find((l) => l.trim() !== "" && indent(l) < own);
    holder = above === undefined ? -1 : indent(above);
  }
  const next = masked.slice(eol + 1).split("\n").find((l) => l.trim() !== "");
  if (next === undefined || indent(next) <= holder) return false;
  const t = next.trim();
  return !/^(?:[,\]}]|[-?](?:\s|$))/.test(t) && !/:(?:\s|$)/.test(t);
}

// A double-quoted scalar's text with YAML's escapes decoded (YAML 1.2 §5.7); undefined for an
// escape YAML does not have.
const ESCAPES: Record<string, string> = { "0": "\0", a: "\x07", b: "\b", t: "\t", "\t": "\t", n: "\n", v: "\v", f: "\f", r: "\r", e: "\x1b", " ": " ", '"': '"', "/": "/", "\\": "\\", N: "\x85", _: "\xa0", L: " ", P: " " };
const HEX: Record<string, number> = { x: 2, u: 4, U: 8 };
function decoded(body: string): string | undefined {
  let out = "";
  for (let i = 0; i < body.length; i++) {
    if (body[i] !== "\\") {
      out += body[i];
      continue;
    }
    const k = body[++i] ?? "";
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
