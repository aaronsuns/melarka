-- Channel discovery ("推荐" in Channels, spec §17.4). Replaced per user on every refresh.
CREATE TABLE channel_suggestions (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  channel_id TEXT NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  score REAL NOT NULL,
  sample_video_id TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, channel_id)
);
CREATE TABLE video_suggestions (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  video_id TEXT NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  channel TEXT NOT NULL DEFAULT '',
  channel_id TEXT NOT NULL DEFAULT '',
  duration_s INTEGER NOT NULL DEFAULT 0,
  score REAL NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, video_id)
);
-- "不感兴趣": permanent per user.
CREATE TABLE channel_dismissals (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK (kind IN ('channel','video')),
  item_id TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, kind, item_id)
);
-- requested_at > max(refreshed_at, attempted_at): a refresh is pending; mixes counts today's (day) on-demand Mix calls.
CREATE TABLE discovery_users (
  user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  refreshed_at INTEGER,
  attempted_at INTEGER,
  requested_at INTEGER,
  day TEXT NOT NULL DEFAULT '',
  mixes INTEGER NOT NULL DEFAULT 0
);
-- The nightly run's last completed day lives in app_state, key channels.discovery_day.
