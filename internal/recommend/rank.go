package recommend

import (
	"sort"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// suggestion: seed (index into the seeds) suggested video at position pos of
// its Mix (or of its Last.fm list when lastfm).
type suggestion struct {
	seed   int
	video  ytdlp.Video
	pos    int
	lastfm bool
}

type ranked struct {
	video  ytdlp.Video
	score  float64
	reason int // index of the strongest seed that suggested it
	pos    int // best position in any list
}

// rank scores each video: the summed weight of the distinct seeds that
// suggested it, +0.5 when Last.fm and a Mix agree on it. Ties go to the
// better list position, then the video id (stable). The reason is the
// heaviest suggesting seed (the earlier one on a tie).
func rank(seeds []Seed, sugg []suggestion) []ranked {
	type acc struct {
		r          ranked
		seen       map[int]bool
		mix, lastf bool
	}
	by := map[string]*acc{}
	var order []string
	for _, sg := range sugg {
		a := by[sg.video.ID]
		if a == nil {
			a = &acc{r: ranked{video: sg.video, reason: sg.seed, pos: sg.pos}, seen: map[int]bool{}}
			by[sg.video.ID] = a
			order = append(order, sg.video.ID)
		}
		if sg.lastfm {
			a.lastf = true
		} else {
			a.mix = true
		}
		a.r.pos = min(a.r.pos, sg.pos)
		if a.seen[sg.seed] {
			continue
		}
		a.seen[sg.seed] = true
		w := seeds[sg.seed].Weight
		a.r.score += w
		if cur := seeds[a.r.reason].Weight; w > cur || (w == cur && sg.seed < a.r.reason) {
			a.r.reason = sg.seed
		}
	}
	out := make([]ranked, 0, len(order))
	for _, id := range order {
		a := by[id]
		if a.mix && a.lastf {
			a.r.score += lastfmAgreement
		}
		out = append(out, a.r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if a.pos != b.pos {
			return a.pos < b.pos
		}
		return a.video.ID < b.video.ID
	})
	return out
}
