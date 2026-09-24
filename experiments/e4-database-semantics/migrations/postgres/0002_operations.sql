-- One durable operation intent per idempotency key, and at most one active operation per machine
-- scope (docs/spec/execution-recovery.md §3.2 item 4). owner_gen is the fencing token.
CREATE TABLE operation (
  id        BIGSERIAL PRIMARY KEY,
  machine   TEXT NOT NULL,
  idem_key  TEXT NOT NULL UNIQUE,
  state     TEXT NOT NULL,
  owner     TEXT NOT NULL,
  owner_gen BIGINT NOT NULL,
  attempts  BIGINT NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX operation_active_scope ON operation (machine)
  WHERE state IN ('committed', 'sending', 'verifying', 'unresolved');
-- Append-only: every intent, takeover and attempt, in commit order of its transaction's insert.
CREATE TABLE timeline (
  seq          BIGSERIAL PRIMARY KEY,
  operation_id BIGINT NOT NULL REFERENCES operation (id),
  kind         TEXT NOT NULL,
  actor        TEXT NOT NULL,
  owner_gen    BIGINT NOT NULL
);
