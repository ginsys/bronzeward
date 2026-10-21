# Operator experience: web wireframes

**Status: proposed, not accepted for implementation** (see the
proposal's status). Low-fidelity wireframes for the web journeys in the
[operator experience proposal](operator-experience.md). They fix content, order and which
action is available to which role. They do not fix layout, styling, framework or wording beyond
the labels that carry meaning. All data is synthetic; identifiers are shortened as `mch_tqhc…35uq`.

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

Roles are shown only once the API can report them
([gap G1](operator-experience.md#11-api-gap-audit)); until then the header shows the identity and
the epoch, and actions are enabled and refused by the server. The recovery banner states the
scope summary and, while the server runs as started for recovery, that plan creation, approval
and unfreeze wait for its normal restart and that accepted publications complete only then;
imports complete on it. It never disables actions recovery mode allows
([§5.9](operator-experience.md#59-recovery-after-a-restore)): an action is disabled only on a
scope whose state refuses it, with that reason.

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
revoked"); `503` ("Authentication could not reach the identity provider or the database; nothing was
committed"); and, once roles
can be read (gap G1), no roles ("You are signed in but hold no Bronzeward role"). A local
reading, such as "your token expired at 10:15 by this browser's clock", is labelled as the
client's own.

## W2. Machines

```text
Machines                                    Cluster: all ▾   State: all ▾   [Record machine]  (author, human)
+------------+----------------+-------------+--------------------------+--------------------------+-------------------------+
| Machine    | Endpoint       | Desired     | Applied (source)         | Observed                 | State                   |
+------------+----------------+-------------+--------------------------+--------------------------+-------------------------+
| mch_k2p…7x | 10.5.0.2:50000 | rel_fgqv…   | rel_fgqv… (operation)    | = applied, 2 min ago     | (•) Matches applied     |
| mch_tqhc…35| 10.5.0.3:50000 | rel_fgqv…   | rel_p4nx… (adoption)     | = applied, 1 min ago     | (•) Pending convergence |
| mch_w4ze…n3| 10.5.0.4:50000 | rel_fgqv…   | rel_fgqv… (operation)    | no match, 5 min ago      | (•) Drifted (•) Frozen  |
+------------+----------------+-------------+--------------------------+--------------------------+-------------------------+
Filtering is done in the browser over all pages (gap G9). Showing 3 of 3. Health is not checked.
```

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
| [Freeze]~ already frozen    [Unfreeze]~ needs approver    [Plan revert]  (publisher)      |
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
Observation rows with unread parts show each cause ("node unreachable", "provider access
denied") and never a healthy badge. "Drifted" comes from the open drift record, which compares
with `Applied`; an observation equal to `Desired` or an older release is still drift. With no
`Applied`, `Desired` or observation the row says so ("Not applied or adopted by Bronzeward",
"No release selected", "No configuration read recorded") and shows no time. The status
sentences are those of [§2.4](operator-experience.md#24-desired-applied-and-observed).

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

Profile entry: an ordered list of fragment revisions with move up/down controls operable by
keyboard. Assignment entry: profile names in order and per-layer fragment names. On a `412`
the editor keeps the unsaved text, shows "Someone changed this draft (revision 7). Your text is
not saved." and offers [Show their change] and [Copy my text into the new revision], never an
automatic overwrite.

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
|   mch_b3c…aa2q   no-reboot   [Open rendered configuration]   [Plan apply] (publisher)    |
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
| (•) Self-approval, predicted: you created this plan and published this release. The      |
|     approval records the actual reasons. Allowed and marked.                              |
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
| Stop new commitments and    | Make what the machine runs    | Put Desired rel_fgqv… back. |
| attempts while you decide.  | the applied release.          | Overwrites the change.      |
| Does not stop an attempt    | 1 Import it for adoption,     | 1 Plan apply-config         |
| already recorded.           |                               |                             |
| (•) already frozen          |                               |                             |
|                             |   review and mark  (author)   |   (publisher)               |
|                             | 2 Publish  (publisher)        | 2 Approve  (approver)       |
|                             | 3 Plan adopt, approve         | 3 Unfreeze (approver)       |
|                             | Sends nothing to the machine. |                             |
| [Unfreeze]~ needs approver  | [Start adoption]              | [Plan revert]               |
+-----------------------------+-------------------------------+-----------------------------+
```

## W10. Recovery console

```text
Recovery   (•) Recovery mode on since 10:42   epoch ep_r2l…   entry by ops-carl
Restored: database backup taken 10:20; stopped at 10:31
Server started for recovery: plan creation, approval and unfreeze wait for its normal restart;
publications it accepts complete only after that restart. Imports complete on it.
+----------------+--------------------------+----------------------------------------------+
| Machine        | Scope state              | Next action                                  |
+----------------+--------------------------+----------------------------------------------+
| mch_k2p…7xqa   | pre-restore-unaccounted  | [Account scope]                              |
| mch_tqhc…35uq  | ready                    | [Release scope]  (not an approval)           |
| mch_w4ze…n3fa  | ready                    | [Release scope]  (not an approval)           |
| mch_b3c…aa2q   | unresolved               | op_q8r… held: classified after restart       |
+----------------+--------------------------+----------------------------------------------+
| [Exit recovery]~ 4 scopes not released (ready is not released)                            |
| Drafts, imports and publication remain available: a scope blocked by a lost key leaves    |
| that state through a newly published release.  [Open Changes]                             |
+-------------------------------------------------------------------------------------------+

Account scope mch_k2p…7xqa (pre-restore unaccounted)
  Restore stopped      10:31         Floor from   the stop plus the maximum transport
                                                  deadline, or a later restored attempt's
  Start observation    [obs_a… ▾]  must be the first successful read after that point
  End observation      [obs_b… ▾]  must begin at least the settle floor (30 s) after the
                                   latest of that point and the start
  Resource version     unchanged between them
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
            for the same sign-in and epoch; otherwise [Show current state])
Set aside:  Your sign-in or the installation's epoch changed. A change with an unknown
            outcome is pending: it may already have been committed. It was not repeated,
            and repeating it stays disabled. [Show current state]
Offline:    Offline since 10:12. Showing data read at 10:11. Actions are disabled.
Human only: This action needs a person signed in; automation tokens cannot approve.
Not accepted: Your sign-in was not accepted (it may have expired). [Sign in again] — text
            you have not sent is kept on this page.
```

Every state text is announced as a status message without moving focus, and every badge has
its text in the accessible name ([§13](operator-experience.md#13-usability-and-accessibility-acceptance)).
