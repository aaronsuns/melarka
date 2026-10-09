-- Lyrics an admin deleted from a track's candidates: later lookups never store
-- them again, matched by provider id (NULL for the file's own lyrics, so a
-- corrected .lrc is picked up) or by text_hash, the sha256 of the words
-- without timestamps (the same text from another provider or id).
CREATE TABLE lyrics_rejected (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  track_id INTEGER NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
  source TEXT NOT NULL,
  external_id TEXT,
  text_hash TEXT NOT NULL,
  rejected_at INTEGER NOT NULL
);
CREATE INDEX lyrics_rejected_track ON lyrics_rejected(track_id);
