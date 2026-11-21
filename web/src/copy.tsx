// One control that copies a text and says whether it did. The browser may have no clipboard (a page on
// plain http) or may refuse the write: either says so, in the sentence the caller gives for its own text.

import { useState } from "react";

export const COPY = "Copy";
export const COPIED = "Copied.";

// What the control knows: the text the clipboard last took, and the text whose copy last failed. It says "Copied."
// only for the text the clipboard holds, and the by-hand sentence only for a text whose copy failed, so a
// reference that changes under the page (an automatic retry), or a slow copy of an older text that ends after a
// newer one, never leaves a line about text the clipboard does not hold or hides the line of the text it does.
interface Known { held?: string; failed?: string }

export function CopyButton({ text, byHand }: { text: string; byHand: string }) {
  const [known, learn] = useState<Known>({});
  const copy = () => {
    const failed = () => learn((was) => ({ ...was, failed: text }));
    const clipboard = typeof navigator === "undefined" ? undefined : navigator.clipboard;
    if (clipboard === undefined) return failed();
    clipboard.writeText(text).then(() => learn((was) => (was.failed === text ? { held: text } : { ...was, held: text })), failed);
  };
  const line = known.held === text ? COPIED : known.failed === text ? byHand : undefined;
  return (
    <>
      <button type="button" onClick={copy}>{COPY}</button>
      {line !== undefined && <span role="status"> {line}</span>}
    </>
  );
}
