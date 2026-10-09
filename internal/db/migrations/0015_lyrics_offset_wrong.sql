-- offset_ms: how far the selected lyrics are shifted for everyone (a line shows
-- at its time + offset_ms; ±30000). It belongs to the selected row and is set
-- back to 0 whenever another candidate (or none) becomes selected.
-- duration_s: the candidate's duration as its provider gave it (0 = unknown),
-- so the next best candidate can be the closest version.
ALTER TABLE lyrics ADD COLUMN offset_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE lyrics ADD COLUMN duration_s INTEGER NOT NULL DEFAULT 0;
-- wrong_at: a user reported the selected lyrics wrong and no other candidate
-- was left; the lyrics agent's missing list flags the track (reported_wrong)
-- until lyrics are selected again.
ALTER TABLE lyrics_lookup ADD COLUMN wrong_at INTEGER;
