// The machine list (wireframe W2) and the clusters' list and items. The server filters machines
// by cluster; the browser filters them by state over all pages (G9). A machine is shown by its name
// where the body carries one and it passes the grammar, else by its address, else as an unnamed machine;
// its identifier is the link's title (G8, client contracts A9).

import type { components } from "./api/v1";
import { AddClusterAction, RecordMachineAction } from "./forms.tsx";
import { OnboardClusterAction } from "./onboard.tsx";
import { Detail, EmptyList, Head, Id, type Listed, More, Ref, RESTARTED, Scroll, short, useItem, useList, usePending } from "./list.tsx";
import { SAFE } from "./principals.ts";
import { Status } from "./states.tsx";
import { TalosAccessSign } from "./talosaccess.tsx";
import { UNNAMED_MACHINE, UNNAMED_MACHINE_HEADING } from "./words.ts";

export type Machine = components["schemas"]["MachineBody"];
export type Cluster = components["schemas"]["ClusterBody"];
type Draft = components["schemas"]["DraftBody"];

// The state filter's values, each a badge text.
export const STATES = [
  ["drifted", "Drifted"],
  ["frozen", "Frozen"],
  ["pending", "Pending convergence"],
  ["matches", "Matches applied"],
  ["not-handed-over", "Not handed over"],
] as const;

// A machine's badges, from the list's fields alone: openDrift is the only authority for drift, and a
// match is never health (journeys §2.4).
export function badges(m: Machine): string[] {
  const out: string[] = [];
  if (m.openDrift !== null) out.push("Drifted");
  if (m.frozen) out.push("Frozen");
  if (m.pendingConvergence) out.push("Pending convergence");
  if (m.desired === null && m.applied === null) out.push("Not handed over");
  else if (m.openDrift === null && !m.pendingConvergence && m.applied !== null && m.observed?.release === m.applied.release) out.push("Matches applied");
  return out;
}

// A reading's age at `now`, as W2 shows it, so a reading from another day never looks current. The
// list takes `now` when it renders, and a read again renders it anew. A time more than a minute ahead
// of this browser's clock is said so, never "just now", and a time that does not parse has no age.
function age(at: string, now: number): string {
  const ms = now - Date.parse(at);
  if (Number.isNaN(ms)) return "age unknown";
  if (ms < -60_000) return "ahead of this browser's clock";
  const min = Math.floor(ms / 60_000);
  if (min < 1) return "just now";
  if (min < 60) return `${min} min ago`;
  if (min < 1440) return `${Math.floor(min / 60)} h ago`;
  return `${Math.floor(min / 1440)} d ago`;
}

export function observed(m: Machine, now: number): string {
  const o = m.observed;
  if (o === null) return "none";
  const to = o.release ?? undefined;
  const what = to !== undefined && to === m.applied?.release ? "= applied" : to !== undefined && to === m.desired ? "= desired" : "no match";
  return `${what}, ${age(o.at, now)}`;
}

// G8: the node ID when the machine was recorded by it, else its SMBIOS UUID.
export const node = (m: Machine) => (m.hardware.talosNodeId != null ? `node ${m.hardware.talosNodeId}` : `UUID ${m.hardware.smbiosUuid ?? "unknown"}`);
export const release = (id: string | null) => <Id id={id} />;

// A9: a name is remote text, shown only when the received value matches the grammar (1 to 63 characters,
// lowercase letters, digits and hyphen, neither end a hyphen; no `m` flag, so `$` refuses a trailing line
// feed), else the machine is shown by its address. It replaces A7's control and format character test.
const MACHINE_NAME = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;
export const machineName = (name: unknown): string | undefined => (typeof name === "string" && MACHINE_NAME.test(name) ? name : undefined);

// What a display reads of a machine: a body, a list row, or the part of one a view holds.
export type MachineLike = { id: string; name?: unknown; talosEndpoint?: unknown; hardware?: Pick<Machine["hardware"], "talosNodeId" | "smbiosUuid"> };

// A9 item 1: an endpoint is remote text, shown only when it has the stored form (an IPv4 address or a
// bracketed IPv6 address, then a port; `0001_schema.sql` talos_endpoint_form). The port is dropped, and
// the brackets with it. A value of another shape shows nothing: it is split by the form and never on ":",
// which would cut an IPv6 address.
const ENDPOINT = /^(?:((?:\d{1,3}\.){3}\d{1,3})|\[([0-9a-f:.]+)\]):([1-9]\d{0,4})$/;
export function machineAddress(endpoint: unknown): string | undefined {
  if (typeof endpoint !== "string" || endpoint.length > 64) return undefined;
  const m = ENDPOINT.exec(endpoint);
  if (m === null || Number(m[3]) > 65535) return undefined;
  const v4 = m[1];
  if (v4 !== undefined) return v4.split(".").every((o) => Number(o) <= 255) ? v4 : undefined;
  return m[2];
}

// The node ID or UUID of G8 for a suffix, cut to `cut` characters when longer than eight: `node 4c1f…`,
// `UUID 9a3e…`. It is remote text like a name: the node ID, else the UUID, whichever is safe to show (A8 rule 3),
// else "unknown".
const SHORT = 4;
const nodeShort = (h: MachineLike["hardware"], cut = SHORT): string => {
  const safe = (v: unknown) => (typeof v === "string" && SAFE.test(v) ? v : undefined);
  const node = safe(h?.talosNodeId);
  const uuid = safe(h?.smbiosUuid);
  const [kind, value] = node !== undefined ? ["node", node] : uuid !== undefined ? ["UUID", uuid] : [h?.talosNodeId != null ? "node" : "UUID", "unknown"];
  return `${kind} ${value.length > Math.max(8, cut) ? `${value.slice(0, cut)}…` : value}`;
};

// The addresses that two or more unnamed machines of a list in hand share, each with the length its suffixes are
// cut to: four characters, or longer where machines of one address would otherwise read alike (VMware UUIDs
// share a prefix). Only such a list can tell: a pair on different pages, or a page that shows one machine, gets
// no suffix.
export function sharedAddresses(machines: readonly MachineLike[]): ReadonlyMap<string, number> {
  const groups = new Map<string, MachineLike[]>();
  for (const m of machines) {
    const address = machineName(m.name) === undefined ? machineAddress(m.talosEndpoint) : undefined;
    if (address !== undefined) groups.set(address, [...(groups.get(address) ?? []), m]);
  }
  const shared = new Map<string, number>();
  for (const [address, group] of groups) {
    if (group.length < 2) continue;
    // Lengthen until the suffixes differ; past the longest whole value no cut remains, and equal values stay equal.
    const longest = Math.max(...group.map((m) => nodeShort(m.hardware, 300).length));
    let cut = SHORT;
    while (cut < longest && new Set(group.map((m) => nodeShort(m.hardware, cut))).size < group.length) cut++;
    shared.set(address, cut);
  }
  return shared;
}

// A machine as text, where a title or a link names it (A8 rule 1, as ginsys/bronzeward#1055 amends it for the web): its
// name, else its address, with the shortened node ID or UUID where the list in hand holds two with that
// address, else "an unnamed machine". Never its identifier: that stays on the detail line.
export function machineText(m: MachineLike, shared?: ReadonlyMap<string, number>): string {
  const name = machineName(m.name);
  if (name !== undefined) return name;
  const address = machineAddress(m.talosEndpoint);
  if (address === undefined) return UNNAMED_MACHINE;
  const cut = shared?.get(address);
  return cut === undefined ? address : `${address} (${nodeShort(m.hardware, cut)})`;
}

// A machine page's heading: "Machine cp1", "Machine 172.31.104.15", or "Unnamed machine".
export function machineHeading(m: MachineLike): string {
  const text = machineText(m);
  return text === UNNAMED_MACHINE ? UNNAMED_MACHINE_HEADING : `Machine ${text}`;
}

// A choice in a list of machines: the text, and the endpoint beside it where the text is a name and the endpoint
// has its stored form (A9 item 1).
export function machineOption(m: MachineLike, shared?: ReadonlyMap<string, number>): string {
  const text = machineText(m, shared);
  return machineName(m.name) === undefined || machineAddress(m.talosEndpoint) === undefined ? text : `${text} ${String(m.talosEndpoint)}`;
}

// A machine's link: the text above, the whole identifier the title. The one display of a machine for every view.
export const MachineRef = ({ machine, shared }: { machine: MachineLike; shared?: ReadonlyMap<string, number> }) => (
  <Ref to={`/machines/${machine.id}`} id={machine.id}>{machineText(machine, shared)}</Ref>
);

export function Filter({ label, value, options, onChange }: { label: string; value: string; options: readonly (readonly [string, string])[]; onChange: (v: string) => void }) {
  return (
    <label>
      {label}{" "}
      <select value={value} onChange={(e) => onChange(e.target.value)}>
        <option value="">all</option>
        {options.map(([v, t]) => <option key={v} value={v}>{t}</option>)}
      </select>{" "}
    </label>
  );
}

export function Machines({ query, go }: { query: string; go: (to: string) => void }) {
  const q = new URLSearchParams(query);
  const cluster = q.get("cluster") ?? "";
  const state = STATES.find(([v]) => v === q.get("state"));
  const [list, more, busy] = useList<Machine>("/machines", cluster === "" ? "" : new URLSearchParams({ cluster }).toString(), state !== undefined);
  const [clusters] = useList<Cluster>("/clusters", "", true);
  const unread = usePending(clusters, "clusters");
  const options: (readonly [string, string])[] = clusters.kind === "ok" ? clusters.data.items.map((c) => [c.id, c.name]) : [];
  // The selected cluster keeps its option while the clusters are not read, so the filter never
  // says "all" over a filtered list.
  if (cluster !== "" && !options.some(([id]) => id === cluster)) options.push([cluster, short(cluster)]);
  let body = usePending(list, "machines");
  const filter = (k: string, v: string) => {
    if (v === "") q.delete(k);
    else q.set(k, v);
    go(q.size > 0 ? `/machines?${q}` : "/machines");
  };
  if (list.kind === "ok") {
    const now = Date.now();
    const shown = list.data.items.filter((m) => state === undefined || badges(m).includes(state[1]));
    const shared = sharedAddresses(list.data.items);
    body = shown.length === 0 && more === undefined
      ? <EmptyList list={list.data} text={cluster === "" && state === undefined ? "No machines recorded yet." : "No machine matches this filter."} />
      : (
        <>
          <Scroll label="Machines">
            <Head cols={["Machine", "Endpoint", "Node", "Desired", "Applied (source)", "Observed", "State"]} />
            <tbody>
              {shown.map((m) => (
                <tr key={m.id}>
                  <th scope="row"><MachineRef machine={m} shared={shared} /></th>
                  <td>{m.talosEndpoint}</td>
                  <td>{node(m)}</td>
                  <td>{release(m.desired)}</td>
                  <td>{m.applied === null ? "none" : <>{release(m.applied.release)} ({m.applied.source})</>}</td>
                  <td title={m.observed?.at}>{observed(m, now)}</td>
                  <td>{badges(m).join(", ")}</td>
                </tr>
              ))}
            </tbody>
          </Scroll>
          <More list={list.data} shown={shown.length} more={more} busy={busy} />
        </>
      );
  }
  return (
    <>
      <h2>Machines</h2>
      <div className="toolbar">
        <Filter label="Cluster:" value={cluster} options={options} onChange={(v) => filter("cluster", v)} />
        {clusters.kind !== "loading" && unread}
        <Filter label="State:" value={state?.[0] ?? ""} options={STATES} onChange={(v) => filter("state", v)} />
        <RecordMachineAction cluster={cluster} />
        <AddClusterAction />
        <OnboardClusterAction />
      </div>
      {body}
      <p>Health is not checked.</p>
    </>
  );
}

export function Clusters() {
  const [list, more, busy] = useList<Cluster>("/clusters", "", false);
  let body = usePending(list, "clusters");
  if (list.kind === "ok") {
    body = list.data.items.length === 0 ? <EmptyList list={list.data} text="No clusters recorded yet." /> : (
      <>
        <Scroll label="Clusters">
          <Head cols={["Cluster", "Kubernetes endpoint", "Contract", "Talos cluster ID"]} />
          <tbody>
            {list.data.items.map((c) => (
              <tr key={c.id}>
                <th scope="row"><Ref to={`/clusters/${c.id}`} id={c.id}>{c.name}</Ref></th>
                <td>{c.endpoint}</td>
                <td>{c.contract}</td>
                <td>{c.talosClusterId}</td>
              </tr>
            ))}
          </tbody>
        </Scroll>
        <More list={list.data} shown={list.data.items.length} more={more} busy={busy} />
      </>
    );
  }
  return <><h2>Clusters</h2><div className="toolbar"><AddClusterAction /><OnboardClusterAction /></div>{body}</>;
}

export function ClusterItem({ id }: { id: string }) {
  const c = useItem<Cluster>(`/clusters/${encodeURIComponent(id)}`);
  const pending = usePending(c, "the cluster");
  if (c.kind !== "ok") return <><h2>Cluster {short(id)}</h2>{pending}</>;
  const d = c.data;
  return (
    <>
      <h2>Cluster {d.name}</h2>
      <Detail id={d.id} />
      <p>Kubernetes endpoint {d.endpoint}, contract {d.contract}, Talos cluster ID {d.talosClusterId}.</p>
      <p><Ref to={`/machines?cluster=${d.id}`} id={d.id}>Machines in this cluster</Ref></p>
      <TalosAccessSign cluster={d.id} />
      <ClusterMachines id={d.id} />
      <ClusterDrafts id={d.id} />
    </>
  );
}

// How many of a cluster's machines carry each badge (a machine can carry several), and how many show
// none. Counts follow the order of STATES; a badge no machine carries is left out.
export function machineStates(machines: readonly Machine[]) {
  const counts = STATES.map(([, text]) => [text, machines.filter((m) => badges(m).includes(text)).length] as const).filter(([, n]) => n > 0);
  return { total: machines.length, counts, none: machines.filter((m) => badges(m).length === 0).length };
}

// A list read again from its start after the server refused its position says so (§8, "Epoch changed").
export const Restarted = ({ list }: { list: Listed<unknown> }) => (list.restarted ? <Status testid="restarted">{RESTARTED}</Status> : null);

export function MachineSummary({ machines }: { machines: readonly Machine[] }) {
  if (machines.length === 0) return <Status>No machines recorded in this cluster.</Status>;
  const { total, counts, none } = machineStates(machines);
  const shared = sharedAddresses(machines);
  const parts = [...counts.map(([text, n]) => `${n} ${text}`), ...(none > 0 ? [`${none} with no state shown`] : [])];
  return (
    <>
      <p>{total} {total === 1 ? "machine" : "machines"}: {parts.join(", ")}. A machine can carry several states, so the counts can add up to more than {total}.</p>
      <Scroll label="Machines of this cluster">
        <Head cols={["Machine", "Endpoint", "Node", "State"]} />
        <tbody>
          {machines.map((m) => (
            <tr key={m.id}>
              <th scope="row"><MachineRef machine={m} shared={shared} /></th>
              <td>{m.talosEndpoint}</td>
              <td>{node(m)}</td>
              <td>{badges(m).join(", ")}</td>
            </tr>
          ))}
        </tbody>
      </Scroll>
    </>
  );
}

function ClusterMachines({ id }: { id: string }) {
  const [list] = useList<Machine>("/machines", new URLSearchParams({ cluster: id }).toString(), true);
  const pending = usePending(list, "machines");
  return <div className="panel"><h3>Machines</h3>{list.kind === "ok" ? <><Restarted list={list.data} /><MachineSummary machines={list.data.items} /></> : pending}</div>;
}

// GET /drafts has no cluster filter, so a cluster's open drafts are picked in the browser from every
// page of the list, as the Changes page does.
export const openDrafts = (drafts: readonly Draft[], cluster: string) => drafts.filter((d) => d.cluster === cluster && d.state === "open");

export function OpenDraftsView({ drafts }: { drafts: readonly Draft[] }) {
  if (drafts.length === 0) return <Status>No open draft in this cluster.</Status>;
  return (
    <Scroll label="Open drafts of this cluster">
      <Head cols={["Draft", "Title", "Revision"]} />
      <tbody>
        {drafts.map((d) => (
          <tr key={d.id}><th scope="row"><Ref to={`/drafts/${d.id}`} id={d.id} /></th><td>{d.title}</td><td>{d.revision}</td></tr>
        ))}
      </tbody>
    </Scroll>
  );
}

function ClusterDrafts({ id }: { id: string }) {
  const [list] = useList<Draft>("/drafts", "", true);
  const pending = usePending(list, "drafts");
  return <div className="panel"><h3>Open drafts</h3>{list.kind === "ok" ? <><Restarted list={list.data} /><OpenDraftsView drafts={openDrafts(list.data.items, id)} /></> : pending}</div>;
}
