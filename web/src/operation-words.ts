// What an operation did and what comes next, in words (client contracts A10): one sentence for each kind
// and state the server can produce (persistence and API 8.2, execution and recovery 3), the next step
// an operator can take from it (operator experience 2.3), and who started it. A kind or state this table
// does not know is shown as the server wrote it, never as a blank. The outcome facts a state carries (its
// cause, its attempts, the applied release) stay with `Outcome`, so a sentence here adds what the state
// means for the machine and never repeats them.

const KNOWN: Record<string, Record<string, [now: string, next: string | undefined]>> = {
  publish: {
    queued: ["Accepted; no worker has taken it yet.", "Wait; nothing to do."],
    running: ["A worker is compiling the draft and writing the release.", "Wait; nothing to do."],
    succeeded: [
      "The draft became an immutable release. Nothing was sent to any machine.",
      "Create a plan for each machine (publisher): an adopt plan where it has no Applied configuration, which sends nothing; an apply plan otherwise, which sends the release.",
    ],
    failed: ["The draft was not published; no release was made.", "Fix the cause named above and publish again; a publish is safe to repeat."],
  },
  ingest: {
    running: [
      "The import is reading the machine's configuration into the draft, waits for review, or has stalled with its runner gone.",
      "Open the import to see which; an author reviews a paused one and takes a stalled one over or abandons it.",
    ],
    succeeded: ["The machine's configuration is stored in the draft. Nothing was written to the machine.", "Publish the draft (publisher)."],
    failed: ["No configuration was stored from the machine, and nothing was written to it.", "Start the import again (author)."],
  },
  "apply-config": {
    committed: ["The controller committed the plan and holds the machine; no request is sent yet.", "Wait for the controller."],
    sending: ["A request is in flight, or its outcome is unknown.", "Wait for the controller."],
    verifying: ["The node accepted the request; the controller is checking the result.", "Wait for the controller."],
    completed: ["The machine's Applied configuration is updated.", "None needed."],
    rejected: ["The machine was not changed.", "Correct the release and create a new plan."],
    failed: ["The machine may have been changed wrongly or not changed at all; its Applied configuration was not updated.", "Read the machine's drift and observations, then create a new plan."],
    // No attempt is recorded; after a restore that is no proof that nothing was sent (execution and recovery 3).
    cancelled: ["No attempt is recorded for it, which is not proof that nothing was sent.", "Read the machine's Observed configuration, then create a new plan if the change is still wanted."],
    // The next step of an unresolved operation is the Recovery text `Outcome` carries.
    unresolved: ["The controller keeps examining it and may settle it on its own.", undefined],
  },
  adopt: {
    completed: [
      "The machine's running configuration is recorded as Applied, by observation. Nothing was sent.",
      "None needed; the first apply plan afterwards carries a whole-configuration diff, or says it is withheld.",
    ],
  },
};

const known = (kind: string, state: string) => (Object.hasOwn(KNOWN, kind) && Object.hasOwn(KNOWN[kind]!, state) ? KNOWN[kind]![state] : undefined);

const article = (kind: string) => (/^[aeiou]/.test(kind) ? "An" : "A");

export function operationNow(kind: string, state: string): string {
  const row = known(kind, state);
  if (row !== undefined) return row[0];
  const what = Object.hasOwn(KNOWN, kind) ? `${article(kind)} ${kind} operation` : `An operation of kind ${kind}`;
  return `${what} in state ${state}; this page has no description of it.`;
}

export function operationNext(kind: string, state: string): string | undefined {
  return known(kind, state)?.[1];
}

type Principal = { principal: string; role: string };
export type Starter =
  | { person: Principal }
  | { controller: string; plan?: string; creator?: Principal }
  | { none: true };

// The starter of an operation: the principal that made the request, or for an apply or an adoption, which the
// controller creates itself at the commitment of an approved plan (persistence and API 8), the controller,
// the plan, and the person that created that plan where its read gave one.
export function startedBy(kind: string, createdBy: Principal | null, plan: { plan: string; creator: Principal | undefined } | undefined): Starter {
  if (createdBy !== null) return { person: createdBy };
  if (kind !== "apply-config" && kind !== "adopt") return { none: true };
  const controller = kind === "adopt" ? "the controller, adopting under plan" : "the controller, committing plan";
  if (plan === undefined) return { controller: "the controller" };
  return plan.creator === undefined ? { controller, plan: plan.plan } : { controller, plan: plan.plan, creator: plan.creator };
}
