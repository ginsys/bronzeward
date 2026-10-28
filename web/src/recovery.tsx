// The shell's recovery parts (journeys §5.9, §8; client contracts §8.5): the start mode, the
// recovery banner with its scope summary and process restriction, and the state before a recovery
// start's entry.

import type { ReadResult } from "./request.ts";
import { clock, Disconnected, ProblemFields, problemFields, Refused, Status } from "./states.tsx";
import type { components } from "./api/v1";

export type Recovery = components["schemas"]["RecoveryBody"];
type Scope = components["schemas"]["RecoveryScope"];

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
  </>
);
