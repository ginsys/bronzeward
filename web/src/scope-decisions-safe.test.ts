import { createElement as h } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import { marked, released } from "./scope-decisions.tsx";

// The answer of a mark or a release prints the server's scope states: a state with a control or format character, or over
// 300 code points, reads "(not shown)" (client contracts A8 rule 6, ginsys/bronzeward#1055).
describe("the answer of a scope decision prints its states only when they are safe text", () => {
  const act = "act_aaaaaaaaaaaaaaaaaaaaaaaaaa";
  const markText = (state: string, from: string, missing: string[] = []) => renderToStaticMarkup(h("p", null, marked({ state, from, act, missing })));
  const releaseText = (from: string) => renderToStaticMarkup(h("p", null, released({ from, act })));

  test("a safe state and previous state are shown as they are", () => {
    expect(markText("ready", "blocked")).toContain("Scope marked ready, was blocked");
    expect(releaseText("ready")).toContain("Scope released, was ready");
  });
  test.each([
    ["a bidirectional control", "bad‮word"],
    ["more than 300 code points", "x".repeat(301)],
  ])("a mark answer with %s in either state reads (not shown)", (_, bad) => {
    expect(markText(bad, "blocked")).toContain("Scope marked (not shown), was blocked");
    expect(markText("ready", bad)).toContain("Scope marked ready, was (not shown)");
    expect(markText(bad, "blocked")).not.toContain(bad);
  });
  test.each([
    ["a bidirectional control", "bad‮word"],
    ["more than 300 code points", "x".repeat(301)],
  ])("a missing item with %s reads (not shown), and a known one keeps its meaning", (_, bad) => {
    const text = markText("blocked", "blocked", ["observation", bad]);
    expect(text).toContain("Missing observation: ");
    expect(text).toContain("Missing (not shown): ");
    expect(text).not.toContain(bad);
  });
  test.each([
    ["a bidirectional control", "bad‮word"],
    ["more than 300 code points", "x".repeat(301)],
  ])("a release answer with %s as the previous state reads (not shown)", (_, bad) => {
    expect(releaseText(bad)).toContain("Scope released, was (not shown)");
    expect(releaseText(bad)).not.toContain(bad);
  });
});
