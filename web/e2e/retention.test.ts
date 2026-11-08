// Client contracts §11.5 item 2: a browser test never retains a trace, HAR, video or screenshot of a
// protected review. The protected import spec must not call any API that makes one. A test cannot
// prove that no artifact exists, so this reads the spec's source for the calls and has a positive
// control: the same read must flag a sample source that makes each kind of artifact.
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";

const ARTIFACT_CALL = /\b(saveScreenshot|saveElementScreenshot|takeScreenshot|takeElementScreenshot|saveRecordingScreen|startRecordingScreen|stopRecordingScreen|startTracing|stopTracing|startHar|saveHar|savePDF)\s*\(/g;

export const artifactCalls = (source: string): string[] => [...source.matchAll(ARTIFACT_CALL)].map((m) => m[1]!);

const spec = (name: string) => readFileSync(fileURLToPath(new URL(`./auth/${name}`, import.meta.url)), "utf8");

test("the positive control: the read flags a source that makes a screenshot, a recording, a trace, a HAR or a PDF", () => {
  const sample = `await browser.saveScreenshot("a.png"); await $("x").saveScreenshot ("b.png"); await browser.startRecordingScreen();
    await browser.startTracing(); await browser.saveHar("c.har"); await browser.savePDF("d.pdf");`;
  expect(artifactCalls(sample)).toEqual(["saveScreenshot", "saveScreenshot", "startRecordingScreen", "startTracing", "saveHar", "savePDF"]);
});

test("the protected import spec makes no screenshot, recording, trace, HAR or PDF, and the read saw a source", () => {
  const source = spec("protected.e2e.ts");
  // The control for this source: the spec is a real, long file, so an empty read would pass here.
  expect(source.split("\n").length).toBeGreaterThan(100);
  expect(artifactCalls(source)).toEqual([]);
});
