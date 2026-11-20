-- Which video sources the server offers (internal/videosrc). No row: the
-- LARK_VIDEO_SOURCES default. A row: an admin's choice, comma-separated
-- ("youtube,bilibili"); an empty string means none.
CREATE TABLE video_sources (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  sources TEXT NOT NULL
);
