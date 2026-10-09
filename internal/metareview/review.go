// Package metareview is the names agent's work list: favorites and YouTube
// downloads whose displayed title, artist, album or year nobody has reviewed
// since they last changed.
package metareview

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"time"

	"github.com/aaronsuns/lark-server/internal/library"
	"github.com/aaronsuns/lark-server/internal/lyrics"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// Scopes of the review list, in the order it is served.
const (
	ScopeAll       = -1
	ScopeFavorites = lyrics.MissingFavorites // anyone's favorite
	ScopeDownloads = lyrics.MissingDownloads // in the download library, or made by a download job
)

// Outcomes a review records.
var Outcomes = map[string]bool{"fixed": true, "ok": true, "skipped": true}

// ErrOutcome: the outcome is not fixed, ok or skipped.
var ErrOutcome = errors.New("outcome must be fixed, ok or skipped")

// Item is one track to review, with its displayed values.
type Item struct {
	Track          library.Track
	Folder         string
	YouTubeTitle   string // newest done download job; "" if none
	YouTubeChannel string
	// *Edited: the field's override was set by a person (or an agent). A
	// field is automatic when it has no override, or the override is what
	// download ingest wrote: a cleaner's title/artist of a done job's video
	// (lyrics.Edited), or for the album the job's channel (ingest files a
	// download under the channel's folder, which becomes the album). Ingest
	// never writes a year, so any year override counts as edited; an
	// explicit "no album" / "no year" is a deliberate choice and counts too.
	TitleEdited, ArtistEdited, AlbumEdited, YearEdited bool
	HasLyrics                                          bool
	// FromDownload: the track is a YouTube download (in the download
	// library, or made by a done download job) rather than a library file.
	FromDownload bool
}

type Service struct {
	DB      *sql.DB
	Library *library.Store
	Lyrics  *lyrics.Service
	Now     func() time.Time // nil: time.Now
}

// Fingerprint hashes what a listener sees of the track's names, so a later
// change to any of them puts the track back on the list.
func Fingerprint(tr library.Track) string {
	year := ""
	if tr.Year != nil {
		year = fmt.Sprint(*tr.Year)
	}
	sum := sha256.Sum256([]byte(tr.Title + "\x1f" + tr.Artist + "\x1f" + tr.Album + "\x1f" + year))
	return hex.EncodeToString(sum[:16])
}

type candidate struct {
	id, grp     int64
	fingerprint sql.NullString
}

const chunk = 200

// each calls fn, in list order (group, then id), for every visible track of
// scope after the cursor whose review is missing or stale, until fn returns
// false.
func (s *Service) each(ctx context.Context, scope int, after int64, fn func(library.Track) bool) error {
	afterGrp := int64(ScopeFavorites)
	if scope != ScopeAll {
		afterGrp = int64(scope)
	} else if after > 0 {
		err := s.DB.QueryRowContext(ctx, `SELECT `+lyrics.GroupSQL+` FROM tracks t JOIN libraries l ON l.id=t.library_id WHERE t.id=?`, after).Scan(&afterGrp)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	for {
		rows, err := s.DB.QueryContext(ctx, `WITH c AS (SELECT t.id AS id, `+lyrics.GroupSQL+` AS grp
			FROM tracks t JOIN libraries l ON l.id=t.library_id
			WHERE t.status!='trashed' AND t.missing_since IS NULL)
			SELECT c.id, c.grp, r.fingerprint FROM c LEFT JOIN metadata_review r ON r.track_id=c.id
			WHERE c.grp IN (0, 1) AND (?1 = -1 OR c.grp = ?1) AND (c.grp > ?2 OR (c.grp = ?2 AND c.id > ?3))
			ORDER BY c.grp, c.id LIMIT ?4`, scope, afterGrp, after, chunk)
		if err != nil {
			return err
		}
		var cs []candidate
		for rows.Next() {
			var c candidate
			if err := rows.Scan(&c.id, &c.grp, &c.fingerprint); err != nil {
				rows.Close()
				return err
			}
			cs = append(cs, c)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		if len(cs) == 0 {
			return nil
		}
		ids := make([]int64, len(cs))
		for i, c := range cs {
			ids[i] = c.id
		}
		trs, err := s.Library.TracksByIDs(ctx, 0, ids)
		if err != nil {
			return err
		}
		byID := make(map[int64]library.Track, len(trs))
		for _, tr := range trs {
			byID[tr.ID] = tr
		}
		for _, c := range cs {
			tr, ok := byID[c.id]
			if !ok || (c.fingerprint.Valid && c.fingerprint.String == Fingerprint(tr)) {
				continue
			}
			if !fn(tr) {
				return nil
			}
		}
		if len(cs) < chunk {
			return nil
		}
		last := cs[len(cs)-1]
		afterGrp, after = last.grp, last.id
	}
}

// List pages through the tracks to review: favorites, then downloads, each
// by id; ScopeAll serves both. after is the last id of the previous page (0
// to start).
func (s *Service) List(ctx context.Context, scope int, after int64, limit int) ([]Item, error) {
	var trs []library.Track
	if err := s.each(ctx, scope, after, func(tr library.Track) bool {
		trs = append(trs, tr)
		return len(trs) < limit
	}); err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(trs))
	for _, tr := range trs {
		it, err := s.item(ctx, tr)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, nil
}

// Count counts what List would serve for scope.
func (s *Service) Count(ctx context.Context, scope int) (int, error) {
	n := 0
	err := s.each(ctx, scope, 0, func(library.Track) bool { n++; return true })
	return n, err
}

func (s *Service) item(ctx context.Context, tr library.Track) (Item, error) {
	it := Item{Track: tr, Folder: path.Dir(tr.Path)}
	if it.Folder == "." {
		it.Folder = ""
	}
	var oTitle, oArtist, oAlbum sql.NullString
	var oYear sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT title, artist, album, year FROM track_overrides WHERE track_id=?`, tr.ID).
		Scan(&oTitle, &oArtist, &oAlbum, &oYear)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return it, err
	}
	jobs, err := s.Lyrics.DoneJobs(ctx, tr.ID)
	if err != nil {
		return it, err
	}
	if n := len(jobs); n > 0 {
		it.YouTubeTitle, it.YouTubeChannel = jobs[n-1][0], jobs[n-1][1] // newest: DoneJobs orders by id
	}
	it.TitleEdited, it.ArtistEdited = lyrics.Edited(oTitle, oArtist, jobs)
	it.AlbumEdited = oAlbum.Valid
	if oAlbum.Valid && oAlbum.String != "" {
		for _, j := range jobs {
			if oAlbum.String == j[1] || oAlbum.String == ytdlp.SafeName(j[1]) {
				it.AlbumEdited = false
			}
		}
	}
	it.YearEdited = oYear.Valid
	err = s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM lyrics WHERE track_id=? AND selected=1),
		EXISTS(SELECT 1 FROM libraries l WHERE l.id=? AND l.is_download_target=1)
		OR EXISTS(SELECT 1 FROM downloads d WHERE d.track_id=? AND d.status='done')`, tr.ID, tr.LibraryID, tr.ID).
		Scan(&it.HasLyrics, &it.FromDownload)
	return it, err
}

// Get is one visible track as the list would serve it, reviewed or not;
// library.ErrNotFound if it is gone, trashed or missing.
func (s *Service) Get(ctx context.Context, trackID int64) (Item, error) {
	trs, err := s.Library.TracksByIDs(ctx, 0, []int64{trackID})
	if err != nil {
		return Item{}, err
	}
	if len(trs) == 0 {
		return Item{}, library.ErrNotFound
	}
	return s.item(ctx, trs[0])
}

// Done records a review of the track as it is displayed now.
func (s *Service) Done(ctx context.Context, trackID int64, outcome string) error {
	if !Outcomes[outcome] {
		return ErrOutcome
	}
	trs, err := s.Library.TracksByIDs(ctx, 0, []int64{trackID})
	if err != nil {
		return err
	}
	if len(trs) == 0 {
		return library.ErrNotFound
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO metadata_review(track_id, reviewed_at, outcome, fingerprint) VALUES (?,?,?,?)
		ON CONFLICT(track_id) DO UPDATE SET reviewed_at=excluded.reviewed_at, outcome=excluded.outcome, fingerprint=excluded.fingerprint`,
		trackID, now().Unix(), outcome, Fingerprint(trs[0]))
	return err
}
