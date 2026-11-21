// The handover button on the draft page (ginsys/bronzeward#1066): the page of handover.ts. It shows one
// line for the draft and one for each machine, each in words, and the write's own refusal where a step
// failed. A press is read-then-act (handover.ts), so pressing again continues and repeats nothing.

import { useEffect, useRef, useState } from "react";
import { Result, unsaved, useWrites } from "./forms.tsx";
import { DRAFT, handOver, HANDOVER_SENTENCE, HANDOVER_TITLE, type Io, type Line, MISSING, missingRoles, UNSAVED } from "./handover.ts";
import { Ref, useSettled } from "./list.tsx";
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

export function Handover({ draft, machines, written }: { draft: string; machines: readonly string[]; written: () => void }) {
  const writes = useWrites()!;
  const read = useSettled();
  const roles = useRoles();
  const gate = useGate(["publisher"]);
  const [lines, setLines] = useState<Readonly<Record<string, Line>>>({});
  const [running, setRunning] = useState(false);
  const [held, setHeld] = useState(false);
  const live = useRef(true);
  useEffect(() => {
    live.current = true;
    return () => void (live.current = false);
  }, []);
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
      send: async (spec) => {
        const w = new Write(spec);
        await writes.submit(w);
        return w;
      },
      wait: (ms) => new Promise((r) => setTimeout(r, ms)),
      say: (key, line) => live.current && setLines((was) => ({ ...was, [key]: line })),
      live: () => live.current,
    };
    void handOver(draft, machines, io).finally(() => {
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
