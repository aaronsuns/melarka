package library

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
)

type Track struct {
	ID           int64  `json:"id"`
	Title        string `json:"title"`
	Artist       string `json:"artist"`
	ArtistID     *int64 `json:"artist_id"`
	Album        string `json:"album"`
	AlbumID      *int64 `json:"album_id"`
	Year         *int64 `json:"year"`
	DurationMS   int64  `json:"duration_ms"`
	Codec        string `json:"codec"`
	Lossless     bool   `json:"lossless"`
	Bitrate      int    `json:"bitrate"`
	Status       string `json:"status"`
	Broken       bool   `json:"broken"`
	BrokenReason string `json:"broken_reason"`
	Favorite     bool   `json:"favorite"`
	Disliked     bool   `json:"disliked"`
	LibraryID    int64  `json:"library_id"`
	Path         string `json:"path"`
	AddedAt      int64  `json:"added_at"`
	TrackNo      *int64 `json:"track_no"`
	DiscNo       *int64 `json:"disc_no"`
}

type Album struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Artist     string `json:"artist"`
	ArtistID   *int64 `json:"artist_id"`
	Year       *int64 `json:"year"`
	TrackCount int    `json:"track_count"`
	LibraryID  int64  `json:"library_id"`
}

type Artist struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	TrackCount int    `json:"track_count"`
}

type TagCount struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor"`
}

type TrackFilter struct {
	LibraryID, ArtistID, AlbumID int64
	Tag, Status                  string
	FavoritesOnly, DislikedOnly  bool
	Broken                       bool   // only broken tracks (admin console "损坏文件")
	Sort                         string // "added" (default) | "title" | "album" | "favorited" (newest favorite first)
	Limit                        int
	Cursor                       string
}

type SearchResult struct {
	Tracks  []Track  `json:"tracks"`
	Albums  []Album  `json:"albums"`
	Artists []Artist `json:"artists"`
}

const visible = `t.status!='trashed' AND t.missing_since IS NULL`

// trackSelect needs the user id bound twice (favorite, disliked flags).
const trackSelect = `SELECT t.id, COALESCE(NULLIF(o.title,''), t.tag_title), t.rel_path,
	COALESCE(NULLIF(o.artist,''), t.tag_artist), t.artist_id, COALESCE(al.name,''), t.album_id,
	NULLIF(COALESCE(o.year, t.tag_year), 0), t.duration_ms, t.codec, t.lossless, t.bitrate, t.status, t.broken, t.broken_reason,
	t.library_id, t.added_at, t.track_no, t.disc_no,
	EXISTS(SELECT 1 FROM favorites f WHERE f.user_id=? AND f.track_id=t.id),
	EXISTS(SELECT 1 FROM dislikes d WHERE d.user_id=? AND d.track_id=t.id)
	FROM tracks t LEFT JOIN track_overrides o ON o.track_id=t.id LEFT JOIN albums al ON al.id=t.album_id`

func scanTracks(rows *sql.Rows) ([]Track, error) {
	defer rows.Close()
	out := []Track{}
	for rows.Next() {
		var tr Track
		if err := rows.Scan(&tr.ID, &tr.Title, &tr.Path, &tr.Artist, &tr.ArtistID, &tr.Album, &tr.AlbumID, &tr.Year,
			&tr.DurationMS, &tr.Codec, &tr.Lossless, &tr.Bitrate, &tr.Status, &tr.Broken, &tr.BrokenReason, &tr.LibraryID, &tr.AddedAt,
			&tr.TrackNo, &tr.DiscNo, &tr.Favorite, &tr.Disliked); err != nil {
			return nil, err
		}
		if tr.Title == "" {
			tr.Title = titleFromPath(tr.Path, tr.Artist) // before the artist fallback below
		}
		if tr.Artist == "" {
			tr.Artist = artistFromPath(tr.Path)
		}
		out = append(out, tr)
	}
	return out, rows.Err()
}

func limitOf(n int) int {
	switch {
	case n <= 0:
		return 100
	case n > 500:
		return 500
	}
	return n
}

func offsetOf(cursor string) int {
	n, _ := strconv.Atoi(cursor)
	if n < 0 {
		return 0
	}
	return n
}

func next(offset, limit, got int) string {
	if got < limit {
		return ""
	}
	return strconv.Itoa(offset + limit)
}

func (s *Store) Tracks(ctx context.Context, userID int64, f TrackFilter) (Page[Track], error) {
	where := []string{visible}
	args := []any{userID, userID}
	add := func(cond string, a ...any) { where = append(where, cond); args = append(args, a...) }
	if f.LibraryID > 0 {
		add("t.library_id=?", f.LibraryID)
	}
	if f.ArtistID > 0 {
		add("t.artist_id=?", f.ArtistID)
	}
	if f.AlbumID > 0 {
		add("t.album_id=?", f.AlbumID)
	}
	if f.Status != "" {
		add("t.status=?", f.Status)
	}
	if f.Broken {
		add("t.broken=1")
	}
	if f.Tag != "" {
		add(`EXISTS(SELECT 1 FROM track_tags tt JOIN tags g ON g.id=tt.tag_id WHERE tt.track_id=t.id AND tt.removed=0 AND g.name=?)`, f.Tag)
	}
	if f.FavoritesOnly {
		add("EXISTS(SELECT 1 FROM favorites f WHERE f.user_id=? AND f.track_id=t.id)", userID)
	}
	if f.DislikedOnly {
		add("EXISTS(SELECT 1 FROM dislikes d WHERE d.user_id=? AND d.track_id=t.id)", userID)
	} else {
		add("NOT EXISTS(SELECT 1 FROM dislikes d WHERE d.user_id=? AND d.track_id=t.id)", userID)
	}
	order := "t.added_at DESC, t.id DESC"
	switch f.Sort {
	case "title":
		order = "COALESCE(NULLIF(o.title,''), NULLIF(t.tag_title,''), t.rel_path) COLLATE NOCASE, t.id"
	case "album":
		order = "COALESCE(t.disc_no,1), COALESCE(t.track_no,0), t.rel_path"
	}
	if f.Sort == "favorited" {
		order = "(SELECT f.created_at FROM favorites f WHERE f.user_id=? AND f.track_id=t.id) DESC, t.id DESC"
		args = append(args, userID)
	}
	limit, offset := limitOf(f.Limit), offsetOf(f.Cursor)
	args = append(args, limit, offset)
	rows, err := s.DB.QueryContext(ctx, trackSelect+" WHERE "+strings.Join(where, " AND ")+" ORDER BY "+order+" LIMIT ? OFFSET ?", args...)
	if err != nil {
		return Page[Track]{}, err
	}
	items, err := scanTracks(rows)
	if err != nil {
		return Page[Track]{}, err
	}
	return Page[Track]{Items: items, NextCursor: next(offset, limit, len(items))}, nil
}

func (s *Store) Track(ctx context.Context, userID, id int64) (Track, error) {
	rows, err := s.DB.QueryContext(ctx, trackSelect+" WHERE "+visible+" AND t.id=?", userID, userID, id)
	if err != nil {
		return Track{}, err
	}
	ts, err := scanTracks(rows)
	if err != nil {
		return Track{}, err
	}
	if len(ts) == 0 {
		return Track{}, ErrNotFound
	}
	return ts[0], nil
}

func (s *Store) TracksByIDs(ctx context.Context, userID int64, ids []int64) ([]Track, error) {
	if len(ids) == 0 {
		return []Track{}, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := []any{userID, userID}
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.DB.QueryContext(ctx, trackSelect+" WHERE "+visible+" AND t.id IN ("+ph+")", args...)
	if err != nil {
		return nil, err
	}
	ts, err := scanTracks(rows)
	if err != nil {
		return nil, err
	}
	byID := map[int64]Track{}
	for _, t := range ts {
		byID[t.ID] = t
	}
	out := make([]Track, 0, len(ids))
	for _, id := range ids {
		if t, ok := byID[id]; ok {
			out = append(out, t)
		}
	}
	return out, nil
}

// RandomOpts selects what Random draws from.
type RandomOpts struct {
	N             int // 1..200, default 50
	Exclude       []int64
	FavoritesOnly bool
	Tag           string // only tracks carrying this (non-removed) tag
}

// Random returns up to o.N random visible, non-broken tracks the user hasn't
// disliked, skipping o.Exclude, optionally limited to the user's favorites
// and/or one tag.
func (s *Store) Random(ctx context.Context, userID int64, o RandomOpts) ([]Track, error) {
	n := o.N
	switch {
	case n <= 0:
		n = 50
	case n > 200:
		n = 200
	}
	where := []string{visible, "t.broken=0",
		"NOT EXISTS(SELECT 1 FROM dislikes d WHERE d.user_id=? AND d.track_id=t.id)"}
	args := []any{userID}
	if o.FavoritesOnly {
		where = append(where, "EXISTS(SELECT 1 FROM favorites f WHERE f.user_id=? AND f.track_id=t.id)")
		args = append(args, userID)
	}
	if o.Tag != "" {
		where = append(where, "EXISTS(SELECT 1 FROM track_tags tt JOIN tags g ON g.id=tt.tag_id WHERE tt.track_id=t.id AND tt.removed=0 AND g.name=?)")
		args = append(args, o.Tag)
	}
	if len(o.Exclude) > 0 {
		ph := strings.TrimSuffix(strings.Repeat("?,", len(o.Exclude)), ",")
		where = append(where, "t.id NOT IN ("+ph+")")
		for _, id := range o.Exclude {
			args = append(args, id)
		}
	}
	args = append(args, n)
	rows, err := s.DB.QueryContext(ctx, `SELECT t.id FROM tracks t WHERE `+strings.Join(where, " AND ")+
		` ORDER BY RANDOM() LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return nil, err
	}
	return s.TracksByIDs(ctx, userID, ids)
}

// RandomTracks returns up to n (1..200, default 50) random visible, non-broken
// tracks the user hasn't disliked, skipping exclude.
func (s *Store) RandomTracks(ctx context.Context, userID int64, n int, exclude []int64) ([]Track, error) {
	return s.Random(ctx, userID, RandomOpts{N: n, Exclude: exclude})
}

// RandomFavorites returns up to n of the user's favorites minus exclude. When
// exclude has used them all up it is a fresh shuffle of all favorites
// (exclude ignored); when the user has no playable favorites at all it is a
// global shuffle. source is "favorites" or "all" accordingly.
func (s *Store) RandomFavorites(ctx context.Context, userID int64, n int, exclude []int64) (ts []Track, source string, err error) {
	ts, err = s.Random(ctx, userID, RandomOpts{N: n, Exclude: exclude, FavoritesOnly: true})
	if err != nil || len(ts) > 0 {
		return ts, "favorites", err
	}
	if len(exclude) > 0 {
		ts, err = s.Random(ctx, userID, RandomOpts{N: n, FavoritesOnly: true})
		if err != nil || len(ts) > 0 {
			return ts, "favorites", err
		}
	}
	ts, err = s.Random(ctx, userID, RandomOpts{N: n, Exclude: exclude})
	return ts, "all", err
}

const albumSelect = `SELECT al.id, al.name, COALESCE(ar.name,''), al.artist_id, al.year, al.library_id,
	(SELECT COUNT(*) FROM tracks t WHERE t.album_id=al.id AND ` + visible + `) AS n
	FROM albums al LEFT JOIN artists ar ON ar.id=al.artist_id`

func scanAlbums(rows *sql.Rows) ([]Album, error) {
	defer rows.Close()
	out := []Album{}
	for rows.Next() {
		var a Album
		if err := rows.Scan(&a.ID, &a.Name, &a.Artist, &a.ArtistID, &a.Year, &a.LibraryID, &a.TrackCount); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) Albums(ctx context.Context, artistID int64, limit int, cursor string) (Page[Album], error) {
	limit, offset := limitOf(limit), offsetOf(cursor)
	// n is a select alias, so filter on it from an outer query.
	q := `SELECT * FROM (` + albumSelect + ` WHERE (? = 0 OR al.artist_id = ?)) WHERE n > 0 ORDER BY 2 COLLATE NOCASE LIMIT ? OFFSET ?`
	rows, err := s.DB.QueryContext(ctx, q, artistID, artistID, limit, offset)
	if err != nil {
		return Page[Album]{}, err
	}
	items, err := scanAlbums(rows)
	if err != nil {
		return Page[Album]{}, err
	}
	return Page[Album]{Items: items, NextCursor: next(offset, limit, len(items))}, nil
}

func (s *Store) Album(ctx context.Context, userID, id int64) (Album, []Track, error) {
	rows, err := s.DB.QueryContext(ctx, albumSelect+" WHERE al.id=?", id)
	if err != nil {
		return Album{}, nil, err
	}
	as, err := scanAlbums(rows)
	if err != nil {
		return Album{}, nil, err
	}
	if len(as) == 0 {
		return Album{}, nil, ErrNotFound
	}
	p, err := s.Tracks(ctx, userID, TrackFilter{AlbumID: id, Sort: "album", Limit: 500})
	return as[0], p.Items, err
}

func (s *Store) Artists(ctx context.Context, limit int, cursor string) (Page[Artist], error) {
	limit, offset := limitOf(limit), offsetOf(cursor)
	rows, err := s.DB.QueryContext(ctx, `SELECT ar.id, ar.name, COUNT(t.id) n FROM artists ar
		JOIN tracks t ON t.artist_id=ar.id AND `+visible+`
		GROUP BY ar.id ORDER BY ar.name COLLATE NOCASE LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return Page[Artist]{}, err
	}
	defer rows.Close()
	items := []Artist{}
	for rows.Next() {
		var a Artist
		if err := rows.Scan(&a.ID, &a.Name, &a.TrackCount); err != nil {
			return Page[Artist]{}, err
		}
		items = append(items, a)
	}
	return Page[Artist]{Items: items, NextCursor: next(offset, limit, len(items))}, rows.Err()
}

func (s *Store) Artist(ctx context.Context, userID, id int64) (Artist, []Album, error) {
	var a Artist
	err := s.DB.QueryRowContext(ctx, `SELECT ar.id, ar.name, (SELECT COUNT(*) FROM tracks t WHERE t.artist_id=ar.id AND `+visible+`)
		FROM artists ar WHERE ar.id=?`, id).Scan(&a.ID, &a.Name, &a.TrackCount)
	if errors.Is(err, sql.ErrNoRows) {
		return Artist{}, nil, ErrNotFound
	}
	if err != nil {
		return Artist{}, nil, err
	}
	p, err := s.Albums(ctx, id, 500, "")
	return a, p.Items, err
}

func (s *Store) Tags(ctx context.Context) ([]TagCount, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT g.name, g.kind, COUNT(DISTINCT tt.track_id) FROM tags g
		JOIN track_tags tt ON tt.tag_id=g.id AND tt.removed=0
		JOIN tracks t ON t.id=tt.track_id AND `+visible+`
		GROUP BY g.id ORDER BY 3 DESC, g.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TagCount{}
	for rows.Next() {
		var tc TagCount
		if err := rows.Scan(&tc.Name, &tc.Kind, &tc.Count); err != nil {
			return nil, err
		}
		out = append(out, tc)
	}
	return out, rows.Err()
}

// scanIDs reads a single int64 column (e.g. an FTS rowid list) from rows.
func scanIDs(rows *sql.Rows) ([]int64, error) {
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// albumsByIDs returns the albums for ids, in the same order as ids
// (first-seen order for callers building ids from a dedup pass). Unknown ids
// are dropped, matching TracksByIDs.
func (s *Store) albumsByIDs(ctx context.Context, ids []int64) ([]Album, error) {
	if len(ids) == 0 {
		return []Album{}, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.DB.QueryContext(ctx, albumSelect+" WHERE al.id IN ("+ph+")", args...)
	if err != nil {
		return nil, err
	}
	as, err := scanAlbums(rows)
	if err != nil {
		return nil, err
	}
	byID := map[int64]Album{}
	for _, a := range as {
		byID[a.ID] = a
	}
	out := make([]Album, 0, len(ids))
	for _, id := range ids {
		if a, ok := byID[id]; ok {
			out = append(out, a)
		}
	}
	return out, nil
}

// Search finds tracks through the FTS index, then returns the distinct albums
// and artists of those tracks.
func (s *Store) Search(ctx context.Context, userID int64, q string, limit int) (SearchResult, error) {
	res := SearchResult{Tracks: []Track{}, Albums: []Album{}, Artists: []Artist{}}
	expr := FTSQuery(q)
	if expr == "" {
		return res, nil
	}
	limit = limitOf(limit)
	rows, err := s.DB.QueryContext(ctx, `SELECT rowid FROM track_fts WHERE track_fts MATCH ? ORDER BY rank LIMIT ?`, expr, limit*3)
	if err != nil {
		return res, err
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return res, err
	}
	tracks, err := s.TracksByIDs(ctx, userID, ids)
	if err != nil {
		return res, err
	}
	if len(tracks) > limit {
		tracks = tracks[:limit]
	}
	res.Tracks = tracks
	seenAlbum, seenArtist := map[int64]bool{}, map[int64]bool{}
	var albumIDs []int64
	for _, t := range tracks {
		if t.AlbumID != nil && !seenAlbum[*t.AlbumID] {
			seenAlbum[*t.AlbumID] = true
			albumIDs = append(albumIDs, *t.AlbumID)
		}
		if t.ArtistID != nil && !seenArtist[*t.ArtistID] {
			seenArtist[*t.ArtistID] = true
			res.Artists = append(res.Artists, Artist{ID: *t.ArtistID, Name: t.Artist})
		}
	}
	albums, err := s.albumsByIDs(ctx, albumIDs)
	if err != nil {
		return res, err
	}
	res.Albums = albums
	return res, nil
}

type StreamInfo struct {
	AbsPath, Codec, Fingerprint string
	BitrateKbps                 int
}

func (s *Store) StreamInfo(ctx context.Context, id int64) (StreamInfo, error) {
	var si StreamInfo
	var rel, root string
	err := s.DB.QueryRowContext(ctx, `SELECT t.rel_path, l.root, t.codec, t.bitrate, t.fingerprint FROM tracks t
		JOIN libraries l ON l.id=t.library_id WHERE t.id=? AND `+visible+` AND t.broken=0`, id).
		Scan(&rel, &root, &si.Codec, &si.BitrateKbps, &si.Fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return si, ErrNotFound
	}
	si.AbsPath = filepath.Join(root, filepath.FromSlash(rel))
	return si, err
}
