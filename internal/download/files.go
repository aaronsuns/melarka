package download

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/aaronsuns/lark-server/internal/library"
	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// artifactName matches a file named the way the worker names downloads,
// "<title> [<video id>].<rest>", capturing the stem and everything after it.
// Recovery only ever touches files of this shape, so a user's own
// "cover.jpg" (or anything else) in the download library is never deleted.
var artifactName = regexp.MustCompile(`^(.* \[[A-Za-z0-9_-]+\])\.(.+)$`)

var thumbExt = map[string]bool{"webp": true, "jpg": true, "jpeg": true, "png": true}

// isLeftover reports whether name (a base name in dir) is a yt-dlp
// intermediate: a partial download (.part, .part-FragN), a .ytdl state file,
// a .temp.* conversion file, or a thumbnail whose .m4a never materialised.
func isLeftover(dir, name string) bool {
	m := artifactName.FindStringSubmatch(name)
	if m == nil {
		return false
	}
	stem, rest := m[1], m[2]
	parts := strings.Split(rest, ".")
	for _, p := range parts {
		if p == "part" || strings.HasPrefix(p, "part-Frag") || p == "ytdl" {
			return true
		}
	}
	if len(parts) > 1 && slices.Contains(parts[:len(parts)-1], "temp") {
		return true
	}
	if len(parts) == 1 && thumbExt[strings.ToLower(parts[0])] {
		_, err := os.Stat(filepath.Join(dir, stem+".m4a"))
		return os.IsNotExist(err)
	}
	return false
}

// recover runs once at Run start, before any worker: jobs a previous
// process left downloading lose whatever they had written (a half-converted
// .m4a included — it was never recorded as finished) and go back to the
// queue, then yt-dlp intermediates left anywhere in the download target
// libraries are deleted.
func (s *Service) recover(ctx context.Context) {
	s.init()
	s.mu.Lock()
	stale, err := s.downloadingJobs(ctx)
	if err != nil {
		s.Log.Error("download: recovery list", "err", err)
	}
	if lib, err := s.target(ctx); err == nil {
		for _, j := range stale {
			dest, _, _ := destFor(lib, j)
			s.cleanup(ctx, lib, dest, true)
		}
	}
	n, err := s.requeueDownloading(ctx)
	s.mu.Unlock()
	if err != nil {
		s.Log.Error("download: recovery requeue", "err", err)
	} else if n > 0 {
		s.Log.Info("download: requeued interrupted jobs", "count", n)
	}
	libs, err := s.Library.Libraries(ctx)
	if err != nil {
		s.Log.Error("download: recovery libraries", "err", err)
		return
	}
	for _, l := range libs {
		if l.DownloadTarget {
			sweep(ctx, l.Root, s.Log)
		}
	}
}

// destFor is where job j downloads to in lib (no extension; yt-dlp adds
// it), plus the cleaned title and artist used for the path and overrides.
func destFor(lib library.Library, j Job) (destNoExt, title, artist string) {
	title, artist = ytdlp.CleanTitle(j.Title, j.Channel)
	channel := j.Channel
	if strings.TrimSpace(channel) == "" {
		channel = "YouTube"
	}
	destNoExt = filepath.Join(lib.Root, ytdlp.SafeName(channel), ytdlp.SafeName(title)+" ["+ytdlp.SafeName(j.VideoID)+"]")
	return destNoExt, title, artist
}

// cleanup removes a job's "<destNoExt>.*" files. withM4A also removes
// "<destNoExt>.m4a" (a cancelled, timed-out or interrupted run's output is
// never a finished file) — unless the library already has a track at that
// path, which must never lose its file to a later job's cleanup.
func (s *Service) cleanup(ctx context.Context, lib library.Library, destNoExt string, withM4A bool) {
	keepM4A := !withM4A
	if withM4A {
		if rel, ok := relInside(lib.Root, destNoExt+".m4a"); ok {
			if _, err := s.trackAt(ctx, lib.ID, rel); err == nil {
				keepM4A = true
			}
		}
	}
	removeLeftovers(destNoExt, keepM4A, s.Log)
}

func sweep(ctx context.Context, root string, log *slog.Logger) {
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if p != root && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir // .lark-trash and other hidden dirs
			}
			return nil
		}
		if d.Type().IsRegular() && isLeftover(filepath.Dir(p), d.Name()) {
			if err := os.Remove(p); err != nil {
				log.Warn("download: remove leftover", "path", p, "err", err)
			} else {
				log.Info("download: removed leftover", "path", p)
			}
		}
		return nil
	})
}

// removeLeftovers deletes every "<destNoExt>.*" file (keeping
// "<destNoExt>.m4a" when keepM4A). The channel directory is left in place
// even when empty: another worker may be downloading into it right now.
// Matching is by prefix, not filepath.Glob: destNoExt ends in
// "[<id>]", which Glob would read as a character class.
func removeLeftovers(destNoExt string, keepM4A bool, log *slog.Logger) {
	dir, base := filepath.Split(destNoExt)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, base+".") || (keepM4A && name == base+".m4a") {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			log.Warn("download: remove leftover", "path", filepath.Join(dir, name), "err", err)
		}
	}
}

// relInside returns p relative to root (slash-separated) if p lies strictly
// inside root.
func relInside(root, p string) (string, bool) {
	if !filepath.IsAbs(p) {
		return "", false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}
