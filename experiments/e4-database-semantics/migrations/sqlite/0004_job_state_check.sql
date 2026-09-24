-- e4:foreign-keys-off
-- SQLite cannot add a CHECK constraint to an existing table. The documented procedure rebuilds
-- it: create the new table, copy, drop the old one, rename, recreate its indexes. job_event
-- references job, so the drop needs foreign-key enforcement off, which SQLite ignores inside a
-- transaction; the runner turns it off on the connection before BEGIN and runs
-- foreign_key_check before COMMIT (migrate.go).
CREATE TABLE job_new (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  state           TEXT NOT NULL CHECK (state IN ('pending', 'claimed', 'done')),
  claimed_by      TEXT,
  fence           INTEGER NOT NULL DEFAULT 0,
  lease_until     INTEGER,
  completed_by    TEXT,
  completed_fence INTEGER
);
INSERT INTO job_new (id, state, claimed_by, fence, lease_until, completed_by, completed_fence)
  SELECT id, state, claimed_by, fence, lease_until, completed_by, completed_fence FROM job;
DROP TABLE job;
ALTER TABLE job_new RENAME TO job;
CREATE INDEX job_state ON job (state, id);
