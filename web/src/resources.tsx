// The read-only lists and items besides the machines (#334): sources and their revisions,
// releases, plans, operations, acts and dependencies with their releases and alerts. Every
// resource has its path (B3) and every identifier links to its view where it has one. An operation's
// timeline is operation.tsx, a machine's drift page is drift.tsx.

import { Fragment as Group, type ReactNode, useCallback, useRef, useState } from "react";
import type { components } from "./api/v1";
import { ApprovalRecord, ApproveControl, RevocationReason, RevokeControl } from "./approval.tsx";
import { BaseDocument } from "./claim.tsx";
import { CreateDraft, DraftItem, Drafts } from "./drafts.tsx";
import { DriftItem, driftId } from "./drift.tsx";
import { RecoveryOperation, recoveryOperationId } from "./recovery-operation.tsx";
import { CancelPlan, FrozenLine, PlanNew, PlanState, PROPOSED, takeFresh } from "./plan.tsx";
import { PublishDraft } from "./publish.tsx";
import { AddCluster, RecordMachine } from "./forms.tsx";
import { OnboardCluster } from "./onboard.tsx";
import { IngestionItem, StartImport } from "./imports.tsx";
import { Detail, EmptyList, Head, Id, IdentifierText, type Listed, More, Ref, Scroll, short, useItem, useList, usePending } from "./list.tsx";
import { Who } from "./principals.tsx";
import { MachineItem } from "./machine.tsx";
import { ClusterItem, Clusters, Filter, Machines } from "./machines.tsx";
import { OperationTimeline } from "./operation.tsx";
import { RecoveryConsole } from "./recovery.tsx";
import { ReviewItem, reviewIds, reviewPath } from "./review.tsx";
import { Action } from "./shell.tsx";
import { Status, when } from "./states.tsx";
import { startedBy } from "./operation-words.ts";
import { safeText, safeWord } from "./safe.ts";
import { alertSentence, assignmentName, dependencyName, importedConfigurationTitle, operationTitle, savedTitle } from "./titles.ts";
import { ClusterLink, type Held, MachineById, Named, OperationHeading, OperationRef, PlanHeading, PlanRef, ReleaseHeading, ReleaseRef, useBody, useMachineText, usePlanTitle } from "./titles.tsx";
import { PageNote } from "./page-note.tsx";
import {
  AUDIT_NOTE, BUILT_FROM, CHANGES_NOTE, DEPENDENCY_NOTE, DIFFERS_IN_FORM_ONLY, DRAFTS_AND_RELEASES, FORM_ONLY_DIFF_NOTE, FRAGMENTS_AND_PROFILES, IMPORTED_NOTE, IMPORTS_NOTE, OPEN_A_MACHINE, OPERATION_NOTE, OPERATIONS_NOTE, PLAN_NOTE,
  NAME_NOT_SHOWN, PLANS_NOTE, PLANS_NOTE_END, RELEASE_NOTE, revisionNote, SOURCES_NOTE, sourceNote,
} from "./words.ts";

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

// `n` is bumped to read the item again.
function ItemOf<T>({ path, what, title, children, n, onSettled }: { path: string; what: string; title: (t: T | undefined) => ReactNode; children: (t: T) => ReactNode; n?: number; onSettled?: () => void }) {
  const item = useItem<T>(path, undefined, n, onSettled);
  const pending = usePending(item, what);
  const t = item.kind === "ok" ? item.data : undefined;
  return <><h2>{title(t)}</h2>{t === undefined ? pending : children(t)}</>;
}

type Row = readonly [string, ReactNode];
const Fields = ({ rows }: { rows: readonly Row[] }) => <dl>{rows.map(([k, v]) => <Group key={k}><dt>{k}</dt><dd>{v}</dd></Group>)}</dl>;
// Each object is shown by its title (A8 rule 6).
const Ids = ({ ids }: { ids: readonly string[] | null }) =>
  ids === null || ids.length === 0 ? "none" : ids.map((id, i) => <Group key={id}>{i > 0 && ", "}<Named id={id} /></Group>);
export const by = (c: { principal: string; role: string } | null) => (c === null ? "none" : <><Who id={c.principal} /> ({safeWord(c.role)})</>);
// Who started an operation (client contracts A10): the principal that made the request, or for an apply or an
// adoption, which the controller creates itself, the controller, the plan it committed and who created that plan.
export function StartedBy({ o }: { o: S["OperationBody"] }) {
  const planId = o.createdBy === null && (o.kind === "apply-config" || o.kind === "adopt") ? o.subject?.plan : undefined;
  const plan = useBody<S["PlanBody"]>(planId === undefined ? undefined : `/plans/${encodeURIComponent(planId)}`);
  const title = usePlanTitle(planId);
  const s = startedBy(o.kind, o.createdBy, planId === undefined ? undefined : { plan: planId, creator: plan?.createdBy });
  if ("person" in s) return by(s.person);
  if ("none" in s) return "none";
  return (
    <>
      {s.controller}
      {s.plan !== undefined && <> <Ref to={`/plans/${encodeURIComponent(s.plan)}`} id={s.plan}>{title}</Ref></>}
      {s.creator !== undefined && <> (created by {by(s.creator)})</>}
    </>
  );
}
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
  return <p ref={own}>{label} <Named id={id} /> <button type="button" onClick={all}>Show all</button></p>;
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
// A row's first column names the thing, "Assignment of cp1", and keeps the identifier as the link's title; the
// shortened identifier stands in until the machine is read.
function AssignmentName({ a }: { a: S["AssignmentBody"] }) {
  const machine = useMachineText(a.machine);
  return <Ref to={`/assignments/${encodeURIComponent(a.id)}`} id={a.id}>{machine === undefined ? `Assignment ${short(a.id)}` : assignmentName(machine)}</Ref>;
}
function ImportName({ b }: { b: S["IngestionBody"] }) {
  const machine = useMachineText(b.machine);
  return <Ref to={`/ingestions/${encodeURIComponent(b.id)}`} id={b.id}>{machine === undefined ? `Import ${short(b.id)}` : operationTitle("ingest", { machine }) ?? "Import"}</Ref>;
}
const ASSIGNMENTS: readonly Col<S["AssignmentBody"]>[] = [["Assignment", (a) => <AssignmentName a={a} />], CLUSTER, ["Machine", (a) => <MachineById id={a.machine} />], CURRENT, CREATED];

// Never a colour alone: the class and its staleness are words.
export const klass = (d: { class: string | null; stale?: boolean }) => `${safeWord(d.class) ?? "none"}${d.stale === true ? " (stale: not observed recently)" : ""}`;
// A dependency's first column names it as its page's heading does (`dependencyName`), and keeps the identifier as
// the link's title.
function DependencyName({ d }: { d: Pick<S["DependencyBody"], "id" | "object" | "version"> }) {
  return <Ref to={`/dependencies/${encodeURIComponent(d.id)}`} id={d.id}>{dependencyName(d)}</Ref>;
}
const DEPENDENCIES: readonly Col<S["DependencyBody"]>[] = [
  ["Dependency", (d) => <DependencyName d={d} />], ["Provider", (d) => safeName(d.provider)], ["Object", (d) => `${safeName(d.object)}, version ${d.version}`],
  ["Class", klass], ["Observed from", (d) => when(d.observedFrom)],
];
// An alert is a sentence: its dependency named once, as the dependency's own page is headed ("This dependency" on that
// page), then what happened (alertSentence). An alert has no page of its own, so its identifier is the last column.
function AlertText({ a, own }: { a: S["AlertBody"]; own: boolean }) {
  const s = alertSentence(a);
  if (!s.subject) return <>{s.text}</>;
  const subject = a.dependency == null || a.object == null || a.version == null ? "A dependency" : own ? "This dependency" : <DependencyName d={{ id: a.dependency, object: a.object, version: a.version }} />;
  return <>{subject} {s.text}</>;
}
// A name reads "(name not shown)" and any other word "(not shown)" (safeWord).
const safeName = (s: string) => safeText(s) ?? NAME_NOT_SHOWN;
// The server's words reach the cells through the safe-text rule: a value with a control or bidirectional character is not shown.
const alertCols = (own: boolean): readonly Col<S["AlertBody"]>[] => [
  ["Alert", (a) => <AlertText a={a} own={own} />], ["Kind", (a) => safeWord(a.kind) ?? "none"], ["Class", klass], ["Reason", (a) => safeWord(a.reason) ?? "none"],
  ["Releases", (a) => <Ids ids={a.releases} />], ["Recorded", (a) => when(a.recordedAt)], ["Identifier", (a) => <IdentifierText id={a.id} />],
];
const ALERTS = alertCols(false);
const OWN_ALERTS = alertCols(true);
const lastPass = (l: Listed<unknown>) => {
  const at = l.extra?.["lastPass"];
  return <p data-testid="last-pass">{typeof at === "string" ? `Last completed monitor pass: ${when(at)}.` : "No monitor pass has completed yet."}</p>;
};

export function Sources({ query, go }: { query: string; go: (to: string) => void }) {
  const { q, set, sent } = useFilters("/sources", query, go);
  const cls = q.get("class") ?? "";
  return (
    <>
      <h2>{FRAGMENTS_AND_PROFILES}</h2>
      <PageNote parts={[SOURCES_NOTE]} />
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
export const REVISIONS: readonly Col<Revision>[] = [["Revision", (r) => <Id id={r.id} />], ["Author", (r) => <Who id={r.author} />], CREATED];
type Kind = "fragment" | "profile" | "assignment";

export function SourceItem({ kind, id }: { kind: Kind; id: string }) {
  const path = `/${kind}s/${encodeURIComponent(id)}`;
  return (
    <ItemOf<Source> path={path} what={`the ${kind}`} title={(s) => `${cap(kind)} ${s !== undefined && "name" in s ? s.name : short(id)}`}>
      {(s) => (
        <>
          <PageNote parts={[sourceNote(kind)]} />
          <Fields rows={[
            ["Identifier", s.id], ["Cluster", <Id id={s.cluster} />],
            ...("scope" in s ? [["Scope", s.scope] as const] : []), ...("layer" in s ? [["Layer", s.layer] as const] : []),
            ...("machine" in s ? [["Machine", <MachineById id={s.machine} />] as const] : []),
            ["Current revision", <Id id={s.revision} none="removed" />], ["Head revision", s.headRevision], ["Created", when(s.createdAt)],
          ]} />
          {kind === "profile" && (
            <>
              <h3>Documents of the current revision</h3>
              {s.revision === null ? <p>No revision yet.</p> : <CurrentDocuments key={s.revision} id={s.revision} />}
            </>
          )}
          <h3>Revisions</h3>
          <ListOf<Revision> label="Revisions" path={`${path}/revisions`} cols={REVISIONS} empty="No revisions recorded." />
        </>
      )}
    </ItemOf>
  );
}

// The configuration a profile pins (ginsys/bronzeward#997): each pinned fragment revision's name, layer and
// document, in the pinned order. A revision lists the identifiers it pins (the server refuses a repeat), so
// a row reads its own fragment revision once; one that fails shows its identifier and the reason, and the
// others still show. The text follows the fragment revision view's display rules (operator experience §9
// item 9): a text node in a `pre`, no copy or download, held by the row only. The first 20 rows read at
// once and a button reads the rest, so a profile of 256 pins never starts 256 reads together.
const PINNED_SHOWN = 20;

// The one display rule item 9 adds (ginsys/bronzeward#988): the text of a fragment revision is the server's
// sanitized text and can still hold a value nobody marked. A page shows this once above the text, a plain
// paragraph in the reading order and not a live region (it is part of the page, not an event), and each `pre`
// points at it with `aria-describedby`, so a reader who jumps straight to one document still hears it.
export const SANITIZED_NOTICE = "This text is sanitized by the server, which cannot promise it holds no value nobody marked.";
const NOTICE_ID = "sanitized-notice";
const SanitizedNotice = () => <p id={NOTICE_ID} data-testid="sanitized-notice">{SANITIZED_NOTICE}</p>;

function PinnedFragment({ id }: { id: string }) {
  const item = useItem<S["FragmentRevisionBody"]>(`/fragment-revisions/${encodeURIComponent(id)}`);
  const pending = usePending(item, "the fragment revision");
  const f = item.kind === "ok" ? item.data : undefined;
  return (
    <div data-testid="pinned-fragment">
      <h4>{f === undefined ? <Id id={id} /> : `${f.name} (${f.layer})`}</h4>
      {f === undefined ? pending : <><p>Revision <Id id={id} /></p><pre data-testid="fragment-document" aria-describedby={NOTICE_ID}>{f.document}</pre></>}
    </div>
  );
}

function PinnedFragments({ ids }: { ids: readonly string[] }) {
  const [all, setAll] = useState(false);
  const shown = all ? ids : ids.slice(0, PINNED_SHOWN);
  return (
    <>
      {ids.length === 0 ? <p>No fragments pinned.</p> : <SanitizedNotice />}
      {shown.map((id) => <PinnedFragment key={id} id={id} />)}
      {shown.length < ids.length && <p><button type="button" onClick={() => setAll(true)}>Show the remaining {ids.length - shown.length}</button></p>}
    </>
  );
}

// The profile page's own documents: those of its current revision, read here so the page that was opened shows them.
function CurrentDocuments({ id }: { id: string }) {
  const item = useItem<S["ProfileRevisionBody"]>(`/profile-revisions/${encodeURIComponent(id)}`);
  const pending = usePending(item, "the profile revision");
  return item.kind === "ok" ? <PinnedFragments ids={item.data.fragments} /> : pending;
}

type AnyRevision = S["FragmentRevisionBody"] | S["ProfileRevisionBody"] | S["AssignmentRevisionBody"];
const REFERENCES: readonly Col<[string, S["Reference"]]>[] = [
  ["Reference", ([name]) => name], ["Kind", ([, r]) => r.kind], ["Version", ([, r]) => r.version], ["Encoding", ([, r]) => r.encoding ?? "none"],
];

// A revision has no number a person could read: "Fragment network-base, saved 21 Nov 2026, 14:02", and for an
// assignment, which has no name, "Assignment of cp1, saved ...". The shortened identifier stands in until it is read.
function RevisionHeading({ kind, id, r }: { kind: Kind; id: string; r: AnyRevision | undefined }) {
  const machine = useMachineText(r !== undefined && "machine" in r ? r.machine : undefined);
  const name = r !== undefined && "name" in r ? safeText(r.name) : undefined;
  const what = r === undefined ? undefined : "name" in r ? name === undefined ? undefined : `${cap(kind)} ${name}` : machine === undefined ? undefined : assignmentName(machine);
  return <>{r === undefined || what === undefined ? `${cap(kind)} revision ${short(id)}` : savedTitle(what, r.createdAt)}</>;
}

export function RevisionItem({ kind, id }: { kind: Kind; id: string }) {
  return (
    <ItemOf<AnyRevision> path={`/${kind}-revisions/${encodeURIComponent(id)}`} what={`the ${kind} revision`}
      title={(r) => <RevisionHeading kind={kind} id={id} r={r} />}>
      {(r) => (
        <>
          <PageNote parts={[revisionNote(kind)]} />
          <Detail id={r.id} />
          <Fields rows={[
            ["Cluster", <ClusterLink id={r.cluster} />], ...("layer" in r ? [["Layer", r.layer] as const] : []),
            ...("machine" in r ? [["Machine", <MachineById id={r.machine} />] as const] : []),
            ...("profiles" in r ? [["Profiles", r.profiles.join(", ") || "none"] as const] : []),
            ...("fragments" in r && !Array.isArray(r.fragments)
              ? [["Fragments", Object.entries(r.fragments).map(([layer, names]) => `${layer}: ${names.join(", ")}`).join("; ") || "none"] as const] : []),
            ["Author", <Who id={r.author} />], ["Created", when(r.createdAt)],
          ]} />
          {"fragments" in r && Array.isArray(r.fragments) && (
            <>
              <h3>Fragments</h3>
              <PinnedFragments key={r.id} ids={r.fragments} />
            </>
          )}
          {"document" in r && (
            <>
              <h3>Document</h3>
              <SanitizedNotice />
              <pre data-testid="document" aria-describedby={NOTICE_ID}>{r.document}</pre>
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

// What an import base revision holds, from the one read (persistence and API §9.2): its size and
// counts, never its document text, digests or reference names (choice §17.38). The operation that
// wrote it is not recorded on the revision, so none is shown.
export const ImportBaseRows = ({ r }: { r: S["ImportBaseSummary"] }) => (
  <>
    <Detail id={r.id} />
    <Fields rows={[
      ["Machine", <MachineById id={r.machine} />], ["Author", <Who id={r.author} />], ["Imported", when(r.createdAt)],
      ["Document size", `${r.documentBytes} bytes`], ["References", r.references], ["Embedded documents", r.embedded],
    ]} />
    <p>The digests and reference names are not shown. The text is read on request. Any author may read an import read from a machine; an import of a file only the author who imported it and reviewed it may read.</p>
  </>
);

// "Imported configuration of cp1, imported 21 Nov 2026, 14:02": an import base has no number, so the day tells two of
// one machine apart. Until the machine is read, the shortened identifier stands in.
function ImportBaseHeading({ id, r }: { id: string; r: S["ImportBaseSummary"] | undefined }) {
  const machine = useMachineText(r?.machine);
  return <>{r === undefined || machine === undefined ? `Imported configuration ${short(id)}` : importedConfigurationTitle(machine, r.createdAt)}</>;
}

export const ImportBaseItem = ({ id }: { id: string }) => (
  <ItemOf<S["ImportBaseSummary"]> path={`/import-base-revisions/${encodeURIComponent(id)}`} what="the imported configuration"
    title={(r) => <ImportBaseHeading id={id} r={r} />}>
    {(r) => <><PageNote parts={[IMPORTED_NOTE]} /><ImportBaseRows r={r} /><BaseDocument id={r.id} machine={r.machine} /></>}
  </ItemOf>
);

const RELEASES: readonly Col<S["ReleaseBody"]>[] = [
  ["Release", (r) => <ReleaseRef r={r} />], CLUSTER, ["Published", (r) => when(r.publishedAt)], ["By", (r) => by(r.publishedBy)],
  [BUILT_FROM, (r) => r.sources.length], ["Machines", (r) => r.machines.length],
];

// The imports (#340): GET /ingestions filtered by state finds paused and lapsed imports (G10), and
// by machine, as a machine's page links them.
const INGESTIONS: readonly Col<S["IngestionBody"]>[] = [
  ["Import", (b) => <ImportName b={b} />], ["State", (b) => safeWord(b.state)], ["Machine", (b) => <MachineById id={b.machine} />], ["Draft", (b) => <Named id={b.draft} />],
  ["Staging", (b) => safeWord(b.mode)], ["Lease until", (b) => (b.state === "held" || b.state === "resumed" ? when(b.leaseUntil) : "none")],
  ["Expires", (b) => when(b.expiresAt)], CREATED,
];

export function Imports({ query, go }: { query: string; go: (to: string) => void }) {
  const { q, set, sent } = useFilters("/ingestions", query, go);
  return (
    <>
      <h2>Imports</h2>
      <PageNote parts={[IMPORTS_NOTE]} />
      <p><Action label="Start import" needs={["author"]} onClick={() => go(q.has("machine") ? `/ingestions/new?machine=${q.get("machine")}` : "/ingestions/new")} /></p>
      <Pinned label="Machine" id={q.get("machine")} clear={() => set("machine", "")} />
      <Filter label="State:" value={q.get("state") ?? ""} options={choices(INGESTION_STATES)} onChange={(v) => set("state", v)} />
      <ListOf label="Imports" path="/ingestions" query={sent(["state", "machine"])} cols={INGESTIONS}
        empty={q.size === 0 ? "No imports recorded yet." : "No import matches this filter."} />
    </>
  );
}

// The Drafts and releases page: drafts (#339) and the imports into them, then releases.
export function Changes() {
  return (
    <>
      <h2>{DRAFTS_AND_RELEASES}</h2>
      <PageNote parts={[CHANGES_NOTE]} />
      <h3>Drafts</h3>
      <Drafts />
      <p><Ref to="/ingestions" id="ingestions">Imports</Ref>, of every machine.</p>
      <h3>Releases</h3>
      <ListOf label="Releases" path="/releases" cols={RELEASES} empty="No releases published yet." />
    </>
  );
}

const BUILT: readonly Col<S["ReleaseSourceBody"]>[] = [
  ["Kind", (s) => safeWord(s.kind)], ["Source", (s) => <Id id={s.head} />], ["Revision", (s) => <Id id={s.revision} none="removed" />],
];
const RenderedLink = ({ to, machine }: { to: string; machine: string }) => (
  <Ref to={to} id={machine}>Rendered configuration of {useMachineText(machine) ?? short(machine)}</Ref>
);
const covered = (release: string): readonly Col<S["ReleaseMachineBody"]>[] => [
  ["Machine", (m) => <MachineById id={m.machine} />], ["Mode", (m) => safeWord(m.mode)], ["Publication observation", (m) => <Id id={m.publicationObservation} />],
  ["Rendered configuration", (m) => {
    const to = reviewPath(release, m.machine);
    return to === undefined ? "none" : <RenderedLink to={to} machine={m.machine} />;
  }],
];

export function ReleaseItem({ id }: { id: string }) {
  return (
    <ItemOf<S["ReleaseBody"]> path={`/releases/${encodeURIComponent(id)}`} what="the release" title={(r) => (r === undefined ? `Release ${short(id)}` : <ReleaseHeading id={id} r={r} />)}>
      {(r) => (
        <>
          <PageNote parts={[RELEASE_NOTE]} />
          <Fields rows={[
            ["Identifier", r.id], ["Cluster", <Id id={r.cluster} />], ["Published", <>{when(r.publishedAt)} by {by(r.publishedBy)}</>],
            ["Renderer", `contract ${r.renderer.contract} · machinery ${r.renderer.machineryVersion} · Kubernetes ${r.renderer.kubernetesVersion}`],
            ["Draft", <><Named id={r.draft} />, revision {r.draftRevision}</>], ["Publication", <Named id={r.operation} />],
          ]} />
          <h3>Source revisions this release was built from (these are not configuration)</h3>
          <Rows label="Source revisions" cols={BUILT} items={r.sources} keyOf={(s) => `${s.kind}:${s.head}`} />
          <h3>Machines covered ({r.machines.length})</h3>
          <Rows label="Machines covered" cols={covered(r.id)} items={r.machines} keyOf={(m) => m.machine} />
        </>
      )}
    </ItemOf>
  );
}

const PLANS: readonly Col<S["PlanBody"]>[] = [
  ["Plan", (p) => <PlanRef p={p} />], ["State", (p) => safeWord(p.state)], ["Machine", (p) => <MachineById id={p.machine} />],
  ["Operation", (p) => (p.mode === null ? safeWord(p.operation) : `${safeWord(p.operation)}, mode ${safeWord(p.mode)}`)], ["Release", (p) => <Named id={p.release} />],
  CREATED, ["Expires", (p) => when(p.expiresAt)],
];

export function Plans({ query, go }: { query: string; go: (to: string) => void }) {
  const { q, set, sent } = useFilters("/plans", query, go);
  return (
    <>
      <h2>Plans</h2>
      <PageNote parts={[PLANS_NOTE]}> <Ref to="/machines" id="machines">{OPEN_A_MACHINE}</Ref> {PLANS_NOTE_END}</PageNote>
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
  const note = d.fromFormOnly ? <p>{FORM_ONLY_DIFF_NOTE}</p> : null;
  if (d.unified === "") return <><p>No differences in the redacted configurations (applied <Named id={d.from} />, release <Named id={d.to} />).</p>{note}</>;
  return <><p>Change shown below: the whole redacted configuration, applied <Named id={d.from} /> → <Named id={d.to} />.</p>{note}<pre className="diff" data-testid="diff" tabIndex={0} aria-label="Change">{d.unified}</pre></>;
}

export function PlanItem({ id }: { id: string }) {
  // The line that answers the creation this browser just sent, shown with the plan's first view only.
  const [proposed] = useState(() => takeFresh(id));
  const [n, setN] = useState(0);
  const reread = useCallback(() => setN((x) => x + 1), []);
  const [answers, setAnswers] = useState(0);
  const settled = useCallback(() => setAnswers((x) => x + 1), []);
  // An approved plan reads its machine itself (and again at each poll): its heading and target name the
  // machine from that body and ask the titles' store for nothing (A8 rule 2).
  // `null` is a read that answered with no body: the title then says "the machine" and survives (A8 rule 1).
  const [machine, setMachine] = useState<S["MachineBody"] | null>();
  // Stable: the approved plan's reads restart whenever this changes, and every body it reports is a new object.
  const onMachine = useCallback((m: S["MachineBody"] | null) => setMachine((was) => m ?? was ?? null), []);
  const held = (p: S["PlanBody"]): Held | undefined => (p.state === "approved" ? { body: machine === null ? null : machine?.id === p.machine ? machine : undefined } : undefined);
  return (
    <ItemOf<S["PlanBody"]> path={`/plans/${encodeURIComponent(id)}`} what="the plan" n={n} onSettled={settled} title={(p) => (p === undefined ? `Plan ${short(id)}` : <PlanHeading id={id} p={p} {...(held(p) !== undefined && { machine: held(p)! })} />)}>
      {(p) => {
        const { validation: v, dryRun, baseline } = p.evidence;
        return (
          <>
            <PageNote parts={[PLAN_NOTE]} />
            {proposed && p.state === "proposed" && <Status testid="plan-proposed">{PROPOSED}</Status>}
            <PlanState p={p} reread={reread} answers={answers} onMachine={onMachine} />
            <FrozenLine p={p} machine={held(p)?.body} />
            <Fields rows={[
              ["State", `${safeWord(p.state)}; expires ${when(p.expiresAt)}`],
              ["Target", <>machine <MachineById id={p.machine} {...(held(p) !== undefined && { held: held(p)! })} />, cluster <Id id={p.cluster} />, route {safeWord(p.route)}</>],
              ["Operation", p.mode === null ? safeWord(p.operation) : `${safeWord(p.operation)}, mode ${safeWord(p.mode)}`],
              ["Release", <><Named id={p.release} /> (the machine's Desired when planned: <Named id={p.desiredRelease} />)</>],
              ["Bound to", <>assignment revision <Id id={p.assignmentRevision} />, baseline revision {p.baselineRevision ?? "none"}, drift record {p.driftRecord ?? "none"}, epoch {p.epoch}</>],
              ["Limits", `transport ${seconds(p.transportDeadlineSeconds)}, verification ${seconds(p.verificationDeadlineSeconds)}, observation age ${seconds(p.maxObservationAgeSeconds)}, check validity ${seconds(p.checkValiditySeconds)}, attempts ${p.maxAttempts ?? "none"}, rollout limit ${p.rolloutLimit}`],
              ["Approval", <>policy {p.approvalPolicy}; <Id id={p.approval} none="not approved" /></>],
              ["Committed operation", <Named id={p.committedOperation} />],
              ["Created", <>{when(p.createdAt)} by {by(p.createdBy)}</>],
            ]} />
            <h3>Evidence</h3>
            <Fields rows={[
              ["Validation", `${safeWord(v.result)} at ${v.at} (contract ${v.contract}, machinery ${v.machineryVersion}, Kubernetes ${v.kubernetesVersion}, ${safeWord(v.platformMode)})`],
              ["Dry run", dryRun.available ? "available" : `not available (${safeWord(dryRun.reason)})`],
              ...(baseline == null ? [] : [["Baseline", <>import base <Id id={baseline.importBaseRevision} />{baseline.pendingConvergence && ", release not applied yet"}{baseline.formOnly && `, ${DIFFERS_IN_FORM_ONLY.toLowerCase()}`}</>] as const]),
              ["Change", <Change p={p} />],
            ]} />
            <p><Ref to={`/operations?plan=${p.id}`} id={p.id}>Operations of this plan</Ref></p>
            <ApproveControl p={p} reread={reread} />
            <ApprovalRecord p={p} />
            {p.state === "revoked" && <RevocationReason p={p} />}
            <RevokeControl p={p} reread={reread} />
            {(p.state === "proposed" || p.state === "approved" || p.state === "committed") && <CancelPlan id={p.id} reread={reread} />}
          </>
        );
      }}
    </ItemOf>
  );
}

// A subject's machine, plan, draft and import are each shown by its name or title (A8 rule 6, A9).
const Subject = ({ s }: { s: S["OperationSubject"] | null }) =>
  <Ids ids={s === null ? null : [s.machine, s.plan, s.draft, s.ingestion].filter((x) => x !== undefined)} />;
const OPERATIONS: readonly Col<S["OperationBody"]>[] = [
  ["Operation", (o) => <OperationRef o={o} />], ["Kind", (o) => safeWord(o.kind)], ["State", (o) => safeWord(o.state)], ["Subject", (o) => <Subject s={o.subject} />],
  ["Started by", (o) => <StartedBy o={o} />], CREATED,
];

export function Operations({ query, go }: { query: string; go: (to: string) => void }) {
  const { q, set, sent } = useFilters("/operations", query, go);
  return (
    <>
      <h2>Operations</h2>
      <PageNote parts={[OPERATIONS_NOTE]} />
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
  ["Attempt", (a) => a.number], ["At", (a) => when(a.at)], ["Route", (a) => safeWord(a.route)], ["Controller", (a) => `${a.controller}, generation ${a.generation}`],
  ["Deadlines", (a) => `transport ${when(a.transportDeadline)}, verification ${when(a.verificationDeadline)}`],
  ["Response", (a) => (a.response === null ? "none" : `${safeWord(a.response.class)} ${a.response.code} at ${when(a.response.at)}`)],
  ["Accounted", (a) => (a.accounting === null ? "no" : `${a.accounting.basis} at ${when(a.accounting.at)}`)],
];
// A result or error record as the server wrote it, as text.
const Record = ({ v }: { v: unknown }) => (v === null ? "none" : <pre>{JSON.stringify(v, null, 2)}</pre>);

export function OperationItem({ id }: { id: string }) {
  // The timeline bumps `n` soon after events arrive, so the state and outcome follow the operation.
  const [n, setN] = useState(0);
  const reread = useCallback(() => setN((x) => x + 1), []);
  return (
    <ItemOf<S["OperationBody"]> path={`/operations/${encodeURIComponent(id)}`} what="the operation" n={n}
      title={(o) => (o === undefined ? `Operation ${short(id)}` : <OperationHeading id={id} o={o} />)}>
      {(o) => (
        <>
          <PageNote parts={[OPERATION_NOTE]} />
          <OperationTimeline id={id} o={o} reread={reread} />
          <Fields rows={[
            ["Kind", safeWord(o.kind)], ["State", safeWord(o.state)], ["Subject", <Subject s={o.subject} />], ["Created", <>{when(o.createdAt)} by <StartedBy o={o} /></>],
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

export const ACTS: readonly Col<S["ActItem"]>[] = [
  ["At", (a) => when(a.at)], ["Principal", (a) => <><Who id={a.principal} /> ({safeWord(a.principalKind)})</>], ["Via", (a) => safeWord(a.via) ?? "none"],
  ["Role", (a) => safeWord(a.role) ?? "none"], ["Action", (a) => safeWord(a.action)], ["Subjects", (a) => <Ids ids={a.subjects} />],
  ["Request", (a) => a.requestId ?? "none"], ["Act", (a) => <Id id={a.id} />],
];

export function Audit() {
  return <><h2>Audit</h2><PageNote parts={[AUDIT_NOTE]} /><ListOf label="Acts" path="/acts" cols={ACTS} empty="No acts recorded yet." /></>;
}

const DEPENDENT: readonly Col<S["DependencyReleaseBody"]>[] = [
  ["Release", (r) => <Named id={r.release} />], CLUSTER, ["Published", (r) => when(r.publishedAt)],
  ["Records", (r) => r.records.map((x, i) => <Group key={`${x.machine}:${x.kind}`}>{i > 0 && ", "}<MachineById id={x.machine} /> {safeWord(x.kind)}</Group>)],
];

export function DependencyItem({ id }: { id: string }) {
  const path = `/dependencies/${encodeURIComponent(id)}`;
  return (
    <ItemOf<S["DependencyBody"]> path={path} what="the dependency" title={(d) => (d === undefined ? `Dependency ${short(id)}` : dependencyName(d))}>
      {(d) => (
        <>
          <PageNote parts={[DEPENDENCY_NOTE]} />
          <Fields rows={[
            ["Identifier", d.id], ["Provider", safeName(d.provider)], ["Object", safeName(d.object)], ["Version", d.version], ["Created", <span title={safeWord(d.created)}>{when(d.created)}</span>],
            ["Class", klass(d)], ["Reason", safeWord(d.reason) ?? "none"], ["First retained", when(d.firstRetainedAt)], ["Unknown since", when(d.unknownSince)],
            ["Observed from", when(d.observedFrom)], ["Recorded", when(d.recordedAt)],
          ]} />
          <h3>Releases that depend on it</h3>
          <ListOf label="Dependent releases" path={`${path}/releases`} cols={DEPENDENT} keyOf={(r) => r.release} empty="No release depends on it." />
          <h3>Alerts</h3>
          <ListOf label="Alerts" path={`${path}/alerts`} cols={OWN_ALERTS} empty="No alerts raised for it." />
        </>
      )}
    </ItemOf>
  );
}

// The view at a path (B3): a navigation page, or a resource's item at /<kind>/<id>. The root is
// the machine list, the hub of the journeys (§6). An identifier is never percent-encoded; one that
// is answers 404 once the view encodes it again.
export function view(path: string, query: string, go: (to: string) => void): ReactNode | undefined {
  const review = reviewIds(path);
  if (review !== undefined) return <ReviewItem key={path} release={review[0]} machine={review[1]} />;
  const drift = driftId(path);
  if (drift !== undefined) return <DriftItem key={path} id={drift} />;
  const recovering = recoveryOperationId(path);
  if (recovering !== undefined) return <RecoveryOperation key={path} id={recovering} />;
  const [, kind, id] = /^\/([a-z-]+)(?:\/([^/]+))?$/.exec(path === "/" ? "/machines" : path) ?? [];
  const source = /^(fragment|profile|assignment)s$/.exec(kind ?? "")?.[1] as Kind | undefined;
  const revision = /^(fragment|profile|assignment)-revisions$/.exec(kind ?? "")?.[1] as Kind | undefined;
  // No identifier is "new": every one starts with its type's prefix.
  if (id === "new" && kind === "clusters") return <AddCluster />;
  // Not an identifier either: every cluster's starts with cl_.
  if (id === "onboard" && kind === "clusters") return <OnboardCluster />;
  if (id === "new" && kind === "machines") return <RecordMachine query={query} />;
  if (id === "new" && kind === "drafts") return <CreateDraft />;
  if (id === "new" && kind === "ingestions") return <StartImport query={query} />;
  if (id === "new" && kind === "publications") return <PublishDraft query={query} />;
  if (id === "new" && kind === "plans") return <PlanNew query={query} />;
  if (id === undefined) {
    // /fragments, /profiles and /assignments open the page that lists all three (ginsys/bronzeward#1087); a filter there moves to /sources.
    const sources = () => <Sources query={query} go={go} />;
    const pages: Record<string, () => ReactNode> = {
      machines: () => <Machines query={query} go={go} />, clusters: () => <Clusters />,
      // /drafts and /releases open the page that lists both.
      changes: () => <Changes />, drafts: () => <Changes />, releases: () => <Changes />,
      plans: () => <Plans query={query} go={go} />, operations: () => <Operations query={query} go={go} />,
      sources, fragments: sources, profiles: sources, assignments: sources, audit: () => <Audit />, recovery: () => <RecoveryConsole />,
      ingestions: () => <Imports query={query} go={go} />,
    };
    return kind !== undefined && Object.hasOwn(pages, kind) ? pages[kind]!() : undefined;
  }
  // Keyed by the path, so a move to another source or revision keeps none of the last one's rows: a
  // profile's pinned fragment reads, and the rest the page was asked to read (ginsys/bronzeward#997).
  if (source !== undefined) return <SourceItem key={path} kind={source} id={id} />;
  if (revision !== undefined) return <RevisionItem key={path} kind={revision} id={id} />;
  // A draft's page is keyed by its id: a move to another draft keeps none of the last one's reads,
  // ETag or editor text (ginsys/bronzeward#517 H2).
  const items: Record<string, () => ReactNode> = {
    machines: () => <MachineItem id={id} />, clusters: () => <ClusterItem id={id} />, releases: () => <ReleaseItem id={id} />, drafts: () => <DraftItem key={id} id={id} />,
    plans: () => <PlanItem key={id} id={id} />, operations: () => <OperationItem id={id} />, dependencies: () => <DependencyItem id={id} />,
    ingestions: () => <IngestionItem id={id} />, "import-base-revisions": () => <ImportBaseItem id={id} />,
  };
  return kind !== undefined && Object.hasOwn(items, kind) ? items[kind]!() : undefined;
}
