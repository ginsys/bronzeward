// The first write views (ginsys/bronzeward#338): Enter a cluster by hand, Add machine and Change endpoint
// (journeys §5.2 and §10), each a human author's act, over the write layer of write.ts. Also the
// notice of a write whose outcome is unknown and the leave guards (#257 B11).

import { createContext, type FormEvent, type ReactNode, useContext, useEffect, useRef, useState, useSyncExternalStore } from "react";
import type { components } from "./api/v1";
import { type Dropped, type Listed, type Load, Ref, short, useList, usePending, useReads } from "./list.tsx";
import type { Cluster, Machine } from "./machines.tsx";
import { RequestRef } from "./problems.tsx";
import { Action, useOffline, type Variant } from "./shell.tsx";
import type { Problem } from "./request.ts";
import { CHECK_STATE, CUT_OFF, problemSentences, Refused, Repeated, REPLAYED, SetAside, Status, unknown, Unknown } from "./states.tsx";
import { PageNote } from "./page-note.tsx";
import { MACHINE_NAME } from "./machine-name.ts";
import { machineName } from "./machines.tsx";
import { MachineById } from "./titles.tsx";
import { ADD_MACHINE, ADD_MACHINE_NOTE, BY_HAND_NOTE, ENTER_A_CLUSTER_BY_HAND, NAME_HINT, NAME_THIS_MACHINE, notAName, RENAME_MACHINE, RENAME_REACHES } from "./words.ts";
import { type Outcome, Write, type Writes, type WriteSpec } from "./write.ts";

type EndpointChange = components["schemas"]["EndpointChangeBody"];
type NameChange = components["schemas"]["NameChangeBody"];

const Context = createContext<Writes | undefined>(undefined);
export const WritesProvider = Context.Provider;
// The page's writes, rendered again on each change; none outside the page's provider.
const none = { subscribe: () => () => {}, snapshot: () => 0 };
export function useWrites(): Writes | undefined {
  const writes = useContext(Context);
  const store = writes ?? none;
  useSyncExternalStore(store.subscribe, store.snapshot, store.snapshot);
  return writes;
}

// What the notice and the leave dialog say of one write: its key, its route and what to read.
const detail = (w: Write, to = w.spec.resource) => ` Key ${w.key}, ${w.route}. To learn the outcome, read ${to}.`;

// The record a write's notice names and opens: the one a 409 conflict of its latest answer names
// (§8.2 409 row, through existing()), else the write's own resource. The notices render outside
// the page's reads, so the record is named in text and [Show current state] reads it.
export function record(w: Write): { named: string; to: string } {
  const o = w.outcome;
  const p = o === undefined ? ((w.doubt ?? w.repeating)?.problem ?? null) : "problem" in o ? o.problem : null;
  const rec = p === null ? undefined : existing(p.status, p, w.spec.resource);
  return rec === undefined ? { named: "", to: w.spec.resource } : { named: ` The ${rec.kind} already recorded: ${rec.id}.`, to: rec.to };
}

// A settled [Repeat exactly]'s result, as its notice shows it (journeys §8, the Replayed row).
function settled(o: Outcome): { line: string; problem: Problem | null } {
  const earlier = "replayed" in o && o.replayed ? `${REPLAYED} ` : "";
  const problem = "problem" in o ? o.problem : null;
  // Where the outcome is still unknown the problem's next step is left out, and the line says how to check.
  const doubt = o.kind === "unknown" || (o.kind === "set-aside" && o.ambiguous);
  // The answer's problem in words, with what the route accepts (§8.2), as every notice shows it.
  const words = problem === null ? "" : ` ${problemSentences(problem, problem.status, !doubt)}`;
  switch (o.kind) {
    case "done":
      return { line: `${earlier}Saved (the server answered ${o.status}).${o.defect !== undefined ? ` ${o.defect}` : ""}`, problem };
    case "refused":
      return { line: `Not saved${problem === null ? `: the server answered ${o.status}.` : "."}${o.cut ? ` ${CUT_OFF}` : ""}${words}`, problem };
    default:
      return { line: `${doubt ? unknown(o.why) : `Not saved: ${o.why}.`}${doubt ? ` ${CHECK_STATE}` : ""}${words}`, problem };
  }
}

// Shown as soon as a write's outcome is unknown, until dismissed (B11). A write this notice showed
// keeps it after it settles, with its result, until dismissed. `go` opens the resource.
export function WriteNotices({ go }: { go: (to: string) => void }) {
  const writes = useWrites();
  // Offline, [Repeat exactly] is not offered: its confirming read would get no answer and set the
  // write aside for good. It is offered again once the page is online.
  const offline = useOffline();
  return writes?.notices.map((w) => {
    const o = w.outcome;
    const { named, to } = record(w);
    const props = { onShow: () => go(to), detail: named + detail(w, to), onDismiss: () => writes.dismiss(w), testid: "write-notice" };
    if (o !== undefined && w.kept) return <Repeated key={w.key} {...settled(o)} role={o.kind === "refused" ? "alert" : "status"} {...props} detail={`${named} Key ${w.key}, ${w.route}. Read ${to}.`} />;
    if (o?.kind === "unknown") return <Unknown key={w.key} why={o.why} problem={o.problem} onRepeat={writes.repeatable(w) && !offline ? () => void writes.submit(w) : undefined} {...props} />;
    if (o?.kind === "set-aside") return <SetAside key={w.key} why={o.why} problem={o.problem} {...props} />;
    // While the run is in flight, the latest state that made the outcome unknown: an attempt of
    // this run that may have committed, else the earlier attempt a [Repeat exactly] repeats.
    const live = o === undefined ? (w.doubt ?? w.repeating) : undefined;
    // Set aside while its answer is still on its way: shown at once, with no dismissal until that
    // answer settles it (§8.5 item 2), and with the problem of the answer behind it.
    if (o === undefined && w.aside !== undefined) return <SetAside key={w.key} why={w.aside} problem={live?.problem ?? null} {...props} onDismiss={undefined} />;
    // An attempt that may have committed, while the automatic repeats run, or a [Repeat exactly]
    // in flight: unknown already (B11), and the notice stays.
    if (live !== undefined) return <Unknown key={w.key} why={live.why} problem={live.problem} repeating {...props} />;
    return null;
  });
}

// §11.4 item 2: browser spelling and fill services get nothing of a document or marks.
export const NO_ASSIST = { autoComplete: "off", spellCheck: false, autoCorrect: "off", autoCapitalize: "off" } as const;

// The editors holding text not saved, each by what it edits (#339): never stored, only guarded.
export const unsaved = new Set<string>();
export function useUnsaved(what: string, dirty: boolean): void {
  useEffect(() => {
    if (!dirty) return;
    unsaved.add(what);
    return () => void unsaved.delete(what);
  }, [what, dirty]);
}

// The in-app router's guard: a move to another page while a write is pending or unknown, or while
// an editor holds unsaved text, asks first, repeating the notices. The writes stay, and so do their
// notices. A notice's own [Show current state] (`notice`, W11) is the notice's way out: it asks
// nothing on the writes' account, but still asks for unsaved text, which leaving drops. `from` is
// the page shown, which Back and Forward have already left in `location` when they ask.
export function leaving(writes: Writes, to: string, notice = false, from = location.pathname): boolean {
  const open = notice ? [] : writes.open;
  if ((open.length === 0 && unsaved.size === 0) || new URL(to, location.href).pathname === new URL(from, location.href).pathname) return true;
  const lines = open.map((w) => (w.outcome === undefined && w.doubt === undefined && w.repeating === undefined && w.aside === undefined ? "Sent, with no answer yet." : "Unknown whether this was saved.") + record(w).named + detail(w, record(w).to));
  for (const what of unsaved) lines.push(`Unsaved text in ${what}: leaving drops it.`);
  return confirm(`${lines.join("\n")}\nLeave this page?`);
}

// Sign-out ends the page and drops every editor's text, so it asks first while an editor holds
// unsaved text (ginsys/bronzeward#517 H10).
export function signingOut(): boolean {
  if (unsaved.size === 0) return true;
  return confirm(`${[...unsaved].map((what) => `Unsaved text in ${what}: signing out drops it.`).join("\n")}\nSign out?`);
}

// A reload or a tab close drops every pending write and unsaved text, so it is guarded by the
// browser's own dialog. A notice's own [Show current state] exempts the writes for one unload only:
// if the operator stops the navigation, the next reload or close asks again. Unsaved text still asks.
export function guardsUnload(writes: Writes): boolean {
  const exempt = writes.unloading;
  writes.unloading = false;
  return (writes.open.length > 0 && !exempt) || unsaved.size > 0;
}
export function useUnloadGuard(writes: Writes): void {
  useEffect(() => {
    const guard = (e: BeforeUnloadEvent) => {
      if (guardsUnload(writes)) e.preventDefault();
    };
    addEventListener("beforeunload", guard);
    return () => removeEventListener("beforeunload", guard);
  }, [writes]);
}

// The notices outside the shell (the sign-in view), whose [Show current state] loads the resource's
// page: the notice's way out, so the unload guard does not ask.
export function PageNotices() {
  const writes = useWrites();
  return <WriteNotices go={(to) => {
    if (writes !== undefined) writes.unloading = true;
    location.assign(to);
  }} />;
}

// One form's logical write: a second submit while it runs, or while its outcome is unknown, sends
// nothing new. A new write, under a new key, starts only after it ended or its notice was dismissed.
// Whether the form's next send starts a new write: only once its last one is no longer open.
export const startsNew = (writes: Writes, w: Write | undefined) => w === undefined || !writes.open.includes(w);

export function useSend(): [Write | undefined, (spec: WriteSpec) => void] {
  const writes = useWrites()!;
  const current = useRef<Write>(undefined);
  const [, shown] = useState<Write>();
  const send = (spec: WriteSpec) => {
    const w = current.current;
    if (!startsNew(writes, w)) {
      if (w!.outcome === undefined) void writes.submit(w!);
      return;
    }
    const next = new Write(spec);
    current.current = next;
    shown(next);
    void writes.submit(next);
  };
  return [current.current, send];
}

// A 409 conflict that names another cluster or machine already recorded, as Enter a cluster by hand's or
// Add machine's does: the record to read (§8.2 409 row). Not the write's own resource, which a
// Change endpoint conflict names.
export function existing(status: number, p: Problem | null, resource: string): { kind: "cluster" | "machine"; id: string; to: string } | undefined {
  if (status !== 409 || p?.type !== "urn:bronzeward:problem:conflict") return undefined;
  // Only a write on a cluster or a machine can name one already recorded: a plan's or a draft's refusal
  // names the machine it is about, which is no record to read instead (ginsys/bronzeward#357).
  if (!/^\/(clusters|machines)(\/|\?|$)/.test(resource)) return undefined;
  for (const kind of ["cluster", "machine"] as const) {
    const id = p[kind];
    if (typeof id === "string" && `/${kind}s/${id}` !== resource) return { kind, id, to: `/${kind}s/${id}` };
  }
  return undefined;
}

// A fragment write's repeat that waits for the first request of its key (client contracts §8.4
// item 2, AMB-02), as the CLI says it.
export const stillIngesting = (ingestion: string) => `Saving: the first request with this key is still ingesting (ingestion ${ingestion}); waiting for it.`;

// The form's own line for its write; an unknown outcome is the notice's, until it is dismissed.
export function Result({ w, done }: { w: Write | undefined; done: (data: unknown) => ReactNode }) {
  const writes = useWrites();
  const o: Outcome | undefined = w?.outcome;
  if (w === undefined) return null;
  if (o === undefined) return <Status testid="write-result">{w.ingesting === undefined ? "Saving" : stillIngesting(w.ingesting)}</Status>;
  switch (o.kind) {
    case "done":
      // A success whose body could not be read is still saved: it names the page to read, and is
      // never repeated.
      if (typeof o.data !== "object" || o.data === null) {
        return <Status testid="write-result">{o.replayed && `${REPLAYED} `}Saved (the server answered {o.status}), but its answer could not be read. Read <Ref to={w.spec.resource} id={w.spec.resource}>{w.spec.resource}</Ref>.{o.defect !== undefined && ` ${o.defect}`}</Status>;
      }
      return <Status testid="write-result">{o.replayed && `${REPLAYED} `}{done(o.data)}{o.defect !== undefined && ` ${o.defect}`}</Status>;
    case "refused": {
      const named = existing(o.status, o.problem, w.spec.resource);
      return (
        <>
          <Refused testid="write-result" problem={o.problem} status={o.status} verb="saved" cut={o.cut === true} />
          {named !== undefined && <Status testid="write-existing" role="alert">The {named.kind} already recorded: <Ref to={named.to} id={named.id}>{named.id}</Ref>.</Status>}
        </>
      );
    }
    case "not-done":
      // The last answer's problem, when it carried one (§8.2).
      return <Status testid="write-result" reference={o.problem === null ? undefined : <RequestRef problem={o.problem} status={o.problem.status} />}>Not saved: {o.why}.{o.problem !== null && ` ${problemSentences(o.problem, o.problem.status)}`}</Status>;
    case "set-aside":
    case "unknown":
      // Set aside before any attempt that may have committed, nothing was; otherwise the notice says.
      if (o.kind === "set-aside" && !o.ambiguous) return <Status testid="write-result" reference={o.problem === null ? undefined : <RequestRef problem={o.problem} status={o.problem.status} />}>Not saved: {o.why}. Nothing of it was committed.{o.problem !== null && ` ${problemSentences(o.problem, o.problem.status)}`}</Status>;
      if (writes?.notices.includes(w) !== false) return <Status testid="write-result">See the notice at the top of the page: its key and what to read.</Status>;
      // Its notice was dismissed: the form says what the notice said of it.
      return <Status testid="write-result">Unknown whether this was saved, and its notice was dismissed.{record(w).named}{detail(w, record(w).to)}</Status>;
  }
}

// `variant` is how the submit button looks (the console layout's action hierarchy, §2.3); a form is drawn as the page's one filled button unless its
// page already has one, as the machine page's two forms do not.
export function Form({ title, label, onSubmit, w, done, children, variant }: { title?: string; label: string; onSubmit: (data: FormData) => void; w: Write | undefined; done: (data: unknown) => ReactNode; children: ReactNode; variant?: Variant }) {
  const writes = useWrites()!;
  const held = w !== undefined && writes.open.includes(w);
  const submit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    onSubmit(new FormData(e.currentTarget));
  };
  return (
    <form onSubmit={submit} aria-label={title ?? label}>
      {children}
      <p><Action label={label} needs={["author"]} submit busy={held} {...(variant !== undefined && { variant })} /></p>
      <Result w={w} done={done} />
    </form>
  );
}

export const field = (data: FormData, name: string) => String(data.get(name) ?? "");
export const Text =({ label, name, hint, value }: { label: string; name: string; hint?: string; value?: string }) => (
  <p><label>{label} <input name={name} required defaultValue={value} autoComplete="off" /></label>{hint !== undefined && ` ${hint}`}</p>
);

export function AddCluster() {
  const [w, send] = useSend();
  const submit = (d: FormData) => send({
    method: "POST", path: "/clusters", resource: "/clusters",
    body: { name: field(d, "name"), endpoint: field(d, "endpoint"), contract: field(d, "contract"), talosClusterId: field(d, "talosClusterId") },
  });
  const done = (data: unknown) => {
    const c = data as Cluster;
    return <>Cluster {c.name} recorded as <Ref to={`/clusters/${c.id}`} id={c.id} />.</>;
  };
  return (
    <>
      <h2>{ENTER_A_CLUSTER_BY_HAND}</h2>
      <PageNote parts={[BY_HAND_NOTE]} />
      <Form label={ENTER_A_CLUSTER_BY_HAND} onSubmit={submit} w={w} done={done}>
        <Text label="Name:" name="name" />
        <Text label="Kubernetes endpoint:" name="endpoint" hint="https://host or https://host:port" />
        <Text label="Talos contract:" name="contract" hint="a minor version, as v1.14" />
        <Text label="Talos cluster ID:" name="talosClusterId" hint="as talosctl get info prints it" />
      </Form>
    </>
  );
}

// Add machine's cluster choice. A cluster read that ended without data shows why, as the list
// views do (§8.2): its problem's title, detail and instance, or what the route accepts.
export function ClusterField({ clusters, chosen, choose }: { clusters: Load<Listed<Cluster>> | Dropped; chosen: string; choose: (id: string) => void }) {
  const pending = usePending(clusters, "clusters");
  const options = clusters.kind === "ok" ? clusters.data.items : [];
  return (
    <>
      <p>
        <label>Cluster:{" "}
          <select name="cluster" required value={chosen} onChange={(e) => choose(e.target.value)}>
            <option value="" disabled>{clusters.kind === "ok" ? "choose" : "clusters not read"}</option>
            {options.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
          </select>
        </label>
      </p>
      {clusters.kind !== "loading" && pending}
    </>
  );
}

export function RecordMachine({ query }: { query: string }) {
  const [w, send] = useSend();
  const [clusters] = useList<Cluster>("/clusters", "", true);
  // The operator's choice, kept while the cluster list is read again; the query only presets it.
  const [chosen, choose] = useState(() => new URLSearchParams(query).get("cluster") ?? "");
  const submit = (d: FormData) => {
    const cluster = field(d, "cluster");
    send({
      method: "POST", path: "/machines", resource: `/machines?${new URLSearchParams({ cluster })}`,
      body: { cluster, talosNodeId: field(d, "talosNodeId"), platform: field(d, "platform"), talosEndpoint: field(d, "talosEndpoint") },
    });
  };
  const done = (data: unknown) => {
    const m = data as Machine;
    return (
      <>
        Machine <Ref to={`/machines/${m.id}`} id={m.id} /> recorded in cluster {short(m.cluster)}. Endpoint {m.talosEndpoint}. Next: supply
        the cluster's first key with bronzewardctl cluster first-key, then import its configuration.
      </>
    );
  };
  return (
    <>
      <h2>{ADD_MACHINE}</h2>
      <PageNote parts={[ADD_MACHINE_NOTE]} />
      <Form label={ADD_MACHINE} onSubmit={submit} w={w} done={done}>
        <ClusterField clusters={clusters} chosen={chosen} choose={choose} />
        <Text label="Talos node ID:" name="talosNodeId" hint="as talosctl get identity prints it" />
        <p>
          <label>Platform:{" "}
            <select name="platform" required defaultValue="metal">
              {["metal", "container", "cloud"].map((p) => <option key={p}>{p}</option>)}
            </select>
          </label>
        </p>
        <Text label="Talos endpoint:" name="talosEndpoint" hint="an IP address; the port is 50000 unless given" />
      </Form>
    </>
  );
}

// On the machine's page (W3); `onChanged` reads the machine again.
export function ChangeEndpoint({ machine, onChanged }: { machine: Machine; onChanged: () => void }) {
  const [w, send] = useSend();
  const path = `/machines/${encodeURIComponent(machine.id)}`;
  const submit = (d: FormData) => send({ method: "POST", path: `${path}/talos-endpoints`, resource: path, body: { talosEndpoint: field(d, "talosEndpoint") } });
  const outcome = w?.outcome;
  const changed = outcome?.kind === "done";
  useEffect(() => {
    if (changed) onChanged();
  }, [changed, onChanged]);
  const done = (data: unknown) => {
    const c = data as EndpointChange;
    return <>Endpoint changed from {c.previousEndpoint} to {c.talosEndpoint} (revision {c.revision}).</>;
  };
  return (
    <Form title="Change endpoint" label="Change endpoint" onSubmit={submit} w={w} done={done} variant="secondary">
      <Text label="Talos endpoint:" name="talosEndpoint" value={machine.talosEndpoint} />
    </Form>
  );
}

// On the machine's page: name an unnamed machine, or rename a named one (client contracts A9 item 6). The grammar is
// the one MACHINE_NAME the views already read, tested before anything is sent; the server's refusals show as it words
// them, and `name-in-use` shows the machine that holds the name. `onChanged` reads the machine again.
export function RenameMachine({ machine, onChanged }: { machine: Machine; onChanged: () => void }) {
  const [w, send] = useSend();
  const [typed, notSent] = useState<string>();
  const current = machineName(machine.name);
  const label = current === undefined ? NAME_THIS_MACHINE : RENAME_MACHINE;
  const path = `/machines/${encodeURIComponent(machine.id)}`;
  const submit = (d: FormData) => {
    const name = field(d, "name").trim();
    if (!MACHINE_NAME.test(name)) return notSent(name);
    notSent(undefined);
    send({ method: "POST", path: `${path}/names`, resource: path, body: { name } });
  };
  const outcome = w?.outcome;
  const changed = outcome?.kind === "done";
  useEffect(() => {
    if (changed) onChanged();
  }, [changed, onChanged]);
  const done = (data: unknown) => {
    const c = data as NameChange;
    // Received names are remote text (A9 item 1): each is shown only when it matches the grammar.
    const to = machineName(c.name) ?? "the new name";
    const from = machineName(c.previousName);
    const said = c.previousName == null ? `Machine named ${to}` : from === undefined ? `Machine renamed to ${to}` : `Machine renamed from ${from} to ${to}`;
    return <>{said} (revision {c.revision}).</>;
  };
  const holder = outcome?.kind === "refused" && outcome.problem?.type === "urn:bronzeward:problem:name-in-use" ? outcome.problem["machine"] : undefined;
  return (
    <>
      <Form title={label} label={label} onSubmit={submit} w={w} done={done} variant="secondary">
        {/* Keyed by the machine's name: a name changed elsewhere, or by this form, replaces the field's text. */}
        <Text key={current ?? ""} label="Name:" name="name" hint={NAME_HINT} value={current ?? ""} />
      </Form>
      {typed !== undefined && <Status testid="rename-not-sent" role="alert">{notAName(typed)}</Status>}
      {typeof holder === "string" && <Status testid="rename-holder">The machine that has this name: <MachineById id={holder} />.</Status>}
      <p data-testid="rename-reaches">{RENAME_REACHES.join(" ")}</p>
    </>
  );
}

// The list pages' entries to the forms.
export const AddClusterAction = ({ variant }: { variant?: Variant } = {}) => {
  const { go } = useReads();
  return <Action label={ENTER_A_CLUSTER_BY_HAND} needs={["author"]} onClick={() => go("/clusters/new")} {...(variant !== undefined && { variant })} />;
};
export const RecordMachineAction = ({ cluster, variant }: { cluster: string; variant?: Variant }) => {
  const { go } = useReads();
  return <Action label={ADD_MACHINE} needs={["author"]} onClick={() => go(cluster === "" ? "/machines/new" : `/machines/new?${new URLSearchParams({ cluster })}`)} {...(variant !== undefined && { variant })} />;
};
