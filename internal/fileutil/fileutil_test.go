package fileutil

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestRelInside(t *testing.T) {
	for _, c := range []struct {
		root, p, rel string
		ok           bool
	}{
		{"/ch", "/ch/UCx/a.m4a", "UCx/a.m4a", true},
		{"/ch/", "/ch/UCx/../UCy/a.m4a", "UCy/a.m4a", true},
		{"/ch", "/ch", "", false},
		{"/ch", "/chx/a", "", false},
		{"/ch", "/ch/../etc/passwd", "", false},
		{"/ch", "relative/a", "", false},
	} {
		rel, ok := RelInside(c.root, c.p)
		if rel != c.rel || ok != c.ok {
			t.Errorf("RelInside(%q,%q) = %q,%v", c.root, c.p, rel, ok)
		}
	}
}

func TestMove(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "a.m4a"), filepath.Join(dir, "sub", "b.m4a")
	os.WriteFile(src, []byte("audio"), 0o644)
	if err := Move(src, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "audio" {
		t.Fatal(string(b))
	}
	if _, err := os.Stat(src); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("source must be gone")
	}
	os.WriteFile(src, []byte("other"), 0o644)
	if err := Move(src, dst); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("never overwrite: %v", err)
	}
	// Across filesystems (link says EXDEV): copied, synced, source removed.
	link = func(string, string) error { return &os.LinkError{Op: "link", Err: syscall.EXDEV} }
	t.Cleanup(func() { link = os.Link })
	dst2 := filepath.Join(dir, "c.m4a")
	if err := Move(src, dst2); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst2); string(b) != "other" {
		t.Fatal(string(b))
	}
	if _, err := os.Stat(src); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("source must be gone after a copy")
	}
	if _, err := os.Stat(dst2 + ".lark-move"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("no temp file left")
	}
}

// A filesystem without hard links (link says EPERM) gets the copy too, and
// still never overwrites.
func TestMoveWithoutHardLinks(t *testing.T) {
	dir := t.TempDir()
	link = func(string, string) error { return &os.LinkError{Op: "link", Err: syscall.EPERM} }
	t.Cleanup(func() { link = os.Link })
	src, dst := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	os.WriteFile(src, []byte("x"), 0o644)
	if err := Move(src, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "x" {
		t.Fatal(string(b))
	}
	os.WriteFile(src, []byte("y"), 0o644)
	if err := Move(src, dst); !errors.Is(err, fs.ErrExist) {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "x" {
		t.Fatal("overwritten")
	}
}
