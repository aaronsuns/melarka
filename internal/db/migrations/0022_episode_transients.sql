-- Episode downloads YouTube answered with a transient error that is the
-- file's alone (HTTP 403): each waits for the file's own backoff without
-- spending an attempt; too many fail it as lark:forbidden.
ALTER TABLE episode_files ADD COLUMN transients INTEGER NOT NULL DEFAULT 0;
