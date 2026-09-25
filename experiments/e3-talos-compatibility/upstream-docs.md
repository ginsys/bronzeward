# Upstream support-policy pages, as read

The report's §4.4 compares the machinery's encoded policy with these pages. The pages carry no
last-updated date and change without notice, so this file records what they said when read: the
table cells and sentences below are copied verbatim from the rendered text. First read
2026-09-25 about 00:25Z; re-read 2026-09-25 00:51Z with the same content.

## Talos v1.13 support matrix

<https://docs.siderolabs.com/talos/v1.13/getting-started/support-matrix>

| Talos Version | 1.13 | 1.12 |
|---|---|---|
| Release Date | 2026-04-27 (TBD) | 2025-12-22 (1.12.0) |
| End of Community Support | 1.14.0 release (2026-08-30, TBD) | 1.13.0 release (2026-04-27, TBD) |
| Kubernetes | 1.36, 1.35, 1.34, 1.33, 1.32, 1.31 | 1.35, 1.34, 1.33, 1.32, 1.31, 1.30 |

## Talos v1.14 support matrix

<https://docs.siderolabs.com/talos/v1.14/getting-started/support-matrix>

| Talos Version | 1.14 | 1.13 |
|---|---|---|
| Release Date | 2026-08-27 (TBD) | 2026-04-27 (1.13.0) |
| End of Community Support | 1.15.0 release (2026-12-27, TBD) | 1.14.0 release (2026-08-27, TBD) |
| Kubernetes | 1.37, 1.36, 1.35, 1.34, 1.33 | 1.36, 1.35, 1.34, 1.33, 1.32, 1.31 |

The two pages give different dates for the 1.14.0 release (2026-08-30 and 2026-08-27).

## Upgrading Talos (v1.13)

<https://docs.siderolabs.com/talos/v1.13/configure-your-talos-cluster/lifecycle-management/upgrading-talos>

Under "Supported upgrade paths":

> The upgrade process should handle such changes transparently, but this migration is only tested
> between adjacent minor releases. Thus the recommended upgrade path is to always upgrade to the
> latest patch release of all intermediate minor releases.

On which `talosctl` version to use:

> We recommend using the version that matches the current running version of the cluster.
