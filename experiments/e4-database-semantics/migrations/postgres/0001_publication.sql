-- A fragment carries its current revision. Every accepted fragment change and every publication
-- appends to ledger in its own transaction; ledger.seq orders them, and it is what the readers use
-- to tell which writer won which revision and whether a release committed on a superseded source.
CREATE TABLE fragment (
  name     TEXT PRIMARY KEY,
  revision BIGINT NOT NULL,
  body     TEXT NOT NULL
);
CREATE TABLE ledger (
  seq     BIGSERIAL PRIMARY KEY,
  kind    TEXT NOT NULL,
  subject TEXT NOT NULL,
  number  BIGINT NOT NULL,
  actor   TEXT NOT NULL
);
CREATE TABLE release (
  id             BIGSERIAL PRIMARY KEY,
  name           TEXT NOT NULL UNIQUE,
  digest         TEXT NOT NULL,
  artifact_count BIGINT NOT NULL,
  publisher      TEXT NOT NULL
);
CREATE TABLE release_source (
  release_id BIGINT NOT NULL REFERENCES release (id),
  fragment   TEXT NOT NULL REFERENCES fragment (name),
  revision   BIGINT NOT NULL,
  PRIMARY KEY (release_id, fragment)
);
CREATE TABLE release_artifact (
  release_id BIGINT NOT NULL REFERENCES release (id),
  machine    TEXT NOT NULL,
  ciphertext BYTEA NOT NULL,
  PRIMARY KEY (release_id, machine)
);
