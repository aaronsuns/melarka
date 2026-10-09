// Package download is the YouTube download queue: a SQLite-backed job list
// worked by a small pool that runs yt-dlp (via internal/ytdlp), then rescans
// the download library and links the new track to its job.
package download

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/aaronsuns/lark-server/internal/library"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

const (
	playlistCap      = 200
	jobTimeout       = 30 * time.Minute
	resolveTimeout   = 60 * time.Second
	resolveParallel  = 2
	progressInterval = 2 * time.Second
	idlePoll         = 30 * time.Second // safety net if a Kick is ever missed
)

var (
	ErrNotFound = errors.New("download not found")
	ErrNoTarget = errors.New("no library is set as the download target")
	// ErrAlreadyQueued: a retry was refused because another job already
	// covers the same video (queued, running, or done and still playable).
	ErrAlreadyQueued = errors.New("download: video already queued")
)

type Service struct {
	DB      *sql.DB
	YT      *ytdlp.Client
	Library *library.Store
	Scans   *library.Service
	Log     *slog.Logger
	Workers int // default 2
	Now     func() time.Time

	initOnce sync.Once
	kick     chan struct{}
	// resolveSem bounds URL resolves (Enqueue) to resolveParallel at once,
	// each cut off after resolveTimeout (overridable in tests).
	resolveSem     chan struct{}
	resolveTimeout time.Duration
	// forbiddenWaits: the pauses before re-trying a download YouTube
	// refused with a 403 (nil = defaultForbiddenWaits; tests shorten them).
	forbiddenWaits []time.Duration

	// mu guards running. Claiming a job and registering its cancel func
	// happen under one hold of mu, so Cancel never sees a downloading job
	// of this process without its cancel func.
	mu      sync.Mutex
	running map[int64]*runningJob
}

type runningJob struct {
	cancel    context.CancelFunc
	cancelled bool // set by Cancel: the job ends cancelled, not failed
}

func (s *Service) init() {
	s.initOnce.Do(func() {
		s.kick = make(chan struct{}, 1)
		s.resolveSem = make(chan struct{}, resolveParallel)
		s.running = map[int64]*runningJob{}
		if s.Log == nil {
			s.Log = slog.Default()
		}
	})
}

// defaultForbiddenWaits: YouTube answers a file's media with a 403 now and
// then (seen 2026-10 on sun) and the same download usually works seconds
// later, so a 403 is tried twice more before the job fails.
var defaultForbiddenWaits = []time.Duration{5 * time.Second, 20 * time.Second}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Kick wakes an idle worker. It never blocks.
func (s *Service) Kick() {
	s.init()
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// target is the first library marked as the download target.
func (s *Service) target(ctx context.Context) (library.Library, error) {
	libs, err := s.Library.Libraries(ctx)
	if err != nil {
		return library.Library{}, err
	}
	for _, l := range libs {
		if l.DownloadTarget {
			return l, nil
		}
	}
	return library.Library{}, ErrNoTarget
}

// Enqueue is EnqueueURL without the playlist it may have filled.
func (s *Service) Enqueue(ctx context.Context, userID int64, rawURL string) ([]Job, error) {
	res, err := s.EnqueueURL(ctx, userID, rawURL)
	return res.Jobs, err
}

// EnqueueURL validates rawURL, resolves it (a playlist to at most 200
// entries) and queues one job per video, returning an existing job instead
// for any video that is already queued, running, or downloaded and still in
// the library. When rawURL names a whole YouTube list, the list is recorded
// and becomes userID's Lark playlist, in YouTube order: tracks already
// downloaded are placed now, the rest as their jobs finish.
func (s *Service) EnqueueURL(ctx context.Context, userID int64, rawURL string) (Enqueued, error) {
	u, err := ytdlp.ValidURL(rawURL)
	if err != nil {
		return Enqueued{}, err
	}
	if _, err := s.target(ctx); err != nil {
		return Enqueued{}, err
	}
	list, err := s.resolve(ctx, u)
	if err != nil {
		return Enqueued{}, err
	}
	res := Enqueued{Jobs: []Job{}}
	// Recorded before the jobs exist, so a job that finishes from here on
	// finds the list and places itself; one already done is placed below.
	if isWholeList(u, list) {
		ref, err := s.recordList(ctx, userID, list)
		if err != nil {
			return Enqueued{}, err
		}
		res.Playlist = &ref
	}
	for _, v := range list.Videos {
		j, err := s.insertOrExisting(ctx, userID, v)
		if err != nil {
			return Enqueued{}, err
		}
		res.Jobs = append(res.Jobs, j)
	}
	if res.Playlist != nil {
		for _, j := range res.Jobs {
			cur, err := s.get(ctx, j.ID) // j may predate its finish
			if err != nil {
				return Enqueued{}, err
			}
			if cur.Status == StatusDone && cur.TrackID != nil {
				if err := s.placeInLists(ctx, cur.VideoID, *cur.TrackID); err != nil {
					return Enqueued{}, err
				}
			}
		}
	}
	s.Kick()
	return res, nil
}

func (s *Service) resolveTimeoutOrDefault() time.Duration {
	if s.resolveTimeout > 0 {
		return s.resolveTimeout
	}
	return resolveTimeout
}

// resolve runs YT.ResolveList under the resolve limiter and timeout.
func (s *Service) resolve(ctx context.Context, u string) (ytdlp.List, error) {
	s.init()
	select {
	case s.resolveSem <- struct{}{}:
	case <-ctx.Done():
		return ytdlp.List{}, ctx.Err()
	}
	defer func() { <-s.resolveSem }()
	ctx, cancel := context.WithTimeout(ctx, s.resolveTimeoutOrDefault())
	defer cancel()
	return s.YT.ResolveList(ctx, u, playlistCap)
}

// EnqueueVideo queues v (typically a search result) without resolving it
// again; v.URL must still pass ytdlp.ValidURL.
func (s *Service) EnqueueVideo(ctx context.Context, userID int64, v ytdlp.Video) (Job, error) {
	u, err := ytdlp.ValidURL(v.URL)
	if err != nil {
		return Job{}, err
	}
	if strings.TrimSpace(v.ID) == "" {
		return Job{}, ytdlp.ErrBadURL
	}
	if _, err := s.target(ctx); err != nil {
		return Job{}, err
	}
	v.URL = u
	j, err := s.insertOrExisting(ctx, userID, v)
	if err != nil {
		return Job{}, err
	}
	s.Kick()
	return j, nil
}

// List returns jobs newest first: the caller's own, or everyone's when all,
// without done jobs whose song is no longer usable.
func (s *Service) List(ctx context.Context, userID int64, all bool, limit int) ([]Job, error) {
	switch {
	case limit <= 0:
		limit = 200
	case limit > 1000:
		limit = 1000
	}
	return s.list(ctx, userID, all, limit)
}

// Cancel stops a queued or downloading job; on a done, failed or cancelled
// job it instead removes it from the lists (a soft hide: the row, its
// requesters, the track and the file all stay, and a later request for the
// same video brings the job back). Jobs the caller can't see are ErrNotFound.
func (s *Service) Cancel(ctx context.Context, userID int64, isAdmin bool, id int64) error {
	s.init()
	s.mu.Lock()
	defer s.mu.Unlock()
	j, err := s.visible(ctx, userID, isAdmin, id)
	if err != nil {
		return err
	}
	switch j.Status {
	case StatusQueued:
		_, err = s.setStatusFrom(ctx, id, StatusCancelled, StatusQueued)
	case StatusDownloading:
		if r := s.running[id]; r != nil {
			r.cancelled = true
			r.cancel() // the worker records cancelled once yt-dlp has exited
			return nil
		}
		// No worker of this process owns it (Run not started yet): nothing
		// to kill, so record the cancel directly.
		_, err = s.setStatusFrom(ctx, id, StatusCancelled, StatusDownloading)
	case StatusDone, StatusFailed, StatusCancelled:
		_, err = s.DB.ExecContext(ctx, `UPDATE downloads SET hidden_at=? WHERE id=? AND hidden_at IS NULL`, s.now().Unix(), id)
	}
	return err
}

// Retry requeues a failed or cancelled job. Jobs the caller can't see are
// ErrNotFound; one another job already covers is ErrAlreadyQueued; retrying
// a job in any other state is a no-op.
func (s *Service) Retry(ctx context.Context, userID int64, isAdmin bool, id int64) error {
	j, err := s.visible(ctx, userID, isAdmin, id)
	if err != nil {
		return err
	}
	ok, err := s.retry(ctx, id, j.VideoID)
	if err == nil && ok {
		s.Kick()
	}
	return err
}

// Run recovers from a previous crash, then works the queue until ctx is
// done. It returns only after every in-flight job has stopped.
func (s *Service) Run(ctx context.Context) {
	s.init()
	s.recover(ctx)
	n := s.Workers
	if n <= 0 {
		n = 2
	}
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.worker(ctx)
		}()
	}
	s.Kick()
	wg.Wait()
}

func (s *Service) worker(ctx context.Context) {
	for ctx.Err() == nil {
		s.mu.Lock()
		j, ok, err := s.claim(ctx)
		var jctx context.Context
		if ok {
			var cancel context.CancelFunc
			jctx, cancel = context.WithTimeout(ctx, jobTimeout)
			s.running[j.ID] = &runningJob{cancel: cancel}
		}
		s.mu.Unlock()
		if err != nil && ctx.Err() == nil {
			s.Log.Error("download: claim", "err", err)
		}
		if !ok {
			select {
			case <-ctx.Done():
			case <-s.kick:
			case <-time.After(idlePoll):
			}
			continue
		}
		s.Kick() // another idle worker may take the next queued job
		s.process(ctx, jctx, j)
	}
}

// process runs one claimed job to its end state. runCtx is Run's ctx (for
// shutdown); jctx additionally carries the job timeout and Cancel.
func (s *Service) process(runCtx, jctx context.Context, j Job) {
	var (
		final    Status
		msg      string
		trackID  int64
		filePath string
	)
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if r := s.running[j.ID]; r != nil {
			r.cancel()
			delete(s.running, j.ID)
		}
		if runCtx.Err() != nil {
			return // shutting down: the row stays downloading and is requeued on the next start
		}
		var err error
		if final == StatusDone {
			err = s.finishDone(runCtx, j.ID, trackID, filePath)
			if err == nil {
				if ferr := s.favoriteRequesters(runCtx, j.ID, trackID); ferr != nil {
					s.Log.Warn("download: auto-favorite", "job", j.ID, "err", ferr)
				}
				if perr := s.placeInLists(runCtx, j.VideoID, trackID); perr != nil {
					s.Log.Warn("download: place in playlists", "job", j.ID, "err", perr)
				}
			}
		} else {
			err = s.finish(runCtx, j.ID, final, msg)
		}
		if err != nil {
			s.Log.Error("download: record result", "job", j.ID, "err", err)
		}
	}()

	lib, err := s.target(runCtx)
	if err != nil {
		final, msg = StatusFailed, "lark:no_download_target"
		return
	}
	destNoExt, title, artist := destFor(lib, j)

	var lastWrite time.Time
	onProgress := func(pct float64) {
		if now := s.now(); now.Sub(lastWrite) >= progressInterval {
			lastWrite = now
			if err := s.setProgress(jctx, j.ID, pct); err != nil && jctx.Err() == nil {
				s.Log.Warn("download: progress", "job", j.ID, "err", err)
			}
		}
	}
	v := ytdlp.Video{ID: j.VideoID, Title: j.Title, Channel: j.Channel, URL: j.URL, Thumbnail: j.Thumbnail, DurationS: j.DurationS}
	finalPath, err := s.download(jctx, v, destNoExt, onProgress)
	if err != nil {
		switch {
		case runCtx.Err() != nil:
		case s.wasCancelled(j.ID):
			s.cleanup(runCtx, lib, destNoExt, true)
			final = StatusCancelled
		case errors.Is(jctx.Err(), context.DeadlineExceeded):
			s.cleanup(runCtx, lib, destNoExt, true)
			final, msg = StatusFailed, "lark:timeout"
		default:
			s.cleanup(runCtx, lib, destNoExt, false)
			final, msg = StatusFailed, ytdlp.LastLine(err.Error())
		}
		return
	}
	// yt-dlp picks the extension, so trust its reported path — but only
	// if it really is inside the library.
	id, fail := s.ingest(runCtx, j.ID, lib, finalPath, title, artist)
	if fail != "" {
		final, msg = StatusFailed, fail
		return
	}
	final, trackID, filePath = StatusDone, id, finalPath
}

// download runs yt-dlp, re-trying a 403 after each of the forbidden waits.
func (s *Service) download(ctx context.Context, v ytdlp.Video, destNoExt string, onProgress func(float64)) (string, error) {
	waits := s.forbiddenWaits
	if waits == nil {
		waits = defaultForbiddenWaits
	}
	for i := 0; ; i++ {
		path, err := s.YT.Download(ctx, v, destNoExt, onProgress)
		if err == nil || !ytdlp.Forbidden(err) || i == len(waits) {
			return path, err
		}
		s.Log.Info("download: YouTube refused (403), trying again", "video", v.ID, "attempt", i+2)
		select {
		case <-ctx.Done():
			return path, err
		case <-time.After(waits[i]):
		}
	}
}

// ingest scans finalPath (which must lie inside lib) into the library, finds
// its track and writes the cleaned title/artist as overrides; it returns the
// track id, or a "lark:<code>" failure.
func (s *Service) ingest(ctx context.Context, jobID int64, lib library.Library, finalPath, title, artist string) (int64, string) {
	rel, ok := relInside(lib.Root, finalPath)
	if !ok {
		s.Log.Error("download: final path outside library", "job", jobID, "path", finalPath)
		return 0, "lark:not_ingested"
	}
	if _, err := s.Scans.ScanNow(ctx, lib.ID); err != nil {
		s.Log.Warn("download: rescan", "job", jobID, "err", err)
	}
	id, err := s.trackAt(ctx, lib.ID, rel)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			s.Log.Error("download: find track", "job", jobID, "err", err)
		}
		return 0, "lark:not_ingested"
	}
	if err := s.Library.SetOverrides(ctx, id, library.Overrides{Title: &title, Artist: &artist}); err != nil {
		s.Log.Warn("download: set overrides", "job", jobID, "track", id, "err", err)
	}
	return id, ""
}

func (s *Service) wasCancelled(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.running[id]
	return r != nil && r.cancelled
}

// Busy reports whether any music download is queued or running: channel
// episodes (the lowest download priority) wait while it does.
func (s *Service) Busy(ctx context.Context) (bool, error) {
	var b bool
	err := s.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM downloads WHERE status IN ('queued','downloading'))`).Scan(&b)
	return b, err
}
