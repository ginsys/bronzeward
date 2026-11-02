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
  // Each segment follows the node it steps into: a list's index, or a mapping's key as the server
  // reads it, a string, whether the YAML spells it 8080, "8080" or 0.
  let node: unknown = doc.contents;
  for (const k of m[2] === "" ? [] : m[2]!.slice(1).split("/").map(segment)) {
    if (isSeq(node)) node = /^(0|[1-9]\d*)$/.test(k) ? node.items[Number(k)] : undefined;
    else if (isMap(node)) node = node.items.find((p) => isScalar(p.key) && String(p.key.value) === k)?.value;
    else node = undefined;
  }
  if (!isNode(node) || node.range == null) return undefined;
  return { start: node.range[0], end: node.range[1] };
}

// The reference tags in the text, outside comments: a reference name is the §5.1 grammar's
// characters (internal/ingest/declare.go), so a flow list's comma or bracket ends it.
function tags(text: string): { index: number; tag: string; name: string }[] {
  const found: { index: number; tag: string; name: string }[] = [];
  let at = 0;
  for (const line of text.split("\n")) {
    const end = at + commentStart(line);
    for (const m of text.slice(at, end).matchAll(/!bwref[ \t]+([a-z0-9][a-z0-9/-]*)/g)) found.push({ index: at + m.index, tag: m[0], name: m[1]! });
    at += line.length + 1;
  }
  return found;
}

// Where a line's comment starts: a # at its start or after a blank, outside quotes; the line's
// length if it has none.
function commentStart(line: string): number {
  let quote: string | undefined;
  for (let i = 0; i < line.length; i++) {
    const c = line[i]!;
    if (quote !== undefined) {
      if (c === quote) quote = undefined;
      else if (c === "\\" && quote === '"') i++;
    } else if (c === '"' || c === "'") quote = c;
    else if (c === "#" && (i === 0 || line[i - 1] === " " || line[i - 1] === "\t")) return i;
  }
  return line.length;
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
