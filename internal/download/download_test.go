package download

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/library"
	"github.com/aaronsuns/lark-server/internal/media"
	"github.com/aaronsuns/lark-server/internal/testutil"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// entry is one video the fake yt-dlp knows about.
type entry struct {
	ID, Title, Channel string
}

// fakeRunner is a ytdlp.Runner that answers flat-playlist lookups from
// entries and "downloads" by copying a real ffmpeg-generated audio sample to
// destNoExt+".m4a", so the real scanner ingests the result.
type fakeRunner struct {
	sample    string
	entries   []entry
	listID    string // top-level playlist id ("" = none)
	listTitle string
	fail      map[string]bool // video id → yt-dlp fails
	forbid    map[string]int  // video id → this many 403s before it downloads
	block     map[string]bool // video id → blocks until ctx done or release closed
	release   chan struct{}
	started   chan string // receives a video id when a blocking download starts

	// outputGate, when set, makes every Output (a Resolve) wait until it is
	// closed or ctx is done; outputStarted receives once per Output entered.
	outputGate     chan struct{}
	outputStarted  chan struct{}
	outInflight    int
	maxOutInflight int

	mu          sync.Mutex
	outputArgs  [][]string
	streamCalls []string
	inflight    int
	maxInflight int
}

func (f *fakeRunner) Output(ctx context.Context, args []string) ([]byte, error) {
	f.mu.Lock()
	f.outputArgs = append(f.outputArgs, slices.Clone(args))
	f.outInflight++
	f.maxOutInflight = max(f.maxOutInflight, f.outInflight)
	gate := f.outputGate
	f.mu.Unlock()
	defer func() { f.mu.Lock(); f.outInflight--; f.mu.Unlock() }()
	if gate != nil {
		f.outputStarted <- struct{}{}
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, fmt.Errorf("yt-dlp: %w", ctx.Err())
		}
	}
	type e struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		Channel string `json:"channel"`
		URL     string `json:"url"`
	}
	top := struct {
		Type          string `json:"_type"`
		ID            string `json:"id,omitempty"`
		Title         string `json:"title,omitempty"`
		PlaylistCount int    `json:"playlist_count"`
		Entries       []e    `json:"entries"`
	}{Type: "playlist", ID: f.listID, Title: f.listTitle, PlaylistCount: len(f.entries)}
	for _, x := range f.entries {
		top.Entries = append(top.Entries, e{x.ID, x.Title, x.Channel, "https://www.youtube.com/watch?v=" + x.ID})
	}
	return json.Marshal(top)
}

// set flips a fail/block flag for id (under mu: workers read them concurrently).
func (f *fakeRunner) set(m map[string]bool, id string, v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m[id] = v
}

func (f *fakeRunner) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.streamCalls)
}

func (f *fakeRunner) Stream(ctx context.Context, args []string, onLine func(string)) error {
	var destNoExt string
	for i, a := range args {
		if a == "-o" {
			destNoExt = strings.TrimSuffix(args[i+1], ".%(ext)s")
		}
	}
	u, _ := url.Parse(args[len(args)-1])
	id := u.Query().Get("v")

	f.mu.Lock()
	f.streamCalls = append(f.streamCalls, id)
	f.inflight++
	f.maxInflight = max(f.maxInflight, f.inflight)
	f.mu.Unlock()
	defer func() { f.mu.Lock(); f.inflight--; f.mu.Unlock() }()

	if err := os.MkdirAll(filepath.Dir(destNoExt), 0o755); err != nil {
		return err
	}
	f.mu.Lock()
	fail, block := f.fail[id], f.block[id]
	forbidden := f.forbid[id] > 0
	if forbidden {
		f.forbid[id]--
	}
	f.mu.Unlock()
	if forbidden {
		return errors.New("yt-dlp: exit status 1: ERROR: unable to download video data: HTTP Error 403: Forbidden")
	}
	if fail {
		if err := os.WriteFile(destNoExt+".webm.part", []byte("partial"), 0o644); err != nil {
			return err
		}
		return errors.New("yt-dlp: exit status 1: WARNING: something\nERROR: [youtube] " + id + ": Video unavailable")
	}
	if block {
		// A partial download plus a half-converted .m4a: neither may survive
		// a cancel.
		if err := os.WriteFile(destNoExt+".webm.part", []byte("partial"), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(destNoExt+".m4a", []byte("half-converted"), 0o644); err != nil {
			return err
		}
		f.started <- id
		select {
		case <-ctx.Done():
			return fmt.Errorf("yt-dlp: %w", ctx.Err())
		case <-f.release:
			os.Remove(destNoExt + ".webm.part")
		}
	}
	onLine("LARKPROG  42.0%")
	b, err := os.ReadFile(f.sample)
	if err != nil {
		return err
	}
	final := destNoExt + ".m4a"
	if err := os.WriteFile(final, b, 0o644); err != nil {
		return err
	}
	onLine(final)
	return nil
}

type env struct {
	svc    *Service
	fake   *fakeRunner
	store  *library.Store
	lib    library.Library
	root   string
	alice  int64 // member
	bob    int64 // member
	admin  int64
	cancel context.CancelFunc
	done   chan struct{}
}

func addUser(t *testing.T, st *library.Store, name, role string) int64 {
	t.Helper()
	r, err := st.DB.Exec(`INSERT INTO users(username,password_hash,role,created_at) VALUES (?,?,?,0)`, name, "x", role)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := r.LastInsertId()
	return id
}

func newEnv(t *testing.T, withTarget bool) *env {
	t.Helper()
	d := testutil.DB(t)
	st := &library.Store{DB: d}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	root := t.TempDir()
	lib, err := st.EnsureLibrary(context.Background(), "YouTube", root, withTarget)
	if err != nil {
		t.Fatal(err)
	}
	sample := testutil.Sample(t, t.TempDir(), "sample.m4a")
	fake := &fakeRunner{sample: sample, fail: map[string]bool{}, forbid: map[string]int{}, block: map[string]bool{},
		release: make(chan struct{}), started: make(chan string, 10)}
	scans := &library.Service{Store: st, Scanner: &library.Scanner{Store: st, Prober: media.FFprobe{Path: "ffprobe"}, Log: log}, Log: log}
	svc := &Service{DB: d, YT: &ytdlp.Client{Runner: fake}, Library: st, Scans: scans, Log: log, Workers: 2}
	return &env{svc: svc, fake: fake, store: st, lib: lib, root: root,
		alice: addUser(t, st, "alice", "member"), bob: addUser(t, st, "bob", "member"), admin: addUser(t, st, "root", "admin")}
}

// start runs the service until the test ends and asserts Run returns once
// its ctx is cancelled (in-flight workers included).
func (e *env) start(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel, e.done = cancel, make(chan struct{})
	go func() { e.svc.Run(ctx); close(e.done) }()
	t.Cleanup(e.stop(t))
}

func (e *env) stop(t *testing.T) func() {
	return func() {
		e.cancel()
		select {
		case <-e.done:
		case <-time.After(10 * time.Second):
			t.Error("Run did not return after ctx cancel")
		}
	}
}

// waitStarted waits for a blocking fake download to start.
func (e *env) waitStarted(t *testing.T) string {
	t.Helper()
	select {
	case id := <-e.fake.started:
		return id
	case <-time.After(10 * time.Second):
		t.Fatal("no blocking download started in 10s")
		return ""
	}
}

func (e *env) job(t *testing.T, id int64) Job {
	t.Helper()
	j, err := e.svc.get(context.Background(), id)
	if err != nil {
		t.Fatalf("job %d: %v", id, err)
	}
	return j
}

func (e *env) waitStatus(t *testing.T, id int64, want Status) Job {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		j := e.job(t, id)
		if j.Status == want {
			return j
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %d: status %q (error %q), want %q", id, j.Status, j.Error, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (e *env) countStatus(t *testing.T, st Status) int {
	t.Helper()
	var n int
	if err := e.svc.DB.QueryRow(`SELECT COUNT(*) FROM downloads WHERE status=?`, string(st)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *env) trackCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.svc.DB.QueryRow(`SELECT COUNT(*) FROM tracks`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func watchURL(id string) string { return "https://www.youtube.com/watch?v=" + id }

// 1. single video → done, track ingested with override title/artist.
func TestSingleVideoDone(t *testing.T) {
	e := newEnv(t, true)
	e.fake.entries = []entry{{"v1_0000000x", "邓丽君 - 甜蜜蜜【MV】", "Teresa Teng Official"}}
	e.start(t)
	jobs, err := e.svc.Enqueue(context.Background(), e.alice, watchURL("v1_0000000x"))
	if err != nil || len(jobs) != 1 || jobs[0].Status != StatusQueued || jobs[0].Username != "alice" {
		t.Fatalf("enqueue: %+v %v", jobs, err)
	}
	j := e.waitStatus(t, jobs[0].ID, StatusDone)
	if j.TrackID == nil || j.Progress != 100 || j.Error != "" {
		t.Fatalf("done job: %+v", j)
	}
	tr, err := e.store.Track(context.Background(), e.alice, *j.TrackID)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Title != "甜蜜蜜" || tr.Artist != "邓丽君" {
		t.Fatalf("track title/artist = %q/%q", tr.Title, tr.Artist)
	}
	if want := "Teresa Teng Official/甜蜜蜜 [v1_0000000x].m4a"; tr.Path != want {
		t.Fatalf("track path %q, want %q", tr.Path, want)
	}
}

// 2. same URL twice, before and after completion → one job, one track.
func TestDedupe(t *testing.T) {
	e := newEnv(t, true)
	e.fake.entries = []entry{{"v1_0000000x", "Song", "Chan"}}
	ctx := context.Background()
	a, err := e.svc.Enqueue(ctx, e.alice, watchURL("v1_0000000x"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.svc.Enqueue(ctx, e.bob, watchURL("v1_0000000x"))
	if err != nil || len(b) != 1 || b[0].ID != a[0].ID {
		t.Fatalf("queued dedupe: %+v vs %+v (%v)", b, a, err)
	}
	e.start(t)
	e.waitStatus(t, a[0].ID, StatusDone)
	c, err := e.svc.EnqueueVideo(ctx, e.alice, ytdlp.Video{ID: "v1_0000000x", Title: "Song", Channel: "Chan", URL: watchURL("v1_0000000x")})
	if err != nil || c.ID != a[0].ID || c.Status != StatusDone {
		t.Fatalf("done dedupe: %+v (%v)", c, err)
	}
	if n := e.countStatus(t, StatusDone) + e.countStatus(t, StatusQueued); n != 1 {
		t.Fatalf("jobs=%d", n)
	}
	if n := e.trackCount(t); n != 1 {
		t.Fatalf("tracks=%d", n)
	}
}

// 3. playlist of 3 with the 2nd failing → [done, failed, done].
func TestPlaylistPartialFailure(t *testing.T) {
	e := newEnv(t, true)
	e.fake.entries = []entry{{"p1_0000000x", "One", "Chan"}, {"p2_0000000x", "Two", "Chan"}, {"p3_0000000x", "Three", "Chan"}}
	e.fake.fail["p2_0000000x"] = true
	jobs, err := e.svc.Enqueue(context.Background(), e.alice, "https://www.youtube.com/playlist?list=PL1")
	if err != nil || len(jobs) != 3 {
		t.Fatalf("%+v %v", jobs, err)
	}
	e.start(t)
	e.waitStatus(t, jobs[0].ID, StatusDone)
	f := e.waitStatus(t, jobs[1].ID, StatusFailed)
	e.waitStatus(t, jobs[2].ID, StatusDone)
	// ytdlp.LastLine now prefers the text after "ERROR:" over the marker
	// itself (shared with the search-failure message; Task 4 review round 1).
	if f.Error != "[youtube] p2_0000000x: Video unavailable" || f.TrackID != nil {
		t.Fatalf("failed job: %+v", f)
	}
	if _, err := os.Stat(filepath.Join(e.root, "Chan", "Two [p2_0000000x].webm.part")); !os.IsNotExist(err) {
		t.Fatalf("failed job left its partial behind (%v)", err)
	}
}

// 4. resolve returning 250 entries → 200 jobs, argv --playlist-end 200.
func TestPlaylistCap(t *testing.T) {
	e := newEnv(t, true)
	for i := range 250 {
		e.fake.entries = append(e.fake.entries, entry{fmt.Sprintf("id%09d", i), fmt.Sprintf("T%d", i), "Chan"})
	}
	jobs, err := e.svc.Enqueue(context.Background(), e.alice, "https://www.youtube.com/playlist?list=PL1")
	if err != nil || len(jobs) != 200 {
		t.Fatalf("jobs=%d err=%v", len(jobs), err)
	}
	args := e.fake.outputArgs[0]
	if i := slices.Index(args, "--playlist-end"); i < 0 || args[i+1] != "200" {
		t.Fatalf("argv %v", args)
	}
	if n := e.countStatus(t, StatusQueued); n != 200 {
		t.Fatalf("queued rows=%d", n)
	}
}

// 5. cancel while downloading → cancelled, no leftover files.
func TestCancelDownloading(t *testing.T) {
	e := newEnv(t, true)
	e.fake.entries = []entry{{"c1_0000000x", "Blocker", "Chan"}}
	e.fake.block["c1_0000000x"] = true
	e.start(t)
	jobs, err := e.svc.Enqueue(context.Background(), e.alice, watchURL("c1_0000000x"))
	if err != nil {
		t.Fatal(err)
	}
	e.waitStarted(t)
	if _, err := os.Stat(filepath.Join(e.root, "Chan", "Blocker [c1_0000000x].webm.part")); err != nil {
		t.Fatalf("fake did not write partial: %v", err)
	}
	if err := e.svc.Cancel(context.Background(), e.alice, false, jobs[0].ID); err != nil {
		t.Fatal(err)
	}
	j := e.waitStatus(t, jobs[0].ID, StatusCancelled)
	if j.Error != "" || j.TrackID != nil {
		t.Fatalf("cancelled job: %+v", j)
	}
	var left []string
	filepath.WalkDir(e.root, func(p string, d os.DirEntry, _ error) error {
		if !d.IsDir() {
			left = append(left, p)
		}
		return nil
	})
	if len(left) != 0 {
		t.Fatalf("leftover files: %v", left)
	}
	// A cancelled job can be retried and then completes.
	e.fake.set(e.fake.block, "c1_0000000x", false)
	if err := e.svc.Retry(context.Background(), e.alice, false, jobs[0].ID); err != nil {
		t.Fatal(err)
	}
	e.waitStatus(t, jobs[0].ID, StatusDone)
}

// 6. recovery: a downloading row + a .part file → requeued, then done; .part gone.
func TestRecovery(t *testing.T) {
	e := newEnv(t, true)
	e.fake.entries = []entry{{"r1_0000000x", "Recover", "Chan"}}
	ctx := context.Background()
	r, err := e.svc.DB.Exec(`INSERT INTO downloads(user_id,url,video_id,title,channel,status,progress,created_at,updated_at)
		VALUES (?,?,?,?,?,'downloading',37,1,1)`, e.alice, watchURL("r1_0000000x"), "r1_0000000x", "Recover", "Chan")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := r.LastInsertId()
	dir := filepath.Join(e.root, "Chan")
	os.MkdirAll(dir, 0o755)
	part := filepath.Join(dir, "Recover [r1_0000000x].webm.part")
	thumb := filepath.Join(dir, "Recover [r1_0000000x].webp")
	temp := filepath.Join(dir, "Recover [r1_0000000x].temp.m4a")
	cover := filepath.Join(dir, "cover.jpg") // not a yt-dlp artifact: must survive
	for _, p := range []string{part, thumb, temp, cover} {
		if err := os.WriteFile(p, []byte("junk"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The scanner never treats a .part or .temp.m4a file as a track.
	if _, err := e.svc.Scans.ScanNow(ctx, e.lib.ID); err != nil && !errors.Is(err, library.ErrLibraryUnavailable) {
		t.Fatal(err)
	}
	if n := e.trackCount(t); n != 0 {
		t.Fatalf("scanner ingested a partial: tracks=%d", n)
	}

	e.svc.recover(ctx)
	if j := e.job(t, id); j.Status != StatusQueued || j.Progress != 0 {
		t.Fatalf("after recovery: %+v", j)
	}
	for _, p := range []string{part, thumb, temp} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s not removed (%v)", p, err)
		}
	}
	if _, err := os.Stat(cover); err != nil {
		t.Fatalf("unrelated cover.jpg removed: %v", err)
	}

	// And through Run: set it back to downloading and let Run recover + finish it.
	os.WriteFile(part, []byte("junk"), 0o644)
	e.svc.DB.Exec(`UPDATE downloads SET status='downloading' WHERE id=?`, id)
	e.start(t)
	e.waitStatus(t, id, StatusDone)
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Fatalf(".part survived Run: %v", err)
	}
}

// 7. no download_target library → ErrNoTarget.
func TestNoTarget(t *testing.T) {
	e := newEnv(t, false)
	e.fake.entries = []entry{{"v1_0000000x", "Song", "Chan"}}
	if _, err := e.svc.Enqueue(context.Background(), e.alice, watchURL("v1_0000000x")); !errors.Is(err, ErrNoTarget) {
		t.Fatalf("Enqueue err=%v", err)
	}
	if _, err := e.svc.EnqueueVideo(context.Background(), e.alice, ytdlp.Video{ID: "v1_0000000x", URL: watchURL("v1_0000000x")}); !errors.Is(err, ErrNoTarget) {
		t.Fatalf("EnqueueVideo err=%v", err)
	}
	if _, err := e.svc.Enqueue(context.Background(), e.alice, "https://evil.com/watch?v=x"); !errors.Is(err, ytdlp.ErrBadURL) {
		t.Fatalf("bad url err=%v", err)
	}
}

// 8. a member cannot cancel/retry another user's job; admin can.
func TestOwnership(t *testing.T) {
	e := newEnv(t, true)
	e.fake.entries = []entry{{"o1_0000000x", "Owned", "Chan"}}
	ctx := context.Background()
	jobs, err := e.svc.Enqueue(ctx, e.alice, watchURL("o1_0000000x"))
	if err != nil {
		t.Fatal(err)
	}
	id := jobs[0].ID
	if err := e.svc.Cancel(ctx, e.bob, false, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob cancel err=%v", err)
	}
	if err := e.svc.Cancel(ctx, e.bob, false, 9999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing cancel err=%v", err)
	}
	if err := e.svc.Cancel(ctx, e.admin, true, id); err != nil {
		t.Fatalf("admin cancel err=%v", err)
	}
	if j := e.job(t, id); j.Status != StatusCancelled {
		t.Fatalf("status %q", j.Status)
	}
	if err := e.svc.Retry(ctx, e.bob, false, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob retry err=%v", err)
	}
	if err := e.svc.Retry(ctx, e.admin, true, id); err != nil {
		t.Fatalf("admin retry err=%v", err)
	}
	if j := e.job(t, id); j.Status != StatusQueued {
		t.Fatalf("status %q", j.Status)
	}
	mine, err := e.svc.List(ctx, e.bob, false, 0)
	if err != nil || len(mine) != 0 {
		t.Fatalf("bob sees %+v (%v)", mine, err)
	}
	all, err := e.svc.List(ctx, e.bob, true, 0)
	if err != nil || len(all) != 1 || all[0].Username != "alice" {
		t.Fatalf("all: %+v (%v)", all, err)
	}
}

// 9. Workers=2: at most two downloads in flight.
func TestTwoWorkers(t *testing.T) {
	e := newEnv(t, true)
	e.fake.entries = []entry{{"w1_0000000x", "A", "Chan"}, {"w2_0000000x", "B", "Chan"}, {"w3_0000000x", "C", "Chan"}}
	for _, x := range e.fake.entries {
		e.fake.block[x.ID] = true
	}
	jobs, err := e.svc.Enqueue(context.Background(), e.alice, "https://www.youtube.com/playlist?list=PL1")
	if err != nil || len(jobs) != 3 {
		t.Fatal(jobs, err)
	}
	e.start(t)
	e.waitStarted(t)
	e.waitStarted(t)
	if d, q := e.countStatus(t, StatusDownloading), e.countStatus(t, StatusQueued); d != 2 || q != 1 {
		t.Fatalf("downloading=%d queued=%d", d, q)
	}
	close(e.fake.release) // the third starts only once a slot frees up
	e.waitStarted(t)
	for _, j := range jobs {
		e.waitStatus(t, j.ID, StatusDone)
	}
	e.fake.mu.Lock()
	defer e.fake.mu.Unlock()
	if e.fake.maxInflight != 2 {
		t.Fatalf("max in flight = %d, want 2", e.fake.maxInflight)
	}
}

// Retry must not bypass dedupe: once a new job covers the same video, retrying
// the old failed one is a no-op, so two jobs never download into one file.
func TestRetryRespectsDedupe(t *testing.T) {
	e := newEnv(t, true)
	e.fake.entries = []entry{{"d1_0000000x", "Dup", "Chan"}}
	e.fake.fail["d1_0000000x"] = true
	ctx := context.Background()
	e.start(t)
	first, err := e.svc.Enqueue(ctx, e.alice, watchURL("d1_0000000x"))
	if err != nil {
		t.Fatal(err)
	}
	e.waitStatus(t, first[0].ID, StatusFailed)

	e.fake.set(e.fake.fail, "d1_0000000x", false)
	e.fake.set(e.fake.block, "d1_0000000x", true)
	second, err := e.svc.Enqueue(ctx, e.alice, watchURL("d1_0000000x"))
	if err != nil || second[0].ID == first[0].ID {
		t.Fatalf("second enqueue: %+v %v", second, err)
	}
	e.waitStarted(t)
	if err := e.svc.Retry(ctx, e.alice, false, first[0].ID); !errors.Is(err, ErrAlreadyQueued) {
		t.Fatalf("retry while another job runs: %v, want ErrAlreadyQueued", err)
	}
	if j := e.job(t, first[0].ID); j.Status != StatusFailed {
		t.Fatalf("job 1 requeued while job 2 downloads: %q", j.Status)
	}
	close(e.fake.release)
	e.waitStatus(t, second[0].ID, StatusDone)
	if err := e.svc.Retry(ctx, e.alice, false, first[0].ID); !errors.Is(err, ErrAlreadyQueued) {
		t.Fatalf("retry after job 2 done: %v, want ErrAlreadyQueued", err)
	}
	if j := e.job(t, first[0].ID); j.Status != StatusFailed {
		t.Fatalf("job 1 requeued although job 2's track exists: %q", j.Status)
	}
	if calls := e.fake.calls(); len(calls) != 2 {
		t.Fatalf("downloads run: %v, want 2 (job 1 once, job 2 once)", calls)
	}
}

// Recovery removes an interrupted job's stale .m4a (it was never recorded as
// finished), then the job downloads fresh.
func TestRecoveryRemovesStaleM4A(t *testing.T) {
	e := newEnv(t, true)
	e.fake.entries = []entry{{"s1_0000000x", "Stale", "Chan"}}
	ctx := context.Background()
	r, err := e.svc.DB.Exec(`INSERT INTO downloads(user_id,url,video_id,title,channel,status,created_at,updated_at)
		VALUES (?,?,?,?,?,'downloading',1,1)`, e.alice, watchURL("s1_0000000x"), "s1_0000000x", "Stale", "Chan")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := r.LastInsertId()
	stale := filepath.Join(e.root, "Chan", "Stale [s1_0000000x].m4a")
	os.MkdirAll(filepath.Dir(stale), 0o755)
	if err := os.WriteFile(stale, []byte("half-converted"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.svc.recover(ctx)
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale .m4a survived recovery (%v)", err)
	}
	e.start(t)
	j := e.waitStatus(t, id, StatusDone)
	got, _ := os.ReadFile(stale)
	want, _ := os.ReadFile(e.fake.sample)
	if j.TrackID == nil || string(got) != string(want) {
		t.Fatalf("job not completed fresh: %+v, %d bytes", j, len(got))
	}
}

// YouTube refuses a file with a 403 now and then; a download retries it
// (twice, after a short wait) before it fails.
func TestForbiddenRetriedBeforeFailing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		forbid int
		want   Status
	}{{"two 403s then works", 2, StatusDone}, {"always 403", 3, StatusFailed}} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, true)
			e.svc.forbiddenWaits = []time.Duration{0, 0}
			e.fake.entries = []entry{{"f1_0000000x", "Song", "Chan"}}
			e.fake.forbid["f1_0000000x"] = tc.forbid
			e.start(t)
			jobs, err := e.svc.Enqueue(context.Background(), e.alice, watchURL("f1_0000000x"))
			if err != nil {
				t.Fatal(err)
			}
			e.waitStatus(t, jobs[0].ID, tc.want)
			if j := e.job(t, jobs[0].ID); tc.want == StatusFailed && !strings.Contains(j.Error, "403") {
				t.Fatalf("error %q, want the 403", j.Error)
			}
			if n := len(e.fake.calls()); n != 3 {
				t.Fatalf("attempts %d, want 3", n)
			}
		})
	}
}
