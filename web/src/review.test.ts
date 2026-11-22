import { createElement as h, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import type { components } from "./api/v1";
import { type Reads, ReadsProvider, short } from "./list.tsx";
import type { Load } from "./list.tsx";
import type { Position } from "./nextstep.tsx";
import { EMPTY_NOTE, NextStepSection, NO_PROVENANCE, positionOwns, PROVENANCE_LIMIT, releaseNote, ReviewBody, ReviewItem, parts, reviewIds, reviewPath, tokenLabel } from "./review.tsx";
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

  test("a redaction token in a provenance path is a badge too, and the rest of the path is kept", () => {
    const path = "doc[0]/machine/registries/config/<redacted>/auth/password";
    const r = served({ provenance: [{ reference: "registry/example-pass", version: 1, member: null, source: { revision: "frv_BWSYNTH-3jnk4ma", digest: "sha256:0", path, occurrence: 0 }, output: path }] });
    const page = html(h(ReviewBody, { r }));
    expect(page).not.toContain("&lt;redacted&gt;/auth");
    expect(page).toContain("registries/config/");
    expect(page).toContain("/auth/password");
    expect(page.match(/class="badge"/g)?.length).toBeGreaterThanOrEqual(2);
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
    expect(releaseNote(rel, { desired: rel, applied: null, pendingConvergence: true })).toContain("Hand it over first (an adopt plan)");
    expect(releaseNote(rel, { desired: rel, applied: { release: "rel_BWSYNTH-old", source: "bronzeward", importBase: null }, pendingConvergence: true })).toContain("An apply-config plan sends this release to it");
    const unchanged = releaseNote(rel, { desired: rel, applied: { release: "rel_BWSYNTH-old", source: "bronzeward", importBase: null }, pendingConvergence: pc });
    expect(unchanged).toContain("carries this machine's Applied configuration");
    expect(unchanged).toContain("Nothing is left to send");
    expect(unchanged).not.toContain("apply-config");
    expect(releaseNote(rel, { desired: rel, applied: { release: rel, source: "bronzeward", importBase: null }, pendingConvergence: pc })).toContain("Nothing is left to send");
    expect(releaseNote(rel, { desired: "rel_BWSYNTH-other", applied: null, pendingConvergence: pc })).toContain(`Desired is ${short("rel_BWSYNTH-other")}`);
    expect(releaseNote(rel, { desired: null, applied: null, pendingConvergence: pc })).toContain("needs no step");
  });

  test("after an adoption, Desired equal to Applied is not 'nothing left to send' while the machine has a release not applied yet", () => {
    const rel = "rel_BWSYNTH-h7rt2bd";
    const text = releaseNote(rel, { desired: rel, applied: { release: rel, source: "adoption", importBase: null }, pendingConvergence: true });
    expect(text).toContain("release not applied yet");
    expect(text).toContain("An apply-config plan sends this release to it");
    expect(text).not.toContain("Nothing is left to send");
  });

  test("the page reads before it shows anything, and the release page links to it by both identifiers", () => {
    const page = html(h(ReviewItem, { release: "rel_a", machine: "mch_b" }));
    expect(page).toContain("Rendered configuration of the machine in release rel_a");
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

// The page's next step, drawn from the machine's position (ginsys/bronzeward#1077, operator experience §2.3).
describe("the next step of the release review page", () => {
  const rel = "rel_BWSYNTH-h7rt2bd";
  const pos = (step: string, over: Record<string, unknown> = {}, machine: Record<string, unknown> = {}): Load<Position> => ({
    kind: "ok",
    data: { machine: { id: "mch_b", cluster: "cl_a", name: null, desired: rel, applied: { release: "rel_BWSYNTH-old" }, pendingConvergence: true, frozen: false, scopeState: "normal", ...machine }, ingestion: null, draft: null, plan: null, operation: null, rolloutHeldBy: null, step, ...over } as unknown as Position,
  });
  const failing: Reads = { ...reads, failure: () => "The read failed." };
  const page = (p: Load<Position>, r: Reads = failing) => renderToStaticMarkup(h(ReadsProvider, { value: r }, h(NextStepSection, { release: rel, machine: "mch_b", pos: p, readAgain: () => {} })));
  const op = { id: "op_1", state: "sending" };

  test("an operation open on the Desired release shows the position's words and its link, no plan button and no word about what was sent", () => {
    const out = page(pos("operation-in-flight", { operation: op }));
    expect(out).toContain("An operation is changing this machine (sending)");
    expect(out).toContain('href="/operations/op_1"');
    expect(out).not.toContain("Plan apply");
    expect(out).not.toMatch(/nothing has been sent/i);
    expect(out).not.toContain("An apply-config plan sends this release");
  });

  test("a plan awaiting approval or dispatch shows its link and no second plan button", () => {
    for (const step of ["awaiting-approval", "awaiting-dispatch"]) {
      const out = page(pos(step, { plan: { id: "pln_1", state: "approved", operation: "apply-config", release: rel, approvalCurrent: true } }));
      expect(out).toContain('href="/plans/pln_1"');
      expect(out).not.toContain("Plan apply");
      expect(out).not.toContain("Plan adopt");
    }
    expect(page(pos("operation-unresolved", { operation: op }))).not.toContain("Plan apply");
  });

  test("an open step on a machine whose Desired is another release leaves this page's sentence and button alone", () => {
    const other = pos("operation-in-flight", { operation: op }, { desired: "rel_BWSYNTH-other" });
    const out = page(other);
    expect(out).toContain("not the Desired release of this machine");
    expect(out).not.toContain("An operation is changing");
    expect(out).not.toContain("Plan apply");
    if (other.kind === "ok") expect(positionOwns(other.data, rel)).toBe(false);
  });

  test("a machine with nothing open keeps the release sentence and the plan button", () => {
    for (const step of ["needs-plan", "no-step"]) {
      const out = page(pos(step));
      expect(out).toContain("An apply-config plan sends this release to it.");
      expect(out).toContain("Plan apply");
      expect(out).not.toMatch(/nothing has been sent yet/i);
      expect(out).not.toContain("An operation is changing");
    }
  });

  test("the page never draws both the position's line and the release sentence", () => {
    for (const step of ["operation-in-flight", "operation-unresolved", "awaiting-approval", "awaiting-dispatch", "needs-plan", "no-step", "drift-open", "not-handed-over"]) {
      const out = page(pos(step, { operation: op, plan: { id: "pln_1", state: "approved", operation: "apply-config", release: rel, approvalCurrent: true } }));
      expect(out.match(/data-testid="next-step"/g)?.length ?? 0, step).toBeLessThanOrEqual(1);
    }
  });

  test("a failed position read shows the failure and a way to read again, and no sentence, line or plan button", () => {
    for (const failed of [{ kind: "unavailable", status: 503, problem: null }, { kind: "transport" }] as Load<Position>[]) {
      const out = page(failed);
      expect(out).toContain("The read failed.");
      expect(out).toContain(">Read again</button>");
      expect(out).not.toContain('data-testid="next-step"');
      expect(out).not.toContain("Plan apply");
      expect(out).not.toContain("Plan adopt");
      expect(out).not.toContain("An apply-config plan sends this release");
    }
  });

  test("a read still loading shows no sentence and no button", () => {
    const out = page({ kind: "loading" });
    expect(out).toContain("Loading");
    expect(out).not.toContain("Read again");
    expect(out).not.toContain("Plan apply");
  });
});
