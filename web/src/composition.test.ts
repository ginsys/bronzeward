import { createElement as h } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import type { components } from "./api/v1";
import { choices, isCurrent, move, PINS_MAX, PINS_STATEMENT, pinned, pinRefusal, pinsOf, ProfilePane, profileBody, stillOffered, withoutPin, withPin } from "./composition.tsx";
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
