// The apply page (ginsys/bronzeward#1096): /releases/<id>/apply. The release's machines with their role and profiles,
// none chosen; "Show apply" makes one plan for each machine chosen and shows what each changes; "Apply" records the
// approval of those plans. The logic is apply.ts; every step is decided from the machine's position.

import { type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { components } from "./api/v1";
import { type ApplyIo, applyPlans, cancelPlans, type Line, LINES, rowOf, showApply } from "./apply.ts";
import type { Assignment } from "./apply-shared.ts";
import { SharedBlock } from "./apply-shared.tsx";
import { Change } from "./change.tsx";
import { Result, startsNew, useWrites } from "./forms.tsx";
import { firstSentence, Note, Panel } from "./layout.tsx";
import { Ref, useItem, useReads, useSettled } from "./list.tsx";
import type { Position } from "./nextstep.tsx";
import { poll } from "./plan.tsx";
import { safeWord } from "./safe.ts";
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

function Row({ release, machine, chosen, choose, locked, line, n, profiles, seen, settled }: { release: string; machine: string; chosen: boolean; choose: (on: boolean) => void; locked: boolean; line: Line | undefined; n: number; profiles: readonly string[] | undefined; seen: (m: string, s: Seen) => void; settled: () => void }) {
  const m = useItem<S["MachineBody"]>(`/machines/${enc(machine)}`, undefined, n, settled);
  const p = useItem<Position>(`/machines/${enc(machine)}/position`, undefined, n, settled);
  const role = m.kind === "ok" ? m.data.role : undefined;
  const position = p.kind === "ok" ? p.data : undefined;
  useEffect(() => seen(machine, { position, role }), [seen, machine, position, role]);
  // The machine's line is read from its position and no other source (ginsys/bronzeward#1159). What a step of this page
  // says, and the answer of a write, is a note about the press, drawn after the line: a note whose write has since
  // succeeded (the person repeated it) is stale and goes.
  const note = line?.write?.outcome?.kind === "done" ? undefined : line;
  const shown = position === undefined ? undefined : rowOf(position, release);
  return (
    <li data-testid="apply-row" data-machine={machine} data-state={shown?.state}>
      <label><input type="checkbox" checked={chosen} disabled={locked} onChange={(e) => choose(e.target.checked)} /> <MachineById id={machine} /></label>
      {role != null && <> <span data-testid="apply-role">{safeWord(roleWords(role))}</span></>}
      {profiles?.map((x) => <span key={x} className="label" data-testid="apply-profile"> {safeWord(x)}</span>)}
      {shown !== undefined && <>: {STATES[shown.state]}. {shown.text}</>}
      {note !== undefined && <span data-testid="apply-note"> {note.write === undefined ? note.text : <><Result w={note.write} done={() => null} />{note.write.outcome?.kind === "refused" && <> {LINES.afterRefusal}</>}</>}</span>}
      {" "}<Ref to={`/machines/${enc(machine)}`} id={machine}>Open the machine</Ref>
    </li>
  );
}

// What the page knows of a plan's change: shown (Apply may approve it), withheld by the server (it cannot be shown, so
// Apply is off), bound to a drift record (it overwrites a configuration no reviewer has seen, so Apply is off) or unread
// (the read has not answered).
type Seeing = "shown" | "withheld" | "drift" | "unread";

// The plan of one machine, whose change is the one Apply approves. A plan another person prepared says who and when.
// A plan whose line says the change is withheld is handed over to the person as that line is drawn: the page does not
// cancel it however the person gets to it (a click, a new tab, a copied address), and the line says it stays.
function PlanChange({ id, machine, mine, onRead, onWithheld }: { id: string; machine: string; mine: boolean; onRead: (id: string, s: Seeing) => void; onWithheld: (id: string) => void }) {
  const p = useItem<S["PlanBody"]>(`/plans/${enc(id)}`);
  const seeing: Seeing = p.kind !== "ok" ? "unread" : p.data.evidence.diff?.withheld === true ? "withheld" : p.data.operation === "apply-config" && p.data.driftRecord != null ? "drift" : "shown";
  useEffect(() => onRead(id, seeing), [onRead, id, seeing]);
  useEffect(() => {
    if (seeing === "withheld" || seeing === "drift") onWithheld(id);
  }, [onWithheld, id, seeing]);
  return (
    <>
      <details data-testid="apply-plan" data-plan={id}>
        <summary>What the plan for <MachineById id={machine} /> changes</summary>
        {p.kind === "ok" && <p data-testid="apply-plan-by">{mine ? LINES.madeHere : <>{LINES.preparedBy} <Who id={p.data.createdBy?.principal} /> on {when(p.data.createdAt)}.</>} It expires {when(p.data.expiresAt)}.</p>}
        {p.kind === "ok" ? <Change p={p.data} /> : <p>Not read yet.</p>}
      </details>
      {seeing === "withheld" && <p data-testid="apply-withheld"><MachineById id={machine} />: the change of its plan {LINES.withheld} <Ref to={`/plans/${enc(id)}`} id={id}>Open the plan</Ref></p>}
      {seeing === "drift" && p.kind === "ok" && <p data-testid="apply-drift"><MachineById id={machine} />: its plan overwrites the drifted configuration of drift record {p.data.driftRecord}, which no reviewer has seen. {LINES.handedOver} <Ref to={`/plans/${enc(id)}`} id={id}>Open the plan</Ref></p>}
    </>
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
  // What each machine's assignment revision selects, undefined until read: its profiles label the rows and, with the
  // fragments, say which machines a shared name the release moved reaches.
  const [assignments, setAssignments] = useState<Readonly<Record<string, Assignment>> | undefined>(undefined);
  const [busy, setBusy] = useState(false);
  const [n, setN] = useState(0);
  // The plans this page made, each with its machine, and those Apply was pressed for, which the page never cancels.
  const made = useRef(new Map<string, string>());
  const pressed = useRef(new Set<string>());
  const [readable, setReadable] = useState<Readonly<Record<string, Seeing>>>({});
  const live = useRef(true);
  const sent = useRef<Write[]>([]);
  // A write of this page that is still open (in flight, unknown or set aside after an attempt) is the logical write of
  // its press: nothing new is sent until it ended or its notice was dismissed (forms.tsx startsNew). The buttons and
  // every send of a batch read it here.
  const openWrite = useCallback(() => sent.current.some((w) => !startsNew(writes, w)), [writes]);

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
      canSend: () => live.current && valid() && !openWrite(),
      handedOver: (id) => pressed.current.has(id),
    };
  }, [writes, read, openWrite]);

  // The plans this page made for a machine that is not in `keep`, and that Apply was not pressed for, are cancelled:
  // when the page leaves (nothing is kept) and when a machine is no longer chosen. The rows are read again after.
  const discard = useCallback((keep: ReadonlySet<string> = new Set()) => {
    const ids = [...made.current].filter(([id, m]) => !keep.has(m) && !pressed.current.has(id)).map(([id]) => id);
    for (const id of ids) made.current.delete(id);
    if (ids.length > 0) void cancelPlans(ids, io()).then(() => live.current && setN((x) => x + 1));
  }, [io]);
  // Only leaving the page cancels what it made: the effect runs on mount and unmount, never when `io` is rebuilt, and
  // calls the latest `discard` through a ref.
  const discarding = useRef(discard);
  discarding.current = discard;
  useEffect(() => {
    live.current = true;
    return () => {
      live.current = false;
      discarding.current();
    };
  }, []);

  // Profiles are in the release's assignment revisions: one read each, never one per row.
  useEffect(() => {
    let on = true;
    void (async () => {
      const out: Record<string, Assignment> = {};
      for (const s of r.sources) {
        if (s.kind !== "assignment" || s.revision === null) continue;
        const a = await read<S["AssignmentRevisionBody"]>(`/assignment-revisions/${enc(s.revision)}`, () => on);
        if (a?.kind === "ok") out[a.data.machine] = { profiles: a.data.profiles, fragments: a.data.fragments };
      }
      if (on) setAssignments(out);
    })();
    return () => void (on = false);
  }, [r.sources, read]);

  // The rows are read again for ten minutes after a press of either button, and only while a machine waits or applies:
  // five seconds after the last read settled, never on a clock, so a slow read is not dropped by the next one (list.tsx
  // drops a read whose effect re-ran). Each row reads its machine and its position, so a round is over when 2 x the
  // machines' reads have answered. A round is judged once it is drawn (the effect on `rounds` runs after the rows'
  // effects have handed their positions to `latest`), so it is the answered round that says whether another follows.
  const until = useRef(0);
  const latest = useRef<Record<string, Seen>>({});
  const round = useRef({ answered: 0, reads: 0 });
  round.current.reads = 2 * r.machines.length;
  const [rounds, setRounds] = useState(0);
  // The ten minutes run from the press, and no read starts after them; a press's own batch is not a round to watch.
  const batch = useRef(false);
  const timer = useMemo(() => poll(() => Date.now() < until.current && setN((x) => x + 1), POLL_MS), []);
  const rowSettled = useCallback(() => {
    round.current.answered++;
    if (round.current.answered >= round.current.reads && Date.now() < until.current) setRounds((x) => x + 1);
  }, []);
  useEffect(() => {
    if (rounds > 0 && !batch.current && Date.now() < until.current && Object.values(latest.current).some((s) => s.position !== undefined && ["waiting", "running"].includes(rowOf(s.position, release).state))) timer.settled();
  }, [rounds, timer, release]);
  // A new round of reads (the page's own, or the shell's refresh or view change) starts the count again and drops a
  // pending wait: the rows' effects run before this one, and their answers come after it.
  useEffect(() => {
    round.current.answered = 0;
    timer.stop();
  }, [timer, n, reads.again, reads.view]);
  useEffect(() => timer.stop, [timer]);

  const see = useCallback((m: string, s: Seen) => {
    latest.current[m] = s;
    setSeen((was) => (was[m]?.position === s.position && was[m]?.role === s.role ? was : { ...was, [m]: s }));
  }, []);
  const machines = r.machines.map((x) => x.machine);
  const picked = machines.filter((m) => chosen.has(m));
  // The plans of the chosen machines as the server holds them now: the ones just made and those a publisher prepared.
  const plans = new Map<string, string>();
  for (const m of picked) {
    const p = seen[m]?.position;
    // A machine whose position cannot be read now still shows the plan this page made for it.
    const own = [...made.current].find(([, mm]) => mm === m)?.[0];
    if (p !== undefined && rowOf(p, release).state === "ready") plans.set(m, p.plan!.id);
    else if (p === undefined && own !== undefined) plans.set(m, own);
  }
  const roles = Object.values(seen).map((s) => s.role);
  // Apply needs every plan's change shown: a person never approves a change they could not see. A plan whose change the
  // server withholds says so, and where to go, in its own line.
  const unread = [...plans.values()].some((id) => readable[id] !== "shown");
  const unreadOnly = [...plans.values()].some((id) => readable[id] !== "shown" && readable[id] !== "withheld" && readable[id] !== "drift");
  const held = openWrite();

  const choose = (m: string, on: boolean) => {
    // The plans shown were made for the machines chosen: a machine no longer chosen has its plan cancelled.
    const next = new Set(on ? [...chosen, m] : [...chosen].filter((x) => x !== m));
    discard(next);
    setLines({});
    setChosen(next);
  };
  const show = async () => {
    setBusy(true);
    setLines({});
    (batch.current = true), (until.current = Date.now() + POLL_FOR_MS);
    await showApply(release, picked, io(), made.current);
    batch.current = false;
    if (live.current) (setBusy(false), setN((x) => x + 1));
  };
  const apply = async () => {
    setBusy(true);
    for (const id of plans.values()) pressed.current.add(id);
    (batch.current = true), (until.current = Date.now() + POLL_FOR_MS);
    await applyPlans(plans, io());
    batch.current = false;
    if (live.current) (setBusy(false), setN((x) => x + 1));
  };
  const handOff = useCallback((id: string) => void pressed.current.add(id), []);
  const markRead = useCallback((id: string, s: Seeing) => setReadable((was) => (was[id] === s ? was : { ...was, [id]: s })), []);

  return (
    <>
      <Note line={firstSentence(APPLY_NOTE)} parts={[APPLY_NOTE]} />
      <SharedBlock r={r} assignments={assignments} />
      <Panel title="Machines of this release" label="The machine list">
        <ul aria-label="Machines of this release">
          {machines.map((m) => <Row key={m} release={release} machine={m} chosen={chosen.has(m)} choose={(on) => choose(m, on)} locked={busy} line={lines[m]} n={n} profiles={assignments?.[m]?.profiles} seen={see} settled={rowSettled} />)}
        </ul>
        {picked.some((m) => seen[m]?.role === "controlplane") && <p data-testid="apply-control-plane">{CONTROL_PLANE_NOTE}</p>}
        {roles.some((x) => x === null) && <p data-testid="apply-no-role">{NO_ROLE_NOTE}</p>}
        <p>
          <Action label="Show apply" variant="secondary" needs={["publisher"]} onClick={() => void show()} busy={busy || held || picked.length === 0} />
          {picked.length === 0 && <span data-testid="apply-none"> Choose at least one machine.</span>}
        </p>
        {held && <p data-testid="apply-held">{LINES.held}</p>}
        {picked.length > 0 && plans.size < picked.length && <p data-testid="apply-needs-publisher">{APPLY_NEEDS_PUBLISHER}</p>}
      </Panel>
      {plans.size > 0 && (
        <Panel title="Plans to approve">
          {[...plans].map(([m, id]) => <PlanChange key={id} id={id} machine={m} mine={made.current.has(id)} onRead={markRead} onWithheld={handOff} />)}
          <p data-testid="apply-sentence">{APPLY_SENTENCE}</p>
          {unreadOnly && <p data-testid="apply-unread">{LINES.unreadPlan}</p>}
          <p><Action label="Apply" needs={["approver"]} onClick={() => void apply()} busy={busy || held || unread} /></p>
        </Panel>
      )}
    </>
  );
}
