CREATE TABLE tagging_state (
  track_id INTEGER PRIMARY KEY REFERENCES tracks(id) ON DELETE CASCADE,
  lastfm_at INTEGER,          -- last completed Last.fm attempt
  agent_at INTEGER            -- last agent batch that covered the track
);
CREATE TABLE lastfm_artist_tags (
  artist TEXT PRIMARY KEY COLLATE NOCASE,
  tags TEXT NOT NULL,         -- JSON [{"name":"mandopop","count":100}, ...] as Last.fm returned them
  fetched_at INTEGER NOT NULL
);
CREATE TABLE app_state (key TEXT PRIMARY KEY, value TEXT NOT NULL);
