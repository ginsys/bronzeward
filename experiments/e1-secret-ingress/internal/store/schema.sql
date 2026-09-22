-- Phase-0 evidence code for the secret-ingress feasibility experiment
-- (ginsys/bronzeward issue 2). This is not the v1 schema.
--
-- The tables exist to put bytes where design §7.1 says persistence happens, so that the
-- investigation fixtures' leak scan can look for them: an ordinary draft row, a parsed index, the
-- staging row the two alternatives are compared over, and the encrypted baseline. Nothing here is
-- a proposal for how v1 stores anything; the database decisions belong to their own work items.
--
-- There is no migration framework and no versioning. Every statement is CREATE ... IF NOT EXISTS,
-- so applying the file twice is a no-op, and the experiment's runs each use a fresh run_id rather
-- than mutating what an earlier run wrote.

-- The sanitized draft. Design §7.1 calls this the ordinary plaintext persistence that extraction
-- must precede, so it is the first place a leak would appear.
CREATE TABLE IF NOT EXISTS machine_draft (
    id              BIGSERIAL PRIMARY KEY,
    run_id          TEXT        NOT NULL,
    source          TEXT        NOT NULL,
    document        TEXT        NOT NULL,
    document_sha256 TEXT        NOT NULL,
    -- clock_timestamp() rather than now(): now() is the transaction's start time, and this column
    -- exists to be an observer independent of the prototype's own clock. A value that is really
    -- the transaction's start would silently agree with whatever the prototype recorded.
    created_at      TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

-- The parsed index. §7.1 names it separately from the draft because a system can sanitize the
-- document it stores and then index the values it parsed out of the original.
CREATE TABLE IF NOT EXISTS parsed_index (
    id         BIGSERIAL PRIMARY KEY,
    run_id     TEXT        NOT NULL,
    path       TEXT        NOT NULL,
    value      TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (run_id, path)
);

-- Where each extracted secret went. It holds a provider URI and a digest, never a value, and is
-- what a later resolution step would read.
CREATE TABLE IF NOT EXISTS secret_reference (
    id         BIGSERIAL PRIMARY KEY,
    run_id     TEXT        NOT NULL,
    path       TEXT        NOT NULL,
    uri        TEXT        NOT NULL,
    digest     TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (run_id, path)
);

-- The ordering journal, mirrored into the database so that PostgreSQL's own clock and WAL position
-- can be recorded beside each record. journal.jsonl on disk holds the same fields under the same
-- names; the two are compared without a mapping table, and disagreement between them is itself a
-- finding.
CREATE TABLE IF NOT EXISTS ingest_journal (
    run_id         TEXT        NOT NULL,
    seq            INTEGER     NOT NULL,
    event          TEXT        NOT NULL,
    checkpoint     TEXT,
    surface        TEXT,
    payload_sha256 TEXT,
    secret_digests TEXT[]      NOT NULL DEFAULT '{}',
    detail         TEXT,
    -- The prototype's own clocks, as recorded on disk.
    wall           TIMESTAMPTZ NOT NULL,
    mono_ns        BIGINT      NOT NULL,
    -- The server's, which the prototype does not supply and cannot backdate.
    server_clock   TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    server_lsn     PG_LSN      NOT NULL DEFAULT pg_current_wal_lsn(),
    PRIMARY KEY (run_id, seq)
);

-- The staging claim. One table for both staging alternatives, deliberately: the comparison is
-- only like-for-like if ownership, expiry and resumption are expressed in the same columns, and
-- the difference is then visible as which columns each mode uses and what it puts in payload.
--
-- payload is NULL for the transient mode, which keeps the pending change in process memory and so
-- puts nothing in the write-ahead log, and ciphertext for the encrypted mode, which puts a row
-- there on purpose. That asymmetry is the finding the experiment is built to produce, not an
-- oversight: what reaches the log is exactly what the two modes differ on.
CREATE TABLE IF NOT EXISTS staging_claim (
    run_id            TEXT PRIMARY KEY,
    mode              TEXT        NOT NULL CHECK (mode IN ('transient', 'encrypted')),
    -- Who holds it. The transient mode identifies a process; the encrypted mode identifies a
    -- principal, which is what lets a different one resume.
    owner_principal   TEXT        NOT NULL,
    owner_pid         INTEGER,
    -- The owning process's start time, read from the kernel. A PID alone is not an identity:
    -- PIDs are reused, and a new process inheriting a dead one's claim is the failure this
    -- column exists to make impossible.
    owner_start_token TEXT,
    payload           TEXT,
    payload_sha256    TEXT        NOT NULL,
    resume_checkpoint TEXT        NOT NULL,
    state             TEXT        NOT NULL CHECK (state IN ('held', 'resumed', 'released', 'abandoned')),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    heartbeat_at      TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    expires_at        TIMESTAMPTZ NOT NULL
);

-- The encrypted baseline: the observed configuration retained as ciphertext, which §7.1 permits,
-- rather than as the plaintext draft it forbids.
CREATE TABLE IF NOT EXISTS encrypted_baseline (
    id           BIGSERIAL PRIMARY KEY,
    run_id       TEXT        NOT NULL UNIQUE,
    key_name     TEXT        NOT NULL,
    input_sha256 TEXT        NOT NULL,
    input_bytes  INTEGER     NOT NULL,
    ciphertext   TEXT        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
