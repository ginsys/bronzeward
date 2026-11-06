// The read-only lists and items besides the machines (#334): sources and their revisions,
// releases, plans, operations, acts and dependencies with their releases and alerts. Every
// resource has its path (B3) and every identifier links to its view where it has one. The release
// review, plan and drift actions, and the operation watch come with their own tickets.

import { Fragment as Group, type ReactNode, useRef } from "react";
import type { components } from "./api/v1";
import { CreateDraft, DraftItem, Drafts } from "./drafts.tsx";
import { AddCluster, RecordMachine } from "./forms.tsx";
import { IngestionItem, StartImport } from "./imports.tsx";
import { EmptyList, Head, Id, type Listed, More, Ref, Scroll, short, useItem, useList, usePending } from "./list.tsx";
import { MachineItem } from "./machine.tsx";
import { ClusterItem, Clusters, Filter, Machines } from "./machines.tsx";
import { RecoveryConsole } from "./recovery.tsx";
import { Action } from "./shell.tsx";
import { when } from "./states.tsx";

type S = components["schemas"];
type Col<T> = readonly [string, (t: T) => ReactNode];

// The filter values each list route accepts.
export const PLAN_STATES = ["proposed", "approved", "committed", "revoked", "cancelled", "expired"];
export const OPERATION_STATES = ["queued", "running", "succeeded", "failed", "committed", "sending", "verifying", "completed", "rejected", "cancelled", "unresolved"];
export const OPERATION_KINDS = ["publish", "ingest", "apply-config", "adopt"];
export const INGESTION_STATES = ["held", "paused", "resumed", "released", "abandoned"];
export const DEPENDENCY_CLASSES = ["retained", "unknown", "blocked", "lost"];
const choices = (values: string[]) => values.map((v) => [v, v] as const);

function Rows<T>({ label, cols, items, keyOf }: { label: string; cols: readonly Col<T>[]; items: readonly T[]; keyOf: (t: T) => string }) {
  return (
    <Scroll label={label}>
      <Head cols={cols.map(([h]) => h)} />
      <tbody>
        {items.map((t) => (
          <tr key={keyOf(t)}>{cols.map(([h, cell], i) => (i === 0 ? <th key={h} scope="row">{cell(t)}</th> : <td key={h}>{cell(t)}</td>))}</tr>
        ))}
      </tbody>
    </Scroll>
  );
}

const byId = (t: unknown) => (t as { id: string }).id;

// A list read a page at a time, with its empty text and, above the table, what the last page adds.
function ListOf<T>({ label, path, query = "", cols, empty, keyOf = byId, head }: {
  label: string; path: string; query?: string; cols: readonly Col<T>[]; empty: string; keyOf?: (t: T) => string; head?: (l: Listed<T>) => ReactNode;
}) {
  const [list, more, busy] = useList<T>(path, query, false);
  const pending = usePending(list, label.toLowerCase());
  if (list.kind !== "ok") return pending;
  const { items } = list.data;
  return (
    <>
      {head?.(list.data)}
      {items.length === 0 && more === undefined ? <EmptyList list={list.data} text={empty} /> : (
        <><Rows label={label} cols={cols} items={items} keyOf={keyOf} /><More list={list.data} shown={items.length} more={more} busy={busy} /></>
      )}
    </>
  );
}

function ItemOf<T>({ path, what, title, children }: { path: string; what: string; title: (t: T | undefined) => ReactNode; children: (t: T) => ReactNode }) {
  const item = useItem<T>(path);
  const pending = usePending(item, what);
  const t = item.kind === "ok" ? item.data : undefined;
  return <><h2>{title(t)}</h2>{t === undefined ? pending : children(t)}</>;
}

type Row = readonly [string, ReactNode];
const Fields = ({ rows }: { rows: readonly Row[] }) => <dl>{rows.map(([k, v]) => <Group key={k}><dt>{k}</dt><dd>{v}</dd></Group>)}</dl>;
const Ids = ({ ids }: { ids: readonly string[] | null }) =>
  ids === null || ids.length === 0 ? "none" : ids.map((id, i) => <Group key={id}>{i > 0 && ", "}<Id id={id} /></Group>);
const by = (c: { principal: string; role: string } | null) => (c === null ? "none" : <><Id id={c.principal} /> ({c.role})</>);
const cap = (s: string) => s[0]!.toUpperCase() + s.slice(1);
export const seconds = (n: number | null) => (n === null ? "none" : n % 60 === 0 && n > 0 ? `${n / 60} min` : `${n} s`);

// The page's filters live in its query (B3); setting or clearing one is a navigation.
function useFilters(base: string, query: string, go: (to: string) => void) {
  const q = new URLSearchParams(query);
  const set = (k: string, v: string) => {
    const n = new URLSearchParams(query);
    if (v === "") n.delete(k);
    else n.set(k, v);
    go(n.size > 0 ? `${base}?${n}` : base);
  };
  // The query sent to the server: only the filters its list takes.
  const sent = (keys: string[]) => new URLSearchParams([...q].filter(([k]) => keys.includes(k))).toString();
  return { q, set, sent };
}

// A filter set by a link from another view, such as a machine's plans, with the way to clear it.
// Clearing it removes its button, so focus first moves on to the page's next control: a filter
// change keeps focus on the filters (WCAG 2.4.3).
function Pinned({ label, id, clear }: { label: string; id: string | null; clear: () => void }) {
  const own = useRef<HTMLParagraphElement>(null);
  if (id === null) return null;
  const all = () => {
    nextControl(own.current)?.focus();
    clear();
  };
  return <p ref={own}>{label} <Id id={id} /> <button type="button" onClick={all}>Show all</button></p>;
}

// The first control after `from` and outside it, else the page.
function nextControl(from: HTMLElement | null): HTMLElement | null {
  const page = from?.closest<HTMLElement>("main") ?? null;
  if (from === null || page === null) return null;
  const controls = [...page.querySelectorAll<HTMLElement>("a[href], button, input, select, textarea")];
  return controls.find((c) => !from.contains(c) && (from.compareDocumentPosition(c) & Node.DOCUMENT_POSITION_FOLLOWING) !== 0) ?? page;
}

const CREATED: Col<{ createdAt: string }> = ["Created", (t) => when(t.createdAt)];
const CLUSTER: Col<{ cluster: string }> = ["Cluster", (t) => <Id id={t.cluster} />];
// A head's current revision, null once the head is removed.
const CURRENT: Col<{ revision: string | null }> = ["Current revision", (t) => <Id id={t.revision} none="removed" />];
const named = (kind: string): Col<{ id: string; name: string }> => [cap(kind), (t) => <Ref to={`/${kind}s/${t.id}`} id={t.id}>{t.name}</Ref>];

const FRAGMENTS: readonly Col<S["FragmentBody"]>[] = [named("fragment"), CLUSTER, ["Scope", (f) => f.scope], ["Layer", (f) => f.layer], CURRENT, CREATED];
const PROFILES: readonly Col<S["ProfileBody"]>[] = [named("profile"), CLUSTER, ["Scope", (p) => p.scope], CURRENT, CREATED];
const ASSIGNMENTS: readonly Col<S["AssignmentBody"]>[] = [["Assignment", (a) => <Id id={a.id} />], CLUSTER, ["Machine", (a) => <Id id={a.machine} />], CURRENT, CREATED];

// Never a colour alone: the class and its staleness are words.
export const klass = (d: { class: string | null; stale?: boolean }) => `${d.class ?? "none"}${d.stale === true ? " (stale: not observed recently)" : ""}`;
const DEPENDENCIES: readonly Col<S["DependencyBody"]>[] = [
  ["Dependency", (d) => <Id id={d.id} />], ["Provider", (d) => d.provider], ["Object", (d) => `${d.object}, version ${d.version}`],
  ["Class", klass], ["Observed from", (d) => when(d.observedFrom)],
];
const ALERTS: readonly Col<S["AlertBody"]>[] = [
  ["Alert", (a) => <Id id={a.id} />], ["Kind", (a) => a.kind], ["Dependency", (a) => <Id id={a.dependency} />], ["Class", klass],
  ["Reason", (a) => a.reason ?? "none"], ["Releases", (a) => <Ids ids={a.releases} />], ["Recorded", (a) => when(a.recordedAt)],
];
const lastPass = (l: Listed<unknown>) => {
  const at = l.extra?.["lastPass"];
  return <p data-testid="last-pass">{typeof at === "string" ? `Last completed monitor pass: ${when(at)}.` : "No monitor pass has completed yet."}</p>;
};

export function Sources({ query, go }: { query: string; go: (to: string) => void }) {
  const { q, set, sent } = useFilters("/sources", query, go);
  const cls = q.get("class") ?? "";
  return (
    <>
      <h2>Sources</h2>
      <p>What releases are built from. Sources are not configuration: a machine's configuration is a release's.</p>
      <h3>Fragments</h3>
      <ListOf label="Fragments" path="/fragments" cols={FRAGMENTS} empty="No fragments recorded yet." />
      <h3>Profiles</h3>
      <ListOf label="Profiles" path="/profiles" cols={PROFILES} empty="No profiles recorded yet." />
      <h3>Assignments</h3>
      <ListOf label="Assignments" path="/assignments" cols={ASSIGNMENTS} empty="No assignments recorded yet." />
      <h3>Dependencies</h3>
      <Filter label="Class:" value={cls} options={choices(DEPENDENCY_CLASSES)} onChange={(v) => set("class", v)} />
      <ListOf label="Dependencies" path="/dependencies" query={sent(["class"])} cols={DEPENDENCIES} head={lastPass}
        empty={cls === "" ? "No dependencies monitored yet." : "No dependency of this class."} />
      <h3>Dependency alerts</h3>
      <ListOf label="Dependency alerts" path="/dependency-alerts" cols={ALERTS} empty="No dependency alerts raised." />
    </>
  );
}

type Source = S["FragmentBody"] | S["ProfileBody"] | S["AssignmentBody"];
type Revision = { id: string; author: string; createdAt: string };
const REVISIONS: readonly Col<Revision>[] = [["Revision", (r) => <Id id={r.id} />], ["Author", (r) => <Id id={r.author} />], CREATED];
type Kind = "fragment" | "profile" | "assignment";

export function SourceItem({ kind, id }: { kind: Kind; id: string }) {
  const path = `/${kind}s/${encodeURIComponent(id)}`;
  return (
    <ItemOf<Source> path={path} what={`the ${kind}`} title={(s) => `${cap(kind)} ${s !== undefined && "name" in s ? s.name : short(id)}`}>
      {(s) => (
        <>
          <Fields rows={[
            ["Identifier", s.id], ["Cluster", <Id id={s.cluster} />],
            ...("scope" in s ? [["Scope", s.scope] as const] : []), ...("layer" in s ? [["Layer", s.layer] as const] : []),
            ...("machine" in s ? [["Machine", <Id id={s.machine} />] as const] : []),
            ["Current revision", <Id id={s.revision} none="removed" />], ["Head revision", s.headRevision], ["Created", when(s.createdAt)],
          ]} />
          <h3>Revisions</h3>
          <ListOf<Revision> label="Revisions" path={`${path}/revisions`} cols={REVISIONS} empty="No revisions recorded." />
        </>
      )}
    </ItemOf>
  );
}

type AnyRevision = S["FragmentRevisionBody"] | S["ProfileRevisionBody"] | S["AssignmentRevisionBody"];
const REFERENCES: readonly Col<[string, S["Reference"]]>[] = [
  ["Reference", ([name]) => name], ["Kind", ([, r]) => r.kind], ["Version", ([, r]) => r.version], ["Encoding", ([, r]) => r.encoding ?? "none"],
];

export function RevisionItem({ kind, id }: { kind: Kind; id: string }) {
  return (
    <ItemOf<AnyRevision> path={`/${kind}-revisions/${encodeURIComponent(id)}`} what={`the ${kind} revision`}
      title={(r) => `${cap(kind)} revision ${short(id)}${r !== undefined && "name" in r ? ` of ${r.name}` : ""}`}>
      {(r) => (
        <>
          <Fields rows={[
            ["Identifier", r.id], ["Cluster", <Id id={r.cluster} />], ...("layer" in r ? [["Layer", r.layer] as const] : []),
            ...("machine" in r ? [["Machine", <Id id={r.machine} />] as const] : []),
            ...("profiles" in r ? [["Profiles", r.profiles.join(", ") || "none"] as const] : []),
            ...("fragments" in r ? [["Fragments", Array.isArray(r.fragments) ? <Ids ids={r.fragments} />
              : Object.entries(r.fragments).map(([layer, names]) => `${layer}: ${names.join(", ")}`).join("; ") || "none"] as const] : []),
            ["Author", <Id id={r.author} />], ["Created", when(r.createdAt)],
          ]} />
          {"document" in r && (
            <>
              <h3>Document</h3>
              <pre data-testid="document">{r.document}</pre>
              <h3>Declared references</h3>
              {Object.keys(r.declarations.references).length === 0 ? <p>No references declared.</p>
                : <Rows label="Declared references" cols={REFERENCES} items={Object.entries(r.declarations.references)} keyOf={([n]) => n} />}
              <p>Embedded documents: {r.declarations.embedded.map((e) => `${e.path} (${e.format})`).join(", ") || "none"}.</p>
            </>
          )}
        </>
      )}
    </ItemOf>
  );
}

const RELEASES: readonly Col<S["ReleaseBody"]>[] = [
  ["Release", (r) => <Id id={r.id} />], CLUSTER, ["Published", (r) => when(r.publishedAt)], ["By", (r) => by(r.publishedBy)],
  ["Sources", (r) => r.sources.length], ["Machines", (r) => r.machines.length],
];

// The imports (#340): GET /ingestions filtered by state finds paused and lapsed imports (G10), and
// by machine, as a machine's page links them.
const INGESTIONS: readonly Col<S["IngestionBody"]>[] = [
  ["Import", (b) => <Id id={b.id} />], ["State", (b) => b.state], ["Machine", (b) => <Id id={b.machine} />], ["Draft", (b) => <Id id={b.draft} />],
  ["Staging", (b) => b.mode], ["Lease until", (b) => (b.state === "held" || b.state === "resumed" ? when(b.leaseUntil) : "none")],
  ["Expires", (b) => when(b.expiresAt)], CREATED,
];

export function Imports({ query, go }: { query: string; go: (to: string) => void }) {
  const { q, set, sent } = useFilters("/ingestions", query, go);
  return (
    <>
      <h2>Imports</h2>
      <p><Action label="Start import" needs={["author"]} onClick={() => go(q.has("machine") ? `/ingestions/new?machine=${q.get("machine")}` : "/ingestions/new")} /></p>
      <Pinned label="Machine" id={q.get("machine")} clear={() => set("machine", "")} />
      <Filter label="State:" value={q.get("state") ?? ""} options={choices(INGESTION_STATES)} onChange={(v) => set("state", v)} />
      <ListOf label="Imports" path="/ingestions" query={sent(["state", "machine"])} cols={INGESTIONS}
        empty={q.size === 0 ? "No imports recorded yet." : "No import matches this filter."} />
    </>
  );
}

// The Changes page: drafts (#339) and the imports into them, then releases.
export function Changes() {
  return (
    <>
      <h2>Changes</h2>
      <h3>Drafts</h3>
      <Drafts />
      <p><Ref to="/ingestions" id="ingestions">Imports</Ref>, of every machine.</p>
      <h3>Releases</h3>
      <ListOf label="Releases" path="/releases" cols={RELEASES} empty="No releases published yet." />
    </>
  );
}

const BUILT: readonly Col<S["ReleaseSourceBody"]>[] = [
  ["Kind", (s) => s.kind], ["Source", (s) => <Id id={s.head} />], ["Revision", (s) => <Id id={s.revision} none="removed" />],
];
const COVERED: readonly Col<S["ReleaseMachineBody"]>[] = [
  ["Machine", (m) => <Id id={m.machine} />], ["Mode", (m) => m.mode], ["Publication observation", (m) => <Id id={m.publicationObservation} />],
];

export function ReleaseItem({ id }: { id: string }) {
  return (
    <ItemOf<S["ReleaseBody"]> path={`/releases/${encodeURIComponent(id)}`} what="the release" title={() => `Release ${short(id)}`}>
      {(r) => (
        <>
          <Fields rows={[
            ["Identifier", r.id], ["Cluster", <Id id={r.cluster} />], ["Published", <>{when(r.publishedAt)} by {by(r.publishedBy)}</>],
            ["Renderer", `contract ${r.renderer.contract} · machinery ${r.renderer.machineryVersion} · Kubernetes ${r.renderer.kubernetesVersion}`],
            ["Draft", <><Id id={r.draft} />, revision {r.draftRevision}</>], ["Publication", <Id id={r.operation} />],
          ]} />
          <h3>Source revisions this release was built from (these are not configuration)</h3>
          <Rows label="Source revisions" cols={BUILT} items={r.sources} keyOf={(s) => `${s.kind}:${s.head}`} />
          <h3>Machines covered ({r.machines.length})</h3>
          <Rows label="Machines covered" cols={COVERED} items={r.machines} keyOf={(m) => m.machine} />
        </>
      )}
    </ItemOf>
  );
}

const PLANS: readonly Col<S["PlanBody"]>[] = [
  ["Plan", (p) => <Id id={p.id} />], ["State", (p) => p.state], ["Machine", (p) => <Id id={p.machine} />],
  ["Operation", (p) => (p.mode === null ? p.operation : `${p.operation}, mode ${p.mode}`)], ["Release", (p) => <Id id={p.release} />],
  CREATED, ["Expires", (p) => when(p.expiresAt)],
];

export function Plans({ query, go }: { query: string; go: (to: string) => void }) {
  const { q, set, sent } = useFilters("/plans", query, go);
  return (
    <>
      <h2>Plans</h2>
      <Pinned label="Machine" id={q.get("machine")} clear={() => set("machine", "")} />
      <Filter label="State:" value={q.get("state") ?? ""} options={choices(PLAN_STATES)} onChange={(v) => set("state", v)} />
      <ListOf label="Plans" path="/plans" query={sent(["state", "machine"])} cols={PLANS} empty={q.size === 0 ? "No plans created yet." : "No plan matches this filter."} />
    </>
  );
}

export function Change({ p }: { p: S["PlanBody"] }) {
  const d = p.evidence.diff;
  if (p.operation === "adopt") return <p>Sends nothing. Records the machine's current configuration as applied.</p>;
  if (d == null) return <p>No change recorded.</p>;
  if (d.withheld) return <p>The change cannot be shown.</p>;
  return <><p>Whole redacted configuration, applied <Id id={d.from} /> → <Id id={d.to} />:</p><pre data-testid="diff">{d.unified}</pre></>;
}

export function PlanItem({ id }: { id: string }) {
  return (
    <ItemOf<S["PlanBody"]> path={`/plans/${encodeURIComponent(id)}`} what="the plan" title={(p) => `Plan ${short(id)}${p === undefined ? "" : `: ${p.state}`}`}>
      {(p) => {
        const { validation: v, dryRun, baseline } = p.evidence;
        return (
          <>
            <Fields rows={[
              ["State", `${p.state}; expires ${when(p.expiresAt)}`],
              ["Target", <>machine <Id id={p.machine} />, cluster <Id id={p.cluster} />, route {p.route}</>],
              ["Operation", p.mode === null ? p.operation : `${p.operation}, mode ${p.mode}`],
              ["Release", <><Id id={p.release} /> (the machine's Desired when planned: <Id id={p.desiredRelease} />)</>],
              ["Bound to", <>assignment revision <Id id={p.assignmentRevision} />, baseline revision {p.baselineRevision ?? "none"}, drift record {p.driftRecord ?? "none"}, epoch {p.epoch}</>],
              ["Limits", `transport ${seconds(p.transportDeadlineSeconds)}, verification ${seconds(p.verificationDeadlineSeconds)}, observation age ${seconds(p.maxObservationAgeSeconds)}, check validity ${seconds(p.checkValiditySeconds)}, attempts ${p.maxAttempts ?? "none"}, rollout limit ${p.rolloutLimit}`],
              ["Approval", <>policy {p.approvalPolicy}; <Id id={p.approval} none="not approved" /></>],
              ["Committed operation", <Id id={p.committedOperation} />],
              ["Created", <>{when(p.createdAt)} by {by(p.createdBy)}</>],
            ]} />
            <h3>Evidence</h3>
            <Fields rows={[
              ["Validation", `${v.result} at ${v.at} (contract ${v.contract}, machinery ${v.machineryVersion}, Kubernetes ${v.kubernetesVersion}, ${v.platformMode})`],
              ["Dry run", dryRun.available ? "available" : `not available (${dryRun.reason})`],
              ...(baseline == null ? [] : [["Baseline", <>import base <Id id={baseline.importBaseRevision} />{baseline.pendingConvergence && ", pending convergence"}</>] as const]),
              ["Change", <Change p={p} />],
            ]} />
            <p><Ref to={`/operations?plan=${p.id}`} id={p.id}>Operations of this plan</Ref></p>
          </>
        );
      }}
    </ItemOf>
  );
}

const Subject = ({ s }: { s: S["OperationSubject"] | null }) =>
  s === null ? "none" : <Ids ids={[s.machine, s.plan, s.draft, s.ingestion].filter((x) => x !== undefined)} />;
const OPERATIONS: readonly Col<S["OperationBody"]>[] = [
  ["Operation", (o) => <Id id={o.id} />], ["Kind", (o) => o.kind], ["State", (o) => o.state], ["Subject", (o) => <Subject s={o.subject} />],
  ["Created by", (o) => by(o.createdBy)], CREATED,
];

export function Operations({ query, go }: { query: string; go: (to: string) => void }) {
  const { q, set, sent } = useFilters("/operations", query, go);
  return (
    <>
      <h2>Operations</h2>
      <Pinned label="Machine" id={q.get("machine")} clear={() => set("machine", "")} />
      <Pinned label="Plan" id={q.get("plan")} clear={() => set("plan", "")} />
      <Filter label="Kind:" value={q.get("kind") ?? ""} options={choices(OPERATION_KINDS)} onChange={(v) => set("kind", v)} />
      <Filter label="State:" value={q.get("state") ?? ""} options={choices(OPERATION_STATES)} onChange={(v) => set("state", v)} />
      <ListOf label="Operations" path="/operations" query={sent(["plan", "machine", "state", "kind"])} cols={OPERATIONS}
        empty={q.size === 0 ? "No operations recorded yet." : "No operation matches this filter."} />
    </>
  );
}

const ATTEMPTS: readonly Col<S["AttemptBody"]>[] = [
  ["Attempt", (a) => a.number], ["At", (a) => when(a.at)], ["Route", (a) => a.route], ["Controller", (a) => `${a.controller}, generation ${a.generation}`],
  ["Deadlines", (a) => `transport ${when(a.transportDeadline)}, verification ${when(a.verificationDeadline)}`],
  ["Response", (a) => (a.response === null ? "none" : `${a.response.class} ${a.response.code} at ${when(a.response.at)}`)],
  ["Accounted", (a) => (a.accounting === null ? "no" : `${a.accounting.basis} at ${when(a.accounting.at)}`)],
];
// A result or error record as the server wrote it, as text.
const Record = ({ v }: { v: unknown }) => (v === null ? "none" : <pre>{JSON.stringify(v, null, 2)}</pre>);

export function OperationItem({ id }: { id: string }) {
  return (
    <ItemOf<S["OperationBody"]> path={`/operations/${encodeURIComponent(id)}`} what="the operation"
      title={(o) => `Operation ${short(id)}${o === undefined ? "" : `: ${o.kind}, ${o.state}`}`}>
      {(o) => (
        <>
          <Fields rows={[
            ["Kind", o.kind], ["State", o.state], ["Subject", <Subject s={o.subject} />], ["Created", <>{when(o.createdAt)} by {by(o.createdBy)}</>],
            ["Epoch", o.epoch], ["Last event", o.lastEvent], ["Result", <Record v={o.result} />], ["Error", <Record v={o.error} />],
          ]} />
          <h3>Attempts</h3>
          {(o.attempts ?? []).length === 0 ? <p>No attempt recorded.</p>
            : <Rows label="Attempts" cols={ATTEMPTS} items={o.attempts ?? []} keyOf={(a) => a.id} />}
        </>
      )}
    </ItemOf>
  );
}

const ACTS: readonly Col<S["ActItem"]>[] = [
  ["At", (a) => when(a.at)], ["Principal", (a) => <><Id id={a.principal} /> ({a.principalKind})</>], ["Via", (a) => a.via],
  ["Role", (a) => a.role ?? "none"], ["Action", (a) => a.action], ["Subjects", (a) => <Ids ids={a.subjects} />],
  ["Request", (a) => a.requestId ?? "none"], ["Act", (a) => <Id id={a.id} />],
];

export function Audit() {
  return <><h2>Audit</h2><ListOf label="Acts" path="/acts" cols={ACTS} empty="No acts recorded yet." /></>;
}

const DEPENDENT: readonly Col<S["DependencyReleaseBody"]>[] = [
  ["Release", (r) => <Id id={r.release} />], CLUSTER, ["Published", (r) => when(r.publishedAt)],
  ["Records", (r) => r.records.map((x, i) => <Group key={`${x.machine}:${x.kind}`}>{i > 0 && ", "}<Id id={x.machine} /> {x.kind}</Group>)],
];

export function DependencyItem({ id }: { id: string }) {
  const path = `/dependencies/${encodeURIComponent(id)}`;
  return (
    <ItemOf<S["DependencyBody"]> path={path} what="the dependency" title={(d) => `Dependency ${d === undefined ? short(id) : `${d.object}, version ${d.version}`}`}>
      {(d) => (
        <>
          <Fields rows={[
            ["Identifier", d.id], ["Provider", d.provider], ["Object", d.object], ["Version", d.version], ["Created", d.created],
            ["Class", klass(d)], ["Reason", d.reason ?? "none"], ["First retained", when(d.firstRetainedAt)], ["Unknown since", when(d.unknownSince)],
            ["Observed from", when(d.observedFrom)], ["Recorded", when(d.recordedAt)],
          ]} />
          <h3>Releases that depend on it</h3>
          <ListOf label="Dependent releases" path={`${path}/releases`} cols={DEPENDENT} keyOf={(r) => r.release} empty="No release depends on it." />
          <h3>Alerts</h3>
          <ListOf label="Alerts" path={`${path}/alerts`} cols={ALERTS} empty="No alerts raised for it." />
        </>
      )}
    </ItemOf>
  );
}

// The view at a path (B3): a navigation page, or a resource's item at /<kind>/<id>. The root is
// the machine list, the hub of the journeys (§6). An identifier is never percent-encoded; one that
// is answers 404 once the view encodes it again.
export function view(path: string, query: string, go: (to: string) => void): ReactNode | undefined {
  const [, kind, id] = /^\/([a-z-]+)(?:\/([^/]+))?$/.exec(path === "/" ? "/machines" : path) ?? [];
  const source = /^(fragment|profile|assignment)s$/.exec(kind ?? "")?.[1] as Kind | undefined;
  const revision = /^(fragment|profile|assignment)-revisions$/.exec(kind ?? "")?.[1] as Kind | undefined;
  // No identifier is "new": every one starts with its type's prefix.
  if (id === "new" && kind === "clusters") return <AddCluster />;
  if (id === "new" && kind === "machines") return <RecordMachine query={query} />;
  if (id === "new" && kind === "drafts") return <CreateDraft />;
  if (id === "new" && kind === "ingestions") return <StartImport query={query} />;
  if (id === undefined) {
    const pages: Record<string, () => ReactNode> = {
      machines: () => <Machines query={query} go={go} />, clusters: () => <Clusters />, changes: () => <Changes />,
      plans: () => <Plans query={query} go={go} />, operations: () => <Operations query={query} go={go} />,
      sources: () => <Sources query={query} go={go} />, audit: () => <Audit />, recovery: () => <RecoveryConsole />,
      ingestions: () => <Imports query={query} go={go} />,
    };
    return kind !== undefined && Object.hasOwn(pages, kind) ? pages[kind]!() : undefined;
  }
  if (source !== undefined) return <SourceItem kind={source} id={id} />;
  if (revision !== undefined) return <RevisionItem kind={revision} id={id} />;
  // A draft's page is keyed by its id: a move to another draft keeps none of the last one's reads,
  // ETag or editor text (ginsys/bronzeward#517 H2).
  const items: Record<string, () => ReactNode> = {
    machines: () => <MachineItem id={id} />, clusters: () => <ClusterItem id={id} />, releases: () => <ReleaseItem id={id} />, drafts: () => <DraftItem key={id} id={id} />,
    plans: () => <PlanItem id={id} />, operations: () => <OperationItem id={id} />, dependencies: () => <DependencyItem id={id} />,
    ingestions: () => <IngestionItem id={id} />,
  };
  return kind !== undefined && Object.hasOwn(items, kind) ? items[kind]!() : undefined;
}
