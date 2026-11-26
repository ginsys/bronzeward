import { createElement as h } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import type { components } from "./api/v1";
import { choices, fragmentOffers, profileOffers } from "./composition.tsx";
import { change, EntryName, inUse, isChange, isSource, keyOf } from "./drafts.tsx";
import { type Reads, ReadsProvider } from "./list.tsx";
import { DraftChanges } from "./publish.tsx";

type Source = components["schemas"]["SourceEntry"];
type Adoption = components["schemas"]["AdoptionEntry"];

// A draft holds a cluster entry and a shared (library) entry of one name in two slots. The console
// writes the cluster's slot alone, so the shared entry never stands for it.
const shared: Source = { kind: "fragment", name: "ntp", scope: "library", head: "frg_2", base: 2, adoptionBase: null, revision: "frv_shared" };
const own: Source = { kind: "fragment", name: "ntp", head: "frg_1", base: 1, revision: "frv_own" };
const draft = (...entries: Source[]) => ({ cluster: "cl_1", entries });
// EntryName reads a machine's name through the page's reads; these answer nothing.
const reads: Reads = { client: { read: () => new Promise(() => {}) }, settle: () => true, undropped: () => () => true, view: "", again: 0, regained: 0, go: () => {}, failure: () => null };
const named = (e: Source | Adoption) => renderToStaticMarkup(h(ReadsProvider, { value: reads }, h(EntryName, { e })));
const adopted: Adoption = { kind: "adoption", of: "fragment", name: "ntp", head: "frg_2", base: null, revision: "frv_adopted" };
const dropped: Adoption = { kind: "adoption", of: "profile", name: "ntp", head: "prf_2", base: 4, revision: null };

describe("shared draft entries", () => {
  test("the two slots of one name have their own keys, and the shared one says what it is", () => {
    expect(keyOf(shared)).not.toBe(keyOf(own));
    expect(named(shared)).toContain("shared library");
    expect(named(own)).not.toContain("shared library");
  });

  test("a shared entry takes no name in the cluster's slot", () => {
    expect(inUse(draft(shared), "ntp")).toBe(false);
    expect(inUse(draft(shared, own), "ntp")).toBe(true);
  });

  // An adoption entry has no scope member: it must not read as the cluster's own entry, which the page edits.
  test("an adoption entry and removal are shared, with their own keys and words", () => {
    expect([isSource(adopted), isChange(adopted), isSource(dropped), isChange(dropped)]).toEqual([false, true, false, true]);
    expect(new Set([keyOf(adopted), keyOf(dropped), keyOf(own), keyOf(shared), keyOf({ ...own, kind: "profile" })]).size).toBe(5);
    expect(named(adopted)).toContain("fragment ntp (shared library)");
    expect(named(dropped)).toContain("profile ntp (shared library)");
    expect(change(adopted)).toBe("this cluster adopts it (base adoption none)");
    expect(change(dropped)).toBe("this cluster stops following it (base adoption 4)");
    expect(inUse(draft(adopted as never), "ntp")).toBe(false);
    expect(choices(draft(adopted as never), [])).toEqual([]);
    expect(profileOffers(draft(dropped as never), [])).toEqual([]);
  });

  test("a refused publication's changes name an adoption entry and removal", () => {
    const d = { id: "drf_1", cluster: "cl_1", title: "t", state: "open", revision: 3, entries: [adopted, dropped] };
    const said = renderToStaticMarkup(h(ReadsProvider, { value: reads }, h(DraftChanges, { d, revision: 3 }))).replace(/<[^>]*>/g, "");
    expect(said).toContain("fragment ntp (shared library) (adopted)");
    expect(said).toContain("profile ntp (shared library) (no longer followed)");
  });

  test("the pickers offer none of it, and a shared entry hides no cluster head", () => {
    const head = { id: "frg_1", cluster: "cl_1", scope: "cluster", name: "ntp", layer: "cluster", revision: "frv_head", headRevision: 1, createdAt: "2026-11-02T00:00:00Z" };
    const library = { ...head, id: "frg_2", cluster: null, scope: "library", name: "dns" };
    expect(choices(draft(shared), [head, library]).map((c) => c.revision)).toEqual(["frv_head"]);
    expect(fragmentOffers(draft(shared), [head, library], { frv_shared: "cluster" }).cluster).toEqual(["ntp"]);
    expect(profileOffers(draft({ ...shared, kind: "profile" }), [])).toEqual([]);
  });
});
