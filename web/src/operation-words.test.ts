import { readFileSync } from "node:fs";
import { describe, expect, test } from "vitest";
import { OPERATION_KINDS, OPERATION_STATES } from "./resources.tsx";
import { operationNext, operationNow, startedBy } from "./operation-words.ts";

// What an operation did and what comes next, in words (client contracts A10, ginsys/bronzeward#998): one
// sentence per kind and state the server can produce, the next step an operator can take from it, and the
// starter of an operation the controller created. A kind or state this table does not know is shown raw.

// Every kind and state pair the server can produce (persistence and API 8.2, execution and recovery 3).
const PAIRS: [string, string, string, string | undefined][] = [
  ["publish", "queued", "Accepted; no worker has taken it yet.", "Wait; nothing to do."],
  ["publish", "running", "A worker is compiling the draft and writing the release.", "Wait; nothing to do."],
  ["publish", "succeeded", "The draft became an immutable release. Nothing was sent to any machine.", "Create a plan for each machine (publisher): an adopt plan where it has no Applied configuration or has open drift to take over, which sends nothing; an apply plan otherwise, or to revert open drift, which sends the release."],
  ["publish", "failed", "The draft was not published; no release was made.", "Fix the cause named above and publish again; a publish is safe to repeat."],
  ["ingest", "running", "The import is reading its source into the draft, waits for review, or has stalled with its runner gone.", "Open the import to see which; an author reviews a paused one and takes a stalled one over or abandons it."],
  ["ingest", "succeeded", "The imported configuration is stored in the draft. Nothing was written to any machine.", "Publish the draft (publisher)."],
  ["ingest", "failed", "No configuration was stored from the import's source, and nothing was written to any machine.", "Start the import again (author)."],
  ["apply-config", "committed", "The controller committed the plan and holds the machine; no request is sent yet.", "Wait for the controller."],
  ["apply-config", "sending", "A request is in flight, or its outcome is unknown.", "Wait for the controller."],
  ["apply-config", "verifying", "The node accepted the request; the controller is checking the result.", "Wait for the controller."],
  ["apply-config", "completed", "The machine's Applied configuration is updated.", "None needed."],
  ["apply-config", "rejected", "The machine was not changed.", "Correct the release and create a new plan."],
  ["apply-config", "failed", "The machine may have been changed wrongly or not changed at all; its Applied configuration was not updated.", "Read the machine's drift and observations, then create a new plan."],
  ["apply-config", "cancelled", "No attempt is recorded for it, which is not proof that nothing was sent.", "Read the machine's Observed configuration, then create a new plan if the change is still wanted."],
  ["apply-config", "unresolved", "The controller keeps examining it and may settle it on its own.", undefined],
  ["adopt", "completed", "The machine's running configuration is recorded as Applied, by observation. Nothing was sent.", "None needed; the first apply plan afterwards carries a whole-configuration diff, or says it is withheld."],
];

describe("the sentence and the next step of each kind and state", () => {
  test.each(PAIRS)("%s %s", (kind, state, now, next) => {
    expect(operationNow(kind, state)).toBe(now);
    expect(operationNext(kind, state)).toBe(next);
  });

  test("every kind and state the server lists gives some sentence, never a blank", () => {
    const go = (name: string) => {
      const src = readFileSync(new URL("../../internal/api/operations.go", import.meta.url), "utf8");
      return [...(new RegExp(`${name}\\s*=\\s*\\[\\]string\\{([^}]*)\\}`).exec(src)?.[1] ?? "").matchAll(/"([^"]+)"/g)].map((m) => m[1]!);
    };
    expect(go("operationKinds")).toEqual(OPERATION_KINDS);
    expect(go("operationStates")).toEqual(OPERATION_STATES);
    for (const kind of OPERATION_KINDS) for (const state of OPERATION_STATES) {
      expect(operationNow(kind, state)?.trim(), `${kind} ${state}`).toBeTruthy();
    }
  });

  test("a kind or state it does not know shows its raw value", () => {
    expect(operationNow("frobnicate", "sideways")).toBe("An operation of kind frobnicate in state sideways; this page has no description of it.");
    expect(operationNow("publish", "sideways")).toBe("A publish operation in state sideways; this page has no description of it.");
    expect(operationNow("frobnicate", "running")).toBe("An operation of kind frobnicate in state running; this page has no description of it.");
    expect(operationNext("frobnicate", "sideways")).toBeUndefined();
    expect(operationNext("publish", "sideways")).toBeUndefined();
  });

  // A state or a kind named like a property every object has is unknown, not the property's value.
  test.each(["constructor", "toString", "__proto__", "hasOwnProperty"])("the inherited name %s is an unknown state and an unknown kind", (name) => {
    expect(operationNow("publish", name)).toBe(`A publish operation in state ${name}; this page has no description of it.`);
    expect(operationNow(name, "running")).toBe(`An operation of kind ${name} in state running; this page has no description of it.`);
    expect(operationNext("publish", name)).toBeUndefined();
    expect(operationNext(name, "running")).toBeUndefined();
  });

  test("a sentence repeats no fact the outcome line states for the same state", () => {
    // Outcome says the node refused every attempt as invalid, confirmed by an observation, and that the outcome is unknown.
    expect(operationNow("apply-config", "rejected")).not.toMatch(/refused|invalid/);
    expect(operationNow("apply-config", "completed")).not.toMatch(/confirmed|observation/);
    expect(operationNow("apply-config", "unresolved")).not.toMatch(/not established|unknown/);
  });

  test("a pair the server cannot produce says so with both raw values", () => {
    expect(operationNow("publish", "verifying")).toBe("A publish operation in state verifying; this page has no description of it.");
    expect(operationNow("adopt", "running")).toBe("An adopt operation in state running; this page has no description of it.");
  });
});

describe("who started an operation", () => {
  const who = (principal: string, role: string) => ({ principal, role });

  test("a publish or an import names the principal and role that made the request", () => {
    expect(startedBy("publish", who("usr_a", "publisher"), undefined)).toEqual({ person: who("usr_a", "publisher") });
    expect(startedBy("ingest", who("usr_b", "author"), undefined)).toEqual({ person: who("usr_b", "author") });
  });

  test("an apply or an adoption the controller created names the controller, the plan and the plan's creator", () => {
    expect(startedBy("apply-config", null, { plan: "pln_a", creator: who("usr_c", "publisher") }))
      .toEqual({ controller: "the controller, committing plan", plan: "pln_a", creator: who("usr_c", "publisher") });
    expect(startedBy("adopt", null, { plan: "pln_a", creator: who("usr_c", "publisher") }))
      .toEqual({ controller: "the controller, adopting under plan", plan: "pln_a", creator: who("usr_c", "publisher") });
  });

  test("a plan that could not be read leaves the controller alone, and no plan names the plan as none", () => {
    expect(startedBy("apply-config", null, { plan: "pln_a", creator: undefined })).toEqual({ controller: "the controller, committing plan", plan: "pln_a" });
    expect(startedBy("adopt", null, undefined)).toEqual({ controller: "the controller" });
  });

  test("a creator that is missing on a kind that always has one is shown as none, and an unknown kind shows none", () => {
    expect(startedBy("publish", null, undefined)).toEqual({ none: true });
    expect(startedBy("frobnicate", null, undefined)).toEqual({ none: true });
  });
});
