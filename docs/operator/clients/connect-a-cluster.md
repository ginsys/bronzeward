# Journey: connect a cluster

**You want** to bring a cluster the installation does not know yet under management, by giving its
talosconfig file.

**You need** the `author` role and a person signed in, not an automation token, and the cluster's
talosconfig file.

The installation reads the cluster's members through one endpoint of that file. This first read only reads: it records nothing.

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
prints one error that names each of them. The `text` block on this page shows the shape of the
output, read from the source. This page does not claim that a run printed it.

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

The page sends a try from a list once. The one exception: a try answered `401 unauthenticated` is
sent once more, after 1 second, when the sign-in renews, and the list then goes on. An endpoint you give, or a context that lists only one, is
repeated up to four times, after 1, 2 and 4 seconds. One such endpoint then takes about 47 seconds
when it does not answer, and up to 4 minutes 7 seconds when it stalls.

The steps that run after the cluster is read are not written here yet.

Next: [read the inventory](inventory.md).
