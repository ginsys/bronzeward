# Investigation fixtures

A disposable environment for the feasibility experiments (design §18.1): a two-node Talos cluster,
PostgreSQL, OpenBao, and pinned `age`/`sops` CLIs, all created from nothing and removed completely.
Rationale, alternatives and recorded results are in the
[fixture investigation report](../docs/design/research/20260919-investigation-fixtures.md).

Nothing here selects the PoC database, provider or deployment profile. Every candidate is present
so the experiments can compare them.

## Prerequisites

Linux on x86-64, Docker Engine with the compose v2 plugin, and `bash`, `curl`, `jq`, `git`, `tar`,
`find`, `awk` and GNU coreutils (`sha256sum`, `timeout`, `install`). Each command checks for what
it needs before doing anything. The user must be able to run `docker` without `sudo`. About 4 GiB
of free memory and 1.5 GiB of image downloads on first run. No other tool is taken from the host: `talosctl`, `sops`
and `age` are downloaded into `fixtures/.cache/` and refused on a checksum mismatch.

## Commands

| Command | Does |
|---|---|
| `fixtures/bin/up` | Generates synthetic secrets, starts PostgreSQL and OpenBao, initializes and unseals OpenBao, creates the Talos cluster. Takes about two minutes once images are cached. |
| `fixtures/bin/inject <action>` | Failure injection and backup/restore. Run it without arguments for the list. Every action is timestamped in `.state/injections.log`. |
| `fixtures/bin/evidence [path ...]` | Writes versions, container logs, a database dump, OpenBao metadata and Talos config digests to `.state/evidence/<utc>/`, then scans them, and any extra paths given, for synthetic secret material. Prints the bundle path. |
| `fixtures/bin/down [--purge]` | Removes every container, network, volume and `.state/`, then checks that nothing is left and fails if something is. `--purge` also removes `.cache/`. |
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
  tokens. Values the fixture chooses itself start with `BWSYNTH-`.
- `bao-init.json`: the single OpenBao unseal key and root token.
- `talos-secrets.yaml`, `controlplane.yaml`, `talosconfig`, `kubeconfig`: the cluster's own
  generated secrets bundle and client configs.
- `scan-patterns.txt`: every one of the above as a fixed string. `bin/evidence` reports the files
  that contain any of them, by count only; a matched line is never printed.

The scan has a positive control, `.state/data/canary-control.txt`. If the scan does not find it, or
cannot read a path or file it was given, the command fails, because an empty result would then
prove nothing. `bin/evidence` is meant to run while parts of the fixture are down: a source it
cannot read, such as the live database after `inject kill postgres`, is named in
`unavailable.txt` in the bundle and everything else is still captured and scanned. Compressed
backups are expanded before scanning, without needing the database server. OpenBao snapshots are
encrypted by OpenBao and are scanned as they are. Talos key material is matched in the base64 form
the secrets bundle carries, so a decoded PEM copy would not match.

Use only these values in experiments. Never point a prototype at a real cluster, vault or database
from here.

## Ownership and recovery

`.state/` belongs to whoever ran `bin/up`, and its lifetime is one run. Nothing in it is backed up
or recoverable by design: losing `bao-init.json` loses that OpenBao instance, which is the
intended way to study key loss. `bin/down` is the only cleanup and is safe to run at any time,
including after a failed or interrupted `up`. Evidence worth keeping must be copied out of `.state/`
before `down`.

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
- Talos node volumes are anonymous. `bin/down` removes the ones attached to fixture containers. If
  `talosctl cluster create` fails before a container exists, its empty volumes cannot be told apart
  from anyone else's and are left behind; `docker volume ls --filter dangling=true` shows them.
- A host that restarts the Docker daemon when a new bridge interface appears, for example through
  a NetworkManager dispatcher script, stops the containers that were already running each time a
  fixture network is created. `bin/up` detects this and fails. Fix the host hook so that it ignores
  `br-*`, `veth*` and `docker0`; the fixture does not work around it.
- `bin/evidence` observes OpenBao and PostgreSQL out of band, from inside their containers. After
  `inject netsplit openbao` every client finds the provider unreachable while the bundle still
  shows its true metadata; the partition itself is visible as an empty `networks=[]` on that
  container's line in `versions.txt`. An experiment compares what its own client concluded with
  that ground truth. The bundle reports `unknown` only when the provider cannot be asked at all:
  stopped, sealed, paused, or a listing that fails.
- CI lints these scripts but does not run them.
