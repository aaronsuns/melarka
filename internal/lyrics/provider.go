// Package lyrics finds, stores and serves song lyrics: providers behind one
// interface (the file's own embedded/sidecar lyrics, LRCLIB, …), LRC parsing,
// normalized title/artist matching, and a lookup service with a total time
// budget and miss memory.
package lyrics

import (
	"context"
	"net/http"
)

// Query describes the track a provider is asked about.
type Query struct {
	TrackID              int64
	Title, Artist, Album string // display values (override ?? tag ?? path)
	DurationS            int
	Path                 string // absolute file path; only the embedded provider reads it
	ChannelArtist        bool   // Artist is just the YouTube channel the track was downloaded from
	VideoTitle           string // raw title of the YouTube video the track came from ("" if none); search hints only
	Channel              string // its YouTube channel ("" if none); search hints only
}

// Candidate is one set of lyrics a provider offered.
type Candidate struct {
	Source, ExternalID string
	Title, Artist      string // as the provider spells them; Title "" = the file's own lyrics (always matches)
	DurationS          int    // 0 = unknown
	Synced             bool
	Text               string // LRC when Synced, else plain text
}

// Provider is one lyrics source. Search must honour ctx; a clean "nothing
// here" is (nil, nil), anything that went wrong is an error.
type Provider interface {
	Name() string
	Search(ctx context.Context, q Query) ([]Candidate, error)
}

// Local is optionally implemented by providers that read only the track's
// own file (embedded/sidecar). Their clean "nothing here" says nothing about
// whether lyrics exist elsewhere, so it doesn't count as an answer for the
// 7-day miss memory while any remote provider is enabled.
type Local interface {
	Local() bool
}

func isLocal(p Provider) bool {
	l, ok := p.(Local)
	return ok && l.Local()
}

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// TagReader reads an audio file's metadata tags (media.FFprobe in production).
type TagReader interface {
	Tags(ctx context.Context, path string) (map[string]string, error)
}
