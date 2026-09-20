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
2, 4, 8, 9, 10, 19, 21, 22 and 23 (section 5, items 8 to 25); the revision described here passed 24 of 24 from
clean. `selftest --keep`, which skips `up` and checks 23 and 24, passed 22 of 22 twice in a row
against one fixture. Those runs were not timed.

| # | Check | Expected | Observed |
|---|---|---|---|
| 1 | All four containers running | running | pass |
| 2 | `inject`, `evidence` and `down` with the claim container removed, then `inject` and `evidence` with one labelled for another checkout | All five calls refuse, `down` by naming the missing claim; PostgreSQL is not paused and nothing is removed | pass |
| 3 | Full-config `no-reboot` apply on the worker | The configuration read back is byte-identical to the one sent | pass: equal SHA-256 once the single extra trailing newline of `-o jsonpath` output is normalized |
| 4 | `db-snapshot`, diverge, `db-restore`, then `db-restore` of a dump cut in half | Row written after the snapshot is gone; the truncated restore fails, leaves the live database with its rows and leaves no scratch database | pass |
| 5 | `bao-soft-delete` | Metadata shows a deletion time, `destroyed` false | pass |
| 6 | `bao-destroy` | Metadata shows `destroyed` true | pass |
| 7 | `bao-delete-key` | Transit key no longer readable | pass |
| 8 | A second `bao-snapshot` under a `CURL_HOME` whose curlrc sets `output`; `bao-restore` of the earlier snapshot | The curlrc is ignored: nothing is written where it points and the snapshot is not empty; destroyed version and deleted key are back | pass |
| 9 | `store-snapshot` / `store-restore`, then `store-restore` of a corrupt archive, a `store-snapshot` with an excluding `TAR_OPTIONS` in the environment, a `store-snapshot` whose final name is a symlink to a directory, `store-restore` of an archive whose `data` is a symlink, a `store-snapshot` over a snapshot that has a second hard-linked name, a `store-snapshot` whose name holds a slash, one whose name holds a newline, one whose name holds `.partial.`, an action word with a newline and an action that does not exist, a `store-snapshot` with `injections.log` replaced by a symlink to a file outside `.state` and then with a second hard-linked name, then `store-restore` with the live directory gone, then `kill postgres` with the PostgreSQL container renamed aside and an unlabelled container created under its name, then `pause worker` with the worker renamed aside and a container created under its name carrying the Talos cluster-name label | Deleted file is back; the corrupt restore fails and leaves the live directory, positive control included, untouched; the snapshot holds the file `TAR_OPTIONS` named; the symlinked name is replaced by the snapshot and nothing is written into the directory it pointed at; the archive with the symlinked `data` is refused and the live directory stays; the name with a slash is refused and nothing is written under it; the snapshot with a second name is not replaced and no partial is left; the name and the action with a newline, the name with `.partial.` and the unknown action are refused before anything is logged; with the log linked either way the action is refused, no snapshot is taken and the file behind the symlink keeps its content; the missing directory is restored, and an archive holding an entry named `previous`, restored while the directory is gone, is refused without that entry being installed; the kill and the pause are each refused and not logged, and the real containers are untouched | pass |
| 10 | `bin/evidence` while PostgreSQL is dead | Does not abort; `unavailable.txt` names the missing live dump; the database backup is still expanded and scanned; the data directory is read from the stopped container and the canary found in it | pass |
| 11 | `db-snapshot` while PostgreSQL is dead | Fails; no file of that snapshot name is left in `.state/backups`; `injections.log` records it as `failed` and the preceding `kill` as `done` | pass |
| 12 | `kill` / `start` PostgreSQL | No answer while dead; committed row survives | pass |
| 13 | `bin/evidence` while OpenBao is dead | `unavailable.txt` names the provider metadata; `openbao-metadata.jsonl` holds an `unknown` record, not an empty file | pass |
| 14 | `kill` / `start` OpenBao | Comes back sealed, unseals with the stored key, state intact | pass |
| 15 | `bao-snapshot` while OpenBao is paused | The request ends on its own; the action fails, is logged as `failed` and leaves no snapshot file | pass |
| 16 | A 5-second wait on an authenticated OpenBao request while the server process inside the container is stopped (`SIGSTOP`), so that the CLI connects and is never answered | The wait fails within 10 seconds; OpenBao answers again after `SIGCONT` | pass |
| 17 | `netsplit` / `netjoin` OpenBao, with `bin/evidence` during the split | Published port does not answer; bundle records the client view as `unreachable` and still holds the true metadata; port answers again after `netjoin` | pass |
| 18 | `bin/evidence` while the control plane is paused | Control-plane config digest `UNAVAILABLE`; worker digest still read, because each node is asked on its own address | pass |
| 19 | `kill` / `start` worker, then `pause` / `unpause` worker | The first request after `start` returns is answered, without a wait; no Talos API answer while paused, answers after | pass |
| 20 | `netsplit` / `netjoin` worker | No answer while detached, answers again on the same address | pass |
| 21 | `bin/evidence` with a scan path that does not exist, then with a mode-000 file whose name holds a secret (skipped when run as root) | Refuses both, non-zero exit; the second message withholds the name, and stderr holds no secret | pass |
| 22 | Evidence with the positive control replaced by a symlink to a file of the same content outside the scanned paths, then with a hard link to the control outside them, then with `.state/evidence` replaced by a symlink, then with `.state/secrets.env` replaced by a symlink, with a command substitution appended to it and with a second hard-linked name, then with a second name for `bao-init.json` and for `injections.log`, then with `GIT_DIR` pointing at no repository, then with the control's canary replaced by another secret, then with a hard link to a store snapshot archive, then with `scan-patterns.txt` cut down to the prefix, then with `secrets.env` short of its canary line while the caller's shell holds that variable, then with `.cache` replaced by a symlink, then with what an interruption leaves (a complete dump, a complete archive and a truncated archive under their `.partial.` names, a `store-restore` staging directory holding a copy of the control, and a database under the `db-restore` scratch name `bronzeward_previous`), then with the live store directory moved aside as a killed `store-restore` leaves it, then with a `store-restore` staging directory that is a symlink to a directory outside `.state` holding a copy of the control; then evidence and leak scan on the healthy fixture, run from another directory with the relative scan paths `-delete` and `-delete.d`, a symlinked file under `.state/data`, a leaking file named `canary-control.txt` outside `.state/data`, a benign file with a secret in its name, a scan path that is a symlink with a secret in its name, a symlink with a benign name and benign content behind it whose stored target path holds a secret, a leaking file whose name holds a newline, and a planted copy of the `talosconfig` client key | The symlinked and the hard-linked control each fail the capture for want of its control; the symlinked evidence directory is refused and nothing is written through it; the symlinked, the appended and the hard-linked `secrets.env` are each refused and the appended command does not run; the hard-linked `bao-init.json` and `injections.log` are refused; the capture without git fails; the replaced control is reported as a leak and the capture fails for want of its control; the control copy expanded from the hard-linked archive is reported as a leak; the cut-down pattern list is refused; the incomplete `secrets.env` is refused rather than completed from the environment; the symlinked cache directory is refused; the interruption leftovers are each reported: the dump expanded from its temporary name, the control copy in the staging directory, and the canary in the scratch database's dump, while the truncated archive is named in `unavailable.txt` and the control's copy from the complete archive under its temporary name is not a leak; with the live directory aside the capture succeeds, names the absent directory in `unavailable.txt` and does not report the control set aside; the symlinked staging directory is refused; in the last run all are scanned and reported, nothing is deleted; the file that only shares the control's name is reported as a leak, the control's copy inside the expanded store snapshot is not; exactly the two secret-bearing names are reported, with the names withheld, and the report itself holds no secret; the file whose name holds a newline is reported on one line, in quoted form, and every line of the report is one entry; the client key is found; no `unavailable.txt`; client view `reachable`; finds the canary in the live dump, in the expanded backup and in the copied PostgreSQL data directory, whose `pg_wal` is in the bundle; finds a planted copy of the OpenBao metadata-only token and a plain file planted next to the OpenBao snapshots in `.state/backups`; lists the Transit key; finds nothing in container logs | pass |
| 23 | `bin/down` with `.state/down-node-volumes` replaced by a symlink and `down-node-volumes-incomplete` a symlink to a file outside `.state`, then with a second hard-linked name for the record, then with the marker a dangling symlink, then with a name in the record that is no anonymous volume's, then with the name of a labelled volume created for the test, then with a never-started container created for the test that carries only the Talos cluster-name label, then with the record of the Talos container IDs moved aside, then `bin/down`, then the daemon's whole volume list against the one taken before `up` | The first seven `down` refuse, remove nothing and leave `.state`, the file behind the marker link keeps its content, the labelled volume still exists and so does the labelled container, and both Talos containers survive the run without the record; after the eighth no container, network, volume or state directory is left; no volume exists that did not before `up` | pass |
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

Each of these is reproducible and each changed the fixture.

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
