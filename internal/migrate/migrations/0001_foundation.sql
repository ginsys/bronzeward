-- 0001 foundation (persistence-api.md §3, §11, §12.1). Later tables arrive with the migration
-- of the change that first uses them.

-- An immutable table refuses UPDATE, DELETE and TRUNCATE (§3, choice §17.3), with SQLSTATE BW001
-- so a test can tell this refusal from any other error.
CREATE FUNCTION refuse_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'table % is immutable: % refused', TG_TABLE_NAME, TG_OP USING ERRCODE = 'BW001';
END
$$;

-- Every immutable table is made so by this call, in the migration that creates it.
CREATE PROCEDURE make_immutable(t regclass) LANGUAGE plpgsql AS $$
BEGIN
  EXECUTE format('CREATE TRIGGER immutable_rows BEFORE UPDATE OR DELETE ON %s FOR EACH ROW EXECUTE FUNCTION refuse_mutation()', t);
  EXECUTE format('CREATE TRIGGER immutable_truncate BEFORE TRUNCATE ON %s FOR EACH STATEMENT EXECUTE FUNCTION refuse_mutation()', t);
END
$$;

CALL make_immutable('schema_migrations');

-- One row per epoch (§12.1): the installation's, then one per recovery-mode entry. entered_by
-- is NULL for the installation epoch; its reference to the principal table is added with that
-- table. Entry's restored-backup rows arrive with recovery mode.
CREATE TABLE recovery_epoch (
  epoch      text PRIMARY KEY CHECK (epoch ~ '^ep_[a-z2-7]{26}$'),
  entered_at timestamptz NOT NULL,
  entered_by text
);
CALL make_immutable('recovery_epoch');

-- The single installation row (§12.1): the current epoch, recovery mode, the schema version.
CREATE TABLE installation_state (
  singleton      boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  epoch          text NOT NULL REFERENCES recovery_epoch (epoch),
  recovery_mode  boolean NOT NULL DEFAULT false,
  schema_version integer NOT NULL
);
