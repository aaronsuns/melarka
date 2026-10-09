CREATE TABLE download_lists (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  list_id TEXT NOT NULL,                                        -- YouTube playlist id
  title TEXT NOT NULL,
  playlist_id INTEGER REFERENCES playlists(id) ON DELETE SET NULL, -- the requester's Lark playlist
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE (user_id, list_id)
);
CREATE TABLE download_list_items (
  list_row INTEGER NOT NULL REFERENCES download_lists(id) ON DELETE CASCADE,
  position INTEGER NOT NULL,                                    -- 0-based YouTube order
  video_id TEXT NOT NULL,
  PRIMARY KEY (list_row, position)
);
CREATE INDEX download_list_items_video ON download_list_items(video_id);
-- Finished jobs are soft-hidden from the downloads list (not deleted), so
-- dedupe and "never fetched twice" keep working.
ALTER TABLE downloads ADD COLUMN hidden_at INTEGER;
