// The words the console shows for the things a person meets (ginsys/bronzeward#1055). A page imports a word
// from here and never writes it again, so a word Serge answers differently is one edit. Each pull request of that
// work adds only the words it renders; the operation sentences stay in operation-words.ts (client contracts A10).

/** What a draft whose title is not safe text is called; its shortened identifier stands beside it where the view shows no other. */
export const UNTITLED_DRAFT = "Untitled draft";
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
/** What stands where a server's word that is not a name (a kind, class or reason) is not safe text. */
export const NOT_SHOWN = "(not shown)";
/** What follows a server's sentence that is cut at its limit, so the cut is not read as the server's own text. */
export const CUT_SENTENCE = "… (the rest is not shown)";
/** The page that lists drafts and releases (navigation label and heading; /drafts and /releases open it too). */
export const DRAFTS_AND_RELEASES = "Drafts and releases";
/** The page that lists fragments and profiles, which the word "source" no longer names. */
export const FRAGMENTS_AND_PROFILES = "Fragments and profiles";
/** The sentences a page shows under its title: what the page is, what to do here, where to go next. A part is shown only where its control is. */
export const DRAFT_NOTE = "A draft collects changes to one cluster; its entries are below. Click a fragment, profile or assignment entry to open it.";
export const DRAFT_NOTE_OPEN = "While the draft is open you can add a fragment, a profile or an assignment, publish it, or discard it. Publishing builds a release and sends nothing to any machine.";
export const DRAFT_NOTE_START = "To adopt the machines imported into this draft, use Adopt these machines below.";
export const PUBLISH_NOTE = "Publishing builds a release from this draft: it renders the configuration of every machine of the cluster whose imported configuration is in this draft or in the release applied to it, and makes the release the Desired release of those machines.";
export const MACHINE_NOTE = "This page shows where this machine stands: its status, the next step where there is one, what Bronzeward has Desired and Applied for it, what was Observed on it, and its timeline. To change its configuration,";
export const CREATE_A_DRAFT = "create a draft";
export const RELEASE_NOTE = "A release is the finished configuration of the machines of one cluster, built from one draft. The machines it covers are listed below; for each, open its Rendered configuration to read the configuration it would receive.";
export const REVIEW_NOTE = "This is the configuration this machine would receive from this release. A secret is never shown as its value: it shows as a badge, or as redaction text where it sits inside embedded JSON.";
export const PLANS_NOTE = "A plan is one proposed action on one machine: apply a release to it, or adopt it as it is. This lists plans with their state. To make one,";
export const OPEN_A_MACHINE = "open a machine";
export const PLANS_NOTE_END = "and follow its next step.";
export const CREATE_PLAN_NOTE = "Choose what to do with this machine and release: apply-config sends the release to the machine; adopt records what the machine runs and sends nothing to it. Nothing runs until the plan is approved.";
export const PLAN_NOTE = "This plan proposes one action on one machine, with one release. What it does, the evidence for it and its state are below.";
export const PLAN_NOTE_APPROVE = "To approve it, read the plan on this page, then press Approve. You can approve a plan you made yourself; Bronzeward records that you did. Approving does not run the plan: Bronzeward commits it later, after its own checks, and can refuse.";
export const OPERATIONS_NOTE = "An operation is one run of work: publishing a draft, importing a machine's configuration, applying a release to a machine, or adopting a machine. This lists operations with their state; open one to follow its progress.";
export const OPERATION_NOTE = "This is one run of work: what it is doing, its state, its timeline and each attempt it made.";
/** The sentences of the pages off the path from a draft to an operation (ginsys/bronzeward#1055, pull request B). */
export const MACHINES_NOTE = "A machine is one node of a cluster. Open a machine to read its state. To bring in a whole cluster, use Connect a cluster; to add one machine, use Add machine.";
export const ADD_MACHINE_NOTE = "Use this to add one machine to a cluster Bronzeward has recorded. It asks for the cluster, the machine's Talos node ID, its platform and its Talos endpoint, an IP address. To bring in a whole cluster, open Clusters and use Connect a cluster.";
export const CLUSTERS_NOTE = "A cluster is a Talos cluster Bronzeward records. Open one to see its machines and its open drafts. To bring one in by reading its talosconfig, use Connect a cluster; to record one yourself, use Enter a cluster by hand.";
export const BY_HAND_NOTE = "This records an existing cluster by hand. Connect a cluster instead reads a talosconfig to find the cluster and its machines. This form asks for the cluster's name, its Kubernetes endpoint, its Talos contract and its Talos cluster ID.";
export const CLUSTER_NOTE = "This shows the machines of this cluster and the drafts open for it. A draft is a set of changes to this cluster that is not published yet; open one to see its entries.";
export const CHANGES_NOTE = "A draft is a set of changes to one cluster, open until it is published or discarded; releases are what publishing a draft built. Create a draft here, and open one to see its entries. Imports of every machine are on their own page, linked between the two lists.";
export const CREATE_DRAFT_NOTE = "A draft collects changes to one cluster. Choose the cluster and give the draft a title you will recognise.";
export const IMPORTS_NOTE = "An import brings one machine's configuration into a draft, read from the machine or taken from a document. This lists the imports and the draft updates, with their state; the filters narrow it. Start an import with Start import.";
export const START_IMPORT_NOTE = "Choose the machine and the draft that should receive its configuration, and whether to read it from the machine or from a document you paste or choose. Reading from the machine writes nothing to it.";
export const IMPORT_NOTE = "An import reads one machine's configuration into a draft; a draft update changes a draft and has no machine. This shows its state, the machine where there is one, the draft it fills and, once an operation has started for it, that operation's progress.";
export const IMPORTED_NOTE = "This is the configuration imported from one machine, with its size and counts. Its text is read on request, as the lines below say. The configuration a machine would receive is read on its release, under Rendered configuration.";
export const SOURCES_NOTE = "The fragments, profiles and assignments that releases are built from, and the dependencies Bronzeward monitors. None of them is a machine's configuration: that is the release's. A fragment is added with Add fragment on a draft's page, and is listed here once that draft is published.";
export const sourceNote = (kind: string) => `This is one ${kind}, with its revisions below. Open a revision to read that saved state of it.`;
export const revisionNote = (kind: string) => `A revision is one saved state of a ${kind}. This shows it as it was saved.`;
export const DEPENDENCY_NOTE = "A dependency is an outside thing of which Bronzeward records a version, by provider and object. Below: the releases that depend on it and the alerts raised for it.";
export const AUDIT_NOTE = "Each row is an act Bronzeward recorded: who did what, through which route, and to what. Nothing on this page changes them.";
export const DRIFT_ADOPTION_NOTE = "Drift adoption reads the machine's own configuration into a draft, so that you can review what changed on it. Choose the machine and the draft that should receive the configuration. Reading it writes nothing to the machine.";
export const ENTER_RECOVERY_NOTE = "Enter recovery after a restore, to record which backups were restored. Name each restored backup, when it was taken, and when the restore stopped writes. Times are RFC 3339 with a zone.";
export const RECOVERY_NOTE = "Recovery mode is for restoring Bronzeward from a backup. This page shows whether it is on and which scopes are still in recovery. On a machine's row, Mark reads the machine and records what it supports, and Release lets a ready scope act again, one machine at a time. You can also leave recovery mode or revoke an identity.";
export const RECOVERY_ROLE_NOTE = "Marking and releasing a scope, leaving recovery mode and revoking an identity need the recovery-admin role, which you do not have.";
// What each item of a mark's `missing` list says (the command's table, cmd/bronzewardctl/recovery_scope.go).
export const MISSING_MEANING: Readonly<Record<string, string>> = {
  observation: "the restoration observation does not support the scope: it read no identity, does not follow the accounting, an operation changed after it, or it used another access version",
  identity: "the identity the node showed is not the one the machine record binds, or a later observation showed another",
  access: "the cluster's Talos access failed the dependency check",
  artifact: "the Desired release's artifact could not be decrypted for the machine; a lost key version leaves through a newly published release",
};
export const MISSING_UNKNOWN = "the server named it and this page has no text for it";
// The step after a mark, by the state it set.
export const MARK_NEXT: Readonly<Record<string, string>> = {
  ready: "Next: release the scope.",
  unresolved: "A restored operation still holds the scope. It is classified after the normal restart; then mark the scope again.",
  blocked: "Next: fix what is missing, then mark the scope again.",
};
export const RELEASE_NOT_APPROVAL = "A release is not an approval: it approves no plan and sends nothing, and a plan still needs its own approval.";
// The recovery page of one operation (ginsys/bronzeward#365): take over, and resolve as cancelled.
export const RECOVERY_OPERATION_NOTE = "This page decides an operation whose outcome is not known. Take over moves it to the server you are talking to and sends nothing to the node. Resolve as cancelled ends an operation that has no attempt. Both need the recovery-admin role and a reason, which is the record.";
export const RECOVERY_OPERATION_ROLE_NOTE = "You do not have the recovery-admin role, so both controls are off.";
export const TAKE_OVER_TEXT = "Move the operation to this server instance, from the owner generation read from its events and attempts. The server refuses a generation that is not current, and then nothing changed.";
export const RESOLVE_TEXT = "Cancel an operation that has no attempt. This action sends nothing to the node and records no remote outcome. No recorded attempt does not prove that nothing was ever sent: after a restore the journal can lack an attempt, and nothing shows whether anything landed.";
export const RESOLVE_NO_OUTCOME = "This records no remote outcome.";
export const ACCOUNT_TEXT = "Account for an attempt whose response was lost, once its executor is stopped or the operation is taken over. It sends nothing to the node and changes no state: it records that the attempt is accounted for, so the controller or an operator can go on. Choose the two observations the settle runs between.";
export const NOT_FOUND_NOTE = "Start from Machines or Clusters.";
export const CONNECT_NOTE = "Connecting records the cluster and each machine that answers with a platform, and starts an import of each of those machines into one draft. A machine that does not answer, or reports no platform, is skipped. First you choose the talosconfig and name the cluster, then you confirm what the page found.";
/** The column of a release that counts what it is built from. */
export const BUILT_FROM = "Built from";
/** The state words of a machine. */
export const CHANGED_ON_THE_MACHINE = "Changed on the machine";
export const NOT_MANAGED_YET = "Not adopted yet";
export const LAST_READ_MATCHED = "Last read matched what was applied";
export const RELEASE_NOT_APPLIED_YET = "Release not applied yet";
/** The server's formOnly verdict (ginsys/bronzeward#1098): the same documents, keys and values, other comments and encoding. */
export const DIFFERS_IN_FORM_ONLY = "Differs in form only (comments and encoding)";
export const FORM_ONLY_NOTE = "The release has the same documents, keys and values as the configuration Bronzeward adopted from the machine. The first apply sends the release's text, without the comments of the adopted configuration.";
export const FORM_ONLY_DIFF_NOTE = "This apply removes the comments of the configuration Bronzeward adopted: the release's text carries none.";
/** The buttons and headings that record a cluster by hand and add a machine. */
export const ENTER_A_CLUSTER_BY_HAND = "Enter a cluster by hand";
export const ADD_MACHINE = "Add machine";
export const NAME_THIS_MACHINE = "Name this machine";
export const RENAME_MACHINE = "Rename machine";
export const NAME_HINT = "a hostname: lowercase letters, digits and hyphens, with dots between labels";
export const notAName = (typed: string) => `Not sent: "${typed}" is not a name the server takes; use ${NAME_HINT}.`;
// When a rename reaches the machine (client contracts A9 item 6), word for word; the command line prints the same
// sentences. The restart sentence is the "not yet confirmed" form until ginsys/bronzeward#1040's measurement picks one.
export const RENAME_REACHES = [
  "If this machine's hostname comes from its name, the new name reaches the machine only after the next release is published and a plan for it is approved. Until then the machine keeps its current hostname.",
  "Whether the machine needs a restart to take the new name is not yet confirmed.",
  "On the node, the Kubernetes node name normally follows the hostname.",
] as const;
// Where a release's hostname came from (client contracts A9 item 7), word for word; the command line prints the same
// sentences. A release's hostname taken from the configuration is never shown as a value: the release records none.
export const HOSTNAME_BY_SOURCE = {
  configuration: "Hostname: set in the configuration. The machine's name does not change it.",
  none: "Hostname: not set in the configuration; the node chooses its own.",
  "legacy-stable": "Hostname: the configuration holds the legacy stable-hostname setting, so the machine's name is not used. Remove that setting to use the name.",
  unrecorded: "Hostname: not recorded for this release.",
} as const;
export const HOSTNAME_NODE_NAME = "The machine's Kubernetes node name normally follows the hostname, so it may change too.";
export const HOSTNAME_LEFT_TO_NODE = "The last release applied to it left the hostname to the node.";
export const HOSTNAME_UNREAD = "The comparison with the release last applied to this machine could not be read.";
// A9 item 8: where no node-name clash was checked. Neither names a cause or a value.
export const NODE_NOT_CHECKED_HIDDEN = "Not checked: the review hides an input to this machine's Kubernetes node name, so a clash with another machine's node name cannot be checked.";
export const NODE_NOT_CHECKED_OLD = "Not checked: this release was made before Kubernetes node names were recorded, so a clash with another machine's node name cannot be checked.";
// A9 item 8: an approved plan waits for another machine's hostname. It says "waits", never "refused" or "failed": the plan stays approved and nothing was sent.
export const HOST_WAITS_SHORT = "This plan waits for another machine's hostname.";
export const hostWaits = (holder: string, node: string, mine: string) =>
  `This plan waits. The last release applied to machine ${holder} records the Kubernetes node name ${node}, and this release gives the same name to machine ${mine}. ` +
  `This plan has sent nothing to either machine. Apply the release for machine ${holder} first, or give machine ${mine} another hostname.`;
// A9 item 8: the machine timeline's entry for a commitment the hostname gate refused, from its `holder` and `nodeName`.
export const HOST_REFUSAL_BEFORE = "The plan could not start yet: the last release applied to machine ";
export const hostRefusalAfter = (node: string) => ` records the Kubernetes node name ${node}.`;
export const hostRefusal = (holder: string, node: string) => HOST_REFUSAL_BEFORE + holder + hostRefusalAfter(node);
export const hostnameTaken = (name: string) => `Hostname: ${name}, taken from the machine's name.`;
export const hostnameSets = (name: string) => `This release sets the machine's hostname to ${name}, taken from its name.`;
export const hostnameNowFromName = (name: string) => `This release now takes the machine's hostname from its name, ${name}, in place of the setting it had before.`;
export const hostnameLastSet = (name: string) => `The last release applied to it set ${name}.`;
export const hostnameEarlier = (used: string, now: string) => `This release used the earlier name ${used}. While its hostname comes from the machine's name, the next release will use ${now}.`;
export const hostnameUnknownSource = (source: string) => `Hostname source: ${source}`;
/** The label, heading and page name for taking an existing cluster in; it replaces "Onboard cluster". */
export const CONNECT_A_CLUSTER = "Connect a cluster";
/** Said beside the Copy control once the text is on the clipboard. */
export const COPIED = "Copied";
/** Said beside an identifier's Copy control where the browser would not copy. */
export const COPY_BY_HAND = "Select the identifier to copy it";
/** Said beside the Copy control of a page that shows no identifier in its text, where the browser would not copy: the identifier is in the sentence. */
export const copyFailedWith = (id: string) => `Copy did not work here; the identifier is ${id}.`;
/** The two screens of Connect a cluster: each says which step it is and what it asks. The second screen's button differs from the page's own label. */
export const CONNECT_STEP_ONE = "Step 1 of 2: choose the talosconfig and name the cluster";
export const CONNECT_STEP_TWO = "Step 2 of 2: check what was read and connect";
export const CONNECT_THIS_CLUSTER = "Connect this cluster";
/** Under an imported configuration's text: why a worker's machine.ca.key is empty. */
export const CA_KEY_WORKER_NOTE = "The configuration of a worker shows machine.ca.key as an empty value. Talos writes it empty for a worker: only a control-plane machine holds the CA key of the cluster, and that one is shown as a reference.";
/** The empty lists of fragments and profiles say why they are empty and what adds one; the link to the Drafts and releases page stands between the two parts. */
export const noSourcesStart = (kind: "fragment" | "profile") => `No ${kind}s recorded yet. An import records one imported configuration for each machine, not ${kind}s. To add a ${kind}, open a draft on`;
export const noSourcesEnd = (kind: "fragment" | "profile") => `and press Add ${kind}; it is listed here once that draft is published.`;
/** The drift page (wireframe W9): its note, the sentences it says of the change, and the three choices. */
export const DRIFT_NOTE = "This page shows the change made on this machine, where one is open, and is where you freeze or unfreeze the machine. Nothing on this page is sent to the machine.";
export const DRIFT_NOTE_OPEN = "To decide the change, adopt it or revert the machine; the machine can be frozen while you decide. Each step says who may take it. A plan sends nothing until it is approved.";
export const DRIFT_NOT_SHOWN = "What changed is not shown: Bronzeward can show it only by importing the machine's configuration for adoption.";
export const DRIFT_NONE = "No change made on the machine is open. Only a configuration read that differs from what Bronzeward last verified opens one; a read that matches it, or one that could not be read, opens none. There is nothing to adopt or revert.";
export const DRIFT_FREEZE = "Freeze the machine while you decide. It gets no new commitment or attempt until an approver unfreezes it. An attempt already recorded is not stopped, and an adoption can still be recorded. Freezing needs an author, publisher, approver or recovery-admin; unfreezing needs an approver.";
export const DRIFT_ADOPT = "Adopt the change: import the machine's own configuration for adoption (an author), review it, publish it as a release (a publisher), then make and approve an adopt plan (a publisher makes it, an approver approves it). Its compiled configuration may differ from the node's bytes. Starting the import sends nothing to the machine, and this page stops at its review.";
export const DRIFT_REVERT = "Revert the machine to its Desired release: make an apply-config plan (a publisher), approve it (an approver), and unfreeze the machine first if it is frozen (an approver). Approving overwrites a change nobody has reviewed. The plan records which change it was made on.";
/** The machine page's standing link to the drift page: its words follow the machine's state. */
export const DECIDE_ON_THE_CHANGE = "Decide on the change made on the machine";
export const UNFREEZE_OR_DECIDE = "Unfreeze the machine or decide on the change";
export const UNFREEZE_THE_MACHINE = "Unfreeze the machine";
export const FREEZE_THIS_MACHINE = "Freeze this machine";
/** A machine's role in a person's words (ginsys/bronzeward#1096): the API's "controlplane" is two words; a value this client does not know is shown as sent. */
export const roleWords = (role: string): string => (role === "controlplane" ? "control plane" : role);

/** The apply page (ginsys/bronzeward#1096): /releases/<id>/apply. */
export const APPLY_NOTE = "Choose the machines of this release to change, then press Show apply: it makes one plan for each machine and shows what each plan changes. Apply then records your approval of those plans. A machine you leave out stays as it is and can be applied later from this page.";
export const APPLY_SENTENCE = "Pressing Apply records your approval of each plan shown above. The controller re-checks each plan when it sends it and applies it only if every check passes, one machine of a cluster at a time, in an order the controller does not promise. A plan that is not applied expires at the time shown for it. Approval is for a human sign-in only.";
export const CONTROL_PLANE_NOTE = "A control plane machine is chosen. Applying to a control plane machine was not covered by this version's acceptance tests. The server does not refuse it, machines are applied one at a time, and you decide.";
export const NO_ROLE_NOTE = "A machine imported before its role was stored shows none until it is imported again.";
/** Shared fragments and profiles (ginsys/bronzeward#1193): the draft page's reach block and the shared page. */
export const SHARED_REACH_NOTE = "A shared entry reaches no other cluster by itself: each cluster takes it in its own draft and reads it in its own plan. This shows what the draft's entries change for the machines of this cluster, and which other clusters stay as they are. It renders no configuration.";
export const SHARED_PAGE_NOTE = "A change to a shared fragment or profile reaches no cluster by itself. This page changes no cluster: a cluster takes a revision when it adopts it in its own draft. A cluster marked behind is on an older revision than the shared head.";
/** The apply page's shared block (ginsys/bronzeward#1193): what the release moved, here, and the other clusters as information. */
export const APPLY_SHARED_NOTE = "This release moved the shared names below for this cluster. Other clusters are named for information only: nothing here chooses, plans or approves anything for them, because one apply is for one cluster. Each cluster takes a shared change in its own draft and reads it in its own plan.";
export const APPLY_SHARED_UNKNOWN = "Some machines could not be read, so the list may be short.";
export const APPLY_SHARED_UNREAD = "Which machines it reaches could not be read.";
export const APPLY_SHARED_NONE_BEHIND = "No other cluster is behind it.";
/** Shared names on the draft page (ginsys/bronzeward#1193): adopting one and stopping to follow it, in this draft only. */
export const SHARED_NAMES_NOTE = "A shared fragment or profile is followed by the clusters that adopt it. Adopting or stopping to follow records an entry in this draft: nothing changes for this cluster until the draft is published, and no other cluster changes.";
export const adoptSentence = (kind: string, name: string) => `Adopt records in this draft that this cluster follows the shared ${kind} ${name} at its current revision.`;
export const stopSentence = (kind: string, name: string) => `Stop following records in this draft that this cluster no longer follows the shared ${kind} ${name}. The cluster loses the name unless it holds its own.`;
/** An entry staged before this cluster's adoption changed (another draft changed it first): publication would refuse it, and pressing again records the current one. */
export const AGAIN_ADOPT = "The cluster's adoption may have changed since this was staged: Adopt again to record the current one.";
export const AGAIN_STOP = "The cluster's adoption changed since this was staged: Stop following again to record the current one.";
/** The shared choice of the fragment and profile editors: what Save and Remove record for a shared entry, in this draft only. */
export const SHARED_CHOICE = "Shared (library)";
export const SHARED_SAVE_SENTENCE = "Save records a shared revision in this draft. Publishing the draft moves this cluster to it; no other cluster changes.";
export const SHARED_REMOVE_SENTENCE = "Remove records, in this draft, that the shared name leaves the library. Clusters that adopted it keep their revision.";
export const SHARED_PROFILE_PINS = "A shared profile pins shared fragment revisions: the ones this draft saves as shared first, else each shared fragment's current revision.";
export const ADOPT_PIN_NOTE = "A profile of this cluster that pins the name keeps its pin: set it again to use the new revision.";
export const APPLY_NEEDS_PUBLISHER = "Show apply makes the plans, which needs the publisher role. A publisher can prepare them and an approver can then apply them from this page.";
