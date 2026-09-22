# Reproducible fixtures for the feasibility experiments

| | |
|---|---|
| **Date** | 19 September 2026 |
| **Work item** | [Establish reproducible investigation fixtures](https://github.com/ginsys/bronzeward/issues/1) |
| **Design reference** | [§18.1 Phase 0](../Talos_Configuration_and_Machine_Management_Design.md#181-phase-0---evidence-before-implementation-contracts) |
| **Artifacts** | [`fixtures/`](../../../fixtures/README.md), version manifest [`fixtures/versions.env`](../../../fixtures/versions.env) |
| **Decision enabled** | Experiments E1 to E5 can be designed against one shared, disposable environment. No database, provider or deployment profile is selected here. |

## 1. Question

Experiments E1 to E5 each need some of: Talos machines that accept configuration, a PostgreSQL
server, SQLite files, an OpenBao instance that can really be sealed, backed up and damaged, local
`age`/SOPS stores, synthetic secrets whose leakage is detectable, and ways to crash, hang, partition
and restore each part. Without a shared fixture every experiment would invent its own, with its own
versions, and the feasibility review could not compare their evidence. What is the smallest
environment that a reviewer can create from nothing, break on purpose, inspect and remove, with no
production access and no real credential?

## 2. Alternatives compared

| Approach | Covers | Does not cover | Cost to reproduce | Verdict |
|---|---|---|---|---|
| **Talos in Docker** (`talosctl cluster create docker`) plus compose services | Real Talos API, real machine configuration, `apply-config --mode=no-reboot`, real apid/TLS behavior on pause, partition and reconnect | Reboot, upgrade, reset, installer, disks | Docker only, no root, about two minutes per run | **Chosen** |
| Talos in QEMU (`talosctl cluster create qemu`) | Everything above plus reboot, upgrade and reset | Nothing relevant to Phase 0 | Requires root: the v1.13.6 provisioner stops with `please run as root user (CNI, qemu hvf requirement)`. Downloads a 321 MiB ISO per version. | Rejected for Phase 0; the right tool for the later lifecycle phases and for the deferred Upgrade/LifecycleClient tests |
| Mocked Talos API | Fully deterministic interleavings, no containers | Proves nothing about Talos itself | A prototype to write and maintain | Not a fixture. The [dispatch-safety experiment](https://github.com/ginsys/bronzeward/issues/7) may add a fake executor for exact interleavings, but must confirm its findings against the real API here |
| A spare physical or cloud cluster | Everything | Disposability and independence: state survives between runs, access is personal | Hardware or an account | Rejected: not reproducible by a reviewer, and too close to production access |
| OpenBao in dev mode | KV and Transit APIs | Startup unlock, snapshot and restore, surviving a crash: dev mode is in-memory and unseals itself | Least | Rejected: E5 is about exactly the parts dev mode removes. The fixture runs a single-node integrated-Raft server with a real init and one unseal key |

The Docker limit matches the PoC scope. [Design §18.2](../Talos_Configuration_and_Machine_Management_Design.md#182-phase-1---configuration-control-on-an-existing-cluster)
needs one safe worker `no-reboot` change, and the
[compatibility investigation](https://github.com/ginsys/bronzeward/issues/5) already defers
Upgrade/LifecycleClient execution. The fixture therefore adds no new gap, and it does not close
that one.

## 3. What was built

See [`fixtures/README.md`](../../../fixtures/README.md) for the commands. In short:

- `bin/up` generates synthetic secrets, starts PostgreSQL and OpenBao from digest-pinned images,
  initializes and unseals OpenBao (KV v2 at `secret/`, Transit with key `bw-artifact`, a
  metadata-only policy and token), and creates a one-control-plane, one-worker Talos cluster.
- `bin/inject` crashes (`kill`), hangs (`pause`) or partitions (`netsplit`) any of the four
  containers and brings it back; snapshots and restores the database, OpenBao and the local store
  directory; soft-deletes and destroys a KV version; deletes a Transit key. Each action is logged
  when it begins and again with its outcome. `db-restore` and `store-restore` replace live data only after
  the snapshot was read in full, and every request to a service is time-bounded. Like `bin/evidence`, it
  refuses unless the fixture on the daemon is this checkout's own.
- `bin/evidence` records versions, logs, a database dump, a copy of the PostgreSQL data directory
  as written to disk, OpenBao metadata read through the
  metadata-only identity, and a digest of each node's machine configuration, then scans all of it
  for synthetic secret material, with a positive control. It runs while parts of the fixture are
  down: a source it cannot read is named in `unavailable.txt`, and a scan input it cannot read
  fails the command.
- `bin/down` removes everything and fails if anything is left; the claim and then `.state/` go
  last, only after that check, so a `down` that could not finish can be run again.
- `bin/selftest` exercises all of the above with assertions.

### Pinned versions

From `fixtures/versions.env`, with what the running services reported:

| Component | Pin | Reported at run time |
|---|---|---|
| Talos node image | `ghcr.io/siderolabs/talos:v1.13.6@sha256:f2e2b7e5…7a6e` | `v1.13.6` on both nodes |
| `talosctl` | v1.13.6, SHA-256 `540c5e7c…41d4` from upstream `sha256sum.txt` | `Talos v1.13.6` |
| Kubernetes | 1.36.2 (version, not digest) | not separately recorded |
| OpenBao | `ghcr.io/openbao/openbao:2.6.1@sha256:5b2486ab…67e0` | `OpenBao v2.6.1 (ba7ad886…)` |
| HTTP client `inject` runs inside the OpenBao container's network namespace | `docker.io/curlimages/curl:8.22.0@sha256:58adaa4e…6777` | `curl 8.22.0` (by hand; not recorded in a bundle) |
| PostgreSQL | `postgres:17-alpine@sha256:f02121de…7995` | `17.11` |
| `sops` | v3.13.3, SHA-256 `e5bec334…ef6b` from upstream checksums file | `sops 3.13.3` |
| `age` | v1.3.2, SHA-256 `cbe24006…ac10` (the digest GitHub records for the release asset; upstream publishes no checksum file) | `v1.3.2` |
| Host used | Docker 26.1.5, compose 2.27.1, Linux 7.1.3 x86-64 | |

Talos v1.13.6 and OpenBao v2.6.1 are the design's technical baseline. The pins are outside
`mise.toml` on purpose: the repository's Renovate preset automerges minor and patch bumps of mise
tools, which would silently invalidate recorded evidence.

### Synthetic secrets

Nothing secret is committed. Values the fixture chooses start with `BWSYNTH-`; values chosen by
OpenBao and Talos (root token, unseal key, the cluster's secrets bundle) are collected after
creation. All of them become fixed-string scan patterns. This is what makes the
[secret-ingress experiment](https://github.com/ginsys/bronzeward/issues/2) checkable: after a
success, rejection, crash or restart it runs `bin/evidence <its temp and staging paths>` and gets a
list of files containing secret material, or none.

## 4. Expected and observed

`fixtures/bin/selftest`, run from a host with no fixture state, 19 September 2026, images already
pulled. The first revision had 14 checks and passed 14 of 14 twice in a row, in 2 min 35 s each,
`up` and `down` included. Review added checks 2, 10, 11, 13, 15, 16, 17, 18, 21 and 24 and widened
1, 2, 4, 5, 6, 7, 8, 9, 10, 12, 13, 19, 20, 21, 22 and 23 (section 5, items 8 to 58); the revision described here passed 24 of 24 from
clean. `selftest --keep`, which skips `up` and checks 23 and 24, passed 22 of 22 twice in a row
against one fixture. Those runs were not timed. Each revision was run this way again, from clean,
before it was committed, and the run of the revision described here is the one recorded with the
pull request that landed it; the numbers above are that run's, not an earlier revision's.

| # | Check | Expected | Observed |
|---|---|---|---|
| 1 | `bin/up` with an unlabelled volume named `bw-fixture_postgres-data` created for the test, then `bin/up` with it removed; all four containers running | The first `up` refuses and leaves no `.state`; after the second all four run | pass |
| 2 | `inject`, `evidence` and `down` with the claim container removed, then `inject` and `evidence` with one labelled for another checkout; then the claim taken again | All five calls refuse, `down` by naming the missing claim; PostgreSQL is not paused and nothing is removed; the claim container has no mount, so no anonymous volume was made for it | pass |
| 3 | Full-config `no-reboot` apply on the worker | The configuration read back is byte-identical to the one sent | pass: equal SHA-256 once the single extra trailing newline of `-o jsonpath` output is normalized |
| 4 | `db-snapshot`, diverge, `db-restore`, then `db-restore` of a dump cut in half, then `db-restore` with the live database dropped, then `db-restore` from a snapshot name that is a symlink to a dump outside `.state` and from a snapshot with a second hard-linked name | Row written after the snapshot is gone; the truncated restore fails, leaves the live database with its rows and leaves no scratch database; with the live database gone the restore installs the snapshot under the name and leaves no scratch database; the symlinked name and the hard-linked snapshot are each refused unread and the live database keeps its rows | pass |
| 5 | `bao-destroy` of `selftest/../selftest/item`, then `bao-soft-delete` | The destroy is refused before it is logged and the version is not destroyed; metadata then shows a deletion time, `destroyed` false | pass |
| 6 | `bao-destroy` | Metadata shows `destroyed` true | pass |
| 7 | `bao-delete-key`, on a key made deletable beforehand | Transit key no longer readable; the setting is read first and left as found (a deletion refused after the setting was applied puts it back to what was read and fails; not exercised) | pass |
| 8 | A second `bao-snapshot` under a `CURL_HOME` whose curlrc sets `output`; `bao-restore` of the earlier snapshot | The curlrc is ignored: nothing is written where it points and the snapshot is not empty; destroyed version and deleted key are back | pass |
| 9 | `store-snapshot` / `store-restore`, then `store-restore` of a corrupt archive, a `store-snapshot` with an excluding `TAR_OPTIONS` in the environment, a `store-snapshot` whose final name is a symlink to a directory, `store-restore` of an archive whose `data` is a symlink, a `store-snapshot` over a snapshot that has a second hard-linked name and a `store-restore` from it, a `store-restore` from a snapshot name that is a symlink to an archive outside `.state`, a `store-snapshot` whose name holds a slash, one whose name holds a newline, one whose name holds `.partial.`, an action word with a newline and an action that does not exist, a `store-snapshot` with `injections.log` replaced by a symlink to a file outside `.state` and then with a second hard-linked name, then `store-restore` with the live directory gone, then with the store a completed restore replaced left in its staging directory next to the live one, then with the store an interrupted restore moved aside left in its staging directory while the live one is gone, then `kill postgres` with the PostgreSQL container renamed aside and an unlabelled container created under its name, then `pause worker` with the worker renamed aside and a container created under its name carrying the Talos cluster-name label, then `pause worker` with the two Talos containers' names swapped | Deleted file is back; the corrupt restore fails and leaves the live directory, positive control included, untouched; the snapshot holds the file `TAR_OPTIONS` named; the symlinked name is replaced by the snapshot and nothing is written into the directory it pointed at; the archive with the symlinked `data` is refused and the live directory stays; the name with a slash is refused and nothing is written under it; the snapshot with a second name is not replaced and no partial is left, and neither it nor the symlinked name is restored from, no staging directory being left; the name and the action with a newline, the name with `.partial.` and the unknown action are refused before anything is logged; with the log linked either way the action is refused, no snapshot is taken and the file behind the symlink keeps its content; the missing directory is restored, the completed restore's leftover is refused as one to remove with the live directory untouched, the interrupted restore's as the store to move back with nothing installed, and an archive holding an entry named `previous`, restored while the directory is gone, is refused without that entry being installed; the kill and the two pauses are each refused and not logged, the real containers are untouched and neither node is paused | pass |
| 10 | `bin/evidence` while PostgreSQL is dead | Does not abort; `unavailable.txt` names the missing live dump; the database backup is still expanded and scanned; the data directory is read from the stopped container and the canary found in it | pass |
| 11 | `db-snapshot` while PostgreSQL is dead, after a `pause postgres` and a second `kill postgres` while it is dead | The pause fails and is recorded `failed`, the second kill is recorded `done`, since the container is stopped as asked; the snapshot fails; no file of that snapshot name is left in `.state/backups`; `injections.log` records it as `failed` and the preceding `kill` as `done` | pass |
| 12 | `kill` / `start` PostgreSQL, then `start` with the fixture's database renamed aside, then `start` with the role's password changed under the server (set from the container's environment over stdin, on no command line; put back through the handler), then a statement past the request limit (`pg_sleep` under a limit of 3 s), then the network password probe `up` ends on, with another password and with this run's | No answer while dead; committed row survives; the start with the database gone, and the one with the password changed, fail at their wait and are recorded `failed`, since readiness is an authenticated query on that database, not `pg_isready`, sent to the container's address on the Compose network, since the image trusts loopback; the probe, run from inside the container's namespace against its address on the Compose network, refuses the other password and answers this run's; the statement is cancelled by the server (its own statement-timeout error, not the client's status 124), so nothing runs on inside the container after the client is given up on | pass |
| 13 | `bin/evidence` while OpenBao is dead, then `bao-soft-delete` against it | `unavailable.txt` names the provider metadata; `openbao-metadata.jsonl` holds an `unknown` record, not an empty file; the mutation ends with status 125 (its client had no namespace to join and was not run) and is logged `failed`, not `unknown` | pass |
| 14 | `kill` / `start` OpenBao | Comes back sealed, unseals with the stored key, state intact | pass |
| 15 | `bao-snapshot` while OpenBao is paused, after a second `pause openbao` | The second pause is recorded `done`, since the container is paused as asked; the request ends on its own; the action fails, is logged as `failed` and leaves no snapshot file | pass |
| 16 | A 5-second wait on an authenticated OpenBao request while the server process inside the container is stopped (`SIGSTOP`), so that the CLI connects and is never answered | The wait fails within 10 seconds; OpenBao answers again after `SIGCONT` | pass |
| 17 | `netsplit` / `netjoin` OpenBao, with `bin/evidence` during the split | Published port does not answer; bundle records the client view as `unreachable` and still holds the true metadata; port answers again after `netjoin` | pass |
| 18 | `bin/evidence` while the control plane is paused | Control-plane config digest `UNAVAILABLE`; worker digest still read, because each node is asked on its own address | pass |
| 19 | `kill` / `start` worker, then `pause` / `unpause` worker | The first request after `start` returns is answered, without a wait; no Talos API answer while paused, answers after | pass |
| 20 | `netsplit` / `netjoin` worker, with `netjoin` tried first while the record of the Talos network is moved aside, then with the worker attached by hand at another address, then a second `netjoin` once joined | No answer while detached; the `netjoin` without the record is refused and the worker stays detached; attached at another address, the connect is refused, the state read back is not the one asked for and the action is recorded `failed`; detached again and joined, answers again on the same address; the second `netjoin` is refused by Docker and recorded `done`, the worker being at its address | pass |
| 21 | `bin/evidence` with a scan path that does not exist, then with a mode-000 file whose name holds a secret, then with a mode-000 database dump in `.state/backups` whose name holds a secret (the last two skipped when run as root) | Refuses all three, non-zero exit; the second message withholds the name, and stderr holds no secret in either case | pass |
| 22 | Evidence with the positive control replaced by a symlink to a file of the same content outside the scanned paths, then with a hard link to the control outside them, then with `.state/evidence` replaced by a symlink, then with `.state/secrets.env` replaced by a symlink, with a command substitution appended to it and with a second hard-linked name, then with a second name for `bao-init.json` and for `injections.log`, then with `GIT_DIR` pointing at no repository, then with the control's canary replaced by another secret, then with a hard link to a store snapshot archive and then with a dump name that is a symlink to a file outside `.state`, then with `scan-patterns.txt` cut down to the prefix, then with `secrets.env` short of its canary line while the caller's shell holds that variable, then with `.cache` replaced by a symlink, then with what an interruption leaves (a complete dump, a complete archive and a truncated archive under their `.partial.` names, a `store-restore` staging directory holding a copy of the control, and a database under the `db-restore` scratch name `bronzeward_previous`), then with the live store directory moved aside as a killed `store-restore` leaves it, then with a `store-restore` staging directory that is a symlink to a directory outside `.state` holding a copy of the control, then with an untracked symlink in the checkout, then with a tracked file (`README.md`) replaced by a symlink to the moved file, then with an untracked file whose name holds a secret hidden by a global excludes file, then with an untracked directory whose name holds a secret that git cannot open, then with `.state/evidence` writable but not readable (both skipped as root), then with an empty untracked directory in the checkout, then with a clean filter and then a `text` attribute given to a tracked file (`README.md`) by an attributes file outside the repository, then with a clean filter given to an untracked file the same way, then with `README.md` flagged assume-unchanged, then with the creation record `up-fixtures-diff.txt` moved aside, then with a rule appended to `fixtures/.gitignore` that hides an untracked file holding the canary, then with an untracked directory whose own `.gitignore` ignores everything in it; then evidence and leak scan on the healthy fixture, under `GIT_EXTERNAL_DIFF` set to a helper that prints nothing, `core.autocrlf=true` in the caller's configuration with `README.md` turned to CRLF, `status.showUntrackedFiles=no` and `core.ignoreCase=true` with an untracked `README.MD` beside the tracked `README.md` (planted only where the filesystem tells the two apart), then `core.trustctime=false` and `core.checkStat=minimal` with `README.md` changed to bytes of the same length and its mtime put back, then a symlink to `.state` in a directory of its own given as an extra path, then a gitlink staged under `fixtures/` and a nested `.git` directory under it, then a replacement ref (`git replace`) for the commit pointing at its parent, then `core.fileMode=false` in the caller's configuration with `bin/inject` made non-executable, then `core.symlinks=false` in the caller's configuration with a symlink staged under `fixtures/` and a regular file holding the target's name at its path, then with the cached `talosctl` replaced by a stand-in that reports the pinned version and leaves a mark when run, then with an earlier bundle marked incomplete whose `scanned-by.txt` is a symlink to a file outside it, then with an earlier bundle whose `.incomplete` marker is a symlink to nothing, run from another directory with the relative scan paths `-delete` and `-delete.d`, with the live store directory, a root already, as an extra path and a symlink to it as another, a symlinked file under `.state/data`, a leaking file named `canary-control.txt` outside `.state/data`, a benign file with a secret in its name, a scan path that is a symlink with a secret in its name, a symlink with a benign name and benign content behind it whose stored target path holds a secret, a leaking file whose name holds a newline, a file with a secret in its name and the canary in its content, an untracked text file, an untracked binary file and an untracked file under a nested directory named `.state` in the checkout, a sibling bundle marked incomplete that holds the canary and two exact copies of the control as expanded store snapshots, one with the archive's digest recorded next to it and one with another digest, a copy of the store archive under a name holding a backslash, a sibling bundle marked incomplete whose marker another process holds locked and which holds a leaking file, and a planted copy of the `talosconfig` client key | The symlinked and the hard-linked control each fail the capture for want of its control; the symlinked evidence directory is refused and nothing is written through it; the symlinked, the appended and the hard-linked `secrets.env` are each refused and the appended command does not run; the hard-linked `bao-init.json` and `injections.log` are refused; the capture without git fails; the replaced control is reported as a leak and the capture fails for want of its control; the hard-linked archive and the symlinked dump are each refused and nothing is expanded; the cut-down pattern list is refused; the incomplete `secrets.env` is refused rather than completed from the environment; the symlinked cache directory is refused; the interruption leftovers are each reported: the dump expanded from its temporary name, the control copy in the staging directory, and the canary in the scratch database's dump, while the truncated archive is named in `unavailable.txt` and the control's copy from the complete archive under its temporary name is not a leak; with the live directory aside the capture succeeds, names the absent directory in `unavailable.txt` and does not report the control set aside; the symlinked staging directory is refused; the untracked symlink, the tracked file replaced by one, the hidden untracked file (whose name the refusal withholds), the directory git cannot open (whose name git's own warning holds and the refusal withholds), the unreadable bundle directory, the empty directory, the filtered file, the `text`-attributed file, the filtered untracked file, the assume-unchanged file, the missing creation record, the modified ignore file and the untracked one each fail the capture; in the last run all are scanned and reported, the incomplete sibling's file and its control copy under the other digest among them while its copy under the archive's digest is not, the sibling is marked scanned by this bundle and no longer incomplete, the locked sibling's file is not reported and that sibling stays marked incomplete with no scanned-by mark, the control's copy from the backslash-named archive is not reported, the bundle is no longer marked incomplete, nothing is deleted, no entry appears twice, `versions.txt` says the tree differs from the commit and names the commit the fixture was created at, and the bundle's `fixtures-diff.txt` carries the untracked text file's content, the nested file's, the case-differing file's, the binary file as a binary patch, not as "differ", and the CRLF bytes of the turned file, and the same-length change with its mtime put back; nothing is reported under the symlinked spelling of the live store; under the symlink to `.state` the control is listed once at its own spelling, not as a leak, nothing under the symlink's spelling and no entry twice; the staged gitlink and the nested repository are each refused, naming them; under the replacement ref the capture succeeds, the diff is byte for byte the one taken without the ref and `versions.txt` names the real commit; the executable bit taken is in the diff as `new mode 100644`; the regular file where the index has a symlink is in the diff as `new file mode 100644`, not `120000`; the stand-in is refused against the manifest's digest and never run; the linked scanned-by mark is refused before anything is scanned, the file behind it keeps its content, the link stays and the bundle stays marked incomplete; the dangling marker is refused as a symlink and left; the file that only shares the control's name is reported as a leak, the control's copy inside the expanded store snapshot is not; exactly the three secret-bearing names are reported, with the names withheld, and the report itself holds no secret; the file whose name holds a newline is reported on one line, in quoted form, and every line of the report is one entry; the client key is found; no `unavailable.txt`; client view `reachable`; finds the canary in the live dump, in the expanded backup and in the copied PostgreSQL data directory, whose `pg_wal` is in the bundle; finds a planted copy of the OpenBao metadata-only token and a plain file planted next to the OpenBao snapshots in `.state/backups`; lists the Transit key; finds nothing in container logs | pass |
| 23 | `bin/down` with `.state/down-node-volumes` replaced by a symlink and `down-node-volumes-incomplete` a symlink to a file outside `.state`, then with a second hard-linked name for the record, then with the marker a dangling symlink, then with a name in the record that is no anonymous volume's, then with the name of a labelled volume created for the test, then with a never-started container created for the test that carries only the Talos cluster-name label, then with the PostgreSQL container renamed aside and a never-started one created under its name carrying the Compose project's labels (`down` and `inject pause postgres`), then with one that carries the fixture claim label and this checkout's owner label under another name, then with the fixture-name record naming another fixture, then with the daemon record naming another Docker daemon (`down` and `inject pause postgres`), then with the daemon record moved aside (`down` and `inject pause postgres`), then with a line appended to the record of `versions.env` (`down` and `inject pause postgres`), then with a second hard-linked name for the record of `compose.yaml` (`down` and `inject pause postgres`; the record is first held byte for byte to the file), then with `versions.env` short of its `OPENBAO_PORT` line while the shell exports that value (`down` and `inject pause postgres`), then with that line holding a shell expansion of the same port (`down` and `inject pause postgres`), then with a line setting a key the manifest does not define (`STATE`) and then with its `OPENBAO_PORT` line set a second time (`down` and `inject pause postgres` each), then with `.state/backups` replaced by a symlink to the moved directory, then with the record of the Talos container IDs moved aside, then with the record of the Compose volumes moved aside and then replaced by one naming another run (`down` and `down --adopt`), then with a network created for the test that carries only the Talos label, then with one that carries only the Compose project's labels, then with a database snapshot hard-linked outside `.state`, then with a dot-named symlink in `.state/backups`, then with `.state/talos` replaced by a symlink to the moved directory, then `bin/down` with the record cut short of its last newline, then `bin/down` with an unlabelled volume named `bw-fixture_openbao-data` created for the test, then `bin/down` on a `.state` made of the daemon record and a leftover file alone, as a removal cut short leaves it, then on an empty `.state`, as one cut short after the daemon record leaves it, then on one of the daemon record and the two Compose records, as one cut short among the last records leaves it, then the daemon's whole volume list against the one taken before `up`; before all that, that `up` left no unrecorded-cluster marker | No marker; the first twenty-seven `down` refuse, remove nothing and leave `.state`, each `inject` refuses and pauses nothing, the file behind the marker link keeps its content, the labelled volume, both containers and both networks still exist, the Compose volumes survive the three runs without their record or against it, and both Talos containers survive the run without theirs, and the `STATE` line makes no directory where it points; after the twenty-eighth no container, network, volume or state directory is left; the twenty-ninth refuses and the unlabelled volume still exists; the thirtieth, thirty-first and thirty-second remove the cut-short directories; no volume exists that did not before `up` | pass |
| 24 | `up`, `evidence` and `down` with `.state` replaced by a dangling symlink | Each refuses; nothing is created at the link's target | pass |

After the second run of the first revision, an independent `docker volume ls --filter dangling=true` showed exactly the
volumes that existed on the host before the first run.

Check 8 is worth a note for [key-loss testing](https://github.com/ginsys/bronzeward/issues/10): a
provider snapshot restores a *destroyed* secret version and a *deleted* Transit key. "Lost" in the
current provider and "recoverable from a provider backup" are separate facts, as
[design §7.6](../Talos_Configuration_and_Machine_Management_Design.md#76-metadata-only-dependency-checks)
already words it.

Check 3 matters for the [execution contract](../../spec/execution-recovery.md): an observed
configuration digest can equal the digest of the bound artifact, but only under a stated
normalization. The read path used here adds one newline.

## 5. Failures hit while building it

Each of these changed the fixture or was answered; whether it was reproduced in the selftest,
on its own, or not at all is stated per item, and a finding rejected is recorded as such.

1. **Subnet inside the Kubernetes service range.** With `--subnet 10.98.76.0/24`, which lies in the
   default service range `10.96.0.0/12`, the cluster never bootstrapped. Talos logged the
   `address-overlap` diagnostic and excluded the node address from its API certificate, so every
   client failed with `x509: certificate is valid for 127.0.0.1, not 10.98.76.2`. The manifest now
   pins `10.55.0.0/24` and says why.
2. **`talosctl cluster destroy` cannot clean a failed create.** When `cluster create` fails before
   writing `state.yaml`, `destroy` exits with `failed to read cluster state`, leaving the network,
   an empty state directory and anonymous volumes. `bin/down` therefore removes containers and
   networks by the `talos.cluster.name` label as well.
3. **Anonymous node volumes.** Talos node volumes carry no label. `bin/down` records the volume
   names of fixture containers before removing them. Volumes from a create that failed before the
   container existed cannot be attributed and are left; this is a stated limit.
4. **`cluster create` writes outside its state directory** unless told not to: the talosconfig and
   kubeconfig go to the user's own files by default, and the QEMU provisioner drops
   `controlplane.yaml` and `worker.yaml` with live secrets into the working directory and caches
   ISOs under the user's home. The fixture sets `TALOSCONFIG`, `KUBECONFIG` and
   `--talosconfig-destination` into `.state/`; the caller's own Talos config was verified unchanged
   (same size and modification time before and after).
5. **JSON patches are refused** for the multi-document machine configuration of v1.13
   (`JSON6902 patches are not supported for multi-document machine configuration`). Experiments
   must use strategic merge patches.
6. **Compressed backups hide leaks.** A `pg_dump --format=custom` file is compressed, so scanning
   it as bytes reports no canary even when the database holds one. `bin/evidence` expands database
   dumps and store archives before scanning. OpenBao snapshots are encrypted by OpenBao's barrier;
   they are scanned as written, and a clean result there says only that the barrier is on.
7. **A host hook restarted the Docker daemon on every new bridge.** On the machine used, a
   NetworkManager dispatcher script restarts Docker whenever an interface comes up, including the
   bridge of a network Docker itself just created. Each `docker network create` therefore stopped
   every running container without a restart policy: first seen as `failed to populate volume …
   database not open` during `cluster create`, then as PostgreSQL and OpenBao exiting cleanly at the
   second the Talos network appeared. This is a host defect, not a fixture property, so the fixture
   does not work around it; `bin/up` now refuses to report success when a container has stopped,
   and the README names the symptom. Before the hook was corrected a run from clean failed at
   check 1 every time; the two passing runs in section 4 were made after the hook was changed to
   ignore `br-*`, `veth*` and `docker*` interfaces.
8. **The first revision's evidence capture failed exactly when it was needed.** Review of the
   first revision found that `bin/evidence` aborted on the live database dump when PostgreSQL was
   killed, so no bundle existed for the crash cases E1 and E4 are about; that a file or path the
   scan could not read was skipped and read as clean; that a failed OpenBao listing produced an
   empty metadata file, which reads as "no secrets"; and that the OpenBao metadata-only token was
   not a scan pattern. Checking the pattern list against the real secrets bundle then showed the
   Talos bootstrap token was missing as well. `bin/evidence` now records each unreadable source
   in `unavailable.txt` and continues, expands database backups without the server, treats any
   unreadable scan input as fatal, and writes an explicit `unknown` record for a provider it
   cannot ask; `bin/up` refuses a pattern list that is implausibly short and drops patterns under
   eight characters, since an empty pattern matches every line. Checks 10, 13, 21 and 22 hold these.
9. **"Nothing found" and "could not look" kept collapsing into each other.** A second review round
   found three more instances of the same defect class. A Transit key listing that failed became
   an empty list. OpenBao snapshots were claimed to be scanned but lay outside the scan roots.
   `bin/down` reported "nothing left" when its Docker queries failed, because a failed query lists
   nothing. Now a listing that returns non-zero is recorded as unknown and marks the bundle
   incomplete (the CLI uses one exit code for empty and for failed, so empty is never assumed);
   `.state/backups` is scanned as written; and `bin/down` refuses to start without a Docker daemon
   and exits non-zero when any verification query fails. Observed with a token denied the Transit
   listing (unknown record, bundle marked incomplete) and with a stand-in `docker` that fails,
   first from the start and then only on the verification queries (exit 1 both times, and no
   "nothing left" message).
10. **Inputs the scripts did not expect.** A third round found ways the caller's environment could
    void a result. A relative scan path named like a `find` action (`-delete`) was executed by
    `find`, not scanned; extra paths are now made absolute first. A symlinked file or directory
    was skipped silently; the scan now follows links and fails on one it cannot follow. Two
    captures in the same second shared a bundle directory and a temporary list; each now gets its
    own atomically. A `BAO_TOKEN` in the caller's shell overrode the fixture's root token; it is
    now cleared. Every Talos read went through the control plane, so a dead control plane made a
    healthy worker look dead; each node is now asked on its own address, which a worker answers
    even while the control plane is paused (check 18). And two checkouts on one Docker daemon
    share every name, so `down` in one would have removed the other's fixture: `up` now refuses
    while fixture containers exist, and `down` refuses a fixture it cannot show to be its own
    unless told to adopt it. Per-checkout names were considered and rejected, because ports and
    subnet are pinned for reproducibility and would collide anyway; the limit is one fixture per
    daemon. Each was observed once against a live fixture; the first two are held by check 22.
11. **Success reported before it was earned, and state discarded before it was safe.** A fourth
    round found the remaining places where a step's outcome was assumed. `bin/down` deleted
    `.state/` before it verified the teardown, so a `down` that could not finish left resources
    this checkout could no longer show to be its own, and the retry needed `--adopt`; the state
    directory now goes last, only after a verified clean teardown, and remembers the anonymous
    node volumes across runs. The ownership check of `down` and the pre-flight check of `up`
    looked at containers only; both now count fixture networks and volumes, because a leftover
    PostgreSQL volume keeps the password of the run that initialized it and a new run would
    start a database its own password cannot open. `bin/up` reported the fixture up on the
    strength of containers running; it now asks OpenBao an authenticated question and PostgreSQL
    a password-authenticated one from a second container, since the image trusts every
    connection from inside the server's own container. The PostgreSQL health check went through
    the unix socket, where the image's temporary initialization server also answers; it now
    uses TCP. `bin/inject` logged an action before running it and never said how it ended, so a
    refused `kill` read like one that happened; it now adds a `done` or `failed rc=N` record.
    Snapshots were written straight to their final name, so an interrupted one looked like a
    snapshot to a later restore; they are now written aside and renamed on success (check 11).
    The OpenBao unseal key was a scan pattern in base64 only; its hex form, which
    `operator init` returns next to it, is now one too. And the scan took any file named like its positive
    control for the control; only the control itself and its copies in expanded store snapshots
    count now (check 22). Observed against a live fixture: with an unrelated container holding
    the Compose network, `down` exits 1, names the network and keeps `.state/` with twelve node
    volumes remembered, and a plain `down` afterwards finishes; with only a labelled volume and
    no `.state/`, `up` and `down` refuse and `down --adopt` removes it. The end-of-`up`
    readiness checks were observed only in their passing state. One more input from the caller's
    environment, of the kind in item 10: a `COMPOSE_PROJECT_NAME` in the caller's shell wins over
    the `name` in `compose.yaml`, which would have put the services under another project label
    than the one every ownership check and teardown probe selects by. The project name is now
    passed explicitly; `selftest` passed 20 of 20 from clean with that variable set to another
    name. And a `.state` that is a symlink: every command loads the shared setup first, which
    sourced `secrets.env` through the link, and a dangling link read as "no state" to `up`. The
    fixture never creates such a link, so the shared setup now refuses one before it reads
    anything (check 24).
12. **What the fixture knows about itself was still incomplete.** A fifth round. The client
    private keys in the generated `talosconfig` and `kubeconfig` are made for this cluster's admin
    clients and are not part of the secrets bundle, so the scan could not find them; both are
    patterns now, and `up` fails if either config holds no key where one is expected. The scan
    counted matching lines, so a positive control that gained a second secret on its one line
    still counted one and was excused; it counts occurrences now (the first held by check 22). `down`
    learned the anonymous node volumes only from containers that still existed; `up` now records
    them right after creating the cluster. Observed: with the Talos containers removed by hand
    and all 12 volumes left, `down` removed the 12 and verified it. Two `up` started together
    could both pass the look-before checks; a plain `mkdir` of `.state/` is now the claim, and
    the hint to run `down` is printed only by an `up` that holds it. Observed: of two started
    together one came up and the other refused without that hint. And two more inputs from the
    caller's shell: with `CDPATH` set, `cd` prints the directory it found, which doubled the
    path the scripts resolve themselves by; and with `http_proxy` set, the client-view probe of
    OpenBao went to the proxy, whose answer would have read as `reachable`. `CDPATH` is emptied
    for that one `cd`, and every request to localhost bypasses proxies. `selftest` passed 21 of
    21 from clean, called by relative path with `CDPATH=.` and an `http_proxy` nothing listens on.
13. **Recovery paths that could destroy, and requests that could hang.** A sixth round.
    `store-restore` deleted the live store directory before it knew the archive could be read; it
    now extracts aside and swaps only on success (check 9). The OpenBao snapshot and restore
    requests had no time limit, and a paused OpenBao accepts the connection without ever
    answering; they are bounded now, so the action ends and is logged as failed (check 15). The
    same went for every request `bin/evidence` makes after its first, bounded, one: all of them
    now run under a timeout, and one that times out is recorded as unknown or unavailable like any
    other failure. The positive control was recognized by path and hit count, so a control whose
    canary had been replaced by another credential still passed for the control; it is now
    compared byte for byte with what `up` planted, which also covers the two-secrets case of item
    12 (check 22). A secret in a file or directory name was never looked at; names are matched
    now, and a name that holds a secret is withheld from the report, which records the inode
    instead (check 22). `down` trusted that its record of the node volumes could be written and
    read back; both are checked now. The same-checkout claim of item 12 did not stop two
    checkouts from starting at once, so `up` now first creates a container that is never
    started, `bw-fixture-claim`: Docker refuses a second one of that name atomically, and its
    label names the owning checkout, which gives `down` an owner mark that also covers the
    Talos containers. Observed: with a claim labelled for another directory, `up` and `down`
    refuse and `down --adopt` removes it; with a claim from this checkout and no `.state/`, a
    plain `down` removes it; of two `up` started together one came up and the other reported
    Docker's name conflict. Two checkouts racing were not exercised; the name conflict is the
    same mechanism. And the selftest drew its node label from `$RANDOM`, which can repeat and
    then fails the "digest changed" assertion; it uses the clock now.
14. **The previous round's fixes, held to their own standard.** A seventh round, six findings, and
    one the new check for them found. The claim container of item 13 is made from the PostgreSQL
    image, which declares a data volume, so Docker created an anonymous volume for a container
    that never starts, and removing the container left it: four such volumes were on the host
    after the runs of item 13, and `down` had reported a clean teardown each time, because no
    label or record pointed at them. The claim is now removed with its volumes, and the selftest
    compares the daemon's whole volume list before `up` and after `down` (check 23).
    `store-restore` still removed the live directory a moment before it moved the new one into
    place; it now renames the live one aside and removes it last, and if the action ends between
    the two renames its exit handler moves the earlier store back. That window was not exercised;
    observed separately, bash runs the exit handler on `SIGTERM` and `SIGHUP`. After a `SIGKILL`
    the earlier store stays in the staging directory, and the next `store-restore` names it and
    refuses (observed with such a directory planted: exit 1, both copies untouched). `db-restore` had the same defect as the `store-restore` of item 13: it dropped the
    live database before it knew the dump could be read. It restores into a scratch database and
    swaps only on success (check 4). The waits in `up` and `inject` checked their deadline only
    between attempts, so an attempt that never returned was never timed out; `bao`, `pg` and
    `pg_client` are now bounded for every caller, and a wait gives each attempt what is left of
    its limit (check 16, against an OpenBao server process stopped inside its container, which a
    container pause does not reproduce because `docker exec` then fails at once). `inject` and
    `evidence` took the presence of `.state/secrets.env` for ownership; with this checkout's
    resources removed by hand and another checkout's fixture running under the same fixed names,
    a kill or a restore would have gone to that one. Both now require the claim to name this
    checkout (check 2). And the error paths of `evidence` printed raw paths, past the rule of
    item 13 that a secret-bearing name is never written out; they go through the same function
    now. The check written for that (21) then failed on a leak nobody had reported: `grep` names
    the file it cannot read in its own message. The messages of `grep`, `find`, `tar` and
    `realpath` are suppressed in `evidence`; their exit status still decides, and the message
    that follows says what failed.
15. **Four more of the same two kinds, and two findings that were wrong.** An eighth round. The
    `db-restore` of item 14 still had a moment, between dropping the live database and renaming
    the restored one, in which no database of the configured name existed; the swap is now two
    renames in one transaction, so the name always points at the old database or the new one,
    and what an interrupted run leaves under the two other names is dropped at the start of the
    next (check 4; a swap with a client connected was not exercised). The `store-restore` of
    item 14 assumed a live directory to move aside, and failed in exactly the case a store
    snapshot is for, the directory being gone (check 9). The leak scan recognized the control's
    copy inside an expanded store snapshot with a regular expression built from the bundle path,
    so a `+` or a parenthesis in the checkout path, which a worktree directory named after a
    branch readily has, made the copy a reported leak; it is compared as a string now. Observed:
    `selftest --keep` passed twice with the checkout reached through a directory named
    `a+b.(c)`; the earlier code was not run there. Extra scan paths were resolved before the
    scan, so a path that is a symlink lost its own name, the one place a secret in it could be
    found; they are made absolute without resolving links (check 22). That run also showed that
    relative scan paths are spelled from the physical working directory, unlike `.state` when the
    checkout is reached through a symlink; two selftest assertions compared whole paths and now
    compare the tail. A `jq` assertion in check 13 read only the last record of a file; it reads
    all of them. Two findings were rejected after checking: that `bin/evidence` and `bin/down`
    run a `talosctl` from `PATH`, not the pinned one. In both, `talosctl` is the shell function
    of the shared setup, which runs the pinned binary; the bundle of the run above records
    `Client: Talos v1.13.6`. `bin/down` says so in a comment now, and also why the status of its
    removal steps is not checked: the probes after them decide.
16. **One more input from the caller's shell, one more way to fool the control, one more outcome
    recorded wrongly.** A ninth round, three findings, each of a kind already listed. A
    `TAR_OPTIONS` in the caller's shell reaches every `tar` the fixture runs, and one holding an
    `--exclude` made `store-snapshot` write, without any error, an archive that lacked part of
    the store; it is cleared in the shared setup next to `BAO_TOKEN`, together with the `GZIP`
    that `tar` hands to `gzip` (check 9). A positive control replaced by a symlink to a file
    outside `.state/data` with exactly the planted content passed for the control, because both
    the walk and the comparison follow links, and the canary then lived where teardown does not
    reach; the control and its directory must not be symlinks now (check 22). And `db-restore`
    dropped the superseded database as a fatal last step, so a failure there made the log say
    `failed` about a restore that had succeeded; the swap decides now, and the next `db-restore`
    drops what was left. That last case was not exercised.
17. **A mark of ownership that was none, a teardown that vouched for volumes it never looked for,
    and four smaller ones.** A tenth round, six findings. `bin/down` took a `.state/` for proof
    that the resources on the daemon were this checkout's, for the sake of a fixture that had lost
    its claim; but a stale `.state/` next to the rest of another checkout's fixture passes the
    same test, and `compose down --volumes` would then delete that checkout's volumes. The one
    ordinary way to lose the claim was `down` itself, which dropped it before verifying; it drops
    it last now, so the claim is the only mark, and without it `down` refuses unless `--adopt` is
    given (check 2). `down --adopt` without the owning checkout's record, after Talos containers
    were removed by hand, cannot find their anonymous volumes and still ended with "no volumes
    left"; keeping the names on the daemon was considered and left out as more machinery than a
    disposable fixture warrants, so the limit stays and the last line of `down` now says what it
    did not look for. `inject start` on a Talos node returned when the container had started,
    not when the node answered, so a capture right after `done` could record a recovering node
    as unavailable; it waits for the node's own address now (check 19, which also is the first
    time a Talos node was killed and started here). A hard link to the positive control passed
    where the symlink of item 16 no longer did; the control must have one name (check 22). A
    symlinked `.state/evidence` sent every bundle outside the checkout, where teardown does not
    reach; `data`, `backups` and `evidence` are refused as symlinks along with `.state` itself
    (check 22). And `bin/up` built its two chosen secrets in a command substitution used as a
    `printf` argument, the very shape its own comment on the metadata token warns about: a failed
    read of the random source would have left the constant prefix as the PostgreSQL password,
    with `up` reporting success. The value is assigned and measured first. That failure was not
    provoked.
18. **Links again, the caller's environment again, and two outcomes reported before they were
    checked.** An eleventh round, six findings, all from one reviewer; the advisory review found
    nothing. A snapshot's final name that was a symlink to a directory received the finished
    snapshot inside that directory, outside `.state`, with the action logged as done; every
    rename that publishes or swaps something now refuses to treat its destination as a directory
    (check 9). `store-restore` tested the extracted `data` with a test that follows links, so an
    archive holding `data` as a symlink was installed as the store, the real one was removed for
    it, and every later command refused the result; it must be a real directory (check 9). The
    leak scan read what a symlink points at and what it is called, but not the path it stores,
    so a secret held only there went unreported; the stored target is matched too (check 22).
    A curlrc in the caller's home could set `output` and make `bao-snapshot` publish an empty
    file with exit status 0; every `curl` of the fixture now ignores it, through one wrapper in
    the shared setup, and the prerequisite check was changed to look for executables only, since
    a wrapper function would otherwise have satisfied it (check 8). `bin/up` wrote its record of
    the node volumes in place, so an interruption could leave a partial record that `bin/down`
    takes for complete; it is written aside and renamed. And `down --purge` reported success
    without checking that the cache was gone. Observed outside `selftest`: with an entry in
    `.cache` that cannot be removed, `down --purge` exits 1 and names the directory, and
    finishes once the entry is removable. The interrupted record was not provoked.
19. **A file of secrets run as code, a name that was a path, and two more links.** A twelfth
    round, four findings from one reviewer and one accepted from the advisory review. The shared
    setup sourced `.state/secrets.env` as shell code before any ownership check, so a line added
    to it, or a file put in its place, ran with every command; the file is now read as data, only
    the four assignments `bin/up` writes are accepted, and it must be the regular file `up`
    wrote (check 22). A snapshot name with a slash was a path, so `store-snapshot a/b` wrote
    outside `.state/backups`; names are one path component (check 9). `bin/down` appended to
    and read the node-volume record through whatever `.state/down-node-volumes` was, so a
    symlink there would have chosen which volumes `docker volume rm` removes; a symlink or any
    non-regular file is refused before anything is removed (check 23). And the leak scan excused
    a control copy inside an expanded store snapshot without asking whether the archive it came
    from was the fixture's own; a hard-linked or symlinked archive no longer excuses its copy
    (check 22). The advisory review also reported that `down` never refuses a symlinked
    `.state`; that was rejected, since the shared setup refuses it for every command and check 24
    already exercises `down` (its second finding, a fragile `-newer` comparison in the selftest,
    was replaced by comparing directory listings).
20. **Hard links where symlinks had been refused, and what an interruption leaves unscanned.** A
    thirteenth round, eight findings from one reviewer; the advisory review found nothing. Two
    are the previous round's fixes held to the hard-link test they had not been: `secrets.env`
    and `down-node-volumes` must each have exactly one name, since a hard link is no symlink and
    is a regular file (checks 22 and 23). Four concern evidence after an interruption, which is
    what the fixture exists to capture: a dump or archive killed before its rename survives under
    its `.partial.` name, which the expansion never matched, and a custom-format dump matches no
    pattern as written; a `store-restore` killed after extraction leaves a plaintext staging
    directory outside every scan root; a `db-restore` killed after restoring aside, or whose
    post-swap drop failed, leaves `bronzeward_restore` or `bronzeward_previous` undumped; and a
    `scan-patterns.txt` thinned after `up` was trusted as it was, so a scan could pass with a
    token unmatched. Evidence now expands partial names as far as they go, scans every
    `store-restore.*` directory, dumps both scratch databases when they exist, and rebuilds the
    pattern list from the generated credentials, refusing a file that differs (check 22). Then
    the download cache: `curl --output` writes through an existing symlink, so a linked
    `.cache/download-<tool>` entry would have sent a download into whatever file it named; the
    download lands in a file of its own and is renamed once its checksum passed. Observed outside
    `selftest`: with `download-age` a symlink to an unrelated file, `fetch` exits 1 and the file
    is untouched; with the entry removed, the download completes and leaves no partial. Last, an
    argument holding a newline wrote records of its own into `injections.log`; no argument may
    hold a control character, refused before anything is logged (check 9).
21. **The previous round's fixes, each one step further.** A fourteenth round: six findings from
    one reviewer and three from the advisory review, of which one was rejected and two accepted.
    The hard-link test was extended from the two files it covered to every credential file
    `up` generates, since a hard-linked `bao-init.json` keeps the root token past teardown just
    as `secrets.env` would (check 22); to a snapshot about to be replaced by one of the same
    name, whose earlier archive would otherwise live on under its other name (check 9); and to
    the marker `down` writes when the node volumes are not all known, which it now publishes by
    rename instead of writing through whatever is there (check 23). The scan of what an
    interruption leaves was corrected twice: a complete archive under its temporary name expands
    under that name, which the archive-ownership test then spelled wrongly and reported the
    control's copy as a leak; and with the live store gone, as a `store-restore` killed between
    its renames leaves it, the walk failed on the absent root and voided the scan of the copy
    set aside, so the live directory is a scan root only while it exists, its absence is named
    in `unavailable.txt`, and the control is taken from the copy in the staging directory (check
    22). The exit handler of `store-restore` moved `previous` back whenever the live directory
    was gone, so an archive holding an entry of that name, restored while the store was gone,
    had that entry installed as the store after the archive was refused; the handler now moves
    back only what its own run set aside (check 9). The control-character guard in `inject`
    skipped the action word, and an unknown action was logged before it was refused (check 9).
    And the checkout's path was taken as spelled, so the same checkout reached through a symlink
    did not own its own fixture; it is resolved to the physical path. Observed outside
    `selftest`: `up` through a symlinked path, `selftest --keep` through the symlink and through
    the real path, and `down` through the real path, with the claim naming the physical checkout.
    The rejected finding was, for the third time, a `talosctl` call said to come from the host's
    `PATH`; it is the `lib.sh` function running the cached binary.
22. **The record trusted with `docker volume rm`, and the rest of the one-name rule.** A fifteenth
    round: seven findings from one reviewer and one from the advisory review, all accepted. The
    node-volume record in `.state` names what `down` removes, and it can be written by anyone who
    can write there; `down` now holds each name to what Docker says of a Talos node volume, an
    anonymous one with no label, and refuses the record otherwise (check 23). A first version
    also required the volume to be no older than the claim, and failed its own selftest: check
    2 re-takes the claim after the cluster exists, so a claim newer than the node volumes is a
    state the fixture itself produces. What the remaining test does not close is stated in the
    README: any unlabelled anonymous volume on the daemon is indistinguishable. The one-name
    rule reached `injections.log`, which `inject` now refuses as a link before it records anything
    (check 9), and `.cache`, refused as a symlink like the state directories (check 22). A
    `secrets.env` short of one assignment, as an `up` interrupted while writing it leaves it, fell
    through to a variable of that name in the caller's shell; the four are cleared before the
    file is read and all four are required (check 22). The rebuilt pattern list was compared
    through a process substitution, whose exit status bash does not report, so a source that
    failed halfway would have matched a file cut the same way; it is written to a file and each
    source's status is checked (not exercised: no source was made to fail). A snapshot name that
    holds `.partial.` would have been read by `evidence` as an interrupted snapshot's temporary
    file; it is refused (check 9). A reported name that holds a newline broke the one-entry-per-
    line report; such a name is printed in bash's quoted form (check 22). And the exit handler of
    `inject` ran its removals on the right of `||` under `set -e`, so a removal that failed ended
    the handler before the outcome was recorded; each removal is guarded and the handler restates
    the action's status (not exercised: no removal was made to fail).
23. **The name is not the container.** A sixteenth round: eight findings from one reviewer and
    two from the advisory review, of which eight were accepted and two rejected. The one rated
    highest: the container names are fixed, so once the real PostgreSQL container was removed by
    hand, anything created under its name answers to `inject kill postgres` while the claim still
    stands. `need_state` now checks that every container answering to a fixture name carries the
    fixture's own labels, the Compose project and directory or the Talos cluster name, and
    refuses otherwise (check 9). The positive control was counted as found before its content was
    compared, so a control overwritten with another secret still let the capture report "positive
    control found"; it counts only once `is_control` passed, so a changed, symlinked or hard-linked
    control now fails the capture instead of being reported as a leak in a passing one, which
    moved the changed-control case out of the healthy run into one of its own and turned the two
    linked-control cases into refusals (check 22). The
    one-name rule reached `injections.log` for `evidence` and `down` too, which never call
    `record` (check 22), and a `store-restore` staging directory that is a symlink is refused
    before the walk can follow it out of `.state` (check 22). `down` read a marker that had been
    replaced by a dangling symlink as no marker, dropping the uncertainty an earlier run recorded;
    a linked marker is refused (check 23). `secrets.env` was written in three appends, so a write
    cut mid-line would leave a file `lib.sh` refuses for every later command, `down` included; it
    is written whole and renamed into place at each stage (not exercised: no write was made to
    fail). The manifest commit in `versions.txt` came from a `git` inside a `printf` argument,
    whose failure left the line empty; the capture now fails without it (check 22). And in
    `selftest` itself, the `docker volume rm` after the labelled-volume test ran unguarded under
    `set -e`, so the case it tests for would have ended the script before its own diagnostic.
    Rejected: the `talosctl` call in `selftest` said to come from the host's `PATH`, for the fourth
    time (it is the `lib.sh` function, sourced on line 7); and a request to mark the previous
    round's additions as unverified, when the run that verified them is the one recorded for that
    revision.
24. **A dump is not the disk.** A seventeenth round: four findings from one reviewer and three
    from the advisory review, of which six were accepted and one rejected. Two rated highest. The
    leak scan covered the database through `pg_dump`, which shows what the server chooses to
    show, never the heap and index files, the write-ahead log or a temporary file the server did
    not remove: the very places E1 has to look. `bin/evidence` now copies the container's data
    directory into the bundle, whether the server runs or not, and the scan covers it with the
    rest; after `kill postgres` the copy is read from the stopped container (checks 10 and 22,
    which finds the committed canary in `pg_wal`). And the Talos containers were removed by label,
    a cluster name any container can be created with, so a container labelled `bw-fixture` by
    someone else went with the fixture's own. `bin/up` records the IDs of the two containers it
    created and `bin/down` refuses while a container carries the label without being one of them;
    `--adopt`, which has no record to go by, removes by label as before (check 23). Smaller:
    `secrets_publish` in `up` renames over whatever holds the name, so a second name made for the
    earlier file between two stages would keep that file reachable past teardown; a linked
    destination is refused (not exercised). `store-restore` treated its second rename as one
    more step, so a staging directory that could not be removed after it made the log say the
    restore failed with the restored store in place; the rename is the commit and a leftover is
    named (not exercised). `db-restore` ended the live database's sessions with
    `pg_terminate_backend(pid)`, which signals and returns, so the rename that followed could fail
    on a backend not yet gone; it waits for each with the two-argument form (not exercised: no
    client was connected). And in `selftest`, the assertion for the scan path `-delete` was a
    prefix match that `-delete.d/...` satisfied too; it is anchored to the whole entry. Rejected:
    the `talosctl` call in `selftest` said to come from the host's `PATH`, for the fifth time.
25. **The record has to be used everywhere the label is.** An eighteenth round: three findings
    from one reviewer, all accepted, and two from the advisory review, both rejected. The record of
    Talos container IDs from the round before was consulted by `down` only when it existed, so an
    `up` interrupted between `talosctl cluster create` and the record's publication, a state a
    stray labelled container can bring about by itself through `up`'s count check, left a plain
    `down` removing by label after all; with labelled containers and no record it now refuses and
    names `--adopt` as the way to remove by label (check 23). `containers_own` still held the
    Talos nodes to the label alone, so a container created under a node's name with that label
    took a `kill`, `pause` or `netsplit`; the nodes are now held to the recorded IDs, and a node
    name with no record is refused (check 9). And in `store-restore`, `moved_aside` was set after
    the rename of the live directory, so a signal during that rename had the exit handler run
    with the flag unset, skip the move back, and remove the staging directory holding the only
    copy; the flag is set before the rename, which is harmless when nothing was moved (not
    exercised). Rejected: `talosctl` said to come from the host's `PATH`, in `selftest` for the
    sixth time and now in `evidence` too; both source `lib.sh`, whose `talosctl()` runs the
    cached binary, and `reported talosctl ...` calls that function.
26. **The commit is the rename, and the label reaches the network too.** A nineteenth round:
    eight findings from one reviewer, all accepted, and one from the advisory review, rejected.
    The cluster network carries the same Talos label as the containers and `down` removed it by
    that label alone; `up` records its ID next to the containers' and `down` holds every labelled
    network to it, refusing by label alone without the record (check 23). A signal during the
    rename that publishes a snapshot, or the one that puts a restored store in place, had bash
    finish the rename and run the exit handler before the flag that would have said so, which
    logged `failed` over an artifact a later restore accepts; the handler now recognizes a
    completed publication by the inode it recorded before the rename, and a completed store swap
    by a flag set before it, and records both as `done` (not exercised). A capture from a tree
    that differs from its commit only said so, leaving the commit unable to reproduce what ran;
    the bundle now carries `fixtures-diff.txt` with the status and the diff (check 22). A backup
    that could not be opened for expansion had bash name it on stderr, with a secret-bearing
    name intact, before `shown` could withhold it; the expansion runs with stderr closed (check
    21). An extra scan path under a root the scan has anyway, `.state/data` say, was walked
    twice and each file under it counted twice; the walks are deduplicated (check 22). A
    snapshot hard-linked outside `.state` after publication survived teardown under the other
    name with nothing to show it; `down` refuses while any file in `.state/backups` is a symlink
    or has a second name (check 23). And `.state/talos` was not among the directories refused as
    symlinks, so `talosctl cluster destroy` would have followed a link to someone else's state;
    it is, and `down` refuses a linked cluster directory or `state.yaml` inside it as well
    (check 23). Rejected: `talosctl` in `evidence` said to come from `PATH`, for the seventh
    time counting `selftest`.

27. **Each fix of the last round, one step further.** A twentieth round: three findings from
    one reviewer and two from the advisory review, all accepted, and one from the advisory
    review, rejected. `fixtures-diff.txt` named an untracked file but not its content, so the
    commit plus the file still could not reproduce what ran; the untracked files are now
    appended as diffs against nothing (check 22, with an untracked file planted in the
    checkout). A file with a secret in its name and another in its content was reported by
    both scan passes, so the count of leaks was off by one; the name pass skips what the
    content pass reported (check 22, with such a file planted). The `db-restore` swap is one
    transaction the server may commit while the signal ends the client, which the exit handler
    logged `failed`; it now asks the server whether the scratch database still exists under its
    name and records `done` when it does not (not exercised). The one-name check over
    `.state/backups` at teardown used a glob, which skips a dot-named entry; `down` asks `find`
    (check 23, with a dot-named symlink planted). And the handler's recognition of a completed
    store swap required the staging name still set, which the cleanup after the swap had already
    cleared, so a signal during that cleanup logged `failed`; the guard no longer needs it (not
    exercised). Rejected: `talosctl` in `evidence` said to come from `PATH`, for the eighth time.

28. **What the previous item left: the binary file, the second network, the unanswered
    request, the missing record, the cut append.** A twenty-first round: five findings from one
    reviewer, all accepted, and two from the advisory review, both rejected. The untracked-file
    diff of item 27 was made without `--binary`, so a helper that is not text came through as
    "differ"; `--binary` (check 22, with a file holding a NUL planted). `up` recorded every
    network of the Talos label, so one made by someone else during the minutes of cluster
    creation would have been recorded as the fixture's and removed with it; exactly one is
    required. A `bao-restore` whose request went out and got no answer was logged `failed`
    while OpenBao may have applied it; the exit handler tells curl's "no connection" and "HTTP
    error" statuses, after which nothing was applied, from a timeout, a dropped connection or a
    signal, which it now logs as `unknown rc=N` (not exercised: the one way to provoke it in the
    selftest, a paused OpenBao, would apply the buffered request on unpause and roll the
    fixture back under the following checks). An `up` interrupted after `talosctl cluster
    create` made its volumes and before the record was published, followed by a hand removal of
    every labelled resource, had `down` find nothing labelled, take the volumes as known and
    remove `.state`; `up` now writes the not-all-known marker before creating the cluster and
    clears it once the records are published, so such a `down` keeps `.state` and says what it
    did not look for (check 23 asserts the marker is gone after `up`; the interrupted `up` is
    not exercised). And `down` appended to the node-volume record in place, so an append cut
    short left a name without its newline for the next run to append onto and then refuse; the
    record is rewritten whole and published by rename, and one cut short of its last newline is
    read with the newline supplied (check 23). Rejected: `talosctl` said to come from `PATH` in
    `evidence` and in `selftest`, for the ninth and tenth time; both source `lib.sh`, whose
    `talosctl` function runs the pinned binary.

29. **The bundle nothing scanned, the other three mutations, the network under the name.** A
    twenty-second round: three findings from one reviewer, all accepted, and two from the
    advisory review, both rejected (the same two as in item 28, for the eleventh and twelfth
    time). A capture ended before its leak scan, by a signal or one of the refusals above, left
    a bundle holding a dump, logs and a data-directory copy that nothing scanned, and the next
    capture scanned only its own; a bundle is marked incomplete from creation until its scan
    ran, and a capture takes every sibling still so marked as one more root (check 22, with
    such a sibling planted). Item 28's `unknown` outcome covered `bao-restore` alone, while
    `bao-soft-delete`, `bao-destroy` and `bao-delete-key` went through the CLI, whose exit
    status cannot tell an unreachable server from an unanswered request; the three go over HTTP
    like the restore and are logged `unknown` on the same rule (checks 5 to 7 exercise the new
    path; the unanswered case is not exercised, as before). And `netjoin` connected a container
    to whatever network carried the fixture's name, which is free for anyone's once every
    container is off it, and gave a Talos node its fixed address there; the Talos network is
    held to the ID `up` recorded and the Compose network to the project's labels before either
    `netsplit` or `netjoin` touches it (check 20, `netjoin` refused with the record aside).

30. **The volume under a Compose name, the state file with a second name, the dot in the path.**
    A twenty-third round: three findings from one reviewer and three from the advisory review,
    one of them the same as a reviewer's, one rejected (`talosctl` from `PATH` in `evidence`, the
    thirteenth time). The named volumes `compose.yaml` declares get fixed names, and Compose
    reuses a volume of that name it did not create, with a warning only: the fixture would then
    run on someone's data, and `compose down --volumes` would remove it; `up` refuses while a
    volume of either name exists (labelled ones were already refused), and `down`, `--adopt` or
    not, refuses while one exists without the project label (checks 1 and 23). `down` refused a
    symlinked Talos `state.yaml` but not a hard-linked one, through which `talosctl cluster
    destroy` would act on someone else's state while the other name survives; the file must be
    regular with one name (check 23). A KV path given to `bao-soft-delete`, `bao-destroy` or
    `bao-delete-key` could hold `.` or `..` components, which curl resolves into another API path
    than the one the log names; the path is held to slash-separated components without them,
    before anything is logged, and curl is told `--path-as-is` (check 5). And `down` read any
    failure to inspect a recorded node volume as the volume being gone; only "no such volume"
    passes now, any other answer stops the teardown (not exercised).

31. **The Compose volume under the shared label.** A twenty-fourth round: two findings from one
    reviewer, one accepted and one answered, and two from the advisory review, one accepted and
    one rejected (`talosctl` from `PATH` in `evidence`, the fourteenth time). Item 30 held a
    volume under a Compose name to the project label, but that label is only the project name,
    which any Compose run of that name from anywhere puts on the volumes it makes, and a volume
    has no ID: with this run's volume removed by hand, the name could be taken again under the
    same label and `compose down --volumes` would remove someone's data. `up` records each
    volume's creation time once the services are started, `down` removes a labelled volume only
    as the one recorded, and without the record only `down --adopt` removes it by the label
    (check 23, record aside and then naming another time). Answered: that the report's checks
    were recorded as passed before the run; the run reported was of the tree that became that
    revision, up to one failure-path edit, and this round's run covers everything. And the
    selftest's `netjoin` case put the network record back only after its assertion, so a failed
    assertion would have left it aside; the record is restored first (check 20).

32. **The volume made in between, the stranger's node volumes, the hidden helper.** A
    twenty-fifth round: six findings from one reviewer, all accepted, and three from the advisory
    review, one accepted and two rejected (`talosctl` from `PATH` in `evidence` and in
    `selftest`, the fifteenth and sixteenth time). Item 31 held a Compose volume to a creation
    time recorded after `compose up`, but a volume made by anyone between `up`'s look and
    `compose up`, a window holding the three tool downloads, would have been run on and its time
    recorded as the fixture's own; and `docker volume create` is no claim, since on a name that
    exists it returns the name without a word. `up` now makes the two volumes itself, first of
    all, labelled with the run, reads the label back and refuses when it is not its own (the run
    whose create made the volume sees its own value, any other sees another's or none), and
    `down` holds each labelled volume to the run recorded (check 23, record aside and then naming
    another run). The Talos node volumes were listed from every container under the cluster label
    and the two container IDs counted afterwards, so a stranger's container under the label,
    listed and gone in between, would have had its volumes recorded and removed; the IDs are
    taken first and the volumes from those two alone (not exercised). An untracked file that only
    `.git/info/exclude` or a global excludes file hides was in neither the status nor the content
    the bundle carries, while claiming to say what ran; the two listings are compared and such a
    file refuses the capture (check 22, through a global excludes file). An untracked symlink was
    carried as its target's name; refused (check 22). The listing of sibling bundles ran inside a
    process substitution, whose failure reads as an empty list; listed into a file first, and a
    listing that fails voids the capture (check 22, `.state/evidence` writable but unreadable). A
    `kill`, `start`, `pause`, `unpause`, `netsplit` or `netjoin` ended by a signal while the
    Docker request was in flight was logged `failed` though the daemon may have applied it; logged
    `unknown` on the same rule as the OpenBao mutations (not exercised). And two selftest checks
    asserted before unpausing or rejoining the container they had paused or split (checks 17
    and 18); the fixture is restored first.

33. **The recovery hint, the Compose network, the diff helper.** A twenty-sixth round: seven
    findings from one reviewer, all accepted, and three from the advisory review, one a
    duplicate and two rejected (`talosctl` from `PATH` in `evidence` and in `selftest`, the
    seventeenth and eighteenth time). The hint an interrupted `up` prints sent its caller to a
    plain `down`, which refuses a labelled volume, container or network it finds without its
    record, exactly what an `up` interrupted between making a resource and recording it leaves;
    the hint names `down --adopt` from the first Compose volume until the last record is published
    (not exercised). The Compose network was held only to the project's labels, which any network
    can be given, so with the containers and network removed by hand and the claim standing, a
    stranger's network under the fixed name and labels would have been removed by `compose down`
    as the project's; `up` records its ID after `compose up`, and `down` and `inject` hold the
    labelled network to it, as for the Talos one (check 23, a network with the project's labels).
    The refusal of a hidden untracked file printed its path verbatim, bypassing the sanitizer every
    other path goes through; each path goes through it (check 22, a hidden file with a secret in
    its name). The tracked diff lacked `--binary`, so a tracked input changed into a binary file
    would have been carried as "differ" (not exercised); and a tracked file replaced by a symlink
    was carried as the link's target text, though the scripts read what is at the far end; any
    symlink under `fixtures/` outside `.state` and `.cache` refuses the capture (check 22,
    `README.md`). A diff helper from the caller's configuration (`GIT_EXTERNAL_DIFF`,
    `diff.external`) was handed both diffs and one that prints nothing left the bundle without the
    bytes while the loop succeeded, verified against git 2.47.3; both diffs run with `--no-ext-diff`
    (check 22, under a helper that prints nothing). And `containers_own` in `lib.sh` read any
    failure to inspect a container as its absence, so a daemon error would have let a mutation
    proceed against a container under a fixture name unverified; only "no such container" passes,
    any other failure refuses (not exercised).

34. **The race lost, the commit the fixture was made from, the filter, git's own warning.** A
    twenty-seventh round: five findings from one reviewer, four accepted and one answered, and
    two from the advisory review, both rejected (`talosctl` from `PATH` in `evidence`, the
    nineteenth time; and `.state/backups` as a scan root that may not exist, while `up` makes it
    before anything else). Item 33's hint sent an `up` that lost the race for a Compose volume
    name (the label read back is another run's) to `down --adopt`, which with no record removes a
    labelled volume by the label, that is the winner's volume and data; `up` publishes the record
    of the volumes it did make before it stops, its hint names the volume to remove or rename by
    hand and then a plain `down`, and `down` holds a labelled Compose volume, container or network
    to its record whenever a record exists, `--adopt` or not (check 23, `down --adopt` against a
    record naming another run). The bundle named the commit the checkout was at when captured,
    while the fixture, its generated configuration and its cached tools were made from the commit
    it was at when `up` ran; `up` records that commit and how `fixtures/` differed from it then
    (`.state/up-manifest`, `up-fixtures-diff.txt`), `evidence` carries both and `versions.txt`
    says when the two commits are not the same (check 22, the `created at` line). A `textconv`
    filter from the caller's attributes and configuration was still handed both diffs after item
    33's `--no-ext-diff`, and one that prints nothing left the tracked diff empty with status 0
    and the untracked one as headers alone (reproduced against git 2.47.3); `--no-textconv` on
    both, in one `fixtures_manifest_diff` in `lib.sh` that `up` and `evidence` share (not
    exercised). And an untracked directory git cannot open had git and `find` name it on stderr as
    it is, with status 0, before anything went through the sanitizer; both are collected, a
    warning refuses the capture as an incomplete listing, and each line goes through the
    sanitizer (check 22, a closed directory whose name holds a secret). The answered finding asked
    for the added selftest cases to be marked as not rerun; the run recorded for item 33 was made
    on the final script tree, after every script edit and before the documentation, and this
    round's run covers them again.

35. **The creation record's own checks, the restore's source, the archive behind a sibling's copy,
    the claim behind a failed answer.** A twenty-eighth round: four findings from one reviewer,
    all accepted, and three from the advisory review (degraded run), two accepted and one
    rejected (`.state/backups` as a scan root that may not exist, the third time). Item 34 had
    `up` record the commit and the diff without the checks `evidence` makes before it stands by
    the same pair: a hidden untracked file, a symlink or a directory git cannot open would have
    left the creation record claiming to say what the fixture was made from. The checks are one
    `fixtures_tree_check` in `lib.sh`, run by `up` before it records and by `evidence` before it
    ties the bundle, every name through the caller's sanitizer (`evidence`'s withholds a secret;
    at `up` no secret of the fixture's exists yet) (check 22 for `evidence`; the `up` refusals are
    not exercised, the same function under the same cases). The three restores read whatever
    carried the snapshot's name: a symlink would have installed a file outside `.state` as the
    live store, database or provider state, a hard link one that also exists under another name;
    each restore now requires the regular file `inject` published with no second name, before
    anything is read (checks 4 and 9). The control's copy in an incomplete sibling bundle was
    excused by the archive under that name now, whatever the sibling had expanded: `evidence`
    records each archive's SHA-256 next to its expansion and excuses a copy only while the archive
    still has those bytes (check 22, two copies in a sibling, one under the archive's digest and
    one under another). That binding showed up a cost in item 28's sibling scan: a sibling once
    marked incomplete was scanned again at every later capture, so from the first `store-snapshot`
    taken under the same name since, the copy it had expanded was reported at every capture (the
    second `selftest --keep` of this round's first run failed there, six such siblings from the
    first). A sibling is now scanned once: the capture whose scan completes marks it scanned,
    `scanned-by.txt` in the sibling naming the bundle whose `leak-scan.txt` holds the hits (check
    22 asserts the planted sibling's marks). And Docker can create the claim container and still answer the request
    with an error, after which every next `up` from the checkout would have been refused by its
    own claim: on a failed claim, one that names this checkout is reported as such, with a plain
    `down` to remove it (not exercised). The advisory review's two: the closed-directory and
    unreadable-bundle-directory cases of check 22 cannot be built as root, which opens both, and
    are skipped there like the unreadable-file case before them.
36. **A running capture's bundle, the claim by its name and its attempt, what git records nothing
    of, a digest by content.** A twenty-ninth round: seven findings from one reviewer, all
    accepted, and three from the advisory review (degraded run), one accepted and two rejected
    (`talosctl` from PATH in `evidence`, the twentieth time, and `.state/backups` as a scan root
    that may not exist, the fourth). Item 35's sibling scan took every bundle still marked
    incomplete as abandoned, a capture still writing its bundle included: that one's marker was
    removed after a scan of what it had written so far, and what it wrote after, if it was then
    interrupted before its own scan, no later capture would look at. A capture now holds a lock
    on its marker for its whole run, and a sibling is taken only when its marker can be locked;
    one that cannot be is left to its own scan and said so (check 22, a planted sibling whose
    marker another process holds). `claim_drop` removed every container carrying the fixture
    label with this checkout as owner, whatever its name: `down` now refuses while any container
    carries the label without being the claim, and removes the claim, by its name, only while it
    carries the label (check 23, a never-started container carrying both labels under another
    name). Item 35's own-claim hint held for a claim from this checkout, which a second `up`
    running here also leaves, and its recovery would have torn down the winner's fixture: each
    `up` labels its attempt with a nonce, and the hint names the claim as its own only under this
    attempt's nonce; another nonce is reported as a second `up` from the checkout (not
    exercised; the race was run in items 12 and 13, before the hint existed). `sha256sum` given a file name
    holding a backslash escapes the name and marks the line with a leading backslash, so the
    recorded digest was 65 characters and never the archive's, and the archive's own control copy
    was reported as a leak: the digest is read from the archive's content on standard input, and
    the selftest copies the archive under a name holding a backslash (check 22). That case found
    a second thing, not the reviewer: GNU tar 1.35 unquotes backslash escapes in the directory
    given to `-C` (`\b` read as a backspace), so the archive under that name could not be
    expanded into the directory made for it, and the first probe of this round failed there;
    every `tar` now runs with `--no-unquote`, through a `lib.sh` wrapper like `curl`'s. An empty
    directory, a named pipe, a socket or a device under `fixtures/` is in no status, no listing
    and no diff, and code that runs may look at it; and a clean filter that git's attributes
    give a tracked file has git compare the filter's output, so a modified script could have
    read as the committed one: `fixtures_tree_check` refuses both, the first by `find` alongside
    its symlink walk, the second by asking `git check-attr` for the `filter` attribute of every
    tracked file (check 22, an empty directory and a filter given `README.md` by an attributes
    file outside the repository). And the OpenBao metadata loops read the KV and Transit
    listings through a process substitution, whose `jq` status was lost: a listing that does not
    parse now records an unknown entry and leaves the bundle incomplete (not exercised). The
    advisory review's one: `inject`'s usage text promised `netjoin` "the same address" for every
    target, while only the Talos nodes are reattached on their fixed addresses; a service is
    reattached under its name, from which its clients resolve it, and the text now says so.
37. **The identity teardown derives everything from, the daemon it asks, what git's own settings
    hide, a volume the claim made.** A thirtieth round: six findings from one reviewer, all
    accepted, and two from the advisory review (degraded run), one accepted and one rejected
    (`talosctl` from PATH in `evidence`, the twenty-first time). Every name, label and port the
    commands use is derived from `FIXTURE_NAME` in `versions.env`, and every check is made
    against the daemon the shell reaches: with `versions.env` edited, the checkout switched, or
    `DOCKER_HOST` or the Docker context changed since `up`, `down` looked for nothing under the
    new name or on the new daemon, found nothing, removed `.state` and reported success while
    the fixture, and its credentials, ran on. `up` now records the fixture's name and the
    daemon's ID first of all (`up-fixture-name`, `up-daemon`, under the one-name rule); `lib.sh`
    refuses every command while `versions.env` names another fixture than the record, and
    `need_state` and `down` refuse while the daemon reached is not the recorded one (check 23,
    both records rewritten: `down` refuses, `inject` refuses and pauses nothing). Item 30's
    read-back of the Compose volume's run label died on a failed inspect before the partial
    record was published and the hint replaced, so the hint still named `down --adopt`, which
    removes a volume of that name by the label whosever it is: a failed read-back now takes the
    same way out as a label that is another's, the record of the volumes made so far published,
    the hint naming the volume to inspect by hand and not `--adopt` (not exercised; the mismatch
    branch was, in item 30). Item 35's tree check listed untracked files under the working
    tree's ignore rules: a rule added to `fixtures/.gitignore`, or an untracked `.gitignore`
    ignoring its own directory, hid a helper from every listing, with the diff carrying the rule
    and not the helper; the check now refuses while any `.gitignore` under `fixtures/` or at the
    root is modified or untracked (check 22, both). And git's line-ending conversion: under
    `core.autocrlf=true` a tracked script turned to CRLF read as unmodified, or as modified
    with an empty diff, both sides normalised, and the bytes that ran, a CRLF shebang among them,
    were in neither; the status and the diff now run with `core.autocrlf` off, and the `text`,
    `eol` and `ident` attributes are refused with the clean filter (check 22: the healthy
    capture runs under `core.autocrlf=true` with `README.md` turned to CRLF, and its diff carries
    the CRLF bytes; a `text` attribute is refused). The first probe of this round failed on that
    healthy capture: the diff of each untracked file had been left under the setting, and git
    warned there of an LF file it would convert, which the tree check refuses as an incomplete
    listing; that diff runs with the setting off too. The claim container's image declares a data
    volume, so Docker made an anonymous, unlabelled volume for the claim although it never
    starts; removed by hand without `--volumes`, the volume outlived every record, and a later
    `down --adopt` reported no volume left while it persisted. A tmpfs at the declared path
    takes the volume's place and nothing is made (observed: one new volume for a plain create,
    none with the tmpfs; check 2 asserts the claim has no mount). The advisory review's one:
    the sibling marker of item 36 was opened for writing, and a marker its capture removed
    between the existence test and the open, its scan complete, would have been made again, the
    bundle scanned once more at every capture after; it is opened read-only, which `flock` locks
    all the same, and a marker gone by then is a capture that completed and is skipped (not
    exercised; the window is between two statements).
38. **Every container under its name, git's own settings once more, a backup under a second
    name, a root under two spellings.** A thirty-first round: twelve findings from one reviewer,
    all accepted (one P1), and one from the advisory review (degraded run), rejected (`talosctl`
    from PATH in `evidence`, the twenty-second time; it is the `lib.sh` function). The Compose
    containers were held to the project's labels, which any container can be given, so once the
    real one was gone by hand a replacement under the name passed as the fixture's and a kill, a
    data-directory copy or a teardown went to it; and the Talos containers were held to the
    record as a set, so the two nodes' names swapped each still passed, and a pause of the
    worker would have gone to the control plane. `up` now records every container's ID under its
    name (`down-compose-containers`, and `down-node-containers` rewritten as `<id> <name>`),
    `containers_own` holds each name to the ID recorded under it and `down` every labelled
    container to the records (check 9: the two nodes' names swapped, the pause refused and
    neither paused; check 23: a never-started container under the PostgreSQL name with the
    project's labels, `down` and `inject` refused). Item 37's identity records were written
    before the recovery trap was armed and through a redirection, so a `docker info` that failed
    there left an empty `up-daemon` every command refused, `down` among them, and no hint; the
    trap is armed right after the claim and the state directory, and each record is written
    whole under a temporary name and renamed into place with the daemon asked first (not
    exercised). Item 37's volume read-back branch left the create itself out: a `docker volume
    create` that failed after an earlier volume was made exited with the hint still naming
    `down --adopt` and the partial record unpublished; the three ways out are one function now
    (`volume_stop`), the record published and the hint naming the volume to inspect by hand (not
    exercised). Git's own settings, three more: the tree check listed the working tree's ignore
    rules against a modified `.gitignore`, but the committed patterns were unanchored, so
    `.state/` hid a directory of that name at any depth under `fixtures/`, in no status, listing
    or diff, while `find` saw an ordinary directory; the three patterns (`.state/`, `.cache/`
    and the root's `upstream/`) are anchored, in a commit of their own since the check refuses
    to run on a modified ignore file, mine included (check 22: an untracked file under a nested
    `.state` is carried in the diff). A tracked file flagged assume-unchanged or skip-worktree
    (`git update-index`) is in no status and no diff when modified; `git ls-files -v` tags such
    files, and any is refused (check 22, `README.md` flagged). `status.showUntrackedFiles=no` in
    the caller's configuration left a tree whose only change is an untracked file reading as the
    commit's, and the diff was never taken; the status runs with `--untracked-files=all` (check
    22: the healthy capture runs under that setting and the untracked file is in the diff). The
    `working-tree-encoding` attribute re-encodes a file for comparison like `text` does, and the
    attribute check covered tracked files only while the diff against nothing that carries an
    untracked file applies the attributes too, a clean filter among them; the check asks for
    that attribute as well and for untracked files as well (check 22: a clean filter given to
    an untracked helper is refused). `up-fixtures-diff.txt` was written only when the tree
    differed, so its absence read as "the tree was the commit's" and a fixture made from a dirty
    tree, the record removed since, passed for the commit's; the tree check writes it empty when
    the tree is the commit's and `evidence` requires it (check 22: moved aside, refused). A dump
    or archive under `.state/backups` that was a symlink, or had a second name, was expanded
    into the bundle whatever the other name held; `down` refused such an entry at teardown, and
    `evidence` now refuses it before expanding anything, where before it only declined to
    excuse the control's copy from it (check 22: the hard-linked archive and a symlinked dump,
    both refused). And an extra scan path that was a symlink to the live store, or lay under a
    root the scan had anyway, was walked twice under two spellings: every file counted twice,
    and the control's copy under the other spelling reported as a leak, since it is the control
    at one name only; the roots are reduced to one per place they name, the fixture's own names
    before the extra paths, and a root dropped keeps its own name for the name pass (check 22:
    a symlink to the live store as an extra path, nothing reported under it).
39. **The daemon before the claim, case folding, a named volume in the record, a completed
    restore's leftover.** A thirty-second round: two findings from one reviewer and three from
    the advisory review (degraded run), four accepted and one rejected (`talosctl` from PATH in
    `evidence`, the twenty-third time; it is the `lib.sh` function). Item 38 wrote the daemon
    record whole and behind the trap, but asked the daemon after the claim was made on it: a
    `docker info` that failed then left the claim and `.state` with no daemon record, and
    `daemon_own` took no record for no mismatch, so an operator who followed the hint after
    switching `DOCKER_HOST` had `down` verify the new daemon clean, remove `.state` and leave the
    claim on the old one. The daemon is asked before the claim; the two identity records are the
    first thing written under `.state`, and one that cannot be written takes the directory and
    the claim with it, nothing else existing yet; and `daemon_own` refuses a state directory
    with no daemon record, since `up` leaves none such (check 23: the record moved aside, `down`
    and `inject` refused; the failing `docker info` and the failing write not exercised).
    `core.ignoreCase=true` in the caller's configuration had git take an untracked file whose
    name differs from a tracked one's only by case for the tracked one: in no status and no
    listing, while `find` saw an ordinary file (reproduced: `README.MD` beside `README.md`, every
    inventory empty). Every git inventory and diff of the tree check and the manifest diff go
    through one function that turns `core.autocrlf` and `core.ignoreCase` off (`fixtures_git`);
    the first probe of this round failed at `up` on two calls the rewrite had joined
    (`ls-files-v`), fixed and the whole run repeated (check 22: the healthy capture runs under
    the setting with `README.MD` planted, where the filesystem tells the two apart, and its
    content is in the diff). `down` merged the volume names the Talos-labelled containers mount
    into `down-node-volumes` before holding each to what a node's volume is, so under `--adopt`
    with no container record a labelled stranger mounting a named volume would have put that
    name into the record, and every `down` after refused the record as not the fixture's; the
    names from the daemon are held to the anonymous form before the merge, and a stranger's
    named volume is refused as its own (not exercised: with a record the stranger is refused
    earlier). And `store-restore` cleared its staging name before removing the directory, so a
    kill during that removal left the replaced store under `previous` next to a live directory
    that is the restored one, and the next `store-restore` named it as the only copy to move
    back, undoing a restore that had completed; the name stays set until the directory is gone,
    the exit handler finishing the removal and moving nothing back while the live directory is
    there, and the leftover check tells the two apart by whether the live directory exists and
    the staging one still holds `data` (check 9: both leftovers planted, the completed one
    refused as one to remove with the live store untouched, the interrupted one as the store to
    move back).
40. **Recovery armed before the directory, git's stat cache, a symlinked parent of the roots.** A
    thirty-third round: three findings from one reviewer and one from the advisory review
    (degraded run, four of seven chunks lost), three accepted and one rejected (`talosctl` from
    PATH in `evidence`, the twenty-fourth time). Item 39 armed the exit handler after `.state`
    and the two identity records existed, so a signal between the `mkdir` and the trap left the
    claim and a `.state` with no daemon record, which every `down` then refused: the handler is
    armed before the directory is made and, until the records are published, removes exactly
    what this stage put there (the four record names, then the empty directory, then the claim),
    naming a directory that holds more as left (check 23 stands; the signal in the window is not
    exercised). `core.trustctime=false` or `core.checkStat=minimal` in the caller's configuration
    had git trust its stat cache: a tracked script changed to bytes of the same length with its
    mtime put back was in no status and no diff, and the bundle named a clean commit for bytes
    that never ran (the reviewer reproduced it). `fixtures_git` now also sets
    `core.trustctime=true`, `core.checkStat=default`, and turns `core.fsmonitor` and
    `core.untrackedCache` off, two more caches that would answer for the filesystem (check 22:
    the healthy capture under both settings with `README.md` so changed, its bytes in the diff).
    And the leak scan, given an extra path whose resolved place holds a root the scan has anyway
    (a symlink to the checkout, or to `.state`), dropped the fixture's own roots and walked the
    alias by its given spelling, so the control was met under a name `is_live_control` does not
    know, reported as a leak, and the capture void; such a root is walked by the name it resolves
    to, its given spelling kept for the name pass (check 22: a symlink to `.state` in a directory
    of its own; the control listed once at its own name, not as a leak).
41. **The flag before the directory, a volume's hint before its create, gitlinks, the volume
    filter.** A thirty-fourth round: four findings from one reviewer and three from the advisory
    review (degraded run, three of seven chunks lost), five accepted, one whose premise a probe
    contradicted, hardened all the same, and one rejected (`talosctl` from PATH in `evidence`,
    the twenty-fifth time). Item 40 armed the handler before
    the `mkdir`, but set the flag it reads after it, so a signal between the two dropped the claim
    and left the empty directory, which `down` refuses for its missing record and `up` for
    existing: the flag is set before the `mkdir` and cleared when it fails, the handler removes
    the records only when the daemon record is this run's and leaves a directory that is not, or
    holds more, naming it (the window between a failed `mkdir` and the clearing remains, and is
    an empty directory removed or a directory of another daemon's `up` from this checkout left
    with its records; a signal there is not exercised). The Compose-volume stage set the `--adopt`
    hint before its loop, so a signal between a `docker volume create`, which reuses a volume of
    the name whoever made it, and the label read back left that hint standing, and following it
    removed the volume by the label, whosever it was: the hint names that volume, inspect by hand
    and not `--adopt`, from before its create until its label is this run's, the handler
    publishes the record of the volumes made so far, and the `--adopt` hint is set once the
    record is published (not exercised). A gitlink under `fixtures/`, a submodule or an embedded
    repository staged as one, had the diff carry `Subproject commit <hash>`, `-dirty` at most, and
    never the bytes under it, with the untracked listing stopping at its directory (the reviewer
    reproduced it): the tree check refuses a mode-160000 index entry and any `.git` under
    `fixtures/` (check 22: a gitlink staged with `update-index --cacheinfo`, and a `.git` directory
    with a `HEAD` under an untracked directory, each refused). And `down` verified each recorded
    node volume gone through `docker volume ls --filter name=^<id>$`, which Docker documents as
    matching part of a name: probed on the daemon used here, the anchored expression matched the
    whole name and nothing else, so the verdict was right, but rested on what the documentation
    does not promise; the listing is now held to the whole name in the shell. From the advisory
    review: `db-restore` renamed the live database inside its one transaction whether it existed
    or not, so with the live database dropped, the loss a snapshot is for, the rename failed, the
    transaction rolled back, and the restored copy was stranded under its scratch name while the
    message said the live database was left alone; the first rename now runs only while a
    database of the name exists, in the same transaction, and the drop of the superseded one
    tolerates its absence (check 4: the live database dropped, the restore installs the snapshot
    under the name). And `inject`'s exit handler, when the earlier store could not be moved back
    from a `store-restore` cut between its two renames, left the staging directory without a
    word: it now names the directory as the only copy of the store, and the live name as the
    place it belongs (not exercised).
42. **Replacement refs, the executable bit, the rest of `versions.env`, a Docker refusal read
    back.** A thirty-fifth round: four findings from one reviewer and four from the advisory
    review (degraded run), six accepted and two rejected (`talosctl` from PATH in `evidence`, the
    twenty-sixth time, and in `selftest`; both are the `lib.sh` function). A replacement ref
    (`git replace`) has `status` and `diff` compare against the replacement commit's tree while
    `rev-parse HEAD` names the original, so the manifest would name a commit whose bytes did not
    run (the reviewer reproduced it): `fixtures_git` passes `--no-replace-objects`, so every
    object is read as it is (check 22: the commit replaced by its parent, the capture succeeds,
    the diff is byte for byte the one taken without the ref and `versions.txt` names the real
    commit; the first probe asserted the diff empty, which a tree that differs from the commit,
    as it does while a fix is being made, contradicts). `core.fileMode=false`, which
    git sets itself on a filesystem that does not keep the bit, has git ignore a command made
    non-executable, so the change was in no status and no diff while running the command fails
    (reproduced too): `core.fileMode=true` is forced with the other settings (check 22:
    `bin/inject` without its bit under `core.fileMode=false`, the diff carries `new mode
    100644`). Item 36 held `versions.env` to the fixture name it was created with and nothing
    else, while the ports, node addresses, image pins and requested versions every command uses
    come from the same file: an edit since `up` would have `inject` send a token to another
    port, `netjoin` put a node back at another address, or `evidence` report a version never
    requested. `up` now records the whole file (`up-versions.env`, written whole and renamed into
    place with the two identity records, under the one-name rule), and `lib.sh` refuses every
    command while the file differs from the record byte for byte, naming the record to restore
    from (check 23: a line appended to the record, `down` and `inject` refuse). And `inject`
    classified a Docker mutation as `unknown` only when a signal ended the CLI, so a connection
    that closed before the answer, an ordinary exit 1, read as a refusal while the daemon may
    have applied the request: each Docker action now names the state it asks for (`Running`,
    `Paused`, membership of the recorded network), and when the CLI fails the handler reads that
    state back, up to five times a second apart since a kill is answered before the container
    has stopped; the state asked for is `done` (a `start` stays `unknown`, since the service was
    not seen to answer), another is `failed`, and a daemon that cannot say is `unknown` (checks
    11 and 15: a pause of a stopped container is `failed`, a second kill of it and a second pause
    of a paused container are `done`, since the refusal left the state asked for; a connection
    closed mid-request is not exercised). From the advisory review: `down`, when the marker
    that names the node volumes as not all known could not be renamed into place, left the
    `mktemp` file beside it; the failure branch removes it (not exercised). And `selftest` held
    a sibling bundle's marker locked through `flock` given a command, which forks the `sleep`
    and holds the lock in the parent: killing `flock` left the `sleep` behind for its five
    minutes; the lock is now taken on a descriptor by a subshell that `exec`s the `sleep`, so
    the process killed is the holder (check 22, and the probe counts no `sleep` left).
43. **The address behind a membership, an authenticated readiness, the bytes that were sourced,
    the commit that was named, every volume without a record, a deletion half done.** A
    thirty-sixth round: six findings from one reviewer and two from the advisory review (degraded
    run), six accepted and two rejected. Item 42 read a `netjoin` back as membership of the
    network, but a container attached meanwhile at another address, or without its alias, is a
    member the connect refuses, so the action read `done` while the node was not at the address
    its record promises: each `netjoin` names the address, or the alias, it asks for, and the
    read-back holds the endpoint to it (check 20: the worker attached by hand at another address,
    the `netjoin` is `failed`; detached and joined, a second `netjoin` is `done`). `inject start
    postgres` waited on `pg_isready`, which answers once the server accepts connections whatever
    the parameters, so a start was `done` on a database no client could use: the wait is an
    authenticated `select 1` on the fixture's database (check 12: the database renamed aside,
    the start fails at its wait). Item 42 recorded `versions.env` by copying the file after
    `lib.sh` had sourced it, two readings a moment apart: `lib.sh` reads the bytes once, sources
    them, and holds those bytes to the record; `up` writes the record from the same bytes (not
    exercised: the edit would have to land between two statements). The tree check took the
    manifest commit from `HEAD` and then diffed against `HEAD` again, so a checkout in between
    would record a diff against another commit than the one named: the diff is taken against the
    commit recorded, and `HEAD` is read once more at the end and a move refused (not exercised).
    `up`'s stop on a Compose volume it could not vouch for hinted at inspecting that volume and
    then a plain `down`, but when the record of the volumes made so far could not be published
    either, the plain `down` refuses those too: the hint then names every volume in the
    unpublished record for inspection by hand, from `volume_stop` and from the exit handler
    alike (not exercised). And `bao-delete-key` sends two requests, the setting that allows
    deletion and the deletion: a DELETE refused after the POST was applied left the key
    deletable while `failed` said nothing had changed; the setting is put back and the action
    fails with the key as it was, and when that cannot be done either, or the connection is
    refused, the outcome is `unknown` (not exercised: the root token is refused nothing).
    Rejected: `node_tag` said to filter for a `Tag:` line that `version --short` does not print:
    run against a live node, `--short` shortens the client block only and the server block keeps
    `Tag:         v1.13.6`; check 22 now asserts both nodes' tags in `versions.txt`, so the
    premise is held by a test. And `talosctl` from PATH in `evidence`, the twenty-seventh time;
    it is the `lib.sh` function.
44. **The setting as it was found, a symlink checked out as a file.** A thirty-seventh round: two
    findings from one reviewer, both accepted; the advisory review ran degraded (three of eight
    chunks lost) and reported nothing. Item 43 put `deletion_allowed` back to false after a
    refused deletion, whatever it had been, so a key that was deletable before was changed while
    the message said it was put back: the setting is read first, a key already deletable is left
    so and the message says so, and one made deletable here is put back to what was read (check
    7: the key made deletable beforehand, the read path; the refusal itself is still not
    exercised). And `core.symlinks=false` in the caller's configuration has git check a committed
    symlink out as a regular file holding the target's name and read that file back as the
    symlink, so the status was clean and the diff empty while a regular file ran where the commit
    has a link, and the tree check, which refuses a symlink it finds, found none: `core.symlinks`
    is forced on for every inventory and diff, so the file is a type change and the diff carries
    it as the regular file it is (check 22: a symlink staged under `fixtures/` with a regular
    file at its path, under `core.symlinks=false`, the diff carries `new file mode 100644` and
    not `120000`).

45. **Compose from the records, a mark written through a link, the daemon record last, the
    server's own timeout, a deletion that got no answer.** A thirty-eighth round: four findings
    from one reviewer and two from the advisory review (degraded, three of eight chunks lost),
    five accepted and one rejected (a symlinked `.state/backups` is refused at load by every
    command, before `down` looks at the entries in it; check 23 now shows it). Item 43 held
    `versions.env` to the bytes read once, but Compose still read the checkout's `versions.env`
    and `compose.yaml` when it created the services, well after the records were written (the
    CLIs are fetched in between), so an edit in that window created them from bytes no record
    holds, and the checkout put back passed every guard: `up` records `compose.yaml` as
    `up-compose.yaml` with the other three, and Compose runs from the two records at `up` and at
    `down` (check 23: the record is the file's bytes, and one with a second name is refused).
    `evidence` wrote an earlier bundle's `scanned-by.txt` by redirection, which follows a symlink
    planted there into whatever it names: the entry is refused before anything is scanned unless
    it is a regular file with one name, and the mark is written aside and renamed into place
    (check 22: a linked mark, the file behind it unchanged). `down` removed `.state` with one
    `rm -rf`, which takes the daemon record in whatever order the tree is walked, so a removal
    cut short left a directory `up` refuses as existing and `down` as not one `up` left: the
    daemon record, and the compose record with it, go last (check 23: a `.state` of the daemon
    record and a leftover is removed by a plain `down`). `timeout` around `docker exec` ends the
    client and leaves the statement running inside the container, so a rename waiting on a lock
    would still commit after the log said the action failed: the server is given the same limit,
    a second less, as its statement and lock timeouts (check 12: a `pg_sleep` past the limit is
    cancelled by the server, not given up on by the client). And `bao-delete-key` on a key
    already deletable recorded a request that never reached the server as `unknown` and one that
    got no answer as `failed`, the reverse of every other request: curl's 7 and 22 are `failed`
    with the key left as it was, anything else `unknown`, the setting notwithstanding (not
    exercised, as before).

46. **The records against the inventory, the manifest's values alone, the tools against the
    manifest, the outcome handler before the begin line, an empty state directory.** A
    thirty-ninth round: five findings from one reviewer, all accepted, and one from the advisory
    review (degraded, four of eight chunks lost), rejected for the sixth time (`.state/backups` as
    a scan root that may not exist, while `up` makes it). Item 45 recorded `compose.yaml` before
    the tree check inventoried `fixtures/`, so an edit between the two would have the inventory
    carry bytes the services were not created from: both file records are compared to the files
    again once the inventory is done, and a difference refuses the run (not exercised). Sourcing
    `versions.env` kept a value the caller's shell exported when the file omitted it, used by
    every command and recorded nowhere: the values the manifest must set are cleared before the
    file is read and each is required after (check 23: the file short of `OPENBAO_PORT` while the
    shell exports it; `down` and `inject` refuse). The cached tools were checked once, at
    download, and run unchecked from a writable cache ever after, while every bundle cites the
    manifest and a replacement can report the pinned version: `talosctl` is checked against the
    manifest's digest before its first run in any command, `evidence` checks all four before
    running any, `up` once they are installed, and the two binaries extracted from the `age`
    archive are pinned by digest too (check 22: a stand-in `talosctl` reporting the pinned
    version is refused, and never run). `inject` armed its outcome handler after the `begin`
    line, so a signal between the two left a begin with no outcome: the handler is armed first
    and writes an outcome only once the begin line is written (not exercised). And item 45 kept
    the daemon record to the last, but a removal cut short between it and the directory left an
    empty `.state` that `down` refused as not one `up` left: an empty state directory is taken
    for none (check 23: `down` on an empty `.state`).
47. **The Compose records kept together, every bounded `talosctl` call through the check, the
    signals held across the begin line, a dangling marker refused.** A fortieth round: three
    findings from one reviewer and one from the advisory review (degraded, four of eight chunks
    lost), all accepted. Item 46 kept the compose record to the last with the daemon record, but
    removed the `versions.env` record with the rest first, so a removal cut short between the
    two left a compose record without its environment, and the next `down`, which runs Compose
    from the records whenever the compose record is there, failed before it could start: the
    three records go together at the end, the compose file first, so that a cut among them leaves
    either both Compose records or neither (check 23: `down` on a `.state` of the three records).
    Item 46 checked `talosctl` before its first run through the wrapper, but the three bounded
    calls (`timeout` runs a program, not a function) named the cached file directly, and
    `inject start` of a node had never checked it: `talosctl_timed` in `lib.sh` checks before
    the bounded call, and no call site names the file. Item 46 armed `inject`'s outcome handler
    before the begin line, but the line and the flag that arms the outcome stayed two commands,
    and a signal between them still left a begin with no outcome: `TERM`, `HUP` and `INT` are
    held from before the line until the flag is set, then delivered as they came (not
    exercised in the fixture; the pattern was run on its own with a `TERM` during the line,
    and the handler saw the flag set). That run showed a gap older than the finding: a signal
    that ends the shell between two commands leaves the status of the command before it, 0 as
    a rule, and the handler took that 0 for an action completed, recording `done` for one the
    signal ended before it ran. A flag set by the last line tells completion from a status of
    0, and without it the outcome is `failed`; the commit checks still turn it into `done`
    where the artifact shows the commit went through. And `evidence` tested an earlier bundle's marker with `-e`, which a symlink to
    nothing fails, so such a marker read as none, the bundle passed for a completed capture and
    went unscanned, while a marker linked to a file was refused: a marker that is a symlink is
    refused either way (check 22: a dangling marker is refused and left).
48. **The manifest parsed, the scripts held to their bytes at load, a container acted on by
    its verified ID, an `up` cut short told from one completed.** A forty-first round: four
    findings from one reviewer and two from the advisory review (degraded, three of eight
    chunks lost, two unverdicted); five accepted, one rejected (`node_tag` parsing
    `version --short` for `Tag:`, which the earlier live check and check 22's assertion on
    both node tags settle; second time). `versions.env` was sourced, so a value written as an
    expansion (`$RANDOM`, `${X:-58200}`) would be evaluated, and the same bytes could give
    another value at another command while the byte-for-byte record held: the file is parsed
    as blank, comment or one plain `KEY=value` line, and any other line is refused (check 23:
    the `OPENBAO_PORT` line as an expansion of the same port; `down` and `inject` refuse). The
    tree check inventoried `fixtures/` as it was at that moment, so `lib.sh` or the command
    edited before launch and put back before the inventory would have run as edited while the
    inventory said the commit's: both are digested as on disk at load, and compared again at
    the inventory and at the end of `up` (not exercised; what remains is the moment between
    the shell opening a script and that line). `inject` acted on a container by name, so one
    removed and replaced under the name between `need_state`'s check and the action would have
    been the one killed, paused or moved: every mutation and read-back takes the ID the check
    verified under that name (not exercised as a race; every `inject` in the selftest now goes
    by ID). And `up`'s exit handler ran teardown only on a non-zero status, which a signal
    between two commands does not give (item 47): a flag set once the fixture is up tells the
    two apart, and every other exit runs the teardown (not exercised). The report's row 23
    named an expected outcome for two of its three cut-short directories; the third is named.
49. **The inventory taken twice, a Docker mutation flagged until the action completes, teardown
    by the IDs it held, the manifest's keys alone and each once, a moved-aside store kept when
    something is in its way.** A forty-second round: five findings from one reviewer and two
    from the advisory review (degraded, five of nine chunks lost), all accepted. Item 44 re-read
    `HEAD` at the end of the tree check, but nothing else: a file changed, added, linked or
    flagged between a listing and the diff passed one and was described by the other. Every
    listing and the diff are taken a second time after the first diff and held to it: the
    same commit, the same status, the same diff byte for byte, or the run is refused (not
    exercised; what is not seen is a change made and undone between the two takes of one
    listing). Item 47's completion flag turned a signal at a command boundary into `failed`,
    but `inject` cleared its Docker flag on a line of its own after each request, so a signal
    between the request returning and that line recorded `failed` for a mutation the daemon
    had made, with nothing read back: the flag stays up until the action completes, so the
    read-back decides (`done` where the state is the one asked for); a `start` is flagged as
    started once the daemon answered, and a signal during the wait for the service records
    `unknown`, a wait that ran to its limit `failed` as before (check 12). Not exercised as a
    race; checks 11, 15, 19 and 20 pass through the kept flag. `down` held every labelled Talos
    container and network to `up`'s record, then removed by a fresh listing on the label, which
    would take in a container or network given the label since the check: the removals take
    exactly the IDs the check held (or, without a record, listed under `--adopt`); what remains
    is `talosctl cluster destroy`, run before them on the cluster's own state file, which
    selects by its own label (not exercised as a race; check 23 passes through the held IDs).
    Item 48's parser assigned any line whose key was uppercase, so `STATE=`, `CACHE=` or
    `PATH=` in the file would have been taken, and a key set twice would have run under its last
    value: only the manifest's keys are assigned, each once (check 23: a `STATE` line, then a
    second `OPENBAO_PORT` line; `down` and `inject` refuse, naming the key). And `store-restore`'s
    exit handler declined to move the earlier store back over a `data` directory made since it
    was moved aside, then removed the staging directory holding that store: with such a
    directory in the way, and the restore's own not installed, the staging directory is left
    and named as the only copy, as when the move back fails; it goes only once the restore's
    own directory is in place (not exercised). From the advisory review: section 4's list of
    the checks review widened omitted 6, 7 and 12, and section 5's opening line claimed every
    item reproducible and fixture-changing, which the items rejected or not exercised
    contradict; both corrected.
50. **The held IDs kept to the removal, an unanswered key deletion unknown whatever the setting.**
    A forty-third round: the reviewer found nothing on the previous revision, and the advisory
    review (degraded, three of nine chunks lost, four unverdicted) reported five, three accepted
    and two rejected (`.state/evidence` as a symlink is refused at load by `lib.sh`'s check of the
    managed directories, check 22; `talosctl` in the selftest is the `lib.sh` function running the
    checked binary, twenty-eighth time). Item 49 kept the Talos container IDs `down`'s check held
    for the removal, but a later line of `down` listed the containers by label again into the same
    variable, for the count that decides whether the node volumes are known, so the removal took
    the fresh listing after all: the count takes the held list, and nothing lists by the label
    again (check 23 passes through the held IDs; not exercised as a race). `bao-delete-key` on a
    key made deletable here treated a `DELETE` that went out and got no answer like a refusal,
    putting the setting back and recording `failed`, while the key may be gone, and a `DELETE`
    that never reached the server (curl 7) skipped the put-back and recorded `unknown`, while the
    key is there and deletable: a refusal (curl 7 or 22) puts the setting back and fails, or is
    `unknown` when the put-back fails too, and any other status puts the setting back where it was
    applied and is `unknown` whatever came of that (check 7 exercises the read and the deletion
    itself; the refusal and the unanswered request are not exercised).
51. **Every action by the ID the check held, the Compose side torn down the same way, a signal
    after a snapshot's rename, the volumes of the held containers, the node volumes unknown
    whether or not anything is left.** A forty-fourth round: ten findings from one reviewer, eight
    accepted, one answered and one declined, and none from the advisory review (degraded, four of
    nine chunks lost). Items 48 and 49 had `inject` act by the verified ID, but `evidence` still read the
    logs, the images, the running state, the dump and the data directory by name, `bao` and `pg`
    in `lib.sh` still ran `docker exec` by name, the claim was dropped by name after its label was
    checked, the fixture network was connected and disconnected by name after its ID was checked
    and read back under that name, and the selftest's bounded worker call named the cached
    `talosctl` directly: each takes the ID its check held (`container_id` in `lib.sh`, filled by
    `containers_own` and by `up` from its own records; `network_id` in `inject`, the read-back
    matching endpoints on the held network ID; `claim_drop` on the ID inspected with the labels;
    `talosctl_timed`), and `pg_client` joins the network by the recorded ID. `down` still ran
    `compose down --volumes --remove-orphans`, which looks the project up by its labels at the
    time of the removal: the Compose containers and network are removed by the IDs the check held,
    then the declared volumes by name (a volume has no ID; the one-name rule stands). The Talos
    node volumes were listed by a fresh listing on the label, not from the held containers: listed
    from those, and `talos_volume_names` refuses a call without IDs. `snapshot` cleared its partial
    name on the line after the rename, so a signal between the two recorded `failed` for a
    snapshot that is published: the handler clears it where the final name holds the recorded
    inode. And a `down` that found nothing labelled and no record said no volume was left, while
    the node volumes of containers removed by hand are exactly what it could not look for: whether
    anything labelled exists no longer enters into that. Declined: that the leak scan reads a tree
    a writer may still be changing; the scan is of the moment, experiments quiesce their writers
    before capturing, and the limit is recorded in the README. A finding that section 4 attributed
    an earlier revision's run to this one was answered by stating there that each revision was run
    again before it was committed. Check 23 passes through every teardown path changed; no case
    is exercised as a race.
52. **The restore through the verified ID, no moment with neither flag, an answered mutation
    kept through the end, the claim dropped only as seen held, listings held to arrays, the
    Docker context pinned, and no error line from a state directory already gone.** A forty-fifth
    round: six findings from one reviewer and one from the advisory review (degraded, six of nine
    chunks lost, one unverdicted), all accepted. Item 51 left one
    `docker exec` by name, `db-restore`'s `pg_restore`: it takes the verified ID. `start` cleared
    its Docker flag before it set the started flag, so a signal between the two lines found
    neither and recorded `failed` for a container the daemon started: the started flag goes
    first. `bao-soft-delete`, `bao-destroy`, `bao-delete-key` and `bao-restore` cleared the
    dispatch flag once OpenBao had answered, so a signal between that line and the last recorded
    `failed` for a mutation OpenBao made: a flag set on the answer, before the other is cleared,
    has the handler record `done`, saying that what followed the answer (`bao-restore`'s wait for
    an active node) was not seen to finish. `claim_drop` removed the claim by the ID it inspected
    once its owner label was non-empty, so a claim retaken by another checkout between `down`'s
    look at the start and the drop at the end would have been removed from under that fixture:
    it takes the owner the caller saw and refuses, with status 1, one held by anyone else (check
    2 plants a claim of another checkout and asserts the refusal; the retake itself is not
    exercised as a race). `evidence` iterated an OpenBao listing with `.[]`, which walks an
    object's values as well as an array's, so an empty object read as an empty listing instead
    of an unparsable one: both listings are held to the array type (the guard was run on an
    object and on an array by hand; a malformed listing is not exercised through the fixture).
    And the Docker CLI reads its default context afresh for every call, so a `docker context use`
    in another shell while `up` ran would have it record the daemon and take the claim on one
    daemon and create the services on another, where no `down` could both pass the daemon check
    and find them: `lib.sh` pins the context at load in `DOCKER_CONTEXT` (unless the caller set
    it) for the life of the command (`DOCKER_HOST` beside a pinned `default` was run by hand and
    reaches the daemon; a context re-pointed with `docker context update` is not held, and is
    caught by the daemon record before `inject`, `evidence` and `down` act, not by `up` before its
    records). Last, `down`'s removal of the state directory ran `find` on it whether it existed
    or not, so a `down --adopt` after `.state/` was moved away, a documented path, printed
    `find`'s "No such file or directory" on a teardown it then reported clean: the removal runs
    only while the directory exists (probed by hand: a `down` on a clean daemon with no `.state/`
    prints nothing but its last line; not a selftest case).
53. **OpenBao requests from inside the verified container's namespace, `up`'s own records held
    to labels and asked by ID, the swap told by the restored database's identity, an answered
    mutation kept through any end, and the partial marker refused at a name's end.** A
    forty-sixth round: four findings from one reviewer and three from the advisory review
    (degraded, four of nine chunks lost, two unverdicted), six accepted and one rejected (the
    state directory's `backups` as a symlink: `lib.sh` refuses it at load for every command, item
    17; the seventh time). `inject` sent every OpenBao request to the published port on the host,
    which is an address: whatever held it by then answered, a replacement bound to it once the
    verified container was gone included, and was handed the root token. The requests now go
    from inside the network namespace of the container verified under the name, to its own
    listener, by a pinned client image (`CURL_IMAGE`; the OpenBao image carries only BusyBox
    `wget`, which cannot send a `DELETE`), pulled by `up` and run by `inject` with `--pull never`.
    The client's status is curl's own when it ran (7, 22, 28 as before) and Docker's 125 to 127
    when it was not run, which is a request that never went out: a container stopped or gone has
    no namespace to join. Check 13 sends `bao-soft-delete` to the killed OpenBao and asserts
    status 125 and `failed`. The token goes in through the environment and curl expands it into
    the header. The client view `evidence` records stays on the published port, since it is the
    client's view. `up` recorded each Compose container by inspecting its name after `compose up`,
    so a replacement made under the fixed name in that window would have been recorded and
    installed as the verified ID: the loop reads the ID with the two Compose labels and refuses one
    not naming this project and checkout, as `containers_own` does. Its final readiness loop asked
    `docker inspect` by name for all four containers: it asks the ID recorded under each name
    (the Talos loop now fills the same table). The `db-restore` handler took the scratch name's
    absence after an interrupted swap as proof the swap committed, which a concurrent drop of the
    scratch database would fake: the OID of the restored copy is read before the swap, and the
    handler asks whether the database under the live name carries it, since a rename keeps the
    OID (not exercised; the query was run by hand). The answered-mutation branch of item 52
    required a signal, so a `bao-restore` whose wait for an active node gave up was recorded
    `failed` for a mutation OpenBao had confirmed: the branch takes any failure after the answer
    (not exercised: the wait has not been seen to give up). And the snapshot-name guard refused
    `.partial.` inside a name, while a name ending in `.partial` gets the suffix that completes
    the marker: both are refused (checked by hand; not a selftest case).
54. **No teardown by the label at all, the checkout put back when `selftest` is ended, the
    password probe from inside the container, and a request to an unverified name refused
    unlogged.** A forty-seventh round: four findings from one reviewer and one from the advisory
    review (degraded, four of nine chunks lost, one unverdicted), four accepted and one rejected.
    Item 49 left `talosctl cluster destroy`, which selects by the cluster label, running before
    the removals by held ID, so a container or network given the label between `down`'s check
    and that call would have been removed unvouched for: it is not run any more. The Talos
    containers and network go by the IDs held (or, under `--adopt` with no record, listed once),
    and the cluster's state directory, the one other thing `destroy` removed, goes with `.state`.
    `down`'s checks on the cluster's `state.yaml` (a symlink, a second name), which guarded only
    what `destroy` read, went with it; `.state/talos` itself is still refused as a symlink at
    load, since the removal of `.state` would take the link and leave the credentials (check 23
    keeps that case and drops the hard-linked `state.yaml` one). `selftest` changed the checkout
    for its cases (a replacement ref, an index flag, staged entries, tracked files rewritten, a
    command's executable bit) and put each change back on the line after, with no handler: a
    signal between the two left the repository altered with nothing to say so, a replacement ref
    in particular making every later git command read the parent's tree for `HEAD`. It arms an
    EXIT handler, with TERM, HUP and INT routed to it, before its first check; each change
    registers its put-back before it is made and unregisters it after its own, and the handler
    runs what is still registered, last first (run by hand: `selftest --keep` sent TERM while the
    replacement ref was installed exits 143 and leaves no ref, a clean `fixtures/` status and an
    executable `bin/inject`). `up`'s last probe, the password over the network, ran `psql` on the
    Compose network by the service's name, so a container attached under the `postgres` alias
    once the recorded one was gone would have answered it and been handed the password: the
    client runs inside the recorded container's namespace, against that container's own address
    on the Compose network (loopback would not do, since the image trusts it as it trusts the
    socket, and the password would go unasked); check 12 asserts that another password is
    refused there and this run's answered. And `inject`'s OpenBao client returned 1 when the
    name had been verified under no container, a status the handler read as a request sent and
    not answered, so the mutation was recorded `unknown` for a request that never went out: the
    ID is resolved before the action begins, and a name with no container is refused before
    anything is logged. Rejected: that the context pin overrides a caller's `DOCKER_HOST`. Run by
    hand: with `DOCKER_HOST` set to an address nothing listens on and `DOCKER_CONTEXT` unset,
    `docker context show` prints `default`, and with that pinned beside the same `DOCKER_HOST`
    the CLI fails to connect there, as with `DOCKER_HOST` alone; `DOCKER_CONTEXT` overrides
    `DOCKER_HOST` only when it names another context, and the CLI reports `default` whenever
    `DOCKER_HOST` is set, so the pin never names another in that case.

55. **The networks from the verified containers' attachments, a readiness that asks each node
    and refuses a paused container, the generated secrets never sourced, and `selftest`'s own
    volumes and renames put back.** A forty-eighth round: six findings from one reviewer (two
    the same) and one from the advisory review (degraded, five of nine chunks lost, one
    unverdicted), all accepted. `up` recorded each network from a listing by its label, counted
    to one: the label is one any network can be given, so a network made under it once the real
    one was gone by hand would have been listed, counted, recorded and removed as the fixture's.
    Each is now the one network both verified containers are attached to, read from those
    containers, and must carry the label; a listing by the label is not consulted. `up`'s final
    readiness asked only `Running`, which a paused container still is, and asked nothing of the
    Talos nodes after the control plane's config was read minutes before: it refuses a paused
    container by name and sends a bounded request to each node. `up` sourced the `secrets.env`
    it had just published, and `selftest` sourced the one a child `up` wrote, while every other
    command parses the file as four literal assignments: `up` exports the values it generated
    and never reads the file back, and `selftest` reads it through the same parser (`secrets_load`
    in `lib.sh`). `selftest` made a volume under a Compose name for two of its cases with
    `docker volume create`, which returns an existing volume of that name as if it had made it,
    then removed it: the volume is made under a label only that run gives and read back under
    it, one that existed is left and named, and the removal is registered with the handler item
    54 added. That handler had nothing registered for the Docker changes the cases make: the
    PostgreSQL container or the worker renamed aside with an impostor created under its name
    (checks 9 and 23), the two nodes' names swapped (check 9), the claim replaced by another
    checkout's (check 2), and the containers, networks and volume made for check 23's refusals.
    The reverse of each is registered before it is made (a rename back, a removal by the ID the
    create returned, `claim_take`), each of the swap's three renames its own, so that a signal
    after any of them puts the fixture back from that point, last first. Run by hand, the first
    attempt hit this: a TERM during check 9's rename, the site the finding had not named, left
    the fixture under the aside name with the impostor under its own. And item
    51's header counted nine findings accepted and none answered where that round had eight and
    one; corrected. No case is exercised as a race.

56. **A repeated secret assignment refused, `selftest`'s reverses registered before each
    create, `up`'s handler armed before the claim, the creation commit held to its shape, and no
    credential on a command line.** A forty-ninth round: three findings from one reviewer and
    three from the advisory review (degraded, four of nine chunks lost, two unverdicted), five
    accepted and one answered. `secrets_load` accepted a `secrets.env` that set one of the four
    names twice, the last value winning, where the fixture writes each once: a repeated name is
    refused by line number (check 3). `selftest` registered the removal of the volume it makes
    after `docker volume create` returned, and the removal of each impostor, claimant and network
    after its create, so a signal inside the create left the object with nothing registered: each
    reverse is registered before the create, and removes only what carries this run's label
    (volumes) or what exists under the name and is not the fixture's own recorded container
    (containers, networks), so an object the create never made is not removed. `up` armed its
    exit handler after `claim_take` returned, so a signal while the create returned left the
    claim with no handler to remove it: the handler is armed first, and removes the claim only
    while it carries the nonce this `up` labelled its attempt with, so that another `up`'s claim,
    or none, is left. `evidence` took `up-manifest` as whatever `cat` returned, so an empty
    record, as an `up` interrupted while writing it leaves, had `versions.txt` name nothing as
    what ran: the record must be one full commit ID (check 22). The OpenBao token and the
    PostgreSQL password were passed to Docker as `--env NAME=value` and `-e NAME=value`, on the
    client's command line where any local user reads it, at six sites (`bao_http` in `inject`,
    whose comment claimed otherwise; `db-restore`'s `pg_restore`; `bao` and `pg` in `lib.sh`;
    `up`'s policy write; `evidence`'s dumps): each is now a prefix assignment plus `--env NAME`,
    which the client copies from its own environment, verified by hand on the pinned PostgreSQL
    image (a name given without a value is copied, and stays unset in the container when unset
    in the client). Answered, not changed: the advisory review read a window between `evidence`
    creating its `.incomplete` marker and locking it, in which a sibling could take the lock
    and scan the bundle; a sibling that wins the lock then scans a bundle that holds only the
    marker, the creator dies at its own `flock`, and nothing that capture wrote is marked
    scanned by another, so the window costs a run, never evidence. Not exercised: a signal
    between the claim's create and its return; a marker lost to a sibling.

57. **`selftest`'s creates labelled and put back only under the label, every file it moves
    aside registered first, `down`'s three records removed one by one and the declared volumes
    probed by name, and `start postgres` asked for the password.** A fiftieth round: four
    findings from one reviewer and one from the advisory review (degraded, five of nine chunks
    lost, one unverdicted), all accepted. Item 56's put-back for a create removed whatever held
    the name unless it was the fixture's own recorded container, so a stranger under a name the
    test uses (which is what made the create fail) would have been removed as the test's: every
    container and network a test creates carries this run's label, and the put-back removes
    only under it, as the volumes' did. `selftest` moved a fixture file aside for a case (the
    store directory, the injection log, the secrets, the positive control, the evidence
    directory, the cache, five `down` records, the backups directory, `versions.env` four
    times) and put it back at the end of the case with nothing registered, so a signal in
    between left the fixture without its store, its records or its secrets, the only copy under
    a name nothing reads: `restore_aside` is registered before each move (twenty-six sites)
    and puts the original back over whatever the case put under its name, and the case
    unregisters it after its own put-back. `down` removed its last three records with one
    `rm -f`, which goes on past a record it cannot remove and takes the daemon record with it,
    leaving what the next `down` refuses: one `rm` per record, in order. `down`'s last look for
    Compose volumes was by the project label alone, so a volume made under a declared name
    without the label after the removal would have passed, `down` reporting nothing left while
    the next `up` refused the name: each declared name is probed exactly, as the node volumes
    are, and a match keeps the state (not exercised: the window is inside `down`). `inject
    start postgres` waited on a query over loopback, which the image trusts, so a start with
    the role's password changed was recorded `done` while no client with this run's password
    connected: the wait goes through `pg_client`, to the container's address on the Compose
    network, as `up`'s last probe does. Check 12 changes the role's password under the server
    (from the container's environment over stdin, on no command line, verified first on a
    throwaway container of the pinned image: `-c` does not interpolate a psql variable, stdin
    does) and asserts the start is `failed` and the password is back.

58. **The unseal key and the canary over stdin, and `down`'s last look under every fixed
    name.** A fifty-first round: three findings from one reviewer, two accepted and one
    answered; the advisory review reported none (degraded, four of nine chunks lost). Item 56
    claimed no credential on a command line while `bao_unseal` still passed the unseal key to
    `bao operator unseal` as an argument, on the CLI's command line inside the container, where
    any user there reads it: that command takes the key as an argument or asks for it on a
    terminal, and refuses a pipe (verified on the pinned image), so the key goes over stdin to
    the unseal endpoint instead (`bao write sys/unseal key=-` through `docker exec -i`, as
    `bao_stdin` in `lib.sh`), and the answer is held to unsealed, since the endpoint answers
    the seal status whatever the key did. The canary went the same way at three sites no
    reviewer raised (`up`'s and `selftest`'s `kv put`, and every `selftest` statement through
    its `sql` helper): `value=-` and psql over stdin, verified first on a throwaway container
    of the pinned OpenBao image (`value=-` keeps the bytes on stdin exactly, a trailing newline
    included, so none is sent). `down`'s last look was by label for containers and networks
    (and, since item 57, by name for volumes): a container or network made under one of the
    fixture's fixed names since the removal, without the label, passed it, and the next `up`
    refused the name while this run had reported nothing left. Each of the four container
    names, both network names and, once the claim is dropped, the claim's name is probed
    exactly, as the volumes are (not exercised: the window is inside `down`). Answered, not
    changed: the reviewer read section 4 as recording an earlier revision's run; each revision
    is run from clean before it is committed, and the run recorded is the described
    revision's, as the section says.

## 6. What each experiment gets

| Experiment | Uses | Must add itself |
|---|---|---|
| [E1 extraction before persistence](https://github.com/ginsys/bronzeward/issues/2) | Talos secrets bundle and canary as marked secrets; leak scan over its own temp, staging and log paths plus the database dump, the PostgreSQL data directory as written to disk (heap, indexes, write-ahead log, temporary files) and the backups; `kill` and `start` for crash and restart | The ingestion prototype |
| [E2 structural references](https://github.com/ginsys/bronzeward/issues/3), [E2 provenance](https://github.com/ginsys/bronzeward/issues/4) | Pinned `talosctl` for upstream composition and validation; real machine configurations as inputs | Reference-syntax candidates and test documents |
| [E3 compatibility](https://github.com/ginsys/bronzeward/issues/5) | Pinned `talosctl`, a live v1.13.6 API for configuration RPCs | Older contract versions; anything needing reboot or upgrade stays deferred |
| [E4 database semantics](https://github.com/ginsys/bronzeward/issues/6) | PostgreSQL 17 on localhost, `.state/data` for SQLite files, snapshot and restore for both, `kill`/`pause`/`netsplit` | Schema, concurrent clients, the SQLite engine pin |
| [E4 approval and dispatch safety](https://github.com/ginsys/bronzeward/issues/7) | A real worker to apply to, digest read-back, `pause` and `netsplit` for interrupted and unanswered requests, the injection log as a timeline | The executor prototype and its interleaving control |
| [E5 provider capabilities](https://github.com/ginsys/bronzeward/issues/8) | OpenBao with KV v2 and Transit, real init and unseal; pinned `age` and `sops` | The local-store candidates themselves |
| [E5 retention and metadata](https://github.com/ginsys/bronzeward/issues/9) | The metadata-only token; `bao-soft-delete`, `bao-destroy`, `bao-delete-key`; `netsplit openbao` for the `unknown` state | Classification logic |
| [E5 key loss and restoration](https://github.com/ginsys/bronzeward/issues/10) | Independent database, provider and store snapshots taken at different times and restored in any combination | The scenarios |
| [Omni refresh](https://github.com/ginsys/bronzeward/issues/11) | Nothing; it is a documentation review | |

## 7. Limits

- Linux on x86-64 with rootless access to Docker only. The CLI pins are `linux-amd64` assets.
- No reboot, upgrade, reset, installer or disk behavior. No remote transport.
- One control plane and one worker. Rollout-concurrency questions across several workers need
  `--workers` raised in `bin/up`, which has not been tried.
- Kubernetes component images inside the nodes are pinned by version, not digest.
- The leak scan finds exact copies of known synthetic values. It does not find a transformed
  secret (re-encoded, split, hashed), so a clean scan is necessary evidence for E1, not sufficient.
  Talos key material is matched in the base64 form the secrets bundle and machine configuration
  carry; a decoded PEM copy would not match.
- `bin/evidence` observes PostgreSQL and OpenBao out of band, from inside their containers. With
  `netsplit openbao` a client on the published port cannot connect while `openbao-metadata.jsonl`
  still shows the provider's true metadata. The bundle records both: `openbao-client-view.txt`
  says `unreachable`, and `versions.txt` shows `networks=[]` (check 17). The `unknown`
  classification under partition is therefore the experiment's own client's to make; the bundle
  supplies the ground truth to judge it against. Reading the metadata through the partitioned
  path instead was considered and rejected: the bundle could then not tell a classifier that said
  `unknown` over an intact secret from one that said it over a destroyed secret. PostgreSQL has
  no client-view record.
- Evidence capture with a paused or partitioned Talos node is bounded by a 20-second timeout per
  read and records the node as unavailable. `bin/selftest` exercises the paused control plane
  (check 18); a partitioned node was not exercised.
- One fixture per Docker daemon, enforced by a claim container that `bin/up` creates first and
  never starts. The ownership check in `bin/down` rests on the checkout that claim names and on
  the origin directory Compose records on its containers; Talos containers, networks and volumes
  carry no owner mark of their own, so `bin/up` records the container IDs and the volume names
  in `.state/` and `bin/down` holds what it finds under the Talos label to them. A fixture whose
  claim was removed by hand needs `down --adopt`, which has no record to go by and removes by
  label. Adoption without the owning checkout's `.state/` cannot find the volumes of Talos
  containers that are already gone, and says so.
- CI shellchecks the scripts and does not run them. "Reproducible" is claimed for a host meeting
  the README prerequisites, and was exercised on one machine.
- Recovery ownership: `.state/` belongs to the person who ran `bin/up` and lives for one run.
  Nothing in it is recoverable, by design. `bin/down` is the only cleanup, is safe after a failed
  or interrupted `up`, and reports anything it could not remove.

## 8. Recommendation

Use this fixture for E1 to E5 and cite the `fixtures/versions.env` commit in every report. Keep the
QEMU provisioner for the lifecycle phases, where root on a dedicated machine is acceptable. Do not
read the presence of PostgreSQL and OpenBao here as a profile choice: the
[deployment profile decision](https://github.com/ginsys/bronzeward/issues/13) is made from the
experiments' evidence.
