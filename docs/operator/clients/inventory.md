# Journey: read the inventory, and record one machine

**You want** to see which clusters and machines the installation knows, how each machine's
configuration compares with what was applied, and to record one machine that is missing.

**You need** at least one role to read: `viewer`, `author`, `publisher`, `approver` or
`recovery-admin`. A credential with none is refused. Recording a machine needs the `author` role and
a person signed in, not an automation token.

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
timeline`. A machine's name works wherever the command takes a machine; a name that two clusters
hold needs `--cluster` or the ID.

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

## Connect a cluster

Use this for a cluster the installation does not know yet. It needs the `author` role and a person
signed in. You give the cluster's talosconfig file. The installation reads the cluster's members
through one endpoint of that file. This first read only reads: it records nothing.

**Which endpoints are tried.**

- A talosconfig holds contexts, and each context lists endpoints. You can name a context. With none,
  the current one is used.
- With no endpoint of your own, the context's endpoints are tried in order, and the first one that
  gives the cluster is used.
- An endpoint you give is the only one tried. It is an IP address, with the port 50000 unless you give
  one, or a DNS name. A name is looked up once. The page then says which address it resolved to. If the
  nodes' certificates do not list the name, the read is refused with a sentence that asks you to add
  the name to `machine.certSANs` of the control-plane machines, or to give the address of a
  control-plane node.
- A context that lists no endpoint and no endpoint given: the read stops and asks for an address.

**Command line:**

```bash
bronzewardctl cluster discover --talosconfig ./talosconfig --talos-context <name> --endpoint <host|ip[:port]>
```

`--talos-context` and `--endpoint` are optional. `cluster onboard` takes the same three options, and
also `--name` and `--kubernetes-endpoint`. With no `--endpoint`, the command tries the context's
endpoints in order. A silent endpoint costs up to about 15 seconds, so three silent endpoints cost
about 45 seconds. A failure of one endpoint moves on to the next, except Ctrl-C, which ends the list.

When one endpoint fails alone, the command prints that endpoint's own error. When several fail, it
prints one error that names each of them:

```text
no endpoint of the talosconfig gave the cluster: 10.55.0.2:50000: <error>; 10.55.0.3:50000: <error>
```

A context with no endpoint gives a usage error, "the talosconfig names no endpoint: give --endpoint
<host|ip[:port]>".

**Web.** Steps:

1. Press **Connect a cluster** on the Machines or the Clusters page. For a person without `author`
   the button is absent or off, with the reason.
2. Choose the talosconfig file. **Talos context:** is the current one when empty. **Talos endpoint:**
   is the context's endpoints, tried in order, when empty.
3. Before you press anything else, the page says which endpoint it will use. For a list: "The page
   will try the 3 endpoints in order, starting with 10.55.0.2:50000." For one endpoint: "The page will
   try the cluster at 10.55.0.2:50000." For a context with none: "The talosconfig's context names no
   endpoint: type the IP address of a control-plane node."
4. Press **Read cluster**. While the page tries a list, it says where it is, for example "Trying
   10.55.0.3:50000 (2 of 3).", and shows **Stop**. Stop ends the list after the try that is running. A
   request already sent cannot be taken back. The page then says "Stopping: no other endpoint will be
   tried after this one."
5. When every endpoint fails, the page says "Nothing was recorded. No endpoint gave the cluster." and
   then each endpoint with its own answer. After a Stop it says "Stopped before trying the other
   endpoints." first. A list entry that cannot be an endpoint is not sent: its line says "it is not an
   endpoint, so it was not tried."
6. When one gives the cluster, the page shows the Talos version, the contract and the members, and
   asks for the cluster's name and its Kubernetes endpoint. The button **Connect a cluster** records
   the cluster, supplies its first key and imports each machine into one draft.

**When the list moves on, and when it stops.** An endpoint that does not answer, or whose node is no
control plane, moves on to the next. An answer that every endpoint would give the same way stops the
list: a first key the server refuses, a malformed request, a cluster with too many members, or a lost
sign-in. The server answers a first key that every node refuses as it answers an endpoint that does not
answer. So such a key is tried at each endpoint, and the page adds "Every node that refuses the first
key gives the same answer as for an endpoint that does not answer, so read each answer above."

**How long it takes.** The page states no wait, because it depends on how an endpoint fails.

| What the endpoint does | On the page | In the command |
|---|---|---|
| Does not answer | about 10 seconds each, so three take about 30 seconds | up to about 15 seconds each |
| Answers and then stalls, or its name lookup stalls | up to 60 seconds each, so three take up to 3 minutes | not stated here |

The page sends a try from a list once. An endpoint you give, or a context that lists only one, is
repeated up to four times, after 1, 2 and 4 seconds. One such endpoint then takes about 47 seconds
when it does not answer, and up to 4 minutes 7 seconds when it stalls.

The steps that run after the cluster is read are not written here yet.

Next: [edit fragments, profiles and assignments](editing.md).
