# Journey: share a fragment between clusters

**You want** one fragment, or one profile, that several clusters use, without any cluster receiving
a change it has not read. The shared copy lives in a **library**. A cluster follows it by
**adopting** it in a draft of its own, and the change then appears in that cluster's own plan, the
same as any other change. Editing a draft is [another journey](editing.md), publishing is
[a third](publication.md), and applying is [a fourth](plan-and-approval.md). Other words of the guide
are in [the word list](terms.md).

**You need** the `author` role for every write on this page. Reading needs at least one role. A
fragment that holds a secret cannot be shared: it stays in one cluster.

The rules this page rests on:

- Publishing a shared fragment moves **your cluster** to the new revision and no other cluster.
- Another cluster reads the change only when its own draft adopts it. Until then its releases keep
  the revision it had adopted, and its plans hold nothing of the newer one.
- Before you publish, `draft impact` lists the machines of your cluster that gain, lose or keep a
  revision, and the other clusters that stay on their revision. After you publish,
  `fragment adoptions` lists every cluster that uses the name and, with `--behind`, those on an
  older revision than the library's.
- One apply is for one cluster. Nothing on this page chooses, plans or approves anything for a
  second cluster.

## Command line

The blocks below run in order. The first is cluster A, which publishes the shared fragment
`ntp` from a draft whose machine is imported. The second is cluster B, which adopts it, selects it
for a machine and publishes. Flags are in the [reference](cli-reference.md#fragment-set).

```bash {.bw-run .bw-db}
bronzewardctl fragment set ntp --draft <draft> --layer global --file ntp.yaml --library
bronzewardctl draft impact <draft>
bronzewardctl publish <draft>
```

```bash {.bw-run .bw-db}
bronzewardctl fragment adopt ntp --draft <draft>
bronzewardctl assignment set <machine> --draft <draft> --fragment global=ntp
bronzewardctl draft impact <draft>
bronzewardctl publish <draft>
```

Later, cluster A changes the shared fragment in a new draft. `draft impact` lists cluster B as
staying on its revision, and publishing moves A and no other cluster.

```bash {.bw-run .bw-db}
bronzewardctl fragment set ntp --draft <draft> --layer global --file ntp.yaml --library
bronzewardctl draft impact <draft>
bronzewardctl publish <draft>
```

Any cluster can now list who is behind. B is listed until its own draft adopts the new revision.

```bash {.bw-run .bw-db}
bronzewardctl fragment adoptions ntp --behind
```

`fragment adopt` prints the machines of the draft's cluster it reaches. `--revision <frv_ ID>`
adopts an older revision than the head. `--remove` stops following the name, and `fragment detach`
copies the adopted revision into the cluster as its own fragment and stops following. A profile of
your cluster that pins the name keeps its pin: set it again to use the new revision. `profile set`,
`profile adopt`, `profile detach` and `profile adoptions` do the same for a shared profile.

## Web

Steps:

<!-- bw-web-steps: shared-fragments -->

1. Open a draft and its **Shared names** section, and press **Show shared names**. Each shared
   fragment and profile shows what this cluster uses of it: the revision it follows and whether
   that is behind the library's.
2. Read **What the shared entries reach** on the draft page. For each shared entry of the draft it
   lists the machines of this cluster that gain, lose or keep a revision, and the other clusters
   that stay as they are.
3. Open a shared fragment or profile from the lists. Its page shows **Clusters using this fragment**
   with the revision each is on and whether it is behind the library's. The page changes no
   cluster.

Beside a name, **Adopt** records in this draft that the cluster follows it at its current revision,
and **Stop following** records that it no longer does. Each says what it records before the press,
and nothing changes for any machine until the draft is published.

When a release moves a shared name, [the apply page](plan-and-approval.md) lists it above the
machines, with the machines of this cluster it reaches, and names the other clusters behind it as
information only.

## What this page does not establish

- The command blocks run against a disposable server with a stand-in for each Talos node. They
  show that the commands do what this page says on the server; they do not apply a release made
  from an adoption to a real node. That run has not been made.
- A shared change is never applied to a cluster that has not adopted it. The page shows that this
  holds in the plans of the cluster, which is where an operator reads it; it does not read what a
  machine runs.
- The web steps have a browser case each against recorded server answers, not against a server with
  two clusters.

Next: [apply the release](plan-and-approval.md).
