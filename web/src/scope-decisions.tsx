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

// The next step of a refused mark or release. The server's 409 conflict on these routes has no shared next step (the command adds
// its own, cmd/bronzewardctl/recovery_scope.go), so this adds it by the refusal's detail.
export function scopeNext(p: Problem | null): string | undefined {
  if (p === null) return undefined;
  const detail = typeof p.detail === "string" ? p.detail : "";
  if (p["scope"] === "pre-restore-unaccounted") return "Account the scope first: it leaves that state only by the accounting.";
  if (detail.includes("only a ready scope is released")) return `Mark the scope until it is ready, then release it. ${RELEASE_NOT_APPROVAL}`;
  if (detail.includes("Desired release changed")) return "Mark the scope again.";
  if (detail.includes("is not marked")) return "Read the scope's state and its next step on this page.";
  return undefined;
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
