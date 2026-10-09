package artwork

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/aaronsuns/lark-server/internal/lyrics"
)

// Embedded offers the picture attached to the audio file itself (YouTube
// downloads embed their thumbnail).
type Embedded struct{ Probe PictureProber }

// Folder offers cover.*, folder.* or front.* (jpg/jpeg/png, any case) next
// to the song, in that order.
type Folder struct{}

func (*Embedded) Name() string { return "embedded" }
func (*Embedded) Local() bool  { return true }

func (e *Embedded) Find(ctx context.Context, q lyrics.Query) ([]Found, error) {
	if q.Path == "" || e.Probe == nil {
		return nil, nil
	}
	idx, ok, err := e.Probe.AttachedPicture(ctx, q.Path)
	if err != nil || !ok {
		return nil, err
	}
	return []Found{{Source: "embedded", ExternalID: "picture", Path: q.Path, Stream: idx}}, nil
}

var folderNames = []string{"cover", "folder", "front"}
var folderExts = map[string]bool{".jpg": true, ".jpeg": true, ".png": true}

func (Folder) Name() string { return "folder" }
func (Folder) Local() bool  { return true }

func (Folder) Find(ctx context.Context, q lyrics.Query) ([]Found, error) {
	if q.Path == "" {
		return nil, nil
	}
	dir := filepath.Dir(q.Path)
	ents, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for _, want := range folderNames {
		for _, e := range ents {
			n := e.Name()
			ext := filepath.Ext(n)
			if e.Type().IsRegular() && folderExts[strings.ToLower(ext)] && strings.EqualFold(strings.TrimSuffix(n, ext), want) {
				return []Found{{Source: "folder", ExternalID: n, Path: filepath.Join(dir, n), Stream: -1}}, nil
			}
		}
	}
	return nil, nil
}
