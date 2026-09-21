# Investigation fixtures

A disposable environment for the feasibility experiments (design §18.1): a two-node Talos cluster,
PostgreSQL, OpenBao, and pinned `age`/`sops` CLIs, all created from nothing and removed completely.
Rationale, alternatives and recorded results are in the
[fixture investigation report](../docs/design/research/20260919-investigation-fixtures.md).

Nothing here selects the PoC database, provider or deployment profile. Every candidate is present
so the experiments can compare them.

## Prerequisites

Linux on x86-64, Docker Engine with the compose v2 plugin, and `bash`, `curl`, `jq`, `git`, `tar`,
`find`, `awk`, `cmp` and GNU coreutils (`sha256sum`, `timeout`, `install`, `stat`, `mktemp`). Each command checks for what
it needs before doing anything. The user must be able to run `docker` without `sudo`. About 4 GiB
of free memory and 1.5 GiB of image downloads on first run. No other tool is taken from the host: `talosctl`, `sops`
and `age` are downloaded into `fixtures/.cache/` and refused on a checksum mismatch; since the cache is
writable, each is checked against the manifest's digest again before use (`talosctl` before its first
run in any command, bounded calls included, all four by `evidence` before it runs any and by `up` once
they are installed), so a binary replaced or damaged since the download is refused, not run as the
pinned one.

## Commands

| Command | Does |
|---|---|
| `fixtures/bin/up` | Generates synthetic secrets, starts PostgreSQL and OpenBao, initializes and unseals OpenBao, creates the Talos cluster, then asks both services again with this run's credentials before it reports the fixture up. Takes about two minutes once images are cached. |
| `fixtures/bin/inject <action>` | Failure injection and backup/restore. Run it without arguments for the list. `.state/injections.log` gets two timestamped lines per action (the outcome handler is armed before the first is written, and a signal is held from before that line until the outcome is armed, so a `begin` is never left without its outcome; a signal that ends the command between two of its steps is recorded as `failed`, not as the `done` the shell's status would read as): `begin` when it starts, then `done` or `failed rc=N`, or `unknown rc=N` for a `bao-restore`, `bao-soft-delete`, `bao-destroy` or `bao-delete-key` whose request went out and got no answer (a timeout, a dropped connection or a signal), since OpenBao may have applied it all the same, and for a `kill`, `start`, `pause`, `unpause`, `netsplit` or `netjoin` whose Docker request failed and whose outcome the daemon cannot be asked for: when the CLI fails, whether by a refusal, a dropped connection or a signal, the container's state is read back (a few times over, since a kill is answered before the container has stopped), and the state asked for is `done`, another state `failed` (a `start` whose CLI failed stays `unknown` even with the container running, since the service was not seen to answer); for a `netjoin` the state asked for is membership at the node's fixed address, or under the service's alias, not membership alone, since a container attached meanwhile at another address is a member the connect refuses. `start` is done only once the service or Talos node answers again: for PostgreSQL an authenticated query on the fixture's database, not `pg_isready`, which answers whatever the parameters. `bao-delete-key` reads the key's `deletion_allowed` setting first, then sends two requests; a deletion refused after the setting was applied puts it back to what was read (a key already deletable is left so) and fails, and is `unknown` when that cannot be done either, or when the deletion went out and got no answer, whatever the setting was. A snapshot name is one path component, without a slash and without `.partial.` in it; a KV path or Transit key name is slash-separated components of letters, digits, `_`, `-` and `.`, none being `.` or `..` (which would reach another API path than the one the log names), and is refused before anything is logged; no argument may hold a control character, since each is written into the log, which must itself be the regular file `inject` made, under that one name. A snapshot gets its final name only once it is complete; `db-restore` and `store-restore` replace the live data only after the snapshot was read in full. The rename that publishes a snapshot, or puts a restored store in place, is the commit, as is the one transaction that swaps the restored database in: an action interrupted after it is logged `done`, since the artifact it left is what a later restore acts on (after an interrupted `db-restore` the server is asked whether the swap committed; `bronzeward_previous` is then left until the next `db-restore` drops it). Every request to a service is time-bounded, so an action against a hung service fails instead of hanging; PostgreSQL is given the same limit as its statement and lock timeouts for the session, since ending the client leaves the statement running inside the container, and one waiting on a lock would otherwise still commit after the log said the action failed (`pg_dump` and `pg_restore` lift both for their own session). |
| `fixtures/bin/evidence [path ...]` | Writes versions, container logs, a database dump, a copy of the PostgreSQL data directory as written to disk (read from the container whether the server runs or not), OpenBao metadata and Talos config digests to `.state/evidence/<utc>/`, then scans them, and any extra paths given, for synthetic secret material. Prints the bundle path. A bundle is some tens of MiB, most of it that data directory. |
| `fixtures/bin/down [--purge] [--adopt]` | Removes every container, network and volume, then checks that nothing is left and fails if something is, or if Docker could not be asked. The claim and then `.state/` are removed last and only after that check passed, so a `down` that could not finish can simply be run again. `--purge` also removes `.cache/`. It refuses a fixture it cannot show to be this checkout's own; `--adopt` overrides that. |
| `fixtures/bin/selftest [--keep]` | `up`, each injection once with an assertion, `evidence`, `down`. `--keep` runs the checks against a fixture that is already up. |

An experiment uses the fixture like this:

```sh
fixtures/bin/up
set -a; . fixtures/versions.env; . fixtures/.state/secrets.env; set +a
export TALOSCONFIG=$PWD/fixtures/.state/talosconfig
# ... run the prototype against 127.0.0.1:$POSTGRES_PORT, http://127.0.0.1:$OPENBAO_PORT,
#     Talos nodes $TALOS_CONTROLPLANE_IP and $TALOS_WORKER_IP, local stores in fixtures/.state/data ...
fixtures/bin/inject kill postgres
fixtures/bin/evidence /path/to/the/prototype/tmp
cp -r "$(ls -d fixtures/.state/evidence/* | tail -1)" /somewhere/outside/.state   # down deletes .state
fixtures/bin/down
```

## Versions

[`versions.env`](versions.env) is the single manifest: image index digests, CLI release URLs with
their SHA-256, the Kubernetes version and the topology. An experiment report cites the commit of
that file; `bin/evidence` records it together with the versions the running services report. The
values are frozen. Bump one only in a commit that says why, because evidence recorded against the
old value no longer reproduces. The file is deliberately not a format Renovate reads.

## Synthetic secrets

No secret is committed. `bin/up` generates all of them into the gitignored `.state/`:

- `secrets.env`: a canary value, the PostgreSQL password, and the OpenBao root and metadata-only
  tokens. Values the fixture chooses itself start with `BWSYNTH-`. The commands read this file as
  data, never as shell code: only those four assignments are accepted, all four must be there, the
  file must be the regular file `bin/up` wrote under that one name, and anything else in it stops
  every command. A value the caller's shell holds under one of those names is never used.
- `bao-init.json`: the single OpenBao unseal key, in base64 and in hex, and the root token.
- `talos-secrets.yaml`, `controlplane.yaml`, `talosconfig`, `kubeconfig`: the cluster's own
  generated secrets bundle and client configs.
- `scan-patterns.txt`: every one of the above as a fixed string, the client private keys in
  `talosconfig` and `kubeconfig` included. `bin/evidence` reports the files that contain any of
  them, with the number of occurrences only; a matched line is never printed. A name that holds a
  newline or another control character is reported in bash's quoted form, so that the report stays
  one entry per line.

The scan has a positive control, `.state/data/canary-control.txt`. If the scan does not find it
holding exactly what `bin/up` planted, or cannot read a path or file it was given, the command
fails, because an empty result would then prove nothing. Only that file and its copies inside
expanded store snapshots count as the control; any other file of the same name is reported like
every other hit, and a control whose content changed, or that is a symlink or has a second hard
link, is no control at all: the command then fails, since the planted canary is absent.
The bundle's `versions.txt` names the manifest commit; a capture that cannot read it from git
fails rather than write a bundle that cannot be tied to its pins, and the bundle's
`fixtures-diff.txt` holds the status of every changed or untracked file, the diff of the tracked
ones and the whole content of the untracked ones (text or not), so that the commit plus that file
says what ran; it is empty when `fixtures/` is the commit's, and the like record `bin/up` wrote at
creation is required, since one removed could otherwise not be told from one never needed. Both diffs are git's own (`--no-ext-diff`, so a diff helper from the
caller's configuration is not handed them) and carry binary content whole; the status lists
untracked files whatever `status.showUntrackedFiles` says. A symlink anywhere
under `fixtures/` outside `.state` and `.cache`, tracked or not, is refused, since the file would
carry the target's name and not the bytes that ran; so is an untracked file that only
`.git/info/exclude` or a global excludes file hides, since neither the status nor the content
would show it (the repository's own `.gitignore` files are the one exclusion the bundle stands
by); the refusal withholds a name that holds secret material, like every path the command prints. So is
what git records nothing of, an empty directory or a special file (a named pipe, a socket, a
device), since the commit plus a diff would not say it is there; a gitlink (a submodule, or a
repository staged as one) or a nested `.git` under `fixtures/`, since the diff would carry a
commit hash at most and not the bytes under it; a `filter`, `text`, `eol`,
`ident` or `working-tree-encoding` attribute that git's attributes give a file, tracked or
untracked, since git would then compare the attribute's output and not the bytes that run
(`core.autocrlf` is turned off for every listing and diff for the same reason, so a script
turned to CRLF is recorded as the bytes it has, and `core.ignoreCase` with it, so an untracked
file whose name differs from a tracked one's only by case is listed and not taken for the
tracked one, and git's stat cache is not trusted either — `core.trustctime`, `core.checkStat`,
`core.fsmonitor` and `core.untrackedCache` are set so that a file changed to bytes of the same
length with its mtime put back is compared by content, not taken for the commit's; `core.fileMode`
is forced on, so a command made non-executable is in the diff; `core.symlinks` is forced on, so a
regular file checked out where the commit has a symlink (as `core.symlinks=false` does) is in the
diff as the regular file it is; and replacement refs are turned
off with `--no-replace-objects`, so the diff is taken against the commit the manifest names, not
against a replacement of it); a tracked file git is told to skip
(assume-unchanged or skip-worktree), since a change there is in no status and no diff; and
an ignore file that is not the commit's, a modified `.gitignore` or an untracked one, since the
listings apply the working tree's rules and a file a new rule hides would be in none of them,
with the diff carrying the rule and not the file (the committed rules are anchored, so a
directory named `.state` or `.cache` anywhere else is untracked like any other). The diff is taken
against the commit recorded, not `HEAD` read a second time, and `HEAD` is read once more at the
end: a checkout or commit during the inventory is refused, since the listings would describe
another commit than the one named. `bin/up` makes the same
checks before it records the commit the fixture is made from, so that record stands by what the
bundle stands by. Each archive expanded into the bundle has its SHA-256 recorded next to the
expansion, read from the archive's content so that its name, whatever it holds, is not in the
record (and `tar` runs with `--no-unquote`, so a backslash in a name is a backslash, not an
escape), and the control's copy inside one is the control only while the archive under that
name still has those bytes; a dump or archive that is a symlink or has a second name is refused,
not expanded, since the fixture never makes one and what the other name holds would be copied
into the bundle. Each path is scanned once, however many of the given paths contain
it and under whichever spelling (an extra path that is a symlink to a scanned directory, or lies
under one, is not walked again; its own name is still matched; an extra path that resolves to a
parent of a scanned directory, such as a symlink to the checkout or to `.state`, is walked by the
name it resolves to, so the control is met at its own name and not taken for a leak), and reported once, whether the
secret is in its content, in its name or in both. Names of files and directories,
and the target path a symlink stores, are matched as well as file contents; a name that holds a secret is withheld from the report, which
gives the inode instead. Symlinks are followed, so a linked file or directory is scanned through its link and
a link that cannot be followed fails the command; extra paths may be relative and may have any
name. `bin/evidence` is meant to run while parts of the fixture are down: a source it
cannot read, such as the live database after `inject kill postgres`, is named in
`unavailable.txt` in the bundle and everything else is still captured and scanned. Compressed
backups are expanded before scanning, without needing the database server. What an interrupted
action leaves is covered too: a dump or archive still under its temporary `.partial.` name is
expanded as far as it goes (a truncated one is named in `unavailable.txt`), a `store-restore`
staging directory under `.state` is scanned like the live store, and a database left under a
`db-restore` scratch name is dumped next to the live one, and a bundle a capture left before its
own leak scan ran (it stays marked incomplete, by a `.incomplete` file removed only after the
scan) is scanned as one more root by the next capture whose scan completes, since it holds a
dump, logs and a data-directory copy nothing scanned; that capture then marks it scanned
(`scanned-by.txt` in the earlier bundle names the one whose `leak-scan.txt` holds the hits), so
it is scanned once, not at every capture after. A capture holds a lock on its marker for as long
as it runs, and an earlier bundle is taken only when its marker can be locked: one that another
capture is still writing is left to its own scan and said so, not scanned half-written and
marked scanned. The OpenBao metadata is recorded from listings parsed as JSON; a listing that is
not is recorded as unknown, and the bundle stays marked incomplete. With the live store directory gone, as
a `store-restore` killed between its two renames leaves it, the capture goes on without it, names
it in `unavailable.txt`, and takes the control from the copy the staging directory holds. The
next `store-restore` refuses while such a leftover exists and names it as the only copy, to move
back by hand; one left by a restore that completed, killed while removing its staging directory
(the live directory present, the staging one holding only `previous`), it names as the replaced
store, to remove. OpenBao snapshots are encrypted by
OpenBao and are scanned as they are. The pattern list in `scan-patterns.txt` is rebuilt from the
generated credentials at every capture and must equal the file, or the scan is void. Talos key material is matched in the base64 form
the secrets bundle carries, so a decoded PEM copy would not match.

Use only these values in experiments. Never point a prototype at a real cluster, vault or database
from here.

## Ownership and recovery

`.state/` belongs to whoever ran `bin/up`, and its lifetime is one run. Nothing in it is backed up
or recoverable by design: losing `bao-init.json` loses that OpenBao instance, which is the
intended way to study key loss. `bin/down` is the only cleanup and is safe to run at any time,
including after a failed or interrupted `up`. `.state`, its `data`, `backups`, `evidence` and `talos`
directories, and `.cache` must be real directories: every command refuses to run while one of them
is a symlink, because secrets, or the CLIs, would be read or written outside the checkout, or
`talosctl cluster destroy` pointed at someone else's state; the cluster state file `talosctl` writes
under `.state/talos` must likewise be a regular file with that one name at teardown. The files `bin/up` generates
(`secrets.env`, `bao-init.json`, `talosconfig`, `kubeconfig`, `talos-secrets.yaml`,
`controlplane.yaml`, `scan-patterns.txt`, `injections.log`, the node-volume, node-container,
node-network, Compose-container, Compose-volume and Compose-network records, `up-manifest`, `up-fixtures-diff.txt`, `up-fixture-name`, `up-daemon`, `up-versions.env` and `up-compose.yaml`) must each be
the regular file it wrote, with no second name: a symlink or a hard link there stops `inject`,
`evidence` and `down` before anything is scanned or removed, since the secret would outlive
teardown under the other name. The same holds for a snapshot about to be replaced by one of the
same name, for a snapshot about to be restored from (a symlink or a second name there would install
what nothing here wrote), for every entry in `.state/backups` at teardown (dot-named ones included), for a `store-restore` staging
directory, and for the marker `down` leaves when the node volumes are not all known. `secrets.env` is written whole and renamed into place at each stage of
`up`, so an interruption leaves it complete or absent, never cut mid-line, and a second name made
for it between two stages stops `up` there. The first two records `up` writes, before anything
else is made, are the fixture's name and the ID of the Docker daemon it is made on: every name,
label and port the commands use is derived from `FIXTURE_NAME` in `versions.env`, and every check
is made against the daemon the shell reaches, so with `versions.env` edited, the checkout
switched, or `DOCKER_HOST` or the Docker context changed since `up`, `down` would look for
nothing, find nothing, remove `.state` and report success while the fixture ran on. Every command
refuses while `versions.env` names another fixture than the record; `inject`, `evidence` and
`down` refuse while the shell reaches another daemon than the record, or while `.state` holds no
daemon record at all. The rest of `versions.env` (ports, node addresses, image pins, requested
versions) is bound the same way: every command reads the file once, as bytes, clears every value
the manifest must set before taking its values from that reading, and refuses a file that leaves
one unset, so a value exported by the caller's shell never stands in for one the file omits;
`up` records those bytes as `up-versions.env`, and every command refuses while
the bytes it read differ from the record, since an edit since `up` would send an `inject` to
another port or report a version never requested; restore the file, or the checkout it came from,
then run `down`. `compose.yaml` is recorded the same way, as `up-compose.yaml`, and Compose is run
from the two records, at `up` and at `down`, never from the checkout's files: the services are
created well after the records are written (the CLIs are fetched in between), and an edit in that
window would create them from bytes no record holds; both file records are compared to the files
again once `fixtures/` has been inventoried, so the inventory carries the bytes recorded. At
teardown the daemon record is the last entry removed from `.state`, the two records Compose is run
from go just before it and together, and an empty `.state`, which a removal cut short after the
daemon record leaves, is taken for none: a removal cut short at any point leaves a directory the
next plain `down` removes. The daemon is asked before the claim is made on it;
the four records are
written whole and renamed into place, the first thing under `.state`, and a failure or a signal
before they are in place, the exit handler armed before the directory is made, takes the records
(when the daemon record is this run's), the directory and the claim with it, since nothing else
exists yet: so no `.state` without the record is one `up` left, and none is taken for a fixture
on whichever daemon the shell reaches now. While a Compose volume is being made and its label not
yet read back, the hint a failure prints names that volume to inspect by hand, not `--adopt`,
which would remove it by the label whoever made it. The commands also check
that every container answering to a fixture name carries the fixture's own labels (Compose
project and directory, or Talos cluster name), since the names are fixed and, once the real
container was removed by hand, anything can take the name while the claim stands, and that it is
the container `up` recorded under that name: the labels can be given to any container, so `up`
records the ID of each of the four under its name once it exists, and `inject` and `evidence`
act on a container under a fixture name only if its ID is the one recorded under that name (the
two Talos nodes' names swapped, each is still one recorded, and a pause of the worker would go
to the control plane); a container
under a fixture name that Docker cannot be asked about stops the command too, since only "no such
container" means absent. The node-volume record's content is held against Docker as well: `down` removes
a recorded name only if it is an anonymous volume's, and the volume, if it still exists, carries
no label. The Talos label is only a cluster name, which any container or network can be created
with, so `up` records the IDs of the two containers and the network it created once they exist:
`inject netsplit` and `netjoin` touch the network under the fixture's name only if its ID is
recorded (a Talos node would otherwise be given its fixed address on whatever network took the
name once every node was off it), and
`down` refuses while a container or network carries the label without being recorded, or while
labelled ones exist and there is no record, as an `up` interrupted right after creating the
cluster leaves it. The Compose containers and network are held the same way: their labels are only
the project's, which any container or network can be given, so `up` records their IDs after
`compose up`, `down` holds every labelled container and the labelled network to the records, and
`inject` the network. Only `down --adopt` removes by label alone, and only
where no record exists: with a record, a labelled container, network or volume the record does not
name is someone's and no `down` removes it. That is what the hint of an `up` that failed names
from the first Compose volume until the last record is published, since a plain `down` refuses
what it finds without its record; an `up` that found a Compose volume name taken (the label read
back is another run's), or whose `docker volume create` failed or whose read-back did (the volume
may exist all the same, and whose it is is not known), publishes the record of the volumes it
did make and names that volume to inspect, remove or rename by hand, then a plain `down`, not
`--adopt`. The named volumes `compose.yaml`
declares get fixed names too (`<fixture>_postgres-data`, `<fixture>_openbao-data`), and Compose
reuses a volume of that name it did not create: `up` refuses while one exists, and `down`, with
or without `--adopt`, refuses to run `compose down --volumes` while one exists without the
project label, since it is someone's data. The label is only the project name, which any Compose
run of that name puts on the volumes it makes, and a look before `compose up` cannot rule out a
volume made in between, so `up` makes the two volumes itself, first of all, labelled with the
run, reads the label back (a volume that existed keeps its own labels, and `docker volume create`
on an existing name says nothing) and refuses when it is not this run's; `down` removes a
labelled volume only as the run recorded, `--adopt` or not, and without the record, as an `up`
interrupted right after making the volumes leaves it, only `down --adopt` removes it by the label. The Talos node
volumes are recorded from the two containers whose IDs were recorded, not from whatever carried
the label at the time. The checkout is
identified by its physical path, so the same checkout reached through a symlink is still its own.
Evidence worth keeping must be copied out of `.state/` before `down`.

## Known limits

- Talos runs as containers. Configuration RPCs and `apply-config --mode=no-reboot` work; reboot,
  upgrade, reset, disk and installer behavior do not exist here. This matches the scope of the PoC
  path and the deferred Upgrade/LifecycleClient tests, and nothing more.
- Kubernetes images pulled inside the Talos nodes are pinned by version, not by digest.
- The PostgreSQL tag floats under a fixed digest. The exact server version is in each evidence
  bundle.
- SQLite has no service. Put database files under `.state/data`; the engine version is whatever
  the prototype links, so each experiment records it. `store-snapshot` copies files, which is only
  a valid SQLite backup while no process has the database open.
- The Talos subnet must not overlap the Kubernetes pod or service ranges. With an overlapping
  subnet Talos leaves the node address out of its API certificate and the cluster never bootstraps.
- Talos node volumes are anonymous. `bin/up` records their names in `.state/` once the cluster
  exists, and `bin/down` removes those and any still attached to fixture containers. If
  `talosctl cluster create` fails before a container exists, its empty volumes cannot be told apart
  from anyone else's and are left behind; `docker volume ls --filter dangling=true` shows them.
  The same holds for `down --adopt` run without the owning checkout's `.state/` after Talos
  containers were removed by hand. In both cases `down` says in its last line that it did not
  look for such volumes, instead of reporting that no volume is left. `bin/up` marks `.state/`
  before it creates the cluster and clears the mark once the records are published, so a `down`
  after an `up` interrupted in between says the same even when every container is gone; `down`
  rewrites the record whole and publishes it by rename, so an interrupted `down` leaves it
  readable. The converse limit: the
  record in `.state/` can be rewritten by anyone who can write there, and `down` can only hold
  each name to what Docker says of a Talos node volume. Any anonymous, unlabelled volume on the
  daemon, written into the record, is removed with the rest.
- A host that restarts the Docker daemon when a new bridge interface appears, for example through
  a NetworkManager dispatcher script, stops the containers that were already running each time a
  fixture network is created. `bin/up` detects this and fails. Fix the host hook so that it ignores
  `br-*`, `veth*` and `docker0`; the fixture does not work around it.
- One fixture per Docker daemon. Names, ports and subnet are pinned so that evidence reproduces,
  which means two checkouts on one daemon would share them. `bin/up` refuses to start while
  fixture containers, networks or volumes exist: a leftover PostgreSQL volume keeps the password
  of the run that created it. It then takes the claim: a container named `bw-fixture-claim` that
  is created, labelled with this checkout's path and never started (with a tmpfs in place of the
  data volume its image declares, so that no anonymous volume is made for it, which removed
  without `--volumes` would outlive every record). Docker refuses a second one
  of that name atomically, so of two `up` started together, from one checkout or two, only one
  proceeds. Docker can create the claim and still answer with an error (a connection lost after
  the request went through); an `up` refused that way looks at the claim, and one that carries
  the nonce this very `up` labelled its attempt with is reported as its own, with a plain `down`
  to remove it; a claim from this checkout under another nonce is a second `up` running here, and
  is reported as such. The claim's label is what the commands read the claim by, and a container
  that carries it under another name is nobody's they know: `down` names it, removes nothing,
  and leaves it to be removed or relabelled by hand; on its way out `down` removes the claim
  container only, never every carrier of the label. `bin/down` refuses when the claim or Compose names another directory as the origin,
  or when fixture resources exist and no claim from this checkout shows them to be its own; run
  `down` in the checkout that owns them, or `down --adopt` if that checkout is gone or the claim
  was removed by hand. A `.state/` alone does not count, because it can be left over from an
  earlier fixture, and `down` drops the claim only after everything else is verified gone. `bin/inject` and `bin/evidence` refuse unless the claim names this checkout: a `.state/`
  left over here does not make another checkout's fixture, which answers to the same names, fair
  game.
- `bin/evidence` observes OpenBao and PostgreSQL out of band, from inside their containers. After
  `inject netsplit openbao` every client finds the provider unreachable while
  `openbao-metadata.jsonl` still shows its true metadata. The bundle records both sides:
  `openbao-client-view.txt` says whether the published port answered, and `versions.txt` shows the
  partition as `networks=[]` on that container's line. An experiment compares what its own client
  concluded with that ground truth. The metadata file reports `unknown` only when the provider
  cannot be asked at all: stopped, sealed, paused, or a listing that fails.
- `bin/evidence` copies the PostgreSQL data directory into each bundle, about 46 MiB each, so a
  run of many captures fills `.state/evidence` accordingly (two `selftest --keep` runs against
  one fixture left 1.7 GiB); `down` removes it with the rest. A copy taken
  while the server runs is not a consistent backup and is not meant as one: it is what was on
  disk at that moment, for the leak scan.
- CI lints these scripts but does not run them.
