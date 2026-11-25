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
credential and the current context. The first context you add becomes the current one. If you
already have another, or the `BRONZEWARD_CONTEXT` environment variable is set (it wins over the
current context), add `--context office` to every command below.

This page is written from the client's source and its specification. CI runs the commands of steps
3 to 5 against a disposable server with no machine recorded; it does not run steps 1 and 2 from
this page. The `text` blocks of the other client pages show the shape of the output, not a recorded
run.

## 2. Sign in

```bash
bronzewardctl login
```

Your browser opens at the identity provider. Sign in there. On a computer with no browser, run
`bronzewardctl login --device`: it prints a code and an address for another device.

## 3. Check who you are

```bash {.bw-run}
bronzewardctl whoami
```

The answer is what the **server** sees. It lists the context, your identity, your principal (the
server's own identifier for you), your roles, the installation's epoch and whether recovery mode is
on. You do not need the epoch yet: it matters when a command repeats a write, which this page does
not do (it is explained in [the word list](terms.md)). If a role you need is missing, no command that needs it will work; ask your administrator.
Do not try another sign-in to get it. The answer says what the server saw when you asked, not
that a command you run later is allowed.

## 4. Check the server

```bash {.bw-run}
bronzewardctl version
bronzewardctl status
```

`version` shows the client's version, then the server's version, its API revision and its start
mode. The client needs a minimum API revision, and `version` prints it, so a mismatch shows here
first. `status` shows the recovery mode, open drift (a machine changed outside Bronzeward) and
unresolved operations, and a "Health" line. A reading is not a verdict: `none` means the server
recorded none, and "not checked" means nobody asked the machines.

## 5. Read what the installation manages

```bash {.bw-run}
bronzewardctl cluster list
bronzewardctl machine list
```

Each list shows one page. Add `--all` to read every page. `-o json` prints the server's own answer
for one page, for a script. With `--all`, it prints one object `{"items": [...]}` that the client
builds from every page, so a script must not assume the same shape with and without `--all`. An
empty list shows its column names, and the machine list adds "Health is not checked in this
installation." The installation has none recorded. That does not show that no machine exists.

## What you did, and what you did not

You added a context, signed in and read the identity, the versions, the status and the two lists.
You changed nothing on the server or on any machine. Nothing here shows that a machine runs the configuration it should: that is a drift
check, in the journey pages.

## If a step fails

- `context add` fails and names the address it could not reach, or a certificate it does not
  trust: [contexts and TLS trust](contexts.md#tls-trust).
- A command says there is no context: run `bronzewardctl context list`, then `context use <name>`.
- A command exits with a number: [exit codes](cli-reference.md#global-flags-and-exit-codes).

## Next

- To stay signed in and to read a refusal: [sign in and renewal](sign-in-and-renewal.md).
- To read the machines and clusters in detail: [the inventory](inventory.md).
- For the same first read in a browser: [quick start for the web](quick-start-web.md).
- For a word you do not know: [the word list](terms.md).
