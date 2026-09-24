-- S5 per job and per event, for the lease rows (a handful of jobs).
SELECT 'job:' || id, state || ' ' || fence || ' ' || coalesce(completed_by, '-') || ' ' || coalesce(completed_fence, 0) FROM job ORDER BY id;
SELECT 'event:' || seq, job_id || ' ' || action || ' ' || worker || ' ' || fence FROM job_event ORDER BY seq;
