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

// A relative specifier of a static import, a re-export, a side-effect import or a dynamic import, read by
// the TypeScript scanner, so a comment, a string or a template cannot hide one or make a false one. A
// template specifier with an interpolation names no file and is not returned.
export const localImports = (source: string): string[] =>
  ts
    .preProcessFile(source, true, true)
    .importedFiles.map((f) => f.fileName)
    .filter((name) => name.startsWith("./") || name.startsWith("../"));

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
    for (const spec of localImports(source)) todo.push(resolve(dirname(file), spec));
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
  ]);
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
