package channels

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/aaronsuns/lark-server/internal/fileutil"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

const (
	episodeTimeout   = 2 * time.Hour // long videos on a slow line
	maxAttempts      = 3
	retryBase        = 30 * time.Minute
	busyWait         = 30 * time.Second
	roomWait         = 10 * time.Minute
	workIdleMax      = 10 * time.Minute
	progressInterval = 2 * time.Second
	// YouTube pushing back (a bot check, 429, a timeout, a network error)
	// stops all claiming for pushBackBase, doubling up to pushBackMax while it
	// goes on; a successful download resets it.
	pushBackBase = 5 * time.Minute
	pushBackMax  = 6 * time.Hour
	// A download YouTube answers with HTTP 403 (its throttling, gone again
	// soon) waits for its own backoff, forbiddenBase doubling up to
	// forbiddenMax, spending no attempt; after maxTransients it fails as
	// lark:forbidden.
	forbiddenBase = 15 * time.Minute
	forbiddenMax  = 6 * time.Hour
	maxTransients = 6
)

// RunWorker downloads queued episode files one at a time, newest episode
// first, until ctx ends. It yields to music downloads and previews (Busy),
// waits while the disk is short (MinFree) or the cap is reached (MaxBytes),
// and never spins.
func (s *Service) RunWorker(ctx context.Context) {
	s.init()
	s.recoverFiles(ctx)
	for ctx.Err() == nil {
		wait := s.workStep(ctx)
		if wait == 0 {
			continue
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-s.workKick:
			t.Stop()
		case <-t.C:
		}
	}
}

type claimed struct {
	videoID, kind, channelID, title, channel string
	durationS, attempts, transients          int
}

// workStep downloads at most one file; 0 means "look again at once".
func (s *Service) workStep(ctx context.Context) time.Duration {
	if d := s.coolingFor(); d > 0 {
		return d
	}
	if s.Busy != nil && s.Busy(ctx) {
		return busyWait
	}
	if !s.roomFor(ctx) {
		return roomWait
	}
	c, ok, err := s.claim(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.log().Warn("channels: claim", "err", err)
		}
		return time.Minute
	}
	if !ok {
		return s.nextAttemptIn(ctx)
	}
	return s.download(ctx, c)
}

// coolingFor: how long the push-back cooldown still holds claiming back.
func (s *Service) coolingFor() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.coolUntil.Sub(s.now())
}

// pushedBack starts (or doubles) the cooldown and returns its length.
func (s *Service) pushedBack() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.coolWait == 0 {
		s.coolWait = pushBackBase
	} else {
		s.coolWait = min(s.coolWait*2, pushBackMax)
	}
	s.coolUntil = s.now().Add(s.coolWait)
	return s.coolWait
}

func (s *Service) resetCooldown() {
	s.mu.Lock()
	s.coolWait, s.coolUntil = 0, time.Time{}
	s.mu.Unlock()
}

// roomFor: enough free disk under Root and the cap not reached.
func (s *Service) roomFor(ctx context.Context) bool {
	if err := os.MkdirAll(s.Root, 0o755); err != nil {
		s.log().Warn("channels: root", "err", err)
		return false
	}
	free := s.FreeBytes
	if free == nil {
		free = freeBytes
	}
	minFree := s.MinFree
	if minFree <= 0 {
		minFree = defaultMinFree
	}
	if n, err := free(s.Root); err != nil || n < minFree {
		return false
	}
	if s.MaxBytes > 0 {
		used, err := s.Usage(ctx)
		if err != nil || used.Bytes >= s.MaxBytes {
			s.kickSweep()
			return false
		}
	}
	return true
}

func freeBytes(dir string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}

// Usage is the disk space episode files take.
type Usage struct {
	Bytes int64 `json:"bytes"`
	Files int   `json:"files"`
}

func (s *Service) Usage(ctx context.Context) (Usage, error) {
	var u Usage
	err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(bytes),0), COUNT(*) FROM episode_files WHERE status='done'`).Scan(&u.Bytes, &u.Files)
	return u, err
}

// claim moves the newest due queued file to downloading.
func (s *Service) claim(ctx context.Context) (claimed, bool, error) {
	now := s.now().Unix()
	var c claimed
	err := s.DB.QueryRowContext(ctx, `UPDATE episode_files SET status='downloading', progress=0, updated_at=?
		WHERE rowid=(SELECT f.rowid FROM episode_files f JOIN episodes e ON e.video_id=f.video_id
		             WHERE f.status='queued' AND f.next_attempt_at<=? ORDER BY e.published_at DESC, f.kind LIMIT 1)
		RETURNING video_id, kind, attempts, transients`, now, now).Scan(&c.videoID, &c.kind, &c.attempts, &c.transients)
	if errors.Is(err, sql.ErrNoRows) {
		return claimed{}, false, nil
	}
	if err != nil {
		return claimed{}, false, err
	}
	err = s.DB.QueryRowContext(ctx, `SELECT e.channel_id, e.title, c.title, e.duration_s FROM episodes e JOIN channels c ON c.id=e.channel_id
		WHERE e.video_id=?`, c.videoID).Scan(&c.channelID, &c.title, &c.channel, &c.durationS)
	if err != nil {
		// Never leave the row claimed by nobody: back to the queue.
		if _, rerr := s.DB.ExecContext(context.WithoutCancel(ctx), `UPDATE episode_files SET status='queued' WHERE video_id=? AND kind=? AND status='downloading'`,
			c.videoID, c.kind); rerr != nil {
			s.log().Error("channels: unclaim", "video", c.videoID, "err", rerr)
		}
		return claimed{}, false, err
	}
	return c, true, nil
}

// nextAttemptIn: how long until the earliest backed-off file is due.
func (s *Service) nextAttemptIn(ctx context.Context) time.Duration {
	var next sql.NullInt64
	if err := s.DB.QueryRowContext(ctx, `SELECT MIN(next_attempt_at) FROM episode_files WHERE status='queued'`).Scan(&next); err != nil || !next.Valid {
		return workIdleMax
	}
	d := time.Duration(next.Int64-s.now().Unix()) * time.Second
	return min(max(d, time.Second), workIdleMax)
}

// episodeStem is a file's name without extension: "<id>" for audio (and
// its .jpg), "<id>.v" for video, so the two downloads never share a name.
func episodeStem(videoID string, kind ytdlp.MediaKind) string {
	if kind == ytdlp.MediaVideo {
		return videoID + ".v"
	}
	return videoID
}

// download fetches one claimed file and says how long to wait before the
// next claim (0: at once; a cooldown when YouTube pushed back).
func (s *Service) download(ctx context.Context, c claimed) time.Duration {
	kind := ytdlp.MediaKind(c.kind)
	dir := filepath.Join(s.Root, c.channelID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.fileFailed(ctx, c, err, false)
		return 0
	}
	jctx, cancel := context.WithTimeout(ctx, orDur(s.jobTimeout, episodeTimeout))
	defer cancel()
	var last time.Time
	v := ytdlp.Video{ID: c.videoID, Title: c.title, Channel: c.channel, URL: ytdlp.WatchURL(c.videoID), DurationS: c.durationS}
	path, err := s.YT.DownloadEpisode(jctx, v, filepath.Join(dir, episodeStem(c.videoID, kind)), kind, func(pct float64) {
		if now := s.now(); now.Sub(last) >= progressInterval {
			last = now
			// Best effort: a lost progress update only makes the bar lag.
			s.DB.ExecContext(jctx, `UPDATE episode_files SET progress=? WHERE video_id=? AND kind=? AND status='downloading'`, pct, c.videoID, c.kind)
		}
	})
	if ctx.Err() != nil {
		return 0 // shutting down: recoverFiles requeues it on the next start
	}
	if err != nil {
		removePartials(dir, c.videoID, kind)
		if errors.Is(jctx.Err(), context.DeadlineExceeded) {
			s.ownTimeout(ctx, c, err)
			return 0
		}
		if ytdlp.PushBack(err) {
			return s.requeuePushedBack(ctx, c, err)
		}
		if ytdlp.Forbidden(err) && !ytdlp.Unavailable(err) { // a permanent error next to the 403 wins
			s.forbidden(ctx, c, err)
			return 0
		}
		s.fileFailed(ctx, c, err, ytdlp.Unavailable(err))
		return 0
	}
	rel, ok := fileutil.RelInside(s.Root, path)
	st, serr := os.Stat(path)
	if !ok || serr != nil {
		removePartials(dir, c.videoID, kind)
		s.fileFailed(ctx, c, errors.New("lark:not_ingested"), false)
		return 0
	}
	s.resetCooldown()
	if _, err := s.DB.ExecContext(ctx, `UPDATE episode_files SET status='done', path=?, bytes=?, progress=100, error='', updated_at=?
		WHERE video_id=? AND kind=? AND status='downloading'`, rel, st.Size(), s.now().Unix(), c.videoID, c.kind); err != nil {
		s.log().Error("channels: record download", "video", c.videoID, "err", err)
	}
	if s.MaxBytes > 0 {
		s.kickSweep()
	}
	return 0
}

// requeuePushedBack puts a file YouTube refused for now back in the queue
// without spending an attempt and cools the worker down.
func (s *Service) requeuePushedBack(ctx context.Context, c claimed, cause error) time.Duration {
	msg := ytdlp.LastLine(cause.Error())
	if _, err := s.DB.ExecContext(ctx, `UPDATE episode_files SET status='queued', progress=0, error=?, updated_at=?
		WHERE video_id=? AND kind=?`, msg, s.now().Unix(), c.videoID, c.kind); err != nil {
		s.log().Error("channels: requeue", "video", c.videoID, "err", err)
	}
	wait := s.pushedBack()
	s.log().Warn("channels: YouTube pushed back, episode downloads pause", "video", c.videoID, "for", wait, "err", msg)
	return wait
}

// ownTimeout: the download ran into the worker's own deadline, so the file
// is too long for the line rather than YouTube pushing back. The first time
// it goes back to the queue behind its own backoff (the older episodes go
// ahead, nothing cools down); the second time it fails as lark:too_long.
// Attempts are not spent.
func (s *Service) ownTimeout(ctx context.Context, c claimed, cause error) {
	now := s.now()
	r, err := s.DB.ExecContext(ctx, `UPDATE episode_files SET status='failed', timeouts=timeouts+1, error='lark:too_long', progress=0, updated_at=?
		WHERE video_id=? AND kind=? AND timeouts>=1`, now.Unix(), c.videoID, c.kind)
	if err == nil {
		if n, _ := r.RowsAffected(); n > 0 {
			s.log().Warn("channels: episode too long to download in time, giving up", "video", c.videoID, "kind", c.kind)
			return
		}
		_, err = s.DB.ExecContext(ctx, `UPDATE episode_files SET status='queued', timeouts=timeouts+1, error=?, progress=0, next_attempt_at=?, updated_at=?
			WHERE video_id=? AND kind=?`, ytdlp.LastLine(cause.Error()), now.Add(retryBase).Unix(), now.Unix(), c.videoID, c.kind)
	}
	if err != nil {
		s.log().Error("channels: record timeout", "video", c.videoID, "err", err)
	}
	s.log().Warn("channels: episode download timed out, trying again later", "video", c.videoID, "kind", c.kind, "after", orDur(s.jobTimeout, episodeTimeout))
}

// forbidden: YouTube answered this file's download with HTTP 403. It goes
// back to the queue behind its own backoff (the others go ahead, nothing
// cools down, no attempt is spent); after maxTransients it fails as
// lark:forbidden.
func (s *Service) forbidden(ctx context.Context, c claimed, cause error) {
	now := s.now()
	var err error
	if c.transients >= maxTransients {
		_, err = s.DB.ExecContext(ctx, `UPDATE episode_files SET status='failed', transients=transients+1, error='lark:forbidden', progress=0, updated_at=?
			WHERE video_id=? AND kind=?`, now.Unix(), c.videoID, c.kind)
		s.log().Warn("channels: YouTube keeps refusing the episode (403), giving up", "video", c.videoID, "kind", c.kind)
	} else {
		wait := min(forbiddenBase<<c.transients, forbiddenMax)
		_, err = s.DB.ExecContext(ctx, `UPDATE episode_files SET status='queued', transients=transients+1, error=?, progress=0, next_attempt_at=?, updated_at=?
			WHERE video_id=? AND kind=?`, ytdlp.LastLine(cause.Error()), now.Add(wait).Unix(), now.Unix(), c.videoID, c.kind)
		s.log().Warn("channels: YouTube refused the episode (403), trying it again later", "video", c.videoID, "kind", c.kind, "in", wait)
	}
	if err != nil {
		s.log().Error("channels: record 403", "video", c.videoID, "err", err)
	}
}

// fileFailed records a failed attempt: back to the queue with backoff, or
// failed for good after maxAttempts or when the video is unavailable.
func (s *Service) fileFailed(ctx context.Context, c claimed, cause error, permanent bool) {
	attempts := c.attempts + 1
	msg := ytdlp.LastLine(cause.Error())
	now := s.now()
	var err error
	if permanent || attempts >= maxAttempts {
		_, err = s.DB.ExecContext(ctx, `UPDATE episode_files SET status='failed', attempts=?, error=?, updated_at=?
			WHERE video_id=? AND kind=?`, attempts, msg, now.Unix(), c.videoID, c.kind)
		if permanent && err == nil {
			_, err = s.DB.ExecContext(ctx, `UPDATE episodes SET kind='unavailable' WHERE video_id=?`, c.videoID)
		}
	} else {
		next := now.Add(retryBase << (attempts - 1))
		_, err = s.DB.ExecContext(ctx, `UPDATE episode_files SET status='queued', attempts=?, error=?, next_attempt_at=?, updated_at=?
			WHERE video_id=? AND kind=?`, attempts, msg, next.Unix(), now.Unix(), c.videoID, c.kind)
	}
	if err != nil {
		s.log().Error("channels: record failure", "video", c.videoID, "err", err)
	}
	s.log().Warn("channels: episode download failed", "video", c.videoID, "kind", c.kind, "attempt", attempts, "err", msg)
}

// removePartials deletes what a failed or interrupted download of one kind
// left in dir: every "<id>.*" (audio) or "<id>.v.*" (video) file, never the
// thumbnail and never the other kind's files.
func removePartials(dir, videoID string, kind ytdlp.MediaKind) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasPrefix(n, videoID+".") || n == videoID+".jpg" {
			continue
		}
		if strings.HasPrefix(n, videoID+".v.") != (kind == ytdlp.MediaVideo) {
			continue
		}
		os.Remove(filepath.Join(dir, n))
	}
}

// recoverFiles runs once at worker start: files a previous process left
// downloading lose their partial output and go back to the queue.
func (s *Service) recoverFiles(ctx context.Context) {
	rows, err := s.DB.QueryContext(ctx, `SELECT f.video_id, f.kind, e.channel_id FROM episode_files f
		JOIN episodes e ON e.video_id=f.video_id WHERE f.status='downloading'`)
	if err != nil {
		s.log().Error("channels: recovery", "err", err)
		return
	}
	type stale struct{ video, kind, channel string }
	var all []stale
	for rows.Next() {
		var x stale
		if err := rows.Scan(&x.video, &x.kind, &x.channel); err == nil {
			all = append(all, x)
		}
	}
	if err := rows.Err(); err != nil {
		s.log().Error("channels: recovery", "err", err)
	}
	rows.Close()
	for _, x := range all {
		removePartials(filepath.Join(s.Root, x.channel), x.video, ytdlp.MediaKind(x.kind))
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE episode_files SET status='queued', progress=0 WHERE status='downloading'`); err != nil {
		s.log().Error("channels: recovery requeue", "err", err)
	}
}
