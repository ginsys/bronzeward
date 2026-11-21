// The state catalogue (journeys §8, wireframe W11). Every state text is a status message, announced
// without moving focus, except a server refusal, which is an alert. The write states (unknown, set aside, replayed, conflict) are exercised from
// the first write view (#257) on.

import { createContext, type ReactNode, useCallback, useContext, useEffect, useId, useRef, useState } from "react";
import { Icon } from "./icons.tsx";
import { codeOf, refusedAt, RequestRef, sentence, visibleDetail } from "./problems.tsx";
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
// A recorded instant in a list or record, 26 Oct 2026, 10:12; none when absent. The date and the time are
// formatted apart and joined here: one call with both styles lets the browser pick the joiner, and Safari
// writes " at " (ginsys/bronzeward#946).
export const when = (at: string | null | undefined) => {
  if (at == null) return "none";
  const d = new Date(at);
  return `${d.toLocaleDateString("en-GB", { dateStyle: "medium" })}, ${clock(d)}`;
};

// A page has one role="status" region, for progress and outcomes (design B25, B37). It is hidden
// from sight and mirrors the text of every status message the page shows, so a screen reader hears
// each message once through it. A reader going through the page in order meets the text twice: the
// accepted cost of a mirrored live region. The region sets aria-atomic to false, because a status
// role implies true and a change to one message would then announce every message again.
const Mirror = createContext<((id: string, text: string | undefined) => void) | undefined>(undefined);

export function StatusPage({ children }: { children: ReactNode }) {
  const [lines, setLines] = useState<Readonly<Record<string, string>>>({});
  const set = useCallback((id: string, text: string | undefined) => {
    setLines((was) => {
      if (was[id] === text || (text === undefined && !(id in was))) return was;
      const { [id]: _gone, ...rest } = was;
      return text === undefined ? rest : { ...rest, [id]: text };
    });
  }, []);
  return (
    <Mirror value={set}>
      <div role="status" aria-atomic="false" data-testid="page-status" className="sr-only">
        {Object.entries(lines).map(([id, text]) => <p key={id}>{text}</p>)}
      </div>
      {children}
    </Mirror>
  );
}

// `role` is "alert" for a server refusal, which a screen reader then announces at once. Any other
// message carries no role: the page's one status region mirrors it.
// `reference` sits beside the message and not inside it: a details element is not allowed in a paragraph, and the
// status region mirrors the paragraph's text only.
export function Status({ children, actions, reference, testid = "status", role = "status" }: { children: ReactNode; actions?: ReactNode; reference?: ReactNode; testid?: string | undefined; role?: "status" | "alert" | undefined }) {
  const mirror = useContext(Mirror);
  const id = useId();
  const shown = useRef<HTMLParagraphElement>(null);
  // Every render: the text can change without the message going.
  useEffect(() => {
    mirror?.(id, role === "status" ? (shown.current?.textContent ?? "") : undefined);
  });
  useEffect(() => () => mirror?.(id, undefined), [mirror, id]);
  // A child that changes on its own, such as a title arriving for an identifier, changes the text with no
  // render of the message: the mirror follows it.
  useEffect(() => {
    const el = shown.current;
    if (mirror === undefined || el === null || role !== "status") return;
    const watcher = new MutationObserver(() => mirror(id, el.textContent ?? ""));
    watcher.observe(el, { subtree: true, childList: true, characterData: true });
    return () => watcher.disconnect();
  }, [mirror, id, role]);
  return (
    <div className={role === "alert" ? "alert danger" : "alert"}>
      <Icon name={role === "alert" ? "error" : "info"} />
      <p ref={shown} role={role === "alert" ? "alert" : undefined} data-testid={testid}>{children}</p>
      {actions}
      {reference}
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

// A Bronzeward problem: a sentence per code, its detail, and its code, rule, scope and instance in a reference
// block (client contracts §8.2), never a request body. Any other answer is named by its status alone. A replayed refusal is the earlier
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

// A `409 recovery-mode-active` with a `scope` member is that scope's refusal, about one part of the
// installation. It says nothing about the process's start mode (client contracts §8.5 item 1, §8.2 409
// row), and the scope's identifier is in the reference. Any other problem, and a `scope` that is not text,
// add nothing. The process's own refusal has no `scope` member.
export const SCOPE_NO_MODE = "This answer says nothing about the start mode.";
export function scopeRefusal(code: string, problem: Problem): string {
  return code === "recovery-mode-active" && typeof problem["scope"] === "string" ? ` This applies to one part of the installation only. ${SCOPE_NO_MODE}` : "";
}

// A problem in words (client contracts §8.2): the sentence and its next step unless `next` is false, what the
// route accepts, then the server's own detail where one may be shown. The code, rule, scope and request
// number are in the reference.
export function problemSentences(p: Problem, status: number, next = true): string {
  const detail = visibleDetail(p);
  const said = detail === undefined ? "" : ` The server says: ${/[.!?]$/.test(detail) ? detail : `${detail}.`}`;
  return `${sentence(p, next)}${refusedAt(p)}${needs(status, p)}${scopeRefusal(codeOf(p), p)}${said}`;
}

export function Refused({ problem, status, verb, testid, replayed = false, cut = false }: { problem: Problem | null; status: number; verb: "read" | "saved"; testid?: string; replayed?: boolean; cut?: boolean }) {
  const earlier = replayed && `${REPLAYED} `;
  if (problem === null) return <Status testid={testid} role="alert">{earlier}Not {verb}: the server answered {status}.{cut && ` ${CUT_OFF}`}</Status>;
  return (
    <Status testid={testid} role="alert" reference={<RequestRef problem={problem} status={status} />}>
      {earlier}Not {verb}. {problemSentences(problem, status)}
    </Status>
  );
}

// The answer's problem, when it carries one.
export function problemFields(res: ReadResult<unknown>): Problem | undefined {
  return "problem" in res && res.problem !== null ? res.problem : undefined;
}

// The problem in words under a message of its own (a 403 row, W1, a revocation), with its reference. The message
// above it already says what the route accepts, so the sentences are asked for with no status.
export const ProblemFields = ({ problem, role, next = true }: { problem: Problem | undefined; role?: "alert"; next?: boolean }) =>
  problem === undefined ? null : (
    <>
      <p role={role} data-testid="problem">Not read. {problemSentences(problem, 0, next)}</p>
      <RequestRef problem={problem} status={problem.status} />
    </>
  );

export const Conflict = ({ what, revision, onShow, role }: { what: string; revision: number; onShow: () => void; role?: "status" | "alert" | undefined }) => (
  <Status role={role} actions={button("Show their change", onShow)}>Someone changed this {what} (revision {revision}). Your text is not saved.</Status>
);

// A write's notice (B11): `detail` names its key, route and the resource to read, and `onDismiss`
// ends it.
interface Notice { onShow: () => void; detail?: ReactNode; onDismiss?: (() => void) | undefined; testid?: string }
const dismiss = (onDismiss: (() => void) | undefined) => onDismiss !== undefined && button("Dismiss", onDismiss);

// [Repeat exactly] only while the page holds the request, for the same sign-in and epoch. While
// the repeat runs (`repeating`), the notice stays, with no second repeat and no dismissal.
export const REPEATING = " Repeating it exactly; no answer yet.";
// After a write whose outcome is unknown the notice says how to find out, and never to try again: the change may
// already be saved, and the problem's own next step would send the person to do it twice.
export const CHECK_STATE = "Check the current state before you do it again.";
const unsure = (problem: Problem | null) => ` ${CHECK_STATE}${problem === null ? "" : ` ${problemSentences(problem, problem.status, false)}`}`;
const reference = (problem: Problem | null) => (problem === null ? undefined : <RequestRef problem={problem} status={problem.status} />);

export const Unknown = ({ why, problem = null, onRepeat, onShow, detail, onDismiss, testid, repeating = false }: Notice & { why?: string; problem?: Problem | null; onRepeat?: (() => void) | undefined; repeating?: boolean }) => (
  <Status testid={testid} reference={reference(problem)} actions={<>{onRepeat !== undefined && !repeating ? button("Repeat exactly", onRepeat) : button("Show current state", onShow)}{!repeating && dismiss(onDismiss)}</>}>
    {why === undefined ? UNKNOWN : unknown(why)}{unsure(problem)}{repeating && REPEATING}{detail}
  </Status>
);

export const SetAside = ({ why, problem = null, onShow, detail, onDismiss, testid }: Notice & { why: string; problem?: Problem | null }) => (
  <Status testid={testid} reference={reference(problem)} actions={<>{button("Repeat exactly", () => {}, true)}{button("Show current state", onShow)}{dismiss(onDismiss)}</>}>{setAside(why)}{unsure(problem)}{detail}</Status>
);

// A [Repeat exactly] that settled: its result, until dismissed.
export const Repeated = ({ line, problem = null, onShow, detail, onDismiss, testid, role }: Notice & { line: string; problem?: Problem | null; role?: "status" | "alert" }) => (
  <Status testid={testid} role={role} reference={reference(problem)} actions={<>{button("Show current state", onShow)}{dismiss(onDismiss)}</>}>{line}{detail}</Status>
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

export const HumanOnly = () => <Status role="alert">{HUMAN_ONLY}</Status>;

// Text the operator has not sent stays on the page; signing in again is the operator's choice.
export const NotAccepted = ({ onSignIn }: { onSignIn: () => void }) => (
  <Status actions={button("Sign in again", onSignIn)}>{NOT_ACCEPTED}</Status>
);
