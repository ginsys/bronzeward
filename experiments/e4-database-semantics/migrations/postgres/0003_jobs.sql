-- A work queue. fence is incremented by every claim; a completion must present the fence it
-- claimed with. job_event is the independent claim record: two claims of one job with no expiry
-- between them is a double claim.
CREATE TABLE job (
  id              BIGSERIAL PRIMARY KEY,
  state           TEXT NOT NULL,
  claimed_by      TEXT,
  fence           BIGINT NOT NULL DEFAULT 0,
  lease_until     BIGINT,
  completed_by    TEXT,
  completed_fence BIGINT
);
CREATE INDEX job_state ON job (state, id);
CREATE TABLE job_event (
  seq    BIGSERIAL PRIMARY KEY,
  job_id BIGINT NOT NULL REFERENCES job (id),
  worker TEXT NOT NULL,
  fence  BIGINT NOT NULL,
  action TEXT NOT NULL
);
