-- Previews: temporary downloads played before (or instead of) keeping them.
-- One file per (video, media) for everyone; deleted preview_ttl after the last access.
CREATE TABLE previews (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  video_id TEXT NOT NULL,
  media TEXT NOT NULL CHECK (media IN ('audio','video')),
  status TEXT NOT NULL CHECK (status IN ('downloading','done','failed')),
  path TEXT NOT NULL DEFAULT '',          -- relative to channels.preview_root
  size INTEGER NOT NULL DEFAULT 0,        -- bytes on disk once done
  total INTEGER NOT NULL DEFAULT 0,       -- bytes yt-dlp announced (0: unknown)
  error TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL DEFAULT '',
  channel TEXT NOT NULL DEFAULT '',
  channel_id TEXT NOT NULL DEFAULT '',
  duration_s INTEGER NOT NULL DEFAULT 0,
  user_id INTEGER REFERENCES users(id) ON DELETE SET NULL, -- who started it (the per-user cap)
  created_at INTEGER NOT NULL,
  accessed_at INTEGER NOT NULL,
  UNIQUE (video_id, media)
);
