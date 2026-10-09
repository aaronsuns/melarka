package library

import (
	"context"
	"testing"

	"github.com/aaronsuns/lark-server/internal/config"
	"github.com/aaronsuns/lark-server/internal/testutil"
)

// The DB is authoritative after the first start.
// SeedIfEmpty must apply config.yaml's seeds on an empty DB, but must not
// resurrect a library an admin deleted on a later restart -- it only ever
// looks at whether the libraries table is empty, not at individual names.
func TestSeedIfEmptyAppliesOnceThenLeavesDeletionsAlone(t *testing.T) {
	ctx := context.Background()
	store := &Store{DB: testutil.DB(t)}
	seeds := []config.LibrarySeed{
		{Name: "main", Path: t.TempDir()},
		{Name: "extra", Path: t.TempDir()},
	}

	if err := SeedIfEmpty(ctx, store, seeds); err != nil {
		t.Fatal(err)
	}
	libs, err := store.Libraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(libs) != 2 {
		t.Fatalf("seeds not applied on empty DB: got %d libraries", len(libs))
	}

	// A later "restart" with the table already populated must not reseed.
	if err := SeedIfEmpty(ctx, store, seeds); err != nil {
		t.Fatal(err)
	}
	if libs2, _ := store.Libraries(ctx); len(libs2) != 2 {
		t.Fatalf("reseeded a non-empty DB: got %d libraries", len(libs2))
	}

	// An admin deletes one library; the DB is no longer empty, so a further
	// restart must leave the deletion alone rather than recreating it from
	// config.yaml.
	if err := store.DeleteLibrary(ctx, libs[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := SeedIfEmpty(ctx, store, seeds); err != nil {
		t.Fatal(err)
	}
	libs3, err := store.Libraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(libs3) != 1 {
		t.Fatalf("deleted library was reseeded: got %d libraries, want 1", len(libs3))
	}
}
