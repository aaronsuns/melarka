package trash

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/library"
	"github.com/aaronsuns/lark-server/internal/testutil"
)

func setup(t *testing.T) (*Service, string, int64, *time.Time) {
	d := testutil.DB(t)
	now := time.Unix(1_700_000_000, 0)
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "A"), 0o755)
	os.WriteFile(filepath.Join(root, "A", "x.mp3"), []byte("data"), 0o644)
	lib := &library.Store{DB: d}
	l, _ := lib.EnsureLibrary(context.Background(), "main", root, false)
	d.Exec(`INSERT INTO tracks(id,library_id,rel_path,size,mtime,fingerprint,status,added_at) VALUES (1,?,'A/x.mp3',4,1,'f','kept',0)`, l.ID)
	lib.Reindex(context.Background(), 1)
	s := &Service{DB: d, Library: lib, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return now }}
	return s, root, 1, &now
}

func TestMoveRestorePurge(t *testing.T) {
	s, root, id, now := setup(t)
	ctx := context.Background()
	if err := s.Move(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".lark-trash", "A", "x.mp3")); err != nil {
		t.Fatal("not in trash")
	}
	if err := s.Move(ctx, id); err != ErrNotFound {
		t.Fatalf("double move err=%v", err)
	}
	items, _ := s.List(ctx)
	if len(items) != 1 || items[0].PurgeAt != now.Unix()+int64(Retention/time.Second) {
		t.Fatalf("list %+v", items)
	}
	// List() reports the original path, not the trash path.
	if items[0].Path != "A/x.mp3" {
		t.Fatalf("path=%s", items[0].Path)
	}

	// While trashed, rel_path holds the trash location and
	// trash_orig_rel holds the original — verify both.
	var relPath string
	var origRel sql.NullString
	s.DB.QueryRow(`SELECT rel_path, trash_orig_rel FROM tracks WHERE id=1`).Scan(&relPath, &origRel)
	if relPath != ".lark-trash/A/x.mp3" || !origRel.Valid || origRel.String != "A/x.mp3" {
		t.Fatalf("rel_path=%s trash_orig_rel=%v", relPath, origRel)
	}

	if err := s.Restore(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "A", "x.mp3")); err != nil {
		t.Fatal("not restored")
	}
	var status string
	s.DB.QueryRow(`SELECT status FROM tracks WHERE id=1`).Scan(&status)
	if status != "kept" {
		t.Fatalf("status=%s", status)
	}
	// After restore, rel_path is back to the original and trash_orig_rel is cleared.
	s.DB.QueryRow(`SELECT rel_path, trash_orig_rel FROM tracks WHERE id=1`).Scan(&relPath, &origRel)
	if relPath != "A/x.mp3" || origRel.Valid {
		t.Fatalf("after restore rel_path=%s trash_orig_rel=%v", relPath, origRel)
	}

	s.Move(ctx, id)
	if n, _ := s.Purge(ctx, Retention); n != 0 {
		t.Fatal("purged too early")
	}
	*now = now.Add(Retention + time.Hour)
	if n, err := s.Purge(ctx, Retention); n != 1 || err != nil {
		t.Fatalf("purge n=%d err=%v", n, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".lark-trash", "A", "x.mp3")); !os.IsNotExist(err) {
		t.Fatal("file survived purge")
	}
	var rows int
	s.DB.QueryRow(`SELECT COUNT(*) FROM tracks`).Scan(&rows)
	if rows != 0 {
		t.Fatal("row survived purge")
	}
}

func TestRestoreConflict(t *testing.T) {
	s, root, id, _ := setup(t)
	s.Move(context.Background(), id)
	os.WriteFile(filepath.Join(root, "A", "x.mp3"), []byte("new file at same path"), 0o644)
	if err := s.Restore(context.Background(), id); err != ErrConflict {
		t.Fatalf("err=%v", err)
	}
}

// Re-downloading the same song while the original is trashed must
// not collide with UNIQUE(library_id, rel_path) — the trashed row's rel_path
// has moved into .lark-trash, freeing the original path for a new row. Once
// that new row exists, Restore must refuse (ErrConflict) rather than silently
// overwrite or orphan the re-added track.
func TestReaddAtOriginalPathWhileTrashed(t *testing.T) {
	s, _, id, _ := setup(t)
	ctx := context.Background()
	if err := s.Move(ctx, id); err != nil {
		t.Fatal(err)
	}
	var libID int64
	if err := s.DB.QueryRow(`SELECT library_id FROM tracks WHERE id=?`, id).Scan(&libID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO tracks(id,library_id,rel_path,size,mtime,fingerprint,status,added_at) VALUES (2,?,'A/x.mp3',4,1,'f2','kept',0)`, libID); err != nil {
		t.Fatalf("insert at original path collided: %v", err)
	}
	if err := s.Restore(ctx, id); err != ErrConflict {
		t.Fatalf("err=%v", err)
	}
}

// If the DB UPDATE after the file link fails (e.g. a
// row raced us to the same rel_path between our conflict check and the
// UPDATE), Restore must undo the link and leave the trashed row and its file
// exactly as they were. Getting this wrong loses history two ways: the file
// would sit at the original path while the row still reads trashed, so the
// next scan inserts a brand-new pending row (the old row's favorites/plays
// orphaned), a retried Restore gets ErrConflict against that new row, and a
// later trash.Purge deletes the original row out from under the file that's
// actually sitting at the live path.
func TestRestoreRollsBackOnUpdateFailure(t *testing.T) {
	s, root, id, _ := setup(t)
	ctx := context.Background()
	if err := s.Move(ctx, id); err != nil {
		t.Fatal(err)
	}
	var trashPath string
	var libID int64
	if err := s.DB.QueryRow(`SELECT trash_path, library_id FROM tracks WHERE id=?`, id).Scan(&trashPath, &libID); err != nil {
		t.Fatal(err)
	}

	// Simulate the race from inside the Link hook: by the time the real link
	// has been made, another row has claimed the original rel_path, so the
	// UPDATE that would repoint row `id` back onto that path must fail.
	s.Link = func(o, n string) error {
		if err := os.Link(o, n); err != nil {
			return err
		}
		if _, err := s.DB.Exec(`INSERT INTO tracks(id,library_id,rel_path,size,mtime,fingerprint,status,added_at) VALUES (99,?,'A/x.mp3',4,1,'race','kept',0)`, libID); err != nil {
			t.Fatal(err)
		}
		return nil
	}

	if err := s.Restore(ctx, id); err == nil {
		t.Fatal("expected the UPDATE's UNIQUE(library_id, rel_path) violation to surface")
	}

	if _, err := os.Stat(filepath.Join(root, "A", "x.mp3")); !os.IsNotExist(err) {
		t.Fatal("dest link not cleaned up after a failed restore")
	}
	if _, err := os.Stat(trashPath); err != nil {
		t.Fatal("file lost from trash after a failed restore")
	}
	var status string
	s.DB.QueryRow(`SELECT status FROM tracks WHERE id=?`, id).Scan(&status)
	if status != "trashed" {
		t.Fatalf("status=%s, row must stay trashed and unchanged", status)
	}
}

// os.Rename silently replaces an existing file, so
// trashing the same original path twice (e.g. delete, re-download, delete
// again) within the same second — the resolution the collision suffix used
// to use — must not let the second Move clobber the first trashed copy.
// Repeats it a third time so the incrementing-counter branch (the
// timestamp-suffixed path itself already taken) is exercised too.
func TestMoveCollisionKeepsEveryTrashedCopy(t *testing.T) {
	s, root, id, _ := setup(t)
	ctx := context.Background()
	var libID int64
	if err := s.DB.QueryRow(`SELECT library_id FROM tracks WHERE id=?`, id).Scan(&libID); err != nil {
		t.Fatal(err)
	}
	if err := s.Move(ctx, id); err != nil {
		t.Fatal(err)
	}
	for i, tid := range []int64{2, 3} {
		if err := os.WriteFile(filepath.Join(root, "A", "x.mp3"), []byte("copy"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB.Exec(`INSERT INTO tracks(id,library_id,rel_path,size,mtime,fingerprint,status,added_at) VALUES (?,?,'A/x.mp3',4,1,?,'kept',0)`,
			tid, libID, fmt.Sprintf("f%d", i)); err != nil {
			t.Fatal(err)
		}
		if err := s.Move(ctx, tid); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, ".lark-trash", "A"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 distinct files in .lark-trash (none clobbered), got %d: %v", len(entries), entries)
	}
	var p1, p2, p3 string
	s.DB.QueryRow(`SELECT trash_path FROM tracks WHERE id=1`).Scan(&p1)
	s.DB.QueryRow(`SELECT trash_path FROM tracks WHERE id=2`).Scan(&p2)
	s.DB.QueryRow(`SELECT trash_path FROM tracks WHERE id=3`).Scan(&p3)
	if p1 == p2 || p1 == p3 || p2 == p3 {
		t.Fatalf("trash paths collided: %q %q %q", p1, p2, p3)
	}
	for _, p := range []string{p1, p2, p3} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("trashed file missing at %s", p)
		}
	}
}

// Never copy-then-delete across filesystems.
func TestCrossDeviceMoveFailsSafely(t *testing.T) {
	s, root, id, _ := setup(t)
	s.Rename = func(o, n string) error { return &os.LinkError{Op: "rename", Old: o, New: n, Err: syscall.EXDEV} }
	if err := s.Move(context.Background(), id); err != ErrCrossDevice {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "A", "x.mp3")); err != nil {
		t.Fatal("original touched")
	}
	var status string
	s.DB.QueryRow(`SELECT status FROM tracks WHERE id=1`).Scan(&status)
	if status != "kept" {
		t.Fatal("status changed on failed move")
	}
}

// An offline disk makes os.Remove(trashPath) return
// ENOENT for exactly the same reason a genuinely-already-gone file would --
// the whole library root is unreachable. Purge must tell the two apart by
// checking the root itself before treating ENOENT as "already purged",
// otherwise the row (and the caller's only record that the file is still
// sitting in .lark-trash once the disk comes back) is deleted for good.
func TestPurgeSkipsRowWhenLibraryRootIsOffline(t *testing.T) {
	s, root, id, _ := setup(t)
	ctx := context.Background()
	if err := s.Move(ctx, id); err != nil {
		t.Fatal(err)
	}

	// Simulate the disk going offline: the whole library root (and so the
	// trashed file within it) disappears from the filesystem.
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}

	n, err := s.Purge(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("purge must not count an offline root's rows as purged, got %d", n)
	}
	var status string
	if err := s.DB.QueryRow(`SELECT status FROM tracks WHERE id=?`, id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "trashed" {
		t.Fatal("row deleted although its library root was offline; the file may still be there once the disk returns")
	}
}

func TestPurgeRefusesPathsOutsideTrash(t *testing.T) {
	s, root, id, _ := setup(t)
	s.DB.Exec(`UPDATE tracks SET status='trashed', trashed_at=0, trash_path=? WHERE id=?`, filepath.Join(root, "A", "x.mp3"), id)
	s.Purge(context.Background(), 0)
	if _, err := os.Stat(filepath.Join(root, "A", "x.mp3")); err != nil {
		t.Fatal("purge deleted a file outside .lark-trash")
	}
}
