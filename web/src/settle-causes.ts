// The server's named refusals of an accounting, each with the one next step a person can take (journeys §5.9,
// ginsys/bronzeward#365). The causes are the server's words (internal/api/settle.go, recovery_accountings.go) and the list is the
// command's (`settleCauses` in cmd/bronzewardctl/recovery_account.go); a Go test reads this file against both, so a renamed cause
// fails there. A refusal carries its cause only in its detail text, so a cause is found by the text its detail holds.

export const SETTLE_CAUSES: readonly { cause: string; next: string }[] = [
  {
    cause: "is not the first successful read of the machine after the transport deadline",
    next: "Choose the first read of the machine after that deadline that read a node value as the start.",
  },
  {
    cause: "is not a recovery observation",
    next: "Choose a recovery observation as the end. Bronzeward takes one while the server runs for recovery.",
  },
  {
    cause: "was not read at both ends of the settle",
    next: "Choose observations that both read the machine-configuration resource version.",
  },
  {
    cause: "did not begin after the start observation was recorded",
    next: "Choose an end observation that began after the start observation was recorded.",
  },
  {
    cause: "began before the settle floor of",
    next: "Wait until the floor has passed since the time named, wait for a recovery observation that begins after it, then choose that observation as the end.",
  },
  {
    cause: "changed during the settle and no explanation is recorded",
    next: "Say in the explanation why the version changed, then record the accounting again.",
  },
  {
    cause: "is already accounted for",
    next: "Nothing to record. The accounting is on the machine's timeline.",
  },
  {
    cause: "executor is not stopped or taken over",
    next: "Stop the executor or take the operation over, then record the accounting again.",
  },
  {
    cause: "not pre-restore unaccounted",
    next: "Nothing to record on this scope. Its state and next step are in its row.",
  },
];

// The next step of a refusal, found by its raw detail; undefined where the detail names no known cause.
export function settleNext(detail: unknown): string | undefined {
  if (typeof detail !== "string") return undefined;
  return SETTLE_CAUSES.find((c) => detail.includes(c.cause))?.next;
}
