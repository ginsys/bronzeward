// The sentences a page shows under its title (ginsys/bronzeward#1055): the parts given are joined in one paragraph, and a
// part that is false or undefined is left out, so a note says only what the page shows in its state. A link goes in as children.

import type { ReactNode } from "react";

export function PageNote({ parts, children }: { parts: readonly (string | false | undefined)[]; children?: ReactNode }) {
  return <p data-testid="page-note">{parts.filter((p): p is string => typeof p === "string").join(" ")}{children}</p>;
}
