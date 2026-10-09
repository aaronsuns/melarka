// Package preview downloads YouTube videos to a temporary area so they can
// be played before (or instead of) keeping them (spec §17.5): served while
// still downloading, deleted preview_ttl after the last access or when over
// the size cap, and handed to the music library or Channels on "keep"
// without downloading again.
package preview

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aaronsuns/lark-server/internal/channels"
	"github.com/aaronsuns/lark-server/internal/download"
	"github.com/aaronsuns/lark-server/internal/fileutil"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

var (
	ErrNotFound  = errors.New("preview: not found")
	ErrBadVideo  = errors.New("preview: bad video id")
	ErrBadMedia  = errors.New("preview: media must be audio, video or hd (and only audio is music)")
	ErrLimit     = errors.New("preview: too many previews")
	ErrNotReady  = errors.New("preview: still downloading")
	ErrFailed    = errors.New("preview: download failed")
	ErrNoChannel = errors.New("preview: the video's channel is unknown")
	// ErrVideoUnavailable: the video has no mp4 of 360p or lower, progressive
	// or merged (ruling R10, production fix R1); an audio preview still works.
	ErrVideoUnavailable = errors.New("preview: no video preview for this video")
	// ErrRetry: YouTube pushed back (a bot check, a network error, a
	// timeout) — nothing was stored as failed; asking again may work
	// (ruling R12).
	ErrRetry = errors.New("preview: YouTube refused for now, try again")
	// ErrNoSpace: under MinFree free disk on the preview root.
	ErrNoSpace = errors.New("preview: not enough free disk space")
	// ErrTooLong: the download hit Lark's own timeout twice in a row.
	ErrTooLong = errors.New("preview: the video takes too long to download")
	// ErrHDUnavailable: the video has no mp4 of 720p or lower for the hd preview.
	ErrHDUnavailable = errors.New("preview: no 720p (or lower) mp4 for this video")
)

// MediaHD is the third preview media: the ≤720p merged mp4 (what an audio+video
// channel episode is), served only once it is complete (spec §18.2).
const MediaHD = "hd"

// CodeHDUnavailable is the error stored on an hd preview that failed for
// want of a 720p (or lower) mp4.
const CodeHDUnavailable = "hd_unavailable"

// CodeVideoUnavailable is the error stored on a video preview that failed
// for want of a format of 360p or lower.
const CodeVideoUnavailable = "video_preview_unavailable"

// CodeQueueTimeout is the error stored on a preview that waited its whole
// timeout for a download slot (a second 高清 behind a long one). Not the
// video's fault: asking again starts afresh.
const CodeQueueTimeout = "preview_queue_timeout"

var errQueueTimeout = errors.New("preview: no download slot within the timeout")

// CodeTooLong is the error stored on a preview whose download ran into
// Lark's own timeout twice in a row (once is treated as push-back).
const CodeTooLong = "preview_too_long"

const (
	defaultMaxPerUser = 20
	defaultSlots      = 2
	defaultWaitCap    = 60 * time.Second
	downloadTimeout   = 15 * time.Minute
	maxOwnTimeouts    = 2
	defaultMinFree    = 2 << 30 // the same floor as the episode worker
	sweepEvery        = 10 * time.Minute
	failedKeep        = time.Hour
	defaultHDSlots    = 1
	hdTimeout         = 45 * time.Minute
	// defaultGrace: a preview played within this long is in use; neither the
	// size cap nor the per-user limit takes it away.
	defaultGrace = 15 * time.Minute
)

type Downloader interface {
	DownloadPreview(ctx context.Context, v ytdlp.Video, destNoExt string, kind ytdlp.MediaKind, onMeta func(ytdlp.PreviewMeta), onProgress func(float64)) (string, error)
	DownloadPreviewHD(ctx context.Context, v ytdlp.Video, destNoExt string, onProgress func(float64), onMeta func(ytdlp.PreviewMeta)) (string, error)
}

type MusicAdopter interface {
	Adopt(ctx context.Context, userID int64, v ytdlp.Video, src string) (download.Job, bool, error)
}

type EpisodeAdopter interface {
	AdoptEpisode(ctx context.Context, userID int64, in channels.Adopt) error
}

// Remuxer writes dst = src with thumb embedded as its cover (no re-encode);
// thumb "" only rewrites the container (a preview is downloaded with
// --fixup never, so its duration and tags need this).
type Remuxer func(ctx context.Context, src, thumb, dst string) error

type Request struct {
	VideoID, Media, Title, Channel string
	DurationS                      int
}

type Preview struct {
	ID        int64  `json:"id"`
	VideoID   string `json:"video_id"`
	Media     string `json:"media"`  // audio | video | hd
	Status    string `json:"status"` // downloading | done | failed
	Title     string `json:"title"`
	Channel   string `json:"channel"`
	ChannelID string `json:"channel_id"`
	DurationS int    `json:"duration_s"`
	Error     string `json:"error"`
	// Progress is 0–100 while an hd or a merged video preview downloads (live state, not stored).
	Progress float64 `json:"progress"`
	// Merged: a video preview that is a merged ≤360p (YouTube refused format
	// 18), servable only once it is complete — show its progress, not the
	// player (live state, not stored; false once done).
	Merged bool `json:"merged"`
	// Queued: still waiting for a download slot (live state, not stored).
	Queued      bool   `json:"queued"`
	Description string `json:"description"`
	StreamURL   string `json:"stream_url"` // /api/v1/previews/<id>/stream
	path        string // relative to Root, once done
	total       int64
}

// failure is the error a failed preview answers with.
func (p Preview) failure() error {
	switch p.Error {
	case CodeVideoUnavailable:
		return ErrVideoUnavailable
	case CodeTooLong:
		return ErrTooLong
	case CodeHDUnavailable:
		return ErrHDUnavailable
	}
	return ErrFailed
}

type KeepResult struct {
	Job       *download.Job `json:"job,omitempty"`
	EpisodeID string        `json:"episode_id,omitempty"`
}

type Service struct {
	DB       *sql.DB
	YT       Downloader // the interactive (not niced) *ytdlp.Client
	Music    MusicAdopter
	Episodes EpisodeAdopter
	Root     string
	TTL      time.Duration // since last access; 0: 24h
	MaxBytes int64         // 0: no cap
	Remux    Remuxer       // nil: a kept file moves as it is
	Log      *slog.Logger
	Now      func() time.Time

	MaxPerUser int                             // live previews per user; 0: 20
	Slots      int                             // downloads at once; 0: 2
	HDSlots    int                             // hd downloads at once (their own slots); 0: 1
	HDTimeout  time.Duration                   // one hd download at most; 0: 45 min
	Grace      time.Duration                   // a preview played this recently is spared; 0: 15 min
	WaitCap    time.Duration                   // how long a request waits for bytes; 0: 60s
	Timeout    time.Duration                   // one download at most; 0: 15 min
	MinFree    int64                           // Start refuses below this much free disk; 0: 2 GiB
	FreeBytes  func(dir string) (int64, error) // nil: statfs
	Thumbs     *Thumbs                         // the 视频 thumbnail proxy; Run sweeps it too (nil: none)

	once sync.Once
	mu   sync.Mutex
	live map[int64]*live
	// serving counts the responses in flight per preview (mu): one somebody
	// is watching is never released, whatever its accessed_at says.
	serving map[int64]int
	slots   chan struct{}
	hdSlots chan struct{}
	base    context.Context
	// running counts download goroutines; Run waits for them on shutdown
	// so none outlives the database.
	running sync.WaitGroup
	// timeouts counts, per "<video>.<media>", own timeouts in a row (mu).
	timeouts map[string]int

	// Ids whose rows are gone, remembered for an hour (their own lock: Get
	// runs under mu). pushedBack: YouTube pushed back on the download, so a
	// client asking hears "retry" rather than "not found". kept: the
	// preview was kept; a hard link to its file stays here so whoever is
	// still playing it by id goes on (ruling R15).
	pbMu       sync.Mutex
	pushedBack map[int64]time.Time
	kept       map[int64]keptFile
}

type keptFile struct {
	path, media string
	at          time.Time
}

// live is a preview being downloaded right now.
type live struct {
	mu       sync.Mutex
	path     string  // absolute path of the growing file
	total    int64   // announced size, 0 unknown
	progress float64 // 0–100, an hd (or merged video) download's own progress
	hd       bool    // an hd download: it does not hold the episode worker back
	merged   bool    // a video preview yt-dlp merges at the end: not servable while it grows
	queued   bool    // waiting for a download slot
	done     chan struct{}
	err      error // ErrFailed, ErrVideoUnavailable or ErrRetry; set before done closes
}

func (l *live) snapshot() (string, int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.path, l.total
}

// result is the download's outcome once done is closed (nil: success).
func (l *live) result() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.err
}

func (s *Service) init() {
	s.once.Do(func() {
		s.live = map[int64]*live{}
		s.serving = map[int64]int{}
		s.pbMu.Lock()
		if s.pushedBack == nil {
			s.pushedBack = map[int64]time.Time{}
		}
		s.kept = map[int64]keptFile{}
		s.pbMu.Unlock()
		n := s.Slots
		if n <= 0 {
			n = defaultSlots
		}
		s.slots = make(chan struct{}, n)
		h := s.HDSlots
		if h <= 0 {
			h = defaultHDSlots
		}
		s.hdSlots = make(chan struct{}, h)
		s.mu.Lock()
		s.timeouts = map[string]int{}
		if s.base == nil {
			s.base = context.Background()
		}
		s.mu.Unlock()
	})
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

func (s *Service) grace() time.Duration {
	if s.Grace > 0 {
		return s.Grace
	}
	return defaultGrace
}

func (s *Service) waitCap() time.Duration {
	if s.WaitCap > 0 {
		return s.WaitCap
	}
	return defaultWaitCap
}

const cols = `id, video_id, media, status, title, channel, channel_id, duration_s, error, path, total, description`

func scan(r interface{ Scan(...any) error }) (Preview, error) {
	var p Preview
	err := r.Scan(&p.ID, &p.VideoID, &p.Media, &p.Status, &p.Title, &p.Channel, &p.ChannelID, &p.DurationS, &p.Error, &p.path, &p.total, &p.Description)
	if errors.Is(err, sql.ErrNoRows) {
		return Preview{}, ErrNotFound
	}
	p.StreamURL = fmt.Sprintf("/api/v1/previews/%d/stream", p.ID)
	return p, err
}

// Get returns preview id: ErrNotFound when there is none, ErrRetry when
// YouTube pushed back on its download within the last hour.
func (s *Service) Get(ctx context.Context, id int64) (Preview, error) {
	p, err := s.get(ctx, id)
	if err != nil {
		return p, err
	}
	s.mu.Lock()
	lv := s.live[id]
	s.mu.Unlock()
	if lv != nil && p.Status == "downloading" {
		lv.mu.Lock()
		p.Progress = lv.progress
		p.Queued = lv.queued
		p.Merged = lv.merged
		lv.mu.Unlock()
	}
	return p, nil
}

// get is Get without the live progress (Start calls it with s.mu held).
func (s *Service) get(ctx context.Context, id int64) (Preview, error) {
	s.init()
	p, err := scan(s.DB.QueryRowContext(ctx, `SELECT `+cols+` FROM previews WHERE id=?`, id))
	if errors.Is(err, ErrNotFound) {
		s.pbMu.Lock()
		_, pb := s.pushedBack[id]
		s.pbMu.Unlock()
		if pb {
			return Preview{}, ErrRetry
		}
	}
	return p, err
}

// touch records an access (best effort: a lost update only shortens the TTL).
func (s *Service) touch(ctx context.Context, id int64) {
	s.DB.ExecContext(ctx, `UPDATE previews SET accessed_at=? WHERE id=?`, s.now().Unix(), id)
}

// Downloading reports whether any preview download runs (episodes wait for it).
func (s *Service) Downloading() bool {
	s.init()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, lv := range s.live {
		if !lv.hd { // an hd download has its own slot and must not starve the episode worker
			return true
		}
	}
	return false
}

// beginServe marks a response in flight for preview id; call the result when it ends.
func (s *Service) beginServe(id int64) (end func()) {
	s.init()
	s.mu.Lock()
	s.serving[id]++
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		if s.serving[id]--; s.serving[id] <= 0 {
			delete(s.serving, id)
		}
		s.mu.Unlock()
		s.touch(context.Background(), id) // the grace period runs from the end of the response
	}
}

// victimLocked returns the least recently played finished preview idle for the
// grace period and not being served (s.mu held); userID 0: any user's.
func (s *Service) victimLocked(ctx context.Context, userID int64) (id int64, video, media string, found bool, err error) {
	q := `SELECT id, video_id, media FROM previews WHERE status='done' AND accessed_at<?`
	args := []any{s.now().Add(-s.grace()).Unix()}
	if userID != 0 {
		q += ` AND user_id=?`
		args = append(args, userID)
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY accessed_at, id`, args...)
	if err != nil {
		return 0, "", "", false, err
	}
	defer rows.Close()
	for rows.Next() {
		if err := rows.Scan(&id, &video, &media); err != nil {
			return 0, "", "", false, err
		}
		if s.serving[id] == 0 {
			return id, video, media, true, nil
		}
	}
	return 0, "", "", false, rows.Err()
}

func ext(media string) string {
	if media == "audio" {
		return ".m4a"
	}
	return ".mp4" // video, hd
}

// kindOf is what a preview is as a channel episode file.
func kindOf(media string) ytdlp.MediaKind {
	if media == "audio" {
		return ytdlp.MediaAudio
	}
	return ytdlp.MediaVideo
}

// Start returns the preview of (video, media) — shared by everyone — and
// starts downloading it when there is none yet (or the last attempt failed;
// a video with no playable video format answers ErrVideoUnavailable until
// the sweeper forgets that, an hour later). A user may have at most
// MaxPerUser live previews they started.
func (s *Service) Start(ctx context.Context, userID int64, req Request) (Preview, error) {
	if !ytdlp.IsVideoID(req.VideoID) {
		return Preview{}, ErrBadVideo
	}
	if req.Media != "audio" && req.Media != "video" && req.Media != MediaHD {
		return Preview{}, ErrBadMedia
	}
	s.init()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.base.Err() != nil {
		return Preview{}, ErrRetry // shutting down: Run no longer waits for new downloads
	}
	now := s.now().Unix()
	p, err := scan(s.DB.QueryRowContext(ctx, `SELECT `+cols+` FROM previews WHERE video_id=? AND media=?`, req.VideoID, req.Media))
	switch {
	case err == nil && p.Status != "failed":
		s.touch(ctx, p.ID)
		return p, nil
	case err == nil && (p.Error == CodeVideoUnavailable || p.Error == CodeTooLong || p.Error == CodeHDUnavailable):
		return Preview{}, p.failure() // until the sweeper forgets it, an hour later
	case err == nil:
		if err := s.removeLocked(ctx, p.ID, p.VideoID, p.Media); err != nil {
			return Preview{}, err
		}
	case !errors.Is(err, ErrNotFound):
		return Preview{}, err
	}
	// Refuse for disk before releasing anything for room: a refused start deletes nothing.
	if err := os.MkdirAll(s.Root, 0o755); err != nil {
		return Preview{}, err
	}
	free, minFree := s.FreeBytes, s.MinFree
	if free == nil {
		free = fileutil.FreeBytes
	}
	if minFree <= 0 {
		minFree = defaultMinFree
	}
	if n, err := free(s.Root); err != nil {
		return Preview{}, err
	} else if n < minFree {
		return Preview{}, ErrNoSpace
	}
	limit := s.MaxPerUser
	if limit <= 0 {
		limit = defaultMaxPerUser
	}
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM previews WHERE user_id=? AND status IN ('downloading','done')`, userID).Scan(&n); err != nil {
		return Preview{}, err
	}
	if n >= limit {
		// Binge watching: release this user's least recently played preview
		// when it has been idle for the grace period.
		rid, rvideo, rmedia, found, err := s.victimLocked(ctx, userID)
		switch {
		case err != nil:
			return Preview{}, err
		case !found:
			return Preview{}, ErrLimit
		}
		if err := s.removeLocked(ctx, rid, rvideo, rmedia); err != nil {
			return Preview{}, err
		}
	}
	r, err := s.DB.ExecContext(ctx, `INSERT INTO previews(video_id,media,status,title,channel,duration_s,user_id,created_at,accessed_at)
		VALUES (?,?,'downloading',?,?,?,?,?,?)`, req.VideoID, req.Media, req.Title, req.Channel, req.DurationS, userID, now, now)
	if err != nil {
		return Preview{}, err
	}
	id, err := r.LastInsertId()
	if err != nil {
		return Preview{}, err
	}
	// The video id was validated above, so the name stays inside Root.
	destNoExt := filepath.Join(s.Root, req.VideoID+"."+req.Media)
	lv := &live{path: destNoExt + ext(req.Media), done: make(chan struct{}), hd: req.Media == MediaHD, queued: true}
	s.live[id] = lv
	v := ytdlp.Video{ID: req.VideoID, Title: req.Title, Channel: req.Channel, URL: ytdlp.WatchURL(req.VideoID), DurationS: req.DurationS}
	s.running.Add(1)
	go func() {
		defer s.running.Done()
		s.run(id, lv, v, req.Media, destNoExt)
	}()
	return s.get(ctx, id)
}

func (s *Service) run(id int64, lv *live, v ytdlp.Video, media, destNoExt string) {
	s.mu.Lock()
	base := s.base
	s.mu.Unlock()
	hd := media == MediaHD
	timeout, slots := s.Timeout, s.slots
	if timeout <= 0 {
		timeout = downloadTimeout
	}
	long := s.HDTimeout
	if long <= 0 {
		long = hdTimeout
	}
	if hd {
		slots, timeout = s.hdSlots, long
	}
	// The slot wait is bounded on its own; the download's timeout starts once it has the slot.
	waitCtx, cancelWait := context.WithTimeout(base, timeout)
	select {
	case slots <- struct{}{}:
		cancelWait()
		lv.mu.Lock()
		lv.queued = false
		lv.mu.Unlock()
	case <-waitCtx.Done():
		cancelWait()
		// Waited the whole timeout for a slot: not this video's fault.
		s.finish(id, lv, v.ID, media, destNoExt, "", fmt.Errorf("%w: %w", errQueueTimeout, waitCtx.Err()), false)
		return
	}
	defer func() { <-slots }()
	// The download's own deadline: a merged video preview, known once yt-dlp
	// has picked its formats, gets 高清's (its timeout follows the hd code).
	ctx, cancel := context.WithCancelCause(base)
	defer cancel(nil)
	began := time.Now()
	deadline := time.AfterFunc(timeout, func() { cancel(context.DeadlineExceeded) })
	defer deadline.Stop()
	onMeta := func(m ytdlp.PreviewMeta) {
		total := m.Size
		if hd || m.Merged {
			total = 0 // merged at the end: nothing is servable while it grows
		}
		lv.mu.Lock()
		lv.total = total
		extend := m.Merged && !lv.merged && !hd
		lv.merged = lv.merged || (m.Merged && !hd)
		lv.mu.Unlock()
		if extend {
			deadline.Reset(max(long-time.Since(began), 0))
		}
		if _, err := s.DB.ExecContext(ctx, `UPDATE previews SET total=?, title=COALESCE(NULLIF(?,''),title), channel=COALESCE(NULLIF(?,''),channel),
			channel_id=?, duration_s=CASE WHEN ?>0 THEN ? ELSE duration_s END, description=COALESCE(NULLIF(?,''),description) WHERE id=?`,
			total, m.Title, m.Channel, m.ChannelID, m.DurationS, m.DurationS, m.Description, id); err != nil {
			s.log().Warn("preview: record details", "preview", id, "err", err)
		}
	}
	// yt-dlp downloads the video stream and then the audio stream, each
	// reporting 0-100: video maps to 0-90, audio to 90-100, never backwards.
	// Only an hd or a merged video preview has progress to show.
	stream, last := 0, 0.0
	onProgress := func(pct float64) {
		if pct < last-10 {
			stream++
		}
		last = pct
		scaled := pct * 0.9
		if stream > 0 {
			scaled = 90 + pct*0.1
		}
		lv.mu.Lock()
		if hd || lv.merged {
			lv.progress = max(lv.progress, scaled)
		}
		lv.mu.Unlock()
	}
	var path string
	var err error
	if hd {
		path, err = s.YT.DownloadPreviewHD(ctx, v, destNoExt, onProgress, onMeta)
	} else {
		path, err = s.YT.DownloadPreview(ctx, v, destNoExt, ytdlp.MediaKind(media), onMeta, onProgress)
	}
	own := err != nil && errors.Is(context.Cause(ctx), context.DeadlineExceeded) && base.Err() == nil
	if own {
		// yt-dlp killed by our timeout: push-back, like a network timeout
		// (unless it keeps happening: finish counts them).
		err = fmt.Errorf("preview: download timed out (%v): %w", err, context.DeadlineExceeded)
	}
	s.finish(id, lv, v.ID, media, destNoExt, path, err, own)
}

// finish records a download's outcome. YouTube pushing back (and a
// shutdown) stores nothing: the row and files go and the id answers
// ErrRetry for a while. Lark's own timeout counts as push-back once, and is
// stored as failed (CodeTooLong) the second time in a row. A video without
// a progressive format is stored as failed with CodeVideoUnavailable;
// anything else as failed with yt-dlp's message. A success runs the size
// cap sweep.
func (s *Service) finish(id int64, lv *live, videoID, media, destNoExt, path string, err error, ownTimeout bool) {
	ctx := context.Background()
	if err == nil {
		rel, ok := fileutil.RelInside(s.Root, path)
		st, serr := os.Stat(path)
		switch {
		case !ok || serr != nil:
			err = fmt.Errorf("preview: yt-dlp reported %q", path)
		default:
			if _, err = s.DB.ExecContext(ctx, `UPDATE previews SET status='done', path=?, size=?, accessed_at=? WHERE id=?`, rel, st.Size(), s.now().Unix(), id); err == nil {
				lv.mu.Lock()
				lv.path = path
				lv.mu.Unlock()
			}
		}
	}
	key := videoID + "." + media
	tooLong := false
	s.mu.Lock()
	if ownTimeout {
		s.timeouts[key]++
		tooLong = s.timeouts[key] >= maxOwnTimeouts
	}
	if !ownTimeout || tooLong {
		delete(s.timeouts, key)
	}
	s.mu.Unlock()
	var result error
	switch {
	case err == nil:
	case errors.Is(err, errQueueTimeout):
		result = ErrFailed
		s.failRow(ctx, id, destNoExt, CodeQueueTimeout, err)
	case tooLong:
		result = ErrTooLong
		s.failRow(ctx, id, destNoExt, CodeTooLong, err)
	case ytdlp.PushBack(err) || errors.Is(err, context.Canceled):
		result = ErrRetry
		s.mu.Lock()
		if rerr := s.removeLocked(ctx, id, videoID, media); rerr != nil {
			s.log().Error("preview: forget pushed-back preview", "preview", id, "err", rerr)
		}
		s.mu.Unlock()
		s.pbMu.Lock()
		s.pushedBack[id] = s.now()
		s.pbMu.Unlock()
		s.log().Warn("preview: YouTube pushed back", "preview", id, "err", ytdlp.LastLine(err.Error()))
	case media == "video" && ytdlp.FormatUnavailable(err):
		result = ErrVideoUnavailable
		s.failRow(ctx, id, destNoExt, CodeVideoUnavailable, err)
	case media == MediaHD && ytdlp.FormatUnavailable(err):
		result = ErrHDUnavailable
		s.failRow(ctx, id, destNoExt, CodeHDUnavailable, err)
	default:
		result = ErrFailed
		s.failRow(ctx, id, destNoExt, ytdlp.LastLine(err.Error()), err)
	}
	lv.mu.Lock()
	lv.err = result
	lv.mu.Unlock()
	close(lv.done)
	s.mu.Lock()
	delete(s.live, id)
	if err == nil && s.MaxBytes > 0 {
		if n, err := s.capLocked(ctx); err != nil {
			s.log().Warn("preview: size cap", "err", err)
		} else if n > 0 {
			s.log().Info("preview: removed previews over the size cap", "count", n)
		}
	}
	s.mu.Unlock()
}

// failRow stores a failed download (its files removed) with error msg.
func (s *Service) failRow(ctx context.Context, id int64, destNoExt, msg string, cause error) {
	removeFiles(destNoExt)
	if _, err := s.DB.ExecContext(ctx, `UPDATE previews SET status='failed', error=? WHERE id=?`, msg, id); err != nil {
		s.log().Error("preview: record failure", "preview", id, "err", err)
	}
	s.log().Warn("preview: download failed", "preview", id, "err", cause)
}

// removeFiles deletes every "<destNoExt>.*" file (media, thumbnail, a remux).
func removeFiles(destNoExt string) {
	dir, base := filepath.Split(destNoExt)
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if !e.IsDir() && strings.HasPrefix(e.Name(), base+".") {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// removeLocked deletes a preview's row and files (s.mu held).
func (s *Service) removeLocked(ctx context.Context, id int64, videoID, media string) error {
	if !ytdlp.IsVideoID(videoID) {
		return fmt.Errorf("preview: bad video id %q in row %d", videoID, id)
	}
	removeFiles(filepath.Join(s.Root, videoID+"."+media))
	_, err := s.DB.ExecContext(ctx, `DELETE FROM previews WHERE id=?`, id)
	return err
}

// Keep hands a finished preview to the music library (audio only: a
// download job, scanned and auto-favorited) or to Channels (the episode,
// kept), then forgets the preview. Its thumbnail becomes the music file's
// embedded cover (Remux) or the episode's cover. The file moves; nothing
// is downloaded again.
func (s *Service) Keep(ctx context.Context, userID, id int64, to string) (KeepResult, error) {
	s.init()
	p, err := s.Get(ctx, id)
	if err != nil {
		return KeepResult{}, err
	}
	s.touch(ctx, id) // the sweeper takes the least recently used first
	switch p.Status {
	case "downloading":
		return KeepResult{}, ErrNotReady
	case "failed":
		return KeepResult{}, p.failure()
	}
	src := filepath.Join(s.Root, filepath.FromSlash(p.path))
	if _, ok := fileutil.RelInside(s.Root, src); !ok {
		return KeepResult{}, fmt.Errorf("preview %d: path %q outside the preview root", id, p.path)
	}
	thumb := strings.TrimSuffix(src, filepath.Ext(src)) + ".jpg"
	if _, err := os.Stat(thumb); err != nil {
		thumb = ""
	}
	if to != "music" && to != "channel" {
		return KeepResult{}, ErrBadMedia
	}
	if to == "music" && p.Media != "audio" {
		return KeepResult{}, ErrBadMedia
	}
	// Someone may still be playing this preview: a hard link to the very
	// same bytes stays for an hour and Serve answers the id from it.
	hold := filepath.Join(s.Root, fmt.Sprintf("kept-%d%s", id, filepath.Ext(src)))
	os.Remove(hold)
	if err := os.Link(src, hold); err != nil {
		s.log().Warn("preview: keep a copy for players", "preview", id, "err", err)
		hold = ""
	}
	res, err := s.adopt(ctx, userID, p, to, src, thumb)
	if err != nil {
		if hold != "" {
			os.Remove(hold)
		}
		return KeepResult{}, err
	}
	if hold != "" {
		s.pbMu.Lock()
		s.kept[id] = keptFile{path: hold, media: p.Media, at: s.now()}
		s.pbMu.Unlock()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return res, s.removeLocked(ctx, id, p.VideoID, p.Media)
}

// adopt hands src to the music library or Channels.
func (s *Service) adopt(ctx context.Context, userID int64, p Preview, to, src, thumb string) (KeepResult, error) {
	id := p.ID
	var res KeepResult
	switch to {
	case "music":
		file := src
		if s.Remux != nil { // with no thumbnail too: the container still needs its fixup
			out := strings.TrimSuffix(src, ".m4a") + ".keep.m4a"
			if err := s.Remux(ctx, src, thumb, out); err != nil {
				s.log().Warn("preview: remux", "preview", id, "err", err)
				os.Remove(out)
			} else {
				file = out
			}
		}
		v := ytdlp.Video{ID: p.VideoID, Title: p.Title, Channel: p.Channel, DurationS: p.DurationS, URL: ytdlp.WatchURL(p.VideoID)}
		job, _, err := s.Music.Adopt(ctx, userID, v, file)
		if err != nil {
			if file != src {
				os.Remove(file)
			}
			return KeepResult{}, err
		}
		res.Job = &job
	case "channel":
		if !ytdlp.IsChannelID(p.ChannelID) {
			return KeepResult{}, ErrNoChannel
		}
		if err := s.Episodes.AdoptEpisode(ctx, userID, channels.Adopt{VideoID: p.VideoID, Title: p.Title, ChannelID: p.ChannelID,
			ChannelTitle: p.Channel, DurationS: p.DurationS, Kind: kindOf(p.Media), Src: src, Thumb: thumb}); err != nil {
			return KeepResult{}, err
		}
		res.EpisodeID = p.VideoID
	}
	return res, nil
}

// Run removes what a previous process left half-downloaded, then sweeps
// every 10 minutes until ctx ends; downloads started later inherit ctx.
func (s *Service) Run(ctx context.Context) {
	s.mu.Lock()
	s.base = ctx
	s.mu.Unlock()
	s.init()
	s.recover(ctx)
	for {
		if n, err := s.Sweep(ctx); err != nil && ctx.Err() == nil {
			s.log().Warn("preview: sweep", "err", err)
		} else if n > 0 {
			s.log().Info("preview: removed previews", "count", n)
		}
		if s.Thumbs != nil {
			if n := s.Thumbs.Sweep(); n > 0 {
				s.log().Debug("preview: removed thumbnails", "count", n)
			}
		}
		t := time.NewTimer(sweepEvery)
		select {
		case <-ctx.Done():
			t.Stop()
			// Start refuses once ctx is done (checked under mu), so after
			// this lock no download can be added: waiting is safe.
			s.mu.Lock()
			s.mu.Unlock()
			s.running.Wait() // the downloads end with ctx
			return
		case <-t.C:
		}
	}
}

// recover removes rows (and files) a previous process left downloading.
func (s *Service) recover(ctx context.Context) {
	s.init()
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.DB.QueryContext(ctx, `SELECT id, video_id, media FROM previews WHERE status='downloading'`)
	if err != nil {
		s.log().Error("preview: recovery", "err", err)
		return
	}
	type stale struct {
		id           int64
		video, media string
	}
	var all []stale
	for rows.Next() {
		var x stale
		if rows.Scan(&x.id, &x.video, &x.media) == nil && s.live[x.id] == nil {
			all = append(all, x)
		}
	}
	if err := rows.Err(); err != nil {
		s.log().Error("preview: recovery", "err", err)
	}
	rows.Close()
	for _, x := range all {
		if err := s.removeLocked(ctx, x.id, x.video, x.media); err != nil {
			s.log().Error("preview: recovery", "preview", x.id, "err", err)
		}
	}
	// Kept copies of an earlier process: nobody can reach them any more.
	ents, _ := os.ReadDir(s.Root)
	for _, e := range ents {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "kept-") {
			os.Remove(filepath.Join(s.Root, e.Name()))
		}
	}
}

// Sweep deletes previews not played for TTL (and failed ones after an
// hour), then the least recently played while over MaxBytes. Downloads in
// progress are never touched.
func (s *Service) Sweep(ctx context.Context) (int, error) {
	s.init()
	s.mu.Lock()
	defer s.mu.Unlock()
	ttl := s.TTL
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	now := s.now()
	s.pbMu.Lock()
	for id, at := range s.pushedBack {
		if now.Sub(at) > failedKeep {
			delete(s.pushedBack, id)
		}
	}
	for id, k := range s.kept {
		if now.Sub(k.at) > failedKeep {
			os.Remove(k.path)
			delete(s.kept, id)
		}
	}
	s.pbMu.Unlock()
	type row struct {
		id           int64
		video, media string
	}
	collect := func(q string, args ...any) ([]row, error) {
		rows, err := s.DB.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.video, &r.media); err != nil {
				return nil, err
			}
			out = append(out, r)
		}
		return out, rows.Err()
	}
	old, err := collect(`SELECT id, video_id, media FROM previews WHERE status!='downloading'
		AND (accessed_at<? OR (status='failed' AND created_at<?))`, now.Add(-ttl).Unix(), now.Add(-failedKeep).Unix())
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range old {
		if err := s.removeLocked(ctx, r.id, r.video, r.media); err != nil {
			return n, err
		}
		n++
	}
	m, err := s.capLocked(ctx)
	return n + m, err
}

// capLocked removes the least recently played finished previews while they
// take more than MaxBytes (s.mu held).
func (s *Service) capLocked(ctx context.Context) (int, error) {
	n := 0
	for s.MaxBytes > 0 {
		var used int64
		if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(size),0) FROM previews WHERE status='done'`).Scan(&used); err != nil || used <= s.MaxBytes {
			return n, err
		}
		id, video, media, found, err := s.victimLocked(ctx, 0)
		if err != nil {
			return n, err
		}
		if !found {
			s.log().Info("preview: over the size cap, every preview is in use")
			return n, nil
		}
		if err := s.removeLocked(ctx, id, video, media); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// FFmpegRemux rewrites the container with stream copy (no re-encode) and
// faststart, embedding a thumbnail as the attached cover picture when there
// is one; niced like transcodes.
func FFmpegRemux(ffmpeg string) Remuxer {
	return func(ctx context.Context, src, thumb, dst string) error {
		args := []string{"-n", "10", ffmpeg, "-nostdin", "-loglevel", "error", "-y", "-i", src}
		if thumb != "" {
			args = append(args, "-i", thumb, "-map", "0:a", "-map", "1:v", "-c", "copy", "-disposition:v:0", "attached_pic")
		} else {
			args = append(args, "-map", "0:a", "-c", "copy")
		}
		out, err := exec.CommandContext(ctx, "nice", append(args, "-movflags", "+faststart", dst)...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("ffmpeg remux: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
}
