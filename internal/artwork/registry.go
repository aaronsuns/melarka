package artwork

import (
	"fmt"

	"github.com/aaronsuns/lark-server/internal/lyrics"
)

// Deps are the outside services providers may need (HTTP nil = the lyrics default client).
type Deps struct {
	Probe PictureProber
	HTTP  lyrics.HTTPDoer
}

var registry = map[string]func(Deps) Provider{
	"embedded": func(d Deps) Provider { return &Embedded{Probe: d.Probe} },
	"folder":   func(Deps) Provider { return Folder{} },
	"itunes":   func(d Deps) Provider { return &ITunes{HTTP: d.HTTP} },
	"netease":  func(d Deps) Provider { return &NetEase{Search: &lyrics.NetEase{HTTP: d.HTTP}} },
	"qq":       func(d Deps) Provider { return &QQ{Search: &lyrics.QQ{HTTP: d.HTTP}} },
}

// Build returns the named providers in order (config artwork.providers).
func Build(names []string, d Deps) ([]Provider, error) {
	seen := map[string]bool{}
	out := make([]Provider, 0, len(names))
	for _, n := range names {
		mk, ok := registry[n]
		if !ok {
			return nil, fmt.Errorf("unknown artwork provider %q", n)
		}
		if seen[n] {
			return nil, fmt.Errorf("artwork provider %q listed twice", n)
		}
		seen[n] = true
		out = append(out, mk(d))
	}
	return out, nil
}
