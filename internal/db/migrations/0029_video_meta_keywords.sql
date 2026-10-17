-- What YouTube says about a downloaded video beyond its title (yt-dlp Meta):
-- the uploader's tags, YouTube's categories and the description (its first
-- 2000 characters). Kept when the song downloads, or read later for songs
-- downloaded before; the tagging agent works from it.
CREATE TABLE video_meta (
  video_id TEXT PRIMARY KEY,
  tags TEXT NOT NULL DEFAULT '[]',        -- JSON array of strings
  categories TEXT NOT NULL DEFAULT '[]',  -- JSON array of strings
  description TEXT NOT NULL DEFAULT '',
  fetched_at INTEGER NOT NULL
);
-- Free-form search words per track, beside the vocabulary tags: the film or
-- drama a song is from, the original singer, another name. Search finds them;
-- the Tags tab never shows them.
CREATE TABLE track_keywords (
  track_id INTEGER NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
  word TEXT NOT NULL COLLATE NOCASE,
  source TEXT NOT NULL,
  PRIMARY KEY (track_id, word)
);
