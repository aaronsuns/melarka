-- Channels: follow YouTube channels without an account. Episodes
-- are never tracks: they live in their own tables and files.
CREATE TABLE channels (
  id TEXT PRIMARY KEY,                        -- UC… (ytdlp.IsChannelID)
  title TEXT NOT NULL DEFAULT '',
  handle TEXT NOT NULL DEFAULT '',
  avatar TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  polled_at INTEGER,                          -- last successful feed read
  next_poll_at INTEGER NOT NULL DEFAULT 0,    -- 0: as soon as possible
  poll_failures INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL
);
CREATE TABLE channel_follows (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  media TEXT NOT NULL DEFAULT 'audio' CHECK (media IN ('audio','video')),
  keep_days INTEGER CHECK (keep_days IS NULL OR keep_days BETWEEN 1 AND 3650), -- NULL: channels.keep_days
  paused INTEGER NOT NULL DEFAULT 0,
  include_shorts INTEGER NOT NULL DEFAULT 0,
  include_live INTEGER NOT NULL DEFAULT 0,
  backfill_pending INTEGER NOT NULL DEFAULT 1, -- the next poll queues the newest few (channels.initial_backfill)
  want_since INTEGER NOT NULL,                 -- episodes published from here on are downloaded (follow / un-pause time)
  followed_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, channel_id)
);
CREATE INDEX channel_follows_channel ON channel_follows(channel_id);
CREATE TABLE episodes (
  video_id TEXT PRIMARY KEY,
  channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  title TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  published_at INTEGER NOT NULL,
  duration_s INTEGER NOT NULL DEFAULT 0,
  -- unknown: details not read yet; video | short | replay (a finished live stream);
  -- live / upcoming: read again on later polls; unavailable: members-only, private, removed, age-gated.
  kind TEXT NOT NULL DEFAULT 'unknown' CHECK (kind IN ('unknown','video','short','replay','live','upcoming','unavailable')),
  info_failures INTEGER NOT NULL DEFAULT 0,
  seen_at INTEGER NOT NULL
);
CREATE INDEX episodes_channel ON episodes(channel_id, published_at);
-- One copy per video for all followers. status expired: retention deleted the file (the row stays for history).
CREATE TABLE episode_files (
  video_id TEXT NOT NULL REFERENCES episodes(video_id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK (kind IN ('audio','video')),
  status TEXT NOT NULL CHECK (status IN ('queued','downloading','done','failed','expired')),
  path TEXT NOT NULL DEFAULT '',              -- relative to channels.root
  bytes INTEGER NOT NULL DEFAULT 0,
  progress REAL NOT NULL DEFAULT 0,
  error TEXT NOT NULL DEFAULT '',
  attempts INTEGER NOT NULL DEFAULT 0,
  next_attempt_at INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (video_id, kind)
);
CREATE INDEX episode_files_status ON episode_files(status, next_attempt_at);
CREATE TABLE episode_progress (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  video_id TEXT NOT NULL REFERENCES episodes(video_id) ON DELETE CASCADE,
  position_s REAL NOT NULL DEFAULT 0,
  played INTEGER NOT NULL DEFAULT 0,
  hidden INTEGER NOT NULL DEFAULT 0,          -- "delete for me"
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, video_id)
);
CREATE TABLE episode_keeps (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  video_id TEXT NOT NULL REFERENCES episodes(video_id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, video_id)
);
