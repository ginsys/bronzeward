-- S3 unique operation intent: at most one active operation per machine scope, one per key.
SELECT 'operations', count(*) FROM operation;
SELECT 'scopes_with_two_active', count(*) FROM (SELECT machine FROM operation WHERE state IN ('committed', 'sending', 'verifying', 'unresolved') GROUP BY machine HAVING count(*) > 1) d;
SELECT 'intents_recorded', count(*) FROM timeline WHERE kind = 'intent';
SELECT 'op:' || idem_key, id || ' ' || machine || ' ' || state || ' ' || owner FROM operation ORDER BY id;
