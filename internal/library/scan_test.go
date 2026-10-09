package library

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/media"
	"github.com/aaronsuns/lark-server/internal/testutil"
)

type fakeProber struct{ broken map[string]bool }

func (f fakeProber) Probe(_ context.Context, path string) (media.Info, error) {
	if f.broken[filepath.Base(path)] {
		return media.Info{}, errors.New("corrupt")
	}
	return media.Info{DurationMS: 200_000, Codec: "mp3", BitrateKbps: 192}, nil
}

func writeRandom(t *testing.T, path string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	b := make([]byte, 4096)
	rand.Read(b)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

type env struct {
	st   *Store
	sc   *Scanner
	lib  Library
	root string
	now  time.Time
}

func newEnv(t *testing.T, p media.Prober) *env {
	e := &env{root: t.TempDir(), now: time.Unix(1_700_000_000, 0)}
	e.st = &Store{DB: testutil.DB(t), Now: func() time.Time { return e.now }}
	e.sc = &Scanner{Store: e.st, Prober: p, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	lib, err := e.st.EnsureLibrary(context.Background(), "main", e.root, false)
	if err != nil {
		t.Fatal(err)
	}
	e.lib = lib
	return e
}

func (e *env) scan(t *testing.T) ScanResult {
	t.Helper()
	lib, _ := e.st.Library(context.Background(), e.lib.ID) // refresh last_scan_at
	r, err := e.sc.Scan(context.Background(), lib)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (e *env) col(t *testing.T, relPath, col string) any {
	var v any
	if err := e.st.DB.QueryRow(`SELECT `+col+` FROM tracks WHERE rel_path=?`, relPath).Scan(&v); err != nil {
		t.Fatalf("%s %s: %v", relPath, col, err)
	}
	return v
}

func TestInitialImportKeptLaterPending(t *testing.T) {
	e := newEnv(t, fakeProber{})
	writeRandom(t, filepath.Join(e.root, "邓丽君精选", "01 - 甜蜜蜜.mp3"))
	writeRandom(t, filepath.Join(e.root, "notes.txt"))
	writeRandom(t, filepath.Join(e.root, ".lark-trash", "old.mp3"))
	if r := e.scan(t); r.Added != 1 {
		t.Fatalf("added=%+v", r)
	}
	if e.col(t, "邓丽君精选/01 - 甜蜜蜜.mp3", "status") != "kept" {
		t.Fatal("initial import must be kept")
	}

	writeRandom(t, filepath.Join(e.root, "new", "song.flac"))
	r := e.scan(t)
	if r.Added != 1 || r.Unchanged != 1 {
		t.Fatalf("second scan %+v", r)
	}
	if e.col(t, "new/song.flac", "status") != "pending" {
		t.Fatal("later additions must be pending")
	}
}

func TestTitleAndAlbumFallbacks(t *testing.T) {
	e := newEnv(t, fakeProber{})
	writeRandom(t, filepath.Join(e.root, "邓丽君精选", "01 - 甜蜜蜜.mp3"))
	e.scan(t)
	var title, album string
	e.st.DB.QueryRow(`SELECT COALESCE(o.title, NULLIF(t.tag_title,''), ''), a.name FROM tracks t
	    LEFT JOIN track_overrides o ON o.track_id=t.id JOIN albums a ON a.id=t.album_id`).Scan(&title, &album)
	if album != "邓丽君精选" {
		t.Errorf("album=%q", album)
	}
	var n int
	e.st.DB.QueryRow(`SELECT COUNT(*) FROM track_fts WHERE track_fts MATCH ?`, FTSQuery("甜蜜蜜")).Scan(&n)
	if n != 1 {
		t.Error("title fallback from file name not indexed")
	}
}

func TestMovedFileKeepsIdentity(t *testing.T) {
	e := newEnv(t, fakeProber{})
	old := filepath.Join(e.root, "a", "x.mp3")
	writeRandom(t, old)
	e.scan(t)
	id := e.col(t, "a/x.mp3", "id")
	e.st.DB.Exec(`INSERT INTO users(id,username,password_hash,role,created_at) VALUES (1,'u','h','member',0)`)
	e.st.DB.Exec(`INSERT INTO favorites(user_id,track_id,created_at) VALUES (1,?,0)`, id)

	os.MkdirAll(filepath.Join(e.root, "b"), 0o755)
	os.Rename(old, filepath.Join(e.root, "b", "renamed.mp3"))
	r := e.scan(t)
	if r.Moved != 1 || r.Added != 0 {
		t.Fatalf("move %+v", r)
	}
	if e.col(t, "b/renamed.mp3", "id") != id {
		t.Fatal("id changed")
	}
	var favs int
	e.st.DB.QueryRow(`SELECT COUNT(*) FROM favorites`).Scan(&favs)
	if favs != 1 {
		t.Fatal("favorite lost on move")
	}
}

func TestBrokenFileRecordedNotFatal(t *testing.T) {
	e := newEnv(t, fakeProber{broken: map[string]bool{"bad.wma": true}})
	writeRandom(t, filepath.Join(e.root, "bad.wma"))
	writeRandom(t, filepath.Join(e.root, "good.mp3"))
	r := e.scan(t)
	if r.Broken != 1 || r.Added != 2 {
		t.Fatalf("%+v", r)
	}
	if e.col(t, "bad.wma", "broken").(int64) != 1 {
		t.Fatal("not marked broken")
	}
}

func TestMissingThenBack(t *testing.T) {
	e := newEnv(t, fakeProber{})
	a := filepath.Join(e.root, "a.mp3")
	writeRandom(t, a)
	writeRandom(t, filepath.Join(e.root, "b.mp3"))
	e.scan(t)
	data, _ := os.ReadFile(a)
	os.Remove(a)
	if r := e.scan(t); r.Missing != 1 {
		t.Fatalf("%+v", r)
	}
	if e.col(t, "a.mp3", "missing_since") == nil {
		t.Fatal("missing_since not set")
	}
	os.WriteFile(a, data, 0o644)
	e.scan(t)
	if e.col(t, "a.mp3", "missing_since") != nil {
		t.Fatal("missing_since not cleared")
	}
}

// An unmounted USB disk looks like an empty directory.
func TestEmptyOrAbsentRootAbortsWithoutTouchingRows(t *testing.T) {
	e := newEnv(t, fakeProber{})
	writeRandom(t, filepath.Join(e.root, "a.mp3"))
	e.scan(t)
	os.Remove(filepath.Join(e.root, "a.mp3")) // root now empty
	lib, _ := e.st.Library(context.Background(), e.lib.ID)
	if _, err := e.sc.Scan(context.Background(), lib); !errors.Is(err, ErrLibraryUnavailable) {
		t.Fatalf("err=%v", err)
	}
	if e.col(t, "a.mp3", "missing_since") != nil {
		t.Fatal("rows touched on unavailable library")
	}
	os.RemoveAll(e.root)
	if _, err := e.sc.Scan(context.Background(), lib); !errors.Is(err, ErrLibraryUnavailable) {
		t.Fatalf("absent root err=%v", err)
	}
}

func TestMissingPurgedAfter30Days(t *testing.T) {
	e := newEnv(t, fakeProber{})
	writeRandom(t, filepath.Join(e.root, "a.mp3"))
	writeRandom(t, filepath.Join(e.root, "b.mp3"))
	e.scan(t)
	os.Remove(filepath.Join(e.root, "a.mp3"))
	e.scan(t)
	e.now = e.now.Add(31 * 24 * time.Hour)
	e.scan(t)
	var n int
	e.st.DB.QueryRow(`SELECT COUNT(*) FROM tracks`).Scan(&n)
	if n != 1 {
		t.Fatalf("tracks=%d", n)
	}
}

// A trashed row must never be swept
// by the missing-file purge. trash.Service.Purge (30-day retention) owns
// deleting trashed rows and their files under .lark-trash; if the scanner's
// missing-purge deleted the row instead, the file sitting in .lark-trash
// would be orphaned forever — no row left to point at it or restore it.
func TestMissingPurgeNeverDeletesTrashedRows(t *testing.T) {
	e := newEnv(t, fakeProber{})
	writeRandom(t, filepath.Join(e.root, "a.mp3"))
	e.scan(t)
	// A row that went missing long ago and was then trashed without its
	// stale missing_since being cleared (the exact bug fixed in trash.Move).
	old := e.now.Add(-31 * 24 * time.Hour).Unix()
	if _, err := e.st.DB.Exec(`UPDATE tracks SET status='trashed', trashed_at=?, missing_since=? WHERE rel_path='a.mp3'`, old, old); err != nil {
		t.Fatal(err)
	}
	e.scan(t)
	var n int
	e.st.DB.QueryRow(`SELECT COUNT(*) FROM tracks WHERE status='trashed'`).Scan(&n)
	if n != 1 {
		t.Fatal("missing-purge deleted a trashed row; only trash.Purge may do that")
	}
}

// A new file landing on a path a trashed row still occupies
// (trashed rows are excluded from `known`, but rel_path is UNIQUE) must not
// abort the whole scan.
func TestTrashedRowPathCollisionSkippedNotFatal(t *testing.T) {
	e := newEnv(t, fakeProber{})
	writeRandom(t, filepath.Join(e.root, "a.mp3"))
	writeRandom(t, filepath.Join(e.root, "b.mp3"))
	e.scan(t)
	if _, err := e.st.DB.Exec(`UPDATE tracks SET status='trashed' WHERE rel_path='a.mp3'`); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(e.root, "a.mp3"))
	writeRandom(t, filepath.Join(e.root, "a.mp3")) // a new, distinct file reoccupies the trashed row's path

	r := e.scan(t) // must not error
	if r.Unchanged != 1 {
		t.Fatalf("b.mp3 should still be processed: %+v", r)
	}
	if r.Added != 0 {
		t.Fatalf("the colliding file must be skipped, not added: %+v", r)
	}
	lib, err := e.st.Library(context.Background(), e.lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if lib.LastScanAt == nil {
		t.Fatal("last_scan_at not set after a scan with a skipped collision")
	}
}

// Two content-identical files must not share one slot in the
// move-candidate map — otherwise renaming both loses one track's identity.
func TestDuplicateFingerprintMovesBothPreserveIdentity(t *testing.T) {
	e := newEnv(t, fakeProber{})
	pa := filepath.Join(e.root, "A", "song.mp3")
	pc := filepath.Join(e.root, "C", "song.mp3")
	writeRandom(t, pa)
	data, err := os.ReadFile(pa)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(pc), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pc, data, 0o644); err != nil { // byte-identical content
		t.Fatal(err)
	}
	e.scan(t)
	idA := e.col(t, "A/song.mp3", "id")
	idC := e.col(t, "C/song.mp3", "id")
	if idA == idC {
		t.Fatal("setup: expected two distinct rows")
	}

	if err := os.MkdirAll(filepath.Join(e.root, "B"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(e.root, "D"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(pa, filepath.Join(e.root, "B", "song.mp3")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(pc, filepath.Join(e.root, "D", "song.mp3")); err != nil {
		t.Fatal(err)
	}
	r := e.scan(t)
	if r.Moved != 2 || r.Added != 0 || r.Missing != 0 {
		t.Fatalf("duplicate-fingerprint move: %+v", r)
	}
	idB := e.col(t, "B/song.mp3", "id")
	idD := e.col(t, "D/song.mp3", "id")
	seen := map[any]bool{idB: true, idD: true}
	if idB == idD || !seen[idA] || !seen[idC] {
		t.Fatalf("identities not preserved across the double move: A=%v C=%v -> B=%v D=%v", idA, idC, idB, idD)
	}
}

// cancelingProber cancels the scan's context from inside Probe, simulating a
// context cancellation racing a slow USB read mid-probe.
type cancelingProber struct{ cancel context.CancelFunc }

func (p cancelingProber) Probe(_ context.Context, _ string) (media.Info, error) {
	p.cancel()
	return media.Info{DurationMS: 200_000, Codec: "mp3", BitrateKbps: 192}, nil
}

// A probe that never gets to write its result (context
// cancelled) must leave the row retryable, not silently "Unchanged" forever.
func TestCancelledProbeIsRetriedNextScan(t *testing.T) {
	e := newEnv(t, fakeProber{})
	writeRandom(t, filepath.Join(e.root, "a.mp3"))
	ctx, cancel := context.WithCancel(context.Background())
	e.sc.Prober = cancelingProber{cancel: cancel}
	lib, err := e.st.Library(context.Background(), e.lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.sc.Scan(ctx, lib); err == nil {
		t.Fatal("expected the cancelled context to surface as a scan error")
	}
	if e.col(t, "a.mp3", "mtime").(int64) != 0 {
		t.Fatal("mtime sentinel not left at 0 after a cancelled probe")
	}

	e.sc.Prober = fakeProber{} // clean, uncancelled retry
	e.scan(t)
	if e.col(t, "a.mp3", "album_id") == nil {
		t.Fatal("album_id not set after the retried probe")
	}
	id := e.col(t, "a.mp3", "id")
	var n int
	e.st.DB.QueryRow(`SELECT COUNT(*) FROM track_fts WHERE rowid=?`, id).Scan(&n)
	if n != 1 {
		t.Fatal("FTS row missing after the retried probe")
	}
}

// retryProber returns a good result, then a failure, then a good result
// again on successive calls — a transient disk glitch followed by recovery.
type retryProber struct{ n int }

func (p *retryProber) Probe(_ context.Context, _ string) (media.Info, error) {
	p.n++
	if p.n == 2 {
		return media.Info{}, errors.New("disk glitch")
	}
	return media.Info{DurationMS: 200_000, Codec: "mp3", BitrateKbps: 192, Title: "Sweet"}, nil
}

// A temporary probe failure on an already-good row must keep
// its old tags (not overwrite them with empty strings) and must be retried
// on the next scan rather than marked broken forever.
func TestBrokenRowRetriedAndTagsPreservedOnFailedReprobe(t *testing.T) {
	pr := &retryProber{}
	e := newEnv(t, pr)
	p := filepath.Join(e.root, "a.mp3")
	writeRandom(t, p)
	e.scan(t) // probe #1 succeeds
	if e.col(t, "a.mp3", "tag_title") != "Sweet" {
		t.Fatal("setup: expected tag_title to be set by the first probe")
	}

	// Touch the file so the scanner re-probes it; call #2 fails.
	future := e.now.Add(time.Minute)
	if err := os.Chtimes(p, future, future); err != nil {
		t.Fatal(err)
	}
	r := e.scan(t)
	if r.Broken != 1 {
		t.Fatalf("expected the re-probe failure to be recorded: %+v", r)
	}
	if e.col(t, "a.mp3", "tag_title") != "Sweet" {
		t.Fatal("tag_title lost on a failed re-probe")
	}
	if e.col(t, "a.mp3", "broken").(int64) != 1 {
		t.Fatal("not marked broken")
	}
	if e.col(t, "a.mp3", "mtime").(int64) != 0 {
		t.Fatal("mtime sentinel not reset after a broken re-probe")
	}

	// The mtime sentinel forces a retry even though the file itself didn't
	// change again; call #3 succeeds and clears broken.
	r = e.scan(t)
	if r.Broken != 0 {
		t.Fatalf("expected broken to clear on a successful retry: %+v", r)
	}
	if e.col(t, "a.mp3", "broken").(int64) != 0 {
		t.Fatal("broken flag not cleared")
	}
}

// Dotfiles (macOS AppleDouble "._song.mp3" sidecars from SMB
// copies) must never be scanned, let alone probed and recorded as broken.
func TestDotFilesSkipped(t *testing.T) {
	e := newEnv(t, fakeProber{})
	writeRandom(t, filepath.Join(e.root, "song.mp3"))
	writeRandom(t, filepath.Join(e.root, "._song.mp3"))
	r := e.scan(t)
	if r.Added != 1 {
		t.Fatalf("dotfile must not be scanned: %+v", r)
	}
	var n int
	e.st.DB.QueryRow(`SELECT COUNT(*) FROM tracks WHERE rel_path='._song.mp3'`).Scan(&n)
	if n != 0 {
		t.Fatal("AppleDouble sidecar file became a track")
	}
}

// trashingProber simulates a concurrent trash.Move landing on relPath's row
// between the scanner's DB snapshot and its per-row write: the first time
// it's asked to probe that file, it flips the row to 'trashed' directly (as
// trash.Move would, mid-scan) before returning a normal probe result.
type trashingProber struct {
	db      *sql.DB
	relPath string
	trashed bool
}

func (p *trashingProber) Probe(ctx context.Context, path string) (media.Info, error) {
	if !p.trashed && filepath.Base(path) == filepath.Base(p.relPath) {
		p.trashed = true
		if _, err := p.db.ExecContext(ctx, `UPDATE tracks SET status='trashed' WHERE rel_path=?`, p.relPath); err != nil {
			return media.Info{}, err
		}
	}
	return media.Info{DurationMS: 200_000, Codec: "mp3", BitrateKbps: 192, Title: "clobbered"}, nil
}

// A row trashed by a concurrent trash.Move between the
// scanner's snapshot and its write for that row must be left exactly as the
// trash operation left it -- not have its trash status/columns overwritten
// with fresh scan data for a file that, as far as the row is now concerned,
// no longer lives at that path.
func TestRowTrashedMidScanIsLeftUntouched(t *testing.T) {
	e := newEnv(t, fakeProber{})
	p := filepath.Join(e.root, "a.mp3")
	writeRandom(t, p)
	e.scan(t) // initial import: fakeProber leaves tag_title empty

	// Touch the file so the next scan re-probes it (mtime changed).
	future := e.now.Add(time.Minute)
	if err := os.Chtimes(p, future, future); err != nil {
		t.Fatal(err)
	}

	e.sc.Prober = &trashingProber{db: e.st.DB, relPath: "a.mp3"}
	r := e.scan(t)
	if r.Broken != 0 {
		t.Fatalf("unexpected broken count: %+v", r)
	}

	var status, title string
	var mtime int64
	if err := e.st.DB.QueryRow(`SELECT status, tag_title, mtime FROM tracks WHERE rel_path='a.mp3'`).Scan(&status, &title, &mtime); err != nil {
		t.Fatal(err)
	}
	if status != "trashed" {
		t.Fatalf("status=%q, row must stay trashed", status)
	}
	if title == "clobbered" {
		t.Fatal("scan overwrote tags on a row that was trashed mid-scan")
	}
}

// yt-dlp intermediates (a ".temp.m4a" written while converting or embedding
// the thumbnail, and ".part"/".ytdl" partials) must never become tracks, even
// when a watcher-triggered scan runs mid-download and the name has an audio
// extension.
func TestSkipsYtdlpIntermediates(t *testing.T) {
	e := newEnv(t, fakeProber{})
	writeRandom(t, filepath.Join(e.root, "Chan", "Real [x0].m4a"))
	writeRandom(t, filepath.Join(e.root, "Chan", "Song [x1].temp.m4a"))
	writeRandom(t, filepath.Join(e.root, "Chan", "Song [x2].m4a.part"))
	writeRandom(t, filepath.Join(e.root, "Chan", "Song [x3].m4a.ytdl"))
	if r := e.scan(t); r.Added != 1 || r.Broken != 0 {
		t.Fatalf("scan %+v", r)
	}
	var n int
	e.st.DB.QueryRow(`SELECT COUNT(*) FROM tracks WHERE rel_path='Chan/Real [x0].m4a'`).Scan(&n)
	if n != 1 {
		t.Fatal("real track not ingested")
	}
}
