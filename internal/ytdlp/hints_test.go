package ytdlp

import (
	"strings"
	"testing"
)

func TestSplitSingers(t *testing.T) {
	for in, want := range map[string]string{
		"盧冠廷 莫文蔚":     "盧冠廷|莫文蔚",
		"周华健 和 齐豫":    "周华健|齐豫",
		"张学友 & 汤宝如":   "张学友|汤宝如",
		"刘德华、、陈慧琳":    "刘德华|陈慧琳",
		"周华健和齐豫":      "周华健|齐豫",
		"和平":          "和平",
		"Alan Walker": "Alan Walker",
	} {
		if got := strings.Join(SplitSingers(in), "|"); got != want {
			t.Errorf("SplitSingers(%q) = %q, want %q", in, got, want)
		}
	}
}

// BracketPair only splits; deciding which side is the song is the lyrics
// query builder's job.
func TestBracketPair(t *testing.T) {
	for raw, want := range map[string]string{
		"盧冠廷 莫文蔚【一生所愛 Love In A Life Time】電影「大话西游」插曲【MV】": "盧冠廷 莫文蔚|一生所愛|",
		"后来【刘若英】":            "后来|刘若英|",
		"怀旧金曲【千千阙歌】陈慧娴":      "怀旧金曲|千千阙歌|陈慧娴",
		"【MV】周杰伦【晴天】":        "周杰伦|晴天|",
		"Alan Walker【Faded】": "",
		"邓丽君 - 甜蜜蜜":          "",
	} {
		l, b, r, ok := BracketPair(raw)
		got := ""
		if ok {
			got = l + "|" + b + "|" + r
		}
		if got != want {
			t.Errorf("BracketPair(%q) = %q, want %q", raw, got, want)
		}
	}
	for s, want := range map[string]bool{"粤语": true, "怀旧金曲": true, "官方": true, "无损": true, "经典老歌": true,
		"钢琴曲": true, "第1集": true, "KTV": true, "刘若英": false, "盧冠廷 莫文蔚": false, "Alan Walker": false} {
		if got := Filler(s); got != want {
			t.Errorf("Filler(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestWorkRestAndLeadingQuote(t *testing.T) {
	for raw, want := range map[string]string{
		"电影《英雄本色》主题曲【粤语】当年情":  "【粤语】当年情",
		"电影《大话西游》插曲【一生所爱】卢冠廷": "【一生所爱】卢冠廷",
		"《新白娘子传奇》插曲 合集":       "合集",
		"刘欢《好汉歌》电视剧主题曲":       "", // the quote after a singer is the song: no work
		"邓丽君 - 甜蜜蜜【MV】":       "",
	} {
		got, ok := WorkRest(raw)
		if !ok {
			got = ""
		}
		if got != want {
			t.Errorf("WorkRest(%q) = %q, want %q", raw, got, want)
		}
	}
	if x, rest, ok := LeadingQuote("【一生所爱】卢冠廷"); !ok || x != "一生所爱" || rest != "卢冠廷" {
		t.Errorf("LeadingQuote = %q %q %v", x, rest, ok)
	}
	if _, _, ok := LeadingQuote("卢冠廷《一生所爱》"); ok {
		t.Error("LeadingQuote took a quote that does not lead")
	}
}

func TestFillerOnly(t *testing.T) {
	for s, want := range map[string]bool{"合集": true, "儿歌合集 30分钟": true, "中文字幕": true, "经典老歌": true,
		"KTV": true, "1993": true, "高清修复版": true, "粤语": true, "怀旧": true,
		"当年情": false, "你的故事": false, "一生所爱": false, "Faded": false} {
		if got := FillerOnly(s); got != want {
			t.Errorf("FillerOnly(%q) = %v, want %v", s, got, want)
		}
	}
}
