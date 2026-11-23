package main

import (
	"flag"
	"slices"
	"strings"
)

// The command table: every command group and leaf the client has, with the words of its help, its roles
// and whether it needs a person. Help (`help`, `--help`, a usage error) and the verbs a group accepts read
// it. A leaf's flags are not copied here: help reads them from the flag set the leaf builds in parse, so a
// flag that is renamed in code is renamed in help (docs/spec/operator-experience.md §4 lists the leaves,
// §10 their roles and which need a person).

// cmdEntry is one command. A group (path "machine") holds a purpose line and nothing else; its leaves are the
// entries whose path starts with its name and a space. A leaf holds its usage lines (after "bronzewardctl "),
// its notes, the roles that may run it and whether it needs a person signed in, not an automation token.
type cmdEntry struct {
	path     string
	line     string // the purpose, one line: shown in the index (a group shows its leaves instead) and in a group's help
	synopsis string // the usage lines of a leaf
	notes    string
	roles    string // "any", or the role names as a sentence part
	person   bool
	ifMatch  bool   // the leaf takes --if-match: a write to a draft, or one that sends a draft's kept ETag
	not      string // flags the leaf registers and refuses, by name, space separated: help leaves them out
}

// cmdTable lists the groups and leaves in the order of operator-experience.md §4, each group's leaves
// in the order its dispatcher names its verbs.
var cmdTable = []cmdEntry{
	{path: "help", line: "this index, a group's leaves, or one command's flags, notes and roles",
		synopsis: "help [<command>]", roles: "any",
		notes: "help with no argument prints the index; help global prints the global flags and the exit codes;\n" +
			"help <group> lists the group's commands; help <group> <command> is the same text as\n" +
			"bronzewardctl <group> <command> --help."},
	{path: "completion", line: "print the shell completion script for bash, zsh or fish", synopsis: "completion bash|zsh|fish", roles: "any",
		notes: "reads no server and no context. bash: source <(bronzewardctl completion bash). zsh: bronzewardctl completion zsh >\n" +
			"\"${fpath[1]}/_bronzewardctl\". fish: bronzewardctl completion fish | source. The script lists the commands, each group's\n" +
			"commands and each command's flags, as help shows them."},
	{path: "context", line: "the API servers this client knows and the sign-in each uses"},
	{path: "context list", line: "list the contexts", synopsis: "context list", roles: "any", notes: "reads the client's own configuration; no server is asked."},
	{path: "context use", line: "make a context the current one", synopsis: "context use <name>", roles: "any"},
	{path: "context add", line: "add a context", roles: "any",
		synopsis: "context add <name> --server <url> [--certificate-authority <file>] [--automation-token-file <path>]\n" +
			"context add <name> --server <url> --issuer <url> --client-id <id> [--audience <aud>] [--scopes a,b]\n" +
			"            [--oidc-certificate-authority <file>] [--oidc-token-file <path>]\n" +
			"context add <name> --from <url> [--server <url>] [--certificate-authority <file>] [--scopes a,b]\n" +
			"            [--oidc-certificate-authority <file>] [--oidc-token-file <path>]",
		notes: "--from reads the issuer, audience, CLI client ID and scopes from <url>/bronzeward-client.json."},
	{path: "context remove", line: "remove a context", synopsis: "context remove <name>", roles: "any"},
	{path: "version", line: "this client's version, the server's and its API revision", synopsis: "version", roles: "any"},
	{path: "whoami", line: "the identity, roles and epoch the server sees", synopsis: "whoami", roles: "any",
		notes: "also shows this client's reading of a sign-in."},
	{path: "login", line: "sign in to an oidc context", synopsis: "login [--device]", roles: "any",
		notes: "signs in in this host's browser, or with --device on any device."},
	{path: "logout", line: "remove an oidc context's stored sign-in", synopsis: "logout", roles: "any",
		notes: "also revokes its refresh token."},
	{path: "status", line: "start mode, recovery mode, open drift and unresolved operations", synopsis: "status", roles: "any"},

	{path: "cluster", line: "clusters Bronzeward manages"},
	{path: "cluster list", line: "list the clusters", synopsis: "cluster list", roles: "any"},
	{path: "cluster show", line: "show one cluster", synopsis: "cluster show <cluster>", roles: "any"},
	{path: "cluster add", line: "record a cluster", synopsis: "cluster add --name <name> --endpoint <https url> --contract <v1.x> --talos-cluster-id <id>",
		roles: "author", person: true,
		notes: "--endpoint is the cluster's endpoint, an https URL. --talos-cluster-id is the Talos cluster ID the nodes report, not\n" +
			"Bronzeward's cluster ID (cl_...), which this command prints."},
	{path: "cluster discover", line: "read a cluster, print the commands that record it", roles: "any",
		synopsis: "cluster discover --talosconfig <file> [--talos-context <name>] [--endpoint <host|ip[:port]>]",
		notes: "reads the Talos nodes with your talosconfig and prints the commands that record the cluster; it writes nothing.\n" +
			endpointsNote},
	{path: "cluster first-key", line: "supply or replace a cluster's first key", roles: "author", person: true,
		synopsis: "cluster first-key --cluster <cluster> --kind talosconfig|machine-config|secrets-bundle --file <file>\n" +
			"                  --endpoint <ip[:port]> [--talos-context <name>]",
		notes: "one request, tried at the endpoint before anything is stored; a Ctrl-C while it runs leaves the outcome unknown (exit 3)."},
	{path: "cluster onboard", line: "discover a cluster, record it and its machines, supply its first key, import each machine",
		roles: "author", person: true, not: "no-wait idempotency-key expect-epoch expect-principal",
		synopsis: "cluster onboard --talosconfig <file> --name <name> --kubernetes-endpoint <https url>|proposed\n" +
			"                [--first-key-kind machine-config|secrets-bundle --first-key-file <file> | --skip-first-key]\n" +
			"                [--talos-context <name>] [--endpoint <host|ip[:port]>] [--staging encrypted|transient]\n" +
			"                [--review] [--no-import]",
		notes: "discovers the cluster, records it and its machines, supplies its first key (the talosconfig unless --first-key-* names\n" +
			"another document), then imports each machine into one draft. Run it again after a failure: what exists is found, the\n" +
			"first key is supplied again (replacing it), and --skip-first-key skips it.\n" +
			endpointsNote},

	{path: "machine", line: "the machines of a cluster"},
	{path: "machine list", line: "list the machines", synopsis: "machine list [--cluster <cluster>]", roles: "any"},
	{path: "machine show", line: "show one machine", synopsis: "machine show <machine> [--cluster <cluster>]", roles: "any", notes: machineByNameNote},
	{path: "machine timeline", line: "a machine's history", roles: "any",
		synopsis: "machine timeline <machine> [--cluster <cluster>] [--follow] [--limit <n>]",
		notes: machineByNameNote + "\nwith --follow: the newest entries, then each new one as the server records it, read every 5s; Ctrl-C stops it with\n" +
			"exit 130; -o json prints one entry per line, -o yaml one document per entry."},
	{path: "machine observations", line: "what Bronzeward last read from a machine", synopsis: "machine observations <machine> [--cluster <cluster>]", roles: "any", notes: machineByNameNote},
	{path: "machine add", line: "record a machine in a cluster", roles: "author", person: true,
		synopsis: "machine add --cluster <cluster> (--talos-node-id <id> | --smbios-uuid <uuid>) [--serial <s>]\n" +
			"            --platform metal|container|cloud --talos-endpoint <ip>[:<port>] [--name <name>]",
		notes: "--cluster is Bronzeward's cluster ID (cl_...), not the Talos cluster ID.\n" +
			"Choose how the machine is recorded: if the node reports an SMBIOS UUID (read it with `talosctl get systeminformation`),\n" +
			"use --smbios-uuid. If it reports none, as a container node does, use --talos-node-id (read it with `talosctl get\n" +
			"nodeidentity`). A machine recorded by node ID must report no SMBIOS UUID, so a node that reports one and is recorded by\n" +
			"node ID is refused later with machine-identity-mismatch. `bronzewardctl cluster discover` prints the right flag for each\n" +
			"machine it reads.\n" +
			"A write repeats with --idempotency-key, --expect-epoch and --expect-principal exactly as printed when its outcome was unknown."},
	{path: "machine set-endpoint", line: "change a machine's Talos endpoint", synopsis: "machine set-endpoint <machine> --talos-endpoint <ip>[:<port>] [--cluster <cluster>]",
		roles: "author", person: true,
		notes: machineByNameNote + "\nrepeat a write whose outcome was unknown exactly with --idempotency-key, --expect-epoch and --expect-principal as printed."},
	{path: "machine rename", line: "name a machine", synopsis: "machine rename <machine> --name <name> [--cluster <cluster>]", roles: "author", person: true,
		notes: machineByNameNote + "\nrepeat a write whose outcome was unknown exactly with --idempotency-key, --expect-epoch and --expect-principal as printed."},
	{path: "machine freeze", line: "stop new commitments and attempts on a machine", synopsis: "machine freeze <machine> --reason <text> [--cluster <cluster>]",
		roles: "author, publisher, approver or recovery-admin",
		notes: machineByNameNote + "\na freeze stops new commitments and attempts, not an attempt already recorded, and an adoption can still be recorded."},
	{path: "machine unfreeze", line: "lift a machine's freeze", synopsis: "machine unfreeze <machine> --reason <text> [--cluster <cluster>]", roles: "approver",
		notes: machineByNameNote + "\na server started for recovery, or a scope still pre-restore unaccounted, refuses an unfreeze."},

	{path: "drift", line: "a machine that no longer reports what Bronzeward applied"},
	{path: "drift show", line: "the open drift of a machine", synopsis: "drift show <machine> [--cluster <cluster>]", roles: "any",
		notes: machineByNameNote + "\nshows its observations, late landing and the choices; the time shown is that of the observation, the API serves no\n" +
			"opening time. There is no ignore command."},
	{path: "drift adopt", line: "adopt the drift into a release", roles: "author, then publisher, then approver", person: true, ifMatch: true,
		synopsis: "drift adopt <machine> --draft <draft> [--cluster <cluster>] [--review] [--staging encrypted|transient] [--marks]",
		notes: machineByNameNote + "\nimports the machine's configuration as a drift adoption (author, a person), publishes it (publisher), then creates an\n" +
			"adopt plan (publisher). It stops at a missing role or a pause and prints the rest; it never approves. Its repeat flags\n" +
			"repeat the import; -o json and yaml print one object with the state of each step instead of the API resources."},
	{path: "drift revert", line: "revert a machine to Desired", synopsis: "drift revert <machine> [--cluster <cluster>]", roles: "publisher", not: "idempotency-key expect-epoch expect-principal",
		notes: machineByNameNote + "\ncreates an apply-config plan of Desired, bound to the open drift record. It reads the machine first and takes no repeat\n" +
			"flag; a machine with no Applied configuration is pointed at an adopt plan, and nothing is sent."},

	{path: "fragment", line: "configuration fragments"},
	{path: "fragment show", line: "show a fragment head or revision", synopsis: "fragment show <head or revision ID>", roles: "any", not: "limit cursor all"},
	{path: "fragment history", line: "a fragment's revisions", synopsis: "fragment history <head ID>", roles: "any"},
	{path: "fragment set", line: "set a fragment in a draft", roles: "author",
		synopsis: "fragment set <name> --draft <draft> --layer <layer> --file <path or -> [--declarations <path>]\n" +
			"             [--marks] [--ingest-wait <duration>]",
		ifMatch: true,
		notes: "the document comes from a file, or - for standard input; --marks reads one mark per line from a prompt or standard input\n" +
			"until an empty line. fragment set runs on Linux only. A repeat waits while the first request still ingests, for\n" +
			"--ingest-wait (2m) or the server's claim lease, whichever is longer.\n" + draftWrite},
	{path: "fragment rm", line: "remove a fragment from a draft", synopsis: "fragment rm <name> --draft <draft>", roles: "author", ifMatch: true, notes: draftWrite},

	{path: "profile", line: "profiles of fragments"},
	{path: "profile show", line: "show a profile head or revision", synopsis: "profile show <head or revision ID>", roles: "any", not: "limit cursor all"},
	{path: "profile history", line: "a profile's revisions", synopsis: "profile history <head ID>", roles: "any"},
	{path: "profile set", line: "set a profile in a draft", roles: "author",
		synopsis: "profile set <name> --draft <draft> --fragments <name or frv_ ID>[,...]",
		ifMatch:  true,
		notes:    "a fragment name pins the revision the draft proposes, else the fragment's head revision.\n" + draftWrite},
	{path: "profile rm", line: "remove a profile from a draft", synopsis: "profile rm <name> --draft <draft>", roles: "author", ifMatch: true, notes: draftWrite},

	{path: "assignment", line: "what configuration each machine is assigned"},
	{path: "assignment show", line: "show an assignment head or revision", synopsis: "assignment show <head or revision ID>", roles: "any", not: "limit cursor all"},
	{path: "assignment history", line: "an assignment's revisions", synopsis: "assignment history <head ID>", roles: "any"},
	{path: "assignment set", line: "assign configuration to a machine in a draft", roles: "author",
		synopsis: "assignment set <machine> --draft <draft> [--cluster <cluster>] [--profiles <name>[,...]]\n" +
			"               [--fragment <layer>=<name>[,...]]...",
		ifMatch: true,
		notes: machineByNameNote + "\nthe layer is global, site, cluster, role, workload or override. --none instead selects nothing, so the machine's\n" +
			"configuration is its import base alone.\n" + draftWrite},
	{path: "assignment rm", line: "remove a machine's assignment from a draft", synopsis: "assignment rm <machine> --draft <draft> [--cluster <cluster>]", roles: "author", ifMatch: true, notes: machineByNameNote + "\n" + draftWrite},

	{path: "draft", line: "drafts: changes gathered before a publish"},
	{path: "draft list", line: "list the drafts", synopsis: "draft list [--cluster <cluster>]", roles: "any"},
	{path: "draft show", line: "show a draft", synopsis: "draft show <draft>", roles: "any"},
	{path: "draft create", line: "start a draft", synopsis: "draft create --cluster <cluster> --title <title>", roles: "author"},
	{path: "draft discard", line: "discard a draft", synopsis: "draft discard <draft>", roles: "author", ifMatch: true, notes: draftWrite},

	{path: "import", line: "imports of a machine's configuration into a draft"},
	{path: "import start", line: "import a machine's configuration into a draft", roles: "author", person: true, ifMatch: true,
		synopsis: "import start --machine <machine> --draft <draft> [--cluster <cluster>] [--file <path or ->] [--staging encrypted|transient]\n" +
			"             [--review] [--marks] [--no-wait]",
		notes: "--machine is a machine's name or its ID (mch_...). A name that two clusters hold needs --cluster, a cluster's name or ID, or the ID.\n" +
			"imports the machine's configuration, or with --file a document, into the draft, sending its kept ETag or --if-match. It\n" +
			"waits until the import pauses for review or ends."},
	{path: "import status", line: "one import's state, or a list", roles: "any",
		synopsis: "import status [<ingestion>]\nimport status [--state <s>] [--machine <machine>] [--cluster <cluster>]",
		notes:    "--machine is a machine's name or its ID (mch_...); --cluster narrows a name two clusters hold.\none import's state, lease and next steps, or a list: --state paused or held finds imports to continue or take over."},
	{path: "import review", line: "read a paused import's staged document", synopsis: "import review <ingestion>", roles: "author", person: true,
		notes: "only on a terminal, in a pager (space or f forward, b back, q quit) that writes nothing else; then marks from the prompt."},
	{path: "import mark", line: "mark values as secret", synopsis: "import mark <ingestion> [--no-wait]", roles: "author", person: true,
		notes: "marks values as secret by their paths, one per line from the prompt or standard input, never an argument. It waits until the\n" +
			"import pauses again."},
	{path: "import continue", line: "continue an import paused for review", synopsis: "import continue <ingestion> [--no-wait]", roles: "author", person: true,
		notes: "waits until the import pauses again or ends."},
	{path: "import takeover", line: "take over an import whose lease lapsed", synopsis: "import takeover <ingestion> [--no-wait]", roles: "author", person: true,
		notes: "waits until the import pauses again or ends."},
	{path: "import abandon", line: "abandon an import", synopsis: "import abandon <ingestion>", roles: "author", person: true},
	{path: "import-base", line: "the stored configuration an import started from"},
	{path: "import-base document", line: "the stored document of an import base", synopsis: "import-base document <import base revision>",
		roles: "author", person: true,
		notes: "any author may read it for an import read from a node; for a file import only the author who made it and continued its\n" +
			"review. Only on a terminal, in the same pager as import review; no marks prompt."},

	{path: "publish", line: "publish a draft as a release", synopsis: "publish <draft> [--no-wait]", roles: "publisher", ifMatch: true,
		notes: "sends the draft's kept ETag or --if-match, waits for the publication and names the release and each machine's plan create\n" +
			"command. Nothing is sent to any machine."},
	{path: "release", line: "releases: published configuration"},
	{path: "release list", line: "list the releases", synopsis: "release list [--cluster <cluster>] [--draft <draft>]", roles: "any"},
	{path: "release show", line: "show a release", synopsis: "release show <release>", roles: "any"},
	{path: "release review", line: "a machine's rendered, redacted configuration in a release", synopsis: "release review <release> <machine> [--cluster <cluster>]", roles: "any", notes: machineByNameNote},
	{path: "release provenance", line: "the secret references a machine's configuration was built from", synopsis: "release provenance <release> <machine> [--cluster <cluster>]", roles: "any", notes: machineByNameNote},

	{path: "plan", line: "plans: what to send to a machine"},
	{path: "plan list", line: "list the plans", synopsis: "plan list [--state <s>] [--machine <machine>] [--cluster <cluster>]", roles: "any",
		notes: "--machine is a machine's name or its ID (mch_...); --cluster narrows a name two clusters hold."},
	{path: "plan show", line: "show a plan, its evidence and what can be done next", synopsis: "plan show <plan>", roles: "any"},
	{path: "plan create", line: "create a plan for a machine", roles: "publisher",
		synopsis: "plan create --release <release> --machine <machine> [--cluster <cluster>] --operation apply-config|adopt [--mode no-reboot]\n" +
			"            [--expires-in <d>] [--max-observation-age <d>] [--check-validity <d>]\n" +
			"            [--transport-deadline <d>] [--verification-deadline <d>] [--max-attempts <n>]",
		notes: machineByNameNote + "\n" +
			"a duration is in whole seconds, such as 90s or 24h; an omitted value takes the server's default. An adopt plan takes no\n" +
			"mode, deadline or attempt limit. The plan is not approved and nothing runs until an approver approves."},
	{path: "plan cancel", line: "cancel a plan", synopsis: "plan cancel <plan> --reason <text>", roles: "publisher (its own plan), approver or recovery-admin",
		notes: "a cancelled plan cannot be committed; a committed plan's operation ends only while no attempt is recorded, and an attempt\n" +
			"already recorded is not stopped."},
	{path: "apply", line: "apply a release to the machines you choose", synopsis: "apply <release> (--machine <a,b> | --all) [--diff]", roles: "publisher, then approver", person: true,
		notes: "makes one apply-config plan for each machine chosen, by name or ID, or --all of the release's machines, prints each machine with\n" +
			"its role, profiles and plan expiry, and with --diff each plan's change, on standard error, then asks Approve them? [y/N] and approves\n" +
			"every plan listed only on y or yes. A plan whose change cannot be read, that is withheld, or that overwrites a drifted configuration is left out. Enter, n and the end of input send no approval\n" +
			"and cancel the plans this command made; a plan somebody else prepared is approved as it stands and never cancelled here. A Ctrl-C at\n" +
			"the question leaves the plans to expire at the time each shows. A machine already applied, frozen, in drift, in an operation or with\n" +
			"another operation's plan waiting is left out with its reason.\n" +
			"There is no --yes, --no-input refuses, and -o json and -o yaml are not offered. It does not wait for the operations."},
	{path: "approve", line: "approve a plan", synopsis: "approve <plan>", roles: "approver", person: true,
		notes: "prints the plan and the predicted self-approval on standard error, then asks Approve it? [y/N] and sends only on y or yes,\n" +
			"on the terminal or as one line of standard input. Enter, n and the end of input send nothing. There is no --yes, and\n" +
			"--no-input refuses. An approval starts no operation, so approve does not wait."},
	{path: "approval", line: "approvals of plans"},
	{path: "approval show", line: "show an approval", synopsis: "approval show <approval>", roles: "any"},
	{path: "approval revoke", line: "revoke an approval", synopsis: "approval revoke <approval> --reason <text>", roles: "approver or recovery-admin",
		notes: "a revocation ends an uncommitted approval and undoes no commitment; an attempt already recorded is not stopped."},

	{path: "operation", line: "operations: what a plan did"},
	{path: "operation list", line: "list the operations", synopsis: "operation list [--plan <id>] [--machine <machine>] [--cluster <cluster>] [--state <s>] [--kind <k>]", roles: "any",
		notes: "--machine is a machine's name or its ID (mch_...); --cluster narrows a name two clusters hold."},
	{path: "operation show", line: "show an operation", synopsis: "operation show <operation>", roles: "any"},
	{path: "operation watch", line: "follow an operation until it ends", synopsis: "operation watch <operation> [--timeout <d>]", roles: "any",
		notes: "prints one line per event until the operation ends, then its outcome; it goes on past unresolved. --timeout (30m) ends the\n" +
			"watch, not the operation, with exit 5; Ctrl-C stops only the watch, with exit 130."},
	{path: "act", line: "the audit record"},
	{path: "act list", line: "list the audit record", synopsis: "act list", roles: "any"},

	{path: "recovery", line: "recovery after a restore"},
	{path: "recovery status", line: "the epoch, recovery mode, entry and exit, and each scope's next step", synopsis: "recovery status", roles: "any"},
	{path: "recovery enter", line: "enter recovery", roles: "recovery-admin", person: true, not: "expect-epoch",
		synopsis: "recovery enter --restored family=<f>,backup=<id>,taken-at=<time> [--restored ...]\n" +
			"               --stopped-at <time> --reason <text>",
		notes: "on a server started for recovery; sent only after a y. A repeat of an unknown outcome takes --idempotency-key\n" +
			"and --expect-principal, no epoch. Times are RFC 3339."},
	{path: "recovery exit", line: "leave recovery", synopsis: "recovery exit [--fact name=value]...", roles: "recovery-admin", person: true,
		notes: "once every scope is released."},
	{path: "recovery scope-account", line: "account every unaccounted attempt of a machine after a restore", roles: "recovery-admin", person: true,
		synopsis: "recovery scope-account <machine> --start <observation> --end <observation> [--explanation <text>] [--cluster <cluster>]",
		notes:    machineByNameNote + "\nshows the window and the risk, and is sent only when y is answered to the question that names the machine."},
	{path: "recovery account", line: "account one attempt whose response was lost", roles: "recovery-admin", person: true,
		synopsis: "recovery account <operation> <attempt> --start <observation> --end <observation> [--explanation <text>]",
		notes:    "the same window, risk and question as scope-account."},
	{path: "recovery takeover", line: "make the serving controller own an operation", synopsis: "recovery takeover <operation> --reason <text>",
		roles: "recovery-admin", person: true,
		notes: "makes the serving controller the owner of a non-terminal operation; sends nothing to the node. A repeat of an unknown\n" +
			"outcome also takes --generation as printed."},
	{path: "recovery resolve", line: "end an unresolved operation that has no attempt", synopsis: "recovery resolve <operation> --reason <text>",
		roles: "recovery-admin", person: true,
		notes: "ends an unresolved operation with no attempt as cancelled and records no remote outcome. A repeat of an unknown outcome\n" +
			"sends the same body without the state checks."},
	{path: "recovery mark", line: "record what the evidence supports for a machine", synopsis: "recovery mark <machine> [--fact name=value]... [--cluster <cluster>]",
		roles: "recovery-admin", person: true, notes: machineByNameNote + "\nrecords the state the evidence supports: ready, blocked or unresolved."},
	{path: "recovery release", line: "release a ready scope", synopsis: "recovery release <machine> [--fact name=value]... [--cluster <cluster>]",
		roles: "recovery-admin", person: true, notes: machineByNameNote + "\na release is not an approval."},
	{path: "identity", line: "identities of people and services"},
	{path: "identity revoke", line: "revoke an identity", roles: "recovery-admin", person: true,
		synopsis: "identity revoke (--identity <id> | --iss <iss> --sub <sub>) --reason <text>",
		notes:    "permanent; names the identity (or the subject and its issuer), then sends only after a y."},
}

// draftWrite is the note of every leaf that writes to a draft.
const draftWrite = "A write to a draft sends the ETag of the last draft show or write of that draft, or --if-match <etag>; if the draft\n" +
	"changed since, it saves nothing and says so. Draft writes take author; repeat one with the outcome-unknown block's flags,\n" +
	"--if-match included."

// globalTopic is what `help global` prints: the flags every command takes and the exit codes. It moved
// here from the usage text, word for word.
const globalTopic = `global flags, before or after the command:
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
a code above applies or the server refused the request (1); 130 a read command or operation watch stopped by Ctrl-C`

// lookup is the table entry for a command path, or nil.
func lookup(path string) *cmdEntry {
	for i := range cmdTable {
		if cmdTable[i].path == path {
			return &cmdTable[i]
		}
	}
	return nil
}

// verbsOf is the leaves of a group, in table order: the verbs its dispatcher accepts.
func verbsOf(group string) []string {
	var verbs []string
	for _, x := range cmdTable {
		if rest, ok := strings.CutPrefix(x.path, group+" "); ok && !strings.Contains(rest, " ") {
			verbs = append(verbs, rest)
		}
	}
	return verbs
}

// topLevel is the names the client takes first, in table order: a group, or a leaf with no group.
func topLevel() []string {
	var names []string
	for _, x := range cmdTable {
		if !strings.Contains(x.path, " ") {
			names = append(names, x.path)
		}
	}
	return names
}

const helpFooter = "Run bronzewardctl help <command> for a command's flags, notes and roles, and bronzewardctl help global for the global flags and the exit codes."

// indexText is the index: the usage line, then one line per top-level command, a group with its leaves
// on that line, no flags.
func indexText() string {
	var b strings.Builder
	b.WriteString("usage: bronzewardctl [global flags] <command> [arguments]\n\ncommands:\n")
	for _, name := range topLevel() {
		x := lookup(name)
		what := x.line
		if verbs := verbsOf(name); len(verbs) > 0 {
			what = strings.Join(verbs, ", ")
		}
		b.WriteString("  " + pad(name, 13) + what + "\n")
	}
	b.WriteString(helpFooter)
	return b.String()
}

func pad(s string, n int) string {
	if len(s) >= n {
		return s + " "
	}
	return s + strings.Repeat(" ", n-len(s))
}

// groupText is the help of a group: its leaves, one line each.
func groupText(group string) string {
	var b strings.Builder
	b.WriteString("usage: bronzewardctl [global flags] " + group + " <command> [arguments]\n\n" + lookup(group).line + "\n\n")
	for _, v := range verbsOf(group) {
		b.WriteString("  " + pad(group+" "+v, 28) + lookup(group+" "+v).line + "\n")
	}
	b.WriteString("\nRun bronzewardctl help " + group + " <command> for a command's flags, notes and roles.")
	return b.String()
}

// leafText is the help of a leaf: its usage lines, notes, roles and, from the flag set the leaf built in
// parse (nil when none was read), its flags. The global flags every set carries are left out.
func leafText(x *cmdEntry, fs *flag.FlagSet) string {
	var b strings.Builder
	for i, l := range strings.Split(x.synopsis, "\n") {
		switch {
		case i == 0:
			b.WriteString("usage: bronzewardctl " + l + "\n")
		case strings.HasPrefix(l, " "): // the continuation of the line above
			b.WriteString(strings.Repeat(" ", 21) + strings.TrimLeft(l, " ") + "\n")
		default:
			b.WriteString("       bronzewardctl " + l + "\n")
		}
	}
	b.WriteString("\n" + x.line + ".\n")
	if x.notes != "" {
		b.WriteString("\n" + x.notes + "\n")
	}
	b.WriteString("\nRoles: " + x.roles + ".")
	if x.person {
		b.WriteString(" Needs a person signed in, not an automation token.")
	}
	b.WriteString("\n")
	if names := leafFlags(x, fs); len(names) > 0 {
		b.WriteString("\nFlags: " + strings.Join(names, ", ") + "\n")
	}
	b.WriteString("\nGlobal flags and exit codes: bronzewardctl help global.")
	return b.String()
}

// globalFlags are the flags parse adds to every flag set.
var globalFlags = []string{"context", "o", "token-file", "token-stdin", "no-input", "v"}

// visibleFlags are the flags a flag set holds that the leaf takes: not a global one, not one the entry hides.
func visibleFlags(x *cmdEntry, fs *flag.FlagSet) []*flag.Flag {
	if fs == nil {
		return nil
	}
	var shown []*flag.Flag
	fs.VisitAll(func(f *flag.Flag) {
		if !slices.Contains(globalFlags, f.Name) && !slices.Contains(strings.Fields(x.not), f.Name) && (f.Name != "if-match" || x.ifMatch) {
			shown = append(shown, f)
		}
	})
	return shown
}

// leafFlags are those flags, as they are written in help.
func leafFlags(x *cmdEntry, fs *flag.FlagSet) []string {
	var names []string
	for _, f := range visibleFlags(x, fs) {
		names = append(names, "--"+f.Name)
	}
	return names
}
