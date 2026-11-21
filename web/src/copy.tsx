// One control that copies a text and says whether it did. The browser may have no clipboard (a page on
// plain http) or may refuse the write: either says so, in the sentence the caller gives for its own text.

import { useState } from "react";

export const COPY = "Copy";
export const COPIED = "Copied.";

export function CopyButton({ text, byHand }: { text: string; byHand: string }) {
  // What was said belongs to the text it was said about: a reference that changes under the page (an automatic
  // retry) leaves no "Copied." for text the clipboard does not hold, and a slow copy of the old text cannot say it.
  const [said, say] = useState<{ of: string; line: string }>();
  const copy = () => {
    const clipboard = typeof navigator === "undefined" ? undefined : navigator.clipboard;
    if (clipboard === undefined) return say({ of: text, line: byHand });
    clipboard.writeText(text).then(() => say({ of: text, line: COPIED }), () => say({ of: text, line: byHand }));
  };
  return (
    <>
      <button type="button" onClick={copy}>{COPY}</button>
      {said?.of === text && <span role="status"> {said.line}</span>}
    </>
  );
}
