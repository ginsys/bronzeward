import { createElement as h, type ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import { WritesProvider } from "./forms.tsx";
import {
  ENTRY_EFFECTS, EnterRecovery, ExitRecovery, nextStep, PRE_ENTRY, PreEntry, RECOVERY_START, RecoveryBanner, RecoveryRead, REVOCATION_LIMITS, Revocation, scopeSummary,
  startModeOf, startRefusal, UNKNOWN_START, unreleased,
} from "./recovery.tsx";
import { type Problem, type ReadResult, TRANSPORT_FAILURE } from "./request.ts";
import { type Role, ShellContext } from "./shell.tsx";
import { Writes } from "./write.ts";

const html = (e: ReactElement) => renderToStaticMarkup(e);
function status(e: ReactElement): string {
  const m = /<p role="status" data-testid="[a-z-]+">(.*?)<\/p>/.exec(html(e));
  expect(m, html(e)).not.toBeNull();
  return m![1]!.replace(/<!-- -->/g, "").trim();
}
const problem = (code: string, extra: Record<string, unknown> = {}): Problem => ({ type: `urn:bronzeward:problem:${code}`, title: code, status: 409, ...extra });
const refused = (p: Problem): ReadResult<{ startMode?: string }> => ({ kind: "refused", status: p.status, problem: p });
const scopes = (...states: string[]) => states.map((state, i) => ({ machine: `mch_${i}`, state }));

describe("client contracts §8.5 start mode", () => {
  test("GET /server's startMode, or the process's refusal, shows it; a scope's refusal does not", () => {
    expect(startModeOf({ kind: "ok", data: { startMode: "normal" } })).toBe("normal");
    expect(startModeOf({ kind: "ok", data: { startMode: "recovery" } })).toBe("recovery");
    expect(startModeOf(refused(problem("recovery-mode-active")))).toBe("recovery");
    expect(startRefusal(refused(problem("recovery-mode-active")))).toBe(true);
    expect(startModeOf(refused(problem("recovery-mode-active", { scope: "mch_a" })))).toBeUndefined();
    expect(startRefusal(refused(problem("recovery-mode-active", { scope: "mch_a" })))).toBe(false);
    expect(startModeOf(refused(problem("conflict")))).toBeUndefined();
    expect(startModeOf({ kind: "transport" })).toBeUndefined();
  });
});

describe("the recovery banner", () => {
  test("the scope summary", () => {
    expect(scopeSummary([])).toBe("No machine scopes.");
    expect(scopeSummary(scopes("ready", "blocked", "pre-restore-unaccounted", "unresolved"))).toBe("4 of 4 scopes not released: none can commit.");
    expect(scopeSummary(scopes("released", "ready"))).toBe("1 of 2 scopes not released: those cannot commit.");
    expect(scopeSummary(scopes("released", "released"))).toBe("All 2 scopes released.");
  });

  test("states the entry, the epoch, the scopes and the process's restriction, or that the mode is unknown", () => {
    const enteredAt = new Date("2026-10-26T10:42:00").toISOString();
    const recovery = { epoch: "ep_r2l", recoveryMode: true, exit: null, scopes: scopes("ready"), entry: { enteredBy: "h-recovery", enteredAt, stoppedAt: enteredAt, reason: "restore", restored: [] } };
    expect(status(h(RecoveryBanner, { recovery, mode: "recovery" }))).toBe(`Recovery mode since 10:42, epoch ep_r2l. 1 of 1 scopes not released: none can commit. Drafts can be edited. ${RECOVERY_START}`);
    expect(status(h(RecoveryBanner, { recovery, mode: "normal" }))).toBe("Recovery mode since 10:42, epoch ep_r2l. 1 of 1 scopes not released: none can commit. Drafts can be edited.");
    expect(status(h(RecoveryBanner, { recovery: undefined, mode: "unknown" }))).toBe(`Recovery mode. Drafts can be edited. ${UNKNOWN_START}`);
  });

  test("its own read without data is shown under it with the catalogue's text, under its own test id", () => {
    expect(status(h(RecoveryRead, { res: { kind: "transport" } }))).toBe(TRANSPORT_FAILURE);
    expect(html(h(RecoveryRead, { res: { kind: "transport" } }))).toContain('data-testid="recovery-read"');
    expect(status(h(RecoveryRead, { res: { kind: "defect", message: "the response is not JSON" } }))).toBe("Not read: the response is not JSON.");
    const p = { ...problem("internal-error"), status: 500, title: "Internal error", instance: "req-9" };
    expect(status(h(RecoveryRead, { res: { kind: "unavailable", status: 500, problem: p } }))).toBe("Not read: internal-error. Internal error Request req-9.");
    expect(status(h(RecoveryRead, { res: { kind: "refused", status: 502, problem: null } }))).toBe("Not read: the server answered 502.");
    expect(html(h(RecoveryRead, { res: { kind: "discarded" } }))).toBe("");
  });

  test("before the entry: the process's restriction, and a read again", () => {
    const refused = { kind: "refused", status: 409, problem: { type: "urn:bronzeward:problem:recovery-mode-active", title: "Recovery mode active", status: 409, detail: "only the recovery entry", instance: "req-pre" } } as const;
    expect(status(h(PreEntry, { res: refused, onRead: () => {} }))).toBe(PRE_ENTRY);
    expect(html(h(PreEntry, { res: refused, onRead: () => {} }))).toContain(">Read again</button>");
    // §8.2: the refusal's title, detail and request ID stay shown under the restriction.
    expect(html(h(PreEntry, { res: refused, onRead: () => {} }))).toContain('<p data-testid="problem">Not read: Recovery mode active (only the recovery entry). Request req-pre.</p>');
  });
});

describe("the console's parts (#364)", () => {
  const writes = new Writes(() => "https://idp.invalid SYNTH-sub-1");
  const shell = (roles: Role[], e: ReactElement, offline = false) => html(h(ShellContext, { value: { roles, offline } }, h(WritesProvider, { value: writes }, e)));

  test("each state's next step is text, and a scope outside recovery is not counted", () => {
    expect(nextStep("pre-restore-unaccounted")).toMatch(/^Account the scope/);
    expect(nextStep("ready")).toBe("Release the scope. A release is not an approval.");
    expect(nextStep("released")).toBe("None.");
    expect(nextStep("unresolved")).toMatch(/classified after the normal restart/);
    expect(nextStep("blocked")).toMatch(/Keys are missing/);
    expect(unreleased(scopes("normal", "released", "ready", "blocked")).map((s) => s.state)).toEqual(["ready", "blocked"]);
  });

  test("the entry form states the effects before its control, which stays off until enter is typed", () => {
    const form = shell([], h(EnterRecovery));
    const escaped = ENTRY_EFFECTS.map((e) => e.replaceAll("'", "&#x27;"));
    for (const effect of escaped) expect(form).toContain(effect);
    for (const family of ["database", "provider", "credential-store"]) expect(form).toContain(`name="${family}-backup"`);
    expect(form.indexOf(escaped[0]!)).toBeLessThan(form.indexOf(">Enter recovery</button>"));
    expect(form).toMatch(/<button type="submit" disabled="">Enter recovery<\/button>/);
    // No role was read before the entry, so no role text is shown; offline is.
    expect(form).not.toContain("Needs");
    expect(shell([], h(EnterRecovery), true)).toContain("Offline");
    // Outside the page's writes there is no form.
    expect(html(h(EnterRecovery))).toBe("");
  });

  test("the exit is off for a role without recovery-admin, and while a scope is not released", () => {
    const exit = (roles: Role[], held: number, recoveryMode = true) => shell(roles, h(ExitRecovery, { held, recoveryMode, onChanged: () => {} }));
    expect(exit(["author"], 0)).toContain("Needs recovery-admin");
    expect(exit(["recovery-admin"], 2)).toContain("2 scopes not released (ready is not released)");
    expect(exit(["recovery-admin"], 1)).toContain("1 scope not released (ready is not released)");
    expect(exit(["recovery-admin"], 0, false)).toContain("Recovery mode is off");
    const open = exit(["recovery-admin"], 0);
    expect(open).toMatch(/<button type="submit">Exit recovery<\/button>/);
    expect(open).not.toContain("not released");
  });

  test("the revocation states its limits, and stays off without recovery-admin or a typed identity", () => {
    for (const roles of [["author"], ["recovery-admin"]] as Role[][]) {
      const form = shell(roles, h(Revocation));
      for (const limit of REVOCATION_LIMITS) expect(form).toContain(limit);
      expect(form).toMatch(/<button type="submit" disabled="">Revoke identity<\/button>/);
    }
    expect(shell(["author"], h(Revocation))).toContain("Needs recovery-admin");
    expect(shell(["recovery-admin"], h(Revocation))).toContain("type the identity back");
  });
});
