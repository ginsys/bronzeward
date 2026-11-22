import { readFileSync } from "node:fs";
import { createElement as h, type ReactElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, test } from "vitest";
import { WritesProvider } from "./forms.tsx";
import {
  ConfirmationLine, ENTRY_EFFECTS, EnterRecovery, Entered, ExitRecovery, nextStep, NORMAL_START, noteEntry, PRE_ENTRY, PreEntry, RECOVERY_START, RecoveryBanner, RecoveryRead, REVOCATION_LIMITS, Revocation, revocationConfirmation, scopeSummary,
  stamp, startModeOf, startRefusal, UNKNOWN_START, unreleased,
} from "./recovery.tsx";
import { type Problem, type ReadResult, TRANSPORT_FAILURE } from "./request.ts";
import { type Role, ShellContext } from "./shell.tsx";
import { Writes } from "./write.ts";

const html = (e: ReactElement) => renderToStaticMarkup(e);
function status(e: ReactElement): string {
  const m = /<p(?: role="alert")? data-testid="[a-z-]+">(.*?)<\/p>/.exec(html(e));
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
    expect(status(h(RecoveryRead, { res: { kind: "unavailable", status: 500, problem: p } }))).toBe("Not read. The server failed while handling the request. Report it with the reference below.");
    expect(status(h(RecoveryRead, { res: { kind: "refused", status: 502, problem: null } }))).toBe("Not read: the server answered 502.");
    expect(html(h(RecoveryRead, { res: { kind: "discarded" } }))).toBe("");
  });

  test("before the entry: the process's restriction, and a read again", () => {
    const refused = { kind: "refused", status: 409, problem: { type: "urn:bronzeward:problem:recovery-mode-active", title: "Recovery mode active", status: 409, detail: "only the recovery entry", instance: "req-pre" } } as const;
    expect(status(h(PreEntry, { res: refused, onRead: () => {} }))).toBe(PRE_ENTRY);
    expect(html(h(PreEntry, { res: refused, onRead: () => {} }))).toContain(">Read again</button>");
    // §8.2: the refusal in words, its detail and its request number stay shown under the restriction; the page
    // is where the entry is made, so the words carry no "wait for an administrator".
    const shown = html(h(PreEntry, { res: refused, onRead: () => {} }));
    expect(shown).toContain('<p data-testid="problem">Not read. The server refuses this until a recovery is finished. The server says: only the recovery entry.</p>');
    expect(shown).toContain("Request: req-pre");
  });
});

describe("the console's parts (#364)", () => {
  const writes = new Writes(() => "https://idp.invalid SYNTH-sub-1");
  const shell = (roles: Role[], e: ReactElement, offline = false) => html(h(ShellContext, { value: { roles, offline } }, h(WritesProvider, { value: writes }, e)));

  test("each state's next step is text, and a scope outside recovery is not counted", () => {
    expect(nextStep("pre-restore-unaccounted")).toMatch(/^Account the scope/);
    expect(nextStep("ready")).toBe("Release the scope. A release is not an approval.");
    expect(nextStep("released")).toBe("None.");
    // An unresolved scope can stay so after its operation ended, or the operation can be cancelled
    // first: the restart is conditional, and the other way is to mark the scope now.
    expect(nextStep("unresolved")).toMatch(/^Read the machine's operations\. If one has not ended, it is classified after the normal restart/);
    expect(nextStep("unresolved")).toMatch(/If none holds the scope, mark it again now\.$/);
    // A scope is also blocked after a plain accounting, until it is marked: the step does not claim a missing key.
    expect(nextStep("blocked")).toMatch(/^Mark the scope, then read its recorded cause/);
    expect(nextStep("blocked")).not.toMatch(/Keys are missing/);
    expect(unreleased(scopes("normal", "released", "ready", "blocked")).map((s) => s.state)).toEqual(["ready", "blocked"]);
  });

  test("the entry form states the effects before its one control, which asks for nothing typed back", () => {
    const form = shell([], h(EnterRecovery));
    const escaped = ENTRY_EFFECTS.map((e) => e.replaceAll("'", "&#x27;"));
    for (const effect of escaped) expect(form).toContain(effect);
    for (const family of ["database", "provider", "credential-store"]) expect(form).toContain(`name="${family}-backup"`);
    expect(form.indexOf(escaped[0]!)).toBeLessThan(form.indexOf(">Enter recovery</button>"));
    expect(form).toMatch(/<button type="submit">Enter recovery<\/button>/);
    expect(form).not.toContain("Type enter");
    // Offline, the one press is off with its reason.
    expect(shell([], h(EnterRecovery), true)).toMatch(/<button type="submit" disabled="">Enter recovery<\/button> Offline/);
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

  test("the entry's result in the header: its body, a saved success that could not be read, a start that takes none", () => {
    try {
      expect(html(h(Entered))).toBe("");
      noteEntry({ act: "act_a", epoch: "ep_new", previousEpoch: "ep_old", machines: 2, operations: [] } as never);
      expect(html(h(Entered))).toContain("Epoch ep_new, was ep_old");
      // A success whose body could not be read stays saved: no body is read from it.
      noteEntry(null);
      expect(html(h(Entered))).toContain("saved (the server answered), but its answer could not be read");
      // A refusal keeps its problem in the header: the form that showed it does not outlive the read.
      const refused = (extra: Record<string, unknown>) =>
        ({ type: "urn:bronzeward:problem:conflict", title: "conflict", detail: "SYNTH detail", status: 409, instance: "req_synth", ...extra }) as Problem;
      noteEntry({ refused: refused({}) });
      const normal = html(h(Entered));
      expect(normal).toContain(NORMAL_START.replaceAll("'", "&#x27;"));
      expect(normal).toContain("The server says: SYNTH detail.");
      expect(normal).toContain("Request: req_synth");
      noteEntry({ refused: refused({ epoch: "ep_other" }) });
      const other = html(h(Entered));
      expect(other).toContain("An entry is already recorded on this server, in epoch ep_other, under another request.");
      expect(other).toContain("Request: req_synth");
      expect(other).not.toContain("started normally");
    } finally {
      noteEntry(undefined);
    }
    expect(html(h(Entered))).toBe("");
  });

  test("a start that takes no entry names the operator's order: stop every instance, then restore (the runbook's step 1)", () => {
    expect(NORMAL_START).toMatch(/Stop every instance, restore the backups, and start one for recovery\.$/);
  });

  test("the entry's times must exist on the calendar: an impossible date is refused, never rolled over", () => {
    expect(stamp("2026-11-01T08:30:00Z")).toBe("2026-11-01T08:30:00.000Z");
    expect(stamp("2026-11-01T09:30+01:00")).toBe("2026-11-01T08:30:00.000Z");
    expect(stamp("2028-02-29T00:00:00Z")).toBe("2028-02-29T00:00:00.000Z");
    // Years 0 to 99 are not shifted to 1900 to 1999, and the century rule holds.
    expect(stamp("0000-02-29T08:30:00Z")).toBe("0000-02-29T08:30:00.000Z");
    expect(stamp("2000-02-29T08:30:00Z")).toBe("2000-02-29T08:30:00.000Z");
    expect(stamp("2100-02-29T08:30:00Z")).toBeUndefined();
    for (const bad of ["2026-02-30T08:30:00Z", "2026-02-29T08:30:00Z", "2026-04-31T08:30:00Z", "2026-13-01T08:30:00Z", "2026-00-10T08:30:00Z", "2026-11-00T08:30:00Z", "2026-11-01T24:00:00Z", "2026-11-01T08:60:00Z", "2026-11-01T08:30:60Z", "2026-11-01T08:30:00+24:00", "2026-11-01T08:30:00", "yesterday"]) {
      expect(stamp(bad), bad).toBeUndefined();
    }
  });

  test("the revocation states its limits, and stays off without recovery-admin or a named identity", () => {
    for (const roles of [["author"], ["recovery-admin"]] as Role[][]) {
      const form = shell(roles, h(Revocation));
      for (const limit of REVOCATION_LIMITS) expect(form).toContain(limit);
      expect(form).toMatch(/<button type="submit" disabled="">Revoke identity<\/button>/);
    }
    expect(shell(["author"], h(Revocation))).toContain("Needs recovery-admin");
    expect(shell(["recovery-admin"], h(Revocation))).toContain("Name the identity, or the issuer and subject, and give a reason");
    expect(shell(["recovery-admin"], h(Revocation))).not.toContain("type the identity back");
  });

  // One press confirms a permanent act, so the line that names its target is the only check: it names the identity as
  // entered, or the issuer and the subject both, and a target that cannot be shown cannot be confirmed.
  describe("the revocation's confirmation line", () => {
    const BIDI = "‮";
    test("an identity is named exactly as entered, with the act's permanence", () => {
      expect(revocationConfirmation("idn_BWSYNTH", "", "", " left ")).toEqual({
        line: "Revokes the identity idn_BWSYNTH. A revocation is permanent.", target: { identity: "idn_BWSYNTH", reason: "left" },
      });
    });
    test("an issuer and a subject are both named, each as entered", () => {
      const c = revocationConfirmation("", "https://idp.example", "sub-1", " left ");
      expect(c.line).toBe("Revokes the subject sub-1 of the issuer https://idp.example. A revocation is permanent.");
      expect(c.target).toEqual({ iss: "https://idp.example", sub: "sub-1", reason: "left" });
    });
    test("a target that is not safe text leaves the press off with its reason, in both forms", () => {
      for (const [identity, iss, sub] of [[`idn${BIDI}x`, "", ""], ["", `https://i${BIDI}.example`, "sub-1"], ["", "https://idp.example", `s${BIDI}ub`], ["x".repeat(301), "", ""]] as [string, string, string][]) {
        const c = revocationConfirmation(identity, iss, sub, "left");
        expect(c.why, identity + iss + sub).toBe("The identity cannot be shown, so it cannot be confirmed.");
        expect(c.target).toBeUndefined();
        expect(c.line).toBeUndefined();
      }
    });
    test("the target is checked and named as entered: an edge control or format character keeps the press off, a space is shown as typed", () => {
    for (const v of ["﻿alice", "alice\t", "\talice", "ali​ce"]) {
      const c = revocationConfirmation(v, "", "", "left");
      expect([v, c.target, c.line, c.why]).toEqual([v, undefined, undefined, "The identity cannot be shown, so it cannot be confirmed."]);
    }
    // Spaces inside stay as typed.
    expect(revocationConfirmation("alice  smith", "", "", "left").line).toBe("Revokes the identity alice  smith. A revocation is permanent.");
  });

  // A space at either edge of a target is invisible in the line, and the server would act on it: a padded subject records
  // a new human row and revokes that, leaving the real one active. So the press stays off and the reason names it.
  test("a space at the start or end of the identity, the issuer or the subject keeps the press off with a reason that names it", () => {
    for (const [args, field] of [
      [[" alice", "", ""], "identity"], [["alice ", "", ""], "identity"],
      [["", " https://idp.example", "sub-1"], "issuer"], [["", "https://idp.example ", "sub-1"], "issuer"],
      [["", "https://idp.example", " alice"], "subject"], [["", "https://idp.example", "alice "], "subject"],
      // a no-break space at either edge
      [[" alice", "", ""], "identity"], [["alice ", "", ""], "identity"],
      [["", "https://idp.example", " alice"], "subject"], [["", "https://idp.example", "alice "], "subject"],
      [["", " https://idp.example", "sub-1"], "issuer"],
    ] as [[string, string, string], string][]) {
      const c = revocationConfirmation(args[0], args[1], args[2], "left");
      expect([args, c.target, c.line, c.why]).toEqual([args, undefined, undefined, `The ${field} has spaces at its start or end; enter it without them.`]);
    }
  });

  test("the line keeps the spaces of the target, so two targets that differ in spaces read differently", () => {
    const line = (v: string) => renderToStaticMarkup(h(ConfirmationLine, { line: revocationConfirmation(v, "", "", "left").line! }));
    expect(line("alice  smith")).toContain("alice  smith");
    expect(line("alice  smith")).toContain('class="exact"');
    expect(line("alice  smith")).not.toBe(line("alice smith"));
    expect(readFileSync(new URL("./app.css", import.meta.url), "utf8")).toMatch(/\.exact\s*\{[^}]*white-space:\s*pre-wrap/);
  });

  test("the press stays off while the identity, the pair or the reason is missing or both forms are filled", () => {
      for (const args of [["", "", "", "left"], ["idn_a", "", "", "  "], ["idn_a", "https://i", "", "left"], ["", "https://i", "", "left"], ["idn_a", "", "s", "left"]] as [string, string, string, string][]) {
        const c = revocationConfirmation(args[0], args[1], args[2], args[3]);
        expect(c.why, args.join("|")).toBe("Name the identity, or the issuer and subject, and give a reason.");
        expect(c.target).toBeUndefined();
      }
    });
  });
});
