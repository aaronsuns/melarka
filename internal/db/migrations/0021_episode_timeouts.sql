-- Episode downloads that ran into the worker's own deadline (a file too long
-- for the line): the first waits for its own backoff, the second fails it.
ALTER TABLE episode_files ADD COLUMN timeouts INTEGER NOT NULL DEFAULT 0;
