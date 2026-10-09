package lyrics

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Embedded offers the file's own lyrics: a sidecar "<name>.lrc" next to the
// audio file, else a lyrics tag inside it. Both carry Title "" (the file's
// own lyrics always match).
type Embedded struct {
	Tags TagReader // nil: sidecar only
}

func (*Embedded) Name() string { return "embedded" }
func (*Embedded) Local() bool  { return true }

func (e *Embedded) Search(ctx context.Context, q Query) ([]Candidate, error) {
	if q.Path == "" {
		return nil, nil
	}
	if text, err := readSidecar(q.Path); err != nil {
		return nil, err
	} else if text != "" {
		_, synced := ParseLRC(text)
		return []Candidate{{Source: "embedded", ExternalID: "sidecar", Synced: synced, Text: text}}, nil
	}
	if e.Tags == nil {
		return nil, nil
	}
	tags, err := e.Tags.Tags(ctx, q.Path)
	if err != nil {
		return nil, err
	}
	if text := lyricsTag(tags); text != "" {
		_, synced := ParseLRC(text)
		return []Candidate{{Source: "embedded", ExternalID: "tag", Synced: synced, Text: text}}, nil
	}
	return nil, nil
}

func readSidecar(audio string) (string, error) {
	base := strings.TrimSuffix(audio, filepath.Ext(audio))
	for _, ext := range []string{".lrc", ".LRC"} {
		f, err := os.Open(base + ext)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		b, err := io.ReadAll(io.LimitReader(f, maxBody))
		f.Close()
		if err != nil {
			return "", err
		}
		if s := strings.TrimSpace(string(b)); s != "" {
			return s, nil
		}
	}
	return "", nil
}

// lyricsTag finds the lyrics among lowercased ffprobe tags: "lyrics" or
// "lyrics-<lang>" (ID3 USLT), "unsyncedlyrics" (Vorbis/FLAC), "uslt". Keys
// are tried in a fixed order (bare names first, then lyrics-<lang> sorted),
// so a file with several languages always yields the same text.
func lyricsTag(tags map[string]string) string {
	keys := []string{"lyrics", "unsyncedlyrics", "uslt"}
	var langs []string
	for k := range tags {
		if strings.HasPrefix(k, "lyrics-") {
			langs = append(langs, k)
		}
	}
	sort.Strings(langs)
	for _, k := range append(keys, langs...) {
		if v := strings.TrimSpace(tags[k]); v != "" {
			return v
		}
	}
	return ""
}
