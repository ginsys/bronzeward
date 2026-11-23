# Install the clients

**No release is published yet.** There is no download page, no signed archive and no checksum file
to fetch. This page says what a release will carry, and how to build the same archives from a
checkout today. Nothing here says that a release is ready or supported.

You need two things from a release: the command-line client `bronzewardctl`, and the web files
the browser client is served from. Your administrator needs the server too. This page covers the
client and shows where the other two are described.

## What a release will carry

A release is one set of seven archives and a checksum file. The names come from
`scripts/release/check-build.sh` (lines 18-22), which checks every build against this list.
`<version>` is the tag without its leading `v`.

| File | What it is | Who needs it |
|---|---|---|
| `bronzewardctl_<version>_linux_amd64.tar.gz`, `..._linux_arm64.tar.gz` | the command-line client for Linux | an operator on Linux |
| `bronzewardctl_<version>_darwin_amd64.tar.gz`, `..._darwin_arm64.tar.gz` | the command-line client for macOS | an operator on a Mac |
| `bronzeward_<version>_linux_amd64.tar.gz`, `..._linux_arm64.tar.gz` | the server | the administrator |
| `bronzeward-web_<version>.tar.gz` | the web files, a ready web server configuration and an example client configuration | the administrator ([hosting](../deployment/hosting.md)) |
| `SHA256SUMS` | one checksum for each archive above | everyone who verifies |

Each client and server archive also holds `LICENSE-STATUS.md` and `THIRD_PARTY_NOTICES.txt`.
The release workflow (`.github/workflows/release-publish.yml`, lines 101-130) adds a software bill of
materials file for each archive (`<archive>.spdx.json`), a signature bundle for `SHA256SUMS`
(`SHA256SUMS.sigstore.json`) and a build attestation for each archive. It publishes them as a
pre-release that is never marked as the latest release.

## Build the same archives today

You can build every archive from a checkout of this repository. The result has the same names
and the same checks as a release, with a version you choose. Use a version that cannot be mistaken
for a release, such as `v0.0.0-dev`.

You need the tools the repository pins in `mise.toml` (run `mise install` once), and `python3`
and `tar` from your system. The build writes `dist/` and `build/`, which Git ignores. It builds
every platform, so it takes a while.

**What the output blocks on this page are.** A block that shows output shows its shape, read from
the source and the checks the repository runs. This page does not claim that a run printed it.
A block marked to run is run in a later documentation run, and this page is changed to say so
only after that run.

```bash {.bw-run}
BW_VERSION=v0.0.0-dev mise run package
```

The command ends with the list of archives in `dist/`. Next, check each archive against its
checksum. `sha256sum --check` reads `SHA256SUMS` and says `OK` or `FAILED` for every file. The
parentheses keep your shell in the directory you started in.

```bash {.bw-run}
( cd dist && sha256sum --check --strict SHA256SUMS )
```

```text
bronzeward-web_0.0.0-dev.tar.gz: OK
bronzeward_0.0.0-dev_linux_amd64.tar.gz: OK
bronzeward_0.0.0-dev_linux_arm64.tar.gz: OK
bronzewardctl_0.0.0-dev_darwin_amd64.tar.gz: OK
bronzewardctl_0.0.0-dev_darwin_arm64.tar.gz: OK
bronzewardctl_0.0.0-dev_linux_amd64.tar.gz: OK
bronzewardctl_0.0.0-dev_linux_arm64.tar.gz: OK
```

A checksum proves that the file you hold is the file the checksum list names. It does not prove who
made the list. For a release, the signature in the next section proves that. A build you made
yourself needs no signature: you made it.

## Verify a release

When a release exists, check it in this order. These steps need a published release, so this page
shows no command for them that you could copy and run today.

1. Download the archive you need, `SHA256SUMS` and `SHA256SUMS.sigstore.json` from the release
   page into one directory.
2. Check the signature of `SHA256SUMS` with `cosign verify-blob`, giving it the bundle. The
   signature is a keyless Sigstore signature, made by the release workflow with its GitHub
   identity and recorded in the public Rekor log. `cosign` needs two values from you: the
   identity that signed and the issuer of that identity. The issuer is GitHub's workflow token
   issuer. The identity is the Publish workflow of this repository,
   `.github/workflows/release-publish.yml`. Read both from the certificate inside the bundle
   instead of copying a value from a page you cannot check, and compare them with the workflow
   file in the repository at the release's tag.
3. Check the archive against `SHA256SUMS`. You downloaded one archive, so check only its line:
   `grep -E "^[0-9a-f]{64} [ *]<archive>$" SHA256SUMS | sha256sum --check --strict -`. The
   `[ *]` accepts both markers the file may use before the name. (Checking the whole
   file, as in the build above, reports every archive you did not download as missing.)
4. Check the build attestation of the archive with `gh attestation verify <archive> --repo
   ginsys/bronzeward`.

If step 2 fails, do not use the archives. If you cannot run `cosign`, you can still use the
checksum check. Then you trust whoever gave you `SHA256SUMS`.

## Install the command-line client

Unpack the archive for your system and put `bronzewardctl` on your `PATH`. This puts it in
`~/.local/bin`. Check that this directory is on your `PATH` (`echo "$PATH"`); if it is not, add it
to your shell's start-up file.

```bash {.bw-run}
mkdir -p ~/.local/bin
tar -xzf dist/bronzewardctl_0.0.0-dev_linux_amd64.tar.gz -C dist bronzewardctl
install -m 0755 dist/bronzewardctl ~/.local/bin/bronzewardctl
```

Use the archive for your system and version. The archive also holds `LICENSE-STATUS.md` and
`THIRD_PARTY_NOTICES.txt`; read them before you use the client.

Check that it starts. At this point you have no context, which is the server your client talks to
([contexts](contexts.md) explains them). So the command prints the client's part and then stops
with exit status 1, which the [reference](cli-reference.md#global-flags-and-exit-codes) lists as
"refused or failed". That is the expected result here.

```bash
bronzewardctl version
```

```text
Client  bronzewardctl v0.0.0-dev, needs API revision 2 or later
Server  unknown
bronzewardctl: no context selected: add one with bronzewardctl context add
```

`bronzewardctl context list` also works with no server and exits 0. It prints a header and no rows.

```bash {.bw-run}
bronzewardctl context list
```

```text
CURRENT  NAME  SERVER  CREDENTIAL
```

## Next

- Your administrator hosts the web files and the server: [hosting](../deployment/hosting.md),
  [identity provider](../deployment/identity-provider.md) and [upgrade](../deployment/upgrade.md).
- To reach a server: [contexts and TLS trust](contexts.md).
- A first read from the command line: [quick start for the command line](quick-start-cli.md).
- A first read in a browser: [quick start for the web](quick-start-web.md).
