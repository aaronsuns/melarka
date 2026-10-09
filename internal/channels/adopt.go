package channels

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"

	"github.com/aaronsuns/lark-server/internal/fileutil"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// Adopt is a kept preview handed to Channels.
type Adopt struct {
	VideoID, Title, ChannelID, ChannelTitle string
	DurationS                               int
	Kind                                    ytdlp.MediaKind
	Src, Thumb                              string // Thumb "" when none
}

// AdoptEpisode keeps a previewed video in Channels for userID without
// downloading it again: channel and episode rows are created when missing
// (published "now" when the feed never listed it), the file moves to where
// the worker would have put it — unless that file is already there or
// being downloaded, in which case Src is deleted — and the episode is kept.
// A video file already there is replaced by a bigger one: 保留 at 360p, then
// again once the 720p (高清) is done, upgrades the episode instead of
// throwing the 720p away.
func (s *Service) AdoptEpisode(ctx context.Context, userID int64, in Adopt) error {
	if !ytdlp.IsVideoID(in.VideoID) || !ytdlp.IsChannelID(in.ChannelID) || (in.Kind != ytdlp.MediaAudio && in.Kind != ytdlp.MediaVideo) {
		return ErrBadID
	}
	now := s.now().Unix()
	if err := s.EnsureChannel(ctx, ytdlp.Channel{ID: in.ChannelID, Title: in.ChannelTitle}); err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO episodes(video_id,channel_id,title,published_at,duration_s,kind,seen_at)
		VALUES (?,?,?,?,?,'video',?) ON CONFLICT(video_id) DO NOTHING`, in.VideoID, in.ChannelID, in.Title, now, in.DurationS, now); err != nil {
		return err
	}
	var status, oldRel string
	var oldBytes int64
	err := s.DB.QueryRowContext(ctx, `SELECT status, path, bytes FROM episode_files WHERE video_id=? AND kind=?`, in.VideoID, string(in.Kind)).Scan(&status, &oldRel, &oldBytes)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	dir := filepath.Join(s.Root, in.ChannelID)
	upgrade := false
	if status == "done" && in.Kind == ytdlp.MediaVideo {
		if st, err := os.Stat(in.Src); err == nil && st.Size() > oldBytes {
			upgrade = true
		}
	}
	if upgrade {
		dst := filepath.Join(dir, episodeStem(in.VideoID, in.Kind)+filepath.Ext(in.Src))
		// Moved next to it first, then renamed over it: whoever is playing
		// the old file keeps it open, a new request gets the whole new one.
		tmp := dst + ".lark-upgrade"
		os.Remove(tmp)
		if err := fileutil.Move(in.Src, tmp); err != nil {
			return err
		}
		if err := os.Rename(tmp, dst); err != nil {
			os.Remove(tmp)
			return err
		}
		if old, ok := fileutil.RelInside(s.Root, filepath.Join(s.Root, oldRel)); ok && old != "" {
			if abs := filepath.Join(s.Root, old); abs != dst {
				os.Remove(abs) // the 360p had another extension
			}
		}
		st, err := os.Stat(dst)
		if err != nil {
			return err
		}
		rel, _ := fileutil.RelInside(s.Root, dst)
		if _, err := s.DB.ExecContext(ctx, `UPDATE episode_files SET path=?, bytes=?, updated_at=? WHERE video_id=? AND kind=?`,
			rel, st.Size(), now, in.VideoID, string(in.Kind)); err != nil {
			return err
		}
	} else if status == "done" || status == "downloading" {
		os.Remove(in.Src)
	} else {
		dst := filepath.Join(dir, episodeStem(in.VideoID, in.Kind)+filepath.Ext(in.Src))
		os.Remove(dst) // a failed or expired attempt's leftover, never a done file
		if err := fileutil.Move(in.Src, dst); err != nil {
			return err
		}
		st, err := os.Stat(dst)
		if err != nil {
			return err
		}
		rel, _ := fileutil.RelInside(s.Root, dst)
		if _, err := s.DB.ExecContext(ctx, `INSERT INTO episode_files(video_id,kind,status,path,bytes,progress,created_at,updated_at)
			VALUES (?,?,'done',?,?,100,?,?) ON CONFLICT(video_id,kind) DO UPDATE SET status='done', path=excluded.path,
			bytes=excluded.bytes, progress=100, error='', updated_at=excluded.updated_at`,
			in.VideoID, string(in.Kind), rel, st.Size(), now, now); err != nil {
			return err
		}
	}
	if in.Thumb != "" {
		if err := fileutil.Move(in.Thumb, filepath.Join(dir, in.VideoID+".jpg")); err != nil {
			os.Remove(in.Thumb)
		}
	}
	_, err = s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO episode_keeps(user_id,video_id,created_at) VALUES (?,?,?)`, userID, in.VideoID, now)
	return err
}
