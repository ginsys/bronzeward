// The handover button on the draft page (ginsys/bronzeward#1066): the page of handover.ts. It shows one
// line for the draft and one for each machine, each in words, and the write's own refusal where a step
// failed. A press is read-then-act (handover.ts), so pressing again continues and repeats nothing.

import { useEffect, useRef, useState } from "react";
import { Result, startsNew, unsaved, useWrites } from "./forms.tsx";
import { DRAFT, type Followed, handOver, HANDOVER_SENTENCE, HANDOVER_TITLE, type Io, type Line, MISSING, missingRoles, NOT_PUBLISHED, UNRESOLVED, UNSAVED } from "./handover.ts";
import { keepOnDone } from "./drafts.tsx";
import { settledReads } from "./imports.tsx";
import { Ref, useReads, useSettled } from "./list.tsx";
import { watch } from "./progress.ts";
import { established, notEstablished, publishEnd } from "./publish.tsx";
import { useGate, useRoles } from "./shell.tsx";
import { Status } from "./states.tsx";
import { MachineById } from "./titles.tsx";
import { Write } from "./write.ts";

const STATES: Record<Line["state"], string> = { working: "Working", waiting: "Waiting", done: "Done", left: "Left as it is", stopped: "Stopped" };

// The writes the handover sent, per write layer (which outlives the page): a press waits while one stays open.
const sentBy = new WeakMap<object, Write[]>();

const changed = (line: Line) => line.write?.outcome?.kind === "refused" && line.write.outcome.status === 412;

function LineView({ k, line, show }: { k: string; line: Line; show: () => void }) {
  return (
    <li data-testid="handover-line" data-state={line.state}>
      {k === DRAFT ? "Draft" : <MachineById id={k} />}: {STATES[line.state]}.{" "}
      {line.write === undefined ? line.text : k === DRAFT && changed(line) ? <>{NOT_PUBLISHED} <button type="button" onClick={show}>Show their change</button></> : <Result w={line.write} done={() => null} />}
      {k !== DRAFT && line.state !== "working" && <>{" "}<Ref to={`/machines/${k}`} id={k}>Open the machine</Ref></>}
    </li>
  );
}

// `etag` reads the draft's ETag as this page shows it, at the moment of the press. `seeChange` is the page's
// "Show their change" for a draft that changed under it: it reads the draft again and keeps that read's ETag, and
// answers the note to show where it did not read.
export function Handover({ draft, machines, etag, seeChange, written }: { draft: string; machines: readonly string[]; etag: () => string | undefined; seeChange: () => string | undefined; written: () => void }) {
  const writes = useWrites()!;
  const read = useSettled();
  const reads = useReads();
  const now = useRef(reads);
  now.current = reads;
  const roles = useRoles();
  const gate = useGate(["publisher"]);
  const [lines, setLines] = useState<Readonly<Record<string, Line>>>({});
  const [running, setRunning] = useState(false);
  const [held, setHeld] = useState(false);
  const [note, setNote] = useState<string | undefined>();
  const live = useRef(true);
  // The press in progress: aborted when the page leaves, and when the shell invalidates the reads it began with.
  const run = useRef<AbortController>(undefined);
  const sent = (sentBy.get(writes) ?? sentBy.set(writes, []).get(writes))!;
  useEffect(() => {
    live.current = true;
    return () => {
      live.current = false;
      run.current?.abort();
    };
  }, []);
  const missing = missingRoles(roles);
  const reason = missing.length > 0 ? MISSING(missing) : gate;
  const press = () => {
    if (running || reason !== undefined) return;
    // The press publishes what is stored, so text the editor holds unsaved would be left out of the release.
    if (unsaved.size > 0) return setHeld(true);
    setHeld(false);
    // An earlier press's write still open (in flight, unknown outcome or set aside) may commit or be repeated from its
    // notice: a new key for the same act would send it twice, as useSend's form is held back until its write is settled.
    const pending = sent.filter((w) => !startsNew(writes, w));
    sent.splice(0, sent.length, ...pending);
    if (pending.length > 0) return setNote(UNRESOLVED);
    setNote(undefined);
    setRunning(true);
    setLines({});
    // The press goes on only while the page is here and the shell has not invalidated the reads it began with
    // (an epoch change does, §8.5): a page barred from writes sends no further plan or approval.
    const valid = now.current.undropped();
    // Every write carries the epoch the press began under: one made under a later epoch is refused by the
    // server (412 epoch-mismatch), shown in words, and `current` below only stops the press quietly before it.
    const epoch = writes.client?.currentEpoch;
    const ctl = new AbortController();
    run.current = ctl;
    const current = () => {
      const ok = live.current && valid();
      if (!ok) ctl.abort();
      return ok;
    };
    // The publication is followed by the progress reader the publication page uses, and its end is read by
    // the same words (publishEnd), under the epoch it was read in.
    const follow = async (op: string): Promise<Followed> => {
      const client = writes.client;
      if (client === undefined) return { kind: "unread" };
      const reading = settledReads(client, { settle: now.current.settle, undropped: now.current.undropped }, current);
      const r = await watch(reading, op, { show: () => {}, clear: () => {}, signal: ctl.signal });
      if (r.kind === "stopped" || !current()) return { kind: "stopped" };
      if (r.kind === "failed") return { kind: "unread" };
      if (!established(r, client.currentEpoch)) return { kind: "ended", text: notEstablished(op) };
      const e = publishEnd(r.operation);
      return "release" in e ? { kind: "released" } : { kind: "ended", text: e.text };
    };
    const io: Io = {
      read: (path) => read(path, current),
      follow,
      send: async (spec) => {
        // A done assignment write keeps its answer's ETag for the page, so a press after one that stopped midway
        // sends the next write under the draft as it now is, never the ETag from before the first.
        const keeps = spec.method === "PUT" ? { settled: keepOnDone(draft, () => writes.client?.currentEpoch) } : {};
        const w = new Write({ ...spec, ...keeps, ...(epoch !== undefined && { epoch }) });
        sent.push(w);
        await writes.submit(w);
        return w;
      },
      wait: (ms) => new Promise((r) => setTimeout(r, ms)),
      say: (key, line) => live.current && setLines((was) => ({ ...was, [key]: line })),
      live: current,
    };
    void handOver(draft, machines, io, etag()).finally(() => {
      if (!live.current) return;
      setRunning(false);
      written();
    });
  };
  const shown = Object.entries(lines).sort(([a], [b]) => (a === DRAFT ? -1 : b === DRAFT ? 1 : 0));
  return (
    <section aria-label={HANDOVER_TITLE}>
      <h3>{HANDOVER_TITLE}</h3>
      <p data-testid="handover-sentence">{HANDOVER_SENTENCE}</p>
      <p>
        <button type="button" onClick={press} disabled={reason !== undefined || running}>{HANDOVER_TITLE}</button>
        {reason !== undefined && <span data-testid="handover-reason"> {reason}</span>}
      </p>
      {held && <p role="alert" data-testid="handover-unsaved">{UNSAVED}</p>}
      {shown.length > 0 && <ul aria-label="Adoption progress" aria-live="polite">{shown.map(([k, l]) => <LineView key={k} k={k} line={l} show={() => setNote(seeChange())} />)}</ul>}
      {note !== undefined && <Status testid="handover-note">{note}</Status>}
    </section>
  );
}
