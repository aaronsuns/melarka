package lyrics

import (
	"strings"
	"testing"
)

func TestMatch(t *testing.T) {
	q := func(title, artist string, d int) Query { return Query{Title: title, Artist: artist, DurationS: d} }
	c := func(title, artist string, d int) Candidate {
		return Candidate{Title: title, Artist: artist, DurationS: d}
	}
	cases := []struct {
		q    Query
		c    Candidate
		want bool
	}{
		{q("甜蜜蜜", "邓丽君", 211), c("甜蜜蜜", "鄧麗君", 210), true}, // traditional vs simplified
		{q("Faded", "Alan Walker", 212), c("Faded (Official Music Video)", "Alan Walker", 0), true},
		{q("甜蜜蜜 (Live)", "邓丽君", 0), c("甜蜜蜜", "邓丽君", 0), true},
		{q("甜蜜蜜【MV】", "邓丽君", 0), c("甜蜜蜜", "邓丽君, 周杰伦", 0), true},
		{q("甜蜜蜜", "邓丽君", 211), c("甜蜜蜜", "毛辣角", 228), false},        // other artist, 17 s off
		{q("甜蜜蜜", "邓丽君", 211), c("甜蜜蜜", "毛辣角", 241), false},        // other artist, 30 s off
		{q("甜蜜蜜", "邓丽君", 211), c("甜蜜蜜", "邓丽君", 215), false},        // same song, 4 s off
		{q("甜蜜蜜", "邓丽君", 211), c("甜蜜蜜", "Teresa Teng", 210), true}, // romanized artist, durations agree
		{q("甜蜜蜜", "邓丽君", 0), c("甜蜜蜜", "Teresa Teng", 210), false},  // ...but not without a duration
		{q("爱", "X", 200), c("爱你一万年", "X", 200), false},            // too-short containment
		{q("River Flows In You", "", 180), c("River Flows in You", "Yiruma", 181), true},
		{q("Anything", "Anyone", 100), c("", "", 0), true}, // the file's own lyrics
	}
	for _, tc := range cases {
		if got := Match(tc.q, tc.c); got != tc.want {
			t.Errorf("%+v vs %+v = %v", tc.q, tc.c, got)
		}
	}
}

func TestNorm(t *testing.T) {
	if Norm("鄧麗君") != Norm("邓丽君") || Norm("邓丽君") == "" {
		t.Errorf("%q vs %q", Norm("鄧麗君"), Norm("邓丽君"))
	}
	if got := Norm("Faded (Official Music Video)"); got != "faded" {
		t.Errorf("%q", got)
	}
}

func TestRank(t *testing.T) {
	cs := []Candidate{{Source: "lrclib", ExternalID: "1"}, {Source: "kugou", ExternalID: "2", Synced: true}, {Source: "netease", ExternalID: "3", Synced: true}}
	Rank(Query{}, cs, []string{"embedded", "lrclib", "netease", "qq", "kugou"})
	if cs[0].ExternalID != "3" || cs[1].ExternalID != "2" || cs[2].ExternalID != "1" {
		t.Fatalf("%v", cs)
	}
}

// After synced-first, the duration closest to the track wins (a version
// mismatch is the usual cause of lyrics running early or late); unknown
// durations go last, then provider order.
func TestRankPrefersTheClosestDuration(t *testing.T) {
	q := Query{DurationS: 211}
	cs := []Candidate{
		{Source: "lrclib", ExternalID: "off3", DurationS: 214, Synced: true},
		{Source: "netease", ExternalID: "unknown", Synced: true},
		{Source: "kugou", ExternalID: "exact", DurationS: 211, Synced: true},
		{Source: "qq", ExternalID: "off1", DurationS: 210, Synced: true},
		{Source: "lrclib", ExternalID: "plain-exact", DurationS: 211},
	}
	Rank(q, cs, []string{"embedded", "lrclib", "netease", "qq", "kugou"})
	var got []string
	for _, c := range cs {
		got = append(got, c.ExternalID)
	}
	if strings.Join(got, ",") != "exact,off1,off3,unknown,plain-exact" {
		t.Fatalf("%v", got)
	}
	// Broad: among strict matches the same order applies.
	bq := Query{Title: "甜蜜蜜", Artist: "邓丽君", DurationS: 211}
	bs := []Candidate{
		{Source: "lrclib", ExternalID: "off2", Title: "甜蜜蜜", Artist: "邓丽君", DurationS: 213, Synced: true},
		{Source: "kugou", ExternalID: "exact", Title: "甜蜜蜜", Artist: "邓丽君", DurationS: 211, Synced: true},
	}
	RankBroad(bq, bs, []string{"embedded", "lrclib", "netease", "qq", "kugou"})
	if bs[0].ExternalID != "exact" {
		t.Fatalf("broad strict: %v", bs)
	}
}

func TestMatchBroad(t *testing.T) {
	q := func(title, artist string, d int) Query { return Query{Title: title, Artist: artist, DurationS: d} }
	c := func(title, artist string, d int) Candidate {
		return Candidate{Title: title, Artist: artist, DurationS: d}
	}
	cases := []struct {
		q    Query
		c    Candidate
		want bool
	}{
		{q("甜蜜蜜", "邓丽君", 211), c("甜蜜蜜", "鄧麗君", 210), true},                     // strict matches stay matches
		{q("甜蜜蜜", "邓丽君", 211), c("甜蜜蜜", "毛辣角", 228), true},                     // any artist, 17 s off
		{q("甜蜜蜜", "邓丽君", 211), c("甜蜜蜜", "邓丽君", 230), true},                     // 19 s off
		{q("甜蜜蜜", "邓丽君", 211), c("甜蜜蜜", "邓丽君", 241), false},                    // 30 s off
		{q("甜蜜蜜", "邓丽君", 400), c("甜蜜蜜", "邓丽君", 211), true},                     // a long video: duration ignored
		{q("甜蜜蜜", "邓丽君", 211), c("甜蜜蜜", "毛辣角", 0), true},                       // candidate without a duration
		{q("甜蜜蜜", "", 0), c("甜蜜蜜", "Teresa Teng", 210), true},                  // title only
		{q("一生所爱 Love In A Life Time 电影插曲", "", 0), c("一生所爱", "卢冠廷", 0), true}, // containment, any length
		{q("一生所爱", "", 0), c("一生所爱 (电影《大话西游》插曲)", "卢冠廷", 0), true},
		{q("River Flows In You", "", 0), c("River Flow In You", "Yiruma", 0), true}, // similar enough
		{q("甜蜜蜜", "邓丽君", 211), c("月亮代表我的心", "邓丽君", 211), false},                     // another song
		{q("爱", "X", 200), c("爱你一万年", "X", 200), false},                             // too short to contain
		{q("Anything", "Anyone", 100), c("", "", 0), true},                          // the file's own lyrics
	}
	for _, tc := range cases {
		if got := MatchBroad(tc.q, tc.c); got != tc.want {
			t.Errorf("%+v vs %+v = %v", tc.q, tc.c, got)
		}
	}
}

func TestRankBroad(t *testing.T) {
	q := Query{Title: "甜蜜蜜", Artist: "邓丽君", DurationS: 211}
	cs := []Candidate{
		{Source: "kugou", ExternalID: "far", Title: "甜蜜蜜", Artist: "毛辣角", DurationS: 229, Synced: true},
		{Source: "qq", ExternalID: "loose", Title: "甜蜜蜜 (DJ版)", Artist: "某人", DurationS: 0, Synced: true},
		{Source: "netease", ExternalID: "near", Title: "甜蜜蜜", Artist: "毛辣角", DurationS: 215, Synced: true},
		{Source: "lrclib", ExternalID: "strict", Title: "甜蜜蜜", Artist: "邓丽君", DurationS: 211},
	}
	RankBroad(q, cs, []string{"embedded", "lrclib", "netease", "qq", "kugou"})
	var got []string
	for _, c := range cs {
		got = append(got, c.ExternalID)
	}
	if strings.Join(got, ",") != "strict,near,far,loose" {
		t.Fatalf("%v", got)
	}
}
