package channels

import (
	"context"
	"database/sql"
	"errors"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// Settings are one user's choices for one followed channel.
type Settings struct {
	Media         string `json:"media"`     // audio | video (audio + ≤720p video)
	KeepDays      *int   `json:"keep_days"` // nil: the server default
	Paused        bool   `json:"paused"`
	IncludeShorts bool   `json:"include_shorts"`
	IncludeLive   bool   `json:"include_live"`
}

// ChannelInfo is a channel row as the API shows it.
type ChannelInfo struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Handle      string `json:"handle"`
	Avatar      string `json:"avatar"`
	Description string `json:"description"`
	PolledAt    *int64 `json:"polled_at"`
	LastError   string `json:"last_error"`
}

// MyChannel is one of the user's followed channels with their settings.
type MyChannel struct {
	Channel    ChannelInfo `json:"channel"`
	Settings   Settings    `json:"settings"`
	FollowedAt int64       `json:"followed_at"`
	Unplayed   int         `json:"unplayed"`
	LatestAt   *int64      `json:"latest_published_at"`
}

// upsertChannel stores ch; empty fields never overwrite known ones.
func upsertChannel(ctx context.Context, q interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, ch ytdlp.Channel, now int64) error {
	_, err := q.ExecContext(ctx, `INSERT INTO channels(id,title,handle,avatar,description,created_at) VALUES (?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET title=COALESCE(NULLIF(excluded.title,''),title), handle=COALESCE(NULLIF(excluded.handle,''),handle),
		avatar=COALESCE(NULLIF(excluded.avatar,''),avatar), description=COALESCE(NULLIF(excluded.description,''),description)`,
		ch.ID, ch.Title, ch.Handle, ch.Avatar, ch.Description, now)
	return err
}

// EnsureChannel stores (or refreshes) a resolved channel without following it.
func (s *Service) EnsureChannel(ctx context.Context, ch ytdlp.Channel) error {
	if !ytdlp.IsChannelID(ch.ID) {
		return ErrBadID
	}
	return upsertChannel(ctx, s.DB, ch, s.now().Unix())
}

// Follow makes userID follow ch (at most MaxFollows channels; following
// again changes nothing). The channel is polled right away and its newest
// channels.initial_backfill episodes are fetched.
func (s *Service) Follow(ctx context.Context, userID int64, ch ytdlp.Channel) (MyChannel, error) {
	if !ytdlp.IsChannelID(ch.ID) {
		return MyChannel{}, ErrBadID
	}
	now := s.now().Unix()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return MyChannel{}, err
	}
	defer tx.Rollback()
	var already bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM channel_follows WHERE user_id=? AND channel_id=?)`, userID, ch.ID).Scan(&already); err != nil {
		return MyChannel{}, err
	}
	if !already {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_follows WHERE user_id=?`, userID).Scan(&n); err != nil {
			return MyChannel{}, err
		}
		if n >= MaxFollows {
			return MyChannel{}, ErrFollowLimit
		}
	}
	if err := upsertChannel(ctx, tx, ch, now); err != nil {
		return MyChannel{}, err
	}
	if !already {
		if _, err := tx.ExecContext(ctx, `INSERT INTO channel_follows(user_id,channel_id,want_since,followed_at) VALUES (?,?,?,?)`,
			userID, ch.ID, now, now); err != nil {
			return MyChannel{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE channels SET next_poll_at=0 WHERE id=?`, ch.ID); err != nil {
			return MyChannel{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return MyChannel{}, err
	}
	s.kickPoll()
	return s.myChannel(ctx, userID, ch.ID)
}

// Unfollow stops userID following channelID. The channel, its episodes and
// the user's progress stay (history); files nobody follows any more are
// deleted by the next sweep unless someone kept them.
func (s *Service) Unfollow(ctx context.Context, userID int64, channelID string) error {
	r, err := s.DB.ExecContext(ctx, `DELETE FROM channel_follows WHERE user_id=? AND channel_id=?`, userID, channelID)
	if err != nil {
		return err
	}
	if n, err := r.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFollowing
	}
	s.kickSweep()
	return nil
}

// UpdateSettings replaces userID's settings for channelID. Un-pausing starts
// a fresh "since": episodes published while paused are not fetched.
func (s *Service) UpdateSettings(ctx context.Context, userID int64, channelID string, st Settings) (Settings, error) {
	if st.Media != "audio" && st.Media != "video" {
		return Settings{}, ErrBadSettings
	}
	if st.KeepDays != nil && (*st.KeepDays < 1 || *st.KeepDays > 3650) {
		return Settings{}, ErrBadSettings
	}
	now := s.now().Unix()
	r, err := s.DB.ExecContext(ctx, `UPDATE channel_follows SET media=?, keep_days=?, include_shorts=?, include_live=?,
		want_since=CASE WHEN paused=1 AND ?=0 THEN ? ELSE want_since END, paused=?
		WHERE user_id=? AND channel_id=?`,
		st.Media, st.KeepDays, st.IncludeShorts, st.IncludeLive, st.Paused, now, st.Paused, userID, channelID)
	if err != nil {
		return Settings{}, err
	}
	if n, err := r.RowsAffected(); err != nil {
		return Settings{}, err
	} else if n == 0 {
		return Settings{}, ErrNotFollowing
	}
	if !st.Paused {
		s.kickPoll()
	}
	s.kickSweep() // a shorter keep_days may free space now
	return st, nil
}

const myChannelSQL = `SELECT c.id, c.title, c.handle, c.avatar, c.description, c.polled_at, c.last_error,
	f.media, f.keep_days, f.paused, f.include_shorts, f.include_live, f.followed_at,
	(SELECT MAX(e.published_at) FROM episodes e WHERE e.channel_id=c.id AND ` + visibleFor + `),
	(SELECT COUNT(*) FROM episodes e JOIN episode_files af ON af.video_id=e.video_id AND af.kind='audio' AND af.status='done'
	   LEFT JOIN episode_progress p ON p.video_id=e.video_id AND p.user_id=f.user_id
	   WHERE e.channel_id=c.id AND COALESCE(p.played,0)=0 AND COALESCE(p.hidden,0)=0 AND ` + visibleFor + `)
	FROM channel_follows f JOIN channels c ON c.id=f.channel_id WHERE f.user_id=?`

func scanMyChannel(r interface{ Scan(...any) error }) (MyChannel, error) {
	var mc MyChannel
	var polled, latest, keep sql.NullInt64
	err := r.Scan(&mc.Channel.ID, &mc.Channel.Title, &mc.Channel.Handle, &mc.Channel.Avatar, &mc.Channel.Description,
		&polled, &mc.Channel.LastError, &mc.Settings.Media, &keep, &mc.Settings.Paused, &mc.Settings.IncludeShorts,
		&mc.Settings.IncludeLive, &mc.FollowedAt, &latest, &mc.Unplayed)
	if err != nil {
		return MyChannel{}, err
	}
	if polled.Valid {
		mc.Channel.PolledAt = &polled.Int64
	}
	if latest.Valid {
		mc.LatestAt = &latest.Int64
	}
	if keep.Valid {
		k := int(keep.Int64)
		mc.Settings.KeepDays = &k
	}
	return mc, nil
}

func (s *Service) myChannel(ctx context.Context, userID int64, channelID string) (MyChannel, error) {
	mc, err := scanMyChannel(s.DB.QueryRowContext(ctx, myChannelSQL+` AND c.id=?`, userID, channelID))
	if errors.Is(err, sql.ErrNoRows) {
		return MyChannel{}, ErrNotFollowing
	}
	return mc, err
}

// MyChannels lists userID's followed channels by title.
func (s *Service) MyChannels(ctx context.Context, userID int64) ([]MyChannel, error) {
	rows, err := s.DB.QueryContext(ctx, myChannelSQL+` ORDER BY c.title COLLATE NOCASE, c.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MyChannel{}
	for rows.Next() {
		mc, err := scanMyChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, mc)
	}
	return out, rows.Err()
}
