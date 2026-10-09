package channels

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/aaronsuns/lark-server/internal/fileutil"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

const (
	playGrace  = 48 * time.Hour
	sweepEvery = time.Hour
)

// midPlayback: some user is listening to episode e (SQL, ? = now - playGrace).
const midPlayback = `EXISTS (SELECT 1 FROM episode_progress p WHERE p.video_id=e.video_id AND p.played=0 AND p.hidden=0
	AND p.position_s>0 AND p.updated_at>?)`

const sweepable = `EXISTS (SELECT 1 FROM episode_files f WHERE f.video_id=e.video_id AND f.status IN ('done','queued','failed'))
	AND NOT EXISTS (SELECT 1 FROM episode_files f WHERE f.video_id=e.video_id AND f.status='downloading')
	AND NOT EXISTS (SELECT 1 FROM episode_keeps k WHERE k.video_id=e.video_id)`

// RunRetention sweeps every hour, and soon after a download finished over
// the cap, an unfollow or a settings change.
func (s *Service) RunRetention(ctx context.Context) {
	s.init()
	for {
		if n, err := s.Sweep(ctx); err != nil && ctx.Err() == nil {
			s.log().Warn("channels: sweep", "err", err)
		} else if n > 0 {
			s.log().Info("channels: expired episodes", "count", n)
		}
		t := time.NewTimer(sweepEvery)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-s.sweepKick:
			t.Stop()
		case <-t.C:
		}
	}
}

// retentionStart (SQL) is when an episode's keep_days clock starts: its
// publication, or when its first live (not expired) file was queued if that
// is later (an episode a backfill fetched long after it came out, or one
// queued again after it had expired: its clock starts over).
const retentionStart = `MAX(e.published_at, COALESCE((SELECT MIN(f.created_at) FROM episode_files f WHERE f.video_id=e.video_id AND f.status!='expired'), 0))`

type victim struct{ video, channel string }

// Sweep expires what retention says must go (see the Task 4 rules) and
// returns how many episodes it expired.
func (s *Service) Sweep(ctx context.Context) (int, error) {
	now := s.now()
	grace := now.Add(-playGrace).Unix()
	rows, err := s.DB.QueryContext(ctx, `SELECT e.video_id, e.channel_id FROM episodes e WHERE `+sweepable+`
		AND NOT `+midPlayback+`
		AND (NOT EXISTS (SELECT 1 FROM channel_follows cf WHERE cf.channel_id=e.channel_id)
		     OR `+retentionStart+` + 86400*(SELECT MAX(COALESCE(cf.keep_days, ?)) FROM channel_follows cf WHERE cf.channel_id=e.channel_id) < ?)
		ORDER BY e.published_at`, grace, s.DefaultKeepDays(), now.Unix())
	if err != nil {
		return 0, err
	}
	var due []victim
	for rows.Next() {
		var v victim
		if err := rows.Scan(&v.video, &v.channel); err != nil {
			rows.Close()
			return 0, err
		}
		due = append(due, v)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, v := range due {
		if err := s.expire(ctx, v.video, v.channel); err != nil {
			return n, err
		}
		n++
	}
	if s.MaxBytes <= 0 {
		return n, nil
	}
	for {
		u, err := s.Usage(ctx)
		if err != nil || u.Bytes <= s.MaxBytes {
			return n, err
		}
		var v victim
		err = s.DB.QueryRowContext(ctx, `SELECT e.video_id, e.channel_id FROM episodes e
			WHERE `+sweepable+` AND EXISTS (SELECT 1 FROM episode_files f WHERE f.video_id=e.video_id AND f.status='done')
			ORDER BY `+midPlayback+`, e.published_at, e.video_id LIMIT 1`, grace).Scan(&v.video, &v.channel)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				s.log().Warn("channels: over the cap with only kept or downloading episodes left", "bytes", u.Bytes, "cap", s.MaxBytes)
				return n, nil
			}
			return n, err
		}
		if err := s.expire(ctx, v.video, v.channel); err != nil {
			return n, err
		}
		n++
	}
}

// expire hard-deletes an episode's files (audio, video, thumbnail) and marks
// its file rows expired. Deletions stay inside channels.root (RelInside); a
// file that can't be deleted (other than already gone) keeps its row, so the
// next sweep tries again.
func (s *Service) expire(ctx context.Context, videoID, channelID string) error {
	if !ytdlp.IsVideoID(videoID) || !ytdlp.IsChannelID(channelID) {
		return fmt.Errorf("channels: refusing to expire %q/%q", channelID, videoID)
	}
	dir := filepath.Join(s.Root, channelID)
	var victims []string
	for _, name := range []string{videoID + ".m4a", videoID + ".v.mp4", videoID + ".jpg"} {
		victims = append(victims, filepath.Join(dir, name))
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT path FROM episode_files WHERE video_id=? AND status='done' AND path!=''`, videoID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var p string
		if rows.Scan(&p) == nil { // a file yt-dlp gave another extension (e.g. .webm audio)
			victims = append(victims, filepath.Join(s.Root, filepath.FromSlash(p)))
		}
	}
	rows.Close()
	for _, p := range victims {
		if _, ok := fileutil.RelInside(s.Root, p); !ok {
			continue
		}
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE episode_files SET status='expired', path='', bytes=0, progress=0, updated_at=?
		WHERE video_id=? AND status IN ('done','queued','failed')`, s.now().Unix(), videoID)
	return err
}
