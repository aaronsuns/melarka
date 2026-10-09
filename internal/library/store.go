// Package library owns music libraries and their tracks: scanning, indexing,
// search and browse queries.
package library

import (
	"context"
	"database/sql"
	"errors"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/aaronsuns/lark-server/internal/tags"
)

var (
	ErrNotFound           = errors.New("not found")
	ErrLibraryUnavailable = errors.New("library root is missing or empty; refusing to mark tracks missing")
)

type Library struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	Root           string `json:"root"`
	DownloadTarget bool   `json:"download_target"`
	LastScanAt     *int64 `json:"last_scan_at"`
}

type Store struct {
	DB  *sql.DB
	Now func() time.Time
}

func (s *Store) now() int64 {
	if s.Now != nil {
		return s.Now().Unix()
	}
	return time.Now().Unix()
}

func (s *Store) EnsureLibrary(ctx context.Context, name, root string, downloadTarget bool) (Library, error) {
	root = filepath.Clean(root)
	_, err := s.DB.ExecContext(ctx, `INSERT INTO libraries(name,root,is_download_target) VALUES (?,?,?)
		ON CONFLICT(name) DO UPDATE SET root=excluded.root, is_download_target=excluded.is_download_target`,
		name, root, downloadTarget)
	if err != nil {
		return Library{}, err
	}
	var id int64
	if err := s.DB.QueryRowContext(ctx, `SELECT id FROM libraries WHERE name=?`, name).Scan(&id); err != nil {
		return Library{}, err
	}
	return s.Library(ctx, id)
}

// LibraryByName looks up a library by its unique name, ErrNotFound if none
// exists yet.
func (s *Store) LibraryByName(ctx context.Context, name string) (Library, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM libraries WHERE name=?`, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Library{}, ErrNotFound
	}
	if err != nil {
		return Library{}, err
	}
	return s.Library(ctx, id)
}

func (s *Store) Library(ctx context.Context, id int64) (Library, error) {
	var l Library
	err := s.DB.QueryRowContext(ctx, `SELECT id,name,root,is_download_target,last_scan_at FROM libraries WHERE id=?`, id).
		Scan(&l.ID, &l.Name, &l.Root, &l.DownloadTarget, &l.LastScanAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Library{}, ErrNotFound
	}
	return l, err
}

func (s *Store) Libraries(ctx context.Context) ([]Library, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,root,is_download_target,last_scan_at FROM libraries ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Library{}
	for rows.Next() {
		var l Library
		if err := rows.Scan(&l.ID, &l.Name, &l.Root, &l.DownloadTarget, &l.LastScanAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) DeleteLibrary(ctx context.Context, id int64) error {
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM track_fts WHERE rowid IN (SELECT id FROM tracks WHERE library_id=?)`, id); err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, `DELETE FROM libraries WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return s.pruneOrphans(ctx)
}

func (s *Store) TrackPath(ctx context.Context, trackID int64) (string, Library, error) {
	var rel string
	var libID int64
	err := s.DB.QueryRowContext(ctx, `SELECT rel_path, library_id FROM tracks WHERE id=?`, trackID).Scan(&rel, &libID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", Library{}, ErrNotFound
	}
	if err != nil {
		return "", Library{}, err
	}
	lib, err := s.Library(ctx, libID)
	if err != nil {
		return "", Library{}, err
	}
	return filepath.Join(lib.Root, filepath.FromSlash(rel)), lib, nil
}

var leadingTrackNo = regexp.MustCompile(`^\s*\d{1,3}\s*[-._、)]\s*`)

// titleFromPath is the display title when the title tag is empty:
// "邓丽君精选/01 - 甜蜜蜜.mp3" gives "甜蜜蜜", and with no known artist
// "经典怀旧(1)/陈星 - 离家的孩子.mp3" gives "离家的孩子" (see splitFileName).
// artist is the track's tagged (or overridden) artist: when there is one,
// the name is not split — "甜蜜蜜 - 现场版.mp3" by 邓丽君 is titled
// "甜蜜蜜 - 现场版" — unless the part before the dash is that artist.
func titleFromPath(rel, artist string) string {
	a, t := splitFileName(rel)
	if a == "" || artist == "" || a == artist {
		return t
	}
	base := path.Base(rel)
	base = strings.TrimSuffix(base, path.Ext(base))
	if rest := leadingTrackNo.ReplaceAllString(base, ""); rest != "" {
		return rest
	}
	return base
}

// Reindex derives artist, album and the search document for one track from its
// effective values (override ?? tag ?? path).
func (s *Store) Reindex(ctx context.Context, trackID int64) error {
	return s.reindex(ctx, trackID, false)
}

// reindex is Reindex; with relinkAlbum the track's album takes this track's
// album artist even if it already had one (the album upsert otherwise keeps
// an existing link), in the same transaction.
func (s *Store) reindex(ctx context.Context, trackID int64, relinkAlbum bool) error {
	var libID int64
	var rel, tTitle, tArtist, tAlbum, tAlbumArtist, libName string
	var tYear sql.NullInt64
	var oTitle, oArtist, oAlbum sql.NullString
	var oYear sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT t.library_id, t.rel_path, t.tag_title, t.tag_artist, t.tag_album,
		t.tag_album_artist, t.tag_year, o.title, o.artist, o.album, o.year, l.name
		FROM tracks t JOIN libraries l ON l.id=t.library_id LEFT JOIN track_overrides o ON o.track_id=t.id WHERE t.id=?`, trackID).
		Scan(&libID, &rel, &tTitle, &tArtist, &tAlbum, &tAlbumArtist, &tYear, &oTitle, &oArtist, &oAlbum, &oYear, &libName)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	known := pick(oArtist, tArtist, "")
	title := pick(oTitle, tTitle, titleFromPath(rel, known))
	artist := pick(oArtist, tArtist, artistFromPath(rel))
	folder := path.Dir(rel)
	folderName := path.Base(folder)
	if folder == "." {
		folderName = libName
	}
	album := pick(oAlbum, tAlbum, folderName)
	noAlbum := oAlbum.Valid && oAlbum.String == "" // explicit "no album" (Overrides.NoAlbum)
	if noAlbum {
		album = ""
	}
	year := tYear
	if oYear.Valid {
		year = oYear
	}
	if year.Valid && year.Int64 == 0 { // explicit "no year" (Overrides.NoYear)
		year = sql.NullInt64{}
	}

	// Tag words are read before the transaction opens, so the transaction
	// stays short and doesn't hold the write lock across this extra query.
	tagWords, err := s.tagSearchWords(ctx, trackID)
	if err != nil {
		return err
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	artistID, err := upsertArtist(ctx, tx, artist)
	if err != nil {
		return err
	}
	albumArtistID := artistID
	if tAlbumArtist != "" && !oArtist.Valid {
		if albumArtistID, err = upsertArtist(ctx, tx, tAlbumArtist); err != nil {
			return err
		}
	}
	keep := "COALESCE(albums.artist_id, excluded.artist_id)"
	if relinkAlbum {
		keep = "excluded.artist_id"
	}
	var albumID any // nil: no album
	if !noAlbum {
		if _, err := tx.ExecContext(ctx, `INSERT INTO albums(library_id,folder,name,artist_id,year) VALUES (?,?,?,?,?)
		ON CONFLICT(library_id,folder,name) DO UPDATE SET artist_id=`+keep+`,
		year=COALESCE(albums.year, excluded.year)`, libID, folder, album, albumArtistID, year); err != nil {
			return err
		}
		var id int64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM albums WHERE library_id=? AND folder=? AND name=?`, libID, folder, album).Scan(&id); err != nil {
			return err
		}
		albumID = id
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tracks SET artist_id=?, album_id=? WHERE id=?`, artistID, albumID, trackID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM track_fts WHERE rowid=?`, trackID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO track_fts(rowid, doc) VALUES (?,?)`, trackID,
		SearchDoc(append([]string{title, artist, album, folderName}, tagWords...)...)); err != nil {
		return err
	}
	return tx.Commit()
}

// tagSearchWords returns the search words of the track's live tags: a
// vocabulary tag contributes its slug and every locale name.
func (s *Store) tagSearchWords(ctx context.Context, trackID int64) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT g.name FROM track_tags tt JOIN tags g ON g.id=tt.tag_id
		WHERE tt.track_id=? AND tt.removed=0 ORDER BY g.name`, trackID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, tags.SearchWords(n)...)
	}
	return out, rows.Err()
}

func pick(override sql.NullString, tag, fallback string) string {
	if override.Valid && strings.TrimSpace(override.String) != "" {
		return strings.TrimSpace(override.String)
	}
	if tag != "" {
		return tag
	}
	return fallback
}

// upsertArtist returns nil for an empty name (unknown artist).
func upsertArtist(ctx context.Context, tx *sql.Tx, name string) (any, error) {
	if name == "" {
		return nil, nil
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO artists(name) VALUES (?) ON CONFLICT(name) DO NOTHING`, name); err != nil {
		return nil, err
	}
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM artists WHERE name=?`, name).Scan(&id)
	return id, err
}

// PruneOrphans drops albums and artists no track (or album) links any more —
// for callers that relinked tracks with Reindex.
func (s *Store) PruneOrphans(ctx context.Context) error { return s.pruneOrphans(ctx) }

func (s *Store) pruneOrphans(ctx context.Context) error {
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM albums WHERE id NOT IN (SELECT album_id FROM tracks WHERE album_id IS NOT NULL)`); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM artists WHERE id NOT IN (SELECT artist_id FROM tracks WHERE artist_id IS NOT NULL)
		AND id NOT IN (SELECT artist_id FROM albums WHERE artist_id IS NOT NULL)`)
	return err
}
