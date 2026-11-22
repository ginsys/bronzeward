// Onboard a cluster from a talosconfig (ginsys/bronzeward#1001), as `bronzewardctl cluster onboard` does
// (operator experience §5.2): discover the cluster's members, record the cluster and each machine, supply
// the first key, open one draft and import each machine into it. The server is the record: each step finds
// what exists (a 409 that names a cluster or a machine, an open draft with the title, an import of the
// machine in the draft), so running it again finishes whatever a failure left, and a page reload resumes
// from the same file. The steps are a plain function of `Env`, so the page's own write layer, reads and
// clock reach them and the tests give them a server in memory.

import { parse, stringify } from "yaml";
import type { components } from "./api/v1";
import { existing } from "./forms.tsx";
import { startBody } from "./imports.tsx";
import { safeWord } from "./titles.ts";
import { type Listed, readPages } from "./list.tsx";
import { MACHINE_NAME } from "./machine-name.ts";
import type { Problem, ReadResult } from "./request.ts";
import { CHECK_STATE, problemSentences } from "./states.tsx";
import { EPOCH_CHANGED_ASIDE, type Outcome, Write, type Writes, type WriteSpec } from "./write.ts";

export type Discovery = components["schemas"]["DiscoveryBody"];
type Draft = components["schemas"]["DraftBody"];
type Ingestion = components["schemas"]["IngestionBody"];

export interface Env {
  read: <T>(path: string) => Promise<ReadResult<T>>;
  send: (spec: WriteSpec) => Promise<Outcome>;
  wait: (ms: number) => Promise<void>;
}

// What a step ended in: `existing` for a record the server already held, `skipped` for a member that
// cannot be recorded, `stopped` for an import that waits on a person.
export type LineState = "done" | "existing" | "skipped" | "stopped" | "failed" | "unknown";
// The line of a press whose client holds no epoch to send under: nothing was sent, and the page's [Run again] is the way on.
export const NO_EPOCH_YET = "not sent: this page has not read the installation's epoch yet. Run it again; if this repeats, open the page again.";
// The problem a failure carries, which the page shows as the reference for a bug report (the words point to it).
export interface Reference { problem: Problem; status: number }
// `holder` is the identifier of the machine that holds the name a machine was refused: the page links to it.
export interface Line { key: string; label: string; state: LineState; detail?: string; to?: string; reference?: Reference; holder?: string }
// The reference of an outcome that carried a problem document, as a member to spread; nothing otherwise.
export const referenceOf = (o: Outcome): { reference?: Reference } =>
  "problem" in o && o.problem !== null ? { reference: { problem: o.problem, status: o.kind === "refused" ? o.status : o.problem.status } } : {};

// One onboarding in this page's memory. `document` is the cut talosconfig: held until the first key's
// write is released, which is the one place it is needed again after the discovery.
export interface Plan {
  found: Discovery;
  endpoint: string;
  name: string;
  kube: string;
  document: string | undefined;
  cluster?: string;
  keyed: boolean;
  machines: Record<string, string>;
  draft?: string;
  imports: Record<string, string>;
  // The machines whose assignment the step found or wrote, by node id: the draft that holds it.
  assigned: Record<string, string>;
  lines: Line[];
  // The epoch the plan's first press began under. Every write of every press of this plan is sent under it: a
  // press that resumes from the ids the plan holds (read under that epoch) is refused once the epoch has moved.
  epoch?: string | undefined;
  // The steps after the one that stopped the last press, by name.
  unsent?: string[] | undefined;
}

const MAX_FILE = 512 * 1024;
const NOT_TALOSCONFIG = "the file is not a talosconfig";
const NO_CREDENTIALS = "the talosconfig's context has no certificate authority, certificate and key";
const enc = encodeURIComponent;

// The name a machine takes from its node's hostname (operator experience §5.2): the hostname lowercased, when it
// is a valid name (persistence and API §3.5); else no name, and the reason, which the machine's line says.
export function nameOf(hostname: string): { name: string; note?: string } | { name?: undefined; note: string } {
  if (hostname === "") return { note: "no name: the node reported no hostname" };
  // A to Z only, as the command does: Unicode lowercasing differs between languages (U+0130 is two code points here).
  const name = hostname.replace(/[A-Z]/g, (c) => String.fromCharCode(c.charCodeAt(0) + 32));
  if (!MACHINE_NAME.test(name)) {
    const shown = hostname.length > 64 ? `${hostname.slice(0, 64)}…` : hostname;
    return { note: `no name: the hostname ${safeWord(shown) === shown ? JSON.stringify(shown) : safeWord(shown)} is not a valid name` };
  }
  return name === hostname ? { name } : { name, note: "named by its hostname in lower case" };
}

// The talosconfig cut to one context: the one `name` gives, else the current one, with its certificate
// authority, certificate and key and nothing else, as discover.OneContext writes it (the Go golden and
// this one's test read the same two files). The reasons name no text of the file.
export function cutContext(text: string, name: string | undefined): { document: string; endpoint: string; count: number } {
  if (text.length > MAX_FILE) throw new Error("the file is larger than 512 KiB, the most a first key may be");
  let cfg: unknown;
  try {
    // "error": the parser's warnings (an unresolved tag, say) go to the console with the line they came from.
    cfg = parse(text, { logLevel: "error" });
  } catch {
    throw new Error(NOT_TALOSCONFIG);
  }
  const { context, contexts } = (typeof cfg === "object" && cfg !== null ? cfg : {}) as { context?: unknown; contexts?: unknown };
  if (typeof contexts !== "object" || contexts === null) throw new Error(NOT_TALOSCONFIG);
  const picked = name !== undefined && name !== "" ? name : typeof context === "string" ? context : "";
  if (!Object.hasOwn(contexts, picked)) throw new Error(name !== undefined && name !== "" ? "the talosconfig holds no context of that name" : "the talosconfig names no current context");
  const c = (contexts as Record<string, unknown>)[picked] as Record<string, unknown> | null;
  const { ca, crt, key, endpoints } = c ?? {};
  if (![ca, crt, key].every((v) => typeof v === "string" && v !== "")) throw new Error(NO_CREDENTIALS);
  const endpoint = Array.isArray(endpoints) && typeof endpoints[0] === "string" ? endpoints[0] : "";
  // How many endpoints the context lists: the page uses the first and says so.
  const count = Array.isArray(endpoints) ? endpoints.filter((e) => typeof e === "string").length : 0;
  // `endpoints: []` as Talos writes it, so the two cuts parse to one document.
  return { document: stringify({ context: picked, contexts: { [picked]: { endpoints: [], ca, crt, key } } }), endpoint, count };
}

// The page's `send`. A write that carries the document (it has a `released` hook) is kept per write layer,
// which outlives this page, and at most one such write stays open: the same request again repeats it (in
// flight, or unknown and repeatable) and hands it this call's hook; any other waits for the open one's
// answer, then dismisses what that left open. A second write beside an open one would leave the first,
// repeatable, holding the document.
// The choice of write is made one request at a time (`gate`), and the answer is awaited outside it.
const kept = new WeakMap<object, { held: Write[]; gate: Promise<unknown> }>();
export function sender(writes: Pick<Writes, "submit" | "repeatable" | "dismiss" | "open">, alive: () => boolean): Env["send"] {
  const s = kept.get(writes) ?? { held: [], gate: Promise.resolve() };
  kept.set(writes, s);
  const left: Outcome = { kind: "not-done", why: "the page was left", problem: null };
  return async (spec) => {
    if (!alive()) return left;
    const hook = spec.released;
    if (hook === undefined) return writes.submit(new Write(spec));
    const same = (w: Write) => w.spec.method === spec.method && w.spec.path === spec.path && JSON.stringify(w.spec.body) === JSON.stringify(spec.body);
    const choose = async (): Promise<{ sent: Promise<Outcome> }> => {
      // The page may have been left while an earlier request was answered.
      if (!alive()) return { sent: Promise.resolve(left) };
      let w = s.held.find((x) => same(x) && (x.running !== undefined || writes.repeatable(x)));
      if (w !== undefined) w.spec.released = hook;
      else {
        for (const old of s.held) {
          await old.running?.catch(() => undefined);
          if (writes.open.includes(old)) writes.dismiss(old);
        }
        s.held.length = 0;
        // And again while the open write's answer was awaited.
        if (!alive()) return { sent: Promise.resolve(left) };
        w = new Write(spec);
        s.held.push(w);
      }
      return { sent: writes.submit(w) };
    };
    const chosen = s.gate.then(choose);
    s.gate = chosen.catch(() => undefined);
    return (await chosen).sent;
  };
}

// A sentence from a reason that starts in lower case and ends with no full stop.
const sentence = (reason: string) => `${reason.charAt(0).toUpperCase()}${reason.slice(1)}${reason.endsWith(".") ? "" : "."}`;

// Whether an endpoint has the shape of an IP address, by the server's own first split
// (internal/talos/endpoint.go ParseEndpoint): text in brackets holds only hex digits, colons and dots
// (an IPv6 address); more than one colon, with only those characters, is an IPv6 address the server will
// ask to have bracketed; and with at most one colon the host before it is four dotted numbers. Anything
// else is a name. Both are sent as they are and the server's own sentence says what is wrong with an
// octet, a port or the brackets: the page copies the shape, never the parser. The page asks only to say,
// after a read, whether the server resolved a name.
const IPV4 = /^\d{1,3}(\.\d{1,3}){3}$/;
const V6_TEXT = /^[0-9a-f:.]+$/i;
export function isAddress(endpoint: string): boolean {
  if (endpoint.startsWith("[")) {
    const end = endpoint.indexOf("]");
    const inner = endpoint.slice(1, end < 0 ? undefined : end);
    // An IPv6 address has a colon; four dotted numbers in brackets are the server's to refuse.
    return V6_TEXT.test(inner) && (inner.includes(":") || IPV4.test(inner));
  }
  const colons = endpoint.split(":").length - 1;
  if (colons > 1) return V6_TEXT.test(endpoint);
  return IPV4.test(colons === 1 ? endpoint.slice(0, endpoint.indexOf(":")) : endpoint);
}

const NO_ENDPOINT = "The talosconfig's context names no endpoint: type the IP address of a control-plane node.";
// After a refused read of a name: the server resolves the name once, dials that address and checks the node's
// certificate against the name, so a name that does not resolve, or one the nodes' certificates do not list, is
// a 503 a node's own address does not meet. The server's 503 does not say which of the two, so the page names both.
const nameHint = (endpoint: string) =>
  ` The endpoint ${endpoint} is a name: the nodes' certificates may not list it. Add the name to machine.certSANs of the control-plane machines, or type the IP address, and the port if it is not 50000, of a control-plane node in the endpoint field.`;

// The endpoint a read will use: the one typed, else the chosen context's first, with how many the context
// lists when it is the context's. Undefined when the text is not a talosconfig the page can cut (the read's
// own sentence says why).
function readEndpoint(text: string, context: string | undefined, typed: string | undefined): { endpoint: string; count: number } | undefined {
  if (typed !== undefined && typed !== "") return { endpoint: typed, count: 1 };
  try {
    const { endpoint, count } = cutContext(text, context);
    return { endpoint, count };
  } catch {
    return undefined;
  }
}

// What the page says, before the button, about the endpoint the read will use. Nothing without a file the
// page can cut.
export function endpointNote(text: string | undefined, context: string, typed: string): string | undefined {
  if (text === undefined) return undefined;
  const read = readEndpoint(text, context, typed.trim());
  if (read === undefined) return undefined;
  if (read.endpoint === "") return NO_ENDPOINT;
  // The page uses one endpoint: the first of a list says so, and says how long the list is.
  if (read.count > 1) return `The page will try the first of ${read.count} endpoints, ${read.endpoint}.`;
  return `The page will try the cluster at ${read.endpoint}.`;
}

// A discovery's failure as whole sentences, with no lead of its own: the page writes "Nothing was
// recorded." once, because a discovery only reads.
function discoveryWhy(o: Outcome): string {
  if (o.kind === "refused") return o.problem === null ? `The server answered ${o.status}.` : problemSentences(o.problem, o.status);
  if (o.kind === "done") return "";
  return `${sentence(o.why)}${words(o.problem, true)}`;
}

// A step's failure in the words of the form's own notice (client contracts §8.2), from the outcome and nothing of
// the request: the problem's sentence and next step, never its code, rule or request number. Where the outcome is
// unknown the next step is left out and the line says to check the current state.
function reason(o: Outcome): string {
  if (o.kind === "refused") return o.problem === null ? `not saved: the server answered ${o.status}.` : `not saved. ${problemSentences(o.problem, o.status)}`;
  if (o.kind === "done") return "";
  const doubt = stateOf(o) === "unknown";
  return `${doubt ? `unknown whether it was saved: ${o.why}. ${CHECK_STATE}` : `not saved: ${o.why}.`}${words(o.problem, !doubt)}`;
}
const words = (p: Problem | null, next: boolean) => (p === null ? "" : ` ${problemSentences(p, p.status, next)}`);
const stateOf = (o: Outcome): LineState => (o.kind === "unknown" || (o.kind === "set-aside" && o.ambiguous) ? "unknown" : "failed");

const members = (f: Discovery) => f.members;
// How a member is named in a step's line and in the list of what a stopped press left: its hostname, else its node id.
const labelOf = (m: Discovery["members"][number]) => (m.hostname === "" ? m.nodeId : safeWord(m.hostname));
// An IPv6 address is bracketed where the server takes an endpoint, as the CLI's endpointArg does.
const endpointOf = (address: string) => (address.includes(":") && !address.startsWith("[") ? `[${address}]` : address);
// The address that carries the first key: the first control-plane member that answered.
const keyAddress = (f: Discovery) => members(f).find((m) => m.role === "controlplane" && m.answered)?.address;

// Reads the cluster's members with the cut talosconfig, and refuses what the CLI refuses before it records
// anything: a contract this server does not render, and a cluster with no control-plane member to carry the
// first key. The file's text is held only as its cut. A name is sent as it is: the server resolves it once
// and answers the address it dialled (found.endpoint). Each `why` is whole sentences with no lead of its
// own, because the page writes "Nothing was recorded." once: a discovery only reads.
export async function discoverCluster(env: Env, text: string, o: { context?: string; endpoint?: string }): Promise<{ ok: true; plan: Plan } | { ok: false; why: string; reference?: Reference }> {
  let cut;
  try {
    cut = cutContext(text, o.context);
  } catch (e) {
    return { ok: false, why: sentence((e as Error).message) };
  }
  const typed = (o.endpoint ?? "").trim();
  const endpoint = typed !== "" ? typed : cut.endpoint;
  if (endpoint === "") return { ok: false, why: NO_ENDPOINT };
  const out = await env.send({ method: "POST", path: "/discoveries", resource: "/clusters", body: { kind: "talosconfig", document: cut.document, endpoint }, released: () => {} });
  if (out.kind !== "done") return { ok: false, why: `${discoveryWhy(out)}${out.kind === "refused" && out.status === 503 && !isAddress(endpoint) ? nameHint(endpoint) : ""}`, ...referenceOf(out) };
  const found = out.data as Discovery;
  if (!found.supported) return { ok: false, why: `Contract ${found.contract} is not the contract of the machinery in this server, so it would refuse the cluster.` };
  if (keyAddress(found) === undefined) return { ok: false, why: "No control-plane member answered, so no address can carry the first key. Run it again once one does." };
  return { ok: true, plan: { found, endpoint, name: "", kube: found.kubernetesEndpoint, document: cut.document, keyed: false, machines: {}, imports: {}, assigned: {}, lines: [] } };
}

// Runs the steps in order, each finding what exists before it writes, and reports the lines after each.
// True when every step ended; false at the first that did not, whose line says why.
export async function onboardCluster(env: Env, p: Plan, report: (lines: Line[]) => void): Promise<boolean> {
  const set = (l: Line) => {
    const at = p.lines.findIndex((x) => x.key === l.key);
    if (at < 0) p.lines.push(l);
    else p.lines[at] = l;
    report([...p.lines]);
  };
  const fail: Fail = (key, label, o) => {
    set({ key, label, state: typeof o === "string" ? "failed" : stateOf(o), detail: typeof o === "string" ? o : reason(o), ...(typeof o !== "string" && referenceOf(o)) });
    return false;
  };
  const list = async <T>(path: string, query: string) => {
    const r = await readPages<T>(env, path, query, { items: [], next: undefined, restarted: false }, true);
    return r.kind === "ok" ? r.data.items : (`the server's answer to a read of ${path} did not hold the list: ${r.kind}` as const);
  };
  const steps: [string, () => Promise<boolean>][] = [
    ["Cluster", () => cluster(env, p, set, fail)],
    ["First key", () => firstKey(env, p, set, fail)],
    ["Machines", () => machines(env, p, set, fail)],
    ["Draft", () => draft(env, p, set, fail, list)],
    ["Imports", () => imports(env, p, set, fail, list)],
    ["Assignments", () => assignments(env, p, set, fail, list)],
  ];
  // What the stopped step has not finished, one by name where it has members, then the steps after it.
  const left: Record<string, () => string[]> = {
    Machines: () => members(p.found).filter((m) => m.answered && m.platform !== "" && p.machines[m.nodeId] === undefined).map((m) => `Machine ${labelOf(m)}`),
    Imports: () => members(p.found).filter((m) => p.machines[m.nodeId] !== undefined && p.imports[m.nodeId] === undefined).map((m) => `Import ${labelOf(m)}`),
    Assignments: () => members(p.found).filter((m) => p.imports[m.nodeId] !== undefined && p.assigned[m.nodeId] === undefined).map((m) => `Assignment ${labelOf(m)}`),
  };
  p.unsent = undefined;
  for (const [i, [name, go]] of steps.entries()) {
    if (await go()) continue;
    p.unsent = [...(left[name]?.() ?? []), ...steps.slice(i + 1).map(([later]) => later)];
    return false;
  }
  return true;
}

// The page's sentence after a stop. `moved`: the epoch changed after the plan's first press began, so the plan
// is never run again, only chosen again from the file (which finds what is recorded and begins under the new
// epoch); what is not finished is named (a write may have been answered before the stop, so never "not sent"). `choose`: another stop that offers the file again (the first key's
// write is released).
// `kept`: the shell keeps this page, with its actions off, while it held the file when the epoch changed, so the
// way on is to open the page again.
export function stoppedSentence(label: string, moved: boolean, choose: boolean, unsent: readonly string[] | undefined, kept = false): string {
  if (moved) {
    const rest = unsent !== undefined && unsent.length > 0 ? ` (not finished: ${unsent.join(", ")})` : "";
    const way = kept ? "Open the machines and connect the cluster again: choosing the file" : "Choosing the file again";
    return `Stopped at ${label}. The installation's epoch changed after this onboarding began, so nothing more was sent under it${rest}. ${way} finds what is recorded and finishes the rest under the new epoch.`;
  }
  return `Stopped at ${label}. Running it again finds what is recorded and finishes the rest${choose ? ": choose the file again" : ""}.`;
}

// One press of a plan: `onboardCluster` with every write sent under the plan's epoch, set by its first press
// from `press.current()`; a client that holds none sends nothing (`NO_EPOCH_YET`). A press stops quietly before a write when `press.valid()` says the shell has ended the
// reads the press began with (an epoch change does, and so do a revocation and a sign-out: `not-done`, and
// the plan can be run again); a write the server refuses for the epoch (412) is set aside by the write layer.
// `epochChanged` is set only from a fact: that set-aside, or, once the press has stopped, the client's epoch
// (`press.current()`) differing from the plan's (one comparison, at the end, which also covers a quiet stop). It says the plan cannot be run again, since the ids it holds were read under the old epoch.
export async function pressOnboarding(env: Env, p: Plan, press: { current: () => string | undefined; valid: () => boolean }, report: (lines: Line[]) => void): Promise<{ ok: boolean; epochChanged: boolean }> {
  p.epoch ??= press.current();
  const epoch = p.epoch;
  // A client that holds no epoch has nothing to pin the plan to, and the write layer would pick one of its own for each write.
  if (epoch === undefined) {
    report([{ key: "epoch", label: "Epoch", state: "failed", detail: NO_EPOCH_YET }]);
    return { ok: false, epochChanged: false };
  }
  let epochChanged = false;
  const send: Env["send"] = async (spec) => {
    if (!press.valid()) return { kind: "not-done", why: "the page's reads ended", problem: null };
    const o = await env.send({ ...spec, epoch });
    if (o.kind === "set-aside" && o.why === EPOCH_CHANGED_ASIDE) epochChanged = true;
    return o;
  };
  const ok = await onboardCluster({ ...env, send }, p, report);
  // A stop that only reads saw (an answer discarded under a new epoch) leaves no later write to notice it.
  const now = press.current();
  if (!ok && now !== undefined && now !== epoch) epochChanged = true;
  // Nothing sends this plan again: its copy of the talosconfig goes now, not at the next read.
  if (epochChanged) p.document = undefined;
  return { ok, epochChanged };
}

type Set = (l: Line) => void;
type Fail = (key: string, label: string, o: Outcome | string) => false;
type List = <T>(path: string, query: string) => Promise<T[] | string>;

async function cluster(env: Env, p: Plan, set: Set, fail: Fail): Promise<boolean> {
  if (p.cluster !== undefined) return true;
  const label = `Cluster ${p.name}`;
  const o = await env.send({ method: "POST", path: "/clusters", resource: "/clusters", body: { name: p.name, endpoint: p.kube, contract: p.found.contract, talosClusterId: p.found.clusterId } });
  const named = o.kind === "refused" ? existing(o.status, o.problem, "/clusters") : undefined;
  const id = o.kind === "done" ? (o.data as { id?: string } | null)?.id : named?.kind === "cluster" ? named.id : undefined;
  if (id === undefined) return o.kind === "done" ? fail("cluster", label, "the cluster was recorded, but its answer names no cluster") : fail("cluster", label, o);
  // A cluster the server already held keeps its own name, which the draft's title and the lines then use.
  if (o.kind !== "done") {
    const r = await env.read<{ name: string }>(`/clusters/${enc(id)}`);
    if (r.kind !== "ok") return fail("cluster", label, `the cluster already recorded could not be read: ${r.kind}`);
    p.name = r.data.name;
  }
  p.cluster = id;
  set({ key: "cluster", label: `Cluster ${p.name}`, state: o.kind === "done" ? "done" : "existing", to: `/clusters/${id}` });
  return true;
}

async function firstKey(env: Env, p: Plan, set: Set, fail: Fail): Promise<boolean> {
  if (p.keyed) return true;
  const label = "First key";
  const address = keyAddress(p.found);
  if (p.document === undefined || address === undefined) return fail("first-key", label, "the talosconfig is no longer held here: choose the file again, which finds what is recorded");
  const o = await env.send({
    method: "POST", path: `/clusters/${enc(p.cluster!)}/first-keys`, resource: `/clusters/${p.cluster}`,
    body: { kind: "talosconfig", document: p.document, endpoint: endpointOf(address) },
    // The page's copy goes with the write's: nothing holds the document once it can no longer be repeated.
    released: () => void (p.document = undefined),
  });
  if (o.kind !== "done") return fail("first-key", label, o);
  p.keyed = true;
  set({ key: "first-key", label, state: "done", ...((o.data as { replaced?: boolean } | null)?.replaced === true && { detail: "it replaced the key stored before" }) });
  return true;
}

// The note for a machine whose name the server refused, undefined when the answer is no refusal of a name
// that was sent: `name-in-use` names the machine that holds it, which the page shows as a link to it and
// never as an identifier, and `invalid-request` is a grammar the server does not take (a server older than
// the widened one takes one label).
function nameRefusal(o: Outcome, name: string): { note: string; holder?: string } | undefined {
  if (o.kind !== "refused") return undefined;
  if (o.problem?.type === "urn:bronzeward:problem:name-in-use") {
    const holder = o.problem["machine"];
    return { note: `no name: the name "${name}" is already held by another machine of the cluster; rename that machine, then name this one`, ...(typeof holder === "string" && { holder }) };
  }
  if (o.problem?.type === "urn:bronzeward:problem:invalid-request") return { note: `no name: the server refused the hostname "${name}" as a name` };
  return undefined;
}

async function machines(env: Env, p: Plan, set: Set, fail: Fail): Promise<boolean> {
  for (const m of members(p.found)) {
    const key = `machine:${m.nodeId}`;
    const label = `Machine ${labelOf(m)}`;
    if (p.machines[m.nodeId] !== undefined) continue;
    if (!m.answered || m.platform === "") {
      set({ key, label, state: "skipped", detail: m.answered ? "answered, reported no platform" : "did not answer" });
      continue;
    }
    // By the SMBIOS UUID where the node reports one, else by its node ID: the import checks that identity.
    const nm = nameOf(m.hostname);
    const body = { cluster: p.cluster, ...(m.smbiosUuid !== "" ? { smbiosUuid: m.smbiosUuid } : { talosNodeId: m.nodeId }), platform: m.platform, talosEndpoint: endpointOf(m.address), ...(nm.name !== undefined && { name: nm.name }) };
    const write = (b: object) => env.send({ method: "POST", path: "/machines", resource: `/machines?${new URLSearchParams({ cluster: p.cluster! })}`, body: b });
    let o = await write(body);
    let note = nm.note;
    let holder: string | undefined;
    // A name the server refuses (another machine holds it, or the grammar rejects it) never stops the run:
    // the machine is recorded without it, with the reason, to be named afterwards.
    const refused = nm.name === undefined ? undefined : nameRefusal(o, nm.name);
    if (refused !== undefined) {
      const { name: _unsent, ...unnamed } = body;
      o = await write(unnamed);
      ({ note, holder } = refused);
    }
    const named = o.kind === "refused" ? existing(o.status, o.problem, "/machines") : undefined;
    const id = o.kind === "done" ? (o.data as { id?: string } | null)?.id : named?.kind === "machine" ? named.id : undefined;
    if (id === undefined) return o.kind === "done" ? fail(key, label, "the machine was recorded, but its answer names no machine") : fail(key, label, o);
    if (o.kind !== "done") {
      const r = await env.read<{ cluster: string }>(`/machines/${enc(id)}`);
      if (r.kind !== "ok") return fail(key, label, `the machine already recorded could not be read: ${r.kind}`);
      if (r.data.cluster !== p.cluster) return fail(key, label, `it is recorded in another cluster, ${r.data.cluster}: nothing after the machines ran. Remove it from that cluster or leave the node out of this one`);
    }
    p.machines[m.nodeId] = id;
    // A machine this run recorded says how it was named, or why it was not: never a silent drop.
    set({ key, label, state: o.kind === "done" ? "done" : "existing", to: `/machines/${id}`, ...(o.kind === "done" && note !== undefined && { detail: note }), ...(o.kind === "done" && holder !== undefined && { holder }) });
  }
  return true;
}

async function draft(env: Env, p: Plan, set: Set, fail: Fail, list: List): Promise<boolean> {
  if (p.draft !== undefined) return true;
  const title = `onboard ${p.name}`;
  const label = `Draft ${title}`;
  const items = await list<Draft>("/drafts", "");
  if (typeof items === "string") return fail("draft", label, items);
  const open = items.find((d) => d.title === title && d.state === "open" && d.cluster === p.cluster);
  let id = open?.id;
  if (id === undefined) {
    const o = await env.send({ method: "POST", path: "/drafts", resource: "/drafts", body: { cluster: p.cluster, title } });
    id = o.kind === "done" ? (o.data as { id?: string } | null)?.id : undefined;
    if (id === undefined) return o.kind === "done" ? fail("draft", label, "the draft was opened, but its answer names no draft") : fail("draft", label, o);
  }
  p.draft = id;
  set({ key: "draft", label, state: open === undefined ? "done" : "existing", to: `/drafts/${id}` });
  return true;
}

// Each machine whose import was released gets an assignment that selects nothing, so its release holds an
// assignment revision and a plan can bind it (ginsys/bronzeward#1068): the machine's configuration is its import
// base alone. A machine that already holds an assignment head, or has an assignment entry in the draft (a removal
// is a person's choice too), is left as it is. The draft is read again before each write for its ETag and entries.
async function assignments(env: Env, p: Plan, set: Set, fail: Fail, list: List): Promise<boolean> {
  const heads = await list<{ machine: string }>("/assignments", "");
  if (typeof heads === "string") return fail("assignments", "Assignments", heads);
  for (const m of members(p.found)) {
    const machine = p.machines[m.nodeId];
    const key = `assignment:${m.nodeId}`;
    const label = `Assignment ${labelOf(m)}`;
    if (machine === undefined || p.imports[m.nodeId] === undefined || p.assigned[m.nodeId] !== undefined) continue;
    const r = await env.read<Draft>(`/drafts/${enc(p.draft!)}`);
    if (r.kind !== "ok" || r.etag === undefined) return fail(key, label, "the draft could not be read for its ETag: run it again");
    const held = r.data.entries.some((e) => e.kind === "assignment" && e.machine === machine) || heads.some((h) => h.machine === machine);
    if (!held) {
      const o = await env.send({ method: "PUT", path: `/drafts/${enc(p.draft!)}/assignments/${enc(machine)}`, resource: `/drafts/${p.draft}`, ifMatch: r.etag, body: { profiles: [], fragments: {} } });
      if (o.kind !== "done") return fail(key, label, o);
    }
    p.assigned[m.nodeId] = p.draft!;
    set({ key, label, state: held ? "existing" : "done", to: `/drafts/${p.draft}`, detail: held ? "an assignment exists: left as it is" : "selects nothing: its configuration is its import base alone" });
  }
  return true;
}

// How long an import is followed here, in one-second reads; past it the line says to read the import.
const FOLLOW = 120;
// An import of the machine into this draft that stands, the furthest on: a running one outranks a release.
const RANK: Record<string, number> = { released: 1, held: 2, resumed: 2, paused: 3 };

async function imports(env: Env, p: Plan, set: Set, fail: Fail, list: List): Promise<boolean> {
  for (const m of members(p.found)) {
    const machine = p.machines[m.nodeId];
    const key = `import:${m.nodeId}`;
    const label = `Import ${labelOf(m)}`;
    if (machine === undefined || p.imports[m.nodeId] !== undefined) continue;
    const had = await list<Ingestion>("/ingestions", new URLSearchParams({ machine }).toString());
    if (typeof had === "string") return fail(key, label, had);
    let ing = had.filter((i) => i.draft === p.draft && (RANK[i.state] ?? 0) > 0).sort((a, b) => RANK[b.state]! - RANK[a.state]!)[0];
    let state: LineState = "existing";
    if (ing === undefined) {
      // The draft's ETag is read again for each import: the one before it changed the draft.
      const r = await env.read<Draft>(`/drafts/${enc(p.draft!)}`);
      if (r.kind !== "ok" || r.etag === undefined) return fail(key, label, "the draft could not be read for its ETag: run it again");
      const body = startBody({ kind: "import", machine, draft: p.draft!, source: "machine", staging: "encrypted", review: false, doc: "", marks: "" });
      const o = await env.send({ method: "POST", path: "/ingestions", resource: "/ingestions", ifMatch: r.etag, body });
      const id = o.kind === "done" ? (o.data as { ingestion?: string } | null)?.ingestion : undefined;
      if (id === undefined) return o.kind === "done" ? fail(key, label, "the import was started, but its answer names no ingestion") : fail(key, label, o);
      ing = { id, state: "held" } as Ingestion;
      state = "done";
    }
    const to = `/ingestions/${ing.id}`;
    for (let n = 0; n < FOLLOW && (ing.state === "held" || ing.state === "resumed"); n++) {
      if (n > 0) await env.wait(1000);
      const r = await env.read<Ingestion>(to);
      if (r.kind !== "ok") return fail(key, label, `the import could not be read: ${r.kind}. Read ${to}`);
      ing = r.data;
    }
    if (ing.state === "released") {
      p.imports[m.nodeId] = ing.id;
      set({ key, label, state, to });
      continue;
    }
    set({ key, label, state: ing.state === "paused" || ing.state === "held" || ing.state === "resumed" ? "stopped" : "failed", detail: `the import is ${safeWord(ing.state)}: read it`, to });
    return false;
  }
  return true;
}
