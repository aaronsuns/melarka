package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNewerYtDlpVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"2026.08.01", "2026.07.04", true},
		{"2026.07.04", "2026.08.01", false},
		{"2026.07.04", "2026.07.04", false},
		{"2026.07.04.1", "2026.07.04", true},
		{"2026.07.04", "2026.07.04.1", false},
		{"2026.07.04.10", "2026.07.04.9", true},
		{"2027.01.01", "2026.12.31", true},
		{"", "2026.07.04", false},
	}
	for _, c := range cases {
		if got := newerYtDlpVersion(c.a, c.b); got != c.want {
			t.Errorf("newerYtDlpVersion(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// At startup a data-dir copy older than the image's own yt-dlp (the image
// was upgraded since the copy was made) is removed, so the newer image
// binary wins; anything else keeps the copy.
func TestPreferImageYtDlp(t *testing.T) {
	cases := []struct {
		name        string
		image, data string
		dataErr     error
		wantRemoved bool
	}{
		{"image newer", "2026.08.01", "2026.07.04", nil, true},
		{"same", "2026.07.04", "2026.07.04", nil, false},
		{"data newer", "2026.07.04", "2026.09.01", nil, false},
		{"data copy does not run", "2026.07.04", "", errors.New("exec format error"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, _ := newTestServer(t)
			s.YtDlpPath = "/image/yt-dlp"
			s.YtDlpBin = filepath.Join(t.TempDir(), "bin", "yt-dlp")
			writeScript(t, s.YtDlpBin, "echo data")
			s.preferImageYtDlp(context.Background(), func(_ context.Context, bin string) (string, error) {
				if bin == s.YtDlpBin {
					return c.data, c.dataErr
				}
				return c.image, nil
			})
			_, err := os.Stat(s.YtDlpBin)
			if removed := errors.Is(err, os.ErrNotExist); removed != c.wantRemoved {
				t.Fatalf("data copy removed=%v, want %v", removed, c.wantRemoved)
			}
		})
	}
}

// No data-dir copy: nothing to compare, no binary is even run.
func TestPreferImageYtDlpWithoutDataCopy(t *testing.T) {
	s, _ := newTestServer(t)
	s.preferImageYtDlp(context.Background(), func(context.Context, string) (string, error) {
		t.Fatal("version probed although there is no data-dir copy")
		return "", nil
	})
}

// The startup self-update runs the same code path as the admin endpoint:
// it creates the data-dir copy and reports its version.
func TestUpdateYtDlpSharedPath(t *testing.T) {
	s, _ := newTestServer(t)
	src := filepath.Join(t.TempDir(), "image-yt-dlp")
	writeScript(t, src, `echo 9.9.9`)
	s.YtDlpPath = src
	s.YtDlpBin = filepath.Join(t.TempDir(), "bin", "yt-dlp")
	v, path, err := s.updateYtDlp(context.Background())
	if err != nil || v != "9.9.9" || path != s.YtDlpBin {
		t.Fatalf("updateYtDlp = %q, %q, %v", v, path, err)
	}
}
