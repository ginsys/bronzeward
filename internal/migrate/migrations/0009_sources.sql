-- 0009 sources (persistence-api.md §3, §3.1, choice §17.31): fragment, profile and assignment
-- revisions and their heads, and a draft's entries for them. Heads are inserted and advanced only
-- by the publication commit (§6.2, T3); a draft update inserts revisions and sets entries (T1).
-- The PoC accepts cluster scope only: library scope (choice §17.28) is refused here.

-- A fragment or profile name.
CREATE DOMAIN source_name AS text
  CONSTRAINT source_name_form CHECK (VALUE ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?$' AND octet_length(VALUE) <= 63);

-- The six fragment layers, in composition order: design §6.2's seven minus machine-intrinsic,
-- which is the import base (compilation §6).
CREATE DOMAIN fragment_layer AS text
  CONSTRAINT fragment_layer_known CHECK (VALUE IN ('global', 'site', 'cluster', 'role', 'workload', 'override'));

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

-- A fragment revision (§3; compilation §2, §5): its sanitized document, written by a draft update.
-- It names its fragment by cluster and name, since the head may not exist until publication.
CREATE TABLE fragment_revision (
  id         text PRIMARY KEY CHECK (id ~ '^frv_[a-z2-7]{26}$'),
  cluster    text NOT NULL REFERENCES cluster (id),
  name       source_name NOT NULL,
  layer      fragment_layer NOT NULL,
  document   text NOT NULL CHECK (document <> ''),
  author     text NOT NULL REFERENCES principal (id),
  created_at timestamptz NOT NULL,
  writer     xid8 NOT NULL,
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
-- name (and, for a fragment, layer); a removal sets the pointer to NULL and keeps the head.
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
