package artwork

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// sweepEvery is how often RunSweep cleans the cache; tempMaxAge is how old a
// temp file must be before it counts as left by a crash.
const (
	sweepEvery = 24 * time.Hour
	tempMaxAge = time.Hour
)

var coverName = regexp.MustCompile(`^(\d+)-\d+\.jpg$`)

// Sweep removes covers whose track is no longer in the library (purged from
// the trash, a missing file, a deleted library) and temp files older than an
// hour (a fetch or conversion that died). Trashed tracks keep theirs, for a
// restore; any other file is left alone. It returns how many files it removed.
func (s *Service) Sweep(ctx context.Context) (int, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, de := range entries {
		if ctx.Err() != nil {
			return removed, ctx.Err()
		}
		name := de.Name()
		if !de.Type().IsRegular() {
			continue
		}
		stale := false
		switch m := coverName.FindStringSubmatch(name); {
		case m != nil:
			id, _ := strconv.ParseInt(m[1], 10, 64)
			var one int
			err := s.DB.QueryRowContext(ctx, `SELECT 1 FROM tracks WHERE id=?`, id).Scan(&one)
			switch {
			case err == nil:
			case errors.Is(err, sql.ErrNoRows):
				stale = true
			default:
				return removed, err
			}
		case strings.HasPrefix(name, ".dl-") || strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".tmp"):
			info, err := de.Info()
			stale = err == nil && s.now().Sub(info.ModTime()) > tempMaxAge
		}
		if stale {
			if err := os.Remove(filepath.Join(s.Dir, name)); err == nil {
				removed++
			} else if !errors.Is(err, fs.ErrNotExist) {
				s.log().Warn("artwork sweep: remove", "file", name, "err", err)
			}
		}
	}
	return removed, nil
}

// RunSweep runs Sweep now and then once a day until ctx is done.
func (s *Service) RunSweep(ctx context.Context) {
	for {
		if n, err := s.Sweep(ctx); err != nil && ctx.Err() == nil {
			s.log().Warn("artwork sweep", "err", err)
		} else if n > 0 {
			s.log().Info("artwork sweep", "removed", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(sweepEvery):
		}
	}
}
