# Upgrade and rollback

**Upgrade the server first, then the clients.** Roll the server back only by restoring a database
backup.

## Why this order

Within `/api/v1` a newer server keeps serving older clients. Fields and routes are only added, and a
route due for removal carries a `Deprecation` header for at least one minor release first
([client contracts §7](../../spec/client-contracts.md#7-compatibility-and-versioning)). The
reverse is not safe. A client declares the lowest API revision it needs and reads `GET /server`
before any write. Against an older server it refuses with both revisions named, and sends nothing
(case COMP-01 of [§12](../../spec/client-contracts.md#12-shared-conformance-cases)).
`bronzewardctl version` and the web footer show the client's version and the server's, with its API
revision.

## Upgrade the server

Migrations change the schema, only through `migrate`, with every server stopped
([persistence and API §11](../../spec/persistence-api.md#11-migrations)). `serve` refuses a schema
that is not exactly its own binary's, so no instance runs across a migration.

1. Take a backup of the database and the OpenBao data that you can restore. It is the only way
   back (below).
2. Stop every instance. `migrate` must run with the service stopped (§11 rule 1).
3. Run the new binary's `bronzeward migrate -config bronzeward.yaml`. It prints
   `the schema is at version N, exactly this binary's migrations`.
4. Start every instance on the new binary.
5. Upgrade the CLI and the web files ([hosting](hosting.md)). Render the `Caddyfile` again from the
   new `headers.json`, or take the release's.

**Between pre-releases** (`v0.N.0-alpha.M`, `-beta.M`, `-rc.M`), the schema is one migration that
is edited in place, and nothing upgrades a database made by an earlier version of it
([§11](../../spec/persistence-api.md#11-migrations) item 6). If `migrate` refuses because the
database records the migration with another name or checksum, that pre-release changed the schema. The database has to be
created again, and its contents are lost. Pre-releases are for evaluation, not for data you keep.

## Roll back

There is no downgrade ([§11](../../spec/persistence-api.md#11-migrations) item 5). An older binary
refuses a schema that a newer one migrated, so going back across a migration means restoring the
backup of step 1:

1. Stop every instance.
2. Restore the database, and OpenBao with it if the backup holds both, as
   [Recovery: restoring from a backup](../recovery.md#restoring-from-a-backup) describes.
3. Start one instance of the older binary with `serve -recovery-start`, and follow that page to the
   end. A restore always enters recovery mode. Everything after the backup is lost, and each
   machine is accounted before normal service resumes.

Bronzeward adds no migration tooling. A client rolls back by installing the older client, as long
as the server still serves the API revision it needs.
