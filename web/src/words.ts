// The words the console shows for the things a person meets (ginsys/bronzeward#1055). A page imports a word
// from here and never writes it again, so a word Serge answers differently is one edit. Each pull request of that
// work adds only the words it renders; the operation sentences stay in operation-words.ts (client contracts A10).

/** The heading of a machine with no name and no address to show. */
export const UNNAMED_MACHINE_HEADING = "Unnamed machine";
/** The text standing for a machine with no name and no address, where a title or a link names it. */
export const UNNAMED_MACHINE = "an unnamed machine";
/** The word before a machine-made identifier on a page's detail line. */
export const IDENTIFIER = "Identifier";
/** The control that copies an identifier or a reference. */
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
/** The sentences a page shows under its title: what the page is, what to do here, where to go next. A part is shown only where its control is. */
export const DRAFT_NOTE = "A draft collects changes to one cluster; its entries are below. Click a fragment, profile or assignment entry to open it.";
export const DRAFT_NOTE_OPEN = "While the draft is open you can add a fragment, a profile or an assignment, publish it, or discard it. Publishing builds a release and sends nothing to any machine.";
export const DRAFT_NOTE_START = "To bring the machines imported into this draft under management, use Start managing these machines below.";
export const PUBLISH_NOTE = "Publishing builds a release from this draft: it renders the configuration of every machine of the cluster that has an imported configuration, and makes the release the Desired release of those machines.";
export const MACHINE_NOTE = "This page shows where this machine stands: its status, the next step where there is one, what Bronzeward has Desired and Applied for it, what was Observed on it, and its timeline. To change its configuration,";
export const CREATE_A_DRAFT = "create a draft";
export const RELEASE_NOTE = "A release is the finished configuration of every machine of the cluster that has an imported configuration, built from one draft. For each machine below, open its Rendered configuration to read the configuration it would receive.";
export const REVIEW_NOTE = "This is the configuration this machine would receive from this release. A secret is never shown as its value: it shows as a badge, or as redaction text where it sits inside embedded JSON.";
export const PLANS_NOTE = "A plan is one proposed action on one machine: apply a release to it, or adopt it as it is. This lists plans with their state. To make one,";
export const OPEN_A_MACHINE = "open a machine";
export const PLANS_NOTE_END = "and follow its next step.";
export const CREATE_PLAN_NOTE = "Choose what to do with this machine and release: apply-config sends the release to the machine; adopt hands the machine over and sends nothing to it. Nothing runs until the plan is approved.";
export const PLAN_NOTE = "This plan proposes one action on one machine, with one release. What it does, the evidence for it and its state are below.";
export const PLAN_NOTE_APPROVE = "To approve it, type the plan ID, as the form shows it, into its field, then press Approve. You can approve a plan you made yourself; Bronzeward records that you did. Approving does not run the plan: Bronzeward commits it later, after its own checks, and can refuse.";
export const OPERATIONS_NOTE = "An operation is one run of work: publishing a draft, importing a machine's configuration, applying a release to a machine, or adopting a machine. This lists operations with their state; open one to follow its progress.";
export const OPERATION_NOTE = "This is one run of work: what it is doing, its state, its timeline and each attempt it made.";
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
/** Said beside the Copy control once the text is on the clipboard. */
export const COPIED = "Copied";
/** Said beside an identifier's Copy control where the browser would not copy. */
export const COPY_BY_HAND = "Select the identifier to copy it";
