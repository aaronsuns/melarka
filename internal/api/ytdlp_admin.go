package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// resolvedYtDlpBin reports the binary a running download actually uses: the
// data-dir copy if it's there and executable, else the configured fallback.
// This mirrors (deliberately duplicated, not shared) the resolution
// ExecRunner.Bin performs on every call in main.go.
func (s *Server) resolvedYtDlpBin() string {
	if st, err := os.Stat(s.YtDlpBin); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
		return s.YtDlpBin
	}
	return s.YtDlpPath
}

func (s *Server) ytdlpInfo(w http.ResponseWriter, r *http.Request) {
	v, err := s.YT.Version(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"version": v, "path": s.resolvedYtDlpBin()})
}

// copyYtDlpBinary resolves src (a bare command name looked up on PATH, or an
// absolute path) and atomically replaces dst with a copy of it, mode 0755:
// the content is written to a temp file in dst's own directory, fsynced and
// chmod'd, then renamed over dst. A reader of dst (another request's Bin()
// resolution, or a concurrent read of the file) never observes a
// partially-written binary, and a failure partway through leaves whatever
// was at dst before untouched.
func copyYtDlpBinary(src, dst string) error {
	resolved, err := exec.LookPath(src)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return err
	}
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".yt-dlp-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once the rename below succeeds
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return err
	}
	return os.Rename(tmpPath, dst)
}

// ytdlpUpdateTimeout bounds the detached "-U" run below: long enough for a
// real update+relaunch, short enough that a wedged network fetch can't hang
// the handler (and the mutex it holds) forever.
const ytdlpUpdateTimeout = 2 * time.Minute

// ytdlpUpdate copies the image's yt-dlp into the data dir the first time
// it's called (so it's writable and survives image upgrades independently),
// then runs "-U" on it and reports the resulting version. ExecRunner.Bin
// resolves to the data-dir copy as soon as it exists, so the "-U" call below
// already runs it rather than the (possibly read-only) image binary.
//
// Guarded by ytdlpMu: two concurrent admin clicks must not race the copy or
// run "-U" twice at once. The "-U" run is detached from the request context
// (an admin closing the tab mid-update must not abort it, leaving the binary
// in an unknown half-updated state) but bounded by ytdlpUpdateTimeout so a
// hung fetch can't wedge the handler indefinitely. Finally the data-dir copy
// is verified by actually running it (existence and the executable bit, all
// Bin() resolution elsewhere checks, don't prove an update fetched a binary
// that runs on this host/arch) — if that fails, the copy is removed so every
// subsequent Bin() resolution (this handler's response included) falls back
// to the configured path instead of silently shadowing a working binary.
func (s *Server) ytdlpUpdate(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), ytdlpUpdateTimeout)
	defer cancel()
	v, path, err := s.updateYtDlp(ctx)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"version": v, "path": path})
}

// updateYtDlp is the update itself, shared by the admin endpoint and the
// startup self-update: see ytdlpUpdate for what it does and why.
func (s *Server) updateYtDlp(ctx context.Context) (version, path string, err error) {
	s.ytdlpMu.Lock()
	defer s.ytdlpMu.Unlock()

	if _, err := os.Stat(s.YtDlpBin); errors.Is(err, os.ErrNotExist) {
		if err := copyYtDlpBinary(s.YtDlpPath, s.YtDlpBin); err != nil {
			return "", "", err
		}
	} else if err != nil {
		return "", "", err
	}

	if _, err := s.YT.Runner.Output(ctx, []string{"-U"}); err != nil {
		return "", "", err
	}

	v, err := ytdlpVersionOf(ctx, s.YtDlpBin)
	if err != nil {
		s.Log.Error("ytdlp update: data-dir copy failed to run after update, reverting to configured path", "err", err)
		if rmErr := os.Remove(s.YtDlpBin); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			s.Log.Error("ytdlp update: remove broken copy", "err", rmErr)
		}
		v, err = s.YT.Version(ctx)
		if err != nil {
			return "", "", err
		}
	}
	return v, s.resolvedYtDlpBin(), nil
}

// ytdlpVersionOf runs bin --version.
func ytdlpVersionOf(ctx context.Context, bin string) (string, error) {
	c := &ytdlp.Client{Runner: ytdlp.ExecRunner{Bin: func() string { return bin }}}
	return c.Version(ctx)
}

// newerYtDlpVersion reports whether yt-dlp version a is newer than b.
// Versions are dates with an optional build number, YYYY.MM.DD[.N]; parts
// are compared in order (numerically when both are numbers, else as
// strings), and a version with an extra part is newer than its prefix.
func newerYtDlpVersion(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := range min(len(pa), len(pb)) {
		if pa[i] == pb[i] {
			continue
		}
		na, errA := strconv.Atoi(pa[i])
		nb, errB := strconv.Atoi(pb[i])
		if errA == nil && errB == nil {
			return na > nb
		}
		return pa[i] > pb[i]
	}
	return len(pa) > len(pb)
}

// startupVersionTimeout bounds each --version probe at startup.
const startupVersionTimeout = 30 * time.Second

// preferImageYtDlp removes the data-dir copy of yt-dlp when the image's own
// binary is newer — an image upgrade must not stay shadowed by a copy made
// (and self-updated) under an older image. versionOf runs a binary's
// --version (injectable for tests). Any probe failure keeps the copy.
func (s *Server) preferImageYtDlp(ctx context.Context, versionOf func(ctx context.Context, bin string) (string, error)) {
	if _, err := os.Stat(s.YtDlpBin); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, startupVersionTimeout)
	defer cancel()
	dataV, err := versionOf(ctx, s.YtDlpBin)
	if err != nil {
		s.Log.Warn("ytdlp startup: data-dir copy version", "err", err)
		return
	}
	imageV, err := versionOf(ctx, s.YtDlpPath)
	if err != nil {
		s.Log.Warn("ytdlp startup: image version", "err", err)
		return
	}
	if !newerYtDlpVersion(imageV, dataV) {
		return
	}
	if err := os.Remove(s.YtDlpBin); err != nil {
		s.Log.Error("ytdlp startup: remove older data-dir copy", "err", err)
		return
	}
	s.Log.Info("ytdlp startup: image yt-dlp is newer, removed data-dir copy", "image", imageV, "data", dataV)
}

// StartYtDlpMaintenance runs at server start: the image's yt-dlp wins if it
// is newer than the data-dir copy, then one self-update (the same code path
// as POST /admin/ytdlp/update, so it shares its mutex) runs in the
// background, bounded by ytdlpUpdateTimeout. It never blocks startup.
func (s *Server) StartYtDlpMaintenance(ctx context.Context) {
	s.preferImageYtDlp(ctx, ytdlpVersionOf)
	go func() {
		uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ytdlpUpdateTimeout)
		defer cancel()
		v, path, err := s.updateYtDlp(uctx)
		if err != nil {
			s.Log.Warn("ytdlp startup: self-update failed", "err", err)
			return
		}
		s.Log.Info("ytdlp startup: self-update done", "version", v, "path", path)
	}()
}
