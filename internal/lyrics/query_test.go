package lyrics

import (
	"fmt"
	"strings"
	"testing"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

func TestSearchQueriesCleanYouTubeTitles(t *testing.T) {
	cases := []struct {
		title, artist string
		channel       bool
		want          [][2]string // {title, artist} per query, best first
	}{
		{"《西游记》插曲 女儿情 吴静 高清", "Roy Hoo", true, [][2]string{{"女儿情", "吴静"}, {"女儿情", ""}, {"女儿情", "Roy Hoo"}}},
		{"信仰", "【纯享】#张信哲 深情演绎", false, [][2]string{{"信仰", "张信哲"}}},
		{"当 MV (動力火車-當-還珠格格主題曲)", "动力火车", false, [][2]string{{"当", "动力火车"}}},
		{"小寶貝 [歌詞字幕][完整高清音質] Summer Play Band - My Little Baby", "夏天播放", false, [][2]string{{"小寶貝", "夏天播放"}}},
		// The un-split raw title (channel artist): the song inside 《》, the singer from the hashtag.
		{"【纯享】#张信哲 深情演绎《信仰》张信哲的嗓音太绝了 | 我是歌手4", "湖南卫视芒果TV官方频道", true,
			[][2]string{{"信仰", "张信哲"}, {"信仰", ""}, {"信仰", "湖南卫视芒果TV"}}},
		{"月亮代表我的心 邓丽君", "邓丽君", false, [][2]string{{"月亮代表我的心", "邓丽君"}}},
		// Ordinary titles are left alone.
		{"甜蜜蜜", "邓丽君", false, [][2]string{{"甜蜜蜜", "邓丽君"}}},
		{"Faded", "Alan Walker", false, [][2]string{{"Faded", "Alan Walker"}}},
		{"Song (feat. B)", "Chan", false, [][2]string{{"Song", "Chan"}}},
		{"Oliver Twist", "Live Band", false, [][2]string{{"Oliver Twist", "Live Band"}}},
		// Bare live / audio / lyrics are real words in real titles (only bracketed "(Live)" etc. is noise).
		{"Live Forever", "Oasis", false, [][2]string{{"Live Forever", "Oasis"}}},
		{"Live Forever (Remastered)", "Oasis", false, [][2]string{{"Live Forever", "Oasis"}}},
		{"Audio Love", "Chan", false, [][2]string{{"Audio Love", "Chan"}}},
		{"Lyrics of Love", "Chan", false, [][2]string{{"Lyrics of Love", "Chan"}}},
		{"Faded (Live) [Official Audio]", "Alan Walker", false, [][2]string{{"Faded", "Alan Walker"}}},
		// Nothing but noise: fall back to the original, never an empty search.
		// With no singer in the title, the channel artist comes before title alone.
		{"【MV】", "Chan", true, [][2]string{{"【MV】", "Chan"}, {"【MV】", ""}}},
		{"HD 4K", "Chan", false, [][2]string{{"HD 4K", "Chan"}}},
		{"", "", false, [][2]string{{"", ""}}},
		// A leading "Artist - " is the artist, and title alone is never asked first
		// while an artist is known (Match takes any similar title for an empty artist).
		{"Coldplay - Fix You (Official Video)", "Coldplay", true, [][2]string{{"Fix You", "Coldplay"}, {"Fix You", ""}}},
		{"动力火车 - 当 MV (動力火車-當-還珠格格主題曲)", "动力火车官方频道", true, [][2]string{{"当", "动力火车"}, {"当", ""}}},
		{"Fix You - Coldplay", "Coldplay", false, [][2]string{{"Fix You", "Coldplay"}}},
		{"Hello", "Adele", true, [][2]string{{"Hello", "Adele"}, {"Hello", ""}}},
		{"Fix-You", "Coldplay", false, [][2]string{{"Fix-You", "Coldplay"}}},
		{"甜蜜蜜 现场版", "邓丽君", false, [][2]string{{"甜蜜蜜", "邓丽君"}}},
		{"甜蜜蜜 現場版", "鄧麗君", false, [][2]string{{"甜蜜蜜", "鄧麗君"}}},
		// A download from before the artist cleanup: 原唱的歌曲 is not the singer.
		{"天边", "布仁巴雅尔原唱的歌曲", false, [][2]string{{"天边", "布仁巴雅尔"}}},
		{"天边", "布仁巴雅尔的歌曲", false, [][2]string{{"天边", "布仁巴雅尔"}}},
		// The work in 「…」 or with only a kind word before it is context, not the song.
		{"电视剧「神雕侠侣」主题曲 天下有情人", "周华健", false, [][2]string{{"天下有情人", "周华健"}}},
		{"电影《大话西游》 一生所爱", "卢冠廷", false, [][2]string{{"一生所爱", "卢冠廷"}}},
		{"一生所愛 Love In A Life Time", "盧冠廷, 莫文蔚", false, [][2]string{{"一生所愛", "盧冠廷, 莫文蔚"}}},
		// A bare trailing 的 can be the name itself.
		{"Song", "小鱼的", false, [][2]string{{"Song", "小鱼的"}}},
	}
	for _, c := range cases {
		q := Query{TrackID: 7, Title: c.title, Artist: c.artist, ChannelArtist: c.channel, DurationS: 200, Path: "/x.m4a"}
		got := SearchQueries(q)
		var pairs [][2]string
		for _, g := range got {
			if g.TrackID != 7 || g.DurationS != 200 || g.Path != "/x.m4a" {
				t.Errorf("%q: other fields not kept: %+v", c.title, g)
			}
			pairs = append(pairs, [2]string{g.Title, g.Artist})
		}
		if fmt.Sprint(pairs) != fmt.Sprint(c.want) {
			t.Errorf("SearchQueries(%q, %q, channel=%v) = %v, want %v", c.title, c.artist, c.channel, pairs, c.want)
		}
		if q.Title != c.title || q.Artist != c.artist {
			t.Errorf("input changed: %+v", q)
		}
	}
	// 小寶貝 is searched as written; providers and Match fold scripts via pinyin.
	if Norm("小寶貝") != Norm("小宝贝") {
		t.Fatal("pinyin fold")
	}
}

// "A【B】" titles (shown, or the raw video title) add searches in both orders
// after the first query — the right (song, singer) pair is among them —
// without changing the first query or ever searching a guess by title alone.
func TestSearchQueriesBracketVariants(t *testing.T) {
	const ysa = "盧冠廷 莫文蔚【一生所愛 Love In A Life Time】電影「大话西游」插曲【创作Creative MV Lyrics】「一生所爱隐约在白云，外苦海翻起爱恨，在世间难逃避命运」原创remix"
	cases := []struct {
		q    Query
		want [2]string
	}{
		{Query{Title: "后来【刘若英】", Artist: "Chan", ChannelArtist: true}, [2]string{"后来", "刘若英"}},
		{Query{Title: "孤勇者【陈奕迅】", Artist: "Chan", ChannelArtist: true}, [2]string{"孤勇者", "陈奕迅"}},
		{Query{Title: "粤语【千千阙歌】", Artist: "Chan", ChannelArtist: true}, [2]string{"千千阙歌", "Chan"}},
		{Query{Title: "千千阙歌【粤语】", Artist: "Chan", ChannelArtist: true}, [2]string{"千千阙歌", "Chan"}},
		{Query{Title: "怀旧金曲【千千阙歌】陈慧娴", Artist: "Chan", ChannelArtist: true}, [2]string{"千千阙歌", "陈慧娴"}},
		{Query{Title: "月亮代表我的心【邓丽君】经典老歌", Artist: "Chan", ChannelArtist: true}, [2]string{"月亮代表我的心", "邓丽君"}},
		{Query{Title: ysa, Artist: "華音殿Music Channel", ChannelArtist: true}, [2]string{"一生所愛", "盧冠廷"}},
		// Shown names as CleanTitleV2 stored them; the video title still knows the song.
		{Query{Title: "大话西游", Artist: "盧冠廷 莫文蔚【一生所愛 Love In A Life Time】電影", VideoTitle: ysa, Channel: "華音殿Music Channel"}, [2]string{"一生所愛", "盧冠廷"}},
		{Query{Title: "大话西游", Artist: "电影", VideoTitle: "电影《大话西游》插曲【一生所爱】卢冠廷", Channel: "Chan"}, [2]string{"一生所爱", "卢冠廷"}},
		{Query{Title: "还珠格格", Artist: "电视剧", VideoTitle: "电视剧《还珠格格》插曲【有一个姑娘】赵薇", Channel: "Chan"}, [2]string{"有一个姑娘", "赵薇"}},
		{Query{Title: "英雄本色", Artist: "电影", VideoTitle: "电影《英雄本色》主题曲【粤语】当年情", Channel: "张国荣"}, [2]string{"当年情", "张国荣"}},
		{Query{Title: "英雄本色", Artist: "电影", VideoTitle: "电影《英雄本色》主题曲【张国荣】当年情", Channel: "Chan"}, [2]string{"当年情", "张国荣"}},
	}
	for _, c := range cases {
		got := SearchQueries(c.q)
		first := SearchQueries(Query{Title: anyBracket.ReplaceAllString(c.q.Title, " "), Artist: c.q.Artist, ChannelArtist: c.q.ChannelArtist})[0]
		if got[0].Title != first.Title || got[0].Artist != first.Artist {
			t.Errorf("%q: first query %q/%q changed (want %q/%q)", c.q.Title, got[0].Title, got[0].Artist, first.Title, first.Artist)
		}
		if len(got) > 5 {
			t.Errorf("%q: %d queries", c.q.Title, len(got))
		}
		found := false
		var pairs [][2]string
		for _, g := range got {
			pairs = append(pairs, [2]string{g.Title, g.Artist})
			if Norm(g.Title) == Norm(c.want[0]) && Norm(g.Artist) == Norm(c.want[1]) {
				found = true
			}
		}
		if !found {
			t.Errorf("SearchQueries(%q, %q, video=%v) = %v, lacks %v", c.q.Title, c.q.Artist, c.q.VideoTitle != "", pairs, c.want)
		}
	}
	// Filler is never a guessed title.
	for _, raw := range []string{"动画片《葫芦娃》主题曲 儿歌合集 30分钟", "《新白娘子传奇》插曲 合集", "电影《唐伯虎点秋香》插曲【中文字幕】",
		"电影《英雄本色》主题曲【经典老歌】当年情", "电影《英雄本色》主题曲【KTV】当年情", "电影《英雄本色》主题曲【1993】当年情", "电影《英雄本色》主题曲【高清修复版】当年情"} {
		for _, p := range hintPairs(raw, "Chan") {
			if ytdlp.FillerOnly(p[0]) || ytdlp.Filler(p[1]) || strings.ContainsAny(p[0], "【】") {
				t.Errorf("hintPairs(%q) guessed %v", raw, p)
			}
		}
	}
	// A lyric line is not a title; a singer loses a leading "- ".
	if ps := hintPairs("周冬雨【一生所爱隐约在白云外苦海翻起爱恨在世间】", "Chan"); len(ps) != 0 {
		t.Errorf("lyric-length guess %v", ps)
	}
	if ps := hintPairs("电影《X》插曲【后来】- 周冬雨", "Chan"); fmt.Sprint(ps) != fmt.Sprint([][2]string{{"后来", "周冬雨"}, {"周冬雨", "后来"}}) {
		t.Errorf("dash singer %v", ps)
	}
	// A filler-only bracket title adds nothing that searches by title alone.
	for _, g := range SearchQueries(Query{Title: "粤语【千千阙歌】", Artist: "Chan"}) {
		if g.Artist == "" {
			t.Errorf("title-alone search %q", g.Title)
		}
	}
}
