// The drift page of one machine (wireframe W9, journeys §5.8): the change made on the machine in words, then the three
// ways to decide it (freeze, adopt, revert), each with its steps and the role each step needs. The controls live here and
// only here: the machine page links to this page. Freeze and unfreeze are sent through the write layer; adopt and revert
// are links to the pages that already do them (the import wizard as a drift adoption, which this page stops at the review
// of, and the create-plan page, which binds the open drift record on the server).

import { type FormEvent, type ReactNode, useCallback, useEffect, useId, useRef, useState } from "react";
import type { components } from "./api/v1";
import { Result, startsNew, useSend, useWrites } from "./forms.tsx";
import { Id, Ref, short, useItem, useList, usePending, Detail, viewOf } from "./list.tsx";
import { attribution, at, causes, driftReads, FROZEN, type Observation, latestUnread } from "./machine.tsx";
import { type Machine, machineHeading } from "./machines.tsx";
import { PageNote } from "./page-note.tsx";
import { driftPath, REASON_MAX, planPath, reasonProblem } from "./plan.tsx";
import { Who } from "./principals.tsx";
import { safeWord } from "./titles.ts";
import { Action, useFocusOn, useGate } from "./shell.tsx";
import { Status } from "./states.tsx";
import { CHANGED_ON_THE_MACHINE, DRIFT_ADOPT, DRIFT_FREEZE, DRIFT_NONE, DRIFT_NOT_SHOWN, DRIFT_NOTE, DRIFT_NOTE_OPEN, DRIFT_REVERT } from "./words.ts";

const enc = encodeURIComponent;
type FreezeBody = components["schemas"]["FreezeBody"];

// The machine a drift path names, decoded; undefined when the path is not `/machines/<id>/drift`, a segment is not valid
// encoding or the identifier is not a machine's. A dot-only identifier is refused here, as a review path's is.
export function driftId(path: string): string | undefined {
  const m = /^\/machines\/([^/]+)\/drift$/.exec(path);
  if (m === null) return undefined;
  try {
    const id = decodeURIComponent(m[1]!);
    return viewOf(id) === "machines" && driftPath(id) === path ? id : undefined;
  } catch {
    return undefined;
  }
}

// A link that is a control: where the role does not allow it, the same words as a disabled button with the reason, as an
// Action shows one. The server's refusal stays authoritative.
function LinkAction({ label, to, id, needs }: { label: string; to: string; id: string; needs: readonly components["schemas"]["IdentityBody"]["roles"][number][] }) {
  const reason = useGate(needs);
  const why = useId();
  if (reason === undefined) return <Ref to={to} id={id}>{label}</Ref>;
  return <span><button type="button" disabled aria-describedby={why}>{label}</button><span id={why}> {reason}</span></span>;
}

// [Freeze] or [Unfreeze]: a reason of 1 to REASON_MAX bytes, the control disabled without its role (a form's submit still
// reaches the server, whose refusal is shown in place), and the answer kept in one place so the machine's reread, which
// swaps one control for the other, does not unmount it. Each instance sends once; the page draws a fresh one (a new key)
// when a read shows the machine moved the other way, so a freeze after an unfreeze works, whoever unfroze, and shows only
// its own answer.
function Reasoned({ id, kind, show, finished }: { id: string; kind: "freeze" | "unfreeze"; show: boolean; finished: () => void }) {
  const writes = useWrites()!;
  const [note, setNote] = useState<string | undefined>();
  const [w, send] = useSend();
  const answer = useRef<HTMLDivElement>(null);
  useFocusOn(w?.outcome, answer);
  const done = w?.outcome?.kind === "done";
  useEffect(() => {
    if (done) finished();
  }, [done, finished]);
  const label = kind === "freeze" ? "Freeze" : "Unfreeze";
  const recorded = (data: unknown) => {
    const r = data as FreezeBody;
    return <>{r.frozen ? "Frozen" : "Unfrozen"}. Recorded by <Who id={r.principal} /> ({safeWord(r.role)}), act <Id id={r.act} />.</>;
  };
  const submit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const reason = String(new FormData(e.currentTarget).get("reason") ?? "");
    const bad = reasonProblem(reason);
    setNote(bad);
    if (bad === undefined && !done) send({ method: "POST", path: `/machines/${enc(id)}/${kind}s`, body: { reason }, resource: `/machines/${enc(id)}` });
  };
  const busy = (w !== undefined && !startsNew(writes, w)) || done;
  return (
    <>
      {show && (
        <form onSubmit={submit} aria-label={label}>
          <p><label>Reason (at most {REASON_MAX} bytes) <input name="reason" autoComplete="off" disabled={busy} /></label></p>
          <p><Action label={label} needs={kind === "freeze" ? ["author", "publisher", "approver", "recovery-admin"] : ["approver"]} submit busy={busy} /></p>
          {note !== undefined && <p role="alert" data-testid={`${kind}-input`}>{note}</p>}
        </form>
      )}
      <div ref={answer} tabIndex={-1} data-testid={`${kind}-answer`}><Result w={w} done={recorded} /></div>
    </>
  );
}

const readAt = (o: Observation | undefined) => (o === undefined ? "a read whose time was not read" : `the read at ${at(o.at)}`);

// The open change in words: what opened it and last saw it, with each read's date and time or that it was not read, a late
// landing, the newest read when it could not read, and the sentence that the change itself is not shown.
function Record({ m, obs }: { m: Machine; obs: Observation[] }) {
  const drift = m.openDrift;
  if (drift === null) return <Status testid="drift-none">{DRIFT_NONE}</Status>;
  const { opened, last } = driftReads(drift, obs);
  const late = latestUnread(m, obs);
  return (
    <>
      <p data-testid="drift-record">
        {CHANGED_ON_THE_MACHINE}: a read differed from what Bronzeward last verified ({m.applied === null ? "nothing applied" : <Ref to={`/releases/${enc(m.applied.release)}`} id={m.applied.release}>the release it applied</Ref>}). Opened by {readAt(opened)}, last seen in {readAt(last)}.
      </p>
      <p data-testid="drift-late">
        Late landing: {drift.lateLanding === null ? "no." : <>yes, the last read may be the late result of <Ref to={`/operations/${enc(drift.lateLanding)}`} id={drift.lateLanding}>an operation</Ref>.</>}
      </p>
      <p data-testid="drift-observed">Observed: {m.observed === null ? "none" : <>read at {at(m.observed.at)}, {attribution(m.observed.release)}</>}.</p>
      {late !== undefined && <p data-testid="drift-unread">The latest read at {at(late.at)} could not read: {causes(late).join("; ")}.</p>}
      <p data-testid="drift-not-shown">{DRIFT_NOT_SHOWN}</p>
    </>
  );
}

export function DriftItem({ id }: { id: string }) {
  const path = `/machines/${enc(id)}`;
  const [n, setN] = useState(0);
  const reread = useCallback(() => setN((x) => x + 1), []);
  const m = useItem<Machine>(path, undefined, n);
  // How many times a read showed the machine freeze and unfreeze in this visit, by this page or by anyone else: an
  // instance's key is the count of the other kind, so a fresh control is drawn when the machine moved the other way.
  const [seen, setSeen] = useState<{ frozen?: boolean; froze: number; unfroze: number }>({ froze: 0, unfroze: 0 });
  if (m.kind === "ok" && m.data.frozen !== seen.frozen) {
    const moved = seen.frozen !== undefined;
    setSeen({ frozen: m.data.frozen, froze: seen.froze + (moved && m.data.frozen ? 1 : 0), unfroze: seen.unfroze + (moved && !m.data.frozen ? 1 : 0) });
  }
  const [obs] = useList<Observation>(`${path}/observations`, "", true);
  const pm = usePending(m, "the machine");
  const po = usePending(obs, "the observations");
  if (m.kind !== "ok") return <><h2>Change on the machine {short(id)}</h2>{pm}</>;
  const d = m.data;
  const read: Observation[] = obs.kind === "ok" ? obs.data.items : [];
  const open = d.openDrift !== null;
  const choice = (title: string, text: string, body?: ReactNode) => (
    <section aria-label={title}>
      <h3>{title}</h3>
      <p>{text}</p>
      {body}
    </section>
  );
  return (
    <>
      <h2>Change on the machine: {machineHeading(d)}</h2>
      <Detail id={d.id} />
      <PageNote parts={[DRIFT_NOTE, open && DRIFT_NOTE_OPEN]}> <Ref to={path} id={d.id}>Back to {machineHeading(d)}</Ref>.</PageNote>
      {obs.kind !== "ok" && po}
      <Record m={d} obs={read} />
      {d.frozen && <Status testid="drift-frozen">{FROZEN}</Status>}
      {choice("Freeze", DRIFT_FREEZE, (
        <>
          <Reasoned key={`freeze-${seen.unfroze}`} id={d.id} kind="freeze" show={!d.frozen} finished={reread} />
          <Reasoned key={`unfreeze-${seen.froze}`} id={d.id} kind="unfreeze" show={d.frozen} finished={reread} />
        </>
      ))}
      {open && choice("Adopt", DRIFT_ADOPT, <p><LinkAction label="Start adoption" to={`/ingestions/new?${new URLSearchParams({ machine: d.id, kind: "drift-adoption", source: "machine" })}`} id={d.id} needs={["author"]} /></p>)}
      {open && choice("Revert", DRIFT_REVERT, d.desired === null
        ? <p>No release is selected for this machine, so there is nothing to revert it to.</p>
        : <p><LinkAction label="Plan revert" to={planPath(d.desired, d.id, "apply-config")} id={d.id} needs={["publisher"]} /></p>)}
    </>
  );
}
