// The shell's recovery parts (journeys §5.9, §8; client contracts §8.5): the start mode, the
// recovery banner with its scope summary and process restriction, and the state before a recovery
// start's entry.

import { type FormEvent, type ReactNode, useEffect, useRef, useState, useSyncExternalStore } from "react";
import { Result, useSend, useWrites } from "./forms.tsx";
import { Head, Id, type Load, Scroll, useReads, useSettled } from "./list.tsx";
import type { Problem, ReadResult } from "./request.ts";
import type { Outcome } from "./write.ts";
import { useGate, useOffline, useRecoveryState } from "./shell.tsx";
import { clock, Disconnected, ProblemFields, problemFields, problemTail, Refused, Status, when } from "./states.tsx";
import type { components } from "./api/v1";

export type Recovery = components["schemas"]["RecoveryBody"];
type Scope = components["schemas"]["RecoveryScope"];
type EntryBody = components["schemas"]["EntryBody"];

// §8.5 start mode: the last one an answer showed, unknown before any.
export type StartMode = "normal" | "recovery" | "unknown";

export const PRE_ENTRY = "This server was started for recovery and serves only the recovery entry until it is recorded.";
export const RECOVERY_START =
  "Server started for recovery: plan creation, approval and unfreeze wait for its normal restart, and publications it accepts complete only then. Imports complete here.";
export const UNKNOWN_START = "Whether this server was started for recovery is not known yet.";

// A 409 recovery-mode-active without a scope member is the process's refusal, a recovery start's;
// with one it is a scope's and says nothing about the start mode (§8.5 item 1).
export function startRefusal(res: ReadResult<unknown>): boolean {
  if (res.kind !== "refused" || res.status !== 409 || res.problem === null) return false;
  return res.problem.type === "urn:bronzeward:problem:recovery-mode-active" && !("scope" in res.problem);
}

// The start mode an answer shows, or undefined when it shows none.
export function startModeOf(res: ReadResult<{ startMode?: string }>): StartMode | undefined {
  if (res.kind === "ok") return res.data.startMode === "recovery" ? "recovery" : res.data.startMode === "normal" ? "normal" : undefined;
  return startRefusal(res) ? "recovery" : undefined;
}

// The wireframe's "4 of 4 scopes not released: none can commit."
export function scopeSummary(scopes: readonly Scope[]): string {
  const held = scopes.filter((s) => s.state !== "released").length;
  if (scopes.length === 0) return "No machine scopes.";
  if (held === 0) return `All ${scopes.length} scopes released.`;
  return `${held} of ${scopes.length} scopes not released: ${held === scopes.length ? "none can commit" : "those cannot commit"}.`;
}

// Shown while responses carry Bronzeward-Recovery-Mode: true. It never disables an action: only a
// scope's state or the process refuses one, and the server's refusal says which.
export function RecoveryBanner({ recovery, mode }: { recovery: Recovery | undefined; mode: StartMode }) {
  const since = recovery?.entry != null ? ` since ${clock(new Date(recovery.entry.enteredAt))}` : "";
  return (
    <Status testid="recovery">
      Recovery mode{since}{recovery !== undefined ? `, epoch ${recovery.epoch}. ${scopeSummary(recovery.scopes)}` : "."} Drafts can be edited.{" "}
      {mode === "recovery" ? RECOVERY_START : mode === "unknown" ? UNKNOWN_START : ""}
    </Status>
  );
}

// The banner's own read when it found no data, under the banner, so the missing entry and scopes
// are explained with the catalogue's text (W11). Once its data was shown, a read without answer
// keeps that data under the offline banner instead.
export function RecoveryRead({ res }: { res: ReadResult<Recovery> }) {
  if (res.kind === "ok" || res.kind === "discarded") return null;
  if (res.kind === "transport") return <Disconnected testid="recovery-read" />;
  if (res.kind === "defect") return <Status testid="recovery-read">Not read: {res.message}.</Status>;
  return <Refused testid="recovery-read" problem={res.problem} status={"status" in res ? res.status : 0} verb="read" />;
}

// The refusal's own fields stay under the restriction (§8.2): title, detail and the request ID.
export const PreEntry = ({ res, onRead }: { res: ReadResult<unknown>; onRead: () => void }) => (
  <>
    <Status actions={<button type="button" onClick={onRead}>Read again</button>}>{PRE_ENTRY}</Status>
    <ProblemFields text={problemFields(res)} />
    <EnterRecovery onRead={onRead} />
  </>
);

// The administrator steps stay outside the browser (B30): the restores, the auth.deniedSubjects edit
// and the restarts. The operator guide is published with the documentation site.
const GUIDE = "https://ginsys.github.io/bronzeward/operator/recovery/";
const STEPS = [
  ["Restoring from a backup", "restoring-from-a-backup"],
  ["Record the entry", "3-record-the-entry"],
  ["Restart normally, still in recovery mode", "4-restart-normally-still-in-recovery-mode"],
] as const;
const Steps = () => (
  <p>
    Administrator steps, outside the browser:{" "}
    {STEPS.map(([title, anchor], i) => <span key={anchor}>{i > 0 && ", "}<a href={`${GUIDE}#${anchor}`}>{title}</a></span>)}.
  </p>
);

// T32's effects text, as the CLI says it (cmd/bronzewardctl/recovery.go).
export const ENTRY_EFFECTS = [
  "The entry mints a new epoch: every other client's pending write is refused.",
  "It marks every machine's scope pre-restore-unaccounted.",
  "It takes over every non-terminal apply-config operation as unresolved, and ends the running import and publish jobs.",
  "Recovery mode then lasts until recovery exit, once every scope is released.",
];
export const NORMAL_START = "This server was started normally, so it takes no recovery entry. Stop every instance, restore the backups, and start one for recovery.";
export const REVOCATION_LIMITS = [
  "A revocation is permanent: nothing undoes it.",
  "It adds no auth.deniedSubjects entry and reissues no token, and a restore of the database can erase it.",
  "First add the subject to auth.deniedSubjects, a human by iss and sub, a service identity by its identity and name, and leave it there. After a restore, record this revocation again.",
];

// What each scope state needs next (T31), as text; the controls are the recovery decisions' (#365).
export function nextStep(state: string): string {
  switch (state) {
    case "pre-restore-unaccounted":
      return "Account the scope: record that no request sent before the restore can still land.";
    case "ready":
      return "Release the scope. A release is not an approval.";
    case "released":
      return "None.";
    case "unresolved":
      return "Read the machine's operations. If one has not ended, it is classified after the normal restart: restart, then mark the scope again. If none holds the scope, mark it again now.";
    case "blocked":
      return "Mark the scope, then read its recorded cause: a scope stays blocked until it is marked. If the cause names a missing key or observation, repair that, or publish a new release for a lost key version.";
    default:
      return "None.";
  }
}

// The scopes in recovery: a scope in `normal` state is outside it.
const inRecovery = (scopes: readonly Scope[]) => scopes.filter((s) => s.state !== "normal");
export const unreleased = (scopes: readonly Scope[]) => inRecovery(scopes).filter((s) => s.state !== "released");

// The result of the entry this page made, kept after the epoch change its commit causes replaced the
// page it was sent from (the entry's form lives on the start's pre-entry state, which that read ends).
// It is the entry's body; null when the success could not be read (it is still saved); or the problem of
// the 409 that refused it (REC-02): without `epoch` the server takes no entry, so the restriction the
// page showed is stale; with `epoch` another entry committed under another key. The form does not
// outlive either, so the header keeps the refusal's title, detail and request (§8.2).
type Noted = EntryBody | null | { refused: Problem };
// What a refused entry means (REC-02), one sentence.
const reading = (p: Problem) =>
  p["epoch"] === undefined ? NORMAL_START : `An entry is already recorded on this server, in epoch ${String(p["epoch"])}, under another request. Nothing was added. Read the console.`;
let entered: Noted | undefined;
const listeners = new Set<() => void>();
export const noteEntry = (e: Noted | undefined) => {
  entered = e;
  for (const f of listeners) f();
};
const watch = (f: () => void) => (listeners.add(f), () => void listeners.delete(f));

export function Entered() {
  useSyncExternalStore(watch, () => entered, () => entered);
  if (entered === undefined) return null;
  const dismiss = <button type="button" onClick={() => noteEntry(undefined)}>Dismiss</button>;
  if (entered === null) {
    return <Status testid="entered" actions={dismiss}>Recovery entry saved (the server answered), but its answer could not be read. Read the Recovery page.</Status>;
  }
  if ("refused" in entered) {
    return <Status testid="entered" actions={dismiss}>{reading(entered.refused)}{problemTail(entered.refused)}</Status>;
  }
  return (
    <Status testid="entered" actions={dismiss}>
      Recovery mode entered (act {entered.act}). Epoch {entered.epoch}, was {entered.previousEpoch}. Scopes marked pre-restore-unaccounted: {entered.machines} machines.
      Apply-config operations taken over: {entered.operations.length === 0 ? "none" : entered.operations.join(", ")}. Restart the server normally when ready; recovery mode continues.
    </Status>
  );
}

const TIME = /^(\d{4})-(\d\d)-(\d\d)T(\d\d):(\d\d)(?::(\d\d)(?:\.\d+)?)?(?:Z|[+-](\d\d):(\d\d))$/;
const FAMILIES = ["database", "provider", "credential-store"] as const;
// An RFC 3339 time with a zone whose calendar parts exist: Date.parse rolls 2026-02-30 over to March,
// which would record a different time than the operator typed.
export const stamp = (v: string): string | undefined => {
  const m = TIME.exec(v);
  if (m === null) return undefined;
  const [year, month, day, hour, minute, second = 0, zoneHour = 0, zoneMinute = 0] = m.slice(1).map((p) => (p === undefined ? undefined : Number(p)));
  const days = new Date(Date.UTC(year!, month!, 0)).getUTCDate();
  const exists = month! >= 1 && month! <= 12 && day! >= 1 && day! <= days && hour! < 24 && minute! < 60 && second < 60 && zoneHour < 24 && zoneMinute < 60;
  return exists && !Number.isNaN(Date.parse(v)) ? new Date(v).toISOString() : undefined;
};
const text = (d: FormData, n: string) => String(d.get(n) ?? "").trim();
// The 409 conflict an entry meets on a start that takes none (no `epoch`) or one already entered under
// another key (with `epoch`).
const conflict = (o: Outcome | undefined): Problem | undefined =>
  o?.kind === "refused" && o.status === 409 && o.problem?.type === "urn:bronzeward:problem:conflict" ? o.problem : undefined;

// B31, on a recovery start before its entry. REC-02: a 409 conflict without `epoch` is read as a
// normal start; one with `epoch` is a recovery start whose entry committed under another key.
export function EnterRecovery({ onRead }: { onRead?: () => void }) {
  const writes = useWrites();
  const [w, send] = useSend();
  const [typed, type] = useState("");
  const [bad, complain] = useState<string>();
  // Before the entry no role was read, so only the offline banner gates it: the server decides.
  const off = useOffline() ? "Offline" : undefined;
  if (writes === undefined) return null;
  const held = w !== undefined && writes.open.includes(w);
  const submit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    // The button is off until enter is typed, but a submit event does not need the button.
    if (typed !== "enter" || off !== undefined || held) return;
    const d = new FormData(e.currentTarget);
    const restored = FAMILIES.flatMap((family) => {
      const backup = text(d, `${family}-backup`);
      return backup === "" ? [] : [{ family, backup, takenAt: stamp(text(d, `${family}-taken`)) }];
    });
    const stoppedAt = stamp(text(d, "stoppedAt"));
    const reason = text(d, "reason");
    const why =
      restored.length === 0 ? "Name at least one restored backup."
      : restored.some((r) => r.takenAt === undefined) ? "Give the time each backup was taken in RFC 3339 with its zone, such as 2026-11-01T08:00:00Z."
      : stoppedAt === undefined ? "Give the time the restore stopped writes in RFC 3339 with its zone, such as 2026-11-01T08:30:00Z."
      : reason === "" ? "Give a reason."
      : undefined;
    complain(why);
    if (why !== undefined) return;
    const settled = (o: Outcome) => {
      if (o.kind === "done") noteEntry(typeof o.data === "object" && o.data !== null ? (o.data as EntryBody) : null);
      else {
        // REC-02: the refusal is kept in the header, since the read it causes (or §8.5's move to the
        // recovery state) replaces this form. Without `epoch` the start takes no entry and the page's
        // restriction is stale, so the shell reads again.
        const p = conflict(o);
        if (p !== undefined) (noteEntry({ refused: p }), p["epoch"] === undefined && onRead?.());
      }
    };
    send({ method: "POST", path: "/recovery/entries", resource: "/recovery", entry: true, body: { restored, stoppedAt, reason }, settled });
  };
  const o = w?.outcome;
  const p = conflict(o);
  return (
    <form onSubmit={submit} aria-label="Enter recovery">
      <h2>Enter recovery</h2>
      <p>Name each restored backup, when it was taken, and when the restore stopped writes. Times are RFC 3339 with a zone.</p>
      {FAMILIES.map((f) => (
        <p key={f}>
          <label>Restored {f} backup: <input name={`${f}-backup`} autoComplete="off" /></label>{" "}
          <label>taken at: <input name={`${f}-taken`} autoComplete="off" placeholder="2026-11-01T08:00:00Z" /></label>
        </p>
      ))}
      <p><label>Restore stopped at: <input name="stoppedAt" autoComplete="off" placeholder="2026-11-01T08:30:00Z" /></label></p>
      <p><label>Reason: <input name="reason" autoComplete="off" /></label></p>
      <ul>{ENTRY_EFFECTS.map((e) => <li key={e}>{e}</li>)}</ul>
      <Steps />
      <p>
        <label>Type enter to record it: <input value={typed} onChange={(e) => type(e.target.value)} autoComplete="off" /></label>{" "}
        <button type="submit" disabled={typed !== "enter" || off !== undefined || held}>Enter recovery</button>
        {off !== undefined && ` ${off}`}
      </p>
      {bad !== undefined && <p role="alert" data-testid="entry-input">{bad}</p>}
      {p === undefined || o?.kind !== "refused" ? <Result w={w} done={() => "Recovery mode entered."} /> : (
        <>
          <Refused testid="write-result" problem={p} status={o.status} verb="saved" replayed={o.replayed} />
          <Status testid="write-reading">{reading(p)}</Status>
        </>
      )}
    </form>
  );
}

// W10. Read again every 5 s while shown (no stream serves recovery state, cc §8.6 item 6).
export const REREAD_MS = 5000;

export function RecoveryConsole() {
  const settled = useSettled();
  const { again: shellAgain, view } = useReads();
  const [n, again] = useState(0);
  const [read, put] = useState<Load<Recovery>>({ kind: "loading" });
  // One read at a time. The next is scheduled when the last one has settled, whatever its result (a
  // read without answer keeps what is shown), so a slow answer is never discarded by the next tick.
  // A refresh asked for (n, or the shell's) cancels the schedule and reads once the read in flight
  // has settled: `flight` chains the reads, and a refresh superseded while it waits reads nothing.
  const flight = useRef<Promise<unknown>>(Promise.resolve());
  // Whether the console shows an answer now: only then does a read without answer keep it under the
  // offline banner. The first read failing shows the disconnected state instead of loading for good.
  const shown = useRef(false);
  useEffect(() => {
    let live = true;
    let timer: ReturnType<typeof setTimeout> | undefined;
    // A read that rejects (a credential renewal that threw) is a read without answer: the next one
    // is still scheduled, and the chain never rejects.
    const run = flight.current.then(() => (live ? settled<Recovery>("/recovery", () => live, () => shown.current) : undefined)).catch(() => undefined);
    flight.current = run;
    void run.then((res) => {
      if (!live) return;
      if (res !== undefined) (shown.current = res.kind === "ok", put(res));
      timer = setTimeout(() => again((i) => i + 1), REREAD_MS);
    });
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [settled, n, shellAgain, view]);
  const { mode } = useRecoveryState();
  if (read.kind === "loading") return <><h2>Recovery</h2><Status>Loading the recovery state</Status></>;
  if (read.kind === "discarded") return null;
  if (read.kind !== "ok") return <><h2>Recovery</h2><RecoveryRead res={read} /></>;
  const r = read.data;
  const held = unreleased(r.scopes);
  return (
    <>
      <h2>Recovery</h2>
      <Status testid="recovery-state">
        Recovery mode {r.recoveryMode ? "on" : "off"}, epoch {r.epoch}. {mode === "recovery" ? RECOVERY_START : mode === "normal" ? "Server started normally." : UNKNOWN_START}
      </Status>
      {r.entry != null && (
        <p data-testid="recovery-entry">
          Entered {when(r.entry.enteredAt)} by {r.entry.enteredBy}: {r.entry.reason}. Restore stopped {when(r.entry.stoppedAt)}. Restored:{" "}
          {r.entry.restored.map((b) => `${b.family} ${b.backup} (taken ${when(b.takenAt)})`).join("; ")}.
        </p>
      )}
      {r.exit != null && <p data-testid="recovery-exit">Exited {when(r.exit.at)} by {r.exit.exitedBy} (act {r.exit.act}).</p>}
      <Scopes scopes={inRecovery(r.scopes)} />
      <ExitRecovery held={held.length} recoveryMode={r.recoveryMode} onChanged={() => again((i) => i + 1)} />
      <Revocation />
      <Steps />
    </>
  );
}

function Scopes({ scopes }: { scopes: readonly Scope[] }) {
  if (scopes.length === 0) return <Status testid="scopes">No scope is in recovery.</Status>;
  return (
    <Scroll label="Scopes in recovery" testid="scopes">
      <Head cols={["Machine", "Scope state", "Next step"]} />
      <tbody>
        {scopes.map((s) => (
          <tr key={s.machine}>
            <td><Id id={s.machine} /></td>
            <td>{s.state}</td>
            <td>{nextStep(s.state)}</td>
          </tr>
        ))}
      </tbody>
    </Scroll>
  );
}

// B31: disabled with the reason until `GET /recovery` shows every scope released. A send forced past it
// shows the server's refusal, which names the scope (internal/api/recovery_exits.go).
export function ExitRecovery({ held, recoveryMode, onChanged }: { held: number; recoveryMode: boolean; onChanged: () => void }) {
  const [w, send] = useSend();
  const gate = useGate(["recovery-admin"]);
  const writes = useWrites();
  const busy = w !== undefined && writes?.open.includes(w) === true;
  const done = w?.outcome?.kind === "done";
  // Once per completed exit: the parent's callback changes on every render, so it is not a dependency.
  const changed = useRef(onChanged);
  changed.current = onChanged;
  useEffect(() => {
    if (done) changed.current();
  }, [done]);
  const reason = !recoveryMode ? "Recovery mode is off" : held > 0 ? `${held} ${held === 1 ? "scope" : "scopes"} not released (ready is not released)` : undefined;
  const why = gate ?? reason;
  return (
    <form aria-label="Exit recovery" onSubmit={(e) => (e.preventDefault(), send({ method: "POST", path: "/recovery/exits", resource: "/recovery", body: {} }))}>
      <h3>Exit recovery</h3>
      <p>
        <button type="submit" disabled={why !== undefined || busy}>Exit recovery</button>
        {why !== undefined && ` ${why}`}
      </p>
      <Result w={w} done={(d) => `Recovery mode left (act ${(d as components["schemas"]["ExitBody"]).act}).`} />
    </form>
  );
}

// B35: the identity, or the issuer and subject, and a reason; sent only when the identity is typed back.
export function Revocation() {
  const [w, send] = useSend();
  const [identity, setIdentity] = useState("");
  const [iss, setIss] = useState("");
  const [sub, setSub] = useState("");
  const [reason, setReason] = useState("");
  const [typed, type] = useState("");
  const gate = useGate(["recovery-admin"]);
  const writes = useWrites();
  const busy = w !== undefined && writes?.open.includes(w) === true;
  // The identity, or the issuer and subject, not both (RevocationInput). The identity typed back is
  // the identity, or the subject (as the CLI asks).
  const byIdentity = identity.trim() !== "" && iss.trim() === "" && sub.trim() === "";
  const bySubject = identity.trim() === "" && iss.trim() !== "" && sub.trim() !== "";
  const target = byIdentity ? identity.trim() : bySubject ? sub.trim() : "";
  const ready = target !== "" && reason.trim() !== "" && typed === target;
  const why = gate ?? (ready ? undefined : "Name the identity, or the issuer and subject, give a reason, and type the identity back");
  const submit = (e: FormEvent) => {
    e.preventDefault();
    // A submit event does not need the button: the guards that disable it also stop the send, so a
    // permanent revocation goes out neither without the role, nor unconfirmed, nor twice.
    if (why !== undefined || busy) return;
    const body = byIdentity ? { identity: target, reason: reason.trim() } : { iss: iss.trim(), sub: target, reason: reason.trim() };
    send({ method: "POST", path: "/identity-revocations", resource: "/recovery", body });
  };
  return (
    <form aria-label="Revoke identity" onSubmit={submit}>
      <h3>Revoke identity</h3>
      <ul>{REVOCATION_LIMITS.map((l) => <li key={l}>{l}</li>)}</ul>
      <p><label>Identity: <input value={identity} onChange={(e) => setIdentity(e.target.value)} autoComplete="off" /></label></p>
      <p>or <label>Issuer: <input value={iss} onChange={(e) => setIss(e.target.value)} autoComplete="off" /></label> <label>Subject: <input value={sub} onChange={(e) => setSub(e.target.value)} autoComplete="off" /></label></p>
      <p><label>Reason: <input value={reason} onChange={(e) => setReason(e.target.value)} autoComplete="off" /></label></p>
      <p><label>Type the identity to record it{target !== "" ? ` (${target})` : ""}: <input value={typed} onChange={(e) => type(e.target.value)} autoComplete="off" /></label></p>
      <p>
        <button type="submit" disabled={why !== undefined || busy}>Revoke identity</button>
        {why !== undefined && ` ${why}`}
      </p>
      <Result w={w} done={(d) => revoked(d as components["schemas"]["RevocationBody"])} />
    </form>
  );
}

const revoked = (x: components["schemas"]["RevocationBody"]): ReactNode =>
  `Revoked ${x.identity} (act ${x.act}). auth.deniedSubjects lists it: ${x.deniedSubjectsListed ? "yes" : "no, add it now, since a restore can erase this revocation"}. ${x.note}`;
