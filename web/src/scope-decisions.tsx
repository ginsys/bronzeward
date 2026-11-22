// The scope decisions of the recovery console (journeys §5.9, ginsys/bronzeward#365): mark and release, each on the scope's own row.
// One machine per press and no batch control. The row's own write layer instances keep the answers, so the console's reread, which
// moves the scope, does not drop them, as the drift page's controls do.

import type { ReactNode } from "react";
import type { components } from "./api/v1";
import { Result, startsNew, useSend, useWrites } from "./forms.tsx";
import { Id } from "./list.tsx";
import type { Problem } from "./request.ts";
import { Action } from "./shell.tsx";
import { Status } from "./states.tsx";
import { MARK_NEXT, MISSING_MEANING, MISSING_UNKNOWN, RELEASE_NOT_APPROVAL } from "./words.ts";

type MarkBody = components["schemas"]["ScopeMarkBody"];
type ReleaseBody = components["schemas"]["ScopeReleaseBody"];

const marked = (data: unknown): ReactNode => {
  const x = data as MarkBody;
  return (
    <>
      Scope marked {x.state}, was {x.from} (act <Id id={x.act} />).{" "}
      {x.missing.map((k) => `Missing ${k}: ${MISSING_MEANING[k] ?? MISSING_UNKNOWN}. `)}
      {MARK_NEXT[x.state] ?? MARK_NEXT["blocked"]}
    </>
  );
};

const released = (data: unknown): ReactNode => {
  const x = data as ReleaseBody;
  return <>Scope released, was {x.from} (act <Id id={x.act} />). {RELEASE_NOT_APPROVAL}</>;
};

// What each scope state needs next (T31), as text; the Decision column holds the controls (ginsys/bronzeward#365). The accounting of a
// pre-restore-unaccounted scope has none there yet.
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

export function ScopeDecision({ machine, state }: { machine: string; state: string }) {
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
  return (
    <>
      {markable && <p><Action label="Mark" needs={["recovery-admin"]} onClick={send("marks")} busy={busy(mark)} /></p>}
      <Result w={mark} done={marked} />
      {next(mark) !== undefined && <Status testid="write-next">{next(mark)}</Status>}
      {state === "ready" && <p><Action label="Release" needs={["recovery-admin"]} onClick={send("releases")} busy={busy(release)} /></p>}
      <Result w={release} done={released} />
      {next(release) !== undefined && <Status testid="write-next">{next(release)}</Status>}
    </>
  );
}
