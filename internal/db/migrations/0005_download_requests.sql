CREATE TABLE download_requests (
  download_id INTEGER NOT NULL REFERENCES downloads(id) ON DELETE CASCADE,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (download_id, user_id)
);
-- Existing owners count as requesters of their own jobs; finished downloads
-- are NOT favorited retroactively ("when a download links its track").
INSERT INTO download_requests(download_id, user_id, created_at) SELECT id, user_id, created_at FROM downloads;
