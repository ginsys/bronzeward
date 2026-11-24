// @vitest-environment happy-dom
// ginsys/bronzeward#1121: the review page's machine link is one element before and after the page's own read of the
// machine answers. A link that React replaces loses the keyboard focus a person had on it.
import { act, createElement as h } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, test } from "vitest";
import type { components } from "./api/v1";
import { type Reads, ReadsProvider } from "./list.tsx";
import { ReviewBody } from "./review.tsx";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

type Review = components["schemas"]["ReviewBody"];
const MCH = "mch_BWSYNTH-b3caa2q";
const review = { release: "rel_BWSYNTH-h7rt2bd", machine: MCH, mode: "metal", configuration: "machine: {}\n", provenance: [] } as unknown as Review;
const reads: Reads = { client: { read: () => new Promise(() => {}) }, settle: () => false, undropped: () => () => true, view: "", again: 0, regained: 0, go: () => {}, failure: () => null };

let root: Root;
let box: HTMLElement;
beforeEach(() => {
  box = document.createElement("div");
  document.body.append(box);
  root = createRoot(box);
});
afterEach(async () => {
  await act(async () => root.unmount());
  box.remove();
});

test("keeps the same machine link, and the focus on it, when the page's read of the machine answers", async () => {
  await act(async () => root.render(h(ReadsProvider, { value: reads }, h(ReviewBody, { r: review }))));
  const before = box.querySelector('a[href^="/machines/"]') as HTMLAnchorElement | null;
  expect(before?.getAttribute("title")).toBe(MCH);
  before?.focus();
  expect(document.activeElement).toBe(before);
  await act(async () => root.render(h(ReadsProvider, { value: reads }, h(ReviewBody, { r: review, machine: { id: MCH, name: "worker-1" } as never }))));
  const after = box.querySelector('a[href^="/machines/"]');
  expect(after?.textContent).toBe("worker-1");
  expect(after).toBe(before);
  expect(document.activeElement).toBe(before);
});
