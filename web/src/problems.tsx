// Every server refusal in words (client contracts §8.2, ginsys/bronzeward#1049): one sentence and one next
// step per problem code, and a reference block that keeps the code, rule, scope and request number for
// a bug report, with a control that copies them. The CLI prints its own text.

import { CopyButton } from "./copy.tsx";
import type { Problem } from "./request.ts";
import { safeSentence, safeWord } from "./safe.ts";

export interface Words {
  what: string;
  next: string;
  // The next step points at the server's own detail, so it is shown only where a detail is.
  explains?: true;
}

const PREFIX = "urn:bronzeward:problem:";
export const codeOf = (p: Problem) => (p.type.startsWith(PREFIX) ? p.type.slice(PREFIX.length) : p.type);

const REPORT = "Report it with the reference below.";
const FAULT = "This page left out something the server requires. That is a fault in the page, not in what you entered.";

export const PROBLEM_WORDS: Readonly<Record<string, Words>> = {
  "invalid-request": { what: "The request was malformed, so the server did not act on it.", next: "Check what you entered and change it before sending again. If nothing looks wrong, report it with the reference below." },
  "cursor-invalid": { what: "The list position this page asked for is no longer valid.", next: "Reload the list from the start." },
  unauthenticated: { what: "The server did not accept your sign-in.", next: "Sign in again." },
  forbidden: { what: "Your account is not allowed to do this.", next: "Ask someone who has the right role." },
  "identity-revoked": { what: "Your access was withdrawn.", next: "Ask an administrator; signing in again will not help." },
  "not-found": { what: "This does not exist on this server.", next: "Go back to the list and check that it is still there." },
  conflict: { what: "This is in a state that does not allow the change.", next: "Read its current state before you decide what to do." },
  "stale-input": { what: "Something this change depends on changed, or was not read yet.", next: "Reload, read the current state, then decide whether to make the change again." },
  "recovery-mode-active": { what: "The server refuses this until a recovery is finished.", next: "Wait for an administrator to finish the recovery." },
  "scope-busy": { what: "Another operation is working on the same part of the installation.", next: "Wait for it to finish, then try again." },
  "ingestion-abandoned": { what: "The import was abandoned before it finished, and nothing it read was kept.", next: "Start the import again." },
  "recovery-mode-entered": { what: "The server went into recovery while this was publishing, so nothing was published.", next: "Wait for the recovery to finish, then publish again." },
  "idempotency-key-reused": { what: "The server could not match this to the earlier request sent under the same key.", next: "Reload and check whether it was saved before you do it again." },
  "idempotency-key-required": { what: FAULT, next: REPORT },
  "precondition-required": { what: FAULT, next: REPORT },
  "precondition-failed": { what: "Someone else saved a newer version first.", next: "Reload, read the newer version, then decide." },
  "epoch-mismatch": { what: "The installation's recovery state changed since this page loaded.", next: "Reload the page, read what the server holds now, then decide." },
  "validation-failed": { what: "The server checked this and found it not valid.", next: "Change what it points at; the server's explanation follows.", explains: true },
  "epoch-superseded": { what: "This server process is out of date with the installation's recovery state, so it may not change anything.", next: "Ask an administrator to restart the server." },
  "internal-error": { what: "The server failed while handling the request.", next: REPORT },
  "not-implemented": { what: "The server does not offer this yet.", next: "Nothing can be done from this page." },
  "transient-conflict": { what: "The server gave up for now because of a clashing change.", next: "Try again in a moment." },
  "dependency-unavailable": { what: "Something the server depends on is not available or not set up.", next: "Try again in a moment; if it keeps happening, ask an administrator." },
  "name-in-use": { what: "Another machine in this cluster already has this name.", next: "Choose a different name." },
  "machine-identity-mismatch": { what: "The machine at that address could not be confirmed as the one on record.", next: "Check which machine answers at that address; nothing it read was kept." },
  "talos-access-unavailable": { what: "The server could not work with Talos for this machine or cluster.", next: "The server's explanation follows.", explains: true },
};

// A rule narrows a code: the same code with a different cause that the general sentence would misstate.
export const RULE_WORDS: Readonly<Record<string, Words>> = {
  // A client cannot tell from GET /server whether the server shows an import read from a machine (the API revision is the same
  // before and after that change), so both sentences stay true for a server that still refuses one.
  "conflict/import-not-reviewed": { what: "The server did not show you this import's text. It shows a file import only to the person who continued it after its review, and an import read from a machine only if the server is new enough to show it.", next: "Nothing needs doing: the import itself is not affected. If this import was read from a machine, ask whoever runs the server whether it is up to date." },
  "forbidden/not-the-importer": { what: "The server did not show you this import's text. It shows a file import only to the person who imported it, and an import read from a machine only if the server is new enough to show it.", next: "Ask that person. If this import was read from a machine, ask whoever runs the server whether it is up to date." },
};

// The plan cancellation's creator check carries a `plan` member, and its detail is the explanation (§8.2 403
// row): the caller holds the route's roles, so "ask someone with the right role" would mislead.
const PLAN_FORBIDDEN: Words = { what: "You may not do this to this plan.", next: "The server's explanation follows.", explains: true };

// The words for a code, narrowed by its rule where the table has one. An operation's error and a refused mark
// carry a code and a rule but are no `Problem`, so they read the same two tables through this.
export function wordsOfCode(code: string, rule?: string): Words | undefined {
  const narrowed = rule !== undefined && Object.hasOwn(RULE_WORDS, `${code}/${rule}`) ? RULE_WORDS[`${code}/${rule}`] : undefined;
  return narrowed ?? (Object.hasOwn(PROBLEM_WORDS, code) ? PROBLEM_WORDS[code] : undefined);
}

export function wordsFor(p: Problem): Words | undefined {
  const code = codeOf(p);
  if (code === "forbidden" && p["plan"] !== undefined) return PLAN_FORBIDDEN;
  const rule = p["rule"];
  return wordsOfCode(code, typeof rule === "string" ? rule : undefined);
}

// What the import page says where the table has no words for a code. The code is not lost: the events listed on
// the same page name it, for a bug report.
export const NO_WORDS = "The server gave a reason this page has no words for. The events listed on this page name it.";

// A refusal or an error in words for the import page's sentences: never the code or the rule, and a refused mark
// by its position only, never its path (§8.2 422 row).
export function refusalWords(r: { code?: string | undefined; rule?: string | undefined; position?: number | undefined }): string {
  const w = r.code === undefined ? undefined : wordsOfCode(r.code, r.rule);
  const at = r.position === undefined ? "" : ` The mark at position ${r.position} (the first is 0) is refused.`;
  return `${w === undefined ? NO_WORDS : w.what}${at}`;
}

// A problem's `position` member when it is a whole number (a mark refused by its index).
const positionOf = (p: Problem): number | undefined => {
  const n = p["position"];
  return typeof n === "number" && Number.isSafeInteger(n) && n >= 0 ? n : undefined;
};
// A refused mark is named by its position in the request, never its path (§8.2 422 row): its detail
// is left out of every notice, the reference and the copy, because the server's text may name the path.
const VALIDATION_FAILED = `${PREFIX}validation-failed`;
export const refusedAt = (p: Problem) => (p.status === 422 && p.type === VALIDATION_FAILED && positionOf(p) !== undefined ? ` The mark at position ${positionOf(p)} (the first is 0) is refused.` : "");

// The detail a person may read: none where it is absent or hidden for a refused mark.
export const visibleDetail = (p: Problem): string | undefined => (p.detail !== undefined && refusedAt(p) === "" ? safeSentence(p.detail) : undefined);

// Why a configuration was refused, from the problem document a failed publication or import carries (compilation
// §8.3): the rule, the input, the paths and the server's message, as safe text. The server redacts the paths and the
// message, so neither holds a value. The machine comes back as its identifier, for the caller to show by name.
const MAX_PATHS = 20;
const INPUT = /^fragment\[(\d+)\]$/;
const ended = (s: string) => (/[.!?]$/.test(s) ? s : `${s}.`);
export function whyRefused(e: Record<string, unknown> | null | undefined): { facts: string; machine?: string } {
  const text = (k: string) => (typeof e?.[k] === "string" && e[k] !== "" ? (e[k] as string) : undefined);
  const rule = text("rule");
  const input = text("input");
  const message = text("message");
  const paths = Array.isArray(e?.["paths"]) ? e["paths"].filter((p): p is string => typeof p === "string") : [];
  const at = input === undefined ? undefined : input === "base" ? "the base" : INPUT.test(input) ? `the fragment at position ${INPUT.exec(input)![1]} (the first is 0)` : safeWord(input);
  const more = paths.length > MAX_PATHS ? ` and ${paths.length - MAX_PATHS} more` : "";
  const facts = [
    rule === undefined ? "" : `The rule: ${safeWord(rule)}.`,
    at === undefined ? "" : `The input: ${at}.`,
    paths.length === 0 ? "" : `The paths: ${paths.slice(0, MAX_PATHS).map((p) => safeWord(p)).join(", ")}${more}.`,
    message === undefined ? "" : `The server says: ${ended(safeSentence(message))}`,
  ].filter((s) => s !== "").join(" ");
  const machine = text("machine");
  return { facts, ...(machine !== undefined && { machine }) };
}

// An earlier-epoch idempotency record's 409 (§8.2 409 row, §8.4 item 7): the request it recorded and that request's epoch.
export const earlierOf = (p: Problem): { request: string; epoch: string } | undefined =>
  p.status === 409 && typeof p["request"] === "string" && typeof p["epoch"] === "string" ? { request: p["request"], epoch: p["epoch"] } : undefined;

// The sentence for a problem, and its next step unless `next` is false or the step points at a detail that is not shown.
export function sentence(p: Problem, next = true): string {
  const detail = visibleDetail(p);
  const w = wordsFor(p);
  if (w === undefined) return `${safeSentence(p.title)}.`;
  return next && (w.explains !== true || detail !== undefined) ? `${w.what} ${w.next}` : w.what;
}

const BY_HAND = "Select the text above to copy it.";

// The labelled lines of the reference, in the order a bug report wants them.
function referenceLines(p: Problem, status: number): [string, string][] {
  const lines: [string, string][] = [["Status", String(status)], ["Code", codeOf(p)]];
  if (typeof p["rule"] === "string") lines.push(["Rule", p["rule"]]);
  if (typeof p["scope"] === "string") lines.push(["Scope", p["scope"]]);
  if (p.instance !== undefined) lines.push(["Request", p.instance]);
  const earlier = earlierOf(p);
  if (earlier !== undefined) lines.push(["Earlier record", `request ${earlier.request}, epoch ${earlier.epoch}`]);
  const detail = visibleDetail(p);
  lines.push(["What the server said", detail === undefined ? safeSentence(p.title) : `${safeSentence(p.title)}: ${detail}`]);
  return lines;
}

export const referenceText = (p: Problem, status: number) => referenceLines(p, status).map(([label, value]) => `${label}: ${value}`).join("\n");

// The reference block: closed until opened, outside the status paragraph so the live region never reads it.
export function RequestRef({ problem, status }: { problem: Problem; status: number }) {
  const lines = referenceLines(problem, status);
  return (
    <details data-testid="request-ref">
      <summary>Reference for a bug report</summary>
      <ul>{lines.map(([label, value]) => <li key={label}>{label}: {value}</li>)}</ul>
      <CopyButton text={referenceText(problem, status)} byHand={BY_HAND} />
    </details>
  );
}
