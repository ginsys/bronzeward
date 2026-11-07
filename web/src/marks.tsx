// The marks of a protected import review (W5, ginsys/bronzeward#341; client contracts §11.3, §11.4):
// the operator names the values to keep secret by their paths, and the page sends them in one write.
// Marks are protected input (§11.2): a path can spell a secret mapping key, so they are held in this
// page's memory only, emptied when the request is sent, and never named in an outcome. A refusal is
// named by its rule and position (progress.ts, states.tsx), and the outcome of a run follows the
// import page's watch (imports.tsx).

import { useState } from "react";
import { isAlias, isMap, isScalar, isSeq, parseAllDocuments } from "yaml";
import { NO_ASSIST } from "./drafts.tsx";
import { useUnsaved } from "./forms.tsx";
import { short } from "./list.tsx";
import { Action } from "./shell.tsx";
import type { WriteSpec } from "./write.ts";

const enc = encodeURIComponent;
// The server's limit on one request's marks (MarkInput.marks).
export const MAX_MARKS = 1024;
// How many candidate values the review lists; the rest are named by count, and can be typed.
export const MAX_CANDIDATES = 200;

// Each nonempty line is one RFC 6901 path, sent as typed: whitespace is part of a token, and a
// trimmed path would address another node (compilation.md §2.2).
export const markLines = (text: string): string[] => text.split("\n").filter((s) => s !== "");

// Why nothing is sent, or undefined.
export const markRefusal = (marks: string[]): string | undefined =>
  marks.length === 0 ? "Not sent: type at least one path, or choose a value." : marks.length > MAX_MARKS ? `Not sent: at most ${MAX_MARKS} marks go in one request.` : undefined;

// The mark write: the ingestion is the resource to read when its outcome is unknown. The page's copy
// of the marks is dropped when the layer can no longer repeat the write (§11.2 item 6).
export function markSpec(id: string, marks: string[]): WriteSpec {
  const body: Record<string, unknown> = { marks };
  return { method: "POST", path: `/ingestions/${enc(id)}/marks`, resource: `/ingestions/${enc(id)}`, body, released: () => { delete body["marks"]; } };
}

// A path segment, escaped as RFC 6901: "~" is "~0" and "/" is "~1".
const token = (s: string) => s.replace(/~/g, "~0").replace(/\//g, "~1");

// The string values of a review's documents as compilation §2.2 paths: doc[<n>], then a JSON Pointer.
// A value already extracted (a tagged scalar), an alias and a key holding "|" (it cannot be addressed)
// are left out. The `yaml` package only reads the text: nothing is re-encoded, stored or sent.
export function candidates(text: string): { paths: string[]; more: number } {
  const paths: string[] = [];
  let more = 0;
  const add = (p: string) => (paths.length < MAX_CANDIDATES ? paths.push(p) : more++);
  const walk = (node: unknown, at: string): void => {
    if (isAlias(node)) return;
    if (isScalar(node)) {
      if (typeof node.value === "string" && node.value !== "" && !node.tag?.startsWith("!")) add(at);
    } else if (isSeq(node)) {
      node.items.forEach((n, i) => walk(n, `${at}/${i}`));
    } else if (isMap(node)) {
      for (const p of node.items) {
        const k = p.key;
        if (!isScalar(k)) continue;
        const key = k.source ?? String(k.value);
        if (!key.includes("|")) walk(p.value, `${at}/${token(key)}`);
      }
    }
  };
  parseAllDocuments(text).forEach((d, i) => walk(d.contents, `doc[${i}]`));
  return { paths, more };
}

// The typed marks with one more path on its own line, once.
export const withMark = (marks: string, path: string): string =>
  markLines(marks).includes(path) ? marks : `${marks}${marks === "" || marks.endsWith("\n") ? "" : "\n"}${path}\n`;

// What a done send says: the progress watch follows the run and shows how it ended.
export const markSent = (d: unknown): string => {
  const a = (d ?? {}) as { operation?: unknown; generation?: unknown };
  return `The marks were accepted in operation ${short(typeof a.operation === "string" ? a.operation : "")} at generation ${typeof a.generation === "number" ? a.generation : "?"}. How the run ended shows under Progress.`;
};

// The marks panel under an open review: the values to choose from, the typed list and [Send marks].
// The send itself belongs to the claim (claim.tsx), so its notice outlives the review, which closes
// once the claim leaves `paused`. The typed list is emptied when the request is sent.
export function MarksPanel({ text, send, busy }: { text: string; send: (marks: string[]) => void; busy: boolean }) {
  const [marks, setMarks] = useState("");
  const [refusal, setRefusal] = useState<string>();
  useUnsaved("the marks", marks !== "");
  const { paths, more } = candidates(text);
  const go = () => {
    const lines = markLines(marks);
    const why = markRefusal(lines);
    setRefusal(why);
    if (why !== undefined) return;
    setMarks("");
    send(lines);
  };
  return (
    <section aria-label="Marks">
      <h4>Mark as secret</h4>
      <ul data-testid="candidates">
        {paths.map((p) => <li key={p}><code>{p}</code> <Action label="Mark as secret" needs={["author"]} onClick={() => setMarks((m) => withMark(m, p))} /></li>)}
      </ul>
      {more > 0 && <p>{more} more values are not listed. Type their paths below.</p>}
      <p><label>Marks, one path per line:<br /><textarea rows={4} cols={80} value={marks} onChange={(e) => setMarks(e.target.value)} data-testid="marks" {...NO_ASSIST} /></label></p>
      <Action label="Send marks" needs={["author"]} onClick={go} busy={busy} />
      {refusal !== undefined && <p data-testid="not-sent">{refusal}</p>}
    </section>
  );
}
