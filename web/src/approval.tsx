// Approval and its revocation on the plan page (ginsys/bronzeward#358; decisions B21, B22 and B26 of
// ginsys/bronzeward#259; journeys §5.6 and the approver role; W7). Before approval the page predicts the
// self-approval reasons its reads can show, in W7's words; the returned approval owns the actual
// reasons. Approval is confirmed by typing the plan ID back.

import { type FormEvent, type ReactNode, useEffect, useRef, useState } from "react";
import type { components } from "./api/v1";
import { Result, startsNew, useSend, useWrites } from "./forms.tsx";
import { type Load, Id, short, useItem, useReads } from "./list.tsx";
import { REASON_MAX, reasonProblem } from "./plan.tsx";
import { Action } from "./shell.tsx";
import { Status } from "./states.tsx";

type S = components["schemas"];
type Plan = S["PlanBody"];
type Release = S["ReleaseBody"];
type Draft = S["DraftBody"];
type Approval = S["ApprovalBody"];

const enc = encodeURIComponent;

// W7's fixed tail: what no read can predict.
export const UNPREDICTED = "Import-base authorship and automation you own are not predicted. The approval records the actual reasons. Allowed and marked.";
// W7's approved variant, on the revoke control (B22).
export const REVOKE_LIMIT = "Revoking after the controller commits does not stop an attempt already recorded.";
export const afterApproval = (p: Plan) => p.operation === "adopt"
  ? `The controller records the adoption without sending anything; Applied becomes ${short(p.release)} (adoption) once it does.`
  : "The controller re-checks the plan at use time; it applies it only if every check passes.";
// An apply-config plan bound to a drift record overwrites a configuration no reviewer has seen.
export const overwrites = (p: Plan) => p.operation === "apply-config" && p.driftRecord !== null
  ? `Overwrites the drifted configuration of drift record ${p.driftRecord}, which no reviewer has seen. Approving records that you approve overwriting it.`
  : undefined;
export const recordedReasons = (a: Approval) => `Recorded self-approval reasons: ${a.selfApproval.reasons.length > 0 ? a.selfApproval.reasons.join(", ") : "none"}.`;

// A revision a release contains, with what its read answered.
export type Source = { kind: string; revision: string; load: Load<{ author: string }> };

// The prediction by these reads: one clause per reason, in W7's wording, and a line for each read in
// flight or failed. A reason whose read has not answered is not claimed either way, so the approver
// is never shown as clear of self-approval.
export function predict(p: Plan, me: Load<{ principal: string | null }>, release: Load<Release>, draft: Load<Draft> | undefined, sources: readonly Source[]): string[] {
  const yet: string[] = [], failed: string[] = [];
  const note = (l: Load<unknown>, what: string) => {
    if (l.kind === "loading") yet.push(what);
    else if (l.kind !== "ok") failed.push(what);
  };
  note(me, "your identity");
  note(release, `release ${short(p.release)}`);
  if (release.kind === "ok" && draft !== undefined) note(draft, `the release's draft ${short(release.data.draft)}`);
  for (const s of sources) note(s.load, `${s.kind} revision ${short(s.revision)}`);
  const clauses: string[] = [];
  const who = me.kind === "ok" ? me.data.principal : null;
  if (who !== null) {
    const created = p.createdBy.principal === who;
    if (release.kind !== "ok") clauses.push(created ? "you created this plan" : "you did not create this plan");
    else {
      const published = release.data.publishedBy.principal === who;
      clauses.push(created
        ? published ? "you created this plan and published this release" : "you created this plan but did not publish this release"
        : published ? "you did not create this plan but published this release" : "you did not create this plan or publish this release");
      const mine = sources.filter((s) => s.load.kind === "ok" && s.load.data.author === who);
      const named = (l: readonly Source[]) => l.map((s) => `${s.kind} revision ${short(s.revision)}`).join(", ");
      if (mine.length > 0 && draft?.kind === "ok") {
        const introduced = new Set(draft.data.entries.map((e) => e.revision));
        const change = mine.filter((s) => introduced.has(s.revision)), reused = mine.filter((s) => !introduced.has(s.revision));
        if (change.length > 0) clauses.push(`you authored ${named(change)}, which this release's draft introduced`);
        if (reused.length > 0) clauses.push(`you authored ${named(reused)}, which this release reuses`);
      } else if (mine.length > 0) clauses.push(`you authored ${named(mine)} in it`);
      else if (sources.every((s) => s.load.kind === "ok")) clauses.push("you authored no fragment, profile or assignment revision in it");
    }
  }
  const lines = [clauses.length > 0 ? `Self-approval, predicted: ${clauses.join(", and ")}.` : "Self-approval is not predicted."];
  if (yet.length > 0) lines.push(`Not predicted yet: ${yet.join(", ")} ${yet.length > 1 ? "are" : "is"} being read.`);
  if (failed.length > 0) lines.push(`Not predicted, since this read failed: ${failed.join(", ")}.`);
  lines.push(UNPREDICTED);
  return lines;
}

// Reads each path, keyed by the paths together; each answer settles its own entry.
function useAll<T>(paths: readonly string[]): ReadonlyMap<string, Load<T>> {
  const { client, settle, undropped, again, view } = useReads();
  const [got, setGot] = useState<ReadonlyMap<string, Load<T>>>(new Map());
  const has = useRef(new Set<string>());
  const key = paths.join("\n");
  useEffect(() => {
    const ok = undropped();
    let live = true;
    for (const path of key === "" ? [] : key.split("\n")) {
      const put = (r: Load<T>) => {
        if (r.kind === "ok") has.current.add(path);
        else has.current.delete(path);
        setGot((m) => new Map(m).set(path, r));
      };
      void client.read<T>(path).then((res) => settle(res, put, () => live && ok(), () => has.current.has(path)));
    }
    return () => void (live = false);
  }, [client, settle, undropped, again, view, key]);
  return new Map(paths.map((p) => [p, got.get(p) ?? { kind: "loading" }]));
}

function Prediction({ p }: { p: Plan }) {
  const me = useItem<{ principal: string | null }>("/identity");
  const release = useItem<Release>(`/releases/${enc(p.release)}`);
  const rel = release.kind === "ok" ? release.data : undefined;
  const revs = (rel?.sources ?? []).flatMap((s) => (s.revision === null ? [] : [{ kind: s.kind, revision: s.revision }]));
  const path = (r: { kind: string; revision: string }) => `/${r.kind}-revisions/${enc(r.revision)}`;
  const draftPath = rel === undefined ? undefined : `/drafts/${enc(rel.draft)}`;
  const all = useAll<unknown>([...(draftPath === undefined ? [] : [draftPath]), ...revs.map(path)]);
  const sources = revs.map((r) => ({ ...r, load: all.get(path(r)) as Load<{ author: string }> }));
  const lines = predict(p, me, release, draftPath === undefined ? undefined : (all.get(draftPath) as Load<Draft>), sources);
  return <Status testid="self-approval">{lines.map((l, i) => <span key={i}>{i > 0 && " "}{l}</span>)}</Status>;
}

// [Approve] (B21): the prediction, then the plan ID typed back. The button waits for the ID; the
// submit checks only the ID, so a role the page disables still reaches the server, whose refusal is
// authoritative. Once the plan is no longer proposed, only the result of the approval that ended it
// stays shown. `reread` reads the plan again.
export function ApproveControl({ p, reread }: { p: Plan; reread: () => void }) {
  const writes = useWrites()!;
  const [w, send] = useSend();
  const [typed, setTyped] = useState("");
  const done = w?.outcome?.kind === "done";
  useEffect(() => {
    if (done) reread();
  }, [done, reread]);
  const recorded = (data: unknown) => {
    const a = data as Approval;
    return <>Approved (<Id id={a.id} />). {recordedReasons(a)} {afterApproval(p)}</>;
  };
  const submit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    if (!done && typed.trim() === p.id) send({ method: "POST", path: `/plans/${enc(p.id)}/approvals`, body: {}, resource: `/plans/${enc(p.id)}` });
  };
  if (p.state !== "proposed") return <Result w={w} done={recorded} />;
  const over = overwrites(p);
  const busy = (w !== undefined && !startsNew(writes, w)) || done;
  return (
    <section aria-label="Approval">
      <Prediction p={p} />
      {over !== undefined && <Status testid="unseen-overwrite">{over}</Status>}
      <form onSubmit={submit} aria-label="Approve plan">
        <p><label>To approve, type the plan ID ({p.id}): <input name="plan" autoComplete="off" value={typed} onChange={(e) => setTyped(e.target.value)} disabled={busy} /></label> Approval is for a human sign-in only.</p>
        <p><Action label="Approve" needs={["approver"]} submit busy={busy || typed.trim() !== p.id} /></p>
        <Result w={w} done={recorded} />
      </form>
    </section>
  );
}

// [Revoke approval] (B22): approver or recovery-admin, for an approved or committed plan's approval.
export function RevokeControl({ p, reread }: { p: Plan; reread: () => void }) {
  const writes = useWrites()!;
  const [note, setNote] = useState<string | undefined>();
  const [w, send] = useSend();
  const done = w?.outcome?.kind === "done";
  useEffect(() => {
    if (done) reread();
  }, [done, reread]);
  const recorded = (data: unknown) => {
    const r = data as S["ApprovalRevocationBody"];
    return <>Revocation of approval <Id id={r.approval} /> recorded by <Id id={r.revokedBy} /> ({r.role}), act <Id id={r.act} />. {REVOKE_LIMIT} The plan's state above is read again.</>;
  };
  if (p.approval === null || (p.state !== "approved" && p.state !== "committed")) return <Result w={w} done={recorded} />;
  const approval = p.approval;
  const submit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const reason = String(new FormData(e.currentTarget).get("reason") ?? "");
    const bad = reasonProblem(reason);
    setNote(bad);
    if (bad === undefined && !done) send({ method: "POST", path: `/approvals/${enc(approval)}/revocations`, body: { reason }, resource: `/plans/${enc(p.id)}` });
  };
  const busy = (w !== undefined && !startsNew(writes, w)) || done;
  return (
    <form onSubmit={submit} aria-label="Revoke approval">
      <p><label>Reason (at most {REASON_MAX} bytes) <input name="reason" autoComplete="off" disabled={busy} /></label></p>
      <p><Action label="Revoke approval" needs={["approver", "recovery-admin"]} submit busy={busy} /> <span data-testid="revoke-limit">{REVOKE_LIMIT}</span></p>
      {note !== undefined && <p role="alert" data-testid="revoke-input">{note}</p>}
      <Result w={w} done={recorded} />
    </form>
  );
}

// The recorded approval's own line: its reasons, and the unseen overwrite it approved.
export const ApprovalRecord = ({ p }: { p: Plan }): ReactNode => (p.approval === null ? null : <Recorded id={p.approval} />);
function Recorded({ id }: { id: string }) {
  const a = useItem<Approval>(`/approvals/${enc(id)}`);
  if (a.kind !== "ok") return null;
  return <Status testid="approval-record">{recordedReasons(a.data)}{a.data.unseenOverwrite && " It approved overwriting the drifted configuration, which no reviewer has seen."}</Status>;
}

// A revoked plan's reason: the plan carries none, so the machine's timeline entry for the revocation
// is read (one read of the newest page). Where it is not on that page, the line says so.
export const RevocationReason = ({ p }: { p: Plan }): ReactNode => (p.approval === null ? null : <Reason machine={p.machine} approval={p.approval} />);
function Reason({ machine, approval }: { machine: string; approval: string }) {
  const t = useItem<{ items: S["TimelineEntry"][] }>(`/machines/${enc(machine)}/timeline?order=newest`);
  if (t.kind !== "ok") return null;
  const e = t.data.items.find((x) => x.kind === "approval-revocation" && (x.entry as { approval?: string }).approval === approval)?.entry as { reason?: string; principal?: string } | undefined;
  return <Status testid="revocation-reason">{e?.reason === undefined ? "The revocation's reason is not on the newest page of the machine's timeline." : `Revoked by ${e.principal === undefined ? "an approver" : short(e.principal)}: ${e.reason}`}</Status>;
}
