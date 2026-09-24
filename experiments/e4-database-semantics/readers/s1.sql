-- S1 stale revision. Every accepted write appended one 'revision' ledger row; fragment.revision
-- counts the writes the database kept. A lost update is a recorded write the revision did not keep.
SELECT 'revisions_recorded', count(*) FROM ledger WHERE kind = 'revision';
SELECT 'duplicate_revisions', count(*) FROM (SELECT subject, number FROM ledger WHERE kind = 'revision' GROUP BY subject, number HAVING count(*) > 1) d;
SELECT 'final_revision', coalesce(sum(revision), 0) FROM fragment;
SELECT 'lost_updates', (SELECT count(*) FROM ledger WHERE kind = 'revision') - (SELECT coalesce(sum(revision), 0) FROM fragment);
