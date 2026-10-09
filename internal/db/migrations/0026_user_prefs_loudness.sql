-- Loudness normalization: the player turns loud tracks down to a common
-- level using each track's measured gain. Per user, on by default.
ALTER TABLE user_prefs ADD COLUMN normalize_loudness INTEGER NOT NULL DEFAULT 1 CHECK (normalize_loudness IN (0,1));
