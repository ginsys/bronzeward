# Journey: read the inventory, and record one machine

**You want** to see which clusters and machines the installation knows, how each machine's
configuration compares with what was applied, and to record one machine that is missing.

**You need** at least one role to read: `viewer`, `author`, `publisher`, `approver` or
`recovery-admin`. A credential with none is refused. Recording a machine needs the `author` role and
a person signed in, not an automation token.

A cluster the installation does not know yet comes first: [connect it](connect-a-cluster.md).

Words such as Desired, Applied, Observed, drift and frozen are in [the word list](terms.md); the
table "Read the columns" below also says what each column shows.

## Read the machines

**Command line.** Each list shows one page. `--all` reads every page, and `--cluster` limits the machine
list to one cluster, given by its ID (the first column of `cluster list`; a name is refused here). [Contexts](contexts.md) and the
[reference](cli-reference.md#machine-list) hold the flags.

```bash {.bw-run}
bronzewardctl cluster list
bronzewardctl machine list
```

```text
CLUSTER  NAME  ENDPOINT  CONTRACT  TALOS CLUSTER ID
MACHINE  ENDPOINT  DESIRED  APPLIED (SOURCE)  OBSERVED  STATE
Health is not checked in this installation.
```

With nothing recorded, the lists print their column names and no rows, and the machine list adds
"Health is not checked in this installation." With machines recorded, each row looks like this
(shortened identifiers; the dots are not output). The `text` blocks on this page show the shape of
the output, read from the source. This page does not claim that a run printed them.

```text
MACHINE        ENDPOINT         DESIRED       APPLIED (SOURCE)          OBSERVED           STATE
mch_k2p…7xqa   10.55.0.2:50000  rel_fgqv…j6m  rel_fgqv…j6m (operation)  =applied, 2m0s ago  last read matched what was applied
mch_w4ze…n3fa  10.55.0.3:50000  rel_fgqv…j6m  rel_fgqv…j6m (operation)  no match, 5m0s ago  changed on the machine, frozen
```

The command line prints the age as a duration with seconds, such as `2m0s ago`, and prints `-` in
the State column when no state applies. The web page uses its own badges, listed below.

**Web.** Steps:

1. Open **Machines** in the left navigation (the first page after you sign in). The page lists
   one row for each machine: machine, endpoint, node, desired, applied (source), observed and state.
2. Narrow the list with the **Cluster:** and **State:** menus. The state menu offers "Changed on the
   machine", "Frozen", "Release not applied yet", "Last read matched what was applied" and "Not
   adopted yet".
3. Press a machine's name to open it, and **Clusters** in the navigation to see the clusters: name,
   Kubernetes endpoint, contract and Talos cluster ID. A cluster's page shows its machines and its
   open drafts. A person with the `author` role also sees the cluster's **Talos access** line.
4. The page ends with "Health is not checked."

## Read the columns, and what they do not establish

| Column | It says | It does not establish |
|---|---|---|
| Desired | the release the installation wants this machine to run | that anything was sent to the machine |
| Applied (source) | the release the installation recorded as applied, and how it got there (an operation, or an adoption) | that the machine still runs it |
| Observed | how the last configuration read compares: "= applied", "= desired" or "no match", and how long ago | that the machine is healthy. It compares configuration only. |
| State | the badges: "Changed on the machine" (an open drift record), "Frozen", "Release not applied yet", "Differs in form only (comments and encoding)", "Last read matched what was applied", "Not adopted yet" | one state for the machine: a machine can carry several badges |

"no match" means the read equals neither the applied nor the desired release. The reason is the
open drift record, which the machine's page and `bronzewardctl status` show. A match is never a
health result, and a time such as "5m ago" is the age of the last read, not the age of the machine.

Read one machine and its history with `machine show`, `machine observations` and `machine
timeline`. Every command that takes a `<machine>` accepts its name as well as its ID. The
command reads the machine list once and matches the name across the clusters you can read.
`--cluster` (a cluster's name or ID) narrows that match: a name that two clusters hold is refused
without it. An ID is sent as it is, and `--cluster` given with an ID is a usage error. So are
`plan list`, `operation list` and `import status` given `--cluster` and no `--machine`. The web
pages link machines by ID.

## Record one machine

Use this for a machine that is not recorded yet in a cluster that is. Recording a machine is an
`author` act. The node's own identity must be read first: `--smbios-uuid` where the node reports
one, else `--talos-node-id` (the reference says how to read each from the node, and that a node
that reports a UUID and is recorded by node ID is refused later).

**Command line:**

```bash
bronzewardctl machine add --cluster <cluster> --talos-node-id <node id> --platform metal --talos-endpoint 10.55.0.3
```

```text
Machine <name or ID> recorded in cluster <ID> (<name>). Endpoint 10.55.0.3:50000.
Next: supply the cluster's first key (cluster first-key), then import its configuration.
```

The endpoint is an IP address, with the port 50000 unless you give one.

**Web.** Steps:

1. Open **Machines** and press **Add machine**. The button is absent or off for a person without
   `author`, with the reason.
2. Choose the cluster. Fill in **Talos node ID:**, **Platform:** and **Talos endpoint:**.
3. Press the form's **Add machine** button. The form then says "Machine" and the machine's name
   as a link, "recorded in cluster" and its identifier, the endpoint, and the next step; it does
   not open the machine's page. Press the link to open it. A refusal shows in the same place.

A recorded machine is not yet managed: it has no desired or applied release. The web list shows "Not
adopted yet" for it; the command line shows `-` in the State column. Recording does not read or
change the node.

## Refusals you can meet

- A person without `author` is refused with the roles the route needs: "Needs one of the roles
  [author]." The command line adds "You are signed in as `<name>`."
- An automation token is refused: "This needs a person signed in, not an automation token."
- A name another machine holds is refused as `name-in-use`; the line names the machine that holds it.

Next: [edit fragments, profiles and assignments](editing.md).
