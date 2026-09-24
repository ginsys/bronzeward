-- S5 queue claims. Without lease expiry, a job claimed twice is a double claim.
SELECT 'jobs', count(*) FROM job;
SELECT 'done', count(*) FROM job WHERE state = 'done';
SELECT 'claims', count(*) FROM job_event WHERE action = 'claim';
SELECT 'jobs_claimed_twice', count(*) FROM (SELECT job_id FROM job_event WHERE action = 'claim' GROUP BY job_id HAVING count(*) > 1) d;
SELECT 'completions', count(*) FROM job_event WHERE action = 'complete';
SELECT 'completed_on_stale_fence', count(*) FROM job WHERE state = 'done' AND completed_fence <> fence;
