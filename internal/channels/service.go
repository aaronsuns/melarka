package channels

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// YouTube is the yt-dlp surface the background work uses (a niced *ytdlp.Client).
type YouTube interface {
	VideoInfo(ctx context.Context, id string) (ytdlp.Info, error)
	DownloadEpisode(ctx context.Context, v ytdlp.Video, destNoExt string, kind ytdlp.MediaKind, onProgress func(pct float64)) (string, error)
	Mix(ctx context.Context, videoID string, max int) ([]ytdlp.Video, error)
}

// Feeds reads channel feeds (*FeedClient).
type Feeds interface {
	Fetch(ctx context.Context, channelID string) (Feed, error)
}

// Gate runs background yt-dlp work at low priority (the search limiter's
// low path: one token, only while no interactive search waits).
type Gate interface {
	Low(ctx context.Context, fn func(ctx context.Context) error) error
}

var (
	ErrNotFound     = errors.New("channels: not found")
	ErrBadID        = errors.New("channels: bad id")
	ErrNotFollowing = errors.New("channels: not following that channel")
	ErrFollowLimit  = errors.New("channels: follow limit reached")
	ErrBadSettings  = errors.New("channels: bad settings")
	ErrNotReady     = errors.New("channels: episode file not downloaded yet")
	ErrExpired      = errors.New("channels: episode file was deleted")
)

const (
	MaxFollows          = 100
	defaultKeepDays     = 10
	defaultPollInterval = 2 * time.Hour
	defaultPause        = 2 * time.Second
	defaultMinFree      = 2 << 30 // 2 GiB
)

// ytTimeout bounds one background yt-dlp call (a var for tests).
var ytTimeout = 90 * time.Second

// visibleFor: the episode kinds follow f's user sees (and that get downloaded for them).
const visibleFor = `(e.kind='video' OR (e.kind='short' AND f.include_shorts=1) OR (e.kind='replay' AND f.include_live=1))`

type Service struct {
	DB    *sql.DB
	YT    YouTube
	Feeds Feeds
	Gate  Gate // nil: yt-dlp runs directly (tests)
	// Busy reports music downloads or previews in flight; the episode worker
	// waits while it says true (episodes are the lowest download priority).
	// Contract for the adapter: an error from
	// download.Service.Busy (or the preview check) counts as busy.
	Busy func(ctx context.Context) bool

	Root            string        // channels.root
	PollInterval    time.Duration // 0: 2h
	KeepDays        int           // 0: 10
	InitialBackfill int
	MaxBytes        int64  // cap on episode files; 0: none
	RefreshAt       string // nightly discovery "HH:MM"; "": 02:30
	Log             *slog.Logger
	Now             func() time.Time

	Pause     time.Duration                   // between two channel polls; 0: 2s
	MinFree   int64                           // the worker waits below this much free disk; 0: 2 GiB
	FreeBytes func(dir string) (int64, error) // nil: statfs

	jobTimeout time.Duration // one episode download's deadline; 0: episodeTimeout (tests shorten it)

	once                                    sync.Once
	pollKick, workKick, sweepKick, discKick chan struct{}
	mu                                      sync.Mutex
	running                                 map[string]int64 // refresher name → the user it refreshes now
	videoGen                                map[int64]uint64 // per user: bumped by ClearVideoHistory (videos.go)
	// mixFilled: per video, the fetched_at of its cached Mix whose channel
	// feed was read for the thin-Mix fallback (videos.go): not read again
	// for that row.
	mixFilled map[string]int64
	// The episode worker's push-back cooldown (worker.go): the current
	// wait (0: none) and when claiming may resume.
	coolWait  time.Duration
	coolUntil time.Time
}

func (s *Service) init() {
	s.once.Do(func() {
		s.pollKick = make(chan struct{}, 1)
		s.workKick = make(chan struct{}, 1)
		s.sweepKick = make(chan struct{}, 1)
		s.discKick = make(chan struct{}, 1)
	})
}

func kick(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (s *Service) kickPoll()  { s.init(); kick(s.pollKick) }
func (s *Service) kickWork()  { s.init(); kick(s.workKick) }
func (s *Service) kickSweep() { s.init(); kick(s.sweepKick) }

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

// DefaultKeepDays is channels.keep_days (a follow's keep_days NULL means this).
func (s *Service) DefaultKeepDays() int {
	if s.KeepDays > 0 {
		return s.KeepDays
	}
	return defaultKeepDays
}

func (s *Service) pollInterval() time.Duration { return orDur(s.PollInterval, defaultPollInterval) }

func orDur(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}

// low runs one background yt-dlp call through the gate with its own timeout.
func (s *Service) low(ctx context.Context, fn func(ctx context.Context) error) error {
	run := func(outer context.Context) error {
		ctx, cancel := context.WithTimeout(outer, ytTimeout)
		defer cancel()
		err := fn(ctx)
		// yt-dlp killed by our own deadline reports only "signal: killed":
		// say it timed out, so callers see push-back rather than a failure
		// of the video.
		if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) && outer.Err() == nil {
			return fmt.Errorf("channels: yt-dlp timed out (%v): %w", err, context.DeadlineExceeded)
		}
		return err
	}
	if s.Gate == nil {
		return run(ctx)
	}
	return s.Gate.Low(ctx, run)
}
