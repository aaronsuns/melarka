package channels

import (
	"sort"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// seedMix is one seed video's YouTube Mix and how much the seed counts.
type seedMix struct {
	seedVideo string
	weight    float64
	videos    []ytdlp.Video
}

type mixScore struct {
	v     ytdlp.Video
	score float64
	seed  int // index (into mixes) of the heaviest suggesting seed
	pos   int // best position in any Mix holding it
}

// rankMixVideos scores each video by the summed weight of the distinct seeds
// whose Mix holds it; ties by best position, then id. keep filters; a seed's
// own video never counts from its own Mix. The first entry seen of a video is
// the one returned.
func rankMixVideos(mixes []seedMix, keep func(ytdlp.Video) bool) []mixScore {
	type acc struct {
		mixScore
		from map[int]bool
		best float64 // weight of mixes[seed]
	}
	byID := map[string]*acc{}
	for i, m := range mixes {
		for pos, v := range m.videos {
			if v.ID == m.seedVideo || !keep(v) {
				continue
			}
			a := byID[v.ID]
			if a == nil {
				a = &acc{mixScore: mixScore{v: v, seed: i, pos: pos}, from: map[int]bool{}, best: m.weight}
				byID[v.ID] = a
			}
			if !a.from[i] {
				a.from[i] = true
				a.score += m.weight
				if m.weight > a.best {
					a.seed, a.best = i, m.weight
				}
			}
			a.pos = min(a.pos, pos)
		}
	}
	out := make([]mixScore, 0, len(byID))
	for _, a := range byID {
		out = append(out, a.mixScore)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if a.pos != b.pos {
			return a.pos < b.pos
		}
		return a.v.ID < b.v.ID
	})
	return out
}
