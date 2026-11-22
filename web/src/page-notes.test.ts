// @vitest-environment happy-dom
// The sentences each path page shows under its title (ginsys/bronzeward#1055): each page is mounted with its own reads,
// and the test asserts the exact text, that it sits under the title before the page's content, that a part is shown only
// where its control is, and that a link in a note is the only one of its text and target.

import { act, createElement as h, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { DraftItem, settledFragment } from "./drafts.tsx";
import { WritesProvider } from "./forms.tsx";
import { HANDOVER_TITLE } from "./handover.ts";
import { type Reads, ReadsProvider } from "./list.tsx";
import { MachineItem, MachineShown } from "./machine.tsx";
import { PlanNew } from "./plan.tsx";
import { PublishDraft, SENT_NOTHING } from "./publish.tsx";
import { OperationItem, Operations, PlanItem, Plans, ReleaseItem } from "./resources.tsx";
import { ReviewItem } from "./review.tsx";
import { ShellContext } from "./shell.tsx";
import {
  CREATE_A_DRAFT, CREATE_PLAN_NOTE, DRAFT_NOTE, DRAFT_NOTE_OPEN, DRAFT_NOTE_START, MACHINE_NOTE, OPEN_A_MACHINE, OPERATION_NOTE, OPERATIONS_NOTE, PLAN_NOTE,
  PLAN_NOTE_APPROVE, PLANS_NOTE, PLANS_NOTE_END, PUBLISH_NOTE, RELEASE_NOTE, REVIEW_NOTE,
} from "./words.ts";
import { Writes } from "./write.ts";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const DRF = "drf_k2pq3rstuvwxyz234567abcdef";
const MCH = "mch_b";
const REL = "rel_a";
const PLN = "pln_k2pq3rstuvwxyz234567abcdef";
const OP = "op_k2pq3rstuvwxyz234567abcdef";
const machine = {
  id: MCH, cluster: "cl_a", name: null, hardware: { smbiosUuid: null, talosNodeId: "node-a", serial: null }, platform: "metal", talosEndpoint: "10.5.0.2:50000",
  desired: REL, applied: { release: "rel_old", source: "operation", importBase: null }, pendingConvergence: true,
  observed: null, frozen: false, scopeState: "normal", openDrift: null,
};
const draft = (state: string, entries: unknown[]) => ({ id: DRF, title: "onboard office", state, revision: 3, cluster: "cl_a", entries });
const imported = { kind: "import-base", machine: MCH, revision: "ibr_a" };
const release = {
  id: REL, cluster: "cl_a", publishedAt: "2026-11-21T14:02:00Z", publishedBy: null, draft: DRF, draftRevision: 3, operation: OP,
  renderer: { contract: "v1.14", machineryVersion: "1", kubernetesVersion: "1.34" }, sources: [], machines: [],
};
const plan = (state: string) => ({
  id: PLN, state, expiresAt: "2026-11-22T14:02:00Z", machine: MCH, cluster: "cl_a", route: "talos", operation: "apply-config", mode: "no-reboot", release: REL,
  desiredRelease: REL, assignmentRevision: "asr_a", baselineRevision: null, driftRecord: null, epoch: "e0", transportDeadlineSeconds: 60, verificationDeadlineSeconds: 60,
  maxObservationAgeSeconds: 60, checkValiditySeconds: 60, maxAttempts: null, rolloutLimit: 1, approvalPolicy: "one", approval: null, committedOperation: null,
  createdAt: "2026-11-21T14:02:00Z", createdBy: { principal: "p0", role: "publisher" },
  evidence: { validation: { result: "ok", at: "t", contract: "v1.14", machineryVersion: "1", kubernetesVersion: "1.34", platformMode: "metal" }, dryRun: { available: false, reason: "none" }, baseline: null, diff: null },
});
const operation = {
  id: OP, kind: "apply-config", state: "running", subject: { machine: MCH }, createdAt: "2026-11-21T14:02:00Z", createdBy: null, epoch: "e0", lastEvent: null,
  result: null, error: null, attempts: [],
};

let root: Root;
let box: HTMLElement;
// What each path answers; a list path answers an empty list, any other path is not found.
let served: Record<string, unknown>;

const reads = (): Reads => ({
  client: {
    read: async (path: string) => {
      if (held) return new Promise(() => {});
      const bare = path.split("?")[0]!;
      if (path in served) return { kind: "ok", data: served[path] };
      if (bare in served) return { kind: "ok", data: served[bare] };
      if (["/plans", "/operations", "/drafts", "/releases"].includes(bare) || bare.endsWith("/events") || bare.endsWith("/observations") || bare.endsWith("/timeline")) return { kind: "ok", data: { items: [], next: undefined } };
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

type Roles = ("author" | "publisher" | "approver")[];
const mount = (page: ReactNode, roles: Roles = ["author", "publisher", "approver"]) =>
  act(async () => {
    root.render(h(WritesProvider, { value: new Writes(() => "p") }, h(ShellContext, { value: { roles, offline: false } }, h(ReadsProvider, { value: reads() }, page))));
  });
const settled = () => act(async () => void (await new Promise((r) => setTimeout(r, 30))));
const shown = async (page: ReactNode, roles?: Roles) => {
  await mount(page, roles);
  await settled();
};
const notes = () => [...box.querySelectorAll('[data-testid="page-note"]')];
const note = () => {
  expect(notes()).toHaveLength(1);
  return notes()[0]!;
};
// The note follows the page's heading, and nothing but the title's own lines comes before it: the heading, a draft's state
// line, the identifier line and any line the caller names. Any other element before the note (a paragraph, a label, an
// input, a button, a section, a form) fails the check.
const underTheTitle = (n: Element, ...also: (Element | null | undefined)[]) => {
  const h2 = box.querySelector("h2")!;
  expect(h2.compareDocumentPosition(n) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  const title = [h2, ...box.querySelectorAll('[data-testid="draft-line"]'), ...[...box.querySelectorAll("small")].map((s) => s.parentElement), ...also];
  const all = [...box.querySelectorAll("*")];
  const stray = all.slice(0, all.indexOf(n)).filter((e) => !e.contains(n) && !title.some((t) => t === e || t?.contains(e)));
  expect(stray.map((e) => e.outerHTML.slice(0, 80))).toEqual([]);
};
// Reads that never answer, for the loading state.
let held = false;
const linksTo = (href: string) => [...box.querySelectorAll("a")].filter((a) => a.getAttribute("href") === href);
const button = (label: string) => [...box.querySelectorAll("button")].find((b) => b.textContent === label);

beforeEach(() => {
  box = document.createElement("div");
  document.body.append(box);
  root = createRoot(box);
  served = {};
  held = false;
});
afterEach(async () => {
  await act(async () => root.unmount());
  box.remove();
});

describe("the machine page's note", () => {
  test("says where the machine stands, and its link opens the Create draft page, once", async () => {
    await mount(h(MachineShown, {
      id: MCH, m: { kind: "ok", data: machine }, obs: { kind: "ok", data: { items: [], next: undefined, restarted: false } }, km: { view: "v", data: machine, restarted: false },
      ko: { view: "v", data: undefined, restarted: false }, tl: { kind: "loading" }, kt: { view: "v", data: undefined, restarted: false }, older: undefined, busy: false,
    } as never));
    const n = note();
    expect(n.textContent).toBe(`${MACHINE_NOTE} ${CREATE_A_DRAFT}.`);
    underTheTitle(n);
    expect(linksTo("/drafts/new")).toHaveLength(1);
    expect(n.querySelector("a")!.getAttribute("href")).toBe("/drafts/new");
  });
});

describe("the machine page through its own reads", () => {
  test("draws no note while the machine read is pending, and the note under the title once it answers", async () => {
    served[`/machines/${MCH}`] = machine;
    served[`/machines/${MCH}/position`] = { machine, ingestion: null, draft: null, plan: null, operation: null, rolloutHeldBy: null, step: "needs-plan" };
    held = true;
    await shown(h(MachineItem, { id: MCH }));
    expect(notes()).toHaveLength(0);
    held = false;
    await shown(h(MachineItem, { id: MCH, key: "answered" }));
    const n = note();
    expect(n.textContent).toBe(`${MACHINE_NOTE} ${CREATE_A_DRAFT}.`);
    underTheTitle(n);
  });
});

describe("the draft page's note, a part only where its control shows", () => {
  const parts = (...p: string[]) => p.join(" ");
  test("an open draft with a machine's import: all three parts, and the Start managing section they point at", async () => {
    served[`/drafts/${DRF}`] = draft("open", [imported]);
    await shown(h(DraftItem, { id: DRF }));
    const n = note();
    expect(n.textContent).toBe(parts(DRAFT_NOTE, DRAFT_NOTE_OPEN, DRAFT_NOTE_START));
    underTheTitle(n);
    expect(DRAFT_NOTE_START).toContain(HANDOVER_TITLE);
    expect(button(HANDOVER_TITLE)).toBeDefined();
    expect(button("Add fragment")).toBeDefined();
    expect(linksTo(`/publications/new?draft=${DRF}`)).toHaveLength(1);
  });
  test("after a write the page reads again with the last answer kept: the note stays under the title, above the read-again control", async () => {
    served[`/drafts/${DRF}`] = draft("open", [imported]);
    await shown(h(DraftItem, { id: DRF }));
    held = true;
    await act(async () => settledFragment(DRF, () => undefined)({ kind: "done", status: 200, data: {}, replayed: false } as never, {} as never));
    await settled();
    expect(button("Read the draft again")).toBeDefined();
    expect(note().textContent).toBe(parts(DRAFT_NOTE, DRAFT_NOTE_OPEN, DRAFT_NOTE_START));
    underTheTitle(note());
  });
  test("an open draft with no import: no Start part, and no Start managing section", async () => {
    served[`/drafts/${DRF}`] = draft("open", []);
    await shown(h(DraftItem, { id: DRF }));
    expect(note().textContent).toBe(parts(DRAFT_NOTE, DRAFT_NOTE_OPEN));
    expect(button(HANDOVER_TITLE)).toBeUndefined();
  });
  test("a published draft with an import: the Start part, and none of the open part (no Add, Publish or Discard is shown)", async () => {
    served[`/drafts/${DRF}`] = draft("published", [imported]);
    await shown(h(DraftItem, { id: DRF }));
    expect(note().textContent).toBe(parts(DRAFT_NOTE, DRAFT_NOTE_START));
    expect(button(HANDOVER_TITLE)).toBeDefined();
    expect(button("Add fragment")).toBeUndefined();
    expect(button("Discard draft")).toBeUndefined();
    expect(linksTo(`/publications/new?draft=${DRF}`)).toHaveLength(0);
  });
  test("a discarded draft with an import: the always part alone", async () => {
    served[`/drafts/${DRF}`] = draft("discarded", [imported]);
    await shown(h(DraftItem, { id: DRF }));
    expect(note().textContent).toBe(DRAFT_NOTE);
    expect(button(HANDOVER_TITLE)).toBeUndefined();
  });
  test("while the read is pending there is no note, since no entry is below yet; the note appears with the entries once it answers", async () => {
    served[`/drafts/${DRF}`] = draft("open", [imported]);
    held = true;
    await shown(h(DraftItem, { id: DRF }));
    expect(box.querySelector("h2")).not.toBeNull();
    expect(notes()).toHaveLength(0);
    held = false;
    await shown(h(DraftItem, { id: DRF, key: "answered" }));
    underTheTitle(note());
  });
});

describe("the publish page's note", () => {
  test("an open draft shows the note once, with the sentence that nothing has been sent, in place of the old paragraph", async () => {
    served[`/drafts/${DRF}`] = draft("open", [imported]);
    await shown(h(PublishDraft, { query: `draft=${DRF}` }));
    const n = note();
    expect(n.textContent).toBe(`${PUBLISH_NOTE} ${SENT_NOTHING}`);
    expect(box.textContent!.split(SENT_NOTHING)).toHaveLength(2);
    underTheTitle(n);
    expect(linksTo(`/drafts/${DRF}`)).toHaveLength(1);
  });
  test("a draft that is not open shows its own refusal line and no note", async () => {
    served[`/drafts/${DRF}`] = draft("published", [imported]);
    await shown(h(PublishDraft, { query: `draft=${DRF}` }));
    expect(notes()).toHaveLength(0);
    expect(box.querySelector('[data-testid="not-open"]')!.textContent).toContain("not open");
  });
});

describe("the release page's note", () => {
  test("is drawn with the body, under the title, once the release is read", async () => {
    served[`/releases/${REL}`] = release;
    await shown(h(ReleaseItem, { id: REL }));
    const n = note();
    expect(n.textContent).toBe(RELEASE_NOTE);
    underTheTitle(n);
  });
  test("names the Rendered configuration link of each covered machine, which the page draws, and is not drawn while the read is pending", async () => {
    served[`/releases/${REL}`] = { ...release, machines: [{ machine: MCH, mode: "no-reboot", publicationObservation: null }] };
    held = true;
    await shown(h(ReleaseItem, { id: REL }));
    expect(notes()).toHaveLength(0);
    held = false;
    await shown(h(ReleaseItem, { id: REL, key: "answered" }));
    expect(RELEASE_NOTE).toContain("Rendered configuration");
    const links = [...box.querySelectorAll("a")].filter((a) => a.textContent!.startsWith("Rendered configuration of"));
    expect(links).toHaveLength(1);
    expect(links[0]!.getAttribute("href")).toBe(`/releases/${REL}/machines/${MCH}`);
    underTheTitle(note());
  });
});

describe("the rendered configuration page's note", () => {
  const review = (configuration: string | null) => ({ release: REL, machine: MCH, mode: "metal", configuration, provenance: [] });
  test("is drawn where the configuration is drawn, with the page's Back to the release link kept once and no link of its own", async () => {
    served[`/releases/${REL}/machines/${MCH}/review`] = review("a: b");
    served[`/machines/${MCH}/position`] = { machine, ingestion: null, draft: null, plan: null, operation: null, rolloutHeldBy: null, step: "needs-plan" };
    await shown(h(ReviewItem, { release: REL, machine: MCH }));
    const n = note();
    expect(n.textContent).toBe(REVIEW_NOTE);
    underTheTitle(n, [...box.querySelectorAll("a")].find((a) => a.textContent === "Back to the release")?.parentElement);
    expect(box.querySelector('[data-testid="document"]')).not.toBeNull();
    expect([...box.querySelectorAll("a")].filter((a) => a.textContent === "Back to the release")).toHaveLength(1);
    expect(n.querySelector("a")).toBeNull();
  });
  test("is not drawn where the configuration is withheld", async () => {
    served[`/releases/${REL}/machines/${MCH}/review`] = review(null);
    served[`/machines/${MCH}/position`] = { machine, ingestion: null, draft: null, plan: null, operation: null, rolloutHeldBy: null, step: "needs-plan" };
    await shown(h(ReviewItem, { release: REL, machine: MCH }));
    expect(notes()).toHaveLength(0);
    expect(box.querySelector('[data-testid="document"]')).toBeNull();
  });
});

describe("the plans list's note", () => {
  test("says what a plan is, and links to the Machines list with words the navigation does not use", async () => {
    await shown(h(Plans, { query: "", go: () => {} }));
    const n = note();
    expect(n.textContent).toBe(`${PLANS_NOTE} ${OPEN_A_MACHINE} ${PLANS_NOTE_END}`);
    expect(PLANS_NOTE).not.toMatch(/\bevery\b/);
    underTheTitle(n);
    const link = n.querySelector("a")!;
    expect(link.getAttribute("href")).toBe("/machines");
    expect(link.textContent).toBe(OPEN_A_MACHINE);
    expect(linksTo("/machines")).toHaveLength(1);
  });
  test("is the same under a filter, since it says nothing about which plans are listed", async () => {
    await shown(h(Plans, { query: `machine=${MCH}`, go: () => {} }));
    expect(note().textContent).toBe(`${PLANS_NOTE} ${OPEN_A_MACHINE} ${PLANS_NOTE_END}`);
  });
});

describe("the create-a-plan page's note", () => {
  test("is shown with the form, in the form's own words", async () => {
    await shown(h(PlanNew, { query: `release=${REL}&machine=${MCH}` }));
    const n = note();
    expect(n.textContent).toBe(CREATE_PLAN_NOTE);
    underTheTitle(n);
    const options = [...box.querySelectorAll("option")].map((o) => o.textContent!);
    expect(options.some((o) => o.startsWith("apply-config"))).toBe(true);
    expect(options.some((o) => o.startsWith("adopt"))).toBe(true);
  });
  test("is not shown where no release and machine are named", async () => {
    await shown(h(PlanNew, { query: "" }));
    expect(notes()).toHaveLength(0);
    expect(box.querySelector('[data-testid="no-target"]')).not.toBeNull();
  });
});

describe("the plan page's note", () => {
  test("a proposed plan: the always part under the title, and the approval part inside the approval form, once", async () => {
    served[`/plans/${PLN}`] = plan("proposed");
    await shown(h(PlanItem, { id: PLN }));
    const all = notes().map((n) => n.textContent);
    expect(all).toEqual([PLAN_NOTE, PLAN_NOTE_APPROVE]);
    underTheTitle(notes()[0]!);
    expect(box.querySelector('section[aria-label="Approval"]')!.contains(notes()[1]!)).toBe(true);
    expect(PLAN_NOTE_APPROVE).toContain("plan ID");
    expect(box.textContent).toContain("To approve, type the plan ID (");
    expect(linksTo(`/operations?plan=${PLN}`)).toHaveLength(1);
  });
  describe("an approved plan, whose form shows only for an approval from an earlier recovery entry", () => {
    const approved = { ...plan("approved"), approval: "apr_a" };
    const approval = (epoch: string) => ({ id: "apr_a", plan: PLN, approver: { principal: "p", role: "approver" }, epoch, selfApproval: { marked: false, reasons: [] }, unseenOverwrite: false });
    const formShown = () => box.querySelector('section[aria-label="Approval"]') !== null;
    test("an approval from an earlier epoch: the form and the approval part appear together", async () => {
      served[`/plans/${PLN}`] = approved;
      served["/approvals/apr_a"] = approval("ep_before");
      served["/identity"] = { principal: "p", epoch: "ep_now" };
      await shown(h(PlanItem, { id: PLN }));
      expect(formShown()).toBe(true);
      expect(notes().map((n) => n.textContent)).toEqual([PLAN_NOTE, PLAN_NOTE_APPROVE]);
    });
    test("an approval from the current epoch: no form and no approval part", async () => {
      served[`/plans/${PLN}`] = approved;
      served["/approvals/apr_a"] = approval("ep_now");
      served["/identity"] = { principal: "p", epoch: "ep_now" };
      await shown(h(PlanItem, { id: PLN }));
      expect(formShown()).toBe(false);
      expect(notes().map((n) => n.textContent)).toEqual([PLAN_NOTE]);
    });
    test("an approval or identity read that did not answer: no form and no approval part, until both answer", async () => {
      served[`/plans/${PLN}`] = approved;
      served["/identity"] = { principal: "p", epoch: "ep_now" };
      await shown(h(PlanItem, { id: PLN }));
      expect(formShown()).toBe(false);
      expect(notes().map((n) => n.textContent)).toEqual([PLAN_NOTE]);
      served["/approvals/apr_a"] = approval("ep_before");
      await shown(h(PlanItem, { id: PLN, key: "answered" }));
      expect(formShown()).toBe(true);
      expect(notes().map((n) => n.textContent)).toEqual([PLAN_NOTE, PLAN_NOTE_APPROVE]);
    });
  });
  test("a cancelled plan shows no approval form and so only the always part", async () => {
    served[`/plans/${PLN}`] = plan("cancelled");
    await shown(h(PlanItem, { id: PLN }));
    expect(notes().map((n) => n.textContent)).toEqual([PLAN_NOTE]);
    expect(box.querySelector('section[aria-label="Approval"]')).toBeNull();
  });
});

describe("the operations pages' notes", () => {
  test("the list says what an operation is, naming the four kinds, without claiming to list every one", async () => {
    await shown(h(Operations, { query: "", go: () => {} }));
    const n = note();
    expect(n.textContent).toBe(OPERATIONS_NOTE);
    underTheTitle(n);
    for (const word of ["publishing", "importing", "applying", "adopting"]) expect(OPERATIONS_NOTE).toContain(word);
    expect(OPERATIONS_NOTE).not.toMatch(/\bevery\b|lists them/);
  });
  test("an operation's page draws its note with the body", async () => {
    served[`/operations/${OP}`] = operation;
    await shown(h(OperationItem, { id: OP }));
    const n = note();
    expect(n.textContent).toBe(OPERATION_NOTE);
    underTheTitle(n);
  });
  test("an operation's page draws none while its read is pending", async () => {
    held = true;
    await shown(h(OperationItem, { id: OP }));
    expect(box.querySelector("h2")).not.toBeNull();
    expect(notes()).toHaveLength(0);
  });
});

describe("no note carries an identifier or a table reference", () => {
  test("the sentences hold no identifier string, no role word and no table cite", () => {
    const all = [DRAFT_NOTE, DRAFT_NOTE_OPEN, DRAFT_NOTE_START, PUBLISH_NOTE, SENT_NOTHING, MACHINE_NOTE, RELEASE_NOTE, REVIEW_NOTE, PLANS_NOTE, PLANS_NOTE_END, CREATE_PLAN_NOTE, PLAN_NOTE, PLAN_NOTE_APPROVE, OPERATIONS_NOTE, OPERATION_NOTE];
    for (const s of all) {
      expect(s).not.toMatch(/\b(mch|drf|pln|rel|op|cl)_|\bD11\b/);
      expect(s).not.toMatch(/\b(author|publisher|approver)\b/);
    }
  });
});
