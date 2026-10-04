-- 0011 Releases (persistence-api.md §3, §6.2, §7.3; compilation.md §9, §10.2, §11; dependency
-- monitor §5.1): a release and its machines, sources and dependency records, written only by the
-- publication commit (T3); the dependency status it seeds; the release keys 0004 deferred; and
-- the publish operation's natural key.

-- A release's rows are written in the transaction that writes the release (§3), as a revision's
-- are (0009): this refuses an INSERT that would add to a committed release, with make_immutable's
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
-- Kubernetes version; each machine's validation mode is its row's.
CREATE TABLE release (
  id                 text PRIMARY KEY CHECK (id ~ '^rel_[a-z2-7]{26}$'),
  cluster            text NOT NULL REFERENCES cluster (id),
  draft              text NOT NULL,
  draft_revision     integer NOT NULL CHECK (draft_revision >= 1),
  digest             bytea NOT NULL CHECK (length(digest) = 32),
  contract           text NOT NULL CHECK (contract ~ '^v[0-9]{1,4}\.[0-9]{1,4}$'),
  machinery_version  text NOT NULL CHECK (machinery_version ~ '^v[0-9]{1,4}\.[0-9]{1,4}\.[0-9]{1,4}(-[0-9A-Za-z.-]{1,64})?$'),
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
  FOREIGN KEY (operation, operation_kind) REFERENCES operation (id, kind)
);
CALL make_immutable('release');
CREATE TRIGGER writer BEFORE INSERT ON release FOR EACH ROW EXECUTE FUNCTION stamp_revision_writer();

-- A machine of a release (§3; compilation §11): its import base and assignment revisions, the
-- mode it was validated in, its artifact ciphertext and the ciphertext's SHA-256, the SHA-256 of
-- the plaintext configuration (§1.1), and its review data: the redacted configuration and the
-- provenance records (compilation §8.2, §8.3), which hold no value. A configuration that could
-- not be redacted is NULL: it shows nothing and says so (§8.3).
CREATE TABLE release_machine (
  release              text NOT NULL,
  cluster              text NOT NULL,
  machine              text NOT NULL,
  import_base_revision text NOT NULL,
  assignment_revision  text,
  mode                 text NOT NULL CHECK (mode IN ('metal', 'container', 'cloud')),
  ciphertext           text NOT NULL CHECK (ciphertext ~ '^vault:v[1-9][0-9]*:[A-Za-z0-9+/]+={0,2}$'),
  ciphertext_digest    bytea NOT NULL CHECK (length(ciphertext_digest) = 32),
  configuration_digest bytea NOT NULL CHECK (length(configuration_digest) = 32),
  redacted             text CHECK (redacted <> ''),
  provenance           jsonb NOT NULL CHECK (jsonb_typeof(provenance) = 'array'),
  PRIMARY KEY (release, machine),
  FOREIGN KEY (release, cluster) REFERENCES release (id, cluster),
  FOREIGN KEY (machine, cluster) REFERENCES machine (id, cluster),
  FOREIGN KEY (import_base_revision, machine) REFERENCES import_base_revision (id, machine),
  FOREIGN KEY (assignment_revision, cluster, machine) REFERENCES assignment_revision (id, cluster, machine)
);
CALL make_immutable('release_machine');
CREATE TRIGGER with_release BEFORE INSERT ON release_machine
  FOR EACH ROW EXECUTE FUNCTION refuse_late_release_row();

-- A release names each head it used once, as its sources (§6.2): the revision (NULL: removed) and
-- the head revision the release left it at. A head the release introduces is inserted later in the
-- same transaction, so the head keys are checked at commit.
ALTER TABLE fragment ADD UNIQUE (id, cluster, name);
ALTER TABLE profile ADD UNIQUE (id, cluster, name);
ALTER TABLE assignment ADD UNIQUE (id, cluster, machine);
CREATE TABLE release_source (
  release             text NOT NULL,
  cluster             text NOT NULL,
  kind                text NOT NULL,
  fragment            text,
  profile             text,
  assignment          text,
  name                source_name,
  machine             text,
  fragment_revision   text,
  profile_revision    text,
  assignment_revision text,
  head_revision       integer NOT NULL CHECK (head_revision >= 1),
  CONSTRAINT release_source_shape CHECK (
    (kind = 'fragment' AND fragment IS NOT NULL AND name IS NOT NULL AND profile IS NULL AND assignment IS NULL
      AND machine IS NULL AND profile_revision IS NULL AND assignment_revision IS NULL) OR
    (kind = 'profile' AND profile IS NOT NULL AND name IS NOT NULL AND fragment IS NULL AND assignment IS NULL
      AND machine IS NULL AND fragment_revision IS NULL AND assignment_revision IS NULL) OR
    (kind = 'assignment' AND assignment IS NOT NULL AND machine IS NOT NULL AND fragment IS NULL AND profile IS NULL
      AND name IS NULL AND fragment_revision IS NULL AND profile_revision IS NULL)),
  UNIQUE (release, fragment),
  UNIQUE (release, profile),
  UNIQUE (release, assignment),
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

-- The last classification of each provider object version a release depends on (dependency
-- monitor §5.1): the only mutable dependency table, updated under its row lock. Its object is a
-- KV generation path (0004's reference rows) or a Transit key name (as configuration accepts one:
-- one URL path segment of at most 227 bytes). A retained version has no reason, and every other
-- class one; only an unknown one has the time it became unknown.
CREATE TABLE dependency_status (
  id                    text PRIMARY KEY CHECK (id ~ '^dep_[a-z2-7]{26}$'),
  provider              text NOT NULL CHECK (provider IN ('kv', 'transit')),
  object                text NOT NULL,
  version               bigint NOT NULL CHECK (version >= 1),
  class                 text NOT NULL CHECK (class IN ('retained', 'blocked', 'lost', 'unknown')),
  reason                text CHECK (reason ~ '^[a-z]+(-[a-z]+)*$'),
  first_retained_at     timestamptz,
  unknown_since         timestamptz,
  persistent_alerted_at timestamptz,
  deletion_observed     timestamptz,
  deletion_warned       timestamptz,
  observed_from         timestamptz NOT NULL,
  recorded_at           timestamptz NOT NULL,
  answer_date           timestamptz,
  UNIQUE (provider, object, version),
  CONSTRAINT dependency_status_object CHECK (
    (provider = 'kv' AND object ~ '^gen/cl_[a-z2-7]{26}/ing_[a-z2-7]{26}/[A-Za-z0-9_-]{1,128}$') OR
    (provider = 'transit' AND object NOT IN ('.', '..') AND octet_length(object) BETWEEN 1 AND 227
      AND object !~ '[/#?%\\[:space:][:cntrl:]]')),
  CONSTRAINT dependency_status_reason CHECK ((class = 'retained') = (reason IS NULL)),
  CONSTRAINT dependency_status_unknown_since CHECK ((class = 'unknown') = (unknown_since IS NOT NULL))
);

-- A release machine's dependency records (compilation §9): each effective dependency, each
-- reproduction dependency (a reference occurrence in its source revision, by its redacted path)
-- and the artifact's encryption dependency. Each names a provider object version by the creation
-- time the provider gave it, kept as RFC 3339 text: a KV created_time has nanoseconds, which a
-- timestamp would round.
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
  path            text CHECK (path <> '' AND octet_length(path) <= 4096),
  CONSTRAINT dependency_shape CHECK (
    (kind = 'encryption' AND provider = 'transit' AND reference IS NULL AND source_revision IS NULL
      AND source_digest IS NULL AND path IS NULL) OR
    (kind = 'effective' AND provider = 'kv' AND reference IS NOT NULL AND source_revision IS NULL
      AND source_digest IS NULL AND path IS NULL) OR
    (kind = 'reproduction' AND provider = 'kv' AND reference IS NOT NULL AND source_revision IS NOT NULL
      AND source_digest IS NOT NULL AND path IS NOT NULL)),
  FOREIGN KEY (release, machine) REFERENCES release_machine (release, machine),
  FOREIGN KEY (provider, object, version) REFERENCES dependency_status (provider, object, version)
);
CREATE UNIQUE INDEX dependency_effective ON dependency (release, machine, reference, object, version)
  WHERE kind = 'effective';
CREATE UNIQUE INDEX dependency_reproduction ON dependency (release, machine, source_revision, path)
  WHERE kind = 'reproduction';
CREATE UNIQUE INDEX dependency_encryption ON dependency (release, machine) WHERE kind = 'encryption';
CALL make_immutable('dependency');
CREATE TRIGGER with_release BEFORE INSERT ON dependency
  FOR EACH ROW EXECUTE FUNCTION refuse_late_release_row();

-- A draft names the release that published it, and only then (§6.2).
ALTER TABLE draft
  ADD COLUMN release text,
  ADD FOREIGN KEY (release, id) REFERENCES release (id, draft),
  ADD CONSTRAINT draft_published_release CHECK ((state = 'published') = (release IS NOT NULL));

-- 0004 deferred these keys until the release table existed: Desired and Applied each name a
-- release that covers the machine.
ALTER TABLE machine_state
  ADD FOREIGN KEY (desired, machine) REFERENCES release_machine (release, machine),
  ADD FOREIGN KEY (applied_release, machine) REFERENCES release_machine (release, machine);

-- §7.3: at most one queued or running publish operation per draft revision, over kind = 'publish'
-- only, so a publish and an ingest of one draft revision never collide.
CREATE UNIQUE INDEX operation_active_publish ON operation (draft, draft_revision)
  WHERE kind = 'publish' AND state IN ('queued', 'running');
