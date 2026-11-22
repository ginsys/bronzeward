// The apply page (ginsys/bronzeward#1096): /releases/<id>/apply. The release's machines with their role and profiles,
// none chosen; "Show apply" makes one plan for each machine chosen and shows what each changes; "Apply" records the
// approval of those plans. The logic is apply.ts; every step is decided from the machine's position.

import { type ReactNode, useCallback, useEffect, useRef, useState } from "react";
import type { components } from "./api/v1";
import { type ApplyIo, applyPlans, cancelPlans, type Line, LINES, rowOf, showApply } from "./apply.ts";
import { Change } from "./change.tsx";
import { Result, useWrites } from "./forms.tsx";
import { Ref, useItem, useReads, useSettled } from "./list.tsx";
import type { Position } from "./nextstep.tsx";
import { PageNote } from "./page-note.tsx";
import { Who } from "./principals.tsx";
import { when } from "./states.tsx";
import { Action } from "./shell.tsx";
import { MachineById } from "./titles.tsx";
import { Write } from "./write.ts";
import { APPLY_NEEDS_PUBLISHER, APPLY_NOTE, APPLY_SENTENCE, CONTROL_PLANE_NOTE, NO_ROLE_NOTE, roleWords } from "./words.ts";

type S = components["schemas"];
const enc = encodeURIComponent;
const STATES: Record<Line["state"], string> = { "not-applied": "Not applied", ready: "Plan ready", waiting: "Waiting", running: "Applying", stopped: "Stopped", applied: "Applied", working: "Working", done: "Approved" };
const POLL_MS = 5000;
const POLL_FOR_MS = 10 * 60 * 1000;

interface Seen { position: Position | undefined; role: string | null | undefined }

function Row({ release, machine, chosen, choose, line, n, profiles, seen }: { release: string; machine: string; chosen: boolean; choose: (on: boolean) => void; line: Line | undefined; n: number; profiles: readonly string[] | undefined; seen: (m: string, s: Seen) => void }) {
  const m = useItem<S["MachineBody"]>(`/machines/${enc(machine)}`, undefined, n);
  const p = useItem<Position>(`/machines/${enc(machine)}/position`, undefined, n);
  const role = m.kind === "ok" ? m.data.role : undefined;
  const position = p.kind === "ok" ? p.data : undefined;
  useEffect(() => seen(machine, { position, role }), [seen, machine, position, role]);
  const shown = line ?? (position === undefined ? undefined : { ...rowOf(position, release), write: undefined });
  return (
    <li data-testid="apply-row" data-machine={machine} data-state={shown?.state}>
      <label><input type="checkbox" checked={chosen} onChange={(e) => choose(e.target.checked)} /> <MachineById id={machine} /></label>
      {role != null && <> <span data-testid="apply-role">{roleWords(role)}</span></>}
      {profiles?.map((x) => <span key={x} className="label" data-testid="apply-profile"> {x}</span>)}
      {shown !== undefined && <>: {STATES[shown.state]}. {shown.write === undefined ? shown.text : <><Result w={shown.write} done={() => null} /> {LINES.afterRefusal}</>}</>}
      {" "}<Ref to={`/machines/${enc(machine)}`} id={machine}>Open the machine</Ref>
    </li>
  );
}

// The plan of one machine, whose change is the one Apply approves. A plan another person prepared says who and when.
function PlanChange({ id, machine, mine }: { id: string; machine: string; mine: boolean }) {
  const p = useItem<S["PlanBody"]>(`/plans/${enc(id)}`);
  return (
    <details data-testid="apply-plan" data-plan={id}>
      <summary>What the plan for <MachineById id={machine} /> changes</summary>
      {p.kind === "ok" && <p data-testid="apply-plan-by">{mine ? LINES.madeHere : <>{LINES.preparedBy} <Who id={p.data.createdBy?.principal} /> on {when(p.data.createdAt)}.</>}</p>}
      {p.kind === "ok" ? <Change p={p.data} /> : <p>Not read yet.</p>}
    </details>
  );
}

export function ApplyForm({ release, r }: { release: string; r: S["ReleaseBody"] }): ReactNode {
  const writes = useWrites()!;
  const read = useSettled();
  const reads = useReads();
  const now = useRef(reads);
  now.current = reads;
  const [chosen, setChosen] = useState<ReadonlySet<string>>(new Set());
  const [lines, setLines] = useState<Readonly<Record<string, Line>>>({});
  const [seen, setSeen] = useState<Readonly<Record<string, Seen>>>({});
  const [profiles, setProfiles] = useState<Readonly<Record<string, readonly string[]>>>({});
  const [busy, setBusy] = useState(false);
  const [n, setN] = useState(0);
  const [watching, setWatching] = useState(false);
  const made = useRef(new Set<string>());
  const approved = useRef(new Set<string>());
  const live = useRef(true);
  const sent = useRef<Write[]>([]);

  const io = useCallback((): ApplyIo => {
    const epoch = writes.client?.currentEpoch;
    const valid = now.current.undropped();
    return {
      read: (path) => read(path, () => live.current),
      send: async (spec) => {
        const w = new Write({ ...spec, ...(epoch !== undefined && { epoch }) });
        sent.current.push(w);
        await writes.submit(w);
        return w;
      },
      // Done and ready lines say what the machine's position says next; only a step in progress and a stop keep their own words.
      say: (m, line) => live.current && setLines((was) => {
        const { [m]: _gone, ...rest } = was;
        return line.state === "done" || line.state === "ready" ? rest : { ...rest, [m]: line };
      }),
      live: () => live.current && valid(),
    };
  }, [writes, read]);

  // The plans this page made and the person did not approve are cancelled when the page leaves and when the choice changes.
  const discard = useCallback(() => {
    const ids = [...made.current].filter((id) => !approved.current.has(id));
    made.current.clear();
    if (ids.length > 0) void cancelPlans(ids, io());
  }, [io]);
  useEffect(() => {
    live.current = true;
    return () => {
      live.current = false;
      discard();
    };
  }, [discard]);

  // Profiles are in the release's assignment revisions: one read each, never one per row.
  useEffect(() => {
    let on = true;
    void (async () => {
      const out: Record<string, readonly string[]> = {};
      for (const s of r.sources) {
        if (s.kind !== "assignment" || s.revision === null) continue;
        const a = await read<S["AssignmentRevisionBody"]>(`/assignment-revisions/${enc(s.revision)}`, () => on);
        if (a?.kind === "ok") out[a.data.machine] = a.data.profiles;
      }
      if (on) setProfiles(out);
    })();
    return () => void (on = false);
  }, [r.sources, read]);

  // While machines wait or apply, the rows are read again, for ten minutes after the press.
  useEffect(() => {
    if (!watching) return;
    const tick = setInterval(() => setN((x) => x + 1), POLL_MS);
    const stop = setTimeout(() => setWatching(false), POLL_FOR_MS);
    return () => (clearInterval(tick), clearTimeout(stop));
  }, [watching]);

  const see = useCallback((m: string, s: Seen) => setSeen((was) => (was[m]?.position === s.position && was[m]?.role === s.role ? was : { ...was, [m]: s })), []);
  const machines = r.machines.map((x) => x.machine);
  const picked = machines.filter((m) => chosen.has(m));
  // The plans of the chosen machines as the server holds them now: the ones just made and those a publisher prepared.
  const plans = new Map<string, string>();
  for (const m of picked) {
    const p = seen[m]?.position;
    if (p !== undefined && rowOf(p, release).state === "ready") plans.set(m, p.plan!.id);
  }
  const roles = Object.values(seen).map((s) => s.role);

  const choose = (m: string, on: boolean) => {
    // The plans shown were made for the machines chosen: another choice cancels the ones this page made.
    discard();
    setChosen((was) => new Set(on ? [...was, m] : [...was].filter((x) => x !== m)));
  };
  const show = async () => {
    setBusy(true);
    setLines({});
    const shown = await showApply(release, picked, io());
    for (const id of shown.made) made.current.add(id);
    if (live.current) (setBusy(false), setN((x) => x + 1));
  };
  const apply = async () => {
    setBusy(true);
    const ok = await applyPlans(plans, io());
    for (const id of ok) approved.current.add(id);
    if (live.current) (setBusy(false), setN((x) => x + 1), setWatching(true));
  };

  return (
    <>
      <PageNote parts={[APPLY_NOTE]} />
      <p><Ref to={`/releases/${enc(release)}`} id={release}>Back to the release</Ref></p>
      <ul aria-label="Machines of this release">
        {machines.map((m) => <Row key={m} release={release} machine={m} chosen={chosen.has(m)} choose={(on) => choose(m, on)} line={lines[m]} n={n} profiles={profiles[m]} seen={see} />)}
      </ul>
      {picked.some((m) => seen[m]?.role === "controlplane") && <p data-testid="apply-control-plane">{CONTROL_PLANE_NOTE}</p>}
      {roles.some((x) => x === null) && <p data-testid="apply-no-role">{NO_ROLE_NOTE}</p>}
      <p>
        <Action label="Show apply" needs={["publisher"]} onClick={() => void show()} busy={busy || picked.length === 0} />
        {picked.length === 0 && <span data-testid="apply-none"> Choose at least one machine.</span>}
      </p>
      {picked.length > 0 && plans.size < picked.length && <p data-testid="apply-needs-publisher">{APPLY_NEEDS_PUBLISHER}</p>}
      {plans.size > 0 && (
        <section aria-label="Plans to approve">
          {[...plans].map(([m, id]) => <PlanChange key={id} id={id} machine={m} mine={made.current.has(id)} />)}
          <p data-testid="apply-sentence">{APPLY_SENTENCE}</p>
          <p><Action label="Apply" needs={["approver"]} onClick={() => void apply()} busy={busy} /></p>
        </section>
      )}
    </>
  );
}
