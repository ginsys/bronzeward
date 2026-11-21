// The Onboard cluster page (ginsys/bronzeward#1001): choose a talosconfig, read the cluster it names,
// confirm the cluster's name and Kubernetes endpoint, and let the steps of onboard.ts record it, supply the
// first key and import each machine. The file is read in the page, cut to one context in memory, and sent
// only as that cut: to the discovery and to the first key. It is never stored, logged or shown, and the
// page's copy ends with the first key's write (onboard.ts).

import { type FormEvent, useEffect, useRef, useState } from "react";
import { useReads, Ref, useSettled } from "./list.tsx";
import { type Env, endpointNote, isAddress, type Line, onboardCluster, type Plan, discoverCluster, sender } from "./onboard.ts";
import { Action } from "./shell.tsx";
import { useUnsaved, useWrites } from "./forms.tsx";

// The most a first key may be (the API's bound), so a larger file is refused before it is read.
const MAX_FILE = 512 * 1024;
const HELD = "the talosconfig chosen for onboarding";
const AGAIN = "choose the file again";

export const OnboardClusterAction = () => {
  const { go } = useReads();
  return <Action label="Onboard cluster" needs={["author"]} onClick={() => go("/clusters/onboard")} />;
};

export function OnboardCluster() {
  const writes = useWrites()!;
  const settled = useSettled();
  const { go } = useReads();
  const plan = useRef<Plan>(undefined);
  const alive = useRef(true);
  // The open write that carries the document is kept per write layer, so it outlives this page.
  const send = useRef<Env["send"]>(sender(writes, () => alive.current));
  const [phase, setPhase] = useState<"file" | "confirm" | "run">("file");
  const [lines, setLines] = useState<Line[]>([]);
  const [held, setHeld] = useState(false);
  const [busy, setBusy] = useState(false);
  const [ended, setEnded] = useState<boolean>();
  const [note, setNote] = useState<string>();
  // What the page says about the endpoint the read will use, from the file, the context and the endpoint
  // field. The file is read again at each change and only this sentence is kept, never its text.
  const [said, setSaid] = useState<string>();
  const latest = useRef(0);
  useUnsaved(HELD, held);
  useEffect(() => {
    alive.current = true;
    return () => void (alive.current = false);
  }, []);

  // The page's own writes, reads and clock; nothing is sent once the page is left.
  const env: Env = {
    read: async <T,>(path: string) => {
      const r = await settled<T>(path, () => alive.current);
      return r === undefined || r.kind === "loading" ? { kind: "discarded" } : r;
    },
    send: (spec) => send.current(spec),
    wait: (ms) => new Promise((r) => setTimeout(r, ms)),
  };

  // Clearing the note also drops every answer still being read, so none can bring an old note back.
  const unsay = () => {
    latest.current++;
    setSaid(undefined);
  };

  const say = async (form: HTMLFormElement) => {
    const mine = ++latest.current;
    const d = new FormData(form);
    const file = d.get("file");
    const text = file instanceof File && file.size > 0 && file.size <= MAX_FILE ? await file.text().catch(() => undefined) : undefined;
    const s = endpointNote(text, String(d.get("context") ?? ""), String(d.get("endpoint") ?? ""));
    if (alive.current && mine === latest.current) setSaid(s);
  };

  const read = async (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const d = new FormData(e.currentTarget);
    const file = d.get("file");
    if (busy || !(file instanceof File) || file.size === 0) return;
    if (file.size > MAX_FILE) return setNote("The file is larger than 512 KiB, the most a first key may be.");
    setBusy(true);
    setNote(undefined);
    // The read's own answer says what is wrong; a note for the previous text, or a preview still being read,
    // must not stand beside it.
    unsay();
    const text = await file.text().catch(() => undefined);
    const r = text === undefined ? ({ ok: false, why: "The file could not be read." } as const) : await discoverCluster(env, text, { context: String(d.get("context") ?? ""), endpoint: String(d.get("endpoint") ?? "") });
    if (!alive.current) return;
    setBusy(false);
    // A discovery only reads, so this lead is true; its reasons are whole sentences with none of their own.
    if (!r.ok) return setNote(`Nothing was recorded. ${r.why}`);
    unsay();
    plan.current = r.plan;
    setHeld(r.plan.document !== undefined);
    setLines([]);
    setEnded(undefined);
    setPhase("confirm");
  };

  const run = async () => {
    const p = plan.current;
    if (p === undefined || busy) return;
    setBusy(true);
    setEnded(undefined);
    const ok = await onboardCluster(env, p, (l) => {
      if (!alive.current) return;
      setLines(l);
      setHeld(p.document !== undefined);
    });
    if (!alive.current) return;
    setHeld(p.document !== undefined);
    setBusy(false);
    setEnded(ok);
  };

  const confirm = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const p = plan.current;
    if (p === undefined) return;
    const d = new FormData(e.currentTarget);
    const kube = String(d.get("kube") ?? "").trim();
    if (!/^https:\/\/[^/\s]+$/.test(kube)) return setNote("Give the Kubernetes endpoint as https://host or https://host:port.");
    setNote(undefined);
    p.name = String(d.get("name") ?? "").trim();
    p.kube = kube;
    setPhase("run");
    void run();
  };

  const p = plan.current;
  const failed = lines.find((l) => l.state === "failed" || l.state === "unknown" || l.state === "stopped");
  // A first key that did not end well is released with its write, and nothing here holds the file then.
  const choose = failed?.key === "first-key" && p?.document === undefined;
  return (
    <>
      <h2>Onboard cluster</h2>
      {note !== undefined && <p role="alert" data-testid="onboard-note">{note}</p>}
      {phase === "file" && (
        <form onSubmit={(e) => void read(e)} onChange={(e) => void say(e.currentTarget)} aria-label="Read the cluster">
          <p>
            Choose the talosconfig of the cluster. It is read in this page and sent only as the one context it names, to the server, which uses it
            to read the cluster's nodes and to supply the cluster's first key. It is not stored here.
          </p>
          <p><label>Talosconfig file: <input type="file" name="file" required /></label></p>
          <p><label>Talos context: <input name="context" autoComplete="off" /></label> the current one when empty</p>
          <p><label>Talos endpoint: <input name="endpoint" autoComplete="off" /></label> the context's first endpoint when empty</p>
          {said !== undefined && <p role="status" data-testid="onboard-endpoint">{said}</p>}
          <p><Action label="Read cluster" needs={["author"]} submit busy={busy} /></p>
          {busy && <p role="status">Reading the cluster's nodes</p>}
        </form>
      )}
      {phase === "confirm" && p !== undefined && (
        <form onSubmit={confirm} aria-label="Record the cluster">
          <p data-testid="onboard-found">
            Talos {p.found.version}, contract {p.found.contract}, {p.found.members.length} {p.found.members.length === 1 ? "member" : "members"}, read at {p.found.endpoint}.
            {!isAddress(p.endpoint) && ` The server resolved ${p.endpoint} to that address.`}
            {!p.found.complete && " The read ended at its deadline: a member listed as not answering may be reachable."}
          </p>
          <table>
            <thead><tr><th>Hostname</th><th>Role</th><th>Address</th><th>Platform</th><th>Answered</th></tr></thead>
            <tbody>
              {p.found.members.map((m) => (
                <tr key={m.nodeId}><td>{m.hostname}</td><td>{m.role}</td><td>{m.address}</td><td>{m.platform}</td><td>{m.answered ? "yes" : "no"}</td></tr>
              ))}
            </tbody>
          </table>
          <p><label>Name: <input name="name" required autoComplete="off" /></label> for the cluster in Bronzeward</p>
          <p><label>Kubernetes endpoint: <input name="kube" required defaultValue={p.kube} autoComplete="off" /></label> https://host or https://host:port, the cluster's own where its control-plane members agree on one, else the first control-plane member's address</p>
          <p>Onboarding records the cluster and each member that answered, supplies the cluster's first key, and imports each machine into one draft.</p>
          <p><Action label="Onboard cluster" needs={["author"]} submit busy={busy} /></p>
        </form>
      )}
      {phase === "run" && (
        <>
          <ul data-testid="onboard-lines">
            {lines.map((l) => (
              <li key={l.key} data-testid="onboard-line" data-state={l.state}>
                {l.label}: {l.state}{l.detail !== undefined && `, ${l.detail}`}{l.to !== undefined && <> (<Ref to={l.to} id={l.to}>open</Ref>)</>}
              </li>
            ))}
          </ul>
          {busy && <p role="status" data-testid="onboard-running">Running</p>}
          {ended === true && p?.draft !== undefined && (
            <p role="status" data-testid="onboard-result">
              Onboarded. Review what was imported in the draft <Ref to={`/drafts/${p.draft}`} id={p.draft} />, or <a href="/machines" onClick={(e) => { e.preventDefault(); go("/machines"); }}>see the machines</a>.
            </p>
          )}
          {ended === false && (
            <p role="alert" data-testid="onboard-result">
              Stopped at {failed?.label ?? "a step"}. Running it again finds what is recorded and finishes the rest{choose ? `: ${AGAIN}` : ""}.
            </p>
          )}
          {ended === false && (
            <p>
              {choose
                ? <Action label="Choose the file again" needs={["author"]} onClick={() => { setPhase("file"); setNote(undefined); unsay(); }} />
                : <Action label="Run again" needs={["author"]} onClick={() => void run()} busy={busy} />}
            </p>
          )}
        </>
      )}
    </>
  );
}
