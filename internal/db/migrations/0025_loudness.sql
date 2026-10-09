-- Loudness (EBU R128) per track, measured in the background: integrated loudness in LUFS and true peak
-- in dBTP. checked_at is set on success and on failure (NULL loudness), so a failure is not retried
-- in a loop; a changed file clears all three and is measured again.
ALTER TABLE tracks ADD COLUMN loudness_lufs REAL;
ALTER TABLE tracks ADD COLUMN true_peak_db REAL;
ALTER TABLE tracks ADD COLUMN loudness_checked_at INTEGER;
CREATE INDEX tracks_loudness_pending ON tracks(loudness_checked_at, added_at);
