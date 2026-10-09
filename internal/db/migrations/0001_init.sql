CREATE TABLE users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT NOT NULL UNIQUE COLLATE NOCASE,
  password_hash TEXT NOT NULL,
  role TEXT NOT NULL CHECK (role IN ('admin','member')),
  created_at INTEGER NOT NULL
);
CREATE TABLE devices (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL
);
CREATE TABLE libraries (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  root TEXT NOT NULL UNIQUE,
  is_download_target INTEGER NOT NULL DEFAULT 0,
  last_scan_at INTEGER
);
CREATE TABLE artists (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE COLLATE NOCASE
);
CREATE TABLE albums (
  id INTEGER PRIMARY KEY,
  library_id INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  folder TEXT NOT NULL,            -- relative dir of the tracks
  name TEXT NOT NULL,
  artist_id INTEGER REFERENCES artists(id),
  year INTEGER,
  UNIQUE (library_id, folder, name)
);
CREATE TABLE tracks (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  library_id INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  rel_path TEXT NOT NULL,
  size INTEGER NOT NULL,
  mtime INTEGER NOT NULL,
  fingerprint TEXT NOT NULL,
  duration_ms INTEGER NOT NULL DEFAULT 0,
  codec TEXT NOT NULL DEFAULT '',
  bitrate INTEGER NOT NULL DEFAULT 0,     -- kbps
  sample_rate INTEGER NOT NULL DEFAULT 0,
  lossless INTEGER NOT NULL DEFAULT 0,
  tag_title TEXT NOT NULL DEFAULT '',
  tag_artist TEXT NOT NULL DEFAULT '',
  tag_album TEXT NOT NULL DEFAULT '',
  tag_album_artist TEXT NOT NULL DEFAULT '',
  tag_year INTEGER,
  track_no INTEGER,
  disc_no INTEGER,
  artist_id INTEGER REFERENCES artists(id),
  album_id INTEGER REFERENCES albums(id),
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','kept','trashed')),
  keep_plays INTEGER NOT NULL DEFAULT 0,
  trashed_at INTEGER,
  trash_path TEXT,
  missing_since INTEGER,
  broken INTEGER NOT NULL DEFAULT 0,
  broken_reason TEXT NOT NULL DEFAULT '',
  added_at INTEGER NOT NULL,
  UNIQUE (library_id, rel_path)
);
CREATE INDEX tracks_fingerprint ON tracks(fingerprint);
CREATE INDEX tracks_album ON tracks(album_id);
CREATE INDEX tracks_artist ON tracks(artist_id);
CREATE TABLE track_overrides (
  track_id INTEGER PRIMARY KEY REFERENCES tracks(id) ON DELETE CASCADE,
  title TEXT, artist TEXT, album TEXT, year INTEGER
);
CREATE VIRTUAL TABLE track_fts USING fts5(doc, tokenize='unicode61');
-- rowid of track_fts == tracks.id
CREATE TABLE tags (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE COLLATE NOCASE,
  kind TEXT NOT NULL DEFAULT 'other' CHECK (kind IN ('genre','mood','scene','era','language','other'))
);
CREATE TABLE track_tags (
  track_id INTEGER NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
  tag_id INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
  source TEXT NOT NULL CHECK (source IN ('folder_rule','lastfm','musicbrainz','agent','manual')),
  confidence REAL NOT NULL DEFAULT 1,
  removed INTEGER NOT NULL DEFAULT 0,     -- manual tombstone
  PRIMARY KEY (track_id, tag_id, source)
);
CREATE TABLE favorites (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  track_id INTEGER NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, track_id)
);
CREATE TABLE dislikes (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  track_id INTEGER NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, track_id)
);
CREATE TABLE playlists (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE playlist_items (
  playlist_id INTEGER NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,
  position INTEGER NOT NULL,
  track_id INTEGER NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
  PRIMARY KEY (playlist_id, position)
);
CREATE TABLE play_events (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  track_id INTEGER NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
  device_id INTEGER REFERENCES devices(id) ON DELETE SET NULL,
  client_event_id TEXT NOT NULL,
  started_at INTEGER NOT NULL,
  played_seconds INTEGER NOT NULL,
  skipped INTEGER NOT NULL DEFAULT 0,
  quality TEXT NOT NULL DEFAULT '',
  UNIQUE (user_id, client_event_id)
);
CREATE INDEX play_events_user_time ON play_events(user_id, started_at);
CREATE TABLE play_queue (
  user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  track_ids TEXT NOT NULL,          -- JSON array of ints
  current_index INTEGER NOT NULL,
  position_ms INTEGER NOT NULL,
  version INTEGER NOT NULL,
  updated_by TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);
