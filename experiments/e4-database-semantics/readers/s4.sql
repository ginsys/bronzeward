-- S4 ownership. An attempt is stale when a takeover to a newer generation is ordered before it.
SELECT 'takeovers', count(*) FROM timeline WHERE kind = 'takeover';
SELECT 'attempts', count(*) FROM timeline WHERE kind = 'attempt';
SELECT 'stale_attempts', count(*) FROM timeline a WHERE a.kind = 'attempt' AND EXISTS (SELECT 1 FROM timeline t WHERE t.operation_id = a.operation_id AND t.kind = 'takeover' AND t.seq < a.seq AND t.owner_gen > a.owner_gen);
SELECT 'op:' || id, owner || ' ' || owner_gen || ' ' || attempts FROM operation ORDER BY id;
