// @vitest-environment happy-dom
// Every refusal in words (client contracts §8.2, ginsys/bronzeward#1049): one sentence per server code, the code,
// rule, scope and request number in a reference block with one copy control, and no sentence that says
// "try again" after a write whose outcome is unknown.

import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { act, createElement as h } from "react";
import { createRoot, type Root } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { COPIED, COPY, CopyButton } from "./copy.tsx";
import { PROBLEM_WORDS, referenceText, RequestRef, RULE_WORDS, wordsFor } from "./problems.tsx";
import type { Problem } from "./request.ts";
import { problemSentences, Refused, SetAside, Unknown } from "./states.tsx";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const problem = (code: string, more: Record<string, unknown> = {}, status = 400): Problem => ({ type: `urn:bronzeward:problem:${code}`, title: `Title of ${code}`, status, ...more });
// The server's codes, read from the Go file that answers them, so a new code fails the table test.
const serverCodes = [...readFileSync(resolve(process.cwd(), "../internal/api/problem.go"), "utf8").matchAll(/^\t"([a-z-]+)":\s+\S.*,$/gm)].map((m) => m[1]!);

describe("the words table", () => {
  test("the test reads all 26 server codes", () => {
    expect(serverCodes).toHaveLength(26);
  });

  test("every server code has one sentence and a next step, and no sentence names a code or an identifier", () => {
    for (const code of serverCodes) {
      const w = PROBLEM_WORDS[code];
      expect(w, code).toBeDefined();
      for (const text of [w!.what, w!.next]) {
        expect(text, code).toMatch(/^[A-Z].*[.]$/);
        expect(text, code).not.toMatch(/req_|[a-z]+-[a-z]+-[a-z]+|epoch|idempotency|Idempotency|If-Match/);
        for (const other of serverCodes) expect(text, `${code} names ${other}`).not.toContain(other);
      }
    }
    expect(Object.keys(PROBLEM_WORDS).sort()).toEqual([...serverCodes].sort());
  });

  test("the import that was not reviewed has one sentence true for both causes, and says nothing needs doing", () => {
    const w = wordsFor(problem("conflict", { rule: "import-not-reviewed" }, 409));
    expect(w).toEqual(RULE_WORDS["conflict/import-not-reviewed"]);
    expect(w?.what).toBe("Only a file import that you continued yourself after its review can be shown here, and this import is not one.");
    expect(w?.next).toBe("Nothing needs doing: the import itself is not affected.");
    expect(wordsFor(problem("forbidden", { rule: "not-the-importer" }, 403))?.next).toBe("Ask that person.");
    expect(wordsFor(problem("conflict", { rule: "some-other-rule" }, 409))).toEqual(PROBLEM_WORDS["conflict"]);
  });

  test("an unknown code has no words, so the caller shows the server's title", () => {
    expect(wordsFor(problem("brand-new-code"))).toBeUndefined();
    expect(problemSentences(problem("brand-new-code", { detail: "Something new." }), 400)).toBe("Title of brand-new-code. The server says: Something new.");
  });
});

describe("the sentences of a refusal", () => {
  test("words, then the server's own detail; the code, rule and request number are not in the text", () => {
    const p = problem("invalid-request", { detail: "name must not be empty", instance: "req_abc", rule: "name" });
    const text = problemSentences(p, 400);
    expect(text).toBe("The request was malformed, so the server did not act on it. Check what you entered and change it before sending again. If nothing looks wrong, report it with the reference below. The server says: name must not be empty.");
    for (const hidden of ["invalid-request", "req_abc", "Title of"]) expect(text).not.toContain(hidden);
  });

  test("a next step that points at the explanation is left out when there is no detail", () => {
    expect(problemSentences(problem("talos-access-unavailable", {}, 503), 503)).toBe("The server could not work with Talos for this machine or cluster.");
    expect(problemSentences(problem("talos-access-unavailable", { detail: "no first key" }, 503), 503)).toBe("The server could not work with Talos for this machine or cluster. The server's explanation follows. The server says: no first key.");
  });

  test("a refused mark names its position, and its detail, which may name a path, appears nowhere", () => {
    const p = problem("validation-failed", { detail: 'path "a/b" is not declared', position: 3, instance: "req_m" }, 422);
    const text = problemSentences(p, 422);
    expect(text).toContain("The mark at position 3 (the first is 0) is refused.");
    expect(text).not.toContain("a/b");
    expect(text).not.toContain("explanation");
    expect(referenceText(p, 422)).not.toContain("a/b");
    expect(renderToStaticMarkup(h(Refused, { problem: p, status: 422, verb: "saved" }))).not.toContain("a/b");
    expect(renderToStaticMarkup(h(RequestRef, { problem: p, status: 422 }))).not.toContain("a/b");
  });

  test("a recovery refusal of one scope keeps the sentence that it says nothing about the start mode, and not the scope's identifier", () => {
    const p = problem("recovery-mode-active", { scope: "scp_one" }, 409);
    const text = problemSentences(p, 409);
    expect(text).toContain("This answer says nothing about the start mode.");
    expect(text).not.toContain("scp_one");
    expect(referenceText(p, 409)).toContain("Scope: scp_one");
  });

  test("an earlier record's request and epoch are in the reference, not in the sentence", () => {
    const p = problem("conflict", { request: "req_old", epoch: "ep_old" }, 409);
    expect(problemSentences(p, 409)).not.toMatch(/req_old|ep_old/);
    expect(referenceText(p, 409)).toContain("Earlier record: request req_old, epoch ep_old");
  });
});

describe("the reference block", () => {
  test("it holds the status, code, rule, scope, request and what the server said, as the text it copies", () => {
    const p = problem("stale-input", { detail: "1 input moved", instance: "req_x", rule: "moved" }, 409);
    expect(referenceText(p, 409)).toBe(["Status: 409", "Code: stale-input", "Rule: moved", "Request: req_x", "What the server said: Title of stale-input: 1 input moved"].join("\n"));
  });

  test("it sits outside the status paragraph, so the mirrored live region does not copy it", () => {
    const p = problem("not-found", { instance: "req_x" }, 404);
    const markup = renderToStaticMarkup(h(Refused, { problem: p, status: 404, verb: "read", testid: "r" }));
    const paragraph = /<p role="alert" data-testid="r">(.*?)<\/p>/.exec(markup)![1]!;
    expect(paragraph).not.toContain("req_x");
    expect(markup).toContain("<details");
    expect(markup).toContain("req_x");
  });
});

describe("a write whose outcome is unknown", () => {
  const p = problem("internal-error", { instance: "req_u" }, 500);
  const props = { onShow: () => {}, problem: p };
  test("says the outcome is not known and how to check, never to try again", () => {
    for (const markup of [renderToStaticMarkup(h(Unknown, { ...props, why: "the server failed" })), renderToStaticMarkup(h(SetAside, { ...props, why: "it was set aside" }))]) {
      expect(markup).not.toMatch(/try again|report it with/i);
      expect(markup).toContain("Check the current state before you do it again.");
      expect(markup).toContain("req_u");
    }
  });
});

describe("the copy control", () => {
  let root: Root;
  let box: HTMLElement;
  beforeEach(() => {
    box = document.createElement("div");
    document.body.append(box);
    root = createRoot(box);
  });
  afterEach(async () => {
    await act(async () => root.unmount());
    box.remove();
    vi.unstubAllGlobals();
  });
  const press = () => act(async () => void box.querySelector("button")!.click());

  test("it copies the exact text and says it did", async () => {
    const writeText = vi.fn(() => Promise.resolve());
    vi.stubGlobal("navigator", { clipboard: { writeText } });
    await act(async () => root.render(h(CopyButton, { text: "Code: x", byHand: "By hand." })));
    expect(box.querySelector("button")!.textContent).toBe(COPY);
    await press();
    expect(writeText).toHaveBeenCalledWith("Code: x");
    expect(box.textContent).toContain(COPIED);
  });

  test("with no clipboard, or a refused write, it says to select by hand", async () => {
    vi.stubGlobal("navigator", {});
    await act(async () => root.render(h(CopyButton, { text: "Code: x", byHand: "Select the text above to copy it." })));
    await press();
    expect(box.textContent).toContain("Select the text above to copy it.");
    vi.stubGlobal("navigator", { clipboard: { writeText: () => Promise.reject(new Error("denied")) } });
    await act(async () => root.render(h(CopyButton, { text: "Code: y", byHand: "By hand." })));
    await press();
    expect(box.textContent).toContain("By hand.");
  });

  test("a reference that changes under the page leaves no \"Copied.\" for text the clipboard does not hold", async () => {
    const writeText = vi.fn(() => Promise.resolve());
    vi.stubGlobal("navigator", { clipboard: { writeText } });
    await act(async () => root.render(h(CopyButton, { text: "Request: req_old", byHand: "By hand." })));
    await press();
    expect(box.textContent).toContain(COPIED);
    await act(async () => root.render(h(CopyButton, { text: "Request: req_new", byHand: "By hand." })));
    expect(box.textContent).not.toContain(COPIED);
    await press();
    expect(writeText).toHaveBeenLastCalledWith("Request: req_new");
    expect(box.textContent).toContain(COPIED);
  });

  test("a slow copy of the old text cannot say it copied the new text", async () => {
    let finish: () => void = () => {};
    vi.stubGlobal("navigator", { clipboard: { writeText: () => new Promise<void>((r) => (finish = r)) } });
    await act(async () => root.render(h(CopyButton, { text: "Request: req_old", byHand: "By hand." })));
    await press();
    await act(async () => root.render(h(CopyButton, { text: "Request: req_new", byHand: "By hand." })));
    await act(async () => finish());
    expect(box.textContent).not.toContain(COPIED);
  });

  test("an older copy that fails after a newer one succeeded does not hide the newer one's line", async () => {
    let refuse: () => void = () => {};
    const writes = [new Promise<void>((_, reject) => (refuse = () => reject(new Error("denied")))), Promise.resolve()];
    vi.stubGlobal("navigator", { clipboard: { writeText: () => writes.shift()! } });
    await act(async () => root.render(h(CopyButton, { text: "Request: req_a", byHand: "By hand." })));
    await press();
    await act(async () => root.render(h(CopyButton, { text: "Request: req_b", byHand: "By hand." })));
    await press();
    expect(box.textContent).toContain(COPIED);
    await act(async () => refuse());
    expect(box.textContent).toContain(COPIED);
    expect(box.textContent).not.toContain("By hand.");
  });

  test("an older copy that succeeds after a newer one leaves the clipboard holding the older text, and the line follows the clipboard", async () => {
    let land: () => void = () => {};
    const writes = [new Promise<void>((resolve) => (land = resolve)), Promise.resolve()];
    vi.stubGlobal("navigator", { clipboard: { writeText: () => writes.shift()! } });
    await act(async () => root.render(h(CopyButton, { text: "Request: req_a", byHand: "By hand." })));
    await press();
    await act(async () => root.render(h(CopyButton, { text: "Request: req_b", byHand: "By hand." })));
    await press();
    expect(box.textContent).toContain(COPIED);
    await act(async () => land());
    // The clipboard now holds req_a: no "Copied." beside req_b, and one beside req_a.
    expect(box.textContent).not.toContain(COPIED);
    await act(async () => root.render(h(CopyButton, { text: "Request: req_a", byHand: "By hand." })));
    expect(box.textContent).toContain(COPIED);
  });

  test("the reference block passes its own by-hand sentence", async () => {
    vi.stubGlobal("navigator", {});
    await act(async () => root.render(h(RequestRef, { problem: problem("not-found", { instance: "req_x" }, 404), status: 404 })));
    await press();
    expect(box.textContent).toContain("Select the text above to copy it.");
    expect(box.textContent).not.toContain("Select the identifier");
  });
});
