-- YouTube view and like counts per video, read by a full extraction (flat
-- lists carry no likes, and a Mix not even views). Recommendations multiply
-- their top candidates' scores by a quality factor from these; a row is
-- trusted for 30 days, and rows older than 90 days are deleted.
CREATE TABLE video_stats (
  video_id TEXT PRIMARY KEY,
  view_count INTEGER NOT NULL,
  like_count INTEGER NOT NULL,
  fetched_at INTEGER NOT NULL
);
-- The day's count of those extractions per user (reset with the search counters).
ALTER TABLE recommendation_users ADD COLUMN stats_lookups INTEGER NOT NULL DEFAULT 0;
