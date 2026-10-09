-- Recommendations ("为你推荐"). Replaced per user on every refresh.
CREATE TABLE recommendations (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  video_id TEXT NOT NULL,
  title TEXT NOT NULL,
  channel TEXT NOT NULL DEFAULT '',
  duration_s INTEGER NOT NULL DEFAULT 0,
  thumbnail TEXT NOT NULL DEFAULT '',
  score REAL NOT NULL,
  reason_track_id INTEGER REFERENCES tracks(id) ON DELETE SET NULL,
  reason_kind TEXT NOT NULL DEFAULT 'played' CHECK (reason_kind IN ('played','favorite')),
  created_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, video_id)
);
-- "不感兴趣": permanent per user.
CREATE TABLE recommendation_dismissals (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  video_id TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, video_id)
);
-- A seed track's YouTube video: searched once; video_id NULL is a remembered miss.
CREATE TABLE seed_videos (
  track_id INTEGER PRIMARY KEY REFERENCES tracks(id) ON DELETE CASCADE,
  video_id TEXT,
  searched_at INTEGER NOT NULL
);
-- A Last.fm suggestion ("title artist" query) resolved to YouTube; video_id NULL is a miss.
CREATE TABLE search_videos (
  query TEXT PRIMARY KEY,
  video_id TEXT,
  title TEXT NOT NULL DEFAULT '',
  channel TEXT NOT NULL DEFAULT '',
  duration_s INTEGER NOT NULL DEFAULT 0,
  searched_at INTEGER NOT NULL
);
-- Per-user refresh state: requested_at > max(refreshed_at, attempted_at) means a refresh is
-- pending; search_day + the counters bound YouTube searches per user per day.
CREATE TABLE recommendation_users (
  user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  refreshed_at INTEGER,
  attempted_at INTEGER,
  requested_at INTEGER,
  search_day TEXT NOT NULL DEFAULT '',
  seed_searches INTEGER NOT NULL DEFAULT 0,
  other_searches INTEGER NOT NULL DEFAULT 0
);
-- The nightly refresh's last completed day lives in app_state (0007), key recommendations.nightly_day.
