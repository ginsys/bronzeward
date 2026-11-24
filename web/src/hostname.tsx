// What a release review says of a machine's hostname (client contracts A9 item 7): the sentence of its source and,
// for a name taken from the machine's name, how it differs from the release last applied to the machine and from
// the machine's current name; and, where no node-name clash was checked, the notice of item 8. The sentences are in
// words.ts, and a test compares each with the contract.

import type { components } from "./api/v1";
import { safeWord } from "./safe.ts";
import {
  HOSTNAME_BY_SOURCE, HOSTNAME_LEFT_TO_NODE, HOSTNAME_NODE_NAME, HOSTNAME_UNREAD, NODE_NOT_CHECKED_HIDDEN, NODE_NOT_CHECKED_OLD, RENAME_REACHES,
  hostnameEarlier, hostnameLastSet, hostnameNowFromName, hostnameSets, hostnameTaken, hostnameUnknownSource,
} from "./words.ts";

type Hostname = components["schemas"]["HostnameBody"];

// What the release last applied to the machine recorded for its hostname: still being read, none (the machine has no
// applied release), known, or not readable. A comparison is never guessed.
export type Applied = { state: "pending" } | { state: "none" } | { state: "unread" } | { state: "known"; hostname: Hostname };

// The lines of the review's hostname (item 7) and, last, the one notice of item 8 where no node-name clash was
// checked. A source this client does not know gets none: it can only come from a server past the revision that
// records the node name, and for a null the exemption below cannot be decided without knowing the source.
export function hostnameLines(h: Hostname | undefined, applied: Applied, current: string | null | undefined): string[] {
  const lines = sourceLines(h, applied, current);
  const notice = h === undefined || lines.length === 0 ? undefined : nodeNotice(h);
  return notice === undefined ? lines : [...lines, notice];
}

function nodeNotice(h: Hostname): string | undefined {
  const source: string = h.source;
  if (!(source === "machine-name" || source === "configuration" || source === "none" || source === "legacy-stable" || source === "unrecorded")) return undefined;
  if (h.nodeName === undefined) return NODE_NOT_CHECKED_OLD;
  // A release with a none or legacy-stable source and no node name already says so in its item 7 sentence.
  if (h.nodeName === null && source !== "none" && source !== "legacy-stable") return NODE_NOT_CHECKED_HIDDEN;
  return undefined;
}

function sourceLines(h: Hostname | undefined, applied: Applied, current: string | null | undefined): string[] {
  if (h === undefined) return [];
  const source: string = h.source;
  if (source === "configuration" || source === "none" || source === "legacy-stable" || source === "unrecorded") return [HOSTNAME_BY_SOURCE[source]];
  if (source !== "machine-name") return [hostnameUnknownSource(safeWord(source))];
  if (h.name === undefined) return [];
  const name = safeWord(h.name);
  const lines = [hostnameTaken(name)];
  const restart = RENAME_REACHES[1];
  if (applied.state === "unread") return [...lines, HOSTNAME_UNREAD];
  if (applied.state === "known") {
    const was = applied.hostname;
    if (was.source === "none" || was.source === "legacy-stable") lines.push(`${hostnameSets(name)} ${HOSTNAME_LEFT_TO_NODE} ${HOSTNAME_NODE_NAME}`, restart);
    else if (was.source === "unrecorded") lines.push(hostnameSets(name), restart);
    else if (was.source === "configuration") lines.push(hostnameNowFromName(name), HOSTNAME_NODE_NAME, restart);
    else if (was.source === "machine-name" && was.name !== undefined && was.name !== h.name) {
      lines.push(`${hostnameSets(name)} ${hostnameLastSet(safeWord(was.name))}`, HOSTNAME_NODE_NAME, restart);
    }
  }
  if (typeof current === "string" && current !== h.name) lines.push(hostnameEarlier(name, safeWord(current)));
  return lines;
}

export function HostnameLines({ hostname, applied, current }: { hostname: Hostname | undefined; applied: Applied; current: string | null | undefined }) {
  const lines = hostnameLines(hostname, applied, current);
  if (lines.length === 0) return null;
  return <div className="hostname" data-testid="hostname"><h3>Hostname</h3>{lines.map((l) => <p key={l}>{l}</p>)}</div>;
}
