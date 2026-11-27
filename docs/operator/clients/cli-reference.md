# bronzewardctl command reference

This page is generated from the command table of `bronzewardctl`: `mise run generate` writes it and `mise run go` fails
when it is stale. Edit `cmd/bronzewardctl/commandtable.go`, never this page. Each section is what `bronzewardctl help`
prints for that command.

## Index

```text
usage: bronzewardctl [global flags] <command> [arguments]

commands:
  help         this index, a group's leaves, or one command's flags, notes and roles
  completion   print the shell completion script for bash, zsh or fish
  context      list, use, add, remove
  version      this client's version, the server's and its API revision
  whoami       the identity, roles and epoch the server sees
  login        sign in to an oidc context
  logout       remove an oidc context's stored sign-in
  status       start mode, recovery mode, open drift and unresolved operations
  cluster      list, show, add, discover, first-key, onboard
  machine      list, show, timeline, observations, add, set-endpoint, rename, freeze, unfreeze
  drift        show, adopt, revert
  fragment     show, history, set, rm, adopt, detach
  profile      show, history, set, rm, adopt, detach
  assignment   show, history, set, rm
  draft        list, show, create, discard
  import       start, status, review, mark, continue, takeover, abandon
  import-base  document
  publish      publish a draft as a release
  release      list, show, review, provenance
  plan         list, show, create, cancel
  apply        apply a release to the machines you choose
  approve      approve a plan
  approval     show, revoke
  operation    list, show, watch
  act          list
  recovery     status, enter, exit, scope-account, account, takeover, resolve, mark, release
  identity     revoke
Run bronzewardctl help <command> for a command's flags, notes and roles, and bronzewardctl help global for the global flags and the exit codes.
```

## Global flags and exit codes

```text
global flags, before or after the command:
  --context <name>      else BRONZEWARD_CONTEXT, else the current context
  -o table|json|yaml    json and yaml print the API resource unchanged, but for drift adopt, which
                        prints one object for its steps
  --token-file <path>   the automation token, from an owner-only file; else the context's
                        automation.tokenFile, else the path in BRONZEWARD_TOKEN_FILE
  --token-stdin         the automation token, from standard input
  --no-input            never prompt
  -v                    name a proxy a request goes through

lists, timeline, observations and history read one page: --limit <n>, --cursor <next>; --all reads every
page, which -o json and yaml print as one {"items": [...]}; the draft and release filters apply to the items read.

exit codes: 0 success; 1 refused or failed; 2 usage error, refused repeat or approval not
confirmed; 3 outcome unknown, or an import's lease lapsed while its operation runs;
4 a waited-for operation ended failed, rejected or cancelled; 5 operation watch's --timeout
ended with the operation not ended; 6 the output cannot be written (a full disk, say), unless
a code above applies or the server refused the request (1); 130 a read command or operation watch stopped by Ctrl-C
```

## help

```text
usage: bronzewardctl help [<command>]

this index, a group's leaves, or one command's flags, notes and roles.

help with no argument prints the index; help global prints the global flags and the exit codes;
help <group> lists the group's commands; help <group> <command> is the same text as
bronzewardctl <group> <command> --help.

Roles: any.

Global flags and exit codes: bronzewardctl help global.
```

## completion

```text
usage: bronzewardctl completion bash|zsh|fish

print the shell completion script for bash, zsh or fish.

reads no server and no context. bash: source <(bronzewardctl completion bash). zsh: bronzewardctl completion zsh >
"${fpath[1]}/_bronzewardctl". fish: bronzewardctl completion fish | source. The script lists the commands, each group's
commands and each command's flags, as help shows them.

Roles: any.

Global flags and exit codes: bronzewardctl help global.
```

## context

```text
usage: bronzewardctl [global flags] context <command> [arguments]

the API servers this client knows and the sign-in each uses

  context list                list the contexts
  context use                 make a context the current one
  context add                 add a context
  context remove              remove a context

Run bronzewardctl help context <command> for a command's flags, notes and roles.
```

### context list

```text
usage: bronzewardctl context list

list the contexts.

reads the client's own configuration; no server is asked.

Roles: any.

Global flags and exit codes: bronzewardctl help global.
```

### context use

```text
usage: bronzewardctl context use <name>

make a context the current one.

Roles: any.

Global flags and exit codes: bronzewardctl help global.
```

### context add

```text
usage: bronzewardctl context add <name> --server <url> [--certificate-authority <file>] [--automation-token-file <path>]
       bronzewardctl context add <name> --server <url> --issuer <url> --client-id <id> --audience <aud> [--scopes a,b]
                     [--oidc-certificate-authority <file>] [--oidc-token-file <path>]
       bronzewardctl context add <name> --from <url> [--server <url>] [--certificate-authority <file>] [--scopes a,b]
                     [--oidc-certificate-authority <file>] [--oidc-token-file <path>]

add a context.

--from reads the issuer, audience, CLI client ID and scopes from <url>/bronzeward-client.json.

Roles: any.

Flags: --audience, --automation-token-file, --certificate-authority, --client-id, --credential, --from, --issuer, --oidc-certificate-authority, --oidc-token-file, --scopes, --server

Global flags and exit codes: bronzewardctl help global.
```

### context remove

```text
usage: bronzewardctl context remove <name>

remove a context.

Roles: any.

Global flags and exit codes: bronzewardctl help global.
```

## version

```text
usage: bronzewardctl version

this client's version, the server's and its API revision.

Roles: any.

Global flags and exit codes: bronzewardctl help global.
```

## whoami

```text
usage: bronzewardctl whoami

the identity, roles and epoch the server sees.

also shows this client's reading of a sign-in.

Roles: any.

Global flags and exit codes: bronzewardctl help global.
```

## login

```text
usage: bronzewardctl login [--device]

sign in to an oidc context.

signs in in this host's browser, or with --device on any device.

Roles: any.

Flags: --device

Global flags and exit codes: bronzewardctl help global.
```

## logout

```text
usage: bronzewardctl logout

remove an oidc context's stored sign-in.

also revokes its refresh token.

Roles: any.

Global flags and exit codes: bronzewardctl help global.
```

## status

```text
usage: bronzewardctl status

start mode, recovery mode, open drift and unresolved operations.

Roles: any.

Global flags and exit codes: bronzewardctl help global.
```

## cluster

```text
usage: bronzewardctl [global flags] cluster <command> [arguments]

clusters Bronzeward manages

  cluster list                list the clusters
  cluster show                show one cluster
  cluster add                 record a cluster
  cluster discover            read a cluster, print the commands that record it
  cluster first-key           supply or replace a cluster's first key
  cluster onboard             discover a cluster, record it and its machines, supply its first key, import each machine

Run bronzewardctl help cluster <command> for a command's flags, notes and roles.
```

### cluster list

```text
usage: bronzewardctl cluster list

list the clusters.

Roles: any.

Flags: --all, --cursor, --limit

Global flags and exit codes: bronzewardctl help global.
```

### cluster show

```text
usage: bronzewardctl cluster show <cluster>

show one cluster.

Roles: any.

Global flags and exit codes: bronzewardctl help global.
```

### cluster add

```text
usage: bronzewardctl cluster add --name <name> --endpoint <https url> --contract <v1.x> --talos-cluster-id <id>

record a cluster.

--endpoint is the cluster's endpoint, an https URL. --talos-cluster-id is the Talos cluster ID the nodes report, not
Bronzeward's cluster ID (cl_...), which this command prints.

Roles: author. Needs a person signed in, not an automation token.

Flags: --contract, --endpoint, --expect-epoch, --expect-principal, --idempotency-key, --name, --talos-cluster-id

Global flags and exit codes: bronzewardctl help global.
```

### cluster discover

```text
usage: bronzewardctl cluster discover --talosconfig <file> [--talos-context <name>] [--endpoint <host|ip[:port]>]

read a cluster, print the commands that record it.

reads the Talos nodes with your talosconfig and prints the commands that record the cluster; it writes nothing.
with no --endpoint, the context's endpoints are tried in order until one gives the cluster; a silent one costs up to
about 15 seconds, so three silent endpoints about 45. --endpoint is the only one tried.

Roles: any.

Flags: --endpoint, --talos-context, --talosconfig

Global flags and exit codes: bronzewardctl help global.
```

### cluster first-key

```text
usage: bronzewardctl cluster first-key --cluster <cluster> --kind talosconfig|machine-config|secrets-bundle --file <file>
                     --endpoint <ip[:port]> [--talos-context <name>]

supply or replace a cluster's first key.

one request, tried at the endpoint before anything is stored; a Ctrl-C while it runs leaves the outcome unknown (exit 3).

Roles: author. Needs a person signed in, not an automation token.

Flags: --cluster, --endpoint, --expect-epoch, --expect-principal, --file, --idempotency-key, --kind, --talos-context

Global flags and exit codes: bronzewardctl help global.
```

### cluster onboard

```text
usage: bronzewardctl cluster onboard --talosconfig <file> --name <name> --kubernetes-endpoint <https url>|proposed
                     [--first-key-kind machine-config|secrets-bundle --first-key-file <file> | --skip-first-key]
                     [--talos-context <name>] [--endpoint <host|ip[:port]>] [--staging encrypted|transient]
                     [--review] [--no-import]

discover a cluster, record it and its machines, supply its first key, import each machine.

discovers the cluster, records it and its machines, supplies its first key (the talosconfig unless --first-key-* names
another document), then imports each machine into one draft. Run it again after a failure: what exists is found, the
first key is supplied again (replacing it), and --skip-first-key skips it.
with no --endpoint, the context's endpoints are tried in order until one gives the cluster; a silent one costs up to
about 15 seconds, so three silent endpoints about 45. --endpoint is the only one tried.

Roles: author. Needs a person signed in, not an automation token.

Flags: --endpoint, --first-key-file, --first-key-kind, --kubernetes-endpoint, --name, --no-import, --review, --skip-first-key, --staging, --talos-context, --talosconfig

Global flags and exit codes: bronzewardctl help global.
```

## machine

```text
usage: bronzewardctl [global flags] machine <command> [arguments]

the machines of a cluster

  machine list                list the machines
  machine show                show one machine
  machine timeline            a machine's history
  machine observations        what Bronzeward last read from a machine
  machine add                 record a machine in a cluster
  machine set-endpoint        change a machine's Talos endpoint
  machine rename              name a machine
  machine freeze              stop new commitments and attempts on a machine
  machine unfreeze            lift a machine's freeze

Run bronzewardctl help machine <command> for a command's flags, notes and roles.
```

### machine list

```text
usage: bronzewardctl machine list [--cluster <cluster>]

list the machines.

Roles: any.

Flags: --all, --cluster, --cursor, --limit

Global flags and exit codes: bronzewardctl help global.
```

### machine show

```text
usage: bronzewardctl machine show <machine> [--cluster <cluster>]

show one machine.

<machine> is a machine's name or its ID (mch_...). A name that two clusters hold needs --cluster, a cluster's name or ID, or the ID.

Roles: any.

Flags: --cluster

Global flags and exit codes: bronzewardctl help global.
```

### machine timeline

```text
usage: bronzewardctl machine timeline <machine> [--cluster <cluster>] [--follow] [--limit <n>]

a machine's history.

<machine> is a machine's name or its ID (mch_...). A name that two clusters hold needs --cluster, a cluster's name or ID, or the ID.
with --follow: the newest entries, then each new one as the server records it, read every 5s; Ctrl-C stops it with
exit 130; -o json prints one entry per line, -o yaml one document per entry.

Roles: any.

Flags: --all, --cluster, --cursor, --follow, --limit

Global flags and exit codes: bronzewardctl help global.
```

### machine observations

```text
usage: bronzewardctl machine observations <machine> [--cluster <cluster>]

what Bronzeward last read from a machine.

<machine> is a machine's name or its ID (mch_...). A name that two clusters hold needs --cluster, a cluster's name or ID, or the ID.

Roles: any.

Flags: --all, --cluster, --cursor, --limit

Global flags and exit codes: bronzewardctl help global.
```

### machine add

```text
usage: bronzewardctl machine add --cluster <cluster> (--talos-node-id <id> | --smbios-uuid <uuid>) [--serial <s>]
                     --platform metal|container|cloud --talos-endpoint <ip>[:<port>] [--name <name>]

record a machine in a cluster.

--cluster is Bronzeward's cluster ID (cl_...), not the Talos cluster ID.
Choose how the machine is recorded: if the node reports an SMBIOS UUID (read it with `talosctl get systeminformation`),
use --smbios-uuid. If it reports none, as a container node does, use --talos-node-id (read it with `talosctl get
nodeidentity`). A machine recorded by node ID must report no SMBIOS UUID, so a node that reports one and is recorded by
node ID is refused later with machine-identity-mismatch. `bronzewardctl cluster discover` prints the right flag for each
machine it reads.
A write repeats with --idempotency-key, --expect-epoch and --expect-principal exactly as printed when its outcome was unknown.

Roles: author. Needs a person signed in, not an automation token.

Flags: --cluster, --expect-epoch, --expect-principal, --idempotency-key, --name, --platform, --serial, --smbios-uuid, --talos-endpoint, --talos-node-id

Global flags and exit codes: bronzewardctl help global.
```

### machine set-endpoint

```text
usage: bronzewardctl machine set-endpoint <machine> --talos-endpoint <ip>[:<port>] [--cluster <cluster>]

change a machine's Talos endpoint.

<machine> is a machine's name or its ID (mch_...). A name that two clusters hold needs --cluster, a cluster's name or ID, or the ID.
repeat a write whose outcome was unknown exactly with --idempotency-key, --expect-epoch and --expect-principal as printed.

Roles: author. Needs a person signed in, not an automation token.

Flags: --cluster, --expect-epoch, --expect-principal, --idempotency-key, --talos-endpoint

Global flags and exit codes: bronzewardctl help global.
```

### machine rename

```text
usage: bronzewardctl machine rename <machine> --name <name> [--cluster <cluster>]

name a machine.

<machine> is a machine's name or its ID (mch_...). A name that two clusters hold needs --cluster, a cluster's name or ID, or the ID.
repeat a write whose outcome was unknown exactly with --idempotency-key, --expect-epoch and --expect-principal as printed.

Roles: author. Needs a person signed in, not an automation token.

Flags: --cluster, --expect-epoch, --expect-principal, --idempotency-key, --name

Global flags and exit codes: bronzewardctl help global.
```

### machine freeze

```text
usage: bronzewardctl machine freeze <machine> --reason <text> [--cluster <cluster>]

stop new commitments and attempts on a machine.

<machine> is a machine's name or its ID (mch_...). A name that two clusters hold needs --cluster, a cluster's name or ID, or the ID.
a freeze stops new commitments and attempts, not an attempt already recorded, and an adoption can still be recorded.

Roles: author, publisher, approver or recovery-admin.

Flags: --cluster, --expect-epoch, --expect-principal, --idempotency-key, --reason

Global flags and exit codes: bronzewardctl help global.
```

### machine unfreeze

```text
usage: bronzewardctl machine unfreeze <machine> --reason <text> [--cluster <cluster>]

lift a machine's freeze.

<machine> is a machine's name or its ID (mch_...). A name that two clusters hold needs --cluster, a cluster's name or ID, or the ID.
a server started for recovery, or a scope still pre-restore unaccounted, refuses an unfreeze.

Roles: approver.

Flags: --cluster, --expect-epoch, --expect-principal, --idempotency-key, --reason

Global flags and exit codes: bronzewardctl help global.
```

## drift

```text
usage: bronzewardctl [global flags] drift <command> [arguments]

a machine that no longer reports what Bronzeward applied

  drift show                  the open drift of a machine
  drift adopt                 adopt the drift into a release
  drift revert                revert a machine to Desired

Run bronzewardctl help drift <command> for a command's flags, notes and roles.
```

### drift show

```text
usage: bronzewardctl drift show <machine> [--cluster <cluster>]

the open drift of a machine.

<machine> is a machine's name or its ID (mch_...). A name that two clusters hold needs --cluster, a cluster's name or ID, or the ID.
shows its observations, late landing and the choices; the time shown is that of the observation, the API serves no
opening time. There is no ignore command.

Roles: any.

Flags: --cluster

Global flags and exit codes: bronzewardctl help global.
```

### drift adopt

```text
usage: bronzewardctl drift adopt <machine> --draft <draft> [--cluster <cluster>] [--review] [--staging encrypted|transient] [--marks]

adopt the drift into a release.

<machine> is a machine's name or its ID (mch_...). A name that two clusters hold needs --cluster, a cluster's name or ID, or the ID.
imports the machine's configuration as a drift adoption (author, a person), publishes it (publisher), then creates an
adopt plan (publisher). It stops at a missing role or a pause and prints the rest; it never approves. Its repeat flags
repeat the import; -o json and yaml print one object with the state of each step instead of the API resources.

Roles: author, then publisher, then approver. Needs a person signed in, not an automation token.

Flags: --cluster, --draft, --expect-epoch, --expect-principal, --idempotency-key, --if-match, --marks, --review, --staging

Global flags and exit codes: bronzewardctl help global.
```

### drift revert

```text
usage: bronzewardctl drift revert <machine> [--cluster <cluster>]

revert a machine to Desired.

<machine> is a machine's name or its ID (mch_...). A name that two clusters hold needs --cluster, a cluster's name or ID, or the ID.
creates an apply-config plan of Desired, bound to the open drift record. It reads the machine first and takes no repeat
flag; a machine with no Applied configuration is pointed at an adopt plan, and nothing is sent.

Roles: publisher.

Flags: --cluster

Global flags and exit codes: bronzewardctl help global.
```

## fragment

```text
usage: bronzewardctl [global flags] fragment <command> [arguments]

configuration fragments

  fragment show               show a fragment head or revision
  fragment history            a fragment's revisions
  fragment set                set a fragment in a draft
  fragment rm                 remove a fragment from a draft
  fragment adopt              adopt a shared fragment in a draft
  fragment detach             copy an adopted fragment into the cluster and stop following it

Run bronzewardctl help fragment <command> for a command's flags, notes and roles.
```

### fragment show

```text
usage: bronzewardctl fragment show <head or revision ID>

show a fragment head or revision.

Roles: any.

Global flags and exit codes: bronzewardctl help global.
```

### fragment history

```text
usage: bronzewardctl fragment history <head ID>

a fragment's revisions.

Roles: any.

Flags: --all, --cursor, --limit

Global flags and exit codes: bronzewardctl help global.
```

### fragment set

```text
usage: bronzewardctl fragment set <name> --draft <draft> --layer <layer> --file <path or -> [--declarations <path>]
                     [--marks] [--library] [--ingest-wait <duration>]

set a fragment in a draft.

the document comes from a file, or - for standard input; --marks reads one mark per line from a prompt or standard input
until an empty line. fragment set runs on Linux only. A repeat waits while the first request still ingests, for
--ingest-wait (2m) or the server's claim lease, whichever is longer.
--library writes a shared entry instead of the cluster's own: publishing the draft moves this cluster to it and no other cluster.
A shared document holds no secret reference, so a fragment with a secret stays in one cluster.
A write to a draft sends the ETag of the last draft show or write of that draft, or --if-match <etag>; if the draft
changed since, it saves nothing and says so. Draft writes take author; repeat one with the outcome-unknown block's flags,
--if-match included.

Roles: author.

Flags: --declarations, --draft, --expect-epoch, --expect-principal, --file, --idempotency-key, --if-match, --ingest-wait, --layer, --library, --marks

Global flags and exit codes: bronzewardctl help global.
```

### fragment rm

```text
usage: bronzewardctl fragment rm <name> --draft <draft> [--library]

remove a fragment from a draft.

--library removes the shared fragment's head; no cluster loses it by that, each keeps its adoption.
A write to a draft sends the ETag of the last draft show or write of that draft, or --if-match <etag>; if the draft
changed since, it saves nothing and says so. Draft writes take author; repeat one with the outcome-unknown block's flags,
--if-match included.

Roles: author.

Flags: --draft, --expect-epoch, --expect-principal, --idempotency-key, --if-match, --library

Global flags and exit codes: bronzewardctl help global.
```

### fragment adopt

```text
usage: bronzewardctl fragment adopt <name> --draft <draft> [--revision <frv_ ID>]
       bronzewardctl fragment adopt <name> --draft <draft> --remove

adopt a shared fragment in a draft.

the draft's cluster takes the shared fragment at --revision, the head's revision when none is given, and prints the machines it
reaches. --remove stops following it, and the cluster loses the name unless it holds its own. A profile of this cluster that pins the name keeps its pin: set it again to use the new revision.
A write to a draft sends the ETag of the last draft show or write of that draft, or --if-match <etag>; if the draft
changed since, it saves nothing and says so. Draft writes take author; repeat one with the outcome-unknown block's flags,
--if-match included.

Roles: author.

Flags: --draft, --expect-epoch, --expect-principal, --idempotency-key, --if-match, --remove, --revision

Global flags and exit codes: bronzewardctl help global.
```

### fragment detach

```text
usage: bronzewardctl fragment detach <name> --draft <draft>

copy an adopted fragment into the cluster and stop following it.

one step of the draft: the adopted revision becomes the cluster's own fragment of that name.
A write to a draft sends the ETag of the last draft show or write of that draft, or --if-match <etag>; if the draft
changed since, it saves nothing and says so. Draft writes take author; repeat one with the outcome-unknown block's flags,
--if-match included.

Roles: author.

Flags: --draft, --expect-epoch, --expect-principal, --idempotency-key, --if-match

Global flags and exit codes: bronzewardctl help global.
```

## profile

```text
usage: bronzewardctl [global flags] profile <command> [arguments]

profiles of fragments

  profile show                show a profile head or revision
  profile history             a profile's revisions
  profile set                 set a profile in a draft
  profile rm                  remove a profile from a draft
  profile adopt               adopt a shared profile in a draft
  profile detach              copy an adopted profile into the cluster and stop following it

Run bronzewardctl help profile <command> for a command's flags, notes and roles.
```

### profile show

```text
usage: bronzewardctl profile show <head or revision ID>

show a profile head or revision.

Roles: any.

Global flags and exit codes: bronzewardctl help global.
```

### profile history

```text
usage: bronzewardctl profile history <head ID>

a profile's revisions.

Roles: any.

Flags: --all, --cursor, --limit

Global flags and exit codes: bronzewardctl help global.
```

### profile set

```text
usage: bronzewardctl profile set <name> --draft <draft> --fragments <name or frv_ ID>[,...] [--library]

set a profile in a draft.

a fragment name pins the revision this cluster composes for it: the draft's own or adopted entry, else the cluster's own
head revision, else the revision the cluster adopted. With --library a name pins the shared fragment's revision instead.
--library writes a shared entry instead of the cluster's own: publishing the draft moves this cluster to it and no other cluster.
A shared document holds no secret reference, so a fragment with a secret stays in one cluster.
A write to a draft sends the ETag of the last draft show or write of that draft, or --if-match <etag>; if the draft
changed since, it saves nothing and says so. Draft writes take author; repeat one with the outcome-unknown block's flags,
--if-match included.

Roles: author.

Flags: --draft, --expect-epoch, --expect-principal, --fragments, --idempotency-key, --if-match, --library

Global flags and exit codes: bronzewardctl help global.
```

### profile rm

```text
usage: bronzewardctl profile rm <name> --draft <draft> [--library]

remove a profile from a draft.

--library removes the shared profile's head; no cluster loses it by that, each keeps its adoption.
A write to a draft sends the ETag of the last draft show or write of that draft, or --if-match <etag>; if the draft
changed since, it saves nothing and says so. Draft writes take author; repeat one with the outcome-unknown block's flags,
--if-match included.

Roles: author.

Flags: --draft, --expect-epoch, --expect-principal, --idempotency-key, --if-match, --library

Global flags and exit codes: bronzewardctl help global.
```

### profile adopt

```text
usage: bronzewardctl profile adopt <name> --draft <draft> [--revision <prv_ ID>]
       bronzewardctl profile adopt <name> --draft <draft> --remove

adopt a shared profile in a draft.

the draft's cluster takes the shared profile at --revision, the head's revision when none is given, and prints the machines it
reaches. --remove stops following it. A profile of this cluster that pins the name keeps its pin: set it again to use the new revision.
A write to a draft sends the ETag of the last draft show or write of that draft, or --if-match <etag>; if the draft
changed since, it saves nothing and says so. Draft writes take author; repeat one with the outcome-unknown block's flags,
--if-match included.

Roles: author.

Flags: --draft, --expect-epoch, --expect-principal, --idempotency-key, --if-match, --remove, --revision

Global flags and exit codes: bronzewardctl help global.
```

### profile detach

```text
usage: bronzewardctl profile detach <name> --draft <draft>

copy an adopted profile into the cluster and stop following it.

one step of the draft: the adopted revision becomes the cluster's own profile of that name.
A write to a draft sends the ETag of the last draft show or write of that draft, or --if-match <etag>; if the draft
changed since, it saves nothing and says so. Draft writes take author; repeat one with the outcome-unknown block's flags,
--if-match included.

Roles: author.

Flags: --draft, --expect-epoch, --expect-principal, --idempotency-key, --if-match

Global flags and exit codes: bronzewardctl help global.
```

## assignment

```text
usage: bronzewardctl [global flags] assignment <command> [arguments]

what configuration each machine is assigned

  assignment show             show an assignment head or revision
  assignment history          an assignment's revisions
  assignment set              assign configuration to a machine in a draft
  assignment rm               remove a machine's assignment from a draft

Run bronzewardctl help assignment <command> for a command's flags, notes and roles.
```

### assignment show

```text
usage: bronzewardctl assignment show <head or revision ID>

show an assignment head or revision.

Roles: any.

Global flags and exit codes: bronzewardctl help global.
```

### assignment history

```text
usage: bronzewardctl assignment history <head ID>

an assignment's revisions.

Roles: any.

Flags: --all, --cursor, --limit

Global flags and exit codes: bronzewardctl help global.
```

### assignment set

```text
usage: bronzewardctl assignment set <machine> --draft <draft> [--cluster <cluster>] [--profiles <name>[,...]]
                     [--fragment <layer>=<name>[,...]]...

assign configuration to a machine in a draft.

<machine> is a machine's name or its ID (mch_...). A name that two clusters hold needs --cluster, a cluster's name or ID, or the ID.
the layer is global, site, cluster, role, workload or override. --none instead selects nothing, so the machine's
configuration is its import base, with its own name as its hostname where the base sets none (compilation §6.1 decision 2).
A write to a draft sends the ETag of the last draft show or write of that draft, or --if-match <etag>; if the draft
changed since, it saves nothing and says so. Draft writes take author; repeat one with the outcome-unknown block's flags,
--if-match included.

Roles: author.

Flags: --cluster, --draft, --expect-epoch, --expect-principal, --fragment, --idempotency-key, --if-match, --none, --profiles

Global flags and exit codes: bronzewardctl help global.
```

### assignment rm

```text
usage: bronzewardctl assignment rm <machine> --draft <draft> [--cluster <cluster>]

remove a machine's assignment from a draft.

<machine> is a machine's name or its ID (mch_...). A name that two clusters hold needs --cluster, a cluster's name or ID, or the ID.
A write to a draft sends the ETag of the last draft show or write of that draft, or --if-match <etag>; if the draft
changed since, it saves nothing and says so. Draft writes take author; repeat one with the outcome-unknown block's flags,
--if-match included.

Roles: author.

Flags: --cluster, --draft, --expect-epoch, --expect-principal, --idempotency-key, --if-match

Global flags and exit codes: bronzewardctl help global.
```

## draft

```text
usage: bronzewardctl [global flags] draft <command> [arguments]

drafts: changes gathered before a publish

  draft list                  list the drafts
  draft show                  show a draft
  draft create                start a draft
  draft discard               discard a draft

Run bronzewardctl help draft <command> for a command's flags, notes and roles.
```

### draft list

```text
usage: bronzewardctl draft list [--cluster <cluster>]

list the drafts.

Roles: any.

Flags: --all, --cluster, --cursor, --limit

Global flags and exit codes: bronzewardctl help global.
```

### draft show

```text
usage: bronzewardctl draft show <draft>

show a draft.

Roles: any.

Global flags and exit codes: bronzewardctl help global.
```

### draft create

```text
usage: bronzewardctl draft create --cluster <cluster> --title <title>

start a draft.

Roles: author.

Flags: --cluster, --expect-epoch, --expect-principal, --idempotency-key, --title

Global flags and exit codes: bronzewardctl help global.
```

### draft discard

```text
usage: bronzewardctl draft discard <draft>

discard a draft.

A write to a draft sends the ETag of the last draft show or write of that draft, or --if-match <etag>; if the draft
changed since, it saves nothing and says so. Draft writes take author; repeat one with the outcome-unknown block's flags,
--if-match included.

Roles: author.

Flags: --expect-epoch, --expect-principal, --idempotency-key, --if-match

Global flags and exit codes: bronzewardctl help global.
```

## import

```text
usage: bronzewardctl [global flags] import <command> [arguments]

imports of a machine's configuration into a draft

  import start                import a machine's configuration into a draft
  import status               one import's state, or a list
  import review               read a paused import's staged document
  import mark                 mark values as secret
  import continue             continue an import paused for review
  import takeover             take over an import whose lease lapsed
  import abandon              abandon an import

Run bronzewardctl help import <command> for a command's flags, notes and roles.
```

### import start

```text
usage: bronzewardctl import start --machine <machine> --draft <draft> [--cluster <cluster>] [--file <path or ->] [--staging encrypted|transient]
                     [--review] [--marks] [--no-wait]

import a machine's configuration into a draft.

--machine is a machine's name or its ID (mch_...). A name that two clusters hold needs --cluster, a cluster's name or ID, or the ID.
imports the machine's configuration, or with --file a document, into the draft, sending its kept ETag or --if-match. It
waits until the import pauses for review or ends.

Roles: author. Needs a person signed in, not an automation token.

Flags: --cluster, --draft, --expect-epoch, --expect-principal, --file, --idempotency-key, --if-match, --machine, --marks, --no-wait, --review, --staging

Global flags and exit codes: bronzewardctl help global.
```

### import status

```text
usage: bronzewardctl import status [<ingestion>]
       bronzewardctl import status [--state <s>] [--machine <machine>] [--cluster <cluster>]

one import's state, or a list.

--machine is a machine's name or its ID (mch_...); --cluster narrows a name two clusters hold.
one import's state, lease and next steps, or a list: --state paused or held finds imports to continue or take over.

Roles: any.

Flags: --all, --cluster, --cursor, --limit, --machine, --state

Global flags and exit codes: bronzewardctl help global.
```

### import review

```text
usage: bronzewardctl import review <ingestion>

read a paused import's staged document.

only on a terminal, in a pager (space or f forward, b back, q quit) that writes nothing else; then marks from the prompt.

Roles: author. Needs a person signed in, not an automation token.

Global flags and exit codes: bronzewardctl help global.
```

### import mark

```text
usage: bronzewardctl import mark <ingestion> [--no-wait]

mark values as secret.

marks values as secret by their paths, one per line from the prompt or standard input, never an argument. It waits until the
import pauses again.

Roles: author. Needs a person signed in, not an automation token.

Flags: --expect-epoch, --expect-principal, --idempotency-key, --no-wait

Global flags and exit codes: bronzewardctl help global.
```

### import continue

```text
usage: bronzewardctl import continue <ingestion> [--no-wait]

continue an import paused for review.

waits until the import pauses again or ends.

Roles: author. Needs a person signed in, not an automation token.

Flags: --expect-epoch, --expect-principal, --idempotency-key, --no-wait

Global flags and exit codes: bronzewardctl help global.
```

### import takeover

```text
usage: bronzewardctl import takeover <ingestion> [--no-wait]

take over an import whose lease lapsed.

waits until the import pauses again or ends.

Roles: author. Needs a person signed in, not an automation token.

Flags: --expect-epoch, --expect-principal, --idempotency-key, --no-wait

Global flags and exit codes: bronzewardctl help global.
```

### import abandon

```text
usage: bronzewardctl import abandon <ingestion>

abandon an import.

Roles: author. Needs a person signed in, not an automation token.

Flags: --expect-epoch, --expect-principal, --idempotency-key

Global flags and exit codes: bronzewardctl help global.
```

## import-base

```text
usage: bronzewardctl [global flags] import-base <command> [arguments]

the stored configuration an import started from

  import-base document        the stored document of an import base

Run bronzewardctl help import-base <command> for a command's flags, notes and roles.
```

### import-base document

```text
usage: bronzewardctl import-base document <import base revision>

the stored document of an import base.

any author may read it for an import read from a node; for a file import only the author who made it and continued its
review. Only on a terminal, in the same pager as import review; no marks prompt.

Roles: author. Needs a person signed in, not an automation token.

Global flags and exit codes: bronzewardctl help global.
```

## publish

```text
usage: bronzewardctl publish <draft> [--no-wait]

publish a draft as a release.

sends the draft's kept ETag or --if-match, waits for the publication and names the release and each machine's plan create
command. Nothing is sent to any machine.

Roles: publisher.

Flags: --expect-epoch, --expect-principal, --idempotency-key, --if-match, --no-wait

Global flags and exit codes: bronzewardctl help global.
```

## release

```text
usage: bronzewardctl [global flags] release <command> [arguments]

releases: published configuration

  release list                list the releases
  release show                show a release
  release review              a machine's rendered, redacted configuration in a release
  release provenance          the secret references a machine's configuration was built from

Run bronzewardctl help release <command> for a command's flags, notes and roles.
```

### release list

```text
usage: bronzewardctl release list [--cluster <cluster>] [--draft <draft>]

list the releases.

Roles: any.

Flags: --all, --cluster, --cursor, --draft, --limit

Global flags and exit codes: bronzewardctl help global.
```

### release show

```text
usage: bronzewardctl release show <release>

show a release.

Roles: any.

Global flags and exit codes: bronzewardctl help global.
```

### release review

```text
usage: bronzewardctl release review <release> <machine> [--cluster <cluster>]

a machine's rendered, redacted configuration in a release.

<machine> is a machine's name or its ID (mch_...). A name that two clusters hold needs --cluster, a cluster's name or ID, or the ID.

Roles: any.

Flags: --cluster

Global flags and exit codes: bronzewardctl help global.
```

### release provenance

```text
usage: bronzewardctl release provenance <release> <machine> [--cluster <cluster>]

the secret references a machine's configuration was built from.

<machine> is a machine's name or its ID (mch_...). A name that two clusters hold needs --cluster, a cluster's name or ID, or the ID.

Roles: any.

Flags: --cluster

Global flags and exit codes: bronzewardctl help global.
```

## plan

```text
usage: bronzewardctl [global flags] plan <command> [arguments]

plans: what to send to a machine

  plan list                   list the plans
  plan show                   show a plan, its evidence and what can be done next
  plan create                 create a plan for a machine
  plan cancel                 cancel a plan

Run bronzewardctl help plan <command> for a command's flags, notes and roles.
```

### plan list

```text
usage: bronzewardctl plan list [--state <s>] [--machine <machine>] [--cluster <cluster>]

list the plans.

--machine is a machine's name or its ID (mch_...); --cluster narrows a name two clusters hold.

Roles: any.

Flags: --all, --cluster, --cursor, --limit, --machine, --state

Global flags and exit codes: bronzewardctl help global.
```

### plan show

```text
usage: bronzewardctl plan show <plan>

show a plan, its evidence and what can be done next.

Roles: any.

Global flags and exit codes: bronzewardctl help global.
```

### plan create

```text
usage: bronzewardctl plan create --release <release> --machine <machine> [--cluster <cluster>] --operation apply-config|adopt [--mode no-reboot]
                     [--expires-in <d>] [--max-observation-age <d>] [--check-validity <d>]
                     [--transport-deadline <d>] [--verification-deadline <d>] [--max-attempts <n>]

create a plan for a machine.

<machine> is a machine's name or its ID (mch_...). A name that two clusters hold needs --cluster, a cluster's name or ID, or the ID.
a duration is in whole seconds, such as 90s or 24h; an omitted value takes the server's default. An adopt plan takes no
mode, deadline or attempt limit. The plan is not approved and nothing runs until an approver approves.

Roles: publisher.

Flags: --check-validity, --cluster, --expect-epoch, --expect-principal, --expires-in, --idempotency-key, --machine, --max-attempts, --max-observation-age, --mode, --operation, --release, --transport-deadline, --verification-deadline

Global flags and exit codes: bronzewardctl help global.
```

### plan cancel

```text
usage: bronzewardctl plan cancel <plan> --reason <text>

cancel a plan.

a cancelled plan cannot be committed; a committed plan's operation ends only while no attempt is recorded, and an attempt
already recorded is not stopped.

Roles: publisher (its own plan), approver or recovery-admin.

Flags: --expect-epoch, --expect-principal, --idempotency-key, --reason

Global flags and exit codes: bronzewardctl help global.
```

## apply

```text
usage: bronzewardctl apply <release> (--machine <a,b> | --all) [--diff]

apply a release to the machines you choose.

makes one apply-config plan for each machine chosen, by name or ID, or --all of the release's machines, prints each machine with
its role, profiles and plan expiry, and with --diff each plan's change, on standard error, then asks Approve them? [y/N] and approves
every plan listed only on y or yes. A plan whose change cannot be read, that is withheld, or that overwrites a drifted configuration is left out. Enter, n and the end of input send no approval
and cancel the plans this command made; a plan somebody else prepared is approved as it stands and never cancelled here. A Ctrl-C at
the question leaves the plans to expire at the time each shows. A machine already applied, frozen, in drift, in an operation or with
another operation's plan waiting is left out with its reason.
There is no --yes, --no-input refuses, and -o json and -o yaml are not offered. It does not wait for the operations.

Roles: publisher, then approver. Needs a person signed in, not an automation token.

Flags: --all, --diff, --machine

Global flags and exit codes: bronzewardctl help global.
```

## approve

```text
usage: bronzewardctl approve <plan>

approve a plan.

prints the plan and the predicted self-approval on standard error, then asks Approve it? [y/N] and sends only on y or yes,
on the terminal or as one line of standard input. Enter, n and the end of input send nothing. There is no --yes, and
--no-input refuses. An approval starts no operation, so approve does not wait.

Roles: approver. Needs a person signed in, not an automation token.

Flags: --expect-epoch, --expect-principal, --idempotency-key

Global flags and exit codes: bronzewardctl help global.
```

## approval

```text
usage: bronzewardctl [global flags] approval <command> [arguments]

approvals of plans

  approval show               show an approval
  approval revoke             revoke an approval

Run bronzewardctl help approval <command> for a command's flags, notes and roles.
```

### approval show

```text
usage: bronzewardctl approval show <approval>

show an approval.

Roles: any.

Global flags and exit codes: bronzewardctl help global.
```

### approval revoke

```text
usage: bronzewardctl approval revoke <approval> --reason <text>

revoke an approval.

a revocation ends an uncommitted approval and undoes no commitment; an attempt already recorded is not stopped.

Roles: approver or recovery-admin.

Flags: --expect-epoch, --expect-principal, --idempotency-key, --reason

Global flags and exit codes: bronzewardctl help global.
```

## operation

```text
usage: bronzewardctl [global flags] operation <command> [arguments]

operations: what a plan did

  operation list              list the operations
  operation show              show an operation
  operation watch             follow an operation until it ends

Run bronzewardctl help operation <command> for a command's flags, notes and roles.
```

### operation list

```text
usage: bronzewardctl operation list [--plan <id>] [--machine <machine>] [--cluster <cluster>] [--state <s>] [--kind <k>]

list the operations.

--machine is a machine's name or its ID (mch_...); --cluster narrows a name two clusters hold.

Roles: any.

Flags: --all, --cluster, --cursor, --kind, --limit, --machine, --plan, --state

Global flags and exit codes: bronzewardctl help global.
```

### operation show

```text
usage: bronzewardctl operation show <operation>

show an operation.

Roles: any.

Global flags and exit codes: bronzewardctl help global.
```

### operation watch

```text
usage: bronzewardctl operation watch <operation> [--timeout <d>]

follow an operation until it ends.

prints one line per event until the operation ends, then its outcome; it goes on past unresolved. --timeout (30m) ends the
watch, not the operation, with exit 5; Ctrl-C stops only the watch, with exit 130.

Roles: any.

Flags: --timeout

Global flags and exit codes: bronzewardctl help global.
```

## act

```text
usage: bronzewardctl [global flags] act <command> [arguments]

the audit record

  act list                    list the audit record

Run bronzewardctl help act <command> for a command's flags, notes and roles.
```

### act list

```text
usage: bronzewardctl act list

list the audit record.

Roles: any.

Flags: --all, --cursor, --limit

Global flags and exit codes: bronzewardctl help global.
```

## recovery

```text
usage: bronzewardctl [global flags] recovery <command> [arguments]

recovery after a restore

  recovery status             the epoch, recovery mode, entry and exit, and each scope's next step
  recovery enter              enter recovery
  recovery exit               leave recovery
  recovery scope-account      account every unaccounted attempt of a machine after a restore
  recovery account            account one attempt whose response was lost
  recovery takeover           make the serving controller own an operation
  recovery resolve            end an unresolved operation that has no attempt
  recovery mark               record what the evidence supports for a machine
  recovery release            release a ready scope

Run bronzewardctl help recovery <command> for a command's flags, notes and roles.
```

### recovery status

```text
usage: bronzewardctl recovery status

the epoch, recovery mode, entry and exit, and each scope's next step.

Roles: any.

Global flags and exit codes: bronzewardctl help global.
```

### recovery enter

```text
usage: bronzewardctl recovery enter --restored family=<f>,backup=<id>,taken-at=<time> [--restored ...]
                     --stopped-at <time> --reason <text>

enter recovery.

on a server started for recovery; sent only after a y. A repeat of an unknown outcome takes --idempotency-key
and --expect-principal, no epoch. Times are RFC 3339.

Roles: recovery-admin. Needs a person signed in, not an automation token.

Flags: --expect-principal, --idempotency-key, --reason, --restored, --stopped-at

Global flags and exit codes: bronzewardctl help global.
```

### recovery exit

```text
usage: bronzewardctl recovery exit [--fact name=value]...

leave recovery.

once every scope is released.

Roles: recovery-admin. Needs a person signed in, not an automation token.

Flags: --expect-epoch, --expect-principal, --fact, --idempotency-key

Global flags and exit codes: bronzewardctl help global.
```

### recovery scope-account

```text
usage: bronzewardctl recovery scope-account <machine> --start <observation> --end <observation> [--explanation <text>] [--cluster <cluster>]

account every unaccounted attempt of a machine after a restore.

<machine> is a machine's name or its ID (mch_...). A name that two clusters hold needs --cluster, a cluster's name or ID, or the ID.
shows the window and the risk, and is sent only when y is answered to the question that names the machine.

Roles: recovery-admin. Needs a person signed in, not an automation token.

Flags: --cluster, --end, --expect-epoch, --expect-principal, --explanation, --idempotency-key, --start

Global flags and exit codes: bronzewardctl help global.
```

### recovery account

```text
usage: bronzewardctl recovery account <operation> <attempt> --start <observation> --end <observation> [--explanation <text>]

account one attempt whose response was lost.

the same window, risk and question as scope-account.

Roles: recovery-admin. Needs a person signed in, not an automation token.

Flags: --end, --expect-epoch, --expect-principal, --explanation, --idempotency-key, --start

Global flags and exit codes: bronzewardctl help global.
```

### recovery takeover

```text
usage: bronzewardctl recovery takeover <operation> --reason <text>

make the serving controller own an operation.

makes the serving controller the owner of a non-terminal operation; sends nothing to the node. A repeat of an unknown
outcome also takes --generation as printed.

Roles: recovery-admin. Needs a person signed in, not an automation token.

Flags: --expect-epoch, --expect-principal, --generation, --idempotency-key, --reason

Global flags and exit codes: bronzewardctl help global.
```

### recovery resolve

```text
usage: bronzewardctl recovery resolve <operation> --reason <text>

end an unresolved operation that has no attempt.

ends an unresolved operation with no attempt as cancelled and records no remote outcome. A repeat of an unknown outcome
sends the same body without the state checks.

Roles: recovery-admin. Needs a person signed in, not an automation token.

Flags: --expect-epoch, --expect-principal, --idempotency-key, --reason

Global flags and exit codes: bronzewardctl help global.
```

### recovery mark

```text
usage: bronzewardctl recovery mark <machine> [--fact name=value]... [--cluster <cluster>]

record what the evidence supports for a machine.

<machine> is a machine's name or its ID (mch_...). A name that two clusters hold needs --cluster, a cluster's name or ID, or the ID.
records the state the evidence supports: ready, blocked or unresolved.

Roles: recovery-admin. Needs a person signed in, not an automation token.

Flags: --cluster, --expect-epoch, --expect-principal, --fact, --idempotency-key

Global flags and exit codes: bronzewardctl help global.
```

### recovery release

```text
usage: bronzewardctl recovery release <machine> [--fact name=value]... [--cluster <cluster>]

release a ready scope.

<machine> is a machine's name or its ID (mch_...). A name that two clusters hold needs --cluster, a cluster's name or ID, or the ID.
a release is not an approval.

Roles: recovery-admin. Needs a person signed in, not an automation token.

Flags: --cluster, --expect-epoch, --expect-principal, --fact, --idempotency-key

Global flags and exit codes: bronzewardctl help global.
```

## identity

```text
usage: bronzewardctl [global flags] identity <command> [arguments]

identities of people and services

  identity revoke             revoke an identity

Run bronzewardctl help identity <command> for a command's flags, notes and roles.
```

### identity revoke

```text
usage: bronzewardctl identity revoke (--identity <id> | --iss <iss> --sub <sub>) --reason <text>

revoke an identity.

permanent; names the identity (or the subject and its issuer), then sends only after a y.

Roles: recovery-admin. Needs a person signed in, not an automation token.

Flags: --expect-epoch, --expect-principal, --idempotency-key, --identity, --iss, --reason, --sub

Global flags and exit codes: bronzewardctl help global.
```
