import { createElement as h } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import { referenceText, sentence, visibleDetail } from "./problems.tsx";
import type { Problem } from "./request.ts";
import { safeSentence } from "./safe.ts";
import { Failure } from "./shell.tsx";
import { problemSentences } from "./states.tsx";
import { CUT_SENTENCE } from "./words.ts";

// ginsys/bronzeward#1055 part 7: a refusal's title and detail are shown whole, with each control or format
// character read as U+FFFD, and a text over 1000 code points cut with a phrase that says so.
const BIDI = "‮";
const FFFD = "�";
const problem = (code: string, more: Record<string, unknown> = {}, status = 409): Problem => ({ type: `urn:bronzeward:problem:${code}`, title: `Title of ${code}`, status, ...more });

describe("safeSentence", () => {
  test("a safe sentence reads as received, an empty one stays empty", () => {
    expect(safeSentence("the draft is closed.")).toBe("the draft is closed.");
    expect(safeSentence("")).toBe("");
  });

  test("each control or format character reads U+FFFD in its place and the rest of the sentence stays", () => {
    expect(safeSentence(`the kv dependency a${BIDI}b version 3`)).toBe(`the kv dependency a${FFFD}b version 3`);
    expect(safeSentence("one\u0000two\nthree​four")).toBe(`one${FFFD}two${FFFD}three${FFFD}four`);
  });

  test("1000 code points are shown whole, 1001 are cut and say so, counted by code point", () => {
    expect(safeSentence("a".repeat(1000))).toBe("a".repeat(1000));
    expect(safeSentence("a".repeat(1001))).toBe(`${"a".repeat(1000)}${CUT_SENTENCE}`);
    expect(safeSentence("😀".repeat(1000))).toBe("😀".repeat(1000));
    expect(safeSentence("😀".repeat(1001))).toBe(`${"😀".repeat(1000)}${CUT_SENTENCE}`);
  });
});

describe("the three places a refusal's text is printed", () => {
  const detail = `Publication refused: the kv dependency na${BIDI}me version 3 was recorded under another creation time.`;
  const shown = `Publication refused: the kv dependency na${FFFD}me version 3 was recorded under another creation time.`;

  test("the server's detail in the sentence of a problem (a dependency name an author wrote, with a bidirectional control)", () => {
    const text = problemSentences(problem("stale-input", { detail }), 409);
    expect(text).toContain(` The server says: ${shown}`);
    expect(text).not.toContain(BIDI);
  });

  test("the title and detail in the reference block", () => {
    const text = referenceText(problem("stale-input", { title: `Stale${BIDI}input`, detail }), 409);
    expect(text).toContain(`What the server said: Stale${FFFD}input: ${shown}`);
    expect(text).not.toContain(BIDI);
    expect(referenceText(problem("stale-input", { title: `Stale${BIDI}input` }), 409)).toContain(`What the server said: Stale${FFFD}input`);
  });

  test("the title of a problem whose code the client does not know", () => {
    expect(sentence(problem("a-code-from-the-future", { title: `Odd${BIDI}title` }))).toBe(`Odd${FFFD}title.`);
  });

  test("a 403's detail in the plan refusal", () => {
    const res = { kind: "refused" as const, status: 403, problem: problem("forbidden", { plan: "pln_1", detail: `only the creator${BIDI} may cancel it` }, 403) };
    const html = renderToStaticMarkup(h(Failure, { res, sub: "h-publisher" }));
    expect(html).toContain(`only the creator${FFFD} may cancel it; you are signed in as h-publisher`);
    expect(html).not.toContain(BIDI);
  });

  test("a refusal with no unsafe character reads as before, and the problem itself keeps the raw text", () => {
    const p = problem("stale-input", { detail: "Publication refused: 2 input(s) moved." });
    expect(problemSentences(p, 409)).toContain(" The server says: Publication refused: 2 input(s) moved.");
    const raw = problem("stale-input", { detail });
    expect(visibleDetail(raw)).toBe(shown);
    expect(raw.detail).toBe(detail);
  });
});
