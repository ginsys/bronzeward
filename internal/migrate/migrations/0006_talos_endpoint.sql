-- 0006 Talos endpoint (persistence-api.md §3.3, choice §17.29; execution-recovery.md §4.1): each
-- machine's Talos endpoint, the machine timeline its changes are entries on, and the
-- MachineEndpointChange record.

-- An endpoint as ParseEndpoint (internal/talos) returns it: an IPv4 literal or a bracketed IPv6
-- literal, then a port from 1 to 65535. The application holds the grammar; this is the shape a
-- writer that bypassed it cannot get past (no DNS name, scheme, path, user part or whitespace).
CREATE DOMAIN talos_endpoint AS text
  CONSTRAINT talos_endpoint_form CHECK (
    octet_length(VALUE) <= 64
    AND VALUE ~ '^([0-9]{1,3}(\.[0-9]{1,3}){3}|\[[0-9a-f:.]+\]):[1-9][0-9]{0,4}$'
    AND substring(VALUE FROM ':([0-9]+)$')::integer <= 65535);

-- Every machine has an endpoint, with no default. Bronzeward is unreleased, so this migration
-- supports no earlier database (§11 rule 6): on one holding machines, PostgreSQL refuses the NOT
-- NULL column and the operator recreates the database.
ALTER TABLE machine ADD COLUMN talos_endpoint talos_endpoint NOT NULL;

-- A machine's timeline (§3 TimelineEvent; execution and recovery §4.1): its plans, operations and
-- machine-scope facts, each at a revision T7 allocates from the machine's revision counter under
-- its row lock, with the epoch it was appended in. Kinds are added by the migrations of the issues
-- that write them; an endpoint change is the first.
CREATE TABLE machine_event (
  machine  text NOT NULL REFERENCES machine (id),
  revision bigint NOT NULL CHECK (revision >= 1),
  epoch    text NOT NULL REFERENCES recovery_epoch (epoch),
  kind     text NOT NULL CONSTRAINT machine_event_kind CHECK (kind IN ('endpoint-change')),
  -- A JSON object: JSON null is not SQL NULL, and an immutable entry cannot be corrected later.
  entry    jsonb NOT NULL CHECK (jsonb_typeof(entry) = 'object'),
  at       timestamptz NOT NULL,
  PRIMARY KEY (machine, revision),
  UNIQUE (machine, revision, kind)
);
CALL make_immutable('machine_event');

-- A replaced endpoint (§3.3): the previous and new endpoint and the act that replaced it, which
-- holds who, role, epoch and time (§10.5). It is exactly one endpoint-change entry on the
-- machine's timeline, keyed by that entry's revision, so the record and the entry cannot part. The
-- act is written after the effect in the same transaction, so its reference is checked at commit.
CREATE TABLE machine_endpoint_change (
  machine           text NOT NULL,
  revision          bigint NOT NULL,
  kind              text NOT NULL DEFAULT 'endpoint-change' CHECK (kind = 'endpoint-change'),
  previous_endpoint talos_endpoint NOT NULL,
  new_endpoint      talos_endpoint NOT NULL,
  act               text NOT NULL UNIQUE REFERENCES act (id) DEFERRABLE INITIALLY DEFERRED,
  CONSTRAINT machine_endpoint_change_pkey PRIMARY KEY (machine, revision),
  CONSTRAINT machine_endpoint_change_entry FOREIGN KEY (machine, revision, kind)
    REFERENCES machine_event (machine, revision, kind),
  CONSTRAINT machine_endpoint_change_changes CHECK (previous_endpoint <> new_endpoint)
);
CALL make_immutable('machine_endpoint_change');
