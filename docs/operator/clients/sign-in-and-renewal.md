# Journey: sign in, stay signed in, and read a refusal

**You want** to sign in to an installation, to be sure who the installation thinks you are, and to
know what to do when it stops accepting you. Adding a context and its trust settings is on
[contexts](contexts.md); this page starts with a context that exists.

**You need** no role to sign in. The roles you hold decide what you may do afterwards, and the server
reports them: you cannot choose them here.

## Sign in and check who you are

The `text` blocks on this page show the shape of the output, read from the source. This page does
not claim that a run printed them.

**Command line.** `login` signs in to the context the client selects: the `--context` flag, else
the `BRONZEWARD_CONTEXT` environment variable, else the current context. Name the context with
`--context` when you are not sure which one is current. It opens your browser on this computer; `--device` is for a computer with no browser
(see [contexts](contexts.md#sign-in-as-a-person)). `whoami` then asks the server.

```bash
bronzewardctl login
```

```bash {.bw-run}
bronzewardctl whoami
```

```text
Context    dev (http://127.0.0.1:8080)
Identity   docs-run  service
Principal  idn_…
Roles      viewer
Epoch      ep_…  recovery mode: off
Expires    2026-12-23 12:15:19 UTC
```

The block shows an automation identity, with shortened identifiers (the dots are not output). A
person's answer shows their name and the word `human`. Read the roles first: they are what the server
will check on your next step. `whoami` shows what the server saw when you asked. It does not show
that a later command is allowed, and it does not show that the identity is the person you meant to
be until you compare the name.

**Web.** Steps:

1. Open the installation's web address. With no sign-in open in another tab of this browser, the
   page sends the browser to the identity provider by itself, with no button to press.
2. Sign in at the identity provider. The browser returns to the installation. If the sign-in
   fails, the page shows the reason and a button, **Sign in with the identity provider**, to
   start again.
3. Read the header: "Signed in as" and then "Roles:" and your roles. The name after "Signed in
   as" comes from your identity provider, never from the server; if it gives none, the header
   reads "Signed in as a person".
4. To leave, press **Sign out**. The page says "You are signed out of Bronzeward." and offers
   **Sign in**.

## Stay signed in

A person's access token is short-lived. The server refuses a human token that lives longer than
15 minutes by default, so renewing is normal and you do not have to do anything.

- **Command line.** The client renews an access token when less than a minute of its life is left,
  and before it sends a command. Two commands that run together renew one at a time, so they do
  not spend the same refresh token twice. If the identity provider gave no refresh token, the
  client says so when you sign in: "The identity provider issued no refresh token, so sign in
  again when the access token expires."
- **Web.** One browser tab renews for all the tabs of the installation, so a second tab does not
  start a second renewal.

A renewal that comes back for **another account** is not carried on silently.
The command line prints "a new sign-in: the renewed token is for X at Y, not A at B." The web page
says "The identity provider renewed the sign-in for another account. Sign in again to continue."
Read the name and sign in again on purpose. This protects you from running a command as someone you
did not mean to be.

## Refusals you can meet, and what to do

The server answers every token it does not accept with the same 401 and no reason. The clients
therefore say only what they can read themselves, and label it.

| You see | What it establishes | What to do |
|---|---|---|
| Command line: "Your sign-in expired: sign in again." | The client read the token's own expiry, which has passed. | `bronzewardctl login`. |
| Command line: "The token is from X; this context expects Y." | The token's issuer is not the one the context names. | Check the context's settings, or the server's. |
| Command line: "The token is not issued for A; the identity provider needs an audience mapping." | The token's audience is not the one the context names. | Your administrator maps the audience at the identity provider. |
| Command line: "The token lives longer than this server accepts; the identity provider's access-token lifetime must be shortened." | The token's lifetime is above the server's limit. | Your administrator shortens the lifetime at the identity provider. |
| Command line: "Not accepted by `<context>`: sign in again." Web: "Not accepted by this installation. Sign in again." | The server refused the token and the client read nothing wrong with it. | Sign in again. If it repeats, give your administrator the time and the message. |
| "Your access was revoked or is denied by this installation; contact a recovery administrator." | The installation denies this identity. | Do not sign in again: it does not restore access. The command line clears its stored sign-in. Ask a recovery administrator. |
| A line ending in `dependency-unavailable` (answer 503) | By the server's account, something it depends on did not answer: the identity provider, its database or a secret store. It says nothing about which. | Try again after a minute. If it stays, tell your administrator. |

Every command-line reading in the first rows begins "bronzewardctl's own reading of the token, not the
server's reason", and is the client's guess from the token. The server's own answer is the line
above it.

## What this page does not establish

- A successful sign-in does not show that you may do what you want next. Only `whoami`'s roles and the
  next command's own answer show that.
- A refusal that says "sign in again" does not show that signing in will help. The revoked case
  says so.
- Nothing here shows the identity provider is well; it shows the installation accepted, or refused,
  one token.

Next: [read the inventory](inventory.md).
