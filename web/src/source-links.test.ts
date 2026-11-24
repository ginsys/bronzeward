// @vitest-environment happy-dom
// ginsys/bronzeward#1121: a release's source and revision links are one element before and after the read of what they name
// answers. A link that React replaces loses the keyboard focus a person had on it.
import { act, createElement as h } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, test } from "vitest";
import { type Reads, ReadsProvider } from "./list.tsx";
import type { ReadResult } from "./request.ts";
import { ReleaseItem } from "./resources.tsx";
import { when } from "./states.tsx";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const ok = (data: unknown): ReadResult<unknown> => ({ kind: "ok", data });
const missing: ReadResult<unknown> = { kind: "refused", status: 404, problem: null };
const REL = "rel_k2pq3rstuvwxyz234567abcdef";
const PRF = "prf_k2pq3rstuvwxyz234567abcdef";
const PRV = "prv_k2pq3rstuvwxyz234567abcdef";
const at = "2026-11-19T10:00:00Z";
const release = { id: REL, cluster: "cl_a", draft: "drf_k2pq3rstuvwxyz234567abcdef", draftRevision: 3, operation: "op_k2pq3rstuvwxyz234567abcdefg", publishedBy: null, publishedAt: at, renderer: {}, sources: [{ kind: "profile", head: PRF, revision: PRV, headRevision: 1 }], machines: [] };

let root: Root;
let box: HTMLElement;
let answerProfile: (r: ReadResult<unknown>) => void;
let answerRevision: (r: ReadResult<unknown>) => void;

beforeEach(() => {
  box = document.createElement("div");
  document.body.append(box);
  root = createRoot(box);
});
afterEach(async () => {
  await act(async () => root.unmount());
  box.remove();
});

// Every read answers at once but the profile's and the profile revision's, which wait until the test answers them.
const mount = async () => {
  const profile = new Promise<ReadResult<unknown>>((r) => { answerProfile = r; });
  const revision = new Promise<ReadResult<unknown>>((r) => { answerRevision = r; });
  const answers: Record<string, ReadResult<unknown> | Promise<ReadResult<unknown>>> = { [`/releases/${REL}`]: ok(release), [`/profiles/${PRF}`]: profile, [`/profile-revisions/${PRV}`]: revision };
  const reads: Reads = {
    client: { read: async <T,>(path: string) => (await (answers[path] ?? missing)) as ReadResult<T> },
    settle: (res, set, live, held) => {
      if (res.kind !== "ok" && held()) return true;
      if (live()) set(res);
      return false;
    },
    undropped: () => () => true, view: "v", again: 0, regained: 0, go: () => {}, failure: () => null,
  };
  await act(async () => root.render(h(ReadsProvider, { value: reads }, h(ReleaseItem, { id: REL }))));
  await act(async () => void (await new Promise((r) => setTimeout(r, 60))));
};

test("a release's source link keeps its element, and the focus on it, when the source's read answers", async () => {
  await mount();
  const before = box.querySelector(`a[href="/profiles/${PRF}"]`) as HTMLAnchorElement | null;
  expect(before?.getAttribute("title")).toBe(PRF);
  before?.focus();
  expect(document.activeElement).toBe(before);
  await act(async () => answerProfile(ok({ id: PRF, name: "edge-profile" })));
  await act(async () => void (await new Promise((r) => setTimeout(r, 60))));
  const after = box.querySelector(`a[href="/profiles/${PRF}"]`);
  expect(after?.textContent).toBe("edge-profile");
  expect(after).toBe(before);
  expect(document.activeElement).toBe(before);
});

test("a release's revision link keeps its element, and the focus on it, when the revision's read answers", async () => {
  await mount();
  const before = box.querySelector(`a[href="/profile-revisions/${PRV}"]`) as HTMLAnchorElement | null;
  expect(before?.getAttribute("title")).toBe(PRV);
  before?.focus();
  expect(document.activeElement).toBe(before);
  await act(async () => answerRevision(ok({ createdAt: at })));
  await act(async () => void (await new Promise((r) => setTimeout(r, 60))));
  const after = box.querySelector(`a[href="/profile-revisions/${PRV}"]`);
  expect(after?.textContent).toBe(`Saved ${when(at)}`);
  expect(after).toBe(before);
  expect(document.activeElement).toBe(before);
});
