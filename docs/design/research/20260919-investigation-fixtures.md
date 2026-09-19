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
  directory; soft-deletes and destroys a KV version; deletes a Transit key.
- `bin/evidence` records versions, logs, a database dump, OpenBao metadata read through the
  metadata-only identity, and a digest of each node's machine configuration, then scans all of it
  for synthetic secret material, with a positive control. It runs while parts of the fixture are
  down: a source it cannot read is named in `unavailable.txt`, and a scan input it cannot read
  fails the command.
- `bin/down` removes everything and fails if anything is left.
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
`up` and `down` included. Review added checks 9, 11, 13 and 16 and widened 17 (section 5, items 8 and
9); the revision described here passed 18 of 18 from clean, and `selftest --keep` passed twice in a
row against one fixture. Those runs were not timed.

| # | Check | Expected | Observed |
|---|---|---|---|
| 1 | All four containers running | running | pass |
| 2 | Full-config `no-reboot` apply on the worker | The configuration read back is byte-identical to the one sent | pass: equal SHA-256 once the single extra trailing newline of `-o jsonpath` output is normalized |
| 3 | `db-snapshot`, diverge, `db-restore` | Row written after the snapshot is gone | pass |
| 4 | `bao-soft-delete` | Metadata shows a deletion time, `destroyed` false | pass |
| 5 | `bao-destroy` | Metadata shows `destroyed` true | pass |
| 6 | `bao-delete-key` | Transit key no longer readable | pass |
| 7 | `bao-restore` of the earlier snapshot | Destroyed version and deleted key are back | pass |
| 8 | `store-snapshot` / `store-restore` | Deleted file is back | pass |
| 9 | `bin/evidence` while PostgreSQL is dead | Does not abort; `unavailable.txt` names the missing live dump; the database backup is still expanded and scanned | pass |
| 10 | `kill` / `start` PostgreSQL | No answer while dead; committed row survives | pass |
| 11 | `bin/evidence` while OpenBao is dead | `unavailable.txt` names the provider metadata; `openbao-metadata.jsonl` holds an `unknown` record, not an empty file | pass |
| 12 | `kill` / `start` OpenBao | Comes back sealed, unseals with the stored key, state intact | pass |
| 13 | `netsplit` / `netjoin` OpenBao, with `bin/evidence` during the split | Published port does not answer; bundle records the client view as `unreachable` and still holds the true metadata; port answers again after `netjoin` | pass |
| 14 | `pause` / `unpause` worker | No Talos API answer while paused, answers after | pass |
| 15 | `netsplit` / `netjoin` worker | No answer while detached, answers again on the same address | pass |
| 16 | `bin/evidence` with a scan path that does not exist | Refuses, non-zero exit | pass |
| 17 | Evidence and leak scan on the healthy fixture | No `unavailable.txt`; client view `reachable`; finds the canary in the live dump and in the expanded backup; finds a planted copy of the OpenBao metadata-only token and a plain file planted next to the OpenBao snapshots in `.state/backups`; lists the Transit key; finds nothing in container logs | pass |
| 18 | `bin/down` | No container, network, volume or state directory left | pass |

After the second run of the first revision, an independent `docker volume ls --filter dangling=true` showed exactly the
volumes that existed on the host before the first run.

Check 7 is worth a note for [key-loss testing](https://github.com/ginsys/bronzeward/issues/10): a
provider snapshot restores a *destroyed* secret version and a *deleted* Transit key. "Lost" in the
current provider and "recoverable from a provider backup" are separate facts, as
[design §7.6](../Talos_Configuration_and_Machine_Management_Design.md#76-metadata-only-dependency-checks)
already words it.

Check 2 matters for the [execution contract](../../spec/execution-recovery.md): an observed
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
   eight characters, since an empty pattern matches every line. Checks 9, 11, 16 and 17 hold these.
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

## 6. What each experiment gets

| Experiment | Uses | Must add itself |
|---|---|---|
| [E1 extraction before persistence](https://github.com/ginsys/bronzeward/issues/2) | Talos secrets bundle and canary as marked secrets; leak scan over its own temp, staging and log paths plus the database dump and backups; `kill` and `start` for crash and restart | The ingestion prototype |
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
  says `unreachable`, and `versions.txt` shows `networks=[]` (check 13). The `unknown`
  classification under partition is therefore the experiment's own client's to make; the bundle
  supplies the ground truth to judge it against. Reading the metadata through the partitioned
  path instead was considered and rejected: the bundle could then not tell a classifier that said
  `unknown` over an intact secret from one that said it over a destroyed secret. PostgreSQL has
  no client-view record.
- Evidence capture with a paused or partitioned Talos node is bounded by a 20-second timeout per
  read and records the node as unavailable. That path was read, not exercised by `bin/selftest`.
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
