// What the draft editor's first case (auth/drafts.e2e.ts, "Create draft, then a fragment saved") adds to a failed read
// that the name field of a new entry is gone from the pane after the save (ginsys/bronzeward#1121, group B). The page
// renders that field only while the pane has no entry and Remove only while it has one (web/src/drafts.tsx), so a read
// that sees the field after Remove was seen means the pane went back, or the read matched something else. The page's
// own record of what it showed, and when, says which. Functions of their input alone, so that pane-trace.test.ts runs
// them without a browser. Booleans, counts, milliseconds and the first words of a label: no document text, no name.

// What the pane showed at one moment, read in the page the way the case's own reads read it. `field` is what
// `label*=name` finds in the first Fragment section, `which` the first words of the label that matched, `same` whether
// that section is the element the case first saw, and `sections` how many Fragment sections the page holds.
export interface Sample {
  ms: number;
  field: boolean;
  which: string;
  remove: boolean;
  listed: boolean;
  sections: number;
  same: boolean;
  note?: string;
}

const SHOWN = 12;
const yn = (b: boolean) => (b ? "yes" : "no");
const key = (s: Sample) => JSON.stringify([s.field, s.which, s.remove, s.listed, s.sections, s.same]);
const state = (s: Sample) =>
  `${s.ms} ms name field ${yn(s.field)}${s.field ? ` (label "${s.which}")` : ""}, Remove ${yn(s.remove)}, entry listed ${yn(s.listed)}, ` +
  `${s.sections} Fragment section${s.sections === 1 ? "" : "s"}, ${s.same ? "same" : "replaced"} pane element`;

// The samples as lines: a state where it differs from the one before, and each note on a line of its own. Only the last
// SHOWN lines are kept, since the failure is at the end, with a count of the ones left out.
export function paneTraceText(samples: readonly Sample[]): string {
  if (samples.length === 0) return "no sample taken";
  const lines: string[] = [];
  let shown: string | undefined;
  for (const s of samples) {
    if (shown !== key(s)) {
      shown = key(s);
      lines.push(state(s));
    }
    if (s.note !== undefined) lines.push(`${s.ms} ms ${s.note}`);
  }
  const left = lines.length - SHOWN;
  return [...(left > 0 ? [`${left} earlier lines left out`] : []), ...lines.slice(Math.max(0, left))].join("; ");
}

// The failure with the trace after it, read now. A read that fails leaves the trace out, and the original failure is the
// message's start and the `cause` either way: a read made to explain a failure must never be the failure.
export async function failedWithPane(error: unknown, read: () => Promise<readonly Sample[]>): Promise<Error> {
  const text = error instanceof Error ? error.message : String(error);
  let trace: string | undefined;
  try {
    trace = paneTraceText(await read());
  } catch {
    trace = undefined;
  }
  return new Error(trace === undefined ? text : `${text}; pane trace: ${trace}`, { cause: error });
}
