package api

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/artwork"
	"github.com/aaronsuns/lark-server/internal/lyrics"
)

func TestCoverEndpoints(t *testing.T) {
	s, ts := newTestServer(t)
	s.Artwork.Providers = []artwork.Provider{artwork.Folder{}}
	kid := loginAs(t, s, "kid", "member")
	ctx := context.Background()
	root := t.TempDir()
	lib, _ := s.Library.EnsureLibrary(ctx, "covers", root, false)
	os.MkdirAll(filepath.Join(root, "with"), 0o755)
	os.MkdirAll(filepath.Join(root, "without"), 0o755)
	if out, err := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-y", "-f", "lavfi", "-i", "color=c=red:s=640x640",
		"-frames:v", "1", filepath.Join(root, "with", "cover.jpg")).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg: %v %s", err, out)
	}
	add := func(rel, album string) int64 {
		res, err := s.Library.DB.Exec(`INSERT INTO tracks(library_id,rel_path,size,mtime,fingerprint,duration_ms,codec,bitrate,
			tag_title,tag_album,status,added_at) VALUES (?,?,1,1,?,1000,'mp3',192,'t',?,'kept',1)`, lib.ID, rel, rel, album)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		s.Library.Reindex(ctx, id)
		return id
	}
	a := add("with/a.mp3", "有封面")
	b := add("without/b.mp3", "")
	get := func(path string) (*http.Response, []byte) { return do(t, ts, kid, "GET", path, nil) }
	r, body := get(fmt.Sprintf("/api/v1/tracks/%d/cover", a))
	if r.StatusCode != 200 || r.Header.Get("Content-Type") != "image/jpeg" || !strings.Contains(r.Header.Get("Cache-Control"), "max-age=86400") || len(body) < 100 {
		t.Fatalf("cover: %d %v", r.StatusCode, r.Header)
	}
	if r, _ := get(fmt.Sprintf("/api/v1/tracks/%d/cover?size=1000", a)); r.StatusCode != 200 {
		t.Fatalf("1000: %d", r.StatusCode)
	}
	if r, _ := get(fmt.Sprintf("/api/v1/tracks/%d/cover?size=500", a)); r.StatusCode != 400 {
		t.Fatalf("bad size: %d", r.StatusCode)
	}
	if r, b := get(fmt.Sprintf("/api/v1/tracks/%d/cover", b)); r.StatusCode != 404 || !strings.Contains(string(b), `"code":"no_cover"`) || r.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("none: %d %s", r.StatusCode, b)
	}
	var album int64
	s.Library.DB.QueryRow(`SELECT album_id FROM tracks WHERE id=?`, a).Scan(&album)
	if r, _ := get(fmt.Sprintf("/api/v1/albums/%d/cover", album)); r.StatusCode != 200 {
		t.Fatalf("album: %d", r.StatusCode)
	}
	s.Library.DB.Exec(`UPDATE tracks SET status='trashed' WHERE id=?`, a)
	if r, b := get(fmt.Sprintf("/api/v1/tracks/%d/cover", a)); r.StatusCode != 404 || !strings.Contains(string(b), `"code":"not_found"`) {
		t.Fatalf("trashed: %d %s", r.StatusCode, b)
	}
	if r, _ := do(t, ts, "", "GET", fmt.Sprintf("/api/v1/tracks/%d/cover", b), nil); r.StatusCode != 401 {
		t.Fatalf("no token: %d", r.StatusCode)
	}
}

// hangProv is an online provider that ignores ctx until the test ends.
type hangProv struct{ release chan struct{} }

func (hangProv) Name() string { return "itunes" }
func (h hangProv) Find(context.Context, lyrics.Query) ([]artwork.Found, error) {
	<-h.release
	return nil, nil
}

func TestCoverWaitIsCapped(t *testing.T) {
	s, ts := newTestServer(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	s.Artwork.Providers = []artwork.Provider{hangProv{release}}
	old := coverWait
	coverWait = 200 * time.Millisecond
	t.Cleanup(func() { coverWait = old })
	kid := loginAs(t, s, "kid", "member")
	id := seedTrack(t, s, "slow.mp3", "x", "y", "")
	start := time.Now()
	r, b := do(t, ts, kid, "GET", fmt.Sprintf("/api/v1/tracks/%d/cover", id), nil)
	if r.StatusCode != 404 || !strings.Contains(string(b), "no_cover") || time.Since(start) > 2*time.Second {
		t.Fatalf("%d %s after %v", r.StatusCode, b, time.Since(start))
	}
}

// A lookup that fails on the database (say "database is locked" after the
// busy timeout) is logged and answered like a missing cover — never a 500 a
// cover grid would show as broken, and never cached by the client.
func TestCoverLookupErrorIsNoCover(t *testing.T) {
	s, ts := newTestServer(t)
	var logs strings.Builder
	s.Log = slog.New(slog.NewTextHandler(&syncWriter{w: &logs}, nil))
	s.Artwork.Providers = []artwork.Provider{artwork.Folder{}}
	kid := loginAs(t, s, "kid", "member")
	id := seedTrack(t, s, "locked.mp3", "x", "y", "")
	if _, err := s.Library.DB.Exec(`DROP TABLE artwork_lookup`); err != nil {
		t.Fatal(err)
	}
	r, b := do(t, ts, kid, "GET", fmt.Sprintf("/api/v1/tracks/%d/cover", id), nil)
	if r.StatusCode != 404 || !strings.Contains(string(b), `"code":"no_cover"`) || r.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("db error: %d %s %v", r.StatusCode, b, r.Header)
	}
	if !strings.Contains(logs.String(), "artwork_lookup") {
		t.Fatalf("lookup error not logged: %q", logs.String())
	}
}

type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}
