package channels

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/auth"
	"github.com/aaronsuns/lark-server/internal/testutil"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

const (
	chA = "UCaaaaaaaaaaaaaaaaaaaaaa"
	chB = "UCbbbbbbbbbbbbbbbbbbbbbb"
)

func vid(n int) string { return fmt.Sprintf("vid%08d", n) } // 11 characters, like a real id

type fakeFeeds struct {
	mu    sync.Mutex
	feeds map[string]Feed
	errs  map[string]error
	calls []string
}

func (f *fakeFeeds) Fetch(ctx context.Context, id string) (Feed, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, id)
	if err := f.errs[id]; err != nil {
		return Feed{}, err
	}
	return f.feeds[id], nil
}

type fakeYT struct {
	mu        sync.Mutex
	infos     map[string]ytdlp.Info
	infoErr   map[string]error
	infoCalls []string
	dlErr     map[string]error // by video id
	dlHang    map[string]bool  // by video id: runs until its context ends, like a long download
	dlCalls   []string         // "<id>/<kind>"
	size      int              // bytes per downloaded file; 0: 1000
	mix       map[string][]ytdlp.Video
	mixErr    error
	mixCalls  []string
}

func (f *fakeYT) VideoInfo(ctx context.Context, id string) (ytdlp.Info, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.infoCalls = append(f.infoCalls, id)
	if err := f.infoErr[id]; err != nil {
		return ytdlp.Info{}, err
	}
	if in, ok := f.infos[id]; ok {
		return in, nil
	}
	return ytdlp.Info{ID: id, DurationS: 1200, LiveStatus: "not_live", Availability: "public", MediaType: "video"}, nil
}

func (f *fakeYT) DownloadEpisode(ctx context.Context, v ytdlp.Video, destNoExt string, kind ytdlp.MediaKind, onProgress func(float64)) (string, error) {
	f.mu.Lock()
	f.dlCalls = append(f.dlCalls, v.ID+"/"+string(kind))
	err, size, hang := f.dlErr[v.ID], f.size, f.dlHang[v.ID]
	f.mu.Unlock()
	if hang {
		<-ctx.Done()
		err = errors.New("yt-dlp: signal: killed") // what a killed yt-dlp reports
	}
	if size == 0 {
		size = 1000
	}
	if err != nil {
		os.WriteFile(destNoExt+".m4a.part", []byte("partial"), 0o644) // what a failed run leaves behind
		return "", err
	}
	if onProgress != nil {
		onProgress(50)
	}
	ext := ".m4a"
	if kind == ytdlp.MediaVideo {
		ext = ".mp4"
	} else {
		os.WriteFile(destNoExt+".jpg", []byte("jpg"), 0o644)
	}
	p := destNoExt + ext
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		return "", err
	}
	return p, nil
}

func (f *fakeYT) Mix(ctx context.Context, id string, max int) ([]ytdlp.Video, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mixCalls = append(f.mixCalls, id)
	if f.mixErr != nil {
		return nil, f.mixErr
	}
	vs := f.mix[id]
	if len(vs) > max {
		vs = vs[:max]
	}
	return vs, nil
}

func (f *fakeYT) downloads() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.dlCalls...)
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

var t0 = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

type env struct {
	t     *testing.T
	db    *sql.DB
	svc   *Service
	feeds *fakeFeeds
	yt    *fakeYT
	clock *clock
	root  string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d := testutil.DB(t)
	e := &env{t: t, db: d, feeds: &fakeFeeds{feeds: map[string]Feed{}, errs: map[string]error{}},
		yt:    &fakeYT{infos: map[string]ytdlp.Info{}, infoErr: map[string]error{}, dlErr: map[string]error{}, dlHang: map[string]bool{}, mix: map[string][]ytdlp.Video{}},
		clock: &clock{t: t0}, root: t.TempDir()}
	e.svc = &Service{DB: d, YT: e.yt, Feeds: e.feeds, Root: e.root, PollInterval: 2 * time.Hour, KeepDays: 10,
		InitialBackfill: 3, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: e.clock.Now,
		Pause: time.Millisecond, MinFree: 1, FreeBytes: func(string) (int64, error) { return 1 << 40, nil }}
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

func (e *env) follow(user int64, id string) MyChannel {
	e.t.Helper()
	mc, err := e.svc.Follow(context.Background(), user, ytdlp.Channel{ID: id, Title: "Channel " + id[2:6]})
	if err != nil {
		e.t.Fatal(err)
	}
	return mc
}

func (e *env) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.db.Exec(q, args...); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRow(q, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// entry is a feed entry published `ago` before the env clock's now.
func (e *env) entry(n int, ago time.Duration) FeedEntry {
	return FeedEntry{VideoID: vid(n), Title: fmt.Sprintf("Episode %d", n), Published: e.clock.Now().Add(-ago), Description: "desc"}
}

func (e *env) fileStatus(videoID, kind string) string {
	e.t.Helper()
	var s string
	err := e.db.QueryRow(`SELECT status FROM episode_files WHERE video_id=? AND kind=?`, videoID, kind).Scan(&s)
	if err == sql.ErrNoRows {
		return ""
	}
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func (e *env) path(channelID, name string) string { return filepath.Join(e.root, channelID, name) }

func ytdlpChannel(id, title string) ytdlp.Channel { return ytdlp.Channel{ID: id, Title: title} }
