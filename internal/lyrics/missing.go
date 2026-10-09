package lyrics

import (
	"context"
	"database/sql"
	"errors"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// Groups of the missing-lyrics list, in the order it is served.
const (
	MissingAll       = -1
	MissingFavorites = 0 // anyone's favorite
	MissingDownloads = 1 // in the download library, or made by a download job
	MissingOther     = 2
)

// Missing is one track without selected lyrics, as the lyrics agent's work
// list carries it (display fields come from the library).
type Missing struct {
	ID             int64
	YouTubeTitle   string // from the newest done download job of the track; "" if none
	YouTubeChannel string
	LastLookupAt   *int64 // lyrics_lookup.attempted_at, nil if never looked up
	Found          *bool
	// ReportedWrong: a user reported the last selected lyrics wrong and no
	// other candidate was left (lyrics_lookup.wrong_at); those words are
	// rejected for good, so another version is needed.
	ReportedWrong bool
	// TitleEdited / ArtistEdited: the override was typed by a person. A field
	// is automatic when its override is unset or empty, or equals what a
	// cleaner version (ytdlp.CleanTitle, V2, V1) made of a done job's video.
	TitleEdited, ArtistEdited bool
}

// cleaners are every (title, artist) cleaner downloads were ever tagged with.
var cleaners = []func(title, channel string) (string, string){ytdlp.CleanTitle, ytdlp.CleanTitleV2, ytdlp.CleanTitleV1}

// edited reports which override fields no cleaner produced from jobs.
func edited(oTitle, oArtist sql.NullString, jobs [][2]string) (title, artist bool) {
	title, artist = oTitle.String != "", oArtist.String != ""
	for _, j := range jobs {
		for _, clean := range cleaners {
			t, a := clean(j[0], j[1])
			if t == oTitle.String {
				title = false
			}
			if a == oArtist.String {
				artist = false
			}
		}
	}
	return title, artist
}

// Edited reports which of the title/artist overrides no cleaner produced
// from the track's done download jobs ((video title, channel) pairs): those
// a person (or an agent) typed. An unset or empty override is automatic.
func Edited(oTitle, oArtist sql.NullString, jobs [][2]string) (title, artist bool) {
	return edited(oTitle, oArtist, jobs)
}

// DoneJobs lists the (video title, channel) of the track's done downloads,
// oldest first.
func (s *Service) DoneJobs(ctx context.Context, trackID int64) ([][2]string, error) {
	return s.doneJobs(ctx, trackID)
}

// GroupSQL is the SQL expression of a track's group (MissingFavorites,
// MissingDownloads or MissingOther) over tracks t JOIN libraries l.
const GroupSQL = missingGroup

// missingGroup is the track's group; missingBase lists every visible track
// with no selected lyrics, not marked "no lyrics" by the admin (MarkNone)
// and no instrumental tag, with its group.
const missingGroup = `CASE WHEN EXISTS (SELECT 1 FROM favorites f WHERE f.track_id=t.id) THEN 0
	WHEN l.is_download_target=1 OR EXISTS (SELECT 1 FROM downloads d WHERE d.track_id=t.id AND d.status='done') THEN 1
	ELSE 2 END`

const missingBase = `WITH m AS (SELECT t.id AS id, ` + missingGroup + ` AS grp
	FROM tracks t JOIN libraries l ON l.id=t.library_id
	WHERE t.status!='trashed' AND t.missing_since IS NULL
	AND NOT EXISTS (SELECT 1 FROM lyrics y WHERE y.track_id=t.id AND y.selected=1)
	AND NOT EXISTS (SELECT 1 FROM lyrics_lookup lk WHERE lk.track_id=t.id AND lk.found=0 AND lk.manual=1)
	AND NOT EXISTS (SELECT 1 FROM track_tags tt JOIN tags g ON g.id=tt.tag_id
		WHERE tt.track_id=t.id AND tt.removed=0 AND g.name='instrumental'))`

// MissingList pages through tracks with no selected lyrics: favorites, then
// downloads, then the rest, each by id; group MissingAll serves all three,
// any other narrows to that group. after is the last id of the previous page
// (0 to start); with MissingAll the page continues from that track's current
// group.
func (s *Service) MissingList(ctx context.Context, group int, after int64, limit int) ([]Missing, error) {
	return s.MissingListFiltered(ctx, group, false, after, limit)
}

// MissingListFiltered is MissingList, with reported only the tracks whose
// lyrics were reported wrong (ReportedWrong), in the same order.
func (s *Service) MissingListFiltered(ctx context.Context, group int, reported bool, after int64, limit int) ([]Missing, error) {
	afterGrp := MissingFavorites
	if group != MissingAll {
		afterGrp = group
	} else if after > 0 {
		err := s.DB.QueryRowContext(ctx, `SELECT `+missingGroup+` FROM tracks t JOIN libraries l ON l.id=t.library_id WHERE t.id=?`, after).Scan(&afterGrp)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	rows, err := s.DB.QueryContext(ctx, missingBase+`
		SELECT m.id, lk.attempted_at, lk.found, lk.wrong_at IS NOT NULL, o.title, o.artist,
			COALESCE((SELECT d.title FROM downloads d WHERE d.track_id=m.id AND d.status='done' ORDER BY d.id DESC LIMIT 1), ''),
			COALESCE((SELECT d.channel FROM downloads d WHERE d.track_id=m.id AND d.status='done' ORDER BY d.id DESC LIMIT 1), '')
		FROM m LEFT JOIN lyrics_lookup lk ON lk.track_id=m.id LEFT JOIN track_overrides o ON o.track_id=m.id
		WHERE (?1 = -1 OR m.grp = ?1) AND (m.grp > ?2 OR (m.grp = ?2 AND m.id > ?3)) AND (?5 = 0 OR lk.wrong_at IS NOT NULL)
		ORDER BY m.grp, m.id LIMIT ?4`, group, afterGrp, after, limit, reported)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Missing{}
	type names struct{ title, artist sql.NullString }
	var overrides []names
	for rows.Next() {
		var m Missing
		var at sql.NullInt64
		var found sql.NullBool
		var o names
		var wrong sql.NullBool
		if err := rows.Scan(&m.ID, &at, &found, &wrong, &o.title, &o.artist, &m.YouTubeTitle, &m.YouTubeChannel); err != nil {
			return nil, err
		}
		overrides = append(overrides, o)
		m.ReportedWrong = wrong.Bool
		if at.Valid {
			m.LastLookupAt = &at.Int64
			f := found.Valid && found.Bool
			m.Found = &f
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for i := range out {
		o := overrides[i]
		if o.title.String == "" && o.artist.String == "" {
			continue
		}
		jobs, err := s.doneJobs(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].TitleEdited, out[i].ArtistEdited = edited(o.title, o.artist, jobs)
	}
	return out, nil
}

// doneJobs lists the (video title, channel) of the track's done downloads.
func (s *Service) doneJobs(ctx context.Context, trackID int64) ([][2]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT title, channel FROM downloads WHERE track_id=? AND status='done' ORDER BY id`, trackID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][2]string
	for rows.Next() {
		var j [2]string
		if err := rows.Scan(&j[0], &j[1]); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// MissingCount counts what MissingList would serve for group.
func (s *Service) MissingCount(ctx context.Context, group int) (int, error) {
	return s.MissingCountFiltered(ctx, group, false)
}

// MissingCountFiltered counts what MissingListFiltered would serve.
func (s *Service) MissingCountFiltered(ctx context.Context, group int, reported bool) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, missingBase+` SELECT COUNT(*) FROM m WHERE (?1 = -1 OR m.grp = ?1)
		AND (?2 = 0 OR EXISTS (SELECT 1 FROM lyrics_lookup lk WHERE lk.track_id=m.id AND lk.wrong_at IS NOT NULL))`, group, reported).Scan(&n)
	return n, err
}
