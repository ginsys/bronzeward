// The deadline a scope's settle counts from (ginsys/bronzeward#365): the later of the entry's stoppedAt plus the maximum transport
// deadline and the transport deadline of each of the machine's attempts that no accounting covers (internal/api/recovery_accountings.go,
// accountScope). The operations list carries no attempts, so each apply-config operation of the machine is read on its own. No state
// narrows them: an attempt whose response did not account stays unaccounted in any operation state (internal/api/outcome.go, the
// response's Accounts), so above a fixed count the page reads nothing and leaves the choice to the server. A read that fails, or
// reads less than all, gives no deadline: a missed operation could hide the later one.

import { useEffect, useState } from "react";
import type { components } from "./api/v1";
import { useReads } from "./list.tsx";
import type { Deadline } from "./start-mark.ts";

type Operation = components["schemas"]["OperationBody"];

export const MAX_OPERATIONS = 8;

// `again` reads once more each time it changes. Undefined until the first answer, or while the entry or the settings are not read.
export function useScopeDeadline(machine: string, stoppedAt: string | undefined, maxSeconds: number | undefined, again: number): Deadline | undefined {
  const { client, settle, undropped } = useReads();
  const [deadline, setDeadline] = useState<Deadline | undefined>();
  useEffect(() => {
    if (stoppedAt === undefined || maxSeconds === undefined) return;
    let live = true;
    const unchanged = undropped();
    const get = async <T,>(path: string): Promise<T | undefined> => {
      const res = await client.read<T>(path);
      settle(res, () => {}, () => live && unchanged(), () => false);
      return res.kind === "ok" ? res.data : undefined;
    };
    void (async () => {
      const enc = encodeURIComponent;
      const unknown = (why: string) => live && setDeadline({ unknown: why });
      const list = await get<{ items: Operation[]; next?: string }>(`/operations?machine=${enc(machine)}&kind=apply-config&limit=${MAX_OPERATIONS + 1}`);
      if (list === undefined) return unknown("the machine's operations could not be read.");
      if (list.items.length > MAX_OPERATIONS || list.next !== undefined) return unknown(`the machine has more than ${MAX_OPERATIONS} apply operations, too many to read here.`);
      let at = Date.parse(stoppedAt) + maxSeconds * 1000;
      for (const op of list.items) {
        const full = await get<Operation>(`/operations/${enc(op.id)}`);
        if (full === undefined) return unknown("an operation of the machine could not be read.");
        for (const a of full.attempts ?? []) if (a.accounting === null) at = Math.max(at, Date.parse(a.transportDeadline));
      }
      if (live && unchanged()) setDeadline({ at });
    })();
    return () => { live = false; };
  }, [machine, stoppedAt, maxSeconds, again]);
  return deadline;
}
