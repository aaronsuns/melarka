package download

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

type Status string

const (
	StatusQueued      Status = "queued"
	StatusDownloading Status = "downloading"
	StatusDone        Status = "done"
	StatusFailed      Status = "failed"
	StatusCancelled   Status = "cancelled"
)

type Job struct {
	ID        int64   `json:"id"`
	UserID    int64   `json:"user_id"`
	Username  string  `json:"username"`
	URL       string  `json:"url"`
	VideoID   string  `json:"video_id"`
	Title     string  `json:"title"`
	Channel   string  `json:"channel"`
	DurationS int     `json:"duration_s"`
	Thumbnail string  `json:"thumbnail"`
	Status    Status  `json:"status"`
	Progress  float64 `json:"progress"`
	Error     string  `json:"error"`
	TrackID   *int64  `json:"track_id"`
	// TrackAvailable: the job's track is still playable in the library (the
	// same test dedupe uses); false for a done job whose song was deleted.
	TrackAvailable bool  `json:"track_available"`
	CreatedAt      int64 `json:"created_at"`
	UpdatedAt      int64 `json:"updated_at"`
}

const jobCols = `d.id, d.user_id, COALESCE(u.username,''), d.url, d.video_id, d.title, d.channel, d.duration_s,
	d.thumbnail, d.status, d.progress, d.error, d.track_id, (d.track_id IS NOT NULL AND ` + trackUsable + `), d.created_at, d.updated_at`

const jobFrom = ` FROM downloads d LEFT JOIN users u ON u.id=d.user_id`

// dupWhere matches a job that makes a new download of video_id=? redundant:
// one still in the queue or running, or a finished one whose track is still
// playable in the library (not trashed, not missing from disk, not broken:
// a track that is gone or unplayable may be downloaded again).
const dupWhere = `d.video_id=? AND (d.status IN ('queued','downloading') OR (d.status='done' AND d.track_id IS NOT NULL
	AND ` + trackUsable + `))`

// trackUsable: job d's track exists and is playable (not trashed, not missing
// from disk, not broken). The one definition of a usable track for jobs.
const trackUsable = `EXISTS (SELECT 1 FROM tracks t WHERE t.id=d.track_id AND t.status!='trashed' AND t.missing_since IS NULL AND t.broken=0)`

type rowScanner interface{ Scan(dest ...any) error }

func scanJob(r rowScanner) (Job, error) {
	var j Job
	var status string
	var track sql.NullInt64
	err := r.Scan(&j.ID, &j.UserID, &j.Username, &j.URL, &j.VideoID, &j.Title, &j.Channel, &j.DurationS,
		&j.Thumbnail, &status, &j.Progress, &j.Error, &track, &j.TrackAvailable, &j.CreatedAt, &j.UpdatedAt)
	if err != nil {
		return Job{}, err
	}
	j.Status = Status(status)
	if track.Valid {
		j.TrackID = &track.Int64
	}
	return j, nil
}

func (s *Service) get(ctx context.Context, id int64) (Job, error) {
	j, err := scanJob(s.DB.QueryRowContext(ctx, `SELECT `+jobCols+jobFrom+` WHERE d.id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	return j, err
}

// visible returns job id when the caller may see it (its owner, or an admin),
// else ErrNotFound — members never learn that other users' jobs exist.
func (s *Service) visible(ctx context.Context, userID int64, isAdmin bool, id int64) (Job, error) {
	j, err := s.get(ctx, id)
	if err != nil {
		return Job{}, err
	}
	if !isAdmin && j.UserID != userID {
		return Job{}, ErrNotFound
	}
	return j, nil
}

// insertOrExisting inserts a queued job for v unless a job that dedupes it
// already exists, in which case that job is returned. The existence check and
// the insert are one statement, so two concurrent enqueues of the same video
// can't both insert.
func (s *Service) insertOrExisting(ctx context.Context, userID int64, v ytdlp.Video) (Job, error) {
	v.Thumbnail = ytdlp.ThumbnailURL(v.ID)
	for range 3 {
		now := s.now().Unix()
		r, err := s.DB.ExecContext(ctx, `INSERT INTO downloads(user_id,url,video_id,title,channel,duration_s,thumbnail,status,created_at,updated_at)
			SELECT ?,?,?,?,?,?,?,'queued',?,? WHERE NOT EXISTS (SELECT 1 FROM downloads d WHERE `+dupWhere+`)`,
			userID, v.URL, v.ID, v.Title, v.Channel, v.DurationS, v.Thumbnail, now, now, v.ID)
		if err != nil {
			return Job{}, err
		}
		if n, err := r.RowsAffected(); err != nil {
			return Job{}, err
		} else if n == 1 {
			id, err := r.LastInsertId()
			if err != nil {
				return Job{}, err
			}
			j, err := s.get(ctx, id)
			if err != nil {
				return Job{}, err
			}
			return j, s.recordRequest(ctx, j, userID)
		}
		j, err := scanJob(s.DB.QueryRowContext(ctx, `SELECT `+jobCols+jobFrom+` WHERE `+dupWhere+` ORDER BY d.id DESC LIMIT 1`, v.ID))
		if err == nil {
			// A request that lands on a job the user cleared from the list
			// brings it back, so they can see it.
			if _, err := s.DB.ExecContext(ctx, `UPDATE downloads SET hidden_at=NULL WHERE id=? AND hidden_at IS NOT NULL`, j.ID); err != nil {
				return Job{}, err
			}
			return j, s.recordRequest(ctx, j, userID)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return Job{}, err
		}
		// The duplicate changed state (e.g. failed) between the two
		// statements: try the insert again.
	}
	return Job{}, errors.New("download: could not enqueue (job state kept changing)")
}

func (s *Service) list(ctx context.Context, userID int64, all bool, limit int) ([]Job, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+jobCols+jobFrom+` WHERE (? OR d.user_id=?) AND d.hidden_at IS NULL
		AND (d.status!='done' OR (d.track_id IS NOT NULL AND `+trackUsable+`))
		ORDER BY d.created_at DESC, d.id DESC LIMIT ?`, all, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// claim atomically moves the oldest queued job to downloading. ok is false
// when the queue is empty.
func (s *Service) claim(ctx context.Context) (j Job, ok bool, err error) {
	var id int64
	err = s.DB.QueryRowContext(ctx, `UPDATE downloads SET status='downloading', progress=0, error='', updated_at=?
		WHERE id=(SELECT id FROM downloads WHERE status='queued' ORDER BY created_at, id LIMIT 1) AND status='queued'
		RETURNING id`, s.now().Unix()).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, err
	}
	j, err = s.get(ctx, id)
	return j, err == nil, err
}

// The writes below only touch a job that is still downloading, so a late
// write from a worker can never resurrect a job that has since moved on.

func (s *Service) setProgress(ctx context.Context, id int64, pct float64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE downloads SET progress=?, updated_at=? WHERE id=? AND status='downloading'`,
		pct, s.now().Unix(), id)
	return err
}

func (s *Service) finishDone(ctx context.Context, id, trackID int64, filePath string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE downloads SET status='done', progress=100, error='', track_id=?, file_path=?, updated_at=?
		WHERE id=? AND status='downloading'`, trackID, filePath, s.now().Unix(), id)
	return err
}

// finish moves a downloading job to failed or cancelled.
func (s *Service) finish(ctx context.Context, id int64, st Status, msg string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE downloads SET status=?, error=?, updated_at=? WHERE id=? AND status='downloading'`,
		string(st), msg, s.now().Unix(), id)
	return err
}

// setStatusFrom moves job id to `to` if it is currently in one of `from`.
func (s *Service) setStatusFrom(ctx context.Context, id int64, to Status, from ...Status) (bool, error) {
	q := `UPDATE downloads SET status=?, updated_at=?`
	if to == StatusQueued {
		q += `, progress=0, error=''`
	}
	q += ` WHERE id=? AND status IN (?` + strings.Repeat(",?", len(from)-1) + `)`
	args := []any{string(to), s.now().Unix(), id}
	for _, f := range from {
		args = append(args, string(f))
	}
	r, err := s.DB.ExecContext(ctx, q, args...)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n > 0, err
}

// retry requeues a failed or cancelled job unless another job already
// covers the same video (queued, running, or done with its track still in
// the library): two jobs must never download the same video into the same
// file. ok is false when nothing changed; err is ErrAlreadyQueued when that
// was because of such a duplicate.
func (s *Service) retry(ctx context.Context, id int64, videoID string) (bool, error) {
	r, err := s.DB.ExecContext(ctx, `UPDATE downloads SET status='queued', progress=0, error='', updated_at=?
		WHERE id=? AND status IN ('failed','cancelled')
		AND NOT EXISTS (SELECT 1 FROM downloads d WHERE d.id<>? AND `+dupWhere+`)`,
		s.now().Unix(), id, id, videoID)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	if err != nil || n > 0 {
		return n > 0, err
	}
	var dup bool
	err = s.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM downloads d WHERE d.id<>? AND `+dupWhere+`)
		AND EXISTS (SELECT 1 FROM downloads WHERE id=? AND status IN ('failed','cancelled'))`, id, videoID, id).Scan(&dup)
	if err != nil {
		return false, err
	}
	if dup {
		return false, ErrAlreadyQueued
	}
	return false, nil
}

// downloadingJobs lists jobs in the downloading state.
func (s *Service) downloadingJobs(ctx context.Context) ([]Job, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+jobCols+jobFrom+` WHERE d.status='downloading'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// requeueDownloading resets jobs a previous process left mid-download.
func (s *Service) requeueDownloading(ctx context.Context) (int64, error) {
	r, err := s.DB.ExecContext(ctx, `UPDATE downloads SET status='queued', progress=0, updated_at=? WHERE status='downloading'`, s.now().Unix())
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}

func (s *Service) trackAt(ctx context.Context, libID int64, rel string) (int64, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM tracks WHERE library_id=? AND rel_path=? AND status!='trashed'`, libID, rel).Scan(&id)
	return id, err
}

// recordRequest adds userID as a requester of job j (idempotent) and, when
// the job is already done with a playable track, favorites it right away.
// j may be a stale snapshot: the job can finish between the caller's read and
// the INSERT below (after which favoriteRequesters would have missed this
// requester), so the job is re-read after the INSERT.
func (s *Service) recordRequest(ctx context.Context, j Job, userID int64) error {
	if _, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO download_requests(download_id,user_id,created_at) VALUES (?,?,?)`,
		j.ID, userID, s.now().Unix()); err != nil {
		return err
	}
	cur, err := s.get(ctx, j.ID)
	if err != nil {
		return err
	}
	if cur.Status != StatusDone || cur.TrackID == nil {
		return nil
	}
	return s.favorite(ctx, []int64{userID}, *cur.TrackID)
}

// favoriteRequesters favorites trackID for every requester of job jobID and
// clears their dislike of it, in one transaction.
func (s *Service) favoriteRequesters(ctx context.Context, jobID, trackID int64) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT user_id FROM download_requests WHERE download_id=?`, jobID)
	if err != nil {
		return err
	}
	var users []int64
	for rows.Next() {
		var u int64
		if err := rows.Scan(&u); err != nil {
			rows.Close()
			return err
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	return s.favorite(ctx, users, trackID)
}

// favorite mirrors personal.Store.SetFavorite: on, and the opposite (dislike) cleared.
func (s *Service) favorite(ctx context.Context, users []int64, trackID int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, u := range users {
		if _, err := tx.ExecContext(ctx, `DELETE FROM dislikes WHERE user_id=? AND track_id=?`, u, trackID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO favorites(user_id,track_id,created_at) VALUES (?,?,?)`, u, trackID, s.now().Unix()); err != nil {
			return err
		}
	}
	return tx.Commit()
}
