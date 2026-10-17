# Operator guide

How to build, configure and start the Bronzeward proof of concept, and where to go next. The
[walkthrough](walkthrough.md) then takes one existing cluster through adoption, publication,
approval, a safe apply and drift handling.

This is a proof of concept under implementation. Nothing here is a release or a production
deployment guide: the supported scope is
[design §18.2](../design/Talos_Configuration_and_Machine_Management_Design.md#182-phase-1---configuration-control-on-an-existing-cluster),
and the [acceptance plan](../spec/acceptance-plan.md) is what has been checked.

## The profile

The PoC supports one deployment profile
([design §7.7](../design/Talos_Configuration_and_Machine_Management_Design.md#77-poc-deployment-profile)):

- PostgreSQL as the database (evidence on 17.11), one server;
- OpenBao KV v2 and Transit as the secret and artifact-encryption provider (evidence on 2.6.1),
  one node, one unseal share;
- Talos clusters reached directly through each node's Talos API (design baseline v1.13.6).

No other database, provider or topology is supported. Replication, failover, auto-unseal and
several Bronzeward instances behind one address are unmeasured. You run, secure and back up
PostgreSQL, OpenBao and the store holding Bronzeward's provider tokens yourself (design §7.7,
"Operator responsibilities").

## Two ways to run it

- **The disposable fixture** ([`fixtures/`](../../fixtures/README.md)): a two-node Talos cluster,
  PostgreSQL, OpenBao, an OIDC issuer with synthetic humans and three server instances, all in
  Docker on one Linux host. Every secret is synthetic. This is the environment the walkthrough is
  written for, and the one a reviewer should use.
- **Your own deployment**: the same binary and configuration file against your own services. Each
  walkthrough step ends with a note on what differs.

## Build

Prerequisites: Go at the version `mise.toml` pins, and [mise](https://mise.jdx.dev/) (or run the
`go build` lines below yourself). The fixture needs more; see
[its prerequisites](../../fixtures/README.md#prerequisites).

```sh
mise run build        # bin/bronzeward and bin/oidc-fixture
```

`bin/bronzeward` is the server and its operator commands: `serve`, `migrate`, `token` and
`orphans`. Each takes `-config <file>`.

## Configure

Start from [`examples/bronzeward.yaml`](../../examples/bronzeward.yaml), whose comments name each
setting and its contract. The blocks:

| Block | What you set |
|---|---|
| `listen` | The address to serve on. Plain HTTP: terminate TLS in front of it. |
| `database.dsn` | Your PostgreSQL DSN, with credentials. |
| `auth.oidc` | Your identity provider's issuer, the audience, the groups claim, the longest token lifetime accepted (default 15 minutes). |
| `auth.roles` | Which groups grant `viewer`, `author`, `publisher`, `approver` and `recovery-admin`. |
| `auth.deniedSubjects` | Subjects refused from configuration, which survives a database restore; list a subject here before revoking it (persistence API §10.4). |
| `execution` | `maxTransportDeadline` (required), and the defaults a plan binds when its request leaves a value out. |
| `provider` | OpenBao's address, the four Transit key names and one token file per identity. Without it, ingestion and publication answer `503`. |
| `ingestion` | Required with `provider`: this instance's name and the claim and job timers. |

OpenBao needs KV v2 at `secret/`, Transit, the four Transit keys, and one token per identity
under its own policy. The fixture's policies are the reference:
[`fixtures/openbao/policies`](../../fixtures/openbao/policies), described in
[OpenBao keys and policies](../../fixtures/README.md#openbao-keys-and-policies). Each token file is
mode 0600 and named by an absolute path.

Each cluster's Talos client configuration is a provider secret you write, never a Bronzeward
request (persistence API §3.3):

```sh
bao kv put -mount=secret access/talos/<cluster id> talosconfig=@talosconfig
```

`<cluster id>` is the `cl_...` identifier Bronzeward returns when the cluster is recorded
(walkthrough, S1).

## Migrate, then serve

```sh
bin/bronzeward migrate -config bronzeward.yaml
bin/bronzeward serve -config bronzeward.yaml
curl -i http://127.0.0.1:8080/livez           # 204 once serving
```

`migrate` is the only way the schema changes. Run it with every server stopped. It applies the
binary's migrations, records the installation epoch on first use, and prints
`the schema is at version N, exactly this binary's migrations`. `serve` refuses a schema that is
not exactly its own.

`serve -recovery-start` is only for starting after a database or provider restore. It is
covered with recovery, not here.

## Identities and roles

**Humans** authenticate with a JWT from your identity provider, sent as
`Authorization: Bearer <token>` on every request. Their roles come from the groups claim through
`auth.roles`. `approver` and `recovery-admin` are held only by humans.

**Automation** uses a token Bronzeward issues. The token tool talks to the database directly, so
run it on the server host:

```sh
bin/bronzeward token issue -config bronzeward.yaml -name ci -roles author,publisher \
  -responsible <human subject> -operator <your subject>
bin/bronzeward token list -config bronzeward.yaml
bin/bronzeward token rotate -config bronzeward.yaml -identity <idn_...> -operator <your subject>
bin/bronzeward token revoke -config bronzeward.yaml -identity <idn_...> -operator <your subject>
```

`issue` and `rotate` print the token once, on stdout. Roles are `viewer`, `author` and
`publisher`. A token lives 30 days unless `-expires` says otherwise, at most 2160h.

## Start the fixture

```sh
fixtures/bin/up            # about two minutes once images are cached
fixtures/bin/inject start A
fixtures/bin/inject start B
fixtures/bin/inject start C
fixtures/bin/bw migrate
fixtures/bin/seed          # the automation identity; its token goes to fixtures/.state/automation-token
curl -i http://127.0.0.1:28080/livez
```

What `up` makes, from [`fixtures/versions.env`](../../fixtures/versions.env):

| Service | Host address |
|---|---|
| Instance A (the one the walkthrough calls) | `http://127.0.0.1:28080` |
| Instance B | `http://127.0.0.1:28081` |
| Instance C (the interruption build) | `http://127.0.0.1:28082` |
| PostgreSQL | `127.0.0.1:25432` |
| OpenBao | `http://127.0.0.1:28200` |
| Talos control plane, worker | `10.55.0.2`, `10.55.0.3` (on the Docker network) |

The OIDC issuer is not published. Tokens are minted offline from its key with the image's `oidc`
binary, as the walkthrough shows. Its synthetic humans are `h-author`, `h-publisher`,
`h-approver`, `h-recovery` and `h-viewer`, each holding the role it names, and `h-all`, holding
`author`, `publisher` and `approver`. A minted token lives five minutes.

`fixtures/bin/down` removes everything, including `fixtures/.state/`. Copy out what you want to
keep first.

## Next

The [walkthrough](walkthrough.md): adopt the fixture's cluster, publish, approve, apply and handle
drift.
