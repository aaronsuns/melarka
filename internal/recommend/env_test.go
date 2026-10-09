package recommend

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/auth"
	"github.com/aaronsuns/lark-server/internal/lastfm"
	"github.com/aaronsuns/lark-server/internal/library"
	"github.com/aaronsuns/lark-server/internal/testutil"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// fakeYT answers searches and mixes from maps; anything unknown is empty.
// err, when set, fails every call.
type fakeYT struct {
	mu       sync.Mutex
	search   map[string][]ytdlp.Video
	mix      map[string][]ytdlp.Video
	err      error
	searches []string
	mixes    []string
	at       []time.Time
}

func (f *fakeYT) SearchTop(ctx context.Context, q string, n int) ([]ytdlp.Video, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.searches = append(f.searches, q)
	f.at = append(f.at, time.Now())
	if f.err != nil {
		return nil, f.err
	}
	return f.search[q], nil
}

func (f *fakeYT) Mix(ctx context.Context, id string, max int) ([]ytdlp.Video, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mixes = append(f.mixes, id)
	f.at = append(f.at, time.Now())
	if f.err != nil {
		return nil, f.err
	}
	vs := f.mix[id]
	if len(vs) > max {
		vs = vs[:max]
	}
	return vs, nil
}

func (f *fakeYT) calls() (searches, mixes []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.searches...), append([]string(nil), f.mixes...)
}

func (f *fakeYT) times() []time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Time(nil), f.at...)
}

type fakeSimilar struct {
	mu      sync.Mutex
	tracks  map[string][]lastfm.SimilarTrack // key "artist|title"
	artists map[string][]lastfm.SimilarArtist
	err     error
	asked   []string
}

func (f *fakeSimilar) TrackSimilar(ctx context.Context, artist, track string, limit int) ([]lastfm.SimilarTrack, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, "track:"+artist+"|"+track)
	if f.err != nil {
		return nil, f.err
	}
	ts, ok := f.tracks[artist+"|"+track]
	if !ok {
		return nil, lastfm.ErrNotFound
	}
	if len(ts) > limit {
		ts = ts[:limit]
	}
	return ts, nil
}

func (f *fakeSimilar) ArtistSimilar(ctx context.Context, artist string, limit int) ([]lastfm.SimilarArtist, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, "artist:"+artist)
	if f.err != nil {
		return nil, f.err
	}
	as := f.artists[artist]
	if len(as) > limit {
		as = as[:limit]
	}
	return as, nil
}

// countingGate records every call that went through it.
type countingGate struct {
	mu sync.Mutex
	n  int
}

func (g *countingGate) Low(ctx context.Context, fn func(context.Context) error) error {
	g.mu.Lock()
	g.n++
	g.mu.Unlock()
	return fn(ctx)
}

type env struct {
	t     *testing.T
	db    *sql.DB
	svc   *Service
	yt    *fakeYT
	gate  *countingGate
	libID int64
	clock *clock
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

var t0 = time.Date(2026, 10, 4, 12, 0, 0, 0, time.Local)

func newEnv(t *testing.T) *env {
	t.Helper()
	d := testutil.DB(t)
	res, err := d.Exec(`INSERT INTO libraries(name,root) VALUES ('main','/m')`)
	if err != nil {
		t.Fatal(err)
	}
	lib, _ := res.LastInsertId()
	e := &env{t: t, db: d, yt: &fakeYT{search: map[string][]ytdlp.Video{}, mix: map[string][]ytdlp.Video{}}, gate: &countingGate{}, libID: lib, clock: &clock{t: t0}}
	e.svc = &Service{DB: d, Library: &library.Store{DB: d}, YT: e.yt, Gate: e.gate,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: e.clock.Now}
	return e
}

func (e *env) user(name string) int64 {
	e.t.Helper()
	u, err := (&auth.Store{DB: e.db}).CreateUser(context.Background(), name, "pw", auth.RoleMember)
	if err != nil {
		e.t.Fatal(err)
	}
	return u.ID
}

// track adds a visible track; durS 0 means unknown.
func (e *env) track(title, artist string, durS int) int64 {
	e.t.Helper()
	res, err := e.db.Exec(`INSERT INTO tracks(library_id,rel_path,size,mtime,fingerprint,duration_ms,codec,bitrate,
		tag_title,tag_artist,status,added_at) VALUES (?,?,1,1,?,?,'mp3',192,?,?,'kept',1)`,
		e.libID, title+"-"+artist, title+"-"+artist, durS*1000, title, artist)
	if err != nil {
		e.t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func (e *env) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.db.Exec(q, args...); err != nil {
		e.t.Fatal(err)
	}
}

var evN int

// play records n plays of track by user at the given time, each played secs long.
func (e *env) play(user, track int64, n int, at time.Time, secs int) {
	e.t.Helper()
	for range n {
		evN++
		e.exec(`INSERT INTO play_events(user_id,track_id,client_event_id,started_at,played_seconds) VALUES (?,?,?,?,?)`,
			user, track, fmt.Sprintf("ev%d", evN), at.Unix(), secs)
	}
}

func (e *env) fav(user, track int64, at time.Time) {
	e.t.Helper()
	e.exec(`INSERT INTO favorites(user_id,track_id,created_at) VALUES (?,?,?)`, user, track, at.Unix())
}

// download records a download job of videoID (status) by user, linked to track when non-zero.
func (e *env) download(user int64, videoID, status string, track int64) {
	e.t.Helper()
	var tr any
	if track != 0 {
		tr = track
	}
	e.exec(`INSERT INTO downloads(user_id,url,video_id,status,track_id,created_at,updated_at) VALUES (?,?,?,?,?,1,1)`,
		user, ytdlp.WatchURL(videoID), videoID, status, tr)
}

func vid(id, title, channel string, dur int) ytdlp.Video {
	return ytdlp.Video{ID: id, Title: title, Channel: channel, DurationS: dur, URL: ytdlp.WatchURL(id), Thumbnail: ytdlp.ThumbnailURL(id)}
}

func ids(items []Item) string {
	var s []string
	for _, it := range items {
		s = append(s, it.VideoID)
	}
	return strings.Join(s, ",")
}

var errBoom = errors.New("boom")
