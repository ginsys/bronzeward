# Journey: find out why a client cannot use the server

**You want** to know why `bronzewardctl` or the web client cannot talk to the server, or warns
about it. This page covers five cases: the server cannot be reached, its certificate is not
trusted, it is too old for this client, it marks a route as deprecated, and it answers with a
redirect. Words such as context and API revision are in [the word list](terms.md). What to do when
the server itself is the problem is in the [server troubleshooting guide](../troubleshooting.md).

**You need** no role: none of these cases reaches the point where a role is checked. The `text`
blocks show the sentences as the source builds them. This page does not claim that a run printed
them, except one: a browser run on the participant fixture showed the web sentence "The server
could not be reached", in the screenshot in the web section.

**Nothing this page asks you to run changes a machine or the installation.** A command that stops in any of
these cases sends nothing more, and a redirect or a certificate refusal stops it before the
request is repeated.

## Command line

Run any read, for example:

```bash
bronzewardctl status
```

`context add` also checks the server when you create a context: it sends `GET /livez`, which
carries no credential, and fails for the cases of an unreachable server, an untrusted certificate
and a redirect. It checks neither the API revision nor deprecation. Then find the case in the table. `<context>`
stands for the context's description, which the notices below begin with.

| What you see | Case |
|---|---|
| A retry line ending "connection refused", "no such host", "i/o timeout" or similar | [Server cannot be reached](#the-server-cannot-be-reached) |
| "the certificate of <host> (subject ..., issuer ...) is not trusted by ...; nothing was sent" | [Certificate not trusted](#the-certificate-is-not-trusted) |
| "the server serves API revision <n>; this bronzewardctl needs revision <m> or later" | [Server too old](#the-server-is-too-old-for-this-client) |
| "<route> is deprecated (Deprecation: <value>)" | [Deprecated route](#a-route-is-deprecated) |
| "... redirecting to \"<address>\" ..." | [Redirect](#the-server-redirects) |

### The server cannot be reached

A read that fails at the network is repeated: after 1 second, then doubling up to 30 seconds, at
most 5 tries. Each failed try that is followed by another prints a line:

```text
<context>: GET /api/v1/<path>: <cause>; try 2 of 5 in 1s
```

The cause is the network error as the system reports it, for example one that ends "connection
refused", "no such host" or "i/o timeout". The command's own words for these causes ("the
connection was refused", "the name <host> could not be resolved", "it timed out", "the connection
closed before an answer", "the connection was reset") are printed only for the identity provider
and the client-configuration host. A retry line says only that one try failed. If a later try
answers, the command gives its normal result: read the last answer and the exit status, not the
retry lines.

- Refused or reset: nothing listens at that address and port, or something in between refused it.
  Check the address in the context with `context list` (see [contexts](contexts.md)).
- No such host: the host name is wrong, or your resolver cannot find it.
- Timed out: a firewall or a proxy drops the traffic. `-v` names the proxy a request goes through.
- Closed before an answer: the connection started and was cut. For a write, exit 3 means the
  outcome is unknown: read before you act again (see the
  [reference](cli-reference.md#global-flags-and-exit-codes)).

Reaching a host that is not a Bronzeward server gives a different sentence: "GET /livez answered
<status>, not 204: this is not a Bronzeward server, or not its base URL". Use the server's base address,
without `/livez` or `/api/v1...`.

### The certificate is not trusted

```text
the certificate of <host> (subject "<subject>", issuer "<issuer>") is not trusted by <source>: <reason>; nothing was sent
```

`<source>` is "the system trust store", or "the bundle <file>" where the context was given a
certificate authority file. The client never skips the check, and no flag does. The fix depends on
the reason:

| Reason | Fix |
|---|---|
| "x509: certificate signed by unknown authority" | Give the client the PEM bundle of the authority that signed the server's certificate, with `--certificate-authority` (see [TLS trust in contexts](contexts.md#tls-trust)). The bundle replaces the system store for this server, so it must hold every authority this server needs. |
| "x509: certificate has expired or is not yet valid" | A bundle does not help. The server's operator renews the certificate, or you check this computer's clock. |
| A host name message, which lists the names the certificate holds | A bundle does not help. Use an address the certificate lists in the context, or the server's operator issues a certificate for the name you use. |
| "x509: certificate is not valid" | The certificate fails for another reason. Ask the server's operator; a bundle fixes only an unknown authority. |

The identity provider has its own flag, `--oidc-certificate-authority`.

### The server is too old for this client

```text
the server serves API revision <n>; this bronzewardctl needs revision <m> or later
```

The command stops with this sentence: the server's revision is below the lowest this client
accepts. A newer `bronzewardctl` does not help, since it needs the same revision or a higher one.
Ask the server's operator to upgrade the server, or use an older `bronzewardctl` that accepts the
revision the server serves. `machine timeline --follow` has its own sentence for a server below the
revision that serves it: "the server serves API revision <n>; machine timeline --follow needs
revision <m> or later".

### A route is deprecated

```text
<context>: <route> is deprecated (Deprecation: <value>)
```

This is a notice, not an outcome: the client prints it when an answer arrives, before it checks
whether the answer succeeded, so a command that printed it can still have failed. Read the
command's result and exit status. The server marks the route as one it will remove. The client
prints the notice once per route in a run, so a long list warns once. Use a newer `bronzewardctl`,
which no longer calls that route, and tell the server's operator if the notice stays.

### The server redirects

The client follows no redirect. It stops and says where the server sent it:

```text
GET /api/v1/<path> answered <status> redirecting to "<address>"; bronzewardctl follows no redirect, and nothing was resent
GET /livez answered <status> redirecting to "<address>", which bronzewardctl does not follow
```

A write says "..., which bronzewardctl does not follow; it is not repeated". The usual cause is a
context whose address is the old one, or `http` where the server wants `https`. The address in the
message is the server's `Location` header, as sent: a full address or only a path, and the place
one route moved to, not the server's base address. Work the base address out from it: scheme and
host, plus the path the server is mounted at, without the route's own part (`/livez` or
`/api/v1...`). A header with no host does not give the host: ask the server's operator. Then remove
the context and add it again with the base address (see [contexts](contexts.md)).

## Web client

The page itself has three of the five cases. A server below the client's minimum shows "This server
serves API revision <n>; this client needs API revision <m> or later." A route the server marks as
deprecated shows "The server marks <route> as deprecated: it will be removed in a later release."
once per page load. A request that fails at the network, or that the server answers with a redirect
(which the page refuses), shows one sentence for both: "The server could not be reached. The
network may be down, or the server answered with a redirect, which this page refuses." A write says
"the connection failed. The network may be down, or the server answered with a redirect, which this
page refuses" and its outcome is unknown. The page cannot tell the two causes apart. The footer
shows "Server <version>, API revision <n>" while the server answers, and "Server unknown" when it
does not.

![The web client after the server stopped answering. The header shows the name Bronzeward and a
**Sign out** button, with no navigation. A notice reads "The server could not be reached. The
network may be down, or the server answered with a redirect, which this page refuses." The rest of
the page is empty and the footer reads "Client (devel). Server unknown". Taken on the participant
fixture at revision 454b5609, with the server stopped on purpose.](images/client-troubleshooting-1.png)

This screenshot shows the one sentence for a network failure. It does not show the too-old or the
deprecated-route sentence: neither was run in a browser.

If the page itself does not load, the browser shows its own error page, and the certificate of the
page is the browser's to judge. For a cause the page cannot name, run a command such as `status` from
the same computer: it names the host, the certificate and the cause.

## What the result does and does not show

- A refusal that says "nothing was sent" shows that nothing reached the server. A retry line shows
  only that one try failed; the command's last answer and exit status show the outcome.
- "Not trusted" shows that this client, with this bundle, does not trust that certificate. It does
  not show that the certificate is wrong, or that the server is not the one you wanted.
- None of these sentences shows the state of the installation. Read that with `status` once the
  client is connected.

Next: a connected client that is refused for a role or a sign-in is in [sign-in and
renewal](sign-in-and-renewal.md). Faults on the server side are in the [server troubleshooting
guide](../troubleshooting.md).
