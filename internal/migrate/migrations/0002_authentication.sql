-- 0002 authentication (persistence-api.md §10): principals, automation tokens, and the act
-- record, whose first writer is the token tool (§10.2, §10.5).

-- A human is (iss, sub), created on first admitted use (§10); a service identity has a name and a
-- responsible human (§10.2, choice §17.18) and is created by the token tool. No row stores roles:
-- a human's come from each token, a service identity's from its token row. revoked is set once,
-- by identity revocation (§10.4, T5c).
CREATE TABLE principal (
  id          text PRIMARY KEY CHECK (id ~ '^idn_[a-z2-7]{26}$'),
  kind        text NOT NULL CHECK (kind IN ('human', 'service')),
  iss         text,
  sub         text,
  name        text,
  responsible text REFERENCES principal (id),
  created_at  timestamptz NOT NULL,
  revoked     boolean NOT NULL DEFAULT false,
  UNIQUE (id, kind),
  -- IS NOT NULL is spelled out: a CHECK passes when its expression is NULL.
  CHECK ((kind = 'human' AND iss IS NOT NULL AND sub IS NOT NULL AND iss <> '' AND sub <> ''
          AND name IS NULL AND responsible IS NULL)
      OR (kind = 'service' AND iss IS NULL AND sub IS NULL AND name IS NOT NULL AND name <> ''
          AND responsible IS NOT NULL))
);
-- The arbiter of §10's INSERT ... ON CONFLICT (iss, sub) DO NOTHING. Service rows have NULL
-- iss and sub, which never conflict.
CREATE UNIQUE INDEX principal_human ON principal (iss, sub);
CREATE UNIQUE INDEX principal_service_name ON principal (name);

-- 0001 deferred this reference until the table existed.
ALTER TABLE recovery_epoch ADD FOREIGN KEY (entered_by) REFERENCES principal (id);

-- Only SHA-256 of the secret is stored (§10.2). Automation never holds approver or
-- recovery-admin (design §13.7 item 2). Expiry is at most 90 days: 2160 hours, not '90 days',
-- so a DST change cannot make the bound differ from the tool's.
CREATE TABLE automation_token (
  id            text PRIMARY KEY CHECK (id ~ '^tok_[a-z2-7]{26}$'),
  -- Issuance order. Tokens are inserted under the principal lock, so seq follows the order in
  -- which an identity's tokens replaced each other; issued_at (a transaction's start) need not.
  seq           bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
  owner        text NOT NULL REFERENCES principal (id),
  secret_sha256 bytea NOT NULL CHECK (length(secret_sha256) = 32),
  roles         text[] NOT NULL CHECK (cardinality(roles) > 0
                  AND roles <@ ARRAY['viewer', 'author', 'publisher']::text[]),
  epoch         text NOT NULL REFERENCES recovery_epoch (epoch),
  issued_at     timestamptz NOT NULL,
  expires_at    timestamptz NOT NULL CHECK (expires_at > issued_at
                  AND expires_at <= issued_at + interval '2160 hours'),
  -- No CHECK against issued_at: now() is the transaction's start, and a revocation that began
  -- before a rotation it then waited on writes a revoked_at earlier than that token's issued_at.
  revoked_at    timestamptz
);
-- One valid token per identity: at most one unrevoked, expired or not. It backs the principal
-- row lock every issuing transaction takes (§10.2).
CREATE UNIQUE INDEX automation_token_one_unrevoked ON automation_token (owner) WHERE revoked_at IS NULL;

-- Every mutating act (§10.5). via 'api' is a request, with its role and request id; via 'tool'
-- is the operator's token command, which exercises no API role (§10.2). seq orders acts inside
-- one database state and never leaves it (§2).
CREATE TABLE act (
  id              text PRIMARY KEY CHECK (id ~ '^act_[a-z2-7]{26}$'),
  seq             bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
  principal       text NOT NULL,
  principal_kind  text NOT NULL,
  via             text NOT NULL CHECK (via IN ('api', 'tool')),
  role            text CHECK (role IN ('viewer', 'author', 'publisher', 'approver', 'recovery-admin')),
  action          text NOT NULL CHECK (action <> ''),
  subjects        text[] NOT NULL,
  idempotency_key text,
  request_id      text CHECK (request_id ~ '^req_[a-z2-7]{26}$'),
  epoch           text NOT NULL REFERENCES recovery_epoch (epoch),
  at              timestamptz NOT NULL,
  FOREIGN KEY (principal, principal_kind) REFERENCES principal (id, kind),
  CHECK ((via = 'api' AND role IS NOT NULL AND request_id IS NOT NULL)
      OR (via = 'tool' AND role IS NULL AND request_id IS NULL AND idempotency_key IS NULL))
);
CALL make_immutable('act');
