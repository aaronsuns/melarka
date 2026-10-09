CREATE TABLE search_history (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  query TEXT NOT NULL,          -- whitespace-collapsed display text, the latest-typed spelling
  norm_key TEXT NOT NULL,       -- lower-cased in Go (Unicode-aware; NOCASE is ASCII-only): one row per key
  seq INTEGER NOT NULL,         -- per-user order, newest highest (timestamps tie within a second)
  searched_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, norm_key)
);
CREATE INDEX search_history_seq ON search_history(user_id, seq);
