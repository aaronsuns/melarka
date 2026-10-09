package webui

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServesDiskDirWhenLARK_WEB_DIRSet(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<p>from disk</p>"), 0o644)
	os.MkdirAll(filepath.Join(dir, "assets"), 0o755)
	os.WriteFile(filepath.Join(dir, "assets", "a.js"), []byte("console.log(1)"), 0o644)
	t.Setenv("LARK_WEB_DIR", dir)
	h := Handler()
	for path, want := range map[string]string{"/": "from disk", "/albums/3": "from disk", "/assets/a.js": "console.log"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Body.String())
		}
	}
}

func TestEmbeddedByDefault(t *testing.T) {
	t.Setenv("LARK_WEB_DIR", "")
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Melarka") {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
}

func TestIndexIsNotCachedButAssetsAre(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<p>from disk</p>"), 0o644)
	os.MkdirAll(filepath.Join(dir, "assets"), 0o755)
	os.WriteFile(filepath.Join(dir, "assets", "a.js"), []byte("console.log(1)"), 0o644)
	t.Setenv("LARK_WEB_DIR", dir)
	h := Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/assets/a.js", nil))
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("asset cache-control %q", got)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/some/route", nil))
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("index cache-control %q", got)
	}
}

func TestWebManifestIsServedWithItsOwnMimeType(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<p>from disk</p>"), 0o644)
	os.WriteFile(filepath.Join(dir, "manifest.webmanifest"), []byte(`{"name":"Melarka"}`), 0o644)
	t.Setenv("LARK_WEB_DIR", dir)
	h := Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/manifest.webmanifest", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/manifest+json" {
		t.Errorf("content-type = %q, want application/manifest+json", got)
	}
}

func TestPathTraversalStaysInsideWebDir(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<p>from disk</p>"), 0o644)
	os.MkdirAll(filepath.Join(dir, "assets"), 0o755)
	os.WriteFile(filepath.Join(dir, "assets", "a.js"), []byte("console.log(1)"), 0o644)
	t.Setenv("LARK_WEB_DIR", dir)
	h := Handler()
	for _, path := range []string{"/../../etc/passwd", "/assets/../../x"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "from disk") {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Body.String())
		}
	}
}

// The service worker must revalidate, so a deploy's new worker is picked up.
func TestServiceWorkerIsServedAsJSAndRevalidates(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<p>from disk</p>"), 0o644)
	os.WriteFile(filepath.Join(dir, "sw.js"), []byte("self.x=1"), 0o644)
	t.Setenv("LARK_WEB_DIR", dir)
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/sw.js", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "self.x") {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("sw cache-control %q", got)
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "javascript") {
		t.Errorf("sw content-type %q", got)
	}
}
