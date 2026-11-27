import { createElement as h, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import type { components } from "./api/v1";
import { hasShared } from "./drafts.tsx";
import { type Reads, ReadsProvider } from "./list.tsx";
import { FRAGMENTS, PROFILES, Rows } from "./resources.tsx";
import { AdoptionRows, changesOf, Reach, ReachItems, scopeWords, SharedClusters, useOf } from "./shared.tsx";

type S = components["schemas"];

// Pages read through the shell; these answer nothing, so a component shows what it holds before any read.
const go = () => {};
const reads: Reads = { client: { read: () => new Promise(() => {}) }, settle: () => false, undropped: () => () => true, view: "", again: 0, regained: 0, go, failure: () => null };
const html = (e: ReactNode) => renderToStaticMarkup(h(ReadsProvider, { value: reads }, e));
const text = (e: ReactNode) => html(e).replace(/<[^>]*>/g, "");

const fragment = (over: Partial<S["FragmentBody"]>): S["FragmentBody"] =>
  ({ id: "frg_a", cluster: null, scope: "library", name: "ntp", layer: "global", revision: "frv_a", headRevision: 1, createdAt: "2026-11-02T00:00:00Z", ...over }) as S["FragmentBody"];
const profile = (over: Partial<S["ProfileBody"]>): S["ProfileBody"] =>
  ({ id: "prf_a", cluster: null, scope: "library", name: "edge", revision: "prv_a", headRevision: 1, createdAt: "2026-11-02T00:00:00Z", ...over }) as S["ProfileBody"];

describe("the shared mark in the lists", () => {
  test("a fragment and a profile of the library read shared; a cluster's own reads cluster", () => {
    const fragments = text(h(ReadsProvider, { value: reads }, h(Rows<S["FragmentBody"]>, { label: "Fragments", cols: FRAGMENTS, items: [fragment({}), fragment({ id: "frg_b", cluster: "cl_a", scope: "cluster", name: "dns" })], keyOf: (f) => f.id })));
    // The cells run together in text: the owner column then the scope column.
    expect(fragments).toContain("shared libraryshared");
    expect(fragments).toMatch(/clustercluster/);
    const profiles = text(h(ReadsProvider, { value: reads }, h(Rows<S["ProfileBody"]>, { label: "Profiles", cols: PROFILES, items: [profile({})], keyOf: (p) => p.id })));
    expect(profiles).toContain("shared libraryshared");
  });

  test("a scope this page does not know is shown as sent", () => {
    expect([scopeWords("library"), scopeWords("cluster"), scopeWords("tenant")]).toEqual(["shared", "cluster", "tenant"]);
  });
});

describe("the clusters of a shared name", () => {
  const row = (over: Partial<S["ClusterAdoptionBody"]>): S["ClusterAdoptionBody"] =>
    ({ cluster: "cl_a", revision: "frv_old", adoptionRevision: 2, behind: false, release: "rel_a", via: null, machines: ["mch_a", "mch_b"], ...over }) as S["ClusterAdoptionBody"];

  test("each row names the revision, whether it is behind, its release and its machine count", () => {
    const out = text(h(ReadsProvider, { value: reads }, h(AdoptionRows, { rows: [row({ behind: true }), row({ cluster: "cl_b", release: null, machines: [] })] })));
    expect(out).toContain("behind the shared head");
    expect(out).toContain("on the shared head");
    expect(out).toContain("none yet");
    expect(out).toContain("2");
  });

  test("a cluster that only reaches the name through a shared profile is labelled as not an adoption", () => {
    expect(useOf({ behind: false, via: "edge" })).toBe("through the shared profile edge (not an adoption)");
    expect(useOf({ behind: true, via: "edge" })).toBe("through the shared profile edge (not an adoption)");
    // A cluster that adopts the name and also reaches it through a profile has two rows, both kept.
    const out = text(h(ReadsProvider, { value: reads }, h(AdoptionRows, { rows: [row({}), row({ via: "edge", adoptionRevision: null, release: null })] })));
    expect(out).toContain("on the shared head");
    expect(out).toContain("not an adoption");
  });

  test("the page says what it changes (no cluster) and offers no control", () => {
    const out = html(h(SharedClusters, { kind: "fragment", head: "frg_a" }));
    expect(out).toContain("Clusters using this fragment");
    expect(out).toContain("This page changes no cluster");
    expect(out).not.toContain("<button");
    expect(html(h(SharedClusters, { kind: "profile", head: "prf_a" }))).toContain("Clusters using this profile");
  });
});

describe("what a draft's shared entries reach here", () => {
  const item = (over: Partial<S["ImpactItem"]>): S["ImpactItem"] => ({ kind: "fragment", name: "ntp", scope: "library", machines: [], otherClusters: [], ...over }) as S["ImpactItem"];
  const m = (machine: string, change: string, reason = "assignment") => ({ machine, change, reason }) as S["ImpactMachine"];

  test("machines gaining, losing and keeping it, and the other clusters unchanged or also changing", () => {
    const out = text(h(ReadsProvider, { value: reads }, h(ReachItems, {
      items: [item({
        machines: [m("mch_a", "gains"), m("mch_b", "gains", "profile"), m("mch_c", "loses"), m("mch_d", "keeps")],
        otherClusters: [{ cluster: "cl_b", revision: "frv_1", via: null, unchanged: true }, { cluster: "cl_c", revision: "frv_2", via: "edge", unchanged: false }],
      })],
    })));
    expect(out).toContain("fragment ntp (shared library)");
    expect(out).toContain("Gains it here: 2 machines");
    expect(out).toContain("its assignment selects the name, a profile of this cluster pins it");
    expect(out).toContain("Loses it here: 1 machine");
    expect(out).toContain("Keeps its revision here: 1 machine");
    expect(out).toContain("Stays unchanged:");
    expect(out).toContain("Also changes:");
    expect(out).toContain("through the shared profile edge");
  });

  test("a change this page does not know is shown as sent, never dropped", () => {
    expect(changesOf([m("mch_a", "keeps"), m("mch_b", "swaps"), m("mch_c", "gains")])).toEqual(["gains", "keeps", "swaps"]);
    const out = text(h(ReadsProvider, { value: reads }, h(ReachItems, { items: [item({ machines: [m("mch_a", "swaps", "elsewhere")] })] })));
    expect(out).toContain("swaps here: 1 machine");
    expect(out).toContain("elsewhere");
    expect(out).not.toContain("Reaches no machine");
  });

  test("an entry that reaches nothing says so, a cluster entry is not listed, and a draft with none says so", () => {
    expect(text(h(ReadsProvider, { value: reads }, h(ReachItems, { items: [item({})] })))).toContain("Reaches no machine of this cluster.");
    const own = text(h(ReadsProvider, { value: reads }, h(ReachItems, { items: [item({ name: "dns", scope: "cluster", machines: [m("mch_a", "gains")] })] })));
    expect(own).toBe("The draft holds no shared entry.");
  });

  test("a long list of machines shows the first ten and counts the rest", () => {
    const many = Array.from({ length: 13 }, (_, i) => m(`mch_${String(i).padStart(2, "0")}`, "gains"));
    const out = text(h(ReadsProvider, { value: reads }, h(ReachItems, { items: [item({ machines: many })] })));
    expect(out).toContain("Gains it here: 13 machines");
    expect(out).toContain("and 3 more");
  });

  test("the block says what it is before its read answers", () => {
    const out = text(h(ReadsProvider, { value: reads }, h(Reach, { draft: "drf_a", n: 0 })));
    expect(out).toContain("What the shared entries reach");
    expect(out).toContain("renders no configuration");
  });

  test("a draft is read for reach only when it holds an adoption, its removal or a shared entry", () => {
    const own = { kind: "fragment", name: "ntp", head: "frg_1", base: 1, revision: "frv_own" };
    const shared = { ...own, scope: "library", adoptionBase: null };
    const adoption = { kind: "adoption", of: "fragment", name: "ntp", head: "frg_2", base: null, revision: "frv_a" };
    const dropped = { ...adoption, revision: null };
    expect([own, shared, adoption, dropped].map((e) => hasShared({ entries: [e] } as never))).toEqual([false, true, true, true]);
    expect(hasShared({ entries: [] })).toBe(false);
  });
});
