CREATE TABLE artwork_lookup (
  track_id INTEGER PRIMARY KEY REFERENCES tracks(id) ON DELETE CASCADE,
  attempted_at INTEGER NOT NULL,
  found INTEGER NOT NULL,
  source TEXT NOT NULL DEFAULT ''   -- embedded | folder | itunes | netease | qq; '' for a miss
);
