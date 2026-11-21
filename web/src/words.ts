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
/** The text standing for a machine whose body has not been read yet, in a heading, and for one whose read gave no body, in a title. */
export const THE_MACHINE = "the machine";
/** The text of a cluster's link when its name has not been read, or is not safe to show; no word stands before it. */
export const THIS_CLUSTER = "This cluster";
/** What stands where a fragment's or profile's name is not safe text, so no identifier or rejected text is shown. */
export const NAME_NOT_SHOWN = "(name not shown)";
/** The page that lists drafts and releases (navigation label and heading; /drafts and /releases open it too). */
export const DRAFTS_AND_RELEASES = "Drafts and releases";
/** The page that lists fragments and profiles, which the word "source" no longer names. */
export const FRAGMENTS_AND_PROFILES = "Fragments and profiles";
/** The column of a release that counts what it is built from. */
export const BUILT_FROM = "Built from";
/** The state words of a machine. */
export const CHANGED_ON_THE_MACHINE = "Changed on the machine";
export const NOT_MANAGED_YET = "Not managed yet";
export const LAST_READ_MATCHED = "Last read matched what was applied";
export const RELEASE_NOT_APPLIED_YET = "Release not applied yet";
/** The buttons and headings that record a cluster by hand and add a machine. */
export const ENTER_A_CLUSTER_BY_HAND = "Enter a cluster by hand";
export const ADD_MACHINE = "Add machine";
/** The label, heading and page name for taking an existing cluster in; it replaces "Onboard cluster". */
export const CONNECT_A_CLUSTER = "Connect a cluster";
/** Said beside the Copy control once the identifier is on the clipboard. */
export const COPIED = "Copied";
/** Said beside the Copy control where the browser would not copy. */
export const COPY_BY_HAND = "Select the identifier to copy it";
