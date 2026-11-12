// The rendered review of one machine of a release (ginsys/bronzeward#342; journeys §5.5, W6, T6, T7).
// It reads GET /releases/{id}/machines/{m}/review and shows the redacted configuration with every
// redaction token as a badge whose kind is spelled out, a withheld review as its own state, the
// provenance records exactly as served, and what the machine needs next (B18). It sends no write.

import { Fragment as Group, type ReactNode } from "react";
import type { components } from "./api/v1";
import { Head, Id, Ref, Scroll, short, useItem, usePending, useReads, viewOf } from "./list.tsx";
import { planPath } from "./plan.tsx";
import { Action } from "./shell.tsx";
import { Withheld } from "./states.tsx";

type S = components["schemas"];
type Review = S["ReviewBody"];
type Machine = S["MachineBody"];
type Prov = S["ProvenanceRecord"];

const enc = encodeURIComponent;

// A rendered review's path, or undefined when either identifier is not of its kind (a release `rel_…`, a machine
// `mch_…`). That also refuses a dot-only identifier, which a browser would read as a path step. Each identifier
// is one encoded segment, so no identifier can change the path.
export function reviewPath(release: string, machine: string): string | undefined {
  if (viewOf(release) !== "releases" || viewOf(machine) !== "machines") return undefined;
  return `/releases/${enc(release)}/machines/${enc(machine)}`;
}

// The release and the machine a path names, decoded; undefined when it is no review path, a segment is not valid
// encoding, or an identifier is not of its kind.
export function reviewIds(path: string): [release: string, machine: string] | undefined {
  const m = /^\/releases\/([^/]+)\/machines\/([^/]+)$/.exec(path);
  if (m === null) return undefined;
  try {
    const ids: [string, string] = [decodeURIComponent(m[1]!), decodeURIComponent(m[2]!)];
    return reviewPath(...ids) === undefined ? undefined : ids;
  } catch {
    return undefined;
  }
}

// compilation §8.3: `<redacted:REF@VERSION>`, `<redacted:REF@VERSION#N>`, `<redacted:schema>`, `<redacted:value>`,
// `<redacted:paired>` and `<redacted>`. A name is segments of lowercase letters, digits and hyphens joined by `/`
// (compilation §5.1), so text outside that grammar stays text. A token inside embedded JSON has its angle brackets
// written as Unicode escapes (§8.3); the page cannot tell that from a YAML value that spells the same characters,
// so it shows the escaped form as the text it is and never as a badge.
const SEGMENT = "[a-z0-9](?:[a-z0-9-]*[a-z0-9])?";
const TOKEN = new RegExp(`<redacted(?::(?:(schema|value|paired)|(${SEGMENT}(?:/${SEGMENT})*)@([1-9][0-9]*)(?:#([0-9]+))?))?>`, "g");

export type Part = { text: string } | { token: string; label: string };

// What a token says in words, so no reader takes it for a literal value.
export function tokenLabel(ref: string | undefined, version: string | undefined, member: string | undefined): string {
  if (ref === undefined) return "redacted";
  if (version !== undefined) return `secret ${ref}, version ${version}${member === undefined ? "" : `, member ${member}`}`;
  if (ref === "schema") return "secret, marked by the pinned machinery";
  if (ref === "value") return "secret, a copy of a resolved value";
  if (ref === "paired") return "redacted, paired with a secret";
  return "redacted";
}

// The configuration split into its text and its tokens, in order. Concatenating the text and the tokens
// gives the configuration back.
export function parts(configuration: string): Part[] {
  const out: Part[] = [];
  let at = 0;
  for (const m of configuration.matchAll(TOKEN)) {
    const i = m.index as number;
    if (i > at) out.push({ text: configuration.slice(at, i) });
    out.push({ token: m[0], label: tokenLabel(m[1] ?? m[2], m[3], m[4]) });
    at = i + m[0].length;
  }
  if (at < configuration.length) out.push({ text: configuration.slice(at) });
  return out;
}

// What the machine needs next for this release (B18), from its Desired, Applied and pendingConvergence.
export function nextStep(release: string, m: Pick<Machine, "desired" | "applied" | "pendingConvergence">): string {
  if (m.desired !== release) {
    return `This release is not the Desired release of this machine${m.desired === null ? "" : ` (Desired is ${short(m.desired)})`}, so it needs no step for this release.`;
  }
  if (m.applied === null) return "This machine has no Applied configuration. Hand it over first (an adopt plan): an apply-config plan is refused until then. Nothing has been sent to it.";
  // The server's pendingConvergence says whether the node's configuration differs from this release's (operator-experience §2.4).
  // After an adoption Applied can equal Desired while it does; after a publication Applied can name another release while it does not.
  const same = m.applied.release === release;
  if (m.pendingConvergence) {
    return `${same ? "This release is Desired and Applied on this machine, but its configuration is pending convergence: it differs from what Bronzeward applied." : `This machine has an Applied configuration (release ${short(m.applied.release)}) that differs from this release.`} An apply-config plan sends this release to it. Nothing has been sent yet.`;
  }
  return same
    ? "This release is the one Applied on this machine. Nothing is left to send."
    : `This release carries this machine's Applied configuration (release ${short(m.applied.release)}) unchanged. Nothing is left to send.`;
}

// The plan this machine's state offers for this release (B18): a handover for a machine with no Applied, an
// apply-config plan while the configuration differs. None where the text says nothing is left to send.
export function planFor(release: string, m: Pick<Machine, "desired" | "applied" | "pendingConvergence">): "adopt" | "apply-config" | undefined {
  if (m.desired !== release) return undefined;
  if (m.applied === null) return "adopt";
  return m.pendingConvergence ? "apply-config" : undefined;
}

export const EMPTY_NOTE = "A field that shows as empty is empty in the rendered review. A redacted field shows as a badge, so an empty one was not redacted.";
export const PROVENANCE_LIMIT = "Provenance covers secret references only; other fields are not traced.";
export const NO_PROVENANCE = "No secret reference was rendered into this configuration.";

// Text with each redaction token as a badge, so a path with a redacted segment reads as the document does.
function Marked({ text }: { text: string }) {
  return <>{parts(text).map((p, i) => ("text" in p ? <Group key={i}>{p.text}</Group> : <span key={i} className="badge" title={p.token}>[{p.label}]</span>))}</>;
}

const outcome = (r: Prov): ReactNode =>
  r.overriddenBy == null ? (r.output == null ? "none" : <Marked text={r.output} />) : <>overridden by <Id id={r.overriddenBy.revision} /></>;

const COLS = ["Reference", "Version", "Source revision", "Path in the source revision", "Output path or overriding revision"] as const;
const keyOf = (r: Prov) => `${r.reference}@${r.version}:${r.member ?? ""}:${r.source.revision}:${r.source.occurrence}`;

function Provenance({ records }: { records: readonly Prov[] }) {
  return (
    <>
      <h3>Provenance (secret references only)</h3>
      {records.length === 0 ? <p>{NO_PROVENANCE}</p> : (
        <Scroll label="Provenance" testid="provenance">
          <Head cols={COLS} />
          <tbody>
            {records.map((r) => (
              <tr key={keyOf(r)}>
                <th scope="row">{r.reference}{r.member == null ? "" : ` (member ${r.member})`}</th>
                <td>{r.version}</td>
                <td><Id id={r.source.revision} /></td>
                <td><Marked text={r.source.path} /></td>
                <td>{outcome(r)}</td>
              </tr>
            ))}
          </tbody>
        </Scroll>
      )}
      <p>{PROVENANCE_LIMIT}</p>
    </>
  );
}

export function Document({ configuration }: { configuration: string }) {
  return (
    <section aria-label="Rendered configuration, redacted">
      <pre data-testid="document">
        <Marked text={configuration} />
      </pre>
    </section>
  );
}

// What the review says, from a body as the server serves it.
export function ReviewBody({ r }: { r: Review }) {
  return (
    <>
      <dl>
        <dt>Release</dt><dd><Id id={r.release} /></dd>
        <dt>Machine</dt><dd><Id id={r.machine} /></dd>
        <dt>Validated in mode</dt><dd>{r.mode}</dd>
      </dl>
      {r.configuration === null ? <Withheld /> : <><Document configuration={r.configuration} /><p>{EMPTY_NOTE}</p></>}
      <Provenance records={r.provenance} />
    </>
  );
}

// [Plan adopt (handover)] or [Plan apply]: opens the creation page for this release and machine. It sends nothing.
function PlanButton({ release, machine, m }: { release: string; machine: string; m: Machine }) {
  const { go } = useReads();
  const op = planFor(release, m);
  if (op === undefined) return null;
  return <p><Action label={op === "adopt" ? "Plan adopt (handover)" : "Plan apply"} needs={["publisher"]} onClick={() => go(planPath(release, machine, op))} /></p>;
}

export function ReviewItem({ release, machine }: { release: string; machine: string }) {
  const review = useItem<Review>(`/releases/${enc(release)}/machines/${enc(machine)}/review`);
  const pending = usePending(review, "the rendered review");
  const mach = useItem<Machine>(`/machines/${enc(machine)}`);
  const machinePending = usePending(mach, "the machine");
  return (
    <>
      <h2>Rendered configuration of {short(machine)} in release {short(release)}</h2>
      <p><Ref to={`/releases/${enc(release)}`} id={release}>Back to the release</Ref></p>
      {review.kind === "ok" ? <ReviewBody r={review.data} /> : pending}
      <h3>Next step</h3>
      {mach.kind === "ok" ? <p data-testid="next-step">{nextStep(release, mach.data)}</p> : machinePending}
      {mach.kind === "ok" && <PlanButton release={release} machine={machine} m={mach.data} />}
    </>
  );
}
