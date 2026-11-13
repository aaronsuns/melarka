-- The usage ping is on by default from 0.2.22, for new and existing installs
-- (internal/usageping). A row in usage_ping still means on; an admin who turns
-- it off deletes the row, and nothing brings it back. LARK_USAGE_PING=off keeps
-- it off (and deletes the row) at every start.
INSERT OR IGNORE INTO usage_ping(id, install_id, last_sent_at)
VALUES (1, lower(hex(randomblob(16))), 0);
