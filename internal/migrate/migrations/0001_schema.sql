-- 0001 schema (persistence-api.md §3, §11). Bronzeward is unreleased: until a first release the
-- schema is this one migration, edited in place, and a database migrated from an earlier version of
-- it is refused at startup and recreated (§11 rules 2 and 6). Tables are created in dependency
-- order; the one cycle, a draft and the release that published it, is closed by the ALTER after
-- the release table.

-- An immutable table refuses UPDATE, DELETE and TRUNCATE (§3, choice §17.3), with SQLSTATE BW001
-- so a test can tell this refusal from any other error.
CREATE FUNCTION refuse_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'table % is immutable: % refused', TG_TABLE_NAME, TG_OP USING ERRCODE = 'BW001';
END
$$;

-- Every immutable table is made so by this call, after the statement that creates it.
CREATE PROCEDURE make_immutable(t regclass) LANGUAGE plpgsql AS $$
BEGIN
  EXECUTE format('CREATE TRIGGER immutable_rows BEFORE UPDATE OR DELETE ON %s FOR EACH ROW EXECUTE FUNCTION refuse_mutation()', t);
  EXECUTE format('CREATE TRIGGER immutable_truncate BEFORE TRUNCATE ON %s FOR EACH STATEMENT EXECUTE FUNCTION refuse_mutation()', t);
END
$$;

CALL make_immutable('schema_migrations');

-- Authentication (§10) -----------------------------------------------------------------------

-- A human is (iss, sub), created on first admitted use (§10); a service identity has a name and a
-- responsible human (§10.2, choice §17.18) and is created by the token tool. No row stores roles:
-- a human's come from each token, a service identity's from its token row. revoked is set once,
-- by identity revocation (§10.4, T5c).
CREATE TABLE principal (
  id          text PRIMARY KEY CHECK (id ~ '^idn_[a-z2-7]{26}$'),
  kind        text NOT NULL CHECK (kind IN ('human', 'service')),
  iss         text,
  sub         text,
  name        text,
  responsible text,
  -- The responsible principal must be a human: the key below names its kind, as act's does.
  responsible_kind text GENERATED ALWAYS AS (CASE WHEN responsible IS NOT NULL THEN 'human' END) STORED,
  created_at  timestamptz NOT NULL,
  revoked     boolean NOT NULL DEFAULT false,
  UNIQUE (id, kind),
  FOREIGN KEY (responsible, responsible_kind) REFERENCES principal (id, kind),
  -- IS NOT NULL is spelled out: a CHECK passes when its expression is NULL.
  CHECK ((kind = 'human' AND iss IS NOT NULL AND sub IS NOT NULL AND iss <> '' AND sub <> ''
          AND name IS NULL AND responsible IS NULL)
      OR (kind = 'service' AND iss IS NULL AND sub IS NULL AND name IS NOT NULL AND name <> ''
          AND responsible IS NOT NULL))
);
-- The arbiter of §10's INSERT ... ON CONFLICT (iss, sub) DO NOTHING. Service rows have NULL
-- iss and sub, which never conflict.
CREATE UNIQUE INDEX principal_human ON principal (iss, sub);
CREATE UNIQUE INDEX principal_service_name ON principal (name);

-- Installation (§11, §12.1) ------------------------------------------------------------------

-- One row per epoch (§12.1): the installation's, then one per recovery-mode entry. entered_by
-- is NULL for the installation epoch. Entry's restored-backup rows arrive with recovery mode.
CREATE TABLE recovery_epoch (
  epoch      text PRIMARY KEY CHECK (epoch ~ '^ep_[a-z2-7]{26}$'),
  entered_at timestamptz NOT NULL,
  entered_by text REFERENCES principal (id)
);
CALL make_immutable('recovery_epoch');

-- The single installation row (§12.1): the current epoch, recovery mode, the schema version.
CREATE TABLE installation_state (
  singleton      boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  epoch          text NOT NULL REFERENCES recovery_epoch (epoch),
  recovery_mode  boolean NOT NULL DEFAULT false,
  schema_version integer NOT NULL
);

-- Only SHA-256 of the secret is stored (§10.2). Automation never holds approver or
-- recovery-admin (design §13.7 item 2). Expiry is at most 90 days: 2160 hours, not '90 days',
-- so a DST change cannot make the bound differ from the tool's.
CREATE TABLE automation_token (
  id            text PRIMARY KEY CHECK (id ~ '^tok_[a-z2-7]{26}$'),
  -- Issuance order. Tokens are inserted under the principal lock, so seq follows the order in
  -- which an identity's tokens replaced each other; issued_at (a transaction's start) need not.
  seq           bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
  owner         text NOT NULL,
  -- Only a service identity holds a token: the key below names its kind.
  owner_kind    text NOT NULL GENERATED ALWAYS AS ('service') STORED,
  secret_sha256 bytea NOT NULL CHECK (length(secret_sha256) = 32),
  roles         text[] NOT NULL CHECK (cardinality(roles) > 0
                  AND roles <@ ARRAY['viewer', 'author', 'publisher']::text[]),
  epoch         text NOT NULL REFERENCES recovery_epoch (epoch),
  issued_at     timestamptz NOT NULL,
  expires_at    timestamptz NOT NULL CHECK (expires_at > issued_at
                  AND expires_at <= issued_at + interval '2160 hours'),
  -- No CHECK against issued_at: now() is the transaction's start, and a revocation that began
  -- before a rotation it then waited on writes a revoked_at earlier than that token's issued_at.
  revoked_at    timestamptz,
  FOREIGN KEY (owner, owner_kind) REFERENCES principal (id, kind)
);
-- One valid token per identity: at most one unrevoked, expired or not. It backs the principal
-- row lock every issuing transaction takes (§10.2).
CREATE UNIQUE INDEX automation_token_one_unrevoked ON automation_token (owner) WHERE revoked_at IS NULL;

-- Every mutating act (§10.5). via 'api' is a request, with its role and request id; via 'tool'
-- is the operator's token command, which exercises no API role (§10.2). seq orders acts inside
-- one database state and never leaves it (§2).
CREATE TABLE act (
  id              text PRIMARY KEY CHECK (id ~ '^act_[a-z2-7]{26}$'),
  seq             bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
  principal       text NOT NULL,
  principal_kind  text NOT NULL,
  via             text NOT NULL CHECK (via IN ('api', 'tool')),
  role            text CHECK (role IN ('viewer', 'author', 'publisher', 'approver', 'recovery-admin')),
  action          text NOT NULL CHECK (action <> ''),
  subjects        text[] NOT NULL,
  idempotency_key text,
  request_id      text CHECK (request_id ~ '^req_[a-z2-7]{26}$'),
  epoch           text NOT NULL REFERENCES recovery_epoch (epoch),
  at              timestamptz NOT NULL,
  FOREIGN KEY (principal, principal_kind) REFERENCES principal (id, kind),
  CHECK ((via = 'api' AND role IS NOT NULL AND request_id IS NOT NULL)
      OR (via = 'tool' AND role IS NULL AND request_id IS NULL AND idempotency_key IS NULL))
);
CALL make_immutable('act');

-- Inventory (§3, §3.3, §7.3; execution and recovery choice §10.26) ----------------------------

-- An endpoint as ParseEndpoint (internal/talos) returns it: an IPv4 literal or a bracketed IPv6
-- literal, then a port from 1 to 65535. The application holds the grammar; this is the shape a
-- writer that bypassed it cannot get past (no DNS name, scheme, path, user part or whitespace).
CREATE DOMAIN talos_endpoint AS text
  CONSTRAINT talos_endpoint_form CHECK (
    octet_length(VALUE) <= 64
    AND VALUE ~ '^([0-9]{1,3}(\.[0-9]{1,3}){3}|\[[0-9a-f:.]+\]):[1-9][0-9]{0,4}$'
    AND substring(VALUE FROM ':([0-9]+)$')::integer <= 65535);

-- A cluster (§3): its name, its Kubernetes API endpoint, the Talos contract minor it runs
-- (compilation §10.2) and its Talos cluster ID. Not written through If-Match (§4.1), so its
-- revision never leaves the database and carries no ETag token. The Talos cluster ID is as
-- `talosctl get info` prints it: the URL-safe, padded base64 encoding of 32 bytes, in its one
-- canonical spelling, compared byte for byte.
CREATE TABLE cluster (
  id         text PRIMARY KEY CHECK (id ~ '^cl_[a-z2-7]{26}$'),
  name       text NOT NULL CHECK (btrim(name) <> '' AND octet_length(name) <= 128 AND name !~ '[[:cntrl:]]'),
  endpoint   text NOT NULL CHECK (endpoint ~ '^https://[^/?#@[:space:][:cntrl:]]+$' AND octet_length(endpoint) <= 512),
  contract   text NOT NULL CHECK (contract ~ '^v[0-9]{1,4}\.[0-9]{1,4}$'),
  revision   integer NOT NULL DEFAULT 1 CHECK (revision >= 1),
  created_at timestamptz NOT NULL,
  talos_cluster_id text NOT NULL CHECK (talos_cluster_id ~ '^[A-Za-z0-9_-]{42}[AEIMQUYcgkosw048]=$')
);
-- §7.3's cluster key: one Talos cluster ID is one record across the installation.
CREATE UNIQUE INDEX cluster_talos_cluster_id ON cluster (talos_cluster_id);

-- A machine (§3): its cluster, its identity, its Talos endpoint (§3.3, choice §17.29), its current
-- freeze and recovery scope state (projected from their facts, execution and recovery §6, §7.4),
-- and the counter T7 allocates its scope's revisions from. A machine is keyed by its SMBIOS UUID
-- when it reports one and otherwise by its Talos node ID, exactly one of them, fixed at inventory.
-- The PostgreSQL uuid type compares values, so one UUID in two spellings is one key. SMBIOS's nil
-- and all-ones values are accepted as a UUID (§7.3): each can be recorded once, and a second
-- machine reporting it is recorded by node ID and refused at every comparison. The Talos node ID
-- is as `talosctl get identity` prints it: an opaque string of printable ASCII without spaces,
-- compared byte for byte, never normalised. Every machine has an endpoint, with no default, and
-- the platform mode its configuration validates in (compilation §6 step 8), with no default.
CREATE TABLE machine (
  id               text PRIMARY KEY CHECK (id ~ '^mch_[a-z2-7]{26}$'),
  cluster          text NOT NULL REFERENCES cluster (id),
  smbios_uuid      uuid,
  serial           text CHECK (serial IS NULL OR (btrim(serial) <> '' AND octet_length(serial) <= 128
                     AND serial !~ '[[:cntrl:]]')),
  frozen           boolean NOT NULL DEFAULT false,
  scope_state      text NOT NULL CHECK (scope_state IN ('normal', 'pre-restore-unaccounted', 'unresolved',
                     'blocked', 'ready', 'released')),
  revision_counter bigint NOT NULL DEFAULT 0 CHECK (revision_counter >= 0),
  created_at       timestamptz NOT NULL,
  talos_endpoint   talos_endpoint NOT NULL,
  talos_node_id    text CHECK (talos_node_id ~ '^[!-~]{1,128}$'),
  platform         text NOT NULL CONSTRAINT machine_platform CHECK (platform IN ('metal', 'container', 'cloud')),
  UNIQUE (id, cluster),
  UNIQUE (id, platform),
  CONSTRAINT machine_identity_key CHECK ((smbios_uuid IS NULL) <> (talos_node_id IS NULL))
);
-- §7.3's machine keys: one SMBIOS UUID, and one Talos node ID, is one record across the
-- installation.
CREATE UNIQUE INDEX machine_smbios_uuid ON machine (smbios_uuid);
CREATE UNIQUE INDEX machine_talos_node_id ON machine (talos_node_id);

-- A revision's writer: the transaction that wrote it, set here whatever the INSERT supplies.
-- pg_current_xact_id() is the top-level transaction's full ID, also inside a savepoint, and is
-- never reused, unlike a row's 32-bit xmin. make_immutable keeps it from changing.
CREATE FUNCTION stamp_revision_writer() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  NEW.writer := pg_current_xact_id();
  RETURN NEW;
END
$$;

-- A revision's rows are written in the transaction that writes the revision (§3): make_immutable
-- refuses UPDATE and DELETE, and this refuses an INSERT that would add to a committed revision,
-- with the same SQLSTATE. TG_ARGV[0] names the revision table; the row names it in `revision`. A
-- revision this transaction cannot see is refused here as the foreign key would, and at once: the
-- foreign key runs at the end of the statement and would accept one committed meanwhile.
CREATE FUNCTION refuse_late_revision_row() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  w xid8;
BEGIN
  EXECUTE format('SELECT writer FROM %I WHERE id = $1', TG_ARGV[0]) INTO w USING NEW.revision;
  IF w IS NULL THEN
    RAISE EXCEPTION 'table %: no such revision', TG_TABLE_NAME USING ERRCODE = 'foreign_key_violation';
  ELSIF w <> pg_current_xact_id() THEN
    RAISE EXCEPTION 'table %: a row of a committed revision is refused', TG_TABLE_NAME USING ERRCODE = 'BW001';
  END IF;
  RETURN NEW;
END
$$;

-- An import base revision (§3, §3.2; compilation §2.3 step 8, §6): the machine's sanitized
-- document, its baseline ciphertext, the baseline's keyed digest with the key identity and version
-- that computed it (compilation §4.1), and its unkeyed configuration digest (choice §16.26 there).
-- embedded holds the embedded documents the import identified (compilation §5.2), as a fragment
-- revision's does, so publication rebuilds the stored document with the authoring checks. Its
-- author is the human whose ingestion wrote it, whom an approval's self-approval mark names like a
-- fragment revision's author (execution and recovery §2; §10.5).
CREATE TABLE import_base_revision (
  id                   text PRIMARY KEY CHECK (id ~ '^ibr_[a-z2-7]{26}$'),
  machine              text NOT NULL REFERENCES machine (id),
  document             text NOT NULL CHECK (document <> ''),
  embedded             jsonb NOT NULL CHECK (jsonb_typeof(embedded) = 'array'),
  baseline_ciphertext  bytea NOT NULL CHECK (length(baseline_ciphertext) > 0),
  baseline_digest      bytea NOT NULL CHECK (length(baseline_digest) = 32),
  baseline_digest_key  text NOT NULL CHECK (btrim(baseline_digest_key) <> '' AND octet_length(baseline_digest_key) <= 256),
  configuration_digest bytea NOT NULL CHECK (length(configuration_digest) = 32),
  created_at           timestamptz NOT NULL,
  writer               xid8 NOT NULL,
  author               text NOT NULL REFERENCES principal (id),
  UNIQUE (id, machine)
);
CALL make_immutable('import_base_revision');
CREATE TRIGGER writer BEFORE INSERT ON import_base_revision FOR EACH ROW EXECUTE FUNCTION stamp_revision_writer();

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
  PRIMARY KEY (revision, name),
  -- base64 places a string secret's UTF-8 bytes (compilation §5.2); no other kind takes it.
  CHECK (encoding IS NULL OR kind = 'string')
);
CALL make_immutable('import_base_reference');
CREATE TRIGGER with_revision BEFORE INSERT ON import_base_reference
  FOR EACH ROW EXECUTE FUNCTION refuse_late_revision_row('import_base_revision');

-- Drafts and ingestion (§3.1, §5, §7.2, §7.3; compilation §2.3, §3) ---------------------------

-- A draft (§3.1): one cluster's change set, revisioned, with the random token of its ETag
-- (§4.1, choice §17.2). A draft names the release that published it, and only then (§6.2); that
-- key is added after the release table.
CREATE TABLE draft (
  id         text PRIMARY KEY CHECK (id ~ '^drf_[a-z2-7]{26}$'),
  cluster    text NOT NULL REFERENCES cluster (id),
  title      text NOT NULL CHECK (btrim(title) <> '' AND octet_length(title) <= 256 AND title !~ '[[:cntrl:]]'),
  state      text NOT NULL CHECK (state IN ('open', 'published', 'discarded')),
  revision   integer NOT NULL CHECK (revision >= 1),
  etag_token text NOT NULL CHECK (etag_token ~ '^[a-z2-7]{26}$'),
  created_at timestamptz NOT NULL,
  release    text,
  UNIQUE (id, cluster),
  CONSTRAINT draft_published_release CHECK ((state = 'published') = (release IS NOT NULL))
);

-- A draft's import base entries, set by its draft transaction (T1). An import or drift adoption
-- carries the machine's new import base revision as an entry (§3.2); fragment, profile and
-- assignment entries are draft_source_entry's. The keys hold the entry to the draft's cluster and
-- the revision to the entry's machine.
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
-- commits. Its subject (compilation §2.3 step 6, §3.1, §3.4) is the cluster its provider
-- generations are created under (gen/<cluster>/<claim id>/<value id>), and either the machine an
-- import imports or the draft a draft update (a fragment PUT, §9.3) updates. payload_digest is the
-- SHA-256 of the envelope's plaintext, which a resume checks its decrypted envelope against.
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
  cluster         text NOT NULL REFERENCES cluster (id),
  machine         text,
  -- Drift adoption (execution and recovery §6) arrives with its own issue.
  kind            text NOT NULL CONSTRAINT staging_claim_kind CHECK (kind IN ('import', 'draft-update')),
  payload_digest  bytea CHECK (octet_length(payload_digest) = 32),
  draft           text,
  CHECK (payload IS NULL OR (mode = 'encrypted' AND state IN ('held', 'resumed'))),
  -- Only an encrypted claim can be taken over (§3.2, §3.4): transient staging has no recovery owner.
  CHECK (state <> 'resumed' OR mode = 'encrypted'),
  CHECK ((principal IS NULL) = (idempotency_key IS NULL)),
  CHECK ((payload IS NULL) = (payload_digest IS NULL)),
  FOREIGN KEY (machine, cluster) REFERENCES machine (id, cluster),
  FOREIGN KEY (draft, cluster) REFERENCES draft (id, cluster),
  -- An import names its machine, a draft update its draft; neither names the other.
  CONSTRAINT staging_claim_subject CHECK ((kind = 'import') = (machine IS NOT NULL)
    AND (kind = 'draft-update') = (draft IS NOT NULL)),
  -- A draft update answers its request: no review outlives its process (compilation §3.1).
  CONSTRAINT staging_claim_draft_update_transient CHECK (kind <> 'draft-update' OR mode = 'transient')
);
CREATE UNIQUE INDEX staging_claim_live_key ON staging_claim (principal, idempotency_key)
  WHERE state IN ('held', 'resumed');
-- The sweep's scan (compilation §3.5): the live claims, by the time they become due.
CREATE INDEX staging_claim_live_expiry ON staging_claim (expires_at) WHERE state IN ('held', 'resumed');

-- Operations (§8; execution-recovery.md §3.2, §3.4) --------------------------------------------

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
      OR ((result IS NOT NULL) = (state = 'succeeded') AND (error IS NOT NULL) = (state = 'failed'))),
  -- An outcome is a JSON object (a result, a problem document), never JSON null, which is not SQL
  -- NULL and would pass the check above with nothing to answer.
  CHECK ((result IS NULL OR jsonb_typeof(result) = 'object') AND (error IS NULL OR jsonb_typeof(error) = 'object')),
  -- execution-recovery §3.2 creates an apply-config operation in `committed` owned by the
  -- committing instance at generation 1, and §3.4's takeover and attempt comparisons read that
  -- owner until the operation is terminal. The pairing check above makes an owner carry its epoch
  -- and a generation of at least 1. No lease: no timer takes an apply-config operation over
  -- (choice §10.7).
  CONSTRAINT operation_apply_config_owned
    CHECK (kind <> 'apply-config' OR state NOT IN ('committed', 'sending', 'verifying', 'unresolved')
      OR owner IS NOT NULL),
  -- The key operation events name their operation's kind by.
  CONSTRAINT operation_id_kind UNIQUE (id, kind),
  -- The key a release names its publish operation by (§6.2).
  CONSTRAINT operation_publication UNIQUE (id, kind, draft, draft_revision, created_by, created_role)
);
-- §7.3: at most one running ingest per draft revision it binds.
CREATE UNIQUE INDEX operation_running_ingest ON operation (draft, draft_revision)
  WHERE kind = 'ingest' AND state = 'running';
-- §7.3: at most one queued or running publish operation per draft revision, over kind = 'publish'
-- only, so a publish and an ingest of one draft revision never collide.
CREATE UNIQUE INDEX operation_active_publish ON operation (draft, draft_revision)
  WHERE kind = 'publish' AND state IN ('queued', 'running');

-- A publish or ingest operation's timeline entries (§3 TimelineEvent, §8.3), numbered from 1 under
-- the operation's row lock (T7), each with the epoch it was appended in. apply-config and adopt
-- operations are on the machine timeline: the event names its operation's kind, and the key on
-- (operation, kind) holds it to the truth, which also keeps an operation with events from changing
-- kind.
CREATE TABLE operation_event (
  operation text NOT NULL REFERENCES operation (id),
  number    integer NOT NULL CHECK (number >= 1),
  epoch     text NOT NULL REFERENCES recovery_epoch (epoch),
  -- A JSON object: JSON null is not SQL NULL, and an immutable event cannot be corrected later.
  entry     jsonb NOT NULL CHECK (jsonb_typeof(entry) = 'object'),
  at        timestamptz NOT NULL,
  kind      text NOT NULL CONSTRAINT operation_event_job_kind CHECK (kind IN ('publish', 'ingest')),
  PRIMARY KEY (operation, number),
  CONSTRAINT operation_event_operation_kind FOREIGN KEY (operation, kind) REFERENCES operation (id, kind)
);
CALL make_immutable('operation_event');

-- Requests (§7, §10.4) ---------------------------------------------------------------------------

-- The idempotency record every committed mutating request writes with its effect.
CREATE TABLE idempotency_record (
  principal text NOT NULL REFERENCES principal (id),
  key text NOT NULL CHECK (key ~ '^[A-Za-z0-9_-]{16,128}$'),
  -- SHA-256, or HMAC-SHA-256 for a keyed route (§7.1), whose key identity and version
  -- fingerprint_key names; empty for SHA-256.
  fingerprint bytea NOT NULL CHECK (length(fingerprint) = 32),
  fingerprint_key text NOT NULL DEFAULT '',
  request_id text NOT NULL CHECK (request_id ~ '^req_[a-z2-7]{26}$'),
  epoch text NOT NULL REFERENCES recovery_epoch (epoch),
  -- The response to replay; never a secret value (design §11.1).
  status integer NOT NULL CHECK (status BETWEEN 200 AND 599),
  location text,
  etag text,
  body bytea NOT NULL,
  -- A 202's operation.
  operation_id text CHECK (operation_id ~ '^op_[a-z2-7]{26}$') REFERENCES operation (id),
  created_at timestamptz NOT NULL,
  PRIMARY KEY (principal, key)
);
CALL make_immutable('idempotency_record');

-- Identity revocation's row (T5c), one per identity: a revocation is permanent (§10.4). The act is
-- written after the effect in the same transaction, so its reference is checked at commit.
CREATE TABLE identity_revocation (
  identity text PRIMARY KEY REFERENCES principal (id),
  revoked_by text NOT NULL,
  revoked_by_kind text NOT NULL GENERATED ALWAYS AS ('human') STORED,
  role text NOT NULL CHECK (role = 'recovery-admin'),
  reason text NOT NULL CHECK (btrim(reason) <> '' AND octet_length(reason) <= 1024),
  act text NOT NULL UNIQUE REFERENCES act (id) DEFERRABLE INITIALLY DEFERRED,
  epoch text NOT NULL REFERENCES recovery_epoch (epoch),
  at timestamptz NOT NULL,
  FOREIGN KEY (revoked_by, revoked_by_kind) REFERENCES principal (id, kind)
);
CALL make_immutable('identity_revocation');

-- Machine timeline (§3 TimelineEvent, §3.3; execution-recovery.md §4.1) -----------------------

-- A machine's timeline: its plans, operations and machine-scope facts, each at a revision T7
-- allocates from the machine's revision counter under its row lock, with the epoch it was appended
-- in. Kinds are added with the issues that write them; an endpoint change is the first.
CREATE TABLE machine_event (
  machine  text NOT NULL REFERENCES machine (id),
  revision bigint NOT NULL CHECK (revision >= 1),
  epoch    text NOT NULL REFERENCES recovery_epoch (epoch),
  kind     text NOT NULL CONSTRAINT machine_event_kind CHECK (kind IN ('endpoint-change')),
  -- A JSON object: JSON null is not SQL NULL, and an immutable entry cannot be corrected later.
  entry    jsonb NOT NULL CHECK (jsonb_typeof(entry) = 'object'),
  at       timestamptz NOT NULL,
  PRIMARY KEY (machine, revision),
  UNIQUE (machine, revision, kind)
);
CALL make_immutable('machine_event');

-- A replaced endpoint (§3.3): the previous and new endpoint and the act that replaced it, which
-- holds who, role, epoch and time (§10.5). It is exactly one endpoint-change entry on the
-- machine's timeline, keyed by that entry's revision, so the record and the entry cannot part. The
-- act is written after the effect in the same transaction, so its reference is checked at commit.
CREATE TABLE machine_endpoint_change (
  machine           text NOT NULL,
  revision          bigint NOT NULL,
  kind              text NOT NULL DEFAULT 'endpoint-change' CHECK (kind = 'endpoint-change'),
  previous_endpoint talos_endpoint NOT NULL,
  new_endpoint      talos_endpoint NOT NULL,
  act               text NOT NULL UNIQUE REFERENCES act (id) DEFERRABLE INITIALLY DEFERRED,
  CONSTRAINT machine_endpoint_change_pkey PRIMARY KEY (machine, revision),
  CONSTRAINT machine_endpoint_change_entry FOREIGN KEY (machine, revision, kind)
    REFERENCES machine_event (machine, revision, kind),
  CONSTRAINT machine_endpoint_change_changes CHECK (previous_endpoint <> new_endpoint)
);
CALL make_immutable('machine_endpoint_change');

-- Sources (§3, §3.1, choice §17.31) --------------------------------------------------------------

-- Fragment, profile and assignment revisions and their heads, and a draft's entries for them.
-- Heads are inserted and advanced only by the publication commit (§6.2, T3); a draft update
-- inserts revisions and sets entries (T1). The PoC accepts cluster scope only: library scope
-- (choice §17.28) is refused here.

-- A fragment or profile name.
CREATE DOMAIN source_name AS text
  CONSTRAINT source_name_form CHECK (VALUE ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?$' AND octet_length(VALUE) <= 63);

-- The six fragment layers, in composition order: design §6.2's seven minus machine-intrinsic,
-- which is the import base (compilation §6).
CREATE DOMAIN fragment_layer AS text
  CONSTRAINT fragment_layer_known CHECK (VALUE IN ('global', 'site', 'cluster', 'role', 'workload', 'override'));

-- A fragment revision (§3; compilation §2, §5): its sanitized document, written by a draft update,
-- and the embedded documents it identifies, as compilation §5.2 declares them: a JSON array of
-- {"path", "format"}, which its reference rows do not hold. It names its fragment by cluster and
-- name, since the head may not exist until publication.
CREATE TABLE fragment_revision (
  id         text PRIMARY KEY CHECK (id ~ '^frv_[a-z2-7]{26}$'),
  cluster    text NOT NULL REFERENCES cluster (id),
  name       source_name NOT NULL,
  layer      fragment_layer NOT NULL,
  document   text NOT NULL CHECK (document <> ''),
  author     text NOT NULL REFERENCES principal (id),
  created_at timestamptz NOT NULL,
  writer     xid8 NOT NULL,
  embedded   jsonb NOT NULL CHECK (jsonb_typeof(embedded) = 'array'),
  UNIQUE (id, cluster),
  UNIQUE (id, cluster, name),
  UNIQUE (id, cluster, name, layer)
);
CALL make_immutable('fragment_revision');
CREATE TRIGGER writer BEFORE INSERT ON fragment_revision FOR EACH ROW EXECUTE FUNCTION stamp_revision_writer();

-- A fragment revision's reference rows, as import_base_reference's (compilation §5.1, §5.2; §6.4).
CREATE TABLE fragment_reference (
  revision   text NOT NULL REFERENCES fragment_revision (id),
  name       text NOT NULL CHECK (name ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?(/[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$'
               AND octet_length(name) <= 256),
  kind       text NOT NULL CHECK (kind IN ('string', 'integer', 'boolean', 'mapping')),
  version    integer NOT NULL CHECK (version >= 1),
  encoding   text CHECK (encoding = 'base64'),
  generation text NOT NULL CHECK (generation ~ '^gen/cl_[a-z2-7]{26}/ing_[a-z2-7]{26}/[A-Za-z0-9_-]{1,128}$'),
  PRIMARY KEY (revision, name),
  CHECK (encoding IS NULL OR kind = 'string')
);
CALL make_immutable('fragment_reference');
CREATE TRIGGER with_revision BEFORE INSERT ON fragment_reference
  FOR EACH ROW EXECUTE FUNCTION refuse_late_revision_row('fragment_revision');

-- A profile revision: fragment revisions in order (§3.1). A pin stays in its profile's cluster.
CREATE TABLE profile_revision (
  id         text PRIMARY KEY CHECK (id ~ '^prv_[a-z2-7]{26}$'),
  cluster    text NOT NULL REFERENCES cluster (id),
  name       source_name NOT NULL,
  author     text NOT NULL REFERENCES principal (id),
  created_at timestamptz NOT NULL,
  writer     xid8 NOT NULL,
  UNIQUE (id, cluster),
  UNIQUE (id, cluster, name)
);
CALL make_immutable('profile_revision');
CREATE TRIGGER writer BEFORE INSERT ON profile_revision FOR EACH ROW EXECUTE FUNCTION stamp_revision_writer();

CREATE TABLE profile_revision_fragment (
  revision          text NOT NULL,
  cluster           text NOT NULL,
  position          integer NOT NULL CHECK (position >= 0),
  fragment_revision text NOT NULL,
  PRIMARY KEY (revision, position),
  UNIQUE (revision, fragment_revision),
  FOREIGN KEY (revision, cluster) REFERENCES profile_revision (id, cluster),
  FOREIGN KEY (fragment_revision, cluster) REFERENCES fragment_revision (id, cluster)
);
CALL make_immutable('profile_revision_fragment');
CREATE TRIGGER with_revision BEFORE INSERT ON profile_revision_fragment
  FOR EACH ROW EXECUTE FUNCTION refuse_late_revision_row('profile_revision');

-- An assignment revision: a machine's profiles, then its fragments per layer, each by name (§3.1).
CREATE TABLE assignment_revision (
  id         text PRIMARY KEY CHECK (id ~ '^asr_[a-z2-7]{26}$'),
  cluster    text NOT NULL,
  machine    text NOT NULL,
  author     text NOT NULL REFERENCES principal (id),
  created_at timestamptz NOT NULL,
  writer     xid8 NOT NULL,
  FOREIGN KEY (machine, cluster) REFERENCES machine (id, cluster),
  UNIQUE (id, cluster),
  UNIQUE (id, cluster, machine)
);
CALL make_immutable('assignment_revision');
CREATE TRIGGER writer BEFORE INSERT ON assignment_revision FOR EACH ROW EXECUTE FUNCTION stamp_revision_writer();

CREATE TABLE assignment_revision_profile (
  revision text NOT NULL REFERENCES assignment_revision (id),
  position integer NOT NULL CHECK (position >= 0),
  profile  source_name NOT NULL,
  PRIMARY KEY (revision, position),
  UNIQUE (revision, profile)
);
CALL make_immutable('assignment_revision_profile');
CREATE TRIGGER with_revision BEFORE INSERT ON assignment_revision_profile
  FOR EACH ROW EXECUTE FUNCTION refuse_late_revision_row('assignment_revision');

CREATE TABLE assignment_revision_fragment (
  revision text NOT NULL REFERENCES assignment_revision (id),
  layer    fragment_layer NOT NULL,
  position integer NOT NULL CHECK (position >= 0),
  fragment source_name NOT NULL,
  PRIMARY KEY (revision, layer, position),
  UNIQUE (revision, fragment)
);
CALL make_immutable('assignment_revision_fragment');
CREATE TRIGGER with_revision BEFORE INSERT ON assignment_revision_fragment
  FOR EACH ROW EXECUTE FUNCTION refuse_late_revision_row('assignment_revision');

-- Heads (§3.1, §4.1): one per name in a cluster, or per machine, pointing at a revision of that
-- name (and, for a fragment, layer); a removal sets the pointer to NULL and keeps the head. The
-- (id, cluster, name) and (id, cluster, machine) keys are the ones a release's sources name.
CREATE TABLE fragment (
  id               text PRIMARY KEY CHECK (id ~ '^frg_[a-z2-7]{26}$'),
  cluster          text NOT NULL REFERENCES cluster (id),
  scope            text NOT NULL CONSTRAINT fragment_cluster_scope CHECK (scope = 'cluster'),
  name             source_name NOT NULL,
  layer            fragment_layer NOT NULL,
  head_revision_id text,
  head_revision    integer NOT NULL CHECK (head_revision >= 1),
  etag_token       text NOT NULL CHECK (etag_token ~ '^[a-z2-7]{26}$'),
  created_at       timestamptz NOT NULL,
  UNIQUE (cluster, name),
  UNIQUE (id, cluster, name),
  CONSTRAINT fragment_head_revision FOREIGN KEY (head_revision_id, cluster, name, layer)
    REFERENCES fragment_revision (id, cluster, name, layer)
);

CREATE TABLE profile (
  id               text PRIMARY KEY CHECK (id ~ '^prf_[a-z2-7]{26}$'),
  cluster          text NOT NULL REFERENCES cluster (id),
  scope            text NOT NULL CONSTRAINT profile_cluster_scope CHECK (scope = 'cluster'),
  name             source_name NOT NULL,
  head_revision_id text,
  head_revision    integer NOT NULL CHECK (head_revision >= 1),
  etag_token       text NOT NULL CHECK (etag_token ~ '^[a-z2-7]{26}$'),
  created_at       timestamptz NOT NULL,
  UNIQUE (cluster, name),
  UNIQUE (id, cluster, name),
  FOREIGN KEY (head_revision_id, cluster, name) REFERENCES profile_revision (id, cluster, name)
);

CREATE TABLE assignment (
  id               text PRIMARY KEY CHECK (id ~ '^asg_[a-z2-7]{26}$'),
  cluster          text NOT NULL,
  machine          text NOT NULL UNIQUE,
  head_revision_id text,
  head_revision    integer NOT NULL CHECK (head_revision >= 1),
  etag_token       text NOT NULL CHECK (etag_token ~ '^[a-z2-7]{26}$'),
  created_at       timestamptz NOT NULL,
  UNIQUE (id, cluster, machine),
  FOREIGN KEY (machine, cluster) REFERENCES machine (id, cluster),
  FOREIGN KEY (head_revision_id, cluster, machine) REFERENCES assignment_revision (id, cluster, machine)
);

-- A draft's fragment, profile and assignment entries (§3.1), set by T1: the proposed revision, or
-- none for a removal, and the base, the head revision the author edited from (NULL: absent). One
-- entry per name or machine; import base entries stay in draft_entry.
CREATE TABLE draft_source_entry (
  draft               text NOT NULL,
  cluster             text NOT NULL,
  kind                text NOT NULL,
  name                source_name,
  machine             text,
  fragment_revision   text,
  profile_revision    text,
  assignment_revision text,
  base                integer CHECK (base >= 1),
  CONSTRAINT draft_source_entry_shape CHECK (
    (kind = 'fragment' AND name IS NOT NULL AND machine IS NULL
      AND profile_revision IS NULL AND assignment_revision IS NULL) OR
    (kind = 'profile' AND name IS NOT NULL AND machine IS NULL
      AND fragment_revision IS NULL AND assignment_revision IS NULL) OR
    (kind = 'assignment' AND name IS NULL AND machine IS NOT NULL
      AND fragment_revision IS NULL AND profile_revision IS NULL)),
  FOREIGN KEY (draft, cluster) REFERENCES draft (id, cluster),
  FOREIGN KEY (machine, cluster) REFERENCES machine (id, cluster),
  FOREIGN KEY (fragment_revision, cluster, name) REFERENCES fragment_revision (id, cluster, name),
  FOREIGN KEY (profile_revision, cluster, name) REFERENCES profile_revision (id, cluster, name),
  FOREIGN KEY (assignment_revision, cluster, machine) REFERENCES assignment_revision (id, cluster, machine)
);
CREATE UNIQUE INDEX draft_source_entry_name ON draft_source_entry (draft, kind, name) WHERE kind <> 'assignment';
CREATE UNIQUE INDEX draft_source_entry_machine ON draft_source_entry (draft, machine) WHERE kind = 'assignment';

-- Releases (§3, §6.2, §7.3; compilation.md §9, §10.2, §11; dependency monitor §5.1) -------------

-- A release and its machines, sources and dependency records, written only by the publication
-- commit (T3), and the dependency status it seeds.

-- A release's rows are written in the transaction that writes the release (§3), as a revision's
-- are: this refuses an INSERT that would add to a committed release, with make_immutable's
-- SQLSTATE, and one naming a release this transaction cannot see, at once.
CREATE FUNCTION refuse_late_release_row() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  w xid8;
BEGIN
  SELECT writer INTO w FROM release WHERE id = NEW.release;
  IF w IS NULL THEN
    RAISE EXCEPTION 'table %: no such release', TG_TABLE_NAME USING ERRCODE = 'foreign_key_violation';
  ELSIF w <> pg_current_xact_id() THEN
    RAISE EXCEPTION 'table %: a row of a committed release is refused', TG_TABLE_NAME USING ERRCODE = 'BW001';
  END IF;
  RETURN NEW;
END
$$;

-- A release (§3, §6.2): one cluster's draft revision, published once (choice §17.10), by the
-- publish operation that committed it. digest is the content digest over its metadata and its
-- machines' configuration digests, never over ciphertext (§6.2). The renderer and contract record
-- (compilation §10.2): the target contract, the machinery module's version and checksum, and the
-- Kubernetes version; each machine's validation mode is its row's. Its publisher is the principal
-- that requested the publish operation, in the role it requested it in.
CREATE TABLE release (
  id                 text PRIMARY KEY CHECK (id ~ '^rel_[a-z2-7]{26}$'),
  cluster            text NOT NULL REFERENCES cluster (id),
  draft              text NOT NULL,
  draft_revision     integer NOT NULL CHECK (draft_revision >= 1),
  digest             bytea NOT NULL CHECK (length(digest) = 32),
  contract           text NOT NULL CHECK (contract ~ '^v[0-9]{1,4}\.[0-9]{1,4}$'),
  -- A canonical Go module version (semver): no empty part, no leading zero in a numeric part.
  machinery_version  text NOT NULL CHECK (length(machinery_version) <= 80 AND machinery_version ~
                       '^v(0|[1-9][0-9]{0,3})\.(0|[1-9][0-9]{0,3})\.(0|[1-9][0-9]{0,3})(-(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*)?$'),
  -- The go.sum hash of the machinery module: h1: and the base64 of a SHA-256.
  machinery_checksum text NOT NULL CHECK (machinery_checksum ~ '^h1:[A-Za-z0-9+/]{43}=$'),
  kubernetes_version text NOT NULL CHECK (kubernetes_version ~ '^v[0-9]{1,4}\.[0-9]{1,4}\.[0-9]{1,4}$'),
  operation          text NOT NULL UNIQUE,
  operation_kind     text NOT NULL DEFAULT 'publish' CHECK (operation_kind = 'publish'),
  published_by       text NOT NULL REFERENCES principal (id),
  published_role     text NOT NULL CHECK (published_role = 'publisher'),
  epoch              text NOT NULL REFERENCES recovery_epoch (epoch),
  published_at       timestamptz NOT NULL,
  writer             xid8 NOT NULL,
  UNIQUE (draft, draft_revision),
  UNIQUE (id, cluster),
  UNIQUE (id, draft),
  FOREIGN KEY (draft, cluster) REFERENCES draft (id, cluster),
  -- The publish operation of this draft revision, so a retry finds the release by its operation's
  -- natural key (§6.2), requested by its publisher.
  CONSTRAINT release_operation FOREIGN KEY (operation, operation_kind, draft, draft_revision, published_by, published_role)
    REFERENCES operation (id, kind, draft, draft_revision, created_by, created_role)
);
CALL make_immutable('release');
CREATE TRIGGER writer BEFORE INSERT ON release FOR EACH ROW EXECUTE FUNCTION stamp_revision_writer();

-- The one cycle: a draft names the release that published it (§6.2), and the release its draft.
ALTER TABLE draft ADD FOREIGN KEY (release, id) REFERENCES release (id, draft);

-- A release names each head it used once, as its sources (§6.2): the revision (NULL: removed) and
-- the head revision the release left it at. A head the release introduces is inserted later in the
-- same transaction, so the head keys are checked at commit.
CREATE TABLE release_source (
  release             text NOT NULL,
  cluster             text NOT NULL,
  kind                text NOT NULL CHECK (kind IN ('fragment', 'profile', 'assignment')),
  fragment            text,
  profile             text,
  assignment          text,
  name                source_name,
  machine             text,
  fragment_revision   text,
  profile_revision    text,
  assignment_revision text,
  head_revision       integer NOT NULL CHECK (head_revision >= 1),
  -- Each kind's shape is an implication, so a row of an unknown kind is refused by the kind check
  -- alone.
  CONSTRAINT release_source_shape CHECK (
    (kind <> 'fragment' OR (fragment IS NOT NULL AND name IS NOT NULL AND profile IS NULL AND assignment IS NULL
      AND machine IS NULL AND profile_revision IS NULL AND assignment_revision IS NULL)) AND
    (kind <> 'profile' OR (profile IS NOT NULL AND name IS NOT NULL AND fragment IS NULL AND assignment IS NULL
      AND machine IS NULL AND fragment_revision IS NULL AND assignment_revision IS NULL)) AND
    (kind <> 'assignment' OR (assignment IS NOT NULL AND machine IS NOT NULL AND fragment IS NULL AND profile IS NULL
      AND name IS NULL AND fragment_revision IS NULL AND profile_revision IS NULL))),
  UNIQUE (release, fragment),
  UNIQUE (release, profile),
  UNIQUE (release, assignment),
  UNIQUE (release, fragment_revision),
  UNIQUE (release, machine, assignment_revision),
  FOREIGN KEY (release, cluster) REFERENCES release (id, cluster),
  FOREIGN KEY (fragment, cluster, name) REFERENCES fragment (id, cluster, name) DEFERRABLE INITIALLY DEFERRED,
  FOREIGN KEY (profile, cluster, name) REFERENCES profile (id, cluster, name) DEFERRABLE INITIALLY DEFERRED,
  FOREIGN KEY (assignment, cluster, machine) REFERENCES assignment (id, cluster, machine) DEFERRABLE INITIALLY DEFERRED,
  FOREIGN KEY (fragment_revision, cluster, name) REFERENCES fragment_revision (id, cluster, name),
  FOREIGN KEY (profile_revision, cluster, name) REFERENCES profile_revision (id, cluster, name),
  FOREIGN KEY (assignment_revision, cluster, machine) REFERENCES assignment_revision (id, cluster, machine)
);
CALL make_immutable('release_source');
CREATE TRIGGER with_release BEFORE INSERT ON release_source
  FOR EACH ROW EXECUTE FUNCTION refuse_late_release_row();

-- A release's sources are the ones its compilation used (§3.1): each fragment revision a profile
-- source pins is a fragment source of the release, and each name an assignment source selects is a
-- source of the release with a revision, a fragment one under the layer its revision carries. The
-- sources are written in any order, so this is checked at commit.
CREATE FUNCTION require_release_selection() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.profile_revision IS NOT NULL AND EXISTS (
       SELECT FROM profile_revision_fragment p WHERE p.revision = NEW.profile_revision AND NOT EXISTS (
         SELECT FROM release_source s WHERE s.release = NEW.release AND s.fragment_revision = p.fragment_revision)) THEN
    RAISE EXCEPTION 'release_source: a profile pinning a fragment revision the release does not name is refused'
      USING ERRCODE = 'check_violation', CONSTRAINT = 'release_source_pin', TABLE = 'release_source';
  END IF;
  IF NEW.assignment_revision IS NOT NULL AND (EXISTS (
       SELECT FROM assignment_revision_profile a WHERE a.revision = NEW.assignment_revision AND NOT EXISTS (
         SELECT FROM release_source s WHERE s.release = NEW.release AND s.kind = 'profile' AND s.name = a.profile
           AND s.profile_revision IS NOT NULL))
     OR EXISTS (
       SELECT FROM assignment_revision_fragment a WHERE a.revision = NEW.assignment_revision AND NOT EXISTS (
         SELECT FROM release_source s JOIN fragment_revision f ON f.id = s.fragment_revision
         WHERE s.release = NEW.release AND s.name = a.fragment AND f.layer = a.layer))) THEN
    RAISE EXCEPTION 'release_source: an assignment selecting a name the release does not name is refused'
      USING ERRCODE = 'check_violation', CONSTRAINT = 'release_source_selection', TABLE = 'release_source';
  END IF;
  RETURN NULL;
END
$$;
CREATE CONSTRAINT TRIGGER selection AFTER INSERT ON release_source DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION require_release_selection();

-- A machine of a release (§3; compilation §11): its import base and assignment revisions, the
-- mode it was validated in, its artifact ciphertext and the ciphertext's SHA-256, the SHA-256 of
-- the plaintext configuration (§1.1), and its review data: the redacted configuration and the
-- provenance records (compilation §8.2, §8.3), which hold no value. A configuration that could
-- not be redacted is NULL: it shows nothing and says so (§8.3). key_version is the Transit key
-- version the ciphertext names, of up to 19 digits; one past the bigint range is NULL and refused.
-- key_name is the Transit key the ciphertext was encrypted under, which the ciphertext names only
-- by version (compilation §9); its encryption dependency names that key at that version, not
-- another key retained at the same version, which the monitor would report while the artifact's
-- own key is blocked or lost (dependency monitor §3). The key name's form is its status row's,
-- which the dependency's status key binds. Its assignment revision is the one its release's
-- assignment source names, inserted later in the transaction.
CREATE TABLE release_machine (
  release              text NOT NULL,
  cluster              text NOT NULL,
  machine              text NOT NULL,
  import_base_revision text NOT NULL,
  assignment_revision  text,
  mode                 text NOT NULL CHECK (mode IN ('metal', 'container', 'cloud')),
  ciphertext           text NOT NULL CHECK (ciphertext ~ '^vault:v[1-9][0-9]{0,18}:[A-Za-z0-9+/]+={0,2}$'),
  key_version          bigint GENERATED ALWAYS AS (CASE
                         WHEN substring(ciphertext FROM '^vault:v([0-9]{1,19}):')::numeric <= 9223372036854775807
                         THEN substring(ciphertext FROM '^vault:v([0-9]{1,19}):')::bigint END) STORED,
  CONSTRAINT release_machine_key_version CHECK (key_version IS NOT NULL),
  ciphertext_digest    bytea NOT NULL CHECK (length(ciphertext_digest) = 32),
  configuration_digest bytea NOT NULL CHECK (length(configuration_digest) = 32),
  redacted             text CHECK (redacted <> ''),
  provenance           jsonb NOT NULL CHECK (jsonb_typeof(provenance) = 'array'),
  key_name             text NOT NULL,
  PRIMARY KEY (release, machine),
  UNIQUE (release, machine, import_base_revision),
  UNIQUE (release, machine, key_name, key_version),
  FOREIGN KEY (release, cluster) REFERENCES release (id, cluster),
  FOREIGN KEY (machine, cluster) REFERENCES machine (id, cluster),
  -- Each machine validates in its recorded platform mode (§3.3).
  CONSTRAINT release_machine_platform FOREIGN KEY (machine, mode) REFERENCES machine (id, platform),
  FOREIGN KEY (import_base_revision, machine) REFERENCES import_base_revision (id, machine),
  FOREIGN KEY (assignment_revision, cluster, machine) REFERENCES assignment_revision (id, cluster, machine),
  CONSTRAINT release_machine_assignment_source
    FOREIGN KEY (release, machine, assignment_revision) REFERENCES release_source (release, machine, assignment_revision)
    DEFERRABLE INITIALLY DEFERRED
);
CALL make_immutable('release_machine');
CREATE TRIGGER with_release BEFORE INSERT ON release_machine
  FOR EACH ROW EXECUTE FUNCTION refuse_late_release_row();

-- Each machine's artifact has its encryption dependency (compilation §9), written later in the
-- release's transaction, so this is checked at commit; the dependency key binds its key and version.
CREATE FUNCTION require_encryption_dependency() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NOT EXISTS (SELECT FROM dependency
                 WHERE release = NEW.release AND machine = NEW.machine AND kind = 'encryption') THEN
    RAISE EXCEPTION 'release_machine: a machine without its encryption dependency is refused'
      USING ERRCODE = 'check_violation', CONSTRAINT = 'release_machine_encryption', TABLE = 'release_machine';
  END IF;
  RETURN NULL;
END
$$;
CREATE CONSTRAINT TRIGGER encryption AFTER INSERT ON release_machine DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION require_encryption_dependency();

-- A machine's Desired, Applied (release, verified configuration digest and source) and baseline
-- revision (§3; execution and recovery, Desired, Applied and Observed). The baseline revision is
-- a per-machine counter that every change of Applied advances, by a completed operation or an
-- adoption record, and a machine with no Applied has none (execution and recovery §2). Desired and
-- Applied each name a release that covers the machine.
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
     AND (applied_release IS NULL) = (baseline_revision IS NULL)),
  FOREIGN KEY (desired, machine) REFERENCES release_machine (release, machine),
  FOREIGN KEY (applied_release, machine) REFERENCES release_machine (release, machine)
);

-- The last classification of each provider object version a release depends on (dependency
-- monitor §5.1): the only mutable dependency table, updated under its row lock. Its object is a
-- KV generation path (the reference rows' form) or a Transit key name (as configuration accepts
-- one: one URL path segment of at most 227 bytes). A version is identified with the creation time
-- the provider gave it (dependency monitor §3), as the dependency records name it: an object
-- deleted and recreated reissues its version numbers, and the replacement is another dependency
-- with its own status. Its class and reason are a row of the monitor's §3 table for its provider;
-- only an unknown one has the time it became unknown.
CREATE FUNCTION dependency_reason(provider text, class text, reason text) RETURNS boolean
  LANGUAGE sql IMMUTABLE AS $$
  -- Dependency monitor §3's table: each provider's classes and reasons.
  SELECT (class = 'retained' AND (reason IS NULL OR (provider = 'kv' AND reason = 'deletion-scheduled'))) OR
    (class = 'unknown' AND reason IN ('malformed', 'denied', 'absent', 'unavailable', 'unreachable', 'unreadable',
      'insufficient-evidence', 'identity-mismatch')) OR
    (class = 'unknown' AND provider = 'kv' AND reason = 'deletion-time-undecidable') OR
    (class = 'unknown' AND provider = 'transit' AND reason IN ('soft-delete-unobserved', 'trimmed-unverified',
      'below-decryption-floor-unverified')) OR
    (class = 'lost' AND provider = 'kv' AND reason IN ('destroyed', 'pruned')) OR
    (class = 'lost' AND provider = 'transit' AND reason = 'trimmed') OR
    (class = 'blocked' AND provider = 'kv' AND reason = 'soft-deleted') OR
    (class = 'blocked' AND provider = 'transit' AND reason = 'below-decryption-floor')
$$;
CREATE TABLE dependency_status (
  id                    text PRIMARY KEY CHECK (id ~ '^dep_[a-z2-7]{26}$'),
  provider              text NOT NULL CHECK (provider IN ('kv', 'transit')),
  object                text NOT NULL,
  version               bigint NOT NULL CHECK (version >= 1),
  created               text NOT NULL CHECK (created ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?Z$'),
  class                 text NOT NULL,
  reason                text,
  first_retained_at     timestamptz,
  unknown_since         timestamptz,
  persistent_alerted_at timestamptz,
  deletion_observed     timestamptz,
  deletion_warned       timestamptz,
  observed_from         timestamptz NOT NULL,
  recorded_at           timestamptz NOT NULL,
  answer_date           timestamptz,
  UNIQUE (provider, object, version, created),
  -- The key an alert names its dependency's identity by (dependency monitor §5.1).
  UNIQUE (id, provider, object, version, created),
  -- Implications per provider, so a row of an unknown provider is refused by the provider check
  -- alone.
  CONSTRAINT dependency_status_object CHECK (
    (provider <> 'kv' OR object ~ '^gen/cl_[a-z2-7]{26}/ing_[a-z2-7]{26}/[A-Za-z0-9_-]{1,128}$') AND
    (provider <> 'transit' OR (object NOT IN ('.', '..') AND octet_length(object) BETWEEN 1 AND 227
      AND object !~ '[/#?%\\[:space:][:cntrl:]]'))),
  -- A retained version's only reason is a KV version's deletion-scheduled, with the scheduled time
  -- it observed. A NULL comparison is no match, never a pass.
  CONSTRAINT dependency_status_reason CHECK (COALESCE(dependency_reason(provider, class, reason), false)),
  CONSTRAINT dependency_status_schedule CHECK (reason <> 'deletion-scheduled' OR deletion_observed IS NOT NULL),
  CONSTRAINT dependency_status_unknown_since CHECK ((class = 'unknown') = (unknown_since IS NOT NULL)),
  -- A class is recorded after the request that observed it began (dependency monitor §5.2, §6.1):
  -- publication's re-check compares recorded_at with the time it began classifying, so a row
  -- recorded before it was observed could date a transition before a publication it raced.
  CONSTRAINT dependency_status_times CHECK (recorded_at >= observed_from)
);

-- A KV version holds one identity (dependency monitor §5.1): a second creation time for one
-- version means its path was deleted and recreated, a changed identity that publication refuses,
-- never another status. Two publications seeding one version concurrently meet here, and the
-- second reads the first's row.
CREATE UNIQUE INDEX dependency_status_kv_version ON dependency_status (object, version) WHERE provider = 'kv';

-- A release machine's dependency records (compilation §9): each effective dependency, each
-- reproduction dependency and the artifact's encryption dependency. A reproduction dependency is
-- one reference occurrence in its source revision: its path is shown redacted, so two occurrences
-- can share one (a mapping key holding a value reads <redacted>), and each is named by its
-- ordinal among its source revision's occurrences instead. Each record names a provider object
-- version by the creation time the provider gave it, kept as RFC 3339 text: a KV created_time
-- has nanoseconds, which a timestamp would round. An encryption dependency names its machine's
-- Transit key at the ciphertext's key version; a reproduction dependency's source revision is its
-- machine's import base or a fragment revision among the release's sources.
CREATE TABLE dependency (
  release         text NOT NULL,
  machine         text NOT NULL,
  kind            text NOT NULL CHECK (kind IN ('effective', 'reproduction', 'encryption')),
  provider        text NOT NULL,
  object          text NOT NULL,
  version         bigint NOT NULL,
  created         text NOT NULL CHECK (created ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?Z$'),
  reference       text CHECK (reference ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?(/[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$'
                    AND octet_length(reference) <= 256),
  source_revision text CHECK (source_revision ~ '^(ibr|frv)_[a-z2-7]{26}$'),
  source_digest   bytea CHECK (length(source_digest) = 32),
  path            text CHECK (path <> ''),
  occurrence      integer CHECK (occurrence >= 0),
  key_version     bigint GENERATED ALWAYS AS (CASE WHEN kind = 'encryption' THEN version END) STORED,
  source_import_base text GENERATED ALWAYS AS (
                    CASE WHEN left(source_revision, 4) = 'ibr_' THEN source_revision END) STORED,
  source_fragment_revision text GENERATED ALWAYS AS (
                    CASE WHEN left(source_revision, 4) = 'frv_' THEN source_revision END) STORED,
  key_name        text GENERATED ALWAYS AS (CASE WHEN kind = 'encryption' THEN object END) STORED,
  -- Implications per kind, so a row of an unknown kind is refused by the kind check alone.
  CONSTRAINT dependency_shape CHECK (
    (kind <> 'encryption' OR (provider = 'transit' AND reference IS NULL AND source_revision IS NULL
      AND source_digest IS NULL AND path IS NULL AND occurrence IS NULL)) AND
    (kind <> 'effective' OR (provider = 'kv' AND reference IS NOT NULL AND source_revision IS NULL
      AND source_digest IS NULL AND path IS NULL AND occurrence IS NULL)) AND
    (kind <> 'reproduction' OR (provider = 'kv' AND reference IS NOT NULL AND source_revision IS NOT NULL
      AND source_digest IS NOT NULL AND path IS NOT NULL AND occurrence IS NOT NULL))),
  FOREIGN KEY (release, machine) REFERENCES release_machine (release, machine),
  FOREIGN KEY (provider, object, version, created) REFERENCES dependency_status (provider, object, version, created),
  FOREIGN KEY (release, machine, source_import_base) REFERENCES release_machine (release, machine, import_base_revision),
  FOREIGN KEY (release, source_fragment_revision) REFERENCES release_source (release, fragment_revision),
  CONSTRAINT dependency_encryption_key FOREIGN KEY (release, machine, key_name, key_version)
    REFERENCES release_machine (release, machine, key_name, key_version)
);
CREATE UNIQUE INDEX dependency_effective ON dependency (release, machine, reference, object, version)
  WHERE kind = 'effective';
CREATE UNIQUE INDEX dependency_reproduction ON dependency (release, machine, source_revision, occurrence)
  WHERE kind = 'reproduction';
CREATE UNIQUE INDEX dependency_encryption ON dependency (release, machine) WHERE kind = 'encryption';
-- The monitor reads the releases that reference a dependency by its status (dependency monitor
-- §6.1 step 5), and every other index on dependency leads with the release.
CREATE INDEX dependency_status_identity ON dependency (provider, object, version, created);
CALL make_immutable('dependency');
CREATE TRIGGER with_release BEFORE INSERT ON dependency
  FOR EACH ROW EXECUTE FUNCTION refuse_late_release_row();

-- An effective dependency is a reference occurrence that reached the artifact (compilation §9), so
-- the machine has a reproduction dependency at the same version and creation time, whose source
-- revision is in the machine's composition and declares that reference at that version and
-- generation (compilation §5.1): one the artifact does not hold cannot stand in for the generation
-- it does. The import base is the machine's own by the reproduction row's key; a fragment revision
-- is one a profile its assignment selects pins, or one the release names under a fragment name the
-- assignment selects (compilation §6), as T3's own check composes it. A declaration has no
-- creation time; the occurrence's row names it. Its reproduction rows and the release's sources are
-- written in the same transaction, so this is checked at commit.
CREATE FUNCTION require_effective_occurrence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NOT EXISTS (
      SELECT FROM dependency o
       WHERE o.release = NEW.release AND o.machine = NEW.machine AND o.kind = 'reproduction'
         AND o.reference = NEW.reference AND o.object = NEW.object AND o.version = NEW.version
         AND o.created = NEW.created
         AND (EXISTS (SELECT FROM import_base_reference d WHERE d.revision = o.source_import_base
                        AND d.name = o.reference AND d.version = o.version AND d.generation = o.object)
           OR EXISTS (SELECT FROM fragment_reference d WHERE d.revision = o.source_fragment_revision
                        AND d.name = o.reference AND d.version = o.version AND d.generation = o.object)
              AND EXISTS (SELECT FROM release_machine m WHERE m.release = o.release AND m.machine = o.machine
                            AND (EXISTS (SELECT FROM assignment_revision_profile a
                                           JOIN release_source s ON s.release = m.release AND s.kind = 'profile'
                                             AND s.name = a.profile
                                           JOIN profile_revision_fragment p ON p.revision = s.profile_revision
                                          WHERE a.revision = m.assignment_revision
                                            AND p.fragment_revision = o.source_fragment_revision)
                              OR EXISTS (SELECT FROM assignment_revision_fragment a
                                           JOIN release_source s ON s.release = m.release AND s.kind = 'fragment'
                                             AND s.name = a.fragment
                                          WHERE a.revision = m.assignment_revision
                                            AND s.fragment_revision = o.source_fragment_revision))))) THEN
    RAISE EXCEPTION 'dependency: an effective dependency without a declared occurrence is refused'
      USING ERRCODE = 'check_violation', CONSTRAINT = 'dependency_effective', TABLE = 'dependency';
  END IF;
  RETURN NULL;
END
$$;
CREATE CONSTRAINT TRIGGER effective AFTER INSERT ON dependency DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW WHEN (NEW.kind = 'effective') EXECUTE FUNCTION require_effective_occurrence();

-- The dependency monitor's single row (dependency monitor §5.1): its last progress (§6.3), last
-- completed pass, last monitor-stalled alert and the recording sequence of the last alert logged
-- (§7.1). It is created with the installation, its progress the installation time, so a monitor
-- that never runs is stalled after three intervals. Progress and the last logged sequence never
-- move back: a writer keeps the later value, and one that would not is refused.
CREATE TABLE dependency_monitor (
  singleton    boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  progress     timestamptz NOT NULL,
  last_pass    timestamptz,
  last_stalled timestamptz,
  last_logged  bigint NOT NULL DEFAULT 0 CHECK (last_logged >= 0)
);
CREATE FUNCTION refuse_monitor_regression() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.progress < OLD.progress OR NEW.last_logged < OLD.last_logged THEN
    RAISE EXCEPTION 'dependency_monitor: progress or the last logged sequence moving back is refused'
      USING ERRCODE = 'check_violation', CONSTRAINT = 'dependency_monitor_forward', TABLE = 'dependency_monitor';
  END IF;
  RETURN NEW;
END
$$;
CREATE TRIGGER forward BEFORE UPDATE ON dependency_monitor
  FOR EACH ROW EXECUTE FUNCTION refuse_monitor_regression();

-- Every alert the dependency monitor raises (dependency monitor §5.1, §6.2), with the class and
-- reason it recorded, the identity of its dependency, the releases it names, the scheduled
-- deletion time a deletion-scheduled alert warns of, and the observation it came from. A
-- monitor-stalled alert concerns no dependency, so all of those are empty. seq is the recording
-- sequence the logger reads by (§7.1): the insert allocates it only after locking the
-- DependencyMonitor row, held to commit, so sequences commit in order and a sequence the writer
-- supplies is replaced.
CREATE TABLE dependency_alert (
  id            text PRIMARY KEY CHECK (id ~ '^dal_[a-z2-7]{26}$'),
  seq           bigint NOT NULL UNIQUE,
  kind          text NOT NULL,
  dependency    text,
  provider      text,
  object        text,
  version       bigint,
  created       text,
  class         text,
  reason        text,
  releases      text[],
  deletion      timestamptz,
  observed_from timestamptz,
  answer_date   timestamptz,
  epoch         text NOT NULL REFERENCES recovery_epoch (epoch),
  recorded_at   timestamptz NOT NULL,
  FOREIGN KEY (dependency, provider, object, version, created)
    REFERENCES dependency_status (id, provider, object, version, created),
  -- Each kind's class (§6.2): lost and blocked on entry to that class, regression and persistent
  -- of an unknown version, deletion-scheduled of a retained version with a scheduled deletion; a
  -- monitor-stalled alert has none.
  CONSTRAINT dependency_alert_kind CHECK (COALESCE(
    (kind = 'monitor-stalled' AND dependency IS NULL AND class IS NULL) OR
    (kind = 'lost' AND class = 'lost') OR
    (kind = 'blocked' AND class = 'blocked') OR
    (kind IN ('regression', 'persistent') AND class = 'unknown') OR
    (kind = 'deletion-scheduled' AND class = 'retained' AND reason = 'deletion-scheduled'), false)),
  CONSTRAINT dependency_alert_reason CHECK (
    kind = 'monitor-stalled' OR COALESCE(dependency_reason(provider, class, reason), false)),
  CONSTRAINT dependency_alert_shape CHECK (
    (kind = 'monitor-stalled' OR (dependency IS NOT NULL AND provider IS NOT NULL AND object IS NOT NULL
      AND version IS NOT NULL AND created IS NOT NULL AND releases IS NOT NULL AND cardinality(releases) > 0
      AND observed_from IS NOT NULL)) AND
    (kind <> 'monitor-stalled' OR (dependency IS NULL AND provider IS NULL AND object IS NULL AND version IS NULL
      AND created IS NULL AND class IS NULL AND reason IS NULL AND releases IS NULL AND observed_from IS NULL
      AND answer_date IS NULL)) AND
    (kind = 'deletion-scheduled') = (deletion IS NOT NULL))
);
CREATE SEQUENCE dependency_alert_seq AS bigint OWNED BY dependency_alert.seq;
-- An alert's dependency alerts are served in recording order (dependency monitor §7.2), and §6.2
-- reads a version's earlier deletion-scheduled alerts.
CREATE INDEX dependency_alert_dependency ON dependency_alert (dependency, seq);
CALL make_immutable('dependency_alert');

-- Allocates the recording sequence under the DependencyMonitor row lock (dependency monitor §6.1
-- step 5, §7.1): a sequence taken before the lock could commit after a higher one, which the logger
-- would already have passed. The releases an alert names each reference its version, once.
CREATE FUNCTION record_dependency_alert() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  PERFORM 1 FROM dependency_monitor FOR UPDATE;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'dependency_alert: no DependencyMonitor row' USING ERRCODE = 'foreign_key_violation';
  END IF;
  NEW.seq := nextval('dependency_alert_seq');
  IF NEW.releases IS NOT NULL AND NEW.dependency IS NOT NULL AND (
       (SELECT count(DISTINCT r) FROM unnest(NEW.releases) r) <> cardinality(NEW.releases)
       OR EXISTS (SELECT FROM unnest(NEW.releases) r WHERE NOT EXISTS (
         SELECT FROM dependency d WHERE d.release = r AND d.provider = NEW.provider AND d.object = NEW.object
           AND d.version = NEW.version AND d.created = NEW.created))) THEN
    RAISE EXCEPTION 'dependency_alert: a release named twice or not referencing the version is refused'
      USING ERRCODE = 'check_violation', CONSTRAINT = 'dependency_alert_releases', TABLE = 'dependency_alert';
  END IF;
  RETURN NEW;
END
$$;
CREATE TRIGGER record BEFORE INSERT ON dependency_alert
  FOR EACH ROW EXECUTE FUNCTION record_dependency_alert();
