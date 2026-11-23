# Journey: edit fragments, profiles and assignments in a draft

**You want** to change what a machine's configuration is made of, without sending anything to a
machine. You collect the change in a **draft**, a set of changes to one cluster that stays open
until it is published or discarded. Publishing and applying are other journeys.

**You need** the `author` role for every write on this page. Reading a draft needs no role. An
automation token may edit a draft, but it cannot record a cluster or a machine.

Three kinds of thing make up a machine's configuration, and a name for each helps:

- A **fragment** is a piece of configuration in a **layer**: global, site, cluster, role, workload
  or override.
- A **profile** is a named list of fragments. It stores the exact **revision** of each fragment,
  not the name.
- An **assignment** says what one machine uses: profiles, fragments by layer, or nothing at all.
  It stores names. Because of that, a later save of a fragment or profile named there changes what
  the machine composes.

## Command line

`draft create` starts a draft for a cluster. The three `set` commands write into it, and `draft
show` reads it. Flags are in the [reference](cli-reference.md#fragment-set); this page shows the
shape.

```bash
bronzewardctl draft create --cluster <cluster> --title "worker labels"
bronzewardctl fragment set worker-labels --draft <draft> --layer role --file worker-labels.yaml
bronzewardctl profile set workers --draft <draft> --fragments worker-labels,worker-sysctls
bronzewardctl assignment set <machine> --draft <draft> --profiles workers --fragment cluster=cluster-base
bronzewardctl draft show <draft>
```

Each write answers with what it saved and the draft's revision, for example "Fragment `<name>`
saved in draft `<draft>` (revision `<revision>`; draft revision 4)." and "Assignment saved (draft
revision 6)." Three things to know:

1. **A profile pins revisions.** `profile set` takes a fragment name and pins the revision the draft
   holds for it. After you change a fragment, set the profile again to use the new one. The command
   says so: "A profile pins revisions: after changing a fragment, set the profile again to use it."
2. **A fragment's document never goes on the command line.** Give `--file <path>` or `-` for
   standard input. `fragment set` runs on Linux only.
3. **`assignment set --none` selects nothing.** The machine's configuration is then its import base
   alone. This is the form a machine needs before a release can bind it.

`draft show` lists the draft's entries and marks which changed against their base. It does not
render any machine's configuration: nothing is rendered until the draft is published.

## Web

Steps:

1. Open **Drafts and releases** in the navigation. Press **Create draft**, choose the **Cluster:**
   and fill in **Title:**. The page shows the new draft.
2. In the draft's **Entries** list press an entry to edit it, or **Add fragment**.
3. Edit the document in the fragment editor. Press **Save** (needs `author`). **Revert to base**
   restores the base text of the entry, and **Remove** removes the entry from the draft.
4. For a profile, build its list: **Add to profile** adds a fragment, and each row has buttons to
   move it up or down or remove it. The same buttons work from the keyboard.
5. For an assignment, the list of profile names and each layer's list of fragment names work the
   same way. The machine is the entry's name.
6. Press **Show composition and references** to read the order in which the lists compose and, for
   each fragment, each reference with its state. It shows no document and no secret value.
7. **Discard draft** drops the whole draft.

## The refusal you will meet: the draft changed

Another author, or you in another window, changed the draft after you read it. The client then saves
nothing and says so. This protects the other change from being overwritten without being seen.

- **Command line:** "The draft changed since you last read it (you saw revision N, it is now
  revision M). Nothing was saved. Review the change before writing again:" followed by a `draft
  show` command to run. The client never re-reads the draft and repeats your write for you.
- **Web:** for a discard, "Not discarded: someone changed this draft since this page read it." with
  a **Show their change** button. If the editor holds unsaved text, it says "Not read: the fragment
  editor holds unsaved text. Save it or switch entries first." Save your text, or leave the entry,
  and then read their change.

After reading, repeat the write if it is still right.

## What this page does not establish

- A saved entry is a change to a draft. It is not published, not planned and not applied. No machine
  has received anything.
- A profile that saved shows its pins; it does not show that the fragments it names still say what
  you read.
- "Saved" does not show that the configuration is valid for a machine. That shows when the draft is
  published and a machine's configuration is rendered.

Next: [import a machine's configuration under review](protected-import.md).
