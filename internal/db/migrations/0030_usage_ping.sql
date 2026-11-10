-- The opt-in usage ping (internal/usageping): a row here means an admin turned
-- it on. install_id is random, made when it is turned on and deleted with the
-- row when it is turned off; last_sent_at is when the website last took a ping.
CREATE TABLE usage_ping (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  install_id TEXT NOT NULL,
  last_sent_at INTEGER NOT NULL DEFAULT 0
);
