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
  // The next step points at a reference block a request's refusal shows and an operation's text does not.
  refers?: true;
}

const PREFIX = "urn:bronzeward:problem:";
export const codeOf = (p: Problem) => (p.type.startsWith(PREFIX) ? p.type.slice(PREFIX.length) : p.type);

const REPORT = "Report it with the reference below.";
const REFERS = { refers: true } as const;
const FAULT = "This page left out something the server requires. That is a fault in the page, not in what you entered.";

export const PROBLEM_WORDS: Readonly<Record<string, Words>> = {
  "invalid-request": { what: "The request was malformed, so the server did not act on it.", next: "Check what you entered and change it before sending again. If nothing looks wrong, report it with the reference below.", ...REFERS },
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
  "idempotency-key-required": { what: FAULT, next: REPORT, ...REFERS },
  "precondition-required": { what: FAULT, next: REPORT, ...REFERS },
  "precondition-failed": { what: "Someone else saved a newer version first.", next: "Reload, read the newer version, then decide." },
  "epoch-mismatch": { what: "The installation's recovery state changed since this page loaded.", next: "Reload the page, read what the server holds now, then decide." },
  "validation-failed": { what: "The server checked this and found it not valid.", next: "Change what it points at; the server's explanation follows.", explains: true },
  "epoch-superseded": { what: "This server process is out of date with the installation's recovery state, so it may not change anything.", next: "Ask an administrator to restart the server." },
  "internal-error": { what: "The server failed while handling the request.", next: REPORT, ...REFERS },
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
  // Shared fragments and profiles (persistence and API §3.1.1, ginsys/bronzeward#1193): the same causes and next steps as the
  // command line's, with the controls of this page for its flags.
  "validation-failed/library-reference": { what: "A shared document holds no secret reference, and this one does.", next: "A fragment that holds a secret stays in one cluster: save it there, without the Shared choice." },
  "validation-failed/library-removed": { what: "The shared name has no head to adopt.", next: "Bring it back with a shared edit (a fragment or profile saved as shared) and publish that, then adopt it. A cluster that adopted it keeps its revision." },
  "validation-failed/name-in-two-scopes": { what: "This cluster holds the name as its own and as a shared one.", next: "In the same draft remove one of them (the cluster's own, or Stop following the shared one), or copy the shared one into the cluster with the command line's detach, then publish again." },
  "validation-failed/library-pin-conflict": { what: "Two sources of this release pin different revisions of one fragment.", next: "Make them agree, by setting the profile that pins the older one again or by adopting the revision the other source pins, then publish again." },
  "validation-failed/detach-pin-not-in-view": { what: "A profile of this cluster pins a fragment at a revision the cluster does not hold.", next: "Adopt that fragment at the pinned revision, or set the profile again, then publish again." },
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

// A conflict that names a shared head (scope adoption): the adoption or the head the draft was based on moved. The head is
// named as the server gave it; a conflict of another scope is not in this sentence.
export function staleAdoption(p: { readonly [member: string]: unknown }): Words | undefined {
  const cs = p["conflicts"];
  const moved = (Array.isArray(cs) ? cs : []).flatMap((c: unknown) => {
    const m = typeof c === "object" && c !== null ? (c as Record<string, unknown>) : {};
    // The members are the server's and unchecked: only a string or a number is shown, a null is none, anything else unknown.
    const shown = (v: unknown) => (typeof v === "string" ? safeWord(v) : typeof v === "number" ? String(v) : v === null ? "none" : "unknown");
    return m["scope"] === "adoption" ? [`${shown(m["head"])} was at adoption ${shown(m["expected"])} and is at ${shown(m["actual"])}`] : [];
  });
  return moved.length === 0 ? undefined : { what: `The shared name moved after this draft was read (${moved.join("; ")}).`, next: "Read the draft again, adopt the name again in this draft and publish that." };
}

export function wordsFor(p: Problem): Words | undefined {
  const code = codeOf(p);
  if (code === "forbidden" && p["plan"] !== undefined) return PLAN_FORBIDDEN;
  if (code === "stale-input") return staleAdoption(p) ?? wordsOfCode(code);
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

// A failed operation's next step (§8.2, Failed operations): the row's `next`, led by a space, unless the step points at
// a server explanation or a reference block that an operation's text does not show.
export const operationNext = (w: Words | undefined): string => (w === undefined || w.explains === true || w.refers === true ? "" : ` ${w.next}`);

// A failed operation's error in words: the row's `what`, `more` (a detail of the same sentence), then its next step.
export function operationWords(r: { code?: string | undefined; rule?: string | undefined }, more = ""): string {
  const w = r.code === undefined ? undefined : wordsOfCode(r.code, r.rule);
  if (w === undefined) return `${NO_WORDS}${more}`;
  return `${w.what}${more}${operationNext(w)}`;
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
// §8.3): the rule, the input, the paths and the server's message, as safe text, shown as the server serves them. A fragment
// input is shown by the name and layer the server adds, with its position in brackets. The server redacts a message by
// template and searches the rest for exact copies of resolved strings of six bytes or more, so a shorter copy, or a copy
// of an integer or a boolean inside an authored literal, is not detected, and a message or a path may quote an authored
// literal from a fragment (compilation §8.3, §8.4). A run of tabs and line breaks in the message reads as one space, as
// Talos wraps its errors in them. The machine comes back as its identifier, for the caller to show by name.
const MAX_PATHS = 20;
// What the rule `invalid` means: Talos refused the composed configuration, so no one fragment is named.
const RULE_INVALID = "The configuration of the machine as a whole, its adopted configuration plus the fragments selected for it, did not pass Talos's validation; the server's message is Talos's.";
const INPUT = /^fragment\[(\d+)\]$/;
const ended = (s: string) => (/[.!?]$/.test(s) ? s : `${s}.`);
export function whyRefused(e: Record<string, unknown> | null | undefined): { facts: string; machine?: string } {
  const text = (k: string) => (typeof e?.[k] === "string" && e[k] !== "" ? (e[k] as string) : undefined);
  const rule = text("rule");
  const input = text("input");
  const message = text("message");
  const paths = Array.isArray(e?.["paths"]) ? e["paths"].filter((p): p is string => typeof p === "string") : [];
  const fragment = text("fragment");
  const layer = text("layer");
  const place = (n: string) => (fragment === undefined ? `the fragment at position ${n} (the first is 0)` : `the fragment ${safeWord(fragment)}${layer === undefined ? "" : ` in layer ${safeWord(layer)}`} (position ${n}, the first is 0)`);
  const at = input === undefined ? undefined : input === "base" ? "the base" : INPUT.test(input) ? place(INPUT.exec(input)![1]!) : safeWord(input);
  const more = paths.length > MAX_PATHS ? ` and ${paths.length - MAX_PATHS} more` : "";
  const facts = [
    rule === undefined ? "" : rule === "invalid" ? RULE_INVALID : `The rule: ${safeWord(rule)}.`,
    at === undefined ? "" : `The input: ${at}.`,
    paths.length === 0 ? "" : `The paths: ${paths.slice(0, MAX_PATHS).map((p) => safeWord(p)).join(", ")}${more}.`,
    message === undefined ? "" : `The server says: ${ended(safeSentence(message.replace(/[\t\n\r]+/g, " ")))}`,
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
function referenceLines(p: Problem, status: number | undefined): [string, string][] {
  const lines: [string, string][] = [...(status === undefined ? [] : [["Status", String(status)] as [string, string]]), ["Code", codeOf(p)]];
  if (typeof p["rule"] === "string") lines.push(["Rule", p["rule"]]);
  if (typeof p["scope"] === "string") lines.push(["Scope", p["scope"]]);
  if (p.instance !== undefined) lines.push(["Request", p.instance]);
  const earlier = earlierOf(p);
  if (earlier !== undefined) lines.push(["Earlier record", `request ${earlier.request}, epoch ${earlier.epoch}`]);
  const detail = visibleDetail(p);
  const said = [safeSentence(p.title), detail].filter((s) => s !== undefined && s !== "").join(": ");
  if (said !== "") lines.push(["What the server said", said]);
  return lines;
}

export const referenceText = (p: Problem, status: number | undefined) => referenceLines(p, status).map(([label, value]) => `${label}: ${value}`).join("\n");

// A problem document an operation stored, read as a `Problem` whose members may be absent: no status reads as none,
// never "undefined".
export function storedProblem(e: Record<string, unknown>): { problem: Problem; status: number | undefined } {
  const str = (k: string) => (typeof e[k] === "string" ? (e[k] as string) : undefined);
  const status = typeof e["status"] === "number" ? e["status"] : undefined;
  const detail = str("detail");
  const instance = str("instance");
  return { problem: { ...e, type: str("type") ?? "", title: str("title") ?? "", status: status ?? 0, ...(detail !== undefined && { detail }), ...(instance !== undefined && { instance }) }, status };
}

// The reference block: closed until opened, outside the status paragraph so the live region never reads it. A caller that
// holds the whole problem document passes it as `document`, shown as JSON inside the same section.
export function RequestRef({ problem, status, document }: { problem: Problem; status: number | undefined; document?: unknown }) {
  const lines = referenceLines(problem, status);
  return (
    <details data-testid="request-ref">
      <summary>Reference for a bug report</summary>
      <ul>{lines.map(([label, value]) => <li key={label}>{label}: {value}</li>)}</ul>
      {document !== undefined && <pre>{JSON.stringify(document, null, 2)}</pre>}
      <CopyButton text={referenceText(problem, status)} byHand={BY_HAND} />
    </details>
  );
}
