// @vitest-environment happy-dom
// The fragment and profile editors with the Shared (library) choice (ginsys/bronzeward#1193 part 6b-2): a mounted pane with a
// fake client, the request each Save and Remove sends, the sentence before the press, and the refusals before any send.

import { act, createElement as h } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { ProfilePane } from "./composition.tsx";
import { forgetETag, FragmentPane, keepETag } from "./drafts.tsx";
import { unsaved, WritesProvider } from "./forms.tsx";
import { type Reads, ReadsProvider } from "./list.tsx";
import { ShellContext } from "./shell.tsx";
import { Writes } from "./write.ts";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const DRF = "drf_k2pq3rstuvwxyz234567abcdef";
const entry = (over: Record<string, unknown>) => ({ kind: "fragment", name: "ntp", head: "frg_c", base: 1, revision: "frv_c", ...over });
const sharedFragment = entry({ scope: "library", head: "frg_s", adoptionBase: null, revision: "frv_s" });
const draftOf = (entries: unknown[], state = "open") => ({ id: DRF, cluster: "cl_a", title: "t", state, revision: 3, entries }) as never;
const head = (id: string, name: string, cluster: string | null, revision: string) => ({ id, name, cluster, revision, scope: cluster === null ? "library" : "cluster", layer: "global", headRevision: 1, createdAt: "2026-11-02T00:00:00Z" });

const revision = (id: string) => ({ id, cluster: null, name: "ntp", layer: "global", document: "ntp: pool\n", declarations: { references: {}, embedded: [] }, author: "idn_a", createdAt: "2026-11-02T00:00:00Z" });
const answers: Record<string, unknown> = {
  "/fragment-revisions/frv_c": revision("frv_c"),
  "/fragment-revisions/frv_s": { id: "frv_s", cluster: null, name: "ntp", layer: "global", document: "ntp: pool\n", declarations: { references: {}, embedded: [] }, author: "idn_a", createdAt: "2026-11-02T00:00:00Z" },
  "/fragments": { items: [head("frg_L1", "ntp-servers", null, "frv_L1"), head("frg_L2", "motd", null, "frv_L2"), head("frg_C1", "dns", "cl_a", "frv_C1")] },
  "/profiles": { items: [] },
};

describe("the shared choice of the editors", () => {
  let box: HTMLDivElement;
  let root: Root;
  let writes: Writes;
  let submit: ReturnType<typeof vi.spyOn>;
  const read = vi.fn(async (path: string) => (Object.hasOwn(answers, path) ? { kind: "ok", data: answers[path] } : { kind: "refused", status: 404, problem: null }));
  const reads = { client: { read }, settle: (res: unknown, set: (r: unknown) => void, live: () => boolean) => (live() && set(res), true), undropped: () => () => true, view: "", again: 0, regained: 0, go: () => {}, failure: () => null } as unknown as Reads;
  beforeEach(() => {
    box = document.createElement("div");
    document.body.append(box);
    root = createRoot(box);
    writes = new Writes(() => "p");
    submit = vi.spyOn(writes, "submit").mockImplementation(async () => ({ kind: "unknown" }) as never);
    forgetETag(DRF);
    keepETag(DRF, '"5-t"', undefined);
  });
  afterEach(async () => {
    unsaved.clear();
    await act(async () => root.unmount());
    box.remove();
  });
  const claim = vi.fn();
  const mount = (pane: unknown) =>
    act(async () => root.render(h(WritesProvider, { value: writes }, h(ShellContext, { value: { roles: ["author"], offline: false } }, h(ReadsProvider, { value: reads }, pane as never)))));
  const settled = () => act(async () => void (await new Promise((r) => setTimeout(r, 20))));
  const button = (label: string) => [...box.querySelectorAll("button")].find((b) => b.textContent === label);
  const type = (el: Element, value: string) =>
    act(async () => {
      const proto = el instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
      Object.getOwnPropertyDescriptor(proto, "value")!.set!.call(el, value);
      el.dispatchEvent(new Event("input", { bubbles: true }));
    });
  const field = (selector: string) => box.querySelector(selector)!;
  const sent = (n = 0) => (submit.mock.calls[n]![0] as { spec: Record<string, unknown> }).spec;
  const fragmentPane = (draft: never, e?: unknown) => h(FragmentPane, { draft, entry: e as never, seeChange: () => {}, claim });

  test("a new fragment saved as shared sends the scope in its body, and claims the shared key", async () => {
    claim.mockClear();
    await mount(fragmentPane(draftOf([])));
    expect(box.textContent).not.toContain("Save records a shared revision");
    await type(field("label input[autocomplete]"), "ntp");
    await type(field("textarea"), "ntp: pool\n");
    await act(async () => void (box.querySelector('input[type="checkbox"]') as HTMLInputElement).click());
    expect(box.textContent).toContain("Save records a shared revision in this draft. Publishing the draft moves this cluster to it; no other cluster changes.");
    await act(async () => void button("Save")!.click());
    expect(submit).toHaveBeenCalledTimes(1);
    expect(sent()).toMatchObject({ method: "PUT", path: `/drafts/${DRF}/fragments/ntp`, ifMatch: '"5-t"', body: { scope: "library", layer: "role", document: "ntp: pool\n", marks: null, declarations: null } });
    expect(claim).toHaveBeenCalledWith("ntp:library");
  });

  test("a cluster fragment sends no scope and claims its plain name", async () => {
    claim.mockClear();
    await mount(fragmentPane(draftOf([])));
    await type(field("label input[autocomplete]"), "ntp");
    await type(field("textarea"), "ntp: pool\n");
    await act(async () => void button("Save")!.click());
    expect(Object.hasOwn(sent().body as object, "scope")).toBe(false);
    expect(claim).toHaveBeenCalledWith("ntp");
  });

  test("an opened shared entry keeps its scope: Save sends it, Remove sends the scope query, and both are said before the press", async () => {
    await mount(fragmentPane(draftOf([sharedFragment]), sharedFragment));
    await settled();
    expect(box.querySelector("h3")!.textContent).toContain("shared library");
    expect(box.querySelector('input[type="checkbox"]')).toBeNull();
    expect(box.querySelector('[data-testid="shared-sentence"]')!.textContent).toContain("Remove records, in this draft, that the shared name leaves the library. Clusters that adopted it keep their revision.");
    await act(async () => void button("Remove")!.click());
    expect(sent(0)).toMatchObject({ method: "DELETE", path: `/drafts/${DRF}/fragments/ntp?scope=library`, ifMatch: '"5-t"' });
    expect(sent(0)["body"]).toBeUndefined();
  });

  test("a cluster entry's Remove sends no query", async () => {
    const own = entry({});
    await mount(fragmentPane(draftOf([own]), own));
    await settled();
    await act(async () => void button("Remove")!.click());
    expect(sent(0)).toMatchObject({ method: "DELETE", path: `/drafts/${DRF}/fragments/ntp` });
    expect(box.querySelector('[data-testid="shared-sentence"]')).toBeNull();
  });

  test("a draft that already holds the shared name refuses a second one, and holds none for a cluster fragment of the name", async () => {
    await mount(fragmentPane(draftOf([sharedFragment])));
    await type(field("label input[autocomplete]"), "ntp");
    await type(field("textarea"), "a: 1\n");
    await act(async () => void (box.querySelector('input[type="checkbox"]') as HTMLInputElement).click());
    await act(async () => void button("Save")!.click());
    expect(submit).not.toHaveBeenCalled();
    expect(box.textContent).toContain("Not sent: this draft already holds shared fragment ntp. Open it from Entries to change it.");
    // The cluster's own fragment of the name is another slot: a shared one of that name is allowed beside it.
    await act(async () => root.unmount());
    root = createRoot(box);
    await mount(fragmentPane(draftOf([entry({})])));
    await type(field("label input[autocomplete]"), "ntp");
    await type(field("textarea"), "a: 1\n");
    await act(async () => void (box.querySelector('input[type="checkbox"]') as HTMLInputElement).click());
    await act(async () => void button("Save")!.click());
    expect(submit).toHaveBeenCalledTimes(1);
  });

  test("a new shared profile offers shared fragments only and saves them under the scope; a cluster profile offers the cluster's", async () => {
    await mount(h(ProfilePane, { draft: draftOf([]), entry: undefined, seeChange: () => {}, claim }));
    await settled();
    const options = () => [...box.querySelectorAll("select option")].map((o) => o.textContent ?? "");
    expect(options().join("|")).toContain("dns");
    expect(options().join("|")).not.toContain("ntp-servers");
    await act(async () => void (box.querySelector('input[type="checkbox"]') as HTMLInputElement).click());
    await settled();
    expect(box.textContent).toContain("A shared profile pins shared fragment revisions");
    expect(options().join("|")).toContain("ntp-servers");
    expect(options().join("|")).toContain("motd");
    expect(options().join("|")).not.toContain("dns");
    // Switching the choice drops pins chosen for the other scope: a cluster's revision is no pin of a shared profile.
    expect(box.textContent).toContain("None.");
    await type(field("label input[autocomplete]"), "edge");
    const select = box.querySelector("select") as HTMLSelectElement;
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, "value")!.set!.call(select, "frv_L1");
      select.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await act(async () => void button("Add to profile")!.click());
    await act(async () => void button("Save")!.click());
    expect(sent()).toMatchObject({ method: "PUT", path: `/drafts/${DRF}/profiles/edge`, body: { scope: "library", fragments: ["frv_L1"] } });
    expect(claim).toHaveBeenLastCalledWith("edge:library");
  });
});
