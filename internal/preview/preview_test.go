package preview

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/auth"
	"github.com/aaronsuns/lark-server/internal/channels"
	"github.com/aaronsuns/lark-server/internal/download"
	"github.com/aaronsuns/lark-server/internal/testutil"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// fakeDL writes data to the preview file in the chunks the test sends, then
// finishes (with err, if set) when chunks is closed.
type fakeDL struct {
	mu     sync.Mutex
	meta   ytdlp.PreviewMeta
	data   []byte
	chunks chan int
	err    error
	calls  int
	// metaGate, when set, holds onMeta (the size) back until it is closed —
	// yt-dlp announces the size only seconds after the request.
	metaGate chan struct{}
	// progSeq, when set, is what an hd download reports (instead of 50) as it starts.
	progSeq []float64
}

// next gives the fake a fresh chunk channel (closed: the next download finishes at once).
func (f *fakeDL) next(closed bool) chan int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.chunks = make(chan int, 100)
	if closed {
		close(f.chunks)
	}
	return f.chunks
}

// setErr sets the error the next downloads finish with.
func (f *fakeDL) setErr(err error) {
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
}

func (f *fakeDL) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeDL) DownloadPreview(ctx context.Context, v ytdlp.Video, destNoExt string, kind ytdlp.MediaKind, onMeta func(ytdlp.PreviewMeta), onProgress func(float64)) (string, error) {
	ext := ".m4a"
	if kind == ytdlp.MediaVideo {
		ext = ".mp4"
	}
	return f.download(ctx, destNoExt, ext, onMeta, onProgress)
}

// DownloadPreviewHD writes <destNoExt>.mp4 the same way, reporting 50% as it
// starts and 100% at the end.
func (f *fakeDL) DownloadPreviewHD(ctx context.Context, v ytdlp.Video, destNoExt string, onProgress func(float64), onMeta func(ytdlp.PreviewMeta)) (string, error) {
	return f.download(ctx, destNoExt, ".mp4", onMeta, onProgress)
}

func (f *fakeDL) download(ctx context.Context, destNoExt, ext string, onMeta func(ytdlp.PreviewMeta), onProgress func(float64)) (string, error) {
	f.mu.Lock()
	f.calls++
	chunks, meta, ferr, data, gate, seq := f.chunks, f.meta, f.err, f.data, f.metaGate, f.progSeq
	f.mu.Unlock()
	p := destNoExt + ext
	os.WriteFile(destNoExt+".jpg", []byte("jpg"), 0o644)
	fh, err := os.Create(p)
	if err != nil {
		return "", err
	}
	defer fh.Close()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	onMeta(meta)
	if onProgress != nil {
		if seq == nil {
			seq = []float64{50}
		}
		for _, pct := range seq {
			onProgress(pct)
		}
	}
	off := 0
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case n, ok := <-chunks:
			if !ok {
				if ferr != nil {
					return "", ferr
				}
				fh.Write(data[off:])
				if onProgress != nil {
					onProgress(100)
				}
				return p, nil
			}
			fh.Write(data[off : off+n])
			fh.Sync()
			off += n
		}
	}
}

// startDone starts anna's preview of (video, media) with a download that
// finishes at once, and waits until it is done.
func (e *env) startDone(videoID, media string) int64 {
	e.t.Helper()
	e.dl.next(true)
	p, err := e.svc.Start(context.Background(), e.anna, Request{VideoID: videoID, Media: media})
	if err != nil {
		e.t.Fatal(err)
	}
	e.waitStatus(p.ID, "done")
	e.waitIdle()
	return p.ID
}

// waitCalls waits until the fake downloader has been called n times.
func (e *env) waitCalls(n int) {
	e.t.Helper()
	for range 1000 {
		if e.dl.callCount() >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	e.t.Fatalf("downloader called %d times, want %d", e.dl.callCount(), n)
}

// waitFile waits until the preview file holds at least n bytes (its size
// was announced before the first byte, so total is known by then).
func (e *env) waitFile(name string, n int64) {
	e.t.Helper()
	for range 1000 {
		if st, err := os.Stat(filepath.Join(e.svc.Root, name)); err == nil && st.Size() >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	e.t.Fatalf("%s never reached %d bytes", name, n)
}

type fakeMusic struct {
	calls []string
}

func (f *fakeMusic) Adopt(ctx context.Context, userID int64, v ytdlp.Video, src string) (download.Job, bool, error) {
	f.calls = append(f.calls, fmt.Sprintf("%d %s %s %s", userID, v.ID, v.Title, filepath.Base(src)))
	os.Remove(src) // a real Adopt moves it away
	return download.Job{ID: 9, VideoID: v.ID, Status: download.StatusDone}, true, nil
}

type fakeEpisodes struct{ got []channels.Adopt }

func (f *fakeEpisodes) last() channels.Adopt { return f.got[len(f.got)-1] }

func (f *fakeEpisodes) AdoptEpisode(ctx context.Context, userID int64, in channels.Adopt) error {
	f.got = append(f.got, in)
	os.Remove(in.Src)
	return nil
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

type env struct {
	t     *testing.T
	db    *sql.DB
	svc   *Service
	dl    *fakeDL
	music *fakeMusic
	eps   *fakeEpisodes
	clock *clock
	anna  int64
	bo    int64
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d := testutil.DB(t)
	e := &env{t: t, db: d, dl: &fakeDL{chunks: make(chan int, 100), data: make([]byte, 1000)}, music: &fakeMusic{}, eps: &fakeEpisodes{},
		clock: &clock{t: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)}}
	for i := range e.dl.data {
		e.dl.data[i] = byte(i % 251)
	}
	e.svc = &Service{DB: d, YT: e.dl, Music: e.music, Episodes: e.eps, Root: t.TempDir(), TTL: 24 * time.Hour, MaxBytes: 1 << 30,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: e.clock.Now, WaitCap: 2 * time.Second}
	st := &auth.Store{DB: d}
	a, _ := st.CreateUser(context.Background(), "anna", "pw", auth.RoleMember)
	b, _ := st.CreateUser(context.Background(), "bo", "pw", auth.RoleMember)
	e.anna, e.bo = a.ID, b.ID
	return e
}

func (e *env) waitStatus(id int64, want string) Preview {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		p, err := e.svc.Get(context.Background(), id)
		if err == nil && p.Status == want {
			return p
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("preview %d: %+v %v, want %s", id, p, err, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitIdle waits until every download has finished (finish() has run to
// its end), so Downloading() can be asserted without a race.
func (e *env) waitIdle() {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for e.svc.Downloading() {
		if time.Now().After(deadline) {
			e.t.Fatal("a preview download never finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitGone waits until preview id's row is gone (Get no longer finds it).
func (e *env) waitGone(id int64) error {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := e.svc.Get(context.Background(), id)
		if err != nil {
			return err
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("preview %d still there", id)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStartSharesAndCaps(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p, err := e.svc.Start(ctx, e.anna, Request{VideoID: "pv_00000001", Media: "audio", Title: "Song", Channel: "Chan"})
	if err != nil || p.Status != "downloading" || p.StreamURL != fmt.Sprintf("/api/v1/previews/%d/stream", p.ID) || p.Title != "Song" {
		t.Fatalf("%+v %v", p, err)
	}
	if !e.svc.Downloading() {
		t.Fatal("a running preview makes episodes wait")
	}
	q, _ := e.svc.Start(ctx, e.bo, Request{VideoID: "pv_00000001", Media: "audio"})
	if q.ID != p.ID {
		t.Fatal("one file per video and media, shared")
	}
	close(e.dl.chunks)
	e.waitStatus(p.ID, "done")
	e.waitIdle()
	if e.svc.Downloading() {
		t.Fatal("idle again")
	}
	e.svc.MaxPerUser = 2
	e.dl.next(true)
	if _, err := e.svc.Start(ctx, e.anna, Request{VideoID: "pv_00000002", Media: "audio"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Start(ctx, e.anna, Request{VideoID: "pv_00000003", Media: "audio"}); !errors.Is(err, ErrLimit) {
		t.Fatalf("third live preview of anna: %v", err)
	}
	for _, bad := range []Request{{VideoID: "nope", Media: "audio"}, {VideoID: "pv_00000004", Media: "flac"}} {
		if _, err := e.svc.Start(ctx, e.bo, bad); !errors.Is(err, ErrBadVideo) && !errors.Is(err, ErrBadMedia) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	e.waitIdle()
}

func TestFailedPreviewIsRetriedOnNextStart(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.dl.setErr(errors.New("yt-dlp: exit status 1: ERROR: [youtube] x: Requested format is not available"))
	close(e.dl.chunks)
	p, _ := e.svc.Start(ctx, e.anna, Request{VideoID: "pv_00000001", Media: "audio"})
	f := e.waitStatus(p.ID, "failed")
	if f.Error != "[youtube] x: Requested format is not available" {
		t.Fatalf("%q", f.Error)
	}
	e.waitIdle()
	if ents, _ := os.ReadDir(e.svc.Root); len(ents) != 0 {
		t.Fatalf("failed files removed: %v", ents)
	}
	e.dl.setErr(nil)
	e.dl.next(true)
	q, _ := e.svc.Start(ctx, e.anna, Request{VideoID: "pv_00000001", Media: "audio"})
	e.waitStatus(q.ID, "done")
	e.waitIdle()
}

// A video with no progressive mp4 (neither format 18 nor
// another ≤480p) fails as video_preview_unavailable, and asking again
// within the hour answers that at once instead of running yt-dlp again.
func TestVideoPreviewUnavailable(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.dl.setErr(errors.New("yt-dlp: exit status 1: ERROR: [youtube] pv_00000001: Requested format is not available. Use --list-formats for a list of available formats"))
	close(e.dl.chunks)
	p, _ := e.svc.Start(ctx, e.anna, Request{VideoID: "pv_00000001", Media: "video"})
	f := e.waitStatus(p.ID, "failed")
	if f.Error != CodeVideoUnavailable {
		t.Fatalf("%q", f.Error)
	}
	e.waitIdle()
	if _, err := serve(e, p.ID, "bytes=0-1"); !errors.Is(err, ErrVideoUnavailable) {
		t.Fatalf("stream: %v", err)
	}
	if _, err := e.svc.Keep(ctx, e.anna, p.ID, "channel"); !errors.Is(err, ErrVideoUnavailable) {
		t.Fatalf("keep: %v", err)
	}
	calls := e.dl.callCount()
	if _, err := e.svc.Start(ctx, e.bo, Request{VideoID: "pv_00000001", Media: "video"}); !errors.Is(err, ErrVideoUnavailable) {
		t.Fatalf("again: %v", err)
	}
	if e.dl.callCount() != calls {
		t.Fatal("no second yt-dlp run for a known-unavailable video preview")
	}
	// The audio preview of the same video is another file and still works.
	e.dl.setErr(nil)
	e.dl.next(true)
	a, err := e.svc.Start(ctx, e.anna, Request{VideoID: "pv_00000001", Media: "audio"})
	if err != nil {
		t.Fatal(err)
	}
	e.waitStatus(a.ID, "done")
	e.waitIdle()
	// After the hour the sweeper forgets the failure; yt-dlp may try again.
	e.clock.Add(time.Hour + time.Minute)
	if _, err := e.svc.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	e.dl.next(true)
	if v, err := e.svc.Start(ctx, e.bo, Request{VideoID: "pv_00000001", Media: "video"}); err != nil || v.Status != "downloading" {
		t.Fatalf("%+v %v", v, err)
	}
	e.waitIdle()
}

// YouTube pushing back (a bot check, a network error, a
// timeout) is never stored as a failed preview: the row and files go, and
// whoever asks about it hears "retry" (503 preview_retry), including a
// stream request waiting on the download when it happened.
func TestPushBackIsRetryableNotFailed(t *testing.T) {
	for _, cause := range []error{
		errors.New("yt-dlp: exit status 1: ERROR: [youtube] pv_00000001: Sign in to confirm you're not a bot. Use --cookies"),
		errors.New("yt-dlp: exit status 1: ERROR: [youtube] pv_00000001: Unable to download webpage: <urlopen error timed out>"),
		fmt.Errorf("killed: %w", context.DeadlineExceeded),
	} {
		e := newEnv(t)
		ctx := context.Background()
		e.dl.meta.Size = 1000
		e.dl.setErr(cause)
		p, _ := e.svc.Start(ctx, e.anna, Request{VideoID: "pv_00000001", Media: "audio"})
		e.dl.chunks <- 10
		e.waitFile("pv_00000001.audio.m4a", 10)
		type result struct {
			n   int
			err error
		}
		got := make(chan result, 1)
		go func() { w, err := serve(e, p.ID, "bytes=500-"); got <- result{w.Body.Len(), err} }()
		time.Sleep(50 * time.Millisecond)
		close(e.dl.chunks)
		if r := <-got; !errors.Is(r.err, ErrRetry) || r.n != 0 {
			t.Fatalf("%v: waiting stream: %d bytes, %v", cause, r.n, r.err)
		}
		if err := e.waitGone(p.ID); !errors.Is(err, ErrRetry) {
			t.Fatalf("%v: get: %v", cause, err)
		}
		e.waitIdle()
		var n int
		e.db.QueryRow(`SELECT COUNT(*) FROM previews WHERE status='failed'`).Scan(&n)
		if ents, _ := os.ReadDir(e.svc.Root); n != 0 || len(ents) != 0 {
			t.Fatalf("%v: nothing stored as failed (%d), no files left (%v)", cause, n, ents)
		}
		if _, err := serve(e, p.ID, "bytes=0-1"); !errors.Is(err, ErrRetry) {
			t.Fatalf("%v: later stream: %v", cause, err)
		}
		if _, err := e.svc.Keep(ctx, e.anna, p.ID, "music"); !errors.Is(err, ErrRetry) {
			t.Fatalf("%v: keep: %v", cause, err)
		}
		// Asking again downloads again.
		e.dl.setErr(nil)
		e.dl.next(true)
		q, err := e.svc.Start(ctx, e.anna, Request{VideoID: "pv_00000001", Media: "audio"})
		if err != nil || q.ID == p.ID {
			t.Fatalf("%v: %+v %v", cause, q, err)
		}
		e.waitStatus(q.ID, "done")
		e.waitIdle()
	}
}

func TestKeep(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	close(e.dl.chunks)
	e.dl.meta = ytdlp.PreviewMeta{Size: 1000, ChannelID: "UC0e5c4U67Vm6sAVK0vxN3Uw", Channel: "刘翔的投资频道", Title: "Real title", DurationS: 880}
	a, _ := e.svc.Start(ctx, e.anna, Request{VideoID: "pv_00000001", Media: "audio", Title: "Shown title"})
	e.waitStatus(a.ID, "done")
	if p, _ := e.svc.Get(ctx, a.ID); p.Title != "Real title" || p.ChannelID != "UC0e5c4U67Vm6sAVK0vxN3Uw" || p.DurationS != 880 {
		t.Fatalf("yt-dlp's own details win: %+v", p)
	}
	res, err := e.svc.Keep(ctx, e.bo, a.ID, "music")
	if err != nil || res.Job == nil || len(e.music.calls) != 1 || e.music.calls[0] != fmt.Sprintf("%d pv_00000001 Real title pv_00000001.audio.m4a", e.bo) {
		t.Fatalf("%+v %v %v", res, err, e.music.calls)
	}
	if _, err := e.svc.Get(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("a kept preview is gone from previews")
	}
	// Nothing left behind but the hour-long copy for whoever still plays it.
	if ents, _ := os.ReadDir(e.svc.Root); len(ents) != 1 || ents[0].Name() != fmt.Sprintf("kept-%d.m4a", a.ID) {
		t.Fatalf("nothing left behind: %v", ents)
	}
	e.dl.next(true)
	v, _ := e.svc.Start(ctx, e.anna, Request{VideoID: "pv_00000002", Media: "video"})
	e.waitStatus(v.ID, "done")
	if _, err := e.svc.Keep(ctx, e.anna, v.ID, "music"); !errors.Is(err, ErrBadMedia) {
		t.Fatalf("a video preview isn't music: %v", err)
	}
	if res, err := e.svc.Keep(ctx, e.anna, v.ID, "channel"); err != nil || res.EpisodeID != "pv_00000002" {
		t.Fatalf("%+v %v", res, err)
	}
	if got := e.eps.got[0]; got.Kind != ytdlp.MediaVideo || got.ChannelID != "UC0e5c4U67Vm6sAVK0vxN3Uw" || got.Thumb == "" || filepath.Ext(got.Src) != ".mp4" {
		t.Fatalf("%+v", got)
	}
	e.dl.meta = ytdlp.PreviewMeta{}
	chunks := e.dl.next(false)
	c, _ := e.svc.Start(ctx, e.anna, Request{VideoID: "pv_00000003", Media: "audio"})
	if _, err := e.svc.Keep(ctx, e.anna, c.ID, "music"); !errors.Is(err, ErrNotReady) {
		t.Fatalf("still downloading: %v", err)
	}
	close(chunks)
	e.waitStatus(c.ID, "done")
	if _, err := e.svc.Keep(ctx, e.anna, c.ID, "channel"); !errors.Is(err, ErrNoChannel) {
		t.Fatalf("no channel id known: %v", err)
	}
	e.waitIdle()
}

func TestSweepTTLCapAndRecovery(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		e.dl.next(true)
		p, _ := e.svc.Start(ctx, e.anna, Request{VideoID: fmt.Sprintf("pv_0000000%d", i), Media: "audio"})
		e.waitStatus(p.ID, "done")
		e.waitIdle()
		e.clock.Add(time.Hour)
	}
	e.clock.Add(22*time.Hour + 30*time.Minute) // the first was last touched 25.5 h ago, the second 24.5 h, the third 23.5 h
	n, err := e.svc.Sweep(ctx)
	if err != nil || n != 2 {
		t.Fatalf("ttl: %d %v", n, err)
	}
	e.svc.MaxBytes = 1500 // two 1000-byte previews: the least recently used goes
	if n, _ := e.svc.Sweep(ctx); n != 0 {
		t.Fatalf("one preview is under the cap: %d", n)
	}
	e.dl.next(true)
	p4, _ := e.svc.Start(ctx, e.anna, Request{VideoID: "pv_00000004", Media: "audio"})
	e.waitStatus(p4.ID, "done")
	e.waitIdle() // the cap sweep runs as the download finishes
	if _, err := e.svc.Get(ctx, 3); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cap: the third is gone: %v", err)
	}
	if _, err := e.svc.Get(ctx, p4.ID); err != nil {
		t.Fatal("the most recently used survives the cap")
	}
	// A row a previous process left downloading is removed at start.
	e.db.Exec(`INSERT INTO previews(video_id,media,status,path,created_at,accessed_at) VALUES ('pv_00000009','audio','downloading','',1,1)`)
	os.WriteFile(filepath.Join(e.svc.Root, "pv_00000009.audio.m4a"), []byte("half"), 0o644)
	e.svc.recover(ctx)
	var n9 int
	e.db.QueryRow(`SELECT COUNT(*) FROM previews WHERE video_id='pv_00000009'`).Scan(&n9)
	if _, err := os.Stat(filepath.Join(e.svc.Root, "pv_00000009.audio.m4a")); n9 != 0 || !os.IsNotExist(err) {
		t.Fatal("interrupted preview removed")
	}
}

func TestFFmpegRemuxEmbedsCover(t *testing.T) {
	dir := t.TempDir()
	src := testutil.Sample(t, dir, "a.m4a") // skips without ffmpeg
	thumb := filepath.Join(dir, "a.jpg")
	if b, err := exec.Command("ffmpeg", "-nostdin", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "color=c=red:s=64x64", "-frames:v", "1", thumb).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, b)
	}
	dst := filepath.Join(dir, "a.keep.m4a")
	if err := FFmpegRemux("ffmpeg")(context.Background(), src, thumb, dst); err != nil {
		t.Fatal(err)
	}
	out, _ := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_type:stream_disposition=attached_pic", "-of", "csv=p=0", dst).Output()
	if !strings.Contains(string(out), "video,1") {
		t.Fatalf("no attached picture: %q", out)
	}
	bare := filepath.Join(dir, "b.keep.m4a")
	if err := FFmpegRemux("ffmpeg")(context.Background(), src, "", bare); err != nil {
		t.Fatal(err)
	}
	if out, _ := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_type", "-of", "csv=p=0", bare).Output(); strings.TrimSpace(string(out)) != "audio" {
		t.Fatalf("no thumbnail: the audio alone: %q", out)
	}
}

func TestStartNeedsFreeDisk(t *testing.T) {
	e := newEnv(t)
	e.svc.FreeBytes = func(string) (int64, error) { return 1 << 30, nil } // 1 GiB: under the 2 GiB floor
	if _, err := e.svc.Start(context.Background(), e.anna, Request{VideoID: "pv_00000001", Media: "audio"}); !errors.Is(err, ErrNoSpace) {
		t.Fatal(err)
	}
	if e.dl.callCount() != 0 {
		t.Fatal("no download")
	}
	e.svc.FreeBytes = func(string) (int64, error) { return 3 << 30, nil }
	close(e.dl.chunks)
	p, err := e.svc.Start(context.Background(), e.anna, Request{VideoID: "pv_00000001", Media: "audio"})
	if err != nil {
		t.Fatal(err)
	}
	e.waitStatus(p.ID, "done")
	e.waitIdle()
}

// Every finished download runs the size-cap sweep at once.
func TestCapSweepAfterEachDownload(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.MaxBytes = 1500
	close(e.dl.chunks)
	a, _ := e.svc.Start(ctx, e.anna, Request{VideoID: "pv_00000001", Media: "audio"})
	e.waitStatus(a.ID, "done")
	e.waitIdle()
	e.clock.Add(16 * time.Minute) // a was last played longer ago than the grace period
	e.dl.next(true)
	b, _ := e.svc.Start(ctx, e.anna, Request{VideoID: "pv_00000002", Media: "audio"})
	e.waitStatus(b.ID, "done")
	e.waitIdle()
	if err := e.waitGone(a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := e.svc.Get(ctx, b.ID); err != nil {
		t.Fatal("the new one stays")
	}
}

// Our own download timeout is push-back once; twice in a row for the same
// video it is stored as failed (preview_too_long), not retried forever.
func TestOwnTimeoutTwiceIsTooLong(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.Timeout = 50 * time.Millisecond
	p, _ := e.svc.Start(ctx, e.anna, Request{VideoID: "pv_00000001", Media: "audio"}) // chunks never close: it times out
	if err := e.waitGone(p.ID); !errors.Is(err, ErrRetry) {
		t.Fatalf("first timeout is push-back: %v", err)
	}
	e.waitIdle()
	q, err := e.svc.Start(ctx, e.anna, Request{VideoID: "pv_00000001", Media: "audio"})
	if err != nil {
		t.Fatal(err)
	}
	f := e.waitStatus(q.ID, "failed")
	e.waitIdle()
	if f.Error != CodeTooLong {
		t.Fatalf("%q", f.Error)
	}
	if _, err := serve(e, q.ID, "bytes=0-1"); !errors.Is(err, ErrTooLong) {
		t.Fatal(err)
	}
	if _, err := e.svc.Start(ctx, e.bo, Request{VideoID: "pv_00000001", Media: "audio"}); !errors.Is(err, ErrTooLong) {
		t.Fatal(err)
	}
	// A success in between resets the count.
	e.svc.Timeout = 5 * time.Second
	e.dl.next(true)
	r, _ := e.svc.Start(ctx, e.anna, Request{VideoID: "pv_00000002", Media: "audio"})
	e.waitStatus(r.ID, "done")
	e.waitIdle()
}

// After shutdown began no new download starts (it could outlive Run).
func TestStartAfterShutdownIsRetry(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.svc.Run(ctx); close(done) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done
	if _, err := e.svc.Start(context.Background(), e.anna, Request{VideoID: "pv_00000001", Media: "audio"}); !errors.Is(err, ErrRetry) {
		t.Fatal(err)
	}
}

// A family member still playing a preview someone else just
// kept goes on playing: the id is served from a kept copy for an hour.
func TestServeAfterKeep(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	close(e.dl.chunks)
	e.dl.meta = ytdlp.PreviewMeta{Size: 1000, ChannelID: "UC0e5c4U67Vm6sAVK0vxN3Uw", Channel: "C", Title: "T"}
	for _, to := range []string{"music", "channel"} {
		e.dl.next(true)
		p, _ := e.svc.Start(ctx, e.anna, Request{VideoID: "pv_0000000" + map[string]string{"music": "1", "channel": "2"}[to], Media: "audio"})
		e.waitStatus(p.ID, "done")
		e.waitIdle()
		if _, err := e.svc.Keep(ctx, e.bo, p.ID, to); err != nil {
			t.Fatal(err)
		}
		w, err := serve(e, p.ID, "bytes=10-19")
		if err != nil || w.Code != 206 || !bytes.Equal(w.Body.Bytes(), e.dl.data[10:20]) {
			t.Fatalf("%s: %v %d", to, err, w.Code)
		}
	}
	e.clock.Add(time.Hour + time.Minute)
	e.svc.Sweep(ctx)
	if _, err := serve(e, 1, "bytes=0-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an hour later: %v", err)
	}
	if ents, _ := os.ReadDir(e.svc.Root); len(ents) != 0 {
		t.Fatalf("kept copies removed: %v", ents)
	}
}

// An audio preview is downloaded with --fixup never, so one
// kept into music without a thumbnail is still remuxed (stream copy,
// faststart) to get its duration and tags right.
func TestKeepIntoMusicRemuxesWithoutThumbnail(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	var remuxed []string
	e.svc.Remux = func(ctx context.Context, src, thumb, dst string) error {
		remuxed = append(remuxed, filepath.Base(src)+"|"+thumb+"|"+filepath.Base(dst))
		b, _ := os.ReadFile(src)
		return os.WriteFile(dst, b, 0o644)
	}
	close(e.dl.chunks)
	a, _ := e.svc.Start(ctx, e.anna, Request{VideoID: "pv_00000001", Media: "audio", Title: "T"})
	e.waitStatus(a.ID, "done")
	e.waitIdle()
	os.Remove(filepath.Join(e.svc.Root, "pv_00000001.audio.jpg")) // no thumbnail came with it
	if _, err := e.svc.Keep(ctx, e.anna, a.ID, "music"); err != nil {
		t.Fatal(err)
	}
	if len(remuxed) != 1 || remuxed[0] != "pv_00000001.audio.m4a||pv_00000001.audio.keep.m4a" {
		t.Fatalf("remux calls: %q", remuxed)
	}
	if len(e.music.calls) != 1 || !strings.HasSuffix(e.music.calls[0], " pv_00000001.audio.keep.m4a") {
		t.Fatalf("the remuxed file is adopted: %v", e.music.calls)
	}
}

// FFmpegRemux's arguments, through a fake ffmpeg that records them: a
// cover is attached when there is one; either way streams are copied and
// the result is faststart.
func TestFFmpegRemuxArgs(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "args")
	fake := filepath.Join(dir, "ffmpeg")
	os.WriteFile(fake, []byte("#!/bin/sh\necho \"$@\" >> "+log+"\n"), 0o755)
	src, thumb, dst := filepath.Join(dir, "a.m4a"), filepath.Join(dir, "a.jpg"), filepath.Join(dir, "a.keep.m4a")
	if err := FFmpegRemux(fake)(context.Background(), src, thumb, dst); err != nil {
		t.Fatal(err)
	}
	if err := FFmpegRemux(fake)(context.Background(), src, "", dst); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	want := []string{
		"-nostdin -loglevel error -y -i " + src + " -i " + thumb + " -map 0:a -map 1:v -c copy -disposition:v:0 attached_pic -movflags +faststart " + dst,
		"-nostdin -loglevel error -y -i " + src + " -map 0:a -c copy -movflags +faststart " + dst,
	}
	if len(lines) != 2 || lines[0] != want[0] || lines[1] != want[1] {
		t.Fatalf("ffmpeg args:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}

func TestHDPreviewDownloadsWholeThenServes(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.dl.meta = ytdlp.PreviewMeta{Size: 0, Title: "T", ChannelID: "UC0e5c4U67Vm6sAVK0vxN3Uw", Description: "第一行"}
	p, err := e.svc.Start(ctx, e.anna, Request{VideoID: "rvOZh8idOrU", Media: MediaHD})
	if err != nil || p.Media != "hd" || p.Status != "downloading" {
		t.Fatalf("%+v %v", p, err)
	}
	e.dl.chunks <- 400
	e.waitFile("rvOZh8idOrU.hd.mp4", 400)
	// Bytes exist, but an HD file is merged at the end: never served while it grows.
	if _, err := serve(e, p.ID, "bytes=0-1"); !errors.Is(err, ErrNotReady) {
		t.Fatalf("serve while downloading: %v, want ErrNotReady at once", err)
	}
	for range 400 {
		if got, _ := e.svc.Get(ctx, p.ID); got.Progress > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got, _ := e.svc.Get(ctx, p.ID); got.Progress <= 0 {
		t.Errorf("progress %v while downloading", got.Progress)
	}
	close(e.dl.chunks)
	got := e.waitStatus(p.ID, "done")
	if got.Description != "第一行" || got.Progress != 0 {
		t.Errorf("%+v", got)
	}
	w, err := serve(e, p.ID, "bytes=0-9")
	if err != nil || w.Code != 206 || w.Header().Get("Content-Type") != "video/mp4" {
		t.Fatalf("%d %v", w.Code, err)
	}
}

func TestHDHasItsOwnSlot(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.Slots, e.svc.HDSlots = 1, 1
	hold := e.dl.next(false) // the HD download blocks
	if _, err := e.svc.Start(ctx, e.anna, Request{VideoID: "rvOZh8idOrU", Media: MediaHD}); err != nil {
		t.Fatal(err)
	}
	// A 360p preview of another video still starts at once.
	if _, err := e.svc.Start(ctx, e.bo, Request{VideoID: "PncPZ-E1GjE", Media: "video"}); err != nil {
		t.Fatal(err)
	}
	e.waitCalls(2) // both downloads are running: the HD one did not take the preview slot
	close(hold)
	e.waitIdle()
}

func TestHDUnavailableIsRemembered(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.dl.setErr(errors.New("ERROR: [youtube] x: Requested format is not available"))
	p, _ := e.svc.Start(ctx, e.anna, Request{VideoID: "rvOZh8idOrU", Media: MediaHD})
	close(e.dl.chunks)
	if got := e.waitStatus(p.ID, "failed"); got.Error != CodeHDUnavailable {
		t.Fatalf("error %q", got.Error)
	}
	if _, err := e.svc.Start(ctx, e.anna, Request{VideoID: "rvOZh8idOrU", Media: MediaHD}); !errors.Is(err, ErrHDUnavailable) {
		t.Fatalf("restart: %v", err)
	}
}

func TestKeepHDIntoChannelsIsTheEpisodeVideo(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.dl.meta = ytdlp.PreviewMeta{ChannelID: "UC0e5c4U67Vm6sAVK0vxN3Uw", Channel: "刘翔的投资频道"}
	p, _ := e.svc.Start(ctx, e.anna, Request{VideoID: "rvOZh8idOrU", Media: MediaHD})
	close(e.dl.chunks)
	e.waitStatus(p.ID, "done")
	if _, err := e.svc.Keep(ctx, e.anna, p.ID, "music"); !errors.Is(err, ErrBadMedia) {
		t.Fatalf("hd into music: %v", err)
	}
	if _, err := e.svc.Keep(ctx, e.anna, p.ID, "channel"); err != nil {
		t.Fatal(err)
	}
	if got := e.eps.last(); got.Kind != ytdlp.MediaVideo || !strings.HasSuffix(got.Src, ".hd.mp4") {
		t.Fatalf("adopted %+v", got)
	}
}

func TestCapSparesRecentlyPlayed(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.MaxBytes = 1500 // two 1000-byte previews do not fit
	a := e.startDone("rvOZh8idOrU", "video")
	e.clock.Add(time.Minute)
	b := e.startDone("PncPZ-E1GjE", "video") // its cap sweep would take a, the least recently played…
	// …but a was played a minute ago: it stays, over the cap, until it has been idle 15 min.
	if _, err := e.svc.Get(ctx, a); err != nil {
		t.Fatalf("a evicted while being watched: %v", err)
	}
	e.clock.Add(16 * time.Minute)
	if n, _ := e.svc.Sweep(ctx); n != 1 {
		t.Fatalf("sweep removed %d, want 1 (a)", n)
	}
	if _, err := e.svc.Get(ctx, b); err != nil {
		t.Fatal(err)
	}
}

func TestUserLimitReleasesIdle(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.MaxPerUser = 2
	first := e.startDone("rvOZh8idOrU", "video")
	e.startDone("PncPZ-E1GjE", "video")
	// Both played just now: a third is refused, as before.
	if _, err := e.svc.Start(ctx, e.anna, Request{VideoID: "aaaaaaaaaaa", Media: "video"}); !errors.Is(err, ErrLimit) {
		t.Fatalf("got %v, want ErrLimit", err)
	}
	e.clock.Add(20 * time.Minute)
	e.dl.next(true)
	if _, err := e.svc.Start(ctx, e.anna, Request{VideoID: "aaaaaaaaaaa", Media: "video"}); err != nil {
		t.Fatalf("an idle preview should make room: %v", err)
	}
	if _, err := e.svc.Get(ctx, first); !errors.Is(err, ErrNotFound) {
		t.Errorf("the least recently played was not released: %v", err)
	}
	e.waitIdle()
}

// blockWriter holds every Write until gate is closed: a client mid-playback.
type blockWriter struct {
	*httptest.ResponseRecorder
	gate chan struct{}
}

func (b *blockWriter) Write(p []byte) (int, error) {
	<-b.gate
	return b.ResponseRecorder.Write(p)
}

func (e *env) servingCount(id int64) int {
	e.svc.mu.Lock()
	defer e.svc.mu.Unlock()
	return e.svc.serving[id]
}

func TestHDFinishedIsNotEvictedAtOnce(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.MaxBytes = 1500
	hold := e.dl.next(false)
	hd, err := e.svc.Start(ctx, e.anna, Request{VideoID: "rvOZh8idOrU", Media: MediaHD})
	if err != nil {
		t.Fatal(err)
	}
	e.waitCalls(1)
	e.clock.Add(20 * time.Minute)
	b := e.startDone("PncPZ-E1GjE", "video")
	e.clock.Add(16 * time.Minute)
	close(hold)
	e.waitStatus(hd.ID, "done")
	e.waitIdle()
	if _, err := e.svc.Get(ctx, hd.ID); err != nil {
		t.Fatalf("the hd preview that just finished was evicted: %v", err)
	}
	if _, err := e.svc.Get(ctx, b); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the idle one should have gone: %v", err)
	}
}

func TestServingPreviewIsNeverReleased(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.MaxPerUser = 1
	a := e.startDone("rvOZh8idOrU", "video")
	e.clock.Add(20 * time.Minute)
	gate := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		w := &blockWriter{httptest.NewRecorder(), gate}
		done <- e.svc.Serve(w, httptest.NewRequest("GET", "/x", nil), a)
	}()
	for range 400 {
		if e.servingCount(a) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if e.servingCount(a) != 1 {
		t.Fatal("serve not counted")
	}
	e.clock.Add(20 * time.Minute) // idle by the clock, but someone is watching
	e.svc.MaxBytes = 500
	if n, _ := e.svc.Sweep(ctx); n != 0 {
		t.Fatalf("sweep took a preview being served (%d)", n)
	}
	if _, err := e.svc.Start(ctx, e.anna, Request{VideoID: "aaaaaaaaaaa", Media: "video"}); !errors.Is(err, ErrLimit) {
		t.Fatalf("room made from a preview being served: %v", err)
	}
	close(gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if e.servingCount(a) != 0 {
		t.Fatal("serve count not released")
	}
	if _, err := e.svc.Get(ctx, a); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.svc.Sweep(ctx); n != 0 {
		t.Fatalf("just served: spared for the grace period, swept %d", n)
	}
}

func TestHDTimeoutStartsAfterTheSlot(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.HDSlots, e.svc.HDTimeout = 1, 400*time.Millisecond
	hold1 := e.dl.next(false)
	if _, err := e.svc.Start(ctx, e.anna, Request{VideoID: "rvOZh8idOrU", Media: MediaHD}); err != nil {
		t.Fatal(err)
	}
	e.waitCalls(1)
	hold2 := e.dl.next(false)
	p2, err := e.svc.Start(ctx, e.bo, Request{VideoID: "PncPZ-E1GjE", Media: MediaHD})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)
	close(hold1)
	e.waitCalls(2)
	time.Sleep(250 * time.Millisecond) // 500ms since it was asked for, 250ms since it began
	close(hold2)
	e.waitStatus(p2.ID, "done")
}

// A second 高清 while the one HD slot is busy is queued, and says so; one
// that waits out its whole timeout for the slot fails as preview_queue_timeout
// (not this video's fault: asking again starts afresh).
func TestHDQueuedWhileTheSlotIsBusy(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.HDSlots, e.svc.HDTimeout = 1, 300*time.Millisecond
	e.svc.init()
	e.svc.hdSlots <- struct{}{} // a long 高清 holds the one slot
	p2, err := e.svc.Start(ctx, e.bo, Request{VideoID: "PncPZ-E1GjE", Media: MediaHD})
	if err != nil {
		t.Fatal(err)
	}
	var got Preview
	for range 100 {
		if got, _ = e.svc.Get(ctx, p2.ID); got.Queued {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !got.Queued || got.Status != "downloading" {
		t.Fatalf("second hd: %+v, want queued", got)
	}
	e.waitStatus(p2.ID, "failed")
	if got, _ = e.svc.Get(ctx, p2.ID); got.Error != CodeQueueTimeout || got.Queued {
		t.Fatalf("timed out in the queue: %+v", got)
	}
	<-e.svc.hdSlots // it finished
	hold3 := e.dl.next(false)
	p3, err := e.svc.Start(ctx, e.bo, Request{VideoID: "PncPZ-E1GjE", Media: MediaHD})
	if err != nil {
		t.Fatalf("asking again after a queue timeout: %v", err)
	}
	e.waitCalls(1)
	if got, _ = e.svc.Get(ctx, p3.ID); got.Queued {
		t.Fatalf("running, not queued: %+v", got)
	}
	close(hold3)
	e.waitStatus(p3.ID, "done")
}

func TestStartRefusedForDiskReleasesNothing(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.MaxPerUser = 1
	a := e.startDone("rvOZh8idOrU", "video")
	e.clock.Add(20 * time.Minute)
	e.svc.FreeBytes = func(string) (int64, error) { return 1 << 30, nil }
	if _, err := e.svc.Start(ctx, e.anna, Request{VideoID: "aaaaaaaaaaa", Media: "video"}); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("got %v, want ErrNoSpace", err)
	}
	if _, err := e.svc.Get(ctx, a); err != nil {
		t.Fatalf("a preview was released for a start that was refused: %v", err)
	}
}

func TestHDProgressIsOneRun(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.dl.progSeq = []float64{80, 100, 40} // video stream, then audio stream
	p, _ := e.svc.Start(ctx, e.anna, Request{VideoID: "rvOZh8idOrU", Media: MediaHD})
	var got float64
	for range 400 {
		if got, _ = func() (float64, error) { x, err := e.svc.Get(ctx, p.ID); return x.Progress, err }(); got >= 93 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got < 93.9 || got > 94.1 {
		t.Fatalf("progress %v, want 94 (video 0-90, audio 90-100)", got)
	}
	close(e.dl.chunks)
	e.waitStatus(p.ID, "done")
}

func TestHDDoesNotHoldBackEpisodes(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	hold := e.dl.next(false)
	e.svc.Start(ctx, e.anna, Request{VideoID: "rvOZh8idOrU", Media: MediaHD})
	e.waitCalls(1)
	if e.svc.Downloading() {
		t.Error("an hd download must not hold the episode worker back")
	}
	close(hold)
	e.waitIdle()
}

// A video preview whose format 18 YouTube refuses is a
// merged ≤360p. Its bytes are never served while it grows (yt-dlp merges at
// the end); the preview says merged and reports progress like 高清 (video
// 0-90, audio 90-100), and a stream request waits for the complete file.
func TestMergedVideoPreviewServedOnceComplete(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.WaitCap = 300 * time.Millisecond
	e.dl.meta = ytdlp.PreviewMeta{Merged: true, Title: "T", ChannelID: "UC0e5c4U67Vm6sAVK0vxN3Uw"}
	e.dl.progSeq = []float64{80, 100, 40}
	p, err := e.svc.Start(ctx, e.anna, Request{VideoID: "rvOZh8idOrU", Media: "video"})
	if err != nil {
		t.Fatal(err)
	}
	e.dl.chunks <- 400
	e.waitFile("rvOZh8idOrU.video.mp4", 400)
	var got Preview
	for range 400 {
		if got, _ = e.svc.Get(ctx, p.ID); got.Merged && got.Progress >= 93 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !got.Merged || got.Progress < 93.9 || got.Progress > 94.1 || got.Status != "downloading" {
		t.Fatalf("%+v, want merged at 94%%", got)
	}
	if !e.svc.Downloading() {
		t.Error("a merged 360p uses the normal preview slots: it holds the episode worker back")
	}
	// Bytes exist, but nothing is served while it grows.
	if w, err := serve(e, p.ID, "bytes=0-1"); !errors.Is(err, ErrNotReady) {
		t.Fatalf("serve while downloading: %d %v, want ErrNotReady after the wait cap", w.Code, err)
	}
	// A request waiting when the merge ends gets the whole file.
	type res struct {
		w   *httptest.ResponseRecorder
		err error
	}
	done := make(chan res, 1)
	go func() {
		w, err := serve(e, p.ID, "bytes=0-9")
		done <- res{w, err}
	}()
	time.Sleep(50 * time.Millisecond)
	close(e.dl.chunks)
	r := <-done
	if r.err != nil || r.w.Code != 206 || r.w.Header().Get("Content-Range") != "bytes 0-9/1000" || r.w.Header().Get("Content-Type") != "video/mp4" {
		t.Fatalf("%d %q %v", r.w.Code, r.w.Header().Get("Content-Range"), r.err)
	}
	if got = e.waitStatus(p.ID, "done"); got.Merged || got.Progress != 0 {
		t.Errorf("done: %+v (merged and progress are download state)", got)
	}
	e.waitIdle()
}

// A progressive 360p (format 18) streams while it grows and has no progress
// to report; nothing about it changed.
func TestProgressiveVideoPreviewStreamsWhileGrowing(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.dl.meta = ytdlp.PreviewMeta{Size: 1000}
	p, _ := e.svc.Start(ctx, e.anna, Request{VideoID: "rvOZh8idOrU", Media: "video"})
	e.dl.chunks <- 10
	e.waitFile("rvOZh8idOrU.video.mp4", 10)
	w, err := serve(e, p.ID, "bytes=0-9")
	if err != nil || w.Code != 206 || w.Header().Get("Content-Range") != "bytes 0-9/1000" {
		t.Fatalf("%d %v", w.Code, err)
	}
	if got, _ := e.svc.Get(ctx, p.ID); got.Merged || got.Progress != 0 {
		t.Errorf("%+v", got)
	}
	close(e.dl.chunks)
	e.waitStatus(p.ID, "done")
	e.waitIdle()
}

// The merged 360p's timeout is 高清's: a progressive one past Timeout is cut,
// a merged one goes on until HDTimeout.
func TestMergedVideoPreviewHasTheHDTimeout(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.Timeout, e.svc.HDTimeout = 100*time.Millisecond, 5*time.Second
	e.dl.meta = ytdlp.PreviewMeta{Merged: true}
	p, _ := e.svc.Start(ctx, e.anna, Request{VideoID: "rvOZh8idOrU", Media: "video"})
	time.Sleep(300 * time.Millisecond)
	if got, err := e.svc.Get(ctx, p.ID); err != nil || got.Status != "downloading" {
		t.Fatalf("merged past Timeout: %+v %v, want still downloading", got, err)
	}
	close(e.dl.chunks)
	e.waitStatus(p.ID, "done")
	e.waitIdle()

	// Past HDTimeout it is Lark's own timeout, as for any download.
	e.svc.HDTimeout = 200 * time.Millisecond
	e.dl.next(false)
	q, _ := e.svc.Start(ctx, e.anna, Request{VideoID: "PncPZ-E1GjE", Media: "video"})
	if err := e.waitGone(q.ID); !errors.Is(err, ErrRetry) {
		t.Fatalf("merged past HDTimeout: %v, want push-back", err)
	}
	e.waitIdle()

	// A progressive one still has Timeout.
	e.dl.meta = ytdlp.PreviewMeta{Size: 1000}
	e.dl.next(false)
	r, _ := e.svc.Start(ctx, e.anna, Request{VideoID: "aaaaaaaaaaa", Media: "video"})
	if err := e.waitGone(r.ID); !errors.Is(err, ErrRetry) {
		t.Fatalf("progressive past Timeout: %v, want push-back", err)
	}
	e.waitIdle()
}
