// Client contracts §11.5 item 2: a browser test never retains a trace, HAR, video or screenshot of a
// protected review. The protected import spec must not call any API that makes one, and neither may a
// file it imports or the WebdriverIO configuration the run loads. A test cannot prove that no artifact
// exists, so this reads the sources for the calls and has a positive control for every read: the same
// read must flag a sample source that makes each kind of artifact, the walk must reach a file that only
// a transitive import names and read it to its last line, and each real file must be read byte for byte.
import { readFileSync, statSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import ts from "typescript";
import { expect, test } from "vitest";

// WebdriverIO commands, the WebDriver and BiDi print and capture commands, and the Puppeteer calls that
// getPuppeteer reaches (screenshot, pdf, createPDFStream, screencast, tracing.start).
const ARTIFACT_CALL = /\b(saveScreenshot|saveElementScreenshot|takeScreenshot|takeElementScreenshot|saveRecordingScreen|startRecordingScreen|stopRecordingScreen|startTracing|stopTracing|startHar|saveHar|savePDF|printPage|browsingContextCaptureScreenshot|browsingContextPrint|screenshot|screencast|pdf|createPDFStream|tracing\s*\.\s*start)\s*\(/g;

export const artifactCalls = (source: string): string[] => [...source.matchAll(ARTIFACT_CALL)].map((m) => m[1]!.replace(/\s+/g, ""));

// A relative specifier of a static import, an import equals, a re-export (named or namespace), a side-effect
// import, a dynamic import, a literal require or an import type, read from the TypeScript parse tree in the
// language mode of the file name (so .tsx parses as TSX). A comment, a string, a regular expression or a
// template cannot hide one or make a false one. A template specifier with an interpolation names no file and
// is not returned. A literal inside parentheses or a type assertion counts as that literal.
export const localImports = (source: string, fileName = "source.ts"): string[] => {
  const found: string[] = [];
  const add = (node: ts.Node | undefined) => {
    while (
      node &&
      (ts.isParenthesizedExpression(node) || ts.isAsExpression(node) || ts.isTypeAssertionExpression(node) || ts.isSatisfiesExpression(node) || ts.isNonNullExpression(node))
    ) {
      node = node.expression;
    }
    if (node && (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node))) found.push(node.text);
  };
  const visit = (node: ts.Node): void => {
    if (ts.isImportDeclaration(node) || ts.isExportDeclaration(node)) add(node.moduleSpecifier);
    else if (ts.isImportEqualsDeclaration(node) && ts.isExternalModuleReference(node.moduleReference)) add(node.moduleReference.expression);
    else if (ts.isCallExpression(node) && (node.expression.kind === ts.SyntaxKind.ImportKeyword || (ts.isIdentifier(node.expression) && node.expression.text === "require"))) add(node.arguments[0]);
    else if (ts.isImportTypeNode(node) && ts.isLiteralTypeNode(node.argument)) add(node.argument.literal);
    ts.forEachChild(node, visit);
  };
  visit(ts.createSourceFile(fileName, source, ts.ScriptTarget.Latest, true));
  return found.filter((name) => name.startsWith("./") || name.startsWith("../"));
};

const here = (name: string) => fileURLToPath(new URL(`./${name}`, import.meta.url));

type Reader = (file: string) => string;

// The sources of the roots and of every local file they import, read once each, by absolute path.
const sources = (roots: string[], read: Reader = (file) => readFileSync(file, "utf8")): Map<string, string> => {
  const seen = new Map<string, string>();
  const todo = [...roots];
  for (let file = todo.pop(); file !== undefined; file = todo.pop()) {
    if (seen.has(file)) continue;
    const source = read(file);
    seen.set(file, source);
    for (const spec of localImports(source, file)) todo.push(resolve(dirname(file), spec));
  }
  return seen;
};

test("the positive control: the read flags a source that makes a screenshot, a recording, a trace, a HAR or a PDF", () => {
  const sample = `await browser.saveScreenshot("a.png"); await $("x").saveScreenshot ("b.png"); await browser.startRecordingScreen();
    await browser.startTracing(); await browser.saveHar("c.har"); await browser.savePDF("d.pdf");`;
  expect(artifactCalls(sample)).toEqual(["saveScreenshot", "saveScreenshot", "startRecordingScreen", "startTracing", "saveHar", "savePDF"]);
});

test("the positive control: the read flags the WebDriver print, the BiDi capture and the Puppeteer calls", () => {
  const sample = `await browser.printPage(); await browser.browsingContextCaptureScreenshot({ context });
    await browser.browsingContextPrint({ context }); const p = await browser.getPuppeteer(); await page.screenshot();
    await page.pdf (); await page.createPDFStream(); await page.screencast(); await p.tracing .start();`;
  expect(artifactCalls(sample)).toEqual([
    "printPage",
    "browsingContextCaptureScreenshot",
    "browsingContextPrint",
    "screenshot",
    "pdf",
    "createPDFStream",
    "screencast",
    "tracing.start",
  ]);
});

test("the positive control: the read finds the local imports of a source and skips a package import", () => {
  const sample = `import { a } from "./one.ts";
    import {
      b,
    } from "../two.ts";
    import "./three.ts";
    export { c } from './four.ts';
    const d = await import("./five.ts");
    const g = await import(\`./six.ts\`);
    import /* a helper */ "./seven.ts";
    const h = "./*"; import "./eight.ts"; const i = "*/";
    import "./$nine.ts";
    const j = await import(\`./ten\${name}.ts\`);
    // import "./commented.ts";
    export * as helper from "./eleven.ts";
    const k = /"/; import "./twelve.ts";
    import thirteen = require("./thirteen.ts");
    type T = import("./fourteen.ts").Fourteen;
    const l = require("./fifteen.cjs");
    const m = await import("./sixteen.ts" as const);
    const n = await import(("./seventeen.ts"));
    import { e } from "node:fs";
    import { f } from "@wdio/globals";`;
  expect(localImports(sample)).toEqual([
    "./one.ts",
    "../two.ts",
    "./three.ts",
    "./four.ts",
    "./five.ts",
    "./six.ts",
    "./seven.ts",
    "./eight.ts",
    "./$nine.ts",
    "./eleven.ts",
    "./twelve.ts",
    "./thirteen.ts",
    "./fourteen.ts",
    "./fifteen.cjs",
    "./sixteen.ts",
    "./seventeen.ts",
  ]);
});

test("the positive control: the read parses a .tsx file as TSX, so an import after a JSX element is found", () => {
  const sample = `const view = <div>{import("./inside.ts")}</div>; import "./after.ts";`;
  expect(localImports(sample, "view.tsx")).toEqual(["./inside.ts", "./after.ts"]);
});

test("the positive control: the walk reaches a file only a transitive import names and reads it to the end", () => {
  const dir = "/virtual";
  const padding = "// padding line\n".repeat(300);
  const files: Record<string, string> = {
    [`${dir}/root.ts`]: `import { a } from "./middle.ts";\n`,
    [`${dir}/middle.ts`]: `await import(\`./leaf.ts\`);\n`,
    [`${dir}/leaf.ts`]: `${padding}await browser.saveScreenshot("late.png");`,
  };
  const read: Reader = (file) => files[file] ?? expect.unreachable(`unexpected read of ${file}`);
  const walked = sources([`${dir}/root.ts`], read);
  expect([...walked.keys()].sort()).toEqual(Object.keys(files).sort());
  expect(artifactCalls(walked.get(`${dir}/leaf.ts`)!)).toEqual(["saveScreenshot"]);
});

test("the protected import spec, the files it imports and the auth configuration make no screenshot, recording, trace, HAR or PDF", () => {
  const spec = here("auth/protected.e2e.ts");
  const files = sources([spec, here("auth.conf.ts")]);
  // The control for the spec: it is a real, long file, so an empty read would pass here.
  expect(files.get(spec)!.split("\n").length).toBeGreaterThan(100);
  // The control for the walk: it reached the helpers and the configuration, which the spec imports.
  expect([...files.keys()]).toEqual(expect.arrayContaining([here("auth/helpers.ts"), here("auth.conf.ts")]));
  for (const [file, source] of files) {
    // The control for this file: the read returned every byte of it, so a call near the end cannot hide.
    expect(Buffer.byteLength(source), file).toBe(statSync(file).size);
    expect(source.trim().length, file).toBeGreaterThan(0);
    expect(artifactCalls(source), file).toEqual([]);
  }
});
