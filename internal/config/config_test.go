package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("LARK_DATA_DIR", t.TempDir())
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != ":4600" {
		t.Errorf("listen=%q", c.Listen)
	}
	if c.Transcode.CacheMaxBytes != 20<<30 {
		t.Errorf("cache=%d", c.Transcode.CacheMaxBytes)
	}
	if c.Transcode.MaxConcurrent != 2 || !c.Transcode.Nice {
		t.Errorf("transcode=%+v", c.Transcode)
	}
	if c.FFmpegPath != "ffmpeg" || c.FFprobePath != "ffprobe" {
		t.Errorf("bins=%q %q", c.FFmpegPath, c.FFprobePath)
	}
	if c.MusicRoot != "/music" {
		t.Errorf("music_root=%q", c.MusicRoot)
	}
	if c.DBPath() != filepath.Join(c.DataDir, "lark.db") {
		t.Errorf("db=%q", c.DBPath())
	}
}

func TestLoadYAMLAndEnv(t *testing.T) {
	dir := t.TempDir()
	yml := "libraries:\n  - name: main\n    path: /music/main\n  - name: youtube\n    path: /music/youtube\n    download_target: true\ntranscode:\n  max_concurrent: 1\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LARK_DATA_DIR", dir)
	t.Setenv("LARK_LISTEN", "127.0.0.1:9999")
	t.Setenv("LARK_ADMIN_USER", "alice")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Libraries) != 2 || !c.Libraries[1].DownloadTarget {
		t.Errorf("libs=%+v", c.Libraries)
	}
	if c.Transcode.MaxConcurrent != 1 {
		t.Errorf("max=%d", c.Transcode.MaxConcurrent)
	}
	if c.Transcode.CacheMaxBytes != 20<<30 {
		t.Errorf("unset yaml field must keep default")
	}
	if c.Listen != "127.0.0.1:9999" || c.AdminUser != "alice" {
		t.Errorf("env not applied: %+v", c)
	}
}

func TestLanguage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LARK_DATA_DIR", dir)
	c, err := Load()
	if err != nil || c.Language != "en" {
		t.Fatalf("default: %q %v", c.Language, err)
	}
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("language: sv\n"), 0o644)
	if c, _ := Load(); c.Language != "sv" {
		t.Fatalf("yaml: %q", c.Language)
	}
	t.Setenv("LARK_LANGUAGE", "zh-Hans")
	if c, _ := Load(); c.Language != "zh-Hans" {
		t.Fatalf("env must win: %q", c.Language)
	}
	t.Setenv("LARK_LANGUAGE", "klingon")
	if _, err := Load(); err == nil {
		t.Fatal("unknown language accepted")
	}
}

func TestTaggingConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LARK_DATA_DIR", dir)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Tagging.FolderRules) != len(DefaultFolderRules()) || len(c.Tagging.FolderRules) == 0 || c.Tagging.LastFMMinCount != 10 || c.LastFMAPIKey != "" {
		t.Fatalf("defaults %+v key=%q", c.Tagging, c.LastFMAPIKey)
	}
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("tagging:\n  folder_rules:\n    - {match: [x], tags: [jazz]}\n"), 0o644)
	t.Setenv("LARK_LASTFM_API_KEY", "k123")
	c, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Tagging.FolderRules) != 1 || c.Tagging.FolderRules[0].Tags[0] != "jazz" || c.Tagging.LastFMMinCount != 10 || c.LastFMAPIKey != "k123" {
		t.Fatalf("override %+v key=%q", c.Tagging, c.LastFMAPIKey)
	}
}

func TestLyricsConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LARK_DATA_DIR", dir)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.Lyrics.Providers, []string{"embedded", "lrclib", "netease", "qq", "kugou"}) || c.Lyrics.PrefetchInterval != 3*time.Second {
		t.Fatalf("defaults %+v", c.Lyrics)
	}
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("lyrics:\n  providers: [lrclib]\n  prefetch_interval: 0s\n"), 0o644)
	c, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.Lyrics.Providers, []string{"lrclib"}) || c.Lyrics.PrefetchInterval != 0 {
		t.Fatalf("yaml %+v", c.Lyrics)
	}
}

func TestArtworkConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LARK_DATA_DIR", dir)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.Artwork.Providers, []string{"embedded", "folder", "itunes", "netease", "qq"}) || c.Artwork.PrefetchInterval != 3*time.Second {
		t.Fatalf("defaults %+v", c.Artwork)
	}
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("artwork:\n  providers: [folder]\n  prefetch_interval: 0s\n"), 0o644)
	c, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.Artwork.Providers, []string{"folder"}) || c.Artwork.PrefetchInterval != 0 {
		t.Fatalf("yaml %+v", c.Artwork)
	}
}

func TestOfflineCacheSwitch(t *testing.T) {
	t.Setenv("LARK_DATA_DIR", t.TempDir())
	t.Setenv("LARK_CONFIG", filepath.Join(t.TempDir(), "none.yaml"))
	c, err := Load()
	if err != nil || !c.OfflineCache {
		t.Fatalf("default on: %v %v", c.OfflineCache, err)
	}
	for v, want := range map[string]bool{"off": false, "false": false, "0": false, "on": true, "true": true, "1": true} {
		t.Setenv("LARK_OFFLINE_CACHE", v)
		if c, err := Load(); err != nil || c.OfflineCache != want {
			t.Errorf("%s: %v %v", v, c.OfflineCache, err)
		}
	}
	t.Setenv("LARK_OFFLINE_CACHE", "maybe")
	if _, err := Load(); err == nil {
		t.Error("bad value accepted")
	}
}

func TestRecommendationsConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LARK_DATA_DIR", dir)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !c.Recommendations.Enabled || c.Recommendations.RefreshAt != "02:30" {
		t.Fatalf("defaults %+v", c.Recommendations)
	}
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("recommendations:\n  enabled: false\n  refresh_at: \"04:05\"\n"), 0o644)
	c, err = Load()
	if err != nil || c.Recommendations.Enabled || c.Recommendations.RefreshAt != "04:05" {
		t.Fatalf("%+v %v", c.Recommendations, err)
	}
	for _, bad := range []string{"25:00", "4pm", "02:30:00"} {
		os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("recommendations:\n  refresh_at: \""+bad+"\"\n"), 0o644)
		if _, err := Load(); err == nil {
			t.Errorf("refresh_at %q accepted", bad)
		}
	}
}

func TestChannelsDefaultsAndRoots(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LARK_DATA_DIR", dir)
	t.Setenv("LARK_CONFIG", filepath.Join(dir, "none.yaml"))
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := ChannelsConfig{Enabled: true, PollInterval: 2 * time.Hour, KeepDays: 10, InitialBackfill: 3, MaxGB: 200,
		PreviewTTL: 24 * time.Hour, PreviewMaxGB: 10}
	if c.Channels != want {
		t.Fatalf("%+v", c.Channels)
	}
	if c.ChannelsRoot() != filepath.Join(dir, "channels") || c.PreviewRoot() != filepath.Join(dir, "previews") {
		t.Fatalf("%s %s", c.ChannelsRoot(), c.PreviewRoot())
	}
	if c.YouTubeFeedURL != "https://www.youtube.com/feeds/videos.xml" {
		t.Fatal(c.YouTubeFeedURL)
	}
	t.Setenv("LARK_YOUTUBE_FEED_URL", "http://127.0.0.1:4701/videos.xml")
	if c, _ := Load(); c.YouTubeFeedURL != "http://127.0.0.1:4701/videos.xml" {
		t.Fatal(c.YouTubeFeedURL)
	}
	if c.YouTubeThumbURL != "https://i.ytimg.com/vi" {
		t.Fatal(c.YouTubeThumbURL)
	}
	t.Setenv("LARK_YOUTUBE_THUMB_URL", "http://127.0.0.1:4701/vi")
	if c, _ := Load(); c.YouTubeThumbURL != "http://127.0.0.1:4701/vi" {
		t.Fatal(c.YouTubeThumbURL)
	}
}

func TestChannelsConfigFromYAML(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LARK_DATA_DIR", dir)
	p := filepath.Join(dir, "c.yaml")
	os.WriteFile(p, []byte("channels:\n  root: /channels\n  preview_root: /previews\n  poll_interval: 30m\n  keep_days: 7\n  initial_backfill: 0\n"), 0o644)
	t.Setenv("LARK_CONFIG", p)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.ChannelsRoot() != "/channels" || c.PreviewRoot() != "/previews" || c.Channels.PollInterval != 30*time.Minute ||
		c.Channels.KeepDays != 7 || c.Channels.InitialBackfill != 0 || c.Channels.MaxGB != 200 {
		t.Fatalf("%+v", c.Channels)
	}
}

func TestChannelsConfigRejectsBadValues(t *testing.T) {
	// "poll_interval: 7200" is a bare number: yaml.v3 refuses it itself (its
	// error does not mention "channels."), so only an error is asserted there.
	for _, bad := range []string{
		"poll_interval: 10m", "poll_interval: 7200", "keep_days: 0", "keep_days: 4000", "initial_backfill: 16",
		"initial_backfill: -1", "max_gb: 0", "preview_ttl: 30m", "preview_max_gb: 0",
	} {
		dir := t.TempDir()
		t.Setenv("LARK_DATA_DIR", dir)
		p := filepath.Join(dir, "c.yaml")
		os.WriteFile(p, []byte("channels:\n  "+bad+"\n"), 0o644)
		t.Setenv("LARK_CONFIG", p)
		_, err := Load()
		if err == nil || (bad != "poll_interval: 7200" && !strings.Contains(err.Error(), "channels.")) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestLoudnessConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LARK_DATA_DIR", dir)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !c.Loudness.Enabled || c.Loudness.Gap != 2*time.Second || c.Loudness.RetryFailedAfter != 720*time.Hour {
		t.Fatalf("defaults %+v", c.Loudness)
	}
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("loudness:\n  enabled: false\n  gap: 0s\n  retry_failed_after: 24h\n"), 0o644)
	c, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Loudness.Enabled || c.Loudness.Gap != 0 || c.Loudness.RetryFailedAfter != 24*time.Hour {
		t.Fatalf("yaml %+v", c.Loudness)
	}
	for _, bad := range []string{"loudness:\n  gap: -1s\n", "loudness:\n  retry_failed_after: 10m\n"} {
		os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(bad), 0o644)
		if _, err := Load(); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}
