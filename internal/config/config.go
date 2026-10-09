// Package config loads Lark's settings from environment variables and an
// optional <data dir>/config.yaml (path overridable with LARK_CONFIG).
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Languages lists the UI languages Lark supports; Config.Language and every
// user's prefs.Language must be one of these (or, for a user, null).
var Languages = []string{"en", "zh-Hans", "zh-Hant", "sv"}

type LibrarySeed struct {
	Name           string `yaml:"name"`
	Path           string `yaml:"path"`
	DownloadTarget bool   `yaml:"download_target"`
}

type TranscodeConfig struct {
	CacheMaxBytes int64 `yaml:"cache_max_bytes"`
	MaxConcurrent int   `yaml:"max_concurrent"`
	Nice          bool  `yaml:"nice"`
}

// FolderRule tags every track whose folder path contains any Match substring
// (case-insensitive) with the vocabulary slugs in Tags.
type FolderRule struct {
	Match []string `yaml:"match"`
	Tags  []string `yaml:"tags"`
}

type TaggingConfig struct {
	FolderRules    []FolderRule `yaml:"folder_rules"`
	LastFMMinCount int          `yaml:"lastfm_min_count"`
}

// DefaultFolderRules maps common folder names to vocabulary tags.
func DefaultFolderRules() []FolderRule {
	return []FolderRule{
		// Only an explicit "instrumental" folder implies it: light music and
		// piano songs often have vocals, and instrumental skips lyrics lookups.
		{Match: []string{"纯音乐", "純音樂", "instrumental"}, Tags: []string{"instrumental"}},
		{Match: []string{"轻音乐", "輕音樂"}, Tags: []string{"chill"}},
		{Match: []string{"钢琴", "鋼琴", "piano"}, Tags: []string{"piano"}},
		{Match: []string{"咖啡", "cafe", "café"}, Tags: []string{"cafe", "chill"}},
		{Match: []string{"精选", "精選", "金曲", "经典", "經典", "怀旧", "懷舊", "老歌"}, Tags: []string{"classic"}},
		{Match: []string{"儿歌", "兒歌", "童谣", "童謠", "kids", "children"}, Tags: []string{"kids"}},
		{Match: []string{"粤语", "粵語", "cantonese"}, Tags: []string{"cantonese"}},
		{Match: []string{"古典", "classical"}, Tags: []string{"classical"}},
		{Match: []string{"摇滚", "搖滾", "rock"}, Tags: []string{"rock"}},
		{Match: []string{"民谣", "民謠", "folk"}, Tags: []string{"folk"}},
		{Match: []string{"爵士", "jazz"}, Tags: []string{"jazz"}},
		{Match: []string{"睡眠", "助眠", "sleep"}, Tags: []string{"sleep"}},
		{Match: []string{"车载", "車載", "开车", "開車", "driving"}, Tags: []string{"driving"}},
		{Match: []string{"健身", "运动", "運動", "workout", "gym"}, Tags: []string{"workout"}},
		{Match: []string{"圣诞", "聖誕", "christmas"}, Tags: []string{"christmas"}},
		{Match: []string{"蒙古"}, Tags: []string{"mongolian"}},
		{Match: []string{"80年代"}, Tags: []string{"80s"}},
		{Match: []string{"90年代"}, Tags: []string{"90s"}},
	}
}

// LyricsConfig picks the lyrics providers (in ranking order) and the
// background prefetch pace.
type LyricsConfig struct {
	Providers        []string      `yaml:"providers"`         // default [embedded, lrclib, netease, qq, kugou]
	PrefetchInterval time.Duration `yaml:"prefetch_interval"` // default 3s; 0 = off (write "0s" in YAML)
}

// ArtworkConfig picks the cover sources (asked in this order) and the background prefetch pace.
type ArtworkConfig struct {
	Providers        []string      `yaml:"providers"`         // default [embedded, folder, itunes, netease, qq]
	PrefetchInterval time.Duration `yaml:"prefetch_interval"` // default 3s; 0 = off (write "0s" in YAML)
}

// LoudnessConfig paces the background loudness measurement (one niced
// ffmpeg decode per track, one at a time).
type LoudnessConfig struct {
	Enabled          bool          `yaml:"enabled"`            // default true
	Gap              time.Duration `yaml:"gap"`                // pause between tracks, default 2s; 0s = none
	RetryFailedAfter time.Duration `yaml:"retry_failed_after"` // a failed measurement is tried again after this, default 720h, at least 1h (checked when enabled)
}

func (c LoudnessConfig) validate() error {
	if !c.Enabled {
		return nil // the limits only matter when the worker runs
	}
	switch {
	case c.Gap < 0:
		return fmt.Errorf("loudness.gap %s: must not be negative", c.Gap)
	case c.RetryFailedAfter < time.Hour:
		return fmt.Errorf("loudness.retry_failed_after %s: at least 1h (with a unit)", c.RetryFailedAfter)
	}
	return nil
}

// RecommendationsConfig switches "为你推荐" on and sets the nightly refresh time.
type RecommendationsConfig struct {
	Enabled   bool   `yaml:"enabled"`    // default true
	RefreshAt string `yaml:"refresh_at"` // "HH:MM" in the process's local time (TZ; UTC in a container without it), default "02:30"
}

// ChannelsConfig: following YouTube channels and previews.
type ChannelsConfig struct {
	Enabled         bool          `yaml:"enabled"`          // default true
	Root            string        `yaml:"root"`             // episode files; "" = <data dir>/channels
	PollInterval    time.Duration `yaml:"poll_interval"`    // default 2h, at least 15m
	KeepDays        int           `yaml:"keep_days"`        // default 10 (1–3650)
	InitialBackfill int           `yaml:"initial_backfill"` // default 3 (0–15)
	MaxGB           int           `yaml:"max_gb"`           // default 200
	PreviewRoot     string        `yaml:"preview_root"`     // "" = <data dir>/previews
	PreviewTTL      time.Duration `yaml:"preview_ttl"`      // default 24h, at least 1h
	PreviewMaxGB    int           `yaml:"preview_max_gb"`   // default 10
}

func (c ChannelsConfig) validate() error {
	switch {
	case c.PollInterval < 15*time.Minute:
		return fmt.Errorf("channels.poll_interval %s: at least 15m (with a unit)", c.PollInterval)
	case c.KeepDays < 1 || c.KeepDays > 3650:
		return fmt.Errorf("channels.keep_days %d: 1–3650", c.KeepDays)
	case c.InitialBackfill < 0 || c.InitialBackfill > 15:
		return fmt.Errorf("channels.initial_backfill %d: 0–15", c.InitialBackfill)
	case c.MaxGB < 1:
		return fmt.Errorf("channels.max_gb %d: at least 1", c.MaxGB)
	case c.PreviewTTL < time.Hour:
		return fmt.Errorf("channels.preview_ttl %s: at least 1h (with a unit)", c.PreviewTTL)
	case c.PreviewMaxGB < 1:
		return fmt.Errorf("channels.preview_max_gb %d: at least 1", c.PreviewMaxGB)
	}
	return nil
}

// refreshAtRe is "H:MM" or "HH:MM" from 00:00 to 23:59.
var refreshAtRe = regexp.MustCompile(`^([01]?[0-9]|2[0-3]):[0-5][0-9]$`)

type Config struct {
	DataDir         string                `yaml:"-"`
	Listen          string                `yaml:"-"`
	AdminUser       string                `yaml:"-"`
	AdminPassword   string                `yaml:"-"`
	FFmpegPath      string                `yaml:"ffmpeg_path"`
	FFprobePath     string                `yaml:"ffprobe_path"`
	YtDlpPath       string                `yaml:"ytdlp_path"`
	MusicRoot       string                `yaml:"music_root"`
	Language        string                `yaml:"language"`
	Libraries       []LibrarySeed         `yaml:"libraries"`
	Transcode       TranscodeConfig       `yaml:"transcode"`
	Tagging         TaggingConfig         `yaml:"tagging"`
	Lyrics          LyricsConfig          `yaml:"lyrics"`
	Artwork         ArtworkConfig         `yaml:"artwork"`
	Loudness        LoudnessConfig        `yaml:"loudness"`
	Recommendations RecommendationsConfig `yaml:"recommendations"`
	Channels        ChannelsConfig        `yaml:"channels"`
	// YouTubeFeedURL is where channel feeds are read (LARK_YOUTUBE_FEED_URL;
	// tests and e2e point it at a local server).
	YouTubeFeedURL string `yaml:"-"`
	// YouTubeThumbURL is where the 视频 thumbnail proxy reads YouTube's
	// thumbnails, <base>/<id>/mqdefault.jpg (LARK_YOUTUBE_THUMB_URL; default
	// https://i.ytimg.com/vi, which must equal preview.DefaultThumbURL).
	YouTubeThumbURL string `yaml:"-"`
	// YouTubeCoverURL is the base of the video cover links given to pages,
	// <base>/<id>/hqdefault.jpg (LARK_YOUTUBE_COVER_URL; default
	// https://i.ytimg.com/vi; the e2e tests point it at a local server).
	YouTubeCoverURL string `yaml:"-"`
	LastFMAPIKey    string `yaml:"-"`

	// OfflineCache lets the web app keep songs on the phone (its service
	// worker); default true. false is the remote kill switch: /sw.js then
	// removes itself and the app deletes what it cached. LARK_OFFLINE_CACHE
	// (on/off) overrides it.
	OfflineCache bool `yaml:"offline_cache"`
}

func (c Config) DBPath() string   { return filepath.Join(c.DataDir, "lark.db") }
func (c Config) CacheDir() string { return filepath.Join(c.DataDir, "cache") }

func (c Config) ChannelsRoot() string {
	if c.Channels.Root != "" {
		return c.Channels.Root
	}
	return filepath.Join(c.DataDir, "channels")
}

func (c Config) PreviewRoot() string {
	if c.Channels.PreviewRoot != "" {
		return c.Channels.PreviewRoot
	}
	return filepath.Join(c.DataDir, "previews")
}

func Load() (Config, error) {
	c := Config{
		DataDir:         env("LARK_DATA_DIR", "/data"),
		Listen:          env("LARK_LISTEN", ":4600"),
		FFmpegPath:      "ffmpeg",
		FFprobePath:     "ffprobe",
		YtDlpPath:       "yt-dlp",
		MusicRoot:       "/music",
		Language:        "en",
		Transcode:       TranscodeConfig{CacheMaxBytes: 20 << 30, MaxConcurrent: 2, Nice: true},
		Tagging:         TaggingConfig{FolderRules: DefaultFolderRules(), LastFMMinCount: 10},
		Lyrics:          LyricsConfig{Providers: []string{"embedded", "lrclib", "netease", "qq", "kugou"}, PrefetchInterval: 3 * time.Second},
		Artwork:         ArtworkConfig{Providers: []string{"embedded", "folder", "itunes", "netease", "qq"}, PrefetchInterval: 3 * time.Second},
		Loudness:        LoudnessConfig{Enabled: true, Gap: 2 * time.Second, RetryFailedAfter: 720 * time.Hour},
		Recommendations: RecommendationsConfig{Enabled: true, RefreshAt: "02:30"},
		Channels:        ChannelsConfig{Enabled: true, PollInterval: 2 * time.Hour, KeepDays: 10, InitialBackfill: 3, MaxGB: 200, PreviewTTL: 24 * time.Hour, PreviewMaxGB: 10},
	}
	c.OfflineCache = true
	path := env("LARK_CONFIG", filepath.Join(c.DataDir, "config.yaml"))
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return c, fmt.Errorf("read %s: %w", path, err)
	default:
		// Unmarshal onto the defaults so absent keys keep their default value.
		if err := yaml.Unmarshal(b, &c); err != nil {
			return c, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	c.AdminUser = os.Getenv("LARK_ADMIN_USER")
	c.AdminPassword = os.Getenv("LARK_ADMIN_PASSWORD")
	c.LastFMAPIKey = os.Getenv("LARK_LASTFM_API_KEY")
	c.YouTubeFeedURL = env("LARK_YOUTUBE_FEED_URL", "https://www.youtube.com/feeds/videos.xml")
	c.YouTubeThumbURL = env("LARK_YOUTUBE_THUMB_URL", "https://i.ytimg.com/vi")
	c.YouTubeCoverURL = env("LARK_YOUTUBE_COVER_URL", "https://i.ytimg.com/vi")
	if v := os.Getenv("LARK_LANGUAGE"); v != "" {
		c.Language = v
	}
	switch v := strings.ToLower(os.Getenv("LARK_OFFLINE_CACHE")); v {
	case "":
	case "on", "true", "1":
		c.OfflineCache = true
	case "off", "false", "0":
		c.OfflineCache = false
	default:
		return c, fmt.Errorf("LARK_OFFLINE_CACHE %q: must be on or off", v)
	}
	if !refreshAtRe.MatchString(c.Recommendations.RefreshAt) {
		return c, fmt.Errorf("recommendations.refresh_at %q: want HH:MM (00:00–23:59)", c.Recommendations.RefreshAt)
	}
	if err := c.Channels.validate(); err != nil {
		return c, err
	}
	if err := c.Loudness.validate(); err != nil {
		return c, err
	}
	if !slices.Contains(Languages, c.Language) {
		return c, fmt.Errorf("language %q: must be one of %s", c.Language, strings.Join(Languages, ", "))
	}
	return c, nil
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
