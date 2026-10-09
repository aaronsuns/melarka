// Package testutil holds helpers shared by package tests.
package testutil

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/aaronsuns/lark-server/internal/db"
)

func DB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "lark.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// Sample generates a 2-second sine-wave audio file with ffmpeg at
// dir/name. Extra args are passed to ffmpeg as output options, such as
// "-metadata", "title=X". Skips the test if ffmpeg is missing.
func Sample(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	out := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := append([]string{"-nostdin", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=440:duration=2"}, args...)
	cmd = append(cmd, out)
	if b, err := exec.Command("ffmpeg", cmd...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v %s", err, b)
	}
	return out
}
