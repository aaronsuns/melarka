-- 视频 (spec §18.2): a third preview media, "hd" — the ≤720p merged mp4, served once complete —
-- and the description yt-dlp prints with the preview's details (the watch page shows its first lines).
CREATE TABLE previews_new (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  video_id TEXT NOT NULL,
  media TEXT NOT NULL CHECK (media IN ('audio','video','hd')),
  status TEXT NOT NULL CHECK (status IN ('downloading','done','failed')),
  path TEXT NOT NULL DEFAULT '',
  size INTEGER NOT NULL DEFAULT 0,
  total INTEGER NOT NULL DEFAULT 0,
  error TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL DEFAULT '',
  channel TEXT NOT NULL DEFAULT '',
  channel_id TEXT NOT NULL DEFAULT '',
  duration_s INTEGER NOT NULL DEFAULT 0,
  description TEXT NOT NULL DEFAULT '',
  user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
  created_at INTEGER NOT NULL,
  accessed_at INTEGER NOT NULL,
  UNIQUE (video_id, media)
);
INSERT INTO previews_new(id,video_id,media,status,path,size,total,error,title,channel,channel_id,duration_s,user_id,created_at,accessed_at)
  SELECT id,video_id,media,status,path,size,total,error,title,channel,channel_id,duration_s,user_id,created_at,accessed_at FROM previews;
-- AUTOINCREMENT: ids of deleted rows must not be given out again.
DELETE FROM sqlite_sequence WHERE name='previews_new';
INSERT INTO sqlite_sequence(name, seq) SELECT 'previews_new', seq FROM sqlite_sequence WHERE name='previews';
DROP TABLE previews;
ALTER TABLE previews_new RENAME TO previews;
CREATE INDEX previews_user ON previews(user_id, status);
