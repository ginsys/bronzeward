// What a plan changes, in words: the plan page's "Change" evidence and the apply page's diff of each machine.

import type { components } from "./api/v1";
import { Named } from "./titles.tsx";
import { FORM_ONLY_DIFF_NOTE } from "./words.ts";

export function Change({ p }: { p: components["schemas"]["PlanBody"] }) {
  const d = p.evidence.diff;
  if (p.operation === "adopt") return <p>Sends nothing. Records the machine's current configuration as applied.</p>;
  if (d == null) return <p>No change recorded.</p>;
  if (d.withheld) return <p>The change cannot be shown.</p>;
  const note = d.fromFormOnly ? <p>{FORM_ONLY_DIFF_NOTE}</p> : null;
  if (d.unified === "") return <><p>No differences in the redacted configurations (applied <Named id={d.from} />, release <Named id={d.to} />).</p>{note}</>;
  return <><p>Change shown below: the whole redacted configuration, applied <Named id={d.from} /> → <Named id={d.to} />.</p>{note}<pre className="diff" data-testid="diff" tabIndex={0} aria-label="Change">{d.unified}</pre></>;
}
