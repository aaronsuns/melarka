CREATE TABLE lyrics (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  track_id INTEGER NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
  source TEXT NOT NULL,          -- embedded | lrclib | netease | qq | kugou
  external_id TEXT NOT NULL,     -- the provider's id ("sidecar" / "tag" for embedded)
  synced INTEGER NOT NULL,
  text TEXT NOT NULL,
  selected INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  UNIQUE (track_id, source, external_id)
);
CREATE INDEX lyrics_track ON lyrics(track_id, selected);
CREATE TABLE lyrics_lookup (
  track_id INTEGER PRIMARY KEY REFERENCES tracks(id) ON DELETE CASCADE,
  attempted_at INTEGER NOT NULL,
  found INTEGER NOT NULL,
  manual INTEGER NOT NULL DEFAULT 0  -- an admin chose the selected row; lookups keep it while it exists
);
