// A stored configuration read as fields, for the parsed read-only view of an imported configuration
// (docs/spec/configuration-editing.md §4 items 1, 2 and 4, §5; ginsys/bronzeward#1270). The text is parsed with the
// `yaml` package and each document is read against the schema of the cluster's contract (talos-schema.ts). Nothing
// is re-encoded and nothing is sent: this module builds a tree of what the text says and the schema describes.
//
// What the schema does not describe is never dropped and never reordered. A key the schema lacks, a `$patch`
// directive, an anchor or alias, a tag other than `!bwref`, a value of another type than the schema says, a choice
// the schema does not list, and a document it cannot place each become an "other" row holding the raw lines of that
// key, in document order. Comments are never read, so they never show. A value stored as `!bwref <name>` is the name
// only. The tree holds the stored values, so the page keeps it only as long as it keeps the text (claim.tsx).
//
// No secret list exists here: a value nobody marked is shown as text like any other (the notice above the view says so).

import { isAlias, isMap, isScalar, isSeq, parseAllDocuments, type Document } from "yaml";
import { legacyDef, noFormReasons, placeDocument, type FormSchema, type NoFormReason } from "./talos-schema.ts";

/** What a field holds: text, a reference name, an object, a list of rows or a map of key and value rows. */
export type Node =
  | { readonly kind: "text"; readonly text: string }
  | { readonly kind: "ref"; readonly name: string }
  | { readonly kind: "object"; readonly fields: readonly Field[] }
  | { readonly kind: "list"; readonly rows: readonly Node[] }
  | { readonly kind: "map"; readonly rows: readonly { readonly key: string; readonly node: Node }[] };

/** A labelled item: the key, the schema's description of it as help text, and what it holds. */
export type Field = { readonly name: string; readonly help: string | undefined; readonly node: Node };

/** What the view cannot show as a field: where it is (`machine/network`, or `document`) and its raw lines. */
export type Other = { readonly path: string; readonly lines: string };

/** One document of the stream: its heading, its fields in document order and its Other settings in document order. */
export type DocView = { readonly heading: string; readonly fields: readonly Field[]; readonly other: readonly Other[] };

/** The parsed view, or the first parse error: its code and line only, since the error's own message quotes the text. */
export type ConfigView =
  | { readonly ok: true; readonly docs: readonly DocView[] }
  | { readonly ok: false; readonly code: string; readonly line: number | undefined };

type Sch = { readonly [k: string]: unknown };
type Pairs = { readonly items: readonly { readonly key: unknown; readonly value: unknown }[] };
const isObj = (v: unknown): v is Sch => typeof v === "object" && v !== null && !Array.isArray(v);

// A depth no real configuration reaches; the schema's own definitions can refer to themselves.
const MAX_DEPTH = 16;
const REF = "!bwref";

type Ctx = { readonly text: string; readonly defs: Sch };

// The schema node a `$ref` names, with the referring node's own words (title, description) over the definition's.
function resolve(s: unknown, defs: Sch): Sch | undefined {
  if (!isObj(s)) return undefined;
  const ref = s["$ref"];
  if (typeof ref !== "string") return s;
  if (!ref.startsWith("#/$defs/")) return undefined;
  const name = ref.slice("#/$defs/".length);
  const def = Object.hasOwn(defs, name) ? defs[name] : undefined;
  if (!isObj(def)) return undefined;
  const { $ref: _, ...own } = s;
  return { ...def, ...own };
}

const strOf = (n: unknown, k: string): string | undefined => (isObj(n) && typeof n[k] === "string" ? n[k] : undefined);
const tagOf = (n: unknown) => strOf(n, "tag");
const anchorOf = (n: unknown) => strOf(n, "anchor");
// Where a node sits in the text: its start and the end of its value.
const spanOf = (n: unknown): readonly [number, number] | undefined => {
  const r = isObj(n) ? n["range"] : undefined;
  return Array.isArray(r) && typeof r[0] === "number" && typeof r[1] === "number" ? [r[0], r[1]] : undefined;
};

// Whole lines from the start of the line that holds `start` to the end of the line that holds the last character before `end`.
// A block scalar ends where its own extent ends: the blank lines a keep-chomped one ("|+") holds are part of its value.
function linesOf(text: string, start: number, end: number, exact = false): string {
  const from = text.lastIndexOf("\n", start - 1) + 1;
  if (exact) return text.slice(from, text.charAt(end - 1) === "\n" ? end - 1 : end);
  let e = end;
  while (e > start && /\s/.test(text.charAt(e - 1))) e--;
  const to = text.indexOf("\n", e);
  return text.slice(from, to < 0 ? text.length : to);
}

const isBlockScalar = (n: unknown): boolean => isScalar(n) && (n.type === "BLOCK_LITERAL" || n.type === "BLOCK_FOLDED");

// An anchor, an alias or a tag other than the reference tag: not shown as a field, shown raw. The reference tag is a scalar's
// alone: on a map or a list it would turn the node's own content into ordinary values, so there it is shown raw too.
const marked = (n: unknown): boolean => isAlias(n) || anchorOf(n) !== undefined || (tagOf(n) !== undefined && !(tagOf(n) === REF && isScalar(n)));

const keyOf = (k: unknown): string | undefined => (isScalar(k) && typeof k.value === "string" && anchorOf(k) === undefined && tagOf(k) === undefined ? k.value : undefined);

const hasProperties = (s: Sch | undefined): s is Sch => s !== undefined && isObj(s["properties"]) && Object.keys(s["properties"]).length > 0;

function scalar(s: Sch, node: unknown): Node | null {
  if (!isScalar(node)) return null;
  const v = node.value;
  const type = s["type"];
  const fits =
    type === "string" ? typeof v === "string"
    : type === "integer" ? typeof v === "number" && Number.isInteger(v)
    : type === "boolean" ? typeof v === "boolean"
    : type === undefined && Array.isArray(s["enum"]) && (typeof v === "string" || typeof v === "number" || typeof v === "boolean");
  return fits && (!Array.isArray(s["enum"]) || s["enum"].includes(v)) ? { kind: "text", text: String(v) } : null;
}

// The node a schema places at a YAML node, or null where the view cannot show it as a field. A list or map shows
// whole or not at all, so that no row is dropped and none is reordered.
function walk(ctx: Ctx, sch: unknown, node: unknown, depth: number): Node | null {
  const s = resolve(sch, ctx.defs);
  if (depth > MAX_DEPTH || s === undefined || node === null || node === undefined) return null;
  if (isScalar(node) && tagOf(node) === REF && anchorOf(node) === undefined) {
    return typeof node.value === "string" && node.value !== "" ? { kind: "ref", name: node.value } : null;
  }
  if (marked(node)) return null;
  for (const alt of ["oneOf", "anyOf"] as const) {
    const branches = s[alt];
    if (Array.isArray(branches)) {
      for (const b of branches) {
        const got = walk(ctx, b, node, depth + 1);
        if (got !== null) return got;
      }
      return null;
    }
  }
  switch (s["type"]) {
    case "array": {
      if (!isSeq(node)) return null;
      const rows: Node[] = [];
      for (const item of node.items) {
        const got = walk(ctx, s["items"], item, depth + 1);
        if (got === null) return null;
        rows.push(got);
      }
      return { kind: "list", rows };
    }
    case "object": {
      if (!isMap(node)) return null;
      if (hasProperties(s)) {
        const fields = fieldsOf(ctx, s, node, "", null, depth + 1);
        return fields === null ? null : { kind: "object", fields };
      }
      const values = isObj(s["additionalProperties"]) ? s["additionalProperties"] : isObj(s["patternProperties"]) ? Object.values(s["patternProperties"])[0] : undefined;
      if (values === undefined) return null;
      const rows: { key: string; node: Node }[] = [];
      for (const pair of node.items) {
        const key = keyOf(pair.key);
        // A `$patch` directive is never a row of the map it sits in.
        const got = key === undefined || key === "$patch" ? null : walk(ctx, values, pair.value, depth + 1);
        if (key === undefined || got === null) return null;
        rows.push({ key, node: got });
      }
      return { kind: "map", rows };
    }
    default:
      return scalar(s, node);
  }
}

// The schema's description of a property: the referring node's own, else the definition's.
const helpOf = (prop: unknown, resolved: Sch | undefined): string | undefined => (strOf(prop, "description") ?? strOf(resolved, "description"))?.trim();

// The fields of a map the schema describes with `properties`, in document order. A pair the view cannot show goes
// to `sink` as its raw lines, with its path; an object the schema describes nests its own pairs into the same sink.
// With no sink (a row of a list or map) the whole object cannot be shown, and the caller keeps it raw.
//
// A flow map (`{a: 1, b: 2}`) has its pairs on shared lines, so raw rows of single pairs would show their neighbours
// twice: one pair it cannot show makes the whole flow map one raw row of its parent, and what it had added to `sink` is taken back.
function fieldsOf(ctx: Ctx, s: Sch, node: Pairs & { readonly flow?: boolean }, path: string, sink: Other[] | null, depth: number): Field[] | null {
  const props = isObj(s["properties"]) ? s["properties"] : {};
  const fields: Field[] = [];
  const kept = sink?.length ?? 0;
  for (const pair of node.items) {
    const key = keyOf(pair.key);
    const prop = key !== undefined && Object.hasOwn(props, key) ? props[key] : undefined;
    const resolved = resolve(prop, ctx.defs);
    let got: Node | null = null;
    if (prop !== undefined) {
      const nests = sink !== null && depth < MAX_DEPTH && resolved?.["type"] === "object" && hasProperties(resolved) && isMap(pair.value) && !marked(pair.value);
      if (nests) {
        const inner = fieldsOf(ctx, resolved, pair.value, `${path}${key}/`, sink, depth + 1);
        got = inner === null ? null : { kind: "object", fields: inner };
      } else {
        got = walk(ctx, prop, pair.value, depth + 1);
      }
    }
    if (key !== undefined && got !== null) {
      fields.push({ name: key, help: helpOf(prop, resolved), node: got });
      continue;
    }
    if (sink === null) return null;
    if (node.flow === true) {
      sink.length = kept;
      return null;
    }
    const from = spanOf(pair.key);
    const to = spanOf(pair.value) ?? from;
    if (from !== undefined && to !== undefined) sink.push({ path: `${path}${key ?? "?"}`, lines: linesOf(ctx.text, from[0], to[1], isBlockScalar(pair.value)) });
  }
  return fields;
}

// A document's apiVersion and kind as placing reads them: a key that is present is a key, with its scalar value or none.
function head(node: Pairs): { apiVersion?: unknown; kind?: unknown } {
  const h: { apiVersion?: unknown; kind?: unknown } = {};
  for (const pair of node.items) {
    const k = keyOf(pair.key);
    if (k === "apiVersion" || k === "kind") h[k] = isScalar(pair.value) ? pair.value.value : undefined;
  }
  return h;
}

function docView(ctx: Ctx, s: FormSchema, d: Document.Parsed, n: number): DocView | undefined {
  const c = d.contents;
  if (c === null) return undefined;
  // The document as raw lines. They start at the first line before the content that is not blank, a comment or the bare `---`
  // marker, because an anchor or a tag of the document's own map sits before the range of the map itself.
  const whole = (): DocView => {
    const r = spanOf(c);
    const doc = spanOf(d);
    if (r === undefined) return { heading: `Document ${n}`, fields: [], other: [] };
    let start = r[0];
    if (doc !== undefined && doc[0] < r[0]) {
      let at = doc[0];
      for (const line of ctx.text.slice(doc[0], r[0]).split("\n")) {
        const t = line.trim();
        if (t !== "" && t !== "---" && !t.startsWith("#")) {
          start = at;
          break;
        }
        at += line.length + 1;
      }
    }
    return { heading: `Document ${n}`, fields: [], other: [{ path: "document", lines: linesOf(ctx.text, start, r[1]) }] };
  };
  if (!isMap(c) || marked(c)) return whole();
  const h = head(c);
  const placed = placeDocument(s, h);
  const def = placed.form && Object.hasOwn(ctx.defs, placed.def) ? ctx.defs[placed.def] : undefined;
  if (!placed.form || !isObj(def)) return whole();
  const other: Other[] = [];
  const fields = fieldsOf(ctx, def, c, "", other, 1);
  if (fields === null) return whole();
  const legacy = placed.def === legacyDef && !Object.hasOwn(h, "apiVersion") && !Object.hasOwn(h, "kind");
  return { heading: legacy ? `Document ${n}: v1alpha1 configuration` : `Document ${n}: ${String(h.kind)}`, fields, other };
}

/**
 * The text as fields, or its first parse error. `schema` is the schema of the cluster's contract (`schemaFor`). A
 * stream is a list of documents, each placed by its own `apiVersion` and `kind`.
 */
export function buildConfigView(text: string, schema: FormSchema): ConfigView {
  const parsed = parseAllDocuments(text);
  for (const d of parsed) {
    const e = d.errors[0];
    if (e !== undefined) return { ok: false, code: e.code, line: e.linePos?.[0].line };
  }
  const ctx: Ctx = { text, defs: schema.schema.$defs as unknown as Sch };
  const docs: DocView[] = [];
  parsed.forEach((d, i) => {
    const v = docView(ctx, schema, d, i + 1);
    if (v !== undefined) docs.push(v);
  });
  return { ok: true, docs };
}

/** Why the view shows no fields: a reason of the schema reader, or that the cluster's contract could not be read. */
export const noFieldsReasons = [...noFormReasons, "contract-unread"] as const;
export type NoFieldsReason = NoFormReason | "contract-unread";

const words: Readonly<Record<NoFieldsReason, string>> = {
  "no-contract": "This cluster has no recorded contract, so there are no fields. The text is shown.",
  "unsupported-contract": "There are no fields for this cluster's contract: the console holds the Talos schema of other versions only. The text is shown.",
  "asset-missing": "The Talos schema of this cluster's contract could not be loaded, so there are no fields. The text is shown.",
  unplaceable: "The Talos schema of this contract does not describe this document, so there are no fields. The text is shown.",
  "contract-unread": "The cluster's contract could not be read, so there are no fields. The text is shown.",
};

/** The one sentence of the raw view when there are no fields; unlike the editor's, it asks for nothing to be edited. */
export const noFieldsWords = (reason: NoFieldsReason): string => words[reason];

/** The one sentence when the text does not parse: the code and the line, never the parser's own message, which quotes the text. */
export const noParseWords = (v: { readonly code: string; readonly line: number | undefined }): string =>
  `The text did not parse as YAML (${v.code}${v.line === undefined ? "" : `, line ${v.line}`}), so there are no fields. The text is shown.`;
