// Package trash moves deleted tracks into <library>/.lark-trash, restores
// them, and purges them after the retention period.
package trash

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/aaronsuns/lark-server/internal/library"
)

const Retention = 30 * 24 * time.Hour
const dirName = ".lark-trash"

var (
	ErrNotFound    = errors.New("not found")
	ErrCrossDevice = errors.New("trash is on a different filesystem than the file; refusing to copy")
	ErrConflict    = errors.New("a file already exists at the original path")
)

type Item struct {
	TrackID   int64  `json:"track_id"`
	Path      string `json:"path"`
	TrashedAt int64  `json:"trashed_at"`
	PurgeAt   int64  `json:"purge_at"`
}

type Service struct {
	DB      *sql.DB
	Library *library.Store
	Log     *slog.Logger
	Now     func() time.Time
	Rename  func(oldpath, newpath string) error
	Link    func(oldpath, newpath string) error
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) rename(o, n string) error {
	if s.Rename != nil {
		return s.Rename(o, n)
	}
	return os.Rename(o, n)
}

func (s *Service) link(o, n string) error {
	if s.Link != nil {
		return s.Link(o, n)
	}
	return os.Link(o, n)
}

// uniquePath returns candidate unchanged if nothing exists there yet, else a
// path with a timestamp suffix (and, if even that is already taken, with an
// incrementing counter on top of it) that nothing currently occupies.
// Without this, trashing two files that share a rel_path within the same
// second (e.g. delete, re-download, delete again) would collide and
// os.Rename would silently clobber the earlier trashed copy.
func uniquePath(candidate string, now time.Time) string {
	if _, err := os.Lstat(candidate); err != nil {
		return candidate
	}
	stamped := candidate + "." + now.Format("20060102150405")
	candidate = stamped
	for i := 2; ; i++ {
		if _, err := os.Lstat(candidate); err != nil {
			return candidate
		}
		candidate = fmt.Sprintf("%s.%d", stamped, i)
	}
}

// Move relocates the track's file into <library root>/.lark-trash, mirroring
// its relative path, and marks the row trashed. The row's rel_path is
// repointed at the trash location (so the original path is freed for a
// re-downloaded file to reuse under UNIQUE(library_id, rel_path)) while
// trash_orig_rel remembers where to put the file back on Restore.
func (s *Service) Move(ctx context.Context, trackID int64) error {
	var status, rel string
	err := s.DB.QueryRowContext(ctx, `SELECT status, rel_path FROM tracks WHERE id=?`, trackID).Scan(&status, &rel)
	if errors.Is(err, sql.ErrNoRows) || status == "trashed" {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	abs, lib, err := s.Library.TrackPath(ctx, trackID)
	if err != nil {
		return err
	}
	dest := filepath.Join(lib.Root, dirName, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	dest = uniquePath(dest, s.now())
	if err := s.rename(abs, dest); err != nil {
		if errors.Is(err, syscall.EXDEV) {
			return ErrCrossDevice
		}
		return err
	}
	relDest, err := filepath.Rel(lib.Root, dest)
	if err != nil {
		s.rename(dest, abs) // keep disk and DB consistent
		return err
	}
	relDest = filepath.ToSlash(relDest)
	if _, err := s.DB.ExecContext(ctx, `UPDATE tracks SET status='trashed', trashed_at=?, trash_path=?, rel_path=?, trash_orig_rel=?, missing_since=NULL WHERE id=?`,
		s.now().Unix(), dest, relDest, rel, trackID); err != nil {
		s.rename(dest, abs) // keep disk and DB consistent
		return err
	}
	_, err = s.DB.ExecContext(ctx, `DELETE FROM track_fts WHERE rowid=?`, trackID)
	return err
}

// MoveBroken moves every track currently flagged broken into the trash, one
// Move at a time (so each is restorable until it is purged), and reports how
// many went and how many could not be moved. Only the tracks the admin
// console lists count: broken, not trashed, not missing from disk. A file
// that fails is logged and skipped; the rest still go. Running it again
// moves nothing.
func (s *Service) MoveBroken(ctx context.Context) (moved, failed int, err error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM tracks WHERE broken=1 AND status!='trashed' AND missing_since IS NULL ORDER BY id`)
	if err != nil {
		return 0, 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return moved, failed, err
		}
		switch err := s.Move(ctx, id); {
		case err == nil:
			moved++
		case errors.Is(err, ErrNotFound):
			// trashed meanwhile: nothing to do
		default:
			failed++
			s.Log.Warn("trash broken: could not move", "track", id, "err", err)
		}
	}
	return moved, failed, nil
}

// Restore puts a trashed track's file back at its original path
// (trash_orig_rel), not at the current (trash) rel_path.
//
// It links the file into place rather than stat-then-rename: a separate
// os.Stat check followed by os.Rename leaves a race window in which another
// file can appear at dest between the two calls, and os.Rename would then
// silently replace it. os.Link instead fails atomically with EEXIST if
// anything already occupies dest. The original stays in trash until the DB
// row is updated, so if that UPDATE fails (e.g. a row raced us to the same
// rel_path), rolling back only means removing the new link at dest — the
// trashed file itself was never touched.
func (s *Service) Restore(ctx context.Context, trackID int64) error {
	var status string
	var trashPath, origRel sql.NullString
	var libID int64
	err := s.DB.QueryRowContext(ctx, `SELECT status, trash_path, trash_orig_rel, library_id FROM tracks WHERE id=?`, trackID).
		Scan(&status, &trashPath, &origRel, &libID)
	if errors.Is(err, sql.ErrNoRows) || status != "trashed" || !trashPath.Valid || !origRel.Valid {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	lib, err := s.Library.Library(ctx, libID)
	if err != nil {
		return err
	}
	dest := filepath.Join(lib.Root, filepath.FromSlash(origRel.String))
	var collisions int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM tracks WHERE library_id=? AND rel_path=? AND status!='trashed'`,
		libID, origRel.String).Scan(&collisions); err != nil {
		return err
	}
	if collisions > 0 {
		return ErrConflict
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := s.link(trashPath.String, dest); err != nil {
		if errors.Is(err, syscall.EXDEV) {
			return ErrCrossDevice
		}
		if os.IsExist(err) {
			return ErrConflict
		}
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE tracks SET rel_path=?, trash_orig_rel=NULL, status='kept', trashed_at=NULL, trash_path=NULL, missing_since=NULL WHERE id=?`,
		origRel.String, trackID); err != nil {
		os.Remove(dest) // undo the link; the file stays safely in trash, row stays trashed
		return err
	}
	if rmErr := os.Remove(trashPath.String); rmErr != nil {
		s.Log.Warn("restore: could not remove trash copy after linking it into place", "path", trashPath.String, "err", rmErr)
	}
	return s.Library.Reindex(ctx, trackID)
}

// insideTrash guards purge against deleting anything outside <root>/.lark-trash.
func insideTrash(root, p string) bool {
	rel, err := filepath.Rel(filepath.Join(root, dirName), p)
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..")
}

func (s *Service) Purge(ctx context.Context, olderThan time.Duration) (int, error) {
	cutoff := s.now().Add(-olderThan).Unix()
	rows, err := s.DB.QueryContext(ctx, `SELECT t.id, t.trash_path, l.root FROM tracks t JOIN libraries l ON l.id=t.library_id
		WHERE t.status='trashed' AND t.trashed_at <= ?`, cutoff)
	if err != nil {
		return 0, err
	}
	type victim struct {
		id         int64
		path, root string
	}
	var vs []victim
	for rows.Next() {
		var v victim
		var p sql.NullString
		if err := rows.Scan(&v.id, &p, &v.root); err != nil {
			rows.Close()
			return 0, err
		}
		v.path = p.String
		vs = append(vs, v)
	}
	rows.Close()
	n := 0
	for _, v := range vs {
		if !insideTrash(v.root, v.path) {
			s.Log.Error("purge: refusing path outside trash", "track", v.id, "path", v.path)
			continue
		}
		if err := os.Remove(v.path); err != nil {
			if os.IsNotExist(err) {
				// A missing file normally just means it's already gone
				// (e.g. purged by hand). But if the library's disk is
				// offline, its whole root -- and so every path under it,
				// trash included -- looks the same way: ENOENT, not a
				// permission or I/O error. Deleting the row in that case
				// would forget the track forever the moment the disk
				// comes back with the file still sitting in .lark-trash.
				// So before trusting ENOENT, confirm the root itself is
				// actually there.
				if st, statErr := os.Stat(v.root); statErr != nil || !st.IsDir() {
					s.Log.Warn("purge: library root is missing or unreadable; keeping trashed row", "track", v.id, "root", v.root, "err", statErr)
					continue
				}
			} else {
				s.Log.Warn("purge: remove", "path", v.path, "err", err)
				continue
			}
		}
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM tracks WHERE id=?`, v.id); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// List reports trashed items by their original path, since that's what a
// user recognizes from before deletion.
func (s *Service) List(ctx context.Context) ([]Item, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, trash_orig_rel, trashed_at FROM tracks WHERE status='trashed' ORDER BY trashed_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		var it Item
		var orig sql.NullString
		if err := rows.Scan(&it.TrackID, &orig, &it.TrashedAt); err != nil {
			return nil, err
		}
		it.Path = orig.String
		it.PurgeAt = it.TrashedAt + int64(Retention/time.Second)
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	for {
		if n, err := s.Purge(ctx, Retention); err != nil || n > 0 {
			s.Log.Info("trash purge", "purged", n, "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
