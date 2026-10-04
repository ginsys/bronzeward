-- 0010 Draft-update claims (compilation.md §2.3, §3; persistence-api.md §7.2, §9.3): a fragment
-- PUT ingests its document under a claim of its own, which names the draft it updates instead of a
-- machine. And a fragment revision's embedded declarations (compilation §5.2), which its reference
-- rows do not hold.

-- Bronzeward is unreleased, so this migration supports no earlier database (persistence-api §11
-- rule 6): on one holding fragment revisions, PostgreSQL refuses the NOT NULL column and the
-- operator recreates the database.
ALTER TABLE staging_claim
  DROP CONSTRAINT staging_claim_kind_check,
  ALTER COLUMN machine DROP NOT NULL,
  ADD COLUMN draft text,
  ADD FOREIGN KEY (draft, cluster) REFERENCES draft (id, cluster),
  ADD CONSTRAINT staging_claim_kind CHECK (kind IN ('import', 'draft-update')),
  -- An import names its machine, a draft update its draft; neither names the other.
  ADD CONSTRAINT staging_claim_subject CHECK ((kind = 'import') = (machine IS NOT NULL)
    AND (kind = 'draft-update') = (draft IS NOT NULL)),
  -- A draft update answers its request: no review outlives its process (compilation §3.1).
  ADD CONSTRAINT staging_claim_draft_update_transient CHECK (kind <> 'draft-update' OR mode = 'transient');

-- The embedded documents a revision identifies, as compilation §5.2 declares them: a JSON array of
-- {"path", "format"}. make_immutable (0009) covers the new column.
ALTER TABLE fragment_revision
  ADD COLUMN embedded jsonb NOT NULL CHECK (jsonb_typeof(embedded) = 'array');
