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
  const { isNode, parseAllDocuments } = await import("yaml");
  const docs = parseAllDocuments(text);
  const doc = Array.isArray(docs) ? docs[Number(m[1])] : undefined;
  if (doc === undefined) return undefined;
  const keys = m[2] === "" ? [] : m[2]!.slice(1).split("/").map(segment).map((k) => (/^(0|[1-9]\d*)$/.test(k) ? Number(k) : k));
  const node = keys.length === 0 ? doc.contents : doc.getIn(keys, true);
  if (!isNode(node) || node.range == null) return undefined;
  return { start: node.range[0], end: node.range[1] };
}

// The tag of a reference the refusal names: the first `!bwref <name>` in the text.
export function spotOfReference(text: string, name: string): Spot | undefined {
  const m = new RegExp(`!bwref[ \\t]+${name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}(?=\\s|$)`).exec(text);
  return m === null ? undefined : { start: m.index, end: m.index + m[0].length };
}

// The n-th line (from 0) of the marks field.
export function spotOfLine(text: string, n: number): Spot | undefined {
  const lines = text.split("\n");
  if (n < 0 || n >= lines.length) return undefined;
  const start = lines.slice(0, n).reduce((a, l) => a + l.length + 1, 0);
  return { start, end: start + lines[n]!.length };
}

// The references a document carries, in order, each once.
export const references = (text: string) => [...new Set([...text.matchAll(/!bwref[ \t]+(\S+)/g)].map((m) => m[1]!))];
