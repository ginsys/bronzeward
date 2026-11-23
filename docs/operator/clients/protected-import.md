# Journey: import a machine's configuration under review, and adopt it

**You want** to bring a running machine's configuration into a draft, mark any secret the schema did
not recognise, and then adopt the machine so it can be managed. The import is **protected**: its
text may hold secrets, so it is shown in one place and kept nowhere.

**You need** the `author` role, signed in as a person, to import, review and mark. You need
`publisher` to publish and to create the plan, and a person with `approver` to approve it. One
operator who holds all three can do every step. An automation token cannot import, review or approve.

You also need a recorded machine and an open draft for its cluster: see
[the inventory](inventory.md) and [editing](editing.md).

The `text` blocks and quoted messages on this page are read from the source. This page does not
claim that a run printed them.

## Command line

```bash
bronzewardctl import start --machine <machine> --draft <draft> --review
```

```text
Import <id> started (operation <id>). Staging: encrypted. Review: requested.
Paused for review (generation 1, expires <time>).
Review it, then continue it, or abandon it:
```

`--review` needs encrypted staging and pauses the import before anything reaches the draft.

**Review.** `import review <import>` shows the staged document and then reads marks. It works only
when standard input and output are a terminal, and it has no `-o json`, `-o yaml`, save or export
form: the client writes the text nowhere else. It begins with the notice "protected review: this
client writes it nowhere but this terminal. Terminal scrollback, session recording and screen
capture are outside its control." The review is an interactive pager, so this step is not a marked
block. At the prompt "Mark a value as secret by its path, one per line; empty line to finish:" type
each path; an empty line ends it. A mark is refused with the rule and its position, never its path.
The import then pauses again with a new generation. Review again, or continue.

```bash
bronzewardctl import continue <import>
```

```text
Import <id> released into draft <draft> (revision N), import base <id>.
```

`continue` releases the import into the draft. The draft's revision moves, and the client keeps
the draft's ETag for later writes.

## Web

Steps:

1. Open the list of imports and press **Start import**. Choose the **Machine:** and the **Draft:**
   (**Create draft** is beside it if the cluster has none open). Under **Source**, choose **Read from
   the machine**. Under **Staging**, choose **Encrypted staging**, and tick the box that pauses for
   review before the draft changes. A review needs encrypted staging.
2. When the import is paused, open its page. Press **Review import** (needs `author`). The
   **Protected review** section shows the document.
3. For each value that is a secret, press **Mark as secret** beside its path. Press **Send marks**.
4. Press **Continue import** to release the import into the draft. **Abandon import** discards its
   staged input instead.

## When it does not go through

| You see | What it establishes | What to do |
|---|---|---|
| Command line: "…The draft changed after this import started, so the import was not applied and its claim is abandoned. Read the draft's current revision, which keeps its ETag, then import again:" (exit 4) | Another write moved the draft while the import ran. The import applied nothing. | Run the `draft show` the message prints, then `import start` again. The server keeps no marks: add `--file` or `--marks` again if you used them. |
| "This client did not start import `<id>`, so it keeps no ETag of draft `<draft>`. Read the draft before writing to it:" | The client has no version of the draft to send, because it did not start the import. | Run the `draft show` the message prints, then write. |
| "…its staged input could not be decrypted (resume-failed …)" | The import was not applied. Its claim stays held until its lease lapses. | Read its lease with `import status`. Once it lapsed, `import takeover` it, or `import abandon` it. |
| A held import whose lease lapsed | Its runner is gone; nothing is running it. | `bronzewardctl import takeover <import>` to resume it, or `import abandon`. |
| Web: the buttons **Continue import**, **Take over import** and **Abandon import** | The page offers only the ones the claim's state allows now. | Press the one that fits; the page reads the claim again after it. |

Abandoning says "its staged input is cleared, and nothing of it reaches draft `<draft>`".

## Adopt the machine

A machine recorded from an existing cluster has no applied release. A plan to apply configuration is
refused until the machine is adopted. Adoption gives it an assignment in the draft, publishes the
draft, and applies nothing to the machine: an adopt plan records what the machine already runs.

```bash
bronzewardctl assignment set <machine> --draft <draft> --none
bronzewardctl publish <draft>
bronzewardctl plan create --release <release> --machine <machine> --operation adopt
bronzewardctl approve <plan>
```

`--none` selects nothing, so the machine's configuration is its import base alone. `publish` needs
`publisher`: it makes the release the desired one and sends nothing to any machine. `plan create`
needs `publisher` too; the plan is proposed and not approved, and it sends nothing. `approve` needs a
person with `approver`. On the web, the same steps are the **Plan adopt** button on the release's
rendered review and **Approve** on the plan's page.

## What this page does not establish

- A released import is in a draft. It is not published, not planned, and the machine has been sent
  nothing.
- A mark covers the paths you typed. It does not show that the document holds no other secret. Read
  the whole review before you continue.
- An approved adopt plan records the machine's own bytes as applied. It does not show that those
  bytes equal what a release would compile: the plan's evidence says whether they differ. What a later
  apply plan shows when they differ is part of plans and approval, not this page.
- Nothing here shows the review text was not captured outside the client.

Next: back to [the inventory](inventory.md), where the machine now shows an applied release.
