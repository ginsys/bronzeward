-- 0012 Release dependency bindings (compilation.md §9; dependency-monitor.md §5.2, §6.1;
-- persistence-api.md §3, §6.2): an encryption dependency names its machine's key, an effective
-- dependency a declared occurrence of its machine's sources, a status is recorded after it was
-- observed, and the dependency rows of a status are found by its identity.

-- Bronzeward is unreleased, so this migration supports no earlier database (persistence-api §11
-- rule 6): on one holding a release, PostgreSQL refuses the NOT NULL column and the operator
-- recreates the database.

-- A release machine records the Transit key its ciphertext was encrypted under, which the
-- ciphertext names only by version (compilation §9), and its encryption dependency names that key
-- at that version: not another key retained at the same version, which the monitor would report
-- while the artifact's own key is blocked or lost (dependency monitor §3). The key name's form is
-- its status row's (0011), which the dependency's status key binds.
ALTER TABLE release_machine ADD COLUMN key_name text NOT NULL;
ALTER TABLE dependency DROP CONSTRAINT dependency_release_machine_key_version_fkey;
ALTER TABLE release_machine
  DROP CONSTRAINT release_machine_release_machine_key_version_key,
  ADD UNIQUE (release, machine, key_name, key_version);
ALTER TABLE dependency
  ADD COLUMN key_name text GENERATED ALWAYS AS (CASE WHEN kind = 'encryption' THEN object END) STORED,
  ADD CONSTRAINT dependency_encryption_key FOREIGN KEY (release, machine, key_name, key_version)
    REFERENCES release_machine (release, machine, key_name, key_version);

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

-- A class is recorded after the request that observed it began (dependency monitor §5.2, §6.1):
-- publication's re-check compares recorded_at with the time it began classifying, so a row
-- recorded before it was observed could date a transition before a publication it raced.
ALTER TABLE dependency_status ADD CONSTRAINT dependency_status_times CHECK (recorded_at >= observed_from);

-- The monitor reads the releases that reference a dependency by its status (dependency monitor
-- §6.1 step 5), and every other index on dependency leads with the release.
CREATE INDEX dependency_status_identity ON dependency (provider, object, version, created);
