// The state catalogue (journeys §8, wireframe W11). Every state text is a status message, announced
// without moving focus. The write states (unknown, set aside, replayed, conflict) are exercised from
// the first write view (#257) on.

import type { ReactNode } from "react";
import { type Problem, type ReadResult, TRANSPORT_FAILURE, WRITE_TRANSPORT_FAILURE } from "./request.ts";

export const WITHHELD = "Configuration not shown: it could not be redacted.";
export const unknown = (why: string) => `Unknown whether this was saved: ${why}.`;
export const UNKNOWN = unknown(WRITE_TRANSPORT_FAILURE);
// A write set aside after an attempt that may have committed: the reason it was set aside, then
// SET_ASIDE.
export const SET_ASIDE =
  "A change with an unknown outcome is pending: it may already have been committed. It was not repeated, and repeating it stays disabled.";
export const setAside = (why: string) => `Set aside: ${why}. ${SET_ASIDE}`;
export const REPLAYED = "Already done earlier; showing that result.";
// A 4xx write answer whose body the network or the attempt bound cut off (§8.4 item 8).
export const CUT_OFF = "The answer was cut off, so its reason could not be read.";
export const HUMAN_ONLY = "This action needs a person signed in; automation tokens cannot approve.";
export const NOT_ACCEPTED = "Not accepted by this installation. Sign in again.";

// A time as the wireframes show it, 10:12.
export const clock = (d: Date) => d.toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit" });
// A recorded instant in a list or record, 26 Oct 2026, 10:12; none when absent.
export const when = (at: string | null | undefined) => (at == null ? "none" : new Date(at).toLocaleString("en-GB", { dateStyle: "medium", timeStyle: "short" }));

export function Status({ children, actions, testid = "status" }: { children: ReactNode; actions?: ReactNode; testid?: string | undefined }) {
  return (
    <div>
      <p role="status" data-testid={testid}>{children}</p>
      {actions}
    </div>
  );
}

const button = (label: string, onClick: () => void, disabled = false) => (
  <button type="button" onClick={onClick} disabled={disabled}>{label}</button>
);

export const Loading = ({ what }: { what: string }) => <Status>Loading {what}</Status>;

// A list with no items, with the first action when there is one.
export const Empty = ({ text, action }: { text: string; action?: ReactNode }) => <Status actions={action}>{text}</Status>;

export const Withheld = () => <Status>{WITHHELD}</Status>;

// An observation not read, with the cause per field; never shown as success.
export const Unread = ({ at, causes }: { at: Date; causes: string[] }) => (
  <Status>Observed: not read at {clock(at)} — {causes.join("; ")}.</Status>
);

// A Bronzeward problem: its code, rule, title, detail and instance (client contracts §8.2), never a
// request body. Any other answer is named by its status alone. A replayed refusal is the earlier
// attempt's, and says so (§8.4 item 2).
// A 403 forbidden names what the route accepts (§8.2 403 row): "humans only" when `humanOnly` is
// true, else its `roles`. The plan cancellation's creator check, which carries `plan`, says it in
// its `detail` instead.
export function needs(status: number, problem: Problem): string {
  if (status !== 403 || problem["plan"] !== undefined) return "";
  if (problem["humanOnly"] === true) return ` ${HUMAN_ONLY}`;
  const roles = problem["roles"];
  return Array.isArray(roles) && roles.length > 0 && roles.every((r) => typeof r === "string") ? ` Needs ${roles.join(" or ")}.` : "";
}

// A problem's title, its detail when present and its instance (§8.2), as one sentence or two.
export const problemLine = (p: Problem) => `${p.title}${p.detail !== undefined ? `: ${p.detail}` : ""}.${p.instance !== undefined ? ` Request ${p.instance}.` : ""}`;
// An earlier-epoch idempotency record's 409 (§8.2 409 row, §8.4 item 7): the request it recorded
// and that request's epoch, which the operator reads and reports.
export const earlierRecord = (p: Problem) =>
  p.status === 409 && typeof p["request"] === "string" && typeof p["epoch"] === "string" ? ` The key's earlier record: request ${p["request"]}, epoch ${p["epoch"]}.` : "";
// The problem an unknown or set-aside write's last answer carried, after its sentence: what the
// route accepts (a 403's), then its fields. Nothing when no answer carried one.
export const problemTail = (p: Problem | null) => (p === null ? "" : `${needs(p.status, p)} ${problemLine(p)}${earlierRecord(p)}`);

// A `409 recovery-mode-active` with a `scope` member is that scope's refusal. It says nothing about
// the process's start mode (client contracts §8.5 item 1, §8.2 409 row). Any other problem, and a
// `scope` that is not text, add nothing. The process's own refusal has no `scope` member.
export const SCOPE_NO_MODE = "This answer says nothing about the start mode.";
export function scopeRefusal(code: string, problem: Problem): string {
  const scope = problem["scope"];
  return code === "recovery-mode-active" && typeof scope === "string" ? ` Scope ${scope} refuses this request. ${SCOPE_NO_MODE}` : "";
}

// A problem's `position` member when it is a whole number (a mark refused by its index).
const positionOf = (p: Problem): number | undefined => {
  const n = p["position"];
  return typeof n === "number" && Number.isSafeInteger(n) && n >= 0 ? n : undefined;
};

export function Refused({ problem, status, verb, testid, replayed = false, cut = false }: { problem: Problem | null; status: number; verb: "read" | "saved"; testid?: string; replayed?: boolean; cut?: boolean }) {
  const earlier = replayed && `${REPLAYED} `;
  if (problem === null) return <Status testid={testid}>{earlier}Not {verb}: the server answered {status}.{cut && ` ${CUT_OFF}`}</Status>;
  const code = problem.type.slice("urn:bronzeward:problem:".length);
  const rule = typeof problem["rule"] === "string" ? ` (rule ${problem["rule"]})` : "";
  // A refused mark is named by its position in the request, never its path (§8.2 422 row).
  const at = problem.status === 422 && positionOf(problem) !== undefined ? ` The mark at position ${positionOf(problem)} (the first is 0) is refused.` : "";
  return (
    <Status testid={testid}>
      {earlier}Not {verb}: {code}{rule}.{at}{needs(status, problem)}{scopeRefusal(code, problem)} {problem.title}{problem.detail !== undefined && `: ${problem.detail}`}
      {problem.instance !== undefined && ` Request ${problem.instance}.`}
    </Status>
  );
}

// §8.2: a problem's title, its detail when present and its instance, never a request body. The
// authentication 503's detail is W1's text.
export function problemText(res: ReadResult<unknown>): string {
  const p = "problem" in res ? res.problem : null;
  if (p === null) return `Not read: the server answered ${"status" in res ? res.status : res.kind}.`;
  return `Not read: ${p.title}${p.detail !== undefined ? ` (${p.detail})` : ""}.${p.instance !== undefined ? ` Request ${p.instance}.` : ""}`;
}

// The answer's problem, when it carries one.
export function problemFields(res: ReadResult<unknown>): string | undefined {
  return "problem" in res && res.problem !== null ? problemText(res) : undefined;
}

// The problem fields under a message of their own (a 403 row, W1, a revocation).
export const ProblemFields = ({ text }: { text: string | undefined }) => (text === undefined ? null : <p data-testid="problem">{text}</p>);

export const Conflict = ({ what, revision, onShow }: { what: string; revision: number; onShow: () => void }) => (
  <Status actions={button("Show their change", onShow)}>Someone changed this {what} (revision {revision}). Your text is not saved.</Status>
);

// A write's notice (B11): `detail` names its key, route and the resource to read, and `onDismiss`
// ends it.
interface Notice { onShow: () => void; detail?: ReactNode; onDismiss?: (() => void) | undefined; testid?: string }
const dismiss = (onDismiss: (() => void) | undefined) => onDismiss !== undefined && button("Dismiss", onDismiss);

// [Repeat exactly] only while the page holds the request, for the same sign-in and epoch. While
// the repeat runs (`repeating`), the notice stays, with no second repeat and no dismissal.
export const REPEATING = " Repeating it exactly; no answer yet.";
export const Unknown = ({ why, problem = null, onRepeat, onShow, detail, onDismiss, testid, repeating = false }: Notice & { why?: string; problem?: Problem | null; onRepeat?: (() => void) | undefined; repeating?: boolean }) => (
  <Status testid={testid} actions={<>{onRepeat !== undefined && !repeating ? button("Repeat exactly", onRepeat) : button("Show current state", onShow)}{!repeating && dismiss(onDismiss)}</>}>
    {why === undefined ? UNKNOWN : unknown(why)}{problemTail(problem)}{repeating && REPEATING}{detail}
  </Status>
);

export const SetAside = ({ why, problem = null, onShow, detail, onDismiss, testid }: Notice & { why: string; problem?: Problem | null }) => (
  <Status testid={testid} actions={<>{button("Repeat exactly", () => {}, true)}{button("Show current state", onShow)}{dismiss(onDismiss)}</>}>{setAside(why)}{problemTail(problem)}{detail}</Status>
);

// A [Repeat exactly] that settled: its result, until dismissed.
export const Repeated = ({ line, onShow, detail, onDismiss, testid }: Notice & { line: string }) => (
  <Status testid={testid} actions={<>{button("Show current state", onShow)}{dismiss(onDismiss)}</>}>{line}{detail}</Status>
);

export const Replayed = () => <Status>{REPLAYED}</Status>;

// A banner over the page's last data.
export const Offline = ({ since, readAt }: { since: Date; readAt: Date | undefined }) => (
  <Status testid="offline">
    Offline since {clock(since)}. {readAt !== undefined ? `Showing data read at ${clock(readAt)}. ` : ""}Actions are disabled.
  </Status>
);

// No answer at all: the network, or a redirect the page refuses (client contracts §6 item 7).
export const Disconnected = ({ testid }: { testid?: string }) => <Status testid={testid}>{TRANSPORT_FAILURE}</Status>;

export const HumanOnly = () => <Status>{HUMAN_ONLY}</Status>;

// Text the operator has not sent stays on the page; signing in again is the operator's choice.
export const NotAccepted = ({ onSignIn }: { onSignIn: () => void }) => (
  <Status actions={button("Sign in again", onSignIn)}>{NOT_ACCEPTED}</Status>
);
