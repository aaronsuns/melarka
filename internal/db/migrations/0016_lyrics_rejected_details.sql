-- What a rejection removed, so it can be undone or lifted: who reported it
-- ("wrong lyrics"; NULL for an admin's delete), the report it belongs to
-- (every copy of the reported words shares the first tombstone's id), a short
-- preview for the admin, and the removed row itself (NULL text: rejected
-- before this migration, nothing to restore).
ALTER TABLE lyrics_rejected ADD COLUMN reported_by INTEGER REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE lyrics_rejected ADD COLUMN report_id INTEGER;
ALTER TABLE lyrics_rejected ADD COLUMN preview TEXT NOT NULL DEFAULT '';
ALTER TABLE lyrics_rejected ADD COLUMN text TEXT;
ALTER TABLE lyrics_rejected ADD COLUMN lyrics_external_id TEXT;
ALTER TABLE lyrics_rejected ADD COLUMN synced INTEGER NOT NULL DEFAULT 0;
ALTER TABLE lyrics_rejected ADD COLUMN duration_s INTEGER NOT NULL DEFAULT 0;
ALTER TABLE lyrics_rejected ADD COLUMN offset_ms INTEGER NOT NULL DEFAULT 0;
