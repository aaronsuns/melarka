package library

import (
	"context"

	"github.com/aaronsuns/lark-server/internal/config"
)

// CountLibraries reports how many libraries currently exist.
func (s *Store) CountLibraries(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM libraries`).Scan(&n)
	return n, err
}

// SeedIfEmpty applies config.yaml's library seeds only when the libraries
// table is empty. The DB is authoritative after the first start: once an
// admin has deleted a seeded library, every later process restart must not
// resurrect it just because it's still listed in config.yaml.
func SeedIfEmpty(ctx context.Context, store *Store, seeds []config.LibrarySeed) error {
	n, err := store.CountLibraries(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	for _, seed := range seeds {
		if _, err := store.EnsureLibrary(ctx, seed.Name, seed.Path, seed.DownloadTarget); err != nil {
			return err
		}
	}
	return nil
}
