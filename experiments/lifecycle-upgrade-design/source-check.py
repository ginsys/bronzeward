#!/usr/bin/env python3
"""Inspect pinned public sources; never execute upstream or contact a cluster."""

import argparse
import hashlib
import json
from pathlib import Path
import sys
from urllib.request import urlopen


def check(condition, name):
    if not condition:
        raise ValueError(name)
    print(f"PASS\t{name}")


def ordered(source, *terms):
    position = 0
    for term in terms:
        position = source.find(term, position)
        if position < 0:
            return False
        position += len(term)
    return True


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("cache", type=Path, help="disposable directory for upstream source files")
    parser.add_argument("--fetch", action="store_true", help="download missing pinned public files")
    args = parser.parse_args()
    sources = {}
    manifest = json.loads(Path(__file__).with_name("sources.json").read_text())
    for row in manifest:
        path = args.cache / row["id"]
        if not path.exists() and args.fetch:
            url = f"https://raw.githubusercontent.com/{row['repository']}/{row['revision']}/{row['path']}"
            with urlopen(url, timeout=30) as response:
                data = response.read()
            check(hashlib.sha256(data).hexdigest() == row["sha256"], row["id"] + " downloaded hash")
            args.cache.mkdir(parents=True, exist_ok=True)
            path.write_bytes(data)
        data = path.read_bytes()
        check(hashlib.sha256(data).hexdigest() == row["sha256"], row["id"] + " pinned hash")
        sources[row["id"]] = data.decode("utf-8")

    upgrade = sources["talos-upgrade"]
    check(ordered(upgrade, "func upgradeViaLifecycleService", "imagePullInternal(", "upgradeInternal(",
                  "nodeNames, err = drainNodes(", "defer func()", "rebootInternal("),
          "lifecycle image installation precedes drain and reboot")
    check(ordered(upgrade, "nodeNames, err = drainNodes(", "if err != nil {", "return err", "defer func()"),
          "drain error returns before deferred uncordon is installed")
    check('semver.MustParseRange(">1.13.0-alpha.2 <2.0.0")' in upgrade,
          "API selection includes the prerelease boundary")
    k8s = sources["talos-k8s"]
    check('if options.PrePullImages {' in k8s and
          'if options.PrePullImages {\n\t\tif err = prePullImages(' in k8s,
          "pre-pull condition does not include DryRun")
    check(ordered(k8s, "func syncManifestsSSA", "ssa.NewManager(", "if options.DryRun {", "manager.Diff("),
          "dry-run manifest path calls SSA Diff")
    check("inv, err := m.inventory(ctx)" in sources["ssa-diff"] and
          "return GetInventory(ctx" in sources["ssa-manager"] and
          "configmap.AssureInventoryNamespace(ctx" in sources["ssa-inventory"] and
          "return configmap.NewInventory(ctx" in sources["ssa-inventory"] and
          ".ConfigMaps(namespace).Create(ctx" in sources["ssa-configmap"] and
          ".Namespaces().Create(ctx" in sources["ssa-configmap"],
          "diff inventory path can create a missing namespace or ConfigMap")
    check("m.resourceManager.ApplyAllStaged(" in sources["ssa-apply"] and
          ordered(sources["flux-apply"], "func (m *ResourceManager) apply(",
                  "client.ForceOwnership", "m.client.Patch(ctx, object, client.Apply, opts...)"),
          "manifest apply reaches unconditional SSA field ownership")
    check("github.com/siderolabs/go-kubernetes v0.2.41" in sources["talos-module"] and
          "github.com/fluxcd/pkg/ssa v0.77.0" in sources["ssa-module"],
          "inspected dependency versions match the upstream module pins")
    check("The `--mode=reboot` option was removed in Talos 1.14" in sources["configuration-doc"] and
          "This list is not exhaustive" in sources["configuration-doc"],
          "documentation separates explicit reboot and incomplete impact catalogue")
    # A broken ordering assumption must be detectable; this is a string-check control,
    # not a mutation of upstream code or evidence of lifecycle behavior.
    check(not ordered("drain install reboot", "install", "drain", "reboot"),
          "control rejects reversed synthetic phase order")
    print("LIMIT\tstatic call-path inspection only; no Talos or Kubernetes operation executed")


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, KeyError) as error:
        print(f"FAIL\t{error}", file=sys.stderr)
        sys.exit(1)
