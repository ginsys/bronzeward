#!/bin/bash
# Generate the synthetic cluster for the bw-861 note: one secrets bundle, one control plane and one worker
# configuration (pinned talosctl v1.14.5), then six per-machine variants made with `talosctl machineconfig patch`.
# usage: gen.sh <work dir>   (the directory is created, mode 0700; nothing here is a real credential)
set -eu
W=$1
T=$(dirname "$0")/bin/talosctl
mkdir -p "$W/patches" "$W/machines"
chmod 700 "$W"

"$T" gen secrets --output-file "$W/secrets.yaml" >/dev/null
"$T" gen config bw-synth https://10.55.0.2:6443 --with-secrets "$W/secrets.yaml" --output-dir "$W/cfg" --force >/dev/null
ls "$W/cfg"

# per-machine patches (strategic merge, the form talosctl machineconfig patch takes)
mk() { # name role disk addr extra-file
  local n=$1 disk=$2 addr=$3
  # v1.14.5 keeps the hostname in a HostnameConfig document and the install disk in an
  # UnattendedInstallConfig document; the legacy machine.network.hostname and machine.install are refused
  cat >"$W/patches/$n.yaml" <<EOF
machine:
  network:
    interfaces:
      - interface: eth0
        addresses:
          - $addr
---
apiVersion: v1alpha1
kind: HostnameConfig
hostname: $n
---
apiVersion: v1alpha1
kind: UnattendedInstallConfig
provisioning:
  diskSelector:
    match: disk.dev_path == "$disk"
EOF
  # a merge cannot drop the generated 'auto: stable' (null does not delete it, and auto and
  # hostname are exclusive), so the generated document is deleted first and a new one added
  cat >"$W/patches/$n-hostdel.yaml" <<EOF
apiVersion: v1alpha1
kind: HostnameConfig
\$patch: delete
EOF
}
mk cp1 /dev/sda 10.55.0.11/24
mk cp2 /dev/nvme0n1 10.55.0.12/24
mk cp3 /dev/sda 10.55.0.13/24
mk w1 /dev/sda 10.55.0.21/24
mk w2 /dev/sda 10.55.0.22/24
mk w3 /dev/sda 10.55.0.23/24

# shared extras: a registry mirror on every worker; a node label on cp3; a sysctl on w3 only
cat >"$W/patches/workers-mirror.yaml" <<EOF
machine:
  registries:
    mirrors:
      docker.io:
        endpoints:
          - http://10.55.0.99:5000
EOF
cat >"$W/patches/cp3-label.yaml" <<EOF
apiVersion: v1alpha1
kind: KubeNodeConfig
labels:
  topology.kubernetes.io/zone: zone-b
EOF
cat >"$W/patches/w3-sysctl.yaml" <<EOF
machine:
  sysctls:
    net.core.somaxconn: "4096"
EOF

p() { # base out patches...
  local base=$1 out=$2
  shift 2
  local args=()
  for f in "$@"; do args+=(--patch "@$W/patches/$f.yaml"); done
  "$T" machineconfig patch "$base" "${args[@]}" --output "$out"
}
p "$W/cfg/controlplane.yaml" "$W/machines/cp1.yaml" cp1-hostdel cp1
p "$W/cfg/controlplane.yaml" "$W/machines/cp2.yaml" cp2-hostdel cp2
p "$W/cfg/controlplane.yaml" "$W/machines/cp3.yaml" cp3-hostdel cp3 cp3-label
p "$W/cfg/worker.yaml" "$W/machines/w1.yaml" w1-hostdel w1 workers-mirror
p "$W/cfg/worker.yaml" "$W/machines/w2.yaml" w2-hostdel w2 workers-mirror
p "$W/cfg/worker.yaml" "$W/machines/w3.yaml" w3-hostdel w3 workers-mirror w3-sysctl

for m in "$W"/machines/*.yaml; do
  if out=$("$T" validate --config "$m" --mode metal 2>&1); then rc=0; else rc=$?; fi
  echo "validate $(basename "$m") rc=$rc: $(printf '%s' "$out" | tr '\n' ' ' | cut -c1-200)"
done
wc -l "$W"/machines/*.yaml
