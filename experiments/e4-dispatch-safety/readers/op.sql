-- One operation (:'op') as the database records it, read without e4x. Timeline revisions come
-- from a sequence at insert time. Where one transaction waited on a lock another held (a revoke
-- behind a commitment's FOR SHARE, a takeover behind an attempt's FOR UPDATE), the waiter takes
-- its rev after the holder committed, so rev order is their commit order; transactions that
-- contend on nothing can commit out of rev order. Times are the server's clock_timestamp().
SELECT 'state', coalesce((SELECT state FROM operations WHERE id = :'op'), 'none');
SELECT 'owner', coalesce((SELECT owner || '@' || owner_gen FROM operations WHERE id = :'op'), 'none');
SELECT 'attempts', count(*) FROM attempts WHERE op_id = :'op';
SELECT 'attempt_owners', coalesce(string_agg(n || ':' || owner || '@' || owner_gen, ',' ORDER BY n), 'none')
FROM attempts WHERE op_id = :'op';
-- An attempt recorded at a generation older than the operation's current one when it was
-- recorded: an attempt whose recorder had already lost ownership.
SELECT 'stale_attempts', count(*) FROM attempts a
WHERE a.op_id = :'op' AND EXISTS (
  SELECT 1 FROM timeline t WHERE t.op_id = a.op_id AND t.kind = 'ownership'
    AND t.at <= a.recorded_at AND substring(t.detail FROM 'gen=([0-9]+)')::int > a.owner_gen);
SELECT 'responses', coalesce(string_agg(n || ':' || coalesce(response, 'none'), ',' ORDER BY n), 'none')
FROM attempts WHERE op_id = :'op';
SELECT 'accounted', count(*) FROM attempts WHERE op_id = :'op' AND accounted_rev IS NOT NULL;
SELECT 'revoked', count(*) FROM approvals WHERE plan_id = :'op' AND revoked_at IS NOT NULL;
-- Where the revocation fell relative to the commitment and to the first attempt.
SELECT 'revocation', CASE
  WHEN r.rev IS NULL THEN 'none'
  WHEN c.rev IS NULL OR r.rev < c.rev THEN 'before-commit'
  WHEN a.rev IS NULL OR r.rev < a.rev THEN 'after-commit'
  ELSE 'after-attempt' END
FROM (SELECT min(rev) AS rev FROM timeline WHERE op_id = :'op' AND kind = 'revoked') r,
     (SELECT min(rev) AS rev FROM timeline WHERE op_id = :'op' AND kind = 'committed') c,
     (SELECT min(rev) AS rev FROM timeline WHERE op_id = :'op' AND kind = 'attempt') a;
SELECT 'applied_is_artifact', count(*) FROM machines m JOIN plans p ON p.machine = m.id
WHERE p.id = :'op' AND m.applied_digest = p.artifact_digest;
SELECT 'baseline_rev', coalesce((SELECT m.baseline_rev FROM machines m JOIN plans p ON p.machine = m.id
  WHERE p.id = :'op'), 0);
SELECT 'timeline', coalesce(string_agg(kind, ',' ORDER BY rev), 'none') FROM timeline
WHERE op_id = :'op' AND kind <> 'observation';
