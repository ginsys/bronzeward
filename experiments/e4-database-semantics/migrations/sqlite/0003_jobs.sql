-- A work queue. fence is incremented by every claim; a completion must present the fence it
-- claimed with. job_event is the independent claim record: two claims of one job with no expiry
-- between them is a double claim.
CREATE TABLE job (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  state           TEXT NOT NULL,
  claimed_by      TEXT,
  fence           INTEGER NOT NULL DEFAULT 0,
  lease_until     INTEGER,
  completed_by    TEXT,
  completed_fence INTEGER
);
CREATE INDEX job_state ON job (state, id);
CREATE TABLE job_event (
  seq    INTEGER PRIMARY KEY AUTOINCREMENT,
  job_id INTEGER NOT NULL REFERENCES job (id),
  worker TEXT NOT NULL,
  fence  INTEGER NOT NULL,
  action TEXT NOT NULL
);
