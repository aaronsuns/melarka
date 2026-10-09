package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The binary prints its version and exits 0 without loading any config; the
// release sets the version with -ldflags -X (the same flag the Dockerfile uses).
func TestVersionFlag(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := filepath.Join(t.TempDir(), "lark")
	build := exec.Command("go", "build", "-o", bin,
		"-ldflags", "-X github.com/aaronsuns/lark-server/internal/buildinfo.Version=1.2.3-test", ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	for _, flag := range []string{"--version", "-version"} {
		out, err := exec.Command(bin, flag).Output()
		if err != nil {
			t.Fatalf("%s: %v", flag, err)
		}
		if got := strings.TrimSpace(string(out)); got != "melarka 1.2.3-test" {
			t.Fatalf("%s printed %q", flag, got)
		}
	}
}
