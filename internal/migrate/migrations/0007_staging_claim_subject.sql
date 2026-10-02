-- 0007 Staging claim subject (compilation.md §2.3 step 6, §3.1, §3.4): the cluster a claim's
-- provider generations are created under (gen/<cluster>/<claim id>/<value id>), the machine it
-- imports, and the digest a resume checks its decrypted envelope against.

-- Bronzeward is unreleased, so this migration supports no earlier database (persistence-api §11
-- rule 6): on one holding claims, PostgreSQL refuses the NOT NULL columns and the operator
-- recreates the database.
ALTER TABLE staging_claim
  ADD COLUMN cluster text NOT NULL REFERENCES cluster (id),
  ADD COLUMN machine text NOT NULL,
  -- An import only: drift adoption (execution and recovery §6) arrives with its own issue.
  ADD COLUMN kind text NOT NULL CHECK (kind = 'import'),
  -- SHA-256 of the envelope's plaintext, present exactly when the payload is.
  ADD COLUMN payload_digest bytea CHECK (octet_length(payload_digest) = 32),
  ADD FOREIGN KEY (machine, cluster) REFERENCES machine (id, cluster),
  ADD CHECK ((payload IS NULL) = (payload_digest IS NULL));

-- The sweep's scan (compilation §3.5): the live claims, by the time they become due.
CREATE INDEX staging_claim_live_expiry ON staging_claim (expires_at) WHERE state IN ('held', 'resumed');
