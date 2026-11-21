// What shows a release, a plan, an operation, a draft, an import or a machine by a title instead of its
// identifier (client contracts A8 and A9): the hooks that read the bodies a title names through the page's
// own store, and the links and headings that show it. The title is the text and the whole identifier the title attribute; a
// read that did not answer, or text that is not safe, leaves the shortened identifier, as every other is (a machine's
// failed read in a title reads "the machine" instead, so the title survives: A8 rule 1).

import { type ReactNode, useEffect, useSyncExternalStore } from "react";
import type { components } from "./api/v1";
import { Id, Ref, short, useReads, viewOf } from "./list.tsx";
import { MachineRef, machineText } from "./machines.tsx";
import { bodiesOf, genOf } from "./bodies.ts";
import { draftText, importedConfiguration, operationTitle, planTitle, releaseTitle } from "./titles.ts";
import { SAFE } from "./principals.ts";
import { THE_MACHINE, THIS_CLUSTER } from "./words.ts";

type S = components["schemas"];

// The body at `path`, asked for through the page's store: undefined while pending or with no path, null
// when the read gave none.
export function useBody<T>(path: string | undefined): T | null | undefined {
  const { client, view, again, settle, undropped } = useReads();
  const bodies = bodiesOf(client);
  const gen = genOf(view, again);
  useEffect(() => {
    // A refusal the read meets that the shell must act on ends the page as any read's does. With no path
    // nothing is asked, and the generation still advances, so a view that asks nothing forgets as any other.
    const live = undropped();
    bodies.want(gen, path === undefined ? [] : [path], (res) => void settle(res, () => {}, live, () => false));
  }, [bodies, gen, path, settle, undropped]);
  const snapshot = () => (path === undefined ? undefined : bodies.get(gen, path));
  return useSyncExternalStore(bodies.subscribe, snapshot, snapshot) as T | null | undefined;
}

const at = (kind: string, id: string | null | undefined) => (id == null ? undefined : `/${kind}/${encodeURIComponent(id)}`);

// The text for a machine: its name where it has a valid one, else its address, else "an unnamed machine";
// undefined while its read is pending, so a title built on it waits for it, and "the machine" where the read
// gave no body, so a title survives a failed read.
export function useMachineText(id: string | null | undefined): string | undefined {
  const m = useBody<S["MachineBody"]>(at("machines", id));
  return id == null || m === undefined ? undefined : m === null ? THE_MACHINE : machineText(m);
}

// The name of a cluster, as its read gives it; undefined while the read is pending, or when the name is not safe to show.
export function useClusterText(id: string | null | undefined): string | undefined {
  const c = useBody<S["ClusterBody"]>(at("clusters", id));
  return typeof c?.name === "string" && SAFE.test(c.name) ? c.name : undefined;
}

// A cluster as a link: its name where the read gives a safe one, preceded by the word "Cluster", else "This cluster" alone.
export function ClusterRef({ id }: { id: string }) {
  const text = useClusterText(id);
  return <>{text !== undefined && "Cluster "}<ClusterLink id={id} /></>;
}
// The same link without the word "Cluster", for a row or sentence that already has one.
export function ClusterLink({ id }: { id: string }) {
  const text = useClusterText(id);
  return <Ref to={`/clusters/${encodeURIComponent(id)}`} id={id}>{text ?? THIS_CLUSTER}</Ref>;
}

// A machine's imported configuration as a link: "Imported configuration of cp1" once the machine is read, "Imported
// configuration" until then. The identifier of the import base is the link's title attribute.
export function ImportedConfigurationRef({ machine, revision }: { machine: string; revision: string }) {
  const text = useMachineText(machine);
  return <Ref to={`/import-base-revisions/${encodeURIComponent(revision)}`} id={revision}>{importedConfiguration(text)}</Ref>;
}

// A machine as a link once its body is read: its name, else its address, else "an unnamed machine", and the
// whole identifier as the title attribute. Until the body arrives, or where the read gave none, and for an
// identifier of another shape (no read), it is shown as any other identifier is.
// A view that reads the machine itself passes it as `held` (its body, once read): nothing is asked.
// The view's own read of the machine: its body, null once it answered with no body (an HTTP failure), and undefined
// until it answers (a transport failure the shell holds keeps the view as it is).
export type Held = { body: S["MachineBody"] | null | undefined };
export function MachineById({ id, none = "none", held }: { id: string | null | undefined; none?: string; held?: Held }): ReactNode {
  const asked = id != null && viewOf(id) === "machines";
  const m = useBody<S["MachineBody"]>(asked && held === undefined ? at("machines", id) : undefined);
  if (id == null) return none;
  const body = held === undefined ? m : held.body;
  if (!asked) return <Id id={id} />;
  // A machine not read yet shows as any identifier does, until its body arrives.
  return body == null ? <Id id={id} /> : <MachineRef machine={body} />;
}

// A title wants the body it is built on: held, as a list row holds it, or read by its identifier.
function useOwn<T>(kind: string, id: string | undefined, held: T | undefined): T | undefined {
  const read = useBody<T>(held === undefined ? at(kind, id) : undefined);
  return held ?? read ?? undefined;
}

export function useReleaseTitle(id: string | undefined, held?: S["ReleaseBody"]): string | undefined {
  const r = useOwn("releases", id, held);
  const d = useBody<S["DraftBody"]>(at("drafts", r?.draft));
  return r === undefined ? undefined : releaseTitle(r, d);
}

export function usePlanTitle(id: string | undefined, held?: S["PlanBody"], machineHeld?: Held): string | undefined {
  const p = useOwn("plans", id, held);
  const release = useReleaseTitle(p?.release);
  const read = useMachineText(machineHeld === undefined ? p?.machine : undefined);
  const machine = machineHeld === undefined ? read : machineHeld.body === undefined || p === undefined ? undefined : machineHeld.body === null ? THE_MACHINE : machineText(machineHeld.body);
  return p === undefined || machine === undefined ? undefined : planTitle(p, machine, release);
}

export function useOperationTitle(o: S["OperationBody"] | undefined): string | undefined {
  const s = o?.subject ?? undefined;
  const draft = useBody<S["DraftBody"]>(o?.kind === "publish" ? at("drafts", s?.draft) : undefined);
  const ingestion = useBody<S["IngestionBody"]>(o?.kind === "ingest" ? at("ingestions", s?.ingestion) : undefined);
  const machine = useMachineText(ingestion?.machine);
  const plan = usePlanTitle(o?.kind === "apply-config" || o?.kind === "adopt" ? s?.plan : undefined);
  if (o === undefined) return undefined;
  if (o.kind !== "ingest") return operationTitle(o.kind, { draft: draftText(draft), plan });
  // An import's machine is on its ingestion, which the subject names: no ingestion named, no answer
  // from it or no machine on it is an import with no machine to name.
  if (s?.ingestion == null) return operationTitle("ingest", {});
  if (ingestion === undefined || (ingestion?.machine != null && machine === undefined)) return undefined;
  return operationTitle("ingest", { machine });
}

const Titled = ({ id, text, what, fallback, tail = "" }: { id: string; text: string | undefined; what: string; fallback: string; tail?: string }) =>
  text === undefined ? fallback : <span title={id}>{`${what}: ${text}${tail}`}</span>;

// A link to an object the view names by its identifier: the title as the text where there is one, else
// the shortened identifier, and the whole identifier as the title attribute.
const NamedLink = ({ id, text }: { id: string; text: string | undefined }) => <Ref to={`/${viewOf(id)}/${encodeURIComponent(id)}`} id={id}>{text}</Ref>;

const NamedDraft = ({ id }: { id: string }) => <NamedLink id={id} text={draftText(useBody<S["DraftBody"]>(at("drafts", id)))} />;
const NamedRelease = ({ id }: { id: string }) => <NamedLink id={id} text={useReleaseTitle(id)} />;
const NamedPlan = ({ id }: { id: string }) => <NamedLink id={id} text={usePlanTitle(id)} />;
// An operation named by its identifier has no body in hand: it is read, then its subject's.
const NamedOperation = ({ id }: { id: string }) => <NamedLink id={id} text={useOperationTitle(useBody<S["OperationBody"]>(at("operations", id)) ?? undefined)} />;
// An import is titled as its operation is: "Import of <machine>", or "Import" where it names no machine.
function NamedImport({ id }: { id: string }) {
  const i = useBody<S["IngestionBody"]>(at("ingestions", id));
  const machine = useMachineText(i?.machine);
  return <NamedLink id={id} text={i == null || (i.machine != null && machine === undefined) ? undefined : operationTitle("ingest", { machine })} />;
}

// An object a view names by its identifier, by the view its prefix names: a machine, release, plan,
// operation, draft or import shows its title, any other identifier as `Id` shows it. Each body is read
// once through the page's store.
export function Named({ id, none = "none" }: { id: string | null | undefined; none?: string }): ReactNode {
  if (id == null) return none;
  switch (viewOf(id)) {
    case "machines": return <MachineById id={id} />;
    case "releases": return <NamedRelease id={id} />;
    case "plans": return <NamedPlan id={id} />;
    case "operations": return <NamedOperation id={id} />;
    case "drafts": return <NamedDraft id={id} />;
    case "ingestions": return <NamedImport id={id} />;
    default: return <Id id={id} />;
  }
}

export const ReleaseRef = ({ r }: { r: S["ReleaseBody"] }) => <Ref to={at("releases", r.id)!} id={r.id}>{useReleaseTitle(r.id, r)}</Ref>;
export const PlanRef = ({ p }: { p: S["PlanBody"] }) => <Ref to={at("plans", p.id)!} id={p.id}>{usePlanTitle(p.id, p)}</Ref>;
export const OperationRef = ({ o }: { o: S["OperationBody"] }) => <Ref to={at("operations", o.id)!} id={o.id}>{useOperationTitle(o)}</Ref>;

export const ReleaseHeading = ({ id, r }: { id: string; r: S["ReleaseBody"] }) =>
  <Titled id={id} what="Release" text={useReleaseTitle(id, r)} fallback={`Release ${short(id)}`} />;
export const PlanHeading = ({ id, p, machine }: { id: string; p: S["PlanBody"]; machine?: Held }) =>
  <Titled id={id} what="Plan" text={usePlanTitle(id, p, machine)} tail={` (${p.state})`} fallback={`Plan ${short(id)}: ${p.state}`} />;
export const OperationHeading = ({ id, o }: { id: string; o: S["OperationBody"] }) =>
  <Titled id={id} what="Operation" text={useOperationTitle(o)} tail={` (${o.kind}, ${o.state})`} fallback={`Operation ${short(id)}: ${o.kind}, ${o.state}`} />;
