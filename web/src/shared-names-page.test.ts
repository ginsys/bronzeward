// @vitest-environment happy-dom
// The Shared names block on a mounted draft page (ginsys/bronzeward#1193 part 6b-1): the block reads when it is opened,
// each name shows this cluster's view, and a press sends the entry of the draft with the draft's last ETag.

import { act, createElement as h } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { forgetETag, keepETag, watchWrites } from "./drafts.tsx";
import { unsaved, WritesProvider } from "./forms.tsx";
import { NO_ETAG } from "./handover.ts";
import { type Reads, ReadsProvider } from "./list.tsx";
import { SharedNames } from "./shared-names.tsx";
import { ShellContext } from "./shell.tsx";
import { Write, Writes } from "./write.ts";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const DRF = "drf_k2pq3rstuvwxyz234567abcdef";
const head = (id: string, name: string, revision: string | null, cluster: string | null = null) => ({ id, name, cluster, revision, scope: cluster === null ? "library" : "cluster", layer: "global", headRevision: 1, createdAt: "2026-11-02T00:00:00Z" });
const adoption = (over: Record<string, unknown>) => ({ cluster: "cl_a", revision: "frv_old", adoptionRevision: 2, behind: true, release: "rel_a", via: null, machines: [], ...over });

// One shared fragment the cluster is behind on, one it follows at the head, one nobody follows, two removed (this cluster still
// follows one, only another cluster follows the other); and a profile.
const answers: Record<string, unknown> = {
  "/fragments": { items: [head("frg_1", "ntp", "frv_head"), head("frg_2", "dns", "frv_dns"), head("frg_3", "motd", "frv_motd"), head("frg_4", "gone", null), head("frg_5", "own", "frv_own", "cl_a"), head("frg_6", "lost", null), head("frg_7", "slow", null), head("frg_8", "broken", null)] },
  "/fragments/frg_4/adoptions": { items: [adoption({ revision: "frv_gone", behind: true }), adoption({ cluster: "cl_b", revision: "frv_gone", behind: true })] },
  "/fragments/frg_6/adoptions": { items: [adoption({ cluster: "cl_b", revision: "frv_lost", behind: true })] },
  "/fragments/frg_1/adoptions": { items: [adoption({}), adoption({ cluster: "cl_b", behind: false })] },
  "/fragments/frg_2/adoptions": { items: [adoption({ revision: "frv_dns", behind: false })] },
  "/fragments/frg_3/adoptions": { items: [] },
  "/profiles": { items: [head("prf_1", "edge", "prv_edge")] },
  "/profiles/prf_1/adoptions": { items: [adoption({ revision: "prv_edge", behind: false })] },
};

describe("the Shared names block on a draft's page", () => {
  let box: HTMLDivElement;
  let root: Root;
  let writes: Writes;
  let submit: ReturnType<typeof vi.spyOn>;
  // `slow` is a removed name whose adoptions read never answers; `broken` is one whose read is refused (no answer is set for it).
  const read = vi.fn(async (path: string) => (path === "/fragments/frg_7/adoptions" ? new Promise(() => {}) : Object.hasOwn(answers, path) ? { kind: "ok", data: answers[path] } : { kind: "refused", status: 404, problem: null }));
  const reads = { client: { read }, settle: (res: unknown, set: (r: unknown) => void, live: () => boolean) => (live() && set(res), true), undropped: () => () => true, view: "", again: 0, regained: 0, go: () => {}, failure: () => null } as unknown as Reads;
  beforeEach(() => {
    box = document.createElement("div");
    document.body.append(box);
    root = createRoot(box);
    read.mockClear();
    writes = new Writes(() => "p");
    submit = vi.spyOn(writes, "submit").mockImplementation(async () => ({ kind: "unknown" }) as never);
    forgetETag(DRF);
  });
  afterEach(async () => {
    unsaved.clear();
    await act(async () => root.unmount());
    box.remove();
  });
  const mount = (entries: unknown[] = []) =>
    act(async () =>
      root.render(h(WritesProvider, { value: writes }, h(ShellContext, { value: { roles: ["author"], offline: false } }, h(ReadsProvider, { value: reads }, h(SharedNames, { draft: { id: DRF, cluster: "cl_a", state: "open", entries } as never, seeChange: () => {} }))))),
    );
  const button = (label: string) => [...box.querySelectorAll("button")].find((b) => b.textContent === label);
  const open = () => act(async () => void button("Show shared names")!.click());
  const settled = () => act(async () => void (await new Promise((r) => setTimeout(r, 20))));

  test("nothing is read until it is opened; then each name shows this cluster's view and only the controls that apply", async () => {
    await mount();
    expect(read).not.toHaveBeenCalled();
    await open();
    await settled();
    const text = box.textContent ?? "";
    // The shared names with a revision are listed, and the cluster's own fragment is not (the removed ones: next test).
    expect(text).toContain("fragment ntp");
    expect(text).toContain("fragment dns");
    expect(text).toContain("fragment motd");
    expect(text).toContain("profile edge");
    expect(text).not.toContain("fragment own");
    expect(text).toContain("behind the shared head");
    expect(text).toContain("Not followed by this cluster.");
    // Adopt where behind or not followed; Stop following where followed; both for a name followed and behind.
    expect(button("Adopt ntp")).toBeDefined();
    expect(button("Stop following ntp")).toBeDefined();
    expect(button("Adopt dns")).toBeUndefined();
    expect(button("Stop following dns")).toBeDefined();
    expect(button("Adopt motd")).toBeDefined();
    expect(button("Stop following motd")).toBeUndefined();
    expect(text).toContain("Adopt records in this draft that this cluster follows the shared fragment ntp");
  });

  // ginsys/bronzeward#1242: a removed head is still a head (persistence-api §3.1.1), and a cluster's adoption keeps the revision it names.
  test("a removed shared name this cluster still follows is listed in words with Stop following and no Adopt; one it does not follow is not listed", async () => {
    await mount();
    await open();
    await settled();
    const row = [...box.querySelectorAll("li")].find((li) => li.textContent?.startsWith("fragment gone"));
    expect(row).toBeDefined();
    expect(row!.textContent).toContain("Followed at the revision");
    expect(row!.textContent).toContain("The shared name was removed from the library");
    expect(row!.textContent).not.toContain("behind the shared head");
    expect(button("Stop following gone")).toBeDefined();
    expect(button("Adopt gone")).toBeUndefined();
    // Another cluster follows `lost`, this one does not: nothing to adopt and nothing to stop.
    expect(box.textContent).not.toContain("fragment lost");
    // While a removed name's read is pending nothing says this cluster follows it, so it is not listed; a read that failed still shows its row.
    expect(box.textContent).not.toContain("fragment slow");
    expect(box.textContent).toContain("fragment broken");
    expect(button("Stop following broken")).toBeUndefined();
    keepETag(DRF, '"5-t"', undefined);
    await act(async () => void button("Stop following gone")!.click());
    const stop = (submit.mock.calls[0]![0] as { spec: Record<string, unknown> }).spec;
    expect(stop).toMatchObject({ method: "DELETE", path: `/drafts/${DRF}/adoptions/fragments/gone`, ifMatch: '"5-t"' });
  });

  test("the draft's own entry decides the view: an adoption at the head says so, and offers Stop following and Adopt again", async () => {
    await mount([{ kind: "adoption", of: "fragment", name: "ntp", head: "frg_1", base: 2, revision: "frv_head" }]);
    await open();
    await settled();
    expect(box.textContent).toContain("This draft adopts");
    // The row is on the counter the entry was staged on, so nothing says it is stale; Adopt stays for a row another draft removed.
    expect(box.textContent).not.toContain("Adopt again to record the current one.");
    expect(button("Adopt ntp")).toBeDefined();
    expect(button("Stop following ntp")).toBeDefined();
  });

  // A re-adoption over a removed adoption cannot be removed again (404, persistence-api §3.1.1); a first adoption can.
  test("Stop following is offered for an adoption the draft introduced, and not for a re-adoption over a removed one", async () => {
    await mount([{ kind: "adoption", of: "fragment", name: "motd", head: "frg_3", base: 3, revision: "frv_motd" }]);
    await open();
    await settled();
    expect(box.textContent).toContain("This draft adopts");
    expect(button("Stop following motd")).toBeUndefined();
    await act(async () => root.unmount());
    root = createRoot(box);
    await mount([{ kind: "adoption", of: "fragment", name: "motd", head: "frg_3", base: null, revision: "frv_motd" }]);
    await open();
    await settled();
    expect(button("Stop following motd")).toBeDefined();
  });

  // The carried finding G1: another draft changed the cluster's adoption after this entry was staged.
  test("an adoption staged before the cluster's adoption changed offers Adopt again and says why, though it is at the head", async () => {
    // ntp: the row is at counter 2, the entry was staged on counter 1, and it adopts the head's revision.
    await mount([{ kind: "adoption", of: "fragment", name: "ntp", head: "frg_1", base: 1, revision: "frv_head" }]);
    await open();
    await settled();
    expect(box.textContent).toContain("Adopt again to record the current one.");
    expect(button("Adopt ntp")).toBeDefined();
    expect(button("Stop following ntp")).toBeDefined();
    // motd: staged over a row that is gone: Adopt again, and no Stop following.
    await act(async () => root.unmount());
    root = createRoot(box);
    await mount([{ kind: "adoption", of: "fragment", name: "motd", head: "frg_3", base: 3, revision: "frv_motd" }]);
    await open();
    await settled();
    expect(button("Adopt motd")).toBeDefined();
    expect(button("Stop following motd")).toBeUndefined();
  });

  test("a removal staged before the cluster's adoption changed offers Stop following again", async () => {
    // dns: the row is at counter 2 and the removal was staged on counter 1.
    await mount([{ kind: "adoption", of: "fragment", name: "dns", head: "frg_2", base: 1, revision: null }]);
    await open();
    await settled();
    expect(box.textContent).toContain("Stop following again to record the current one.");
    expect(button("Stop following dns")).toBeDefined();
    expect(button("Adopt dns")).toBeDefined();
  });

  test("Adopt sends the head's revision under the draft's last ETag, and Stop following deletes the adoption", async () => {
    keepETag(DRF, '"5-t"', undefined);
    await mount();
    await open();
    await settled();
    await act(async () => void button("Adopt ntp")!.click());
    expect(submit).toHaveBeenCalledTimes(1);
    const adopt = (submit.mock.calls[0]![0] as { spec: Record<string, unknown> }).spec;
    expect(adopt).toMatchObject({ method: "PUT", path: `/drafts/${DRF}/adoptions/fragments/ntp`, body: { revision: "frv_head" }, ifMatch: '"5-t"', resource: `/drafts/${DRF}` });
    await act(async () => void button("Stop following dns")!.click());
    const stop = (submit.mock.calls[1]![0] as { spec: Record<string, unknown> }).spec;
    expect(stop).toMatchObject({ method: "DELETE", path: `/drafts/${DRF}/adoptions/fragments/dns`, ifMatch: '"5-t"' });
    expect(stop["body"]).toBeUndefined();
  });

  // The press's page may be left before its write settles (ginsys/bronzeward#517 H3): the hook, not the press, reads the draft
  // again on whichever page shows it then, so a page opened again is never left with old entries under the newer ETag.
  test("a write that settles after the press was left refreshes the draft page now shown, and a refusal refreshes none", async () => {
    keepETag(DRF, '"5-t"', undefined);
    await mount();
    await open();
    await settled();
    await act(async () => void button("Adopt ntp")!.click());
    const spec = (submit.mock.calls[0]![0] as { spec: { settled: (o: unknown, w: unknown) => void } }).spec;
    // The press goes away; another page of the same draft is shown.
    await act(async () => root.unmount());
    const shown = vi.fn();
    const stop = watchWrites(DRF, shown);
    spec.settled({ kind: "refused", status: 412, problem: null, replayed: false }, new Write({ method: "PUT", path: "/x", body: null, resource: "/x" }));
    expect(shown).not.toHaveBeenCalled();
    spec.settled({ kind: "done", status: 200, data: null, replayed: false, etag: '"6-t"' }, new Write({ method: "PUT", path: "/x", body: null, resource: "/x" }));
    expect(shown).toHaveBeenCalledTimes(1);
    stop();
    root = createRoot(box);
  });

  test("a press with no ETag read for the draft sends nothing and says so", async () => {
    await mount();
    await open();
    await settled();
    await act(async () => void button("Adopt motd")!.click());
    expect(submit).not.toHaveBeenCalled();
    expect(box.textContent).toContain(NO_ETAG);
  });
});
