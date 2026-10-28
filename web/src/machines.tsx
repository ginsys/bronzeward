// The machines (wireframe W2) and the clusters, their lists and items. The server filters machines
// by cluster; the browser filters them by state over all pages (G9). Machines have no name, so
// they show endpoint and node ID (G8).

import type { components } from "./api/v1";
import { More, Ref, short, useItem, useList, usePending } from "./list.tsx";
import { Empty } from "./states.tsx";

export type Machine = components["schemas"]["MachineBody"];
export type Cluster = components["schemas"]["ClusterBody"];

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
// list takes `now` when it renders, and a read again renders it anew.
function age(at: string, now: number): string {
  const min = Math.floor((now - Date.parse(at)) / 60_000);
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
const node = (m: Machine) => (m.hardware.talosNodeId != null ? `node ${m.hardware.talosNodeId}` : `UUID ${m.hardware.smbiosUuid ?? "unknown"}`);
// A release's link comes with its view (#334's third part).
const release = (id: string | null) => (id === null ? "none" : <span title={id}>{short(id)}</span>);

function Filter({ label, value, options, onChange }: { label: string; value: string; options: readonly (readonly [string, string])[]; onChange: (v: string) => void }) {
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

const Head = ({ cols }: { cols: string[] }) => <thead><tr>{cols.map((c) => <th key={c} scope="col">{c}</th>)}</tr></thead>;

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
    body = shown.length === 0 && more === undefined
      ? <Empty text={cluster === "" && state === undefined ? "No machines recorded yet." : "No machine matches this filter."} />
      : (
        <>
          <table>
            <Head cols={["Machine", "Endpoint", "Node", "Desired", "Applied (source)", "Observed", "State"]} />
            <tbody>
              {shown.map((m) => (
                <tr key={m.id}>
                  <th scope="row"><Ref to={`/machines/${m.id}`} id={m.id} /></th>
                  <td>{m.talosEndpoint}</td>
                  <td>{node(m)}</td>
                  <td>{release(m.desired)}</td>
                  <td>{m.applied === null ? "none" : <>{release(m.applied.release)} ({m.applied.source})</>}</td>
                  <td title={m.observed?.at}>{observed(m, now)}</td>
                  <td>{badges(m).join(", ")}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <More list={list.data} shown={shown.length} more={more} busy={busy} />
        </>
      );
  }
  return (
    <>
      <h2>Machines</h2>
      <Filter label="Cluster:" value={cluster} options={options} onChange={(v) => filter("cluster", v)} />
      {clusters.kind !== "loading" && unread}
      <Filter label="State:" value={state?.[0] ?? ""} options={STATES} onChange={(v) => filter("state", v)} />
      {body}
      <p>Health is not checked.</p>
    </>
  );
}

// The identity half of W3; its state comes with the detail view (#334's second part).
export function MachineItem({ id }: { id: string }) {
  const m = useItem<Machine>(`/machines/${encodeURIComponent(id)}`);
  const pending = usePending(m, "the machine");
  if (m.kind !== "ok") return <><h2>Machine {short(id)}</h2>{pending}</>;
  const d = m.data;
  return (
    <>
      <h2>Machine {d.id}</h2>
      <p>Cluster <Ref to={`/clusters/${d.cluster}`} id={d.cluster} />, {d.talosEndpoint}, {node(d)}, {d.platform}.</p>
    </>
  );
}

export function Clusters() {
  const [list, more, busy] = useList<Cluster>("/clusters", "", false);
  let body = usePending(list, "clusters");
  if (list.kind === "ok") {
    body = list.data.items.length === 0 ? <Empty text="No clusters recorded yet." /> : (
      <>
        <table>
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
        </table>
        <More list={list.data} shown={list.data.items.length} more={more} busy={busy} />
      </>
    );
  }
  return <><h2>Clusters</h2>{body}</>;
}

export function ClusterItem({ id }: { id: string }) {
  const c = useItem<Cluster>(`/clusters/${encodeURIComponent(id)}`);
  const pending = usePending(c, "the cluster");
  if (c.kind !== "ok") return <><h2>Cluster {short(id)}</h2>{pending}</>;
  const d = c.data;
  return (
    <>
      <h2>Cluster {d.name}</h2>
      <p>{d.id}: Kubernetes endpoint {d.endpoint}, contract {d.contract}, Talos cluster ID {d.talosClusterId}.</p>
      <p><Ref to={`/machines?cluster=${d.id}`} id={d.id}>Machines in this cluster</Ref></p>
    </>
  );
}
