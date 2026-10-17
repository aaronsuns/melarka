-- Lyrics matching now takes versions of the same song whose duration is up to
-- 60 s off (a video's intro or outro) and sees more search results, so the
-- automatic misses are worth another look: the prefetch takes them again, as
-- if their retry time had come. A user's "no lyrics" (manual=1) and a
-- reported-wrong track (wrong_at) stay as they are.
UPDATE lyrics_lookup SET attempted_at = 0 WHERE found = 0 AND manual = 0 AND wrong_at IS NULL;
