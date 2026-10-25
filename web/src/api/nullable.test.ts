// B5's generator proof: every nullable `type` array in api/openapi.yaml generates `| null`, and
// ReferenceInput generates a union of its two shapes, discriminated by `kind`. The fields come
// from openapi-typescript's own parse of the description, counted against the description's text;
// each becomes a type assertion that the TypeScript compiler checks against src/api/v1.d.ts.
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import openapiTS from "openapi-typescript";
import ts from "typescript";
import { beforeAll, expect, test } from "vitest";

const spec = fileURLToPath(new URL("../../../api/openapi.yaml", import.meta.url));
const generated = fileURLToPath(new URL("./v1.d.ts", import.meta.url));
const checkFile = generated.replace(/v1\.d\.ts$/, "nullable-check.ts");

interface Field {
  schema: string; // "#/components/schemas/AlertBody", or a nested or oneOf-branch path under it
  name: string;
}

// Every property whose `type` array holds "null", found through its parent object, so each is
// named even inside a oneOf branch. Any nullable schema not reached that way fails the test.
async function nullableFields(): Promise<{ fields: Field[]; unnamed: number }> {
  const fields: Field[] = [];
  const named = new WeakSet<object>();
  const nullable = new Set<object>();
  await openapiTS(new URL(`file://${spec}`), {
    transform(schema, meta) {
      if (Array.isArray(schema.type) && schema.type.includes("null")) nullable.add(schema);
      const properties = "properties" in schema ? (schema.properties as Record<string, { type?: unknown }>) : {};
      for (const [name, prop] of Object.entries(properties ?? {})) {
        if (Array.isArray(prop.type) && prop.type.includes("null") && !named.has(prop)) {
          named.add(prop);
          fields.push({ schema: meta.path ?? "", name });
        }
      }
      return undefined;
    },
  });
  return { fields, unnamed: [...nullable].filter((s) => !named.has(s)).length };
}

// components["schemas"]["A"], then one indexed access per nested property; a oneOf branch keeps
// the union, and Every requires the field nullable in each member.
function owner(path: string): string {
  const parts = path.replace(/^#\/components\/schemas\//, "").split("/");
  let expr = `components["schemas"][${JSON.stringify(parts.shift())}]`;
  while (parts.length > 0) {
    const p = parts.shift()!;
    if (p === "oneOf") parts.shift();
    else expr = `NonNullable<${expr}>[${JSON.stringify(p)}]`;
  }
  return expr;
}

const prelude = `import type { components } from "./v1";
type Nullable<T> = unknown extends T ? false : null extends T ? true : false;
type Every<U, K extends string> = U extends unknown ? (K extends keyof U ? Nullable<U[K]> : false) : never;
type IsUnion<T, U = T> = T extends unknown ? ([U] extends [T] ? false : true) : never;
declare function ok<T extends true>(): void;
type RI = components["schemas"]["ReferenceInput"];
`;

function diagnostics(lines: string[]): string[] {
  const options: ts.CompilerOptions = {
    strict: true,
    noEmit: true,
    skipLibCheck: true,
    module: ts.ModuleKind.ESNext,
    moduleResolution: ts.ModuleResolutionKind.Bundler,
  };
  const host = ts.createCompilerHost(options);
  const source = prelude + lines.join("\n") + "\n";
  const { readFile, fileExists } = host;
  host.readFile = (name) => (name === checkFile ? source : readFile(name));
  host.fileExists = (name) => name === checkFile || fileExists(name);
  const program = ts.createProgram([checkFile], options, host);
  return ts.getPreEmitDiagnostics(program).map((d) => ts.flattenDiagnosticMessageText(d.messageText, "\n"));
}

let fields: Field[] = [];
let unnamed = -1;
beforeAll(async () => {
  ({ fields, unnamed } = await nullableFields());
});

test("all 88 nullable fields are found, each named", () => {
  const textCount = readFileSync(spec, "utf8").match(/type: \[[a-z]+, 'null'\]/g)?.length;
  expect(unnamed).toBe(0);
  expect(fields.length).toBe(textCount);
  expect(fields.length).toBe(88);
  expect(fields.every((f) => f.schema.startsWith("#/components/schemas/"))).toBe(true);
});

test("every nullable field generates | null", () => {
  const lines = fields.map((f) => `ok<Every<${owner(f.schema)}, ${JSON.stringify(f.name)}>>(); // ${f.schema} ${f.name}`);
  expect(diagnostics(lines)).toEqual([]);
});

test("the check refuses a field that is not nullable (negative control)", () => {
  expect(diagnostics([`ok<Every<components["schemas"]["ServerBody"], "version">>();`])).toHaveLength(1);
});

test("ReferenceInput generates a union of two shapes discriminated by kind", () => {
  const lines = [
    "ok<IsUnion<RI>>();",
    'const s: RI = { kind: "string", version: 1, encoding: "base64" };',
    'const n: RI = { kind: "mapping", version: 1, encoding: null };',
    "// @ts-expect-error only a string may be base64-encoded",
    'const bad: RI = { kind: "integer", version: 1, encoding: "base64" };',
    "void [s, n, bad];",
  ];
  expect(diagnostics(lines)).toEqual([]);
});
