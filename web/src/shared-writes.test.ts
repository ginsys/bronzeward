import { describe, expect, test } from "vitest";
import type { components } from "./api/v1";
import { choices, libraryChoices } from "./composition.tsx";
import { inUse, inUseShared, isShared, keyOf, paneEntry } from "./drafts.tsx";
import { claimedName, scopeMember, scopeQuery } from "./shared-writes.ts";

type Source = components["schemas"]["SourceEntry"];
type Fragment = components["schemas"]["FragmentBody"];

const own = (name: string, kind = "fragment", over: Partial<Source> = {}) => ({ kind, name, head: "frg_1", base: 1, revision: `frv_${name}`, ...over }) as Source;
const shared = (name: string, kind = "fragment", over: Partial<Source> = {}) => ({ kind, name, scope: "library", head: "frg_s", base: 1, adoptionBase: null, revision: `frv_s_${name}`, ...over }) as Source;
const head = (name: string, cluster: string | null, revision: string | null) => ({ id: `frg_${name}`, cluster, name, revision, scope: cluster === null ? "library" : "cluster", layer: "global", headRevision: 1, createdAt: "2026-11-02T00:00:00Z" }) as Fragment;

describe("what a shared Save and Remove send", () => {
  test("the scope member of the body, the scope query of a removal, and the claimed name", () => {
    expect(scopeMember(true)).toEqual({ scope: "library" });
    expect(scopeMember(false)).toEqual({});
    expect(scopeQuery(true)).toBe("?scope=library");
    expect(scopeQuery(false)).toBe("");
    expect(claimedName("ntp", true)).toBe("ntp:library");
    expect(claimedName("ntp", false)).toBe("ntp");
  });

  test("a new shared entry's claimed name finds it by the key its slot has, apart from the cluster's entry of the name", () => {
    const sources = [own("ntp"), shared("ntp")];
    expect(keyOf(shared("ntp"))).toBe("fragment:ntp:library");
    expect(paneEntry(sources, "fragment:", claimedName("ntp", true))).toBe(sources[1]);
    expect(paneEntry(sources, "fragment:", claimedName("ntp", false))).toBe(sources[0]);
    expect(paneEntry(sources, "profile:", claimedName("ntp", true))).toBeUndefined();
    // A shared entry is opened from the list by its own key.
    expect(paneEntry(sources, keyOf(shared("ntp")), undefined)).toBe(sources[1]);
  });

  test("a draft holds a shared entry of a name apart from the cluster's, of its own kind", () => {
    const entries = [own("ntp"), shared("dns"), shared("edge", "profile", { revision: null })];
    expect([inUseShared({ entries }, "ntp", "fragment"), inUseShared({ entries }, "dns", "fragment"), inUseShared({ entries }, "dns", "profile")]).toEqual([false, true, false]);
    // A removed shared entry still holds the slot.
    expect(inUseShared({ entries }, "edge", "profile")).toBe(true);
    expect([inUse({ entries }, "ntp"), inUse({ entries }, "dns")]).toEqual([true, false]);
    expect(isShared(shared("x"))).toBe(true);
    expect(isShared(own("x"))).toBe(false);
  });
});

describe("what a shared profile can pin", () => {
  const fragments = [head("ntp", null, "frv_L_ntp"), head("motd", null, "frv_L_motd"), head("gone", null, null), head("dns", "cl_a", "frv_C_dns")];
  const draft = { cluster: "cl_a", entries: [] as Source[] };

  test("shared fragments' head revisions in name order, and no fragment of a cluster", () => {
    expect(libraryChoices(draft, fragments)).toEqual([
      { name: "motd", revision: "frv_L_motd", from: "base" },
      { name: "ntp", revision: "frv_L_ntp", from: "base" },
    ]);
  });

  test("the revision the draft saves as shared comes first for its name; a shared removal offers none; a cluster entry is not offered", () => {
    const entries = [shared("ntp", "fragment", { revision: "frv_draft_ntp" }), shared("motd", "fragment", { revision: null }), own("dns")];
    expect(libraryChoices({ entries }, fragments)).toEqual([{ name: "ntp", revision: "frv_draft_ntp", from: "draft" }]);
  });

  test("a cluster profile still offers the cluster's own and never a shared fragment", () => {
    expect(choices({ cluster: "cl_a", entries: [shared("ntp")] }, fragments).map((c) => c.name)).toEqual(["dns"]);
    expect(choices({ cluster: "cl_a", entries: [own("dns", "fragment", { revision: "frv_dns2" })] }, fragments)).toEqual([{ name: "dns", revision: "frv_dns2", from: "draft" }]);
  });
});
