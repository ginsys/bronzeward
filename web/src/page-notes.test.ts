// @vitest-environment happy-dom
// The sentences each path page shows under its title (ginsys/bronzeward#1055): each page is mounted with its own reads,
// and the test asserts the exact text, that it sits under the title before the page's content, that a part is shown only
// where its control is, and that a link in a note is the only one of its text and target.

import { act, createElement as h, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { CreateDraft, DraftItem, settledFragment } from "./drafts.tsx";
import { WritesProvider } from "./forms.tsx";
import { HANDOVER_TITLE } from "./handover.ts";
import { type Reads, ReadsProvider } from "./list.tsx";
import { MachineItem, MachineShown } from "./machine.tsx";
import { PlanNew } from "./plan.tsx";
import { PublishDraft, SENT_NOTHING } from "./publish.tsx";
import { Audit, Changes, DependencyItem, ImportBaseItem, Imports, OperationItem, Operations, PlanItem, Plans, ReleaseItem, RevisionItem, SourceItem, Sources } from "./resources.tsx";
import { ReviewItem } from "./review.tsx";
import { Page, type Role, ShellContext } from "./shell.tsx";
import { AddCluster, RecordMachine } from "./forms.tsx";
import { IngestionItem, StartImport } from "./imports.tsx";
import { ClusterItem, Clusters, Machines } from "./machines.tsx";
import { OnboardCluster } from "./onboard.tsx";
import { RecoveryConsole } from "./recovery.tsx";
import {
  CREATE_A_DRAFT, CREATE_PLAN_NOTE, DRAFT_NOTE, DRAFT_NOTE_OPEN, DRAFT_NOTE_START, MACHINE_NOTE, OPEN_A_MACHINE, OPERATION_NOTE, OPERATIONS_NOTE, PLAN_NOTE,
  PLAN_NOTE_APPROVE, PLANS_NOTE, PLANS_NOTE_END, PUBLISH_NOTE, RELEASE_NOTE, REVIEW_NOTE,
  ADD_MACHINE_NOTE, AUDIT_NOTE, BY_HAND_NOTE, CHANGES_NOTE, CLUSTER_NOTE, CLUSTERS_NOTE, CONNECT_NOTE, CREATE_DRAFT_NOTE, DEPENDENCY_NOTE, IMPORT_NOTE, IMPORTED_NOTE, IMPORTS_NOTE,
  MACHINES_NOTE, NOT_FOUND_NOTE, RECOVERY_NOTE, RECOVERY_ROLE_NOTE, revisionNote, SOURCES_NOTE, sourceNote, START_IMPORT_NOTE,
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
      if (failing) return { kind: "unavailable", status: 503, problem: null };
      const bare = path.split("?")[0]!;
      if (path in served) return { kind: "ok", data: served[path] };
      if (bare in served) return { kind: "ok", data: served[bare] };
      if (["/plans", "/operations", "/drafts", "/releases", "/machines", "/clusters", "/ingestions", "/acts", "/fragments", "/profiles", "/assignments", "/dependencies", "/dependency-alerts"].includes(bare) || bare.endsWith("/events") || bare.endsWith("/observations") || bare.endsWith("/timeline")) return { kind: "ok", data: { items: [], next: undefined } };
      return { kind: "refused", status: 404, problem: null };
    },
  },
  settle: (res: unknown, set: (r: unknown) => void, live: () => boolean) => {
    if (live()) set(res);
    return false;
  },
  undropped: () => () => true,
  view: "v", again, regained: 0, go: () => {}, failure: () => "FAILED",
}) as unknown as Reads;

type Roles = Role[];
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
  // A note inside the heading follows it in document order too, so containment is refused on its own.
  expect(h2.contains(n)).toBe(false);
  const title = [h2, ...box.querySelectorAll('[data-testid="draft-line"]'), ...[...box.querySelectorAll("small")].map((s) => s.parentElement), ...also];
  // Every node before the note in document order, text included (a failure line can be bare text).
  const walker = document.createTreeWalker(box, NodeFilter.SHOW_ELEMENT | NodeFilter.SHOW_TEXT);
  const stray: string[] = [];
  for (let node = walker.nextNode(); node !== null && node !== n; node = walker.nextNode()) {
    if (node.contains(n) || (node.nodeType === Node.TEXT_NODE && node.textContent!.trim() === "")) continue;
    if (title.some((t) => t === node || t?.contains(node))) continue;
    stray.push(node.nodeType === Node.TEXT_NODE ? node.textContent! : (node as Element).outerHTML.slice(0, 80));
  }
  expect(stray).toEqual([]);
};
// Reads that never answer, for the loading state.
let held = false;
// Reads that fail once the page shows an answer, and the shell's read counter that makes a page read again.
let failing = false;
let again = 0;
const linksTo = (href: string) => [...box.querySelectorAll("a")].filter((a) => a.getAttribute("href") === href);
const button = (label: string) => [...box.querySelectorAll("button")].find((b) => b.textContent === label);

beforeEach(() => {
  box = document.createElement("div");
  document.body.append(box);
  root = createRoot(box);
  served = {};
  held = false;
  failing = false;
  again = 0;
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
  test("after a failed read with the last answer kept, the note stays under the title, above the failure line, and once", async () => {
    served[`/drafts/${DRF}`] = draft("open", [imported]);
    const page = h(PublishDraft, { query: `draft=${DRF}` });
    await shown(page);
    failing = true;
    again = 1;
    await shown(page);
    expect(box.textContent).toContain("FAILED");
    expect(note().textContent).toBe(`${PUBLISH_NOTE} ${SENT_NOTHING}`);
    underTheTitle(note());
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
  test("a proposed plan: the always part under the title, and the approval part inside the Approval section, once", async () => {
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

// The pages off the path (ginsys/bronzeward#1055, pull request B): each shows one note directly under its title.
describe("the notes of the other pages", () => {
  const cluster = { id: "cl_a", name: "office", endpoint: "https://10.5.0.1:6443", contract: "v1.14", talosClusterId: "tcid" };
  const pageNote = async (page: ReactNode, text: string, roles?: Roles) => {
    await shown(page, roles);
    const n = note();
    expect(n.textContent).toBe(text);
    underTheTitle(n);
  };
  const lists = [
    ["the machines list", () => h(Machines, { query: "", go: () => {} }), MACHINES_NOTE],
    ["the clusters list", () => h(Clusters, {}), CLUSTERS_NOTE],
    ["Add machine", () => h(RecordMachine, { query: "" }), ADD_MACHINE_NOTE],
    ["Enter a cluster by hand", () => h(AddCluster, {}), BY_HAND_NOTE],
    ["Create draft", () => h(CreateDraft, {}), CREATE_DRAFT_NOTE],
    ["Drafts and releases", () => h(Changes, {}), CHANGES_NOTE],
    ["Imports", () => h(Imports, { query: "", go: () => {} }), IMPORTS_NOTE],
    ["Start import", () => h(StartImport, { query: "" }), START_IMPORT_NOTE],
    ["Fragments and profiles", () => h(Sources, { query: "", go: () => {} }), SOURCES_NOTE],
    ["Audit", () => h(Audit, {}), AUDIT_NOTE],
    ["Connect a cluster", () => h(OnboardCluster, {}), CONNECT_NOTE],
  ] as const;
  for (const [name, page, text] of lists) {
    test(`${name}: its note is the one sentence block under the title, also while its reads are pending`, async () => {
      held = true;
      await pageNote(page(), text);
    });
  }

  test("a fragment, profile or assignment, and its revision, show their note only once read, named by the kind", async () => {
    const source = { id: "frg_a", name: "base", cluster: "cl_a", scope: "cluster", layer: "base", revision: null, headRevision: 0, createdAt: "2026-11-21T14:02:00Z" };
    served["/fragments/frg_a"] = source;
    held = true;
    await shown(h(SourceItem, { kind: "fragment", id: "frg_a" }));
    expect(notes()).toHaveLength(0);
    held = false;
    await pageNote(h(SourceItem, { kind: "fragment", id: "frg_a", key: "read" }), sourceNote("fragment"));
    expect(sourceNote("profile")).toContain("profile");
    served["/assignment-revisions/asr_a"] = { id: "asr_a", cluster: "cl_a", machine: MCH, profiles: [], fragments: {}, author: "p0", createdAt: "2026-11-21T14:02:00Z" };
    await pageNote(h(RevisionItem, { kind: "assignment", id: "asr_a" }), revisionNote("assignment"));
  });

  test("the cluster page shows its note once the cluster is read, and the dependency and imported-configuration pages likewise", async () => {
    served["/clusters/cl_a"] = cluster;
    held = true;
    await shown(h(ClusterItem, { id: "cl_a" }));
    expect(notes()).toHaveLength(0);
    held = false;
    await pageNote(h(ClusterItem, { id: "cl_a", key: "read" }), CLUSTER_NOTE);
    served["/dependencies/dep_a"] = { id: "dep_a", provider: "github", object: "owner/repo", version: 1, created: "2026-11-21", class: "release", reason: null, firstRetainedAt: null, unknownSince: null, observedFrom: "2026-11-21T14:02:00Z", recordedAt: "2026-11-21T14:02:00Z", stale: false };
    await pageNote(h(DependencyItem, { id: "dep_a" }), DEPENDENCY_NOTE);
    served["/import-base-revisions/ibr_a"] = { id: "ibr_a", machine: MCH, author: "p0", createdAt: "2026-11-21T14:02:00Z", documentBytes: 10, references: 0, embedded: 0 };
    await pageNote(h(ImportBaseItem, { id: "ibr_a" }), IMPORTED_NOTE);
  });

  test("the import page shows its note once the import is read", async () => {
    served["/ingestions/ing_a"] = { id: "ing_a", kind: "import", mode: "encrypted", state: "held", machine: MCH, draft: DRF, operation: null, review: null, ownerGeneration: 1, leaseUntil: "2026-11-22T14:02:00Z", expiresAt: "2026-11-22T14:02:00Z", createdAt: "2026-11-21T14:02:00Z" };
    held = true;
    await shown(h(IngestionItem, { id: "ing_a" }));
    expect(notes()).toHaveLength(0);
    held = false;
    await pageNote(h(IngestionItem, { id: "ing_a", key: "read" }), IMPORT_NOTE);
  });

  test("the not-found page says where to start, after its heading and its own line", async () => {
    await shown(h(Page, { path: "/nowhere", query: "", go: () => {} }));
    const status = box.querySelector('[data-testid="status"]')!;
    expect(status.textContent).toContain("No page at /nowhere.");
    expect(note().textContent).toBe(NOT_FOUND_NOTE);
    expect(box.querySelector("h2")!.textContent).toBe("Not found");
    expect(status.compareDocumentPosition(note()) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    underTheTitle(note(), status.closest(".alert") ?? status);
  });

  test("a list or form page keeps its note under the title when its reads fail", async () => {
    failing = true;
    for (const [, page, text] of lists) {
      await act(async () => root.unmount());
      root = createRoot(box);
      await pageNote(page(), text);
    }
  });

  test("a list or form page keeps its note under the title when its reads succeed, empty or populated", async () => {
    for (const [, page, text] of lists) {
      await act(async () => root.unmount());
      root = createRoot(box);
      await pageNote(page(), text);
    }
    served["/machines"] = { items: [machine], next: undefined };
    served["/clusters"] = { items: [cluster], next: undefined };
    served["/drafts"] = { items: [draft("open", [])], next: undefined };
    served["/ingestions"] = { items: [{ id: "ing_a", kind: "import", mode: "encrypted", state: "held", machine: MCH, draft: DRF, operation: null, review: null, ownerGeneration: 1, leaseUntil: "2026-11-22T14:02:00Z", expiresAt: "2026-11-22T14:02:00Z", createdAt: "2026-11-21T14:02:00Z" }], next: undefined };
    served["/fragments"] = { items: [{ id: "frg_a", name: "base", cluster: "cl_a", scope: "cluster", layer: "base", revision: null, headRevision: 0, createdAt: "2026-11-21T14:02:00Z" }], next: undefined };
    served["/profiles"] = { items: [{ id: "prf_a", name: "workers", cluster: "cl_a", scope: "cluster", layer: "base", revision: null, headRevision: 0, createdAt: "2026-11-21T14:02:00Z" }], next: undefined };
    served["/assignments"] = { items: [{ id: "asg_a", cluster: "cl_a", machine: MCH, revision: null, headRevision: 0, createdAt: "2026-11-21T14:02:00Z" }], next: undefined };
    served["/acts"] = { items: [{ id: "act_a", at: "2026-11-21T14:02:00Z", principal: "p0", principalKind: "human", via: "api", role: null, action: "draft.create", subjects: [], requestId: null }], next: undefined };
    const populated = ["the machines list", "the clusters list", "Drafts and releases", "Imports", "Fragments and profiles", "Audit"];
    for (const [, page, text] of lists.filter(([n]) => populated.includes(n))) {
      await act(async () => root.unmount());
      root = createRoot(box);
      await pageNote(page(), text);
      expect(box.querySelector("table, ul, ol")).not.toBeNull();
    }
  });

  test("the placement check refuses a note drawn inside the heading, and one drawn below a control", async () => {
    await act(async () => root.unmount());
    box.innerHTML = '<h2>Title<p data-testid="page-note">x</p></h2>';
    expect(() => underTheTitle(note())).toThrow();
    box.innerHTML = '<h2>Title</h2><button>Add</button><p data-testid="page-note">x</p>';
    expect(() => underTheTitle(note())).toThrow();
    box.innerHTML = '<h2>Title</h2><p data-testid="page-note">x</p><button>Add</button>';
    expect(() => underTheTitle(note())).not.toThrow();
    box.innerHTML = "";
    root = createRoot(box);
  });

  test("the fragment, profile and assignment pages and their revision pages show the note named by their kind, once read", async () => {
    const at = "2026-11-21T14:02:00Z";
    served["/profiles/prf_a"] = { id: "prf_a", name: "workers", cluster: "cl_a", scope: "cluster", layer: "base", revision: null, headRevision: 0, createdAt: at };
    served["/assignments/asg_a"] = { id: "asg_a", cluster: "cl_a", machine: MCH, revision: null, headRevision: 0, createdAt: at };
    served["/fragment-revisions/frr_a"] = { id: "frr_a", cluster: "cl_a", name: "base", layer: "base", document: "{}", declarations: { references: {}, embedded: [] }, author: "p0", createdAt: at };
    served["/profile-revisions/prr_a"] = { id: "prr_a", cluster: "cl_a", name: "workers", fragments: [], author: "p0", createdAt: at };
    const pages = [
      [() => h(SourceItem, { kind: "profile", id: "prf_a" }), sourceNote("profile")],
      [() => h(SourceItem, { kind: "assignment", id: "asg_a" }), sourceNote("assignment")],
      [() => h(RevisionItem, { kind: "fragment", id: "frr_a" }), revisionNote("fragment")],
      [() => h(RevisionItem, { kind: "profile", id: "prr_a" }), revisionNote("profile")],
    ] as const;
    for (const [page, text] of pages) {
      await act(async () => root.unmount());
      root = createRoot(box);
      await pageNote(page(), text);
    }
  });

  test("the item pages show no note while their read is pending or has failed", async () => {
    served["/assignment-revisions/asr_a"] = { id: "asr_a", cluster: "cl_a", machine: MCH, profiles: [], fragments: {}, author: "p0", createdAt: "2026-11-21T14:02:00Z" };
    served["/dependencies/dep_a"] = { id: "dep_a", provider: "github", object: "owner/repo", version: 1, created: "2026-11-21", class: "release", reason: null, firstRetainedAt: null, unknownSince: null, observedFrom: "2026-11-21T14:02:00Z", recordedAt: "2026-11-21T14:02:00Z", stale: false };
    served["/import-base-revisions/ibr_a"] = { id: "ibr_a", machine: MCH, author: "p0", createdAt: "2026-11-21T14:02:00Z", documentBytes: 10, references: 0, embedded: 0 };
    served["/recovery"] = { epoch: "e0", recoveryMode: false, entry: null, exit: null, scopes: [] };
    served["/clusters/cl_a"] = cluster;
    served["/ingestions/ing_a"] = { id: "ing_a", kind: "import", mode: "encrypted", state: "held", machine: MCH, draft: DRF, operation: null, review: null, ownerGeneration: 1, leaseUntil: "2026-11-22T14:02:00Z", expiresAt: "2026-11-22T14:02:00Z", createdAt: "2026-11-21T14:02:00Z" };
    served["/fragments/frg_a"] = { id: "frg_a", name: "base", cluster: "cl_a", scope: "cluster", layer: "base", revision: null, headRevision: 0, createdAt: "2026-11-21T14:02:00Z" };
    served["/profiles/prf_a"] = { id: "prf_a", name: "workers", cluster: "cl_a", scope: "cluster", layer: "base", revision: null, headRevision: 0, createdAt: "2026-11-21T14:02:00Z" };
    served["/assignments/asg_a"] = { id: "asg_a", cluster: "cl_a", machine: MCH, revision: null, headRevision: 0, createdAt: "2026-11-21T14:02:00Z" };
    served["/fragment-revisions/frr_a"] = { id: "frr_a", cluster: "cl_a", name: "base", layer: "base", document: "{}", declarations: { references: {}, embedded: [] }, author: "p0", createdAt: "2026-11-21T14:02:00Z" };
    served["/profile-revisions/prr_a"] = { id: "prr_a", cluster: "cl_a", name: "workers", fragments: [], author: "p0", createdAt: "2026-11-21T14:02:00Z" };
    const items = [
      () => h(ClusterItem, { id: "cl_a" }),
      () => h(IngestionItem, { id: "ing_a" }),
      () => h(SourceItem, { kind: "fragment", id: "frg_a" }),
      () => h(SourceItem, { kind: "profile", id: "prf_a" }),
      () => h(SourceItem, { kind: "assignment", id: "asg_a" }),
      () => h(RevisionItem, { kind: "fragment", id: "frr_a" }),
      () => h(RevisionItem, { kind: "profile", id: "prr_a" }),
      () => h(RevisionItem, { kind: "assignment", id: "asr_a" }),
      () => h(DependencyItem, { id: "dep_a" }),
      () => h(ImportBaseItem, { id: "ibr_a" }),
      () => h(RecoveryConsole, {}),
    ];
    for (const state of ["held", "failing"] as const) {
      held = state === "held";
      failing = state === "failing";
      for (const page of items) {
        await act(async () => root.unmount());
        root = createRoot(box);
        await shown(page());
        expect(notes()).toHaveLength(0);
      }
    }
  });

  test("an import with no machine, a draft update, shows the import note and its progress part draws nothing", async () => {
    served["/ingestions/ing_u"] = { id: "ing_u", kind: "draft-update", mode: "transient", state: "held", machine: null, draft: DRF, operation: null, review: null, ownerGeneration: 1, leaseUntil: "2026-11-22T14:02:00Z", expiresAt: "2026-11-22T14:02:00Z", createdAt: "2026-11-21T14:02:00Z" };
    await pageNote(h(IngestionItem, { id: "ing_u" }), IMPORT_NOTE);
    expect(box.querySelector('section[aria-label="Progress"]')).toBeNull();
    expect(IMPORT_NOTE).toContain("draft update");
    expect(IMPORTS_NOTE).toContain("draft updates");
  });

  test("the recovery page adds its role sentence only for a person without the recovery role", async () => {
    served["/recovery"] = { epoch: "e0", recoveryMode: false, entry: null, exit: null, scopes: [] };
    await pageNote(h(RecoveryConsole, {}), `${RECOVERY_NOTE} ${RECOVERY_ROLE_NOTE}`, ["author"]);
    await act(async () => root.unmount());
    root = createRoot(box);
    await pageNote(h(RecoveryConsole, {}), RECOVERY_NOTE, ["author", "recovery-admin"]);
  });
});

describe("no note carries an identifier or a table reference", () => {
  test("the sentences hold no identifier string, no role word and no table cite", () => {
    const all = [ADD_MACHINE_NOTE, AUDIT_NOTE, BY_HAND_NOTE, CHANGES_NOTE, CLUSTER_NOTE, CLUSTERS_NOTE, CONNECT_NOTE, CREATE_DRAFT_NOTE, DEPENDENCY_NOTE, IMPORT_NOTE, IMPORTED_NOTE, IMPORTS_NOTE, MACHINES_NOTE, NOT_FOUND_NOTE, RECOVERY_NOTE, SOURCES_NOTE, sourceNote("fragment"), revisionNote("fragment"), START_IMPORT_NOTE, DRAFT_NOTE, DRAFT_NOTE_OPEN, DRAFT_NOTE_START, PUBLISH_NOTE, SENT_NOTHING, MACHINE_NOTE, RELEASE_NOTE, REVIEW_NOTE, PLANS_NOTE, PLANS_NOTE_END, CREATE_PLAN_NOTE, PLAN_NOTE, PLAN_NOTE_APPROVE, OPERATIONS_NOTE, OPERATION_NOTE];
    for (const s of all) {
      expect(s).not.toMatch(/\b(mch|drf|pln|rel|op|cl)_|\bD11\b/);
      expect(s).not.toMatch(/\b(author|publisher|approver)\b/);
    }
  });
});
