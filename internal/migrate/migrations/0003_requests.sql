-- 0003 requests (persistence-api.md §7, §10.4): the idempotency record every committed mutating
-- request writes with its effect, and identity revocation's immutable row (T5c).

CREATE TABLE idempotency_record (
  principal text NOT NULL REFERENCES principal (id),
  key text NOT NULL CHECK (key ~ '^[A-Za-z0-9_-]{16,128}$'),
  -- SHA-256, or HMAC-SHA-256 for a keyed route (§7.1), whose key identity and version
  -- fingerprint_key names; empty for SHA-256.
  fingerprint bytea NOT NULL CHECK (length(fingerprint) = 32),
  fingerprint_key text NOT NULL DEFAULT '',
  request_id text NOT NULL CHECK (request_id ~ '^req_[a-z2-7]{26}$'),
  epoch text NOT NULL REFERENCES recovery_epoch (epoch),
  -- The response to replay; never a secret value (design §11.1).
  status integer NOT NULL CHECK (status BETWEEN 200 AND 599),
  location text,
  etag text,
  body bytea NOT NULL,
  -- A 202's operation; its foreign key joins with the operation table.
  operation_id text CHECK (operation_id ~ '^op_[a-z2-7]{26}$'),
  created_at timestamptz NOT NULL,
  PRIMARY KEY (principal, key)
);
CALL make_immutable('idempotency_record');

-- One per identity: a revocation is permanent (§10.4). The act is written after the effect in the
-- same transaction, so its reference is checked at commit.
CREATE TABLE identity_revocation (
  identity text PRIMARY KEY REFERENCES principal (id),
  revoked_by text NOT NULL,
  revoked_by_kind text NOT NULL GENERATED ALWAYS AS ('human') STORED,
  role text NOT NULL CHECK (role = 'recovery-admin'),
  reason text NOT NULL CHECK (btrim(reason) <> '' AND octet_length(reason) <= 1024),
  act text NOT NULL UNIQUE REFERENCES act (id) DEFERRABLE INITIALLY DEFERRED,
  epoch text NOT NULL REFERENCES recovery_epoch (epoch),
  at timestamptz NOT NULL,
  FOREIGN KEY (revoked_by, revoked_by_kind) REFERENCES principal (id, kind)
);
CALL make_immutable('identity_revocation');
