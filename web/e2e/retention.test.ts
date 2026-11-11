// Client contracts §11.5 item 2: a browser test never retains a trace, HAR, video or screenshot of a
// protected review. The protected import spec must not call any API that makes one, and neither may a
// file it imports or the WebdriverIO configuration the run loads. A test cannot prove that no artifact
// exists, so this reads the sources for the calls and has a positive control for every read: the same
// read must flag a sample source that makes each kind of artifact, and a read of each real file must
// flag a capture call appended to that file.
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

// WebdriverIO commands, the WebDriver print command, the BiDi capture command and the Puppeteer calls
// that getPuppeteer reaches (screenshot, pdf, tracing.start).
const ARTIFACT_CALL = /\b(saveScreenshot|saveElementScreenshot|takeScreenshot|takeElementScreenshot|saveRecordingScreen|startRecordingScreen|stopRecordingScreen|startTracing|stopTracing|startHar|saveHar|savePDF|printPage|browsingContextCaptureScreenshot|screenshot|pdf|tracing\s*\.\s*start)\s*\(/g;

export const artifactCalls = (source: string): string[] => [...source.matchAll(ARTIFACT_CALL)].map((m) => m[1]!.replace(/\s+/g, ""));

// A relative specifier of a static import, a re-export, a side-effect import or a dynamic import.
const LOCAL_IMPORT = /\b(?:from|import)\s*\(?\s*["'](\.{1,2}\/[^"']+)["']/g;

export const localImports = (source: string): string[] => [...source.matchAll(LOCAL_IMPORT)].map((m) => m[1]!);

const here = (name: string) => fileURLToPath(new URL(`./${name}`, import.meta.url));

// The sources of the roots and of every local file they import, read once each, by absolute path.
const sources = (roots: string[]): Map<string, string> => {
  const seen = new Map<string, string>();
  const todo = [...roots];
  for (let file = todo.pop(); file !== undefined; file = todo.pop()) {
    if (seen.has(file)) continue;
    const source = readFileSync(file, "utf8");
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
    const p = await browser.getPuppeteer(); await page.screenshot(); await page.pdf (); await p.tracing .start();`;
  expect(artifactCalls(sample)).toEqual(["printPage", "browsingContextCaptureScreenshot", "screenshot", "pdf", "tracing.start"]);
});

test("the positive control: the read finds the local imports of a source and skips a package import", () => {
  const sample = `import { a } from "./one.ts";
    import {
      b,
    } from "../two.ts";
    import "./three.ts";
    export { c } from './four.ts';
    const d = await import("./five.ts");
    import { e } from "node:fs";
    import { f } from "@wdio/globals";`;
  expect(localImports(sample)).toEqual(["./one.ts", "../two.ts", "./three.ts", "./four.ts", "./five.ts"]);
});

test("the protected import spec, the files it imports and the auth configuration make no screenshot, recording, trace, HAR or PDF", () => {
  const spec = here("auth/protected.e2e.ts");
  const files = sources([spec, here("auth.conf.ts")]);
  // The control for the spec: it is a real, long file, so an empty read would pass here.
  expect(files.get(spec)!.split("\n").length).toBeGreaterThan(100);
  // The control for the walk: it reached the helpers and the configuration, which the spec imports.
  expect([...files.keys()]).toEqual(expect.arrayContaining([here("auth/helpers.ts"), here("auth.conf.ts")]));
  for (const [file, source] of files) {
    // The control for this file: a capture call appended to its own source is flagged, so the read saw the whole text.
    expect(source.trim().length, file).toBeGreaterThan(0);
    expect(artifactCalls(`${source}\nawait browser.saveScreenshot("x");`), file).toEqual([...artifactCalls(source), "saveScreenshot"]);
    expect(artifactCalls(source), file).toEqual([]);
  }
});
