-- Car lyrics: the current synced lyric line goes into the media-session
-- title (what Bluetooth car displays and the lock screen show). On by default.
ALTER TABLE user_prefs ADD COLUMN car_lyrics INTEGER NOT NULL DEFAULT 1 CHECK (car_lyrics IN (0,1));
