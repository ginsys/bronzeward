// The words the console shows for the things a person meets (ginsys/bronzeward#1055). A page imports a word
// from here and never writes it again, so a word Serge answers differently is one edit. Each pull request of that
// work adds only the words it renders; the operation sentences stay in operation-words.ts (client contracts A10).

/** The heading of a machine with no name and no address to show. */
export const UNNAMED_MACHINE_HEADING = "Unnamed machine";
/** The text standing for a machine with no name and no address, where a title or a link names it. */
export const UNNAMED_MACHINE = "an unnamed machine";
/** The word before a machine-made identifier on a page's detail line. */
export const IDENTIFIER = "Identifier";
/** The control that copies an identifier. */
export const COPY = "Copy";
/** The text standing for a machine whose body has not been read yet, in a heading. */
export const THE_MACHINE = "the machine";
/** The text of a cluster's link when its name has not been read, or is not safe to show; no word stands before it. */
export const THIS_CLUSTER = "This cluster";
/** Said beside the Copy control once the identifier is on the clipboard. */
export const COPIED = "Copied";
/** Said beside the Copy control where the browser would not copy. */
export const COPY_BY_HAND = "Select the identifier to copy it";
