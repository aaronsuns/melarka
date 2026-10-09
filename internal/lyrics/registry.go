package lyrics

import "fmt"

// Deps are the outside services providers may need.
type Deps struct {
	Tags TagReader
	HTTP HTTPDoer
}

var registry = map[string]func(Deps) Provider{
	"embedded": func(d Deps) Provider { return &Embedded{Tags: d.Tags} },
	"lrclib":   func(d Deps) Provider { return &LRCLIB{HTTP: d.HTTP} },
	"netease":  func(d Deps) Provider { return &NetEase{HTTP: d.HTTP} },
	"qq":       func(d Deps) Provider { return &QQ{HTTP: d.HTTP} },
	"kugou":    func(d Deps) Provider { return &Kugou{HTTP: d.HTTP} },
}

// Build returns the named providers in order (config lyrics.providers).
func Build(names []string, d Deps) ([]Provider, error) {
	seen := map[string]bool{}
	out := make([]Provider, 0, len(names))
	for _, n := range names {
		mk, ok := registry[n]
		if !ok {
			return nil, fmt.Errorf("unknown lyrics provider %q", n)
		}
		if seen[n] {
			return nil, fmt.Errorf("lyrics provider %q listed twice", n)
		}
		seen[n] = true
		out = append(out, mk(d))
	}
	return out, nil
}
