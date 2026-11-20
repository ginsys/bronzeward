// @vitest-environment happy-dom
// The profile's pages show the configuration the profile pins (ginsys/bronzeward#997): each pinned
// fragment revision's name, layer and document, in the pinned order. The page composes two reads the
// API already answers: the profile revision's pinned fragment revision identifiers, then each
// fragment revision. This file mounts the two exported pages in a DOM with a client that answers by
// path and records every path it is asked.

import { act, createElement as h, type ReactElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { WritesProvider } from "./forms.tsx";
import { type Reads, ReadsProvider } from "./list.tsx";
import type { ReadResult } from "./request.ts";
import { RevisionItem, SourceItem, view } from "./resources.tsx";
import { ShellContext } from "./shell.tsx";
import { Writes } from "./write.ts";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const ok = (data: unknown): ReadResult<unknown> => ({ kind: "ok", data });
const at = "2026-11-20T17:00:00Z";
const profile = (revision: string | null) => ({ id: "prf_a", cluster: "cl_a", scope: "workers", name: "workers", revision, headRevision: 1, createdAt: at });
const profileRevision = (fragments: string[]) => ({ id: "prv_a", cluster: "cl_a", name: "workers", fragments, author: "usr_a", createdAt: at });
const fragment = (id: string, name: string, layer: string, document: string) => ({
  id, cluster: "cl_a", name, layer, document, declarations: { references: {}, embedded: [] }, author: "usr_a", createdAt: at,
});
const pins = (n: number) => Array.from({ length: n }, (_, i) => `frv_${i + 1}`);

let root: Root;
let box: HTMLElement;
// What each path answers; a path not listed answers an empty list. The fragment answers arrive in the
// reverse of their pinned order, so a page that shows them as they arrive shows them out of order.
let answers: Record<string, ReadResult<unknown>>;
let asked: string[];

const client: Reads["client"] = {
  read: async <T,>(path: string) => {
    asked.push(path);
    if (path.startsWith("/fragment-revisions/")) {
      const n = Number(path.slice(path.lastIndexOf("_") + 1));
      await new Promise((r) => setTimeout(r, Math.max(0, 8 - n) * 2));
    }
    return (answers[path] ?? ok({ items: [] })) as ReadResult<T>;
  },
};
const settle: Reads["settle"] = (res, set, live, held) => {
  if (res.kind !== "ok" && held()) return true;
  if (live()) set(res);
  return false;
};
const reads: Reads = { client, settle, undropped: () => () => true, view: "v", again: 0, regained: 0, go: () => {}, failure: () => "FAILED" };

const mount = (page: "profile" | "revision") =>
  act(async () => {
    const view = page === "profile" ? h(SourceItem, { kind: "profile", id: "prf_a" }) : h(RevisionItem, { kind: "profile", id: "prv_a" });
    root.render(h(WritesProvider, { value: new Writes(() => "p") }, h(ShellContext, { value: { roles: ["viewer"], offline: false } }, h(ReadsProvider, { value: reads }, view))));
  });
const settled = () => act(async () => void (await new Promise((r) => setTimeout(r, 120))));
const shown = () => [...box.querySelectorAll('[data-testid="pinned-fragment"]')];
const documents = () => [...box.querySelectorAll('[data-testid="fragment-document"]')].map((e) => e.textContent);
const fragmentReads = () => asked.filter((p) => p.startsWith("/fragment-revisions/"));

beforeEach(() => {
  box = document.createElement("div");
  document.body.append(box);
  root = createRoot(box);
  asked = [];
  answers = {
    "/profiles/prf_a": ok(profile("prv_a")),
    "/profile-revisions/prv_a": ok(profileRevision(["frv_1", "frv_2", "frv_3"])),
    "/fragment-revisions/frv_1": ok(fragment("frv_1", "base", "cluster", "machine:\n  type: controlplane\n")),
    "/fragment-revisions/frv_2": ok(fragment("frv_2", "network", "scope", "machine:\n  network: {}\n")),
    "/fragment-revisions/frv_3": ok(fragment("frv_3", "labels", "machine", "machine:\n  nodeLabels: {}\n")),
  };
});
afterEach(async () => {
  await act(async () => root.unmount());
  box.remove();
  localStorage.clear();
  sessionStorage.clear();
});

describe.each(["profile", "revision"] as const)("ginsys/bronzeward#997: the %s page shows the documents it pins", (page) => {
  test("each pinned fragment's document shows in the pinned order, with its name and layer", async () => {
    await mount(page);
    await settled();
    expect(documents()).toEqual(["machine:\n  type: controlplane\n", "machine:\n  network: {}\n", "machine:\n  nodeLabels: {}\n"]);
    const heads = shown().map((e) => e.querySelector("h4")?.textContent);
    expect(heads).toEqual(["base (cluster)", "network (scope)", "labels (machine)"]);
  });

  test("a fragment whose read fails shows its identifier and the reason, and the others still show", async () => {
    answers["/fragment-revisions/frv_2"] = { kind: "refused", status: 404, problem: null };
    await mount(page);
    await settled();
    expect(documents()).toEqual(["machine:\n  type: controlplane\n", "machine:\n  nodeLabels: {}\n"]);
    const rows = shown();
    expect(rows).toHaveLength(3);
    expect(rows[1]!.textContent).toContain("frv_2");
    expect(rows[1]!.textContent).toContain("FAILED");
  });

  test("a document with markup characters is shown as text, never as markup", async () => {
    const text = "<script>alert(1)</script>\n&amp; <!-- note --> <b>bold</b>\n";
    answers["/fragment-revisions/frv_1"] = ok(fragment("frv_1", "base", "cluster", text));
    await mount(page);
    await settled();
    const pre = box.querySelector('[data-testid="fragment-document"]')!;
    expect(pre.textContent).toBe(text);
    expect(pre.children).toHaveLength(0);
    expect(box.querySelector("script")).toBeNull();
    expect(box.querySelector("b")).toBeNull();
  });

  test("each pinned fragment revision is read once", async () => {
    await mount(page);
    await settled();
    expect(fragmentReads().sort()).toEqual(["/fragment-revisions/frv_1", "/fragment-revisions/frv_2", "/fragment-revisions/frv_3"]);
    expect(asked.filter((p) => p === "/profile-revisions/prv_a")).toHaveLength(1);
  });

  test("the documents have no copy control, no download link and leave nothing in browser storage", async () => {
    await mount(page);
    await settled();
    expect(documents()).toHaveLength(3);
    expect(box.querySelector("button")).toBeNull();
    expect(box.querySelector("a[download]")).toBeNull();
    expect(box.querySelector("textarea")).toBeNull();
    expect(localStorage.length + sessionStorage.length).toBe(0);
  });

  test("more than 20 pins read and show the first 20, and a button reads the rest", async () => {
    answers["/profile-revisions/prv_a"] = ok(profileRevision(pins(25)));
    for (const id of pins(25)) answers[`/fragment-revisions/${id}`] = ok(fragment(id, `f${id}`, "cluster", `doc ${id}\n`));
    await mount(page);
    await settled();
    expect(fragmentReads()).toHaveLength(20);
    expect(documents()).toHaveLength(20);
    const more = [...box.querySelectorAll("button")].filter((b) => b.textContent === "Show the remaining 5");
    expect(more).toHaveLength(1);
    await act(async () => more[0]!.click());
    await settled();
    expect(fragmentReads()).toHaveLength(25);
    expect(documents()).toHaveLength(25);
    expect(documents()[24]).toBe("doc frv_25\n");
  });
});

describe("ginsys/bronzeward#997: the profile page with no revision", () => {
  test("says so and reads no profile revision", async () => {
    answers["/profiles/prf_a"] = ok(profile(null));
    await mount("profile");
    await settled();
    expect(box.textContent).toContain("No revision yet.");
    expect(asked.some((p) => p.startsWith("/profile-revisions/"))).toBe(false);
    expect(shown()).toHaveLength(0);
  });

  test("a failed read of the revision shows its identifier and the reason, not the documents", async () => {
    answers["/profile-revisions/prv_a"] = { kind: "refused", status: 404, problem: null };
    await mount("profile");
    await settled();
    expect(box.textContent).toContain("prv_a");
    expect(box.textContent).toContain("FAILED");
    expect(shown()).toHaveLength(0);
    expect(fragmentReads()).toHaveLength(0);
  });
});

describe("ginsys/bronzeward#997: a move from one profile page to another", () => {
  test("after the rest was read, the new page starts only its own first 20 reads", async () => {
    for (const rev of ["prv_a", "prv_b"]) answers[`/profile-revisions/${rev}`] = ok({ ...profileRevision(pins(25)), id: rev });
    for (const id of pins(25)) answers[`/fragment-revisions/${id}`] = ok(fragment(id, `f${id}`, "cluster", `doc ${id}\n`));
    const go = (path: string, v: string) =>
      act(async () => {
        const page = view(path, "", () => {}) as ReactElement;
        root.render(h(WritesProvider, { value: new Writes(() => "p") }, h(ShellContext, { value: { roles: ["viewer"], offline: false } }, h(ReadsProvider, { value: { ...reads, view: v } }, page))));
      });
    await go("/profile-revisions/prv_a", "v1");
    await settled();
    await act(async () => [...box.querySelectorAll("button")].find((b) => b.textContent === "Show the remaining 5")!.click());
    await settled();
    expect(fragmentReads()).toHaveLength(25);
    asked.length = 0;
    await go("/profile-revisions/prv_b", "v2");
    await settled();
    expect(fragmentReads()).toHaveLength(20);
    expect(documents()).toHaveLength(20);
  });
});
