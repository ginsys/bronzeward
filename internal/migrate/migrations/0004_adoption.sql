-- 0004 adoption (persistence-api.md §3, §5, §7.3, §8; compilation §3.2, §5): the inventory
-- (cluster, machine and its MachineState), drafts and their import base entries, the import base
-- revision with its reference rows, the staging claim, and the operation with its events. The
-- tables of later flows (heads and their revisions, releases, plans, observations, the machine
-- timeline) are created by the migrations of the issues that write them.

-- A cluster (§3): its name, its Kubernetes API endpoint and the Talos contract minor it runs
-- (compilation §10.2). Not written through If-Match (§4.1), so its revision never leaves the
-- database and carries no ETag token.
CREATE TABLE cluster (
  id         text PRIMARY KEY CHECK (id ~ '^cl_[a-z2-7]{26}$'),
  name       text NOT NULL CHECK (btrim(name) <> '' AND octet_length(name) <= 128 AND name !~ '[[:cntrl:]]'),
  endpoint   text NOT NULL CHECK (endpoint ~ '^https://[^/?#@[:space:][:cntrl:]]+$' AND octet_length(endpoint) <= 512),
  contract   text NOT NULL CHECK (contract ~ '^v[0-9]{1,4}\.[0-9]{1,4}$'),
  revision   integer NOT NULL DEFAULT 1 CHECK (revision >= 1),
  created_at timestamptz NOT NULL
);

-- A machine (§3): its hardware evidence, its cluster, its current freeze and recovery scope state
-- (projected from their facts, execution and recovery §6, §7.4), and the counter T7 allocates its
-- scope's revisions from. The PostgreSQL uuid type compares values, so one UUID in two spellings
-- is one key; the all-zero and all-one values are SMBIOS's "not set" and "not present", a machine
-- reporting no UUID, which the PoC cannot inventory (§7.3).
CREATE TABLE machine (
  id               text PRIMARY KEY CHECK (id ~ '^mch_[a-z2-7]{26}$'),
  cluster          text NOT NULL REFERENCES cluster (id),
  smbios_uuid      uuid NOT NULL CHECK (smbios_uuid NOT IN ('00000000-0000-0000-0000-000000000000',
                     'ffffffff-ffff-ffff-ffff-ffffffffffff')),
  serial           text CHECK (serial IS NULL OR (btrim(serial) <> '' AND octet_length(serial) <= 128
                     AND serial !~ '[[:cntrl:]]')),
  frozen           boolean NOT NULL DEFAULT false,
  scope_state      text NOT NULL CHECK (scope_state IN ('normal', 'pre-restore-unaccounted', 'unresolved',
                     'blocked', 'ready', 'released')),
  revision_counter bigint NOT NULL DEFAULT 0 CHECK (revision_counter >= 0),
  created_at       timestamptz NOT NULL,
  UNIQUE (id, cluster)
);
-- §7.3's machine key: one supplied SMBIOS UUID is one record across the installation.
CREATE UNIQUE INDEX machine_smbios_uuid ON machine (smbios_uuid);

-- An import base revision (§3, §3.2; compilation §2.3 step 8, §6): the machine's sanitized
-- document, its baseline ciphertext, the baseline's keyed digest with the key identity and version
-- that computed it (compilation §4.1), and its unkeyed configuration digest (choice §16.26 there).
CREATE TABLE import_base_revision (
  id                   text PRIMARY KEY CHECK (id ~ '^ibr_[a-z2-7]{26}$'),
  machine              text NOT NULL REFERENCES machine (id),
  document             text NOT NULL CHECK (document <> ''),
  baseline_ciphertext  bytea NOT NULL CHECK (length(baseline_ciphertext) > 0),
  baseline_digest      bytea NOT NULL CHECK (length(baseline_digest) = 32),
  baseline_digest_key  text NOT NULL CHECK (btrim(baseline_digest_key) <> '' AND octet_length(baseline_digest_key) <= 256),
  configuration_digest bytea NOT NULL CHECK (length(configuration_digest) = 32),
  created_at           timestamptz NOT NULL,
  UNIQUE (id, machine)
);
CALL make_immutable('import_base_revision');

-- The import base revision's reference rows: one declaration per name (compilation §5.1, §5.2)
-- and the provider generation it resolves to, whose path is gen/<cluster>/<claim>/<value id>
-- (§6.4, choice §17.8). The orphan report reads which generations a committed row names.
CREATE TABLE import_base_reference (
  revision   text NOT NULL REFERENCES import_base_revision (id),
  name       text NOT NULL CHECK (name ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?(/[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$'
               AND octet_length(name) <= 256),
  kind       text NOT NULL CHECK (kind IN ('string', 'integer', 'boolean', 'mapping')),
  version    integer NOT NULL CHECK (version >= 1),
  encoding   text CHECK (encoding = 'base64'),
  generation text NOT NULL CHECK (generation ~ '^gen/cl_[a-z2-7]{26}/ing_[a-z2-7]{26}/[A-Za-z0-9_-]{1,128}$'),
  PRIMARY KEY (revision, name)
);
CALL make_immutable('import_base_reference');

-- A machine's Desired, Applied (release, verified configuration digest and source) and baseline
-- revision (§3; execution and recovery, Desired, Applied and Observed). The baseline revision is
-- a per-machine counter that every change of Applied advances, by a completed operation or an
-- adoption record, and a machine with no Applied has none (execution and recovery §2). The
-- release table arrives with publication, so the release identifiers carry no foreign key yet.
CREATE TABLE machine_state (
  machine           text PRIMARY KEY REFERENCES machine (id),
  revision          integer NOT NULL DEFAULT 1 CHECK (revision >= 1),
  desired           text CHECK (desired ~ '^rel_[a-z2-7]{26}$'),
  applied_release   text CHECK (applied_release ~ '^rel_[a-z2-7]{26}$'),
  applied_digest    bytea CHECK (length(applied_digest) = 32),
  applied_source    text CHECK (applied_source IN ('operation', 'adoption')),
  baseline_revision integer CHECK (baseline_revision >= 1),
  CHECK ((applied_release IS NULL) = (applied_digest IS NULL)
     AND (applied_release IS NULL) = (applied_source IS NULL)
     AND (applied_release IS NULL) = (baseline_revision IS NULL))
);

-- A draft (§3.1): one cluster's change set, revisioned, with the random token of its ETag
-- (§4.1, choice §17.2).
CREATE TABLE draft (
  id         text PRIMARY KEY CHECK (id ~ '^drf_[a-z2-7]{26}$'),
  cluster    text NOT NULL REFERENCES cluster (id),
  title      text NOT NULL CHECK (btrim(title) <> '' AND octet_length(title) <= 256 AND title !~ '[[:cntrl:]]'),
  state      text NOT NULL CHECK (state IN ('open', 'published', 'discarded')),
  revision   integer NOT NULL CHECK (revision >= 1),
  etag_token text NOT NULL CHECK (etag_token ~ '^[a-z2-7]{26}$'),
  created_at timestamptz NOT NULL,
  UNIQUE (id, cluster)
);

-- A draft's entries, set by its draft transaction (T1). An import or drift adoption carries the
-- machine's new import base revision as an entry (§3.2); the entries of fragments, profiles and
-- assignments arrive with their heads. The keys hold the entry to the draft's cluster and the
-- revision to the entry's machine.
CREATE TABLE draft_entry (
  draft                text NOT NULL,
  cluster              text NOT NULL,
  kind                 text NOT NULL CHECK (kind = 'import-base'),
  machine              text NOT NULL,
  import_base_revision text NOT NULL,
  PRIMARY KEY (draft, machine),
  FOREIGN KEY (draft, cluster) REFERENCES draft (id, cluster),
  FOREIGN KEY (machine, cluster) REFERENCES machine (id, cluster),
  FOREIGN KEY (import_base_revision, machine) REFERENCES import_base_revision (id, machine)
);

-- A staging claim (compilation §3.1, §3.2; §5.1): its mode, state and fence, with the epoch the
-- owner generation was issued in, a server-clock lease and an absolute expiry. A transient claim
-- never has a payload, and a released or abandoned one has it cleared (§3). A draft entry route's
-- claim names the request's principal and key (§7.2): of two live claims for one key, only one
-- commits.
CREATE TABLE staging_claim (
  id              text PRIMARY KEY CHECK (id ~ '^ing_[a-z2-7]{26}$'),
  mode            text NOT NULL CHECK (mode IN ('transient', 'encrypted')),
  state           text NOT NULL CHECK (state IN ('held', 'resumed', 'released', 'abandoned')),
  owner           text NOT NULL CHECK (btrim(owner) <> '' AND octet_length(owner) <= 512),
  owner_gen       bigint NOT NULL CHECK (owner_gen >= 1),
  owner_epoch     text NOT NULL REFERENCES recovery_epoch (epoch),
  lease_until     timestamptz NOT NULL,
  expires_at      timestamptz NOT NULL,
  payload         bytea,
  principal       text REFERENCES principal (id),
  idempotency_key text CHECK (idempotency_key ~ '^[A-Za-z0-9_-]{16,128}$'),
  created_at      timestamptz NOT NULL,
  CHECK (payload IS NULL OR (mode = 'encrypted' AND state IN ('held', 'resumed'))),
  CHECK ((principal IS NULL) = (idempotency_key IS NULL))
);
CREATE UNIQUE INDEX staging_claim_live_key ON staging_claim (principal, idempotency_key)
  WHERE state IN ('held', 'resumed');

-- An operation (§8): a mutable projection under a fence (§5.1). Its states are the job states for
-- publish and ingest (§8.2; an ingest is never queued) and execution and recovery's for
-- apply-config (its §4); an adopt operation is created completed. A publish or ingest operation
-- binds a draft revision and has a creator; an ingest operation is its staging claim's, one to
-- one. seq orders the publish job claim (§5.1). The columns an apply-config or adopt operation
-- adds (its plan, machine and scope) arrive with plans.
CREATE TABLE operation (
  id              text PRIMARY KEY CHECK (id ~ '^op_[a-z2-7]{26}$'),
  seq             bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
  kind            text NOT NULL CHECK (kind IN ('publish', 'ingest', 'apply-config', 'adopt')),
  state           text NOT NULL,
  -- The epoch the operation was created in (§8.3).
  epoch           text NOT NULL REFERENCES recovery_epoch (epoch),
  owner           text CHECK (btrim(owner) <> '' AND octet_length(owner) <= 512),
  owner_gen       bigint NOT NULL DEFAULT 0 CHECK (owner_gen >= 0),
  owner_epoch     text REFERENCES recovery_epoch (epoch),
  lease_until     timestamptz,
  last_event      integer NOT NULL DEFAULT 0 CHECK (last_event >= 0),
  draft           text REFERENCES draft (id),
  draft_revision  integer CHECK (draft_revision >= 1),
  ingestion       text UNIQUE REFERENCES staging_claim (id),
  created_by      text,
  created_by_kind text,
  created_role    text CHECK (created_role IN ('viewer', 'author', 'publisher', 'approver', 'recovery-admin')),
  created_at      timestamptz NOT NULL,
  result          jsonb,
  error           jsonb,
  FOREIGN KEY (created_by, created_by_kind) REFERENCES principal (id, kind),
  CHECK (CASE kind
           WHEN 'publish' THEN state IN ('queued', 'running', 'succeeded', 'failed')
           WHEN 'ingest' THEN state IN ('running', 'succeeded', 'failed')
           WHEN 'apply-config' THEN state IN ('committed', 'sending', 'verifying', 'completed', 'rejected',
             'failed', 'cancelled', 'unresolved')
           WHEN 'adopt' THEN state = 'completed'
           ELSE false
         END),
  CHECK ((owner IS NULL) = (owner_epoch IS NULL) AND (owner IS NULL OR owner_gen >= 1)),
  CHECK (state <> 'running' OR (owner IS NOT NULL AND lease_until IS NOT NULL)),
  CHECK ((draft IS NULL) = (draft_revision IS NULL) AND (kind IN ('publish', 'ingest')) = (draft IS NOT NULL)),
  CHECK ((kind = 'ingest') = (ingestion IS NOT NULL)),
  CHECK ((created_by IS NULL) = (created_by_kind IS NULL) AND (created_by IS NULL) = (created_role IS NULL)),
  CHECK (kind NOT IN ('publish', 'ingest') OR created_by IS NOT NULL),
  CHECK (kind NOT IN ('publish', 'ingest')
      OR ((result IS NOT NULL) = (state = 'succeeded') AND (error IS NOT NULL) = (state = 'failed')))
);
-- §7.3: at most one running ingest per draft revision it binds. The publish key is its own
-- index, over kind = 'publish' only, created with the publication route.
CREATE UNIQUE INDEX operation_running_ingest ON operation (draft, draft_revision)
  WHERE kind = 'ingest' AND state = 'running';

-- A publish or ingest operation's timeline entries (§3 TimelineEvent, §8.3), numbered from 1 under
-- the operation's row lock (T7), each with the epoch it was appended in.
CREATE TABLE operation_event (
  operation text NOT NULL REFERENCES operation (id),
  number    integer NOT NULL CHECK (number >= 1),
  epoch     text NOT NULL REFERENCES recovery_epoch (epoch),
  entry     jsonb NOT NULL,
  at        timestamptz NOT NULL,
  PRIMARY KEY (operation, number)
);
CALL make_immutable('operation_event');

-- 0003 deferred this reference until the table existed. No committed record names an operation
-- before this migration, so the key validates.
ALTER TABLE idempotency_record ADD FOREIGN KEY (operation_id) REFERENCES operation (id);
