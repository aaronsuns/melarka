// Package recommend builds each user's "为你推荐" list: seeds from
// the user's most-played and newest favorite tracks, candidates from each
// seed's YouTube Mix and Last.fm's similar tracks, filtered against the
// library, downloads and dismissals, ranked by how many seeds agree.
//
// All yt-dlp work goes through Gate (the search limiter's low-priority
// path, so interactive searches are never starved) and runs niced (the
// caller wires a niced yt-dlp runner). Refreshes happen one user at a time
// in Run: nightly at RefreshAt and on demand.
package recommend

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/aaronsuns/lark-server/internal/lastfm"
	"github.com/aaronsuns/lark-server/internal/library"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// YouTube is the yt-dlp surface recommendations use (*ytdlp.Client).
type YouTube interface {
	SearchTop(ctx context.Context, q string, n int) ([]ytdlp.Video, error)
	Mix(ctx context.Context, videoID string, max int) ([]ytdlp.Video, error)
}

// Similar is the Last.fm surface recommendations use (*lastfm.Client).
type Similar interface {
	TrackSimilar(ctx context.Context, artist, track string, limit int) ([]lastfm.SimilarTrack, error)
	ArtistSimilar(ctx context.Context, artist string, limit int) ([]lastfm.SimilarArtist, error)
}

// Gate runs background yt-dlp work at low priority (one search token, only
// while no interactive search waits).
type Gate interface {
	Low(ctx context.Context, fn func(ctx context.Context) error) error
}

// Seed kinds; also the reason shown ("因为你常听 X" / "因为你收藏了 X").
const (
	KindPlayed   = "played"
	KindFavorite = "favorite"
)

// Limits.
const (
	maxSeeds          = 10
	maxPlayedSeeds    = 5 // when there are enough favorites to fill the rest
	playWindow        = 30 * 24 * time.Hour
	minPlayedSeconds  = 30
	mixEntries        = 50
	lastfmPerSeed     = 5
	lastfmArtists     = 2  // artist.getSimilar fallback
	keepTop           = 30 // about 30 shown
	seedSearchesDay   = 10
	otherSearchesDay  = 20 // Last.fm suggestions resolved to YouTube
	missRetry         = 30 * 24 * time.Hour
	hitRetry          = 90 * 24 * time.Hour
	durationTolerance = 10 // seconds, seed search match
	ytTimeout         = 90 * time.Second
	minDurationS      = 60
	maxDurationS      = 600
	lastfmAgreement   = 0.5
	favoriteBonus     = 0.25 // for a favorite made just now, fading to 0 over 30 days
)

var (
	ErrBadVideo = errors.New("recommend: bad video id")
)

type Service struct {
	DB      *sql.DB
	Library *library.Store
	YT      YouTube
	LastFM  Similar // nil: Last.fm suggestions off
	Gate    Gate    // nil: yt-dlp runs directly (tests)
	Log     *slog.Logger
	Now     func() time.Time

	RefreshAt  string        // nightly refresh, "HH:MM" server time; default "02:30"
	ErrBackoff time.Duration // after a failed refresh; default 1m
	Pause      time.Duration // between two users; default 2s

	once    sync.Once
	wake    chan struct{}
	mu      sync.Mutex
	running int64 // user being refreshed now
}

func (s *Service) init() {
	s.once.Do(func() { s.wake = make(chan struct{}, 1) })
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// low runs one yt-dlp call through the gate with its own timeout.
func (s *Service) low(ctx context.Context, fn func(ctx context.Context) error) error {
	run := func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, ytTimeout)
		defer cancel()
		return fn(ctx)
	}
	if s.Gate == nil {
		return run(ctx)
	}
	return s.Gate.Low(ctx, run)
}
