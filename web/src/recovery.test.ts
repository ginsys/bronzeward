import { createElement as h, type ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import { PRE_ENTRY, PreEntry, RECOVERY_START, RecoveryBanner, scopeSummary, startModeOf, startRefusal, UNKNOWN_START } from "./recovery.tsx";
import type { Problem, ReadResult } from "./request.ts";

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

  test("before the entry: the process's restriction, and a read again", () => {
    expect(status(h(PreEntry, { onRead: () => {} }))).toBe(PRE_ENTRY);
    expect(html(h(PreEntry, { onRead: () => {} }))).toContain(">Read again</button>");
  });
});
