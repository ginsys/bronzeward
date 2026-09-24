-- S6 migrations, PostgreSQL catalog.
SELECT 'migration:' || version, applied_by FROM schema_migrations ORDER BY version;
SELECT 'tables', count(*) FROM information_schema.tables WHERE table_schema = 'public';
SELECT 'has_job', count(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'job';
SELECT 'has_job_state_check', count(*) FROM pg_constraint WHERE conname = 'job_state_check';
