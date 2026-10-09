package library

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/aaronsuns/lark-server/internal/media"
)

const missingPurgeSeconds = 30 * 24 * 3600

var audioExt = map[string]bool{
	".mp3": true, ".flac": true, ".wav": true, ".wma": true, ".m4a": true, ".aac": true,
	".ogg": true, ".opus": true, ".ape": true, ".aif": true, ".aiff": true, ".wv": true,
}

func IsAudioFile(name string) bool { return audioExt[strings.ToLower(filepath.Ext(name))] }

// isDownloadIntermediate reports whether name is a yt-dlp work file: a
// ".temp.<ext>" written while converting or embedding the thumbnail (it has
// an audio extension, so without this a scan mid-download would ingest it),
// or a ".part"/".ytdl" partial.
func isDownloadIntermediate(name string) bool {
	return strings.Contains(name, ".temp.") || strings.HasSuffix(name, ".part") || strings.HasSuffix(name, ".ytdl")
}

// isUniqueConstraintErr reports whether err is a UNIQUE-constraint violation.
// It's how a rel_path collision with a trashed row (a trashed row keeps its
// slot until it is moved out of the way) shows up.
func isUniqueConstraintErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE")
}

type ScanResult struct {
	Added     int `json:"added"`
	Updated   int `json:"updated"`
	Moved     int `json:"moved"`
	Missing   int `json:"missing"`
	Broken    int `json:"broken"`
	Unchanged int `json:"unchanged"`
}

// FolderApplier tags a track from its folder path (tags.FolderTagger).
type FolderApplier interface {
	Apply(ctx context.Context, trackID int64, rel string) error
}

type Scanner struct {
	Store  *Store
	Prober media.Prober
	Log    *slog.Logger
	Tagger FolderApplier // optional; applied to new, changed and moved tracks before Reindex
}

func (sc *Scanner) applyFolderTags(ctx context.Context, id int64, rel string) {
	if sc.Tagger == nil {
		return
	}
	if err := sc.Tagger.Apply(ctx, id, rel); err != nil {
		sc.Log.Warn("scan: folder tags", "track", id, "path", rel, "err", err)
	}
}

type existing struct {
	id          int64
	rel         string
	size, mtime int64
	fingerprint string
	seen        bool
}

type found struct {
	rel         string
	size, mtime int64
}

func (sc *Scanner) Scan(ctx context.Context, lib Library) (ScanResult, error) {
	var res ScanResult
	if st, err := os.Stat(lib.Root); err != nil || !st.IsDir() {
		return res, ErrLibraryUnavailable
	}
	initial := lib.LastScanAt == nil
	db := sc.Store.DB

	// Trashed rows are invisible to the scanner: their files live under .lark-trash.
	rows, err := db.QueryContext(ctx, `SELECT id, rel_path, size, mtime, fingerprint FROM tracks WHERE library_id=? AND status!='trashed'`, lib.ID)
	if err != nil {
		return res, err
	}
	known := map[string]*existing{}
	for rows.Next() {
		var rel string
		e := &existing{}
		if err := rows.Scan(&e.id, &rel, &e.size, &e.mtime, &e.fingerprint); err != nil {
			rows.Close()
			return res, err
		}
		e.rel = rel
		known[rel] = e
	}
	rows.Close()

	var files []found
	var skipped []string // unreadable dirs: rows under them are not marked missing
	walkErr := filepath.WalkDir(lib.Root, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rel, _ := filepath.Rel(lib.Root, p)
		rel = filepath.ToSlash(rel)
		if err != nil {
			if (d != nil && d.IsDir()) || rel == "." {
				skipped = append(skipped, rel+"/")
				sc.Log.Warn("scan: unreadable dir", "path", p, "err", err)
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if rel != "." && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		// Dotfiles (e.g. macOS AppleDouble "._song.mp3" sidecars left by SMB
		// copies) are never real tracks; skip them before the extension check
		// so they don't get probed and recorded as broken.
		if strings.HasPrefix(d.Name(), ".") || isDownloadIntermediate(d.Name()) {
			return nil
		}
		if !d.Type().IsRegular() || !IsAudioFile(d.Name()) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		files = append(files, found{rel: rel, size: info.Size(), mtime: info.ModTime().Unix()})
		return nil
	})
	if walkErr != nil {
		return res, walkErr
	}
	if len(files) == 0 && len(known) > 0 {
		return res, ErrLibraryUnavailable
	}

	var fresh []found
	for _, f := range files {
		e, ok := known[f.rel]
		switch {
		case !ok:
			fresh = append(fresh, f)
		case e.size == f.size && e.mtime == f.mtime:
			e.seen = true
			res.Unchanged++
		default:
			e.seen = true
			if err := sc.update(ctx, lib, e.id, f, &res); err != nil {
				return res, err
			}
			res.Updated++
		}
	}

	// Unseen rows are candidates for moves (same content under a new path).
	// Content-identical files (e.g. duplicate rips) can share a fingerprint,
	// so each fingerprint keeps a list of candidates, not a single row.
	byFP := map[string][]*existing{}
	for rel, e := range known {
		if !e.seen && !underAny(rel, skipped) {
			byFP[e.fingerprint] = append(byFP[e.fingerprint], e)
		}
	}
	now := sc.Store.now()
	for _, f := range fresh {
		fp, err := media.Fingerprint(filepath.Join(lib.Root, filepath.FromSlash(f.rel)), f.size)
		if err != nil {
			sc.Log.Warn("scan: fingerprint", "path", f.rel, "err", err)
			continue
		}
		if idx := pickMoveCandidate(byFP[fp], f); idx >= 0 {
			cands := byFP[fp]
			e := cands[idx]
			cands = append(cands[:idx], cands[idx+1:]...)
			if len(cands) == 0 {
				delete(byFP, fp)
			} else {
				byFP[fp] = cands
			}
			// AND status!='trashed': e was snapshotted before the walk, so a
			// concurrent trash.Move between the snapshot and this UPDATE
			// would otherwise have its trash bookkeeping (rel_path,
			// trash_path, ...) clobbered by scan data for a file that isn't
			// where the trashed row now claims to live.
			r, err := db.ExecContext(ctx, `UPDATE tracks SET rel_path=?, mtime=?, missing_since=NULL WHERE id=? AND status!='trashed'`, f.rel, f.mtime, e.id)
			if err != nil {
				if isUniqueConstraintErr(err) {
					sc.Log.Warn("scan: move target collides with an existing row (likely trashed); leaving both for a later scan", "path", f.rel, "err", err)
					continue
				}
				return res, err
			}
			if n, _ := r.RowsAffected(); n == 0 {
				sc.Log.Warn("scan: move target was trashed mid-scan; leaving it untouched", "path", f.rel, "track", e.id)
				continue
			}
			e.seen = true
			sc.applyFolderTags(ctx, e.id, f.rel)
			if err := sc.Store.Reindex(ctx, e.id); err != nil {
				return res, err
			}
			res.Moved++
			continue
		}
		status := "pending"
		if initial {
			status = "kept"
		}
		r, err := db.ExecContext(ctx, `INSERT INTO tracks(library_id,rel_path,size,mtime,fingerprint,status,added_at) VALUES (?,?,?,0,?,?,?)`,
			lib.ID, f.rel, f.size, fp, status, now)
		if err != nil {
			if isUniqueConstraintErr(err) {
				sc.Log.Warn("scan: rel_path collides with an existing row (likely trashed); skipping this scan", "path", f.rel, "err", err)
				continue
			}
			return res, err
		}
		id, _ := r.LastInsertId()
		if err := sc.update(ctx, lib, id, f, &res); err != nil {
			return res, err
		}
		res.Added++
	}

	for rel, e := range known {
		if e.seen || underAny(rel, skipped) {
			continue
		}
		r, err := db.ExecContext(ctx, `UPDATE tracks SET missing_since=? WHERE id=? AND missing_since IS NULL AND status!='trashed'`, now, e.id)
		if err != nil {
			return res, err
		}
		if n, _ := r.RowsAffected(); n > 0 {
			res.Missing++
		}
	}
	// Seen rows are present again.
	for _, e := range known {
		if e.seen {
			if _, err := db.ExecContext(ctx, `UPDATE tracks SET missing_since=NULL WHERE id=? AND missing_since IS NOT NULL AND status!='trashed'`, e.id); err != nil {
				return res, err
			}
		}
	}
	// status!='trashed': a trashed row's file lives under .lark-trash, not at
	// missing_since's stale rel_path, and only trash.Purge (the 30-day trash
	// retention) may delete it — deleting the row here would orphan the file
	// in .lark-trash forever.
	if _, err := db.ExecContext(ctx, `DELETE FROM track_fts WHERE rowid IN (SELECT id FROM tracks WHERE library_id=? AND missing_since < ? AND status!='trashed')`, lib.ID, now-missingPurgeSeconds); err != nil {
		return res, err
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM tracks WHERE library_id=? AND missing_since < ? AND status!='trashed'`, lib.ID, now-missingPurgeSeconds); err != nil {
		return res, err
	}
	if err := sc.Store.pruneOrphans(ctx); err != nil {
		return res, err
	}
	_, err = db.ExecContext(ctx, `UPDATE libraries SET last_scan_at=? WHERE id=?`, now, lib.ID)
	return res, err
}

// pickMoveCandidate returns the index within cands that best matches f: a
// same-size candidate whose base file name matches f's, or else the first
// same-size candidate. Returns -1 when no candidate has a matching size.
func pickMoveCandidate(cands []*existing, f found) int {
	base := path.Base(f.rel)
	idx := -1
	for i, c := range cands {
		if c.size != f.size {
			continue
		}
		if idx == -1 {
			idx = i
		}
		if path.Base(c.rel) == base {
			return i
		}
	}
	return idx
}

// update probes a file and writes its media info, then reindexes it. The
// real mtime is only committed once the probe and the reindex both succeed;
// until then the row keeps (or reverts to) the mtime=0 sentinel so a later
// scan retries it instead of treating it as unchanged.
func (sc *Scanner) update(ctx context.Context, lib Library, id int64, f found, res *ScanResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	abs := filepath.Join(lib.Root, filepath.FromSlash(f.rel))
	fp, _ := media.Fingerprint(abs, f.size)
	info, perr := sc.Prober.Probe(ctx, abs)
	if err := ctx.Err(); err != nil {
		return err
	}
	if perr != nil {
		res.Broken++
		// Keep the existing tag/codec/duration columns: a temporary probe
		// failure (disk glitch, USB hiccup) must not erase good metadata.
		// AND status!='trashed': id was probed off a snapshot taken before
		// the walk; a concurrent trash.Move since then must not be
		// clobbered by writing probe results for the pre-trash file.
		_, err := sc.Store.DB.ExecContext(ctx, `UPDATE tracks SET size=?, mtime=0, fingerprint=COALESCE(NULLIF(?,''),fingerprint),
			broken=1, broken_reason=? WHERE id=? AND status!='trashed'`, f.size, fp, perr.Error(), id)
		return err
	}
	// Tags are stored cleaned (cleanTag): GBK mojibake repaired, garbled or
	// placeholder values emptied so the display falls back to the file name.
	// A new or changed file has no loudness yet: clearing it queues the file
	// for the background loudness worker.
	r, err := sc.Store.DB.ExecContext(ctx, `UPDATE tracks SET size=?, mtime=0, fingerprint=COALESCE(NULLIF(?,''),fingerprint),
		duration_ms=?, codec=?, bitrate=?, sample_rate=?, lossless=?, tag_title=?, tag_artist=?, tag_album=?,
		tag_album_artist=?, tag_year=NULLIF(?,0), track_no=NULLIF(?,0), disc_no=NULLIF(?,0), broken=0, broken_reason='',
		loudness_lufs=NULL, true_peak_db=NULL, loudness_checked_at=NULL WHERE id=? AND status!='trashed'`,
		f.size, fp, info.DurationMS, info.Codec, info.BitrateKbps, info.SampleRate, info.Lossless,
		cleanTag(info.Title), cleanTag(info.Artist), cleanTag(info.Album), cleanTag(info.AlbumArtist), info.Year, info.TrackNo, info.DiscNo, id)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		// Trashed mid-scan (see above): leave the row untouched and skip the
		// reindex, which would otherwise index a trashed track for search.
		return nil
	}
	sc.applyFolderTags(ctx, id, f.rel)
	if err := sc.Store.Reindex(ctx, id); err != nil {
		return err // mtime stays 0: a later scan retries the probe and the reindex
	}
	_, err = sc.Store.DB.ExecContext(ctx, `UPDATE tracks SET mtime=? WHERE id=?`, f.mtime, id)
	return err
}

func underAny(rel string, prefixes []string) bool {
	for _, p := range prefixes {
		if p == "./" || strings.HasPrefix(rel, p) {
			return true
		}
	}
	return false
}
