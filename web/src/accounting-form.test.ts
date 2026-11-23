// @vitest-environment happy-dom
// The accounting form of a scope on the console's row (ginsys/bronzeward#365), mounted through its own reads and a fake client:
// a press sends one POST with the two chosen observations and no typed identifier, a choice that needs an explanation sends nothing
// without one, the answer stays after the scope's next read shows it accounted, and a scope accounted by another operator is no
// longer offered where this page never pressed it. Every value is synthetic.

import { act, createElement as h, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { WritesProvider } from "./forms.tsx";
import { type Reads, ReadsProvider } from "./list.tsx";
import { Client } from "./request.ts";
import { ATTEMPT_WHAT, AccountingForm, accountedAttempt } from "./accounting.tsx";
import { ScopeDecision } from "./scope-decisions.tsx";
import { type Role, ShellContext } from "./shell.tsx";
import { Writes } from "./write.ts";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const MCH = "mch_k2pq3rstuvwxyz234567abcdef";
const CL = "cl_k2pq3rstuvwxyz234567abcdef";
const START = "obs_start3rstuvwxyz234567abcdef";
const END = "obs_end3rstuvwxyz234567abcdef";
const EPOCH = "e1";
const observation = (id: string, basis: number, startedAt: string, at: string, resourceVersion: string | null) => ({
  id, machine: MCH, basis, revision: basis + 1, purpose: "recovery", publicationGeneration: null, plan: null, operation: null, endpoint: "10.5.0.2:50000",
  controller: "ctl_a", startedAt, at, access: null, smbiosUuid: null, talosNodeId: "node-a", talosClusterId: null, assignmentEvidence: null,
  runningVersion: null, resourceVersion, health: null, unread: {},
});
const answerBody = {
  scopes: [{ machine: MCH, state: "blocked", revision: 9, start: START, end: END, attempts: [] }],
  decidedBy: "idn_k2pq3rstuvwxyz234567abcdea", role: "recovery-admin", act: "act_k2pq3rstuvwxyz234567abcdef", epoch: EPOCH, at: "2026-11-01T10:00:00Z",
};

let root: Root;
let box: HTMLElement;
let served: Record<string, unknown>;
let sent: { method: string; path: string; body: unknown }[];
// The reads the fake client was asked for, a path whose answer is held until released, and whether the shell has invalidated every read in flight.
let requested: string[];
let hold: { path: string; release: () => void; gate: Promise<void> } | undefined;
let dropped: boolean;
// Whether the fake server refuses a mark with a 409 that names the scope's state.
let refuseMarks = false;

const reads = (again = 0): Reads => ({
  client: {
    read: async (path: string) => {
      const bare = path.split("?")[0]!;
      requested.push(bare);
      if (hold?.path === bare) await hold.gate;
      if (bare in served) return { kind: "ok", data: served[bare] };
      return { kind: "refused", status: 404, problem: null };
    },
  },
  settle: (res: unknown, set: (r: unknown) => void, live: () => boolean) => {
    if (live()) set(res);
    return false;
  },
  undropped: () => () => !dropped,
  view: "v", again, regained: 0, go: () => {}, failure: () => "FAILED",
}) as unknown as Reads;

const writes = () => {
  const w = new Writes(() => "https://idp.invalid SYNTH-sub-1", async () => {});
  w.client = new Client({
    token: () => "SYNTH-token",
    fetch: (async (url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url).replace("/api/v1", "");
      if (path === "/recovery") return new Response(JSON.stringify({ mode: "normal" }), { status: 200, headers: { "Content-Type": "application/json", "Bronzeward-Epoch": EPOCH } });
      sent.push({ method: init?.method ?? "GET", path, body: init?.body === undefined ? undefined : JSON.parse(String(init.body)) });
      if (refuseMarks && path.endsWith("/marks")) {
        return new Response(JSON.stringify({ type: "about:blank", title: "Conflict", status: 409, detail: "the scope moved", scope: "blocked" }), { status: 409, headers: { "Content-Type": "application/problem+json", "Bronzeward-Epoch": EPOCH } });
      }
      const body = path.endsWith("/marks") ? { state: "blocked", from: "blocked", act: "act_markmarkmarkmarkmark", missing: [] } : answerBody;
      return new Response(JSON.stringify(body), { status: 201, headers: { "Content-Type": "application/json", "Bronzeward-Epoch": EPOCH } });
    }) as typeof fetch,
  });
  return w;
};

// One writes layer for the whole test, as the page has one: a remount with a new one would drop the answer under test.
let layer: Writes;
const mount = (page: ReactNode, roles: Role[] = ["recovery-admin"], again = 0) =>
  act(async () => {
    root.render(h(WritesProvider, { value: layer }, h(ShellContext, { value: { roles, offline: false } }, h(ReadsProvider, { value: reads(again) }, page))));
  });
const reread = () =>
  act(async () => {
    (box.querySelector('form[aria-label="Record accounting"] button[type="button"]') as HTMLButtonElement).click();
    await new Promise((r) => setTimeout(r, 30));
  });
const settled = () => act(async () => void (await new Promise((r) => setTimeout(r, 30))));
const text = (testid: string) => box.querySelector(`[data-testid="${testid}"]`)?.textContent;
const choose = (name: "start" | "end", id: string) =>
  act(async () => {
    (box.querySelector(`input[name="${name}"][value="${id}"]`) as HTMLInputElement).click();
  });
const explain = (value: string) =>
  act(async () => {
    const input = box.querySelector('input[name="explanation"]') as HTMLInputElement;
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
  });
const press = () =>
  act(async () => {
    // A real click on the button, as a person's press is, with the real writes layer behind it.
    (box.querySelector('form[aria-label="Record accounting"] button[type="submit"]') as HTMLButtonElement).click();
    await new Promise((r) => setTimeout(r, 60));
  });

beforeEach(() => {
  box = document.createElement("div");
  document.body.append(box);
  root = createRoot(box);
  layer = writes();
  served = {
    "/server": { settleFloorSeconds: 30, maxTransportDeadlineSeconds: 60 },
    [`/machines/${MCH}`]: { id: MCH, cluster: CL, name: "cp1", scopeState: "pre-restore-unaccounted" },
    [`/clusters/${CL}`]: { id: CL, name: "office" },
    "/principals": { items: [{ id: "idn_k2pq3rstuvwxyz234567abcdea", kind: "human", display: "Ada Synth", subject: "SYNTH-sub-1", issuer: "https://idp.invalid" }] },
    [`/machines/${MCH}/observations`]: {
      items: [observation(START, 1, "2026-11-01T09:00:00Z", "2026-11-01T09:00:01Z", "rv1"), observation(END, 2, "2026-11-01T09:05:00Z", "2026-11-01T09:05:01Z", "rv2")],
      next: undefined,
    },
  };
  sent = [];
  requested = [];
  hold = undefined;
  dropped = false;
  refuseMarks = false;
});
afterEach(async () => {
  await act(async () => root.unmount());
  box.remove();
});

const row = (state: string) => h(ScopeDecision, { machine: MCH, state });

describe("a scope's accounting form on the console's row", () => {
  test("a press sends one POST with the two chosen observations and the explanation, and shows what was recorded", async () => {
    await mount(row("pre-restore-unaccounted"));
    await settled();
    expect(text("settle-limits")).toBe("Settle floor 30 s, maximum transport deadline 1 min, as the server serves them.");
    await choose("start", START);
    await choose("end", END);
    await explain("the operator re-applied the restored configuration");
    await press();
    expect(sent).toEqual([{ method: "POST", path: "/recovery/accountings", body: { scopes: [{ machine: MCH, start: START, end: END }], facts: { explanation: "the operator re-applied the restored configuration" } } }]);
    expect(text("settle-answer")).toMatch(/^Recorded\. Scope \S+ is now blocked until it is marked\./);
    // The machine and the person are named, never a cut identifier.
    await settled();
    expect(text("settle-answer")).toContain("Scope cp1 is now");
    expect(text("settle-answer")).toContain("Recorded by Ada Synth (recovery-admin)");
  });

  test("a moved resource version with no explanation sends nothing", async () => {
    await mount(row("pre-restore-unaccounted"));
    await settled();
    await choose("start", START);
    await choose("end", END);
    await press();
    expect(sent).toEqual([]);
    expect(text("settle-input")).toContain("say why");
  });

  test("the answer stays when the next read shows the scope accounted, and the form is off", async () => {
    await mount(row("pre-restore-unaccounted"));
    await settled();
    await choose("start", START);
    await choose("end", END);
    await explain("the operator re-applied the restored configuration");
    await press();
    await mount(row("blocked"));
    await settled();
    expect(text("settle-answer")).toMatch(/^Recorded\./);
    expect(box.querySelector('form[aria-label="Record accounting"] button[type="submit"]')?.hasAttribute("disabled")).toBe(true);
  });

  test("the accounting's answer leaves the row when the person marks the scope, since what it says the scope is now is stale", async () => {
    await mount(row("pre-restore-unaccounted"));
    await settled();
    await choose("start", START);
    await choose("end", END);
    await explain("the operator re-applied the restored configuration");
    await press();
    await mount(row("blocked"));
    await settled();
    expect(text("settle-answer")).toMatch(/^Recorded\./);
    await act(async () => {
      (Array.from(box.querySelectorAll("button")).find((b) => b.textContent === "Mark") as HTMLButtonElement).click();
      await new Promise((r) => setTimeout(r, 60));
    });
    expect(text("write-result")).toMatch(/^Scope marked blocked, was blocked/);
    expect(text("settle-answer")).toBeUndefined();
  });

  test("a mark the server refuses leaves the accounting's answer in place, since the scope is as it said", async () => {
    refuseMarks = true;
    await mount(row("pre-restore-unaccounted"));
    await settled();
    await choose("start", START);
    await choose("end", END);
    await explain("the operator re-applied the restored configuration");
    await press();
    await mount(row("blocked"));
    await settled();
    await act(async () => {
      (Array.from(box.querySelectorAll("button")).find((b) => b.textContent === "Mark") as HTMLButtonElement).click();
      await new Promise((r) => setTimeout(r, 60));
    });
    expect(text("write-result")).toBeDefined();
    expect(text("settle-answer")).toMatch(/^Recorded\. Scope \S+ is now blocked until it is marked\./);
  });

  test("a person who waits for a later observation reads the machine's observations again and keeps what was typed", async () => {
    const all = (served[`/machines/${MCH}/observations`] as { items: unknown[] }).items;
    served[`/machines/${MCH}/observations`] = { items: all.slice(0, 1), next: undefined };
    await mount(row("pre-restore-unaccounted"));
    await settled();
    await choose("start", START);
    await explain("the operator re-applied the restored configuration");
    expect(box.querySelector(`input[name="end"][value="${END}"]`)).toBeNull();
    served[`/machines/${MCH}/observations`] = { items: all, next: undefined };
    await reread();
    expect(box.querySelector(`input[name="end"][value="${END}"]`)).not.toBeNull();
    expect((box.querySelector(`input[name="start"][value="${START}"]`) as HTMLInputElement).checked).toBe(true);
    expect((box.querySelector('input[name="explanation"]') as HTMLInputElement).value).toBe("the operator re-applied the restored configuration");
  });

  test("the answer stays when a later read of the observations fails", async () => {
    await mount(row("pre-restore-unaccounted"));
    await settled();
    await choose("start", START);
    await choose("end", END);
    await explain("the operator re-applied the restored configuration");
    await press();
    delete served[`/machines/${MCH}/observations`];
    await mount(row("pre-restore-unaccounted"), ["recovery-admin"], 1);
    await settled();
    expect(text("settle-answer")).toMatch(/^Recorded\./);
  });

  test("a scope another operator accounted is not offered where this page never pressed it", async () => {
    await mount(row("pre-restore-unaccounted"));
    await settled();
    expect(box.querySelector('form[aria-label="Record accounting"]')).not.toBeNull();
    await mount(row("blocked"));
    await settled();
    expect(box.querySelector('form[aria-label="Record accounting"]')).toBeNull();
  });
});

// The Start choice where the page can name the deadline (ginsys/bronzeward#365): the one start the server accepts is marked and every
// other observation says why it is off; where it cannot, the choice is left to the person and the server.
const attemptForm = (deadline: { at: number } | { unknown: string }) =>
  h(AccountingForm, { machine: MCH, path: "/operations/op_a/attempts/att_a/accountings", resource: "/operations/op_a", what: ATTEMPT_WHAT, done: accountedAttempt, due: true, deadline });
const startInput = (id: string) => box.querySelector(`input[name="start"][value="${id}"]`) as HTMLInputElement;

describe("the Start choice names the start the server accepts", () => {
  test("the first read after the deadline is marked, and the one before it is off with its reason", async () => {
    await mount(attemptForm({ at: Date.parse("2026-11-01T09:02:00Z") }));
    await settled();
    expect(startInput(START).disabled).toBe(true);
    expect(startInput(START).closest("li")!.textContent).toContain("off: it began at or before the transport deadline");
    expect(startInput(END).disabled).toBe(false);
    expect(startInput(END).closest("li")!.textContent).toContain("(the first read after the deadline)");
  });

  test("no observation after the deadline: the page says it finds no start yet, and offers none", async () => {
    await mount(attemptForm({ at: Date.parse("2026-11-01T09:30:00Z") }));
    await settled();
    expect(text("settle-start-none")).toContain("the page finds no start yet");
    expect(startInput(START).disabled).toBe(true);
    expect(startInput(END).disabled).toBe(true);
  });

  test("a first read after the deadline that read no resource version is not marked, and the page says no start can be recorded", async () => {
    served[`/machines/${MCH}/observations`] = {
      items: [observation(START, 1, "2026-11-01T09:00:00Z", "2026-11-01T09:00:01Z", null), observation(END, 2, "2026-11-01T09:05:00Z", "2026-11-01T09:05:01Z", "rv2")],
      next: undefined,
    };
    await mount(attemptForm({ at: Date.parse("2026-11-01T08:00:00Z") }));
    await settled();
    expect(text("settle-start-refused")).toContain("refuses it without one, so no start can be recorded for this settle");
    expect(startInput(START).disabled).toBe(true);
    expect(startInput(START).closest("li")!.textContent).not.toContain("(the first read after the deadline)");
    expect(startInput(START).closest("li")!.textContent).toContain("off: it read no machine-configuration resource version");
    expect(startInput(END).disabled).toBe(true);
  });

  test("a deadline the page cannot name leaves the choice to the person and says the server decides", async () => {
    await mount(attemptForm({ unknown: "an operation of the machine could not be read." }));
    await settled();
    expect(text("settle-start-unknown")).toBe("The page cannot name the valid start: an operation of the machine could not be read. The server decides.");
    expect(startInput(START).disabled).toBe(false);
    expect(startInput(END).disabled).toBe(false);
  });
});

// A scope's deadline is the later of the entry's stoppedAt plus the maximum transport deadline (30 s floor, 60 s maximum here) and the
// transport deadline of each attempt of the machine that no accounting covers, which takes one read per apply operation of the machine.
describe("the Start choice of a scope reads the machine's unaccounted attempts", () => {
  const STOPPED = "2026-11-01T08:00:00Z";
  const scopeRow = () => h(ScopeDecision, { machine: MCH, state: "pre-restore-unaccounted", stoppedAt: STOPPED });
  const operation = (id: string, deadline: string, accounted: boolean) =>
    ({ id, attempts: [{ id: `att_${id}`, number: 1, transportDeadline: deadline, accounting: accounted ? { basis: "response" } : null }] });

  test("without a later unaccounted attempt the deadline is the stop plus the maximum, and the first read after it is marked", async () => {
    served["/operations"] = { items: [{ id: "op_a" }], next: undefined };
    served["/operations/op_a"] = operation("op_a", "2026-11-01T09:02:00Z", true);
    await mount(scopeRow());
    await settled();
    expect(startInput(START).disabled).toBe(false);
    expect(startInput(START).closest("li")!.textContent).toContain("(the first read after the deadline)");
  });

  test("an unaccounted attempt with a later transport deadline moves the deadline past the first observation", async () => {
    served["/operations"] = { items: [{ id: "op_a" }], next: undefined };
    served["/operations/op_a"] = operation("op_a", "2026-11-01T09:02:00Z", false);
    await mount(scopeRow());
    await settled();
    expect(startInput(START).disabled).toBe(true);
    expect(startInput(END).closest("li")!.textContent).toContain("(the first read after the deadline)");
  });

  test("more apply operations than the page reads: no mark, and the server decides", async () => {
    served["/operations"] = { items: Array.from({ length: 9 }, (_, i) => ({ id: `op_${i}` })), next: undefined };
    await mount(scopeRow());
    await settled();
    expect(text("settle-start-unknown")).toBe("The page cannot name the valid start: the machine has more than 8 apply operations, too many to read here. The server decides.");
    expect(startInput(START).disabled).toBe(false);
  });

  test("the shell's re-read reads the operations again, so the deadline follows the observations' re-read", async () => {
    served["/operations"] = { items: [{ id: "op_a" }], next: undefined };
    served["/operations/op_a"] = operation("op_a", "2026-11-01T09:02:00Z", false);
    await mount(scopeRow());
    await settled();
    expect(startInput(START).disabled).toBe(true);
    served["/operations/op_a"] = operation("op_a", "2026-11-01T09:02:00Z", true);
    await mount(scopeRow(), ["recovery-admin"], 1);
    await settled();
    expect(startInput(START).disabled).toBe(false);
    expect(startInput(START).closest("li")!.textContent).toContain("(the first read after the deadline)");
  });

  test("an operation that cannot be read gives no mark, whatever the others say", async () => {
    served["/operations"] = { items: [{ id: "op_a" }, { id: "op_b" }], next: undefined };
    served["/operations/op_a"] = operation("op_a", "2026-11-01T07:00:00Z", true);
    await mount(scopeRow());
    await settled();
    expect(text("settle-start-unknown")).toBe("The page cannot name the valid start: an operation of the machine could not be read. The server decides.");
    expect(startInput(START).disabled).toBe(false);
  });
});

// A read the effect or the shell has superseded changes nothing and starts no further read.
describe("a superseded read of a scope's operations", () => {
  const STOPPED = "2026-11-01T08:00:00Z";
  const scopeRow = () => h(ScopeDecision, { machine: MCH, state: "pre-restore-unaccounted", stoppedAt: STOPPED });
  const holding = (path: string) => {
    let release = () => {};
    const gate = new Promise<void>((r) => (release = r));
    hold = { path, release, gate };
  };
  const later = () => act(async () => { hold!.release(); await new Promise((r) => setTimeout(r, 30)); });

  test("a read the shell invalidated, failing late, leaves no \"cannot name\" text and starts no further read", async () => {
    served["/operations"] = { items: [{ id: "op_a" }, { id: "op_b" }], next: undefined };
    holding("/operations/op_a");
    await mount(scopeRow());
    await settled();
    dropped = true;
    await later();
    expect(text("settle-start-unknown")).toBeUndefined();
    expect(requested).not.toContain("/operations/op_b");
  });

  test("an unmount during an operation's read starts no further read", async () => {
    served["/operations"] = { items: [{ id: "op_a" }, { id: "op_b" }], next: undefined };
    served["/operations/op_a"] = { id: "op_a", attempts: [] };
    holding("/operations/op_a");
    await mount(scopeRow());
    await settled();
    await act(async () => root.unmount());
    root = createRoot(box);
    await later();
    expect(requested).not.toContain("/operations/op_b");
  });
});
