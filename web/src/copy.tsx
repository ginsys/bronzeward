// One control that copies a text and says whether it did. The browser may have no clipboard (a page on
// plain http) or may refuse the write: either says so, in the sentence the caller gives for its own text.

import { useState } from "react";

export const COPY = "Copy";
export const COPIED = "Copied.";

export function CopyButton({ text, byHand }: { text: string; byHand: string }) {
  const [said, say] = useState<string>();
  const copy = () => {
    const clipboard = typeof navigator === "undefined" ? undefined : navigator.clipboard;
    if (clipboard === undefined) return say(byHand);
    clipboard.writeText(text).then(() => say(COPIED), () => say(byHand));
  };
  return (
    <>
      <button type="button" onClick={copy}>{COPY}</button>
      {said !== undefined && <span role="status"> {said}</span>}
    </>
  );
}
