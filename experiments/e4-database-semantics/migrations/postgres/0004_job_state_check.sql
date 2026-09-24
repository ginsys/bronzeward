-- Adding a constraint to an existing table: one statement here.
ALTER TABLE job ADD CONSTRAINT job_state_check CHECK (state IN ('pending', 'claimed', 'done'));
