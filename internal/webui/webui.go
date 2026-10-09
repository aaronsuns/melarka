// Package webui serves the single-page web app: the embedded production
// build by default, or a directory on disk when LARK_WEB_DIR is set (dev).
package webui

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"
)

func init() {
	// Go's built-in MIME table doesn't know ".webmanifest"; without this,
	// net/http falls back to sniffing the body and serves it as text/plain,
	// which some browsers refuse to treat as a PWA manifest at all.
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
}

//go:embed dist
var dist embed.FS

// killWorker replaces sw.js when the offline cache is switched off. It has no
// fetch handler, so once it takes over every request goes straight to the
// network. On activate it claims the open pages from the old worker (no
// reload: music keeps playing), deletes the offline caches and unregisters
// itself. The page does the same on its side when /info says offline_cache
// is false, so either half is enough.
const killWorker = `self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", (e) => e.waitUntil((async () => {
  await self.clients.claim();
  const keys = await caches.keys();
  await Promise.all(keys.filter((k) => k.startsWith("lark-offline")).map((k) => caches.delete(k)));
  await self.registration.unregister();
})()));
`

type Options struct {
	// KillServiceWorker serves killWorker at /sw.js instead of the build's.
	KillServiceWorker bool
}

func Handler() http.Handler { return HandlerWith(Options{}) }

func HandlerWith(o Options) http.Handler {
	var root fs.FS
	if dir := os.Getenv("LARK_WEB_DIR"); dir != "" {
		root = os.DirFS(dir)
	} else {
		root, _ = fs.Sub(dist, "dist")
	}
	files := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Clean the path so "." and ".." segments can't be used to reach
		// outside the web root: path.Clean on a rooted ("/...") path always
		// collapses any ".." that would climb past "/", it never lets one
		// through. A cleaned copy of the request is used for serving so the
		// net/http path-traversal guard (which inspects r.URL.Path) agrees
		// with the path we actually looked up.
		clean := path.Clean("/" + r.URL.Path)
		r2 := r.Clone(r.Context())
		r2.URL.Path = clean
		p := strings.TrimPrefix(clean, "/")
		if p == "sw.js" && o.KillServiceWorker {
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			_, _ = w.Write([]byte(killWorker))
			return
		}
		if p != "" && p != "index.html" {
			if st, err := fs.Stat(root, p); err == nil && !st.IsDir() {
				if strings.HasPrefix(p, "assets/") {
					// Vite fingerprints asset names, so they can be cached forever.
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else if p == "sw.js" {
					// The offline-cache service worker: revalidate so a deploy's
					// new worker is picked up on the next visit.
					w.Header().Set("Cache-Control", "no-cache")
				}
				files.ServeHTTP(w, r2)
				return
			}
		}
		// SPA fallback. index.html must revalidate so a deploy is picked up.
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r2, root, "index.html")
	})
}
