// Package fileutil holds small file helpers shared by the channel and
// preview stores.
package fileutil

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// RelInside returns p relative to root (slash-separated) if p lies
// strictly inside root.
func RelInside(root, p string) (string, bool) {
	if !filepath.IsAbs(p) {
		return "", false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// link is os.Link (a test swaps it to force the copy path).
var link = os.Link

// Move moves src to dst, creating dst's directory. It never overwrites
// (fs.ErrExist), atomically: the file is hard-linked into place (link fails
// when dst exists) and only then is src removed. Across filesystems (the
// preview disk and the music disk differ), or where hard links are not
// supported, it copies to dst+".lark-move", syncs, links that into place
// the same way and only then removes src.
func Move(src, dst string) error {
	if _, err := os.Lstat(src); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	err := link(src, dst)
	if err == nil {
		return os.Remove(src)
	}
	if errors.Is(err, fs.ErrExist) {
		return fs.ErrExist
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".lark-move"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = placeNew(tmp, dst)
	}
	os.Remove(tmp) // after a link: the second name; after a failure: the partial copy
	if err != nil {
		return err
	}
	return os.Remove(src)
}

// placeNew gives the finished temp file its final name without ever
// replacing an existing dst: a hard link where the filesystem has them,
// else (no hard links at all) a rename after a last existence check.
func placeNew(tmp, dst string) error {
	err := os.Link(tmp, dst)
	if err == nil || errors.Is(err, fs.ErrExist) {
		return err
	}
	if _, serr := os.Lstat(dst); serr == nil {
		return fs.ErrExist
	}
	return os.Rename(tmp, dst)
}

// FreeBytes is the disk space available to Lark under dir (statfs).
func FreeBytes(dir string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}
