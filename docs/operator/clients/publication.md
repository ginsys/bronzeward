# Journey: publish a draft and read what it built

**You want** to turn a finished draft into a **release**: the configuration of every machine of
one cluster that has an import base, rendered and checked, and made each such machine's **Desired** release (the release the
installation wants the machine to run; see [the inventory](inventory.md)). Then you want to
read what was built, machine by machine, before anybody plans to send it. Words such as ETag and
epoch are in [the word list](terms.md).

**You need** the `publisher` role to publish. Reading a release, a rendered configuration and its
provenance needs at least one role (viewer, author, publisher, approver or recovery-admin). A draft
you have just finished is in [editing](editing.md); sending a release to a machine is in [plans and
approval](plan-and-approval.md).

The `text` blocks on this page show the shape of the output, read from the source. This page does
not claim that a run printed them.

**Publishing sends nothing to any machine.** It renders, validates and records. The release
becomes the machine's Desired release, and a machine changes only after a plan is approved and
applied. Both clients say so after every publication: "Nothing has been sent to any machine."

## Command line

Run the publication against the draft's ID. The client sends the ETag of the draft it last read, so
a draft that somebody else changed since is refused (see the table).

```bash
bronzewardctl publish <draft>
```

```text
Publishing draft <draft> revision <n> (operation <operation>).
Publication succeeded: release <release> is now Desired for <n> machines. Nothing has been sent to any machine.
  <machine> has no Applied release: adopt it with an adopt plan:
    bronzewardctl plan create --release <release> --machine <machine> --operation adopt
  <machine> has Applied release <release>: plan an apply-config:
    bronzewardctl plan create --release <release> --machine <machine> --operation apply-config
Review a machine's rendered configuration: bronzewardctl release review <release> <machine>
```

The command follows the publication operation until it ends and prints its events on standard
error. With `--no-wait` it prints the operation and stops: "Nothing has been sent to any machine.
Follow it with: bronzewardctl operation show <operation>". A published draft takes no more writes.

The lines after the first name the next command for each machine of the release.
A machine with no Applied release is **adopted** first; a machine with one gets an
**apply-config** plan. A machine whose Desired release is another release gets no plan from this
one.

Read the result with `release list`, `release show`, `release review` and `release provenance`.
Each needs any one role.

```bash
bronzewardctl release list
bronzewardctl release show <release>
bronzewardctl release review <release> <machine>
bronzewardctl release provenance <release> <machine>
```

`<machine>` is the machine's name or its ID (see [the inventory](inventory.md)).

- `release show` names the cluster, who published it and when, the draft and revision it was
  built from, the renderer (contract, machinery and Kubernetes versions), the **source
  revisions** and the machines it covers, each with the mode it was validated in. The source revisions are the fragments, profiles and
  assignments the release was built from. They are not configuration.
- `release review` prints the rendered configuration of one machine, as the server redacted it. A
  redacted value shows as a token, so an empty field was empty and not redacted. If the server
  could not redact the configuration, the command prints "Configuration not shown: it could not
  be redacted." and the server's notice, and no document. Under its first line it says where the
  release's hostname came from. It ends those lines with "Not checked: ..." where a clash with
  another machine's Kubernetes node name could not be checked: the review hides an input to the
  node name, or the release was made before node names were recorded.
- `release provenance` lists, for that machine, each secret reference, its version, the source
  revision and path it came from, and where it was rendered. It ends with "Provenance covers
  secret references only; other fields are not traced." It never shows a
  secret's value.

`-o json` and `-o yaml` print the review as the server sent it. See the
[reference](cli-reference.md#release-show) for the flags and exit codes.

## Web client

1. Open the draft and choose **Publish this draft**. The page "Publish draft" says that
   publishing builds a release from this draft, renders every machine of the cluster whose
   imported configuration is in this draft or in the release applied to it, and makes the release
   their Desired release. It repeats "Nothing has been sent to any machine."
2. Press **Publish**. It needs the `publisher` role. The page shows the operation's progress, and
   at the end "Publication succeeded: release <release> was published. Nothing has been sent to
   any machine."
3. Open the release. Its page lists the source revisions "(these are not configuration)", the
   machines covered, and for each a link **Rendered configuration of <machine>**.
4. Open a machine's rendered configuration. The page shows the document, the note "A field that
   shows as empty is empty in the rendered review. A redacted field shows as a badge, so an empty
   one was not redacted.", where the release's hostname came from (taken from the machine's name, set in the configuration, left to the node, the legacy stable-hostname setting, or not recorded) and, for a name taken from the machine's name, how it differs from the release last applied to the machine and from the machine's current name, a line "Not checked: ..." where a clash with another machine's node name could not be checked (the review hides an input to the node name, or the release was made before node names were recorded), and a table "Provenance (secret references only)" with the
   same records as the command, with the source revision and its path in separate columns. Under "Next step" it offers **Plan adopt** or **Plan apply** for a
   machine that needs one. Pressing it opens the plan page and sends nothing.

## What can go wrong

| What you see | Why, and what to do |
|---|---|
| `Needs one of the roles [publisher].` and `You are signed in as ...` | You lack the role. The text is the one in [the inventory page](inventory.md); ask for `publisher`. |
| "The draft changed since you last read it (you saw revision N, it is now revision M). Nothing was saved. Review the change before writing again:" | Somebody changed the draft after you read it. Read it with the command printed under the message, then publish again. The web page says "Not published: someone changed this draft since this page read it." with **Read the draft again**. |
| "Not sent: this page has not read the draft under the current epoch. Read it again first." | The page holds no ETag of the draft that is valid in this installation's current epoch, so it sends nothing. Press **Read the draft again**. |
| "publication operation <operation> failed: <reason>; nothing was sent to any machine" | The publication failed and left the draft open. Exit code 4. Fix what the reason names and publish again. The server checks the observation behind each covered machine before it makes the release, so a publication whose evidence for one machine does not hold fails in this way and makes no release, so no Desired release moves. |
| "This draft is <state>, not open: it cannot be published." | The draft was published or discarded. |
| "release <release> was published for <n> machines, but it is no longer Desired for <k> of them" | A later publication replaced it for those machines. Plan nothing for them from this release. |
| "The epoch changed from <a> to <b> while the machines were read, so no next step is shown." | The installation was restored while the command read the machines. Read them again with `release show`. |
| "the publication was accepted without naming its operation; find it with: bronzewardctl operation list --kind publish" | The server answered without an operation ID. Find the operation with the command named. |

## What the result does and does not show

- A succeeded publication shows that the server rendered and validated a configuration for each
  machine and recorded the release as their Desired release.
- It does not show that any machine has the configuration. Only an approved plan that was applied
  does that, and the machine's observation after it.
- The rendered review is the configuration as redacted. Provenance covers secret references only,
  so it does not trace any other field.

This page has no screenshot: the release page lists rendered configuration, and no screenshot of one
is kept in this documentation.

Next: [plan the release for a machine and have it approved](plan-and-approval.md). A draft that
needs more changes first goes back to [editing](editing.md).
