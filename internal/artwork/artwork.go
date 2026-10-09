// Package artwork finds song covers — the picture embedded in the file, an
// image in its folder, later online sources — and caches them as square
// 300 px and 1000 px JPEGs under <data dir>/cache/artwork. Music files and
// folders are never written.
package artwork

import (
	"context"
	"errors"

	"github.com/aaronsuns/lark-server/internal/lyrics"
)

// Sizes are the cached cover sizes (px, square).
var Sizes = []int{300, 1000}

var (
	ErrNotFound = errors.New("artwork: track not found")
	ErrNoCover  = errors.New("artwork: no cover")
	ErrBadSize  = errors.New("artwork: size must be 300 or 1000")
)

// Found is one picture a provider offered.
type Found struct {
	Source, ExternalID string
	Title, Artist      string // the provider's spelling (for lyrics.Match); "" = the file's own picture
	DurationS          int
	URL                string // online image (https only)
	Path               string // folder: the image; embedded: the audio file
	Stream             int    // embedded: ffmpeg index of the attached picture; -1 otherwise
}

// Provider is one cover source. Find must honour ctx; a clean "nothing here"
// is (nil, nil), anything that went wrong is an error.
type Provider interface {
	Name() string
	Find(ctx context.Context, q lyrics.Query) ([]Found, error)
}

// Local is implemented by providers that read only the track's own file or
// folder. They are asked first, one at a time; their clean "nothing here"
// counts as a miss only while no online provider is enabled.
type Local interface{ Local() bool }

func isLocal(p Provider) bool {
	l, ok := p.(Local)
	return ok && l.Local()
}

// PictureProber finds an audio file's embedded cover (media.FFprobe).
type PictureProber interface {
	AttachedPicture(ctx context.Context, path string) (stream int, ok bool, err error)
}

// Imager writes src (an image, or stream ≥ 0 of an audio file) as a square,
// centre-cropped JPEG of at most size px (never upscaled) to dst.
type Imager interface {
	Square(ctx context.Context, src string, stream int, dst string, size int) error
}
