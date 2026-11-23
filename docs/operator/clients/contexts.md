# Contexts, TLS trust and sign-in

A **context** is one server that `bronzewardctl` knows, with the way you sign in to it. You can keep
several, for example one for a test installation and one for the real one. A command picks its
context in this order: the `--context <name>` flag, then the `BRONZEWARD_CONTEXT` environment
variable, then the **current** context.

The `text` blocks on this page show the shape of the output, read from the source. This page does
not claim that a run printed them.

The reference lists every flag of the commands on this page:
[context](cli-reference.md#context), [login](cli-reference.md#login), [whoami](cli-reference.md#whoami).
The [global flags and exit codes](cli-reference.md#global-flags-and-exit-codes) are there once and
are not repeated here.

## Where contexts are kept

The client keeps its contexts in one file, `bronzeward/config.yaml` in your configuration
directory (`$XDG_CONFIG_HOME`, else `~/.config`, on Linux). The file holds names, addresses and
paths. It holds no password and no token. `bronzewardctl context list` reads this file and asks no
server.

```bash {.bw-run}
bronzewardctl context list
```

```text
CURRENT  NAME    SERVER                          CREDENTIAL
*        office  https://bronzeward.example.com  oidc
```

The star marks the current context. `bronzewardctl context use <name>` moves it. The first context
you add becomes the current one. A context you add later does not: select it with `--context`, or
with `context use`.

## Add a context

A server answers on one address, and your identity provider answers on another. There are two ways
to tell the client about both.

**From the server's own web address (recommended).** The administrator publishes a small file,
`bronzeward-client.json`, next to the web files. It names the identity provider and the client's
identity there. It holds no secret
([the file](../deployment/hosting.md#the-client-configuration-file)). `--from` reads it, and uses
the same address as the server:

```bash
bronzewardctl context add office --from https://bronzeward.example.com
```

The client asks the server `GET /livez` before it saves anything, so a wrong address fails now and
not in the middle of your work.

**By hand.** Give the server and the identity provider's values yourself. You cannot mix this
with `--from`.

```bash
bronzewardctl context add office --server https://bronzeward.example.com \
  --issuer https://idp.example.com --client-id <the CLI client's ID> --audience <the server's audience>
```

A context for **automation** has no identity provider. It uses a long-lived automation token you
keep in a file ([below](#credentials-for-a-script)):

```bash
bronzewardctl context add ci --server https://bronzeward.example.com --automation-token-file ~/.config/bronzeward-ci.token
```

The default credential is `oidc` when you give any identity provider flag and `automation`
otherwise. `--credential` names it, and refuses a combination that cannot work.
Paths you give are saved as absolute paths, so a context works from any directory.

`context remove <name>` removes the context and its stored sign-in. A token file you made stays
where you put it.

## TLS trust

The client checks the server's certificate. By default it trusts your system's certificate store.
Some installations use a certificate from a private authority. For that, give the client the
authority's certificate:

```bash
bronzewardctl context add office --from https://bronzeward.example.com --certificate-authority ./office-ca.pem
```

Know three things about the flag:

- The file is a PEM bundle of certificates. If the file holds no certificate, the command refuses.
- The bundle **replaces** the system store for this server. It does not add to it. Put every
  authority this server needs in the bundle.
- `--certificate-authority` is for the Bronzeward server. The identity provider has its own flag,
  `--oidc-certificate-authority`, because it often has a different authority.

The client never skips the check. There is no flag that does. A certificate that is not trusted
stops the command before anything is sent, and the message names the host, the certificate's
subject and issuer, and which trust it used (the system store, or your bundle). The fix is the
right bundle in the context, not a way around the check.

## Sign in as a person

An `oidc` context needs a sign-in before most commands work.

```bash
bronzewardctl login
```

`login` opens your browser on this computer. On a computer with no browser, for example a server you
reach over `ssh`, use the device flow: it prints a short code and an address, and you finish the
sign-in on any other device.

```bash
bronzewardctl login --device
```

`bronzewardctl whoami` shows what the server sees: the context, your identity, your roles and
whether recovery mode is on. Use it after every sign-in to check that you are the person you meant
to be.
`bronzewardctl logout` removes the stored sign-in and revokes its refresh token.

### Where the sign-in is kept

In this order:

1. **A token file**, if the context has one (`--oidc-token-file <path>` when you add it). Use this
   when the system has no credential store, or when you want the sign-in somewhere you can see.
   Treat the file like a password.
2. **The system credential store**: the Secret Service (for example GNOME Keyring or KWallet) on
   Linux, and the Keychain on macOS. No other system's store is supported yet.
3. **This process's memory only.** When neither exists, you sign in again for every command.
   The client says so, with the reason.

With `--no-input` the client never opens a locked credential store's unlock prompt: a locked
store is refused.

## Credentials for a script

An automation token starts with `bwt_`. Your administrator creates it ([identities and
roles](../README.md#identities-and-roles)). The client never takes it as an argument or from an
environment value, since both can end up in logs and process lists. Give it by:

| How | Use |
|---|---|
| `--token-file <path>` | one command |
| `automation.tokenFile` in the context (`--automation-token-file`) | every command of the context |
| `BRONZEWARD_TOKEN_FILE=<path>` | a variable that names a file, never the token itself |
| `--token-stdin` | one command, token on standard input |

The file must be a regular file that only you can read (mode `600`). The client refuses a file
others can read, and says to run `chmod 600`. It must hold one line that starts with `bwt_`.

Name the automation context with `--context`. If another context is the current one and it signs
in with `oidc`, the client refuses `--token-stdin` and `--token-file` for it.

```bash
bronzewardctl --context ci --token-stdin status < ~/.config/bronzeward-ci.token
```

## Exit codes

The client's exit codes are in the [reference](cli-reference.md#global-flags-and-exit-codes). A
script should test for them and not read the text of a message.

## Next

[A first read from the command line](quick-start-cli.md) or [in a browser](quick-start-web.md).
