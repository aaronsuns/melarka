package channels

import (
	"context"
	"database/sql"
	"errors"
	"hash/fnv"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

const (
	maxPollBackoff = 24 * time.Hour
	pollIdleMax    = 10 * time.Minute
	infoPerPoll    = 20                  // yt-dlp detail reads per channel per poll
	infoWindow     = 30 * 24 * time.Hour // older episodes are never read again (a pending backfill's picks aside)
	// maxInfoFailures: a video whose details failed to read this many times
	// (a premiere yt-dlp keeps choking on) is marked unavailable and skipped.
	maxInfoFailures = 3
)

// RunPoller reads followed channels' feeds, one channel at a time, until ctx
// ends. A channel is due poll_interval after its last success (+ up to 5 min
// of per-channel jitter); a failure pushes it back exponentially (≤ 24 h).
// With nothing due it sleeps until the next due time, at most 10 min, or a
// new follow. It never spins.
func (s *Service) RunPoller(ctx context.Context) {
	s.init()
	for {
		wait := s.pollStep(ctx)
		if ctx.Err() != nil {
			return
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-s.pollKick:
			t.Stop()
		case <-t.C:
		}
	}
}

// pollStep polls the most overdue channel someone follows un-paused and says
// how long to wait before the next step.
func (s *Service) pollStep(ctx context.Context) time.Duration {
	now := s.now().Unix()
	var id string
	var next int64
	err := s.DB.QueryRowContext(ctx, `SELECT c.id, c.next_poll_at FROM channels c
		WHERE EXISTS (SELECT 1 FROM channel_follows f WHERE f.channel_id=c.id AND f.paused=0)
		ORDER BY c.next_poll_at, c.id LIMIT 1`).Scan(&id, &next)
	if errors.Is(err, sql.ErrNoRows) {
		return pollIdleMax
	}
	if err != nil {
		if ctx.Err() == nil {
			s.log().Warn("channels: pick next poll", "err", err)
		}
		return time.Minute
	}
	if next > now {
		return min(time.Duration(next-now)*time.Second, pollIdleMax)
	}
	if err := s.PollChannel(ctx, id); err != nil && ctx.Err() == nil {
		s.log().Warn("channels: poll", "channel", id, "err", err)
	}
	return orDur(s.Pause, defaultPause)
}

// PollChannel reads channel id's feed once: stores new episodes, reads the
// details (duration, live state, availability) of the ones someone may
// want, and queues downloads. A feed failure only backs this channel off.
func (s *Service) PollChannel(ctx context.Context, id string) error {
	feed, err := s.Feeds.Fetch(ctx, id)
	if err != nil {
		if ctx.Err() == nil {
			s.pollFailed(ctx, id, err)
		}
		return err
	}
	if err := s.storeFeed(ctx, id, feed); err != nil {
		return err
	}
	complete, err := s.classify(ctx, id)
	if err != nil {
		return err
	}
	if err := s.queueWanted(ctx, id, complete); err != nil {
		return err
	}
	now := s.now()
	next := now.Add(s.pollInterval() + jitter(id))
	if _, err := s.DB.ExecContext(ctx, `UPDATE channels SET polled_at=?, next_poll_at=?, poll_failures=0, last_error='',
		title=CASE WHEN title='' THEN ? ELSE title END WHERE id=?`, now.Unix(), next.Unix(), cleanLine(feed.Title), id); err != nil {
		return err
	}
	s.kickWork()
	return nil
}

// jitter spreads channels over 5 minutes, the same for a channel every time.
func jitter(id string) time.Duration {
	h := fnv.New32a()
	h.Write([]byte(id))
	return time.Duration(h.Sum32()%300) * time.Second
}

func (s *Service) pollFailed(ctx context.Context, id string, cause error) {
	var fails int
	if err := s.DB.QueryRowContext(ctx, `UPDATE channels SET poll_failures=poll_failures+1 WHERE id=? RETURNING poll_failures`, id).Scan(&fails); err != nil {
		s.log().Warn("channels: record poll failure", "channel", id, "err", err)
		return
	}
	backoff := maxPollBackoff
	if fails <= 10 {
		backoff = min(s.pollInterval()<<(fails-1), maxPollBackoff)
	}
	msg := truncate(cause.Error(), 200) // not ytdlp.LastLine: this is our own HTTP error, not yt-dlp's
	if _, err := s.DB.ExecContext(ctx, `UPDATE channels SET next_poll_at=?, last_error=? WHERE id=?`,
		s.now().Add(backoff).Unix(), msg, id); err != nil {
		s.log().Warn("channels: record poll failure", "channel", id, "err", err)
	}
}

// storeFeed inserts new entries (a Short is known from its link) and keeps
// titles/descriptions of known ones current. Feed text is stripped of
// control characters; a video listed twice is stored from its first entry.
func (s *Service) storeFeed(ctx context.Context, id string, feed Feed) error {
	now := s.now().Unix()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	seen := map[string]bool{}
	for _, e := range feed.Entries {
		if seen[e.VideoID] {
			continue
		}
		seen[e.VideoID] = true
		kind := "unknown"
		if e.Short {
			kind = "short"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO episodes(video_id,channel_id,title,description,published_at,kind,seen_at)
			VALUES (?,?,?,?,?,?,?)
			ON CONFLICT(video_id) DO UPDATE SET title=excluded.title,
			description=CASE WHEN excluded.description!='' THEN excluded.description ELSE episodes.description END
			WHERE episodes.channel_id=excluded.channel_id`,
			e.VideoID, id, cleanLine(e.Title), cleanText(e.Description), e.Published.Unix(), kind, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// cleanLine is feed text for one line (a title): control characters go,
// line breaks and tabs become spaces.
func cleanLine(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return ' '
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, strings.ToValidUTF8(s, "")))
}

// cleanText is feed text over several lines (a description): control
// characters go except line breaks and tabs.
func cleanText(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, strings.ToValidUTF8(s, "")))
}

// backfillCandidates (SQL, ? = InitialBackfill; f is a follow) is the
// newest episodes before f's want_since that f may still want: read or not
// yet read. These are the backfill's picks, read whatever their age.
const backfillCandidates = `SELECT b.video_id FROM episodes b WHERE b.channel_id=f.channel_id AND b.published_at<f.want_since
	AND (b.kind IN ('unknown','live','upcoming') OR b.kind='video' OR (b.kind='short' AND f.include_shorts=1) OR (b.kind='replay' AND f.include_live=1))
	ORDER BY b.published_at DESC LIMIT ?`

// backfillPicks (SQL, ? = InitialBackfill; f is a follow) is the newest
// episodes of a kind f sees published before its want_since: what f's
// backfill queues.
const backfillPicks = `SELECT e.video_id FROM episodes e WHERE e.channel_id=f.channel_id AND e.published_at<f.want_since AND ` + visibleFor + `
	ORDER BY e.published_at DESC LIMIT ?`

// requeueable (SQL; x is an episode_files row, followedAt when a follower
// followed): the file expired before that follower followed (another
// follower's retention, or nobody following), so they never had it, and it
// did not fail for good (attempts, own timeouts or 403s used up). What the
// follower's own retention expired never qualifies: nothing cycles between
// download and expiry.
func requeueable(followedAt string) string {
	return `x.status='expired' AND x.updated_at<` + followedAt + ` AND x.attempts<` + strconv.Itoa(maxAttempts) +
		` AND x.timeouts<2 AND x.transients<=` + strconv.Itoa(maxTransients)
}

// healBackfill (SQL, ? = InitialBackfill twice; f is a follow): f's backfill
// ran, yet none of its picks is on disk or on its way and some file of a
// kind f wants expired before f followed — a backfill that met another follower's expired files
// (before those were re-queued). It runs again.
var healBackfill = `f.backfill_pending=0
	AND NOT EXISTS (SELECT 1 FROM episode_files x WHERE x.status IN ('done','queued','downloading') AND x.video_id IN (` + backfillPicks + `))
	AND EXISTS (SELECT 1 FROM episode_files x WHERE ` + requeueable("f.followed_at") + ` AND (x.kind='audio' OR f.media='video') AND x.video_id IN (` + backfillPicks + `))`

// classify reads yt-dlp details for undecided episodes someone may want
// (published since a follower's want_since, or any while a backfill is
// pending), newest first, at most infoPerPoll, within the last 30 days; a
// pending backfill's picks (backfillCandidates) are read whatever their age.
//
// YouTube pushing back (a bot check, "try again later", 429, a timeout, a
// network error) ends this poll's reads and marks nothing; so do
// maxInfoFailures generic failures in a row (the environment, not the
// videos). complete is then false and backfill waits for the next poll.
// Any other failure is that video's alone, and the others go on: it earns a
// strike only when other reads of the same poll succeeded (maxInfoFailures
// strikes mark it unavailable).
func (s *Service) classify(ctx context.Context, id string) (complete bool, err error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT e.video_id FROM episodes e
		WHERE e.channel_id=? AND e.kind IN ('unknown','live','upcoming')
		  AND EXISTS (SELECT 1 FROM channel_follows f WHERE f.channel_id=e.channel_id AND f.paused=0
		              AND ((e.published_at>=? AND (f.backfill_pending=1 OR e.published_at>=f.want_since))
		                   OR (f.backfill_pending=1 AND e.video_id IN (`+backfillCandidates+`))))
		ORDER BY e.published_at DESC LIMIT ?`, id, s.now().Add(-infoWindow).Unix(), max(s.InitialBackfill, 0), infoPerPoll)
	if err != nil {
		return false, err
	}
	var ids []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return false, err
		}
		ids = append(ids, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	var failed []string // generic failures, not yet struck
	succeeded, streak := false, 0
	complete = true
reads:
	for _, v := range ids {
		var in ytdlp.Info
		err := s.low(ctx, func(ctx context.Context) error {
			var err error
			in, err = s.YT.VideoInfo(ctx, v)
			return err
		})
		switch {
		case err == nil:
			succeeded, streak = true, 0
			_, err = s.DB.ExecContext(ctx, `UPDATE episodes SET kind=?, duration_s=?, info_failures=0,
				description=CASE WHEN description='' THEN ? ELSE description END WHERE video_id=?`,
				kindOf(in), in.DurationS, truncate(in.Description, maxDescription), v)
		case ctx.Err() != nil:
			return false, ctx.Err()
		case ytdlp.Unavailable(err):
			succeeded, streak = true, 0 // YouTube answered about this video
			_, err = s.DB.ExecContext(ctx, `UPDATE episodes SET kind='unavailable' WHERE video_id=?`, v)
		case ytdlp.PushBack(err):
			s.log().Warn("channels: video details: YouTube pushed back, reading the rest next poll", "channel", id, "video", v, "err", ytdlp.LastLine(err.Error()))
			complete = false
			break reads
		default:
			s.log().Warn("channels: video details", "video", v, "err", ytdlp.LastLine(err.Error()))
			failed = append(failed, v)
			if streak++; streak >= maxInfoFailures {
				s.log().Warn("channels: video details keep failing, reading the rest next poll", "channel", id)
				complete = false
				break reads
			}
			err = nil
		}
		if err != nil {
			return false, err
		}
	}
	// The failures that ended a stopped poll are the environment's: no strikes.
	if !complete {
		failed = failed[:len(failed)-min(streak, len(failed))]
	}
	if succeeded {
		for _, v := range failed {
			if _, err := s.DB.ExecContext(ctx, `UPDATE episodes SET info_failures=info_failures+1,
				kind=CASE WHEN info_failures+1>=? THEN 'unavailable' ELSE kind END WHERE video_id=?`, maxInfoFailures, v); err != nil {
				return false, err
			}
		}
	}
	return complete, nil
}

// kindOf maps yt-dlp's details to an episode kind.
func kindOf(in ytdlp.Info) string {
	switch {
	case in.Availability != "" && in.Availability != "public" && in.Availability != "unlisted":
		return "unavailable"
	case in.LiveStatus == "is_live":
		return "live"
	case in.LiveStatus == "is_upcoming":
		return "upcoming"
	case in.MediaType == "short":
		return "short"
	case in.LiveStatus == "was_live" || in.LiveStatus == "post_live":
		return "replay"
	}
	return "video"
}

// queueWanted queues downloads: for every un-paused follower, episodes of a
// kind they see published since their want_since; for a follower whose
// backfill is pending (and when this poll's details are complete), the
// newest InitialBackfill such episodes from before. Audio always; video too
// when that follower chose audio + video. One row per (video, kind), so
// several followers never download twice. A file that expired before the
// wanting follower followed (requeueable) is queued again from scratch, its
// retention clock restarted; one the follower's own retention expired is
// not. A follow whose finished backfill met such files backfills again
// (healBackfill).
func (s *Service) queueWanted(ctx context.Context, id string, complete bool) error {
	type want struct {
		video, media string
		followedAt   int64
	}
	var wants []want
	collect := func(q string, args ...any) error {
		rows, err := s.DB.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var w want
			if err := rows.Scan(&w.video, &w.media, &w.followedAt); err != nil {
				return err
			}
			wants = append(wants, w)
		}
		return rows.Err()
	}
	if err := collect(`SELECT e.video_id, f.media, f.followed_at FROM episodes e JOIN channel_follows f ON f.channel_id=e.channel_id
		WHERE e.channel_id=? AND f.paused=0 AND e.published_at>=f.want_since AND `+visibleFor, id); err != nil {
		return err
	}
	var backfill []int64
	if complete {
		n := max(s.InitialBackfill, 0)
		rows, err := s.DB.QueryContext(ctx, `SELECT f.user_id, f.backfill_pending FROM channel_follows f
			WHERE f.channel_id=? AND f.paused=0 AND (f.backfill_pending=1 OR (`+healBackfill+`))`, id, n, n)
		if err != nil {
			return err
		}
		for rows.Next() {
			var u int64
			var pending bool
			if err := rows.Scan(&u, &pending); err != nil {
				rows.Close()
				return err
			}
			if !pending {
				s.log().Info("channels: backfill again, its picks had expired before the follow", "channel", id, "user", u)
			}
			backfill = append(backfill, u)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, u := range backfill {
			if err := collect(`SELECT e.video_id, f.media, f.followed_at FROM channel_follows f JOIN episodes e
				ON e.video_id IN (`+backfillPicks+`) WHERE f.user_id=? AND f.channel_id=?`, n, u, id); err != nil {
				return err
			}
		}
	}
	now := s.now().Unix()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	enqueue := func(w want, kind string) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO episode_files AS x (video_id,kind,status,created_at,updated_at)
			VALUES (?,?,'queued',?,?) ON CONFLICT(video_id,kind) DO UPDATE SET status='queued', attempts=0, timeouts=0, transients=0, error='',
			  next_attempt_at=0, path='', bytes=0, progress=0, created_at=excluded.created_at, updated_at=excluded.updated_at
			WHERE `+requeueable("?"), w.video, kind, now, now, w.followedAt)
		return err
	}
	for _, w := range wants {
		if err := enqueue(w, "audio"); err != nil {
			return err
		}
		if w.media == "video" {
			if err := enqueue(w, "video"); err != nil {
				return err
			}
		}
	}
	for _, u := range backfill {
		if _, err := tx.ExecContext(ctx, `UPDATE channel_follows SET backfill_pending=0 WHERE user_id=? AND channel_id=?`, u, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
