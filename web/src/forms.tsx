// The first write views (ginsys/bronzeward#338): Add cluster, Record machine and Change endpoint
// (journeys §5.2 and §10), each a human author's act, over the write layer of write.ts. Also the
// notice of a write whose outcome is unknown and the leave guards (#257 B11).

import { createContext, type FormEvent, type ReactNode, useContext, useEffect, useRef, useState, useSyncExternalStore } from "react";
import type { components } from "./api/v1";
import { Ref, short, useList, useReads } from "./list.tsx";
import type { Cluster, Machine } from "./machines.tsx";
import { Action } from "./shell.tsx";
import { problemLine, Refused, Repeated, REPLAYED, SetAside, Status, Unknown } from "./states.tsx";
import { type Outcome, Write, type Writes, type WriteSpec } from "./write.ts";

type EndpointChange = components["schemas"]["EndpointChangeBody"];

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
const detail = (w: Write) => ` Key ${w.key}, ${w.route}. To learn the outcome, read ${w.spec.resource}.`;

// A settled [Repeat exactly]'s result, as its notice shows it (journeys §8, the Replayed row).
function settled(o: Outcome): string {
  const earlier = "replayed" in o && o.replayed ? `${REPLAYED} ` : "";
  const fields = "problem" in o && o.problem !== null ? ` ${problemLine(o.problem)}` : "";
  switch (o.kind) {
    case "done":
      return `${earlier}Saved (the server answered ${o.status}).`;
    case "refused":
      return `${earlier}Not saved: the server answered ${o.status}.${fields}`;
    case "not-done":
      return `Not saved: ${o.why}.${fields}`;
    default:
      return `Not saved: ${o.why}.`;
  }
}

// Shown as soon as a write's outcome is unknown, until dismissed (B11), and after a [Repeat exactly]
// settles, with its result, until dismissed. `go` opens the resource.
export function WriteNotices({ go }: { go: (to: string) => void }) {
  const writes = useWrites();
  return writes?.notices.map((w) => {
    const o = w.outcome;
    const props = { onShow: () => go(w.spec.resource), detail: detail(w), onDismiss: () => writes.dismiss(w), testid: "write-notice" };
    if (o !== undefined && w.repeated) return <Repeated key={w.key} line={settled(o)} {...props} detail={` Key ${w.key}, ${w.route}. Read ${w.spec.resource}.`} />;
    if (o?.kind === "unknown") return <Unknown key={w.key} why={o.why} onRepeat={writes.repeatable(w) ? () => void writes.submit(w) : undefined} {...props} />;
    if (o?.kind === "set-aside") return <SetAside key={w.key} why={o.why} {...props} />;
    // Set aside while its answer is still on its way: shown at once, with no dismissal until that
    // answer settles it (§8.5 item 2).
    if (o === undefined && w.aside !== undefined) return <SetAside key={w.key} why={w.aside} {...props} onDismiss={undefined} />;
    // [Repeat exactly] in flight: the earlier attempt stays unknown, and its notice stays.
    if (o === undefined && w.repeating !== undefined) return <Unknown key={w.key} why={w.repeating.why} repeating {...props} />;
    // An attempt that may have committed, while the automatic repeats run: unknown already (B11).
    if (o === undefined && w.doubt !== undefined) return <Unknown key={w.key} why={w.doubt} repeating {...props} />;
    return null;
  });
}

// The in-app router's guard: a move to another page while a write is pending or unknown asks first,
// repeating the notices. The writes stay, and so do their notices. A notice's own [Show current
// state] (`notice`, W11) is the notice's way out: it asks nothing on the writes' account. `from` is
// the page shown, which Back and Forward have already left in `location` when they ask.
export function leaving(writes: Writes, to: string, notice = false, from = location.pathname): boolean {
  const open = notice ? [] : writes.open;
  if (open.length === 0 || new URL(to, location.href).pathname === new URL(from, location.href).pathname) return true;
  const lines = open.map((w) => (w.outcome === undefined && w.doubt === undefined && w.repeating === undefined ? "Sent, with no answer yet." : "Unknown whether this was saved.") + detail(w));
  return confirm(`${lines.join("\n")}\nLeave this page?`);
}

// A reload or a tab close drops every pending write, so it is guarded by the browser's own dialog,
// unless the page is being left by a notice's own [Show current state]. That exemption covers one
// unload only: if the operator stops the navigation, the next reload or close asks again.
export function guardsUnload(writes: Writes): boolean {
  const exempt = writes.unloading;
  writes.unloading = false;
  return writes.open.length > 0 && !exempt;
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
function useSend(): [Write | undefined, (spec: WriteSpec) => void] {
  const writes = useWrites()!;
  const current = useRef<Write>(undefined);
  const [, shown] = useState<Write>();
  const send = (spec: WriteSpec) => {
    const w = current.current;
    if (w !== undefined && writes.open.includes(w)) {
      if (w.outcome === undefined) void writes.submit(w);
      return;
    }
    const next = new Write(spec);
    current.current = next;
    shown(next);
    void writes.submit(next);
  };
  return [current.current, send];
}

// The form's own line for its write; an unknown outcome is the notice's.
export function Result({ w, done }: { w: Write | undefined; done: (data: unknown) => ReactNode }) {
  const o: Outcome | undefined = w?.outcome;
  if (w === undefined) return null;
  if (o === undefined) return <Status testid="write-result">Saving</Status>;
  switch (o.kind) {
    case "done":
      // A success whose body could not be read is still saved: it names the page to read, and is
      // never repeated.
      if (typeof o.data !== "object" || o.data === null) {
        return <Status testid="write-result">{o.replayed && `${REPLAYED} `}Saved (the server answered {o.status}), but its answer could not be read. Read <Ref to={w.spec.resource} id={w.spec.resource}>{w.spec.resource}</Ref>.</Status>;
      }
      return <Status testid="write-result">{o.replayed && `${REPLAYED} `}{done(o.data)}</Status>;
    case "refused":
      return <Refused testid="write-result" problem={o.problem} status={o.status} verb="saved" replayed={o.replayed} />;
    case "not-done":
      // The last answer's problem fields, when it carried any (§8.2).
      return <Status testid="write-result">Not saved: {o.why}.{o.problem !== null && ` ${problemLine(o.problem)}`}</Status>;
    case "set-aside":
    case "unknown":
      // Set aside before any attempt that may have committed, nothing was; otherwise the notice says.
      if (o.kind === "set-aside" && !o.ambiguous) return <Status testid="write-result">Not saved: {o.why}. Nothing of it was committed.</Status>;
      return <Status testid="write-result">See the notice at the top of the page: its key and what to read.</Status>;
  }
}

function Form({ title, label, onSubmit, w, done, children }: { title?: string; label: string; onSubmit: (data: FormData) => void; w: Write | undefined; done: (data: unknown) => ReactNode; children: ReactNode }) {
  const writes = useWrites()!;
  const held = w !== undefined && writes.open.includes(w);
  const submit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    onSubmit(new FormData(e.currentTarget));
  };
  return (
    <form onSubmit={submit} aria-label={title ?? label}>
      {children}
      <p><Action label={label} needs={["author"]} submit busy={held} /></p>
      <Result w={w} done={done} />
    </form>
  );
}

const field = (data: FormData, name: string) => String(data.get(name) ?? "");
const Text = ({ label, name, hint, value }: { label: string; name: string; hint?: string; value?: string }) => (
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
      <h2>Add cluster</h2>
      <Form label="Add cluster" onSubmit={submit} w={w} done={done}>
        <Text label="Name:" name="name" />
        <Text label="Kubernetes endpoint:" name="endpoint" hint="https://host or https://host:port" />
        <Text label="Talos contract:" name="contract" hint="a minor version, as v1.13" />
        <Text label="Talos cluster ID:" name="talosClusterId" hint="as talosctl get info prints it" />
      </Form>
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
        Machine <Ref to={`/machines/${m.id}`} id={m.id} /> recorded in cluster {short(m.cluster)}. Endpoint {m.talosEndpoint}. Next: give
        Bronzeward the cluster's Talos access in the provider, outside Bronzeward, then import its configuration.
      </>
    );
  };
  const options = clusters.kind === "ok" ? clusters.data.items : [];
  return (
    <>
      <h2>Record machine</h2>
      <Form label="Record machine" onSubmit={submit} w={w} done={done}>
        <p>
          <label>Cluster:{" "}
            <select name="cluster" required value={chosen} onChange={(e) => choose(e.target.value)}>
              <option value="" disabled>{clusters.kind === "ok" ? "choose" : "clusters not read"}</option>
              {options.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
            </select>
          </label>
        </p>
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
    <Form title="Change endpoint" label="Change endpoint" onSubmit={submit} w={w} done={done}>
      <Text label="Talos endpoint:" name="talosEndpoint" value={machine.talosEndpoint} />
    </Form>
  );
}

// The list pages' entries to the forms.
export const AddClusterAction = () => {
  const { go } = useReads();
  return <Action label="Add cluster" needs={["author"]} onClick={() => go("/clusters/new")} />;
};
export const RecordMachineAction = ({ cluster }: { cluster: string }) => {
  const { go } = useReads();
  return <Action label="Record machine" needs={["author"]} onClick={() => go(cluster === "" ? "/machines/new" : `/machines/new?${new URLSearchParams({ cluster })}`)} />;
};
