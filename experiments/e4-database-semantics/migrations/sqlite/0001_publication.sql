-- A fragment carries its current revision. Every accepted fragment change and every publication
-- appends to ledger in its own transaction; ledger.seq orders them, and it is what the readers use
-- to tell which writer won which revision and whether a release committed on a superseded source.
CREATE TABLE fragment (
  name     TEXT PRIMARY KEY,
  revision INTEGER NOT NULL,
  body     TEXT NOT NULL
);
-- AUTOINCREMENT, here and below: without it SQLite may reuse the largest rowid after a delete,
-- where a PostgreSQL sequence never goes back.
CREATE TABLE ledger (
  seq     INTEGER PRIMARY KEY AUTOINCREMENT,
  kind    TEXT NOT NULL,
  subject TEXT NOT NULL,
  number  INTEGER NOT NULL,
  actor   TEXT NOT NULL
);
CREATE TABLE release (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  name           TEXT NOT NULL UNIQUE,
  digest         TEXT NOT NULL,
  artifact_count INTEGER NOT NULL,
  publisher      TEXT NOT NULL
);
CREATE TABLE release_source (
  release_id INTEGER NOT NULL REFERENCES release (id),
  fragment   TEXT NOT NULL REFERENCES fragment (name),
  revision   INTEGER NOT NULL,
  PRIMARY KEY (release_id, fragment)
);
CREATE TABLE release_artifact (
  release_id INTEGER NOT NULL REFERENCES release (id),
  machine    TEXT NOT NULL,
  ciphertext BLOB NOT NULL,
  PRIMARY KEY (release_id, machine)
);
