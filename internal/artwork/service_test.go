package artwork

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/library"
	"github.com/aaronsuns/lark-server/internal/lyrics"
	"github.com/aaronsuns/lark-server/internal/media"
	"github.com/aaronsuns/lark-server/internal/testutil"
)

func ffmpeg(t *testing.T, args ...string) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	if out, err := exec.Command("ffmpeg", append([]string{"-nostdin", "-v", "error", "-y"}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %v: %v %s", args, err, out)
	}
}

// image writes a solid-colour w×h picture (format from the extension).
func image(t *testing.T, path, color string, w, h int) {
	os.MkdirAll(filepath.Dir(path), 0o755)
	ffmpeg(t, "-f", "lavfi", "-i", "color=c="+color+":s="+strconv.Itoa(w)+"x"+strconv.Itoa(h), "-frames:v", "1", path)
}

// audioWithPicture writes an mp3 whose attached picture is w×h.
func audioWithPicture(t *testing.T, path string, w, h int) {
	pic := path + ".src.png"
	image(t, pic, "blue", w, h)
	ffmpeg(t, "-f", "lavfi", "-i", "sine=duration=1", "-i", pic, "-map", "0:a", "-map", "1:v", "-c:v", "mjpeg",
		"-disposition:v:0", "attached_pic", "-id3v2_version", "3", path)
	os.Remove(pic)
}

func dims(t *testing.T, path string) string {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=width,height", "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", path, err)
	}
	return strings.TrimSpace(string(out))
}

type artEnv struct {
	svc     *Service
	db      *sql.DB
	root    string
	ctx     context.Context
	libID   int64
	advance func(time.Duration)
}

func newArtEnv(t *testing.T, provs ...Provider) artEnv {
	t.Helper()
	ctx := context.Background()
	d := testutil.DB(t)
	lib := &library.Store{DB: d}
	root := t.TempDir()
	l, err := lib.EnsureLibrary(ctx, "main", root, false)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	now := time.Unix(1_800_000_000, 0)
	svc := &Service{DB: d, Library: lib, Providers: provs, Imager: FFmpeg{Path: "ffmpeg"}, Dir: filepath.Join(t.TempDir(), "artwork"),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { mu.Lock(); defer mu.Unlock(); return now }}
	return artEnv{svc: svc, db: d, root: root, ctx: ctx, libID: l.ID,
		advance: func(x time.Duration) { mu.Lock(); now = now.Add(x); mu.Unlock() }}
}

// track inserts a visible track at rel (the file itself is created by the caller when needed).
func (e artEnv) track(t *testing.T, rel, title, artist, album string) int64 {
	t.Helper()
	res, err := e.db.Exec(`INSERT INTO tracks(library_id,rel_path,size,mtime,fingerprint,duration_ms,codec,bitrate,
		tag_title,tag_artist,tag_album,status,added_at) VALUES (?,?,1,1,?,211000,'mp3',192,?,?,?,'kept',1)`, e.libID, rel, rel, title, artist, album)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if err := (&library.Store{DB: e.db}).Reindex(e.ctx, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (e artEnv) lookupRow(t *testing.T, id int64) (found int, source string, ok bool) {
	err := e.db.QueryRow(`SELECT found, source FROM artwork_lookup WHERE track_id=?`, id).Scan(&found, &source)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return found, source, true
}

func local() []Provider {
	return []Provider{&Embedded{Probe: media.FFprobe{Path: "ffprobe"}}, Folder{}}
}

func TestEmbeddedPictureWinsAndBothSizesAreSquare(t *testing.T) {
	e := newArtEnv(t, local()...)
	audioWithPicture(t, filepath.Join(e.root, "a", "song.mp3"), 1280, 720) // YouTube-shaped thumbnail
	image(t, filepath.Join(e.root, "a", "cover.png"), "red", 600, 600)
	id := e.track(t, "a/song.mp3", "甜蜜蜜", "邓丽君", "精选")
	p300, err := e.svc.Get(e.ctx, id, 300)
	if err != nil {
		t.Fatal(err)
	}
	p1000, err := e.svc.Get(e.ctx, id, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if dims(t, p300) != "300,300" || dims(t, p1000) != "720,720" { // cropped square, never upscaled
		t.Fatalf("dims %s / %s", dims(t, p300), dims(t, p1000))
	}
	if p300 != e.svc.File(id, 300) {
		t.Fatalf("path %s", p300)
	}
	if found, src, _ := e.lookupRow(t, id); found != 1 || src != "embedded" {
		t.Fatalf("row %d %q", found, src)
	}
	for _, f := range []string{"a/song.mp3", "a/cover.png"} { // the library folder is never written
		if _, err := os.Stat(filepath.Join(e.root, f)); err != nil {
			t.Fatal(err)
		}
	}
	if ents, _ := os.ReadDir(filepath.Join(e.root, "a")); len(ents) != 2 {
		t.Fatalf("library folder changed: %d entries", len(ents))
	}
}

func TestFolderImageNamesAndOrder(t *testing.T) {
	e := newArtEnv(t, local()...)
	testutil.Sample(t, filepath.Join(e.root, "b"), "song.mp3")
	image(t, filepath.Join(e.root, "b", "cover-old.jpg"), "red", 50, 50) // not a cover name
	image(t, filepath.Join(e.root, "b", "Front.PNG"), "red", 400, 300)
	image(t, filepath.Join(e.root, "b", "Folder.JPG"), "green", 500, 500) // folder beats front
	id := e.track(t, "b/song.mp3", "x", "y", "")
	p, err := e.svc.Get(e.ctx, id, 1000)
	if err != nil || dims(t, p) != "500,500" {
		t.Fatalf("%v %s", err, p)
	}
	if _, src, _ := e.lookupRow(t, id); src != "folder" {
		t.Fatalf("source %q", src)
	}
}

func TestLocalMissIsRememberedFor30Days(t *testing.T) {
	cnt := &countProv{local: true}
	e := newArtEnv(t, cnt)
	id := e.track(t, "c/none.mp3", "x", "y", "")
	if _, err := e.svc.Get(e.ctx, id, 300); !errors.Is(err, ErrNoCover) {
		t.Fatalf("want ErrNoCover, got %v", err)
	}
	if found, _, ok := e.lookupRow(t, id); !ok || found != 0 {
		t.Fatal("miss not recorded")
	}
	e.advance(29 * 24 * time.Hour)
	e.svc.Get(e.ctx, id, 300)
	if cnt.n.Load() != 1 {
		t.Fatalf("asked again within 30 days: %d", cnt.n.Load())
	}
	e.advance(2 * 24 * time.Hour)
	e.svc.Get(e.ctx, id, 300)
	if cnt.n.Load() != 2 {
		t.Fatalf("not asked again after 30 days: %d", cnt.n.Load())
	}
}

func TestOnlineOutageIsNotAMiss(t *testing.T) {
	down := &countProv{err: errors.New("HTTP 503")}
	e := newArtEnv(t, Folder{}, down)
	id := e.track(t, "d/x.mp3", "甜蜜蜜", "邓丽君", "")
	if _, err := e.svc.Get(e.ctx, id, 300); !errors.Is(err, ErrNoCover) {
		t.Fatal(err)
	}
	if _, _, ok := e.lookupRow(t, id); ok {
		t.Fatal("an outage was recorded as a miss")
	}
	if n := down.n.Load(); n != 1 { // not a channel download: one query variant
		t.Fatalf("calls %d", n)
	}
	e.svc.Get(e.ctx, id, 300) // inside the 10-minute backoff: not asked
	if n := down.n.Load(); n != 1 {
		t.Fatalf("asked again during backoff: %d", n)
	}
	e.advance(11 * time.Minute)
	e.svc.Get(e.ctx, id, 300)
	if n := down.n.Load(); n != 2 {
		t.Fatalf("not asked again after the backoff: %d", n)
	}
}

func TestOnlineImageIsFetchedMatchedAndValidated(t *testing.T) {
	png := filepath.Join(t.TempDir(), "art.png")
	image(t, png, "purple", 1200, 1200)
	body, _ := os.ReadFile(png)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/good.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write(body)
		case "/html":
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<html>blocked</html>"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	prov := &staticProv{found: []Found{
		{Source: "itunes", Title: "甜蜜蜜", Artist: "Teresa Teng", DurationS: 240, URL: srv.URL + "/good.png", Stream: -1}, // 29 s off: rejected
		{Source: "itunes", Title: "甜蜜蜜", Artist: "Teresa Teng", DurationS: 210, URL: srv.URL + "/html", Stream: -1},     // not an image
		{Source: "itunes", Title: "甜蜜蜜", Artist: "Teresa Teng", DurationS: 210, URL: srv.URL + "/good.png", Stream: -1},
	}}
	e := newArtEnv(t, prov)
	e.svc.HTTP = srv.Client()
	id := e.track(t, "e/x.mp3", "甜蜜蜜", "邓丽君", "")
	p, err := e.svc.Get(e.ctx, id, 1000)
	if err != nil || dims(t, p) != "1000,1000" {
		t.Fatalf("%v", err)
	}
	if _, src, _ := e.lookupRow(t, id); src != "itunes" {
		t.Fatalf("source %q", src)
	}
	if n := countFiles(t, e.svc.Dir); n != 2 { // 300 + 1000, no temp files left
		t.Fatalf("cache holds %d files", n)
	}
}

func TestBadImagesAreNeverCachedNorAMiss(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/huge" {
			w.Header().Set("Content-Type", "image/jpeg")
			w.Write(make([]byte, 9<<20))
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte("definitely not a jpeg"))
	}))
	defer srv.Close()
	prov := &staticProv{found: []Found{
		{Source: "qq", Title: "x", URL: srv.URL + "/garbage", Stream: -1},
		{Source: "qq", Title: "x", URL: srv.URL + "/huge", Stream: -1},
		{Source: "qq", Title: "x", URL: "http://insecure.example/x.jpg", Stream: -1},
	}}
	e := newArtEnv(t, prov)
	e.svc.HTTP = srv.Client()
	id := e.track(t, "f/x.mp3", "x", "", "")
	if _, err := e.svc.Get(e.ctx, id, 300); !errors.Is(err, ErrNoCover) {
		t.Fatal(err)
	}
	if _, _, ok := e.lookupRow(t, id); ok {
		t.Fatal("a failed image download must back off, not be remembered as a miss")
	}
	if n := countFiles(t, e.svc.Dir); n != 0 {
		t.Fatalf("cache holds %d files", n)
	}
}

func TestWipedCacheIsRebuilt(t *testing.T) {
	e := newArtEnv(t, local()...)
	audioWithPicture(t, filepath.Join(e.root, "g", "s.mp3"), 400, 400)
	id := e.track(t, "g/s.mp3", "x", "y", "")
	if _, err := e.svc.Get(e.ctx, id, 300); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(e.svc.Dir) // found=1 stays in artwork_lookup
	if p, err := e.svc.Get(e.ctx, id, 300); err != nil || dims(t, p) != "300,300" {
		t.Fatalf("not rebuilt: %v", err)
	}
}

func TestAtMostTwoLookupsAtOnceAndOnePerTrack(t *testing.T) {
	slow := &countProv{local: true, delay: 100 * time.Millisecond}
	e := newArtEnv(t, slow)
	var ids []int64
	for i := range 5 {
		ids = append(ids, e.track(t, "h/"+strconv.Itoa(i)+".mp3", "x", "y", ""))
	}
	var wg sync.WaitGroup
	for _, id := range append(ids, ids[0], ids[0]) { // ids[0] three times: one lookup
		wg.Add(1)
		go func() { defer wg.Done(); e.svc.Get(e.ctx, id, 300) }()
	}
	wg.Wait()
	if slow.max.Load() > 2 {
		t.Fatalf("%d lookups at once", slow.max.Load())
	}
	if slow.n.Load() != 5 {
		t.Fatalf("%d lookups for 5 tracks", slow.n.Load())
	}
}

func TestAlbumCoverIsTheFirstTrackThatHasOne(t *testing.T) {
	e := newArtEnv(t, local()...)
	testutil.Sample(t, filepath.Join(e.root, "al"), "1.mp3")
	audioWithPicture(t, filepath.Join(e.root, "al", "2.mp3"), 300, 300)
	t1 := e.track(t, "al/1.mp3", "One", "A", "专辑")
	t2 := e.track(t, "al/2.mp3", "Two", "A", "专辑")
	e.db.Exec(`UPDATE tracks SET track_no=1 WHERE id=?`, t1)
	e.db.Exec(`UPDATE tracks SET track_no=2 WHERE id=?`, t2)
	var album int64
	e.db.QueryRow(`SELECT album_id FROM tracks WHERE id=?`, t1).Scan(&album)
	if _, err := e.svc.Get(e.ctx, t1, 300); !errors.Is(err, ErrNoCover) { // first track has none
		t.Fatal(err)
	}
	if _, err := e.svc.Get(e.ctx, t2, 300); err != nil {
		t.Fatal(err)
	}
	if p, err := e.svc.AlbumCover(e.ctx, album, 300); err != nil || p != e.svc.File(t2, 300) {
		t.Fatalf("%s %v", p, err)
	}
	if _, err := e.svc.AlbumCover(e.ctx, 9999, 300); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

// countProv answers nothing (or err) after delay, counting calls and peak concurrency.
type countProv struct {
	local    bool
	err      error
	delay    time.Duration
	n        atomic.Int32
	inflight atomic.Int32
	max      atomic.Int32
}

func (p *countProv) Name() string { return "fake" }
func (p *countProv) Local() bool  { return p.local }
func (p *countProv) Find(ctx context.Context, q lyrics.Query) ([]Found, error) {
	p.n.Add(1)
	cur := p.inflight.Add(1)
	defer p.inflight.Add(-1)
	for {
		m := p.max.Load()
		if cur <= m || p.max.CompareAndSwap(m, cur) {
			break
		}
	}
	select {
	case <-time.After(p.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return nil, p.err
}

type staticProv struct{ found []Found }

func (p *staticProv) Name() string                                        { return "itunes" }
func (p *staticProv) Find(context.Context, lyrics.Query) ([]Found, error) { return p.found, nil }

func countFiles(t *testing.T, dir string) int {
	ents, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(ents)
}

// failImager always fails, counting calls.
type failImager struct{ n atomic.Int32 }

func (f *failImager) Square(context.Context, string, int, string, int) error {
	f.n.Add(1)
	return errors.New("ffmpeg: exec: no such file")
}

// hangImager ignores ctx until released.
type hangImager struct{ release chan struct{} }

func (h hangImager) Square(context.Context, string, int, string, int) error {
	<-h.release
	return errors.New("released")
}

// hangLocal is a local provider that ignores ctx on paths containing "hang".
type hangLocal struct {
	release chan struct{}
	n       atomic.Int32
}

func (*hangLocal) Name() string { return "folder" }
func (*hangLocal) Local() bool  { return true }
func (h *hangLocal) Find(_ context.Context, q lyrics.Query) ([]Found, error) {
	h.n.Add(1)
	if strings.Contains(q.Path, "hang") {
		<-h.release
	}
	return nil, nil
}

func TestLocalWriteFailureIsNotAMiss(t *testing.T) {
	e := newArtEnv(t, Folder{})
	img := &failImager{}
	e.svc.Imager = img
	image(t, filepath.Join(e.root, "w", "cover.png"), "red", 100, 100)
	id := e.track(t, "w/s.mp3", "x", "y", "")
	if _, err := e.svc.Get(e.ctx, id, 300); !errors.Is(err, ErrNoCover) {
		t.Fatal(err)
	}
	if _, _, ok := e.lookupRow(t, id); ok {
		t.Fatal("a failed conversion was recorded as a 30-day miss")
	}
	n := img.n.Load()
	e.svc.Get(e.ctx, id, 300)
	if img.n.Load() != n {
		t.Fatal("not backed off")
	}
	e.advance(11 * time.Minute)
	e.svc.Get(e.ctx, id, 300)
	if img.n.Load() == n {
		t.Fatal("not retried after the backoff")
	}
}

func TestFailedRebuildKeepsFoundRow(t *testing.T) {
	e := newArtEnv(t, Folder{})
	image(t, filepath.Join(e.root, "k", "cover.png"), "red", 100, 100)
	id := e.track(t, "k/s.mp3", "x", "y", "")
	if _, err := e.svc.Get(e.ctx, id, 300); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(e.svc.Dir)
	e.svc.Imager = &failImager{}
	if _, err := e.svc.Get(e.ctx, id, 300); !errors.Is(err, ErrNoCover) {
		t.Fatal(err)
	}
	if found, src, _ := e.lookupRow(t, id); found != 1 || src != "folder" {
		t.Fatalf("found=1 row downgraded: %d %q", found, src)
	}
}

func TestHungLocalSourceFreesItsSlot(t *testing.T) {
	h := &hangLocal{release: make(chan struct{})}
	t.Cleanup(func() { close(h.release) })
	e := newArtEnv(t, h)
	e.svc.Parallel, e.svc.LocalTimeout, e.svc.LookupTimeout = 1, 100*time.Millisecond, 300*time.Millisecond
	a := e.track(t, "hang/a.mp3", "x", "y", "")
	b := e.track(t, "ok/b.mp3", "x", "y", "")
	start := time.Now()
	if _, err := e.svc.Get(e.ctx, a, 300); !errors.Is(err, ErrNoCover) {
		t.Fatal(err)
	}
	if _, err := e.svc.Get(e.ctx, b, 300); !errors.Is(err, ErrNoCover) {
		t.Fatal(err)
	}
	if time.Since(start) > 2*time.Second || h.n.Load() != 2 {
		t.Fatalf("slot not freed: %v, %d lookups", time.Since(start), h.n.Load())
	}
	if _, _, ok := e.lookupRow(t, a); ok {
		t.Fatal("a timeout was recorded as a miss")
	}
	if found, _, ok := e.lookupRow(t, b); !ok || found != 0 {
		t.Fatal("b's clean miss not recorded")
	}
}

func TestHungImagerFreesItsSlot(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	e := newArtEnv(t, Folder{})
	e.svc.Imager = hangImager{release}
	e.svc.Parallel, e.svc.LocalTimeout, e.svc.LookupTimeout = 1, 100*time.Millisecond, 300*time.Millisecond
	image(t, filepath.Join(e.root, "m", "cover.png"), "red", 100, 100)
	a := e.track(t, "m/a.mp3", "x", "y", "")
	b := e.track(t, "n/b.mp3", "x", "y", "")
	start := time.Now()
	e.svc.Get(e.ctx, a, 300)
	if _, err := e.svc.Get(e.ctx, b, 300); !errors.Is(err, ErrNoCover) || time.Since(start) > 2*time.Second {
		t.Fatalf("%v after %v", err, time.Since(start))
	}
	if _, _, ok := e.lookupRow(t, a); ok {
		t.Fatal("a hung conversion was recorded")
	}
	if _, _, ok := e.lookupRow(t, b); !ok {
		t.Fatal("b never looked up")
	}
}

func TestQueuedLookupWithoutWaitersIsDropped(t *testing.T) {
	slow := &countProv{local: true, delay: 300 * time.Millisecond}
	e := newArtEnv(t, slow)
	e.svc.Parallel = 1
	a := e.track(t, "q/a.mp3", "x", "y", "")
	b := e.track(t, "q/b.mp3", "x", "y", "")
	done := make(chan struct{})
	go func() { defer close(done); e.svc.Get(e.ctx, a, 300) }()
	for slow.n.Load() == 0 { // a holds the only slot
		time.Sleep(5 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(e.ctx, 50*time.Millisecond)
	defer cancel()
	if _, err := e.svc.Get(ctx, b, 300); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	<-done
	deadline := time.Now().Add(2 * time.Second)
	for {
		e.svc.mu.Lock()
		n := len(e.svc.calls)
		e.svc.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("queued lookup never finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if slow.n.Load() != 1 {
		t.Fatalf("an abandoned queued lookup still ran: %d lookups", slow.n.Load())
	}
	if _, _, ok := e.lookupRow(t, b); ok {
		t.Fatal("dropped lookup wrote a row")
	}
	e.svc.Get(e.ctx, b, 300) // asked again with a waiter: runs
	if slow.n.Load() != 2 {
		t.Fatalf("%d lookups", slow.n.Load())
	}
}

func TestRedirectsMustStayHTTPS(t *testing.T) {
	png := filepath.Join(t.TempDir(), "art.png")
	image(t, png, "purple", 400, 400)
	body, _ := os.ReadFile(png)
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(body)
	}))
	defer plain.Close()
	var tls *httptest.Server
	tls = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/down":
			http.Redirect(w, r, plain.URL+"/x.png", http.StatusFound)
		case "/loop":
			http.Redirect(w, r, tls.URL+"/loop", http.StatusFound)
		}
	}))
	defer tls.Close()
	prov := &staticProv{found: []Found{
		{Source: "itunes", Title: "x", URL: tls.URL + "/down", Stream: -1},
		{Source: "itunes", Title: "x", URL: tls.URL + "/loop", Stream: -1},
	}}
	e := newArtEnv(t, prov)
	e.svc.HTTP = tls.Client()
	id := e.track(t, "r/x.mp3", "x", "", "")
	if _, err := e.svc.Get(e.ctx, id, 300); !errors.Is(err, ErrNoCover) {
		t.Fatal(err)
	}
	if _, _, ok := e.lookupRow(t, id); ok {
		t.Fatal("row written")
	}
	if n := countFiles(t, e.svc.Dir); n != 0 {
		t.Fatalf("cache holds %d files", n)
	}
}

// A local source that errors (ffprobe timing out on a busy USB disk) may
// still hold the cover: neither an online clean miss nor "no online
// sources" may turn that into a 30-day miss — the track only backs off.
func TestErroringLocalSourceIsNotAMiss(t *testing.T) {
	for _, tc := range []struct {
		name  string
		provs func(bad *countProv) []Provider
	}{
		{"online clean miss", func(bad *countProv) []Provider { return []Provider{bad, Folder{}, &staticProv{}} }},
		{"no online sources", func(bad *countProv) []Provider { return []Provider{bad, Folder{}} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := &countProv{local: true, err: errors.New("ffprobe: signal: killed")}
			e := newArtEnv(t, tc.provs(bad)...)
			id := e.track(t, "e/x.mp3", "甜蜜蜜", "邓丽君", "")
			if _, err := e.svc.Get(e.ctx, id, 300); !errors.Is(err, ErrNoCover) {
				t.Fatalf("want ErrNoCover, got %v", err)
			}
			if _, _, ok := e.lookupRow(t, id); ok {
				t.Fatal("an erroring local source was recorded as a miss")
			}
			if !e.svc.inBackoff(id) {
				t.Fatal("track not in failure backoff")
			}
		})
	}
}
