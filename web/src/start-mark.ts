// The one start observation the server accepts for a settle (ginsys/bronzeward#365; internal/api/settle.go, `settled`): the first
// observation of the machine, in order of basis, that began strictly after the deadline and read at least one of identity, running
// version and configuration (an unread key that is absent counts as read; a key that is present counts as unread). The server stays
// the judge and its refusal is still shown: this names the start so a person does not choose among observations the server refuses.
// Times are compared at the precision the server sends (see `instant`), as the server compares them.

import type { components } from "./api/v1";

type Observation = components["schemas"]["ObservationBody"];

// A time the server sent, as milliseconds since the epoch with the fraction kept to the digits it sent (the database keeps microseconds;
// Date.parse keeps three digits, which reads two times in one millisecond as equal and turns a valid start off).
export const instant = (t: string): number => {
  const m = /^(.*:\d\d)(?:\.(\d+))?(Z|[+-]\d\d:\d\d)$/.exec(t);
  return m === null ? Date.parse(t) : Date.parse(`${m[1]}${m[3]}`) + Number(`0.${m[2] ?? "0"}`) * 1000;
};

const READ_KEYS = ["identity", "runningVersion", "configuration"] as const;
const read = (o: Observation) => READ_KEYS.some((k) => !(k in (o.unread ?? {})));

export interface StartMarks {
  // The identifier of the one observation the server accepts, or undefined where no observation qualifies yet.
  mark: string | undefined;
  // Why an observation cannot be the start, or undefined where it can.
  why: (o: Observation) => string | undefined;
  // Set where the one start the server accepts is one it refuses: the server does not skip to the next read (settle.go), and an
  // observation's resource version never changes, so no start can be recorded for this settle.
  refused: string | undefined;
}

// `deadline` is the instant the settle counts from, in milliseconds: an attempt's transport deadline, or for a scope the later of
// the entry's stoppedAt plus the maximum transport deadline and the transport deadline of each of the machine's unaccounted attempts.
export function startMarks(observations: Observation[], deadline: number): StartMarks {
  const first = [...observations].sort((a, b) => a.basis - b.basis).find((o) => instant(o.startedAt) > deadline && read(o));
  const why = (o: Observation): string | undefined => {
    if (instant(o.startedAt) <= deadline) return "it began at or before the transport deadline";
    if (!read(o)) return "it read none of identity, running version and configuration";
    if (o.id !== first?.id) return "it is not the first read after the transport deadline";
    return o.resourceVersion == null ? "it read no machine-configuration resource version" : undefined;
  };
  const refused = first !== undefined && first.resourceVersion == null
    ? "the first read after the transport deadline read no machine-configuration resource version, and the server accepts only that read as the start and refuses it without one, so no start can be recorded for this settle."
    : undefined;
  return { mark: first?.id, why, refused };
}

// Where the form's deadline comes from: known (milliseconds), or not known with the reason the page cannot say. Not known leaves the
// choice to the person and the server, as before.
export type Deadline = { at: number } | { unknown: string };
