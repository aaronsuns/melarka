CREATE TABLE user_prefs (
  user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  language TEXT CHECK (language IS NULL OR language IN ('en','zh-Hans','zh-Hant','sv')),
  on_open TEXT NOT NULL DEFAULT 'shuffle_favorites' CHECK (on_open IN ('shuffle_favorites','resume','nothing')),
  updated_at INTEGER NOT NULL
);
