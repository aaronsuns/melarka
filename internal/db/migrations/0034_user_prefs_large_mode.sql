-- Large mode (大图模式): the web app shows 收藏 as a grid of large covers and
-- enlarges the player and tab bar buttons. Per user, off by default.
ALTER TABLE user_prefs ADD COLUMN large_mode INTEGER NOT NULL DEFAULT 0 CHECK (large_mode IN (0,1));
