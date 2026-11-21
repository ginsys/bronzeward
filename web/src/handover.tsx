// The handover button on the draft page (ginsys/bronzeward#1066): the page of handover.ts. It shows one
// line for the draft and one for each machine, each in words, and the write's own refusal where a step
// failed. A press is read-then-act (handover.ts), so pressing again continues and repeats nothing.

import { useEffect, useRef, useState } from "react";
import { Result, unsaved, useWrites } from "./forms.tsx";
import { DRAFT, type Followed, handOver, HANDOVER_SENTENCE, HANDOVER_TITLE, type Io, type Line, MISSING, missingRoles, UNSAVED } from "./handover.ts";
import { settledReads } from "./imports.tsx";
import { Ref, useReads, useSettled } from "./list.tsx";
import { terminal, watch } from "./progress.ts";
import { notEstablished, publishEnd } from "./publish.tsx";
import { useGate, useRoles } from "./shell.tsx";
import { MachineById } from "./titles.tsx";
import { Write } from "./write.ts";

const STATES: Record<Line["state"], string> = { working: "Working", waiting: "Waiting", done: "Done", left: "Left as it is", stopped: "Stopped" };

function LineView({ k, line }: { k: string; line: Line }) {
  return (
    <li data-testid="handover-line" data-state={line.state}>
      {k === DRAFT ? "Draft" : <MachineById id={k} />}: {STATES[line.state]}.{" "}
      {line.write === undefined ? line.text : <Result w={line.write} done={() => null} />}
      {k !== DRAFT && line.state !== "working" && <>{" "}<Ref to={`/machines/${k}`} id={k}>Open the machine</Ref></>}
    </li>
  );
}

// `etag` reads the draft's ETag as this page shows it, at the moment of the press.
export function Handover({ draft, machines, etag, written }: { draft: string; machines: readonly string[]; etag: () => string | undefined; written: () => void }) {
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
  const live = useRef(true);
  const leave = useRef(new AbortController());
  useEffect(() => {
    live.current = true;
    leave.current = new AbortController();
    return () => {
      live.current = false;
      leave.current.abort();
    };
  }, []);
  // The publication is followed by the progress reader the publication page uses, and its end is read by
  // the same words (publishEnd), under the epoch it was read in.
  const follow = async (op: string): Promise<Followed> => {
    const client = writes.client;
    if (client === undefined) return { kind: "unread" };
    const reading = settledReads(client, { settle: now.current.settle, undropped: now.current.undropped }, () => live.current);
    const r = await watch(reading, op, { show: () => {}, clear: () => {}, signal: leave.current.signal });
    if (r.kind === "stopped") return { kind: "stopped" };
    if (r.kind === "failed") return { kind: "unread" };
    if (!terminal(r.operation.state) || r.epoch === undefined || r.epoch !== client.currentEpoch) return { kind: "ended", text: notEstablished(op) };
    const e = publishEnd(r.operation);
    return "release" in e ? { kind: "released" } : { kind: "ended", text: e.text };
  };
  const missing = missingRoles(roles);
  const reason = missing.length > 0 ? MISSING(missing) : gate;
  const press = () => {
    if (running || reason !== undefined) return;
    // The press publishes what is stored, so text the editor holds unsaved would be left out of the release.
    if (unsaved.size > 0) return setHeld(true);
    setHeld(false);
    setRunning(true);
    setLines({});
    const io: Io = {
      read: (path) => read(path, () => live.current),
      follow,
      send: async (spec) => {
        const w = new Write(spec);
        await writes.submit(w);
        return w;
      },
      wait: (ms) => new Promise((r) => setTimeout(r, ms)),
      say: (key, line) => live.current && setLines((was) => ({ ...was, [key]: line })),
      live: () => live.current,
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
      {shown.length > 0 && <ul aria-label="Handover progress" aria-live="polite">{shown.map(([k, l]) => <LineView key={k} k={k} line={l} />)}</ul>}
    </section>
  );
}
