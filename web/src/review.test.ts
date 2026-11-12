import { createElement as h, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import type { components } from "./api/v1";
import { type Reads, ReadsProvider, short } from "./list.tsx";
import { EMPTY_NOTE, nextStep, NO_PROVENANCE, PROVENANCE_LIMIT, ReviewBody, ReviewItem, parts, reviewIds, reviewPath, tokenLabel } from "./review.tsx";
import { WITHHELD } from "./states.tsx";

type Review = components["schemas"]["ReviewBody"];

const go = () => {};
const reads: Reads = { client: { read: () => new Promise(() => {}) }, settle: () => false, undropped: () => () => true, view: "", again: 0, regained: 0, go, failure: () => null };
const html = (e: ReactNode) => renderToStaticMarkup(h(ReadsProvider, { value: reads }, e));

// A review as the server serves it (internal/api/releases.go reviewBody), with synthetic content.
const served = (over: Partial<Review> = {}): Review => ({
  release: "rel_BWSYNTH-h7rt2bd",
  machine: "mch_BWSYNTH-b3caa2q",
  mode: "metal",
  configuration: [
    "machine:",
    "  registries:",
    "    auth:",
    "      username: ci-pull",
    "      password: <redacted:registry/example-pass@1>",
    "  nodeLabels:",
    '    team: ""',
    "  token: <redacted:schema>",
    "  copy: <redacted:value>",
    "  paired: <redacted:paired>",
    "  bare: <redacted>",
    "  member: <redacted:app/map@1#2>",
  ].join("\n"),
  provenance: [{
    reference: "registry/example-pass", version: 1, member: null,
    source: { revision: "frv_BWSYNTH-3jnk4ma", digest: "sha256:0", path: "doc[0]/machine/registries/auth/password", occurrence: 0 },
    output: "doc[0]/machine/registries/auth/password",
  }],
  ...over,
}) as Review;

describe("the rendered review page (ginsys/bronzeward#342, journeys §5.5, W6)", () => {
  test("every redaction token is a badge with its kind spelled out, and the text between them is kept", () => {
    const text = served().configuration!;
    const ps = parts(text);
    expect(ps.map((p) => ("text" in p ? p.text : p.token)).join("")).toBe(text);
    expect(ps.flatMap((p) => ("label" in p ? [p.label] : []))).toEqual([
      "secret registry/example-pass, version 1",
      "secret, marked by the pinned machinery",
      "secret, a copy of a resolved value",
      "redacted, paired with a secret",
      "redacted",
      "secret app/map, version 1, member 2",
    ]);
    expect(tokenLabel("app/str", "3", undefined)).toBe("secret app/str, version 3");
  });

  test("an escaped token, written \\u003c…\\u003e, stays text: the page cannot tell embedded JSON from a YAML value that spells it", () => {
    for (const text of ['"password":"\\u003credacted:app/json@1\\u003e"', "NOTE: '\\u003credacted:schema\\u003e'", "x: <redacted:schema\\u003e"]) {
      expect(parts(text), text).toEqual([{ text }]);
    }
  });

  test("text that only looks like a token stays text, and an empty value is never a badge", () => {
    // The ones after the empty string are outside compilation §8.3: an unknown kind, a name outside the §5.1 grammar, a missing or zero version.
    const outside = ["note: <redacted:example>", "x: <redacted:invalid_name@1>", "x: <redacted:Upper/case@1>", "x: <redacted:app/x@>", "x: <redacted:app/x@0>", "x: <redacted:app//x@1>", "x: <redacted:-a@1>"];
    for (const text of ["team: \"\"", "note: <redacted", "a: redacted:schema", "x: <redact:value>", "", ...outside]) {
      expect(parts(text), text).toEqual(text === "" ? [] : [{ text }]);
    }
  });

  test("the page shows the document with badges, the empty-value note and the provenance exactly as served", () => {
    const page = html(h(ReviewBody, { r: served() }));
    expect(page).toContain("[secret registry/example-pass, version 1]");
    expect(page).toContain("team: &quot;&quot;");
    expect(page).toContain(EMPTY_NOTE);
    expect(page).toContain(PROVENANCE_LIMIT);
    expect(page).toContain("doc[0]/machine/registries/auth/password");
    expect(page).toContain(short("frv_BWSYNTH-3jnk4ma"));
    expect(page).not.toContain(WITHHELD);
    expect(page).not.toContain(NO_PROVENANCE);
  });

  test("a withheld review is its own state: no document area, no empty-value note", () => {
    const page = html(h(ReviewBody, { r: served({ configuration: null, notice: "server text that is not echoed", provenance: [] }) }));
    expect(page).toContain(WITHHELD);
    expect(page).not.toContain('data-testid="document"');
    expect(page).not.toContain(EMPTY_NOTE);
    expect(page).not.toContain("server text that is not echoed");
    expect(page).toContain(NO_PROVENANCE);
    expect(page).toContain(PROVENANCE_LIMIT);
  });

  test("a provenance record that was overridden names the revision that overrode it", () => {
    const r = served({ provenance: [{ reference: "app/map", version: 2, member: 1, source: { revision: "frv_BWSYNTH-a", digest: "d", path: "p", occurrence: 3 }, overriddenBy: { revision: "frv_BWSYNTH-b", digest: "e" } }] });
    const page = html(h(ReviewBody, { r }));
    expect(page).toContain("app/map (member 1)");
    expect(page).toContain("overridden by");
    expect(page).toContain(short("frv_BWSYNTH-b"));
  });

  test("the next step comes from the machine's Desired and Applied, and sends nothing", () => {
    const rel = "rel_BWSYNTH-h7rt2bd";
    const pc = false;
    expect(nextStep(rel, { desired: rel, applied: null, pendingConvergence: true })).toContain("Hand it over first (an adopt plan)");
    expect(nextStep(rel, { desired: rel, applied: { release: "rel_BWSYNTH-old", source: "bronzeward" }, pendingConvergence: pc })).toContain("An apply-config plan sends this release to it");
    expect(nextStep(rel, { desired: rel, applied: { release: rel, source: "bronzeward" }, pendingConvergence: pc })).toContain("Nothing is left to send");
    expect(nextStep(rel, { desired: "rel_BWSYNTH-other", applied: null, pendingConvergence: pc })).toContain(`Desired is ${short("rel_BWSYNTH-other")}`);
    expect(nextStep(rel, { desired: null, applied: null, pendingConvergence: pc })).toContain("needs no step");
  });

  test("after an adoption, Desired equal to Applied is not 'nothing left to send' while the machine is pending convergence", () => {
    const rel = "rel_BWSYNTH-h7rt2bd";
    const text = nextStep(rel, { desired: rel, applied: { release: rel, source: "adoption" }, pendingConvergence: true });
    expect(text).toContain("pending convergence");
    expect(text).toContain("An apply-config plan sends this release to it");
    expect(text).not.toContain("Nothing is left to send");
  });

  test("the page reads before it shows anything, and the release page links to it by both identifiers", () => {
    const page = html(h(ReviewItem, { release: "rel_a", machine: "mch_b" }));
    expect(page).toContain("Rendered configuration of mch_b in release rel_a");
    expect(page).toContain("Loading the rendered review");
  });

  test("an identifier is one path segment: a crafted one cannot change which review is read", () => {
    const crafted = "mch_x/../../../rel_b/machines/mch_y";
    const path = reviewPath("rel_a", crafted)!;
    expect(path).toBe("/releases/rel_a/machines/mch_x%2F..%2F..%2F..%2Frel_b%2Fmachines%2Fmch_y");
    expect(new URL(path, "https://bronzeward.test").pathname).toBe(path);
    expect(reviewIds(path)).toEqual(["rel_a", crafted]);
    // Dot-only and unprefixed identifiers make no path at all: a browser would read them as path steps.
    expect(reviewPath("..", "mch_b")).toBeUndefined();
    expect(reviewPath("rel_a", "..")).toBeUndefined();
    expect(reviewPath("rel_a", "mch_b")).toBe("/releases/rel_a/machines/mch_b");
    expect(reviewPath("mch_b", "rel_a")).toBeUndefined();
    expect(reviewIds("/releases/%2E%2E/machines/mch_b")).toBeUndefined();
    expect(reviewIds("/releases/rel_a/machines/%2E")).toBeUndefined();
    expect(reviewIds(reviewPath("rel_a?#", "mch_b&c")!)).toEqual(["rel_a?#", "mch_b&c"]);
    expect(reviewIds("/releases/rel_a/machines/%E0%A4%A")).toBeUndefined();
    expect(reviewIds("/releases/rel_a/machines/mch_b/extra")).toBeUndefined();
    expect(reviewIds("/releases/rel_a")).toBeUndefined();
  });
});
