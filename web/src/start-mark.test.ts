// The one start the server accepts (ginsys/bronzeward#365): the first observation of the machine, by basis, that began after the
// deadline and read at least one of identity, running version and configuration (internal/api/settle.go, settled). The table is the
// server's query written out: strict "after", ordering by basis, an unread key that is absent counts as read and one that is present
// counts as unread whatever its value. Every value is synthetic.

import { describe, expect, test } from "vitest";
import type { components } from "./api/v1";
import { startMarks } from "./start-mark.ts";

type Observation = components["schemas"]["ObservationBody"];

const T0 = Date.parse("2026-11-01T10:00:00Z");
const at = (s: number) => new Date(T0 + s * 1000).toISOString();
const obs = (id: string, basis: number, startedAt: string, unread: Record<string, unknown> = {}, resourceVersion: string | null = "7"): Observation =>
  ({
    id, machine: "mch_k2pq3rstuvwxyz234567abcdef", basis, revision: basis + 1, purpose: "recovery", publicationGeneration: null, plan: null,
    operation: null, endpoint: "10.5.0.2:50000", controller: "ctl_a", startedAt, at: startedAt, access: null, smbiosUuid: null,
    talosNodeId: "node-a", talosClusterId: null, assignmentEvidence: null, runningVersion: null, resourceVersion, health: null, unread,
  }) as unknown as Observation;
const ALL_UNREAD = { identity: { cause: "x" }, runningVersion: { cause: "x" }, configuration: { cause: "x" } };
const deadline = T0 + 60 * 1000;

describe("the start the server accepts", () => {
  test("the first observation after the deadline that read something is the mark, and no other is offered a reason of its own kind", () => {
    const list = [obs("a", 1, at(10)), obs("b", 2, at(70)), obs("c", 3, at(80))];
    const m = startMarks(list, deadline);
    expect(m.mark).toBe("b");
    expect(m.why(list[0]!)).toBe("it began at or before the transport deadline");
    expect(m.why(list[1]!)).toBeUndefined();
    expect(m.why(list[2]!)).toBe("it is not the first read after the transport deadline");
  });

  test("an observation that began exactly at the deadline is not after it", () => {
    const list = [obs("a", 1, at(60)), obs("b", 2, at(61))];
    const m = startMarks(list, deadline);
    expect(m.mark).toBe("b");
    expect(m.why(list[0]!)).toBe("it began at or before the transport deadline");
  });

  test("an observation that read none of identity, running version and configuration is not a read", () => {
    const list = [obs("a", 1, at(70), ALL_UNREAD), obs("b", 2, at(80))];
    const m = startMarks(list, deadline);
    expect(m.mark).toBe("b");
    expect(m.why(list[0]!)).toBe("it read none of identity, running version and configuration");
  });

  test("one of the three read is enough, and an empty unread object reads all three", () => {
    const partial = obs("a", 1, at(70), { identity: { cause: "x" }, runningVersion: { cause: "x" } });
    expect(startMarks([partial], deadline).mark).toBe("a");
    expect(startMarks([obs("a", 1, at(70), {})], deadline).mark).toBe("a");
  });

  test("a key that is present counts as unread whatever its value, as the server's key test does", () => {
    const list = [obs("a", 1, at(70), { identity: null, runningVersion: null, configuration: null }), obs("b", 2, at(80))];
    expect(startMarks(list, deadline).mark).toBe("b");
  });

  test("no observation after the deadline: no mark, and every observation says it began at or before the deadline", () => {
    const list = [obs("a", 1, at(10)), obs("b", 2, at(60))];
    const m = startMarks(list, deadline);
    expect(m.mark).toBeUndefined();
    expect(list.map((o) => m.why(o))).toEqual(["it began at or before the transport deadline", "it began at or before the transport deadline"]);
  });

  test("the order is the basis, whatever order the list is served in", () => {
    const list = [obs("late", 5, at(90)), obs("first", 2, at(70)), obs("early", 1, at(10))];
    const m = startMarks(list, deadline);
    expect(m.mark).toBe("first");
    expect(m.why(list[0]!)).toBe("it is not the first read after the transport deadline");
  });

  test("the mark that read no resource version stays the mark and is off with that reason, as the server refuses it later", () => {
    const list = [obs("a", 1, at(70), {}, null), obs("b", 2, at(80))];
    const m = startMarks(list, deadline);
    expect(m.mark).toBe("a");
    expect(m.why(list[0]!)).toBe("it read no machine-configuration resource version");
    expect(m.why(list[1]!)).toBe("it is not the first read after the transport deadline");
  });
});
