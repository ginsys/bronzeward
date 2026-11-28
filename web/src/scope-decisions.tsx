// The scope decisions of the recovery console (journeys §5.9, ginsys/bronzeward#365): mark and release, each on the scope's own row.
// One machine per press and no batch control. The row's own write layer instances keep the answers, so the console's reread, which
// moves the scope, does not drop them, as the drift page's controls do.

import { type ReactNode, useState } from "react";
import { AccountingForm, SCOPE_WHAT, accountedScope } from "./accounting.tsx";
import type { components } from "./api/v1";
import { Result, startsNew, useSend, useWrites } from "./forms.tsx";
import { Id } from "./list.tsx";
import type { Problem } from "./request.ts";
import { safeWord } from "./safe.ts";
import { Action } from "./shell.tsx";
import { Status } from "./states.tsx";
import { MARK_NEXT, MISSING_MEANING, MISSING_UNKNOWN, RELEASE_NOT_APPROVAL } from "./words.ts";

type MarkBody = components["schemas"]["ScopeMarkBody"];
type ReleaseBody = components["schemas"]["ScopeReleaseBody"];

export const marked = (data: unknown): ReactNode => {
  const x = data as MarkBody;
  return (
    <>
      Scope marked {safeWord(x.state)}, was {safeWord(x.from)} (act <Id id={x.act} />).{" "}
      {x.missing.map((k) => `Missing ${safeWord(k)}: ${MISSING_MEANING[k] ?? MISSING_UNKNOWN}. `)}
      {MARK_NEXT[x.state] ?? MARK_NEXT["blocked"]}
    </>
  );
};

export const released = (data: unknown): ReactNode => {
  const x = data as ReleaseBody;
  return <>Scope released, was {safeWord(x.from)} (act <Id id={x.act} />). {RELEASE_NOT_APPROVAL}</>;
};

// What each scope state needs next (T31), as text; the Decision column holds the controls (ginsys/bronzeward#365), the accounting of a
// pre-restore-unaccounted scope included.
export function nextStep(state: string): string {
  switch (state) {
    case "pre-restore-unaccounted":
      return "Account the scope: record that no request sent before the restore can still land.";
    case "ready":
      return "Release the scope. A release is not an approval.";
    case "released":
      return "None.";
    case "unresolved":
      return "Read the machine's operations. If one has not ended, it is classified after the normal restart: restart, then mark the scope again. If none holds the scope, mark it again now.";
    case "blocked":
      return "Mark the scope, then read its recorded cause: a scope stays blocked until it is marked. If the cause names a missing key or observation, repair that, or publish a new release for a lost key version.";
    default:
      return "None.";
  }
}

// The next step of a refused mark or release. Both refusals carry the scope's state as a field, so the step is that state's own
// (`nextStep`). A `ready` scope is refused a release only when no mark is recorded for it, so its step is to mark. The one refusal with
// no field, a Desired release that moved, is told by its text (recovery_marks.go, recovery_releases.go).
export function scopeNext(p: Problem | null): string | undefined {
  if (p === null) return undefined;
  if (typeof p.detail === "string" && p.detail.includes("Desired release changed")) return "Mark the scope again.";
  const state = p["scope"];
  if (typeof state !== "string") return undefined;
  return state === "ready" ? `Mark the scope, then release it. ${RELEASE_NOT_APPROVAL}` : nextStep(state);
}

export function ScopeDecision({ machine, state, stoppedAt }: { machine: string; state: string; stoppedAt?: string | undefined }) {
  const writes = useWrites()!;
  const [mark, sendMark] = useSend();
  const [release, sendRelease] = useSend();
  type W = typeof mark;
  // `resource` is the console: a 409 of these routes names the machine, which is no record to read instead.
  const send = (act: "marks" | "releases") => () =>
    (act === "marks" ? sendMark : sendRelease)({ method: "POST", path: `/recovery/scopes/${encodeURIComponent(machine)}/${act}`, body: {}, resource: "/recovery" });
  const busy = (w: W) => w !== undefined && !startsNew(writes, w);
  const next = (w: W) => (w?.outcome?.kind === "refused" ? scopeNext(w.outcome.problem) : undefined);
  const markable = state === "blocked" || state === "unresolved" || state === "ready";
  // The accounting form stays drawn once pressed, for its answer: the console's reread then shows the scope as accounted.
  const due = state === "pre-restore-unaccounted";
  const [started, setStarted] = useState(false);
  // The scope state the accounting's answer reported ("blocked until it is marked" or "unresolved"), once the answer is in.
  const [reported, setReported] = useState<string | undefined>();
  // The answer says what the scope is now, so it stays only while a read shows the scope in that state; until the answer is in, only
  // that the scope can still be marked.
  const keep = started && (reported === undefined ? markable : state === reported);
  return (
    <>
      {markable && <p><Action label="Mark" needs={["recovery-admin"]} onClick={send("marks")} busy={busy(mark)} variant="secondary" /></p>}
      <Result w={mark} done={marked} />
      {next(mark) !== undefined && <Status testid="write-next">{next(mark)}</Status>}
      {state === "ready" && <p><Action label="Release" needs={["recovery-admin"]} onClick={send("releases")} busy={busy(release)} variant="secondary" /></p>}
      <Result w={release} done={released} />
      {next(release) !== undefined && <Status testid="write-next">{next(release)}</Status>}
      {/* The accounting's answer says what the scope "is now"; once a mark or a release has been made that sentence is stale, so the form leaves then. A refused one leaves it while the scope is still as it said, and the form leaves when a read shows the scope in another state than the answer reported (another administrator marked or released it). A mark whose outcome is `unknown` (the answer never came) keeps the form, and so keeps the sentence "is now blocked", which may then be stale: that is accepted, because the next reread of the scopes replaces it. */}
      {(due || keep) &&mark?.outcome?.kind !== "done" && release?.outcome?.kind !== "done" && (
        <AccountingForm machine={machine} path="/recovery/accountings" resource="/recovery" what={SCOPE_WHAT} done={accountedScope} due={due} stoppedAt={stoppedAt} onStart={() => setStarted(true)} onAnswered={(d) => setReported((d as { scopes?: { state: string }[] } | null)?.scopes?.[0]?.state)} />
      )}
    </>
  );
}
