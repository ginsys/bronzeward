// The Talos access sign on the cluster page (ginsys/bronzeward#856): its words for each answer the
// route gives, that an unreachable store never reads as "not supplied", that the sign never says the
// key works, and that the sign is rendered for an author only.

import { createElement as h, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import { type Load, ReadsProvider, type Reads } from "./list.tsx";
import { ShellContext, type Role } from "./shell.tsx";
import { when } from "./states.tsx";
import {
  CANNOT_TELL, CHECKING, DELETED, HELD_MEANS, NOT_SUPPLIED, ONBOARD_PATH, TalosAccessSign, type TalosAccess, TalosAccessView, talosAccessWords,
} from "./talosaccess.tsx";

const created = "2026-11-21T05:40:12.123456789Z";
const ok = (data: TalosAccess): Load<TalosAccess> => ({ kind: "ok", data });
const held = ok({ present: true, version: 2, createdTime: created });
const problem = { type: "urn:bronzeward:problem:talos-access-unavailable", title: "t", status: 503, instance: "req_x" };

// renderToStaticMarkup runs no effect, so no read is sent here; the author-only gate is shown by what
// the page renders: the checking sentence for an author, nothing for anyone else.
const reads: Reads = {
  client: { read: () => new Promise(() => {}) },
  settle: () => false, undropped: () => () => true, view: "", again: 0, regained: 0, go: () => {}, failure: () => null,
};
const html = (e: ReactNode, roles: readonly Role[] = ["author"]) =>
  renderToStaticMarkup(h(ShellContext, { value: { roles, offline: false } }, h(ReadsProvider, { value: reads }, e)));

describe("the Talos access sign's words", () => {
  test("a held key says when it was supplied and which version, then what held means", () => {
    const words = talosAccessWords(held);
    expect(words).toEqual([`Talos access key held: supplied ${when(created)} (version 2).`, HELD_MEANS]);
    // The creation time leads and the version is secondary, in words (the lead's ruling on ginsys/bronzeward#856).
    expect(words?.[0]?.indexOf("supplied")).toBeLessThan(words?.[0]?.indexOf("version") ?? -1);
  });

  test("a key never supplied, or removed, says to supply one and where", () => {
    expect(talosAccessWords(ok({ present: false, reason: "never-written-or-removed" }))).toEqual([NOT_SUPPLIED]);
    expect(NOT_SUPPLIED).toContain("Onboard cluster page");
  });

  test("a key whose latest version was deleted says so, and says where to supply one again", () => {
    expect(talosAccessWords(ok({ present: false, reason: "latest-version-deleted" }))).toEqual([DELETED]);
    expect(DELETED).toContain("Onboard cluster page");
  });

  test("a read that gave no usable answer says it cannot tell, and never that no key was supplied", () => {
    const unusable: Load<TalosAccess>[] = [
      { kind: "refused", status: 503, problem },
      { kind: "unavailable", status: 503, problem },
      { kind: "transport" },
      { kind: "defect", message: "bad body" },
      // An answer that does not say present or why not is not a usable answer either.
      ok({ present: false }),
      ok({ present: true }),
      ok({ present: true, version: 2 }),
    ];
    for (const load of unusable) expect(talosAccessWords(load), JSON.stringify(load)).toEqual([CANNOT_TELL]);
    expect(CANNOT_TELL).not.toContain("not supplied");
  });

  test("a read in flight says it is checking, and a discarded read says nothing", () => {
    expect(talosAccessWords({ kind: "loading" })).toEqual([CHECKING]);
    expect(talosAccessWords({ kind: "discarded" })).toBeNull();
  });

  test("no sentence says the key works, shows a token, a path, a policy or the status token", () => {
    const loads: Load<TalosAccess>[] = [
      held, ok({ present: false, reason: "never-written-or-removed" }), ok({ present: false, reason: "latest-version-deleted" }), { kind: "transport" }, { kind: "loading" },
    ];
    const all = loads.flatMap((l) => talosAccessWords(l) ?? []).join(" ");
    expect(all).not.toMatch(/working|works|valid|trusted/i);
    expect(all).not.toMatch(/talos-ca|secret\/|policy|token|never-written-or-removed|latest-version-deleted|dependency-unavailable|talos-access-unavailable/i);
  });
});

describe("the Talos access sign's markup", () => {
  test("a held key shows its words, and the Onboard cluster page is a link where it is named", () => {
    expect(html(h(TalosAccessView, { load: held }))).toContain(`Talos access key held: supplied ${when(created)} (version 2).`);
    const out = html(h(TalosAccessView, { load: ok({ present: false, reason: "never-written-or-removed" }) }));
    expect(out).toContain("Talos access key not supplied yet. Supply one on the ");
    expect(out).toContain(`<a href="${ONBOARD_PATH}"`);
    expect(out).toContain(">Onboard cluster page</a>.");
  });

  test("the sign marks nothing for a discarded read", () => {
    expect(html(h(TalosAccessView, { load: { kind: "discarded" } }))).toBe("");
  });
});

describe("the sign is for an author", () => {
  test("an author's page mounts the read, which says it is checking until the answer comes", () => {
    expect(html(h(TalosAccessSign, { cluster: "cl_a" }), ["author"])).toContain(CHECKING);
  });

  test("a viewer, a publisher and a page whose roles are not read yet show nothing", () => {
    for (const roles of [["viewer"], ["publisher"], ["viewer", "publisher", "approver"], []] as Role[][]) {
      expect(html(h(TalosAccessSign, { cluster: "cl_a" }), roles), roles.join(",")).toBe("");
    }
  });
});
