package recommend

import (
	"regexp"
	"strings"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

// blockedCJK are compilation / long-form markers matched as substrings.
var blockedCJK = []string{"合集", "串烧", "串燒", "大全", "全集", "1小时", "1小時"}

// blockedWords are matched as whole words, case-insensitive ("Remix" and
// "Hourglass" stay).
var blockedWords = regexp.MustCompile(`(?i)\b(mix|playlist|hours?|live\s*stream)\b`)

// BlockedTitle reports a compilation, playlist, hour-long or live-stream title.
func BlockedTitle(title string) bool {
	for _, w := range blockedCJK {
		if strings.Contains(title, w) {
			return true
		}
	}
	return blockedWords.MatchString(title)
}

// playable: a single song worth suggesting — a real video id, 60–600 s
// (unknown durations are almost always streams), not live, not blocked.
func playable(v ytdlp.Video) bool {
	return ytdlp.IsVideoID(v.ID) && !v.Live && v.DurationS >= minDurationS && v.DurationS <= maxDurationS && !BlockedTitle(v.Title)
}
