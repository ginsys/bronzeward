-- 0005 operation fence (execution-recovery.md §3.2, §3.4; persistence-api.md §3 TimelineEvent):
-- two rules 0004 left to the writers, enforced before the first writer of either table lands.

-- §3.2 creates an apply-config operation in `committed` owned by the committing instance at
-- generation 1, and §3.4's takeover and attempt comparisons read that owner until the operation is
-- terminal. 0004's pairing check makes an owner carry its epoch and a generation of at least 1.
-- No lease: no timer takes an apply-config operation over (choice §10.7).
ALTER TABLE operation ADD CONSTRAINT operation_apply_config_owned
  CHECK (kind <> 'apply-config' OR state NOT IN ('committed', 'sending', 'verifying', 'unresolved')
    OR owner IS NOT NULL);

-- An operation event is a publish or ingest operation's timeline entry; apply-config and adopt
-- operations are on the machine timeline. The event names its operation's kind and the key on
-- (operation, kind) holds it to the truth, which also keeps an operation with events from changing
-- kind. No code writes operation_event before this migration, so an installation holding events
-- is refused rather than given a kind it never recorded.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM operation_event) THEN
    RAISE EXCEPTION '0005: operation_event holds rows written before events named their operation''s kind';
  END IF;
END
$$;
ALTER TABLE operation ADD CONSTRAINT operation_id_kind UNIQUE (id, kind);
ALTER TABLE operation_event ADD COLUMN kind text NOT NULL
  CONSTRAINT operation_event_job_kind CHECK (kind IN ('publish', 'ingest'));
ALTER TABLE operation_event ADD CONSTRAINT operation_event_operation_kind
  FOREIGN KEY (operation, kind) REFERENCES operation (id, kind);
