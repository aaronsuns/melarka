-- The names agent's review log: a track is reviewed while its fingerprint (a
-- hash of the displayed title|artist|album|year) still matches; any later
-- change to those puts it back on GET /admin/metadata/review.
CREATE TABLE metadata_review (
  track_id INTEGER PRIMARY KEY REFERENCES tracks(id) ON DELETE CASCADE,
  reviewed_at INTEGER NOT NULL,
  outcome TEXT NOT NULL CHECK (outcome IN ('fixed','ok','skipped')),
  fingerprint TEXT NOT NULL
);
-- track_overrides.album = '' (not NULL) and year = 0 now mean "no album" /
-- "no year" (set with no_album / no_year); NULL still means "no override".
