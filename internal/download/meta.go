package download

import (
	"context"
	"encoding/json"
	"time"
	"unicode/utf8"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// Gate runs background yt-dlp work at low priority (the server's search
// limiter: one token, only while no interactive search waits).
type Gate interface {
	Low(ctx context.Context, fn func(ctx context.Context) error) error
}

const (
	// maxDescription caps the description kept per video (runes).
	maxDescription = 2000
	// metaEvery paces the backfill; metaIdle is its sleep once nothing is left.
	metaEvery = 15 * time.Second
	metaIdle  = time.Hour
)

// storeMeta keeps what YouTube says about videoID (video_meta). retag sends
// the songs downloaded from it back to the tagging agent: they now have more
// to go on than when it last looked.
func (s *Service) storeMeta(ctx context.Context, videoID string, m ytdlp.Meta, retag bool) error {
	if m.Tags == nil {
		m.Tags = []string{}
	}
	if m.Categories == nil {
		m.Categories = []string{}
	}
	desc := m.Description
	if utf8.RuneCountInString(desc) > maxDescription {
		desc = string([]rune(desc)[:maxDescription])
	}
	tags, _ := json.Marshal(m.Tags)
	cats, _ := json.Marshal(m.Categories)
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO video_meta(video_id,tags,categories,description,fetched_at) VALUES (?,?,?,?,?)
		ON CONFLICT(video_id) DO UPDATE SET tags=excluded.tags, categories=excluded.categories,
		description=excluded.description, fetched_at=excluded.fetched_at`, videoID, string(tags), string(cats), desc, s.now().Unix()); err != nil {
		return err
	}
	if !retag {
		return nil
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE tagging_state SET agent_at=NULL WHERE track_id IN
		(SELECT track_id FROM downloads WHERE video_id=? AND status='done' AND track_id IS NOT NULL)`, videoID)
	return err
}

// BackfillMeta reads the Meta of up to limit downloaded songs that have none
// (downloaded before it was kept), one through gate every `every`, and
// returns how many it stored. A video yt-dlp can't read is left until the
// next start.
func (s *Service) BackfillMeta(ctx context.Context, gate Gate, every time.Duration, limit int) (int, error) {
	stored := 0
	for range limit {
		id, ok, err := s.nextMissingMeta(ctx)
		if err != nil || !ok {
			return stored, err
		}
		var m ytdlp.Meta
		run := func(ctx context.Context) error {
			var err error
			m, err = s.YT.VideoMeta(ctx, id)
			return err
		}
		if gate != nil {
			err = gate.Low(ctx, run)
		} else {
			err = run(ctx)
		}
		if ctx.Err() != nil {
			return stored, ctx.Err()
		}
		if err != nil {
			s.metaMu.Lock()
			if s.metaFailed == nil {
				s.metaFailed = map[string]bool{}
			}
			s.metaFailed[id] = true
			s.metaMu.Unlock()
			s.Log.Warn("download: video meta", "video", id, "err", ytdlp.LastLine(err.Error()))
		} else if err := s.storeMeta(ctx, id, m, true); err != nil {
			return stored, err
		} else {
			stored++
		}
		select {
		case <-ctx.Done():
			return stored, ctx.Err()
		case <-time.After(every):
		}
	}
	return stored, nil
}

// nextMissingMeta: a video a visible downloaded song came from, with no
// video_meta row and not failed this run; newest download first.
func (s *Service) nextMissingMeta(ctx context.Context) (string, bool, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT d.video_id FROM downloads d JOIN tracks t ON t.id=d.track_id
		WHERE d.status='done' AND d.video_id!='' AND t.status!='trashed'
		  AND NOT EXISTS (SELECT 1 FROM video_meta m WHERE m.video_id=d.video_id)
		ORDER BY d.id DESC LIMIT 50`)
	if err != nil {
		return "", false, err
	}
	defer rows.Close()
	s.metaMu.Lock()
	defer s.metaMu.Unlock()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", false, err
		}
		if !s.metaFailed[id] {
			return id, true, nil
		}
	}
	return "", false, rows.Err()
}

// RunMetaBackfill runs BackfillMeta until ctx is done: a batch every
// metaEvery, then an hour's sleep once nothing is left.
func (s *Service) RunMetaBackfill(ctx context.Context, gate Gate) {
	for ctx.Err() == nil {
		n, err := s.BackfillMeta(ctx, gate, metaEvery, 50)
		if err != nil && ctx.Err() == nil {
			s.Log.Warn("download: video meta backfill", "err", err)
		}
		if n > 0 {
			s.Log.Info("download: video meta backfill", "stored", n)
			continue
		}
		select {
		case <-ctx.Done():
		case <-time.After(metaIdle):
		}
	}
}
