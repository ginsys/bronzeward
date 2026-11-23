// The accounting of a lost request, as one form (journeys §5.9, ginsys/bronzeward#365): the machine, the two observations the settle
// runs between and one press. It serves both decisions: the scope's accounting on the console's row (POST /recovery/accountings) and
// an attempt's on the operation's recovery page (POST /operations/{id}/attempts/{attempt}/accountings). Nothing is typed back: the
// sentence above the button is what is recorded, and the press records it (the ruling of ginsys/bronzeward#1100).

import { type FormEvent, type ReactNode, useEffect, useRef, useState } from "react";
import type { components } from "./api/v1";
import { Result, startsNew, useSend, useWrites } from "./forms.tsx";
import { Id, Ref, useItem, useList, usePending } from "./list.tsx";
import { Who } from "./principals.tsx";
import { safeWord } from "./safe.ts";
import { Action, useFocusOn } from "./shell.tsx";
import { useScopeDeadline } from "./scope-deadline.ts";
import { settleNext } from "./settle-causes.ts";
import { type Deadline, startMarks } from "./start-mark.ts";
import { Status, when } from "./states.tsx";
import { ClusterLink, MachineById } from "./titles.tsx";

type Observation = components["schemas"]["ObservationBody"];
type Server = components["schemas"]["ServerBody"];

export const EXPLANATION_MAX = 1024;
// What the judgment is, and what it does not show (the command's `accountRisk`): `what` names the requests it covers.
export const accountRisk = (what: string) =>
  `This records your judgment that ${what} can still land on this machine. It may already have landed; nothing here shows whether it did. ` +
  "The settle floor is a minimum wait, not a proven bound: a request landing after this can overwrite work admitted once the scope is released, and would be seen only as drift at a later observation.";
export const SCOPE_WHAT = "no request sent before the restore, from any attempt the restored journal shows or one it lost,";
export const ATTEMPT_WHAT = "no copy of the request this attempt sent, whose response was lost,";

const seconds = (s: number) => (s >= 60 && s % 60 === 0 ? `${s / 60} min` : `${s} s`);
const version = (o: Observation) => (o.resourceVersion == null ? "none" : safeWord(o.resourceVersion));
// An observation as a choice: what tells it from the others.
export const observationLine = (o: Observation) =>
  `${safeWord(o.purpose)}, began ${when(o.startedAt)}, recorded ${when(o.at)}, resource version ${version(o)}`;

// Why an observation cannot be the start, or undefined where it can (the settle's own rules that the observation shows for itself).
export const startWhy = (o: Observation): string | undefined => (o.resourceVersion == null ? "it read no machine-configuration resource version" : undefined);
// Why it cannot be the end of a settle that starts at `start` (undefined: none chosen yet).
export function endWhy(o: Observation, start: Observation | undefined): string | undefined {
  if (o.purpose !== "recovery") return `its purpose is ${safeWord(o.purpose)}, not recovery`;
  if (o.resourceVersion == null) return "it read no machine-configuration resource version";
  if (start === undefined) return undefined;
  if (o.id === start.id) return "it is the start";
  // The server compares the two times to the nanosecond; this is to the millisecond, so an end that began in the same millisecond, after the start, is off here.
  if (Date.parse(o.startedAt) <= Date.parse(start.at)) return "it began before the start observation was recorded";
  return undefined;
}
// Whether the two chosen observations read a different resource version, which needs an explanation.
export const moved = (start: Observation | undefined, end: Observation | undefined) =>
  start?.resourceVersion != null && end?.resourceVersion != null && start.resourceVersion !== end.resourceVersion;
export const explanationProblem = (text: string, needed: boolean): string | undefined =>
  needed && text.trim() === "" ? "The resource version moved between the two observations: say why." :
  new TextEncoder().encode(text).length > EXPLANATION_MAX ? `The explanation must be at most ${EXPLANATION_MAX} bytes.` : undefined;

type ScopeAnswer = components["schemas"]["ScopeAccountingBody"];
type AttemptAnswer = components["schemas"]["AccountingBody"];
// The recorded facts after the answer: who, in which role, which act, and which two observations the settle ran between.
// The settle floor and its anchor are on the machine's timeline entry of the answered revision.
export const accountedScope = (data: unknown): ReactNode => {
  const x = data as ScopeAnswer;
  const s = x.scopes[0];
  if (s === undefined) return "Recorded, but the answer names no scope.";
  const held = s.state === "unresolved" ? "unresolved: a restored operation still holds it" : "blocked until it is marked";
  return (
    <>
      Recorded. Scope <MachineById id={s.machine} /> is now {held}. Attempts accounted: {s.attempts.length === 0 ? "none" : s.attempts.map((a, i) => <span key={a}>{i > 0 && ", "}<Id id={a} /></span>)}.
      {" "}Settled between <Id id={s.start} /> and <Id id={s.end} />. Recorded by <Who id={x.decidedBy} /> ({safeWord(x.role)}): act <Id id={x.act} />.{" "}
      {s.state === "unresolved" ? "Next: read what the operation that holds it needs." : "Next: mark the scope."}
    </>
  );
};
export const accountedAttempt = (data: unknown): ReactNode => {
  const x = data as AttemptAnswer;
  return (
    <>
      Recorded. Attempt <Id id={x.attempt} /> is accounted for; the operation's state is unchanged. Settled between <Id id={x.start} /> and <Id id={x.end} />.
      {" "}Recorded by <Who id={x.decidedBy} /> ({safeWord(x.role)}): act <Id id={x.act} />.
    </>
  );
};

// The machine by its name and its cluster by its name, as the command's question names them; a machine or a cluster that is not read
// yet or could not be read is named by its identifier and stops nothing.
function MachineLine({ machine }: { machine: string }) {
  const m = useItem<components["schemas"]["MachineBody"]>(`/machines/${encodeURIComponent(machine)}`);
  const cluster = m.kind === "ok" ? m.data.cluster : undefined;
  return <p data-testid="settle-machine">Machine <MachineById id={machine} />{cluster !== undefined && <>, cluster <ClusterLink id={cluster} /></>}.</p>;
}

function Choice({ name, o, off, checked, onChoose, marked }: { name: string; o: Observation; off: string | undefined; checked: boolean; onChoose: () => void; marked?: boolean }) {
  return (
    <li>
      <label>
        <input type="radio" name={name} value={o.id} checked={checked} disabled={off !== undefined} onChange={onChoose} /> <Id id={o.id} />: {observationLine(o)}
        {marked && " (the first read after the deadline)"}
        {off !== undefined && <> (off: {off})</>}
      </label>
    </li>
  );
}

// `deadline` is the instant the settle counts from where the page can name it (an attempt's own; a scope's is read from `stoppedAt`):
// with it the Start choice marks the one observation the server accepts and gives every other its reason; without it the choice is
// the person's and the server's, as the server judges it.
// `path` is the route the decision is sent to, `resource` the page it names, `what` the requests it covers; `done` shows the answer.
// `due` is whether the latest read still shows the accounting owed: a form that was pressed stays mounted after the read says
// otherwise, to keep its answer, but is off; one never pressed is not drawn. `onStart` tells the parent a press was made, so the
// parent keeps this form mounted when the reread that follows the answer stops showing the accounting as owed.
export function AccountingForm({ machine, path, resource, what, label = "Record accounting", done, due, onStart, deadline: given, stoppedAt }: {
  machine: string; path: string; resource: string; what: string; label?: string; done: (data: unknown) => ReactNode; due: boolean; onStart?: () => void;
  deadline?: Deadline; stoppedAt?: string | undefined;
}) {
  const writes = useWrites()!;
  const server = useItem<Server>("/server");
  // The observations are read when the form is drawn and each time the person asks: a later one may be what the settle floor waits for.
  const [again, setAgain] = useState(0);
  const [list] = useList<Observation>(`/machines/${encodeURIComponent(machine)}/observations`, "", true, undefined, again);
  const [startId, setStart] = useState("");
  const [endId, setEnd] = useState("");
  const [explanation, setExplanation] = useState("");
  const [note, setNote] = useState<string | undefined>();
  const [w, send] = useSend();
  const answer = useRef<HTMLDivElement>(null);
  useFocusOn(w?.outcome, answer);
  const pending = usePending(list, "the machine's observations");
  const sdone = usePending(server, "the server's settings");
  // A scope's deadline needs the machine's operations, so the form reads them (`stoppedAt` is the entry's); an attempt's is given.
  const scoped = useScopeDeadline(machine, stoppedAt, server.kind === "ok" ? server.data.maxTransportDeadlineSeconds : undefined, again);
  const deadline = given ?? scoped;
  const recorded = w?.outcome?.kind === "done";
  const pressed = w !== undefined;
  useEffect(() => { if (pressed) onStart?.(); }, [pressed]);
  // The limits are what the person reads before pressing, so a press waits for them.
  const busy = (w !== undefined && !startsNew(writes, w)) || recorded || !due || sdone !== undefined || server.kind !== "ok";
  if (!due && !pressed) return null;
  // The answer to a press does not wait on the observations: a reread of them that fails leaves it shown.
  const observations = list.kind === "ok" ? [...list.data.items].sort((a, b) => a.basis - b.basis) : [];
  const start = observations.find((o) => o.id === startId);
  const end = observations.find((o) => o.id === endId);
  const needed = moved(start, end);
  const marks = deadline !== undefined && "at" in deadline ? startMarks(observations, deadline.at) : undefined;
  const startReason = (o: Observation) => marks?.why(o) ?? startWhy(o);
  const submit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const bad = start === undefined ? "Choose the start observation." : end === undefined ? "Choose the end observation." :
      startReason(start) ?? endWhy(end, start) ?? explanationProblem(explanation, needed);
    setNote(bad);
    if (bad === undefined && !busy) {
      send({ method: "POST", path, body: { ...(path.startsWith("/recovery/") ? { scopes: [{ machine, start: startId, end: endId }] } : { start: startId, end: endId }), ...(explanation.trim() === "" ? {} : { facts: { explanation } }) }, resource });
    }
  };
  const o = w?.outcome;
  const next = o?.kind === "refused" ? settleNext(o.problem?.detail) : undefined;
  return (
    <form onSubmit={submit} aria-label={label}>
      <MachineLine machine={machine} />
      {sdone === undefined && server.kind === "ok" ? (
        <p data-testid="settle-limits">Settle floor {seconds(server.data.settleFloorSeconds)}, maximum transport deadline {seconds(server.data.maxTransportDeadlineSeconds)}, as the server serves them.</p>
      ) : sdone}
      {list.kind !== "ok" ? pending : observations.length === 0 ? <Status testid="settle-none">No observation of this machine is recorded, so there is nothing to choose.</Status> : (
        <>
          <fieldset disabled={busy}>
            <legend>Start: the first successful read of the machine after the transport deadline</legend>
            {deadline !== undefined && "unknown" in deadline && <Status testid="settle-start-unknown">The page cannot name the valid start: {deadline.unknown} The server decides.</Status>}
            {marks !== undefined && marks.mark === undefined && <Status testid="settle-start-none">No observation of this machine began after the transport deadline and read something, so the page finds no start yet. Read the observations again once one has.</Status>}
            <ul>{observations.map((x) => <Choice key={x.id} name="start" o={x} off={startReason(x)} marked={x.id === marks?.mark} checked={x.id === startId} onChoose={() => setStart(x.id)} />)}</ul>
          </fieldset>
          <fieldset disabled={busy}>
            <legend>End: a recovery observation that began at least the settle floor after that</legend>
            <ul>{observations.map((x) => <Choice key={x.id} name="end" o={x} off={endWhy(x, start)} checked={x.id === endId} onChoose={() => setEnd(x.id)} />)}</ul>
          </fieldset>
          <p><label>Explanation{needed ? " (required: the resource version moved)" : " (needed only if the resource version moved)"} <input name="explanation" value={explanation} autoComplete="off" disabled={busy} onChange={(e) => setExplanation(e.target.value)} /></label></p>
        </>
      )}
      <p><button type="button" disabled={busy} onClick={() => setAgain((i) => i + 1)}>Read the observations again</button></p>
      <p data-testid="settle-judgment">{accountRisk(what)}</p>
      <p><Action label={label} needs={["recovery-admin"]} submit busy={busy || list.kind !== "ok" || observations.length === 0} /></p>
      {note !== undefined && <p role="alert" data-testid="settle-input">{note}</p>}
      <div ref={answer} tabIndex={-1} data-testid="settle-answer">
        <Result w={w} done={done} />
      </div>
      {recorded && (
        <p data-testid="settle-timeline">The settle floor and the anchor it counts from are on <Ref to={`/machines/${encodeURIComponent(machine)}`} id={machine}>the machine's timeline</Ref>.</p>
      )}
      {next !== undefined && <Status testid="write-next">{next}</Status>}
    </form>
  );
}
