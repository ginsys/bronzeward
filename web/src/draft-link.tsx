// A draft's link in a list that names the draft by its title (ginsys/bronzeward#1055), apart from titles.tsx so the
// machines page can use it without importing a module that imports machines.tsx.

import type { components } from "./api/v1";
import { Ref } from "./list.tsx";
import { draftName } from "./titles.ts";

// A draft in a list that has its identifier in a column of its own: its name as the link, "Untitled draft" alone.
export const DraftLink = ({ d }: { d: components["schemas"]["DraftBody"] }) => <Ref to={`/drafts/${encodeURIComponent(d.id)}`} id={d.id}>{draftName(d)}</Ref>;
