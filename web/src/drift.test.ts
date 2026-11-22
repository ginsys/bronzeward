// @vitest-environment happy-dom
// The drift page (ginsys/bronzeward#363; wireframe W9, journeys §5.8): each state of a machine mounted through its own
// reads, the words it says, the roles each control needs, the refusals a freeze and an unfreeze draw, no identifier as
// link text or first column, and the machine page's one link to it.

import { act, createElement as h, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { driftId, DriftItem } from "./drift.tsx";
import { WritesProvider } from "./forms.tsx";
import { type Reads, ReadsProvider } from "./list.tsx";
import { at, driftLinkText, MachineShown } from "./machine.tsx";
import { nextStep } from "./nextstep.tsx";
import { FROZEN } from "./machine.tsx";
import { PlanItem } from "./resources.tsx";
import { Client } from "./request.ts";
import { type Role, ShellContext } from "./shell.tsx";
import { SCOPE_NO_MODE } from "./states.tsx";
import {
  DECIDE_ON_THE_CHANGE, DRIFT_ADOPT, DRIFT_FREEZE, DRIFT_NONE, DRIFT_NOT_SHOWN, DRIFT_NOTE, DRIFT_NOTE_OPEN, DRIFT_REVERT, FREEZE_THIS_MACHINE, UNFREEZE_OR_DECIDE, UNFREEZE_THE_MACHINE,
} from "./words.ts";
import { PLAN_FROZEN } from "./plan.tsx";
import { Writes } from "./write.ts";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const MCH = "mch_k2pq3rstuvwxyz234567abcdef";
const REL = "rel_k2pq3rstuvwxyz234567abcdef";
const OP = "op_k2pq3rstuvwxyz234567abcdef";
const PLN = "pln_k2pq3rstuvwxyz234567abcdef";
const OPENED = "2026-11-01T09:41:07Z";
const LAST = "2026-11-01T09:46:12Z";
const machine = (over: Record<string, unknown> = {}) => ({
  id: MCH, cluster: "cl_a", name: "cp1", hardware: { smbiosUuid: null, talosNodeId: "node-a", serial: null }, platform: "metal", talosEndpoint: "10.5.0.2:50000",
  desired: REL, applied: { release: REL, source: "operation", importBase: null }, pendingConvergence: false,
  observed: { observation: "obs_m", at: LAST, release: null }, frozen: false, scopeState: "normal",
  openDrift: { revision: 3, observation: "obs_k", lastObservation: "obs_m", lateLanding: null }, ...over,
});
const unchanged = { openDrift: null, observed: { observation: "obs_m", at: LAST, release: REL } };
const observation = (id: string, basis: number, when: string, unread: Record<string, unknown> = {}) => ({
  id, machine: MCH, basis, revision: basis + 1, purpose: "scheduled", publicationGeneration: null, plan: null, operation: null, endpoint: "10.5.0.2:50000",
  controller: "ctl_a", startedAt: when, at: when, access: null, smbiosUuid: null, talosNodeId: "node-a", talosClusterId: null, assignmentEvidence: null,
  runningVersion: null, resourceVersion: null, health: null, unread,
});
const obsK = observation("obs_k", 2, OPENED);
const obsM = observation("obs_m", 4, LAST);

let root: Root;
let box: HTMLElement;
let served: Record<string, unknown>;
// What the next writes answer, in order, and what was sent.
let answers: Response[];
let sent: { method: string; path: string; body: unknown }[];

const reads = (): Reads => ({
  client: {
    read: async (path: string) => {
      const bare = path.split("?")[0]!;
      if (path in served) return { kind: "ok", data: served[path] };
      if (bare in served) return { kind: "ok", data: served[bare] };
      if (bare.endsWith("/observations") || bare.endsWith("/timeline")) return { kind: "ok", data: { items: [], next: undefined } };
      return { kind: "refused", status: 404, problem: null };
    },
  },
  settle: (res: unknown, set: (r: unknown) => void, live: () => boolean) => {
    if (live()) set(res);
    return false;
  },
  undropped: () => () => true,
  view: "v", again: 0, regained: 0, go: () => {}, failure: () => "FAILED",
}) as unknown as Reads;

const EPOCH = "e1";
const problem = (status: number, code: string, extra: Record<string, unknown> = {}) =>
  new Response(JSON.stringify({ type: `urn:bronzeward:problem:${code}`, title: code, status, ...extra }), { status, headers: { "Content-Type": "application/problem+json", "Bronzeward-Epoch": EPOCH } });
const created = (body: unknown) => new Response(JSON.stringify(body), { status: 201, headers: { "Content-Type": "application/json", "Bronzeward-Epoch": EPOCH } });

const writes = () => {
  const w = new Writes(() => "https://idp.invalid SYNTH-sub-1", async () => {});
  w.client = new Client({
    token: () => "SYNTH-token",
    fetch: (async (url: RequestInfo | URL, init?: RequestInit) => {
      const path = String(url).replace("/api/v1", "");
      if (path === "/recovery") return new Response(JSON.stringify({ mode: "normal" }), { status: 200, headers: { "Content-Type": "application/json", "Bronzeward-Epoch": EPOCH } });
      sent.push({ method: init?.method ?? "GET", path, body: init?.body === undefined ? undefined : JSON.parse(String(init.body)) });
      return answers.shift() ?? problem(500, "internal-error");
    }) as typeof fetch,
  });
  return w;
};

const mount = (page: ReactNode, roles: Role[] = ["author", "publisher", "approver"]) =>
  act(async () => {
    root.render(h(WritesProvider, { value: writes() }, h(ShellContext, { value: { roles, offline: false } }, h(ReadsProvider, { value: reads() }, page))));
  });
const settled = () => act(async () => void (await new Promise((r) => setTimeout(r, 30))));
const shown = async (roles?: Role[], id = MCH) => {
  await mount(h(DriftItem, { id }), roles);
  await settled();
};
const text = (testid: string) => box.querySelector(`[data-testid="${testid}"]`)?.textContent;
const links = () => [...box.querySelectorAll("a")];
const linkTo = (href: string) => links().filter((a) => a.getAttribute("href") === href);
const button = (label: string) => [...box.querySelectorAll("button")].find((b) => b.textContent === label);
const sections = () => [...box.querySelectorAll("section")].map((s) => s.getAttribute("aria-label"));
const submit = (label: string, reason: string) =>
  act(async () => {
    const form = box.querySelector(`form[aria-label="${label}"]`)!;
    const input = form.querySelector("input")!;
    const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!;
    set.call(input, reason);
    input.dispatchEvent(new Event("input", { bubbles: true }));
    form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await new Promise((r) => setTimeout(r, 60));
  });

beforeEach(() => {
  box = document.createElement("div");
  document.body.append(box);
  root = createRoot(box);
  served = { [`/machines/${MCH}`]: machine(), [`/machines/${MCH}/observations`]: { items: [obsM, obsK], next: undefined } };
  answers = [];
  sent = [];
});
afterEach(async () => {
  await act(async () => root.unmount());
  box.remove();
});

describe("the path", () => {
  test("names a machine, once, and no other path", () => {
    expect(driftId(`/machines/${MCH}/drift`)).toBe(MCH);
    for (const bad of ["/machines/drift", `/machines/${MCH}`, `/machines/${MCH}/drift/`, "/machines/../drift", "/machines/./drift", `/machines/rel_a/drift`, "/machines/%E0%A4%A/drift", `/machines/${MCH}/x/drift`]) {
      expect(driftId(bad), bad).toBeUndefined();
    }
  });
});

describe("the page's note", () => {
  // The identifier and role-word check of every note's parts is page-notes.test.ts's one list.
  const note = () => box.querySelector('[data-testid="page-note"]')!.textContent!;
  test.each([
    ["an open change", {}, true],
    ["an open change on a frozen machine", { frozen: true }, true],
    ["no change", unchanged, false],
    ["no change on a frozen machine", { ...unchanged, frozen: true }, false],
  ])("%s: the note is true of the page, and the choice sentence shows only where the choices do", async (_n, over, open) => {
    served[`/machines/${MCH}`] = machine(over);
    await shown();
    expect(note()).toBe(`${DRIFT_NOTE}${open ? ` ${DRIFT_NOTE_OPEN}` : ""} Back to Machine cp1.`);
    expect(sections().includes("Adopt")).toBe(open);
    expect(note()).toContain("Nothing on this page is sent to the machine");
    expect(note()).not.toMatch(/decide what to do|changes the machine/);
  });
});

describe("a machine with an open change", () => {
  test("says the change in words: who opened it and last saw it, each with its time, no late landing, and that the change is not shown", async () => {
    await shown();
    expect(text("drift-record")).toBe(`Changed on the machine: a read differed from what Bronzeward last verified (the release it applied). Opened by the read at ${at(OPENED)}, last seen in the read at ${at(LAST)}.`);
    expect(text("drift-late")).toBe("Late landing: no.");
    expect(text("drift-not-shown")).toBe(DRIFT_NOT_SHOWN);
    expect(text("drift-observed")).toBe(`Observed: read at ${at(LAST)}, matches neither Applied nor Desired.`);
  });

  test("a read the page does not hold has no time, and says so; a late landing names its operation; a newer read that could not read is shown", async () => {
    served[`/machines/${MCH}`] = machine({ openDrift: { revision: 3, observation: "obs_gone", lastObservation: "obs_m", lateLanding: OP } });
    served[`/machines/${MCH}/observations`] = { items: [obsM, observation("obs_n", 9, "2026-11-01T09:51:00Z", { configuration: { cause: "node unreachable" } })], next: undefined };
    await shown();
    expect(text("drift-record")).toContain("Opened by a read whose time was not read, last seen in the read at");
    expect(text("drift-late")).toMatch(/^Late landing: yes, the last read may be the late result of /);
    expect(text("drift-unread")).toBe(`The latest read at ${at("2026-11-01T09:51:00Z")} could not read: configuration (node unreachable).`);
  });

  test("shows the note under the title, with one link back to the machine, then the three choices in order, each with its steps", async () => {
    await shown();
    expect(box.querySelector('[data-testid="page-note"]')!.textContent).toBe(`${DRIFT_NOTE} ${DRIFT_NOTE_OPEN} Back to Machine cp1.`);
    expect(linkTo(`/machines/${MCH}`)).toHaveLength(1);
    expect(sections()).toEqual(["Freeze", "Adopt", "Revert"]);
    const said = [...box.querySelectorAll("section > p")].map((p) => p.textContent);
    expect(said).toEqual(expect.arrayContaining([DRIFT_FREEZE, DRIFT_ADOPT, DRIFT_REVERT]));
    expect(box.querySelector("h2")!.textContent).toBe("Change on the machine: Machine cp1");
  });

  test("[Start adoption] is the import wizard as a drift adoption, [Plan revert] the create-plan page with Desired and apply-config", async () => {
    await shown();
    const q = (s: string) => new URLSearchParams(s);
    const adopt = linkTo(`/ingestions/new?${q(`machine=${MCH}&kind=drift-adoption&source=machine`)}`);
    expect(adopt).toHaveLength(1);
    expect(adopt[0]!.textContent).toBe("Start adoption");
    const revert = linkTo(`/plans/new?${q(`release=${REL}&machine=${MCH}&operation=apply-config`)}`);
    expect(revert).toHaveLength(1);
    expect(revert[0]!.textContent).toBe("Plan revert");
    expect(button("Freeze")).toBeDefined();
    expect(button("Unfreeze")).toBeUndefined();
  });

  test("with no Desired release there is nothing to revert to, and no revert link", async () => {
    served[`/machines/${MCH}`] = machine({ desired: null });
    await shown();
    expect(box.textContent).toContain("nothing to revert it to");
    expect(box.querySelector('a[href^="/plans/new"]')).toBeNull();
  });

  test("a frozen machine says so and offers Unfreeze in place of Freeze", async () => {
    served[`/machines/${MCH}`] = machine({ frozen: true });
    await shown();
    expect(text("drift-frozen")).toBe(FROZEN);
    expect(button("Unfreeze")).toBeDefined();
    expect(button("Freeze")).toBeUndefined();
  });
});

describe("a machine with no open change", () => {
  test("says there is none and why, offers Freeze, and no adoption or revert choice", async () => {
    served[`/machines/${MCH}`] = machine(unchanged);
    await shown();
    expect(text("drift-none")).toBe(DRIFT_NONE);
    expect(sections()).toEqual(["Freeze"]);
    expect(button("Freeze")).toBeDefined();
    expect(box.querySelector('a[href^="/ingestions/new"], a[href^="/plans/new"]')).toBeNull();
    expect(box.querySelector('[data-testid="drift-record"]')).toBeNull();
  });

  test("a frozen machine with no open change still reaches Unfreeze, and says it is frozen", async () => {
    served[`/machines/${MCH}`] = machine({ ...unchanged, frozen: true });
    await shown();
    expect(text("drift-none")).toBe(DRIFT_NONE);
    expect(text("drift-frozen")).toBe(FROZEN);
    expect(sections()).toEqual(["Freeze"]);
    expect(button("Unfreeze")).toBeDefined();
    expect(button("Freeze")).toBeUndefined();
  });
});

describe("the roles each control needs", () => {
  test("a control without its role is disabled with the reason, and a link becomes a disabled button with it", async () => {
    served[`/machines/${MCH}`] = machine({ frozen: true });
    await shown(["viewer" as Role]);
    const unfreeze = button("Unfreeze") as HTMLButtonElement;
    expect(unfreeze.disabled).toBe(true);
    expect(unfreeze.parentElement!.textContent).toContain("Needs approver");
    for (const label of ["Start adoption", "Plan revert"]) expect((button(label) as HTMLButtonElement).disabled, label).toBe(true);
    expect(button("Start adoption")!.parentElement!.textContent).toContain("Needs author");
    expect(button("Plan revert")!.parentElement!.textContent).toContain("Needs publisher");
    expect(box.querySelector('a[href^="/ingestions/new"], a[href^="/plans/new"]')).toBeNull();
  });

  test("a publisher can freeze but not unfreeze; an author can adopt but not revert", async () => {
    await shown(["publisher"]);
    expect((button("Freeze") as HTMLButtonElement).disabled).toBe(false);
    expect(box.querySelector('a[href^="/plans/new"]')).not.toBeNull();
    expect(button("Start adoption")).toBeDefined();
    await act(async () => root.unmount());
    root = createRoot(box);
    served[`/machines/${MCH}`] = machine({ frozen: true });
    await shown(["author"]);
    expect((button("Unfreeze") as HTMLButtonElement).disabled).toBe(true);
    expect(box.querySelector('a[href^="/ingestions/new"]')).not.toBeNull();
    expect(button("Plan revert")).toBeDefined();
  });
});

describe("freeze and unfreeze", () => {
  test("freeze sends the reason to the machine's freezes route and says who recorded it", async () => {
    answers = [created({ machine: MCH, revision: 41, frozen: true, principal: "https://idp.invalid SYNTH-sub-1", role: "author", reason: "incident 4711", act: "act_p2dk2pq3rstuvwxyz234567abc", epoch: EPOCH, at: LAST })];
    await shown();
    await submit("Freeze", "incident 4711");
    expect(sent).toEqual([{ method: "POST", path: `/machines/${MCH}/freezes`, body: { reason: "incident 4711" } }]);
    expect(text("freeze-answer")).toMatch(/^Frozen\. Recorded by .* \(author\), act /);
  });

  test("a blank reason is refused on the page and sends nothing", async () => {
    await shown();
    await submit("Freeze", "   ");
    expect(sent).toEqual([]);
    expect(text("freeze-input")).toBe("Give a reason.");
  });

  test("a forced send without the role shows the server's 403 and what the route accepts", async () => {
    served[`/machines/${MCH}`] = machine({ frozen: true });
    answers = [problem(403, "forbidden", { roles: ["approver"] })];
    await shown(["author"]);
    await submit("Unfreeze", "done deciding");
    expect(sent).toEqual([{ method: "POST", path: `/machines/${MCH}/unfreezes`, body: { reason: "done deciding" } }]);
    expect(text("unfreeze-answer")).toContain("Needs approver");
  });

  test("a scope's refusal and the process's refusal are worded differently, and only the scope's says it is about one part", async () => {
    served[`/machines/${MCH}`] = machine({ frozen: true });
    answers = [problem(409, "recovery-mode-active", { scope: MCH, detail: "the scope is pre-restore unaccounted" })];
    await shown();
    await submit("Unfreeze", "x");
    const scope = text("unfreeze-answer")!;
    expect(scope).toContain("This applies to one part of the installation only.");
    expect(scope).toContain(SCOPE_NO_MODE);
    await act(async () => root.unmount());
    root = createRoot(box);
    answers = [problem(409, "recovery-mode-active", { detail: "record the recovery entry first" })];
    await shown();
    await submit("Unfreeze", "x");
    const process = text("unfreeze-answer")!;
    expect(process).not.toContain("one part of the installation");
    expect(process).not.toContain(SCOPE_NO_MODE);
    expect(process).not.toBe(scope);
  });

  test("the answer stays after the machine is read again and the other control replaces the form", async () => {
    answers = [created({ machine: MCH, revision: 41, frozen: true, principal: "https://idp.invalid SYNTH-sub-1", role: "author", reason: "r", act: "act_p2dk2pq3rstuvwxyz234567abc", epoch: EPOCH, at: LAST })];
    await shown();
    served[`/machines/${MCH}`] = machine({ frozen: true });
    await submit("Freeze", "r");
    await settled();
    expect(button("Unfreeze")).toBeDefined();
    expect(text("freeze-answer")).toMatch(/^Frozen\./);
  });

  test("a freeze, an unfreeze and a freeze again in one visit: three writes sent, one answer shown, the newest", async () => {
    const recorded = (frozen: boolean, act: string) => created({ machine: MCH, revision: 41, frozen, principal: "https://idp.invalid SYNTH-sub-1", role: "approver", reason: "r", act, epoch: EPOCH, at: LAST });
    answers = [recorded(true, "act_p2dk2pq3rstuvwxyz234567aaa"), recorded(false, "act_p2dk2pq3rstuvwxyz234567bbb"), recorded(true, "act_p2dk2pq3rstuvwxyz234567ccc")];
    await shown();
    served[`/machines/${MCH}`] = machine({ frozen: true });
    await submit("Freeze", "r");
    await settled();
    expect(text("freeze-answer")).toMatch(/^Frozen\./);
    served[`/machines/${MCH}`] = machine({ frozen: false });
    await submit("Unfreeze", "r");
    await settled();
    // The Freeze form is drawn again and enabled, with the first freeze's answer gone beside the unfreeze's.
    expect(text("unfreeze-answer")).toMatch(/^Unfrozen\./);
    expect(text("freeze-answer")).toBe("");
    expect(box.querySelector('form[aria-label="Freeze"] input')!.hasAttribute("disabled")).toBe(false);
    served[`/machines/${MCH}`] = machine({ frozen: true });
    await submit("Freeze", "r");
    await settled();
    expect(sent.filter((x) => x.method === "POST").map((x) => x.path)).toEqual([`/machines/${MCH}/freezes`, `/machines/${MCH}/unfreezes`, `/machines/${MCH}/freezes`]);
    expect(text("freeze-answer")).toMatch(/^Frozen\./);
    expect(text("freeze-answer")).toContain("ccc");
    expect(text("unfreeze-answer")).toBe("");
  });
});

describe("no identifier is a link's text or a first column", () => {
  const BARE = /\b(mch|obs|rel|pln|op|ing|ibr|drf|cl|act)_[a-z0-9]/;
  test.each([
    ["open change", {}],
    ["late landing", { openDrift: { revision: 3, observation: "obs_k", lastObservation: "obs_m", lateLanding: OP } }],
    ["no change", unchanged],
    ["frozen, no change", { ...unchanged, frozen: true }],
  ])("%s", async (_n, over) => {
    served[`/machines/${MCH}`] = machine(over);
    served[`/operations/${OP}`] = { id: OP, kind: "apply-config", state: "running", subject: { machine: MCH }, createdAt: LAST, createdBy: null, epoch: "e0", lastEvent: null, result: null, error: null, attempts: [] };
    await shown();
    expect(links().length).toBeGreaterThan(0);
    for (const a of links()) expect(a.textContent, a.outerHTML).not.toMatch(BARE);
    for (const c of box.querySelectorAll("tr > :first-child")) expect(c.textContent).not.toMatch(BARE);
    // The identifier is the detail line's, in small type, and in no sentence.
    const outside = [...box.children].filter((c) => !c.contains(box.querySelector("small"))).map((c) => c.textContent).join(" ");
    expect(outside).not.toMatch(BARE);
  });
});

describe("the machine page's one link to it", () => {
  const link = (m: Record<string, unknown>) =>
    h(MachineShown, {
      id: MCH, m: { kind: "ok", data: m }, obs: { kind: "ok", data: { items: [], next: undefined, restarted: false } }, km: { view: "v", data: m, restarted: false },
      ko: { view: "v", data: undefined, restarted: false }, tl: { kind: "loading" }, kt: { view: "v", data: undefined, restarted: false }, older: undefined, busy: false,
    } as never);
  test.each([
    ["a change made on the machine", {}, DECIDE_ON_THE_CHANGE],
    ["a frozen machine with a change", { frozen: true }, UNFREEZE_OR_DECIDE],
    ["a frozen machine with none", { ...unchanged, frozen: true }, UNFREEZE_THE_MACHINE],
    ["neither", unchanged, FREEZE_THIS_MACHINE],
  ])("%s reaches the page with words that follow its state", async (_n, over, words) => {
    const m = machine(over) as Parameters<typeof driftLinkText>[0];
    expect(driftLinkText(m)).toBe(words);
    await mount(link(machine(over)));
    const a = linkTo(`/machines/${MCH}/drift`);
    expect(a).toHaveLength(1);
    expect(a[0]!.textContent).toBe(words);
  });

  test("the next-step line of an open change links to the same page", () => {
    const step = nextStep({ machine: machine(), ingestion: null, draft: null, plan: null, operation: null, rolloutHeldBy: null, step: "drift-open" } as never);
    expect(step?.links).toEqual([{ to: `/machines/${MCH}/drift`, label: DECIDE_ON_THE_CHANGE, id: MCH }]);
  });
});

describe("the plan page says a frozen machine holds an apply-config plan", () => {
  const plan = (state: string, operation = "apply-config") => ({
    id: PLN, state, expiresAt: "2026-11-22T14:02:00Z", machine: MCH, cluster: "cl_a", route: "talos", operation, mode: "no-reboot", release: REL,
    desiredRelease: REL, assignmentRevision: "asr_a", baselineRevision: null, driftRecord: 3, epoch: "e0", transportDeadlineSeconds: 60, verificationDeadlineSeconds: 60,
    maxObservationAgeSeconds: 60, checkValiditySeconds: 60, maxAttempts: null, rolloutLimit: 1, approvalPolicy: "one", approval: null, committedOperation: null,
    createdAt: "2026-11-21T14:02:00Z", createdBy: { principal: "p0", role: "publisher" },
    evidence: { validation: { result: "ok", at: "t", contract: "v1.14", machineryVersion: "1", kubernetesVersion: "1.34", platformMode: "metal" }, dryRun: { available: false, reason: "none" }, baseline: null, diff: null },
  });
  const planPage = async (state: string, frozen: boolean, operation?: string) => {
    served[`/plans/${PLN}`] = plan(state, operation);
    served[`/machines/${MCH}`] = machine({ frozen });
    await mount(h(PlanItem, { id: PLN }));
    await settled();
  };
  test("a proposed revert plan on a frozen machine says it cannot commit, and links to the page that unfreezes", async () => {
    await planPage("proposed", true);
    expect(text("plan-frozen")).toBe(`${PLAN_FROZEN} ${UNFREEZE_THE_MACHINE}.`);
    expect(linkTo(`/machines/${MCH}/drift`)).toHaveLength(1);
  });
  test("an approved plan says it too, from the machine read it already makes", async () => {
    await planPage("approved", true);
    expect(text("plan-frozen")).toContain(PLAN_FROZEN);
  });
  test("an unfrozen machine, an adopt plan and a finished plan say nothing of it", async () => {
    await planPage("proposed", false);
    expect(text("plan-frozen")).toBeUndefined();
    await planPage("proposed", true, "adopt");
    expect(text("plan-frozen")).toBeUndefined();
    await planPage("cancelled", true);
    expect(text("plan-frozen")).toBeUndefined();
  });
});

describe("the drift page's own sentences are the machine page's", () => {
  test("the same lookup draws the opening time on both", async () => {
    await mount(h(MachineShown, {
      id: MCH, m: { kind: "ok", data: machine() }, obs: { kind: "ok", data: { items: [obsM, obsK], next: undefined, restarted: false } }, km: { view: "v", data: machine(), restarted: false },
      ko: { view: "v", data: undefined, restarted: false }, tl: { kind: "loading" }, kt: { view: "v", data: undefined, restarted: false }, older: undefined, busy: false,
    } as never));
    expect(text("sentences")).toContain(`Changed on the machine since ${at(OPENED)}`);
  });
});
