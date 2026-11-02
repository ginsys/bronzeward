import { createElement as h } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import type { components } from "./api/v1";
import {
  ASSIGNMENT_STATEMENT, AssignmentPane, assignmentBody, assignmentRefusal, choices, compose, fragmentOffers, fromRevision, isCurrent, listsOf, machineOffers, move, noLists, PINS_MAX, PINS_STATEMENT,
  pinned, pinRefusal, pinsOf, ProfilePane, profileBody, profileOffers, readComposition, referenceStates, sourceKey, stillOffered, unknownPins, withLists, withoutPin, withPin,
} from "./composition.tsx";
import { editor, EMPTY, holdsUnsaved, where } from "./drafts.tsx";
import { WritesProvider } from "./forms.tsx";
import { type Reads, ReadsProvider } from "./list.tsx";
import { ShellContext } from "./shell.tsx";
import { Refused } from "./states.tsx";
import { Writes } from "./write.ts";

type Source = components["schemas"]["SourceEntry"];
type Fragment = components["schemas"]["FragmentBody"];
const src = (e: Partial<Source>): Source => ({ kind: "fragment", name: "f", head: "frg_1", base: 1, revision: "frv_2", document: "a: 1\n", ...e });
const frag = (e: Partial<Fragment>): Fragment => ({ id: "frg_1", cluster: "cl_1", scope: "s", name: "f", layer: "role", revision: "frv_1", headRevision: 1, createdAt: "2026-11-02T00:00:00Z", ...e });

describe("a profile's pins, as a list the keyboard moves (W4)", () => {
  test("move up and down swap with the neighbour and change nothing at an end", () => {
    expect(move(["a", "b", "c"], 1, -1)).toEqual(["b", "a", "c"]);
    expect(move(["a", "b", "c"], 1, 1)).toEqual(["a", "c", "b"]);
    const top = ["a", "b"];
    expect(move(top, 0, -1)).toBe(top);
    expect(move(top, 1, 1)).toBe(top);
    expect(move(top, 5, -1)).toBe(top);
  });

  test("a move leaves the list it was given as it was", () => {
    const l = ["a", "b"];
    move(l, 1, -1);
    expect(l).toEqual(["a", "b"]);
  });

  test("adding appends a revision once, removing drops one row", () => {
    expect(withPin(["a"], "b")).toEqual(["a", "b"]);
    const l = ["a", "b"];
    expect(withPin(l, "b")).toBe(l);
    expect(withoutPin(["a", "b", "c"], 1)).toEqual(["a", "c"]);
  });

  test("the list lives in the editor's text, one revision id per line, and an empty list is no text", () => {
    expect(pinsOf(pinned(EMPTY, ["frv_1", "frv_2"]))).toEqual(["frv_1", "frv_2"]);
    expect(pinned(EMPTY, ["frv_1", "frv_2"]).document).toBe("frv_1\nfrv_2");
    expect(pinsOf(EMPTY)).toEqual([]);
    expect(pinned(EMPTY, [])).toEqual(EMPTY);
  });

  test("the body sends revision ids in order and nothing else", () => {
    expect(profileBody(["frv_2", "frv_1"])).toEqual({ fragments: ["frv_2", "frv_1"] });
  });

  test("a Save sends 1 to 256 pins", () => {
    expect(pinRefusal([])).toMatch(/at least one/);
    expect(pinRefusal(["a"])).toBeUndefined();
    expect(pinRefusal(Array.from({ length: PINS_MAX }, (_, i) => `r${i}`))).toBeUndefined();
    expect(pinRefusal(Array.from({ length: PINS_MAX + 1 }, (_, i) => `r${i}`))).toMatch(/at most 256/);
  });

  test("the page says that a profile pins revisions", () => {
    expect(PINS_STATEMENT).toBe("A profile pins these fragment revisions. A later save of a fragment does not change the pin.");
  });
});

describe("the revisions a pin can name (api: updateProfile)", () => {
  test("the draft's own revision of a fragment, else the cluster fragment's head revision", () => {
    const draft = { cluster: "cl_1", entries: [src({ name: "a", revision: "frv_a2" }), src({ name: "b", revision: null }), src({ kind: "profile", name: "p", revision: "prv_1" })] };
    const list = [
      frag({ name: "a", revision: "frv_a1" }),
      frag({ name: "b", revision: "frv_b1" }),
      frag({ name: "c", revision: "frv_c1" }),
      frag({ name: "d", revision: null }),
      frag({ name: "e", cluster: "cl_2", revision: "frv_e1" }),
    ];
    expect(choices(draft, list)).toEqual([
      { name: "a", revision: "frv_a2", from: "draft" },
      { name: "c", revision: "frv_c1", from: "base" },
    ]);
  });

  test("a pin that is not one of them is not current, so a later save of the fragment shows", () => {
    const options = [{ name: "a", revision: "frv_a2", from: "draft" as const }];
    expect(isCurrent("frv_a2", options)).toBe(true);
    expect(isCurrent("frv_a1", options)).toBe(false);
  });

  test("a selection the list no longer offers is not chosen, so Add cannot append it", () => {
    const options = [{ name: "a", revision: "frv_a2", from: "draft" as const }];
    expect(stillOffered("frv_a2", options)).toBe("frv_a2");
    expect(stillOffered("frv_a1", options)).toBe("");
    expect(stillOffered("", options)).toBe("");
  });
});

describe("a 422 on a profile or an assignment (api: updateProfile, updateAssignment)", () => {
  const p = (extra: Record<string, unknown>) => ({ type: "urn:bronzeward:problem:validation-failed", title: "t", status: 422, ...extra });
  test("where reads the singular path the server sends", () => {
    expect(where(p({ path: "fragments[2]" }), "")).toBe("At fragments[2].");
    expect(where(p({ path: "fragments.role[1]" }), "")).toBe("At fragments.role[1].");
    expect(where(p({ paths: ["a", "b"] }), "")).toBe("At a, b.");
  });

  test("the refusal does not print the revision member", () => {
    const out = renderToStaticMarkup(h(Refused, { problem: p({ path: "fragments[0]", revision: "frv_secretlike" }), status: 422, verb: "saved" }));
    expect(out).not.toContain("frv_secretlike");
  });
});

describe("the editor's conflict rules apply to a list unchanged", () => {
  test("a moved list is unsaved until a done write stores it", () => {
    const saved = pinned(EMPTY, ["a", "b"]);
    let s = editor({ held: saved, saved }, { kind: "type", held: pinned(EMPTY, ["b", "a"]) });
    expect(holdsUnsaved(s.held, s.saved, s.conflict?.mine)).toBe(true);
    s = editor(s, { kind: "send" });
    s = editor(s, { kind: "done" });
    expect(holdsUnsaved(s.held, s.saved, s.conflict?.mine)).toBe(false);
    expect(pinsOf(s.saved)).toEqual(["b", "a"]);
  });

  test("a 412 keeps the operator's list aside until theirs shows", () => {
    const saved = pinned(EMPTY, ["a", "b"]);
    let s = editor({ held: saved, saved }, { kind: "type", held: pinned(EMPTY, ["b", "a"]) });
    s = editor(s, { kind: "send" });
    s = editor(s, { kind: "refused" });
    expect(s.conflict?.phase).toBe("refused");
    expect(pinsOf(s.conflict!.mine)).toEqual(["b", "a"]);
    expect(holdsUnsaved(s.held, s.saved, s.conflict?.mine)).toBe(true);
  });
});

describe("the profile entry (W4)", () => {
  const reads: Reads = { client: { read: () => new Promise(() => {}) }, settle: () => true, undropped: () => () => true, view: "", again: 0, regained: 0, go: () => {}, failure: () => null };
  const draft = { id: "drf_1", cluster: "cl_1", title: "t", state: "open", revision: 6, entries: [] };
  const pane = (state = "open") =>
    renderToStaticMarkup(h(WritesProvider, { value: new Writes(() => "p") }, h(ShellContext, { value: { roles: ["author"], offline: false } },
      h(ReadsProvider, { value: reads }, h(ProfilePane, { draft: { ...draft, state }, entry: undefined, seeChange: () => {} })))));

  test("a new profile names itself, says it pins revisions and offers Save", () => {
    const out = pane();
    expect(out).toContain("Profile");
    expect(out).toContain(PINS_STATEMENT);
    expect(out).toContain(">Save</button>");
    expect(out).not.toContain(">Remove</button>");
  });

  test("a draft that is not open takes no write", () => {
    const out = pane("discarded");
    expect(out).not.toContain(">Save</button>");
    expect(out).not.toContain("Add to profile");
  });
});

describe("the pin name lookup waits for the fragment list (ginsys/bronzeward#563)", () => {
  const known = new Map([["frv_a", "a"]]);
  test("nothing is asked while the list loads", () => {
    expect(unknownPins(["frv_a", "frv_b"], known, {}, true)).toEqual([]);
  });
  test("once loaded, only a pin the list does not name and no read answered is asked", () => {
    expect(unknownPins(["frv_a", "frv_b", "frv_c"], known, { frv_c: "" }, false)).toEqual(["frv_b"]);
  });
});

describe("an assignment's lists, as the editor's text (W4)", () => {
  const lists = { ...noLists(), profile: ["p1", "p2"], role: ["r1"], global: ["g1", "g2"] };
  test("profiles come first, then the layers in composition order, one line per member", () => {
    expect(withLists(EMPTY, lists).document).toBe("profile p1\nprofile p2\nglobal g1\nglobal g2\nrole r1");
  });
  test("the text reads back to the same lists and no member is no text", () => {
    expect(listsOf(withLists(EMPTY, lists))).toEqual(lists);
    expect(withLists(EMPTY, noLists())).toEqual(EMPTY);
    expect(listsOf(EMPTY)).toEqual(noLists());
  });
  test("a stored revision gives its profiles and layers in order, and a missing layer is empty", () => {
    expect(fromRevision({ profiles: ["p2", "p1"], fragments: { site: ["s2", "s1"] } })).toEqual({ ...noLists(), profile: ["p2", "p1"], site: ["s2", "s1"] });
    expect(fromRevision({ profiles: [], fragments: { constructor: ["x"] } })).toEqual(noLists());
  });
  test("the body leaves out an empty layer and keeps the order", () => {
    expect(assignmentBody(lists)).toEqual({ profiles: ["p1", "p2"], fragments: { global: ["g1", "g2"], role: ["r1"] } });
    expect(assignmentBody({ ...noLists(), override: ["o"] })).toEqual({ profiles: [], fragments: { override: ["o"] } });
  });
  test("a list moves inside its own section", () => {
    const next = { ...lists, global: move(lists.global, 1, -1) };
    expect(listsOf(withLists(EMPTY, next))).toEqual({ ...lists, global: ["g2", "g1"] });
  });
});

describe("what Save refuses before it sends (api: updateAssignment)", () => {
  test("nothing selected", () => {
    expect(assignmentRefusal(noLists())).toMatch(/at least one/);
  });
  test("more than 256 in all, profiles and fragments together", () => {
    const many = (n: number, p: string) => Array.from({ length: n }, (_, i) => `${p}${i}`);
    expect(assignmentRefusal({ ...noLists(), profile: many(100, "p"), role: many(156, "r") })).toBeUndefined();
    expect(assignmentRefusal({ ...noLists(), profile: many(100, "p"), role: many(157, "r") })).toMatch(/at most 256/);
  });
  test("a profile twice, and a fragment twice across layers", () => {
    expect(assignmentRefusal({ ...noLists(), profile: ["p", "p"] })).toBe("Not sent: profile p is selected twice.");
    expect(assignmentRefusal({ ...noLists(), global: ["f"], role: ["f"] })).toBe("Not sent: fragment f is selected twice.");
  });
  test("a profile and a fragment of one name are two members", () => {
    expect(assignmentRefusal({ ...noLists(), profile: ["x"], role: ["x"] })).toBeUndefined();
  });
});

describe("what the pickers offer", () => {
  const entry = (e: Partial<Source>) => src({ revision: "frv_1", ...e });
  const draft = {
    cluster: "cl_1",
    entries: [
      entry({ name: "mine", revision: "frv_mine" }),
      entry({ name: "gone", revision: null }),
      entry({ name: "unread", revision: "frv_unread" }),
      entry({ kind: "profile", name: "pmine", revision: "prv_1" }),
      entry({ kind: "profile", name: "pgone", revision: null }),
      { kind: "assignment", machine: "mch_held", head: "asg_1", base: 1, revision: "asr_1" } as Source,
    ],
  };
  test("a layer offers only fragments of that layer, a draft entry by its revision's layer", () => {
    const list = [
      frag({ name: "b", layer: "role", revision: "frv_b" }),
      frag({ name: "a", layer: "global", revision: "frv_a" }),
      frag({ name: "gone", layer: "role", revision: "frv_g" }),
      frag({ name: "mine", layer: "global", revision: "frv_m0" }),
      frag({ name: "nohead", layer: "role", revision: null }),
      frag({ name: "other", layer: "role", cluster: "cl_2", revision: "frv_o" }),
    ];
    const offers = fragmentOffers(draft, list, { frv_mine: "role" });
    expect(offers.role).toEqual(["b", "mine"]);
    expect(offers.global).toEqual(["a"]);
    expect(offers.override).toEqual([]);
  });
  test("a draft fragment whose layer is not read, or is not a layer, is left out", () => {
    expect(fragmentOffers(draft, [], {}).role).toEqual([]);
    expect(fragmentOffers(draft, [], { frv_mine: "" })).toEqual(fragmentOffers(draft, [], {}));
  });
  test("profiles: the draft's own, then the cluster's with a head, never a removed one", () => {
    const list: components["schemas"]["ProfileBody"][] = [
      { id: "prf_1", cluster: "cl_1", scope: "s", name: "base", revision: "prv_b", headRevision: 1, createdAt: "t" },
      { id: "prf_2", cluster: "cl_1", scope: "s", name: "pgone", revision: "prv_g", headRevision: 1, createdAt: "t" },
      { id: "prf_3", cluster: "cl_1", scope: "s", name: "nohead", revision: null, headRevision: 1, createdAt: "t" },
      { id: "prf_4", cluster: "cl_2", scope: "s", name: "else", revision: "prv_e", headRevision: 1, createdAt: "t" },
    ];
    expect(profileOffers(draft, list)).toEqual(["base", "pmine"]);
  });
  test("machines: the draft's cluster, without one the draft holds an assignment for", () => {
    const m = (id: string, cluster: string) => ({ id, cluster }) as components["schemas"]["MachineBody"];
    expect(machineOffers(draft, [m("mch_held", "cl_1"), m("mch_free", "cl_1"), m("mch_far", "cl_2")]).map((x) => x.id)).toEqual(["mch_free"]);
  });
});

describe("the composition order (operator-experience §5.4)", () => {
  test("layers from global to override; in a layer the profile pins first, then the assignment's fragments", () => {
    const pin = (name: string, layer: string) => ({ name, revision: `rev_${name}`, layer });
    const order = compose(
      [{ name: "p1", pins: [pin("a", "role"), pin("b", "global")] }, { name: "p2", pins: [pin("c", "role")] }],
      [{ layer: "role", name: "own", revision: "rev_own" }, { layer: "global", name: "g", revision: "rev_g" }],
    );
    expect(order.map((c) => `${c.layer} ${c.name} ${c.via ?? "-"}`)).toEqual(["global b p1", "global g -", "role a p1", "role c p2", "role own -"]);
  });

  test("a revision that two profiles pin, or that the assignment also selects, is composed once at its first place", () => {
    const pin = { name: "a", revision: "rev_a", layer: "role" };
    const order = compose([{ name: "p1", pins: [pin] }, { name: "p2", pins: [pin] }], [{ layer: "role", name: "a", revision: "rev_a" }, { layer: "role", name: "z", revision: "rev_z" }]);
    expect(order.map((c) => `${c.name} ${c.via ?? "-"}`)).toEqual(["a p1", "z -"]);
  });

  test("the source key changes with a draft revision of a name and with the head of a cluster name", () => {
    const lists = { ...noLists(), profile: ["p"], role: ["f"] };
    const profile = (revision: string | null): components["schemas"]["ProfileBody"] => ({ id: "prf_1", cluster: "cl_1", scope: "s", name: "p", revision, headRevision: 1, createdAt: "t" });
    const base = sourceKey({ cluster: "cl_1", entries: [] }, lists, [profile("prv_1")], [frag({ name: "f", revision: "frv_1" })]);
    expect(sourceKey({ cluster: "cl_1", entries: [] }, lists, [profile("prv_2")], [frag({ name: "f", revision: "frv_1" })])).not.toBe(base);
    expect(sourceKey({ cluster: "cl_1", entries: [src({ name: "f", revision: "frv_9" })] }, lists, [profile("prv_1")], [frag({ name: "f", revision: "frv_1" })])).not.toBe(base);
    expect(sourceKey({ cluster: "cl_1", entries: [] }, lists, [profile("prv_1")], [frag({ name: "f", revision: "frv_1" })])).toBe(base);
  });
});

describe("the reference states never carry a value", () => {
  const doc = "a: !bwref one\nb: !bwref two\n";
  test("declared, carried not declared, and declared not in the document", () => {
    const declared = { one: { kind: "string" as const, version: 1 }, three: { kind: "integer" as const, version: 2 } };
    expect(referenceStates(doc, declared).map((r) => `${r.name} ${r.state}`)).toEqual(["one declared", "two carried, not declared", "three declared, not in the document"]);
  });
  test("a name that is an Object property is not a declaration", () => {
    expect(referenceStates("a: !bwref constructor\n", {})[0]).toMatchObject({ name: "constructor", declared: undefined, state: "carried, not declared" });
  });
});

describe("reading the composition, one read at a time", () => {
  const draft = { cluster: "cl_1", entries: [src({ kind: "profile", name: "pown", revision: "prv_own" })] };
  const table: Record<string, unknown> = {
    "/profile-revisions/prv_own": { fragments: ["frv_x"] },
    "/profile-revisions/prv_base": { fragments: ["frv_y", "frv_x"] },
    "/fragment-revisions/frv_x": { id: "frv_x", name: "x", layer: "site", document: "a: 1\n", declarations: { references: {}, embedded: [] } },
    "/fragment-revisions/frv_y": { id: "frv_y", name: "y", layer: "site", document: "b: !bwref k\n", declarations: { references: {}, embedded: [] } },
    "/fragment-revisions/frv_f": { id: "frv_f", name: "f", layer: "site", document: "c: 1\n", declarations: { references: {}, embedded: [] } },
  };
  const profiles: components["schemas"]["ProfileBody"][] = [{ id: "prf_1", cluster: "cl_1", scope: "s", name: "pbase", revision: "prv_base", headRevision: 1, createdAt: "t" }];
  const fragments = [frag({ name: "f", layer: "site", revision: "frv_f" })];
  const lists = { ...noLists(), profile: ["pown", "pbase"], site: ["f"] };
  const asked: string[] = [];
  const get = async <T,>(p: string) => {
    asked.push(p);
    return table[p] as T | undefined;
  };
  test("profile revisions, then the pins once each, then the selected fragments", async () => {
    asked.length = 0;
    const r = await readComposition(draft, lists, profiles, fragments, get);
    expect(typeof r).toBe("object");
    expect(asked).toEqual(["/profile-revisions/prv_own", "/fragment-revisions/frv_x", "/profile-revisions/prv_base", "/fragment-revisions/frv_y", "/fragment-revisions/frv_f"]);
    if (typeof r === "string") return;
    expect(r.order.map((c) => `${c.name} ${c.via ?? "-"}`)).toEqual(["x pown", "y pbase", "f -"]);
  });
  test("a read that gives nothing ends it with a text that names the entry", async () => {
    expect(await readComposition(draft, lists, profiles, fragments, async () => undefined)).toBe("The revision of profile pown could not be read.");
    expect(await readComposition(draft, { ...noLists(), role: ["nope"] }, profiles, fragments, get)).toBe("Fragment nope has no revision to read.");
  });
  test("a selected fragment whose revision is in another layer than its list is reported, not composed", async () => {
    expect(await readComposition(draft, { ...noLists(), override: ["f"] }, profiles, fragments, get)).toBe("Fragment f is in layer site, not override. Remove it from the list and add it again.");
  });
});

describe("the assignment entry (W4)", () => {
  const reads: Reads = { client: { read: () => new Promise(() => {}) }, settle: () => true, undropped: () => () => true, view: "", again: 0, regained: 0, go: () => {}, failure: () => null };
  const draft = { id: "drf_1", cluster: "cl_1", title: "t", state: "open", revision: 6, entries: [] };
  const pane = (state = "open") =>
    renderToStaticMarkup(h(WritesProvider, { value: new Writes(() => "p") }, h(ShellContext, { value: { roles: ["author"], offline: false } },
      h(ReadsProvider, { value: reads }, h(AssignmentPane, { draft: { ...draft, state }, entry: undefined, seeChange: () => {} })))));

  test("a new assignment chooses a machine, shows the six layers in order and offers Save", () => {
    const out = pane();
    expect(out).toContain(ASSIGNMENT_STATEMENT);
    expect(out).toContain(">Save</button>");
    expect(out).not.toContain(">Remove</button>");
    const at = ["Profiles", "Layer global", "Layer site", "Layer cluster", "Layer role", "Layer workload", "Layer override"].map((t) => out.indexOf(`${t}, in order`));
    expect(at.every((x, i) => x > 0 && (i === 0 || x > at[i - 1]!))).toBe(true);
    expect(out).toContain("Show composition and references");
  });
  test("a draft that is not open takes no write", () => {
    const out = pane("discarded");
    expect(out).not.toContain(">Save</button>");
    expect(out).not.toContain("Add to profiles");
  });
});
