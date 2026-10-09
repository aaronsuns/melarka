package stream

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/aaronsuns/lark-server/internal/library"
	"github.com/aaronsuns/lark-server/internal/testutil"
)

func insertTrack(t *testing.T, lib *library.Store, libID int64, rel, codec string, bitrate int) int64 {
	t.Helper()
	res, err := lib.DB.ExecContext(context.Background(),
		`INSERT INTO tracks(library_id,rel_path,size,mtime,fingerprint,duration_ms,codec,bitrate,status,added_at)
		 VALUES (?,?,1,1,?,200000,?,?,'kept',1)`, libID, rel, "fp-"+rel, codec, bitrate)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

// Review round 1, finding 3: an iOS-native codec (FLAC) muxed into a
// container AVPlayer can't open (.ogg) must not be passed through — it must
// be transcoded like any other unplayable-as-is source. A recognized
// container for the same codec must still pass straight through untouched.
func TestResolvePassthroughRequiresKnownContainer(t *testing.T) {
	d := testutil.DB(t)
	lib := &library.Store{DB: d}
	ctx := context.Background()
	l, err := lib.EnsureLibrary(ctx, "main", t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}

	r := &fakeRunner{}
	svc := &Service{
		Library: lib,
		Cache:   NewCache(filepath.Join(t.TempDir(), "cache"), 1<<30, 1, r),
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	oggID := insertTrack(t, lib, l.ID, "song.ogg", "flac", 900)
	path, ctype, err := svc.Resolve(ctx, oggID, Lossless)
	if err != nil {
		t.Fatal(err)
	}
	if r.calls.Load() != 1 {
		t.Fatalf("ogg-contained flac: ffmpeg ran %d times, want 1 (must transcode, not pass through)", r.calls.Load())
	}
	if ctype != "audio/flac" {
		t.Fatalf("ogg-contained flac: content type = %s, want audio/flac", ctype)
	}
	if filepath.Ext(path) != ".flac" {
		t.Fatalf("ogg-contained flac: output path = %s, want .flac", path)
	}

	flacID := insertTrack(t, lib, l.ID, "song.flac", "flac", 900)
	path, ctype, err = svc.Resolve(ctx, flacID, Lossless)
	if err != nil {
		t.Fatal(err)
	}
	if r.calls.Load() != 1 {
		t.Fatalf("recognized .flac container: ffmpeg ran again (calls=%d), want passthrough", r.calls.Load())
	}
	if ctype != "audio/flac" || filepath.Base(path) != "song.flac" {
		t.Fatalf("recognized .flac container: passthrough broken, path=%s ctype=%s", path, ctype)
	}
}
