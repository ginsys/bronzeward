// The state catalogue (journeys §8, wireframe W11). Every state text is a status message, announced
// without moving focus. The write states (unknown, set aside, replayed, conflict) are exercised from
// the first write view (#257) on.

import type { ReactNode } from "react";
import { type Problem, type ReadResult, TRANSPORT_FAILURE } from "./request.ts";

export const WITHHELD = "Configuration not shown: it could not be redacted.";
export const unknown = (why: string) => `Unknown whether this was saved: ${why}.`;
export const UNKNOWN = unknown("the connection dropped");
export const SET_ASIDE =
  "Your sign-in or the installation's epoch changed. A change with an unknown outcome is pending: it may already have been committed. It was not repeated, and repeating it stays disabled.";
export const REPLAYED = "Already done earlier; showing that result.";
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
// request body. Any other answer is named by its status alone.
export function Refused({ problem, status, verb, testid }: { problem: Problem | null; status: number; verb: "read" | "saved"; testid?: string }) {
  if (problem === null) return <Status testid={testid}>Not {verb}: the server answered {status}.</Status>;
  const code = problem.type.slice("urn:bronzeward:problem:".length);
  const rule = typeof problem["rule"] === "string" ? ` (rule ${problem["rule"]})` : "";
  return (
    <Status testid={testid}>
      Not {verb}: {code}{rule}. {problem.title}{problem.detail !== undefined && `: ${problem.detail}`}
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

// [Repeat exactly] only while the page holds the request, for the same sign-in and epoch.
export const Unknown = ({ why, onRepeat, onShow, detail, onDismiss, testid }: Notice & { why?: string; onRepeat?: (() => void) | undefined }) => (
  <Status testid={testid} actions={<>{onRepeat !== undefined ? button("Repeat exactly", onRepeat) : button("Show current state", onShow)}{dismiss(onDismiss)}</>}>
    {why === undefined ? UNKNOWN : unknown(why)}{detail}
  </Status>
);

export const SetAside = ({ onShow, detail, onDismiss, testid }: Notice) => (
  <Status testid={testid} actions={<>{button("Repeat exactly", () => {}, true)}{button("Show current state", onShow)}{dismiss(onDismiss)}</>}>{SET_ASIDE}{detail}</Status>
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
