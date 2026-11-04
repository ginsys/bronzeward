# Operator experience: web wireframes

**Status: accepted for implementation** with the
[operator experience journeys](operator-experience.md) (see their status). Low-fidelity
wireframes for the web journeys in that document. They fix content, order and which
action is available to which role. They do not fix layout, styling, framework or wording beyond
the labels that carry meaning. All data is synthetic; identifiers are shortened as `mch_tqhc…35uq`.
Other people appear by readable names (`ops-ben`). The API serves only principal identifiers;
[gap G15](operator-experience.md#11-api-gap-audit) adds a human's subject and issuer, and a
readable name for a human depends on pending owner choice BW-335-display-1
([persistence and API §10](persistence-api.md#10-authentication-and-authorization)). Without
it, a human is shown by subject and issuer.

Conventions: `[Button]` is an action; `[Button]~` is shown disabled with its reason;
`(•)` is a status badge whose text carries the meaning, never its colour alone; `▸` opens a
detail. Every screen has the [header](#header-on-every-screen) and the state variants of
[W11](#w11-empty-withheld-error-and-offline-states).

## Header on every screen

```text
+------------------------------------------------------------------------------------------+
| Bronzeward   Context: lab ▾   ops-anna@example.invalid (human)   Roles: author, publisher |
|              Epoch ep_bqek…ekgi                                   [Sign out]              |
+------------------------------------------------------------------------------------------+
| RECOVERY MODE since 10:42, epoch ep_r2l…. 4 of 4 scopes not released: none can commit.   |  <- only in recovery mode
| Drafts editable. Recovery-start server: no plans; publications wait for restart. [Open]  |
+------------------------------------------------------------------------------------------+
| Machines | Changes | Plans | Operations | Sources | Audit | Recovery                      |
+------------------------------------------------------------------------------------------+
```

The header shows the identity, the roles and the epoch that `GET /identity` serves
([gap G1](operator-experience.md#11-api-gap-audit)); an action the roles do not allow is disabled,
and the server's refusal stays authoritative. A read of `GET /identity` that fails after a good one
(a refusal other than the two below, or a `503` after its retries) keeps the header
and the page, so the page keeps its unsaved text ([client contracts §11.4](client-contracts.md#114-web-review-and-document-input)),
and shows the failure above the page. A refusal that ends the session (unauthenticated, revoked)
still ends the page. The process-level `409 recovery-mode-active` refusal without `scope` also
replaces the page and its unsaved text: the page shows the state before the recovery entry. The recovery banner states the
scope summary and, while the server runs as started for recovery, that plan creation, approval
and unfreeze wait for its normal restart and that accepted publications complete only then;
imports complete on it. The client reads that start mode from `GET /server`
([gap G14](operator-experience.md#11-api-gap-audit)). Before a recovery start's entry that read
is refused with a `409 recovery-mode-active` without a `scope` member, which itself shows a
recovery start. Until the read answers, the client also learns the mode from answers: plan creation, approval and unfreeze are refused on a recovery
start without a `scope` member, and recovery entry on a normal one with a `409 conflict` that
carries neither `epoch` nor `request`; the entry's other `409 conflict` answers carry `epoch`
and show a recovery start. The line reflects the last such answer and is
corrected by the next one. It never disables actions recovery mode allows
([§5.9](operator-experience.md#59-recovery-after-a-restore)): an action is disabled only on a
scope whose state refuses it, with that reason.

The read-only lists sit under the navigation as follows. Changes lists the releases; the drafts
list joins it with the draft editor. Sources lists fragments, profiles, assignments, the monitored
dependencies with the last completed monitor pass, and the dependency alerts. Audit lists the
acts. Each resource opens at its own path: `/fragments/{id}` with its revisions, `/fragment-revisions/{id}`
and likewise for profiles and assignments, `/releases/{id}`, `/plans/{id}`, `/operations/{id}`
and `/dependencies/{id}` with its releases and alerts. The plan list filters by state and
machine and the operation list by plan, machine, state and kind, in the query; a machine's
detail links to its plans and operations, and a plan to its operations.

## W1. Sign-in and context

```text
+---------------------------------------------+
| Sign in to Bronzeward                        |
|                                              |
| Installation   lab  (bronzeward.lab.exa…)  ▾ |
|                                              |
| [Sign in with the identity provider]         |   <- flow decided in #253
|                                              |
| Signed out: your session ended at 10:15.     |   <- after expiry or sign-out
+---------------------------------------------+
```

Refusal variants, each saying only what the server's answer establishes: `401` ("Not accepted
by this installation. Sign in again."), whatever the cause, since the server does not say
whether issuer, audience, expiry or signature failed; `403 identity-revoked` ("Your access was
revoked or is denied by this installation; contact a recovery administrator."); `503` ("Authentication could not reach the identity provider or the database; nothing was
committed"); and, from
`GET /identity` (gap G1), no roles ("You are signed in but hold no Bronzeward role"). A local
reading, such as "your token expired at 10:15 by this browser's clock", is labelled as the
client's own.

The signed-out page, `/signed-out`, where the provider's end-session returns the browser, shows
"You are signed out of Bronzeward." and a link to sign in. It reads nothing, holds no data and
starts no sign-in by itself ([client contracts §9.7](client-contracts.md#97-sign-out-and-cache-clearing)
item 2).

## W2. Machines

```text
Machines                          Cluster: all ▾   State: all ▾   [Record machine] [Add cluster]  (author, human)
+------------+----------------+-------------+--------------------------+--------------------------+-------------------------+
| Machine    | Endpoint       | Desired     | Applied (source)         | Observed                 | State                   |
+------------+----------------+-------------+--------------------------+--------------------------+-------------------------+
| mch_k2p…7x | 10.5.0.2:50000 | rel_fgqv…   | rel_fgqv… (operation)    | = applied, 2 min ago     | (•) Matches applied     |
| mch_tqhc…35| 10.5.0.3:50000 | rel_fgqv…   | rel_p4nx… (adoption)     | = applied, 1 min ago     | (•) Pending convergence |
| mch_w4ze…n3| 10.5.0.4:50000 | rel_fgqv…   | rel_fgqv… (operation)    | no match, 5 min ago      | (•) Drifted (•) Frozen  |
| mch_b3c…aa | 10.5.0.5:50000 | none        | none                     | —                        | (•) Not handed over     |
+------------+----------------+-------------+--------------------------+--------------------------+-------------------------+
The server filters by cluster; the browser filters by state over all pages (gap G9). Showing 4 of 4. Health is not checked.
```

All four rows are at one moment: just after `mch_b3c…aa2q` was recorded
([§5.2](operator-experience.md#52-inventory)), before its import and handover
([§5.3](operator-experience.md#53-protected-import-with-review)), so it has no `Desired` and no
`Applied` yet.

[Add cluster] opens the form of `cluster add`
([journeys §10](operator-experience.md#10-action-route-and-role-matrix)). (2026-11-01, lead
ruling: #338 found no wireframe placing Add cluster; Machines is the hub and already reads
`GET /clusters`.)

## W3. Machine detail

```text
Machine mch_w4ze…n3fa   cluster cl_9fd… (lab-a)   10.5.0.4:50000   node 9Zs…=   metal
+-------------------------------------------------------------------------------------------+
| (•) Drifted since 09:41: a read differed from what Bronzeward last verified (rel_fgqv…).  |
|     Opened by obs_k3d…, last seen 09:46. Late landing: no.         [Decide] -> W9         |
| (•) Frozen: no new commitment or attempt until an approver unfreezes. An attempt already  |
|     recorded is not stopped; an adoption can still be recorded.                           |
+-------------------------------------------------------------------------------------------+
| Desired    rel_fgqv…j6m  ▸ rendered configuration                                         |
| Applied    rel_fgqv…j6m  verified by operation op_d5s… at 08:56                           |
| Observed   read at 09:46 (obs_m1q…): matches neither Applied nor Desired                  |
|            Latest read 09:51 (obs_p8w…): configuration not read, node unreachable         |
|            Health: not checked (none bound in the PoC)                                    |
| Scope      normal                                                                         |
+-------------------------------------------------------------------------------------------+
| [Freeze]~ already frozen    [Unfreeze] (approver)         [Plan revert]  (publisher)      |
+-------------------------------------------------------------------------------------------+
| Timeline (newest first)                                            [Load older]           |
|  09:51  observation   drift    obs_p8w…   configuration not read: node unreachable        |
|  09:46  observation   drift    obs_m1q…   configuration differs from applied              |
|  09:44  freeze        ops-anna (author)  "incident 4711"                                  |
|  09:41  observation   drift    obs_k3d…   drift opened                                    |
|  08:56  operation     apply-config completed  op_d5s…  ▸                                  |
| Observations ▸   Plans ▸   Releases covering this machine ▸                               |
+-------------------------------------------------------------------------------------------+
```

`Observed` is the highest-basis observation that read the configuration digest, which need not
be the newest one; the newest read and its unread causes come from the observations list.
Observation rows with unread parts show each field with its cause ("configDigest (node
unreachable)"), because the API gives several fields the same cause, and never a healthy badge.
The `Observed` row keeps its attribution under an open drift too, where the sentences speak of the
drift. The web client shows each time with its date, since the page covers the machine's whole
history. "Drifted" comes from the open drift record, which compares
with `Applied`; an observation equal to `Desired` or an older release is still drift. With no
`Applied`, `Desired` or observation the row says so ("Not applied or adopted by Bronzeward",
"No release selected", "No configuration read recorded") and shows no time. The status
sentences are those of [§2.4](operator-experience.md#24-desired-applied-and-observed).

The timeline opens on its newest page and polls on from its tail, as
[client contracts §8.6](client-contracts.md#86-event-resumption) item 6 has it, and a poll with
new entries reads the machine and its observations again, one such read at a time. "Load older"
reads the page before the oldest entry shown and is gone once the machine's first entry is shown.

## W4. Draft editor

```text
Changes › Draft drf_8kdq…z6pa "worker labels"   open, revision 6   cluster cl_9fd… (lab-a)
+------------------------------+------------------------------------------------------------+
| Entries                      | Fragment worker-labels   layer: role ▾                      |
|  import base  mch_b3c… ibr_… |  changed: base frv_1ab… -> unsaved                           |
|  fragment worker-labels  *   | +--------------------------------------------------------+ |
|  fragment cluster-base       | | machine:                                               | |
|  profile  workers            | |   nodeLabels:                                          | |
|  assignment mch_b3c…         | |     team: storage                                      | |
| [Add fragment] [Add profile] | |   registries: …                                        | |
|                              | |       password: !bwref registry/example-pass  [ref]    | |
|                              | +--------------------------------------------------------+ |
|                              | References in this document                                |
|                              |  registry/example-pass   string   version 1   declared     |
|                              | [Save]  [Revert to base]                                   |
+------------------------------+------------------------------------------------------------+
| Not rendered yet. A machine's configuration exists only after publication (gap G5).       |
| [Publish] (publisher)        [Discard draft] (author)                                     |
+-------------------------------------------------------------------------------------------+
```

Profile entry: an ordered list of fragment revisions with [Move up], [Move down] and [Remove from
list] buttons per row, operable by keyboard. After a move, the focus stays on the moved row. The
pane says "A profile pins these fragment revisions. A later save of a fragment does not change the
pin." [Add profile] opens a new profile, and [Add to profile] adds the revision chosen in the
list. That list offers the draft's own revision of each fragment, and for a fragment the draft
holds no entry for, its head revision in the cluster. A pin that is no longer one of these shows
"pin is not current", and a Save that sends it gets a `422` that names its path, never a
revision. A profile pins 1 to 256 revisions, each once. [Save] and [Remove] follow the same `412`
rules as the fragment entry.

Assignment entry: the machine, the profile names in order, and the fragment names of each of the
six layers in order, from global to override. Each list has the same [Move up], [Move down] and
[Remove from list] buttons as the profile entry. [Add assignment] opens a new assignment and offers
the machines of the draft's cluster that the draft holds no assignment for, each as its short ID
and Talos endpoint. [Add to profiles] adds a profile that the draft holds or that the cluster
has a head revision for. The picker of a layer offers only the fragments of that layer. An
assignment selects 1 to 256 names in all, each profile once and each fragment once. A name that is
no longer available shows "not available in this draft", and a Save that sends it gets a `422`
that names its path. [Show composition and references] reads on demand, one read at a time, and
counts the reads. It lists the composition order and the state of each reference of each
fragment: declared, carried and not declared, or declared and not in the document. It shows names,
kinds and versions, never a document or a value. [Save] and [Remove] follow the same `412` rules
as the fragment entry.

On a `412`
the editor keeps the unsaved text, shows "Someone changed this draft (revision 7). Your text is
not saved." and offers [Show their change] and [Copy my text into the new revision], never an
automatic overwrite. [Copy my text into the new revision], [Save] and [Remove] wait until
[Show their change] put their text in the editor. If the entry has a new revision, only the read
of that revision puts their text there. If their change removed the entry, their text is empty.
A second `412` after their text shows keeps the editor's text at that moment as the operator's
text. If [Show their change] is clicked again, [Copy my text into the new revision] waits until the
draft read that it starts answers. After [Remove], the conflict keeps the operator's text for [Copy
my text into the new revision].

A draft read can show another author's change to the entry without a `412`. Examples are the read
after [Show their change] on a Discard's `412`, and the read after a Save that ended while the
operator left the draft and opened it again. Their text, or the removal, then goes into the
editor in the same way. Text typed since the last read or save is kept for [Copy my text into the
new revision], and is never saved over their change unseen. A read that arrives before the
pane's own Save or Remove has its answer can show that write's own change. Such a read opens no
conflict and keeps the editor's text. If another author changed the entry before the write, that
write gets a `412`. If the write's last answer is neither done nor a `412`, the change that read
showed goes into the editor as theirs, and the typed text is kept for [Copy my text into the new
revision]. A done Save does the same, unless that read shows the revision that the Save created.
The answer to the Save names that revision. If the answer or the read names no revision, the read
stays the Save's own. A done Remove shows any text that such a read named, because a Remove
creates no text revision. While the pane's own Remove has no answer, a read of a revision that the
pane has not read before counts as another author's change, also when its text equals the saved
text. A read again of the revision that the pane already shows does not. When nothing was typed
since the last read or save, a read of another author's text shows at once. A done write keeps
that text as the entry's text, with no conflict and no unsaved text. Text typed after that read is
kept for [Copy my text into the new revision]. An unknown outcome is not the last answer while
[Repeat exactly] can still send the write.

The same rule applies after their text shows. If a later read shows another author's newer text,
or their removal, the editor shows that change. Text typed since their text showed is kept for
[Copy my text into the new revision]. The pane's own Remove keeps its text, and a read of the
pane's own Save opens nothing. A removal that such a read shows while the pane's own Save or
Remove has no answer yet is held back in the same way. If that write fails, the editor shows the
removal. A done Save shows it too. A done Remove shows a newer text of theirs, read while it had
no answer, and keeps the operator's text for [Copy my text into the new revision].

[Add fragment] refuses a name that the draft already holds, a removed entry included, and points
to that entry in the list. Once the new fragment is saved, the pane edits the entry that it
created.

Until the pane read the revision that the draft shown names, the pane shows "Reading the entry's
text.", takes no edit and sends nothing. This also applies when a later draft read names a newer
revision, the revision of the pane's own Save included. If that read fails, the pane offers [Read the entry again]. A draft read again also reads
it again. A read that began before a write finished counts as failed, because it may name the revision from before that write. While a `412` waits for [Show their change], a failed revision read keeps it waiting. After [Remove], the draft stores the removal: the text still in the editor is
unsaved, and leaving or switching entries asks first.

[Sign out] asks first while an editor holds unsaved text. When the installation's epoch changes,
a page that holds unsaved text stays shown while the shell reads the server and the identity
again. It also stays if one of those reads gets no answer or a server error, with that failure
shown above it. Its actions stay disabled until the operator opens another page, so it sends
nothing. The operator can copy the text out, then open the page again.

After a Save or Remove is done, the draft page shown at that moment reads the draft again. Until that read shows, [Save] and
[Remove] send nothing, also in an entry the operator opened again meanwhile. If that read fails,
the page offers [Read the draft again], which keeps the editor's text.

[Revert to base] puts the base revision's text in the editor, not saved. Save then writes it as
a new revision of the entry, and the entry stays in the draft. (2026-11-01, lead ruling: no route
drops a draft entry; Revert to base restores the base text for saving, and the saved entry is a
new revision whose content equals the base). After a `412`, [Revert to base] waits until
[Show their change] put their text in the editor: until then the editor holds the operator's text
that the conflict keeps.

A `412` on [Discard draft] shows "Not discarded: someone changed this draft since this page read
it." and offers [Show their change]. While an editor holds unsaved text, that button reads nothing
and asks the operator to save the text or switch entries first.

The editor lists a reference whose name is quoted, or stands on the line after its `!bwref` tag.
The editor also lists a name that follows one anchor (`!bwref &e x/foo`), but only where the scalar
clearly ends; for any other shape with an anchor it lists nothing, and the server's check decides.
A stream that starts with a byte order mark lists no value after an anchor.
The cases are listed in the comment above `anchoredContinues` in `web/src/position.ts`.
It also lists a name in a block scalar on one content line, if strip chomping (`|-` or `>-`) drops
the line break after it or that line ends the document. A double-quoted name that continues after
an escaped line break is listed too. A CRLF, a carriage return alone, and U+0085, U+2028 and
U+2029 each count as one line break, as the server reads them. A block scalar is not listed where
the server refuses the document: content that is not indented past its key (also after a sequence
dash), or a blank line with a tab in or after the scalar. A block scalar with an indentation
indicator is not listed either. The server still checks that reference. A key that follows the tag
is not its name.

The owner decided (BW-606-tag-handles-1, 4 November 2026) that the editor reads a tag the way the
server's parser does. This is option A of the
[tag handle record](../design/research/20261104-tag-handle-resolution.md). The editor resolves the
tag through the `%TAG` directives of its document, and the directives end with that document. It
lists a reference for every spelling that resolves to `!bwref`: the plain form, the verbatim form
`!<!bwref>`, a handle prefix that holds part of the name, and a percent escape. A tag that resolves
to another tag, such as `!!bwref`, is not a reference. Option B, to accept the gap, closes nothing.
Option C, the verbatim form alone, leaves the prefix split and the percent escape open, which is how
the six separate fixes of #598 went.

The editor accepts `%YAML 1.1` only, as the server's parser does. A document lists nothing when the
parser refuses it. The parser refuses a directive line it cannot read, a `%YAML` line or a `%TAG`
handle given twice, a directive line or a `---` or `...` marker inside an open flow collection, and
directives with no `---` after them. A directive line in the first column after block content ends
the document and opens the directives of the next one. The parser also refuses content after `...`
that has no `---`, which includes text on the `...` line itself, a handle that the document does not declare, a bad percent escape, and a tag that
a character outside the tag characters ends, such as `!bwref}`. The parser then refuses the whole
stream. The editor blanks only the document with the fault, so the other documents of the stream
still list. A line that starts with `%` inside a quoted scalar or a block scalar is text, not a
directive.

If the operator edited the marks after a refusal,
[Go to field] finds the refused line where it is now. If that line is gone, it focuses the marks field and selects nothing.

## W5. Protected import review

```text
+-------------------------------------------------------------------------------------------+
| PROTECTED REVIEW  Import ing_6fd…q2la  machine mch_b3c…  paused, generation 1             |
| This may contain secrets nothing has recognised yet. This page stores none of it; screen  |
| capture and copying by hand are outside its control.                                      |
| Review expires 14:52 (in 38 min).                                                         |
+-------------------------------------------------------------------------------------------+
| doc[0]                                                                                    |
|   machine.install.disk: /dev/sda                                                          |
|   machine.files[0].content: "token = BWSYNTH-example"           [Mark as secret]          |
|   machine.ca.key: !bwref example/ca-key (extracted)                                       |
+-------------------------------------------------------------------------------------------+
| Marked in this session: doc[0]/machine/files/0/content                                    |
| [Send marks]   [Continue import]   [Abandon import]                                       |
+-------------------------------------------------------------------------------------------+
```

No copy, download, print styling or browser history entry for this view; it is fetched on
demand and cleared on leaving the page. A refused mark says "Mark 1 was refused: rule
`mark-kind`" and never echoes the path. "Continue" ends with "The draft changed after this
import started; the import was not applied and is abandoned" when the draft moved, and offers
[Import again] against the draft's current revision; the claim is already abandoned.

## W6. Release and rendered review

```text
Release rel_h7rt…2bd   cluster cl_9fd… (lab-a)   published 10:02 by ops-ben (publisher)
Renderer: contract v1.13 · machinery v1.13.6 · Kubernetes v1.34.1
+-------------------------------------------------------------------------------------------+
| Source revisions this release was built from (these are not configuration)               |
|   fragment    cluster-base    frv_5cz…h1ma  ▸                                             |
|   fragment    worker-labels   frv_3jn…k4ma  ▸                                             |
|   fragment    worker-sysctls  frv_8wq…c3ra  ▸                                             |
|   profile     workers         prv_7ce…m2qa  ▸                                             |
|   assignment  mch_b3c…        asr_5qp…d7fa  ▸   (+3 more, one per covered machine)        |
+-------------------------------------------------------------------------------------------+
| Rendered configuration per machine (4 covered)                                            |
|   mch_b3c…aa2q  validated: metal  [Open rendered configuration]  [Plan apply] (publisher) |
|   mch_k2p…7xqa · mch_tqhc…35uq · mch_w4ze…n3fa   (same actions per row)                   |
+-------------------------------------------------------------------------------------------+

Rendered configuration: mch_b3c…aa2q in rel_h7rt…2bd (redacted)
  machine:
    registries: … auth:
      username: ci-pull
      password: [secret registry/example-pass, version 1]      <- badge for <redacted:registry/example-pass@1>
    nodeLabels:
      team: storage
  Provenance (secret references only)
    registry/example-pass v1  from frv_3jn…k4ma doc[0]/…/password  -> doc[0]/…/password
```

Withheld variant: the rendered panel reads only "Configuration not shown: it could not be
redacted." with no document area. It is never an empty editor.

For a machine with no `Applied`, such as one just imported from an existing cluster, the row
offers [Plan adopt (handover)] instead of [Plan apply]: an `apply-config` plan is refused until
the machine is handed over
([§5.3](operator-experience.md#53-protected-import-with-review)).

## W7. Plan and approval

Shown signed in as ops-ben (publisher, approver), who created the plan and published the
release, as in [§5.6](operator-experience.md#56-plan-and-approval).

```text
Plan pln_f645…cawy   (•) Proposed — not approved, nothing will run   expires in 23 h 59 min
+-------------------------------------------------------------------------------------------+
| Target      machine mch_b3c…aa2q · 10.5.0.5:50000 · cluster cl_9fd… (lab-a)               |
|             route 10.5.0.5:50000                                                          |
| Operation   apply-config, mode no-reboot                                                  |
| Release     rel_h7rt…2bd (the machine's Desired)                                          |
| Bound to    assignment asr_5qp…d7fa · baseline revision 4 · drift record none · epoch …   |
| Limits      transport 1 min · verification 10 min · observation age 5 min · attempts 3   |
| Created by  ops-ben (publisher)                                                           |
+-------------------------------------------------------------------------------------------+
| Evidence                                                                                  |
|  Validation  passed at publication (contract v1.13)                                       |
|  Dry run     not available (the PoC has none)                                             |
|  Change      whole redacted configuration, applied rel_9vbn… -> rel_h7rt…                 |
|              machine.nodeLabels.team: (absent) -> storage                                 |
+-------------------------------------------------------------------------------------------+
| (•) Self-approval, predicted: you created this plan and published this release, and you   |
|     authored no fragment, profile or assignment revision in it. Import-base authorship    |
|     and automation you own are not predicted. The approval records the actual reasons.    |
|     Allowed and marked.                                                                   |
| To approve, type the plan ID: [______________]   [Approve]  (approver, human)             |
| [Cancel plan] (creator publisher, approver, recovery-admin)                               |
+-------------------------------------------------------------------------------------------+
```

Variants: diff withheld ("The change cannot be shown"); adopt plan shows "Sends nothing.
Records the machine's current configuration as applied" and the `pendingConvergence` result;
revert shows "(•) Overwrites the drifted configuration, which no reviewer has seen"; approved
shows the approval with its recorded self-approval reasons,
[Revoke approval] and "Revoking after the controller commits does not stop an attempt already
recorded"; expired, cancelled and revoked are terminal badges with their reason.

## W8. Operation timeline

```text
Operation op_q8r…t2na   apply-config   (•) Unresolved — outcome unknown
machine mch_b3c… · plan pln_f645… · attempt 1 of 3
+-------------------------------------------------------------------------------------------+
| 10:06:02  committed     controller ctl-b, generation 3                                    |
| 10:06:03  sending       route 10.5.0.5:50000                                              |
| 10:07:03  no response before the transport deadline                                       |
| 10:07:03  unresolved    A recovery administrator must account for attempt 1.              |
|                         No conflicting work on this machine proceeds until then.          |
+-------------------------------------------------------------------------------------------+
| Live: connected (resumes after a drop)   [Open in Recovery] (recovery-admin)              |
+-------------------------------------------------------------------------------------------+
```

There is no retry button. A stream reset after a restore replaces the list with "The
installation was restored; events replayed from the start".

## W9. Drift decision

```text
Drift on mch_w4ze…n3fa   opened 09:41   (•) Frozen
What changed is not shown: Bronzeward can show it only by importing it (gap G7).
+-----------------------------+-------------------------------+-----------------------------+
| Freeze                      | Adopt                         | Revert                      |
| Stop new commitments and    | Record the node's bytes as    | Put Desired rel_fgqv… back. |
| attempts while you decide.  | Applied for a newly published | Overwrites the change.      |
| Does not stop an attempt    | release. Its compiled bytes   | 1 Plan apply-config         |
| already recorded.           | may differ: the adopt plan    |   (publisher)               |
| (•) already frozen          | reports pendingConvergence.   | 2 Approve  (approver)       |
|                             | 1 Import it for adoption,     | 3 Unfreeze (approver)       |
|                             |   review and mark  (author)   |                             |
|                             | 2 Publish  (publisher)        |                             |
|                             | 3 Plan adopt, approve         |                             |
|                             | Sends nothing to the machine. |                             |
| [Unfreeze] (approver)       | [Start adoption]              | [Plan revert]               |
+-----------------------------+-------------------------------+-----------------------------+
```

## W10. Recovery console

```text
Recovery   (•) Recovery mode on since 10:42   epoch ep_r2l…   entry by ops-carl
Restored: database backup taken 10:20; stopped at 10:31
Server started for recovery (as GET /server reports; G14): plan creation, approval and
unfreeze wait for its normal restart; publications it accepts complete only after that
restart. Imports complete on it.
+----------------+--------------------------+----------------------------------------------+
| Machine        | Scope state              | Next action                                  |
+----------------+--------------------------+----------------------------------------------+
| mch_k2p…7xqa   | pre-restore-unaccounted  | [Account scope]                              |
| mch_tqhc…35uq  | ready                    | [Release scope]  (not an approval)           |
| mch_w4ze…n3fa  | ready                    | [Release scope]  (not an approval)           |
| mch_b3c…aa2q   | unresolved               | op_q8r… held: classified after the normal    |
|                |                          | restart, then re-mark                        |
+----------------+--------------------------+----------------------------------------------+
| [Exit recovery]~ 4 scopes not released (ready is not released)                            |
| Drafts, imports and publication remain available: a scope blocked by a lost key leaves    |
| that state through a newly published release that leaves unreleased scopes' assignments   |
| unchanged; one that changes them fails recovery-mode-active, or scope-busy first where an |
| operation holds the scope.  [Open Changes]                                                |
+-------------------------------------------------------------------------------------------+

Account scope mch_k2p…7xqa (pre-restore unaccounted)
  Restore stopped      10:31
  Start observation    [obs_a… ▾]  must be the first successful read after the latest of the
                                   stop plus the maximum transport deadline and any restored
                                   unaccounted attempt's transport deadline
  End observation      [obs_b… ▾]  recovery observations only; must begin at least the
                                   settle floor after the latest of that point and the start.
                                   Settle floor 30s, maximum transport deadline 2m, as
                                   GET /server serves them (G17).
  Resource version     read at both ends, unchanged between them
  Explanation          [______________]  required if the resource version moved
  This records your judgment that no request sent before the restore, from any attempt the
  restored journal shows or one it lost, can still land. It may already have landed; nothing
  here shows whether it did. The settle floor is a minimum wait, not a proven bound: a later
  landing can overwrite work admitted after release, and would be seen only as drift at a
  later observation.
  Type the machine ID to record: [________]   [Record accounting]
```

## W11. Empty, withheld, error and offline states

```text
Empty:      No drafts yet. A draft collects changes before they are published. [Create draft]
Withheld:   Configuration not shown: it could not be redacted.
Unread:     Observed: not read at 09:46 — node unreachable; provider access denied.
Refused:    Not saved: validation-failed (rule unknown-reference). "registry/x" has no
            declaration. [Go to field]
Conflict:   Someone changed this draft (revision 7). Your text is not saved. [Show their change]
Unknown:    Unknown whether this was saved: the connection dropped. [Repeat exactly]
            (same request, same key; offered only while this page holds the request,
            for the same sign-in and last-seen epoch; otherwise [Show current state])
Set aside:  Your sign-in or the installation's epoch changed. A change with an unknown
            outcome is pending: it may already have been committed. It was not repeated,
            and repeating it stays disabled. [Show current state]
Offline:    Offline since 10:12. Showing data read at 10:11. Actions are disabled.
Human only: This action needs a person signed in; automation tokens cannot approve.
Not accepted: Not accepted by this installation. Sign in again. [Sign in again] — text
            you have not sent is kept on this page.
```

Every state text is announced as a status message without moving focus, and every badge has
its text in the accessible name ([§13](operator-experience.md#13-usability-and-accessibility-acceptance)).
