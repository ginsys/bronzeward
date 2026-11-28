import { readdirSync, readFileSync } from "node:fs";
import { describe, expect, test } from "vitest";
import {
  contractsWithForm,
  legacyDef,
  noFormReasons,
  noFormWords,
  placeDocument,
  schemaFor,
  type FormSchema,
  type JsonSchema,
} from "./talos-schema.ts";

const dir = new URL("./talos-schema/", import.meta.url);
const file = (contract: string) => readFileSync(new URL(`${contract}.json`, dir), "utf8");

const form = async (contract: string): Promise<FormSchema> => {
  const r = await schemaFor(contract);
  if (!r.form) throw new Error(`no schema for ${contract}: ${r.reason}`);
  return r;
};

describe("the schema of a contract", () => {
  test("v1.14 is the module's file: its text, its 89 top-level choices, its own $id", async () => {
    const r = await form("v1.14");
    expect(r.contract).toBe("v1.14");
    expect(r.schema.oneOf).toHaveLength(89);
    expect(r.schema.$id).toBe("https://talos.dev/v1.14/schemas/config.schema.json");
    // The build's `?raw` import is the file's own text, so what the page parses is what the Go test
    // proves equal to the module's file.
    const raw = await import("./talos-schema/v1.14.json?raw");
    expect(raw.default).toBe(file("v1.14"));
    expect(r.schema).toEqual(JSON.parse(file("v1.14")));
  });

  test("the contracts that have a schema are the files of talos-schema/, no more and no fewer", () => {
    const files = readdirSync(dir).map((f) => {
      expect(f.endsWith(".json")).toBe(true);
      return f.slice(0, -".json".length);
    });
    expect(contractsWithForm().sort()).toEqual(files.sort());
    expect(files.length).toBeGreaterThan(0);
  });

  test.each([
    [undefined, "no-contract"],
    [null, "no-contract"],
    ["", "no-contract"],
    ["v1.15", "unsupported-contract"],
    ["v1.13", "unsupported-contract"],
    ["v1.14.5", "unsupported-contract"],
    ["1.14", "unsupported-contract"],
    ["V1.14", "unsupported-contract"],
    [" v1.14", "unsupported-contract"],
    ["v1.14 ", "unsupported-contract"],
    ["v1.1", "unsupported-contract"],
    // names an object inherits are no contract
    ["constructor", "unsupported-contract"],
    ["__proto__", "unsupported-contract"],
    ["toString", "unsupported-contract"],
  ] as const)("%j has no form (%s), never a nearby version", async (contract, reason) => {
    const r = await schemaFor(contract);
    expect(r).toMatchObject({ form: false, reason });
  });

  test("an unsupported contract is named in the result, a missing one is not", async () => {
    expect(await schemaFor("v1.15")).toEqual({ form: false, reason: "unsupported-contract", contract: "v1.15" });
    expect(await schemaFor(null)).toEqual({ form: false, reason: "no-contract" });
  });

  test("a schema that cannot be loaded or read is a missing asset, not a throw", async () => {
    const asset = (load: () => Promise<{ default: string }>) => ({ "v9.9": load });
    const missing = { form: false, reason: "asset-missing", contract: "v9.9" };
    expect(await schemaFor("v9.9", asset(() => Promise.reject(new Error("chunk failed"))))).toEqual(missing);
    expect(await schemaFor("v9.9", asset(() => Promise.resolve({ default: "{" })))).toEqual(missing);
    expect(await schemaFor("v9.9", asset(() => Promise.resolve({ default: "{}" })))).toEqual(missing);
    expect(await schemaFor("v9.9", asset(() => Promise.resolve({ default: "null" })))).toEqual(missing);
    expect(await schemaFor("v9.9", asset(() => Promise.resolve({ default: '{"oneOf":[],"$defs":null}' })))).toEqual(missing);
  });

  test("each reason has one sentence for the page", () => {
    const seen = new Set<string>();
    for (const r of noFormReasons) {
      const w = noFormWords(r);
      expect(w).toMatch(/^[A-Z][^.]*\.( [A-Z][^.]*\.)?$/);
      seen.add(w);
    }
    expect(seen.size).toBe(noFormReasons.length);
  });
});

describe("placing a document", () => {
  test("a document with neither apiVersion nor kind is the legacy document, with or without a version key", async () => {
    const s = await form("v1.14");
    expect(placeDocument(s, {})).toEqual({ form: true, def: legacyDef });
    expect(placeDocument(s, { version: "v1alpha1", machine: {} } as object)).toEqual({ form: true, def: legacyDef });
    expect(placeDocument(s, { machine: { token: "x" } } as object)).toEqual({ form: true, def: legacyDef });
  });

  test("the legacy document is the one top-level choice that lists no apiVersion or kind", async () => {
    const { schema } = await form("v1.14");
    const bare = schema.oneOf.map((c) => c.$ref.replace("#/$defs/", "")).filter((n) => !schema.$defs[n]?.properties?.kind);
    expect(bare).toEqual([legacyDef]);
  });

  test("a pair the schema lists is placed on its definition", async () => {
    const s = await form("v1.14");
    expect(placeDocument(s, { apiVersion: "v1alpha1", kind: "ExternalVolumeConfig" })).toEqual({
      form: true,
      def: "block.ExternalVolumeConfigV1Alpha1",
    });
  });

  test("every other top-level choice is placed on itself by the pair it lists, 88 of 88", async () => {
    const s = await form("v1.14");
    let placed = 0;
    for (const c of s.schema.oneOf) {
      const name = c.$ref.replace("#/$defs/", "");
      const props = s.schema.$defs[name]?.properties;
      if (name === legacyDef) continue;
      for (const apiVersion of props?.apiVersion?.enum ?? []) {
        for (const kind of props?.kind?.enum ?? []) {
          expect(placeDocument(s, { apiVersion, kind }), `${apiVersion} ${kind}`).toEqual({ form: true, def: name });
        }
      }
      placed++;
    }
    expect(placed).toBe(88);
  });

  test.each([
    ["an unknown kind", { apiVersion: "v1alpha1", kind: "NoSuchConfig" }],
    ["a kind of another case", { apiVersion: "v1alpha1", kind: "externalvolumeconfig" }],
    ["a kind with an apiVersion the schema does not list for it", { apiVersion: "v1beta1", kind: "ExternalVolumeConfig" }],
    ["a kind only", { kind: "ExternalVolumeConfig" }],
    ["an apiVersion only", { apiVersion: "v1alpha1" }],
    ["a kind that is no string", { apiVersion: "v1alpha1", kind: 5 }],
    ["an apiVersion that is null", { apiVersion: null, kind: "ExternalVolumeConfig" }],
    ["both keys empty", { apiVersion: "", kind: "" }],
    ["the name of an inherited property", { apiVersion: "v1alpha1", kind: "constructor" }],
  ])("%s is unplaceable", async (_name, doc) => {
    const s = await form("v1.14");
    expect(placeDocument(s, doc)).toEqual({ form: false, reason: "unplaceable", contract: "v1.14" });
  });

  test("a pair that two definitions list is unplaceable, never a guess", () => {
    const def = (kind: string) => ({ properties: { apiVersion: { enum: ["v1"] }, kind: { enum: [kind] } } });
    const schema: JsonSchema = {
      $id: "https://talos.dev/v0.0/schemas/config.schema.json",
      oneOf: [{ $ref: "#/$defs/a" }, { $ref: "#/$defs/b" }, { $ref: "#/$defs/c" }, { $ref: "#/$defs/d" }],
      $defs: { a: def("Twice"), b: def("Twice"), c: def("Once"), d: def("Twice") },
    };
    const s: FormSchema = { form: true, contract: "v0.0", schema };
    expect(placeDocument(s, { apiVersion: "v1", kind: "Twice" })).toMatchObject({ form: false, reason: "unplaceable" });
    expect(placeDocument(s, { apiVersion: "v1", kind: "Once" })).toEqual({ form: true, def: "c" });
    // a schema with no legacy definition places the empty document nowhere
    expect(placeDocument(s, {})).toMatchObject({ form: false, reason: "unplaceable" });
  });
});
