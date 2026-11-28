// The Talos configuration schema of a cluster's contract, from the file the web build carries
// (docs/spec/configuration-editing.md §3). It is the pinned machinery module's own
// config/schemas/config.schema.json, copied byte for byte into talos-schema/<contract>.json by
// `mise run web-schema`; TestWebSchemaAssets (internal/compile) fails when a file differs from the
// module's or when the contracts that have a file differ from the contracts the compiler renders.
// Nothing is fetched at run time, and no contract is ever answered with a nearby version's schema:
// a contract that has no file, or a document the schema cannot place, is "no form".
//
// This module reads and places; it builds no field tree and sends no request.

/** A top-level choice and a definition of the schema, as far as placing a document reads them. */
export type SchemaDef = {
  readonly properties?: {
    readonly apiVersion?: { readonly enum?: readonly string[] } | undefined;
    readonly kind?: { readonly enum?: readonly string[] } | undefined;
  };
};

export type JsonSchema = {
  readonly $id: string;
  readonly oneOf: readonly { readonly $ref: string }[];
  readonly $defs: Readonly<Record<string, SchemaDef>>;
};

export type FormSchema = { readonly form: true; readonly contract: string; readonly schema: JsonSchema };

export const noFormReasons = ["no-contract", "unsupported-contract", "asset-missing", "unplaceable"] as const;
export type NoFormReason = (typeof noFormReasons)[number];

/** There is no form: the page shows the text editor and says why in one sentence. */
export type NoForm = { readonly form: false; readonly reason: NoFormReason; readonly contract?: string };

const words: Readonly<Record<NoFormReason, string>> = {
  "no-contract": "This cluster has no recorded contract, so there is no form. Edit the text.",
  "unsupported-contract":
    "There is no form for this cluster's contract: the console holds the Talos schema of other versions only. Edit the text.",
  "asset-missing": "The Talos schema of this cluster's contract could not be loaded, so there is no form. Edit the text.",
  unplaceable: "The Talos schema of this contract does not describe this document, so there is no form for it. Edit the text.",
};

export const noFormWords = (reason: NoFormReason): string => words[reason];

const isObject = (v: unknown): v is Record<string, unknown> => typeof v === "object" && v !== null && !Array.isArray(v);
const isStrings = (v: unknown): boolean => v === undefined || (Array.isArray(v) && v.every((s) => typeof s === "string"));

/**
 * Whether a parsed file has the shape placing reads: every top-level choice is a `$ref` into a
 * definition that exists, and each definition's apiVersion and kind lists, where it has them, are
 * lists of strings. Anything else is no schema, so placing never meets a shape it cannot read.
 */
function wellFormed(s: unknown): s is JsonSchema {
  if (!isObject(s) || !Array.isArray(s["oneOf"]) || !isObject(s["$defs"])) return false;
  const defs = s["$defs"];
  return s["oneOf"].every((c) => {
    if (!isObject(c) || typeof c["$ref"] !== "string" || !c["$ref"].startsWith("#/$defs/")) return false;
    const def = Object.hasOwn(defs, c["$ref"].slice("#/$defs/".length)) ? defs[c["$ref"].slice("#/$defs/".length)] : undefined;
    if (!isObject(def)) return false;
    const props = def["properties"];
    if (props === undefined) return true;
    if (!isObject(props)) return false;
    return ["apiVersion", "kind"].every((k) => {
      const p = props[k];
      return p === undefined || (isObject(p) && isStrings(p["enum"]));
    });
  });
}

type Loader = () => Promise<{ readonly default: string }>;

// One loader per contract that has a file, each its own chunk of the build. The keys are the file
// names of talos-schema/ (checked by talos-schema.test.ts), and so the contracts of the supported
// list (checked by TestWebSchemaAssets).
const assets: Readonly<Record<string, Loader>> = {
  "v1.14": () => import("./talos-schema/v1.14.json?raw"),
};

/** The contracts that have a schema in this build, in no promised order. */
export const contractsWithForm = (): string[] => Object.keys(assets);

/**
 * The schema for a contract exactly as the cluster records it (`v1.14`), or why there is none.
 * `v1.14.5`, `1.14` and a contract of another minor have no schema: the answer is never a nearby one.
 * `load` is a seam for the tests.
 */
export async function schemaFor(
  contract: string | null | undefined,
  load: Readonly<Record<string, Loader>> = assets,
): Promise<FormSchema | NoForm> {
  if (contract === null || contract === undefined || contract === "") return { form: false, reason: "no-contract" };
  if (!Object.hasOwn(load, contract)) return { form: false, reason: "unsupported-contract", contract };
  try {
    const loader = load[contract];
    const schema: unknown = JSON.parse((await loader!()).default);
    if (!wellFormed(schema)) return { form: false, reason: "asset-missing", contract };
    return { form: true, contract, schema };
  } catch {
    return { form: false, reason: "asset-missing", contract };
  }
}

/** The definition of the legacy v1alpha1 document, which carries neither apiVersion nor kind. */
export const legacyDef = "v1alpha1.Config";

/** The definition that describes a document: the name of an entry of the schema's `$defs`. */
export type Placed = { readonly form: true; readonly def: string };

// "apiVersion\0kind" to the one definition that lists both, per schema. A pair two definitions
// list stays out of the index: placing it would be a guess.
const indexes = new WeakMap<JsonSchema, ReadonlyMap<string, string>>();

function indexOf(schema: JsonSchema): ReadonlyMap<string, string> {
  const known = indexes.get(schema);
  if (known) return known;
  const index = new Map<string, string>();
  const clash = new Set<string>();
  for (const { $ref } of schema.oneOf) {
    const name = $ref.replace(/^#\/\$defs\//, "");
    const props = schema.$defs[name]?.properties;
    for (const v of props?.apiVersion?.enum ?? []) {
      for (const k of props?.kind?.enum ?? []) {
        const key = `${v}\0${k}`;
        if (index.has(key) || clash.has(key)) {
          index.delete(key);
          clash.add(key);
        } else {
          index.set(key, name);
        }
      }
    }
  }
  indexes.set(schema, index);
  return index;
}

/**
 * Places a document by its `apiVersion` and `kind`, which the schema lists as allowed values. A
 * document with neither key is the legacy v1alpha1 document, whether or not it has a `version` key
 * (a fragment is a partial patch, section 4 item 1). A document with one key only, a value that is
 * no string, or a pair the schema does not list is `unplaceable`.
 */
export function placeDocument(
  s: FormSchema,
  doc: { readonly apiVersion?: unknown; readonly kind?: unknown },
): Placed | NoForm {
  const no: NoForm = { form: false, reason: "unplaceable", contract: s.contract };
  const { apiVersion, kind } = doc;
  // Key presence, not value: a key that holds undefined is still a discriminator key, so the
  // document is a half pair and not the legacy one.
  if (!Object.hasOwn(doc, "apiVersion") && !Object.hasOwn(doc, "kind")) {
    return s.schema.oneOf.some((c) => c.$ref === `#/$defs/${legacyDef}`) && s.schema.$defs[legacyDef] !== undefined
      ? { form: true, def: legacyDef }
      : no;
  }
  if (typeof apiVersion !== "string" || typeof kind !== "string") return no;
  const def = indexOf(s.schema).get(`${apiVersion}\0${kind}`);
  return def === undefined ? no : { form: true, def };
}
