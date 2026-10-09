-- 视频 (spec §18.2). Per-user history of opened videos and searches (90 days),
-- feeding 为你推荐; the YouTube Mix cache shared by 相关视频 and 为你推荐.
CREATE TABLE video_history (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK (kind IN ('watch','search')),
  item TEXT NOT NULL,                    -- watch: video id; search: lower-cased NormQuery
  label TEXT NOT NULL DEFAULT '',        -- watch: title; search: the query as last typed (untrusted)
  channel TEXT NOT NULL DEFAULT '',
  channel_id TEXT NOT NULL DEFAULT '',
  duration_s INTEGER NOT NULL DEFAULT 0,
  seed_video_id TEXT NOT NULL DEFAULT '', -- the video whose Mix this seeds: watch = item, search = its first result
  times INTEGER NOT NULL DEFAULT 1,
  last_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, kind, item)
);
CREATE INDEX video_history_recent ON video_history(user_id, last_at);
CREATE TABLE video_mixes (
  video_id TEXT PRIMARY KEY,
  entries TEXT NOT NULL,                  -- JSON []ytdlp.Video (non-live, valid ids)
  fetched_at INTEGER NOT NULL
);
CREATE TABLE video_recommendations (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  video_id TEXT NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  channel TEXT NOT NULL DEFAULT '',
  channel_id TEXT NOT NULL DEFAULT '',
  duration_s INTEGER NOT NULL DEFAULT 0,
  score REAL NOT NULL,
  reason_kind TEXT NOT NULL DEFAULT 'watch',
  reason TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, video_id)
);
-- Same shape as discovery_users: requested_at > MAX(refreshed_at, attempted_at) means pending.
CREATE TABLE video_rec_users (
  user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  refreshed_at INTEGER,
  attempted_at INTEGER,
  requested_at INTEGER,
  day TEXT NOT NULL DEFAULT '',
  mixes INTEGER NOT NULL DEFAULT 0
);
-- The nightly run's last day: app_state key video.recs_day. History older than 90 days
-- and Mix cache rows older than 7 days are pruned once a day (app_state key video.prune_day).
