package channels

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// seedQueued stores an episode of channel ch published `ago` before now with a queued file of kind.
func (e *env) seedQueued(ch string, n int, ago time.Duration, kinds ...string) {
	e.t.Helper()
	now := e.clock.Now().Unix()
	e.exec(`INSERT OR IGNORE INTO channels(id,title,created_at) VALUES (?,?,?)`, ch, "C", now)
	e.exec(`INSERT OR IGNORE INTO episodes(video_id,channel_id,title,published_at,kind,seen_at) VALUES (?,?,?,?,'video',?)`,
		vid(n), ch, "Episode", e.clock.Now().Add(-ago).Unix(), now)
	for _, k := range kinds {
		e.exec(`INSERT INTO episode_files(video_id,kind,status,created_at,updated_at) VALUES (?,?,'queued',?,?)`, vid(n), k, now, now)
	}
}

func TestWorkerDownloadsNewestFirst(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.seedQueued(chA, 1, 3*time.Hour, "audio")
	e.seedQueued(chA, 2, time.Hour, "audio", "video")
	for range 3 {
		if w := e.svc.workStep(ctx); w != 0 {
			t.Fatalf("work was waiting: %s", w)
		}
	}
	if got := e.yt.downloads(); !slices.Equal(got, []string{vid(2) + "/audio", vid(2) + "/video", vid(1) + "/audio"}) {
		t.Fatalf("order %v", got)
	}
	var path string
	var bytes int64
	e.db.QueryRow(`SELECT path, bytes FROM episode_files WHERE video_id=? AND kind='video' AND status='done'`, vid(2)).Scan(&path, &bytes)
	if path != chA+"/"+vid(2)+".v.mp4" || bytes != 1000 || !exists(e.path(chA, vid(2)+".jpg")) {
		t.Fatalf("path %q bytes %d", path, bytes)
	}
	if w := e.svc.workStep(ctx); w <= 0 {
		t.Fatal("an empty queue waits")
	}
}

func TestWorkerWaitsForMusicAndPreviews(t *testing.T) {
	e := newEnv(t)
	busy := true
	e.svc.Busy = func(context.Context) bool { return busy }
	e.seedQueued(chA, 1, time.Hour, "audio")
	if w := e.svc.workStep(context.Background()); w != busyWait || len(e.yt.downloads()) != 0 {
		t.Fatalf("%s %v", w, e.yt.downloads())
	}
	busy = false
	e.svc.workStep(context.Background())
	if len(e.yt.downloads()) != 1 {
		t.Fatal("runs once the music queue is idle")
	}
}

// Review focus 1 + fix round R12: YouTube pushing back requeues without
// spending an attempt and cools the whole worker down; a private video fails
// for good at once; a genuine per-video failure retries with backoff, three
// attempts at most.
func TestWorkerRetryAndPermanentFailure(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.seedQueued(chA, 1, time.Hour, "audio")
	e.seedQueued(chA, 2, 2*time.Hour, "audio")
	e.seedQueued(chA, 3, 3*time.Hour, "audio")
	os.MkdirAll(e.path(chA, ""), 0o755)
	e.yt.dlErr[vid(1)] = errors.New("yt-dlp: exit status 1: ERROR: Sign in to confirm you're not a bot")
	e.yt.dlErr[vid(2)] = errors.New("yt-dlp: exit status 1: ERROR: [youtube] x: Private video")
	e.yt.dlErr[vid(3)] = errors.New("yt-dlp: exit status 1: ERROR: [youtube] x: Requested format is not available")
	if w := e.svc.workStep(ctx); w != pushBackBase {
		t.Fatalf("a bot check cools the worker down: %s", w)
	}
	var status string
	var attempts, next int64
	e.db.QueryRow(`SELECT status, attempts, next_attempt_at FROM episode_files WHERE video_id=?`, vid(1)).Scan(&status, &attempts, &next)
	if status != "queued" || attempts != 0 || next != 0 || exists(e.path(chA, vid(1)+".m4a.part")) {
		t.Fatalf("push-back: %s %d %d", status, attempts, next)
	}
	if w := e.svc.workStep(ctx); w <= 0 || w > pushBackBase || len(e.yt.downloads()) != 1 {
		t.Fatalf("nothing is claimed while cooling down: %s %v", w, e.yt.downloads())
	}
	e.clock.Add(pushBackBase)
	delete(e.yt.dlErr, vid(1))
	e.svc.workStep(ctx)
	if e.fileStatus(vid(1), "audio") != "done" {
		t.Fatal("after the cooldown it downloads")
	}
	e.svc.workStep(ctx)
	if e.fileStatus(vid(2), "audio") != "failed" || e.count(`SELECT COUNT(*) FROM episodes WHERE video_id=? AND kind='unavailable'`, vid(2)) != 1 {
		t.Fatal("a private video fails for good")
	}
	start := e.clock.Now()
	e.svc.workStep(ctx)
	e.db.QueryRow(`SELECT status, attempts, next_attempt_at FROM episode_files WHERE video_id=?`, vid(3)).Scan(&status, &attempts, &next)
	if status != "queued" || attempts != 1 || next != start.Add(retryBase).Unix() {
		t.Fatalf("per-video failure: %s %d %d", status, attempts, next)
	}
	if w := e.svc.workStep(ctx); w <= 0 || len(e.yt.downloads()) != 4 {
		t.Fatalf("not due before its backoff: %s", w)
	}
	e.clock.Add(30 * time.Minute)
	e.svc.workStep(ctx) // attempt 2 → next in 60 min
	e.clock.Add(60 * time.Minute)
	e.svc.workStep(ctx) // attempt 3 → failed
	if e.fileStatus(vid(3), "audio") != "failed" {
		t.Fatal("three attempts at most")
	}
}

// Fix round R12: a storm of push-back (bot checks, timeouts, network errors)
// never fails a file; the cooldown doubles up to its cap and a success resets it.
func TestWorkerPushBackStormFailsNothing(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	errs := []error{
		errors.New("yt-dlp: exit status 1: ERROR: Sign in to confirm you're not a bot"),
		fmt.Errorf("channels: download timed out: %w", context.DeadlineExceeded),
		errors.New("yt-dlp: exit status 1: ERROR: Unable to download webpage: <urlopen error [Errno -3] Temporary failure in name resolution>"),
		errors.New("yt-dlp: exit status 1: ERROR: unable to download video data: HTTP Error 503: Service Unavailable"),
	}
	for i := 1; i <= 3; i++ {
		e.seedQueued(chA, i, time.Duration(i)*time.Hour, "audio")
	}
	want := pushBackBase
	for i := range 12 {
		for n := 1; n <= 3; n++ {
			e.yt.dlErr[vid(n)] = errs[i%len(errs)]
		}
		w := e.svc.workStep(ctx)
		if w != want {
			t.Fatalf("storm %d: cooldown %s, want %s", i, w, want)
		}
		e.clock.Add(w)
		want = min(want*2, pushBackMax)
	}
	if e.count(`SELECT COUNT(*) FROM episode_files WHERE status='queued' AND attempts=0`) != 3 {
		t.Fatal("no file failed or spent an attempt")
	}
	e.yt.dlErr = map[string]error{}
	if w := e.svc.workStep(ctx); w != 0 {
		t.Fatalf("a success: %s", w)
	}
	e.yt.dlErr[vid(2)] = errs[0]
	if w := e.svc.workStep(ctx); w != pushBackBase {
		t.Fatalf("the cooldown starts over after a success: %s", w)
	}
}

// Fix round R12 minor: a download yt-dlp reports outside Root leaves no files behind.
func TestWorkerNotIngestedRemovesPartials(t *testing.T) {
	e := newEnv(t)
	e.seedQueued(chA, 1, time.Hour, "audio")
	e.svc.YT = outsideYT{e.yt, t.TempDir()}
	e.svc.workStep(context.Background())
	if exists(e.path(chA, vid(1)+".m4a.part")) {
		t.Fatal("partials removed")
	}
	var attempts int
	e.db.QueryRow(`SELECT attempts FROM episode_files WHERE video_id=?`, vid(1)).Scan(&attempts)
	if attempts != 1 || e.fileStatus(vid(1), "audio") != "queued" {
		t.Fatal("counted as a failed attempt")
	}
}

// outsideYT downloads like fakeYT, leaves a partial, and reports a path outside Root.
type outsideYT struct {
	*fakeYT
	other string
}

func (o outsideYT) DownloadEpisode(ctx context.Context, v ytdlp.Video, destNoExt string, kind ytdlp.MediaKind, onProgress func(float64)) (string, error) {
	os.WriteFile(destNoExt+".m4a.part", []byte("partial"), 0o644)
	return filepath.Join(o.other, "x.m4a"), nil
}

// Review focus 2: no new downloads without disk room.
func TestWorkerNeedsRoom(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.seedQueued(chA, 1, time.Hour, "audio")
	e.svc.MinFree = 2 << 30
	e.svc.FreeBytes = func(string) (int64, error) { return 1 << 30, nil }
	if w := e.svc.workStep(ctx); w != roomWait || len(e.yt.downloads()) != 0 {
		t.Fatalf("low disk: %s", w)
	}
	e.svc.FreeBytes = func(string) (int64, error) { return 1 << 40, nil }
	e.svc.MaxBytes = 500
	e.seedQueued(chA, 2, 2*time.Hour)
	e.exec(`INSERT INTO episode_files(video_id,kind,status,bytes,created_at,updated_at) VALUES (?,'audio','done',1000,1,1)`, vid(2))
	if w := e.svc.workStep(ctx); w != roomWait {
		t.Fatalf("over the cap: %s", w)
	}
	e.svc.MaxBytes = 0
	e.svc.workStep(ctx)
	if len(e.yt.downloads()) != 1 {
		t.Fatal("room again: downloads")
	}
}

func TestRecoverRequeuesInterrupted(t *testing.T) {
	e := newEnv(t)
	e.seedQueued(chA, 1, time.Hour, "audio", "video")
	e.exec(`UPDATE episode_files SET status='downloading'`)
	os.MkdirAll(e.path(chA, ""), 0o755)
	for _, n := range []string{vid(1) + ".m4a.part", vid(1) + ".v.f136.mp4", vid(1) + ".jpg", vid(2) + ".m4a"} {
		os.WriteFile(e.path(chA, n), []byte("x"), 0o644)
	}
	e.svc.recoverFiles(context.Background())
	if e.count(`SELECT COUNT(*) FROM episode_files WHERE status='queued'`) != 2 {
		t.Fatal("both requeued")
	}
	if exists(e.path(chA, vid(1)+".m4a.part")) || exists(e.path(chA, vid(1)+".v.f136.mp4")) {
		t.Fatal("partials removed")
	}
	if !exists(e.path(chA, vid(1)+".jpg")) || !exists(e.path(chA, vid(2)+".m4a")) {
		t.Fatal("the thumbnail and other episodes' files stay")
	}
}

// Final review: a download that runs into the worker's own deadline is not
// push-back. The first time it waits for its own backoff while older
// episodes go ahead (no global cooldown); the second time it fails as too long.
func TestWorkerOwnTimeoutBacksOffThenFailsTooLong(t *testing.T) {
	e := newEnv(t)
	e.svc.jobTimeout = 20 * time.Millisecond
	ctx := context.Background()
	e.seedQueued(chA, 1, time.Hour, "audio")   // the newest: too long to fetch in time
	e.seedQueued(chA, 2, 2*time.Hour, "audio") // older
	e.yt.dlHang[vid(1)] = true
	if w := e.svc.workStep(ctx); w != 0 {
		t.Fatalf("own timeout: wait %s, want 0 (no cooldown)", w)
	}
	if e.svc.coolingFor() > 0 {
		t.Fatal("an own timeout starts no global cooldown")
	}
	if e.fileStatus(vid(1), "audio") != "queued" || e.count(`SELECT next_attempt_at FROM episode_files WHERE video_id=?`, vid(1)) <= int(t0.Unix()) {
		t.Fatal("the first own timeout requeues the file with its own backoff")
	}
	e.svc.workStep(ctx)
	if e.fileStatus(vid(2), "audio") != "done" {
		t.Fatalf("the older episode is downloaded meanwhile: %s", e.fileStatus(vid(2), "audio"))
	}
	e.clock.Add(7 * time.Hour)
	e.svc.workStep(ctx)
	var status, msg string
	e.db.QueryRow(`SELECT status, error FROM episode_files WHERE video_id=? AND kind='audio'`, vid(1)).Scan(&status, &msg)
	if status != "failed" || msg != "lark:too_long" {
		t.Fatalf("second own timeout: %s %q, want failed lark:too_long", status, msg)
	}
	if n := len(e.yt.downloads()); n != 3 {
		t.Fatalf("%d download runs, want 3 (the long file stops cycling)", n)
	}
	if e.svc.workStep(ctx) == 0 {
		t.Fatal("nothing left to do")
	}
}

// YouTube's transient 403 on a download is that file's alone: it waits for
// its own backoff (15 min doubling, ≤ 6 h) without spending an attempt or
// cooling the worker down; after maxTransients of them it fails as
// lark:forbidden.
func TestWorkerForbiddenBacksOffPerFileThenFails(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.seedQueued(chA, 1, time.Hour, "audio")   // the newest: YouTube answers 403
	e.seedQueued(chA, 2, 2*time.Hour, "audio") // older
	e.yt.dlErr[vid(1)] = errors.New("yt-dlp: exit status 1: ERROR: unable to download video data: HTTP Error 403: Forbidden")
	if w := e.svc.workStep(ctx); w != 0 {
		t.Fatalf("403: wait %s, want 0 (no cooldown)", w)
	}
	if e.svc.coolingFor() > 0 {
		t.Fatal("a 403 starts no global cooldown")
	}
	e.svc.workStep(ctx)
	if e.fileStatus(vid(2), "audio") != "done" {
		t.Fatalf("the older episode is downloaded meanwhile: %s", e.fileStatus(vid(2), "audio"))
	}
	backoff := 15 * time.Minute
	for i := 1; i <= 6; i++ {
		var status string
		var attempts, transients int
		var next int64
		e.db.QueryRow(`SELECT status, attempts, transients, next_attempt_at FROM episode_files WHERE video_id=?`, vid(1)).Scan(&status, &attempts, &transients, &next)
		if status != "queued" || attempts != 0 || transients != i || next != e.clock.Now().Add(backoff).Unix() {
			t.Fatalf("403 #%d: %s attempts=%d transients=%d next=+%ds, want queued 0 %d +%s",
				i, status, attempts, transients, next-e.clock.Now().Unix(), i, backoff)
		}
		e.clock.Add(backoff - time.Second)
		e.svc.workStep(ctx)
		if n := len(e.yt.downloads()); n != i+1 {
			t.Fatalf("403 #%d: claimed before its backoff ended", i)
		}
		e.clock.Add(time.Second)
		e.svc.workStep(ctx)
		backoff = min(backoff*2, 6*time.Hour)
	}
	var status, msg string
	var attempts int
	e.db.QueryRow(`SELECT status, error, attempts FROM episode_files WHERE video_id=?`, vid(1)).Scan(&status, &msg, &attempts)
	if status != "failed" || msg != "lark:forbidden" || attempts != 0 {
		t.Fatalf("seventh 403: %s %q attempts=%d, want failed lark:forbidden 0", status, msg, attempts)
	}
	if e.count(`SELECT COUNT(*) FROM episodes WHERE kind='unavailable'`) != 0 {
		t.Fatal("a 403 never marks the video unavailable")
	}
}

// A 403 next to a permanent error (private, removed) is the permanent
// error: the video is unavailable, not throttled.
func TestWorkerForbiddenNeverMasksUnavailable(t *testing.T) {
	for _, msg := range []string{
		"yt-dlp: exit status 1: ERROR: [youtube] x: Private video. Sign in if you've been granted access\nERROR: unable to download video data: HTTP Error 403: Forbidden",
		"yt-dlp: exit status 1: ERROR: unable to download video data: HTTP Error 403: Forbidden\nERROR: [youtube] x: Video unavailable",
	} {
		e := newEnv(t)
		e.seedQueued(chA, 1, time.Hour, "audio")
		e.yt.dlErr[vid(1)] = errors.New(msg)
		e.svc.workStep(context.Background())
		if e.fileStatus(vid(1), "audio") != "failed" || e.count(`SELECT COUNT(*) FROM episodes WHERE kind='unavailable'`) != 1 ||
			e.count(`SELECT transients FROM episode_files WHERE video_id=?`, vid(1)) != 0 {
			t.Fatalf("%q: %s, want failed and the video unavailable", msg, e.fileStatus(vid(1), "audio"))
		}
	}
}
