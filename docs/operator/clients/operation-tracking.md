# Journey: follow an operation and read a machine's history

**You want** to know what happened after a plan was approved: whether the controller sent the
configuration, whether the machine accepted it, and what the machine's applied configuration is
now. You also want the history of one machine, including plans that never ran.

**You need** at least one role (viewer, author, publisher, approver or recovery-admin). Every
command and page on this page reads. Plans and approvals are in [plans and
approval](plan-and-approval.md). Words such as operation, controller, Applied and Observed are in
[the word list](terms.md).

The `text` block on this page shows the shape of the output, read from the source. This page does
not claim that a run printed it.

An **operation** is one run of something: a publication, an import, an apply or an adoption. An
approved apply or adopt plan becomes an operation when the controller commits it, so a plan page
that says "Committed: operation <operation>" gives you the one to follow. The operation's state
moves on its own. Reading it never changes it.

## Command line

```bash
bronzewardctl operation list --machine <machine>
bronzewardctl operation show <operation>
bronzewardctl operation watch <operation>
```

`<machine>` is the machine's name or its ID (see [the inventory](inventory.md)).
`operation list` filters by `--plan`, `--machine`, `--state` and `--kind`; `--cluster` there needs
`--machine`. `operation show` prints
the operation once: who started it, a sentence for its kind and state, its outcome, and the next
step. For an apply or an adoption the starter is "the controller", with the plan it committed.

`operation watch` follows it live and prints each event as it arrives:

```text
apply-config  plan <plan>, machine <machine>
Times marked "received" are this client's clock when the event arrived; the completion line shows the server's time.
received 10:06:02  commitment            plan <plan>, approval <approval>, ...
received 10:06:03  attempt               attempt 1, route <address>, transport deadline <time>, ...
received 10:06:04  response              attempt 1, class accepted
received 10:06:40  classification        outcome completed, ...
received 10:06:40  operation-state       from verifying, to completed
Completed at <time> (server): applied <release>, confirmed by observation <observation>.
```

An event carries no time on the stream, so each line shows when this client received it. The last
line shows the time the server recorded the end.

**What the states mean.** The sentence and next step that `operation show` prints for each:

| Kind | State | What it says |
|---|---|---|
| publish | queued, running | "Accepted; no worker has taken it yet." / "A worker is compiling the draft and writing the release." Wait. |
| publish | succeeded | "The draft became an immutable release. Nothing was sent to any machine." Next, create a plan for each machine. |
| publish | failed | "The draft was not published; no release was made." Fix the cause and publish again. |
| apply-config | committed | "The controller committed the plan and holds the machine; no request is sent yet." Wait. |
| apply-config | sending | "A request is in flight, or its outcome is unknown." Wait. |
| apply-config | verifying | "The node accepted the request; the controller is checking the result." Wait. |
| apply-config | completed | "The machine's Applied configuration is updated." Nothing more. |
| apply-config | rejected | "The machine was not changed." Correct the release and create a new plan. |
| apply-config | failed | "The machine may have been changed wrongly or not changed at all; its Applied configuration was not updated." Read the machine's drift and observations, then create a new plan. |
| apply-config | cancelled | "No attempt is recorded for it, which is not proof that nothing was sent." Read the machine's Observed configuration, then plan again if the change is still wanted. |
| apply-config | unresolved | "The controller keeps examining it and may settle it on its own." The outcome line carries the next step. |
| adopt | completed | "The machine's running configuration is recorded as Applied, by observation. Nothing was sent." |

**`unresolved` is not the end.** The watch goes on past it. If the time given by `--timeout`
(default `30m`) passes, the watch ends with exit 5 and prints: "Still unresolved after --timeout
30m0s. Unresolved: completion and retry safety are unknown. A recovery administrator must account
for attempt 1. No conflicting work on this machine proceeds until then." Accounting for an attempt
is in [recovery after a restore](recovery-after-restore.md). The watch never changes the operation,
and ending it does not stop the operation.

**Ending the watch.** A `failed`, `rejected` or `cancelled` operation ends the watch with exit 4,
naming the cause and the next safe step. Ctrl-C stops only the watch, with exit 130, and prints
"Stopped watching at your request. Operation <operation> goes on" and the command that follows it
again. If the server cuts the stream, the watch reads again after 1 second, doubling up to 30
seconds, so a restart of a minute does not end it. The other exit codes, including 3 (the state
could not be read) and 6 (a line could not be written), are in the
[reference](cli-reference.md#operation-watch).

## A machine's history

```bash
bronzewardctl machine timeline <machine>
bronzewardctl machine timeline <machine> --follow
```

The timeline shows what happened to the plans of one machine, including the ones that never ran:
each line is a plan, an approval, a refusal, a plan cancellation, a revocation, or an entry of an
operation. For
example, a refusal names the plan, the comparison that failed and its cause, such as "the machine's
Desired release changed". Without `--follow` it reads one page, oldest first. With `--follow` it
shows the newest page and then each new entry as the server records it, read every 5 seconds;
Ctrl-C ends it with exit 130 and "Stopped following the timeline of machine <machine> at your
request." If the installation was restored while you follow, it says so and starts again from the
newest page. `--follow` needs an API revision that serves it; on an older server the command says
which revision the server has and sends nothing.

## Web client

1. Open the operation. A plan page that has committed names it ("Committed: operation
   <operation>"). The page opens with a sentence for the
   operation's kind and state, the outcome, and the next step.
2. The "Timeline" lists each event live. The page says "Live: connected (resumes after a drop)"
   while it follows, and "Live: ended." after the end. The outcome line reads, for example,
   "Applied: this operation applied release <release>, confirmed by observation <observation>.",
   "Failed: <cause>", "Rejected: the node refused every attempt as invalid" or "Cancelled".
3. Closing the page does not stop the operation: "Closing this page does not stop the operation.
   Open it again and the server's state is read."
4. A machine's page shows the machine's timeline, newest first.

![The operation page of the web client for a finished adopt operation. The title reads "Operation:
Adopt of 10.55.0.3 to S1 import, published 24 Nov 2026, 06:40 (adopt, completed)". A notice says an
adoption record was made. The "Timeline" lists "adoption" and "applied-change" at 06:40:54, then
"Live: ended." and "Closing this page does not stop the operation." Below, the page shows Kind
adopt, State completed, Subject and Created. Taken on the participant fixture at revision
454b5609, with synthetic names.](images/operation-tracking-1.png)

This screenshot shows a finished adopt operation, which sends nothing to a machine. It does not
show an apply that is still held, "Live: connected", or an outcome line for an applied release: no
screenshot of those states was taken.

## What the result does and does not show

- "Completed" for an apply names the observation that confirmed the release. It is the
  controller's reading of the machine at that time, and not a guarantee about any later time.
- On the command line, times marked "received" are this client's clock, and only the completion
  line carries the server's time. On the web, a finished operation's timeline is read again from the
  server, so its rows then show the server's times without the word "received".
- A stopped watch says nothing about the operation. Read it again with `operation show`.

Next: read the machine's row in [the inventory](inventory.md), where Applied now names the
release. A change that failed or was refused goes back to
[plans and approval](plan-and-approval.md). An operation that ends "unresolved" needs the recovery
pages, which are not written yet.
