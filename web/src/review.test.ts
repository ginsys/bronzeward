import { createElement as h, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import type { components } from "./api/v1";
import { type Reads, ReadsProvider, short } from "./list.tsx";
import { reviewPath } from "./resources.tsx";
import { EMPTY_NOTE, nextStep, NO_PROVENANCE, PROVENANCE_LIMIT, ReviewBody, ReviewItem, parts, tokenLabel } from "./review.tsx";
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

  test("a token inside a JSON string, written \\u003c…\\u003e, is a badge too", () => {
    const ps = parts('"password":"\\u003credacted:app/json@1\\u003e"');
    expect(ps).toEqual([{ text: '"password":"' }, { token: "\\u003credacted:app/json@1\\u003e", label: "secret app/json, version 1" }, { text: '"' }]);
  });

  test("text that only looks like a token stays text, and an empty value is never a badge", () => {
    for (const text of ["team: \"\"", "note: <redacted", "a: redacted:schema", "x: <redact:value>", ""]) {
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
    expect(nextStep(rel, { desired: rel, applied: null })).toContain("Hand it over first (an adopt plan)");
    expect(nextStep(rel, { desired: rel, applied: { release: "rel_BWSYNTH-old", source: "bronzeward" } })).toContain("An apply-config plan sends this release to it");
    expect(nextStep(rel, { desired: rel, applied: { release: rel, source: "bronzeward" } })).toContain("Nothing is left to send");
    expect(nextStep(rel, { desired: "rel_BWSYNTH-other", applied: null })).toContain(`Desired is ${short("rel_BWSYNTH-other")}`);
    expect(nextStep(rel, { desired: null, applied: null })).toContain("needs no step");
  });

  test("the page reads before it shows anything, and the release page links to it by both identifiers", () => {
    const page = html(h(ReviewItem, { release: "rel_a", machine: "mch_b" }));
    expect(page).toContain("Rendered configuration of mch_b in release rel_a");
    expect(page).toContain("Loading the rendered review");
    expect(reviewPath("rel_a", "mch_b")).toBe("/releases/rel_a/machines/mch_b");
  });
});
