// A principal as a person or service (client contracts A7, gap G23): its name once `GET /principals`
// answered, and until then, for an identifier the route does not name and when the read failed, the
// shortened identifier, as every other identifier is shown. The title is the identifier, never the name.

import { type ReactNode, useEffect, useSyncExternalStore } from "react";
import { Id, useReads } from "./list.tsx";
import { namesOf, principalId, principalText } from "./principals.ts";

export function Who({ id, none }: { id: string | null | undefined; none?: string }): ReactNode {
  const { client, view, again } = useReads();
  const names = namesOf(client);
  // The page's reads start again with the view and with each read again, a new epoch included, and a
  // restore may change who an identifier names: a name read under another generation is not kept.
  const gen = `${view}|${again}`;
  const ask = id != null && principalId(id);
  useEffect(() => {
    if (ask) names.want(gen, [id]);
  }, [names, gen, id, ask]);
  const snapshot = () => (ask ? names.get(gen, id) : undefined);
  const name = useSyncExternalStore(names.subscribe, snapshot, snapshot);
  const text = principalText(name);
  return text === undefined || id == null ? <Id id={id} {...(none !== undefined && { none })} /> : <span title={id}>{text}</span>;
}
