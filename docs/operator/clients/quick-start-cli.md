# Quick start: the command line

This page takes you from an installed `bronzewardctl` to your first read of a running installation:
who you are, what the server is, and what machines it knows. It writes nothing. It assumes one
person who holds every role they need, and a server that your administrator already set up
([hosting](../deployment/hosting.md), [identity provider](../deployment/identity-provider.md)).

You need three things from your administrator:

- the web address of the installation, for example `https://bronzeward.example.com`;
- a certificate file, if the installation uses a private certificate authority;
- an account at the identity provider.

Install the client first: [install](install.md). Then follow the steps in order.

## 1. Add a context

A context tells the client which server to talk to and how you sign in
([contexts](contexts.md) explains the choices). `--from` reads the identity provider's address
from the installation itself.

```bash
bronzewardctl context add office --from https://bronzeward.example.com
```

If the installation uses a private certificate authority, add `--certificate-authority
./office-ca.pem`. The result names the server, where its certificate trust comes from, the
credential and the current context.

## 2. Sign in

```bash
bronzewardctl login
```

Your browser opens at the identity provider. Sign in there. On a computer with no browser, run
`bronzewardctl login --device`: it prints a code and an address for another device.

## 3. Check who you are

```bash
bronzewardctl whoami
```

The answer is what the **server** sees: your identity, the roles that came from your sign-in and
the installation's epoch. If a role you need is missing, no command that needs it will work; ask
your administrator. Do not try another sign-in to get it.

## 4. Check the server

```bash
bronzewardctl version
bronzewardctl status
```

`version` shows the client's version, the server's version and the server's API revision. The
client refuses to write to a server older than it can use, so a mismatch shows here first.
`status` shows whether the installation is in normal or recovery mode, whether any machine has
changed outside Bronzeward (drift) and whether any operation is unresolved. A reading is not a
verdict: `status` says what the server knows now, not that the machines are well.

## 5. Read what the installation manages

```bash
bronzewardctl cluster list
bronzewardctl machine list
```

Each list shows one page. Add `--all` to read every page. `-o json` prints the server's own
answer unchanged, for a script.

## What you did, and what you did not

You added a context, signed in and made four reads. You changed nothing on the server or on any
machine. Nothing here shows that a machine runs the configuration it should: that is a drift
check, in the journey pages. The reference lists every command: [command reference](cli-reference.md).

## If a step fails

- `context add` fails and names the address it could not reach, or a certificate it does not
  trust: [contexts and TLS trust](contexts.md#tls-trust).
- A command says there is no context: run `bronzewardctl context list`, then `context use <name>`.
- A command exits with a number: [exit codes](cli-reference.md#global-flags-and-exit-codes).
