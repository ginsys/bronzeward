// The recovery page of one operation prints the server's words (an operation's state and kind, the role and state an answer
// records) only as safe text: a word with a control or format character, or over 300 code points, reads "(not shown)"
// (client contracts A8 rule 6, ginsys/bronzeward#1055). Every value is synthetic.

import { createElement as h } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test, vi } from "vitest";
import type { components } from "./api/v1";
import { resolved, resolveWhy, stateLine, takenOver, takeOverWhy } from "./recovery-operation.tsx";

// The principal's name is a read of its own; the answers are tested with its identifier standing in.
vi.mock("./principals.tsx", () => ({ Who: ({ id }: { id: string }) => id }));

type Operation = components["schemas"]["OperationBody"];
type Takeover = components["schemas"]["TakeoverBody"];
type Resolution = components["schemas"]["ResolutionBody"];

const BAD: [string, string][] = [
  ["a bidirectional control", "bad‮word"],
  ["more than 300 code points", "x".repeat(301)],
];
const op = (state: string, kind = "apply-config", attempts = 0): Operation =>
  ({ id: "op_x", kind, state, attempts: Array.from({ length: attempts }, (_, i) => ({ id: `att_${i}`, number: i + 1, generation: 1 })) }) as unknown as Operation;
const html = (node: unknown) => renderToStaticMarkup(h("p", null, node as never));
const takeover = (state: string, role: string) => ({ operation: "op_x", generation: 2, state, requestedBy: "usr_a", role, act: "act_aaaaaaaaaaaaaaaaaaaaaaaaaa" }) as unknown as Takeover;
const resolution = (state: string, role: string) => ({ operation: "op_x", state, resolvedBy: "usr_a", role, act: "act_aaaaaaaaaaaaaaaaaaaaaaaaaa" }) as unknown as Resolution;

describe("the state line", () => {
  test("a safe state is shown as it is", () => {
    expect(stateLine("unresolved")).toBe("State: unresolved.");
  });
  test.each(BAD)("a state with %s reads (not shown), and the raw value is absent", (_, bad) => {
    expect(stateLine(bad)).toBe("State: (not shown).");
    expect(stateLine(bad)).not.toContain(bad);
  });
});

describe("the reasons a control is off", () => {
  test.each(BAD)("a state with %s reads (not shown) in both reasons", (_, bad) => {
    expect(takeOverWhy(op(bad))).toContain("The operation is (not shown):");
    expect(resolveWhy(op(bad))).toContain("The operation is (not shown):");
    expect(takeOverWhy(op(bad))).not.toContain(bad);
    expect(resolveWhy(op(bad))).not.toContain(bad);
  });
  test.each(BAD)("a kind with %s reads (not shown)", (_, bad) => {
    expect(takeOverWhy(op("unresolved", bad))).toContain("The operation is a (not shown)");
    expect(takeOverWhy(op("unresolved", bad))).not.toContain(bad);
  });
  test("a safe kind and state are shown as they are", () => {
    expect(takeOverWhy(op("unresolved", "publish"))).toContain("The operation is a publish");
    expect(takeOverWhy(op("cancelled"))).toContain("The operation is cancelled");
  });
});

describe("the answers print the state and role they record only when they are safe", () => {
  test("safe words are shown as they are", () => {
    expect(html(takenOver(takeover("unresolved", "recovery-admin")))).toContain("state unresolved");
    expect(html(takenOver(takeover("unresolved", "recovery-admin")))).toContain("(recovery-admin)");
    expect(html(resolved(resolution("cancelled", "recovery-admin")))).toContain("the operation is cancelled");
  });
  test.each(BAD)("a take-over answer with %s as state or role reads (not shown)", (_, bad) => {
    const a = html(takenOver(takeover(bad, "recovery-admin")));
    const b = html(takenOver(takeover("unresolved", bad)));
    expect(a).toContain("state (not shown)");
    expect(b).toContain("((not shown))");
    expect(a).not.toContain(bad);
    expect(b).not.toContain(bad);
  });
  test.each(BAD)("a resolve answer with %s as state or role reads (not shown)", (_, bad) => {
    const a = html(resolved(resolution(bad, "recovery-admin")));
    const b = html(resolved(resolution("cancelled", bad)));
    expect(a).toContain("the operation is (not shown)");
    expect(b).toContain("((not shown))");
    expect(a).not.toContain(bad);
    expect(b).not.toContain(bad);
  });
});
