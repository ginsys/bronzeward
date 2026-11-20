// What shows a release, a plan, an operation or a machine by a title instead of its identifier (client
// contracts A8 and A9): the hooks that read the bodies a title names through the page's own store, and the
// links and headings that show it. The title is the text and the whole identifier the title attribute; a
// read that did not answer, or text that is not safe, leaves the shortened identifier, as every other is.

import { type ReactNode, useEffect, useSyncExternalStore } from "react";
import type { components } from "./api/v1";
import { Id, Ref, short, useReads, viewOf } from "./list.tsx";
import { MachineRef, machineText } from "./machines.tsx";
import { bodiesOf, draftText, operationTitle, planTitle, releaseTitle } from "./titles.ts";

type S = components["schemas"];

// The body at `path`, asked for through the page's store: undefined while pending or with no path, null
// when the read gave none.
export function useBody<T>(path: string | undefined): T | null | undefined {
  const { client, view, again, settle, undropped } = useReads();
  const bodies = bodiesOf(client);
  // The page's reads start again with the view and with each read again, a new epoch included: a title
  // read under another generation is not kept.
  const gen = `${view}|${again}`;
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

// The text for a machine: its name where it has a valid one, else its identifier shortened; undefined
// while its read is pending, so a title built on it waits for it.
export function useMachineText(id: string | null | undefined): string | undefined {
  const m = useBody<S["MachineBody"]>(at("machines", id));
  return id == null || m === undefined ? undefined : machineText(id, m?.name);
}

// A machine as a link: its name as the text where it has one, the shortened identifier until then and
// when it has none, and the whole identifier as the title attribute. An identifier of another shape is
// shown as any other is, with no read.
// A view that reads the machine itself passes it as `held` (its body, once read): nothing is asked.
export type Held = { body: S["MachineBody"] | undefined };
export function MachineById({ id, none = "none", held }: { id: string | null | undefined; none?: string; held?: Held }): ReactNode {
  const asked = id != null && viewOf(id) === "machines";
  const m = useBody<S["MachineBody"]>(asked && held === undefined ? at("machines", id) : undefined);
  if (id == null) return none;
  return asked ? <MachineRef id={id} name={(held === undefined ? m : held.body)?.name} /> : <Id id={id} />;
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
  const machine = machineHeld === undefined ? read : machineHeld.body === undefined || p === undefined ? undefined : machineText(p.machine, machineHeld.body.name);
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

export const ReleaseRef = ({ r }: { r: S["ReleaseBody"] }) => <Ref to={at("releases", r.id)!} id={r.id}>{useReleaseTitle(r.id, r)}</Ref>;
export const PlanRef = ({ p }: { p: S["PlanBody"] }) => <Ref to={at("plans", p.id)!} id={p.id}>{usePlanTitle(p.id, p)}</Ref>;
export const OperationRef = ({ o }: { o: S["OperationBody"] }) => <Ref to={at("operations", o.id)!} id={o.id}>{useOperationTitle(o)}</Ref>;

export const ReleaseHeading = ({ id, r }: { id: string; r: S["ReleaseBody"] }) =>
  <Titled id={id} what="Release" text={useReleaseTitle(id, r)} fallback={`Release ${short(id)}`} />;
export const PlanHeading = ({ id, p, machine }: { id: string; p: S["PlanBody"]; machine?: Held }) =>
  <Titled id={id} what="Plan" text={usePlanTitle(id, p, machine)} tail={` (${p.state})`} fallback={`Plan ${short(id)}: ${p.state}`} />;
export const OperationHeading = ({ id, o }: { id: string; o: S["OperationBody"] }) =>
  <Titled id={id} what="Operation" text={useOperationTitle(o)} tail={` (${o.kind}, ${o.state})`} fallback={`Operation ${short(id)}: ${o.kind}, ${o.state}`} />;
