-- S6 migrations, SQLite catalog.
SELECT 'migration:' || version, applied_by FROM schema_migrations ORDER BY version;
SELECT 'tables', count(*) FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%';
SELECT 'has_job', count(*) FROM sqlite_master WHERE type = 'table' AND name = 'job';
SELECT 'has_job_state_check', count(*) FROM sqlite_master WHERE type = 'table' AND name = 'job' AND sql LIKE '%CHECK (state IN%';
